# Phase 4 plan: MCP server

Source: `phase-4-mcp-server.md` (cited as P4 §n), with `phases.md` Decisions taking precedence. Phases 1 to 3 are specs only; the repo has no code yet. The plan therefore assumes Phase 1 and 2 names exactly as the specs state them, and it has a checkpoint (WP0) that confirms they exist.

## 0. Ground rules

- `mcpserver` is a thin adapter. It adds no SQL, tables or business rules (P4 §1). A missing service capability becomes a small additive service change, and the plan records it.
- Every `go` command uses `CGO_ENABLED=0`. The gate is `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`. A `-race` run is optional where a C toolchain exists.
- Tool count is 14: 5 read tools and 9 write tools. Main-spec lists 13 and omits `update_comment`; `phases.md` Decisions override it.
- Write tests first for each resolver, error mapper, tool group and scope behaviour. Each WP's "Proof" line names the tests that must go green before moving on.

## WP0. Preconditions and T0 SDK spike (blocking, time-box about half a day)

### 0a. Preconditions
Confirm the following exist in the tree. If not, Phase 4 cannot start; stub only after asking:
- Phase 2: `httpapi.Server.MountRaw`, `httpapi.WriteError`, `auth.Resolver.ResolveBearer`, `Principal.Actor()`, `Principal.Token{ID, Name, Scope, ProjectID, ProjectKey}`.
- Phase 1: `service.Services`, `TokenActor`, `Error`/`Kind`/`Code`, the `Resolve` methods, `Comments.Latest/Edit`, and the shared constants and `Validate()` methods.
- Phase 3 is optional.

### 0b. Spike
Write a throwaway test in `internal/mcpserver/spike_test.go`, which is deleted at the end of WP0. Pin the SDK with `CGO_ENABLED=0 go get github.com/modelcontextprotocol/go-sdk@<exact>`, preferring the latest tagged release. The spike must prove each row of P4 §4.1 and record the result:

| # | Must prove | Fallback if false |
|---|---|---|
| S1 | `NewStreamableHTTPHandler(func(*http.Request)*mcp.Server, &StreamableHTTPOptions{Stateless:true})` exists and calls `getServer` per request. | One server with all 14 tools; filter `tools/list` and reject hidden calls in `AddReceivingMiddleware`. |
| S2 | `JSONResponse: true` (or equivalent) exists. | Accept SSE-framed replies; the tests use the SDK client either way. |
| S3 | Request context values reach `getServer`, and a closure over the principal works. | Closure only. |
| S4 | Typed `mcp.AddTool` fills `structuredContent` plus a text block, and `isError` results work. | Build the `CallToolResult` by hand. |
| S5 | Unknown argument, wrong type and missing required field come back as `isError` results, not JSON-RPC errors. | Use low-level `Server.AddTool` with raw `json.RawMessage` arguments, a hand-written schema, `DisallowUnknownFields`, and handler-side validation. This also gives omitted-vs-null detection for free. |
| S6 | A Go `error` or panic from a handler is converted to an `isError` result. | Always return sanitized `toolErr` results and wrap handlers in a recover middleware. |
| S7 | `mcp.NewInMemoryTransports()` exists. | Use `httptest` plus `StreamableClientTransport` with an `http.Client` that injects `Authorization`. |
| S8 | Schema hooks allow enum, `maxLength` and `type:["string","null"]`, and `additionalProperties:false`. | Hand-built `jsonschema.Schema` helper, cached at init. |
| S9 | Resources and templates can be registered per request. | Drop T10. |
| S10 | In stateless mode, GET and DELETE return 405, and the SDK's own localhost or cross-origin protection does not reject `localhost` or proxied requests. | Leave the SDK defaults, or disable its protection and rely on our Origin rule. |
| S11 | The `Accept: application/json, text/event-stream` requirement, and the status the SDK returns for a body cut by `MaxBytesReader`. | Document it and keep the 400 assertion in the tests. |

