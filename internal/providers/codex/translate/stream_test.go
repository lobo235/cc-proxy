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
