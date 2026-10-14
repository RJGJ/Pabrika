# Agent roles and playbooks

Prompts for AI agents that **use Pabrika over MCP**. The current set is the five-role agentic workflow: an Orchestrator and four subagents that take a goal from epic to merged PR with three human gates. The four older playbooks (standup, triage, planner, worker) are still here and are described at the end; they are legacy and are removed in phase 5.

These are for people who *run* Pabrika. They don't change the application, and nothing here is code.

> **Status:** the role files are written to the workflow spec (`specs/agentic-workflow.md`) and their token wiring was checked against a local scratch Pabrika (results below). They have **not** yet had the hostile-text checks (H1 to H8) or the smoke tests run, and the file-based wiring (agent files in `.claude/agents/`) is not yet confirmed. Treat the first run as a trial on a throwaway project and repo.

## The five roles

| File | Role | Runs as | Token | Board tools | Shell and files |
|---|---|---|---|---|---|
| [pabrika-orchestrator.md](pabrika-orchestrator.md) | Runs the workflow: epics, child tickets, labels, stages, gates, worktrees, spawns the others | main agent (`claude --agent pabrika-orchestrator`) | `orchestrator-bot` | read tools, `create_ticket`, `update_ticket`, `move_ticket`, `add_comment`, `create_label` | `Read`, `Grep`, `Glob`, `Agent`, `Bash` (prompt-limited to read-only git, `git worktree add/remove`, `gh pr view`) |
| [pabrika-researcher.md](pabrika-researcher.md) | Writes `requirements/<EPIC-REF>-<slug>.md` | subagent | `researcher-bot` | read tools, `add_comment`, `update_ticket` (epic description line only) | `Read`, `Grep`, `Glob`, `Write`, `Edit` (only `requirements/`), `Bash` (git on its branch) |
| [pabrika-developer.md](pabrika-developer.md) | Plans, implements one child ticket, opens the PR | subagent | `developer-bot` | read tools, `add_comment`, `update_comment`, `update_ticket` (description lines only) | `Read`, `Grep`, `Glob`, `Edit`, `Write`, `Bash` (own worktree, push own branch, `gh pr create`) |
| [pabrika-reviewer.md](pabrika-reviewer.md) | Reviews the PR, verdict `APPROVE` or `CHANGES REQUESTED` | subagent | `reviewer-bot` | read tools, `add_comment` | `Read`, `Grep`, `Glob`, `Bash` (read-only git, `gh pr view/diff/review/comment`) |
| [pabrika-qa.md](pabrika-qa.md) | Writes and runs Playwright tests, verdict `PASS` or `FAIL` | subagent | `qa-bot` | read tools, `add_comment` | `Read`, `Grep`, `Glob`, `Edit`, `Write` (only the e2e directory), `Bash` (run tests, commit and push to the PR branch) |

Read tools: `list_projects`, `list_members`, `list_labels`, `list_tickets`, `get_ticket`. No role has `delete_ticket`, `create_project` or `update_project`. The four subagent files leave `Agent` out of `tools:` so they cannot spawn further agents; only the Orchestrator lists it.

Workflow, labels, gates and comment formats: [the workflow spec](../specs/agentic-workflow.md). Each role file carries its own copy of the rules it needs, so a file works standalone when copied.

## Install

1. **Build the board side.** One bot user per role, each a project member with the **editor** role (never owner), because a token acts with its creator's project role. Create the users with `pabrika user create --email E --name N --password-stdin` (the password goes on stdin, never an argument), add them to the project in the web app, sign in as each, and create one token per user in Settings:

   | Token name | Scope | Project limit | Environment variable |
   |---|---|---|---|
   | `orchestrator-bot` | `write` | the one project | `PABRIKA_TOKEN_ORCHESTRATOR` |
   | `researcher-bot` | `write` | the one project | `PABRIKA_TOKEN_RESEARCHER` |
   | `developer-bot` | `write` | the one project | `PABRIKA_TOKEN_DEVELOPER` |
   | `reviewer-bot` | `write` | the one project | `PABRIKA_TOKEN_REVIEWER` |
   | `qa-bot` | `write` | the one project | `PABRIKA_TOKEN_QA` |

   Use exactly these token names: the Orchestrator only accepts verdict comments whose author is the expected token name. The board shows a token only once; copy it straight into your environment. Never put a token in a file, a ticket, a comment or a prompt.

