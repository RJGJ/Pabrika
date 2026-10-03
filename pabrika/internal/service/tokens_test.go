package service_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func tokenIn(name string, scope service.Scope, project string, n int) service.CreateTokenInput {
	hash := fmt.Sprintf("%064d", n)
	return service.CreateTokenInput{Name: name, Scope: scope, ProjectRef: project, Hash: hash, Prefix: "pb_" + hash[:5]}
}

func TestTokensCreateListRevoke(t *testing.T) {
	env := testutil.NewTestServices(t)
	owner := env.NewUser(t, "o@x.io", "O")
	p := env.NewProject(t, owner, "WEB")
	actor := env.UserActor(owner)

	tok, err := env.Svc.Tokens.Create(ctx, actor, tokenIn("  ci  ", service.ScopeRead, "web", 1))
	if err != nil {
		t.Fatal(err)
	}
	if tok.ID == "" || tok.Name != "ci" || tok.Scope != service.ScopeRead || tok.Prefix != "pb_00000" ||
		tok.Project == nil || tok.Project.ID != p.ID || tok.Project.Key != "WEB" || tok.RevokedAt != nil || tok.LastUsedAt != nil {
		t.Fatalf("token = %+v", tok)
	}
	env.Clock.Advance(time.Minute)
	all, err := env.Svc.Tokens.Create(ctx, actor, tokenIn("all", service.ScopeWrite, "", 2))
	if err != nil || all.Project != nil {
		t.Fatalf("all = %+v %v", all, err)
	}

	list, err := env.Svc.Tokens.List(ctx, actor)
	if err != nil || len(list) != 2 || list[0].ID != all.ID || list[1].ID != tok.ID {
		t.Fatalf("list (newest first) = %+v %v", list, err)
	}

	env.Clock.Advance(time.Minute)
	if err := env.Svc.Tokens.Revoke(ctx, actor, tok.ID); err != nil {
		t.Fatal(err)
	}
	first, _ := env.Svc.Tokens.List(ctx, actor)
	var revokedAt *time.Time
	for _, x := range first {
		if x.ID == tok.ID {
			revokedAt = x.RevokedAt
		}
	}
	if revokedAt == nil {
		t.Fatal("revoked token missing revoked_at in list")
	}
	// idempotent: second revoke succeeds and keeps the original timestamp
	env.Clock.Advance(time.Hour)
	if err := env.Svc.Tokens.Revoke(ctx, actor, tok.ID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	second, _ := env.Svc.Tokens.List(ctx, actor)
	for _, x := range second {
		if x.ID == tok.ID && !x.RevokedAt.Equal(*revokedAt) {
			t.Fatalf("revoked_at changed: %v -> %v", revokedAt, x.RevokedAt)
		}
	}
}

func TestTokensListIsOwnOnlyAndRevokeOthersIsNotFound(t *testing.T) {
	env := testutil.NewTestServices(t)
	a := env.NewUser(t, "a@x.io", "A")
	b := env.NewUser(t, "b@x.io", "B")
	ta, err := env.Svc.Tokens.Create(ctx, env.UserActor(a), tokenIn("a", service.ScopeWrite, "", 1))
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := env.Svc.Tokens.List(ctx, env.UserActor(b)); len(l) != 0 {
		t.Fatalf("b sees %v", l)
	}
	if err := env.Svc.Tokens.Revoke(ctx, env.UserActor(b), ta.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("revoke other's = %v", err)
	}
	for _, id := range []string{"nonsense", "01ARZ3NDEKTSV4RRFFQ69G5FAV", strings.ToLower(ta.ID) + "x"} {
		if err := env.Svc.Tokens.Revoke(ctx, env.UserActor(a), id); !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("revoke %q = %v", id, err)
		}
	}
	// lowercase ULID of an own token is accepted
	if err := env.Svc.Tokens.Revoke(ctx, env.UserActor(a), strings.ToLower(ta.ID)); err != nil {
		t.Fatalf("lowercase id: %v", err)
	}
	// the other user's token was not revoked by b's attempt (a revoked it last): sanity via DB
	row, _ := env.Store.Read().GetAPIToken(ctx, ta.ID)
	if row.RevokedAt == nil {
		t.Fatal("expected revoked")
	}
}

