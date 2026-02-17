# M98 PRD-Normalized Backup Replay Determinism Gate

## Scope
- Milestone: `M98`
- Execution slices: `M98-S1` (`I-0524`), `M98-S2` (`I-0525`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R4`: deterministic replay.
- `8.4`: replay from persisted normalized artifacts must be deterministic.
- `10`: deterministic replay acceptance behavior.
- `I-0545`/`I-0546` (`C0098`) handoff: residual PRD replay/recovery hardening for `R4`, `8.4`, and `10` with mandatory perturbation coverage.

## Problem Statement
`M97` validated rollback/reorg recovery continuity, but PRD `8.4` also requires deterministic replay behavior from persisted normalized artifacts. This tranche adds explicit evidence contracts so `M98` promotion is blocked unless persisted-backup replay and chain-isolation restart perturbations are reproducible.

## PRD closeout lock (C0093)
- Cross-milestone lock-in checks required before optional refinements resume:
  - `M96`: `DP-0109-M96` in `specs/m96-prd-asset-volatility-closeout.md` must have all required rows with `evidence_present=true` and `outcome=GO`.
  - `M97`: `DP-0109-M97` in `specs/m97-prd-reorg-recovery-determinism-gate.md` must have all required fork/recovery/isolation rows with `outcome=GO`, zero peer deltas where required, and `evidence_present=true`.
  - `M98`: this spec rows must have all required `outcome=GO`, invariant flags true, and required peer deltas zero where required.
- `DP-0114-C0098`: required C0098 artifacts for `I-0545`/`I-0546` remain hard blockers for optional refinement continuation.

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

## C0098 Matrix Contracts (`I-0545` / `I-0546`)
- Required perturbation families for replay source permutations:
  - `persisted_checkpoint_restart`
  - `persisted_checkpoint_restart_with_cross_chain_stress`
  - `backup_replay_reproducibility_seeded`
- Backup replay continuity matrix (`.ralph/reports/I-0545-m98-s1-backup-replay-continuity-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `replay_source`, `permutation`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `evidence_present`, `outcome`, `failure_mode`
- Backup replay class-path coverage matrix (`.ralph/reports/I-0545-m98-s1-backup-class-coverage-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `replay_source`, `peer_chain`, `evidence_present`, `outcome`, `failure_mode`, `notes`
  - hard-stop columns when `outcome=GO`: `canonical_event_id_unique_ok=true`, `replay_idempotent_ok=true`, `cursor_monotonic_ok=true`, `signed_delta_conservation_ok=true`, `evidence_present=true`, `outcome=GO`.
- Backup replay isolation matrix (`.ralph/reports/I-0546-m98-s2-backup-restart-isolation-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`
- Hard-stop columns for required isolation rows: `peer_cursor_delta=0`, `peer_watermark_delta=0`, `outcome=GO`, `evidence_present=true`.

Required enum/value constraints:
- `outcome` must be `GO` or `NO-GO`.
- `evidence_present=true` is required for all `GO` rows.
- For `outcome=NO-GO`, `failure_mode` must be non-empty.
- For required `peer` rows with `GO`, `peer_cursor_delta=0` and `peer_watermark_delta=0`.

## C0099 PRD Closeout Transition Handoff

## PRD Traceability
- `8.4`: deterministic replay from persisted normalized artifacts and safe replay boundaries.
- `8.5`: no failed-path cursor/watermark progression on abort path.
- `10`: deterministic replay and peer-isolation acceptance under perturbation.

## C0099 Transition Blockers
- Required `I-0545`/`I-0546` evidence families for closeout transition unblocking:
  - `.ralph/reports/I-0545-m98-s1-backup-replay-continuity-matrix.md`
  - `.ralph/reports/I-0545-m98-s1-backup-class-coverage-matrix.md`
  - `.ralph/reports/I-0546-m98-s2-backup-restart-isolation-matrix.md`
- Transition blockers for optional-refinement unlock and PRD gate alignment:
  - `8.4` requires every required backup continuity/class-coverage row in C0098 outputs to stay in `GO` and carry `evidence_present=true`.
  - `8.5` requires failed-path replay continuity and restart perturbation rows to keep peer-chain bleed at zero where peer deltas are reported (`peer_cursor_delta=0`, `peer_watermark_delta=0`).
  - `10` requires every required replay proof row to preserve deterministic invariants (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`) as `true` when columns are present.
- Required hard-stop for each required row in the C0098 evidence families:
  - `outcome` must be `GO`.
  - `evidence_present` must be `true`.
  - `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, and `signed_delta_conservation_ok` must be `true` for rows exposing them.
  - `peer_cursor_delta` and `peer_watermark_delta` must be `0` where those columns exist.
  - For any `outcome=NO-GO`, `failure_mode` must be populated.
- `DP-0115-C0099`: any required transition row violating the above (including missing evidence) blocks optional-refinement unblocking.

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
- `DP-0114-C0098`: any required `I-0545`/`I-0546` row missing, with `outcome=NO-GO`, `evidence_present=false`, non-empty `failure_mode` missing on `NO-GO`, or non-zero required peer deltas is a hard NO-GO for C0098 and optional refinement continuation.

## C0113 (`I-0602`) revalidation addendum
- C0113 lock state: `C0113-PRD-BACKUP-RESTART-CONTINUITY-COUNTEREXAMPLE`.
- C0113 focused PRD traceability:
  - `R4`: deterministic replay.
  - `8.4`: rollback/restart replay determinism from committed boundaries.
  - `8.5`: failed-path cursor/watermark is prohibited from advancing on correctness-impacting path.
  - `10`: one-chain perturbation isolation + deterministic replay acceptance.
  - `reorg_recovery_deterministic`: persisted checkpoint replay remains deterministic under fork/recovery permutations.
  - `chain_adapter_runtime_wired`: adapter/runtime invariance under persisted-backup perturbation.
- C0113 downstream handoff (`I-0599 -> I-0602 -> I-0603`) requires three C0113 artifacts:
  - `.ralph/reports/I-0602-m98-s1-backup-replay-continuity-matrix.md`
  - `.ralph/reports/I-0602-m98-s2-backup-class-coverage-matrix.md`
  - `.ralph/reports/I-0602-m98-s3-backup-restart-isolation-matrix.md`

### C0113 Matrix Contracts (`I-0602`)
- `I-0602-m98-s1-backup-replay-continuity-matrix.md` required columns:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `replay_source`, `permutation`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0602-m98-s2-backup-class-coverage-matrix.md` required columns:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `outcome`, `failure_mode`
- `I-0602-m98-s3-backup-restart-isolation-matrix.md` required columns:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`
- Mandatory one-chain peer-isolation constraints for C0113 replay perturbation rows:
  - `solana`/`devnet`: `peer_chain=base-sepolia,btc-testnet`
  - `base`/`sepolia`: `peer_chain=solana-devnet,btc-testnet`
  - `btc`/`testnet`: `peer_chain=solana-devnet,base-sepolia`
- Required `C0113` perturbation families for C0113 replay continuity rows:
  - `persisted_checkpoint_restart`
  - `persisted_checkpoint_restart_with_cross_chain_stress`
  - `backup_replay_reproducibility_seeded`
- `C0113` hard-stop checks for required `GO` rows by artifact:
  - `I-0602-m98-s1-backup-replay-continuity-matrix.md`: `outcome=GO`, `evidence_present=true`, and all invariant booleans true where present (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`)
  - `I-0602-m98-s2-backup-class-coverage-matrix.md`: `outcome=GO` and `evidence_present=true`
  - `I-0602-m98-s3-backup-restart-isolation-matrix.md`: `outcome=GO`, `evidence_present=true`, `peer_cursor_delta=0`, and `peer_watermark_delta=0`
  - all required `GO` rows must keep `failure_mode` empty
- Any required `NO-GO` row in any required C0113 artifact must include non-empty `failure_mode`.

#### C0113 Decision Hook
- `DP-0146-C0113`: C0113 remains blocked unless all required `I-0602` rows for `solana-devnet`, `base-sepolia`, and `btc-testnet` in the three C0113 artifacts are present and satisfy:
  - `outcome=GO`
  - `evidence_present=true`
  - required hard-stop booleans (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`) are true where columns are present
  - required peer deltas are zero where present
  - required `NO-GO` rows include non-empty `failure_mode`.
