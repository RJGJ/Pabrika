# Phase 6 plan: Ship

Basis: `phases.md` (Decisions), `main-spec.md` (Layout, config and deployment; container contract and Dockerfile), `phase-6-ship.md` (scope source), and the Phase 2/3/4 sections that Phase 6 relies on. Phases 1 to 5 were only grepped for the Phase 6 seams: `SetFallback`, `WriteError`, `healthcheck`, the module layout and the shutdown order.

**Repo facts that change the plan**
- The repo is spec-only today: git root `D:\rj\Pabrika`, Go module root `D:\rj\Pabrika\pabrika` (Phase 1 §2). There is no code yet, so WP0 cannot run until Phases 1 to 5 land.
- Root `README.md` and `CLAUDE.md` are placeholders. The root `.gitignore` has a BOM and does not yet ignore `data/`, `*.db*` or `web/dist/*`.
- The Dockerfile and `.dockerignore` must sit where the build context contains `go.mod`, which is `pabrika/`. Their `COPY` paths (`go.mod`, `web/`) assume that. Deliverables go in `pabrika/`, not the git root. See Open question 1.
- Ten sub-tasks plus the 12-step smoke test in `phase-6-ship.md` §5 are the whole job. Phase 6 adds no endpoints, features or schema.

## Sequencing

```
WP0 verify ──┬─ WP1 SPA handler + embed ──┬─ WP3 Dockerfile ── WP6 smoke test ── WP8 close-out
             ├─ WP2 audit tests/patches ──┘                         ▲
             ├─ WP4 docs (README, CLAUDE.md) ───────────────────────┤ (README dry run needs WP3)
             └─ WP5 Makefile / LICENSE / startup log ───────────────┘
                              WP7 proxy + backup docs (parallel with WP2)
```

- Parallel after WP0: WP1, WP2 (audit tests), WP4 (docs drafting), WP5 and LICENSE.
- Strictly serial:
  - WP3 needs WP1 (`web/embed.go` and a working `serve`).
  - WP6 needs WP1, WP2 and WP3.
  - The README dry run (smoke step 11) needs WP3, WP4 and WP7.

## WP0: Verify assumptions (Task 0, §1 table)

Run from `pabrika/` and paste the results into the PR description. Fix each failure as a small patch in the owning phase's code.

1. `grep -n "^go \|^toolchain" go.mod`. If it is above 1.23, bump the Dockerfile image tag.
2. `ls web/bun.lock`. If the repo has `bun.lockb` instead, change the `COPY`.
3. `grep -rn WriteTimeout cmd internal`. It must find nothing set.
4. `grep -rn "NewServer\|ListenAndServe" cmd`. The address must be `":"+port`, not loopback.
5. Read the top-level handler chain. Recover, log, security headers and the body cap must wrap the whole mux, including `/healthz`, `/mcp`, `/.well-known/` and the fallback.
6. Check that every wrapper `ResponseWriter` has `Unwrap()` and `Flush()`.
7. Run `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...` as the baseline gate.
8. Check that `Server.SetFallback`, `Mount`, `MountRaw` and exported `httpapi.WriteError` exist.
9. Check that `version` is a package-level `var` in `cmd/pabrika`. Use its real name in the ldflags (`-X main.version=...` by default).

Verify: all checks green, or each patch listed.

## WP1: Embed and SPA handler (Task 1, test-first)

Do the tests before the code (Phase 6 §4).