Also check whether the SDK's schema inference depends on `invopop/jsonschema` struct tags (`jsonschema:"..."`) or on a different tag format. This decides how inputs are declared in WP4.

**Go/no-go.** Go if S1 and S3 pass, or fall back to the middleware route (which works with a single server), and S4 to S8 each pass or have a workable fallback. The usual outcome is "go, with fallbacks X, Y". No-go (stop and ask the user) only if the SDK cannot serve stateless Streamable HTTP with per-request auth at all, or cannot return `isError` content for argument errors even through the low-level `AddTool`. In that case the options are a different SDK version or a thin hand-rolled JSON-RPC handler.

Output of WP0: a short "Spike result" section (SDK version and the fallback chosen per row) added to this file. The decisions it fixes are:
- **Registration style: typed or raw.** A raw `Server.AddTool` table with hand-built schemas is the most robust choice. If S5 or S8 fail, choose it for all 14 tools.
- **Nullable handling:** `Nullable[T]` versus raw decode.

Proof: spike test passes, then delete it.

## WP1. Scaffold and wiring (T1, T2)

Files:
- `internal/mcpserver/server.go`: `Deps{Services *service.Services; Resolver *auth.Resolver; BaseURL string; Origin string; Logger *slog.Logger}` and `NewHandler(deps) http.Handler`. The first version returns a stub that serves an empty-tools server.
- `cmd/pabrika` serve wiring: `srv.MountRaw("/mcp", mcpserver.NewHandler(...))`. `/mcp/x` and `/.well-known/*` return the JSON 404 via `httpapi.WriteError`.
- `go.mod`, `go.sum`.

Proof: `CGO_ENABLED=0 go build ./...`, and `CGO_ENABLED=0 go test ./internal/mcpserver -run TestMount` (the mount reaches the handler, `/mcp/x` is a JSON 404).

## WP2. Transport guard: Origin, bearer, visibility (T3, `auth.go`)

Steps, test-first:
1. `originGuard` runs before auth on every method. A present `Origin` is compared with `config.Config.Origin()`; `null` or a mismatch gives 403 `origin_mismatch`. An absent `Origin` passes. No CORS headers and no `OPTIONS` handling.
2. `bearerAuth` calls `Resolver.ResolveBearer(r)`, never `Resolve`. Any failure gives 401 with `WWW-Authenticate: Bearer` and the identical `unauthorized` body via `httpapi.WriteError`. It guards `Method == "token"` and puts the `Principal` into the request context.
3. A body-cap check rejects `Content-Length > 1 MiB` with 400 `body_too_large`.
4. `visibleTools(principal)` returns the list of tool names. Read scope gets 5, write scope gets 14. `delete_project` and any member or token tool must never exist.
5. Chain order is: global middleware (from `MountRaw`), then Origin guard, bearer auth, body check, SDK handler. Phase 2 may already apply an Origin rule inside the global chain (see Open questions); applying it again is harmless.

Proof: `httptest` tests via `go test ./internal/mcpserver -run 'TestAuth|TestOrigin|TestBodyCap'`. These cover P4 §11 "Transport and auth": 401 matrix, cookie-only is 401 with no session slide, bearer plus cookie uses the token, Origin on POST, GET and DELETE, GET returns 405 after auth, `Authorization` never logged, `last_used_at` set.

## WP3. Resolvers and error mapping (T4, T5; parallel with each other and with WP4)

