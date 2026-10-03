package mcpserver

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

func keysOf(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func items(m map[string]any) []map[string]any {
	var out []map[string]any
	for _, v := range m["items"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

func refs(m map[string]any) []string {
	var out []string
	for _, it := range items(m) {
		out = append(out, it["ref"].(string))
	}
	return out
}

func TestResultShapesAndStructured(t *testing.T) {
	f := newFx(t)
	cs, _ := f.as(f.alice, service.ScopeWrite)
	tk := f.okCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": "T1"})
	want := []string{"assignee", "due_date", "labels", "position", "priority", "ref", "status", "title", "url"}
	if !reflect.DeepEqual(keysOf(tk), want) {
		t.Fatalf("ticket keys %v", keysOf(tk))
	}
	if tk["url"] != testBase+"/p/WEB/t/1" || tk["ref"] != "WEB-1" || tk["assignee"] != nil || tk["due_date"] != nil {
		t.Fatalf("%v", tk)
	}
	r := f.call(cs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	if r.R.StructuredContent == nil {
		t.Fatal("no structuredContent")
	}
	d := r.JSON()
	wantD := []string{"assignee", "comment_count", "comments", "comments_truncated", "created_at", "description", "due_date", "labels", "position", "priority", "project", "ref", "status", "title", "updated_at", "url"}
	if !reflect.DeepEqual(keysOf(d), wantD) {
		t.Fatalf("detail keys %v", keysOf(d))
	}
	pl := f.okCall(cs, "list_projects", nil)
	p0 := items(pl)[0]
	if !reflect.DeepEqual(keysOf(p0), []string{"archived", "counts", "description", "key", "name", "role", "url"}) {
		t.Fatalf("project keys %v", keysOf(p0))
	}
	if len(p0["counts"].(map[string]any)) != 4 {
		t.Fatal("counts")
	}
}

func TestReadListProjectsAndMembersLabels(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	f.seedTicket("WEB", "b", "done")
	f.seedLabel("WEB", "ui")
	cs, _ := f.as(f.carol, service.ScopeWrite)
	pl := items(f.okCall(cs, "list_projects", nil))
	if len(pl) != 1 || pl[0]["key"] != "WEB" || pl[0]["role"] != "viewer" {
		t.Fatalf("%v", pl)
	}
	c := pl[0]["counts"].(map[string]any)
	if c["todo"].(float64) != 1 || c["done"].(float64) != 1 || c["backlog"].(float64) != 0 {
		t.Fatalf("%v", c)
	}
	// read token reports viewer
	rcs, _ := f.as(f.alice, service.ScopeRead)
	for _, p := range items(f.okCall(rcs, "list_projects", nil)) {
		if p["role"] != "viewer" {
			t.Fatalf("read token role %v", p["role"])
		}
	}
	ms := items(f.okCall(cs, "list_members", map[string]any{"project": "web"}))
	if len(ms) != 3 {
		t.Fatalf("%v", ms)
	}
	ls := items(f.okCall(cs, "list_labels", map[string]any{"project": f.web.ID}))
	if len(ls) != 1 || ls[0]["name"] != "ui" || ls[0]["color"] != "blue" {
		t.Fatalf("%v", ls)
	}
}

func TestResolutionMessages(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	cs, _ := f.as(f.alice, service.ScopeWrite)
	f.errCall(cs, "list_members", map[string]any{"project": "WEBB"}, "No project with key WEBB. Available keys: OPS, WEB.")
	f.errCall(cs, "list_members", map[string]any{}, "project is required.")
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "WEB-0"}, `Invalid ticket "WEB-0"`)
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "web12"}, `Invalid ticket "web12"`)
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "WEB-99"}, "No ticket WEB-99 in project WEB. It may not exist or may already be deleted.")
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "ZZZ-1"}, "No project with key ZZZ.")
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "01J00000000000000000000000"}, "No ticket with id 01J00000000000000000000000.")
	f.errCall(cs, "get_ticket", map[string]any{}, "ticket is required.")
	f.okCall(cs, "get_ticket", map[string]any{"ticket": "web-1"})
	// unknown and mistyped arguments are tool errors, not protocol errors
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1", "bogus": 1}, `Unknown argument "bogus"`)
	f.errCall(cs, "list_tickets", map[string]any{"project": "WEB", "limit": "x"}, "wrong type")
	f.errCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "status": "done"}, "use move_ticket")
	// non-member sees only what is available to him, never WEB or OPS
	dcs, _ := f.as(f.dave, service.ScopeWrite)
	f.errCall(dcs, "list_members", map[string]any{"project": "WEB"}, "No projects available to this token.")
	r1 := f.call(dcs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	r2 := f.call(dcs, "get_ticket", map[string]any{"ticket": "WEB-99"})
	if !r1.IsError || strings.Replace(r1.Text, "WEB-1", "X", 1) != strings.Replace(r2.Text, "WEB-99", "X", 1) {
		t.Fatalf("hidden ticket differs from nonexistent: %q vs %q", r1.Text, r2.Text)
	}
}

