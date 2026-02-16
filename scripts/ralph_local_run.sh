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
require_cmd sort
require_cmd comm

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
ISSUE_CONTRACT_CMD="${RALPH_ISSUE_CONTRACT_CMD:-scripts/ralph_issue_contract.sh}"
if [ ! -x "${ISSUE_CONTRACT_CMD}" ]; then
  echo "issue contract script is missing or not executable: ${ISSUE_CONTRACT_CMD}" >&2
  exit 2
fi
INVARIANTS_CMD="${RALPH_INVARIANTS_CMD:-scripts/ralph_invariants.sh}"
if [ ! -x "${INVARIANTS_CMD}" ]; then
  echo "invariants script is missing or not executable: ${INVARIANTS_CMD}" >&2
  exit 2
fi
PLAN_SCHEMA_VALIDATOR_CMD="${PLAN_SCHEMA_VALIDATOR_CMD:-scripts/validate_planning_output.sh}"
if [ ! -x "${PLAN_SCHEMA_VALIDATOR_CMD}" ]; then
  echo "planning schema validator script is missing or not executable: ${PLAN_SCHEMA_VALIDATOR_CMD}" >&2
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
LEARNING_NOTES_FILE="${RALPH_ROOT}/state.learning.md"
LEARNING_JSONL_FILE="${RALPH_ROOT}/state.learning.jsonl"
CONTEXT_ARCHIVE_DIR="${RALPH_ROOT}/archive/context"

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
RISK_SCORE_COMPLEX_THRESHOLD="${RALPH_RISK_SCORE_COMPLEX_THRESHOLD:-7}"
LEARNING_CONTEXT_LINES="${RALPH_LEARNING_CONTEXT_LINES:-40}"
LEARNING_CONTEXT_ITEMS="${RALPH_LEARNING_CONTEXT_ITEMS:-${LEARNING_CONTEXT_LINES}}"
LEARNING_NOTE_MAX_CHARS="${RALPH_LEARNING_NOTE_MAX_CHARS:-220}"
LEARNING_EXCERPT_MAX_CHARS="${RALPH_LEARNING_EXCERPT_MAX_CHARS:-260}"
PROMPT_CONTEXT_MAX_LINES="${RALPH_PROMPT_CONTEXT_MAX_LINES:-220}"
PROMPT_CONTEXT_HEAD_LINES="${RALPH_PROMPT_CONTEXT_HEAD_LINES:-100}"
PROMPT_CONTEXT_TAIL_LINES="${RALPH_PROMPT_CONTEXT_TAIL_LINES:-100}"
PROMPT_CONTEXT_ARCHIVE_ENABLED="${RALPH_PROMPT_CONTEXT_ARCHIVE_ENABLED:-true}"
PROMPT_CONTEXT_ARCHIVE_MIN_LINES="${RALPH_PROMPT_CONTEXT_ARCHIVE_MIN_LINES:-260}"
PROMPT_CONTEXT_ADAPTIVE_ENABLED="${RALPH_PROMPT_CONTEXT_ADAPTIVE_ENABLED:-true}"
PROMPT_CONTEXT_ADAPTIVE_RISK_SCORE_THRESHOLD="${RALPH_PROMPT_CONTEXT_ADAPTIVE_RISK_SCORE_THRESHOLD:-${RISK_SCORE_COMPLEX_THRESHOLD}}"
PROMPT_CONTEXT_ADAPTIVE_HIGH_RISK_CLASSES="${RALPH_PROMPT_CONTEXT_ADAPTIVE_HIGH_RISK_CLASSES:-high,critical}"
PROMPT_CONTEXT_PLANNER_MAX_LINES="${RALPH_PROMPT_CONTEXT_PLANNER_MAX_LINES:-280}"
PROMPT_CONTEXT_PLANNER_HEAD_LINES="${RALPH_PROMPT_CONTEXT_PLANNER_HEAD_LINES:-140}"
PROMPT_CONTEXT_PLANNER_TAIL_LINES="${RALPH_PROMPT_CONTEXT_PLANNER_TAIL_LINES:-140}"
PROMPT_CONTEXT_HIGH_RISK_MAX_LINES="${RALPH_PROMPT_CONTEXT_HIGH_RISK_MAX_LINES:-220}"
PROMPT_CONTEXT_HIGH_RISK_HEAD_LINES="${RALPH_PROMPT_CONTEXT_HIGH_RISK_HEAD_LINES:-110}"
PROMPT_CONTEXT_HIGH_RISK_TAIL_LINES="${RALPH_PROMPT_CONTEXT_HIGH_RISK_TAIL_LINES:-110}"
PROMPT_CONTEXT_PLANNING_FIRST_MAX_LINES="${RALPH_PROMPT_CONTEXT_PLANNING_FIRST_MAX_LINES:-140}"
PROMPT_CONTEXT_PLANNING_FIRST_HEAD_LINES="${RALPH_PROMPT_CONTEXT_PLANNING_FIRST_HEAD_LINES:-70}"
PROMPT_CONTEXT_PLANNING_FIRST_TAIL_LINES="${RALPH_PROMPT_CONTEXT_PLANNING_FIRST_TAIL_LINES:-70}"
PROMPT_PLANNING_FIRST_ENABLED="${RALPH_PROMPT_PLANNING_FIRST_ENABLED:-true}"
PROMPT_PLANNING_MILESTONES_MAX="${RALPH_PROMPT_PLANNING_MILESTONES_MAX:-2}"
PROMPT_PLANNING_ACCEPTANCE_MAX="${RALPH_PROMPT_PLANNING_ACCEPTANCE_MAX:-4}"
PROMPT_PLANNING_DECISIONS_MAX="${RALPH_PROMPT_PLANNING_DECISIONS_MAX:-3}"
PROMPT_PLANNING_OBJECTIVE_MAX_CHARS="${RALPH_PROMPT_PLANNING_OBJECTIVE_MAX_CHARS:-420}"
PROMPT_PLANNING_OUTCOME_MAX_CHARS="${RALPH_PROMPT_PLANNING_OUTCOME_MAX_CHARS:-260}"
PROMPT_PLANNING_DECISION_MAX_CHARS="${RALPH_PROMPT_PLANNING_DECISION_MAX_CHARS:-220}"
DEFAULT_MAX_DIFF_SCOPE="${RALPH_DEFAULT_MAX_DIFF_SCOPE:-25}"
STRICT_CONTRACT_GATE="${RALPH_STRICT_CONTRACT_GATE:-true}"
PLANNER_OUTPUT_FILE_TEMPLATE="${RALPH_PLANNER_OUTPUT_FILE_TEMPLATE:-${RALPH_ROOT}/plans/plan-output-%s.json}"
AUTO_COMPACT_CONTEXT_ENABLED="${RALPH_AUTO_COMPACT_CONTEXT_ENABLED:-true}"
CONTEXT_COMPACT_CMD="${RALPH_CONTEXT_COMPACT_CMD:-scripts/ralph_context_compact.sh}"

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
CURRENT_MODEL_REASON=""
CURRENT_RISK_SCORE=0
EVIDENCE_FILE_PATH=""
SCOPE_GUARD_CHANGED_FILE=""
SCOPE_GUARD_REASON_FILE=""
SCOPE_GUARD_CHANGED_COUNT=0

