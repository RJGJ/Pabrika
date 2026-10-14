# Agentic Phase 1 plan: Roles and runtime

Basis: `specs/agentic-phase-1-roles-runtime.md` (scope source), `specs/agentic-phases.md` (resolved decisions), `specs/agentic-workflow.md` (wins on conflict; sections 3, 5, 6, 9, 10, open question 3). Style follows `pabrika/plans/phase-*.md`.

**Resolved decisions used here:** base branch is `dev`; one bot user per role with the editor role (never owner), one token per bot (`orchestrator-bot`, `researcher-bot`, `developer-bot`, `reviewer-bot`, `qa-bot`, scope `write`, one project); specs in root `specs/`, plans in root `plans/`. Working assumptions from phases.md: defaults 60 minutes and 3 rounds; manual checklist, no frontmatter script; deny list as README text only; `Humans:` list in the project description for merge detection.

**Repo facts**
- `agents/` holds `README.md`, `standup.md`, `triage.md`, `planner.md`, `worker.md` (prompts only, no code). The four old playbooks stay untouched until phase 5.
- Only files under `agents/` change (plus this plan). No Go, web or MCP code. Confirm the `agents/` location (repo root vs `pabrika/agents/`) with `git ls-files | grep agents/` before step 1 and use that path throughout.
- Tool names are fixed by the current MCP server: `list_projects`, `list_members`, `list_labels`, `list_tickets`, `get_ticket`, `create_ticket`, `update_ticket`, `move_ticket`, `add_comment`, `update_comment`, `create_label`.

## Prerequisites from other phases
- None hard. Phases 1, 2, 3 are independent.
- Phase 2 (`E2E:` line, `requirements/`, PR template) is referenced by the Researcher and QA files as conventions only; the roles `BLOCKED:` when the line is missing, so no wait is needed.
- Phase 3/4 tools (label add/remove, `comment` scope, Approve action) are NOT adopted here; the role files use the `update_ticket` read-modify-write workaround. Adoption is phase 5 task 8.1a.
- Needs for T0 and smoke tests: a local throwaway Pabrika (`go run ./cmd/pabrika serve`), a throwaway project, a scratch GitHub repo, the Claude Code CLI (version recorded), and `gh` authenticated on a bot account without merge rights.

## Sequencing

```
S0 branch ── S1 T0 wiring verification ──┬─ S2 role files A (orchestrator) ─┐
                                          ├─ S3 role files B (researcher, developer) ├─ S6 README ── S7 conformance ── S8 hostile tests ── S9 smoke tests ── S10 close-out
                                          └─ S4 role files C (reviewer, qa) ─┘
              S5 (draft README skeleton) can start with S0
```

- Serial: S0, then S1 (its outcome fixes the `mcpServers` block and tool-name prefix), then S2 to S4, then S6 to S10.
- Parallel group A (after S1): S2, S3, S4 touch separate files.
- Parallel group B (after S0, before S1 ends): S5 README skeleton (roles table, legacy note, token creation, deny list text). It is finalised in S6.
- Parallel group C (after S7): S8 and S9 on separate throwaway projects.

## S0: Branch and baseline
- Files: none.
- Change: `git checkout dev && git pull && git checkout -b feat/agentic-phase-1-roles-runtime`. Check `git status` is clean.
- Verify: `git branch --show-current`; `git diff dev --stat` empty.
- Estimate: S. Executor: main agent (git).

## S1: T0 token-wiring verification (decides option A, B or C)
- Files: scratch `.claude/agents/probe-dev.md`, `probe-rev.md`, `probe-orch.md` in a throwaway target repo outside this repo. Results are written into this plan (section "T0 results") and later into `agents/README.md`.
- Change: create `dev-bot` and `rev-bot` tokens (scope `write`, one throwaway project). Define probes with inline `mcpServers` (`pabrika-dev`, `pabrika-rev`, `Authorization: Bearer ${PABRIKA_TOKEN_*}`). Run V1 to V6 from the spec (5.1), plus the `claude --agent probe-orch` main-session check. Apply decision rule: A if V1 to V4 pass, else B if V5 passes, else C.
- Verify: results table with columns check, observed, pass/fail, Claude Code version; comment authors on the board show `dev-bot` and `rev-bot`; V3 refusal is from Claude Code, not model choice.
- Estimate: M. Executor: main agent (design judgment, token handling; the user creates tokens, never paste them in files). opencode may run the mechanical `claude -p --strict-mcp-config` V5 commands once configs exist.

