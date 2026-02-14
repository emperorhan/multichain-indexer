#!/usr/bin/env bash
set -euo pipefail

# Runs deterministic counterexample-oriented checks for QA tasks.
# Usage:
#   QA_CHAIN=solana-devnet scripts/qa_counterexample_checks.sh

QA_CHAIN="${QA_CHAIN:-solana-devnet}"
QA_CHAIN="$(tr '[:upper:]' '[:lower:]' <<<"${QA_CHAIN}")"

echo "[qa-counterexample] chain=${QA_CHAIN}"

# Canonical/idempotency invariants in core model + normalizer.
go test ./internal/domain/model -run 'Test.*Canonical|Test.*Chain' -count=1
go test ./internal/pipeline/normalizer -run 'Test.*Canonical|Test.*Fee|Test.*Dedup|Test.*EventID' -count=1

case "${QA_CHAIN}" in
  solana|solana-devnet)
    go test ./internal/chain/solana -count=1
    ;;
  base|base-sepolia|evm)
    go test ./internal/chain/base -count=1
    ;;
  *)
    echo "unsupported QA_CHAIN=${QA_CHAIN}" >&2
    exit 2
    ;;
esac

echo "[qa-counterexample] passed"
