# Phase 1: Foundation (detailed spec)

Parent docs: [phases.md](phases.md) (scope and exit criteria), [main-spec.md](main-spec.md) (source of truth; wins on any conflict).
Status: todo. Depends on: nothing.

**Goal:** a runnable Go project with the SQLite database, migrations, sqlc queries and a fully tested service layer. No HTTP, no auth, no events hub, no MCP, no UI.

**Spec sections used:** Tech stack and architecture, Data model, Layout config and deployment (config, SQLite settings, repo layout), Testing. The Auth role table and the REST/MCP argument shapes are used only as the contract the service layer must satisfy later.

---

## 1. Deliverables and exit criteria

- `CGO_ENABLED=0 go build ./...` and `CGO_ENABLED=0 go test ./...` pass; `go vet ./...` is clean.
- `go run ./cmd/pabrika serve` loads config, opens the DB (creating the file and parent directory), applies migrations, logs the schema version and exits 0 (HTTP arrives in phase 2).
- Service tests run against in-memory SQLite and cover: ordering, role and permission rules (matrix), last-owner protection, soft delete (section 9 lists them).
- Generated sqlc code is committed, so tests need neither `sqlc` nor `goose` CLIs installed.
- **The gate is `CGO_ENABLED=0 go test ./...`.** A separate, OPTIONAL `CGO_ENABLED=1 go test -race ./...` is run where a C toolchain is available (the race detector needs cgo, which conflicts with the no-CGO rule, so it is never part of the gate). Phases 2 to 6 inherit this rule.

---

## 2. Repo layout (Go module root is `D:\rj\Pabrika\pabrika`)

```text
go.mod                         # module github.com/RJGJ/Pabrika; go 1.23
Makefile                       # build, test (CGO_ENABLED=0), vet, generate targets; optional `test-race` (CGO_ENABLED=1 go test -race ./...)
sqlc.yaml
cmd/pabrika/main.go            # subcommand dispatch; `serve` (bootstrap only) and `version` in this phase;
                               #   declares `var version = "dev"` (Phase 6 overrides via -ldflags "-X main.version=...")
internal/
  config/config.go             # env parsing + validation
  config/config_test.go
  store/
    store.go                   # Open, OpenMemory, Close, WithTx, WithReadTx, Migrate
    pragma.go                  # DSN building
    timefmt.go                 # exported FormatTime / ParseTime (the one timestamp format, also used by phase 2 for sessions)
    db/                        # sqlc output (committed): models.go, *.sql.go, querier.go
    queries/                   # sqlc input: users.sql, projects.sql, members.sql, labels.sql,
                               #   tickets.sql, comments.sql, activity.sql
    store_test.go
  service/
    service.go                 # Services struct, constructor, Deps (clock, ids, publisher, streams), write/Tx helper
    actor.go                   # Actor (incl. Scope, ProjectID), roles, role ordering
    errors.go                  # error kinds, codes, constructors
    authz.go                   # the single permission choke point (membership, role, token scope, project limit)
    validate.go                # exported limits/enums + Validate() on input structs
    optional.go                # Optional[T] with JSON (absent / null / value) support
    refs.go                    # ULID vs project key / ticket reference parsing
    cursor.go                  # opaque cursor encode/decode shared by all paginated lists
    position.go                # pure ordering math (no DB)
    projects.go  members.go  tickets.go  labels.go  comments.go  activity.go  users.go
    events.go                  # Event/EventType/EventActor/EventActorOf/Publisher/StreamControl/CloseReason seam (no hub); owned by Phase 1
    *_test.go                  # external package `service_test` (testutil imports service)
  testutil/                    # NewTestServices(t) *Env, seed helpers (users, tokens), fake clock, recording publisher
migrations/
  embed.go                     # package migrations; //go:embed *.sql; var FS embed.FS
  00001_init.sql               # all ten tables, goose Up/Down
```

Not created in this phase: `internal/auth`, `internal/httpapi`, `internal/mcpserver`, `web/`, `Dockerfile`.

---

## 3. Task breakdown

Each task ends with passing tests for what it added.

1. **Project skeleton.** `go.mod` (`github.com/RJGJ/Pabrika`), directory layout, `Makefile`, `.gitignore` (`data/`, `*.db*`), `sqlc.yaml`, `//go:generate` line in `internal/store` that runs a pinned sqlc via `go run`.
2. **Config.** `config.Load(getenv func(string) string) (Config, error)`; table-driven tests.
3. **Migrations.** `migrations/00001_init.sql` with the ten main-spec tables (section 5), embedded FS.
4. **Store.** Open/pragmas/pools/`WithTx`/`Migrate`; `OpenMemory` for tests (section 4). Migration test.
5. **sqlc queries.** Write query files, run generate, commit output.
6. **Service core.** `Actor`, errors, clock/ID deps, `authz`, validation, refs, events seam, testutil.
7. **Position math.** `position.go` pure functions with table tests, written before tickets.
8. **Projects and members.** Create/get/resolve/list/update/archive/delete; add/change/remove member; last-owner protection.
9. **Labels.**
10. **Tickets.** Create, get, list, update, move, delete, reference resolution, counter, activity writes.
11. **Comments and activity listing.**
11a. **User profile.** `UserService.UpdateProfile` and the user/session store queries (section 6.4).
12. **Bootstrap `serve` and `version`.** Wire config + store + migrate in `main.go`; `version` prints the build version.
13. **Full test pass** against the list in section 9 with `CGO_ENABLED=0 go test ./...` (the gate); optionally also `CGO_ENABLED=1 go test -race ./...` where cgo is available.

---

## 4. Config and store

### Config (`internal/config`)

```go
type Config struct {
    Port         int    // PORT, default 8080, 1..65535
    DBPath       string // DB_PATH, default ./data/pabrika.db
    BaseURL      string // BASE_URL, default http://localhost:8080; absolute http(s) URL with no path (other than "/"), query or fragment; trailing slash trimmed
    AllowSignup  bool   // ALLOW_SIGNUP, default true (strconv.ParseBool)
    CookieSecure bool   // COOKIE_SECURE, default true
    TrustProxy   bool   // TRUST_PROXY, default false (phase 2 rate limiter and logs read the first X-Forwarded-For hop)
}
func Load(getenv func(string) string) (Config, error)
// Origin returns BaseURL normalised the way a browser sends the Origin header: lowercase scheme and host,
// default port (80 for http, 443 for https) dropped. Phase 2's Origin check and phase 6's "BaseURL is HTTPS"
// test use this (and strings.HasPrefix(c.BaseURL, "https://")); neither re-parses BASE_URL.
func (c Config) Origin() string
```

Errors name the offending variable. Empty string means "use default". Unknown variables are ignored. These fields are parsed now though only later phases use them.

### Store (`internal/store`)

```go
type Store struct { /* write *sql.DB, read *sql.DB */ }

func Open(ctx context.Context, path string) (*Store, error)  // creates parent dir; does NOT migrate
func OpenMemory(ctx context.Context) (*Store, error)          // tests; fresh isolated DB per call
func (s *Store) Migrate(ctx context.Context) (version int64, err error)
func (s *Store) Close() error                                 // best-effort PRAGMA wal_checkpoint(TRUNCATE) first (phase 6 relies on this), then closes both pools
func (s *Store) Read() *db.Queries                            // read pool; each statement is its own snapshot
func (s *Store) WithReadTx(ctx context.Context, fn func(q *db.Queries) error) error // read pool, read-only tx: one consistent snapshot for multi-query reads
func (s *Store) WithTx(ctx context.Context, fn func(q *db.Queries) error) error // write pool, commit/rollback
```

`store.FormatTime(time.Time) string` and `store.ParseTime(string) (time.Time, error)` (section 5 format) are exported here so phase 2 (sessions, tokens) writes the identical text format; do not re-implement it elsewhere.