func TestTokensCreateProjectRules(t *testing.T) {
	env := testutil.NewTestServices(t)
	owner := env.NewUser(t, "o@x.io", "O")
	viewer := env.NewUser(t, "v@x.io", "V")
	stranger := env.NewUser(t, "s@x.io", "S")
	p := env.NewProject(t, owner, "WEB")
	env.AddMember(t, p.ID, viewer.ID, service.RoleViewer)

	// any role may scope a token to the project, by key or by ULID
	if _, err := env.Svc.Tokens.Create(ctx, env.UserActor(viewer), tokenIn("v", service.ScopeRead, "WEB", 1)); err != nil {
		t.Fatalf("viewer: %v", err)
	}
	tok, err := env.Svc.Tokens.Create(ctx, env.UserActor(owner), tokenIn("o", service.ScopeWrite, p.ID, 2))
	if err != nil || tok.Project.Key != "WEB" {
		t.Fatalf("by ulid: %+v %v", tok, err)
	}
	// non-member and unknown projects are the same 404, even with an invalid body
	bad := tokenIn("", "nope", "WEB", 3)
	for _, ref := range []string{"WEB", p.ID, "NOPE"} {
		in := bad
		in.ProjectRef = ref
		if _, err := env.Svc.Tokens.Create(ctx, env.UserActor(stranger), in); !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("stranger ref %q err = %v", ref, err)
		}
	}
	// archived project is still allowed (read-only data, token can still read)
	env.Archive(t, p.ID)
	if _, err := env.Svc.Tokens.Create(ctx, env.UserActor(owner), tokenIn("arch", service.ScopeRead, "WEB", 4)); err != nil {
		t.Fatalf("archived: %v", err)
	}
	// deleting the project cascades to its limited tokens
	if err := env.Store.Exec(ctx, "DELETE FROM projects WHERE id = ?", p.ID); err != nil {
		t.Fatal(err)
	}
	l, _ := env.Svc.Tokens.List(ctx, env.UserActor(owner))
	if len(l) != 0 {
		t.Fatalf("limited tokens survived project delete: %+v", l)
	}
}

func TestTokensCreateValidation(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "o@x.io", "O")
	actor := env.UserActor(u)
	cases := []struct {
		name   string
		in     service.CreateTokenInput
		fields []string
	}{
		{"empty name", tokenIn("  ", service.ScopeRead, "", 1), []string{"name"}},
		{"long name", tokenIn(strings.Repeat("n", 101), service.ScopeRead, "", 1), []string{"name"}},
		{"bad scope", tokenIn("x", "admin", "", 1), []string{"scope"}},
		{"empty scope", tokenIn("x", "", "", 1), []string{"scope"}},
		{"both", tokenIn("", "", "", 1), []string{"name", "scope"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := env.Svc.Tokens.Create(ctx, actor, c.in)
			se := asErr(t, err)
			if se.Kind != service.KindValidation || len(se.Fields) != len(c.fields) {
				t.Fatalf("err = %+v", se)
			}
			for _, f := range c.fields {
				if se.Fields[f] == "" {
					t.Fatalf("missing %s: %v", f, se.Fields)
				}
			}
		})
	}
	if _, err := env.Svc.Tokens.Create(ctx, actor, tokenIn(strings.Repeat("é", 100), service.ScopeRead, "", 9)); err != nil {
		t.Fatalf("100-rune name: %v", err)
	}
}

