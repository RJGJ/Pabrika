package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

var wantHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'",
	"X-Content-Type-Options":  "nosniff",
	"Referrer-Policy":         "same-origin",
	"X-Frame-Options":         "DENY",
	"Permissions-Policy":      "camera=(), microphone=(), geolocation=()",
}

func TestCSPConstIsExact(t *testing.T) {
	if CSP != wantHeaders["Content-Security-Policy"] {
		t.Fatalf("CSP drifted:\n%s", CSP)
	}
}

func TestSecurityHeadersAndRequestIDOnEveryResponse(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	owner := h.Signup(t, "o@x.io")
	cases := []struct {
		name, method, path string
		opts               []ReqOpt
		code               int
	}{
		{"healthz", "GET", "/healthz", nil, 200},
		{"api 404", "GET", "/api/v1/nope", nil, 404},
		{"api 405", "DELETE", "/api/v1/_t/authed", []ReqOpt{As(owner)}, 405},
		{"401", "GET", "/api/v1/_t/authed", nil, 401},
		{"403 origin", "POST", "/api/v1/_t/write", []ReqOpt{As(owner), WithOrigin("http://evil.example")}, 403},
		{"415", "POST", "/api/v1/_t/write", []ReqOpt{As(owner), RawBody("x", "text/plain")}, 415},
		{"fallback 404", "GET", "/", nil, 404},
		{"panic 500", "GET", "/api/v1/_t/panic", nil, 500},
		{"body too large", "POST", "/api/v1/_t/write", []ReqOpt{As(owner), RawBody(strings.Repeat("a", MaxBodyBytes+1), "application/json")}, 400},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := h.Do(t, c.method, c.path, nil, c.opts...)
			if r.Code != c.code {
				t.Fatalf("status %d want %d (%s)", r.Code, c.code, r.Body)
			}
			for k, v := range wantHeaders {
				if got := r.Header.Get(k); got != v {
					t.Errorf("%s = %q", k, got)
				}
			}
			id := r.Header.Get("X-Request-Id")
			if len(id) != 26 || seen[id] {
				t.Errorf("request id %q (dup=%v)", id, seen[id])
			}
			seen[id] = true
			if r.Header.Get("Strict-Transport-Security") != "" {
				t.Error("HSTS on an http BASE_URL")
			}
			noStore := r.Header.Get("Cache-Control") == "no-store"
			wantNoStore := strings.HasPrefix(c.path, "/api/") || c.path == "/healthz"
			if noStore != wantNoStore {
				t.Errorf("Cache-Control %q for %s", r.Header.Get("Cache-Control"), c.path)
			}
			for k := range r.Header {
				if strings.HasPrefix(strings.ToLower(k), "access-control-") {
					t.Errorf("CORS header %s", k)
				}
			}
		})
	}
}

func TestHSTSOnlyForHTTPSBaseURL(t *testing.T) {
	h := Setup(t, Opts{BaseURL: "https://pabrika.example.com"})
	r := h.Do(t, "GET", "/healthz", nil)
	if r.Header.Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Fatalf("HSTS = %q", r.Header.Get("Strict-Transport-Security"))
	}
}

