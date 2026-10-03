# Release smoke test

Source: `specs/phase-6-ship.md` section 5. Steps marked **[Docker]** need Docker, which was not available on the machine where the first run happened. Those steps are **NOT RUN**. The same checks were run against the real binary without Docker; see "Results of the first run (no Docker)" at the end for what that did and did not prove.

## Windows and Git Bash notes

- Shell examples are bash. Use Git Bash or WSL. In PowerShell use `curl.exe` (not the `curl` alias) and `Measure-Command { docker stop pabrika }` instead of `time`.
- Git Bash rewrites arguments that look like Unix paths (`/data`, `/pabrika`). If a `docker run`/`docker exec` command fails with a path error, prefix it with `MSYS_NO_PATHCONV=1` (for example `MSYS_NO_PATHCONV=1 docker exec pabrika /pabrika version`).
- `"$PWD"` in `-v` mounts may need `"$(pwd -W)"` in Git Bash, or run the command from PowerShell with `${PWD}`.
- `printf '%s\n' 'pw' | docker exec -i ...` works in Git Bash. In PowerShell, pipe a plain string (`'pw' | docker exec -i ...`), mind that PowerShell 5.1 may add a BOM or CRLF when piping to native commands; prefer Git Bash for the stdin steps.
- `make` may be missing on Windows; every step lists the underlying commands.
- Use a named Docker volume, not a host folder, on Docker Desktop.

| Step | Status |
|---|---|
| 1 Static checks | pass (no Docker needed) |
| 2 Build image [Docker] | NOT RUN |
| 3 Run with volume [Docker] | NOT RUN; equivalent routes and headers verified on the native binary |
| 3a Signup disabled [Docker] | NOT RUN in a container; verified on the native binary |
| 4 Create a user via CLI [Docker] | NOT RUN in a container; verified on the native binary |
| 5 Use the app (browser) | partial: login, board, ticket panel with markdown, settings pages, zero CSP violations; drag, second window and password change not done here |
| 6 Connect an agent over MCP | partial: raw JSON-RPC over curl (14 tools, create_ticket); `claude mcp add` itself NOT RUN |
| 7 Persistence [Docker] | NOT RUN in Docker; restart on the same database file verified natively |
| 8 Read-only root and shutdown [Docker] | NOT RUN (`docker stop` and exit 137 untested); graceful shutdown with an open stream verified natively |
| 9 Proxy check [Docker] | NOT RUN |
| 10 Backup check [Docker] | NOT RUN |
| 11 README dry run | NOT RUN |
| 12 Clean up [Docker] | NOT RUN (nothing to clean) |

## 1. Static checks (no Docker)

```bash
cd web && bun install --frozen-lockfile && bun run typecheck && bun run test && bun run build && cd ..
go vet ./...
CGO_ENABLED=0 go test ./...
make audit        # or its underlying commands, see docs/security-audit.md
```

Web first, so Go tooling also runs with `node_modules` present and a real bundle in `web/dist`.

## 2. Build image [Docker]

```bash
docker build --no-cache --build-arg VERSION=$(git describe --tags --always) -t pabrika .
docker image ls pabrika
```

Succeeds from scratch. Record the image size (expect under about 40 MB). Record base image digests: `docker image inspect --format '{{index .RepoDigests 0}}' <image>`.

## 3. Run with volume [Docker]

```bash
docker run -d --name pabrika -p 8080:8080 -v pabrika-data:/data \
  -e BASE_URL=http://localhost:8080 -e COOKIE_SECURE=false pabrika
```

- Health: poll `docker inspect --format '{{.State.Health.Status}}' pabrika` until it prints `healthy` (allow up to 60 s).
- `curl -i http://localhost:8080/healthz` is 200.
- `curl -i http://localhost:8080/` returns the SPA with the audit headers and `Cache-Control: no-cache`.
- `curl -i http://localhost:8080/p/WEB/t/1` also returns the SPA.
- `curl -i http://localhost:8080/api/v1/nope` returns the JSON 404.
- `curl -i http://localhost:8080/assets/missing.js` is a plain 404.
- `curl -i http://localhost:8080/api/v1/auth/config` returns `{"signup_enabled":true}` and nothing else.
- `docker exec pabrika /pabrika version` prints the version from step 2; the first line of `docker logs pabrika` shows it.
- `docker exec pabrika /pabrika healthcheck; echo $?` prints `0`.
- `docker exec pabrika sh` and `docker run --rm --entrypoint sh pabrika` fail (no shell).
- `docker inspect --format '{{.Config.User}} {{.Config.Volumes}}' pabrika` shows `nonroot:nonroot` and `/data`.
- No Bun, Node or shell in the image: `docker export $(docker create pabrika) | tar -t | grep -E '(^|/)(bun|node|sh)$'` prints nothing. Remove the created container afterwards (`docker rm <id>`).

## 3a. Signup disabled [Docker]

