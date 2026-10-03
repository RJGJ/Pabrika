package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// ---- fixture shared by the project, member and label tests ----

// rfx is one project (WEB) with an owner, an editor, a viewer and a non-member, each with a
// session client, plus tokens for the interesting scope x limit combinations.
type rfx struct {
	h                              *Harness
	owner, editor, viewer, outside *Client
	projID                         string

	ownerWrite, ownerRead string // bearer secrets of the owner (all projects)
	editorWrite           string
	ownerLimited          string // write token limited to WEB
	ownerOtherLimited     string // write token limited to another project (OTH)
	ownerLimitedRead      string // read token limited to WEB
	outsiderWrite         string
	otherProjID           string
}

func newRfx(t testing.TB, o Opts) *rfx {
	t.Helper()
	h := Setup(t, o)
	f := &rfx{h: h}
	f.owner = h.Signup(t, "owner@x.io")
	f.editor = h.Signup(t, "editor@x.io")
	f.viewer = h.Signup(t, "viewer@x.io")
	f.outside = h.Signup(t, "outside@x.io")
	proj := h.Env.NewProject(t, tuser(f.owner), "WEB")
	f.projID = proj.ID
	h.Env.AddMember(t, proj.ID, f.editor.User.ID, service.RoleEditor)
	h.Env.AddMember(t, proj.ID, f.viewer.User.ID, service.RoleViewer)
	oth := h.Env.NewProject(t, tuser(f.owner), "OTH")
	f.otherProjID = oth.ID
	f.ownerWrite, _ = h.MkToken(t, f.owner, service.ScopeWrite, "")
	f.ownerRead, _ = h.MkToken(t, f.owner, service.ScopeRead, "")
	f.editorWrite, _ = h.MkToken(t, f.editor, service.ScopeWrite, "")
	f.ownerLimited, _ = h.MkToken(t, f.owner, service.ScopeWrite, proj.ID)
	f.ownerOtherLimited, _ = h.MkToken(t, f.owner, service.ScopeWrite, oth.ID)
	f.ownerLimitedRead, _ = h.MkToken(t, f.owner, service.ScopeRead, proj.ID)
	f.outsiderWrite, _ = h.MkToken(t, f.outside, service.ScopeWrite, "")
	return f
}

// who names the caller rows of the matrices.
type who struct {
	name string
	opts func(f *rfx) []ReqOpt
}

var (
	whoAnon      = who{"anonymous", func(f *rfx) []ReqOpt { return nil }}
	whoOutside   = who{"non-member", func(f *rfx) []ReqOpt { return []ReqOpt{As(f.outside)} }}
	whoViewer    = who{"viewer", func(f *rfx) []ReqOpt { return []ReqOpt{As(f.viewer)} }}
	whoEditor    = who{"editor", func(f *rfx) []ReqOpt { return []ReqOpt{As(f.editor)} }}
	whoOwner     = who{"owner", func(f *rfx) []ReqOpt { return []ReqOpt{As(f.owner)} }}
	whoEdTok     = who{"editor-write-token", func(f *rfx) []ReqOpt { return []ReqOpt{Bearer(f.editorWrite)} }}
	whoOwTok     = who{"owner-write-token", func(f *rfx) []ReqOpt { return []ReqOpt{Bearer(f.ownerWrite)} }}
	whoOwRead    = who{"owner-read-token", func(f *rfx) []ReqOpt { return []ReqOpt{Bearer(f.ownerRead)} }}
	whoOwLim     = who{"owner-limited-token", func(f *rfx) []ReqOpt { return []ReqOpt{Bearer(f.ownerLimited)} }}
	whoOwOthLim  = who{"owner-token-limited-elsewhere", func(f *rfx) []ReqOpt { return []ReqOpt{Bearer(f.ownerOtherLimited)} }}
	whoOwLimRead = who{"owner-limited-read-token", func(f *rfx) []ReqOpt { return []ReqOpt{Bearer(f.ownerLimitedRead)} }}
)

// rfxRow is one expected outcome of a matrix.
type rfxRow struct {
	who  who
	code int
	err  string // expected error.code ("" for success)
}

