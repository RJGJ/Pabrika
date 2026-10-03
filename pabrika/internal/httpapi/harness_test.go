package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// This file is the shared test harness for every httpapi test (see doc.go, "Testing an
// endpoint"). It builds the real Server over testutil.Env with ONE fake clock shared by the
// services, the sessions and the rate limiters, and a cheap argon2 hasher.

// TestPassword is the password of every user the harness creates.
const TestPassword = "correct horse battery"

// Opts configure Setup. The zero value is: signup allowed, insecure cookie, no proxy trust,
// BASE_URL http://localhost:8080, in-memory database.
type Opts struct {
	SignupOff    bool
	CookieSecure bool
	TrustProxy   bool
	BaseURL      string
	File         bool // use a temp-file database (concurrency tests)
}

// Harness is a running Server with its collaborators.
type Harness struct {
	Env    *testutil.Env
	Server *Server
	Cfg    config.Config
	Hasher *auth.Hasher
	Logs   *syncBuffer // everything the server logged

	pwHash string
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Setup builds a Harness.
func Setup(t testing.TB, o Opts) *Harness {
	t.Helper()
	var env *testutil.Env
	if o.File {
		env = testutil.NewFileEnv(t)
	} else {
		env = testutil.NewTestServices(t)
	}
	cfg := config.Config{
		Port: 8080, DBPath: "unused", BaseURL: "http://localhost:8080",
		AllowSignup: !o.SignupOff, CookieSecure: o.CookieSecure, TrustProxy: o.TrustProxy,
	}
	if o.BaseURL != "" {
		cfg.BaseURL = o.BaseURL
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	hasher := auth.NewHasher(auth.TestParams, 0)
	srv := New(Deps{
		Config: cfg, Store: env.Store, Services: env.Svc, Logger: logger,
		Now: env.Clock.Now, Hasher: hasher,
	})
	h := &Harness{Env: env, Server: srv, Cfg: cfg, Hasher: hasher, Logs: logs}
	hash, err := hasher.Hash(context.Background(), TestPassword)
	if err != nil {
		t.Fatal(err)
	}
	h.pwHash = hash
	return h
}

// Client is a cookie-jar HTTP client over the handler.
type Client struct {
	User    service.User // zero for an anonymous client
	cookies map[string]*http.Cookie
}

// NewClient returns an anonymous client with an empty jar.
func (h *Harness) NewClient() *Client { return &Client{cookies: map[string]*http.Cookie{}} }

// Cookie returns the jar's cookie of that name (nil when absent).
func (c *Client) Cookie(name string) *http.Cookie { return c.cookies[name] }

// SetCookie plants a cookie in the jar.
func (c *Client) SetCookie(name, value string) {
	c.cookies[name] = &http.Cookie{Name: name, Value: value}
}

func (c *Client) absorb(resp *http.Response) {
	for _, ck := range resp.Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
}

// NewUser creates a user through the real service (password TestPassword) without a session.
func (h *Harness) NewUser(t testing.TB, email, displayName string) service.User {
	t.Helper()
	u, err := h.Env.Svc.Users.Create(context.Background(), email, displayName, h.pwHash)
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return u
}

// Login starts a new session for the user directly through auth.Sessions (the auth endpoints
// are not needed to test other endpoints) and returns a client holding the cookie.
func (h *Harness) Login(t testing.TB, u service.User) *Client {
	t.Helper()
	value, _, err := h.Server.Sessions().Create(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := h.NewClient()
	c.User = u
	c.SetCookie(auth.SessionCookieName, value)
	return c
}

// Signup creates a user (display name derived from the email) and logs it in.
func (h *Harness) Signup(t testing.TB, email string) *Client {
	t.Helper()
	local := strings.SplitN(email, "@", 2)[0]
	name := strings.ToUpper(local[:1]) + local[1:]
	return h.Login(t, h.NewUser(t, email, name))
}

// MkToken mints a real API token for the client's user and returns its plain secret and id.
// projectRef is "" or a project ULID.
func (h *Harness) MkToken(t testing.TB, c *Client, scope service.Scope, projectID string) (secret, id string) {
	t.Helper()
	id, secret = h.Env.NewTokenWithSecret(t, c.User.ID, scope, projectID)
	return secret, id
}

type reqSpec struct {
	client      *Client
	bearer      string
	origin      *string
	noOrigin    bool
	headers     http.Header
	raw         *string
	contentType string
	remoteAddr  string
}

// ReqOpt tweaks one request made with Do.
type ReqOpt func(*reqSpec)

// As sends the client's cookies and stores the cookies of the response.
func As(c *Client) ReqOpt { return func(r *reqSpec) { r.client = c } }

// Bearer authenticates with an API token secret.
func Bearer(secret string) ReqOpt { return func(r *reqSpec) { r.bearer = secret } }

// NoOrigin omits the Origin header (default is BASE_URL).
func NoOrigin() ReqOpt { return func(r *reqSpec) { r.noOrigin = true } }

// WithOrigin overrides the Origin header.
func WithOrigin(o string) ReqOpt { return func(r *reqSpec) { r.origin = &o } }

// WithHeader sets a request header.
func WithHeader(k, v string) ReqOpt {
	return func(r *reqSpec) {
		if r.headers == nil {
			r.headers = http.Header{}
		}
		r.headers.Set(k, v)
	}
}

// RawBody sends s verbatim with the given Content-Type ("" sends none).
func RawBody(s, contentType string) ReqOpt {
	return func(r *reqSpec) { r.raw, r.contentType = &s, contentType }
}

// RemoteAddr sets the peer address ("203.0.113.9:4000").
func RemoteAddr(a string) ReqOpt { return func(r *reqSpec) { r.remoteAddr = a } }

// Resp is a recorded response.
type Resp struct {
	Code   int
	Header http.Header
	Body   []byte
	resp   *http.Response
}

// JSON decodes the body as a JSON object.
func (r *Resp) JSON(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("body is not a JSON object (%d): %q", r.Code, r.Body)
	}
	return m
}

// ErrCode returns error.code of an error response ("" when the body is not an error).
func (r *Resp) ErrCode(t testing.TB) string {
	t.Helper()
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &e)
	return e.Error.Code
}

// Cookies returns the Set-Cookie cookies of the response.
func (r *Resp) Cookies() []*http.Cookie { return r.resp.Cookies() }

// Do performs one request against the handler. A non-nil body is sent as JSON with
// Content-Type application/json (a string or []byte is sent as is). Origin defaults to BASE_URL.
func (h *Harness) Do(t testing.TB, method, path string, body any, opts ...ReqOpt) *Resp {
	t.Helper()
	var spec reqSpec
	for _, o := range opts {
		o(&spec)
	}
	var rd io.Reader
	ct := ""
	switch {
	case spec.raw != nil:
		rd, ct = strings.NewReader(*spec.raw), spec.contentType
	case body != nil:
		var b []byte
		switch v := body.(type) {
		case string:
			b = []byte(v)
		case []byte:
			b = v
		default:
			var err error
			if b, err = json.Marshal(body); err != nil {
				t.Fatal(err)
			}
		}
		rd, ct = bytes.NewReader(b), "application/json"
	}
	req := httptest.NewRequest(method, path, rd)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	switch {
	case spec.noOrigin:
	case spec.origin != nil:
		req.Header.Set("Origin", *spec.origin)
	default:
		req.Header.Set("Origin", h.Cfg.BaseURL)
	}
	if spec.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+spec.bearer)
	}
	for k, vs := range spec.headers {
		req.Header[k] = vs
	}
	if spec.remoteAddr != "" {
		req.RemoteAddr = spec.remoteAddr
	}
	if spec.client != nil {
		for _, ck := range spec.client.cookies {
			req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
		}
	}
	rec := httptest.NewRecorder()
	h.Server.Handler().ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	if spec.client != nil {
		spec.client.absorb(res)
	}
	return &Resp{Code: res.StatusCode, Header: res.Header, Body: b, resp: res}
}