if [ "${LOCAL_TRUST_MODE}" = "true" ]; then
  CODEX_SANDBOX="${AGENT_CODEX_SANDBOX:-danger-full-access}"
  CODEX_APPROVAL="never"
fi

mkdir -p "${QUEUE_DIR}" "${IN_PROGRESS_DIR}" "${DONE_DIR}" "${BLOCKED_DIR}" "${REPORTS_DIR}" "${LOGS_DIR}" "${CONTEXT_ARCHIVE_DIR}"
[ -f "${STATE_FILE}" ] || printf 'RALPH_LOCAL_ENABLED=true\n' > "${STATE_FILE}"
[ -f "${TRANSIENT_STATE_FILE}" ] || printf '0\n' > "${TRANSIENT_STATE_FILE}"
[ -f "${BLOCKED_RETRY_STATE_FILE}" ] || : > "${BLOCKED_RETRY_STATE_FILE}"
[ -f "${AUTOMANAGER_STATE_FILE}" ] || printf '0\n' > "${AUTOMANAGER_STATE_FILE}"
[ -f "${LEARNING_NOTES_FILE}" ] || cat >"${LEARNING_NOTES_FILE}" <<'EOF'
# Ralph Learning Notes
EOF
[ -f "${LEARNING_JSONL_FILE}" ] || : > "${LEARNING_JSONL_FILE}"

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

run_context_compact_if_enabled() {
  [ "${AUTO_COMPACT_CONTEXT_ENABLED}" = "true" ] || return 0
  if [ ! -x "${CONTEXT_COMPACT_CMD}" ]; then
    echo "[ralph-local] context compact script missing/unexecutable: ${CONTEXT_COMPACT_CMD}"
    return 0
  fi
  if ! "${CONTEXT_COMPACT_CMD}" >/dev/null 2>&1; then
    echo "[ralph-local] context compact step failed; continuing without compaction"
  fi
}

run_context_compact_if_enabled

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

