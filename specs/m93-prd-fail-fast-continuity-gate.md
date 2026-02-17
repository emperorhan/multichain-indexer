# M93 PRD-Priority Fail-Fast + Continuity Gate

## Scope
- Milestone: `M93`
- Execution slices: `M93-S1` (`I-0486`), `M93-S2` (`I-0487`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `8.4`: Restart + failed-path replay continuity while preserving cursor/watermark correctness.
- `8.5`: Fail-fast safety must abort on error classes without unsafe cursor/watermark advancement.
- `10`: deterministic replay acceptance, including one-chain perturbation isolation.

## C0095 Tranche Issue Mapping
- tranche: `C0095` (`I-0532 -> I-0533 -> I-0534`)
- `I-0533` defines the planner handoff artifacts for:
  - PRD `8.4`/`8.5` continuity proof on mandatory chains
  - `I-0533-m93-s1-fail-fast-continuity-matrix.md`
  - `I-0533-m93-s2-one-chain-isolation-matrix.md`
- `I-0534` consumes those artifacts and enforces NO-GO if any required row is missing, any `outcome=NO-GO`, or any one-chain isolation violation.

## Problem Statement
The PRD `R5/R8` controls are still not closed with an explicit, testable gate. Current execution requires a definitive contract proving correctness-impacting failures always abort immediately, cannot advance failed-path state, and that restart/replay reproduces equivalent outputs across mandatory chains.

## Reliability Contract
1. Correctness-impacting defects must abort immediately (`panic`) on first failure path and must not advance cursor or watermark on failed execution.
2. Replay from last-safe committed boundary reproduces canonical tuple outcomes and materialized-balance outcomes deterministically across mandatory chains.
3. One-chain fail-fast perturbation while peer chains progress produces no cross-chain control bleed and no cross-chain cursor/watermark bleed.
4. Fail-fast and restart evidence is represented as reproducible matrix artifacts and mandatory follow-up tickets on any regression.

## Required Matrices (C0095 handoff)
- Fail-fast continuity matrix (`.ralph/reports/I-0530-m93-s1-fail-fast-continuity-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- One-chain isolation matrix (`.ralph/reports/I-0530-m93-s2-one-chain-isolation-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`
- For `I-0533` tranche completeness, these matrix files must be the following active counterparts:
  - `.ralph/reports/I-0533-m93-s1-fail-fast-continuity-matrix.md`
  - `.ralph/reports/I-0533-m93-s2-one-chain-isolation-matrix.md`

## Matrix Contract (Machine-Checkable)
- `outcome` is `GO` or `NO-GO`.
- `evidence_present=true` is required for all required `GO` rows.
- For `outcome=NO-GO`, `failure_mode` must be non-empty.
- For required fail-fast continuity rows, invariants `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, and `chain_adapter_runtime_wired_ok` must all be `true` on `GO`.
- For required one-chain isolation rows with `GO`, `peer_cursor_delta=0` and `peer_watermark_delta=0`.
- For C0095 acceptance, `I-0533` must include all mandatory-chain rows for:
  - `solana-devnet`
  - `base-sepolia`
  - `btc-testnet`

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
5. PRD handoff matrix files exist and are bounded: `I-0533-m93-s1-fail-fast-continuity-matrix.md` and `I-0533-m93-s2-one-chain-isolation-matrix.md`.
6. Validation commands pass: `make test`, `make test-sidecar`, `make lint`.

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
- `DP-0113-C0095`: C0095 remains blocked unless required `I-0533` matrix artifacts for all mandatory chains have `outcome=GO`, `evidence_present=true`, and required invariants true.
