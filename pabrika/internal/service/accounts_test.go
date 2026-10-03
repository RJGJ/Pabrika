package service_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func asErr(t *testing.T, err error) *service.Error {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) {
		t.Fatalf("want *service.Error, got %T %v", err, err)
	}
	return se
}

func TestUsersCreateNormalisesEmail(t *testing.T) {
	env := testutil.NewTestServices(t)
	u, err := env.Svc.Users.Create(ctx, "  Alice@Example.COM ", "  Alice  ", "hash1")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.com" || u.DisplayName != "Alice" || u.ID == "" || u.CreatedAt.IsZero() {
		t.Fatalf("user = %+v", u)
	}
	id, hash, err := env.Svc.Users.Credentials(ctx, "ALICE@example.com ")
	if err != nil || id != u.ID || hash != "hash1" {
		t.Fatalf("credentials = %q %q %v", id, hash, err)
	}
	h, err := env.Svc.Users.PasswordHash(ctx, u.ID)
	if err != nil || h != "hash1" {
		t.Fatalf("PasswordHash = %q %v", h, err)
	}
}

func TestUsersCreateDuplicateIsEmailTaken(t *testing.T) {
	env := testutil.NewTestServices(t)
	if _, err := env.Svc.Users.Create(ctx, "a@x.io", "A", "h"); err != nil {
		t.Fatal(err)
	}
	_, err := env.Svc.Users.Create(ctx, " A@X.IO", "Other", "h")
	se := asErr(t, err)
	if se.Kind != service.KindConflict || se.Code != "email_taken" || !errors.Is(err, service.ErrConflict) {
		t.Fatalf("err = %+v", se)
	}
}

func TestUsersCreateValidationReportsAllFields(t *testing.T) {
	env := testutil.NewTestServices(t)
	cases := []struct {
		name, email, display string
		fields               []string
	}{
		{"both bad", "nope", "   ", []string{"email", "display_name"}},
		{"email empty", "", "Ok", []string{"email"}},
		{"two at signs", "a@b@c.io", "Ok", []string{"email"}},
		{"no dot in domain", "a@localhost", "Ok", []string{"email"}},
		{"space", "a b@x.io", "Ok", []string{"email"}},
		{"email too long", strings.Repeat("a", 250) + "@x.io", "Ok", []string{"email"}},
		{"display too long", "a@x.io", strings.Repeat("é", 101), []string{"display_name"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verr := env.Svc.Users.ValidateNew(c.email, c.display)
			if verr == nil || verr.Kind != service.KindValidation {
				t.Fatalf("ValidateNew = %v", verr)
			}
			for _, f := range c.fields {
				if verr.Fields[f] == "" {
					t.Errorf("missing field %q in %v", f, verr.Fields)
				}
			}
			if len(verr.Fields) != len(c.fields) {
				t.Errorf("fields = %v, want exactly %v", verr.Fields, c.fields)
			}
			_, err := env.Svc.Users.Create(ctx, c.email, c.display, "h")
			if se := asErr(t, err); se.Kind != service.KindValidation {
				t.Errorf("Create err = %+v", se)
			}
		})
	}
	// boundaries are valid: 100 runes, 254 bytes of email
	if v := env.Svc.Users.ValidateNew("a@x.io", strings.Repeat("é", 100)); v != nil {
		t.Fatalf("100-rune name rejected: %v", v)
	}
	local := strings.Repeat("a", 254-len("@x.io"))
	if v := env.Svc.Users.ValidateNew(local+"@x.io", "Ok"); v != nil {
		t.Fatalf("254-char email rejected: %v", v)
	}
}

func TestUsersCreateDoesNoDBWorkWhenInvalid(t *testing.T) {
	env := testutil.NewTestServices(t)
	before := env.Store.QueryCount()
	if v := env.Svc.Users.ValidateNew("x@y.io", "Name"); v != nil {
		t.Fatal(v)
	}
	if _, err := env.Svc.Users.Create(ctx, "bad", "Name", "h"); err == nil {
		t.Fatal("want error")
	}
	if d := env.Store.QueryCount() - before; d != 0 {
		t.Fatalf("validation issued %d queries", d)
	}
}

func TestUsersCreateConcurrentRaceExactlyOneWinner(t *testing.T) {
	env := testutil.NewFileEnv(t)
	const n = 12
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, taken := 0, 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.Svc.Users.Create(ctx, "race@x.io", "Racer", "h")
			mu.Lock()
			defer mu.Unlock()
			var se *service.Error
			switch {
			case err == nil:
				wins++
			case errors.As(err, &se) && se.Code == "email_taken":
				taken++
			default:
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || taken != n-1 {
		t.Fatalf("wins=%d taken=%d", wins, taken)
	}
}

func TestUsersCredentialsUnknownIsNotFound(t *testing.T) {
	env := testutil.NewTestServices(t)
	if _, _, err := env.Svc.Users.Credentials(ctx, "ghost@x.io"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := env.Svc.Users.PasswordHash(ctx, "01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func sessionHashes(t *testing.T, env *testutil.Env, userID string) []string {
	t.Helper()
	hs, err := env.Store.Read().ListSessionHashesForUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestUsersSetPasswordKeepsOnlyNamedSession(t *testing.T) {
	env := testutil.NewTestServices(t)
	u := env.NewUser(t, "a@x.io", "A")
	other := env.NewUser(t, "b@x.io", "B")
	mk := func(uid, hash string) {
		if err := env.Store.Exec(ctx, "INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?,?,?,?)",
			hash, uid, "2030-01-01T00:00:00.000Z", "2026-01-01T00:00:00.000Z"); err != nil {
			t.Fatal(err)
		}
	}
	mk(u.ID, "h1")
	mk(u.ID, "h2")
	mk(u.ID, "h3")
	mk(other.ID, "o1")

	if err := env.Svc.Users.SetPassword(ctx, u.ID, "newhash", "h2"); err != nil {
		t.Fatal(err)
	}
	if got := sessionHashes(t, env, u.ID); len(got) != 1 || got[0] != "h2" {
		t.Fatalf("sessions after change = %v", got)
	}
	if got := sessionHashes(t, env, other.ID); len(got) != 1 {
		t.Fatalf("other user's sessions touched: %v", got)
	}
	if h, _ := env.Svc.Users.PasswordHash(ctx, u.ID); h != "newhash" {
		t.Fatalf("hash = %q", h)
	}

	// CLI reset: empty keep deletes all of that user's sessions only
	if err := env.Svc.Users.SetPassword(ctx, u.ID, "reset", ""); err != nil {
		t.Fatal(err)
	}
	if got := sessionHashes(t, env, u.ID); len(got) != 0 {
		t.Fatalf("sessions after reset = %v", got)
	}
	if got := sessionHashes(t, env, other.ID); len(got) != 1 {
		t.Fatalf("other user's sessions touched by reset: %v", got)
	}
}

func TestUsersSetPasswordUnknownUserIsNotFoundAndKeepsSessions(t *testing.T) {
	env := testutil.NewTestServices(t)
	err := env.Svc.Users.SetPassword(ctx, "01ARZ3NDEKTSV4RRFFQ69G5FAV", "x", "")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
