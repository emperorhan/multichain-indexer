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
require_cmd flock

CODEX_SAFETY_GUARD_CMD="${CODEX_SAFETY_GUARD_CMD:-scripts/codex_safety_guard.sh}"
if [ ! -x "${CODEX_SAFETY_GUARD_CMD}" ]; then
  echo "codex safety guard script is missing or not executable: ${CODEX_SAFETY_GUARD_CMD}" >&2
  exit 2
fi
CODEX_AUTH_STATUS_CMD="${CODEX_AUTH_STATUS_CMD:-scripts/codex_auth_status.sh}"
if [ ! -x "${CODEX_AUTH_STATUS_CMD}" ]; then
  echo "codex auth status script is missing or not executable: ${CODEX_AUTH_STATUS_CMD}" >&2
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
RUN_LOCK_FILE="${RALPH_ROOT}/run.lock"
TRANSIENT_STATE_FILE="${RALPH_ROOT}/state.transient_failures"
BLOCKED_RETRY_STATE_FILE="${RALPH_ROOT}/state.blocked_retries"
AUTOMANAGER_STATE_FILE="${RALPH_ROOT}/state.automanager_last_run"

MAX_LOOPS="${MAX_LOOPS:-0}" # 0 means infinite
IDLE_SLEEP_SEC="${RALPH_IDLE_SLEEP_SEC:-20}"
EXIT_ON_IDLE="${RALPH_EXIT_ON_IDLE:-false}"
NOOP_LIMIT="${RALPH_NOOP_LIMIT:-3}"
VALIDATE_CMD="${RALPH_VALIDATE_CMD:-make test && make test-sidecar && make lint}"
VALIDATE_ROLES="${RALPH_VALIDATE_ROLES:-developer,qa}"
RECOVER_IN_PROGRESS="${RALPH_RECOVER_IN_PROGRESS:-true}"
LOCAL_TRUST_MODE="${RALPH_LOCAL_TRUST_MODE:-false}"
REQUIRE_CHATGPT_AUTH="${RALPH_REQUIRE_CHATGPT_AUTH:-true}"
STRIP_API_ENV="${RALPH_STRIP_API_ENV:-true}"
TRANSIENT_REQUEUE_ENABLED="${RALPH_TRANSIENT_REQUEUE_ENABLED:-true}"
TRANSIENT_RETRY_SLEEP_SEC="${RALPH_TRANSIENT_RETRY_SLEEP_SEC:-20}"
TRANSIENT_BACKOFF_MAX_SEC="${RALPH_TRANSIENT_BACKOFF_MAX_SEC:-300}"
TRANSIENT_HEALTHCHECK_AFTER_FAILS="${RALPH_TRANSIENT_HEALTHCHECK_AFTER_FAILS:-5}"
TRANSIENT_HEALTHCHECK_TIMEOUT_SEC="${RALPH_TRANSIENT_HEALTHCHECK_TIMEOUT_SEC:-20}"
BLOCKED_REQUEUE_ENABLED="${RALPH_BLOCKED_REQUEUE_ENABLED:-true}"
BLOCKED_REQUEUE_MAX_ATTEMPTS="${RALPH_BLOCKED_REQUEUE_MAX_ATTEMPTS:-3}"
BLOCKED_REQUEUE_COOLDOWN_SEC="${RALPH_BLOCKED_REQUEUE_COOLDOWN_SEC:-300}"
AUTOMANAGER_ENABLED="${RALPH_AUTOMANAGER_ENABLED:-true}"
AUTOMANAGER_CMD="${RALPH_AUTOMANAGER_CMD:-scripts/ralph_local_manager_autofill.sh}"
AUTOMANAGER_COOLDOWN_SEC="${RALPH_AUTOMANAGER_COOLDOWN_SEC:-60}"
SELF_HEAL_ENABLED="${RALPH_SELF_HEAL_ENABLED:-true}"
SELF_HEAL_MAX_ATTEMPTS="${RALPH_SELF_HEAL_MAX_ATTEMPTS:-3}"
SELF_HEAL_LOG_TAIL_LINES="${RALPH_SELF_HEAL_LOG_TAIL_LINES:-220}"
SELF_HEAL_RETRY_SLEEP_SEC="${RALPH_SELF_HEAL_RETRY_SLEEP_SEC:-5}"
AUTO_PUBLISH_ENABLED="${RALPH_AUTO_PUBLISH_ENABLED:-true}"
AUTO_PUBLISH_MIN_COMMITS="${RALPH_AUTO_PUBLISH_MIN_COMMITS:-3}"
AUTO_PUBLISH_TARGET_BRANCH="${RALPH_AUTO_PUBLISH_TARGET_BRANCH:-main}"
AUTO_PUBLISH_REMOTE="${RALPH_AUTO_PUBLISH_REMOTE:-origin}"
BRANCH_STRATEGY="${RALPH_BRANCH_STRATEGY:-main}"

