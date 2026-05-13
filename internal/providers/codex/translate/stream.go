package translate

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/lobo235/cc-proxy/internal/sse"
)

type StreamOptions struct {
	MessageID string
	Model     string
}

type codexUsage struct {
	InputTokens        int `json:"input_tokens,omitempty"`
	OutputTokens       int `json:"output_tokens,omitempty"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
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
		Usage codexUsage `json:"usage,omitempty"`
	} `json:"response,omitempty"`
}

type downstreamBlock struct {
	Index       int
	Kind        string
	Arguments   string
	ArgumentsOK bool
}

func TranslateStream(upstream io.Reader, out io.Writer, opts StreamOptions) error {
	events, err := sse.ParseAll(upstream)
	if err != nil {
		return err
	}
	messageStarted := false
	nextIndex := 0
	blocks := map[int]downstreamBlock{}
	var usage *codexUsage
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

	for _, evt := range events {
		if evt.Data == "" {
			continue
		}
		var payload upstreamStreamEvent
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			continue
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
				blocks[payload.OutputIndex] = downstreamBlock{Index: idx, Kind: "tool_use"}
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
				continue
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
				continue
			}
			block.Arguments += payload.Delta
			block.ArgumentsOK = true
			blocks[payload.OutputIndex] = block
			if err := emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": block.Index,
				"delta": map[string]string{"type": "input_json_delta", "partial_json": payload.Delta},
			}); err != nil {
				return err
			}
		case "response.function_call_arguments.done":
			block, ok := blocks[payload.OutputIndex]
			if !ok || block.Kind != "tool_use" || block.ArgumentsOK || payload.Arguments == "" {
				continue
			}
			block.Arguments = payload.Arguments
			block.ArgumentsOK = true
			blocks[payload.OutputIndex] = block
			if err := emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": block.Index,
				"delta": map[string]string{"type": "input_json_delta", "partial_json": payload.Arguments},
			}); err != nil {
				return err
			}
		case "response.output_item.done":
			block, ok := blocks[payload.OutputIndex]
			if !ok {
				continue
			}
			delete(blocks, payload.OutputIndex)
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

func mapUsageToAnthropic(u *codexUsage) map[string]int {
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
