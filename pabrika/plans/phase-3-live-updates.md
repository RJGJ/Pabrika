# Phase 3 plan: Live updates

Nothing is implemented yet: the repo holds only specs and an empty `plans/` directory. This plan is written against the specs, and every path assumes the layout in phase-1 and phase-2. The spec sections cited are in `phase-3-live-updates.md` unless stated otherwise. Phase 3 delivers a `Hub`, an SSE handler, shutdown wiring, a completeness-guard test and tests. Phase 1 already owns the event types, `Tx.Emit`/`AfterCommit` and the emit hooks (Phase 1 §6.5), and Phase 3 must not redefine them.

## 0. Preconditions (check before starting)

Phases 1 and 2 must be `done` in `phases.md`. Confirm these exist, and use the real names if they differ from the specs:

- **Phase 1 (`internal/service`):** `Event`, `EventActor`, `CloseReason`, `Publisher` and `StreamControl`. Also `Deps{Publisher, Streams}`, `Projects.Resolve`, `UserActor`, and `testutil.NewTestServices` with its recording publisher.
- **Phase 2 (`internal/httpapi`, `internal/auth`):** `Server.Mount`, `Access` and `Server.Routes()`. Also `Principal.Session.TokenHash`, `auth.Sessions.Validate`, `auth.ErrInvalidCredentials` and `httpapi.WriteError`.
- **Server settings:** the server has no `WriteTimeout`, and `serve` already calls `srv.Shutdown` with an 8 s deadline and an `srv.Close()` fallback.
- **Baseline command:** `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...` is green before any Phase 3 change.

## 1. Work packages

### WP1. Hub (no HTTP, test-first). Spec §3, tasks 1 and 7

Files: `internal/service/hub.go` and `internal/service/hub_test.go`.

1. **Hub tests first, in the order below, so each fails before the code exists.**
   - **Isolation and order:** publish reaches only that project's subscribers, and order is preserved.
   - **Slow client:** after `StreamBuffer+1` unread events, `Done()` closes with `CloseSlow`, the reading subscriber gets every event, `Publish` returns within a timeout, and `SubscriberCount` drops.
   - **Targeted closes:** `CloseUser`, `CloseProject` and `Shutdown` close the right streams, and `Subscribe` after `Shutdown` returns `ErrHubClosed`.
   - **Idempotence:** `Close`, `Shutdown` and repeated `CloseUser` can be called twice without a panic, and `C()` is never closed.
   - **Event JSON:** the key set is exactly `type, project_id, ticket_id?, comment_id?, label_id?, user_id?, renumbered?, actor{type,id}, at`.
2. **Types.** Define `DefaultStreamBuffer = 32`, `ErrHubClosed`, `HubOptions{StreamBuffer, Logger}` and `NewHub`. Add the compile-time assertions `var _ Publisher = (*Hub)(nil)` and `var _ StreamControl = (*Hub)(nil)`.
3. **Hub state.** Use `map[projectID]map[*Subscription]struct{}` under one `sync.RWMutex`, plus a `closed bool`. `Subscription` holds `userID`, a buffered `ch`, `done`, a `sync.Once` and `reason`.
4. **Subscribe and Close.** `Subscribe` returns `ErrHubClosed` after shutdown. `Close()` takes the write lock, deletes the entry, unlocks, then calls `end(CloseClient)`.
5. **Publish.** Take `RLock` and do a non-blocking send to each subscriber, collecting the ones that are full. Release `RLock`, take the write `Lock` to delete the slow ones (and the project entry if it empties), unlock, then call `end(CloseSlow)` and log.
6. **Targeted closes.** `CloseUser`, `CloseProject` and `Shutdown` each collect and delete under the write lock, then call `end` after unlocking.
7. **Rules that must hold.**
   - `end` is only `once.Do(set reason; close(done))` and never touches the hub lock.
   - `ch` is never closed.
   - No logging or I/O happens under the write lock.
8. **Add `SubscriberCount`.**
9. **Stress test.** Run many goroutines doing `Publish`, `Subscribe`, `Close`, `CloseUser` and `Shutdown` at once, with a watchdog timeout. It must be meaningful without `-race`.

