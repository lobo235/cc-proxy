package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
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
	resp, err := p.HandleMessages(r.Context(), req, meta)
	if err != nil {
		s.writeProviderError(w, err)
		return
	}
	defer resp.Body.Close()
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = ioCopy(w, resp.Body)
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
	resp, err := p.HandleCountTokens(r.Context(), req, meta)
	if err != nil {
		s.writeProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) parseProviderRequest(w http.ResponseWriter, r *http.Request) (provider.Request, bool) {
	defer r.Body.Close()
	var req provider.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON: "+err.Error())
		return provider.Request{}, false
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", `Missing "model" in request body. `+modelregistry.SupportedMessage(s.cfg.AliasProvider))
		return provider.Request{}, false
	}
	req.Model = modelregistry.NormalizeIncomingModel(req.Model)
	return req, true
}

func (s *Server) routeProvider(w http.ResponseWriter, r *http.Request, req provider.Request) (provider.Provider, provider.Context, bool) {
	sessionID := r.Header.Get("x-claude-code-session-id")
	session := s.existingSession(sessionID, time.Now())
	aliasProvider := s.cfg.AliasProvider
	if session != nil && session.HasAffinity {
		aliasProvider = session.AffinityProvider
	}
	resolved, ok := modelregistry.Resolve(req.Model, aliasProvider)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown model %q. %s", req.Model, modelregistry.SupportedMessage(s.cfg.AliasProvider)))
		return nil, provider.Context{}, false
	}
	current := s.recordSessionRequest(sessionID, session, resolved.Provider, req.Model, time.Now())
	meta := provider.Context{
		RequestID:  randomID(),
		SessionID:  sessionID,
		SessionSeq: 0,
	}
	if current != nil {
		meta.SessionSeq = current.Seq
	}
	switch resolved.Provider {
	case modelregistry.ProviderCodex:
		return s.provider.Codex, meta, true
	case modelregistry.ProviderKimi:
		return s.provider.Kimi, meta, true
	default:
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown provider for model %q", req.Model))
		return nil, provider.Context{}, false
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
