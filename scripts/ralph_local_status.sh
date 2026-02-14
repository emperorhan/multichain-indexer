#!/usr/bin/env bash
set -euo pipefail

# Status board for local-only Ralph loop.
# Usage:
#   scripts/ralph_local_status.sh

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
STATE_FILE="${RALPH_ROOT}/state.env"

count_files() {
  local dir="$1"
  if [ ! -d "${dir}" ]; then
    echo "0"
    return 0
  fi
  find "${dir}" -maxdepth 1 -type f -name 'I-*.md' | wc -l | awk '{print $1}'
}

count_ready_issues() {
  local dir="$1"
  if [ ! -d "${dir}" ]; then
    echo "0"
    return 0
  fi
  find "${dir}" -maxdepth 1 -type f -name 'I-*.md' | while IFS= read -r file; do
    status="$(awk -F': ' '$1=="status"{print $2; exit}' "${file}" 2>/dev/null || true)"
    [ -n "${status}" ] || status="ready"
    if [ "${status}" = "ready" ]; then
      echo 1
    fi
  done | awk '{s+=$1} END {print s+0}'
}

enabled="unset"
if [ -f "${STATE_FILE}" ]; then
  enabled="$(awk -F= '/^RALPH_LOCAL_ENABLED=/{print $2; exit}' "${STATE_FILE}" 2>/dev/null || true)"
  [ -n "${enabled}" ] || enabled="unset"
fi

queue_count="$(count_ready_issues "${RALPH_ROOT}/issues")"
progress_count="$(count_files "${RALPH_ROOT}/in-progress")"
done_count="$(count_files "${RALPH_ROOT}/done")"
blocked_count="$(count_files "${RALPH_ROOT}/blocked")"

latest_commit="$(git log -1 --pretty='format:%h %ad %s' --date=iso-strict 2>/dev/null || echo "none")"

cat <<EOF
## Ralph Local Status

- updated_utc: $(date -u +'%Y-%m-%d %H:%M:%S UTC')
- ralph_local_enabled: ${enabled}
- queue_ready: ${queue_count}
- in_progress: ${progress_count}
- done: ${done_count}
- blocked: ${blocked_count}
- latest_commit: ${latest_commit}
EOF
