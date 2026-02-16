# M88 Post-Reintegration-Seal Drift-Reanchor Lineage-Compaction Marker-Expiry Late-Resurrection Quarantine Reintegration Seal Drift Reanchor Lineage-Compaction Marker-Expiry Late-Resurrection Quarantine Reintegration Determinism

## Scope
- Milestone: `M88`
- Execution slices: `M88-S1` (`I-0461`), `M88-S2` (`I-0462`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## Problem Statement
After post-reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine converges, reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration must be deterministic. If sequencing is non-deterministic, delayed reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine echoes and stale reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration candidates can reopen ownership, duplicate canonical IDs, suppress valid logical events, or violate cursor/watermark safety during replay/restart.

## Reliability Contract
1. Equivalent logical ranges under hold baseline, deterministic apply, crash-restart, and rollback+re-forward permutations produce one deterministic canonical tuple output set per chain.
2. One-chain post-reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration transitions while peer chains progress produce no cross-chain control bleed and no cross-chain cursor bleed.
3. Replay/resume from post-reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration boundaries preserves fee and signed-delta invariants:
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
1. `0` canonical tuple diffs across post-reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration permutations.
2. `0` duplicate canonical IDs and `0` missing logical events in post-reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration replay fixtures.
3. `0` cross-chain control/cursor bleed violations in one-chain transition counterexamples.
4. Validation passes: `make test`, `make test-sidecar`, `make lint`.

## Decision Hook
- `DP-0101-BA`: deterministic reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration ownership fencing on `(epoch, bridge_sequence, drain_watermark, live_head, steady_state_watermark, steady_generation, generation_retention_floor, floor_lift_epoch, settle_window_epoch, spillover_epoch, spillover_rejoin_epoch, rejoin_seal_epoch, seal_drift_epoch, drift_reanchor_epoch, reanchor_compaction_epoch, compaction_expiry_epoch, resurrection_quarantine_epoch, resurrection_reintegration_epoch, resurrection_reintegration_seal_epoch, resurrection_reintegration_seal_drift_epoch, resurrection_reintegration_seal_drift_reanchor_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_epoch, resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_epoch)`; ambiguity fallback is pre-late-resurrection-quarantine-reintegration boundary pin + unresolved reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration candidate quarantine until replay-safe lineage confirmation.

## Implementation Notes
- Reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine echoes cannot overtake an already accepted reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration ownership fence for the same rollback lineage.
- Reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine-reintegration progression remains strictly monotonic at the same epoch and must not regress to reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry-late-resurrection-quarantine ownership.
