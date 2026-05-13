package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

func TestProviderCountTokensUsesUpstreamInputTokensWhenAvailable(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	var gotPath string
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Model  string `json:"model"`
			Stream *bool  `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel = body.Model
		if body.Stream != nil {
			t.Fatal("input token request should not include stream")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"object":"response.input_tokens","input_tokens":42}`))
	}))
	defer upstream.Close()

	p := Provider{Client: Client{BaseURL: upstream.URL + "/responses", AuthStore: store}}
	resp, err := p.CountTokens(context.Background(), provider.CountTokensCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello world"}]}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123", SessionID: "sess-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.InputTokens != 42 {
		t.Fatalf("input tokens = %d, want 42", resp.InputTokens)
	}
	if gotPath != "/responses/input_tokens" {
		t.Fatalf("path = %q, want /responses/input_tokens", gotPath)
	}
	if gotModel != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", gotModel)
	}
}

func TestProviderCountTokensFallsBackWhenUpstreamUnsupported(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	p := Provider{Client: Client{BaseURL: upstream.URL + "/responses", AuthStore: store}}
	resp, err := p.CountTokens(context.Background(), provider.CountTokensCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello world"}]}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.InputTokens <= 0 || resp.InputTokens == 42 {
		t.Fatalf("input tokens = %d, want positive fallback estimate", resp.InputTokens)
	}
}

func TestProviderCountTokensSkipsEndpointAfterNotFound(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	p := Provider{Client: Client{BaseURL: upstream.URL + "/responses", AuthStore: store}, Verbose: true}
	call := provider.CountTokensCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello world"}]}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123"},
	}
	if _, err := p.CountTokens(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	call.Meta.RequestID = "req_456"
	if _, err := p.CountTokens(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
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
			Model:  "sonnet",
			Stream: boolPtr(true),
			Raw:    json.RawMessage(`{"model":"sonnet","messages":[{"role":"user","content":"hello"}],"stream":true}`),
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
	if !strings.Contains(got, `"model":"gpt-5.4"`) {
		t.Fatalf("stream model should use upstream model: %s", got)
	}
}

func TestProviderMessagesStatefulResponsesSendOnlyNewMessages(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	var bodies []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		responseID := fmt.Sprintf("resp_%d", len(bodies))
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.created`,
			`data: {"type":"response.created","response":{"id":"` + responseID + `"}}`,
			``,
			`event: response.output_item.added`,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1"}}`,
			``,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","output_index":0,"delta":"ok"}`,
			``,
			`event: response.output_item.done`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message"}}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"` + responseID + `","usage":{"input_tokens":1,"output_tokens":1}}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()
	p := Provider{Client: Client{BaseURL: upstream.URL, AuthStore: store}, StatefulResponses: true}
	firstOut := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model:  "gpt-5.5",
			Stream: boolPtr(true),
			Raw:    json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":true}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_1", SessionID: "sess-1"},
	}, firstOut)
	if err != nil {
		t.Fatal(err)
	}
	secondOut := &recordingOut{}
	err = p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model:  "gpt-5.5",
			Stream: boolPtr(true),
			Raw: json.RawMessage(`{
				"model":"gpt-5.5",
				"messages":[
					{"role":"user","content":"hello"},
					{"role":"assistant","content":"ok"},
					{"role":"user","content":"next"}
				],
				"stream":true
			}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_2", SessionID: "sess-1"},
	}, secondOut)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("upstream bodies = %d, want 2", len(bodies))
	}
	if bodies[0]["previous_response_id"] != nil {
		t.Fatalf("first previous_response_id = %v, want absent", bodies[0]["previous_response_id"])
	}
	if bodies[0]["store"] != true {
		t.Fatalf("first store = %v, want true", bodies[0]["store"])
	}
	if bodies[1]["previous_response_id"] != "resp_1" {
		t.Fatalf("second previous_response_id = %v, want resp_1", bodies[1]["previous_response_id"])
	}
	input, ok := bodies[1]["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("second input = %#v, want one new message", bodies[1]["input"])
	}
	msg := input[0].(map[string]any)
	content := msg["content"].([]any)
	part := content[0].(map[string]any)
	if msg["role"] != "user" || part["text"] != "next" {
		t.Fatalf("second input[0] = %#v, want only next user message", input[0])
	}
}

func TestProviderMessagesFallsBackWhenStatefulStoreRejected(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	var bodies []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			http.Error(w, `{"detail":"Store must be set to false"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_item.added`,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1"}}`,
			``,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","output_index":0,"delta":"ok"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_retry","usage":{"input_tokens":1,"output_tokens":1}}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()

	p := Provider{Client: Client{BaseURL: upstream.URL, AuthStore: store}, StatefulResponses: true}
	out := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model:  "gpt-5.5",
			Stream: boolPtr(true),
			Raw:    json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":true}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_store_rejected", SessionID: "sess-1"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(bodies))
	}
	if bodies[0]["store"] != true {
		t.Fatalf("first store = %v, want true", bodies[0]["store"])
	}
	if bodies[1]["store"] != false {
		t.Fatalf("retry store = %v, want false", bodies[1]["store"])
	}
	if bodies[1]["previous_response_id"] != nil {
		t.Fatalf("retry previous_response_id = %v, want absent", bodies[1]["previous_response_id"])
	}
	if out.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", out.status)
	}
	previousID, messageStart := p.previousResponse("sess-1", 3, true)
	if previousID != "" || messageStart != 0 {
		t.Fatalf("remembered state = (%q, %d), want empty after stateful rejection", previousID, messageStart)
	}
}