CODEX_SANDBOX="${AGENT_CODEX_SANDBOX:-workspace-write}"
CODEX_APPROVAL="${AGENT_CODEX_APPROVAL:-never}"
CODEX_SEARCH="${AGENT_CODEX_SEARCH:-false}"
MODEL_PLANNER="${PLANNING_CODEX_MODEL:-gpt-5.3-codex}"
MODEL_DEVELOPER_FAST="${AGENT_CODEX_MODEL_FAST:-gpt-5.3-codex-spark}"
MODEL_DEVELOPER_COMPLEX="${AGENT_CODEX_MODEL_COMPLEX:-gpt-5.3-codex}"
MODEL_QA="${QA_TRIAGE_CODEX_MODEL:-gpt-5.3-codex}"
SELF_HEAL_MODEL="${RALPH_SELF_HEAL_MODEL:-gpt-5.3-codex}"
RALPH_LAST_LOG_FILE=""
SELF_HEAL_RESULT="not-run"
SELF_HEAL_LAST_ATTEMPTS=0
SELF_HEAL_LAST_CODEX_RC=0
SELF_HEAL_FINAL_VALIDATION_STATE=""

if [ "${LOCAL_TRUST_MODE}" = "true" ]; then
  CODEX_SANDBOX="${AGENT_CODEX_SANDBOX:-danger-full-access}"
  CODEX_APPROVAL="never"
fi

mkdir -p "${QUEUE_DIR}" "${IN_PROGRESS_DIR}" "${DONE_DIR}" "${BLOCKED_DIR}" "${REPORTS_DIR}" "${LOGS_DIR}"
[ -f "${STATE_FILE}" ] || printf 'RALPH_LOCAL_ENABLED=true\n' > "${STATE_FILE}"
[ -f "${TRANSIENT_STATE_FILE}" ] || printf '0\n' > "${TRANSIENT_STATE_FILE}"
[ -f "${BLOCKED_RETRY_STATE_FILE}" ] || : > "${BLOCKED_RETRY_STATE_FILE}"
[ -f "${AUTOMANAGER_STATE_FILE}" ] || printf '0\n' > "${AUTOMANAGER_STATE_FILE}"

exec 9>"${RUN_LOCK_FILE}"
if ! flock -n 9; then
  echo "[ralph-local] another runner instance is active; exiting with lock-busy"
  exit 75
fi

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

strip_api_auth_env() {
  [ "${STRIP_API_ENV}" = "true" ] || return 0
  unset OPENAI_API_KEY OPENAI_BASE_URL OPENAI_API_BASE OPENAI_ORGANIZATION OPENAI_ORG_ID
}

enforce_chatgpt_auth_mode() {
  [ "${REQUIRE_CHATGPT_AUTH}" = "true" ] || return 0
  if ! "${CODEX_AUTH_STATUS_CMD}" --require-chatgpt >/dev/null; then
    echo "[ralph-local] auth preflight failed (ChatGPT Pro mode required)."
    return 1
  fi
}

strip_api_auth_env
enforce_chatgpt_auth_mode

get_transient_failures() {
  awk 'NR==1 { if ($1 ~ /^[0-9]+$/) print $1; else print 0; exit }' "${TRANSIENT_STATE_FILE}" 2>/dev/null || echo 0
}

