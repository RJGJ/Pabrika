package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
	"github.com/RJGJ/Pabrika/migrations"
)

var ctx = context.Background()

func memStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func fileStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "p.db")
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestTimeFormat(t *testing.T) {
	a := time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.FixedZone("x", 3600))
	b := a.Add(time.Millisecond)
	fa, fb := store.FormatTime(a), store.FormatTime(b)
	if fa != "2026-01-02T02:04:05.006Z" {
		t.Fatal(fa)
	}
	if len(fa) != len(fb) || !(fa < fb) {
		t.Fatal("not fixed width / sortable")
	}
	if len(store.FormatTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))) != len(fa) {
		t.Fatal("zero millis width")
	}
	back, err := store.ParseTime(fa)
	if err != nil || !back.Equal(a) {
		t.Fatal(back, err)
	}
}

func TestMigrate(t *testing.T) {
	s := memStore(t)
	v, err := s.Migrate(ctx)
	if err != nil || v != 1 {
		t.Fatalf("rerun: %d %v", v, err)
	}
	want := []string{"users", "sessions", "projects", "project_members", "api_tokens", "labels",
		"tickets", "ticket_labels", "comments", "ticket_activity"}
	for _, tb := range want {
		if n := count(t, s, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='"+tb+"'"); n != 1 {
			t.Errorf("table %s missing", tb)
		}
	}
	if n := count(t, s, "SELECT count(*) FROM sqlite_master WHERE type='index' AND name='tickets_board'"); n != 1 {
		t.Error("tickets_board missing")
	}
	if n := count(t, s, "PRAGMA foreign_keys"); n != 1 {
		t.Error("foreign_keys off")
	}
}

