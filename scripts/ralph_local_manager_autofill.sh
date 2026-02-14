#!/usr/bin/env bash
set -euo pipefail

# Auto-manager for local Ralph loop.
# - Detects product completeness gaps when queue is empty.
# - Creates ready issues so the loop keeps moving.
#
# Usage:
#   scripts/ralph_local_manager_autofill.sh

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
ISSUES_DIR="${RALPH_ROOT}/issues"
LOCK_FILE="${RALPH_ROOT}/manager.lock"
NEW_ISSUE_SCRIPT="${RALPH_NEW_ISSUE_SCRIPT:-scripts/ralph_local_new_issue.sh}"

mkdir -p "${ISSUES_DIR}" "${RALPH_ROOT}/logs"

if [ ! -x "${NEW_ISSUE_SCRIPT}" ]; then
  echo "[ralph-manager] missing new issue script: ${NEW_ISSUE_SCRIPT}" >&2
  exit 2
fi

exec 8>"${LOCK_FILE}"
if ! flock -n 8; then
  echo "[ralph-manager] lock busy; skip"
  exit 0
fi

issue_exists_with_key() {
  local key="$1"
  rg -q "automanager_key:[[:space:]]*${key}" \
    "${RALPH_ROOT}/issues" \
    "${RALPH_ROOT}/in-progress" \
    "${RALPH_ROOT}/done" \
    "${RALPH_ROOT}/blocked" 2>/dev/null
}

base_sepolia_runtime_gap_detected() {
  # Runtime still wired as Solana-only in main/indexer boot path.
  if ! rg -q "Chain:[[:space:]]*model\\.ChainSolana" cmd/indexer/main.go 2>/dev/null; then
    return 1
  fi
  if ! rg -q "solana\\.NewAdapter" cmd/indexer/main.go 2>/dev/null; then
    return 1
  fi

  # Base/EVM runtime adapter and config surface are not present.
  if [ -d "internal/chain/ethereum" ] || [ -d "internal/chain/base" ]; then
    return 1
  fi
  if rg -q "BASE_|ETHEREUM_" internal/config/config.go 2>/dev/null; then
    return 1
  fi

  # Sidecar contract is still Solana-only.
  if rg -q "Decode(Ethereum|Evm|Base).*Batch" proto/sidecar/v1/decoder.proto 2>/dev/null; then
    return 1
  fi

  return 0
}

create_base_runtime_issue() {
  local issue_path issue_id
  issue_path="$("${NEW_ISSUE_SCRIPT}" developer "M6 implement Base Sepolia runtime pipeline wiring" "I-0109,I-0107" p0 high)"
  issue_id="$(basename "${issue_path}" .md)"

  cat > "${issue_path}" <<EOF
id: ${issue_id}
role: developer
status: ready
priority: p0
depends_on: I-0109,I-0107
title: M6 implement Base Sepolia runtime pipeline wiring
complexity: high
---
## Objective
- Deliver true Base Sepolia runtime indexing (not only normalization-unit support), while preserving Solana stability.

## In Scope
- Add Base/EVM chain adapter runtime path under \`internal/chain/\`.
- Extend boot/config surface for Base Sepolia RPC and watched addresses.
- Update pipeline orchestration to run Solana + Base Sepolia ingestion paths deterministically.
- Extend sidecar protobuf/API surface if needed for non-Solana decode input.
- Add integration/regression tests proving Base Sepolia events are fetched -> decoded -> normalized -> ingested end-to-end.

## Out of Scope
- Mainnet rollout and infra deployment policy.

## Acceptance Criteria
- [ ] Base Sepolia path runs in real runtime boot path (not test-only branches).
- [ ] End-to-end tests cover Base Sepolia from fetch to DB persistence.
- [ ] Solana regression remains green.
- [ ] Validation passes: \`make test\`, \`make test-sidecar\`, \`make lint\`.

## Notes
- automanager_key: auto-base-sepolia-runtime-gap
- Trigger reason: runtime is currently Solana-only despite Base Sepolia target.
EOF

  echo "[ralph-manager] created ${issue_id} for Base Sepolia runtime gap"
}

main() {
  if issue_exists_with_key "auto-base-sepolia-runtime-gap"; then
    echo "[ralph-manager] gap issue already exists; skip"
    return 0
  fi

  if base_sepolia_runtime_gap_detected; then
    create_base_runtime_issue
    return 0
  fi

  echo "[ralph-manager] no known gap detected"
  return 0
}

main "$@"