1. **`pabrika/web/dist/.gitkeep` and `.gitignore`**: add `web/dist/*`, `!web/dist/.gitkeep`, `data/` and `*.db*` if Phase 1 has not already. Check by running `git status` after a build; `.gitkeep` must stay tracked.
2. **`pabrika/web/embed.go`**: `package web`, `//go:embed all:dist`, `func FS() (fs.FS, error)` returning `fs.Sub(distFS, "dist")`. Verify: `CGO_ENABLED=0 go build ./...` passes with only `.gitkeep` present.
3. **`pabrika/internal/httpapi/spa_test.go`** (`fstest.MapFS`), written first. Cases come from Task 1 "Tests", as a table:
   - `/` returns index with `no-cache`.
   - `/assets/app-abc.js` returns the file with immutable cache headers and a JS content type.
   - `/p/WEB/t/12` returns index with 200.
   - `/assets/missing.js`, `/foo.js` and `/favicon.ico` return a plain 404.
   - API-like paths (`/api/v1/nope`, `/api/other`, `/mcp/unknown`, `/healthz/x`, `/.well-known/*`) return the JSON 404. `POST /api/v1/nope` also returns the JSON 404.
   - `/mcpx` returns index.
   - `POST /` returns 405 with `Allow: GET, HEAD`, and `HEAD /` returns no body.
   - Traversal: `/../etc/passwd`, `/%2e%2e/secret` and `//evil`.
   - `/index.html` returns 200 with no redirect, and `If-None-Match` returns 304.
   - Content types for `.js`, `.css`, `.woff2`, `.svg` and `.ico`.
   - Empty FS (only `.gitkeep`) returns a 503 "UI not built" page with `no-store`, while `/api/...` stays a JSON 404.
4. **`pabrika/internal/httpapi/spa.go`**: `NewSPAHandler(fs.FS) http.Handler`.
   - Method gate: GET and HEAD only; other methods get 405 JSON `method_not_allowed` with `Allow` set.
   - API-prefix check comes first, using exact or `/`-suffix matching for `/mcp` and `/healthz`, and prefix matching for `/api/` and `/.well-known/`. It returns `WriteError(404, "not_found")`.
   - Clean the path with `path.Clean` after stripping the leading `/`. Do not use `http.FileServer`.
   - If the path names a file, open it and serve with `http.ServeContent`.
   - If the path is missing and is under `/assets/` or its last segment has a dot, return a plain 404.
   - Otherwise serve index.
   - Compute index.html's strong ETag from a content hash at construction. Set `Cache-Control: no-cache` on index and fallbacks, `public, max-age=31536000, immutable` on `/assets/*`, and `max-age=3600` on other root files.
   - Detect the missing bundle by checking for `index.html`. Return a 503 page with `no-store`.
   - Add `init()` MIME registrations for `.woff2`, `.woff`, `.ico`, `.webmanifest`, `.map`, `.js`, `.mjs` and `.css`, because distroless has no `mime.types`.
   - Verify: the unit tests pass under `CGO_ENABLED=0`.
5. **Integration test** (real mux, routes plus SPA, `httptest`): the precedence checks from Task 1 "Tests". `/healthz`, `/api/v1/auth/config`, `/mcp` without a token, `/.well-known/x` and `/p/WEB` each hit the right handler, and the fallback is never reached for `/api/`.
6. **Wire-up in `cmd/pabrika/main.go`**: `web.FS()`, then `srv.SetFallback(httpapi.NewSPAHandler(fsys))`. Log one warning at startup if `index.html` is absent.
7. **Header and cache assertions**: confirm that `Cache-Control: no-store` for `/api/v1`, `/mcp` and `/healthz` is already in Phase 2's code and add it for `/mcp` if missing. Confirm the SSE `no-store, no-transform` is not overwritten. Run `grep -rniE "gzip|compress" internal cmd` and expect an empty result (Task 1 §5).

## WP2: Security audit (Task 4)

Keep a PR table with one pass/fail line per checklist item, naming the evidence. Most items are existing Phase 2 to 4 tests, so name them rather than rewrite them. Add only the tests below that are missing.

Add new Go tests, probably in `internal/httpapi/audit_test.go`:
1. **Header table test.** Cover `/`, `/p/WEB`, `/api/v1/auth/me` (401), `/api/v1/auth/config`, `/healthz`, `/.well-known/x`, `/assets/x.js`, `/assets/missing.js`, `/mcp` (401), `POST /`, and forced 429, 503 and 500 responses. Assert the full header set against constants shared with Phase 2's middleware.
   - Run separate cases for HSTS off with an `http://` `BASE_URL` and exactly one HSTS with `https://`.
   - Add the SSE stream on a real `httptest.Server`, asserting `no-store, no-transform` and `X-Accel-Buffering: no`.
   - Add `Origin: https://evil.example` returning zero `Access-Control-*` headers, and `OPTIONS` returning the JSON 405/404.