func TestCreateTicketAssigneeLabels(t *testing.T) {
	f := newFx(t)
	f.seedLabel("WEB", "backend")
	f.seedLabel("WEB", "ui")
	cs, tokID := f.as(f.alice, service.ScopeWrite)
	tk := f.okCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": "x", "assignee": "BOB@x.com",
		"labels": []string{"Backend", "backend", "ui"}, "priority": "high", "status": "in_progress", "due_date": "2026-11-01"})
	if tk["assignee"] != "bob@x.com" || tk["priority"] != "high" || tk["status"] != "in_progress" || tk["due_date"] != "2026-11-01" {
		t.Fatalf("%v", tk)
	}
	if !reflect.DeepEqual(tk["labels"], []any{"backend", "ui"}) {
		t.Fatalf("%v", tk["labels"])
	}
	f.errCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": "x", "assignee": "zed@x.com"}, "No member with email zed@x.com in project WEB. Members: ")
	f.errCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": "x", "labels": []string{"nope", "ui", "nah"}}, `labels "nope", "nah" in project WEB. Labels: backend, ui. Create one with create_label.`)
	f.errCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": ""}, "title is required.")
	f.errCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": strings.Repeat("a", 250)}, "title is too long (250 > 200).")
	f.errCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": "x", "status": "doing"}, `Invalid status "doing". Valid: backlog, todo, in_progress, done.`)
	f.errCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": "x", "due_date": "tomorrow"}, "due_date")
	// activity attribution
	tkt, _ := f.env.Svc.Tickets.Get(context.Background(), f.aliceActor(), "WEB-1")
	rows := f.activity(tkt.ID)
	if len(rows) != 1 || rows[0].ActorType != "api_token" || rows[0].ActorID != tokID || rows[0].Action != "created" {
		t.Fatalf("%+v", rows)
	}
}

func TestUpdateTicket(t *testing.T) {
	f := newFx(t)
	f.seedLabel("WEB", "ui")
	tk := f.seedTicket("WEB", "orig", "todo")
	cs, tokID := f.as(f.bob, service.ScopeWrite)
	f.errCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1"}, "Pass at least one field to change.")
	u := f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "assignee": "alice@x.com", "due_date": "2026-01-02"})
	if u["assignee"] != "alice@x.com" || u["due_date"] != "2026-01-02" {
		t.Fatal(u)
	}
	// omitted leaves unchanged; null and "" clear
	u = f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "title": "new"})
	if u["assignee"] != "alice@x.com" || u["title"] != "new" {
		t.Fatal(u)
	}
	u = f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "assignee": nil, "due_date": ""})
	if u["assignee"] != nil || u["due_date"] != nil {
		t.Fatal(u)
	}
	u = f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "assignee": "bob@x.com"})
	u = f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "assignee": ""})
	if u["assignee"] != nil {
		t.Fatal(u)
	}
	// labels replace, [] clears
	u = f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "labels": []string{"ui"}})
	if !reflect.DeepEqual(u["labels"], []any{"ui"}) {
		t.Fatal(u)
	}
	u = f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "labels": []string{}})
	if len(u["labels"].([]any)) != 0 {
		t.Fatal(u)
	}
	// no-op writes no activity
	before := len(f.activity(tk.ID))
	f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "title": "new"})
	f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "labels": []string{}})
	if after := len(f.activity(tk.ID)); after != before {
		t.Fatalf("no-op wrote activity %d -> %d", before, after)
	}
	// actions: created(user) updated assigned assigned(cleared) ... check actor and kinds
	var actions []string
	for _, r := range f.activity(tk.ID) {
		actions = append(actions, r.Action)
		if r.Action != "created" && (r.ActorType != "api_token" || r.ActorID != tokID) {
			t.Fatalf("bad attribution %+v", r)
		}
	}
	want := []string{"created", "updated", "updated", "updated", "assigned", "assigned", "labeled", "labeled"}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions %v", actions)
	}
	f.errCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "title": nil}, "title must not be null.")
	f.errCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "priority": "huge"}, `Invalid priority "huge"`)
}