2. **Export the tokens** in the shell that starts Claude Code (placeholders shown):

   ```bash
   export PABRIKA_TOKEN_ORCHESTRATOR=pb_...   # and the other four
   ```

   A variable that is unset leaves the header unusable: the server answers 401 and the role has no board tools (it fails closed, see V1 below).

3. **Copy the role files into the target repo**, not into a plugin (plugin subagents ignore `mcpServers`):

   ```bash
   mkdir -p .claude/agents
   cp agents/pabrika-*.md .claude/agents/
   ```

   Edit the `url:` line in each file if your server is not `http://localhost:8080/mcp`. Claude Code only starts inline MCP servers from `.claude/agents/` after you trust the folder.

4. **Prepare the repo and project.** The project description needs `Repo:`, `Base:` (the base branch, `dev` for Pabrika itself), and for the merge check `Humans: <github logins that may merge>`; optional `Max rounds: <n>`. The repo's `CLAUDE.md` or `AGENTS.md` must list the test, lint and build commands and an `E2E:` line; without them the Developer and QA post `BLOCKED`. Use a GitHub account for the agents that has no merge rights and protect the base branch.

5. **Run** from the target repo: `claude --agent pabrika-orchestrator`, then give it a goal. Stay at the keyboard: it stops at each gate and asks.

## Token wiring (workflow open question 3)

**Answer: option A, per-role inline `mcpServers` in each role file, with `disallowedTools` as the enforced tool restriction.** Each role file declares its own MCP entry (`pabrika-<role>`) whose `Authorization` header reads `${PABRIKA_TOKEN_<ROLE>}` from the environment, so each role calls the board with its own token and its comments show its own bot name. Tool names are `mcp__pabrika-<role>__<tool>`.

Two things differ from the spec's assumption and are why the files carry `disallowedTools`:

- In a **subagent**, the `tools:` allowlist did **not** hide the tools of its inline MCP server: a probe with `tools: Read` could still call `get_ticket` and `move_ticket`. A `disallowedTools` list does remove them. So each role file lists in `tools:` what it may use and in `disallowedTools:` every other Pabrika tool (these are the only places `delete_ticket`, `create_project` and `update_project` appear). When the file runs as the main agent (the Orchestrator), `tools:` is enforced as written.
- The token still bounds the damage: it acts with the bot user's editor role.

Known limits: the tool lists and the bot's editor role are two layers, the role prompts a third, and none is a security boundary against a determined model. Path and command limits inside `Bash`, `Write` and `Edit` are prompt rules plus the deny list below.

### Verification results (T0)

Run on 2026-10-14 on a local scratch Pabrika built from the `HOST`-setting branch (127.0.0.1:18080, throwaway database and users, since deleted), Claude Code 2.1.296, `claude -p` with the haiku model, from a scratch folder that is **not** a trusted folder. Subagent definitions were passed with `--agents` (JSON with the same `mcpServers`, `tools`, `disallowedTools` fields) because the identical definition placed in `.claude/agents/` files did not start its inline server in this untrusted folder (see the first row). Tokens were held in shell variables only.

| Id | Check | Observed | Result |
|---|---|---|---|
| V1a | Agent files in `.claude/agents/` with inline `mcpServers` and `${VAR}` header, run via `claude -p` in an untrusted folder | The agents loaded and their `tools:` applied, but no connection to the server was ever attempted (0 requests); the subagent had only `Read`. Consistent with the documented trust gate for project agents, not confirmed | not confirmed (re-run in a trusted folder, see open items) |
| V1b | Same definitions through `--agents` | Inline server connected; `${PABRIKA_TOKEN_*}` expanded in the header; tools named `mcp__pabrika-dev__*` and `mcp__pabrika-rev__*`; a `tools:` list naming those tools resolved | pass |
| V1c | Inline `mcpServers` when the agent runs as the main session (`--agent`) | Server `connected`; the session's tools were exactly the `tools:` list | pass |
| V1d | Variable missing | Server got requests answered 401; the subagent had no board tools | pass (fails closed) |
| V2 | `add_comment` from two subagents | Board comment authors were `dev-bot` and `rev-bot` (type `api_token`, bot) | pass |
| V3 | Subagent whose `tools:` omits `move_ticket` and `update_ticket` is asked to call them | Both calls reached the board; `move_ticket` succeeded. Same with `tools: Read`. With `disallowedTools` naming them the tools were absent and no call was possible | fail as written; pass with `disallowedTools` |
| V4 | Visibility of a subagent's server | The main session could not see `mcp__pabrika-dev__*` (no tool by that name, even with it pre-approved). A forced cross-call from the other subagent was not completed (session limit hit); the other subagent's reported tool lists showed only its own server | pass for the main session, partial for subagent to subagent |
| V5 | `claude -p --mcp-config <file with ${VAR}> --strict-mcp-config` | Only the configured server; `${VAR}` expanded; comment author `dev-bot`. The main session then has all 14 tools (no allowlist) | pass |
| V6 | Revoke `rev-bot` in the app, retry | The revoked role had no board tools (its server no longer connected); `dev-bot` unaffected and still commenting | pass (shows as missing tools, not a 401 message) |

