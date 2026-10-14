# Agentic Phase 4: Gates and visibility (detailed spec)

Source of truth: [agentic-workflow.md](agentic-workflow.md) (sections 5, 6, 9.7, 11). Phase list and rules: [agentic-phases.md](agentic-phases.md). For anything about the app itself, `pabrika/specs/phases.md` "Decisions" override older specs, and `pabrika/CLAUDE.md` holds the conventions. If this file conflicts with `agentic-workflow.md`, that file wins; flag the conflict instead of deviating.

**Goal:** people can see where agent work stands on the board and run the human gates in the app. The app learns to show the workflow, but it does not run it: the Orchestrator still owns all workflow label changes and stage moves.
**Depends on:** [phase 3](agentic-phase-3-board-safety.md) (add/remove label tool, comment-only token scope, multi-label filter). Nothing here duplicates it: this phase never adds a label tool or a token scope, and reads labels only. The Orchestrator in this phase uses phase 3's label tool to relabel after a decision.
**Out of scope:** configurable stages, real parent/child tickets, a runner, auto-merge, a button that merges a PR (Gate 3 stays a human merge on GitHub), editing workflow rules in the UI, notification delivery (decision note only, section 7).

Marking: **Proposal** = my design, not decided; **Open question** = needs the user. Nothing in this file is a user **Decision** except what `agentic-workflow.md` already records (role tag on comments, labels for sub-stages, three human gates).

---

## 1. Current state (what the code does today)

- Ticket JSON (`ticket`, `TicketSummary` in `web/src/api/types.ts`) has `labels`, `comment_count`, no PR/round fields. **List items omit `description`**, so the card cannot read `PR:` lines today.
- `TicketCard.vue` shows ref, priority, title, label chips (color only), due date, comment count, assignee.
- `BotBadge.vue` is a static "bot" badge; comment authors carry `{type, id, name, bot, owner_name?}`.
- `ticket_activity` (migration `00001_init.sql`) holds per-ticket change rows; `GET /tickets/{id}/activity` is the only reader. There is no project-wide reader. Comments live in `comments`, not in activity.
- Events (`service/events.go`) carry ids only; types are fixed in `EventType`. The SSE route is session-only.
- Approvals today would be a comment starting `APPROVED` (workflow 5.2), checked by the Orchestrator against `list_members`.

---

## 2. Workflow labels and PR link on cards

**Motivation.** The stage is already in the labels, but ten label chips in the same style as `bug` are unreadable, and the PR is buried in a description the card never loads.

**UX.**
- A pure client module `lib/workflow.ts` knows the eleven workflow label names (workflow 5.1: `epic`, `task` and the nine stage and flag labels) and classifies a ticket's labels into: `kind` (`epic`/`task`), `stage` (the one `needs-*` or `awaiting-*`), `flags` (`blocked`, `changes-requested`), and `other`.
- Card: the stage renders as one badge with text and icon (not color only): "Needs dev", "Needs review", "Needs QA", and for the gates "Needs you: approve requirements", "Needs you: approve plan", "Needs you: merge". `blocked` renders as a red "Blocked" badge and `changes-requested` as an orange "Changes requested" badge. `epic`/`task` render as a small kind tag next to the ref. `other` labels render as today.
- Ticket with a PR shows a PR chip (`PR` icon, link text "PR" or "#123" when the URL ends in `/pull/123`) that opens the PR in a new tab (`rel="noopener noreferrer"`); clicking it does not open the panel. Branch name is shown only in the panel, monospace, with a copy button.
- Panel: a "Workflow" block above the description with stage, flags, round, PR, branch, and the latest decisions (section 5).
- Board toolbar: new filter "Needs my attention" shows tickets whose stage is an `awaiting-*` gate label or that carry `blocked`; it uses the existing client-side filter machinery and URL query (`?attention=1`) and disables dragging like other filters.
- Projects with none of these labels look exactly as today (no empty workflow UI).

**PR link: parse vs structured field (comparison).**

