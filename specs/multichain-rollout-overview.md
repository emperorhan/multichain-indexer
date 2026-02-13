# Canonical Normalizer Roadmap Overview (I-0101)

## Objective
Translate `PRD.md v2.0` into executable architecture boundaries for deterministic, duplicate-safe multi-chain normalization on:
- `solana-devnet`
- `base-sepolia`

## Baseline Gaps to Close
1. Normalizer path is Solana-first and not yet full canonical-envelope aligned.
2. Dedup identity currently depends on instruction/address tuple behavior and must converge on canonical `event_id` semantics.
3. PRD v2 canonical fields and fee categories need strict end-to-end enforcement.

## Canonical Contract (Target)
Every normalized event must carry stable replay-safe identity:
- `event_id`
- `chain`, `network`
- `block_cursor`, `block_hash`, `tx_hash`, `tx_index`
- `event_path`
- `actor_address`
- `asset_type`, `asset_id`
- `delta` (signed)
- `event_category`
- `finality_state`
- `decoder_version`, `schema_version`

## Deterministic Identity Rules
`event_id` must be derived from stable fields only:
- chain/network
- tx identity (`tx_hash` or signature)
- canonical `event_path`
- `actor_address`
- `asset_id`
- `event_category`

Never include mutable runtime fields:
- ingest timestamps
- retries
- host-local processing time

## Milestones and Slice Boundaries
1. `M1` foundation:
- `I-0102`: canonical envelope/schema scaffolding.
- `I-0103`: deterministic `event_id` and idempotent ingestion.

2. `M2` Solana completeness:
- `I-0104`: CPI ownership dedup + explicit fee debit coverage.

3. `M3` Base fee completeness:
- `I-0105`: `fee_execution_l2` + `fee_data_l1` normalization.

4. `M4` deterministic recovery (split for slice size):
- `I-0108`: reorg detection + rollback orchestration.
- `I-0109`: replay determinism + cursor/watermark monotonicity.

5. `M5` QA release gate:
- `I-0107`: golden datasets + invariant report + release recommendation.

Dependency order:
`I-0102 -> I-0103 -> (I-0104 || I-0105) -> I-0108 -> I-0109 -> I-0107`

## Milestone Metrics (Release-Relevant)
1. Duplicate metric: `0` duplicate canonical IDs in replay suites.
2. Determinism metric: `0` tuple diffs between independent runs on same range.
3. Solana fee coverage: `100%` successful fixture tx include fee delta.
4. Base fee coverage:
- `100%` successful fixtures include execution fee delta.
- `100%` fixtures with L1 fee fields include `fee_data_l1` delta.

## Critical Decisions and Fallbacks
1. `DP-0101-A` canonical `event_path` encoding.
- Preferred: one canonical serialized key from structured chain-specific path.
- Fallback: persist structured path plus derived key and enforce uniqueness on derived key.

2. `DP-0101-B` Base L1 fee source reliability.
- Preferred: receipt extension extraction from selected client/RPC.
- Fallback: explicit unavailable marker metadata while preserving deterministic execution fee emission.

3. `DP-0101-C` recovery mode while reorg hardening is in progress.
- Preferred: pending/finalized lifecycle with rollback.
- Fallback: finalized-only ingest mode with configurable confirmation lag.
