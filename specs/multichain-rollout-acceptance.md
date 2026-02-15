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

### I-0122 (M9-S1)
Assertions:
1. Mandatory-chain adapter RPC contracts are explicit and enforced for runtime clients and test doubles.
2. Contract drift is surfaced deterministically in tests/lint before runtime execution.
3. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Contract-parity test coverage is present for both `solana-devnet` and `base-sepolia` adapter client surfaces.
- Runtime wiring guard tests remain green for mandatory chains.
- Replay/idempotency regression tests remain green with no duplicate canonical IDs or cursor regression evidence.

### I-0123 (M9-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M9 invariants.
2. QA executes at least one counterexample scenario targeting adapter RPC contract drift or missing mandatory adapter wiring.
3. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes are documented with invariant-level verdicts for mandatory-chain contract drift checks.
- Follow-up issue links are present for failures (if any).

### I-0127 (M10-S1)
Assertions:
1. Injected transient failures in mandatory-chain fetch/normalize/ingest paths do not advance persisted cursor or watermark state before successful commit.
2. Fail-first then succeed replay runs for Solana and Base preserve deterministic ordered canonical tuples and produce zero duplicate canonical IDs.
3. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic failure-injection tests exist for at least one fetch/normalize/ingest boundary on both mandatory chains.
- Recovery replay tests show `0` canonical tuple diffs and `0` duplicate IDs after retry success.
- Runtime wiring/adapter parity regression tests remain green.

### I-0128 (M10-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M10 invariants.
2. QA executes at least one counterexample scenario that injects transient failure before commit and verifies deterministic retry recovery.
3. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes are documented with invariant-level verdicts for fail-first/retry recovery checks.
- Follow-up issue links are present for failures (if any).

### I-0130 (M11-S1)
Assertions:
1. Mandatory-chain retry loops in fetcher/normalizer/ingester use explicit deterministic transient-vs-terminal classification instead of broad retry-on-any-error behavior.
2. Terminal counterexample failures fail on first attempt and do not advance cursor/watermark state.
3. Transient fail-first/retry paths on both mandatory chains recover deterministically with stable canonical tuples and zero duplicate canonical IDs.
4. Retry exhaustion emits explicit stage-scoped diagnostics suitable for QA follow-up issue fanout.

Pass Evidence:
- Unit tests cover representative transient and terminal classification outcomes used by runtime retry loops.
- Stage-level tests prove terminal no-retry behavior and bounded transient retry behavior.
- Dual-chain replay/idempotency/cursor regressions remain green with no duplicate canonical IDs or cursor regression.

### I-0131 (M11-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M11 invariants.
2. QA executes both transient and terminal counterexample scenarios for retry-boundary behavior.
3. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of terminal no-retry and transient bounded-retry behavior.
- Follow-up issue links are present for failures (if any).

### I-0135 (M12-S1)
Assertions:
1. A decode error on one signature does not block normalization/ingestion of later decodable signatures in the same deterministic input batch for both mandatory chains.
2. Decode-failed signatures emit deterministic stage-scoped diagnostics that include reproducible signature-level failure context.
3. Mixed success+decode-failure replay runs preserve canonical no-dup behavior and cursor monotonicity.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject decode failures at controlled positions and prove suffix continuity on `solana-devnet` and `base-sepolia`.
- Replay tests show `0` duplicate canonical IDs and stable tuple ordering for mixed success+decode-failure fixtures.
- Diagnostic assertions prove identical decode-failure outputs across independent runs on the same fixtures.

### I-0136 (M12-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M12 invariants.
2. QA executes at least one counterexample where a decode failure is injected before decodable suffix signatures and verifies suffix continuity behavior.
3. QA executes at least one counterexample for full-batch decode collapse and verifies deterministic fail-fast behavior.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of both partial-failure isolation and full-batch decode-collapse behavior.
- Follow-up issue links are present for failures (if any).

### I-0138 (M13-S1)
Assertions:
1. Equivalent logical fetch inputs with permuted provider return order converge to identical deterministic canonical tuple ordering on both mandatory chains.
2. Adjacent-window/page overlap duplicates are deterministically suppressed so each logical transaction/signature identity is emitted once.
3. Overlap-heavy replay runs preserve canonical no-dup behavior and cursor monotonicity without balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests explicitly permute fetch ordering on `solana-devnet` and `base-sepolia` and show `0` canonical tuple diffs.
- Overlap-window duplicate tests show `0` duplicate canonical IDs with stable canonical ordering.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0139 (M13-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M13 invariants.
2. QA executes at least one counterexample with order-permuted fetch results and verifies deterministic canonical tuple equivalence.
3. QA executes at least one counterexample with overlap-duplicated adjacent windows/pages and verifies deterministic duplicate suppression.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of both order-permutation equivalence and overlap-dedupe determinism on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0141 (M14-S1)
Assertions:
1. Equivalent logical transaction/signature identities represented with alias variants (case differences, optional prefix forms, provider-format variance) converge to one deterministic canonical identity on both mandatory chains.
2. Alias variants cannot bypass duplicate suppression boundaries and cannot produce duplicate canonical IDs in replay-equivalent runs.
3. Alias-mixed replay runs preserve canonical no-dup behavior and cursor monotonicity without balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject alias variants on `solana-devnet` and `base-sepolia` and show `0` canonical tuple diffs across independent runs.
- Alias+overlap duplicate tests show `0` duplicate canonical IDs with stable canonical ordering.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0142 (M14-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M14 invariants.
2. QA executes at least one counterexample with canonical identity alias variants and verifies deterministic canonical tuple equivalence.
3. QA executes at least one counterexample combining alias variants with overlap duplicates and verifies deterministic duplicate suppression.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of both alias-equivalence determinism and alias+overlap duplicate suppression behavior on mandatory chains.
- Follow-up issue links are present for failures (if any).

## Release Blockers
Release recommendation must be `fail` if any condition holds:
1. Duplicate canonical IDs detected after replay.
2. Fee completeness invariant fails on either chain.
3. Determinism comparison differs between independent runs on same range.
4. Required validation commands fail.
5. Mandatory chain adapter runtime wiring cannot be proven in runtime-path evidence.
6. Mandatory-chain adapter RPC contract parity cannot be proven in deterministic test evidence.
7. Transient-failure recovery invariants cannot be proven with deterministic fail-first/retry evidence for both mandatory chains.
8. Retry-boundary determinism cannot be proven (terminal no-retry and transient bounded-retry behavior not evidenced for both mandatory chains).
9. Decode-error isolation determinism cannot be proven (partial-failure suffix continuity and full-batch fail-fast behavior not evidenced for both mandatory chains).
10. Fetch-order canonicalization determinism cannot be proven (order-permutation equivalence and overlap-dedupe stability not evidenced for both mandatory chains).
11. Canonical identity alias determinism cannot be proven (alias-equivalent identity convergence and alias+overlap duplicate suppression not evidenced for both mandatory chains).
