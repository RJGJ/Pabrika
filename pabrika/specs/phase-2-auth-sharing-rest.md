# Phase 2: Auth, sharing and REST

Detailed spec for phase 2 in [phases.md](phases.md). [main-spec.md](main-spec.md) is the source of truth and wins on any conflict. Sections used: Auth, REST API, Security checklist, plus the config and container contract for `healthcheck`.

**Goal:** humans (session cookie) and agents (bearer token) can use every feature over `/api/v1`, with one "current user" resolution, role and scope checks, and 404 for non-members.

**Out of scope:** `GET /projects/{id}/events` handler and the event hub (phase 3), `/mcp` (phase 4), the Vue app and static file serving (phases 5 and 6), Dockerfile (phase 6). This phase only reserves the mount points (see "Hooks for later phases").

---

## 0. What this phase expects from Phase 1

Phase 1 is not redesigned here. Phase 2 needs the following; if something is missing, add it as a small additive change in Phase 1's packages (and note it in the plan), not a redesign.

- **Store:** sqlc queries for all ten tables, a single-writer setup, `store.Open(path)` and an in-memory variant for tests. Phase 2 may add sqlc queries (sessions, tokens, users, purge jobs).
- **Service layer** has no HTTP knowledge and enforces membership, role, **token scope and project limit** itself (decision: no transport can skip them), so MCP gets the same rules later. Each method takes a `service.Actor`, exactly as Phase 1 defines it (`Type service.ActorType`, `ID`, `UserID`, `Scope service.Scope`, `ProjectID`), built **only** with Phase 1's constructors `service.UserActor(userID)` (Scope = write, ProjectID empty) and `service.TokenActor(tokenID, ownerUserID, scope, projectID)`; an empty session scope behaves as write. Phase 2 never fills the struct by hand. Phase 1 enforces: a project outside `ProjectID`, or one the user is not in, is `ErrNotFound`; a read-scope actor attempting any write is a `*service.Error` of Kind forbidden, code `insufficient_scope`; a token with `ProjectID` creating a project is Kind forbidden. The HTTP layer only builds the `Actor` from the `Principal`; the route-table `Write` flag is an early, cheap check, not the source of truth. The `role` shown in REST responses is the member role capped to `viewer` for read-scope tokens (the view layer does this; Phase 1 returns the raw member role).
- **Errors:** Phase 1 has one error type, `*service.Error{Kind, Code, Message, Fields}` (sentinels matched with `errors.Is` by Kind); Phase 2 defines no separate validation error type (no `ErrValidation{fields}`). The HTTP mapper uses `errors.As` and maps Kind to status (`KindValidation` 422, `KindBadRequest` 400, `KindForbidden` 403, `KindNotFound` 404, `KindConflict` 409) and uses `Code` as the JSON `code` and `Fields` as `fields`, with these exceptions: any `KindValidation` error is rendered as code `validation_failed` (so `user_not_found` becomes 422 `fields.email` "No account with this email" and `anchor_invalid` becomes 422 on `fields.before` or `fields.after`, whichever the request set); Phase 1's `not_author` (KindForbidden) is sent on the wire as `forbidden`. `session_required` may come from the service itself (token actors on session-only operations such as project delete, member management and profile update) as well as from the route guard; both render as 403 `session_required`. `email_taken` is created in `UserService.Create` via `service.NewError(KindConflict, "email_taken", "...")`. Anything that is not a `*service.Error` is a 500 `internal` (logged, never echoed). **Phase 1 already provides** `Fields map[string]string` keyed by the **REST JSON field names** (not Go names: `AssigneeID` reports as `assignee`, `LabelIDs` as `labels`). **Additive Phase 1 changes this phase needs** (small, do not redesign): (a) a malformed list cursor returns `KindBadRequest` code `invalid_cursor` instead of a validation error; (b) `email_taken` for users as above; (c) display name cap 1..100 (Phase 1 will be changed to match its current 80).
- **Request DTOs live in `httpapi`, not in the service.** Phase 1 service inputs are tag-free structs (`CreateTicketInput`, `UpdateTicketInput` with `Optional[T]`, ...). Phase 2 defines JSON request structs in `internal/httpapi/requests.go` (with `json` tags, `DisallowUnknownFields`, a small `field[T]` decoder type that distinguishes absent, `null` and value for PATCH) and converts them to service inputs. Length caps, enum checks, key format and the label palette are enforced by the service `Validate()` (Phase 1 `validate.go`), which Phase 4 shares by calling the same service methods.
- **Resolution helpers:** `Projects.Resolve(ctx, actor, ref)` (key or ULID, key case-insensitive) and `Tickets.Resolve(ctx, actor, ref)` (ULID or `KEY-N`) as defined in Phase 1; label and comment ids are resolved inside the service methods that take them. Non-members always get `ErrNotFound`.
- **List methods** return a page and an opaque next-cursor **string produced and parsed by the service** (Phase 1 defines the tickets and activity cursor format). The service owns and parses the cursor; Phase 2 does not encode or decode cursors (no HTTP cursor encoding anywhere): HTTP passes the opaque string through unchanged and only validates and clamps `limit` (section 6). **Additive Phase 1 changes** (a) `CommentService.List` takes `limit` and `cursor` and returns a page (like `ActivityService.List`), ordered `(created_at, id)` ascending; (b) ticket list items carry `CommentCount` (one grouped query, like labels); (c) `ProjectSummary` carries the caller's `Role`; (d) `ProjectService.List` stays unpaginated (name order, then key); (e) activity `changes` keys are exactly: `title`, `description`, `priority`, `due_date`, `assignee`, `labels`, `status`, `position`.
- Users, sessions and API tokens: Phase 1 provides tables and the store queries listed in its section 5 (`GetUserByEmail`, `GetUserByID`, `CreateUser`, `UpdateUserPassword`, `DeleteSessionsForUserExcept`, `CreateAPIToken`, `GetAPIToken`, ...). **Phase 2 adds** sqlc queries for sessions (create, get by hash, extend, delete by hash, delete all for user, delete expired) and tokens (get by hash with owner and project key, list for user, revoke, touch `last_used_at`, count active for user), and **extends `service.Services`** with:

  ```go
  // UserService gains (hashes are computed by internal/auth; the service never imports auth):
  Create(ctx, email, displayName, passwordHash string) (User, error)            // normalises email, validates, ErrConflict email_taken (also on UNIQUE violation under a race)
  ValidateNew(email, displayName string) *service.Error                          // field errors only, no DB; used so signup can report all 422 fields before hashing
  Credentials(ctx, email string) (userID, passwordHash string, err error)        // login; ErrNotFound if unknown
  PasswordHash(ctx, userID string) (string, error)
  SetPassword(ctx, userID, newHash, keepSessionHash string) error                // updates hash, deletes the user's other sessions (keepSessionHash "" deletes all: CLI reset)
  // TokenService (new, on Services.Tokens):
  Create(ctx, actor Actor, in CreateTokenInput) (Token, error) // CreateTokenInput{Name, Scope, ProjectRef, Hash, Prefix}; session actors only (token actor -> ErrForbidden); Hash/Prefix come from auth's generator, the plain secret never enters the service
  List(ctx, actor Actor) ([]Token, error)   // caller's own, newest first
  Revoke(ctx, actor Actor, tokenID string) error  // own tokens only, else ErrNotFound; idempotent
  ```
  Session persistence and cookie handling live in `internal/auth`.
- **Timestamps** in responses use the fixed-width form Phase 1 stores (`2006-01-02T15:04:05.000Z`, valid RFC 3339 UTC).

---

## 1. Packages and files

| Path | Responsibility |
|---|---|
| `internal/auth/password.go` | argon2id hash and verify (PHC string), password rules, concurrency limiter |
| `internal/auth/session.go` | session create, resolve, slide, delete, purge; cookie build and clear |
| `internal/auth/apitoken.go` | token generate (`pb_` + 32 random bytes hex), hash, prefix, resolve from bearer |
| `internal/auth/principal.go` | `Principal`, context helpers, `Principal.Actor()` |
| `internal/auth/resolver.go` | **the single current-user resolution** `Resolve(r) (Principal, error)` |
| `internal/auth/ratelimit.go` | in-memory limiter with injectable clock; `ClientIP(r, trustProxy)` helper |
| `internal/httpapi/server.go` | `New(deps) *Server`, route table, `Handler()`, `Mount` for later phases |
| `internal/httpapi/middleware.go` | recover, request log, security headers, body cap, Origin check, auth, route guards |
| `internal/httpapi/errors.go` | error shape, service-error mapping, JSON decode helpers |
| `internal/httpapi/pagination.go` | `limit` parsing and clamping; passes the opaque cursor string through |
| `internal/httpapi/requests.go` | JSON request structs and the absent/null/value `field[T]` decoder (section 0) |
| `internal/httpapi/view.go` | response structs and mappers (user, project, member, ticket, label, comment, activity, token) |
| `internal/httpapi/auth_handlers.go`, `tokens_handlers.go`, `projects_handlers.go`, `members_handlers.go`, `labels_handlers.go`, `tickets_handlers.go`, `comments_handlers.go` | one file per resource |
| `cmd/pabrika/main.go`, `cmd/pabrika/user.go`, `cmd/pabrika/healthcheck.go` | `serve`, `user create`, `user reset-password`, `healthcheck` |

