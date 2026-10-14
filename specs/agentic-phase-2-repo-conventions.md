# Phase 2: Repo conventions and onboarding

Detailed spec for phase 2 of [agentic-phases.md](agentic-phases.md). [agentic-workflow.md](agentic-workflow.md) is the source of truth (mainly sections 4, 5.1, 7, 8, 9.1) and wins on any conflict; conflicts are flagged, not silently resolved. Items not already decided there are marked **Proposal**; items needing the user are **Open question**. This spec describes work; it changes no app code.

**Goal:** make a target repo (starting with Pabrika itself) ready for the five roles: a requirements template, a PR template, branch and worktree rules, a Playwright `e2e/` setup with an `E2E:` line, project-command lines in `CLAUDE.md`/`AGENTS.md`, a `Repo:`/`Base:` project description, the workflow labels, and one token per role.

## 1. Facts found in the repo (checked, not assumed)

- Git root is `D:\rj\Pabrika`; the Go module root is `pabrika/` (next to `go.mod`). Root `CLAUDE.md` is a pointer; root `AGENTS.md` and `pabrika/CLAUDE.md` hold the commands. There is no `.github/` folder yet.
- `pabrika/web/package.json` has `dev`, `build`, `typecheck`, `test` (vitest). **Playwright is not installed anywhere**; there is no `e2e/` folder.
- Label palette (`pabrika/internal/service/validate.go`, `LabelColors`): `gray, red, orange, amber, green, teal, blue, indigo, purple, pink`. All five workflow colors (gray, blue, amber, orange, red) are in it, so the mapping in section 3 is one to one. `create_label` rejects any other color.
- Ticket refs are `<KEY>-<n>`; project keys are 2 to 6 uppercase letters. Project description max 2,000 characters.
- Branches: `main` (default), `dev` (current working branch).
- Root `.gitignore` already ignores `node_modules/`, `.env`, `data/`, `*.db`, `*.db-wal`, `*.db-shm`, `*.log`, and `pabrika/web/dist/*` (except `.gitkeep`).

## 2. In scope and out of scope

In scope: repo files listed in section 4; a one-time board setup checklist (labels, tokens, project description); conventions text the role files in phase 1 will cite.

Out of scope: role files and Claude Code wiring (phase 1); any Go, MCP or web UI change (phases 3 and 4); the dry run (phase 5); CI configuration (**Proposal**: not now, listed in open questions); real Playwright tests for Pabrika features beyond one smoke test; configurable stages, parent/child tickets, runner, auto-merge.

## 3. Label table (workflow 5.1 mapped to the board palette)

Labels are created by the Orchestrator on first use with `create_label`; this table is the checklist and the exact arguments. Names are lower-case with hyphens (<= 50 characters).

| Label | Color arg | Meaning |
|---|---|---|
| `epic` | `gray` | Ticket kind |
| `task` | `gray` | Ticket kind |
| `needs-requirements` | `blue` | Researcher's turn |
| `needs-dev` | `blue` | Developer's turn or rework |
| `needs-review` | `blue` | Code Reviewer's turn |
| `needs-qa` | `blue` | QA Tester's turn |
| `awaiting-req-approval` | `amber` | Gate 1 |
| `awaiting-plan-approval` | `amber` | Gate 2 |
| `awaiting-merge` | `amber` | Gate 3 |
| `changes-requested` | `orange` | Review or QA failed |
| `blocked` | `red` | Needs a human |

Rule behind the colors: gray = kind, blue = an agent is up, amber = a human is up, orange = rework, red = stop. Kept from workflow 5.1; no new colors needed.

## 4. Tasks

### Task 1: Requirements template

Create `requirements/TEMPLATE.md` at the git root (not under `pabrika/`, so the same path rule works for any repo). Real files are `requirements/<EPIC-REF>-<slug>.md` (workflow 7). **Proposal:** the template carries a header block and one fixed-order section list so the Researcher and QA can parse it.

```markdown
# <EPIC-REF>: <Epic title>

Epic: <EPIC-REF>
Status: draft | approved   <!-- set to approved by the Orchestrator after Gate 1 -->

## Goal
One paragraph: the problem and the outcome. No solution design.

## Users and use cases
- UC1: As a <user>, I <do something> so that <value>.

## Acceptance criteria
Each criterion is testable and has a stable id. Ids are never renumbered after approval.
- AC1 (UC1): Given <state>, When <action>, Then <observable result>.

## Out of scope
- <thing that must not be built in this epic>

## Open questions
- <question>, or "None".
```