func TestMoveTicket(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	f.seedTicket("WEB", "b", "todo")
	f.seedTicket("WEB", "c", "todo")
	f.seedTicket("WEB", "x", "done")
	f.seedTicket("OPS", "o", "todo")
	cs, tokID := f.as(f.bob, service.ScopeWrite)
	order := func(status string) []string {
		return refs(f.okCall(cs, "list_tickets", map[string]any{"project": "WEB", "status": status}))
	}
	f.okCall(cs, "move_ticket", map[string]any{"ticket": "WEB-3", "status": "todo", "place": "top"})
	if !reflect.DeepEqual(order("todo"), []string{"WEB-3", "WEB-1", "WEB-2"}) {
		t.Fatal(order("todo"))
	}
	f.okCall(cs, "move_ticket", map[string]any{"ticket": "WEB-3", "status": "todo", "after": "WEB-1"})
	if !reflect.DeepEqual(order("todo"), []string{"WEB-1", "WEB-3", "WEB-2"}) {
		t.Fatal(order("todo"))
	}
	f.okCall(cs, "move_ticket", map[string]any{"ticket": "WEB-2", "status": "todo", "before": "WEB-1"})
	if !reflect.DeepEqual(order("todo"), []string{"WEB-2", "WEB-1", "WEB-3"}) {
		t.Fatal(order("todo"))
	}
	r := f.okCall(cs, "move_ticket", map[string]any{"ticket": "WEB-1", "status": "done"})
	if r["status"] != "done" || !reflect.DeepEqual(order("done"), []string{"WEB-4", "WEB-1"}) {
		t.Fatal(r, order("done"))
	}
	if _, has := r["renumbered"]; has {
		t.Fatal("renumbered must be omitted")
	}
	f.errCall(cs, "move_ticket", map[string]any{"ticket": "WEB-1", "status": "done", "place": "top", "before": "WEB-4"}, "Use only one of place, before, after.")
	acs, _ := f.as(f.alice, service.ScopeWrite)
	f.errCall(acs, "move_ticket", map[string]any{"ticket": "WEB-1", "status": "done", "before": "OPS-1"}, "before ticket OPS-1 is not in project WEB.")
	f.errCall(cs, "move_ticket", map[string]any{"ticket": "WEB-1", "status": "done", "before": "OPS-1"}, "No project with key OPS.")
	f.errCall(cs, "move_ticket", map[string]any{"ticket": "WEB-1", "status": "done", "after": "WEB-1"}, "Cannot place a ticket relative to itself.")
	f.errCall(cs, "move_ticket", map[string]any{"ticket": "WEB-1", "status": "todo", "before": "WEB-4"},
		`before ticket WEB-4 must be in the same project and currently in column todo (the target status).`)
	f.errCall(cs, "move_ticket", map[string]any{"ticket": "WEB-1", "status": "doing"}, `Invalid status "doing"`)
	f.errCall(cs, "move_ticket", map[string]any{"ticket": "WEB-1"}, "status is required.")
	f.errCall(cs, "move_ticket", map[string]any{"ticket": "WEB-1", "status": "done", "place": "middle"}, `Invalid place "middle"`)
	tk, _ := f.env.Svc.Tickets.Get(context.Background(), f.aliceActor(), "WEB-1")
	last := f.activity(tk.ID)
	if a := last[len(last)-1]; a.Action != "moved" || a.ActorID != tokID || a.ActorType != "api_token" {
		t.Fatalf("%+v", a)
	}
	// renumbered appears only when the service renumbers: force it with repeated insertion
	renumbered := false
	for i := 0; i < 80 && !renumbered; i++ {
		f.seedTicket("OPS", fmt.Sprintf("n%d", i), "backlog")
		if i == 0 {
			continue
		}
		ocs, _ := f.as(f.alice, service.ScopeWrite)
		rr := f.okCall(ocs, "move_ticket", map[string]any{"ticket": fmt.Sprintf("OPS-%d", i+2), "status": "backlog", "before": "OPS-2"})
		if v, has := rr["renumbered"]; has {
			if v != true {
				t.Fatal(rr)
			}
			renumbered = true
		}
	}
	if !renumbered {
		t.Log("service never renumbered within 80 inserts (not asserted)")
	}
}

