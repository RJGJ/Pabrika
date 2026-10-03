# Build findings

Aggregated from the agents that implemented the six phases. This is the review list: what was built, what was and was not verified, decisions the agents made on their own, and open items. Written 2026-10-03.

## State at a glance

| Phase | Built | Verified how |
|---|---|---|
| 1 Foundation | Config, SQLite store, migrations, sqlc, service layer (projects, members, labels, tickets, comments, activity, users, tokens), authz, events seam, testutil | `go test` (service, store, config), concurrency tests on a file DB |
| 2 Auth, sharing, REST | argon2id, sessions, API tokens, rate limits, httpapi core, all `/api/v1` endpoints, CLI (`serve`, `user create`, `user reset-password`, `healthcheck`, `version`) | httpapi tests incl. a 35-route x 12-principal matrix, golden shapes, hardening tests; real binary smoke test |
| 3 Live updates | In-memory hub, SSE endpoint, shutdown wiring | hub stress test, service integration tests, SSE tests, real binary with an open stream |
| 4 MCP | 14 tools (5 read, 9 write), 2 resources, Streamable HTTP on `/mcp` | SDK client end to end over httptest and the real mux, scope and limit matrices, real binary |
| 5 Web UI | Auth, board with drag and drop, ticket panel, live updates, project settings, account settings | 403 Vitest tests, build, `check:dist`; manual browser checks (partial, see below) |
| 6 Ship | SPA handler and embedded UI, Makefile, audit script, Dockerfile and docs | audit tests, native binary smoke test; **Docker not run** |

Final gates on `main`: `go build`, `go vet`, `go test ./... -count=1`, `gofmt -l` all clean (CGO off); `bun run typecheck`, `bun run test` (403/403), `bun run build`, `bun run check:dist` all pass.

## Not verified (needs you, or Docker)

- **Docker:** `docker build`, image size, non-root and no-shell checks, `HEALTHCHECK`, `docker stop` exit code and timing, read-only root, volume ownership. Docker is not installed here. The Dockerfile COPY paths were only checked on paper.
- **Proxy and backups:** Caddy and nginx snippets, SQLite `.backup` and restore, Litestream. Not run.
- **Real Claude Code against `/mcp`:** `claude mcp add ...` with a real token. Covered only by the in-process SDK client test.
- **Browser end-to-end on the real server was partial.** The WEB4 agent was stopped part-way to save tokens. Done in a real browser: the screens checked by WEB1 to WEB3 against the mock, the P6 smoke check (login, board, ticket panel, settings, account, no CSP violations), and WEB4's fixes for the label picker, Escape in the title field, and focus return. **Not completed:** the full checklist against the real Go server (drag and drop with two sessions, member add and role change, lost access after removal, reconnect after a server restart, password change, token create and revoke, dev Origin mismatch toast). The code for these is covered by unit and component tests, but not by an end-to-end run.
- **`make`:** not installed here, so Makefile targets were never run (their underlying commands were).
- **Windows signal handling:** Windows has no SIGTERM. Graceful shutdown was verified with CTRL_BREAK. Linux SIGTERM behaviour in a container is untested.

## Decisions the agents made without you

Provisional decisions are in `specs/phases.md`. These came up during the build:

