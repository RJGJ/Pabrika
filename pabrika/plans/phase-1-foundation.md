# Phase 1 plan: Foundation

Sources: `phases.md` (Decisions and the Phase 1 section), `main-spec.md` §Tech stack, §Data model, §Auth, §Layout/config, §Testing, and `phase-1-foundation.md` (called P1 below, with section numbers like §6.3). Phases 2 to 6 were also checked for what they expect from Phase 1.

Environment findings (verified when this plan was saved):
- **No toolchain:** `go`, `bun` and `docker` are not on PATH in Git Bash, and `sqlc`, `goose`, `gcc` and `make` were not found either. WP0 has to install Go before anything else. Bun is needed from Phase 5, Docker from Phase 6.
- **Git:** `D:\rj\Pabrika` is a git repository (remote `RJGJ/Pabrika`, branch `main`).
- **Module root:** the Go module root is `D:\rj\Pabrika\pabrika`. `CLAUDE.md` and `README.md` are one level up at `D:\rj\Pabrika`, and Phase 6 owns updating them.

## 0. Ground rules

- **Gate:** `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 go test ./...`, `go vet ./...` and `gofmt -l .` (must print nothing). The race run is optional and separate (P1 §1).
- **PowerShell form:** `$env:CGO_ENABLED='0'; go test ./...`. Git Bash form: `CGO_ENABLED=0 go test ./...`.
- **No `make`:** every Makefile target must map to a plain `go` command, so nothing depends on GNU make being installed.
- **Layers:**
  - `service` imports `store` and `store/db` only.
  - `store` is the only package that imports `modernc.org/sqlite`.
  - `service` must not import the driver.
  - `testutil` imports `service`, and tests live in external `service_test` packages.
- **Generated code:** sqlc output in `internal/store/db` is committed. Tests must not need the sqlc or goose CLIs.
- **Go version:** the `go` line in `go.mod` must stay at 1.23 (Phase 6 builds in `golang:1.23`). After every `go get`, run `grep -n "^go \|^toolchain" go.mod`. Set `GOTOOLCHAIN=local` so a dependency cannot silently bump the toolchain.
- **Test-first:** pure packages (position, refs, cursor, validate, optional, config, timefmt) get their table tests written before the implementation. Service packages get their test file skeleton and fixtures first, then fill in cases as the methods land.

## 1. Dependency graph and parallelism

```
WP0 toolchain+spike -> WP1 skeleton
WP1 -> WP2 config                      (parallel with everything below)
WP1 -> WP5 pure service foundations    (parallel with WP3/WP4)
WP1 -> WP3a migrations -> WP4a user/token queries + first sqlc generate -> WP3b store
WP3b + WP5 -> WP6 service core (service.go, authz, testutil)
WP6 -> WP7 projects+members -> WP8 labels -> WP9 tickets -> WP10 comments+activity
WP6 -> WP11 UpdateProfile             (parallel with WP7+)
WP2 + WP3b -> WP12 serve/version      (parallel with WP7+)
all -> WP13 hardening + DoD
```

Two people or agents can run in parallel from after WP1. Track A is WP2, WP5 and WP12. Track B is WP3a, WP4a, WP3b and WP6, then WP7 onward. Within WP9, the list and cursor work (9b) can proceed in parallel with update (9c) once 9a is done.

## 2. Work packages

### WP0. Toolchain and driver spike (0.5 day, blocking)

1. Install Go 1.23.x or newer. Verify with `go version` and `go env CGO_ENABLED GOTOOLCHAIN GOFLAGS`. Confirm no C compiler is needed for the gate.
2. Pick the sqlc and goose versions and record them in the plan or Makefile comments. Run sqlc as `go run github.com/sqlc-dev/sqlc/cmd/sqlc@<pinned> generate`. This needs no CLI install and does not touch `go.mod`.
3. If `go run ...sqlc@` fails on Windows (network or build), fall back to the prebuilt release `.exe` pinned by version, called through a `SQLC` variable. Do not add sqlc to `go.mod`, because it would pull a huge dependency tree and may raise the `go` line.
4. Run a throwaway spike in the scratchpad directory, not in the repo, to verify these `modernc.org/sqlite` behaviours on Windows:
   - A file DSN with `_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate` and a `t.TempDir()` path with a drive letter and backslashes. Try `file:C:/x/y.db?...` and `file:///C:/x/y.db?...` and keep the form that works. Build the DSN with `url.URL`, not string concatenation.
   - `BeginTx(ctx, &sql.TxOptions{ReadOnly: true})` is accepted. If not, `WithReadTx` uses a plain deferred `Begin` on the `query_only` pool, which still gives a WAL snapshot after the first read.
   - A `query_only(1)` read connection opens fine against a WAL file. Order the pragmas so `journal_mode(WAL)` comes before `query_only`.
   - `:memory:` with `SetMaxOpenConns(1)` and lifetime and idle limits at 0 keeps its data across calls. A cancelled context followed by reuse does not drop the connection.
   - `UPDATE ... RETURNING` and a `*sqlite.Error` with code 2067 or 1555 on a UNIQUE or PRIMARY KEY violation.
