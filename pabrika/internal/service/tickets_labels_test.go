package service_test

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func labelNames(tk service.Ticket) []string {
	out := []string{}
	for _, l := range tk.Labels {
		out = append(out, l.Name)
	}
	return out
}

func (e *tkEnv) change(t *testing.T, ref string, add, remove []string) service.Ticket {
	t.Helper()
	tk, err := e.Svc.Tickets.ChangeLabels(ctx, e.Editor, ref, service.ChangeLabelsInput{Add: add, Remove: remove})
	if err != nil {
		t.Fatalf("ChangeLabels: %v", err)
	}
	return tk
}

func TestTicketSvcChangeLabelsBasics(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo)
	bug := e.label(t, e.P.ID, "bug")
	ui := e.label(t, e.P.ID, "ui")
	ops := e.label(t, e.P.ID, "ops")

	got := e.change(t, tk.Ref, []string{bug}, nil)
	if !reflect.DeepEqual(labelNames(got), []string{"bug"}) {
		t.Fatalf("add to empty: %v", labelNames(got))
	}
	got = e.change(t, tk.Ref, []string{ui, ops}, nil)
	if !reflect.DeepEqual(labelNames(got), []string{"bug", "ops", "ui"}) {
		t.Fatalf("add keeps others: %v", labelNames(got))
	}
	got = e.change(t, tk.Ref, nil, []string{ops})
	if !reflect.DeepEqual(labelNames(got), []string{"bug", "ui"}) {
		t.Fatalf("remove one: %v", labelNames(got))
	}
	// swap in one call
	got = e.change(t, tk.Ref, []string{ops}, []string{bug})
	if !reflect.DeepEqual(labelNames(got), []string{"ops", "ui"}) {
		t.Fatalf("swap: %v", labelNames(got))
	}
	// activity row shape
	p, _ := e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 1, "")
	a := p.Items[0]
	if a.Action != "labeled" || !reflect.DeepEqual(a.Changes["labels"], [2]any{[]any{"bug", "ui"}, []any{"ops", "ui"}}) {
		// the activity reader may return typed slices; accept either encoding
		if fmt.Sprint(a.Changes["labels"]) != fmt.Sprint([2]any{[]string{"bug", "ui"}, []string{"ops", "ui"}}) {
			t.Fatalf("activity %+v", a)
		}
	}
}

func TestTicketSvcChangeLabelsNoOps(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo)
	bug := e.label(t, e.P.ID, "bug")
	ui := e.label(t, e.P.ID, "ui")
	tk = e.change(t, tk.Ref, []string{bug}, nil)
	e.Clock.Advance(time.Second)
	e.Pub.Reset()
	acts := activityLen(t, e, tk.Ref)

	got := e.change(t, tk.Ref, []string{bug}, nil) // already there
	if !got.UpdatedAt.Equal(tk.UpdatedAt) {
		t.Fatal("no-op bumped updated_at")
	}
	got = e.change(t, tk.Ref, nil, []string{ui}) // not on the ticket
	if !got.UpdatedAt.Equal(tk.UpdatedAt) {
		t.Fatal("no-op remove bumped updated_at")
	}
	got = e.change(t, tk.Ref, []string{bug}, []string{ui}) // both no-ops
	if !reflect.DeepEqual(labelNames(got), []string{"bug"}) {
		t.Fatalf("labels %v", labelNames(got))
	}
	if n := len(e.Pub.Events()); n != 0 {
		t.Fatalf("no-op published %d events", n)
	}
	if activityLen(t, e, tk.Ref) != acts {
		t.Fatal("no-op wrote activity")
	}
	// a real change: exactly one ticket.updated event and a bumped updated_at
	got = e.change(t, tk.Ref, []string{ui}, nil)
	ev := e.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventTicketUpdated || ev[0].TicketID != tk.ID {
		t.Fatalf("events %+v", ev)
	}
	if !got.UpdatedAt.After(tk.UpdatedAt) || activityLen(t, e, tk.Ref) != acts+1 {
		t.Fatal("real change must bump updated_at and write one activity row")
	}
}