func TestProviderMessagesTranslatesUpstreamTextNonStream(t *testing.T) {
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
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":9,"output_tokens":1}}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()
	p := Provider{Client: Client{BaseURL: upstream.URL, AuthStore: store}}
	out := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123", SessionID: "sess-1"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if out.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", out.status)
	}
	got := out.body.String()
	for _, want := range []string{
		`"type":"message"`,
		`"model":"gpt-5.5"`,
		`"type":"text"`,
		`"text":"Hello"`,
		`"input_tokens":9`,
		`"output_tokens":1`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("response missing %s:\n%s", want, got)
		}
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

func TestProviderMessagesCapsCompactionEffort(t *testing.T) {
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
	}
	out := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   compactionRequestRaw(t),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Reasoning == nil || captured.Reasoning.Effort != "medium" {
		t.Fatalf("reasoning = %+v, want medium for compaction-shaped request", captured.Reasoning)
	}
	if !containsString(captured.Include, "reasoning.encrypted_content") {
		t.Fatalf("include = %+v, want reasoning.encrypted_content", captured.Include)
	}
}

func compactionRequestRaw(t *testing.T) json.RawMessage {
	t.Helper()
	messages := make([]map[string]string, 12)
	for i := range messages {
		content := "brief"
		if i == 0 {
			content = strings.Repeat("context ", 30_000)
		}
		messages[i] = map[string]string{"role": "user", "content": content}
	}
	raw, err := json.Marshal(map[string]any{
		"model":      "gpt-5.5",
		"messages":   messages,
		"tools":      []map[string]any{{"name": "compact", "input_schema": map[string]any{"type": "object"}}},
		"max_tokens": 20_000,
		"output_config": map[string]any{
			"effort": "max",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func boolPtr(v bool) *bool {
	return &v
}

func TestProviderMessagesLogsUpstreamResponse(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}))
	defer upstream.Close()
	p := Provider{
		Client: Client{BaseURL: upstream.URL, AuthStore: store},
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}
	out := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	got := logs.String()
	for _, want := range []string{
		`"msg":"codex upstream response"`,
		`"request_id":"req_123"`,
		`"status":200`,
		`"content_type":"text/event-stream"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %s:\n%s", want, got)
		}
	}
}

func TestProviderMessagesReturnsUpstreamErrorWithSnippet(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"contract changed"}`, http.StatusBadGateway)
	}))
	defer upstream.Close()
	p := Provider{Client: Client{BaseURL: upstream.URL, AuthStore: store}}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123"},
	}, &recordingOut{})
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if !strings.Contains(err.Error(), "codex upstream returned 502") || !strings.Contains(err.Error(), "contract changed") {
		t.Fatalf("error = %v", err)
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
