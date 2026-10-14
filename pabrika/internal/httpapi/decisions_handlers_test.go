package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
)

func TestDecisions_RecordAndRead(t *testing.T) {
	w := newMWorld(t)
	sc := w.scene(t)
	path := "/api/v1/tickets/" + sc.tkRef + "/decisions"

	r := w.h.Do(t, "POST", path, map[string]any{"gate": "plan", "verdict": "changes_requested"}, As(w.fresh(t, w.editor)))
	if r.Code != 422 || r.ErrCode(t) != "validation_failed" {
		t.Fatalf("missing note: %d %s", r.Code, r.Body)
	}
	r = w.h.Do(t, "POST", path, map[string]any{"gate": "plan", "verdict": "changes_requested", "note": "split it"}, As(w.fresh(t, w.editor)))
	if r.Code != 201 {
		t.Fatalf("record: %d %s", r.Code, r.Body)
	}
	d := r.JSON(t)
	if d["gate"] != "plan" || d["verdict"] != "changes_requested" || d["note"] != "split it" || d["actor_role"] != "editor" ||
		d["gate_label"] != "awaiting-plan-approval" || obj(t, d["actor"])["id"] != w.editor.User.ID {
		t.Fatalf("decision: %v", d)
	}
	// wrong gate for the ticket's label
	r = w.h.Do(t, "POST", path, map[string]any{"gate": "requirements", "verdict": "approved"}, As(w.fresh(t, w.owner)))
	if r.Code != 409 || r.ErrCode(t) != "gate_not_open" {
		t.Fatalf("gate_not_open: %d %s", r.Code, r.Body)
	}
	// unknown field and unknown gate
	r = w.h.Do(t, "POST", path, map[string]any{"gate": "merge", "verdict": "approved"}, As(w.fresh(t, w.owner)))
	if r.Code != 422 {
		t.Fatalf("unknown gate: %d %s", r.Code, r.Body)
	}
	r = w.h.Do(t, "POST", path, map[string]any{"gate": "plan", "verdict": "approved", "labels": []string{"x"}}, As(w.fresh(t, w.owner)))
	if r.Code != 400 {
		t.Fatalf("unknown field: %d %s", r.Code, r.Body)
	}
	// viewer: 403, outsider: 404
	if r := w.h.Do(t, "POST", path, map[string]any{"gate": "plan", "verdict": "approved"}, As(w.fresh(t, w.viewer))); r.Code != 403 || r.ErrCode(t) != "forbidden" {
		t.Fatalf("viewer: %d %s", r.Code, r.Body)
	}
	if r := w.h.Do(t, "POST", path, map[string]any{"gate": "plan", "verdict": "approved"}, As(w.fresh(t, w.outsider))); r.Code != 404 {
		t.Fatalf("outsider: %d %s", r.Code, r.Body)
	}
	// any token, even the owner's write token, gets session_required and creates nothing
	tok, _ := w.h.MkToken(t, w.owner, service.ScopeWrite, "")
	if r := w.h.Do(t, "POST", path, map[string]any{"gate": "plan", "verdict": "approved"}, Bearer(tok)); r.Code != 403 || r.ErrCode(t) != "session_required" {
		t.Fatalf("token: %d %s", r.Code, r.Body)
	}

	// reads: a read-scope token can list; newest first
	w.h.Env.Clock.Advance(time.Second)
	if r := w.h.Do(t, "POST", path, map[string]any{"gate": "plan", "verdict": "approved"}, As(w.fresh(t, w.owner))); r.Code != 201 {
		t.Fatalf("approve: %d %s", r.Code, r.Body)
	}
	rtok, _ := w.h.MkToken(t, w.owner, service.ScopeRead, "")
	l := w.h.Do(t, "GET", path, nil, Bearer(rtok))
	if l.Code != 200 {
		t.Fatalf("list: %d %s", l.Code, l.Body)
	}
	its := l.JSON(t)["items"].([]any)
	if len(its) != 2 || obj(t, its[0])["verdict"] != "approved" || obj(t, its[1])["verdict"] != "changes_requested" {
		t.Fatalf("items: %v", its)
	}

	// the ticket detail carries the latest decision, the list does not
	tk := w.h.Do(t, "GET", "/api/v1/tickets/"+sc.tkRef, nil, As(w.fresh(t, w.viewer))).JSON(t)
	wf := obj(t, tk["workflow"])
	ds := wf["decisions"].([]any)
	if len(ds) != 1 || obj(t, ds[0])["verdict"] != "approved" || obj(t, ds[0])["current"] != true {
		t.Fatalf("workflow.decisions: %v", wf)
	}
	list := w.h.Do(t, "GET", "/api/v1/projects/"+sc.proj.Key+"/tickets", nil, As(w.fresh(t, w.viewer))).JSON(t)
	if wl := list["items"].([]any)[0].(map[string]any)["workflow"]; wl != nil {
		if _, has := wl.(map[string]any)["decisions"]; has {
			t.Fatalf("list item carries decisions: %v", wl)
		}
	}
}

func TestDecisions_EventJSONCarriesIDsOnly(t *testing.T) {
	b, err := json.Marshal(service.Event{Type: service.EventDecisionRecorded, ProjectID: "P", TicketID: "T", DecisionID: "D"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["type"] != "decision.recorded" || m["ticket_id"] != "T" || m["decision_id"] != "D" || m["project_id"] != "P" {
		t.Fatalf("%s", b)
	}
	if _, has := m["note"]; has {
		t.Fatal("event carries content")
	}
}

func TestActivityFeed_Handler(t *testing.T) {
	w := newMWorld(t)
	sc := w.scene(t)
	base := "/api/v1/projects/" + sc.proj.Key + "/activity"
	r := w.h.Do(t, "GET", base, nil, As(w.fresh(t, w.viewer)))
	if r.Code != 200 {
		t.Fatalf("feed: %d %s", r.Code, r.Body)
	}
	if n := len(r.JSON(t)["items"].([]any)); n < 3 {
		t.Fatalf("expected seed rows, got %d", n)
	}
	for _, q := range []string{"?limit=0", "?limit=x", "?kind=bogus", "?actor_type=bogus", "?ticket=NOPE-1", "?kind=comment&kind=activity"} {
		if r := w.h.Do(t, "GET", base+q, nil, As(w.fresh(t, w.viewer))); r.Code != 422 {
			t.Errorf("%s: %d %s", q, r.Code, r.Body)
		}
	}
	if r := w.h.Do(t, "GET", base+"?cursor=garbage", nil, As(w.fresh(t, w.viewer))); r.Code != 400 || r.ErrCode(t) != "invalid_cursor" {
		t.Errorf("cursor: %d %s", r.Code, r.Body)
	}
	r = w.h.Do(t, "GET", base+"?kind=comment&limit=1", nil, As(w.fresh(t, w.viewer)))
	if its := r.JSON(t)["items"].([]any); r.Code != 200 || len(its) != 1 || obj(t, its[0])["kind"] != "comment" || obj(t, its[0])["summary"] != "seed" {
		t.Fatalf("comment filter: %d %s", r.Code, r.Body)
	}
	if r := w.h.Do(t, "GET", base, nil, As(w.fresh(t, w.outsider))); r.Code != 404 {
		t.Errorf("outsider: %d", r.Code)
	}
	// limit is capped at 200, not rejected
	if r := w.h.Do(t, "GET", base+"?limit=5000", nil, As(w.fresh(t, w.viewer))); r.Code != 200 {
		t.Errorf("cap: %d", r.Code)
	}
}
