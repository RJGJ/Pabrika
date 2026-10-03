# Phase 3: Live updates (detailed spec)

Parent docs: [phases.md](phases.md), [main-spec.md](main-spec.md) (wins on any conflict; the "Decisions" and "Provisional decisions" sections of phases.md override both). Sections used: Live updates, REST API (events endpoint), Security checklist, Testing.

**Goal:** every write, from any caller, is announced to everyone who has the project open, over SSE, without ever carrying ticket content.

**What this phase delivers:** the in-memory `Hub` (it implements Phase 1's `Publisher` and `StreamControl`), the SSE handler, hub options, the hub's part of graceful shutdown, and the tests. **What it does not deliver:** the event types, `Event`, `EventActor`, `CloseReason`, the `Tx.Emit` / `Tx.AfterCommit` write helper, `Publisher`, `StreamControl`, `NopPublisher`, the `Deps` fields and the per-method emit hooks. Phase 1 owns all of those (Phase 1 section 6.5); Phase 3 must not redefine them and only tests that they behave as the catalog says once a real hub is attached.

**Out of scope:** multi-process fan-out, event replay or persistence, the browser client (Phase 5), MCP tools (Phase 4), per-user stream caps, metrics, events for display-name changes.

---

## 1. What this phase expects from Phases 1 and 2

Names below are the ones in the Phase 1 and Phase 2 specs; reuse whatever the code actually has, never add a parallel type.

**From Phase 1 (`internal/service`)**
- `service.New(st *store.Store, deps Deps) *Services` with `Deps{Clock, NewID, Publisher, Streams}`. A nil `Publisher` means `NopPublisher`; a nil `Streams` is a no-op. `cmd/pabrika` sets `deps.Publisher = hub` and `deps.Streams = hub` from one `*Hub`.
- `events.go` (Phase 1): `EventType` and the nine constants (`EventTicketCreated`, `EventTicketUpdated`, `EventTicketMoved`, `EventTicketDeleted`, `EventCommentAdded`, `EventCommentChanged`, `EventLabelChanged`, `EventMemberChanged`, `EventProjectUpdated`), `EventActor{Type ActorType, ID string}` (json `type`, `id`), `Event{Type, ProjectID, TicketID, CommentID, LabelID, UserID, Renumbered, Actor, At}` (json `type, project_id, ticket_id?, comment_id?, label_id?, user_id?, renumbered?, actor, at`), `Publisher{Publish(Event)}`, `NopPublisher`, `CloseReason` with `CloseNone, CloseSlow, CloseRemoved, CloseProject, CloseShutdown, CloseClient`, and `StreamControl{CloseUser(projectID, userID string, reason CloseReason); CloseProject(projectID string, reason CloseReason)}`.
- The write helper: every mutating method runs `s.write(ctx, func(tx *Tx) error)`; `tx.Emit(Event)` queues an event and `tx.AfterCommit(func())` queues a stream action. After a successful commit the helper calls `Publisher.Publish` for each queued event in order, then runs the `AfterCommit` funcs in order; on error, rollback or panic both queues are discarded. Phase 1 sets `Event.At` from `Deps.Clock` and builds the actor from the method's `Actor` (`{Type, ID}`; for a token the TOKEN id, never the owner's user id).
- Phase 1 already wires the emits and the access-loss closes: `Members.Remove` (including leave) emits `member.changed` and then `Streams.CloseUser(project, user, CloseRemoved)`; `Projects.Delete` emits nothing and calls `Streams.CloseProject(project, CloseProject)`. Phase 1's recording publisher and recording `StreamControl` in `testutil` stay; this phase adds an option to `testutil.NewTestServices` to attach a real `*service.Hub` instead (test-only, no change to Phase 1 types).
- `Projects.Resolve(ctx, actor, ref) (ProjectRef{ID, Key, Role}, error)`: key or ULID, through the membership join, so a non-member, a missing project and an out-of-limit token are all `ErrNotFound`. It works for archived projects. It is the membership check for the stream.
- Service interfaces named `ProjectService`, `MemberService`, `LabelService`, `TicketService`, `CommentService`, `ActivityService`, `UserService` (Phase 2 adds `Users.Create` and friends and a `TokenService`; they are no-event, see Section 4).

