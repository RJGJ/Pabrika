package mcpserver

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
)

// gateFixture makes a ticket wait at the plan gate (label applied through add_label so the
// label-change activity row exists).
func (f *fx) gateFixture() (service.Ticket, string) {
	f.t.Helper()
	f.seedLabel("WEB", service.LabelAwaitingPlanApproval)
	tk := f.seedTicket("WEB", "gated", service.StatusTodo)
	f.env.Clock.Advance(time.Second)
	cs, _ := f.as(f.alice, service.ScopeWrite)
	f.okCall(cs, "add_label", map[string]any{"ticket": tk.Ref, "labels": []string{service.LabelAwaitingPlanApproval}})
	return tk, service.LabelAwaitingPlanApproval
}

func TestGetTicketWorkflowAndDecisions(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	cs, _ := f.as(f.alice, service.ScopeWrite)

	// plain ticket: workflow is null
	plain := f.seedTicket("WEB", "plain", service.StatusTodo)
	if g := f.okCall(cs, "get_ticket", map[string]any{"ticket": plain.Ref}); g["workflow"] != nil {
		t.Fatalf("workflow should be null: %v", g["workflow"])
	}

	tk, label := f.gateFixture()
	if _, err := f.env.Svc.Tickets.Update(ctx, f.aliceActor(), tk.Ref, service.UpdateTicketInput{
		Description: service.Some("PR: https://github.com/o/r/pull/9\nBranch: pabrika/WEB-2")}); err != nil {
		t.Fatal(err)
	}
	bot, _ := f.as(f.bob, service.ScopeWrite)
	f.okCall(bot, "add_comment", map[string]any{"ticket": tk.Ref, "body": "[orchestrator] ROUND 1/3"})

	// lists carry the summary workflow without decisions
	lst := f.okCall(cs, "list_tickets", map[string]any{"project": "WEB"})
	var summary map[string]any
	for _, it := range lst["items"].([]any) {
		if m := it.(map[string]any); m["ref"] == tk.Ref {
			summary = m["workflow"].(map[string]any)
		}
	}
	if summary["pr_url"] != "https://github.com/o/r/pull/9" || summary["branch"] != "pabrika/WEB-2" || summary["round"].(map[string]any)["n"] != float64(1) {
		t.Fatalf("summary workflow: %v", summary)
	}
	if _, has := summary["decisions"]; has {
		t.Fatal("list item carries decisions")
	}

	// before any decision: decisions is an empty array
	g := f.okCall(cs, "get_ticket", map[string]any{"ticket": tk.Ref})
	wf := g["workflow"].(map[string]any)
	if ds, ok := wf["decisions"].([]any); !ok || len(ds) != 0 {
		t.Fatalf("decisions: %v", wf["decisions"])
	}
	if wf["pr_url"] != "https://github.com/o/r/pull/9" {
		t.Fatalf("detail workflow: %v", wf)
	}

	// a person decides (session), the Orchestrator reads it
	f.env.Clock.Advance(time.Second)
	note := "looks right"
	if _, err := f.env.Svc.Decisions.Record(ctx, service.UserActor(f.bob.ID), tk.Ref,
		service.RecordDecisionInput{Gate: service.GatePlan, Verdict: service.VerdictApproved, Note: &note}); err != nil {
		t.Fatal(err)
	}
	g = f.okCall(cs, "get_ticket", map[string]any{"ticket": tk.Ref})
	ds := g["workflow"].(map[string]any)["decisions"].([]any)
	d := ds[0].(map[string]any)
	by := d["by"].(map[string]any)
	if len(ds) != 1 || d["gate"] != "plan" || d["verdict"] != "approved" || d["note"] != "looks right" || d["current"] != true ||
		by["name"] != "Bob" || by["email"] != "bob@x.com" || by["role"] != "editor" || by["id"] != f.bob.ID || d["at"] == "" {
		t.Fatalf("decision: %v", d)
	}
	// the gate is re-opened (label removed and applied again): the old approval is not current
	f.env.Clock.Advance(time.Second)
	f.okCall(cs, "remove_label", map[string]any{"ticket": tk.Ref, "labels": []string{label}})
	f.env.Clock.Advance(time.Second)
	f.okCall(cs, "add_label", map[string]any{"ticket": tk.Ref, "labels": []string{label}})
	g = f.okCall(cs, "get_ticket", map[string]any{"ticket": tk.Ref})
	if d := g["workflow"].(map[string]any)["decisions"].([]any)[0].(map[string]any); d["current"] != false {
		t.Fatalf("stale decision still current: %v", d)
	}
}