func TestDeleteTicket(t *testing.T) {
	f := newFx(t)
	tk := f.seedTicket("WEB", "a", "todo")
	cs, tokID := f.as(f.bob, service.ScopeWrite)
	f.errCall(cs, "delete_ticket", map[string]any{"ticket": "WEB-1"}, "delete_ticket needs confirm: true. Nothing was deleted.")
	f.errCall(cs, "delete_ticket", map[string]any{"ticket": "WEB-1", "confirm": false}, "needs confirm: true")
	f.okCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	r := f.okCall(cs, "delete_ticket", map[string]any{"ticket": "WEB-1", "confirm": true})
	if r["ref"] != "WEB-1" || r["deleted"] != true {
		t.Fatal(r)
	}
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1"}, "may already be deleted")
	f.errCall(cs, "delete_ticket", map[string]any{"ticket": "WEB-1", "confirm": true}, "may already be deleted")
	if n := len(items(f.okCall(cs, "list_tickets", map[string]any{"project": "WEB"}))); n != 0 {
		t.Fatal(n)
	}
	rows := f.activity(tk.ID)
	if a := rows[len(rows)-1]; a.Action != "deleted" || a.ActorID != tokID {
		t.Fatalf("%+v", a)
	}
}

func TestListTicketsFiltersAndPagination(t *testing.T) {
	f := newFx(t)
	f.seedLabel("WEB", "ui")
	for i := 0; i < 5; i++ {
		f.seedTicket("WEB", fmt.Sprintf("ticket %d", i), "todo")
	}
	cs, _ := f.as(f.bob, service.ScopeWrite)
	f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "assignee": "bob@x.com", "labels": []string{"ui"}, "priority": "urgent"})
	got := func(args map[string]any) []string {
		args["project"] = "WEB"
		return refs(f.okCall(cs, "list_tickets", args))
	}
	if !reflect.DeepEqual(got(map[string]any{"assignee": "me"}), []string{"WEB-1"}) {
		t.Fatal("me")
	}
	if !reflect.DeepEqual(got(map[string]any{"assignee": "bob@x.com"}), []string{"WEB-1"}) {
		t.Fatal("email")
	}
	if len(got(map[string]any{"assignee": "unassigned"})) != 4 || len(got(map[string]any{"assignee": "none"})) != 4 {
		t.Fatal("unassigned")
	}
	if !reflect.DeepEqual(got(map[string]any{"label": "UI"}), []string{"WEB-1"}) {
		t.Fatal("label")
	}
	if !reflect.DeepEqual(got(map[string]any{"priority": "urgent"}), []string{"WEB-1"}) {
		t.Fatal("priority")
	}
	if !reflect.DeepEqual(got(map[string]any{"query": "ticket 3"}), []string{"WEB-4"}) {
		t.Fatal("query")
	}
	f.errCall(cs, "list_tickets", map[string]any{"project": "WEB", "label": "zzz"}, `No label "zzz"`)
	f.errCall(cs, "list_tickets", map[string]any{"project": "WEB", "status": "doing"}, "Invalid status")
	f.errCall(cs, "list_tickets", map[string]any{"project": "WEB", "limit": 0}, "limit must be between 1 and 200.")
	f.errCall(cs, "list_tickets", map[string]any{"project": "WEB", "limit": 201}, "limit must be between 1 and 200.")
	f.errCall(cs, "list_tickets", map[string]any{"project": "WEB", "cursor": "garbage"}, "Invalid cursor.")
	// pagination: no gaps or duplicates, cursor passed through
	var all []string
	cursor := ""
	for i := 0; i < 10; i++ {
		args := map[string]any{"project": "WEB", "limit": 2}
		if cursor != "" {
			args["cursor"] = cursor
		}
		page := f.okCall(cs, "list_tickets", args)
		all = append(all, refs(page)...)
		next, _ := page["next_cursor"].(string)
		if next == "" {
			if page["next_cursor"] != nil {
				t.Fatal("empty cursor must be null")
			}
			break
		}
		cursor = next
	}
	if !reflect.DeepEqual(all, []string{"WEB-1", "WEB-2", "WEB-3", "WEB-4", "WEB-5"}) {
		t.Fatal(all)
	}
}

