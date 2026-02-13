#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   AGENT_EXEC_CMD='your executor command' \
#   AGENT_RUNNER='self-hosted' \
#   AGENT_MAX_ISSUES_PER_RUN='2' \
#   AGENT_MAX_AUTO_RETRIES='2' \
#   DECISION_REMINDER_HOURS='24' \
#   scripts/setup_agent_loop.sh [owner/repo]

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required. Install from https://cli.github.com/" >&2
  exit 1
fi

REPO="${1:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}"
AGENT_EXEC_CMD_VALUE="${AGENT_EXEC_CMD:-}"
AGENT_RUNNER_VALUE="${AGENT_RUNNER:-}"
AGENT_MAX_ISSUES_PER_RUN_VALUE="${AGENT_MAX_ISSUES_PER_RUN:-2}"
AGENT_MAX_AUTO_RETRIES_VALUE="${AGENT_MAX_AUTO_RETRIES:-2}"
DECISION_REMINDER_HOURS_VALUE="${DECISION_REMINDER_HOURS:-24}"

if [ -z "${AGENT_EXEC_CMD_VALUE}" ]; then
  echo "AGENT_EXEC_CMD is required." >&2
  echo "Example: AGENT_EXEC_CMD='make test && make lint' scripts/setup_agent_loop.sh ${REPO}" >&2
  exit 1
fi

set_repo_var() {
  local name="$1"
  local value="$2"
  gh variable set "${name}" --repo "${REPO}" --body "${value}" >/dev/null
  echo "set ${name}"
}

echo "Configuring agent loop variables on ${REPO}"
set_repo_var "AGENT_EXEC_CMD" "${AGENT_EXEC_CMD_VALUE}"
set_repo_var "AGENT_MAX_ISSUES_PER_RUN" "${AGENT_MAX_ISSUES_PER_RUN_VALUE}"
set_repo_var "AGENT_MAX_AUTO_RETRIES" "${AGENT_MAX_AUTO_RETRIES_VALUE}"
set_repo_var "DECISION_REMINDER_HOURS" "${DECISION_REMINDER_HOURS_VALUE}"

if [ -n "${AGENT_RUNNER_VALUE}" ]; then
  set_repo_var "AGENT_RUNNER" "${AGENT_RUNNER_VALUE}"
fi

echo "Done."
echo "Next:"
echo "1) Ensure .github/workflows/agent-loop.yml is enabled"
echo "2) Create Autonomous Task issues with labels: autonomous + ready"
echo "3) Protect main branch (scripts/setup_branch_protection.sh ${REPO} main)"
