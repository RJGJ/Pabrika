package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func countRows(t *testing.T, h *Harness, query string, args ...any) int {
	t.Helper()
	var n int
	if err := h.Env.Store.RawQueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sessionCookie(t *testing.T, r *Resp) *http.Cookie {
	t.Helper()
	for _, c := range r.Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in response (%d %s)", auth.SessionCookieName, r.Code, r.Body)
	return nil
}

func signupBody(email string) map[string]any {
	return map[string]any{"email": email, "display_name": "Alice", "password": TestPassword}
}

func TestAuthConfig(t *testing.T) {
	for _, off := range []bool{false, true} {
		h := Setup(t, Opts{SignupOff: off})
		r := h.Do(t, "GET", "/api/v1/auth/config", nil)
		m := r.JSON(t)
		wantKeys(t, "config", m, "signup_enabled")
		if r.Code != 200 || m["signup_enabled"] != !off {
			t.Fatalf("off=%v: %d %v", off, r.Code, m)
		}
	}
}

func TestSignupHappyPathAndCookie(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.NewClient()
	r := h.Do(t, "POST", "/api/v1/auth/signup", map[string]any{"email": " Alice@X.io ", "display_name": " Alice ", "password": TestPassword}, As(c))
	if r.Code != 201 {
		t.Fatalf("signup: %d %s", r.Code, r.Body)
	}
	m := r.JSON(t)
	wantKeys(t, "signup", m, "user")
	u := m["user"].(map[string]any)
	wantKeys(t, "signup.user", u, "id", "email", "display_name", "created_at")
	if u["email"] != "alice@x.io" || u["display_name"] != "Alice" {
		t.Fatalf("user: %v", u)
	}
	ck := sessionCookie(t, r)
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.MaxAge != 2592000 || ck.Secure {
		t.Fatalf("cookie attributes: %+v", ck)
	}
	// The cookie works, and the DB holds only its hash.
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(c)); r.Code != 200 {
		t.Fatalf("me: %d", r.Code)
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM sessions WHERE token_hash = '"+ck.Value+"'"); n != 0 {
		t.Fatal("raw session value stored")
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM sessions WHERE token_hash = '"+auth.HashSessionValue(ck.Value)+"'"); n != 1 {
		t.Fatal("hashed session missing")
	}
	// Password is stored as an argon2id hash, never plain.
	if n := countRows(t, h, "SELECT COUNT(*) FROM users WHERE password_hash LIKE '$argon2id$%'"); n != 1 {
		t.Fatal("password not hashed with argon2id")
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM users WHERE password_hash LIKE '%"+TestPassword+"%'"); n != 0 {
		t.Fatal("plaintext password stored")
	}
}

func TestCookieSecureToggle(t *testing.T) {
	h := Setup(t, Opts{CookieSecure: true})
	r := h.Do(t, "POST", "/api/v1/auth/signup", signupBody("a@x.io"))
	if ck := sessionCookie(t, r); !ck.Secure || !ck.HttpOnly {
		t.Fatalf("want Secure cookie: %+v", ck)
	}
	c := h.NewClient()
	r = h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword}, As(c))
	if ck := sessionCookie(t, r); !ck.Secure {
		t.Fatalf("login cookie not Secure: %+v", ck)
	}
	r = h.Do(t, "POST", "/api/v1/auth/logout", nil, As(c))
	if ck := sessionCookie(t, r); !ck.Secure || !ck.HttpOnly || ck.MaxAge >= 0 || ck.Path != "/" {
		t.Fatalf("logout cookie: %+v", ck)
	}
}