**From Phase 2 (`internal/httpapi`, `internal/auth`)**
- `Server.Mount(pattern, Access, handler)` with `Access` = `Public | Authed | SessionOnly`; the events route is `SessionOnly`, `Write=false`, and appears in `Server.Routes()` so the "every route has an explicit Access" test covers it. Guard order: 401 `unauthorized`, then 403 `session_required` for a token on a `SessionOnly` route, then the handler.
- `Principal` with `User`, `Method == "session"` and `Principal.Session.TokenHash` (sha256 hex of the cookie value). `Principal.Actor()` exists, but the stream uses the user's own actor (Section 5): `service.UserActor(principal.User.ID)`.
- `auth.Sessions.Validate(ctx, tokenHash) error`: read-only (no sliding, no cookie, no write); returns nil for a live row, `auth.ErrInvalidCredentials` when the row is missing or `expires_at <= now` (injectable clock), any other error for DB failures.
- `httpapi.WriteError(w, status, code, message)` writes the standard error shape. The events handler uses it for every pre-stream error (404 `not_found`, 503 `unavailable`).
- 503 `unavailable` is an allowed extra status (phases.md "Decisions": 405, 415, 429, 500 and 503). Phase 2 emits it nowhere itself; this phase's stream handler is the only producer.
- The request-log, security-headers and body-cap wrappers implement `Flush()` and `Unwrap() http.ResponseWriter` so `http.ResponseController` reaches the real writer (Phase 2 section 3). Task 5 verifies it with a test; it fixes a wrapper only if the test fails.
- Server timeouts per Phase 2: `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `IdleTimeout` 120 s, **no `WriteTimeout`**.
- **No-store exemption:** Phase 2 sets `Cache-Control: no-store` on `/api/` responses. The SSE response must end up with `Cache-Control: no-store, no-transform`, so that middleware must either run before the handler (so the handler's own `Header().Set` wins) or skip the events route. A test asserts the final header on the real chain (Section 8).
- **No compression or buffering middleware** anywhere in the app. Compression is the reverse proxy's job (Phase 6 excludes the events path).

---

## 2. Event contract (owned by Phase 1; reproduced for the wire and for tests)

**Wire example** (exactly one JSON line in `data:`; the actor is `{type, id}` only):

```text
event: ticket.moved
data: {"type":"ticket.moved","project_id":"01J...","ticket_id":"01J...","renumbered":true,"actor":{"type":"api_token","id":"01J..."},"at":"2026-10-03T12:00:00.000Z"}

```

**Payload rule:** ids, type, actor `{type, id}`, timestamp and the `renumbered` flag only. No title, description, comment body, label name, email, display name or status/position values. `service.Actor` fields `scope`, `project_id` and `user_id` never appear. The client refetches, so permission checks always apply. A test pins the exact allowed key set.

**Catalog** (the Phase 1 section 6.5 table; Phase 3 tests assert it against a real hub, it does not re-implement it):

| Service write | Event | Ids set |
|---|---|---|
| `Tickets.Create` / `Update` / `Move` / `Delete` | `ticket.created` / `ticket.updated` / `ticket.moved` (`renumbered` when the column was renumbered) / `ticket.deleted` | ticket |
| `Comments.Add` | `comment.added` | ticket, comment |
| `Comments.Edit`, `Comments.Delete` | `comment.changed` | ticket, comment |
| `Labels.Create/Update/Delete` | `label.changed` | label |
| `Members.Add`, `SetRole`, `Remove` (also leave) | `member.changed` | user (the affected member) |
| `Projects.Update` (rename, description, archive or unarchive) | `project.updated` | none |
| `Projects.Delete` | no event; `StreamControl.CloseProject` after commit | |
| `Projects.Create`, `Users.UpdateProfile` and every other user, session or token operation | no event | |

Rules the tests rely on (all decided in Phase 1 and phases.md):
- One event per logical change; no-op writes (judged by values) and failed writes emit nothing.
- Label delete emits only `label.changed`. Member removal emits only `member.changed`: the removed user's tickets are unassigned silently (no `ticket.updated` per ticket, which could overflow the buffer), so the UI reloads tickets on `member.changed` (Section 9). Display-name changes emit no event in v1 (other members see the new name on their next reload; known limitation). Project delete emits no event and no final `close` frame.
- `ProjectID` is set on every event, and the event is for the project the entity belongs to.

---

## 3. Hub design

Files: `internal/service/hub.go` and `internal/service/hub_test.go`. The hub lives in the service package per the main-spec layout, has no HTTP knowledge, and depends only on the standard library (`log/slog` optional). It uses Phase 1's `Event`, `CloseReason` and `StreamControl` as they are.

```go
const DefaultStreamBuffer = 32

var ErrHubClosed = errors.New("event hub is shut down")

type HubOptions struct {
    StreamBuffer int          // per-subscription buffer; default DefaultStreamBuffer (32), set in code only
    Logger       *slog.Logger // optional; logs slow-client drops (project id, user id, never event content)
}

type Hub struct { /* unexported */ }

func NewHub(opts HubOptions) *Hub

// Subscribe registers a stream for (project, user). Returns ErrHubClosed after Shutdown.
func (h *Hub) Subscribe(projectID, userID string) (*Subscription, error)

