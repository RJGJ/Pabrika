# Agentic Phase 3: Board safety gaps (detailed spec)

Source of truth: [agentic-workflow.md](agentic-workflow.md) (sections 3, 9, 11). Phase split and rules: [agentic-phases.md](agentic-phases.md). Code rules: [../pabrika/CLAUDE.md](../pabrika/CLAUDE.md); `pabrika/specs/phases.md` Decisions override older specs (they are respected below: scope and project limit live in the service through `Actor`, events are published by the service after commit, REST and MCP share inputs and `Validate()`).

Status: draft. Items marked **Proposal** are my design; items under "Open questions" need the user's decision. If this file conflicts with the workflow spec, the workflow spec wins; flag it.

**Goal:** remove the four unsafe workarounds in workflow section 11 by changing the Go app and MCP server: label add/remove without whole-set replace, compare-and-set on status, a comment-only token scope, and a multi-label filter. No new product features beyond these four. No web UI work except the token form (UI for gates is phase 4).

## 1. Current behaviour (verified in code)

| Area | Today | File |
|---|---|---|
| Labels | `update_ticket` / `PATCH /tickets/{id}` with `labels` replaces the whole set: read old set, `TicketClearLabels`, re-insert. Activity action `labeled` when labels are the only change; event `ticket.updated`. | `service/tickets.go` Update |
| Status change | `Move` reads the current row and writes status+position. There is no precondition, so two actors both "claim" a `todo` ticket and the last write wins. Writes are serialized by the single SQLite write connection, so a check inside the same `write` transaction is atomic. | `service/tickets.go` Move |
| Scope | `Scope` is `read` or `write`; `api_tokens.scope` has `CHECK (scope IN ('read','write'))`. Read-only is enforced in `authz.go` (`checkActor`, `effectiveRole`), in the REST guard (`httpapi/middleware.go`, `p.Token.Scope == ScopeRead`), and in MCP tool visibility (`mcpserver/auth.go` `toolsFor`: read tokens see the 5 read tools). | see left |
| Label filter | One label: `TicketFilter.LabelID`, `list_tickets.label` (name), REST `label=<id>`; SQL `EXISTS` on one id. | `store/queries/tickets.sql` TicketListPage |

## 2. Cross-cutting rules

- Business rules, authz and validation go in `service`; `httpapi` and `mcpserver` stay thin (CLAUDE.md rule). Order of checks stays as in `authz.go`: session, not-found, project limit, scope, role, archived, validation. New checks slot in as noted per feature.
- New error code `status_conflict` (`KindConflict`, REST 409) in `service/errors.go`; nothing else new.
- Backwards compatibility is a hard rule: every existing REST request, MCP call and stored token keeps working unchanged. All new inputs are optional; the only schema change is widening a CHECK constraint.
- Tool count: 14 becomes 16 (`add_label`, `remove_label`, both write). Update every place that states 14/5/9 (see 8).

## 3. Feature A: add_label / remove_label

Motivation (workflow 11, row "update_ticket replaces whole label set (race)"; 3 note): two actors doing read-modify-write on labels can drop each other's change, so only the Orchestrator may touch labels today. Targeted add/remove removes the race and lets Developer/Reviewer/QA roles own their own transition labels later (that role change belongs to phase 1, not here).

### 3.1 Service

New method on `TicketService` (**Proposal**, one primitive used by both transports):

```go
ChangeLabels(ctx, actor Actor, ref string, in ChangeLabelsInput) (Ticket, error)

type ChangeLabelsInput struct {
    Add    []string // label ULIDs
    Remove []string // label ULIDs
}
```