issue_reference_value() {
  local file="$1"
  local key="$2"
  local value

  value="$(meta_value "${file}" "${key}")"
  if [ -n "${value}" ]; then
    printf '%s\n' "${value}"
    return 0
  fi

  awk -v key="${key}" '
    {
      line=$0
      gsub(/^[[:space:]]*-[[:space:]]*/, "", line)
      if (line ~ ("^" key ":[[:space:]]*")) {
        sub("^" key ":[[:space:]]*", "", line)
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
        print line
        exit
      }
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

trim_ws() {
  awk '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); print}'
}

clip_text() {
  local text="$1"
  local max_chars="$2"
  if ! [[ "${max_chars}" =~ ^[0-9]+$ ]] || [ "${max_chars}" -le 0 ]; then
    printf '%s' "${text}"
    return 0
  fi
  awk -v max_chars="${max_chars}" '
    {
      line=$0
      gsub(/[[:space:]]+/, " ", line)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
      if (length(line) > max_chars) {
        printf "%s...", substr(line, 1, max_chars)
      } else {
        printf "%s", line
      }
    }
  ' <<<"${text}"
}

context_block_for_prompt() {
  local issue_id="$1"
  local issue_file="$2"
  local role="$3"
  local line_count byte_count omitted archive_file ts head_lines tail_lines max_lines policy_reason

  [ -f "${CONTEXT_FILE}" ] || {
    echo "(no context file)"
    return 0
  }

  line_count="$(wc -l < "${CONTEXT_FILE}" | awk '{print $1}')"
  byte_count="$(wc -c < "${CONTEXT_FILE}" | awk '{print $1}')"
  IFS='|' read -r max_lines head_lines tail_lines policy_reason < <(derive_prompt_context_limits "${issue_file}" "${role}")
  echo "(context policy: ${policy_reason}; budget=${max_lines}; head=${head_lines}; tail=${tail_lines})"

  if [[ "${PROMPT_CONTEXT_ARCHIVE_ENABLED}" = "true" ]] \
    && [[ "${PROMPT_CONTEXT_ARCHIVE_MIN_LINES}" =~ ^[0-9]+$ ]] \
    && [ "${line_count}" -ge "${PROMPT_CONTEXT_ARCHIVE_MIN_LINES}" ]; then
    ts="$(date -u +%Y%m%dT%H%M%SZ)"
    archive_file="${CONTEXT_ARCHIVE_DIR}/${issue_id}-context-${ts}.md"
    cp "${CONTEXT_FILE}" "${archive_file}" 2>/dev/null || true
    echo "(full context archived: ${archive_file})"
  fi

  if [ "${line_count}" -le "${max_lines}" ]; then
    echo
    cat "${CONTEXT_FILE}"
    return 0
  fi

  omitted=$((line_count - head_lines - tail_lines))
  if [ "${omitted}" -lt 0 ]; then
    omitted=0
  fi

  echo "(context compressed for prompt: ${line_count} lines, ${byte_count} bytes)"
  echo
  echo "### Context (head ${head_lines})"
  sed -n "1,${head_lines}p" "${CONTEXT_FILE}"
  if [ "${omitted}" -gt 0 ]; then
    echo
    echo "... (${omitted} lines omitted) ..."
  fi
  if [ "${tail_lines}" -gt 0 ]; then
    echo
    echo "### Context (tail ${tail_lines})"
    tail -n "${tail_lines}" "${CONTEXT_FILE}"
  fi
}

planner_output_path_for_issue_file() {
  local issue_file="$1"
  local planner_output planner_issue

  planner_output="$(issue_reference_value "${issue_file}" "planner_output")"
  if [ -z "${planner_output}" ]; then
    planner_issue="$(issue_reference_value "${issue_file}" "planner_issue")"
    if [ -n "${planner_issue}" ]; then
      planner_output="$(planner_output_file_for_issue "${planner_issue}")"
    fi
  fi
  printf '%s\n' "${planner_output}"
}

planning_contract_available() {
  local issue_file="$1"
  local planner_output
  planner_output="$(planner_output_path_for_issue_file "${issue_file}")"
  [ -n "${planner_output}" ] && [ -f "${planner_output}" ]
}

planning_context_block() {
  local issue_file="$1"
  local issue_id="$2"
  local role="$3"
  local planner_output

  if [ "${role}" = "planner" ] || [ "${PROMPT_PLANNING_FIRST_ENABLED}" != "true" ]; then
    echo "(planning-first bundle disabled for role=${role})"
    return 0
  fi

  planner_output="$(planner_output_path_for_issue_file "${issue_file}")"
  if [ -z "${planner_output}" ]; then
    echo "(no planner_output reference found in issue metadata/notes)"
    return 0
  fi
  if [ ! -f "${planner_output}" ]; then
    echo "(planner_output file missing: ${planner_output})"
    return 0
  fi

  echo "- planner_output: ${planner_output}"
  if command -v jq >/dev/null 2>&1; then
    jq -r \
      --arg issue_id "${issue_id}" \
      --argjson milestones_max "${PROMPT_PLANNING_MILESTONES_MAX}" \
      --argjson acceptance_max "${PROMPT_PLANNING_ACCEPTANCE_MAX}" \
      --argjson decisions_max "${PROMPT_PLANNING_DECISIONS_MAX}" \
      --argjson objective_max "${PROMPT_PLANNING_OBJECTIVE_MAX_CHARS}" \
      --argjson outcome_max "${PROMPT_PLANNING_OUTCOME_MAX_CHARS}" \
      --argjson decision_max "${PROMPT_PLANNING_DECISION_MAX_CHARS}" '
      def clip($n):
        gsub("[[:space:]]+"; " ")
        | gsub("^\\s+|\\s+$"; "")
        | if length > $n then .[0:$n] + "..." else . end;
      def relevant:
        [.roadmap.milestones[]?
          | select(
              ((.outcome // "") | contains($issue_id))
              or ((.title // "") | contains($issue_id))
              or (((.acceptance // []) | join(" ")) | contains($issue_id))
            )];
      def selected_milestones:
        (relevant) as $r
        | if ($r | length) > 0
          then $r[:$milestones_max]
          else (.roadmap.milestones // [])[:$milestones_max]
          end;

      "Objective:",
      ((.roadmap.objective // "n/a") | clip($objective_max)),
      "",
      "Contract defaults:",
      "- risk_class: \(.contract_defaults.risk_class // "n/a")",
      "- max_diff_scope: \(.contract_defaults.max_diff_scope // "n/a")",
      "- acceptance_tests: \((.contract_defaults.acceptance_tests // "n/a") | clip(180))",
      "- invariants: \((.contract_defaults.invariants // "n/a") | clip(240))",
      "",
      "Milestones (selected):",
      (selected_milestones[]? |
        "- [\(.id // "n/a")] \((.title // "n/a") | clip(180))\n"
        + "  outcome: \((.outcome // "n/a") | clip($outcome_max))\n"
        + "  acceptance: \(((.acceptance // [])[:$acceptance_max] | join(" | ")) | clip(320))\n"
        + "  risks: \(((.risks // [])[:2] | join(" | ")) | clip(260))"
      ),
      "",
      "Decisions (top):",
      ((.decisions // [])[:$decisions_max][]? | "- " + (. | clip($decision_max)))
    ' "${planner_output}" 2>/dev/null || {
      echo "(planner_output parse failed; falling back to raw excerpt)"
      sed -n '1,160p' "${planner_output}"
    }
    return 0
  fi

  echo "(jq unavailable; using raw planner_output excerpt)"
  sed -n '1,160p' "${planner_output}"
}

normalize_nonneg_int() {
  local value="$1"
  local fallback="$2"
  if [[ "${value}" =~ ^[0-9]+$ ]]; then
    echo "${value}"
    return 0
  fi
  echo "${fallback}"
}

normalize_context_budget_tuple() {
  local max_lines="$1"
  local head_lines="$2"
  local tail_lines="$3"

  max_lines="$(normalize_nonneg_int "${max_lines}" 220)"
  head_lines="$(normalize_nonneg_int "${head_lines}" 100)"
  tail_lines="$(normalize_nonneg_int "${tail_lines}" 100)"

  if [ "${max_lines}" -lt 80 ]; then
    max_lines=80
  fi
  if [ $((head_lines + tail_lines)) -gt "${max_lines}" ]; then
    head_lines=$((max_lines / 2))
    tail_lines=$((max_lines - head_lines))
  fi

  echo "${max_lines}|${head_lines}|${tail_lines}"
}

derive_prompt_context_limits() {
  local issue_file="$1"
  local role="$2"
  local max_lines head_lines tail_lines policy_reason risk_class threshold high_risk_classes
  local planning_max planning_head planning_tail

  IFS='|' read -r max_lines head_lines tail_lines < <(
    normalize_context_budget_tuple \
      "${PROMPT_CONTEXT_MAX_LINES}" \
      "${PROMPT_CONTEXT_HEAD_LINES}" \
      "${PROMPT_CONTEXT_TAIL_LINES}"
  )
  policy_reason="base"

  [ "${PROMPT_CONTEXT_ADAPTIVE_ENABLED}" = "true" ] || {
    echo "${max_lines}|${head_lines}|${tail_lines}|${policy_reason}"
    return 0
  }

  risk_class="$(meta_value "${issue_file}" "risk_class")"
  [ -n "${risk_class}" ] || risk_class="$(default_issue_value "risk_class" "${role}")"
  risk_class="$(printf '%s' "${risk_class}" | tr '[:upper:]' '[:lower:]')"
  high_risk_classes="$(printf '%s' "${PROMPT_CONTEXT_ADAPTIVE_HIGH_RISK_CLASSES}" | tr '[:upper:]' '[:lower:]')"
  threshold="$(normalize_nonneg_int "${PROMPT_CONTEXT_ADAPTIVE_RISK_SCORE_THRESHOLD}" "${RISK_SCORE_COMPLEX_THRESHOLD}")"

  case "${role}" in
    planner)
      IFS='|' read -r max_lines head_lines tail_lines < <(
        normalize_context_budget_tuple \
          "${PROMPT_CONTEXT_PLANNER_MAX_LINES}" \
          "${PROMPT_CONTEXT_PLANNER_HEAD_LINES}" \
          "${PROMPT_CONTEXT_PLANNER_TAIL_LINES}"
      )
      policy_reason="planner-adaptive"
      ;;
    developer)
      if [ "${CURRENT_RISK_SCORE:-0}" -ge "${threshold}" ] || csv_contains "${high_risk_classes}" "${risk_class}"; then
        IFS='|' read -r max_lines head_lines tail_lines < <(
          normalize_context_budget_tuple \
            "${PROMPT_CONTEXT_HIGH_RISK_MAX_LINES}" \
            "${PROMPT_CONTEXT_HIGH_RISK_HEAD_LINES}" \
            "${PROMPT_CONTEXT_HIGH_RISK_TAIL_LINES}"
        )
        policy_reason="developer-high-risk-adaptive(score=${CURRENT_RISK_SCORE:-0},risk_class=${risk_class})"
      fi
      ;;
  esac

  if [ "${PROMPT_PLANNING_FIRST_ENABLED}" = "true" ] \
    && [ "${role}" != "planner" ] \
    && planning_contract_available "${issue_file}"; then
    IFS='|' read -r planning_max planning_head planning_tail < <(
      normalize_context_budget_tuple \
        "${PROMPT_CONTEXT_PLANNING_FIRST_MAX_LINES}" \
        "${PROMPT_CONTEXT_PLANNING_FIRST_HEAD_LINES}" \
        "${PROMPT_CONTEXT_PLANNING_FIRST_TAIL_LINES}"
    )
    if [ "${planning_max}" -lt "${max_lines}" ]; then
      max_lines="${planning_max}"
      head_lines="${planning_head}"
      tail_lines="${planning_tail}"
      policy_reason="${policy_reason}+planning-first"
    else
      policy_reason="${policy_reason}+planning-ref"
    fi
  fi

  echo "${max_lines}|${head_lines}|${tail_lines}|${policy_reason}"
}

default_issue_value() {
  local key="$1"
  local role="$2"
  case "${key}" in
    risk_class)
      case "${role}" in
        planner) echo "high" ;;
        qa) echo "medium" ;;
        *) echo "medium" ;;
      esac
      ;;
    max_diff_scope)
      case "${role}" in
        planner) echo "15" ;;
        qa) echo "20" ;;
        *) echo "${DEFAULT_MAX_DIFF_SCOPE}" ;;
      esac
      ;;
    allowed_paths)
      case "${role}" in
        planner) echo "IMPLEMENTATION_PLAN.md,specs/,docs/,PROMPT_plan.md,.agent/,.ralph/" ;;
        qa) echo "internal/,scripts/,docs/,specs/,.ralph/" ;;
        *) echo "cmd/,internal/,pkg/,proto/,sidecar/,scripts/,docs/,specs/,PROMPT_build.md,PROMPT_plan.md,IMPLEMENTATION_PLAN.md,.ralph/" ;;
      esac
      ;;
    denied_paths)
      echo ".github/workflows/,deployments/,.git/"
      ;;
    acceptance_tests)
      echo "make test,make test-sidecar,make lint"
      ;;
    invariants)
      case "${role}" in
        planner) echo "canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,chain_adapter_runtime_wired" ;;
        qa) echo "canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,solana_fee_event_coverage,base_fee_split_coverage,chain_adapter_runtime_wired" ;;
        *) echo "canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,chain_adapter_runtime_wired" ;;
      esac
      ;;
    non_goals)
      echo "replace-with-non-goals"
      ;;
    evidence_required)
      echo "true"
      ;;
    *)
      echo ""
      ;;
  esac
}

