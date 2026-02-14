#!/usr/bin/env bash
set -euo pipefail

# Runtime-focused status view for local Ralph loop.
# - systemd service health
# - currently running in-progress issue(s)
# - next ready issue in queue

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

SERVICE_NAME="${RALPH_LOCAL_SERVICE_NAME:-ralph-local.service}"
RALPH_ROOT="${RALPH_ROOT:-.ralph}"
IN_PROGRESS_DIR="${RALPH_ROOT}/in-progress"
QUEUE_DIR="${RALPH_ROOT}/issues"

get_field() {
  local key="$1"
  local file="$2"
  awk -F': ' -v key="${key}" '$1==key{print $2; exit}' "${file}" 2>/dev/null || true
}

count_ready_issues() {
  local dir="$1"
  if [ ! -d "${dir}" ]; then
    echo "0"
    return 0
  fi
  find "${dir}" -maxdepth 1 -type f -name 'I-*.md' | while IFS= read -r file; do
    status="$(get_field status "${file}")"
    [ -n "${status}" ] || status="ready"
    if [ "${status}" = "ready" ]; then
      echo 1
    fi
  done | awk '{s+=$1} END {print s+0}'
}

daemon="stopped"
active_state="unknown"
sub_state="unknown"
main_pid="n/a"
if command -v systemctl >/dev/null 2>&1; then
  if systemctl --user is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
    daemon="running"
  fi
  active_state="$(systemctl --user show -p ActiveState --value "${SERVICE_NAME}" 2>/dev/null || echo "unknown")"
  sub_state="$(systemctl --user show -p SubState --value "${SERVICE_NAME}" 2>/dev/null || echo "unknown")"
  main_pid="$(systemctl --user show -p MainPID --value "${SERVICE_NAME}" 2>/dev/null || echo "n/a")"
fi

ready_count="$(count_ready_issues "${QUEUE_DIR}")"

mapfile -t progress_files < <(find "${IN_PROGRESS_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
mapfile -t queue_files < <(find "${QUEUE_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)

echo "## Ralph Local Runtime Status"
echo
echo "- updated_utc: $(date -u +'%Y-%m-%d %H:%M:%S UTC')"
echo "- service: ${SERVICE_NAME}"
echo "- daemon: ${daemon}"
echo "- systemd_state: ${active_state}/${sub_state}"
echo "- main_pid: ${main_pid}"
echo "- queue_ready: ${ready_count}"
echo "- in_progress: ${#progress_files[@]}"

if [ "${#progress_files[@]}" -gt 0 ]; then
  for file in "${progress_files[@]}"; do
    id="$(get_field id "${file}")"
    role="$(get_field role "${file}")"
    status="$(get_field status "${file}")"
    title="$(get_field title "${file}")"
    echo "- current_task: ${id:-unknown} | ${role:-unknown} | ${status:-unknown} | ${title:-untitled}"
  done
else
  echo "- current_task: none"
fi

next_ready=""
for file in "${queue_files[@]}"; do
  status="$(get_field status "${file}")"
  [ -n "${status}" ] || status="ready"
  if [ "${status}" = "ready" ]; then
    id="$(get_field id "${file}")"
    role="$(get_field role "${file}")"
    title="$(get_field title "${file}")"
    next_ready="${id:-unknown} | ${role:-unknown} | ${title:-untitled}"
    break
  fi
done

if [ -n "${next_ready}" ]; then
  echo "- next_ready_task: ${next_ready}"
else
  echo "- next_ready_task: none"
fi

if command -v journalctl >/dev/null 2>&1; then
  last_processing="$(journalctl --user -u "${SERVICE_NAME}" -n 120 --no-pager 2>/dev/null | grep -F '[ralph-local] processing' | tail -n 1 || true)"
  if [ -n "${last_processing}" ]; then
    echo "- last_processing_log: ${last_processing}"
  fi
fi
