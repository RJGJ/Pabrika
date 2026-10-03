# Phase 5 plan: Web UI

Greenfield: when planned, the repo had only specs, no `web/` and no Go code. The plan is based on phases.md (Decisions), main-spec.md "UI", phase-5 (all), phase-2 section 6 (shapes, which match phase-5 section 1 verbatim), phase-3 sections 2 and 5, and the phase-6 build, embed and Dockerfile contract. The phase-5 spec is detailed and self-consistent. This plan orders it into verifiable steps. "§" means a section of `phase-5-web-ui.md` unless prefixed.

## 0. Ground rules

- **Mock first.** Phases 2 and 3 may not exist when this starts. Build against fixtures plus a mock server (WP1), so the UI never blocks on the backend.
- **Test first for logic.** Write the failing Vitest spec, then the code, for `lib/*`, the API client, stores and composables. Component tests are lighter and cover behavior only (§12 lists them).
- **Commands.** Run everything in `web/`:
  - `bun run test`, or a single file with `bun run test tests/x.spec.ts`
  - `bun run typecheck`
  - `bun run build`
  - `bun run check:dist`
  - Target script names: `dev`, `build`, `typecheck`, `test`, `check:dist`. Phase 6 depends on them.
- **Windows.** Use Bun 1.2 or later (text `bun.lock`). The dev shell is Git Bash or PowerShell. Use forward slashes in scripts.
- **Scope guard.** No Go changes, no attachments, no notifications, no i18n, no subpath support. `v-html` appears only in `MarkdownView.vue`.

## 1. Work packages

Estimates are in focused days for one engineer or agent.

### WP0. Scaffold and toolchain (0.5 to 1 day; the main risk is the shadcn/Tailwind setup under Bun on Windows)

The spec has no shadcn step beyond "init", so do this explicitly.

1. Run `bun create vite web --template vue-ts` (or hand-write the files). Add the dependencies from §11. Commit `bun.lock`.
2. Tailwind: use the tool that shadcn-vue's current docs call for. Check the shadcn-vue "Vite" install page first, because the CLI and Tailwind (v3 or v4) pairing changes. Pin the versions in `package.json`.
3. `bunx --bun shadcn-vue@latest init`, then `add` the §11 components:
   - Core: `button input textarea card badge avatar dialog sheet select popover command dropdown-menu sidebar sonner skeleton tabs`
   - Extras: `label separator tooltip alert table checkbox switch`
4. Configure `vite.config.ts` (§10): alias `@`, `server.port 5173`, proxy `/api` to `localhost:8080` with `changeOrigin: true`, `base: '/'`, `build.emptyOutDir: false`. Add the comment about `BASE_URL=http://localhost:5173`.
5. Configure Vitest (jsdom or happy-dom; pick one and use it everywhere; jsdom is the safer default for DOMPurify). Use the `vue()` plugin and set `test.environment`.
6. Add the Vue runtime-only alias. Do not alias `vue` to the full build. Add `scripts/prebuild.ts`, which wipes `dist/*` except `.gitkeep`, and a post-build step that recreates `.gitkeep`.
7. Write a smoke test (`1+1`) and a trivial `App.vue`.

Verify (all must pass):
- `bun install --frozen-lockfile`
- `bun run typecheck`
- `bun run test`
- `bun run build`, with `dist/.gitkeep` still present afterwards
- `bun run dev` serves on 5173

**De-risk:**
- Run `bun run build` and read the emitted `index.html` and chunks at the end of WP0, not at the end of the phase. This catches inline scripts and `eval` early.
- If the shadcn CLI misbehaves under Bun on Windows, run it with `bunx --bun`, or fall back to `npx shadcn-vue@latest`. The generated files are plain source and get committed either way.
- Vite's default `index.html` has no inline script. Do not add one for the theme (CSP, §10).

### WP1. Types, fixtures, API client, mock server (1.5 days)

Can start right after WP0. It is the unblocking package for everything else.