insert_meta_key_before_delimiter() {
  local file="$1"
  local key="$2"
  local value="$3"
  local tmp_file
  tmp_file="${file}.tmp"
  awk -v key="${key}" -v value="${value}" '
    BEGIN { inserted=0 }
    /^---[[:space:]]*$/ && !inserted {
      print key ": " value
      inserted=1
    }
    { print }
  ' "${file}" > "${tmp_file}"
  mv "${tmp_file}" "${file}"
}

ensure_issue_contract_defaults() {
  local file="$1"
  local role="$2"
  local key value
  for key in risk_class max_diff_scope allowed_paths denied_paths acceptance_tests invariants non_goals evidence_required; do
    if [ -z "$(meta_value "${file}" "${key}")" ]; then
      value="$(default_issue_value "${key}" "${role}")"
      [ -n "${value}" ] || continue
      insert_meta_key_before_delimiter "${file}" "${key}" "${value}"
    fi
  done
  if ! has_section "${file}" "Non Goals"; then
    cat >>"${file}" <<'EOF'

## Non Goals
- replace-with-non-goals
EOF
  fi
}

has_section() {
  local file="$1"
  local section="$2"
  awk -v section="${section}" '
    $0 == "## " section { found=1; exit }
    END { exit(found ? 0 : 1) }
  ' "${file}"
}

validate_issue_contract_gate() {
  local file="$1"
  local role="$2"
  ensure_issue_contract_defaults "${file}" "${role}"
  if [ "${STRICT_CONTRACT_GATE}" != "true" ]; then
    return 0
  fi
  "${ISSUE_CONTRACT_CMD}" validate "${file}" "${role}"
}

collect_changed_files_snapshot() {
  local out_file="$1"
  git status --porcelain | awk '
    {
      path=$0
      sub(/^.../, "", path)
      gsub(/^ +| +$/, "", path)
      if (path ~ / -> /) {
        split(path, a, / -> /)
        path=a[2]
      }
      if (path != "") print path
    }
  ' | sort -u > "${out_file}"
}

new_changed_files_since_snapshot() {
  local snapshot_file="$1"
  local after_file
  after_file="$(mktemp)"
  collect_changed_files_snapshot "${after_file}"
  comm -13 "${snapshot_file}" "${after_file}" || true
  rm -f "${after_file}"
}

matches_path_rule() {
  local path="$1"
  local rule="$2"
  local normalized
  normalized="$(trim_ws <<<"${rule}")"
  [ -n "${normalized}" ] || return 1
  normalized="${normalized#./}"
  if [[ "${normalized}" == *"*" ]]; then
    normalized="${normalized%\*}"
  fi
  [[ "${path}" == "${normalized}" || "${path}" == "${normalized}"* ]]
}

is_path_allowed() {
  local path="$1"
  local allowed_csv="$2"
  local rule has_rules=false
  while IFS= read -r rule; do
    rule="$(trim_ws <<<"${rule}")"
    [ -n "${rule}" ] || continue
    has_rules=true
    if matches_path_rule "${path}" "${rule}"; then
      return 0
    fi
  done < <(printf '%s\n' "${allowed_csv}" | tr ',' '\n')
  [ "${has_rules}" = "false" ]
}