Verify:
- `CGO_ENABLED=0 go test ./internal/service -run 'Hub|EventJSON' -count=1`
- Optional: `CGO_ENABLED=1 go test -race ./internal/service -run Hub -count=5`

### WP2. Test wiring. Spec task 2 (depends on WP1; parallel with WP4 start)

Add an option to `testutil.NewTestServices`, for example `WithHub(opts HubOptions)`. It builds one `*service.Hub`, passes it as both `Deps.Publisher` and `Deps.Streams`, and exposes `env.Hub`. The default stays the recording publisher, so Phase 1 tests must not change. Verify with `CGO_ENABLED=0 go test ./internal/service -count=1`, where Phase 1 tests stay green.

### WP3. Service integration tests against a real hub, plus the completeness guard. Spec §2, §4, §8 (depends on WP1 and WP2)

Put these in `internal/service` as `service_test`. They assert the Phase 1 hooks, and any failure is fixed in the Phase 1 method, not with a second mechanism.

1. **Catalog rows.**
   - Table-driven over the §2 catalog: event type, project id, entity ids, `Renumbered` and actor.
   - Force a renumber with the Phase 1 "60 inserts between two tickets" setup.
2. **No event.**
   - Rollback cases (validation, permission, `project_archived`).
   - No-op writes (the six in §8).
   - `Users.UpdateProfile`.
   - A multi-field `Tickets.Update`, which emits exactly one event.
3. **Single-event rules.**
   - Label delete emits only `label.changed`.
   - Member removal emits only `member.changed`.
4. **Post-commit read.** On receipt of the event, read the new state through the service.
5. **Access-loss closes.**
   - Removal and leave: `member.changed` is published, and only that user's stream ends with `CloseRemoved`.
   - Project delete: all streams end with `CloseProject` and no event is published.
   - Role change and archive keep the streams open.
6. **Actor shape.** A token write gives `{api_token, tokenID}`, a session write gives `{user, userID}`, and the owner id never appears.
7. **Completeness guard.**
   - Use `reflect` over the six service interfaces, including Phase 2's added methods.
   - Fail on any method that is neither in the catalog nor on an explicit read-only or no-event allowlist.
   - Check that the allowlist contains no stale names.

Verify: `CGO_ENABLED=0 go test ./internal/service -run 'Events|Guard|Hub' -count=1`.

### WP4. SSE handler. Spec §5, task 4 (depends on WP1; can start in parallel with WP2/WP3 using a bare hub)

File: `internal/httpapi/events.go`, plus `events_test.go`.

Write the tests first, using the Phase 2 harness (real chain over `httptest.NewServer`, cookie jar, in-memory store). Then build the handler in these steps, each tied to the test that proves it.

1. **Skeleton and mount.**
   - Define `eventsHandler` with the §5 fields. Add the small `sessionChecker` interface for fakes.
   - Register `Mount("GET /api/v1/projects/{id}/events", SessionOnly, h.ServeHTTP)` with `Write=false`.
   - Proves: the route table test (present, `SessionOnly`), the 401 test, and the 403 `session_required` test for a bearer token.
2. **Open path.**
   - `Resolve` with `UserActor`, mapping `ErrNotFound` to 404 and other errors to 500.
   - Subscribe by ULID, mapping `ErrHubClosed` to 503 `unavailable` via `WriteError`.
   - Then `Resolve` again, closing the subscription and returning 404 if it fails. Then `defer sub.Close()`.
   - Proves: non-member 404, key and lowercase key and ULID paths, unknown key 404, and archived-project streaming.
3. **HEAD.** Return 200 with headers after check 1, with no subscribe. Proves: `SubscriberCount` is unchanged.
4. **Headers and deadlines.**
   - Set `Content-Type`, `Cache-Control: no-store, no-transform` and `X-Accel-Buffering: no`, with no `Connection` header.
   - Call `rc.SetReadDeadline(time.Time{})`, and `SetWriteDeadline(now+writeTimeout)` before every write and flush.
   - Tolerate `ErrNotSupported`.
   - Write `retry: 3000` and `: connected`, then flush.
   - Proves: the headers test and the fake `ResponseWriter` deadline test (a write error ends the handler).
