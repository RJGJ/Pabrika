package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

// WP8 track 4: hardening tests that go through the whole HTTP chain (the unit tables for
// ClientIP, the limiter and decodeJSON live next to their code).

var addrSeq atomic.Int64

// freshAddr returns a peer address nobody has used, so the rate limiter never interferes.
func freshAddr() ReqOpt {
	n := addrSeq.Add(1)
	return RemoteAddr(fmt.Sprintf("10.%d.%d.%d:4000", (n>>16)&255, (n>>8)&255, n&255))
}

func xffLines(lines ...string) ReqOpt {
	return func(r *reqSpec) {
		if r.headers == nil {
			r.headers = http.Header{}
		}
		r.headers["X-Forwarded-For"] = lines
	}
}

// ---- TRUST_PROXY: which requests share a rate-limit bucket ----

func TestTrustProxyBucketTable(t *testing.T) {
	const peer = "192.0.2.1:4000"
	type req = []ReqOpt
	cases := []struct {
		name  string
		trust bool
		a, b  req
		same  bool
	}{
		{"off: X-Forwarded-For is ignored (spoofing does not evade)", false, req{xffLines("1.1.1.1")}, req{xffLines("2.2.2.2")}, true},
		{"off: different peers are different buckets", false, req{RemoteAddr("203.0.113.1:1")}, req{RemoteAddr("203.0.113.2:1")}, false},
		{"off: the peer port does not matter", false, req{RemoteAddr("203.0.113.1:1")}, req{RemoteAddr("203.0.113.1:999")}, true},
		{"off: IPv4 and IPv4-mapped IPv6 are one client", false, req{RemoteAddr("203.0.113.9:1")}, req{RemoteAddr("[::ffff:203.0.113.9]:1")}, true},
		{"off: IPv6 in one /64 share a bucket", false, req{RemoteAddr("[2001:db8:1:2::1]:1")}, req{RemoteAddr("[2001:db8:1:2:ffff::9]:1")}, true},
		{"off: IPv6 in different /64s do not", false, req{RemoteAddr("[2001:db8:1:2::1]:1")}, req{RemoteAddr("[2001:db8:1:3::1]:1")}, false},
		{"on: first hop decides", true, req{xffLines("1.1.1.1")}, req{xffLines("2.2.2.2")}, false},
		{"on: only the first hop of a list counts", true, req{xffLines("1.1.1.1, 9.9.9.9")}, req{xffLines("1.1.1.1, 8.8.8.8")}, true},
		{"on: only the first header line counts", true, req{xffLines("1.1.1.1", "2.2.2.2")}, req{xffLines("1.1.1.1", "3.3.3.3")}, true},
		{"on: a later line cannot replace the first", true, req{xffLines("1.1.1.1", "2.2.2.2")}, req{xffLines("2.2.2.2")}, false},
		{"on: a port on the hop is stripped", true, req{xffLines("1.1.1.1")}, req{xffLines("1.1.1.1:5555")}, true},
		{"on: IPv4-mapped IPv6 hop equals the IPv4", true, req{xffLines("::ffff:198.51.100.7")}, req{xffLines("198.51.100.7")}, true},
		{"on: IPv6 hops in one /64 share a bucket", true, req{xffLines("2001:db8:1:2::1")}, req{xffLines("2001:db8:1:2:ffff::9")}, true},
		{"on: IPv6 hops in different /64s do not", true, req{xffLines("2001:db8:1:2::1")}, req{xffLines("2001:db8:1:3::1")}, false},
		{"on: an invalid header falls back to the peer", true, req{xffLines("garbage")}, req{}, true},
		{"on: an empty first hop falls back to the peer", true, req{xffLines(", 1.1.1.1")}, req{}, true},
		{"on: an invalid header and a valid one differ", true, req{xffLines("garbage")}, req{xffLines("1.1.1.1")}, false},
		{"on: the peer is irrelevant when the header is valid", true, req{RemoteAddr("203.0.113.1:1"), xffLines("1.1.1.1")}, req{RemoteAddr("203.0.113.2:1"), xffLines("1.1.1.1")}, true},
	}
	bad := map[string]any{"email": "a@x.io", "password": "wrong password!"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := Setup(t, Opts{TrustProxy: c.trust})
			login := func(o req) *Resp {
				return h.Do(t, "POST", "/api/v1/auth/login", bad, append(req{RemoteAddr(peer)}, o...)...)
			}
			for i := 0; i < 5; i++ {
				if r := login(c.a); r.Code != 401 {
					t.Fatalf("attempt %d: %d %s", i+1, r.Code, r.Body)
				}
			}
			if r := login(c.a); r.Code != 429 {
				t.Fatalf("6th attempt: %d", r.Code)
			}
			r := login(c.b)
			if c.same && r.Code != 429 {
				t.Errorf("want the same bucket (429), got %d", r.Code)
			}
			if !c.same && r.Code != 401 {
				t.Errorf("want a separate bucket (401), got %d", r.Code)
			}
		})
	}
}

