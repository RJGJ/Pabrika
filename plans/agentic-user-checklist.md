# Agentic workflow: everything only you can do, in one place

Merged from the five phase plans (`plans/agentic-phase-*.md`). Everything else is done by the main agent or `opencode`. Do the batches in order; each batch unblocks the work after it. Never paste tokens or passwords in chat or files; keep them in your environment variables or secret store.

Working assumptions (change any by saying so): `Base:` is `dev`; one bot user per role with the editor role; project key `PAB`; Playwright in `pabrika/e2e/`; comment scope `comment`; 60 minutes and 3 rounds; no CI yet.

## Batch 1: before any work starts (about 20 minutes)

| # | Step | Needed by | Source |
|---|---|---|---|
| 1 | Confirm the plans may run in the order: phase 1 and 2 and 3 in parallel, then 4, then 5. Reply "go" in chat. | all | this file |
| 2 | Run the Pabrika app locally for the main agent (or tell me it is already running and where). | phase 1 T0, phase 3 manual checks | phase 1 plan S1 |
| 3 | Create a throwaway board project `TST` and two bot users with tokens (`dev-bot`, `rev-bot`, scope `write`, limited to `TST`), exported as `PABRIKA_TOKEN_DEV` and `PABRIKA_TOKEN_REV`. | phase 1 T0 (token wiring test) | phase 1 plan |
| 4 | Real project: create board project `PAB` (name Pabrika), set the description (`Repo: D:\rj\Pabrika`, `Base: dev`, `Max rounds: 3`, `Humans: <your GitHub login>`). | phase 2 | phase 2 U1, U2 |
| 5 | Create the five bot users (`orchestrator-bot`, `researcher-bot`, `developer-bot`, `reviewer-bot`, `qa-bot`) with the CLI (`--password-stdin`), add each to `PAB` as editor, never owner. Add other human approvers as owner or editor. | phase 2 | phase 2 U3, U4 |
| 6 | Log in as each bot once and create its token (name = the bot name, scope `write`, limited to `PAB`). Switch `reviewer-bot` and `qa-bot` to `comment` after phase 3 ships. | phase 1 and 5 | phase 2 U5 |
| 7 | GitHub: protect `dev` (no direct pushes or force-push by the agents' identity, merge only by you). Create or pick the agents' GitHub account or fine-grained token with push and PR rights but no merge. | phase 2 PR, phase 5 | phase 2 U6, phase 5 U3 |

## Batch 2: at review points (each takes minutes)

| # | Step | When |
|---|---|---|
| 8 | Review and merge each phase PR into `dev` (phase 2, 1, 3, 4 in that order of readiness; the main agent opens them, you merge). | after each phase's verification |
| 9 | Verify the 11 labels with `list_labels` (or let the Orchestrator create them first). | after phase 2 merge |
| 10 | Verify each token sees only `PAB` and no role has `delete_ticket`. | after step 6 |
| 11 | Answer anything the phase plans flag as open (the cross-phase table in `specs/agentic-phases.md`, rows 2 and 4 to 11, if my assumptions are wrong). | when asked |

## Batch 3: before the dry run (phase 5)

| # | Step | When |
|---|---|---|
| 12 | Create the private GitHub repo `pabrika-dryrun`; protect `main` (PR required); share the clone URL. | before S1 |
| 13 | Confirm a second and third human account (`human-editor`, `human-viewer`) exist, or waive those tests. | before S1 |
| 14 | Create the DRY bot tokens (log in as each bot; shown once). | S1 |

## Batch 4: during the dry run (the main agent pings you)

| # | Step | When |
|---|---|---|
| 15 | Gate 1: comment `CHANGES: add AC for missing version`, then `APPROVED` (or the buttons if phase 4 shipped). | S3 step 3 |
| 16 | Gate 2: approve the plan on the epic. | S3 step 5 |
| 17 | Gate 3: merge the DRY-2 PR, then DRY-3, on GitHub with your own account. | S3 steps 9 and 10 |
| 18 | Negative tests: post a few comments as the viewer, as a quoted-`APPROVED` human, and as a duplicate-display-name editor; start the second Claude session as `human-editor` at the same time as the first. | S5 N2, N3, N9 |
| 19 | Remove `blocked` from DRY-4 and DRY-5 as owner to prove the legitimate unblock path. | after N7 |
| 20 | Review `checks.md` and `findings.md`; sign off or waive with a reason. | end of S6 |

## Batch 5: rollout and cleanup

| # | Step | When |
|---|---|---|
| 21 | Decide the six workflow open questions and the first dogfood epic. | R4 |
| 22 | Merge the rollout PR into `dev`. | after R5 |
| 23 | Revoke the DRY tokens (or tell me to do it by CLI) and archive or delete `pabrika-dryrun`. | T1 |

## Where interruptions can still happen

- Phase 1 T0 may show per-subagent tokens do not work. The plan then falls back to separate sessions or a shared token and asks you to accept the weaker option.
- Any fact-check item marked "cannot verify" (sqlc ordering, GitHub merge rights on a bot token) can surface a surprise during implementation.
- A failed negative test in phase 5 reopens an earlier phase.
