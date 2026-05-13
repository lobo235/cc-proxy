package provider

import (
	"context"
	"encoding/json"
	"net/http"
)

type Request struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages,omitempty"`
	Stream   *bool           `json:"stream,omitempty"`
}

type CountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

type Context struct {
	RequestID  string
	SessionID  string
	SessionSeq int
}

type Provider interface {
	Name() string
	HandleMessages(ctx context.Context, req Request, meta Context) (*http.Response, error)
	HandleCountTokens(ctx context.Context, req Request, meta Context) (CountTokensResponse, error)
	AuthStatus(ctx context.Context) error
	AuthLogout(ctx context.Context) error
}

type NotImplemented struct {
	ProviderName string
}

func (p NotImplemented) Name() string {
	return p.ProviderName
}

func (p NotImplemented) HandleMessages(context.Context, Request, Context) (*http.Response, error) {
	return nil, ErrNotImplemented{Provider: p.ProviderName, Operation: "messages"}
}

func (p NotImplemented) HandleCountTokens(context.Context, Request, Context) (CountTokensResponse, error) {
	return CountTokensResponse{}, ErrNotImplemented{Provider: p.ProviderName, Operation: "count_tokens"}
}

func (p NotImplemented) AuthStatus(context.Context) error {
	return ErrNotImplemented{Provider: p.ProviderName, Operation: "auth status"}
}

func (p NotImplemented) AuthLogout(context.Context) error {
	return ErrNotImplemented{Provider: p.ProviderName, Operation: "auth logout"}
}

type ErrNotImplemented struct {
	Provider  string
	Operation string
}

func (e ErrNotImplemented) Error() string {
	return e.Provider + " " + e.Operation + " not implemented yet"
}
