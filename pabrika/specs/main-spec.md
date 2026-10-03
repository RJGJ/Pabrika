# Pabrika: Technical Spec

2026-10-03 · 

## Overview

Pabrika is a small, self-hosted kanban board where tickets are grouped into projects that several people can share, and AI agents can create, read, update and delete those tickets through an MCP server. One Go binary serves the API, the MCP endpoint and the web app, and all data lives in one SQLite file.

**Goals**

- Humans sign in with email and password and work together on a drag-and-drop board.
- Projects can be shared: the owner adds other people with a role (owner, editor or viewer).
- Agents connect over MCP with an API token and can do everything their owner is allowed to do to tickets.
- Every change, from the UI, REST or an agent, shows up on everyone's open board without a refresh.
- Both the web UI and MCP go through the same service code, so rules (validation, ordering, permissions) live in one place.
- Easy to run: a single Docker container running one process, with one data volume to back up.

**Non-goals for v1**

- Organizations or workspaces above projects: sharing is per project.
- Sending email: no verification, no password-reset emails, no invitations. An owner adds people who already have an account, by email address.
- File attachments and notifications.
- Collaborative text editing: if two people edit the same description at once, the last save wins.
- Custom columns: every project uses the same four statuses.

## Tech stack and architecture

Go serves everything from one process: the REST API, a live-update stream, the MCP endpoint and the built Vue app, backed by a single SQLite file.

| Layer | Choice | Why |
|---|---|---|
| Backend | Go 1.22 or newer, standard `net/http` with pattern routing | Few dependencies, one static binary |
| Database | SQLite through `modernc.org/sqlite` (pure Go) | No CGO, one file to back up |
| Queries and migrations | `sqlc` and `goose` with embedded SQL files | Type-safe queries, versioned schema |
| MCP | Official Go MCP SDK, Streamable HTTP | Tool input types can be shared with REST |
| Live updates | Server-Sent Events (`EventSource` in the browser) | One-way push over plain HTTP, no WebSocket library |
| Auth | `argon2id`, session cookies, API tokens | Small and easy to audit, no auth framework |
| Frontend | Bun, Vue 3, Vite, TypeScript, Vue Router, Pinia | Vue SPA; Bun handles installs, scripts and the build |
| UI kit | shadcn-vue, Tailwind CSS, `vue-draggable-plus` | Simple, accessible components and drag and drop |

_[Diagram: architecture · 2 clients, 1 service layer, 1 SQLite file (see the original doc)]_

The browser and agents differ only in how they authenticate; after that both go through the same service layer, which is where validation, ordering and permission checks live. The Go binary also serves the built Vue files, which are not drawn here.

## Data model

Ten tables: users, sessions, API tokens, projects, project members, labels, tickets, ticket labels, comments and an activity log. IDs are ULIDs stored as text (sortable by creation time), and timestamps are UTC ISO-8601 text.

