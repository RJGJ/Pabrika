<!--
Validation rule (enforced by the Code Reviewer, not by a script):
- the five `##` headings below exist, in this order;
- every AC has Given/When/Then and names at least one UC;
- `Open questions` is "None" before Gate 1.
Real files are named requirements/<EPIC-REF>-<slug>.md.
-->
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