| Option | How | Pros | Cons |
|---|---|---|---|
| A. Client parses description | Card reads `PR:`/`Branch:` lines | No backend change | Impossible on cards: list omits `description`. Each card would need a ticket fetch. Rejected. |
| B. Structured fields | New `pr_url`, `branch` ticket columns, settable via `update_ticket` / REST PATCH | Explicit, validated, no parsing | Changes REST, MCP schemas and every role file; a second place to keep in sync with the description; the workflow (4, "Links") already puts the lines in the description |
| C. Server derives from description | On ticket create/update, Go parses the description lines `PR: <url>` and `Branch: <name>` and stores derived read-only columns; exposed as `workflow.pr_url`, `workflow.branch` in ticket JSON (summary and detail) | Roles keep writing the lines they already write; one parser (Go); cards get it free; no MCP input change | Parsing is a convention (first match wins, line must start with the key); a malformed line yields no chip |

**Proposal:** option C. Rules: line match `^(PR|Branch):\s*(\S+)\s*$` (case-sensitive key, first occurrence wins, scanned in the first 50 lines); `pr_url` must be `https://` and at most 500 characters or it is dropped (no `javascript:`, no `http:`); `branch` at most 200 characters and no whitespace. The link is rendered by the UI with `new URL()` validation again. Option B is left as a follow-up (**Open question 1**).

**API.** Ticket JSON gains a nullable `workflow` object (section 4). No new endpoint. The shape is additive, so Phase 2 golden tests need the new key added.

**MCP.** `get_ticket` and `list_tickets` outputs include the same `workflow` object (read only). No tool input changes. MCP uses its own flat result shapes in `mcpserver/results.go` (`ticketSummary`, `ticketDetail`), not the REST view, so the field is added in both `httpapi/view.go` and `mcpserver/results.go`. The existing golden test is `httpapi/golden_test.go` (`TestGoldenShapes`, fixtures in `httpapi/testdata/shapes/*.json`); there is no MCP golden, so add MCP result tests.

**Files.** `migrations/00003_*.sql` (phase 3 takes `00002_token_scope_comment.sql`; use the next free number if phase 3 changes), `internal/store/queries/tickets.sql` and `store/db` (sqlc), `internal/service/tickets.go` (derive on write), new `internal/service/workflow.go` (parser, pure), `internal/httpapi/view.go`, `internal/mcpserver/results.go`; web: `api/types.ts`, new `lib/workflow.ts`, `components/ticket/WorkflowBadges.vue`, `components/board/TicketCard.vue`, `components/ticket/TicketPanel.vue`, `lib/filters.ts`, `composables/useBoardFilters.ts`, `components/board/BoardToolbar.vue`.

**Tests.** Go: table test for the parser (valid, `http:`, `javascript:`, duplicate keys, key in the middle of a line, CRLF, 500+ chars, line 51); derivation updated when the description is edited and cleared when the line is removed; golden JSON updated. Web (Vitest): `classifyLabels` (every label, unknown labels land in `other`, two `needs-*` labels pick a deterministic one and the panel shows a "conflicting labels" hint); card renders the stage text, PR chip href, no chip for bad URL, PR click does not navigate to the ticket; "Needs my attention" filter and URL restore.

**Exit criteria.** A card for a ticket with `needs-review` and a `PR:` line shows "Needs review" and a working PR link without opening it; description edits update it live; invalid URLs never render as links.

---

## 3. Round counter and role tags

### 3.1 Round counter

**Motivation.** The cap (default 3, workflow 5.4) is invisible; people only see `Round: 2/3` by reading comments.

**Note on a source inconsistency.** Workflow 5.4 says the Orchestrator records `Round: n/3` in a comment, while section 6 types it as `[orchestrator] ROUND n/N`. Workflow 5.4 has since been aligned to the section 6 form (alignment pass), so `[orchestrator] ROUND n/N` is canonical. **Proposal:** the parser still accepts the tagged form `[orchestrator] ROUND n/N` as the canonical one and also a line `Round: n/N` inside an orchestrator comment, so either implementation of phase 1 works. Flag to align the workflow text (**Open question 2**).

**UX.** Card and panel show "Round 2/3" next to the stage badge when `round` is set and the ticket carries `changes-requested` or `needs-dev`; the badge turns amber at `n = N - 1` and red with the `blocked` label at the cap. Hidden otherwise.

