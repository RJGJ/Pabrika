package service_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// Phase 3 WP3: the Phase 1 emit hooks asserted against a REAL hub (testutil.WithHub). Publish is
// synchronous and the channel buffers, so once a service call returns every event it emitted is
// already readable from the subscription.

// evEnv is a ticket env on a real hub with two live subscriptions per member of the project.
type evEnv struct {
	*tkEnv
	Hub  *service.Hub
	subs map[testutil.Who]*service.Subscription
}

func newEv(t *testing.T) *evEnv {
	t.Helper()
	env := testutil.NewTestServices(t, testutil.WithHub(service.HubOptions{StreamBuffer: 256}))
	e := &evEnv{tkEnv: newTkFrom(t, env), Hub: env.Hub, subs: map[testutil.Who]*service.Subscription{}}
	for _, w := range []testutil.Who{testutil.Owner, testutil.Editor, testutil.Viewer} {
		s, err := e.Hub.Subscribe(e.P.ID, e.M.Users[w].ID)
		if err != nil {
			t.Fatal(err)
		}
		e.subs[w] = s
	}
	return e
}

// drain returns the events currently buffered on the subscription of who.
func (e *evEnv) drain(who testutil.Who) []service.Event { return drainSub(e.subs[who]) }

func drainSub(s *service.Subscription) []service.Event {
	var out []service.Event
	for {
		select {
		case ev := <-s.C():
			out = append(out, ev)
		default:
			return out
		}
	}
}

// one drains the owner's subscription and requires exactly one event.
func (e *evEnv) one(t *testing.T) service.Event {
	t.Helper()
	evs := e.drain(testutil.Owner)
	if len(evs) != 1 {
		t.Fatalf("want exactly 1 event, got %d: %+v", len(evs), evs)
	}
	return evs[0]
}