**`resolve.go`** (P4 §6; table-driven unit tests against an in-memory service with the seeded fixtures: alice owner, bob editor, carol viewer, dave non-member, projects WEB and OPS):
- `resolveProject(ctx, actor, in)`: `Projects.Resolve`, with the "No project with key X. Available keys: ..." message built from `Projects.List(includeArchived=true)`. Lists are capped at 20 entries plus "... and N more", and each entry is truncated at 80 characters.
- `resolveTicket`: ULID check (26 Crockford-base32 characters, case-insensitive) or the `KEY-N` regex, then `Tickets.Resolve`. On not-found, call `Projects.Resolve` on the key part only to choose the message. Messages cover malformed, unknown project, unknown number ("may already be deleted") and unknown id.
- `resolveComment`: `Comments.Resolve` returns `CommentRef`.
- `resolveAssignee`: email, case-insensitive, matched against `Members.List`. `null` or `""` means unassign, omitted means unchanged. Used for `list_tickets` as well (`me` resolves to `Actor.UserID`, `unassigned`/`none` is the alias).
- `resolveLabels`: names matched against `Labels.List`. Collapse duplicates and report all unknown names in one message. Empty list clears.
- `resolveAnchor` (before/after): resolve like `ticket`, same project check, with the cross-project message and the self-anchor message.
- Limited-token rule: when `Principal.Token.ProjectID != ""`, every not-found target returns the single "limited to project X" text. An unlimited token never gets that text.
- Empty-input messages: "project is required." and "ticket is required."

**`errors.go`** (P4 §8), `mapErr(err, ctx)`:
- Handle the service `Kind` and `Code` rows: `forbidden` (role, via a follow-up `Projects.Get`, with a fallback text), `insufficient_scope`, limited-token `create_project`, `not_author`, `invalid_cursor`, `project_archived`, `key_taken`, `label_exists`, `anchor_invalid`.
- Validation messages come from `Fields`.
- Unexpected errors log tool name and token id and return the generic internal text.
- Helper `toolErr(format, args...) *mcp.CallToolResult`, plus a recover middleware (panic becomes "Internal error. Try again.").

Proof: `go test ./internal/mcpserver -run 'TestResolve|TestMapErr'`.

## WP4. Inputs, schemas, results (T6, T9)

Files: `results.go` and a schema file, either `schemas.go` or inline in the `tools_*.go` files.

1. Input structs (one per tool) with `json` and `jsonschema` tags. Enums come from `service.Statuses/Priorities/LabelColors` and `place` is `top|bottom`. `maxLength` comes from the `Max*` constants. `required` follows P4 §7.2. `confirm` is deliberately not required. `additionalProperties:false` throughout. Schemas are built once at init and cached.
2. `Nullable[T]` (`Set`, `Value *T`, custom `UnmarshalJSON`) for `assignee` and `due_date` (`""` equals null), or raw decode if the spike chose that. It is kept separate from Phase 1's `Optional[T]`; convert to `Optional` when building service inputs.
3. DTOs and builders (P4 §7.1): `ProjectSummary`, `TicketSummary`, `TicketDetail`, comment items with `author_kind` and `body_truncated`, and URLs built from `BASE_URL` (`/p/KEY`, `/p/KEY/t/N`). Assignee emails come from one id-to-email map per call. `get_ticket` applies the 8,000-character per-comment cut and the 40,000-character total budget. No internal fields.
4. A `result(out)` helper puts the compact JSON text block and `structuredContent` side by side (hand-built if S4 failed).

Proof: `go test ./internal/mcpserver -run 'TestSchemas|TestResultShapes'`. The drift test asserts enum and cap equality with the shared constants, matches `required` to the table, and checks no extra keys.

## WP5. Test harness (parallel with WP3/WP4; blocks WP6/WP7)

`testutil_test.go`:
- Seeds users, projects, labels and tickets (the fixtures above).
- `connect(t, principal) *mcp.ClientSession` builds the server with the production constructor (the same function `getServer` calls), over in-memory transports, or the httptest fallback.
- `mkToken(scope, limitProject)` builds a `Principal`.
- An activity-row and event-capture helper.

## WP6. Read tools (T7)