5. **Loop.**
   - `select` over request context, `sub.Done()`, `sub.C()` and the ticker.
   - Write `event: <type>\ndata: <json>\n\n`, and return without writing if `Done` is already closed.
   - Call `beforeLoop` after the open flush.
   - Log once on exit with the reason and duration.
   - Proves: event-arrives, non-member-gets-nothing, the exact key set and no content, slow-client drop via `beforeLoop`, and disconnect cleanup (goroutine count after N cycles).
6. **Tick checks.**
   - Run `sessions.Validate` first and return on `ErrInvalidCredentials`. Other errors are logged and the stream stays open.
   - Then `Resolve` again, returning on `ErrNotFound` and logging other errors.
   - Both use `checkTimeout`, run after the keepalive write, and never slide the session.
   - Proves: keepalive at a 50 ms interval, membership re-check by direct DB update, logout and expired session (injected clock), password change (B closes, A stays), and fake checkers returning errors.

Verify:
- `CGO_ENABLED=0 go test ./internal/httpapi -run 'Events|SSE' -count=1`
- Optional: `CGO_ENABLED=1 go test -race ./internal/httpapi -run 'Events|SSE' -count=3`

### WP5. Middleware audit. Spec §1, task 5 (depends on WP4 step 1; do it early because it is the biggest risk)

Write these tests before the handler is finished. Fix a Phase 2 wrapper only if its test fails, and flag it when you do.

1. **Flush through the chain.** Frames arrive before the response ends, and `SetWriteDeadline` plus `Flush` reach the real connection through recover, log, headers and body-cap.
2. **Final `Cache-Control`.** On the real chain the SSE response is exactly `no-store, no-transform`. If Phase 2's blanket `no-store` runs after the handler's header and wins, make it set the header only when absent or skip the events route (the smallest change that passes).
3. **Server timeouts.** Use a server with `ReadTimeout` and `WriteTimeout` of about 200 ms and check an event still arrives at 600 ms. This proves the read-deadline clear and the write-deadline reset.
4. **No `http.TimeoutHandler` or compression.** Confirm with `grep -rniE "gzip|compress|TimeoutHandler" internal cmd`.

### WP6. Shutdown wiring. Spec §6, task 6 (depends on WP1 and WP4)

1. **Order in `cmd/pabrika` `serve`.** On SIGINT/SIGTERM: `hub.Shutdown()`, then `srv.Shutdown(ctx 8s)`, falling back to `srv.Close()`, then stop the purge job and close the store. Also call `srv.RegisterOnShutdown(hub.Shutdown)`.
2. **Construction.** Build one `Hub` in `serve` and pass it as `Deps.Publisher` and `Deps.Streams`, and into the events handler. The Phase 1 placeholder `serve` already becomes real in Phase 2, so only add the hub there.
3. **Extraction for testability.** Put the sequence in a small function such as `runServer(ctx, srv, hub, closeStore, deadline)`. Phase 2's test already uses a fake hook, so replace the fake with the real hub.
4. **Tests.**
   - Open streams get a clean EOF, `srv.Shutdown` returns promptly, and a new connect gets 503 with code `unavailable`.
   - With a handler that ignores shutdown, the `srv.Close()` fallback fires at a short deadline, the function returns nil, and the order is hub, then `Shutdown`, then store.

Verify: `CGO_ENABLED=0 go test ./cmd/pabrika ./internal/httpapi -run 'Shutdown' -count=1`.

### WP7. Comments and close-out. Spec task 8

- Add code comments on the handler covering proxy buffering, HTTP/2 vs the 6-connection limit, `Unwrap()` and no compression. No README work.
- Run the full gate and `go vet ./...`.
- Set Phase 3 to `done` in `phases.md`.

## 2. Dependencies and parallelism

```text
WP1 ──┬─> WP2 ─> WP3
      ├─> WP4 ─┬─> WP6
      │        └─> WP7
      └─> WP5 (tests can be written as soon as the Phase 2 chain exists; run against WP4)
```

- WP1 first. After it, WP2/WP3 (service side) and WP4/WP5 (HTTP side) are independent and suit two agents.
- WP4 needs only a bare `Hub` (`NewHub`), not the service integration.
- WP6 needs WP1 and WP4, and WP7 comes last.
- The WP5 tests can be written before the handler exists (against a stub that flushes), which de-risks Phase 2 interactions early.

