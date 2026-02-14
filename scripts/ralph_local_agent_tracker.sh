#!/usr/bin/env bash
set -euo pipefail

# Agent-centric tracker for local Ralph loop.
# Shows per-role current task, queue backlog, and latest activity.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

SERVICE_NAME="${RALPH_LOCAL_SERVICE_NAME:-ralph-local.service}"
RALPH_ROOT="${RALPH_ROOT:-.ralph}"
ISSUES_DIR="${RALPH_ROOT}/issues"
IN_PROGRESS_DIR="${RALPH_ROOT}/in-progress"
DONE_DIR="${RALPH_ROOT}/done"
BLOCKED_DIR="${RALPH_ROOT}/blocked"
LOGS_DIR="${RALPH_ROOT}/logs"
AUTOMANAGER_STATE_FILE="${RALPH_ROOT}/state.automanager_last_run"

get_meta_value() {
  local key="$1"
  local file="$2"
  awk -F': ' -v key="${key}" '$1 == key { print $2; exit }' "${file}" 2>/dev/null || true
}

get_issue_status() {
  local file="$1"
  local status
  status="$(get_meta_value "status" "${file}")"
  [ -n "${status}" ] || status="ready"
  echo "${status}"
}

format_issue_brief() {
  local file="$1"
  local id role status title
  id="$(get_meta_value "id" "${file}")"
  role="$(get_meta_value "role" "${file}")"
  status="$(get_issue_status "${file}")"
  title="$(get_meta_value "title" "${file}")"
  echo "${id:-unknown} | ${role:-unknown} | ${status} | ${title:-untitled}"
}

