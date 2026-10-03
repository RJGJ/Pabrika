package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store/db"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

const testBase = "https://pabrika.test"

// fx is the seeded fixture: alice owner, bob editor, carol viewer, dave non-member; projects
// WEB (alice owner, bob editor, carol viewer) and OPS (alice owner).
type fx struct {
	t                       *testing.T
	env                     *testutil.Env
	h                       *handler
	alice, bob, carol, dave testutil.User
	web, ops                testutil.Project
	logBuf                  *strings.Builder
}

func newFx(t *testing.T) *fx {
	t.Helper()
	env := testutil.NewTestServices(t)
	f := &fx{t: t, env: env, logBuf: &strings.Builder{}}
	f.alice = env.NewUser(t, "alice@x.com", "Alice")
	f.bob = env.NewUser(t, "bob@x.com", "Bob")
	f.carol = env.NewUser(t, "carol@x.com", "Carol")
	f.dave = env.NewUser(t, "dave@x.com", "Dave")
	f.web = env.NewProject(t, f.alice, "WEB")
	f.ops = env.NewProject(t, f.alice, "OPS")
	env.AddMember(t, f.web.ID, f.bob.ID, service.RoleEditor)
	env.AddMember(t, f.web.ID, f.carol.ID, service.RoleViewer)
	logger := slog.New(slog.NewTextHandler(f.logBuf, nil))
	f.h = NewHandler(Deps{Services: env.Svc, BaseURL: testBase, Logger: logger}).(*handler)
	return f
}

// principal mints a token row and returns the matching Principal and token id.
func (f *fx) principal(u testutil.User, scope service.Scope, limit *testutil.Project) (auth.Principal, string) {
	pid := ""
	if limit != nil {
		pid = limit.ID
	}
	id := f.env.NewToken(f.t, u.ID, scope, pid)
	ti := &auth.TokenInfo{ID: id, Name: "tok-" + id[len(id)-4:], Scope: scope, ProjectID: pid}
	if limit != nil {
		ti.ProjectKey = limit.Key
	}
	return auth.Principal{User: auth.AuthUser{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName}, Method: auth.MethodToken, Token: ti}, id
}

// connect builds the production per-principal server and returns a client session on it.
func (f *fx) connect(p auth.Principal) *mcp.ClientSession {
	f.t.Helper()
	srv := f.h.newServer(p)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = ss.Close() })
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := cl.Connect(ctx, ct, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// as returns a connected session for a write/read token of the user.
func (f *fx) as(u testutil.User, scope service.Scope) (*mcp.ClientSession, string) {
	p, id := f.principal(u, scope, nil)
	return f.connect(p), id
}

// res is a decoded tool result.
type res struct {
	t       *testing.T
	R       *mcp.CallToolResult
	Text    string
	IsError bool
}

func (r res) JSON() map[string]any {
	r.t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.Text), &out); err != nil {
		r.t.Fatalf("not JSON: %q", r.Text)
	}
	return out
}

func (f *fx) call(cs *mcp.ClientSession, tool string, args any) res {
	f.t.Helper()
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		f.t.Fatalf("%s: protocol error: %v", tool, err)
	}
	if len(r.Content) != 1 {
		f.t.Fatalf("%s: want 1 content block, got %d", tool, len(r.Content))
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		f.t.Fatalf("%s: content is not text", tool)
	}
	return res{t: f.t, R: r, Text: tc.Text, IsError: r.IsError}
}

// okCall requires success.
func (f *fx) okCall(cs *mcp.ClientSession, tool string, args any) map[string]any {
	f.t.Helper()
	r := f.call(cs, tool, args)
	if r.IsError {
		f.t.Fatalf("%s failed: %s", tool, r.Text)
	}
	return r.JSON()
}

// errCall requires an isError result containing want.
func (f *fx) errCall(cs *mcp.ClientSession, tool string, args any, want string) {
	f.t.Helper()
	r := f.call(cs, tool, args)
	if !r.IsError {
		f.t.Fatalf("%s: expected error containing %q, got %s", tool, want, r.Text)
	}
	if !strings.Contains(r.Text, want) {
		f.t.Fatalf("%s: error %q does not contain %q", tool, r.Text, want)
	}
}

// seed helpers go through the service as alice (a user actor).
func (f *fx) aliceActor() service.Actor { return service.UserActor(f.alice.ID) }

func (f *fx) seedLabel(project, name string) {
	f.t.Helper()
	if _, err := f.env.Svc.Labels.Create(context.Background(), f.aliceActor(), project, service.LabelInput{Name: name, Color: "blue"}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) seedTicket(project, title string, st service.Status) service.Ticket {
	f.t.Helper()
	tk, err := f.env.Svc.Tickets.Create(context.Background(), f.aliceActor(), project, service.CreateTicketInput{Title: title, Status: st})
	if err != nil {
		f.t.Fatal(err)
	}
	return tk
}

// activity returns the raw activity rows of a ticket (oldest first).
func (f *fx) activity(ticketID string) []db.TicketActivity {
	f.t.Helper()
	rows, err := f.env.Store.Read().ActivitySeedListRaw(context.Background(), ticketID)
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	r, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range r.Tools {
		names = append(names, tl.Name)
	}
	return names
}

var _ = io.Discard

type mcpCall = mcp.CallToolParams

func mustErr(err error) error {
	if err == nil {
		panic("expected an error")
	}
	return err
}

func textOf(r *mcp.CallToolResult) string {
	return r.Content[0].(*mcp.TextContent).Text
}
