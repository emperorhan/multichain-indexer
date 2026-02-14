#!/usr/bin/env bash
set -euo pipefail

# Manage local Ralph loop as a background daemon.
# Usage:
#   scripts/ralph_local_daemon.sh start|stop|status|tail

CMD="${1:-status}"
RALPH_ROOT="${RALPH_ROOT:-.ralph}"
PID_FILE="${RALPH_ROOT}/runner.pid"
LOG_FILE="${RALPH_ROOT}/logs/runner.out"
RUN_SCRIPT="${RALPH_RUN_SCRIPT:-scripts/ralph_local_run.sh}"
SUPERVISOR_SCRIPT="${RALPH_SUPERVISOR_SCRIPT:-scripts/ralph_local_supervisor.sh}"
IDLE_SLEEP_SEC="${RALPH_IDLE_SLEEP_SEC:-15}"
TAIL_LINES="${TAIL_LINES:-120}"
TRUST_MODE="${RALPH_LOCAL_TRUST_MODE:-false}"
LOCAL_SANDBOX="${RALPH_LOCAL_SANDBOX:-}"
LOCAL_APPROVAL="${RALPH_LOCAL_APPROVAL:-never}"
LOCAL_OMX_SAFE_MODE="${RALPH_LOCAL_OMX_SAFE_MODE:-}"
REQUIRE_CHATGPT_AUTH="${RALPH_REQUIRE_CHATGPT_AUTH:-true}"
CONNECTIVITY_PREFLIGHT="${RALPH_CONNECTIVITY_PREFLIGHT:-true}"
CONNECTIVITY_TIMEOUT_SEC="${RALPH_CONNECTIVITY_TIMEOUT_SEC:-25}"

ensure_layout() {
  scripts/ralph_local_init.sh >/dev/null
  mkdir -p "${RALPH_ROOT}/logs"
}

cleanup_stray_local_processes() {
  pkill -f "${SUPERVISOR_SCRIPT}" >/dev/null 2>&1 || true
  pkill -f "${RUN_SCRIPT}" >/dev/null 2>&1 || true
  pkill -f "You are executing a local Ralph loop task with no GitHub dependency." >/dev/null 2>&1 || true
}

requeue_in_progress() {
  local src id dst
  [ -d "${RALPH_ROOT}/in-progress" ] || return 0
  for src in "${RALPH_ROOT}/in-progress"/I-*.md; do
    [ -f "${src}" ] || continue
    id="$(basename "${src}")"
    dst="${RALPH_ROOT}/issues/${id}"
    if [ -f "${dst}" ]; then
      dst="${RALPH_ROOT}/issues/requeued-${id}"
    fi
    mv "${src}" "${dst}"
  done
}

is_running() {
  local pid
  [ -f "${PID_FILE}" ] || return 1
  pid="$(cat "${PID_FILE}" 2>/dev/null || true)"
  [ -n "${pid}" ] || return 1
  ps -p "${pid}" >/dev/null 2>&1
}

current_supervisor_pid() {
  pgrep -f "${SUPERVISOR_SCRIPT}" | head -n1 || true
}

effective_local_sandbox() {
  if [ -n "${LOCAL_SANDBOX}" ]; then
    echo "${LOCAL_SANDBOX}"
    return 0
  fi
  if [ "${TRUST_MODE}" = "true" ]; then
    echo "danger-full-access"
    return 0
  fi
  echo "workspace-write"
}

codex_connectivity_preflight() {
  local sandbox="$1"
  [ "${CONNECTIVITY_PREFLIGHT}" = "true" ] || return 0

  local check_log rc model
  check_log="${RALPH_ROOT}/logs/connectivity-$(date -u +%Y%m%dT%H%M%SZ).log"
  model="${PLANNING_CODEX_MODEL:-gpt-5.3-codex}"

  set +e
  timeout "${CONNECTIVITY_TIMEOUT_SEC}" \
    codex --ask-for-approval never exec --model "${model}" --sandbox "${sandbox}" --cd "$(pwd)" \
    "Reply exactly: ok" >"${check_log}" 2>&1
  rc=$?
  set -e

  if [ "${rc}" -eq 0 ]; then
    return 0
  fi

  echo "ralph-local start blocked: Codex connectivity preflight failed (rc=${rc}, sandbox=${sandbox})."
  echo "See log: ${check_log}"
  echo "Hint: start Ralph from an unrestricted host shell/tmux, not from a restricted sandbox."
  return 1
}

