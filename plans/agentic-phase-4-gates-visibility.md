# Agentic phase 4 plan: Gates and visibility

Plans `specs/agentic-phase-4-gates-visibility.md` ("spec §N"). Index and resolved decisions: `specs/agentic-phases.md`. Code conventions: `pabrika/CLAUDE.md`. Style follows `pabrika/plans/phase-5-web-ui.md`. All Go, Bun and sqlc commands run from `pabrika/` (Go) or `pabrika/web/` (Bun).

## 0. Ground rules and decisions in force

- **Base branch is `dev`.** Work on `feat/agentic-phase-4-gates-visibility` off `dev` (see 7).
- **Decisions table: record-only and session-only.** The server never edits labels or columns on a decision; the Orchestrator does, with phase 3's `add_label`/`remove_label`. No MCP write tool for decisions.
- **PR link: option C** (server derives `pr_url`/`branch` from `PR:`/`Branch:` description lines).
- **Notifications: none.** In-app cues only (attention filter, sidebar count). Tab title is an optional last step.
- **Assumptions from the index:** self-approval allowed; label names hard-coded in `service/workflow.go`; `list_activity` shipped only if cheap (step 14, optional); round marker is `[orchestrator] ROUND n/N` (the parser also accepts `Round: n/N` inside an orchestrator comment); migration number is `00003` (phase 3 owns `00002`).
- **Scope guard.** No label tool, no token scope, no auto-merge, no Gate 3 button, no configurable stages, no notification delivery, no role-file rewrites beyond the one-line `workflow.decisions` note.
- **Test first.** For every step: write the failing test, then the code. Go gate: `CGO_ENABLED=0 go test ./...` and `go vet ./...`. Web gate: `bun run test && bun run typecheck && bun run build`.
- **Executors.** `opencode` = simple well-scoped components, table tests, doc sweeps. `main agent` = authz, migrations, decisions semantics, security, git commits. Per CLAUDE.md the main agent reviews every opencode diff before calling a step done.

## 1. Dependency map

```text
S1 contract freeze (main agent)
 |
 |-- BACKEND                               |-- WEB (starts right after S1; mock the API)
 |   [needs phase 3 only where marked]     |
 |                                         |   W-A (no phase 3 needed):
 |   S2 parser (pure)                      |     S8  lib/workflow.ts + lib/roleTag.ts + types + fixtures
 |   S3 migration 00003  [after phase 3    |     S9  card badges, PR chip, round badge, role chips,
 |        lands: number > 00002]           |         attention filter + URL
 |   S4 derive hooks + workflow JSON       |     S10 project feed view/store/route (against mock)
 |   S5 decisions service + REST + SSE     |   W-B (needs S5/S6 shapes only; mock until real):
 |   S6 MCP: workflow + list_decisions     |     S11 GateActions, DecisionCard, panel Workflow block,
 |        [phase 3 tool-count asserts]     |         event handling, humanizer
 |   S7 project feed API (+ S14 optional)  |
 |                                         |
 +-----------------------------------------+--> S12 integration + browser checks (needs all)
                                                S13 docs sweep
                                                S15 optional tab title
```

Parallel groups:

- **P1** (after S1): S2 (backend) || S8 (web libs). Neither needs phase 3.
- **P2**: S3 -> S4 -> S5 -> S6 run sequentially on the backend; S9 and S10 run on the web side in parallel with them. S7 can start once S3 exists (it needs no new table, only `ticket_activity` and `comments`, so its SQL can start before S3 lands, but its route/test numbering waits for phase 3 merge only if it adds a migration, which it does not).
- **P3**: S11 starts when S5 freezes the decisions JSON (already frozen in S1; real wiring waits for S5).
- S12, S13 are the join point.