// ---- log redaction ----

// TestLogsNeverContainSecrets drives real signup, login, password change, token creation and
// token use, with secrets in the query string, headers and bodies (valid, malformed and
// oversized), and checks that nothing secret reaches the log.
func TestLogsNeverContainSecrets(t *testing.T) {
	h := Setup(t, Opts{})
	const (
		email  = "leaky.person@example.com"
		pw1    = "Initial-Passw0rd-xyz"
		pw2    = "Changed-Passw0rd-xyz"
		qsec   = "query-secret-value-42"
		hdrsec = "header-secret-value-43"
	)
	c := h.NewClient()
	r := h.Do(t, "POST", "/api/v1/auth/signup?password="+pw1+"&q="+qsec, map[string]any{"email": email, "display_name": "Leaky", "password": pw1}, As(c))
	if r.Code != 201 {
		t.Fatalf("signup: %d %s", r.Code, r.Body)
	}
	cookie := c.Cookie("pb_session").Value
	// failed and successful logins, a malformed body that still contains a password
	h.Do(t, "POST", "/api/v1/auth/login?email="+email, map[string]any{"email": email, "password": "wrong-Passw0rd-abc"})
	h.Do(t, "POST", "/api/v1/auth/login", nil, RawBody(`{"email":"`+email+`","password":"`+pw1+`"`, "application/json"))
	h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": email, "password": pw1}, As(h.NewClient()))
	if r := h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": pw1, "new_password": pw2}, As(c)); r.Code != 204 {
		t.Fatalf("change password: %d %s", r.Code, r.Body)
	}
	h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": "nope-Passw0rd-abc", "new_password": "short"}, As(c))
	tr := h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": "ci", "scope": "write"}, As(c))
	if tr.Code != 201 {
		t.Fatalf("token: %d %s", tr.Code, tr.Body)
	}
	secret := tr.JSON(t)["secret"].(string)
	h.Do(t, "POST", "/api/v1/projects?access_token="+secret+"&x="+qsec, map[string]any{"key": "WEB", "name": "Web"}, Bearer(secret),
		WithHeader("X-Probe", hdrsec), WithHeader("Cookie", "pb_session="+cookie))
	h.Do(t, "GET", "/api/v1/projects?token="+secret, nil, As(c), WithHeader("X-Probe", hdrsec))
	h.Do(t, "GET", "/api/v1/auth/me", nil, WithHeader("Authorization", "Bearer pb_"+strings.Repeat("a", 64)))
	h.Do(t, "POST", "/api/v1/projects", nil, Bearer(secret), RawBody(`{"name":"`+pw2+strings.Repeat("a", MaxBodyBytes)+`"}`, "application/json"))
	h.Do(t, "POST", "/api/v1/projects", nil, Bearer(secret), RawBody(`password=`+pw2, "application/x-www-form-urlencoded"))

	logs := h.Logs.String()
	if !strings.Contains(logs, "request_id=") {
		t.Fatalf("expected request log lines, got:\n%s", logs)
	}
	for _, secretValue := range []string{pw1, pw2, "wrong-Passw0rd-abc", "nope-Passw0rd-abc", qsec, hdrsec, secret, secret[3:],
		cookie, email, "leaky.person", "Authorization", "Bearer", "Cookie", "Set-Cookie", "access_token"} {
		if strings.Contains(logs, secretValue) {
			t.Errorf("log contains %q", secretValue)
		}
	}
}

