# Phase 6: Ship

Detailed spec for phase 6 of [phases.md](phases.md). [main-spec.md](main-spec.md) is the source of truth and wins on any conflict; conflicts are flagged, not silently resolved. The confirmed decisions in the "Decisions (resolved open questions)" section of [phases.md](phases.md) override both and are applied here (see "Decisions applied" at the end).

**Goal:** one container that is safe and easy to run.

**Spec sections:** Layout, config and deployment; Testing; Security checklist.

## 1. Assumptions about phases 1 to 5

Phase 6 adds no features and no endpoints beyond what is listed here. It expects:

| From | Expectation |
|---|---|
| 1 | `CGO_ENABLED=0 go build ./...` and `CGO_ENABLED=0 go test ./...` pass (note: `go test -race` needs CGO, so race runs are a separate optional `CGO_ENABLED=1` run, not part of the no-CGO gate). Go module path is `github.com/RJGJ/Pabrika`, `go 1.23` in `go.mod`. Config parses `PORT`, `DB_PATH`, `BASE_URL` (trailing slash trimmed), `ALLOW_SIGNUP`, `COOKIE_SECURE`, and `TRUST_PROXY` (default `false`; the config table in README lists all six). Migrations are embedded and run at `serve` start and by the CLI user commands. `cmd/pabrika` already has a `version` subcommand (`var version = "dev"`) and a `Makefile` exists (Phase 1: `build`, `test`, `vet`, `generate`, optional `test-race`); `/healthz` and the Makefile base come from Phases 1 and 2, so this phase extends them, it does not create them. |
| 2 | `cmd/pabrika` has `serve`, `user create`, `user reset-password`, `healthcheck`. `serve` listens on `:PORT` (all interfaces, not loopback only). Phase 2 owns graceful shutdown on SIGINT/SIGTERM (`srv.Shutdown` with an 8 s deadline, then `srv.Close()`, then WAL checkpoint and store close, so `docker stop` exits 0); Task 3 only verifies it. The user commands (`user create`, `user reset-password`) run migrations before touching the DB, so they work on a fresh volume before `serve` has ever run. `GET /healthz` is built in Phase 2 (this phase only verifies it): unauthenticated, 200 when the DB answers. Auth, CSRF, rate limiting (using the first `X-Forwarded-For` hop only when `TRUST_PROXY=true`), body and field caps exist. Account endpoints exist: session-only `PATCH /auth/me` and `POST /auth/me/password`, and public `GET /auth/config` (`{"signup_enabled": bool}`). With `ALLOW_SIGNUP=false`, `POST /auth/signup` returns 404 `not_found`. A security-headers middleware exists (Phase 2 section 11: CSP with `base-uri 'none'`, `nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY`, `Permissions-Policy`, HSTS only for an `https://` `BASE_URL`). Phase 2 exposes `Server.Mount(pattern, Access, handler)` (guarded route), `Server.MountRaw(pattern, handler)` (global middleware only; `/mcp`) and `Server.SetFallback(handler)` (everything not under `/api/`, `/mcp`, `/healthz`; registered as method-less `/`). `user create` and `user reset-password` read the password only from `--password-stdin` or a TTY prompt (never an argument). |
| 3 | `GET /api/v1/projects/{id}/events` streams SSE with a 25 s keepalive, headers `Content-Type: text/event-stream`, `Cache-Control: no-store, no-transform` and `X-Accel-Buffering: no`, with a short per-frame write deadline (no server-wide `WriteTimeout`). Phase 3 owns the hub shutdown: `serve` calls `hub.Shutdown()` before `srv.Shutdown` (Phase 3 section 6); a new stream during shutdown gets 503 `unavailable`. |
| 4 | `/mcp` works with bearer tokens and exposes 14 tools (including `update_comment`). |
| 5 | The UI redirects `/signup` to `/login` and hides "Create account" when `signup_enabled` is false. `cd web && bun run build` writes a static bundle to `web/dist/` (`index.html` plus hashed files under `web/dist/assets/`), `base: '/'`, no inline scripts, no `eval`, no external network calls, scripts `dev`, `build`, `typecheck`, `test`. Vue Router uses `createWebHistory` with these client routes: `/`, `/login`, `/signup`, `/p/:key`, `/p/:key/t/:number`, `/p/:key/settings`, `/settings`, plus anything else (NotFoundView). Vite dev server on 5173 proxies `/api` only (not `/mcp`) to 8080. `web/bun.lock` (text lockfile, Bun 1.2+) is committed. The build keeps (or recreates) `web/dist/.gitkeep` (`emptyOutDir: false`, clean step skips it); `make web` ensures it either way. |

If any expectation is false, fix it in the owning phase's code as a small patch and note it in the PR; do not rebuild the feature here. Task 0 below turns this table into a checklist.

### Task 0: Verify the assumptions (first, before any new code)

Run and record (PR description):
- `grep -n "^go \|^toolchain" go.mod` (go line must be <= 1.23 to build in `golang:1.23`; if a dependency forced a newer `go` or `toolchain` line, bump the Dockerfile image tag to match and note why, instead of relying on toolchain auto-download in the build).
- `ls web/bun.lock` exists (if the repo has `bun.lockb`, switch the Dockerfile `COPY` to it and note it).
- `grep -rn "WriteTimeout" cmd internal` finds none set.
- `grep -rn "NewServer\|ListenAndServe" cmd` shows the listen address is `:` + port, not `127.0.0.1`.
- The top-level handler chain (recover, request log, security headers, body cap) wraps the **whole** mux, including `/healthz`, `/mcp`, `/.well-known/` and the SPA fallback, not only `/api/v1`. The cookie-and-Origin check applies only under `/api/` (Phase 2 section 8); `/mcp` has its own rule (Task 4). If headers only wrap `/api/v1`, move them outward (small patch).
- Every wrapper `ResponseWriter` implements `Unwrap()` and `Flush()` (Phase 2 and 3 require it; re-verify now because Task 1 adds a wrapper for ETag/cache headers).

## 2. Tasks

### Task 1: Embed the built UI

1. **Embed package lives in `web/`**, because `go:embed` cannot reach outside its own directory. Create `web/embed.go`:
   - `package web`
   - `//go:embed all:dist` on `var distFS embed.FS`
   - `func FS() (fs.FS, error)` returning `fs.Sub(distFS, "dist")`.
2. **Dev and CI builds without a bundle must still compile.** Commit `web/dist/.gitkeep` and add `web/dist/*` plus `!web/dist/.gitkeep` to `.gitignore`. "Bundle missing" is detected by the absence of `index.html` in the FS (not by the absence of other files). In that case the handler (below) serves a plain 503 page ("UI not built. Run `cd web && bun run build`.", `Cache-Control: no-store`) for non-API paths; API and MCP still work, and `serve` logs one warning at startup. Phase 5's build is configured to keep `.gitkeep` (`emptyOutDir: false`, clean step skips it) or recreate it; as a safety net the Makefile `web` target also ensures it after the build (`touch web/dist/.gitkeep`), and README/CLAUDE.md note `git checkout web/dist/.gitkeep` if it shows as deleted. (The Docker build does not care: `web/dist` is excluded from the context and copied in from the Bun stage.)
3. **Wire-up in `cmd/pabrika` `serve`:** `fsys, err := web.FS()`, `httpapi.NewSPAHandler(fsys)`, installed with Phase 2's `Server.SetFallback(handler)` (the existing hook for everything outside `/api/`, `/mcp` and `/healthz`; it runs inside the global middleware, so headers and body cap apply, and it needs no `Mount` entry or auth resolution). The SPA handler is registered by Phase 2 as the method-less `/` pattern. Route precedence (Go 1.22 mux, most specific pattern wins): `/api/v1/...` route table and the `/api/` JSON-404 catch-all, `/mcp`, `/healthz`, then the SPA `/`. **The catch-all is method-less `/`** (not `GET /`): a `GET /` pattern would make the mux answer unmatched `POST`s with its own 405 and `Allow: GET, HEAD` instead of the JSON 404. Method filtering happens inside the handler.
4. **New handler `internal/httpapi/spa.go`**, mounted last (lowest priority):
   - Takes an `fs.FS`, so tests can pass `fstest.MapFS`.
   - Path cleaned with `path.Clean` after stripping the leading `/`; never serves outside the FS; requests for `index.html` itself are served directly (do not use `http.FileServer`, which redirects `/index.html` to `/`; use `http.ServeContent` on the opened file, which handles `Content-Type`, `If-None-Match`, `Range` and `HEAD`).
   - If the path names an existing file in the FS, serve it.
   - Otherwise serve `index.html` with 200 (SPA fallback for `/p/WEB`, `/p/WEB/t/12`, `/p/WEB/settings`, `/settings`, `/login`, `/signup` and any unknown client route, which the SPA renders as its own not-found view).
   - **Never fall back for API-like paths:** anything equal to or under `/api/`, `/mcp`, `/healthz` and `/.well-known/` (prefix match on `/api/` and `/.well-known/`, exact or `/`-suffixed match for `/mcp` and `/healthz`) that did not match a route must return the JSON 404 error shape (`{"error":{"code":"not_found",...}}`), not HTML.
   - **Never fall back for file-like paths:** a missing path under `/assets/`, or any missing path whose last segment has a file extension (`/foo.js`, `/favicon.ico`, `/robots.txt`), returns a plain 404 (a stale hashed asset must not return HTML with 200). Client routes have no dots (keys are letters, ticket numbers are digits).
   - Only `GET` and `HEAD` are served; other methods on non-API paths return 405 with `Allow: GET, HEAD` (JSON error shape, code `method_not_allowed`).
   - **MIME types:** the distroless image has no `/etc/mime.types`, and Go's built-in table lacks several web types. In an `init()` register `mime.AddExtensionType` for `.woff2` (`font/woff2`), `.woff`, `.ico` (`image/x-icon`), `.webmanifest`, `.map` (`application/json`), and ensure `.js`/`.mjs` are `text/javascript` and `.css` is `text/css` (needed because `nosniff` is set). Test that the content types come out right with `CGO_ENABLED=0` and no system mime files.
