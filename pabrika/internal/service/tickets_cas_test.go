package service_test

import (
	"sync"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func statusPtr(s service.Status) *service.Status { return &s }

func activityLen(t *testing.T, e *tkEnv, ref string) int {
	t.Helper()
	p, err := e.Svc.Activity.List(ctx, e.Owner, ref, 200, "")
	if err != nil {
		t.Fatal(err)
	}
	return len(p.Items)
}

func TestTicketSvcMoveExpectedStatusMatch(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	r := e.move(t, a.Ref, service.MoveInput{Status: service.StatusInProgress, ExpectedStatus: statusPtr(service.StatusTodo)})
	if r.Ticket.Status != service.StatusInProgress {
		t.Fatalf("status %v", r.Ticket.Status)
	}
	// same-status reorder with a matching expectation works
	b := e.create(t, "B", service.StatusInProgress)
	r = e.move(t, b.Ref, service.MoveInput{Status: service.StatusInProgress, Place: "top", ExpectedStatus: statusPtr(service.StatusInProgress)})
	if r.Ticket.Status != service.StatusInProgress {
		t.Fatal("reorder changed status")
	}
	// works together with before/after
	c := e.create(t, "C", service.StatusTodo)
	r = e.move(t, c.Ref, service.MoveInput{Status: service.StatusInProgress, After: a.ID, ExpectedStatus: statusPtr(service.StatusTodo)})
	if r.Ticket.Status != service.StatusInProgress {
		t.Fatal("after + expected failed")
	}
}

func TestTicketSvcMoveExpectedStatusMismatch(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	e.Pub.Reset()
	acts := activityLen(t, e, a.Ref)
	_, err := e.Svc.Tickets.Move(ctx, e.Owner, a.Ref, service.MoveInput{Status: service.StatusDone, ExpectedStatus: statusPtr(service.StatusBacklog)})
	se := wantSvcErr(t, err, service.KindConflict, service.CodeStatusConflict)
	if se.Message != "Ticket is in todo, not backlog" {
		t.Fatalf("message %q", se.Message)
	}
	got, _ := e.Svc.Tickets.Get(ctx, e.Owner, a.Ref)
	if got.Status != service.StatusTodo || got.Position != a.Position || !got.UpdatedAt.Equal(a.UpdatedAt) {
		t.Fatalf("ticket changed: %+v", got)
	}
	if activityLen(t, e, a.Ref) != acts {
		t.Fatal("conflict wrote activity")
	}
	if n := len(e.Pub.Events()); n != 0 {
		t.Fatalf("conflict published %d events", n)
	}
}

func TestTicketSvcMoveExpectedStatusInvalid(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	bad := service.Status("nope")
	_, err := e.Svc.Tickets.Move(ctx, e.Owner, a.Ref, service.MoveInput{Status: service.StatusDone, ExpectedStatus: &bad})
	se := wantSvcErr(t, err, service.KindValidation, "")
	if _, ok := se.Fields["expected_status"]; !ok {
		t.Fatalf("fields %v", se.Fields)
	}
}

func TestTicketSvcMoveExpectedStatusBeforeAnchorCheck(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	// the anchor is invalid AND the expectation is wrong: the conflict wins
	_, err := e.Svc.Tickets.Move(ctx, e.Owner, a.Ref, service.MoveInput{
		Status: service.StatusDone, Before: "WEB-999", ExpectedStatus: statusPtr(service.StatusBacklog)})
	wantSvcErr(t, err, service.KindConflict, service.CodeStatusConflict)
}

func TestTicketSvcMoveWithoutExpectedStatusUnchanged(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	r := e.move(t, a.Ref, service.MoveInput{Status: service.StatusDone})
	if r.Ticket.Status != service.StatusDone {
		t.Fatal("old behaviour broken")
	}
}

func TestTicketSvcConcurrentClaim(t *testing.T) {
	e := newTkFrom(t, testutil.NewFileEnv(t))
	a := e.create(t, "A", service.StatusTodo)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.Svc.Tickets.Move(ctx, e.Owner, a.Ref, service.MoveInput{
				Status: service.StatusInProgress, ExpectedStatus: statusPtr(service.StatusTodo)})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			wins++
			continue
		}
		wantSvcErr(t, err, service.KindConflict, service.CodeStatusConflict)
		conflicts++
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("wins %d conflicts %d", wins, conflicts)
	}
	p, _ := e.Svc.Activity.List(ctx, e.Owner, a.Ref, 200, "")
	moved := 0
	for _, it := range p.Items {
		if it.Action == "moved" {
			moved++
		}
	}
	if moved != 1 {
		t.Fatalf("moved rows %d", moved)
	}
}
