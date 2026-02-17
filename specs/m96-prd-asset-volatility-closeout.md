# M96 PRD-Traceable Asset-Volatility Closeout Gate

## Scope
- Milestone: `M96`
- Execution slice: `M96-S1`
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R1`: no-duplicate indexing.
- `R2`: full asset-volatility coverage.
- `R3`: chain-family fee completeness.
- `8.4`/`8.5`: failed-path continuity and fail-fast cursor/watermark safety.
- `9.4`: topology parity and continuity principles.
- `10`: deterministic replay acceptance behavior.

## Problem Statement
`I-0517` must close the remaining PRD class-coverage/replay gap by defining explicit, machine-checkable gates for mandatory-chain class completeness **before** optional reliability work.

## Coverage Matrix Contract (Mandatory-Chain Class Cells)
All required matrix rows must be present and set `evidence_present=true` in the required `M96` artifacts.

Mandatory rows:
- `solana-devnet`:
  - `TRANSFER`
  - `MINT`
  - `BURN`
  - `FEE`
- `base-sepolia`:
  - `TRANSFER`
  - `MINT`
  - `BURN`
  - `fee_execution_l2`
  - `fee_data_l1`
- `btc-testnet`:
  - `TRANSFER:vin`
  - `TRANSFER:vout`
  - `miner_fee` (canonical BTC miner-fee conservation row)

## Deterministic Replay and Duplicate-Risk Gates
- `event_id` generation and tuple ordering for all required class-path cells must be deterministic for replayed fixture permutations.
- Required permutation axis (exact enum):
  - `canonical_range_replay`
  - `replay_order_swap`
  - `one_chain_restart_perturbation`
- Every required row in the replay/duplication families must report:
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`

## Evidence Artifacts (to be produced by downstream `I-0542`/`I-0543`)
- `.ralph/reports/I-0518-m96-s1-class-coverage-matrix.md`
- `.ralph/reports/I-0518-m96-s1-dup-suppression-matrix.md`
- `.ralph/reports/I-0519-m96-s1-replay-continuity-matrix.md`
- `.ralph/reports/I-0519-m96-s1-chain-isolation-matrix.md`
- `.ralph/reports/I-0542-evidence.md`

## Machine-Checkable Evidence Schema
- Class-coverage matrix rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `outcome`, `failure_mode`, `notes`
- Duplicate-suppression matrix rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `canonical_id_count`, `evidence_present`, `outcome`, `failure_mode`
- Replay-continuity matrix rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `evidence_present`, `outcome`, `failure_mode`
- Chain-isolation matrix rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`

Required enum/value constraints:
- `outcome` must be `GO` or `NO-GO`.
- `evidence_present=true` is required for all `GO` rows.
- For `outcome=NO-GO`, `failure_mode` must be non-empty.
- `outcome=GO` requires all required gates true for that matrix row and `peer_cursor_delta=0`, `peer_watermark_delta=0` where those fields exist.

## Measurable Exit Gates
1. `0` missing required matrix cells for non-`NA` class-paths in `solana-devnet`, `base-sepolia`, and `btc-testnet`.
2. `0` duplicate canonical IDs across required replay permutations in `I-0519-m96-s1-replay-continuity-matrix.md`.
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
- `DP-0119-C0097`: Any required row with `outcome=NO-GO`, `evidence_present=false`, `peer_cursor_delta!=0`, or `peer_watermark_delta!=0` blocks promotion into optional post-PRD continuation.