// runMatrix runs call against a FRESH fixture per row and checks status and error code.
func runMatrix(t *testing.T, name string, call func(f *rfx, o []ReqOpt) *Resp, rows []rfxRow) {
	t.Helper()
	for _, row := range rows {
		t.Run(name+"/"+row.who.name, func(t *testing.T) {
			f := newRfx(t, Opts{})
			resp := call(f, row.who.opts(f))
			if resp.Code != row.code || (row.err != "" && resp.ErrCode(t) != row.err) {
				t.Fatalf("got %d %q (%s), want %d %q", resp.Code, resp.ErrCode(t), resp.Body, row.code, row.err)
			}
		})
	}
}

func (f *rfx) do(t testing.TB, method, path string, body any, o ...ReqOpt) *Resp {
	t.Helper()
	return f.h.Do(t, method, path, body, o...)
}

// randULID is a well-formed id that exists nowhere.
func randULID(f *rfx) string { return f.h.Env.NewID() }

func items(t testing.TB, r *Resp) []map[string]any {
	t.Helper()
	m := r.JSON(t)
	raw, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items array: %s", r.Body)
	}
	out := make([]map[string]any, len(raw))
	for i, it := range raw {
		out[i] = it.(map[string]any)
	}
	return out
}

func sameBytes(t testing.TB, what string, a, b *Resp) {
	t.Helper()
	if a.Code != b.Code || !bytes.Equal(a.Body, b.Body) {
		t.Fatalf("%s differs:\n %d %s\n %d %s", what, a.Code, a.Body, b.Code, b.Body)
	}
}

const (
	pr401 = http.StatusUnauthorized
	pr403 = http.StatusForbidden
	pr404 = http.StatusNotFound
)

// ---- golden shapes ----

func TestProjectsGoldenShapes(t *testing.T) {
	f := newRfx(t, Opts{})
	projKeys := []string{"id", "key", "name", "description", "archived_at", "created_at", "updated_at", "role"}

	r := f.do(t, "POST", "/api/v1/projects", map[string]any{"key": "abc", "name": "Alpha", "description": "d"}, As(f.owner))
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	wantKeys(t, "create", r.JSON(t), projKeys...)

	r = f.do(t, "GET", "/api/v1/projects", nil, As(f.owner))
	env := r.JSON(t)
	wantKeys(t, "list envelope", env, "items", "next_cursor")
	if env["next_cursor"] != nil {
		t.Fatalf("next_cursor must be null")
	}
	for _, it := range items(t, r) {
		wantKeys(t, "list item", it, projKeys...)
	}

	r = f.do(t, "GET", "/api/v1/projects/WEB", nil, As(f.owner))
	m := r.JSON(t)
	wantKeys(t, "detail", m, append(projKeys, "counts")...)
	wantKeys(t, "counts", m["counts"].(map[string]any), "backlog", "todo", "in_progress", "done")

	r = f.do(t, "PATCH", "/api/v1/projects/WEB", map[string]any{"name": "Renamed"}, As(f.owner))
	wantKeys(t, "patch", r.JSON(t), projKeys...)
	if r.JSON(t)["role"] != "owner" || r.JSON(t)["name"] != "Renamed" {
		t.Fatalf("patch body: %s", r.Body)
	}
}

// ---- create ----

func TestProjectsCreate(t *testing.T) {
	f := newRfx(t, Opts{})
	r := f.do(t, "POST", "/api/v1/projects", map[string]any{"key": "abc", "name": "  Alpha  "}, As(f.outside))
	if r.Code != 201 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	m := r.JSON(t)
	if m["key"] != "ABC" || m["name"] != "Alpha" || m["description"] != "" || m["role"] != "owner" || m["archived_at"] != nil {
		t.Fatalf("body: %s", r.Body)
	}
	// The creator is an owner and can use it by key (any case) and by ULID.
	for _, ref := range []string{"ABC", "abc", m["id"].(string)} {
		if g := f.do(t, "GET", "/api/v1/projects/"+ref, nil, As(f.outside)); g.Code != 200 {
			t.Fatalf("GET by %q: %d %s", ref, g.Code, g.Body)
		}
	}
	// Duplicate key, case-insensitively.
	for _, key := range []string{"ABC", "abc", "WEB"} {
		d := f.do(t, "POST", "/api/v1/projects", map[string]any{"key": key, "name": "Dup"}, As(f.viewer))
		if d.Code != 409 || d.ErrCode(t) != "key_taken" {
			t.Fatalf("dup %s: %d %s", key, d.Code, d.Body)
		}
	}
	// A write token creates projects; the owner is the token's user.
	r = f.do(t, "POST", "/api/v1/projects", map[string]any{"key": "TOK", "name": "ByToken"}, Bearer(f.editorWrite))
	if r.Code != 201 {
		t.Fatalf("token create: %d %s", r.Code, r.Body)
	}
	if g := f.do(t, "GET", "/api/v1/projects/TOK/members", nil, As(f.editor)); g.Code != 200 || len(items(t, g)) != 1 {
		t.Fatalf("members: %d %s", g.Code, g.Body)
	}
}

