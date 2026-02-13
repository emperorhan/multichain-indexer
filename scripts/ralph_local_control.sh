#!/usr/bin/env bash
set -euo pipefail

# Control local-only Ralph loop state.
# Usage:
#   scripts/ralph_local_control.sh on|off|status

MODE="${1:-}"
RALPH_ROOT="${RALPH_ROOT:-.ralph}"
STATE_FILE="${RALPH_ROOT}/state.env"

if [ "${MODE}" != "on" ] && [ "${MODE}" != "off" ] && [ "${MODE}" != "status" ]; then
  echo "Usage: scripts/ralph_local_control.sh <on|off|status>" >&2
  exit 1
fi

mkdir -p "${RALPH_ROOT}"
if [ ! -f "${STATE_FILE}" ]; then
  cat >"${STATE_FILE}" <<'EOF'
RALPH_LOCAL_ENABLED=true
EOF
fi

current_value() {
  awk -F= '/^RALPH_LOCAL_ENABLED=/{print $2; exit}' "${STATE_FILE}" 2>/dev/null || true
}

if [ "${MODE}" = "status" ]; then
  value="$(current_value)"
  [ -n "${value}" ] || value="unset"
  echo "RALPH_LOCAL_ENABLED=${value}"
  exit 0
fi

if [ "${MODE}" = "on" ]; then
  printf 'RALPH_LOCAL_ENABLED=true\n' >"${STATE_FILE}"
  echo "local ralph loop enabled"
  exit 0
fi

printf 'RALPH_LOCAL_ENABLED=false\n' >"${STATE_FILE}"
echo "local ralph loop disabled"