func TestGetTicketComments(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	cs, tokID := f.as(f.bob, service.ScopeWrite)
	ac := f.aliceActor()
	if _, err := f.env.Svc.Comments.Add(context.Background(), ac, "WEB-1", "from alice"); err != nil {
		t.Fatal(err)
	}
	c := f.okCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": "from bot"})
	if !reflect.DeepEqual(keysOf(c), []string{"author", "created_at", "id", "ticket"}) || c["ticket"] != "WEB-1" || c["id"] == "" {
		t.Fatal(c)
	}
	d := f.okCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	cm := d["comments"].([]any)
	if len(cm) != 2 || d["comment_count"].(float64) != 2 || d["comments_truncated"] != false {
		t.Fatal(d)
	}
	a, b := cm[0].(map[string]any), cm[1].(map[string]any)
	if a["author"] != "Alice" || a["author_kind"] != "user" || b["author_kind"] != "api_token" || b["id"] != c["id"] || b["edited_at"] != nil {
		t.Fatal(a, b)
	}
	_ = tokID
	// 20,000 accepted, 20,001 rejected
	f.okCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": strings.Repeat("é", 20000)})
	f.errCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": strings.Repeat("a", 20001)}, "body is too long (20001 > 20000).")
	f.errCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": " "}, "body is required.")
	// budgets: 8000 per comment, 40000 total, dropping the oldest
	d = f.okCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	last := d["comments"].([]any)[2].(map[string]any)
	if last["body_truncated"] != true || len([]rune(last["body"].(string))) != 8000 {
		t.Fatal("per-comment cut")
	}
	for i := 0; i < 6; i++ {
		f.okCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": strings.Repeat("z", 9000)})
	}
	d = f.okCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	if d["comments_truncated"] != true {
		t.Fatal("total budget not reported")
	}
	total := 0
	for _, x := range d["comments"].([]any) {
		total += len([]rune(x.(map[string]any)["body"].(string)))
	}
	if total > 40000 || d["comment_count"].(float64) != 9 {
		t.Fatal(total, d["comment_count"])
	}
	// cap of 50 with truncation flag and real count
	for i := 0; i < 45; i++ {
		f.okCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": "x"})
	}
	d = f.okCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	if len(d["comments"].([]any)) > 50 || d["comments_truncated"] != true || d["comment_count"].(float64) != 54 {
		t.Fatal(len(d["comments"].([]any)), d["comment_count"])
	}
}

