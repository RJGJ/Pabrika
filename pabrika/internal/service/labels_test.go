package service_test

import (
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func TestLabelsMatrix(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	seed, err := env.Svc.Labels.Create(ctx, wp7U(m, testutil.Owner), "WEB", service.LabelInput{Name: "seed"})
	if err != nil {
		t.Fatal(err)
	}
	read := map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.OK, testutil.Editor: testutil.OK, testutil.Owner: testutil.OK}
	write := map[testutil.Who]testutil.Outcome{testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden("forbidden"), testutil.Editor: testutil.OK, testutil.Owner: testutil.OK}
	n := 0
	m.Run(t, testutil.MatrixCase{Name: "list", Want: read, Op: func(t *testing.T, a service.Actor) error {
		_, err := env.Svc.Labels.List(ctx, a, "WEB")
		return err
	}})
	m.Run(t, testutil.MatrixCase{Name: "create", Want: write, Op: func(t *testing.T, a service.Actor) error {
		n++
		_, err := env.Svc.Labels.Create(ctx, a, "web", service.LabelInput{Name: string(rune('a'+n)) + "-label"})
		return err
	}})
	m.Run(t, testutil.MatrixCase{Name: "update", Want: write, Op: func(t *testing.T, a service.Actor) error {
		n++
		_, err := env.Svc.Labels.Update(ctx, a, seed.ID, service.UpdateLabelInput{Color: service.Some(service.LabelColor("red"))})
		return err
	}})
	m.Run(t, testutil.MatrixCase{Name: "delete", Want: write,
		Before: func(t *testing.T) {
			if wp7Count(t, env, `SELECT COUNT(*) FROM labels WHERE id = ?`, seed.ID) == 0 {
				l, err := env.Svc.Labels.Create(ctx, wp7U(m, testutil.Owner), "WEB", service.LabelInput{Name: "seed"})
				if err != nil {
					t.Fatal(err)
				}
				seed = l
			}
		},
		Op: func(t *testing.T, a service.Actor) error { return env.Svc.Labels.Delete(ctx, a, seed.ID) }})
}

