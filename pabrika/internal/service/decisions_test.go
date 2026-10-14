package service_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store/db"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// gateTicket makes a ticket that carries a gate label (applied through ChangeLabels so the
// label-change activity row exists).
func (e *tkEnv) gateTicket(t *testing.T, label string) (service.Ticket, string) {
	t.Helper()
	tk := e.create(t, "gate", service.StatusTodo)
	lid := e.label(t, e.P.ID, label)
	e.Clock.Advance(time.Second)
	e.change(t, tk.Ref, []string{lid}, nil)
	return tk, lid
}

func wantCode(t *testing.T, err error, kind service.Kind, code string) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) || se.Kind != kind || (code != "" && se.Code != code) {
		t.Fatalf("want kind %v code %q, got %v", kind, code, err)
	}
}

func approve(g service.Gate) service.RecordDecisionInput {
	return service.RecordDecisionInput{Gate: g, Verdict: service.VerdictApproved}
}

func TestDecisionRecordRoles(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	e.Clock.Advance(time.Second)

	for who, role := range map[testutil.Who]service.Role{testutil.Owner: service.RoleOwner, testutil.Editor: service.RoleEditor} {
		d, err := e.Svc.Decisions.Record(ctx, e.UserActor(e.M.Users[who]), tk.Ref, approve(service.GatePlan))
		if err != nil {
			t.Fatalf("%s: %v", who, err)
		}
		if d.ActorRole != role || d.GateLabel != service.LabelAwaitingPlanApproval || d.Actor.ID != e.M.Users[who].ID ||
			d.Actor.Name == "" || d.Gate != service.GatePlan || d.Verdict != service.VerdictApproved || d.TicketRef != tk.Ref {
			t.Fatalf("%s: %+v", who, d)
		}
		e.Clock.Advance(time.Second)
	}
	_, err := e.Svc.Decisions.Record(ctx, e.Viewer, tk.Ref, approve(service.GatePlan))
	wantCode(t, err, service.KindForbidden, service.CodeForbidden)
	_, err = e.Svc.Decisions.Record(ctx, e.UserActor(e.M.Users[testutil.NonMember]), tk.Ref, approve(service.GatePlan))
	wantCode(t, err, service.KindNotFound, "")
}

func TestDecisionRecordRejectsEveryToken(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	for _, sc := range []service.Scope{service.ScopeRead, service.ScopeComment, service.ScopeWrite} {
		a := e.TokenActor(e.NewToken(t, e.M.Users[testutil.Owner].ID, sc, ""))
		_, err := e.Svc.Decisions.Record(ctx, a, tk.Ref, approve(service.GatePlan))
		wantCode(t, err, service.KindForbidden, service.CodeSessionRequired)
	}
	// session_required comes before any lookup: an unknown ticket gives it too
	a := e.TokenActor(e.NewToken(t, e.M.Users[testutil.Owner].ID, service.ScopeWrite, ""))
	_, err := e.Svc.Decisions.Record(ctx, a, "WEB-999", approve(service.GatePlan))
	wantCode(t, err, service.KindForbidden, service.CodeSessionRequired)
	p, _ := e.Svc.Decisions.List(ctx, e.Owner, tk.Ref, 10, "")
	if len(p.Items) != 0 {
		t.Fatal("a token created a decision")
	}
}

func TestDecisionValidation(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	long := strings.Repeat("x", 2001)
	blank := "   "
	cases := map[string]service.RecordDecisionInput{
		"bad gate":      {Gate: "merge", Verdict: service.VerdictApproved},
		"bad verdict":   {Gate: service.GatePlan, Verdict: "maybe"},
		"changes w/o":   {Gate: service.GatePlan, Verdict: service.VerdictChangesRequested},
		"changes blank": {Gate: service.GatePlan, Verdict: service.VerdictChangesRequested, Note: &blank},
		"note too long": {Gate: service.GatePlan, Verdict: service.VerdictApproved, Note: &long},
	}
	for name, in := range cases {
		_, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, in)
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		wantCode(t, err, service.KindValidation, "")
	}
	ok := strings.Repeat("x", 2000)
	if _, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, service.RecordDecisionInput{Gate: service.GatePlan, Verdict: service.VerdictChangesRequested, Note: &ok}); err != nil {
		t.Fatalf("2000 chars: %v", err)
	}
}

