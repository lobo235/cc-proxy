package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
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
	DisabledSkillToolSkills []string
	CacheKeyStrategy        translate.CacheKeyStrategy
	Logger                  *slog.Logger
	Verbose                 bool
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
	body, err := translate.Translate(call.Request, translate.Options{
		SessionID:               call.Meta.SessionID,
		ServiceTier:             call.Route.ServiceTier,
		Model:                   call.Route.UpstreamModel,
		Effort:                  effort,
		DisabledSkillToolSkills: p.DisabledSkillToolSkills,
		CacheKeyStrategy:        p.CacheKeyStrategy,
	})
	if err != nil {
		return err
	}
	if p.Verbose {
		breakdown := translatedBodyBreakdown(body)
		log.Debug("codex request translated",
			"request_id", call.Meta.RequestID,
			"model", body.Model,
			"input_items", len(body.Input),
			"tools", len(body.Tools),
			"store", body.Store,
			"service_tier", body.ServiceTier,
			"reasoning_effort", reasoningEffort(body.Reasoning),
			"include_reasoning", containsString(body.Include, "reasoning.encrypted_content"),
			"body_bytes", breakdown.BodyBytes,
			"instructions_bytes", breakdown.InstructionsBytes,
			"tools_bytes", breakdown.ToolsBytes,
			"input_bytes", breakdown.InputBytes,
			"prefix_fingerprint", breakdown.PrefixFingerprint,
			"cache_key_strategy", p.cacheKeyStrategyLabel(),
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
		log.Warn("codex upstream rejected", "request_id", call.Meta.RequestID, "status", resp.StatusCode, "body", bodySnippet)
		return provider.UpstreamError{Provider: "codex", StatusCode: resp.StatusCode, Body: bodySnippet}
	}
	if !call.Request.WantsStream() {
		var usage translate.Usage
		hasUsage := false
		message, err := translate.TranslateResponse(resp.Body, translate.StreamOptions{
			MessageID: "msg_" + call.Meta.RequestID,
			Model:     call.Route.UpstreamModel,
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
				"cached_pct", cachedPctOf(usage),
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
	streamStart := time.Now()
	counting := &countingWriter{w: writer}
	var usage translate.Usage
	hasUsage := false
	err = translate.TranslateStream(resp.Body, counting, translate.StreamOptions{
		MessageID: "msg_" + call.Meta.RequestID,
		Model:     call.Route.UpstreamModel,
		OnUsage: func(u translate.Usage) {
			usage = u
			hasUsage = true
		},
	})
	if err != nil {
		log.Warn("codex stream translation failed",
			"request_id", call.Meta.RequestID,
			"duration_ms", time.Since(streamStart).Milliseconds(),
			"bytes_emitted", counting.bytes,
			"error", err.Error(),
		)
		// If we already wrote some events the client can recover by treating
		// the stream as truncated; if we wrote nothing, salvage with a
		// message_start + error + message_stop sequence so Claude Code shows
		// a real error instead of "Stream ended without receiving any events".
		if salvageErr := salvageStreamIfNeeded(counting, call.Meta.RequestID, call.Route.UpstreamModel, err); salvageErr != nil {
			log.Warn("codex stream salvage failed",
				"request_id", call.Meta.RequestID,
				"error", salvageErr.Error(),
			)
		}
		return nil
	}
	if hasUsage {
		log.Info("codex usage",
			"request_id", call.Meta.RequestID,
			"input_tokens", usage.InputTokens,
			"cached_input_tokens", usage.InputTokensDetails.CachedTokens,
			"cached_pct", cachedPctOf(usage),
			"output_tokens", usage.OutputTokens,
			"reasoning_tokens", usage.OutputTokensDetails.ReasoningTokens,
			"total_tokens", usage.TotalTokens,
		)
	}
	return err
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

// translatedBody captures the post-translation request shape we expose in
// verbose logs so prefix stability and request bloat are measurable across
// turns without reading the full request body.
type translatedBody struct {
	BodyBytes         int
	InstructionsBytes int
	ToolsBytes        int
	InputBytes        int
	PrefixFingerprint string
}

func translatedBodyBreakdown(body translate.ResponsesRequest) translatedBody {
	out := translatedBody{InstructionsBytes: len(body.Instructions)}
	if encoded, err := json.Marshal(body); err == nil {
		out.BodyBytes = len(encoded)
	}
	toolsJSON, _ := json.Marshal(body.Tools)
	out.ToolsBytes = len(toolsJSON)
	inputJSON, _ := json.Marshal(body.Input)
	out.InputBytes = len(inputJSON)
	// Fingerprint covers the cache-relevant prefix (instructions + tool list).
	// Same prefix turn-over-turn means the model side can hit its prompt cache.
	sum := sha256.New()
	sum.Write([]byte(body.Instructions))
	sum.Write([]byte{0})
	sum.Write(toolsJSON)
	out.PrefixFingerprint = hex.EncodeToString(sum.Sum(nil))[:12]
	return out
}

func (p Provider) cacheKeyStrategyLabel() string {
	if p.CacheKeyStrategy == "" {
		return string(translate.CacheKeyStrategySession)
	}
	return string(p.CacheKeyStrategy)
}

// countingWriter forwards writes to w while tracking the total byte count so we
// can tell, after a stream failure, whether the client already saw any events.
type countingWriter struct {
	w     io.Writer
	bytes int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.bytes += n
	return n, err
}

// salvageStreamIfNeeded writes a minimal Anthropic-shaped error stream when the
// underlying writer has not produced any bytes yet, so Claude Code receives a
// readable failure event instead of a silent "Stream ended without receiving
// any events" message.
func salvageStreamIfNeeded(w *countingWriter, requestID, model string, cause error) error {
	if w.bytes > 0 {
		return nil
	}
	return salvageStreamFailure(w, requestID, model, cause)
}

func salvageStreamFailure(w io.Writer, requestID, model string, cause error) error {
	messageID := "msg_" + requestID
	start, err := sseEncode("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]int{
				"input_tokens":                0,
				"output_tokens":               0,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	})
	if err != nil {
		return err
	}
	message := "Upstream stream ended before any events"
	if cause != nil {
		message = message + ": " + cause.Error()
	}
	errEvent, err := sseEncode("error", map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    "stream_error",
			"message": message,
		},
	})
	if err != nil {
		return err
	}
	stop, err := sseEncode("message_stop", map[string]string{"type": "message_stop"})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, start+errEvent+stop); err != nil {
		return err
	}
	return nil
}

func sseEncode(event string, payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return "event: " + event + "\ndata: " + string(data) + "\n\n", nil
}

func cachedPctOf(usage translate.Usage) float64 {
	if usage.InputTokens <= 0 {
		return 0
	}
	return math.Round(float64(usage.InputTokensDetails.CachedTokens)/float64(usage.InputTokens)*1000) / 10
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
