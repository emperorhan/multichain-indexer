# IMPLEMENTATION_PLAN.md

- Source PRD: `PRD.md v2.0` (2026-02-13)
- Execution mode: local Ralph loop (`.ralph/` markdown queue)
- Runtime targets: `solana-devnet`, `base-sepolia`
- Mission-critical target: canonical normalizer that indexes all asset-volatility events without duplicates

## Program Graph
`M1 -> (M2 || M3) -> M4 -> M5`

Execution queue (dependency-ordered):
1. `I-0102` (`M1-S1`) canonical envelope + schema scaffolding
2. `I-0103` (`M1-S2/S3`) deterministic `event_id` + idempotent ingestion
3. `I-0104` (`M2`) Solana dedup + fee completeness
4. `I-0105` (`M3`) Base fee decomposition
5. `I-0108` (`M4-S1`) reorg detection + rollback wiring
6. `I-0109` (`M4-S2`) replay determinism + cursor monotonicity hardening
7. `I-0107` (`M5`) QA golden/invariant release gate

## Global Verification Contract
Every implementation slice must pass:
1. `make test`
2. `make test-sidecar`
3. `make lint`

Global non-negotiables:
1. Deterministic canonical IDs for identical normalized inputs.
2. Replay-safe idempotency (`balance_events` and materialized balances).
3. Signed delta event model for all in-scope balance changes.
4. Explicit fee event coverage:
- Solana transaction fee.
- Base L2 execution fee + L1 data/rollup fee (or deterministic unavailable marker path).

## Milestone Scorecard

### M1. Canonical Contract + Deterministic Identity (P0)

#### Objective
Establish canonical normalized envelope, deterministic identity, and idempotent ingestion as hard gate for downstream work.

#### Entry Gate
- Planner-approved canonical event contract in `specs/*`.
- Migration approach reviewed for compatibility and rollback plan.

#### Slices
1. `M1-S1` (`I-0102`): schema/model scaffolding for canonical envelope fields.
2. `M1-S2/S3` (`I-0103`): deterministic `event_id` builder + DB uniqueness/idempotent ingestion.

#### Definition Of Done
1. Canonical fields are represented end-to-end in event pipeline contracts:
- `event_id`, `event_path`, `asset_type`, `asset_id`, `delta`, `event_category`, `finality_state`, `decoder_version`, `schema_version`.
2. DB uniqueness is enforced with canonical identity (`UNIQUE(event_id)` or equivalent deterministic key).
3. Re-ingesting identical normalized batches does not increase event rows and does not double-apply balances.

#### Test Contract
1. Deterministic ID fixtures: same input -> same ordered `event_id` set across independent runs.
2. Repo/ingester idempotency test: replay batch keeps event row count unchanged.
3. Balance stability test: replay batch keeps materialized balances unchanged.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs in replay test fixtures.
2. `0` balance drift after replay on fixture ranges.
3. Validation commands pass.

#### Risk Gate + Fallback
- Gate: migration compatibility and live-read safety.
- Fallback: temporary dual-constraint mode (`legacy unique key` + canonical `event_id`) until cutover confidence is proven.

### M2. Solana Completeness Hardening (P0)

#### Objective
Guarantee Solana signed-delta completeness and duplicate-safe ownership for CPI-heavy transactions.

#### Entry Gate
- `M1` exit gate green.

#### Slice
1. `I-0104`: deterministic Solana ownership + fee completeness hardening.

#### Definition Of Done
1. Deterministic outer/inner instruction ownership for CPI-heavy paths.
2. Successful Solana transactions emit explicit fee debit delta (`asset_type=fee`).
3. No duplicate canonical events for identical Solana instruction-path identity.

#### Test Contract
1. CPI fixture suite asserts one owner per canonical path.
2. Fee presence assertions for successful transactions.
3. Solana replay determinism assertions on ordered tuples `(event_id, delta, event_category)`.

#### Exit Gate (Measurable)
1. `0` duplicate-path emissions in CPI fixture suite.
2. `100%` fee-event presence in successful Solana fixture transactions.
3. Validation commands pass.

#### Risk Gate + Fallback
- Gate: ambiguous ownership across nested CPI traces.
- Fallback: deterministic precedence rule codified in tests and docs; unresolved ambiguous patterns are dropped with explicit diagnostic counter until rule extension lands.