func TestSignupValidation(t *testing.T) {
	h := Setup(t, Opts{})
	r := h.Do(t, "POST", "/api/v1/auth/signup", map[string]any{"email": "nope", "display_name": "", "password": "short"})
	if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	f := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
	wantKeys(t, "fields", f, "email", "display_name", "password")
	if n := countRows(t, h, "SELECT COUNT(*) FROM users"); n != 0 {
		t.Fatal("user created despite validation failure")
	}
	// Display name 101 chars.
	r = h.Do(t, "POST", "/api/v1/auth/signup", map[string]any{"email": "a@x.io", "display_name": strings.Repeat("x", 101), "password": TestPassword})
	if r.Code != 422 {
		t.Fatalf("long name: %d", r.Code)
	}
	// Bad bodies.
	for _, raw := range []string{`{"email":"a@x.io","extra":1}`, `[]`, `{"email":"a@x.io"} {}`} {
		if r := h.Do(t, "POST", "/api/v1/auth/signup", nil, RawBody(raw, "application/json")); r.Code != 400 {
			t.Fatalf("%q: %d", raw, r.Code)
		}
	}
	if r := h.Do(t, "POST", "/api/v1/auth/signup", nil, RawBody(`{}`, "text/plain")); r.Code != 415 {
		t.Fatalf("415: %d", r.Code)
	}
}

func TestSignupDuplicateEmail(t *testing.T) {
	h := Setup(t, Opts{})
	h.NewUser(t, "a@x.io", "A")
	r := h.Do(t, "POST", "/api/v1/auth/signup", signupBody("A@X.io"))
	if r.Code != 409 || r.ErrCode(t) != "email_taken" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

func TestSignupOff404ByteIdentical(t *testing.T) {
	h := Setup(t, Opts{SignupOff: true})
	unknown := h.Do(t, "POST", "/api/v1/auth/zzz", signupBody("a@x.io"))
	for _, body := range []any{signupBody("a@x.io"), nil, "{garbage", map[string]any{"bogus": 1}} {
		r := h.Do(t, "POST", "/api/v1/auth/signup", body)
		if r.Code != 404 || string(r.Body) != string(unknown.Body) ||
			r.Header.Get("Content-Type") != unknown.Header.Get("Content-Type") ||
			r.Header.Get("Cache-Control") != unknown.Header.Get("Cache-Control") {
			t.Fatalf("not identical: %d %q vs %d %q", r.Code, r.Body, unknown.Code, unknown.Body)
		}
		if len(r.Cookies()) != 0 {
			t.Fatal("cookie set")
		}
	}
	// The limiter was never touched: more than 5 attempts all stay 404.
	for i := 0; i < 8; i++ {
		if r := h.Do(t, "POST", "/api/v1/auth/signup", signupBody("a@x.io")); r.Code != 404 {
			t.Fatalf("attempt %d: %d", i, r.Code)
		}
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM users"); n != 0 {
		t.Fatal("user created")
	}
	// Login still works when signup is off (users made by CLI).
	h.NewUser(t, "a@x.io", "A")
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword}); r.Code != 200 {
		t.Fatalf("login: %d", r.Code)
	}
}

func TestLogin(t *testing.T) {
	h := Setup(t, Opts{})
	h.NewUser(t, "a@x.io", "Alice")
	c := h.NewClient()
	r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": " A@X.io ", "password": TestPassword}, As(c))
	if r.Code != 200 {
		t.Fatalf("login: %d %s", r.Code, r.Body)
	}
	wantKeys(t, "login", r.JSON(t), "user")
	wantKeys(t, "login.user", r.JSON(t)["user"].(map[string]any), "id", "email", "display_name", "created_at")
	ck := sessionCookie(t, r)
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 2592000 || ck.Path != "/" {
		t.Fatalf("cookie: %+v", ck)
	}
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(c)); r.Code != 200 {
		t.Fatalf("me after login: %d", r.Code)
	}
}