## 3. Definition of done (mapped to the phase exit criteria)

| Exit criterion (phases.md, §8) | Proving tests |
|---|---|
| The right event arrives after a service write | WP3 catalog and post-commit-read tests, and WP4 "event arrives" over HTTP |
| A non-member's stream gets nothing | WP4 non-member 404, and the isolated-stream test (another project's member receives nothing) |
| A removed member's stream closes | WP3 removal and leave tests, and WP4 removed-member EOF with reconnect 404 |
| Gate | `CGO_ENABLED=0 go test ./...` and `go vet ./...` pass |
| Optional | `CGO_ENABLED=1 go test -race ./internal/service ./internal/httpapi` is clean where a C toolchain exists |

Also done when:
- The completeness guard is in place.
- The SSE header set and the route-table `SessionOnly` entry are asserted.
- The shutdown test passes.
- No Phase 1 type was redefined.
- Any Phase 2 wrapper fix is noted.

## 4. Risks and mitigations

| Risk | Mitigation |
|---|---|
| **Lock misuse and deadlock** | Never send under the write lock. Send under `RLock` only because `ch` is never closed. `end` never takes the hub lock. The stress test has a watchdog, and the optional `-race` run is repeated with `-count`. |
| **Slow clients** | Non-blocking send, then drop with `CloseSlow`. The per-frame write deadline frees the handler goroutine. The slow-client test at both hub and HTTP level uses `beforeLoop`. |
| **Goroutine leaks** | `defer sub.Close()` right after subscribe, `defer ticker.Stop()`, and the handler exits on request context, `Done`, a write error or a failed tick check. The leak test runs N connect/disconnect cycles and compares goroutine counts. |
| **SSE vs server timeouts** | Clear the read deadline and set a per-frame write deadline. The 200 ms timeout test proves it. Never add `WriteTimeout`. |
| **Middleware hiding `Flusher` or blanket `no-store`** | WP5 tests run on the real chain. Use `http.ResponseController` only. Fix wrappers (`Unwrap`, `Flush`) and header ordering only if a test fails. |
| **Race: removal between check and subscribe** | Subscribe, then check again. A test with an injected `beforeLoop`/hook can force it. |
| **Tick check blocks delivery** | `checkTimeout` of 5 s bounds it, and the buffer of 32 absorbs the delay. |
| **Phase 1 hooks missing or wrong** | WP3 finds them. Fix in Phase 1 methods and report. |
| **Hub accidentally closes `ch`** | A test checks `C()` stays open after every end, and a code comment says why. |
| **Flaky timing tests** | Short tickers (50 ms) and generous read timeouts (about 2 s). Use channels and `SubscriberCount` polling helpers rather than fixed sleeps. |

## 5. Size estimate

About 2 to 3 focused days, roughly 600 lines of production code and 1,500 to 2,000 lines of tests.

| Package | Estimate |
|---|---|
| WP1 | 0.5 day |
| WP2 and WP3 | 0.5 day |
| WP4 | 1 day |
| WP5 | 0.25 day |
| WP6 | 0.25 day |
| WP7 | 0.25 day |

The largest uncertainty is the amount of Phase 1 or 2 rework if hooks or wrappers do not match the specs.

## 6. Open questions

None blocking. Spec §10 says unresolved is none, and the specs agree with each other on the points checked. Two minor items to confirm in code, not decisions for the user:

1. **Where the blanket `no-store` runs.** If it runs after the handler, it needs a small Phase 2 change. WP5 decides this, and the spec already says to flag it.
2. **Exact names.** Names such as `UserActor`, `Projects.Resolve` and `testutil.NewTestServices` options come from the specs, so align them with the code once Phases 1 and 2 exist.

### Critical files for implementation
- `internal/service/hub.go` (new; hub, subscription, locking contract)
- `internal/httpapi/events.go` (new; SSE handler, tick checks, deadlines)
- `internal/testutil/` (add the real-hub option to `NewTestServices`, plus the completeness guard test)
- `cmd/pabrika/main.go` (hub construction, shutdown order)
- `internal/httpapi/server.go` (middleware chain, only if the WP5 audit fails: Flush, Unwrap, `Cache-Control` ordering)
