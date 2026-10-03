package service_test

import (
	"errors"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// wp7Count runs a COUNT query and returns the number.
func wp7Count(t *testing.T, env *testutil.Env, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Store.RawQueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// wp7Ticket inserts a ticket row directly (tickets service is another work package).
func wp7Ticket(t *testing.T, env *testutil.Env, projectID string, number int, status string, assignee *string, deleted bool) string {
	t.Helper()
	id := env.NewID()
	now := "2026-01-01T00:00:00.000Z"
	var del *string
	if deleted {
		del = &now
	}
	err := env.Store.Exec(ctx, `INSERT INTO tickets (id, project_id, number, title, status, assignee_id, position, deleted_at, created_at, updated_at)
		VALUES (?, ?, ?, 'T', ?, ?, ?, ?, ?, ?)`, id, projectID, number, status, assignee, float64(number)*1024, del, now, now)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func wp7Kind(t *testing.T, err error, kind service.Kind, code string) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) || se.Kind != kind || (code != "" && se.Code != code) {
		t.Fatalf("want kind %d code %q, got %v", kind, code, err)
	}
}

func TestProjectsCreate(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	a := env.UserActor(u)
	p, err := env.Svc.Projects.Create(ctx, a, service.CreateProjectInput{Key: " web ", Name: "  Web Site ", Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Key != "WEB" || p.Name != "Web Site" || p.Description != "d" || p.ArchivedAt != nil || !p.CreatedAt.Equal(env.Clock.Now()) {
		t.Fatalf("project = %+v", p)
	}
	d, err := env.Svc.Projects.Get(ctx, a, "web")
	if err != nil || d.Role != service.RoleOwner || len(d.TicketCounts) != 4 {
		t.Fatalf("get = %+v %v", d, err)
	}
	for _, st := range service.Statuses {
		if d.TicketCounts[st] != 0 {
			t.Fatalf("counts = %v", d.TicketCounts)
		}
	}
	if n := wp7Count(t, env, `SELECT next_ticket_number FROM projects WHERE id = ?`, p.ID); n != 1 {
		t.Fatalf("next_ticket_number = %d", n)
	}
	if len(env.Pub.Events()) != 0 {
		t.Fatal("create must not emit")
	}
	// duplicate key (case-insensitive after normalisation)
	_, err = env.Svc.Projects.Create(ctx, a, service.CreateProjectInput{Key: "Web", Name: "Other"})
	wp7Kind(t, err, service.KindConflict, service.CodeKeyTaken)
	// validation
	for _, in := range []service.CreateProjectInput{{Key: "W", Name: "x"}, {Key: "WEB1", Name: "x"}, {Key: "OK", Name: "  "}} {
		_, err = env.Svc.Projects.Create(ctx, a, in)
		wp7Kind(t, err, service.KindValidation, "")
	}
}

func TestProjectsCreateTokens(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	p := env.NewProject(t, u, "AAA")
	in := service.CreateProjectInput{Key: "NEW", Name: "New"}
	_, err := env.Svc.Projects.Create(ctx, env.TokenActor(env.NewToken(t, u.ID, service.ScopeRead, "")), in)
	wp7Kind(t, err, service.KindForbidden, service.CodeInsufficientScope)
	_, err = env.Svc.Projects.Create(ctx, env.TokenActor(env.NewToken(t, u.ID, service.ScopeWrite, p.ID)), in)
	wp7Kind(t, err, service.KindForbidden, service.CodeForbidden)
	got, err := env.Svc.Projects.Create(ctx, env.TokenActor(env.NewToken(t, u.ID, service.ScopeWrite, "")), in)
	if err != nil || got.Key != "NEW" {
		t.Fatal(got, err)
	}
	// the owner of the new project is the token's owner
	d, err := env.Svc.Projects.Get(ctx, env.UserActor(u), "NEW")
	if err != nil || d.Role != service.RoleOwner {
		t.Fatal(d, err)
	}
}

func TestProjectsGetResolveRefs(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	for _, ref := range []string{"WEB", "web", "Web", m.Project.ID} {
		ref := ref
		m.Run(t, testutil.MatrixCase{
			Name: "get/" + ref,
			Op: func(t *testing.T, a service.Actor) error {
				d, err := env.Svc.Projects.Get(ctx, a, ref)
				if err == nil && d.ID != m.Project.ID {
					t.Fatalf("wrong project %v", d)
				}
				return err
			},
			Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.OK, testutil.Editor: testutil.OK, testutil.Owner: testutil.OK},
		})
		m.Run(t, testutil.MatrixCase{
			Name: "resolve/" + ref,
			Op: func(t *testing.T, a service.Actor) error {
				r, err := env.Svc.Projects.Resolve(ctx, a, ref)
				if err == nil && (r.ID != m.Project.ID || r.Key != "WEB" || r.Role == "") {
					t.Fatalf("ref = %+v", r)
				}
				return err
			},
			Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.OK, testutil.Editor: testutil.OK, testutil.Owner: testutil.OK},
		})
	}
	// unknown and garbage refs look the same as non-member
	_, e1 := env.Svc.Projects.Get(ctx, env.UserActor(testutil.User{ID: m.Users[testutil.Owner].ID}), "NOPE")
	_, e2 := env.Svc.Projects.Get(ctx, env.UserActor(testutil.User{ID: m.Users[testutil.NonMember].ID}), "WEB")
	_, e3 := env.Svc.Projects.Get(ctx, env.UserActor(testutil.User{ID: m.Users[testutil.Owner].ID}), "!!")
	for _, e := range []error{e1, e2, e3} {
		wp7Kind(t, e, service.KindNotFound, service.CodeNotFound)
	}
	if e1.Error() != e2.Error() || e2.Error() != e3.Error() {
		t.Fatal("messages differ")
	}
}

func TestProjectsGetTokenRole(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	p := env.NewProject(t, u, "WEB")
	other := env.NewProject(t, u, "OTH")
	rd := env.TokenActor(env.NewToken(t, u.ID, service.ScopeRead, ""))
	d, err := env.Svc.Projects.Get(ctx, rd, "WEB")
	if err != nil || d.Role != service.RoleViewer {
		t.Fatalf("read token role = %v %v", d.Role, err)
	}
	r, err := env.Svc.Projects.Resolve(ctx, rd, "WEB")
	if err != nil || r.Role != service.RoleViewer {
		t.Fatal(r, err)
	}
	lim := env.TokenActor(env.NewToken(t, u.ID, service.ScopeWrite, p.ID))
	_, err = env.Svc.Projects.Get(ctx, lim, other.ID)
	wp7Kind(t, err, service.KindNotFound, "")
	_, err = env.Svc.Projects.Resolve(ctx, lim, "OTH")
	wp7Kind(t, err, service.KindNotFound, "")
	list, err := env.Svc.Projects.List(ctx, lim, true)
	if err != nil || len(list) != 1 || list[0].ID != p.ID {
		t.Fatal(list, err)
	}
	list, err = env.Svc.Projects.List(ctx, rd, false)
	if err != nil || len(list) != 2 || list[0].Role != service.RoleViewer {
		t.Fatal(list, err)
	}
}

func TestProjectsListOrderCountsArchived(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	o := env.NewUser(t, "o@x.io", "O")
	a := env.UserActor(u)
	mk := func(key, name string) service.Project {
		p, err := env.Svc.Projects.Create(ctx, a, service.CreateProjectInput{Key: key, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	zed := mk("AAA", "zed")
	bob1 := mk("CCC", "Bob")
	bob2 := mk("BBB", "bob")
	alpha := mk("ZZZ", "Alpha")
	_ = zed
	// not a member of this one
	env.NewProject(t, o, "OUT")
	wp7Ticket(t, env, alpha.ID, 1, "todo", nil, false)
	wp7Ticket(t, env, alpha.ID, 2, "todo", nil, false)
	wp7Ticket(t, env, alpha.ID, 3, "done", nil, true) // soft-deleted: not counted
	wp7Ticket(t, env, alpha.ID, 4, "in_progress", nil, false)
	if _, err := env.Svc.Projects.Update(ctx, a, bob1.ID, service.UpdateProjectInput{Archived: service.Some(true)}); err != nil {
		t.Fatal(err)
	}
	list, err := env.Svc.Projects.List(ctx, a, false)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range list {
		keys = append(keys, p.Key)
	}
	// lower(name) then key: Alpha, bob(BBB), zed(AAA); archived Bob(CCC) hidden
	if len(keys) != 3 || keys[0] != "ZZZ" || keys[1] != bob2.Key || keys[2] != "AAA" {
		t.Fatalf("order = %v", keys)
	}
	if c := list[0].TicketCounts; len(c) != 4 || c[service.StatusTodo] != 2 || c[service.StatusInProgress] != 1 || c[service.StatusDone] != 0 || c[service.StatusBacklog] != 0 {
		t.Fatalf("counts = %v", c)
	}
	if list[0].Role != service.RoleOwner {
		t.Fatalf("role = %v", list[0].Role)
	}
	list, err = env.Svc.Projects.List(ctx, a, true)
	if err != nil || len(list) != 4 {
		t.Fatal(list, err)
	}
	// same name: key breaks the tie (BBB before CCC)
	if list[1].Key != "BBB" || list[2].Key != "CCC" {
		t.Fatalf("tie order = %s %s", list[1].Key, list[2].Key)
	}
	d, err := env.Svc.Projects.Get(ctx, a, "ZZZ")
	if err != nil || d.TicketCounts[service.StatusTodo] != 2 || d.TicketCounts[service.StatusDone] != 0 {
		t.Fatal(d, err)
	}
	empty, err := env.Svc.Projects.List(ctx, env.UserActor(env.NewUser(t, "n@x.io", "N")), false)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty list = %#v %v", empty, err)
	}
}

func TestProjectsUpdateMatrixAndNoop(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	m.Run(t, testutil.MatrixCase{
		Name: "update",
		Op: func(t *testing.T, a service.Actor) error {
			_, err := env.Svc.Projects.Update(ctx, a, "WEB", service.UpdateProjectInput{Description: service.Some("x")})
			return err
		},
		Want:      map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden("forbidden"), testutil.Editor: testutil.Forbidden("forbidden"), testutil.Owner: testutil.OK},
		TokenWant: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden("forbidden"), testutil.Editor: testutil.Forbidden("forbidden"), testutil.Owner: testutil.OK},
	})
	owner := env.UserActor(testutil.User{ID: m.Users[testutil.Owner].ID})
	// read token owner: insufficient_scope
	rd := env.TokenActor(env.NewToken(t, m.Users[testutil.Owner].ID, service.ScopeRead, ""))
	_, err := env.Svc.Projects.Update(ctx, rd, "WEB", service.UpdateProjectInput{Name: service.Some("zz")})
	wp7Kind(t, err, service.KindForbidden, service.CodeInsufficientScope)

	env.Pub.Reset()
	env.Clock.Advance(3600e9)
	before, _ := env.Svc.Projects.Get(ctx, owner, "WEB")
	same := service.UpdateProjectInput{Name: service.Some(" " + before.Name + " "), Description: service.Some(before.Description), Archived: service.Some(false)}
	for _, in := range []service.UpdateProjectInput{{}, same} {
		got, err := env.Svc.Projects.Update(ctx, owner, "WEB", in)
		if err != nil || !got.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("noop: %+v %v", got, err)
		}
	}
	if len(env.Pub.Events()) != 0 {
		t.Fatal("no-op emitted")
	}
	got, err := env.Svc.Projects.Update(ctx, owner, "WEB", service.UpdateProjectInput{Name: service.Some("Renamed"), Archived: service.Some(true)})
	if err != nil || got.Name != "Renamed" || got.ArchivedAt == nil || !got.UpdatedAt.Equal(env.Clock.Now()) {
		t.Fatal(got, err)
	}
	ev := env.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventProjectUpdated || ev[0].ProjectID != m.Project.ID || ev[0].Actor.ID != owner.ID {
		t.Fatalf("events = %+v", ev)
	}
	// archiving an archived project is a no-op; owners may edit an archived project and unarchive
	env.Pub.Reset()
	if _, err := env.Svc.Projects.Update(ctx, owner, "WEB", service.UpdateProjectInput{Archived: service.Some(true)}); err != nil || len(env.Pub.Events()) != 0 {
		t.Fatal(err, env.Pub.Events())
	}
	if _, err := env.Svc.Projects.Update(ctx, owner, "WEB", service.UpdateProjectInput{Description: service.Some("while archived")}); err != nil {
		t.Fatal(err)
	}
	got, err = env.Svc.Projects.Update(ctx, owner, "WEB", service.UpdateProjectInput{Archived: service.Some(false)})
	if err != nil || got.ArchivedAt != nil || len(env.Pub.Events()) != 2 {
		t.Fatal(got, err, env.Pub.Events())
	}
	// validation last; nulls rejected; non-members never see field errors
	_, err = env.Svc.Projects.Update(ctx, owner, "WEB", service.UpdateProjectInput{Name: service.Some("  ")})
	wp7Kind(t, err, service.KindValidation, "")
	_, err = env.Svc.Projects.Update(ctx, owner, "WEB", service.UpdateProjectInput{Name: service.Null[string]()})
	wp7Kind(t, err, service.KindValidation, "")
	out := env.UserActor(testutil.User{ID: m.Users[testutil.NonMember].ID})
	_, err = env.Svc.Projects.Update(ctx, out, "WEB", service.UpdateProjectInput{Name: service.Some("")})
	wp7Kind(t, err, service.KindNotFound, "")
	vw := env.UserActor(testutil.User{ID: m.Users[testutil.Viewer].ID})
	_, err = env.Svc.Projects.Update(ctx, vw, "WEB", service.UpdateProjectInput{Name: service.Some("")})
	wp7Kind(t, err, service.KindForbidden, "forbidden")
}

