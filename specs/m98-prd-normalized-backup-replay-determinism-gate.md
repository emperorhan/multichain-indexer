# M98 PRD-Normalized Backup Replay Determinism Gate

## Scope
- Milestone: `M98`
- Execution slices: `M98-S1` (`I-0524`), `M98-S2` (`I-0525`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R4`: deterministic replay.
- `8.4`: replay from persisted normalized artifacts must be deterministic.
- `10`: deterministic replay acceptance behavior.

## Problem Statement
`M97` validated rollback/reorg recovery continuity, but PRD `8.4` also requires deterministic replay behavior from persisted normalized artifacts. This tranche adds explicit evidence contracts so `M98` promotion is blocked unless persisted-backup replay and chain-isolation restart perturbations are reproducible.

## PRD closeout lock (C0093)
- Cross-milestone lock-in checks required before optional refinements resume:
  - `M96`: `DP-0109-M96` in `specs/m96-prd-asset-volatility-closeout.md` must have all required rows with `evidence_present=true` and `outcome=GO`.
  - `M97`: `DP-0109-M97` in `specs/m97-prd-reorg-recovery-determinism-gate.md` must have all required fork/recovery/isolation rows with `outcome=GO`, zero peer deltas where required, and `evidence_present=true`.
  - `M98`: this spec rows must have all required `outcome=GO`, invariant flags true, and required peer deltas zero where required.

## Backup Replay Contract
1. Persisted normalized replay must preserve all mandatory class-path outputs for mandatory chains and produce stable ordered canonical identity.
2. Replay with checkpoint persistence + restart perturbation must preserve:
  - no duplicate canonical IDs,
  - idempotent replay outputs,
  - cursor monotonicity,
  - signed delta conservation.
3. One-chain perturbation while peers continue must remain chain-isolated with zero peer bleed.

Required class-path basis (mandatory baseline from prior PRD closeouts):
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
  - `miner_fee`

## Required Matrices
- Backup replay continuity matrix (`.ralph/reports/I-0524-m98-s1-backup-replay-continuity-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `replay_source`, `permutation`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `evidence_present`, `outcome`, `failure_mode`
- Backup replay class-path coverage matrix (`.ralph/reports/I-0524-m98-s1-backup-class-coverage-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `replay_source`, `peer_chain`, `evidence_present`, `outcome`, `failure_mode`, `notes`
- Backup replay isolation matrix (`.ralph/reports/I-0525-m98-s2-backup-restart-isolation-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`

Required enum/value constraints:
- `outcome` must be `GO` or `NO-GO`.
- `evidence_present=true` is required for all `GO` rows.
- For `outcome=NO-GO`, `failure_mode` must be non-empty.
- For required `peer` rows with `GO`, `peer_cursor_delta=0` and `peer_watermark_delta=0`.

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `chain_adapter_runtime_wired`
- `reorg_recovery_deterministic`

## Measurable Exit Gates
1. `0` missing required class-path rows in the backup replay continuity and class-coverage matrices for mandatory chains.
2. `0` duplicate canonical IDs, replay drift, or cursor/monotonicity violations across required replay permutations.
3. Required peer-isolation rows report zero peer deltas for perturbation families.
4. Validation commands remain unchanged: `make test`, `make test-sidecar`, `make lint`.

## Decision Hook
- `DP-0110-M98`: any required `(chain, network, class_path)` missing evidence, any required row with `outcome=NO-GO`, any required row with `evidence_present=false`, or any required peer-delta row with non-zero movement is a hard NO-GO for `M98` promotion.
- `DP-0111-C0093`: any required `M96`/`M97`/`M98` closeout row missing, `evidence_present=false`, `outcome=NO-GO`, or required peer-delta row with non-zero movement is a hard NO-GO for optional refinement progression.