- Auth: `resolveTicket(..., RoleEditor, write=true)` then `requireWritable` (same as `Update`). Then `Validate()`.
- Validation (`ChangeLabelsInput.Validate`, field errors keyed `add` / `remove`): at least one of add/remove non-empty; each list at most 10 ids; ids normalised with `tktNormalizeIDs`; an id in both lists is `labels: a label cannot be in both add and remove`; `add` ids must belong to the ticket's project (`tktCheckLabels`); after the change the ticket must have at most `MaxLabelsPerTicket` (50) labels.
- Apply inside the one write transaction: `INSERT ... ON CONFLICT DO NOTHING` for adds (new query `TicketAddLabelIfAbsent`), `DELETE ... WHERE ticket_id=? AND label_id IN (...)` for removes (new query `TicketRemoveLabels`). Removing a label not on the ticket and adding one already on it are no-ops, not errors (idempotent; a retry after a timeout is safe).
- No-op overall (nothing actually changed): return the ticket, no activity row, no event, no `updated_at` bump (matches `Update`'s no-change path).
- Otherwise bump `updated_at` (`TicketTouch` query), write activity, emit event.
- Activity: one `ticket_activity` row, action `labeled`, `changes["labels"] = [oldNames, newNames]` (same shape as today, so the existing activity renderer works). No new action or key.
- SSE: `ticket.updated` for the ticket (existing type; no new event type, so web code needs no change).
- Add label removal of a deleted label: not applicable (label delete cascades `ticket_labels`).

### 3.2 REST

`PATCH /api/v1/tickets/{id}/labels`, `Authed`, write. Body `{"add": ["<label id>"], "remove": ["<label id>"]}` (ids, like `labels` in `PATCH /tickets/{id}`). Response `200` with the full ticket JSON (same view as the other ticket endpoints). Errors: 404 `not_found`, 403 `insufficient_scope` / `forbidden`, 409 `project_archived`, 422 `validation_failed` with `fields.add` / `fields.remove`. File: `httpapi/tickets_handlers.go` (+ route line, request DTO in `httpapi`).

### 3.3 MCP

Two tools in `mcpserver/tools_tickets.go`, both `write: true`, `idempotent: true`.

| Tool | Args | Result | Description text (short) |
|---|---|---|---|
| `add_label` | `ticket` (required), `labels` (required, array of label names, 1 to 10) | ticket summary (same as `update_ticket`) | "Add labels to a ticket without touching its other labels. Labels must already exist in the project (create_label). Adding a label the ticket already has is not an error." |
| `remove_label` | same | ticket summary | "Remove labels from a ticket without touching its other labels. Removing a label it does not have is not an error." |

Resolution reuses `c.resolveLabels(project, names)` (case-insensitive, reports all unknown names with the "Labels: ..." available list). Error text for the overlap case comes from `mapErr` (validation). `update_ticket.labels` description is extended: "replaces the full label set; prefer add_label / remove_label". Behaviour of `update_ticket` itself is unchanged.

### 3.4 Tests

- `service/tickets_labels_test.go`: add to empty set; add keeps other labels; remove one of several; add existing is a no-op (no activity, no event, `updated_at` unchanged); remove missing is a no-op; add and remove in one call (swap); overlap is 422; label from another project is 422; 11 ids is 422; 51st label is 422; viewer 403; read token 403 `insufficient_scope`; project-limited token on other project 404; archived project 409; activity row shape and one `ticket.updated` event on change, none on no-op.
- Concurrency: two goroutines add different labels to one ticket 50 times; final set contains both labels every round (this is the bug the update-replace path has).
- `httpapi/tickets_handlers_test.go`: PATCH labels happy path, 422 fields, 404, scope.
- `mcpserver/tools_test.go`: both tools, unknown name message, tool list lengths and annotations, read token does not see them.

## 4. Feature B: compare-and-set on status (and claim)

Motivation (workflow 9.8 and 11, "No atomic claim"): the Orchestrator is the single assigner only to avoid two agents picking the same ticket. A precondition on move makes a claim safe without that convention.

### 4.1 Service

`MoveInput` gains `ExpectedStatus *Status` (JSON `expected_status`, optional). In `Tickets.Move`, after `requireWritable`, `Validate()` and loading `cur`, and **before** anchor resolution or any write:

```text
if in.ExpectedStatus != nil && Status(cur.Status) != *in.ExpectedStatus  ->  conflict
```

- New error: `NewError(KindConflict, CodeStatusConflict, "Ticket is in <actual>, not <expected>")`. The actual status is in the message (REST body has only `code` and `message` today; adding a structured field is Open question 2).
- `Validate()`: `expected_status` must be a valid `Status` (422 field `expected_status`).
- Atomic because the check and the write share one `write` transaction on the single write connection. No new SQL needed; no change to locking.
- The check also applies when the target equals the current status (a pure reorder with `expected_status` set).
- Retrying a successful claim returns `status_conflict` (the ticket is now in the target column). Agents must `get_ticket` after a conflict or timeout instead of blindly retrying; the tool text says so.
- A conflict writes no activity and emits no event.

### 4.2 REST

`POST /api/v1/tickets/{id}/move` accepts `expected_status`. On mismatch: `409 {"error": {"code": "status_conflict", "message": "Ticket is in in_progress, not todo"}}`. Other behaviour unchanged. Files: `service/validate.go` (`MoveInput` struct and its `Validate()` live here; `service/types.go` only has the `Move` interface method), `service/tickets.go`, `service/errors.go`, `httpapi/errors.go` (confirm 409 mapping covers the new code; `KindConflict` already maps).

### 4.3 MCP

- `move_ticket` gains optional `expected_status` (enum of statuses). Description addition: "Pass expected_status to claim safely: the move only happens if the ticket is currently in that column, otherwise you get a conflict and nothing changes. After a conflict, call get_ticket; do not retry blindly."
- `mapErr` (`mcpserver/errors.go`): `status_conflict` becomes `Ticket WEB-12 is no longer in todo (now in in_progress). Someone else probably picked it up. Call get_ticket to see its current state; nothing was changed.` Needs the ref: pass through `errOpts` or build in the tool's own error branch like `anchor_invalid`.
- **Proposal:** no separate `claim_ticket` tool in this phase. `move_ticket` with `expected_status` covers the workflow's claim (todo to in_progress) and keeps the tool count small. See Open question 1 for adding a sugar tool.

### 4.4 Tests

- Service: matching expectation moves; mismatch returns `status_conflict` and changes nothing (row, position, activity count, events); invalid enum is 422; works with `before/after/place`; check runs before anchor validation; same-status reorder with matching expectation works.
- Concurrency: N goroutines claim one `todo` ticket with `expected_status: todo`; exactly one succeeds and N-1 get `status_conflict` (run with `CGO_ENABLED=0`, plus once with `-race` where CGO exists); exactly one `moved` activity row.
- REST and MCP: 409 shape, MCP text, `expected_status` without value keeps old behaviour (backwards compatibility test: the old request bodies in existing tests still pass untouched).

## 5. Feature C: comment-only token scope

Motivation (workflow 9.1 and 11, "Token scope is read or write only"): Code Reviewer and QA Tester only need to read and comment, but a `write` token also lets them edit, move or delete tickets. Today only the tool list in the subagent file stops them (second layer); the token should be the first layer.

### 5.1 Semantics (**Proposal**)

New scope value `comment` (name: Open question 4). A `comment` token may:

- do everything a `read` token can;
- `Comments.Add` (add a comment) and `Comments.Edit` for its own comments (author check unchanged), in projects where its owner is editor or owner (role rule unchanged; Open question 3 on viewers).

It may not: create/update/move/delete tickets, labels or projects, delete comments, or use any session-only route. Scope never raises role; the owner's current role still applies on every call. Order of checks in `authz.go` keeps "scope before role" (step 3).

### 5.2 Data and migration

SQLite cannot alter a CHECK, so `migrations/00002_token_scope_comment.sql` (goose) rebuilds `api_tokens`:

```text
-- +goose Up
CREATE TABLE api_tokens_new (... same columns ...,
  scope TEXT NOT NULL CHECK (scope IN ('read','comment','write')), ...);
INSERT INTO api_tokens_new SELECT * FROM api_tokens;   -- explicit column list in the real file
DROP TABLE api_tokens;
ALTER TABLE api_tokens_new RENAME TO api_tokens;
CREATE INDEX tokens_by_user ON api_tokens (user_id);   -- recreate dropped index
-- +goose Down
-- demote comment tokens to read (least privilege), then rebuild with the old CHECK
```

Nothing has a foreign key to `api_tokens` (comments and activity store `actor_type/actor_id` as plain columns), so no `foreign_keys` pragma juggling is needed; verify with `PRAGMA foreign_key_list` in the migration test. Existing tokens, hashes and ids are preserved. Run `sqlc generate` (models keep `Scope string`; commit the regenerated output only if it changes). Never edit `00001_init.sql`.

### 5.3 Service changes (`actor.go`, `authz.go`, `tokens.go`, `comments.go`)

- `actor.go`: add `ScopeComment Scope = "comment"`. Replace the bool helpers with intent-revealing ones, **fail closed**: `readOnly()` stays true for `read`; add `canWrite()` (scope is write or empty, as today) and `canComment()` (comment, write, empty). Doc comment on `Scope` updated: "unknown values are treated as read".
- `authz.go`: `checkActor(actor, write)` keeps meaning "general write": a `comment` actor fails it with `insufficient_scope` (message depends on scope: "This token can only read and comment"). Add `requireProjectComment(...)` (a variant of `requireProjectWrite` that uses `canComment()` for the scope step) and use it only in `Comments.Add` and `Comments.Edit`; `resolveComment`/`resolveTicket` get the variant through a parameter or a small wrapper so the choke point stays single. `effectiveRole`: read tokens still cap at viewer; comment tokens are not capped on comment operations (they are denied elsewhere by scope), but are capped at viewer when reported (view.go) because they cannot edit.
- `tokens.go`: `validate()` accepts `read`, `comment`, `write`; message "must be read, comment or write".
- `Comments.Delete`, all ticket/label/project writes: unchanged code path, rejected by `checkActor`.

### 5.4 REST and auth

- `httpapi/middleware.go` guard: today `write && Scope == ScopeRead` rejects. Change to: for a write route, reject unless scope is write, or scope is comment and the route is flagged comment-allowed. Add `Route.CommentOK bool`; set it on `POST /tickets/{id}/comments` and `PATCH /comments/{id}` only. Unflagged write routes fail closed. Message and code stay `insufficient_scope`.
- `httpapi/tokens_handlers.go` accepts `scope: "comment"`; `view.go` token and `/auth/me` views already pass the scope string through; role display for comment tokens as in 5.3.
- `auth/resolver.go` passes scope through; add a defensive mapping: unknown string becomes `read`.

### 5.5 MCP

- `toolDef.write bool` becomes `scope` (`scopeRead`, `scopeComment`, `scopeWrite`) on each tool: `add_comment` and `update_comment` are `scopeComment`; the other 7 existing writes (`create_project`, `update_project`, `create_label`, `create_ticket`, `update_ticket`, `move_ticket`, `delete_ticket`) plus the 2 new label tools are `scopeWrite`. Totals: 5 read + 2 comment + 9 write = 16.
- `toolsFor` (`mcpserver/auth.go`): read token sees 5; comment token sees 5 + `add_comment` + `update_comment` = 7; write token sees 16. Hidden tools are not registered, so `tools/call` for them fails as unknown (existing behaviour).
- `newServer` instructions: for comment tokens append "This token can read and comment; ticket changes are not available."
- `mapErr` `CodeInsufficientScope` text becomes scope-aware: read: "This token has read-only scope..." (unchanged); comment: "This token can only read tickets and add or edit its own comments. Use a write token for other changes."
- Annotations unchanged (comment tools stay non-destructive).

### 5.6 Web (small)

`web/src/api/types.ts`: add `'comment'` to the `SCOPES` array (`Scope` is derived from it); `CreateTokenDialog.vue` option "Comment (read and comment)"; `TokensTable.vue` shows it. Keep read as the default selection. One component test each for the option list and the table label. No other UI work (workflow badges, gate buttons are phase 4).

### 5.7 Tests

- Migration test: Up preserves rows and hashes, accepts `comment`, rejects `admin`; Down demotes comment tokens to read; tokens created before still authenticate.
- Service matrix (`testutil/matrix.go` gets a comment-scope row): every service write method against a comment actor: only `Comments.Add` and own-comment `Comments.Edit` succeed; each other write returns `insufficient_scope` (table-driven over all methods so a future method cannot be forgotten); comment actor cannot edit another author's comment (`not_author`); viewer-role owner still 403 `forbidden`; project-limited comment token on another project 404; archived project 409.
- REST: each write route returns 403 `insufficient_scope` for a comment token except the two flagged ones; route-table test fails if a new write route is added without an explicit decision (assert flagged set equals exactly the two routes).
- MCP: `tools/list` for comment token is exactly the 7 names; calling a hidden tool fails; `add_comment` works; instruction text.
- Token creation via REST with `comment`, invalid scope 422.

## 6. Feature D: multi-label filter

Motivation (workflow 11, last row): finding "ready for QA" work needs `needs-qa` and not-`blocked`-style combinations; one label per call forces several calls and client-side joins.

### 6.1 Semantics (**Proposal**)

`labels` is a list of labels, **all must be present (AND)**. Up to 10. The existing single `label` still works and is simply merged into the list (de-duplicated). Excluding labels (NOT) and any-of (OR) are out of scope (Open question 5). Other filters combine with AND as today; pagination and cursor format are unchanged (the label predicate only filters rows, ordering keys are the same).

### 6.2 Service and SQL

- `TicketFilter.LabelIDs []string` added; `LabelID string` stays for compatibility and is appended in `List`. Max 10 ids else 422 field `label`. Each id upper-cased; ids not in the project are not an error (match nothing), as today.
- `TicketListPage` query: replace the single-id `EXISTS` with a count predicate:

```sql
AND (CAST(sqlc.arg(label_count) AS INTEGER) = 0 OR
     (SELECT COUNT(DISTINCT tl.label_id) FROM ticket_labels tl
      WHERE tl.ticket_id = t.id AND tl.label_id IN (sqlc.slice('label_ids'))) = CAST(sqlc.arg(label_count) AS INTEGER))
```

Run `sqlc generate` and check how sqlc emits an empty slice with this predicate for SQLite (it substitutes `NULL`; the `label_count = 0` guard short-circuits). If that proves awkward, fallback: build the predicate with a fixed number of nullable args. Index `ticket_labels` already has (ticket_id, label_id) primary key; no new index needed.

### 6.3 REST

`GET /api/v1/projects/{id}/tickets?label=<id>&label=<id>`: repeated `label` params (currently a repeated param is rejected by `once("label")` in `httpapi/tickets_handlers.go` with 422 `validation_failed`, field "may be given only once"; this becomes allowed for `label` only; other params keep rejecting repeats). Comma-separated lists are not supported (ids and future names may contain commas).

### 6.4 MCP

`list_tickets` gains `labels` (array of names, max 10); `label` (single name) stays and is merged. Unknown name returns the existing label-not-found error listing all unknown names. Description: "labels: only tickets having ALL of these labels."

### 6.5 Tests

Service: two labels AND, one label (old param) unchanged, label + status combination, no match gives empty page, duplicate ids collapse, 11 ids is 422, pagination across a filtered result with a cursor (page 1 then page 2 gives each ticket once). REST: repeated param, single param, repeated `status` still 422 with field `status`. MCP: `labels`, `label`, both merged, unknown name, schema max items.

## 7. Files to change (summary)

| Area | Files |
|---|---|
| Service | `service/actor.go`, `authz.go`, `tickets.go` (ChangeLabels, Move CAS, List filter), `comments.go` (comment-scope variant), `tokens.go`, `types.go`, `validate.go`, `errors.go`, `doc.go` |
| Store | `migrations/00002_token_scope_comment.sql`, `store/queries/tickets.sql` (3 new queries, list predicate), regenerated `store/db/*` |
| REST | `httpapi/tickets_handlers.go`, `tokens_handlers.go`, `middleware.go` (Route.CommentOK), `server.go` (Route), `view.go`, `errors.go` (verify) |
| Auth | `auth/resolver.go` (unknown scope becomes read) |
| MCP | `mcpserver/tools.go` (scope field), `tools_tickets.go`, `auth.go` (toolsFor), `server.go` (instructions, package doc counts), `errors.go`, `schemas.go` |
| Web | `src/api/types.ts`, `CreateTokenDialog.vue`, `TokensTable.vue` (+ tests) |
| Tests | `service/*_test.go`, `httpapi/*_test.go`, `mcpserver/tools_test.go`, `testutil/matrix.go`, `store` migration test |
| Docs | `pabrika/README.md` (tools table, scopes, REST), `pabrika/CLAUDE.md` (Conventions has no scope or error-code list today, only line 80 on `Actor`; add one line there naming the three scopes and `status_conflict`), this repo's `specs/agentic-workflow.md` section 11 (mark gaps closed, in the commit that finishes the phase), `docs/` if scopes or tool counts are mentioned |

## 8. Backwards compatibility and risks

- Existing clients: `update_ticket.labels`, `move_ticket` without `expected_status`, `list_tickets.label`, REST `label=<id>` and all current tokens behave exactly as before. Tests for the old shapes stay unmodified and must keep passing.
- Tool count and text assertions (checked in code): `mcpserver/server.go:15` ("the 14 tools") and `:19` ("5 read-only tools"), `mcpserver/auth.go:126` comment, `mcpserver/tools.go:41` comment, `mcpserver/tools_test.go:526`, `pabrika/README.md` lines 14 and 117 ("Tools (14)" and the table), `pabrika/docs/FINDINGS.md:12`, `pabrika/docs/smoke-test.md:22,114,197`. Historical `pabrika/specs/` and `plans/` files also say 14; leave them as history. Clients that count tools are unaffected.
- Risk, scope enforcement regression: a mistake in the comment-scope variant could let a comment token write. Mitigated by fail-closed defaults, the table-driven "every write method" test and the route-table test (5.7).
- Risk, rollback: `Down` of 00002 demotes `comment` tokens to `read`. A downgraded binary that sees an unknown `comment` scope (without running Down) treats it as non-write only if its code does; the old code treats unknown as write (`readOnly()` is false). Document in the PR: roll back with `goose down`, not by swapping the binary alone. (This is why 5.3 moves to a fail-closed rule now.)
- Risk, status conflict ambiguity: a client that times out after a successful claim sees `status_conflict` on retry. Documented in tool text; mitigated by `get_ticket`.
- Risk, sqlc slice with empty list on SQLite (6.2). Covered by the "no label filter" and "one label" tests.
- Risk, SSE/event churn: none; no new event types.

## 9. Recommended build order

Each step is its own small commit, tests included, and leaves `CGO_ENABLED=0 go test ./...`, `go vet ./...` and `cd web && bun run test && bun run typecheck` green.

1. **B: CAS on move** (smallest, no migration, highest safety value): `status_conflict`, `expected_status`, REST and MCP, concurrency test.
2. **A: ChangeLabels + add_label/remove_label** (unblocks phase 4 label badges and the label-ownership change in phase 1).
3. **D: multi-label filter** (one SQL change plus params).
4. **C: comment scope** (largest, touches authz, migration, REST guard, MCP, web). Ends with the matrix and route-table tests.
5. **Docs sweep**: README tool and scope tables, CLAUDE.md, mark workflow section 11 rows closed, manual check with Claude Code (two MCP sessions racing a claim; a comment token trying `update_ticket`).

## 10. Exit criteria

- [ ] Two concurrent `move_ticket` calls with `expected_status` on one ticket: exactly one succeeds (automated test); the loser gets the documented message and no activity/event.
- [ ] `add_label` / `remove_label` never change labels outside their arguments; concurrent adds from two actors keep both (automated test); no-op calls write no activity or event.
- [ ] A `comment` token can list/get and add/edit own comments and nothing else, through REST, MCP and the service directly; `tools/list` shows exactly 7 tools.
- [ ] `list_tickets` / REST filter by two or more labels with AND semantics, pagination correct.
- [ ] Migration 00002 applies on a database with existing tokens; Up/Down tested; existing tokens still authenticate.
- [ ] All pre-existing tests pass unmodified, except tool-count assertions (`mcpserver/tools_test.go` `len(names) != 14`) and `store/store_test.go` `TestMigrateNewerDB`, which asserts exactly 3 `goose_db_version` rows and becomes 4 with migration 00002; new tests listed in 3.4, 4.4, 5.7, 6.5 exist.
- [ ] `pabrika/README.md` and `pabrika/CLAUDE.md` updated; workflow section 11 rows for these four gaps marked done.
- [ ] Manual check: Claude Code with a comment token cannot move or edit a ticket; two sessions racing a claim get one winner.

## 11. Out of scope

Configurable stages, parent/child tickets, per-tool scopes beyond `comment`, first-class approvals, notifications, a runner, label-CAS (`expected_labels`), label exclusion/OR filters, auto-creating labels on add, bulk ticket operations, comment deletion by comment-scope tokens, and any change to the role files in `agents/` (phase 1 adopts these tools afterwards).

## 12. Proposals (my design; change if you disagree)

1. Single service primitive `ChangeLabels(add, remove)`; MCP exposes it as two tools, REST as one `PATCH /tickets/{id}/labels`.
2. Add/remove are idempotent no-ops, never errors, and a no-op writes nothing.
3. CAS lives on `move` as optional `expected_status`; conflict is 409 `status_conflict` with the actual status in the message.
4. Comment scope value is `comment`: read + add comment + edit own comment, nothing else; role rules unchanged.
5. Unknown scope values fail closed to read-only.
6. Multi-label filter is AND, max 10, repeated `label` params in REST, `labels` array in MCP.
7. Route table flag `CommentOK` makes comment-scope access opt-in per REST route.

## 13. Open questions (need the user)

1. **Dedicated `claim_ticket` tool?** Proposal says no (use `move_ticket` + `expected_status`). A sugar tool (`ticket`, optional `from`, `to`, also sets assignee to the token's owner?) is clearer to agents but adds a tool and assignee semantics (assigning a human for bot work). Decide: none / sugar without assignee / sugar with assignee.
2. **Conflict body detail:** keep actual status only in the message (proposed), or add a structured field (`current_status`) to the REST error shape? The latter changes the shared error format for one code.
3. **May a viewer-role owner's `comment` token comment?** Today commenting needs editor. Proposal keeps editor. Letting viewers comment would be a product change (reviewers who are only viewers).
4. **Scope name and breadth:** `comment`, or something wider like `triage` (comment plus label add/remove) so Reviewer/QA could also set their own `needs-*` labels? Workflow 5.1 currently has only the Orchestrator relabel; a wider scope would change that design.
5. **Filter modes:** is AND-only enough, or do you want any-of and "not label X" now (for example "needs-dev but not blocked")? Excluding `blocked` is likely common; I left it out to avoid widening scope.
6. **Atomic label swap exposure:** expose `ChangeLabels` fully to MCP (a third tool or `remove` param on `add_label`) so a transition (`needs-review` to `needs-qa`) is one atomic call and never shows two `needs-*` labels at once? Proposal: two calls for now.
7. **Role for comment tokens in `/auth/me` and token UI:** show owner's real role or the capped one? Proposal: capped to viewer for display, as read tokens are.
