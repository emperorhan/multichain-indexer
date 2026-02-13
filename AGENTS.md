# AGENTS.md

## Purpose
This repository runs a multi-agent autonomous loop for a Solana on-chain indexer.

## Agent Roles
- Planner (`role/planner`):
  - Updates `specs/*` and `IMPLEMENTATION_PLAN.md`
  - Escalates major decisions via `decision/major`
- Developer (`role/developer`):
  - Implements `autonomous + ready` issues
  - Runs tests/lint/build and opens draft PRs
- QA (`role/qa`):
  - Validates manager-provided whitelist address sets
  - Creates bug issues for developer queue on failure
- Manager (`role/manager`):
  - Discovers whitelist subsets and creates `qa-ready` issues

## Loop Governance
- Global ON/OFF switch: repository variable `RALPH_LOOP_ENABLED`
- Toggle via:
  - `.github/workflows/ralph-loop-control.yml`
  - `scripts/toggle_ralph_loop.sh on|off`
- Local iterative mode (feature branch only):
  - `scripts/ralph_loop_local.sh`

## Required Validation
- `make test`
- `make test-sidecar`
- `make lint`

## Planning-First Files
- `PROMPT_plan.md`
- `PROMPT_build.md`
- `IMPLEMENTATION_PLAN.md`
- `specs/*`
