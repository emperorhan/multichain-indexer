# M72 Post-Lineage-Compaction Marker-Expiry Determinism

## Scope
- Milestone: `M72`
- Execution slices: `M72-S1` (`I-0394`), `M72-S2` (`I-0395`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## Problem Statement
After post-reanchor lineage compaction converges, compacted lineage markers must eventually expire. If marker-expiry sequencing is not deterministic, delayed expiry/compaction markers can reopen stale ownership, duplicate canonical IDs, suppress valid logical events, or violate cursor/watermark safety.

## Reliability Contract
1. Equivalent logical ranges under marker-expiry baseline, replay, crash-during-marker-expiry restart, and rollback+re-forward permutations produce one deterministic canonical tuple output set per chain.
2. One-chain marker-expiry transitions while peer chains progress produce no cross-chain control bleed and no cross-chain cursor bleed.
3. Replay/resume from post-lineage-compaction marker-expiry boundaries preserves fee and signed-delta invariants:
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
1. `0` canonical tuple diffs across marker-expiry permutation fixtures.
2. `0` duplicate canonical IDs and `0` missing logical events in marker-expiry replay fixtures.
3. `0` cross-chain control/cursor bleed violations in one-chain marker-expiry counterexamples.
4. Validation passes: `make test`, `make test-sidecar`, `make lint`.

## Decision Hook
- `DP-0101-AK`: deterministic marker-expiry ownership fencing on `(epoch, bridge_sequence, drain_watermark, live_head, steady_state_watermark, steady_generation, generation_retention_floor, floor_lift_epoch, settle_window_epoch, spillover_epoch, spillover_rejoin_epoch, rejoin_seal_epoch, seal_drift_epoch, drift_reanchor_epoch, reanchor_compaction_epoch, compaction_expiry_epoch)`; ambiguity fallback is pre-expiry boundary pin + unresolved marker quarantine until replay-safe lineage confirmation.
