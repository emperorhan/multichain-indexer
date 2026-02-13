#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   scripts/setup_branch_protection.sh [owner/repo] [branch]
# Example:
#   scripts/setup_branch_protection.sh emperorhan/multichain-indexer main

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required. Install from https://cli.github.com/" >&2
  exit 1
fi

REPO="${1:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}"
BRANCH="${2:-main}"

echo "Applying branch protection to ${REPO}:${BRANCH}"

gh api \
  --method PUT \
  "repos/${REPO}/branches/${BRANCH}/protection" \
  -H "Accept: application/vnd.github+json" \
  -F required_status_checks.strict=true \
  -F required_status_checks.contexts[]="Go Test + Build" \
  -F required_status_checks.contexts[]="Go Lint" \
  -F required_status_checks.contexts[]="Sidecar Test + Build" \
  -F enforce_admins=true \
  -F required_pull_request_reviews.dismiss_stale_reviews=true \
  -F required_pull_request_reviews.require_code_owner_reviews=true \
  -F required_pull_request_reviews.required_approving_review_count=1 \
  -F restrictions= \
  -F allow_force_pushes=false \
  -F allow_deletions=false \
  -F block_creations=false \
  -F required_linear_history=true

echo "Done."

