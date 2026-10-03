# Pabrika

Pabrika is a small, self-hosted kanban board. Tickets live in projects that several people can share, and AI agents can create, read, update and delete tickets through a built-in MCP server. One Go binary serves the web app, the REST API and the MCP endpoint, and all data lives in one SQLite file.

<!-- Screenshot placeholder (optional): docs/screenshot.png -->

Status: the non-Docker paths (build, run, CLI, REST, MCP, shutdown, restart) were exercised against the real binary. The Docker image, proxy and backup steps were written from the specs and have **not been run yet**; `docs/smoke-test.md` lists exactly what was and was not verified.

## Features

- Projects with four fixed columns (Backlog, To do, In progress, Done), drag and drop, labels, assignees, due dates, priorities and comments.
- Sharing per project with three roles: owner, editor, viewer. Owners add people who already have an account, by email.
- Live updates: every change, from the UI, REST or an agent, appears on open boards without a refresh (Server-Sent Events).
- MCP server at `/mcp` with 14 tools, read or write API tokens, optional single-project tokens.
- Email and password login (argon2id), session cookies, CSRF defences, rate limiting.
- One container, one process, one volume. No Bun or Node at runtime.

## Quick start (Docker)

Build and run (the image is built from this directory):

```bash
docker build --build-arg VERSION=dev -t pabrika .
docker run -d --name pabrika --restart unless-stopped -p 8080:8080 -v pabrika-data:/data \
  -e BASE_URL=http://localhost:8080 -e COOKIE_SECURE=false pabrika
```

Open <http://localhost:8080> and sign up; the first account is yours.

Notes:

- `COOKIE_SECURE=false` is needed for plain HTTP: the session cookie is `Secure` by default, browsers may drop it on `http://`, and the app never infers the setting from the connection. In production behind HTTPS use the defaults (see "Running behind a proxy").
- All state is in the `/data` volume. A named volume is writable out of the box. A bind mount (`-v /srv/pabrika:/data`) needs `sudo chown 65532:65532 /srv/pabrika` first. On Docker Desktop for Windows or Mac use a named volume.
- Run one container per volume; the live-update hub is in memory.
- The image has no shell. Run CLI commands with `docker exec` or `docker run --entrypoint /pabrika`.
- Podman and Kubernetes ignore the image `HEALTHCHECK`; point their probes at `GET /healthz`.
- Give the container at least 512 MB of RAM (password hashing uses 64 MiB per hash).

## Quick start (without Docker)

Needs Go 1.25+ (the MCP SDK requires it) and Bun 1.x.

```bash
cd web && bun install --frozen-lockfile && bun run build && cd ..
CGO_ENABLED=0 go build -o pabrika ./cmd/pabrika
COOKIE_SECURE=false ./pabrika serve     # on Windows build with -o pabrika.exe and run ./pabrika.exe serve
```

This serves the UI, API and MCP endpoint on <http://localhost:8080>, with data in `./data/pabrika.db`. If `web/dist` was never built, the server still starts but non-API paths show a 503 "UI not built" page.

## Configuration

Environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | HTTP port (1 to 65535). |
| `DB_PATH` | `./data/pabrika.db` | SQLite file. The image sets `/data/pabrika.db`. |
| `BASE_URL` | `http://localhost:8080` | Public URL of the app: scheme, host, optional port, no path. Used for the Origin check and links in MCP results. A trailing slash is trimmed. |
| `ALLOW_SIGNUP` | `true` | Allow public signup. When `false`, `POST /api/v1/auth/signup` returns 404, the `/signup` page redirects to `/login`, and users are created with the CLI. |
| `COOKIE_SECURE` | `true` | `Secure` flag on the session cookie. Set `false` only for plain-HTTP local use. |
| `TRUST_PROXY` | `false` | Set `true` behind a reverse proxy: the rate limiter and logs then use the first `X-Forwarded-For` hop. Leave `false` if the container is reachable without a proxy. |

In the image `DB_PATH=/data/pabrika.db` and `PORT=8080` are baked in. To change the host port use `-p 9090:8080` and update `BASE_URL` to match. The health check reads the container's own `PORT`, so it keeps working if you set `PORT`.