`internal/config` already parses `TRUST_PROXY` (Phase 1; bool, default `false`). Phase 2 only consumes it: when true, the rate limiter and request log use the first (leftmost) hop of `X-Forwarded-For` as the client IP (falling back to `RemoteAddr` if the header is absent or unparsable); this assumes the proxy overwrites the header rather than appending a client-supplied value (Phase 6 documents it). Phase 2 also uses `BASE_URL`, `ALLOW_SIGNUP`, `COOKIE_SECURE`, `PORT`, `DB_PATH`. Dependency direction: `httpapi` -> `auth`, `service`; `auth` -> `store`; `service` knows neither.

---

## 2. Tasks

1. **Passwords** (`auth/password.go`): argon2id, 64 MiB, 3 passes, 2 threads, 32-byte key, 16-byte random salt, PHC string `$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>`. Verify parses params from the stored string and compares with `subtle.ConstantTimeCompare`. A semaphore (default 4 concurrent hashes) bounds memory. Minimum 10 characters, maximum 200 (DoS cap), counted in characters.
2. **Users** (service): signup/create validates email (trimmed, lowercased, <= 254, contains one `@`, basic shape), display name (trimmed, 1 to 100, same rule as Phase 1 `UpdateProfile`), password (in `auth`: 10 to 200 characters). All field errors of one request are reported together in one 422. Duplicate email -> `ErrConflict{email_taken}`, including when the UNIQUE constraint fires under a concurrent signup.
3. **Sessions** (`auth/session.go`): see section 4.
4. **API tokens** (`auth/apitoken.go`, service): see section 5.
5. **Resolver and `Principal`** (`auth/resolver.go`, `principal.go`): section 3.
6. **Rate limiter** (`auth/ratelimit.go`): section 7.
7. **Middleware chain and route table** (`httpapi`): sections 3 and 8.
8. **Error shape and mapping**, **pagination**, **view structs**: sections 6 and 9.
9. **Handlers** for every endpoint in section 10.
10. **Security headers, body and field caps**: section 8 and 11.
11. **CLI**: section 12.
12. **Tests**: section 13.
13. **Docs touch**: none beyond code comments (README is phase 6). Do not edit `CLAUDE.md`.

---

## 3. Current-user resolution and middleware chain

### Principal

```go
type Principal struct {
    User    AuthUser      // id, email, display_name, created_at
    Method  string        // "session" | "token"
    Session *SessionInfo  // when Method == session: TokenHash (sha256 hex of the cookie value), ExpiresAt
    Token   *TokenInfo    // when Method == token: ID, Name, Scope, ProjectID ("" = none), ProjectKey ("" = none)
}
func (p Principal) Actor() service.Actor
```

`Actor()` is the only place that turns an authenticated request into service permissions: sessions get `service.UserActor(userID)`; tokens get `service.TokenActor(tokenID, ownerUserID, scope, projectID)`. The service layer enforces scope and project limit from these fields. Phase 4 calls the same method. `SessionInfo.TokenHash` is exported on purpose: Phase 3's stream handler keeps `Principal.Session.TokenHash` and calls `auth.Sessions.Validate(ctx, tokenHash) error` on every keepalive tick. `Validate` is read-only (no sliding of expiry, no writes, no cookies), returns `auth.ErrInvalidCredentials` when the row is missing or expired, and any other error for DB failures (the caller keeps the stream open on those).

### `Resolve(r *http.Request) (Principal, error)` and `ResolveBearer(r)`

`ResolveBearer(r)` is step 1 alone and never looks at cookies; `/mcp` (Phase 4) uses it. `Resolve` is the full chain below.

1. If an `Authorization` header is present: it must be `Bearer <token>` (scheme case-insensitive) with the token matching `^pb_[0-9a-f]{64}$`, else 401 with no DB lookup (cookies are then ignored, no fallback, and the session cookie is neither read nor cleared). SHA-256 the token, look it up by `token_hash` (unique index), then `subtle.ConstantTimeCompare` on the hashes. Unknown or revoked -> 401. Valid -> load user and the token's project key, update `last_used_at` (throttled: only if older than 60 seconds, to avoid one write per request on the single writer connection; a failed update is logged and ignored). Failed bearer attempts are deliberately not rate limited (256-bit secrets, one indexed hash lookup each).
2. Else if the `pb_session` cookie is present: hash it, look up the session, reject if expired (401 and clear the cookie), load user, slide expiry (section 4).
3. Else `ErrNoCredentials` (401).

Returns a `Principal` or one of: `ErrNoCredentials`, `ErrInvalidCredentials`. Never distinguishes "revoked" from "unknown" to the client (same status, code `unauthorized`, same message "Authentication required").

### Middleware order (outermost first)

The first four layers are **outermost** and wrap the whole mux, so they cover `/api`, `/mcp`, `/healthz` and the SPA fallback (the method-less `/` catch-all) alike.

1. **recover**: panic -> 500 `internal` (generic message), logged with stack only (never request headers or bodies).
2. **request id and log** (`slog`): a fresh ULID per request is set as the `X-Request-Id` response header and logged with method, route pattern, `r.URL.Path` (never the query string), status, duration, user id if known, client IP (per `TRUST_PROXY`). Never logs `Authorization`, `Cookie`, `Set-Cookie`, request or response bodies, emails or passwords. A failed login logs the client IP only.
3. **security headers** (section 11), applied to every response including errors, 404s, `/mcp`, `/healthz` and the SPA fallback.
4. **body cap**: `http.MaxBytesReader` 1 MiB on every request (a `Content-Length` above the cap is rejected up front); overflow -> 400 `body_too_large`.
5. **Origin check** (section 8): on POST, PATCH, DELETE under `/api/`, with the bearer-or-cookie rules of section 8; and on **every method** under `/mcp`, where the only rule is "if an `Origin` header is present it must match `BASE_URL`, absent is allowed" (`/mcp` is not exempt). `/healthz` is GET only.
6. **route guard** from the route table (below): runs `Resolve` unless the route is `Public`, then enforces auth level. (`/mcp` is not in the route table; it authenticates itself with `ResolveBearer`, ignores cookies and requires a bearer token.)
7. handler.

**`TRUST_PROXY=true` and the proxy:** the app trusts the **first** `X-Forwarded-For` hop, so the reverse proxy must **overwrite** the header (set it to the real client address) and not append to a client-supplied value, or a client can choose its own rate-limit key and log IP. Phase 6 documents the proxy configuration.

The wrapper `ResponseWriter` must implement `Unwrap()` and `Flush()` (phase 3 SSE needs flushing).

### Mount points (exported for later phases)