## Parallel group A: role files (after S1)

Every file: frontmatter (`name`, `description`, exact `tools`, `mcpServers` per S1 outcome), role statement, token needed, inputs/outputs/stop conditions, numbered steps with exact tool names, the eight safety rules (self-contained), literal comment templates (`[<role>] <TYPE>` first line), report section. No `delete_ticket`, `create_project`, `update_project`. Verify each file with the S7 checklist.

### S2: `agents/pabrika-orchestrator.md`
- Change: tools = read tools + `create_ticket`, `update_ticket`, `move_ticket`, `add_comment`, `create_label`; `Read`, `Grep`, `Glob`, `Agent`, `Bash` (prompt-limited to read-only git, `git worktree add/remove`, `gh pr view`). Content: label table with colors and at-most-one `needs-*`/`awaiting-*` rule; epic and child lifecycle tables (workflow 5.2, 5.3); label read-modify-write; approval check (author owner/editor via `list_members`, case-sensitive `APPROVED`); head-SHA staleness check; verdict-author check (`reviewer-bot`/`qa-bot`, H8); `gh pr view --json state,mergedBy` merge detection against `Humans:`; worktree rules; one ticket at a time; subagent spawn instructions; templates `STARTED`, `ROUND n/N`, `BLOCKED`, `DONE`; rounds default 3.
- Verify: S7 checklist; `grep -c "mcp__" ` against tool table.
- Estimate: L. Executor: main agent (gate logic, security).

### S3: `agents/pabrika-researcher.md` and `agents/pabrika-developer.md`
- Change: Researcher tools = read + `add_comment`, `update_ticket` (description only, epic only); `Read`, `Grep`, `Glob`, `Write`/`Edit` (only `requirements/`), `Bash` (git on own branch). Sections `Goal`, `Users and use cases`, `Acceptance criteria` (Given/When/Then, ids), `Out of scope`, `Open questions`; file name `requirements/<EPIC-REF>-<slug>.md`; no code; `REQUIREMENTS` template. Developer tools = read + `add_comment`, `update_comment`, `update_ticket` (own ticket, description lines `PR:`/`Branch:`); `Read`, `Grep`, `Glob`, `Edit`, `Write`, `Bash`. Plan-first (`PLAN`), gates from repo `CLAUDE.md`/`AGENTS.md` else `BLOCKED:`, branch `pabrika/<REF>-<slug>`, PR format (workflow 7), `READY`, `FIXED`, `BLOCKED`, 60 minute cap.
- Verify: S7 checklist; templates contain every required field of spec 4.2.
- Estimate: M each. Executor: opencode drafts from the spec tables, main agent reviews safety blocks (can be run as two parallel opencode tasks).

### S4: `agents/pabrika-reviewer.md` and `agents/pabrika-qa.md`
- Change: Reviewer tools = read + `add_comment`; `Read`, `Grep`, `Glob`, `Bash` (read-only git, `gh pr view/diff/review/comment`). Checklist (correctness, security, scope, tests, AC ids), never edits code, numbered findings `file:line, issue, why`, verdict only for current head SHA, `APPROVE`/`CHANGES REQUESTED`. QA tools = read + `add_comment`; `Read`, `Grep`, `Glob`, `Edit`, `Write` (only e2e dir), `Bash`. Reads `E2E:` line, one Playwright test per AC titled with AC id, `e2e/<REF>.spec.ts`, untestable criteria reported, `PASS`/`FAIL`, `BLOCKED:` if no `E2E:` line.
- Verify: S7 checklist.
- Estimate: M each. Executor: opencode drafts, main agent reviews safety.

## S5: README skeleton (Parallel group B)
- File: `agents/README.md`.
- Change: roles table, install (copy into target `.claude/agents/`, not as a plugin since plugin subagents ignore `mcpServers`), token creation (5.2, placeholders only), settings deny list text (deny `Bash(git push *)` outright, `git reset --hard*`, `gh pr merge*`; note deny rules are not a boundary, recommend a `PreToolUse` hook and GitHub branch protection), "legacy playbooks, removed in phase 5" note, no-`Agent` in the four subagents.
- Verify: grep README for secrets patterns (`Bearer [A-Za-z0-9]`), none; links resolve.
- Estimate: S. Executor: opencode.