find_current_in_progress_for_role() {
  local role="$1"
  local file file_role
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    file_role="$(get_meta_value "role" "${file}")"
    [ -n "${file_role}" ] || file_role="developer"
    if [ "${file_role}" = "${role}" ]; then
      echo "${file}"
      return 0
    fi
  done < <(find "${IN_PROGRESS_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
  return 1
}

find_next_ready_for_role() {
  local role="$1"
  local file file_role status
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    status="$(get_issue_status "${file}")"
    [ "${status}" = "ready" ] || continue
    file_role="$(get_meta_value "role" "${file}")"
    [ -n "${file_role}" ] || file_role="developer"
    if [ "${file_role}" = "${role}" ]; then
      echo "${file}"
      return 0
    fi
  done < <(find "${ISSUES_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
  return 1
}

count_ready_for_role() {
  local role="$1"
  local file file_role status count
  count=0
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    status="$(get_issue_status "${file}")"
    [ "${status}" = "ready" ] || continue
    file_role="$(get_meta_value "role" "${file}")"
    [ -n "${file_role}" ] || file_role="developer"
    if [ "${file_role}" = "${role}" ]; then
      count=$((count + 1))
    fi
  done < <(find "${ISSUES_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
  echo "${count}"
}

count_in_progress_for_role() {
  local role="$1"
  local file file_role count
  count=0
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    file_role="$(get_meta_value "role" "${file}")"
    [ -n "${file_role}" ] || file_role="developer"
    if [ "${file_role}" = "${role}" ]; then
      count=$((count + 1))
    fi
  done < <(find "${IN_PROGRESS_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
  echo "${count}"
}

get_done_completed_at() {
  local file="$1"
  local completed_at
  completed_at="$(awk '/^- completed_at_utc:[[:space:]]*/ { sub("^- completed_at_utc:[[:space:]]*", ""); print; exit }' "${file}" 2>/dev/null || true)"
  if [ -n "${completed_at}" ]; then
    echo "${completed_at}"
    return 0
  fi
  stat -c '%y' "${file}" 2>/dev/null | awk '{print $1" "$2" "$3}' || true
}

find_last_done_for_role() {
  local role="$1"
  local file file_role
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    file_role="$(get_meta_value "role" "${file}")"
    [ -n "${file_role}" ] || file_role="developer"
    if [ "${file_role}" = "${role}" ]; then
      echo "${file}"
      return 0
    fi
  done < <(find "${DONE_DIR}" -maxdepth 1 -type f -name 'I-*.md' -printf '%T@|%p\n' 2>/dev/null | sort -t'|' -k1,1nr | awk -F'|' '{print $2}')
  return 1
}

last_processing_log_for_role() {
  local role="$1"
  if ! command -v journalctl >/dev/null 2>&1; then
    return 1
  fi
  journalctl --user -u "${SERVICE_NAME}" -n 300 --no-pager 2>/dev/null \
    | grep -F '[ralph-local] processing' \
    | grep -F "role=${role}" \
    | tail -n 1 || true
}

print_role_section() {
  local role="$1"
  local ready_count in_progress_count
  local current_file next_file last_done_file
  local last_processing completed_at

  ready_count="$(count_ready_for_role "${role}")"
  in_progress_count="$(count_in_progress_for_role "${role}")"

  echo
  echo "### ${role}"
  echo "- in_progress_count: ${in_progress_count}"
  echo "- ready_queue_count: ${ready_count}"

  current_file="$(find_current_in_progress_for_role "${role}" || true)"
  if [ -n "${current_file}" ]; then
    echo "- current_task: $(format_issue_brief "${current_file}")"
  else
    echo "- current_task: none"
  fi

  next_file="$(find_next_ready_for_role "${role}" || true)"
  if [ -n "${next_file}" ]; then
    echo "- next_ready_task: $(format_issue_brief "${next_file}")"
  else
    echo "- next_ready_task: none"
  fi

  last_done_file="$(find_last_done_for_role "${role}" || true)"
  if [ -n "${last_done_file}" ]; then
    completed_at="$(get_done_completed_at "${last_done_file}")"
    echo "- last_completed_task: $(format_issue_brief "${last_done_file}")"
    echo "- last_completed_at: ${completed_at:-unknown}"
  else
    echo "- last_completed_task: none"
  fi

  last_processing="$(last_processing_log_for_role "${role}" || true)"
  if [ -n "${last_processing}" ]; then
    echo "- last_processing_log: ${last_processing}"
  fi
}

format_epoch_utc() {
  local epoch="$1"
  if ! [[ "${epoch}" =~ ^[0-9]+$ ]]; then
    echo "unknown"
    return 0
  fi
  date -u -d "@${epoch}" +'%Y-%m-%d %H:%M:%S UTC' 2>/dev/null || echo "${epoch}"
}

automanager_last_run_epoch() {
  awk 'NR==1 { if ($1 ~ /^[0-9]+$/) print $1; else print 0; exit }' "${AUTOMANAGER_STATE_FILE}" 2>/dev/null || echo 0
}

automanager_last_log_file() {
  ls -1t "${LOGS_DIR}"/automanager-*.log 2>/dev/null | head -n 1 || true
}

count_open_cycle_issues() {
  grep -R -E -l "automanager_key:[[:space:]]*auto-quality-cycle-" \
    "${ISSUES_DIR}" "${IN_PROGRESS_DIR}" "${BLOCKED_DIR}" 2>/dev/null | wc -l | awk '{print $1}'
}

service_active="unknown"
service_sub="unknown"
if command -v systemctl >/dev/null 2>&1; then
  service_active="$(systemctl --user show -p ActiveState --value "${SERVICE_NAME}" 2>/dev/null || echo "unknown")"
  service_sub="$(systemctl --user show -p SubState --value "${SERVICE_NAME}" 2>/dev/null || echo "unknown")"
fi

echo "## Ralph Agent Tracker"
echo
echo "- updated_utc: $(date -u +'%Y-%m-%d %H:%M:%S UTC')"
echo "- service: ${SERVICE_NAME}"
echo "- service_state: ${service_active}/${service_sub}"

print_role_section "planner"
print_role_section "developer"
print_role_section "qa"

echo
echo "### manager"
echo "- open_quality_cycles: $(count_open_cycle_issues)"
manager_epoch="$(automanager_last_run_epoch)"
echo "- last_automanager_run_utc: $(format_epoch_utc "${manager_epoch}")"
manager_log="$(automanager_last_log_file)"
if [ -n "${manager_log}" ]; then
  echo "- last_automanager_log: ${manager_log}"
  echo "- last_automanager_result: $(tail -n 1 "${manager_log}" 2>/dev/null || true)"
else
  echo "- last_automanager_log: none"
fi
