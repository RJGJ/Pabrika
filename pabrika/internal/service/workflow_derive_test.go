package service_test

import (
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func (e *tkEnv) botActor(t *testing.T, name string) service.Actor {
	t.Helper()
	return e.TokenActor(e.NewToken(t, e.M.Users[testutil.Editor].ID, service.ScopeWrite, ""))
}

func strOf(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestTicketLinksDerivedFromDescription(t *testing.T) {
	e := newTk(t)
	tk, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{
		Title: "A", Description: "Links\nPR: https://github.com/o/r/pull/7\nBranch: feat/a-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strOf(tk.PRURL) != "https://github.com/o/r/pull/7" || strOf(tk.Branch) != "feat/a-1" || !tk.HasWorkflow() {
		t.Fatalf("create: %v %v", strOf(tk.PRURL), strOf(tk.Branch))
	}
	// listed without the description, still carries the derived fields
	pg, _ := e.Svc.Tickets.List(ctx, e.Owner, e.P.Key, service.TicketFilter{})
	if len(pg.Items) != 1 || strOf(pg.Items[0].PRURL) != "https://github.com/o/r/pull/7" || strOf(pg.Items[0].Branch) != "feat/a-1" {
		t.Fatalf("list: %+v", pg.Items)
	}
	// description edit re-derives
	tk, err = e.Svc.Tickets.Update(ctx, e.Owner, tk.Ref, service.UpdateTicketInput{
		Description: service.Some("PR: https://github.com/o/r/pull/8"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strOf(tk.PRURL) != "https://github.com/o/r/pull/8" || tk.Branch != nil {
		t.Fatalf("update: %v %v", strOf(tk.PRURL), strOf(tk.Branch))
	}
	// an unrelated edit keeps them
	tk, _ = e.Svc.Tickets.Update(ctx, e.Owner, tk.Ref, service.UpdateTicketInput{Title: service.Some("B")})
	if strOf(tk.PRURL) != "https://github.com/o/r/pull/8" {
		t.Fatal("title edit lost the pr url")
	}
	// removing the line clears it
	tk, _ = e.Svc.Tickets.Update(ctx, e.Owner, tk.Ref, service.UpdateTicketInput{Description: service.Some("nothing")})
	if tk.PRURL != nil || tk.Branch != nil || tk.HasWorkflow() {
		t.Fatalf("cleared: %v", strOf(tk.PRURL))
	}
	// an invalid scheme never becomes a link
	tk, _ = e.Svc.Tickets.Update(ctx, e.Owner, tk.Ref, service.UpdateTicketInput{Description: service.Some("PR: javascript:alert(1)")})
	if tk.PRURL != nil {
		t.Fatal("javascript url stored")
	}
}

func roundOf(t *testing.T, e *tkEnv, ref string) *service.Round {
	t.Helper()
	tk, err := e.Svc.Tickets.Get(ctx, e.Owner, ref)
	if err != nil {
		t.Fatal(err)
	}
	return tk.Round
}

func TestRoundDerivedFromBotComments(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo)
	bot := e.botActor(t, "orch")

	// a human comment is ignored, and so is a malformed marker
	if _, err := e.Svc.Comments.Add(ctx, e.Owner, tk.Ref, "[orchestrator] ROUND 2/3"); err != nil {
		t.Fatal(err)
	}
	if roundOf(t, e, tk.Ref) != nil {
		t.Fatal("human comment set the round")
	}
	if _, err := e.Svc.Comments.Add(ctx, bot, tk.Ref, "[orchestrator] ROUND x/3"); err != nil {
		t.Fatal(err)
	}
	if roundOf(t, e, tk.Ref) != nil {
		t.Fatal("malformed marker set the round")
	}

	c1, err := e.Svc.Comments.Add(ctx, bot, tk.Ref, "[orchestrator] ROUND 1/3")
	if err != nil {
		t.Fatal(err)
	}
	if r := roundOf(t, e, tk.Ref); r == nil || r.N != 1 || r.Max != 3 {
		t.Fatalf("round 1: %+v", r)
	}
	e.Clock.Advance(1_000_000_000)
	c2, err := e.Svc.Comments.Add(ctx, bot, tk.Ref, "[orchestrator] ROUND 2/3\nback to dev")
	if err != nil {
		t.Fatal(err)
	}
	if r := roundOf(t, e, tk.Ref); r == nil || r.N != 2 {
		t.Fatalf("latest wins: %+v", r)
	}
	// a non-orchestrator bot comment does not disturb it
	if _, err := e.Svc.Comments.Add(ctx, bot, tk.Ref, "[developer] PLAN"); err != nil {
		t.Fatal(err)
	}
	if r := roundOf(t, e, tk.Ref); r == nil || r.N != 2 {
		t.Fatalf("other comment: %+v", r)
	}
	// edit updates, delete falls back to the previous marker, then clears
	if _, err := e.Svc.Comments.Edit(ctx, bot, c2.ID, "[orchestrator] ROUND 3/3"); err != nil {
		t.Fatal(err)
	}
	if r := roundOf(t, e, tk.Ref); r == nil || r.N != 3 || r.Max != 3 {
		t.Fatalf("edit: %+v", r)
	}
	if err := e.Svc.Comments.Delete(ctx, bot, c2.ID); err != nil {
		t.Fatal(err)
	}
	if r := roundOf(t, e, tk.Ref); r == nil || r.N != 1 {
		t.Fatalf("delete falls back: %+v", r)
	}
	if err := e.Svc.Comments.Delete(ctx, bot, c1.ID); err != nil {
		t.Fatal(err)
	}
	if roundOf(t, e, tk.Ref) != nil {
		t.Fatal("round not cleared")
	}
}

func TestRoundOwnerDeletesBotComment(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo)
	bot := e.botActor(t, "orch")
	c, _ := e.Svc.Comments.Add(ctx, bot, tk.Ref, "[orchestrator] ROUND 1/2")
	if err := e.Svc.Comments.Delete(ctx, e.Owner, c.ID); err != nil {
		t.Fatal(err)
	}
	if roundOf(t, e, tk.Ref) != nil {
		t.Fatal("owner deleting the marker did not clear the round")
	}
}