**Source (Proposal).** Server-derived, because summaries carry no comments: when a comment is added or edited by an `api_token` author and matches the round pattern, the service writes `round_n`, `round_max` on the ticket row (same derivation hook as section 2). It is a derived cache, never authoritative; the comment stays the record. Humans cannot set it (only bot authors count), so a human comment pretending to be a round marker is ignored. Alternative considered: compute from the latest orchestrator comment on every list query (no column, but a correlated subquery per card); rejected for cost, keep as fallback.

**API/MCP.** `workflow.round: {n, max} | null` in ticket JSON, REST and MCP.

**Files.** As section 2 plus the comment service (`internal/service/comments.go`) for the derivation hook.

**Tests.** Go: only bot comments set it; latest wins; editing the marker comment updates it; deleting it clears it; malformed (`ROUND x/3`, `n > N`) ignored. Web: badge states (hidden, normal, amber at N-1, red at cap).

**Exit criteria.** After the Orchestrator posts a round marker, the card shows it within one SSE refresh.

### 3.2 Role tag next to BotBadge

**Motivation.** Every agent comment starts `[developer] PLAN` (workflow 6), but the UI shows only a generic "bot" badge and the raw tag in the markdown.

**UX.**
- `lib/roleTag.ts` parses a comment body: `^\[(orchestrator|researcher|developer|reviewer|qa)\]\s+([A-Z][A-Z ]{1,30})` (the message type is the leading uppercase words, e.g. `PLAN`, `CHANGES REQUESTED`). Only for comments whose author has `bot: true`; a human typing `[developer] APPROVE` gets no tag (anti-spoofing).
- `CommentList.vue`: next to `BotBadge`, a role chip ("Developer"); message type as a second chip (colors: `APPROVE`/`PASS` green, `CHANGES REQUESTED`/`FAIL` orange, `BLOCKED` red, others neutral, always with text). The tag line is removed from the rendered body only when it is exactly the first line (the tag stays in the stored body).
- `BotBadge.vue` gets an optional `role` prop so the chip is part of the badge (one place to style). Token name and owner (`owner_name`) remain visible.
- `ActivityList.vue` and the project feed (section 6) show the same chip for bot actors where a related comment exists (feed rows for comments only).
- A tag that does not match the author's token name pattern is **not** treated as an error; the UI shows what was written. Verifying that `[reviewer]` really came from `reviewer-bot` is the Orchestrator's job and not done here.

**API/MCP.** None; client-side only. The feed (section 6) returns comment summaries and parses on the client with the same module.

**Files.** New `web/src/lib/roleTag.ts`; `components/common/BotBadge.vue`; `components/ticket/CommentList.vue`; `components/ticket/ActivityList.vue`.

**Tests.** Vitest table test for `parseRoleTag` (all five roles, each message type from workflow 6, lowercase type rejected, tag not at start rejected, human author rejected, multi-word type, no type word); component test: chip appears for bot authors only; body renders without the first tag line; BotBadge without `role` renders exactly as before.

**Exit criteria.** A comment `[reviewer] CHANGES REQUESTED` by a token shows "bot", "Reviewer" and an orange "Changes requested" chip; the same text by a human shows none.

---

## 4. Ticket JSON addition (summary)

```text
workflow: {
  pr_url: string | null,       // derived, https only
  branch: string | null,       // derived
  round: {n: int, max: int} | null,
  decisions: {gate, verdict, at, by: {id, name}}[]   // latest per gate, section 5; detail only
} | null                       // null when all of the above are empty
```

Summaries carry `pr_url`, `branch`, `round`. `decisions` appears on `GET /tickets/{id}` and in `get_ticket`, not in lists.

---

## 5. Approve / Request changes as a first-class event

**Motivation.** An `APPROVED` comment is checked after the fact, can be edited or deleted by its author, shares a stream with chatter and relies on every Orchestrator run re-implementing the member check. A recorded decision is explicit, immutable, auditable and verified by the server.

**Data model (Proposal).** New table `ticket_decisions`:

| Column | Notes |
|---|---|
| `id` | ULID |
| `ticket_id` | FK, cascade |
| `project_id` | FK (denormalized for the feed) |
| `gate` | `requirements` or `plan` (Gate 1 and 2). Gate 3 is the merge on GitHub and has no in-app decision |
| `verdict` | `approved` or `changes_requested` |
| `note` | optional text, max 2,000; **required** for `changes_requested` |
| `actor_user_id` | the human; never a token |
| `actor_role` | role at the time (`owner`/`editor`) |
| `gate_label` | the `awaiting-*` label that was on the ticket |
| `created_at` | UTC via `store.FormatTime` |

