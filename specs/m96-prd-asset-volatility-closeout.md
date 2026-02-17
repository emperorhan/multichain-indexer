# M96 PRD-Traceable Asset-Volatility Closeout Gate

## Scope
- Milestone: `M96`
- Execution slice: `M96-S1`
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R1`: no-duplicate indexing.
- `R2`: full asset-volatility coverage.
- `R3`: chain-family fee completeness.
- `8`: replay and recovery continuity expectations.
- `9.4`: topology parity and continuity principles.
- `10`: deterministic replay acceptance behavior.

## Problem Statement
`I-0512` must close the remaining PRD class-coverage/replay gap by defining explicit, machine-checkable gates for mandatory-chain class completeness **before** optional reliability work.

## Required Coverage Matrix (Mandatory-Chain Class Cells)
Required cells must be present with `evidence_present=true` in the `M96` coverage artifacts.

1. `solana-devnet`:
- `TRANSFER`
- `MINT`
- `BURN`
- `FEE`
2. `base-sepolia`:
- `TRANSFER`
- `MINT`
- `BURN`
- `fee_execution_l2`
- `fee_data_l1`
3. `btc-testnet`:
- `TRANSFER` (`vin` and `vout` paths are represented explicitly)
- `miner_fee` (or deterministic miner-fee conservation delta where transfer-path encoding is the canonical owner)

## Deterministic Duplicate-Risk Gates
- `event_id` generation and tuple ordering for all required class-path cells must be deterministic for replayed fixture permutations.
- Required replay permutations:
  - canonical range replay
  - replay-order swap
  - one-chain restart perturbation
- All three permutations must report:
  - `canonical_event_id_unique=true`
  - `replay_idempotent=true`
  - `cursor_monotonic=true`
  - `signed_delta_conservation=true`

## Evidence Artifacts (to be produced by downstream `I-0515`/`I-0516`)
- `.ralph/reports/I-0515-m96-s1-class-coverage-matrix.md`
- `.ralph/reports/I-0515-m96-s1-dup-suppression-matrix.md`
- `.ralph/reports/I-0516-m96-s1-replay-continuity-matrix.md`
- `.ralph/reports/I-0516-m96-s1-chain-isolation-matrix.md`

## Required Evidence Fields
Minimum keys per matrix row:
- `fixture_id`
- `fixture_seed`
- `run_id`
- `chain`
- `network`
- `class_path`
- `peer_chain`
- `evidence_present`
- `canonical_event_id_unique_ok`
- `replay_idempotent_ok`
- `cursor_monotonic_ok`
- `signed_delta_conservation_ok`
- `peer_cursor_delta`
- `peer_watermark_delta`
- `outcome`
- `failure_mode`

`outcome=GO` requires all required gates above to be true and both peer deltas equal `0`.

## Measurable Exit Gates
1. `0` missing required matrix cells for non-`NA` class-paths in `solana-devnet`, `base-sepolia`, and `btc-testnet`.
2. `0` duplicate canonical IDs across required replay permutations in `I-0516-m96-s1-replay-continuity-matrix.md`.
3. Replay permutation checks (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`) are true for all required rows.
4. Peer isolation checks report `peer_cursor_delta=0` and `peer_watermark_delta=0` for all required rows.
5. Validation commands remain unchanged and must pass: `make test`, `make test-sidecar`, `make lint`.

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `chain_adapter_runtime_wired`

## Decision Hook
- `DP-0109-M96`: Any required class-path cell missing evidence, or any required row with `outcome=NO-GO`/invalid fail-mode semantics, is a hard NO-GO for milestone promotion.
