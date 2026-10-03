// Package service holds every business rule of Pabrika. It has no knowledge of HTTP or MCP:
// transports build an Actor, call a method, and map the returned *Error mechanically.
// It imports internal/store and internal/store/db only (never the SQLite driver).
//
// # Layout
//
//	types.go      domain types and the seven service interfaces (the contract, spec section 6.4)
//	validate.go   enums, limits (Max*), Validate() on every input struct (pure)
//	actor.go      Actor, Scope, Role; errors.go: *Error, Kind, codes; optional.go: Optional[T]
//	refs.go       ULID / project key / KEY-N parsing; cursor.go: shared opaque cursor; position.go: pure ordering math
//	events.go     the events seam (Event, Publisher, StreamControl); no hub here
//	service.go    Deps, Services, New, Tx and the write/read helpers
//	authz.go      the single permission choke point (requireProject and owner lookups)
//	users.go      UserService (UpdateProfile) - implemented
//	projects.go members.go labels.go tickets.go comments.go activity.go
//	              currently STUBS returning errNotImplemented; each work package replaces its file
//
// # The write pattern
//
// Every mutating method runs inside s.write; shared helpers take the *db.Queries they are
// given, never the store (the write pool has ONE connection: nesting deadlocks).
//
//	func (p *projectService) Update(ctx context.Context, actor Actor, ref string, in UpdateProjectInput) (Project, error) {
//	    if err := p.s.requireSession(actor); err != nil { ... }  // step 0, only for session-only ops
//	    var out Project
//	    err := p.s.write(ctx, actor, func(tx *Tx) error {
//	        proj, _, err := p.s.requireProject(ctx, tx.Q, actor, ref, RoleOwner, true) // 404 -> scope -> role
//	        if err != nil { return err }
//	        // for ticket/label/comment writes use requireProjectWrite (adds the archived 409 check)
//	        if err := in.Validate(); err != nil { return err }       // validation is LAST
//	        ... tx.Q.SomeQuery(ctx, ...) ...
//	        tx.Emit(Event{Type: EventProjectUpdated, ProjectID: proj.ID}) // queued; Actor and At filled in
//	        tx.AfterCommit(func() { ... })                                // e.g. Streams.CloseUser
//	        return nil
//	    })
//	    return out, err
//	}
//
// Events and AfterCommit funcs run only after a successful commit (events first, in order) and
// are discarded on error, rollback or panic. A no-op must not Emit. Methods never call the
// Publisher or Streams directly. Use tx.NewID() / tx.NowText() for ids and timestamps (never
// time.Now or random ids), and store.FormatTime/ParseTime through the parseTime helpers.
//
// Reads use s.read (one consistent snapshot) with the same requireProject call.
//
// # Order of checks (authz.go)
//
// session-only (requireSession) -> unknown/non-member 404 -> token limited to another project 404
// -> read scope on a write 403 insufficient_scope -> role too low 403 forbidden -> archived 409
// project_archived (requireWritable / requireProjectWrite) -> input Validate() 422.
// requireProject returns the project and the EFFECTIVE role (capped at viewer for read tokens).
// Resolve tickets, labels and comments to their project with resolveTicket, resolveLabel and
// resolveComment (each then calls requireProject); soft-deleted tickets and comments are not found.
//
// # Adding a query
//
// Write it in internal/store/queries/<domain>.sql (projects.sql, members.sql, labels.sql,
// tickets.sql, comments.sql, activity.sql belong to their work packages; authz.sql holds the
// owner lookups; seed.sql holds testutil seeding), then regenerate and commit the output:
//
//	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate     (or: go generate ./internal/store/)
//
// Notes: nullable columns are pointers; times are text (convert with parseTime/parseTimePtr and
// format with FormatTime via tx.NowText()); sqlc.narg needs CAST(... AS TEXT) for type
// inference; use the expanded OR form for keyset comparisons, not row values; sqlc.slice works
// for IN lists. Different domains must not define the same query name.
//
// # Adding a test (internal/testutil)
//
// Tests live in the external package service_test. env := testutil.NewTestServices(t) gives an
// in-memory DB with migrations, env.Svc, env.Clock (moves only on Advance), env.Pub and env.Streams
// (recording), env.NewID() (monotonic, deterministic). Use testutil.NewFileEnv(t) for concurrency
// tests (the in-memory store has a single connection). Seed straight through the store:
// env.NewUser, env.NewProject (with owner membership), env.AddMember, env.Archive, env.NewToken
// (a row only: no secret, no hash), env.UserActor(u), env.TokenActor(tokenID).
// Permission tables: m := env.NewMatrix(t, "WEB") seeds owner/editor/viewer/non-member, and
// m.Run(t, testutil.MatrixCase{...}) runs an Op as each role x {session, write token}; use
// testutil.OK / NotFound / Forbidden(code) / Conflict(code) / Invalid() and AssertOutcome.
// Unexported helpers are reachable from tests through export_test.go.
//
// store.QueryCount() returns the statements issued so far; compare deltas to prove batching.
package service