is_path_denied() {
  local path="$1"
  local denied_csv="$2"
  local rule
  while IFS= read -r rule; do
    rule="$(trim_ws <<<"${rule}")"
    [ -n "${rule}" ] || continue
    if matches_path_rule "${path}" "${rule}"; then
      return 0
    fi
  done < <(printf '%s\n' "${denied_csv}" | tr ',' '\n')
  return 1
}

evaluate_scope_guard() {
  local issue_file="$1"
  local role="$2"
  local snapshot_file="$3"
  local max_diff_scope allowed_paths denied_paths changed_count=0 path
  local report reason_file

  max_diff_scope="$(meta_value "${issue_file}" "max_diff_scope")"
  [ -n "${max_diff_scope}" ] || max_diff_scope="$(default_issue_value "max_diff_scope" "${role}")"
  allowed_paths="$(meta_value "${issue_file}" "allowed_paths")"
  [ -n "${allowed_paths}" ] || allowed_paths="$(default_issue_value "allowed_paths" "${role}")"
  denied_paths="$(meta_value "${issue_file}" "denied_paths")"
  [ -n "${denied_paths}" ] || denied_paths="$(default_issue_value "denied_paths" "${role}")"

  report="$(mktemp)"
  reason_file="$(mktemp)"
  while IFS= read -r path; do
    [ -n "${path}" ] || continue
    changed_count=$((changed_count + 1))
    printf '%s\n' "${path}" >>"${report}"
    if is_path_denied "${path}" "${denied_paths}"; then
      printf 'denied path touched: %s\n' "${path}" >>"${reason_file}"
      continue
    fi
    if ! is_path_allowed "${path}" "${allowed_paths}"; then
      printf 'out-of-scope path touched: %s\n' "${path}" >>"${reason_file}"
    fi
  done < <(new_changed_files_since_snapshot "${snapshot_file}")

  if ! [[ "${max_diff_scope}" =~ ^[0-9]+$ ]]; then
    max_diff_scope="${DEFAULT_MAX_DIFF_SCOPE}"
  fi
  if [ "${changed_count}" -gt "${max_diff_scope}" ]; then
    printf 'max_diff_scope exceeded: changed=%s limit=%s\n' "${changed_count}" "${max_diff_scope}" >>"${reason_file}"
  fi

  SCOPE_GUARD_CHANGED_FILE="${report}"
  SCOPE_GUARD_REASON_FILE="${reason_file}"
  SCOPE_GUARD_CHANGED_COUNT="${changed_count}"

  if [ -s "${reason_file}" ]; then
    return 1
  fi
  return 0
}

planner_output_file_for_issue() {
  local issue_id="$1"
  printf "${PLANNER_OUTPUT_FILE_TEMPLATE}" "${issue_id}"
}

validate_planner_output_for_issue() {
  local issue_id="$1"
  local output_file
  output_file="$(planner_output_file_for_issue "${issue_id}")"
  if [ ! -f "${output_file}" ]; then
    echo "planner output missing: ${output_file}" >&2
    return 1
  fi
  "${PLAN_SCHEMA_VALIDATOR_CMD}" "${output_file}" >/dev/null
}

compute_issue_risk_score() {
  local issue_file="$1"
  local role="$2"
  local complexity="$3"
  local risk_class title score=0 reasons=""
  local max_diff_scope invariants_blob

  if [ "${role}" != "developer" ]; then
    echo "0|non-developer-role"
    return 0
  fi

  case "${complexity}" in
    low) score=$((score + 1)); reasons="${reasons},complexity:low" ;;
    medium) score=$((score + 2)); reasons="${reasons},complexity:medium" ;;
    high) score=$((score + 4)); reasons="${reasons},complexity:high" ;;
    critical) score=$((score + 6)); reasons="${reasons},complexity:critical" ;;
  esac

  risk_class="$(meta_value "${issue_file}" "risk_class")"
  case "${risk_class}" in
    low) score=$((score + 1)); reasons="${reasons},risk:low" ;;
    medium) score=$((score + 2)); reasons="${reasons},risk:medium" ;;
    high) score=$((score + 4)); reasons="${reasons},risk:high" ;;
    critical) score=$((score + 6)); reasons="${reasons},risk:critical" ;;
  esac

  max_diff_scope="$(meta_value "${issue_file}" "max_diff_scope")"
  if [[ "${max_diff_scope}" =~ ^[0-9]+$ ]]; then
    if [ "${max_diff_scope}" -gt 50 ]; then
      score=$((score + 3)); reasons="${reasons},diff>50"
    elif [ "${max_diff_scope}" -gt 30 ]; then
      score=$((score + 2)); reasons="${reasons},diff>30"
    fi
  fi

  title="$(meta_value "${issue_file}" "title")"
  if printf '%s\n%s\n' "${title}" "$(cat "${issue_file}")" | grep -Eqi "reorg|rollback|replay|idempot|cursor|canonical|normalizer|pipeline|db|storage|ingest|runtime|adapter|fee"; then
    score=$((score + 2))
    reasons="${reasons},domain-critical-keywords"
  fi

  invariants_blob="$(meta_value "${issue_file}" "invariants")"
  if printf '%s' "${invariants_blob}" | grep -Eqi "reorg_recovery_deterministic|chain_adapter_runtime_wired"; then
    score=$((score + 1))
    reasons="${reasons},advanced-invariants"
  fi

  reasons="${reasons#,}"
  [ -n "${reasons}" ] || reasons="none"
  echo "${score}|${reasons}"
}

learning_context_block() {
  if [ ! -f "${LEARNING_NOTES_FILE}" ]; then
    echo "(no prior learning notes)"
    return 0
  fi
  awk -v max_items="${LEARNING_CONTEXT_ITEMS}" -v max_chars="${LEARNING_NOTE_MAX_CHARS}" '
    function clip(s, c) {
      gsub(/[[:space:]]+/, " ", s)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
      if (c > 0 && length(s) > c) {
        return substr(s, 1, c) "..."
      }
      return s
    }
    /^- / {
      line=$0
      sub(/^[[:space:]]*-[[:space:]]*/, "", line)
      entries[++n]=clip(line, max_chars)
    }
    END {
      if (n == 0) {
        print "(no prior learning notes)"
        exit
      }
      if (max_items <= 0) {
        max_items=12
      }
      start=n-max_items+1
      if (start < 1) {
        start=1
      }
      for (i=start; i<=n; i++) {
        print "- " entries[i]
      }
    }
  ' "${LEARNING_NOTES_FILE}" 2>/dev/null || true
}