1. `src/api/types.ts` mirrors phase-2 §6 exactly:
   - `Ticket` and `TicketSummary` (summary omits `description`) and `MoveResult` (adds `renumbered`)
   - `AuthUser`, `Member`, `Label`, `Comment`, `CommentAuthor`, `Activity`, `Token`, `ApiEvent`
   - `Envelope<T>`, `ErrorBody`, and the unions `Status`, `Priority`, `Role`, `Scope`, `LabelColor`
2. `tests/fixtures/*.json` hold one JSON per shape, copied from the spec: a ticket with `ref` and `project_key`, a list item without `description`, a move response with `renumbered`, a member with nested `user`, a bot comment with `owner_name`, a token with `project`, an `auth_user` with `created_at`, an envelope, a 422 error with `fields`. Add `tests/fixtures.spec.ts`, a typed check (`satisfies`) that fails when `types.ts` drifts.
3. `api/client.ts` (§5), with a failing spec first for each of these:
   - JSON and 204 handling
   - `Content-Type` only when there is a body
   - `ApiError` parsing (`code`, `fields`, `retryAfter`, and `status 0` with `code 'network'`)
   - 401 `unauthorized` triggers exactly one `handleUnauthorized` (debounced), except on login, signup and the initial `/auth/me`
   - `invalid_credentials` never redirects
   - `origin_mismatch` toast
   - Read retry with capped backoff on 503, network errors and 429 (honoring `Retry-After`). Mutations are never retried. Use fake timers.
   - `listAll` follows `next_cursor` with limit 200 and honors `signal`
   - Inject the 401 callback and toast sink through a small `configureClient({onUnauthorized, onToast})` function. That keeps the client free of store and router imports and avoids import cycles.
4. Resource modules: `auth, projects, members, labels, tickets, comments, tokens`. Thin typed functions, with a test only for the non-trivial ones (`tickets.move` body shape, `tokens.create` sending the project ULID).
5. `api/events.ts`: a small `EventSource` factory taking a `kinds[]` list, so tests can swap in a `FakeEventSource`.
6. Mock server `web/scripts/mock-server.ts`, a Bun HTTP server on 8080, in-memory and driven by the fixtures:
   - Endpoints: auth (including `/auth/config`), projects, members, labels, tickets (list, create, patch, move with midpoint and `renumbered` toggles, delete), comments, activity, tokens.
   - SSE endpoint `/api/v1/projects/:key/events`, emitting named events, `retry: 3000` and keepalive comments. Add `POST /__mock/emit` and `POST /__mock/drop` hooks so a script or curl can inject events (as an agent would) and force reconnect.
   - Honor the origin check against `http://localhost:5173` so `origin_mismatch` can be exercised.
   - Add an optional `bun run mock` script. Keep it out of `dist` and out of the Docker context (`web/scripts` is not shipped, which is fine).
   - Cost: about 0.5 day. It pays for itself in WP4 and WP6, and it is the only way to demo before Phase 2 and 3 exist. If the real backend is already available, drop it and use the real server.

Verify: `bun run test tests/api` and `bun run typecheck` pass. With `bun run mock`, `curl localhost:8080/api/v1/auth/config` returns `{"signup_enabled":true}`.

### WP2. Pure libs (1 day, parallel with WP1 after WP0)

Each lib gets a failing spec first. No Vue dependencies.

- `lib/placement.ts` `computePlacement(idsWithoutMoved, newIndex)` returns `{after}`, `{before}` or `{place:'top'}`. Table test: middle, top, bottom, empty column, and the moved ticket excluded. Add `isNoopDrop(...)`.
- `lib/dates.ts`: local date-only parsing (never `new Date('YYYY-MM-DD')`), `isOverdue` (before today in local time, not `done`), relative time. Test with a fake clock and a non-UTC TZ.
- `lib/markdown.ts` (§9): `markdown-it` with `html:false, linkify:true, breaks:true` and the image rule disabled, then DOMPurify with a tight allowlist, `ALLOWED_URI_REGEXP` for http, https and mailto only, and an `afterSanitizeAttributes` hook setting `target` and `rel`. The test set is the full §9 list:
  - `<script>`, `<img onerror>`, `[x](javascript:alert(1))` (mixed case, entity-encoded, with whitespace)
  - `data:` and `vbscript:` links
  - `<iframe>`, SVG with script, raw HTML
  - `![](http://x/y.png)` dropped
  - normal links keep `rel` and `target`
