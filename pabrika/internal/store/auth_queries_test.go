package store_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/RJGJ/Pabrika/internal/store/db"
)

const (
	tsA = "2026-01-01T00:00:00.000Z"
	tsB = "2026-01-01T00:01:00.000Z"
	tsC = "2026-01-01T00:02:00.000Z"
)

func seedUser(t *testing.T, q *db.Queries, id, email string) {
	t.Helper()
	if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: id, Email: email, DisplayName: "N " + id, PasswordHash: "h", CreatedAt: tsA}); err != nil {
		t.Fatal(err)
	}
}

func seedToken(t *testing.T, q *db.Queries, id, user, hash string, project *string, created string) {
	t.Helper()
	if _, err := q.CreateAPIToken(ctx, db.CreateAPITokenParams{ID: id, UserID: user, Name: id, TokenHash: hash,
		TokenPrefix: "pb_" + id, Scope: "write", ProjectID: project, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthSessionQueries(t *testing.T) {
	s := memStore(t)
	q := s.Read()
	seedUser(t, q, "U1", "a@x.io")
	seedUser(t, q, "U2", "b@x.io")
	for _, c := range []struct{ hash, user, exp string }{
		{"h1", "U1", tsC}, {"h2", "U1", tsA}, {"h3", "U2", tsB},
	} {
		if err := q.CreateSession(ctx, db.CreateSessionParams{TokenHash: c.hash, UserID: c.user, ExpiresAt: c.exp, CreatedAt: tsA}); err != nil {
			t.Fatal(err)
		}
	}
	row, err := q.SessionGetByHash(ctx, "h1")
	if err != nil || row.UserID != "U1" || row.Email != "a@x.io" || row.DisplayName != "N U1" || row.ExpiresAt != tsC || row.UserCreatedAt != tsA {
		t.Fatalf("get = %+v %v", row, err)
	}
	if _, err := q.SessionGetByHash(ctx, "nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing = %v", err)
	}
	if n, err := q.SessionExtend(ctx, db.SessionExtendParams{ExpiresAt: tsB, TokenHash: "h1"}); err != nil || n != 1 {
		t.Fatalf("extend = %d %v", n, err)
	}
	if n, _ := q.SessionExtend(ctx, db.SessionExtendParams{ExpiresAt: tsB, TokenHash: "zz"}); n != 0 {
		t.Fatalf("extend unknown = %d", n)
	}
	// expired: strictly "expires_at <= now" (tsA and tsB rows with now = tsB: h2, h3, and h1 now tsB)
	if n, err := q.SessionDeleteExpired(ctx, tsB); err != nil || n != 3 {
		t.Fatalf("purge = %d %v", n, err)
	}
	if err := q.CreateSession(ctx, db.CreateSessionParams{TokenHash: "h4", UserID: "U1", ExpiresAt: tsC, CreatedAt: tsA}); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.SessionDeleteExpired(ctx, tsB); n != 0 {
		t.Fatalf("purge kept row = %d", n)
	}
	if n, err := q.SessionDeleteByHash(ctx, "h4"); err != nil || n != 1 {
		t.Fatalf("delete = %d %v", n, err)
	}
	for _, h := range []string{"a", "b"} {
		_ = q.CreateSession(ctx, db.CreateSessionParams{TokenHash: h, UserID: "U1", ExpiresAt: tsC, CreatedAt: tsA})
	}
	_ = q.CreateSession(ctx, db.CreateSessionParams{TokenHash: "c", UserID: "U2", ExpiresAt: tsC, CreatedAt: tsA})
	if n, err := q.SessionDeleteAllForUser(ctx, "U1"); err != nil || n != 2 {
		t.Fatalf("delete all = %d %v", n, err)
	}
	if hs, _ := q.ListSessionHashesForUser(ctx, "U2"); len(hs) != 1 {
		t.Fatalf("other user's sessions = %v", hs)
	}
}

func TestAuthTokenQueries(t *testing.T) {
	s := memStore(t)
	q := s.Read()
	seedUser(t, q, "U1", "a@x.io")
	seedUser(t, q, "U2", "b@x.io")
	if _, err := q.SeedProject(ctx, db.SeedProjectParams{ID: "P1", Key: "WEB", Name: "W", CreatedBy: "U1", CreatedAt: tsA, UpdatedAt: tsA}); err != nil {
		t.Fatal(err)
	}
	p1 := "P1"
	seedToken(t, q, "T1", "U1", "hash1", &p1, tsA)
	seedToken(t, q, "T2", "U1", "hash2", nil, tsB)
	seedToken(t, q, "T3", "U2", "hash3", nil, tsC)

	got, err := q.TokenGetByHash(ctx, "hash1")
	if err != nil || got.ID != "T1" || got.OwnerEmail != "a@x.io" || got.ProjectKey == nil || *got.ProjectKey != "WEB" || got.RevokedAt != nil {
		t.Fatalf("by hash = %+v %v", got, err)
	}
	got2, _ := q.TokenGetByHash(ctx, "hash2")
	if got2.ProjectID != nil || got2.ProjectKey != nil {
		t.Fatalf("unlimited token has project: %+v", got2)
	}
	if _, err := q.TokenGetByHash(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing = %v", err)
	}

	list, err := q.TokenListForUser(ctx, "U1")
	if err != nil || len(list) != 2 || list[0].ID != "T2" || list[1].ID != "T1" {
		t.Fatalf("list = %+v %v", list, err)
	}
	if _, err := q.TokenGetForUser(ctx, db.TokenGetForUserParams{ID: "T1", UserID: "U2"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get for other user = %v", err)
	}

	// active count excludes revoked
	if n, _ := q.TokenCountActive(ctx, "U1"); n != 2 {
		t.Fatalf("active = %d", n)
	}
	rev := tsC
	if n, err := q.TokenRevoke(ctx, db.TokenRevokeParams{RevokedAt: &rev, ID: "T1", UserID: "U1"}); err != nil || n != 1 {
		t.Fatalf("revoke = %d %v", n, err)
	}
	if n, _ := q.TokenCountActive(ctx, "U1"); n != 1 {
		t.Fatalf("active after revoke = %d", n)
	}
	// second revoke: still 1 row matched, original timestamp kept
	later := "2027-01-01T00:00:00.000Z"
	if n, _ := q.TokenRevoke(ctx, db.TokenRevokeParams{RevokedAt: &later, ID: "T1", UserID: "U1"}); n != 1 {
		t.Fatalf("idempotent revoke matched %d", n)
	}
	if r, _ := q.TokenGetForUser(ctx, db.TokenGetForUserParams{ID: "T1", UserID: "U1"}); r.RevokedAt == nil || *r.RevokedAt != tsC {
		t.Fatalf("revoked_at = %v", r.RevokedAt)
	}
	// someone else's token cannot be revoked
	if n, _ := q.TokenRevoke(ctx, db.TokenRevokeParams{RevokedAt: &rev, ID: "T3", UserID: "U1"}); n != 0 {
		t.Fatalf("revoked other's token: %d", n)
	}
}

func TestAuthTokenTouchThrottle(t *testing.T) {
	s := memStore(t)
	q := s.Read()
	seedUser(t, q, "U1", "a@x.io")
	seedToken(t, q, "T1", "U1", "hash1", nil, tsA)

	touch := func(now, threshold string) int64 {
		t.Helper()
		n, err := q.TokenTouch(ctx, db.TokenTouchParams{Now: &now, ID: "T1", Threshold: &threshold})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	lastUsed := func() string {
		r, _ := q.TokenGetForUser(ctx, db.TokenGetForUserParams{ID: "T1", UserID: "U1"})
		if r.LastUsedAt == nil {
			return ""
		}
		return *r.LastUsedAt
	}
	// never used: written
	if n := touch("2026-01-01T00:10:00.000Z", "2026-01-01T00:09:00.000Z"); n != 1 || lastUsed() != "2026-01-01T00:10:00.000Z" {
		t.Fatalf("first touch n=%d last=%s", n, lastUsed())
	}
	// 30 s later (threshold = now-60s is before last_used): no-op
	if n := touch("2026-01-01T00:10:30.000Z", "2026-01-01T00:09:30.000Z"); n != 0 || lastUsed() != "2026-01-01T00:10:00.000Z" {
		t.Fatalf("touch inside window n=%d last=%s", n, lastUsed())
	}
	// 61 s later: written
	if n := touch("2026-01-01T00:11:01.000Z", "2026-01-01T00:10:01.000Z"); n != 1 || lastUsed() != "2026-01-01T00:11:01.000Z" {
		t.Fatalf("touch after window n=%d last=%s", n, lastUsed())
	}
}

func TestAuthProjectDeleteCascadesLimitedTokens(t *testing.T) {
	s := memStore(t)
	q := s.Read()
	seedUser(t, q, "U1", "a@x.io")
	if _, err := q.SeedProject(ctx, db.SeedProjectParams{ID: "P1", Key: "WEB", Name: "W", CreatedBy: "U1", CreatedAt: tsA, UpdatedAt: tsA}); err != nil {
		t.Fatal(err)
	}
	p1 := "P1"
	seedToken(t, q, "T1", "U1", "hash1", &p1, tsA)
	seedToken(t, q, "T2", "U1", "hash2", nil, tsA)
	if err := s.Exec(ctx, "DELETE FROM projects WHERE id = 'P1'"); err != nil {
		t.Fatal(err)
	}
	list, _ := q.TokenListForUser(ctx, "U1")
	if len(list) != 1 || list[0].ID != "T2" {
		t.Fatalf("after cascade = %+v", list)
	}
}

func TestAuthAccountQueriesAreCaseInsensitive(t *testing.T) {
	s := memStore(t)
	q := s.Read()
	seedUser(t, q, "U1", "Mixed@X.io")
	row, err := q.AccountGetCredentialsByEmail(ctx, "mixed@x.io")
	if err != nil || row.ID != "U1" || row.PasswordHash != "h" {
		t.Fatalf("credentials = %+v %v", row, err)
	}
	if h, err := q.AccountGetPasswordHash(ctx, "U1"); err != nil || h != "h" {
		t.Fatalf("hash = %q %v", h, err)
	}
	if _, err := q.AccountGetPasswordHash(ctx, "none"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown = %v", err)
	}
	// the UNIQUE index is NOCASE too: a differently-cased duplicate violates it
	_, err = q.CreateUser(ctx, db.CreateUserParams{ID: "U2", Email: "MIXED@x.io", DisplayName: "d", PasswordHash: "h", CreatedAt: tsA})
	if err == nil {
		t.Fatal("duplicate email (case-different) accepted")
	}
}