Files: `tools_projects.go` (`list_projects`, `list_members`), `tools_labels.go` (`list_labels`), `tools_tickets.go` (`list_tickets`, `get_ticket`).
- `list_projects`: a single `Projects.List` call. `role` and `counts` come straight from `ProjectSummary`.
- `list_tickets`: pre-validate enums, resolve the assignee and label filters, pass `query`, `limit` (default 50, max 200) and `cursor` unchanged, and map `NextCursor` `""` to `null`.
- `get_ticket`: `Comments.Latest(ref, 50)`, with `comments_truncated` combining the service flag and the size-budget flag, and `comment_count` taken from `Ticket.CommentCount`.
- Set annotations (`ReadOnlyHint`) and `Title`, and include the untrusted-content notice in the `list_tickets`/`get_ticket` descriptions and the server `instructions` (P4 §5.1).

Proof: `go test ./internal/mcpserver -run 'TestRead'`. Cases come from P4 §11 per-tool bullets for `list_projects`, `list_tickets` (filters, `me`, pagination without gaps, garbage cursor) and `get_ticket` (50 cap, budgets, token and user authors).

## WP7. Write tools (T8; the groups can be done in parallel by separate agents once WP3 to WP5 exist)

- **7a Projects and labels:** `create_project` (key pre-check, limited-token error), `update_project` (at least one field, owner required, archive allowed), `create_label` (color validation, `label_exists`).
- **7b Tickets:**
  - `create_ticket`: resolves assignee and labels, builds `CreateTicketInput`, calls `Validate()`, then the service.
  - `update_ticket`: at least one field; `status` is rejected as an unknown argument; the Nullable/Optional conversion.
  - `move_ticket`: one of place, before, after; self and cross-project anchor checks; surface `renumbered` only when true.
  - `delete_ticket`: handler-checked `confirm`, with `DestructiveHint`.
- **7c Comments:** `add_comment` (returns the id, ticket ref, author and `created_at`), `update_comment` (resolves via `Comments.Resolve`; `not_author` is distinct from `forbidden`; result is `{id, ticket, author, updated_at}`).
- **Common to all:** `IdempotentHint` on the update and move tools, `OpenWorldHint:false`, and descriptions verbatim from P4 §7.2, including the "if a call times out, check ..." sentence on the three non-idempotent creators.

Proof: `go test ./internal/mcpserver -run 'TestWrite|TestMove|TestDelete|TestComment'`, covering the §11 bullets, plus the archived-project, project-limited and viewer-role matrices. Also run the **activity and attribution assertions** (`actor_type='api_token'`, `actor_id`, correct `action`; comments write no activity; reads write none) and, if Phase 3 exists, the **events** assertions (one event after commit, none on error or no-op).

## WP8. Scope filtering and full-surface tests (T11)

- `tools/list` returns exactly 5 tools for a read token and 14 for a write token. A direct `tools/call` of a write tool on a read token fails and changes nothing. There is no tool named `delete_project`, and no member or token tools.
- Role matrix: viewer owner with a write token sees the tools but gets the role error; editor gets the owner-required error from `update_project`; `create_project` works for an editor.
- Limited token matrix: identical text for existing and nonexistent targets, `create_project` forbidden, resources for the other project fail.
- One end-to-end test through `httptest` and the real mux with the SDK's Streamable HTTP client and a bearer header (initialize, `tools/list`, one read, one write). This proves the principal reaches the handlers.
- Logging test: no `Authorization` value and no tool arguments or ticket text in captured logs.

Proof: `CGO_ENABLED=0 go test ./internal/mcpserver/...` fully green.

## WP9. Resources (T10, optional, built last)

`resources.go`:
- Static `pabrika://projects` (same content as `list_projects`) and template `pabrika://projects/{key}/board` with columns, per-column ordering by position, a 500-ticket cap with `truncated`, and key or ULID accepted.
- Limited tokens see only their project.
- Drop the whole WP if S9 failed or it is awkward.

Proof: `go test ./internal/mcpserver -run TestResources`.

## WP10. Manual check and close-out (T12)