// ---- headers on every response ----

// TestHSTSAndHeadersOnEveryResponse: HSTS follows the BASE_URL scheme on every kind of response
// (and is exactly "max-age=31536000"), and every response carries a unique X-Request-Id plus the
// fixed security headers: API routes with and without credentials, unknown routes, wrong methods,
// the fallback, /healthz and a raw-mounted /mcp.
func TestHSTSAndHeadersOnEveryResponse(t *testing.T) {
	for _, baseURL := range []string{"http://localhost:8080", "https://pabrika.example.com", "http://pabrika.example.com", "https://pabrika.example.com:8443"} {
		t.Run(baseURL, func(t *testing.T) {
			h := Setup(t, Opts{BaseURL: baseURL})
			h.Server.MountRaw("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				WriteError(w, 401, "unauthorized", "Authentication required")
			}))
			wantHSTS := strings.HasPrefix(baseURL, "https://")
			alice := h.Signup(t, "a@x.io")
			ids := map[string]string{}
			for _, k := range []string{"projects", "tickets", "labels", "comments", "tokens", "members"} {
				ids[k] = ulid.Make().String()
			}
			type call struct {
				method, path string
				opts         []ReqOpt
			}
			var calls []call
			for _, rt := range apiRoutes(h.Server) {
				method, path := substitute(t, rt.Pattern, ids)
				calls = append(calls, call{method, path, nil}, call{method, path, []ReqOpt{As(alice)}},
					call{method, path, []ReqOpt{RawBody("{garbage", "text/plain"), As(alice)}})
			}
			calls = append(calls,
				call{"GET", "/api/v1/projects", []ReqOpt{As(alice)}},
				call{"GET", "/api/v1/nope", nil}, call{"GET", "/api/v2/projects", nil},
				call{"PUT", "/api/v1/projects", []ReqOpt{As(alice)}}, call{"OPTIONS", "/api/v1/projects", nil},
				call{"GET", "/", nil}, call{"GET", "/anything/else", nil}, call{"POST", "/", nil},
				call{"GET", "/.well-known/x", nil}, call{"GET", "/healthz", nil},
				call{"GET", "/mcp", nil}, call{"POST", "/mcp", nil},
				call{"POST", "/api/v1/projects", []ReqOpt{As(alice), WithOrigin("http://evil.example")}},
			)
			seen := map[string]bool{}
			for _, c := range calls {
				r := h.Do(t, c.method, c.path, nil, c.opts...)
				label := c.method + " " + c.path
				got, has := r.Header["Strict-Transport-Security"]
				switch {
				case wantHSTS && (!has || len(got) != 1 || got[0] != "max-age=31536000"):
					t.Errorf("%s: HSTS = %v", label, got)
				case !wantHSTS && has:
					t.Errorf("%s: HSTS %v on an http BASE_URL", label, got)
				}
				id := r.Header.Get("X-Request-Id")
				if !reULID.MatchString(id) || seen[id] {
					t.Errorf("%s: X-Request-Id %q (duplicate=%v)", label, id, seen[id])
				}
				seen[id] = true
				for k, v := range wantHeaders {
					if g := r.Header.Get(k); g != v {
						t.Errorf("%s: %s = %q", label, k, g)
					}
				}
			}
		})
	}
}

// ---- concurrency ----