classify_failure_type() {
  local validation_state="$1"
  local note="$2"
  local log_file="$3"
  local lower_note
  lower_note="$(printf '%s' "${note}" | tr '[:upper:]' '[:lower:]')"

  if printf '%s' "${lower_note}" | grep -Eq "scope guard|out-of-scope|denied path|max_diff_scope"; then
    echo "scope-violation"
    return 0
  fi
  if printf '%s' "${lower_note}" | grep -Eq "contract|planner output"; then
    echo "spec-gap"
    return 0
  fi
  if [ -f "${log_file}" ] && is_transient_model_error "${log_file}"; then
    echo "transient-connectivity"
    return 0
  fi
  if [ -f "${log_file}" ] && grep -Eqi "golangci-lint|eslint|lint" "${log_file}"; then
    echo "lint-failure"
    return 0
  fi
  if [ -f "${log_file}" ] && grep -Eqi "undefined|cannot find|build failed|compile|syntax error" "${log_file}"; then
    echo "compile-failure"
    return 0
  fi
  if [[ "${validation_state}" == failed:* ]]; then
    echo "test-failure"
    return 0
  fi
  echo "unknown"
}

record_failure_learning() {
  local issue_id="$1"
  local role="$2"
  local failure_type="$3"
  local note="$4"
  local log_file="$5"
  local excerpt="" note_key last_entry=""

  note_key="issue=${issue_id} role=${role} type=${failure_type} note=${note}"
  if [ -f "${LEARNING_NOTES_FILE}" ]; then
    last_entry="$(awk '/^- /{last=$0} END{print last}' "${LEARNING_NOTES_FILE}" 2>/dev/null || true)"
    if [ -n "${last_entry}" ] && printf '%s' "${last_entry}" | grep -Fq "${note_key}"; then
      return 0
    fi
  fi

  if [ -n "${log_file}" ] && [ -f "${log_file}" ]; then
    excerpt="$(tail -n 6 "${log_file}" | tr '\n' ' ' | sed -E 's/[[:space:]]+/ /g')"
    excerpt="$(clip_text "${excerpt}" "${LEARNING_EXCERPT_MAX_CHARS}")"
  fi

  cat >>"${LEARNING_NOTES_FILE}" <<EOF
- $(now_utc) issue=${issue_id} role=${role} type=${failure_type} note=${note}
  excerpt: ${excerpt:-n/a}
EOF

  printf '{"time":"%s","issue":"%s","role":"%s","type":"%s","note":"%s","log":"%s"}\n' \
    "$(now_utc)" "${issue_id}" "${role}" "${failure_type}" "$(printf '%s' "${note}" | sed 's/"/\\"/g')" "${log_file}" >> "${LEARNING_JSONL_FILE}"
}

generate_evidence_pack() {
  local issue_file="$1"
  local issue_id="$2"
  local role="$3"
  local model="$4"
  local validation_state="$5"
  local log_file="$6"
  local commit_sha="$7"
  local evidence_file changed_list acceptance_tests invariants
  local planner_output=""

  evidence_file="${REPORTS_DIR}/${issue_id}-evidence.md"
  changed_list="${SCOPE_GUARD_CHANGED_FILE:-}"
  acceptance_tests="$(meta_value "${issue_file}" "acceptance_tests")"
  invariants="$(meta_value "${issue_file}" "invariants")"
  if [ "${role}" = "planner" ]; then
    planner_output="$(planner_output_file_for_issue "${issue_id}")"
  fi

  {
    echo "## Evidence Pack (${issue_id})"
    echo
    echo "- generated_at_utc: $(now_utc)"
    echo "- role: ${role}"
    echo "- model: ${model}"
    echo "- model_reason: ${CURRENT_MODEL_REASON:-n/a}"
    echo "- risk_score: ${CURRENT_RISK_SCORE:-0}"
    echo "- validation: ${validation_state}"
    echo "- codex_log: ${log_file}"
    echo "- commit: ${commit_sha:-none}"
    echo "- acceptance_tests: ${acceptance_tests}"
    echo "- invariants: ${invariants}"
    if [ -n "${planner_output}" ]; then
      echo "- planner_output: ${planner_output}"
    fi
    echo
    echo "### Changed Files"
    if [ -n "${changed_list}" ] && [ -s "${changed_list}" ]; then
      while IFS= read -r path; do
        [ -n "${path}" ] || continue
        echo "- ${path}"
      done < "${changed_list}"
    else
      echo "- none"
    fi
    echo
    echo "### Learning Context Used"
    echo '```text'
    learning_context_block
    echo '```'
  } > "${evidence_file}"

  EVIDENCE_FILE_PATH="${evidence_file}"
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
  local issue_id="${2:-}"
  case "${role}" in
    planner)
      local planner_output
      planner_output="$(planner_output_file_for_issue "${issue_id}")"
      cat <<'EOF'
Role: Planner
- Update implementation/spec docs for executable scope.
- If work should be split, create additional local issues under `.ralph/issues/` using the existing markdown format.
- Keep milestones explicit and measurable.
EOF
      echo "- Write machine-validated planning contract JSON to: ${planner_output}"
      ;;
    qa)
      cat <<'EOF'
Role: QA
- Validate current implementation against acceptance criteria.
- Prefer deterministic checks and concrete failure evidence.
- Write QA report markdown to `.ralph/reports/`.
- Generate at least one counterexample-oriented validation attempt for declared invariants.
- If defects are found, create follow-up developer issues under `.ralph/issues/`.
EOF
      ;;
    *)
      cat <<'EOF'
Role: Developer
- Implement the requested change with production-minded quality.
- Keep diffs small and testable.
- Update tests/docs as required by acceptance criteria.
- Respect issue contract boundaries (`allowed_paths`, `denied_paths`, `max_diff_scope`).
- Preserve declared invariants in `invariants`.
EOF
      ;;
  esac
}