func TestLabelsReadTokenAndLimits(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	p := env.NewProject(t, u, "WEB")
	other := env.NewProject(t, u, "OTH")
	owner := env.UserActor(u)
	l, err := env.Svc.Labels.Create(ctx, owner, "WEB", service.LabelInput{Name: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	rd := env.TokenActor(env.NewToken(t, u.ID, service.ScopeRead, ""))
	if _, err := env.Svc.Labels.List(ctx, rd, "WEB"); err != nil {
		t.Fatal(err)
	}
	_, err = env.Svc.Labels.Create(ctx, rd, "WEB", service.LabelInput{Name: "x"})
	wp7Kind(t, err, service.KindForbidden, service.CodeInsufficientScope)
	_, err = env.Svc.Labels.Update(ctx, rd, l.ID, service.UpdateLabelInput{Name: service.Some("y")})
	wp7Kind(t, err, service.KindForbidden, service.CodeInsufficientScope)
	err = env.Svc.Labels.Delete(ctx, rd, l.ID)
	wp7Kind(t, err, service.KindForbidden, service.CodeInsufficientScope)
	lim := env.TokenActor(env.NewToken(t, u.ID, service.ScopeWrite, p.ID))
	_, err = env.Svc.Labels.List(ctx, lim, "OTH")
	wp7Kind(t, err, service.KindNotFound, "")
	ol, err := env.Svc.Labels.Create(ctx, owner, other.ID, service.LabelInput{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	err = env.Svc.Labels.Delete(ctx, lim, ol.ID)
	wp7Kind(t, err, service.KindNotFound, "")
	// unknown / garbage ids
	err = env.Svc.Labels.Delete(ctx, owner, "nope")
	wp7Kind(t, err, service.KindNotFound, "")
	err = env.Svc.Labels.Delete(ctx, owner, env.NewID())
	wp7Kind(t, err, service.KindNotFound, "")
}

func TestLabelsBehavior(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	p := env.NewProject(t, u, "WEB")
	env.NewProject(t, u, "OTH")
	a := env.UserActor(u)

	l, err := env.Svc.Labels.Create(ctx, a, "WEB", service.LabelInput{Name: "  Bug  "})
	if err != nil || l.Name != "Bug" || l.Color != "gray" || l.ProjectID != p.ID {
		t.Fatal(l, err)
	}
	ev := env.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventLabelChanged || ev[0].LabelID != l.ID || ev[0].ProjectID != p.ID {
		t.Fatalf("events = %+v", ev)
	}
	_, err = env.Svc.Labels.Create(ctx, a, "WEB", service.LabelInput{Name: "bUG", Color: "red"})
	wp7Kind(t, err, service.KindConflict, service.CodeLabelExists)
	// same name in another project is fine
	if _, err := env.Svc.Labels.Create(ctx, a, "OTH", service.LabelInput{Name: "bug"}); err != nil {
		t.Fatal(err)
	}
	for _, in := range []service.LabelInput{{Name: " "}, {Name: "x", Color: "mauve"}, {Name: string(make([]byte, 0)) + "0123456789012345678901234567890123456789012345678901"}} {
		_, err = env.Svc.Labels.Create(ctx, a, "WEB", in)
		wp7Kind(t, err, service.KindValidation, "")
	}
	feat, err := env.Svc.Labels.Create(ctx, a, "WEB", service.LabelInput{Name: "feature", Color: "teal"})
	if err != nil || feat.Color != "teal" {
		t.Fatal(feat, err)
	}
	// list in name order, case-insensitive
	if _, err := env.Svc.Labels.Create(ctx, a, "WEB", service.LabelInput{Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	list, err := env.Svc.Labels.List(ctx, a, "WEB")
	if err != nil || len(list) != 3 || list[0].Name != "alpha" || list[1].Name != "Bug" || list[2].Name != "feature" {
		t.Fatal(list, err)
	}
	// update: casing change of own name allowed, conflicts with another, no-op silent
	env.Pub.Reset()
	got, err := env.Svc.Labels.Update(ctx, a, l.ID, service.UpdateLabelInput{Name: service.Some("BUG")})
	if err != nil || got.Name != "BUG" || len(env.Pub.Events()) != 1 {
		t.Fatal(got, err, env.Pub.Events())
	}
	env.Pub.Reset()
	for _, in := range []service.UpdateLabelInput{{}, {Name: service.Some(" BUG ")}, {Color: service.Some(service.LabelColor("gray"))}} {
		if _, err := env.Svc.Labels.Update(ctx, a, l.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	if len(env.Pub.Events()) != 0 {
		t.Fatal("no-op emitted")
	}
	_, err = env.Svc.Labels.Update(ctx, a, l.ID, service.UpdateLabelInput{Name: service.Some("Feature")})
	wp7Kind(t, err, service.KindConflict, service.CodeLabelExists)
	_, err = env.Svc.Labels.Update(ctx, a, l.ID, service.UpdateLabelInput{Color: service.Some(service.LabelColor("mauve"))})
	wp7Kind(t, err, service.KindValidation, "")
	_, err = env.Svc.Labels.Update(ctx, a, l.ID, service.UpdateLabelInput{Name: service.Null[string]()})
	wp7Kind(t, err, service.KindValidation, "")

	// delete cascades ticket_labels and writes no activity or ticket events
	tk := wp7Ticket(t, env, p.ID, 1, "todo", nil, false)
	if err := env.Store.Exec(ctx, `INSERT INTO ticket_labels (ticket_id, label_id) VALUES (?, ?)`, tk, l.ID); err != nil {
		t.Fatal(err)
	}
	env.Pub.Reset()
	if err := env.Svc.Labels.Delete(ctx, a, l.ID); err != nil {
		t.Fatal(err)
	}
	if n := wp7Count(t, env, `SELECT COUNT(*) FROM ticket_labels WHERE label_id = ?`, l.ID); n != 0 {
		t.Fatal("ticket_labels left")
	}
	if n := wp7Count(t, env, `SELECT COUNT(*) FROM ticket_activity`); n != 0 {
		t.Fatal("activity written")
	}
	ev = env.Pub.Events()
	if len(ev) != 1 || ev[0].Type != service.EventLabelChanged || ev[0].LabelID != l.ID {
		t.Fatalf("events = %+v", ev)
	}
	err = env.Svc.Labels.Delete(ctx, a, l.ID)
	wp7Kind(t, err, service.KindNotFound, "")
}

func TestLabelsArchivedAndCheckOrder(t *testing.T) {
	env := testutil.NewTestServices(t)
	m := env.NewMatrix(t, "WEB")
	owner, ed, vw := wp7U(m, testutil.Owner), wp7U(m, testutil.Editor), wp7U(m, testutil.Viewer)
	l, err := env.Svc.Labels.Create(ctx, owner, "WEB", service.LabelInput{Name: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	env.Archive(t, m.Project.ID)
	bad := service.LabelInput{Name: "", Color: "mauve"}
	// non-member 404, viewer forbidden, editor project_archived (before validation), owner too
	_, err = env.Svc.Labels.Create(ctx, wp7U(m, testutil.NonMember), "WEB", bad)
	wp7Kind(t, err, service.KindNotFound, "")
	_, err = env.Svc.Labels.Create(ctx, vw, "WEB", bad)
	wp7Kind(t, err, service.KindForbidden, "forbidden")
	for _, a := range []service.Actor{ed, owner} {
		_, err = env.Svc.Labels.Create(ctx, a, "WEB", bad)
		wp7Kind(t, err, service.KindConflict, service.CodeProjectArchived)
		_, err = env.Svc.Labels.Update(ctx, a, l.ID, service.UpdateLabelInput{Color: service.Some(service.LabelColor("mauve"))})
		wp7Kind(t, err, service.KindConflict, service.CodeProjectArchived)
		err = env.Svc.Labels.Delete(ctx, a, l.ID)
		wp7Kind(t, err, service.KindConflict, service.CodeProjectArchived)
	}
	// scope comes before archived
	rd := env.TokenActor(env.NewToken(t, m.Users[testutil.Owner].ID, service.ScopeRead, ""))
	_, err = env.Svc.Labels.Create(ctx, rd, "WEB", bad)
	wp7Kind(t, err, service.KindForbidden, service.CodeInsufficientScope)
	// reads still work
	if got, err := env.Svc.Labels.List(ctx, vw, "WEB"); err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	// live project: an editor with invalid input gets a validation error with fields
	env2 := testutil.NewTestServices(t)
	m2 := env2.NewMatrix(t, "WEB")
	_, err = env2.Svc.Labels.Create(ctx, wp7U(m2, testutil.Editor), "WEB", bad)
	wp7Kind(t, err, service.KindValidation, "")
	if se := err.(*service.Error); se.Fields["name"] == "" || se.Fields["color"] == "" {
		t.Fatalf("fields = %v", se.Fields)
	}
}
