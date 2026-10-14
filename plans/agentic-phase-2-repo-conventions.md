# Agentic phase 2 plan: Repo conventions and onboarding

Basis: `specs/agentic-phase-2-repo-conventions.md` (scope source), `specs/agentic-phases.md` (resolved decisions), `specs/agentic-workflow.md` (wins on conflict), `pabrika/CLAUDE.md` (real commands). This plan describes work; nothing is implemented by writing it.

**Decisions applied:** `Base:` is `dev`. One bot user per role, editor role, never owner, one token each. Specs in root `specs/`, plans in root `plans/`. Playwright lives in `pabrika/e2e/` (separate package). Other open questions use the phases.md recommendations (no CI, onboarding checklist stays in the spec, no new doc file).

**Scope guard:** only `requirements/`, `.github/`, `pabrika/e2e/`, root `AGENTS.md`, `pabrika/CLAUDE.md`, root `.gitignore`. No Go, MCP or web app file changes.

**Prerequisites from other phases:** none to start (phases 1, 2, 3 are independent). Soft links: the phase 1 role files cite the files created here; the phase 5 dry run consumes them; the `comment` token scope for reviewer/QA bots arrives with phase 3 (until then those two bots use `write`).

**Branch:** `feat/agentic-phase-2-repo-conventions` off `dev` (`git switch dev && git switch -c feat/agentic-phase-2-repo-conventions`).

## Step 0: Branch and baseline (main agent, S)

- Files: none.
- Change: create the branch off `dev`; confirm `git status` clean.
- Verify: `git branch --show-current` prints the new branch; `git log -1 dev` equals the branch tip.

## Parallel group A (no dependencies, after step 0)

### A1: Requirements template (opencode, S)
- File: `requirements/TEMPLATE.md` (git root).
- Change: write the template from spec Task 1 verbatim, including the header block (`Epic:`, `Status:`), the five `##` headings in order, and a top HTML comment stating the validation rule (Reviewer-enforced, not scripted).
- Verify: `grep -n "^## " requirements/TEMPLATE.md` lists exactly Goal, Users and use cases, Acceptance criteria, Out of scope, Open questions, in that order.

### A2: PR template (opencode, S)
- File: `.github/pull_request_template.md` (create `.github/`).
- Change: four sections from spec Task 2 (Ticket, Requirements, Changes, How verified); add the `gh pr create` rules (title, base, `--body-file`, "no AC list is a finding") as an HTML comment at the top.
- Verify: `grep -c "^## " .github/pull_request_template.md` is 4 and the headings match.

### A3: Playwright package (opencode for files, main agent picks the version, M)
- Files: `pabrika/e2e/package.json`, `pabrika/e2e/playwright.config.ts`, `pabrika/e2e/smoke.spec.ts`, `pabrika/e2e/README.md`, generated `pabrika/e2e/bun.lock`.
- Change: per spec Task 4. `package.json` private, `"type": "module"`, scripts `test` and `install-browsers`, `@playwright/test` pinned to an exact version (main agent chooses the version at implementation time; check it is at least 2 weeks old and matches the style of `web/package.json`). Config: chromium only, `workers: 1`, `retries: 0`, `webServer` runs `go run ./cmd/pabrika serve` with `cwd: '..'`, healthcheck URL `http://localhost:8080/healthz`, env `COOKIE_SECURE=false`, `BASE_URL=http://localhost:8080`, `ALLOW_SIGNUP=true`, `DB_PATH` under `e2e/.tmp/`. Smoke test titled `AC0: server serves the sign-in page` opening `/login`. README three lines (install, run, where tests go).
- Verify: `cd pabrika/e2e && bun install` creates `bun.lock`; `bunx tsc --noEmit` is optional; full run is verified in step B2.
- Note: write a short `test password` rule into README only (generated per run, never committed).

### A4: `.gitignore` entries (opencode, S)
- File: root `.gitignore` (it has a BOM per the phase 6 plan; preserve it, append only).
- Change: append `e2e/.tmp/`, `pabrika/e2e/.tmp/`, `pabrika/e2e/test-results/`, `pabrika/e2e/playwright-report/`.
- Verify: `git check-ignore -v pabrika/e2e/.tmp/x pabrika/e2e/test-results/x pabrika/e2e/playwright-report/x` matches all three; `git diff` shows only added lines at the end and the first bytes unchanged.