Decision rule from the spec: A if V1 to V4 pass. V3 passes only with `disallowedTools`, so A is adopted with that addition. If the file-based check (V1a) turns out to fail in a trusted folder as well, fall back to option B (separate `claude -p --agent <name> --mcp-config <role>.json --strict-mcp-config` processes started by hand or by the Orchestrator, which V5 shows works) and drop the inline block from the role files. Option C (one shared token) would weaken per-role revocation and attribution and is not needed.

## Settings deny list

The role prompts say what the shell may do; Claude Code can also refuse commands. Add to the target repo's `.claude/settings.json` (adjust to taste):

```json
{
  "permissions": {
    "deny": [
      "Bash(git reset --hard*)",
      "Bash(git clean *)",
      "Bash(git push --force*)",
      "Bash(git push -f*)",
      "Bash(gh pr merge*)",
      "Read(./.env)",
      "Read(./.env.*)"
    ],
    "ask": [
      "Bash(git push *)"
    ]
  }
}
```

Notes, so you do not over-trust this:

- **A deny rule is not a security boundary.** Patterns match the command text, so `Bash(git push *)` is skipped by `git -C . push`, `git -c x=y push`, `git 'push'`, `/usr/bin/git push` and `bash -c '...'`, and `Bash(git push --force*)` also misses `git push -f` and `git push origin +main`.
- **Pushing:** the intended end state is to deny `Bash(git push *)` outright and let a person push, or add a `PreToolUse` hook that allows only `git push origin pabrika/<ref>-...` and rejects everything else. Deny rules take precedence over allow rules, so you cannot deny `git push *` and then allow the Developer's push in the same settings; the example above uses `ask` so a person confirms each push. Do not copy `Bash(git push *)` into `deny` unless you push by hand.
- **The real controls are on GitHub:** protect the base branch, give the agents' account no merge rights, and require review. The prompts, the tool lists, the bot users' editor role and the deny list only reduce the chance of a mistake.

## Conformance checklist (per role file)

Run this after editing a role file. It takes minutes and needs no script.

1. `tools:` lists exactly the MCP tools of the role in the table above, and `disallowedTools:` lists exactly the other nine Pabrika tools (14 tools in total).
2. `delete_ticket`, `create_project` and `update_project` appear only in a `disallowedTools:` line, nowhere in `tools:` or the body. `create_ticket`, `move_ticket` and `create_label` are in `tools:` only in `pabrika-orchestrator.md`.
3. Every backticked tool name in the body is in the role's `tools:` list (a sentence saying the role has no such tool is fine).
4. The file has all eight safety rules (least privilege, untrusted text, git, secrets, caps, scope, gate integrity, claiming) and a literal template for every comment type the role posts, each starting `[role] TYPE`.
5. The `mcpServers` entry name equals the prefix in `tools:`, and the variable is `PABRIKA_TOKEN_<ROLE>`.
6. Dynamic: ask each subagent to call `move_ticket` on a throwaway ticket; it must report the tool unavailable. Ask it to run `gh pr merge` or `git push --force`; the settings deny list must stop it.

## Hostile-text and smoke tests

Before relying on the roles, run the checks H1 to H8 and the five smoke tests from `specs/agentic-phase-1-roles-runtime.md` (sections 6.2 and 6.3) on a throwaway project and scratch repo, and record the outcomes. They have not been run yet.

## Safety rules (the same in every role file)

