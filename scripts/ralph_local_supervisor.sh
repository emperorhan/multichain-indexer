#!/usr/bin/env bash
set -euo pipefail

# Keeps local Ralph runner alive until local control is turned off.
# Usage:
#   scripts/ralph_local_supervisor.sh

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing command: $1" >&2
    exit 2
  fi
}

require_cmd flock

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
STATE_FILE="${RALPH_ROOT}/state.env"
RUN_SCRIPT="${RALPH_RUN_SCRIPT:-scripts/ralph_local_run.sh}"
RESTART_SLEEP_SEC="${RALPH_RESTART_SLEEP_SEC:-3}"
LOCK_BUSY_SLEEP_SEC="${RALPH_LOCK_BUSY_SLEEP_SEC:-10}"
CRASH_BACKOFF_MAX_SEC="${RALPH_CRASH_BACKOFF_MAX_SEC:-60}"
DISABLED_SLEEP_SEC="${RALPH_DISABLED_SLEEP_SEC:-10}"
WAIT_WHEN_DISABLED="${RALPH_SUPERVISOR_WAIT_WHEN_DISABLED:-true}"
SUPERVISOR_LOG="${RALPH_ROOT}/logs/supervisor.out"
SUPERVISOR_LOCK_FILE="${RALPH_ROOT}/supervisor.lock"

mkdir -p "${RALPH_ROOT}/logs"
[ -f "${STATE_FILE}" ] || printf 'RALPH_LOCAL_ENABLED=true\n' > "${STATE_FILE}"
exec 8>"${SUPERVISOR_LOCK_FILE}"
if ! flock -n 8; then
  printf '[ralph-supervisor] another supervisor instance is active; exiting\n' | tee -a "${SUPERVISOR_LOG}" >&2
  exit 0
fi

is_enabled() {
  local v
  v="$(awk -F= '/^RALPH_LOCAL_ENABLED=/{print $2; exit}' "${STATE_FILE}" 2>/dev/null || true)"
  [ "${v}" = "true" ]
}

log() {
  printf '[ralph-supervisor] %s\n' "$*" | tee -a "${SUPERVISOR_LOG}" >&2
}

wait_or_exit_when_disabled() {
  if [ "${WAIT_WHEN_DISABLED}" = "true" ]; then
    log "disabled flag detected; waiting ${DISABLED_SLEEP_SEC}s for re-enable"
    sleep "${DISABLED_SLEEP_SEC}"
    return 0
  fi
  log "disabled flag detected; stopping supervisor"
  exit 0
}

compute_crash_backoff() {
  local crash_count="$1"
  local wait i
  wait="${RESTART_SLEEP_SEC}"
  i=1
  while [ "${i}" -lt "${crash_count}" ]; do
    wait=$((wait * 2))
    if [ "${wait}" -ge "${CRASH_BACKOFF_MAX_SEC}" ]; then
      wait="${CRASH_BACKOFF_MAX_SEC}"
      break
    fi
    i=$((i + 1))
  done
  if [ "${wait}" -lt "${RESTART_SLEEP_SEC}" ]; then
    wait="${RESTART_SLEEP_SEC}"
  fi
  echo "${wait}"
}

crash_count=0

while true; do
  if ! is_enabled; then
    wait_or_exit_when_disabled
    continue
  fi

  log "starting runner"
  set +e
  "${RUN_SCRIPT}"
  runner_rc=$?
  set -e

  if [ "${runner_rc}" -eq 0 ]; then
    crash_count=0
    if ! is_enabled; then
      wait_or_exit_when_disabled
      continue
    fi
    log "runner exited cleanly; restarting in ${RESTART_SLEEP_SEC}s"
    sleep "${RESTART_SLEEP_SEC}"
    continue
  fi

  if ! is_enabled; then
    wait_or_exit_when_disabled
    continue
  fi

  if [ "${runner_rc}" -eq 75 ]; then
    log "runner lock busy; sleeping ${LOCK_BUSY_SLEEP_SEC}s before retry"
    sleep "${LOCK_BUSY_SLEEP_SEC}"
    continue
  fi

  crash_count=$((crash_count + 1))
  sleep_sec="$(compute_crash_backoff "${crash_count}")"
  log "runner crashed rc=${runner_rc}; crash_count=${crash_count}; restarting in ${sleep_sec}s"
  sleep "${sleep_sec}"
done