Rows are insert-only: no update, no delete route. A later decision supersedes an earlier one for the same gate; both stay visible. Tickets are soft-deleted (`deleted_at`), so no FK cascade fires on a normal ticket delete: decision rows stay, and every reader (`get_ticket`, `list_decisions`, the feed) must join the ticket and filter `deleted_at IS NULL`. The FK cascade only applies to a hard delete (project delete).

A decision is also written to `ticket_activity` in the same transaction (action `decision`, changes `{gate: [null, verdict]}`) so existing readers and the project feed (section 6) pick it up without a second source.

**Who may decide (Proposal, consistent with workflow 5.2 / 9.7).**
- Session only: a token (including an owner's token) gets 403 `session_required`, enforced with the existing `SessionOnly` guard and again in the service via `Actor` (no transport can skip it, per `CLAUDE.md`).
- Actor must be a project member with role `owner` or `editor`; a viewer gets 403 `forbidden` and a non-member (or a token limited to another project) gets 404, exactly as `Comments.Add` behaves (`authz.go` `requireProject`). Archived project: 409 `project_archived`.
- The ticket must currently carry the gate's label (`requirements` needs `awaiting-req-approval`, `plan` needs `awaiting-plan-approval`) or the call returns 409 `gate_not_open`. This keeps stale tabs from approving a ticket that moved on. The label names are constants in `service/workflow.go`; a project that does not use them cannot use decisions (**Open question 3**: hard-coded names vs a per-project mapping).
- Self-approval: the user who created the ticket may approve (small teams); not blocked. (**Open question 4**.)

**Does a decision change labels or columns? (comparison)**

| Option | Behavior | Consequence |
|---|---|---|
| A. Record only | The decision is recorded; the Orchestrator reads it and applies label/column changes | Matches workflow 5.1 (only the Orchestrator edits workflow labels), no label-race; the UI shows "Approved, waiting for Orchestrator" until it acts |
| B. Server applies | The service removes the `awaiting-*` label and moves the ticket | Instant feedback; puts workflow rules into Go and breaks the single-writer rule |

**Proposal:** A. The UI shows the decision immediately and a hint "The Orchestrator applies the next stage". The Orchestrator uses phase 3's add/remove label tool afterwards.

**UX.**
- Panel Workflow block, visible only when the ticket has a gate label and the viewer can decide (editor or owner, session, project not archived): two buttons, **Approve** and **Request changes**. Approve opens a confirm dialog naming the gate ("Approve requirements for WEB-30?") with an optional note. Request changes opens a dialog with a required note.
- After submit: toast, the decision appears as a card in the block ("Approved by Ana, 2 minutes ago", note rendered as markdown) and as an entry in the ticket's activity. Other viewers see it live (SSE).
- Viewers and archived projects see the current state but no buttons; token-only users cannot reach the UI anyway.
- The panel keeps a note that merging the PR is done on GitHub, not here.
- Posting a comment that starts `APPROVED` still works (it is only text) but nothing treats it as an approval after phase 5 (**Open question 5**: keep a grace period where the Orchestrator accepts both).

**API.**
- `POST /api/v1/tickets/{id}/decisions` `{gate, verdict, note?}` returns 201 with the decision; errors: 403 `session_required`, 403 `forbidden` for a viewer and 404 for a non-member (same as `POST /tickets/{id}/comments`), 409 `gate_not_open`, 409 `project_archived`, 422 `validation_failed` (note rules, unknown gate/verdict).
- `GET /api/v1/tickets/{id}/decisions` returns `{items, next_cursor}` newest first (cursor pagination like comments); allowed for tokens with read scope.
- Rate limit with the existing write limiter; body cap as other requests.

**MCP (how the Orchestrator reads it).**
- `get_ticket` output gains `workflow.decisions`: the latest decision per gate, each `{gate, verdict, note, at, by: {name, email, role}, current: bool}`. `current` is true only when `at` is later than the last time the matching `awaiting-*` label was applied (taken from the label-change activity row), so a decision from an earlier round cannot approve a re-opened gate.
- New read tool `list_decisions` (`ticket`, `limit`) for the full history. Read scope is enough. **There is no MCP write tool for decisions**: agents cannot approve, which is the enforcement of workflow 9.7 in code instead of by convention.
- Tool counts: after phase 3 a read token sees 5 tools, a comment token 7 and a write token 16. `list_decisions` is a read tool, so this phase makes them 6, 8 and 17 (18 with the optional `list_activity`). Phase 3's exact-count assertions must be updated in the same change. Update the MCP docs and `agents/` role text in the same change as the implementation (the role files themselves belong to phase 1/5 and only need a one-line "read `workflow.decisions`" edit).
- The Orchestrator rule becomes: proceed past Gate 1/2 only when the latest decision for the gate has `verdict: approved` and `current: true`; on `changes_requested` post the note into the Researcher/Developer handoff and relabel.

**SSE.** New event `decision.recorded` with `ticket_id` and a new `decision_id` field on `Event`; actor is the user. The web client refetches the ticket and its decisions (same coalescing as `ticket.updated`) and flashes the card. Events still carry ids only.

**Activity log.** `humanizeActivity` gets the `decision` action: "approved the requirements", "requested changes on the plan: <note truncated to 200>".

**Files.** Go: new migration, `internal/store/queries/decisions.sql` and sqlc output, `internal/service/decisions.go` (+ interface in `service.go`, `Services`), `service/events.go` (type + field), `internal/httpapi/decisions_handlers.go`, route registration in `server.go`, `view.go` (decision view), `internal/mcpserver/tools_tickets.go` (or a new `tools_decisions.go`) and `results.go`; web: `api/decisions.ts`, `api/types.ts`, `api/events.ts` and the duplicated `EVENT_TYPES` list in `api/types.ts` (add `decision.recorded` to both), `stores/board.ts` (event handling), `components/ticket/GateActions.vue`, `components/ticket/DecisionCard.vue`, `components/ticket/TicketPanel.vue`, `lib/activity.ts`, `composables/useCan.ts`.

**Tests.**
- Go service: owner and editor succeed; viewer, non-member, token actor (even owner's, even write scope) rejected; `gate_not_open` when the label is absent or after the label is removed; `changes_requested` without note is 422; insert-only (no update/delete exposed); activity row written in the same transaction; event published after commit; supersede order; `current` flag across a re-applied gate label; decisions of a soft-deleted ticket are not returned by any reader; archived project.
- httpapi: auth matrix rows (session only on POST, read scope on GET), error codes, golden JSON, 404-not-403 for non-members.
- mcpserver: `get_ticket` includes `workflow.decisions`; `list_decisions` paginates; no tool can create a decision (tool list assertion); project-limited token sees only its project.
- Web (Vitest): buttons only for editor/owner and only when a gate label is present; confirm dialog and required note; success updates the panel; `decision.recorded` from another actor refetches once and flashes; 409 `gate_not_open` shows "This ticket moved on" and refetches; viewer sees no buttons; humanizer line.

**Browser checklist.**
1. Ticket with `awaiting-req-approval` (set via MCP/curl): as an editor, open it; Approve and Request changes appear. As a viewer in a second browser: no buttons.
2. Request changes with an empty note: blocked inline; with a note: decision card appears in both browsers without reload; the activity list shows it; the card gets the live flash.
3. Approve: decision shows "Approved by <name>"; via curl with a write token to `POST /tickets/{id}/decisions`: 403 `session_required`.
4. Remove the label through the API, then click Approve in a stale tab: "This ticket moved on", no row created.
5. With an MCP client, `get_ticket` shows the decision with `current: true`; re-apply the gate label and confirm `current: false` until a new decision.

**Exit criteria.** Gate 1 and Gate 2 can be run entirely from the UI; the Orchestrator reads them from `get_ticket`; no code path lets a token create a decision; all tests above pass; `APPROVED`-comment logic is no longer needed by the Orchestrator once phase 1's role text is updated.

---

## 6. Project-wide activity feed

**Motivation.** To see what the agents did overnight today one must open every ticket. Only per-ticket `activity` exists, and comments are not in it.

**UX.**
- New route `/p/:key/activity` (name `project-activity`), linked from the board toolbar ("Activity") and the sidebar project entry. Reachable by any member including viewers.
- A reverse-chronological list, one row per event: time (relative, absolute in tooltip), actor (avatar or `BotBadge` + role chip), ticket ref (link opening `/p/:key/t/:n`) and title, and text from `humanizeActivity`. Row kinds: ticket activity (create, move, field changes, label changes), comments (first 200 characters, tag parsed per section 3.2), decisions (section 5).
- Filters (URL query like board filters): kind (`all`, `agent`, `human`), role (the five), ticket ref, and "gates only" (decisions plus moves to/from `awaiting-*`). Cursor-based "Load more"; no infinite scroll in v1.
- Live: `ticket.*`, `comment.added`, `decision.recorded` events prepend a "N new events, show" bar instead of shifting the list under the reader (no layout jump).
- Empty state: "Nothing has happened in this project yet".

**API (Proposal).** `GET /api/v1/projects/{key-or-id}/activity?limit&cursor&kind&actor_type&ticket` returns `{items, next_cursor}` newest first, `limit` default 50, max 200, same cursor helpers as `service/cursor.go` over `(created_at, id)`. Item: `{id, kind: "activity"|"comment"|"decision", ticket: {id, ref, title}, actor, action?, changes?, summary?, created_at}`. Implementation: a SQL `UNION ALL` of `ticket_activity` (joined to tickets of the project) and `comments` (non-deleted; body truncated to 200 chars). Decisions arrive through their activity row (section 5), so there are only two sources. Tickets and comments are soft-deleted, so the query must filter `tickets.deleted_at IS NULL` and `comments.deleted_at IS NULL` explicitly (`ticket_activity` rows survive a ticket delete). Access: any member, session or token with read scope (agents can read it too). Comment bodies are not leaked beyond what `GET /comments` already allows to the same actor.

Comparison: (a) union query as above; (b) add a `comment` row to `ticket_activity` on every comment (one source, simple pagination) but duplicates data and needs a backfill migration. **Proposal:** (a); (b) is an **Open question 6** only if the union proves slow on large projects.

**MCP.** New read tool `list_activity` (`project`, `limit`, `since?`, `kind?`) so the Orchestrator can catch up after a pause. Optional for v1; ship if cheap (**Open question 7**).

**Files.** `internal/store/queries/activity.sql` + sqlc, `internal/service/activity.go` (add `ListProject`), `internal/httpapi/` new `activity_handlers.go` + route, `view.go`, `internal/mcpserver` tool; web: `api/activity.ts`, `views/ProjectActivityView.vue`, `components/activity/ActivityFeedRow.vue`, `router/index.ts`, `components/board/BoardToolbar.vue`, `components/layout/AppSidebar.vue`, `lib/activity.ts` (shared humanizer), `stores/` (a small `activityFeed.ts`), `composables/useProjectEvents.ts` (feed subscription).

**Tests.** Go: ordering and cursor stability with equal timestamps; members only (non-member 404); project isolation (no rows from other projects); filters; truncation at 200; comment edit/delete reflected; limit caps. httpapi matrix and golden. Web: row rendering per kind, role chip on agent comment rows, filter query restore, "new events" bar behaves under SSE, load-more appends without duplicates.

**Browser checklist.**
1. Create tickets, move, comment as human; as an agent via token (with `[developer] PLAN` comment): the feed shows all, in order, with the Developer chip on the agent row.
2. Filter "agent" then "gates only"; reload: filters persist.
3. With two browsers, an event in one shows the "1 new event" bar in the other; clicking it shows the row at the top.
4. Open a row's ticket link: the panel opens and Back returns to the feed with its scroll and filters.
5. Non-member of another project cannot load its feed (404 page).

**Exit criteria.** One page answers "what happened on this project since yesterday"; agent versus human rows are distinguishable; works for viewers; 200-row feed renders in under a second locally.

---

## 7. Notifications: decision note

Main-spec lists notifications as out of scope and workflow 11 repeats "Human cannot be notified" as a known gap; today the Orchestrator waits for the human in the Claude Code session. This phase builds no delivery. It does add cheap in-app cues (the "Needs my attention" filter and a count of gate tickets on the sidebar project entry, **Proposal**) so people who have the app open see pending gates.

| Option | What | Effort | Notes |
|---|---|---|---|
| 0. None (default) | In-app cues only | none | Matches both specs; a human must open the board |
| 1. Tab title and browser notification | While a board tab is open, `decision`-relevant events (a ticket gets an `awaiting-*` label) change the tab title ("(2) Pabrika") and optionally call the Notification API after the user grants permission | small, web only | No server work; useless when the tab is closed |
| 2. Email | SMTP settings, per-user opt-in, digest | medium; new config and secrets | Needs `SMTP_*` env, templates, bounce handling, deliverability |
| 3. Outgoing webhook | Per-project URL called on `awaiting-*` and `blocked` | medium | Lets users bridge to Slack/Telegram; needs SSRF protection (allowlist, block private ranges) and secret storage |
| 4. Web Push | Service worker plus VAPID keys | large | Works with the tab closed; HTTPS only; heavier operations |

**Recommendation (Proposal):** ship option 0 plus the tab-title part of option 1 in this phase only if the phase review wants it; keep 2 to 4 as separate specs. **Open question 8:** which option, if any, should be planned next? It affects the data model only for options 2 and 3 (per-user or per-project settings), so deciding later is safe.

---

## 8. Task order

1. Migration for derived workflow columns and `ticket_decisions`; sqlc; `service/workflow.go` parser with its unit tests.
2. Derive hooks in tickets/comments service; ticket JSON `workflow`; REST and MCP views; goldens.
3. Decisions service, REST, SSE event, activity row, MCP read tools; tests including the auth matrix.
4. Web: types, `lib/workflow.ts`, `lib/roleTag.ts`, card badges and PR chip, round badge, role chips.
5. Web: `GateActions`, `DecisionCard`, panel Workflow block, event handling.
6. Project feed API, then the feed view, filters and live bar.
7. Attention filter and sidebar count.
8. Docs: `pabrika/README.md` (API/MCP tables, new event, feed), `pabrika/CLAUDE.md` layout notes if files are added, `agents/` one-line edits (owned by phases 1/5, listed here so they are not forgotten).
9. Full pass: `CGO_ENABLED=0 go test ./...`, `go vet ./...`, `cd web && bun run test && bun run typecheck && bun run build`, then the browser checklists above against a running server with an MCP token (phase 5 of the agentic plan reuses them).

---

## 9. Proposals and open questions

**Proposals (need review, not yet decided):** derived `workflow` object from description lines (option C); round counter derived from bot comments; client-side role-tag parsing for bot authors only; `ticket_decisions` table, insert-only, mirrored into `ticket_activity`; session-only, owner/editor-only decisions, label-gated (`gate_not_open`); record-only decisions (Orchestrator relabels); no MCP write tool for decisions; `current` flag on decisions; UNION-based project feed; attention filter and sidebar count.

**Open questions:**
1. Keep option C (derive from description) or move to structured `pr_url`/`branch` fields (option B) for explicitness?
2. Align the round marker format between workflow 5.4 (`Round: n/3`) and 6 (`[orchestrator] ROUND n/N`)? This phase accepts both.
3. Hard-code the workflow label names in Go, or add a per-project label mapping so a project can rename labels?
4. May the creator of a ticket approve their own gate, or must a second human do it?
5. Does the Orchestrator keep accepting `APPROVED` comments for a transition period, or is the decision record mandatory at once?
6. If the feed union is slow on large projects, add comment rows to `ticket_activity` (with backfill)?
7. Ship the `list_activity` MCP tool in this phase?
8. Notifications: option 0 only, or add tab-title/browser notifications now, and which of email/webhook/push should be specced next?
9. Should Gate 3 ever have an in-app marker ("merged, confirmed") recorded as a decision, or stay purely a GitHub fact observed by the Orchestrator?

## 10. Phase exit criteria

- Cards show stage, flags, round and PR link; panel shows the Workflow block; role chips next to the bot badge.
- Gates 1 and 2 are approved or rejected with recorded, immutable, session-only decisions that the Orchestrator reads through MCP; agents cannot create them.
- A project activity feed exists for humans (and agents via MCP if open question 7 is yes).
- Notification decision recorded (open question 8 answered or explicitly deferred).
- All Go and web tests pass; browser checklists done; docs updated; no overlap with phase 3 work.