// TestConcurrentSignupExactlyOne: parallel signups of one email (case variants included) on a
// real file database give exactly one 201 and 409 email_taken for the rest.
func TestConcurrentSignupExactlyOne(t *testing.T) {
	for round := 0; round < 3; round++ {
		h := Setup(t, Opts{File: true})
		const n = 12
		email := func(i int) string {
			if i%2 == 0 {
				return "dup@x.io"
			}
			return " DUP@X.io "
		}
		codes := make([]int, n)
		errs := make([]string, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				r := h.Do(t, "POST", "/api/v1/auth/signup",
					map[string]any{"email": email(i), "display_name": fmt.Sprintf("U%d", i), "password": TestPassword},
					RemoteAddr(fmt.Sprintf("198.51.100.%d:1", i+1)))
				codes[i], errs[i] = r.Code, r.ErrCode(t)
			}(i)
		}
		close(start)
		wg.Wait()
		created, taken := 0, 0
		for i, c := range codes {
			switch {
			case c == 201:
				created++
			case c == 409 && errs[i] == "email_taken":
				taken++
			default:
				t.Errorf("round %d: signup %d got %d %q", round, i, c, errs[i])
			}
		}
		if created != 1 || taken != n-1 {
			t.Errorf("round %d: %d created, %d taken (want 1 and %d)", round, created, taken, n-1)
		}
		var users int
		if err := h.Env.Store.RawQueryRow(context.Background(), "SELECT COUNT(*) FROM users WHERE email = 'dup@x.io'").Scan(&users); err != nil || users != 1 {
			t.Errorf("round %d: %d users rows (err %v)", round, users, err)
		}
	}
}

// ---- 429 Retry-After with clock advance ----

func TestRetryAfterFollowsTheClockOnEveryLimitedRoute(t *testing.T) {
	type limited struct {
		name    string
		attempt func(h *Harness, c *Client) *Resp
		setup   func(t *testing.T, h *Harness) *Client
		failing int // status of a counted attempt
	}
	cases := []limited{
		{"login", func(h *Harness, _ *Client) *Resp {
			return h.Do(t, "POST", "/api/v1/auth/login", map[string]any{"email": "a@x.io", "password": "wrong password!"})
		}, func(t *testing.T, h *Harness) *Client { return nil }, 401},
		{"signup", func(h *Harness, _ *Client) *Resp {
			return h.Do(t, "POST", "/api/v1/auth/signup", map[string]any{"email": "a@x.io", "display_name": "", "password": "x"})
		}, func(t *testing.T, h *Harness) *Client { return nil }, 422},
		{"change password", func(h *Harness, c *Client) *Resp {
			return h.Do(t, "POST", "/api/v1/auth/me/password", map[string]any{"current_password": "wrong password!", "new_password": "another long password"}, As(c))
		}, func(t *testing.T, h *Harness) *Client { return h.Signup(t, "pw@x.io") }, 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := Setup(t, Opts{})
			client := c.setup(t, h)
			for i := 0; i < 5; i++ {
				if r := c.attempt(h, client); r.Code != c.failing {
					t.Fatalf("attempt %d: %d %s", i+1, r.Code, r.Body)
				}
			}
			ra := func(want string) {
				t.Helper()
				r := c.attempt(h, client)
				if r.Code != 429 || r.ErrCode(t) != "rate_limited" || r.Header.Get("Retry-After") != want {
					t.Fatalf("want 429 rate_limited Retry-After %s, got %d %q RA=%q", want, r.Code, r.ErrCode(t), r.Header.Get("Retry-After"))
				}
			}
			ra("60")
			h.Env.Clock.Advance(30 * time.Second)
			ra("30")
			h.Env.Clock.Advance(29*time.Second + 500*time.Millisecond)
			ra("1") // 0.5 s left rounds up, never 0
			h.Env.Clock.Advance(time.Second)
			if r := c.attempt(h, client); r.Code != c.failing {
				t.Fatalf("after the window: %d %s", r.Code, r.Body)
			}
		})
	}
}

// ---- 415 / 400 matrix and the body cap on every write route ----

func stateChanging(method string) bool { return method != "GET" && method != "HEAD" }

// writeRoutes returns every state-changing /api/v1 route with its row.
func writeRoutes(t *testing.T, s *Server) []Route {
	t.Helper()
	var out []Route
	for _, rt := range apiRoutes(s) {
		if m, _, _ := strings.Cut(rt.Pattern, " "); stateChanging(m) {
			if _, ok := mrows[rt.Pattern]; !ok {
				t.Errorf("route %q has no matrix row (see matrix_test.go)", rt.Pattern)
				continue
			}
			out = append(out, rt)
		}
	}
	if len(out) == 0 {
		t.Fatal("no write routes")
	}
	return out
}