func TestLoginGenericFailures(t *testing.T) {
	h := Setup(t, Opts{})
	h.NewUser(t, "a@x.io", "Alice")
	cases := []map[string]any{
		{"email": "a@x.io", "password": "wrong password!"},
		{"email": "nobody@x.io", "password": TestPassword},
		{"email": "", "password": TestPassword},
		{"email": "a@x.io", "password": ""},
		{"email": "", "password": ""},
		{},
		{"email": "a@x.io", "password": strings.Repeat("p", 500)},
	}
	var first string
	for i, body := range cases {
		h.Env.Clock.Advance(2 * time.Minute) // keep clear of the rate limit
		r := h.Do(t, "POST", "/api/v1/auth/login", body)
		if r.Code != 401 || r.ErrCode(t) != "invalid_credentials" {
			t.Fatalf("case %d %v: %d %s", i, body, r.Code, r.Body)
		}
		if i == 0 {
			first = string(r.Body)
		} else if string(r.Body) != first {
			t.Fatalf("case %d body differs: %s vs %s", i, r.Body, first)
		}
		if len(r.Cookies()) != 0 {
			t.Fatalf("case %d set a cookie", i)
		}
	}
	if !strings.Contains(first, "Invalid email or password") {
		t.Fatal(first)
	}
	// Garbage body is still a 400 (not an auth failure shape).
	if r := h.Do(t, "POST", "/api/v1/auth/login", nil, RawBody("{nope", "application/json")); r.Code != 400 {
		t.Fatalf("garbage: %d", r.Code)
	}
}

func TestLoginRotatesSession(t *testing.T) {
	h := Setup(t, Opts{})
	u := h.NewUser(t, "a@x.io", "Alice")
	c := h.Login(t, u) // existing session in this browser
	old := c.Cookie(auth.SessionCookieName).Value
	// A second browser stays logged in.
	other := h.Login(t, u)
	r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword}, As(c))
	if r.Code != 200 {
		t.Fatalf("%d", r.Code)
	}
	fresh := sessionCookie(t, r).Value
	if fresh == old {
		t.Fatal("session value reused")
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM sessions WHERE token_hash = '"+auth.HashSessionValue(old)+"'"); n != 0 {
		t.Fatal("old session row not deleted")
	}
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(other)); r.Code != 200 {
		t.Fatal("other browser's session was killed")
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM sessions"); n != 2 {
		t.Fatalf("sessions = %d, want 2", n)
	}
}

func TestSessionFixationClientValueIgnored(t *testing.T) {
	h := Setup(t, Opts{})
	h.NewUser(t, "a@x.io", "Alice")
	c := h.NewClient()
	attacker := strings.Repeat("A", 43) // a well-formed value chosen by the client
	c.SetCookie(auth.SessionCookieName, attacker)
	r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword}, As(c))
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if v := sessionCookie(t, r).Value; v == attacker {
		t.Fatal("client-chosen session value accepted")
	}
	// The planted value is not a session.
	probe := h.NewClient()
	probe.SetCookie(auth.SessionCookieName, attacker)
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(probe)); r.Code != 401 {
		t.Fatalf("planted value works: %d", r.Code)
	}
	// Same on signup.
	h2 := Setup(t, Opts{})
	c2 := h2.NewClient()
	c2.SetCookie(auth.SessionCookieName, attacker)
	r = h2.Do(t, "POST", "/api/v1/auth/signup", signupBody("b@x.io"), As(c2))
	if r.Code != 201 || sessionCookie(t, r).Value == attacker {
		t.Fatalf("signup fixation: %d", r.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	h := Setup(t, Opts{})
	h.NewUser(t, "a@x.io", "Alice")
	bad := map[string]any{"email": "a@x.io", "password": "wrong password!"}
	for i := 0; i < 5; i++ {
		if r := h.Do(t, "POST", "/api/v1/auth/login", bad); r.Code != 401 {
			t.Fatalf("attempt %d: %d", i, r.Code)
		}
	}
	r := h.Do(t, "POST", "/api/v1/auth/login", bad)
	if r.Code != 429 || r.ErrCode(t) != "rate_limited" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if ra := r.Header.Get("Retry-After"); ra != "60" {
		t.Fatalf("Retry-After = %q", ra)
	}
	// Even the correct password is blocked within the window.
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword}); r.Code != 429 {
		t.Fatalf("correct password while limited: %d", r.Code)
	}
	// A different email and a different IP have their own buckets.
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "b@x.io", "password": "x"}); r.Code != 401 {
		t.Fatalf("other email: %d", r.Code)
	}
	if r := h.Do(t, "POST", "/api/v1/auth/login", bad, RemoteAddr("203.0.113.9:4000")); r.Code != 401 {
		t.Fatalf("other ip: %d", r.Code)
	}
	// Retry-After shrinks with time, and the window resets.
	h.Env.Clock.Advance(20 * time.Second)
	if r := h.Do(t, "POST", "/api/v1/auth/login", bad); r.Code != 429 || r.Header.Get("Retry-After") != "40" {
		t.Fatalf("%d RA=%q", r.Code, r.Header.Get("Retry-After"))
	}
	h.Env.Clock.Advance(41 * time.Second)
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword}); r.Code != 200 {
		t.Fatalf("after window: %d", r.Code)
	}
}