func TestPanicIsGenericAndLogged(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	r := h.Do(t, "GET", "/api/v1/_t/panic", nil, WithHeader("X-Probe", "header-secret-value"))
	if r.Code != 500 || r.ErrCode(t) != "internal" || strings.Contains(string(r.Body), "kaboom") {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	logs := h.Logs.String()
	if !strings.Contains(logs, "kaboom") || !strings.Contains(logs, "stack=") || !strings.Contains(logs, "status=500") {
		t.Fatalf("panic not logged with stack and status:\n%s", logs)
	}
	if strings.Contains(logs, "header-secret-value") {
		t.Fatal("request headers must never be logged")
	}
	// The server keeps serving afterwards.
	if r := h.Do(t, "GET", "/healthz", nil); r.Code != 200 {
		t.Fatal(r.Code)
	}
}

func TestOriginMatrix(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		method  string
		path    string
		origin  *string // nil = absent
		auth    bool    // Authorization header present
		cookie  bool
		want    int // 403 = origin_mismatch, 401 = passed the Origin check (route needs auth)
	}{}
	str := func(s string) *string { return &s }
	add := func(name, base, method, path string, origin *string, auth, cookie bool, want int) {
		cases = append(cases, struct {
			name, baseURL, method, path string
			origin                      *string
			auth, cookie                bool
			want                        int
		}{name, base, method, path, origin, auth, cookie, want})
	}
	const b = "http://localhost:8080"
	for _, m := range []string{"POST", "PATCH", "DELETE"} {
		add(m+" matching", b, m, "/api/v1/x", str(b), false, false, 404)
		add(m+" wrong host", b, m, "/api/v1/x", str("http://evil.example"), false, false, 403)
		add(m+" wrong port", b, m, "/api/v1/x", str("http://localhost:9090"), false, false, 403)
		add(m+" wrong scheme", b, m, "/api/v1/x", str("https://localhost:8080"), false, false, 403)
		add(m+" null origin", b, m, "/api/v1/x", str("null"), false, false, 403)
		add(m+" origin with path", b, m, "/api/v1/x", str(b+"/foo"), false, false, 403)
		add(m+" no origin, no auth", b, m, "/api/v1/x", nil, false, false, 403)
		add(m+" no origin, cookie only", b, m, "/api/v1/x", nil, false, true, 403)
		add(m+" no origin, bearer", b, m, "/api/v1/x", nil, true, false, 404)
		add(m+" bad origin with bearer is still checked", b, m, "/api/v1/x", str("http://evil.example"), true, false, 403)
	}
	add("GET foreign origin is not checked", b, "GET", "/api/v1/x", str("http://evil.example"), false, false, 404)
	add("GET no origin", b, "GET", "/api/v1/x", nil, false, false, 404)
	add("case-insensitive scheme and host", "http://LocalHost:8080", "POST", "/api/v1/x", str("HTTP://LOCALHOST:8080"), false, false, 404)
	add("default port https", "https://x.example", "POST", "/api/v1/x", str("https://x.example:443"), false, false, 404)
	add("default port https reverse", "https://x.example:443", "POST", "/api/v1/x", str("https://x.example"), false, false, 404)
	add("default port http", "http://x.example:80", "POST", "/api/v1/x", str("http://x.example"), false, false, 404)
	add("https vs http same host", "https://x.example", "POST", "/api/v1/x", str("http://x.example"), false, false, 403)
	add("ipv6", "http://[::1]:8080", "POST", "/api/v1/x", str("http://[::1]:8080"), false, false, 404)
	add("outside /api not checked", b, "POST", "/other", str("http://evil.example"), false, false, 404)
	add("PUT is checked too", b, "PUT", "/api/v1/x", str("http://evil.example"), false, false, 403)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := Setup(t, Opts{BaseURL: tc.baseURL})
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.origin != nil {
				req.Header.Set("Origin", *tc.origin)
			}
			if tc.auth {
				req.Header.Set("Authorization", "Bearer pb_x")
			}
			if tc.cookie {
				req.AddCookie(&http.Cookie{Name: "pb_session", Value: "x"})
			}
			rec := httptest.NewRecorder()
			h.Server.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("got %d (%s) want %d", rec.Code, rec.Body, tc.want)
			}
			if tc.want == 403 && !strings.Contains(rec.Body.String(), "origin_mismatch") {
				t.Fatalf("code: %s", rec.Body)
			}
		})
	}
}

func TestOriginMultipleHeadersRejected(t *testing.T) {
	h := Setup(t, Opts{})
	req := httptest.NewRequest("POST", "/api/v1/x", nil)
	req.Header.Add("Origin", h.Cfg.BaseURL)
	req.Header.Add("Origin", h.Cfg.BaseURL)
	rec := httptest.NewRecorder()
	h.Server.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
}

