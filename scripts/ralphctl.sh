#!/usr/bin/env bash
set -euo pipefail

# Quick operator control wrapper.
# Usage:
#   scripts/ralphctl.sh on|off|status [owner/repo]
#   scripts/ralphctl.sh kick [owner/repo] [max_issues]
#   scripts/ralphctl.sh scout [owner/repo] [max_issues]

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required. Install from https://cli.github.com/" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CMD="${1:-}"
REPO_DEFAULT="$(gh repo view --json nameWithOwner -q .nameWithOwner)"

usage() {
  cat <<'EOF'
Usage:
  ralphctl.sh on [owner/repo]
  ralphctl.sh off [owner/repo]
  ralphctl.sh status [owner/repo]
  ralphctl.sh kick [owner/repo] [max_issues]
  ralphctl.sh scout [owner/repo] [max_issues]
EOF
}

case "${CMD}" in
  on|off|status)
    REPO="${2:-${REPO_DEFAULT}}"
    "${ROOT_DIR}/scripts/toggle_ralph_loop.sh" "${CMD}" "${REPO}"
    ;;
  kick)
    REPO="${2:-${REPO_DEFAULT}}"
    MAX_ISSUES="${3:-1}"
    gh workflow run "Agent Loop" --repo "${REPO}" -f dry_run=false -f max_issues="${MAX_ISSUES}"
    echo "Triggered Agent Loop on ${REPO} (max_issues=${MAX_ISSUES})"
    ;;
  scout)
    REPO="${2:-${REPO_DEFAULT}}"
    MAX_ISSUES="${3:-1}"
    gh workflow run "Issue Scout" --repo "${REPO}" -f dry_run=false -f max_issues="${MAX_ISSUES}"
    echo "Triggered Issue Scout on ${REPO} (max_issues=${MAX_ISSUES})"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
