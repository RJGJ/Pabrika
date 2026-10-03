# Security audit checklist (Phase 6)

Source: `specs/phase-6-ship.md`, Task 4. Fill in the Status column (`pending`, `pass`, `fail`, or `exception: <reason>`) and name the evidence in the PR. `make audit` runs the grep and tool checks in one go. All items are `pending` until the code from Phases 1 to 5 exists and has been run.

Header values must equal Phase 2 section 11 character for character. Where a check is a test, "reuse" means the owning phase's test may be named as evidence.

## Headers (applied by the outermost middleware to every response)

| # | Item | Check | Status |
|---|---|---|---|
| H1 | `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'`. No `unsafe-eval`, no script `unsafe-inline` | Serve the real bundle from the binary, open `/login`, `/`, a board, a ticket panel, project settings and account settings with devtools open: zero "Refused to ..." messages. `grep -c "<script" web/dist/index.html` shows only `src=` scripts. `grep -rl "eval(\|new Function" web/dist/assets` finds nothing from app code (record any inert library hit) | pending |
| H2 | `X-Content-Type-Options: nosniff` | `curl -sI http://localhost:8080/ \| grep -i x-content-type-options`; header table test | pending |
| H3 | `Referrer-Policy: same-origin` | `curl -sI http://localhost:8080/ \| grep -i referrer-policy`; header table test | pending |
| H4 | `X-Frame-Options: DENY` | `curl -sI http://localhost:8080/ \| grep -i x-frame-options`; header table test | pending |
| H5 | `Permissions-Policy: camera=(), microphone=(), geolocation=()` | `curl -sI http://localhost:8080/ \| grep -i permissions-policy`; header table test | pending |
| H6 | HSTS `max-age=31536000` from the proxy; the app sets it only when `BASE_URL` starts with `https://`, no `includeSubDomains`; sent once | Header test with `BASE_URL=http://...` has no HSTS and with `https://...` exactly one. Through the proxy: `curl -skI https://HOST/ \| grep -ci '^strict-transport-security'` prints `1` | pending |
| H7 | SSE keeps the header set plus `Cache-Control: no-store, no-transform` and `X-Accel-Buffering: no` | Header test opens the events stream on a real `httptest.Server` and asserts the full set | pending |
| H8 | No CORS | `curl -si -H 'Origin: https://evil.example' http://localhost:8080/api/v1/auth/config \| grep -ci '^access-control'` prints `0`. `curl -si -X OPTIONS http://localhost:8080/api/v1/projects` returns the JSON 405 or 404, not a preflight approval | pending |
| H9 | Header values come from shared constants, compared verbatim with Phase 2 section 11 | Review the header test source: it imports the middleware constants | pending |
| H10 | One table-driven test covers `/`, `/p/WEB`, `/api/v1/auth/me` (401), `/api/v1/auth/config`, `/healthz`, `/.well-known/x` (404), `/assets/x.js`, `/assets/missing.js` (404), `/mcp` without a token (401), `POST /` (405), a 429, a 503 `unavailable`, a forced 500 and the SSE stream | `CGO_ENABLED=0 go test ./internal/httpapi/... -run Header` (use the actual test name) | pending |

## Secrets at rest and in comparison

