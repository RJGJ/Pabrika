package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
)

// CSP is the exact Content-Security-Policy sent on every response (phase 2 spec section 11).
// Phase 6 audits it against the real bundle; header tests assert this constant.
const CSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"

// Other fixed header values.
const (
	PermissionsPolicy = "camera=(), microphone=(), geolocation=()"
	ReferrerPolicy    = "same-origin"
	HSTS              = "max-age=31536000"
	// MaxBodyBytes is the request body cap (1 MiB).
	MaxBodyBytes = 1 << 20
)

// respWriter records the status for the request log and the recover layer. It implements
// Unwrap (so http.NewResponseController reaches the real writer) and Flush (SSE needs it).
type respWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *respWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *respWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher.
func (w *respWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *respWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *respWriter) statusCode() int {
	if !w.wrote {
		return http.StatusOK
	}
	return w.status
}

// reqInfo is per-request mutable state shared between the layers (the request log reads what
// the guard learned after routing).
type reqInfo struct {
	id      string
	pattern string
	userID  string
}

type reqInfoKey struct{}

func infoFrom(ctx context.Context) *reqInfo {
	if i, ok := ctx.Value(reqInfoKey{}).(*reqInfo); ok {
		return i
	}
	return &reqInfo{} // detached (a handler called outside the chain, in tests)
}

