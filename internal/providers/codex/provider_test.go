package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lobo235/cc-proxy/internal/authstore"
	"github.com/lobo235/cc-proxy/internal/provider"
	"github.com/lobo235/cc-proxy/internal/providers/codex/translate"
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

func TestProviderMessagesRoutesAutoModeClassifierToFastModel(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Model       string `json:"model"`
		ServiceTier string `json:"service_tier"`
		Reasoning   *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}))
	defer upstream.Close()
	p := Provider{Client: Client{BaseURL: upstream.URL, AuthStore: store}}
	out := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.5",
			Raw:   autoModeClassifierRequestRaw(t),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_classifier", SessionID: "sess-1"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Model != autoModeClassifierModel {
		t.Fatalf("model = %q, want %q", captured.Model, autoModeClassifierModel)
	}
	if captured.ServiceTier != autoModeClassifierServiceTier {
		t.Fatalf("service_tier = %q, want %q", captured.ServiceTier, autoModeClassifierServiceTier)
	}
	if captured.Reasoning == nil || captured.Reasoning.Effort != autoModeClassifierEffort {
		t.Fatalf("reasoning = %+v, want effort %q", captured.Reasoning, autoModeClassifierEffort)
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

func autoModeClassifierRequestRaw(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"model":      "gpt-5.5",
		"system":     strings.Repeat("tool safety classifier policy ", 2_500),
		"max_tokens": 64,
		"messages": []map[string]string{
			{"role": "user", "content": "Decide whether this Skill tool call is allowed."},
			{"role": "assistant", "content": "Return only the safety decision."},
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

func TestProviderMessagesLogsStreamUpstreamError(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.error`,
			`data: {"type":"response.error","error":{"type":"server_error","code":"overloaded","message":"model overloaded; retry later"}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()
	p := Provider{
		Client: Client{BaseURL: upstream.URL, AuthStore: store},
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}
	out := &recordingOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model:  "gpt-5.5",
			Stream: boolPtr(true),
			Raw:    json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":true}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_123"},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.body.String(), `"message":"model overloaded; retry later"`) {
		t.Fatalf("stream missing upstream error message:\n%s", out.body.String())
	}
	got := logs.String()
	for _, want := range []string{
		`"msg":"codex stream upstream error"`,
		`"request_id":"req_123"`,
		`"event_type":"response.error"`,
		`"upstream_error_type":"server_error"`,
		`"upstream_error_code":"overloaded"`,
		`"message":"model overloaded; retry later"`,
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

func TestTranslatedBodyBreakdownReportsBytesAndFingerprint(t *testing.T) {
	p := Provider{}
	_ = p
	body := translateForBreakdown(t)
	got := translatedBodyBreakdown(body)
	if got.BodyBytes <= 0 {
		t.Fatalf("body_bytes = %d, want positive", got.BodyBytes)
	}
	if got.InstructionsBytes <= 0 {
		t.Fatalf("instructions_bytes = %d, want positive", got.InstructionsBytes)
	}
	if got.ToolsBytes <= 0 {
		t.Fatalf("tools_bytes = %d, want positive", got.ToolsBytes)
	}
	if got.InputBytes <= 0 {
		t.Fatalf("input_bytes = %d, want positive", got.InputBytes)
	}
	if len(got.PrefixFingerprint) != 12 {
		t.Fatalf("fingerprint = %q, want 12 hex chars", got.PrefixFingerprint)
	}
	// Stability: second translation should produce identical fingerprint.
	again := translatedBodyBreakdown(translateForBreakdown(t))
	if again.PrefixFingerprint != got.PrefixFingerprint {
		t.Fatalf("fingerprint drift: %q vs %q", got.PrefixFingerprint, again.PrefixFingerprint)
	}
}

func TestStreamSalvageEmitsErrorWhenUpstreamFailsBeforeEvents(t *testing.T) {
	var sink bytes.Buffer
	err := salvageStreamFailure(&sink, "req_xyz", "gpt-5.5", io.ErrUnexpectedEOF)
	if err != nil {
		t.Fatal(err)
	}
	got := sink.String()
	for _, want := range []string{
		"event: message_start",
		`"role":"assistant"`,
		"event: error",
		"stream_error",
		"event: message_stop",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("salvage stream missing %q:\n%s", want, got)
		}
	}
}

func TestProviderMessagesDoesNotReturnErrorAfterStreamStarts(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{Access: "access", Refresh: "refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}))
	defer upstream.Close()
	p := Provider{Client: Client{BaseURL: upstream.URL, AuthStore: store}}
	out := &failingStreamOut{}
	err := p.Messages(context.Background(), provider.MessagesCall{
		Request: provider.AnthropicMessagesRequest{
			Model:  "gpt-5.5",
			Stream: boolPtr(true),
			Raw:    json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":true}`),
		},
		Route: provider.Route{IncomingModel: "gpt-5.5", UpstreamModel: "gpt-5.5"},
		Meta:  provider.CallMeta{RequestID: "req_stream_fail", SessionID: "sess-1"},
	}, out)
	if err != nil {
		t.Fatalf("Messages returned error after stream was started: %v", err)
	}
	if out.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", out.status)
	}
}

func TestStreamSalvageIsNoopWhenStreamAlreadyProducedBytes(t *testing.T) {
	var sink bytes.Buffer
	cw := &countingWriter{w: &sink}
	_, _ = io.WriteString(cw, "event: message_start\ndata: {}\n\n")
	pre := sink.Len()
	if err := salvageStreamIfNeeded(cw, "req_xyz", "gpt-5.5", io.ErrUnexpectedEOF); err != nil {
		t.Fatal(err)
	}
	if sink.Len() != pre {
		t.Fatalf("salvage should be a no-op once bytes were written; before=%d after=%d", pre, sink.Len())
	}
}

func TestCachedPctOfHandlesZero(t *testing.T) {
	if pct := cachedPctOf(translate.Usage{}); pct != 0 {
		t.Fatalf("cachedPctOf zero = %v, want 0", pct)
	}
	var usage translate.Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":1000,"input_tokens_details":{"cached_tokens":750}}`), &usage); err != nil {
		t.Fatal(err)
	}
	if pct := cachedPctOf(usage); pct != 75 {
		t.Fatalf("cachedPctOf 750/1000 = %v, want 75", pct)
	}
}

func translateForBreakdown(t *testing.T) translate.ResponsesRequest {
	t.Helper()
	body, err := translate.Translate(provider.AnthropicMessagesRequest{
		Model: "gpt-5.5",
		Raw: json.RawMessage(`{
			"model":"gpt-5.5",
			"system":"be concise",
			"tools":[{"name":"Read","description":"read","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}],
			"messages":[{"role":"user","content":"hello"}]
		}`),
	}, translate.Options{SessionID: "sess", Model: "gpt-5.5"})
	if err != nil {
		t.Fatal(err)
	}
	return body
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

type failingStreamOut struct {
	status int
	header http.Header
}

func (o *failingStreamOut) StartStream(status int, header http.Header) (io.Writer, error) {
	o.status = status
	o.header = header
	return failingWriter{}, nil
}

func (o *failingStreamOut) WriteJSON(status int, header http.Header, body any) error {
	o.status = status
	o.header = header
	return nil
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}