func TestUpdateComment(t *testing.T) {
	f := newFx(t)
	tk := f.seedTicket("WEB", "a", "todo")
	cs, _ := f.as(f.bob, service.ScopeWrite)
	other, _ := f.as(f.bob, service.ScopeWrite) // a second token of bob
	userC, err := f.env.Svc.Comments.Add(context.Background(), f.aliceActor(), "WEB-1", "by alice")
	if err != nil {
		t.Fatal(err)
	}
	c := f.okCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": "v1"})
	id := c["id"].(string)
	before := len(f.activity(tk.ID))
	u := f.okCall(cs, "update_comment", map[string]any{"comment": id, "body": "v2"})
	if u["id"] != id || u["ticket"] != "WEB-1" || u["updated_at"] == "" || !reflect.DeepEqual(keysOf(u), []string{"author", "id", "ticket", "updated_at"}) {
		t.Fatal(u)
	}
	d := f.okCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	cm := d["comments"].([]any)[1].(map[string]any)
	if cm["body"] != "v2" || cm["edited_at"] == nil || cm["author_kind"] != "api_token" {
		t.Fatal(cm)
	}
	f.okCall(cs, "update_comment", map[string]any{"comment": id, "body": "v2"}) // identical
	if len(f.activity(tk.ID)) != before {
		t.Fatal("comment edit wrote activity")
	}
	f.errCall(cs, "update_comment", map[string]any{"comment": userC.ID, "body": "hack"}, "You can only edit comments written by this token.")
	f.errCall(other, "update_comment", map[string]any{"comment": id, "body": "hack"}, "You can only edit comments written by this token.")
	f.errCall(cs, "update_comment", map[string]any{"comment": "01J00000000000000000000000", "body": "x"}, "No comment with id")
	f.errCall(cs, "update_comment", map[string]any{"comment": id, "body": ""}, "body is required.")
	f.errCall(cs, "update_comment", map[string]any{"comment": id, "body": strings.Repeat("a", 20001)}, "body is too long")
	// archived project
	f.env.Archive(t, f.web.ID)
	f.errCall(cs, "update_comment", map[string]any{"comment": id, "body": "late"}, "Project WEB is archived and read-only.")
}

func TestUpdateCommentDemotedAuthorAndReadToken(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	cs, _ := f.as(f.bob, service.ScopeWrite)
	c := f.okCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": "v1"})
	// demote bob to viewer
	f.demote(f.web.ID, f.bob.ID)
	f.errCall(cs, "update_comment", map[string]any{"comment": c["id"], "body": "v2"}, "Your role in WEB is viewer. Editors and owners can do this.")
	f.errCall(cs, "add_comment", map[string]any{"ticket": "WEB-1", "body": "v2"}, "Your role in WEB is viewer.")
	rcs, _ := f.as(f.alice, service.ScopeRead)
	for _, n := range toolNames(t, rcs) {
		if n == "update_comment" {
			t.Fatal("read token sees update_comment")
		}
	}
}

func (f *fx) demote(projectID, userID string) {
	f.t.Helper()
	ctx := context.Background()
	if _, err := f.env.Svc.Members.SetRole(ctx, f.aliceActor(), projectID, userID, service.RoleViewer); err != nil {
		f.t.Fatal(err)
	}
}

