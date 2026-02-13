# IMPLEMENTATION_PLAN.md

## Current Phase
- Phase: RALPH loop governance + multi-agent collaboration hardening
- Owner-input mode: enforced via `decision/major`

## Milestones
1. Governance
- Global `RALPH_LOOP_ENABLED` switch
- Major decision guard (`decision/major` -> owner input required)

2. Role Specialization
- Manager loop for whitelist QA set discovery
- QA loop for validation + defect handoff
- Developer loop for implementation and PR automation

3. Planning-First Workflow
- Planner artifacts maintained in `specs/*`
- Prompt separation (`PROMPT_plan.md`, `PROMPT_build.md`)

## Open Decisions
- Final Solana whitelist source of truth:
  - `SOLANA_WHITELIST_CSV` variable
  - `configs/solana_whitelist_addresses.txt` file

## Next Recommended Task
- Populate real Solana whitelist addresses and run end-to-end manager -> QA -> developer loop verification.
