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

### I-0144 (M15-S1)
Assertions:
1. Replay-equivalent logical events observed at weaker and stronger finality states converge to one deterministic canonical identity per logical balance delta on both mandatory chains.
2. Finality-state promotion/update semantics do not create duplicate canonical IDs and do not double-apply balances.
3. Mixed-finality overlap/replay runs preserve canonical no-dup behavior and cursor monotonicity with stable canonical tuple ordering.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject mixed-finality observations on `solana-devnet` and `base-sepolia` and show `0` duplicate canonical IDs with stable canonical tuples.
- Finality-transition + overlap counterexample tests show no balance double-apply side effects.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0145 (M15-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M15 invariants.
2. QA executes at least one counterexample with weaker->stronger finality transitions and verifies deterministic canonical tuple equivalence.
3. QA executes at least one counterexample combining finality transitions with overlap duplicates and verifies deterministic duplicate suppression and no balance double-apply behavior.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of finality-transition determinism and finality+overlap duplicate suppression behavior on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0147 (M16-S1)
Assertions:
1. Rollback/reorg paths that invalidate previously finality-promoted events converge to one deterministic post-fork canonical event set per logical balance delta on both mandatory chains.
2. Orphaned pre-fork canonical events cannot persist as stale duplicate canonical IDs and cannot trigger balance double-apply during rollback replay.
3. Repeated rollback+replay cycles preserve deterministic canonical tuple ordering and cursor monotonicity.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject finality promotion followed by rollback/fork replacement on `solana-devnet` and `base-sepolia` and show `0` stale or duplicate canonical IDs.
- Rollback+replay fixture tests show `0` balance drift and no double-apply side effects across repeated runs.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0148 (M16-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M16 invariants.
2. QA executes at least one counterexample with finality-promotion then rollback/fork replacement and verifies deterministic canonical convergence.
3. QA executes at least one counterexample with repeated rollback+replay cycles and verifies no stale canonical IDs, no balance double-apply, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of rollback-after-finality determinism and stale-canonical suppression behavior on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0150 (M17-S1)
Assertions:
1. Adjacent ingestion windows sharing a boundary transaction/signature converge to one deterministic canonical event set on both mandatory chains.
2. Restart/resume from persisted boundary cursors cannot skip in-scope boundary events and cannot create duplicate canonical IDs.
3. Replay-equivalent ranges executed with different deterministic window partitions produce identical canonical tuple ordering with no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject boundary-overlap and restart-from-boundary scenarios on `solana-devnet` and `base-sepolia` and show `0` duplicate canonical IDs.
- Deterministic partition-variance tests show `0` canonical tuple diffs and `0` missing boundary events across at least two fixed partition strategies.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0151 (M17-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M17 invariants.
2. QA executes at least one counterexample with boundary-overlap windows and verifies deterministic duplicate suppression with no missing boundary events.
3. QA executes at least one counterexample with restart-from-boundary replay and verifies canonical tuple equivalence across partition strategies.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of boundary-overlap determinism and restart-from-boundary continuity behavior on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0155 (M18-S1)
Assertions:
1. Logical transactions/signatures discovered from overlapping watched-address inputs converge to one deterministic canonical event set on both mandatory chains.
2. Replay-equivalent ranges executed with different watched-address ordering/partitioning produce identical canonical tuple ordering with no duplicate canonical IDs.
3. Fan-in replay remains idempotent with no missing logical canonical events and no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject watched-address overlap/fan-in scenarios on `solana-devnet` and `base-sepolia` and show `0` duplicate canonical IDs.
- Deterministic watched-address partition-variance tests show `0` canonical tuple diffs and `0` missing logical canonical events across at least two fixed strategies.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0156 (M18-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M18 invariants.
2. QA executes at least one counterexample with overlapping watched-address discovery and verifies deterministic duplicate suppression without missing logical events.
3. QA executes at least one counterexample with watched-address order/partition variance and verifies canonical tuple equivalence.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of watched-address fan-in determinism and partition-variance continuity behavior on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0160 (M19-S1)
Assertions:
1. Divergent-cursor watched-address overlap scenarios on both mandatory chains preserve union-baseline logical-event coverage while maintaining deterministic duplicate suppression.
2. Fan-in membership churn scenarios (address add/remove/reorder across ticks) produce deterministic canonical tuple equivalence with zero duplicate or missing logical canonical events.
3. Replay/resume from partial fan-in progress remains idempotent and preserves cursor monotonicity without balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject divergent-cursor fan-in overlap scenarios on `solana-devnet` and `base-sepolia` and show parity with union-address baseline logical-event sets.
- Deterministic fan-in membership-churn permutation tests show `0` canonical tuple diffs with `0` duplicate/missing logical events.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0161 (M19-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M19 invariants.
2. QA executes at least one counterexample with divergent fan-in cursor progress and verifies deterministic completeness (no missing logical events) plus duplicate suppression.
3. QA executes at least one counterexample with fan-in membership churn and verifies canonical tuple equivalence across permutations.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of lag-aware fan-in completeness and churn-variance determinism on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0165 (M20-S1)
Assertions:
1. Equivalent dual-chain logical input ranges produce deterministic canonical tuple equivalence when Solana/Base completion order is permuted.
2. One-chain lag/transient-failure counterexamples do not introduce cross-chain cursor regression or logical-event loss on the non-failing chain.
3. Replay/resume over mixed interleaving outcomes remains idempotent with no duplicate canonical IDs and no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject opposite Solana/Base completion-order permutations and show `0` canonical tuple diffs on equivalent logical ranges.
- Deterministic one-chain-lag counterexample tests show chain-scoped cursor monotonicity with `0` cross-chain cursor bleed and `0` missing logical events.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0166 (M20-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M20 invariants.
2. QA executes at least one counterexample with dual-chain completion-order permutations and verifies deterministic canonical tuple equivalence.
3. QA executes at least one counterexample with one-chain lag/transient failure and verifies chain-scoped cursor isolation (no cross-chain cursor bleed) plus completeness.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of dual-chain interleaving determinism and one-chain-lag cursor-isolation behavior on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0170 (M21-S1)
Assertions:
1. Crash cutpoint permutations around fetch/normalize/ingest/cursor-commit boundaries converge to one deterministic canonical tuple output set for equivalent dual-chain logical input ranges.
2. Resume from each modeled crash cutpoint yields `0` duplicate canonical IDs, `0` missing logical events, and chain-scoped cursor monotonicity with no cross-chain cursor bleed.
3. Repeated crash/restart replay loops remain idempotent with stable canonical tuple ordering and no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject crash cutpoints across opposite Solana/Base completion-order permutations and show `0` canonical tuple diffs.
- Deterministic resume-from-cutpoint tests show `0` duplicate canonical IDs, `0` missing logical events, and no cursor regression.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0171 (M21-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M21 invariants.
2. QA executes at least one counterexample with crash cutpoint permutations and verifies deterministic canonical tuple equivalence across restart/resume paths.
3. QA executes at least one counterexample with repeated crash/restart loops and verifies no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of crash-point permutation determinism and restart/resume invariant safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0175 (M22-S1)
Assertions:
1. Startup/resume validates persisted checkpoint payload integrity and chain-scoped checkpoint consistency before processing new mandatory-chain ranges.
2. Checkpoint corruption modes (truncated payload, stale cursor snapshot, cross-chain checkpoint mix-up) deterministically recover to one last-safe boundary or fail fast with explicit diagnostics, without duplicate or missing logical events.
3. Replay/resume after integrity-triggered recovery remains idempotent with stable canonical tuple ordering, `0` duplicate canonical IDs, and no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject checkpoint corruption/integrity-failure permutations on `solana-devnet` and `base-sepolia` and show `0` canonical tuple diffs after recovery.
- Deterministic recovery tests show `0` duplicate canonical IDs, `0` missing logical events, and chain-scoped cursor monotonicity.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0176 (M22-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M22 invariants.
2. QA executes at least one counterexample with checkpoint corruption permutations and verifies deterministic recovery convergence to canonical tuple equivalence.
3. QA executes at least one counterexample with repeated integrity-triggered restart/recovery loops and verifies no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of checkpoint-integrity recovery determinism and restart/resume invariant safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0178 (M23-S1)
Assertions:
1. Sidecar degradation permutations (temporary unavailable, schema mismatch, parse failure) preserve deterministic canonical outputs for decodable signatures on both mandatory chains.
2. Transient sidecar-unavailable paths use bounded deterministic retries and do not advance cursor/watermark until decode+ingest succeeds.
3. Terminal decode failures are deterministically isolated or fail fast with reproducible signature-level diagnostics, without duplicate canonical IDs or balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject sidecar-unavailable retry permutations on `solana-devnet` and `base-sepolia` and show bounded retry behavior with no pre-commit cursor advancement.
- Deterministic tests inject mixed schema-mismatch/parse-failure signatures plus decodable suffix signatures and show deterministic continuation outputs across independent runs.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0179 (M23-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M23 invariants.
2. QA executes at least one counterexample with sidecar-unavailable bursts and verifies deterministic bounded-retry behavior plus cursor monotonicity.
3. QA executes at least one counterexample with schema-mismatch/parse-failure signatures and verifies deterministic decode-isolation continuity for decodable outputs.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of sidecar-degradation determinism and decode-isolation continuity behavior on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0183 (M24-S1)
Assertions:
1. Ambiguous ingest-commit permutations (ack timeout, post-write disconnect, retry-after-unknown) converge to one deterministic canonical output set for equivalent logical ranges on both mandatory chains.
2. Deterministic reconciliation of unknown commit outcomes prevents duplicate canonical IDs and missing logical events before retry/resume cursor advancement.
3. Replay/resume from ambiguous-commit boundaries remains idempotent with cursor monotonicity and no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject commit-ack timeout/disconnect permutations on `solana-devnet` and `base-sepolia` and show canonical tuple equivalence across independent runs.
- Deterministic retry-after-unknown tests show `0` duplicate canonical IDs and `0` missing logical events.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0184 (M24-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M24 invariants.
2. QA executes at least one counterexample with commit-ack timeout/disconnect permutations and verifies deterministic canonical output convergence.
3. QA executes at least one counterexample with retry-after-unknown replay/resume and verifies no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of ambiguous-commit reconciliation determinism and replay/resume invariant safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0188 (M25-S1)
Assertions:
1. Equivalent logical ranges processed under deterministic batch-partition variants (different chunk sizes and seam boundaries) converge to one canonical output set on both mandatory chains.
2. Split/merge/retry boundary permutations preserve deterministic seam identity reconciliation, with `0` duplicate canonical IDs and `0` missing logical events before cursor advancement.
3. Replay/resume from partition seam boundaries remains idempotent with cursor monotonicity and no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject partition-size and seam-boundary permutations on `solana-devnet` and `base-sepolia` and show canonical tuple equivalence across independent runs.
- Deterministic split/merge/retry seam tests show `0` duplicate canonical IDs and `0` missing logical events.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0189 (M25-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M25 invariants.
2. QA executes at least one counterexample with batch-partition size/seam permutations and verifies deterministic canonical output convergence.
3. QA executes at least one counterexample with split/merge/retry seam replay/resume and verifies no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of batch-partition determinism and seam-boundary replay/resume invariant safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0191 (M26-S1)
Assertions:
1. Equivalent logical ranges under moving-head permutations (head advances during pagination, late append during tick, resume from partial page) converge to one deterministic canonical output set on both mandatory chains.
2. Per-chain fetch processing uses deterministic pinned cutoff boundaries so cursor/watermark only advances through closed ranges, with no duplicate canonical IDs or missing logical events.
3. Replay/resume from moving-head boundaries remains idempotent with cursor monotonicity and no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject head-advance-during-pagination permutations on `solana-devnet` and `base-sepolia` and show canonical tuple equivalence against fixed-head baseline runs.
- Deterministic tests inject late-append/page-overlap permutations and show `0` duplicate canonical IDs and `0` missing logical events.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0192 (M26-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M26 invariants.
2. QA executes at least one counterexample with moving-head pagination permutations and verifies deterministic canonical output convergence.
3. QA executes at least one counterexample with pinned-cutoff replay/resume boundaries and verifies no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of moving-head cutoff determinism and replay/resume invariant safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0194 (M27-S1)
Assertions:
1. Equivalent high-volatility transaction permutations (inner-op reorder, repeated transfer legs, mixed fee+transfer paths) converge to one deterministic canonical output set on both mandatory chains.
2. Per-transaction actor/asset signed-delta conservation is preserved under volatility-burst normalization, while explicit fee events remain deterministic (`solana` transaction fee and `base` L2/L1 fee components where source fields exist).
3. Replay/resume from volatility-burst boundaries remains idempotent with no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject high-volatility permutations on `solana-devnet` and `base-sepolia` and show canonical tuple equivalence across independent runs.
- Deterministic signed-delta conservation checks show `0` actor/asset aggregate drift while explicit fee-event expectations remain satisfied.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0195 (M27-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M27 invariants.
2. QA executes at least one counterexample with volatility-burst permutation variance and verifies deterministic canonical output convergence.
3. QA executes at least one counterexample with signed-delta conservation plus fee-event coexistence checks and verifies no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of volatility-burst normalizer determinism, signed-delta conservation, and fee-event coexistence safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0199 (M28-S1)
Assertions:
1. Equivalent logical ranges under sidecar degradation->recovery permutations (temporary unavailable, schema mismatch then recovered, mixed recovered+already-decoded signatures) converge to one deterministic canonical output set on both mandatory chains.
2. Recovered signatures reconcile to deterministic canonical identities against prior processed ranges with `0` duplicate canonical IDs and `0` missing logical events.
3. Replay/resume from deferred-recovery boundaries remains idempotent with cursor monotonicity and no balance double-apply side effects.
4. Existing invariants remain green: canonical ID uniqueness, replay idempotency, cursor monotonicity, runtime adapter wiring.

Pass Evidence:
- Deterministic tests inject degradation->recovery permutations on `solana-devnet` and `base-sepolia` and show canonical tuple equivalence against fully-decodable baselines.
- Deterministic recovered+already-decoded replay tests show `0` duplicate canonical IDs and `0` missing logical events.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0200 (M28-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M28 invariants.
2. QA executes at least one counterexample with degradation->recovery permutations and verifies deterministic canonical output convergence.
3. QA executes at least one counterexample validating recovered-signature reconciliation with no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of deferred sidecar-recovery determinism and replay/resume invariant safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0204 (M29-S1)
Assertions:
1. Equivalent logical ranges under live-first, backfill-first, and interleaved source-order permutations converge to one deterministic canonical output set on both mandatory chains.
2. Live/backfill overlap reconciliation preserves deterministic canonical identity boundaries with `0` duplicate canonical IDs and `0` missing logical events.
3. Overlap permutations preserve signed-delta conservation and explicit fee-event coexistence expectations where source fields exist.
4. Replay/resume from live/backfill overlap boundaries remains idempotent with cursor monotonicity and no balance double-apply side effects.

Pass Evidence:
- Deterministic tests inject live-first, backfill-first, and interleaved overlap permutations on `solana-devnet` and `base-sepolia` and show canonical tuple equivalence against single-source baselines.
- Deterministic overlap tests show `0` duplicate canonical IDs and `0` missing logical events while signed-delta and explicit fee-event checks remain satisfied.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0205 (M29-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M29 invariants.
2. QA executes at least one counterexample with live/backfill source-order permutations and verifies deterministic canonical output convergence.
3. QA executes at least one counterexample validating overlap replay/resume safety with no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of live/backfill overlap determinism and replay/resume invariant safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0209 (M30-S1)
Assertions:
1. Equivalent logical ranges under legacy-decoder, upgraded-decoder, and mixed-version interleaving permutations converge to one deterministic canonical output set on both mandatory chains.
2. Decoder-version transition reconciliation preserves deterministic canonical identity boundaries with `0` duplicate canonical IDs and `0` missing logical events.
3. Mixed-version permutations preserve signed-delta conservation and explicit fee-event coexistence expectations where source fields exist.
4. Replay/resume from decoder-version transition boundaries remains idempotent with cursor monotonicity and no balance double-apply side effects.

Pass Evidence:
- Deterministic tests inject legacy-decoder, upgraded-decoder, and mixed-version permutations on `solana-devnet` and `base-sepolia` and show canonical tuple equivalence against stable single-version baselines.
- Deterministic mixed-version reconciliation tests show `0` duplicate canonical IDs and `0` missing logical events while signed-delta and explicit fee-event checks remain satisfied.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0210 (M30-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M30 invariants.
2. QA executes at least one counterexample with legacy-vs-upgraded decoder-version permutations and verifies deterministic canonical output convergence.
3. QA executes at least one counterexample validating mixed-version replay/resume safety with no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of decoder-version transition determinism and mixed-version replay/resume invariant safety on mandatory chains.
- Follow-up issue links are present for failures (if any).

### I-0214 (M31-S1)
Assertions:
1. Equivalent logical ranges under sparse-decoder, enriched-decoder, and mixed sparse/enriched interleaving permutations converge to one deterministic canonical output set on both mandatory chains.
2. Incremental decode-coverage reconciliation preserves deterministic canonical identity boundaries with `0` duplicate canonical IDs while emitting newly discovered logical events exactly once.
3. Sparse/enriched permutations preserve signed-delta conservation and explicit fee-event coexistence expectations where source fields exist.
4. Replay/resume from incremental decode-coverage boundaries remains idempotent with cursor monotonicity and no balance double-apply side effects.

Pass Evidence:
- Deterministic tests inject sparse-decoder, enriched-decoder, and mixed decode-coverage permutations on `solana-devnet` and `base-sepolia` and show canonical tuple equivalence against deterministic enriched baselines.
- Deterministic incremental-coverage reconciliation tests show `0` duplicate canonical IDs for already-materialized events and deterministic one-time emission for newly discovered events.
- Replay/idempotency/cursor regression tests remain green with no runtime adapter wiring regressions.

### I-0215 (M31-S2)
Assertions:
1. QA report is written under `.ralph/reports/` with explicit pass/fail recommendation for M31 invariants.
2. QA executes at least one counterexample with sparse-vs-enriched decode-coverage permutations and verifies deterministic canonical output convergence.
3. QA executes at least one counterexample validating incremental-coverage replay/resume safety with no duplicate canonical IDs, no missing logical events, and cursor monotonicity.
4. Any failed invariant is mapped to a reproducible developer issue under `.ralph/issues/`.

Pass Evidence:
- QA report includes command evidence for `make test`, `make test-sidecar`, `make lint`.
- Counterexample outcomes include explicit proof of incremental decode-coverage determinism and replay/resume invariant safety on mandatory chains.
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
12. Finality-transition determinism cannot be proven (mixed-finality equivalence, finality+overlap duplicate suppression, and no balance double-apply behavior not evidenced for both mandatory chains).
13. Rollback-after-finality convergence determinism cannot be proven (fork-replacement convergence, stale-canonical suppression, replay idempotency, and cursor monotonicity not evidenced for both mandatory chains).
14. Cursor-boundary continuity determinism cannot be proven (boundary-overlap convergence, restart-from-boundary completeness, partition-variance equivalence, and no duplicate/missing boundary events not evidenced for both mandatory chains).
15. Watched-address fan-in determinism cannot be proven (overlap convergence, watched-address partition/order variance equivalence, and no duplicate/missing logical canonical events not evidenced for both mandatory chains).
16. Lag-aware fan-in cursor continuity determinism cannot be proven (divergent-cursor completeness parity to union baseline, fan-in membership-churn permutation equivalence, and no duplicate/missing logical canonical events not evidenced for both mandatory chains).
17. Dual-chain tick interleaving determinism cannot be proven (completion-order permutation equivalence, one-chain-lag cursor isolation, and no duplicate/missing logical canonical events not evidenced for both mandatory chains).
18. Crash-recovery checkpoint determinism cannot be proven (crash-cutpoint permutation equivalence, restart/resume completeness, and no duplicate/missing logical canonical events not evidenced for both mandatory chains).
19. Checkpoint-integrity recovery determinism cannot be proven (corrupted checkpoint detection, deterministic recovery/fail-fast behavior, and post-recovery replay/cursor invariants not evidenced for both mandatory chains).
20. Sidecar-degradation determinism cannot be proven (bounded retry semantics for transient sidecar outage, deterministic terminal decode-failure isolation/fail-fast behavior, and no duplicate/missing decodable canonical outputs across permutations on both mandatory chains).
21. Ambiguous ingest-commit determinism cannot be proven (commit-ack timeout/disconnect reconciliation, retry-after-unknown replay equivalence, and no duplicate/missing logical canonical outputs with cursor monotonicity not evidenced for both mandatory chains).
22. Batch-partition variance determinism cannot be proven (partition-size/seam-boundary permutation convergence, split/merge/retry seam reconciliation, and no duplicate/missing logical canonical outputs with cursor monotonicity not evidenced for both mandatory chains).
23. Moving-head fetch cutoff determinism cannot be proven (head-advance-during-pagination convergence, pinned-cutoff closed-range coverage, and no duplicate/missing logical canonical outputs with cursor monotonicity not evidenced for both mandatory chains).
24. Volatility-burst normalizer determinism cannot be proven (dense same-transaction permutation convergence, actor/asset signed-delta conservation, and explicit fee-event coexistence with no duplicate/missing logical canonical outputs and cursor monotonicity not evidenced for both mandatory chains).
25. Deferred sidecar-recovery backfill determinism cannot be proven (degradation->recovery permutation convergence, recovered-signature canonical identity reconciliation against prior ranges, and no duplicate/missing logical canonical outputs with cursor monotonicity not evidenced for both mandatory chains).
26. Live/backfill overlap canonical convergence determinism cannot be proven (source-order permutation convergence, overlap reconciliation stability, and no duplicate/missing logical canonical outputs with signed-delta/fee-event invariants and cursor monotonicity not evidenced for both mandatory chains).
27. Decoder-version transition canonical convergence determinism cannot be proven (legacy-vs-upgraded decode permutation convergence, mixed-version reconciliation stability, and no duplicate/missing logical canonical outputs with signed-delta/fee-event invariants and cursor monotonicity not evidenced for both mandatory chains).
28. Incremental decode-coverage canonical convergence determinism cannot be proven (sparse-vs-enriched decode permutation convergence, one-time enrichment emission stability, and no duplicate/missing logical canonical outputs with signed-delta/fee-event invariants and cursor monotonicity not evidenced for both mandatory chains).
