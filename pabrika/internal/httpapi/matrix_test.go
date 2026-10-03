package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/config"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// WP8 tracks 1 and 2: the scope x role matrix and the non-member 404 test, both table-driven
// off Server.Routes(). A route that has no row in mrows fails TestMatrixCoversEveryRoute, so a
// new endpoint cannot ship without being put through every principal.

// ---- request helper with a context / chunking hook (h.Do cannot do either) ----

// doReq is h.Do for callers that need a raw body reader and a hook on the *http.Request
// (cancelled context for streams, unknown Content-Length for the body cap).
func (h *Harness) doReq(t testing.TB, method, path string, body io.Reader, ct string, mutate func(*http.Request), opts ...ReqOpt) *Resp {
	t.Helper()
	var spec reqSpec
	for _, o := range opts {
		o(&spec)
	}
	req := httptest.NewRequest(method, path, body)
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
	if mutate != nil {
		mutate(req)
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

// setupWithHub is Setup plus a real event hub, so the SSE route answers like production
// (membership 404 instead of 503 "unavailable").
func setupWithHub(t testing.TB) *Harness {
	t.Helper()
	env := testutil.NewTestServices(t, testutil.WithHub(service.HubOptions{}))
	cfg := config.Config{Port: 8080, DBPath: "unused", BaseURL: "http://localhost:8080", AllowSignup: true}
	logs := &syncBuffer{}
	hasher := auth.NewHasher(auth.TestParams, 0)
	srv := New(Deps{
		Config: cfg, Store: env.Store, Services: env.Svc, Logger: slog.New(slog.NewTextHandler(logs, nil)),
		Now: env.Clock.Now, Hasher: hasher, Hub: env.Hub,
	})
	h := &Harness{Env: env, Server: srv, Cfg: cfg, Hasher: hasher, Logs: logs}
	hash, err := hasher.Hash(context.Background(), TestPassword)
	if err != nil {
		t.Fatal(err)
	}
	h.pwHash = hash
	return h
}

// ---- world and scene ----

// mworld is one harness with a fixed cast of users. Per-principal state (projects, tokens,
// sessions) is created fresh in a scene so destructive rows (DELETE project, logout, ...) never
// disturb each other.
type mworld struct {
	h                                             *Harness
	owner, editor, viewer, outsider, target, newb *Client
	keyN                                          int
}

func newMWorld(t testing.TB) *mworld {
	t.Helper()
	h := setupWithHub(t)
	return &mworld{h: h,
		owner: h.Signup(t, "owner@x.io"), editor: h.Signup(t, "editor@x.io"), viewer: h.Signup(t, "viewer@x.io"),
		outsider: h.Signup(t, "outsider@x.io"), target: h.Signup(t, "target@x.io"), newb: h.Signup(t, "newbie@x.io"),
	}
}

func (w *mworld) nextKey() string {
	w.keyN++
	n := w.keyN
	b := []byte("AAA")
	for i := 2; i >= 0; i-- {
		b[i] = byte('A' + n%26)
		n /= 26
	}
	return string(b)
}

func mUser(c *Client) testutil.User {
	return testutil.User{ID: c.User.ID, Email: c.User.Email, DisplayName: c.User.DisplayName}
}

// fresh returns a client holding a brand new session of the same user.
func (w *mworld) fresh(t testing.TB, c *Client) *Client { return w.h.Login(t, c.User) }

// mscene is a project the cast belongs to: owner (owner), editor (editor), viewer (viewer),
// target (viewer; the subject of member routes); outsider and newbie are not members. other is a
// second project owned by the owner, for project-limited tokens. It holds one ticket, label
// and comment (written by the owner's session).
type mscene struct {
	w                               *mworld
	proj, other                     testutil.Project
	tkRef, tkID, labelID, commentID string
}

func (w *mworld) scene(t testing.TB) *mscene {
	t.Helper()
	h := w.h
	sc := &mscene{w: w}
	sc.proj = h.Env.NewProject(t, mUser(w.owner), w.nextKey())
	sc.other = h.Env.NewProject(t, mUser(w.owner), w.nextKey())
	h.Env.AddMember(t, sc.proj.ID, w.editor.User.ID, service.RoleEditor)
	h.Env.AddMember(t, sc.proj.ID, w.viewer.User.ID, service.RoleViewer)
	h.Env.AddMember(t, sc.proj.ID, w.target.User.ID, service.RoleViewer)
	own := w.fresh(t, w.owner)
	r := h.Do(t, "POST", "/api/v1/projects/"+sc.proj.Key+"/tickets", map[string]any{"title": "seed"}, As(own))
	if r.Code != 201 {
		t.Fatalf("seed ticket: %d %s", r.Code, r.Body)
	}
	sc.tkRef, sc.tkID = r.JSON(t)["ref"].(string), r.JSON(t)["id"].(string)
	r = h.Do(t, "POST", "/api/v1/projects/"+sc.proj.Key+"/labels", map[string]any{"name": "seed", "color": "red"}, As(own))
	if r.Code != 201 {
		t.Fatalf("seed label: %d %s", r.Code, r.Body)
	}
	sc.labelID = r.JSON(t)["id"].(string)
	r = h.Do(t, "POST", "/api/v1/tickets/"+sc.tkRef+"/comments", map[string]any{"body": "seed"}, As(own))
	if r.Code != 201 {
		t.Fatalf("seed comment: %d %s", r.Code, r.Body)
	}
	sc.commentID = r.JSON(t)["id"].(string)
	return sc
}

// ---- principals ----

const (
	rNone = iota
	rViewer
	rEditor
	rOwner
)

// mprinc is one row of the principal axis. role is the member role in the scene project (0 for a
// non-member); token principals carry their owner's role, read tokens are capped by the scope
// rule, limited tokens by "this" (the scene project) or "other".
type mprinc struct {
	name  string
	anon  bool
	token bool
	read  bool
	lim   string // "", "this", "other"
	role  int
	user  func(w *mworld) *Client
}

var (
	uOwner    = func(w *mworld) *Client { return w.owner }
	uEditor   = func(w *mworld) *Client { return w.editor }
	uViewer   = func(w *mworld) *Client { return w.viewer }
	uOutsider = func(w *mworld) *Client { return w.outsider }
)

var mprincs = []mprinc{
	{name: "anonymous", anon: true},
	{name: "non-member session", role: rNone, user: uOutsider},
	{name: "viewer session", role: rViewer, user: uViewer},
	{name: "editor session", role: rEditor, user: uEditor},
	{name: "owner session", role: rOwner, user: uOwner},
	{name: "write token as viewer", token: true, role: rViewer, user: uViewer},
	{name: "write token as editor", token: true, role: rEditor, user: uEditor},
	{name: "write token as owner", token: true, role: rOwner, user: uOwner},
	{name: "read token as editor", token: true, read: true, role: rEditor, user: uEditor},
	{name: "read token as owner", token: true, read: true, role: rOwner, user: uOwner},
	{name: "limited write token (this project)", token: true, lim: "this", role: rOwner, user: uOwner},
	{name: "limited write token (other project)", token: true, lim: "other", role: rOwner, user: uOwner},
}

// opts returns the credential options for p in scene sc (a fresh session or a new token).
func (p mprinc) opts(t testing.TB, sc *mscene) []ReqOpt {
	t.Helper()
	if p.anon {
		return nil
	}
	w := sc.w
	u := p.user(w)
	if !p.token {
		return []ReqOpt{As(w.fresh(t, u))}
	}
	scope := service.ScopeWrite
	if p.read {
		scope = service.ScopeRead
	}
	limit := ""
	switch p.lim {
	case "this":
		limit = sc.proj.ID
	case "other":
		limit = sc.other.ID
	}
	secret, _ := w.h.MkToken(t, u, scope, limit)
	return []ReqOpt{Bearer(secret)}
}

// ---- rows ----

type outcome struct {
	code int
	err  string
}

func (o outcome) String() string { return fmt.Sprintf("%d %q", o.code, o.err) }

// mrow describes one route: the literal Access and Write (cross-checked against the route table),
// the minimum effective role, whether the path resolves a project resource (non-members get 404)
// and a valid body.
type mrow struct {
	acc    string // "public" | "authed" | "session"
	write  bool
	min    int  // minimum effective project role for success
	scoped bool // the path names a project, ticket, label or comment
	ok     int
	body   func(sc *mscene) any
	stream bool // SSE: success is checked with an already-cancelled request context

	limitedForbidden bool                              // limited tokens get 403 forbidden (create project)
	special          func(p mprinc, o outcome) outcome // overrides the computed outcome
}

func jb(m map[string]any) func(*mscene) any { return func(*mscene) any { return m } }

var mrows = map[string]mrow{
	"GET /api/v1/auth/config": {acc: "public", ok: 200},
	"POST /api/v1/auth/signup": {acc: "public", ok: 201, body: func(sc *mscene) any {
		return map[string]any{"email": "new" + sc.proj.Key + "@x.io", "display_name": "N", "password": TestPassword}
	}},
	"POST /api/v1/auth/login": {acc: "public", ok: 200, body: func(*mscene) any {
		return map[string]any{"email": "owner@x.io", "password": TestPassword}
	}},
	"POST /api/v1/auth/logout":      {acc: "session", ok: 204},
	"GET /api/v1/auth/me":           {acc: "authed", ok: 200},
	"PATCH /api/v1/auth/me":         {acc: "session", ok: 200, body: jb(map[string]any{"display_name": "Zed"})},
	"POST /api/v1/auth/me/password": {acc: "session", ok: 204, body: jb(map[string]any{"current_password": TestPassword, "new_password": "another long password"})},

	"GET /api/v1/tokens":         {acc: "session", ok: 200},
	"POST /api/v1/tokens":        {acc: "session", ok: 201, body: jb(map[string]any{"name": "ci", "scope": "read"})},
	"DELETE /api/v1/tokens/{id}": {acc: "session", ok: 204},

	"GET /api/v1/projects": {acc: "authed", ok: 200},
	"POST /api/v1/projects": {acc: "authed", write: true, ok: 201, limitedForbidden: true, body: func(sc *mscene) any {
		return map[string]any{"key": sc.w.nextKey(), "name": "Made"}
	}},
	"GET /api/v1/projects/{id}":    {acc: "authed", min: rViewer, scoped: true, ok: 200},
	"PATCH /api/v1/projects/{id}":  {acc: "authed", write: true, min: rOwner, scoped: true, ok: 200, body: jb(map[string]any{"name": "Renamed"})},
	"DELETE /api/v1/projects/{id}": {acc: "session", write: true, min: rOwner, scoped: true, ok: 204},

	"GET /api/v1/projects/{id}/members": {acc: "authed", min: rViewer, scoped: true, ok: 200},
	"POST /api/v1/projects/{id}/members": {acc: "session", write: true, min: rOwner, scoped: true, ok: 201, body: func(*mscene) any {
		return map[string]any{"email": "newbie@x.io", "role": "viewer"}
	}},
	"PATCH /api/v1/projects/{id}/members/{userId}":  {acc: "session", write: true, min: rOwner, scoped: true, ok: 200, body: jb(map[string]any{"role": "editor"})},
	"DELETE /api/v1/projects/{id}/members/{userId}": {acc: "session", write: true, min: rOwner, scoped: true, ok: 204},

	"GET /api/v1/projects/{id}/labels":  {acc: "authed", min: rViewer, scoped: true, ok: 200},
	"POST /api/v1/projects/{id}/labels": {acc: "authed", write: true, min: rEditor, scoped: true, ok: 201, body: jb(map[string]any{"name": "fresh", "color": "blue"})},
	"PATCH /api/v1/labels/{id}":         {acc: "authed", write: true, min: rEditor, scoped: true, ok: 200, body: jb(map[string]any{"name": "renamed"})},
	"DELETE /api/v1/labels/{id}":        {acc: "authed", write: true, min: rEditor, scoped: true, ok: 204},

	"GET /api/v1/projects/{id}/tickets":  {acc: "authed", min: rViewer, scoped: true, ok: 200},
	"POST /api/v1/projects/{id}/tickets": {acc: "authed", write: true, min: rEditor, scoped: true, ok: 201, body: jb(map[string]any{"title": "new"})},
	"GET /api/v1/tickets/{id}":           {acc: "authed", min: rViewer, scoped: true, ok: 200},
	"PATCH /api/v1/tickets/{id}":         {acc: "authed", write: true, min: rEditor, scoped: true, ok: 200, body: jb(map[string]any{"title": "edited"})},
	"DELETE /api/v1/tickets/{id}":        {acc: "authed", write: true, min: rEditor, scoped: true, ok: 204},
	"POST /api/v1/tickets/{id}/move":     {acc: "authed", write: true, min: rEditor, scoped: true, ok: 200, body: jb(map[string]any{"status": "done"})},
	"GET /api/v1/tickets/{id}/activity":  {acc: "authed", min: rViewer, scoped: true, ok: 200},

	"GET /api/v1/tickets/{id}/comments":  {acc: "authed", min: rViewer, scoped: true, ok: 200},
	"POST /api/v1/tickets/{id}/comments": {acc: "authed", write: true, min: rEditor, scoped: true, ok: 201, body: jb(map[string]any{"body": "hello"})},
	// The seeded comment was written by the owner's SESSION, so only that exact actor may edit it:
	// the owner's write token and every other member are forbidden.
	"PATCH /api/v1/comments/{id}": {acc: "authed", write: true, min: rEditor, scoped: true, ok: 200, body: jb(map[string]any{"body": "edited"}),
		special: func(p mprinc, o outcome) outcome {
			if o.code == 200 && !(p.role == rOwner && !p.token) {
				return outcome{403, "forbidden"}
			}
			return o
		}},
	// Delete: its author or an owner. The author is the owner's session, so owners pass.
	"DELETE /api/v1/comments/{id}": {acc: "authed", write: true, min: rOwner, scoped: true, ok: 204},

	"GET /api/v1/projects/{id}/events": {acc: "session", min: rViewer, scoped: true, ok: 200, stream: true},
}

var (
	placeholderRE = regexp.MustCompile(`([a-z]+)/\{[A-Za-z]+\}`)
	apiPatternRE  = regexp.MustCompile(`^[A-Z]+ /api/v1/`)
)

// substitute fills the placeholders of a route pattern from ids, keyed by the path segment before
// the placeholder (projects, tickets, labels, comments, tokens, members). A route whose placeholder
// follows an unknown segment fails the test, which forces the helper to learn about it.
func substitute(t testing.TB, pattern string, ids map[string]string) (method, path string) {
	t.Helper()
	method, path, _ = strings.Cut(pattern, " ")
	path = placeholderRE.ReplaceAllStringFunc(path, func(m string) string {
		seg, _, _ := strings.Cut(m, "/")
		v, ok := ids[seg]
		if !ok {
			t.Fatalf("pattern %q: no substitution rule for placeholder after %q", pattern, seg)
		}
		return seg + "/" + v
	})
	return method, path
}

func (sc *mscene) ids() map[string]string {
	return map[string]string{
		"projects": sc.proj.Key, "tickets": sc.tkRef, "labels": sc.labelID, "comments": sc.commentID,
		"members": sc.w.target.User.ID, "tokens": ulid.Make().String(),
	}
}

// ghostIDs are random valid ULIDs for every resource kind except members (the user is real, so
// only the hidden project differs).
func (sc *mscene) ghostIDs() map[string]string {
	g := map[string]string{}
	for _, k := range []string{"projects", "tickets", "labels", "comments", "tokens"} {
		g[k] = ulid.Make().String()
	}
	g["members"] = sc.w.target.User.ID
	return g
}

// send performs one matrix request. Streams run with an already-cancelled context so the SSE
// handler returns right after its preamble.
func (r mrow) send(t testing.TB, sc *mscene, pattern string, ids map[string]string, opts []ReqOpt) *Resp {
	t.Helper()
	method, path := substitute(t, pattern, ids)
	var body any
	if r.body != nil {
		body = r.body(sc)
	}
	if !r.stream {
		return sc.w.h.Do(t, method, path, body, opts...)
	}
	// The stream handler runs until its request context ends; the beforeLoop hook (run after the
	// 200 and the preamble are flushed) cancels it, so an allowed stream returns at once. The
	// context must not be cancelled earlier or the auth lookups would fail with a 500.
	srv := sc.w.h.Server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.events.beforeLoop = cancel
	defer func() { srv.events.beforeLoop = nil }()
	return sc.w.h.doReq(t, method, path, nil, "", func(req *http.Request) {
		*req = *req.WithContext(ctx)
	}, opts...)
}

// expect computes the documented outcome for principal p on this row.
func (r mrow) expect(p mprinc) outcome {
	ok := outcome{code: r.ok}
	if r.acc == "public" {
		return ok
	}
	if p.anon {
		return outcome{401, "unauthorized"}
	}
	if p.token && r.acc == "session" {
		return outcome{403, service.CodeSessionRequired}
	}
	if p.token && p.read && r.write {
		return outcome{403, service.CodeInsufficientScope}
	}
	if !r.scoped {
		if r.limitedForbidden && p.lim != "" {
			return outcome{403, "forbidden"}
		}
		return ok
	}
	out := ok
	switch {
	case p.role == rNone || p.lim == "other":
		out = outcome{404, "not_found"}
	case p.role < r.min:
		out = outcome{403, "forbidden"}
	}
	if r.special != nil {
		out = r.special(p, out)
	}
	return out
}

func (r mrow) pathIDs(t testing.TB, sc *mscene, p mprinc) map[string]string {
	ids := sc.ids()
	if !p.anon {
		ids["tokens"] = sc.w.h.Env.NewToken(t, p.user(sc.w).User.ID, service.ScopeRead, "")
	}
	return ids
}

func apiRoutes(s *Server) []Route {
	var out []Route
	for _, rt := range s.Routes() {
		if apiPatternRE.MatchString(rt.Pattern) {
			out = append(out, rt)
		}
	}
	return out
}

func accessName(a Access) string {
	switch a {
	case Public:
		return "public"
	case Authed:
		return "authed"
	case SessionOnly:
		return "session"
	}
	return "unset"
}

// TestMatrixCoversEveryRoute fails when a route has no row (or a row has no route), or when a
// row's literal Access or Write flag disagrees with the route table.
func TestMatrixCoversEveryRoute(t *testing.T) {
	h := setupWithHub(t)
	seen := map[string]bool{}
	for _, rt := range apiRoutes(h.Server) {
		seen[rt.Pattern] = true
		row, ok := mrows[rt.Pattern]
		if !ok {
			t.Errorf("route %q has no row in mrows: add it to the scope x role matrix", rt.Pattern)
			continue
		}
		if row.acc != accessName(rt.Access) {
			t.Errorf("%s: row says access %q, route table says %q", rt.Pattern, row.acc, accessName(rt.Access))
		}
		if row.write != rt.Write {
			t.Errorf("%s: row says write=%v, route table says %v", rt.Pattern, row.write, rt.Write)
		}
		if row.scoped && row.min == rNone {
			t.Errorf("%s: scoped row needs a minimum role", rt.Pattern)
		}
		if method, _, _ := strings.Cut(rt.Pattern, " "); method != "GET" && !row.write && row.acc != "public" && row.acc != "session" {
			t.Errorf("%s: a state-changing authed route must be Write", rt.Pattern)
		}
	}
	for p := range mrows {
		if !seen[p] {
			t.Errorf("mrows has a stale row %q (no such route)", p)
		}
	}
}

// TestScopeRoleMatrix runs every route against every principal and compares with the documented
// outcome (phase 2 spec section 13 table).
func TestScopeRoleMatrix(t *testing.T) {
	probe := setupWithHub(t)
	patterns := make([]string, 0, len(mrows))
	for _, rt := range apiRoutes(probe.Server) {
		patterns = append(patterns, rt.Pattern)
	}
	sort.Strings(patterns)
	for _, pat := range patterns {
		row, ok := mrows[pat]
		if !ok {
			continue // reported by TestMatrixCoversEveryRoute
		}
		t.Run(pat, func(t *testing.T) {
			w := newMWorld(t) // one world per route: rows that spend users' state cannot interfere
			for _, p := range mprincs {
				if row.acc == "public" && !p.anon {
					continue // a login would rotate the presented session; public rows run anonymously
				}
				sc := w.scene(t)
				opts := p.opts(t, sc)
				r := row.send(t, sc, pat, row.pathIDs(t, sc, p), opts)
				want := row.expect(p)
				if r.Code != want.code || r.ErrCode(t) != want.err {
					t.Errorf("%s: got %d %q (%s), want %s", p.name, r.Code, r.ErrCode(t), r.Body, want)
				}
			}
		})
	}
}

// TestSessionOnlyBeforeNotFoundOrValidation: a token calling a session-only route gets 403
// session_required before any 404 or 422/400, and a read token calling a write route gets 403
// insufficient_scope before them too, whatever the path and body.
func TestSessionOnlyBeforeNotFoundOrValidation(t *testing.T) {
	w := newMWorld(t)
	sc := w.scene(t)
	ownerTok, _ := w.h.MkToken(t, w.owner, service.ScopeWrite, "")
	readTok, _ := w.h.MkToken(t, w.owner, service.ScopeRead, "")
	limTok, _ := w.h.MkToken(t, w.owner, service.ScopeWrite, sc.other.ID)
	bad := []string{`{garbage`, `{"unknown_field":1}`, `[]`}
	n := 0
	for _, rt := range apiRoutes(w.h.Server) {
		row := mrows[rt.Pattern]
		if row.acc != "session" && !(row.acc == "authed" && row.write) {
			continue
		}
		for _, who := range []struct {
			name string
			opts []ReqOpt
			want outcome
		}{
			{"write token", []ReqOpt{Bearer(ownerTok)}, outcome{403, service.CodeSessionRequired}},
			{"read token", []ReqOpt{Bearer(readTok)}, outcome{403, service.CodeSessionRequired}},
			{"limited token", []ReqOpt{Bearer(limTok)}, outcome{403, service.CodeSessionRequired}},
		} {
			want := who.want
			if row.acc != "session" {
				if who.name != "read token" {
					continue // a write token on an authed write route is a normal request
				}
				want = outcome{403, service.CodeInsufficientScope}
			}
			method, path := substitute(t, rt.Pattern, sc.ghostIDs())
			if method == "GET" {
				continue
			}
			for _, b := range bad {
				r := w.h.Do(t, method, path, nil, append(who.opts, RawBody(b, "application/json"))...)
				n++
				if r.Code != want.code || r.ErrCode(t) != want.err {
					t.Errorf("%s %s with %s and body %s: got %d %q, want %s", method, path, who.name, b, r.Code, r.ErrCode(t), want)
				}
			}
			// no body and an unknown project, still the credential errors
			r := w.h.Do(t, method, path, nil, who.opts...)
			if r.Code != want.code || r.ErrCode(t) != want.err {
				t.Errorf("%s %s with %s, no body: got %d %q, want %s", method, path, who.name, r.Code, r.ErrCode(t), want)
			}
		}
	}
	if n == 0 {
		t.Fatal("no rows exercised")
	}
}

// TestAnonymousIs401BeforeEverything: no credentials is 401 on every non-public route, even
// with an unknown project, a malformed body or a wrong content type.
func TestAnonymousIs401BeforeEverything(t *testing.T) {
	w := newMWorld(t)
	sc := w.scene(t)
	for _, rt := range apiRoutes(w.h.Server) {
		row := mrows[rt.Pattern]
		if row.acc == "public" {
			continue
		}
		method, path := substitute(t, rt.Pattern, sc.ghostIDs())
		for _, opts := range [][]ReqOpt{nil, {RawBody(`{garbage`, "application/json")}, {RawBody(`x`, "text/plain")}} {
			r := w.h.Do(t, method, path, nil, opts...)
			if r.Code != 401 || r.ErrCode(t) != "unauthorized" {
				t.Errorf("%s %s: got %d %q", method, path, r.Code, r.ErrCode(t))
			}
		}
	}
}

// TestDemotionTakesEffectImmediately: roles are evaluated per request, so a token minted while
// its owner was an editor loses write effect as soon as the owner is demoted, and a removed
// member's token is 404 at once. A read token of an owner never writes.
func TestDemotionTakesEffectImmediately(t *testing.T) {
	w := newMWorld(t)
	sc := w.scene(t)
	h := w.h
	ownerAct := service.UserActor(w.owner.User.ID)
	editorTok, _ := h.MkToken(t, w.editor, service.ScopeWrite, "")
	editorSess := w.fresh(t, w.editor)
	post := func(opts ...ReqOpt) *Resp {
		return h.Do(t, "POST", "/api/v1/projects/"+sc.proj.Key+"/tickets", map[string]any{"title": "t"}, opts...)
	}
	if r := post(Bearer(editorTok)); r.Code != 201 {
		t.Fatalf("editor token before demotion: %d %s", r.Code, r.Body)
	}
	if _, err := h.Env.Svc.Members.SetRole(context.Background(), ownerAct, sc.proj.Key, w.editor.User.ID, service.RoleViewer); err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string][]ReqOpt{"token": {Bearer(editorTok)}, "session": {As(editorSess)}} {
		r := post(opts...)
		if r.Code != 403 || r.ErrCode(t) != "forbidden" {
			t.Errorf("%s after demotion: %d %s", name, r.Code, r.Body)
		}
		if r := h.Do(t, "GET", "/api/v1/projects/"+sc.proj.Key+"/tickets", nil, opts...); r.Code != 200 {
			t.Errorf("%s may still read as a viewer: %d", name, r.Code)
		}
	}
	// and the other direction: promoted again, the same token writes again
	if _, err := h.Env.Svc.Members.SetRole(context.Background(), ownerAct, sc.proj.Key, w.editor.User.ID, service.RoleEditor); err != nil {
		t.Fatal(err)
	}
	if r := post(Bearer(editorTok)); r.Code != 201 {
		t.Errorf("after re-promotion: %d %s", r.Code, r.Body)
	}
	// removed member: 404 at once for the token and the session
	if err := h.Env.Svc.Members.Remove(context.Background(), ownerAct, sc.proj.Key, w.editor.User.ID); err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string][]ReqOpt{"token": {Bearer(editorTok)}, "session": {As(editorSess)}} {
		for _, path := range []string{"/api/v1/projects/" + sc.proj.Key, "/api/v1/tickets/" + sc.tkRef} {
			if r := h.Do(t, "GET", path, nil, opts...); r.Code != 404 {
				t.Errorf("removed member %s GET %s: %d", name, path, r.Code)
			}
		}
		if r := post(opts...); r.Code != 404 {
			t.Errorf("removed member %s POST: %d", name, r.Code)
		}
	}
	// a read token of an owner is viewer-level everywhere
	readTok, _ := h.MkToken(t, w.owner, service.ScopeRead, "")
	if r := h.Do(t, "GET", "/api/v1/projects/"+sc.proj.Key, nil, Bearer(readTok)); r.Code != 200 || r.JSON(t)["role"] != "viewer" {
		t.Errorf("read token project: %d %s", r.Code, r.Body)
	}
	if r := post(Bearer(readTok)); r.Code != 403 || r.ErrCode(t) != "insufficient_scope" {
		t.Errorf("read token write: %d %s", r.Code, r.Body)
	}
}

