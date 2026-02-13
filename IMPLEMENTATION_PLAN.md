# IMPLEMENTATION_PLAN.md

- Source PRD: `PRD.md v2.0` (2026-02-13)
- Execution mode: local Ralph loop (`.ralph/*.md` queue + per-issue commits)
- Target: production-grade support for `solana-devnet` + `base-sepolia`

## Active Milestone
## M1. Canonical Normalizer Contract (Top Priority)

### Goal
Establish a deterministic normalizer contract that guarantees:
- no duplicate indexing,
- canonical signed balance deltas,
- deterministic replay output.

### Scope
- Define canonical normalized event envelope.
- Implement deterministic `event_id` generation.
- Enforce DB uniqueness on `event_id`.
- Wire ingestion idempotency (`upsert/ignore on conflict`).

### Acceptance Criteria
- Same input replay produces identical `event_id` set.
- Duplicate inserts are rejected/ignored without data divergence.
- Solana and Base outputs conform to one shared envelope schema.

## Milestone Queue

## M2. Solana Delta Completeness
- Cover native/token transfer + mint/burn + fee deltas.
- Validate instruction ownership dedup behavior.

## M3. Base Fee Completeness
- Emit `fee_execution_l2` and `fee_data_l1` deltas.
- Ensure payer total fee debit consistency.

## M4. Reorg/Replay Recovery
- Detect canonical hash drift.
- Roll back from fork cursor and replay deterministically.

## M5. QA Gates and Release Readiness
- Golden datasets for both chains.
- Invariant suite (`no-dup`, balance consistency, fee presence).
- Release checklist and rollout guardrails.

## Risks
- R1: Chain-specific decoder outputs diverge from canonical schema.
- R2: Fee decomposition fields unavailable/inconsistent per endpoint.
- R3: Replay drift due to non-deterministic plugin behavior.

## Controls
- Canonical envelope contract tests first.
- Deterministic plugin API boundaries.
- Strict unique constraints and replay tests in CI path.

## Next Slice (Immediate)
1. Schema and model updates for canonical normalized events.
2. Normalizer event_id implementation and dedup tests.
3. Initial Solana/Base fee event normalization contracts.
