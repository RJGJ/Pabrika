// Package testutil is the shared test harness for the service layer (and, later, HTTP and MCP).
//
// Env bundles an in-memory (or temp-file) store with migrations applied, a fake clock that only
// moves when told, deterministic monotonic ULIDs, a recording publisher and a recording
// StreamControl, plus seeding helpers that write straight through the store so tests do not
// depend on services that are not under test:
//
//	env := testutil.NewTestServices(t)            // in-memory, one connection
//	env := testutil.NewFileEnv(t)                 // temp-dir file DB; use for concurrency tests
//	owner := env.NewUser(t, "o@x.io", "Owner")    // user row with a placeholder password hash
//	p := env.NewProject(t, owner, "WEB")          // project row + owner membership
//	env.AddMember(t, p.ID, viewer.ID, service.RoleViewer)
//	env.Archive(t, p.ID)                          // set archived_at directly
//	tok := env.NewToken(t, owner.ID, service.ScopeRead, "") // token row only; no secret, no hash
//	actor := env.UserActor(owner); tactor := env.TokenActor(tok)
//
// The Matrix helper (matrix.go) runs one operation as each of {non-member, viewer, editor, owner}
// x {session user, write token} and asserts the expected outcome.
package testutil

import (
	"context"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/auth"
	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

// FakeClock only moves when told.
type FakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewFakeClock starts at 2026-01-01T00:00:00Z.
func NewFakeClock() *FakeClock {
	return &FakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Set jumps the clock to t.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t.UTC()
	c.mu.Unlock()
}

// RecordingPublisher records every published event.
type RecordingPublisher struct {
	mu     sync.Mutex
	events []service.Event
	// OnPublish, if set, runs inside Publish (e.g. to read the DB and prove commit happened first).
	OnPublish func(service.Event)
}

// Publish implements service.Publisher.
func (p *RecordingPublisher) Publish(e service.Event) {
	p.mu.Lock()
	p.events = append(p.events, e)
	cb := p.OnPublish
	p.mu.Unlock()
	if cb != nil {
		cb(e)
	}
}

// Events returns a copy of the recorded events.
func (p *RecordingPublisher) Events() []service.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]service.Event(nil), p.events...)
}

// Reset forgets recorded events.
func (p *RecordingPublisher) Reset() {
	p.mu.Lock()
	p.events = nil
	p.mu.Unlock()
}

// StreamCall is one recorded StreamControl call. UserID is "" for CloseProject.
type StreamCall struct {
	ProjectID, UserID string
	Reason            service.CloseReason
}

// RecordingStreams records StreamControl calls.
type RecordingStreams struct {
	mu    sync.Mutex
	calls []StreamCall
}

// CloseUser implements service.StreamControl.
func (r *RecordingStreams) CloseUser(projectID, userID string, reason service.CloseReason) {
	r.mu.Lock()
	r.calls = append(r.calls, StreamCall{projectID, userID, reason})
	r.mu.Unlock()
}

// CloseProject implements service.StreamControl.
func (r *RecordingStreams) CloseProject(projectID string, reason service.CloseReason) {
	r.mu.Lock()
	r.calls = append(r.calls, StreamCall{projectID, "", reason})
	r.mu.Unlock()
}

// Calls returns a copy of the recorded calls.
func (r *RecordingStreams) Calls() []StreamCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]StreamCall(nil), r.calls...)
}

// Reset forgets recorded calls.
func (r *RecordingStreams) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

// Env is a ready-to-use test environment.
type Env struct {
	Store   *store.Store
	Svc     *service.Services
	Clock   *FakeClock
	Pub     *RecordingPublisher
	Streams *RecordingStreams
	// NewID is the id source shared with the services (monotonic, deterministic).
	NewID func() string
}

// User is a seeded user.
type User struct{ ID, Email, DisplayName string }

// Project is a seeded project.
type Project struct{ ID, Key string }

// NewTestServices builds an Env on an in-memory database (single connection).
func NewTestServices(t testing.TB) *Env {
	t.Helper()
	st, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return newEnv(t, st)
}

// NewFileEnv builds an Env on a temp-dir file database (write pool of one, read pool of several);
// use it for concurrency tests.
func NewFileEnv(t testing.TB) *Env {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return newEnv(t, st)
}

func newEnv(t testing.TB, st *store.Store) *Env {
	t.Helper()
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock := NewFakeClock()
	pub := &RecordingPublisher{}
	streams := &RecordingStreams{}
	newID := service.NewULIDSource(clock.Now, rand.New(rand.NewSource(42)))
	svc := service.New(st, service.Deps{
		Clock:     clock.Now,
		NewID:     newID,
		Publisher: pub,
		Streams:   streams,
	})
	return &Env{Store: st, Svc: svc, Clock: clock, Pub: pub, Streams: streams, NewID: newID}
}

