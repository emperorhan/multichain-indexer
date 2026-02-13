#!/usr/bin/env bash
set -euo pipefail

# Disables branch protection for direct main push workflow.
# Usage:
#   scripts/enable_direct_main_push.sh [owner/repo] [branch]
#
# WARNING:
# - This removes GitHub branch protection on the target branch.
# - Use only for trusted solo/local automation scenarios.

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required. Install from https://cli.github.com/" >&2
  exit 1
fi

REPO="${1:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}"
BRANCH="${2:-main}"

echo "Disabling protection on ${REPO}:${BRANCH}"
gh api -X DELETE "repos/${REPO}/branches/${BRANCH}/protection" >/dev/null
echo "Branch protection removed for ${REPO}:${BRANCH}"

echo "Recommended local loop env:"
echo "  RALPH_BRANCH_STRATEGY=main"
echo "  RALPH_AUTO_PUBLISH_TARGET_BRANCH=${BRANCH}"