func (e *evEnv) none(t *testing.T, what string) {
	t.Helper()
	for _, w := range []testutil.Who{testutil.Owner, testutil.Editor, testutil.Viewer} {
		if evs := e.drain(w); len(evs) != 0 {
			t.Fatalf("%s: %s's stream got events: %+v", what, w, evs)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func evPtr[T any](v T) *T { return &v }

func ownerEventActor(e *evEnv) service.EventActor {
	return service.EventActor{Type: service.ActorUser, ID: e.M.Users[testutil.Owner].ID}
}

// ---- catalog rows ----

// catalogMethods are the "Interface.Method" names the catalog table below must exercise; the
// completeness guard (TestEventsCompletenessGuard) uses the same list.
var catalogMethods = []string{
	"TicketService.Create", "TicketService.Update", "TicketService.Move", "TicketService.Delete",
	"CommentService.Add", "CommentService.Edit", "CommentService.Delete",
	"LabelService.Create", "LabelService.Update", "LabelService.Delete",
	"MemberService.Add", "MemberService.SetRole", "MemberService.Remove",
	"ProjectService.Update",
}

func TestEventsCatalogRows(t *testing.T) {
	e := newEv(t)
	owner := e.Owner
	outsider := e.M.Users[testutil.NonMember]
	viewerID := e.M.Users[testutil.Viewer].ID
	covered := map[string]bool{}

	var (
		tk      service.Ticket
		cm      service.Comment
		lb      service.Label
		moveRes service.MoveResult
	)
	type row struct {
		name   string
		method string
		run    func() error
		want   service.Event // Type, TicketID, CommentID, LabelID, UserID, Renumbered; ProjectID/Actor are filled in
		// ids is evaluated lazily because entity ids exist only after earlier rows ran.
		ids func(*service.Event)
	}
	rows := []row{
		{name: "ticket created", method: "TicketService.Create", run: func() (err error) {
			tk, err = e.Svc.Tickets.Create(ctx, owner, e.P.Key, service.CreateTicketInput{Title: "A", Status: service.StatusTodo})
			return
		}, want: service.Event{Type: service.EventTicketCreated}, ids: func(ev *service.Event) { ev.TicketID = tk.ID }},
		{name: "ticket updated", method: "TicketService.Update", run: func() (err error) {
			_, err = e.Svc.Tickets.Update(ctx, owner, tk.Ref, service.UpdateTicketInput{Title: service.Some("A2")})
			return
		}, want: service.Event{Type: service.EventTicketUpdated}, ids: func(ev *service.Event) { ev.TicketID = tk.ID }},
		{name: "ticket moved", method: "TicketService.Move", run: func() (err error) {
			moveRes, err = e.Svc.Tickets.Move(ctx, owner, tk.Ref, service.MoveInput{Status: service.StatusDone})
			return
		}, want: service.Event{Type: service.EventTicketMoved}, ids: func(ev *service.Event) { ev.TicketID = tk.ID }},
		{name: "comment added", method: "CommentService.Add", run: func() (err error) {
			cm, err = e.Svc.Comments.Add(ctx, owner, tk.Ref, "hello")
			return
		}, want: service.Event{Type: service.EventCommentAdded}, ids: func(ev *service.Event) { ev.TicketID, ev.CommentID = tk.ID, cm.ID }},
		{name: "comment edited", method: "CommentService.Edit", run: func() (err error) {
			_, err = e.Svc.Comments.Edit(ctx, owner, cm.ID, "edited")
			return
		}, want: service.Event{Type: service.EventCommentChanged}, ids: func(ev *service.Event) { ev.TicketID, ev.CommentID = tk.ID, cm.ID }},
		{name: "comment deleted", method: "CommentService.Delete", run: func() error {
			return e.Svc.Comments.Delete(ctx, owner, cm.ID)
		}, want: service.Event{Type: service.EventCommentChanged}, ids: func(ev *service.Event) { ev.TicketID, ev.CommentID = tk.ID, cm.ID }},
		{name: "label created", method: "LabelService.Create", run: func() (err error) {
			lb, err = e.Svc.Labels.Create(ctx, owner, e.P.Key, service.LabelInput{Name: "bug", Color: "red"})
			return
		}, want: service.Event{Type: service.EventLabelChanged}, ids: func(ev *service.Event) { ev.LabelID = lb.ID }},
		{name: "label updated", method: "LabelService.Update", run: func() (err error) {
			_, err = e.Svc.Labels.Update(ctx, owner, lb.ID, service.UpdateLabelInput{Name: service.Some("bugs")})
			return
		}, want: service.Event{Type: service.EventLabelChanged}, ids: func(ev *service.Event) { ev.LabelID = lb.ID }},
		{name: "label deleted", method: "LabelService.Delete", run: func() error {
			return e.Svc.Labels.Delete(ctx, owner, lb.ID)
		}, want: service.Event{Type: service.EventLabelChanged}, ids: func(ev *service.Event) { ev.LabelID = lb.ID }},
		{name: "member added", method: "MemberService.Add", run: func() (err error) {
			_, err = e.Svc.Members.Add(ctx, owner, e.P.Key, outsider.Email, service.RoleViewer)
			return
		}, want: service.Event{Type: service.EventMemberChanged, UserID: outsider.ID}},
		{name: "member role changed", method: "MemberService.SetRole", run: func() (err error) {
			_, err = e.Svc.Members.SetRole(ctx, owner, e.P.Key, viewerID, service.RoleEditor)
			return
		}, want: service.Event{Type: service.EventMemberChanged, UserID: viewerID}},
		{name: "member removed", method: "MemberService.Remove", run: func() error {
			return e.Svc.Members.Remove(ctx, owner, e.P.Key, outsider.ID)
		}, want: service.Event{Type: service.EventMemberChanged, UserID: outsider.ID}},
		{name: "project renamed", method: "ProjectService.Update", run: func() (err error) {
			_, err = e.Svc.Projects.Update(ctx, owner, e.P.Key, service.UpdateProjectInput{Name: service.Some("Renamed")})
			return
		}, want: service.Event{Type: service.EventProjectUpdated}},
		{name: "project archived", method: "ProjectService.Update", run: func() (err error) {
			_, err = e.Svc.Projects.Update(ctx, owner, e.P.Key, service.UpdateProjectInput{Archived: service.Some(true)})
			return
		}, want: service.Event{Type: service.EventProjectUpdated}},
		{name: "project unarchived", method: "ProjectService.Update", run: func() (err error) {
			_, err = e.Svc.Projects.Update(ctx, owner, e.P.Key, service.UpdateProjectInput{Archived: service.Some(false)})
			return
		}, want: service.Event{Type: service.EventProjectUpdated}},
		{name: "ticket deleted", method: "TicketService.Delete", run: func() error {
			return e.Svc.Tickets.Delete(ctx, owner, tk.Ref)
		}, want: service.Event{Type: service.EventTicketDeleted}, ids: func(ev *service.Event) { ev.TicketID = tk.ID }},
	}
	for _, r := range rows {
		e.Clock.Advance(time.Second)
		want := r.want
		if err := r.run(); err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		if r.ids != nil {
			r.ids(&want)
		}
		want.ProjectID = e.P.ID
		want.Actor = ownerEventActor(e)
		want.At = e.Clock.Now()
		got := e.one(t)
		if got != want {
			t.Fatalf("%s:\n got  %+v\n want %+v", r.name, got, want)
		}
		covered[r.method] = true
		// every member's stream got the same single event
		if r.name != "member removed" {
			for _, w := range []testutil.Who{testutil.Editor, testutil.Viewer} {
				if evs := e.drain(w); len(evs) != 1 || evs[0] != want {
					t.Fatalf("%s: %s's stream: %+v", r.name, w, evs)
				}
			}
		} else {
			e.drain(testutil.Editor)
			e.drain(testutil.Viewer)
		}
	}
	_ = moveRes
	for _, m := range catalogMethods {
		if !covered[m] {
			t.Errorf("catalog method %s has no row in TestEventsCatalogRows", m)
		}
	}
}

func TestEventsRenumberedFlag(t *testing.T) {
	e := newEv(t)
	a, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "A", Status: service.StatusTodo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "B", Status: service.StatusTodo}); err != nil {
		t.Fatal(err)
	}
	e.drain(testutil.Owner)
	renumbered := 0
	for i := 0; i < 60; i++ {
		x, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: fmt.Sprintf("X%02d", i), Status: service.StatusTodo})
		if err != nil {
			t.Fatal(err)
		}
		e.drain(testutil.Owner)
		r, err := e.Svc.Tickets.Move(ctx, e.Owner, x.Ref, service.MoveInput{Status: service.StatusTodo, After: a.ID})
		if err != nil {
			t.Fatal(err)
		}
		ev := e.one(t)
		if ev.Type != service.EventTicketMoved || ev.TicketID != x.ID || ev.Renumbered != r.Renumbered {
			t.Fatalf("move %d: event %+v, result renumbered=%v", i, ev, r.Renumbered)
		}
		if r.Renumbered {
			renumbered++
		}
	}
	if renumbered == 0 {
		t.Fatal("expected at least one renumbering move")
	}
}

