package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
)

// testRoutes adds stub routes that exercise the guard and the middleware.
func (h *Harness) testRoutes() {
	s := h.Server
	echoUser := func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		writeJSON(w, 200, map[string]string{"user": p.User.ID, "method": p.Method})
	}
	s.route("GET /api/v1/_t/public", Public, false, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"ok": "public"})
	})
	s.route("GET /api/v1/_t/authed", Authed, false, echoUser)
	s.route("GET /api/v1/_t/session", SessionOnly, false, echoUser)
	s.route("POST /api/v1/_t/write", Authed, true, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		writeJSON(w, 200, map[string]string{"name": in.Name})
	})
	s.route("POST /api/v1/_t/session-write", SessionOnly, true, echoUser)
	s.route("POST /api/v1/_t/nobody", Authed, false, func(w http.ResponseWriter, r *http.Request) { noContent(w) })
	s.route("GET /api/v1/_t/panic", Public, false, func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom: secret-detail")
	})
	s.route("GET /api/v1/_t/flush", Public, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: hello\n\n"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			panic(err)
		}
	})
	s.handle(Route{Pattern: "GET /api/v1/_t/unset", Handler: echoUser})
}

func TestRoutesAllHaveExplicitAccess(t *testing.T) {
	h := Setup(t, Opts{})
	n := 0
	for _, rt := range h.Server.Routes() {
		if rt.Access == AccessUnset {
			t.Errorf("route %q has no explicit Access", rt.Pattern)
		}
		if rt.Handler == nil {
			t.Errorf("route %q has no handler", rt.Pattern)
		}
		n++
	}
	if n < 2 { // "/" and /healthz
		t.Fatalf("routes: %d", n)
	}
	// The detection itself works: a route registered without Access is AccessUnset.
	h.testRoutes()
	found := false
	for _, rt := range h.Server.Routes() {
		if rt.Pattern == "GET /api/v1/_t/unset" {
			found = rt.Access == AccessUnset
		}
	}
	if !found {
		t.Fatal("AccessUnset sentinel not detectable")
	}
}

func TestGuardOrder(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	owner := h.Signup(t, "o@x.io")
	read, _ := h.MkToken(t, owner, service.ScopeRead, "")
	write, _ := h.MkToken(t, owner, service.ScopeWrite, "")

	body := map[string]any{"name": "x"}
	tests := []struct {
		name   string
		method string
		path   string
		body   any
		opts   []ReqOpt
		code   int
		errc   string
	}{
		{"public needs nothing", "GET", "/api/v1/_t/public", nil, nil, 200, ""},
		{"authed anonymous", "GET", "/api/v1/_t/authed", nil, nil, 401, "unauthorized"},
		{"authed session", "GET", "/api/v1/_t/authed", nil, []ReqOpt{As(owner)}, 200, ""},
		{"authed read token", "GET", "/api/v1/_t/authed", nil, []ReqOpt{Bearer(read)}, 200, ""},
		{"session-only anonymous", "GET", "/api/v1/_t/session", nil, nil, 401, "unauthorized"},
		{"session-only session", "GET", "/api/v1/_t/session", nil, []ReqOpt{As(owner)}, 200, ""},
		{"session-only write token", "GET", "/api/v1/_t/session", nil, []ReqOpt{Bearer(write)}, 403, "session_required"},
		{"session-only read token", "GET", "/api/v1/_t/session", nil, []ReqOpt{Bearer(read)}, 403, "session_required"},
		{"write read token", "POST", "/api/v1/_t/write", body, []ReqOpt{Bearer(read)}, 403, "insufficient_scope"},
		{"write write token", "POST", "/api/v1/_t/write", body, []ReqOpt{Bearer(write)}, 200, ""},
		{"write session", "POST", "/api/v1/_t/write", body, []ReqOpt{As(owner)}, 200, ""},
		{"session-only beats insufficient_scope", "POST", "/api/v1/_t/session-write", body, []ReqOpt{Bearer(read)}, 403, "session_required"},
		// Auth runs before the body is looked at: bad body + bad credentials is 401.
		{"401 before 415", "POST", "/api/v1/_t/write", nil, []ReqOpt{RawBody("x", "text/plain")}, 401, "unauthorized"},
		{"403 scope before 415", "POST", "/api/v1/_t/write", nil, []ReqOpt{Bearer(read), RawBody("x", "text/plain")}, 403, "insufficient_scope"},
		{"415 before decode", "POST", "/api/v1/_t/write", nil, []ReqOpt{As(owner), RawBody(`{"name":"x"}`, "text/plain")}, 415, "unsupported_media_type"},
		{"415 even without a decoding handler", "POST", "/api/v1/_t/nobody", nil, []ReqOpt{As(owner), RawBody("junk", "text/plain")}, 415, "unsupported_media_type"},
		{"no body no content type is fine", "POST", "/api/v1/_t/nobody", nil, []ReqOpt{As(owner)}, 204, ""},
		{"decode runs after auth", "POST", "/api/v1/_t/write", nil, []ReqOpt{As(owner), RawBody(`{"name":`, "application/json")}, 400, "bad_request"},
		{"unset access fails closed (session only)", "GET", "/api/v1/_t/unset", nil, []ReqOpt{Bearer(write)}, 403, "session_required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := h.Do(t, tc.method, tc.path, tc.body, tc.opts...)
			if r.Code != tc.code || r.ErrCode(t) != tc.errc {
				t.Fatalf("got %d %q (%s), want %d %q", r.Code, r.ErrCode(t), r.Body, tc.code, tc.errc)
			}
		})
	}
}

