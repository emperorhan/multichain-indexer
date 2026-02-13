#!/usr/bin/env bash
set -euo pipefail

# Quick diagnostics for local Ralph loop health.
# Usage:
#   scripts/ralph_local_doctor.sh

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
RUNNER_LOG="${RALPH_ROOT}/logs/runner.out"
TRANSIENT_STATE_FILE="${RALPH_ROOT}/state.transient_failures"

echo "## Ralph Doctor"
echo "- time_utc: $(date -u +'%Y-%m-%d %H:%M:%S UTC')"
echo "- cwd: $(pwd)"
echo

if [ -x scripts/ralph_local_daemon.sh ]; then
  scripts/ralph_local_daemon.sh status || true
fi

echo
echo "### Auth Snapshot"
scripts/codex_auth_status.sh || true

echo
echo "### Process Snapshot"
pgrep -af "ralph_local_supervisor.sh|ralph_local_run.sh|codex" || true

echo
echo "### Queue Snapshot"
for d in issues in-progress done blocked; do
  echo "- ${d}:"
  ls -1 "${RALPH_ROOT}/${d}" 2>/dev/null || true
done

echo
echo "### Retry State"
if [ -f "${TRANSIENT_STATE_FILE}" ]; then
  echo "- transient_failures: $(awk 'NR==1 {print; exit}' "${TRANSIENT_STATE_FILE}" 2>/dev/null)"
else
  echo "- transient_failures: (missing)"
fi

echo
echo "### Codex Smoke"
set +e
codex --ask-for-approval never exec --model "${PLANNING_CODEX_MODEL:-gpt-5.3-codex}" --sandbox "${AGENT_CODEX_SANDBOX:-danger-full-access}" --cd "$(pwd)" "Reply with: ok" > /tmp/ralph-codex-smoke.log 2>&1
rc=$?
set -e
echo "- codex_smoke_exit: ${rc}"
tail -n 40 /tmp/ralph-codex-smoke.log || true

echo
echo "### Recent Runner Log"
tail -n 80 "${RUNNER_LOG}" 2>/dev/null || true
