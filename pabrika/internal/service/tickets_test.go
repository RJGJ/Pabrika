package service_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store/db"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// tkEnv is a project (WEB) with an owner, an editor, a viewer and an outsider.
type tkEnv struct {
	*testutil.Env
	M      *testutil.Matrix
	Owner  service.Actor
	Editor service.Actor
	Viewer service.Actor
	P      testutil.Project
}

func newTk(t *testing.T) *tkEnv { return newTkFrom(t, testutil.NewTestServices(t)) }

func newTkFrom(t *testing.T, env *testutil.Env) *tkEnv {
	t.Helper()
	m := env.NewMatrix(t, "WEB")
	return &tkEnv{
		Env: env, M: m, P: m.Project,
		Owner:  env.UserActor(m.Users[testutil.Owner]),
		Editor: env.UserActor(m.Users[testutil.Editor]),
		Viewer: env.UserActor(m.Users[testutil.Viewer]),
	}
}

func (e *tkEnv) create(t *testing.T, title string, status service.Status) service.Ticket {
	t.Helper()
	tk, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: title, Status: status})
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return tk
}

func (e *tkEnv) label(t *testing.T, projectID, name string) string {
	t.Helper()
	id := e.NewID()
	err := e.Store.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.TicketSeedLabel(ctx, db.TicketSeedLabelParams{ID: id, ProjectID: projectID, Name: name, Color: "gray"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *tkEnv) setPosition(t *testing.T, id string, pos float64) {
	t.Helper()
	err := e.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.TicketSetPosition(ctx, db.TicketSetPositionParams{Position: pos, ID: id})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (e *tkEnv) titles(t *testing.T, f service.TicketFilter) []string {
	t.Helper()
	var out []string
	f.Limit = 200
	for {
		page, err := e.Svc.Tickets.List(ctx, e.Owner, e.P.Key, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			out = append(out, it.Title)
		}
		if page.NextCursor == "" {
			return out
		}
		f.Cursor = page.NextCursor
	}
}

func (e *tkEnv) move(t *testing.T, ref string, in service.MoveInput) service.MoveResult {
	t.Helper()
	r, err := e.Svc.Tickets.Move(ctx, e.Owner, ref, in)
	if err != nil {
		t.Fatalf("move %s: %v", ref, err)
	}
	return r
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func wantSvcErr(t *testing.T, err error, kind service.Kind, code string) *service.Error {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) || se.Kind != kind || (code != "" && se.Code != code) {
		t.Fatalf("want kind %d code %q, got %v", kind, code, err)
	}
	return se
}

// ---- Create, Get, Resolve ----

func TestTicketSvcCreateRefsAndHydration(t *testing.T) {
	e := newTk(t)
	assignee := e.M.Users[testutil.Editor]
	lbl := e.label(t, e.P.ID, "bug")
	due := "2026-03-04"
	a, err := e.Svc.Tickets.Create(ctx, e.Owner, "web", service.CreateTicketInput{
		Title: "  First  ", Description: "desc", Priority: service.PriorityHigh, DueDate: &due,
		AssigneeID: &assignee.ID, LabelIDs: []string{lbl, lbl},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Ref != "WEB-1" || a.Number != 1 || a.ProjectKey != "WEB" || a.Title != "First" || a.Status != service.StatusTodo {
		t.Fatalf("bad ticket %+v", a)
	}
	if a.Assignee == nil || a.Assignee.Email != assignee.Email || len(a.Labels) != 1 || a.Labels[0].Name != "bug" || a.CommentCount != 0 {
		t.Fatalf("not hydrated %+v", a)
	}
	if a.Position != service.Gap || a.DueDate == nil || *a.DueDate != due || a.Description != "desc" {
		t.Fatalf("bad fields %+v", a)
	}
	b := e.create(t, "second", "")
	if b.Ref != "WEB-2" || b.Position != 2*service.Gap || b.Labels == nil {
		t.Fatalf("second %+v", b)
	}
	// other statuses are independent columns; counters are per project
	c := e.create(t, "backlog one", service.StatusBacklog)
	if c.Position != service.Gap || c.Ref != "WEB-3" {
		t.Fatalf("backlog %+v", c)
	}
	other := e.Env.NewProject(t, e.M.Users[testutil.Owner], "ZZZ")
	z, err := e.Svc.Tickets.Create(ctx, e.Owner, other.ID, service.CreateTicketInput{Title: "z"})
	if err != nil || z.Ref != "ZZZ-1" {
		t.Fatalf("other project: %+v %v", z, err)
	}

	ev := e.Pub.Events()
	if len(ev) != 4 || ev[0].Type != service.EventTicketCreated || ev[0].TicketID != a.ID || ev[0].ProjectID != e.P.ID {
		t.Fatalf("events %+v", ev)
	}

	// Get by ULID, ref, lowercase ref and lowercase ULID give the same ticket; resolve works too
	for _, ref := range []string{a.ID, "WEB-1", "web-1", " Web-1 "} {
		g, err := e.Svc.Tickets.Get(ctx, e.Viewer, ref)
		if err != nil || g.ID != a.ID || g.Description != "desc" {
			t.Fatalf("get %q: %+v %v", ref, g, err)
		}
	}
	r, err := e.Svc.Tickets.Resolve(ctx, e.Viewer, "web-2")
	if err != nil || r.ID != b.ID || r.Key != "WEB" || r.Number != 2 || r.ProjectID != e.P.ID {
		t.Fatalf("resolve %+v %v", r, err)
	}
	for _, ref := range []string{"nonsense", "WEB-0", "WEB-99", "WEB-", "", "01ARZ3NDEKTSV4RRFFQ69G5FAV"} {
		_, err := e.Svc.Tickets.Get(ctx, e.Owner, ref)
		wantSvcErr(t, err, service.KindNotFound, "")
	}
}

func TestTicketSvcCreateValidation(t *testing.T) {
	e := newTk(t)
	outsider := e.M.Users[testutil.NonMember]
	otherProj := e.Env.NewProject(t, outsider, "OTH")
	foreign := e.label(t, otherProj.ID, "foreign")
	ghost := "01ARZ3NDEKTSV4RRFFQ69G5FAV"

	_, err := e.Svc.Tickets.Create(ctx, e.Owner, "WEB", service.CreateTicketInput{Title: "x", AssigneeID: &outsider.ID})
	se := wantSvcErr(t, err, service.KindValidation, "")
	if se.Fields["assignee"] == "" {
		t.Fatalf("fields %v", se.Fields)
	}
	_, err = e.Svc.Tickets.Create(ctx, e.Owner, "WEB", service.CreateTicketInput{Title: "x", LabelIDs: []string{foreign}})
	if se := wantSvcErr(t, err, service.KindValidation, ""); se.Fields["labels"] == "" {
		t.Fatalf("fields %v", se.Fields)
	}
	_, err = e.Svc.Tickets.Create(ctx, e.Owner, "WEB", service.CreateTicketInput{Title: "x", LabelIDs: []string{ghost}})
	wantSvcErr(t, err, service.KindValidation, "")
	_, err = e.Svc.Tickets.Create(ctx, e.Owner, "WEB", service.CreateTicketInput{Title: ""})
	wantSvcErr(t, err, service.KindValidation, "")

	// failed creates leave no gap in the counter
	first := e.create(t, "ok", "")
	if first.Number != 1 {
		t.Fatalf("number %d", first.Number)
	}
	if n := len(e.Pub.Events()); n != 1 {
		t.Fatalf("failed creates published events: %d", n)
	}
}

func TestTicketSvcConcurrentCreateIsConsecutive(t *testing.T) {
	e := newTkFrom(t, testutil.NewFileEnv(t))
	const n = 20
	var wg sync.WaitGroup
	nums := make(chan int64, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tk, err := e.Svc.Tickets.Create(context.Background(), e.Owner, "WEB", service.CreateTicketInput{Title: fmt.Sprintf("t%d", i)})
			if err != nil {
				errs <- err
				return
			}
			nums <- tk.Number
		}(i)
	}
	wg.Wait()
	close(nums)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for v := range nums {
		seen[v] = true
	}
	for i := int64(1); i <= n; i++ {
		if !seen[i] {
			t.Fatalf("missing number %d in %v", i, seen)
		}
	}
}

// ---- List ----

func TestTicketSvcListFilters(t *testing.T) {
	e := newTk(t)
	ed := e.M.Users[testutil.Editor]
	l1 := e.label(t, e.P.ID, "bug")
	l2 := e.label(t, e.P.ID, "ui")
	mk := func(in service.CreateTicketInput) service.Ticket {
		tk, err := e.Svc.Tickets.Create(ctx, e.Owner, "WEB", in)
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	mk(service.CreateTicketInput{Title: "alpha", Status: service.StatusBacklog, Priority: service.PriorityLow, LabelIDs: []string{l1}})
	mk(service.CreateTicketInput{Title: "beta", AssigneeID: &ed.ID, Priority: service.PriorityHigh, LabelIDs: []string{l1, l2}})
	mk(service.CreateTicketInput{Title: "100% done_ok", Description: "path C:\\tmp"})
	mk(service.CreateTicketInput{Title: "gamma", Status: service.StatusDone, Description: "mentions ALPHA here"})
	mk(service.CreateTicketInput{Title: "100x doneXok"})

	st := func(s service.Status) *service.Status { return &s }
	pr := func(p service.Priority) *service.Priority { return &p }
	cases := []struct {
		name string
		f    service.TicketFilter
		want []string
	}{
		{"all in board order", service.TicketFilter{}, []string{"alpha", "beta", "100% done_ok", "100x doneXok", "gamma"}},
		{"status", service.TicketFilter{Status: st(service.StatusDone)}, []string{"gamma"}},
		{"priority", service.TicketFilter{Priority: pr(service.PriorityHigh)}, []string{"beta"}},
		{"unassigned", service.TicketFilter{Assignee: &service.AssigneeFilter{Unassigned: true}}, []string{"alpha", "100% done_ok", "100x doneXok", "gamma"}},
		{"assignee", service.TicketFilter{Assignee: &service.AssigneeFilter{UserID: ed.ID}}, []string{"beta"}},
		{"label", service.TicketFilter{LabelID: l1}, []string{"alpha", "beta"}},
		{"label ui", service.TicketFilter{LabelID: l2}, []string{"beta"}},
		{"query title ci", service.TicketFilter{Query: " BETA "}, []string{"beta"}},
		{"query description ci", service.TicketFilter{Query: "alpha"}, []string{"alpha", "gamma"}},
		{"query percent literal", service.TicketFilter{Query: "100%"}, []string{"100% done_ok"}},
		{"query underscore literal", service.TicketFilter{Query: "e_o"}, []string{"100% done_ok"}},
		{"query backslash literal", service.TicketFilter{Query: `C:\t`}, []string{"100% done_ok"}},
		{"combined", service.TicketFilter{LabelID: l1, Status: st(service.StatusTodo)}, []string{"beta"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := e.titles(t, c.f); !eqStrings(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}

	bad := service.Status("nope")
	_, err := e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{Status: &bad})
	wantSvcErr(t, err, service.KindValidation, "")
	badP := service.Priority("nope")
	_, err = e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{Priority: &badP})
	wantSvcErr(t, err, service.KindValidation, "")

	// list items carry no description but are hydrated
	page, err := e.Svc.Tickets.List(ctx, e.Viewer, "WEB", service.TicketFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.Description != "" {
			t.Fatalf("description loaded in list: %+v", it)
		}
		if it.Title == "beta" && (it.Assignee == nil || len(it.Labels) != 2 || it.Labels[0].Name != "bug") {
			t.Fatalf("beta not hydrated: %+v", it)
		}
		if it.Ref == "" || it.ProjectKey != "WEB" {
			t.Fatalf("missing ref: %+v", it)
		}
	}
}

func TestTicketSvcListLimits(t *testing.T) {
	e := newTk(t)
	for i := 0; i < 205; i++ {
		e.create(t, fmt.Sprintf("t%03d", i), service.StatusTodo)
	}
	page, err := e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{})
	if err != nil || len(page.Items) != 50 || page.NextCursor == "" {
		t.Fatalf("default: %d %q %v", len(page.Items), page.NextCursor, err)
	}
	page, err = e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{Limit: 5000})
	if err != nil || len(page.Items) != 200 || page.NextCursor == "" {
		t.Fatalf("max: %d %v", len(page.Items), err)
	}
	page, err = e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{Limit: -3})
	if err != nil || len(page.Items) != 50 {
		t.Fatalf("negative: %d %v", len(page.Items), err)
	}
	page, err = e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{Limit: 205})
	if err != nil || len(page.Items) != 200 {
		t.Fatalf("clamp: %d %v", len(page.Items), err)
	}
	last, err := e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{Cursor: page.NextCursor, Limit: 10})
	if err != nil || len(last.Items) != 5 || last.NextCursor != "" {
		t.Fatalf("last page: %d %q %v", len(last.Items), last.NextCursor, err)
	}
}

func TestTicketSvcCursorPagination(t *testing.T) {
	e := newTk(t)
	// spans every status; tickets are created round-robin
	var all []service.Ticket
	for i := 0; i < 12; i++ {
		all = append(all, e.create(t, fmt.Sprintf("n%02d", i), service.Statuses[i%4]))
	}
	want := e.titles(t, service.TicketFilter{})
	if len(want) != 12 {
		t.Fatalf("want 12, got %v", want)
	}
	pageThrough := func(limit int, f service.TicketFilter, between func(page int)) []string {
		var got []string
		f.Limit = limit
		for i := 0; ; i++ {
			p, err := e.Svc.Tickets.List(ctx, e.Owner, "WEB", f)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range p.Items {
				got = append(got, it.Title)
			}
			if p.NextCursor == "" {
				return got
			}
			f.Cursor = p.NextCursor
			if between != nil {
				between(i)
			}
		}
	}
	if got := pageThrough(5, service.TicketFilter{}, nil); !eqStrings(got, want) {
		t.Fatalf("across status boundaries: %v vs %v", got, want)
	}
	if got := pageThrough(1, service.TicketFilter{}, nil); !eqStrings(got, want) {
		t.Fatalf("limit 1: %v vs %v", got, want)
	}

	// equal positions inside one column: ties broken by id
	for _, tk := range all {
		e.setPosition(t, tk.ID, 7)
	}
	want = e.titles(t, service.TicketFilter{})
	if got := pageThrough(1, service.TicketFilter{}, nil); !eqStrings(got, want) || len(got) != 12 {
		t.Fatalf("equal positions: %v vs %v", got, want)
	}

	// inserts between pages: nothing seen twice, nothing before the cursor shows up
	got := pageThrough(4, service.TicketFilter{}, func(page int) {
		e.create(t, fmt.Sprintf("late%d", page), service.StatusBacklog)
	})
	seen := map[string]bool{}
	for _, g := range got {
		if seen[g] {
			t.Fatalf("duplicate %s in %v", g, got)
		}
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Fatalf("lost %s", w)
		}
	}

	for _, cur := range map[string]string{
		"garbage":       "!!!",
		"not json":      service.EncodeCursor("x")[:3],
		"wrong shape":   service.EncodeCursor(1, 2),
		"wrong types":   service.EncodeCursor("a", "b", "c"),
		"too many keys": service.EncodeCursor(1, 2.0, "x", "y"),
	} {
		_, err := e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{Cursor: cur})
		wantSvcErr(t, err, service.KindBadRequest, service.CodeInvalidCursor)
	}
}