func TestProjectsDelete(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	m.Run(t, testutil.MatrixCase{
		Name:        "delete",
		SessionOnly: true,
		Before: func(t *testing.T) {
			// recreate the project when a previous run deleted it
			if n := wp7Count(t, env, `SELECT COUNT(*) FROM projects WHERE key = 'WEB'`); n == 0 {
				m.Project = env.NewProject(t, testutil.User{ID: m.Users[testutil.Owner].ID}, "WEB")
				env.AddMember(t, m.Project.ID, m.Users[testutil.Editor].ID, service.RoleEditor)
				env.AddMember(t, m.Project.ID, m.Users[testutil.Viewer].ID, service.RoleViewer)
			}
		},
		Op:   func(t *testing.T, a service.Actor) error { return env.Svc.Projects.Delete(ctx, a, "WEB") },
		Want: map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden("forbidden"), testutil.Editor: testutil.Forbidden("forbidden"), testutil.Owner: testutil.OK},
	})
}

func TestProjectsDeleteEventsAndStreams(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	ed := env.UserActor(testutil.User{ID: m.Users[testutil.Editor].ID})
	if err := env.Svc.Projects.Delete(ctx, ed, "WEB"); err == nil {
		t.Fatal("editor deleted")
	}
	if len(env.Streams.Calls()) != 0 {
		t.Fatal("stream closed on failure")
	}
	owner := env.UserActor(testutil.User{ID: m.Users[testutil.Owner].ID})
	if err := env.Svc.Projects.Delete(ctx, owner, "WEB"); err != nil {
		t.Fatal(err)
	}
	calls := env.Streams.Calls()
	if len(calls) != 1 || calls[0] != (testutil.StreamCall{ProjectID: m.Project.ID, Reason: service.CloseProject}) {
		t.Fatalf("calls = %+v", calls)
	}
	if len(env.Pub.Events()) != 0 {
		t.Fatal("delete must not publish")
	}
	_, err := env.Svc.Projects.Get(ctx, owner, "WEB")
	wp7Kind(t, err, service.KindNotFound, "")
}

