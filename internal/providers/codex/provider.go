package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lobo235/cc-proxy/internal/provider"
	"github.com/lobo235/cc-proxy/internal/providers/codex/translate"
)

var unsupportedInputTokenEndpoints sync.Map

type Provider struct {
	Client                  Client
	Effort                  string
	CompactionEffort        string
	StatefulResponses       bool
	DisabledSkillToolSkills []string
	Logger                  *slog.Logger
	Verbose                 bool
	mu                      sync.Mutex
	state                   map[string]responseState
	statefulRejected        bool
}

type responseState struct {
	ResponseID      string
	CoveredMessages int
}

func (p Provider) Name() string {
	return string(provider.NameCodex)
}

func (p *Provider) Messages(ctx context.Context, call provider.MessagesCall, out provider.MessagesOut) error {
	log := p.logger()
	effort := p.Effort
	shape := summarizeRequestShape(call.Request.Raw)
	if isLikelyCompactionRequest(call.Request, shape) {
		if compactionEffort := p.compactionEffort(); compactionEffort != "inherit" {
			effort = compactionEffort
			log.Info("codex compaction effort applied",
				"request_id", call.Meta.RequestID,
				"effort", effort,
				"requested_effort", shape.OutputEffort,
				"raw_bytes", shape.RawBytes,
				"message_count", shape.MessageCount,
				"tool_count", shape.ToolCount,
				"max_tokens", shape.MaxTokens,
			)
		}
	}
	wantsStream := call.Request.WantsStream()
	statefulStore := p.statefulStoreAllowed(wantsStream)
	previousID, messageStart := p.previousResponse(call.Meta.SessionID, shape.MessageCount, wantsStream)
	body, err := translate.TranslateFromMessageIndex(call.Request, translate.Options{
		SessionID:               call.Meta.SessionID,
		ServiceTier:             call.Route.ServiceTier,
		Model:                   call.Route.UpstreamModel,
		Effort:                  effort,
		DisabledSkillToolSkills: p.DisabledSkillToolSkills,
		Store:                   statefulStore,
		PreviousResponseID:      previousID,
	}, messageStart)
	if err != nil {
		return err
	}
	if p.Verbose {
		log.Debug("codex request translated",
			"request_id", call.Meta.RequestID,
			"model", body.Model,
			"input_items", len(body.Input),
			"tools", len(body.Tools),
			"stateful_enabled", p.StatefulResponses,
			"stateful_store", statefulStore,
			"previous_response_id", body.PreviousResponseID != "",
			"message_start", messageStart,
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
	if p.Verbose {
		log.Debug("codex upstream response headers",
			"request_id", call.Meta.RequestID,
			"headers", selectedResponseHeaders(resp.Header),
		)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodySnippet := readBodySnippet(resp.Body, 4096)
		if statefulStore && isStoreMustBeFalseRejection(resp.StatusCode, bodySnippet) {
			p.disableStatefulResponses()
			log.Warn("codex stateful store rejected; retrying stateless",
				"request_id", call.Meta.RequestID,
				"status", resp.StatusCode,
				"body", bodySnippet,
			)
			statefulStore = false
			messageStart = 0
			body, err = translate.TranslateFromMessageIndex(call.Request, translate.Options{
				SessionID:               call.Meta.SessionID,
				ServiceTier:             call.Route.ServiceTier,
				Model:                   call.Route.UpstreamModel,
				Effort:                  effort,
				DisabledSkillToolSkills: p.DisabledSkillToolSkills,
				Store:                   false,
			}, 0)
			if err != nil {
				return err
			}
			if p.Verbose {
				log.Debug("codex request translated",
					"request_id", call.Meta.RequestID,
					"model", body.Model,
					"input_items", len(body.Input),
					"tools", len(body.Tools),
					"stateful_enabled", p.StatefulResponses,
					"stateful_store", false,
					"previous_response_id", false,
					"message_start", 0,
					"service_tier", body.ServiceTier,
					"reasoning_effort", reasoningEffort(body.Reasoning),
					"include_reasoning", containsString(body.Include, "reasoning.encrypted_content"),
				)
			}
			start = time.Now()
			resp, err = p.Client.PostResponses(ctx, body, call.Meta)
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
			if p.Verbose {
				log.Debug("codex upstream response headers",
					"request_id", call.Meta.RequestID,
					"headers", selectedResponseHeaders(resp.Header),
				)
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				bodySnippet = readBodySnippet(resp.Body, 4096)
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Warn("codex upstream rejected", "request_id", call.Meta.RequestID, "status", resp.StatusCode, "body", bodySnippet)
			return provider.UpstreamError{Provider: "codex", StatusCode: resp.StatusCode, Body: bodySnippet}
		}
	}
	if !call.Request.WantsStream() {
		var usage translate.Usage
		hasUsage := false
		message, err := translate.TranslateResponse(resp.Body, translate.StreamOptions{
			MessageID: "msg_" + call.Meta.RequestID,
			Model:     call.Route.UpstreamModel,
			OnResponseID: func(id string) {
				if statefulStore {
					p.rememberResponse(call.Meta.SessionID, id, shape.MessageCount+1)
				}
			},
			OnUsage: func(u translate.Usage) {
				usage = u
				hasUsage = true
			},
		})
		if hasUsage {
			log.Info("codex usage",
				"request_id", call.Meta.RequestID,
				"input_tokens", usage.InputTokens,
				"cached_input_tokens", usage.InputTokensDetails.CachedTokens,
				"output_tokens", usage.OutputTokens,
				"reasoning_tokens", usage.OutputTokensDetails.ReasoningTokens,
				"total_tokens", usage.TotalTokens,
			)
		}
		if err != nil {
			return err
		}
		return out.WriteJSON(http.StatusOK, http.Header{}, message)
	}
	header := http.Header{}
	header.Set("content-type", "text/event-stream")
	writer, err := out.StartStream(http.StatusOK, header)
	if err != nil {
		return err
	}
	var usage translate.Usage
	hasUsage := false
	err = translate.TranslateStream(resp.Body, writer, translate.StreamOptions{
		MessageID: "msg_" + call.Meta.RequestID,
		Model:     call.Route.UpstreamModel,
		OnResponseID: func(id string) {
			if statefulStore {
				p.rememberResponse(call.Meta.SessionID, id, shape.MessageCount+1)
			}
		},
		OnUsage: func(u translate.Usage) {
			usage = u
			hasUsage = true
		},
	})
	if hasUsage {
		log.Info("codex usage",
			"request_id", call.Meta.RequestID,
			"input_tokens", usage.InputTokens,
			"cached_input_tokens", usage.InputTokensDetails.CachedTokens,
			"output_tokens", usage.OutputTokens,
			"reasoning_tokens", usage.OutputTokensDetails.ReasoningTokens,
			"total_tokens", usage.TotalTokens,
		)
	}
	return err
}

func (p *Provider) previousResponse(sessionID string, messageCount int, wantsStream bool) (string, int) {
	if !p.StatefulResponses || sessionID == "" || !wantsStream {
		return "", 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.statefulRejected {
		return "", 0
	}
	if p.state == nil {
		return "", 0
	}
	state := p.state[sessionID]
	if state.ResponseID == "" || state.CoveredMessages <= 0 || messageCount <= state.CoveredMessages {
		return "", 0
	}
	return state.ResponseID, state.CoveredMessages
}

func (p *Provider) rememberResponse(sessionID, responseID string, coveredMessages int) {
	if !p.StatefulResponses || sessionID == "" || responseID == "" || coveredMessages <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.statefulRejected {
		return
	}
	if p.state == nil {
		p.state = map[string]responseState{}
	}
	p.state[sessionID] = responseState{ResponseID: responseID, CoveredMessages: coveredMessages}
}

func (p *Provider) statefulStoreAllowed(wantsStream bool) bool {
	if !p.StatefulResponses || !wantsStream {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.statefulRejected
}

func (p *Provider) disableStatefulResponses() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statefulRejected = true
	p.state = nil
}

func isStoreMustBeFalseRejection(status int, body string) bool {
	return status == http.StatusBadRequest && strings.Contains(strings.ToLower(body), "store must be set to false")
}

func (p Provider) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

type requestShape struct {
	RawBytes     int
	MessageCount int
	ToolCount    int
	OutputEffort string
	Stream       *bool
	MaxTokens    int
}

func summarizeRequestShape(raw json.RawMessage) requestShape {
	var decoded struct {
		Messages     []json.RawMessage `json:"messages"`
		Tools        []json.RawMessage `json:"tools"`
		OutputConfig struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
		Stream    *bool `json:"stream"`
		MaxTokens *int  `json:"max_tokens"`
	}
	_ = json.Unmarshal(raw, &decoded)
	shape := requestShape{
		RawBytes:     len(raw),
		MessageCount: len(decoded.Messages),
		ToolCount:    len(decoded.Tools),
		OutputEffort: decoded.OutputConfig.Effort,
		Stream:       decoded.Stream,
	}
	if decoded.MaxTokens != nil {
		shape.MaxTokens = *decoded.MaxTokens
	}
	return shape
}

func isLikelyCompactionRequest(req provider.AnthropicMessagesRequest, shape requestShape) bool {
	if req.WantsStream() {
		return false
	}
	return shape.RawBytes >= 200_000 &&
		shape.MessageCount >= 10 &&
		shape.ToolCount <= 3 &&
		shape.MaxTokens >= 10_000
}

func (p Provider) compactionEffort() string {
	if p.CompactionEffort != "" {
		return p.CompactionEffort
	}
	return "medium"
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

func selectedResponseHeaders(header http.Header) map[string]string {
	out := map[string]string{}
	for _, name := range []string{
		"x-request-id",
		"openai-processing-ms",
		"retry-after",
		"x-ratelimit-limit-requests",
		"x-ratelimit-remaining-requests",
		"x-ratelimit-reset-requests",
		"x-ratelimit-limit-tokens",
		"x-ratelimit-remaining-tokens",
		"x-ratelimit-reset-tokens",
	} {
		if value := header.Get(name); value != "" {
			out[name] = value
		}
	}
	return out
}

func (p Provider) CountTokens(ctx context.Context, call provider.CountTokensCall) (provider.CountTokensResponse, error) {
	log := p.logger()
	estimate, estimateErr := translate.CountTokens(call.Request)
	body, translateErr := translate.Translate(call.Request, translate.Options{
		SessionID:   call.Meta.SessionID,
		ServiceTier: call.Route.ServiceTier,
		Model:       call.Route.UpstreamModel,
		Effort:      p.Effort,
	})
	if translateErr == nil {
		endpoint := p.Client.inputTokensEndpoint()
		if _, unsupported := unsupportedInputTokenEndpoints.Load(endpoint); !unsupported {
			start := time.Now()
			count, err := p.Client.CountInputTokens(ctx, body, call.Meta)
			if err == nil {
				log.Info("codex count_tokens",
					"request_id", call.Meta.RequestID,
					"source", "upstream",
					"input_tokens", count,
					"duration_ms", time.Since(start).Milliseconds(),
				)
				return provider.CountTokensResponse{InputTokens: count}, nil
			}
			var statusErr InputTokensStatusError
			if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
				unsupportedInputTokenEndpoints.Store(endpoint, true)
			}
			if p.Verbose {
				log.Debug("codex count_tokens upstream unavailable",
					"request_id", call.Meta.RequestID,
					"error", err.Error(),
					"duration_ms", time.Since(start).Milliseconds(),
				)
			}
		} else if p.Verbose {
			log.Debug("codex count_tokens upstream skipped",
				"request_id", call.Meta.RequestID,
				"reason", "input token endpoint previously returned 404",
			)
		}
	}
	if estimateErr != nil {
		return provider.CountTokensResponse{}, estimateErr
	}
	if p.Verbose && translateErr != nil {
		log.Debug("codex count_tokens translate unavailable",
			"request_id", call.Meta.RequestID,
			"error", translateErr.Error(),
		)
	}
	log.Info("codex count_tokens",
		"request_id", call.Meta.RequestID,
		"source", "estimate",
		"input_tokens", estimate,
	)
	return provider.CountTokensResponse{InputTokens: estimate}, nil
}
