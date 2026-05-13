package codex

import (
	"context"
	"fmt"
	"net/http"

	"github.com/lobo235/cc-proxy/internal/provider"
	"github.com/lobo235/cc-proxy/internal/providers/codex/translate"
)

type Provider struct {
	Client Client
	Effort string
}

func (p Provider) Name() string {
	return string(provider.NameCodex)
}

func (p Provider) Messages(ctx context.Context, call provider.MessagesCall, out provider.MessagesOut) error {
	body, err := translate.Translate(call.Request, translate.Options{
		SessionID:   call.Meta.SessionID,
		ServiceTier: call.Route.ServiceTier,
		Model:       call.Route.UpstreamModel,
		Effort:      p.Effort,
	})
	if err != nil {
		return err
	}
	resp, err := p.Client.PostResponses(ctx, body, call.Meta)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("codex upstream returned %d", resp.StatusCode)
	}
	header := http.Header{}
	header.Set("content-type", "text/event-stream")
	writer, err := out.StartStream(http.StatusOK, header)
	if err != nil {
		return err
	}
	return translate.TranslateStream(resp.Body, writer, translate.StreamOptions{
		MessageID: "msg_" + call.Meta.RequestID,
		Model:     call.Route.IncomingModel,
	})
}

func (p Provider) CountTokens(_ context.Context, call provider.CountTokensCall) (provider.CountTokensResponse, error) {
	count, err := translate.CountTokens(call.Request)
	if err != nil {
		return provider.CountTokensResponse{}, err
	}
	return provider.CountTokensResponse{InputTokens: count}, nil
}