func count(t *testing.T, s *store.Store, query string) int64 {
	t.Helper()
	var n int64
	if err := s.RawQueryRow(ctx, query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrateDownUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.db")
	d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	p, err := goose.NewProvider(goose.DialectSQLite3, d, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatal("down:", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal("up again:", err)
	}
}

func TestMigrateNewerDB(t *testing.T) {
	s := memStore(t)
	if err := s.Exec(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES (99, 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err == nil {
		t.Fatal("expected error for newer database")
	}
	if n := count(t, s, "SELECT count(*) FROM goose_db_version"); n != 3 {
		t.Fatalf("version table changed: %d rows", n)
	}
}

func TestFileStore(t *testing.T) {
	s, path := fileStore(t)
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var jm string
	var bt int
	if err := s.RawQueryRow(ctx, "PRAGMA journal_mode").Scan(&jm); err != nil {
		t.Fatal(err)
	}
	if err := s.RawQueryRow(ctx, "PRAGMA busy_timeout").Scan(&bt); err != nil {
		t.Fatal(err)
	}
	if jm != "wal" || bt != 5000 {
		t.Fatalf("journal=%s busy=%d", jm, bt)
	}
	// seed something so the WAL has content, then Close must checkpoint it.
	if err := s.Exec(ctx, "INSERT INTO users VALUES ('U1','a@b.c','A','x','t')"); err != nil {
		t.Fatal(err)
	}
	// read pool sees it and refuses writes
	if _, err := s.Read().GetUserByID(ctx, "U1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read().UpdateUserDisplayName(ctx, db.UpdateUserDisplayNameParams{DisplayName: "z", ID: "U1"}); err == nil {
		t.Fatal("read pool accepted a write")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path + "-wal"); err == nil && fi.Size() != 0 {
		t.Fatalf("wal not checkpointed: %d bytes", fi.Size())
	}
	// reopen: data persisted, migrate is a no-op
	s2, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if v, err := s2.Migrate(ctx); err != nil || v != 1 {
		t.Fatal(v, err)
	}
	if _, err := s2.Read().GetUserByID(ctx, "U1"); err != nil {
		t.Fatal(err)
	}
}

func TestTwoStoresMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "two.db")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := store.Open(ctx, path)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			_, err = s.Migrate(ctx)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWithTxRollback(t *testing.T) {
	s := memStore(t)
	boom := errors.New("boom")
	err := s.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: "U1", Email: "a@b.c", DisplayName: "A", PasswordHash: "x", CreatedAt: "t"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := s.Read().GetUserByID(ctx, "U1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("row survived rollback: %v", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic swallowed")
			}
		}()
		_ = s.WithTx(ctx, func(q *db.Queries) error {
			_, _ = q.CreateUser(ctx, db.CreateUserParams{ID: "U2", Email: "b@b.c", DisplayName: "B", PasswordHash: "x", CreatedAt: "t"})
			panic("kaboom")
		})
	}()
	if _, err := s.Read().GetUserByID(ctx, "U2"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("row survived panic: %v", err)
	}
	// store still usable
	if err := s.WithTx(ctx, func(q *db.Queries) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestNestedTxFailsFast(t *testing.T) {
	for _, name := range []string{"WithTx", "Read"} {
		t.Run(name, func(t *testing.T) {
			s := memStore(t)
			c, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			var inner error
			start := time.Now()
			_ = s.WithTx(c, func(q *db.Queries) error {
				if name == "WithTx" {
					inner = s.WithTx(c, func(*db.Queries) error { return nil })
				} else {
					_, inner = s.Read().GetUserByID(c, "x")
				}
				return nil
			})
			if inner == nil || time.Since(start) > 3*time.Second {
				t.Fatalf("nested call should fail fast, got %v after %v", inner, time.Since(start))
			}
		})
	}
}

func TestConcurrentWritesNoLock(t *testing.T) {
	s, _ := fileStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.WithTx(ctx, func(q *db.Queries) error {
				_, err := q.CreateUser(ctx, db.CreateUserParams{ID: string(rune('A' + i)), Email: string(rune('a'+i)) + "@x.y", DisplayName: "n", PasswordHash: "x", CreatedAt: "t"})
				return err
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestUniqueViolation(t *testing.T) {
	s := memStore(t)
	p := db.CreateUserParams{ID: "U1", Email: "a@b.c", DisplayName: "A", PasswordHash: "x", CreatedAt: "t"}
	if err := s.WithTx(ctx, func(q *db.Queries) error { _, err := q.CreateUser(ctx, p); return err }); err != nil {
		t.Fatal(err)
	}
	p.ID = "U2"
	p.Email = "A@B.C" // NOCASE unique
	err := s.WithTx(ctx, func(q *db.Queries) error { _, err := q.CreateUser(ctx, p); return err })
	if !store.IsUniqueViolation(err) || !store.IsUniqueViolationOn(err, "users.email") {
		t.Fatal(err)
	}
	p.Email = "z@b.c"
	p.ID = "U1"
	err = s.WithTx(ctx, func(q *db.Queries) error { _, err := q.CreateUser(ctx, p); return err })
	if !store.IsUniqueViolation(err) || !strings.Contains(err.Error(), "users.id") {
		t.Fatal(err)
	}
	if store.IsUniqueViolation(errors.New("x")) {
		t.Fatal("false positive")
	}
}

func TestMemoryDBSurvivesCancelledContext(t *testing.T) {
	s := memStore(t)
	c, cancel := context.WithCancel(ctx)
	cancel()
	_ = s.WithTx(c, func(q *db.Queries) error { return nil })
	p := db.CreateUserParams{ID: "U1", Email: "a@b.c", DisplayName: "A", PasswordHash: "x", CreatedAt: "t"}
	if err := s.WithTx(ctx, func(q *db.Queries) error { _, err := q.CreateUser(ctx, p); return err }); err != nil {
		t.Fatal(err)
	}
}

func TestUserAndSessionQueries(t *testing.T) {
	s := memStore(t)
	err := s.WithTx(ctx, func(q *db.Queries) error {
		for _, id := range []string{"U1", "U2"} {
			if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: id, Email: id + "@x.y", DisplayName: "Name " + id, PasswordHash: "h-" + id, CreatedAt: "t"}); err != nil {
				return err
			}
		}
		for _, sess := range [][2]string{{"s1", "U1"}, {"s2", "U1"}, {"s3", "U1"}, {"o1", "U2"}} {
			if err := q.CreateSession(ctx, db.CreateSessionParams{TokenHash: sess[0], UserID: sess[1], ExpiresAt: "e", CreatedAt: "c"}); err != nil {
				return err
			}
		}
		u, err := q.UpdateUserDisplayName(ctx, db.UpdateUserDisplayNameParams{DisplayName: "New", ID: "U1"})
		if err != nil {
			return err
		}
		if u.DisplayName != "New" || u.Email != "U1@x.y" || u.PasswordHash != "h-U1" {
			t.Errorf("display name update touched other columns: %+v", u)
		}
		n, err := q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{PasswordHash: "new", ID: "U2"})
		if err != nil || n != 1 {
			t.Errorf("password: %d %v", n, err)
		}
		u2, _ := q.GetUserByID(ctx, "U2")
		if u2.PasswordHash != "new" || u2.DisplayName != "Name U2" {
			t.Errorf("password update touched other columns: %+v", u2)
		}
		u1, _ := q.GetUserByID(ctx, "U1")
		if u1.PasswordHash != "h-U1" {
			t.Error("password of other user changed")
		}
		del, err := q.DeleteSessionsForUserExcept(ctx, db.DeleteSessionsForUserExceptParams{UserID: "U1", TokenHash: "s2"})
		if err != nil || del != 2 {
			t.Errorf("deleted %d %v", del, err)
		}
		h1, _ := q.ListSessionHashesForUser(ctx, "U1")
		h2, _ := q.ListSessionHashesForUser(ctx, "U2")
		if len(h1) != 1 || h1[0] != "s2" || len(h2) != 1 {
			t.Errorf("sessions: %v %v", h1, h2)
		}
		byEmail, err := q.GetUserByEmail(ctx, "u1@X.Y")
		if err != nil || byEmail.ID != "U1" {
			t.Errorf("case-insensitive email lookup: %v %v", byEmail, err)
		}
		list, err := q.ListUsersByIDs(ctx, []string{"U1", "U2", "nope"})
		if err != nil || len(list) != 2 {
			t.Errorf("list: %v %v", list, err)
		}
		tok, err := q.CreateAPIToken(ctx, db.CreateAPITokenParams{ID: "T1", UserID: "U1", Name: "n", TokenHash: "th", TokenPrefix: "pb_abcde", Scope: "read", CreatedAt: "c"})
		if err != nil || tok.ProjectID != nil || tok.Scope != "read" {
			t.Errorf("token: %+v %v", tok, err)
		}
		got, err := q.GetAPIToken(ctx, "T1")
		if err != nil || got.ID != "T1" {
			t.Errorf("get token: %+v %v", got, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueryCount(t *testing.T) {
	s := memStore(t)
	before := s.QueryCount()
	_, _ = s.Read().GetUserByID(ctx, "x")
	if s.QueryCount() != before+1 {
		t.Fatal("count did not increase")
	}
}