choose_model() {
  local role="$1"
  local complexity="$2"
  local issue_file="$3"
  local score reasons

  case "${role}" in
    planner)
      CURRENT_RISK_SCORE=0
      CURRENT_MODEL_REASON="role=planner"
      echo "${MODEL_PLANNER}"
      ;;
    qa)
      CURRENT_RISK_SCORE=0
      CURRENT_MODEL_REASON="role=qa"
      echo "${MODEL_QA}"
      ;;
    developer)
      IFS='|' read -r score reasons < <(compute_issue_risk_score "${issue_file}" "${role}" "${complexity}")
      CURRENT_RISK_SCORE="${score}"
      if [ "${score}" -ge "${RISK_SCORE_COMPLEX_THRESHOLD}" ]; then
        CURRENT_MODEL_REASON="risk-score=${score} threshold=${RISK_SCORE_COMPLEX_THRESHOLD} reasons=${reasons}"
        echo "${MODEL_DEVELOPER_COMPLEX}"
      else
        CURRENT_MODEL_REASON="risk-score=${score} threshold=${RISK_SCORE_COMPLEX_THRESHOLD} reasons=${reasons}"
        echo "${MODEL_DEVELOPER_FAST}"
      fi
      ;;
    *)
      CURRENT_RISK_SCORE=0
      CURRENT_MODEL_REASON="role=unknown-default-fast"
      echo "${MODEL_DEVELOPER_FAST}"
      ;;
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
- model_reason: ${CURRENT_MODEL_REASON:-n/a}
- risk_score: ${CURRENT_RISK_SCORE:-0}
- completed_at_utc: $(now_utc)
- log_file: ${log_file}
- validation: ${validation_state}
- commit: ${commit_sha}
- evidence: ${EVIDENCE_FILE_PATH:-none}
- note: ${note}
EOF
}

is_transient_model_error() {
  local log_file="$1"
  [ -f "${log_file}" ] || return 1
  grep -Eqi \
    "stream disconnected before completion|error sending request for url|reconnecting\\.\\.\\.|timed out|timeout|temporar|connection reset|connection refused|service unavailable|too many requests|rate limit" \
    "${log_file}"
}