Validation rule (**Proposal**, stated in the file's top comment and enforced by the Reviewer, not by a script): the five `##` headings above exist in that order; every AC has a Given/When/Then; every AC names at least one UC; `Open questions` is `None` before Gate 1.

### Task 2: PR template

Create `.github/pull_request_template.md` (git root). Sections match workflow 7: Ticket, Requirements, Changes, How verified.

```markdown
## Ticket
<TICKET-REF> <ticket title>

## Requirements
requirements/<EPIC-REF>-<slug>.md : AC1, AC3

## Changes
- <what changed and why, one line each>

## How verified
- Commands run and results (exact commands from CLAUDE.md), commit SHA: <sha>
- E2E: <spec file and result, or "n/a: <reason>">
```

Rules for `gh pr create`: title `<TICKET-REF>: <ticket title>`; base is the project's `Base:` branch; body filled from this template (a role passes `--body-file`, not the web form). **Proposal:** a PR without an AC list under `Requirements` is a Reviewer finding.

### Task 3: Branch and worktree rules (documented, no files besides CLAUDE.md lines)

Add the rules below to the "Agent workflow" block in section Task 5. Source: workflow 7 and 9.3.

- Branch `pabrika/<TICKET-REF>-<slug>`; slug is lower-case ASCII, hyphens, max 40 characters (**Proposal**).
- Worktree `../wt-<TICKET-REF>` next to the repo, so for Pabrika `D:\rj\wt-<TICKET-REF>`. Create: `git worktree add ../wt-PAB-12 -b pabrika/PAB-12-csv-export origin/<Base>`. Remove after merge or close: `git worktree remove ../wt-PAB-12` (never `--force`; if it refuses, post `BLOCKED:`).
- Only the Orchestrator creates and removes worktrees. Commands run from the worktree root; for Pabrika the Go module root is `<worktree>/pabrika`.
- Never push to `Base:` or to a branch the agent did not create; never force-push (workflow 9.3).
- Each worktree needs its own dependency install (`cd web && bun install --frozen-lockfile`); `node_modules/` is gitignored.

### Task 4: Playwright e2e setup

Playwright is not in the repo, so this task adds it. **Proposal:** put it in a separate package `pabrika/e2e/` (own `package.json` and `bun.lock`) instead of adding dependencies to `pabrika/web`, so the shipped web build and its lockfile do not change and the QA Tester's edit rights are limited to one directory (workflow 3: "edit only the e2e test directory"). The workflow default `e2e/` is read relative to the Go module root for Pabrika (open question 1).

Files to create:

1. `pabrika/e2e/package.json`: private, `"type": "module"`, scripts `"test": "playwright test"`; devDependency `@playwright/test` pinned to an exact version (same exact-pin style as `web/package.json`; version chosen at implementation time).
2. `pabrika/e2e/playwright.config.ts`: sketch.
   - `testDir: '.'`, `testMatch: '*.spec.ts'`, `workers: 1`, `retries: 0`, `use.baseURL: 'http://localhost:8080'`, project `Edge` only.
   - `webServer`: command `go run ./cmd/pabrika serve` with `cwd: '..'`, `url: 'http://localhost:8080/healthz'`, `reuseExistingServer: false`, `env`: `COOKIE_SECURE=false`, `BASE_URL=http://localhost:8080`, `DB_PATH` set to a temp file under `e2e/.tmp/` (gitignored), `ALLOW_SIGNUP=true`. Starting with a fresh DB each run keeps tests independent. The web bundle must already be built (Pabrika serves it embedded; without it the server answers 503), so the `E2E:` line below builds first.
3. `pabrika/e2e/smoke.spec.ts`: one test titled `AC0: server serves the sign-in page` that opens `/login` and expects the page to load. Purpose: prove the setup works before any feature spec exists. File naming for real tests stays `<TICKET-REF>.spec.ts`, test titles begin with the AC id (workflow 7).
4. `pabrika/e2e/README.md`: three lines: install, run, where tests go. (**Proposal**; the only doc file in this task.)
5. Append to root `.gitignore`: `e2e/.tmp/`, `pabrika/e2e/.tmp/`, `pabrika/e2e/test-results/`, `pabrika/e2e/playwright-report/`.

Test users: tests create users through signup (enabled by `ALLOW_SIGNUP=true` in the test server env only) or by piping the password to the CLI as documented in `pabrika/CLAUDE.md` (`--password-stdin`, never an argument). Test credentials are generated per run and never written into tickets or comments (workflow 9.4).

The `E2E:` line (exact text, goes into `AGENTS.md` and `pabrika/CLAUDE.md`, see Task 5):

```text
E2E: cd pabrika/web && bun install --frozen-lockfile && bun run build && cd ../e2e && bun install --frozen-lockfile && bun x playwright test
```

(Per-run output is the Playwright list reporter; failing test names appear as `AC<n>: ...`. Run from the worktree root.)

### Task 5: Project-commands lines in `AGENTS.md` and `pabrika/CLAUDE.md`

Workflow 7 says the repo's `CLAUDE.md` or `AGENTS.md` lists the test, lint and build commands. The Build, Test, Lint and Typecheck lines are copied from the real commands in `pabrika/CLAUDE.md` (checked). The `Gates`, `E2E:`, `Branch/Worktree/Base` and `Secrets` lines are new. Add this block to root `AGENTS.md` (replacing nothing; phase 5 updates the roles list) and the same block, shortened to the command lines, to `pabrika/CLAUDE.md` under a new `## Agent workflow` heading. Root `CLAUDE.md` stays a pointer.

```text
## Agent workflow (Pabrika board)
Run all commands from the worktree root unless stated.
Build web: cd pabrika/web && bun install --frozen-lockfile && bun run build
Build Go: cd pabrika && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=dev" -o pabrika ./cmd/pabrika
Test Go: cd pabrika && CGO_ENABLED=0 go test ./...
Lint Go: cd pabrika && go vet ./...
Test web: cd pabrika/web && bun run test
Typecheck web: cd pabrika/web && bun run typecheck
Gates (all must pass before READY): Build web, Test Go, Lint Go, Test web, Typecheck web
E2E: cd pabrika/web && bun install --frozen-lockfile && bun run build && cd ../e2e && bun install --frozen-lockfile && bun x playwright test
Branch: pabrika/<TICKET-REF>-<slug>   Worktree: ../wt-<TICKET-REF>   Base: see board project description
Secrets: never read .env or deploy/ env files; never put tokens in tickets, comments, files or PRs
```

Notes: `make` is not assumed (it was not installed in the reference environment, per `pabrika/CLAUDE.md`). `CGO_ENABLED=1 go test -race ./...` is optional and not a gate. `docker build` has not been run, so it is not a gate either.

### Task 6: Board project description (`Repo:` / `Base:`)

Set once by a person with `update_project` (no role has it, workflow 3) in the Pabrika project's description. **Proposal:** exact text, within the 2,000-character limit:

```text
Repo: D:\rj\Pabrika
Base: dev
Max rounds: 3
Humans: <github logins allowed to merge>
Pabrika board for building Pabrika itself.
```

`Humans:` is the list the Orchestrator checks `mergedBy` against (phase 1 Proposal; workflow open question 6).

Workflow 4 defines `Repo:` as "path or URL". `Max rounds:` is optional (workflow 5.4). Use `main` instead of `dev` once the branch policy is settled (open question 2). A role that finds `Repo:` or `Base:` missing posts `BLOCKED:` rather than guessing.

### Task 7: One-time onboarding checklist (done by a person; not committed code)

Put this checklist in the Pabrika repo as `pabrika/docs/agentic-onboarding.md` **only if the user wants a file** (open question 4); otherwise it lives in this spec. Steps:

1. Create the board project (key **Proposal**: `PAB`; keys are 2 to 6 uppercase letters) and set its description (Task 6).
2. Create the 11 labels from section 3 (or let the Orchestrator do it on first use; verify with `list_labels`).
3. Add the human approvers as project members with role owner or editor (gate integrity, workflow 5.2). Agents' bot accounts are members too but must not be given owner if the app allows choosing (verify the roles in the member UI).
4. Create one bot user per role, or reuse one account with one token per role (open question 3). Token names: `orchestrator-bot`, `researcher-bot`, `developer-bot`, `reviewer-bot`, `qa-bot` (workflow 9.1). For each: scope `write` (after phase 3: `comment` for `reviewer-bot` and `qa-bot`), limited to the single Pabrika project, copy the token once into the operator's secret store (shown only at creation), never into the repo.
5. Confirm the agents' GitHub identity cannot merge or push to the base branch (branch protection on `Base:`; workflow open question 6).
6. Verify: run `list_projects` with each token and confirm only the one project is visible; confirm a `read`-style call works and `delete_ticket` is not in any role's tool list.
7. Run Task 4's `E2E:` line once on a clean checkout and record the result.

## 5. Verification

- `requirements/TEMPLATE.md` renders and contains the five required `##` headings in order (check by grep).
- `.github/pull_request_template.md` has the four sections (grep). Create a draft PR from a scratch branch with `gh pr create --body-file` and confirm the body is used; close the PR without merging.
- On a clean clone, the `E2E:` line exits 0 with exactly one passing test (`AC0: ...`) and leaves no tracked changes (`git status` clean; only ignored files created).
- Each command in the Task 5 block is run once from a fresh worktree created per Task 3 and exits 0 (record versions and OS in the PR).
- `git worktree add` / `remove` per Task 3 works from the real repo path on Windows (Git Bash) and the branch name passes `git check-ref-format --branch`.
- `list_labels` shows the 11 labels with the colors in section 3; `create_label` with a non-palette color is not used.
- Token checklist step 6 evidence is recorded (no token values).
- Scope check: `git diff --stat` touches only `requirements/`, `.github/`, `pabrika/e2e/`, `AGENTS.md`, `pabrika/CLAUDE.md`, `.gitignore` (and optionally the onboarding doc).

## 6. Exit criteria

1. The files in Tasks 1, 2, 4 and 5 are merged to the base branch via the normal review.
2. The Pabrika board project has the description from Task 6, the 11 labels, and five role tokens (names only recorded).
3. The e2e smoke test passes from a clean clone using only the `E2E:` line.
4. A role reading only `AGENTS.md`/`pabrika/CLAUDE.md` can find build, test, lint, e2e, branch and worktree rules without asking.
5. No Go, MCP or web app file changed.

## 7. Proposals (not yet confirmed by the user)

- `requirements/TEMPLATE.md` structure, header block and the Reviewer-enforced validation rule.
- PR template wording and the "no AC list is a finding" rule.
- Slug rule (lower-case ASCII, max 40) and `git worktree remove` without `--force`.
- Playwright in a separate `pabrika/e2e/` package, Microsoft Edge only (`channel: 'msedge'`, no browser download), one worker, fresh temp DB per run, one `AC0` smoke test.
- The exact `E2E:` line and the gate list ("Gates" line).
- Project key `PAB` and the `Repo: D:\rj\Pabrika` / `Base: dev` text.
- No CI in this phase.

## 8. Open questions

1. Where does `e2e/` live for Pabrika: `pabrika/e2e/` (proposed, next to the Go module) or `e2e/` at the git root (matches the workflow's default literally)? This changes the `E2E:` line and QA's edit scope.
2. `Base:` for Pabrika: `dev` (today's working branch) or `main`? Agent PRs merge into it, so branch protection and the merge-detection check (workflow open question 6) depend on the answer.
3. Per-role tokens: one board user per role, or one shared bot user with five tokens? The token list assumes tokens can be created per user with a project limit; the app does allow one user up to 100 active named tokens (checked in the code), but each token acts with its owner's role, so five tokens on one human's account inherit that human's permissions; separate bot users limit that. Decide which. Ties to workflow open question 3 and phase 1.
4. Should the onboarding checklist be a committed doc (`pabrika/docs/agentic-onboarding.md`) or stay only in this spec?
5. Add CI (GitHub Actions running the Gates line) so "CI results are preferred" (workflow 8.3) holds? Not in this phase unless the user says so.
6. May Playwright be added to the repo's dev dependencies (supply-chain and size cost; it uses the installed Edge, no browser download)? Required for the QA role either way.
7. Should the requirements file merge with the Researcher's own PR after Gate 1 or with the first child PR? Workflow open question 4; this phase's template works with both.