- `lib/activity.ts`: humanizer. Each action, assignee ids resolved through members (fallback "someone"), label name lists, description shown as "changed the description", `position` hidden, unknown action or field falling back to a generic line.
- `lib/colors.ts`: palette to Tailwind class map for labels and priorities, legible in both modes, unknown falls back to gray.
- `lib/redirect.ts` `safeRedirect(path)`: accepts only a single leading `/`; rejects `//evil`, `/\evil`, `https://x`, `/login` and `/signup`.

Verify: `bun run test tests/lib`. The markdown cases are also the Phase 6 security gate.

### WP3. Auth store, router, guards, shell, login and signup (1.5 days)

Depends on WP1 and WP2 (`safeRedirect`).

1. Store `auth` (§6) with `bootstrap`, `login`, `signup`, `logout`, `updateProfile`, `changePassword` and `handleUnauthorized`, plus stubs for the other stores' `reset`. Specs first:
   - bootstrap: `/auth/me` 401 gives `anon`; a network or 5xx error gives a `unreachable` status that is not `anon`; a config failure assumes `signup_enabled`
   - `handleUnauthorized` redirects once
2. `router/index.ts` (§4):
   - One record for `/p/:key/t/:number?`, so the panel does not remount the board
   - Guards 1 to 4, including 3a (`/signup` redirects when signup is disabled)
   - A "Can't reach the server" full-page state with Retry
   - Key normalization lives in `BoardView`, WP4
   - Router specs with `createMemoryHistory`, covering the §12 list
3. Wire `configureClient` in `main.ts`.
4. `App.vue` and `AppShell`, `AppSidebar`, `UserMenu`, `ThemeToggle` and the `ui` store (theme is applied in `main.ts` before mount, with no inline script). `Sonner` toaster. `projects` store with `fetch`, `byKey`, `create` and `lastProjectKey`.
5. `CreateProjectDialog` (key auto-suggest, auto-uppercase, inline 409 `key_taken` and 422 `fields`). `HomeRedirect` (§4 `/` behavior, localStorage in try/catch). `NotFoundView`.
6. `LoginView` and `SignupView` (§7.1). The "Create account" link is hidden when `signupEnabled` is false.

Verify:
- `bun run test tests/router tests/stores/auth`
- Against the mock: sign up, land on the empty state, create a project, reload. This is E2E step 1 on the mock.

### WP4. Board store and board view without live events (3 days; the highest-risk package)

Depends on WP1, WP2 and WP3. Split into sub-steps, each shippable.

**4a. Board store `load`** (0.5 day)
- Parallel project, members, labels and `listAll` tickets. Atomic replace.
- Sort by position then id into `columns`. `loadState` machine: `not-found` on first-load 404, `no-access` when previously loaded.
- `AbortController` on key change. Quiet load keeps the UI. Coalescing flag `reloadAfter`.
- Specs: ordering across pages, abort, 404 states, summaries without `description`, archived turns `canEdit` off.

**4b. Static board UI** (1 day)
- `BoardView` with key normalization (`router.replace` to the canonical key).
- `BoardColumn`, `TicketCard` (a focusable router-link, overdue text, priority text badge, label chips, avatar, comment count), `BoardSkeleton`, error and Retry states, and the archived banner.
- `useCan`. Viewer-mode checks: no add button.