start_daemon() {
  local local_sandbox omx_mode daemon_pid

  ensure_layout
  scripts/ralph_local_control.sh on >/dev/null
  if [ "${REQUIRE_CHATGPT_AUTH}" = "true" ] && ! scripts/codex_auth_status.sh --require-chatgpt >/dev/null; then
    echo "ralph-local start blocked: ChatGPT login mode check failed."
    echo "Run: scripts/codex_auth_status.sh"
    return 1
  fi
  local_sandbox="$(effective_local_sandbox)"
  if ! codex_connectivity_preflight "${local_sandbox}"; then
    return 1
  fi

  if is_running; then
    echo "ralph-local already running (pid=$(cat "${PID_FILE}"))"
    return 0
  fi

  requeue_in_progress
  cleanup_stray_local_processes

  omx_mode="${LOCAL_OMX_SAFE_MODE}"
  if [ -z "${omx_mode}" ]; then
    if [ "${TRUST_MODE}" = "true" ]; then
      omx_mode="false"
    else
      omx_mode="true"
    fi
  fi

  nohup env \
    -u OPENAI_API_KEY \
    -u OPENAI_BASE_URL \
    -u OPENAI_API_BASE \
    -u OPENAI_ORGANIZATION \
    -u OPENAI_ORG_ID \
    MAX_LOOPS=0 \
    RALPH_IDLE_SLEEP_SEC="${IDLE_SLEEP_SEC}" \
    RALPH_RUN_SCRIPT="${RUN_SCRIPT}" \
    RALPH_LOCAL_TRUST_MODE="${TRUST_MODE}" \
    RALPH_REQUIRE_CHATGPT_AUTH="${REQUIRE_CHATGPT_AUTH}" \
    AGENT_CODEX_SANDBOX="${local_sandbox}" \
    AGENT_CODEX_APPROVAL="${LOCAL_APPROVAL}" \
    OMX_SAFE_MODE="${omx_mode}" \
    "${SUPERVISOR_SCRIPT}" >>"${LOG_FILE}" 2>&1 < /dev/null &
  for _ in 1 2 3 4 5; do
    daemon_pid="$(current_supervisor_pid)"
    if [ -n "${daemon_pid}" ] && ps -p "${daemon_pid}" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  if [ -n "${daemon_pid}" ] && ps -p "${daemon_pid}" >/dev/null 2>&1; then
    echo "${daemon_pid}" > "${PID_FILE}"
    echo "ralph-local started (pid=${daemon_pid})"
    return 0
  fi

  echo "ralph-local failed to start. recent log:" >&2
  tail -n 40 "${LOG_FILE}" >&2 || true
  return 1
}

stop_daemon() {
  scripts/ralph_local_control.sh off >/dev/null || true

  if ! is_running; then
    requeue_in_progress
    cleanup_stray_local_processes
    rm -f "${PID_FILE}"
    echo "ralph-local is already stopped"
    return 0
  fi

  pid="$(cat "${PID_FILE}")"
  kill "${pid}" >/dev/null 2>&1 || true
  sleep 1
  if ps -p "${pid}" >/dev/null 2>&1; then
    kill -9 "${pid}" >/dev/null 2>&1 || true
  fi
  rm -f "${PID_FILE}"
  requeue_in_progress
  cleanup_stray_local_processes
  echo "ralph-local stopped"
}

show_status() {
  scripts/ralph_local_status.sh
  if is_running; then
    echo "- daemon: running (pid=$(cat "${PID_FILE}"))"
  else
    echo "- daemon: stopped"
  fi
}

tail_logs() {
  ensure_layout
  tail -n "${TAIL_LINES}" -f "${LOG_FILE}"
}

case "${CMD}" in
  start)
    start_daemon
    ;;
  stop)
    stop_daemon
    ;;
  status)
    show_status
    ;;
  tail)
    tail_logs
    ;;
  *)
    echo "Usage: scripts/ralph_local_daemon.sh <start|stop|status|tail>" >&2
    exit 1
    ;;
esac