At startup `serve` logs one line with the version, address, `BASE_URL`, `ALLOW_SIGNUP`, `TRUST_PROXY`, `COOKIE_SECURE` and DB path (no secrets), and warns about likely misconfiguration (for example `BASE_URL=http://...` with `COOKIE_SECURE=true`).

## Create the first user from the CLI

The password is never an argument. Pipe it in with `--password-stdin`:

```bash
printf '%s\n' 'a-long-password' | docker exec -i pabrika /pabrika user create \
  --email you@example.com --name "You" --password-stdin
```

Or be prompted (no echo) with `docker exec -it pabrika /pabrika user create --email you@example.com --name "You"`. Passwords need at least 10 characters. Check the flags with `docker exec pabrika /pabrika user create -h`.

- The CLI commands apply migrations themselves, so they also work on a fresh volume before `serve` has run. On a fresh install with signup disabled, still start the container first, then run the command.
- One-off container (the `--entrypoint` is required; without it the image would run `serve user create`):

  ```bash
  printf '%s\n' 'a-long-password' | docker run --rm -i -v pabrika-data:/data --entrypoint /pabrika pabrika \
    user create --email you@example.com --name "You" --password-stdin
  ```

- With `ALLOW_SIGNUP=false` the CLI still works; it is the way to add users.

