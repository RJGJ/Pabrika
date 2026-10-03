package httpapi

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
)

// Phase 6 security audit tests (docs/security-audit.md). They only cover what the Phase 2 to 4
// tests do not: the full header set over the real SPA fallback, secrets at rest, password
// redaction through the login endpoint, body caps on real routes and field limits at the
// boundary. Everything else is cited in the audit table as existing evidence.

func TestAuditHeaderTable(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	h.Server.SetFallback(NewSPAHandler(spaFS()))
	// Exhaust the login limiter for the 429 case.
	bad := map[string]any{"email": "nobody@x.io", "password": "wrong password!"}
	for i := 0; i < 5; i++ {
		h.Do(t, "POST", "/api/v1/auth/login", bad)
	}
	owner := h.Signup(t, "o@x.io")

	cases := []struct {
		name, method, path string
		opts               []ReqOpt
		code               int
		noStore            bool
	}{
		{"root", "GET", "/", nil, 200, false},
		{"board", "GET", "/p/WEB", nil, 200, false},
		{"me 401", "GET", "/api/v1/auth/me", nil, 401, true},
		{"auth config", "GET", "/api/v1/auth/config", nil, 200, true},
		{"healthz", "GET", "/healthz", nil, 200, true},
		{"well-known 404", "GET", "/.well-known/x", nil, 404, false},
		{"asset", "GET", "/assets/app-abc.js", nil, 200, false},
		{"asset missing", "GET", "/assets/missing.js", nil, 404, false},
		{"POST / 405", "POST", "/", []ReqOpt{As(owner)}, 405, false},
		{"429", "POST", "/api/v1/auth/login", []ReqOpt{}, 429, true},
		{"500", "GET", "/api/v1/_t/panic", nil, 500, true},
		{"OPTIONS preflight", "OPTIONS", "/api/v1/projects", []ReqOpt{WithHeader("Origin", "https://evil.example"),
			WithHeader("Access-Control-Request-Method", "POST")}, 405, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body any
			if c.code == 429 {
				body = bad
			}
			r := h.Do(t, c.method, c.path, body, c.opts...)
			if r.Code != c.code {
				t.Fatalf("status %d want %d (%s)", r.Code, c.code, r.Body)
			}
			// Verbatim values (wantHeaders is the literal Phase 2 section 11 text) and the shared constants.
			for k, v := range wantHeaders {
				if got := r.Header.Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			if r.Header.Get("Content-Security-Policy") != CSP || r.Header.Get("Permissions-Policy") != PermissionsPolicy ||
				r.Header.Get("Referrer-Policy") != ReferrerPolicy {
				t.Error("headers differ from the middleware constants")
			}
			if strings.Contains(r.Header.Get("Content-Security-Policy"), "unsafe-eval") ||
				strings.Contains(r.Header.Get("Content-Security-Policy"), "script-src 'self' 'unsafe-inline'") {
				t.Error("CSP allows script unsafe-inline or unsafe-eval")
			}
			if len(r.Header.Values("Strict-Transport-Security")) != 0 {
				t.Error("HSTS on an http BASE_URL")
			}
			if got := r.Header.Get("Cache-Control") == "no-store"; got != c.noStore {
				t.Errorf("Cache-Control = %q, want no-store=%v", r.Header.Get("Cache-Control"), c.noStore)
			}
			for k := range r.Header {
				if strings.HasPrefix(strings.ToLower(k), "access-control-") {
					t.Errorf("CORS header %s", k)
				}
			}
			if r.Header.Get("X-Request-Id") == "" {
				t.Error("no X-Request-Id")
			}
		})
	}
}

func TestAuditHSTSExactlyOnceOverHTTPS(t *testing.T) {
	h := Setup(t, Opts{BaseURL: "https://pabrika.example.com"})
	h.Server.SetFallback(NewSPAHandler(spaFS()))
	for _, p := range []string{"/", "/healthz", "/api/v1/nope", "/assets/missing.js", "/.well-known/x"} {
		r := h.Do(t, "GET", p, nil)
		vals := r.Header.Values("Strict-Transport-Security")
		if len(vals) != 1 || vals[0] != "max-age=31536000" {
			t.Errorf("%s: HSTS = %q", p, vals)
		}
		if strings.Contains(vals[0], "includeSubDomains") {
			t.Errorf("%s: includeSubDomains", p)
		}
	}
}

