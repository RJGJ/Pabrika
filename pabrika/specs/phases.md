# Pabrika: Project Phases

High-level phases for building Pabrika. The source of truth for *what* we are building is [main-spec.md](main-spec.md). This file only says *in what order* and *what each phase must deliver*.

## How agents should use this file

1. Pick the next phase whose status is `todo` and whose dependencies are `done`.
2. Read [main-spec.md](main-spec.md), focusing on the sections listed under "Spec sections" for that phase.
3. Write a detailed spec for that phase to `specs/phase-<N>-<slug>.md` (for example `phase-1-foundation.md`). It should break the phase into concrete tasks, files and interfaces, and list tests.
4. Write the implementation plan to `plans/phase-<N>-<slug>.md`.
5. Do not widen scope. Anything under a phase's "Out of scope" belongs to a later phase; anything not in main-spec.md needs the user's decision.
6. When the phase's exit criteria are met, update its status below.

If a phase spec conflicts with main-spec.md, main-spec.md wins; flag the conflict instead of silently deviating.

## Status

| # | Phase | Depends on | Status |
|---|---|---|---|
| 1 | Foundation | none | done |
| 2 | Auth, sharing and REST | 1 | done |
| 3 | Live updates | 2 | done |
| 4 | MCP server | 2 (3 recommended) | done (manual Claude Code check not run) |
| 5 | Web UI | 2, 3 | done (full real-server browser checklist partial) |
| 6 | Ship | 1 to 5 | done except Docker steps (not run); see `pabrika/docs/FINDINGS.md` |

Phase 4 can start once phase 2 is done, so agents can use the board before the UI is polished. Wire phase 3 events into MCP writes as soon as both exist.

## Decisions (resolved open questions)

These were confirmed by the user and override anything in `main-spec.md` or a phase spec that conflicts. Phase specs must reflect them.