// TestOrderOfChecksInvalidForbiddenArchived sends input that is invalid, forbidden and archived
// at once and checks which error wins: credentials, scope, membership 404, role 403, archived
// 409, validation 422 (spec section 3). Validation errors never reach non-members or viewers.
func TestOrderOfChecksInvalidForbiddenArchived(t *testing.T) {
	type bad struct {
		pattern string
		body    any
	}
	longTitle := strings.Repeat("x", 201)
	cases := []bad{
		{"POST /api/v1/projects/{id}/tickets", map[string]any{"title": longTitle}},
		{"PATCH /api/v1/tickets/{id}", map[string]any{"title": longTitle}},
		{"POST /api/v1/tickets/{id}/move", map[string]any{"status": "nonsense"}},
		{"POST /api/v1/projects/{id}/labels", map[string]any{"name": "", "color": "mauve"}},
		{"PATCH /api/v1/labels/{id}", map[string]any{"name": strings.Repeat("l", 51)}},
		{"POST /api/v1/tickets/{id}/comments", map[string]any{"body": ""}},
	}
	for _, c := range cases {
		for _, archived := range []bool{false, true} {
			name := fmt.Sprintf("%s archived=%v", c.pattern, archived)
			t.Run(name, func(t *testing.T) {
				w := newMWorld(t)
				for _, p := range mprincs {
					if p.anon || p.lim == "other" {
						continue
					}
					sc := w.scene(t)
					if archived {
						w.h.Env.Archive(t, sc.proj.ID)
					}
					opts := p.opts(t, sc)
					method, path := substitute(t, c.pattern, sc.ids())
					r := w.h.Do(t, method, path, c.body, opts...)
					want := outcome{422, "validation_failed"}
					switch {
					case p.token && p.read:
						want = outcome{403, service.CodeInsufficientScope}
					case p.role == rNone:
						want = outcome{404, "not_found"}
					case p.role < rEditor:
						want = outcome{403, "forbidden"}
					case archived:
						want = outcome{409, "project_archived"}
					}
					if r.Code != want.code || r.ErrCode(t) != want.err {
						t.Errorf("%s: got %d %q (%s), want %s", p.name, r.Code, r.ErrCode(t), r.Body, want)
					}
				}
			})
		}
	}
}