// credFor gives a credential that clears the guard for the route: public routes need none, the
// rest an owner session. Paths use random ULIDs: decoding the body never depends on the project.
func (w *mworld) credFor(t *testing.T, rt Route) []ReqOpt {
	t.Helper()
	opts := []ReqOpt{freshAddr()}
	if mrows[rt.Pattern].acc != "public" {
		opts = append(opts, As(w.fresh(t, w.owner)))
	}
	return opts
}

func TestContentTypeAndBodyShapeMatrix(t *testing.T) {
	w := newMWorld(t)
	sc := w.scene(t)
	h := w.h
	for _, rt := range writeRoutes(t, h.Server) {
		row := mrows[rt.Pattern]
		method, path := substitute(t, rt.Pattern, sc.ghostIDs())
		t.Run(rt.Pattern, func(t *testing.T) {
			wantNo415 := func(label string, r *Resp) {
				t.Helper()
				if r.Code == 415 {
					t.Errorf("%s: unexpected 415 %s", label, r.Body)
				}
			}
			// 415: a present body with a missing or non-JSON Content-Type, on every write route
			for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonp", "text/json"} {
				r := h.Do(t, method, path, nil, append(w.credFor(t, rt), RawBody(`{}`, ct))...)
				if r.Code != 415 || r.ErrCode(t) != "unsupported_media_type" {
					t.Errorf("Content-Type %q: got %d %s", ct, r.Code, r.Body)
				}
			}
			if r := h.Do(t, method, path, nil, append(w.credFor(t, rt), RawBody(`{}`, ""))...); r.Code != 415 {
				t.Errorf("no Content-Type: got %d %s", r.Code, r.Body)
			}
			if r := h.doReq(t, method, path, strings.NewReader(`{}`), "", func(req *http.Request) { req.ContentLength = -1 },
				append(w.credFor(t, rt), freshAddr())...); r.Code != 415 {
				t.Errorf("chunked body without Content-Type: got %d %s", r.Code, r.Body)
			}
			// parameters on application/json are fine
			wantNo415("json; charset", h.Do(t, method, path, nil, append(w.credFor(t, rt), RawBody(`{}`, "application/json; charset=utf-8"))...))
			wantNo415("JSON uppercase", h.Do(t, method, path, nil, append(w.credFor(t, rt), RawBody(`{}`, "Application/JSON"))...))
			// no body, no Content-Type: never 415
			wantNo415("empty body", h.Do(t, method, path, nil, w.credFor(t, rt)...))

			if row.body == nil {
				return // endpoints without a body ignore it; nothing to decode
			}
			valid, _ := json.Marshal(row.body(sc))
			for name, body := range map[string]string{
				"truncated":      `{`,
				"array":          `[]`,
				"string":         `"x"`,
				"unknown field":  `{"zz_unknown_field":1}`,
				"trailing data":  string(valid) + ` {}`,
				"trailing junk":  string(valid) + `x`,
				"two objects":    `{}{}`,
				"not json":       `name=a`,
				"empty document": ``,
			} {
				r := h.Do(t, method, path, nil, append(w.credFor(t, rt), RawBody(body, "application/json"))...)
				if r.Code != 400 || r.ErrCode(t) != "bad_request" {
					t.Errorf("%s: got %d %s", name, r.Code, r.Body)
				}
			}
		})
	}
}

