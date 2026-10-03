package service

import (
	"context"
	crand "crypto/rand"
	"io"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/RJGJ/Pabrika/internal/store"
	"github.com/RJGJ/Pabrika/internal/store/db"
)

// Deps are the injectable collaborators of the service layer.
type Deps struct {
	Clock     func() time.Time // default time.Now().UTC(); the only source of "now" in the package
	NewID     func() string    // default monotonic ULID
	Publisher Publisher        // default NopPublisher
	Streams   StreamControl    // default no-op; phase 3 passes the hub
}

// Services is the entry point to every business operation. Transports (REST, MCP) hold one.
type Services struct {
	Projects ProjectService
	Members  MemberService
	Labels   LabelService
	Tickets  TicketService
	Comments CommentService
	Activity ActivityService
	Users    UserService
	Tokens   TokenService

	st   *store.Store
	deps Deps
}

// New builds the services on top of an opened and migrated store.
func New(st *store.Store, deps Deps) *Services {
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	if deps.NewID == nil {
		deps.NewID = NewULIDSource(deps.Clock, crand.Reader)
	}
	if deps.Publisher == nil {
		deps.Publisher = NopPublisher{}
	}
	if deps.Streams == nil {
		deps.Streams = nopStreams{}
	}
	s := &Services{st: st, deps: deps}
	s.Projects = &projectService{s: s}
	s.Members = &memberService{s: s}
	s.Labels = &labelService{s: s}
	s.Tickets = &ticketService{s: s}
	s.Comments = &commentService{s: s}
	s.Activity = &activityService{s: s}
	s.Users = &userService{s: s}
	s.Tokens = &tokenService{s: s}
	return s
}

// NewULIDSource returns an id generator producing uppercase ULIDs that sort in creation order
// even within one millisecond (monotonic entropy behind a mutex). The timestamp comes from clock.
func NewULIDSource(clock func() time.Time, entropy io.Reader) func() string {
	var mu sync.Mutex
	mono := ulid.Monotonic(entropy, 0)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return ulid.MustNew(ulid.Timestamp(clock()), mono).String()
	}
}

func (s *Services) now() time.Time { return s.deps.Clock().UTC() }
func (s *Services) newID() string  { return s.deps.NewID() }

// nowText is the current time in the one stored text format.
func (s *Services) nowText() string { return store.FormatTime(s.now()) }

// Tx is the handle a write callback receives. Use only Tx.Q inside the callback (never the
// store: the write pool has one connection, so nesting deadlocks).
type Tx struct {
	Q *db.Queries

	s      *Services
	actor  Actor
	events []Event
	after  []func()
}

// Emit queues an event; Actor and At are filled when unset. It is published only after commit.
func (t *Tx) Emit(e Event) {
	if e.Actor == (EventActor{}) {
		e.Actor = EventActorOf(t.actor)
	}
	if e.At.IsZero() {
		e.At = t.s.now()
	}
	t.events = append(t.events, e)
}

// AfterCommit queues f to run after the events are published, only if the commit succeeds.
func (t *Tx) AfterCommit(f func()) { t.after = append(t.after, f) }

// Now is the transaction's notion of the current time (Deps.Clock).
func (t *Tx) Now() time.Time { return t.s.now() }

// NowText is Now in the stored text format.
func (t *Tx) NowText() string { return t.s.nowText() }

// NewID returns a fresh ULID.
func (t *Tx) NewID() string { return t.s.newID() }

// write runs fn in one write transaction. After a successful commit it publishes the queued
// events in order, then runs the AfterCommit funcs in order. On error, rollback or panic both
// queues are discarded. Methods never call Publisher or Streams directly.
func (s *Services) write(ctx context.Context, actor Actor, fn func(tx *Tx) error) error {
	var tx *Tx
	err := s.st.WithTx(ctx, func(q *db.Queries) error {
		tx = &Tx{Q: q, s: s, actor: actor}
		return fn(tx)
	})
	if err != nil {
		return err
	}
	for _, e := range tx.events {
		s.deps.Publisher.Publish(e)
	}
	for _, f := range tx.after {
		f()
	}
	return nil
}

// read runs fn in a read-only transaction (one consistent snapshot).
func (s *Services) read(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.st.WithReadTx(ctx, fn)
}

// parseTime converts a stored timestamp; an unparsable value is a data bug, so it yields zero time.
func parseTime(v string) time.Time {
	t, err := store.ParseTime(v)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseTimePtr(v *string) *time.Time {
	if v == nil {
		return nil
	}
	t := parseTime(*v)
	return &t
}
