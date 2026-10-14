<!--
Rules for `gh pr create`:
- Title: `<TICKET-REF>: <ticket title>`.
- Base: the project's `Base:` branch (see the board project description).
- Fill the body from this template and pass it with `--body-file` (not the web form).
- A PR without an AC list under "Requirements" is a Reviewer finding.
-->
## Ticket
<TICKET-REF> <ticket title>

## Requirements
requirements/<EPIC-REF>-<slug>.md : AC1, AC3

## Changes
- <what changed and why, one line each>

## How verified
- Commands run and results (exact commands from CLAUDE.md), commit SHA: <sha>
- E2E: <spec file and result, or "n/a: <reason>">