5. **Done when:** the pinned versions are written down and every spike item has a known answer.

### WP1. Skeleton (0.25 day)

- Create `go.mod` (`module github.com/RJGJ/Pabrika`, `go 1.23`) and the directory tree from P1 §2.
- Create `.gitignore` (`data/`, `*.db*`, `*.db-wal`, `*.db-shm`) and `Makefile` (`build`, `test`, `vet`, `generate`, `test-race`, all one-liners).
- Create `sqlc.yaml`: engine sqlite, schema `migrations/`, queries `internal/store/queries/`, out `internal/store/db`, `package: db`, `emit_interface: true`, `emit_json_tags: false`, `emit_pointers_for_null_types: true`. The pointer option gives `*string` and `*time` style nullables instead of `sql.NullString`. Confirm the pinned sqlc version supports it.
- Add a `//go:generate` line in `internal/store/store.go`.
- Add a `package main` stub with `var version = "dev"`.
- **Verify:** `CGO_ENABLED=0 go build ./...` succeeds.

### WP2. Config (0.25 day, size S, about 150 lines plus 150 lines of tests)

- **Files:** `internal/config/config.go`, `config_test.go`.
- **API:** `Load(getenv)` and `Config.Origin()` exactly as P1 §4.
- **Details:**
  - Parse bools with `strconv.ParseBool`.
  - Validate `BASE_URL` with `net/url`: scheme http or https, host required, path empty or `/`, no query or fragment. Trim the trailing slash.
  - Make `Origin()` lowercase the scheme and host and drop the default ports (80 for http, 443 for https).
  - Error messages must name the variable.
- **Tests first, table-driven (P1 §9 Config):**
  - defaults, and each variable overridden.
  - bad `PORT` (0, 65536, "abc"), bad bool, `BASE_URL` with a path, query or fragment, and `BASE_URL` with no scheme.
  - `Origin()` cases: `HTTP://Example.com:80/` gives `http://example.com`, and `https://x:8443` keeps the port.
- **Run:** `CGO_ENABLED=0 go test ./internal/config/...`

### WP3a. Migration (0.25 day, size S)

- **Files:** `migrations/00001_init.sql`, `migrations/embed.go` (`//go:embed *.sql`, `var FS embed.FS`).
- **Contents:** copy the ten tables from main-spec §Data model verbatim, in dependency order. Add the three extra indexes (`activity_by_ticket (ticket_id, created_at, id)`, `sessions_by_user`, `tokens_by_user`). Write `-- +goose Down` as drops in reverse order.
- **Quirk to avoid:** the partial index `tickets_board ... WHERE deleted_at IS NULL` contains no semicolons that need goose `StatementBegin` blocks, but check the file parses in both goose and sqlc.
- **Verify:** covered by the store migration test in WP3b. Until then, run `sqlc generate` in WP4a and confirm it parses the schema.

### WP4a. First sqlc slice and generate (0.25 day)