func TestBodyCapOnEveryWriteRoute(t *testing.T) {
	w := newMWorld(t)
	sc := w.scene(t)
	h := w.h
	big := strings.Repeat("a", MaxBodyBytes+1)
	for _, rt := range writeRoutes(t, h.Server) {
		row := mrows[rt.Pattern]
		method, path := substitute(t, rt.Pattern, sc.ghostIDs())
		t.Run(rt.Pattern, func(t *testing.T) {
			want := func(label string, r *Resp) {
				t.Helper()
				if r.Code != 400 || r.ErrCode(t) != "body_too_large" {
					t.Errorf("%s: got %d %.200s", label, r.Code, r.Body)
				}
			}
			// a declared length over the cap is refused before anything else, even anonymously
			want("anonymous, Content-Length", h.Do(t, method, path, nil, RawBody(`{"x":"`+big+`"}`, "application/json")))
			want("authenticated, Content-Length", h.Do(t, method, path, nil, append(w.credFor(t, rt), RawBody(`{"x":"`+big+`"}`, "application/json"))...))
			want("authenticated, text/plain", h.Do(t, method, path, nil, append(w.credFor(t, rt), RawBody(big, "text/plain"))...))
			if row.body == nil {
				return
			}
			// an undeclared length is cut off while reading
			want("chunked", h.doReq(t, method, path, strings.NewReader(`{"title":"`+big+`"}`), "application/json",
				func(req *http.Request) { req.ContentLength = -1 }, w.credFor(t, rt)...))
		})
	}
}

// TestBodyExactlyAtTheCap: a request body of exactly 1 MiB is accepted, one byte more is not.
func TestBodyExactlyAtTheCap(t *testing.T) {
	h := Setup(t, Opts{})
	alice := h.Signup(t, "a@x.io")
	pad := func(n int) string {
		body := `{"display_name":"Exact"}`
		return body + strings.Repeat(" ", n-len(body))
	}
	if r := h.Do(t, "PATCH", "/api/v1/auth/me", nil, As(alice), RawBody(pad(MaxBodyBytes), "application/json")); r.Code != 200 {
		t.Errorf("exactly at the cap: %d %.200s", r.Code, r.Body)
	}
	if r := h.Do(t, "PATCH", "/api/v1/auth/me", nil, As(alice), RawBody(pad(MaxBodyBytes+1), "application/json")); r.Code != 400 || r.ErrCode(t) != "body_too_large" {
		t.Errorf("one byte over: %d %.200s", r.Code, r.Body)
	}
}

// ---- field limits in characters, not bytes ----

type fieldCase struct {
	name  string
	field string
	limit int
	do    func(w *mworld, sc *mscene, val string) *Resp
	ok    int
}

