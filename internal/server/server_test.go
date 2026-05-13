package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lobo235/cc-proxy/internal/config"
	"github.com/lobo235/cc-proxy/internal/provider"
)

func TestHealthz(t *testing.T) {
	s := New(config.Config{Port: 18765, AliasProvider: config.AliasProviderCodex}, testProviders(), nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body["ok"] {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestStatusReportsVersionRoutesAndProviderCapabilities(t *testing.T) {
	s := New(config.Config{Port: 18765, AliasProvider: config.AliasProviderCodex}, testProviders(), nil)
	s.SetVersion("v-test")
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK        bool `json:"ok"`
		Version   string
		Routes    []string
		Providers map[string]struct {
			Messages    string   `json:"messages"`
			CountTokens string   `json:"count_tokens"`
			Auth        string   `json:"auth"`
			Models      []string `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Version != "v-test" {
		t.Fatalf("body = %+v", body)
	}
	if !containsString(body.Routes, "/v1/messages") || !containsString(body.Routes, "/v1/messages/count_tokens") {
		t.Fatalf("routes = %+v", body.Routes)
	}
	codex := body.Providers["codex"]
	if codex.Messages != "partial" || codex.CountTokens != "ready" || codex.Auth != "status_logout" {
		t.Fatalf("codex = %+v", codex)
	}
	if !containsString(codex.Models, "gpt-5.4") {
		t.Fatalf("codex models = %+v", codex.Models)
	}
	kimi := body.Providers["kimi"]
	if kimi.Messages != "not_implemented" || kimi.CountTokens != "not_implemented" || kimi.Auth != "status_logout" {
		t.Fatalf("kimi = %+v", kimi)
	}
}

func TestUnknownModelReturns400(t *testing.T) {
	s := New(config.Config{Port: 18765, AliasProvider: config.AliasProviderCodex}, testProviders(), nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"wat","messages":[]}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Unknown model") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func TestCountTokensRoutesToProvider(t *testing.T) {
	fp := &fakeProvider{name: "codex"}
	s := New(config.Config{Port: 18765, AliasProvider: config.AliasProviderCodex}, Providers{
		Codex: fp,
		Kimi:  &fakeProvider{name: "kimi"},
	}, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"gpt-5.4","messages":[]}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if fp.countCalls != 1 {
		t.Fatalf("count calls = %d, want 1", fp.countCalls)
	}
}

func TestMessagesRoutesCallToProviderSink(t *testing.T) {
	fp := &fakeProvider{name: "codex"}
	s := New(config.Config{Port: 18765, AliasProvider: config.AliasProviderCodex}, Providers{
		Codex: fp,
		Kimi:  &fakeProvider{name: "kimi"},
	}, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.4[1m]","messages":[],"stream":false}`))
	req.Header.Set("x-claude-code-session-id", "sess-1")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if fp.messageCalls != 1 {
		t.Fatalf("message calls = %d, want 1", fp.messageCalls)
	}
	if fp.lastMessage.Route.IncomingModel != "gpt-5.4" {
		t.Fatalf("incoming model = %q, want gpt-5.4", fp.lastMessage.Route.IncomingModel)
	}
	if fp.lastMessage.Route.UpstreamModel != "gpt-5.4" {
		t.Fatalf("upstream model = %q, want gpt-5.4", fp.lastMessage.Route.UpstreamModel)
	}
	if fp.lastMessage.Meta.SessionID != "sess-1" || fp.lastMessage.Meta.SessionSeq != 1 {
		t.Fatalf("meta = %+v, want session sess-1 seq 1", fp.lastMessage.Meta)
	}
	if !strings.Contains(string(fp.lastMessage.Request.Raw), `"stream":false`) {
		t.Fatalf("raw request = %s", string(fp.lastMessage.Request.Raw))
	}
	if got := rec.Header().Get("x-provider"); got != "codex" {
		t.Fatalf("x-provider = %q, want codex", got)
	}
	if !strings.Contains(rec.Body.String(), `"model":"gpt-5.4"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestAnthropicAliasInheritsExplicitCodexModelWithinSession(t *testing.T) {
	fp := &fakeProvider{name: "codex"}
	s := New(config.Config{Port: 18765, AliasProvider: config.AliasProviderCodex}, Providers{
		Codex: fp,
		Kimi:  &fakeProvider{name: "kimi"},
	}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.5","messages":[]}`))
	req.Header.Set("x-claude-code-session-id", "sess-1")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-6","messages":[]}`))
	req.Header.Set("x-claude-code-session-id", "sess-1")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second status = %d body=%s", rec.Code, rec.Body.String())
	}
	if fp.lastMessage.Route.IncomingModel != "claude-sonnet-4-6" {
		t.Fatalf("incoming model = %q, want claude-sonnet-4-6", fp.lastMessage.Route.IncomingModel)
	}
	if fp.lastMessage.Route.UpstreamModel != "gpt-5.5" {
		t.Fatalf("upstream model = %q, want session gpt-5.5", fp.lastMessage.Route.UpstreamModel)
	}
	if fp.lastMessage.Meta.SessionSeq != 2 {
		t.Fatalf("session seq = %d, want 2", fp.lastMessage.Meta.SessionSeq)
	}
}

