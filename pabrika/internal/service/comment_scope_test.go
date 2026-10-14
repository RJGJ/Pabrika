package service_test

import (
	"errors"
	"sort"
	"testing"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

// commentScopeClasses classifies EVERY service method for a comment-scope token. A new method
// that is not listed here fails TestCommentScopeEveryMethodClassified, so it cannot be forgotten.
//
//	read     works for a comment token (reads)
//	comment  works for a comment token (Comments.Add and own Comments.Edit)
//	denied   a comment token gets insufficient_scope or session_required (every other write)
//	n/a      not an actor-scoped operation (account plumbing without a token path)
var commentScopeClasses = map[string]string{
	"ProjectService.Create": "denied", "ProjectService.Get": "read", "ProjectService.Resolve": "read",
	"ProjectService.List": "read", "ProjectService.Update": "denied", "ProjectService.Delete": "denied",
	"MemberService.List": "read", "MemberService.Add": "denied", "MemberService.SetRole": "denied", "MemberService.Remove": "denied",
	"LabelService.List": "read", "LabelService.Create": "denied", "LabelService.Update": "denied", "LabelService.Delete": "denied",
	"TicketService.Get": "read", "TicketService.List": "read", "TicketService.Resolve": "read",
	"TicketService.Create": "denied", "TicketService.Update": "denied", "TicketService.Move": "denied",
	"TicketService.ChangeLabels": "denied", "TicketService.Delete": "denied",
	"CommentService.List": "read", "CommentService.Latest": "read", "CommentService.Resolve": "read",
	"CommentService.Add": "comment", "CommentService.Edit": "comment", "CommentService.Delete": "denied",
	"ActivityService.List": "read", "ActivityService.ListProject": "read",
	"DecisionService.List": "read", "DecisionService.Record": "denied",
	"UserService.UpdateProfile": "denied",
	"UserService.Create":        "n/a", "UserService.ValidateNew": "n/a", "UserService.Credentials": "n/a",
	"UserService.PasswordHash": "n/a", "UserService.SetPassword": "n/a",
	"TokenService.Create": "denied", "TokenService.List": "denied", "TokenService.Revoke": "denied",
}

func TestCommentScopeEveryMethodClassified(t *testing.T) {
	var missing, stale []string
	methods := serviceMethods()
	for m := range methods {
		if commentScopeClasses[m] == "" {
			missing = append(missing, m)
		}
	}
	for m := range commentScopeClasses {
		if !methods[m] {
			stale = append(stale, m)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("classify new service methods for the comment scope.\nmissing: %v\nstale: %v", missing, stale)
	}
}

type csEnv struct {
	*tkEnv
	ownerCT  service.Actor // comment token of the project owner
	editorCT service.Actor // comment token of an editor
	viewerCT service.Actor // comment token of a viewer
	seed     service.Ticket
	label    string
}

func newCS(t *testing.T) *csEnv {
	e := newTk(t)
	mk := func(w testutil.Who) service.Actor {
		return e.TokenActor(e.NewToken(t, e.M.Users[w].ID, service.ScopeComment, ""))
	}
	return &csEnv{
		tkEnv: e, ownerCT: mk(testutil.Owner), editorCT: mk(testutil.Editor), viewerCT: mk(testutil.Viewer),
		seed: e.create(t, "seed", service.StatusTodo), label: e.label(t, e.P.ID, "bug"),
	}
}

func wantDenied(t *testing.T, name string, err error) {
	t.Helper()
	var se *service.Error
	if err == nil {
		t.Fatalf("%s: comment token succeeded", name)
	}
	if !errors.As(err, &se) || se.Kind != service.KindForbidden ||
		(se.Code != service.CodeInsufficientScope && se.Code != service.CodeSessionRequired) {
		t.Fatalf("%s: want insufficient_scope/session_required, got %v", name, err)
	}
}

func TestCommentScopeDeniesEveryOtherWrite(t *testing.T) {
	c := newCS(t)
	cm, err := c.Svc.Comments.Add(ctx, c.Owner, c.seed.Ref, "mine")
	if err != nil {
		t.Fatal(err)
	}
	a := c.ownerCT // an owner-role user: only the scope can be the reason for the denial
	denied := map[string]func() error{
		"ProjectService.Create": func() error {
			_, err := c.Svc.Projects.Create(ctx, a, service.CreateProjectInput{Key: "NEW", Name: "n"})
			return err
		},
		"ProjectService.Update": func() error {
			_, err := c.Svc.Projects.Update(ctx, a, "WEB", service.UpdateProjectInput{Name: service.Some("x")})
			return err
		},
		"ProjectService.Delete": func() error { return c.Svc.Projects.Delete(ctx, a, "WEB") },
		"MemberService.Add": func() error {
			_, err := c.Svc.Members.Add(ctx, a, "WEB", c.M.Users[testutil.NonMember].Email, service.RoleViewer)
			return err
		},
		"MemberService.SetRole": func() error {
			_, err := c.Svc.Members.SetRole(ctx, a, "WEB", c.M.Users[testutil.Viewer].ID, service.RoleEditor)
			return err
		},
		"MemberService.Remove": func() error { return c.Svc.Members.Remove(ctx, a, "WEB", c.M.Users[testutil.Viewer].ID) },
		"LabelService.Create": func() error {
			_, err := c.Svc.Labels.Create(ctx, a, "WEB", service.LabelInput{Name: "n"})
			return err
		},
		"LabelService.Update": func() error {
			_, err := c.Svc.Labels.Update(ctx, a, c.label, service.UpdateLabelInput{Name: service.Some("x")})
			return err
		},
		"LabelService.Delete": func() error { return c.Svc.Labels.Delete(ctx, a, c.label) },
		"TicketService.Create": func() error {
			_, err := c.Svc.Tickets.Create(ctx, a, "WEB", service.CreateTicketInput{Title: "x"})
			return err
		},
		"TicketService.Update": func() error {
			_, err := c.Svc.Tickets.Update(ctx, a, c.seed.Ref, service.UpdateTicketInput{Title: service.Some("x")})
			return err
		},
		"TicketService.Move": func() error {
			_, err := c.Svc.Tickets.Move(ctx, a, c.seed.Ref, service.MoveInput{Status: service.StatusDone})
			return err
		},
		"TicketService.ChangeLabels": func() error {
			_, err := c.Svc.Tickets.ChangeLabels(ctx, a, c.seed.Ref, service.ChangeLabelsInput{Add: []string{c.label}})
			return err
		},
		"TicketService.Delete":  func() error { return c.Svc.Tickets.Delete(ctx, a, c.seed.Ref) },
		"CommentService.Delete": func() error { return c.Svc.Comments.Delete(ctx, a, cm.ID) },
		"DecisionService.Record": func() error {
			_, err := c.Svc.Decisions.Record(ctx, a, c.seed.Ref, service.RecordDecisionInput{Gate: service.GatePlan, Verdict: service.VerdictApproved})
			return err
		},
		"UserService.UpdateProfile": func() error {
			_, err := c.Svc.Users.UpdateProfile(ctx, a, "Zed")
			return err
		},
		"TokenService.Create": func() error {
			_, err := c.Svc.Tokens.Create(ctx, a, service.CreateTokenInput{Name: "x", Scope: service.ScopeRead, Hash: "h", Prefix: "pb_x"})
			return err
		},
		"TokenService.List":   func() error { _, err := c.Svc.Tokens.List(ctx, a); return err },
		"TokenService.Revoke": func() error { return c.Svc.Tokens.Revoke(ctx, a, c.NewID()) },
	}
	for name, class := range commentScopeClasses {
		if class != "denied" {
			continue
		}
		op, ok := denied[name]
		if !ok {
			t.Errorf("no denial check for %s", name)
			continue
		}
		wantDenied(t, name, op())
	}
	// nothing leaked through: the seed ticket and its comment are untouched
	got, _ := c.Svc.Tickets.Get(ctx, c.Owner, c.seed.Ref)
	if got.Title != "seed" || got.Status != service.StatusTodo || len(got.Labels) != 0 {
		t.Fatalf("ticket changed: %+v", got)
	}
	if _, err := c.Svc.Comments.Resolve(ctx, c.Owner, cm.ID); err != nil {
		t.Fatal("comment deleted by a comment token")
	}
}

func TestCommentScopeAllowed(t *testing.T) {
	c := newCS(t)
	// reads work
	if _, err := c.Svc.Tickets.Get(ctx, c.ownerCT, c.seed.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Svc.Tickets.List(ctx, c.ownerCT, "WEB", service.TicketFilter{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Svc.Projects.List(ctx, c.ownerCT, false); err != nil {
		t.Fatal(err)
	}
	// add and edit own comment
	cm, err := c.Svc.Comments.Add(ctx, c.editorCT, c.seed.Ref, "from a comment token")
	if err != nil {
		t.Fatal(err)
	}
	if cm.Author.Type != service.ActorAPIToken {
		t.Fatalf("author %+v", cm.Author)
	}
	if got, err := c.Svc.Comments.Edit(ctx, c.editorCT, cm.ID, "edited"); err != nil || got.Body != "edited" {
		t.Fatalf("edit own: %v %v", got, err)
	}
	// another author's comment: not_author
	other, err := c.Svc.Comments.Add(ctx, c.Owner, c.seed.Ref, "human")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Svc.Comments.Edit(ctx, c.editorCT, other.ID, "hijack")
	wantSvcErr(t, err, service.KindForbidden, service.CodeNotAuthor)
	// a second comment token of the same user is a different author
	editorCT2 := c.TokenActor(c.NewToken(t, c.M.Users[testutil.Editor].ID, service.ScopeComment, ""))
	_, err = c.Svc.Comments.Edit(ctx, editorCT2, cm.ID, "x")
	wantSvcErr(t, err, service.KindForbidden, service.CodeNotAuthor)
	// the viewer role still cannot comment, whatever the scope
	_, err = c.Svc.Comments.Add(ctx, c.viewerCT, c.seed.Ref, "x")
	wantSvcErr(t, err, service.KindForbidden, service.CodeForbidden)
	// the reported role is capped at viewer
	d, err := c.Svc.Projects.Get(ctx, c.ownerCT, "WEB")
	if err != nil || d.Role != service.RoleViewer {
		t.Fatalf("role %v %v", d.Role, err)
	}
}

// TestCommentScopeMatrix runs the comment operations and a few others through the shared matrix
// with a comment-token row per role.
func TestCommentScopeMatrix(t *testing.T) {
	e := newTk(t)
	seed := e.create(t, "seed", service.StatusTodo)
	denied := testutil.Forbidden(service.CodeInsufficientScope)
	commentRoles := map[testutil.Who]testutil.Outcome{
		testutil.NonMember: testutil.NotFound, testutil.Viewer: testutil.Forbidden(service.CodeForbidden),
		testutil.Editor: testutil.OK, testutil.Owner: testutil.OK,
	}
	cases := []testutil.MatrixCase{
		{Name: "CommentAdd", Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Comments.Add(ctx, a, seed.Ref, "hi")
			return err
		}, Want: commentRoles, CommentWant: commentRoles},
		{Name: "Move", Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Tickets.Move(ctx, a, seed.Ref, service.MoveInput{Status: service.StatusBacklog})
			return err
		}, Want: map[testutil.Who]testutil.Outcome{testutil.Editor: testutil.OK, testutil.Owner: testutil.OK},
			CommentWant: map[testutil.Who]testutil.Outcome{testutil.Editor: denied, testutil.Owner: denied}},
		{Name: "TicketGet", Op: func(t *testing.T, a service.Actor) error {
			_, err := e.Svc.Tickets.Get(ctx, a, seed.Ref)
			return err
		}, Want: map[testutil.Who]testutil.Outcome{testutil.Viewer: testutil.OK},
			CommentWant: map[testutil.Who]testutil.Outcome{testutil.Viewer: testutil.OK, testutil.Owner: testutil.OK, testutil.NonMember: testutil.NotFound}},
	}
	for _, c := range cases {
		e.M.Run(t, c)
	}
}

func TestCommentScopeProjectLimitAndArchive(t *testing.T) {
	c := newCS(t)
	owner := c.M.Users[testutil.Owner]
	other := c.Env.NewProject(t, owner, "OTH")
	limited := c.TokenActor(c.NewToken(t, owner.ID, service.ScopeComment, other.ID))
	_, err := c.Svc.Comments.Add(ctx, limited, c.seed.Ref, "x")
	wantSvcErr(t, err, service.KindNotFound, "")
	_, err = c.Svc.Comments.Add(ctx, c.ownerCT, "WEB-999", "x")
	wantSvcErr(t, err, service.KindNotFound, "")
	outsiderCT := c.TokenActor(c.NewToken(t, c.M.Users[testutil.NonMember].ID, service.ScopeComment, ""))
	_, err = c.Svc.Comments.Add(ctx, outsiderCT, c.seed.Ref, "x")
	wantSvcErr(t, err, service.KindNotFound, "")

	cm, err := c.Svc.Comments.Add(ctx, c.ownerCT, c.seed.Ref, "before archive")
	if err != nil {
		t.Fatal(err)
	}
	c.Env.Archive(t, c.P.ID)
	_, err = c.Svc.Comments.Add(ctx, c.ownerCT, c.seed.Ref, "x")
	wantSvcErr(t, err, service.KindConflict, service.CodeProjectArchived)
	_, err = c.Svc.Comments.Edit(ctx, c.ownerCT, cm.ID, "x")
	wantSvcErr(t, err, service.KindConflict, service.CodeProjectArchived)
}

func TestScopeFailsClosed(t *testing.T) {
	c := newCS(t)
	owner := c.M.Users[testutil.Owner]
	for _, scope := range []service.Scope{"bogus", "admin", "READ"} {
		a := service.TokenActor(c.NewID(), owner.ID, scope, "")
		_, err := c.Svc.Tickets.Update(ctx, a, c.seed.Ref, service.UpdateTicketInput{Title: service.Some("x")})
		wantSvcErr(t, err, service.KindForbidden, service.CodeInsufficientScope)
		_, err = c.Svc.Comments.Add(ctx, a, c.seed.Ref, "x")
		wantSvcErr(t, err, service.KindForbidden, service.CodeInsufficientScope)
		if _, err := c.Svc.Tickets.Get(ctx, a, c.seed.Ref); err != nil {
			t.Fatalf("scope %q cannot read: %v", scope, err)
		}
	}
}

func TestCreateTokenAcceptsCommentScope(t *testing.T) {
	c := newCS(t)
	tok, err := c.Svc.Tokens.Create(ctx, c.Owner, service.CreateTokenInput{Name: "cm", Scope: service.ScopeComment, Hash: "hash-cm", Prefix: "pb_cmcm"})
	if err != nil || tok.Scope != service.ScopeComment {
		t.Fatalf("%+v %v", tok, err)
	}
	_, err = c.Svc.Tokens.Create(ctx, c.Owner, service.CreateTokenInput{Name: "bad", Scope: "admin", Hash: "hash-bad", Prefix: "pb_badd"})
	if se := wantSvcErr(t, err, service.KindValidation, ""); se.Fields["scope"] != "must be read, comment or write" {
		t.Fatalf("fields %v", se.Fields)
	}
}
