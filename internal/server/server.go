package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/lobo235/cc-proxy/internal/config"
	"github.com/lobo235/cc-proxy/internal/modelregistry"
	"github.com/lobo235/cc-proxy/internal/provider"
)

const (
	sessionIdleTTL = 30 * time.Minute
	maxSessions    = 10_000
)

type Providers struct {
	Codex provider.Provider
	Kimi  provider.Provider
}

type Server struct {
	cfg      config.Config
	log      *slog.Logger
	provider Providers
	sessions map[string]*sessionState
	version  string
}

type sessionState struct {
	Seq              int
	AffinityProvider config.AliasProvider
	HasAffinity      bool
	LastSeen         time.Time
}

func New(cfg config.Config, providers Providers, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg:      cfg,
		log:      log,
		provider: providers,
		sessions: make(map[string]*sessionState),
		version:  "dev",
	}
}

func (s *Server) SetVersion(version string) {
	if version == "" {
		version = "dev"
	}
	s.version = version
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/v1/messages/count_tokens", s.handleCountTokens)
	return withNotFound(mux)
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       255 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("server listening", "addr", addr)
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("No route for %s %s", r.Method, r.URL.Path))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("No route for %s %s", r.Method, r.URL.Path))
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{
		OK:      true,
		Version: s.version,
		Routes:  []string{"/healthz", "/status", "/v1/messages", "/v1/messages/count_tokens"},
		Providers: map[string]providerStatus{
			"codex": {
				Messages:    "partial",
				CountTokens: "ready",
				Auth:        "status_logout",
				Models:      appendCodexModels(),
			},
			"kimi": {
				Messages:    "not_implemented",
				CountTokens: "not_implemented",
				Auth:        "status_logout",
				Models:      append([]string{}, modelregistry.KimiModels...),
			},
		},
	})
}

type statusResponse struct {
	OK        bool                      `json:"ok"`
	Version   string                    `json:"version"`
	Routes    []string                  `json:"routes"`
	Providers map[string]providerStatus `json:"providers"`
}

type providerStatus struct {
	Messages    string   `json:"messages"`
	CountTokens string   `json:"count_tokens"`
	Auth        string   `json:"auth"`
	Models      []string `json:"models"`
}

func appendCodexModels() []string {
	models := append([]string{}, modelregistry.CodexModels...)
	for _, model := range modelregistry.CodexModels {
		models = append(models, model+"-fast")
	}
	return models
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("No route for %s %s", r.Method, r.URL.Path))
		return
	}
	req, ok := s.parseProviderRequest(w, r)
	if !ok {
		return
	}
	p, meta, ok := s.routeProvider(w, r, req)
	if !ok {
		return
	}
	if err := p.Messages(r.Context(), meta.messagesCall(req, r), httpMessagesOut{w: w}); err != nil {
		s.writeProviderError(w, err)
	}
}

func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("No route for %s %s", r.Method, r.URL.Path))
		return
	}
	req, ok := s.parseProviderRequest(w, r)
	if !ok {
		return
	}
	p, meta, ok := s.routeProvider(w, r, req)
	if !ok {
		return
	}
	resp, err := p.CountTokens(r.Context(), meta.countTokensCall(req, r))
	if err != nil {
		s.writeProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) parseProviderRequest(w http.ResponseWriter, r *http.Request) (provider.AnthropicMessagesRequest, bool) {
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Invalid request body: "+err.Error())
		return provider.AnthropicMessagesRequest{}, false
	}
	var req provider.AnthropicMessagesRequest
	if err := json.Unmarshal(data, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON: "+err.Error())
		return provider.AnthropicMessagesRequest{}, false
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", `Missing "model" in request body. `+modelregistry.SupportedMessage(s.cfg.AliasProvider))
		return provider.AnthropicMessagesRequest{}, false
	}
	req.Model = modelregistry.NormalizeIncomingModel(req.Model)
	req.Raw = append(req.Raw[:0], data...)
	return req, true
}

type routedCall struct {
	route provider.Route
	meta  provider.CallMeta
}

func (c routedCall) messagesCall(req provider.AnthropicMessagesRequest, r *http.Request) provider.MessagesCall {
	return provider.MessagesCall{
		Request: req,
		Route:   c.route,
		Meta:    c.meta,
		Client:  clientMeta(r),
	}
}

func (c routedCall) countTokensCall(req provider.AnthropicMessagesRequest, r *http.Request) provider.CountTokensCall {
	return provider.CountTokensCall{
		Request: req,
		Route:   c.route,
		Meta:    c.meta,
		Client:  clientMeta(r),
	}
}