// Publish fans an event out to subscriptions of e.ProjectID. Never blocks, never fails. Implements Publisher.
// It does not modify the event (Phase 1 already set Actor and At).
func (h *Hub) Publish(e Event)

// CloseUser and CloseProject implement StreamControl. CloseUser ends every stream of that user on that project.
func (h *Hub) CloseUser(projectID, userID string, reason CloseReason)
func (h *Hub) CloseProject(projectID string, reason CloseReason)

// Shutdown ends all streams with CloseShutdown and rejects new subscriptions. Idempotent.
func (h *Hub) Shutdown()

// SubscriberCount is the number of live subscriptions on a project (tests and diagnostics).
func (h *Hub) SubscriberCount(projectID string) int

type Subscription struct { /* unexported */ }

func (s *Subscription) C() <-chan Event        // buffered; NEVER closed (avoids send-on-closed panics)
func (s *Subscription) Done() <-chan struct{}  // closed exactly once when the subscription ends
func (s *Subscription) Reason() CloseReason    // valid after Done is closed
func (s *Subscription) Close()                 // idempotent; reason CloseClient; removes from hub
```

Compile-time assertions in `hub.go`: `var _ Publisher = (*Hub)(nil)` and `var _ StreamControl = (*Hub)(nil)`. There is exactly one hub per process; `cmd/pabrika` passes the same instance to `service.Deps` (as `Publisher` and `Streams`) and to the HTTP layer for `Subscribe` and `Shutdown`.

**Data structure:** `map[projectID]map[*Subscription]struct{}` guarded by one `sync.RWMutex`; a `closed bool` under the same lock. Each `Subscription` has `userID`, `ch chan Event` (capacity `StreamBuffer`), `done chan struct{}`, a `sync.Once`, and `reason`.

**Locking rules (the whole contract):**
- `Publish`: `RLock`; for each subscription of the project do a non-blocking send (`select { case ch <- e: default: slow = append(slow, sub) }`); `RUnlock`. If `slow` is non-empty: take the write `Lock`, delete each from the map (drop the project entry when it empties), `Unlock`, then `end(sub, CloseSlow)` for each and log it.
- `end(sub, reason)` is `once.Do(set reason; close(done))` and never touches the hub lock, so it can be called with or without it held. Removal from the map is always done by the caller under the write `Lock`.
- `Close()` (client side): write `Lock`, delete from map, `Unlock`, `end(sub, CloseClient)`.
- `CloseUser` / `CloseProject`: write `Lock`, collect matching subscriptions and delete them, `Unlock`, then `end` each with the reason.
- `Shutdown`: write `Lock`, set `closed`, collect everything, clear the map, `Unlock`, `end` each with `CloseShutdown`. `Subscribe` after that returns `ErrHubClosed`.
- Sending under `RLock` is safe because nothing ever closes `ch`; only `done` is closed. No blocking operation (channel send, I/O, logging with a blocking sink) happens under the write lock, and `Publish` never waits on a subscriber.

**Slow-client policy:** buffer of 32 events per stream. If a non-blocking send finds the buffer full, the subscription is ended with `CloseSlow`; the handler returns, the connection closes, and the browser's `EventSource` reconnects and reloads the whole board. Events are never dropped silently for a connected client (drop-and-disconnect, not drop-and-continue). Publishers are never delayed by a slow reader.

**Ordering:** events published by one goroutine for one project reach each subscription in that order, and the several events of one write are published contiguously and in order (Phase 1's helper publishes them in a row). Across concurrent writers the order is the order of post-commit `Publish` calls, which can differ from commit order by a few microseconds. That is acceptable: events carry no content and the client refetches current state. Do not add sequence numbers.

---

## 4. Completeness guard (test only)

Publishing and access-loss closes are wired in Phase 1. Phase 3 adds one guard test so a future write method cannot silently skip events: a test uses `reflect` over `ProjectService`, `MemberService`, `LabelService`, `TicketService`, `CommentService` and `UserService` (including methods Phase 2 adds: `Create`, `SetPassword`, `ValidateNew`, `Credentials`, `PasswordHash`, `TokenService`) and fails if a method is neither in the Section 2 catalog nor on an explicit read-only or no-event allowlist (`List`, `Get`, `Resolve`, `Latest`, `Projects.Create`, `UpdateProfile`, token and credential methods, ...). A new write method therefore fails CI until its event is decided. The guard is the only "hook" work in this phase; if a catalogued write fails the integration tests in Section 8, fix it in Phase 1's method, not with a second mechanism.

---

## 5. SSE endpoint: `GET /api/v1/projects/{id}/events`

File: `internal/httpapi/events.go`. Registered with `Server.Mount("GET /api/v1/projects/{id}/events", SessionOnly, h.ServeHTTP)` (`Write=false`; tokens never get past the `SessionOnly` guard). `{id}` accepts the project key or the ULID; the handler resolves it to the canonical ULID and always uses the ULID for `Subscribe`, so the hub stays keyed by ULID. `TRUST_PROXY` does not affect this endpoint. The Origin check applies to unsafe methods only, so a GET needs none.

```go
type sessionChecker interface { Validate(ctx context.Context, tokenHash string) error } // auth.Sessions, Phase 2

