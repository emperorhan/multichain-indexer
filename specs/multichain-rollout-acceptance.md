# Canonical Normalizer Acceptance Contract (I-0101)

## Required Validation Commands
Every slice must pass:
1. `make test`
2. `make test-sidecar`
3. `make lint`

## Global Invariants (All Milestones)
1. Canonical IDs are deterministic for identical input ranges.
2. Re-ingest/replay does not duplicate normalized events.
3. Signed deltas are transaction-scope balance-consistent.
4. Fee deltas are explicitly present per chain fee rules.
5. Mandatory runtime chain adapters are wired in production boot path, not only test paths.

## Slice-Level Test Contracts

### I-0102 (M1-S1)
Assertions:
1. Canonical envelope fields exist in domain and persistence models.
2. Schema migration applies cleanly in test DB.
3. Serialization/parsing fixtures cover Solana and Base envelope paths.

Pass Evidence:
- model/schema tests green.
- migration test green.

### I-0103 (M1-S2/S3)
Assertions:
1. Fixed fixtures generate stable ordered `event_id` sets across independent runs.
2. Re-ingesting identical normalized batch does not increase event-row count.
3. Re-ingesting identical normalized batch does not alter materialized balances.

Pass Evidence:
- deterministic-ID unit tests green.
- idempotent-ingestion and balance-stability tests green.

### I-0104 (M2)
Assertions:
1. CPI-heavy fixtures emit one canonical owner per Solana path.
2. Successful Solana fixture transactions always include fee debit event(s).
3. Replay preserves identical ordered `(event_id, delta, event_category)` tuples.

Pass Evidence:
- CPI dedup fixture suite green.
- Solana fee presence assertions green.

### I-0105 (M3)
Assertions:
1. Successful Base fixtures emit deterministic `fee_execution_l2` events.
2. Fixtures with L1 fee fields emit deterministic `fee_data_l1` events.
3. Replay produces zero duplicate Base fee events.
4. Where both components are present, component sum matches payer debit.

Pass Evidence:
- Base fee fixture suite green.
- component-sum invariant checks green.

### I-0108 (M4-S1)
Assertions:
1. Block-hash mismatch at same cursor triggers rollback from fork cursor.
2. Rollback path is deterministic across repeated simulations.

Pass Evidence:
- reorg simulation tests green with rollback-path assertion logs.

### I-0109 (M4-S2)
Assertions:
1. Replay from persisted normalized artifacts reproduces identical ordered canonical tuples.
2. Post-recovery cursor/watermark state remains monotonic.

Pass Evidence:
- cross-run tuple comparison report shows `0` diffs.
- cursor monotonic regression tests green.

### I-0107 (M5)
Assertions:
1. Golden datasets exist for Solana and Base fixture ranges.
2. Invariant suite passes (no-dup, signed-delta consistency, fee completeness, determinism).
3. QA report under `.ralph/reports/` includes explicit `pass`/`fail` recommendation.
4. Each failure is mapped to a new developer issue with repro context.

Pass Evidence:
- QA report artifact present and complete.
- follow-up issue files exist for any failed invariant.

### I-0110 (M6)
Assertions:
1. Runtime bootstrap deterministically wires both Solana devnet and Base sepolia targets.
2. Watched-address bootstrap initializes cursor state per address and fails fast on partial sync errors.
3. Base runtime path validates fetch -> decode -> normalize -> ingest end-to-end.
4. Replaying an already-ingested Base batch is idempotent (no duplicate balance adjustments).

Pass Evidence:
- `cmd/indexer/main_test.go` runtime target + watched-address sync tests green.
- `internal/pipeline/normalizer/base_runtime_e2e_test.go` Base runtime e2e green.
- `internal/pipeline/ingester/ingester_test.go` Base replay idempotency regression green.

### I-0114 (M7-S1)
Assertions:
1. Runtime preflight rejects missing/misaligned mandatory chain adapter wiring deterministically.
2. Dual-chain replay smoke checks preserve canonical no-dup behavior (`event_id` uniqueness) across consecutive identical runs.
3. Post-replay cursor/watermark progression remains monotonic in the same smoke path.

Pass Evidence:
- Runtime wiring guard tests green for both mandatory chains (`solana-devnet`, `base-sepolia`).
- Dual-chain replay smoke test/report shows `0` duplicate canonical IDs.
- Cursor monotonic assertions for the same replay path are green.

### I-0115 (M7-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M7 invariants.
2. QA executes at least one counterexample scenario for runtime wiring drift or replay regression.
3. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report artifact includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcome is documented with invariant-level verdict.
- Follow-up issue links are present for failures (if any).

### I-0117 (M8-S1)
Assertions:
1. Solana failed transactions with complete fee metadata (`fee_payer`, non-zero `fee_amount`) emit one deterministic canonical fee debit event.
2. Base failed transactions with complete fee metadata emit deterministic fee events (`fee_execution_l2`, and `fee_data_l1` when source fields exist).
3. Mixed success+failed replay paths preserve canonical no-dup behavior and cursor monotonicity.
4. Incomplete failed-transaction fee metadata paths stay deterministic and do not create synthetic/guessed fee events.

Pass Evidence:
- Normalizer fixture tests cover Solana/Base failed-transaction fee emission and incomplete-metadata no-op cases.
- Replay tests show `0` duplicate canonical IDs on mixed success+failed fixtures.
- Ingestion tests show no double-apply balance side effects on repeated failed-path replay.

### I-0118 (M8-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M8 invariants.
2. QA executes counterexample scenarios targeting failed-transaction fee metadata incompleteness and replay duplication regressions.
3. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes are documented with invariant-level verdicts for failed-fee paths.
- Follow-up issue links are present for failures (if any).

## Release Blockers
Release recommendation must be `fail` if any condition holds:
1. Duplicate canonical IDs detected after replay.
2. Fee completeness invariant fails on either chain.
3. Determinism comparison differs between independent runs on same range.
4. Required validation commands fail.
5. Mandatory chain adapter runtime wiring cannot be proven in runtime-path evidence.
