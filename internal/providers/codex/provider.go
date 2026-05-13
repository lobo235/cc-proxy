package codex

import (
	"context"

	"github.com/lobo235/cc-proxy/internal/provider"
	"github.com/lobo235/cc-proxy/internal/providers/codex/translate"
)

type Provider struct{}

func (p Provider) Name() string {
	return string(provider.NameCodex)
}

func (p Provider) Messages(context.Context, provider.MessagesCall, provider.MessagesOut) error {
	return provider.ErrNotImplemented{Provider: p.Name(), Operation: "messages"}
}

func (p Provider) CountTokens(_ context.Context, call provider.CountTokensCall) (provider.CountTokensResponse, error) {
	count, err := translate.CountTokens(call.Request)
	if err != nil {
		return provider.CountTokensResponse{}, err
	}
	return provider.CountTokensResponse{InputTokens: count}, nil
}
