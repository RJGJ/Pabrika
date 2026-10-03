package testutil

import (
	"errors"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
)

// Who is a role in the permission matrix.
type Who string

const (
	NonMember Who = "non-member"
	Viewer    Who = "viewer"
	Editor    Who = "editor"
	Owner     Who = "owner"
)

// Who lists every row of the matrix in order.
var AllWho = []Who{NonMember, Viewer, Editor, Owner}

// Outcome is an expected result of a service call.
type Outcome struct {
	OK   bool
	Kind service.Kind // for failures
	Code string       // for failures; "" matches any code of that Kind
}

// Expected outcomes.
var (
	OK       = Outcome{OK: true}
	NotFound = Outcome{Kind: service.KindNotFound, Code: service.CodeNotFound}
)

// Forbidden expects ErrForbidden with the given code (`forbidden`, `not_author`, `insufficient_scope`, ...).
func Forbidden(code string) Outcome { return Outcome{Kind: service.KindForbidden, Code: code} }

// Conflict expects ErrConflict with the given code.
func Conflict(code string) Outcome { return Outcome{Kind: service.KindConflict, Code: code} }

// Invalid expects a validation error.
func Invalid() Outcome { return Outcome{Kind: service.KindValidation} }

// Matrix is a project with one user per role (plus a non-member) so a single operation can be
// run as every {role} x {session, write token} combination.
type Matrix struct {
	Env     *Env
	Project Project
	Users   map[Who]User
}

// NewMatrix seeds a project (key given) with an owner, an editor, a viewer and a non-member.
func (e *Env) NewMatrix(t testing.TB, key string) *Matrix {
	t.Helper()
	m := &Matrix{Env: e, Users: map[Who]User{}}
	m.Users[Owner] = e.NewUser(t, "owner-"+key+"@example.com", "Owner "+key)
	m.Users[Editor] = e.NewUser(t, "editor-"+key+"@example.com", "Editor "+key)
	m.Users[Viewer] = e.NewUser(t, "viewer-"+key+"@example.com", "Viewer "+key)
	m.Users[NonMember] = e.NewUser(t, "outsider-"+key+"@example.com", "Outsider "+key)
	m.Project = e.NewProject(t, m.Users[Owner], key)
	e.AddMember(t, m.Project.ID, m.Users[Editor].ID, service.RoleEditor)
	e.AddMember(t, m.Project.ID, m.Users[Viewer].ID, service.RoleViewer)
	return m
}

// Actor returns the session actor (token=false) or a fresh write-scope, unlimited token actor
// (token=true) owned by the given role's user.
func (m *Matrix) Actor(t testing.TB, who Who, token bool) service.Actor {
	t.Helper()
	u := m.Users[who]
	if !token {
		return m.Env.UserActor(u)
	}
	return m.Env.TokenActor(m.Env.NewToken(t, u.ID, service.ScopeWrite, ""))
}

// MatrixCase is one operation under test.
type MatrixCase struct {
	Name string
	// Op performs the operation as the actor and returns its error.
	Op func(t *testing.T, a service.Actor) error
	// Want maps each role to the expected outcome for a SESSION actor. Missing roles are not run.
	Want map[Who]Outcome
	// TokenWant overrides the expectation for write-token actors per role (default: same as Want).
	TokenWant map[Who]Outcome
	// SessionOnly means every token actor must get Forbidden `session_required`, before any lookup.
	SessionOnly bool
	// Before runs before each (role, actor kind) run, e.g. to recreate state a previous run consumed.
	Before func(t *testing.T)
}

// Run executes the case for each role and for both session and write-token actors.
func (m *Matrix) Run(t *testing.T, c MatrixCase) {
	t.Helper()
	for _, who := range AllWho {
		want, ok := c.Want[who]
		if !ok {
			continue
		}
		for _, token := range []bool{false, true} {
			kind := "session"
			w := want
			if token {
				kind = "token"
				if tw, ok := c.TokenWant[who]; ok {
					w = tw
				}
				if c.SessionOnly {
					w = Forbidden(service.CodeSessionRequired)
				}
			}
			t.Run(c.Name+"/"+string(who)+"/"+kind, func(t *testing.T) {
				if c.Before != nil {
					c.Before(t)
				}
				err := c.Op(t, m.Actor(t, who, token))
				AssertOutcome(t, err, w)
			})
		}
	}
}

// AssertOutcome checks err against want.
func AssertOutcome(t testing.TB, err error, want Outcome) {
	t.Helper()
	if want.OK {
		if err != nil {
			t.Fatalf("want success, got %v", err)
		}
		return
	}
	var se *service.Error
	if !errors.As(err, &se) {
		t.Fatalf("want error kind %d code %q, got %v", want.Kind, want.Code, err)
	}
	if se.Kind != want.Kind || (want.Code != "" && se.Code != want.Code) {
		t.Fatalf("want kind %d code %q, got kind %d code %q (%v)", want.Kind, want.Code, se.Kind, se.Code, err)
	}
}
