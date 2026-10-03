# Phase 5: Web UI (detailed spec)

Source of truth: [main-spec.md](main-spec.md) (sections UI, REST API, Live updates, Auth, Security checklist). Phase list: [phases.md](phases.md). If this file conflicts with main-spec.md, main-spec.md wins, except where [phases.md](phases.md) "Decisions" overrides both.

**Goal:** a usable Vue app in `web/` on top of the Phase 2 REST API and Phase 3 event stream.
**Depends on:** Phase 2 (REST, auth; exact resource shapes are in Phase 2 section 6 and are used verbatim here), Phase 3 (`/projects/{id}/events`). Phase 4 (MCP) is not needed, except that the account page shows the MCP snippet text.
**Out of scope:** custom design system, attachments, notifications, embedding in Go (Phase 6), Go changes, i18n, email change, email flows, subpath deployment (the app is served at the origin root; `BASE_URL` has no path). (Profile editing and password change are in scope via `PATCH /auth/me` and `POST /auth/me/password`; see [phases.md](phases.md) "Decisions".)

---

## 1. Backend contract consumed

Base path `/api/v1`. Bodies are JSON. Errors are `{"error":{"code","message","fields?"}}` (`fields` is a map of request field name to message, present on 422). The UI uses the session cookie only (same-origin, `credentials: 'same-origin'`); it never uses API tokens. The browser sends `Origin` automatically on POST, PATCH and DELETE; the UI never sets it.