- **Query files:** `queries/users.sql` with `CreateUser`, `GetUserByID`, `GetUserByEmail`, `ListUsersByIDs` (`sqlc.slice`), `UpdateUserDisplayName`, `UpdateUserPassword`, `DeleteSessionsForUserExcept`, `CreateAPIToken`, `GetAPIToken`. Add a minimal `CreateSession` for tests (the store test for `DeleteSessionsForUserExcept` needs session rows, and Phase 2 will extend it).
- **Run:** `go generate ./internal/store/...`, commit `internal/store/db/*`.
- **Why now:** `store.go` needs `db.Queries`, so this unblocks WP3b. The remaining query files are written alongside the service package that uses them. This avoids dead or wrong queries and keeps each regenerate small.
- **sqlc caveats to check at the first generate:**
  - Do not use row-value comparisons `(a,b,c) > (?,?,?)` in keyset queries. Use the expanded OR form, which parses cleanly.
  - `sqlc.narg` needs a type hint, so use `CAST(sqlc.narg(x) AS TEXT)` where inference fails.
  - The `RETURNING next_ticket_number - 1 AS number` alias must work. Add `CAST(... AS INTEGER)` if sqlc infers `interface{}`.
  - `sqlc.slice` for the batched `IN` lists is supported for SQLite.

### WP3b. Store (1 day, size M, about 350 lines plus 300 lines of tests)

- **Files:** `internal/store/{store.go, pragma.go, timefmt.go, store_test.go}`.
- **Steps:**
  1. `timefmt.go` and its tests: fixed-width `2006-01-02T15:04:05.000Z`, UTC-normalised, lexically sortable, round-trip.
  2. `pragma.go`: DSN builder for a file DB (write and read variants) and for memory.
  3. `Open`: `MkdirAll` the parent directory, open the write pool (max open 1, max idle 1, `_txlock=immediate`), ping, then open the read pool (`max(4, NumCPU)`, `query_only(1)`). The write pool opens first so WAL is established.
  4. `OpenMemory`: a single pool of one connection serving both read and write, with `SetConnMaxLifetime(0)` and `SetConnMaxIdleTime(0)`.
  5. `Migrate`: goose Provider API with the sqlite3 dialect and `migrations.FS` through the write pool. Detect "database newer than embedded" by comparing `GetDBVersion` with the last source version, and return an error without changing anything.
  6. `Close`: best-effort `PRAGMA wal_checkpoint(TRUNCATE)` on the write pool, then close both pools.
  7. `WithTx` and `WithReadTx` (rollback on error and on panic, re-raise the panic).
  8. A statement counter: a `db.DBTX` wrapper that increments an atomic counter, exposed as `Store.QueryCount()`. This supports the "statement count does not grow with page size" test in WP9b without any driver hooks. Decide this now, because it changes how `db.New` is constructed.
  9. `IsUniqueViolation(err) bool` and `IsUniqueViolationOn(err, "projects.key")` in the store, using `errors.As` on `*sqlite.Error` with codes 2067 and 1555, so `service` stays driver-free (P1 §7.1 backstops).
- **Tests (P1 §9 Config/store):**
  - Migration on a fresh DB, re-run is a no-op, the ten tables and `tickets_board` exist, `PRAGMA foreign_keys` is 1, and Down then Up works (an unexported or `export_test.go` helper reaches the goose provider).
  - Newer-DB test: insert a fake higher version row into `goose_db_version`, then `Migrate` errors and nothing changes.
  - File DB tests: `journal_mode` is `wal`, `busy_timeout` is 5000, `write.Stats().MaxOpenConnections == 1`, and `Close` leaves a zero-length or absent `-wal`.
  - `WithTx` rollback on error and on panic.
  - Nested-`WithTx` deadlock guard under a 200 ms context timeout: it must fail fast, not hang. Also nested `Read()` on the memory store.
  - A second `Store` on the same file calling `Migrate` concurrently. Goose has no cross-process lock on sqlite, so if two migrators race the loser can hit "table already exists". Mitigation: on a `Migrate` error, re-read the version and return nil if it is already current. This risk is low with a single migration, but keep the test.
  - Store tests for the `UpdateUserDisplayName`, `UpdateUserPassword` and `DeleteSessionsForUserExcept` queries (P1 §9 User profile).
- **Run:** `CGO_ENABLED=0 go test ./internal/store/...`

### WP5. Pure service foundations (1 day, size M, about 700 lines plus 800 lines of tests, parallel with WP3/WP4)

All files are in `internal/service/` and need no DB.

