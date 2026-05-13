package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lobo235/cc-proxy/internal/authstore"
	"github.com/lobo235/cc-proxy/internal/provider"
	"github.com/lobo235/cc-proxy/internal/providers/codex/translate"
)

const (
	DefaultResponsesEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	DefaultOriginator        = "claude-code-proxy"
)

type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	AuthStore  authstore.Store
	Originator string
	UserAgent  string
	Version    string
}

func (c Client) PostResponses(ctx context.Context, body translate.ResponsesRequest, meta provider.CallMeta) (*http.Response, error) {
	auth, _, ok, err := c.AuthStore.Load(authstore.ProviderCodex)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("not authenticated. Run: cc-proxy codex auth login")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding codex request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("authorization", "Bearer "+auth.Access)
	req.Header.Set("originator", c.originator())
	req.Header.Set("openai-beta", "responses=experimental")
	req.Header.Set("user-agent", c.userAgent())
	if auth.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.AccountID)
	}
	if meta.SessionID != "" {
		req.Header.Set("session_id", meta.SessionID)
		req.Header.Set("x-client-request-id", meta.SessionID)
		req.Header.Set("x-codex-window-id", meta.SessionID+":0")
	}
	return c.httpClient().Do(req)
}

func (c Client) endpoint() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultResponsesEndpoint
}

func (c Client) originator() string {
	if c.Originator != "" {
		return c.Originator
	}
	return DefaultOriginator
}

func (c Client) userAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	version := c.Version
	if version == "" {
		version = "dev"
	}
	return "claude-code-proxy/" + version
}

func (c Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