| Used for | Endpoint |
|---|---|
| Auth | public `GET /auth/config` (`{signup_enabled}`), `POST /auth/signup` (email, display_name, password; 201 `{user}`; 404 when signup is disabled), `POST /auth/login` (email, password; 200 `{user}`), `POST /auth/logout` (204, no body), `GET /auth/me` (200 `{user, auth}`; the store keeps `user`) |
| Account | `PATCH /auth/me` (display_name; 200 `{user}`), `POST /auth/me/password` (current_password, new_password; 204; wrong current password is 422 `fields.current_password`, never 401; deletes the user's other sessions, keeps this one; rate limited) |
| Tokens | `GET /tokens` (`{items, next_cursor: null}`, newest first, revoked ones included with `revoked_at`), `POST /tokens` (name, scope, project_id?; 201 `{token, secret}`), `DELETE /tokens/{id}` (204, idempotent) |
| Projects | `GET /projects?archived=true` (**not paginated**: whole list in the envelope `{items, next_cursor: null}`, ordered by name then key; items carry `role`, no counts), `POST /projects` (key, name, description; 201 project), `GET /projects/{key-or-id}` (project + `role` + `counts`), `PATCH /projects/{key-or-id}` (name, description, archived), `DELETE /projects/{key-or-id}` (204) |
| Members | `GET /projects/{key-or-id}/members` (`{items, next_cursor: null}`, owners first then display name), `POST` (email, role; 201 member), `PATCH/DELETE /projects/{key-or-id}/members/{userId}` (`userId` is the member's `user.id`) |
| Labels | `GET/POST /projects/{key-or-id}/labels` (`{items, next_cursor: null}` ordered by name), `PATCH/DELETE /labels/{id}` |
| Tickets | `GET /projects/{key-or-id}/tickets` (`limit`, `cursor` only; paginated, default 50, max 200), `POST /projects/{key-or-id}/tickets` (title, status; 201 ticket), `GET/PATCH/DELETE /tickets/{ref-or-id}`, `POST /tickets/{id}/move` (`{status, before?, after?, place?}`; 200 ticket plus `renumbered`) |
| Ticket detail | `GET /tickets/{id}/activity` (paginated, newest first), `GET/POST /tickets/{id}/comments` (paginated oldest first; body 1 to 20,000), `PATCH/DELETE /comments/{id}` |
| Live | `GET /projects/{id}/events` (SSE via `EventSource`, session only) |

Project paths (`/projects/{key-or-id}/...`) accept the project key or the ULID; the UI uses the key from the route directly. Ticket paths accept the ULID or the `ref` (`WEB-12`); the UI uses ULIDs once it has them and the reference only for deep links. Label, comment, member and token ids are ULIDs.

**Resource shapes (Phase 2 section 6, used exactly; `types.ts` mirrors them):**
- `user`: `{id, email, display_name}`. `auth_user` (signup, login, `/auth/me`, `PATCH /auth/me`; the auth store keeps this) adds `created_at`.
- `project`: `{id, key, name, description, archived_at, created_at, updated_at, role}` (`role` is the caller's effective role); the single-project GET (`project_detail`) adds `counts: {backlog, todo, in_progress, done}`. List, create and PATCH return no `counts`.
- `member`: `{user: {id, email, display_name}, role, created_at}`.
- `label`: `{id, project_id, name, color}`.
- `ticket`: `{id, ref, project_id, project_key, number, title, description, status, priority, assignee: {id, display_name, email} | null, labels: [label], position, due_date, comment_count, created_at, updated_at}`. The key is `ref` (e.g. `WEB-12`), never `reference`. **List items omit `description`** (type `TicketSummary`) but include `assignee`, `labels` and `comment_count`; `GET /tickets/{id}`, create, PATCH and move responses include `description` (type `Ticket`). The move response additionally carries `renumbered: boolean` (`true` means other cards' positions changed: reload the board). The board holds summaries; the panel fetches the full ticket on open.
- `comment_author` (also the activity `actor`): `{type: 'user'|'api_token', id, name, bot, owner_name?}` (`owner_name` omitted when `type` is `user`; an unresolvable author is `name: "Unknown"`, `bot: false`). `comment`: `{id, ticket_id, author, body, created_at, edited_at}`; `activity`: `{id, ticket_id, actor, action, changes, created_at}`.
- `token`: `{id, name, token_prefix, scope, project: {id, key} | null, last_used_at, revoked_at, created_at}`. Creation response: `{token, secret}`.
- `auth/me` response: `{user: auth_user, auth: {method: 'session'|'token', token?}}`.
- List envelope (every list endpoint): `{items: [...], next_cursor: string | null}`; `next_cursor` is always null for projects, members, labels and tokens.

**Error codes the UI branches on:** 400 `bad_request`, `body_too_large`, `invalid_cursor`; 401 `unauthorized` (session gone) vs `invalid_credentials` (login only); 403 `forbidden`, `insufficient_scope`, `session_required`, `origin_mismatch`; 404 `not_found`; 409 `email_taken`, `key_taken`, `label_exists`, `already_member`, `last_owner`, `project_archived`; 422 `validation_failed` with `fields`; 429 `rate_limited` (`Retry-After`); 503 `unavailable` (stream open during server shutdown) is a transient server error: retry with backoff, no error dialog; 405, 415, 500 are treated as generic failures.

**Event payload (Phase 3 section 2).** Each SSE frame is `event: <type>` plus one-line JSON `data`: `{type, project_id, ticket_id?, comment_id?, label_id?, user_id?, renumbered?, actor: {type: 'user'|'api_token', id}, at}`. `actor` is `{type, id}` only, **no name**; "mine" means `actor.type === 'user' && actor.id === auth.user.id` (a token actor is always someone else and always flashes). `user_id` on `member.changed` is the affected member. No content is carried.

**Event types handled:** `ticket.created`, `ticket.updated`, `ticket.moved`, `ticket.deleted`, `comment.added`, `comment.changed`, `label.changed`, `member.changed`, `project.updated`. Named SSE events: one `addEventListener` per type (no `onmessage`). There is no event on project delete and no final `close` event. The stream sends `retry: 3000` and `: keepalive` comments every 25 s (not visible to the client).

**Not used:** server-side filters on the ticket list (`status`, `priority`, `assignee`, `label`, `q`); filtering is client-side per main-spec and the Decisions. `/mcp` is not called.

---

## 2. Tasks (in order)

1. **Scaffold** `web/` with Bun + Vite + Vue 3 + TS; add Tailwind, shadcn-vue init, Pinia, Vue Router, `vue-draggable-plus`, markdown libs, Vitest + Vue Test Utils. Commit `bun.lock`.
2. **Dev proxy and build config** (section 10); `bun run dev`, `build`, `typecheck`, `test` scripts and the dist check script.
3. **API client** (section 5) with types and 401 handling.
4. **Stores** (section 6): auth, projects, board, ui (theme), toasts via Sonner.
5. **Router and guards** (section 4).
6. **App shell**: sidebar layout, theme toggle, user menu.
7. **Login and signup screens.**
8. **Board** (columns, cards, inline add, toolbar filters, drag and drop, viewer and archived read-only mode).
9. **Ticket panel** (Sheet, deep link, all editable fields, comments, activity, delete).
10. **Live updates** composable and indicator (section 8).
11. **Project settings screen.**
12. **Account settings screen** (profile form, change-password form, tokens, MCP snippet).
13. **Markdown rendering** component (section 9).
14. **Light/dark mode.**
15. **Tests** (section 12) and a manual end-to-end pass against a running Go server.
16. Confirm `bun run build` outputs `web/dist` with an `index.html` and hashed assets under `web/dist/assets/` (what Phase 6 embeds) and that `bun run check:dist` passes.

---

## 3. Directory structure

```text
web/
  package.json  bun.lock  tsconfig.json  vite.config.ts  index.html
  components.json                 # shadcn-vue config
  public/                         # favicon etc. (local files only)
  scripts/check-dist.ts           # post-build assertions (section 10)
  src/
    main.ts  App.vue  style.css   # tailwind + shadcn CSS variables (light/dark)
    router/index.ts               # routes + guards
    api/
      client.ts                   # fetch wrapper, ApiError, 401 handling
      types.ts                    # DTOs mirroring Phase 2 section 6
      auth.ts projects.ts members.ts labels.ts tickets.ts comments.ts tokens.ts
      events.ts                   # EventSource wrapper
    stores/
      auth.ts projects.ts board.ts ui.ts
    composables/
      useProjectEvents.ts         # SSE lifecycle bound to a board
      useBoardFilters.ts          # client-side filtering, URL query sync
      useCan.ts                   # role helpers (canEdit, isOwner), archived-aware
    lib/
      markdown.ts                 # render + sanitize
      dates.ts                    # overdue, relative time, local date-only parsing
      placement.ts                # pure move-placement computation
      activity.ts                 # humanize activity rows
      utils.ts                    # shadcn cn()
    components/
      ui/                         # shadcn-vue generated components
      layout/   AppSidebar.vue  AppShell.vue  ThemeToggle.vue  UserMenu.vue
      board/    BoardColumn.vue  TicketCard.vue  InlineAddTicket.vue  BoardToolbar.vue  LiveIndicator.vue  BoardSkeleton.vue
      ticket/   TicketPanel.vue  AssigneePicker.vue  LabelPicker.vue  PrioritySelect.vue  StatusSelect.vue  CommentList.vue  CommentForm.vue  ActivityList.vue  MarkdownView.vue  MarkdownEditor.vue
      project/  CreateProjectDialog.vue  MembersTable.vue  LabelsManager.vue  DangerZone.vue
      account/  ProfileForm.vue  ChangePasswordForm.vue  TokensTable.vue  CreateTokenDialog.vue  TokenSecretDialog.vue  McpSnippet.vue
      common/   UserAvatar.vue  BotBadge.vue  ConfirmDialog.vue  EmptyState.vue  LostAccessDialog.vue
    views/
      LoginView.vue  SignupView.vue  HomeRedirect.vue
      BoardView.vue  ProjectSettingsView.vue  AccountSettingsView.vue  NotFoundView.vue
  tests/                          # Vitest specs; tests/fixtures/ holds JSON copies of the Phase 2 shapes
```

---

## 4. Routes and guards

| Route | Name | View | Auth |
|---|---|---|---|
| `/login` | `login` | LoginView | public (redirect to `/` if already signed in) |
| `/signup` | `signup` | SignupView | public (same); route guard redirects to `/login` when `signup_enabled` is false |
| `/` | `home` | HomeRedirect | required |
| `/p/:key` | `board` | BoardView | required |
| `/p/:key/t/:number` | `ticket` | BoardView (same component, panel open) | required |
| `/p/:key/settings` | `project-settings` | ProjectSettingsView | required |
| `/settings` | `account` | AccountSettingsView | required |
| `/:pathMatch(.*)*` | `not-found` | NotFoundView | required |

`board` and `ticket` share one `BoardView` instance (one route record with an optional `:number?` param, or a child route) so opening the panel never reloads the board or the stream. The panel is driven by the route, so browser Back closes it and a deep link opens it. Card clicks, panel close and filter changes preserve the current query string (filters). `:number` must be a positive integer; otherwise the panel shows "Ticket not found".

**Key normalization:** the route key is case-insensitive. After the project loads, if the route key differs from `project.key` (lowercase or a ULID was used), `router.replace` to the canonical `/p/<KEY>[/t/<n>]`.

**Guard (`beforeEach`):**
1. If `auth.status === 'unknown'`, await `auth.bootstrap()` (calls public `GET /auth/config` for `signup_enabled`, and `GET /auth/me`; a 401 means signed out, not an error). If the config call fails, assume signup is enabled (the server still returns 404 when it is off). If `/auth/me` fails with a network or 5xx error (not 401), show a full-page "Can't reach the server" state with Retry instead of redirecting to `/login`.
2. Route requires auth and no user: redirect to `/login?redirect=<fullPath>`.
3. Public auth routes with a user: redirect to `/`.
3a. Route `signup` with `signup_enabled === false`: redirect to `/login`.
4. After login, go to the `redirect` query only if it is a same-origin relative path: starts with a single `/`, not `//`, contains no backslash and no scheme, and is not `/login` or `/signup`; else `/`.

**`/` behavior:** read `lastProjectKey` from `localStorage` (try/catch; may throw or be empty). If it matches a project in the loaded list, replace-route to `/p/:key`. Else if the list is non-empty, go to the first project. Else render the empty state with "Create your first project". While the list loads, show a skeleton; on list failure show an inline error with Retry.

**Unknown or inaccessible project key** (the project call returns 404 for non-members and unknown keys alike): show a not-found state with a link home. Never reveal whether the project exists. This is distinct from "lost access" (section 8), which applies to a project the user had open.

---

## 5. API client layer

`api/client.ts`:
- `request<T>(method, path, { body?, query?, signal? })`: prefixes `/api/v1`, sets `Content-Type: application/json` only when there is a body (POST or PATCH that need a body always send one, `{}` if empty; logout and DELETE send none), `Accept: application/json`, `credentials: 'same-origin'`. Handles 204.
- Throws `ApiError { status, code, message, fields?, retryAfter? }`, parsed from the standard error shape (`fields` from 422, `retryAfter` from the `Retry-After` header on 429); a non-JSON body or network failure yields `ApiError` with `status 0`, `code 'network'`.
- **401 handling:** on a 401 with code `unauthorized` from any call other than `POST /auth/login`, `POST /auth/signup` and the initial `GET /auth/me`, call `auth.handleUnauthorized()`: clear user, close any event stream, reset board store, `router.replace('/login?redirect=...')` once (debounce so parallel failing calls redirect once), and show a "Session expired" toast. `invalid_credentials` never triggers it.
- 403 handling: `origin_mismatch` toasts "Request blocked: the page origin does not match the server's BASE_URL" (the common dev misconfiguration); `forbidden` and `insufficient_scope` toast the server message and refetch the project (the role may have changed) via the board store when a board is open. 404 passes through for callers to interpret. 409 and 422 are surfaced in forms: map `fields` onto the form field of the same name (request field names: `title`, `due_date`, `assignee`, `labels`, `email`, `role`, `current_password`, `new_password`, `display_name`, `name`, `key`, `color`, `body`); unmapped 409 codes go to the nearest field (`email_taken`: email; `key_taken`: key; `label_exists`: name; `already_member`: email) or an inline form error. 429 shows "Too many attempts, try again in <retryAfter> seconds" (or "in a minute" when absent). 5xx (including 503 `unavailable`) and `network` toast "Something went wrong" or "Network error, check your connection"; idempotent reads (board load, reloads, probes) retry with capped backoff on 503 and `network`, and a 429 honors `Retry-After` before retrying a read. Mutations are never retried automatically.
- Pagination helper `listAll<T>(path, query)`: follows `next_cursor` until null (limit 200), used for tickets and comments; projects, members, labels and tokens are single calls returning `items` (their `next_cursor` is always null; `listAll` would also cope) (activity loads one page of 50 with "Load more"). Pass the `signal` through; an invalid cursor mid-way aborts the load as an error.
- Resource modules are thin typed functions (`tickets.move(id, {status, before?|after?|place?})`, etc.). All DTO types live in `types.ts`; unions for `Status = 'backlog'|'todo'|'in_progress'|'done'`, `Priority = 'low'|'medium'|'high'|'urgent'`, `Role = 'owner'|'editor'|'viewer'`, `Scope = 'read'|'write'`.

---

## 6. Pinia stores

**`auth`**
- State: `user | null` (an `auth_user`: `id, email, display_name, created_at`), `status: 'unknown'|'authed'|'anon'`, `signupEnabled: boolean` (from `GET /auth/config`).
- Actions: `bootstrap()`, `login()`, `signup()`, `logout()` (call `POST /auth/logout`, then clear all stores, go to `/login`), `updateProfile(displayName)` (`PATCH /auth/me`, replaces `user`), `changePassword(current, next)` (`POST /auth/me/password`; the session stays valid), `handleUnauthorized()`.

**`projects`**
- State: `list: Project[]` (non-archived by default), `includeArchived`, `loading`, `error`, `lastProjectKey` (persisted, try/catch).
- Getters: `byKey(key)` (case-insensitive).
- Actions: `fetch()` (one `GET /projects`, unpaginated; reads `items`), `create(input)` (then navigate to `/p/:key`), `updateLocal(project)`, `remove(id)`. The sidebar lists these; the `project.updated` event and settings edits refresh the list. If the open project is archived and hidden by the toggle, the sidebar still shows it highlighted (inserted from the board store's `project`).

**`board`** (one active project at a time)
- State:
  - `project` (with `role`, `counts`, `archived_at`), `members[]`, `labels[]`
  - `ticketsById: Record<id, TicketSummary>`; `columns: Record<Status, string[]>` (ordered ids, sorted by `position` then `id`)
  - `loadState: 'idle'|'loading'|'ready'|'error'|'no-access'|'not-found'`
  - `flashIds: Set<id>` (transient), `pendingMoves: Set<id>`, `dragging: { column?: Status } | null`, `deferredEvents: Event[]`, `moveQueue` (serialized, see 7.3), `leaving: boolean`
  - `selectedRef` (from route)
- Getters: `canEdit` (role editor or owner **and** project not archived), `isOwner` (role owner), `isViewer`, `isArchived`, `filteredColumns` (applies filters, see 7.3), `counts`, `ticketByNumber(n)`.
- Actions:
  - `load(key, { quiet? })`: use the route key directly in the paths (no key-to-id lookup), and in parallel fetch project, members, labels, all tickets (`listAll`). Replaces state atomically. Abort an in-flight load if the key changes (AbortController). A 404 on the project or ticket-list call sets `no-access` (when the project was previously loaded in this session) or `not-found` (first load). `quiet: true` keeps the current UI (no skeleton) and keeps `flashIds`. A quiet reload requested while a drag is active or a move is pending is deferred until they settle (never replace state under a Sortable list); if a load is already in flight, mark `reloadAfter` and rerun once it ends.
  - `createTicket(status, title)`, `updateTicket(id, patch)`, `deleteTicket(id)`.
  - `moveTicket(id, toStatus, placement)`: optimistic (section 7.3).
  - `refreshTicket(id)`: `GET /tickets/{id}`; on 404 remove locally (the ticket is gone; this is **not** lost access); otherwise upsert (dropping `description` into the summary type) and re-slot by position/status. Discard a refresh result if a local mutation on that ticket was issued after the refresh started (per-ticket mutation counter), and refetch once.
  - `applyEvent(event)`: section 8.
  - `reset()`.
- The settings screen uses `load(key, { tickets: false })` (project, members, labels only) and the same events composable (ticket events are ignored when tickets are not loaded).

**`ui`**: `theme: 'light'|'dark'|'system'` persisted to `localStorage` (try/catch); `sidebarOpen` (shadcn Sidebar handles its own cookie/state).

Keep ticket detail extras (full ticket with description, comments, activity) local to `TicketPanel` (fetched on open, refetched on matching events), not in a global store.

---

## 7. Screens and behavior

### 7.1 Login / signup
- Centered `Card` with `Input`s and `Button`. Signup adds display name (1 to 100); client hints: email format, password >= 10 characters. Server remains authoritative.
- Show the server error message inline. Login failures (401 `invalid_credentials`) show the generic message returned ("Invalid email or password"). Signup 409 `email_taken` and 422 `fields` show under the field. Disable submit while pending. 429 shows the rate-limit message from section 5. A 404 from signup (disabled while the page was open) refreshes `signupEnabled` and redirects to `/login`.
- When `auth.signupEnabled` is false (from `GET /auth/config`), hide the "Create account" link on login; `/signup` redirects to `/login`.
- Cross-link between the two screens; honor `redirect`.

### 7.2 App shell and sidebar
- shadcn `Sidebar`: project list (name, key), active highlight, "New project" button (opens `CreateProjectDialog`: name 1 to 100, key 2-6 letters auto-uppercased (suggested from the name until edited), description <= 2,000; 409 `key_taken` and 422 `fields` shown inline), link to account settings, user menu (display name, theme toggle, logout). Collapsible on small screens. Loading skeleton and an error row with Retry for the list.
- Archived projects are hidden; a "Show archived" toggle in the sidebar sets `?archived=true`. Archived projects open read-only (see 7.3).

### 7.3 Board (`/p/:key`)
- Header: project name, key, role badge, "Archived" badge when archived, toolbar (search, filters, Live indicator), link to project settings (shown to all members; non-owners get a read-only view, see 7.5).
- **Read-only states:** `canEdit` is false for viewers and for archived projects. An archived project shows a banner "This project is archived and read-only" (owners see an "Unarchive" link to settings). If a write returns 409 `project_archived`, toast it, refetch the project, and roll the optimistic change back.
- Four columns in fixed order: Backlog, To do, In progress, Done, each with title and count (count reflects visible, filtered tickets; show "n of m" when filtering).
- `Skeleton` placeholders while `loadState === 'loading'`; inline error with "Retry" on `error`; not-found and lost-access states per sections 4 and 8.
- `TicketCard`: `ref` (`WEB-12`), title (clamped), priority `Badge` (color per level, label text always shown, not color alone), label chips (color from the fixed palette mapped to Tailwind classes; unknown color falls back to gray), due date (red with a visually hidden "Overdue" when the date is before today in the user's local time and status is not `done`; `due_date` is a date-only string, parsed as a local date, never via `new Date('YYYY-MM-DD')` which is UTC), assignee `Avatar` (initials fallback), comment count. The card is a focusable link to `/p/:key/t/:number` (`router.push`, so Back closes the panel); Enter opens it. Sortable uses a small `delay`/`fallbackTolerance` (and `delayOnTouchOnly`) so a plain click or tap opens the panel instead of starting a drag.
- **Inline add:** "Add ticket" button at the bottom of each column (`canEdit` only) swaps to an `Input` (max 200); Enter creates (`POST` with `status`), keeps the field open and focused for rapid entry; Escape or blur-when-empty closes. Optimistically show a card with a temporary client id (not draggable or openable until reconciled), then on the 201 response replace it with the server ticket. The `ticket.created` event may arrive before the response: upserting by real id is idempotent, and the response handler removes the temporary card and upserts by id, so no duplicate appears. On error, remove the temp card, toast, and restore the text in the field (422 shows the field message). The ticket is created at the bottom of the column (server default).
- **Filters (client-side only, on the loaded ticket summaries):** text search over `ref` and title (case-insensitive substring; description is not in the list payload and is not searched); priority (multi-select); assignee (`Select`: Anyone, Me, Unassigned, or a member); label (multi-select, ticket must have any selected). "Clear filters" button. Filters persist in the URL query via `router.replace` (search debounced ~200 ms): `?q=<text>&priority=high,urgent&assignee=me|none|<userId>&label=<labelId>,<labelId>`; unknown values or ids are ignored; `me` is resolved against the signed-in user. They survive reload and are not stored on the server. **While any filter is active, drag and drop is disabled** (positions would be ambiguous); the board shows a hint ("Clear filters to drag tickets") and the Status dropdown in the ticket panel remains available as the alternative.
- **Drag and drop** (`vue-draggable-plus`, shared `group="tickets"`, one list per column):
  - **Binding:** each column's Sortable list is bound to a *local copy* of that column's id array (kept in sync from the store by a watcher that does not run while `dragging` is set). Sortable and Vue never touch store state directly during a drag. A rollback or a reload bumps a per-column `key` so the local copy and DOM re-sync from the store.
  - Disabled (`disabled` prop) when `!canEdit`, when any filter is active, or for a card that is a temporary (unsaved) ticket.
  1. On `onStart`: set `board.dragging = { column }`.
  2. On `onAdd` (cross-column, handled on the target list) and `onUpdate` (same column): take the target column's id list after the drop and `newIndex`, remove the moved ticket id from it, and call the pure `computePlacement(idsWithoutMoved, newIndex)` in `lib/placement.ts`: `{after: ids[newIndex-1]}` if there is a previous neighbor, else `{before: ids[newIndex]}` if there is a next neighbor, else `{place: 'top'}` (empty column). Neighbors are therefore always tickets currently in the target column (the server requires this). Never send raw positions. If the ticket ends where it started (same column, same neighbors), do nothing. `onRemove` on the source list does nothing.
  3. Apply the move locally first (snapshot previous `columns` and the ticket's status/position for rollback). Mark `pendingMoves`.
  4. **Serialize moves:** requests go through a single FIFO queue per board, so each request's neighbors are valid against server state produced by the previous one. A queued move whose neighbor ids no longer exist locally is recomputed from the current local order before it is sent.
  5. On success: replace the ticket with the server response so `position` matches the server; if the response has `renumbered: true` (or the matching `ticket.moved` event does), do a full quiet reload.
  6. On failure: restore the snapshot, bump the column keys, and show a destructive toast with the server message ("Couldn't move WEB-12: ..."). 422 (a neighbor was moved or deleted by someone else meanwhile) and 409 `project_archived` additionally trigger a quiet reload. 404 on the ticket means it is gone: remove it. 404 on the project is the lost-access path. 403 refetches the project (role changed).
  7. On `onEnd` (always): clear `dragging` and flush `deferredEvents` (section 8) once the queue is empty or the move has settled.
  - **Concurrent moves by others:** last write wins by server position; clients converge through events. A conflicting drag either succeeds (server order) or fails with 422 and rolls back.
- **Viewer read-only mode:** `useCan` exposes `canEdit`. When false: drag disabled, no "Add ticket", no delete, all panel fields rendered as plain text (title, rendered markdown only, priority badge, assignee name, label chips, no pickers, no Status select), no comment form. Optional small "View only" badge in the header. Never rely on this for security; the server enforces.

### 7.4 Ticket panel (`/p/:key/t/:number`)
- shadcn `Sheet` (right side) over the board. Opening via card click or deep link. Closing navigates to `/p/:key` (keeping the filter query; `router.push`, or `back` when the previous entry is this board). Escape closes; focus returns to the originating card (or the board heading on a cold deep link).
- Panel data: on open, `GET /tickets/{id}` for the full ticket (the list payload has no description) while showing the summary immediately; comments (`listAll`) and the first activity page are fetched in parallel. Each part has its own loading skeleton and inline error with Retry.
- Deep link on a cold load: the board loads, then the panel opens for the ticket whose `number` matches. If not in the loaded board (deleted or never existed), fetch `GET /tickets/{KEY-number}`; on 404 show "Ticket not found" in the panel and a button to close. Because the list endpoint returns open tickets only, treat a 404 as final.
- Content:
  - Title: editable text field (`canEdit`); saves on blur or Enter via `PATCH` if changed and non-empty; max 200; 422 shows the field message and keeps the draft.
  - Description: `Tabs` "Write" / "Preview" with a `Textarea` and `MarkdownView`; explicit Save and Cancel; max 20,000 with a counter. Viewers see only rendered markdown. If the server copy changes while the user has an unsaved draft (title or description; live event), keep the draft and show a "Changed by someone else" notice with a "Reload" action (last save wins, per main-spec).
  - Status `Select` (`canEdit`): the keyboard alternative to dragging; it calls `POST /tickets/{id}/move` with `{status, place: 'bottom'}` through the same optimistic move path and queue. (`status` is not patchable.)
  - Priority `Select`; due date date input (clearable, sends `null`); assignee picker (`Popover` + `Command` over `board.members` users, includes "Unassigned"); label picker (`Popover` + `Command`, multi-select, with "Create label 'x'" entry for editors that calls `POST /labels` then assigns it; 409 `label_exists` selects the existing label instead).
  - Property edits send `PATCH /tickets/{id}` with only the changed fields (`assignee` user id or `null`, `labels` the full list of label ids, `due_date` `YYYY-MM-DD` or `null`), optimistic with rollback toast; the response replaces the ticket in the panel and the board.
  - Comments (oldest first): author name (`author.name`), relative time, rendered markdown; token-authored comments (`author.bot`) show the token name plus `BotBadge` and `owner_name` in a tooltip. `canEdit` users can add a comment (Ctrl/Cmd+Enter submits; body max 20,000 with counter); the 201 response is appended by id (the own `comment.added` event is idempotent). A user can edit their own comment (`author.type === 'user' && author.id === me`) while `canEdit` (`PATCH /comments/{id}`); delete (`DELETE`, ConfirmDialog) is shown to the author while `canEdit` and to owners for any comment. Agent (token) comments show the `BotBadge`; a human has no edit control for them (owners can still delete). Agents edit their own comments through the MCP `update_comment` tool. Edited comments (`edited_at`) show "edited". Deleted comments are removed. 403 or 409 on these calls toast the message and refetch.
  - Activity: collapsed list under a `Tabs` ("Comments", "Activity"), newest first with "Load more" (`next_cursor`); humanize via `lib/activity.ts`: `action` is one of created, updated, moved, assigned, labeled, deleted; `changes` is `{field: [old, new]}` where `status` and `priority` are plain values, `assignee` holds user ids (resolve through `board.members`, fallback "someone"), `labels` holds name lists, `description` is truncated text (show "changed the description" without the text), `position` is never shown; unknown actions or fields fall back to a generic "updated <field>". Actor shown as user or token (name plus bot badge).
  - Delete ticket (`canEdit`): `ConfirmDialog`, then `DELETE`, close panel, remove card.
- Panel data is refetched when an event names this ticket (section 8). If the ticket is deleted elsewhere while open, the panel closes with a toast.

### 7.5 Project settings (`/p/:key/settings`)
- Visible to all members. Non-owners see General and Members read-only; editors still manage labels; viewers see labels read-only. Archived projects: labels are read-only for everyone (server returns 409), the owner can still unarchive, manage members and delete. Uses `Tabs`: General, Members, Labels.
- **General (owner):** edit name (1 to 100) and description (<= 2,000) (`PATCH`; 422 inline); archive/unarchive toggle (`PATCH {archived}`); **Danger zone**: delete project, requiring the user to type the project key in a `Dialog`, then `DELETE`; order: close the event stream and set `board.leaving`, call `DELETE`, on success reset and go to `/` after refreshing the project list; on failure reopen the stream. No event is emitted for project delete, so nothing else clears other members' boards; their streams close and reconnect gets 404 (lost access). Project key is shown but not editable.
- **Members (owner edits):** table of avatar, name, email, role (from `member.user` and `role`; row key and the `{userId}` in PATCH/DELETE paths is `member.user.id`; "me" is `member.user.id === auth.user.id`). Add member form (email + role `Select`) calling `POST`; 422 `fields.email` ("No account with this email") and 409 `already_member` shown inline. Change role via `Select` (`PATCH`). Remove via `DropdownMenu` + confirm (`DELETE`). The server enforces last-owner protection (409 `last_owner`); show its message. Any member (not only owners) can leave: "Leave project" (`DELETE` own membership) with confirm; set `board.leaving` and close the stream first so the self-inflicted stream close does not show the lost-access dialog; after leaving, go to `/` and refresh the list. An owner removing themself is the same path.
- **Labels (editors/owners):** list with color swatch; create (name max 50 + color from the fixed palette `gray, red, orange, amber, green, teal, blue, indigo, purple, pink` via a color picker; 409 `label_exists` inline), rename/recolor, delete (confirm; note it is removed from tickets).

### 7.6 Account settings (`/settings`)
- **Profile:** `ProfileForm` shows email (read-only) and an editable display name (trimmed, 1 to 100); saves with `PATCH /auth/me` via `auth.updateProfile`, success toast, field errors inline. The sidebar user menu updates immediately.
- **Change password:** `ChangePasswordForm` with current password and new password (client hint >= 10 characters; server authoritative) calling `POST /auth/me/password`. Show field errors (422 `fields.current_password` "Incorrect password", `fields.new_password`; 429 rate-limit message). A wrong current password must never redirect to login (it is 422, not 401). On success (204): toast and clear the fields; other sessions are signed out and this one stays signed in.
- **API tokens:** `TokensTable` (name, `token_prefix...`, scope badge, project limit (`project.key`) or "All projects", last used (or "Never"), created, status, Revoke button with confirm). `CreateTokenDialog`: name (required, max 100), scope `Select` (read/write, default read), project limit `Select` ("All my projects" or one of my projects, sent as that project's ULID `project_id`). On 201 `{token, secret}`, `TokenSecretDialog` shows the full secret once with a copy button and the warning "You won't be able to see this again"; closing it clears the secret from memory (never written to storage, URL, or logs). Revoked tokens (`revoked_at` set; the API keeps them in the list) are shown muted with a "Revoked" badge and no Revoke button. Revoking an already revoked token is a no-op on the server.
- **MCP snippet:** `McpSnippet` shows the `claude mcp add --transport http pabrika <origin>/mcp --header "Authorization: Bearer pb_your_token_here"` command with a copy button, where `<origin>` is `window.location.origin` (in dev through Vite this is the Vite origin, which does not proxy `/mcp`; the snippet notes "use the Go server's address when developing"). If a token was just created in this view, offer to fill it into the snippet (still in-memory only, never persisted). Also a short generic note for other MCP clients (Streamable HTTP + custom `Authorization` header). The snippet section says nothing about editing comments.

### 7.7 Errors and empty states
- Network error on load: board shows an inline error with "Retry". A global offline condition shows "Reconnecting" in the Live indicator; mutations made offline fail with a toast and roll back (no offline queue).
- Global toast (Sonner) for failed mutations. Forms show inline errors. All screens have loading (Skeleton), empty and error states: sidebar list, board, panel parts, members, labels, tokens, activity.
- Empty project: columns each show "No tickets"; first-time empty state with hint to add one (viewers see "No tickets yet").
- 404 for an unknown route: `NotFoundView` with a link home.

---

## 8. Live updates (EventSource)

`composables/useProjectEvents.ts` opens `new EventSource('/api/v1/projects/{key}/events')` (named events, one `addEventListener` per event type) when the board (or the project settings screen) becomes `ready` and closes it on unmount, project change, logout, or 401. One `EventSource` per open project.

**Lifecycle**
- `onopen` (first open and every reconnect): set indicator `live` and run a **full quiet board reload** (`board.load(key, { quiet: true })`) **every time, including the first open**. The stream is opened after the initial load, so events between that load and the subscription would otherwise be missed (Phase 3: the reload on open is what guarantees nothing is missed; there is no replay or `Last-Event-ID`). The one extra list fetch on first connect is accepted. The quiet reload follows the deferral rules in section 6 (not during a drag or pending move; coalesced with an in-flight load).
- `onerror`: inspect `readyState`.
  - `CONNECTING` (network drop, server restart, slow-client or shutdown close; the browser is retrying by itself after the server's `retry: 3000`): set indicator `reconnecting`; do nothing else. Do not add a second retry loop.
  - `CLOSED` (the browser gave up, which happens for any non-200 response such as 401, 404 or 503): close the stream and probe `GET /projects/{key}` once. 401: client 401 handling (login redirect, session invalidated or password changed on another session). 404: the **lost-access** path (removed from the project, or the project was deleted). Anything else (200, 429, 5xx including 503 `unavailable`, network): set `reconnecting` and recreate the `EventSource` with a capped backoff (3 s, 6 s, 12 s, up to 30 s; a 429 probe waits at least its `Retry-After`), resetting on a successful open.
- Indicator `LiveIndicator`: a small dot + label "Live" (green) or "Reconnecting" (amber, pulsing; static with reduced motion) in the toolbar, inside an `aria-live="polite"` region.
- If the tab is hidden for a long time the browser may throttle the stream; no special handling (the reload on open covers any gap).

**Event handling (`board.applyEvent`)**, where "mine" is `actor.type === 'user' && actor.id === auth.user.id`:

| Event | Action |
|---|---|
| `ticket.created`, `ticket.updated`, `ticket.moved` | `refreshTicket(ticket_id)` (fetch only that ticket), upsert, re-slot into its column by `position`; flash if not mine. If `ticket.moved` has `renumbered: true` (server rebalanced the column's positions), do a full quiet board reload instead of a single-ticket refresh |
| `ticket.deleted` | remove card locally (no fetch); if it was open in the panel, close it and toast "WEB-12 was deleted" (only when not mine); no flash |
| `comment.added`, `comment.changed` | if the panel shows `ticket_id`, refetch comments (and activity); also `refreshTicket(ticket_id)` to update the comment count |
| `label.changed` | quietly refetch labels **and** the full ticket list (the event has no sub-type and a delete or rename changes the label objects embedded in tickets) |
| `member.changed` | refetch members **and the full ticket list** (removing a member unassigns their tickets without any ticket event); refetch the project (`GET /projects/{key}`) so `role` updates without reload (viewers become editors or vice versa; a 404 means lost access). If `user_id` is the current user and their membership is gone, the stream closes next and the CLOSED path confirms |
| `project.updated` | refetch the project (name, archived state, counts, role) and refresh the sidebar list; archived state flips `canEdit` and the banner |

- **Own events:** applying them is idempotent (the optimistic path already did the work); still refresh the single ticket, but do not flash. Refresh results for a ticket with a pending move or an unsettled local mutation are discarded and refetched after it settles (see `refreshTicket`). De-dupe in-flight refreshes per ticket id (coalesce rapid events: a Map of pending promises, trailing refetch if an event arrives mid-fetch).
- **Flash:** add the ticket id to `flashIds` for ~1.5 s (CSS ring/background animation on `TicketCard`; no animation under `prefers-reduced-motion`), only for events whose actor is not me (an `api_token` actor always flashes).
- **Drag in progress:** while `board.dragging` is set (or a move is pending), any event whose ticket is in the dragged column (source or hovered target, tracked at least for the source column) is pushed into `deferredEvents` and applied, in order, after the drop completes and the move queue settles. Events for other columns apply immediately. Never mutate the arrays a Sortable list is bound to while the drag is active. If the dragged ticket itself is the subject of a deferred event, apply it after the move request settles so the local order is not clobbered. Deferral also holds quiet reloads (section 6).
- **Lost access:** triggered by a 404 on a **project-level** call (board load of a previously loaded project, project refetch, the probe) or by a CLOSED stream whose probe is 404. A 404 on a single ticket call is a deleted ticket, not lost access. Behavior (skipped silently when `board.leaving` is set): close the stream, set `loadState = 'no-access'`, show `LostAccessDialog` ("You no longer have access to this project", also covers a deleted project), remove it from the sidebar list, then `router.replace('/')` when the dialog is dismissed.
- **Session invalidated:** the server re-validates the session on every 25-second keepalive and closes the stream on logout, expiry or password change elsewhere; reconnect returns 401, the browser sets CLOSED, the probe returns 401 and the client 401 handling goes to `/login`.

---

## 9. Markdown rendering and sanitization

- Library: `markdown-it` with `html: false`, `linkify: true`, `breaks: true`, and the `image` rule disabled (images are dropped in v1 to avoid remote content tracking; `![x](y)` renders as plain text); sanitize the output with `DOMPurify` (tight allowlist: headings, paragraphs, lists, code/pre, blockquote, strong/em/del, tables, links, hr, br; `img`, `svg`, `iframe`, `form`, `style` and `script` forbidden; no `style` attribute, no event attributes).
- Links: only `http`, `https`, `mailto` (`ALLOWED_URI_REGEXP`); force `target="_blank"` and `rel="noopener noreferrer nofollow"` via a DOMPurify `afterSanitizeAttributes` hook.
- `MarkdownView` is the only place that uses `v-html`, and it only ever receives the sanitized string from `lib/markdown.ts`. Used for descriptions, comments and the editor preview. Phase 6 greps for `v-html` in `web/src`; keep it to this one component.
- Tests must prove `<script>`, `<img onerror>`, `[x](javascript:alert(1))` (including mixed case and entity-encoded schemes), `data:` and `vbscript:` links, `<iframe>`, SVG with script, raw HTML, and `![](http://x/y.png)` are neutralized, and that normal links get the `rel` and `target` attributes.
- Both client and the server's CSP are defense in depth; the server CSP is `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'` (Phase 2 and 6). The bundle must therefore emit no inline scripts, no `eval` or `new Function` (use the runtime-only Vue build; no string templates outside tests), no external fonts, images or CDN calls, and no `blob:` or remote `connect` targets.

---

## 10. Dev setup, build and theming

**Dev proxy (`vite.config.ts`)**
- Dev server on `5173`, `proxy: { '/api': { target: 'http://localhost:8080', changeOrigin: true } }`. Covers `/api/v1/**` including the SSE path (Vite's proxy streams SSE; do not enable compression or buffering, and set no proxy timeout). Open the app as exactly `http://localhost:5173` (not `127.0.0.1`, which would send a different `Origin` and get `origin_mismatch`), with the Go server run with `BASE_URL=http://localhost:5173`.
- **Origin check:** the Go server rejects mutations whose `Origin` does not match `BASE_URL`, and the browser sends `Origin: http://localhost:5173` in dev. Decided: run the Go server with `BASE_URL=http://localhost:5173` (no Origin rewriting in the proxy) and `COOKIE_SECURE=false`; note it in `vite.config.ts` comments. Full command: `BASE_URL=http://localhost:5173 COOKIE_SECURE=false go run ./cmd/pabrika serve`.
- Do not proxy `/mcp` (not used by the UI). (Phase 6 mentions proxying `/mcp` as well; that is harmless but unused.)
- Path alias `@` to `src`. Do not confuse Vite's `import.meta.env.BASE_URL` (always `/`) with the server's `BASE_URL`; the UI reads neither the server's env nor any runtime config (it uses `window.location.origin`).

**Build**
- `bun run build` runs `vue-tsc --noEmit`, a `prebuild` clean, `vite build`, then `bun run check:dist`. Output goes to `web/dist/` (`build.outDir`, default) with `base: '/'` (absolute `/assets/<hash>.js` paths; deep links like `/p/WEB/t/12` and a reload on them work because Phase 6 falls back to `index.html`). The app is root-served only; there is no subpath support.
- Phase 6 commits `web/dist/.gitkeep` and ignores the rest of `web/dist`. A default Vite build empties `outDir` and so deletes `.gitkeep`. Set `build.emptyOutDir: false` and have the `prebuild` clean step delete everything in `web/dist` **except** `.gitkeep`, recreating `.gitkeep` if it is missing (also after a build, in case it was removed). Phase 6's Makefile `make web` target also recreates `web/dist/.gitkeep` after `bun run build`, so the placeholder survives either way; `check:dist` ignores `.gitkeep`.
- `scripts/check-dist.ts` fails the build if: `web/dist/index.html` is missing; `index.html` has any inline `<script>` (a script tag without `src`) or inline event handler attribute; any file in `dist` references an `http(s)://` URL for a script, stylesheet, font or image (external origins); no `assets/` directory with hashed files exists; any emitted JS contains `eval(` or `new Function(` from our own code (informational allowlist for known-safe library matches, recorded in the script).
- Fonts and assets are local (system font stack, or fonts bundled via the build); favicon in `public/`; no CDN; no external network calls.
- SPA fallback: all non-`/api`, non-`/mcp`, non-`/healthz` paths serve `index.html`; Phase 6 handles this in Go. Router uses `createWebHistory`. Hashed asset files are served immutable by Phase 6, so never reference a non-hashed JS or CSS file.

**Light/dark mode**
- Tailwind `dark` class strategy with shadcn CSS variables. `ThemeToggle` (`DropdownMenu`: Light, Dark, System) persists to `localStorage` (try/catch) and applies the class on `<html>` before mount (in `main.ts`; no inline script because of the CSP, so a brief flash on load is accepted per the Decisions). System follows `prefers-color-scheme` live.
- Priority and label colors must be legible in both modes.

**Accessibility baseline:** keyboard operable dialogs/sheets (shadcn defaults), focus trap and return on close, `aria-live` for the Live indicator, sensible button labels, cards are focusable links, color is never the only signal (priority text, "Overdue" text, "Live"/"Reconnecting" text), `prefers-reduced-motion` respected. Drag and drop has no keyboard alternative; the ticket panel Status `Select` (editors) is the alternative (it moves to the bottom of the chosen column), and when filters disable dragging the hint explains why.

---

## 11. shadcn-vue components to add

`Button`, `Input`, `Textarea`, `Card`, `Badge`, `Avatar`, `Dialog`, `Sheet`, `Select`, `Popover`, `Command`, `DropdownMenu`, `Sidebar`, `Sonner` (toaster), `Skeleton`, `Tabs` (from main-spec). Small additions from the same registry that support the listed screens and are not new design-system work: `Label`, `Separator`, `Tooltip`, `Alert`, `Table`, `Checkbox`, `Switch`. Install via the shadcn-vue CLI under `src/components/ui/`.

Other runtime dependencies: `vue`, `vue-router`, `pinia`, `vue-draggable-plus`, `markdown-it`, `dompurify`, `lucide-vue-next`, `@vueuse/core` (optional), `class-variance-authority`, `clsx`, `tailwind-merge`. Dev: `vite`, `typescript`, `vue-tsc`, `@vitejs/plugin-vue`, `tailwindcss`, `vitest`, `@vue/test-utils`, `happy-dom` or `jsdom`.

---

## 12. Tests and verification

**Unit/component tests (Vitest, `bun run test`)**. Mock `fetch` with JSON fixtures in `tests/fixtures/` that copy the Phase 2 shapes exactly (ticket with `ref` and `project_key`, list items without `description`, move response with `renumbered`, member with nested `user`, comment author/activity actor with `bot` and `owner_name?`, token with `project`, `auth_user` with `created_at`, list envelope `{items, next_cursor}`, error with `fields`); a typed fixture check fails if `types.ts` drifts.
- API client: JSON/204 handling, `ApiError` parsing (`code`, `fields`, `retryAfter`), network error shape, 503 `unavailable` treated as transient (read retried with backoff, no login redirect), 429 `Retry-After`, 401 `unauthorized` triggers one redirect except on login/signup/`me`, `invalid_credentials` never redirects, `origin_mismatch` toast, `listAll` follows `next_cursor`.
- Router guards: unauth redirect with `redirect` param, redirect sanitization (`//evil`, `/\evil`, `https://x`, `/login`), authed visit to `/login` goes to `/`; `/signup` redirects to `/login` when `signup_enabled` is false, and the login view hides "Create account"; `/auth/me` network failure shows the server-unreachable state, not a login redirect; key normalization replaces `/p/web` with `/p/WEB`.
- Account: profile form calls `PATCH /auth/me` and updates the store; change-password form calls `POST /auth/me/password`, shows 422 field errors (wrong current password does not redirect), toasts on success and keeps the session; client hint rejects < 10 characters; display name hint rejects empty and > 100.
- Comments: edit/delete controls appear for own comments only (while `canEdit`); token comments show the bot badge and no edit control for humans; owners get delete on all; viewers and archived projects get none.
- Filters in URL query restore on reload and disable dragging while active; viewers and non-owners see project settings read-only (editors can manage labels; archived projects read-only labels).
- Board store: load builds ordered columns (position then id) from paginated tickets; the optimistic move updates then rolls back on error (toast called, column keys bumped); filters (search, priority, assignee me/unassigned/member, label) work and combine; summaries without `description` are handled; archived disables `canEdit`; 409 `project_archived` rolls back and reloads.
- Placement: `computePlacement` table test (middle, top, bottom, empty column, moved ticket excluded from neighbors); a drop at the original place sends no request; cross-column and same-column paths send `after`, `before` or `place: 'top'`; moves are serialized FIFO and a queued move with a vanished neighbor is recomputed; 422 on move rolls back and quietly reloads; Status select sends `place: 'bottom'`.
- Inline add: temp card reconciled with the 201 response; the `ticket.created` event arriving before the response yields no duplicate; error restores the text.
- Event handling: `ticket.updated` refetches only the named ticket (assert exactly one `GET /tickets/{id}` and no list call); others' events (including an `api_token` actor) flash, own events do not; `ticket.deleted` removes the card without fetching and closes the panel with a toast (not when mine); `renumbered: true` on `ticket.moved` triggers a full reload; `label.changed` refetches labels and tickets; `member.changed` refetches members, tickets and the project role; "mine" is `actor.type === 'user' && actor.id === me.id`; events during a drag are deferred then applied in order after drop; refresh results older than a local mutation are discarded; coalescing of rapid events for one ticket; a 404 on `refreshTicket` removes the card and does not trigger lost access.
- Live composable (with a fake `EventSource` exposing `readyState`): every open, including the first, triggers one quiet reload (coalesced with an in-flight load, deferred during a drag); `CONNECTING` error sets "Reconnecting" with no probe; `CLOSED` error probes `GET /projects/{key}`: 401 goes to login, 404 runs lost access, other results (including 503) recreate the stream with capped backoff; `member.changed` without membership leads to lost access; `board.leaving` suppresses the lost-access dialog (leave project, delete project).
- Viewer mode: no add button, draggable disabled, panel fields not editable, no Status select.
- Markdown: sanitization cases from section 9, including images dropped and link attributes.
- Token flow: secret shown once and cleared after closing; revoked tokens muted without Revoke button; project limit sends the project ULID.
- Activity humanizer: each action, assignee id resolved through members, labels as name lists, position hidden, unknown action fallback.
- Dist check (`check:dist`): fails on an inline script, an external URL or missing `index.html` (tested against fixture directories).

**Manual end-to-end (exit criteria), against a running Go server with Phases 1-3 (and 4 for the agent check)**
1. Sign up a new user; land on empty state; create a project (also try a duplicate key: inline error); board appears.
2. Add tickets inline in each column; drag within and across columns; reload and verify order persisted; with the keyboard, change a ticket's Status in the panel; apply a filter and confirm dragging is disabled with the hint and the filter survives reload; force a failure (e.g. stop the server, or a viewer in another browser) and verify the rollback toast. With two browsers, drag in one while the other changes the same column and confirm the dragging browser applies it after the drop.
3. Open a ticket via card click and via a pasted deep link (`/p/WEB/t/1`, also `/p/web/t/1` which normalizes) in a fresh tab; a nonexistent `/p/WEB/t/999` shows "Ticket not found"; edit all fields; add, edit and delete a comment; delete a ticket.
4. In project settings, add a second user (by email) as viewer; sign in as them in another browser: board is read-only, no drag or add. Change them to editor: the role change shows without a reload. Archive the project: both boards become read-only with the banner; unarchive. Remove them: their open board shows the lost-access notice and returns to the project list. Re-add, then have them "Leave project" (no spurious lost-access dialog); delete a project as owner while a second member has it open (they get the lost-access notice).
5. Live: with two browsers open, changes appear in the other with a flash; disconnect/restart the server and verify "Reconnecting", then "Live" with a full reload; use an MCP token (Phase 4) or `curl` with a token to create, move and comment on a ticket and see it appear live with the bot badge on the comment.
6. Account settings: change the display name (sidebar updates); change the password with a wrong then a correct current password (field error, then success toast; a second browser's session is signed out, this one stays); create a read token and a write token (one project-limited), copy the secret once, copy the MCP snippet, revoke a token (it shows muted).
6a. With `ALLOW_SIGNUP=false`: login has no "Create account" link and `/signup` lands on `/login`.
6b. Comments: edit and delete your own comment; an agent comment shows the bot badge and has no edit control for you; as owner you can delete it.
7. Log out, expire/clear the session cookie in devtools and click anything: redirected to `/login` with the original path preserved in `redirect`.
8. Light/dark/system toggle persists across reload.
9. Dev mode: with `BASE_URL=http://localhost:5173`, writes succeed through the Vite proxy; with a mismatched `BASE_URL` the `origin_mismatch` toast appears.
10. `bun run typecheck`, `bun run test`, `bun run build` all pass; `web/dist/index.html` and hashed assets exist; `check:dist` passes; `web/dist/.gitkeep` still exists after the build (and after `make web` once Phase 6 lands).

---

## 13. Decisions applied, and what is still open

Decisions applied (from [phases.md](phases.md) "Decisions"):
- Signup: `GET /auth/config` on bootstrap, hide link and redirect `/signup` when off.
- Account: profile form (`PATCH /auth/me`) and change-password form (`POST /auth/me/password`).
- Comments: own comments editable/deletable; agent comments show the bot badge, humans cannot edit them.
- Project routes use the key directly (key or ULID accepted); `GET /projects` still feeds the sidebar.
- Filters in the URL query and disable dragging; non-owners see settings read-only, editors manage labels; any member can leave.
- Status dropdown in the ticket panel is the keyboard alternative; a brief dark-mode flash is accepted.
- Markdown via `markdown-it` (`html: false`) plus DOMPurify; images dropped.
- Comment body cap 20,000 (client hint); label palette gray, red, orange, amber, green, teal, blue, indigo, purple, pink.
- Dev proxy: Go runs with `BASE_URL=http://localhost:5173`.
- SSE: named events, one listener per type; `renumbered` on `ticket.moved` triggers a full reload; label delete reloads labels and tickets; a stream closed by session invalidation ends in a 401 probe and login.
- Archived projects are read-only (409 `project_archived`); ticket list omits `description` (the panel fetches it).
- Ticket key is `ref` with `project_key` everywhere (provisional decision); display name hint is 1 to 100.
- Project list is unpaginated (envelope, `next_cursor` null); projects addressed by key in paths; members keyed by `member.user.id`.
- Event actor is `{type, id}`; "mine" is `type === 'user' && id === me.id`; token actors always flash; `member.changed` reloads members, tickets and project.
- Board reloads on every stream open; CLOSED probes `GET /projects/{key}` (401 login, 404 lost access, else capped-backoff recreate).
- 503 `unavailable` is a transient server error (retry/backoff); 429 `Retry-After` kept.
- Vite build empties nothing automatically (`emptyOutDir: false`), keeps/recreates `web/dist/.gitkeep`; Phase 6 `make web` recreates it too; `base: '/'`; dev at `http://localhost:5173` with `BASE_URL=http://localhost:5173`.
- Account calls match Phase 2: `PATCH /auth/me`, `POST /auth/me/password` (wrong current password is 422 `fields.current_password`).

Resolved against Phase 2 and 3 (compared field by field with Phase 2 section 6): all DTO field names (`ref`, `project_key`, `auth_user`, nested `member.user`, `comment_author` for `author`/`actor` with `bot` and `owner_name`, token `project`, list envelope, move `renumbered`), event payload keys (actor is `{type, id}` only), the move body, the `{token, secret}` creation response, and `/auth/me` returning `{user, auth}`.

Still open: none. Residual assumption: the activity `changes` value shapes follow Phase 1 (assignee as user ids, labels as name lists, description truncated to 200 chars); the humanizer falls back to a generic line for anything else.