func TestTokensActiveCap(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "o@x.io", "O")
	other := env.NewUser(t, "p@x.io", "P")
	actor := env.UserActor(u)
	var ids []string
	for i := 0; i < service.MaxActiveTokens; i++ {
		tok, err := env.Svc.Tokens.Create(ctx, actor, tokenIn(fmt.Sprintf("t%d", i), service.ScopeRead, "", i))
		if err != nil {
			t.Fatalf("token %d: %v", i, err)
		}
		ids = append(ids, tok.ID)
	}
	_, err := env.Svc.Tokens.Create(ctx, actor, tokenIn("one too many", service.ScopeRead, "", 1000))
	se := asErr(t, err)
	if se.Kind != service.KindValidation || se.Fields["name"] != "Too many active tokens; revoke one first" {
		t.Fatalf("101st: %+v", se)
	}
	// another user is unaffected
	if _, err := env.Svc.Tokens.Create(ctx, env.UserActor(other), tokenIn("fine", service.ScopeRead, "", 2000)); err != nil {
		t.Fatal(err)
	}
	// revoked tokens do not count
	if err := env.Svc.Tokens.Revoke(ctx, actor, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.Tokens.Create(ctx, actor, tokenIn("replacement", service.ScopeRead, "", 1001)); err != nil {
		t.Fatalf("after revoke: %v", err)
	}
	if _, err := env.Svc.Tokens.Create(ctx, actor, tokenIn("again", service.ScopeRead, "", 1002)); err == nil {
		t.Fatal("cap not enforced again")
	}
	if l, _ := env.Svc.Tokens.List(ctx, actor); len(l) != service.MaxActiveTokens+1 {
		t.Fatalf("list keeps revoked: %d", len(l))
	}
}

func TestTokensTokenActorGetsSessionRequiredFirst(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "o@x.io", "O")
	env.NewProject(t, u, "WEB")
	tokID := env.NewToken(t, u.ID, service.ScopeWrite, "")
	ta := env.TokenActor(tokID)

	before := env.Store.QueryCount()
	// the body is invalid and the project unknown: session_required must still win, with no queries
	_, err := env.Svc.Tokens.Create(ctx, ta, tokenIn("", "bogus", "NOPE", 1))
	check := func(name string, err error) {
		t.Helper()
		se := asErr(t, err)
		if se.Kind != service.KindForbidden || se.Code != service.CodeSessionRequired {
			t.Fatalf("%s: %+v", name, se)
		}
	}
	check("create", err)
	_, err = env.Svc.Tokens.List(ctx, ta)
	check("list", err)
	check("revoke", env.Svc.Tokens.Revoke(ctx, ta, tokID))
	if d := env.Store.QueryCount() - before; d != 0 {
		t.Fatalf("token actor caused %d queries", d)
	}
	// users' session-only operations too
	_, err = env.Svc.Users.UpdateProfile(ctx, ta, "X")
	check("profile", err)
}

func TestTokensCreatedRowHoldsOnlyHash(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "o@x.io", "O")
	in := tokenIn("x", service.ScopeWrite, "", 7)
	tok, err := env.Svc.Tokens.Create(ctx, env.UserActor(u), in)
	if err != nil {
		t.Fatal(err)
	}
	row, err := env.Store.Read().GetAPIToken(ctx, tok.ID)
	if err != nil || row.TokenHash != in.Hash || row.TokenPrefix != in.Prefix || row.CreatedAt != store.FormatTime(env.Clock.Now()) {
		t.Fatalf("row = %+v %v", row, err)
	}
}

func TestTestutilNewTokenWithSecretAuthenticates(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "o@x.io", "O")
	id, secret := env.NewTokenWithSecret(t, u.ID, service.ScopeRead, "")
	if !strings.HasPrefix(secret, "pb_") || len(secret) != 67 {
		t.Fatalf("secret = %q", secret)
	}
	row, _ := env.Store.Read().GetAPIToken(ctx, id)
	if row.TokenHash == secret || len(row.TokenHash) != 64 || row.TokenPrefix != secret[:8] {
		t.Fatalf("row = %+v", row)
	}
}