func TestTicketSvcCursorFloatRoundTrip(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "a", service.StatusTodo)
	b := e.create(t, "b", service.StatusTodo)
	c := e.create(t, "c", service.StatusTodo)
	d := e.create(t, "d", service.StatusTodo)
	e.setPosition(t, a.ID, 1.0/3.0)
	e.setPosition(t, b.ID, 1.0/3.0+1e-12)
	e.setPosition(t, c.ID, 1e-7)
	e.setPosition(t, d.ID, math.Nextafter(1.0/3.0+1e-12, 1))
	want := []string{"c", "a", "b", "d"}
	var got []string
	f := service.TicketFilter{Limit: 1}
	for {
		p, err := e.Svc.Tickets.List(ctx, e.Owner, "WEB", f)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range p.Items {
			got = append(got, it.Title)
		}
		if p.NextCursor == "" {
			break
		}
		f.Cursor = p.NextCursor
	}
	if !eqStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTicketSvcListBatchesQueries(t *testing.T) {
	e := newTk(t)
	ed := e.M.Users[testutil.Editor]
	other := e.Env.NewProject(t, e.M.Users[testutil.Owner], "BIG")
	lbl := e.label(t, e.P.ID, "l")
	lblBig := e.label(t, other.ID, "l")
	fill := func(proj, label string, n int) {
		for i := 0; i < n; i++ {
			c := service.CreateTicketInput{Title: fmt.Sprintf("t%d", i), AssigneeID: &ed.ID, LabelIDs: []string{label}}
			if _, err := e.Svc.Tickets.Create(ctx, e.Owner, proj, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.Env.AddMember(t, other.ID, ed.ID, service.RoleEditor)
	fill("WEB", lbl, 5)
	fill("BIG", lblBig, 50)
	count := func(proj string) (int64, int) {
		before := e.Store.QueryCount()
		p, err := e.Svc.Tickets.List(ctx, e.Owner, proj, service.TicketFilter{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		return e.Store.QueryCount() - before, len(p.Items)
	}
	small, ns := count("WEB")
	big, nb := count("BIG")
	if ns != 5 || nb != 50 {
		t.Fatalf("items %d %d", ns, nb)
	}
	if small != big {
		t.Fatalf("statement count grows with page size: %d vs %d", small, big)
	}
}

// ---- Update ----

func TestTicketSvcUpdate(t *testing.T) {
	e := newTk(t)
	ed := e.M.Users[testutil.Editor]
	bug := e.label(t, e.P.ID, "bug")
	ui := e.label(t, e.P.ID, "ui")
	tk := e.create(t, "orig", "")
	upd := func(in service.UpdateTicketInput) service.Ticket {
		out, err := e.Svc.Tickets.Update(ctx, e.Editor, tk.Ref, in)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		return out
	}
	lastActivity := func() service.Activity {
		p, err := e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 1, "")
		if err != nil || len(p.Items) != 1 {
			t.Fatalf("activity: %v %v", p, err)
		}
		return p.Items[0]
	}
	activityCount := func() int {
		p, _ := e.Svc.Activity.List(ctx, e.Owner, tk.Ref, 200, "")
		return len(p.Items)
	}

	// generic update: title + priority
	e.Clock.Advance(time.Second)
	e.Pub.Reset()
	got := upd(service.UpdateTicketInput{Title: service.Some(" new title "), Priority: service.Some(service.PriorityUrgent)})
	if got.Title != "new title" || got.Priority != service.PriorityUrgent || !got.UpdatedAt.After(tk.UpdatedAt) {
		t.Fatalf("%+v", got)
	}
	act := lastActivity()
	if act.Action != "updated" || len(act.Changes) != 2 || act.Changes["title"] != [2]any{"orig", "new title"} || act.Changes["priority"] != [2]any{"medium", "urgent"} {
		t.Fatalf("activity %+v", act)
	}
	if act.Actor.Type != service.ActorUser || act.Actor.ID != e.Editor.ID {
		t.Fatalf("actor %+v", act.Actor)
	}
	if ev := e.Pub.Events(); len(ev) != 1 || ev[0].Type != service.EventTicketUpdated || ev[0].TicketID != tk.ID {
		t.Fatalf("events %+v", ev)
	}

	// no-op: same values, empty update, null on nullable fields already null, label set unchanged
	cur := upd(service.UpdateTicketInput{LabelIDs: service.Some([]string{bug, ui})})
	n := activityCount()
	e.Pub.Reset()
	e.Clock.Advance(time.Second)
	noop := upd(service.UpdateTicketInput{
		Title: service.Some("new title"), Description: service.Some(""), Priority: service.Some(service.PriorityUrgent),
		DueDate: service.Null[*string](), AssigneeID: service.Null[*string](),
		LabelIDs: service.Some([]string{ui, bug, ui}),
	})
	upd(service.UpdateTicketInput{})
	if activityCount() != n || len(e.Pub.Events()) != 0 || !noop.UpdatedAt.Equal(cur.UpdatedAt) {
		t.Fatalf("no-op wrote something: %d vs %d events %v", activityCount(), n, e.Pub.Events())
	}

	// labeled: label replace and clear
	got = upd(service.UpdateTicketInput{LabelIDs: service.Some([]string{ui})})
	act = lastActivity()
	if act.Action != "labeled" || len(got.Labels) != 1 || got.Labels[0].Name != "ui" {
		t.Fatalf("labeled %+v %+v", act, got.Labels)
	}
	old, _ := act.Changes["labels"][0].([]any)
	nw, _ := act.Changes["labels"][1].([]any)
	if len(old) != 2 || old[0] != "bug" || old[1] != "ui" || len(nw) != 1 || nw[0] != "ui" {
		t.Fatalf("label changes %v", act.Changes)
	}
	got = upd(service.UpdateTicketInput{LabelIDs: service.Null[[]string]()})
	if len(got.Labels) != 0 || got.Labels == nil {
		t.Fatalf("labels after clear %+v", got.Labels)
	}

	// assigned: set then clear (null vs omitted)
	got = upd(service.UpdateTicketInput{AssigneeID: service.Some[*string](&ed.ID)})
	act = lastActivity()
	if act.Action != "assigned" || got.Assignee == nil || got.Assignee.ID != ed.ID || act.Changes["assignee"] != [2]any{nil, ed.ID} {
		t.Fatalf("assigned %+v %+v", act, got)
	}
	got = upd(service.UpdateTicketInput{Title: service.Some("again")}) // omitted: assignee untouched
	if got.AssigneeID == nil {
		t.Fatal("omitted assignee cleared")
	}
	got = upd(service.UpdateTicketInput{AssigneeID: service.Null[*string]()})
	act = lastActivity()
	if got.AssigneeID != nil || got.Assignee != nil || act.Action != "assigned" || act.Changes["assignee"] != [2]any{ed.ID, nil} {
		t.Fatalf("unassigned %+v %+v", act, got)
	}

	// due date set, clear via null and via empty string
	d := "2026-12-31"
	got = upd(service.UpdateTicketInput{DueDate: service.Some[*string](&d)})
	if got.DueDate == nil || *got.DueDate != d || lastActivity().Changes["due_date"] != [2]any{nil, d} {
		t.Fatalf("due %+v", got)
	}
	got = upd(service.UpdateTicketInput{DueDate: service.Null[*string]()})
	if got.DueDate != nil {
		t.Fatalf("due not cleared %+v", got)
	}
	upd(service.UpdateTicketInput{DueDate: service.Some[*string](&d)})
	empty := ""
	got = upd(service.UpdateTicketInput{DueDate: service.Some[*string](&empty)})
	if got.DueDate != nil {
		t.Fatalf("due not cleared by empty %+v", got)
	}

	// description is truncated to 200 runes in activity
	long := ""
	for i := 0; i < 250; i++ {
		long += "é"
	}
	got = upd(service.UpdateTicketInput{Description: service.Some(long)})
	act = lastActivity()
	nd, _ := act.Changes["description"][1].(string)
	if got.Description != long || len([]rune(nd)) != 200 {
		t.Fatalf("description %d / %d", len([]rune(got.Description)), len([]rune(nd)))
	}
	for k := range act.Changes {
		switch k {
		case "title", "description", "priority", "due_date", "assignee", "labels", "status", "position":
		default:
			t.Fatalf("unexpected key %s", k)
		}
	}
}

func TestTicketSvcUpdateValidation(t *testing.T) {
	e := newTk(t)
	outsider := e.M.Users[testutil.NonMember]
	foreignProj := e.Env.NewProject(t, outsider, "OTH")
	foreign := e.label(t, foreignProj.ID, "x")
	tk := e.create(t, "t", "")
	bad := func(in service.UpdateTicketInput, field string) {
		t.Helper()
		_, err := e.Svc.Tickets.Update(ctx, e.Owner, tk.ID, in)
		se := wantSvcErr(t, err, service.KindValidation, "")
		if field != "" && se.Fields[field] == "" {
			t.Fatalf("want field %s in %v", field, se.Fields)
		}
	}
	rep := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a'
		}
		return string(b)
	}
	bad(service.UpdateTicketInput{Title: service.Some(rep(201))}, "title")
	bad(service.UpdateTicketInput{Title: service.Some("  ")}, "title")
	bad(service.UpdateTicketInput{Title: service.Null[string]()}, "title")
	bad(service.UpdateTicketInput{Description: service.Some(rep(20001))}, "description")
	bad(service.UpdateTicketInput{Priority: service.Some(service.Priority("x"))}, "priority")
	badDate := "2026-02-30"
	bad(service.UpdateTicketInput{DueDate: service.Some[*string](&badDate)}, "due_date")
	bad(service.UpdateTicketInput{AssigneeID: service.Some[*string](&outsider.ID)}, "assignee")
	bad(service.UpdateTicketInput{LabelIDs: service.Some([]string{foreign})}, "labels")
	for _, ok := range []service.UpdateTicketInput{
		{Title: service.Some(rep(200))}, {Description: service.Some(rep(20000))},
	} {
		if _, err := e.Svc.Tickets.Update(ctx, e.Owner, tk.ID, ok); err != nil {
			t.Fatal(err)
		}
	}
}

// ---- Move ----

func TestTicketSvcMovePlacements(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	b := e.create(t, "B", service.StatusTodo)
	c := e.create(t, "C", service.StatusTodo)
	d := e.create(t, "D", service.StatusTodo)
	todo := service.Status("todo")
	order := func() []string {
		return e.titles(t, service.TicketFilter{Status: &todo})
	}
	check := func(want ...string) {
		t.Helper()
		if got := order(); !eqStrings(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	}

	e.move(t, d.Ref, service.MoveInput{Status: todo, Place: "top"})
	check("D", "A", "B", "C")
	e.move(t, d.Ref, service.MoveInput{Status: todo, Place: "bottom"})
	check("A", "B", "C", "D")
	e.move(t, a.Ref, service.MoveInput{Status: todo, After: c.ID})
	check("B", "C", "A", "D")
	e.move(t, d.Ref, service.MoveInput{Status: todo, Before: b.Ref})
	check("D", "B", "C", "A")
	e.move(t, a.Ref, service.MoveInput{Status: todo}) // default: bottom (already there: no-op)
	check("D", "B", "C", "A")
	e.move(t, b.Ref, service.MoveInput{Status: todo}) // default: bottom
	check("D", "C", "A", "B")
	e.move(t, d.Ref, service.MoveInput{Status: todo, After: "web-" + fmt.Sprint(c.Number)}) // ref anchor
	check("C", "D", "A", "B")

	// cross column: empty target, then before/after in a populated one
	r := e.move(t, a.Ref, service.MoveInput{Status: service.StatusDone})
	if r.Ticket.Status != service.StatusDone || r.Ticket.Position != service.Gap || r.Renumbered {
		t.Fatalf("%+v", r)
	}
	e.move(t, b.Ref, service.MoveInput{Status: service.StatusDone, Before: a.ID})
	e.move(t, c.Ref, service.MoveInput{Status: service.StatusDone, After: b.ID})
	done := service.Status("done")
	if got := e.titles(t, service.TicketFilter{Status: &done}); !eqStrings(got, []string{"B", "C", "A"}) {
		t.Fatalf("done column %v", got)
	}
	check("D")
	// board order: status rank first
	if got := e.titles(t, service.TicketFilter{}); !eqStrings(got, []string{"D", "B", "C", "A"}) {
		t.Fatalf("board %v", got)
	}

	// the moved activity row
	p, _ := e.Svc.Activity.List(ctx, e.Owner, a.Ref, 1, "")
	if p.Items[0].Action != "moved" || p.Items[0].Changes["status"] != [2]any{"todo", "done"} || p.Items[0].Changes["position"][1] != service.Gap {
		t.Fatalf("activity %+v", p.Items[0])
	}
}

func TestTicketSvcMoveNoOp(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	b := e.create(t, "B", service.StatusTodo)
	c := e.create(t, "C", service.StatusTodo)
	todo := service.StatusTodo
	e.Clock.Advance(time.Second)
	e.Pub.Reset()
	before, _ := e.Svc.Activity.List(ctx, e.Owner, b.Ref, 200, "")

	noops := []service.MoveInput{
		{Status: todo, After: a.ID},  // just after its current predecessor
		{Status: todo, Before: c.ID}, // just before its current successor
	}
	for _, in := range noops {
		r := e.move(t, b.Ref, in)
		if r.Renumbered || r.Ticket.Position != b.Position || !r.Ticket.UpdatedAt.Equal(b.UpdatedAt) {
			t.Fatalf("not a no-op: %+v", r)
		}
	}
	r := e.move(t, a.Ref, service.MoveInput{Status: todo, Place: "top"})
	if r.Ticket.Position != a.Position {
		t.Fatal("top of first moved")
	}
	r = e.move(t, c.Ref, service.MoveInput{Status: todo, Place: "bottom"})
	if r.Ticket.Position != c.Position {
		t.Fatal("bottom of last moved")
	}
	if n := len(e.Pub.Events()); n != 0 {
		t.Fatalf("no-op published %d events", n)
	}
	after, _ := e.Svc.Activity.List(ctx, e.Owner, b.Ref, 200, "")
	if len(after.Items) != len(before.Items) {
		t.Fatal("no-op wrote activity")
	}

	// a real move afterwards still works
	r = e.move(t, b.Ref, service.MoveInput{Status: todo, Place: "bottom"})
	if r.Ticket.Position <= c.Position || len(e.Pub.Events()) != 1 || e.Pub.Events()[0].Type != service.EventTicketMoved {
		t.Fatalf("real move: %+v %v", r, e.Pub.Events())
	}
}

func TestTicketSvcMoveInvalidAnchors(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	b := e.create(t, "B", service.StatusTodo)
	done := e.create(t, "Done one", service.StatusDone)
	gone := e.create(t, "Gone", service.StatusTodo)
	if err := e.Svc.Tickets.Delete(ctx, e.Owner, gone.Ref); err != nil {
		t.Fatal(err)
	}
	otherProj := e.Env.NewProject(t, e.M.Users[testutil.Owner], "OTH")
	foreign, err := e.Svc.Tickets.Create(ctx, e.Owner, otherProj.Key, service.CreateTicketInput{Title: "f"})
	if err != nil {
		t.Fatal(err)
	}
	todo := service.StatusTodo
	cases := map[string]service.MoveInput{
		"before other project": {Status: todo, Before: foreign.ID},
		"after other project":  {Status: todo, After: foreign.Ref},
		"other status":         {Status: todo, After: done.ID},
		"self":                 {Status: todo, Before: a.ID},
		"soft deleted":         {Status: todo, After: gone.ID},
		"unknown":              {Status: todo, After: "01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		"garbage":              {Status: todo, Before: "???"},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.Svc.Tickets.Move(ctx, e.Owner, a.Ref, in)
			se := wantSvcErr(t, err, service.KindValidation, service.CodeAnchorInvalid)
			if se.Fields["before"] == "" && se.Fields["after"] == "" {
				t.Fatalf("fields %v", se.Fields)
			}
		})
	}
	_, err = e.Svc.Tickets.Move(ctx, e.Owner, a.Ref, service.MoveInput{Status: todo, Before: b.ID, After: b.ID})
	wantSvcErr(t, err, service.KindValidation, "")
	_, err = e.Svc.Tickets.Move(ctx, e.Owner, a.Ref, service.MoveInput{Status: todo, Before: b.ID, Place: "top"})
	wantSvcErr(t, err, service.KindValidation, "")
	_, err = e.Svc.Tickets.Move(ctx, e.Owner, a.Ref, service.MoveInput{})
	wantSvcErr(t, err, service.KindValidation, "")
}

func TestTicketSvcMoveRenumbersAfterManyInserts(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	b := e.create(t, "B", service.StatusTodo)
	const n = 60
	xs := make([]service.Ticket, n)
	for i := range xs {
		xs[i] = e.create(t, fmt.Sprintf("X%02d", i+1), service.StatusTodo)
	}
	renumbered := 0
	for _, x := range xs {
		r := e.move(t, x.Ref, service.MoveInput{Status: service.StatusTodo, After: a.ID})
		if r.Renumbered {
			renumbered++
			evs := e.Pub.Events()
			if last := evs[len(evs)-1]; last.Type != service.EventTicketMoved || !last.Renumbered {
				t.Fatalf("renumber flag missing on event: %+v", last)
			}
		}
	}
	if renumbered == 0 {
		t.Fatal("expected at least one renumber")
	}
	want := []string{"A"}
	for i := n; i >= 1; i-- {
		want = append(want, fmt.Sprintf("X%02d", i))
	}
	want = append(want, "B")
	if got := e.titles(t, service.TicketFilter{}); !eqStrings(got, want) {
		t.Fatalf("final order wrong:\n%v\n%v", got, want)
	}
	// untouched tickets keep their identity
	ga, _ := e.Svc.Tickets.Get(ctx, e.Owner, a.ID)
	gb, _ := e.Svc.Tickets.Get(ctx, e.Owner, b.ID)
	if ga.ID != a.ID || gb.ID != b.ID || !(ga.Position < gb.Position) {
		t.Fatalf("a=%v b=%v", ga.Position, gb.Position)
	}
}

func TestTicketSvcMoveSoftDeletedTakePartInNothing(t *testing.T) {
	e := newTk(t)
	a := e.create(t, "A", service.StatusTodo)
	b := e.create(t, "B", service.StatusTodo)
	c := e.create(t, "C", service.StatusTodo)
	if err := e.Svc.Tickets.Delete(ctx, e.Owner, b.Ref); err != nil {
		t.Fatal(err)
	}
	// "after A" is a no-op because the deleted B is not A's successor
	e.Pub.Reset()
	e.move(t, c.Ref, service.MoveInput{Status: service.StatusTodo, After: a.ID})
	if len(e.Pub.Events()) != 0 {
		t.Fatal("deleted ticket took part in neighbour computation")
	}
	// bottom ignores the deleted ticket's position as well
	d := e.create(t, "D", service.StatusTodo)
	if d.Position != c.Position+service.Gap {
		t.Fatalf("d=%v c=%v", d.Position, c.Position)
	}
}

func TestTicketSvcConcurrentMoves(t *testing.T) {
	e := newTkFrom(t, testutil.NewFileEnv(t))
	a := e.create(t, "A", service.StatusTodo)
	const n = 12
	var cs []service.Ticket
	for i := 0; i < n; i++ {
		cs = append(cs, e.create(t, fmt.Sprintf("C%02d", i), service.StatusTodo))
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, c := range cs {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			_, err := e.Svc.Tickets.Move(context.Background(), e.Owner, ref, service.MoveInput{Status: service.StatusTodo, After: a.ID})
			errs <- err
		}(c.Ref)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := e.Svc.Tickets.List(ctx, e.Owner, "WEB", service.TicketFilter{Limit: 200})
	if err != nil || len(p.Items) != n+1 {
		t.Fatalf("%d %v", len(p.Items), err)
	}
	if p.Items[0].Title != "A" {
		t.Fatalf("A must stay first: %v", p.Items[0].Title)
	}
	seen := map[float64]bool{}
	for i, it := range p.Items {
		if seen[it.Position] {
			t.Fatalf("duplicate position %v", it.Position)
		}
		seen[it.Position] = true
		if i > 0 && !(it.Position > p.Items[i-1].Position) {
			t.Fatal("list order differs from position order")
		}
	}
}

// ---- Delete ----

func TestTicketSvcSoftDelete(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "doomed", "")
	keep := e.create(t, "keeper", "")
	if _, err := e.Svc.Comments.Add(ctx, e.Owner, tk.Ref, "hello"); err != nil {
		t.Fatal(err)
	}
	e.Pub.Reset()
	if err := e.Svc.Tickets.Delete(ctx, e.Editor, tk.Ref); err != nil {
		t.Fatal(err)
	}
	if ev := e.Pub.Events(); len(ev) != 1 || ev[0].Type != service.EventTicketDeleted || ev[0].TicketID != tk.ID {
		t.Fatalf("events %+v", ev)
	}
	for _, ref := range []string{tk.ID, tk.Ref} {
		_, err := e.Svc.Tickets.Get(ctx, e.Owner, ref)
		wantSvcErr(t, err, service.KindNotFound, "")
		_, err = e.Svc.Tickets.Resolve(ctx, e.Owner, ref)
		wantSvcErr(t, err, service.KindNotFound, "")
		_, err = e.Svc.Comments.List(ctx, e.Owner, ref, 0, "")
		wantSvcErr(t, err, service.KindNotFound, "")
		_, err = e.Svc.Comments.Add(ctx, e.Owner, ref, "x")
		wantSvcErr(t, err, service.KindNotFound, "")
	}
	wantSvcErr(t, e.Svc.Tickets.Delete(ctx, e.Owner, tk.Ref), service.KindNotFound, "")
	_, err := e.Svc.Tickets.Update(ctx, e.Owner, tk.Ref, service.UpdateTicketInput{Title: service.Some("x")})
	wantSvcErr(t, err, service.KindNotFound, "")
	_, err = e.Svc.Tickets.Move(ctx, e.Owner, tk.Ref, service.MoveInput{Status: service.StatusDone})
	wantSvcErr(t, err, service.KindNotFound, "")
	if got := e.titles(t, service.TicketFilter{}); !eqStrings(got, []string{"keeper"}) {
		t.Fatalf("list %v", got)
	}

	// the row and its activity survive; the counter did not decrease
	row, err := e.Store.Read().TicketSeedGetRaw(ctx, tk.ID)
	if err != nil || row.DeletedAt == nil {
		t.Fatalf("row %+v %v", row, err)
	}
	acts, err := e.Store.Read().ActivitySeedListRaw(ctx, tk.ID)
	if err != nil || len(acts) != 2 || acts[1].Action != "deleted" {
		t.Fatalf("activity %+v %v", acts, err)
	}
	next := e.create(t, "next", "")
	if next.Number != keep.Number+1 {
		t.Fatalf("counter reused: %d", next.Number)
	}
}

// ---- Permissions and order of checks ----

func TestTicketSvcMatrix(t *testing.T) {
	e := newTk(t)
	m := e.M
	seed := e.create(t, "seed", service.StatusTodo)
	mk := func() string {
		tk, err := e.Svc.Tickets.Create(ctx, e.Owner, "WEB", service.CreateTicketInput{Title: "tmp"})
		if err != nil {
			t.Fatal(err)
		}
		return tk.Ref
	}
	readers := map[testutil.Who]testutil.Outcome{
		testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.OK, testutil.Editor: testutil.OK, testutil.Owner: testutil.OK,
	}
	writers := map[testutil.Who]testutil.Outcome{
		testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden(service.CodeForbidden),
		testutil.Editor: testutil.OK, testutil.Owner: testutil.OK,
	}
	cases := []testutil.MatrixCase{
		{Name: "Get", Want: readers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Tickets.Get(ctx, a, seed.Ref)
			return err
		}},
		{Name: "Resolve", Want: readers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Tickets.Resolve(ctx, a, seed.Ref)
			return err
		}},
		{Name: "List", Want: readers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Tickets.List(ctx, a, "WEB", service.TicketFilter{})
			return err
		}},
		{Name: "Create", Want: writers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Tickets.Create(ctx, a, "WEB", service.CreateTicketInput{Title: "x"})
			return err
		}},
		{Name: "Update", Want: writers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Tickets.Update(ctx, a, seed.Ref, service.UpdateTicketInput{Priority: service.Some(service.PriorityLow)})
			return err
		}},
		{Name: "Move", Want: writers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Tickets.Move(ctx, a, seed.Ref, service.MoveInput{Status: service.StatusBacklog})
			return err
		}},
		{Name: "Delete", Want: writers, Op: func(t *testing.T, a service.Actor) error {
			return e.Svc.Tickets.Delete(ctx, a, mk())
		}},
		{Name: "CommentAdd", Want: writers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Comments.Add(ctx, a, seed.Ref, "hi")
			return err
		}},
		{Name: "CommentList", Want: readers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Comments.List(ctx, a, seed.Ref, 0, "")
			return err
		}},
		{Name: "CommentLatest", Want: readers, Op: func(t *testing.T, a service.Actor) error {
			_, _, err := e.Svc.Comments.Latest(ctx, a, seed.Ref, 5)
			return err
		}},
		{Name: "ActivityList", Want: readers, Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Activity.List(ctx, a, seed.Ref, 0, "")
			return err
		}},
	}
	for _, c := range cases {
		m.Run(t, c)
	}
}