## S6: Finalise README with T0 outcome
- Change: add chosen wiring option, V1 to V6 table, known limits (option C weakens rule 9.1 if reached), wiring snippet matching S2 to S4.
- Verify: README snippet equals the frontmatter block in the role files (diff the two).
- Estimate: S. Executor: main agent.

## S7: T7 conformance checklist (Serial, after S2 to S6)
- Change: add the 6.1 checklist to `agents/README.md`; run items 1 to 4 on all five files; item 5 (dynamic tool unavailability, `gh pr merge` deny) on the throwaway project.
- Verify: `grep -rn "delete_ticket\|create_project\|update_project" agents/pabrika-*.md` empty; `grep -ln "create_ticket\|move_ticket\|create_label" agents/pabrika-*.md` lists only the orchestrator; each file has eight safety rules and all templates.
- Estimate: M. Executor: opencode for the grep runs; main agent for the dynamic step.

## Parallel group C (after S7)

### S8: H1 to H8 hostile-text tests
- Change: create tickets on the throwaway project with the spec texts; run each applicable role; record outcomes in this plan. Fix any failure in the role file and re-run; none waived.
- Verify: each run posts `BLOCKED:` naming the refusal without echoing secrets, no forbidden call or label change, `git` branch unchanged (H4).
- Estimate: L. Executor: main agent (security judgment); opencode may run the repetitive sessions and report transcripts.

### S9: Smoke tests (6.3)
- Change: five smoke runs against a scratch repo: Researcher (5 sections, one `REQUIREMENTS`), Developer (`PLAN`, PR, `READY` with SHA), Reviewer (seeded bug -> `CHANGES REQUESTED`, after fix `APPROVE`, SHA matches head), QA (test per AC, `PASS`/`FAIL`), Orchestrator two-ticket scenario with existing labels preserved.
- Verify: board and PR show the expected comments and labels.
- Estimate: L. Executor: opencode runs and reports; main agent judges results.

## S10: Close-out
- Change: fill result tables in this plan; run exit-criteria check: `git diff dev --stat` shows only `agents/pabrika-*.md`, `agents/README.md` and `plans/agentic-phase-1-roles-runtime.md`; old playbooks unchanged (`git diff dev -- agents/standup.md agents/triage.md agents/planner.md agents/worker.md` empty). Answer open question 3 in writing in README.
- Estimate: S. Executor: main agent.

## Commit plan (branch `feat/agentic-phase-1-roles-runtime` off `dev`; commits by the main agent only)
1. `docs: add agentic phase 1 implementation plan` (this file, then T0 results)
2. `docs: add pabrika-orchestrator role file`
3. `docs: add researcher and developer role files`
4. `docs: add reviewer and qa role files`
5. `docs: update agents README with install, tokens, wiring and checklist`
6. `docs: fix role files after hostile-text and smoke tests` (only if needed)
Each commit ends with the Co-Authored-By trailer. Merge to `dev` via PR; no push or merge without the user's go-ahead.

## Risks
- Env-var expansion not applied in inline `mcpServers`, or the `tools:` pattern fails to resolve before the server connects (V1). Mitigation: option B, then C.
- A bot token acts with its owner's role, so a shared account would erase role separation: one bot user per role (decided).
- Prompt-only limits (path and command scope) can be bypassed; the deny list is not a boundary. Mitigation: bot GitHub account without merge rights, branch protection, `PreToolUse` hook.
- Role files drifting from the workflow spec; the spec wins, flag conflicts instead of editing silently.
- Option B: `--strict-mcp-config` vs inline `mcpServers` precedence undocumented; keep inline blocks out of option B files.
- Tokens leaking into files or transcripts during T0; use environment variables only and revoke the throwaway tokens at close-out.
- Label read-modify-write races because there is no label add/remove tool; one ticket at a time per project mitigates.

## Rollback
Docs-only change. Revert by deleting the branch or reverting the commits; the old playbooks are untouched so nothing breaks. Revoke throwaway tokens and delete the throwaway project and scratch repo.
