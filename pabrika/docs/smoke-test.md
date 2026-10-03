# Release smoke test

Source: `specs/phase-6-ship.md` section 5. Run on a clean checkout. Record results in the PR. Steps marked **[Docker]** need Docker, which was not available when this file was written, so all steps are `pending`.

## Windows and Git Bash notes

- Shell examples are bash. Use Git Bash or WSL. In PowerShell use `curl.exe` (not the `curl` alias) and `Measure-Command { docker stop pabrika }` instead of `time`.
- Git Bash rewrites arguments that look like Unix paths (`/data`, `/pabrika`). If a `docker run`/`docker exec` command fails with a path error, prefix it with `MSYS_NO_PATHCONV=1` (for example `MSYS_NO_PATHCONV=1 docker exec pabrika /pabrika version`).
- `"$PWD"` in `-v` mounts may need `"$(pwd -W)"` in Git Bash, or run the command from PowerShell with `${PWD}`.
- `printf '%s\n' 'pw' | docker exec -i ...` works in Git Bash. In PowerShell, pipe a plain string (`'pw' | docker exec -i ...`), mind that PowerShell 5.1 may add a BOM or CRLF when piping to native commands; prefer Git Bash for the stdin steps.
- `make` may be missing on Windows; every step lists the underlying commands.
- Use a named Docker volume, not a host folder, on Docker Desktop.

| Step | Status |
|---|---|
| 1 Static checks | pending |
| 2 Build image [Docker] | pending |
| 3 Run with volume [Docker] | pending |
| 3a Signup disabled [Docker] | pending |
| 4 Create a user via CLI [Docker] | pending |
| 5 Use the app (browser) | pending |
| 6 Connect an agent over MCP | pending |
| 7 Persistence [Docker] | pending |
| 8 Read-only root and shutdown [Docker] | pending |
| 9 Proxy check [Docker] | pending |
| 10 Backup check [Docker] | pending |
| 11 README dry run | pending |
| 12 Clean up [Docker] | pending |

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
