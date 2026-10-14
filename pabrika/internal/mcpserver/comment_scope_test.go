package mcpserver

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

func TestCommentTokenSurface(t *testing.T) {
	f := newFx(t)
	f.seedTicket("WEB", "a", "todo")
	ccs, _ := f.as(f.alice, service.ScopeComment)

	// exactly the 5 read tools plus the 2 comment tools
	want := []string{"add_comment", "get_ticket", "list_labels", "list_members", "list_projects", "list_tickets", "update_comment"}
	names := toolNames(t, ccs)
	sort.Strings(names)
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("comment token tools %v", names)
	}
	// counts per scope: 5 / 7 / 16
	rcs, _ := f.as(f.alice, service.ScopeRead)
	wcs, _ := f.as(f.alice, service.ScopeWrite)
	if n := len(toolNames(t, rcs)); n != 5 {
		t.Fatalf("read tools %d", n)
	}
	if n := len(toolNames(t, wcs)); n != 16 {
		t.Fatalf("write tools %d", n)
	}

	// the comment tools work, annotated as non-destructive writes
	c := f.okCall(ccs, "add_comment", map[string]any{"ticket": "WEB-1", "body": "reviewed"})
	id, _ := c["id"].(string)
	if id == "" {
		t.Fatal(c)
	}
	f.okCall(ccs, "update_comment", map[string]any{"comment": id, "body": "reviewed, edited"})
	f.okCall(ccs, "get_ticket", map[string]any{"ticket": "WEB-1"})
	tk, _ := f.env.Svc.Tickets.Get(context.Background(), f.aliceActor(), "WEB-1")
	if got := len(f.activity(tk.ID)); got != 1 { // only the creation row: comments are not ticket activity
		t.Fatalf("activity rows %d", got)
	}
	lr, _ := ccs.ListTools(context.Background(), nil)
	for _, tl := range lr.Tools {
		if tl.Name == "add_comment" || tl.Name == "update_comment" {
			a := tl.Annotations
			if a.ReadOnlyHint || (a.DestructiveHint != nil && *a.DestructiveHint) {
				t.Fatalf("%s annotations %+v", tl.Name, a)
			}
		}
	}

	// every other tool is hidden: a direct call fails as unknown and changes nothing
	for _, n := range []string{"update_ticket", "move_ticket", "delete_ticket", "create_ticket", "add_label", "remove_label", "create_label", "create_project", "update_project"} {
		if _, err := ccs.CallTool(context.Background(), &mcpCall{Name: n, Arguments: map[string]any{"ticket": "WEB-1", "confirm": true}}); err == nil {
			t.Fatalf("hidden tool %s was callable", n)
		}
	}
	after, _ := f.env.Svc.Tickets.Get(context.Background(), f.aliceActor(), "WEB-1")
	if after.Title != "a" || after.Status != "todo" {
		t.Fatalf("ticket changed: %+v", after)
	}

	// the instructions say what the token can do
	instr := ccs.InitializeResult().Instructions
	if !strings.Contains(instr, "This token can read and comment; ticket changes are not available.") || strings.Contains(instr, "read-only") {
		t.Fatalf("instructions %q", instr)
	}
	if ri := rcs.InitializeResult().Instructions; !strings.Contains(ri, "This token is read-only; write tools are not available.") {
		t.Fatalf("read instructions %q", ri)
	}

	// the service still refuses when the tool wiring is bypassed, with the scope-aware text
	p, _ := f.principal(f.alice, service.ScopeComment, nil)
	cc := &call{ctx: context.Background(), h: f.h, p: p, actor: p.Actor(), tool: "x"}
	r := cc.mapErr(mustErr(f.env.Svc.Tickets.Delete(context.Background(), p.Actor(), "WEB-1")), errOpts{})
	if got := textOf(r); !strings.Contains(got, "can only read tickets and add or edit its own comments") || strings.Contains(got, "read-only scope") {
		t.Fatal(got)
	}
	// and the read-token text is unchanged
	rp, _ := f.principal(f.alice, service.ScopeRead, nil)
	rc := &call{ctx: context.Background(), h: f.h, p: rp, actor: rp.Actor(), tool: "x"}
	if got := textOf(rc.mapErr(mustErr(f.env.Svc.Tickets.Delete(context.Background(), rp.Actor(), "WEB-1")), errOpts{})); !strings.Contains(got, "read-only scope") {
		t.Fatal(got)
	}
}