**4c. Filters** (0.5 day)
- `useBoardFilters` syncs to `?q&priority&assignee&label` through `router.replace`, with a debounced search (200 ms). Unknown values are ignored, and `me` is resolved against the auth user.
- `BoardToolbar` with "n of m" counts and the "Clear filters to drag tickets" hint.
- Specs: URL restore, combination, drag disabled.

**4d. Drag and drop with optimistic move** (1 day). Needs careful handling:
- **Binding.** `vue-draggable-plus` is bound to a local copy of each column's id array. A watcher syncs from the store but is suspended while `board.dragging` is set. A per-column `key` bump forces a re-sync after a rollback or reload (§7.3).
- **Placement.** `onStart` sets `dragging`. `onAdd` and `onUpdate` compute placement with the WP2 function against the target list minus the moved id. A no-op drop sends no request.
- **Optimistic move queue.** Snapshot, apply locally, mark `pendingMoves`, then a FIFO queue. A queued move whose neighbors vanished is recomputed from current local order. On success, replace the ticket from the response; `renumbered` triggers a quiet reload. On failure, restore the snapshot, bump the keys and toast. 422 and 409 `project_archived` also trigger a quiet reload. 404 on the ticket removes it. 404 on the project is the lost-access path.
- **Click versus drag.** Set `delay` and `fallbackTolerance` (and `delayOnTouchOnly`) so a plain click opens the panel.
- Specs, with a mocked `tickets.move`:
  - the store's `moveTicket` path (success, 422 rollback with reload, serialization, recompute of vanished neighbors)
  - a thin component test that fires the Sortable callbacks (call the `onAdd` and `onUpdate` handlers directly, since jsdom has no real DnD)
- Real drag is verified manually in a browser (WP9), and optionally with Playwright or a Chrome-driven check.

**4e. Inline add** (0.5 day). `InlineAddTicket`:
- Temp client id.
- Reconciliation by real id. The `ticket.created` event arriving before the 201 yields no duplicate.
- On error, remove the temp card and restore the text. 422 shows the field message.
- Temp cards are not draggable or openable.

Verify: `bun run test tests/stores/board tests/components/board`, plus E2E step 2 (minus live) on the mock.

### WP5. Ticket panel (2 days)

Depends on WP4 (route-driven `Sheet`). Parts are independent and can be split across agents.

1. Panel shell: `Sheet` driven by the `:number` param; Back closes it; closing preserves the query string; Escape closes; focus return. Cold deep link: find the ticket in the board by number, otherwise `GET /tickets/KEY-n`, and on 404 show "Ticket not found" with a close button. A non-integer `:number` also shows not found.
2. Editable fields:
   - Title (blur or Enter, 200 max, 422 keeps the draft).
   - Markdown description with Write and Preview tabs, explicit Save and Cancel, and a 20,000 counter.
   - Status `Select`, which sends `move` with `place:'bottom'` through the same optimistic path and queue. This is the keyboard alternative to dragging.
   - Priority, due date (nullable), assignee picker (`Popover` and `Command`), label picker with "Create label 'x'" (409 `label_exists` selects the existing label).
   - `PATCH` sends only the changed fields, optimistically, with rollback.
3. Comments: list, add (Ctrl or Cmd+Enter), edit own, delete (author or owner). `BotBadge` with the owner tooltip, and an "edited" mark. The 201 is appended by id. Specs for the control matrix (own, bot, owner, viewer, archived).
4. Activity tab: first page of 50 plus "Load more".
5. Delete with `ConfirmDialog`.
6. "Changed by someone else" draft notice. This depends on WP6 events, so leave the hook in place now and finish it in WP6.

Verify: `bun run test tests/components/ticket` and E2E step 3 on the mock.

### WP6. Live updates (2 days; depends on WP4 and WP5; can be developed against the mock's `/__mock/*` hooks)