2. **Hash-at-rest scan.** After login and token creation, iterate `sqlite_master` and every `SELECT *`, and substring-match the cookie value and the token secret. Check that `GET /tokens` never contains the secret.
3. **Log redaction.** Use a capturing `slog` handler. Send a bearer request, a cookie request and a login with a password, including `/mcp`, then force a panic. Assert that no secret appears in the output.
4. **Body caps.** Send 1 MiB + 1 to `/api/v1/tickets`, `/auth/login` and `/mcp`, expecting 400 `body_too_large`.
5. **Field limits.** Table test at limit and limit+1, with a multibyte case. Cover display name at 0, 1, 100 and 101. Confirm the MCP 201-character title returns the "too long" tool error.
6. **415.** Send a non-JSON content type with a body.
7. **SSE longer than `ReadTimeout`.** Use a shortened `ReadTimeout` and assert a keepalive still arrives.
8. **Graceful shutdown.** Cancel the signal context with an open SSE stream and assert it returns in under 8 s with the stream at EOF. Reuse Phase 2 and 3 tests if they already cover this, and say so.
9. **Gaps only.** Check for existing tests on these before adding any:
   - cookie `Secure` over plain HTTP with default config
   - Origin default-port normalization and the `Origin: null` case
   - the `/mcp` rule (cookie-only 401, mismatched Origin 403, absent Origin allowed)
   - `TRUST_PROXY` on/off bucket behavior
   - signup 404
   - password change revoking other sessions
   - the Phase 4 schema drift test

**Command audit** (these also become `make audit`; each prints a pass/fail line):

```bash
go vet ./...
CGO_ENABLED=0 go test ./...
grep -rn WriteTimeout cmd internal
grep -rniE "gzip|compress" internal cmd
grep -rniE "fmt\.Sprintf\(.*(select|insert|update|delete) " internal --include=*.go | grep -v _test
grep -rnE "(==|!=) *[a-zA-Z_.]*([Hh]ash|[Tt]oken|[Ss]ecret)" internal/auth internal/httpapi internal/mcpserver --include=*.go | grep -v _test   # list and justify hits
grep -rniE "slog\.|log\.(Print|Fatal)" internal cmd --include=*.go | grep -iE "authorization|cookie|password|secret|token\)" | grep -v _test
grep -rn "argon2.IDKey" internal      # only password.go
grep -rn "v-html" web/src             # exactly MarkdownView.vue
grep -rn "innerHTML" web/src          # empty
grep -rl "eval(\|new Function" web/dist/assets   # empty, or record why a library hit is inert
grep -c "<script" web/dist/index.html            # only src= scripts
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
cd web && bun audit && bun run test
```

- **Gate semantics for `make audit`.** A "no hit expected" grep needs `! grep` to pass.
- **Manual CSP check.** Serve the real bundle from the Go binary. Open `/login`, `/`, a board, a ticket panel, project settings and account settings with the console open, and expect zero "Refused to ..." messages. If the bundle forces a CSP change, change Phase 2's middleware and its test, record why, and do not add a second middleware.

## WP3: Dockerfile and `.dockerignore` (Task 2), in `pabrika/`

1. Write the Dockerfile from the main-spec skeleton with these tightenings:
   - `ARG VERSION=dev`
   - `go mod download` before `COPY . .`
   - `COPY --from=web /web/dist ./web/dist` after `COPY . .`
   - the `-trimpath -ldflags "-s -w -X main.version=${VERSION}"` build
   - `LABEL`, `USER nonroot:nonroot` and the `HEALTHCHECK` with interval, timeout, start-period and retries
   - exec-form `ENTRYPOINT`
