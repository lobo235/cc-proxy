package translate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lobo235/cc-proxy/internal/sse"
)

type StreamOptions struct {
	MessageID string
	Model     string
	OnUsage   func(Usage)
}

type Usage struct {
	InputTokens        int `json:"input_tokens,omitempty"`
	OutputTokens       int `json:"output_tokens,omitempty"`
	TotalTokens        int `json:"total_tokens,omitempty"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	} `json:"output_tokens_details,omitempty"`
}

type upstreamStreamEvent struct {
	Type        string `json:"type"`
	OutputIndex int    `json:"output_index"`
	Delta       string `json:"delta"`
	ItemID      string `json:"item_id,omitempty"`
	Name        string `json:"name,omitempty"`
	Arguments   string `json:"arguments,omitempty"`
	Item        struct {
		Type      string `json:"type"`
		ID        string `json:"id,omitempty"`
		CallID    string `json:"call_id,omitempty"`
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"item,omitempty"`
	Response struct {
		Usage Usage `json:"usage,omitempty"`
	} `json:"response,omitempty"`
}

func TranslateResponse(upstream io.Reader, opts StreamOptions) (map[string]any, error) {
	var stream bytes.Buffer
	if err := TranslateStream(upstream, &stream, opts); err != nil {
		return nil, err
	}
	events, err := sse.ParseAll(bytes.NewReader(stream.Bytes()))
	if err != nil {
		return nil, err
	}
	return collectResponse(events)
}

type collectedBlock struct {
	Index     int
	Kind      string
	Text      string
	ID        string
	Name      string
	Arguments string
}

