package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// ---- fixture ----

// tkWorld is one project (WEB) with an owner, an editor, a viewer and an outsider (a user with
// their own project OTH), plus a second project owned by the owner for token-limit tests.
type tkWorld struct {
	h                                *Harness
	owner, editor, viewer, outsider  *Client
	proj, other                      testutil.Project
	ownerTok, readTok, limOwn, limOt string // token secrets (owner's tokens)
	outsiderTok                      string // write token of the outsider
}

func tkSetup(t *testing.T) *tkWorld {
	t.Helper()
	h := Setup(t, Opts{})
	w := &tkWorld{h: h}
	w.owner = h.Signup(t, "owner@x.io")
	w.editor = h.Signup(t, "editor@x.io")
	w.viewer = h.Signup(t, "viewer@x.io")
	w.outsider = h.Signup(t, "outsider@x.io")
	tu := func(c *Client) testutil.User {
		return testutil.User{ID: c.User.ID, Email: c.User.Email, DisplayName: c.User.DisplayName}
	}
	w.proj = h.Env.NewProject(t, tu(w.owner), "WEB")
	w.other = h.Env.NewProject(t, tu(w.owner), "OTH")
	h.Env.NewProject(t, tu(w.outsider), "OUT")
	h.Env.AddMember(t, w.proj.ID, w.editor.User.ID, service.RoleEditor)
	h.Env.AddMember(t, w.proj.ID, w.viewer.User.ID, service.RoleViewer)
	w.ownerTok, _ = h.MkToken(t, w.owner, service.ScopeWrite, "")
	w.readTok, _ = h.MkToken(t, w.owner, service.ScopeRead, "")
	w.limOwn, _ = h.MkToken(t, w.owner, service.ScopeWrite, w.proj.ID)
	w.limOt, _ = h.MkToken(t, w.owner, service.ScopeWrite, w.other.ID)
	w.outsiderTok, _ = h.MkToken(t, w.outsider, service.ScopeWrite, "")
	return w
}

// mk creates a ticket as the owner and returns its JSON.
func (w *tkWorld) mk(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	r := w.h.Do(t, "POST", "/api/v1/projects/WEB/tickets", body, As(w.owner))
	if r.Code != 201 {
		t.Fatalf("create ticket: %d %s", r.Code, r.Body)
	}
	return r.JSON(t)
}

func (w *tkWorld) label(t *testing.T, project, name string) string {
	t.Helper()
	l, err := w.h.Env.Svc.Labels.Create(context.Background(), service.UserActor(w.owner.User.ID), project, service.LabelInput{Name: name, Color: "red"})
	if err != nil {
		t.Fatal(err)
	}
	return l.ID
}

func tkItems(t *testing.T, r *Resp) []map[string]any {
	t.Helper()
	if r.Code != 200 {
		t.Fatalf("list: %d %s", r.Code, r.Body)
	}
	raw, ok := r.JSON(t)["items"].([]any)
	if !ok {
		t.Fatalf("no items array: %s", r.Body)
	}
	out := make([]map[string]any, len(raw))
	for i, v := range raw {
		out[i] = v.(map[string]any)
	}
	return out
}

func tkRefs(items []map[string]any) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it["ref"].(string)
	}
	return out
}

func tkEq(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func tkFieldErr(t *testing.T, r *Resp, field string) {
	t.Helper()
	if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
		t.Fatalf("want 422 validation_failed, got %d %s", r.Code, r.Body)
	}
	fields, _ := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
	if _, ok := fields[field]; !ok {
		t.Fatalf("want fields.%s, got %s", field, r.Body)
	}
}

func tkWant(t *testing.T, r *Resp, code int, errCode string) {
	t.Helper()
	if r.Code != code || r.ErrCode(t) != errCode {
		t.Fatalf("want %d %q, got %d %s", code, errCode, r.Code, r.Body)
	}
}

// ---- golden shapes ----

var (
	tkFullKeys = []string{"id", "ref", "project_id", "project_key", "number", "title", "description", "status",
		"priority", "assignee", "labels", "position", "due_date", "comment_count", "created_at", "updated_at"}
	tkListKeys = []string{"id", "ref", "project_id", "project_key", "number", "title", "status",
		"priority", "assignee", "labels", "position", "due_date", "comment_count", "created_at", "updated_at"}
)