// ---- no event ----

func TestEventsNoEventOnRollback(t *testing.T) {
	e := newEv(t)
	tk, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	cm, err := e.Svc.Comments.Add(ctx, e.Owner, tk.Ref, "c")
	if err != nil {
		t.Fatal(err)
	}
	e.drain(testutil.Owner)
	e.drain(testutil.Editor)
	e.drain(testutil.Viewer)

	// validation failures
	if _, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: ""}); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, err := e.Svc.Labels.Create(ctx, e.Owner, e.P.Key, service.LabelInput{Name: "", Color: "red"}); err == nil {
		t.Fatal("empty label accepted")
	}
	if _, err := e.Svc.Comments.Add(ctx, e.Owner, tk.Ref, ""); err == nil {
		t.Fatal("empty comment accepted")
	}
	// permission failures
	if _, err := e.Svc.Tickets.Create(ctx, e.Viewer, e.P.Key, service.CreateTicketInput{Title: "no"}); err == nil {
		t.Fatal("viewer created a ticket")
	}
	if _, err := e.Svc.Projects.Update(ctx, e.Editor, e.P.Key, service.UpdateProjectInput{Name: service.Some("x")}); err == nil {
		t.Fatal("editor renamed the project")
	}
	if err := e.Svc.Members.Remove(ctx, e.Editor, e.P.Key, e.M.Users[testutil.Viewer].ID); err == nil {
		t.Fatal("editor removed a member")
	}
	// read-scope token
	ro := e.TokenActor(e.NewToken(t, e.M.Users[testutil.Owner].ID, service.ScopeRead, ""))
	if _, err := e.Svc.Tickets.Update(ctx, ro, tk.Ref, service.UpdateTicketInput{Title: service.Some("ro")}); err == nil {
		t.Fatal("read token wrote")
	}
	e.none(t, "validation/permission failures")

	// project_archived
	if _, err := e.Svc.Projects.Update(ctx, e.Owner, e.P.Key, service.UpdateProjectInput{Archived: service.Some(true)}); err != nil {
		t.Fatal(err)
	}
	if evs := e.drain(testutil.Owner); len(evs) != 1 || evs[0].Type != service.EventProjectUpdated {
		t.Fatalf("archive events: %+v", evs)
	}
	e.drain(testutil.Editor)
	e.drain(testutil.Viewer)
	_, err = e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "late"})
	wantSvcErr(t, err, service.KindConflict, "project_archived")
	_, err = e.Svc.Tickets.Update(ctx, e.Owner, tk.Ref, service.UpdateTicketInput{Title: service.Some("late")})
	wantSvcErr(t, err, service.KindConflict, "project_archived")
	_, err = e.Svc.Comments.Edit(ctx, e.Owner, cm.ID, "late")
	wantSvcErr(t, err, service.KindConflict, "project_archived")
	_, err = e.Svc.Labels.Create(ctx, e.Owner, e.P.Key, service.LabelInput{Name: "late", Color: "red"})
	wantSvcErr(t, err, service.KindConflict, "project_archived")
	e.none(t, "project_archived writes")
}

