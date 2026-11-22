// Package api implements the REST API (docs/API.md) and serves the web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/finn-freitag/tossbox/internal/clientip"
	"github.com/finn-freitag/tossbox/internal/core"
	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/finn-freitag/tossbox/internal/secrets"
	"github.com/finn-freitag/tossbox/internal/store"
	"github.com/finn-freitag/tossbox/internal/version"
)

const (
	sessionCookie = "tossbox_session"
	csrfHeader    = "X-Tossbox-CSRF"
)

// Server is the HTTP API.
type Server struct {
	core  *core.Service
	log   *slog.Logger
	ui    http.Handler
	login *loginLimiter
}

// New creates the API server; ui serves everything outside /api.
func New(c *core.Service, ui http.Handler, log *slog.Logger) *Server {
	return &Server{core: c, log: log.With("component", "api"), ui: ui, login: &loginLimiter{m: map[string][]time.Time{}}}
}

type ctxKey struct{}

type authInfo struct {
	p           core.Principal
	sessionHash string
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.Handle(pattern, s.wrap(fn, false, false))
	}
	auth := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.Handle(pattern, s.wrap(fn, true, false))
	}
	admin := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.Handle(pattern, s.wrap(fn, true, true))
	}

	mux.HandleFunc("GET /healthz", s.healthz)
	h("GET /api/info", s.info)
	h("POST /api/auth/login", s.loginHandler)
	h("POST /api/auth/logout", s.logout)
	auth("GET /api/auth/me", s.me)
	auth("PUT /api/me/ssh-keys", s.setSSHKeys)
	auth("PUT /api/me/adb-keys", s.setADBKeys)
	auth("POST /api/me/password", s.changePassword)
	auth("GET /api/me/tokens", s.listTokens)
	auth("POST /api/me/tokens", s.createToken)
	auth("DELETE /api/me/tokens/{id}", s.deleteToken)

	auth("GET /api/templates", s.templates)
	admin("POST /api/templates/reload", s.reloadTemplates)
	auth("GET /api/create-options", s.createOptions)

	auth("GET /api/devices", s.listDevices)
	auth("POST /api/devices", s.createDevice)
	auth("GET /api/devices/{id}", s.getDevice)
	auth("PATCH /api/devices/{id}", s.updateDevice)
	auth("DELETE /api/devices/{id}", s.deleteDevice)
	auth("GET /api/devices/{id}/rdp", s.rdpFile)
	auth("GET /api/devices/{id}/ssh-config", s.sshConfig)
	auth("POST /api/devices/{id}/guacamole", s.guacamole)

	admin("GET /api/nodes", s.listNodes)
	admin("POST /api/nodes", s.createNode)
	admin("GET /api/nodes/{id}", s.getNode)
	admin("PATCH /api/nodes/{id}", s.updateNode)
	admin("DELETE /api/nodes/{id}", s.deleteNode)
	admin("POST /api/nodes/{id}/token", s.nodeToken)

	admin("GET /api/users", s.listUsers)
	admin("POST /api/users", s.createUser)
	admin("GET /api/users/{id}", s.getUser)
	admin("PATCH /api/users/{id}", s.updateUser)
	admin("DELETE /api/users/{id}", s.deleteUser)

	admin("GET /api/settings", s.getSettings)
	admin("PUT /api/settings", s.putSettings)

	mux.Handle("GET /api/events", s.wrap(s.eventsHandler, true, false))
	mux.Handle("/api/", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return model.Errf(model.CodeNotFound, "no such endpoint")
	}, false, false))
	if s.ui != nil {
		mux.Handle("/", s.ui)
	}
	return securityHeaders(mux, s.core.Config().SecureCookies)
}

// contentSecurityPolicy only allows the UI's own scripts and API calls.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(next http.Handler, https bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if https || r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// wrap adds authentication, CSRF checks and error rendering.
func (s *Server) wrap(fn func(http.ResponseWriter, *http.Request) error, needAuth, needAdmin bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if needAuth {
			ai, err := s.authenticate(r)
			if err != nil {
				writeError(w, err)
				return
			}
			if needAdmin && !ai.p.IsAdmin() {
				writeError(w, model.Errf(model.CodeForbidden, "admin only"))
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, ai))
		}
		if err := fn(w, r); err != nil {
			e := model.AsError(err)
			if e.Code == model.CodeInternal {
				s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
				e = &model.Error{Code: model.CodeInternal, Msg: "internal error"}
			}
			writeError(w, e)
		}
	})
}

func (s *Server) authenticate(r *http.Request) (*authInfo, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		u, err := s.core.Store().APITokenUser(ctx, secrets.Hash(strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))))
		if err != nil {
			return nil, model.Errf(model.CodeUnauthorized, "invalid API token")
		}
		return &authInfo{p: core.Principal{User: u, ViaToken: true}}, nil
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, model.Errf(model.CodeUnauthorized, "not logged in")
	}
	hash := secrets.Hash(c.Value)
	u, err := s.core.Store().SessionUser(ctx, hash)
	if err != nil {
		return nil, model.Errf(model.CodeUnauthorized, "session expired")
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && r.Header.Get(csrfHeader) != "1" {
		return nil, model.Errf(model.CodeForbidden, "missing %s header", csrfHeader)
	}
	return &authInfo{p: core.Principal{User: u}, sessionHash: hash}, nil
}