func TestTickets_Shapes(t *testing.T) {
	w := tkSetup(t)
	lid := w.label(t, "WEB", "bug")
	r := w.h.Do(t, "POST", "/api/v1/projects/WEB/tickets", map[string]any{
		"title": "First", "description": "desc", "priority": "high", "due_date": "2030-01-02",
		"assignee": w.editor.User.ID, "labels": []string{lid},
	}, As(w.owner))
	if r.Code != 201 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	tk := r.JSON(t)
	wantKeys(t, "create", tk, tkFullKeys...)
	if tk["ref"] != "WEB-1" || tk["project_key"] != "WEB" || tk["status"] != "todo" || tk["description"] != "desc" ||
		tk["due_date"] != "2030-01-02" || tk["comment_count"].(float64) != 0 {
		t.Errorf("unexpected ticket %v", tk)
	}
	wantKeys(t, "assignee", tk["assignee"].(map[string]any), "id", "display_name", "email")
	wantKeys(t, "label", tk["labels"].([]any)[0].(map[string]any), "id", "project_id", "name", "color")
	for k, typ := range map[string]string{"id": "string", "number": "number", "position": "number", "labels": "array", "assignee": "object"} {
		wantType(t, "create", tk, k, typ)
	}

	// empty description is still present on the full shape; null ones are explicit nulls
	bare := w.mk(t, map[string]any{"title": "bare"})
	wantKeys(t, "bare", bare, tkFullKeys...)
	if d, ok := bare["description"]; !ok || d != "" {
		t.Errorf("description should be empty string, got %v", d)
	}
	wantType(t, "bare", bare, "assignee", "null")
	wantType(t, "bare", bare, "due_date", "null")
	if len(bare["labels"].([]any)) != 0 {
		t.Errorf("labels should be []")
	}

	// GET, PATCH have the identical full shape
	g := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1", nil, As(w.viewer))
	wantKeys(t, "get", g.JSON(t), tkFullKeys...)
	p := w.h.Do(t, "PATCH", "/api/v1/tickets/WEB-1", map[string]any{"title": "renamed"}, As(w.editor))
	wantKeys(t, "patch", p.JSON(t), tkFullKeys...)

	// move adds renumbered
	m := w.h.Do(t, "POST", "/api/v1/tickets/WEB-1/move", map[string]any{"status": "done"}, As(w.editor))
	if m.Code != 200 {
		t.Fatalf("%d %s", m.Code, m.Body)
	}
	mj := m.JSON(t)
	wantKeys(t, "move", mj, append([]string{"renumbered"}, tkFullKeys...)...)
	wantType(t, "move", mj, "renumbered", "bool")

	// list items omit description; envelope keys
	l := w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets", nil, As(w.viewer))
	wantKeys(t, "list", l.JSON(t), "items", "next_cursor")
	wantType(t, "list", l.JSON(t), "next_cursor", "null")
	for _, it := range tkItems(t, l) {
		wantKeys(t, "list item", it, tkListKeys...)
	}

	// activity items
	a := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/activity", nil, As(w.viewer))
	items := tkItems(t, a)
	if len(items) < 3 {
		t.Fatalf("want created, updated and moved activity, got %d", len(items))
	}
	wantKeys(t, "activity list", a.JSON(t), "items", "next_cursor")
	for _, it := range items {
		wantKeys(t, "activity", it, "id", "ticket_id", "actor", "action", "changes", "created_at")
		wantKeys(t, "activity.actor", it["actor"].(map[string]any), "id", "type", "name", "bot")
	}
	if items[0]["action"] != "moved" || items[len(items)-1]["action"] != "created" {
		t.Errorf("activity should be newest first: %v", items)
	}
}

// ---- create / get / patch / delete ----

func TestTickets_RefsAndGet(t *testing.T) {
	w := tkSetup(t)
	tk := w.mk(t, map[string]any{"title": "A"})
	id := tk["id"].(string)
	for _, ref := range []string{id, "WEB-1", "web-1"} {
		r := w.h.Do(t, "GET", "/api/v1/tickets/"+ref, nil, As(w.viewer))
		if r.Code != 200 || r.JSON(t)["id"] != id {
			t.Errorf("GET %s: %d %s", ref, r.Code, r.Body)
		}
	}
	// by project ULID too
	if r := w.h.Do(t, "GET", "/api/v1/projects/"+w.proj.ID+"/tickets", nil, As(w.viewer)); len(tkItems(t, r)) != 1 {
		t.Errorf("list by project ULID: %s", r.Body)
	}
	tkWant(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-99", nil, As(w.viewer)), 404, "not_found")
}

func TestTickets_CreateValidation(t *testing.T) {
	w := tkSetup(t)
	otherLabel := w.label(t, "OTH", "x")
	cases := []struct {
		name  string
		body  any
		field string
	}{
		{"empty title", map[string]any{"title": "  "}, "title"},
		{"missing title", map[string]any{}, "title"},
		{"long title", map[string]any{"title": strings.Repeat("a", 201)}, "title"},
		{"long description", map[string]any{"title": "a", "description": strings.Repeat("a", 20001)}, "description"},
		{"bad status", map[string]any{"title": "a", "status": "nope"}, "status"},
		{"bad priority", map[string]any{"title": "a", "priority": "nope"}, "priority"},
		{"bad due date", map[string]any{"title": "a", "due_date": "2030-13-45"}, "due_date"},
		{"assignee not member", map[string]any{"title": "a", "assignee": w.outsider.User.ID}, "assignee"},
		{"label of other project", map[string]any{"title": "a", "labels": []string{otherLabel}}, "labels"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tkFieldErr(t, w.h.Do(t, "POST", "/api/v1/projects/WEB/tickets", c.body, As(w.owner)), c.field)
		})
	}
	bad := []struct {
		name string
		body any
		opts []ReqOpt
		want int
	}{
		{"unknown field", map[string]any{"title": "a", "bogus": 1}, nil, 400},
		{"wrong type title", map[string]any{"title": 5}, nil, 400},
		{"wrong type labels", map[string]any{"title": "a", "labels": "x"}, nil, 400},
		{"malformed", `{"title":`, nil, 400},
		{"array body", `[]`, nil, 400},
		{"empty body", nil, nil, 400},
		{"trailing", `{"title":"a"} {}`, nil, 400},
		{"not json", "x", []ReqOpt{RawBody("title=a", "text/plain")}, 415},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			r := w.h.Do(t, "POST", "/api/v1/projects/WEB/tickets", c.body, append([]ReqOpt{As(w.owner)}, c.opts...)...)
			if r.Code != c.want {
				t.Fatalf("want %d got %d %s", c.want, r.Code, r.Body)
			}
		})
	}
	// nothing was created
	if r := w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets", nil, As(w.owner)); len(tkItems(t, r)) != 0 {
		t.Errorf("failed creates left tickets behind: %s", r.Body)
	}
}