// buildChain wraps the mux in the global middleware. Outermost first: request id and log,
// security headers, recover, body cap, Origin check, then the mux (which applies the route
// guard per route).
//
// Request id/log and headers sit outside recover (the spec lists recover first) so that the
// 500 written for a panic still carries X-Request-Id and the security headers and is logged
// with its real status.
func (s *Server) buildChain(next http.Handler) http.Handler {
	h := next
	h = s.originCheck(h)
	h = s.bodyCap(h)
	h = s.recoverPanics(h)
	h = s.securityHeaders(h)
	h = s.requestLog(h)
	return h
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		info := &reqInfo{id: ulid.Make().String()}
		rw := &respWriter{ResponseWriter: w}
		rw.Header().Set("X-Request-Id", info.id)
		r = r.WithContext(context.WithValue(r.Context(), reqInfoKey{}, info))
		defer func() {
			// Only these fields are ever logged: no headers, query string, cookies or bodies.
			s.log.Info("request",
				"request_id", info.id,
				"method", r.Method,
				"route", info.pattern,
				"path", r.URL.Path,
				"status", rw.statusCode(),
				"duration_ms", s.now().Sub(start).Milliseconds(),
				"user_id", info.userID,
				"ip", auth.ClientIP(r, s.cfg.TrustProxy),
			)
		}()
		next.ServeHTTP(rw, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	hsts := strings.HasPrefix(s.cfg.BaseURL, "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", ReferrerPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", PermissionsPolicy)
		if hsts {
			h.Set("Strict-Transport-Security", HSTS)
		}
		// Set BEFORE the handler runs so a handler can override it (the event stream sends
		// "no-store, no-transform").
		if noStorePath(r.URL.Path) {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func noStorePath(p string) bool {
	return strings.HasPrefix(p, "/api/") || p == "/api" || isMCPPath(p) || p == "/healthz"
}

func isMCPPath(p string) bool { return p == "/mcp" || strings.HasPrefix(p, "/mcp/") }

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			// Stack and panic value only: never headers or bodies.
			s.log.Error("panic recovered", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()),
				"request_id", w.Header().Get("X-Request-Id"), "path", r.URL.Path)
			if rw, ok := w.(*respWriter); !ok || !rw.wrote {
				WriteError(w, http.StatusInternalServerError, CodeInternal, "Internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) bodyCap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > MaxBodyBytes {
			WriteError(w, http.StatusBadRequest, CodeBodyTooLarge, "Request body is too large")
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// normalizeOrigin parses an Origin header value into the form Config.Origin returns:
// lowercase scheme and host, default port dropped. "null", paths and junk fail.
func normalizeOrigin(v string) (string, bool) {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", false
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, true
}

// originMatches reports whether the single Origin header value equals BASE_URL's origin.
func (s *Server) originMatches(values []string) bool {
	if len(values) != 1 {
		return false
	}
	got, ok := normalizeOrigin(values[0])
	return ok && got == s.cfg.Origin()
}

func isStateChanging(method string) bool {
	return method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete || method == http.MethodPut
}

// originCheck implements the CSRF rules (spec section 8):
//
//   - POST/PATCH/DELETE under /api/: an Origin header must match BASE_URL; without one the
//     request is allowed only when it carries an Authorization header.
//   - every method under /mcp: if an Origin is present it must match BASE_URL; absent is allowed.
//
// The Host and X-Forwarded-* headers are never consulted.
func (s *Server) originCheck(next http.Handler) http.Handler {
	deny := func(w http.ResponseWriter) {
		WriteError(w, http.StatusForbidden, CodeOriginMismatch, "Origin not allowed")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, hasOrigin := r.Header["Origin"]
		switch {
		case isMCPPath(r.URL.Path):
			if hasOrigin && !s.originMatches(origin) {
				deny(w)
				return
			}
		case strings.HasPrefix(r.URL.Path, "/api/") && isStateChanging(r.Method):
			if hasOrigin {
				if !s.originMatches(origin) {
					deny(w)
					return
				}
			} else if _, hasAuth := r.Header["Authorization"]; !hasAuth {
				deny(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// guard is the route guard (spec section 3). The order is part of the contract:
//
//  1. Public routes skip authentication.
//  2. Resolve the principal, else 401 (cookie actions of the resolver are applied).
//  3. SessionOnly and a token principal: 403 session_required.
//  4. Write and a read-scope token: 403 insufficient_scope.
//  5. Content-Type check of a present body: 415.
//  6. The handler (decode, resolve resource and membership 404, role 403, archived 409,
//     validation 422 happen in the handler and the service in that order).
//
// An Access left at AccessUnset is treated as SessionOnly + Write (fail closed).
func (s *Server) guard(rt Route) http.Handler {
	access, write := rt.Access, rt.Write
	if access == AccessUnset {
		access, write = SessionOnly, true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := infoFrom(r.Context())
		info.pattern = rt.Pattern
		if access != Public {
			p, err := s.resolver.Resolve(r)
			if err != nil {
				s.writeAuthError(w, r, err)
				return
			}
			if p.SetCookie != nil {
				http.SetCookie(w, p.SetCookie)
			}
			info.userID = p.User.ID
			r = r.WithContext(auth.WithPrincipal(r.Context(), p))
			if p.Method == auth.MethodToken && p.Token != nil {
				if access == SessionOnly {
					WriteError(w, http.StatusForbidden, service.CodeSessionRequired, "This action requires a signed-in session")
					return
				}
				if write && p.Token.Scope == service.ScopeRead {
					WriteError(w, http.StatusForbidden, service.CodeInsufficientScope, "This token has read-only scope")
					return
				}
			}
		}
		if isStateChanging(r.Method) && strings.Contains(rt.Pattern, "/api/") {
			if e := checkMediaType(r); e != nil {
				e.write(w)
				return
			}
		}
		rt.Handler(w, r)
	})
}

// writeAuthError renders a resolver failure: 401 unauthorized with one message whatever the
// reason, the cookie cleared when the resolver asks, WWW-Authenticate for bearer failures.
func (s *Server) writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *auth.AuthError
	switch {
	case errors.As(err, &ae):
		if ae.ClearCookie {
			http.SetCookie(w, s.sessions.ClearCookie())
		}
		if ae.Bearer {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "Authentication required")
	case errors.Is(err, auth.ErrNoCredentials):
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "Authentication required")
	default:
		s.log.Error("resolve principal failed", "err", err, "request_id", w.Header().Get("X-Request-Id"), "path", r.URL.Path)
		WriteError(w, http.StatusInternalServerError, CodeInternal, "Internal error")
	}
}

// retryAfterSeconds renders a Retry-After header value (whole seconds, at least 1).
func retryAfterSeconds(d time.Duration) string {
	n := int((d + time.Second - 1) / time.Second)
	if n < 1 {
		n = 1
	}
	return fmt.Sprint(n)
}
