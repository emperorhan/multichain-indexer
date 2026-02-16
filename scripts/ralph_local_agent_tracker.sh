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

roles=("planner" "developer" "qa")

read_issue_meta() {
  local file="$1"
  awk -F': ' '
    BEGIN { id=""; role=""; status=""; title="" }
    $1=="id" && id=="" { id=$2; next }
    $1=="role" && role=="" { role=$2; next }
    $1=="status" && status=="" { status=$2; next }
    $1=="title" && title=="" { title=$2; next }
    END {
      if (role == "") role = "developer"
      if (status == "") status = "ready"
      print id "\t" role "\t" status "\t" title
    }
  ' "${file}" 2>/dev/null
}

is_tracked_role() {
  local role="$1"
  local candidate
  for candidate in "${roles[@]}"; do
    if [ "${candidate}" = "${role}" ]; then
      return 0
    fi
  done
  return 1
}

issue_brief() {
  local id="$1"
  local role="$2"
  local status="$3"
  local title="$4"
  echo "${id:-unknown} | ${role:-unknown} | ${status:-unknown} | ${title:-untitled}"
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
  if command -v rg >/dev/null 2>&1; then
    {
      rg -l "automanager_key:[[:space:]]*auto-quality-cycle-" \
        "${ISSUES_DIR}" "${IN_PROGRESS_DIR}" "${BLOCKED_DIR}" 2>/dev/null || true
    } | wc -l | awk '{print $1}'
    return 0
  fi
  {
    grep -R -E -l "automanager_key:[[:space:]]*auto-quality-cycle-" \
      "${ISSUES_DIR}" "${IN_PROGRESS_DIR}" "${BLOCKED_DIR}" 2>/dev/null || true
  } | wc -l | awk '{print $1}'
}

declare -A ready_count in_progress_count current_brief next_brief last_done_brief last_done_path
for role in "${roles[@]}"; do
  ready_count["${role}"]=0
  in_progress_count["${role}"]=0
  current_brief["${role}"]=""
  next_brief["${role}"]=""
  last_done_brief["${role}"]=""
  last_done_path["${role}"]=""
done

mapfile -t in_progress_files < <(find "${IN_PROGRESS_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
for file in "${in_progress_files[@]}"; do
  [ -f "${file}" ] || continue
  IFS=$'\t' read -r id role status title < <(read_issue_meta "${file}")
  if ! is_tracked_role "${role}"; then
    continue
  fi
  in_progress_count["${role}"]=$((in_progress_count["${role}"] + 1))
  if [ -z "${current_brief[${role}]}" ]; then
    current_brief["${role}"]="$(issue_brief "${id}" "${role}" "in-progress" "${title}")"
  fi
done

mapfile -t queue_files < <(find "${ISSUES_DIR}" -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
for file in "${queue_files[@]}"; do
  [ -f "${file}" ] || continue
  IFS=$'\t' read -r id role status title < <(read_issue_meta "${file}")
  if ! is_tracked_role "${role}"; then
    continue
  fi
  if [ "${status}" != "ready" ]; then
    continue
  fi
  ready_count["${role}"]=$((ready_count["${role}"] + 1))
  if [ -z "${next_brief[${role}]}" ]; then
    next_brief["${role}"]="$(issue_brief "${id}" "${role}" "${status}" "${title}")"
  fi
done

while IFS='|' read -r _mtime file; do
  [ -f "${file}" ] || continue
  IFS=$'\t' read -r id role status title < <(read_issue_meta "${file}")
  if ! is_tracked_role "${role}"; then
    continue
  fi
  if [ -n "${last_done_path[${role}]}" ]; then
    continue
  fi
  last_done_path["${role}"]="${file}"
  last_done_brief["${role}"]="$(issue_brief "${id}" "${role}" "${status}" "${title}")"
done < <(find "${DONE_DIR}" -maxdepth 1 -type f -name 'I-*.md' -printf '%T@|%p\n' 2>/dev/null | sort -t'|' -k1,1nr)

processing_logs=""
if command -v journalctl >/dev/null 2>&1; then
  processing_logs="$(journalctl --user -u "${SERVICE_NAME}" -n 300 --no-pager 2>/dev/null | grep -F '[ralph-local] processing' || true)"
fi

last_processing_log_for_role() {
  local role="$1"
  if [ -z "${processing_logs}" ]; then
    return 0
  fi
  awk -v role="${role}" '
    index($0, "role=" role) { last=$0 }
    END { if (last != "") print last }
  ' <<<"${processing_logs}"
}

print_role_section() {
  local role="$1"
  local last_processing completed_at

  echo
  echo "### ${role}"
  echo "- in_progress_count: ${in_progress_count[${role}]:-0}"
  echo "- ready_queue_count: ${ready_count[${role}]:-0}"

  if [ -n "${current_brief[${role}]}" ]; then
    echo "- current_task: ${current_brief[${role}]}"
  else
    echo "- current_task: none"
  fi

  if [ -n "${next_brief[${role}]}" ]; then
    echo "- next_ready_task: ${next_brief[${role}]}"
  else
    echo "- next_ready_task: none"
  fi

  if [ -n "${last_done_path[${role}]}" ]; then
    completed_at="$(get_done_completed_at "${last_done_path[${role}]}")"
    echo "- last_completed_task: ${last_done_brief[${role}]}"
    echo "- last_completed_at: ${completed_at:-unknown}"
  else
    echo "- last_completed_task: none"
  fi

  last_processing="$(last_processing_log_for_role "${role}" || true)"
  if [ -n "${last_processing}" ]; then
    echo "- last_processing_log: ${last_processing}"
  fi
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