set_transient_failures() {
  local count="$1"
  printf '%s\n' "${count}" > "${TRANSIENT_STATE_FILE}"
}

reset_transient_failures() {
  set_transient_failures 0
}

increment_transient_failures() {
  local current next
  current="$(get_transient_failures)"
  if ! [[ "${current}" =~ ^[0-9]+$ ]]; then
    current=0
  fi
  next=$((current + 1))
  set_transient_failures "${next}"
  echo "${next}"
}

compute_transient_backoff() {
  local fails="$1"
  local base wait i max
  base="${TRANSIENT_RETRY_SLEEP_SEC}"
  max="${TRANSIENT_BACKOFF_MAX_SEC}"
  wait="${base}"
  i=1
  while [ "${i}" -lt "${fails}" ]; do
    wait=$((wait * 2))
    if [ "${wait}" -ge "${max}" ]; then
      wait="${max}"
      break
    fi
    i=$((i + 1))
  done
  if [ "${wait}" -lt "${base}" ]; then
    wait="${base}"
  fi
  echo "${wait}"
}

transient_connectivity_healthcheck() {
  local check_log rc
  check_log="${LOGS_DIR}/healthcheck-$(date -u +%Y%m%dT%H%M%SZ).log"
  set +e
  timeout "${TRANSIENT_HEALTHCHECK_TIMEOUT_SEC}" \
    codex --ask-for-approval never exec --model "${MODEL_PLANNER}" --sandbox workspace-write --cd "$(pwd)" \
    "Reply with: ok" >"${check_log}" 2>&1
  rc=$?
  set -e
  if [ "${rc}" -eq 0 ]; then
    echo "[ralph-local] healthcheck ok (${check_log})"
    return 0
  fi
  echo "[ralph-local] healthcheck failed rc=${rc} (${check_log})"
  return 1
}

ensure_branch_strategy() {
  local target current
  target="${AUTO_PUBLISH_TARGET_BRANCH}"
  case "${BRANCH_STRATEGY}" in
    main)
      current="$(git rev-parse --abbrev-ref HEAD)"
      if [ "${current}" != "${target}" ]; then
        if git status --porcelain | grep -q .; then
          echo "[ralph-local] branch strategy=main but worktree is dirty; staying on ${current}"
          return 0
        fi
        if git checkout "${target}" >/dev/null 2>&1; then
          echo "[ralph-local] switched to ${target} for direct-main workflow"
        else
          echo "[ralph-local] failed to checkout ${target}; staying on ${current}"
        fi
      fi
      git pull --ff-only "${AUTO_PUBLISH_REMOTE}" "${target}" >/dev/null 2>&1 || true
      ;;
    current|feature)
      ;;
    *)
      echo "[ralph-local] unknown RALPH_BRANCH_STRATEGY=${BRANCH_STRATEGY}; using current branch"
      ;;
  esac
}

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
    publish_base="$(get_last_publish_sha)"
    if [ -n "${publish_base}" ]; then
      ahead="$(git rev-list --count "${publish_base}..${target}" 2>/dev/null || echo "${ahead}")"
    fi
    if [ "${ahead}" -lt "${AUTO_PUBLISH_MIN_COMMITS}" ]; then
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

set_issue_status() {
  local file="$1"
  local status="$2"
  local tmp_file

  [ -f "${file}" ] || return 0
  tmp_file="${file}.tmp"
  awk -v status="${status}" '
    BEGIN { in_header=1; replaced=0; inserted=0 }
    in_header && /^---[[:space:]]*$/ {
      if (!replaced) {
        print "status: " status
        inserted=1
      }
      in_header=0
      print
      next
    }
    in_header && /^status:[[:space:]]*/ {
      print "status: " status
      replaced=1
      next
    }
    { print }
    END {
      if (in_header && !replaced && !inserted) {
        print "status: " status
      }
    }
  ' "${file}" > "${tmp_file}"
  mv "${tmp_file}" "${file}"
}

