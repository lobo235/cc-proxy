package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lobo235/cc-proxy/internal/authstore"
)

const (
	ClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultIssuer = "https://auth.openai.com"
)

type DeviceLoginOptions struct {
	Issuer       string
	Store        authstore.Store
	Stdout       io.Writer
	HTTPClient   *http.Client
	PollInterval time.Duration
}

type tokenResponse struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
}

func DeviceLogin(ctx context.Context, opts DeviceLoginOptions) (authstore.Record, error) {
	issuer := opts.Issuer
	if issuer == "" {
		issuer = DefaultIssuer
	}
	out := opts.Stdout
	if out == nil {
		out = io.Discard
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	init, err := postJSON[struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     string `json:"interval"`
	}](ctx, client, issuer+"/api/accounts/deviceauth/usercode", map[string]string{"client_id": ClientID})
	if err != nil {
		return authstore.Record{}, err
	}
	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = parsePollInterval(init.Interval) + 3*time.Second
	}
	fmt.Fprintf(out, "\nVisit: %s/codex/device\nEnter code: %s\n\n", issuer, init.UserCode)
	for {
		tokens, done, err := pollDevice(ctx, client, issuer, init.DeviceAuthID, init.UserCode)
		if err != nil {
			return authstore.Record{}, err
		}
		if done {
			rec := authstore.Record{
				Access:    tokens.AccessToken,
				Refresh:   tokens.RefreshToken,
				Expires:   time.Now().Add(time.Duration(expiresIn(tokens.ExpiresIn)) * time.Second).UnixMilli(),
				AccountID: extractAccountIDFromTokens(tokens),
			}
			if err := opts.Store.Save(authstore.ProviderCodex, rec); err != nil {
				return authstore.Record{}, err
			}
			fmt.Fprintf(out, "Auth saved in %s\n", opts.Store.AuthPath(authstore.ProviderCodex))
			if rec.AccountID != "" {
				fmt.Fprintf(out, "Account: %s\n", rec.AccountID)
			}
			return rec, nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return authstore.Record{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func pollDevice(ctx context.Context, client *http.Client, issuer, deviceAuthID, userCode string) (tokenResponse, bool, error) {
	reqBody := map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	}
	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/api/accounts/deviceauth/token", bytes.NewReader(data))
	if err != nil {
		return tokenResponse{}, false, err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return tokenResponse{}, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, false, fmt.Errorf("device poll failed: %d", resp.StatusCode)
	}
	var body struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return tokenResponse{}, false, err
	}
	tokens, err := exchangeToken(ctx, client, issuer, body.AuthorizationCode, body.CodeVerifier)
	if err != nil {
		return tokenResponse{}, false, err
	}
	return tokens, true, nil
}

func exchangeToken(ctx context.Context, client *http.Client, issuer, code, verifier string) (tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", issuer+"/deviceauth/callback")
	form.Set("client_id", ClientID)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("token exchange failed: %d", resp.StatusCode)
	}
	var tokens tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return tokenResponse{}, err
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return tokenResponse{}, fmt.Errorf("invalid token response")
	}
	return tokens, nil
}

func postJSON[T any](ctx context.Context, client *http.Client, endpoint string, body any) (T, error) {
	var zero T
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return zero, err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("device init failed: %d", resp.StatusCode)
	}
	var decoded T
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return zero, err
	}
	return decoded, nil
}

func parsePollInterval(v string) time.Duration {
	n, err := time.ParseDuration(v + "s")
	if err != nil || n < time.Second {
		return 5 * time.Second
	}
	return n
}

func expiresIn(seconds int64) int64 {
	if seconds <= 0 {
		return 3600
	}
	return seconds
}

func extractAccountIDFromTokens(tokens tokenResponse) string {
	if id := ExtractAccountID(tokens.IDToken); id != "" {
		return id
	}
	return ExtractAccountID(tokens.AccessToken)
}

func ExtractAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		Organizations    []struct {
			ID string `json:"id"`
		} `json:"organizations"`
		OpenAIAuth struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
		OpenAIAccountID string `json:"https://api.openai.com/auth.chatgpt_account_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	switch {
	case claims.ChatGPTAccountID != "":
		return claims.ChatGPTAccountID
	case claims.OpenAIAuth.ChatGPTAccountID != "":
		return claims.OpenAIAuth.ChatGPTAccountID
	case claims.OpenAIAccountID != "":
		return claims.OpenAIAccountID
	case len(claims.Organizations) > 0:
		return claims.Organizations[0].ID
	default:
		return ""
	}
}