func clientMeta(r *http.Request) provider.ClientMeta {
	return provider.ClientMeta{
		Headers: r.Header.Clone(),
		Remote:  r.RemoteAddr,
	}
}

func (s *Server) routeProvider(w http.ResponseWriter, r *http.Request, req provider.AnthropicMessagesRequest) (provider.Provider, routedCall, bool) {
	sessionID := r.Header.Get("x-claude-code-session-id")
	now := time.Now()
	session := s.existingSession(sessionID, now)
	aliasProvider := s.cfg.AliasProvider
	if session != nil && session.HasAffinity {
		aliasProvider = session.AffinityProvider
	}
	resolved, ok := modelregistry.Resolve(req.Model, aliasProvider)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown model %q. %s", req.Model, modelregistry.SupportedMessage(s.cfg.AliasProvider)))
		return nil, routedCall{}, false
	}
	current := s.recordSessionRequest(sessionID, session, resolved.Provider, req.Model, now)
	meta := provider.CallMeta{
		RequestID:  randomID(),
		SessionID:  sessionID,
		SessionSeq: 0,
	}
	if current != nil {
		meta.SessionSeq = current.Seq
	}
	call := routedCall{
		route: provider.Route{
			Provider:      provider.NameFromRegistry(resolved.Provider),
			IncomingModel: req.Model,
			UpstreamModel: resolved.Model,
			ServiceTier:   resolved.ServiceTier,
		},
		meta: meta,
	}
	switch resolved.Provider {
	case modelregistry.ProviderCodex:
		return s.provider.Codex, call, true
	case modelregistry.ProviderKimi:
		return s.provider.Kimi, call, true
	default:
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown provider for model %q", req.Model))
		return nil, routedCall{}, false
	}
}

func (s *Server) existingSession(sessionID string, now time.Time) *sessionState {
	if sessionID == "" {
		return nil
	}
	state := s.sessions[sessionID]
	if state == nil {
		return nil
	}
	if now.Sub(state.LastSeen) > sessionIdleTTL {
		delete(s.sessions, sessionID)
		return nil
	}
	return state
}

func (s *Server) recordSessionRequest(sessionID string, session *sessionState, providerName modelregistry.Provider, model string, now time.Time) *sessionState {
	if sessionID == "" {
		return nil
	}
	state := session
	if state == nil {
		state = &sessionState{LastSeen: now}
	}
	state.Seq++
	state.LastSeen = now
	if !isAnthropicAlias(model) {
		if providerName == modelregistry.ProviderCodex {
			state.AffinityProvider = config.AliasProviderCodex
			state.HasAffinity = true
		}
		if providerName == modelregistry.ProviderKimi {
			state.AffinityProvider = config.AliasProviderKimi
			state.HasAffinity = true
		}
	}
	s.sessions[sessionID] = state
	s.evictOldestSessions()
	return state
}

func (s *Server) evictOldestSessions() {
	for len(s.sessions) > maxSessions {
		var oldestID string
		var oldest time.Time
		for id, state := range s.sessions {
			if oldestID == "" || state.LastSeen.Before(oldest) {
				oldestID = id
				oldest = state.LastSeen
			}
		}
		delete(s.sessions, oldestID)
	}
}

func (s *Server) writeProviderError(w http.ResponseWriter, err error) {
	var notImpl provider.ErrNotImplemented
	if errors.As(err, &notImpl) {
		writeError(w, http.StatusNotImplemented, "not_implemented", notImpl.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

type httpMessagesOut struct {
	w http.ResponseWriter
}

func (o httpMessagesOut) StartStream(status int, header http.Header) (io.Writer, error) {
	copyHeader(o.w.Header(), header)
	if o.w.Header().Get("content-type") == "" {
		o.w.Header().Set("content-type", "text/event-stream")
	}
	o.w.WriteHeader(status)
	return flushWriter{w: o.w}, nil
}

func (o httpMessagesOut) WriteJSON(status int, header http.Header, body any) error {
	copyHeader(o.w.Header(), header)
	writeJSON(o.w, status, body)
	return nil
}

type flushWriter struct {
	w http.ResponseWriter
}

func (w flushWriter) Write(data []byte) (int, error) {
	n, err := w.w.Write(data)
	if flusher, ok := w.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}

func copyHeader(dst, src http.Header) {
	for k, values := range src {
		for _, v := range values {
			dst.Add(k, v)
		}
	}
}

func withNotFound(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.wrote {
			return
		}
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("No route for %s %s", r.Method, r.URL.Path))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	r.wrote = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, typ, message string) {
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    typ,
			"message": message,
		},
	})
}

func isAnthropicAlias(model string) bool {
	for _, alias := range modelregistry.AnthropicAliases {
		if model == alias {
			return true
		}
	}
	return false
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