2. Write `.dockerignore`. These entries are required: `.git`, `data/`, `*.db*`, `web/node_modules`, `web/dist`, `.claude`, `specs`, `plans`, binaries, and IDE files. Use `**/*.md` rather than `*.md`, which only matches the context root.
   - Never ignore `go.mod`, `go.sum`, `web/bun.lock`, `web/embed.go` or `migrations/`. Ignoring `web/dist` is fine, because the Bun stage supplies it in the build.
3. Verify with `docker build --build-arg VERSION=test -t pabrika .` followed by `docker run --rm --entrypoint /pabrika pabrika version`. The second command must print `test`.
4. Image checks (Task 3):
   - `docker inspect --format '{{.Config.User}} {{.Config.Volumes}}' pabrika`
   - `docker run --rm --entrypoint sh pabrika` must fail.
   - The tar scan for `bun|node|sh` must print nothing.
   - Record the size (expect under about 40 MB) and the image digests.
5. If the build fails because `go.mod` needs a newer Go, bump the tag, not the toolchain download. Phase 1 pins `go 1.23`, but a dependency such as the MCP SDK may force a newer one.

## WP4: Documentation, parallel with WP1 to WP3

Draft against the spec, then correct commands against the real CLI flags once Phase 2 is verified.

1. **README.md** (Task 7), 12 sections as listed. Use `docker exec pabrika /pabrika user create -h` to get the exact flags, and put `--password-stdin` with flags before the positional email. Keep these in:
   - bind-mount `chown 65532:65532`
   - the Podman and Kubernetes healthcheck note
   - the prompt-injection warning
   - the `/mcp` transport notes
   - the troubleshooting symptom list
2. **CLAUDE.md** (Task 8): replace the placeholder with the layout, build, test, run, codegen, conventions and gotchas sections. Run each command once and record it.
3. **LICENSE** (Task 10): MIT, with the copyright holder from the user's git identity (check `git config user.name`) and the current year (2026). Add the one-line README reference.
4. **Proxy and backup docs** (Tasks 5 and 6). Paste the Caddy and nginx snippets and the verification curls. Copy the backup and restore commands exactly, including the alpine helper container. Write them with Docker Desktop in mind: no `/var/lib/docker/volumes` path, and a note that PowerShell users need `curl.exe` and `${PWD}` for `$PWD`.

## WP5: Makefile and startup log

1. **Extend the Makefile; do not replace it.** Add `web`, `build`, `docker`, `run`, `audit` and `test` (Go plus web). Optionally keep `test-race`, which needs `CGO_ENABLED=1`. Add `VERSION ?= $(shell git describe --tags --always --dirty)`.
   - `web` ends with `touch web/dist/.gitkeep`.
   - `build` depends on `web` and uses the ldflag.
   - `audit` collects the WP2 commands.
   - Check with `make -n` and each target once.
   - Windows: `make` may not be installed, so the docs spell out the underlying commands.
2. **Startup log and warnings** in `serve`: one line carrying version, listen address, `BASE_URL`, `ALLOW_SIGNUP`, `TRUST_PROXY`, `COOKIE_SECURE` and the DB path. Warn, never fatal, on these combinations:
   - an `http://` `BASE_URL` with `COOKIE_SECURE=true` on a non-localhost host
   - an `https://` `BASE_URL` with `COOKIE_SECURE=false`
   - `TRUST_PROXY=true`

   Add a small unit test for the warning predicate.

## WP6: Smoke test (§5), run on a clean checkout after WP1 to WP5

Use Git Bash for the bash blocks, or PowerShell with `curl.exe`. Record all results in the PR.

1. **Static checks:**
   ```bash
   cd web && bun install --frozen-lockfile && bun run typecheck && bun run test && bun run build && cd ..
   go vet ./... && CGO_ENABLED=0 go test ./... && make audit
   ```
