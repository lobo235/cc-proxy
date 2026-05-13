package translate

import (
	"bytes"
	"strings"
	"testing"
)

func TestTranslateStreamTextResponse(t *testing.T) {
	upstream := strings.NewReader(strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1"}}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":3}}}}`,
		``,
	}, "\n"))
	var out bytes.Buffer
	if err := TranslateStream(upstream, &out, StreamOptions{MessageID: "msg_ccp", Model: "gpt-5.4"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"event: message_start",
		`"id":"msg_ccp"`,
		"event: content_block_start",
		`"type":"text"`,
		"event: content_block_delta",
		`"text":"Hello"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		`"input_tokens":7`,
		`"cache_read_input_tokens":3`,
		"event: message_stop",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("translated stream missing %q:\n%s", want, got)
		}
	}
}

func TestTranslateStreamReportsFullCodexUsage(t *testing.T) {
	upstream := strings.NewReader(strings.Join([]string{
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}}`,
		``,
	}, "\n"))
	var out bytes.Buffer
	var got Usage
	called := false
	if err := TranslateStream(upstream, &out, StreamOptions{
		MessageID: "msg_ccp",
		Model:     "gpt-5.5",
		OnUsage: func(u Usage) {
			got = u
			called = true
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("usage callback was not called")
	}
	if got.InputTokens != 10 || got.InputTokensDetails.CachedTokens != 3 || got.OutputTokens != 4 || got.OutputTokensDetails.ReasoningTokens != 2 || got.TotalTokens != 14 {
		t.Fatalf("usage = %+v, want full usage fields", got)
	}
}

func TestTranslateResponseCollectsNonStreamMessage(t *testing.T) {
	upstream := strings.NewReader(strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1"}}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","output_index":0,"delta":" world"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":3}}}}`,
		``,
	}, "\n"))
	resp, err := TranslateResponse(upstream, StreamOptions{MessageID: "msg_ccp", Model: "gpt-5.5"})
	if err != nil {
		t.Fatal(err)
	}
	if resp["id"] != "msg_ccp" || resp["model"] != "gpt-5.5" || resp["stop_reason"] != "end_turn" {
		t.Fatalf("response metadata = %+v", resp)
	}
	content, ok := resp["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v", resp["content"])
	}
	text, ok := content[0].(map[string]any)
	if !ok || text["type"] != "text" || text["text"] != "Hello world" {
		t.Fatalf("text block = %#v", content[0])
	}
	usage, ok := resp["usage"].(map[string]int)
	if !ok {
		t.Fatalf("usage = %#v", resp["usage"])
	}
	if usage["input_tokens"] != 7 || usage["cache_read_input_tokens"] != 3 || usage["output_tokens"] != 2 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestTranslateStreamFunctionCallResponse(t *testing.T) {
	upstream := strings.NewReader(strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"toolu_123","name":"lookup_weather","arguments":""}}`,
		``,
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\":\"Denver\"}"}`,
		``,
		`event: response.function_call_arguments.done`,
		`data: {"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_1","name":"lookup_weather","arguments":"{\"city\":\"Denver\"}"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"toolu_123","name":"lookup_weather","arguments":"{\"city\":\"Denver\"}","status":"completed"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":1}}}`,
		``,
	}, "\n"))
	var out bytes.Buffer
	if err := TranslateStream(upstream, &out, StreamOptions{MessageID: "msg_ccp", Model: "gpt-5.5"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"event: message_start",
		`"model":"gpt-5.5"`,
		"event: content_block_start",
		`"type":"tool_use"`,
		`"id":"toolu_123"`,
		`"name":"lookup_weather"`,
		`"input":{}`,
		"event: content_block_delta",
		`"type":"input_json_delta"`,
		`"partial_json":"{\"city\":\"Denver\"}"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"tool_use"`,
		`"input_tokens":5`,
		`"output_tokens":1`,
		"event: message_stop",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("translated stream missing %q:\n%s", want, got)
		}
	}
}