| File | Content | Tests (write first) |
|---|---|---|
| `errors.go` | `Kind`, `Error`, sentinels, `NewError`, `Validation(fields)`, `Is` by Kind | `errors.Is(err, ErrNotFound)`; `Validation` message is the lowest-sorted field |
| `actor.go` | `Actor`, `UserActor`, `TokenActor`, `Role.AtLeast/Valid`, `Scope` | role ranking; empty scope behaves as write |
| `optional.go` | `Optional[T]`, `Some`, `Null`, `UnmarshalJSON` | absent, `null` and value decode to three distinct states, for `Optional[string]`, `Optional[*string]`, `Optional[[]string]` |
| `validate.go` | enums, `Statuses`, `Priorities`, `LabelColors`, `Max*` constants, `Validate()` on all 7 input structs | drift test equals documented values; `Fields` keyed by JSON name; null on a non-nullable field gives "must not be null"; nil labels normalised; rune counts for lengths (201 titles, 100 and 101 rune display names) |
| `refs.go` | ULID vs project key vs `KEY-N` classification | lowercase ULID accepted; `web-12` accepted; garbage gives `ErrNotFound`; `WEB-0` and `WEB-012` rejected |
| `cursor.go` | base64url (no padding) of `{"v":1,"k":[...]}` encode and decode | round-trip including floats (`1/3`, `1e-7`, `-1024`); wrong version, shape or base64 gives `invalid_cursor` (`KindBadRequest`) |
| `position.go` | `Gap`, `MinGap`, `PlanMove(neighbours, idx)`, `Renumber(n)` | P1 §9 Position math (empty, single and multi-item columns; repeated top inserts go negative; sub-`MinGap` and equal positions force renumber; renumber gives `Gap*i`) |
| `events.go` | `EventType` constants, `Event`, `EventActor`, `EventActorOf`, `Publisher`, `NopPublisher`, `CloseReason`, `StreamControl` | `json.Marshal(Event)` yields only documented keys; `EventActorOf` is `{type,id}` only |

- **Optional spike:** `encoding/json` must call `UnmarshalJSON` for a literal `null` on a non-pointer struct field. If it does not, the `Null` state is lost. This is the main reason the test comes first.
- **`validate.go` rule:** it is pure with no DB or clock, and it never checks "assignee is a member".
- **Decision:** export `PlanMove`, `Gap` and `MinGap`, so the tests can stay in the external package.
- **Run:** `CGO_ENABLED=0 go test ./internal/service/...`

### WP6. Service core and testutil (1 day, size M, about 500 lines)

- **Files:** `internal/service/{service.go, authz.go}`, `internal/testutil/testutil.go`.
- **`service.go`:**
  - `Deps` with defaults (clock is UTC `time.Now`, a mutex-guarded `ulid.Monotonic` id source, `NopPublisher`, no-op `StreamControl`).
  - `Services`, `New`, the `Tx` type (`Q`, `Emit`, `AfterCommit`), and `s.write(ctx, actor, fn)`.
  - `s.write` wraps `store.WithTx`. It queues events and after-commit funcs, discards them on error or panic, and after a successful commit publishes events in order and then runs the `AfterCommit` funcs. `Emit` fills `Actor` and `At`.
  - `s.read` helpers (`Read()` and `WithReadTx` wrappers). Shared internals take `q *db.Queries`, never the store.
- **`authz.go`:**
  - `resolveProject(ctx, q, actor, ref)`: one query joining projects and `project_members`, matching by key (case-insensitive) or id and by user id.
  - `requireProject` with the P1 §6.3 order: step 0 session-only, then 404 (non-member), then the project-limit 404, then scope 403 `insufficient_scope`, then role 403 `forbidden`, then archived 409 (writes only).
  - `checkActor`, and effective role (capped to viewer for read scope).
  - `resolveTicket`, `resolveLabel` and `resolveComment`. Each looks up the owning project id (soft-deleted ticket and comment resolve to not found), then calls `requireProject`. Unknown id and non-member must give the same message.
  - A `sessionOnly` flag or helper for the step-0 callers.
- **`testutil.Env`:**
  - `NewTestServices(t)` uses `OpenMemory` plus `Migrate`, a fake clock that only moves when told, a deterministic monotonic ULID source driven by that clock, a recording publisher and a recording `StreamControl`.
  - It exposes `Store`, `Svc`, `Clock`, `Pub`, `Streams`, `NewUser`, `NewToken`, `UserActor`, `TokenActor` (placeholder password hash, no secret).
  - `NewFileEnv(t)` is the same on a temp-dir file DB, for the concurrency tests.
  - A reusable matrix helper: `type Case struct{ Role; ActorKind; Op func(...) error; Want Outcome }`. Build it now so every later package adds rows instead of new harness code.