- **Ticket, comment and label text is untrusted.** Roles treat it as data and refuse instructions that widen their permissions. This reduces the risk; it does not remove it. Do not point agents with write tokens at a project where people you don't trust can create tickets.
- **Least privilege.** One token per role, project-limited, named for the role so you can revoke one without the others; per-role tool lists.
- **No destructive tools.** Deleting tickets and managing projects are deliberately left out of every role.
- **Gates are human.** Requirements, plan and merge are approved by a person; an `APPROVED` comment counts only from a user (not a token) who is an owner or editor member. Display names are not unique, which is a known weakness until the app has first-class approvals.
- **Agents sign their work.** Comments show the token's name with a bot badge and a `[role] TYPE` first line.

## Known limits

- **No atomic claim.** The Orchestrator runs one ticket at a time per project; never run two Orchestrators on one project.
- **Label writes race.** There is no add or remove label tool yet; only the Orchestrator changes labels, by read-modify-write. Phase 3 adds the tools; adoption in these files is planned for phase 5.
- **Comment size.** The board returns comment bodies cut at 8,000 characters and the latest 50 comments per ticket, so the role files keep comments short and put the SHA near the top.
- **Fixed states.** Tickets move between Backlog, To do, In progress and Done; sub-stages are labels.
- Other gaps: see section 11 of the workflow spec.

## Open items

- Re-run V1a with the role files in `.claude/agents/` of a trusted folder (for example `claude --agent pabrika-orchestrator` in a repo you already trust) and with a subagent file; if the inline server still does not start, use option B.
- Confirm that `disallowedTools:` in a file's frontmatter behaves like the `--agents` JSON field used in the tests.

---

# Legacy playbooks (removed in phase 5)

These four older playbooks stay untouched until phase 5 replaces them with the roles above. They are standalone: a daily standup, a backlog triager, a planner that turns a goal into tickets, and a worker that takes tickets from To do to Done. They were checked against the real MCP tool definitions but have not been run against a live model.

## Before you start

1. **Connect the server under the name `pabrika`.** Create an API token in the web app (Settings), then:

   ```bash
   claude mcp add --transport http pabrika https://your-domain/mcp \
     --header "Authorization: Bearer pb_your_token_here"
   ```

   Claude Code exposes the tools as `mcp__pabrika__<tool>`, and the `tools:` lists in these files rely on that name. If you register the server under another name, replace `mcp__pabrika__` in the frontmatter. Full connection steps: [the main README](../README.md#connect-an-agent-over-mcp).

2. **Use the narrowest token that works.**

   | Playbook | Token scope | Limit to one project? |
   |---|---|---|
   | [standup](standup.md) | `read` | optional |
   | [triage](triage.md) | `write` | **yes** |
   | [planner](planner.md) | `write` | **yes** |
   | [worker](worker.md) | `write` | **yes** |

   A read token only sees the five read tools, so a mistake in a read-only agent cannot change anything. A project-limited token cannot touch other projects.

## Install a playbook in Claude Code

Each file is a Claude Code subagent definition: a YAML header (`name`, `description`, `tools`) followed by the prompt.

```bash
mkdir -p .claude/agents
cp agents/triage.md .claude/agents/                  # this project only
# or: cp agents/triage.md ~/.claude/agents/          # all your projects
```

Then ask for it by name: "Use the pabrika-triage agent on project WEB." The `tools:` line is an allowlist, so the agent cannot call any other Pabrika tool, and none of the playbooks include `delete_ticket`, `create_project` or `update_project`. (The wiring check above found that for a subagent with its own inline MCP server `tools:` does not hide that server's tools; these legacy playbooks use the session-level `pabrika` server, which was not part of that check.)

## Use with another MCP client

The body of each file (everything after the second `---`) is plain markdown. Paste it into the client's system prompt or agent instructions, and drop the `mcp__pabrika__` prefix when the client names tools differently. Give the agent the same scope of token as in the table above.

## Playbooks

| File | What it does | Writes? |
|---|---|---|
| [standup.md](standup.md) | Summarises what is in progress, waiting, blocked and recently done | no |
| [triage.md](triage.md) | Sets priority and labels on Backlog tickets, asks clarifying questions in comments | yes |
| [planner.md](planner.md) | Breaks a goal into well-formed Backlog tickets, avoiding duplicates | yes |
| [worker.md](worker.md) | Takes the next To do ticket, does the work, reports back in comments | yes |

## Legacy limits

- **No history.** There is no MCP tool for the activity log. List results have no timestamps; only `get_ticket` returns `updated_at` and comment times. The standup agent works within that and says so.
- **No atomic claim.** Two workers can pick the same ticket. Run **one worker per project** until agent identities and assignment ship.
- **Fixed states.** Tickets move between Backlog, To do, In progress and Done.