func TestDecisionGateNotOpen(t *testing.T) {
	e := newTk(t)
	tk, lid := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	// wrong gate for the label on the ticket
	_, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GateRequirements))
	wantCode(t, err, service.KindConflict, service.CodeGateNotOpen)
	// label removed -> closed
	e.change(t, tk.Ref, nil, []string{lid})
	_, err = e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GatePlan))
	wantCode(t, err, service.KindConflict, service.CodeGateNotOpen)
	if p, _ := e.Svc.Decisions.List(ctx, e.Owner, tk.Ref, 10, ""); len(p.Items) != 0 {
		t.Fatal("row created for a closed gate")
	}
	if pg, _ := e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 50, ""); pg.Items[0].Action == "decision" {
		t.Fatal("activity row for a closed gate")
	}
}

func TestDecisionArchivedProject(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	e.Archive(t, e.P.ID)
	_, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GatePlan))
	wantCode(t, err, service.KindConflict, service.CodeProjectArchived)
}

func TestDecisionActivityRowEventAndNoLabelChange(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	e.Pub.Reset()
	e.Clock.Advance(time.Second)
	note := "  needs a split  "
	d, err := e.Svc.Decisions.Record(ctx, e.Editor, tk.Ref, service.RecordDecisionInput{Gate: service.GatePlan, Verdict: service.VerdictChangesRequested, Note: &note})
	if err != nil {
		t.Fatal(err)
	}
	if d.Note == nil || *d.Note != "needs a split" {
		t.Fatalf("note: %v", d.Note)
	}
	evs := e.Pub.Events()
	if len(evs) != 1 || evs[0].Type != service.EventDecisionRecorded || evs[0].DecisionID != d.ID || evs[0].TicketID != tk.ID {
		t.Fatalf("events: %+v", evs)
	}
	pg, _ := e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 50, "")
	a := pg.Items[0]
	if a.Action != "decision" || a.Actor.ID != e.M.Users[testutil.Editor].ID ||
		a.Changes["gate"][1] != "changes_requested" || a.Changes["gate_name"][1] != "plan" || a.Changes["note"][1] != "needs a split" {
		t.Fatalf("activity: %+v", a)
	}
	// record-only: labels and status untouched
	got, _ := e.Svc.Tickets.Get(ctx, e.Owner, tk.Ref)
	if len(got.Labels) != 1 || got.Labels[0].Name != service.LabelAwaitingPlanApproval || got.Status != service.StatusTodo {
		t.Fatalf("ticket changed: %+v", got)
	}
}

func TestDecisionNoEventOnFailure(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	e.Pub.Reset()
	_, _ = e.Svc.Decisions.Record(ctx, e.Viewer, tk.Ref, approve(service.GatePlan))
	_, _ = e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GateRequirements))
	if n := len(e.Pub.Events()); n != 0 {
		t.Fatalf("%d events after failures", n)
	}
}

func TestDecisionSupersedeOrderAndCurrent(t *testing.T) {
	e := newTk(t)
	tk, lid := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	note := "split it"
	e.Clock.Advance(time.Second)
	d1, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, service.RecordDecisionInput{Gate: service.GatePlan, Verdict: service.VerdictChangesRequested, Note: &note})
	if err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(time.Second)
	d2, err := e.Svc.Decisions.Record(ctx, e.Editor, tk.Ref, approve(service.GatePlan))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Svc.Decisions.List(ctx, e.Viewer, tk.Ref, 10, "")
	if err != nil || len(p.Items) != 2 || p.Items[0].ID != d2.ID || p.Items[1].ID != d1.ID {
		t.Fatalf("order: %+v %v", p.Items, err)
	}
	got, _ := e.Svc.Tickets.Get(ctx, e.Owner, tk.Ref)
	if !got.DecisionsLoaded || len(got.Decisions) != 1 || got.Decisions[0].Verdict != service.VerdictApproved ||
		!got.Decisions[0].Current || got.Decisions[0].By.Email == "" {
		t.Fatalf("latest: %+v", got.Decisions)
	}
	if !got.HasWorkflow() {
		t.Fatal("workflow should be set")
	}
	// the gate is re-opened: remove and re-apply the label -> the old approval is not current
	e.Clock.Advance(time.Second)
	e.change(t, tk.Ref, nil, []string{lid})
	e.Clock.Advance(time.Second)
	e.change(t, tk.Ref, []string{lid}, nil)
	got, _ = e.Svc.Tickets.Get(ctx, e.Owner, tk.Ref)
	if got.Decisions[0].Current {
		t.Fatal("stale decision still current after the gate label was re-applied")
	}
	// a new decision is current again
	e.Clock.Advance(time.Second)
	if _, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GatePlan)); err != nil {
		t.Fatal(err)
	}
	got, _ = e.Svc.Tickets.Get(ctx, e.Owner, tk.Ref)
	if !got.Decisions[0].Current {
		t.Fatal("new decision should be current")
	}
	// list items never carry decisions
	pg, _ := e.Svc.Tickets.List(ctx, e.Owner, e.P.Key, service.TicketFilter{})
	if pg.Items[0].DecisionsLoaded || pg.Items[0].Decisions != nil {
		t.Fatal("list item carries decisions")
	}
}