func TestLoginRateLimitKeyCaseAndBadBodies(t *testing.T) {
	h := Setup(t, Opts{})
	// Case and whitespace variants share one bucket.
	for i, e := range []string{"a@x.io", "A@X.IO", " a@x.io ", "a@x.io", "A@x.io"} {
		if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": e, "password": "x"}); r.Code != 401 {
			t.Fatalf("%d: %d", i, r.Code)
		}
	}
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@X.io", "password": "x"}); r.Code != 429 {
		t.Fatalf("variants not sharing a bucket: %d", r.Code)
	}
	// Garbage bodies count against the empty-email bucket and fail 400 until it is full.
	for i := 0; i < 5; i++ {
		if r := h.Do(t, "POST", "/api/v1/auth/login", nil, RawBody("{garbage", "application/json")); r.Code != 400 {
			t.Fatalf("garbage %d: %d", i, r.Code)
		}
	}
	if r := h.Do(t, "POST", "/api/v1/auth/login", nil, RawBody("{garbage", "application/json")); r.Code != 429 {
		t.Fatalf("garbage not counted: %d", r.Code)
	}
	// Empty-email bodies share that bucket.
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "", "password": "x"}); r.Code != 429 {
		t.Fatalf("empty email: %d", r.Code)
	}
}

func TestLoginRateLimitTrustProxy(t *testing.T) {
	h := Setup(t, Opts{TrustProxy: true})
	bad := map[string]any{"email": "a@x.io", "password": "x"}
	for i := 0; i < 5; i++ {
		h.Do(t, "POST", "/api/v1/auth/login", bad, WithHeader("X-Forwarded-For", "198.51.100.1"))
	}
	if r := h.Do(t, "POST", "/api/v1/auth/login", bad, WithHeader("X-Forwarded-For", "198.51.100.1")); r.Code != 429 {
		t.Fatalf("%d", r.Code)
	}
	if r := h.Do(t, "POST", "/api/v1/auth/login", bad, WithHeader("X-Forwarded-For", "198.51.100.2")); r.Code != 401 {
		t.Fatalf("other forwarded ip: %d", r.Code)
	}
	// Without TRUST_PROXY the header is ignored.
	h2 := Setup(t, Opts{})
	for i := 0; i < 5; i++ {
		h2.Do(t, "POST", "/api/v1/auth/login", bad, WithHeader("X-Forwarded-For", "198.51.100.1"))
	}
	if r := h2.Do(t, "POST", "/api/v1/auth/login", bad, WithHeader("X-Forwarded-For", "198.51.100.9")); r.Code != 429 {
		t.Fatalf("header not ignored: %d", r.Code)
	}
}

func TestSignupRateLimit(t *testing.T) {
	h := Setup(t, Opts{})
	bad := map[string]any{"email": "a@x.io", "display_name": "", "password": "x"}
	for i := 0; i < 5; i++ {
		if r := h.Do(t, "POST", "/api/v1/auth/signup", bad); r.Code != 422 {
			t.Fatalf("%d: %d", i, r.Code)
		}
	}
	r := h.Do(t, "POST", "/api/v1/auth/signup", signupBody("a@x.io"))
	if r.Code != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("%d", r.Code)
	}
	// Separate bucket from login.
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": "x"}); r.Code != 401 {
		t.Fatalf("login shares the signup bucket: %d", r.Code)
	}
}

