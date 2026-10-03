package httpapi

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
)

// Golden shape tests. The expected key sets are LITERAL lists (never derived from the view
// structs) so renaming or adding a field fails here. Phase 5's types.ts and phase 4 rely on
// these shapes (phase 2 spec section 6).

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func wantKeys(t *testing.T, name string, m map[string]any, keys ...string) {
	t.Helper()
	sort.Strings(keys)
	if got := keysOf(m); !reflect.DeepEqual(got, keys) {
		t.Errorf("%s keys:\n got  %v\n want %v", name, got, keys)
	}
}

func wantType(t *testing.T, name string, m map[string]any, key, typ string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("%s.%s missing", name, key)
		return
	}
	got := "null"
	switch v.(type) {
	case string:
		got = "string"
	case float64:
		got = "number"
	case bool:
		got = "bool"
	case []any:
		got = "array"
	case map[string]any:
		got = "object"
	}
	if got != typ {
		t.Errorf("%s.%s is %s, want %s", name, key, got, typ)
	}
}

var (
	t0     = time.Date(2026, 3, 4, 5, 6, 7, 123000000, time.UTC)
	t1     = t0.Add(time.Hour)
	sessP  = auth.Principal{User: auth.AuthUser{ID: "U1", Email: "a@x.io", DisplayName: "A", CreatedAt: t0}, Method: auth.MethodSession}
	tokenP = func(scope service.Scope, pid, pkey string) auth.Principal {
		return auth.Principal{User: sessP.User, Method: auth.MethodToken,
			Token: &auth.TokenInfo{ID: "T1", Name: "ci", Scope: scope, ProjectID: pid, ProjectKey: pkey}}
	}
)

func TestShapeUsers(t *testing.T) {
	wantKeys(t, "user", asMap(t, mapUser("U1", "a@x.io", "A")), "id", "email", "display_name")
	au := asMap(t, mapAuthUser(sessP.User))
	wantKeys(t, "auth_user", au, "id", "email", "display_name", "created_at")
	if au["created_at"] != "2026-03-04T05:06:07.123Z" {
		t.Errorf("created_at %v", au["created_at"])
	}
	wantKeys(t, "auth_user (service)", asMap(t, mapServiceUser(service.User{ID: "U", CreatedAt: t0})), "id", "email", "display_name", "created_at")
	env := asMap(t, authUserEnvelope{User: mapAuthUser(sessP.User)})
	wantKeys(t, "signup envelope", env, "user")
}

func TestShapeMe(t *testing.T) {
	s := asMap(t, mapMe(sessP))
	wantKeys(t, "me(session)", s, "user", "auth")
	a := s["auth"].(map[string]any)
	wantKeys(t, "me(session).auth", a, "method") // token omitted for a session
	if a["method"] != "session" {
		t.Error(a)
	}

	tk := asMap(t, mapMe(tokenP(service.ScopeRead, "P1", "WEB")))
	a = tk["auth"].(map[string]any)
	wantKeys(t, "me(token).auth", a, "method", "token")
	tok := a["token"].(map[string]any)
	wantKeys(t, "me(token).auth.token", tok, "id", "name", "scope", "project_id", "project_key")
	if tok["project_id"] != "P1" || tok["project_key"] != "WEB" || a["method"] != "token" {
		t.Error(tok)
	}
	// Unlimited token: nullable keys present as null.
	tok = asMap(t, mapMe(tokenP(service.ScopeWrite, "", "")))["auth"].(map[string]any)["token"].(map[string]any)
	wantKeys(t, "unlimited token", tok, "id", "name", "scope", "project_id", "project_key")
	wantType(t, "unlimited token", tok, "project_id", "null")
	wantType(t, "unlimited token", tok, "project_key", "null")
}