func TestTicketSvcScopeAndProjectLimit(t *testing.T) {
	e := newTk(t)
	seed := e.create(t, "seed", service.StatusTodo)
	owner := e.M.Users[testutil.Owner]
	readTok := e.TokenActor(e.NewToken(t, owner.ID, service.ScopeRead, ""))
	other := e.Env.NewProject(t, owner, "OTH")
	limited := e.TokenActor(e.NewToken(t, owner.ID, service.ScopeWrite, other.ID))

	if _, err := e.Svc.Tickets.Get(ctx, readTok, seed.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Tickets.List(ctx, readTok, "WEB", service.TicketFilter{}); err != nil {
		t.Fatal(err)
	}
	insuff := func(err error) { t.Helper(); wantSvcErr(t, err, service.KindForbidden, service.CodeInsufficientScope) }
	_, err := e.Svc.Tickets.Create(ctx, readTok, "WEB", service.CreateTicketInput{Title: "x"})
	insuff(err)
	_, err = e.Svc.Tickets.Update(ctx, readTok, seed.Ref, service.UpdateTicketInput{})
	insuff(err)
	_, err = e.Svc.Tickets.Move(ctx, readTok, seed.Ref, service.MoveInput{Status: service.StatusDone})
	insuff(err)
	insuff(e.Svc.Tickets.Delete(ctx, readTok, seed.Ref))
	_, err = e.Svc.Comments.Add(ctx, readTok, seed.Ref, "x")
	insuff(err)

	_, err = e.Svc.Tickets.Get(ctx, limited, seed.Ref)
	wantSvcErr(t, err, service.KindNotFound, "")
	_, err = e.Svc.Tickets.List(ctx, limited, "WEB", service.TicketFilter{})
	wantSvcErr(t, err, service.KindNotFound, "")
	_, err = e.Svc.Tickets.Create(ctx, limited, "WEB", service.CreateTicketInput{Title: "x"})
	wantSvcErr(t, err, service.KindNotFound, "")
	if _, err := e.Svc.Tickets.Create(ctx, limited, "OTH", service.CreateTicketInput{Title: "ok"}); err != nil {
		t.Fatal(err)
	}
}

func TestTicketSvcOrderOfChecks(t *testing.T) {
	e := newTk(t)
	seed := e.create(t, "seed", service.StatusTodo)
	archived := e.Env.NewProject(t, e.M.Users[testutil.Owner], "ARC")
	arcTicket, err := e.Svc.Tickets.Create(ctx, e.Owner, "ARC", service.CreateTicketInput{Title: "a"})
	if err != nil {
		t.Fatal(err)
	}
	ed := e.M.Users[testutil.Editor]
	vw := e.M.Users[testutil.Viewer]
	e.Env.AddMember(t, archived.ID, ed.ID, service.RoleEditor)
	e.Env.AddMember(t, archived.ID, vw.ID, service.RoleViewer)
	e.Env.Archive(t, archived.ID)
	badInput := service.CreateTicketInput{Title: ""}

	outsider := e.UserActor(e.M.Users[testutil.NonMember])
	_, err = e.Svc.Tickets.Create(ctx, outsider, "ARC", badInput)
	wantSvcErr(t, err, service.KindNotFound, "")
	_, err = e.Svc.Tickets.Create(ctx, e.Viewer, "ARC", badInput)
	wantSvcErr(t, err, service.KindForbidden, service.CodeForbidden)
	_, err = e.Svc.Tickets.Create(ctx, e.Editor, "ARC", badInput)
	wantSvcErr(t, err, service.KindConflict, service.CodeProjectArchived)
	_, err = e.Svc.Tickets.Create(ctx, e.Editor, "WEB", badInput)
	if se := wantSvcErr(t, err, service.KindValidation, ""); se.Fields["title"] == "" {
		t.Fatal("no title field")
	}
	readTok := e.TokenActor(e.NewToken(t, ed.ID, service.ScopeRead, ""))
	_, err = e.Svc.Tickets.Create(ctx, readTok, "ARC", badInput)
	wantSvcErr(t, err, service.KindForbidden, service.CodeInsufficientScope)

	// every ticket write in an archived project conflicts; reads work
	_, err = e.Svc.Tickets.Update(ctx, e.Editor, arcTicket.Ref, service.UpdateTicketInput{Title: service.Some("n")})
	wantSvcErr(t, err, service.KindConflict, service.CodeProjectArchived)
	_, err = e.Svc.Tickets.Move(ctx, e.Editor, arcTicket.Ref, service.MoveInput{Status: service.StatusDone})
	wantSvcErr(t, err, service.KindConflict, service.CodeProjectArchived)
	wantSvcErr(t, e.Svc.Tickets.Delete(ctx, e.Editor, arcTicket.Ref), service.KindConflict, service.CodeProjectArchived)
	if _, err := e.Svc.Tickets.Get(ctx, e.Viewer, arcTicket.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Tickets.Get(ctx, e.Viewer, seed.Ref); err != nil {
		t.Fatal(err)
	}
	// a non-member sees a ticket exactly like a nonexistent one
	_, err1 := e.Svc.Tickets.Get(ctx, outsider, seed.Ref)
	_, err2 := e.Svc.Tickets.Get(ctx, outsider, "WEB-999")
	if err1 == nil || err2 == nil || err1.Error() != err2.Error() {
		t.Fatalf("%v / %v", err1, err2)
	}
}