func TestOriginRequiredOnAuthPosts(t *testing.T) {
	h := Setup(t, Opts{})
	h.NewUser(t, "a@x.io", "A")
	r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword}, NoOrigin())
	if r.Code != 403 || r.ErrCode(t) != "origin_mismatch" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

func TestLogout(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	value := c.Cookie(auth.SessionCookieName).Value
	r := h.Do(t, "POST", "/api/v1/auth/logout", nil, As(c))
	if r.Code != 204 || len(r.Body) != 0 {
		t.Fatalf("%d %q", r.Code, r.Body)
	}
	ck := sessionCookie(t, r)
	if ck.Value != "" || ck.MaxAge >= 0 || ck.Path != "/" || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("clear cookie: %+v", ck)
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM sessions WHERE token_hash = '"+auth.HashSessionValue(value)+"'"); n != 0 {
		t.Fatal("session row remains")
	}
	// Replaying the old cookie is 401 and clears it again.
	replay := h.NewClient()
	replay.SetCookie(auth.SessionCookieName, value)
	r = h.Do(t, "POST", "/api/v1/auth/logout", nil, As(replay))
	if r.Code != 401 || r.ErrCode(t) != "unauthorized" || sessionCookie(t, r).MaxAge >= 0 {
		t.Fatalf("replay: %d %s", r.Code, r.Body)
	}
	// Anonymous.
	if r := h.Do(t, "POST", "/api/v1/auth/logout", nil); r.Code != 401 {
		t.Fatalf("anon: %d", r.Code)
	}
}

func TestLogoutTokenForbidden(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	secret, _ := h.MkToken(t, c, "write", "")
	r := h.Do(t, "POST", "/api/v1/auth/logout", nil, Bearer(secret))
	if r.Code != 403 || r.ErrCode(t) != "session_required" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

func TestMe(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(c))
	m := r.JSON(t)
	wantKeys(t, "me", m, "user", "auth")
	wantKeys(t, "me.user", m["user"].(map[string]any), "id", "email", "display_name", "created_at")
	a := m["auth"].(map[string]any)
	wantKeys(t, "me.auth(session)", a, "method")
	if a["method"] != "session" {
		t.Fatal(a)
	}

	// Token, unlimited.
	secret, id := h.MkToken(t, c, "read", "")
	a = h.Do(t, "GET", "/api/v1/auth/me", nil, Bearer(secret)).JSON(t)["auth"].(map[string]any)
	wantKeys(t, "me.auth(token)", a, "method", "token")
	tk := a["token"].(map[string]any)
	wantKeys(t, "me.auth.token", tk, "id", "name", "scope", "project_id", "project_key")
	if a["method"] != "token" || tk["id"] != id || tk["scope"] != "read" || tk["project_id"] != nil || tk["project_key"] != nil {
		t.Fatalf("%v", a)
	}

	// Token limited to a project.
	p := h.Env.NewProject(t, testutil.User{ID: c.User.ID, Email: c.User.Email, DisplayName: c.User.DisplayName}, "WEB")
	secret2, _ := h.MkToken(t, c, "write", p.ID)
	tk = h.Do(t, "GET", "/api/v1/auth/me", nil, Bearer(secret2)).JSON(t)["auth"].(map[string]any)["token"].(map[string]any)
	if tk["project_id"] != p.ID || tk["project_key"] != "WEB" || tk["scope"] != "write" {
		t.Fatalf("%v", tk)
	}

	// Anonymous and unknown cookie.
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil); r.Code != 401 || r.ErrCode(t) != "unauthorized" {
		t.Fatalf("anon: %d", r.Code)
	}
	bad := h.NewClient()
	bad.SetCookie(auth.SessionCookieName, strings.Repeat("B", 43))
	r = h.Do(t, "GET", "/api/v1/auth/me", nil, As(bad))
	if r.Code != 401 || sessionCookie(t, r).MaxAge >= 0 {
		t.Fatalf("unknown cookie: %d", r.Code)
	}
}

