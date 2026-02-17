# AGENTS.md

## Purpose
This repository runs a multi-agent autonomous loop for a multi-chain on-chain indexer (Solana/Base/BTC runtime targets).

## Agent Roles
- Planner (`role/planner`):
  - Updates `specs/*` and `IMPLEMENTATION_PLAN.md`
  - Optionally emits fanout task file for child issues (`.agent/planner-fanout-<issue>.json`)
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
- Local ON/OFF:
  - `scripts/ralph_local_control.sh on|off|status`
- Local daemon mode:
  - `scripts/ralph_local_daemon.sh start|stop|status`
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