Rules:
- Driver `modernc.org/sqlite`, registered name `sqlite`. Pragmas via DSN `_pragma=` params: `foreign_keys(1)`, `busy_timeout(5000)`, `journal_mode(WAL)`, `synchronous(NORMAL)`.
- **Write pool:** `SetMaxOpenConns(1)`, `SetMaxIdleConns(1)`, `_txlock=immediate` so write transactions take the write lock at BEGIN.
- **Read pool:** several connections (default `max(4, NumCPU)`), `_pragma=query_only(1)`.
- All mutations (including their permission checks and counter allocation) run inside one `WithTx` call. `WithTx` rolls back on error or panic (the panic is re-raised).
- **No nesting.** The write pool has one connection, so a `WithTx` (or a `Read()`/`WithReadTx` call on the in-memory DB, which has one connection for everything) issued from inside another `WithTx` callback deadlocks. Inside a callback use only the `q` it was given; shared internals take `q *db.Queries`, never the store. A test with a short context timeout guards this.
- Multi-query reads (a ticket plus labels, assignee and comment count; a project plus counts) use `WithReadTx` so the parts are consistent. Single-statement reads use `Read()`.
- `OpenMemory`: in-memory databases are per-connection, so it uses a **single pool of one connection for both read and write** (`:memory:` with `SetMaxOpenConns(1)`, connection never closed by idle timeout: `SetConnMaxLifetime(0)`, `SetConnMaxIdleTime(0)`). Because a single connection serves everything, never hold a read query open (an unclosed `Rows`) while calling the write path in the same goroutine.
- Migrations: goose Provider API (no globals), dialect sqlite3, source `migrations.FS`, run through the write pool so they take the write lock. `Migrate` is idempotent and safe to call from a second process while the server runs (the CLI commands in phase 2 may call it); it returns the current version. If the database holds a version newer than the embedded migrations (a downgrade), `Migrate` returns an error and changes nothing.

---

## 5. Migrations: tables to create

`migrations/00001_init.sql`: goose `-- +goose Up` creates, in dependency order, exactly the main-spec DDL for: `users`, `sessions`, `projects`, `project_members` (+ `members_by_user`), `api_tokens`, `labels`, `tickets` (+ partial index `tickets_board`), `ticket_labels`, `comments` (+ `comments_by_ticket`), `ticket_activity`. Column names, types, CHECKs, defaults, foreign keys and ON DELETE actions are copied verbatim from the Data model section. `-- +goose Down` drops them in reverse order.

Additions beyond the main-spec DDL (indexes only, no column changes; recorded as assumptions):
- `CREATE INDEX activity_by_ticket ON ticket_activity (ticket_id, created_at, id);`
- `CREATE INDEX sessions_by_user ON sessions (user_id);` and `CREATE INDEX tokens_by_user ON api_tokens (user_id);` (cheap, used in phase 2).

