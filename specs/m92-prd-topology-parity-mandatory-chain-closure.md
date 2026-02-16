# M92 PRD-Priority Topology A/B/C Mandatory-Chain Closure Gate

## Scope
- Milestone: `M92`
- Execution slices: `M92-S1` (`I-0481`), `M92-S2` (`I-0482`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R6`: deployment topology independence.
- `R7`: strict chain isolation.
- `9.4`: topology parity tests.
- `10`: acceptance requires topology parity and no cross-chain cursor bleed.

## Problem Statement
M91 introduced topology parity hardening, but PRD `R6/R7` obligations are still not promotion-closed until a dedicated `M92` gate proves a deterministic mandatory-chain Topology A/B/C closure with machine-checkable evidence. The PRD-priority path requires an explicit re-gate before any optional post-M90 reliability tranches.

## Reliability Contract
1. Equivalent fixture ranges across topology modes `A`, `B`, and `C` converge to one deterministic canonical tuple output set for each mandatory chain.
2. One-chain failure/restart/replay while peer chains progress yields `0` cross-chain control bleed and `0` cross-chain cursor/watermark bleed across topology modes.
3. Replay/resume from topology-mode boundaries preserves canonical ID determinism, replay idempotency, cursor monotonicity, signed-delta conservation, and fee-event coverage invariants.
4. Topology matrix evidence is explicit and machine-checkable; missing mandatory-chain topology cells are treated as correctness failures.

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
1. `0` canonical tuple diffs across all required `Topology A/B/C` cells (3 chains × 3 topology modes) for equivalent fixture ranges.
2. `0` cross-chain control/cursor bleed violations in one-chain restart/replay topology counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events across topology replay/resume permutations.
4. `0` missing required matrix cells in topology inventory evidence (all mandatory chain/mode combinations present and enumerated).
5. Validation passes: `make test`, `make test-sidecar`, `make lint`.

## Test Matrix
1. Topology parity matrix:
- same fixture ranges under `A`, `B`, and `C` for `solana-devnet`, `base-sepolia`, `btc-testnet` with canonical tuple-set equivalence assertions.
2. Isolation matrix:
- one-chain induced restart/replay in each topology mode while peer chains progress.
3. Replay matrix:
- restart/resume from topology-specific committed boundaries with deterministic convergence assertions.
4. Inventory matrix:
- command/assertion that required topology modes (`A`, `B`, `C`) are present for each mandatory chain; missing coverage fails the gate.

## Decision Hook
- `DP-0101-BE`: promotion is blocked unless deterministic inventory evidence proves full mandatory-chain `Topology A/B/C` matrix coverage and the QA counterexample report provides an explicit `GO` recommendation.