func TestShapeProject(t *testing.T) {
	pr := service.Project{ID: "P1", Key: "WEB", Name: "Web", CreatedAt: t0, UpdatedAt: t1}
	m := asMap(t, mapProject(sessP, pr, service.RoleOwner))
	wantKeys(t, "project", m, "id", "key", "name", "description", "archived_at", "created_at", "updated_at", "role")
	wantType(t, "project", m, "archived_at", "null")
	wantType(t, "project", m, "description", "string")
	if m["role"] != "owner" {
		t.Error(m["role"])
	}
	arch := t1
	pr.ArchivedAt = &arch
	wantType(t, "project", asMap(t, mapProject(sessP, pr, service.RoleViewer)), "archived_at", "string")

	// A read token whose owner is an owner sees viewer.
	if r := asMap(t, mapProject(tokenP(service.ScopeRead, "", ""), pr, service.RoleOwner))["role"]; r != "viewer" {
		t.Errorf("read token role = %v", r)
	}
	if r := asMap(t, mapProject(tokenP(service.ScopeWrite, "", ""), pr, service.RoleOwner))["role"]; r != "owner" {
		t.Errorf("write token role = %v", r)
	}

	d := asMap(t, mapProjectDetail(sessP, service.ProjectDetail{Project: pr, Role: service.RoleEditor,
		TicketCounts: map[service.Status]int{service.StatusTodo: 2}}))
	wantKeys(t, "project_detail", d, "id", "key", "name", "description", "archived_at", "created_at", "updated_at", "role", "counts")
	c := d["counts"].(map[string]any)
	wantKeys(t, "counts", c, "backlog", "todo", "in_progress", "done")
	if c["todo"] != float64(2) || c["backlog"] != float64(0) {
		t.Errorf("%v", c)
	}
	// Missing map entirely still gives all four keys.
	c = asMap(t, mapProjectDetail(sessP, service.ProjectDetail{Project: pr}))["counts"].(map[string]any)
	wantKeys(t, "counts (nil map)", c, "backlog", "todo", "in_progress", "done")

	// Summary is the plain project: no counts.
	wantKeys(t, "project (summary)", asMap(t, mapProjectSummary(sessP, service.ProjectSummary{Project: pr, Role: service.RoleOwner})),
		"id", "key", "name", "description", "archived_at", "created_at", "updated_at", "role")
}

func TestShapeMemberAndLabel(t *testing.T) {
	m := asMap(t, mapMember(service.Member{UserID: "U1", Email: "a@x.io", DisplayName: "A", Role: service.RoleEditor, CreatedAt: t0}))
	wantKeys(t, "member", m, "user", "role", "created_at")
	wantKeys(t, "member.user", m["user"].(map[string]any), "id", "email", "display_name")

	l := asMap(t, mapLabel(service.Label{ID: "L1", ProjectID: "P1", Name: "bug", Color: "red"}))
	wantKeys(t, "label", l, "id", "project_id", "name", "color")
	if len(mapLabels(nil)) != 0 || mapLabels(nil) == nil || len(mapMembers(nil)) != 0 || mapMembers(nil) == nil {
		t.Error("empty lists must be non-nil so they encode as []")
	}
}

var ticketKeys = []string{"id", "ref", "project_id", "project_key", "number", "title", "status", "priority", "assignee",
	"labels", "position", "due_date", "comment_count", "created_at", "updated_at"}

func sampleTicket() service.Ticket {
	due := "2026-05-06"
	return service.Ticket{
		ID: "K1", ProjectID: "P1", ProjectKey: "WEB", Number: 12, Ref: "WEB-12", Title: "T", Description: "",
		Status: service.StatusTodo, Priority: service.PriorityMedium, Position: 1024, DueDate: &due,
		Assignee:     &service.UserRef{ID: "U1", Email: "a@x.io", DisplayName: "A"},
		Labels:       []service.Label{{ID: "L1", ProjectID: "P1", Name: "bug", Color: "red"}},
		CommentCount: 3, CreatedAt: t0, UpdatedAt: t1,
	}
}

