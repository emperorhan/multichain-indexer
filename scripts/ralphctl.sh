#!/usr/bin/env bash
set -euo pipefail

# Quick operator control wrapper (local-first).
# Usage:
#   scripts/ralphctl.sh on|off|status
#   scripts/ralphctl.sh kick|scout|agents|tail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CMD="${1:-}"
RUNTIME_STATUS_CMD="${ROOT_DIR}/scripts/ralph_local_runtime_status.sh"
DAEMON_CMD="${ROOT_DIR}/scripts/ralph_local_daemon.sh"
AUTOMANAGER_CMD="${ROOT_DIR}/scripts/ralph_local_manager_autofill.sh"
AGENT_TRACKER_CMD="${ROOT_DIR}/scripts/ralph_local_agent_tracker.sh"

if [ "$#" -gt 1 ]; then
  echo "note: GitHub repo/max_issues arguments are ignored in local mode." >&2
fi

usage() {
  cat <<'EOF'
Usage:
  ralphctl.sh on
  ralphctl.sh off
  ralphctl.sh status
  ralphctl.sh kick
  ralphctl.sh scout
  ralphctl.sh agents
  ralphctl.sh tail
EOF
}

case "${CMD}" in
  on|start)
    "${DAEMON_CMD}" start
    ;;
  off|stop)
    "${DAEMON_CMD}" stop
    ;;
  status)
    "${RUNTIME_STATUS_CMD}"
    ;;
  kick)
    "${DAEMON_CMD}" start
    "${RUNTIME_STATUS_CMD}"
    ;;
  scout)
    "${AUTOMANAGER_CMD}"
    "${RUNTIME_STATUS_CMD}"
    ;;
  agents)
    "${AGENT_TRACKER_CMD}"
    ;;
  tail)
    "${DAEMON_CMD}" tail
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