- **Sharing is a membership row.** `project_members` links a user to a project with a role (owner, editor or viewer). The creator becomes the first owner, and a project always keeps at least one owner.
- **Project keys are unique across the install**, so `WEB-12` points to exactly one ticket for everyone.
- **Tickets have a human reference** like `WEB-12`: the project's short key plus a per-project counter. Agents and humans can both use it instead of the ULID.
- **Order inside a column** is a `position` number. To move a ticket between two others, set its position to the midpoint of their positions; renumber the column only when the gap gets too small.
- **Assignee** is one project member or nobody. **Labels** belong to a project, and a ticket can have many.
- **Comments and activity record who acted:** a user, or a specific API token (shown with the token's name and its owner).
- **Deletes are soft** for tickets and comments (`deleted_at`), so a mistaken agent call can be undone by hand.
- **Secrets are never stored in clear:** session and API tokens are saved as SHA-256 hashes.

```sql
CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
  display_name  TEXT NOT NULL,             -- shown on cards, comments, assignee picker
  password_hash TEXT NOT NULL,             -- argon2id, PHC string
  created_at    TEXT NOT NULL
);

CREATE TABLE sessions (
  token_hash  TEXT PRIMARY KEY,            -- sha256 of cookie value
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  TEXT NOT NULL,
  created_at  TEXT NOT NULL
);

CREATE TABLE projects (
  id                 TEXT PRIMARY KEY,
  key                TEXT NOT NULL UNIQUE, -- 2-6 uppercase letters, e.g. WEB
  name               TEXT NOT NULL,
  description        TEXT NOT NULL DEFAULT '',
  next_ticket_number INTEGER NOT NULL DEFAULT 1,
  created_by         TEXT NOT NULL REFERENCES users(id),
  archived_at        TEXT,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);

CREATE TABLE project_members (
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL CHECK (role IN ('owner','editor','viewer')),
  created_at TEXT NOT NULL,
  PRIMARY KEY (project_id, user_id)
);
CREATE INDEX members_by_user ON project_members (user_id);

CREATE TABLE api_tokens (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,              -- e.g. "claude-code laptop"
  token_hash   TEXT NOT NULL UNIQUE,       -- sha256 of full token
  token_prefix TEXT NOT NULL,              -- first 8 chars, for display
  scope        TEXT NOT NULL CHECK (scope IN ('read','write')),
  project_id   TEXT REFERENCES projects(id) ON DELETE CASCADE,  -- NULL = every project the user is in
  last_used_at TEXT,
  revoked_at   TEXT,
  created_at   TEXT NOT NULL
);

CREATE TABLE labels (
  id         TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name       TEXT NOT NULL COLLATE NOCASE,
  color      TEXT NOT NULL DEFAULT 'gray',  -- one of a fixed palette
  UNIQUE (project_id, name)
);

CREATE TABLE tickets (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  number      INTEGER NOT NULL,            -- WEB-12 -> 12
  title       TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',    -- markdown
  status      TEXT NOT NULL DEFAULT 'todo'
              CHECK (status IN ('backlog','todo','in_progress','done')),
  priority    TEXT NOT NULL DEFAULT 'medium'
              CHECK (priority IN ('low','medium','high','urgent')),
  assignee_id TEXT REFERENCES users(id) ON DELETE SET NULL,  -- must be a project member
  position    REAL NOT NULL,               -- order within (project, status)
  due_date    TEXT,                        -- YYYY-MM-DD
  deleted_at  TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  UNIQUE (project_id, number)
);
CREATE INDEX tickets_board ON tickets (project_id, status, position)
  WHERE deleted_at IS NULL;

CREATE TABLE ticket_labels (
  ticket_id TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  label_id  TEXT NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
  PRIMARY KEY (ticket_id, label_id)
);

CREATE TABLE comments (
  id         TEXT PRIMARY KEY,
  ticket_id  TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author_type TEXT NOT NULL CHECK (author_type IN ('user','api_token')),
  author_id  TEXT NOT NULL,
  body       TEXT NOT NULL,                -- markdown
  created_at TEXT NOT NULL,
  edited_at  TEXT,
  deleted_at TEXT
);
CREATE INDEX comments_by_ticket ON comments (ticket_id, created_at);

CREATE TABLE ticket_activity (
  id         TEXT PRIMARY KEY,
  ticket_id  TEXT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('user','api_token')),
  actor_id   TEXT NOT NULL,
  action     TEXT NOT NULL,                -- created, updated, moved, assigned, labeled, deleted
  changes    TEXT NOT NULL DEFAULT '{}',   -- JSON: {field: [old, new]}
  created_at TEXT NOT NULL
);
```

## Auth

Humans use email and password with a session cookie; agents use API tokens in a bearer header. Both resolve to the same "current user" object, so every query is limited to the projects that user is a member of.

**Project roles**

| Role | Can do |
|---|---|
| Viewer | Read the board, tickets, comments and activity |
| Editor | Everything a viewer can, plus create, edit, move and delete tickets, manage labels, assign people, and comment |
| Owner | Everything an editor can, plus add and remove members, change roles, rename, archive or delete the project |

- The person who creates a project is its first owner. A project always keeps at least one owner.
- Owners add people who already have an account, by email address. There are no invitation emails.
- Someone who is not a member gets a 404 for the project, so they cannot tell whether it exists.

**Email and password**

- Signup asks for an email, a display name and a password. Email is lowercased and must be unique. Password must be at least 10 characters.
- Passwords are hashed with argon2id (`golang.org/x/crypto/argon2`, about 64 MiB memory, 3 passes, 2 threads) and stored as a PHC string.
- Login failures return one generic message, whether the email or the password was wrong.
- Login and signup are rate limited in memory: 5 attempts per minute per IP and email.
- Signup can be switched off with `ALLOW_SIGNUP=false`; users are then created with the CLI (`pabrika`` user create`).
- No email is sent in v1, so password reset is a CLI command: `pabrika`` user reset-password <email>`.

**Browser sessions**

- Login creates a random 32-byte token, sets it in a cookie (`HttpOnly`, `Secure` outside local dev, `SameSite=Lax`) and stores only its SHA-256 hash.
- Sessions last 30 days and extend while in use. Logout deletes the row.
- CSRF defence: `SameSite=Lax`, JSON-only bodies, and a check that the `Origin` header matches the server on every POST, PATCH and DELETE.

**API tokens for agents**

- Created in Settings. The format is `p``b_` plus 32 random bytes in hex. The full token is shown once.
- Sent as `Authorization: Bearer ``p``b_...` to `/mcp` and `/api/v1/*`.
- Each token has a scope (`read` or `write`) and can optionally be limited to one project.
- Tokens can be revoked at any time. `last_used_at` is updated so unused tokens are easy to spot.
- Tokens cannot create or manage other tokens, and cannot change account details. A token acts as its owner and never gets more: in each project it has the lower of its scope and the owner's role. Write scope covers ticket, label and comment work plus creating and archiving projects; members, roles and project deletion are session-only.

## REST API

The Vue app talks to a JSON API under `/api/v1`. Ticket paths accept either the ULID or the reference (`WEB-12`).

| Method | Path | Purpose | Needs |
|---|---|---|---|
| POST | `/auth/signup` | Create account and start a session | nothing |
| POST | `/auth/login` | Start a session | nothing |
| POST | `/auth/logout` | End the session | session |
| GET | `/auth/me` | Current user | session or token |
| GET | `/tokens` | List API tokens (never the secret) | session |
| POST | `/tokens` | Create a token; secret returned once | session |
| DELETE | `/tokens/{id}` | Revoke a token | session |
| GET | `/projects` | List projects you are a member of (`?archived=true` to include archived) | member |
| POST | `/projects` | Create a project; you become its owner | any user |
| GET | `/projects/{id}` | One project with your role and ticket counts per status | member |
| PATCH | `/projects/{id}` | Rename, edit description, archive | owner |
| DELETE | `/projects/{id}` | Delete project and everything in it | owner, session only |
| GET | `/projects/{id}/members` | List members and roles | member |
| POST | `/projects/{id}/members` | Add an existing user by email with a role | owner, session only |
| PATCH | `/projects/{id}/members/{userId}` | Change a member's role | owner, session only |
| DELETE | `/projects/{id}/members/{userId}` | Remove a member, or leave the project | owner or that member, session only |
| GET | `/projects/{id}/labels` | List labels | member |
| POST | `/projects/{id}/labels` | Create a label | editor |
| PATCH | `/labels/{id}` | Rename or recolor a label | editor |
| DELETE | `/labels/{id}` | Delete a label; it is removed from tickets | editor |
| GET | `/projects/{id}/tickets` | List tickets: `status`, `priority`, `assignee`, `label`, `q`, `limit`, `cursor` | member |
| POST | `/projects/{id}/tickets` | Create a ticket | editor |
| GET | `/tickets/{id}` | One ticket with labels, assignee and comment count | member |
| PATCH | `/tickets/{id}` | Edit title, description, priority, due date, assignee, labels | editor |
| POST | `/tickets/{id}/move` | Change status and position | editor |
| DELETE | `/tickets/{id}` | Soft delete | editor |
| GET | `/tickets/{id}/activity` | Change history | member |
| GET | `/tickets/{id}/comments` | List comments, oldest first | member |
| POST | `/tickets/{id}/comments` | Add a comment | editor |
| PATCH | `/comments/{id}` | Edit a comment | its author |
| DELETE | `/comments/{id}` | Soft delete a comment | its author or an owner |
| GET | `/projects/{id}/events` | Live update stream (Server-Sent Events) | member, session only |

**Conventions**

- Request and response bodies are JSON. Timestamps are RFC 3339 in UTC.
- Errors look like `{"error": {"code": "not_found", "message": "..."}}` with status 400, 401, 403, 404, 409 or 422.
- In the table, "member" means any role, "editor" means editor or owner. A token also needs enough scope: write for anything that changes data. Asking for a project you are not in returns 404.
- `POST /tickets/{id}/move` takes `status` plus one of `before` or `after` (another ticket id), or `place: "top" | "bottom"`. Omitting placement means the bottom of the column.
- In ticket bodies, `assignee` is a member's user id (or `null`) and `labels` is a list of label ids. The assignee must be a member of the project.
- Lists are cursor-paginated with a default limit of 50 and a maximum of 200.
- Managing members and deleting a project are session-only on purpose: API tokens can archive a project but not destroy it or change who is in it.

**Live updates**

`GET /projects/{id}/events` is a Server-Sent Events stream. The board opens it with the browser's `EventSource`, which sends the session cookie automatically (it cannot send a Bearer header, so the stream is for the web UI only).

- Every change made by anyone, through the UI, REST or MCP, is announced to everyone who has that project open.
- An event says what happened and to what (`ticket.created`, `ticket.updated`, `ticket.moved`, `ticket.deleted`, `comment.added`, `comment.changed`, `label.changed`, `member.changed`, `project.updated`), with the ticket id and who did it. It carries no ticket content: the client fetches the ticket again, so normal permission checks always apply.
- The service layer publishes each event to an in-memory hub after the database commit. Each open stream has a small buffer; a client that falls behind is disconnected and reconnects.
- The server sends a comment line every 25 seconds to keep the connection alive. On every connect or reconnect the UI reloads the whole board, so nothing is missed while it was offline.
- Membership is checked when the stream opens. If a member is removed, their open streams are closed.
- The in-memory hub is enough because there is one process. Running several copies behind a load balancer would need a shared message bus, which is out of scope.
- A reverse proxy must not buffer this response (Caddy streams it by default; nginx needs `proxy_buffering off` for this path).

## MCP server

The server exposes a Streamable HTTP MCP endpoint at `/mcp` in the same Go process, authenticated with an API token. It is stateless: each request stands alone, so there are no MCP sessions to store or expire. Tools call the same service layer as the REST handlers.

**Tools**

| Tool | Arguments | Needs write token |
|---|---|---|
| `list_projects` | `include_archived?` | no |
| `create_project` | `name`, `key`, `description?` | yes |
| `update_project` | `project`, `name?`, `description?`, `archived?` | yes (owner) |
| `list_members` | `project` | no |
| `list_labels` | `project` | no |
| `create_label` | `project`, `name`, `color?` | yes |
| `list_tickets` | `project`, `status?`, `priority?`, `assignee?`, `label?`, `query?`, `limit?`, `cursor?` | no |
| `get_ticket` | `ticket` (returns labels, assignee and comments) | no |
| `create_ticket` | `project`, `title`, `description?`, `status?`, `priority?`, `due_date?`, `assignee?`, `labels?` | yes |
| `update_ticket` | `ticket`, `title?`, `description?`, `priority?`, `due_date?`, `assignee?`, `labels?` | yes |
| `move_ticket` | `ticket`, `status`, `place?` (`top`/`bottom`), `before?`, `after?` | yes |
| `add_comment` | `ticket`, `body` | yes |
| `delete_ticket` | `ticket`, `confirm` (must be `true`) | yes |

`project` accepts a project key (`WEB`) or id. `ticket` accepts a reference (`WEB-12`) or id. Using references means an agent can work from what it reads on the board without juggling ULIDs. In the MCP tools `assignee` is a member's email (or `null` to unassign) and `labels` is a list of label names. An unknown email or label returns an error that lists the valid ones, so the agent can correct itself. Comments written through MCP show the token's name as the author, with a bot badge in the UI.

**Resources (optional, nice to have)**

- `pabrika``://projects`: list of projects.
- `pabrika``://projects/{key}/board`: all open tickets grouped by status, as a compact snapshot an agent can load in one read.

**Behavior**

- Read tokens only see the read tools; write tokens see all of them. A token limited to one project gets an error for any other project.
- Tool results are short JSON: the ticket reference, title, status, priority, position and URL to the board. No internal fields.
- Failures return `isError: true` with a message the model can act on, for example "No project with key WEBB. Available keys: WEB, OPS."
- Project deletion is not available over MCP. Agents can archive a project; ticket deletes are soft and need `confirm: true`.
- Every change made through MCP is written to `ticket_activity` with the token as the actor, so you can see what an agent did.
- Implementation: the official Go MCP SDK, with tool input schemas generated from Go structs so the REST and MCP validation rules stay in sync.

**Connecting an agent**

Create a token in Settings, then for Claude Code:

```bash
claude mcp add --transport http pabrika http://localhost:8080/mcp \
  --header "Authorization: Bearer pb_your_token_here"
```

Any MCP client that supports Streamable HTTP with custom headers works the same way.

## UI

A Vue 3 single-page app (Bun for packages and scripts, Vite, TypeScript, Vue Router, Pinia) styled with shadcn-vue on Tailwind. It is built to static files and embedded into the Go binary. The look stays plain: shadcn defaults, light and dark mode, no custom design system.

**Screens**

| Route | What it shows |
|---|---|
| `/login`, `/signup` | Email and password forms; signup also asks for a display name |
| `/` | Redirects to the last opened project, or an empty state with "Create your first project" |
| `/p/:key` | The board for one project, with the project list in a sidebar |
| `/p/:key/t/:number` | Same board with the ticket panel open (deep-linkable) |
| `/p/:key/settings` | Members and roles, labels, archive and delete project (owners) |
| `/settings` | Account, API tokens, and a copy-paste MCP setup snippet |

**Board behavior**

- Four fixed columns: Backlog, To do, In progress, Done. Each shows a ticket count.
- Drag a card within a column or across columns. The move is applied immediately, sent to `POST /tickets/{id}/move`, and rolled back with a toast if the server rejects it.
- "Add ticket" at the bottom of each column opens an inline title field; Enter creates it.
- Cards show reference, title, priority badge, label chips, the due date (red when overdue) and the assignee's avatar.
- Toolbar: text search plus filters for priority, assignee (me, unassigned, or a member) and label. Filtering is client-side on the loaded tickets.
- Clicking a card opens a side panel with editable title, markdown description (plain textarea plus rendered preview), priority, due date, assignee picker, label picker (with "create label"), comments, activity list and delete.
- Comments written by an agent show the token's name with a bot badge.
- Viewers see the same screens read-only: no drag, no add button, no editable fields.
- Live updates: the board opens the project's event stream. When an event arrives it fetches just the ticket named in it and updates or moves the card; after a reconnect it reloads the whole board. Cards changed by someone else, including an agent, flash briefly. A small "Live" or "Reconnecting" indicator sits in the toolbar.
- If an update arrives for the column you are dragging in, it is applied after you drop. If you lose access to the project, the board shows a notice and returns to the project list.

**shadcn-vue components used**

`Button`, `Input`, `Textarea`, `Card`, `Badge`, `Avatar`, `Dialog`, `Sheet` (ticket panel), `Select`, `Popover` and `Command` (assignee and label pickers), `DropdownMenu`, `Sidebar`, `Sonner` (toasts), `Skeleton`, `Tabs`.

Drag and drop uses `vue-draggable-plus` (SortableJS). The auth state lives in a Pinia store that calls `/auth/me` on load and redirects to `/login` on any 401.

## Layout, config and deployment

One repository, one binary. The Go code is split so the service layer has no knowledge of HTTP or MCP.

```text
pabrika/
  cmd/pabrika/main.go        # serve, user create, user reset-password
  internal/
    config/                 # env parsing
    store/                  # sqlite access (sqlc-generated queries)
    service/                # business rules: projects, members, tickets, comments, ordering, tokens, event hub
    auth/                   # argon2id, sessions, token middleware
    httpapi/                # REST handlers, event stream, error mapping
    mcpserver/              # MCP tools and resources, thin wrappers over service
  migrations/               # embedded SQL migrations (goose)
  web/                      # Vue app (Bun + Vite); build output embedded via go:embed
  Dockerfile
```

**Configuration (environment variables)**

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DB_PATH` | `./data/``pabrika``.db` | SQLite file |
| `BASE_URL` | `http://localhost:8080` | Used for the Origin check and links in MCP results |
| `ALLOW_SIGNUP` | `true` | Allow public signup |
| `COOKIE_SECURE` | `true` | Set `false` for plain-HTTP local dev |

**SQLite settings:** WAL mode, `foreign_keys=ON`, `busy_timeout=5000`, and a single write connection (a pool of one for writes, several for reads) to avoid "database is locked" errors. Use the pure-Go driver `modernc.org/sqlite` so builds need no CGO.

**Deployment:** the whole app ships as one Docker container running one process. A three-stage Dockerfile builds the Vue app with Bun (oven/bun image: bun install --frozen-lockfile, then bun run build), builds the Go binary with the built app embedded, and copies only that binary into a small final image. Bun is used at build time only; the running container has no Bun or Node. Mount a volume for the data directory and put a TLS-terminating proxy (Caddy or similar) in front, because the session cookie and bearer tokens must not travel over plain HTTP. Backups: copy the file with `sqlite3 .backup`, or stream it with Litestream.

**Container contract**

- One container, one process, one exposed port (8080). No docker-compose and no separate database or web container.
- All state is in `/data` (`DB_PATH=/data/``pabrika``.db`), which is a volume. Everything else in the image is read-only.
- Runs as a non-root user. `/data` is created with that user as owner in the image, so a named volume mounts writable.
- The image has no shell or curl, so the health check is a subcommand of the binary: `pabrika`` healthcheck`, which calls `/healthz`.

```dockerfile
FROM oven/bun:1 AS web
WORKDIR /web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ .
RUN bun run build

FROM golang:1.23 AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /pabrika ./cmd/pabrika && mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /pabrika /pabrika
COPY --from=server --chown=nonroot:nonroot /data /data
ENV DB_PATH=/data/pabrika.db PORT=8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK CMD ["/pabrika", "healthcheck"]
ENTRYPOINT ["/pabrika", "serve"]
```

```bash
docker build -t pabrika .
docker run -d --name pabrika -p 8080:8080 -v pabrika-data:/data \
  -e BASE_URL=https://pabrika.example.com pabrika
```

**Development:** run `bun run dev (the Vite dev server on 5173)` with a proxy to the Go server on 8080, so the UI hot-reloads against the real API.

**Reverse proxy:** besides TLS, the proxy must not buffer `/api/v1/projects/*/events`, or live updates arrive late. Caddy streams it by default; for nginx set `proxy_buffering off` on that path.

**Testing**

- Service layer: Go tests against an in-memory SQLite database (ordering, roles and permissions, soft delete).
- HTTP: `httptest` for auth flows, scope and role checks, and error shapes.
- MCP: the SDK's in-memory client and server transport, to verify each tool and that read tokens cannot see write tools.
- Live updates: open an event stream in a test, change a ticket through the service layer, and check the right event arrives, and that a non-member's stream gets nothing.

**Security checklist**

- Every project, ticket, label and comment lookup checks the caller's membership and role through the project, and returns 404 to non-members.
- Hash tokens at rest; compare in constant time; never log `Authorization` headers or passwords.
- Cap request bodies (1 MiB) and ticket field lengths (title 200, description 20,000 characters).
- Set `Content-Security-Policy`, `X-Content-Type-Options` and `Referrer-Policy` headers; render markdown with sanitizing, with no raw HTML allowed.

## Milestones and open questions

Build in five steps, each ending with something runnable. MCP comes third, so agents can use the board before the UI is polished.

1. **Foundation:** repo, config, SQLite with migrations, service layer for projects, members, tickets, labels and comments, with tests.
2. **Auth, sharing and REST:** signup, login, sessions, API tokens, project roles, all `/api/v1` endpoints, scope and role checks.
3. **Live updates:** the event hub and the event stream endpoint, wired into every service write.
4. **MCP:** `/mcp` endpoint with all tools, read/write scope filtering, activity log, and a test run from Claude Code.
5. **UI:** login, project sidebar, board with drag and drop, ticket panel, project settings, token management, live updates.
6. **Ship:** embed the UI, single-container Dockerfile with a Bun build stage, backup notes, security headers, README with MCP setup.

**Decided**

- Every project uses the same four fixed columns: Backlog, To do, In progress, Done.
- Projects are shared through memberships with owner, editor and viewer roles.
- Labels, assignees and comments are part of v1.
- Live updates use Server-Sent Events instead of polling.

**Open questions**

1. Add a stdio mode (`pabrika mcp --stdio`), where an agent app starts Pabrika itself as a local program instead of calling the server over HTTP? Recommended: skip it. The database lives inside the container, and HTTP with a token already works for local and remote agents.