### A5: Agent workflow block (main agent, S)
- Files: root `AGENTS.md` (append block, replace nothing), `pabrika/CLAUDE.md` (new `## Agent workflow` heading, shortened to command lines, placed before "Specs and plans").
- Change: spec Task 5 block verbatim (Build web/Go, Test Go/web, Lint, Typecheck, Gates, `E2E:`, Branch/Worktree/Base, Secrets) plus Task 3 branch and worktree rules (slug rule, worktree add/remove, only the Orchestrator creates worktrees, never `--force`, no push to `Base:`, per-worktree `bun install`).
- Why main agent: wording is read by every role; must match real commands in `pabrika/CLAUDE.md` exactly and not conflict with workflow 7 and 9.3.
- Verify: each command line in the block appears (diff by eye or `grep -F`) in the existing Build/Test sections; `grep -n "^E2E:" AGENTS.md pabrika/CLAUDE.md` finds both; the `E2E:` text is identical in both files and in A3's README.

## Group B: verification (depends on all of Group A)

### B1: Worktree rules check (main agent, S)
- Change: none (scratch). From the repo, run `git check-ref-format --branch pabrika/PAB-1-demo-slug`, then `git worktree add ../wt-PAB-1 -b pabrika/PAB-1-demo-slug dev`, run one Task 5 command inside it (for example `cd pabrika && CGO_ENABLED=0 go vet ./...`), then `git worktree remove ../wt-PAB-1` and `git branch -D pabrika/PAB-1-demo-slug` (scratch branch only; main agent confirms before deleting).
- Verify: all exit 0; `git worktree list` shows only the main worktree afterwards.

### B2: E2E line on a clean worktree (opencode runs, main agent reviews, M)
- Change: in a fresh worktree of the feature branch, run the exact `E2E:` line.
- Verify: exit 0, exactly one passing test `AC0: ...`, `git status` clean (only ignored files created). Record Go, Bun, Playwright versions and OS for the PR body. If port 8080 is busy, free it rather than changing the config.

### B3: Other commands in the Task 5 block (opencode, S)
- Change: run Build web, Build Go, Test Go, Lint Go, Test web, Typecheck web from a fresh worktree.
- Verify: each exits 0; paste results into the PR body. Can run in parallel with B2 in separate worktrees.

### B4: Scope and template checks (opencode, S)
- Verify: `git diff --stat dev...HEAD` touches only the allowed paths; `grep` checks for A1 and A2 headings; `git diff dev...HEAD -- '*.go' 'pabrika/web/src' 'pabrika/internal'` is empty.

## Group C: PR (depends on B)

