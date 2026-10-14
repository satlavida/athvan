// Package admin serves the API key dashboard at /admin. It is protected by
// a single username/password from the environment and a session cookie.
// These routes are deliberately not part of the public OpenAPI document.
package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/satlavida/athvan/internal/keys"
)

//go:embed admin.html
var page []byte

const (
	cookieName  = "athvan_admin"
	sessionTTL  = 12 * time.Hour
	maxFailures = 5
	lockout     = 15 * time.Minute
)

// Server is the admin dashboard.
type Server struct {
	store      *keys.Store
	user, pass [32]byte // SHA-256, so comparisons are constant-time and length-blind
	enabled    bool

	mu       sync.Mutex
	sessions map[string]time.Time // token → expiry
	failures map[string]*attempts // client IP → recent failed logins
}

type attempts struct {
	n     int
	first time.Time
}

// New returns the dashboard. With an empty user or password it is
// disabled and only explains how to turn it on.
func New(store *keys.Store, user, pass string) *Server {
	return &Server{
		store:    store,
		user:     sha256.Sum256([]byte(user)),
		pass:     sha256.Sum256([]byte(pass)),
		enabled:  user != "" && pass != "",
		sessions: map[string]time.Time{},
		failures: map[string]*attempts{},
	}
}

// Enabled reports whether admin credentials are configured.
func (s *Server) Enabled() bool { return s.enabled }

// Register adds the dashboard routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", s.servePage)
	mux.HandleFunc("POST /admin/api/login", s.login)
	mux.HandleFunc("POST /admin/api/logout", s.logout)
	mux.HandleFunc("GET /admin/api/me", s.authed(s.me))
	mux.HandleFunc("GET /admin/api/keys", s.authed(s.listKeys))
	mux.HandleFunc("POST /admin/api/keys", s.authed(s.createKey))
	mux.HandleFunc("POST /admin/api/keys/{id}/revoke", s.authed(s.setRevoked(true)))
	mux.HandleFunc("POST /admin/api/keys/{id}/restore", s.authed(s.setRevoked(false)))
	mux.HandleFunc("DELETE /admin/api/keys/{id}", s.authed(s.deleteKey))
}

// IsAdmin reports whether r carries a valid admin session. The preview
// page uses this so a signed-in admin can render without an API key.
func (s *Server) IsAdmin(r *http.Request) bool {
	if !s.enabled {
		return false
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if ok && time.Now().After(exp) {
		delete(s.sessions, c.Value)
		return false
	}
	return ok
}

func (s *Server) servePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(page)
}

// sameOrigin rejects cross-site state changes. SameSite=Strict already
// blocks the cookie; this is a second, independent check.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Method == http.MethodGet
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled {
			writeErr(w, http.StatusServiceUnavailable, "admin disabled: set ATHVAN_ADMIN_USER and ATHVAN_ADMIN_PASSWORD")
			return
		}
		if !s.IsAdmin(r) {
			writeErr(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			writeErr(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		h(w, r)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.enabled {
		writeErr(w, http.StatusServiceUnavailable, "admin disabled: set ATHVAN_ADMIN_USER and ATHVAN_ADMIN_PASSWORD")
		return
	}
	if !sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request refused")
		return
	}
	ip := clientIP(r)
	s.mu.Lock()
	a := s.failures[ip]
	if a != nil && time.Since(a.first) > lockout {
		delete(s.failures, ip)
		a = nil
	}
	locked := a != nil && a.n >= maxFailures
	s.mu.Unlock()
	if locked {
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts; try again later")
		return
	}

	var body struct{ Username, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	u := sha256.Sum256([]byte(body.Username))
	p := sha256.Sum256([]byte(body.Password))
	okUser := subtle.ConstantTimeCompare(u[:], s.user[:])
	okPass := subtle.ConstantTimeCompare(p[:], s.pass[:])
	if okUser&okPass != 1 {
		s.mu.Lock()
		if a := s.failures[ip]; a != nil {
			a.n++
		} else {
			s.failures[ip] = &attempts{n: 1, first: time.Now()}
		}
		s.mu.Unlock()
		time.Sleep(400 * time.Millisecond)
		writeErr(w, http.StatusUnauthorized, "wrong username or password")
		return
	}

	token := newToken()
	s.mu.Lock()
	delete(s.failures, ip)
	now := time.Now()
	for t, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, t)
		}
	}
	s.sessions[token] = now.Add(sessionTTL)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"admin": true})
}

// keyView is a key as the dashboard sees it: no hash.
type keyView struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Note       string           `json:"note,omitempty"`
	Prefix     string           `json:"prefix"`
	CreatedAt  time.Time        `json:"created_at"`
	RevokedAt  *time.Time       `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time       `json:"last_used_at,omitempty"`
	Uses       int64            `json:"uses"`
	Daily      map[string]int64 `json:"daily,omitempty"`
}

func view(k keys.Key) keyView {
	return keyView{k.ID, k.Name, k.Note, k.Prefix, k.CreatedAt, k.RevokedAt, k.LastUsedAt, k.Uses, k.Daily}
}

func (s *Server) listKeys(w http.ResponseWriter, _ *http.Request) {
	all := s.store.List()
	out := make([]keyView, len(all))
	for i, k := range all {
		out[i] = view(k)
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Note string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Note = strings.TrimSpace(body.Note)
	if body.Name == "" || utf8.RuneCountInString(body.Name) > 80 || utf8.RuneCountInString(body.Note) > 300 {
		writeErr(w, http.StatusUnprocessableEntity, "name is required (max 80 chars); note max 300 chars")
		return
	}
	k, secret, err := s.store.Create(body.Name, body.Note)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save key: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": view(k), "secret": secret})
}

func (s *Server) setRevoked(revoked bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.result(w, s.store.SetRevoked(r.PathValue("id"), revoked))
	}
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	s.result(w, s.store.Delete(r.PathValue("id")))
}

func (s *Server) result(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, keys.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
