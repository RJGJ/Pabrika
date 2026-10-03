# Phase 2 plan: Auth, sharing and REST

Module root is `pabrika/`. Specs are in `pabrika/specs`. "P2 §n" means `phase-2-auth-sharing-rest.md`. "P1 §n" is `phase-1-foundation.md`. "MS" is `main-spec.md`. `phases.md` Decisions override everything else.

**State when planned:** the repo contained only specs, `CLAUDE.md`, `README.md` and `plans/.gitkeep`. No Go code existed, so Phase 1 is a hard prerequisite (WP0).

**Conventions for every step**
- Gate command: `CGO_ENABLED=0 go test ./...`. Also run `CGO_ENABLED=0 go vet ./...` and `CGO_ENABLED=0 go build ./...`.
- Per-package loop: `CGO_ENABLED=0 go test ./internal/auth/... -count=1`.
- `-race` needs cgo, so it is an optional extra run only (P1 §1): `CGO_ENABLED=1 go test -race ./...`.
- Order inside each step: write the failing test, then the code, then run the test.
- Generated sqlc code is committed, and tests need no CLI tools (P1 §1).

---

## WP0. Pre-flight: Phase 1 must be done, plus additive Phase 1 changes (size S, about 250 LOC plus tests)

**Gate:** `CGO_ENABLED=0 go test ./...` is green on Phase 1, and the Phase 1 exit criteria are met. Do not start otherwise.

Make the additive Phase 1 changes from P2 §0 and add a Phase 1 test for each:

| # | Change | Where |
|---|---|---|
| a | A malformed cursor returns `KindBadRequest` with code `invalid_cursor` | `internal/service/cursor.go` |
| b | Display name cap is 1..100, `MaxDisplayName = 100` | `validate.go` |
| c | `CommentService.List(ctx, actor, ticketRef, limit, cursor)` returns `CommentPage` ordered `(created_at, id)` | `comments.go` |
| d | Ticket list items carry `CommentCount` from one grouped query | `tickets.go` |
| e | `ProjectSummary.Role` | `projects.go` |
| f | `Projects.List` returns name-then-key order (see Open question 1) | `projects.go` |
| g | Activity `changes` keys are exactly the 8 documented ones, plus a drift test | `activity.go` |

If any of these already exist, record "no change" and move on.

---

## WP1. Store queries for users, sessions and tokens (size S, about 150 SQL lines plus about 80 test lines)

**Depends on:** WP0. **Can run in parallel with:** WP3.

**Files:** new `internal/store/queries/sessions.sql` and `tokens.sql`, plus additions to `users.sql`; regenerate into `internal/store/db`.

- Sessions: create, get by hash, extend (`expires_at`), delete by hash, delete all for user, delete expired before `now`. The existing `DeleteSessionsForUserExcept` takes a session id, but P2 §0 passes a token hash (`keepSessionHash`). The PK `token_hash` is the id, so reuse it.
- Tokens: get by hash joined with owner and project key, list for user (newest first), revoke (set `revoked_at` only if NULL), touch `last_used_at` with a guard "older than 60 s", count active for user.
- Users: `CreateUser`, `GetUserByEmail` and `ListUsersByIDs` exist already. Confirm the NOCASE collation applies and that the UNIQUE violation maps to `email_taken`.

**Tests:** `internal/store/store_test.go` additions.
- Token touch is a no-op inside the 60 s window.
- Count excludes revoked tokens.
- Cascade: deleting a project removes limited tokens.

**Verify:** `CGO_ENABLED=0 go test ./internal/store/... -count=1`.

---

## WP2. Service additions: users and tokens (size M, about 350 LOC plus about 400 test LOC)

**Depends on:** WP0 and WP1.

**Files:** `internal/service/users.go`, a new `internal/service/tokens.go`, the `Services` struct (add `Tokens`), and `testutil`.

1. **Users** (P2 §0):
   - `Create`: normalise (trim and lowercase) the email, run `ValidateNew`, return `email_taken` (`KindConflict`) including under a UNIQUE race.
   - `ValidateNew(email, displayName) *Error`: collects all fields in one 422, with no DB access.
   - `Credentials`, `PasswordHash`.
   - `SetPassword(ctx, userID, newHash, keepSessionHash)`: updates the hash and deletes the other sessions in one tx. An empty keep value deletes all sessions (the CLI reset path). This reuses WP1's `DeleteSessionsForUserExcept`.