### C1: Pull request (main agent, S)
- Change: push the branch (needs user's explicit go-ahead per git rules), open the PR into `dev` with `gh pr create --base dev --body-file` using the repo's new PR template shape. Draft PR check for the template: create from a scratch branch, confirm `--body-file` is used, close without merging (spec section 5).
- Verify: PR shows base `dev`, body lists the commands run and results.

## Commit plan (main agent, small focused commits on `feat/agentic-phase-2-repo-conventions`)

1. `docs: add requirements template` (A1)
2. `docs: add pull request template` (A2)
3. `chore: ignore e2e output folders` (A4)
4. `test: add Playwright e2e package with smoke test` (A3, including `bun.lock`)
5. `docs: add agent workflow commands and branch rules to AGENTS.md and CLAUDE.md` (A5)

Each commit ends with the Co-Authored-By line from the attribution reminder. Commits are made by the main agent only; opencode never commits.

## Estimates and parallelism

| Step | Executor | Size | Depends on |
|---|---|---|---|
| 0 | main agent | S | none |
| A1, A2, A4 | opencode | S each | 0 (parallel) |
| A3 | opencode + main agent (version pick) | M | 0 (parallel) |
| A5 | main agent | S | 0 (parallel) |
| B1 | main agent | S | A5 |
| B2, B3 | opencode (parallel, separate worktrees) | M, S | A1 to A5 |
| B4 | opencode | S | A1 to A5 |
| C1 | main agent | S | B1 to B4 |
| U1 to U8 | user | see list | exit criteria 2 to 4 need U steps; U steps can run any time in parallel with the above, except U7 and U8 which need A3 and A5 merged |

Total: about one session for the repo work, about 30 to 45 minutes of user time.

## Risks

1. Playwright and a Chromium download add supply-chain and size cost (spec open question 6); mitigated by a separate package, exact pin, no change to `web/` lockfile.
2. Port 8080 or leftover `e2e/.tmp` DB makes the smoke test flaky; config forces a fresh DB path and `reuseExistingServer: false`.
3. The `E2E:` line depends on a built web bundle (without it the server returns 503); the line builds first. It also needs `go` on PATH inside the Playwright webServer.
4. Windows (Git Bash) quirks: path conversion, `cd` chains, worktree paths outside the repo (`D:\rj\wt-*`). B1 and B2 run on the real environment.
5. Root `.gitignore` BOM: an editor that strips or doubles it creates a noisy diff; check with `git diff`.
6. `Base: dev` is a working branch, not a protected release branch; branch protection and merge detection (workflow Q6) hinge on it. User step U6 covers protection.
7. Bot tokens in `write` scope for reviewer/QA until phase 3 lands (`comment` scope); keep editor role and one-project limit as mitigation.
8. Docs drift: the Task 5 block duplicates real commands; a later command change must update both files (a Reviewer check).
9. Editing `pabrika/CLAUDE.md` and `AGENTS.md` can conflict with phase 1 and phase 5 edits; keep the A5 block self-contained and rebase before merge.

## Rollback

All changes are additive files and appended lines on a feature branch. Before merge: delete the branch (`git switch dev && git branch -D feat/agentic-phase-2-repo-conventions`, user confirms). After merge: `git revert` the five commits in reverse order (or the merge commit with `-m 1`). Board changes (labels, description, bot users, tokens) are reverted by hand: delete labels, restore the old project description, revoke tokens and remove bot members. Nothing touches app code or the database, so no data migration is involved.

## User checklist (do in one sitting; main agent supplies the exact text)

Do these in the Pabrika web app unless noted. Never paste token values into chat, tickets or the repo.

- [ ] U1: Create the board project with key `PAB` (2 to 6 uppercase letters) named Pabrika.
- [ ] U2: Set its description (under 2,000 characters):
  `Repo: D:\rj\Pabrika` / `Base: dev` / `Max rounds: 3` / `Humans: <your GitHub login(s)>` / `Pabrika board for building Pabrika itself.` (one item per line)
- [ ] U3: Create the five bot users (`orchestrator-bot`, `researcher-bot`, `developer-bot`, `reviewer-bot`, `qa-bot`; use the CLI `printf '%s\n' '<password>' | go run ./cmd/pabrika user create --email ... --name ... --password-stdin` run from `pabrika/`, passwords stored in your secret store), add each to project PAB with the editor role, never owner.
- [ ] U4: Add yourself and any other human approvers to PAB as owner or editor.
- [ ] U5: As each bot user, create one API token with the matching name (`orchestrator-bot`, etc.), scope `write` (switch reviewer and QA to `comment` after phase 3), limited to project PAB only. Copy each once into your secret store.
- [ ] U6: On GitHub, enable branch protection on `dev` (no direct pushes by the agents' GitHub identity, merges only by you; no force-push). Confirm the agents' GitHub account cannot merge.
- [ ] U7: Create the 11 labels (or let the Orchestrator create them on first use, then verify with `list_labels`): epic, task (gray); needs-requirements, needs-dev, needs-review, needs-qa (blue); awaiting-req-approval, awaiting-plan-approval, awaiting-merge (amber); changes-requested (orange); blocked (red).
- [ ] U8: Verify with each token: `list_projects` shows only PAB; no role has `delete_ticket` in its tool list. Then run the `E2E:` line once on a clean checkout of `dev` after the PR merges and note the result (no token values).
- [ ] U9: Approve the PR from step C1 and merge it into `dev` yourself (also confirm the push in step C1).

Exit criteria mapping: exit 1 and 3 by B2 and merge (U9); exit 2 by U1 to U8; exit 4 by A5; exit 5 by B4.
