# Agentic phase 3 plan: Board safety gaps

Source spec: [../specs/agentic-phase-3-board-safety.md](../specs/agentic-phase-3-board-safety.md) (cited as S§n). Phase index and resolved decisions: [../specs/agentic-phases.md](../specs/agentic-phases.md). Code rules: [../pabrika/CLAUDE.md](../pabrika/CLAUDE.md). Go commands run from `pabrika/` (the module root).

## 0. Decisions applied (no re-opening)

- Base branch is `dev`. Work branch: `feat/agentic-phase-3-board-safety`, cut from `dev` (not from `feat/agentic-specs`).
- Comment scope value is `comment`: read, add comment, edit own comment. Editor/owner role only; viewer-role owners still get 403 (S§13 Q3).
- Multi-label filter is AND-only, max 10 (S§13 Q5). No OR, no NOT.
- No `claim_ticket` tool. Claim is `move_ticket` with `expected_status` (S§13 Q1). Conflict detail stays in the message (S§13 Q2).
- Label swap is two calls (`add_label`, `remove_label`) (S§13 Q6). Comment tokens show the viewer-capped role (S§13 Q7).
- Scope stays as in the spec's "Out of scope" (S§11): no role-file changes in `agents/`, no web work beyond the token form, no new event types.

## 1. Prerequisites

1. Branch exists: `git switch dev && git pull && git switch -c feat/agentic-phase-3-board-safety` (main agent).
2. Baseline is green before any change: `CGO_ENABLED=0 go test ./...`, `go vet ./...`, `cd web && bun run test && bun run typecheck`. Record failures so they are not blamed on this phase.
3. `sqlc` is installed (`sqlc version`). If not, steps that need `sqlc generate` (A, D) are blocked: install it first.
4. Migration numbering: `migrations/` holds only `00001_init.sql` today, so this phase owns `00002`; phase 4 later takes `00003`. Check `dev` for newer migrations before step C1; renumber if needed.
5. Check the spec's code claims once against `dev` before starting (main agent, S size): `service/tickets.go` Move/Update/List, `authz.go` `checkActor`, `httpapi/middleware.go` scope guard, `mcpserver/auth.go` `toolsFor`, the `once("label")` check. If something differs, fix the plan, not the code, and flag it.

## 2. Parallelism and conflict map

Features: B (CAS on move), A (labels), D (label filter), C (comment scope).

| Shared file | Touched by | Conflict risk |
|---|---|---|
| `service/tickets.go` | B (Move), A (ChangeLabels), D (List) | Low to medium: different functions, but the same file and imports |
| `service/validate.go`, `service/types.go`, `service/errors.go` | B, A, D, C | Medium: appended structs and interface lines |
| `store/queries/tickets.sql` and `store/db/*` | A (3 new queries), D (list predicate) | High for generated files: always regenerate, never hand-merge |
| `mcpserver/tools_tickets.go`, `mcpserver/errors.go`, `mcpserver/tools_test.go` | B, A, D, C | Medium to high: tool-count assertion lines and the tool table |
| `httpapi/tickets_handlers.go` | B (none), A (new handler), D (`once("label")`) | Low |
| `mcpserver/tools.go`, `auth.go`, `server.go` | C (scope field, `toolsFor`), A (count text) | Medium: tool counts and comments (14 to 16, 5 / 7 / 16) |
| `pabrika/README.md`, `CLAUDE.md` | all (docs sweep) | Avoided by doing docs only in the last step |

What can run in parallel (separate git worktrees off the branch, `git worktree add ../pab-phase3-X -b feat/agentic-phase-3-board-safety-X`):
- B and C can be developed in parallel: B has no migration and touches only `Move`; C's authz, migration and web work barely overlap with B. Overlap is limited to `mcpserver/errors.go` and the test files.
- A and D can be developed in parallel with each other, but both need `sqlc generate`. Rule: on merge, take either side of `store/db/*`, then re-run `sqlc generate` and commit the result.
- All four can be written in parallel, but they are merged one at a time in the commit order below (B, A, D, C), rebasing each onto the previous one. Tool-count constants are settled at merge: after A the count is 16 tools and after C there are 5 / 7 / 16 per token scope.

