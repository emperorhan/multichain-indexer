#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   scripts/toggle_ralph_loop.sh on [owner/repo]
#   scripts/toggle_ralph_loop.sh off [owner/repo]

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required. Install from https://cli.github.com/" >&2
  exit 1
fi

MODE="${1:-}"
REPO="${2:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}"

if [ "${MODE}" != "on" ] && [ "${MODE}" != "off" ]; then
  echo "Usage: scripts/toggle_ralph_loop.sh <on|off> [owner/repo]" >&2
  exit 1
fi

if [ "${MODE}" = "on" ]; then
  gh variable set RALPH_LOOP_ENABLED --repo "${REPO}" --body "true" >/dev/null
  echo "RALPH loop enabled on ${REPO}"
  exit 0
fi

gh variable set RALPH_LOOP_ENABLED --repo "${REPO}" --body "false" >/dev/null
echo "RALPH loop disabled on ${REPO}"
