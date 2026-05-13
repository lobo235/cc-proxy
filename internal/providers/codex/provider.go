package codex

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lobo235/cc-proxy/internal/provider"
	"github.com/lobo235/cc-proxy/internal/providers/codex/translate"
)

type Provider struct {
	Client  Client
	Effort  string
	Logger  *slog.Logger
	Verbose bool
}

func (p Provider) Name() string {
	return string(provider.NameCodex)
}

func (p Provider) Messages(ctx context.Context, call provider.MessagesCall, out provider.MessagesOut) error {
	log := p.logger()
	body, err := translate.Translate(call.Request, translate.Options{
		SessionID:   call.Meta.SessionID,
		ServiceTier: call.Route.ServiceTier,
		Model:       call.Route.UpstreamModel,
		Effort:      p.Effort,
	})
	if err != nil {
		return err
	}
	if p.Verbose {
		log.Debug("codex request translated",
			"request_id", call.Meta.RequestID,
			"model", body.Model,
			"input_items", len(body.Input),
			"tools", len(body.Tools),
			"service_tier", body.ServiceTier,
			"reasoning_effort", reasoningEffort(body.Reasoning),
			"include_reasoning", containsString(body.Include, "reasoning.encrypted_content"),
		)
	}
	start := time.Now()
	resp, err := p.Client.PostResponses(ctx, body, call.Meta)
	if err != nil {
		log.Error("codex upstream request failed", "request_id", call.Meta.RequestID, "error", err.Error())
		return err
	}
	defer resp.Body.Close()
	log.Info("codex upstream response",
		"request_id", call.Meta.RequestID,
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
		"content_type", resp.Header.Get("content-type"),
	)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodySnippet := readBodySnippet(resp.Body, 4096)
		log.Warn("codex upstream rejected", "request_id", call.Meta.RequestID, "status", resp.StatusCode, "body", bodySnippet)
		return provider.UpstreamError{Provider: "codex", StatusCode: resp.StatusCode, Body: bodySnippet}
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

func (p Provider) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

func reasoningEffort(r *translate.Reasoning) string {
	if r == nil {
		return ""
	}
	return r.Effort
}

func readBodySnippet(r io.Reader, limit int64) string {
	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return ""
	}
	return redactSensitive(strings.TrimSpace(string(data)))
}

var sensitiveLogPattern = regexp.MustCompile(`(?i)("?(authorization|access_token|refresh_token|id_token|api_key|code|chatgpt-account-id)"?\s*[:=]\s*"?)([^",}\s]+)`)

func redactSensitive(value string) string {
	return sensitiveLogPattern.ReplaceAllString(value, `${1}[REDACTED]`)
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func (p Provider) CountTokens(_ context.Context, call provider.CountTokensCall) (provider.CountTokensResponse, error) {
	count, err := translate.CountTokens(call.Request)
	if err != nil {
		return provider.CountTokensResponse{}, err
	}
	return provider.CountTokensResponse{InputTokens: count}, nil
}