func principal(r *http.Request) core.Principal { return r.Context().Value(ctxKey{}).(*authInfo).p }

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	e := model.AsError(err)
	writeJSON(w, e.Status(), map[string]string{"error": e.Msg, "code": e.Code})
}

func decode(r *http.Request, v any) error {
	body := io.LimitReader(r.Body, 1<<20)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return model.Errf(model.CodeValidation, "request body required")
		}
		return model.Errf(model.CodeValidation, "invalid JSON: %v", err)
	}
	return nil
}

// clientIP is the caller's address; X-Forwarded-For is only believed from
// TOSSBOX_TRUSTED_PROXIES.
func (s *Server) clientIP(r *http.Request) string {
	if a := clientip.From(r, s.core.Config().TrustedProxies); a.IsValid() {
		return a.String()
	}
	return r.RemoteAddr
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.core.Store().Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok\n"))
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) error {
	n, err := s.core.Store().CountUsers(r.Context())
	if err != nil {
		return err
	}
	cfg := s.core.Config()
	return writeJSON(w, http.StatusOK, map[string]any{
		"version":           version.Version,
		"base_domain":       cfg.BaseDomain,
		"guacamole_enabled": cfg.GuacamoleEnabled(),
		"setup_required":    n == 0,
	})
}

// ---------------------------------------------------------------- auth

// loginLimiter allows 10 failed logins per IP per 10 minutes.
type loginLimiter struct {
	mu     sync.Mutex
	m      map[string][]time.Time
	lastGC time.Time
}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	// Forget stale entries of other IPs now and then (bounded memory).
	if time.Since(l.lastGC) > 10*time.Minute {
		for k, ts := range l.m {
			if len(ts) == 0 || ts[len(ts)-1].Before(cut) {
				delete(l.m, k)
			}
		}
		l.lastGC = time.Now()
	}
	var keep []time.Time
	for _, t := range l.m[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.m, ip)
	} else {
		l.m[ip] = keep
	}
	return len(keep) >= 10
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	l.m[ip] = append(l.m[ip], time.Now())
	l.mu.Unlock()
}

// dummyHash makes failed lookups take as long as a password check.
var dummyHash, _ = core.HashPassword("tossbox-dummy-password")

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) error {
	ip := s.clientIP(r)
	if s.login.blocked(ip) {
		return model.Errf(model.CodeUnauthorized, "too many failed logins, try again later")
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	u, err := s.core.Store().GetUserByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		core.CheckPassword(dummyHash, req.Password)
		s.login.fail(ip)
		return model.Errf(model.CodeUnauthorized, "invalid username or password")
	}
	if !core.CheckPassword(u.PasswordHash, req.Password) {
		s.login.fail(ip)
		return model.Errf(model.CodeUnauthorized, "invalid username or password")
	}
	tok, err := secrets.Token("")
	if err != nil {
		return err
	}
	cfg := s.core.Config()
	exp := time.Now().Add(cfg.SessionTTL)
	if err := s.core.Store().CreateSession(r.Context(), secrets.Hash(tok), u.ID, exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true,
		Secure: cfg.SecureCookies || r.TLS != nil, SameSite: http.SameSiteLaxMode,
	})
	s.core.Store().Audit(r.Context(), u.ID, "auth.login", "", map[string]any{"ip": ip})
	v, err := s.core.UserView(r.Context(), u)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, v)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		s.core.Store().DeleteSession(r.Context(), secrets.Hash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	v, err := s.core.UserView(r.Context(), principal(r).User)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, v)
}

func (s *Server) setSSHKeys(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		SSHKeys []string `json:"ssh_keys"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	v, err := s.core.SetSSHKeys(r.Context(), principal(r), req.SSHKeys)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, v)
}

func (s *Server) setADBKeys(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ADBKeys []string `json:"adb_keys"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	v, err := s.core.SetADBKeys(r.Context(), principal(r), req.ADBKeys)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, v)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ai := r.Context().Value(ctxKey{}).(*authInfo)
	if err := s.core.ChangePassword(r.Context(), ai.p, req.Current, req.New, ai.sessionHash); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) error {
	toks, err := s.core.Store().ListAPITokens(r.Context(), principal(r).User.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toks)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		return model.Errf(model.CodeValidation, "name is required (max 100 characters)")
	}
	if req.ExpiresInDays < 0 {
		return model.Errf(model.CodeValidation, "expires_in_days must be >= 0")
	}
	plain, err := secrets.Token("tbt_")
	if err != nil {
		return err
	}
	t := &store.APIToken{ID: newID(), UserID: principal(r).User.ID, Name: req.Name}
	if req.ExpiresInDays > 0 {
		e := time.Now().AddDate(0, 0, req.ExpiresInDays).UTC()
		t.ExpiresAt = &e
	}
	if err := s.core.Store().CreateAPIToken(r.Context(), t, secrets.Hash(plain)); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, map[string]any{"id": t.ID, "name": t.Name, "token": plain, "created_at": t.CreatedAt, "expires_at": t.ExpiresAt})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) error {
	if !validID(r.PathValue("id")) {
		return model.Errf(model.CodeNotFound, "token not found")
	}
	if err := s.core.Store().DeleteAPIToken(r.Context(), principal(r).User.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.Errf(model.CodeNotFound, "token not found")
		}
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