func TestProjectsCreateValidation(t *testing.T) {
	f := newRfx(t, Opts{})
	long := func(n int) string { return string(bytes.Repeat([]byte("x"), n)) }
	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"key too short", map[string]any{"key": "A", "name": "N"}, "key"},
		{"key too long", map[string]any{"key": "ABCDEFG", "name": "N"}, "key"},
		{"key digits", map[string]any{"key": "AB1", "name": "N"}, "key"},
		{"key empty", map[string]any{"key": "", "name": "N"}, "key"},
		{"name empty", map[string]any{"key": "ABC", "name": "  "}, "name"},
		{"name missing", map[string]any{"key": "ABC"}, "name"},
		{"name 101", map[string]any{"key": "ABC", "name": long(101)}, "name"},
		{"description 2001", map[string]any{"key": "ABC", "name": "N", "description": long(2001)}, "description"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := f.do(t, "POST", "/api/v1/projects", c.body, As(f.owner))
			if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
				t.Fatalf("%d %s", r.Code, r.Body)
			}
			fields := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
			if _, ok := fields[c.field]; !ok {
				t.Fatalf("fields %v lack %q", fields, c.field)
			}
		})
	}
	// Boundaries are accepted.
	if r := f.do(t, "POST", "/api/v1/projects", map[string]any{"key": "ABCDEF", "name": long(100), "description": long(2000)}, As(f.owner)); r.Code != 201 {
		t.Fatalf("boundaries: %d %s", r.Code, r.Body)
	}
	// All field errors are reported together.
	r := f.do(t, "POST", "/api/v1/projects", map[string]any{"key": "1", "name": ""}, As(f.owner))
	fields := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
	if len(fields) != 2 {
		t.Fatalf("want key and name together: %v", fields)
	}
	// Body problems are 400/415, not 422.
	for name, o := range map[string]ReqOpt{
		"unknown field": RawBody(`{"key":"ZZZ","name":"N","nope":1}`, "application/json"),
		"malformed":     RawBody(`{"key":`, "application/json"),
		"empty body":    RawBody(``, "application/json"),
	} {
		if r := f.do(t, "POST", "/api/v1/projects", nil, As(f.owner), o); r.Code != 400 || r.ErrCode(t) != "bad_request" {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	if r := f.do(t, "POST", "/api/v1/projects", nil, As(f.owner), RawBody(`{"key":"ZZZ","name":"N"}`, "text/plain")); r.Code != 415 {
		t.Fatalf("text/plain: %d", r.Code)
	}
}

func TestProjectsCreateMatrix(t *testing.T) {
	body := map[string]any{"key": "NEW", "name": "New"}
	runMatrix(t, "create", func(f *rfx, o []ReqOpt) *Resp { return f.do(t, "POST", "/api/v1/projects", body, o...) }, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 201, ""},
		{whoViewer, 201, ""},
		{whoOwTok, 201, ""},
		{whoOwRead, 403, "insufficient_scope"},
		{whoOwLim, 403, "forbidden"},
		{whoOwLimRead, 403, "insufficient_scope"},
	})
}

// ---- list ----