func TestEventsNoEventOnNoOps(t *testing.T) {
	e := newEv(t)
	a, _ := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "A", Status: service.StatusTodo})
	b, _ := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "B", Status: service.StatusTodo})
	c, _ := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "C", Status: service.StatusTodo})
	cm, _ := e.Svc.Comments.Add(ctx, e.Owner, a.Ref, "body")
	lb, err := e.Svc.Labels.Create(ctx, e.Owner, e.P.Key, service.LabelInput{Name: "bug", Color: "red"})
	if err != nil {
		t.Fatal(err)
	}
	e.drain(testutil.Owner)
	e.drain(testutil.Editor)
	e.drain(testutil.Viewer)
	e.Clock.Advance(time.Second)

	noops := []struct {
		name string
		run  func() error
	}{
		{"Tickets.Update unchanged", func() error {
			_, err := e.Svc.Tickets.Update(ctx, e.Owner, a.Ref, service.UpdateTicketInput{Title: service.Some("A"), Priority: service.Some(a.Priority)})
			return err
		}},
		{"Tickets.Move to the same spot (after predecessor)", func() error {
			_, err := e.Svc.Tickets.Move(ctx, e.Owner, b.Ref, service.MoveInput{Status: service.StatusTodo, After: a.ID})
			return err
		}},
		{"Tickets.Move to the same spot (before successor)", func() error {
			_, err := e.Svc.Tickets.Move(ctx, e.Owner, b.Ref, service.MoveInput{Status: service.StatusTodo, Before: c.ID})
			return err
		}},
		{"Members.SetRole same role", func() error {
			_, err := e.Svc.Members.SetRole(ctx, e.Owner, e.P.Key, e.M.Users[testutil.Editor].ID, service.RoleEditor)
			return err
		}},
		{"Labels.Update same values", func() error {
			_, err := e.Svc.Labels.Update(ctx, e.Owner, lb.ID, service.UpdateLabelInput{Name: service.Some("bug"), Color: service.Some(service.LabelColor("red"))})
			return err
		}},
		{"Comments.Edit identical body", func() error {
			_, err := e.Svc.Comments.Edit(ctx, e.Owner, cm.ID, "body")
			return err
		}},
		{"Projects.Update unchanged", func() error {
			_, err := e.Svc.Projects.Update(ctx, e.Owner, e.P.Key, service.UpdateProjectInput{Name: service.Some("Project " + e.P.Key), Archived: service.Some(false)})
			return err
		}},
	}
	for _, n := range noops {
		if err := n.run(); err != nil {
			t.Fatalf("%s: %v", n.name, err)
		}
		e.none(t, n.name)
	}
}

