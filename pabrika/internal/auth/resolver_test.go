package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
)

func request(headers map[string]string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func bearer(secret string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + secret}
}

func (r *rig) cookie(value string) *http.Cookie {
	return &http.Cookie{Name: auth.SessionCookieName, Value: value}
}

func asAuthError(t *testing.T, err error) *auth.AuthError {
	t.Helper()
	var ae *auth.AuthError
	if !errors.As(err, &ae) {
		t.Fatalf("want *auth.AuthError, got %T %v", err, err)
	}
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("AuthError must unwrap to ErrInvalidCredentials: %v", err)
	}
	return ae
}

func TestResolveNoCredentials(t *testing.T) {
	r := newRig(t)
	if _, err := r.rv.Resolve(request(nil)); !errors.Is(err, auth.ErrNoCredentials) {
		t.Fatalf("Resolve = %v", err)
	}
	if _, err := r.rv.ResolveBearer(request(nil, r.cookie("x"))); !errors.Is(err, auth.ErrNoCredentials) {
		t.Fatalf("ResolveBearer without header = %v", err)
	}
}

func TestResolveSession(t *testing.T) {
	r := newRig(t)
	value, hash := r.newSession(t)
	p, err := r.rv.Resolve(request(nil, r.cookie(value)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Method != auth.MethodSession || p.User.ID != r.user.ID || p.User.Email != "a@x.io" || p.User.DisplayName != "Alice" ||
		p.Token != nil || p.Session == nil || p.Session.TokenHash != hash || p.SetCookie != nil {
		t.Fatalf("principal = %+v", p)
	}
	if want := r.env.Clock.Now().Add(auth.SessionLifetime); !p.Session.ExpiresAt.Equal(want) {
		t.Fatalf("expires = %v", p.Session.ExpiresAt)
	}
	if a := p.Actor(); a != service.UserActor(r.user.ID) {
		t.Fatalf("actor = %+v", a)
	}
}

func TestResolveSessionSlidesOncePerHour(t *testing.T) {
	r := newRig(t)
	value, hash := r.newSession(t)
	resolve := func() (auth.Principal, int64) {
		before := r.env.Store.QueryCount()
		p, err := r.rv.Resolve(request(nil, r.cookie(value)))
		if err != nil {
			t.Fatal(err)
		}
		return p, r.env.Store.QueryCount() - before
	}
	// a plain read is exactly one statement and carries no cookie action
	r.env.Clock.Advance(time.Hour)
	if p, n := resolve(); n != 1 || p.SetCookie != nil {
		t.Fatalf("within the hour: statements=%d cookie=%v", n, p.SetCookie)
	}
	r.env.Clock.Advance(time.Second) // 1h + 1s since creation
	p, n := resolve()
	if n != 2 {
		t.Fatalf("slide statements = %d (want read + one write)", n)
	}
	c := p.SetCookie
	if c == nil || c.Name != "pb_session" || c.Value != value || c.MaxAge != 2592000 || !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("re-issued cookie = %+v", c)
	}
	wantExp := r.env.Clock.Now().Add(auth.SessionLifetime)
	if !p.Session.ExpiresAt.Equal(wantExp) {
		t.Fatalf("principal expiry = %v want %v", p.Session.ExpiresAt, wantExp)
	}
	if r.expiryOf(t, hash) != store.FormatTime(wantExp) {
		t.Fatal("db expiry not extended")
	}
	if p, n := resolve(); n != 1 || p.SetCookie != nil {
		t.Fatalf("immediately after slide: statements=%d cookie=%v", n, p.SetCookie)
	}
}

func TestResolveExpiredOrUnknownSessionClearsCookie(t *testing.T) {
	r := newRig(t)
	value, _ := r.newSession(t)
	r.env.Clock.Advance(auth.SessionLifetime)
	_, err := r.rv.Resolve(request(nil, r.cookie(value)))
	if ae := asAuthError(t, err); !ae.ClearCookie || ae.Bearer {
		t.Fatalf("expired: %+v", ae)
	}
	// unknown (well-formed) and garbage values behave the same, garbage without touching the db
	unknown := strings.Repeat("A", 43)
	_, err = r.rv.Resolve(request(nil, r.cookie(unknown)))
	if ae := asAuthError(t, err); !ae.ClearCookie {
		t.Fatalf("unknown: %+v", ae)
	}
	before := r.env.Store.QueryCount()
	for _, v := range []string{"", "short", strings.Repeat("!", 43), strings.Repeat("A", 44)} {
		_, err = r.rv.Resolve(request(nil, r.cookie(v)))
		if ae := asAuthError(t, err); !ae.ClearCookie {
			t.Fatalf("garbage %q: %+v", v, ae)
		}
	}
	if d := r.env.Store.QueryCount() - before; d != 0 {
		t.Fatalf("garbage cookies cost %d queries", d)
	}
}

func TestResolveToken(t *testing.T) {
	r := newRig(t)
	p1 := r.env.NewProject(t, r.user, "WEB")
	id, secret := r.env.NewTokenWithSecret(t, r.user.ID, service.ScopeRead, p1.ID)
	got, err := r.rv.Resolve(request(bearer(secret)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != auth.MethodToken || got.Session != nil || got.SetCookie != nil || got.User.ID != r.user.ID ||
		got.User.Email != "a@x.io" || got.Token == nil {
		t.Fatalf("principal = %+v", got)
	}
	want := auth.TokenInfo{ID: id, Name: "token " + id[len(id)-6:], Scope: service.ScopeRead, ProjectID: p1.ID, ProjectKey: "WEB"}
	if *got.Token != want {
		t.Fatalf("token info = %+v want %+v", *got.Token, want)
	}
	if a := got.Actor(); a != service.TokenActor(id, r.user.ID, service.ScopeRead, p1.ID) {
		t.Fatalf("actor = %+v", a)
	}
	// unlimited token
	_, s2 := r.env.NewTokenWithSecret(t, r.user.ID, service.ScopeWrite, "")
	g2, err := r.rv.ResolveBearer(request(bearer(s2)))
	if err != nil || g2.Token.ProjectID != "" || g2.Token.ProjectKey != "" || g2.Token.Scope != service.ScopeWrite {
		t.Fatalf("unlimited = %+v %v", g2, err)
	}
}

func TestResolveBearerSchemeCaseInsensitive(t *testing.T) {
	r := newRig(t)
	_, secret := r.env.NewTokenWithSecret(t, r.user.ID, "", "")
	for _, h := range []string{"Bearer " + secret, "bearer " + secret, "BEARER " + secret} {
		if _, err := r.rv.Resolve(request(map[string]string{"Authorization": h})); err != nil {
			t.Fatalf("%q: %v", h[:8], err)
		}
	}
}

func TestMalformedBearerCostsZeroQueries(t *testing.T) {
	r := newRig(t)
	_, secret := r.env.NewTokenWithSecret(t, r.user.ID, "", "")
	value, _ := r.newSession(t)
	bad := []string{
		"", "Bearer", "Bearer ", "Basic abc", "Bearer pb_short", secret, "Token " + secret,
		"Bearer " + strings.ToUpper(secret), "Bearer " + secret + "00", "Bearer " + strings.Repeat("a", 10*1024),
	}
	before := r.env.Store.QueryCount()
	for _, h := range bad {
		for _, withCookie := range []bool{false, true} {
			var cookies []*http.Cookie
			if withCookie {
				cookies = append(cookies, r.cookie(value))
			}
			_, err := r.rv.Resolve(request(map[string]string{"Authorization": h}, cookies...))
			ae := asAuthError(t, err)
			if !ae.Bearer || ae.ClearCookie {
				t.Fatalf("header %.12q: %+v", h, ae)
			}
		}
	}
	if d := r.env.Store.QueryCount() - before; d != 0 {
		t.Fatalf("malformed bearer headers cost %d queries", d)
	}
}

// An Authorization header present but empty is still "bearer only".
func TestEmptyAuthorizationHeaderStillBearerOnly(t *testing.T) {
	r := newRig(t)
	value, _ := r.newSession(t)
	req := request(nil, r.cookie(value))
	req.Header["Authorization"] = []string{""}
	_, err := r.rv.Resolve(req)
	if ae := asAuthError(t, err); !ae.Bearer || ae.ClearCookie {
		t.Fatalf("err = %+v", ae)
	}
}

func TestBearerNeverFallsBackToCookie(t *testing.T) {
	r := newRig(t)
	value, hash := r.newSession(t)
	unknownSecret, _, _ := auth.Generate()

	// unknown token + valid cookie: 401, cookie neither used nor cleared
	before := r.env.Store.QueryCount()
	_, err := r.rv.Resolve(request(bearer(unknownSecret), r.cookie(value)))
	ae := asAuthError(t, err)
	if !ae.Bearer || ae.ClearCookie {
		t.Fatalf("unknown token: %+v", ae)
	}
	if d := r.env.Store.QueryCount() - before; d != 1 {
		t.Fatalf("unknown token statements = %d (one token lookup, no session lookup)", d)
	}
	// the session is untouched and still works by itself
	if _, err := r.rv.Resolve(request(nil, r.cookie(value))); err != nil {
		t.Fatalf("cookie alone: %v", err)
	}

	// revoked token + valid cookie: same
	id, secret := r.env.NewTokenWithSecret(t, r.user.ID, "", "")
	if err := r.env.Svc.Tokens.Revoke(ctx, r.env.UserActor(r.user), id); err != nil {
		t.Fatal(err)
	}
	_, err = r.rv.Resolve(request(bearer(secret), r.cookie(value)))
	if ae := asAuthError(t, err); !ae.Bearer || ae.ClearCookie {
		t.Fatalf("revoked token: %+v", ae)
	}

	// valid token + valid cookie of the same user: the token wins
	_, good := r.env.NewTokenWithSecret(t, r.user.ID, service.ScopeRead, "")
	p, err := r.rv.Resolve(request(bearer(good), r.cookie(value)))
	if err != nil || p.Method != auth.MethodToken || p.Session != nil {
		t.Fatalf("both present: %+v %v", p, err)
	}
	// ... even when the cookie belongs to somebody else (the token's user is reported)
	other := r.env.NewUser(t, "b@x.io", "Bob")
	otherValue, _, _ := r.sess.Create(ctx, other.ID)
	p, err = r.rv.Resolve(request(bearer(good), r.cookie(otherValue)))
	if err != nil || p.User.ID != r.user.ID {
		t.Fatalf("token user must win: %+v %v", p, err)
	}
	_ = hash

	// ResolveBearer never reads cookies at all
	if _, err := r.rv.ResolveBearer(request(nil, r.cookie(value))); !errors.Is(err, auth.ErrNoCredentials) {
		t.Fatalf("ResolveBearer used a cookie: %v", err)
	}
}

func TestRevokedAndUnknownTokensAreIndistinguishable(t *testing.T) {
	r := newRig(t)
	id, secret := r.env.NewTokenWithSecret(t, r.user.ID, "", "")
	if _, err := r.rv.ResolveBearer(request(bearer(secret))); err != nil {
		t.Fatal(err)
	}
	if err := r.env.Svc.Tokens.Revoke(ctx, r.env.UserActor(r.user), id); err != nil {
		t.Fatal(err)
	}
	unknown, _, _ := auth.Generate()
	_, revErr := r.rv.ResolveBearer(request(bearer(secret)))
	_, unkErr := r.rv.ResolveBearer(request(bearer(unknown)))
	a, b := asAuthError(t, revErr), asAuthError(t, unkErr)
	if *a != *b || revErr.Error() != unkErr.Error() {
		t.Fatalf("revoked %+v / unknown %+v differ", a, b)
	}
}

func TestTokenLastUsedThrottle(t *testing.T) {
	r := newRig(t)
	id, secret := r.env.NewTokenWithSecret(t, r.user.ID, "", "")
	lastUsed := func() string {
		row, err := r.env.Store.Read().GetAPIToken(ctx, id)
		if err != nil || row.LastUsedAt == nil {
			return ""
		}
		return *row.LastUsedAt
	}
	resolve := func() int64 {
		before := r.env.Store.QueryCount()
		if _, err := r.rv.ResolveBearer(request(bearer(secret))); err != nil {
			t.Fatal(err)
		}
		return r.env.Store.QueryCount() - before
	}
	if n := resolve(); n != 2 || lastUsed() != store.FormatTime(r.env.Clock.Now()) {
		t.Fatalf("first use: statements=%d last_used=%q", n, lastUsed())
	}
	first := lastUsed()
	r.env.Clock.Advance(59 * time.Second)
	if n := resolve(); n != 1 || lastUsed() != first {
		t.Fatalf("within 60s: statements=%d last_used=%q", n, lastUsed())
	}
	r.env.Clock.Advance(2 * time.Second) // 61 s since last write
	if n := resolve(); n != 2 || lastUsed() == first {
		t.Fatalf("after 60s: statements=%d last_used=%q", n, lastUsed())
	}
}

// A failed last_used_at write is logged and ignored: authentication still succeeds.
func TestTokenTouchFailureIgnored(t *testing.T) {
	r := newRig(t)
	_, secret := r.env.NewTokenWithSecret(t, r.user.ID, "", "")
	if err := r.env.Store.Exec(ctx, `CREATE TRIGGER no_touch BEFORE UPDATE OF last_used_at ON api_tokens BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	p, err := r.rv.ResolveBearer(request(bearer(secret)))
	if err != nil || p.Method != auth.MethodToken {
		t.Fatalf("resolve = %+v %v", p, err)
	}
}

func TestResolveDeletedUserAfterRevokedSessionsCascade(t *testing.T) {
	r := newRig(t)
	value, _ := r.newSession(t)
	_, secret := r.env.NewTokenWithSecret(t, r.user.ID, "", "")
	if err := r.env.Store.Exec(ctx, "DELETE FROM users WHERE id = ?", r.user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.rv.Resolve(request(nil, r.cookie(value))); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("session of deleted user: %v", err)
	}
	if _, err := r.rv.Resolve(request(bearer(secret))); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("token of deleted user: %v", err)
	}
}

func TestPasswordChangeKeepsOnlyCurrentSession(t *testing.T) {
	r := newRig(t)
	keep, keepHash := r.newSession(t)
	other, _ := r.newSession(t)
	if err := r.env.Svc.Users.SetPassword(ctx, r.user.ID, "newhash", keepHash); err != nil {
		t.Fatal(err)
	}
	if _, err := r.rv.Resolve(request(nil, r.cookie(keep))); err != nil {
		t.Fatalf("kept session: %v", err)
	}
	if _, err := r.rv.Resolve(request(nil, r.cookie(other))); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("other session survived: %v", err)
	}
}

func TestPrincipalContextHelpers(t *testing.T) {
	if _, ok := auth.PrincipalFrom(ctx); ok {
		t.Fatal("empty context has a principal")
	}
	p := auth.Principal{Method: auth.MethodSession, User: auth.AuthUser{ID: "U"}}
	got, ok := auth.PrincipalFrom(auth.WithPrincipal(ctx, p))
	if !ok || got.User.ID != "U" {
		t.Fatalf("got %+v %v", got, ok)
	}
}

// Principal must only ever build actors through the service constructors, never by hand.
func TestPrincipalNeverBuildsActorByHand(t *testing.T) {
	src, err := os.ReadFile("principal.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "service.Actor{") {
		t.Fatal("principal.go constructs service.Actor by hand; use UserActor/TokenActor")
	}
	if !strings.Contains(string(src), "service.UserActor(") || !strings.Contains(string(src), "service.TokenActor(") {
		t.Fatal("principal.go must use the service constructors")
	}
}

func TestPrincipalActorTable(t *testing.T) {
	p := auth.Principal{Method: auth.MethodToken, User: auth.AuthUser{ID: "U1"},
		Token: &auth.TokenInfo{ID: "T1", Scope: service.ScopeRead, ProjectID: "P1"}}
	a := p.Actor()
	if a.Type != service.ActorAPIToken || a.ID != "T1" || a.UserID != "U1" || a.Scope != service.ScopeRead || a.ProjectID != "P1" {
		t.Fatalf("token actor = %+v", a)
	}
	s := auth.Principal{Method: auth.MethodSession, User: auth.AuthUser{ID: "U1"}}.Actor()
	if s.Type != service.ActorUser || s.ID != "U1" || s.UserID != "U1" || s.ProjectID != "" || s.IsToken() {
		t.Fatalf("session actor = %+v", s)
	}
}
