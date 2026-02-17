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
- `I-0545`/`I-0546` (`C0098`) handoff: residual PRD replay/recovery hardening for `R4`, `R8`, `8.4`, `8.5`, and `10`.
- `DP-0114-C0098`: required C0098 evidence blockers for any missing row, `NO-GO`, `evidence_present=false`, or non-zero required peer deltas.

## Problem Statement
`M95` completed control-scoping hardening, but deterministic fork-depth recovery behavior still requires an explicit gate before optional post-PRD work resumes. We must prove that deterministic replay remains unique and cursor-safe when a chain observation is invalidated by fork/hash mismatch or restart from committed recovery boundaries.

## Reorg Recovery Contract
1. Fork or block-hash flip at a cursor must trigger rollback/recovery behavior with deterministic replay from the last safe boundary.
2. Recovery/replay across one-block and multi-block reorg vectors must preserve canonical tuple identity and signed-delta conservation.
3. Required matrix families must be chain-scoped and include explicit fork/perturbation metadata.
4. Any one-chain recovery perturbation while peers advance requires zero peer cursor and watermark movement (`peer_cursor_delta=0`, `peer_watermark_delta=0`).

## C0098 Matrix Contracts (`I-0545` / `I-0546`)
- Required perturbation classes (`fork_recovery`):
  - `one_block_reorg`
  - `multi_block_reorg`
  - `canonical_range_replay`
  - `finalized_to_pending_crossover`
  - `restart_from_rollback_boundary`
- Fork recovery matrix (`.ralph/reports/I-0545-m97-s1-fork-recovery-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `fork_type`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `evidence_present`, `outcome`, `failure_mode`
- Replay continuity matrix (`.ralph/reports/I-0545-m97-s1-recovery-continuity-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `recovery_permutation`, `class_path`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `evidence_present`, `outcome`, `failure_mode`
  - mandatory hard-stop columns per required row: `canonical_event_id_unique_ok=true`, `replay_idempotent_ok=true`, `cursor_monotonic_ok=true`, `signed_delta_conservation_ok=true`, `evidence_present=true`, `outcome=GO`, `peer_cursor_delta=0`, `peer_watermark_delta=0` where peer deltas are required.
- QA peer-isolation matrix (`.ralph/reports/I-0546-m97-s2-peer-isolation-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `outcome`, `evidence_present`, `failure_mode`
- Required peer-isolation hard-stop columns: `peer_cursor_delta=0`, `peer_watermark_delta=0`, `outcome=GO`, `evidence_present=true`.

## C0099 PRD Closeout Transition Handoff

## PRD Traceability
- `8.4`: restart continuity checks for failed-path recovery.
- `8.5`: no failed-path cursor/watermark progression.
- `10`: deterministic replay and peer-isolation behavior under one-chain perturbation.

## C0099 Transition Blockers
- Required `I-0545`/`I-0546` evidence families for closeout transition unblocking:
  - `.ralph/reports/I-0545-m97-s1-fork-recovery-matrix.md`
  - `.ralph/reports/I-0545-m97-s1-recovery-continuity-matrix.md`
  - `.ralph/reports/I-0546-m97-s2-peer-isolation-matrix.md`
- Transition blockers for optional-refinement unlock and PRD gate alignment:
  - `8.4` requires every required recovery continuity row in C0098 outputs to stay in `GO` and carry `evidence_present=true`.
  - `8.5` requires failed-path and restart recovery proof rows to keep peer-chain bleed at zero where peer deltas are reported (`peer_cursor_delta=0`, `peer_watermark_delta=0`).
  - `10` requires every required fork/recovery and peer-isolation row to preserve deterministic invariants (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`) as `true` when columns are present.
- Required hard-stop for each required row in the C0098 evidence families:
  - `outcome` must be `GO`.
  - `evidence_present` must be `true`.
  - `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, and `signed_delta_conservation_ok` must be `true` for rows exposing them.
  - `peer_cursor_delta` and `peer_watermark_delta` must be `0` where those columns exist.
  - For any `outcome=NO-GO`, `failure_mode` must be populated.
- `DP-0115-C0099`: any required transition row violating the above (including missing evidence) blocks optional-refinement unblocking.

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
- `DP-0114-C0098`: any required `I-0545`/`I-0546` matrix row missing, with `outcome=NO-GO`, `evidence_present=false`, `failure_mode` missing on `NO-GO`, or non-zero required peer deltas is a hard NO-GO for C0098 and optional refinement unblocking.