func TestShapeTicket(t *testing.T) {
	list := asMap(t, ticketListItem(sampleTicket()))
	wantKeys(t, "ticket list item", list, ticketKeys...) // no description
	full := asMap(t, ticketFull(sampleTicket()))
	wantKeys(t, "ticket full", full, append([]string{"description"}, ticketKeys...)...)
	// An empty description is still present on the full shape.
	wantType(t, "ticket full", full, "description", "string")

	for _, k := range []struct{ key, typ string }{
		{"id", "string"}, {"ref", "string"}, {"number", "number"}, {"position", "number"}, {"comment_count", "number"},
		{"labels", "array"}, {"assignee", "object"}, {"due_date", "string"},
	} {
		wantType(t, "ticket", full, k.key, k.typ)
	}
	if full["ref"] != "WEB-12" || full["project_key"] != "WEB" {
		t.Error(full)
	}
	wantKeys(t, "assignee", full["assignee"].(map[string]any), "id", "display_name", "email")
	wantKeys(t, "ticket.labels[0]", full["labels"].([]any)[0].(map[string]any), "id", "project_id", "name", "color")

	// Nullable keys are present as null; labels is [] never null.
	bare := sampleTicket()
	bare.Assignee, bare.DueDate, bare.Labels = nil, nil, nil
	m := asMap(t, ticketListItem(bare))
	wantKeys(t, "ticket list item (bare)", m, ticketKeys...)
	wantType(t, "ticket", m, "assignee", "null")
	wantType(t, "ticket", m, "due_date", "null")
	wantType(t, "ticket", m, "labels", "array")

	items := mapTicketList([]service.Ticket{sampleTicket()})
	wantKeys(t, "mapTicketList item", asMap(t, items[0]), ticketKeys...)
}

func TestShapeMove(t *testing.T) {
	m := asMap(t, mapMove(service.MoveResult{Ticket: sampleTicket(), Renumbered: true}))
	wantKeys(t, "move", m, append([]string{"description", "renumbered"}, ticketKeys...)...)
	if m["renumbered"] != true {
		t.Error(m["renumbered"])
	}
	wantType(t, "move", asMap(t, mapMove(service.MoveResult{Ticket: sampleTicket()})), "renumbered", "bool")
}

func TestShapeCommentAndActivity(t *testing.T) {
	user := service.CommentAuthor{Type: service.ActorUser, ID: "U1", Name: "A"}
	bot := service.CommentAuthor{Type: service.ActorAPIToken, ID: "T1", Name: "ci", OwnerName: "A"}

	ua := asMap(t, mapAuthor(user))
	wantKeys(t, "comment_author(user)", ua, "type", "id", "name", "bot") // no owner_name
	if ua["bot"] != false || ua["type"] != "user" {
		t.Error(ua)
	}
	ba := asMap(t, mapAuthor(bot))
	wantKeys(t, "comment_author(token)", ba, "type", "id", "name", "bot", "owner_name")
	if ba["bot"] != true || ba["type"] != "api_token" || ba["owner_name"] != "A" {
		t.Error(ba)
	}
	// Unresolvable authors render as Unknown, never as a bot.
	for _, gone := range []service.CommentAuthor{
		{Type: service.ActorUser, ID: "U9", Name: "deleted user"},
		{Type: service.ActorAPIToken, ID: "T9", Name: "deleted token"},
		{Type: service.ActorUser, ID: "U9"},
	} {
		g := asMap(t, mapAuthor(gone))
		if g["name"] != "Unknown" || g["bot"] != false {
			t.Errorf("%+v -> %v", gone, g)
		}
		wantKeys(t, "unknown author", g, "type", "id", "name", "bot")
	}

	edited := t1
	c := asMap(t, mapComment(service.Comment{ID: "C1", TicketID: "K1", Author: user, Body: "hi", CreatedAt: t0}))
	wantKeys(t, "comment", c, "id", "ticket_id", "author", "body", "created_at", "edited_at")
	wantType(t, "comment", c, "edited_at", "null")
	wantKeys(t, "comment.author", c["author"].(map[string]any), "type", "id", "name", "bot")
	c = asMap(t, mapComment(service.Comment{ID: "C1", TicketID: "K1", Author: bot, Body: "hi", CreatedAt: t0, EditedAt: &edited}))
	wantType(t, "comment", c, "edited_at", "string")
	wantKeys(t, "comment.author(token)", c["author"].(map[string]any), "type", "id", "name", "bot", "owner_name")

	a := asMap(t, mapActivity(service.Activity{ID: "A1", TicketID: "K1", Actor: bot, Action: "moved",
		Changes: map[string][2]any{"status": {"todo", "done"}, "position": {1024.0, 2048.0}}, CreatedAt: t0}))
	wantKeys(t, "activity", a, "id", "ticket_id", "actor", "action", "changes", "created_at")
	wantKeys(t, "activity.actor", a["actor"].(map[string]any), "type", "id", "name", "bot", "owner_name")
	ch := a["changes"].(map[string]any)
	wantKeys(t, "activity.changes", ch, "status", "position")
	if pair := ch["status"].([]any); len(pair) != 2 || pair[0] != "todo" || pair[1] != "done" {
		t.Errorf("changes pair %v", pair)
	}
	// No changes encodes as {} not null.
	wantType(t, "activity", asMap(t, mapActivity(service.Activity{Actor: user})), "changes", "object")
}