What must be sequential:
- The migration and `sqlc generate` for C (step C1 before C2+), and the `sqlc generate` for A before A's service code compiles.
- Docs sweep (step 7) after all four features.
- The final manual check (step 8) after the docs sweep.
- Rebases and merges (main agent only).

Practical recommendation: one developer or agent doing B, A, D, C in sequence is about 1 to 1.5 days. Use worktrees only if C is wanted early or two executors are available; with one human, the sequential order is safer and has fewer merge steps.

## 3. Steps

Each feature step ends with its tests green and its own commit (S§9 order). Test first in every step: write the failing tests listed in the spec section named in the step, see them fail, then implement. Standard verification for every step: `CGO_ENABLED=0 go test ./...`, `go vet ./...`, plus `cd web && bun run test && bun run typecheck` when web files changed.

### Step 0. Setup and baseline (S)
- Executor: main agent (git).
- Do: prerequisites 1 to 5 above.
- Verify: baseline commands run; git status clean on the new branch.
- Parallel: no.

### Feature B (commit 1): compare-and-set on move

#### B1. Service and REST: `status_conflict` (M)
- Executor: main agent (concurrency and error semantics, S§4).
- Files: `internal/service/errors.go` (`CodeStatusConflict`, `KindConflict`), `internal/service/validate.go` (`MoveInput.ExpectedStatus *Status`, validation, JSON `expected_status`), `internal/service/tickets.go` (`Move`: check after `requireWritable`, `Validate()` and loading `cur`, before anchor resolution), `internal/httpapi/errors.go` (verify the 409 mapping covers the new code; change only if needed), `internal/httpapi/tickets_handlers.go` (decode only if the handler uses its own DTO).
- Test first (`service/tickets_move_test.go`, httpapi test): matching expectation moves; mismatch returns `status_conflict`, no row/position change, no activity row, no event; invalid enum is 422 on `expected_status`; works with `before/after/place`; check runs before anchor validation; same-status reorder with a matching expectation works; the old request bodies still pass unchanged; concurrency test with N goroutines claiming one `todo` ticket: exactly one wins, N-1 `status_conflict`, one `moved` activity row.
- Verify: `CGO_ENABLED=0 go test ./internal/service/... ./internal/httpapi/...`.