// ---- track 2: non-member 404 ----

func ghostMismatch(t *testing.T, label string, real, ghost *Resp) {
	t.Helper()
	if real.Code != ghost.Code || !bytes.Equal(real.Body, ghost.Body) {
		t.Errorf("%s: hidden resource differs from a random ULID:\n real  %d %s\n ghost %d %s", label, real.Code, real.Body, ghost.Code, ghost.Body)
	}
}

// TestNonMemberIsByteIdenticalToRandomULID: for every project-scoped route, a non-member (session
// and token) and a member removed a moment ago get a response byte-identical to the one for a
// random valid ULID, and the session ones are 404. Rows come from the route table through the
// pattern substitution helper, so a new scoped route is covered the moment it has an mrows entry.
func TestNonMemberIsByteIdenticalToRandomULID(t *testing.T) {
	probe := setupWithHub(t)
	n := 0
	for _, rt := range apiRoutes(probe.Server) {
		row, ok := mrows[rt.Pattern]
		if !ok || !row.scoped {
			continue
		}
		t.Run(rt.Pattern, func(t *testing.T) {
			w := newMWorld(t)
			sc := w.scene(t)
			// the editor is removed, so every credential below is "no longer a member"
			if err := w.h.Env.Svc.Members.Remove(context.Background(), service.UserActor(w.owner.User.ID), sc.proj.Key, w.editor.User.ID); err != nil {
				t.Fatal(err)
			}
			outsiderTok, _ := w.h.MkToken(t, w.outsider, service.ScopeWrite, "")
			editorTok, _ := w.h.MkToken(t, w.editor, service.ScopeWrite, "")
			creds := []struct {
				name    string
				opts    []ReqOpt
				session bool
			}{
				{"non-member session", []ReqOpt{As(w.fresh(t, w.outsider))}, true},
				{"non-member token", []ReqOpt{Bearer(outsiderTok)}, false},
				{"removed member session", []ReqOpt{As(w.fresh(t, w.editor))}, true},
				{"removed member token", []ReqOpt{Bearer(editorTok)}, false},
			}
			for _, c := range creds {
				// fresh scene resources per credential: writes in the earlier iteration may have
				// deleted the seeded ticket or label
				real := row.send(t, sc, rt.Pattern, sc.ids(), c.opts)
				ghost := row.send(t, sc, rt.Pattern, sc.ghostIDs(), c.opts)
				ghostMismatch(t, c.name, real, ghost)
				if c.session && real.Code != 404 {
					t.Errorf("%s: want 404, got %d %s", c.name, real.Code, real.Body)
				}
				if !c.session && row.acc != "session" && real.Code != 404 {
					t.Errorf("%s: want 404, got %d %s", c.name, real.Code, real.Body)
				}
				n++
				// credentials that hit a destructive route cannot have changed anything
				if r := w.h.Do(t, "GET", "/api/v1/projects/"+sc.proj.Key+"/tickets", nil, As(w.fresh(t, w.owner))); r.Code != 200 {
					t.Fatalf("owner lost access: %d", r.Code)
				}
			}
			// a ticket reference of a hidden project looks like a missing reference too
			if strings.Contains(rt.Pattern, "/tickets/{id}") {
				ids := sc.ids()
				ghost := sc.ghostIDs()
				ghost["tickets"] = "NOPE-1"
				a := row.send(t, sc, rt.Pattern, ids, creds[0].opts)
				b := row.send(t, sc, rt.Pattern, ghost, creds[0].opts)
				ghostMismatch(t, "ticket ref", a, b)
			}
		})
	}
	if n == 0 {
		t.Fatal("no scoped routes exercised")
	}
}