2. **Build image:** `docker build --no-cache --build-arg VERSION=$(git describe --tags --always) -t pabrika .`
3. **Run with volume:**
   ```bash
   docker run -d --name pabrika -p 8080:8080 -v pabrika-data:/data \
     -e BASE_URL=http://localhost:8080 -e COOKIE_SECURE=false pabrika
   docker inspect --format '{{.State.Health.Status}}' pabrika   # poll until healthy, up to 60 s
   ```
   Then run the curl checks from smoke step 3:
   - `/healthz`
   - `/`
   - `/p/WEB/t/1`
   - `/api/v1/nope`
   - `/assets/missing.js`
   - `/api/v1/auth/config`

   Also run `docker exec pabrika /pabrika version`, `docker exec pabrika /pabrika healthcheck; echo $?` and the no-shell checks.
4. **3a.** Run with `ALLOW_SIGNUP=false` and check the signup 404 and the auth config.
5. **CLI user creation.** Run `printf '%s\n' 'pw' | docker exec -i pabrika /pabrika user create ... --password-stdin`, a duplicate-email run, `reset-password`, and a fresh-volume run with `--entrypoint /pabrika`.
6. **UI session.** Run the step 5 flow: project, tickets, drag, comment, reload, a second window, and zero CSP violations. Also check the account display-name and change-password behavior.
7. **MCP.** Run `claude mcp add ...`, then create a ticket, move it, comment and `update_comment`. Check the activity actor with curl and the 14 tools against a read token (5 read tools).
8. **Persistence.** `docker restart`, then `rm -f` and re-run on the same volume. The token must still work.
9. **Read-only root and shutdown:**
   ```bash
   docker run ... --read-only --tmpfs /tmp ...
   time docker stop pabrika
   docker inspect --format '{{.State.ExitCode}}' pabrika   # must print 0
   ```
   Use an open SSE stream: a browser tab on a board, or `curl -N` in a second terminal.
10. **Proxy.** Run Caddy against the loopback-published container with `tls internal`.
11. **Backup and restore** into a scratch volume on port 8081.
12. **README dry run** by a subagent given only the README. Then clean up and mark Phase 6 `done` in `phases.md`.

**Windows notes**
- Docker Desktop (WSL2 backend) is needed for Docker commands.
- Git Bash rewrites paths that start with `/`: `docker exec ... /pabrika ...` and `-v ...:/data` get mangled. Set `MSYS_NO_PATHCONV=1` or write `//pabrika` and `//data`.
- `docker exec -i` with piped stdin works in Git Bash. `-it` needs `winpty` there, or use PowerShell.
- `$(git describe ...)` and `date` work in bash. PowerShell needs `$(...)` quoting differences and `${PWD}`.
- Use named volumes, not bind mounts, so the WAL gets a real filesystem. Bind mounts on Windows corrupt or lock SQLite.
- Line endings: the repo may have CRLF in the shell scripts (none are shipped). The `.gitignore` BOM should be removed.
- `.localhost` names resolve on Windows 11, but `host.docker.internal` is needed if Caddy runs in Docker.

## Risk register

| Risk | Mitigation |
|---|---|
| Distroless nonroot volume ownership | `COPY --chown=nonroot:nonroot /data` makes a fresh named volume writable. Smoke steps 3 and 8 cover it. A bind mount needs a documented `chown 65532:65532`. A helper container running as root must not create root-owned `-wal` files, so run it only while the app is up. |
| SIGTERM on PID 1 / exit 137 | Use exec-form `ENTRYPOINT`. Go's signal handling covers PID 1, but the 8 s deadline versus Docker's 10 s grace is the real risk. Test with an open SSE stream, assert exit code 0 and time under 10 s, and patch `serve` if the hub, `srv.Close` or checkpoint steps are missing. |
| SSE through a proxy | `X-Accel-Buffering: no`, `no-transform`, a `flush_interval -1` or `proxy_buffering off` proxy config, and keepalive on a 25 s timer against a 1 h read timeout. Smoke step 9 verifies keepalive timing with a timestamping curl loop. Check HTTP/2 for the 6-connection limit. |
| `CGO_ENABLED=0` sqlite | `modernc.org/sqlite` needs no C. Run `go build` and `go test` with `CGO_ENABLED=0` as the gate from WP0. Confirm `-race` is excluded from that gate. |
| Missing or stale bundle in the image | The Dockerfile `COPY --from=web` must come after `COPY . .`. Smoke test checks `/` returns the SPA, not the 503 page. |
| CSP breaks the real bundle | Check zero violations in the browser with the real build. Fix Phase 2's middleware and test if so. |
| `.dockerignore` too aggressive | `docker build` plus `version` run is the check. Use `**/*.md`. Keep `migrations/`, `go.sum` and `web/bun.lock`. |
| Floating `oven/bun:1` and `golang:1.23` tags | Accepted by decision. Record digests per release. |
| Phase 2 to 5 do not match assumptions | WP0 catches this early. Small patches go in the owning code, noted in the PR. Larger gaps should be flagged, not rebuilt. |

