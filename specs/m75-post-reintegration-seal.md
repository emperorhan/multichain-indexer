# M75 Post-Reintegration Seal Determinism

## Scope
- Milestone: `M75`
- Execution slices: `M75-S1` (`I-0409`), `M75-S2` (`I-0410`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## Problem Statement
After post-late-resurrection quarantine reintegration converges, reintegrated lineage markers must be sealed deterministically. If sealing order is non-deterministic, delayed reintegration echoes and stale seal candidates can reopen ownership, duplicate canonical IDs, suppress valid logical events, or violate cursor/watermark safety during replay/restart.

## Reliability Contract
1. Equivalent logical ranges under reintegration-seal-hold baseline, deterministic seal apply, crash-during-seal restart, and rollback+re-forward permutations produce one deterministic canonical tuple output set per chain.
2. One-chain post-reintegration seal transitions while peer chains progress produce no cross-chain control bleed and no cross-chain cursor bleed.
3. Replay/resume from post-reintegration seal boundaries preserves fee and signed-delta invariants:
- Solana transaction fee coverage.
- Base execution fee + L1 data/rollup fee coverage.
- BTC miner fee + signed delta conservation.
4. Correctness-impacting failures still enforce fail-fast panic with no failed-path cursor/watermark progression.

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `solana_fee_event_coverage`
- `base_fee_split_coverage`
- `reorg_recovery_deterministic`
- `chain_adapter_runtime_wired`

## Measurable Exit Gates
1. `0` canonical tuple diffs across reintegration-seal permutations.
2. `0` duplicate canonical IDs and `0` missing logical events in reintegration-seal replay fixtures.
3. `0` cross-chain control/cursor bleed violations in one-chain reintegration-seal counterexamples.
4. Validation passes: `make test`, `make test-sidecar`, `make lint`.

## Decision Hook
- `DP-0101-AN`: deterministic reintegration-seal ownership fencing on `(epoch, bridge_sequence, drain_watermark, live_head, steady_state_watermark, steady_generation, generation_retention_floor, floor_lift_epoch, settle_window_epoch, spillover_epoch, spillover_rejoin_epoch, rejoin_seal_epoch, seal_drift_epoch, drift_reanchor_epoch, reanchor_compaction_epoch, compaction_expiry_epoch, resurrection_quarantine_epoch, resurrection_reintegration_epoch, resurrection_reintegration_seal_epoch)`; ambiguity fallback is pre-seal boundary pin + unresolved seal-candidate quarantine until replay-safe lineage confirmation.