5. **Cache headers**
   - `index.html` (and any SPA fallback response): `Cache-Control: no-cache` (always revalidate; it names the hashed assets).
   - `/assets/*` (Vite hashed names): `Cache-Control: public, max-age=31536000, immutable`.
   - Other root files (favicon, `robots.txt`): `Cache-Control: public, max-age=3600`.
   - API, MCP and event responses: `Cache-Control: no-store` for `/api/v1` and `/mcp` (Phase 2 sets it on `/api/v1`; verify, and add for `/mcp`). The events handler's own `Cache-Control: no-store, no-transform` (Phase 3) is the one exception and must not be overwritten by the middleware; `no-transform` is wanted so intermediaries do not compress or rewrite the stream. `/healthz`: `no-store`.
   - **The app does no response compression** (no gzip middleware anywhere; a compressing, buffering wrapper would stall SSE and `/mcp` streaming). Compression is the proxy's job, excluding the events path (Task 5). Check: `grep -rni "gzip\|compress" internal cmd` finds nothing in the serving path.
   - ETag: embedded files have no mtime, so compute a strong ETag from the content hash at startup for `index.html` (set before `ServeContent`, so `If-None-Match` yields 304); hashed assets may skip it.
6. **Optional pre-compression:** not required. If the bundle is large, serving `.gz` siblings is a follow-up, not part of this phase.
7. **Dev vs prod**
   - Prod: `pabrika serve` serves the embedded FS. No flags needed.
   - Dev: run Go (`go run ./cmd/pabrika serve` with `COOKIE_SECURE=false`) and `bun run dev` in `web/`; the browser uses `http://localhost:5173`, Vite proxies `/api` only (not `/mcp`) to 8080. The embedded FS is not used.
   - `BASE_URL` for dev through Vite: the Origin check sees `http://localhost:5173`, so dev needs `BASE_URL=http://localhost:5173` (decided; document it in README and CLAUDE.md, including the full dev command `BASE_URL=http://localhost:5173 COOKIE_SECURE=false go run ./cmd/pabrika serve`).
   - No `WEB_DIR` disk-serving option; not added.

**Tests (`internal/httpapi/spa_test.go`, `fstest.MapFS`)**
- `GET /` returns index with `no-cache`.
- `GET /assets/app-abc.js` returns the file with the immutable header and a JS content type.
- `GET /p/WEB/t/12` returns index (200).
- `GET /assets/missing.js` returns 404, not index.
- `GET /api/v1/nope`, `GET /api/other`, `GET /mcp/unknown`, `GET /healthz/x`, `GET /.well-known/oauth-authorization-server` and `GET /.well-known/anything` return the JSON 404 shape (not HTML); `POST /api/v1/nope` returns the JSON 404 (not a mux 405); `GET /mcpx` (not an API prefix) returns index.
- `GET /foo.js` and `GET /favicon.ico` missing return 404, not index.
- `POST /` returns 405 with `Allow: GET, HEAD`; `HEAD /` returns headers and no body.
- `GET /../etc/passwd`, `GET /%2e%2e/secret` and `GET //evil` style paths cannot escape the FS.
- `GET /index.html` returns 200 (no redirect); `If-None-Match` with the index ETag returns 304.
- Content types for `.js`, `.css`, `.woff2`, `.svg`, `.ico` are correct without system mime files.
- Empty FS (only `.gitkeep`) returns the 503 "UI not built" page for `/` and `/p/WEB`, while the JSON 404 for `/api/...` is unchanged.
- Integration (real mux with routes plus SPA, `httptest`): precedence holds: `/healthz` is the health JSON, `/api/v1/auth/config` the JSON, `/mcp` without a token the 401 JSON, `/.well-known/x` the JSON 404, `/p/WEB` the SPA; the SPA is reached through `SetFallback` (a non-API path hits it, an `/api/` path never does).

### Task 2: Dockerfile and `.dockerignore`

Start from the Dockerfile in main-spec and keep it; tighten as below.