1. `board.refreshTicket` with per-ticket in-flight coalescing (a Map of promises and a trailing refetch). A per-ticket mutation counter discards stale results. A 404 removes the card and is not lost-access.
2. `board.applyEvent` table (§8). Specs, asserting calls on the mocked API:
   - `ticket.updated` makes exactly one `GET /tickets/{id}` and no list call
   - `ticket.moved` with `renumbered:true` triggers a full reload
   - `ticket.deleted` removes the card without a fetch and toasts and closes the panel only when not mine
   - `comment.*` refetches comments and activity when the panel is open, and always `refreshTicket`
   - `label.changed` refetches labels and tickets
   - `member.changed` refetches members, tickets and the project role
   - `project.updated` refetches the project and the sidebar list
   - Flash rules: not mine (any `api_token` actor) flashes, mine does not. "Mine" is `actor.type==='user' && actor.id===me.id`.
3. Drag deferral: while `dragging` or a move is pending, events for the dragged (source) column go to `deferredEvents` and flush in order after the queue settles. Quiet reloads are held the same way.
4. `useProjectEvents.ts` (§8):
   - Opens after `ready`, one `addEventListener` per type, and a quiet reload on **every** `onopen`, including the first (Phase 3 §5 relies on this).
   - `onerror` with `CONNECTING` sets "Reconnecting" and does nothing else.
   - `onerror` with `CLOSED` closes the stream and probes `GET /projects/{key}`: 401 goes to the client 401 handler, 404 is lost access, anything else recreates the stream with backoff (3, 6, 12, up to 30 s, with a 429 waiting for `Retry-After`).
   - `board.leaving` suppresses lost-access. Closes on unmount, key change, logout and 401.
   - Tests use a `FakeEventSource` with a settable `readyState`.
5. `LiveIndicator` (`aria-live="polite"`, reduced-motion safe). `LostAccessDialog` (removes the project from the sidebar, then `router.replace('/')` on dismiss).
6. Flash CSS on `TicketCard`, with a reduced-motion guard.

Verify: `bun run test tests/live tests/stores/board-events`. On the mock, restart the server and watch Reconnecting then Live, then use curl against `/__mock/emit` to simulate an agent (E2E step 5).

### WP7. Project settings (1.5 days; independent of WP5 and WP6 after WP4a)

`ProjectSettingsView` uses `board.load(key, {tickets:false})` and the same events composable. It has three tabs.
- **General** (owner edits, others read-only): name, description, archive toggle, and a Danger zone requiring the user to type the key. Delete order: set `leaving`, close the stream, `DELETE`, then reset, refresh the list and go to `/`. On failure, reopen the stream.
- **Members:** table keyed by `member.user.id`, add by email with role (422 `fields.email` and 409 `already_member` inline), role `Select`, remove with confirm (409 `last_owner` message). "Leave project" for any member, with `leaving` set first.
- **Labels:** editors and owners manage them. Viewers and archived projects see them read-only (a 409 is not expected from the UI). Palette color picker, rename and recolor, delete with confirm.

Verify: component specs for the read-only matrix (owner, editor, viewer, archived) and for leave and delete suppressing the lost-access dialog. E2E step 4.

### WP8. Account settings (1 day; independent after WP3)

`ProfileForm` (`PATCH /auth/me`; the sidebar updates). `ChangePasswordForm` (422 `fields.current_password` never redirects; the client hint is at least 10 characters; success toast and cleared fields). `TokensTable` with revoked rows muted and no Revoke button. `CreateTokenDialog` (read is the default scope; sends the project ULID). `TokenSecretDialog` with copy; the secret lives in memory only and is cleared on close. `McpSnippet` using `window.location.origin` plus the dev note, and "fill in the just-created token" (in memory only).

Verify: specs for the token flow (secret cleared after close, project limit sends the ULID, revoked muted) and the account forms. E2E step 6.

### WP9. Dist check, build hardening, a11y, final E2E (1.5 days)