- `Server.Mount(pattern, Access, handler)`: registers a route in the route table under the full guard chain (Phase 3 events). It appears in `Server.Routes()` so the coverage test sees it. `Mount` must also accept a **method-less `Public` catch-all `/`** (used by Phase 6's SPA handler); a `GET /` pattern would conflict with the method-less `/api/` catch-all at registration, so the SPA handler receives all methods and answers 405 for non-GET/HEAD itself. Only `/api/v1` routes are subject to the "explicit Access" coverage test.
- `Server.MountRaw(pattern, http.Handler)`: outermost (global) middleware only: recover, log, headers, body cap, and the `/mcp` Origin rule; no route guard, no `Routes()` entry. Used by Phase 4 for `/mcp`, which authenticates itself with `ResolveBearer` and writes failures with the exported `httpapi.WriteError(w, status, code, message)` (401 plus `WWW-Authenticate: Bearer`).
- `Server.SetFallback(http.Handler)`: sugar for `Mount("/", Public, h)`: the handler for everything not under `/api/`, `/mcp` or `/healthz` (Phase 6 static UI). It must return a JSON 404, never HTML, for unknown paths under `/.well-known/` as well. Default is a plain 404. It runs inside the global middleware.
- There is no exported membership-check helper: Phase 3 calls `Projects.Resolve(ctx, principal.Actor(), ref)` directly (returns `service.ProjectRef{ID, Key}`; `ErrNotFound` for non-members, a missing project or an out-of-limit token; works for archived projects).
- **Routing mechanics (Go 1.22 mux):** never register `GET /` (it conflicts with the method-less `/api/`). Register `/api/` (all methods) as the catch-all that returns the JSON 404, and make that handler also produce the 405: for a path that matches a registered route under another method it returns 405 `method_not_allowed` JSON with an `Allow` header (probe the mux for each method). The fallback is the method-less `/` catch-all above; `/healthz` and every `/api/v1` route are method patterns.

### Route table (single source of truth)

```go
type route struct {
    Pattern string        // "POST /api/v1/tickets/{id}/move"
    Access  Access        // Public | Authed | SessionOnly
    Write   bool          // changes data: token needs write scope
    Handler http.HandlerFunc
}
```

Guard order for each request (this order is part of the contract and is tested):

1. `Public`: skip auth (signup, login, `GET /auth/config`).
2. Resolve principal -> else **401** `unauthorized`.
3. `SessionOnly` and principal is a token -> **403** `session_required`.
4. `Write` and principal is a token with read scope -> **403** `insufficient_scope`.
5. Decode body (malformed JSON, unknown fields, trailing data -> 400; non-JSON content type -> 415 `unsupported_media_type`). These depend only on the request, never on the project, so they leak nothing about hidden resources.
6. Handler resolves the resource and membership via the service -> **404** if the user is not a member (or the project is outside the token's limit).
7. Role check in the service -> **403** `forbidden`.
8. Archived check -> **409** `project_archived`.
9. Validation -> **422**.

(The Origin check, section 8, runs before all of these, so a state-changing request with a bad Origin gets 403 `origin_mismatch` even on an unknown route.) So a non-member never sees 403 for a project-scoped thing except through steps 3 and 4, which depend only on the credential, not on the project. A test enumerates `Server.Routes()` and fails if any `/api/v1` route is not in the table or lacks an explicit `Access`.

`GET /healthz` (no auth, outside `/api/v1`) is registered as `Public`: pings the DB, returns 200 `{"status":"ok"}` or 503 `{"error":{"code":"internal","message":"Database unavailable"}}` (no driver detail), `Cache-Control: no-store`.

---

## 4. Sessions and cookies

- Cookie `pb_session`: value = 32 random bytes, base64url (no padding). DB stores only SHA-256 of the value (`sessions.token_hash`).
- Attributes: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Max-Age=2592000`, `Secure` when `COOKIE_SECURE=true`.
- Lifetime 30 days, **sliding**: on resolve, if `expires_at - now < 30d - 1h` (i.e. last extended more than an hour ago) set `expires_at = now + 30d` and re-issue the cookie. Throttled so reads do not write on every request.
- Signup and login create a new session (fresh random token every time; the server never accepts a client-chosen session value, so no session fixation). If the request already carries a valid `pb_session` cookie, that old row is deleted in the same step (rotation, so a stale session of a previous login in the same browser does not linger). Login does not delete the user's sessions in other browsers.
- Logout deletes the row (by hash) and sends an expired cookie with the same `Path` and attributes (`Max-Age=0`). A logout request with an already-gone session is 401 like any other (the cookie is cleared anyway).
- Expired sessions are rejected at resolve time and removed by a purge goroutine (hourly, plus once at startup), stopped on server shutdown.
- `pabrika user reset-password` deletes all sessions of that user (API tokens are not revoked).
- `POST /auth/me/password` deletes the user's **other** sessions and keeps the current one (the current row is not re-created; its expiry slides as usual).
- Unknown or expired cookie -> 401 and the response clears the cookie.

---

## 5. API tokens

- Secret: `pb_` + 64 hex chars (32 random bytes from `crypto/rand`). Stored: SHA-256 hex in `token_hash`, first 8 characters in `token_prefix` (includes `pb_`). The full secret is returned only in the `POST /tokens` response.
- Fields: `name` (1 to 100 chars), `scope` (`read` or `write`), optional `project_id` (project key or ULID; the creator must be a member of it, any role; the token is cascade-deleted with the project). At most 100 non-revoked tokens per user: beyond that, 422 `fields.name` "Too many active tokens; revoke one first".
- Effective permission in a project is `min(scope, owner's current role)` and is evaluated per request (roles can change after the token was created). Read scope means viewer-level everywhere.
- A project-limited token: lists show only that project; any other project, or its tickets, labels and comments, is 404 (decided). `POST /projects` with a limited token is 403 `forbidden` (decided; cannot create outside its limit). Both are enforced in the service via `Actor.ProjectID`.
- Revoke sets `revoked_at`; revoked tokens fail at resolve (401). Revoking twice is idempotent. Revoked tokens stay in the list with `revoked_at` so the UI can show them.
- Tokens cannot call anything `SessionOnly`: that is the whole of "tokens cannot create or manage tokens or change account details". Account details are `PATCH /auth/me` and `POST /auth/me/password`, both `SessionOnly`.

---

## 6. Conventions

**Error shape** (always JSON, `Content-Type: application/json`):

```json
{"error": {"code": "not_found", "message": "Project not found", "fields": {"title": "must be 200 characters or fewer"}}}
```

`fields` is present only for 422 (a flat `{json_field_name: message}` map, keys are the request body field names) and omitted elsewhere. `code` and `message` are always present and strings. Messages are human readable and never leak whether a hidden resource exists or echo secrets.

| Status | `code` values |
|---|---|
| 400 | `bad_request` (malformed JSON, unknown field, empty body where one is required), `body_too_large` (decided: 400, not 413), `invalid_cursor` |
| 401 | `unauthorized` (missing, unknown, expired or revoked credentials: one message), `invalid_credentials` (login only; one generic message for wrong email or password) |
| 403 | `forbidden` (role too low), `insufficient_scope`, `session_required`, `origin_mismatch` |
| 404 | `not_found` (also for non-members, out-of-limit tokens, signup when `ALLOW_SIGNUP=false`; also unknown routes under `/api/v1`) |
| 405 | `method_not_allowed` (router default, rewritten to JSON, `Allow` header kept) |
| 409 | `email_taken`, `key_taken`, `label_exists`, `already_member`, `last_owner`, `project_archived` |
| 415 | `unsupported_media_type` (body present with a non-`application/json` `Content-Type`) |
| 422 | `validation_failed` (with `fields`) |
| 429 | `rate_limited` (with `Retry-After` seconds) |
| 500 | `internal` |
| 503 | `unavailable` (a new SSE stream opened during shutdown, Phase 3; `/healthz` DB failure keeps its own `internal` body) |

405, 415, 429, 500 and 503 are additions to main-spec's list, confirmed by the user. There is no `signup_disabled` code.

401 responses for bearer failures include `WWW-Authenticate: Bearer`.

**Requests:** bodies are JSON; `Content-Type` must be `application/json` (parameters like charset allowed) on every POST, PATCH, DELETE that has a body, else **415** `unsupported_media_type`. Unknown fields -> 400. A body is "present" when `Content-Length` > 0 or the length is unknown (chunked); a present body with a missing or non-JSON `Content-Type` is 415. The body must be exactly one JSON object (trailing data -> 400). Endpoints that take no body ignore the body but still count against the 1 MiB cap and the 415 rule. Endpoints that need a body but have none -> 400 `bad_request`.

**Responses:** timestamps RFC 3339 UTC; ids are ULIDs; `Cache-Control: no-store` on all `/api/`, `/mcp` and `/healthz` responses. 201 for create (with the resource), 200 for update and reads, 204 with no body for deletes, logout and password change.

**Pagination:** list endpoints return `{"items": [...], "next_cursor": "..." | null}`. Query `limit` (default 50; a value above 200 is clamped to 200; non-integer or < 1 -> 422 `fields.limit`) and `cursor` (an opaque string made by the service; Phase 2 passes it through unchanged; an invalid one -> 400 `invalid_cursor` from the service; `cursor=` empty means first page). A cursor does not encode the filters, so reusing it with different filters is undefined but must not error or leak. Sort orders:

| Endpoint | Order |
|---|---|
| tickets | status (backlog, todo, in_progress, done), then `position`, then `id` |
| comments | `created_at` asc, `id` |
| activity | `created_at` desc, `id` desc (newest first) |
| projects | `name` asc, then `key` (Phase 1 order); returned whole (no HTTP slicing), `next_cursor` always null |
| members, labels, tokens | small bounded sets: returned whole (no HTTP slicing), `next_cursor` always null (same envelope). Members: owners first, then display name. Labels: name. Tokens: newest first |

**Shared resource shapes** (exact and complete; Phase 5 `types.ts` and Phase 4 rely on them; extra fields may be added later, none removed or renamed). Every listed key is always present (nullable ones are `null`, never omitted) unless marked "omitted when". `T|null` means nullable. Timestamps are strings in the format above; `id`s are ULID strings; `position` is a JSON number.

| Shape | Exact fields |
|---|---|
| `user` | `id`, `email`, `display_name` |
| `auth_user` (signup, login, `/auth/me`, `PATCH /auth/me`) | `user` fields plus `created_at` |
| `project` | `id`, `key`, `name`, `description`, `archived_at` (T\|null), `created_at`, `updated_at`, `role` (`owner`\|`editor`\|`viewer`, the caller's effective role: member role capped to `viewer` for a read-scope token) |
| `project_detail` (`GET /projects/{id}` only) | `project` fields plus `counts`: `{"backlog": int, "todo": int, "in_progress": int, "done": int}` (all four keys always present, non-deleted tickets only). Lists, create and PATCH return plain `project` (no `counts`) |
| `member` | `user` (`{id, email, display_name}`), `role`, `created_at` |
| `label` | `id`, `project_id`, `name`, `color` |
| `ticket` | `id`, `ref` (`"WEB-12"`), `project_id`, `project_key`, `number` (int), `title`, `description` (**omitted when** the ticket is a list item), `status`, `priority`, `assignee` (`{id, display_name, email}` \| null), `labels` (array of `label`, `[]` when none), `position`, `due_date` (`YYYY-MM-DD` \| null), `comment_count` (int, non-deleted comments), `created_at`, `updated_at` |
| `comment_author` | `type` (`"user"`\|`"api_token"`), `id`, `name` (user display name, or the token's name), `bot` (bool, true for `api_token`), `owner_name` (**omitted when** `type` is `user`) |
| `comment` | `id`, `ticket_id`, `author` (`comment_author`), `body`, `created_at`, `edited_at` (T\|null) |
| `activity` | `id`, `ticket_id`, `actor` (`comment_author`), `action` (`created`\|`updated`\|`moved`\|`assigned`\|`labeled`\|`deleted`), `changes` (object `{field: [old, new]}`; exact shape is defined by Phase 1 (keys `title`, `description`, `priority`, `due_date`, `assignee`, `labels`, `status`, `position`; `assignee` as user ids, `labels` as name lists, `description` truncated to 200 chars; values are string, number, list of strings, or `null`) so the UI can rely on it), `created_at` |
| `token` | `id`, `name`, `token_prefix` (first 8 chars, e.g. `pb_1a2b3`), `scope` (`read`\|`write`), `project` (`{id, key}` \| null), `last_used_at` (T\|null), `revoked_at` (T\|null), `created_at` |
| token creation response | `{"token": <token>, "secret": "pb_..."}` |
| list envelope | `{"items": [...], "next_cursor": string \| null}` |
| error | `{"error": {"code": string, "message": string, "fields"?: {string: string}}}` |

- **How the ticket shape is built.** List items: the service's list result plus `comment_count` (Phase 1 additive change) and `assignee` objects, which the list handler fills from **one** `Members.List` call (assignee id to member). Create, `PATCH`, `move` and `GET` responses all return the full shape (with `description`): after the write the handler calls `Tickets.Get` and renders that, so these four have identical shape. The move response additionally carries `renumbered` (bool, **omitted when** the request was not a move); `true` means other cards' positions changed and the client should reload the board.
- An author or actor that cannot be resolved (for example a deleted account) renders as `name: "Unknown"`, `bot: false`.
- Soft-deleted comments and tickets are never returned. Labels are returned whole inside a ticket, never as ids only.

---

## 7. Rate limiting

- In-memory fixed-window (or token-bucket) limiter, injectable clock, mutex-protected map, entries evicted on a timer and hard-capped (for example 10,000 keys; at cap, evict oldest) so it cannot grow without bound.
- Applies to `POST /auth/login` and `POST /auth/signup`: **5 attempts per minute per (client IP, trimmed lowercased email)**, separate buckets for login and signup. Every attempt counts, successful or not, including bodies with an empty or malformed email (key uses the raw trimmed lowercased value; a body that cannot be decoded at all uses the empty email). The check runs before any argon2 work and after the body is decoded (so the email is known). Not added (deliberate, per main-spec): a global per-IP bucket across emails; argon2 memory is bounded by the hash semaphore instead.
- Also applies to `POST /auth/me/password`: **5 attempts per minute per (user id, client IP)**, its own bucket. Every attempt counts; the check runs before any argon2 work.
- Exceeded -> 429 `rate_limited` with `Retry-After`.
- Client IP: the host part of `RemoteAddr` by default. When `TRUST_PROXY=true`, the first (leftmost) hop of `X-Forwarded-For` (trimmed; if missing or not a valid IP, fall back to `RemoteAddr`; with several header lines or a comma list only the first entry of the first line counts). The same value goes in the request log. With `TRUST_PROXY=false` the header is ignored entirely. Parse with `net/netip`, strip any port or zone, unmap IPv4-in-IPv6, and for IPv6 key the limiter on the /64 prefix so rotating the low 64 bits does not evade it.
- The `Retry-After` value is the whole seconds until the bucket's window resets (at least 1).
- Login of an unknown email still performs a dummy argon2 verify (a hash of a random value computed once at startup with the same parameters) so timing does not reveal whether the email exists. Acquiring the argon2 semaphore respects the request context (a cancelled request stops waiting).

---

## 8. CSRF and Origin

- `SameSite=Lax` cookie, JSON-only bodies (a cross-site form cannot send `application/json`), plus the Origin check below.
- On every POST, PATCH and DELETE under `/api/`:
  - `Origin` present: its scheme, host and port must equal those of `BASE_URL` (scheme and host compared case-insensitively; an omitted port equals the scheme's default, so `https://x` equals `https://x:443`; any path in `BASE_URL` is ignored). `Origin: null` or any mismatch -> 403 `origin_mismatch`. The `Host` header and `X-Forwarded-*` are never used.
  - `Origin` absent: allowed only if the request carries an `Authorization` header (non-browser client, no ambient credentials). A cookie-bearing or unauthenticated request without `Origin` -> 403 `origin_mismatch`. (This also covers login and signup, preventing login CSRF.)
  - A bearer request whose `Origin` is present is still checked; there is no CORS: no `Access-Control-*` headers are ever sent and `OPTIONS` is not handled.
- GET and HEAD under `/api/` never change state, so they are not checked.
- **`/mcp`** ignores cookies and requires a bearer token (no cookie fallback). If an `Origin` header is present it must match `BASE_URL` by the same comparison, else 403 `origin_mismatch`; an absent `Origin` is allowed (agents are not browsers). This applies to **all methods** on `/mcp`. Browser-origin MCP clients are therefore rejected.
- Test clients (including `httptest` helpers) must set `Origin` to `BASE_URL`.

---

## 9. Handlers: general rules

- Handlers are thin: decode, call the service with `principal.Actor()`, map result or error to JSON. Scope and project-limit enforcement lives in the service, not here.
- Resource lookups for `{id}` on tickets accept a ULID or a reference (`WEB-12`, case-insensitive key). Project paths (`/projects/{id}` and every `/projects/{id}/...`) accept the project **key or ULID** (`/projects/WEB`, `/projects/01J...`). Label, comment, member (user) and token ids are ULIDs only.
- Any path parameter that resolves to nothing, or to something in a project the caller cannot see, is 404 with the same message ("Not found" per resource type), identical in both cases.
- Archived projects are read-only: any ticket, label or comment write in an archived project -> 409 `project_archived`. Only `PATCH /projects/{id}` (to unarchive) and member and project deletion remain possible.
- Ticket `assignee` must be a member of the project; label ids must belong to the same project. Violations -> 422 with `fields`.

---

## 10. Endpoints

Legend: **Access** = credential level; **Write** = token needs write scope; **Role** = minimum effective role (min of member role and token cap); non-members always 404. "S" means session only (token gets 403 `session_required`).

### Auth

| Endpoint | Access | Behavior |
|---|---|---|
| `GET /auth/config` | Public | 200 `{"signup_enabled": bool}` and nothing else (reflects `ALLOW_SIGNUP`). Not rate limited, no Origin check (GET). |
| `POST /auth/signup` | Public | Body `{email, display_name, password}`. If `ALLOW_SIGNUP=false` the route behaves as nonexistent: the standard 404 `not_found` JSON, returned before body decode, rate limiting and validation (decided; no `signup_disabled` code). Otherwise: decode, rate limit check, validate all fields together (422 with `fields.email`, `fields.display_name`, `fields.password`), hash, create user (duplicate email 409 `email_taken`), create session, set cookie. 201 `{user}` (`auth_user`). Rate limited. |
| `POST /auth/login` | Public | Body `{email, password}`. Wrong email or password -> 401 `invalid_credentials`, message "Invalid email or password". Success sets a new session cookie (rotating any presented one), 200 `{user}` (`auth_user`). Missing or empty `email` or `password` is still 401 `invalid_credentials` (not 422), so the response never differs by input shape. Rate limited. |
| `POST /auth/logout` | S | Deletes the session, clears cookie, 204. |
| `GET /auth/me` | session or token | 200 `{"user": auth_user, "auth": {"method": "session"\|"token", "token": {id, name, scope, project_id: string\|null, project_key: string\|null}}}`; `auth.token` is **omitted when** the method is `session`. |
| `PATCH /auth/me` | S | Body `{display_name}` (trimmed, 1 to 100, same rule as signup; unknown fields 400, e.g. `email` cannot be changed). Calls `Users.UpdateProfile`. 200 `{user}` (`auth_user`). |
| `POST /auth/me/password` | S | Body `{current_password, new_password}`. Order: rate limit, decode, verify `current_password` (via `Users.PasswordHash` and argon2 verify), then validate `new_password` (same rules as signup: 10 to 200 characters) and hash with argon2id, then `Users.SetPassword(userID, newHash, currentSessionHash)`. A new password equal to the current one is allowed. Wrong current password -> **422** `validation_failed` with `fields.current_password: "Incorrect password"` (chosen over 401 so the UI never treats it as an expired session and redirects to login). Invalid new password -> 422 `fields.new_password`. On success: update the hash, delete all of the user's **other** sessions, keep the current one, 204. Rate limited (section 7). A token calling it gets 403 `session_required`. |

### Tokens (all S, none need Write because tokens cannot call them)

| Endpoint | Behavior |
|---|---|
| `GET /tokens` | Caller's own tokens (revoked ones included, with `revoked_at`), newest first, never the secret. 200 `{items, next_cursor: null}`. |
| `POST /tokens` | Body `{name, scope, project_id?}` (`project_id` is a key or ULID; `null` or absent = all projects). 201 `{token, secret}`. `project_id` the caller is not a member of -> 404 (same as any hidden project). Invalid `scope` or `name` -> 422. |
| `DELETE /tokens/{id}` | Revoke. Someone else's or unknown id -> 404. Already revoked -> 204. |

### Projects

| Endpoint | Access / Role | Behavior |
|---|---|---|
| `GET /projects` | Authed, member | Projects the caller is in (limited token: just its project). `?archived=true` includes archived. Whole list in an envelope (`next_cursor` null), ordered by name then key. Each item is `project` (with `role`, no counts). |
| `POST /projects` | Authed, Write | Body `{key, name, description?}`. Key is uppercased then must match `^[A-Z]{2,6}$`; name 1 to 100; description <= 2000. Creator becomes owner. 201 `project`. Duplicate key 409 `key_taken`. Limited token -> 403 `forbidden`. |
| `GET /projects/{id}` | member | 200 `project` with `counts`. `{id}` is the key or ULID here and on every `/projects/{id}/...` route. |
| `PATCH /projects/{id}` | owner, Write | Partial body `{name?, description?, archived?}` (`archived: true` sets `archived_at`, `false` clears it; `name` and `description` cannot be `null`, -> 422). An empty object `{}` is a 200 no-op. `key` is immutable (unknown field -> 400). Works on an archived project (that is how it is unarchived). 200 `project`. |
| `DELETE /projects/{id}` | owner, S | Hard delete with everything in it (cascade). 204. |

### Members

| Endpoint | Access / Role | Behavior |
|---|---|---|
| `GET /projects/{id}/members` | member | 200 `{items: [member], next_cursor: null}`, owners first, then by display name. |
| `POST /projects/{id}/members` | owner, S | Body `{email, role}`. Role in owner, editor, viewer. Unknown email -> 422 `fields.email: "No account with this email"` (owners can learn that an account exists; inherent to adding by email). Already a member -> 409 `already_member`. 201 `member`. |
| `PATCH /projects/{id}/members/{userId}` | owner, S | Body `{role}`. Target not a member -> 404. Demoting the last owner -> 409 `last_owner`. 200 `member`. |
| `DELETE /projects/{id}/members/{userId}` | S; owner, or `{userId}` is the caller | Owner removes anyone, any member can remove themselves (leave). A non-owner removing another member -> 403 `forbidden`. Removing the last owner (including self-leave) -> 409 `last_owner`. 204. Assignments of the removed user in that project are cleared by the service (expected from phase 1). |

### Labels

| Endpoint | Access / Role | Behavior |
|---|---|---|
| `GET /projects/{id}/labels` | member | 200 `{items: [label], next_cursor: null}` ordered by name. |
| `POST /projects/{id}/labels` | editor, Write | Body `{name, color?}`. Name trimmed, 1 to 50, unique per project case-insensitively (409 `label_exists`). Colour from the fixed palette `gray, red, orange, amber, green, teal, blue, indigo, purple, pink` (owned by Phase 1 `Validate()`), default `gray`. 201 `label`. |
| `PATCH /labels/{id}` | editor, Write | Partial `{name?, color?}`. 200 `label`. |
| `DELETE /labels/{id}` | editor, Write | Removed from tickets by cascade. 204. |

### Tickets

| Endpoint | Access / Role | Behavior |
|---|---|---|
| `GET /projects/{id}/tickets` | member | Filters: `status` (one of the four), `priority` (one of four), `assignee` (user id, `me` = the principal's owning user, or `none` = unassigned), `label` (label id), `q` (case-insensitive substring of title or description, at most 200 characters else 422; `%`, `_` and `\` are escaped by the service), `limit`, `cursor`. Each filter may be given once (a repeated parameter is 422 for that field). Invalid enum values -> 422 `fields.<name>`. An `assignee` or `label` id that matches nothing, or belongs to another project, is not an error: the result is just empty. Excludes soft-deleted. Paginated, order in section 6; items are the `ticket` list shape (no `description`). |
| `POST /projects/{id}/tickets` | editor, Write | Body `{title, description?, status?, priority?, due_date?, assignee?, labels?}`. Title 1 to 200 (trimmed), description <= 20,000 chars, `due_date` valid `YYYY-MM-DD`, defaults `todo` and `medium`. Placed at the bottom of its column. Activity `created` with the caller's actor. 201 `ticket`. |
| `GET /tickets/{id}` | member | One `ticket` (with `description`, labels, assignee, `comment_count`). |
| `PATCH /tickets/{id}` | editor, Write | Partial `{title?, description?, priority?, due_date?, assignee?, labels?}`; `null` clears `due_date` and `assignee`; `labels` replaces the whole set. `status` and `position` are not patchable (400, "use /move"). An empty object or no effective change is a 200 no-op with no activity. Writes one `updated`, `assigned` or `labeled` activity row (Phase 1 rule). 200 full `ticket`. |
| `POST /tickets/{id}/move` | editor, Write | Body `{status, before?, after?, place?}` (`status` required). At most one of `before`, `after`, `place` (`"top"`/`"bottom"`); more than one or an invalid `place` -> 422 (`fields.place`); none = bottom. `before`/`after` are ticket ids or references, must be in the same project and currently in the target `status` and not the ticket itself (else 422 `fields.before` or `fields.after`, from the service's `anchor_invalid`). A move to the position it already has is a 200 no-op. Writes `moved` activity. 200 full `ticket` plus `renumbered` (section 6). |
| `DELETE /tickets/{id}` | editor, Write | Soft delete, `deleted` activity. 204. Soft-deleted tickets are 404 afterwards. |
| `GET /tickets/{id}/activity` | member | Paginated, newest first. |

### Comments

| Endpoint | Access / Role | Behavior |
|---|---|---|
| `GET /tickets/{id}/comments` | member | Paginated, oldest first. |
| `POST /tickets/{id}/comments` | editor, Write | Body `{body}`, 1 to 20,000 chars. Author is the caller's actor (user, or the token). 201 `comment`. |
| `PATCH /comments/{id}` | its author, Write | Body `{body}`, 1 to 20,000 chars. "Author" means the exact actor (type and id): a user cannot edit a comment written by a token, and a token (the same token id) only its own. The author must still be an editor or owner of the project (a demoted viewer or removed member cannot edit; removed member is 404) and the actor must have write scope (read token -> 403 `insufficient_scope`). Non-author who is a member -> 403 `forbidden`. Archived project -> 409 `project_archived`. Sets `edited_at`. 200 `comment`. |
| `DELETE /comments/{id}` | its author (exact actor, still editor or owner) or an owner, Write | Soft delete. A demoted or removed author cannot delete (403 / 404). Archived project -> 409. 204. |

### Reserved

`GET /api/v1/projects/{id}/events` is not registered in this phase (404 like any unknown route).

---

## 11. Security headers and caps

- Every response (success, error, 404, 405, static fallback, `/mcp`, SSE): `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'` (Phase 6 audits this set against the real bundle; header tests assert the final set), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`. `Cache-Control: no-store` on `/api/`, `/mcp` and `/healthz`. HSTS (`Strict-Transport-Security: max-age=31536000`, no `includeSubDomains`) is set by the app **only when** `BASE_URL` is `https://`; the proxy normally owns it. Phase 6 audits the headers; header tests assert the final set.
- Caps: body 1 MiB; and the field limits below, enforced in service `Validate()` (character counts, not bytes).

| Field | Limit |
|---|---|
| email | 254 |
| display name | 1 to 100 |
| password | 10 to 200 |
| project key | `^[A-Z]{2,6}$` |
| project name / description | 1 to 100 / 2,000 |
| ticket title / description | 1 to 200 / 20,000 |
| comment body | 1 to 20,000 |
| label name | 1 to 50 |
| token name | 1 to 100 |
| labels per ticket | 50 |

- Server timeouts: `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `IdleTimeout` 120 s, **no `WriteTimeout`** (phase 3 streams must not be cut). `serve` listens on `:PORT` (all interfaces) so the container healthcheck on 127.0.0.1 works.
- Secrets: never log `Authorization`, `Cookie`, passwords or tokens; compare hashes in constant time; never echo a password in any error.

---

## 12. CLI

`pabrika` dispatches on the first argument; usage error -> exit code 2, runtime error -> 1, success -> 0, messages to stderr.

| Command | Behavior |
|---|---|
| `pabrika serve` | Loads config, opens the store, runs migrations (phase 1), builds the server, listens on `PORT`, starts the session purge job, shuts down gracefully on SIGINT/SIGTERM (section 12.1, 8 s deadline). Non-`/api` paths return a plain 404 until phase 6 mounts the UI. |
| `pabrika user create --email E --name N [--password-stdin]` | Creates a user regardless of `ALLOW_SIGNUP`, same validation as signup (display name 1 to 100). Usage example for the image: `printf '%s' "$PW" \| docker exec -i pabrika /pabrika user create --email E --name N --password-stdin`. Password is read from one line on stdin with `--password-stdin`, otherwise prompted without echo on a TTY (`golang.org/x/term`); with no TTY and no flag it fails. Never accepted as an argument. Duplicate email -> exit 1 with a clear message. |
| `pabrika user reset-password [--password-stdin] EMAIL` | Same password input rules. Sets a new hash, **revokes (deletes) all of the user's sessions**. API tokens are **not** revoked (decided; revoke them manually if an account is compromised). Unknown email -> exit 1. |
| `pabrika healthcheck` | `GET http://127.0.0.1:$PORT/healthz` with a 3 s timeout (`PORT` default 8080; reads only `PORT`, so an unrelated bad env var such as `BASE_URL` cannot make the check fail); exit 0 on 200, else 1. No output on success. Must work in an image with no shell or curl. |

**Flag parsing:** the CLI uses Go's stdlib `flag` package per subcommand, which stops at the first positional argument, so **flags must precede positionals**: `pabrika user reset-password --password-stdin EMAIL` is the documented form; `EMAIL --password-stdin` is a usage error (exit 2) because manual parsing is not implemented.

**Fresh volume:** `user create` and `user reset-password` open the store and **run migrations first** (the same migrate step `serve` uses), so they work on a fresh volume before `serve` has ever run. They never take the password as an argument.

User commands open the same SQLite file the running server uses (WAL and busy timeout make this safe) and call the same service and auth code; they never go through HTTP.

### 12.1 Graceful shutdown

The deadline is **8 s** (not 10 s) so that `docker stop` (10 s default grace) exits 0 instead of being killed with 137. On SIGINT/SIGTERM `serve`, in order: (1) stops accepting new connections and stops the purge job; (2) calls the hub shutdown hook that Phase 3 provides (`hub.Shutdown()`, ends open SSE streams; new streams get 503 `unavailable`) **first**, because `srv.Shutdown` does not cancel open streams; Phase 2 wires it as an optional hook (no-op until Phase 3 supplies it); (3) `srv.Shutdown(ctx)` with an 8 s deadline; (4) if that returns an error (deadline exceeded), falls back to `srv.Close()`; (5) closes the store (WAL checkpoint), then exits 0. Phase 6 verifies it end to end with a real container.

---

## 13. Test matrix (`httptest`, in-memory SQLite, injectable clock)

Shared test harness: build the real handler over a fresh in-memory store, helpers to create users, log in (cookie jar), create tokens, and call with an `Origin: BASE_URL` header. Tests are table-driven off `Server.Routes()` where possible.

**Auth flows**
- Signup success sets cookie with the right attributes (`HttpOnly`, `SameSite=Lax`, `Secure` toggled by `COOKIE_SECURE`); `/auth/me` works with it; DB holds only the SHA-256 of the cookie value.
- Signup: duplicate email (case-insensitive) 409; 9-character password 422; bad email 422; `ALLOW_SIGNUP=false` -> 404 `not_found` (body identical to an unknown route, even with an invalid body, and not rate limited); email stored lowercased.
- `GET /auth/config`: returns exactly `{"signup_enabled": true}` or `false` matching `ALLOW_SIGNUP`, needs no credentials, contains no other keys.
- `PATCH /auth/me`: updates `display_name` (trim, 1 to 100; 0 or 101 characters 422; `email` field 400); `/auth/me` shows the new name; token gets 403 `session_required`; anonymous 401.
- `POST /auth/me/password`: success 204, new password logs in and old fails; wrong `current_password` 422 `fields.current_password` and nothing changes; short `new_password` 422 (`fields.new_password`); same argon2id PHC params as signup; the user's other sessions (second cookie jar) are 401 afterwards while the current cookie still works; another user's sessions untouched; sixth attempt within a minute for the same (user, IP) 429; token 403 `session_required`; anonymous 401.
- `TRUST_PROXY`: default false ignores `X-Forwarded-For` (spoofing it does not evade the limit); true uses the first hop for the limiter and the log (a multi-hop list uses only the leftmost); missing or invalid header falls back to `RemoteAddr`; IPv6 clients in the same /64 share a bucket; the same IP over IPv4 and IPv4-mapped IPv6 shares one; config parsing default false (Phase 1 test, re-asserted).
- Login: wrong password and unknown email return identical status, code and message; success issues a new cookie each time; logout deletes the row and later use is 401; cookie of a deleted session 401 and cleared.
- Session expiry: expired session 401; sliding works (advance clock more than 1 hour, expiry and cookie renew; within the hour no write); purge removes expired rows.
- Rate limit: sixth login or signup attempt within a minute for the same (IP, email) -> 429 with `Retry-After`; different email not limited; window resets with the clock; bucket map stays bounded.
- Argon2: PHC format and params; verify round trip; wrong password fails; unknown-email login still does a verification (dummy hash path covered).
- Bearer: valid token authenticates; revoked 401; unknown 401; malformed header 401; bearer present and invalid does not fall back to a valid cookie; both present and valid uses the token (`/auth/me` shows `method: token`).
- Tokens: `POST /tokens` returns secret once (`pb_` + 64 hex), list never contains it, prefix is 8 characters; `last_used_at` set and throttled to once per minute; revoke then use 401; revoke another user's token 404; `project_id` of a non-member project 404; `project_id` accepted as key or ULID and returned as `project: {id, key}`; the 101st active token 422 (revoked ones do not count); revoke twice 204; revoked tokens stay in the list; a token's `/auth/me` shows `project_id` and `project_key`.

- Session fixation and rotation: a login or signup sent with a (valid or attacker-chosen) `pb_session` cookie never reuses its value (new cookie differs, DB holds only the new hash); a valid presented session row is deleted by the login. Login with empty email or password is 401 `invalid_credentials`. Two concurrent signups with the same email give one 201 and one 409.
- Bearer format: `Bearer pb_` plus anything not 64 lowercase hex, a `Basic` scheme, and a 10 KB header all give 401 without a DB lookup (count queries) and do not touch the session cookie; `bearer` in lower case works.
- Request id and log: every response carries `X-Request-Id`; log lines contain path without the query string (a request to `/api/v1/projects?x=pb_secret` logs no `pb_secret`).

**CSRF / Origin**
- POST, PATCH, DELETE with wrong `Origin` -> 403 `origin_mismatch`; `Origin: null` 403; same-origin passes; `https://host` equals `https://host:443` (default-port normalisation) and a different port or scheme fails; cookie request with no `Origin` 403; bearer request with no `Origin` passes; GET with foreign `Origin` passes; a `MountRaw("/mcp")` handler: foreign `Origin` 403 `origin_mismatch` on every method, matching or absent `Origin` passes, cookies ignored (cookie-only request is 401); non-JSON `Content-Type` 415 `unsupported_media_type`; a body with no `Content-Type` 415; trailing data after the JSON object 400; unknown field 400; no CORS headers on any response and `OPTIONS` gets the JSON 405.

**Scope x role matrix** (table-driven over every non-public route)

Principals: anonymous; session of a non-member; session as viewer, editor, owner; token (write) as viewer, editor, owner; token (read) as editor and owner; project-limited write token for this project and for another project.

| Endpoint class | anonymous | non-member (any credential) | viewer session | editor session | owner session | read token (editor or owner) | write token as viewer | write token as editor/owner | limited token, other project |
|---|---|---|---|---|---|---|---|---|---|
| member read | 401 | 404 | ok | ok | ok | ok | ok | ok | 404 |
| editor write | 401 | 404 (session) / 403 `insufficient_scope` (read token) | 403 | ok | ok | 403 `insufficient_scope` | 403 | ok | 404 |
| owner write (`PATCH` project) | 401 | 404 | 403 | 403 | ok | 403 `insufficient_scope` | 403 | ok if owner else 403 | 404 |
| session-only | 401 | 404 (session) | per route | per route | per route | 403 `session_required` | 403 `session_required` | 403 `session_required` | 403 `session_required` |

- Session-only endpoints (each tested with a token, expecting 403 `session_required` before any 404 or validation): `/auth/logout`, `PATCH /auth/me`, `POST /auth/me/password`, `GET/POST/DELETE /tokens`, `DELETE /projects/{id}`, member add, role change, remove.
- Token role change: demote a user, existing write token loses write effect immediately; read token with owner role cannot write.
- Limited token: `GET /projects` returns only its project; `POST /projects` 403 `forbidden`; the other project (by key and by ULID), its tickets, labels and comments 404 (never 403); token cascades away when its project is deleted. Scope and limit are also asserted at the service layer directly (service tests with a read `Actor`, a limited `Actor`), independent of HTTP.
- Order of checks: read token POST to a non-member project -> 403 `insufficient_scope`; session non-member POST -> 404; validation errors never shown to non-members or viewers (404 / 403 first).

**Non-members get 404, never 403**
- For every project-scoped route (project, members, labels, tickets, activity, comments, label by id, comment by id, ticket by ULID and by reference), a non-member gets 404 with a body identical to the "does not exist" response for a random valid ULID. A removed member immediately gets 404.

**Behavior and shapes**
- **Exact shapes (golden key sets):** for each shape in section 6 (`user`, `auth_user`, `project`, `project_detail`, `member`, `label`, `ticket` list item and full, `comment` by user and by token, `activity`, `token`, token creation response, list envelope, error, `/auth/me` for session and token, move response with `renumbered`) the test decodes into `map[string]any` and asserts the exact key set (so adding or renaming a field fails), the JSON types, that nullable keys are present as `null`, that `description` is absent from list items only, that `owner_name` is absent for user authors, and that `counts` has all four keys. `ref` is `WEB-12`. `project.role` is `viewer` for a read token whose owner is an owner.
- Ticket list `assignee` is the member object (id, display_name, email) and the list issues one members query regardless of page size; `comment_count` is correct in list and detail; create, PATCH, move and GET return the same full shape.
- Routing: unknown `/api/v1/x` and `/api/v2/x` 404 JSON; `GET` on a POST-only route 405 JSON with `Allow: POST`; `PUT` on a known path 405; the fallback handler (set with `SetFallback`) receives non-API paths and is not reached for `/api/`, `/mcp` or `/healthz`; registering the mux does not panic (no pattern conflicts).
- Project: create (key uppercased, `key_taken` 409, bad key 422), `GET /projects/web` and `GET /projects/{ulid}` return the same project (and likewise for a nested route such as `/projects/WEB/tickets`), `role` and `counts` correct, archive and unarchive by write token, delete cascades, archived project rejects ticket writes with 409.
- Members: add by email (unknown email 422, duplicate 409), role change, last-owner protection on demote, remove and self-leave (including last owner 409), non-owner removing another 403, assigned tickets of a removed member are unassigned.
- Labels: duplicate name case-insensitive 409, bad colour 422, each of the ten palette colours (gray, red, orange, amber, green, teal, blue, indigo, purple, pink) accepted, delete removes from tickets.
- Tickets: create defaults and bottom placement; reference lookup (`web-12` and `WEB-12`) and ULID both work; filters (`status`, `priority`, `assignee` including `me` and `none`, `label`, `q` with `%`); pagination (limit default 50, max clamp 200, cursor round trip, stable after an insert between pages, invalid cursor 400, no duplicate or missing items); PATCH partial semantics (absent vs null vs value), `status` in PATCH 400, assignee non-member 422, label from another project 422; move variants (`top`, `bottom`, `before`, `after`, default bottom, two placements 422, `before` ticket in a different column 422, cross-project 422); soft delete 404 afterwards and gone from lists and counts.
- Comments: create as user and as token (author shape with `bot: true` and `owner_name`), viewer cannot comment (403), edit only by exact actor (user edits own comment; same token edits its own; user cannot edit a token's comment; another token cannot; owner cannot edit another's), edit refused after the author is demoted to viewer, with a read token (403 `insufficient_scope`) or in an archived project (409), `edited_at` set, edit body 20,001 chars 422, delete by author (still editor) or owner, demoted author cannot delete own comment (403), soft-deleted hidden, comments list paginated with a cursor, `comment_count` updates, activity rows record the token as actor for token writes.
- Error shape: every error status above returns `{"error":{"code","message"}}` with the documented code; unknown route 404 JSON; wrong method 405 JSON with `Allow`; 500 from a forced panic is generic and logged; 429 carries `Retry-After`; body over 1 MiB 400 `body_too_large`; no `signup_disabled` code anywhere; field limits (title 201 chars 422, description 20,001 422, comment 20,001 422).
- Headers: CSP (exact string from section 11), `nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options`, `Permissions-Policy` on success, error, 404, 405 and fallback responses; `no-store` on `/api/`, `/mcp` and `/healthz`; HSTS absent for an `http://` `BASE_URL`, present for an `https://` `BASE_URL`.
- Mount points: a route added with `Mount(..., SessionOnly, ...)` appears in `Routes()` and enforces 401/403 guards and the Origin check; a `MountRaw` handler gets global headers, body cap and (for `/mcp`) the Origin rule but no route guard; a `Mount("/", Public, h)` catch-all coexists with `/api/` and every method pattern without a registration panic and gets headers, request id and body cap; `Sessions.Validate` returns `ErrInvalidCredentials` after logout and for expired rows and nil for a live row without extending it.
- Logging: a request with `Authorization` and `Cookie` headers produces no log line containing either value.
- `/healthz` 200; DB failure 503.

**CLI tests** (call the command functions with an in-memory or temp-file DB)
- `user create` and `user reset-password` against a brand-new empty DB file run migrations first and succeed (no prior `serve`); `reset-password EMAIL --password-stdin` (flag after positional) is a usage error exit 2, `reset-password --password-stdin EMAIL` works.
- **Graceful shutdown:** a test server with a hung in-flight request and a fake hub hook that records its call shuts down in the documented order (hook first, then `Shutdown`, then store close) within the 8 s deadline; when `srv.Shutdown` exceeds a short test deadline, `srv.Close()` is called and the run still returns nil; the store is closed afterwards (WAL checkpointed, a second open sees all data).
- `user create` creates a loginable user even with `ALLOW_SIGNUP=false`; short password and duplicate email fail with non-zero exit; password never read from args.
- `user reset-password` changes the hash, old password fails, new works, existing sessions die (revoked), API tokens still work (not revoked); unknown email non-zero.
- `healthcheck` returns 0 against a running test server and non-zero against a closed port, and still returns 0 when `BASE_URL` is set to garbage (only `PORT` is read).

**Exit criteria** (from phases.md): all of the above passing with `go test ./...` and no CGO; non-members get 404 on every project-scoped route; error shape asserted for all documented statuses.

---

## 14. What later phases rely on

- **Phase 3 (events):** `Server.Mount(pattern, Access, handler)` to register `GET /api/v1/projects/{id}/events` as `SessionOnly` after the existing guards; `Projects.Resolve(ctx, principal.Actor(), ref)` as the membership check for streams (404 for non-members on open and on each tick; no HTTP-layer helper); `Principal.Session.TokenHash` plus the read-only `auth.Sessions.Validate(ctx, tokenHash)` (no session slide) for the keepalive session re-check (Phase 2 provides both, nothing to extract; Phase 3 should use these names rather than its own `SessionValid`); the response writer wrapper supports `Flush`/`Unwrap`; no `WriteTimeout`; event actor values come from `Principal.Actor()`. Phase 2 does not own the hub; `serve` passes whatever `Publisher` Phase 3 supplies into `service.Deps`. Publishing happens inside the service, so nothing in the handlers changes. Removing a member goes through the service (so phase 3 can close that user's streams there).
- **Events (Phase 3/5):** a display-name change (`PATCH /auth/me`) emits no event. Removing a member emits only `member.changed`; the UI reloads tickets on it.
- **Phase 4 (MCP):** `auth.Resolver.ResolveBearer(r)` (bearer only, never cookies) for `/mcp`, mounted with `Server.MountRaw("/mcp", h)` (outermost middleware including the `/mcp` Origin rule; no route guard) and failures written with `httpapi.WriteError` (401 plus `WWW-Authenticate: Bearer`); `Principal.Token` carries `ID, Name, Scope, ProjectID, ProjectKey` (empty strings when not limited), so the "limited to project WEB" message needs no extra lookup; `Principal.Actor()` for service calls; Phase 2 request DTOs live in `httpapi` and are not shared with MCP (MCP shares the service constants, enums and `Validate()`); scope and project limit are enforced inside the service via `Actor.Scope` and `Actor.ProjectID`, so MCP cannot skip them (MCP only adds friendlier messages, e.g. "limited to project X", and tool filtering from `Principal.Token.Scope` and project); `/mcp` ignores cookies, needs a bearer token, and applies the Origin rule (present must match `BASE_URL`, absent allowed, all methods); service `Validate()` input structs, enums and caps (comment body 20,000, label palette) are the schemas source; comments and activity from MCP use `Actor{api_token, tokenID}` exactly as REST tokens do; the exact-author comment edit rule (`PATCH /comments/{id}`) is a service method that Phase 4's `update_comment` tool calls unchanged, and comment ids are in every comment response.
- **Phase 5 (UI):** the error shape and codes (401 redirect, 403/404/409/422 `fields` for form errors; wrong current password is a 422 field error, not 401), list envelope and cursors, resource shapes in section 6, `/auth/me` for the Pinia store, `GET /auth/config` (`signup_enabled`) to decide whether to show "Create account" and to redirect `/signup` to `/login`, `PATCH /auth/me` and `POST /auth/me/password` for the account screen, project paths by key (`/projects/WEB/...`) so the UI needs no key-to-id lookup, cookie attributes, `{items}` for labels and members, ticket `ref` (not `reference`), `project_key`, `assignee` objects and `comment_count` in list items, `counts` on `GET /projects/{id}`, move response `renumbered`, `member.user.id` as the assignee id source, and the exact shape table in section 6 as the source for `types.ts` (this resolves Phase 5's open item; there is no `owner_name` on user authors and event actors are `{type, id}` only). In Vite dev the proxy must rewrite the `Origin` header to `BASE_URL` (or set `BASE_URL` to `http://localhost:5173`), otherwise the Origin check rejects writes.
- **Phase 6 (ship):** `Server.SetFallback(h)` takes the static handler for non-`/api/`, non-`/mcp`, non-`/healthz` paths (registered as the method-less Public catch-all via `Server.Mount("/", Public, h)` or `SetFallback`; the SPA handler itself returns 405 for non-GET/HEAD and JSON 404 for API-like paths); `healthcheck` and `/healthz` exist; the header set is Phase 2's final set (`Referrer-Policy: same-origin`, CSP `base-uri 'none'`); Phase 6 audits it and header tests assert it (CSP may still need tuning for the built bundle); the request log omits query strings and carries a request id; `COOKIE_SECURE`, `BASE_URL` and `TRUST_PROXY` semantics are final (Phase 6 documents `TRUST_PROXY=true` behind Caddy or similar).

---

## 15. Decisions applied

Confirmed by the user (see "Decisions" in [phases.md](phases.md)); they override main-spec where it differs.

- Signup off: `POST /auth/signup` is 404 `not_found`; no `signup_disabled` code; public `GET /auth/config` returns only `{"signup_enabled": bool}`.
- Account self-service: session-only `PATCH /auth/me` and `POST /auth/me/password` (wrong current password is 422 `fields.current_password`; other sessions deleted, current kept).
- Project paths accept the key or the ULID.
- `TRUST_PROXY` (default false) selects the first `X-Forwarded-For` hop for the limiter and logs.
- `Actor` carries `Scope` and `ProjectID`; the service layer enforces them, HTTP only builds the `Actor`.
- Comment edit by the exact author actor who is still editor or owner with write scope; comment body cap 20,000.
- Label palette: gray, red, orange, amber, green, teal, blue, indigo, purple, pink.
- Extra statuses 405, 415, 429, 500, 503 `unavailable`; oversized body is 400 `body_too_large`; non-JSON content type is 415.
- Password reset CLI revokes sessions, not API tokens; password change revokes other sessions only.
- Limited token + other project is 404; limited token creating a project is 403.
- Archived projects read-only (409 `project_archived`); origin-absent bearer allowed, cookie rejected; comment authorship is exact actor.
- Review fixes: display name cap is 1 to 100 (matches Phase 1 and 6); ticket JSON key is `ref`; projects are returned whole; cursors are service-owned; REST DTOs live in `httpapi`; CSP, Referrer-Policy, Permissions-Policy and conditional HSTS match Phase 6; max 100 active tokens per user; login rotates a presented session.
- Accepted defaults: caps (display name, password max, project name/description, label name, token name, labels per ticket), ticket list omits `description`, members/labels/tokens returned whole, throttled session and `last_used_at` writes, `GET /healthz` built here, email-existence disclosure on signup and add-member accepted.

- Cross-spec reconciliation: Actor uses typed `service.ActorType`/`service.Scope` via `UserActor`/`TokenActor` (empty session scope = write).
- One `*service.Error{Kind, Code, Message, Fields}`; no `ErrValidation{fields}`; `email_taken` via `service.NewError(KindConflict, ...)`; `not_author` is `forbidden` on the wire; `session_required` can come from the service.
- Service owns and parses cursors; HTTP passes the string through and clamps `limit`; projects, members, labels and tokens returned whole with `next_cursor: null`.
- No exported membership helper; Phase 3 uses `Projects.Resolve` and read-only `Sessions.Validate` with `Principal.Session.TokenHash` (no slide).
- 503 `unavailable` added; ticket key stays `ref`; display name 1..100; 100-active-token cap kept.
- CLI: flags precede positionals (`user reset-password --password-stdin EMAIL`); `user create` and `reset-password` migrate first and never take the password as an argument.
- `/mcp`: cookies ignored, bearer required, `Origin` must match `BASE_URL` if present (absent allowed), all methods; no exemption.
- Recover, request id/log, security headers and body cap are outermost over `/api`, `/mcp`, `/healthz` and the SPA fallback; `Mount` allows a method-less Public `/` catch-all; `Referrer-Policy: same-origin`, CSP `base-uri 'none'`, HSTS only for an https `BASE_URL`.
- `TRUST_PROXY`: the proxy must overwrite `X-Forwarded-For` (first hop is trusted).
- Graceful shutdown: 8 s deadline, hub hook first, `srv.Shutdown`, `srv.Close()` fallback, then store close (WAL checkpoint); tested.
- Activity `changes` shape is Phase 1's; display-name change emits no event; member removal emits only `member.changed`.

**Unresolved:** none. Cross-file names and values were reconciled in the final consistency pass.