func TestOriginRunsBeforeRoutingAndAuth(t *testing.T) {
	h := Setup(t, Opts{})
	// Unknown route, no credentials, bad origin: origin_mismatch wins.
	if r := h.Do(t, "POST", "/api/v1/does-not-exist", nil, WithOrigin("http://evil.example")); r.Code != 403 || r.ErrCode(t) != "origin_mismatch" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

func TestBodyCapContentLengthRejectedUpFront(t *testing.T) {
	h := Setup(t, Opts{})
	called := false
	h.Server.route("POST /api/v1/_t/never", Public, false, func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest("POST", "/api/v1/_t/never", strings.NewReader("x"))
	req.ContentLength = MaxBodyBytes + 1
	req.Header.Set("Origin", h.Cfg.BaseURL)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Server.Handler().ServeHTTP(rec, req)
	if called || rec.Code != 400 || !strings.Contains(rec.Body.String(), "body_too_large") {
		t.Fatalf("called=%v %d %s", called, rec.Code, rec.Body)
	}
}

func TestCacheControlSetBeforeHandlerAndOverridable(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	var before string
	h.Server.route("GET /api/v1/_t/cc", Public, false, func(w http.ResponseWriter, r *http.Request) {
		before = w.Header().Get("Cache-Control")
		noContent(w)
	})
	h.Do(t, "GET", "/api/v1/_t/cc", nil)
	if before != "no-store" {
		t.Fatalf("Cache-Control before handler = %q", before)
	}
	// A streaming handler overrides it.
	r := h.Do(t, "GET", "/api/v1/_t/flush", nil)
	if r.Header.Get("Cache-Control") != "no-store, no-transform" {
		t.Fatalf("override lost: %q", r.Header.Get("Cache-Control"))
	}
}

func TestFlushAndUnwrapThroughTheChain(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	// httptest.ResponseRecorder implements Flusher; the handler panics if Flush fails through
	// the wrappers, and the recorder records that it was flushed.
	req := httptest.NewRequest("GET", "/api/v1/_t/flush", nil)
	rec := httptest.NewRecorder()
	h.Server.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !rec.Flushed || !strings.Contains(rec.Body.String(), "event: hello") {
		t.Fatalf("code=%d flushed=%v body=%q", rec.Code, rec.Flushed, rec.Body)
	}
	// Unwrap reaches the underlying writer.
	inner := httptest.NewRecorder()
	rw := &respWriter{ResponseWriter: inner}
	if rw.Unwrap() != http.ResponseWriter(inner) {
		t.Fatal("Unwrap")
	}
	if err := http.NewResponseController(rw).Flush(); err != nil || !inner.Flushed {
		t.Fatalf("ResponseController flush: %v flushed=%v", err, inner.Flushed)
	}
	if rw.statusCode() != 200 {
		t.Fatal("flush implies a 200 header")
	}
}

func TestRequestLogFieldsAndRedaction(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	owner := h.Signup(t, "o@x.io")
	secret, _ := h.MkToken(t, owner, service.ScopeWrite, "")

	h.Do(t, "GET", "/api/v1/_t/authed?x=pb_querysecret&y=hunter2", nil, Bearer(secret),
		WithHeader("Cookie", "pb_session=cookie-secret-value"))
	h.Do(t, "GET", "/api/v1/_t/authed", nil, As(owner))
	h.Do(t, "POST", "/api/v1/_t/write", map[string]any{"name": "body-secret-name"}, Bearer(secret))
	logs := h.Logs.String()

	for _, forbidden := range []string{"pb_querysecret", "hunter2", "cookie-secret-value", secret, "Bearer", "Authorization",
		"Set-Cookie", "body-secret-name", "o@x.io", owner.Cookie("pb_session").Value} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains %q:\n%s", forbidden, logs)
		}
	}
	for _, want := range []string{"method=GET", "path=/api/v1/_t/authed", "status=200", "user_id=" + owner.User.ID,
		`route="GET /api/v1/_t/authed"`, "request_id=", "duration_ms=", "ip=192.0.2.1"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}
}

func TestRequestLogClientIPFollowsTrustProxy(t *testing.T) {
	for _, trust := range []bool{false, true} {
		h := Setup(t, Opts{TrustProxy: trust})
		h.Do(t, "GET", "/healthz", nil, WithHeader("X-Forwarded-For", "198.51.100.7, 10.0.0.1"))
		logs := h.Logs.String()
		if trust != strings.Contains(logs, "ip=198.51.100.7") {
			t.Errorf("trust=%v:\n%s", trust, logs)
		}
		if strings.Contains(logs, "10.0.0.1") {
			t.Errorf("only the first hop may be used:\n%s", logs)
		}
	}
}