## Rough size

About 3 to 5 working days for one engineer or agent, assuming Phases 1 to 5 are complete and matching the specs:
- WP1: 0.5 to 1 day.
- WP2: 1 to 1.5 days, mostly new audit tests and patching drift.
- WP3: 0.5 day.
- WP4 and WP7: 1 day.
- WP5: 0.25 day.
- WP6: 0.5 to 1 day, with proxy and Claude Code MCP steps being the slowest.

Code volume is small: `spa.go` about 150 lines, tests about 400, `embed.go` about 15 and the Dockerfile about 35. The rest is docs and verification. Contingency of about one day is worth keeping for Phase 2 to 4 patches that the audit surfaces.

## Definition of done (maps to §6 exit criteria)

| Exit criterion | Evidence |
|---|---|
| Docker build and run, data survives restart | Smoke steps 2, 3 and 7. |
| Container contract | Smoke steps 3 and 8: non-root, healthy, no shell, read-only works, `docker stop` under 10 s with exit 0 on an open stream. |
| Embedded UI, SPA fallback, cache headers, no HTML on API paths | WP1 unit and integration tests, plus smoke step 3 curls. |
| Audit checklist ticked or justified | WP2 PR table, the new tests passing and `make audit` output. |
| README from scratch works | Smoke steps 6 and 11. |
| `CLAUDE.md` real commands | Each command executed once, recorded in the PR. |
| `LICENSE` and Makefile | Both present, `make` targets run, `pabrika version` prints the injected version. |
| `go vet`, no-CGO `go test`, web build and tests | Smoke step 1. |
| No scope creep | No compose file, no new endpoints or features, no schema changes. |

Propose the `v0.1.0` tag to the user after all criteria pass. Do not create it.

## Open questions

1. **Where do the deliverables live.** The git root is `D:\rj\Pabrika`, but the Go module root is `D:\rj\Pabrika\pabrika` (Phase 1 §2). The spec says "repo root of `pabrika/`" for the README. Existing root `README.md` and `CLAUDE.md` are placeholders. Suggested: put the Dockerfile, `.dockerignore`, Makefile, README, LICENSE and the real `CLAUDE.md` in `pabrika/` and keep the root files as brief pointers, or remove them. The user should decide, and the `.gitignore` paths and the `.dockerignore` `specs` and `plans` entries depend on it.
2. **Name of the version variable and the Phase 2 flag names.** The specs say "use what Phase 1 defined" but the planner could not see it. WP0 resolves this once code exists. It is not a decision for the user.
3. **Copyright holder name for the LICENSE.** The spec says use the user's git identity. Check `git config user.name` at implementation time.

Everything else is resolved by the specs: the Provisional decisions in `phases.md` close the previous open questions.

### Critical files for implementation
- `web/embed.go` (new)
- `internal/httpapi/spa.go` and `spa_test.go` (new)
- `Dockerfile` and `.dockerignore` (new)
- `cmd/pabrika/main.go` (SetFallback wiring, startup log and warnings, shutdown verification)
- `Makefile`, `README.md`, `CLAUDE.md`, `LICENSE` (extend or create)