func TestGuardAuthErrors(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	owner := h.Signup(t, "o@x.io")

	// A bad bearer: 401 with WWW-Authenticate, the valid cookie is neither used nor cleared.
	r := h.Do(t, "GET", "/api/v1/_t/authed", nil, As(owner), WithHeader("Authorization", "Bearer pb_nope"))
	if r.Code != 401 || r.Header.Get("WWW-Authenticate") != "Bearer" || len(r.Cookies()) != 0 {
		t.Fatalf("%d %v %v", r.Code, r.Header, r.Cookies())
	}
	// An unknown cookie: 401 and the cookie is cleared.
	c := h.NewClient()
	c.SetCookie(auth.SessionCookieName, strings.Repeat("a", 43))
	r = h.Do(t, "GET", "/api/v1/_t/authed", nil, As(c))
	if r.Code != 401 || r.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("%d %v", r.Code, r.Header)
	}
	cs := r.Cookies()
	if len(cs) != 1 || cs[0].Name != auth.SessionCookieName || cs[0].MaxAge >= 0 {
		t.Fatalf("cookie not cleared: %v", cs)
	}
	// Same message whatever the reason.
	anon := h.Do(t, "GET", "/api/v1/_t/authed", nil)
	if string(anon.Body) != string(r.Body) {
		t.Fatalf("bodies differ: %s vs %s", anon.Body, r.Body)
	}
}

func TestGuardAppliesSlidingCookie(t *testing.T) {
	h := Setup(t, Opts{CookieSecure: true})
	h.testRoutes()
	owner := h.Signup(t, "o@x.io")
	if r := h.Do(t, "GET", "/api/v1/_t/authed", nil, As(owner)); len(r.Cookies()) != 0 {
		t.Fatal("fresh session must not re-issue the cookie")
	}
	h.Env.Clock.Advance(auth.SlideAfter + 1)
	r := h.Do(t, "GET", "/api/v1/_t/authed", nil, As(owner))
	cs := r.Cookies()
	if r.Code != 200 || len(cs) != 1 || !cs[0].HttpOnly || !cs[0].Secure || cs[0].MaxAge != 2592000 {
		t.Fatalf("slide cookie not applied: %d %+v", r.Code, cs)
	}
}

func TestRoutingJSON404And405(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	owner := h.Signup(t, "o@x.io")

	for _, p := range []string{"/api/v1/nope", "/api/v2/x", "/api/", "/api/v1/_t"} {
		r := h.Do(t, "GET", p, nil, As(owner))
		if r.Code != 404 || r.ErrCode(t) != "not_found" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %s", p, r.Code, r.Body)
		}
	}
	// Wrong method on a known path: 405 with Allow.
	r := h.Do(t, "GET", "/api/v1/_t/write", nil, As(owner))
	if r.Code != 405 || r.ErrCode(t) != "method_not_allowed" || r.Header.Get("Allow") != "POST" {
		t.Fatalf("%d %q %s", r.Code, r.Header.Get("Allow"), r.Body)
	}
	r = h.Do(t, "PUT", "/api/v1/_t/authed", map[string]any{}, As(owner))
	if r.Code != 405 || r.Header.Get("Allow") != "GET, HEAD" {
		t.Fatalf("PUT: %d %q", r.Code, r.Header.Get("Allow"))
	}
	// OPTIONS is not handled: JSON 405 on a known path, no CORS headers anywhere.
	r = h.Do(t, "OPTIONS", "/api/v1/_t/authed", nil, As(owner))
	if r.Code != 405 || r.ErrCode(t) != "method_not_allowed" {
		t.Fatalf("OPTIONS: %d %s", r.Code, r.Body)
	}
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Fatalf("CORS header %s", k)
		}
	}
	// HEAD maps to the GET route.
	if r := h.Do(t, "HEAD", "/api/v1/_t/authed", nil, As(owner)); r.Code != 200 {
		t.Fatalf("HEAD: %d", r.Code)
	}
	// The 404/405 catch-all needs no credentials.
	if r := h.Do(t, "GET", "/api/v1/zzz", nil); r.Code != 404 {
		t.Fatalf("anonymous unknown: %d", r.Code)
	}
}

