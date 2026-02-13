#!/usr/bin/env bash
set -euo pipefail

# Local-only Ralph multi-agent loop runner.
# - Reads markdown issues from .ralph/issues
# - Routes by role: planner / developer / qa
# - Commits each completed work unit locally
#
# Usage:
#   scripts/ralph_local_init.sh
#   scripts/ralph_local_control.sh on
#   MAX_LOOPS=0 scripts/ralph_local_run.sh

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing command: $1" >&2
    exit 1
  fi
}

require_cmd codex
require_cmd git
require_cmd awk

CODEX_SAFETY_GUARD_CMD="${CODEX_SAFETY_GUARD_CMD:-scripts/codex_safety_guard.sh}"
if [ ! -x "${CODEX_SAFETY_GUARD_CMD}" ]; then
  echo "codex safety guard script is missing or not executable: ${CODEX_SAFETY_GUARD_CMD}" >&2
  exit 2
fi

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
QUEUE_DIR="${RALPH_ROOT}/issues"
IN_PROGRESS_DIR="${RALPH_ROOT}/in-progress"
DONE_DIR="${RALPH_ROOT}/done"
BLOCKED_DIR="${RALPH_ROOT}/blocked"
REPORTS_DIR="${RALPH_ROOT}/reports"
LOGS_DIR="${RALPH_ROOT}/logs"
STATE_FILE="${RALPH_ROOT}/state.env"
CONTEXT_FILE="${RALPH_ROOT}/context.md"
PUBLISH_STATE_FILE="${RALPH_ROOT}/state.last_publish"

MAX_LOOPS="${MAX_LOOPS:-0}" # 0 means infinite
IDLE_SLEEP_SEC="${RALPH_IDLE_SLEEP_SEC:-20}"
EXIT_ON_IDLE="${RALPH_EXIT_ON_IDLE:-false}"
NOOP_LIMIT="${RALPH_NOOP_LIMIT:-3}"
VALIDATE_CMD="${RALPH_VALIDATE_CMD:-make test && make test-sidecar && make lint}"
VALIDATE_ROLES="${RALPH_VALIDATE_ROLES:-developer,qa}"
RECOVER_IN_PROGRESS="${RALPH_RECOVER_IN_PROGRESS:-true}"
LOCAL_TRUST_MODE="${RALPH_LOCAL_TRUST_MODE:-false}"
TRANSIENT_REQUEUE_ENABLED="${RALPH_TRANSIENT_REQUEUE_ENABLED:-true}"
TRANSIENT_RETRY_SLEEP_SEC="${RALPH_TRANSIENT_RETRY_SLEEP_SEC:-20}"
AUTO_PUBLISH_ENABLED="${RALPH_AUTO_PUBLISH_ENABLED:-true}"
AUTO_PUBLISH_MIN_COMMITS="${RALPH_AUTO_PUBLISH_MIN_COMMITS:-3}"
AUTO_PUBLISH_TARGET_BRANCH="${RALPH_AUTO_PUBLISH_TARGET_BRANCH:-main}"
AUTO_PUBLISH_REMOTE="${RALPH_AUTO_PUBLISH_REMOTE:-origin}"

CODEX_SANDBOX="${AGENT_CODEX_SANDBOX:-workspace-write}"
CODEX_APPROVAL="${AGENT_CODEX_APPROVAL:-never}"
CODEX_SEARCH="${AGENT_CODEX_SEARCH:-false}"
MODEL_PLANNER="${PLANNING_CODEX_MODEL:-gpt-5.3-codex}"
MODEL_DEVELOPER_FAST="${AGENT_CODEX_MODEL_FAST:-gpt-5.3-codex-spark}"
MODEL_DEVELOPER_COMPLEX="${AGENT_CODEX_MODEL_COMPLEX:-gpt-5.3-codex}"
MODEL_QA="${QA_TRIAGE_CODEX_MODEL:-gpt-5.3-codex}"
RALPH_LAST_LOG_FILE=""

if [ "${LOCAL_TRUST_MODE}" = "true" ]; then
  CODEX_SANDBOX="${AGENT_CODEX_SANDBOX:-danger-full-access}"
  CODEX_APPROVAL="never"
