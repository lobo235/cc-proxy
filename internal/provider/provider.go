package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/lobo235/cc-proxy/internal/modelregistry"
)

type Name string

const (
	NameCodex Name = "codex"
	NameKimi  Name = "kimi"
)

type AnthropicMessagesRequest struct {
	Model      string          `json:"model"`
	Messages   json.RawMessage `json:"messages,omitempty"`
	System     json.RawMessage `json:"system,omitempty"`
	Tools      json.RawMessage `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	Stream     *bool           `json:"stream,omitempty"`
	MaxTokens  *int            `json:"max_tokens,omitempty"`
	Raw        json.RawMessage `json:"-"`
}

func (r AnthropicMessagesRequest) WantsStream() bool {
	return r.Stream != nil && *r.Stream
}

type Route struct {
	Provider      Name
	IncomingModel string
	UpstreamModel string
	ServiceTier   string
}

type CallMeta struct {
	RequestID  string
	SessionID  string
	SessionSeq int
}

type ClientMeta struct {
	Headers http.Header
	Remote  string
}

type MessagesCall struct {
	Request AnthropicMessagesRequest
	Route   Route
	Meta    CallMeta
	Client  ClientMeta
}

type CountTokensCall struct {
	Request AnthropicMessagesRequest
	Route   Route
	Meta    CallMeta
	Client  ClientMeta
}

type CountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

type MessagesOut interface {
	StartStream(status int, header http.Header) (io.Writer, error)
	WriteJSON(status int, header http.Header, body any) error
}

type Provider interface {
	Name() string
	Messages(ctx context.Context, call MessagesCall, out MessagesOut) error
	CountTokens(ctx context.Context, call CountTokensCall) (CountTokensResponse, error)
}

type NotImplemented struct {
	ProviderName string
}

func (p NotImplemented) Name() string {
	return p.ProviderName
}

func (p NotImplemented) Messages(context.Context, MessagesCall, MessagesOut) error {
	return ErrNotImplemented{Provider: p.ProviderName, Operation: "messages"}
}

func (p NotImplemented) CountTokens(context.Context, CountTokensCall) (CountTokensResponse, error) {
	return CountTokensResponse{}, ErrNotImplemented{Provider: p.ProviderName, Operation: "count_tokens"}
}

func NameFromRegistry(provider modelregistry.Provider) Name {
	switch provider {
	case modelregistry.ProviderKimi:
		return NameKimi
	default:
		return NameCodex
	}
}

type ErrNotImplemented struct {
	Provider  string
	Operation string
}

func (e ErrNotImplemented) Error() string {
	return e.Provider + " " + e.Operation + " not implemented yet"
}
