package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lobo235/cc-proxy/internal/provider"
)

type Options struct {
	SessionID   string
	ServiceTier string
}

type ResponsesRequest struct {
	Model             string          `json:"model"`
	Instructions      string          `json:"instructions,omitempty"`
	Input             []InputItem     `json:"input"`
	Tools             []Tool          `json:"tools,omitempty"`
	ToolChoice        any             `json:"tool_choice,omitempty"`
	ParallelToolCalls bool            `json:"parallel_tool_calls"`
	Reasoning         *Reasoning      `json:"reasoning,omitempty"`
	Store             bool            `json:"store"`
	Stream            bool            `json:"stream"`
	Include           []string        `json:"include,omitempty"`
	ServiceTier       string          `json:"service_tier,omitempty"`
	PromptCacheKey    string          `json:"prompt_cache_key,omitempty"`
	Text              TextConfig      `json:"text"`
	ClientMetadata    json.RawMessage `json:"client_metadata,omitempty"`
}

type InputItem struct {
	Type      string        `json:"type"`
	Role      string        `json:"role,omitempty"`
	Content   []ContentPart `json:"content,omitempty"`
	CallID    string        `json:"call_id,omitempty"`
	Name      string        `json:"name,omitempty"`
	Arguments string        `json:"arguments,omitempty"`
	Output    string        `json:"output,omitempty"`
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Reasoning struct {
	Effort string `json:"effort,omitempty"`
}

type TextConfig struct {
	Verbosity string `json:"verbosity,omitempty"`
	Format    any    `json:"format,omitempty"`
}

type anthropicRequest struct {
	Model        string             `json:"model"`
	Messages     []anthropicMessage `json:"messages"`
	System       json.RawMessage    `json:"system,omitempty"`
	Tools        []anthropicTool    `json:"tools,omitempty"`
	ToolChoice   *toolChoice        `json:"tool_choice,omitempty"`
	OutputConfig *outputConfig      `json:"output_config,omitempty"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Source    *imageSource    `json:"source,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type toolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type outputConfig struct {
	Effort string        `json:"effort,omitempty"`
	Format *outputFormat `json:"format,omitempty"`
}

type outputFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict *bool           `json:"strict,omitempty"`
}

func Translate(req provider.AnthropicMessagesRequest, opts Options) (ResponsesRequest, error) {
	anthropic, err := decodeRequest(req)
	if err != nil {
		return ResponsesRequest{}, err
	}
	input, err := buildInput(anthropic.Messages)
	if err != nil {
		return ResponsesRequest{}, err
	}
	out := ResponsesRequest{
		Model:             anthropic.Model,
		Input:             input,
		Store:             false,
		Stream:            true,
		ParallelToolCalls: true,
		ToolChoice:        mapToolChoice(anthropic.ToolChoice),
		Text:              TextConfig{Verbosity: "low"},
	}
	if instructions, err := buildInstructions(anthropic.System); err != nil {
		return ResponsesRequest{}, err
	} else if instructions != "" {
		out.Instructions = instructions
	}
	if len(anthropic.Tools) > 0 {
		out.Tools = make([]Tool, 0, len(anthropic.Tools))
		for _, tool := range anthropic.Tools {
			out.Tools = append(out.Tools, Tool{
				Type:        "function",
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			})
		}
	}
	if opts.SessionID != "" {
		out.PromptCacheKey = opts.SessionID
	}
	if opts.ServiceTier != "" {
		out.ServiceTier = opts.ServiceTier
	}
	return out, nil
}

func decodeRequest(req provider.AnthropicMessagesRequest) (anthropicRequest, error) {
	var decoded anthropicRequest
	data := req.Raw
	if len(data) == 0 {
		data, _ = json.Marshal(req)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return anthropicRequest{}, fmt.Errorf("decoding anthropic request: %w", err)
	}
	if decoded.Model == "" {
		decoded.Model = req.Model
	}
	if len(decoded.Messages) == 0 && len(req.Messages) > 0 {
		if err := json.Unmarshal(req.Messages, &decoded.Messages); err != nil {
			return anthropicRequest{}, fmt.Errorf("decoding messages: %w", err)
		}
	}
	return decoded, nil
}