**Can start before phase 3 lands:** S1, S2, S8, S9 (except nothing), S10, and the SQL/service drafts for S7. **Needs phase 3:** S3 (migration number must be after 00002; confirm with `ls migrations/`), S5/S6 (decisions UX is useful only once the Orchestrator can relabel; gate-label constants and `current` flag read label-change activity rows written by phase 3's `ChangeLabels`, action `labeled`), and the MCP tool-count assertions in S6 (phase 3 sets 5/7/16; this phase makes 6/8/17). If phase 3 slips, S2, S8, S9, S10 and the feed API (S7) still ship; hold S3 to S6 and S11.

## 2. Backend steps

### S1. Freeze the API contract (S, main agent, serial, first)
- Files: this plan section 8 (the frozen contract) plus an unmerged note in the PR; no code. Re-read spec §2, §3, §4, §5, §6 and write the final JSON shapes below.
- Output: section 8 of this plan is the contract. Any change after S1 must be mirrored in both streams.
- Test-first note: n/a; the contract becomes fixtures in S8 and golden files in S4/S5/S7.
- Verify: reviewer (user) skim; `git diff --stat` shows only the plan.
- Estimate: S.

### S2. `service/workflow.go` pure parser (S, opencode)
- Files: new `pabrika/internal/service/workflow.go`, `workflow_test.go`.
- Change: `ParseDescriptionLinks(desc) (prURL, branch *string)` (rules in spec §2: `^(PR|Branch):\s*(\S+)\s*$`, case-sensitive key, first match wins, first 50 lines, CRLF-safe, `pr_url` https only and at most 500 chars, `branch` at most 200 chars, no whitespace); `ParseRoundMarker(body) (n, max int, ok bool)` (accepts `[orchestrator] ROUND n/N` and a `Round: n/N` line; `n <= N`, both positive, N <= 99); gate label constants (`awaiting-req-approval`, `awaiting-plan-approval`, `awaiting-merge`) and the gate to label map.
- Test-first: table tests listed in spec §2/§3.1 (valid, `http:`, `javascript:`, duplicate keys, key mid-line, CRLF, 500+ chars, line 51, `ROUND x/3`, `n > N`).
- Verify: `CGO_ENABLED=0 go test ./internal/service -run 'Workflow|Round'`.
- Estimate: S. Parallel with S8.

### S3. Migration 00003 and sqlc (M, main agent)
- Files: `pabrika/migrations/00003_workflow_derived.sql`, `internal/store/queries/tickets.sql`, `queries/decisions.sql` (new), regenerated `internal/store/db/*`; `store/store_test.go` (`TestMigrateNewerDB` goose-version row count, phase 3 already moved it to 4, this moves it to 5).
- Change: `ALTER TABLE tickets ADD COLUMN pr_url TEXT, branch TEXT, round_n INTEGER, round_max INTEGER` (nullable, derived cache). `CREATE TABLE ticket_decisions` per spec §5 (insert-only; index `(ticket_id, gate, created_at DESC, id)`, index `(project_id, created_at, id)`). Down drops the table and columns (SQLite 3.35+ `DROP COLUMN` works in modernc; if not, rebuild). Queries: set derived columns, insert decision, list decisions by ticket (join tickets, `deleted_at IS NULL`), latest-per-gate, `last gate label applied` lookup against `ticket_activity` label-change rows.
- Test-first: migration test (Up on a DB with data keeps rows, Down reverts, `decision` rows reject a bad `gate`/`verdict` via CHECK).
- Verify: `sqlc generate && CGO_ENABLED=0 go test ./internal/store ./migrations/...`; confirm number with `ls migrations/` first (rename if phase 3 used a different number).
- Estimate: M. Needs phase 3 merged (numbering, goose version count).

### S4. Derive hooks and `workflow` in ticket JSON (M, main agent for service hooks; opencode for views/goldens)
- Files: `internal/service/tickets.go` (Create/Update derive PR/branch inside the write tx, clear when the line is removed), `internal/service/comments.go` (Add/Edit/Delete: bot (`api_token`) authors only; latest orchestrator marker wins; delete clears or falls back to the previous marker), `internal/httpapi/view.go`, `internal/mcpserver/results.go` (`ticketSummary`, `ticketDetail`), `internal/httpapi/testdata/shapes/*.json`, `httpapi/golden_test.go`, `mcpserver/tools_test.go`.
- Change: nullable `workflow {pr_url, branch, round}` (null when all empty); `decisions` added in S5. List items carry `pr_url`, `branch`, `round`.
- Test-first: service tests in spec §2/§3.1 (human round comment ignored, latest wins, edit updates, delete clears, description edit re-derives); golden JSON updated; MCP result tests (no MCP golden exists, add result tests).
- Verify: `CGO_ENABLED=0 go test ./internal/service ./internal/httpapi ./internal/mcpserver`.
- Estimate: M. Needs S2, S3.

### S5. Decisions service, REST, SSE, activity row (L, main agent)
- Files: new `internal/service/decisions.go` (+ interface in `service.go`, `Services`), `service/events.go` (type `decision.recorded`, `DecisionID` field), `internal/httpapi/decisions_handlers.go`, `server.go` (routes: `POST /tickets/{id}/decisions` guarded `SessionOnly` + write limiter; `GET /tickets/{id}/decisions` read scope), `view.go`, `service/errors.go` (`gate_not_open`, `session_required` if missing), `httpapi/errors.go`, tests `service/decisions_test.go`, `httpapi/decisions_handlers_test.go`, `testutil/matrix.go` row.
- Change: per spec §5. Check order: session (also enforced via `Actor` in the service), not-found / project limit, role owner or editor (viewer 403, non-member 404), archived 409, validation (`changes_requested` needs note, max 2,000), then gate-label present else 409 `gate_not_open`. Insert decision and a `ticket_activity` row (`decision`, `{gate: [null, verdict]}`) in the same transaction; publish the event after commit. No update/delete exposed. Readers join tickets and filter `deleted_at IS NULL`. Record-only: no label or status writes.
- Test-first: all Go service/httpapi tests in spec §5 (token actor of any scope rejected, `gate_not_open` after label removal, supersede order, same-tx activity row, event after commit, soft-deleted ticket hidden, archived project).
- Verify: `CGO_ENABLED=0 go test ./internal/service ./internal/httpapi` and `go vet ./...`.
- Estimate: L. Needs S3; label-gating needs phase 3 only for tests that relabel (can use `Update` labels until it lands).

### S6. MCP: `workflow.decisions` and `list_decisions` (M, main agent)
- Files: `internal/mcpserver/tools_tickets.go` (or new `tools_decisions.go`), `results.go`, `tools.go`, `auth.go`, `server.go` (counts and instructions), `mcpserver/tools_test.go`.
- Change: `get_ticket` returns latest decision per gate with `current` (decision `at` later than the last application of the matching `awaiting-*` label per label-change activity row). New read tool `list_decisions(ticket, limit)`. Counts become read 6, comment 8, write 17. No tool can create a decision.
- Test-first: tool-list assertions (exact names per scope), `current` false after the gate label is re-applied, project-limited token isolation, pagination.
- Verify: `CGO_ENABLED=0 go test ./internal/mcpserver`.
- Estimate: M. Needs S5 and phase 3 (tool counts 5/7/16 baseline).

### S7. Project activity feed API (M, main agent for SQL/authz; opencode for handler boilerplate)
- Files: new `internal/store/queries/activity.sql` + sqlc, `service/activity.go` (`ListProject`), `httpapi/activity_handlers.go`, `server.go`, `view.go`, tests.
- Change: `GET /api/v1/projects/{key-or-id}/activity?limit&cursor&kind&actor_type&ticket` per spec §6: `UNION ALL` of `ticket_activity` (joined to project tickets) and `comments`, filter `tickets.deleted_at IS NULL` and `comments.deleted_at IS NULL`, comment body cut to 200, cursor over `(created_at, id)` using `service/cursor.go`, limit default 50, max 200. Any member; session or read-scope token.
- Test-first: cursor stability with equal timestamps, project isolation, non-member 404, filters, truncation, comment edit/delete reflected, soft-deleted ticket/comment excluded.
- Verify: `CGO_ENABLED=0 go test ./internal/service ./internal/httpapi -run Activity`.
- Estimate: M. Needs only S1 (no new table; decisions appear via their activity row once S5 lands). Can run in parallel with S4/S5.

### S14 (optional). MCP `list_activity` (S, opencode, only if cheap)
- Files: `mcpserver/tools_*.go`, `results.go`, `tools_test.go`. Reuses `ListProject`; read tool; count becomes 7/9/18. Skip if S7 shape needs rework.
- Verify: `CGO_ENABLED=0 go test ./internal/mcpserver`. Estimate: S.

## 3. Web steps

### S8. Types, pure libs, fixtures (M, opencode; no phase 3 needed)
- Files: `web/src/api/types.ts` (Ticket `workflow`, `Decision`, `ActivityFeedItem`, event type `decision.recorded` in `EVENT_TYPES`), new `web/src/lib/workflow.ts` (`classifyLabels` -> kind/stage/flags/other; deterministic pick plus conflict flag when two stage labels), new `web/src/lib/roleTag.ts` (`parseRoleTag(body, author)`, bot authors only, regex from spec §3.2), `web/src/api/events.ts` if it lists types separately, `tests/fixtures/*.json` for the frozen shapes, `tests/workflow.spec.ts`, `tests/roleTag.spec.ts`.
- Test-first: Vitest tables in spec §2 and §3.2 (every label, unknown -> other, two `needs-*` deterministic, five roles, each message type, lowercase type rejected, tag not at start rejected, human rejected). Fixture `satisfies` check keeps types in sync.
- Verify: `cd web && bun run test tests/workflow.spec.ts tests/roleTag.spec.ts && bun run typecheck`.
- Estimate: M. Parallel with S2.

### S9. Card badges, PR chip, round badge, role chips, attention filter (L, opencode, main agent reviews)
- Files: new `components/ticket/WorkflowBadges.vue`, `components/common/BotBadge.vue` (optional `role` prop; unchanged without it), `components/board/TicketCard.vue` (stage badge with text and icon, blocked/changes-requested badges, kind tag, PR chip with `new URL()` validation, `rel="noopener noreferrer"`, click does not open panel), `components/ticket/CommentList.vue` (role and message-type chips, strip exactly-first tag line from rendered body), `components/ticket/ActivityList.vue`, `lib/filters.ts`, `composables/useBoardFilters.ts` (`?attention=1`, drag disabled like other filters), `components/board/BoardToolbar.vue`, `components/layout/AppSidebar.vue` (gate count on project entry, client-side from loaded tickets or summary), component tests.
- Test-first: component tests per spec §2/§3: stage text, PR chip href, no chip for bad URL, PR click does not navigate, round badge states (hidden, normal, amber at N-1, red at cap), chips only for bot authors, BotBadge without `role` unchanged, filter restores from URL. Projects with none of these labels render as today.
- Verify: `cd web && bun run test && bun run typecheck`.
- Estimate: L. Develop against fixtures and the mock server; no backend needed.

### S10. Project feed view (M, opencode)
- Files: `api/activity.ts`, `views/ProjectActivityView.vue`, `components/activity/ActivityFeedRow.vue`, `stores/activityFeed.ts`, `composables/useProjectEvents.ts` (subscription; "N new events, show" bar, no layout shift), `router/index.ts` (`/p/:key/activity`, name `project-activity`), `lib/activity.ts` (shared humanizer), `BoardToolbar.vue` link, tests.
- Test-first: row rendering per kind, role chip on agent comment rows, filter query restore, new-events bar under fake SSE, load-more appends without duplicates, empty state text.
- Verify: `cd web && bun run test && bun run typecheck`.
- Estimate: M. Needs only the frozen contract; use mock data until S7 lands.

### S11. Gate actions, decision card, panel Workflow block (L, main agent for gating logic; opencode for visual components)
- Files: `api/decisions.ts`, `components/ticket/GateActions.vue`, `components/ticket/DecisionCard.vue`, `components/ticket/TicketPanel.vue` (Workflow block above description: stage, flags, round, PR, branch with copy button, latest decisions, hint "The Orchestrator applies the next stage", note that merging is done on GitHub), `composables/useCan.ts`, `stores/board.ts` (handle `decision.recorded`: refetch ticket and decisions, same coalescing as `ticket.updated`, flash), `lib/activity.ts` (humanizer `decision`), tests.
- Test-first: buttons only for editor/owner, only with a gate label, not for viewers or archived projects; confirm dialog with optional note; required note for Request changes; success updates panel; remote `decision.recorded` refetches once; 409 `gate_not_open` shows "This ticket moved on" and refetches; humanizer line.
- Verify: `cd web && bun run test && bun run typecheck && bun run build`.
- Estimate: L. Mock until S5 lands; wire to the real API after.

## 4. Join steps

### S12. Integration and browser checks (M, main agent)
- Run the app: `COOKIE_SECURE=false go run ./cmd/pabrika serve` (embedded bundle on 8080) after `cd web && bun run build`; use two browser sessions (editor and viewer) and an MCP/curl write token.
- Checklists (spec §5 browser checklist items 1 to 5 and spec §6 items 1 to 5), plus: card for a ticket with `needs-review` and a `PR:` line shows "Needs review" and a working PR link without opening the panel; editing the description updates the chip live; `javascript:` URL renders no link; orchestrator comment `[orchestrator] ROUND 2/3` updates the card round within one SSE refresh; `[reviewer] CHANGES REQUESTED` from a token shows bot, Reviewer and orange chip while the same text from a human shows none; "Needs my attention" persists through reload.
- Run: `CGO_ENABLED=0 go test ./...`, `go vet ./...`, `cd web && bun run test && bun run typecheck && bun run build`; optional `-race` where CGO exists.
- Estimate: M. Needs S1 to S11.

### S13. Docs sweep (S, opencode; main agent reviews)
- Files: `pabrika/README.md` (REST table for decisions and feed, MCP tool counts and tables, new event, feed, workflow badges), `pabrika/CLAUDE.md` (layout notes for new files; one line on session-only decisions and derived columns), `pabrika/docs/FINDINGS.md` and `docs/smoke-test.md` tool-count mentions, `agents/` one-line "read `workflow.decisions`" edit (coordinate with phase 1/5 owners; do not rewrite roles), `specs/agentic-workflow.md` section 11 rows only if the phase closes a gap (leave others).
- Test-first: n/a; verify tool counts in docs match `tools_test.go` (`grep -rn "Tools (" pabrika/README.md`).
- Estimate: S.

### S15 (optional). Tab title cue (S, opencode)
- Files: `composables/useTabTitle.ts`, board view wiring, test. Title shows "(N) Pabrika" when N gate tickets are pending; no Notification API. Skip unless the phase review wants it.
- Verify: `cd web && bun run test`. Estimate: S.

## 5. Risk list

| Risk | Mitigation |
|---|---|
| Migration number clash with phase 3 | Confirm `ls migrations/` at S3 start; rename file and the version-count assertion if needed. Do not start S3 before phase 3 merges. |
| Token or MCP path creates a decision | Session-only route guard plus service `Actor` check; test that every scope (including owner's write token) gets 403 `session_required`; assert the MCP tool list has no write path. |
| Stale `current` flag lets an old decision approve a re-opened gate | Compare against the last label-change activity row; explicit test with re-applied label. |
| Soft-deleted tickets leak decisions, comments or feed rows | Every reader joins and filters `deleted_at IS NULL`; tests per reader. |
| `javascript:`/`http:` URL rendered as a link | Server drops non-https; UI re-validates with `new URL()` and `rel="noopener noreferrer"`. |
| Spoofed role tag or round marker by a human | Parse role tag only for `bot: true`; server counts only `api_token` authors for rounds. |
| Phase 3 tool-count assertions break | Update 5/7/16 -> 6/8/17 (18 with S14) in the same change as S6. |
| Golden/fixture drift between Go and TS | S8 `satisfies` fixtures plus S4/S5/S7 goldens from the same frozen contract. |
| UNION feed slow on large projects | Index `(project_id, created_at, id)`; check 200-row render under 1 second; fallback (comments into `ticket_activity`) is a separate decision. |
| Hard-coded gate label names | Documented in README; a project without them cannot use decisions. |
| Self-approval allowed | Recorded actor and note in the log; revisit with the user if needed. |

## 6. Rollback note

Per step, revert the commit. The migration `Down` drops `ticket_decisions` and the four derived columns (data loss limited to decisions and the cache; derived columns rebuild from descriptions and comments only if the code is reapplied and tickets are edited, so take a DB copy before running 00003 in production). Roll back with `goose down` plus the previous binary, not the binary alone: an old binary ignores the new columns safely, but an old binary against a downgraded DB is the supported path. Web and feed work is additive and can be reverted independently; the UI hides the Workflow block when `workflow` is null, so an old API with a new UI degrades to today's look except the missing decision buttons.

## 7. Commit plan

Branch `feat/agentic-phase-4-gates-visibility` off `dev`. All commits by the main agent (git commit is never delegated). Small, focused, tests included, tree green at each commit:

1. `docs: add agentic phase 4 plan` (this file; contract section)
2. `feat(service): parse PR, branch and round markers` (S2)
3. `feat(web): workflow and role-tag libs with fixtures` (S8)
4. `feat(store): add workflow columns and ticket_decisions` (S3, after phase 3 merges)
5. `feat: derive workflow object in ticket JSON (REST and MCP)` (S4)
6. `feat(web): workflow badges, PR chip, round, role chips, attention filter` (S9)
7. `feat: record gate decisions (service, REST, SSE)` (S5)
8. `feat(mcp): decisions in get_ticket and list_decisions` (S6)
9. `feat: project activity feed API` (S7) then `feat(web): project activity feed` (S10)
10. `feat(web): gate actions and decision cards` (S11)
11. `docs: README, CLAUDE.md and tool counts for phase 4` (S13)
12. Optional: `feat(mcp): list_activity` (S14), `feat(web): tab title cue` (S15)

Open a PR to `dev` at the end (never to `main`); no push or merge without the user's say-so.

## 8. Frozen API contract (S1 output)

Ticket JSON, REST and MCP (nullable `workflow`):

```text
workflow: {
  pr_url: string|null, branch: string|null,
  round: {n:int, max:int}|null,
  decisions?: [{gate:"requirements"|"plan", verdict:"approved"|"changes_requested",
                note:string|null, at:string, by:{id,name}, current?:bool}]   // detail only
} | null
```

Decision (REST): `{id, ticket_id, gate, verdict, note, actor:{id,name}, actor_role, gate_label, created_at}`. MCP `by` adds `email, role`.

Endpoints: `POST /api/v1/tickets/{id}/decisions` body `{gate, verdict, note?}` -> 201 decision (403 `session_required`, 403 `forbidden`, 404 non-member, 409 `gate_not_open`, 409 `project_archived`, 422 `validation_failed`); `GET /api/v1/tickets/{id}/decisions` -> `{items, next_cursor}` newest first; `GET /api/v1/projects/{key-or-id}/activity?limit&cursor&kind&actor_type&ticket` -> `{items, next_cursor}`, item `{id, kind:"activity"|"comment"|"decision", ticket:{id,ref,title}, actor, action?, changes?, summary?, created_at}`.

Event: `decision.recorded` with `ticket_id`, `decision_id`. Activity action: `decision`. MCP: `list_decisions(ticket, limit)`; optional `list_activity(project, limit, since?, kind?)`.