func TestProjectsDeleteCascadeComplete(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	keep := env.NewProject(t, testutil.User{ID: m.Users[testutil.Owner].ID}, "KEP")
	ownerID := m.Users[testutil.Owner].ID
	seed := func(pid string) {
		t1 := wp7Ticket(t, env, pid, 1, "todo", &ownerID, false)
		t2 := wp7Ticket(t, env, pid, 2, "done", nil, true)
		label := env.NewID()
		now := "2026-01-01T00:00:00.000Z"
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(env.Store.Exec(ctx, `INSERT INTO labels (id, project_id, name, color) VALUES (?, ?, 'bug', 'red')`, label, pid))
		must(env.Store.Exec(ctx, `INSERT INTO ticket_labels (ticket_id, label_id) VALUES (?, ?)`, t1, label))
		must(env.Store.Exec(ctx, `INSERT INTO comments (id, ticket_id, author_type, author_id, body, created_at) VALUES (?, ?, 'user', ?, 'hi', ?)`, env.NewID(), t1, ownerID, now))
		must(env.Store.Exec(ctx, `INSERT INTO comments (id, ticket_id, author_type, author_id, body, created_at) VALUES (?, ?, 'user', ?, 'hi', ?)`, env.NewID(), t2, ownerID, now))
		must(env.Store.Exec(ctx, `INSERT INTO ticket_activity (id, ticket_id, actor_type, actor_id, action, created_at) VALUES (?, ?, 'user', ?, 'created', ?)`, env.NewID(), t1, ownerID, now))
	}
	seed(m.Project.ID)
	seed(keep.ID)
	env.NewToken(t, ownerID, service.ScopeWrite, m.Project.ID) // project-limited token
	env.NewToken(t, ownerID, service.ScopeWrite, keep.ID)

	if err := env.Svc.Projects.Delete(ctx, env.UserActor(testutil.User{ID: ownerID}), "WEB"); err != nil {
		t.Fatal(err)
	}
	pid := m.Project.ID
	for _, q := range []string{
		`SELECT COUNT(*) FROM projects WHERE id = ?`,
		`SELECT COUNT(*) FROM project_members WHERE project_id = ?`,
		`SELECT COUNT(*) FROM labels WHERE project_id = ?`,
		`SELECT COUNT(*) FROM tickets WHERE project_id = ?`,
		`SELECT COUNT(*) FROM api_tokens WHERE project_id = ?`,
		`SELECT COUNT(*) FROM ticket_labels WHERE ticket_id IN (SELECT id FROM tickets WHERE project_id = ?)`,
	} {
		if n := wp7Count(t, env, q, pid); n != 0 {
			t.Fatalf("%s left %d rows", q, n)
		}
	}
	// orphan checks: nothing refers to a missing ticket or label
	for _, q := range []string{
		`SELECT COUNT(*) FROM comments WHERE ticket_id NOT IN (SELECT id FROM tickets)`,
		`SELECT COUNT(*) FROM ticket_activity WHERE ticket_id NOT IN (SELECT id FROM tickets)`,
		`SELECT COUNT(*) FROM ticket_labels WHERE ticket_id NOT IN (SELECT id FROM tickets) OR label_id NOT IN (SELECT id FROM labels)`,
	} {
		if n := wp7Count(t, env, q); n != 0 {
			t.Fatalf("%s: %d orphans", q, n)
		}
	}
	// the other project is untouched
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM tickets WHERE project_id = ?`:                                              2,
		`SELECT COUNT(*) FROM labels WHERE project_id = ?`:                                               1,
		`SELECT COUNT(*) FROM api_tokens WHERE project_id = ?`:                                           1,
		`SELECT COUNT(*) FROM project_members WHERE project_id = ?`:                                      1,
		`SELECT COUNT(*) FROM comments WHERE ticket_id IN (SELECT id FROM tickets WHERE project_id = ?)`: 2,
	} {
		if n := wp7Count(t, env, q, keep.ID); n != want {
			t.Fatalf("%s = %d, want %d", q, n, want)
		}
	}
}

func TestProjectsArchivedStillAllowsOwnerOps(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	env.Archive(t, m.Project.ID)
	owner := env.UserActor(testutil.User{ID: m.Users[testutil.Owner].ID})
	if _, err := env.Svc.Projects.Get(ctx, owner, "WEB"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.Members.Add(ctx, owner, "WEB", m.Users[testutil.NonMember].Email, service.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := env.Svc.Projects.Delete(ctx, owner, "WEB"); err != nil {
		t.Fatal(err)
	}
}