func TestProjectsList(t *testing.T) {
	f := newRfx(t, Opts{})
	// The outsider sees nothing; members see their projects ordered by name then key.
	r := f.do(t, "GET", "/api/v1/projects", nil, As(f.outside))
	if r.Code != 200 || len(items(t, r)) != 0 {
		t.Fatalf("outsider: %d %s", r.Code, r.Body)
	}
	if string(r.Body) == "" || !bytes.Contains(r.Body, []byte(`"items":[]`)) {
		t.Fatalf("empty list must be [] not null: %s", r.Body)
	}
	f.do(t, "POST", "/api/v1/projects", map[string]any{"key": "AAA", "name": "Zeta"}, As(f.owner))
	f.do(t, "POST", "/api/v1/projects", map[string]any{"key": "BBB", "name": "alpha"}, As(f.owner))
	r = f.do(t, "GET", "/api/v1/projects", nil, As(f.owner))
	var got []string
	for _, it := range items(t, r) {
		got = append(got, it["key"].(string))
	}
	want := []string{"BBB", "OTH", "WEB", "AAA"} // alpha, Project OTH, Project WEB, Zeta (names are "Project KEY")
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order: got %v want %v", got, want)
	}
	// Roles are the caller's own.
	for _, c := range []struct {
		cl   *Client
		role string
	}{{f.owner, "owner"}, {f.editor, "editor"}, {f.viewer, "viewer"}} {
		r := f.do(t, "GET", "/api/v1/projects", nil, As(c.cl))
		role := ""
		for _, it := range items(t, r) {
			if it["key"] == "WEB" {
				role, _ = it["role"].(string)
			}
		}
		if role != c.role {
			t.Fatalf("%s: %s", c.role, r.Body)
		}
	}
}

func TestProjectsListArchivedFlag(t *testing.T) {
	f := newRfx(t, Opts{})
	f.h.Env.Archive(t, f.otherProjID)
	keys := func(path string, o ...ReqOpt) []string {
		r := f.do(t, "GET", path, nil, o...)
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body)
		}
		var ks []string
		for _, it := range items(t, r) {
			ks = append(ks, it["key"].(string))
		}
		sort.Strings(ks)
		return ks
	}
	if g := keys("/api/v1/projects", As(f.owner)); fmt.Sprint(g) != "[WEB]" {
		t.Fatalf("default hides archived: %v", g)
	}
	if g := keys("/api/v1/projects?archived=false", As(f.owner)); fmt.Sprint(g) != "[WEB]" {
		t.Fatalf("archived=false: %v", g)
	}
	if g := keys("/api/v1/projects?archived=true", As(f.owner)); fmt.Sprint(g) != "[OTH WEB]" {
		t.Fatalf("archived=true: %v", g)
	}
	r := f.do(t, "GET", "/api/v1/projects?archived=true", nil, As(f.owner))
	for _, it := range items(t, r) {
		if (it["key"] == "OTH") != (it["archived_at"] != nil) {
			t.Fatalf("archived_at: %v", it)
		}
	}
	for _, bad := range []string{"?archived=maybe", "?archived=true&archived=false", "?archived=1"} {
		r := f.do(t, "GET", "/api/v1/projects"+bad, nil, As(f.owner))
		if r.Code != 422 {
			t.Fatalf("%s: %d %s", bad, r.Code, r.Body)
		}
	}
}

func TestProjectsListTokens(t *testing.T) {
	f := newRfx(t, Opts{})
	keysFor := func(secret string) ([]string, []string) {
		r := f.do(t, "GET", "/api/v1/projects", nil, Bearer(secret))
		if r.Code != 200 {
			t.Fatalf("%d %s", r.Code, r.Body)
		}
		var ks, roles []string
		for _, it := range items(t, r) {
			ks = append(ks, it["key"].(string))
			roles = append(roles, it["role"].(string))
		}
		sort.Strings(ks)
		return ks, roles
	}
	if ks, roles := keysFor(f.ownerWrite); fmt.Sprint(ks) != "[OTH WEB]" || fmt.Sprint(roles) != "[owner owner]" {
		t.Fatalf("write: %v %v", ks, roles)
	}
	// A read token sees owner projects with the role capped to viewer.
	if ks, roles := keysFor(f.ownerRead); fmt.Sprint(ks) != "[OTH WEB]" || fmt.Sprint(roles) != "[viewer viewer]" {
		t.Fatalf("read: %v %v", ks, roles)
	}
	if ks, _ := keysFor(f.ownerLimited); fmt.Sprint(ks) != "[WEB]" {
		t.Fatalf("limited: %v", ks)
	}
	if ks, _ := keysFor(f.ownerOtherLimited); fmt.Sprint(ks) != "[OTH]" {
		t.Fatalf("limited elsewhere: %v", ks)
	}
	if r := f.do(t, "GET", "/api/v1/projects", nil); r.Code != 401 {
		t.Fatalf("anon: %d", r.Code)
	}
}

// ---- get ----

