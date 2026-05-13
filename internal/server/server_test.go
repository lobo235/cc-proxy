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

func testProviders() Providers {
	return Providers{
		Codex: provider.NotImplemented{ProviderName: "codex"},
		Kimi:  provider.NotImplemented{ProviderName: "kimi"},
	}
}

type fakeProvider struct {
	name       string
	countCalls int
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) HandleMessages(context.Context, provider.Request, provider.Context) (*http.Response, error) {
	return nil, provider.ErrNotImplemented{Provider: p.name, Operation: "messages"}
}

func (p *fakeProvider) HandleCountTokens(context.Context, provider.Request, provider.Context) (provider.CountTokensResponse, error) {
	p.countCalls++
	return provider.CountTokensResponse{InputTokens: 42}, nil
}

func (p *fakeProvider) AuthStatus(context.Context) error { return nil }

func (p *fakeProvider) AuthLogout(context.Context) error { return nil }