// TestAuditCORSAbsent: a cross-origin GET is answered without any Access-Control header.
func TestAuditCORSAbsent(t *testing.T) {
	h := Setup(t, Opts{})
	r := h.Do(t, "GET", "/api/v1/auth/config", nil, WithHeader("Origin", "https://evil.example"))
	if r.Code != 200 {
		t.Fatalf("%d", r.Code)
	}
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("CORS header %s", k)
		}
	}
	r = h.Do(t, "OPTIONS", "/api/v1/projects", nil, WithHeader("Origin", "https://evil.example"))
	if r.Code != 405 && r.Code != 404 {
		t.Fatalf("OPTIONS: %d", r.Code)
	}
	if r.ErrCode(t) == "" {
		t.Fatalf("OPTIONS body is not the JSON error shape: %s", r.Body)
	}
}

// secretHits counts the cells of every table that contain secret as a substring.
func secretHits(t *testing.T, h *Harness, secret string) int {
	t.Helper()
	ctx := context.Background()
	var tables string
	if err := h.Env.Store.RawQueryRow(ctx, "SELECT group_concat(name) FROM sqlite_master WHERE type='table'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, tb := range strings.Split(tables, ",") {
		var cols string
		if err := h.Env.Store.RawQueryRow(ctx, "SELECT group_concat(name) FROM pragma_table_info('"+tb+"')").Scan(&cols); err != nil {
			t.Fatal(err)
		}
		for _, col := range strings.Split(cols, ",") {
			var n int
			q := `SELECT count(*) FROM "` + tb + `" WHERE instr(CAST("` + col + `" AS TEXT), ?) > 0`
			if err := h.Env.Store.RawQueryRow(ctx, q, secret).Scan(&n); err != nil {
				t.Fatal(err)
			}
			total += n
		}
	}
	return total
}

func TestAuditSecretsNeverStoredRaw(t *testing.T) {
	h := Setup(t, Opts{})
	h.NewUser(t, "a@x.io", "Alice")
	// A real login: the raw cookie value must not be in any column.
	r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword})
	if r.Code != 200 {
		t.Fatalf("login: %d %s", r.Code, r.Body)
	}
	cookie := sessionCookie(t, r)
	if len(cookie.Value) < 20 {
		t.Fatalf("cookie value %q", cookie.Value)
	}
	if n := secretHits(t, h, cookie.Value); n != 0 {
		t.Errorf("raw session cookie found in %d cells", n)
	}
	// Control: the scan does find a value that is stored (the email), so it is not vacuous.
	if n := secretHits(t, h, "a@x.io"); n == 0 {
		t.Fatal("scan found nothing for a stored value")
	}

	// API token: created through the endpoint, shown once, only the hash stored.
	c := h.NewClient()
	c.User = service.User{}
	c.SetCookie(cookie.Name, cookie.Value)
	tr := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "ci", "scope": "write"}, As(c))
	if tr.Code != 201 {
		t.Fatalf("create token: %d %s", tr.Code, tr.Body)
	}
	secret, _ := tr.JSON(t)["secret"].(string)
	if !strings.HasPrefix(secret, "pb_") || len(secret) < 20 {
		t.Fatalf("token secret %q in %s", secret, tr.Body)
	}
	if n := secretHits(t, h, secret); n != 0 {
		t.Errorf("raw API token found in %d cells", n)
	}
	list := h.Do(t, "GET", "/api/v1/tokens", nil, As(c))
	if list.Code != 200 || strings.Contains(string(list.Body), secret) {
		t.Errorf("token list leaks the secret or fails: %d %s", list.Code, list.Body)
	}
	// The password is stored only as an argon2id PHC string.
	if n := secretHits(t, h, TestPassword); n != 0 {
		t.Errorf("plain password found in %d cells", n)
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM users WHERE password_hash LIKE '$argon2id$%'"); n != 1 {
		t.Errorf("argon2id PHC rows = %d", n)
	}
}