fi

mkdir -p "${QUEUE_DIR}" "${IN_PROGRESS_DIR}" "${DONE_DIR}" "${BLOCKED_DIR}" "${REPORTS_DIR}" "${LOGS_DIR}"
[ -f "${STATE_FILE}" ] || printf 'RALPH_LOCAL_ENABLED=true\n' > "${STATE_FILE}"

recover_in_progress_on_boot() {
  local src id dst
  [ "${RECOVER_IN_PROGRESS}" = "true" ] || return 0
  for src in "${IN_PROGRESS_DIR}"/I-*.md; do
    [ -f "${src}" ] || continue
    id="$(basename "${src}")"
    dst="${QUEUE_DIR}/${id}"
    if [ -f "${dst}" ]; then
      dst="${QUEUE_DIR}/recovered-${id}"
    fi
    mv "${src}" "${dst}"
  done
}

recover_in_progress_on_boot

record_last_publish_sha() {
  local sha="$1"
  printf '%s\n' "${sha}" > "${PUBLISH_STATE_FILE}"
}

get_last_publish_sha() {
  if [ -f "${PUBLISH_STATE_FILE}" ]; then
    awk 'NR==1 {print; exit}' "${PUBLISH_STATE_FILE}"
    return 0
  fi
  echo ""
}

is_worktree_clean() {
  [ -z "$(git status --porcelain)" ]
}

publish_to_main_if_ready() {
  local current_branch ahead target remote publish_base pre_head
  local new_sha merge_msg

  [ "${AUTO_PUBLISH_ENABLED}" = "true" ] || return 0
  target="${AUTO_PUBLISH_TARGET_BRANCH}"
  remote="${AUTO_PUBLISH_REMOTE}"
  current_branch="$(git rev-parse --abbrev-ref HEAD)"

  is_worktree_clean || return 0

  if [ "${current_branch}" = "${target}" ]; then
    ahead="$(git rev-list --count "${remote}/${target}..${target}" 2>/dev/null || echo 0)"
    if [ "${ahead}" -lt 1 ]; then
      return 0
    fi
    if git push "${remote}" "${target}" >/dev/null 2>&1; then
      new_sha="$(git rev-parse HEAD)"
      record_last_publish_sha "${new_sha}"
      echo "[ralph-local] auto-publish: pushed ${target} (ahead=${ahead})"
    else
      echo "[ralph-local] auto-publish: push failed for ${target}; will retry later"
    fi
    return 0
  fi

  ahead="$(git rev-list --count "${target}..${current_branch}" 2>/dev/null || echo 0)"
  if [ "${ahead}" -lt "${AUTO_PUBLISH_MIN_COMMITS}" ]; then
    return 0
  fi

  publish_base="$(get_last_publish_sha)"
  if [ -n "${publish_base}" ]; then
    ahead="$(git rev-list --count "${publish_base}..${current_branch}" 2>/dev/null || echo "${ahead}")"
    if [ "${ahead}" -lt "${AUTO_PUBLISH_MIN_COMMITS}" ]; then
      return 0
    fi
  fi

  pre_head="$(git rev-parse HEAD)"
  if ! git fetch "${remote}" "${target}" >/dev/null 2>&1; then
    echo "[ralph-local] auto-publish: fetch failed (${remote}/${target}); will retry later"
    return 0
  fi

  if ! git checkout "${target}" >/dev/null 2>&1; then
    echo "[ralph-local] auto-publish: cannot checkout ${target}; will retry later"
    git checkout "${current_branch}" >/dev/null 2>&1 || true
    return 0
  fi

  if ! git pull --ff-only "${remote}" "${target}" >/dev/null 2>&1; then
    echo "[ralph-local] auto-publish: ff-only pull failed on ${target}; aborting publish cycle"
    git checkout "${current_branch}" >/dev/null 2>&1 || true
    return 0
  fi

  merge_msg="ralph(publish): merge ${current_branch} into ${target}"
  if ! git merge --no-ff -m "${merge_msg}" "${current_branch}" >/dev/null 2>&1; then
    echo "[ralph-local] auto-publish: merge conflict (${current_branch} -> ${target}); aborting publish cycle"
    git merge --abort >/dev/null 2>&1 || true
    git checkout "${current_branch}" >/dev/null 2>&1 || true
    return 0
  fi

  if git push "${remote}" "${target}" >/dev/null 2>&1; then
    new_sha="$(git rev-parse HEAD)"
    record_last_publish_sha "${new_sha}"
    echo "[ralph-local] auto-publish: pushed ${target} from ${current_branch} (commits=${ahead})"
  else
    echo "[ralph-local] auto-publish: push failed after merge; keeping local merge commit on ${target}"
  fi

  git checkout "${current_branch}" >/dev/null 2>&1 || true
  if ! git merge --ff-only "${target}" >/dev/null 2>&1; then
    echo "[ralph-local] auto-publish: could not fast-forward ${current_branch} from ${target}"
    git checkout "${pre_head}" >/dev/null 2>&1 || true
    git checkout "${current_branch}" >/dev/null 2>&1 || true
  fi
}

