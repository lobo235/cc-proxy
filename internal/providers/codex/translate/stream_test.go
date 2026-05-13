package translate

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lobo235/cc-proxy/internal/sse"
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

func TestTranslateStreamEmitsBeforeUpstreamEOF(t *testing.T) {
	upstreamReader, upstreamWriter := io.Pipe()
	out := &notifyingWriter{ch: make(chan string, 16)}
	done := make(chan error, 1)
	go func() {
		done <- TranslateStream(upstreamReader, out, StreamOptions{MessageID: "msg_ccp", Model: "gpt-5.5"})
	}()

	_, err := io.WriteString(upstreamWriter, strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1"}}`,
		``,
		``,
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case chunk := <-out.ch:
		if !strings.Contains(chunk, "event: message_start") {
			t.Fatalf("first downstream chunk = %q, want message_start", chunk)
		}
	case <-done:
		t.Fatal("TranslateStream returned before upstream was closed")
	case <-time.After(time.Second):
		t.Fatal("TranslateStream did not emit before upstream EOF")
	}

	if err := upstreamWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("TranslateStream did not finish after upstream EOF")
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

func TestTranslateStreamReadDropsInvalidPages(t *testing.T) {
	tests := []struct {
		name      string
		filePath  string
		pages     string
		wantPages bool
	}{
		{
			name:      "empty pages on go file",
			filePath:  "/home/lobo235/dev/goscalp/internal/engine/vertical_dispatch.go",
			pages:     "",
			wantPages: false,
		},
		{
			name:      "non-empty pages on go file",
			filePath:  "/home/lobo235/dev/goscalp/internal/engine/vertical_dispatch.go",
			pages:     "1",
			wantPages: false,
		},
		{
			name:      "non-empty pages on pdf",
			filePath:  "/home/lobo235/dev/docs/spec.pdf",
			pages:     "1",
			wantPages: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := `{"file_path":"` + tt.filePath + `","offset":1,"limit":80,"pages":"` + tt.pages + `"}`
			upstream := strings.NewReader(strings.Join([]string{
				`event: response.output_item.added`,
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"toolu_read","name":"Read","arguments":""}}`,
				``,
				`event: response.function_call_arguments.delta`,
				`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":` + quoted(args) + `}`,
				``,
				`event: response.output_item.done`,
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"toolu_read","name":"Read","arguments":` + quoted(args) + `,"status":"completed"}}`,
				``,
			}, "\n"))
			var out bytes.Buffer
			if err := TranslateStream(upstream, &out, StreamOptions{MessageID: "msg_ccp", Model: "gpt-5.5"}); err != nil {
				t.Fatal(err)
			}
			events, err := sse.ParseAll(strings.NewReader(out.String()))
			if err != nil {
				t.Fatal(err)
			}
			var partial string
			for _, evt := range events {
				if evt.Event != "content_block_delta" {
					continue
				}
				var payload struct {
					Delta struct {
						PartialJSON string `json:"partial_json"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
					t.Fatal(err)
				}
				partial += payload.Delta.PartialJSON
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(partial), &got); err != nil {
				t.Fatalf("tool input JSON = %q: %v", partial, err)
			}
			if _, ok := got["pages"]; ok != tt.wantPages {
				t.Fatalf("pages present = %v, want %v; input = %#v", ok, tt.wantPages, got)
			}
			if got["file_path"] != tt.filePath {
				t.Fatalf("file_path = %#v, want %q", got["file_path"], tt.filePath)
			}
		})
	}
}

func TestSanitizeReadArgumentsRepairsOversizedLineOffset(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/source.go"
	var content strings.Builder
	for i := 0; i < 420; i++ {
		content.WriteString("line\n")
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		offset     any
		wantOffset any
	}{
		{name: "extra trailing zero", offset: float64(2200), wantOffset: float64(220)},
		{name: "concatenated junk digits", offset: float64(2200279), wantOffset: float64(220)},
		{name: "very large concatenation", offset: float64(220012457581260), wantOffset: float64(220)},
		{name: "already valid", offset: float64(200), wantOffset: float64(200)},
		{name: "slightly beyond EOF is left alone", offset: float64(430), wantOffset: float64(430)},
		{name: "single nonzero suffix is left alone", offset: float64(999), wantOffset: float64(999)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := map[string]any{
				"file_path": path,
				"offset":    tt.offset,
				"limit":     float64(120),
			}
			sanitizeReadArguments(input)
			if input["offset"] != tt.wantOffset {
				t.Fatalf("offset = %#v, want %#v; input=%#v", input["offset"], tt.wantOffset, input)
			}
		})
	}
}

func quoted(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

type notifyingWriter struct {
	mu sync.Mutex
	ch chan string
}

func (w *notifyingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ch <- string(p)
	return len(p), nil
}