func buildInput(messages []anthropicMessage) ([]InputItem, error) {
	out := make([]InputItem, 0, len(messages))
	for _, msg := range messages {
		blocks, err := normalizeContent(msg.Content)
		if err != nil {
			return nil, err
		}
		if msg.Role == "user" {
			parts := make([]ContentPart, 0, len(blocks))
			flushParts := func() {
				if len(parts) == 0 {
					return
				}
				out = append(out, InputItem{Type: "message", Role: "user", Content: parts})
				parts = nil
			}
			for _, block := range blocks {
				switch block.Type {
				case "text":
					parts = append(parts, ContentPart{Type: "input_text", Text: block.Text})
				case "image":
					if url := imageToURL(block); url != "" {
						parts = append(parts, ContentPart{Type: "input_image", ImageURL: url})
					}
				case "tool_result":
					flushParts()
					output, err := ToolResultToString(block.Content)
					if err != nil {
						return nil, err
					}
					if block.IsError {
						output = "[tool execution error]\n" + output
					}
					out = append(out, InputItem{Type: "function_call_output", CallID: block.ToolUseID, Output: output})
				}
			}
			flushParts()
			continue
		}

		textParts := make([]ContentPart, 0, len(blocks))
		flushText := func() {
			if len(textParts) == 0 {
				return
			}
			out = append(out, InputItem{Type: "message", Role: "assistant", Content: textParts})
			textParts = nil
		}
		for _, block := range blocks {
			switch block.Type {
			case "text":
				textParts = append(textParts, ContentPart{Type: "output_text", Text: block.Text})
			case "tool_use":
				flushText()
				args := "{}"
				if len(block.Input) > 0 && string(block.Input) != "null" {
					args = string(block.Input)
				}
				out = append(out, InputItem{
					Type:      "function_call",
					CallID:    block.ID,
					Name:      block.Name,
					Arguments: args,
				})
			}
		}
		flushText()
	}
	return out, nil
}

func normalizeContent(raw json.RawMessage) ([]contentBlock, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []contentBlock{{Type: "text", Text: text}}, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("decoding message content: %w", err)
	}
	return blocks, nil
}

func imageToURL(block contentBlock) string {
	if block.Source == nil {
		return ""
	}
	if block.Source.Type == "url" {
		return block.Source.URL
	}
	if block.Source.Type == "base64" {
		return fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data)
	}
	return ""
}

func ToolResultToString(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("decoding tool result content: %w", err)
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch {
		case block.Type == "text" && block.Text != "":
			parts = append(parts, block.Text)
		case block.Type == "image" && block.Source != nil && block.Source.Type == "base64" && block.Source.MediaType != "":
			parts = append(parts, fmt.Sprintf("[image omitted: %s]", block.Source.MediaType))
		case block.Type == "image" && block.Source != nil && block.Source.Type == "url":
			parts = append(parts, "[image omitted: url]")
		default:
			typ := block.Type
			if typ == "" {
				typ = "unknown"
			}
			parts = append(parts, fmt.Sprintf("[unsupported content block omitted: %s]", typ))
		}
	}
	return strings.Join(parts, "\n"), nil
}

func buildInstructions(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.HasPrefix(text, "x-anthropic-billing-header:") {
			return "", nil
		}
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("decoding system content: %w", err)
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" || block.Text == "" {
			continue
		}
		if strings.HasPrefix(block.Text, "x-anthropic-billing-header:") {
			continue
		}
		texts = append(texts, block.Text)
	}
	return strings.Join(texts, "\n\n"), nil
}

func mapToolChoice(choice *toolChoice) any {
	if choice == nil {
		return "auto"
	}
	switch choice.Type {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		if choice.Name != "" {
			return map[string]string{"type": "function", "name": choice.Name}
		}
		return "required"
	default:
		return "auto"
	}
}
