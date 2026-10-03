# CLAUDE.md

Guidance for Claude Code when working in `pabrika/` (the Go module root, next to `go.mod`).

Pabrika is a self-hosted kanban board with an MCP server: Go backend, SQLite, Vue frontend, one binary, one container. `README.md` is the user-facing guide; this file is for working on the code.

Status note: commands below follow the specs. Verify each against the actual code (`Makefile`, `web/package.json`) when it exists, and fix this file if they differ.

## Layout

```text
cmd/pabrika/       subcommands: serve, user create, user reset-password, healthcheck, version
internal/
  config/          env parsing (PORT, DB_PATH, BASE_URL, ALLOW_SIGNUP, COOKIE_SECURE, TRUST_PROXY)
  store/           SQLite (modernc.org/sqlite), sqlc output in store/db, queries in store/queries
  service/         business rules; Actor, authz, validation, ordering, events seam
  auth/            argon2id, sessions, API tokens, middleware
  httpapi/         REST handlers, SSE stream, error mapping, SPA handler
  mcpserver/       MCP tools and resources, thin wrappers over service
  testutil/        test services, seed helpers
migrations/        embedded goose SQL
web/               Vue app (Bun, Vite); web/embed.go embeds web/dist
docs/              proxy, backup, security audit, smoke test
specs/ plans/      specs and implementation plans (see below)
```

Rule: the service layer has no knowledge of HTTP or MCP. REST handlers and MCP tools are thin wrappers over it.

## Build

```bash
cd web && bun install --frozen-lockfile && bun run build     # writes web/dist
CGO_ENABLED=0 go build -o pabrika ./cmd/pabrika
docker build --build-arg VERSION=dev -t pabrika .
```

Makefile shortcuts (optional on Windows): `make web`, `make build`, `make docker`, `make run`, `make test`, `make audit`, `make generate`.

## Test

```bash
CGO_ENABLED=0 go test ./...            # the gate; no CGO
go vet ./...
CGO_ENABLED=1 go test -race ./...      # optional; needs CGO and a C compiler
cd web && bun run test && bun run typecheck
```

## Run

```bash
# Dev with hot reload: API on 8080, Vite on 5173 (proxies /api only, not /mcp)
BASE_URL=http://localhost:5173 COOKIE_SECURE=false go run ./cmd/pabrika serve
cd web && bun run dev

# API alone, serving the embedded bundle on 8080
COOKIE_SECURE=false go run ./cmd/pabrika serve

# CLI (password via stdin, never as an argument; flags before positionals)
printf '%s\n' 'a-long-password' | go run ./cmd/pabrika user create --email you@example.com --name You --password-stdin
printf '%s\n' 'new-long-password' | go run ./cmd/pabrika user reset-password --password-stdin you@example.com
go run ./cmd/pabrika version
```

## Codegen and migrations

- `sqlc generate` (or `make generate`): reads `migrations/` and `internal/store/queries/`, writes `internal/store/db/`. The generated code is committed.
- Migrations are goose SQL files in `migrations/` (`00001_init.sql`, ...), embedded via `migrations/embed.go` and applied at `serve` start and by the CLI user commands. To add one: create the next numbered file with `-- +goose Up` and `-- +goose Down`, then run `sqlc generate` if queries changed. Never edit an applied migration.

## Conventions

- IDs are ULIDs (text); timestamps are UTC, formatted only through `store.FormatTime` / `store.ParseTime`.
- Non-members get 404, never 403, for projects and their contents. Errors use `{"error": {"code": "...", "message": "..."}}`.
- No CGO anywhere (pure-Go SQLite driver).
- REST and MCP share input structs, enums, caps and `Validate()` from `service`. Key, email and label-name resolution for MCP lives in `mcpserver`.
- Ticket references look like `WEB-12`; project paths accept the key or the ULID. Ticket JSON uses `ref` and `project_key`.
- Events are published by the service after DB commit, never from handlers or MCP tools.
- Token scope and project limit are enforced in the service layer through `Actor`.
- Limits: comment body 20,000 characters, ticket description 20,000, title 200, project name 100, description 2,000, label name 50, display name 1 to 100.
- Commit messages: small, focused commits.

## Gotchas

- `web/dist` must be built before the embedded UI works; without `index.html` the server answers non-API paths with a 503 "UI not built" page. `web/dist/.gitkeep` is committed; if a build deletes it, `git checkout web/dist/.gitkeep` (or `make web`).
- One SQLite write connection. Never call the store from inside a `WithTx` callback (deadlock); use the `q` you were given.
- SSE (`/api/v1/projects/{id}/events`) and `/mcp` must not be buffered or compressed. The app does no compression; the proxy must exclude these paths.
- No `WriteTimeout` on the HTTP server (it would cut streams).
- Vite dev needs `BASE_URL=http://localhost:5173` or the Origin check returns 403.
- `go test -race` needs CGO; the default test gate does not.
- The final Docker image has no shell. CLI in a container: `docker exec -i pabrika /pabrika ...`, or `docker run --entrypoint /pabrika`.
- Go's `flag` stops at the first positional argument: `reset-password --password-stdin EMAIL`, not `EMAIL --password-stdin`.
- On Windows use Git Bash; paths like `/data` may need `MSYS_NO_PATHCONV=1` for Docker commands.

## Specs and plans

- `specs/main-spec.md` is the source of truth for what is built.
- `specs/phases.md` lists the six phases and holds the Decisions and Provisional decisions sections, which override everything else.
- `specs/phase-N-<slug>.md` is the detailed spec for each phase; `plans/phase-N-<slug>.md` is its implementation plan.
- If a phase spec conflicts with `main-spec.md`, `main-spec.md` wins; flag the conflict instead of silently deviating. Do not widen scope beyond a phase's "Out of scope".
