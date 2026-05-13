package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lobo235/cc-proxy/internal/authstore"
	"github.com/lobo235/cc-proxy/internal/provider"
	"github.com/lobo235/cc-proxy/internal/providers/codex/translate"
)

const (
	DefaultResponsesEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	DefaultOriginator        = "claude-code-proxy"
)

type Client struct {
	HTTPClient     *http.Client
	BaseURL        string
	InputTokensURL string
	AuthStore      authstore.Store
	Originator     string
	UserAgent      string
	Version        string
}

type InputTokensResponse struct {
	Object      string `json:"object"`
	InputTokens int    `json:"input_tokens"`
}

type InputTokensStatusError struct {
	StatusCode int
}

func (e InputTokensStatusError) Error() string {
	return fmt.Sprintf("input token endpoint returned %d %s", e.StatusCode, http.StatusText(e.StatusCode))
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

func (c Client) CountInputTokens(ctx context.Context, body translate.ResponsesRequest, meta provider.CallMeta) (int, error) {
	auth, _, ok, err := c.AuthStore.Load(authstore.ProviderCodex)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("not authenticated")
	}
	data, err := json.Marshal(translate.InputTokensRequestFromResponses(body))
	if err != nil {
		return 0, fmt.Errorf("encoding codex input token request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.inputTokensEndpoint(), bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
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
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, InputTokensStatusError{StatusCode: resp.StatusCode}
	}
	var decoded InputTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, fmt.Errorf("decoding input token response: %w", err)
	}
	if decoded.InputTokens < 0 {
		return 0, fmt.Errorf("input token endpoint returned negative count")
	}
	return decoded.InputTokens, nil
}

func (c Client) endpoint() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultResponsesEndpoint
}

func (c Client) inputTokensEndpoint() string {
	if c.InputTokensURL != "" {
		return c.InputTokensURL
	}
	return strings.TrimRight(c.endpoint(), "/") + "/input_tokens"
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
