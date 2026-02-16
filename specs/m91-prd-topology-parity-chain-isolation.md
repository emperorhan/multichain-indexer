# M91 PRD-Priority Topology Parity + Strict Chain Isolation Gate

## Scope
- Milestone: `M91`
- Execution slices: `M91-S1` (`I-0473`), `M91-S2` (`I-0474`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R6`: deployment topology independence.
- `R7`: strict chain isolation.
- `9.4`: topology parity tests.
- `10`: acceptance requires topology parity and no cross-chain cursor bleed.

## Problem Statement
This requirement is now closed by M94 follow-through and is retained as the signed baseline for PRD `R6`/`R7` behavior before optional post-M94 refinements.
Deployment-shape changes previously masked shared-state coupling risks; the closed baseline requires topology parity and chain-isolation behavior to remain deterministic under counterexample replay.

## Reliability Contract
1. Equivalent fixture ranges across `Topology A/B/C` converge to one deterministic canonical tuple output set per mandatory chain.
2. One-chain stress (failure/restart/replay) while peers progress produces no cross-chain control bleed and no cross-chain cursor/watermark bleed.
3. Replay/resume from topology-specific restart boundaries preserves canonical ID determinism, replay idempotency, and signed-delta/fee invariants.
4. Correctness-impacting failures still enforce fail-fast panic with no failed-path cursor/watermark advancement.

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
1. `0` canonical tuple diffs per chain across `Topology A/B/C` for equivalent fixture ranges.
2. `0` duplicate canonical IDs and `0` missing logical events under topology-mode replay/resume permutations.
3. `0` cross-chain control/cursor bleed violations under one-chain stress/restart counterexamples.
4. Validation passes: `make test`, `make test-sidecar`, `make lint`.

## Test Matrix
1. Topology parity matrix:
- same fixtures under `A`, `B`, and `C` with canonical tuple equivalence assertion per chain.
2. Isolation matrix:
- one-chain induced failure/restart/replay in each topology mode while peer chains continue.
3. Replay matrix:
- restart from topology-specific committed boundaries with deterministic convergence assertion.

## Decision Hook
- `DP-0101-BD`: topology parity diff key is `(chain, network, topology_mode, block_cursor, tx_hash, event_path, actor_address, asset_id, event_category)`; ambiguous topology-only divergence is quarantined and treated as correctness failure until deterministic equivalence is restored.
