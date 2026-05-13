package server

import (
	"context"
	"encoding/json"
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