supports_codex_search() {
  codex --help 2>/dev/null | grep -q -- "--search"
}

meta_value() {
  local file="$1"
  local key="$2"
  awk -F': ' -v key="${key}" '
    $1 == key {
      sub($1": ", "");
      print;
      exit
    }
  ' "${file}"
}

now_utc() {
  date -u +'%Y-%m-%dT%H:%M:%SZ'
}

local_enabled() {
  local v
  v="$(awk -F= '/^RALPH_LOCAL_ENABLED=/{print $2; exit}' "${STATE_FILE}" 2>/dev/null || true)"
  [ "${v}" = "true" ]
}

csv_contains() {
  local csv="$1"
  local needle="$2"
  printf '%s\n' "${csv}" | tr ',' '\n' | awk '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); if ($0 != "") print}' | grep -Fxq "${needle}"
}

dependency_satisfied() {
  local dep="$1"
  [ -z "${dep}" ] && return 0
  [ -f "${DONE_DIR}/${dep}.md" ]
}

all_dependencies_satisfied() {
  local deps_csv="$1"
  local dep
  [ -z "${deps_csv}" ] && return 0
  while IFS= read -r dep; do
    dep="$(awk '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); print}' <<<"${dep}")"
    [ -n "${dep}" ] || continue
    if ! dependency_satisfied "${dep}"; then
      return 1
    fi
  done < <(printf '%s\n' "${deps_csv}" | tr ',' '\n')
  return 0
}

pick_next_issue() {
  local file status deps
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    status="$(meta_value "${file}" "status")"
    [ -n "${status}" ] || status="ready"
    [ "${status}" = "ready" ] || continue
    deps="$(meta_value "${file}" "depends_on")"
    if ! all_dependencies_satisfied "${deps}"; then
      continue
    fi
    echo "${file}"
    return 0
  done < <(find "${QUEUE_DIR}" -maxdepth 1 -type f -name 'I-*.md' | sort)
  return 1
}

role_guide() {
  local role="$1"
  case "${role}" in
    planner)
      cat <<'EOF'
Role: Planner
- Update implementation/spec docs for executable scope.
- If work should be split, create additional local issues under `.ralph/issues/` using the existing markdown format.
- Keep milestones explicit and measurable.
EOF
      ;;
    qa)
      cat <<'EOF'
Role: QA
- Validate current implementation against acceptance criteria.
- Prefer deterministic checks and concrete failure evidence.
- Write QA report markdown to `.ralph/reports/`.
- If defects are found, create follow-up developer issues under `.ralph/issues/`.
EOF
      ;;
    *)
      cat <<'EOF'
Role: Developer
- Implement the requested change with production-minded quality.
- Keep diffs small and testable.
- Update tests/docs as required by acceptance criteria.
EOF
      ;;
  esac
}

choose_model() {
  local role="$1"
  local complexity="$2"
  case "${role}" in
    planner) echo "${MODEL_PLANNER}" ;;
    qa) echo "${MODEL_QA}" ;;
    developer)
      case "${complexity}" in
        high|critical) echo "${MODEL_DEVELOPER_COMPLEX}" ;;
        *) echo "${MODEL_DEVELOPER_FAST}" ;;
      esac
      ;;
    *) echo "${MODEL_DEVELOPER_FAST}" ;;
  esac
}

