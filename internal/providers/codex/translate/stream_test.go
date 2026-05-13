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