- **Queries needed:** project get-by-ref with membership, `GetTicketProject`-style lookups. Add them in this WP and regenerate.
- **Tests:**
  - `s.write` publishes after commit with the DB state visible, and publishes nothing on error, panic or a no-op.
  - Events are ordered before the after-commit funcs.
  - `Actor` with an invalid `Type` gives `ErrForbidden` (and a panic in tests, per P1 §6.1).
- **Run:** `CGO_ENABLED=0 go test ./internal/service/... ./internal/testutil/...`

### WP7. Projects and members (1.5 days, size L, about 700 lines)

- **Files:** `projects.go`, `members.go`, `queries/projects.sql`, `queries/members.sql`.
- **Projects:**
  - **Create:** trim and uppercase the key, validate against `^[A-Z]{2,6}$`, and map a duplicate to `key_taken`. In one tx, insert the project and the owner membership. Check scope and the project-limit rule via `checkActor`.
  - **Get:** `ProjectDetail` with effective role and counts through `WithReadTx` (project plus one grouped query), all four statuses present.
  - **Resolve:** returns `ProjectRef`.
  - **List:** a limited token sees only its project, archived excluded unless asked, counts from one grouped query for all projects. Order is per Open question 1.
  - **Update:** archive and unarchive, with a no-op detector (no `updated_at` bump, no event).
  - **Delete:** a hard delete relying on FK cascades, then `Streams.CloseProject` via `AfterCommit`.
- **Members:**
  - **List:** owners first, then `lower(display_name)`, then user id.
  - **Add:** look up by email case-insensitively, `user_not_found`, `already_member`, plus the UNIQUE backstop.
  - **SetRole:** the last-owner check counts owners inside the same write tx, and a same-role call is a no-op.
  - **Remove:** an owner removes any member, or a member removes themselves (leave); last owner gives `last_owner`. Clear the assignee on all of that user's tickets in the project (including soft-deleted ones) and write an `assigned` activity row only for live tickets (this needs `queries/activity.sql` insert and `queries/tickets.sql` clear-assignee, so stub those two queries now). Emit `member.changed`, then `CloseUser(CloseRemoved)` after commit.
- **Tests (P1 §9):**
  - Project key validation, key and ULID refs equivalent, the archived no-op.
  - The role matrix rows for projects and members.
  - Session-only: a token actor gets `session_required` even for a non-member project or invalid input.
  - Every last-owner scenario, including the concurrent demotion on `NewFileEnv`: two goroutines demote each other, exactly one wins and the other gets `last_owner`.
  - Cascade completeness: after `Projects.Delete`, assert every table has zero rows for the project (count query per table).
  - Event catalog rows for the project and member events (6.5).
- **Run:** `CGO_ENABLED=0 go test ./internal/service/ -run 'Project|Member'`

### WP8. Labels (0.5 day, size S, about 200 lines)

- **Files:** `labels.go`, `queries/labels.sql`.
- **Behaviour:**
  - `List`, `Create`, `Update`, `Delete`, each resolved through its project.
  - `label_exists` on duplicates, case-insensitive, with the `UNIQUE (project_id, name)` backstop.
  - A rename that only changes the casing of the label's own name is allowed.
  - Palette enforcement and a default colour of `gray`.
  - Delete cascades `ticket_labels`.
  - `label.changed` is emitted for create, update and delete. An update to identical values emits nothing.
  - No activity rows are written for label changes.
- **Tests:** uniqueness (including rename conflict), cascade off tickets (extended in WP9), the archived project gives `project_archived`, and the matrix rows.

### WP9. Tickets (2.5 days, size XL, about 1,100 lines). Split into five shippable steps.

