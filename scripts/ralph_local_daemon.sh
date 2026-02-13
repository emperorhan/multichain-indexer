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
TRUST_MODE="${RALPH_LOCAL_TRUST_MODE:-true}"
LOCAL_SANDBOX="${RALPH_LOCAL_SANDBOX:-danger-full-access}"
LOCAL_APPROVAL="${RALPH_LOCAL_APPROVAL:-never}"
LOCAL_OMX_SAFE_MODE="${RALPH_LOCAL_OMX_SAFE_MODE:-}"
REQUIRE_CHATGPT_AUTH="${RALPH_REQUIRE_CHATGPT_AUTH:-true}"

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

start_daemon() {
  ensure_layout
  scripts/ralph_local_control.sh on >/dev/null
  if [ "${REQUIRE_CHATGPT_AUTH}" = "true" ] && ! scripts/codex_auth_status.sh --require-chatgpt >/dev/null; then
    echo "ralph-local start blocked: ChatGPT login mode check failed."
    echo "Run: scripts/codex_auth_status.sh"
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

  if command -v setsid >/dev/null 2>&1; then
    nohup setsid env \
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
      AGENT_CODEX_SANDBOX="${LOCAL_SANDBOX}" \
      AGENT_CODEX_APPROVAL="${LOCAL_APPROVAL}" \
      OMX_SAFE_MODE="${omx_mode}" \
      "${SUPERVISOR_SCRIPT}" >>"${LOG_FILE}" 2>&1 < /dev/null &
  else
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
      AGENT_CODEX_SANDBOX="${LOCAL_SANDBOX}" \
      AGENT_CODEX_APPROVAL="${LOCAL_APPROVAL}" \
      OMX_SAFE_MODE="${omx_mode}" \
      "${SUPERVISOR_SCRIPT}" >>"${LOG_FILE}" 2>&1 < /dev/null &
  fi
  daemon_pid=$!
  echo "${daemon_pid}" > "${PID_FILE}"
  sleep 1

  if ps -p "${daemon_pid}" >/dev/null 2>&1; then
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
