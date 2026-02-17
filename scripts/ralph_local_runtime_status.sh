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
PID_FILE="${RALPH_ROOT}/runner.pid"
SUPERVISOR_SCRIPT="${RALPH_SUPERVISOR_SCRIPT:-${ROOT_DIR}/scripts/ralph_local_supervisor.sh}"

read_issue_meta() {
  local file="$1"
  awk -F': ' '
    BEGIN { id=""; role=""; status=""; title="" }
    $1=="id" && id=="" { id=$2; next }
    $1=="role" && role=="" { role=$2; next }
    $1=="status" && status=="" { status=$2; next }
    $1=="title" && title=="" { title=$2; next }
    END {
      if (status == "") status = "ready"
      if (role == "") role = "developer"
      print id "\t" role "\t" status "\t" title
    }
  ' "${file}" 2>/dev/null
}

read_pid() {
  [ -f "${PID_FILE}" ] || return 1
  awk 'NR==1{print $1; exit}' "${PID_FILE}" 2>/dev/null
}

is_pid_running() {
  local pid="$1"
  [ -n "${pid}" ] || return 1
  ps -p "${pid}" >/dev/null 2>&1
}

detect_supervisor_pid() {
  pgrep -f "${SUPERVISOR_SCRIPT}" | head -n1 || true
}

daemon="stopped"
active_state="unknown"
sub_state="unknown"
main_pid="n/a"
control_mode="local"
if command -v systemctl >/dev/null 2>&1; then
  control_mode="systemd"
  if systemctl --user is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
    daemon="running"
  fi
  active_state="$(systemctl --user show -p ActiveState --value "${SERVICE_NAME}" 2>/dev/null || echo "unknown")"
  sub_state="$(systemctl --user show -p SubState --value "${SERVICE_NAME}" 2>/dev/null || echo "unknown")"
  main_pid="$(systemctl --user show -p MainPID --value "${SERVICE_NAME}" 2>/dev/null || echo "n/a")"
fi

mapfile -t progress_files < <(find "${IN_PROGRESS_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
mapfile -t queue_files < <(find "${QUEUE_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)

if [ "${daemon}" != "running" ]; then
  local_pid="$(read_pid || true)"
  if is_pid_running "${local_pid}"; then
    daemon="running"
    main_pid="${local_pid}"
    active_state="active"
    sub_state="running(local)"
    control_mode="local-pid"
  else
    supervisor_pid="$(detect_supervisor_pid)"
    if is_pid_running "${supervisor_pid}"; then
      daemon="running"
      main_pid="${supervisor_pid}"
      active_state="active"
      sub_state="running(process)"
      control_mode="process-fallback"
    fi
  fi
fi

ready_count=0
next_ready=""
for file in "${queue_files[@]}"; do
  [ -f "${file}" ] || continue
  IFS=$'\t' read -r id role status title < <(read_issue_meta "${file}")
  if [ "${status}" = "ready" ]; then
    ready_count=$((ready_count + 1))
    if [ -z "${next_ready}" ]; then
      next_ready="${id:-unknown} | ${role:-unknown} | ${title:-untitled}"
    fi
  fi
done

echo "## Ralph Local Runtime Status"
echo
echo "- updated_utc: $(date -u +'%Y-%m-%d %H:%M:%S UTC')"
echo "- service: ${SERVICE_NAME}"
echo "- daemon: ${daemon}"
echo "- control_mode: ${control_mode}"
echo "- systemd_state: ${active_state}/${sub_state}"
echo "- main_pid: ${main_pid}"
echo "- queue_ready: ${ready_count}"
echo "- in_progress: ${#progress_files[@]}"

if [ "${#progress_files[@]}" -gt 0 ]; then
  for file in "${progress_files[@]}"; do
    [ -f "${file}" ] || continue
    IFS=$'\t' read -r id role status title < <(read_issue_meta "${file}")
    status="in-progress"
    echo "- current_task: ${id:-unknown} | ${role:-unknown} | ${status} | ${title:-untitled}"
  done
else
  echo "- current_task: none"
fi

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
