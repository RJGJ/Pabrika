package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store/db"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

var ctx = context.Background()

func TestWriteHelperPublishesAfterCommit(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	actor := env.UserActor(u)
	var order []string
	var seen string
	env.Pub.OnPublish = func(e service.Event) {
		order = append(order, "publish:"+string(e.Type))
		// the new state must already be visible to a subscriber reading the DB
		usr, err := env.Store.Read().GetUserByID(ctx, u.ID)
		if err != nil {
			t.Error(err)
		}
		seen = usr.DisplayName
	}
	err := env.Svc.Write(ctx, actor, func(tx *service.Tx) error {
		if _, err := tx.Q.UpdateUserDisplayName(ctx, db.UpdateUserDisplayNameParams{DisplayName: "Changed", ID: u.ID}); err != nil {
			return err
		}
		tx.AfterCommit(func() { order = append(order, "after1") })
		tx.Emit(service.Event{Type: service.EventTicketCreated, ProjectID: "P", TicketID: "T1"})
		tx.AfterCommit(func() { order = append(order, "after2") })
		tx.Emit(service.Event{Type: service.EventTicketUpdated, ProjectID: "P", TicketID: "T1"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "publish:ticket.created,publish:ticket.updated,after1,after2"
	if strings.Join(order, ",") != want {
		t.Fatalf("order = %v", order)
	}
	if seen != "Changed" {
		t.Fatalf("subscriber saw %q", seen)
	}
	ev := env.Pub.Events()
	if len(ev) != 2 || ev[0].Actor != (service.EventActor{Type: service.ActorUser, ID: u.ID}) || !ev[0].At.Equal(env.Clock.Now()) {
		t.Fatalf("event not filled: %+v", ev)
	}
}

func TestWriteHelperDiscardsOnFailure(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	actor := env.UserActor(u)
	called := false
	boom := errors.New("boom")
	err := env.Svc.Write(ctx, actor, func(tx *service.Tx) error {
		tx.Emit(service.Event{Type: service.EventTicketCreated, ProjectID: "P"})
		tx.AfterCommit(func() { called = true })
		return boom
	})
	if !errors.Is(err, boom) || len(env.Pub.Events()) != 0 || called {
		t.Fatal("error path leaked", err, env.Pub.Events(), called)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic swallowed")
			}
		}()
		_ = env.Svc.Write(ctx, actor, func(tx *service.Tx) error {
			tx.Emit(service.Event{Type: service.EventTicketCreated, ProjectID: "P"})
			tx.AfterCommit(func() { called = true })
			panic("kaboom")
		})
	}()
	if len(env.Pub.Events()) != 0 || called {
		t.Fatal("panic path leaked")
	}
	// a callback that emits nothing publishes nothing
	if err := env.Svc.Write(ctx, actor, func(tx *service.Tx) error { return nil }); err != nil || len(env.Pub.Events()) != 0 {
		t.Fatal("no-op published")
	}
}

func TestDefaultDeps(t *testing.T) {
	env := testutil.NewTestServices(t)
	svc := service.New(env.Store, service.Deps{})
	if svc.Users == nil || svc.Projects == nil || svc.Members == nil || svc.Labels == nil || svc.Tickets == nil || svc.Comments == nil || svc.Activity == nil {
		t.Fatal("services not wired")
	}
	if err := svc.Write(ctx, service.UserActor("x"), func(tx *service.Tx) error {
		tx.Emit(service.Event{Type: service.EventProjectUpdated, ProjectID: "P"})
		tx.AfterCommit(func() {})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMonotonicIDs(t *testing.T) {
	env := testutil.NewTestServices(t)
	prev := ""
	for i := 0; i < 200; i++ {
		id := env.NewID() // same fake millisecond throughout
		if id <= prev || len(id) != 26 || id != strings.ToUpper(id) {
			t.Fatalf("id %d not monotonic/uppercase: %s <= %s", i, id, prev)
		}
		prev = id
	}
	env.Clock.Advance(time.Hour)
	if id := env.NewID(); id <= prev {
		t.Fatal("id after clock advance must sort later")
	}
}

func TestRequireProjectMatrix(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	for _, ref := range []string{"WEB", "web", m.Project.ID, strings.ToLower(m.Project.ID)} {
		ref := ref
		m.Run(t, testutil.MatrixCase{
			Name: "read/" + ref,
			Op: func(t *testing.T, a service.Actor) error {
				_, _, err := env.Svc.RequireProject(ctx, a, ref, service.RoleViewer, false)
				return err
			},
			Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.OK, testutil.Editor: testutil.OK, testutil.Owner: testutil.OK},
		})
	}
	m.Run(t, testutil.MatrixCase{
		Name: "editor-write",
		Op: func(t *testing.T, a service.Actor) error {
			_, _, err := env.Svc.RequireProject(ctx, a, "WEB", service.RoleEditor, true)
			return err
		},
		Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden("forbidden"), testutil.Editor: testutil.OK, testutil.Owner: testutil.OK},
	})
	m.Run(t, testutil.MatrixCase{
		Name: "owner-only",
		Op: func(t *testing.T, a service.Actor) error {
			_, _, err := env.Svc.RequireProject(ctx, a, "WEB", service.RoleOwner, true)
			return err
		},
		Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden("forbidden"), testutil.Editor: testutil.Forbidden("forbidden"), testutil.Owner: testutil.OK},
	})
	m.Run(t, testutil.MatrixCase{
		Name: "session-only",
		Op: func(t *testing.T, a service.Actor) error {
			if err := env.Svc.RequireSession(a); err != nil {
				return err
			}
			_, _, err := env.Svc.RequireProject(ctx, a, "NOPE", service.RoleOwner, true) // nothing is looked up for tokens
			return err
		},
		SessionOnly: true,
		Want:        map[testutil.Who]testutil.Outcome{testutil.Owner: testutil.NotFound, testutil.NonMember: testutil.NotFound},
	})
}

func TestNotFoundIsIndistinguishable(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	out := env.UserActor(m.Users[testutil.NonMember])
	_, _, e1 := env.Svc.RequireProject(ctx, out, "WEB", service.RoleViewer, false)
	_, _, e2 := env.Svc.RequireProject(ctx, out, "NOPE", service.RoleViewer, false)
	_, _, e3 := env.Svc.RequireProject(ctx, out, "01HZX3Q9V2K7M8N4P5R6S7T8W9", service.RoleViewer, false)
	_, _, e4 := env.Svc.RequireProject(ctx, out, "garbage ref!", service.RoleViewer, false)
	for _, e := range []error{e1, e2, e3, e4} {
		var se *service.Error
		if !errors.As(e, &se) || se.Kind != service.KindNotFound || se.Message != "Not found" {
			t.Fatalf("not identical: %v", e)
		}
	}
}

func TestTokenScopeAndLimit(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	other := env.NewProject(t, m.Users[testutil.Owner], "OTH")
	owner := m.Users[testutil.Owner]

	readTok := env.TokenActor(env.NewToken(t, owner.ID, service.ScopeRead, ""))
	_, role, err := env.Svc.RequireProject(ctx, readTok, "WEB", service.RoleViewer, false)
	if err != nil || role != service.RoleViewer {
		t.Fatalf("read token owner should be effective viewer: %v %v", role, err)
	}
	for _, min := range []service.Role{service.RoleEditor, service.RoleOwner} {
		_, _, err = env.Svc.RequireProject(ctx, readTok, "WEB", min, true)
		testutil.AssertOutcome(t, err, testutil.Forbidden("insufficient_scope"))
	}
	// write flag alone (an op that needs only viewer role but writes) also needs write scope
	_, _, err = env.Svc.RequireProject(ctx, readTok, "WEB", service.RoleViewer, true)
	testutil.AssertOutcome(t, err, testutil.Forbidden("insufficient_scope"))
	// non-member read token: 404 comes before scope
	outTok := env.TokenActor(env.NewToken(t, m.Users[testutil.NonMember].ID, service.ScopeRead, ""))
	_, _, err = env.Svc.RequireProject(ctx, outTok, "WEB", service.RoleEditor, true)
	testutil.AssertOutcome(t, err, testutil.NotFound)

	// empty scope behaves as write
	emptyScope := service.TokenActor("T", owner.ID, "", "")
	if _, role, err := env.Svc.RequireProject(ctx, emptyScope, "WEB", service.RoleOwner, true); err != nil || role != service.RoleOwner {
		t.Fatal(role, err)
	}

	// project-limited token
	lim := env.TokenActor(env.NewToken(t, owner.ID, service.ScopeWrite, m.Project.ID))
	for _, ref := range []string{"WEB", m.Project.ID} {
		if _, _, err := env.Svc.RequireProject(ctx, lim, ref, service.RoleOwner, true); err != nil {
			t.Fatalf("limited token on its project via %s: %v", ref, err)
		}
	}
	for _, ref := range []string{"OTH", other.ID} {
		_, _, err := env.Svc.RequireProject(ctx, lim, ref, service.RoleViewer, false)
		testutil.AssertOutcome(t, err, testutil.NotFound)
	}
	if err := env.Svc.RequireUnlimited(lim); err == nil {
		t.Fatal("limited token must not create projects")
	} else {
		testutil.AssertOutcome(t, err, testutil.Forbidden("forbidden"))
	}
	if err := env.Svc.RequireUnlimited(readTok); err != nil {
		t.Fatal(err)
	}
	testutil.AssertOutcome(t, env.Svc.CheckActor(readTok, true), testutil.Forbidden("insufficient_scope"))
	if err := env.Svc.CheckActor(readTok, false); err != nil {
		t.Fatal(err)
	}
	// invalid actor type
	bad := service.Actor{Type: "robot", ID: "x", UserID: owner.ID}
	testutil.AssertOutcome(t, env.Svc.CheckActor(bad, false), testutil.Forbidden("forbidden"))
	_, _, err = env.Svc.RequireProject(ctx, bad, "WEB", service.RoleViewer, false)
	testutil.AssertOutcome(t, err, testutil.Forbidden("forbidden"))
}

// One input that is simultaneously "invalid" (validation is the caller's last step),
// forbidden and in an archived project must report in the documented order.
func TestOrderOfChecks(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	env.Archive(t, m.Project.ID)
	call := func(a service.Actor, ref string) error {
		_, _, err := env.Svc.RequireProjectWrite(ctx, a, ref, service.RoleEditor)
		return err
	}
	testutil.AssertOutcome(t, call(env.UserActor(m.Users[testutil.NonMember]), "WEB"), testutil.NotFound)
	testutil.AssertOutcome(t, call(env.UserActor(m.Users[testutil.Viewer]), "WEB"), testutil.Forbidden("forbidden"))
	testutil.AssertOutcome(t, call(env.UserActor(m.Users[testutil.Editor]), "WEB"), testutil.Conflict("project_archived"))
	readTok := env.TokenActor(env.NewToken(t, m.Users[testutil.Editor].ID, service.ScopeRead, ""))
	testutil.AssertOutcome(t, call(readTok, "WEB"), testutil.Forbidden("insufficient_scope"))
	// owner-only ops (requireProject, no archived check) still work on an archived project
	if _, _, err := env.Svc.RequireProject(ctx, env.UserActor(m.Users[testutil.Owner]), "WEB", service.RoleOwner, true); err != nil {
		t.Fatal(err)
	}
	// reads still work
	if _, _, err := env.Svc.RequireProject(ctx, env.UserActor(m.Users[testutil.Viewer]), "WEB", service.RoleViewer, false); err != nil {
		t.Fatal(err)
	}
	// live project, editor: passes authz; the caller's Validate() would then report fields
	live := env.NewProject(t, m.Users[testutil.Owner], "LIV")
	env.AddMember(t, live.ID, m.Users[testutil.Editor].ID, service.RoleEditor)
	if err := call(env.UserActor(m.Users[testutil.Editor]), "LIV"); err != nil {
		t.Fatal(err)
	}
	if err := (service.CreateTicketInput{}).Validate(); err == nil {
		t.Fatal("validation precondition")
	}
}

func TestOwnerLookups(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	other := env.NewUser(t, "other@x.io", "Other")
	otherProj := env.NewProject(t, other, "OTH")

	tID, lID, cID := env.NewID(), env.NewID(), env.NewID()
	delTicket, delComment := env.NewID(), env.NewID()
	now := "2026-01-01T00:00:00.000Z"
	exec := func(q string, args ...any) {
		t.Helper()
		if err := env.Store.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	ins := "INSERT INTO tickets (id, project_id, number, title, position, deleted_at, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)"
	exec(ins, tID, m.Project.ID, 1, "t", 1024, nil, now, now)
	exec(ins, delTicket, m.Project.ID, 2, "gone", 2048, now, now, now)
	exec("INSERT INTO labels (id, project_id, name) VALUES (?,?,?)", lID, m.Project.ID, "bug")
	cins := "INSERT INTO comments (id, ticket_id, author_type, author_id, body, created_at, deleted_at) VALUES (?,?,?,?,?,?,?)"
	exec(cins, cID, tID, "user", m.Users[testutil.Owner].ID, "hi", now, nil)
	exec(cins, delComment, tID, "user", m.Users[testutil.Owner].ID, "bye", now, now)
	_ = otherProj

	viewer := env.UserActor(m.Users[testutil.Viewer])
	outsider := env.UserActor(m.Users[testutil.NonMember])
	editor := env.UserActor(m.Users[testutil.Editor])

	for _, ref := range []string{tID, strings.ToLower(tID), "WEB-1", "web-1"} {
		pid, err := env.Svc.ResolveTicketProject(ctx, viewer, ref, service.RoleViewer, false)
		if err != nil || pid != m.Project.ID {
			t.Fatalf("ticket %s: %v %v", ref, pid, err)
		}
	}
	for _, ref := range []string{delTicket, "WEB-2", "WEB-3", "WEB-0", "nonsense"} {
		_, err := env.Svc.ResolveTicketProject(ctx, viewer, ref, service.RoleViewer, false)
		testutil.AssertOutcome(t, err, testutil.NotFound)
	}
	// non-member and nonexistent are identical
	_, e1 := env.Svc.ResolveTicketProject(ctx, outsider, "WEB-1", service.RoleViewer, false)
	_, e2 := env.Svc.ResolveTicketProject(ctx, outsider, "WEB-99", service.RoleViewer, false)
	if e1 == nil || e2 == nil || e1.Error() != e2.Error() {
		t.Fatal("non-member vs missing differ", e1, e2)
	}
	_, err := env.Svc.ResolveTicketProject(ctx, viewer, tID, service.RoleEditor, true)
	testutil.AssertOutcome(t, err, testutil.Forbidden("forbidden"))

	if pid, err := env.Svc.ResolveLabelProject(ctx, editor, lID, service.RoleEditor, true); err != nil || pid != m.Project.ID {
		t.Fatal(pid, err)
	}
	_, err = env.Svc.ResolveLabelProject(ctx, outsider, lID, service.RoleViewer, false)
	testutil.AssertOutcome(t, err, testutil.NotFound)
	_, err = env.Svc.ResolveLabelProject(ctx, editor, "not-an-id", service.RoleViewer, false)
	testutil.AssertOutcome(t, err, testutil.NotFound)

	if pid, err := env.Svc.ResolveCommentProject(ctx, viewer, cID, service.RoleViewer, false); err != nil || pid != m.Project.ID {
		t.Fatal(pid, err)
	}
	_, err = env.Svc.ResolveCommentProject(ctx, viewer, delComment, service.RoleViewer, false)
	testutil.AssertOutcome(t, err, testutil.NotFound)
	_, err = env.Svc.ResolveCommentProject(ctx, outsider, cID, service.RoleViewer, false)
	testutil.AssertOutcome(t, err, testutil.NotFound)

	// a comment on a soft-deleted ticket resolves as not found
	exec("UPDATE tickets SET deleted_at = ? WHERE id = ?", now, tID)
	_, err = env.Svc.ResolveCommentProject(ctx, viewer, cID, service.RoleViewer, false)
	testutil.AssertOutcome(t, err, testutil.NotFound)

	// project-limited token and another project's ticket
	lim := env.TokenActor(env.NewToken(t, m.Users[testutil.Owner].ID, service.ScopeWrite, otherProj.ID))
	exec("UPDATE tickets SET deleted_at = NULL WHERE id = ?", tID)
	_, err = env.Svc.ResolveTicketProject(ctx, lim, "WEB-1", service.RoleViewer, false)
	testutil.AssertOutcome(t, err, testutil.NotFound)
}

func TestUpdateProfile(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "Before")
	actor := env.UserActor(u)

	got, err := env.Svc.Users.UpdateProfile(ctx, actor, "  New Name  ")
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "New Name" || got.ID != u.ID || got.Email != "a@x.io" || got.CreatedAt.IsZero() {
		t.Fatalf("%+v", got)
	}
	if len(env.Pub.Events()) != 0 {
		t.Fatal("profile update must not emit events")
	}
	for name, in := range map[string]string{"empty": "", "blank": "   ", "101 runes": strings.Repeat("é", 101)} {
		_, err := env.Svc.Users.UpdateProfile(ctx, actor, in)
		var se *service.Error
		if !errors.As(err, &se) || se.Kind != service.KindValidation || se.Fields["display_name"] == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := env.Svc.Users.UpdateProfile(ctx, actor, strings.Repeat("é", 100)); err != nil {
		t.Fatalf("100 multibyte runes must be accepted: %v", err)
	}
	// token actors (read or write) get session_required, even with invalid input or a missing user
	for _, scope := range []service.Scope{service.ScopeRead, service.ScopeWrite} {
		tok := env.TokenActor(env.NewToken(t, u.ID, scope, ""))
		_, err := env.Svc.Users.UpdateProfile(ctx, tok, "")
		testutil.AssertOutcome(t, err, testutil.Forbidden("session_required"))
	}
	_, err = env.Svc.Users.UpdateProfile(ctx, service.UserActor("GHOST"), "x")
	testutil.AssertOutcome(t, err, testutil.NotFound)
}

func TestStubsReturnNotImplemented(t *testing.T) {
	env := testutil.NewTestServices(t)
	a := service.UserActor("x")
	if _, err := env.Svc.Projects.Get(ctx, a, "WEB"); err == nil || errors.Is(err, service.ErrNotFound) {
		t.Fatal("stub should return a plain not-implemented error")
	}
}
