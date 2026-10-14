package service_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

func TestTicketSvcMultiLabelFilter(t *testing.T) {
	e := newTk(t)
	bug := e.label(t, e.P.ID, "bug")
	ui := e.label(t, e.P.ID, "ui")
	ops := e.label(t, e.P.ID, "ops")
	mk := func(title string, st service.Status, ls ...string) {
		t.Helper()
		if _, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: title, Status: st, LabelIDs: ls}); err != nil {
			t.Fatal(err)
		}
	}
	mk("none", service.StatusTodo)
	mk("bug", service.StatusTodo, bug)
	mk("bug+ui", service.StatusTodo, bug, ui)
	mk("bug+ui+ops", service.StatusDone, bug, ui, ops)
	mk("ui", service.StatusDone, ui)

	st := func(s service.Status) *service.Status { return &s }
	cases := []struct {
		name string
		f    service.TicketFilter
		want []string
	}{
		{"no filter", service.TicketFilter{}, []string{"none", "bug", "bug+ui", "bug+ui+ops", "ui"}},
		{"old single param", service.TicketFilter{LabelID: bug}, []string{"bug", "bug+ui", "bug+ui+ops"}},
		{"one in list", service.TicketFilter{LabelIDs: []string{ui}}, []string{"bug+ui", "bug+ui+ops", "ui"}},
		{"two AND", service.TicketFilter{LabelIDs: []string{bug, ui}}, []string{"bug+ui", "bug+ui+ops"}},
		{"three AND", service.TicketFilter{LabelIDs: []string{bug, ui, ops}}, []string{"bug+ui+ops"}},
		{"merge single and list", service.TicketFilter{LabelID: bug, LabelIDs: []string{ui}}, []string{"bug+ui", "bug+ui+ops"}},
		{"duplicates collapse", service.TicketFilter{LabelID: bug, LabelIDs: []string{bug, strings.ToLower(bug), bug}}, []string{"bug", "bug+ui", "bug+ui+ops"}},
		{"with status", service.TicketFilter{LabelIDs: []string{bug, ui}, Status: st(service.StatusDone)}, []string{"bug+ui+ops"}},
		{"no match", service.TicketFilter{LabelIDs: []string{ops, bug}, Status: st(service.StatusTodo)}, nil},
		{"unknown id matches nothing", service.TicketFilter{LabelIDs: []string{bug, e.NewID()}}, nil},
	}
	for _, c := range cases {
		if got := e.titles(t, c.f); !eqStrings(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestTicketSvcMultiLabelFilterLimit(t *testing.T) {
	e := newTk(t)
	var ids []string
	for i := 0; i < 11; i++ {
		ids = append(ids, e.label(t, e.P.ID, fmt.Sprintf("l%02d", i)))
	}
	_, err := e.Svc.Tickets.List(ctx, e.Owner, e.P.Key, service.TicketFilter{LabelIDs: ids})
	if se := wantSvcErr(t, err, service.KindValidation, ""); se.Fields["label"] == "" {
		t.Fatalf("fields %v", se.Fields)
	}
	// exactly ten works, and filters on all ten
	tk := e.create(t, "ten", service.StatusTodo)
	e.create(t, "nine", service.StatusTodo)
	if _, err := e.Svc.Tickets.ChangeLabels(ctx, e.Owner, tk.Ref, service.ChangeLabelsInput{Add: ids[:10]}); err != nil {
		t.Fatal(err)
	}
	if got := e.titles(t, service.TicketFilter{LabelIDs: ids[:10]}); !eqStrings(got, []string{"ten"}) {
		t.Fatalf("got %v", got)
	}
}

func TestTicketSvcMultiLabelFilterPagination(t *testing.T) {
	e := newTk(t)
	a := e.label(t, e.P.ID, "a")
	b := e.label(t, e.P.ID, "b")
	want := map[string]bool{}
	for i := 0; i < 7; i++ {
		ls := []string{a}
		if i%2 == 0 {
			ls = append(ls, b)
		}
		title := fmt.Sprintf("t%d", i)
		if _, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: title, LabelIDs: ls}); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			want[title] = true
		}
	}
	seen := map[string]int{}
	f := service.TicketFilter{LabelIDs: []string{a, b}, Limit: 2}
	for pages := 0; pages < 10; pages++ {
		p, err := e.Svc.Tickets.List(ctx, e.Owner, e.P.Key, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range p.Items {
			seen[it.Title]++
		}
		if p.NextCursor == "" {
			break
		}
		f.Cursor = p.NextCursor
	}
	if len(seen) != len(want) {
		t.Fatalf("seen %v want %v", seen, want)
	}
	for k, n := range seen {
		if n != 1 || !want[k] {
			t.Fatalf("ticket %s seen %d times (want set %v)", k, n, want)
		}
	}
}