1. `scripts/check-dist.ts` (§10), test-first against fixture directories under `tests/fixtures/dist-*/`. It exits non-zero when:
   - `index.html` is missing
   - there is an inline `<script>` (a script tag without `src`) or an inline event handler attribute
   - any external `http(s)://` URL is referenced for a script, stylesheet, font or image
   - `assets/` has no hashed files
   - emitted JS contains `eval(` or `new Function(` outside a recorded allowlist
   - It ignores `.gitkeep`.
   - **Risk:** libraries such as Vue and DOMPurify may contain `new Function` or `eval(` strings. Run it on the real bundle, and add justified allowlist entries keyed by file and snippet. If Vue's runtime build is clean, the allowlist stays small. Note that `http://` strings inside library code (for example SVG namespace URIs `http://www.w3.org/2000/svg`) must not trip the external-URL rule. Scope that rule to `src`, `href` and `url()` references, not to any string in JS. This is an interpretation gap in §10, so settle it when writing the script.
2. `bun run build` = `vue-tsc --noEmit`, prebuild clean, `vite build`, `.gitkeep` restore, then `check:dist`.
3. Run `grep -rn "v-html\|innerHTML" web/src` and confirm only `MarkdownView.vue` matches (Phase 6 gate). Add this as an automated spec.
4. Accessibility pass (§10): focus return, dialogs, the live region.
5. Full manual E2E (§12 steps 1 to 10) against the real Go server (`BASE_URL=http://localhost:5173 COOKIE_SECURE=false go run ./cmd/pabrika serve`) with two browser profiles. Include `ALLOW_SIGNUP=false` (6a) and the origin-mismatch check (9).

## 2. Dependency graph and parallelism

```
WP0 ─┬─ WP1 (types, client, mock) ─┬─ WP3 (auth, router, shell) ─┬─ WP4 (board) ─┬─ WP5 (panel) ─ WP6 (live) ─┐
     └─ WP2 (pure libs) ───────────┘                             ├─ WP7 (settings)* ──────────────────────────┤ WP9
                                                                 └─ WP8 (account) ────────────────────────────┘
```

- WP1 and WP2 run in parallel right after WP0. Each is test-driven and has no UI.
- WP7 and WP8 can start once WP3 and WP4a land. They share only the stores and the shell.
- WP5 and WP6 are sequential, but the WP6 store and composable tests (no UI) can start as soon as 4a and 4d exist.
- WP7 needs the events composable only for its final wiring. Stub it until WP6 lands.
- Suggested split for two agents: A takes WP1, 3, 4, 5, 6. B takes WP2, then WP7, WP8, then the WP9 tooling.

## 3. Definition of done (mapped to the exit criteria)

Phase exit criterion 1 (core flows end to end against a running server):
- Manual E2E §12 steps 1 to 9 pass.
- Steps 1 to 4 cover sign up, create project, add member, drag.
- Step 5 covers an agent's change appearing live with the bot badge. Use Phase 4 MCP or curl with a token.
- Also required: 6a (signup off), 6b (comments), 7 (401 redirect with `redirect` preserved).

Phase exit criterion 2 (`bun run build` produces the bundle Phase 6 embeds):
- Step 10: `bun run typecheck`, `bun run test` and `bun run build` pass.
- `web/dist/index.html` and `web/dist/assets/*-<hash>.*` exist.
- `check:dist` passes and `web/dist/.gitkeep` survives.
- The `bun.lock` in the repo is text format (Bun 1.2 or later).
- The scripts are named exactly `dev`, `build`, `typecheck`, `test` and `check:dist`.
- Router paths match the phase-6 table (`/`, `/login`, `/signup`, `/p/:key`, `/p/:key/t/:number`, `/p/:key/settings`, `/settings`, and not-found).
- Vite proxies only `/api`.
- No inline scripts, no `eval` and no external URLs.

All §12 unit and component test families have an owner in the WP steps above. Add `CLAUDE.md` web commands, since Phase 6 updates it, and note the dev command there.

## 4. Risks and de-risking

