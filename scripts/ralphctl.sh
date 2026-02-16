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
PROFILE_CMD="${ROOT_DIR}/scripts/ralph_local_profile.sh"

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
  ralphctl.sh profile [list|status|set <절약|최적|퍼포먼스>]
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
  profile|mode)
    shift || true
    if [ "$#" -eq 0 ]; then
      "${PROFILE_CMD}" status
    else
      "${PROFILE_CMD}" "$@"
    fi
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