**9a. Create, Get, Resolve, counter, activity writer (about 0.5 day)**
- **Queries:** `queries/tickets.sql` and `queries/activity.sql` insert.
- **Counter:** the atomic `UPDATE projects SET next_ticket_number = next_ticket_number + 1 ... RETURNING` inside the create tx.
- **Position:** bottom of the target column.
- **Hydration:** `hydrateTickets(q, rows)` loads labels, assignees and comment counts for any slice of tickets in three batched queries (`sqlc.slice`). Use it from every path (create, update, move, get, list).
- **Activity helper:** `recordActivity(q, actor, ticketID, action, changes)` that enforces the allowed `changes` keys.
- **Create activity:** the `created` row's changes cover the fields set at creation (see Open question 5).
- **Tests:**
  - `KEY-1` is the first ticket and counters are per project.
  - 20 concurrent `Create` calls on a file DB give unique, consecutive numbers 1..20 and no "database is locked".
  - Create rollback (an invalid label after the counter increment) leaves no gap.
  - Hydrated fields are present on Create and Get.
  - Ticket ref case-insensitivity and a garbage ref give `ErrNotFound`.

**9b. List and cursor (about 0.5 day)**
- Filters for status, priority, assignee (including unassigned), label and query.
- `Query` is trimmed and `%`, `_`, `\` are escaped with `LIKE ? ESCAPE '\'` over title and description.
- Order is status rank, position, id, using an explicit `CASE` rank. The cursor is `[rank, position, id]` with `limit+1` fetch. Use the expanded OR form for keyset comparison (see WP4a).
- Description is not selected, so list items have `Description == ""`.
- **Tests:**
  - Each filter, and the invalid status filter gives `ErrValidation`.
  - Default and max limits.
  - Pagination across status boundaries, with equal positions, and with inserts between pages.
  - A malformed cursor gives `invalid_cursor`.
  - `Store.QueryCount()` deltas are the same for a 5-item and a 50-item page (batching proof).
  - A float position round-trips exactly through the cursor (use `1/3` and `1e-7` positions).

**9c. Update (about 0.5 day)**
- Only `Set` fields change. Use change detection against current values, with label sets compared as sets.
- Validate the assignee is a project member and the labels belong to the project, in the method (the DB-dependent checks).
- Pick the action: `assigned` if only the assignee changed, `labeled` if only labels changed, otherwise `updated`. The changes use label names, assignee user ids and a description truncated to 200.
- A no-op writes no activity, no event and no `updated_at` bump.
- **Tests:** `Optional` semantics (omit vs null for assignee and due date), action selection, `changes` content, label replace, and the validation limits (200/201, 20,000/20,001, bad date, bad enums).

**9d. Move (about 0.75 day, the riskiest logic)**
- Compute the target column without the moved ticket (`ORDER BY position, id`). Resolve `Before`/`After` anchors with the checks (same project, target status, not deleted, not self) and give `anchor_invalid` otherwise.
- Get the insert index (top is 0, bottom is len, before X is idx(X), after X is idx(X)+1) and call `PlanMove`. On `renumber=true`, rewrite positions `Gap*i` for the other tickets and recompute against the fresh positions in the same tx.
- No-op iff the status is unchanged and the new insert index equals the ticket's current index among the others. A no-op returns early before any write or emit.
- Write the `moved` activity (the status key only when it changed). Emit `ticket.moved` with `Renumbered`.
- **Tests (P1 §9 Ordering):**
  - Each placement mode and the default.
  - Same-column and cross-column moves.
  - Self excluded from the neighbour computation.
  - 60 repeated inserts between the same two tickets force a renumber, `Renumbered` is true, and the final order is correct.
  - All invalid-anchor kinds, and both `Before` and `After` set gives `ErrValidation`.
  - A no-op publishes nothing and a real move afterwards still works.
  - Soft-deleted tickets never take part in ordering.
  - Concurrent `Move` calls into the same gap on a file DB: all succeed and positions end up distinct.

**9e. Delete (about 0.25 day)**
- Soft delete, a `deleted` activity row, `ticket.deleted`, and a second delete gives `ErrNotFound`.
- **Tests:** the P1 §9 Soft delete rows for tickets (the row is retained, the counter does not decrease, absent from list, counts, get, resolve and anchors).

### WP10. Comments and activity listing (1 day, size M, about 450 lines)