run_codex_for_issue() {
  local issue_file="$1"
  local role="$2"
  local model="$3"
  local prompt log_file rc issue_id issue_allowed issue_denied issue_max_diff issue_invariants issue_acceptance_tests allowed_invariants_csv

  log_file="${LOGS_DIR}/$(basename "${issue_file}" .md)-$(date -u +%Y%m%dT%H%M%SZ).log"
  issue_id="$(meta_value "${issue_file}" "id")"
  issue_allowed="$(meta_value "${issue_file}" "allowed_paths")"
  issue_denied="$(meta_value "${issue_file}" "denied_paths")"
  issue_max_diff="$(meta_value "${issue_file}" "max_diff_scope")"
  issue_invariants="$(meta_value "${issue_file}" "invariants")"
  issue_acceptance_tests="$(meta_value "${issue_file}" "acceptance_tests")"
  allowed_invariants_csv="$("${INVARIANTS_CMD}" csv)"
  prompt="$(cat <<EOF
You are executing a local Ralph loop task with no GitHub dependency.

Workspace policy:
- Use local markdown queue under \`.ralph/\`.
- Finish one issue completely before moving to the next.
- If the task should be split, create additional issue markdown files in \`.ralph/issues/\`.
- Keep changes production-safe and verifiable.
- Respect modification boundaries:
  - allowed_paths: ${issue_allowed}
  - denied_paths: ${issue_denied}
  - max_diff_scope: ${issue_max_diff}
- Preserve invariants: ${issue_invariants}
- Allowed invariant ids for any issue contract edits/creation: ${allowed_invariants_csv}
- Do not introduce new invariant ids outside the allowed registry.
- Validation target: ${issue_acceptance_tests}

$(role_guide "${role}" "${issue_id}")

Model routing:
- selected_model: ${model}
- model_reason: ${CURRENT_MODEL_REASON}
- risk_score: ${CURRENT_RISK_SCORE}

Planning contract context (planner-first):
$(planning_context_block "${issue_file}" "${issue_id}" "${role}")

Shared context file (${CONTEXT_FILE}):
$(context_block_for_prompt "${issue_id}" "${issue_file}" "${role}")

Learning memory (recent failures + lessons):
$(learning_context_block)

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
  local prompt log_file rc validation_excerpt failure_type issue_id

  log_file="${LOGS_DIR}/$(basename "${issue_file}" .md)-self-heal-${attempt}-$(date -u +%Y%m%dT%H%M%SZ).log"
  issue_id="$(meta_value "${issue_file}" "id")"
  [ -n "${issue_id}" ] || issue_id="unknown"
  validation_excerpt="(validation log unavailable)"
  if [ -n "${validation_log}" ] && [ -f "${validation_log}" ]; then
    validation_excerpt="$(tail -n "${SELF_HEAL_LOG_TAIL_LINES}" "${validation_log}" 2>/dev/null || true)"
  fi
  failure_type="$(classify_failure_type "failed:${validation_log}" "self-heal-attempt" "${validation_log}")"

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

Failure classification:
- ${failure_type}

Planning contract context (planner-first):
$(planning_context_block "${issue_file}" "${issue_id}" "${role}")

Learning memory (recent failures + lessons):
$(learning_context_block)

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
  EVIDENCE_FILE_PATH=""
  SCOPE_GUARD_CHANGED_FILE=""
  SCOPE_GUARD_REASON_FILE=""
  SCOPE_GUARD_CHANGED_COUNT=0

  if ! validate_issue_contract_gate "${next_issue}" "${role}"; then
    blocked_path="${BLOCKED_DIR}/${issue_id}.md"
    set_issue_status "${next_issue}" "blocked"
    mv "${next_issue}" "${blocked_path}"
    blocked_note="contract gate failed"
    failure_type="$(classify_failure_type "not-run" "${blocked_note}" "")"
    record_failure_learning "${issue_id}" "${role}" "${failure_type}" "${blocked_note}" ""
    append_result_block "${blocked_path}" "blocked" "${role}" "${MODEL_DEVELOPER_FAST}" "" "not-run" "${blocked_note}" ""
    echo "[ralph-local] blocked ${issue_id}: ${blocked_note}"
    loop_count=$((loop_count + 1))
    continue
  fi

  model="$(choose_model "${role}" "${complexity}" "${next_issue}")"

  in_progress_path="${IN_PROGRESS_DIR}/${issue_id}.md"
  mv "${next_issue}" "${in_progress_path}"
  CURRENT_ISSUE_ID="${issue_id}"
  CURRENT_IN_PROGRESS_PATH="${in_progress_path}"
  pre_change_snapshot="$(mktemp)"
  collect_changed_files_snapshot "${pre_change_snapshot}"
  echo "[ralph-local] processing ${issue_id} role=${role} model=${model} reason=${CURRENT_MODEL_REASON}"

  if run_codex_for_issue "${in_progress_path}" "${role}" "${model}"; then
    codex_rc=0
  else
    codex_rc=$?
  fi
  log_file="${RALPH_LAST_LOG_FILE:-}"
  if [ "${codex_rc}" -ne 0 ]; then
    if [ ! -f "${in_progress_path}" ]; then
      echo "[ralph-local] warning: missing in-progress file for ${issue_id}; skipping recovery path"
      rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
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
      rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
      loop_count=$((loop_count + 1))
      continue
    fi

    reset_transient_failures
    blocked_path="${BLOCKED_DIR}/${issue_id}.md"
    set_issue_status "${in_progress_path}" "blocked"
    mv "${in_progress_path}" "${blocked_path}"
    CURRENT_ISSUE_ID=""
    CURRENT_IN_PROGRESS_PATH=""
    blocked_note="codex exited with code ${codex_rc}"
    append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "not-run" "${blocked_note}" ""
    failure_type="$(classify_failure_type "not-run" "${blocked_note}" "${log_file}")"
    record_failure_learning "${issue_id}" "${role}" "${failure_type}" "${blocked_note}" "${log_file}"
    echo "[ralph-local] blocked ${issue_id}: codex failure type=${failure_type}"
    rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
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
        failure_type="$(classify_failure_type "${validation_state}" "${blocked_note}" "${log_file}")"
        record_failure_learning "${issue_id}" "${role}" "${failure_type}" "${blocked_note}" "${log_file}"
        echo "[ralph-local] blocked ${issue_id}: ${blocked_note} type=${failure_type}"
        rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
        loop_count=$((loop_count + 1))
        continue
      fi
    else
      blocked_path="${BLOCKED_DIR}/${issue_id}.md"
      set_issue_status "${in_progress_path}" "blocked"
      mv "${in_progress_path}" "${blocked_path}"
      CURRENT_ISSUE_ID=""
      CURRENT_IN_PROGRESS_PATH=""
      blocked_note="validation failed"
      append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "${validation_state}" "${blocked_note}" ""
      failure_type="$(classify_failure_type "${validation_state}" "${blocked_note}" "${log_file}")"
      record_failure_learning "${issue_id}" "${role}" "${failure_type}" "${blocked_note}" "${log_file}"
      echo "[ralph-local] blocked ${issue_id}: validation failed type=${failure_type}"
      rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
      loop_count=$((loop_count + 1))
      continue
    fi
  fi

  if [ "${role}" = "planner" ]; then
    if ! validate_planner_output_for_issue "${issue_id}"; then
      blocked_path="${BLOCKED_DIR}/${issue_id}.md"
      set_issue_status "${in_progress_path}" "blocked"
      mv "${in_progress_path}" "${blocked_path}"
      CURRENT_ISSUE_ID=""
      CURRENT_IN_PROGRESS_PATH=""
      blocked_note="planner output schema gate failed"
      append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "${validation_state}" "${blocked_note}" ""
      failure_type="$(classify_failure_type "${validation_state}" "${blocked_note}" "${log_file}")"
      record_failure_learning "${issue_id}" "${role}" "${failure_type}" "${blocked_note}" "${log_file}"
      echo "[ralph-local] blocked ${issue_id}: ${blocked_note}"
      rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
      loop_count=$((loop_count + 1))
      continue
    fi
  fi

  if ! evaluate_scope_guard "${in_progress_path}" "${role}" "${pre_change_snapshot}"; then
    blocked_path="${BLOCKED_DIR}/${issue_id}.md"
    set_issue_status "${in_progress_path}" "blocked"
    mv "${in_progress_path}" "${blocked_path}"
    CURRENT_ISSUE_ID=""
    CURRENT_IN_PROGRESS_PATH=""
    blocked_note="scope guard failed: $(tr '\n' ';' < "${SCOPE_GUARD_REASON_FILE}" | sed -E 's/;+$//')"
    append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "${validation_state}" "${blocked_note}" ""
    failure_type="$(classify_failure_type "${validation_state}" "${blocked_note}" "${log_file}")"
    record_failure_learning "${issue_id}" "${role}" "${failure_type}" "${blocked_note}" "${log_file}"
    echo "[ralph-local] blocked ${issue_id}: scope guard failure"
    rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
    loop_count=$((loop_count + 1))
    continue
  fi

  generate_evidence_pack "${in_progress_path}" "${issue_id}" "${role}" "${model}" "${validation_state}" "${log_file}" ""
  evidence_required="$(meta_value "${in_progress_path}" "evidence_required")"
  [ -n "${evidence_required}" ] || evidence_required="true"
  if [ "${evidence_required}" = "true" ] && [ ! -f "${EVIDENCE_FILE_PATH}" ]; then
    blocked_path="${BLOCKED_DIR}/${issue_id}.md"
    set_issue_status "${in_progress_path}" "blocked"
    mv "${in_progress_path}" "${blocked_path}"
    CURRENT_ISSUE_ID=""
    CURRENT_IN_PROGRESS_PATH=""
    blocked_note="evidence pack generation failed"
    append_result_block "${blocked_path}" "blocked" "${role}" "${model}" "${log_file}" "${validation_state}" "${blocked_note}" ""
    failure_type="$(classify_failure_type "${validation_state}" "${blocked_note}" "${log_file}")"
    record_failure_learning "${issue_id}" "${role}" "${failure_type}" "${blocked_note}" "${log_file}"
    echo "[ralph-local] blocked ${issue_id}: ${blocked_note}"
    rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
    loop_count=$((loop_count + 1))
    continue
  fi

  commit_sha=""
  if [ -n "$(git status --porcelain)" ]; then
    safe_title="$(printf '%s' "${issue_title}" | tr '\n' ' ' | sed -E 's/[[:space:]]+/ /g')"
    git add -A
    git commit -m "ralph(local): ${issue_id} ${safe_title}" >/dev/null || true
    commit_sha="$(git rev-parse --short HEAD 2>/dev/null || true)"
    generate_evidence_pack "${in_progress_path}" "${issue_id}" "${role}" "${model}" "${validation_state}" "${log_file}" "${commit_sha}"
    publish_to_main_if_ready || true
    noop_count=0
  else
    noop_count=$((noop_count + 1))
  fi

  done_path="${DONE_DIR}/${issue_id}.md"
  if [ ! -f "${in_progress_path}" ]; then
    echo "[ralph-local] warning: missing in-progress file before completion for ${issue_id}; skipping completion move"
    rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"
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
  rm -f "${pre_change_snapshot}" "${SCOPE_GUARD_CHANGED_FILE:-}" "${SCOPE_GUARD_REASON_FILE:-}"

  loop_count=$((loop_count + 1))
  if [ "${noop_count}" -ge "${NOOP_LIMIT}" ]; then
    echo "[ralph-local] repeated no-op completions reached limit (${NOOP_LIMIT}), stopping"
    break
  fi
done
