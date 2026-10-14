package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/RJGJ/Pabrika/migrations"
)

// openRawDB opens a plain SQLite file with foreign keys on and a single connection, so the
// migration runs under the same FK enforcement as production.
func openRawDB(t *testing.T) (*sql.DB, *goose.Provider) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	p, err := goose.NewProvider(goose.DialectSQLite3, d, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	return d, p
}

func rawCount(t *testing.T, d *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := d.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigration00002TokenScopeComment(t *testing.T) {
	d, p := openRawDB(t)
	if _, err := p.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) error {
		_, err := d.ExecContext(ctx, q, args...)
		return err
	}
	if err := exec(`INSERT INTO users (id, email, display_name, password_hash, created_at) VALUES ('U1','a@x.io','A','h','2026-01-01T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO projects (id, key, name, created_by, created_at, updated_at) VALUES ('P1','WEB','W','U1','2026-01-01T00:00:00.000Z','2026-01-01T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	tok := func(id, scope string, project any) error {
		return exec(`INSERT INTO api_tokens (id, user_id, name, token_hash, token_prefix, scope, project_id, last_used_at, revoked_at, created_at)
			VALUES (?, 'U1', 'n-'||?, 'hash-'||?, 'pb_12345', ?, ?, '2026-02-01T00:00:00.000Z', NULL, '2026-01-02T00:00:00.000Z')`, id, id, id, scope, project)
	}
	if err := tok("T1", "read", nil); err != nil {
		t.Fatal(err)
	}
	if err := tok("T2", "write", "P1"); err != nil {
		t.Fatal(err)
	}
	if err := tok("T3", "comment", nil); err == nil {
		t.Fatal("comment must be rejected before migration 00002")
	}

	if _, err := p.UpTo(ctx, 2); err != nil {
		t.Fatal("up:", err)
	}
	// existing rows survive with ids, hashes, scope, project and timestamps
	if n := rawCount(t, d, `SELECT count(*) FROM api_tokens`); n != 2 {
		t.Fatalf("rows after up: %d", n)
	}
	var hash, scope, created string
	var project, lastUsed sql.NullString
	if err := d.QueryRowContext(ctx, `SELECT token_hash, scope, project_id, last_used_at, created_at FROM api_tokens WHERE id='T2'`).
		Scan(&hash, &scope, &project, &lastUsed, &created); err != nil {
		t.Fatal(err)
	}
	if hash != "hash-T2" || scope != "write" || project.String != "P1" || lastUsed.String != "2026-02-01T00:00:00.000Z" || created != "2026-01-02T00:00:00.000Z" {
		t.Fatalf("row changed: %s %s %v %v %s", hash, scope, project, lastUsed, created)
	}
	// the new scope is accepted, junk is not
	if err := tok("T3", "comment", nil); err != nil {
		t.Fatal("comment after up:", err)
	}
	if err := tok("T4", "admin", nil); err == nil {
		t.Fatal("admin must be rejected")
	}
	// the user index is back and the hash stays unique
	if n := rawCount(t, d, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='tokens_by_user'`); n != 1 {
		t.Fatal("tokens_by_user missing after up")
	}
	if err := exec(`INSERT INTO api_tokens (id, user_id, name, token_hash, token_prefix, scope, created_at) VALUES ('T9','U1','dup','hash-T1','pb_x','read','2026-01-01T00:00:00.000Z')`); err == nil {
		t.Fatal("token_hash must stay unique")
	}
	// the cascade from users and projects still works on the rebuilt table
	if err := exec(`DELETE FROM projects WHERE id='P1'`); err != nil {
		t.Fatal(err)
	}
	if n := rawCount(t, d, `SELECT count(*) FROM api_tokens WHERE id='T2'`); n != 0 {
		t.Fatal("project cascade lost")
	}
	// no table references api_tokens, so the rebuild needs no foreign-key juggling
	for _, tb := range []string{"users", "sessions", "projects", "project_members", "labels", "tickets", "ticket_labels", "comments", "ticket_activity"} {
		rows, err := d.QueryContext(ctx, `SELECT "table" FROM pragma_foreign_key_list(?)`, tb)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var ref string
			if err := rows.Scan(&ref); err != nil {
				t.Fatal(err)
			}
			if ref == "api_tokens" {
				t.Fatalf("%s references api_tokens", tb)
			}
		}
		rows.Close()
	}

	// down demotes comment tokens to read and restores the old CHECK
	if _, err := p.DownTo(ctx, 1); err != nil {
		t.Fatal("down:", err)
	}
	if got := rawCount(t, d, `SELECT count(*) FROM api_tokens WHERE scope='read' AND id IN ('T1','T3')`); got != 2 {
		t.Fatalf("down: want T1 and T3 read, got %d", got)
	}
	if err := tok("T5", "comment", nil); err == nil {
		t.Fatal("comment must be rejected after down")
	}
	if n := rawCount(t, d, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='tokens_by_user'`); n != 1 {
		t.Fatal("tokens_by_user missing after down")
	}
	// and up again works
	if _, err := p.UpTo(ctx, 2); err != nil {
		t.Fatal("up again:", err)
	}
}
