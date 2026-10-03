package auth_test

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

type rig struct {
	env  *testutil.Env
	sess *auth.Sessions
	rv   *auth.Resolver
	user testutil.User
}

func newRig(t *testing.T) *rig {
	t.Helper()
	env := testutil.NewTestServices(t)
	sess := auth.NewSessions(env.Store, auth.SessionOptions{Now: env.Clock.Now})
	rv := auth.NewResolver(auth.ResolverDeps{Store: env.Store, Sessions: sess, Now: env.Clock.Now})
	return &rig{env: env, sess: sess, rv: rv, user: env.NewUser(t, "a@x.io", "Alice")}
}

func (r *rig) newSession(t *testing.T) (value, hash string) {
	t.Helper()
	v, _, err := r.sess.Create(ctx, r.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return v, auth.HashSessionValue(v)
}

func (r *rig) expiryOf(t *testing.T, hash string) string {
	t.Helper()
	row, err := r.env.Store.Read().SessionGetByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	return row.ExpiresAt
}

func TestSessionCreateStoresOnlyHash(t *testing.T) {
	r := newRig(t)
	value, exp, err := r.sess.Create(ctx, r.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(value) {
		t.Fatalf("cookie value shape: %q", value)
	}
	if want := r.env.Clock.Now().Add(auth.SessionLifetime); !exp.Equal(want) {
		t.Fatalf("expiry = %v, want %v", exp, want)
	}
	hs, _ := r.env.Store.Read().ListSessionHashesForUser(ctx, r.user.ID)
	if len(hs) != 1 || hs[0] != auth.HashSessionValue(value) || hs[0] == value || len(hs[0]) != 64 {
		t.Fatalf("stored = %v", hs)
	}
	// nothing anywhere in the database equals the plain value
	var n int
	if err := r.env.Store.RawQueryRow(ctx, "SELECT COUNT(*) FROM sessions WHERE token_hash = ? OR user_id = ?", value, value).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plain value found in db: %d %v", n, err)
	}
	// fresh random value every time
	v2, _, _ := r.sess.Create(ctx, r.user.ID)
	if v2 == value {
		t.Fatal("session values repeat")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	r := newRig(t)
	c := r.sess.Cookie("abc")
	if c.Name != "pb_session" || c.Value != "abc" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode ||
		c.Path != "/" || c.MaxAge != 2592000 || c.Secure {
		t.Fatalf("cookie = %+v", c)
	}
	s := c.String()
	for _, want := range []string{"pb_session=abc", "Path=/", "Max-Age=2592000", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q missing %q", s, want)
		}
	}
	if strings.Contains(s, "Secure") {
		t.Errorf("Secure set without COOKIE_SECURE: %q", s)
	}

	secure := auth.NewSessions(r.env.Store, auth.SessionOptions{CookieSecure: true})
	if sc := secure.Cookie("x").String(); !strings.Contains(sc, "Secure") {
		t.Errorf("Secure missing: %q", sc)
	}
	clear := secure.ClearCookie().String()
	for _, want := range []string{"pb_session=;", "Path=/", "Max-Age=0", "HttpOnly", "SameSite=Lax", "Secure"} {
		if !strings.Contains(clear+";", want) {
			t.Errorf("clear cookie %q missing %q", clear, want)
		}
	}
}

func TestSessionLookupExpiry(t *testing.T) {
	r := newRig(t)
	_, hash := r.newSession(t)
	rec, err := r.sess.Lookup(ctx, hash)
	if err != nil || rec.User.ID != r.user.ID || rec.User.Email != "a@x.io" || rec.User.DisplayName != "Alice" || rec.TokenHash != hash {
		t.Fatalf("lookup = %+v %v", rec, err)
	}
	if _, err := r.sess.Lookup(ctx, "nope"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("unknown = %v", err)
	}
	r.env.Clock.Advance(auth.SessionLifetime - time.Millisecond)
	if _, err := r.sess.Lookup(ctx, hash); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	r.env.Clock.Advance(time.Millisecond) // expires_at == now counts as expired
	if _, err := r.sess.Lookup(ctx, hash); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("at expiry = %v", err)
	}
}

