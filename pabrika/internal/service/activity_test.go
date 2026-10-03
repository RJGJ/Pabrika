package service_test

import (
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
)

func TestActivitySvcListNewestFirstAndPaging(t *testing.T) {
	e := newTk(t)
	lbl := e.label(t, e.P.ID, "bug")
	ed := e.M.Users["editor"]
	agentID := e.NewToken(t, e.M.Users["owner"].ID, service.ScopeWrite, "")
	agent := e.TokenActor(agentID)

	due := "2026-05-06"
	tk, err := e.Svc.Tickets.Create(ctx, e.Owner, "WEB", service.CreateTicketInput{
		Title: "t", Description: "d", DueDate: &due, AssigneeID: &ed.ID, LabelIDs: []string{lbl},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(time.Second)
	if _, err := e.Svc.Tickets.Update(ctx, e.Editor, tk.Ref, service.UpdateTicketInput{Title: service.Some("t2")}); err != nil {
		t.Fatal(err)
	}
	// same-millisecond rows are still ordered newest first (monotonic ids)
	if _, err := e.Svc.Tickets.Move(ctx, agent, tk.Ref, service.MoveInput{Status: service.StatusDone}); err != nil {
		t.Fatal(err)
	}
	// activity of a deleted ticket is not listable through the service (the ticket is gone), checked below
	if err := e.Svc.Tickets.Delete(ctx, e.Owner, tk.Ref); err != nil {
		t.Fatal(err)
	}
	// re-create the history on a live ticket for the listing assertions
	tk2 := e.create(t, "live", "")
	e.Clock.Advance(time.Second)
	if _, err := e.Svc.Tickets.Update(ctx, e.Editor, tk2.Ref, service.UpdateTicketInput{Priority: service.Some(service.PriorityHigh)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Tickets.Move(ctx, agent, tk2.Ref, service.MoveInput{Status: service.StatusDone}); err != nil {
		t.Fatal(err)
	}
	page, err := e.Svc.Activity.List(ctx, e.Viewer, tk2.ID, 0, "")
	if err != nil || len(page.Items) != 3 || page.NextCursor != "" {
		t.Fatalf("%+v %v", page, err)
	}
	if page.Items[0].Action != "moved" || page.Items[1].Action != "updated" || page.Items[2].Action != "created" {
		t.Fatalf("order %s %s %s", page.Items[0].Action, page.Items[1].Action, page.Items[2].Action)
	}
	mv := page.Items[0]
	if mv.Actor.Type != service.ActorAPIToken || mv.Actor.ID != agentID || mv.Actor.OwnerName != "Owner WEB" || mv.TicketID != tk2.ID {
		t.Fatalf("token actor %+v", mv.Actor)
	}
	if mv.Changes["status"] != [2]any{"todo", "done"} {
		t.Fatalf("changes %v", mv.Changes)
	}
	if _, ok := mv.Changes["position"][0].(float64); !ok || mv.CreatedAt.IsZero() {
		t.Fatalf("position change %v", mv.Changes)
	}
	cr := page.Items[2]
	if cr.Actor.Type != service.ActorUser || cr.Actor.Name != "Owner WEB" ||
		cr.Changes["title"] != [2]any{nil, "live"} || cr.Changes["status"] != [2]any{nil, "todo"} || cr.Changes["priority"] != [2]any{nil, "medium"} {
		t.Fatalf("created %+v", cr)
	}
	if _, ok := cr.Changes["position"]; ok {
		t.Fatal("created row must not carry position")
	}

	// cursor paging newest first
	p1, err := e.Svc.Activity.List(ctx, e.Viewer, tk2.Ref, 2, "")
	if err != nil || len(p1.Items) != 2 || p1.NextCursor == "" || p1.Items[0].ID != mv.ID {
		t.Fatalf("%+v %v", p1, err)
	}
	p2, err := e.Svc.Activity.List(ctx, e.Viewer, tk2.Ref, 2, p1.NextCursor)
	if err != nil || len(p2.Items) != 1 || p2.NextCursor != "" || p2.Items[0].ID != cr.ID {
		t.Fatalf("%+v %v", p2, err)
	}
	_, err = e.Svc.Activity.List(ctx, e.Viewer, tk2.Ref, 2, "bogus")
	wantSvcErr(t, err, service.KindBadRequest, service.CodeInvalidCursor)

	// the first ticket's history: created with every optional field, activity kept after soft delete
	rows, err := e.Store.Read().ActivitySeedListRaw(ctx, tk.ID)
	if err != nil || len(rows) != 4 || rows[0].Action != "created" || rows[3].Action != "deleted" {
		t.Fatalf("%+v %v", rows, err)
	}
	_, err = e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 0, "")
	wantSvcErr(t, err, service.KindNotFound, "")
}

func TestActivitySvcCreatedChanges(t *testing.T) {
	e := newTk(t)
	lbl := e.label(t, e.P.ID, "bug")
	ed := e.M.Users["editor"]
	due := "2026-05-06"
	tk, err := e.Svc.Tickets.Create(ctx, e.Owner, "WEB", service.CreateTicketInput{
		Title: "t", Description: "d", Priority: service.PriorityLow, DueDate: &due, AssigneeID: &ed.ID, LabelIDs: []string{lbl},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 0, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	ch := page.Items[0].Changes
	want := map[string]bool{"title": true, "status": true, "priority": true, "description": true, "due_date": true, "assignee": true, "labels": true}
	if len(ch) != len(want) {
		t.Fatalf("keys %v", ch)
	}
	for k := range ch {
		if !want[k] {
			t.Fatalf("unexpected key %s", k)
		}
	}
	if ch["assignee"] != [2]any{nil, ed.ID} || ch["due_date"] != [2]any{nil, due} {
		t.Fatalf("%v", ch)
	}
	if names, _ := ch["labels"][1].([]any); len(names) != 1 || names[0] != "bug" {
		t.Fatalf("labels %v", ch["labels"])
	}
}