func TestEventsUpdateProfileEmitsNothing(t *testing.T) {
	e := newEv(t)
	if _, err := e.Svc.Users.UpdateProfile(ctx, e.Owner, "New Name"); err != nil {
		t.Fatal(err)
	}
	e.none(t, "UpdateProfile")
}

func TestEventsOtherUserAndTokenOpsEmitNothing(t *testing.T) {
	e := newEv(t)
	if _, err := e.Svc.Users.Create(ctx, "new@example.com", "New", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.Users.SetPassword(ctx, e.M.Users[testutil.Owner].ID, "hash2", ""); err != nil {
		t.Fatal(err)
	}
	tok, err := e.Svc.Tokens.Create(ctx, e.Owner, service.CreateTokenInput{Name: "t", Scope: service.ScopeWrite, Hash: "h", Prefix: "pb_12345"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.Tokens.Revoke(ctx, e.Owner, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Projects.Create(ctx, e.Owner, service.CreateProjectInput{Key: "NEWP", Name: "New"}); err != nil {
		t.Fatal(err)
	}
	e.none(t, "user, token and project-create operations")
}

func TestEventsMultiFieldUpdateEmitsOneEvent(t *testing.T) {
	e := newEv(t)
	a, _ := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "A"})
	lb, err := e.Svc.Labels.Create(ctx, e.Owner, e.P.Key, service.LabelInput{Name: "bug", Color: "red"})
	if err != nil {
		t.Fatal(err)
	}
	e.drain(testutil.Owner)
	assignee := e.M.Users[testutil.Editor].ID
	_, err = e.Svc.Tickets.Update(ctx, e.Owner, a.Ref, service.UpdateTicketInput{
		Title: service.Some("A2"), Description: service.Some("d"), Priority: service.Some(service.PriorityHigh),
		DueDate: service.Some(evPtr("2030-01-02")), AssigneeID: service.Some(&assignee),
		LabelIDs: service.Some([]string{lb.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev := e.one(t); ev.Type != service.EventTicketUpdated || ev.TicketID != a.ID {
		t.Fatalf("event: %+v", ev)
	}
}

// ---- single-event rules ----

func TestEventsLabelDeleteEmitsOnlyLabelChanged(t *testing.T) {
	e := newEv(t)
	lb, err := e.Svc.Labels.Create(ctx, e.Owner, e.P.Key, service.LabelInput{Name: "bug", Color: "red"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "A", LabelIDs: []string{lb.ID}})
	b, _ := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "B", LabelIDs: []string{lb.ID}})
	_, _ = a, b
	e.drain(testutil.Owner)
	if err := e.Svc.Labels.Delete(ctx, e.Owner, lb.ID); err != nil {
		t.Fatal(err)
	}
	if ev := e.one(t); ev.Type != service.EventLabelChanged || ev.LabelID != lb.ID || ev.TicketID != "" {
		t.Fatalf("event: %+v", ev)
	}
}

func TestEventsMemberRemovalEmitsOnlyMemberChanged(t *testing.T) {
	e := newEv(t)
	editor := e.M.Users[testutil.Editor]
	for i := 0; i < 3; i++ {
		if _, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: fmt.Sprint("T", i), AssigneeID: &editor.ID}); err != nil {
			t.Fatal(err)
		}
	}
	e.drain(testutil.Owner)
	if err := e.Svc.Members.Remove(ctx, e.Owner, e.P.Key, editor.ID); err != nil {
		t.Fatal(err)
	}
	if ev := e.one(t); ev.Type != service.EventMemberChanged || ev.UserID != editor.ID {
		t.Fatalf("event: %+v", ev)
	}
}

// ---- post-commit read ----

func TestEventsPublishedAfterCommit(t *testing.T) {
	e := newEv(t)
	tk, err := e.Svc.Tickets.Create(ctx, e.Owner, e.P.Key, service.CreateTicketInput{Title: "before"})
	if err != nil {
		t.Fatal(err)
	}
	e.drain(testutil.Owner)

	// A hub subscriber reads the new state the moment the event can be received.
	type result struct {
		title string
		err   error
	}
	got := make(chan result, 1)
	go func() {
		select {
		case ev := <-e.subs[testutil.Viewer].C():
			tt, err := e.Svc.Tickets.Get(ctx, e.Viewer, ev.TicketID)
			got <- result{tt.Title, err}
		case <-time.After(5 * time.Second):
			got <- result{err: fmt.Errorf("no event")}
		}
	}()
	if _, err := e.Svc.Tickets.Update(ctx, e.Owner, tk.Ref, service.UpdateTicketInput{Title: service.Some("after")}); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.err != nil || r.title != "after" {
		t.Fatalf("post-commit read: %+v", r)
	}

	// And through the recording hook of a fresh recorder-based env: the DB is already updated
	// inside Publish.
	env := testutil.NewTestServices(t)
	e2 := newTkFrom(t, env)
	tk2 := e2.create(t, "before", service.StatusTodo)
	seen := ""
	env.Pub.OnPublish = func(ev service.Event) {
		tt, err := env.Svc.Tickets.Get(ctx, e2.Owner, ev.TicketID)
		if err == nil {
			seen = tt.Title
		}
	}
	if _, err := env.Svc.Tickets.Update(ctx, e2.Owner, tk2.Ref, service.UpdateTicketInput{Title: service.Some("after")}); err != nil {
		t.Fatal(err)
	}
	if seen != "after" {
		t.Fatalf("state inside Publish: %q", seen)
	}
}

// ---- access-loss closes ----

func isDoneWith(t *testing.T, s *service.Subscription, want service.CloseReason) {
	t.Helper()
	select {
	case <-s.Done():
		if s.Reason() != want {
			t.Fatalf("reason %q, want %q", s.Reason(), want)
		}
	default:
		t.Fatalf("subscription still open, want closed with %q", want)
	}
}

func TestEventsMemberRemovalClosesOnlyThatUsersStream(t *testing.T) {
	e := newEv(t)
	viewer := e.M.Users[testutil.Viewer]
	if err := e.Svc.Members.Remove(ctx, e.Owner, e.P.Key, viewer.ID); err != nil {
		t.Fatal(err)
	}
	isDoneWith(t, e.subs[testutil.Viewer], service.CloseRemoved)
	if isDone(e.subs[testutil.Owner]) || isDone(e.subs[testutil.Editor]) {
		t.Fatal("other members' streams closed")
	}
	// member.changed was published to the others (the removed stream is already gone).
	for _, w := range []testutil.Who{testutil.Owner, testutil.Editor} {
		evs := e.drain(w)
		if len(evs) != 1 || evs[0].Type != service.EventMemberChanged || evs[0].UserID != viewer.ID {
			t.Fatalf("%s: %+v", w, evs)
		}
	}
	// a second stream of the removed user, on the same project, also ends; other projects do not
	otherProject := e.NewProject(t, e.M.Users[testutil.Owner], "OTH")
	other, _ := e.Hub.Subscribe(otherProject.ID, viewer.ID)
	e.Hub.CloseUser(e.P.ID, viewer.ID, service.CloseRemoved) // idempotent
	if isDone(other) {
		t.Fatal("stream on another project closed")
	}
}

func TestEventsLeaveClosesOnlyThatUsersStream(t *testing.T) {
	e := newEv(t)
	editor := e.M.Users[testutil.Editor]
	if err := e.Svc.Members.Remove(ctx, e.Editor, e.P.Key, editor.ID); err != nil {
		t.Fatal(err)
	}
	isDoneWith(t, e.subs[testutil.Editor], service.CloseRemoved)
	if isDone(e.subs[testutil.Owner]) || isDone(e.subs[testutil.Viewer]) {
		t.Fatal("other members' streams closed")
	}
	if evs := e.drain(testutil.Owner); len(evs) != 1 || evs[0].Type != service.EventMemberChanged || evs[0].UserID != editor.ID {
		t.Fatalf("events: %+v", evs)
	}
	// the actor of a leave is the leaver
	if evs := e.drain(testutil.Viewer); len(evs) != 1 || evs[0].Actor.ID != editor.ID {
		t.Fatalf("leave actor: %+v", evs)
	}
}

func TestEventsProjectDeleteClosesAllStreamsWithoutEvent(t *testing.T) {
	e := newEv(t)
	other := e.NewProject(t, e.M.Users[testutil.Owner], "OTH")
	otherSub, _ := e.Hub.Subscribe(other.ID, e.M.Users[testutil.Owner].ID)
	if err := e.Svc.Projects.Delete(ctx, e.Owner, e.P.Key); err != nil {
		t.Fatal(err)
	}
	for _, w := range []testutil.Who{testutil.Owner, testutil.Editor, testutil.Viewer} {
		isDoneWith(t, e.subs[w], service.CloseProject)
		if evs := e.drain(w); len(evs) != 0 {
			t.Fatalf("%s got events on project delete: %+v", w, evs)
		}
	}
	if isDone(otherSub) {
		t.Fatal("another project's stream closed")
	}
	if n := e.Hub.SubscriberCount(e.P.ID); n != 0 {
		t.Fatalf("subscribers left: %d", n)
	}
}

func TestEventsRoleChangeAndArchiveKeepStreamsOpen(t *testing.T) {
	e := newEv(t)
	if _, err := e.Svc.Members.SetRole(ctx, e.Owner, e.P.Key, e.M.Users[testutil.Editor].ID, service.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Projects.Update(ctx, e.Owner, e.P.Key, service.UpdateProjectInput{Archived: service.Some(true)}); err != nil {
		t.Fatal(err)
	}
	for _, w := range []testutil.Who{testutil.Owner, testutil.Editor, testutil.Viewer} {
		if isDone(e.subs[w]) {
			t.Fatalf("%s's stream closed", w)
		}
		if evs := e.drain(w); len(evs) != 2 {
			t.Fatalf("%s: %+v", w, evs)
		}
	}
}

// ---- actor shape ----

func TestEventsActorShape(t *testing.T) {
	e := newEv(t)
	ownerID := e.M.Users[testutil.Owner].ID
	tokenID := e.NewToken(t, ownerID, service.ScopeWrite, "")
	tactor := e.TokenActor(tokenID)

	tk, err := e.Svc.Tickets.Create(ctx, tactor, e.P.Key, service.CreateTicketInput{Title: "by agent"})
	if err != nil {
		t.Fatal(err)
	}
	ev := e.one(t)
	if ev.Actor != (service.EventActor{Type: service.ActorAPIToken, ID: tokenID}) {
		t.Fatalf("token actor: %+v", ev.Actor)
	}
	// the owner's id appears nowhere in a token event's JSON
	b := mustJSON(t, ev)
	if strings.Contains(b, ownerID) {
		t.Fatalf("owner user id leaked into a token event: %s", b)
	}
	if _, err := e.Svc.Comments.Add(ctx, tactor, tk.Ref, "c"); err != nil {
		t.Fatal(err)
	}
	if ev := e.one(t); ev.Actor.Type != service.ActorAPIToken || ev.Actor.ID != tokenID {
		t.Fatalf("comment actor: %+v", ev.Actor)
	}

	// a session write
	if _, err := e.Svc.Tickets.Update(ctx, e.Editor, tk.Ref, service.UpdateTicketInput{Title: service.Some("x")}); err != nil {
		t.Fatal(err)
	}
	if ev := e.one(t); ev.Actor != (service.EventActor{Type: service.ActorUser, ID: e.M.Users[testutil.Editor].ID}) {
		t.Fatalf("user actor: %+v", ev.Actor)
	}
}

// ---- completeness guard ----

// noEventMethods are the service methods that deliberately publish nothing: reads, and writes
// whose outcome is decided in the spec (Projects.Delete closes streams instead of emitting;
// Projects.Create, profile, password, user and token operations emit nothing).
var noEventMethods = []string{
	"ProjectService.Create", "ProjectService.Get", "ProjectService.Resolve", "ProjectService.List", "ProjectService.Delete",
	"MemberService.List",
	"LabelService.List",
	"TicketService.Get", "TicketService.List", "TicketService.Resolve",
	"CommentService.List", "CommentService.Latest", "CommentService.Resolve",
	"ActivityService.List",
	"UserService.UpdateProfile", "UserService.Create", "UserService.ValidateNew", "UserService.Credentials",
	"UserService.PasswordHash", "UserService.SetPassword",
	"TokenService.Create", "TokenService.List", "TokenService.Revoke",
}

func serviceMethods() map[string]bool {
	out := map[string]bool{}
	for _, it := range []reflect.Type{
		reflect.TypeOf((*service.ProjectService)(nil)).Elem(),
		reflect.TypeOf((*service.MemberService)(nil)).Elem(),
		reflect.TypeOf((*service.LabelService)(nil)).Elem(),
		reflect.TypeOf((*service.TicketService)(nil)).Elem(),
		reflect.TypeOf((*service.CommentService)(nil)).Elem(),
		reflect.TypeOf((*service.ActivityService)(nil)).Elem(),
		reflect.TypeOf((*service.UserService)(nil)).Elem(),
		reflect.TypeOf((*service.TokenService)(nil)).Elem(),
	} {
		for i := 0; i < it.NumMethod(); i++ {
			out[it.Name()+"."+it.Method(i).Name] = true
		}
	}
	return out
}

// TestEventsCompletenessGuard fails when a service method is neither in the event catalog nor on
// the explicit no-event allowlist, so a new write method cannot silently skip its event.
func TestEventsCompletenessGuard(t *testing.T) {
	methods := serviceMethods()
	known := map[string]string{}
	for _, m := range catalogMethods {
		known[m] = "catalog"
	}
	for _, m := range noEventMethods {
		if prev, dup := known[m]; dup {
			t.Errorf("%s is both in the catalog and the no-event allowlist (%s)", m, prev)
		}
		known[m] = "no-event"
	}
	var unclassified, stale []string
	for m := range methods {
		if known[m] == "" {
			unclassified = append(unclassified, m)
		}
	}
	for m := range known {
		if !methods[m] {
			stale = append(stale, m)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 {
		t.Errorf("service methods with no event decision (add to catalogMethods with a catalog row, or to noEventMethods): %v", unclassified)
	}
	if len(stale) > 0 {
		t.Errorf("stale catalog/allowlist names (method no longer exists): %v", stale)
	}
}