type eventsHandler struct {
    hub          *service.Hub
    projects     service.ProjectService
    sessions     sessionChecker // Phase 2 auth.Sessions; nil error = live, auth.ErrInvalidCredentials = gone, other = DB failure
    keepalive    time.Duration  // default 25s
    writeTimeout time.Duration  // per-frame write deadline, default 10s
    checkTimeout time.Duration  // per DB check on a tick, default 5s
    log          *slog.Logger
    beforeLoop   func()         // test hook only: runs after the 200 is flushed, before the select loop
}
```

### On open

1. Principal comes from the Phase 2 guard. No session: 401. Bearer token: 403 `session_required` (Phase 2's `SessionOnly` guard; tests assert through the guard). Take `actor := service.UserActor(principal.User.ID)` and `tokenHash := principal.Session.TokenHash`.
2. **Membership check 1:** `ref, err := projects.Resolve(ctx, actor, id)`. `ErrNotFound` (non-member, missing project, unresolvable key): `httpapi.WriteError` **404** `not_found`, no stream. Any other error: 500 `internal` via the standard mapper.
3. `sub, err := hub.Subscribe(ref.ID, principal.User.ID)`. `ErrHubClosed`: `httpapi.WriteError(w, 503, "unavailable", ...)`, no stream (this is the "new stream during shutdown" response). Then **membership check 2, after subscribing:** `projects.Resolve(ctx, actor, ref.ID)`. `ErrNotFound`: `sub.Close()` and 404. Subscribe-then-check closes the race where a removal lands between the first check and the subscribe. `defer sub.Close()` from here on.
4. Archived projects are still streamable (members can read archived projects).
5. `HEAD` (Go's `GET` pattern also matches it): after check 1, answer 200 with the headers below and no body, without subscribing.
6. Set headers, then `WriteHeader(200)`:
   - `Content-Type: text/event-stream`
   - `Cache-Control: no-store, no-transform` (see the no-store exemption in Section 1)
   - `X-Accel-Buffering: no` (nginx hint; harmless elsewhere)
   - Do not set `Connection` (HTTP/1.1 is handled by Go; forbidden on HTTP/2).
   - No `Content-Encoding`; the app never compresses.
7. Deadlines, via `rc := http.NewResponseController(w)`:
   - `rc.SetReadDeadline(time.Time{})` to clear the server's `ReadTimeout` (30 s), which otherwise can expire the background read Go runs on the connection and cancel the request context mid-stream.
   - Before **every** frame write (and flush) call `rc.SetWriteDeadline(time.Now().Add(writeTimeout))`. This overrides any server-level `WriteTimeout` a future change might add and prevents a stalled client (TCP open, not reading) from pinning the handler goroutine once the hub has already dropped it. A write or flush error ends the stream.
   - `ErrNotSupported` from the deadline calls is tolerated (logged once as a warning); a failing `Flush` at open is not: respond with an error before any frame is written if possible, otherwise return.
8. Write `retry: 3000\n\n` then `: connected\n\n`, flush. Invariant that makes "reload on open" sufficient: the subscription exists **before** the `open` event can fire, so a change committed after `Subscribe` is delivered as an event, and a change committed before it is visible to the board reload the client performs after `open`. A duplicate (event for a change the reload already saw) is harmless.

### Stream loop

```text
if beforeLoop != nil { beforeLoop() }
loop select:
  <-r.Context().Done():   return          // client went away
  <-sub.Done():           return          // slow, removed, deleted, shutdown
  ev := <-sub.C():        if sub.Done() already closed: return (no further frames after access loss)
                          write "event: <type>\ndata: <json>\n\n"; flush; on error return
  <-keepalive.C (25s):    write ": keepalive\n\n"; flush; then session check, then membership check (below)