func collectResponse(events []sse.Event) (map[string]any, error) {
	message := map[string]any{
		"id":            "msg_ccp",
		"type":          "message",
		"role":          "assistant",
		"model":         "",
		"content":       []any{},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         mapUsageToAnthropic(nil),
	}
	blocks := map[int]*collectedBlock{}
	for _, evt := range events {
		if evt.Data == "" {
			continue
		}
		switch evt.Event {
		case "message_start":
			var payload struct {
				Message map[string]any `json:"message"`
			}
			if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
				return nil, err
			}
			for k, v := range payload.Message {
				message[k] = v
			}
		case "content_block_start":
			var payload struct {
				Index        int             `json:"index"`
				ContentBlock json.RawMessage `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
				return nil, err
			}
			var block struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(payload.ContentBlock, &block); err != nil {
				return nil, err
			}
			blocks[payload.Index] = &collectedBlock{
				Index: payload.Index,
				Kind:  block.Type,
				Text:  block.Text,
				ID:    block.ID,
				Name:  block.Name,
			}
		case "content_block_delta":
			var payload struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
				return nil, err
			}
			block := blocks[payload.Index]
			if block == nil {
				continue
			}
			switch payload.Delta.Type {
			case "text_delta":
				block.Text += payload.Delta.Text
			case "input_json_delta":
				block.Arguments += payload.Delta.PartialJSON
			}
		case "message_delta":
			var payload struct {
				Delta struct {
					StopReason   string `json:"stop_reason"`
					StopSequence any    `json:"stop_sequence"`
				} `json:"delta"`
				Usage map[string]int `json:"usage"`
			}
			if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
				return nil, err
			}
			if payload.Delta.StopReason != "" {
				message["stop_reason"] = payload.Delta.StopReason
			}
			message["stop_sequence"] = payload.Delta.StopSequence
			if payload.Usage != nil {
				message["usage"] = payload.Usage
			}
		}
	}
	indexes := make([]int, 0, len(blocks))
	for idx := range blocks {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	content := make([]any, 0, len(indexes))
	for _, idx := range indexes {
		block := blocks[idx]
		switch block.Kind {
		case "text":
			content = append(content, map[string]any{"type": "text", "text": block.Text})
		case "tool_use":
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    block.ID,
				"name":  block.Name,
				"input": decodeToolInput(block.Name, block.Arguments),
			})
		}
	}
	message["content"] = content
	return message, nil
}

func decodeToolInput(name, arguments string) any {
	if arguments == "" {
		return map[string]any{}
	}
	arguments = sanitizeToolArguments(name, arguments)
	var input any
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return map[string]any{}
	}
	if input == nil {
		return map[string]any{}
	}
	return input
}

type downstreamBlock struct {
	Index       int
	Kind        string
	Name        string
	Arguments   string
	ArgumentsOK bool
}

func TranslateStream(upstream io.Reader, out io.Writer, opts StreamOptions) error {
	messageStarted := false
	nextIndex := 0
	blocks := map[int]downstreamBlock{}
	var usage *Usage
	stopReason := "end_turn"
	emit := func(event string, data any) error {
		encoded, err := sse.EncodeEvent(event, data)
		if err != nil {
			return err
		}
		_, err = io.WriteString(out, encoded)
		return err
	}
	ensureMessageStart := func() error {
		if messageStarted {
			return nil
		}
		messageStarted = true
		if err := emit("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            opts.MessageID,
				"type":          "message",
				"role":          "assistant",
				"model":         opts.Model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]int{
					"input_tokens":                0,
					"output_tokens":               0,
					"cache_creation_input_tokens": 0,
					"cache_read_input_tokens":     0,
				},
			},
		}); err != nil {
			return err
		}
		return emit("ping", map[string]string{"type": "ping"})
	}

	if err := sse.Parse(upstream, func(evt sse.Event) error {
		if evt.Data == "" {
			return nil
		}
		var payload upstreamStreamEvent
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			return nil
		}
		typ := payload.Type
		if typ == "" {
			typ = evt.Event
		}
		switch typ {
		case "response.output_item.added":
			switch payload.Item.Type {
			case "message":
				if err := ensureMessageStart(); err != nil {
					return err
				}
				idx := nextIndex
				nextIndex++
				blocks[payload.OutputIndex] = downstreamBlock{Index: idx, Kind: "text"}
				if err := emit("content_block_start", map[string]any{
					"type":          "content_block_start",
					"index":         idx,
					"content_block": map[string]string{"type": "text", "text": ""},
				}); err != nil {
					return err
				}
			case "function_call":
				if err := ensureMessageStart(); err != nil {
					return err
				}
				idx := nextIndex
				nextIndex++
				stopReason = "tool_use"
				blocks[payload.OutputIndex] = downstreamBlock{Index: idx, Kind: "tool_use", Name: payload.Item.Name}
				if err := emit("content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": idx,
					"content_block": map[string]any{
						"type":  "tool_use",
						"id":    toolUseID(payload.Item.CallID, payload.Item.ID),
						"name":  payload.Item.Name,
						"input": map[string]any{},
					},
				}); err != nil {
					return err
				}
			}
		case "response.output_text.delta":
			block, ok := blocks[payload.OutputIndex]
			if !ok || block.Kind != "text" || payload.Delta == "" {
				return nil
			}
			if err := emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": block.Index,
				"delta": map[string]string{"type": "text_delta", "text": payload.Delta},
			}); err != nil {
				return err
			}
		case "response.function_call_arguments.delta":
			block, ok := blocks[payload.OutputIndex]
			if !ok || block.Kind != "tool_use" || payload.Delta == "" {
				return nil
			}
			block.Arguments += payload.Delta
			block.ArgumentsOK = true
			blocks[payload.OutputIndex] = block
		case "response.function_call_arguments.done":
			block, ok := blocks[payload.OutputIndex]
			if !ok || block.Kind != "tool_use" || payload.Arguments == "" {
				return nil
			}
			if !block.ArgumentsOK {
				block.Arguments = payload.Arguments
			}
			block.ArgumentsOK = true
			blocks[payload.OutputIndex] = block
		case "response.output_item.done":
			block, ok := blocks[payload.OutputIndex]
			if !ok {
				return nil
			}
			delete(blocks, payload.OutputIndex)
			if block.Kind == "tool_use" {
				if block.Name == "" {
					block.Name = payload.Item.Name
				}
				if !block.ArgumentsOK && payload.Item.Arguments != "" {
					block.Arguments = payload.Item.Arguments
					block.ArgumentsOK = true
				}
				if block.ArgumentsOK {
					arguments := sanitizeToolArguments(block.Name, block.Arguments)
					if err := emit("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": block.Index,
						"delta": map[string]string{"type": "input_json_delta", "partial_json": arguments},
					}); err != nil {
						return err
					}
				}
			}
			if err := emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": block.Index}); err != nil {
				return err
			}
		case "response.completed":
			u := payload.Response.Usage
			usage = &u
		case "response.failed", "response.error", "error":
			if err := ensureMessageStart(); err != nil {
				return err
			}
			return emit("error", map[string]any{
				"type":  "error",
				"error": map[string]string{"type": "api_error", "message": "Upstream error"},
			})
		}
		return nil
	}); err != nil {
		return err
	}
	if err := ensureMessageStart(); err != nil {
		return err
	}
	if err := emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": mapUsageToAnthropic(usage),
	}); err != nil {
		return fmt.Errorf("emitting message delta: %w", err)
	}
	if opts.OnUsage != nil && usage != nil {
		opts.OnUsage(*usage)
	}
	return emit("message_stop", map[string]string{"type": "message_stop"})
}

func toolUseID(callID, fallback string) string {
	if callID != "" {
		return callID
	}
	if fallback != "" {
		return fallback
	}
	return "toolu_ccp"
}

func sanitizeToolArguments(name, arguments string) string {
	if arguments == "" {
		return arguments
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return arguments
	}
	if name == "Read" {
		sanitizeReadArguments(input)
	}
	out, err := json.Marshal(input)
	if err != nil {
		return arguments
	}
	return string(out)
}

func sanitizeReadArguments(input map[string]any) {
	pages, ok := input["pages"]
	path, _ := input["file_path"].(string)
	if ok {
		if strings.ToLower(filepath.Ext(path)) != ".pdf" {
			delete(input, "pages")
		} else {
			switch v := pages.(type) {
			case nil:
				delete(input, "pages")
			case string:
				if strings.TrimSpace(v) == "" {
					delete(input, "pages")
				}
			case []any:
				if len(v) == 0 {
					delete(input, "pages")
				}
			}
		}
	}
	if strings.ToLower(filepath.Ext(path)) == ".pdf" {
		return
	}
	repairReadOffset(input, path)
}

func repairReadOffset(input map[string]any, path string) {
	offset, ok := numericValue(input["offset"])
	if !ok || offset <= 0 {
		return
	}
	lines, ok := countLines(path)
	if !ok || lines <= 0 || offset <= lines {
		return
	}
	repaired, ok := repairConcatenatedOffset(offset, lines)
	if ok {
		input["offset"] = float64(repaired)
	}
}

func repairConcatenatedOffset(offset, lines int64) (int64, bool) {
	if offset < lines*2 {
		return 0, false
	}
	text := strconv.FormatInt(offset, 10)
	for split := len(text) - 1; split > 0; split-- {
		candidate, err := strconv.ParseInt(text[:split], 10, 64)
		if err != nil || candidate <= 0 || candidate > lines {
			continue
		}
		suffix := text[split:]
		if len(suffix) >= 2 || strings.Trim(suffix, "0") == "" {
			return candidate, true
		}
		return 0, false
	}
	return 0, false
}

func numericValue(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v != float64(int64(v)) {
			return 0, false
		}
		return int64(v), true
	case json.Number:
		i, err := v.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func countLines(path string) (int64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var lines int64
	for scanner.Scan() {
		lines++
	}
	if scanner.Err() != nil {
		return 0, false
	}
	if lines == 0 {
		info, err := file.Stat()
		if err == nil && info.Size() > 0 {
			return 1, true
		}
	}
	return lines, true
}

func mapUsageToAnthropic(u *Usage) map[string]int {
	if u == nil {
		return map[string]int{
			"input_tokens":                0,
			"output_tokens":               0,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		}
	}
	cached := u.InputTokensDetails.CachedTokens
	input := u.InputTokens - cached
	if input < 0 {
		input = 0
	}
	return map[string]int{
		"input_tokens":                input,
		"output_tokens":               u.OutputTokens,
		"cache_creation_input_tokens": 0,
		"cache_read_input_tokens":     cached,
	}
}
