package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
)

// again returns e with new stdin and fresh output buffers (same database).
func again(e env, stdin string) (env, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	e.stdin, e.stdout, e.stderr = strings.NewReader(stdin), out, errb
	return e, out, errb
}

// openDB reopens the CLI's database for assertions.
func openDB(t *testing.T, e env) (*store.Store, *service.Services) {
	t.Helper()
	st, svc, err := openServices(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, svc
}

func verify(t *testing.T, svc *service.Services, email, password string) bool {
	t.Helper()
	_, hash, err := svc.Users.Credentials(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := auth.NewHasher(auth.TestParams, 1).Verify(context.Background(), password, hash)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestUserCreateOnFreshDatabase(t *testing.T) {
	// No serve has ever run: the DB file (and its directory) does not exist yet.
	e, out, errb, _ := testEnv(t, "a-very-long-password\n", map[string]string{"ALLOW_SIGNUP": "false"})
	code := run(context.Background(), []string{"user", "create", "--email", " Ann@Example.COM ", "--name", "Ann", "--password-stdin"}, e)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(out.String(), "ann@example.com") {
		t.Fatalf("stdout %q", out)
	}
	_, svc := openDB(t, e)
	if !verify(t, svc, "ann@example.com", "a-very-long-password") || verify(t, svc, "ann@example.com", "wrong-password-xx") {
		t.Fatal("created user must be loginable with the given password")
	}
	if strings.Contains(out.String()+errb.String(), "a-very-long-password") {
		t.Fatal("password echoed")
	}
}

func TestUserCreateReadsOneLine(t *testing.T) {
	for _, in := range []string{"a-very-long-password", "a-very-long-password\r\n", "a-very-long-password\nsecond line ignored\n"} {
		e, _, errb, _ := testEnv(t, in, nil)
		if code := run(context.Background(), []string{"user", "create", "--email", "a@b.io", "--name", "A", "--password-stdin"}, e); code != 0 {
			t.Fatalf("%q: exit %d: %s", in, code, errb)
		}
		_, svc := openDB(t, e)
		if !verify(t, svc, "a@b.io", "a-very-long-password") {
			t.Fatalf("%q: password mismatch", in)
		}
	}
}

func TestUserCreateFailures(t *testing.T) {
	ctx := context.Background()

	// Bad email and short password: exit 1, all problems reported together.
	e, _, errb, _ := testEnv(t, "short\n", nil)
	if code := run(ctx, []string{"user", "create", "--email", "not-an-email", "--name", "A", "--password-stdin"}, e); code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"email", "password"} {
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("stderr missing %q: %s", want, errb)
		}
	}
	if strings.Contains(errb.String(), "short") {
		t.Fatal("password echoed in an error")
	}

	// Duplicate email, case-insensitive: exit 1 with a clear message.
	e, _, _, _ = testEnv(t, "a-very-long-password\n", nil)
	if code := run(ctx, []string{"user", "create", "--email", "dup@x.io", "--name", "A", "--password-stdin"}, e); code != 0 {
		t.Fatal(code)
	}
	e2, _, errb := again(e, "another-long-password\n")
	if code := run(ctx, []string{"user", "create", "--email", "DUP@x.io", "--name", "B", "--password-stdin"}, e2); code != 1 || !strings.Contains(errb.String(), "already exists") {
		t.Fatalf("duplicate: %d %s", code, errb)
	}

	// No flag and stdin is not a terminal: fails instead of hanging or reading silently.
	e3, _, errb := again(e, "a-very-long-password\n")
	if code := run(ctx, []string{"user", "create", "--email", "n@x.io", "--name", "N"}, e3); code != 1 || !strings.Contains(errb.String(), "--password-stdin") {
		t.Fatalf("no tty: %d %s", code, errb)
	}

	// Empty stdin.
	e4, _, _ := again(e, "")
	if code := run(ctx, []string{"user", "create", "--email", "n@x.io", "--name", "N", "--password-stdin"}, e4); code != 1 {
		t.Fatalf("empty stdin: %d", code)
	}
}

func TestUserCreateUsageErrors(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := testEnv(t, "a-very-long-password\n", nil)
	for _, args := range [][]string{
		{"user", "create", "--name", "A", "--password-stdin"},                               // missing email
		{"user", "create", "--email", "a@b.io", "--password-stdin"},                         // missing name
		{"user", "create", "--email", "a@b.io", "--name", "A", "stray", "--password-stdin"}, // positional
		{"user", "create", "--email", "a@b.io", "--name", "A", "--password", "x"},           // password is never an argument
		{"user", "create", "--bogus"},
	} {
		ee, _, _ := again(e, "a-very-long-password\n")
		if code := run(ctx, args, ee); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestResetPassword(t *testing.T) {
	ctx := context.Background()
	e, _, errb, _ := testEnv(t, "original-password-1\n", nil)
	if code := run(ctx, []string{"user", "create", "--email", "r@x.io", "--name", "R", "--password-stdin"}, e); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	// A live session and an API token, both of which exist before the reset.
	st, svc := openDB(t, e)
	u, _, _ := svc.Users.Credentials(ctx, "r@x.io")
	other, err := svc.Users.Create(ctx, "other@x.io", "Other", "placeholder")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(st, auth.SessionOptions{})
	value, _, err := sessions.Create(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	otherValue, _, err := sessions.Create(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	secret, hash, prefix := auth.Generate()
	if _, err := svc.Tokens.Create(ctx, service.UserActor(u), service.CreateTokenInput{Name: "ci", Scope: service.ScopeWrite, Hash: hash, Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	e2, out, errb := again(e, "brand-new-password-2\n")
	if code := run(ctx, []string{"user", "reset-password", "--password-stdin", "R@X.io"}, e2); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if strings.Contains(out.String()+errb.String(), "brand-new-password-2") {
		t.Fatal("password echoed")
	}

	st, svc = openDB(t, e)
	if !verify(t, svc, "r@x.io", "brand-new-password-2") || verify(t, svc, "r@x.io", "original-password-1") {
		t.Fatal("password not changed")
	}
	sessions = auth.NewSessions(st, auth.SessionOptions{})
	if err := sessions.Validate(ctx, auth.HashSessionValue(value)); err == nil {
		t.Fatal("the user's session must be revoked")
	}
	if err := sessions.Validate(ctx, auth.HashSessionValue(otherValue)); err != nil {
		t.Fatalf("another user's session must survive: %v", err)
	}
	// The API token still works.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rv := auth.NewResolver(auth.ResolverDeps{Store: st, Sessions: sessions})
	if p, err := rv.ResolveBearer(req); err != nil || p.User.ID != u {
		t.Fatalf("token must not be revoked: %v", err)
	}
}

func TestResetPasswordErrors(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := testEnv(t, "original-password-1\n", nil)
	if code := run(ctx, []string{"user", "create", "--email", "r@x.io", "--name", "R", "--password-stdin"}, e); code != 0 {
		t.Fatal(code)
	}

	// Flag after the positional: usage error (documented: flags first).
	ee, _, errb := again(e, "brand-new-password-2\n")
	if code := run(ctx, []string{"user", "reset-password", "r@x.io", "--password-stdin"}, ee); code != 2 {
		t.Fatalf("flag after positional: exit %d (%s)", code, errb)
	}
	_, svc := openDB(t, e)
	if !verify(t, svc, "r@x.io", "original-password-1") {
		t.Fatal("a usage error must change nothing")
	}
	// Missing or extra positional.
	for _, args := range [][]string{{"user", "reset-password", "--password-stdin"}, {"user", "reset-password", "--password-stdin", "a@x.io", "b@x.io"}} {
		ee, _, _ := again(e, "brand-new-password-2\n")
		if code := run(ctx, args, ee); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	// Unknown email.
	ee, _, errb = again(e, "brand-new-password-2\n")
	if code := run(ctx, []string{"user", "reset-password", "--password-stdin", "nobody@x.io"}, ee); code != 1 || !strings.Contains(errb.String(), "no account") {
		t.Fatalf("unknown email: %d %s", code, errb)
	}
	// Short password.
	ee, _, errb = again(e, "short\n")
	if code := run(ctx, []string{"user", "reset-password", "--password-stdin", "r@x.io"}, ee); code != 1 || !strings.Contains(errb.String(), "password") {
		t.Fatalf("short: %d %s", code, errb)
	}
	if !verify(t, svc, "r@x.io", "original-password-1") {
		t.Fatal("a rejected password must change nothing")
	}
	// Works on a fresh DB path too: it migrates first (unknown email is the clean failure).
	fresh, _, errb, _ := testEnv(t, "brand-new-password-2\n", nil)
	if code := run(ctx, []string{"user", "reset-password", "--password-stdin", "x@x.io"}, fresh); code != 1 || strings.Contains(errb.String(), "no such table") {
		t.Fatalf("fresh db: %d %s", code, errb)
	}
}