func TestSessionSlideThrottle(t *testing.T) {
	r := newRig(t)
	_, hash := r.newSession(t)
	created := r.expiryOf(t, hash)

	slide := func() (bool, int64) {
		rec, err := r.sess.Lookup(ctx, hash)
		if err != nil {
			t.Fatal(err)
		}
		before := r.env.Store.QueryCount()
		_, slid, err := r.sess.Slide(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		return slid, r.env.Store.QueryCount() - before
	}
	// inside the first hour: no slide, no write
	r.env.Clock.Advance(59*time.Minute + 59*time.Second)
	if slid, writes := slide(); slid || writes != 0 {
		t.Fatalf("slid=%v writes=%d inside the hour", slid, writes)
	}
	if r.expiryOf(t, hash) != created {
		t.Fatal("expiry changed inside the hour")
	}
	// exactly one hour: expires_at - now == 30d - 1h, not "<", so still no slide
	r.env.Clock.Advance(time.Second)
	if slid, writes := slide(); slid || writes != 0 {
		t.Fatalf("slid=%v writes=%d at exactly 1h", slid, writes)
	}
	// 1 h + 1 s: slides, exactly one write
	r.env.Clock.Advance(time.Second)
	if slid, writes := slide(); !slid || writes != 1 {
		t.Fatalf("slid=%v writes=%d after 1h1s", slid, writes)
	}
	want := store.FormatTime(r.env.Clock.Now().Add(auth.SessionLifetime))
	if got := r.expiryOf(t, hash); got != want {
		t.Fatalf("expiry = %s, want %s", got, want)
	}
	// immediately again: throttled
	if slid, writes := slide(); slid || writes != 0 {
		t.Fatalf("slid=%v writes=%d right after sliding", slid, writes)
	}
}

func TestSessionValidateIsReadOnly(t *testing.T) {
	r := newRig(t)
	_, hash := r.newSession(t)
	created := r.expiryOf(t, hash)
	r.env.Clock.Advance(5 * time.Hour) // stale enough that Slide would extend

	before := r.env.Store.QueryCount()
	if err := r.sess.Validate(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if d := r.env.Store.QueryCount() - before; d != 1 {
		t.Fatalf("Validate issued %d statements, want 1 read", d)
	}
	if r.expiryOf(t, hash) != created {
		t.Fatal("Validate slid the expiry")
	}
	if err := r.sess.Validate(ctx, "missing"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("missing = %v", err)
	}
	r.env.Clock.Advance(auth.SessionLifetime)
	if err := r.sess.Validate(ctx, hash); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("expired = %v", err)
	}
	// a DB failure is NOT ErrInvalidCredentials, so the caller keeps its stream open
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.sess.Validate(cancelled, hash); err == nil || errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("db failure = %v", err)
	}
}

func TestSessionDeleteAndRotate(t *testing.T) {
	r := newRig(t)
	v1, h1 := r.newSession(t)
	_, h2 := r.newSession(t)

	// rotation: the presented session goes away, a new one appears, others are untouched
	v3, _, err := r.sess.Rotate(ctx, r.user.ID, v1)
	if err != nil {
		t.Fatal(err)
	}
	h3 := auth.HashSessionValue(v3)
	hs, _ := r.env.Store.Read().ListSessionHashesForUser(ctx, r.user.ID)
	got := map[string]bool{}
	for _, h := range hs {
		got[h] = true
	}
	if got[h1] || !got[h2] || !got[h3] || len(hs) != 2 {
		t.Fatalf("after rotate: %v (h1=%s h2=%s h3=%s)", hs, h1, h2, h3)
	}
	// garbage or unknown old values delete nothing and do not fail
	if _, _, err := r.sess.Rotate(ctx, r.user.ID, "garbage"); err != nil {
		t.Fatal(err)
	}
	if hs, _ := r.env.Store.Read().ListSessionHashesForUser(ctx, r.user.ID); len(hs) != 3 {
		t.Fatalf("garbage rotate changed sessions: %v", hs)
	}

	if err := r.sess.Delete(ctx, h2); err != nil {
		t.Fatal(err)
	}
	if err := r.sess.Delete(ctx, h2); err != nil {
		t.Fatalf("delete is idempotent: %v", err)
	}
	if _, err := r.sess.Lookup(ctx, h2); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("deleted session still valid: %v", err)
	}
}

func TestSessionPurgeExpired(t *testing.T) {
	r := newRig(t)
	_, old1 := r.newSession(t)
	_, old2 := r.newSession(t)
	r.env.Clock.Advance(auth.SessionLifetime - time.Hour)
	_, fresh := r.newSession(t)
	r.env.Clock.Advance(2 * time.Hour) // old ones expired, fresh has ~30d left
	n, err := r.sess.PurgeExpired(ctx)
	if err != nil || n != 2 {
		t.Fatalf("purged %d %v", n, err)
	}
	hs, _ := r.env.Store.Read().ListSessionHashesForUser(ctx, r.user.ID)
	if len(hs) != 1 || hs[0] != fresh {
		t.Fatalf("remaining = %v (old: %s %s)", hs, old1, old2)
	}
	if n, _ := r.sess.PurgeExpired(ctx); n != 0 {
		t.Fatalf("second purge removed %d", n)
	}
}

func TestSessionStartPurge(t *testing.T) {
	r := newRig(t)
	_, expired := r.newSession(t)
	r.env.Clock.Advance(auth.SessionLifetime + time.Hour)
	_, live := r.newSession(t)

	runCtx, cancel := context.WithCancel(ctx)
	ticks := make(chan time.Time)
	done := r.sess.StartPurge(runCtx, ticks)

	waitFor := func(cond func() bool, what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	count := func() int {
		var n int
		_ = r.env.Store.RawQueryRow(ctx, "SELECT COUNT(*) FROM sessions").Scan(&n)
		return n
	}
	// purge once at start
	waitFor(func() bool { return count() == 1 }, "startup purge")
	if _, err := r.sess.Lookup(ctx, expired); err == nil {
		t.Fatal("expired session survived")
	}

	// then once per tick
	r.env.Clock.Advance(auth.SessionLifetime + time.Hour)
	ticks <- time.Time{}
	waitFor(func() bool { return count() == 0 }, "tick purge")
	_ = live

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("purge goroutine did not stop on cancel")
	}
}
