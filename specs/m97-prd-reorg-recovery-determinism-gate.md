# M97 PRD Reorg Recovery + Replay Determinism Gate

## Scope
- Milestone: `M97`
- Execution slices: `M97-S1` (`I-0521`), `M97-S2` (`I-0522`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R4`: deterministic replay.
- `R8`: fail-fast correctness-impacting path safety.
- `8`: reorg/finality handling and replay from safe boundaries.
- `10`: deterministic replay acceptance behavior.

## Problem Statement
`M95` completed control-scoping hardening, but deterministic fork-depth recovery behavior still requires an explicit gate before optional post-PRD work resumes. We must prove that deterministic replay remains unique and cursor-safe when a chain observation is invalidated by fork/hash mismatch or restart from committed recovery boundaries.

## Reorg Recovery Contract
1. Fork or block-hash flip at a cursor must trigger rollback/recovery behavior with deterministic replay from the last safe boundary.
2. Recovery/replay across one-block and multi-block reorg vectors must preserve canonical tuple identity and signed-delta conservation.
3. Required matrix families must be chain-scoped and include explicit fork/perturbation metadata.
4. Any one-chain recovery perturbation while peers advance requires zero peer cursor and watermark movement (`peer_cursor_delta=0`, `peer_watermark_delta=0`).

## Required Matrices
- Fork recovery matrix (`.ralph/reports/I-0521-m97-s1-fork-recovery-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `fork_type`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `evidence_present`, `outcome`, `failure_mode`
- Replay continuity matrix (`.ralph/reports/I-0521-m97-s1-recovery-continuity-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `recovery_permutation`, `class_path`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `evidence_present`, `outcome`, `failure_mode`
- QA peer-isolation matrix (`.ralph/reports/I-0522-m97-s2-reorg-peer-isolation-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `outcome`, `evidence_present`, `failure_mode`

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `chain_adapter_runtime_wired`
- `reorg_recovery_deterministic`

## Measurable Exit Gates
1. Required fork vectors (`one_block_reorg`, `multi_block_reorg`, `canonical_range_replay`, `finalized_to_pending_crossover`, `restart_from_rollback_boundary`) have complete evidence rows with `evidence_present=true`.
2. For every required row in replay/continuity families, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, and `signed_delta_conservation_ok` are true.
3. Every required peer-isolation row has `peer_cursor_delta=0` and `peer_watermark_delta=0` with `outcome=GO`.
4. Validation remains unchanged: `make test`, `make test-sidecar`, `make lint`.

## Decision Hook
- `DP-0109-M97`: any missing cell, any `outcome=NO-GO`, any non-unique canonical-ID family result, or any required row with non-zero peer deltas is a hard NO-GO for `M97` promotion.