func TestAnthropicAliasUsesConfiguredCodexModelWithoutSessionPreference(t *testing.T) {
	fp := &fakeProvider{name: "codex"}
	s := New(config.Config{
		Port:          18765,
		AliasProvider: config.AliasProviderCodex,
		Codex:         config.CodexConfig{Model: "gpt-5.5"},
	}, Providers{
		Codex: fp,
		Kimi:  &fakeProvider{name: "kimi"},
	}, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-6","messages":[]}`))
	req.Header.Set("x-claude-code-session-id", "sess-1")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if fp.lastMessage.Route.UpstreamModel != "gpt-5.5" {
		t.Fatalf("upstream model = %q, want configured gpt-5.5", fp.lastMessage.Route.UpstreamModel)
	}
}

func TestMessagesLogsRoutingAndHTTPRequest(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	fp := &fakeProvider{name: "codex"}
	s := New(config.Config{Port: 18765, AliasProvider: config.AliasProviderCodex}, Providers{
		Codex: fp,
		Kimi:  &fakeProvider{name: "kimi"},
	}, logger)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.4","messages":[]}`))
	req.Header.Set("x-claude-code-session-id", "sess-1")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	got := logs.String()
	for _, want := range []string{
		`"msg":"message routed"`,
		`"request_id":"`,
		`"operation":"messages"`,
		`"provider":"codex"`,
		`"incoming_model":"gpt-5.4"`,
		`"upstream_model":"gpt-5.4"`,
		`"session_seq":1`,
		`"msg":"http request"`,
		`"request_id":"`,
		`"path":"/v1/messages"`,
		`"status":200`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sess-1") {
		t.Fatalf("logs should not include raw session id:\n%s", got)
	}
}

func TestVerboseMessagesLogsRequestShape(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fp := &fakeProvider{name: "codex"}
	s := New(config.Config{
		Port:          18765,
		AliasProvider: config.AliasProviderCodex,
		Log:           config.LogConfig{Verbose: true},
	}, Providers{
		Codex: fp,
		Kimi:  &fakeProvider{name: "kimi"},
	}, logger)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"gpt-5.4",
		"system":"be brief",
		"messages":[{"role":"user","content":"one"},{"role":"assistant","content":"two"}],
		"tools":[{"name":"lookup","input_schema":{"type":"object"}}],
		"output_config":{"effort":"high"},
		"stream":false
	}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	got := logs.String()
	for _, want := range []string{
		`"msg":"message request summary"`,
		`"message_count":2`,
		`"tool_count":1`,
		`"system_kind":"string"`,
		`"output_effort":"high"`,
		`"stream":false`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %s:\n%s", want, got)
		}
	}
}

func testProviders() Providers {
	return Providers{
		Codex: provider.NotImplemented{ProviderName: "codex"},
		Kimi:  provider.NotImplemented{ProviderName: "kimi"},
	}
}

type fakeProvider struct {
	name         string
	countCalls   int
	messageCalls int
	lastMessage  provider.MessagesCall
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Messages(_ context.Context, call provider.MessagesCall, out provider.MessagesOut) error {
	p.messageCalls++
	p.lastMessage = call
	return out.WriteJSON(http.StatusOK, http.Header{"x-provider": []string{p.name}}, map[string]any{
		"type":  "message",
		"model": call.Route.UpstreamModel,
	})
}

func (p *fakeProvider) CountTokens(context.Context, provider.CountTokensCall) (provider.CountTokensResponse, error) {
	p.countCalls++
	return provider.CountTokensResponse{InputTokens: 42}, nil
}