1. **Stage `web`**: `FROM oven/bun:1 AS web`. Copy `web/package.json` and `web/bun.lock` first, `bun install --frozen-lockfile`, then copy `web/` and `bun run build`. Output `/web/dist`. Keep the floating `oven/bun:1` tag (decided).
2. **Stage `server`**: `FROM golang:1.23 AS server` (kept as decided; must be >= the `go` line in `go.mod`; keep them in sync, see Task 0). `ARG VERSION=dev`. `go mod download` before `COPY . .` for layer caching. `COPY --from=web /web/dist ./web/dist` so `go:embed all:dist` finds it (this must come after `COPY . .`). Build with `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /pabrika ./cmd/pabrika` (use the variable name Phase 1 actually defined for the version string; `main.version` if none). Run `mkdir /data` in this stage. `.git` is not in the build context, so pass the version explicitly: `docker build --build-arg VERSION=$(git describe --tags --always) -t pabrika .` (the Makefile `docker` target does this).
3. **Final stage**: `gcr.io/distroless/static-debian12:nonroot` (includes CA certificates and tzdata; no shell, no mime.types).
   - `COPY --from=server /pabrika /pabrika`
   - `COPY --from=server --chown=nonroot:nonroot /data /data` (a **named** volume first mounted on an empty volume inherits this ownership, so SQLite can write; a **bind mount** does not, see README "Backups" and "Quick start" notes: `chown 65532:65532` the host directory).
   - `ENV DB_PATH=/data/pabrika.db PORT=8080`
   - `VOLUME /data`, `EXPOSE 8080`, `USER nonroot:nonroot` (explicit even though the tag defaults to it).
   - `LABEL org.opencontainers.image.source="https://github.com/RJGJ/Pabrika" org.opencontainers.image.licenses="MIT"`
   - `HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/pabrika", "healthcheck"]`
   - `ENTRYPOINT ["/pabrika", "serve"]` (exec form, so the binary is PID 1 and receives SIGTERM directly; Go installs its own SIGTERM handler, so PID 1's default "ignore" behaviour does not apply, and the smoke test proves it).
   - No `CMD`. CLI use is `docker exec pabrika /pabrika ...`, or a one-off container with **`--entrypoint /pabrika`** (without it, `docker run pabrika user create` would run `serve user create`).
4. **`pabrika healthcheck`** (implemented in phase 2; verify here): calls `http://127.0.0.1:$PORT/healthz` (not `BASE_URL`, which may be the public HTTPS URL), 3 s timeout, exit 0 on 200, exit 1 otherwise, no output on success.
5. **`.dockerignore`**: `.git`, `data/`, `*.db*`, `web/node_modules`, `web/dist` (rebuilt in-image), `.claude`, `specs`, `plans`, `*.md` (nothing in the build reads them), IDE files, the built `pabrika` and `pabrika.exe` binaries. `go.sum`, `go.mod`, `web/bun.lock`, `web/embed.go`, `migrations/` (including `*.sql`) must NOT be ignored (check: `docker build` succeeds and `docker run --rm --entrypoint /pabrika pabrika version` runs).
6. **No Bun or Node in the final image.** Verified by the smoke test (see section 5).
7. **Reproducibility note:** `oven/bun:1` and `golang:1.23` are floating tags (decided); record the image digests used for the release build in the PR (`docker image inspect --format '{{index .RepoDigests 0}}'`), no pinning required.

### Task 3: Container contract (verify, do not widen)

The built image must satisfy, and the smoke test checks each:

- One container, one process, one exposed port 8080. No compose file, no other services.
- All state in `/data`; `DB_PATH=/data/pabrika.db`. The rest of the filesystem is not written to (run-time check with `docker run --read-only -v pabrika-data:/data --tmpfs /tmp`; must work).
- Runs as non-root (UID 65532): `docker inspect --format '{{.Config.User}}' pabrika` prints `nonroot:nonroot`.
- `/data` writable on first run with a fresh named volume.
- Image has no shell: `docker exec pabrika sh` fails; CLI commands run as `docker exec -i pabrika /pabrika user create ... --password-stdin` (`-i` so stdin reaches the process; the password is never an argument).
- **Graceful shutdown is owned by Phases 2 and 3; this task only verifies it and patches anything missing.** Phase 2 owns `srv.Shutdown` with an **8 s** deadline (shorter than Docker's default 10 s stop grace, otherwise Docker sends SIGKILL and the container exits 137), then `srv.Close()` if the deadline passes (forces any remaining connection, for example a long-lived `/mcp` response, to end), stop of the session purge goroutine, then `PRAGMA wal_checkpoint(TRUNCATE)` and store close, then exit 0. Phase 3 owns `hub.Shutdown()`, called first so SSE streams end; a stream opened during shutdown gets 503 `unavailable` (an allowed status). **Verify:** `docker stop` exits 0 in under 10 s with an open SSE stream (`docker inspect --format '{{.State.ExitCode}}' pabrika` prints `0`, not `137`). If the order, the deadline, the `srv.Close()` fallback or the checkpoint is missing in `serve`, patch it (small patch, noted in the PR). A second signal during shutdown exits immediately. Verification test: a Go test starts the real `serve` wiring (or the server plus hub) on a random port, opens an SSE stream, cancels the signal context, and asserts return within 8 s with the stream at EOF; reuse Phase 2's and 3's shutdown tests where they already cover this and say so in the PR.
- Logs to stdout/stderr only (structured `slog`). At startup `serve` logs one line with version, listen address, `BASE_URL`, `ALLOW_SIGNUP`, `TRUST_PROXY`, `COOKIE_SECURE` and DB path (no secrets), and warns when it sees likely misconfiguration: `BASE_URL` starting with `http://` while `COOKIE_SECURE=true` and the host is not `localhost`/`127.0.0.1` (the cookie will never be stored), `BASE_URL` starting with `https://` while `COOKIE_SECURE=false`, and `TRUST_PROXY=true` (reminder that the port must not be publicly reachable). Warnings only, never fatal.
- A second container on the same volume is unsupported (the event hub is in memory); the README says so.

### Task 4: Security checklist final audit

Audit every item, fix gaps, and add or confirm an automated test where noted. Each item is a pass/fail line in the PR description, with the evidence named in its `Check` (command output or test name). `make audit` (Task 9) runs the grep and tool checks in one go.

**Headers** (outermost middleware, applied to every response from the top-level mux: `/api/v1`, `/mcp`, `/healthz`, static files, SPA fallback, 404/405/429/500 errors)
- [ ] `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'` (must equal Phase 2 section 11 character for character, with `base-uri 'none'` per the Provisional decisions; Phase 2 owns the middleware and this phase audits it, so any mismatch is fixed in Phase 2's code and header test, not by a second middleware). Tighten or loosen only if the real bundle breaks (record why). No `unsafe-eval`, no `unsafe-inline` for scripts. `style-src 'unsafe-inline'` is a confirmed decision (component library and toast inline styles); a nonce-based style policy is a follow-up. **Check:** serve the real built bundle from the Go binary, open `/login`, `/`, a board, a ticket panel, project settings and account settings in a browser with the devtools console open, and confirm zero CSP violations (a violation logs "Refused to ..."); `grep -c "<script" web/dist/index.html` shows only `src=` scripts (no inline body); `grep -rl "eval(\|new Function" web/dist/assets` finds nothing from app code (record any library hit and why it is inert).
- [ ] `X-Content-Type-Options: nosniff`
- [ ] `Referrer-Policy: same-origin` (Provisional decision; Phase 2 section 11 must carry the same value; keeps tokens in paths out of cross-origin referrers; external links also carry `noreferrer`)
- [ ] `X-Frame-Options: DENY` (belt and braces with `frame-ancestors`; in scope, confirmed)
- [ ] `Permissions-Policy: camera=(), microphone=(), geolocation=()` (in scope, confirmed)
- [ ] `Strict-Transport-Security: max-age=31536000` is set by the reverse proxy (documented, confirmed); **the app also sets it only when `BASE_URL` starts with `https://`** (decision wording; no dependency on `COOKIE_SECURE`), no `includeSubDomains`. Phase 2's middleware owns this logic; audit that it matches (patch Phase 2's code if it also requires `COOKIE_SECURE`). The proxy snippets avoid sending it twice (Caddy `header` replaces; nginx uses `proxy_hide_header`). **Check:** header test with `BASE_URL=http://...` has no HSTS, with `https://...` has exactly one.
- [ ] SSE response keeps these headers plus its own `Cache-Control: no-store, no-transform` (not overwritten by the `no-store` middleware) and `X-Accel-Buffering: no`. **Check:** the header test opens the events stream on a real `httptest.Server` and asserts the full set.
- [ ] No CORS: **Check:** `curl -si -H 'Origin: https://evil.example' http://localhost:8080/api/v1/auth/config | grep -ci '^access-control'` prints `0`; `OPTIONS /api/v1/projects` returns the JSON 405/404, not a preflight approval.
- [ ] Every header value above is compared against Phase 2 section 11 verbatim (the test imports or shares the constants, so the two cannot drift).
- Test: one table-driven test hits `/`, `/p/WEB`, `/api/v1/auth/me` (401), `/api/v1/auth/config`, `/healthz`, `/.well-known/x` (404), `/assets/x.js`, `/assets/missing.js` (404), `/mcp` without a token (401), `POST /` (405), a 429, a 503 `unavailable` and a forced 500, plus the SSE stream, and asserts the header set on each.

**Secrets at rest and in comparison**
- [ ] Session tokens: only SHA-256 hash stored (`sessions.token_hash`); raw value only in the cookie. **Check (test):** after login, the raw cookie value is not found in any text column of any table (iterate `sqlite_master`, `SELECT *`, substring match). **Check (smoke):** `sqlite3 backup.db .dump | grep -c "<cookie value>"` prints `0`.
- [ ] API tokens: only `token_hash` stored; the full token is returned once on create and never again (list and get never include it; `token_prefix` is 8 chars). **Check (test):** same DB scan for the secret; `GET /tokens` body does not contain it.
- [ ] Token and session lookups by hash use the DB unique index; where a Go-side comparison of secrets or hashes exists (for example an equality check after lookup), it uses `crypto/subtle.ConstantTimeCompare`. **Check:** `grep -rnE "(==|!=) *[a-zA-Z_.]*([Hh]ash|[Tt]oken|[Ss]ecret)" internal/auth internal/httpapi internal/mcpserver --include=*.go | grep -v _test` has no secret-vs-secret comparison (empty-string and nil checks are fine; list and justify each hit).
- [ ] Passwords: argon2id, 64 MiB, 3 passes, 2 threads, PHC string; verification via constant-time compare; login does equal work when the email is unknown (dummy hash) so timing does not reveal accounts. **Check:** Phase 2's argon2 PHC-param and dummy-hash tests pass; `grep -rn "argon2.IDKey" internal` shows only the `password.go` call sites, reading params from one place.
- [ ] Generic login error for unknown email and wrong password (same status, code and message). **Check:** Phase 2's login test passes.
- [ ] Password change (`POST /auth/me/password`): session-only (bearer tokens rejected with 403 `session_required`), rate limited (429), verifies `current_password` (wrong value is 422 `fields.current_password`, never 401), same argon2id and 10-character rules as signup, deletes the user's other sessions and keeps the current one (API tokens are not revoked). **Check (test):** after a change, a second session's cookie gets 401 and the current session still works. `PATCH /auth/me` is session-only too.
- [ ] SQL: every query is a sqlc-generated parameterized statement; no string-built SQL. **Check:** `grep -rniE "fmt\.Sprintf\(.*(select|insert|update|delete) " internal | grep -v _test` is empty; `LIKE` filters escape `%`, `_`, `\` (Phase 1 list test with literal `%`).

**Log redaction**
- [ ] No log line contains `Authorization` header values, `Cookie`/`Set-Cookie` values, passwords, or full tokens. Request logger logs method, route pattern or path, status, duration, request id, user id (and token id for token calls) and client IP only.
- [ ] `pb_` tokens and query strings: no tokens in URLs anywhere; the access log logs the path without the query string. **Check:** `grep -rniE "slog\.|log\.(Print|Fatal)" internal cmd --include=*.go | grep -iE "authorization|cookie|password|secret|token\)" | grep -v _test` is empty or each hit is justified (token *id* and *name* are fine, token *value* is not).
- [ ] Panic recovery logs do not dump request headers (stack and request id only).
- [ ] Startup config log contains no secret (there are none in the config; keep it that way).
- [ ] Test: run a request with a bearer token, a request with a session cookie, and a login with a password through the full handler (including `/mcp`) with a capturing logger; force a panic once; assert the captured output contains none of the token, cookie value or password.

**Body and field caps**
- [ ] Every request body wrapped in `http.MaxBytesReader` at 1 MiB (including `/mcp`); oversize returns 400 `body_too_large` in the standard error shape. **Check:** test posts 1 MiB + 1 byte to `/api/v1/tickets` route, `/api/v1/auth/login` and `/mcp`.
- [ ] Field limits, counted in characters (runes), enforced in the shared service `Validate()` (values from Phase 2 section 11 and Phase 1, which are authoritative; fix the code, not these numbers, if they differ): email 254, display name 1 to 100, password 10 to 200, project key `^[A-Z]{2,6}$`, project name 1 to 100, project description 2,000, ticket title 1 to 200, ticket description 20,000, comment body 1 to 20,000 (REST and MCP `add_comment` and `update_comment`), label name 1 to 50, token name 1 to 100, labels per ticket 50. **Check:** a table test over every field at limit and limit+1 (display name at 100 and 101, and 0), including a multibyte string at the boundary.
- [ ] The same limits apply through MCP (shared structs/validation). **Check:** the schema drift test from Phase 4 passes; an MCP `create_ticket` with a 201-character title returns the "too long" tool error.
- [ ] Bodies must be `application/json`; other content types on POST/PATCH/DELETE with a body return 415 `unsupported_media_type`. **Check:** `curl -s -X POST -H 'Origin: http://localhost:8080' -H 'Content-Type: text/plain' -d x http://localhost:8080/api/v1/auth/login -w '%{http_code}'` ends in `415`.
- [ ] HTTP server timeouts: `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `IdleTimeout` 120 s, and **no `WriteTimeout`** (Phase 2 and 3 decision: it would cut SSE and `/mcp` streaming; the events handler also clears its own write deadline). Residual risk: a client that stops reading a normal response holds a goroutine until the connection drops; accepted for v1 (single-user-scale tool behind a proxy that has its own timeouts), noted in README security notes. **Check:** `grep -rn "WriteTimeout" cmd internal` finds none set; a test holds an SSE stream open for longer than `ReadTimeout` (use a shortened test value) and still receives a keepalive.
- Tests: oversize body, over-length title, over-length description, multibyte boundary.

**Markdown sanitization (client)**
- [ ] The UI renders markdown with raw HTML disabled (for example `markdown-it` with `html: false`) and the output passed through DOMPurify with a tight allowlist; links get `rel="noopener noreferrer nofollow"` and `target="_blank"`; only `http`, `https`, `mailto` URL schemes (no `javascript:`, no `data:`); images are dropped in v1.
- [ ] No `v-html` anywhere in `web/src` except the single sanitized renderer component. **Check:** `grep -rn "v-html" web/src` lists exactly `MarkdownView.vue`; also `grep -rn "innerHTML" web/src` is empty.
- [ ] Phase 5's unit test covers payloads `<script>`, `<img onerror>`, `[x](javascript:alert(1))`, `<iframe>`, SVG with script, `![x](http://a/b.png)` (image dropped), `[x](data:text/html,...)`; add any missing case here. All neutralized. **Check:** `cd web && bun run test`.
- [ ] Ticket and comment content is stored raw on the server (no server-side HTML transform); CSP is the second layer.

**Cookies and proxy**
- [ ] Session cookie: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` unless `COOKIE_SECURE=false`, 30-day sliding `Max-Age`. No `Domain` attribute. **Check:** Phase 2 cookie test passes; smoke step 9 shows the real `Set-Cookie` over HTTPS (`curl -skI` / `-c jar`, then read the jar).
- [ ] Behind a TLS proxy the container sees plain HTTP, so `Secure` must come from `COOKIE_SECURE` (default `true`), not from `r.TLS`. **Check (test):** request over plain HTTP with default config still emits `Secure`.
- [ ] Origin check compares against `BASE_URL`'s scheme, host and port, not the `Host` header, normalizing default ports (`Origin: https://host` equals `BASE_URL=https://host:443`, host case-insensitive). **Check (tests):** wrong Origin on POST/PATCH/DELETE returns 403 `origin_mismatch`; `Origin: null` 403; missing Origin on a cookie-authenticated or unauthenticated unsafe request is 403 (login CSRF); a bearer request with no Origin passes; a bearer request with a **mismatching** Origin on `/api/v1` is still 403 (Phase 2 section 8); `Host` header spoofing does not change the outcome.
- [ ] `/mcp` Origin policy is one explicit, tested rule (Provisional decision, matches Phases 2 and 4): `/mcp` ignores cookies and requires a bearer token (401 without one, even if a valid session cookie is sent); if an `Origin` header is present it must match `BASE_URL` (403 `origin_mismatch` otherwise, per the MCP transport spec's DNS-rebinding guidance); an absent `Origin` (every non-browser client, including Claude Code) is allowed. Consequence: browser-origin MCP clients are rejected. **Check (test):** cookie-only request 401; mismatching Origin with a valid bearer 403; matching Origin and absent Origin with a valid bearer pass. `GET /mcp` and `DELETE /mcp` return 405 from the stateless SDK handler, which is expected.
- [ ] Do not trust `X-Forwarded-*` for security decisions (Secure flag, Origin). `TRUST_PROXY` (default `false`) controls client IP only: the rate limiter and logs use the first `X-Forwarded-For` hop only when it is `true`; when it is off, `X-Forwarded-For` is never trusted and the socket peer address is used. **Check (tests):** with it off, a spoofed `X-Forwarded-For` does not change the rate-limit bucket; with it on, it does. The README states the corollary: with `TRUST_PROXY=true` the proxy must **overwrite** `X-Forwarded-For` rather than append to it, or a client can spoof the first hop (the shipped Caddy and nginx snippets do).

**Authorization spot checks (regression, from phase 2 to 4)** (each is an existing test from the owning phase; name the test in the PR, or add a small one here if none exists)
- [ ] Non-member gets 404 (not 403) for project, ticket, label, comment lookups, and for `/events`.
- [ ] Session-only endpoints (members, project delete, tokens, `/auth/me` writes, events) reject bearer tokens (403 `session_required`).
- [ ] Read token cannot write over REST or see write tools over MCP; project-limited token rejected for other projects (REST 404, MCP "limited to project" message).
- [ ] Rate limit on login, signup and password change returns 429 with `Retry-After`.
- [ ] With `ALLOW_SIGNUP=false`, `POST /api/v1/auth/signup` returns 404 `not_found` (not 403) and `GET /api/v1/auth/config` returns only `{"signup_enabled": false}`.
- [ ] Only editors/owners with write scope can edit a comment, and only its exact author (`update_comment` cannot edit a human's comment; a human cannot edit an agent's).
- [ ] Archived project: ticket, label, comment writes return 409 `project_archived` over REST and the MCP archived message.
- [ ] Every `/api/v1` route has an explicit `Access` (Phase 2's `Server.Routes()` enumeration test passes).
- [ ] `go vet ./...` clean; `CGO_ENABLED=0 go test ./...` passes (the race run is optional, `CGO_ENABLED=1`, needs a C compiler); `govulncheck` (`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`) run and results recorded (fix or justify). `cd web && bun audit` run (needs Bun 1.2.15+) and results recorded. Optional: `trivy image pabrika` or `docker scout cves pabrika`, recorded, not a gate.

### Task 5: Reverse proxy notes (in README, section "Running behind a proxy")

Content requirements:
- TLS must terminate at the proxy; the container speaks plain HTTP on 8080. Do not expose 8080 publicly: publish it on loopback only (`-p 127.0.0.1:8080:8080`; Docker's published ports bypass host firewalls such as ufw) or put the proxy on the same Docker network and do not publish it at all.
- Set `BASE_URL` to the public HTTPS URL exactly (scheme, host, port; no path, no trailing slash); it drives the Origin check and MCP links. A mismatch shows up as 403 `origin_mismatch` on every login.
- Keep `COOKIE_SECURE=true` (default); the app does not infer it from the connection.
- Set `TRUST_PROXY=true` so the rate limiter and logs see the real client IP; otherwise every client shares the proxy's IP and the 5-per-minute login limit applies to everyone together. The app uses the **first** `X-Forwarded-For` hop, so the proxy must overwrite the header, not append to what the client sent (both snippets below do; Caddy does this by default for untrusted clients, nginx via `$remote_addr`). Leave it `false` if the container is reachable without a proxy, since clients could spoof the header. A CDN in front of the proxy is not covered (the first hop would be the CDN's).
- HSTS: set `Strict-Transport-Security: max-age=31536000` at the proxy (the app also sends it when `BASE_URL` is HTTPS; the snippets make sure it is sent once).
- **Compression belongs to the proxy, and must exclude the event stream** (a buffering compressor stalls it): Caddy's `encode` must exclude the SSE path (the snippet's `@compressible` matcher does), and nginx uses `gzip off` there. The app sends `Cache-Control: no-transform` on the stream.
- The events path `/api/v1/projects/*/events` must not be buffered, and read timeouts must exceed the 25 s keepalive (set 1 h on that path). Browsers allow about 6 HTTP/1.1 connections per origin and every open board holds one stream, so recommend HTTP/2 at the proxy (the snippets enable it; Caddy does by default) or the 7th open tab will hang.
- `/mcp` may answer with a streamed (SSE) body for some requests, so do not buffer it either; request bodies are small JSON (1 MiB cap).
- Container memory: password hashing uses 64 MiB per hash and up to 4 run at once; give the container at least 512 MB (`--memory 512m`).

Caddy (streams by default; `flush_interval -1` is an explicit safe setting; compression is applied to everything except the events path):

```caddyfile
pabrika.example.com {
	@compressible not path_regexp ^/api/v1/projects/[^/]+/events$
	encode @compressible gzip zstd
	header Strict-Transport-Security "max-age=31536000"
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

nginx (`proxy_buffering off` on the events path and `/mcp`; the app's own HSTS header is hidden so it is sent once):

```nginx
server {
    listen 443 ssl;
    http2 on;                      # nginx 1.25.1+; on older versions use: listen 443 ssl http2;
    server_name pabrika.example.com;
    # ssl_certificate / ssl_certificate_key ...

    client_max_body_size 1m;
    proxy_hide_header Strict-Transport-Security;
    add_header Strict-Transport-Security "max-age=31536000" always;

    gzip on;
    gzip_types text/css application/javascript application/json image/svg+xml;

    location ~ ^/api/v1/projects/[^/]+/events$ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        gzip off;
    }

    location = /mcp {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_buffering off;
        proxy_read_timeout 3600s;
        gzip off;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

(`add_header` inside a `location` replaces inherited ones, but these locations add none, so the server-level HSTS applies everywhere.)

Verification (put the same commands in the README so users can check their own proxy). Log in with a cookie jar, then stream; replace the host, email and password, and `WEB` with a project key:

```bash
curl -sk -c jar.txt -H 'Content-Type: application/json' -H 'Origin: https://pabrika.example.com' \
  -d '{"email":"you@example.com","password":"..."}' https://pabrika.example.com/api/v1/auth/login
curl -skN -b jar.txt https://pabrika.example.com/api/v1/projects/WEB/events \
  | while IFS= read -r line; do echo "$(date +%T) $line"; done
```

Expected: `retry: 3000` and `: connected` immediately, a `: keepalive` line about every 25 s, and a `event: ticket.created` frame the moment a ticket is created in another window. Lines arriving in bursts or late means something is buffering. Also confirm `/mcp` through the proxy: `curl -sk -X POST https://pabrika.example.com/mcp -H 'Authorization: Bearer pb_...' -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'` returns a result (stateless mode needs no `initialize` first; if the SDK version insists on one, send `initialize` before it, per the SDK).

### Task 6: Backup notes (in README, section "Backups")

- **What to back up:** the single SQLite file `pabrika.db` in `/data` (plus nothing else). The app runs SQLite in WAL mode (recent writes live in `pabrika.db-wal`, with a `-shm` index), so **do not `cp` the file while the app runs**: you would get a torn or stale copy. Two safe ways: use SQLite's online backup (below), or stop the container with `docker stop` (graceful shutdown checkpoints the WAL, so the `.db` file alone is then complete) and copy.
- **Online backup with `sqlite3`.** The Pabrika image has no shell and no sqlite3, so run the backup from a throwaway container that mounts the same volume while Pabrika is running (the sqlite3 CLI and the app share the WAL index through the volume; this works for local Docker volumes, not for network filesystems):
  ```bash
  docker run --rm -v pabrika-data:/data -v "$PWD":/backup alpine \
    sh -c 'apk add --no-cache sqlite >/dev/null && sqlite3 /data/pabrika.db ".backup /backup/pabrika-$(date +%F).db"'
  ```
  On a Linux host with the volume path readable you can instead run `sqlite3 /var/lib/docker/volumes/pabrika-data/_data/pabrika.db ".backup '/backups/pabrika-$(date +%F).db'"` directly (that path does not exist on Docker Desktop for Windows or Mac; use the helper container there). `alpine` is only an example of an image you can install sqlite3 into; any image with sqlite3 works. Run the helper only while Pabrika is running (as root, it could otherwise create root-owned `-wal`/`-shm` files that the app cannot open; if you must back up a stopped instance, copy the single `.db` file instead).
- **Check a backup:** `sqlite3 pabrika-YYYY-MM-DD.db "PRAGMA integrity_check; SELECT count(*) FROM tickets; SELECT count(*) FROM users;"` prints `ok` and plausible counts. Do this once at setup and periodically (an untested backup is not a backup).
- **Restore:** stop and remove the container, replace the file in the volume, delete stale `-wal` and `-shm`, fix ownership, start:
  ```bash
  docker stop pabrika && docker rm pabrika
  docker run --rm -v pabrika-data:/data -v "$PWD":/backup alpine \
    sh -c 'rm -f /data/pabrika.db-wal /data/pabrika.db-shm && cp /backup/pabrika-YYYY-MM-DD.db /data/pabrika.db && chown 65532:65532 /data/pabrika.db'
  docker run -d --name pabrika ...   # same run command as before
  ```
  Migrations run at startup, so restoring an older backup into a newer image works (the schema is upgraded); restoring a newer database into an older image is not supported.
- **Bind mounts:** if you use `-v /srv/pabrika:/data` instead of a named volume, the host directory must be owned by UID 65532 (`sudo chown 65532:65532 /srv/pabrika`); named volumes get this automatically from the image. On Docker Desktop for Windows or Mac prefer a named volume: WAL needs a real local filesystem with working shared memory and file locking (host folders shared through the VM, NFS and SMB can corrupt or lock the database).
- **Continuous replication:** Litestream, run as a separate process or sidecar container mounting the same volume, replicating `/data/pabrika.db` to S3-compatible storage. Example `litestream.yml`:
  ```yaml
  dbs:
    - path: /data/pabrika.db
      replicas:
        - url: s3://my-bucket/pabrika
  ```
  Run it with `litestream replicate -config litestream.yml`; restore with `litestream restore -config litestream.yml -o /data/pabrika.db /data/pabrika.db` (with Pabrika stopped, then fix ownership as above). State clearly: this is optional, not shipped in the image, and not a compose file (out of scope).
- Suggest a cron line for daily backups (for example `0 3 * * * cd /srv/backups && docker run --rm ... && find . -name 'pabrika-*.db' -mtime +14 -delete`) and a restore test on a scratch volume.
- Data in backups includes password hashes (argon2id) and API token hashes; treat backups as sensitive, encrypt them if they leave the machine.
- Upgrades: back up first; `docker pull`/rebuild, `docker stop`, `docker rm`, `docker run` again with the same volume. Migrations apply on start; rollback means restoring the backup.

### Task 7: README

Create or replace `README.md` (repo root of `pabrika/`). Outline:

1. **What it is**: two sentences plus a screenshot placeholder (screenshot optional).
2. **Quick start (Docker)**: `docker build --build-arg VERSION=dev -t pabrika .`, then `docker run -d --name pabrika --restart unless-stopped -p 8080:8080 -v pabrika-data:/data -e BASE_URL=http://localhost:8080 -e COOKIE_SECURE=false pabrika`, open `http://localhost:8080`, first signup. Explain `COOKIE_SECURE=false` (needed for plain HTTP; some browsers drop `Secure` cookies on `http://`, and the app never infers it) and that production behind HTTPS uses the defaults (see section 6). Note bind mounts need `chown 65532:65532`, and that Podman and Kubernetes ignore the image `HEALTHCHECK` (use their own probes against `/healthz`).
3. **Create the first user from the CLI** and the `ALLOW_SIGNUP=false` workflow (signup endpoint returns 404 and `/signup` redirects to `/login`; the CLI still works). The password is never an argument; pipe it: `printf '%s\n' 'a-long-password' | docker exec -i pabrika /pabrika user create --email you@example.com --name "You" --password-stdin` (use the exact flags as implemented in phase 2; check with `docker exec pabrika /pabrika user create -h`; the password is never an argument), or `docker exec -it pabrika /pabrika user create --email ... --name ...` to be prompted. The CLI commands apply migrations themselves, so they also work on a fresh volume before `serve` has run; on a fresh install with signup disabled, still start the container first so the app is up, then run the command. One-off container variant: `docker run --rm -i -v pabrika-data:/data --entrypoint /pabrika pabrika user create ...` (the `--entrypoint` is required). Password reset (revokes sessions, not API tokens), flags before the positional email (Go's flag parsing stops at the first positional argument): `printf '%s\n' 'new-long-password' | docker exec -i pabrika /pabrika user reset-password --password-stdin you@example.com`; account settings (display name, change password; change password signs out other sessions).
4. **Configuration table**: all six env vars (`PORT`, `DB_PATH`, `BASE_URL`, `ALLOW_SIGNUP`, `COOKIE_SECURE`, `TRUST_PROXY` default `false`, "set true behind a reverse proxy") with defaults and meanings, plus a note on container-fixed values (`DB_PATH=/data/pabrika.db`, `PORT=8080` baked into the image; change the host port with `-p 9090:8080` and update `BASE_URL` to match; the healthcheck reads the container's own `$PORT`, so it keeps working if you do set `PORT`).
5. **Connect an agent over MCP**: create token in Settings (write vs read scope, optional project limit), then the exact command from main-spec:
   `claude mcp add --transport http pabrika http://localhost:8080/mcp --header "Authorization: Bearer pb_your_token_here"` (use your public `https://` URL behind a proxy; `claude mcp remove pabrika` to undo). A generic project-scoped JSON snippet for other Streamable HTTP clients: `{"mcpServers":{"pabrika":{"type":"http","url":"http://localhost:8080/mcp","headers":{"Authorization":"Bearer pb_your_token_here"}}}}`. A list of the 14 tools (including `update_comment`, with "agents edit only their own comments"). Example prompts ("list my Pabrika projects", "create a ticket in WEB titled ..., move WEB-3 to In progress"). Token safety note (shown once, never commit it, revoke in Settings; read tokens see only the 5 read tools). **Prompt injection warning:** ticket and comment text is untrusted and can contain instructions aimed at the agent, so for agents that read tickets written by others, use a read-only token or a project-limited token. MCP transport notes: `/mcp` ignores cookies and needs a bearer token; browser-origin MCP clients are rejected (if an `Origin` header is present it must equal `BASE_URL`; Claude Code and other non-browser clients send none); `GET /mcp` returning 405 is expected (stateless mode). REST scripting note: `curl` with `Authorization: Bearer` works for `/api` without an `Origin` header; cookie-bearing requests (and unauthenticated unsafe requests such as login) without an `Origin` header get 403 `origin_mismatch` by design.
6. **Running behind a proxy (TLS)**: Task 5 content, including the verification commands.
7. **Backups**: Task 6 content, including restore and upgrade notes.
8. **Security notes**: what is hashed (passwords argon2id, session and API tokens SHA-256), roles, session-only actions (including password change), `TRUST_PROXY`, CSP and header set, no CORS, the no-`WriteTimeout` residual risk, how to report issues: GitHub private vulnerability reporting (a `SECURITY.md`-style line with a placeholder link, `https://github.com/RJGJ/Pabrika/security/advisories/new`, to confirm once the repo is public), plus the prompt-injection note from item 5 and the proxy `X-Forwarded-For` overwrite rule.
9. **Development**: prerequisites (Go 1.23+, Bun 1.x; Windows users: Git Bash or WSL for the shell examples), run commands (including the Vite dev setup: `BASE_URL=http://localhost:5173 COOKIE_SECURE=false go run ./cmd/pabrika serve` plus `cd web && bun run dev`), test commands (`CGO_ENABLED=0 go test ./...`; race run optional with cgo), regenerate sqlc, migrations, `make` shortcuts and `pabrika version`; project layout tree.
10. **Limits and non-goals**: single instance only (never two containers on one volume), no email, no attachments, last-write-wins on descriptions, SQLite needs a local filesystem, sizing (512 MB RAM).
11. **Troubleshooting** (short, symptom to cause): 403 `origin_mismatch` on login means `BASE_URL` differs from the URL in the browser; login "works" but you are bounced back means the cookie was not stored (`COOKIE_SECURE=true` over plain HTTP); live updates only arrive in bursts means the proxy buffers the event stream; 429 for everyone means `TRUST_PROXY` is off behind a proxy; "database is locked" or readonly errors mean a network or host-shared filesystem, or wrong ownership of `/data`; a blank page and a 503 "UI not built" means the binary was built without `bun run build`.
12. **License**: one line, "MIT, see [LICENSE](LICENSE)."

Every command in the README must be copy-paste runnable (with the obvious placeholders called out); the smoke test (section 5) executes them as written. A short check script `make readme-check` is not required; the dry run in smoke step 11 is the check.

### Task 8: Update `CLAUDE.md`

Create `CLAUDE.md` if it does not exist; otherwise replace any placeholder commands with the real, verified ones. Required sections:

- **Layout**: short tree and the rule "service layer has no HTTP or MCP knowledge".
- **Build**: `cd web && bun install --frozen-lockfile && bun run build`; `CGO_ENABLED=0 go build -o pabrika ./cmd/pabrika`; `docker build --build-arg VERSION=dev -t pabrika .`.
- **Test**: `CGO_ENABLED=0 go test ./...`; `go vet ./...`; optional `CGO_ENABLED=1 go test -race ./...` (race detector needs CGO and a C compiler); web tests and typecheck (`bun run test`, `bun run typecheck`, using the actual script names from `web/package.json`).
- **Run**: `BASE_URL=http://localhost:5173 COOKIE_SECURE=false go run ./cmd/pabrika serve` with `cd web && bun run dev` for the UI (5173, proxy to 8080), or `COOKIE_SECURE=false go run ./cmd/pabrika serve` alone to use the embedded bundle on 8080; CLI examples (stdin password).
- **Codegen/migrations**: `sqlc generate` (or `make generate`), goose usage as actually wired (embedded, run at `serve` start; how to add a migration).
- **Conventions**: ULIDs, UTC timestamps, 404 for non-members, error shape, no CGO, MCP and REST share input structs and validation, ticket refs, project paths accept key or ULID, events are published by the service after commit (never from handlers or MCP tools), comment body cap 20,000.
- **Gotchas**: `web/dist` must be built before the embedded UI works (a missing bundle gives a 503 page; `.gitkeep` is recreated by `make web`); single writer connection; SSE and `/mcp` must not be buffered or compressed; no `WriteTimeout`; Vite dev needs `BASE_URL=http://localhost:5173`; `go test -race` needs CGO; the final image has no shell.
- Remove stale or aspirational statements. Run each command listed once to prove it works, and record that in the PR.

### Task 9: Release helper (small)

- **Extend** the Phase 1 `Makefile` (do not replace it; keep its `build`, `test`, `vet`, `generate` targets working) with: `web` (`cd web && bun install --frozen-lockfile && bun run build`, then ensure `web/dist/.gitkeep` exists), `build` (depends on `web`, then the no-CGO Go build with the version ldflag), `docker` (`docker build --build-arg VERSION=$(VERSION) -t pabrika .`), `run`, `test` (Go and web tests), `audit` (`go vet ./...`, the govulncheck and `bun audit` commands, and the grep checks from Task 4, each printing a clear pass/fail line), and optionally `test-race` (`CGO_ENABLED=1 go test -race ./...`). `VERSION ?= $(shell git describe --tags --always --dirty)`. Thin wrappers over the commands above; CLAUDE.md and README reference them as shortcuts, but spell out the underlying commands too (Windows users may not have make).
- Version string: the `pabrika version` subcommand exists since Phase 1; this phase only ensures the string is injected with `-ldflags "-X main.version=$(VERSION)"` (the Go variable name as defined in Phase 1), defaults to `dev` when unset, and is logged at startup. The Dockerfile build passes the same ldflag via the `VERSION` build arg (default `dev`).
- Tag `v0.1.0` after exit criteria pass. Tagging itself is the user's decision; the agent only proposes it.

### Task 10: License

Add a `LICENSE` file at the repo root with the MIT license text (copyright holder per the user's git identity, current year) and the one-line README reference (Task 7, item 12). No headers in source files.

## 3. Files touched (summary)

| File | Change |
|---|---|
| `web/embed.go` | new: `go:embed all:dist` |
| `web/dist/.gitkeep`, `.gitignore` | new / edited |
| `internal/httpapi/spa.go` (+ test) | new: static, SPA fallback, cache headers |
| `internal/httpapi/` router and middleware | SPA via `SetFallback`; headers, `/mcp` Origin rule, caps, timeouts audited against Phase 2 (patch only on mismatch, with the Phase 2 test updated) |
| `cmd/pabrika/main.go` | graceful shutdown verified and patched only if missing (Phase 2 owns it: 8 s, `srv.Close` fallback, WAL checkpoint; Phase 3 the hub); `web.FS()` wiring via `SetFallback`; startup log and config warnings; version injection |
| `Dockerfile`, `.dockerignore` | new / tightened |
| `README.md`, `CLAUDE.md` | new / updated |
| `Makefile` | extended, not created (Phases 1 and 2 provide the base): `web`, `docker`, `run`, `audit` |
| `LICENSE` | new (MIT) |

No schema, migration, endpoint or tool changes (the account endpoints, `auth/config` and `update_comment` come from phases 2 and 4; this phase only verifies and documents them).

## 4. Tests (automated)

- SPA handler tests (Task 1).
- Security-header table test (including HSTS on/off by `BASE_URL` scheme and the SSE stream); cookie `Secure` over plain HTTP; Origin check (including default-port normalization and the `/mcp` rule); log redaction (including `/mcp` and a forced panic); body and field caps (including `/mcp` oversize, 415 and comment 20,000); `TRUST_PROXY` on/off rate-limit bucket; signup 404 when `ALLOW_SIGNUP=false`; password change revokes other sessions; session/token hash-at-rest scan; SSE survives longer than `ReadTimeout`; graceful shutdown with an open stream (Task 3, Task 4). Reuse existing phase 2 to 4 tests where they already cover an item and say so in the PR.
- Web: markdown sanitizer unit test (Phase 5's, extended if needed).
- `CGO_ENABLED=0 go test ./...` and `go vet ./...` pass (race run optional, `CGO_ENABLED=1`); `bun run build` and the web tests pass.
- Optional CI note (no CI is required by scope): a job running the above plus `docker build`.

## 5. Release and smoke-test procedure

Run on a clean checkout (or `git clean -xdf` equivalent). Shell commands are bash (Git Bash or WSL on Windows; PowerShell has `curl.exe` and `Measure-Command` as equivalents for `curl` and `time`). Record results in the PR.

1. **Static checks:** `cd web && bun install --frozen-lockfile && bun run typecheck && bun run test && bun run build && cd ..`, then `go vet ./...` and `CGO_ENABLED=0 go test ./...` (web first, so Go tooling also runs with `node_modules` present and a real bundle in `web/dist`), then `make audit` (or its underlying commands).
2. **Build image:** `docker build --no-cache --build-arg VERSION=$(git describe --tags --always) -t pabrika .` succeeds from scratch. Image size recorded (expect under about 40 MB).
3. **Run with volume:**
   ```bash
   docker run -d --name pabrika -p 8080:8080 -v pabrika-data:/data \
     -e BASE_URL=http://localhost:8080 -e COOKIE_SECURE=false pabrika
   ```
   - Health: poll `docker inspect --format '{{.State.Health.Status}}' pabrika` until it prints `healthy` (allow up to 60 s: the first check runs after the 30 s interval).
   - `curl -i http://localhost:8080/healthz` is 200; `curl -i http://localhost:8080/` returns the SPA with the Task 4 headers and `Cache-Control: no-cache`; `curl -i http://localhost:8080/p/WEB/t/1` also returns the SPA; `curl -i http://localhost:8080/api/v1/nope` returns the JSON 404; `curl -i http://localhost:8080/assets/missing.js` is a plain 404.
   - `curl -i http://localhost:8080/api/v1/auth/config` returns `{"signup_enabled":true}` and nothing else.
   - `docker exec pabrika /pabrika version` prints the version passed in step 2, and the first log line (`docker logs pabrika`) shows it.
   - `docker exec pabrika /pabrika healthcheck; echo $?` prints 0. `docker exec pabrika sh` and `docker run --rm --entrypoint sh pabrika` fail (no shell). `docker inspect --format '{{.Config.User}} {{.Config.Volumes}}' pabrika` shows `nonroot:nonroot` and `/data`. Confirm no Bun or Node and a minimal filesystem: `docker export $(docker create pabrika) | tar -t | grep -E '(^|/)(bun|node|sh)$'` prints nothing (clean up with `docker rm` the created container).
3a. **Signup disabled:** `docker rm -f pabrika`, rerun the step 3 command with `-e ALLOW_SIGNUP=false` added (same volume); `curl -i -X POST -H 'Content-Type: application/json' -H 'Origin: http://localhost:8080' -d '{}' http://localhost:8080/api/v1/auth/signup` returns 404 `not_found`, `curl -s http://localhost:8080/api/v1/auth/config` returns `{"signup_enabled":false}`, and in the browser `/signup` redirects to `/login` with no "Create account" link. Then recreate the container without `ALLOW_SIGNUP=false`.
4. **Create a user via CLI:** `printf '%s\n' '<10+ chars>' | docker exec -i pabrika /pabrika user create --email you@example.com --name You --password-stdin` (flags exactly as implemented in phase 2; without `--password-stdin` use `docker exec -it` and answer the prompt). A second run with the same email exits non-zero. Log in via the browser at `http://localhost:8080`. Also try `printf '%s\n' '<new pw>' | docker exec -i pabrika /pabrika user reset-password --password-stdin scratch@example.com` once on a scratch user (old session dies, API tokens survive). Separately, on a brand-new volume with no `serve` running yet, `printf '%s\n' '<pw>' | docker run --rm -i -v pabrika-fresh:/data --entrypoint /pabrika pabrika user create --email a@example.com --name A --password-stdin` succeeds (the CLI runs migrations); remove `pabrika-fresh` afterwards.
5. **Use the app:** create project `WEB`, create tickets, drag one to Done, add a comment, reload (state persists), open a second browser window and confirm a live update appears, with the devtools console open on the first window and **zero CSP violations** across login, board, ticket panel, project settings and account settings. In account settings, change the display name, then change the password with a second browser logged in: the second session is signed out, the current one stays signed in, and a wrong current password is rejected.
6. **Connect an agent over MCP:** create a write token in Settings (export it as `TOKEN` for the checks below); run the README's `claude mcp add --transport http pabrika http://localhost:8080/mcp --header "Authorization: Bearer $TOKEN"`; in Claude Code ask it to list projects, create a ticket in `WEB`, move it to In progress, comment, and then edit that comment with `update_comment`. Confirm the ticket moves live in the browser and the comment shows the token name with a bot badge; confirm the activity actor is the token: `curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/tickets/WEB-1/activity` (there is no sqlite3 in the image). Also confirm a read-scope token sees only the read tools (create a second client entry with it) and the write token lists all 14 tools. Clean up with `claude mcp remove pabrika`.
7. **Persistence:** `docker restart pabrika`, then `docker rm -f pabrika` and `docker run` again with the same volume (step 3 command); user, project, tickets and tokens are all still present, and the token still works (`curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/auth/me`).
8. **Read-only root and shutdown:** `docker rm -f pabrika` and run once with `--read-only --tmpfs /tmp` added to the step 3 command; the app works (log in, create a ticket). With a browser tab holding an event stream open, `time docker stop pabrika` finishes in under 10 s and `docker inspect --format '{{.State.ExitCode}}' pabrika` prints `0` (not 137, which would mean the graceful path failed and Docker killed it). Start it again afterwards (`docker start pabrika`).
9. **Proxy check:** put Caddy (or nginx) in front using the README snippet. Local variant: `docker rm -f pabrika` and run it again with `-p 127.0.0.1:8080:8080 -e BASE_URL=https://pabrika.localhost -e TRUST_PROXY=true` (default `COOKIE_SECURE=true`); use the README Caddyfile with the site address `pabrika.localhost` and an extra `tls internal` line inside the site block, run by a Caddy on the host (`*.localhost` resolves to loopback; use `host.docker.internal:8080` instead of `127.0.0.1:8080` if Caddy runs in Docker), accept the local CA warning in the browser or use `curl -k`. Sign in over HTTPS in the browser; logs (`docker logs pabrika`) show the real client IP, not the proxy's; confirm the cookie is `Secure` (devtools Application tab, or `curl -skc jar.txt` and inspect the jar), login and mutating requests pass the Origin check (no 403), the `/mcp` `tools/list` curl from Task 5 works through the proxy, and the Task 5 stream verification shows keepalives about every 25 s and live updates without delay.
10. **Backup check:** with the container running, run the helper-container `.backup` command from the README against the volume, then run the README's check (`PRAGMA integrity_check`, row counts, and `.dump | grep -c <session cookie value>` printing `0`). Then do one full restore into a scratch volume (`docker volume create pabrika-restore`, restore command with that volume, start a second container on port 8081 with `BASE_URL=http://localhost:8081`) and log in with the existing user.
11. **README dry run:** a fresh reader (or a subagent given only the README) completes steps 3 to 6 using nothing else. Every missing or wrong command is fixed in the README and the dry run repeated.
12. Clean up (`docker rm -f pabrika; docker volume rm pabrika-data pabrika-restore pabrika-fresh`; stop Caddy). Update the status table in `phases.md` for phase 6 only when all exit criteria hold (that edit is part of finishing the phase, not of this spec).

## 6. Exit criteria

- [ ] `docker build` from a clean checkout works; `docker run` with a named volume yields a working app; data survives `docker restart` and container recreation (smoke steps 3 and 7).
- [ ] Container contract verified: one process, port 8080, non-root, `/data` writable on a fresh volume, healthcheck reports healthy, no shell, works with a read-only root, `docker stop` returns in under 10 s with exit code 0 and an open event stream.
- [ ] UI is served from the embedded bundle with SPA fallback and correct cache headers; API and MCP paths never return HTML; the SPA handler tests pass.
- [ ] Every item in the Task 4 audit checklist is ticked or has a recorded, justified exception; the new tests pass.
- [ ] README lets a fresh reader set up the app, create a user, connect an agent over MCP (smoke step 6), configure a proxy, and take a backup, using README alone.
- [ ] `CLAUDE.md` lists real build, test and run commands, each executed once successfully.
- [ ] `LICENSE` (MIT) exists, README links it; `Makefile` and `pabrika version` work.
- [ ] `go vet`, `go test ./...` (no CGO), and the web build and tests pass.
- [ ] No scope creep: no compose file, no multi-instance support, no new endpoints, no new product features.

## 7. Out of scope

Docker Compose files, multi-instance deployment or shared event bus, Kubernetes or Helm manifests, CI/CD pipelines and registry publishing (may be suggested, not built), email, new UI features, stdio MCP mode, built-in TLS or ACME, metrics and tracing.

## Decisions applied

1. `/healthz` is built in Phase 2; this phase only verifies it.
2. Embed code lives in `web/embed.go` (`go:embed` cannot reach a parent directory).
3. Dev with Vite needs `BASE_URL=http://localhost:5173`; documented in README and CLAUDE.md; no `WEB_DIR` option.
4. CSP allows `style-src 'unsafe-inline'`; scripts stay strict.
5. HSTS is set by the proxy; the app sets it only when `BASE_URL` is HTTPS (no other condition).
6. `X-Frame-Options`, `Permissions-Policy`, server timeouts and 415 on non-JSON are in scope.
7. Markdown uses `markdown-it` (`html: false`) plus DOMPurify; images are dropped in v1.
8. `TRUST_PROXY` (default `false`) is documented in the README config table, proxy notes and audit checklist.
9. Comment body cap is 20,000 characters everywhere, including MCP; oversize body is 400 `body_too_large`.
10. Go module path is `github.com/RJGJ/Pabrika`.
11. `Makefile` and the `version` subcommand are in scope.
12. License is MIT (Task 10: `LICENSE` file and README line).
13. Graceful shutdown (8 s deadline, then `srv.Close`, WAL checkpoint, so `docker stop` exits 0) is built in Phase 2 with the hub shutdown in Phase 3; this phase only verifies it and patches anything missing.
14. `golang:1.23` and `oven/bun:1` images are kept; no Kubernetes manifests (README notes in one line that Podman and Kubernetes ignore `HEALTHCHECK`).
15. 14 MCP tools (adds `update_comment`); new account endpoints and `ALLOW_SIGNUP=false` returning 404 are reflected in README, smoke test and audit.
16. Provisional: display name is 1 to 100 characters (audit caps table and tests).
17. Provisional: `Referrer-Policy: same-origin` and CSP `base-uri 'none'` (Phase 2 owns the middleware, this phase audits it).
18. Provisional: `/mcp` ignores cookies, needs a bearer token, and a present `Origin` must match `BASE_URL` (absent allowed); browser-origin MCP clients are rejected.
19. Provisional: README security contact is GitHub private vulnerability reporting (placeholder link line).
20. Provisional: Phase 2's `Server.SetFallback` (method-less `/`) carries the SPA; it never returns HTML for `/api/`, `/mcp`, `/healthz` or `/.well-known/`.
21. Allowed statuses include 405, 415, 429, 500 and 503 `unavailable`.
22. Phase 1 and 2 own `/healthz`, `version` and the Makefile base; Phase 6 extends the Makefile.
23. CLI docs use `--password-stdin` with flags before the positional email; CLI commands run migrations.

## Open questions

None. Everything previously open (`/mcp` Origin policy, security contact, Referrer-Policy) is resolved by the Provisional decisions in [phases.md](phases.md), listed as items 16 to 20 above.
