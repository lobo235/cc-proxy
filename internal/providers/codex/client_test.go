package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lobo235/cc-proxy/internal/authstore"
	"github.com/lobo235/cc-proxy/internal/provider"
	"github.com/lobo235/cc-proxy/internal/providers/codex/translate"
)

func TestClientPostResponsesSendsAuthSessionAndBody(t *testing.T) {
	home := t.TempDir()
	store := authstore.New(map[string]string{}, home)
	if err := store.Save(authstore.ProviderCodex, authstore.Record{
		Access:    "access-token",
		Refresh:   "refresh-token",
		Expires:   1_765_000_000_000,
		AccountID: "acct_123",
	}); err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Authorization string
		AccountID     string
		SessionID     string
		RequestID     string
		WindowID      string
		Originator    string
		UserAgent     string
		Body          translate.ResponsesRequest
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Authorization = r.Header.Get("authorization")
		captured.AccountID = r.Header.Get("ChatGPT-Account-Id")
		captured.SessionID = r.Header.Get("session_id")
		captured.RequestID = r.Header.Get("x-client-request-id")
		captured.WindowID = r.Header.Get("x-codex-window-id")
		captured.Originator = r.Header.Get("originator")
		captured.UserAgent = r.Header.Get("User-Agent")
		if err := json.NewDecoder(r.Body).Decode(&captured.Body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: ok\n\n"))
	}))
	defer upstream.Close()

	client := Client{
		BaseURL:   upstream.URL,
		AuthStore: store,
		Version:   "test",
	}
	resp, err := client.PostResponses(context.Background(), translate.ResponsesRequest{
		Model:             "gpt-5.4",
		Input:             []translate.InputItem{{Type: "message", Role: "user", Content: []translate.ContentPart{{Type: "input_text", Text: "hello"}}}},
		Store:             false,
		Stream:            true,
		ParallelToolCalls: true,
		Text:              translate.TextConfig{Verbosity: "low"},
	}, provider.CallMeta{SessionID: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if captured.Authorization != "Bearer access-token" {
		t.Fatalf("authorization = %q", captured.Authorization)
	}
	if captured.AccountID != "acct_123" {
		t.Fatalf("account id = %q", captured.AccountID)
	}
	if captured.SessionID != "sess-1" || captured.RequestID != "sess-1" || captured.WindowID != "sess-1:0" {
		t.Fatalf("session headers = %q %q %q", captured.SessionID, captured.RequestID, captured.WindowID)
	}
	if captured.Originator != "claude-code-proxy" {
		t.Fatalf("originator = %q", captured.Originator)
	}
	if captured.UserAgent != "claude-code-proxy/test" {
		t.Fatalf("user agent = %q", captured.UserAgent)
	}
	if captured.Body.Model != "gpt-5.4" || len(captured.Body.Input) != 1 {
		t.Fatalf("body = %+v", captured.Body)
	}
}
