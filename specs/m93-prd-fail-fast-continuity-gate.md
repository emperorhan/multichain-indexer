# M93 PRD-Priority Fail-Fast + Continuity Gate

## Scope
- Milestone: `M93`
- Execution slices: `M93-S1` (`I-0486`), `M93-S2` (`I-0487`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R5`: Operational continuity.
- `R8`: Fail-fast error contract (no silent progress).
- `9.4`: topology parity/continuity validation principles.
- `10`: acceptance requires deterministic replay equivalence and no cross-chain cursor bleed.

## Problem Statement
The PRD `R5/R8` controls are still not closed with an explicit, testable gate. Current execution requires a definitive contract proving correctness-impacting failures always abort immediately, cannot advance failed-path state, and that restart/replay reproduces equivalent outputs across mandatory chains.

## Reliability Contract
1. Correctness-impacting defects must abort immediately (`panic`) on first failure path and must not advance cursor or watermark on failed execution.
2. Replay from last-safe committed boundary reproduces canonical tuple outcomes and materialized-balance outcomes deterministically across mandatory chains.
3. One-chain fail-fast perturbation while peer chains progress produces no cross-chain control bleed and no cross-chain cursor/watermark bleed.
4. Fail-fast and restart evidence is represented as reproducible matrix artifacts and mandatory follow-up tickets on any regression.

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
1. `0` duplicate canonical IDs under defined fail-fast/restart fixture families.
2. `0` replay-path duplicate deltas and `0` materialized-balance divergence under repeated fail/recover runs.
3. `0` cross-chain control/cursor bleed assertions under one-chain panic/restart counterexamples.
4. `0` failed-path cursor/watermark progressions in all required fail-fast fault cases.
5. Validation commands pass: `make test`, `make test-sidecar`, `make lint`.

## Test Matrix
1. Fault-injection matrix:
- fail-fast correctness-impacting failure classes in `solana-devnet`, `base-sepolia`, `btc-testnet`.
2. Restart matrix:
- replay from deterministic committed boundaries with canonical tuple and balance equivalence assertions.
3. Peer-progress counterexamples:
- one-chain fail-fast perturbation while peers progress, assert no cross-chain control/cursor bleed.
4. Continuity assertions:
- explicit continuity checks for fee/event coverage and signed-delta conservation.
5. Evidence output:
- `.ralph/reports` entries with matrix status and fail/recovery recommendation.

## Decision Hook
- `DP-0103-M93`: treat any observed failed-path cursor/watermark progression as a hard contract failure; fail gate for the corresponding slice until deterministic replay and fault-matrix parity are re-proven.