append_result_block() {
  local file="$1"
  local state="$2"
  local role="$3"
  local model="$4"
  local log_file="$5"
  local validation_state="$6"
  local note="$7"
  local commit_sha="$8"
  cat >>"${file}" <<EOF

## Ralph Result
- state: ${state}
- role: ${role}
- model: ${model}
- completed_at_utc: $(now_utc)
- log_file: ${log_file}
- validation: ${validation_state}
- commit: ${commit_sha}
- note: ${note}
EOF
}

is_transient_model_error() {
  local log_file="$1"
  [ -f "${log_file}" ] || return 1
  rg -q -i \
    "stream disconnected before completion|error sending request for url|reconnecting\\.\\.\\.|timed out|timeout|temporar|connection reset|connection refused|service unavailable|too many requests|rate limit" \
    "${log_file}"
}

run_codex_for_issue() {
  local issue_file="$1"
  local role="$2"
  local model="$3"
  local prompt log_file rc

  log_file="${LOGS_DIR}/$(basename "${issue_file}" .md)-$(date -u +%Y%m%dT%H%M%SZ).log"
  prompt="$(cat <<EOF
You are executing a local Ralph loop task with no GitHub dependency.

Workspace policy:
- Use local markdown queue under \`.ralph/\`.
- Finish one issue completely before moving to the next.
- If the task should be split, create additional issue markdown files in \`.ralph/issues/\`.
- Keep changes production-safe and verifiable.

$(role_guide "${role}")

Shared context file (${CONTEXT_FILE}):
$(cat "${CONTEXT_FILE}" 2>/dev/null || echo "(no context file)")

Task file (${issue_file}):
$(cat "${issue_file}")
EOF
)"

  cmd=(
    codex
    --ask-for-approval "${CODEX_APPROVAL}"
  )
  if [ "${CODEX_SEARCH}" = "true" ] && supports_codex_search; then
    cmd+=(--search)
  fi
  cmd+=(
    exec
    --model "${model}"
    --sandbox "${CODEX_SANDBOX}"
    --cd "$(pwd)"
  )

  if [ "${LOCAL_TRUST_MODE}" = "true" ]; then
    OMX_SAFE_MODE=false "${CODEX_SAFETY_GUARD_CMD}" "${cmd[@]}"
  else
    "${CODEX_SAFETY_GUARD_CMD}" "${cmd[@]}"
  fi
  set +e
  "${cmd[@]}" "${prompt}" >"${log_file}" 2>&1
  rc=$?
  set -e
  RALPH_LAST_LOG_FILE="${log_file}"
  return "${rc}"
}

run_validation_if_needed() {
  local role="$1"
  local issue_id="$2"
  local validation_log="${LOGS_DIR}/${issue_id}-validation.log"
  local rc=0

  if ! csv_contains "${VALIDATE_ROLES}" "${role}"; then
    echo "skipped:${validation_log}"
    return 0
  fi

  set +e
  bash -lc "${VALIDATE_CMD}" >"${validation_log}" 2>&1
  rc=$?
  set -e

  if [ "${rc}" -eq 0 ]; then
    echo "passed:${validation_log}"
    return 0
  fi

  echo "failed:${validation_log}"
  return 1
}

loop_count=0
noop_count=0

while true; do
  if ! local_enabled; then
    echo "[ralph-local] disabled. stopping."
    break
  fi

  if [ "${MAX_LOOPS}" -gt 0 ] && [ "${loop_count}" -ge "${MAX_LOOPS}" ]; then
    echo "[ralph-local] max loops reached (${MAX_LOOPS})"
    break
  fi

  next_issue="$(pick_next_issue || true)"
  if [ -z "${next_issue}" ]; then
    if [ "${EXIT_ON_IDLE}" = "true" ]; then
      echo "[ralph-local] no ready issues; exiting due to RALPH_EXIT_ON_IDLE=true"
      break
    fi
    echo "[ralph-local] no ready issues; sleeping ${IDLE_SLEEP_SEC}s"
    sleep "${IDLE_SLEEP_SEC}"
    continue
  fi

  issue_id="$(meta_value "${next_issue}" "id")"
  [ -n "${issue_id}" ] || issue_id="$(basename "${next_issue}" .md)"
  issue_title="$(meta_value "${next_issue}" "title")"
  role="$(meta_value "${next_issue}" "role")"
  [ -n "${role}" ] || role="developer"
  complexity="$(meta_value "${next_issue}" "complexity")"
  [ -n "${complexity}" ] || complexity="medium"
  model="$(choose_model "${role}" "${complexity}")"

  in_progress_path="${IN_PROGRESS_DIR}/${issue_id}.md"
  mv "${next_issue}" "${in_progress_path}"
  echo "[ralph-local] processing ${issue_id} role=${role} model=${model}"

  if run_codex_for_issue "${in_progress_path}" "${role}" "${model}"; then
    codex_rc=0
  else
    codex_rc=$?
  fi
  log_file="${RALPH_LAST_LOG_FILE:-}"
  if [ "${codex_rc}" -ne 0 ]; then
    if [ ! -f "${in_progress_path}" ]; then
      echo "[ralph-local] warning: missing in-progress file for ${issue_id}; skipping recovery path"
      loop_count=$((loop_count + 1))
      continue
    fi

    if [ "${TRANSIENT_REQUEUE_ENABLED}" = "true" ] && is_transient_model_error "${log_file}"; then
      requeue_path="${QUEUE_DIR}/${issue_id}.md"
      if [ -f "${requeue_path}" ]; then
        requeue_path="${QUEUE_DIR}/retry-${issue_id}-$(date -u +%Y%m%dT%H%M%SZ).md"
      fi
      mv "${in_progress_path}" "${requeue_path}"
      echo "[ralph-local] transient model error on ${issue_id}; requeued and sleeping ${TRANSIENT_RETRY_SLEEP_SEC}s"
      sleep "${TRANSIENT_RETRY_SLEEP_SEC}"
      loop_count=$((loop_count + 1))
      continue
    fi

    blocked_path="${BLOCKED_DIR}/${issue_id}.md"
    mv "${in_progress_path}" "${blocked_path}"
    append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "not-run" "codex exited with code ${codex_rc}" ""
    echo "[ralph-local] blocked ${issue_id}: codex failure"
    loop_count=$((loop_count + 1))
    continue
  fi

  validation_state="$(run_validation_if_needed "${role}" "${issue_id}" || true)"
  if [[ "${validation_state}" == failed:* ]]; then
    blocked_path="${BLOCKED_DIR}/${issue_id}.md"
    mv "${in_progress_path}" "${blocked_path}"
    append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "${validation_state}" "validation failed" ""
    echo "[ralph-local] blocked ${issue_id}: validation failed"
    loop_count=$((loop_count + 1))
    continue
  fi

  commit_sha=""
  if [ -n "$(git status --porcelain)" ]; then
    safe_title="$(printf '%s' "${issue_title}" | tr '\n' ' ' | sed -E 's/[[:space:]]+/ /g')"
    git add -A
    git commit -m "ralph(local): ${issue_id} ${safe_title}" >/dev/null || true
    commit_sha="$(git rev-parse --short HEAD 2>/dev/null || true)"
    publish_to_main_if_ready || true
    noop_count=0
  else
    noop_count=$((noop_count + 1))
  fi

  done_path="${DONE_DIR}/${issue_id}.md"
  if [ ! -f "${in_progress_path}" ]; then
    echo "[ralph-local] warning: missing in-progress file before completion for ${issue_id}; skipping completion move"
    loop_count=$((loop_count + 1))
    continue
  fi
  mv "${in_progress_path}" "${done_path}"
  append_result_block "${done_path}" "done" "${role}" "${model}" "${log_file}" "${validation_state}" "completed" "${commit_sha}"
  echo "[ralph-local] completed ${issue_id} commit=${commit_sha:-none}"

  loop_count=$((loop_count + 1))
  if [ "${noop_count}" -ge "${NOOP_LIMIT}" ]; then
    echo "[ralph-local] repeated no-op completions reached limit (${NOOP_LIMIT}), stopping"
    break
  fi
done