file_mtime_epoch() {
  local file="$1"
  if stat -c %Y "${file}" >/dev/null 2>&1; then
    stat -c %Y "${file}"
    return 0
  fi
  date -r "${file}" +%s
}

get_blocked_retry_count() {
  local issue_id="$1"
  local count
  count="$(awk -v issue_id="${issue_id}" '
    $1 == issue_id {
      if ($2 ~ /^[0-9]+$/) {
        print $2
      } else {
        print 0
      }
      found=1
      exit
    }
    END {
      if (!found) {
        print 0
      }
    }
  ' "${BLOCKED_RETRY_STATE_FILE}" 2>/dev/null || echo 0)"
  if ! [[ "${count}" =~ ^[0-9]+$ ]]; then
    count=0
  fi
  echo "${count}"
}

set_blocked_retry_count() {
  local issue_id="$1"
  local count="$2"
  local tmp_file
  tmp_file="${BLOCKED_RETRY_STATE_FILE}.tmp"
  awk -v issue_id="${issue_id}" -v count="${count}" '
    BEGIN { updated=0 }
    $1 == issue_id {
      print issue_id " " count
      updated=1
      next
    }
    NF > 0 { print }
    END {
      if (!updated) {
        print issue_id " " count
      }
    }
  ' "${BLOCKED_RETRY_STATE_FILE}" > "${tmp_file}"
  mv "${tmp_file}" "${BLOCKED_RETRY_STATE_FILE}"
}

clear_blocked_retry_count() {
  local issue_id="$1"
  local tmp_file
  tmp_file="${BLOCKED_RETRY_STATE_FILE}.tmp"
  awk -v issue_id="${issue_id}" '
    $1 == issue_id { next }
    NF > 0 { print }
  ' "${BLOCKED_RETRY_STATE_FILE}" > "${tmp_file}"
  mv "${tmp_file}" "${BLOCKED_RETRY_STATE_FILE}"
}

pick_retryable_blocked_issue() {
  local file issue_id deps attempts now cooldown age blocked_at
  [ "${BLOCKED_REQUEUE_ENABLED}" = "true" ] || return 1
  now="$(date -u +%s)"
  cooldown="${BLOCKED_REQUEUE_COOLDOWN_SEC}"

  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    issue_id="$(meta_value "${file}" "id")"
    [ -n "${issue_id}" ] || issue_id="$(basename "${file}" .md)"
    deps="$(meta_value "${file}" "depends_on")"
    if ! all_dependencies_satisfied "${deps}"; then
      continue
    fi

    attempts="$(get_blocked_retry_count "${issue_id}")"
    if [ "${attempts}" -ge "${BLOCKED_REQUEUE_MAX_ATTEMPTS}" ]; then
      continue
    fi

    blocked_at="$(file_mtime_epoch "${file}")"
    if ! [[ "${blocked_at}" =~ ^[0-9]+$ ]]; then
      blocked_at="${now}"
    fi
    age=$((now - blocked_at))
    if [ "${age}" -lt "${cooldown}" ]; then
      continue
    fi

    printf '%s|%s|%s\n' "${file}" "${issue_id}" "${attempts}"
    return 0
  done < <(find "${BLOCKED_DIR}" -maxdepth 1 -type f -name '*.md' | sort)

  return 1
}

requeue_retryable_blocked_issue() {
  local candidate file issue_id attempts next_attempt queue_path
  candidate="$(pick_retryable_blocked_issue || true)"
  [ -n "${candidate}" ] || return 1

  file="$(printf '%s' "${candidate}" | awk -F'|' '{print $1}')"
  issue_id="$(printf '%s' "${candidate}" | awk -F'|' '{print $2}')"
  attempts="$(printf '%s' "${candidate}" | awk -F'|' '{print $3}')"
  [ -f "${file}" ] || return 1

  set_issue_status "${file}" "ready"
  queue_path="${QUEUE_DIR}/${issue_id}.md"
  if [ -f "${queue_path}" ]; then
    queue_path="${QUEUE_DIR}/retry-${issue_id}-$(date -u +%Y%m%dT%H%M%SZ).md"
  fi
  mv "${file}" "${queue_path}"

  next_attempt=$((attempts + 1))
  set_blocked_retry_count "${issue_id}" "${next_attempt}"
  echo "[ralph-local] requeued blocked issue ${issue_id} (attempt ${next_attempt}/${BLOCKED_REQUEUE_MAX_ATTEMPTS})"
  return 0
}