1. `CGO_ENABLED=0 go run ./cmd/pabrika serve`, create a user and write and read tokens (CLI plus REST).
2. `claude mcp add --transport http pabrika http://localhost:8080/mcp --header "Authorization: Bearer pb_..."`. In Claude Code, list projects, create a ticket, move it to `in_progress`, comment on it, and edit that comment with `update_comment`.
3. Confirm via REST or the board that activity and comment attribution show the token name.
4. Confirm a read token in the same client shows 5 tools.
5. If Phase 3 is done, watch an open SSE stream for the events.
6. Record the result in this file, and set the Phase 4 status in `phases.md` to done.

## Dependencies and parallelism

```
WP0 -> WP1 -> WP2
WP1 -> WP3 (resolve | errors), WP4, WP5   (all parallel)
WP3+WP4+WP5 -> WP6 -> WP7a|7b|7c (parallel; 7b is the largest) -> WP8 -> WP9 -> WP10
```
WP2 can run in parallel with WP3 to WP5, but WP8's end-to-end test needs it.

## Definition of done (mapped to exit criteria, P4 §12 and phases.md)

- `CGO_ENABLED=0 go build ./...` and `CGO_ENABLED=0 go test ./...` pass.
- All 14 tools are tested through the in-memory (or fallback) transport for happy path, each §7.3 error row, scope filtering and activity logging.
- The httptest end-to-end test passes.
- The schema drift test passes.
- Manual Claude Code check is recorded.
- A read token shows exactly 5 tools.
- Phase 6 receives these notes: `/mcp` and `/.well-known/*` JSON 404 precedence, the browser-origin rejection, the untrusted-content README advice, and the schema-drift check.

## Risks and de-risking

| Risk | Mitigation |
|---|---|
| SDK API churn or mismatch with the assumptions. | WP0 spike, an exact pinned version, and the fallback table. All SDK calls are isolated in `server.go` and a `register` helper, so a swap touches few files. |
| SDK argument errors surface as protocol errors, so the model cannot self-correct. | Raw `Server.AddTool` with `DisallowUnknownFields`. |
| Omitted-vs-null on `assignee`/`due_date`. | `Nullable[T]`, tested with omitted, `null` and `""`. |
| Service gaps (for example `Comments.Latest` or `ProjectSummary.TicketCounts` not as specified). | WP0a check; additive service changes only, noted in the plan. |
| Phase 2 already applies the `/mcp` Origin rule in global middleware, causing double handling. | Harmless, because both rules are identical. Test through the real mux. |
| Role text needs a follow-up `Projects.Get`. | Only on the error path, with a fallback sentence. |
| The "limited to" rule leaking existence. | Same text for existing and nonexistent targets, covered by a dedicated test. |
| Phase 3 not done. | The events tests are skipped, and the tools still work. |

## Size estimate

About 3 to 4 engineer-days: WP0 about 0.5 days, WP1 and WP2 about 0.5 days, WP3 to WP5 about 1 day, WP6 about 0.5 days, WP7 about 1.5 days, WP8 about 0.5 days, WP9 about 0.5 days, WP10 about 0.25 days. Roughly 1,800 lines of Go plus about 1,500 lines of tests.

## Open questions

1. Conflict between the specs. Phase 2 says `MountRaw`'s global middleware includes the `/mcp` Origin rule, while P4 §4 says `mcpserver` applies it because `MountRaw` skips Phase 2's check. The plan implements the rule in `mcpserver` regardless, which is safe either way. Phase 2's final code decides whether the duplication is wanted. (The specs were reconciled after this plan was drafted; confirm against the committed Phase 2 text.)
2. Main-spec still lists 13 tools. `phases.md` Decisions (14 with `update_comment`) win, but main-spec and the phase summary text should be updated for consistency.
3. The exact `ProjectSummary`, `Comments.Latest` and `CommentRef` signatures cannot be verified until Phase 1 code exists. The spike and the WP0a check cover this.

Otherwise none; the spec marks no unresolved items.

### Critical files for implementation
- `pabrika/specs/phase-4-mcp-server.md`
- `pabrika/specs/phase-2-auth-sharing-rest.md`
- `pabrika/specs/phase-1-foundation.md`
- `pabrika/specs/phases.md`
- `internal/mcpserver/` (new package)
