# Pabrika e2e

Run (from the worktree root; builds the web bundle first, uses the installed Microsoft Edge (no browser download), starts the server on a fresh temp DB under `.tmp/`):

E2E: cd pabrika/web && bun install --frozen-lockfile && bun run build && cd ../e2e && bun install --frozen-lockfile && bun x playwright test

Tests go in `<TICKET-REF>.spec.ts` files here; test titles begin with the AC id (`AC1: ...`). Test passwords are generated per run and never committed.