get_automanager_last_run() {
  awk 'NR==1 { if ($1 ~ /^[0-9]+$/) print $1; else print 0; exit }' "${AUTOMANAGER_STATE_FILE}" 2>/dev/null || echo 0
}

set_automanager_last_run() {
  local ts="$1"
  printf '%s\n' "${ts}" > "${AUTOMANAGER_STATE_FILE}"
}

run_automanager_if_due() {
  local now last elapsed log_file rc new_issue
  [ "${AUTOMANAGER_ENABLED}" = "true" ] || return 1
  [ -x "${AUTOMANAGER_CMD}" ] || return 1

  now="$(date -u +%s)"
  last="$(get_automanager_last_run)"
  if ! [[ "${last}" =~ ^[0-9]+$ ]]; then
    last=0
  fi
  elapsed=$((now - last))
  if [ "${elapsed}" -lt "${AUTOMANAGER_COOLDOWN_SEC}" ]; then
    return 1
  fi
  set_automanager_last_run "${now}"

  log_file="${LOGS_DIR}/automanager-$(date -u +%Y%m%dT%H%M%SZ).log"
  set +e
  bash -lc "${AUTOMANAGER_CMD}" >"${log_file}" 2>&1
  rc=$?
  set -e
  if [ "${rc}" -ne 0 ]; then
    echo "[ralph-local] automanager failed rc=${rc} log=${log_file}"
    return 1
  fi

  new_issue="$(pick_next_issue || true)"
  if [ -n "${new_issue}" ]; then
    echo "[ralph-local] automanager produced ready issue(s) log=${log_file}"
    return 0
  fi
  echo "[ralph-local] automanager ran with no new ready issue log=${log_file}"
  return 1
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

validation_log_from_state() {
  local state="$1"
  case "${state}" in
    failed:*|passed:*|skipped:*)
      printf '%s\n' "${state#*:}"
      ;;
    *)
      printf '%s\n' ""
      ;;
  esac
}