func TestTicketSvcChangeLabelsValidation(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo)
	bug := e.label(t, e.P.ID, "bug")
	other := e.Env.NewProject(t, e.M.Users[testutil.Owner], "OTH")
	foreign := e.label(t, other.ID, "foreign")

	field := func(add, remove []string, want string) {
		t.Helper()
		_, err := e.Svc.Tickets.ChangeLabels(ctx, e.Editor, tk.Ref, service.ChangeLabelsInput{Add: add, Remove: remove})
		se := wantSvcErr(t, err, service.KindValidation, "")
		if _, ok := se.Fields[want]; !ok {
			t.Fatalf("want field %q, got %v", want, se.Fields)
		}
	}
	field(nil, nil, "add")
	field([]string{bug}, []string{bug}, "labels") // overlap
	field([]string{foreign}, nil, "add")          // other project
	var eleven []string
	for i := 0; i < 11; i++ {
		eleven = append(eleven, e.NewID())
	}
	field(eleven, nil, "add")
	field(nil, eleven, "remove")

	// the per-ticket cap: fill to 50, the 51st is rejected
	var ids []string
	for i := 0; i < 51; i++ {
		ids = append(ids, e.label(t, e.P.ID, fmt.Sprintf("l%02d", i)))
	}
	for i := 0; i < 50; i += 10 {
		e.change(t, tk.Ref, ids[i:i+10], nil)
	}
	field(ids[50:], nil, "add")
	// removing one makes room again
	e.change(t, tk.Ref, ids[50:], ids[:1])
}

func TestTicketSvcChangeLabelsAuth(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo)
	bug := e.label(t, e.P.ID, "bug")
	in := service.ChangeLabelsInput{Add: []string{bug}}
	owner := e.M.Users[testutil.Owner]

	_, err := e.Svc.Tickets.ChangeLabels(ctx, e.Viewer, tk.Ref, in)
	wantSvcErr(t, err, service.KindForbidden, service.CodeForbidden)
	_, err = e.Svc.Tickets.ChangeLabels(ctx, e.UserActor(e.M.Users[testutil.NonMember]), tk.Ref, in)
	wantSvcErr(t, err, service.KindNotFound, "")
	readTok := e.TokenActor(e.NewToken(t, owner.ID, service.ScopeRead, ""))
	_, err = e.Svc.Tickets.ChangeLabels(ctx, readTok, tk.Ref, in)
	wantSvcErr(t, err, service.KindForbidden, service.CodeInsufficientScope)
	other := e.Env.NewProject(t, owner, "OTH")
	limited := e.TokenActor(e.NewToken(t, owner.ID, service.ScopeWrite, other.ID))
	_, err = e.Svc.Tickets.ChangeLabels(ctx, limited, tk.Ref, in)
	wantSvcErr(t, err, service.KindNotFound, "")

	// archived project
	arc := e.Env.NewProject(t, owner, "ARC")
	arcTk, err := e.Svc.Tickets.Create(ctx, e.Owner, "ARC", service.CreateTicketInput{Title: "a"})
	if err != nil {
		t.Fatal(err)
	}
	arcLabel := e.label(t, arc.ID, "x")
	e.Env.Archive(t, arc.ID)
	_, err = e.Svc.Tickets.ChangeLabels(ctx, e.Owner, arcTk.Ref, service.ChangeLabelsInput{Add: []string{arcLabel}})
	wantSvcErr(t, err, service.KindConflict, service.CodeProjectArchived)
}

func TestTicketSvcChangeLabelsConcurrentAdds(t *testing.T) {
	e := newTkFrom(t, testutil.NewFileEnv(t))
	a := e.label(t, e.P.ID, "alpha")
	b := e.label(t, e.P.ID, "beta")
	for round := 0; round < 50; round++ {
		tk := e.create(t, fmt.Sprintf("T%02d", round), service.StatusTodo)
		var wg sync.WaitGroup
		for _, id := range []string{a, b} {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				if _, err := e.Svc.Tickets.ChangeLabels(ctx, e.Editor, tk.Ref, service.ChangeLabelsInput{Add: []string{id}}); err != nil {
					t.Error(err)
				}
			}(id)
		}
		wg.Wait()
		got, err := e.Svc.Tickets.Get(ctx, e.Owner, tk.Ref)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(labelNames(got), []string{"alpha", "beta"}) {
			t.Fatalf("round %d lost a label: %v", round, labelNames(got))
		}
	}
}