Reset a password (this deletes the user's sessions but not their API tokens). Flags go **before** the email, because Go's flag parsing stops at the first positional argument:

```bash
printf '%s\n' 'new-long-password' | docker exec -i pabrika /pabrika user reset-password --password-stdin you@example.com
```

Signed-in users can change their display name and password in account settings. Changing the password signs out their other sessions. Email change is not supported.

## Connect an agent over MCP

1. In the web app open Settings and create an API token. Pick a scope (`write`, or `read` for read-only tools), optionally limit it to one project, and copy it: it is shown once.
2. Add the server to Claude Code:

   ```bash
   claude mcp add --transport http pabrika http://localhost:8080/mcp \
     --header "Authorization: Bearer pb_your_token_here"
   ```

   Behind a proxy use your public `https://` URL. Undo with `claude mcp remove pabrika`.

   Other clients that support Streamable HTTP with custom headers can use a project-scoped config like:

   ```json
   {"mcpServers":{"pabrika":{"type":"http","url":"http://localhost:8080/mcp","headers":{"Authorization":"Bearer pb_your_token_here"}}}}
   ```

Tools (14). Read tokens see only the first five; write tokens see all:

| Tool | Purpose |
|---|---|
| `list_projects` | List projects (`include_archived?`) |
| `list_members` | List a project's members |
| `list_labels` | List a project's labels |
| `list_tickets` | List tickets with filters and pagination |
| `get_ticket` | One ticket with labels, assignee and recent comments |
| `create_project` | Create a project (write) |
| `update_project` | Rename, edit description, archive (write, owner) |
| `create_label` | Create a label (write) |
| `create_ticket` | Create a ticket (write) |
| `update_ticket` | Edit a ticket (write) |
| `move_ticket` | Change status and position (write) |
| `add_comment` | Add a comment (write) |
| `update_comment` | Edit a comment; agents edit only comments they wrote (write) |
| `delete_ticket` | Soft delete; needs `confirm: true` (write) |

`project` accepts a key (`WEB`) or id, `ticket` a reference (`WEB-12`) or id, `assignee` an email, `labels` label names. Project deletion and member management are not available over MCP.

Example prompts: "List my Pabrika projects." "Create a ticket in WEB titled 'Fix login redirect' with priority high." "Move WEB-3 to In progress and add a comment saying I started."

Safety:

- Tokens are shown once. Do not commit them; revoke them in Settings.
- **Ticket and comment text is untrusted.** It can contain instructions aimed at your agent (prompt injection). For agents that read tickets written by other people, use a read-only token or a project-limited token.
- `/mcp` ignores cookies and needs a bearer token. If an `Origin` header is present it must equal `BASE_URL`, so browser-origin MCP clients are rejected; Claude Code and other non-browser clients send none.
- `GET /mcp` returning 405 is expected (stateless mode).
- REST scripting: `curl` with `Authorization: Bearer` works against `/api/v1` without an `Origin` header. Requests that carry a cookie, and unauthenticated unsafe requests such as login, get 403 `origin_mismatch` without a matching `Origin` header, by design.

## Running behind a proxy (TLS)

Pabrika speaks plain HTTP. Put a TLS-terminating proxy in front, because the session cookie and bearer tokens must not travel over plain HTTP. Full notes and checks: [docs/proxy.md](docs/proxy.md).

- Do not expose 8080 publicly. Publish it on loopback (`-p 127.0.0.1:8080:8080`; Docker's published ports bypass host firewalls such as ufw) or put the proxy on the same Docker network and publish nothing.
- Set `BASE_URL` to the exact public HTTPS URL (no path, no trailing slash). A mismatch shows up as 403 `origin_mismatch` on every login.
- Keep `COOKIE_SECURE=true`.
- Set `TRUST_PROXY=true` so rate limiting and logs see real client IPs; otherwise everyone shares the proxy's IP and the 5-per-minute login limit applies to all of them together. The app uses the first `X-Forwarded-For` hop, so the proxy must overwrite that header, not append to it (both snippets do).
- Set HSTS at the proxy (`Strict-Transport-Security: max-age=31536000`). The snippets avoid sending it twice.
- Compression belongs to the proxy and must exclude the event stream. Do not buffer `/api/v1/projects/*/events` or `/mcp`; read timeouts there must exceed the 25 s keepalive. Use HTTP/2: each open board holds one stream and HTTP/1.1 allows about 6 connections per origin.

```bash
docker run -d --name pabrika --restart unless-stopped --memory 512m \
  -p 127.0.0.1:8080:8080 -v pabrika-data:/data \
  -e BASE_URL=https://pabrika.example.com -e TRUST_PROXY=true pabrika
```

Caddy:

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

nginx (see [docs/proxy.md](docs/proxy.md) for the full server block with the `/mcp` and catch-all locations):

```nginx
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
```

Verify the stream through your proxy (replace host, email, password and the project key `WEB`):

```bash
curl -sk -c jar.txt -H 'Content-Type: application/json' -H 'Origin: https://pabrika.example.com' \
  -d '{"email":"you@example.com","password":"..."}' https://pabrika.example.com/api/v1/auth/login
curl -skN -b jar.txt https://pabrika.example.com/api/v1/projects/WEB/events \
  | while IFS= read -r line; do echo "$(date +%T) $line"; done
```

Expect `retry: 3000` and `: connected` at once, a `: keepalive` about every 25 s, and an `event: ticket.created` frame as soon as a ticket is created elsewhere. Bursts or delays mean something is buffering. Check `/mcp` the same way:

```bash
curl -sk -X POST https://pabrika.example.com/mcp -H 'Authorization: Bearer pb_...' \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

## Backups

Back up the single file `pabrika.db` in `/data`. It runs in WAL mode, so do not `cp` it while the app runs. Details, restore, Litestream and cron: [docs/backup.md](docs/backup.md).

Online backup from a throwaway container on the same volume (the image has no sqlite3; run it while Pabrika is running):

```bash
docker run --rm -v pabrika-data:/data -v "$PWD":/backup alpine \
  sh -c 'apk add --no-cache sqlite >/dev/null && sqlite3 /data/pabrika.db ".backup /backup/pabrika-$(date +%F).db"'
```

Check it (`ok` and plausible counts), and test a restore at least once:

```bash
sqlite3 pabrika-YYYY-MM-DD.db "PRAGMA integrity_check; SELECT count(*) FROM tickets; SELECT count(*) FROM users;"
```

Restore:

```bash
docker stop pabrika && docker rm pabrika
docker run --rm -v pabrika-data:/data -v "$PWD":/backup alpine \
  sh -c 'rm -f /data/pabrika.db-wal /data/pabrika.db-shm && cp /backup/pabrika-YYYY-MM-DD.db /data/pabrika.db && chown 65532:65532 /data/pabrika.db'
docker run -d --name pabrika ...   # same run command as before
```

Backups contain password and token hashes; encrypt them if they leave the machine.

Upgrade: back up, rebuild or pull the image, then `docker stop pabrika`, `docker rm pabrika`, and `docker run` again with the same volume. Migrations apply on start; rollback means restoring the backup. Restoring a newer database into an older image is not supported.

## Security notes

- Passwords are hashed with argon2id (64 MiB, 3 passes, 2 threads). Session cookies and API tokens are stored only as SHA-256 hashes.
- Roles: viewer reads, editor changes tickets, labels and comments, owner manages members, roles and the project. Non-members get 404 for projects and their contents.
- Session-only actions (tokens, members, project delete, profile and password change, live event stream) reject API tokens. Tokens act as their owner and never get more than their scope and the owner's role.
- Session cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` unless `COOKIE_SECURE=false`. Every unsafe request is checked against `BASE_URL` via the `Origin` header, and bodies must be JSON.
- Headers on every response: a strict Content-Security-Policy (scripts `'self'` only; inline styles allowed), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY`, `Permissions-Policy`, and HSTS when `BASE_URL` is HTTPS. There is no CORS.
- Request bodies are capped at 1 MiB. There is no server `WriteTimeout` (it would cut event and MCP streams), so a client that stops reading a normal response holds a goroutine until the connection drops. This is accepted for v1 behind a proxy with its own timeouts.
- With `TRUST_PROXY=true` the proxy must overwrite `X-Forwarded-For`, and the port must not be publicly reachable.
- Ticket text is untrusted input for agents (see "Connect an agent over MCP").
- Report vulnerabilities through GitHub private vulnerability reporting: <https://github.com/RJGJ/Pabrika/security/advisories/new> (link to be confirmed once the repository is public).

The release audit checklist is in [docs/security-audit.md](docs/security-audit.md).

## Development

Prerequisites: Go 1.25+, Bun 1.x. On Windows use Git Bash or WSL for the shell examples; `make` is optional.

```bash
# Run the API (BASE_URL must match the Vite origin) and the UI with hot reload
BASE_URL=http://localhost:5173 COOKIE_SECURE=false go run ./cmd/pabrika serve
cd web && bun run dev          # http://localhost:5173, proxies /api (not /mcp) to :8080

# Or run the API alone and use the embedded bundle on :8080
COOKIE_SECURE=false go run ./cmd/pabrika serve
```

Tests and checks:

```bash
CGO_ENABLED=0 go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...     # optional, needs a C compiler
cd web && bun run typecheck && bun run test
```

Other commands: `go run ./cmd/pabrika version`, `make generate` (sqlc), `make web`, `make build`, `make docker`, `make audit`. Migrations are goose SQL files in `migrations/`, embedded and applied at `serve` start. If `web/dist/.gitkeep` shows as deleted after a build, run `git checkout web/dist/.gitkeep`. See [CLAUDE.md](CLAUDE.md) for layout and conventions.

```text
cmd/pabrika/      serve, user create, user reset-password, healthcheck, version
internal/         config, store, service, auth, httpapi, mcpserver
migrations/       embedded goose SQL
web/              Vue app (Bun + Vite); web/embed.go embeds web/dist
docs/             proxy, backup, security audit, smoke test
specs/            main spec and per-phase specs
```

## Limits and non-goals

- Single instance only: never run two containers on one volume.
- No email (no verification, resets or invitations), no attachments, no notifications.
- Descriptions are last-write-wins; there is no collaborative editing.
- SQLite needs a local filesystem. Plan for 512 MB of RAM.
- Four fixed columns; no organizations above projects.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| 403 `origin_mismatch` on login | `BASE_URL` differs from the URL in the browser (scheme, host or port). |
| Login seems to work but you are bounced back to the login page | The cookie was not stored: `COOKIE_SECURE=true` over plain HTTP. Set `COOKIE_SECURE=false` locally, or use HTTPS. |
| Live updates arrive in bursts or late | The proxy buffers or compresses the event stream. See "Running behind a proxy". |
| 429 for everyone | `TRUST_PROXY` is off behind a proxy, so all clients share one IP. |
| "database is locked" or read-only errors | Network or host-shared filesystem under `/data`, or wrong ownership (`chown 65532:65532` for bind mounts). |
| Blank page or 503 "UI not built" | The binary was built without `bun run build`. |
| `docker run pabrika user create ...` starts the server | Missing `--entrypoint /pabrika`. |

## License

MIT, see [LICENSE](LICENSE).
