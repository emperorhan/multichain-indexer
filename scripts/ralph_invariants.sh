#!/usr/bin/env bash
set -euo pipefail

# Canonical invariants registry for Ralph local contract validation.
# Usage:
#   scripts/ralph_invariants.sh list
#   scripts/ralph_invariants.sh csv
#   scripts/ralph_invariants.sh validate "a,b,c"

INVARIANTS=(
  "canonical_event_id_unique"
  "replay_idempotent"
  "cursor_monotonic"
  "solana_fee_event_coverage"
  "base_fee_split_coverage"
  "reorg_recovery_deterministic"
  "chain_adapter_runtime_wired"
)

cmd="${1:-list}"
shift || true

trim() {
  awk '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); print}'
}

contains_invariant() {
  local needle="$1"
  local item
  for item in "${INVARIANTS[@]}"; do
    if [ "${item}" = "${needle}" ]; then
      return 0
    fi
  done
  return 1
}

case "${cmd}" in
  list)
    printf '%s\n' "${INVARIANTS[@]}"
    ;;
  csv)
    (
      IFS=,
      printf '%s\n' "${INVARIANTS[*]}"
    )
    ;;
  validate)
    raw="${1:-}"
    if [ -z "${raw}" ]; then
      echo "invariants csv is required" >&2
      exit 2
    fi
    invalid=0
    while IFS= read -r token; do
      token="$(trim <<<"${token}")"
      [ -n "${token}" ] || continue
      if ! contains_invariant "${token}"; then
        echo "unknown invariant: ${token}" >&2
        invalid=$((invalid + 1))
      fi
    done < <(printf '%s\n' "${raw}" | tr ',' '\n')
    if [ "${invalid}" -gt 0 ]; then
      exit 1
    fi
    ;;
  *)
    echo "Usage: $0 <list|csv|validate>" >&2
    exit 2
    ;;
esac
