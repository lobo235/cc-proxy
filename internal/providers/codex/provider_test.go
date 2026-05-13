package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lobo235/cc-proxy/internal/authstore"
	"github.com/lobo235/cc-proxy/internal/provider"
)

func TestProviderCountTokensUsesTranslatorCounter(t *testing.T) {
	p := Provider{}
	resp, err := p.CountTokens(context.Background(), provider.CountTokensCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.4",
			Raw: json.RawMessage(`{
				"model":"gpt-5.4",
				"messages":[{"role":"user","content":"hello world"}]
			}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.InputTokens <= 0 {
		t.Fatalf("input tokens = %d, want positive", resp.InputTokens)
	}
}

func TestProviderMessagesTranslatesUpstreamTextStream(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
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
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1}}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()
	p := Provider{Client: Client{BaseURL: upstream.URL, AuthStore: store}}
	out := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "sonnet",
			Raw:   json.RawMessage(`{"model":"sonnet","messages":[{"role":"user","content":"hello"}]}`),
		},
		Route: provider.Route{IncomingModel: "sonnet", UpstreamModel: "gpt-5.4"},
		Meta:  provider.CallMeta{RequestID: "req_123", SessionID: "sess-1"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if out.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", out.status)
	}
	got := out.body.String()
	if !strings.Contains(got, "event: message_start") || !strings.Contains(got, `"text":"Hello"`) {
		t.Fatalf("stream = %s", got)
	}
	if !strings.Contains(got, `"model":"sonnet"`) {
		t.Fatalf("stream model should use incoming model: %s", got)
	}
}

func TestProviderMessagesAppliesEffortOverride(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Reasoning *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		Include []string `json:"include"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}))
	defer upstream.Close()
	p := Provider{
		Client: Client{BaseURL: upstream.URL, AuthStore: store},
		Effort: "max",
	}
	out := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"low"}}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Reasoning == nil || captured.Reasoning.Effort != "xhigh" {
		t.Fatalf("reasoning = %+v, want xhigh", captured.Reasoning)
	}
	if !containsString(captured.Include, "reasoning.encrypted_content") {
		t.Fatalf("include = %+v, want reasoning.encrypted_content", captured.Include)
	}
}

type recordingOut struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func (o *recordingOut) StartStream(status int, header http.Header) (io.Writer, error) {
	o.status = status
	o.header = header
	return &o.body, nil
}

func (o *recordingOut) WriteJSON(status int, header http.Header, body any) error {
	o.status = status
	o.header = header
	return json.NewEncoder(&o.body).Encode(body)
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