```

- On return, log once at info level: project id, user id, close reason (`sub.Reason()` or `client_gone`, `session_invalid`, `access_lost`, `write_error`) and duration. Never log event content.
- SSE frame: `event:` is the event type (so the client uses `addEventListener("ticket.moved", ...)`; `onmessage` does not receive named events), `data:` is the single-line `json.Marshal` of `service.Event` (never contains a newline). No `id:` field, so there is no `Last-Event-ID` replay.
- Keepalive interval is a handler field (default 25 s) so tests can shorten it; use `time.NewTicker` with `defer Stop`.
- **Session re-validation on each tick (first):** `sessions.Validate(ctx, tokenHash)` with a `checkTimeout` context. `errors.Is(err, auth.ErrInvalidCredentials)`: return (the stream closes). This covers logout, expiry and password change: `POST /auth/me/password` deletes the user's OTHER sessions, so their streams close on the next tick while the session that made the change keeps streaming. The check is read-only (Phase 2 `Validate` does not slide), so an idle open tab cannot keep a session alive past its expiry; it closes at the next tick after expiry and the reconnect gets 401. Any other error (DB failure, timeout): log, keep the stream open.
- **Membership re-check on each tick (second):** `projects.Resolve(ctx, actor, projectULID)` with a `checkTimeout` context. `ErrNotFound`: return. Any other error: log, keep the stream open. Cheap safety net for removal paths that did not go through the hub.
- A tick check runs in the handler goroutine and can briefly delay delivery; events wait in the 32-slot buffer, and a stuck DB is bounded by `checkTimeout`. The tick is the worst-case delay for these closes: at most one keepalive interval (25 s) after the session or membership change.
- When the handler returns because of `CloseSlow`, `CloseRemoved`, `CloseProject`, `CloseShutdown` or a failed tick check, just return (connection closes cleanly). No final `close` event is written (confirmed decision).

### Reconnect semantics
- The server keeps no per-client state and replays nothing. On every connect or reconnect, **including the first**, the client must reload the board after `open` (or start its initial board load only after `open` fired; Section 9).
- `retry: 3000` tells `EventSource` to wait 3 s before reconnecting after a clean close or network error.
- Browser behavior to rely on: any non-200 response (401, 403, 404, 5xx, including a reverse proxy's 502 or 503 during a restart) makes `EventSource` fail permanently (`readyState` CLOSED, no automatic retry). Only a network error or a clean close of a 200 stream reconnects automatically. So CLOSED does not by itself mean access was lost; the client must probe (Section 9).
- A 404 or 401 on reconnect is the intended outcome for a removed member or deleted project (404, "lost access") and for a logged-out, expired or password-revoked session (401, back to login). Slow-client closes are clean closes of a 200 stream, so the client reconnects normally. During and just after a server shutdown, a reconnect gets 503 `unavailable` (or the proxy's 502/503) and the browser gives up; the client's probe and capped backoff recreate the stream.

### Proxy and flush notes (for code comments and Phase 6 docs)
- Flush after every write via `rc.Flush()`. If it returns an error, treat the stream as dead.
- Middleware on this route must not buffer: no gzip, no `http.TimeoutHandler`, no body-capture logging wrapper. Any wrapper must implement `Unwrap()`.
- Reverse proxies: Caddy streams by default; nginx needs `proxy_buffering off` for `/api/v1/projects/*/events` (the `X-Accel-Buffering: no` header also helps). Proxy read timeouts must exceed 25 s (nginx default 60 s is fine). Any proxy compression must exclude `text/event-stream`; the app itself does no compression.
- Browsers allow only about 6 HTTP/1.1 connections per origin across all tabs, and each open board holds one stream. Behind a proxy that speaks HTTP/2 (Caddy by default; nginx with `http2`) this limit does not apply. Direct plain-HTTP access on `localhost:8080` is HTTP/1.1, so more than about 5 open boards in one browser can starve normal requests. Known limitation, documented in Phase 6.

---

## 6. Graceful shutdown

`http.Server.Shutdown` waits for active connections and never cancels request contexts, so open SSE streams would block it until its deadline. **Phase 2 owns `srv.Shutdown`** in `serve` (SIGINT/SIGTERM, then stopping the purge job and closing the DB). **Phase 3 delivers the hub's part and its test.** Required order, which Phase 3 wires in `cmd/pabrika` and the shutdown test pins:

1. On SIGINT/SIGTERM, call `hub.Shutdown()` first: it ends every stream with `CloseShutdown` (each handler returns, its connection closes cleanly), and new `Subscribe` calls return `ErrHubClosed`, which the handler turns into 503 `unavailable` via Phase 2's `WriteError`.
2. Then Phase 2's `srv.Shutdown(ctx)` with an **8 s** deadline; if it returns an error (deadline exceeded), fall back to `srv.Close()`.
3. Then stop the purge job and close the DB (`Store.Close` checkpoints the WAL), in Phase 2's existing order.

Also `srv.RegisterOnShutdown(hub.Shutdown)` as belt and braces (idempotent). Phase 6 verifies end to end that `docker stop` exits 0 in under 10 s with a stream open.

---

## 7. Tasks

1. **Hub** (`internal/service/hub.go`): `Hub`, `HubOptions`, `Subscription`, `Subscribe`, `Publish`, `CloseUser`, `CloseProject`, `Shutdown`, `SubscriberCount`, `ErrHubClosed`, per Section 3, using Phase 1's `Event`, `CloseReason`, `Publisher` and `StreamControl` unchanged. Compile-time interface assertions.
2. **Test wiring:** an option on `testutil.NewTestServices` to attach a real hub as both `Publisher` and `Streams` (default stays the recording publisher). No change to Phase 1 types.
3. **Completeness guard** test (Section 4) and the event-contract tests (key set, no `Actor` leakage).
4. **SSE handler** (`internal/httpapi/events.go`) mounted through `Server.Mount` as `SessionOnly`, per Section 5: key-or-ULID resolution, `Projects.Resolve` before and after `Subscribe`, named events, per-frame write deadline, cleared read deadline, HEAD handling, 503 on `ErrHubClosed`, `Sessions.Validate` then membership re-check on each tick.
5. **Middleware audit (verify only):** confirm every wrapper on the `/api/v1` chain flushes and exposes `Unwrap()` (a test sets `SetWriteDeadline` and `Flush` through the full chain), there is no `WriteTimeout`, and the SSE `Cache-Control: no-store, no-transform` survives Phase 2's blanket `no-store`. Fix a wrapper or the ordering only if a test fails, and flag it; change nothing else in Phase 2 files.
6. **Shutdown wiring** in `cmd/pabrika` per Section 6 (hub first, then Phase 2's `srv.Shutdown` at 8 s with `srv.Close()` fallback) plus its test.
7. **Tests** (Section 8).
8. **Code comments** where Phase 6 needs them (proxy buffering, HTTP/2 connection limit). No README work here.

Tasks 1 to 3 need no HTTP; 4 to 6 depend on 1.

---

## 8. Tests

**Gate:** `CGO_ENABLED=0 go test ./...` passes (the exit criterion, matching Phase 1). The race detector needs cgo, so `-race` tests are a **separate optional run** with `CGO_ENABLED=1` (`CGO_ENABLED=1 go test -race ./internal/service ./internal/httpapi`) when a C toolchain exists; they are not part of the gate. Write the stress tests so they are meaningful without `-race` too (assert no deadlock, no panic, no lost wake-ups).

Hub unit tests (`internal/service`):
- `Publish` reaches only subscriptions of that project; two projects stay isolated.
- Event order is preserved per subscription; the events of one write arrive contiguously and in order.
- **Slow client dropped:** subscribe, publish `StreamBuffer + 1` events without reading; `Done()` closes with `CloseSlow`; a second, reading subscriber on the same project still receives every event; `Publish` never blocks (assert with a timeout); the slow subscription is removed (`SubscriberCount` drops).
- `CloseUser` ends only that user's streams on that project (other users and other projects untouched); `CloseProject` ends all of that project; `Shutdown` ends all with `CloseShutdown` and `Subscribe` then returns `ErrHubClosed`.
- `Close()`, `Shutdown()` and repeated `CloseUser` are idempotent; after any end, `C()` is never closed and no panic occurs.
- Stress: many goroutines doing `Publish`, `Subscribe`, `Close`, `CloseUser` and `Shutdown` concurrently; no deadlock, no panic (and, in the optional race run, no race).
- `*Hub` satisfies `Publisher` and `StreamControl` (compile-time).
- Event JSON: allowed key set only; `EventActor` carries only `type` and `id`.

Service integration tests (in-memory SQLite, real hub):
- **Event after service write:** subscribe, then for each row of the Section 2 catalog run the service write and assert the expected event type, project id, ticket/comment/label/user id, `Renumbered` (force a renumber with the Phase 1 "60 inserts between two tickets" setup) and actor.
- **Not published on rollback:** validation failure, permission failure and `project_archived` writes emit nothing.
- **Published after commit:** when the subscriber receives the event, the new state is already readable through the service (read the ticket on receipt).
- No-op writes emit nothing: `Tickets.Update` with no change, `Tickets.Move` to the same spot, `Members.SetRole` same role, `Labels.Update` same values, `Comments.Edit` identical body, `Projects.Update` unchanged. A multi-field `Tickets.Update` emits exactly one `ticket.updated`.
- Label delete emits only `label.changed` (no ticket events). Member removal with assigned tickets emits only `member.changed` (no `ticket.updated`). `Users.UpdateProfile` emits nothing.
- Member removal publishes `member.changed` and closes that user's subscription with `CloseRemoved`, leaving other members' subscriptions open; leave behaves the same. Project delete closes all with `CloseProject` and publishes no event. Role change and archive do not close streams.
- Actor is `{api_token, token id}` for a token-driven write (anticipates Phase 4) and `{user, user id}` for a session write; the token's owner user id never appears.
- Completeness guard (Section 4).

HTTP tests (`httptest.NewServer`, real client reading the body stream, real Phase 2 guard chain):
- **Event arrives:** member opens stream (200, `Content-Type: text/event-stream`, `Cache-Control` is exactly `no-store, no-transform` after the full middleware chain, `X-Accel-Buffering: no`, no `Content-Encoding`, `retry:` and `: connected` lines), a REST write occurs, the matching `event:` and `data:` frame arrives; data contains ids and actor `{type,id}` only (assert no title or body text appears).
- **Non-member gets nothing:** a user not in the project gets 404 and no stream; a member of another project with an open stream receives nothing when the first project changes.
- **Unauthenticated** 401; **bearer token** 403 `session_required` (through the guard).
- **Removed member's stream closes:** member B has an open stream, owner removes B via REST (and separately via the service, and by B leaving); the stream reaches EOF within a short timeout with no frame after the close; reconnecting as B returns 404.
- **Keepalive:** handler built with a short interval (e.g. 50 ms); `: keepalive` lines arrive with no writes happening.
- **Membership re-check:** remove membership by direct DB update (no hub call); the stream closes at the next tick.
- **Session invalidated:** open a stream, delete the session row (logout via REST, and separately an expired session using the injected clock); the stream reaches EOF within one keepalive tick; reconnect returns 401. The tick does not slide the session (expiry unchanged after several ticks).
- **Password change:** user has two sessions with open streams; `POST /auth/me/password` from session A; B's stream closes within one tick, A's stays open.
- **Check errors keep the stream open:** with a fake session checker returning a non-`ErrInvalidCredentials` error, and a fake project service returning a non-`ErrNotFound` error on the tick, the stream stays open past several ticks.
- **Key or ULID path:** `/projects/WEB/events` and `/projects/{ulid}/events` both receive the same events (hub keyed by ULID); lowercase key works; unknown key 404.
- **Slow client dropped:** use the `beforeLoop` hook to hold the handler, publish more than the (small) buffer's worth of events, release; the stream ends (EOF) without delivering stale frames beyond the buffer, and the handler goroutine exits (a handler-done signal or `SubscriberCount == 0`).
- **Per-frame write deadline:** with a fake `ResponseWriter` that records `SetWriteDeadline` calls, every frame and keepalive is preceded by a deadline about `writeTimeout` ahead; a fake writer whose `Write` returns an error ends the handler.
- **Server timeouts do not kill the stream:** `httptest.NewUnstartedServer` with `ReadTimeout` and `WriteTimeout` set to about 200 ms; the stream is still alive and receives an event after 600 ms.
- **Role change keeps the stream open** (owner to viewer still receives events). Archived project can be streamed. `HEAD` returns 200 headers, no body, and does not subscribe (`SubscriberCount` unchanged).
- **Shutdown:** `hub.Shutdown()` ends open streams with a clean EOF, then `srv.Shutdown` (8 s deadline, test uses a shorter one) returns promptly and new connects get 503 with the standard error shape and code `unavailable`. A test of the `cmd/pabrika` ordering (hub first, then `srv.Shutdown`, `srv.Close()` fallback when the deadline passes) uses a handler that ignores shutdown.
- **Disconnect cleanup:** cancelling the client request removes the subscription (`SubscriberCount` back to 0) and the handler goroutine exits (no goroutine growth after N connect/disconnect cycles).
- **Middleware chain:** through the full chain, `Flush` and `SetWriteDeadline` reach the real connection (frames arrive without waiting for the response to end).
- **Route table:** the events route is present in `Server.Routes()` as `SessionOnly`.

Exit criteria (from phases.md): the right event arrives after a service write, a non-member's stream gets nothing, a removed member's stream closes. `CGO_ENABLED=0 go test ./...` passes; the optional `CGO_ENABLED=1 -race` run is clean where available.

---

## 9. What later phases rely on

**Phase 4 (MCP)**
- No MCP-specific work. MCP tools must call the same service write methods and pass the token as `Actor{Type: ActorAPIToken, ID: tokenID, UserID: owner, ...}` (via `Principal.Actor()`); events then carry `actor: {type: api_token, id: tokenID}` automatically.
- Tool code must never call `Publish` or touch the hub.
- The no-op rules apply identically (for example `update_comment` with an identical body and `update_ticket` with identical values publish nothing).

**Phase 5 (Web UI), contract note**
- Endpoint: `GET /api/v1/projects/{key-or-ulid}/events` with the session cookie, via `EventSource` (same origin; the Vite proxy keeps it same-origin; no `withCredentials` needed). The project path accepts the key or the ULID.
- Frames are named events: register a listener per type in the catalog; `onmessage` receives nothing. `data` is the JSON `Event`: keys `type, project_id, ticket_id?, comment_id?, label_id?, user_id?, renumbered?, actor{type,id}, at`. The actor has **no name**; to display who did something, refetch the ticket or use the member list.
- Ticket events: fetch `GET /tickets/{ticket_id}` (ULID accepted) and update or move the card; for `ticket.deleted`, remove the card locally (a fetch would 404). If `renumbered` is true on `ticket.moved`, reload the board.
- `label.changed`: reload labels and tickets. `member.changed`: reload members **and tickets** (a removal unassigns tickets without ticket events), and re-read the project for the caller's role. `project.updated`: reload the project. `comment.*`: refetch comments for the open ticket if `ticket_id` matches, and refresh that ticket for the comment count.
- "Changed by someone else" flash: `actor.type == "user" && actor.id == me.id` is mine; everything else, including every `api_token`, counts as someone else (the id is a token id, not a user id).
- **Open and reconnect:** reload the board on **EVERY** `open`, including the first (or begin the initial load only after the first `open` fires). Loading first and opening the stream afterwards loses any change committed between the load snapshot and the subscription.
- **Error handling:** `readyState CONNECTING` on error means "Reconnecting" (the browser retries by itself). `readyState CLOSED` means the browser gave up: it received a 401, a 404, or any other non-200 (for example a proxy 502 or 503, or 503 `unavailable` from a server shutting down). The client then probes `GET /projects/{key}`: 401 goes to login, 404 is lost access, anything else (5xx, 429, network failure) means recreate the stream with a capped backoff. There must still be a single `EventSource` per board.
- No replay, no `Last-Event-ID`: the reload on open is what guarantees nothing is missed.

**Phase 6 (Ship)**
- Document the nginx `proxy_buffering off` requirement, proxy read timeout above 25 s, that compression is the proxy's job and must exclude `text/event-stream` (the app does no compression), and the HTTP/1.1 six-connection limit (prefer HTTP/2 at the proxy). `X-Accel-Buffering: no` is already set and the SSE response is `Cache-Control: no-store, no-transform`.
- Verify `docker stop` with an open stream exits 0 in under 10 s; the hub shutdown wiring exists from this phase and Phase 2's `srv.Shutdown` uses an 8 s deadline with `srv.Close()` fallback.
- Cross-check Phase 6's header audit: the events response is `no-store, no-transform`, exempt from any blanket `no-store` rewrite.

---

## 10. Decisions applied

Confirmed in [phases.md](phases.md) "Decisions (resolved open questions)" and "Provisional decisions":

1. Named SSE events (`event: <type>`); Phase 5 registers a listener per type.
2. `renumbered`, `user_id`, `label_id` and `comment_id` stay on events (ids and booleans only); the wire actor is `{type, id}` only.
3. Label delete emits only `label.changed`; clients reload labels and tickets.
4. Project delete emits no event and no final `close`; streams end and reconnect gets 404.
5. Each 25 s keepalive tick re-validates the session (Phase 2 `Sessions.Validate` with `Principal.Session.TokenHash`, read-only, no slide), then membership; logout, expiry and password change (other sessions deleted) close the stream within one tick.
6. Project paths accept key or ULID; the hub is keyed by ULID. `TRUST_PROXY` is irrelevant here.
7. No per-user stream cap; buffer 32, set via `HubOptions` in code only.
8. Phase 1 owns `Event`, `EventType`, `EventActor`, `CloseReason`, `Tx.Emit` / `Tx.AfterCommit`, `Publisher`, `StreamControl` and `Deps`; Phase 3 delivers only `Hub`, the SSE handler, hub options, shutdown wiring and tests.
9. Membership check is `Projects.Resolve(UserActor(id), ref)` before and after `Subscribe`; no `ProjectRole`, no `Service`, no exported helper.
10. Gate is `CGO_ENABLED=0 go test ./...`; `-race` is a separate optional `CGO_ENABLED=1` run.
11. Member removal emits only `member.changed` (tickets unassigned silently; the UI reloads tickets on it); display-name changes emit no event.
12. A new stream during shutdown gets 503 `unavailable` through Phase 2's `WriteError` (503 is an allowed extra status). Shutdown order: hub shutdown, then Phase 2's `srv.Shutdown` with an 8 s deadline and `srv.Close()` fallback; Phase 3 delivers the hub shutdown and its test, Phase 2 owns `srv.Shutdown`.
13. No app-side compression; the SSE `Cache-Control: no-store, no-transform` is exempt from Phase 2's blanket `no-store`.
14. Phase 5 contract: CLOSED means the browser gave up (401, 404, other non-200); probe `GET /projects/{key}` to choose login, lost access or backoff retry; reload the board on every open; `member.changed` also reloads tickets.

**Unresolved:** none. Cross-file conflicts were reconciled in the final consistency pass.
