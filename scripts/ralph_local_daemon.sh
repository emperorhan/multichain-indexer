#!/usr/bin/env bash
set -euo pipefail

# Manage local Ralph loop as a background daemon.
# Usage:
#   scripts/ralph_local_daemon.sh start|stop|status|tail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

load_profile_env() {
  local profile_root profile_file
  profile_root="${RALPH_ROOT:-.ralph}"
  profile_file="${RALPH_PROFILE_FILE:-${profile_root}/profile.env}"
  if [ -f "${profile_file}" ]; then
    # shellcheck source=/dev/null
    . "${profile_file}"
  fi
}

load_profile_env

CMD="${1:-status}"
RALPH_ROOT="${RALPH_ROOT:-.ralph}"
PID_FILE="${RALPH_ROOT}/runner.pid"
LOG_FILE="${RALPH_ROOT}/logs/runner.out"
RUN_SCRIPT="${RALPH_RUN_SCRIPT:-${ROOT_DIR}/scripts/ralph_local_run.sh}"
SUPERVISOR_SCRIPT="${RALPH_SUPERVISOR_SCRIPT:-${ROOT_DIR}/scripts/ralph_local_supervisor.sh}"
INIT_SCRIPT="${ROOT_DIR}/scripts/ralph_local_init.sh"
CONTROL_SCRIPT="${ROOT_DIR}/scripts/ralph_local_control.sh"
AUTH_STATUS_SCRIPT="${ROOT_DIR}/scripts/codex_auth_status.sh"
RUNTIME_STATUS_SCRIPT="${ROOT_DIR}/scripts/ralph_local_runtime_status.sh"
LOCAL_STATUS_SCRIPT="${ROOT_DIR}/scripts/ralph_local_status.sh"
SERVICE_NAME="${RALPH_LOCAL_SERVICE_NAME:-ralph-local.service}"
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
  "${INIT_SCRIPT}" >/dev/null
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

supports_systemd_user() {
  [ "${RALPH_LOCAL_USE_SYSTEMD:-true}" = "true" ] || return 1
  command -v systemctl >/dev/null 2>&1 || return 1
}

systemd_unit_exists() {
  supports_systemd_user || return 1
  systemctl --user cat "${SERVICE_NAME}" >/dev/null 2>&1
}

use_systemd_control() {
  systemd_unit_exists
}

is_running_systemd() {
  systemctl --user is-active --quiet "${SERVICE_NAME}" >/dev/null 2>&1
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
  "${CONTROL_SCRIPT}" on >/dev/null
  if [ "${REQUIRE_CHATGPT_AUTH}" = "true" ] && ! "${AUTH_STATUS_SCRIPT}" --require-chatgpt >/dev/null; then
    echo "ralph-local start blocked: ChatGPT login mode check failed."
    echo "Run: ${AUTH_STATUS_SCRIPT}"
    return 1
  fi
  local_sandbox="$(effective_local_sandbox)"
  if ! codex_connectivity_preflight "${local_sandbox}"; then
    return 1
  fi

  if use_systemd_control; then
    if systemctl --user cat "${SERVICE_NAME}" >/dev/null 2>&1; then
      cleanup_stray_local_processes
      rm -f "${PID_FILE}"
      if is_running_systemd; then
        echo "ralph-local already running (service=${SERVICE_NAME})"
        return 0
      fi
      if systemctl --user start "${SERVICE_NAME}" >/dev/null 2>&1; then
        if is_running_systemd; then
          echo "ralph-local started (service=${SERVICE_NAME})"
          return 0
        fi
      fi
      echo "ralph-local failed to start service=${SERVICE_NAME}; falling back to local supervisor mode." >&2
      systemctl --user status "${SERVICE_NAME}" --no-pager >&2 || true
    fi
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
  "${CONTROL_SCRIPT}" off >/dev/null || true

  if use_systemd_control; then
    if systemctl --user cat "${SERVICE_NAME}" >/dev/null 2>&1; then
      systemctl --user stop "${SERVICE_NAME}" >/dev/null 2>&1 || true
      requeue_in_progress
      cleanup_stray_local_processes
      rm -f "${PID_FILE}"
      if is_running_systemd; then
        echo "ralph-local stop requested but service is still active"
        return 1
      fi
      echo "ralph-local stopped (service=${SERVICE_NAME})"
      return 0
    fi
  fi

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
  if use_systemd_control; then
    if systemctl --user cat "${SERVICE_NAME}" >/dev/null 2>&1; then
      "${RUNTIME_STATUS_SCRIPT}"
      if is_running_systemd; then
        echo "- control_mode: systemd (${SERVICE_NAME})"
      else
        echo "- control_mode: systemd (${SERVICE_NAME}, inactive)"
      fi
      return 0
    fi
  fi

  "${LOCAL_STATUS_SCRIPT}"
  if is_running; then
    echo "- daemon: running (pid=$(cat "${PID_FILE}" 2>/dev/null || echo unknown))"
  else
    echo "- daemon: stopped"
  fi
}

tail_logs() {
  ensure_layout
  if use_systemd_control; then
    if systemctl --user cat "${SERVICE_NAME}" >/dev/null 2>&1; then
      journalctl --user -u "${SERVICE_NAME}" -n "${TAIL_LINES}" -f
      return 0
    fi
  fi
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
