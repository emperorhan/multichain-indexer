#!/usr/bin/env bash
set -euo pipefail

# Initialize local-only Ralph workspace.
# Usage:
#   scripts/ralph_local_init.sh

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
ISSUES_DIR="${RALPH_ROOT}/issues"
IN_PROGRESS_DIR="${RALPH_ROOT}/in-progress"
DONE_DIR="${RALPH_ROOT}/done"
BLOCKED_DIR="${RALPH_ROOT}/blocked"
PLANS_DIR="${RALPH_ROOT}/plans"
REPORTS_DIR="${RALPH_ROOT}/reports"
LOGS_DIR="${RALPH_ROOT}/logs"
STATE_FILE="${RALPH_ROOT}/state.env"
CONTEXT_FILE="${RALPH_ROOT}/context.md"
TEMPLATE_FILE="${RALPH_ROOT}/issue-template.md"
SEED_DEFAULT_ISSUES="${RALPH_SEED_DEFAULT_ISSUES:-false}"

mkdir -p "${ISSUES_DIR}" "${IN_PROGRESS_DIR}" "${DONE_DIR}" "${BLOCKED_DIR}" "${PLANS_DIR}" "${REPORTS_DIR}" "${LOGS_DIR}"

if [ ! -f "${STATE_FILE}" ]; then
  cat >"${STATE_FILE}" <<'EOF'
RALPH_LOCAL_ENABLED=true
EOF
fi

if [ ! -f "${CONTEXT_FILE}" ]; then
  cat >"${CONTEXT_FILE}" <<'EOF'
# Ralph Local Context

## Product Goal
- Production-grade multi-chain indexer quality for:
  - `solana-devnet`
  - `base-sepolia`

## Operating Rules
- Keep all planning/issue context in local markdown under `.ralph/`.
- Work in small, reversible commits.
- Escalate major decisions via local decision markdown files under `.ralph/plans/`.

## Runtime Inputs
- Inject endpoints/config via environment variables:
  - `SOLANA_DEVNET_RPC_URL`
  - `BASE_SEPOLIA_RPC_URL`
EOF
fi

if [ ! -f "${TEMPLATE_FILE}" ]; then
  cat >"${TEMPLATE_FILE}" <<'EOF'
id: I-0000
role: developer
status: ready
priority: p1
depends_on:
title: Replace with title
complexity: medium
---

## Objective
- Replace with concrete objective.

## In Scope
- item 1

## Out of Scope
- item 1

## Acceptance Criteria
- [ ] criterion 1
- [ ] criterion 2

## Notes
- Optional context.
EOF
fi

seed_issue() {
  local id="$1"
  local role="$2"
  local priority="$3"
  local depends_on="$4"
  local title="$5"
  local complexity="$6"
  local body="$7"
  local path="${ISSUES_DIR}/${id}.md"

  [ -f "${path}" ] && return 0

  cat >"${path}" <<EOF
id: ${id}
role: ${role}
status: ready
priority: ${priority}
depends_on: ${depends_on}
title: ${title}
complexity: ${complexity}
---
${body}
EOF
}

if [ "${SEED_DEFAULT_ISSUES}" = "true" ]; then
  seed_issue "I-0001" "planner" "p0" "" \
    "M1 plan for dual-chain runtime bootstrap" "high" \
"## Objective
- Define a concrete M1 plan to make solana-devnet + base-sepolia runtime bootstrap executable.

## In Scope
- Update IMPLEMENTATION_PLAN.md to a single active milestone with acceptance criteria.
- Update specs/* rollout docs for M1 slices and QA gates.
- Produce follow-up developer/qa tasks as local markdown issues if decomposition is needed.

## Out of Scope
- Large refactors not required for M1.

## Acceptance Criteria
- [ ] M1 scope, DoD, risks, and decision placeholders are explicit.
- [ ] Developer tasks are decomposed and actionable."

  seed_issue "I-0002" "developer" "p0" "I-0001" \
    "Implement M1 slice for dual-chain runtime config/bootstrap" "high" \
"## Objective
- Implement first executable slice for dual-chain runtime bootstrap.

## In Scope
- Multi-chain config path for solana-devnet + base-sepolia.
- Runtime wiring with safe defaults and env-driven endpoints.
- Unit tests for config/runtime bootstrap behavior.

## Out of Scope
- Full production optimization beyond M1.

## Acceptance Criteria
- [ ] Slice compiles and tests pass in local CI command.
- [ ] Behavior is documented in README/specs."

  seed_issue "I-0003" "qa" "p0" "I-0002" \
    "QA gate for dual-chain runtime bootstrap slice" "medium" \
"## Objective
- Verify no-regression and chain-target correctness for M1 slice.

## In Scope
- Execute validation command and summarize failures with root-cause hypotheses.
- Confirm chain-target configuration paths are test-covered.

## Out of Scope
- Feature development.

## Acceptance Criteria
- [ ] QA report written to .ralph/reports/.
- [ ] If failed, create follow-up local developer issue(s) with repro context."
fi

echo "Initialized local Ralph workspace at ${RALPH_ROOT}"
echo "- queue: ${ISSUES_DIR}"
echo "- run: scripts/ralph_local_run.sh"
