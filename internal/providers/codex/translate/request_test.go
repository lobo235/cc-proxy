package translate

import (
	"encoding/json"
	"testing"

	"github.com/lobo235/cc-proxy/internal/provider"
)

func TestTranslateUserTextMessage(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model:    "gpt-5.4",
		Messages: json.RawMessage(`[{"role":"user","content":"hello"}]`),
		Raw:      json.RawMessage(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got.Model)
	}
	if !got.Stream {
		t.Fatal("stream = false, want true")
	}
	if got.Store {
		t.Fatal("store = true, want false")
	}
	if got.Text.Verbosity != "low" {
		t.Fatalf("verbosity = %q, want low", got.Text.Verbosity)
	}
	if len(got.Input) != 1 {
		t.Fatalf("input len = %d, want 1", len(got.Input))
	}
	item := got.Input[0]
	if item.Type != "message" || item.Role != "user" {
		t.Fatalf("input[0] = %+v, want user message", item)
	}
	if len(item.Content) != 1 || item.Content[0].Type != "input_text" || item.Content[0].Text != "hello" {
		t.Fatalf("content = %+v, want input_text hello", item.Content)
	}
}

func TestTranslateSystemInstructionsDropsBillingHeader(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw: json.RawMessage(`{
			"model":"gpt-5.4",
			"system":[
				{"type":"text","text":"x-anthropic-billing-header: ignore me"},
				{"type":"text","text":"follow instructions"},
				{"type":"text","text":"be concise"}
			],
			"messages":[{"role":"user","content":"hello"}]
		}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Instructions != "follow instructions\n\nbe concise" {
		t.Fatalf("instructions = %q", got.Instructions)
	}
}

func TestTranslateToolUseAndToolResult(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw: json.RawMessage(`{
			"model":"gpt-5.4",
			"messages":[
				{"role":"assistant","content":[
					{"type":"text","text":"checking"},
					{"type":"tool_use","id":"toolu_123","name":"lookup_weather","input":{"city":"Denver"}}
				]},
				{"role":"user","content":[
					{"type":"tool_result","tool_use_id":"toolu_123","content":[{"type":"text","text":"sunny"}]}
				]}
			]
		}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Input) != 3 {
		t.Fatalf("input len = %d, want 3: %+v", len(got.Input), got.Input)
	}
	if got.Input[0].Type != "message" || got.Input[0].Role != "assistant" || got.Input[0].Content[0].Type != "output_text" {
		t.Fatalf("assistant text item = %+v", got.Input[0])
	}
	if got.Input[1].Type != "function_call" || got.Input[1].CallID != "toolu_123" || got.Input[1].Name != "lookup_weather" {
		t.Fatalf("function call item = %+v", got.Input[1])
	}
	if got.Input[1].Arguments != `{"city":"Denver"}` {
		t.Fatalf("arguments = %q", got.Input[1].Arguments)
	}
	if got.Input[2].Type != "function_call_output" || got.Input[2].CallID != "toolu_123" || got.Input[2].Output != "sunny" {
		t.Fatalf("function output item = %+v", got.Input[2])
	}
}