func TestProjectsGet(t *testing.T) {
	f := newRfx(t, Opts{})
	// Seed tickets so counts are visible, including a soft-deleted one that must not count.
	ctx := context.Background()
	a := service.UserActor(f.owner.User.ID)
	mk := func(status service.Status) service.Ticket {
		tk, err := f.h.Env.Svc.Tickets.Create(ctx, a, "WEB", service.CreateTicketInput{Title: "t", Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	mk(service.StatusTodo)
	mk(service.StatusTodo)
	mk(service.StatusDone)
	del := mk(service.StatusBacklog)
	if err := f.h.Env.Svc.Tickets.Delete(ctx, a, del.ID); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"WEB", "web", f.projID} {
		r := f.do(t, "GET", "/api/v1/projects/"+ref, nil, As(f.viewer))
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", ref, r.Code, r.Body)
		}
		m := r.JSON(t)
		if m["id"] != f.projID || m["key"] != "WEB" || m["role"] != "viewer" {
			t.Fatalf("%s: %s", ref, r.Body)
		}
		c := m["counts"].(map[string]any)
		if c["backlog"] != 0.0 || c["todo"] != 2.0 || c["in_progress"] != 0.0 || c["done"] != 1.0 {
			t.Fatalf("counts: %v", c)
		}
	}
	// Read-scope token: role capped to viewer.
	if r := f.do(t, "GET", "/api/v1/projects/WEB", nil, Bearer(f.ownerRead)); r.Code != 200 || r.JSON(t)["role"] != "viewer" {
		t.Fatalf("read token: %d %s", r.Code, r.Body)
	}
}

func TestProjectsGetHiddenIs404ByteIdentical(t *testing.T) {
	f := newRfx(t, Opts{})
	unknown := f.do(t, "GET", "/api/v1/projects/"+randULID(f), nil, As(f.outside))
	if unknown.Code != 404 || unknown.ErrCode(t) != "not_found" {
		t.Fatalf("unknown: %d %s", unknown.Code, unknown.Body)
	}
	for _, ref := range []string{"WEB", "web", f.projID, "ZZZ", "not-a-ulid!!"} {
		sameBytes(t, "GET "+ref, f.do(t, "GET", "/api/v1/projects/"+ref, nil, As(f.outside)), unknown)
	}
	// Tokens: outside the limit looks like a missing project.
	sameBytes(t, "limited elsewhere", f.do(t, "GET", "/api/v1/projects/WEB", nil, Bearer(f.ownerOtherLimited)), unknown)
	sameBytes(t, "outsider token", f.do(t, "GET", "/api/v1/projects/WEB", nil, Bearer(f.outsiderWrite)), unknown)
}

// ---- patch ----

func TestProjectsPatch(t *testing.T) {
	f := newRfx(t, Opts{})
	p := "/api/v1/projects/WEB"
	r := f.do(t, "PATCH", p, map[string]any{"name": " New name ", "description": "about"}, As(f.owner))
	if r.Code != 200 || r.JSON(t)["name"] != "New name" || r.JSON(t)["description"] != "about" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	before := f.do(t, "GET", p, nil, As(f.owner)).JSON(t)
	// {} is a 200 no-op and does not touch updated_at.
	r = f.do(t, "PATCH", p, map[string]any{}, As(f.owner))
	if r.Code != 200 || r.JSON(t)["updated_at"] != before["updated_at"] || r.JSON(t)["name"] != "New name" {
		t.Fatalf("no-op: %d %s", r.Code, r.Body)
	}
	// Empty description is allowed (clears it).
	r = f.do(t, "PATCH", p, map[string]any{"description": ""}, As(f.owner))
	if r.Code != 200 || r.JSON(t)["description"] != "" {
		t.Fatalf("clear description: %d %s", r.Code, r.Body)
	}
	// Archive, then edit and unarchive an archived project.
	r = f.do(t, "PATCH", p, map[string]any{"archived": true}, As(f.owner))
	if r.Code != 200 || r.JSON(t)["archived_at"] == nil {
		t.Fatalf("archive: %d %s", r.Code, r.Body)
	}
	first := r.JSON(t)["archived_at"]
	r = f.do(t, "PATCH", p, map[string]any{"archived": true}, As(f.owner))
	if r.Code != 200 || r.JSON(t)["archived_at"] != first {
		t.Fatalf("re-archive is a no-op: %s", r.Body)
	}
	if r = f.do(t, "PATCH", p, map[string]any{"name": "While archived"}, As(f.owner)); r.Code != 200 {
		t.Fatalf("rename archived: %d %s", r.Code, r.Body)
	}
	if r = f.do(t, "PATCH", p, map[string]any{"archived": false}, As(f.owner)); r.Code != 200 || r.JSON(t)["archived_at"] != nil {
		t.Fatalf("unarchive: %d %s", r.Code, r.Body)
	}
	// By ULID and by write token and limited token.
	if r = f.do(t, "PATCH", "/api/v1/projects/"+f.projID, map[string]any{"name": "ByID"}, As(f.owner)); r.Code != 200 {
		t.Fatalf("by id: %d", r.Code)
	}
	if r = f.do(t, "PATCH", p, map[string]any{"name": "ByTok"}, Bearer(f.ownerLimited)); r.Code != 200 {
		t.Fatalf("by limited token: %d %s", r.Code, r.Body)
	}
}

func TestProjectsPatchValidation(t *testing.T) {
	f := newRfx(t, Opts{})
	p := "/api/v1/projects/WEB"
	long := string(bytes.Repeat([]byte("x"), 101))
	for name, body := range map[string]string{
		"name null":        `{"name":null}`,
		"description null": `{"description":null}`,
		"archived null":    `{"archived":null}`,
		"name empty":       `{"name":""}`,
		"name blank":       `{"name":"   "}`,
		"name 101":         `{"name":"` + long + `"}`,
	} {
		r := f.do(t, "PATCH", p, nil, As(f.owner), RawBody(body, "application/json"))
		if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	for name, body := range map[string]string{
		"key immutable": `{"key":"NEW"}`,
		"unknown":       `{"nope":1}`,
		"wrong type":    `{"archived":"yes"}`,
		"array":         `[]`,
		"trailing":      `{} {}`,
	} {
		r := f.do(t, "PATCH", p, nil, As(f.owner), RawBody(body, "application/json"))
		if r.Code != 400 || r.ErrCode(t) != "bad_request" {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	if r := f.do(t, "PATCH", p, nil, As(f.owner)); r.Code != 400 {
		t.Fatalf("no body: %d", r.Code)
	}
	// Validation does not leak: a non-member with a bad body still gets the body error (400),
	// but with a valid body gets 404 (role/membership precede validation).
	r := f.do(t, "PATCH", p, map[string]any{"name": ""}, As(f.outside))
	if r.Code != 404 {
		t.Fatalf("outsider invalid name: %d %s", r.Code, r.Body)
	}
	r = f.do(t, "PATCH", p, map[string]any{"name": ""}, As(f.editor))
	if r.Code != 403 {
		t.Fatalf("editor invalid name: %d %s", r.Code, r.Body)
	}
}

func TestProjectsPatchMatrix(t *testing.T) {
	body := map[string]any{"name": "Renamed"}
	call := func(f *rfx, o []ReqOpt) *Resp { return f.do(t, "PATCH", "/api/v1/projects/WEB", body, o...) }
	runMatrix(t, "patch", call, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 404, "not_found"},
		{whoViewer, 403, "forbidden"},
		{whoEditor, 403, "forbidden"},
		{whoOwner, 200, ""},
		{whoEdTok, 403, "forbidden"},
		{whoOwTok, 200, ""},
		{whoOwRead, 403, "insufficient_scope"},
		{whoOwLim, 200, ""},
		{whoOwLimRead, 403, "insufficient_scope"},
		{whoOwOthLim, 404, "not_found"},
	})
}

// ---- delete ----

func TestProjectsDelete(t *testing.T) {
	f := newRfx(t, Opts{})
	ctx := context.Background()
	a := service.UserActor(f.owner.User.ID)
	if _, err := f.h.Env.Svc.Labels.Create(ctx, a, "WEB", service.LabelInput{Name: "bug"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.Env.Svc.Tickets.Create(ctx, a, "WEB", service.CreateTicketInput{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	r := f.do(t, "DELETE", "/api/v1/projects/web", nil, As(f.owner))
	if r.Code != 204 || len(r.Body) != 0 {
		t.Fatalf("%d %q", r.Code, r.Body)
	}
	for _, c := range []*Client{f.owner, f.editor, f.viewer} {
		if g := f.do(t, "GET", "/api/v1/projects/WEB", nil, As(c)); g.Code != 404 {
			t.Fatalf("after delete: %d", g.Code)
		}
	}
	// Everything under it is gone (cascade), and the other project is untouched.
	var n int
	if err := f.h.Env.Store.RawQueryRow(ctx, "SELECT (SELECT COUNT(*) FROM labels WHERE project_id=?) + (SELECT COUNT(*) FROM tickets WHERE project_id=?) + (SELECT COUNT(*) FROM project_members WHERE project_id=?)",
		f.projID, f.projID, f.projID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("cascade left %d rows", n)
	}
	if g := f.do(t, "GET", "/api/v1/projects/OTH", nil, As(f.owner)); g.Code != 200 {
		t.Fatalf("OTH: %d", g.Code)
	}
	// A second delete is a 404; the key can be reused.
	if r = f.do(t, "DELETE", "/api/v1/projects/WEB", nil, As(f.owner)); r.Code != 404 {
		t.Fatalf("second delete: %d", r.Code)
	}
	if r = f.do(t, "POST", "/api/v1/projects", map[string]any{"key": "WEB", "name": "Again"}, As(f.owner)); r.Code != 201 {
		t.Fatalf("reuse key: %d %s", r.Code, r.Body)
	}
}

func TestProjectsDeleteArchivedAndByID(t *testing.T) {
	f := newRfx(t, Opts{})
	f.h.Env.Archive(t, f.projID)
	if r := f.do(t, "DELETE", "/api/v1/projects/"+f.projID, nil, As(f.owner)); r.Code != 204 {
		t.Fatalf("archived delete by id: %d %s", r.Code, r.Body)
	}
}

func TestProjectsDeleteMatrix(t *testing.T) {
	call := func(f *rfx, o []ReqOpt) *Resp { return f.do(t, "DELETE", "/api/v1/projects/WEB", nil, o...) }
	runMatrix(t, "delete", call, []rfxRow{
		{whoAnon, 401, "unauthorized"},
		{whoOutside, 404, "not_found"},
		{whoViewer, 403, "forbidden"},
		{whoEditor, 403, "forbidden"},
		{whoOwner, 204, ""},
		// Tokens never delete, whatever their scope or limit, and the answer does not depend on
		// the project (guard step 3 precedes membership).
		{whoEdTok, 403, "session_required"},
		{whoOwTok, 403, "session_required"},
		{whoOwRead, 403, "session_required"},
		{whoOwLim, 403, "session_required"},
		{whoOwOthLim, 403, "session_required"},
	})
	// The token-denial for an existing and a random project is byte-identical.
	f := newRfx(t, Opts{})
	sameBytes(t, "token delete", f.do(t, "DELETE", "/api/v1/projects/WEB", nil, Bearer(f.ownerWrite)),
		f.do(t, "DELETE", "/api/v1/projects/"+randULID(f), nil, Bearer(f.ownerWrite)))
	if g := f.do(t, "GET", "/api/v1/projects/WEB", nil, As(f.owner)); g.Code != 200 {
		t.Fatalf("project must survive a token delete: %d", g.Code)
	}
}

func TestProjectsHiddenWritesIdenticalToRandomULID(t *testing.T) {
	f := newRfx(t, Opts{})
	for _, c := range []struct {
		method, suffix string
		body           any
	}{
		{"PATCH", "", map[string]any{"name": "X"}},
		{"DELETE", "", nil},
	} {
		hidden := f.do(t, c.method, "/api/v1/projects/WEB"+c.suffix, c.body, As(f.outside))
		rnd := f.do(t, c.method, "/api/v1/projects/"+randULID(f)+c.suffix, c.body, As(f.outside))
		if hidden.Code != 404 {
			t.Fatalf("%s: %d %s", c.method, hidden.Code, hidden.Body)
		}
		sameBytes(t, c.method, hidden, rnd)
	}
}

func TestProjectsMethodNotAllowed(t *testing.T) {
	f := newRfx(t, Opts{})
	r := f.do(t, "PUT", "/api/v1/projects/WEB", map[string]any{}, As(f.owner))
	if r.Code != 405 || r.Header.Get("Allow") == "" {
		t.Fatalf("%d %v", r.Code, r.Header)
	}
}

// tuser converts a client user to the seeding type of testutil.
func tuser(c *Client) testutil.User {
	return testutil.User{ID: c.User.ID, Email: c.User.Email, DisplayName: c.User.DisplayName}
}