```bash
docker rm -f pabrika
docker run -d --name pabrika -p 8080:8080 -v pabrika-data:/data \
  -e BASE_URL=http://localhost:8080 -e COOKIE_SECURE=false -e ALLOW_SIGNUP=false pabrika
curl -i -X POST -H 'Content-Type: application/json' -H 'Origin: http://localhost:8080' -d '{}' http://localhost:8080/api/v1/auth/signup
curl -s http://localhost:8080/api/v1/auth/config
```

Expect 404 `not_found` and `{"signup_enabled":false}`. In the browser `/signup` redirects to `/login` and there is no "Create account" link. Then recreate the container without `ALLOW_SIGNUP=false`.

## 4. Create a user via CLI [Docker]

```bash
printf '%s\n' '<10+ chars>' | docker exec -i pabrika /pabrika user create --email you@example.com --name You --password-stdin
```

- A second run with the same email exits non-zero.
- Log in in the browser at `http://localhost:8080`.
- Reset a scratch user's password once (old session dies, API tokens survive; flags before the positional email):
  `printf '%s\n' '<new pw>' | docker exec -i pabrika /pabrika user reset-password --password-stdin scratch@example.com`
- Fresh volume with no `serve` running yet (the CLI runs migrations itself):
  `printf '%s\n' '<pw>' | docker run --rm -i -v pabrika-fresh:/data --entrypoint /pabrika pabrika user create --email a@example.com --name A --password-stdin`
  Then `docker volume rm pabrika-fresh`.

## 5. Use the app (browser)

Create project `WEB`, create tickets, drag one to Done, add a comment, reload (state persists). Open a second browser window and confirm a live update appears. Keep devtools open on the first window: **zero CSP violations** across login, board, ticket panel, project settings and account settings. In account settings change the display name, then change the password with a second browser logged in: the second session is signed out, the current one stays, and a wrong current password is rejected.

## 6. Connect an agent over MCP

Create a write token in Settings and export it as `TOKEN`.

```bash
claude mcp add --transport http pabrika http://localhost:8080/mcp --header "Authorization: Bearer $TOKEN"
```

In Claude Code: list projects, create a ticket in `WEB`, move it to In progress, comment, then edit that comment with `update_comment`. Confirm the ticket moves live in the browser and the comment shows the token name with a bot badge. Confirm the activity actor is the token (there is no sqlite3 in the image):

```bash
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/tickets/WEB-1/activity
```

A read-scope token (second client entry) sees only the 5 read tools; the write token lists all 14. Clean up with `claude mcp remove pabrika`.

## 7. Persistence [Docker]

```bash
docker restart pabrika
docker rm -f pabrika
docker run -d --name pabrika -p 8080:8080 -v pabrika-data:/data \
  -e BASE_URL=http://localhost:8080 -e COOKIE_SECURE=false pabrika
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/auth/me
```

User, project, tickets and tokens are still present and the token still works.

## 8. Read-only root and shutdown [Docker]

```bash
docker rm -f pabrika
docker run -d --name pabrika --read-only --tmpfs /tmp -p 8080:8080 -v pabrika-data:/data \
  -e BASE_URL=http://localhost:8080 -e COOKIE_SECURE=false pabrika
```

Log in and create a ticket. With a browser tab holding an event stream open:

```bash
time docker stop pabrika
docker inspect --format '{{.State.ExitCode}}' pabrika
```

`docker stop` finishes in under 10 s and the exit code is `0` (137 means the graceful path failed). Then `docker start pabrika`.

## 9. Proxy check [Docker]

```bash
docker rm -f pabrika
docker run -d --name pabrika -p 127.0.0.1:8080:8080 -v pabrika-data:/data \
  -e BASE_URL=https://pabrika.localhost -e TRUST_PROXY=true pabrika
```

Run Caddy on the host with the Caddyfile from `docs/proxy.md` (site address `pabrika.localhost`, plus `tls internal` inside the site block; use `host.docker.internal:8080` if Caddy runs in Docker). Accept the local CA warning or use `curl -k`. Check:

- Sign in over HTTPS in the browser; `docker logs pabrika` shows the real client IP, not the proxy's.
- The cookie is `Secure` (devtools Application tab, or `curl -skc jar.txt` and inspect the jar).
- Login and mutating requests pass the Origin check (no 403).
- The `/mcp` `tools/list` curl from `docs/proxy.md` works through the proxy.
- The stream verification from `docs/proxy.md` shows keepalives about every 25 s and live updates without delay.

## 10. Backup check [Docker]

With the container running, run the helper-container backup from `docs/backup.md`, then check it:

```bash
sqlite3 pabrika-YYYY-MM-DD.db "PRAGMA integrity_check; SELECT count(*) FROM tickets; SELECT count(*) FROM users;"
sqlite3 pabrika-YYYY-MM-DD.db .dump | grep -c "<session cookie value>"   # expect 0
```