func TestUpdateMe(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	r := h.Do(t, "PATCH", "/api/v1/auth/me", map[string]any{"display_name": "  New Name "}, As(c))
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	u := r.JSON(t)["user"].(map[string]any)
	wantKeys(t, "patch.user", u, "id", "email", "display_name", "created_at")
	if u["display_name"] != "New Name" || u["email"] != "a@x.io" {
		t.Fatalf("%v", u)
	}
	got := h.Do(t, "GET", "/api/v1/auth/me", nil, As(c)).JSON(t)["user"].(map[string]any)
	if got["display_name"] != "New Name" {
		t.Fatalf("%v", got)
	}
	// 1..100 characters.
	if r := h.Do(t, "PATCH", "/api/v1/auth/me", map[string]any{"display_name": strings.Repeat("é", 100)}, As(c)); r.Code != 200 {
		t.Fatalf("100: %d", r.Code)
	}
	for _, name := range []string{"", "   ", strings.Repeat("x", 101)} {
		r := h.Do(t, "PATCH", "/api/v1/auth/me", map[string]any{"display_name": name}, As(c))
		if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
			t.Fatalf("%q: %d %s", name, r.Code, r.Body)
		}
		f := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
		wantKeys(t, "fields", f, "display_name")
	}
	// Email cannot be changed; unknown field is 400. Missing body is 400.
	if r := h.Do(t, "PATCH", "/api/v1/auth/me", map[string]any{"email": "b@x.io"}, As(c)); r.Code != 400 {
		t.Fatalf("email: %d", r.Code)
	}
	if r := h.Do(t, "PATCH", "/api/v1/auth/me", nil, As(c)); r.Code != 400 {
		t.Fatalf("no body: %d", r.Code)
	}
	// Anonymous and token.
	if r := h.Do(t, "PATCH", "/api/v1/auth/me", map[string]any{"display_name": "x"}); r.Code != 401 {
		t.Fatalf("anon: %d", r.Code)
	}
	secret, _ := h.MkToken(t, c, "write", "")
	r = h.Do(t, "PATCH", "/api/v1/auth/me", map[string]any{"display_name": "x"}, Bearer(secret))
	if r.Code != 403 || r.ErrCode(t) != "session_required" {
		t.Fatalf("token: %d %s", r.Code, r.Body)
	}
}

func TestChangePassword(t *testing.T) {
	h := Setup(t, Opts{})
	u := h.NewUser(t, "a@x.io", "Alice")
	c1 := h.Login(t, u)
	c2 := h.Login(t, u)
	const newPW = "a brand new password"
	r := h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": TestPassword, "new_password": newPW}, As(c1))
	if r.Code != 204 || len(r.Body) != 0 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	// Current session kept, other deleted.
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(c1)); r.Code != 200 {
		t.Fatalf("current session lost: %d", r.Code)
	}
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(c2)); r.Code != 401 {
		t.Fatalf("other session survived: %d", r.Code)
	}
	if n := countRows(t, h, "SELECT COUNT(*) FROM sessions"); n != 1 {
		t.Fatalf("sessions = %d", n)
	}
	// New password logs in, old one does not.
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": newPW}); r.Code != 200 {
		t.Fatalf("new pw: %d", r.Code)
	}
	if r := h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": TestPassword}); r.Code != 401 {
		t.Fatalf("old pw: %d", r.Code)
	}
}

func TestChangePasswordKeepsAPITokens(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	secret, _ := h.MkToken(t, c, "write", "")
	r := h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": TestPassword, "new_password": "another long one"}, As(c))
	if r.Code != 204 {
		t.Fatalf("%d", r.Code)
	}
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, Bearer(secret)); r.Code != 200 {
		t.Fatalf("token revoked by password change: %d", r.Code)
	}
}