1. **Go 1.25.** MCP SDK v1.4.1 was chosen because `govulncheck` found 3 reachable vulnerabilities in v1.3.1, and v1.4.1 needs Go 1.25. So `go.mod` says `go 1.25.0` and the Dockerfile builder is `golang:1.25` (the spec said 1.23). A cross-origin check added in SDK v1.4 rejected valid Origins; the server now tells it to trust the `BASE_URL` origin.
2. **MCP tools use raw registration with hand-built schemas.** The SDK's typed registration turns bad arguments into protocol errors, which the model can't recover from. Calling a hidden write tool with a read token returns a protocol "unknown tool" error rather than a tool error.
3. **Search is `instr(lower(...))`,** not `LIKE ... ESCAPE`, because sqlc's SQLite parser rejects `ESCAPE`. Behaviour is the same (literal, ASCII case-insensitive).
4. **Middleware order** is log, headers, recover, body cap, Origin, so a panic's 500 still carries the request id and security headers.
5. **Comment edit/delete check order:** not found, scope, role below editor (forbidden), archived (409), then not_author, then validation.
6. **Move anchors:** any unknown, garbage, deleted, cross-project or wrong-status anchor is `anchor_invalid`, so another project's tickets are never revealed.
7. **Account:** wrong current password is 422 on `current_password`, not 401, so the UI doesn't treat it as an expired session.
8. **Settings role and scope pickers are native `<select>`** elements, not the shadcn Select.
9. **Router:** `board` and `ticket` are two records sharing one component loader (a single record with an optional `/t/:number` can't match `/p/WEB`). Do not key the `RouterView`.
10. **Web test timeout** raised to 30 s: the first test in a file loads the whole app and exceeded the 5 s default on Windows.

## Open items and risks

1. **Member removal ordering.** The hub publishes `member.changed` before closing the removed user's stream, so that user's own stream can receive one frame (ids only) before it closes.
2. **Rate limiting is per (IP, email)** as in the main spec. One IP can try unlimited different emails, and a distributed attack on one email is unlimited. A global per-IP bucket would help but would throttle everyone behind a proxy when `TRUST_PROXY=false`.
3. **`/mcp` has no rate limiting** in v1 (out of scope in the spec).
4. **Prompt injection.** Ticket text is untrusted data returned to agents. The tool descriptions and server instructions say so, and the README recommends read-only or project-limited tokens for agents that read untrusted tickets. There is no technical mitigation beyond that.
5. **Cursor with a bad percent-escape** (for example `%%%x`) is silently dropped by Go's query parser, so the request returns the first page instead of 400. Properly escaped garbage does return 400.
6. **Chunked bodies over 1 MiB on `/mcp`:** only a `Content-Length` above the cap gets the 400; a chunked body cut by `MaxBytesReader` is left to the SDK and not asserted.
7. **A temp card can briefly sit beside the real one** if `ticket.created` arrives before the POST response. Cosmetic; cleared on reconcile.
8. **Slow-test fragility.** Several web tests are sensitive to machine load; a timeout there is not necessarily a real failure. Run the suite alone before investigating.
9. **markdown-it private API.** To render `![x](y)` as literal text, `lib/markdown.ts` reads `inline.ruler.__rules__`. Revisit on a markdown-it upgrade.
10. **Token-author fixtures** contain a generated name (`token F39HNX`) in `internal/httpapi/testdata/shapes/`. Compare keys and types, not that string, when diffing web fixtures against them.
11. **Web fixtures vs real responses:** the 30 server fixtures in `testdata/shapes/` were never diffed against `web/tests/fixtures`. Do that diff as the first step of any follow-up.
12. **Local `core.autocrlf`.** Git on this machine converted line endings; a root `.gitattributes` now forces LF. Files written through some tools may still arrive with CRLF.
13. **Not done in WP13/Phase 1 plan extras:** the randomised move-model test.
14. **Phase 3 event assertions in MCP tests** are partial (actor on create, no event on no-op or failure) and do not check the `renumbered` flag.
15. **Unused tooling noise:** build and test commands print `NativeCommandError` noise under PowerShell 5.1 (stderr formatting only).

## Suggested next steps

1. Install Docker (or use CI) and run the Docker items in `docs/smoke-test.md` and `docs/security-audit.md`.
2. Run the full browser checklist against the real server (plan `plans/phase-5-web-ui.md` WP9 and spec section 12).
3. Run `claude mcp add --transport http pabrika http://localhost:8080/mcp --header "Authorization: Bearer pb_..."` and exercise the tools.
4. Diff `internal/httpapi/testdata/shapes/*.json` against `web/tests/fixtures`.
5. Decide the open questions above (rate limiting, member-removal frame).