**Changes to the product**
- **Signup off means unavailable.** When `ALLOW_SIGNUP=false`, `POST /auth/signup` behaves as if it does not exist (404 `not_found`, not 403), and the `/signup` page is not reachable (the UI redirects to `/login` and hides the "Create account" link). A public `GET /api/v1/auth/config` returns `{"signup_enabled": bool}` so the UI knows; it exposes nothing else. The CLI `user create` still works.
- **Account self-service in v1 (API and UI).** Session-only endpoints: `PATCH /auth/me` (change `display_name`) and `POST /auth/me/password` (`current_password`, `new_password`; verifies the current password, same rules and argon2id as signup, rate limited, deletes the user's other sessions and keeps the current one). The account screen gets a profile form and a change-password form. Email change is out of scope, and API tokens cannot call these endpoints.
- **Agents can edit comments.** Editing a comment is allowed for its exact author (user or the token that wrote it) who is still an editor or owner with write scope. Add an MCP tool `update_comment` (`comment` id, `body`; write scope) so an agent can edit comments it wrote; the MCP tool count becomes 14. `add_comment` results include the comment id. A human cannot edit an agent's comment (owners can still delete it).

**Design**
- Project paths accept the project **key or the ULID** (`/projects/WEB` and `/projects/{ulid}`); the UI can use keys directly.
- Add `TRUST_PROXY` (default `false`): when true, the rate limiter and logs use the first `X-Forwarded-For` hop.
- Token scope and project limit are enforced in the **service layer** through `Actor` (`Scope`, `ProjectID`), so no transport can skip them.
- Archived projects are read-only for ticket, label and comment writes (409 `project_archived`).
- Password reset (CLI) revokes sessions but not API tokens. Password change (new endpoint) revokes other sessions only.
- A project-limited token asking for another project: REST returns 404; MCP returns an actionable "limited to project X" message. Project-limited tokens cannot create projects (403).
- Extra statuses allowed: 405, 415 (non-JSON), 429, 500 and 503 `unavailable` (new live-update stream during shutdown); oversized body is 400 `body_too_large`.

**Live updates**
- Keep `renumbered` and the extra id fields on events. Use named SSE events (`event: <type>`).
- Re-validate the session on every 25-second keepalive so logout or expiry closes the stream.
- No event on project delete, no final `close` event, label delete emits only `label.changed`.

**MCP**
- Share enums, caps and `Validate()` with REST; keep key/email/name resolution inside `mcpserver`.
- `get_ticket` returns at most 50 comments. Resources are optional and built last. If the SDK has no per-request server, register all tools and filter `tools/list` plus enforce on call.

**Security and deployment**
- CSP allows inline styles (`style-src 'unsafe-inline'`), scripts stay strict. HSTS is set by the proxy; the app sets it only when `BASE_URL` is HTTPS. Extra hardening headers and server timeouts are in.
- Dev with Vite: `BASE_URL=http://localhost:5173`. Markdown uses `markdown-it` (HTML off) plus DOMPurify; images are dropped in v1.
- Go module path: `github.com/RJGJ/Pabrika`. Add a Makefile and a `version` subcommand. License: MIT.

**UI**
- Dragging is disabled while filters are active; filters live in the URL query. Non-owners see project settings read-only (editors still manage labels); any member can leave a project. Ticket panel has a Status dropdown as a keyboard alternative to dragging. A brief dark-mode flash on load is accepted.

**Limits and values**
- Comment body 20,000 characters (everywhere, including MCP). Project name 100, project description 2,000, label name 50.
- Label palette: `gray, red, orange, amber, green, teal, blue, indigo, purple, pink`.

**Small defaults accepted as written in the specs:** ticket `Update` cannot change status; project key immutable and uppercased; unknown email on add member is 422; removing a member unassigns their tickets; activity text truncated to 200 chars; position gap 1024; ticket list omits `description`; members, labels and tokens are returned whole; `last_used_at` writes throttled; hub buffer 32, no per-user stream cap; `/healthz` built in Phase 2; embed in `web/embed.go`; Go 1.23 and Bun 1 images; graceful shutdown (8 s deadline, then forced close and WAL checkpoint, so `docker stop` exits 0) is built in Phase 2 with the hub shutdown in Phase 3, and verified in Phase 6; no Kubernetes manifests; Phase 1 seam and extra indexes stay, `serve` is a placeholder in Phase 1, a minimal `UserService` (profile update only; signup, login and password change are Phase 2), single-connection test DB.

**Provisional decisions from the spec review (recommended defaults, change if you disagree)**
- Ticket JSON key is `ref` (with `project_key`), everywhere including the UI. Display name is 1 to 100 characters.
- Member removal emits only `member.changed`; the UI reloads tickets on it. A display-name change emits no event.
- Login and signup rate limit stays per (IP, email) as in main-spec. Active API tokens are capped at 100 per user (422 beyond).
- `/mcp` ignores cookies, requires a bearer token, and if an `Origin` header is present it must match `BASE_URL` (absent is allowed). Browser-origin MCP clients are therefore rejected.
- Security headers: `Referrer-Policy: same-origin`, CSP with `base-uri 'none'`. Phase 2 owns the middleware, Phase 6 audits it.
- README security contact: GitHub private vulnerability reporting.
- The service owns the cursor format and refuses token actors for session-only operations (project delete, member management, profile update) with `session_required`.
- Code and shared types come from Phase 1 (`Statuses`, `Priorities`, `LabelColors`, `Max*`, `Validate()`); REST request DTOs live in `httpapi`.

---

## Phase 1: Foundation

**Goal:** a runnable Go project with the database and a tested service layer. No HTTP yet.

**Spec sections:** Tech stack and architecture, Data model, Layout config and deployment (config, SQLite settings, repo layout), Testing.

**Scope**
- Go module, repo layout (`cmd/pabrika`, `internal/config`, `internal/store`, `internal/service`, ...), env config parsing.
- SQLite via `modernc.org/sqlite` with WAL, foreign keys, busy timeout and the single-writer connection setup.
- goose migrations for all ten tables; sqlc-generated queries.
- Service layer for projects, members and roles, tickets (references like `WEB-12`, midpoint ordering, soft delete), labels, comments and the activity log.
- Service layer has no knowledge of HTTP or MCP.

**Out of scope:** auth, HTTP handlers, events, MCP, UI.

**Exit criteria**
- `go build` and `go test ./...` pass with no CGO.
- Service tests run against in-memory SQLite and cover ordering, role and permission rules, last-owner protection, and soft delete.

---

## Phase 2: Auth, sharing and REST

**Goal:** humans and tokens can use every feature over `/api/v1`.

**Spec sections:** Auth, REST API, Security checklist.

**Scope**
- Signup, login, logout, `/auth/me`; argon2id, 10-character minimum, generic login errors, in-memory rate limiting.
- Session cookies (hashed at rest, 30-day sliding expiry) and CSRF defences (Origin check, JSON-only bodies).
- API tokens (`pb_` prefix, read or write scope, optional single-project limit, revoke, `last_used_at`).
- A single "current user" resolution used by both cookies and bearer tokens.
- All `/api/v1` endpoints with role checks, scope checks, 404 for non-members, cursor pagination and the standard error shape.
- CLI subcommands: `pabrika user create`, `pabrika user reset-password`, `pabrika healthcheck`.
- Security headers, body size and field length caps.

**Out of scope:** the event stream (`/events`), MCP, UI.

**Exit criteria**
- `httptest` coverage for auth flows, scope and role matrices, session-only endpoints, and error shapes.
- Non-members get 404, never 403, for projects and their contents.

---

## Phase 3: Live updates

**Goal:** every write is announced to everyone with the project open.

**Spec sections:** Live updates, REST API (events endpoint), Security checklist.

**Scope**
- In-memory event hub, published after DB commit, with a bounded buffer per stream and disconnect of slow clients.
- `GET /projects/{id}/events` (SSE): membership checked on open, 25-second keepalive, streams closed when a member is removed.
- Events carry type, ticket id and actor, never ticket content.
- Hook the hub into every service write, whatever the caller (REST now, MCP later).

**Out of scope:** multi-process fan-out; the client side of the stream (phase 5).

**Exit criteria**
- Tests: the right event arrives after a service write; a non-member's stream receives nothing; a removed member's stream closes.

---

## Phase 4: MCP server

**Goal:** agents can run the board through `/mcp` with an API token.

**Spec sections:** MCP server, Auth (API tokens), Data model (activity log).

**Scope**
- Stateless Streamable HTTP endpoint at `/mcp` using the official Go MCP SDK.
- All 13 tools, with project keys and ticket references accepted in place of ULIDs, and emails and label names in place of ids.
- Read tokens see only read tools; project-limited tokens are rejected for other projects.
- Tool input schemas generated from the same Go structs REST uses.
- Short JSON results; errors with `isError: true` and actionable messages (for example listing valid keys).
- Every write recorded in `ticket_activity` with the token as the actor; `delete_ticket` needs `confirm: true`; no project deletion.
- Optional resources: `pabrika://projects` and `pabrika://projects/{key}/board`.

**Out of scope:** stdio mode (decided: skip), token management over MCP.

**Exit criteria**
- Tests using the SDK's in-memory transport for each tool, scope filtering, and activity logging.
- Manual check: connect Claude Code with the documented `claude mcp add` command and create, move and comment on a ticket.

---

## Phase 5: Web UI

**Goal:** a usable Vue app on top of the existing API.

**Spec sections:** UI, REST API, Live updates.

**Scope**
- Bun, Vue 3, Vite, TypeScript, Vue Router, Pinia, shadcn-vue, Tailwind, `vue-draggable-plus`.
- Screens: login, signup, project board with sidebar, ticket panel (deep-linkable), project settings, account settings with token management and MCP snippet.
- Four fixed columns; optimistic drag and drop with rollback; inline ticket creation; client-side filters.
- Live updates over `EventSource`: refetch only the named ticket, reload the board on reconnect, flash cards changed by others, show Live or Reconnecting.
- Read-only experience for viewers; redirect to `/login` on 401; sanitized markdown rendering.

**Out of scope:** custom design system, attachments, notifications.

**Exit criteria**
- Core flows work end to end against a running server: sign up, create a project, add a member, drag tickets, see an agent's change appear live.
- `bun run build` produces the static bundle that phase 6 embeds.

---

## Phase 6: Ship

**Goal:** one container that is safe and easy to run.

**Spec sections:** Layout config and deployment, Testing, Security checklist.

**Scope**
- Embed the built UI in the Go binary.
- Three-stage Dockerfile (Bun build, Go build, distroless nonroot); `/data` volume; health check via `pabrika healthcheck`.
- Final pass on the security checklist and headers.
- Backup notes (`sqlite3 .backup`, Litestream) and reverse proxy notes (TLS, no buffering of the event stream).
- README with setup, configuration and MCP connection instructions; update `CLAUDE.md` with real build, test and run commands.

**Out of scope:** multi-instance deployment, compose files.

**Exit criteria**
- `docker build` and `docker run` with a volume gives a working app, and data survives a container restart.
- A fresh reader can set up the app and connect an agent from the README alone.