func (e *Env) now() string { return store.FormatTime(e.Clock.Now()) }

// NewUser inserts a user with a fixed placeholder password hash (no hashing in phase 1).
func (e *Env) NewUser(t testing.TB, email, displayName string) User {
	t.Helper()
	id := e.NewID()
	err := e.Store.WithTx(context.Background(), func(q *db.Queries) error {
		_, err := q.CreateUser(context.Background(), db.CreateUserParams{
			ID: id, Email: email, DisplayName: displayName,
			PasswordHash: "placeholder-not-a-real-hash", CreatedAt: e.now(),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return User{ID: id, Email: email, DisplayName: displayName}
}

// NewToken inserts an api_tokens row (no secret, no hashing) and returns the token id.
// projectID "" means every project the owner is in.
func (e *Env) NewToken(t testing.TB, ownerUserID string, scope service.Scope, projectID string) string {
	t.Helper()
	if scope == "" {
		scope = service.ScopeWrite
	}
	id := e.NewID()
	var pid *string
	if projectID != "" {
		pid = &projectID
	}
	err := e.Store.WithTx(context.Background(), func(q *db.Queries) error {
		_, err := q.CreateAPIToken(context.Background(), db.CreateAPITokenParams{
			ID: id, UserID: ownerUserID, Name: "token " + id[len(id)-6:], TokenHash: "hash-" + id,
			TokenPrefix: "pb_" + id[len(id)-5:], Scope: string(scope), ProjectID: pid, CreatedAt: e.now(),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// NewTokenWithSecret inserts an api_tokens row whose hash and prefix come from auth.Generate
// (so the returned plain secret authenticates as a bearer token) and returns the token id and
// the secret. Use external test packages (auth_test, httpapi_test): testutil imports auth.
func (e *Env) NewTokenWithSecret(t testing.TB, ownerUserID string, scope service.Scope, projectID string) (id, secret string) {
	t.Helper()
	if scope == "" {
		scope = service.ScopeWrite
	}
	secret, hash, prefix := auth.Generate()
	id = e.NewID()
	var pid *string
	if projectID != "" {
		pid = &projectID
	}
	err := e.Store.WithTx(context.Background(), func(q *db.Queries) error {
		_, err := q.CreateAPIToken(context.Background(), db.CreateAPITokenParams{
			ID: id, UserID: ownerUserID, Name: "token " + id[len(id)-6:], TokenHash: hash,
			TokenPrefix: prefix, Scope: string(scope), ProjectID: pid, CreatedAt: e.now(),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, secret
}

// NewSession creates a real session for userID (clocked by the Env) and returns the cookie value.
func (e *Env) NewSession(t testing.TB, userID string) string {
	t.Helper()
	value, _, err := auth.NewSessions(e.Store, auth.SessionOptions{Now: e.Clock.Now}).Create(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// NewProject inserts a project and the owner's owner membership directly through the store.
func (e *Env) NewProject(t testing.TB, owner User, key string) Project {
	t.Helper()
	id := e.NewID()
	err := e.Store.WithTx(context.Background(), func(q *db.Queries) error {
		now := e.now()
		if _, err := q.SeedProject(context.Background(), db.SeedProjectParams{
			ID: id, Key: key, Name: "Project " + key, CreatedBy: owner.ID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return q.SeedMember(context.Background(), db.SeedMemberParams{ProjectID: id, UserID: owner.ID, Role: string(service.RoleOwner), CreatedAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
	return Project{ID: id, Key: key}
}

// AddMember inserts a membership row directly.
func (e *Env) AddMember(t testing.TB, projectID, userID string, role service.Role) {
	t.Helper()
	err := e.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SeedMember(context.Background(), db.SeedMemberParams{ProjectID: projectID, UserID: userID, Role: string(role), CreatedAt: e.now()})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Archive sets archived_at directly.
func (e *Env) Archive(t testing.TB, projectID string) {
	t.Helper()
	at := e.now()
	err := e.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SeedArchiveProject(context.Background(), db.SeedArchiveProjectParams{ArchivedAt: &at, ID: projectID})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// UserActor is the session actor for a seeded user.
func (e *Env) UserActor(u User) service.Actor { return service.UserActor(u.ID) }

// TokenActor builds the actor for a token created with NewToken, reading scope and limits from its row.
func (e *Env) TokenActor(tokenID string) service.Actor {
	row, err := e.Store.Read().GetAPIToken(context.Background(), tokenID)
	if err != nil {
		panic("testutil: unknown token " + tokenID + ": " + err.Error())
	}
	pid := ""
	if row.ProjectID != nil {
		pid = *row.ProjectID
	}
	return service.TokenActor(row.ID, row.UserID, service.Scope(row.Scope), pid)
}