Conventions the service must follow when writing rows:
- IDs: ULID strings (`github.com/oklog/ulid/v2`), uppercase, from an injected generator. The default generator uses `ulid.Monotonic` entropy behind a mutex (not goroutine-safe otherwise), so ids created in the same millisecond still sort in creation order; `(created_at, id)` ordering of comments and activity relies on that.
- Timestamps: UTC, fixed-width `2006-01-02T15:04:05.000Z` text (sorts lexically, valid RFC 3339). `due_date` is `YYYY-MM-DD`.
- Phase 1 creates the `users`, `sessions` and `api_tokens` tables. The only service touching them is `UserService.UpdateProfile` (section 6.4); there is no signup, login, session or token service. Users and tokens are seeded in tests through store queries (`CreateUser`, `CreateAPIToken`) exposed in `testutil`. Store query files include `GetUserByEmail`, `GetUserByID`, `ListUsersByIDs`, `CreateUser`, `CreateAPIToken`, `GetAPIToken`, plus the account self-service queries `UpdateUserDisplayName`, `UpdateUserPassword` (display name and password hash) and `DeleteSessionsForUserExcept` (deletes all of a user's sessions except a given session id). The last two are defined and store-tested here so phase 2's password change is only hashing and an endpoint; no service method uses them in phase 1. Phase 2 adds the remaining session, token-list, revoke, `last_used_at` and purge queries itself (additive `.sql` files plus regenerate); the store surface (`Read()`, `WithTx`, `FormatTime`) is what it builds on. Seeded test users get a fixed placeholder `password_hash` (no hashing in phase 1).

---

## 6. Service layer contract

The service package imports `store` and `db` only. No `net/http`, no MCP types. Input structs carry `json` tags (so REST can decode straight into them) but there are no transport DTOs.

### 6.1 Core types

```go
type ActorType string
const (ActorUser ActorType = "user"; ActorAPIToken ActorType = "api_token")

type Scope string // "read" | "write" ("" is treated as write: no cap); ScopeWrite includes read
const (ScopeRead Scope = "read"; ScopeWrite Scope = "write")

// Actor is who is acting. For a token, ID is the token id and UserID is the token's owner.
// Role permissions are always evaluated for UserID. Audit columns use (Type, ID).
// Scope and ProjectID carry the token's limits: Scope is the token scope; ProjectID, if non-empty,
// is the single project (ULID) the token is limited to. The service enforces both (section 6.3).
// Only ScopeRead restricts anything: an empty Scope behaves like ScopeWrite, so phase 2's Principal.Actor()
// may leave Scope empty for sessions. Nothing but ActorUser/ActorAPIToken is a valid Type (others -> panic in tests, ErrForbidden at runtime).
type Actor struct { Type ActorType; ID string; UserID string; Scope Scope; ProjectID string }
func UserActor(userID string) Actor                                          // Type user, ID = UserID, Scope = write, ProjectID = ""
func TokenActor(tokenID, ownerUserID string, scope Scope, projectID string) Actor
func (a Actor) IsToken() bool

type Role string // "owner" | "editor" | "viewer"; rank viewer(1) < editor(2) < owner(3)
func (r Role) AtLeast(min Role) bool
func (r Role) Valid() bool

type Status string   // "backlog" | "todo" | "in_progress" | "done"  (ordered; StatusRank 0..3)
type Priority string // "low" | "medium" | "high" | "urgent"
type LabelColor string // the fixed palette
// Exported lists and limits shared with REST and MCP (phase 4 builds its schemas and drift test from these; do not duplicate them elsewhere):
var Statuses []Status; var Priorities []Priority; var LabelColors []LabelColor   // palette: gray, red, orange, amber, green, teal, blue, indigo, purple, pink
func (Status) Valid() bool; func (Priority) Valid() bool; func (LabelColor) Valid() bool
const (MaxTitle = 200; MaxDescription = 20000; MaxCommentBody = 20000; MaxProjectName = 100; MaxProjectDescription = 2000
       MaxLabelName = 50; MaxDisplayName = 100; MaxLabelsPerTicket = 50; DueDateLayout = "2006-01-02") // lengths count runes, not bytes

// Optional distinguishes "field omitted" from "set to null" from "set to a value" for PATCH-style updates.
// UnmarshalJSON: key absent -> zero Optional; `null` -> Set=true, Null=true, Value=zero; value -> Set=true.
// For non-nullable fields (name, title, priority, ...) Null is a validation error (Fields[name] = "must not be null").
// For Optional[*string] (due_date, assignee) and Optional[[]string] (labels) Null means "clear" (labels: empty set).
type Optional[T any] struct { Set bool; Null bool; Value T }
func Some[T any](v T) Optional[T]
func Null[T any]() Optional[T]

type Deps struct {
    Clock     func() time.Time // default time.Now().UTC(); the only source of "now" in the package
    NewID     func() string    // default monotonic ULID (section 5)
    Publisher Publisher        // default NopPublisher
    Streams   StreamControl    // default no-op; phase 3 passes the hub
}
type Services struct { Projects ProjectService; Members MemberService; Labels LabelService
                       Tickets TicketService; Comments CommentService; Activity ActivityService
                       Users UserService }
func New(st *store.Store, deps Deps) *Services
```

### 6.2 Errors (`errors.go`)

One error type so later transports map it mechanically:

```go
type Kind int // KindValidation(422) KindBadRequest(400) KindForbidden(403) KindNotFound(404) KindConflict(409)
type Error struct { Kind Kind; Code string; Message string; Fields map[string]string } // Code: stable snake_case; Fields: per-field messages, only for KindValidation, keyed by the JSON field name (title, description, status, priority, due_date, assignee, labels, name, key, color, body, email, role, display_name, before/after/place)
var ErrNotFound, ErrForbidden, ErrConflict, ErrValidation, ErrBadRequest  // sentinels; errors.Is(err, ErrNotFound) matches by Kind
// Exported constructors, so phase 2 (email_taken, ...) and others build errors the same way. Phase 2 reads Kind, Code, Message and Fields (-> wire `fields`) and nothing else:
func NewError(kind Kind, code, message string) *Error
func Validation(fields map[string]string) *Error   // KindValidation, Code validation_failed, Message = first field's message (lowest-sorted key if several), Fields = the map
func (e *Error) Error() string; func (e *Error) Is(target error) bool // Is matches the sentinels by Kind
```

Codes: `not_found`, `forbidden` (role too low), `not_author` (Forbidden: comment edit/delete by a member who is not the author, nor an owner for delete; the service keeps this code, but the REST wire code is plain `forbidden`; phase 4 gives it its own "not written by this token" text), `insufficient_scope` (Forbidden), `session_required` (Forbidden: session-only operation attempted by a token actor), `validation_failed`, `key_taken`, `already_member`, `last_owner`, `label_exists`, `project_archived`, `user_not_found` (Validation, `Fields["email"]`), `anchor_invalid` (Validation, `Fields["before"|"after"]`), `invalid_cursor` (Kind `KindBadRequest`, HTTP 400). `email_taken` (KindConflict, 409) is reserved for phase 2, created through `NewError(KindConflict, "email_taken", ...)`. `user_not_found` carries `Fields["email"]`; `anchor_invalid` carries `Fields["before"]` or `Fields["after"]`. Messages never reveal whether a project the caller cannot see exists.

### 6.3 Authorization (`authz.go`): the single choke point

```go
// resolveProject maps a key-or-ULID ref to the project and the actor's membership in ONE query (membership join),
// inside the given tx/queries. Not a member and unknown are the same row-not-found.
// requireProject then applies, in this order:
// 0. Session-only operations (Projects.Delete, Members.Add/SetRole/Remove, Users.UpdateProfile): a token actor -> ErrForbidden `session_required`,
//    checked BEFORE any lookup (matches phase 2's guard order, where session_required precedes 404). Main-spec: members, roles and project deletion are session-only.
// 1. Non-member or unknown project -> ErrNotFound (identical message).
// 2. Actor.ProjectID set and != the resolved project id -> ErrNotFound (identical to a non-member; transports map to 404 / MCP "limited to project X").
// 3. Write operation (min >= editor, or any owner-only op) with Actor.Scope == ScopeRead -> ErrForbidden `insufficient_scope`.
// 4. Member's effective role below `min` -> ErrForbidden `forbidden`.
func (s *Services) requireProject(ctx, q *db.Queries, actor Actor, ref string, min Role, write bool) (db.Project, Role, error)

// checkActor is the shared actor-level part (scope, and project limit for project-less calls). requireProject
// calls it; Projects.Create (needs write scope; a project-limited token -> ErrForbidden `forbidden`, cannot create projects)
// and Projects.List (a project-limited token sees only its project) call it directly. It lives in authz.go.
func (s *Services) checkActor(actor Actor, write bool) error
```

- **Effective role:** the member's role, capped at `viewer` when `Actor.Scope == ScopeRead`. `ProjectDetail.Role`, `ProjectSummary.Role` and `ProjectRef.Role` report the effective role (phase 2 puts it in `project.role`).
- Every method that touches a project, ticket, label or comment resolves the owning project and calls `requireProject`. Ticket, label and comment ids are resolved to their project first (`ticket -> project_id`), then checked. A soft-deleted ticket (and its comments) resolves as not found.
- Order of checks (full): session-only (0) before project limit, existence and membership (404) before scope and role (403) before archived state (409) before validation (422). Input validation that needs no project (shape, lengths) still runs only after the 404/403/409 checks, so non-members and viewers never see field errors. A test asserts this order with an input that is simultaneously invalid, forbidden and in an archived project.
- Role needs: read ops viewer; ticket/label/comment writes editor; project update and member management owner; project delete owner. Token actors can use project update (archive) but not delete or member management (step 0).
- Archived project (`archived_at` set): writes to tickets, labels and comments (including comment edit and delete, ticket delete and move) return `ErrConflict` code `project_archived`. Owner-only project operations (update incl. unarchive, delete, member add/role/remove, leave) still work. Reads still work.
- Token scope and project limit are enforced here, in the service layer, through `Actor.Scope` and `Actor.ProjectID`, so no transport can skip them. Phase 2 and 4 only build the `Actor` from the token row and map errors. A read token can read everything in its reach; a project-limited token sees nothing outside its project (404). Token validity (revoked, unknown) and the owner's existence are the transport's job; the service trusts the `Actor`.

### 6.4 Interfaces (signatures only)

```go
type ProjectService interface {
    Create(ctx, actor Actor, in CreateProjectInput) (Project, error)            // any user; becomes owner
    Get(ctx, actor Actor, ref string) (ProjectDetail, error)                    // ref = ULID or key; effective role + counts per status
    Resolve(ctx, actor Actor, ref string) (ProjectRef, error)                   // ULID or key -> {ID, Key, Role}; ErrNotFound for non-members; also the membership check phase 3 uses for SSE open and keepalive
    List(ctx, actor Actor, includeArchived bool) ([]ProjectSummary, error)      // only member projects, key ascending, unpaginated (bounded set); phase 2 pages it in the HTTP layer (key > cursor)
    Update(ctx, actor Actor, ref string, in UpdateProjectInput) (Project, error) // owner; name, description, archived
    Delete(ctx, actor Actor, ref string) error                                  // owner, session only; hard delete, cascades
}
type CreateProjectInput struct { Key string `json:"key"`; Name string `json:"name"`; Description string `json:"description"` }
type UpdateProjectInput struct { Name Optional[string] `json:"name"`; Description Optional[string] `json:"description"`; Archived Optional[bool] `json:"archived"` }
type Project struct { ID, Key, Name, Description string; ArchivedAt *time.Time; CreatedAt, UpdatedAt time.Time }
type ProjectRef struct { ID, Key string; Role Role }
type ProjectSummary struct { Project; Role Role; TicketCounts map[Status]int } // counts via one grouped query, exclude soft-deleted (phase 4 list_projects shows them; phase 2 ignores them)
type ProjectDetail struct { Project; Role Role; TicketCounts map[Status]int }  // TicketCounts has all four keys, 0 when empty

type MemberService interface {
    List(ctx, actor Actor, projectRef string) ([]Member, error)                          // viewer+; order: owners first, then lower(display_name), then user id
    Add(ctx, actor Actor, projectRef, email string, role Role) (Member, error)           // owner, session only
    SetRole(ctx, actor Actor, projectRef, userID string, role Role) (Member, error)      // owner, session only
    Remove(ctx, actor Actor, projectRef, userID string) error                            // owner, or the member themselves; session only
}
type Member struct { UserID, Email, DisplayName string; Role Role; CreatedAt time.Time }

type LabelService interface {
    List(ctx, actor Actor, projectRef string) ([]Label, error)                    // viewer+, name order (case-insensitive), then id
    Create(ctx, actor Actor, projectRef string, in LabelInput) (Label, error)     // editor
    Update(ctx, actor Actor, labelID string, in UpdateLabelInput) (Label, error)  // editor
    Delete(ctx, actor Actor, labelID string) error                                // editor; cascades ticket_labels
}
type Label struct { ID, ProjectID, Name string; Color LabelColor }
type LabelInput struct { Name string `json:"name"`; Color LabelColor `json:"color"` }      // Color default "gray", must be in palette
type UpdateLabelInput struct { Name Optional[string] `json:"name"`; Color Optional[LabelColor] `json:"color"` }

type TicketService interface {
    Create(ctx, actor Actor, projectRef string, in CreateTicketInput) (Ticket, error)   // editor
    Get(ctx, actor Actor, ref string) (Ticket, error)                                   // ref = ULID or WEB-12; viewer+; includes description
    List(ctx, actor Actor, projectRef string, f TicketFilter) (TicketPage, error)       // viewer+; items have Description == "" (not loaded)
    Update(ctx, actor Actor, ref string, in UpdateTicketInput) (Ticket, error)          // editor
    Move(ctx, actor Actor, ref string, in MoveInput) (MoveResult, error)                // editor
    Delete(ctx, actor Actor, ref string) error                                          // editor; soft delete
    Resolve(ctx, actor Actor, ref string) (TicketRef, error)                            // ULID/ref -> {ID, ProjectID, Key, Number}; for transports
}
type CreateTicketInput struct { Title string `json:"title"`; Description string `json:"description"`; Status Status `json:"status"`; Priority Priority `json:"priority"`
                                DueDate *string `json:"due_date"`; AssigneeID *string `json:"assignee"`; LabelIDs []string `json:"labels"` }
type UpdateTicketInput struct { Title Optional[string] `json:"title"`; Description Optional[string] `json:"description"`; Priority Optional[Priority] `json:"priority"`
                                DueDate Optional[*string] `json:"due_date"`; AssigneeID Optional[*string] `json:"assignee"`; LabelIDs Optional[[]string] `json:"labels"` }
type MoveInput struct { Status Status `json:"status"`; Before string `json:"before"`; After string `json:"after"`; Place string `json:"place"` } // at most one of Before/After/Place; Place = "top"|"bottom"
type MoveResult struct { Ticket Ticket; Renumbered bool }
type TicketFilter struct { Status *Status; Priority *Priority; Assignee *AssigneeFilter; LabelID string
                           Query string; Limit int; Cursor string }
type AssigneeFilter struct { Unassigned bool; UserID string }
type TicketPage struct { Items []Ticket; NextCursor string } // NextCursor "" = last page
type TicketRef struct { ID, ProjectID, Key string; Number int64 }
type UserRef struct { ID, Email, DisplayName string }
// Ticket carries everything a transport renders, loaded with batched queries (never per ticket): labels, assignee, comment count.
type Ticket struct { ID, ProjectID, ProjectKey string; Number int64; Ref string /* "WEB-12" */
                     Title, Description string; Status Status; Priority Priority
                     AssigneeID *string; Assignee *UserRef; Position float64; DueDate *string
                     Labels []Label /* name order, never nil */; CommentCount int; CreatedAt, UpdatedAt time.Time }

type CommentService interface {
    List(ctx, actor Actor, ticketRef string, limit int, cursor string) (CommentPage, error) // viewer+, oldest first (created_at, id), excludes deleted; limit rules as tickets
    Latest(ctx, actor Actor, ticketRef string, n int) (comments []Comment, truncated bool, err error) // viewer+, the newest n (n clamped 1..200), returned oldest-first; truncated = more exist (phase 4 get_ticket uses n=50)
    Add(ctx, actor Actor, ticketRef, body string) (Comment, error)               // editor
    Edit(ctx, actor Actor, commentID, body string) (Comment, error)              // exact author actor, still editor+, write scope
    Delete(ctx, actor Actor, commentID string) error                             // author or project owner; soft
    Resolve(ctx, actor Actor, commentID string) (CommentRef, error)              // for transports: id -> {ID, TicketID, TicketRef, ProjectID, ProjectKey}; same visibility rules (soft-deleted -> ErrNotFound)
}
type CommentPage struct { Items []Comment; NextCursor string }
type CommentRef struct { ID, TicketID, TicketRef, ProjectID, ProjectKey string }
type Comment struct { ID, TicketID, TicketRef string; Author CommentAuthor; Body string
                      CreatedAt time.Time; EditedAt *time.Time }
type CommentAuthor struct { Type ActorType; ID, Name string; OwnerName string /* token's owner; "" for users */ }
// Name resolution uses LEFT JOINs: a user or token row that no longer exists yields Name "deleted user" / "deleted token" (never an error, never a missing row).

type ActivityService interface {
    List(ctx, actor Actor, ticketRef string, limit int, cursor string) (ActivityPage, error) // viewer+, newest first
}
type ActivityPage struct { Items []Activity; NextCursor string }
type Activity struct { ID, TicketID string; Actor CommentAuthor; Action string
                       Changes map[string][2]any; CreatedAt time.Time }

type UserService interface {
    UpdateProfile(ctx, actor Actor, displayName string) (User, error) // user actors only (token actor -> ErrForbidden `session_required`); trimmed 1..MaxDisplayName (100) runes
}
type User struct { ID, Email, DisplayName string; CreatedAt time.Time }
```

`Validate() error` is defined on `CreateProjectInput`, `UpdateProjectInput`, `LabelInput`, `UpdateLabelInput`, `CreateTicketInput`, `UpdateTicketInput` and `MoveInput`. It is **pure** (no DB, no clock): length caps, enums, key format, palette, `due_date` shape, `Optional` null rules, at-most-one placement, label count cap. It returns a `*Error` of KindValidation with `Fields`. Service methods call it at the validation step of the order of checks (6.3); phase 2 and phase 4 reuse it and the exported lists and constants (6.1) instead of redefining caps. Checks that need the DB (assignee is a member, labels belong to the project, anchors, uniqueness) stay in the methods.

`UserService` is the minimal service for the account self-service decision. `UpdateProfile` is built here because it needs no hashing; `ChangePassword` (verify current password, argon2id, delete other sessions) belongs to phase 2, which adds it to this interface using the store queries already defined in section 5. Session-only access is a transport rule; the service additionally refuses token actors.

`Resolve` and ref-by-key lookups exist so phases 2 and 4 do not reimplement parsing. Every project-taking method accepts a project **key or ULID** (`WEB` or `{ulid}`), and `Get`/`Update`/etc. on tickets accept a ULID or reference. Label, comment, member (user) and token ids are ULIDs only.

**Cursors (`cursor.go`).** One opaque format for every paginated list, owned by the service so REST and MCP return identical cursors: base64url (no padding) of the JSON `{"v":1,"k":[...]}`. Keys: tickets `[statusRank, position, id]` (position as a JSON number, which Go round-trips exactly), comments `[created_at, id]`, activity `[created_at, id]`. A malformed, wrongly shaped or wrong-version cursor -> `ErrBadRequest` code `invalid_cursor` (HTTP 400; there is no `ErrValidation` for cursors). Fetch `limit+1` rows to decide `NextCursor`. Pagination is keyset-stable under inserts and deletes; a ticket moved between fetches may be seen twice or skipped, which is accepted (clients reload).

### 6.5 Events seam (`events.go`): no hub in this phase

**Phase 1 owns these types** (`Event`, `EventType` and its constants, `EventActor`, `EventActorOf`, `Publisher`, `NopPublisher`, `StreamControl`, `CloseReason`, and the `Tx` helper with `Emit` and `AfterCommit`). Phase 3 does **not** redefine or replace any of them: it adds only the hub, which implements both `Publisher` and `StreamControl` (one instance passed as `Deps.Publisher` and `Deps.Streams`), and the SSE handler. Names and fields below are exactly what Phase 3 uses.

```go
type EventType string
// all constants below are typed EventType
const ( EventTicketCreated EventType = "ticket.created"; EventTicketUpdated = "ticket.updated"; EventTicketMoved = "ticket.moved"
        EventTicketDeleted = "ticket.deleted"; EventCommentAdded = "comment.added"; EventCommentChanged = "comment.changed"
        EventLabelChanged = "label.changed"; EventMemberChanged = "member.changed"; EventProjectUpdated = "project.updated" )

type EventActor struct { Type ActorType `json:"type"`; ID string `json:"id"` } // never the whole Actor (no UserID/Scope/ProjectID in payloads); ID is the token id for api_token
func EventActorOf(a Actor) EventActor // {a.Type, a.ID}
type Event struct {
    Type       EventType  `json:"type"`
    ProjectID  string     `json:"project_id"`
    TicketID   string     `json:"ticket_id,omitempty"`  // ULID
    CommentID  string     `json:"comment_id,omitempty"`
    LabelID    string     `json:"label_id,omitempty"`
    UserID     string     `json:"user_id,omitempty"`    // member.changed: the affected member
    Renumbered bool       `json:"renumbered,omitempty"` // ticket.moved: the column was renumbered
    Actor      EventActor `json:"actor"`
    At         time.Time  `json:"at"`                   // from Deps.Clock
}
type Publisher interface { Publish(Event) }
type NopPublisher struct{}

type CloseReason string // CloseNone "", CloseSlow "slow_client", CloseRemoved "access_removed", CloseProject "project_deleted", CloseShutdown "shutdown", CloseClient "client_closed"
type StreamControl interface { CloseUser(projectID, userID string, reason CloseReason); CloseProject(projectID string, reason CloseReason) }
```

Write helper (internal, `service.go`): every mutating method runs `s.write(ctx, actor, func(tx *Tx) error)` where `Tx{ Q *db.Queries }` has `Emit(Event)` (queues; fills `Actor` with `EventActorOf(actor)` and `At` from `Deps.Clock` when unset) and `AfterCommit(func())`. After a successful commit the helper first calls `Publisher.Publish` for each queued event in order, then runs the `AfterCommit` funcs in order; on error, rollback or panic both queues are discarded. Methods never call `Publisher` or `Streams` directly. Tests use a recording publisher and recording `StreamControl` to assert ordering and that nothing is published on failure or no-op.

Which method emits what (this is the catalog phase 3 expects; ids only, never content; `Actor` is the method's actor argument):

| Write | Event | Ids set | After commit |
|---|---|---|---|
| `Tickets.Create` | `ticket.created` | ticket | |
| `Tickets.Update` (only if something changed) | `ticket.updated` | ticket | |
| `Tickets.Move` (not a no-op) | `ticket.moved`, `Renumbered` when the column was renumbered | ticket | |
| `Tickets.Delete` | `ticket.deleted` | ticket | |
| `Comments.Add` | `comment.added` | ticket, comment | |
| `Comments.Edit`, `Comments.Delete` | `comment.changed` | ticket, comment | |
| `Labels.Create/Update/Delete` | `label.changed` | label | |
| `Members.Add`, `SetRole` (not a no-op), `Remove` (incl. leave) | `member.changed` | user (the affected member) | `Remove`: `Streams.CloseUser(project, user, CloseRemoved)` |
| `Projects.Update` (only if something changed) | `project.updated` | none | |
| `Projects.Delete` | none | | `Streams.CloseProject(project, CloseProject)` |
| `Projects.Create` | none (no one can be subscribed yet) | | |

`ProjectID` is set on every event. Member removal's automatic unassignment of tickets (7.2) emits **no** `ticket.updated` (tickets are unassigned silently); removal emits only `member.changed`, and the UI reloads tickets on it (phase 5). A display-name change (`Users.UpdateProfile`) emits no event. An unchanged `Projects.Update` emits no event and does not bump `updated_at`; an identical-body `Comments.Edit` is a no-op (no `edited_at`, no event). Phase 3's catalog already says one event per logical change and none on no-ops.

---

## 7. Behavior rules

### 7.1 Projects
- **Key:** input is trimmed and uppercased, then must match `^[A-Z]{2,6}$` else `ErrValidation`. Unique across the install; duplicate -> `ErrConflict` `key_taken`. Key is **immutable** after creation (not in `UpdateProjectInput`), because ticket references depend on it.
- **Create:** one transaction inserts the project (`next_ticket_number=1`, `created_by`, timestamps) and the creator's `owner` membership. Name 1..100 chars trimmed; description max 2,000 chars.
- **Update/Archive:** `Archived=true` sets `archived_at=now`; `false` clears it. A call that changes nothing (same name, description and archived state; archiving an archived project; an empty update) is a successful no-op: no `updated_at` bump, no event (phase 4 enforces "at least one field" itself). Otherwise it bumps `updated_at` and emits `project.updated`. Owners may edit name and description of an archived project.
- **Backstops:** every pre-checked uniqueness (`key_taken`, `label_exists`, `already_member`) also maps the SQLite UNIQUE/PRIMARY KEY constraint error (extended codes 2067 and 1555) to the same error, in case a code path forgets the pre-check.
- **Delete:** owner only, session only (token actor -> `session_required`); hard delete relying on FK cascades (members, labels, tickets, ticket_labels, comments, activity, project-limited tokens). A test asserts nothing is left behind.
- **Get/List/Resolve** only ever see projects the actor is a member of; the project ref is resolved (key or ULID, key case-insensitive) through the membership join so unknown and not-a-member are indistinguishable. A project-limited token sees only its project in `List` and gets `ErrNotFound` for any other ref.

### 7.2 Members and roles
- **Add/SetRole/Remove are session-only** (token actor -> `ErrForbidden` `session_required` before anything else, 6.3 step 0).
- **Add:** owner only. Looks the user up by email (case-insensitive). No such user -> `ErrValidation` `user_not_found` with `Fields["email"] = "No account with this email"` (422). Already a member -> `ErrConflict` `already_member`. Role must be a valid role.
- **SetRole:** owner only. Target must be a member (else `ErrNotFound`). Demoting the **last owner** -> `ErrConflict` `last_owner`. Setting the same role is a no-op success (no event).
- **Remove:** allowed if the actor is an owner (any member) or the actor is the member themselves (leave). An editor or viewer removing someone else -> `ErrForbidden`. Removing or leaving as the **last owner** -> `ErrConflict` `last_owner`. After a successful leave the former member gets 404 on the project.
- **Last-owner check** counts owners inside the same write transaction as the change, so it cannot race.
- On removal, in the same transaction, all tickets in that project assigned to the removed user, including soft-deleted ones, get `assignee_id = NULL`, and each non-deleted one gets an `assigned` activity row (`{"assignee":[userId,null]}`, actor = the remover), so the "assignee must be a member" invariant holds. Emits `member.changed` only (no per-ticket events, see 6.5), then `Streams.CloseUser` after commit. Removal and leaving are allowed in an archived project.
- Viewers can see the member list (needed for assignee display).

### 7.3 Tickets

**Reference counter.** Allocating a number happens inside the create transaction: `UPDATE projects SET next_ticket_number = next_ticket_number + 1 WHERE id = ? RETURNING next_ticket_number - 1 AS number` (alias needed for sqlc). The write pool's single connection plus `BEGIN IMMEDIATE` serialise allocation; if the transaction rolls back, the increment rolls back with it (no gaps from failed creates). Numbers start at 1, are consecutive, never reused (soft-deleted tickets keep theirs), and `UNIQUE(project_id, number)` is the backstop. `Ref = Key + "-" + number`.

**Reference parsing (`refs.go`).** A 26-char Crockford-base32 string (uppercased before the check, so lowercase ULIDs work) is a ULID; project keys are at most 6 letters, so a project ref is never ambiguous. Otherwise `^([A-Za-z]{2,6})-([1-9][0-9]*)$` is a reference (key matched case-insensitively). Anything else -> `ErrNotFound` (not 400) so probing reveals nothing. Resolution excludes soft-deleted tickets. Not-a-member and nonexistent give the same `ErrNotFound`.

**Validation.** Title: trimmed, 1..200 characters (runes). Description: 0..20,000 characters, stored as given (not trimmed). Status and priority must be valid enum values (on create an empty value means the default `todo` / `medium`; on `Move` an empty status is an error). `due_date` must parse as a real `YYYY-MM-DD` date (empty string normalised to nil). Assignee (empty string normalised to nil) must be a member of the same project (any role), checked only when the assignee is being set. Every label id must belong to the ticket's project (checked only when labels are being set); duplicates are collapsed; at most `MaxLabelsPerTicket` (50). `Validate()` covers the pure checks, the methods the DB-dependent ones. Violations -> `ErrValidation` with `Fields` naming each field (JSON names, 6.2).

**Ordering by midpoint position (`position.go`, pure).**
- Constants: `Gap = 1024.0`, `MinGap = 1e-6`.
- Column = tickets of one project with one status and `deleted_at IS NULL`, ordered by `(position, id)`.
- `Bottom`: `max(position) + Gap`, or `Gap` if the column is empty. `Top`: `min(position) - Gap`, or `Gap` if empty (negative positions are allowed).
- `Between(a, b)`: `(a + b) / 2`. If `b - a < MinGap` (including equal or inverted positions), the column needs renumbering first. Float64 stays precise enough because renumbering keeps magnitudes near `Gap * columnSize`.
- **Renumber:** within the same transaction, load the column ids ordered by `(position, id)` (excluding the ticket being moved), assign `Gap * i` for `i = 1..n`, then recompute the target position by anchor identity against the fresh positions (the second `PlanMove` cannot ask for another renumber). Renumbering does not bump `updated_at` and writes no activity. `MoveResult.Renumbered` reports it so transports/clients know other cards' positions changed.
- A pure function `PlanMove(neighbours []float64 /* sorted positions of the column without the moved ticket */, idx int /* insert index */) (pos float64, renumber bool)` is unit-tested without a DB.

**Create.** Editor+. Position = bottom of the target column (status default `todo`). Optional assignee and labels applied in the same transaction. Writes activity `created` with `changes = {"title":[null,"..."],"status":[null,"todo"], ...}` for the fields set at creation. Emits `ticket.created`.

**Move** (`MoveInput`):
- `Status` is required. At most one of `Before`, `After`, `Place` may be set (more than one -> `ErrValidation`). None set means bottom of the target column.
- `Before`/`After` is a ticket id or reference (resolved with the same project check). It must be in the **same project**, **in the target status**, not soft-deleted, and not the moved ticket itself; otherwise `ErrValidation` `anchor_invalid`.
- Neighbours are computed on the target column **excluding the moved ticket**: `before X` = between `prev(X)` and `X` (or top-ish: `X.position - Gap` if X is first); `after X` = between `X` and `next(X)` (or `X.position + Gap` if X is last). `Place` top/bottom as above.
- Moving within the same column and across columns use the same code path. **No-op definition:** the status is unchanged and the ticket would keep the same neighbours in the column (same predecessor and successor, ignoring itself). A no-op returns the ticket unchanged: no position write, no renumber, no activity, no event, `updated_at` unchanged, `Renumbered` false.
- Activity `moved` with `changes = {"status":[old,new],"position":[old,new]}` (status key only when it changed). Emits `ticket.moved`.

**Update.** Only `Set` fields change. Fields that did not change are ignored (no activity entry, no event if nothing changed). Status cannot be changed here (use Move; the input type has no status field). Values equal to the current ones count as unchanged (label sets compare as sets). One activity row per call: `action = "assigned"` if only the assignee changed, `"labeled"` if only labels changed, otherwise `"updated"`; `changes` lists every changed field as `[old, new]`. The complete set of `changes` keys across all activity rows is exactly: `title`, `description`, `priority`, `due_date`, `assignee`, `labels`, `status`, `position` (no others). Label changes are recorded as lists of label names (old list, new list, each ordered by name), assignee as user ids (or null), description as old/new truncated to 200 characters. Setting `LabelIDs` replaces the whole set. Bumps `updated_at`. Emits `ticket.updated`.

**Delete.** Editor+. Sets `deleted_at = now`; row, comments and labels stay. Excluded from list, get, counts, ordering anchors, comment counts and reference resolution. Deleting an already-deleted ticket -> `ErrNotFound`. Activity `deleted`. Emits `ticket.deleted`. There is no restore API (the spec says "undone by hand").

**List.** Filters: `status`, `priority`, assignee (`Unassigned` or a user id), `LabelID`, `Query` (case-insensitive substring over title and description; `%`, `_` and `\` escaped). `Query` is trimmed; case-insensitivity is SQLite `LIKE` semantics, i.e. ASCII only (accepted; non-ASCII letters match case-sensitively). An invalid `Status` or `Priority` value in the filter -> `ErrValidation`. Order: status rank (backlog, todo, in_progress, done), then `position`, then `id`. Limit default 50, max 200 (values above are clamped, `<= 0` means default). The cursor is the shared one from 6.4 (`k = [statusRank, position, id]`); a malformed cursor -> `ErrBadRequest` `invalid_cursor`. Labels, assignees and comment counts are loaded for the page in batched queries (one per kind, or joins), not per ticket; `Description` is not selected.

### 7.4 Labels
- Name trimmed, 1..50 chars, unique per project case-insensitively (duplicate -> `ErrConflict` `label_exists`, also on rename). Color must be one of the fixed palette (`LabelColors`): `gray, red, orange, amber, green, teal, blue, indigo, purple, pink` (default `gray`). Renaming a label to a different casing of its own name is allowed. Writes in an archived project -> `project_archived`. Label ids resolve through their project, so a label of another project is `ErrNotFound`.
- Delete removes the label from all tickets via cascade. No ticket activity rows are written for label rename or delete, and no `ticket.updated` events (clients reload on `label.changed`). Emits `label.changed` for create, update and delete (an update to identical values is a no-op: no event).

### 7.5 Comments
- Add: editor+; body trimmed, 1..20,000 chars; `author_type`/`author_id` come from the actor (`user` + user id, or `api_token` + token id). Comment on a soft-deleted ticket -> `ErrNotFound`; so do edit and delete of a comment whose ticket is soft-deleted. Comment writes in an archived project (add, edit, delete) -> `project_archived`. Emits `comment.added`.
- Edit: the comment's `(author_type, author_id)` must equal the actor's `(Type, ID)` exactly (a user edits their own comments; a token edits only comments that same token wrote), the actor's owner must still be an **editor or owner** of the project, and the actor needs write scope; otherwise `ErrForbidden` (non-members `ErrNotFound`): a member who is not the author gets code `not_author`, a demoted author (now viewer) gets `forbidden`, a read-scope actor gets `insufficient_scope`. Editing with an identical (trimmed) body is a no-op success (no `edited_at` change, no event). So a human cannot edit an agent's comment, nor an agent a human's. Body rules as Add (1..20,000). Sets `edited_at`. Emits `comment.changed`.
- Delete: soft (`deleted_at`). Allowed for the author (still editor+, write scope) or a project owner (including deleting an agent's comment); others -> `ErrForbidden` `not_author` (an author who is now a viewer: `forbidden`). A deleted comment edits or deletes as `ErrNotFound`. Emits `comment.changed`.
- List/read: oldest first, excludes deleted. Author name resolution: for `user`, the user's display name; for `api_token`, the token's name plus `OwnerName` (LEFT JOIN on `api_tokens` and `users`; missing rows give "deleted token" / "deleted user"). Comments writes do not create `ticket_activity` rows (the action list in the spec has no comment action).
- `Ticket.CommentCount` counts non-deleted comments (on every ticket the service returns, including list items and the results of create/update/move).

### 7.6 Activity
- Written inside the same transaction as the change; `actor_type`/`actor_id` from the `Actor`. `changes` is JSON `{field: [old, new]}`. Listing: viewer+, newest first, cursor-paginated by `(created_at, id)`, same limit rules. Actor display uses the same name resolution as comments. Activity survives ticket soft delete.

---

## 8. sqlc and query notes

- `sqlc.yaml`: engine `sqlite`, schema = `migrations/`, queries = `internal/store/queries/`, Go output `internal/store/db`, `emit_interface: true`, `emit_json_tags: false`. Time columns are scanned as `string` and converted in the service layer (the single `store.ParseTime`/`store.FormatTime` pair; services never format times by hand).
- Optional filters use the `(sqlc.narg(x) IS NULL OR col = sqlc.narg(x))` pattern. Keyset pagination uses an explicit `CASE status ...` rank expression.
- Query surface (names indicative): project insert/get-by-id/get-by-key/list-for-user/update/delete; member insert/get/list/update-role/delete/count-owners; ticket insert/get/get-by-ref/list-page/update/set-position/soft-delete/list-column/renumber-column-item/counts-by-status/clear-assignee-for-project-user; label CRUD + ticket_labels set/replace/list-for-tickets; comment insert/get/list/update/soft-delete/count; activity insert/list; user and token seed queries; `DeleteSessionsForUserExcept`. Name-resolving reads (comment and activity authors) use LEFT JOINs.
- A `.sql` test (or CI step `sqlc diff`) is optional; committed generated code is what the build uses.

---

## 9. Test list (maps to exit criteria)

All service tests live in the external package `service_test` and use `testutil.NewTestServices(t) *testutil.Env` (in-memory SQLite, migrations applied, fake clock that only moves when told, monotonic ULID source, recording publisher and recording `StreamControl`). `Env` exposes `Store`, `Svc *service.Services`, `Clock`, `Pub`, `Streams`, and seeding helpers phases 2 to 4 reuse: `NewUser(t, email, displayName) User`, `NewToken(t, ownerUserID, scope, projectID) (tokenID string)` (a row only; no secret, no hashing), `UserActor(u)`, `TokenActor(tok)`. Use table-driven subtests. The gate is `CGO_ENABLED=0 go test ./...`; `CGO_ENABLED=1 go test -race ./...` is an optional extra run where cgo is available (section 1).

**Config / store**
- Defaults; each variable overridden; invalid `PORT`, `BASE_URL`, bool values name the variable.
- Migration applies on a fresh DB; re-running is a no-op; the ten expected tables and the `tickets_board` index exist; `PRAGMA foreign_keys` is on; goose Down then Up works.
- File-backed `Open` (temp dir): WAL mode active, `busy_timeout` 5000, write pool limited to one connection; `N=20` concurrent `Tickets.Create` calls yield unique, consecutive numbers `1..20` with no "database is locked".
- `WithTx` rolls back on error and on panic; a nested `WithTx` (or `Read()` on the in-memory store) from inside a callback fails fast under a short context timeout instead of hanging the suite.
- `store.FormatTime` output is fixed-width and sorts lexically; `ParseTime` round-trips it. `Config.Origin()` drops default ports and lowercases. `BASE_URL` with a path, query or fragment is rejected.
- `Migrate` on a database whose version is newer than the embedded migrations returns an error and changes nothing. `Close` on a file DB leaves no `-wal` content behind (checkpoint ran).

**Position math (pure)**
- Bottom/top/between on empty, single and multi-item columns; negative positions after repeated top inserts; gap below `MinGap` and equal positions trigger renumber; renumber output is `Gap*i`.

**Ordering (service)**
- New tickets append to the bottom, per status independently.
- `before`, `after`, `top`, `bottom`, and default placement produce the expected `List` order; within-column and cross-column moves.
- Moved ticket is excluded from its own neighbour computation (move to just after its current predecessor is a no-op).
- 60 consecutive "insert between the same two tickets" moves force a renumber; `Renumbered` is true; final order is still correct; ids and relative order of untouched tickets are preserved.
- Invalid anchors: other project, other status, self, soft-deleted, unknown -> `ErrValidation` `anchor_invalid`; both `Before` and `After` -> `ErrValidation`.
- No-op move (same neighbours) writes nothing and publishes nothing; a real move of the same ticket afterwards still works.
- Concurrent `Move` calls into the same gap on a file DB (goroutines): all succeed, positions end up distinct, `List` order equals a serial replay's valid order (no "database is locked").
- Soft-deleted tickets never take part in ordering.

**Ticket references and counter**
- First ticket is `KEY-1`, counters are per project, never reused after soft delete, ref lookup is case-insensitive on the key, ULID lookup works, garbage refs give `ErrNotFound`.
- Project key validation (length, characters, lowercase normalised, duplicate -> `key_taken`).

**Roles and permissions (matrix)**
For each operation in sections 6.4 and for roles {non-member, viewer, editor, owner} (each as a session user actor and as a write-token actor) assert the outcome: success, `ErrNotFound` (non-member, for projects, tickets, labels, comments, activity and member lists alike), or `ErrForbidden` with the documented code. Specific cases:
- Non-member gets `ErrNotFound`, never `ErrForbidden`, with an identical message to a nonexistent id.
- Viewer can read everything but cannot create/edit/move/delete tickets, manage labels, or comment.
- Editor cannot update/archive/delete the project or manage members.
- Owner-only ops work for owners.
- Token actor: acts with its owner's role; comments and activity record `api_token` actor with the token id; names resolve to token name and owner.
- Comment edit: only the exact author actor (user or the same token) who is still editor+ with write scope; a viewer (e.g. demoted author) cannot edit; owner cannot edit another's comment but can delete it (including an agent's); a human cannot edit a token's comment and vice versa; an editor cannot delete another's comment (`not_author`); a demoted author gets `forbidden`; archived project -> `project_archived` for edit and delete; identical-body edit is a no-op (no event).
- Token scope: a read token can read but every write (tickets, labels, comments, project update, `Projects.Create`) -> `insufficient_scope`; `ProjectDetail.Role` for a read token owning the project reports `viewer` (effective role); a write token acts with its owner's role; an empty `Scope` behaves as write.
- Session-only operations (`Projects.Delete`, `Members.Add/SetRole/Remove`, `Users.UpdateProfile`): any token actor, read or write, -> `session_required`, even for a non-member project or invalid input (no lookup happens first).
- Order of checks: one input that is invalid, from a viewer, in an archived project, targeting a project the actor is not in -> `ErrNotFound`; from a viewer who is a member -> `forbidden`; from an editor in the archived project -> `project_archived`; from an editor in a live project -> `ErrValidation` with `Fields`. A read token on an archived project write -> `insufficient_scope` (scope before archived).
- Project-limited token: any other project, ticket, label or comment -> `ErrNotFound` (same message as a non-member); `Projects.List` returns only its project; `Projects.Create` -> `ErrForbidden` `forbidden`. `Resolve` by key and by ULID of another project -> `ErrNotFound`.
- Project ref: `Get`, `Resolve` and member/label/ticket calls accept the key (any case) or the ULID with identical results.
- Archived project: ticket (create, update, move, delete), label and comment (add, edit, delete) writes -> `project_archived`; owner can edit, unarchive, add/remove members and delete; reads still work; updating an unchanged project is a no-op (no `updated_at` bump, no event).
- `ProjectSummary`/`ProjectDetail` counts exclude soft-deleted tickets and contain all four statuses; `List` is key-ascending and excludes archived unless asked.

**Members and last-owner protection**
- Creator is owner; add by email (case-insensitive), unknown email -> `user_not_found`, duplicate -> `already_member`.
- Cannot demote the only owner; can demote after promoting a second owner; two owners cannot both be demoted down to zero (second demotion fails). Concurrent demotion of each other by two owners on a file DB (two goroutines): exactly one succeeds, the other gets `last_owner`.
- `Members.List` order: owners first, then display name case-insensitively, then user id; viewers can list.
- Last owner cannot remove self or leave; non-last owner can leave; editor/viewer can leave; editor cannot remove others.
- Removing a member unassigns their tickets in that project (soft-deleted ones too, activity rows only for live ones, actor = remover) and they get `ErrNotFound` afterward; no `ticket.updated` is published, only `member.changed` (then `CloseUser`); their tickets in other projects are untouched.

**Soft delete**
- Deleted ticket is absent from `List`, counts, `Get`, ref resolution and anchors; comments on it return `ErrNotFound`; row still exists with `deleted_at` set; activity `deleted` exists and remains queryable via the store; the counter did not decrease.
- Deleted comment disappears from `List` and `CommentCount`; row retained.
- Project delete hard-removes everything.

**Tickets general**
- Validation limits (title 200 / 201, description 20,000 / 20,001, bad due date, bad enums), assignee must be a member, labels must belong to the project, label set replace semantics, `Optional` semantics (omit vs null for assignee and due date), no-op updates produce no activity.
- Activity rows: action selection (`created`, `updated`, `assigned`, `labeled`, `moved`, `deleted`) and `changes` content.
- `List` filters (status, priority, assignee incl. unassigned, label, query with `%`, `_` and `\\` literal), invalid filter enum -> `ErrValidation`, default and max limits, cursor pagination stable across status boundaries, equal positions and inserts between pages, malformed or wrong-version cursor -> `ErrBadRequest` `invalid_cursor`, list items have empty `Description` and a populated `Assignee`, `Labels` and `CommentCount` (statement count for a 50-item page does not grow with the page size).
- `Get` returns description, labels, assignee, comment count; create/update/move results carry the same populated fields.
- `Validate()` on every input struct is pure and reports `Fields` keyed by JSON name; `Optional` JSON: absent, `null` and value decode to distinct states; `null` on a non-nullable field is a field error; a nil `Labels` is `[]`, never null.
- Exported constants and lists (`Statuses`, `Priorities`, `LabelColors`, `Max*`) equal the documented values (a drift test phase 4 reuses).

**Labels and comments**
- Label uniqueness case-insensitive, palette enforcement, delete cascades off tickets, rename conflict.
- Comments: order oldest first (including same-millisecond creations, via monotonic ids), `List` pagination by cursor, `Latest(n)` returns the newest n oldest-first with a truncation flag, edit sets `edited_at`, author display for user vs token, a missing token or user row yields "deleted token" / "deleted user" (no error), `Resolve` returns ticket ref and project key, `Comment.TicketRef` is populated.

**User profile and account queries**
- `UpdateProfile` trims and validates the display name (empty and 101 runes -> `ErrValidation` `Fields["display_name"]`, 100 accepted, multibyte counted as runes), a token actor -> `ErrForbidden` `session_required`; returns `CreatedAt`.
- Store tests: `UpdateUserDisplayName`, `UpdateUserPassword` change only the intended columns; `DeleteSessionsForUserExcept` deletes the user's other sessions, keeps the given one and leaves other users' sessions.

**Events seam**
- Recording publisher receives exactly one expected event per row of the 6.5 catalog (type, project id, ticket/comment/label/user ids, `Actor` = the method's actor with `Type`/`ID` only, `At` from the fake clock, `Renumbered` set on a renumbering move), after commit: when the subscriber-side callback reads the DB the new state is visible. Nothing on failed writes, rolled-back transactions or no-op updates (ticket update, role set to same, project update unchanged, comment identical edit, no-op move).
- `Members.Remove` publishes `member.changed` then calls `Streams.CloseUser(project, user, CloseRemoved)`; `Projects.Delete` publishes nothing and calls `Streams.CloseProject(project, CloseProject)`; neither is called on failure (last owner, forbidden).
- A token actor's events carry `{type:"api_token", id:<token id>}`; JSON-marshalling any event contains only the documented keys (no `user_id` of the actor, no scope, no content).

---

## 10. What later phases rely on

- **Phase 2 (REST/auth):** `service.Services` and the six interfaces; `Actor` built from a cookie user or token owner (`UserActor`, `TokenActor`; an empty `Scope` means write); `service.Error` kinds mapped to 400 (`KindBadRequest`, e.g. `invalid_cursor`)/403/404/409/422 and the `{"error":{"code","message","fields"}}` shape (`Error.Fields` feeds `fields`; wire code for `not_author` is `forbidden`; `session_required` and `insufficient_scope` pass through); `service.NewError` for phase-2-owned codes such as `email_taken`; the exported `Validate()` methods, enums, palette and `Max*` constants; `store.Store` for adding users, sessions, tokens (tables already exist); `TicketService.Resolve` and ULID-or-ref acceptance; the shared cursor (`{"v":1,"k":[...]}` base64url, produced and parsed by the service; HTTP only passes the string through and applies `limit`/`next_cursor` envelope; projects, members, labels are returned whole and paged, if at all, by the HTTP layer); `Actor` with `Scope` and `ProjectID` populated from the token row (scope and project-limit enforcement already live in `requireProject`/`checkActor`; transports only map errors, e.g. project-limited 404); `ProjectService.Resolve` for key-or-ULID paths; `UserService` (phase 2 adds `ChangePassword` using the existing store queries); `config.Config` (incl. `TrustProxy`, `Origin()`); `store.FormatTime`/`ParseTime` and `store.Read()`/`WithTx` for session and token rows; `testutil.Env` for its test harness.
- **Phase 3 (events):** the complete seam in 6.5: `EventType` constants, `Event` (with `CommentID`, `LabelID`, `UserID`, `Renumbered`, `EventActor`), `Publisher`, `CloseReason`, `StreamControl` (the hub implements both `Publisher` and `StreamControl` and is passed through `Deps`), the internal `write`/`Tx` helper with `Emit` and `AfterCommit` already in place, and the emission catalog; fire-after-commit and no-op/rollback silence are already guaranteed and tested. Membership checks for SSE: `Projects.Resolve(UserActor(id), ref)` returns `ProjectRef{ID, Key, Role}` or `ErrNotFound`. Phase 3 only adds the hub and the HTTP handler.
- **Phase 4 (MCP):** ref-or-id inputs on every method; `Projects.List` (with role and counts), `Members.List` and `Labels.List` to build "available keys / emails / labels" error messages and to resolve emails and label names inside `mcpserver`; `TokenActor` (with scope and project limit) for audit rows and enforcement; `CommentService.Edit` (error code `not_author` vs `forbidden` vs `insufficient_scope`), `Comments.Resolve` (comment id to ticket ref and project key) and `Comments.Latest(ticket, 50)` for `get_ticket`; `Ticket` (with `Ref`, labels, `Assignee` email, `CommentCount`) and `Comment.TicketRef` for result shapes; `MoveInput` matching `place`/`before`/`after`; `CommentAuthor` for token display; `ticket_activity` already written by the service for every ticket change; exported enums, palette and `Max*` constants and the `Validate()` methods for the schema drift test; `Error.Fields` for validation messages.
- **Phase 5 (UI):** `MoveResult.Renumbered`, `Ticket` (assignee, labels, comment count on every item), project counts. Member removal unassigns tickets without per-ticket events, so the UI must refetch tickets on `member.changed`.
- **Phase 6:** `store.Open` + `Migrate` used by `serve`; `Store.Close` checkpoints the WAL; `DB_PATH` handling; `var version` in `package main`, the `Makefile` and the `version` subcommand already exist (phase 6 extends them with the `-ldflags` and extra targets).

---

## 11. Out of scope (this phase)

Signup/login/sessions/passwords/argon2id/rate limiting, `ChangePassword`; token creation, hashing, revocation, `last_used_at` (scope and project-limit enforcement is in scope, as service checks on `Actor`); any HTTP handler, middleware or error shape rendering; CLI subcommands other than the `serve` bootstrap; the event hub, SSE and stream closing; MCP; `web/`; Dockerfile, embedding, healthcheck, README and `CLAUDE.md` updates; ticket restore; user management services beyond `UpdateProfile`; project key renaming; attachments, notifications, custom columns.

---

## 12. Decisions applied

Confirmed in [phases.md](phases.md) "Decisions (resolved open questions)" and reflected above.

1. **Module path** is `github.com/RJGJ/Pabrika`; Go 1.23; a `Makefile` and a `version` subcommand are added (here; phase 6 extends them).
2. **Token scope and project limit** live in `Actor` (`Scope`, `ProjectID`) and are enforced in `requireProject`/`checkActor`; project-limited tokens get 404 elsewhere and cannot create projects.
3. **Archived projects are read-only** for ticket, label and comment writes (409 `project_archived`); owner-only project operations still work.
4. **Ticket `Update` cannot change status**; `Move` is the only way. Project key is immutable and uppercased.
5. **Limits:** comment body 20,000 (everywhere), project name 100, project description 2,000, label name 50; ticket title 200 and description 20,000 from the spec.
6. **Label palette:** `gray, red, orange, amber, green, teal, blue, indigo, purple, pink`.
7. **Comment edit:** exact author actor, still editor or owner, write scope; a human cannot edit an agent's comment; owners can delete.
8. **Project refs** accept key or ULID everywhere (`ProjectService.Resolve`, all project-taking methods).
9. **Account self-service:** `UserService.UpdateProfile` is built in phase 1; the password and session store queries are defined and tested here; `ChangePassword` (hashing, endpoint) is phase 2.
10. **`TRUST_PROXY`** (default false) is parsed in config now; used in phase 2.
11. **Unknown email on add member** is 422 `user_not_found`; removing a member unassigns their tickets (logged as `assigned`).
12. **Activity detail:** description changes truncated to 200 chars; label changes as name lists; assignee as user ids; none for label rename/delete or comments.
13. **Position constants** `Gap=1024`, `MinGap=1e-6`, `Top` as `min - Gap`; list order is status rank, position, id.
14. **In-memory test DB** uses one connection for read and write; concurrency is verified on a file DB.
15. **Events seam and extra indexes** stay; `serve` is a placeholder bootstrap in phase 1.

16. **Session-only at the service layer:** project delete, member add/role/remove and profile update refuse any token actor with `session_required` (defence in depth for main-spec's session-only rule).

**Assumptions not in phases.md (small, proposed):** display name 1..100 runes (provisional decision in phases.md; matches phase 2's signup rule; phase 2 owns the wire behaviour and calls `UpdateProfile` for `PATCH /auth/me`); error codes `not_author`, `session_required` and `invalid_cursor` at the service level; cursor format `{"v":1,"k":[...]}` owned by the service; `Ticket` carries assignee, labels and comment count on every item; `Comments.Latest`/`Resolve` and `ProjectSummary.TicketCounts` exist for phase 4; `BASE_URL` may not contain a path.

**Still unresolved:** none.

---

## 13. Reconciliation notes

- Display name limit is 1..100 everywhere (`MaxDisplayName = 100`, `UpdateProfile`, tests: 101 rejected, 100 accepted).
- Exit gate is `CGO_ENABLED=0 go test ./...`; `CGO_ENABLED=1 go test -race ./...` is a separate optional run (Makefile `test-race`).
- Phase 1 owns `Event`, `EventType`, `EventActor`, `EventActorOf`, `Publisher`, `StreamControl`, `CloseReason`, `Tx.Emit`, `Tx.AfterCommit`; Phase 3 adds only the hub (implements `Publisher` and `StreamControl`) and does not redefine them.
- `Tx.Emit` fills `Actor` and `At`; `s.write` takes the actor.
- Member removal emits only `member.changed` (tickets unassigned silently; UI reloads tickets); display-name change, unchanged project update (no `updated_at` bump) and identical comment edit emit nothing.
- `service.Error` exposes `Fields`, `NewError`, `Validation`; codes `not_author` (wire `forbidden`), `session_required`, `invalid_cursor` (KindBadRequest), `email_taken` (conflict, Phase 2), `user_not_found` (Fields email), `anchor_invalid` (Fields before/after) are documented.
- Activity `changes` keys are pinned to `title, description, priority, due_date, assignee, labels, status, position`.
- Phase 1 defines no token service, so the 100-active-tokens-per-user cap is Phase 2's (not added here).