func TestFieldLimitsAreCharactersNotBytes(t *testing.T) {
	project := "/api/v1/projects/"
	cases := []fieldCase{
		{"signup display name", "display_name", 100, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", "/api/v1/auth/signup", map[string]any{"email": fmt.Sprintf("u%d@x.io", addrSeq.Add(1)), "display_name": v, "password": TestPassword}, freshAddr())
		}, 201},
		{"update display name", "display_name", 100, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "PATCH", "/api/v1/auth/me", map[string]any{"display_name": v}, As(w.fresh(t, w.outsider)))
		}, 200},
		{"signup password (max)", "password", 200, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", "/api/v1/auth/signup", map[string]any{"email": fmt.Sprintf("p%d@x.io", addrSeq.Add(1)), "display_name": "P", "password": v}, freshAddr())
		}, 201},
		{"project name", "name", 100, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", "/api/v1/projects", map[string]any{"key": w.nextKey(), "name": v}, As(w.fresh(t, w.owner)))
		}, 201},
		{"project description", "description", 2000, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", "/api/v1/projects", map[string]any{"key": w.nextKey(), "name": "N", "description": v}, As(w.fresh(t, w.owner)))
		}, 201},
		{"patch project name", "name", 100, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "PATCH", project+sc.proj.Key, map[string]any{"name": v}, As(w.fresh(t, w.owner)))
		}, 200},
		{"ticket title", "title", 200, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", project+sc.proj.Key+"/tickets", map[string]any{"title": v}, As(w.fresh(t, w.owner)))
		}, 201},
		{"patch ticket title", "title", 200, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "PATCH", "/api/v1/tickets/"+sc.tkRef, map[string]any{"title": v}, As(w.fresh(t, w.owner)))
		}, 200},
		{"ticket description", "description", 20000, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", project+sc.proj.Key+"/tickets", map[string]any{"title": "T", "description": v}, As(w.fresh(t, w.owner)))
		}, 201},
		{"patch ticket description", "description", 20000, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "PATCH", "/api/v1/tickets/"+sc.tkRef, map[string]any{"description": v}, As(w.fresh(t, w.owner)))
		}, 200},
		{"comment body", "body", 20000, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", "/api/v1/tickets/"+sc.tkRef+"/comments", map[string]any{"body": v}, As(w.fresh(t, w.owner)))
		}, 201},
		{"edit comment body", "body", 20000, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "PATCH", "/api/v1/comments/"+sc.commentID, map[string]any{"body": v}, As(w.fresh(t, w.owner)))
		}, 200},
		{"label name", "name", 50, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", project+sc.proj.Key+"/labels", map[string]any{"name": v, "color": "teal"}, As(w.fresh(t, w.owner)))
		}, 201},
		{"patch label name", "name", 50, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "PATCH", "/api/v1/labels/"+sc.labelID, map[string]any{"name": v}, As(w.fresh(t, w.owner)))
		}, 200},
		{"token name", "name", 100, func(w *mworld, sc *mscene, v string) *Resp {
			return w.h.Do(t, "POST", "/api/v1/tokens", map[string]any{"name": v, "scope": "read"}, As(w.fresh(t, w.owner)))
		}, 201},
	}
	// one-, two-, three- and four-byte characters: the limit counts characters, so a value of
	// exactly `limit` characters passes whatever its byte length and one more fails
	units := []struct{ name, ch string }{{"ascii", "a"}, {"2-byte", "é"}, {"3-byte", "日"}, {"4-byte", "😀"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newMWorld(t)
			sc := w.scene(t)
			for _, u := range units {
				if r := c.do(w, sc, strings.Repeat(u.ch, c.limit)); r.Code != c.ok {
					t.Errorf("%d x %s: got %d %.200s, want %d", c.limit, u.name, r.Code, r.Body, c.ok)
				}
				r := c.do(w, sc, strings.Repeat(u.ch, c.limit+1))
				if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
					t.Errorf("%d x %s: got %d %.200s, want 422", c.limit+1, u.name, r.Code, r.Body)
					continue
				}
				fields, _ := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
				if _, ok := fields[c.field]; !ok {
					t.Errorf("%d x %s: want fields.%s, got %v", c.limit+1, u.name, c.field, fields)
				}
			}
		})
	}
}

func TestEmailAndPasswordBounds(t *testing.T) {
	h := Setup(t, Opts{})
	signup := func(email, pw string) *Resp {
		return h.Do(t, "POST", "/api/v1/auth/signup", map[string]any{"email": email, "display_name": "E", "password": pw}, freshAddr())
	}
	domain := "@example.com"
	if r := signup(strings.Repeat("a", 254-len(domain))+domain, TestPassword); r.Code != 201 {
		t.Errorf("254-character email: %d %s", r.Code, r.Body)
	}
	r := signup(strings.Repeat("b", 255-len(domain))+domain, TestPassword)
	if r.Code != 422 {
		t.Fatalf("255-character email: %d %s", r.Code, r.Body)
	}
	if _, ok := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)["email"]; !ok {
		t.Errorf("want fields.email: %s", r.Body)
	}
	for _, c := range []struct {
		name, pw string
		want     int
	}{
		{"9 characters", "123456789", 422}, {"10 characters", "1234567890", 201},
		{"9 multibyte characters", strings.Repeat("日", 9), 422}, {"10 multibyte characters", strings.Repeat("日", 10), 201},
		{"200 characters", strings.Repeat("p", 200), 201}, {"201 characters", strings.Repeat("p", 201), 422},
		{"200 multibyte characters", strings.Repeat("😀", 200), 201}, {"201 multibyte characters", strings.Repeat("😀", 201), 422},
	} {
		r := signup(fmt.Sprintf("pw%d@x.io", addrSeq.Add(1)), c.pw)
		if r.Code != c.want {
			t.Errorf("%s: %d %.200s", c.name, r.Code, r.Body)
		}
		if c.want == 422 {
			if _, ok := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)["password"]; !ok {
				t.Errorf("%s: want fields.password: %s", c.name, r.Body)
			}
		}
	}
}
