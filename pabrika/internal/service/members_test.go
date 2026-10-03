package service_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func wp7U(m *testutil.Matrix, w testutil.Who) service.Actor {
	return service.UserActor(m.Users[w].ID)
}

func TestMembersAddMatrix(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	target := env.NewUser(t, "new@x.io", "New")
	m.Run(t, testutil.MatrixCase{
		Name:        "add",
		SessionOnly: true,
		Before: func(t *testing.T) {
			_ = env.Store.Exec(ctx, `DELETE FROM project_members WHERE user_id = ?`, target.ID)
		},
		Op: func(t *testing.T, a service.Actor) error {
			_, err := env.Svc.Members.Add(ctx, a, "WEB", target.Email, service.RoleEditor)
			return err
		},
		Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden("forbidden"), testutil.Editor: testutil.Forbidden("forbidden"), testutil.Owner: testutil.OK},
	})
}

func TestMembersAddBehavior(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	owner := wp7U(m, testutil.Owner)
	nu := env.NewUser(t, "Mixed.Case@x.io", "Mixed")
	env.Pub.Reset()
	mem, err := env.Svc.Members.Add(ctx, owner, "web", " mixed.case@X.IO ", service.RoleViewer)
	if err != nil || mem.UserID != nu.ID || mem.Role != service.RoleViewer || mem.DisplayName != "Mixed" || mem.Email != nu.Email {
		t.Fatal(mem, err)
	}
	ev := env.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventMemberChanged || ev[0].UserID != nu.ID || ev[0].ProjectID != m.Project.ID {
		t.Fatalf("events = %+v", ev)
	}
	// the new member can now read
	if _, err := env.Svc.Projects.Get(ctx, env.UserActor(nu), "WEB"); err != nil {
		t.Fatal(err)
	}
	_, err = env.Svc.Members.Add(ctx, owner, "WEB", "mixed.case@x.io", service.RoleEditor)
	wp7Kind(t, err, service.KindConflict, service.CodeAlreadyMember)
	_, err = env.Svc.Members.Add(ctx, owner, "WEB", "ghost@x.io", service.RoleEditor)
	wp7Kind(t, err, service.KindValidation, service.CodeUserNotFound)
	if se := err.(*service.Error); se.Fields["email"] != "No account with this email" {
		t.Fatalf("fields = %v", se.Fields)
	}
	other := env.NewUser(t, "other@x.io", "Other")
	_, err = env.Svc.Members.Add(ctx, owner, "WEB", other.Email, service.Role("boss"))
	wp7Kind(t, err, service.KindValidation, "")
	if n := wp7Count(t, env, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ?`, m.Project.ID, other.ID); n != 0 {
		t.Fatal("member added despite invalid role")
	}
	// validation comes after the 404/403 checks
	_, err = env.Svc.Members.Add(ctx, wp7U(m, testutil.NonMember), "WEB", "ghost@x.io", service.Role("boss"))
	wp7Kind(t, err, service.KindNotFound, "")
	_, err = env.Svc.Members.Add(ctx, wp7U(m, testutil.Viewer), "WEB", "ghost@x.io", service.RoleEditor)
	wp7Kind(t, err, service.KindForbidden, "forbidden")
}

func TestMembersSessionOnlyBeforeLookup(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	for _, sc := range []service.Scope{service.ScopeRead, service.ScopeWrite} {
		tok := env.TokenActor(env.NewToken(t, m.Users[testutil.Owner].ID, sc, ""))
		_, err := env.Svc.Members.Add(ctx, tok, "NOPE", "", service.Role("x"))
		wp7Kind(t, err, service.KindForbidden, service.CodeSessionRequired)
		_, err = env.Svc.Members.SetRole(ctx, tok, "NOPE", "bad", service.Role("x"))
		wp7Kind(t, err, service.KindForbidden, service.CodeSessionRequired)
		err = env.Svc.Members.Remove(ctx, tok, "NOPE", "bad")
		wp7Kind(t, err, service.KindForbidden, service.CodeSessionRequired)
	}
	// List is open to tokens (read scope included)
	rd := env.TokenActor(env.NewToken(t, m.Users[testutil.Viewer].ID, service.ScopeRead, ""))
	if _, err := env.Svc.Members.List(ctx, rd, "WEB"); err != nil {
		t.Fatal(err)
	}
}

func TestMembersListOrderAndMatrix(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	m.Run(t, testutil.MatrixCase{
		Name: "list",
		Op: func(t *testing.T, a service.Actor) error {
			_, err := env.Svc.Members.List(ctx, a, "WEB")
			return err
		},
		Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.OK, testutil.Editor: testutil.OK, testutil.Owner: testutil.OK},
	})
	// add a second owner whose name sorts last, plus same-name members
	zed := env.NewUser(t, "zed@x.io", "zed")
	env.AddMember(t, m.Project.ID, zed.ID, service.RoleOwner)
	aaa := env.NewUser(t, "aaa@x.io", "aaa")
	env.AddMember(t, m.Project.ID, aaa.ID, service.RoleViewer)
	list, err := env.Svc.Members.List(ctx, wp7U(m, testutil.Viewer), "WEB")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range list {
		got = append(got, string(x.Role)+":"+x.DisplayName)
	}
	want := []string{"owner:Owner WEB", "owner:zed", "viewer:aaa", "editor:Editor WEB", "viewer:Viewer WEB"}
	// non-owners ordered by display name, case-insensitive
	want = []string{"owner:Owner WEB", "owner:zed", "viewer:aaa", "editor:Editor WEB", "viewer:Viewer WEB"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v", got)
	}
	if list[0].Email == "" || list[0].CreatedAt.IsZero() {
		t.Fatalf("member = %+v", list[0])
	}
}

func TestMembersSetRole(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	owner, ownerID := wp7U(m, testutil.Owner), m.Users[testutil.Owner].ID
	edID := m.Users[testutil.Editor].ID
	m.Run(t, testutil.MatrixCase{
		Name:        "setrole",
		SessionOnly: true,
		Op: func(t *testing.T, a service.Actor) error {
			_, err := env.Svc.Members.SetRole(ctx, a, "WEB", m.Users[testutil.Viewer].ID, service.RoleViewer) // same role
			return err
		},
		Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden("forbidden"), testutil.Editor: testutil.Forbidden("forbidden"), testutil.Owner: testutil.OK},
	})
	env.Pub.Reset()
	got, err := env.Svc.Members.SetRole(ctx, owner, "WEB", edID, service.RoleEditor)
	if err != nil || got.Role != service.RoleEditor || len(env.Pub.Events()) != 0 {
		t.Fatal("same role must be a silent no-op", got, err, env.Pub.Events())
	}
	got, err = env.Svc.Members.SetRole(ctx, owner, "WEB", strings.ToLower(edID), service.RoleViewer)
	if err != nil || got.Role != service.RoleViewer || got.UserID != edID {
		t.Fatal(got, err)
	}
	ev := env.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventMemberChanged || ev[0].UserID != edID {
		t.Fatalf("events = %+v", ev)
	}
	// target must be a member; invalid role
	_, err = env.Svc.Members.SetRole(ctx, owner, "WEB", m.Users[testutil.NonMember].ID, service.RoleViewer)
	wp7Kind(t, err, service.KindNotFound, "")
	_, err = env.Svc.Members.SetRole(ctx, owner, "WEB", "garbage", service.RoleViewer)
	wp7Kind(t, err, service.KindNotFound, "")
	_, err = env.Svc.Members.SetRole(ctx, owner, "WEB", edID, service.Role("boss"))
	wp7Kind(t, err, service.KindValidation, "")

	// last owner protection
	env.Pub.Reset()
	_, err = env.Svc.Members.SetRole(ctx, owner, "WEB", ownerID, service.RoleEditor)
	wp7Kind(t, err, service.KindConflict, service.CodeLastOwner)
	if len(env.Pub.Events()) != 0 {
		t.Fatal("event on failure")
	}
	// promote a second owner, then the first may step down; the second cannot
	if _, err := env.Svc.Members.SetRole(ctx, owner, "WEB", edID, service.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.Members.SetRole(ctx, owner, "WEB", ownerID, service.RoleEditor); err != nil {
		t.Fatal(err)
	}
	second := service.UserActor(edID)
	_, err = env.Svc.Members.SetRole(ctx, second, "WEB", edID, service.RoleViewer)
	wp7Kind(t, err, service.KindConflict, service.CodeLastOwner)
	// the demoted ex-owner is no longer allowed to manage
	_, err = env.Svc.Members.SetRole(ctx, owner, "WEB", edID, service.RoleViewer)
	wp7Kind(t, err, service.KindForbidden, "forbidden")
}

func TestMembersConcurrentDemotion(t *testing.T) {
	env := testutil.NewFileEnv(t)
	a := env.NewUser(t, "a@x.io", "A")
	b := env.NewUser(t, "b@x.io", "B")
	p := env.NewProject(t, a, "WEB")
	env.AddMember(t, p.ID, b.ID, service.RoleOwner)

	run := func(fn1, fn2 func() error) (e1, e2 error) {
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; e1 = fn1() }()
		go func() { defer wg.Done(); <-start; e2 = fn2() }()
		close(start)
		wg.Wait()
		return
	}
	setRole := func(by, target testutil.User) func() error {
		return func() error {
			_, err := env.Svc.Members.SetRole(ctx, env.UserActor(by), "WEB", target.ID, service.RoleViewer)
			return err
		}
	}
	// each owner demotes themselves: exactly one wins, the other gets last_owner
	e1, e2 := run(setRole(a, a), setRole(b, b))
	if (e1 == nil) == (e2 == nil) {
		t.Fatalf("exactly one must succeed: %v / %v", e1, e2)
	}
	loser := e1
	if loser == nil {
		loser = e2
	}
	wp7Kind(t, loser, service.KindConflict, service.CodeLastOwner)
	if n := wp7Count(t, env, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND role = 'owner'`, p.ID); n != 1 {
		t.Fatalf("owners = %d", n)
	}

	// owners demote each other: never both succeed, one owner always remains
	env2 := testutil.NewFileEnv(t)
	c := env2.NewUser(t, "c@x.io", "C")
	d := env2.NewUser(t, "d@x.io", "D")
	p2 := env2.NewProject(t, c, "WEB")
	env2.AddMember(t, p2.ID, d.ID, service.RoleOwner)
	cross := func(by, target testutil.User) func() error {
		return func() error {
			_, err := env2.Svc.Members.SetRole(ctx, env2.UserActor(by), "WEB", target.ID, service.RoleViewer)
			return err
		}
	}
	e1, e2 = run(cross(c, d), cross(d, c))
	if (e1 == nil) == (e2 == nil) {
		t.Fatalf("exactly one must succeed: %v / %v", e1, e2)
	}
	if n := wp7Count(t, env2, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND role = 'owner'`, p2.ID); n != 1 {
		t.Fatalf("owners = %d", n)
	}
	// the loser is refused either as last_owner or because it was demoted first (forbidden)
	loser = e1
	if loser == nil {
		loser = e2
	}
	if se, ok := loser.(*service.Error); !ok || (se.Code != service.CodeLastOwner && se.Code != service.CodeForbidden) {
		t.Fatalf("loser = %v", loser)
	}
}

func TestMembersRemoveAndLeave(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	owner, ownerID := wp7U(m, testutil.Owner), m.Users[testutil.Owner].ID
	ed, vw := wp7U(m, testutil.Editor), wp7U(m, testutil.Viewer)
	edID, vwID := m.Users[testutil.Editor].ID, m.Users[testutil.Viewer].ID

	// editor/viewer cannot remove others; non-member 404 (also for tokens: session_required first)
	err := env.Svc.Members.Remove(ctx, ed, "WEB", vwID)
	wp7Kind(t, err, service.KindForbidden, "forbidden")
	err = env.Svc.Members.Remove(ctx, vw, "WEB", ownerID)
	wp7Kind(t, err, service.KindForbidden, "forbidden")
	err = env.Svc.Members.Remove(ctx, wp7U(m, testutil.NonMember), "WEB", vwID)
	wp7Kind(t, err, service.KindNotFound, "")
	err = env.Svc.Members.Remove(ctx, wp7U(m, testutil.NonMember), "WEB", m.Users[testutil.NonMember].ID)
	wp7Kind(t, err, service.KindNotFound, "")
	// owner removing a non-member
	err = env.Svc.Members.Remove(ctx, owner, "WEB", m.Users[testutil.NonMember].ID)
	wp7Kind(t, err, service.KindNotFound, "")
	// last owner cannot leave or remove self, and nothing is published or closed
	env.Pub.Reset()
	env.Streams.Reset()
	err = env.Svc.Members.Remove(ctx, owner, "WEB", ownerID)
	wp7Kind(t, err, service.KindConflict, service.CodeLastOwner)
	if len(env.Pub.Events()) != 0 || len(env.Streams.Calls()) != 0 {
		t.Fatal("published on failure")
	}

	// editor leaves; viewer leaves after the project is archived (still allowed)
	if err := env.Svc.Members.Remove(ctx, ed, "WEB", edID); err != nil {
		t.Fatal(err)
	}
	ev := env.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventMemberChanged || ev[0].UserID != edID || ev[0].ProjectID != m.Project.ID || ev[0].Actor.ID != edID {
		t.Fatalf("events = %+v", ev)
	}
	calls := env.Streams.Calls()
	if len(calls) != 1 || calls[0] != (testutil.StreamCall{ProjectID: m.Project.ID, UserID: edID, Reason: service.CloseRemoved}) {
		t.Fatalf("calls = %+v", calls)
	}
	_, err = env.Svc.Projects.Get(ctx, ed, "WEB")
	wp7Kind(t, err, service.KindNotFound, "")
	env.Archive(t, m.Project.ID)
	if err := env.Svc.Members.Remove(ctx, vw, "WEB", vwID); err != nil {
		t.Fatal(err)
	}
	// owner can remove anyone; a second owner may leave
	env.AddMember(t, m.Project.ID, edID, service.RoleOwner)
	if err := env.Svc.Members.Remove(ctx, owner, "WEB", edID); err != nil {
		t.Fatal(err)
	}
	env.AddMember(t, m.Project.ID, edID, service.RoleOwner)
	if err := env.Svc.Members.Remove(ctx, ed, "WEB", edID); err != nil {
		t.Fatal(err)
	}
}

func TestMembersRemoveUnassignsTickets(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	other := env.NewProject(t, testutil.User{ID: m.Users[testutil.Owner].ID}, "OTH")
	env.AddMember(t, other.ID, m.Users[testutil.Editor].ID, service.RoleEditor)
	edID := m.Users[testutil.Editor].ID
	ownerID := m.Users[testutil.Owner].ID
	live1 := wp7Ticket(t, env, m.Project.ID, 1, "todo", &edID, false)
	live2 := wp7Ticket(t, env, m.Project.ID, 2, "done", &edID, false)
	dead := wp7Ticket(t, env, m.Project.ID, 3, "todo", &edID, true)
	mine := wp7Ticket(t, env, m.Project.ID, 4, "todo", &ownerID, false)
	elsewhere := wp7Ticket(t, env, other.ID, 1, "todo", &edID, false)

	env.Pub.Reset()
	if err := env.Svc.Members.Remove(ctx, wp7U(m, testutil.Owner), "WEB", edID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{live1, live2, dead} {
		var a *string
		if err := env.Store.RawQueryRow(ctx, `SELECT assignee_id FROM tickets WHERE id = ?`, id).Scan(&a); err != nil || a != nil {
			t.Fatalf("ticket %s assignee = %v (%v)", id, a, err)
		}
	}
	var a *string
	_ = env.Store.RawQueryRow(ctx, `SELECT assignee_id FROM tickets WHERE id = ?`, mine).Scan(&a)
	if a == nil || *a != ownerID {
		t.Fatal("other assignee touched")
	}
	_ = env.Store.RawQueryRow(ctx, `SELECT assignee_id FROM tickets WHERE id = ?`, elsewhere).Scan(&a)
	if a == nil || *a != edID {
		t.Fatal("ticket in another project touched")
	}
	// activity: only live tickets, actor = remover
	if n := wp7Count(t, env, `SELECT COUNT(*) FROM ticket_activity`); n != 2 {
		t.Fatalf("activity rows = %d", n)
	}
	for _, id := range []string{live1, live2} {
		var typ, actor, action, changes string
		err := env.Store.RawQueryRow(ctx, `SELECT actor_type, actor_id, action, changes FROM ticket_activity WHERE ticket_id = ?`, id).Scan(&typ, &actor, &action, &changes)
		if err != nil || typ != "user" || actor != ownerID || action != "assigned" || changes != `{"assignee":["`+edID+`",null]}` {
			t.Fatalf("activity = %s %s %s %s (%v)", typ, actor, action, changes, err)
		}
	}
	if n := wp7Count(t, env, `SELECT COUNT(*) FROM ticket_activity WHERE ticket_id = ?`, dead); n != 0 {
		t.Fatal("activity written for soft-deleted ticket")
	}
	// only member.changed, no ticket events
	ev := env.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventMemberChanged {
		t.Fatalf("events = %+v", ev)
	}
	_, err := env.Svc.Projects.Get(ctx, wp7U(m, testutil.Editor), "WEB")
	wp7Kind(t, err, service.KindNotFound, "")
}