func TestDecisionCurrentWhenLabelSeededWithoutActivity(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "x", service.StatusTodo)
	lid := e.label(t, e.P.ID, service.LabelAwaitingReqApproval)
	if err := e.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.TicketAddLabel(ctx, db.TicketAddLabelParams{TicketID: tk.ID, LabelID: lid})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GateRequirements)); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Svc.Tickets.Get(ctx, e.Owner, tk.Ref)
	if !got.Decisions[0].Current {
		t.Fatal("seeded label: decision should be current")
	}
}

func TestDecisionListPaginationAndTokenReads(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	for i := 0; i < 5; i++ {
		e.Clock.Advance(time.Second)
		if _, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GatePlan)); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	cur := ""
	for {
		p, err := e.Svc.Decisions.List(ctx, e.Owner, tk.Ref, 2, cur)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range p.Items {
			ids = append(ids, d.ID)
		}
		if p.NextCursor == "" {
			break
		}
		cur = p.NextCursor
	}
	if len(ids) != 5 {
		t.Fatalf("got %d", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] <= ids[i] {
			t.Fatalf("not newest first: %v", ids)
		}
	}
	rt := e.TokenActor(e.NewToken(t, e.M.Users[testutil.Owner].ID, service.ScopeRead, ""))
	if p, err := e.Svc.Decisions.List(ctx, rt, tk.Ref, 10, ""); err != nil || len(p.Items) != 5 {
		t.Fatalf("read token: %v %d", err, len(p.Items))
	}
	_, err := e.Svc.Decisions.List(ctx, e.UserActor(e.M.Users[testutil.NonMember]), tk.Ref, 10, "")
	wantCode(t, err, service.KindNotFound, "")
	_, err = e.Svc.Decisions.List(ctx, e.Owner, tk.Ref, 10, "garbage")
	wantCode(t, err, service.KindBadRequest, service.CodeInvalidCursor)
}

func TestDecisionSoftDeletedTicketHidden(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingPlanApproval)
	d, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GatePlan))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.Tickets.Delete(ctx, e.Owner, tk.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Decisions.List(ctx, e.Owner, tk.Ref, 10, ""); err == nil {
		t.Fatal("list on a deleted ticket")
	}
	// the row still exists but no reader returns it
	err = e.Store.WithReadTx(ctx, func(q *db.Queries) error {
		if _, err := q.DecisionGetView(ctx, d.ID); err == nil {
			t.Fatal("DecisionGetView returned a decision of a deleted ticket")
		}
		if rows, _ := q.DecisionLatestPerGate(ctx, tk.ID); len(rows) != 0 {
			t.Fatal("DecisionLatestPerGate returned rows of a deleted ticket")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDecisionKeepsRoleAtTheTime(t *testing.T) {
	e := newTk(t)
	tk, _ := e.gateTicket(t, service.LabelAwaitingReqApproval)
	d, err := e.Svc.Decisions.Record(ctx, e.Editor, tk.Ref, approve(service.GateRequirements))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Members.SetRole(ctx, e.Owner, e.P.Key, e.M.Users[testutil.Editor].ID, service.RoleViewer); err != nil {
		t.Fatal(err)
	}
	p, _ := e.Svc.Decisions.List(ctx, e.Owner, tk.Ref, 10, "")
	if p.Items[0].ID != d.ID || p.Items[0].ActorRole != service.RoleEditor {
		t.Fatalf("%+v", p.Items[0])
	}
}