| # | Item | Check | Status |
|---|---|---|---|
| S1 | Session tokens: only the SHA-256 hash is stored; the raw value lives only in the cookie | Test: after login the raw cookie value is not found in any text column of any table (iterate `sqlite_master`, `SELECT *`, substring match). Smoke: `sqlite3 backup.db .dump \| grep -c "<cookie value>"` prints `0` | pending |
| S2 | API tokens: only `token_hash` stored; the full token is returned once; `token_prefix` is 8 characters | Test: same DB scan for the secret; `GET /api/v1/tokens` body does not contain it | pending |
| S3 | Secret and hash comparisons in Go use `crypto/subtle.ConstantTimeCompare` | `grep -rnE "(==\|!=) *[a-zA-Z_.]*([Hh]ash\|[Tt]oken\|[Ss]ecret)" internal/auth internal/httpapi internal/mcpserver --include=*.go \| grep -v _test`: no secret-vs-secret comparison (list and justify each hit; empty-string and nil checks are fine) | pending |
| S4 | Passwords: argon2id, 64 MiB, 3 passes, 2 threads, PHC string; constant-time verify; dummy hash for unknown emails | Phase 2 PHC-param and dummy-hash tests pass; `grep -rn "argon2.IDKey" internal` shows only `password.go` call sites reading params from one place | pending |
| S5 | Generic login error for unknown email and wrong password (same status, code, message) | Phase 2 login test passes | pending |
| S6 | Password change (`POST /api/v1/auth/me/password`): session-only (bearer gets 403 `session_required`), rate limited (429), wrong `current_password` is 422 `fields.current_password`, same rules as signup, deletes other sessions and keeps the current one, API tokens not revoked. `PATCH /auth/me` is session-only too | Test: after a change a second session's cookie gets 401 and the current session still works | pending |
| S7 | SQL: every query is a sqlc-generated parameterized statement; `LIKE` filters escape `%`, `_`, `\` | `grep -rniE "fmt\.Sprintf\(.*(select\|insert\|update\|delete) " internal \| grep -v _test` is empty; Phase 1 list test with a literal `%` passes | pending |

## Log redaction

| # | Item | Check | Status |
|---|---|---|---|
| L1 | No log line contains `Authorization` values, `Cookie`/`Set-Cookie` values, passwords or full tokens; request log has method, route or path, status, duration, request id, user id (token id for token calls), client IP only | Capturing-logger test (see L5) | pending |
| L2 | No tokens in URLs; the access log omits the query string | `grep -rniE "slog\.\|log\.(Print\|Fatal)" internal cmd --include=*.go \| grep -iE "authorization\|cookie\|password\|secret\|token\)" \| grep -v _test` is empty or each hit is justified (token id and name are fine, token value is not) | pending |
| L3 | Panic recovery logs stack and request id, not request headers | Forced-panic test (see L5) | pending |
| L4 | Startup config log contains no secret | Read the first `docker logs pabrika` line; review the config log call | pending |
| L5 | Test: a bearer request, a cookie request and a login with a password through the full handler (including `/mcp`) with a capturing logger, plus one forced panic; captured output contains none of the token, cookie value or password | `CGO_ENABLED=0 go test ./... -run Redact` (use the actual test name) | pending |

## Body and field caps

| # | Item | Check | Status |
|---|---|---|---|
| B1 | Bodies capped at 1 MiB with `http.MaxBytesReader`, including `/mcp`; oversize returns 400 `body_too_large` | Test posts 1 MiB + 1 byte to a tickets route, `/api/v1/auth/login` and `/mcp` | pending |
| B2 | Field limits (characters, not bytes): email 254, display name 1 to 100, password 10 to 200, project key `^[A-Z]{2,6}$`, project name 1 to 100, project description 2,000, ticket title 1 to 200, ticket description 20,000, comment body 1 to 20,000, label name 1 to 50, token name 1 to 100, labels per ticket 50 | Table test over every field at limit and limit+1 (display name at 100, 101 and 0), including a multibyte string at the boundary | pending |
| B3 | Same limits through MCP | Phase 4 schema drift test passes; an MCP `create_ticket` with a 201-character title returns the "too long" tool error | pending |
| B4 | Non-JSON bodies on POST/PATCH/DELETE return 415 `unsupported_media_type` | `curl -s -X POST -H 'Origin: http://localhost:8080' -H 'Content-Type: text/plain' -d x http://localhost:8080/api/v1/auth/login -w '%{http_code}'` ends in `415` | pending |
| B5 | Server timeouts: `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `IdleTimeout` 120 s, no `WriteTimeout` | `grep -rn "WriteTimeout" cmd internal` finds none set; a test holds an SSE stream open longer than `ReadTimeout` (shortened value) and still receives a keepalive | pending |

## Markdown sanitization (client)

| # | Item | Check | Status |
|---|---|---|---|
| M1 | `markdown-it` with `html: false`, output through DOMPurify with a tight allowlist; links get `rel="noopener noreferrer nofollow"` and `target="_blank"`; only `http`, `https`, `mailto`; images dropped | Review `web/src/lib/markdown.ts` | pending |
| M2 | `v-html` only in `MarkdownView.vue`; no `innerHTML` | `grep -rn "v-html" web/src` lists exactly `MarkdownView.vue`; `grep -rn "innerHTML" web/src` is empty | pending |
| M3 | Unit test neutralizes `<script>`, `<img onerror>`, `[x](javascript:alert(1))`, `<iframe>`, SVG with script, `![x](http://a/b.png)`, `[x](data:text/html,...)` | `cd web && bun run test` | pending |
| M4 | Ticket and comment content is stored raw on the server | Review: no server-side HTML transform | pending |

## Cookies and proxy

| # | Item | Check | Status |
|---|---|---|---|
| C1 | Session cookie: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` unless `COOKIE_SECURE=false`, 30-day sliding `Max-Age`, no `Domain` | Phase 2 cookie test passes; smoke step 9: `curl -skc jar.txt ...` then read the jar | pending |
| C2 | `Secure` comes from `COOKIE_SECURE`, not `r.TLS` | Test: plain-HTTP request with default config still emits `Secure` | pending |
| C3 | Origin check uses `BASE_URL` (scheme, host, port, default ports normalized, host case-insensitive), not `Host`. Wrong Origin on POST/PATCH/DELETE 403 `origin_mismatch`; `Origin: null` 403; missing Origin on a cookie or unauthenticated unsafe request 403; bearer without Origin passes; bearer with mismatching Origin on `/api/v1` 403; `Host` spoofing changes nothing | Phase 2 Origin tests pass | pending |
| C4 | `/mcp`: ignores cookies, requires a bearer (401 otherwise); a present `Origin` must match `BASE_URL` (403 `origin_mismatch`); absent Origin allowed. `GET /mcp` and `DELETE /mcp` return 405 | Test: cookie-only 401; mismatching Origin with valid bearer 403; matching and absent Origin pass | pending |
| C5 | `X-Forwarded-*` not trusted for security decisions. `TRUST_PROXY` (default `false`) affects client IP only | Tests: with it off a spoofed `X-Forwarded-For` does not change the rate-limit bucket; with it on it does. README states the overwrite rule | pending |

## Authorization spot checks (regression, name the existing test as evidence)

| # | Item | Check | Status |
|---|---|---|---|
| A1 | Non-member gets 404 (not 403) for project, ticket, label, comment lookups and `/events` | Owning-phase tests | pending |
| A2 | Session-only endpoints (members, project delete, tokens, `/auth/me` writes, events) reject bearer tokens with 403 `session_required` | Phase 2 role/scope matrix test | pending |
| A3 | Read token cannot write over REST or see write tools over MCP; project-limited token rejected for other projects (REST 404, MCP "limited to project" message) | Phase 2 and Phase 4 tests | pending |
| A4 | Rate limit on login, signup and password change returns 429 with `Retry-After` | Phase 2 rate-limit test | pending |
| A5 | `ALLOW_SIGNUP=false`: `POST /api/v1/auth/signup` returns 404 `not_found`; `GET /api/v1/auth/config` returns only `{"signup_enabled": false}` | `curl -i -X POST -H 'Content-Type: application/json' -H 'Origin: http://localhost:8080' -d '{}' http://localhost:8080/api/v1/auth/signup`; `curl -s http://localhost:8080/api/v1/auth/config` | pending |
| A6 | Only the exact author, as editor or owner with write scope, can edit a comment | Phase 2 and Phase 4 tests | pending |
| A7 | Archived project: ticket, label and comment writes return 409 `project_archived` (REST) and the MCP archived message | Owning-phase tests | pending |
| A8 | Every `/api/v1` route has an explicit `Access` | Phase 2 `Server.Routes()` enumeration test passes | pending |
| A9 | `go vet ./...` clean; `CGO_ENABLED=0 go test ./...` passes; `govulncheck` run and recorded; `bun audit` run and recorded (Bun 1.2.15+); optional image scan | `go vet ./...`; `CGO_ENABLED=0 go test ./...`; `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`; `cd web && bun audit`; optional `trivy image pabrika` or `docker scout cves pabrika` | pending |
