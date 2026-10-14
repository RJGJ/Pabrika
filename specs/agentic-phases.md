# Agentic Workflow: Implementation Phases

Order of work for implementing [agentic-workflow.md](agentic-workflow.md). That file is the source of truth for *what* the workflow is. This file says *in what order* and *what each phase delivers*. Each phase has its own detailed spec in this folder.

If a phase spec conflicts with `agentic-workflow.md`, the workflow spec wins; flag the conflict instead of deviating silently.

## Status

| # | Phase | Spec | Depends on | Status |
|---|---|---|---|---|
| 1 | Roles and runtime | [agentic-phase-1-roles-runtime.md](agentic-phase-1-roles-runtime.md) | none | draft |
| 2 | Repo conventions and onboarding | [agentic-phase-2-repo-conventions.md](agentic-phase-2-repo-conventions.md) | none | draft |
| 3 | Board safety gaps (Go, MCP) | [agentic-phase-3-board-safety.md](agentic-phase-3-board-safety.md) | none | draft |
| 4 | Gates and visibility (web UI) | [agentic-phase-4-gates-visibility.md](agentic-phase-4-gates-visibility.md) | 3 (label tool, scope) | draft |
| 5 | Dry run and rollout | [agentic-phase-5-dry-run-rollout.md](agentic-phase-5-dry-run-rollout.md) | 1, 2 (3 and 4 recommended) | draft |

Phases 1, 2 and 3 are independent and can run in parallel. Phase 5 is the exit test for the whole workflow (workflow spec section 13).

## Scope per phase

1. **Roles and runtime.** The five role files in `agents/` (Orchestrator, Researcher, Developer, Code Reviewer, QA Tester), the updated `agents/README.md`, the Claude Code wiring (subagents, per-role MCP entries and tokens, workflow open question 3), the handoff comment format, the safety rules of section 9 inside each role file. The four old playbooks are deleted only at the end of phase 5.
2. **Repo conventions and onboarding.** What a target repo (starting with Pabrika itself) needs: `requirements/` template, PR template, branch and worktree rules, Playwright `e2e/` setup and the `E2E:` line, the project-commands lines in `CLAUDE.md`/`AGENTS.md`, the `Repo:`/`Base:` project description, label creation checklist, token creation checklist.
3. **Board safety gaps.** Changes to the Go app and MCP server that remove the unsafe workarounds in workflow section 11: add/remove label tool (no whole-set replace), atomic claim or compare-and-set on status, comment-only or narrower token scope, multi-label filter. Follows `pabrika/CLAUDE.md` and the existing phase-spec style.
4. **Gates and visibility.** Web UI and API changes so people can run the gates and see state: workflow label badges and PR link on cards, round counter, an Approve / Request-changes action recorded as an event (instead of an `APPROVED` comment), a project-wide activity feed, optional notifications decision.
5. **Dry run and rollout.** The acceptance test: a throwaway project and repo takes one small epic from goal to merged PR with all three gates, one forced rework round and one forced `blocked` escalation. Then: delete the old playbooks, update root `README.md` and `AGENTS.md`, copy section 11 gaps into the backlog, start dogfooding on Pabrika.

## Out of scope (separate future specs)

Configurable board stages, real parent/child tickets, a runner that launches agents, auto-merge. Each needs its own spec; phase 4 and 5 may reference them but must not design them.

## Decisions needed from the user (all phases, merged)

Each phase spec has its own open questions. They overlap, so decide each of these once. Recommendations are the specs' Proposals.

| # | Decision | Phases | Recommendation |
|---|---|---|---|
| 1 | `Base:` branch for Pabrika: `dev` or `main` | 2, 5, workflow Q6 | Decide with the merge-protection policy; merge detection depends on it |
| 2 | Where `e2e/` lives (`pabrika/e2e/` or root) and whether Playwright may be added | 2 | `pabrika/e2e/`, separate package; yes to Playwright |
| 3 | Bot identity: separate bot user per role, or five tokens on one account | 1, 2 | Separate bot users, because a token acts with its owner's role |
| 4 | Reviewer/QA scope: `comment` only, or wider; may viewers comment | 3 | `comment` only; editors and owners only |
| 5 | `claim_ticket` tool, structured conflict detail, label filter OR/NOT, atomic label swap | 3 | None for now: use `move_ticket` + `expected_status`, AND-only filter, two label calls |
| 6 | Gates in the app: decisions table vs `APPROVED` comments; self-approval; grace period; label names hard-coded | 4 | Decisions table, record-only, session-only; allow self-approval; short grace period; hard-code |
| 7 | PR link: derive from description lines vs structured field; `list_activity` tool; feed query | 4 | Derive; ship `list_activity` if cheap; UNION query |
| 8 | Notifications: none, tab title, email, webhook, push | 4 | None now (tab title optional); spec the rest separately |
| 9 | Defaults: 60 minutes, 3 rounds; requirements landing (Researcher PR or first child PR) | 1, workflow Q1, Q4 | Keep defaults; Researcher's own PR |
| 10 | Small repo choices: onboarding doc committed, CI, frontmatter script, deny-list example file | 1, 2 | Checklist in the spec only, no CI yet, no script, deny list in README text |
| 11 | Dry-run logistics: second and third human accounts, bot GitHub account without merge rights, `APPROVED` case rule, one rollout PR or several, evidence retention | 5 | Yes to extra accounts and a bot account; case-sensitive; one PR; keep evidence until dogfood epic 3 |

**Resolved by the user (planning kickoff):** rows 1 and 3 are decided. `Base:` is `dev`. Each role gets its own bot user with the editor role (never owner), one token per bot. Agentic specs stay in the root `specs/` and plans go in the root `plans/` (`plans/agentic-phase-N-<slug>.md`). Rows 2 and 4 to 11 use the recommendation column as the working assumption until the user says otherwise.

The alignment pass also settled: round marker format is `[orchestrator] ROUND n/N` (workflow 5.4 edited), phase 4's migration is `00003`, and role-file adoption of phase 3 and 4 tools is phase 5 task 8.1a.

## Rules for every phase spec

- Concrete tasks, files, interfaces and tests, in the style of `pabrika/specs/phase-*.md`; list exit criteria and out of scope.
- Mark anything not already decided in `agentic-workflow.md` as **Proposal**; mark anything needing the user as an **Open question**.
- Phase specs describe work; they do not do it. No code or app changes while writing a spec.
