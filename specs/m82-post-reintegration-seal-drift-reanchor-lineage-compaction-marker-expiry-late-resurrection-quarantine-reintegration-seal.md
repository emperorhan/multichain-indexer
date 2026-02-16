# M82 Post-Reintegration-Seal Drift-Reanchor Lineage-Compaction Marker-Expiry Late-Resurrection Quarantine Reintegration Seal Determinism

## Scope
- Milestone: `M82`
- Execution slices: `M82-S1` (`I-0436`), `M82-S2` (`I-0437`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## Problem Statement
After post-reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration converges, reintegration-seal must be deterministic. If reintegration-seal sequencing is non-deterministic, delayed reintegration echoes and stale reintegration-seal candidates can reopen ownership, duplicate canonical IDs, suppress valid logical events, or violate cursor/watermark safety during replay/restart.

## Reliability Contract
1. Equivalent logical ranges under reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection-quarantine-reintegration-seal-hold baseline, deterministic reintegration-seal apply, crash-during-reintegration-seal restart, and rollback+re-forward permutations produce one deterministic canonical tuple output set per chain.
2. One-chain post-reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal transitions while peer chains progress produce no cross-chain control bleed and no cross-chain cursor bleed.
3. Replay/resume from post-reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal boundaries preserves fee and signed-delta invariants:
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
1. `0` canonical tuple diffs across reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal permutations.
2. `0` duplicate canonical IDs and `0` missing logical events in reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal replay fixtures.
3. `0` cross-chain control/cursor bleed violations in one-chain reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal counterexamples.
4. Validation passes: `make test`, `make test-sidecar`, `make lint`.

## Decision Hook
- `DP-0101-AU`: deterministic reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration-seal ownership fencing on `(epoch, bridge_sequence, drain_watermark, live_head, steady_state_watermark, steady_generation, generation_retention_floor, floor_lift_epoch, settle_window_epoch, spillover_epoch, spillover_rejoin_epoch, rejoin_seal_epoch, seal_drift_epoch, drift_reanchor_epoch, reanchor_compaction_epoch, compaction_expiry_epoch, resurrection_quarantine_epoch, resurrection_reintegration_epoch, resurrection_reintegration_seal_epoch, resurrection_reintegration_seal_drift_epoch, resurrection_reintegration_seal_drift_reanchor_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_epoch)`; ambiguity fallback is pre-reintegration-seal boundary pin + unresolved reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal candidate quarantine until replay-safe lineage confirmation.
