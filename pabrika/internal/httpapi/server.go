package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
)

// Access is the credential level a route requires.
type Access int

const (
	// AccessUnset is the zero value: a route registered without choosing an Access. The guard
	// treats it as SessionOnly + Write (fail closed) and the coverage test fails on it.
	AccessUnset Access = iota
	// Public needs no credentials (signup, login, /auth/config, /healthz, the SPA fallback).
	Public
	// Authed accepts a session cookie or a bearer token.
	Authed
	// SessionOnly accepts a session cookie only; a token gets 403 session_required.
	SessionOnly
)

func (a Access) String() string {
	switch a {
	case Public:
		return "Public"
	case Authed:
		return "Authed"
	case SessionOnly:
		return "SessionOnly"
	}
	return "AccessUnset"
}

// Route is one entry of the route table, the single source of truth for authentication.
type Route struct {
	Pattern string // "POST /api/v1/tickets/{id}/move" (Go 1.22 mux syntax)
	Access  Access
	Write   bool // changes data: a read-scope token gets 403 insufficient_scope
	Handler http.HandlerFunc
}

// Deps are the collaborators of a Server. Config, Store and Services are required; everything
// else has a production default.
type Deps struct {
	Config   config.Config
	Store    *store.Store
	Services *service.Services

	Logger *slog.Logger     // default slog.Default()
	Now    func() time.Time // default time.Now().UTC(); tests share one fake clock with the services

	Sessions *auth.Sessions // default built from Store, Now and Config.CookieSecure
	Resolver *auth.Resolver // default built from Store, Sessions, Now
	Hasher   *auth.Hasher   // default auth.DefaultParams, auth.DefaultConcurrency

	// Rate limiters (5 attempts per minute per key, spec section 7). Defaults are built with Now.
	LoginLimiter    *auth.Limiter // POST /auth/login, key auth.LoginKey(ip, email)
	SignupLimiter   *auth.Limiter // POST /auth/signup, same key, separate buckets
	PasswordLimiter *auth.Limiter // POST /auth/me/password, key auth.PasswordKey(userID, ip)

	// Hub is the phase 3 event hub. The SSE handler (events.go) subscribes through it; serve
	// also passes it to service.Deps as Publisher and StreamControl and calls Hub.Shutdown
	// from its shutdown hook. Nil in tests that do not stream.
	Hub *service.Hub
}

// Server is the REST API. Build it with New, serve Handler().
type Server struct {
	cfg      config.Config
	st       *store.Store
	svc      *service.Services
	log      *slog.Logger
	now      func() time.Time
	sessions *auth.Sessions
	resolver *auth.Resolver
	hasher   *auth.Hasher
	hub      *service.Hub

	loginLimiter, signupLimiter, passwordLimiter *auth.Limiter

	mux     *http.ServeMux
	handler http.Handler

	mu       sync.Mutex
	routes   []Route
	fallback http.Handler
}

// New builds the server: fills defaults, registers every route and wraps the mux in the global
// middleware chain.
func New(d Deps) *Server {
	if d.Store == nil || d.Services == nil {
		panic("httpapi.New: Store and Services are required")
	}
	s := &Server{cfg: d.Config, st: d.Store, svc: d.Services, log: d.Logger, now: d.Now,
		sessions: d.Sessions, resolver: d.Resolver, hasher: d.Hasher, hub: d.Hub,
		loginLimiter: d.LoginLimiter, signupLimiter: d.SignupLimiter, passwordLimiter: d.PasswordLimiter}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	if s.sessions == nil {
		s.sessions = auth.NewSessions(s.st, auth.SessionOptions{Now: s.now, CookieSecure: s.cfg.CookieSecure, Logger: s.log})
	}
	if s.resolver == nil {
		s.resolver = auth.NewResolver(auth.ResolverDeps{Store: s.st, Sessions: s.sessions, Now: s.now, Logger: s.log})
	}
	if s.hasher == nil {
		s.hasher = auth.NewHasher(auth.DefaultParams, auth.DefaultConcurrency)
	}
	for _, l := range []**auth.Limiter{&s.loginLimiter, &s.signupLimiter, &s.passwordLimiter} {
		if *l == nil {
			*l = auth.NewLimiter(5, time.Minute, 0, s.now)
		}
	}
	s.mux = http.NewServeMux()
	s.registerRoutes()
	s.handler = s.buildChain(s.mux)
	return s
}

// Handler is the full handler: global middleware around the mux.
func (s *Server) Handler() http.Handler { return s.handler }

// Sessions returns the session store (serve starts its purge job with it).
func (s *Server) Sessions() *auth.Sessions { return s.sessions }

// Routes returns a copy of the route table (Mount and the registerXRoutes methods add to it),
// in registration order. Tests enumerate it.
func (s *Server) Routes() []Route {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Route(nil), s.routes...)
}