func TestMuxHasNoConflicts(t *testing.T) {
	h := Setup(t, Opts{})
	// Shapes the real handlers will register must coexist with the catch-all and the fallback
	// (a conflict panics at registration).
	for _, p := range []string{
		"POST /api/v1/tickets/{id}/move",
		"POST /api/v1/projects/{id}/members",
		"PATCH /api/v1/projects/{id}/members/{userId}",
		"DELETE /api/v1/projects/{id}/members/{userId}",
		"DELETE /api/v1/tokens/{id}",
		"GET /api/v1/projects/{id}/tickets",
	} {
		h.Server.route(p, Authed, false, func(w http.ResponseWriter, r *http.Request) { noContent(w) })
	}
	h.Server.Mount("/", Public, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.Server.MountRaw("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.Server.Mount("GET /api/v1/projects/{id}/events", SessionOnly, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
}

func TestFallbackAndWellKnown(t *testing.T) {
	h := Setup(t, Opts{})
	// Default: plain 404 (not JSON).
	r := h.Do(t, "GET", "/some/page", nil)
	if r.Code != 404 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("default fallback: %d %q", r.Code, r.Header.Get("Content-Type"))
	}
	var seen []string
	h.Server.SetFallback(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte("spa"))
	}))
	if r := h.Do(t, "GET", "/board/WEB", nil); r.Code != 200 || string(r.Body) != "spa" {
		t.Fatalf("fallback: %d %s", r.Code, r.Body)
	}
	// Every method reaches it (it answers 405 itself); it is inside the global middleware.
	r = h.Do(t, "POST", "/x", nil, NoOrigin())
	if string(r.Body) != "spa" || r.Header.Get("Content-Security-Policy") == "" || r.Header.Get("X-Request-Id") == "" {
		t.Fatalf("fallback POST: %d %v", r.Code, r.Header)
	}
	// Never reached for /api/, /mcp (unregistered here), /healthz, nor for .well-known.
	h.Do(t, "GET", "/api/v1/x", nil)
	h.Do(t, "GET", "/healthz", nil)
	r = h.Do(t, "GET", "/.well-known/security.txt", nil)
	if r.Code != 404 || r.ErrCode(t) != "not_found" {
		t.Fatalf("well-known: %d %s", r.Code, r.Body)
	}
	if len(seen) != 2 {
		t.Fatalf("fallback saw %v", seen)
	}
	// Mount("/") replaces the fallback.
	h.Server.Mount("/", Public, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("two")) }))
	if r := h.Do(t, "GET", "/y", nil); string(r.Body) != "two" {
		t.Fatalf("Mount(/): %s", r.Body)
	}
}

func TestMountedRouteUsesGuardAndOrigin(t *testing.T) {
	h := Setup(t, Opts{})
	owner := h.Signup(t, "o@x.io")
	secret, _ := h.MkToken(t, owner, service.ScopeWrite, "")
	h.Server.Mount("POST /api/v1/_t/mounted", SessionOnly, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { noContent(w) }))
	var found bool
	for _, rt := range h.Server.Routes() {
		found = found || (rt.Pattern == "POST /api/v1/_t/mounted" && rt.Access == SessionOnly)
	}
	if !found {
		t.Fatal("Mount route missing from Routes()")
	}
	cases := []struct {
		opts []ReqOpt
		code int
	}{
		{nil, 401},
		{[]ReqOpt{Bearer(secret)}, 403},
		{[]ReqOpt{As(owner)}, 204},
		{[]ReqOpt{As(owner), WithOrigin("http://evil.example")}, 403},
	}
	for i, c := range cases {
		if r := h.Do(t, "POST", "/api/v1/_t/mounted", nil, c.opts...); r.Code != c.code {
			t.Errorf("case %d: %d %s", i, r.Code, r.Body)
		}
	}
}