### M3. Base Sepolia Fee Decomposition (P0)

#### Objective
Normalize Base fees as deterministic signed deltas with explicit component semantics.

#### Entry Gate
- `M1` exit gate green.

#### Slice
1. `I-0105`: `fee_execution_l2` + `fee_data_l1` normalization with deterministic missing-field behavior.

#### Definition Of Done
1. Emit `fee_execution_l2` for each successful covered transaction.
2. Emit `fee_data_l1` when receipt fields are available.
3. Preserve deterministic behavior when L1 data fee source fields are missing.

#### Test Contract
1. Fixture set includes both field-present and field-missing receipts.
2. Component-sum invariant checks consistency with payer fee debit when both fields exist.
3. Replay tests assert zero duplicate fee events.

#### Exit Gate (Measurable)
1. `100%` execution-fee event coverage in successful Base fixtures.
2. `100%` data-fee event coverage where source fields are present.
3. Validation commands pass.

#### Risk Gate + Fallback
- Gate: RPC/client variability for L1 data fee fields.
- Fallback: deterministic execution fee event + explicit metadata marker (`data_fee_l1_unavailable=true`) while coverage gaps are tracked.

### M4. Replay/Reorg Deterministic Recovery (P1)

#### Objective
Harden deterministic recovery from canonicality drift without manual data repair.

#### Entry Gate
- `M2` and `M3` exit gates green.

#### Slices
1. `M4-S1` (`I-0108`): reorg detection and rollback orchestration from fork cursor.
2. `M4-S2` (`I-0109`): replay determinism and post-recovery cursor/watermark monotonicity.

#### Definition Of Done
1. Block-hash mismatch at same cursor triggers deterministic rollback path.
2. Replay from persisted normalized artifacts preserves canonical identity.
3. Cursor/watermark progression remains monotonic after recovery.

#### Test Contract
1. Reorg simulation test exercises rollback/replay path.
2. Cross-run determinism diff test compares ordered canonical tuples from two independent runs.
3. Cursor regression suite asserts monotonic progression after recovery.

#### Exit Gate (Measurable)
1. `0` tuple diffs in cross-run determinism report for same input range.
2. `0` cursor monotonicity violations in regression suite.
3. Validation commands pass.

#### Risk Gate + Fallback
- Gate: instability in early rollback path.
- Fallback: finalized-only ingestion mode behind config flag until rollback path exits gate.

### M5. QA Goldens + Invariant Release Gate (P0)

#### Objective
Block release unless deterministic, duplicate-free, fee-complete behavior is proven on both chains.

#### Entry Gate
- `M4` exit gate green.

#### Slice
1. `I-0107`: QA golden refresh + invariant execution + release recommendation.

#### Definition Of Done
1. Golden datasets exist for Solana and Base fixture ranges.
2. Invariants pass: no-dup, signed-delta consistency, fee presence/completeness, cross-run determinism.
3. QA report is stored under `.ralph/reports/` with explicit `pass`/`fail` recommendation.

#### Test Contract
1. QA executes required validation commands plus golden/invariant runners.
2. Each failing invariant is mapped to a developer follow-up issue with reproducible fixture context.

#### Exit Gate (Measurable)
1. `0` unresolved blocker invariants in QA report.
2. Explicit release recommendation is present.
3. Validation commands pass.

## Decision Register (Major + Fallback)

1. `DP-0101-A`: canonical `event_path` encoding.
- Preferred: chain-specific structured path normalized into one canonical serialized key.
- Fallback: persist structured fields + derived key; enforce uniqueness on derived key.

2. `DP-0101-B`: Base L1 data fee source reliability.
- Preferred: parse receipt extension fields from selected RPC/client.
- Fallback: explicit unavailable marker metadata while preserving deterministic execution fee event emission.

3. `DP-0101-C`: reorg handling mode during hardening.
- Preferred: pending/finalized lifecycle with rollback/replay.
- Fallback: finalized-only ingestion mode with configurable confirmation lag.

## Local Queue Mapping

Active downstream queue from this plan:
1. `I-0102`
2. `I-0103`
3. `I-0104`
4. `I-0105`
5. `I-0108`
6. `I-0109`
7. `I-0107`

Superseded issue:
- `I-0106` is superseded by `I-0108` + `I-0109` to keep M4 slices independently releasable.
