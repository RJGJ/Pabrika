package store_test

import "testing"

func TestMigration00003WorkflowDerived(t *testing.T) {
	d, p := openRawDB(t)
	if _, err := p.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) error {
		_, err := d.ExecContext(ctx, q, args...)
		return err
	}
	const at = "2026-01-01T00:00:00.000Z"
	for _, q := range []string{
		`INSERT INTO users (id, email, display_name, password_hash, created_at) VALUES ('U1','a@x.io','A','h','` + at + `')`,
		`INSERT INTO projects (id, key, name, created_by, created_at, updated_at) VALUES ('P1','WEB','W','U1','` + at + `','` + at + `')`,
		`INSERT INTO tickets (id, project_id, number, title, position, created_at, updated_at) VALUES ('T1','P1',1,'t',1024,'` + at + `','` + at + `')`,
	} {
		if err := exec(q); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := p.UpTo(ctx, 3); err != nil {
		t.Fatal("up:", err)
	}
	// existing tickets keep their data; the derived cache starts empty
	var title string
	var pr, branch, rn, rm any
	if err := d.QueryRowContext(ctx, `SELECT title, pr_url, branch, round_n, round_max FROM tickets WHERE id='T1'`).Scan(&title, &pr, &branch, &rn, &rm); err != nil {
		t.Fatal(err)
	}
	if title != "t" || pr != nil || branch != nil || rn != nil || rm != nil {
		t.Fatalf("row after up: %q %v %v %v %v", title, pr, branch, rn, rm)
	}
	dec := func(id, gate, verdict, role string) error {
		return exec(`INSERT INTO ticket_decisions (id, ticket_id, project_id, gate, verdict, note, actor_user_id, actor_role, gate_label, created_at)
			VALUES (?, 'T1', 'P1', ?, ?, NULL, 'U1', ?, 'awaiting-plan-approval', ?)`, id, gate, verdict, role, at)
	}
	if err := dec("D1", "plan", "approved", "editor"); err != nil {
		t.Fatal(err)
	}
	if err := dec("D2", "merge", "approved", "editor"); err == nil {
		t.Fatal("bad gate accepted")
	}
	if err := dec("D3", "plan", "rejected", "editor"); err == nil {
		t.Fatal("bad verdict accepted")
	}
	if err := dec("D4", "plan", "approved", "viewer"); err == nil {
		t.Fatal("bad role accepted")
	}
	for _, ix := range []string{"decisions_by_ticket", "decisions_by_project"} {
		if n := rawCount(t, d, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?`, ix); n != 1 {
			t.Fatalf("index %s missing", ix)
		}
	}
	// a hard delete of the project cascades to the decisions (soft deletes keep them)
	if err := exec(`DELETE FROM projects WHERE id='P1'`); err != nil {
		t.Fatal(err)
	}
	if n := rawCount(t, d, `SELECT count(*) FROM ticket_decisions`); n != 0 {
		t.Fatal("cascade lost")
	}

	if _, err := p.DownTo(ctx, 2); err != nil {
		t.Fatal("down:", err)
	}
	if n := rawCount(t, d, `SELECT count(*) FROM sqlite_master WHERE name='ticket_decisions'`); n != 0 {
		t.Fatal("decisions table survived down")
	}
	if n := rawCount(t, d, `SELECT count(*) FROM pragma_table_info('tickets') WHERE name IN ('pr_url','branch','round_n','round_max')`); n != 0 {
		t.Fatal("derived columns survived down")
	}
	if _, err := p.UpTo(ctx, 3); err != nil {
		t.Fatal("up again:", err)
	}
}