Then one full restore into a scratch volume: `docker volume create pabrika-restore`, run the restore command from `docs/backup.md` with that volume, start a second container on port 8081 with `BASE_URL=http://localhost:8081`, and log in with the existing user.

## 11. README dry run

A fresh reader (or a subagent given only `README.md`) completes steps 3 to 6 using nothing else. Fix every missing or wrong command in the README and repeat.

## 12. Clean up [Docker]

```bash
docker rm -f pabrika
docker volume rm pabrika-data pabrika-restore pabrika-fresh
```

Stop Caddy. Update the phase 6 status in `specs/phases.md` only when all exit criteria hold.

## Results of the first run (no Docker)

Machine: Windows 11, Git Bash, Go 1.27.1 (module says `go 1.25.0`), Bun 1.4.2, no Docker, no `make`. The binary was built with `go build -trimpath -ldflags "-s -w -X main.version=smoke-1"` after `bun run build` (15 MB with the UI embedded). It ran on a temporary database in a throwaway directory and a random free port, with `ALLOW_SIGNUP=false COOKIE_SECURE=false BASE_URL=http://localhost:PORT`. Everything was removed afterwards.

Verified for real (all passed):

- `pabrika version` printed the injected `smoke-1`. `user create --password-stdin` worked on a database that did not exist yet (migrations ran); a duplicate email exited 1.
- Startup log: `database ready`, then one `pabrika starting` line with version, listen address, base URL, the three flags and the DB path (no secrets), then `listening`.
- `/healthz` 200 `{"status":"ok"}`. `/` and `/p/WEB/t/1` return the SPA (200, `Cache-Control: no-cache`, strong ETag, `If-None-Match` gives 304). `/assets/index-*.js` is `text/javascript` with `public, max-age=31536000, immutable`. `/api/v1/nope` and `/.well-known/x` give the JSON 404, `/assets/missing.js` the plain `404 page not found`, `POST /` 405. All five security headers are on `/`.
- `ALLOW_SIGNUP=false`: signup 404, `/auth/config` is exactly `{"signup_enabled":false}`. Non-JSON login body 415. No CORS headers. Wrong password 401; right password 200 with an `HttpOnly` host-only `pb_session` cookie.
- REST with the cookie: created project `WEB` and tickets, read them back.
- Live updates: a `curl -N` stream on `/api/v1/projects/WEB/events` received `: connected` and `event: ticket.created`.
- `/mcp` with a write token: no token 401, cookie only 401, foreign Origin 403, `initialize` works, `tools/list` returned 14 tools, `create_ticket` created `WEB-3` (visible over REST), `GET /mcp` 405.
- Graceful shutdown with the SSE stream still open: the runner sent CTRL_BREAK (Go maps it to `os.Interrupt`; Windows has no SIGTERM). The process logged `shutting down`, `event stream closed ... reason=shutdown`, `stopped`, exited **0** within about 15 ms, and the curl stream ended. The port was closed afterwards.
- Restart on the same database file: login, project, tickets and the API token (used over `/mcp`) were all still there; second shutdown also exited 0.
- Browser (in-app browser) against the native server: login, board (Live), ticket panel with rendered markdown and a comment, project settings, account settings; the console showed no CSP "Refused to ..." message.
- `sh scripts/audit.sh` (all checks), `go vet`, `go test ./...`, `bun run typecheck`, `bun run test`, `bun run build`, `bun run check:dist`.

NOT RUN (needs Docker or something not installed here):

- Steps 2, 3, 7, 8, 9, 10, 12 as written: `docker build`, image size and digests, the no-shell and non-root checks, `HEALTHCHECK`, the volume-ownership behaviour of the distroless image, `docker stop` timing and exit code (`docker stop` sends SIGTERM and waits 10 s; the in-process deadline is 8 s and was only verified through the Windows equivalent above and the Go tests `TestShutdownEndsOpenEventStreamCleanly`, `TestShutdownDeadlineFallsBackToClose`, `TestServeEndToEnd`), read-only root, Caddy or nginx, `sqlite3 .backup` and restore, README dry run by a second reader, `claude mcp add` with the real Claude Code, `trivy`/`docker scout`.
- `make` targets: `make` is not installed here. The Makefile only wraps the commands above and `sh scripts/audit.sh`; they were run by hand.

Findings from this run:

- `govulncheck` reported 3 reachable vulnerabilities in the MCP SDK v1.3.1. Moving to v1.4.1 fixes them but raises the `go` line to 1.25.0, so the Dockerfile builder image is now `golang:1.25` (the spec allows bumping the tag when a dependency forces it). v1.4 added its own Host/Origin cross-origin check in front of the handler; `mcpserver` now trusts BASE_URL's origin in it and passes the canonical Origin spelling, and our Origin rule (403 `origin_mismatch`) is unchanged.
- Two web specs (`tests/router/guards.spec.ts`, `tests/stores/auth.spec.ts`) exceeded the default 5 s timeout while the machine was loaded by parallel agents; they pass alone and on rerun.