- **Files:** `comments.go`, `activity.go`, `queries/comments.sql`, plus the activity list query.
- **Comments:**
  - `List` (oldest first by `(created_at, id)`, cursor) and `Latest(n)` (fetch the newest n+1 in descending order, flip to oldest-first, clamp n to 1..200).
  - `Add` (author is the actor's `(Type, ID)`).
  - `Edit` and `Delete` follow P1 §7.5, with the check order given in Open question 4.
  - Author names via LEFT JOINs: the token name plus owner name, or "deleted user" / "deleted token".
  - `Resolve` returns the ticket ref and project key.
  - `Ticket.CommentCount` stays correct.
  - Events `comment.added` and `comment.changed`. An identical-body edit is a no-op.
- **Activity:** `List` is newest first by `(created_at, id)` with a cursor, and the `changes` JSON decodes into `map[string][2]any`.
- **Tests:**
  - Same-millisecond ordering relies on the monotonic ids.
  - The edit and delete permission rows: author, non-author editor (`not_author`), demoted author (`forbidden`), owner deletes an agent's comment, a human cannot edit a token's comment and vice versa, archived gives `project_archived`.
  - A missing user or token row yields the "deleted ..." names with no error.
  - A comment on a soft-deleted ticket gives `ErrNotFound`.
  - `Latest` truncation flag.
  - Activity survives ticket soft delete.

### WP11. User profile (0.25 day, size S, parallel with WP7+)

- **Files:** `users.go` with `UpdateProfile`.
- **Behaviour:** a token actor gets `session_required` before any lookup. Trim and validate 1..100 runes with `Fields["display_name"]`. Return a `User` with `CreatedAt`. It emits no event.
- **Tests:** empty and 101-rune names are rejected, 100 accepted, multibyte counted as runes.

### WP12. `serve` and `version` (0.25 day, size S, parallel)

- **File:** `cmd/pabrika/main.go`.
- **Behaviour:**
  - Subcommand dispatch with a clear usage message on an unknown command.
  - `serve` calls `config.Load(os.Getenv)`, then `store.Open` and `Migrate`, logs the schema version with `slog`, closes the store and exits 0.
  - `version` prints `version` (overridable by `-ldflags "-X main.version=..."`).
- **Verify:**
  - `CGO_ENABLED=0 go run ./cmd/pabrika serve` with `DB_PATH` pointing at a temp directory that does not yet exist.
  - Run it twice (idempotent) and check exit 0 and the logged version.
  - `go run ./cmd/pabrika version`.

### WP13. Hardening and definition of done (1 day)

- Add the cross-cutting tests:
  - the order-of-checks test with a single input that is invalid, forbidden and in an archived project (P1 §6.3, P1 §9 roles).
  - the complete role and scope matrix across every method.
  - the event-catalog test, one row per catalog entry.
  - the `Events seam` JSON key assertions and the `StreamControl` calls.
- Run the full gate and `gofmt -l .`.
- Optionally run the race build `CGO_ENABLED=1 go test -race ./...` if a C toolchain exists. It is not part of the gate.
- Review each package for any import of `net/http` or MCP types in `service`.
- Re-read the P1 §9 test list and tick every item.

## 3. Definition of done mapped to exit criteria

| Exit criterion (`phases.md`, P1 §1) | Proof |
|---|---|
| `go build` and `go test ./...` pass with no CGO | `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 go test ./...`, `go vet ./...`, `gofmt -l .` all clean |
| Service tests cover ordering | WP5 position tests, WP9d Move tests, 60-insert renumber test, concurrent Move test |
| Role and permission rules | WP6 matrix helper plus rows in WP7 to WP11, token scope and project-limit rows, order-of-checks test (WP13) |
| Last-owner protection | WP7 tests, including the concurrent demotion on a file DB |
| Soft delete | WP9e and WP10 tests, plus the project hard-delete cascade test |
| `serve` bootstraps and exits 0 | WP12 manual run, plus the idempotent second run |
| Generated sqlc code committed | `git status` clean after `go generate ./...` |
| Seams for later phases | event catalog test, `Resolve` methods, exported enums and `Validate()`, `store.FormatTime`, `Store.Close` checkpoint, `var version` |

## 4. Risks and how to de-risk them

| Risk | De-risk |
|---|---|
| No Go toolchain installed here | WP0 first. Pin versions, use `GOTOOLCHAIN=local`, and keep the `go` line at 1.23. |
| `modernc.org/sqlite` DSN, `_txlock`, read-only tx and the memory DB on Windows | The WP0 spike answers each one before store code is written. Build DSNs with `url.URL`. Run store tests on `t.TempDir()` on Windows. |
| sqlc and goose tooling on Windows | Use `go run pkg@version`, or a prebuilt pinned `.exe` as the fallback. Never require the CLIs for tests, since generated code is committed. Keep the goose Provider API (no globals). |
| sqlc type inference (nullable columns, `narg`, `RETURNING` aliases, `sqlc.slice`) | Generate early (WP4a) and per domain. Use `CAST(...)` hints. Check generated types before writing service code. |
| Transactions and deadlock on the single connection | Callbacks use only the `q` they are given. The nested-`WithTx` timeout test is mandatory. Never hold `Rows` open while calling a write on the memory store. |
| Memory DB loses data if the connection recycles | `SetConnMaxLifetime(0)` and `SetConnMaxIdleTime(0)`. A test that cancels a context and then reuses the DB. |
| Ordering and renumbering bugs | Pure `PlanMove` tested first. The 60-insert test, the concurrency test, and a randomised model test (random moves compared against an in-memory slice reference) as a cheap extra in WP13. |
| Cursor stability | The cursor uses `(rank, position, id)` with float round-trip tests, equal-position tests and insert-between-pages tests. Use the OR-expanded keyset predicate, not row values. |
| Goose migrate race across processes | Retry-check on error (WP3b) and a two-store test. |
| Dependency bumps the `go` line above 1.23 | Check `go.mod` after every `go get`. Pin `modernc.org/sqlite`, goose and ulid to versions that build on 1.23. |
| Check-order drift between methods | One `requireProject` helper and one order-of-checks test over every write method. |

## 5. Size estimate

| Package | Production Go | Tests | Effort |
|---|---|---|---|
| WP0 spike | throwaway | none | 0.5 day |
| WP1 skeleton | small | none | 0.25 day |
| WP2 config | about 150 lines | about 150 | 0.25 day |
| WP3a/3b/4a store and migration | about 450 lines plus about 150 lines SQL | about 300 | 1.5 days |
| WP5 pure foundations | about 700 | about 800 | 1 day |
| WP6 service core and testutil | about 500 | about 300 | 1 day |
| WP7 projects and members | about 700 | about 900 | 1.5 days |
| WP8 labels | about 200 | about 250 | 0.5 day |
| WP9 tickets | about 1,100 | about 1,500 | 2.5 days |
| WP10 comments and activity | about 450 | about 600 | 1 day |
| WP11 and WP12 | about 150 | about 100 | 0.5 day |
| WP13 hardening | small | about 300 | 1 day |
| Total | about 4,500 lines Go, about 600 lines SQL | about 5,000 lines | about 11 to 12 days single-track; about 8 to 9 days with two tracks |

## 6. Open questions

1. **`Projects.List` order conflict.** P1 §6.4 and §9 say key ascending. Phase 2 (§0 item d and `GET /projects`) and Phase 5 say name then key. `main-spec.md` is silent. Proposed: implement name (case-insensitive) then key now, since both consumers expect it, and change the P1 test line. (The Phase 2 plan assumes the same.)
2. **Phase 2 note about the role is stale.** Phase 2 §0 says Phase 1 returns the raw member role and the view layer caps it. P1 §6.3 and Phase 4 say Phase 1 already returns the effective role. Follow P1 (it wins and Phase 4 depends on it). Phase 2 should drop its extra cap.
3. **Check order for `Comments.Edit` and `Comments.Delete` and `Members.Remove`.** The spec defines the outcomes per case but not the order between `forbidden` and `not_author`. Proposed: role (`forbidden`) before authorship (`not_author`), so a non-author viewer gets `forbidden`, a non-author editor gets `not_author`, and an editor removing someone else gets `forbidden` before any member lookup. These are small and testable.
4. **`created` activity contents.** The spec says "fields set at creation" with title and status as examples. Proposed: always `title`, `status`, `priority`, plus `description` (truncated to 200), `due_date`, `assignee` and `labels` only when set, with no `position`.

(The numbering differs from the original planner output, which also asked whether to `git init`; that is moot because the repo already exists.)

No question blocks starting WP0 to WP6.

### Critical files for implementation
- `pabrika/specs/phase-1-foundation.md`
- `migrations/00001_init.sql` (to create)
- `internal/store/store.go` (to create)
- `internal/service/authz.go` (to create)
- `internal/service/tickets.go` (to create)