run_codex_self_heal() {
  local issue_file="$1"
  local role="$2"
  local model="$3"
  local validation_log="$4"
  local attempt="$5"
  local prompt log_file rc validation_excerpt

  log_file="${LOGS_DIR}/$(basename "${issue_file}" .md)-self-heal-${attempt}-$(date -u +%Y%m%dT%H%M%SZ).log"
  validation_excerpt="(validation log unavailable)"
  if [ -n "${validation_log}" ] && [ -f "${validation_log}" ]; then
    validation_excerpt="$(tail -n "${SELF_HEAL_LOG_TAIL_LINES}" "${validation_log}" 2>/dev/null || true)"
  fi

  prompt="$(cat <<EOF
You are running an automated self-heal pass for a local Ralph loop task.

Goal:
- Fix the concrete compile/test/lint failures shown below.
- Keep scope limited to the current issue file.
- After edits, stop. Validation is run by the runner externally.

Requirements:
- Resolve root cause(s), not superficial workarounds.
- Do not skip/disable tests or lint rules.
- Keep changes deterministic and production-safe.

Role context:
$(role_guide "${role}")

Task file (${issue_file}):
$(cat "${issue_file}")

Validation failure excerpt (${validation_log}):
${validation_excerpt}
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

self_heal_validation_failure() {
  local issue_file="$1"
  local issue_id="$2"
  local role="$3"
  local validation_state="$4"
  local attempt validation_log heal_log codex_rc transient_fails sleep_sec

  SELF_HEAL_RESULT="validation-failed"
  SELF_HEAL_LAST_ATTEMPTS=0
  SELF_HEAL_LAST_CODEX_RC=0
  SELF_HEAL_FINAL_VALIDATION_STATE="${validation_state}"

  validation_log="$(validation_log_from_state "${validation_state}")"
  attempt=1
  while [ "${attempt}" -le "${SELF_HEAL_MAX_ATTEMPTS}" ]; do
    SELF_HEAL_LAST_ATTEMPTS="${attempt}"
    echo "[ralph-local] self-heal ${issue_id} attempt ${attempt}/${SELF_HEAL_MAX_ATTEMPTS} model=${SELF_HEAL_MODEL}"

    if run_codex_self_heal "${issue_file}" "${role}" "${SELF_HEAL_MODEL}" "${validation_log}" "${attempt}"; then
      codex_rc=0
    else
      codex_rc=$?
    fi
    heal_log="${RALPH_LAST_LOG_FILE:-}"

    if [ "${codex_rc}" -ne 0 ]; then
      if [ "${TRANSIENT_REQUEUE_ENABLED}" = "true" ] && is_transient_model_error "${heal_log}"; then
        transient_fails="$(increment_transient_failures)"
        sleep_sec="$(compute_transient_backoff "${transient_fails}")"
        echo "[ralph-local] self-heal transient model error on ${issue_id} (fails=${transient_fails}, sleep=${sleep_sec}s)"
        sleep "${sleep_sec}"
        attempt=$((attempt + 1))
        continue
      fi
      SELF_HEAL_RESULT="codex-failed"
      SELF_HEAL_LAST_CODEX_RC="${codex_rc}"
      return 1
    fi

    reset_transient_failures
    validation_state="$(run_validation_if_needed "${role}" "${issue_id}" || true)"
    SELF_HEAL_FINAL_VALIDATION_STATE="${validation_state}"
    if [[ "${validation_state}" != failed:* ]]; then
      SELF_HEAL_RESULT="healed"
      echo "[ralph-local] self-heal succeeded for ${issue_id} on attempt ${attempt}"
      return 0
    fi

    validation_log="$(validation_log_from_state "${validation_state}")"
    echo "[ralph-local] self-heal validation still failing for ${issue_id} on attempt ${attempt}"
    sleep "${SELF_HEAL_RETRY_SLEEP_SEC}"
    attempt=$((attempt + 1))
  done

  SELF_HEAL_RESULT="validation-failed"
  SELF_HEAL_FINAL_VALIDATION_STATE="${validation_state}"
  return 1
}

loop_count=0
noop_count=0
CURRENT_ISSUE_ID=""
CURRENT_IN_PROGRESS_PATH=""

requeue_current_issue_on_exit() {
  local src dst
  src="${CURRENT_IN_PROGRESS_PATH:-}"
  [ -n "${src}" ] || return 0
  [ -f "${src}" ] || return 0

  dst="${QUEUE_DIR}/${CURRENT_ISSUE_ID}.md"
  if [ -f "${dst}" ]; then
    dst="${QUEUE_DIR}/recovered-${CURRENT_ISSUE_ID}-$(date -u +%Y%m%dT%H%M%SZ).md"
  fi
  set_issue_status "${src}" "ready"
  mv "${src}" "${dst}"
  echo "[ralph-local] recovered ${CURRENT_ISSUE_ID} to queue on runner exit"
}

trap requeue_current_issue_on_exit EXIT INT TERM

ensure_branch_strategy

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
    if requeue_retryable_blocked_issue; then
      continue
    fi
    if run_automanager_if_due; then
      continue
    fi
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
  CURRENT_ISSUE_ID="${issue_id}"
  CURRENT_IN_PROGRESS_PATH="${in_progress_path}"
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
      transient_fails="$(increment_transient_failures)"
      sleep_sec="$(compute_transient_backoff "${transient_fails}")"
      requeue_path="${QUEUE_DIR}/${issue_id}.md"
      if [ -f "${requeue_path}" ]; then
        requeue_path="${QUEUE_DIR}/retry-${issue_id}-$(date -u +%Y%m%dT%H%M%SZ).md"
      fi
      mv "${in_progress_path}" "${requeue_path}"
      CURRENT_ISSUE_ID=""
      CURRENT_IN_PROGRESS_PATH=""
      echo "[ralph-local] transient model error on ${issue_id}; requeued (fails=${transient_fails})"
      if [ "${transient_fails}" -ge "${TRANSIENT_HEALTHCHECK_AFTER_FAILS}" ]; then
        transient_connectivity_healthcheck || true
      fi
      echo "[ralph-local] transient backoff sleep=${sleep_sec}s"
      sleep "${sleep_sec}"
      loop_count=$((loop_count + 1))
      continue
    fi

    reset_transient_failures
    blocked_path="${BLOCKED_DIR}/${issue_id}.md"
    set_issue_status "${in_progress_path}" "blocked"
    mv "${in_progress_path}" "${blocked_path}"
    CURRENT_ISSUE_ID=""
    CURRENT_IN_PROGRESS_PATH=""
    append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "not-run" "codex exited with code ${codex_rc}" ""
    echo "[ralph-local] blocked ${issue_id}: codex failure"
    loop_count=$((loop_count + 1))
    continue
  fi

  reset_transient_failures
  validation_state="$(run_validation_if_needed "${role}" "${issue_id}" || true)"
  if [[ "${validation_state}" == failed:* ]]; then
    if [ "${SELF_HEAL_ENABLED}" = "true" ] && csv_contains "${VALIDATE_ROLES}" "${role}"; then
      if self_heal_validation_failure "${in_progress_path}" "${issue_id}" "${role}" "${validation_state}"; then
        validation_state="${SELF_HEAL_FINAL_VALIDATION_STATE}"
        log_file="${RALPH_LAST_LOG_FILE:-${log_file}}"
      else
        validation_state="${SELF_HEAL_FINAL_VALIDATION_STATE}"
        log_file="${RALPH_LAST_LOG_FILE:-${log_file}}"
        blocked_path="${BLOCKED_DIR}/${issue_id}.md"
        set_issue_status "${in_progress_path}" "blocked"
        mv "${in_progress_path}" "${blocked_path}"
        CURRENT_ISSUE_ID=""
        CURRENT_IN_PROGRESS_PATH=""
        case "${SELF_HEAL_RESULT}" in
          codex-failed)
            blocked_note="self-heal codex failed after ${SELF_HEAL_LAST_ATTEMPTS} attempt(s), rc=${SELF_HEAL_LAST_CODEX_RC}"
            ;;
          *)
            blocked_note="validation failed after self-heal attempts=${SELF_HEAL_LAST_ATTEMPTS}"
            ;;
        esac
        append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "${validation_state}" "${blocked_note}" ""
        echo "[ralph-local] blocked ${issue_id}: ${blocked_note}"
        loop_count=$((loop_count + 1))
        continue
      fi
    else
      blocked_path="${BLOCKED_DIR}/${issue_id}.md"
      set_issue_status "${in_progress_path}" "blocked"
      mv "${in_progress_path}" "${blocked_path}"
      CURRENT_ISSUE_ID=""
      CURRENT_IN_PROGRESS_PATH=""
      append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "${validation_state}" "validation failed" ""
      echo "[ralph-local] blocked ${issue_id}: validation failed"
      loop_count=$((loop_count + 1))
      continue
    fi
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
  set_issue_status "${in_progress_path}" "done"
  mv "${in_progress_path}" "${done_path}"
  clear_blocked_retry_count "${issue_id}"
  CURRENT_ISSUE_ID=""
  CURRENT_IN_PROGRESS_PATH=""
  append_result_block "${done_path}" "done" "${role}" "${model}" "${log_file}" "${validation_state}" "completed" "${commit_sha}"
  echo "[ralph-local] completed ${issue_id} commit=${commit_sha:-none}"

  loop_count=$((loop_count + 1))
  if [ "${noop_count}" -ge "${NOOP_LIMIT}" ]; then
    echo "[ralph-local] repeated no-op completions reached limit (${NOOP_LIMIT}), stopping"
    break
  fi
done