2. **Tokens:**
   - `Create`: session actors only (a token actor gets `session_required`, checked first). A `ProjectRef` resolves through the membership check (404 if not a member). Enforce at most 100 active tokens per user, else 422 `fields.name` "Too many active tokens; revoke one first". Validate name (1..100) and scope.
   - `List` (own tokens, newest first, revoked included), `Revoke` (idempotent; another user's id is 404).
   - The secret never enters the service; it receives only `Hash` and `Prefix`.
3. **Extend testutil** (P1 §9): `NewToken` should create a token with a real hash so later HTTP tests can mint secrets.

**Tests** (external package `service_test`, driven by the fake clock):
- Email normalisation, and a concurrent `Create` race on a file DB gives exactly one winner.
- The 101st active token fails while revoked tokens do not count.
- A token actor calling any of these gets `session_required` before any lookup or validation.
- `SetPassword` keeps only the named session.

**Verify:** `CGO_ENABLED=0 go test ./internal/service/... -count=1`.

---

## WP3. `internal/auth` primitives (size M, about 450 LOC plus about 500 test LOC)

**Depends on:** WP0 only for types. **Can run in parallel with:** WP1 and WP2. Split into three independent sub-steps (3a, 3b, 3c) that different people can take.

### 3a. `password.go` (P2 §2 task 1)

- Define `Params{Memory, Time, Threads, KeyLen}`, `DefaultParams` (64 MiB, t=3, p=2, 32-byte key, 16-byte salt) and `Hasher{params, sem, now}`.
- `Hash(ctx, pw)` and `Verify(ctx, pw, phc)`. Verify parses parameters from the stored string (never from config) and uses `subtle.ConstantTimeCompare`.
- Semaphore of 4 concurrent hashes. Acquisition is `select` on the semaphore and `ctx.Done()`.
- Password rules: 10..200 runes (`utf8.RuneCountInString`). `DummyHash()` is computed once at startup.
- **Argon2 test-cost strategy (a key risk):**
  - Hasher takes `Params` by injection.
  - Everything except the dedicated production-params test uses `TestParams{Memory: 8, Time: 1, Threads: 1}`.
  - Exactly one test (`TestDefaultParamsPHC`) hashes once with `DefaultParams` and asserts the literal prefix `$argon2id$v=19$m=65536,t=3,p=2$`. That is about 100 to 200 ms.
  - A second test hand-builds a PHC string with different params and checks Verify honours them.
  - Do not use `t.Parallel()` with production params (about 64 MiB each).
  - The harness `Params` value must carry the same constants the CLI uses, so there is one source of truth (P6 greps for a single `argon2.IDKey` call site).

### 3b. `apitoken.go`

- `Generate() (secret, hash, prefix)`: `pb_` plus 64 hex characters, SHA-256 hex hash, prefix is the first 8 characters.
- `ParseBearer(header)`: scheme is case-insensitive, with a regex check of `^pb_[0-9a-f]{64}$` before any DB lookup.
- `HashToken(secret)`.

### 3c. `ratelimit.go` (P2 §7)

- `Limiter{now func() time.Time, limit, window, maxKeys}` with a mutex-protected map.
- `Allow(key) (ok bool, retryAfter time.Duration)`. Fixed window per key, and `retryAfter` is whole seconds, at least 1.
- Eviction on a timer plus a hard cap (10,000, evict oldest). Inject the clock and make the eviction sweep callable from tests (`Sweep()`) so no goroutine is needed in tests.
- `ClientIP(r, trustProxy) string`, using `net/netip`:
  - Strip port and zone, and unmap IPv4-in-IPv6.
  - IPv6 collapses to the /64 prefix.
  - With `TRUST_PROXY`, take the first entry of the first `X-Forwarded-For` line, falling back to `RemoteAddr` if it is invalid.
- Bucket key helpers: `LoginKey(ip, email)` and `PasswordKey(userID, ip)`.

**Tests:**
- Argon2: PHC format, wrong password fails, dummy-hash path, semaphore respects a cancelled context.
- Token regex table: `Bearer pb_` plus 63 or 65 hex, uppercase hex, `Basic`, a 10 KB header, and lowercase `bearer`.
- Limiter: 5 allowed then the 6th denied, window reset via the fake clock, separate buckets, cap eviction, the ClientIP table (mapped v4, /64 sharing, spoofed XFF ignored when the flag is false).

**Verify:** `CGO_ENABLED=0 go test ./internal/auth/... -count=1`.

---

## WP4. Sessions, Principal and Resolver (size M, about 400 LOC plus about 450 test LOC)

**Depends on:** WP1, WP2 (Users) and WP3.

**Files:** `internal/auth/session.go`, `principal.go`, `resolver.go`.

- `Sessions{store, now, cookieSecure}`:
  - `Create`: 32 random bytes, base64url; stores only the SHA-256.
  - `Lookup` and `Slide` (see the sliding rule below).
  - `Delete`, `PurgeExpired`, and the exported `Validate(ctx, hash) error` (P2 §3: read-only, returns `ErrInvalidCredentials` for a missing or expired row, any other error for DB failures).
  - `Cookie(value)` and `ClearCookie()`: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Max-Age=2592000`, `Secure` per config.
  - `StartPurge(ctx)`: hourly plus once at start, stoppable. Take a `time.Ticker` interface or channel so the test does not sleep.
- `Principal` and `Actor()`:
  - `Actor()` returns `service.UserActor` or `service.TokenActor` and nothing else.
  - `SessionInfo.TokenHash` is exported (Phase 3 needs it).
  - Compile-time check that `Principal` never fills the `Actor` struct by hand.
- `Resolver.Resolve(r)` and `ResolveBearer(r)`, the single resolution function (P2 §3):
  - Any `Authorization` header means bearer only. Cookies are ignored and neither read nor cleared on failure.
  - Otherwise, the `pb_session` cookie.
  - Otherwise, `ErrNoCredentials`.
  - Token hit: touch `last_used_at` with the 60 s throttle; a failed touch is logged and ignored.
  - Session hit: reject expired (and signal a clear-cookie), slide only when `expires_at - now < 30d - 1h`, and signal a re-issued cookie.
- **Design point to settle at the start of this step:** `Resolve(r)` has no `ResponseWriter`, yet it must "re-issue the cookie" on slide and "clear the cookie" on an expired session. Return the cookie action in the `Principal` (`Principal.SetCookie *http.Cookie`) and in a typed error (`*AuthError{ClearCookie bool}`). The route-guard middleware applies it (WP5). Keep the `Resolve(r) (Principal, error)` signature to match the spec.

**Tests (fake clock, in-memory service env):**
- DB holds only the hash of the cookie value.
- Slide happens after 1 h plus 1 s and does not write inside the hour (count writes).
- Expired session is rejected and flagged for cookie clearing.
- Bearer failure never falls back to a valid cookie, and both present means the token wins.
- Malformed bearer causes zero DB queries (use a counting `db.DBTX` wrapper or a statement counter).
- Revoked and unknown tokens are indistinguishable.
- `last_used_at` throttle is 60 s.
- `Validate` does not slide and does not write.
- Purge removes expired rows.

**Verify:** `CGO_ENABLED=0 go test ./internal/auth/... -count=1`.

---

## WP5. `httpapi` core: errors, middleware, route table, server skeleton (size L, about 900 LOC plus about 700 test LOC)

**Depends on:** WP4. WP5a to WP5d can proceed in parallel after the shared test harness (5.0) exists. Build 5.0 first.

### 5.0 Test harness: `internal/httpapi/harness_test.go`

- Builds the real `Server` over `testutil.Env` with a shared fake clock for service, sessions and limiter.
- Helpers: `signup(email)`, `login`, a cookie-jar client, `mkToken(scope, projectRef)`, `do(method, path, body, creds...)`. The helper always sets `Origin: BASE_URL`, with an `noOrigin` option.
- Hasher uses `TestParams`.
- A `Setup(t, opts{AllowSignup, CookieSecure, TrustProxy, BaseURL})`.

### 5a. `errors.go`, `requests.go`, `pagination.go`

- `WriteError(w, status, code, msg)` is exported (Phases 3 and 4 use it). Also `writeFields`.
- `mapError(err)` uses `errors.As(*service.Error)`:
  - Kind to status.
  - Any `KindValidation` becomes code `validation_failed`.
  - `not_author` becomes `forbidden`.
  - `session_required` and `insufficient_scope` pass through.
  - Anything else becomes a 500 `internal`, logged and never echoed.
- Decode helper `decodeJSON(w, r, &dst)`: checks the content type first (415), then `DisallowUnknownFields`, then trailing data (400), with `*http.MaxBytesError` mapped to 400 `body_too_large`. A "body present" rule covers `Content-Length > 0` or unknown length.
- `requests.go`: `field[T]` that distinguishes absent, null and value, which converts to `service.Optional`, plus per-endpoint DTOs.
- Banned PATCH fields: `status` and `position` give 400 "use /move". A custom `UnmarshalJSON` or a pre-scan for them is simplest.
- `pagination.go`: `parseLimit`. Default 50, above 200 clamps to 200, non-integer or < 1 gives 422 `fields.limit`. The cursor passes through unchanged.

### 5b. `middleware.go` (P2 §3 chain, outermost first)

1. recover.
2. request id (ULID in `X-Request-Id`) plus `slog` log. Path only, never the query. Never log `Authorization`, `Cookie` or `Set-Cookie`.
3. security headers: the exact CSP string from P2 §11 as an exported const, plus `nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY` and `Permissions-Policy`. HSTS only if `BASE_URL` is `https://`.
4. body cap: reject `Content-Length > 1 MiB` upfront, `MaxBytesReader` otherwise.
5. Origin check (P2 §8):
   - Origin present on POST, PATCH or DELETE under `/api/`: scheme, host and port must equal `cfg.Origin()` (Phase 1 normalised form), else 403 `origin_mismatch`. `Origin: null` also fails.
   - Origin absent: allowed only with an `Authorization` header.
   - On every method under `/mcp`: present must match, absent is allowed.
   - Reuse `Config.Origin()` from Phase 1 for normalisation. Do not re-parse `BASE_URL`.
6. route guard (P2 §3 order).

- The wrapper `ResponseWriter` implements `Unwrap()` and `Flush()`, with a test using `http.NewResponseController`.
- Set `Cache-Control: no-store` before the handler runs, so a handler can override it. Phase 3 needs its SSE `no-store, no-transform` to win.

### 5c. `server.go`

- `route{Pattern, Access, Write, Handler}`, `Access` (`Public`, `Authed`, `SessionOnly`), and the guard in the exact documented order: resolve (401, apply cookie action), `SessionOnly` (403), `Write` with a read token (403 `insufficient_scope`), handler.
- `New(deps)`, `Handler()`, `Routes()`, `Mount`, `MountRaw`, `SetFallback`, `GET /healthz` (pings the DB, 200 or 503, `no-store`).
- **Go 1.22 mux risks** (P2 §3 "Routing mechanics"):
  - Register `/api/` method-less as the JSON 404 catch-all.
  - Never register `GET /`. The fallback is the method-less `/`.
  - The catch-all produces 405 by probing: for each of GET, POST, PATCH, PUT and DELETE, clone the request, call `mux.Handler(clone)`, and if the returned pattern is neither `""` nor `/api/`, collect the method into the `Allow` header and return 405 `method_not_allowed`. `OPTIONS` therefore returns 405 JSON and no `Access-Control-*` header is ever set.
  - Use the stdlib mux and register all patterns in one function; a test builds the server and fails on panic (a conflict panics at registration).
  - Known traps to test explicitly:
    - `/api/` and `POST /api/v1/tickets/{id}/move` are not a conflict (the second is strictly more specific).
    - `POST /api/v1/projects/{id}/members` and `PATCH /api/v1/projects/{id}/members/{userId}` are fine.
    - `DELETE /api/v1/tokens/{id}` vs `/api/` is fine.
    - `/healthz` and `/` coexist.
    - `Mount("/", Public, h)` coexists with `/api/` and `/mcp` without a panic.
    - Go's mux 301-redirects unclean paths (`/api//x`). Decide whether to keep or neutralise that (low priority).
    - `HEAD` maps to `GET` patterns.
  - Path wildcards `{id}` carry a project key or ULID. Take `r.PathValue("id")`, URL-decoded, and pass it to the service unvalidated; the service returns `ErrNotFound` for garbage.
- The route table is the single source of truth. A test enumerates `Routes()` and fails if any `/api/v1` route lacks an explicit `Access`. Use a sentinel zero value (`AccessUnset = 0`) so the test can detect "not set".

### 5d. `view.go`

Response structs and mappers for the P2 §6 shapes. Define the golden shapes as Go structs and the golden test (WP8) as the spec of record. Key behaviours:
- Ticket list item vs full: `description` is `omitempty` only for list items, so use two types or a pointer.
- `owner_name` is `omitempty`.
- Nullable fields are explicit `*T` without `omitempty`.
- Timestamps use `store.FormatTime`.
- `role` is the effective role as returned by the service.
- `counts` always has four keys.
- Move response adds `renumbered` only for move (an embedded struct with a pointer).
- Unresolved author renders as "Unknown" with `bot: false`.

**Tests in this WP** (against stub routes plus real middleware):
- Chain: every response has the headers and `X-Request-Id`.
- A forced panic gives a generic 500.
- Origin matrix (P2 §13 CSRF section).
- Content-type 415, trailing data 400, unknown field 400, 1 MiB+1 gives 400 `body_too_large`.
- Guard order table.
- `Routes()` coverage.
- 404 and 405 JSON with `Allow`.
- Fallback and `/mcp` `MountRaw` behaviours.
- Log redaction (capture the slog buffer).
- `Flush` and `Unwrap`.

**Verify:** `CGO_ENABLED=0 go test ./internal/httpapi/... -run 'Middleware|Routing|Origin|Headers|Errors|Guard' -count=1`.

---

## WP6. Resource handlers (size L, about 1,300 LOC plus about 1,500 test LOC)

**Depends on:** WP5. The sub-steps are parallelisable (one file each, no shared state except `Server`). Suggested ownership grouping: {6a, 6b} {6c, 6d} {6e} {6f}. Each step lands its routes in the route table and its tests.

| Step | File | Endpoints | Notes |
|---|---|---|---|
| 6a | `auth_handlers.go` | `GET /auth/config`, `POST /auth/signup`, `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `PATCH /auth/me`, `POST /auth/me/password` | See below |
| 6b | `tokens_handlers.go` | `GET`, `POST /tokens`, `DELETE /tokens/{id}` | `POST` generates a secret via `auth.Generate`, then calls `Tokens.Create`. Response is `{token, secret}`. All `SessionOnly`, none `Write` |
| 6c | `projects_handlers.go` | `GET`, `POST /projects`, `GET`, `PATCH`, `DELETE /projects/{id}` | `GET /projects/{id}` is `project_detail` with counts. `?archived=true`. `PATCH` accepts `{}` as a no-op. `DELETE` is `SessionOnly` |
| 6d | `members_handlers.go` | `GET`, `POST /projects/{id}/members`, `PATCH`, `DELETE .../members/{userId}` | Add, role change and remove are `SessionOnly`. Remove allows owner or self, enforced in the service. Mapping `user_not_found` to `fields.email` is already in `mapError` |
| 6e | `labels_handlers.go` and `tickets_handlers.go` | labels (4 routes), tickets (7 routes including `move`, `activity`) | See below |
| 6f | `comments_handlers.go` | `GET`, `POST /tickets/{id}/comments`, `PATCH`, `DELETE /comments/{id}` | Edit by exact actor is a service rule. `Comments.List` uses the paged form |

**Auth handlers (6a) must get these details right:**
- Signup when `ALLOW_SIGNUP=false`: return the standard 404 `not_found` before decode, rate limit and validation. The route is still registered. Test that the body is byte-identical to an unknown-route 404.
- Rate limit order: decode, rate limit, validate (signup) or verify (login). The attempt is counted even if the body is garbage (empty email key).
- Login with empty email or password is 401 `invalid_credentials`, never 422.
- Login of an unknown email still verifies against the dummy hash.
- Signup: `Users.ValidateNew` and the password rule together give one 422 with all fields, then hash, then `Users.Create` (409 on race), then session.
- Rotation: if a valid `pb_session` is presented, delete that row in the same step.
- `POST /auth/me/password`: order is rate limit (user, IP), decode, verify current (422 `fields.current_password` "Incorrect password"), validate new (422), hash, `SetPassword(keepHash = Principal.Session.TokenHash)`, 204.
- `GET /auth/config` returns exactly `{"signup_enabled": bool}`.

**Ticket handlers (6e):**
- The list handler makes exactly one `Members.List` call and fills `assignee` objects from it.
- Create, PATCH, move and GET all re-fetch via `Tickets.Get` and render the full shape. Move also sets `renumbered`.
- Filter parsing:
  - A repeated query parameter gives 422 for that field.
  - `assignee=me` becomes the principal's owner user id, `none` becomes unassigned.
  - `q` over 200 chars gives 422.
  - Invalid enums give 422 `fields.<name>`.
- `PATCH` with `status` or `position` gives 400.
- Move: if more than one of `before`, `after` and `place` is set, the service validation returns 422 (`fields.place`); `anchor_invalid` maps to `fields.before` or `fields.after`.

**Testing order inside each step:** the golden key-set test for the resource's shape first, then the happy path, then role and scope rows, then the validation rows.

**Verify per step:** for example `CGO_ENABLED=0 go test ./internal/httpapi/... -run 'Auth' -count=1`; then 'Tokens', 'Projects', 'Members', 'Labels', 'Tickets', 'Comments'.

---

## WP7. CLI, purge, graceful shutdown, `serve` wiring (size M, about 450 LOC plus about 350 test LOC)

**Depends on:** WP4 and WP5 (serve), WP2 and WP3 (CLI). The CLI part can start right after WP2 and WP3. **Can run in parallel with:** WP6.

**Files:** `cmd/pabrika/main.go`, `user.go`, `healthcheck.go`, `serve.go` (extracted so it is testable).

- Subcommand dispatch with stdlib `flag` per subcommand (`FlagSet`, `ContinueOnError`). Exit codes: usage 2, runtime 1, success 0, messages to stderr. Flags must precede positionals.
- `user create --email --name [--password-stdin]` and `user reset-password [--password-stdin] EMAIL`:
  - Store open plus `Migrate` first, so these work on a fresh volume.
  - Password comes from stdin (one line) or an `x/term` prompt (`term.ReadPassword`). With no TTY and no flag, it fails. Never accepted as an argument.
  - Call `Users.Create` or `SetPassword` directly. Reset passes an empty keep hash (delete all sessions) and does not revoke tokens.
  - Expose as `runUserCreate(args, stdin, stderr, deps) int` so tests can call it.
- `healthcheck`: reads only `PORT`; GET `127.0.0.1:$PORT/healthz`, 3 s timeout, silent on success, with an exit code. Test against `httptest` on a real listener and a closed port, with `BASE_URL` set to garbage.
- `serve`:
  - `http.Server` with `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `IdleTimeout` 120 s, and no `WriteTimeout`. Listens on `:PORT`.
  - Starts the session purge goroutine.
  - Wires an optional `shutdownHook func()` for the Phase 3 hub, which is a no-op for now.
  - Signal handling through `signal.NotifyContext`, in the documented order (P2 §12.1): stop accepting connections and purge, hub hook, `srv.Shutdown` with an 8 s deadline, `srv.Close()` on error, `Store.Close()` (WAL checkpoint), return nil so the process exits 0.
  - Extract `run(ctx, cfg, deps, shutdownTimeout) error` with an injectable timeout so the test uses 200 ms.
  - Test: a hung in-flight request plus a fake hook that records the call order. Assert hook, then Shutdown, then store close; on timeout `Close` is called and the result is nil; reopening the file DB sees all data.

**Verify:** `CGO_ENABLED=0 go test ./cmd/... -count=1`, plus a manual smoke run: `CGO_ENABLED=0 go run ./cmd/pabrika serve`, then `curl -i localhost:8080/healthz` and `go run ./cmd/pabrika healthcheck; echo $?`.

---

## WP8. Matrix, golden, hardening and exit-criteria pass (size L, about 1,200 test LOC)

**Depends on:** WP6 and WP7. Most cross-cutting tests can be written earlier as table skeletons and filled in as resources land. The four tracks below are independent of each other.

1. **Scope x role matrix** (`httpapi/matrix_test.go`, P2 §13): table-driven off `Server.Routes()`, with principals {anonymous, non-member session, viewer, editor, owner, write token as viewer, editor and owner, read token as editor and owner, limited write token for this project and for another}. Include:
   - Session-only routes with a token (403 `session_required` before any 404 or 422).
   - Demoting a user causes an existing write token to lose its effect immediately.
   - The order-of-checks case with input that is invalid, forbidden and archived at once.
2. **Non-member 404**: for every project-scoped route, the body is byte-identical to the "random valid ULID" response. A removed member gets 404 immediately. Use a route-pattern substitution helper so adding a route automatically adds a test row.
3. **Golden key-set tests** (`golden_test.go`): decode into `map[string]any` and compare the exact sorted key set plus JSON types for every shape in P2 §6. Risk handling:
   - Keep expected key sets as literal Go slices in the test, never derived from the view structs, so renames fail the test.
   - Assert nullable keys are present as `null`, `description` is absent only on list items, `owner_name` is absent for user authors, `counts` has four keys, and `project.role` is `viewer` for a read token.
   - Write fixtures as JSON files under `internal/httpapi/testdata/shapes/` so Phase 5 can copy them to its `tests/fixtures`.
4. **Hardening tests**: the TRUST_PROXY table, the log-redaction test (secrets in query string and headers), the HSTS conditional, the `X-Request-Id` check, concurrent signup (one 201 and one 409), and the 429 `Retry-After` tests with clock advance.

**Final gate** (all must pass):
```
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./... -count=1
```
Also run `gofmt -l .` (must print nothing) and `grep -rn "argon2.IDKey" internal` (one call site in `password.go`).

---

## Dependency graph and parallelism

```
WP0 ──► WP1 ──► WP2 ──► WP4 ──► WP5 ──► WP6 (6a..6f parallel) ──► WP8
   └──► WP3 (3a,3b,3c parallel) ──┘         └──► WP7 (CLI part can start after WP2+WP3)
```

- Parallel from the start (after WP0): WP1 and WP3.
- WP2 needs WP1's queries.
- WP7's CLI half needs only WP2 and WP3. Its `serve` half waits for WP5.
- WP8 test skeletons (matrix tables, golden key lists) can be drafted during WP6.

## Size summary

| WP | Prod LOC | Test LOC | Rough effort |
|---|---|---|---|
| 0 Phase 1 additive changes | 250 | 150 | S (0.5 d) |
| 1 Store queries | 150 SQL | 80 | S (0.5 d) |
| 2 Service users and tokens | 350 | 400 | M (1 d) |
| 3 Auth primitives | 450 | 500 | M (1 d) |
| 4 Sessions, Principal, Resolver | 400 | 450 | M (1 d) |
| 5 httpapi core | 900 | 700 | L (2 d) |
| 6 Handlers | 1,300 | 1,500 | L (2.5 d) |
| 7 CLI, shutdown, serve | 450 | 350 | M (1 d) |
| 8 Matrix, golden, hardening | 0 | 1,200 | L (1.5 d) |
| **Total** | about 4,250 | about 5,300 | about 11 agent-days sequential, about 6 to 7 with two parallel tracks |

New dependencies: `golang.org/x/crypto` (argon2) and `golang.org/x/term`. `oklog/ulid` is already there from Phase 1.

## Definition of done mapped to exit criteria

| Exit criterion (phases.md and P2 §13) | Proven by |
|---|---|
| `httptest` coverage of auth flows | WP6a tests, WP4, WP8 track 4 (cookie attributes, fixation, rotation, rate limit, session expiry and slide, bearer rules, tokens) |
| Scope and role matrices | WP8 track 1 (table off `Routes()`), plus a service-level scope test from WP0 and WP2 |
| Session-only endpoints | WP5 guard-order test and the WP8 track 1 session-only rows (403 `session_required` before 404 or 422) |
| Error shapes for every documented status | WP5 errors test (400 `bad_request`, `body_too_large`, `invalid_cursor`; 401; 403 all four codes; 404; 405; 409 codes; 415; 422; 429; 500; unknown route) |
| Non-members 404, never 403 | WP8 track 2, byte-identical to the random-ULID response, on every project-scoped route |
| No CGO | Gate commands with `CGO_ENABLED=0` |
| Phase 3 to 6 hooks present | WP5 and WP7 tests: `Mount`, `MountRaw`, `SetFallback`, `Sessions.Validate`, `WriteError`, `ResolveBearer`, `Flush` and `Unwrap`, headers on every response, 8 s shutdown order, `/healthz`, `healthcheck` |
| Headers and caps | WP5 headers test (exact CSP const, HSTS conditional, `no-store` on `/api/`, `/mcp`, `/healthz`), WP5 body-cap test |

When done, update the Phase 2 status in `phases.md`. Do not edit `CLAUDE.md` (P2 §2 task 13).

## Risks and de-risking

| Risk | Mitigation |
|---|---|
| Argon2 cost slows tests (64 MiB x many users) | Injectable `Params`, `TestParams` everywhere except one production-params test. Semaphore of 4 also bounds parallel tests. Harness creates users through `testutil.NewUser` (placeholder hash) where login is not under test, and through the real signup only where it is |
| CSRF and Origin edge cases | Reuse `Config.Origin()` for normalisation. Origin runs before routing, so unknown routes also get `origin_mismatch`. A full table test covers default ports, `null`, scheme and port mismatch, bearer without Origin, cookie without Origin, GET with a foreign Origin, and every method on `/mcp`. Harness defaults `Origin` to `BASE_URL` |
| Rate-limit clock and unbounded growth | Single injected clock shared by service, sessions and limiter. `Sweep()` is directly callable. Test the 10,000-key cap with a small configurable cap |
| Go 1.22 mux conflicts and 405 | Register everything in one function. A test constructs the server (panic means conflict), including `Mount("/", ...)` and a catch-all coexistence case. A table test for 404 vs 405 and `Allow`. Avoid `GET /`. Verify `HEAD` and `OPTIONS` behaviour |
| Golden shape drift | Literal expected key lists in the test, fixtures exported to `testdata/shapes/` for Phase 5 |
| Single-connection in-memory DB deadlocks | Never nest `WithTx`, and never touch the store from inside a callback. The resolver must not hold a `Rows` open while writing (`last_used_at` touch, slide). Use `Read()` for single statements only |
| In-memory store hides write-contention bugs | Run the concurrent-signup, SetPassword and Members tests on a file DB (via `store.Open` on `t.TempDir()`) |
| Cookie refresh and clear from a `Resolve(r)` with no writer | Principal and typed error carry the cookie action, applied by the guard (WP4) |
| SSE header override | Set `no-store` before the handler, so a handler can override (Phase 3) |
| Phase 1 divergence (list order, cursor, display name) | WP0 is a gate; keep the Phase 1 tests updated alongside |

## Open questions and spec inconsistencies

The specs claim none are unresolved, but the planner found these. None block a start; the proposed default in each case is safe.

1. **Project list order.** P1 §6.4 and §9 say `Projects.List` is key ascending (and tests it). P2 §0(d) and §6 say name then key. Proposed default: follow P2 (it is the later reconciliation) and change P1's order and test in WP0f.
2. **Cookie re-issue and clear from `Resolve(r)`.** The spec gives `Resolve` no `ResponseWriter` but requires cookie re-issue (§4) and clearing on expiry (§3, §4). Proposed default: the Principal and the typed error carry the cookie action, applied by the guard (see WP4).
3. **Shutdown order.** P2 §12.1 stops the purge job in step 1; P3 stops it after `srv.Shutdown`. Harmless. Proposed default: follow P2 (stop purge first), and treat P3's order as a description only.
4. **`PATCH /auth/me` with `display_name` absent.** P2 does not say whether this is 400 or 422. Proposed default: 422 `fields.display_name`.
5. **`DELETE /comments/{id}` and `PATCH /comments/{id}` routes in the non-member matrix.** They resolve via `Comments.Resolve` and the service. Proposed default: all give 404 for a non-member, identical to a random ULID.
6. **`session_required` for session-only operations from a non-member project.** Both the route guard and the service return it before any 404. Proposed default: the guard's check is authoritative for REST; the service check is the backstop for MCP and tests (P1 §6.3 step 0).

### Critical files for implementation
- `internal/httpapi/server.go`
- `internal/httpapi/middleware.go`
- `internal/auth/resolver.go`
- `internal/service/tokens.go`
- `cmd/pabrika/main.go`