func TestTickets_Patch(t *testing.T) {
	w := tkSetup(t)
	lid := w.label(t, "WEB", "l1")
	tk := w.mk(t, map[string]any{"title": "A", "assignee": w.editor.User.ID, "due_date": "2030-01-01", "labels": []string{lid}})
	id := tk["id"].(string)
	path := "/api/v1/tickets/" + id

	r := w.h.Do(t, "PATCH", path, map[string]any{"title": "B", "description": "d", "priority": "urgent"}, As(w.editor))
	j := r.JSON(t)
	if r.Code != 200 || j["title"] != "B" || j["description"] != "d" || j["priority"] != "urgent" {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	// null clears due_date and assignee; labels replaced with empty set
	r = w.h.Do(t, "PATCH", path, `{"due_date":null,"assignee":null,"labels":[]}`, As(w.editor))
	j = r.JSON(t)
	if r.Code != 200 || j["due_date"] != nil || j["assignee"] != nil || len(j["labels"].([]any)) != 0 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	// empty object is a no-op: 200, no new activity
	before := len(tkItems(t, w.h.Do(t, "GET", path+"/activity", nil, As(w.owner))))
	r = w.h.Do(t, "PATCH", path, map[string]any{}, As(w.editor))
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if after := len(tkItems(t, w.h.Do(t, "GET", path+"/activity", nil, As(w.owner)))); after != before {
		t.Errorf("no-op patch wrote activity: %d -> %d", before, after)
	}

	// status and position are not patchable
	for _, f := range []string{"status", "position"} {
		r = w.h.Do(t, "PATCH", path, map[string]any{f: "done"}, As(w.editor))
		tkWant(t, r, 400, "bad_request")
		if msg := r.JSON(t)["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "use /move") {
			t.Errorf("%s message %q should say 'use /move'", f, msg)
		}
	}
	tkWant(t, w.h.Do(t, "PATCH", path, map[string]any{"bogus": 1}, As(w.editor)), 400, "bad_request")
	tkWant(t, w.h.Do(t, "PATCH", path, map[string]any{"title": 5}, As(w.editor)), 400, "bad_request")
	tkWant(t, w.h.Do(t, "PATCH", path, map[string]any{"labels": "x"}, As(w.editor)), 400, "bad_request")
	tkFieldErr(t, w.h.Do(t, "PATCH", path, `{"title":null}`, As(w.editor)), "title")
	tkFieldErr(t, w.h.Do(t, "PATCH", path, map[string]any{"title": ""}, As(w.editor)), "title")
	tkFieldErr(t, w.h.Do(t, "PATCH", path, map[string]any{"priority": "x"}, As(w.editor)), "priority")
	tkFieldErr(t, w.h.Do(t, "PATCH", path, map[string]any{"assignee": w.outsider.User.ID}, As(w.editor)), "assignee")
}

func TestTickets_Delete(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	r := w.h.Do(t, "DELETE", "/api/v1/tickets/WEB-1", nil, As(w.editor))
	if r.Code != 204 || len(r.Body) != 0 {
		t.Fatalf("%d %q", r.Code, r.Body)
	}
	tkWant(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-1", nil, As(w.owner)), 404, "not_found")
	tkWant(t, w.h.Do(t, "DELETE", "/api/v1/tickets/WEB-1", nil, As(w.owner)), 404, "not_found")
	if r := w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets", nil, As(w.owner)); len(tkItems(t, r)) != 0 {
		t.Errorf("soft-deleted ticket listed: %s", r.Body)
	}
}

// ---- list: filters and pagination ----

func TestTickets_ListFiltersAndOrder(t *testing.T) {
	w := tkSetup(t)
	lid := w.label(t, "WEB", "bug")
	w.mk(t, map[string]any{"title": "Alpha thing", "status": "backlog", "priority": "low"})                                          // 1
	w.mk(t, map[string]any{"title": "Beta", "description": "mentions ZEBRA here", "priority": "high", "assignee": w.editor.User.ID}) // 2 todo
	w.mk(t, map[string]any{"title": "Gamma 100%", "status": "in_progress", "labels": []string{lid}})                                 // 3
	w.mk(t, map[string]any{"title": "Delta", "status": "done", "assignee": w.owner.User.ID})                                         // 4
	w.mk(t, map[string]any{"title": "Epsilon", "status": "todo"})                                                                    // 5

	get := func(c *Client, query string) *Resp {
		return w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets"+query, nil, As(c))
	}
	// default order: status then position
	if got := tkRefs(tkItems(t, get(w.viewer, ""))); !tkEq(got, []string{"WEB-1", "WEB-2", "WEB-5", "WEB-3", "WEB-4"}) {
		t.Errorf("order %v", got)
	}
	cases := []struct {
		query string
		want  []string
	}{
		{"?status=todo", []string{"WEB-2", "WEB-5"}},
		{"?priority=high", []string{"WEB-2"}},
		{"?assignee=none", []string{"WEB-1", "WEB-5", "WEB-3"}},
		{"?assignee=" + w.editor.User.ID, []string{"WEB-2"}},
		{"?label=" + lid, []string{"WEB-3"}},
		{"?q=alpha", []string{"WEB-1"}},
		{"?q=zebra", []string{"WEB-2"}}, // matches description, case-insensitive
		{"?q=100%25", []string{"WEB-3"}},
		{"?q=%25", []string{"WEB-3"}}, // % is literal, not a wildcard
		{"?q=nomatch", nil},
		{"?status=todo&priority=high", []string{"WEB-2"}},
		{"?assignee=01HZZZZZZZZZZZZZZZZZZZZZZZ", nil}, // unknown id: empty, not an error
		{"?label=01HZZZZZZZZZZZZZZZZZZZZZZZ", nil},
		{"?status=", []string{"WEB-1", "WEB-2", "WEB-5", "WEB-3", "WEB-4"}},
	}
	for _, c := range cases {
		if got := tkRefs(tkItems(t, get(w.viewer, c.query))); !tkEq(got, c.want) {
			t.Errorf("%s: got %v want %v", c.query, got, c.want)
		}
	}
	// `me` means the principal's owning user, also for a token
	if got := tkRefs(tkItems(t, w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets?assignee=me", nil, Bearer(w.ownerTok)))); !tkEq(got, []string{"WEB-4"}) {
		t.Errorf("token me: %v", got)
	}
	if got := tkRefs(tkItems(t, get(w.owner, "?assignee=me"))); !tkEq(got, []string{"WEB-4"}) {
		t.Errorf("owner me: %v", got)
	}
	if got := tkRefs(tkItems(t, get(w.editor, "?assignee=me"))); !tkEq(got, []string{"WEB-2"}) {
		t.Errorf("editor me: %v", got)
	}
	// assignee of another project's ticket never errors
	if r := get(w.viewer, "?assignee="+w.outsider.User.ID); r.Code != 200 {
		t.Errorf("foreign assignee: %d", r.Code)
	}
	// list items carry comment_count and assignee objects
	w.h.Do(t, "POST", "/api/v1/tickets/WEB-2/comments", map[string]any{"body": "hi"}, As(w.owner))
	for _, it := range tkItems(t, get(w.viewer, "?status=todo")) {
		if it["ref"] == "WEB-2" {
			if it["comment_count"].(float64) != 1 || it["assignee"].(map[string]any)["id"] != w.editor.User.ID {
				t.Errorf("list item hydrate: %v", it)
			}
		}
	}
}

func TestTickets_ListQueryValidation(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	base := "/api/v1/projects/WEB/tickets"
	cases := []struct{ query, field string }{
		{"?status=nope", "status"},
		{"?priority=nope", "priority"},
		{"?status=todo&status=done", "status"},
		{"?priority=low&priority=high", "priority"},
		{"?assignee=me&assignee=none", "assignee"},
		{"?label=a&label=b", "label"},
		{"?q=a&q=b", "q"},
		{"?q=" + strings.Repeat("a", 201), "q"},
		{"?limit=0", "limit"},
		{"?limit=-3", "limit"},
		{"?limit=abc", "limit"},
		{"?limit=1&limit=2", "limit"},
	}
	for _, c := range cases {
		tkFieldErr(t, w.h.Do(t, "GET", base+c.query, nil, As(w.owner)), c.field)
	}
	// several problems are reported together
	r := w.h.Do(t, "GET", base+"?status=x&priority=y&limit=0", nil, As(w.owner))
	f := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
	if len(f) != 3 {
		t.Errorf("want 3 fields, got %v", f)
	}
	// 200 chars is fine; limit above the max is clamped, not rejected
	if r := w.h.Do(t, "GET", base+"?q="+strings.Repeat("a", 200)+"&limit=100000", nil, As(w.owner)); r.Code != 200 {
		t.Errorf("limit clamp / 200-char q: %d %s", r.Code, r.Body)
	}
	tkWant(t, w.h.Do(t, "GET", base+"?cursor=%25%25garbage", nil, As(w.owner)), 400, "invalid_cursor")
	tkWant(t, w.h.Do(t, "GET", base+"?cursor=bm90LWEtY3Vyc29y", nil, As(w.owner)), 400, "invalid_cursor")
	// empty cursor is the first page
	if r := w.h.Do(t, "GET", base+"?cursor=", nil, As(w.owner)); r.Code != 200 || len(tkItems(t, r)) != 1 {
		t.Errorf("empty cursor: %d %s", r.Code, r.Body)
	}
	// validation errors for an outsider are still 404-free of project info only after membership:
	// a non-member with valid params gets 404
	tkWant(t, w.h.Do(t, "GET", base+"?status=todo", nil, As(w.outsider)), 404, "not_found")
}

func TestTickets_Pagination(t *testing.T) {
	w := tkSetup(t)
	var want []string
	for i := 1; i <= 7; i++ {
		st := []string{"todo", "backlog", "done"}[i%3]
		w.mk(t, map[string]any{"title": fmt.Sprintf("T%d", i), "status": st})
	}
	all := tkRefs(tkItems(t, w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets", nil, As(w.owner))))
	if len(all) != 7 {
		t.Fatalf("want 7, got %v", all)
	}
	var cursor *string
	pages := 0
	for {
		q := "?limit=3"
		if cursor != nil {
			q += "&cursor=" + url.QueryEscape(*cursor)
		}
		r := w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets"+q, nil, As(w.owner))
		items := tkItems(t, r)
		want = append(want, tkRefs(items)...)
		pages++
		nc, _ := r.JSON(t)["next_cursor"].(string)
		if nc == "" {
			if len(items) == 0 || pages != 3 {
				t.Fatalf("last page: pages=%d items=%d", pages, len(items))
			}
			break
		}
		if len(items) != 3 {
			t.Fatalf("full page expected, got %d", len(items))
		}
		cursor = &nc
		if pages > 5 {
			t.Fatal("pagination does not terminate")
		}
	}
	if !tkEq(want, all) {
		t.Errorf("paged %v != unpaged %v", want, all)
	}
	// exactly one full page: no next cursor
	r := w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets?limit=7", nil, As(w.owner))
	if r.JSON(t)["next_cursor"] != nil {
		t.Errorf("limit == count should end the list: %s", r.Body)
	}
	// a cursor reused with different filters must not error
	r = w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets?limit=2", nil, As(w.owner))
	nc := r.JSON(t)["next_cursor"].(string)
	if r := w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets?status=done&cursor="+url.QueryEscape(nc), nil, As(w.owner)); r.Code != 200 {
		t.Errorf("cursor with other filter: %d %s", r.Code, r.Body)
	}
}

func TestTickets_ActivityPagination(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	for i := 0; i < 4; i++ {
		w.h.Do(t, "PATCH", "/api/v1/tickets/WEB-1", map[string]any{"title": fmt.Sprintf("v%d", i)}, As(w.editor))
		w.h.Env.Clock.Advance(1_000_000_000)
	}
	var seen []string
	cursor := ""
	for i := 0; i < 6; i++ {
		r := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/activity?limit=2&cursor="+url.QueryEscape(cursor), nil, As(w.viewer))
		for _, it := range tkItems(t, r) {
			seen = append(seen, it["id"].(string))
		}
		nc, _ := r.JSON(t)["next_cursor"].(string)
		if nc == "" {
			break
		}
		cursor = nc
	}
	if len(seen) != 5 {
		t.Errorf("want 5 activity rows across pages, got %d", len(seen))
	}
	tkFieldErr(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/activity?limit=0", nil, As(w.viewer)), "limit")
	tkWant(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/activity?cursor=%25%25x", nil, As(w.viewer)), 400, "invalid_cursor")
	// token actors render as bots with an owner
	w.h.Do(t, "PATCH", "/api/v1/tickets/WEB-1", map[string]any{"title": "by token"}, Bearer(w.ownerTok))
	first := tkItems(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/activity", nil, As(w.viewer)))[0]
	actor := first["actor"].(map[string]any)
	if actor["type"] != "api_token" || actor["bot"] != true || actor["owner_name"] != "Owner" {
		t.Errorf("token actor: %v", actor)
	}
	wantKeys(t, "token actor", actor, "id", "type", "name", "bot", "owner_name")
}

// ---- move ----

func TestTickets_Move(t *testing.T) {
	w := tkSetup(t)
	for _, n := range []string{"A", "B", "C", "D"} {
		w.mk(t, map[string]any{"title": n, "status": "todo"})
	}
	order := func() []string {
		return tkRefs(tkItems(t, w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets?status=todo", nil, As(w.owner))))
	}
	move := func(ref string, body map[string]any) *Resp {
		return w.h.Do(t, "POST", "/api/v1/tickets/"+ref+"/move", body, As(w.editor))
	}
	if r := move("WEB-4", map[string]any{"status": "todo", "place": "top"}); r.Code != 200 || !tkEq(order(), []string{"WEB-4", "WEB-1", "WEB-2", "WEB-3"}) {
		t.Errorf("top: %d %v", r.Code, order())
	}
	if r := move("WEB-4", map[string]any{"status": "todo", "after": "WEB-2"}); r.Code != 200 || !tkEq(order(), []string{"WEB-1", "WEB-2", "WEB-4", "WEB-3"}) {
		t.Errorf("after: %d %v", r.Code, order())
	}
	if r := move("WEB-3", map[string]any{"status": "todo", "before": "web-1"}); r.Code != 200 || !tkEq(order(), []string{"WEB-3", "WEB-1", "WEB-2", "WEB-4"}) {
		t.Errorf("before (lowercase ref): %d %v", r.Code, order())
	}
	if r := move("WEB-3", map[string]any{"status": "todo"}); r.Code != 200 || !tkEq(order(), []string{"WEB-1", "WEB-2", "WEB-4", "WEB-3"}) {
		t.Errorf("default bottom: %d %v", r.Code, order())
	}
	// no-op move: same place; renumbered false
	r := move("WEB-3", map[string]any{"status": "todo", "place": "bottom"})
	j := r.JSON(t)
	if r.Code != 200 || j["renumbered"] != false {
		t.Errorf("no-op: %d %s", r.Code, r.Body)
	}
	// cross-column move changes status
	r = move("WEB-1", map[string]any{"status": "in_progress"})
	if r.Code != 200 || r.JSON(t)["status"] != "in_progress" {
		t.Errorf("cross column: %d %s", r.Code, r.Body)
	}
	// anchors must be in the target status
	r = move("WEB-2", map[string]any{"status": "done", "before": "WEB-4"})
	tkFieldErr(t, r, "before")
	r = move("WEB-2", map[string]any{"status": "todo", "after": "WEB-2"})
	tkFieldErr(t, r, "after")
	tkFieldErr(t, move("WEB-2", map[string]any{"status": "todo", "after": "WEB-999"}), "after")
	// validation
	tkFieldErr(t, move("WEB-2", map[string]any{}), "status")
	tkFieldErr(t, move("WEB-2", map[string]any{"status": "bogus"}), "status")
	tkFieldErr(t, move("WEB-2", map[string]any{"status": "todo", "place": "middle"}), "place")
	tkFieldErr(t, move("WEB-2", map[string]any{"status": "todo", "place": "top", "before": "WEB-4"}), "place")
	if r := move("WEB-2", map[string]any{"status": "todo", "before": "WEB-4", "after": "WEB-3"}); r.Code != 422 {
		t.Errorf("two anchors: %d %s", r.Code, r.Body)
	}
	tkWant(t, move("WEB-2", map[string]any{"status": 5}), 400, "bad_request")
	tkWant(t, move("WEB-2", map[string]any{"status": "todo", "x": 1}), 400, "bad_request")
	tkWant(t, move("WEB-77", map[string]any{"status": "todo"}), 404, "not_found")
	// a ticket of another project is not a valid anchor
	w.h.Do(t, "POST", "/api/v1/projects/OTH/tickets", map[string]any{"title": "x", "status": "todo"}, As(w.owner))
	tkFieldErr(t, move("WEB-2", map[string]any{"status": "todo", "before": "OTH-1"}), "before")
	// moves write `moved` activity
	if first := tkItems(t, w.h.Do(t, "GET", "/api/v1/tickets/WEB-1/activity", nil, As(w.owner)))[0]; first["action"] != "moved" {
		t.Errorf("want moved activity, got %v", first)
	}
}

func TestTickets_MoveRenumbered(t *testing.T) {
	w := tkSetup(t)
	for _, n := range []string{"A", "B", "C"} {
		w.mk(t, map[string]any{"title": n})
	}
	// keep inserting between the first card and its neighbour until the gap is exhausted
	renum := false
	for i := 0; i < 200 && !renum; i++ {
		ref := []string{"WEB-3", "WEB-2"}[i%2]
		r := w.h.Do(t, "POST", "/api/v1/tickets/"+ref+"/move", map[string]any{"status": "todo", "after": "WEB-1"}, As(w.owner))
		if r.Code != 200 {
			t.Fatalf("move %d: %d %s", i, r.Code, r.Body)
		}
		renum = r.JSON(t)["renumbered"] == true
	}
	if !renum {
		t.Fatal("repeated midpoint inserts never reported renumbered")
	}
	// the board is still consistent: three distinct cards, A first
	got := tkRefs(tkItems(t, w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets", nil, As(w.owner))))
	if len(got) != 3 || got[0] != "WEB-1" {
		t.Errorf("order after renumber: %v", got)
	}
}

// ---- archived project ----

func TestTickets_Archived(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "A"})
	w.mk(t, map[string]any{"title": "B"})
	w.h.Env.Archive(t, w.proj.ID)
	writes := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/projects/WEB/tickets", map[string]any{"title": "x"}},
		{"PATCH", "/api/v1/tickets/WEB-1", map[string]any{"title": "x"}},
		{"POST", "/api/v1/tickets/WEB-1/move", map[string]any{"status": "done"}},
		{"DELETE", "/api/v1/tickets/WEB-1", nil},
	}
	for _, c := range writes {
		for name, c2 := range map[string]*Client{"owner": w.owner, "editor": w.editor} {
			r := w.h.Do(t, c.method, c.path, c.body, As(c2))
			if r.Code != 409 || r.ErrCode(t) != "project_archived" {
				t.Errorf("%s %s as %s: %d %s", c.method, c.path, name, r.Code, r.Body)
			}
		}
	}
	// reads still work; a viewer's write is still 403 (role before archived)
	for _, p := range []string{"/api/v1/projects/WEB/tickets", "/api/v1/tickets/WEB-1", "/api/v1/tickets/WEB-1/activity"} {
		if r := w.h.Do(t, "GET", p, nil, As(w.viewer)); r.Code != 200 {
			t.Errorf("GET %s: %d", p, r.Code)
		}
	}
	tkWant(t, w.h.Do(t, "PATCH", "/api/v1/tickets/WEB-1", map[string]any{"title": "x"}, As(w.viewer)), 403, "forbidden")
	// still a non-member 404, never 409
	tkWant(t, w.h.Do(t, "PATCH", "/api/v1/tickets/WEB-1", map[string]any{"title": "x"}, As(w.outsider)), 404, "not_found")
}

// ---- scope x role matrix and 404 hiding ----

func TestTickets_Matrix(t *testing.T) {
	w := tkSetup(t)
	type who struct {
		name string
		opts func() []ReqOpt
	}
	whos := []who{
		{"anonymous", func() []ReqOpt { return nil }},
		{"non-member", func() []ReqOpt { return []ReqOpt{As(w.outsider)} }},
		{"viewer", func() []ReqOpt { return []ReqOpt{As(w.viewer)} }},
		{"editor", func() []ReqOpt { return []ReqOpt{As(w.editor)} }},
		{"owner", func() []ReqOpt { return []ReqOpt{As(w.owner)} }},
		{"owner read token", func() []ReqOpt { return []ReqOpt{Bearer(w.readTok)} }},
		{"owner write token", func() []ReqOpt { return []ReqOpt{Bearer(w.ownerTok)} }},
		{"token limited to this project", func() []ReqOpt { return []ReqOpt{Bearer(w.limOwn)} }},
		{"token limited to other project", func() []ReqOpt { return []ReqOpt{Bearer(w.limOt)} }},
		{"outsider write token", func() []ReqOpt { return []ReqOpt{Bearer(w.outsiderTok)} }},
	}
	type ep struct {
		name, method string
		path         func(ticket string) string
		body         any
		okCode       int
		write        bool
		fresh        bool // needs a fresh ticket per call (destructive)
	}
	eps := []ep{
		{"list", "GET", func(string) string { return "/api/v1/projects/WEB/tickets" }, nil, 200, false, false},
		{"create", "POST", func(string) string { return "/api/v1/projects/WEB/tickets" }, map[string]any{"title": "m"}, 201, true, false},
		{"get", "GET", func(tk string) string { return "/api/v1/tickets/" + tk }, nil, 200, false, false},
		{"patch", "PATCH", func(tk string) string { return "/api/v1/tickets/" + tk }, map[string]any{"title": "m2"}, 200, true, false},
		{"move", "POST", func(tk string) string { return "/api/v1/tickets/" + tk + "/move" }, map[string]any{"status": "done"}, 200, true, false},
		{"activity", "GET", func(tk string) string { return "/api/v1/tickets/" + tk + "/activity" }, nil, 200, false, false},
		{"delete", "DELETE", func(tk string) string { return "/api/v1/tickets/" + tk }, nil, 204, true, true},
	}
	// expected outcome per who: status and error code ("" for success)
	expect := func(who string, e ep) (int, string) {
		switch who {
		case "anonymous":
			return 401, "unauthorized"
		case "non-member", "token limited to other project", "outsider write token":
			return 404, "not_found"
		case "viewer":
			if e.write {
				return 403, "forbidden"
			}
		case "owner read token":
			if e.write {
				return 403, "insufficient_scope"
			}
		}
		return e.okCode, ""
	}
	for _, wh := range whos {
		for _, e := range eps {
			t.Run(wh.name+"/"+e.name, func(t *testing.T) {
				tk := w.mk(t, map[string]any{"title": "seed"})["ref"].(string)
				r := w.h.Do(t, e.method, e.path(tk), e.body, wh.opts()...)
				wc, we := expect(wh.name, e)
				if r.Code != wc || r.ErrCode(t) != we {
					t.Fatalf("got %d %q (%s), want %d %q", r.Code, r.ErrCode(t), r.Body, wc, we)
				}
			})
		}
	}
}

func TestTickets_NonMemberLooksLikeMissing(t *testing.T) {
	w := tkSetup(t)
	w.mk(t, map[string]any{"title": "secret"})
	ghost := "01HZZZZZZZZZZZZZZZZZZZZZZZ"
	reqs := []struct {
		method, real, ghost string
		body                any
	}{
		{"GET", "/api/v1/tickets/WEB-1", "/api/v1/tickets/" + ghost, nil},
		{"PATCH", "/api/v1/tickets/WEB-1", "/api/v1/tickets/" + ghost, map[string]any{"title": "x"}},
		{"POST", "/api/v1/tickets/WEB-1/move", "/api/v1/tickets/" + ghost + "/move", map[string]any{"status": "done"}},
		{"DELETE", "/api/v1/tickets/WEB-1", "/api/v1/tickets/" + ghost, nil},
		{"GET", "/api/v1/tickets/WEB-1/activity", "/api/v1/tickets/" + ghost + "/activity", nil},
		{"GET", "/api/v1/projects/WEB/tickets", "/api/v1/projects/" + ghost + "/tickets", nil},
		{"POST", "/api/v1/projects/WEB/tickets", "/api/v1/projects/" + ghost + "/tickets", map[string]any{"title": "x"}},
	}
	for _, c := range reqs {
		real := w.h.Do(t, c.method, c.real, c.body, As(w.outsider))
		gh := w.h.Do(t, c.method, c.ghost, c.body, As(w.outsider))
		if real.Code != 404 || gh.Code != 404 || !bytes.Equal(real.Body, gh.Body) {
			t.Errorf("%s %s: non-member %d %s vs missing %d %s", c.method, c.real, real.Code, real.Body, gh.Code, gh.Body)
		}
		// a project-limited token pointing elsewhere looks the same
		tok := w.h.Do(t, c.method, c.real, c.body, Bearer(w.limOt))
		if tok.Code != 404 || !bytes.Equal(tok.Body, gh.Body) {
			t.Errorf("%s %s: limited token %d %s", c.method, c.real, tok.Code, tok.Body)
		}
	}
	// key-style refs of unknown projects look the same too
	a := w.h.Do(t, "GET", "/api/v1/tickets/WEB-1", nil, As(w.outsider))
	b := w.h.Do(t, "GET", "/api/v1/tickets/NOPE-1", nil, As(w.outsider))
	if !bytes.Equal(a.Body, b.Body) {
		t.Errorf("ref 404 differs: %s vs %s", a.Body, b.Body)
	}
}

func TestTickets_TokenActivityAndLimits(t *testing.T) {
	w := tkSetup(t)
	// a project-limited token creates tickets in its project and sees them
	r := w.h.Do(t, "POST", "/api/v1/projects/WEB/tickets", map[string]any{"title": "by bot"}, Bearer(w.limOwn))
	if r.Code != 201 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	tkWant(t, w.h.Do(t, "POST", "/api/v1/projects/OTH/tickets", map[string]any{"title": "x"}, Bearer(w.limOwn)), 404, "not_found")
	if r := w.h.Do(t, "GET", "/api/v1/projects/WEB/tickets", nil, Bearer(w.readTok)); r.Code != 200 || len(tkItems(t, r)) != 1 {
		t.Errorf("read token list: %d %s", r.Code, r.Body)
	}
	// a viewer-level user holding a write token is still a viewer
	vt, _ := w.h.MkToken(t, w.viewer, service.ScopeWrite, "")
	tkWant(t, w.h.Do(t, "POST", "/api/v1/projects/WEB/tickets", map[string]any{"title": "x"}, Bearer(vt)), 403, "forbidden")
	// cookie-less write without Origin is rejected before anything else
	tkWant(t, w.h.Do(t, "POST", "/api/v1/projects/WEB/tickets", map[string]any{"title": "x"}, As(w.owner), NoOrigin()), 403, "origin_mismatch")
}