func TestProjectAndLabelTools(t *testing.T) {
	f := newFx(t)
	cs, _ := f.as(f.bob, service.ScopeWrite)
	p := f.okCall(cs, "create_project", map[string]any{"name": "Docs", "key": "doc", "description": "d"})
	if p["key"] != "DOC" || p["role"] != "owner" || p["url"] != testBase+"/p/DOC" || p["archived"] != false {
		t.Fatal(p)
	}
	f.errCall(cs, "create_project", map[string]any{"name": "Docs", "key": "DOC"}, "Key DOC is already taken. Choose another key.")
	f.errCall(cs, "create_project", map[string]any{"name": "Docs", "key": "web1"}, `Invalid key "web1". Use 2-6 letters.`)
	// editor cannot update project
	f.errCall(cs, "update_project", map[string]any{"project": "WEB", "name": "x"}, "Your role in WEB is editor. Only owners can update a project.")
	f.errCall(cs, "update_project", map[string]any{"project": "DOC"}, "Pass at least one of name, description, archived.")
	u := f.okCall(cs, "update_project", map[string]any{"project": "DOC", "name": "Documents", "archived": true})
	if u["name"] != "Documents" || u["archived"] != true || u["role"] != "owner" {
		t.Fatal(u)
	}
	// archived: writes fail, reads work, unarchive works
	f.errCall(cs, "create_ticket", map[string]any{"project": "DOC", "title": "x"}, "Project DOC is archived and read-only.")
	f.errCall(cs, "create_label", map[string]any{"project": "DOC", "name": "x"}, "Project DOC is archived and read-only.")
	f.okCall(cs, "list_tickets", map[string]any{"project": "DOC"})
	f.okCall(cs, "update_project", map[string]any{"project": "DOC", "archived": false})
	f.okCall(cs, "create_ticket", map[string]any{"project": "DOC", "title": "x"})
	// labels
	l := f.okCall(cs, "create_label", map[string]any{"project": "WEB", "name": "ui", "color": "teal"})
	if l["name"] != "ui" || l["color"] != "teal" {
		t.Fatal(l)
	}
	if l := f.okCall(cs, "create_label", map[string]any{"project": "WEB", "name": "plain"}); l["color"] != "gray" {
		t.Fatal(l)
	}
	f.errCall(cs, "create_label", map[string]any{"project": "WEB", "name": "UI"}, `Label "UI" already exists in WEB.`)
	f.errCall(cs, "create_label", map[string]any{"project": "WEB", "name": "z", "color": "magenta"}, `Invalid color "magenta". Valid colors: gray, red, orange, amber, green, teal, blue, indigo, purple, pink.`)
	// viewer role error on a write tool
	vcs, _ := f.as(f.carol, service.ScopeWrite)
	f.errCall(vcs, "create_ticket", map[string]any{"project": "WEB", "title": "x"}, "Your role in WEB is viewer. Editors and owners can do this.")
	f.errCall(vcs, "create_label", map[string]any{"project": "WEB", "name": "x"}, "Your role in WEB is viewer.")
}