func TestChangePasswordErrors(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	fieldsOf := func(r *Resp) map[string]any {
		return r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
	}
	// Wrong current: 422 (never 401), only current_password, and nothing changes.
	r := h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": "nope nope nope", "new_password": "valid new password"}, As(c))
	if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	f := fieldsOf(r)
	wantKeys(t, "fields", f, "current_password")
	if f["current_password"] != "Incorrect password" {
		t.Fatal(f)
	}
	// Wrong current wins over a bad new password.
	r = h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": "", "new_password": "x"}, As(c))
	wantKeys(t, "fields", fieldsOf(r), "current_password")
	// Valid current, bad new.
	for _, pw := range []string{"short", strings.Repeat("p", 201)} {
		r = h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": TestPassword, "new_password": pw}, As(c))
		if r.Code != 422 {
			t.Fatalf("%d", r.Code)
		}
		wantKeys(t, "fields", fieldsOf(r), "new_password")
	}
	// Session still valid after the failures.
	if r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(c)); r.Code != 200 {
		t.Fatal("session lost")
	}
	// Same password as current is allowed.
	r = h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": TestPassword, "new_password": TestPassword}, As(c))
	if r.Code != 204 {
		t.Fatalf("same pw: %d %s", r.Code, r.Body)
	}
	h.Env.Clock.Advance(2 * time.Minute) // fresh rate-limit window
	// Unknown field and missing body: 400.
	if r := h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": TestPassword, "new_password": "valid new password", "x": 1}, As(c)); r.Code != 400 {
		t.Fatalf("unknown field: %d", r.Code)
	}
	if r := h.Do(t, "POST", "/api/v1/auth/me/password", nil, As(c)); r.Code != 400 {
		t.Fatalf("no body: %d", r.Code)
	}
	// Anonymous and token.
	body := map[string]any{"current_password": TestPassword, "new_password": "valid new password"}
	if r := h.Do(t, "POST", "/api/v1/auth/me/password", body); r.Code != 401 {
		t.Fatalf("anon: %d", r.Code)
	}
	secret, _ := h.MkToken(t, c, "write", "")
	r = h.Do(t, "POST", "/api/v1/auth/me/password", body, Bearer(secret))
	if r.Code != 403 || r.ErrCode(t) != "session_required" {
		t.Fatalf("token: %d %s", r.Code, r.Body)
	}
}

func TestChangePasswordRateLimit(t *testing.T) {
	h := Setup(t, Opts{})
	u := h.NewUser(t, "a@x.io", "A")
	c := h.Login(t, u)
	other := h.Login(t, h.NewUser(t, "b@x.io", "B"))
	bad := map[string]any{"current_password": "wrong wrong wrong", "new_password": "valid new password"}
	for i := 0; i < 5; i++ {
		if r := h.Do(t, "POST", "/api/v1/auth/me/password", bad, As(c)); r.Code != 422 {
			t.Fatalf("%d: %d", i, r.Code)
		}
	}
	r := h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": TestPassword, "new_password": "valid new password"}, As(c))
	if r.Code != 429 || r.ErrCode(t) != "rate_limited" || r.Header.Get("Retry-After") != "60" {
		t.Fatalf("%d %s RA=%q", r.Code, r.Body, r.Header.Get("Retry-After"))
	}
	// Per (user, IP): another user and another IP are unaffected.
	if r := h.Do(t, "POST", "/api/v1/auth/me/password", bad, As(other)); r.Code != 422 {
		t.Fatalf("other user: %d", r.Code)
	}
	if r := h.Do(t, "POST", "/api/v1/auth/me/password", bad, As(c), RemoteAddr("203.0.113.9:1")); r.Code != 422 {
		t.Fatalf("other ip: %d", r.Code)
	}
	h.Env.Clock.Advance(61 * time.Second)
	r = h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": TestPassword, "new_password": "valid new password"}, As(c))
	if r.Code != 204 {
		t.Fatalf("after window: %d", r.Code)
	}
}

func TestSessionSlidesAndReissuesCookie(t *testing.T) {
	h := Setup(t, Opts{})
	c := h.Signup(t, "a@x.io")
	h.Env.Clock.Advance(2 * time.Hour)
	r := h.Do(t, "GET", "/api/v1/auth/me", nil, As(c))
	if r.Code != 200 || sessionCookie(t, r).MaxAge != 2592000 {
		t.Fatalf("no re-issued cookie: %d", r.Code)
	}
}
