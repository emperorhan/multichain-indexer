#!/usr/bin/env bash
set -euo pipefail

# Keeps local Ralph runner alive until local control is turned off.
# Usage:
#   scripts/ralph_local_supervisor.sh

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
STATE_FILE="${RALPH_ROOT}/state.env"
RUN_SCRIPT="${RALPH_RUN_SCRIPT:-scripts/ralph_local_run.sh}"
RESTART_SLEEP_SEC="${RALPH_RESTART_SLEEP_SEC:-3}"
SUPERVISOR_LOG="${RALPH_ROOT}/logs/supervisor.out"

mkdir -p "${RALPH_ROOT}/logs"
[ -f "${STATE_FILE}" ] || printf 'RALPH_LOCAL_ENABLED=true\n' > "${STATE_FILE}"

is_enabled() {
  local v
  v="$(awk -F= '/^RALPH_LOCAL_ENABLED=/{print $2; exit}' "${STATE_FILE}" 2>/dev/null || true)"
  [ "${v}" = "true" ]
}

log() {
  printf '[ralph-supervisor] %s\n' "$*" | tee -a "${SUPERVISOR_LOG}" >&2
}

while true; do
  if ! is_enabled; then
    log "disabled flag detected; stopping supervisor"
    exit 0
  fi

  log "starting runner"
  if "${RUN_SCRIPT}"; then
    if ! is_enabled; then
      log "runner finished and disabled flag is set; exiting"
      exit 0
    fi
    log "runner exited cleanly; restarting in ${RESTART_SLEEP_SEC}s"
    sleep "${RESTART_SLEEP_SEC}"
    continue
  fi

  if ! is_enabled; then
    log "runner failed but disabled flag is set; exiting"
    exit 0
  fi

  log "runner crashed; restarting in ${RESTART_SLEEP_SEC}s"
  sleep "${RESTART_SLEEP_SEC}"
done