func TestListDecisionsTool(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	tk, _ := f.gateFixture()
	for i := 0; i < 3; i++ {
		f.env.Clock.Advance(time.Second)
		if _, err := f.env.Svc.Decisions.Record(ctx, service.UserActor(f.bob.ID), tk.Ref,
			service.RecordDecisionInput{Gate: service.GatePlan, Verdict: service.VerdictApproved}); err != nil {
			t.Fatal(err)
		}
	}
	rcs, _ := f.as(f.carol, service.ScopeRead) // a viewer with a read token may read
	var ids []string
	cursor := ""
	for page := 0; page < 5; page++ {
		args := map[string]any{"ticket": tk.Ref, "limit": 2}
		if cursor != "" {
			args["cursor"] = cursor
		}
		out := f.okCall(rcs, "list_decisions", args)
		for _, it := range out["items"].([]any) {
			m := it.(map[string]any)
			if m["gate_label"] != "awaiting-plan-approval" || m["by"].(map[string]any)["email"] != "bob@x.com" {
				t.Fatalf("item: %v", m)
			}
			ids = append(ids, m["id"].(string))
		}
		if out["next_cursor"] == nil {
			break
		}
		cursor = out["next_cursor"].(string)
	}
	if len(ids) != 3 || !(ids[0] > ids[1] && ids[1] > ids[2]) {
		t.Fatalf("want 3 ids newest first, got %v", ids)
	}
	f.errCall(rcs, "list_decisions", map[string]any{"ticket": tk.Ref, "limit": 0}, "limit must be between 1 and 200")
	f.errCall(rcs, "list_decisions", map[string]any{"ticket": "WEB-999"}, "WEB-999")

	// a token limited to another project and a non-member see nothing
	opsTok, _ := f.principal(f.alice, service.ScopeRead, &f.ops)
	f.errCall(f.connect(opsTok), "list_decisions", map[string]any{"ticket": tk.Ref}, "")
	dcs, _ := f.as(f.dave, service.ScopeRead)
	f.errCall(dcs, "list_decisions", map[string]any{"ticket": tk.Ref}, "")
}

func TestNoToolRecordsDecisions(t *testing.T) {
	f := newFx(t)
	wcs, _ := f.as(f.alice, service.ScopeWrite)
	for _, n := range toolNames(t, wcs) {
		if strings.Contains(n, "decision") && n != "list_decisions" {
			t.Fatalf("tool %s can write decisions", n)
		}
		if strings.Contains(n, "approve") || strings.HasPrefix(n, "record_") {
			t.Fatalf("tool %s", n)
		}
	}
	// the decision tools are read-scope tools
	lr, _ := wcs.ListTools(context.Background(), nil)
	for _, tl := range lr.Tools {
		if (tl.Name == "list_decisions" || tl.Name == "list_activity") && !tl.Annotations.ReadOnlyHint {
			t.Fatalf("%s not read-only", tl.Name)
		}
	}
	// a direct call of an invented write tool fails
	if _, err := wcs.CallTool(context.Background(), &mcpCall{Name: "record_decision", Arguments: map[string]any{"ticket": "WEB-1"}}); err == nil {
		t.Fatal("record_decision exists")
	}
	// and the service refuses a decision from any token actor
	p, _ := f.principal(f.alice, service.ScopeWrite, nil)
	tk, _ := f.gateFixture()
	_, err := f.env.Svc.Decisions.Record(context.Background(), p.Actor(), tk.Ref,
		service.RecordDecisionInput{Gate: service.GatePlan, Verdict: service.VerdictApproved})
	if err == nil {
		t.Fatal("token recorded a decision")
	}
}

func TestListActivityTool(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	tk, _ := f.gateFixture()
	bot, _ := f.as(f.bob, service.ScopeComment)
	f.env.Clock.Advance(time.Second)
	f.okCall(bot, "add_comment", map[string]any{"ticket": tk.Ref, "body": "[developer] PLAN\n" + strings.Repeat("x", 300)})
	f.env.Clock.Advance(time.Second)
	if _, err := f.env.Svc.Decisions.Record(ctx, f.aliceActor(), tk.Ref,
		service.RecordDecisionInput{Gate: service.GatePlan, Verdict: service.VerdictApproved}); err != nil {
		t.Fatal(err)
	}
	rcs, _ := f.as(f.carol, service.ScopeRead)
	out := f.okCall(rcs, "list_activity", map[string]any{"project": "WEB"})
	items := out["items"].([]any)
	kinds := []string{}
	for _, it := range items {
		kinds = append(kinds, it.(map[string]any)["kind"].(string))
	}
	if !reflect.DeepEqual(kinds[:2], []string{"decision", "comment"}) {
		t.Fatalf("kinds %v", kinds)
	}
	cm := items[1].(map[string]any)
	if cm["bot"] != true || cm["ticket"] != tk.Ref || len([]rune(cm["summary"].(string))) != 200 || cm["action"] != "commented" {
		t.Fatalf("comment row: %v", cm)
	}
	// filters
	if o := f.okCall(rcs, "list_activity", map[string]any{"project": "WEB", "kind": "comment"}); len(o["items"].([]any)) != 1 {
		t.Fatalf("kind filter: %v", o)
	}
	since := f.env.Clock.Now().Add(-1500 * time.Millisecond).Format(time.RFC3339Nano)
	if o := f.okCall(rcs, "list_activity", map[string]any{"project": "WEB", "since": since}); len(o["items"].([]any)) != 2 {
		t.Fatalf("since filter: %v", o)
	}
	f.errCall(rcs, "list_activity", map[string]any{"project": "WEB", "since": "yesterday"}, "RFC 3339")
	f.errCall(rcs, "list_activity", map[string]any{"project": "WEB", "kind": "bogus"}, "Invalid kind")
	if o := f.okCall(rcs, "list_activity", map[string]any{"project": "WEB", "limit": 1}); o["next_cursor"] == nil {
		t.Fatal("limit 1 should paginate")
	}
	dcs, _ := f.as(f.dave, service.ScopeRead)
	f.errCall(dcs, "list_activity", map[string]any{"project": "WEB"}, "")
}
