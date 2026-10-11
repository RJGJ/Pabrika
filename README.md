# Pabrika

Pabrika (Filipino for "factory") is an agentic way of building software: AI agents and people work from one shared board, and agents plan, pick up and report on work through it.

This repository holds the pieces that make that work. Today there are two:

| Folder | What it is |
|---|---|
| [`pabrika/`](pabrika/) | The kanban web app: a self-hosted board with an MCP server. It is the shared source of truth for work. Go backend, SQLite, Vue frontend, one Docker container. |
| [`agents/`](agents/) | Ready-made agent playbooks that use the board over MCP: standup, triage, planner, worker. |

## How it fits together

```text
goal ──> planner ──> tickets on the board ──> worker ──> Done
                          ^        ^
                  triage ─┘        └─ standup (reads and summarises)
```

People and agents both read and write tickets on the board. Agents connect through the board's MCP endpoint (`/mcp`) with an API token, so their access is limited by token scope and, optionally, by project.

## Getting started

1. **Run the board.** Follow the quick start in [pabrika/README.md](pabrika/README.md#quick-start-docker).
2. **Connect an agent.** Create an API token in the app and add the MCP server: [Connect an agent over MCP](pabrika/README.md#connect-an-agent-over-mcp).
3. **Install a playbook.** Copy one from [agents/](agents/README.md) into your Claude Code agents folder.

## Repository layout

```text
pabrika/       the kanban web app (Go module root: go.mod, web/, migrations/, deploy/, docs/, specs/)
agents/        agent playbooks (prompts, not code)
AGENTS.md      instructions for coding agents working on this repo
CLAUDE.md      pointer to pabrika/CLAUDE.md
```

Run Go, Bun and Docker commands from inside `pabrika/`.

## Status

Early. The board works when built and run without Docker; the Docker image, proxy and backup steps are written but not yet run, and the playbooks have not been run against a live model. Each sub-README says exactly what was and was not verified.

## Documentation

- [pabrika/README.md](pabrika/README.md): setup, configuration, MCP, proxy, backups, security notes.
- [pabrika/CLAUDE.md](pabrika/CLAUDE.md): code layout, commands, conventions and gotchas.
- [pabrika/specs/](pabrika/specs/): the main spec and per-phase specs.
- [agents/README.md](agents/README.md): playbook install and token scopes.
- [pabrika/LICENSE](pabrika/LICENSE): MIT.
