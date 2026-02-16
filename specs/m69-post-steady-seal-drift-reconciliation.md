# M69 Post-Steady-Seal Drift-Reconciliation Determinism

## Scope
- Milestone: `M69`
- Execution slices: `M69-S1` (`I-0379`), `M69-S2` (`I-0380`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## Problem Statement
After post-rejoin steady sealing, delayed seal-drift markers can arrive near restart and rollback boundaries. If ownership arbitration is not deterministic, the runtime can re-emit canonical events, suppress valid logical events, or violate cursor/watermark safety.

## Reliability Contract
1. Equivalent logical ranges under drift-reconciliation baseline, replay, crash-during-drift restart, and rollback+re-forward permutations produce one deterministic canonical tuple output set per chain.
2. One-chain drift-reconciliation transitions while peer chains progress produce no cross-chain control bleed and no cross-chain cursor bleed.
3. Replay/resume from post-steady-seal drift boundaries preserves fee and signed-delta invariants:
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
1. `0` canonical tuple diffs across drift-reconciliation permutation fixtures.
2. `0` duplicate canonical IDs and `0` missing logical events in drift-reconciliation replay fixtures.
3. `0` cross-chain control/cursor bleed violations in one-chain drift counterexamples.
4. Validation passes: `make test`, `make test-sidecar`, `make lint`.

## Decision Hook
- `DP-0101-AH`: deterministic drift-reconciliation ownership fencing on `(epoch, bridge_sequence, drain_watermark, live_head, steady_state_watermark, steady_generation, generation_retention_floor, floor_lift_epoch, settle_window_epoch, spillover_epoch, spillover_rejoin_epoch, rejoin_seal_epoch, seal_drift_epoch)`; ambiguity fallback is pre-drift boundary pin + unresolved marker quarantine until replay-safe lineage confirmation.
