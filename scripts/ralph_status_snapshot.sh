#!/usr/bin/env bash
set -euo pipefail

# Generates a concise markdown status snapshot for mobile checking.
# Usage:
#   scripts/ralph_status_snapshot.sh [owner/repo]

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required. Install from https://cli.github.com/" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required." >&2
  exit 1
fi

REPO="${1:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}"
NOW_UTC="$(date -u +'%Y-%m-%d %H:%M:%S UTC')"

ralph_enabled="$(gh variable get RALPH_LOOP_ENABLED --repo "${REPO}" 2>/dev/null || true)"
[ -z "${ralph_enabled}" ] && ralph_enabled="unset"
autopilot_enabled="$(gh variable get RALPH_AUTOPILOT_ENABLED --repo "${REPO}" 2>/dev/null || true)"
[ -z "${autopilot_enabled}" ] && autopilot_enabled="unset"
qa_chain_targets="$(gh variable get QA_CHAIN_TARGETS --repo "${REPO}" 2>/dev/null || true)"
[ -z "${qa_chain_targets}" ] && qa_chain_targets="unset"

runner_line="$(gh api "repos/${REPO}/actions/runners" \
  --jq '.runners[]? | select((.labels | map(.name) | index("multichain-indexer")) != null) | "\(.name) | \(.status) | busy=\(.busy)"' \
  | head -n1 || true)"
[ -z "${runner_line}" ] && runner_line="(runner not found)"

open_counts="$(gh issue list --repo "${REPO}" --state open --limit 200 --json labels \
  | jq -r '
      def has_label($x): ((.labels // []) | map(.name) | index($x)) != null;
      {
        autonomous_ready: ([.[] | select(has_label("autonomous") and has_label("ready"))] | length),
        in_progress: ([.[] | select(has_label("in-progress"))] | length),
        decision_needed: ([.[] | select(has_label("decision-needed"))] | length),
        qa_ready: ([.[] | select(has_label("qa-ready"))] | length)
      }')"

autonomous_ready="$(jq -r '.autonomous_ready' <<<"${open_counts}")"
in_progress="$(jq -r '.in_progress' <<<"${open_counts}")"
decision_needed="$(jq -r '.decision_needed' <<<"${open_counts}")"
qa_ready="$(jq -r '.qa_ready' <<<"${open_counts}")"

recent_runs_json="$(gh run list --repo "${REPO}" --limit 30 --json databaseId,name,status,conclusion,createdAt)"
last_run() {
  local workflow_name="$1"
  jq -r --arg name "${workflow_name}" '
    (map(select(.name == $name)) | sort_by(.createdAt) | reverse | .[0]) as $r
    | if $r == null then "no-runs"
      else "#\($r.databaseId) \($r.status)/\($r.conclusion // "n/a") @ \($r.createdAt)"
      end
  ' <<<"${recent_runs_json}"
}

agent_run="$(last_run "Agent Loop")"
autopilot_run="$(last_run "Agent Loop Autopilot")"
scout_run="$(last_run "Issue Scout")"
manager_run="$(last_run "Manager Loop")"
qa_run="$(last_run "QA Loop")"
release_run="$(last_run "Release")"

latest_release="$(gh release list --repo "${REPO}" --limit 1 --json tagName,publishedAt,isLatest \
  --jq 'if length == 0 then "none" else .[0].tagName + " @ " + .[0].publishedAt end')"

cat <<EOF
## Ralph Loop Status Board

- updated: ${NOW_UTC}
- repo: ${REPO}
- ralph_loop_enabled: ${ralph_enabled}
- ralph_autopilot_enabled: ${autopilot_enabled}
- qa_chain_targets: ${qa_chain_targets}
- runner: ${runner_line}
- latest_release: ${latest_release}

### Queue
- autonomous_ready: ${autonomous_ready}
- in_progress: ${in_progress}
- decision_needed: ${decision_needed}
- qa_ready: ${qa_ready}

### Recent Workflow Runs
- Agent Loop: ${agent_run}
- Agent Loop Autopilot: ${autopilot_run}
- Issue Scout: ${scout_run}
- Manager Loop: ${manager_run}
- QA Loop: ${qa_run}
- Release: ${release_run}
EOF