func TestMountRawMCP(t *testing.T) {
	h := Setup(t, Opts{})
	owner := h.Signup(t, "o@x.io")
	secret, _ := h.MkToken(t, owner, service.ScopeWrite, "")
	h.Server.MountRaw("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authenticates itself, ignoring cookies (what phase 4 does).
		p, err := h.Server.resolver.ResolveBearer(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			WriteError(w, 401, CodeUnauthorized, "Authentication required")
			return
		}
		writeJSON(w, 200, map[string]string{"method": p.Method})
	}))
	for _, m := range []string{"GET", "POST", "DELETE"} {
		// Foreign Origin: 403 on every method; matching or absent passes.
		if r := h.Do(t, m, "/mcp", nil, Bearer(secret), WithOrigin("http://evil.example")); r.Code != 403 || r.ErrCode(t) != "origin_mismatch" {
			t.Errorf("%s foreign origin: %d %s", m, r.Code, r.Body)
		}
		if r := h.Do(t, m, "/mcp", nil, Bearer(secret)); r.Code != 200 {
			t.Errorf("%s matching origin: %d %s", m, r.Code, r.Body)
		}
		if r := h.Do(t, m, "/mcp", nil, Bearer(secret), NoOrigin()); r.Code != 200 {
			t.Errorf("%s absent origin: %d %s", m, r.Code, r.Body)
		}
	}
	// Cookies are ignored: a cookie-only request is 401.
	if r := h.Do(t, "POST", "/mcp", nil, As(owner)); r.Code != 401 {
		t.Errorf("cookie-only: %d", r.Code)
	}
	// Global middleware applies (headers, no-store, request id) but there is no guard entry.
	r := h.Do(t, "POST", "/mcp", nil, Bearer(secret))
	if r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("Content-Security-Policy") != CSP || r.Header.Get("X-Request-Id") == "" {
		t.Fatalf("headers: %v", r.Header)
	}
	for _, rt := range h.Server.Routes() {
		if strings.Contains(rt.Pattern, "/mcp") {
			t.Fatalf("MountRaw must not appear in Routes(): %q", rt.Pattern)
		}
	}
	// Body cap applies to /mcp too.
	big := strings.Repeat("a", MaxBodyBytes+1)
	if r := h.Do(t, "POST", "/mcp", nil, Bearer(secret), RawBody(big, "application/json")); r.Code != 400 || r.ErrCode(t) != "body_too_large" {
		t.Fatalf("mcp body cap: %d %s", r.Code, r.Body)
	}
}

func TestHealthz(t *testing.T) {
	h := Setup(t, Opts{})
	r := h.Do(t, "GET", "/healthz", nil, NoOrigin())
	if r.Code != 200 || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", r.Code, r.Header)
	}
	if m := r.JSON(t); len(m) != 1 || m["status"] != "ok" {
		t.Fatalf("%v", m)
	}
	// Database failure: 503 with a generic body.
	if err := h.Env.Store.Close(); err != nil {
		t.Fatal(err)
	}
	r = h.Do(t, "GET", "/healthz", nil, NoOrigin())
	if r.Code != 503 || r.ErrCode(t) != "internal" || !strings.Contains(string(r.Body), "Database unavailable") {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if strings.Contains(strings.ToLower(string(r.Body)), "sql") {
		t.Fatalf("driver detail leaked: %s", r.Body)
	}
}

func TestPrincipalActorFromGuard(t *testing.T) {
	h := Setup(t, Opts{})
	owner := h.Signup(t, "o@x.io")
	secret, tokID := h.MkToken(t, owner, service.ScopeRead, "")
	var got service.Actor
	h.Server.route("GET /api/v1/_t/actor", Authed, false, func(w http.ResponseWriter, r *http.Request) {
		got = principal(r).Actor()
		noContent(w)
	})
	h.Do(t, "GET", "/api/v1/_t/actor", nil, Bearer(secret))
	if got.Type != service.ActorAPIToken || got.ID != tokID || got.UserID != owner.User.ID || got.Scope != service.ScopeRead {
		t.Fatalf("%+v", got)
	}
	h.Do(t, "GET", "/api/v1/_t/actor", nil, As(owner))
	if got.Type != service.ActorUser || got.UserID != owner.User.ID {
		t.Fatalf("%+v", got)
	}
}

func TestNewRequiresDeps(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	New(Deps{})
}

func TestStartBackgroundStops(t *testing.T) {
	h := Setup(t, Opts{})
	ctx, cancel := context.WithCancel(context.Background())
	done := h.Server.StartBackground(ctx)
	cancel()
	<-done
}
