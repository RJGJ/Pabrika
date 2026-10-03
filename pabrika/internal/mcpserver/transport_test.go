package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/httpapi"
	"github.com/RJGJ/Pabrika/internal/service"
)

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// httpFx is the fixture behind the real mux (httpapi.New + Mount).
type httpFx struct {
	*fx
	ts   *httptest.Server
	logs *syncBuf
	sess *auth.Sessions
}

const httpBase = "http://localhost:8080"

func newHTTPFx(t *testing.T) *httpFx {
	t.Helper()
	f := newFx(t)
	logs := &syncBuf{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	cfg := config.Config{Port: 8080, DBPath: "unused", BaseURL: httpBase, AllowSignup: true}
	sess := auth.NewSessions(f.env.Store, auth.SessionOptions{Now: f.env.Clock.Now, Logger: logger})
	resolver := auth.NewResolver(auth.ResolverDeps{Store: f.env.Store, Sessions: sess, Now: f.env.Clock.Now, Logger: logger})
	srv := httpapi.New(httpapi.Deps{Config: cfg, Store: f.env.Store, Services: f.env.Svc, Logger: logger,
		Now: f.env.Clock.Now, Sessions: sess, Resolver: resolver})
	Mount(srv, Deps{Services: f.env.Svc, Resolver: resolver, BaseURL: httpBase, Logger: logger})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &httpFx{fx: f, ts: ts, logs: logs, sess: sess}
}

func (h *httpFx) token(u string, scope service.Scope, limit *string) (id, secret string) {
	h.t.Helper()
	user := map[string]struct{ ID string }{"alice": {h.alice.ID}, "bob": {h.bob.ID}, "carol": {h.carol.ID}}[u]
	pid := ""
	if limit != nil {
		pid = *limit
	}
	return h.env.NewTokenWithSecret(h.t, user.ID, scope, pid)
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`

type raw struct {
	Code   int
	Header http.Header
	Body   string
}

func (h *httpFx) do(method, path, body string, hdr map[string]string) raw {
	h.t.Helper()
	req, err := http.NewRequest(method, h.ts.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return raw{resp.StatusCode, resp.Header, string(b)}
}

func bearer(secret string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + secret}
}

func (r raw) errCode(t *testing.T) string {
	t.Helper()
	var e struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal([]byte(r.Body), &e); err != nil {
		t.Fatalf("not a JSON error body: %q", r.Body)
	}
	return e.Error.Code
}

func TestAuth401Matrix(t *testing.T) {
	h := newHTTPFx(t)
	id, secret := h.token("alice", service.ScopeWrite, nil)
	revoked := func() string {
		rid, rsecret := h.token("alice", service.ScopeWrite, nil)
		if err := h.env.Svc.Tokens.Revoke(context.Background(), service.UserActor(h.alice.ID), rid); err != nil {
			t.Fatal(err)
		}
		return rsecret
	}()
	cases := map[string]map[string]string{
		"none":      nil,
		"malformed": {"Authorization": "Bearer nope"},
		"unknown":   {"Authorization": "Bearer pb_" + strings.Repeat("0", 64)},
		"revoked":   bearer(revoked),
		"basic":     {"Authorization": "Basic abc"},
	}
	var first string
	for name, hdr := range cases {
		r := h.do("POST", "/mcp", initBody, hdr)
		if r.Code != 401 || r.Header.Get("WWW-Authenticate") != "Bearer" || r.errCode(t) != "unauthorized" {
			t.Fatalf("%s: %d %v %s", name, r.Code, r.Header, r.Body)
		}
		if first == "" {
			first = r.Body
		} else if r.Body != first {
			t.Fatalf("%s: body differs", name)
		}
	}
	// unauthenticated GET is 401, not 405
	if r := h.do("GET", "/mcp", "", map[string]string{"Accept": "text/event-stream"}); r.Code != 401 {
		t.Fatal(r.Code)
	}
	// cookie only: 401, session untouched (no Set-Cookie: not slid, not cleared)
	cookie := h.env.NewSession(h.t, h.alice.ID)
	h.env.Clock.Advance(10 * 24 * time.Hour)
	r := h.do("POST", "/mcp", initBody, map[string]string{"Cookie": auth.SessionCookieName + "=" + cookie})
	if r.Code != 401 || r.Header.Get("Set-Cookie") != "" {
		t.Fatalf("cookie only: %d %v", r.Code, r.Header)
	}
	// bearer plus a valid cookie uses the token and no cookie handling
	r = h.do("POST", "/mcp", initBody, map[string]string{"Authorization": "Bearer " + secret, "Cookie": auth.SessionCookieName + "=" + cookie})
	if r.Code != 200 || r.Header.Get("Set-Cookie") != "" || r.Header.Get("Mcp-Session-Id") != "" {
		t.Fatalf("%d %v", r.Code, r.Header)
	}
	_ = id
	// last_used_at set after first use
	tokens, err := h.env.Svc.Tokens.List(context.Background(), service.UserActor(h.alice.ID))
	if err != nil {
		t.Fatal(err)
	}
	used := false
	for _, tk := range tokens {
		if tk.ID == id && tk.LastUsedAt != nil {
			used = true
		}
	}
	if !used {
		t.Fatal("last_used_at not set")
	}
}

func TestOriginRule(t *testing.T) {
	h := newHTTPFx(t)
	_, secret := h.token("alice", service.ScopeWrite, nil)
	for _, method := range []string{"POST", "GET", "DELETE"} {
		for _, origin := range []string{"https://evil.example", "null", "http://localhost:8081"} {
			for _, hdr := range []map[string]string{bearer(secret), nil} {
				hh := map[string]string{"Origin": origin}
				for k, v := range hdr {
					hh[k] = v
				}
				r := h.do(method, "/mcp", initBody, hh)
				if r.Code != 403 || r.errCode(t) != "origin_mismatch" {
					t.Fatalf("%s %s: %d %s", method, origin, r.Code, r.Body)
				}
				if r.Header.Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("CORS header")
				}
			}
		}
	}
	for _, origin := range []string{httpBase, "http://LOCALHOST:8080"} {
		hh := bearer(secret)
		hh["Origin"] = origin
		if r := h.do("POST", "/mcp", initBody, hh); r.Code != 200 {
			t.Fatalf("%s: %d %s", origin, r.Code, r.Body)
		}
	}
	if r := h.do("POST", "/mcp", initBody, bearer(secret)); r.Code != 200 || r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal(r.Code)
	}
}

func TestMethodsAndPaths(t *testing.T) {
	h := newHTTPFx(t)
	_, secret := h.token("alice", service.ScopeWrite, nil)
	if r := h.do("GET", "/mcp", "", map[string]string{"Authorization": "Bearer " + secret, "Accept": "text/event-stream"}); r.Code != 405 {
		t.Fatal(r.Code)
	}
	if r := h.do("DELETE", "/mcp", "", bearer(secret)); r.Code != 405 {
		t.Fatal(r.Code)
	}
	for _, p := range []string{"/mcp/x", "/.well-known/oauth-protected-resource"} {
		r := h.do("GET", p, "", nil)
		if r.Code != 404 || r.errCode(t) != "not_found" {
			t.Fatalf("%s: %d %s", p, r.Code, r.Body)
		}
	}
}

func TestBodyCap(t *testing.T) {
	h := newHTTPFx(t)
	_, secret := h.token("alice", service.ScopeWrite, nil)
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_ticket","arguments":{"project":"WEB","title":"x","description":"` +
		strings.Repeat("a", 1<<20) + `"}}}`
	r := h.do("POST", "/mcp", big, bearer(secret))
	if r.Code != 400 {
		t.Fatalf("%d %.100s", r.Code, r.Body)
	}
	if page, err := h.env.Svc.Tickets.List(context.Background(), h.aliceActor(), "WEB", service.TicketFilter{}); err != nil || len(page.Items) != 0 {
		t.Fatal("a tool ran", err)
	}
}

// sdkClient connects the SDK's Streamable HTTP client with a bearer header.
func (h *httpFx) sdkClient(secret string) *mcp.ClientSession {
	h.t.Helper()
	hc := &http.Client{Transport: headerRT{"Authorization", "Bearer " + secret}}
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	cs, err := cl.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: h.ts.URL + "/mcp", HTTPClient: hc, DisableStandaloneSSE: true}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type headerRT struct{ k, v string }

func (r headerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(r.k, r.v)
	return http.DefaultTransport.RoundTrip(req)
}

func TestEndToEndThroughMux(t *testing.T) {
	h := newHTTPFx(t)
	tokID, secret := h.token("bob", service.ScopeWrite, nil)
	cs := h.sdkClient(secret)
	names := toolNames(t, cs)
	if len(names) != 14 {
		t.Fatal(names)
	}
	pl := h.okCall(cs, "list_projects", nil)
	if p := items(pl); len(p) != 1 || p[0]["key"] != "WEB" || p[0]["role"] != "editor" || p[0]["url"] != httpBase+"/p/WEB" {
		t.Fatal(pl)
	}
	tk := h.okCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": "e2e"})
	if tk["ref"] != "WEB-1" {
		t.Fatal(tk)
	}
	c := h.okCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": "hello"})
	d, _ := h.env.Svc.Tickets.Get(context.Background(), h.aliceActor(), "WEB-1")
	rows := h.activity(d.ID)
	if len(rows) != 1 || rows[0].ActorID != tokID || rows[0].ActorType != "api_token" {
		t.Fatalf("%+v", rows)
	}
	cm, _, _ := h.env.Svc.Comments.Latest(context.Background(), h.aliceActor(), "WEB-1", 5)
	if len(cm) != 1 || cm[0].ID != c["id"] || cm[0].Author.Type != service.ActorAPIToken {
		t.Fatalf("%+v", cm)
	}
	// read token over HTTP sees 5 tools
	_, rsecret := h.token("alice", service.ScopeRead, nil)
	if n := toolNames(t, h.sdkClient(rsecret)); len(n) != 5 {
		t.Fatal(n)
	}
	// limited token: the ProjectKey from the resolver reaches the message
	_, lsecret := h.token("alice", service.ScopeWrite, &h.web.ID)
	lcs := h.sdkClient(lsecret)
	h.errCall(lcs, "get_ticket", map[string]any{"ticket": "OPS-1"}, "This token is limited to project WEB;")
	// two independent requests share no state: no session id
	r := h.do("POST", "/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, bearer(secret))
	if r.Code != 200 || r.Header.Get("Mcp-Session-Id") != "" || !strings.Contains(r.Body, "list_projects") {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	// logging: no secrets, arguments or ticket text
	lg := h.logs.String()
	for _, bad := range []string{secret, rsecret, "Bearer", "e2e", "hello"} {
		if strings.Contains(lg, bad) {
			t.Fatalf("log leaks %q:\n%s", bad, lg)
		}
	}
	if !strings.Contains(lg, "tool=create_ticket") {
		t.Fatal("tool call not logged")
	}
}

func TestSchemasDrift(t *testing.T) {
	enum := func(s map[string]any, prop string) []string {
		var out []string
		for _, v := range s["properties"].(m)[prop].(m)["enum"].([]any) {
			out = append(out, v.(string))
		}
		return out
	}
	maxLen := func(s map[string]any, prop string) int { return s["properties"].(m)[prop].(m)["maxLength"].(int) }
	def := map[string]*toolDef{}
	for _, d := range allTools {
		def[d.name] = d
	}
	var st, pr, co []string
	for _, v := range service.Statuses {
		st = append(st, string(v))
	}
	for _, v := range service.Priorities {
		pr = append(pr, string(v))
	}
	for _, v := range service.LabelColors {
		co = append(co, string(v))
	}
	if !reflect.DeepEqual(enum(def["create_ticket"].schema, "status"), st) || !reflect.DeepEqual(enum(def["move_ticket"].schema, "status"), st) ||
		!reflect.DeepEqual(enum(def["list_tickets"].schema, "status"), st) {
		t.Fatal("status enum drift")
	}
	if !reflect.DeepEqual(enum(def["create_ticket"].schema, "priority"), pr) || !reflect.DeepEqual(enum(def["update_ticket"].schema, "priority"), pr) {
		t.Fatal("priority enum drift")
	}
	if !reflect.DeepEqual(enum(def["create_label"].schema, "color"), co) {
		t.Fatal("color enum drift")
	}
	if maxLen(def["create_ticket"].schema, "title") != service.MaxTitle || maxLen(def["create_ticket"].schema, "description") != service.MaxDescription ||
		maxLen(def["add_comment"].schema, "body") != service.MaxCommentBody || maxLen(def["update_comment"].schema, "body") != service.MaxCommentBody ||
		maxLen(def["create_project"].schema, "name") != service.MaxProjectName || maxLen(def["create_project"].schema, "description") != service.MaxProjectDescription ||
		maxLen(def["create_label"].schema, "name") != service.MaxLabelName {
		t.Fatal("cap drift")
	}
	required := map[string][]string{
		"list_projects": nil, "create_project": {"name", "key"}, "update_project": {"project"}, "list_members": {"project"},
		"list_labels": {"project"}, "create_label": {"project", "name"}, "list_tickets": {"project"}, "get_ticket": {"ticket"},
		"create_ticket": {"project", "title"}, "update_ticket": {"ticket"}, "move_ticket": {"ticket", "status"},
		"add_comment": {"ticket", "body"}, "delete_ticket": {"ticket"}, "update_comment": {"comment", "body"},
	}
	if len(def) != len(required) {
		t.Fatal("tool count")
	}
	for name, want := range required {
		s := def[name].schema
		var got []string
		if r, ok := s["required"].([]any); ok {
			for _, v := range r {
				got = append(got, v.(string))
			}
		}
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s required %v want %v", name, got, want)
		}
		if s["additionalProperties"] != false {
			t.Fatalf("%s additionalProperties", name)
		}
	}
	for _, n := range []string{"title", "description", "priority"} {
		_ = n
	}
}

func TestResources(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	f.seedTicket("WEB", "b", "done")
	f.seedTicket("WEB", "c", "todo")
	f.seedTicket("OPS", "o", "todo")
	read := func(cs *mcp.ClientSession, uri string) (map[string]any, error) {
		r, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(r.Contents[0].Text), &out); err != nil {
			t.Fatal(err)
		}
		return out, nil
	}
	cs, _ := f.as(f.alice, service.ScopeRead)
	pl, err := read(cs, "pabrika://projects")
	if err != nil || len(items(pl)) != 2 {
		t.Fatal(pl, err)
	}
	b, err := read(cs, "pabrika://projects/web/board")
	if err != nil {
		t.Fatal(err)
	}
	cols := b["columns"].(map[string]any)
	if b["project"] != "WEB" || b["truncated"] != false || len(cols["todo"].([]any)) != 2 || len(cols["done"].([]any)) != 1 ||
		len(cols["backlog"].([]any)) != 0 || cols["todo"].([]any)[0].(map[string]any)["ref"] != "WEB-1" {
		t.Fatal(b)
	}
	if _, err := read(cs, f.web.ID[:0]+"pabrika://projects/"+f.web.ID+"/board"); err != nil {
		t.Fatal("ULID board", err)
	}
	if _, err := read(cs, "pabrika://projects/ZZZ/board"); err == nil {
		t.Fatal("unknown key must fail")
	}
	p, _ := f.principal(f.alice, service.ScopeRead, &f.web)
	lcs := f.connect(p)
	if pl, _ := read(lcs, "pabrika://projects"); len(items(pl)) != 1 {
		t.Fatal(pl)
	}
	if _, err := read(lcs, "pabrika://projects/OPS/board"); err == nil {
		t.Fatal("limited token read OPS board")
	}
	dcs, _ := f.as(f.dave, service.ScopeRead)
	if _, err := read(dcs, "pabrika://projects/WEB/board"); err == nil {
		t.Fatal("non-member read board")
	}
}

func TestBoardCap(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	for i := 0; i < 505; i++ {
		if _, err := f.env.Svc.Tickets.Create(ctx, f.aliceActor(), "OPS", service.CreateTicketInput{Title: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	cs, _ := f.as(f.alice, service.ScopeRead)
	r, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "pabrika://projects/OPS/board"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Columns   map[string][]any
		Truncated bool
	}
	if err := json.Unmarshal([]byte(r.Contents[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if n := len(out.Columns["todo"]); n != 500 || !out.Truncated {
		t.Fatal(n, out.Truncated)
	}
}