| Risk | Mitigation |
|---|---|
| Drag and drop placement and rollback (the main correctness risk) | Keep the pure `computePlacement` function (WP2) tested to the table. Isolate Sortable from the store through local list copies and a `key` bump. Serialize moves in a FIFO queue. Defer events and reloads while dragging. Test the store logic headlessly (jsdom cannot drag), then do a real two-browser drag in WP9. Consider a small Playwright or Chrome-driven smoke check if time allows. |
| Drop onto the original place or a stale-neighbor race | A no-op check, recompute at dequeue, and 422 gives rollback plus a quiet reload. |
| SSE reconnect and reload | `FakeEventSource` tests for every `onerror` branch. Reload on every open, coalesced and deferred. A single reconnect owner: the browser handles `CONNECTING`, our backoff handles only `CLOSED`. Use the mock's `/__mock/drop` to exercise it by hand. Phase 3's "no replay" guarantee depends on the first-open reload, so test it explicitly. |
| Shadcn-vue and Tailwind under Bun on Windows | Do WP0 first and timebox it. Pin the versions. If the CLI fails, hand-copy generated components. Check the current docs instead of assuming the Tailwind v3 and v4 setup. |
| CSP-compatible build and `check:dist` false positives | Build and inspect at the end of WP0. Run the runtime-only Vue build. No inline theme script, so a dark-mode flash is accepted. Scope the URL check to resource attributes and keep an explicit allowlist. Fixture tests for the checker. |
| `emptyOutDir:false` leaves stale hashed files | The prebuild clean script (not Vite) removes everything but `.gitkeep`. Add a test or script check that `dist/assets` holds only the current build's files. |
| `v-html` or `innerHTML` creeping in | An automated grep spec. Markdown goes through a single sanitized function. |
| Types drifting from the real API when the backend lands | Typed fixtures check. When Phase 2 exists, run a contract smoke script against it, hitting each endpoint and validating key sets against the fixtures. |
| Dev origin mismatch | Document `BASE_URL=http://localhost:5173` and open exactly `localhost`, not `127.0.0.1`. The `origin_mismatch` toast makes this visible. |
| Browser HTTP/1.1 connection limit (about 6 per origin) in dev | Phase 3 §5 documents it. Keep one `EventSource` per tab, and close it when leaving the board. |

## 5. Size estimate

About 16 to 18 focused days for one engineer (WP0 1, WP1 1.5, WP2 1, WP3 1.5, WP4 3, WP5 2, WP6 2, WP7 1.5, WP8 1, WP9 1.5, plus about 1 day of integration slack and mock server time). With two parallel agents, about 9 to 11 elapsed days. The two largest unknowns are the drag and drop details (WP4d) and the shadcn setup (WP0).

## 6. Open questions

No blockers. The spec states "Still open: none", and the planner found no conflict with phases.md Decisions or phase-2 section 6. Four small items the specs leave to the implementer, with a recommended default for each:

1. **Tailwind and shadcn-vue version pairing** (v3 or v4). The specs only say "Tailwind". Use whichever the current shadcn-vue docs require.
2. **`check:dist` external-URL rule scope.** §10 says no `http(s)://` URL for a script, stylesheet, font or image. Library JS contains namespace URIs. Check resource references only (HTML attributes and CSS `url()`), not arbitrary strings.
3. **jsdom or happy-dom.** The spec allows either. Choose one (jsdom is the safer default for DOMPurify) and run all tests under it.
4. **Mock server.** The specs do not mention one. Recommended as above, and optional if the real Phase 2 and 3 backend is ready when WP1 starts.

One residual assumption, already flagged in §13: the shapes of the activity `changes` values follow Phase 1, and the humanizer falls back to a generic line for anything else.

### Critical files for implementation
- `web/vite.config.ts`
- `web/src/api/client.ts`
- `web/src/stores/board.ts`
- `web/src/composables/useProjectEvents.ts`
- `web/scripts/check-dist.ts`