// StartBackground runs the limiter sweepers until ctx is done and returns a channel that is
// closed when they have stopped.
func (s *Server) StartBackground(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, l := range []*auth.Limiter{s.loginLimiter, s.signupLimiter, s.passwordLimiter} {
		wg.Add(1)
		go func(l *auth.Limiter) {
			defer wg.Done()
			l.RunSweeper(ctx, time.Minute)
		}(l)
	}
	go func() { wg.Wait(); close(done) }()
	return done
}

// registerRoutes builds the whole route table. Each domain registers in its own file through
// an empty-by-default method, so work packages can fill them in parallel without touching this
// function. events.go (SSE) mounts its route through Mount from serve or its own method.
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/", s.apiCatchAll)
	s.handle(Route{Pattern: "/", Access: Public, Handler: s.serveFallback})
	s.registerHealthRoutes()
	s.registerAuthRoutes()
	s.registerTokenRoutes()
	s.registerProjectRoutes()
	s.registerMemberRoutes()
	s.registerLabelRoutes()
	s.registerTicketRoutes()
	s.registerCommentRoutes()
}

// handle adds rt to the route table and the mux under the route guard. Handler agents call
// it (usually through the small helpers below) from their registerXRoutes method.
func (s *Server) handle(rt Route) {
	s.mu.Lock()
	s.routes = append(s.routes, rt)
	s.mu.Unlock()
	s.mux.Handle(rt.Pattern, s.guard(rt))
}

// route is shorthand for handle.
func (s *Server) route(pattern string, access Access, write bool, h http.HandlerFunc) {
	s.handle(Route{Pattern: pattern, Access: access, Write: write, Handler: h})
}

// Mount registers pattern in the route table under the full guard chain (phase 3 events).
// It appears in Routes(). A method-less "/" is the fallback catch-all and behaves like
// SetFallback (Public; the handler receives every method and answers 405 itself).
func (s *Server) Mount(pattern string, access Access, h http.Handler) {
	if pattern == "/" {
		s.SetFallback(h)
		return
	}
	s.handle(Route{Pattern: pattern, Access: access, Handler: h.ServeHTTP})
}

// MountRaw registers h on the mux with only the global middleware (recover, request id and
// log, headers, body cap and the /mcp Origin rule): no route guard, no Routes() entry. /mcp
// authenticates itself with Resolver.ResolveBearer and writes failures with WriteError.
func (s *Server) MountRaw(pattern string, h http.Handler) {
	s.mux.Handle(pattern, h)
}

// SetFallback sets the handler for everything not under /api/, /mcp or /healthz (the phase 6
// static UI). It is the method-less "/" catch-all, so it sees every method. Unknown paths
// under /.well-known/ always get a JSON 404 before it runs. The default is a plain 404.
func (s *Server) SetFallback(h http.Handler) {
	s.mu.Lock()
	s.fallback = h
	s.mu.Unlock()
}

func (s *Server) serveFallback(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/.well-known/") {
		WriteError(w, http.StatusNotFound, CodeNotFound, "Not found")
		return
	}
	s.mu.Lock()
	h := s.fallback
	s.mu.Unlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h.ServeHTTP(w, r)
}

// apiCatchAll answers every /api/ path no route matched: JSON 404, or JSON 405 with an Allow
// header when the path is registered under other methods. (Go 1.22 mux: never register
// "GET /" next to this method-less catch-all, they conflict.)
func (s *Server) apiCatchAll(w http.ResponseWriter, r *http.Request) {
	infoFrom(r.Context()).pattern = "/api/"
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := &http.Request{Method: m, URL: &url.URL{Path: r.URL.Path}, Host: r.Host, Header: http.Header{}}
		if _, pat := s.mux.Handler(probe); pat != "" && pat != "/api/" {
			allow = append(allow, m)
			if m == http.MethodGet {
				allow = append(allow, http.MethodHead)
			}
		}
	}
	if len(allow) == 0 {
		WriteError(w, http.StatusNotFound, CodeNotFound, "Not found")
		return
	}
	sort.Strings(allow)
	w.Header().Set("Allow", strings.Join(allow, ", "))
	WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method not allowed")
}

func (s *Server) registerHealthRoutes() {
	s.route("GET /healthz", Public, false, s.healthz)
}

// healthz pings the database: 200 {"status":"ok"} or 503 without driver detail.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var one int
	if err := s.st.RawQueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		s.log.Error("healthz: database unavailable", "err", err)
		WriteError(w, http.StatusServiceUnavailable, CodeInternal, "Database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- helpers for handlers ----

// principal returns the authenticated principal. The guard stores it on every non-Public route.
func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

// clientIP is the address the rate limiter and the log key on (TRUST_PROXY aware).
func (s *Server) clientIP(r *http.Request) string { return auth.ClientIP(r, s.cfg.TrustProxy) }

// writeRateLimited writes 429 with Retry-After.
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
	WriteError(w, http.StatusTooManyRequests, CodeRateLimited, "Too many attempts, try again later")
}