func TestShapeToken(t *testing.T) {
	tk := service.Token{ID: "T1", Name: "ci", Prefix: "pb_1a2b3", Scope: service.ScopeWrite, CreatedAt: t0}
	m := asMap(t, mapToken(tk))
	wantKeys(t, "token", m, "id", "name", "token_prefix", "scope", "project", "last_used_at", "revoked_at", "created_at")
	wantType(t, "token", m, "project", "null")
	wantType(t, "token", m, "last_used_at", "null")
	wantType(t, "token", m, "revoked_at", "null")

	used, revoked := t1, t1
	tk.LastUsedAt, tk.RevokedAt, tk.Project = &used, &revoked, &service.TokenProject{ID: "P1", Key: "WEB"}
	m = asMap(t, mapToken(tk))
	wantType(t, "token", m, "last_used_at", "string")
	wantType(t, "token", m, "revoked_at", "string")
	wantKeys(t, "token.project", m["project"].(map[string]any), "id", "key")

	cr := asMap(t, mapCreatedToken(tk, "pb_secret"))
	wantKeys(t, "token creation response", cr, "token", "secret")
	wantKeys(t, "token creation response .token", cr["token"].(map[string]any),
		"id", "name", "token_prefix", "scope", "project", "last_used_at", "revoked_at", "created_at")
	if cr["secret"] != "pb_secret" {
		t.Error(cr)
	}
	// The token shape itself never has a secret or hash.
	for _, k := range keysOf(m) {
		if k == "secret" || k == "token_hash" || k == "hash" {
			t.Errorf("token shape leaks %s", k)
		}
	}
}

func TestShapeEnvelope(t *testing.T) {
	b, _ := json.Marshal(listEnvelope[labelView]{Items: mapLabels(nil)})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	wantKeys(t, "envelope", m, "items", "next_cursor")
	wantType(t, "envelope", m, "items", "array")
	wantType(t, "envelope", m, "next_cursor", "null")
}

func TestShapeError(t *testing.T) {
	m := asMap(t, errorBody{Error: errorDetail{Code: "x", Message: "y"}})
	wantKeys(t, "error", m, "error")
	wantKeys(t, "error.error (no fields)", m["error"].(map[string]any), "code", "message")
	m = asMap(t, errorBody{Error: errorDetail{Code: "x", Message: "y", Fields: map[string]string{"a": "b"}}})
	wantKeys(t, "error.error (fields)", m["error"].(map[string]any), "code", "message", "fields")
}
