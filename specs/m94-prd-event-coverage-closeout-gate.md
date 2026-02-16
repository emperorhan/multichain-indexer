# M94 PRD-Priority Event-Coverage + Duplicate-Free Closeout Gate

## Scope
- Milestone: `M94`
- Execution slices: `M94-S1` (`I-0491`), `M94-S2` (`I-0492`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R1`: no-duplicate indexing.
- `R2`: full in-scope asset-volatility event coverage.
- `R3`: chain-family fee completeness.
- `9.4`: parity/continuity validation principles.
- `10`: deterministic replay and no cross-chain cursor bleed acceptance.

## Problem Statement
After PRD `M93` continuity hardening, the current loop still needs an explicit closeout gate proving every in-scope asset-volatility class (`transfer`, `mint`, `burn`, fee classes) is represented as deterministic signed deltas and that duplicate class outputs are impossible for mandatory chains.

## Reliability Contract
1. For equivalent chain fixtures, each required event class yields deterministic canonical signatures and appears exactly once when ownership is unambiguous.
2. `solana-devnet`, `base-sepolia`, and `btc-testnet` cover full transfer/mint/burn/fee path classes without class omission.
3. Replay and restart from committed boundaries preserve canonical event sets and materialized balances for the required coverage classes.
4. Counterexample evidence includes missing-class and duplicate-class detections before promotion.

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `solana_fee_event_coverage`
- `base_fee_split_coverage`
- `chain_adapter_runtime_wired`

## Measurable Exit Gates
1. `0` duplicate canonical IDs in required coverage fixture families (`transfer`, `mint`, `burn`, fee classes).
2. `0` missing required class rows for mandatory chains in deterministic matrices.
3. `0` replay tuple/balance drift in restart-recover permutations for coverage fixtures.
4. `0` cross-chain control/cursor bleed in one-chain perturbation counterexamples.
5. Validation commands pass: `make test`, `make test-sidecar`, `make lint`.

## Test Matrix
1. Event-class matrix:
- by chain family (`solana`, `base`, `btc`) and class (`transfer`, `mint`, `burn`, `fee`), asserting deterministic class presence and canonical id uniqueness.
2. Duplicate suppression matrix:
- replay rerun and alternate fixture ordering to assert no duplicates per event class.
3. Replay continuity matrix:
- committed-boundary restart/recover fixtures with tuple and balance convergence checks.
4. Peer-progress matrix:
- one-chain perturbation under peer-chain progress; assert no cursor/watermark bleed.

## Exit Recommendation
This tranche gates promotion to `M95` and any optional reliability refinements. A promotion to `M94` requires deterministic evidence artifacts under `.ralph/reports/` and explicit matrix-completeness evidence.
