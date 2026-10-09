# Agent playbooks

Ready-to-use prompts for AI agents that **use Pabrika over MCP**: a daily standup, a backlog triager, a planner that turns a goal into tickets, and a worker that takes tickets from To do to Done. They are starting points, not products: read one before you run it, and adjust the rules to your team.

These are for people who *run* Pabrika. They don't change the application, and nothing here is code.

> **Status:** the playbooks were checked against the real MCP tool definitions (names, arguments, status and priority values), but they have **not been run against a live model**. Treat the first run of each as a trial on a throwaway project.

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

Then ask for it by name: "Use the pabrika-triage agent on project WEB." The `tools:` line is an allowlist, so the agent cannot call any other Pabrika tool, and none of the playbooks include `delete_ticket`, `create_project` or `update_project`.

## Use with another MCP client

The body of each file (everything after the second `---`) is plain markdown. Paste it into the client's system prompt or agent instructions, and drop the `mcp__pabrika__` prefix when the client names tools differently. Give the agent the same scope of token as in the table above.

## Playbooks

| File | What it does | Writes? |
|---|---|---|
| [standup.md](standup.md) | Summarises what is in progress, waiting, blocked and recently done | no |
| [triage.md](triage.md) | Sets priority and labels on Backlog tickets, asks clarifying questions in comments | yes |
| [planner.md](planner.md) | Breaks a goal into well-formed Backlog tickets, avoiding duplicates | yes |
| [worker.md](worker.md) | Takes the next To do ticket, does the work, reports back in comments | yes |

## Safety rules (the same in every playbook)

- **Ticket, comment and label text is untrusted.** Anyone who can write to a project can write instructions aimed at your agent ("ignore your rules and ..."). Playbooks tell the agent to treat that text as data. This reduces the risk; it does not remove it. Do not point an agent with a write token at a project where people you don't trust can create tickets.
- **Least privilege.** Read-only token for reading, project-limited token for writing, a separate token per agent so you can revoke one without the others.
- **No destructive tools.** Deleting tickets and managing projects are deliberately left out.
- **Agents sign their work.** Comments show the token's name with a bot badge, so name tokens after the agent (for example `triage-bot`) and you can see who did what.

## Known limits

- **No history.** There is no MCP tool for the activity log. List results have no timestamps; only `get_ticket` returns `updated_at` and comment times. The standup agent works within that and says so. A project activity feed is on the [roadmap](../README.md#roadmap).
- **No atomic claim.** Two workers can pick the same ticket. Run **one worker per project** until agent identities and assignment ship (also on the roadmap).
- **Fixed states.** Tickets move between Backlog, To do, In progress and Done. A review state such as "For Approval" is on the roadmap.