func TestScopeAndSurface(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	read := []string{"get_ticket", "list_labels", "list_members", "list_projects", "list_tickets"}
	write := append([]string{"add_comment", "create_label", "create_project", "create_ticket", "delete_ticket", "move_ticket", "update_comment", "update_project", "update_ticket"}, read...)
	sort.Strings(write)
	rcs, _ := f.as(f.alice, service.ScopeRead)
	names := toolNames(t, rcs)
	sort.Strings(names)
	if !reflect.DeepEqual(names, read) {
		t.Fatal(names)
	}
	wcs, _ := f.as(f.alice, service.ScopeWrite)
	names = toolNames(t, wcs)
	sort.Strings(names)
	if !reflect.DeepEqual(names, write) || len(names) != 14 {
		t.Fatal(names)
	}
	for _, n := range names {
		if strings.Contains(n, "delete_project") || strings.Contains(n, "token") || strings.Contains(n, "member") && n != "list_members" {
			t.Fatal(n)
		}
	}
	// a direct call to a hidden write tool fails and changes nothing
	if _, err := rcs.CallTool(context.Background(), &mcpCall{Name: "delete_ticket", Arguments: map[string]any{"ticket": "WEB-1", "confirm": true}}); err == nil {
		t.Fatal("hidden tool call must fail")
	}
	f.okCall(rcs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	// service still enforces scope even when the tool wiring is bypassed
	p, _ := f.principal(f.alice, service.ScopeRead, nil)
	c := &call{ctx: context.Background(), h: f.h, p: p, actor: p.Actor(), tool: "x"}
	if r := c.mapErr(mustErr(f.env.Svc.Tickets.Delete(context.Background(), p.Actor(), "WEB-1")), errOpts{}); !strings.Contains(textOf(r), "read-only scope") {
		t.Fatal(textOf(r))
	}
	// annotations
	lr, _ := wcs.ListTools(context.Background(), nil)
	for _, tl := range lr.Tools {
		a := tl.Annotations
		if a == nil || a.OpenWorldHint == nil || *a.OpenWorldHint || tl.Title == "" {
			t.Fatalf("%s annotations", tl.Name)
		}
		isRead := strings.HasPrefix(tl.Name, "list_") || tl.Name == "get_ticket"
		if a.ReadOnlyHint != isRead {
			t.Fatalf("%s readonly", tl.Name)
		}
		if (tl.Name == "delete_ticket") != (a.DestructiveHint != nil && *a.DestructiveHint) {
			t.Fatalf("%s destructive", tl.Name)
		}
		if strings.HasPrefix(tl.Name, "update_") || tl.Name == "move_ticket" {
			if !a.IdempotentHint {
				t.Fatalf("%s idempotent", tl.Name)
			}
		}
	}
	// editor: create_project works, update_project owner-required (covered above); read token role viewer
}

func TestProjectLimitedToken(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	f.seedTicket("OPS", "o", "todo")
	p, _ := f.principal(f.alice, service.ScopeWrite, &f.web)
	cs := f.connect(p)
	pl := items(f.okCall(cs, "list_projects", nil))
	if len(pl) != 1 || pl[0]["key"] != "WEB" {
		t.Fatal(pl)
	}
	limited := "This token is limited to project WEB"
	f.errCall(cs, "list_members", map[string]any{"project": "OPS"}, limited)
	f.errCall(cs, "list_members", map[string]any{"project": "NOPE"}, limited)
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "OPS-1"}, limited)
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "OPS-99"}, limited)
	opsTk, _ := f.env.Svc.Tickets.Get(context.Background(), f.aliceActor(), "OPS-1")
	f.errCall(cs, "get_ticket", map[string]any{"ticket": opsTk.ID}, limited)
	f.errCall(cs, "create_ticket", map[string]any{"project": "OPS", "title": "x"}, limited)
	f.errCall(cs, "move_ticket", map[string]any{"ticket": "OPS-1", "status": "done"}, limited)
	f.errCall(cs, "delete_ticket", map[string]any{"ticket": "OPS-1", "confirm": true}, limited)
	f.errCall(cs, "add_comment", map[string]any{"ticket": "OPS-1", "body": "x"}, limited)
	f.errCall(cs, "create_project", map[string]any{"name": "N", "key": "NN"}, "This token is limited to project WEB and cannot create projects.")
	oc, _ := f.env.Svc.Comments.Add(context.Background(), f.aliceActor(), "OPS-1", "hi")
	f.errCall(cs, "update_comment", map[string]any{"comment": oc.ID, "body": "x"}, limited)
	// known-in-limit missing number keeps the normal text
	f.errCall(cs, "get_ticket", map[string]any{"ticket": "WEB-99"}, "No ticket WEB-99 in project WEB.")
	// nothing was written in OPS
	if got := f.activity(opsTk.ID); len(got) != 1 {
		t.Fatal(got)
	}
	// an unlimited token never gets the text
	ucs, _ := f.as(f.alice, service.ScopeWrite)
	r := f.call(ucs, "get_ticket", map[string]any{"ticket": "OPS-99"})
	if strings.Contains(r.Text, "limited to") {
		t.Fatal(r.Text)
	}
}

func TestReadToolsWriteNoActivity(t *testing.T) {
	f := newFx(t)
	tk := f.seedTicket("WEB", "a", "todo")
	cs, _ := f.as(f.alice, service.ScopeRead)
	f.okCall(cs, "list_projects", nil)
	f.okCall(cs, "list_tickets", map[string]any{"project": "WEB"})
	f.okCall(cs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	if len(f.activity(tk.ID)) != 1 {
		t.Fatal("reads wrote activity")
	}
	// read token write call via service is rejected: tool hidden, covered in TestScopeAndSurface
}

func TestEventsOnWriteAndNoOp(t *testing.T) {
	f := newFx(t)
	cs, tokID := f.as(f.alice, service.ScopeWrite)
	f.env.Pub.Reset()
	f.okCall(cs, "create_ticket", map[string]any{"project": "WEB", "title": "a"})
	evs := f.env.Pub.Events()
	if len(evs) != 1 || evs[0].Actor.ID != tokID || evs[0].Actor.Type != service.ActorAPIToken {
		t.Fatalf("%+v", evs)
	}
	f.env.Pub.Reset()
	f.okCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "title": "a"}) // no-op
	f.errCall(cs, "update_ticket", map[string]any{"ticket": "WEB-1", "assignee": "zed@x.com"}, "No member")
	if n := len(f.env.Pub.Events()); n != 0 {
		t.Fatalf("%d events for no-op/failed calls", n)
	}
}