#### B2. MCP: `move_ticket.expected_status` (S)
- Executor: opencode for the schema and description edit once B1 is merged; main agent reviews the error text. Or main agent directly (small).
- Files: `internal/mcpserver/tools_tickets.go` (optional enum arg, description text from S§4.3), `internal/mcpserver/errors.go` (`status_conflict` message with ref, expected and actual status; build in the tool's own error branch like `anchor_invalid` if `mapErr` lacks the ref), `internal/mcpserver/schemas.go` if schemas live there.
- Test first (`mcpserver/tools_test.go`): conflict text matches S§4.3; success without `expected_status` unchanged; invalid enum message.
- Verify: `CGO_ENABLED=0 go test ./internal/mcpserver/...` then the full `CGO_ENABLED=0 go test ./...`, `go vet ./...`.
- Commit 1: `feat(service,mcp): compare-and-set on move_ticket via expected_status`.

### Feature A (commit 2): add_label / remove_label

#### A1. Queries and sqlc (S)
- Executor: main agent runs `sqlc generate`; opencode may draft the SQL.
- Files: `internal/store/queries/tickets.sql` (`TicketAddLabelIfAbsent` with `ON CONFLICT DO NOTHING`, `TicketRemoveLabels` with `IN (sqlc.slice(...))`, `TicketTouch` if no equivalent exists), regenerated `internal/store/db/*`.
- Test first: a store-level test for the two queries if the repo tests queries directly; otherwise covered by A2.
- Verify: `sqlc generate` then `CGO_ENABLED=0 go build ./...`.
- Sequential: A2 depends on this.

#### A2. Service `ChangeLabels` (M)
- Executor: main agent (authz and idempotency).
- Files: `internal/service/tickets.go`, `types.go` (interface method), `validate.go` (`ChangeLabelsInput` and `Validate()`: not both empty, max 10 each, overlap, project membership via `tktCheckLabels`, 50-label cap), `internal/service/doc.go` if it lists methods.
- Test first (`service/tickets_labels_test.go`): the full list in S§3.4, including no-op paths (no activity, no event, `updated_at` unchanged), swap in one call, viewer 403, read token `insufficient_scope`, limited token 404, archived 409, one `labeled` activity row with `changes["labels"]`, one `ticket.updated` event, and the concurrent-add test (two goroutines, 50 rounds, both labels kept).
- Verify: `CGO_ENABLED=0 go test ./internal/service/...`.

#### A3. REST `PATCH /api/v1/tickets/{id}/labels` (S)
- Executor: opencode (thin handler, route and DTO, pattern from existing handlers) with the tests listed; main agent reviews.
- Files: `internal/httpapi/tickets_handlers.go`, route registration file (`server.go`), tests in `internal/httpapi/tickets_handlers_test.go`.
- Test first: happy path, 422 with `fields.add`/`fields.remove`, 404, scope 403, archived 409.
- Verify: `CGO_ENABLED=0 go test ./internal/httpapi/...`.

#### A4. MCP `add_label`, `remove_label` and tool counts (M)
- Executor: opencode for the two tool definitions (same shape as `update_ticket`) and count text; main agent for the review.
- Files: `internal/mcpserver/tools_tickets.go` (names go through `c.resolveLabels`; both `write: true`, `idempotent: true`; extend the `update_ticket.labels` description), `mcpserver/server.go` (lines 15 and 19 counts), `mcpserver/auth.go:126` and `tools.go:41` comments, `mcpserver/tools_test.go:526` (`len(names)` 14 to 16).
- Test first: both tools; unknown-name message; tool lists and annotations; a read token does not see them.
- Verify: `CGO_ENABLED=0 go test ./...`, `go vet ./...`.
- Commit 2: `feat(service,rest,mcp): add_label and remove_label without whole-set replace`.

### Feature D (commit 3): multi-label filter

#### D1. SQL and service filter (M)
- Executor: main agent (sqlc empty-slice risk, S§6.2).
- Files: `internal/store/queries/tickets.sql` (`TicketListPage` count predicate), regenerated `store/db/*`, `internal/service/tickets.go` (`TicketFilter.LabelIDs`, merge `LabelID`, de-duplicate, max 10 gives 422 on `label`), `validate.go`.
- Test first: AND of two labels; old single `label` unchanged; label plus status; no match returns an empty page; duplicate ids collapse; 11 ids is 422; cursor pagination over a filtered set returns each ticket exactly once; no-filter case proves the empty-slice path works on SQLite.
- Fallback if sqlc emits bad SQL for an empty slice: fixed-arity nullable args (S§6.2).
- Verify: `sqlc generate`, `CGO_ENABLED=0 go test ./internal/service/... ./internal/store/...`.

#### D2. REST and MCP params (S)
- Executor: opencode (small edits) or main agent.
- Files: `internal/httpapi/tickets_handlers.go` (allow repeated `label`; every other param still rejects repeats), `internal/mcpserver/tools_tickets.go` (`labels` array, max 10; merge with `label`; description text).
- Test first: REST repeated and single param, repeated `status` still 422 with field `status`; MCP `labels`, `label`, both merged, unknown names, schema max items.
- Verify: `CGO_ENABLED=0 go test ./...`, `go vet ./...`.
- Commit 3: `feat(service,rest,mcp): filter tickets by multiple labels (AND)`.

### Feature C (commit 4): comment-only token scope

#### C1. Migration 00002 and token validation (M)
- Executor: main agent (migration, schema).
- Files: `migrations/00002_token_scope_comment.sql` (rebuild `api_tokens` with `CHECK (scope IN ('read','comment','write'))`, explicit column lists, recreate indexes; Down demotes `comment` to `read`), `internal/service/tokens.go` (`validate()` accepts `comment`; message "must be read, comment or write"), `internal/store/store_test.go` (`TestMigrateNewerDB`: 3 `goose_db_version` rows become 4), a new migration test, `sqlc generate` (commit only if output changes). Never edit `00001_init.sql`.
- Test first (S§5.7): Up preserves rows, ids and hashes; accepts `comment`, rejects `admin`; Down demotes; `PRAGMA foreign_key_list` shows no FK to `api_tokens`; tokens created before the migration still authenticate.
- Verify: `CGO_ENABLED=0 go test ./internal/store/... ./internal/service/...`.
- Sequential: must land before C2.

#### C2. Scope model and authz (L)
- Executor: main agent (security and authz, fail-closed).
- Files: `internal/service/actor.go` (`ScopeComment`, `readOnly()`, `canWrite()`, `canComment()`, doc "unknown values are treated as read"), `authz.go` (`checkActor` denies comment actors for general writes with a scope-aware message; `requireProjectComment` variant used by `Comments.Add` and `Comments.Edit` only; `effectiveRole` and `view.go` cap at viewer for display), `comments.go`, `internal/auth/resolver.go` (unknown scope string becomes `read`), `internal/testutil/matrix.go` (comment-scope row).
- Test first: table-driven over every service write method against a comment actor (only `Comments.Add` and own-comment `Comments.Edit` succeed; the rest `insufficient_scope`); another author's comment gives `not_author`; viewer-role owner still 403 `forbidden`; limited token on another project 404; archived 409; unknown scope string behaves as read.
- Verify: `CGO_ENABLED=0 go test ./internal/service/... ./internal/auth/...`.

#### C3. REST guard and token handler (M)
- Executor: main agent for the guard (`Route.CommentOK`, fail-closed); opencode may add token-handler tests afterwards.
- Files: `internal/httpapi/middleware.go`, `server.go` (Route type and flag on `POST /tickets/{id}/comments` and `PATCH /comments/{id}` only), `tokens_handlers.go` (accept `comment`), `view.go`.
- Test first: every write route returns 403 `insufficient_scope` for a comment token except the two flagged routes; route-table test asserts the flagged set equals exactly those two routes; token creation with `comment` works and an invalid scope is 422.
- Verify: `CGO_ENABLED=0 go test ./internal/httpapi/...`.

#### C4. MCP scope per tool (M)
- Executor: main agent for `toolsFor`; opencode for the mechanical `write bool` to `scope` rename across the tool table once the type is defined.
- Files: `internal/mcpserver/tools.go` (`scopeRead/scopeComment/scopeWrite`; `add_comment` and `update_comment` get `scopeComment`; 9 others `scopeWrite`), `auth.go` (`toolsFor`: 5 / 7 / 16), `server.go` (instructions line for comment tokens, tool count text), `errors.go` (scope-aware `insufficient_scope` text).
- Test first: `tools/list` for a comment token is exactly the 7 names; a hidden tool call fails as unknown; `add_comment` works; instruction text; read token unchanged at 5; write at 16.
- Verify: `CGO_ENABLED=0 go test ./internal/mcpserver/...`.

#### C5. Web token form (S)
- Executor: opencode (well scoped).
- Files: `web/src/api/types.ts` (`'comment'` in `SCOPES`), `CreateTokenDialog.vue` (option "Comment (read and comment)", read stays default), `TokensTable.vue`, one component test each.
- Test first: option list and table label tests.
- Verify: `cd web && bun run test && bun run typecheck`.
- Commit 4 (after C1 to C5 are green together): `feat: comment-only token scope (migration 00002, REST guard, MCP tool visibility)`. If the diff is too big for one review, split as C1+C2 (core) and C3+C4+C5 (surfaces), each green by itself, with the REST guard denying all comment writes in the first commit (fail closed).

### Step 7. Docs and count sweep (M)
- Executor: opencode for the count and table edits, listing every place; main agent for review and the workflow spec edit.
- Files: `pabrika/README.md` (line 14 and 117 tool counts, tools table, scopes, REST for `PATCH /tickets/{id}/labels`, `expected_status`, repeated `label`), `pabrika/CLAUDE.md` (one line naming the three scopes and `status_conflict` under Conventions), `pabrika/docs/FINDINGS.md:12`, `pabrika/docs/smoke-test.md:22,114,197`, `specs/agentic-workflow.md` section 11 (mark the four gaps closed, the only edit outside `pabrika/`). Historical `pabrika/specs/` and `plans/` stay as history (S§8).
- Verify: `grep -rn "14 tools\|Tools (14)\|5 read-only" pabrika --include=*.md --include=*.go` finds only the historical files; `CGO_ENABLED=0 go test ./...`, `go vet ./...`, web test and typecheck.
- Commit 5: `docs: tool and scope tables, close workflow section 11 gaps`.

### Step 8. Manual verification and exit check (S)
- Executor: main agent.
- Do: run `serve` locally, then with Claude Code (or an MCP client) check S§10 last bullet: two sessions racing a claim get one winner; a comment token cannot move or edit; `tools/list` for a comment token shows 7 names. Tick every exit criterion in S§10 in the PR description.
- Verify: full gate, `CGO_ENABLED=1 go test -race ./...` only if CGO is available.

## 4. Commit plan

Branch `feat/agentic-phase-3-board-safety` off `dev`. One commit per feature in S§9 order; each is green by itself and includes its tests and the regenerated sqlc output.

1. B: compare-and-set on move
2. A: add_label / remove_label
3. D: multi-label filter
4. C: comment scope (optionally split core / surfaces)
5. Docs and count sweep

Commit messages end with the Co-Authored-By line given in the session attribution. The main agent does all git operations (branching, rebases, commits). No push or PR without the user's request. PR into `dev`.

## 5. Executor summary

| Step | Executor |
|---|---|
| 0, B1, A1 (generate), A2, D1, C1, C2, C3, C4 (`toolsFor`), 8 | main agent |
| B2, A3, A4, D2, C4 (rename), C5, 7 | opencode, then main agent reviews the diff |
| Test runs and builds (every step's verify) | opencode may run them and report; main agent reads failures |

## 6. Risks

| Risk | Mitigation |
|---|---|
| Comment scope lets a comment token write (regression in authz) | Fail-closed defaults (unknown scope is read), table-driven "every write method" test, route-table test asserting exactly two flagged routes (steps C2, C3) |
| Rollback with a binary swap alone: old code treats the unknown `comment` scope as write | Roll back with `goose down` first (demotes to read); state this in the PR; fail-closed code lands in the same commit as the new scope |
| Migration rebuilds `api_tokens`: data loss or lost index | Explicit column lists, recreate `tokens_by_user`, FK check, Up/Down tests with real rows (C1) |
| `store_test.go` migration-count assertion breaks | Update `TestMigrateNewerDB` from 3 to 4 rows in the C1 commit; this is the only allowed edit to an old test besides tool counts |
| sqlc empty-slice predicate misbehaves on SQLite | "No filter" and "one label" tests; fixed-arity fallback (D1) |
| Generated-file merge conflicts between A and D | Always regenerate, never hand-merge `store/db/*` |
| Tool-count assertions drift (14, 16, 5, 7) | Settle counts once at merge time; step 7 grep checks for leftovers |
| `status_conflict` after a timed-out successful claim confuses a client | Tool text says "call get_ticket, do not retry blindly"; message names the actual status |
| Hidden dependency: spec line numbers differ on `dev` | Step 0 check against `dev` before coding |
| Scope creep (claim tool, OR/NOT filter, role files) | Section 0 decisions and S§11; reject in review |

## 7. Rollback

- Before merge: drop the branch. Per feature: `git revert` the feature commit (B, A, D carry no schema changes, so reverts are safe alone).
- C (only schema change): if deployed, run `goose down` for 00002 first (demotes `comment` tokens to `read`), then revert the code. Never roll back by swapping the binary alone. Back up the SQLite file before deploying C (`docs/` backup script).
- A revert of B, A or D leaves existing clients unaffected, because every new input is optional.

## 8. Exit criteria

Same as S§10; the PR description copies that checklist and ticks it after step 8.