func TestAuditLoginLogsNeverContainPasswordOrCookie(t *testing.T) {
	h := Setup(t, Opts{})
	h.testRoutes()
	h.NewUser(t, "a@x.io", "Alice")
	const wrong = "wrong-password-canary-9Zq"
	h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": wrong})
	r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword})
	cookie := sessionCookie(t, r)
	h.Do(t, "GET", "/api/v1/auth/me", nil, WithHeader("Cookie", cookie.Name+"="+cookie.Value))
	h.Do(t, "GET", "/api/v1/_t/panic", nil, WithHeader("Authorization", "Bearer pb_canarytoken"))
	h.Do(t, "POST", "/api/v1/auth/signup", map[string]any{"email": "b@x.io", "display_name": "B", "password": "signup-password-canary"})
	logs := h.Logs.String()
	if !strings.Contains(logs, "path=/api/v1/auth/login") {
		t.Fatalf("login requests were not logged:\n%s", logs)
	}
	for _, forbidden := range []string{wrong, TestPassword, "signup-password-canary", cookie.Value, "pb_canarytoken", "a@x.io", "b@x.io"} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains %q", forbidden)
		}
	}
}

func TestAuditBodyCapOnRealRoutes(t *testing.T) {
	h := Setup(t, Opts{})
	owner := h.Signup(t, "o@x.io")
	proj := h.Env.NewProject(t, h.Env.NewUser(t, "p@x.io", "P"), "AUD")
	_ = proj
	big := `{"title":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	pr := h.Do(t, "POST", "/api/v1/projects", map[string]any{"key": "BIG", "name": "Big"}, As(owner))
	if pr.Code != 201 {
		t.Fatalf("project: %d %s", pr.Code, pr.Body)
	}
	for _, c := range []struct {
		name, path string
		opts       []ReqOpt
	}{
		{"tickets", "/api/v1/projects/BIG/tickets", []ReqOpt{As(owner)}},
		{"login", "/api/v1/auth/login", nil},
		{"signup", "/api/v1/auth/signup", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			opts := append([]ReqOpt{RawBody(big, "application/json")}, c.opts...)
			r := h.Do(t, "POST", c.path, nil, opts...)
			if r.Code != 400 || r.ErrCode(t) != "body_too_large" {
				t.Fatalf("%d %s", r.Code, r.Body)
			}
		})
	}
	// Chunked (unknown length) bodies are capped too: no Content-Length to reject up front.
	req := httptest.NewRequest("POST", "/api/v1/auth/login", io.NopCloser(strings.NewReader(big)))
	req.ContentLength = -1
	req.Header.Set("Origin", h.Cfg.BaseURL)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Server.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "body_too_large") {
		t.Fatalf("chunked: %d %s", rec.Code, rec.Body)
	}
}

func TestAuditFieldLimits(t *testing.T) {
	h := Setup(t, Opts{})
	owner := h.Signup(t, "o@x.io")
	if r := h.Do(t, "POST", "/api/v1/projects", map[string]any{"key": "LIM", "name": "Limits"}, As(owner)); r.Code != 201 {
		t.Fatalf("project: %d %s", r.Code, r.Body)
	}
	multi := func(n int) string { return strings.Repeat("é", n) } // 2 bytes per character
	a := func(n int) string { return strings.Repeat("a", n) }
	i := 0
	nextKey := func() string { i++; return string(rune('A'+i/26)) + string(rune('A'+i%26)) + "X" }

	cases := []struct {
		name, method, path string
		body               func(ok bool) map[string]any
		okCode             int
		field              string
	}{
		{"project name", "POST", "/api/v1/projects", func(ok bool) map[string]any {
			n := 101
			if ok {
				n = 100
			}
			return map[string]any{"key": nextKey(), "name": multi(n)}
		}, 201, "name"},
		{"project description", "POST", "/api/v1/projects", func(ok bool) map[string]any {
			n := 2001
			if ok {
				n = 2000
			}
			return map[string]any{"key": nextKey(), "name": "N", "description": multi(n)}
		}, 201, "description"},
		{"project key too long", "POST", "/api/v1/projects", func(ok bool) map[string]any {
			if ok {
				return map[string]any{"key": "ABCDEF", "name": "N"}
			}
			return map[string]any{"key": "ABCDEFG", "name": "N"}
		}, 201, "key"},
		{"project key lowercase digits", "POST", "/api/v1/projects", func(ok bool) map[string]any {
			if ok {
				return map[string]any{"key": "zq", "name": "N"} // lowercased input is uppercased
			}
			return map[string]any{"key": "Z1", "name": "N"}
		}, 201, "key"},
		{"ticket title", "POST", "/api/v1/projects/LIM/tickets", func(ok bool) map[string]any {
			n := 201
			if ok {
				n = 200
			}
			return map[string]any{"title": multi(n)}
		}, 201, "title"},
		{"ticket description", "POST", "/api/v1/projects/LIM/tickets", func(ok bool) map[string]any {
			n := 20001
			if ok {
				n = 20000
			}
			return map[string]any{"title": "t", "description": multi(n)}
		}, 201, "description"},
		{"label name", "POST", "/api/v1/projects/LIM/labels", func(ok bool) map[string]any {
			n := 51
			if ok {
				n = 50
			}
			return map[string]any{"name": multi(n), "color": "red"}
		}, 201, "name"},
		{"token name", "POST", "/api/v1/tokens", func(ok bool) map[string]any {
			n := 101
			if ok {
				n = 100
			}
			return map[string]any{"name": multi(n), "scope": "read"}
		}, 201, "name"},
		{"signup display name 100", "POST", "/api/v1/auth/signup", func(ok bool) map[string]any {
			n := 101
			if ok {
				n = 100
			}
			return map[string]any{"email": a(i+5) + "@x.io", "display_name": multi(n), "password": TestPassword}
		}, 201, "display_name"},
		{"signup display name empty", "POST", "/api/v1/auth/signup", func(ok bool) map[string]any {
			name := ""
			if ok {
				name = "x"
			}
			return map[string]any{"email": a(i+9) + "@x.io", "display_name": name, "password": TestPassword}
		}, 201, "display_name"},
		{"signup password", "POST", "/api/v1/auth/signup", func(ok bool) map[string]any {
			n := 201
			if ok {
				n = 200
			}
			return map[string]any{"email": a(i+13) + "@x.io", "display_name": "D", "password": a(n)}
		}, 201, "password"},
		{"signup email", "POST", "/api/v1/auth/signup", func(ok bool) map[string]any {
			local := a(64)
			domain := strings.Repeat("d", 63) + "." + strings.Repeat("d", 63) + "." + strings.Repeat("d", 57) + ".com"
			addr := local + "@" + domain // 64 + 1 + 189 = 254
			if len(addr) != 254 {
				t.Fatalf("fixture length %d", len(addr))
			}
			if !ok {
				addr = "a" + addr // 255
			}
			return map[string]any{"email": addr, "display_name": "D", "password": TestPassword}
		}, 201, "email"},
		{"comment body", "POST", "", nil, 201, "body"}, // filled below once a ticket exists
	}
	tk := h.Do(t, "POST", "/api/v1/projects/LIM/tickets", map[string]any{"title": "for comments"}, As(owner))
	if tk.Code != 201 {
		t.Fatalf("ticket: %d %s", tk.Code, tk.Body)
	}
	tid, _ := tk.JSON(t)["id"].(string)
	last := &cases[len(cases)-1]
	last.path = "/api/v1/tickets/" + tid + "/comments"
	last.body = func(ok bool) map[string]any {
		n := 20001
		if ok {
			n = 20000
		}
		return map[string]any{"body": multi(n)}
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Signup limiter would trip on repeated attempts from one IP.
			h.Env.Clock.Advance(2 * time.Minute)
			opts := []ReqOpt{As(owner)}
			if strings.Contains(c.path, "/auth/") {
				opts = nil
			}
			okResp := h.Do(t, c.method, c.path, c.body(true), opts...)
			if okResp.Code != c.okCode {
				t.Fatalf("at limit: %d %s", okResp.Code, okResp.Body)
			}
			h.Env.Clock.Advance(2 * time.Minute)
			bad := h.Do(t, c.method, c.path, c.body(false), opts...)
			if bad.Code != 422 || bad.ErrCode(t) != "validation_failed" {
				t.Fatalf("over limit: %d %s", bad.Code, bad.Body)
			}
			fields, _ := bad.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
			if _, ok := fields[c.field]; !ok {
				t.Errorf("fields %v lack %q", fields, c.field)
			}
		})
	}
}
