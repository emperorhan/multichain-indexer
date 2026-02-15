# IMPLEMENTATION_PLAN.md

- Source PRD: `PRD.md v2.0` (2026-02-13)
- Execution mode: local Ralph loop (`.ralph/` markdown queue)
- Runtime targets: `solana-devnet`, `base-sepolia`
- Mission-critical target: canonical normalizer that indexes all asset-volatility events without duplicates

## Program Graph
`M1 -> (M2 || M3) -> M4 -> M5 -> M6 -> M7 -> M8 -> M9 -> M10 -> M11 -> M12 -> M13 -> M14 -> M15 -> M16 -> M17 -> M18 -> M19 -> M20`

Execution queue (dependency-ordered):
1. `I-0102` (`M1-S1`) canonical envelope + schema scaffolding
2. `I-0103` (`M1-S2/S3`) deterministic `event_id` + idempotent ingestion
3. `I-0104` (`M2`) Solana dedup + fee completeness
4. `I-0105` (`M3`) Base fee decomposition
5. `I-0108` (`M4-S1`) reorg detection + rollback wiring
6. `I-0109` (`M4-S2`) replay determinism + cursor monotonicity hardening
7. `I-0107` (`M5`) QA golden/invariant release gate
8. `I-0110` (`M6`) Base Sepolia runtime pipeline wiring
9. `I-0114` (`M7-S1`) runtime wiring drift guard + dual-chain replay smoke
10. `I-0115` (`M7-S2`) QA counterexample gate for runtime wiring + replay invariants
11. `I-0117` (`M8-S1`) failed-transaction fee completeness + replay safety hardening
12. `I-0118` (`M8-S2`) QA counterexample gate for failed-transaction fee coverage
13. `I-0122` (`M9-S1`) adapter RPC contract parity hardening + deterministic regression guard
14. `I-0123` (`M9-S2`) QA counterexample gate for adapter contract drift + runtime/replay invariants
15. `I-0127` (`M10-S1`) transient-failure recovery hardening + deterministic retry-resume guard
16. `I-0128` (`M10-S2`) QA counterexample gate for transient-failure recovery + duplicate/cursor invariants
17. `I-0130` (`M11-S1`) deterministic retry-boundary hardening across fetch/normalize/ingest
18. `I-0131` (`M11-S2`) QA counterexample gate for retry-boundary classification + invariant safety
19. `I-0135` (`M12-S1`) deterministic decode-error isolation + suffix continuity hardening
20. `I-0136` (`M12-S2`) QA counterexample gate for decode-error isolation + invariant safety
21. `I-0138` (`M13-S1`) deterministic fetch-order canonicalization + overlap duplicate suppression hardening
22. `I-0139` (`M13-S2`) QA counterexample gate for fetch-order/overlap dedupe determinism + invariant safety
23. `I-0141` (`M14-S1`) canonical identity alias normalization + duplicate-suppression boundary hardening
24. `I-0142` (`M14-S2`) QA counterexample gate for canonical identity alias determinism + invariant safety
25. `I-0144` (`M15-S1`) finality-transition canonical dedupe/update hardening
26. `I-0145` (`M15-S2`) QA counterexample gate for finality-transition determinism + invariant safety
27. `I-0147` (`M16-S1`) rollback/finality convergence dedupe hardening
28. `I-0148` (`M16-S2`) QA counterexample gate for rollback/finality convergence determinism + invariant safety
29. `I-0150` (`M17-S1`) cursor-boundary canonical continuity hardening
30. `I-0151` (`M17-S2`) QA counterexample gate for cursor-boundary determinism + invariant safety
31. `I-0155` (`M18-S1`) watched-address fan-in canonical dedupe hardening
32. `I-0156` (`M18-S2`) QA counterexample gate for watched-address fan-in determinism + invariant safety
33. `I-0160` (`M19-S1`) lag-aware watched-address fan-in cursor continuity hardening
34. `I-0161` (`M19-S2`) QA counterexample gate for lag-aware fan-in cursor continuity + invariant safety
35. `I-0165` (`M20-S1`) dual-chain tick interleaving determinism hardening
36. `I-0166` (`M20-S2`) QA counterexample gate for dual-chain interleaving determinism + invariant safety

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
5. Runtime wiring invariants stay enforced for mandatory chains (`solana-devnet`, `base-sepolia`).

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

### M6. Base Sepolia Runtime Wiring (P0, Completed)

#### Objective
Move Base Sepolia from normalization-only coverage into deterministic runtime orchestration.

#### Slice
1. `I-0110`: runtime target wiring + Base fetch/decode/normalize/ingest end-to-end regression coverage.

#### Exit Gate (Measurable)
1. Runtime target builder includes both mandatory chains in deterministic order.
2. Base runtime e2e replay path remains idempotent.
3. Validation commands pass.

### M7. Runtime Reliability Tranche C0001 (P0, Completed)

#### Objective
Harden post-wiring runtime reliability so mandatory chain adapters cannot silently drift from configured runtime targets and replay invariants remain provable in dual-chain runtime paths.

#### Entry Gate
- `M6` exit gate green.
- Mandatory chain set fixed to `solana-devnet` + `base-sepolia`.

#### Slices
1. `M7-S1` (`I-0114`): implement runtime wiring drift guard + deterministic dual-chain replay smoke checks.
2. `M7-S2` (`I-0115`): QA counterexample validation and invariant evidence report for the M7-S1 increment.

#### Definition Of Done
1. Startup/runtime preflight fails fast when a mandatory chain target is declared but not adapter-wired.
2. Dual-chain replay smoke run proves idempotent canonical persistence (`event_id` no-dup) and cursor monotonicity.
3. QA report captures at least one negative/counterexample scenario and explicitly records pass/fail disposition.

#### Test Contract
1. Runtime unit/integration tests assert target-to-adapter parity for both mandatory chains.
2. Replay smoke test executes two consecutive runs over identical dual-chain fixture input and asserts:
- `0` duplicate canonical event IDs.
- no cursor regression.
3. QA executes required validation commands and documents counterexample checks under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `100%` mandatory target-to-adapter parity at runtime preflight.
2. `0` duplicate canonical IDs in dual-chain replay smoke.
3. `0` cursor monotonicity violations in M7 regression tests.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: strict fail-fast wiring checks may surface latent environment misconfiguration in local/operator runs.
- Fallback: retain strict checks in test/CI and allow explicit operator override flag only with auditable warning + QA follow-up issue.

### M8. Failure-Path Fee Completeness Tranche C0002 (P0, Completed)

#### Objective
Close the remaining asset-volatility gap by ensuring failed transactions emit deterministic fee deltas when chain metadata includes fee payer/amount, while preserving replay/idempotency and runtime wiring invariants.

#### Entry Gate
- `M7` exit gate green.
- Mandatory chain runtime wiring invariant remains green for `solana-devnet` and `base-sepolia`.

#### Slices
1. `M8-S1` (`I-0117`): implement failed-transaction fee normalization/ingestion hardening for Solana + Base with deterministic replay-safe identity.
2. `M8-S2` (`I-0118`): QA counterexample validation and invariant evidence report for failed-transaction fee coverage paths.

#### Definition Of Done
1. Failed transactions with non-zero fee metadata and known payer emit explicit signed fee debit events.
2. Replay of identical failed-transaction fixture batches remains idempotent (`event_id` no-dup, no double-apply balances).
3. Existing runtime wiring guard behavior remains enforced for mandatory chains.
4. QA report captures positive and negative/counterexample outcomes for failed-transaction fee paths.

#### Test Contract
1. Solana and Base fixture tests assert deterministic fee event emission for failed transactions where metadata is present.
2. Replay tests over mixed success+failed fixture batches assert stable ordered canonical tuples and no duplicate fee events.
3. Counterexample tests assert deterministic no-op behavior when failed-transaction fee metadata is incomplete (missing payer and/or missing fee amount).
4. QA executes required validation commands and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `100%` failed fixture transactions with complete fee metadata emit canonical fee debit event(s).
2. `0` duplicate canonical IDs in failed-path replay fixtures.
3. `0` cursor monotonicity violations in mixed success/failed replay paths.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: RPC variability may omit failed-transaction fee metadata on some providers.
- Fallback: keep deterministic no-op for incomplete metadata, emit explicit unavailable marker for observability, and file follow-up issue for provider-specific gap closure.

### M9. Adapter Contract Reliability Tranche C0003 (P0, Completed)

#### Objective
Prevent RPC interface drift between mandatory chain runtime clients and test doubles from silently breaking reliability gates, while preserving canonical/replay/cursor/runtime invariants.

#### Entry Gate
- `M8` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M9-S1` (`I-0122`): implement adapter RPC contract parity guardrails for mandatory chains and add deterministic regression tests that fail fast on contract drift.
2. `M9-S2` (`I-0123`): execute QA counterexample gate for contract drift and runtime/replay invariants; produce pass/fail evidence with follow-up issue fanout on failure.

#### Definition Of Done
1. Required RPC method contracts are explicit and exercised for mandatory chain runtime adapters and their test doubles.
2. Contract drift is caught by deterministic tests/lint before runtime execution.
3. Canonical identity uniqueness, replay idempotency, cursor monotonicity, and runtime adapter wiring invariants remain green.
4. QA report records at least one counterexample-oriented contract-drift check and explicit disposition.

#### Test Contract
1. Contract-parity tests assert mandatory chain runtime RPC clients and corresponding test doubles satisfy required method sets.
2. Runtime wiring tests continue to prove adapter parity for `solana-devnet` and `base-sepolia`.
3. Replay/idempotency regression tests continue to show no duplicate canonical IDs and no cursor regression.
4. QA executes required validation commands and counterexample checks, then records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` mandatory-chain adapter RPC contract-parity violations in targeted test/lint suites.
2. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
3. QA report includes explicit `pass`/`fail` recommendation for `M9`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: strict contract parity checks may require broad test-double updates when adapter interfaces evolve.
- Fallback: phase parity enforcement by mandatory chain adapters first; track non-mandatory parity gaps with explicit backlog issue links until promoted.

### M10. Operational Continuity Reliability Tranche C0004 (P0, Completed)

#### Objective
Guarantee transient failure recovery is deterministic and replay-safe so RPC/decoder/ingestion errors do not silently advance cursors or duplicate canonical asset-volatility events.

#### Entry Gate
- `M9` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M10-S1` (`I-0127`): implement transient failure recovery hardening across mandatory-chain fetch/normalize/ingest paths with deterministic retry-resume behavior.
2. `M10-S2` (`I-0128`): execute QA counterexample gate for fail-first/retry recovery paths and invariant evidence.

#### Definition Of Done
1. Injected transient failures before commit do not advance persisted cursor or watermark state.
2. Retrying the same range after transient failure preserves canonical tuple determinism and does not introduce duplicate canonical IDs.
3. Recovery behavior is proven for both mandatory chains without regressing runtime adapter wiring guarantees.
4. QA report captures at least one transient-failure counterexample scenario with explicit pass/fail disposition.

#### Test Contract
1. Deterministic tests inject transient failures at fetch/normalize/ingest boundaries and assert no cursor/watermark advancement before successful commit.
2. Fail-first then succeed replay tests on Solana and Base assert stable ordered canonical tuples and zero duplicate canonical IDs.
3. Runtime wiring and adapter parity tests remain green for mandatory chains.
4. QA executes required validation commands plus transient-failure counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` cursor or watermark advancement events on injected pre-commit transient failure paths.
2. `0` duplicate canonical IDs in fail-first/retry replay fixtures for mandatory chains.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: retry hardening may hide persistent provider-side faults if transient/permanent error boundaries are too broad.
- Fallback: enforce bounded retries with explicit `transient_recovery_exhausted` diagnostics and required follow-up issue fanout when exhaustion occurs.

### M11. Retry-Boundary Determinism Reliability Tranche C0005 (P0, Completed)

#### Objective
Eliminate nondeterministic retry behavior by enforcing explicit transient-vs-terminal error boundaries so mandatory-chain runtime paths recover when safe, fail fast when terminal, and never compromise canonical/replay/cursor/runtime invariants.

#### Entry Gate
- `M10` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M11-S1` (`I-0130`): implement deterministic retry-boundary classification and stage-aware retry handling across mandatory-chain fetcher/normalizer/ingester paths.
2. `M11-S2` (`I-0131`): execute QA counterexample gate for transient-vs-terminal boundary behavior and invariant evidence.

#### Definition Of Done
1. Retryability is decided by explicit deterministic classification rather than broad "retry on any error" behavior in mandatory-chain runtime stages.
2. Terminal counterexample failures fail on first attempt and do not advance cursor/watermark state.
3. Transient fail-first/retry paths continue to recover deterministically on both mandatory chains with stable canonical tuples and no duplicate canonical IDs.
4. Retry exhaustion produces explicit stage-scoped diagnostics suitable for QA follow-up issue fanout.

#### Test Contract
1. Unit tests cover retryability classification for representative transient and terminal error classes used by fetch/normalize/ingest stages.
2. Stage-level tests assert terminal errors are not retried and transient errors are retried up to bounded limits.
3. Dual-chain replay/idempotency/cursor tests remain green with deterministic canonical tuple ordering and zero duplicate canonical IDs.
4. QA executes required validation commands plus at least one transient and one terminal counterexample scenario, then records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` unexpected retries on terminal counterexample fixtures in mandatory-chain fetch/normalize/ingest tests.
2. `0` cursor or watermark advancement on terminal or retry-exhausted pre-commit failure paths.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: misclassifying provider/client failures can reduce recovery rate (too strict) or mask persistent faults (too broad).
- Fallback: default unknown errors to terminal classification, then promote to retryable only with deterministic tests and explicit QA evidence.

### M12. Decode-Error Isolation Reliability Tranche C0006 (P0, Completed)

#### Objective
Prevent single-signature decode failures from stalling downstream indexing by isolating decode failures deterministically while preserving canonical/replay/cursor/runtime invariants on mandatory chains.

#### Entry Gate
- `M11` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M12-S1` (`I-0135`): implement deterministic decode-error isolation with suffix continuity and auditable per-signature diagnostics for mandatory-chain runtime paths.
2. `M12-S2` (`I-0136`): execute QA counterexample gate for decode-error isolation behavior and invariant evidence.

#### Definition Of Done
1. A decode error tied to one signature does not block normalization/ingestion of later decodable signatures in the same deterministic input batch.
2. Decode-failed signatures produce deterministic, stage-scoped diagnostics with reproducible signature/reason evidence for follow-up.
3. Replay of mixed success+decode-failure fixture batches remains idempotent with stable canonical tuple ordering and no duplicate canonical IDs.
4. Cursor/watermark progression remains monotonic for the processed range and runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject decode errors at controlled signature positions and assert suffix continuity for both `solana-devnet` and `base-sepolia`.
2. Regression tests assert identical decode-failure diagnostics across two independent runs on the same fixtures.
3. Replay/idempotency/cursor tests remain green with no duplicate canonical IDs or cursor regression.
4. QA executes required validation commands plus decode-failure counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `100%` of decodable suffix signatures after injected single-signature decode failure are emitted exactly once in fixture tests for mandatory chains.
2. `0` duplicate canonical IDs in mixed success+decode-failure replay fixtures.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-permissive decode-error isolation can hide broad sidecar/provider outages.
- Fallback: keep deterministic failure threshold guardrails (fail-fast on full-batch decode collapse) while preserving per-signature isolation for bounded partial failures, with QA follow-up issue fanout.

### M13. Fetch-Order Canonicalization Reliability Tranche C0007 (P0, Completed)

#### Objective
Eliminate provider-ordering and overlapping-page nondeterminism by canonicalizing fetch ordering and suppressing deterministic overlap duplicates before normalization, while preserving canonical/replay/cursor/runtime invariants on mandatory chains.

#### Entry Gate
- `M12` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M13-S1` (`I-0138`): implement deterministic fetch-order canonicalization and overlap duplicate suppression across mandatory-chain runtime fetch->normalize paths.
2. `M13-S2` (`I-0139`): execute QA counterexample gate for order-permutation equivalence and overlap dedupe determinism.

#### Definition Of Done
1. Equivalent fetched transaction/signature sets with different provider return orders normalize into the same deterministic canonical tuple ordering.
2. Deterministic overlap dedupe prevents duplicate canonical emission when adjacent fetch windows/pages return repeated transaction/signature records.
3. Replay of overlap-heavy fixtures remains idempotent with no duplicate canonical IDs and no balance double-apply effects.
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject provider-order permutations for both `solana-devnet` and `base-sepolia` and assert stable ordered canonical tuples.
2. Deterministic tests inject adjacent-window overlap duplicates and assert one canonical emission per logical transaction/signature identity.
3. Replay/idempotency/cursor tests remain green with no duplicate canonical IDs or cursor regression.
4. QA executes required validation commands plus permutation/overlap counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across order-permuted runs over identical logical input for mandatory chains.
2. `0` duplicate canonical IDs in overlap-window replay fixtures.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-aggressive overlap dedupe keying can suppress legitimate distinct records when identity boundaries are underspecified.
- Fallback: enforce conservative dedupe key (`chain + cursor-scope + canonical transaction/signature identity`) and fail-fast with deterministic diagnostics on ambiguous collisions until key contract is extended.

### M14. Canonical Identity Alias Determinism Reliability Tranche C0008 (P0, Completed)

#### Objective
Eliminate canonical identity alias drift (case/prefix/format variance) across fetch->normalize->ingest boundaries so logically identical transactions/signatures always map to one deterministic canonical identity without duplicate canonical emission or replay/cursor drift.

#### Entry Gate
- `M13` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M14-S1` (`I-0141`): implement canonical identity alias normalization and deterministic dedupe boundary hardening across mandatory-chain runtime fetch/normalize/ingest paths.
2. `M14-S2` (`I-0142`): execute QA counterexample gate for identity-alias equivalence and duplicate-suppression determinism.

#### Definition Of Done
1. Equivalent logical transaction/signature identities represented with alias variants (case differences, optional `0x` prefix forms, provider-format variance) converge to one deterministic canonical identity per chain.
2. Alias variants cannot bypass overlap/duplicate suppression boundaries or produce duplicate canonical IDs across replay-equivalent runs.
3. Replay over alias-mixed fixtures remains idempotent with stable canonical tuple ordering and no balance double-apply side effects.
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject identity alias variants for both `solana-devnet` and `base-sepolia` and assert identical ordered canonical tuples across independent runs.
2. Deterministic tests combine alias variants with overlap/adjacent-window duplication and assert one canonical emission per logical identity.
3. Replay/idempotency/cursor tests remain green with no duplicate canonical IDs or cursor regression.
4. QA executes required validation commands plus identity-alias counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across alias-variant equivalent inputs over identical logical ranges for mandatory chains.
2. `0` duplicate canonical IDs in alias+overlap replay fixtures.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-normalizing alias inputs can accidentally collapse legitimately distinct chain identifiers.
- Fallback: keep canonicalization chain-scoped and format-conservative; emit deterministic diagnostics and fail fast on ambiguous alias collisions until contract extensions are added.

### M15. Finality-Transition Determinism Reliability Tranche C0009 (P0, Completed)

#### Objective
Prevent duplicate canonical emission during finality-state transitions by ensuring replay-equivalent events observed at different finality levels converge to one canonical identity with deterministic state evolution.

#### Entry Gate
- `M14` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M15-S1` (`I-0144`): implement deterministic finality-transition canonical dedupe/update semantics across mandatory-chain fetch/normalize/ingest paths.
2. `M15-S2` (`I-0145`): execute QA counterexample gate for finality-transition equivalence and duplicate-suppression determinism.

#### Definition Of Done
1. Replays containing the same logical transaction/signature at weaker and stronger finality states produce one canonical event identity per logical balance delta.
2. Finality-state promotion is deterministic and does not produce duplicate canonical IDs or double-apply balances.
3. Mixed-finality overlap/replay fixtures remain idempotent with stable canonical tuple ordering.
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject weaker->stronger finality observations for both `solana-devnet` and `base-sepolia` and assert stable canonical tuple sets with one canonical ID per logical event.
2. Deterministic tests combine finality transitions with overlap duplicates and assert no duplicate canonical IDs or balance double-apply side effects.
3. Replay/idempotency/cursor tests remain green with no regressions on `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, and `chain_adapter_runtime_wired`.
4. QA executes required validation commands plus finality-transition counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across mixed-finality replay fixtures on mandatory chains.
2. `0` canonical tuple diffs across independent runs of equivalent mixed-finality inputs.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-aggressive finality unification may collapse legitimately distinct lifecycle events if canonical identity boundaries are underspecified.
- Fallback: keep finality transition handling chain-scoped and contract-conservative; emit deterministic diagnostics and fail fast on ambiguous transition collisions until model extension is approved.

### M16. Rollback-Finality Convergence Reliability Tranche C0010 (P0, Completed)

#### Objective
Preserve deterministic canonical identity and balance correctness when previously finality-promoted events are later invalidated by rollback/reorg and replaced by fork-successor data.

#### Entry Gate
- `M15` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M16-S1` (`I-0147`): implement deterministic rollback-aware canonical replacement semantics so reorg-invalidated, finality-promoted events cannot survive as stale duplicates or trigger balance double-apply side effects.
2. `M16-S2` (`I-0148`): execute QA counterexample gate for rollback-after-finality transition determinism and invariant evidence.

#### Definition Of Done
1. Reorg/rollback paths that invalidate previously finality-promoted events deterministically converge to one canonical post-fork event set per logical balance delta.
2. Orphaned pre-fork canonical events cannot persist as duplicate canonical IDs and cannot cause balance double-apply after rollback replay.
3. Repeated rollback+replay cycles over equivalent input ranges remain idempotent with stable canonical tuple ordering.
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject weaker->stronger finality promotion followed by rollback/fork replacement for both `solana-devnet` and `base-sepolia` and assert stable canonical tuple outputs.
2. Deterministic tests replay rollback/fork-replacement fixtures multiple times and assert no stale canonical IDs and no balance double-apply side effects.
3. Replay/idempotency/cursor tests remain green with no regressions on `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, and `chain_adapter_runtime_wired`.
4. QA executes required validation commands plus rollback-after-finality counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` stale or duplicate canonical IDs after rollback/fork replacement replay fixtures on mandatory chains.
2. `0` balance drift across repeated rollback+replay fixture runs.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-pruning during rollback reconciliation can drop valid post-fork events if fork boundaries are computed incorrectly.
- Fallback: keep reconciliation strictly fork-range scoped, emit deterministic rollback-collision diagnostics, and fail fast on ambiguous ancestry boundaries until contract extension is approved.

### M17. Cursor-Boundary Continuity Reliability Tranche C0011 (P0, Completed)

#### Objective
Eliminate canonical duplicate/missing-event risk at adjacent cursor boundaries so restart/resume and shifted batch windows preserve one deterministic canonical event set on mandatory chains.

#### Entry Gate
- `M16` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M17-S1` (`I-0150`): implement deterministic cursor-boundary continuity semantics so adjacent or resumed ranges cannot re-emit or skip boundary events while preserving replay-idempotent canonical outputs.
2. `M17-S2` (`I-0151`): execute QA counterexample gate for boundary-shift/restart determinism and invariant evidence.

#### Definition Of Done
1. Adjacent ingestion windows that share a boundary transaction/signature converge to one canonical event set without duplicate canonical IDs.
2. Restart/resume from a persisted boundary cursor cannot skip in-scope boundary events and cannot double-apply balances.
3. Replay over equivalent ranges with different deterministic batch/window partitioning yields identical ordered canonical tuples.
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject boundary-overlap windows and restart-from-boundary scenarios for both `solana-devnet` and `base-sepolia` and assert stable canonical tuple outputs.
2. Deterministic tests replay the same logical range with at least two deterministic window partition strategies and assert `0` canonical tuple diffs.
3. Replay/idempotency/cursor tests remain green with no regressions on `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, and `chain_adapter_runtime_wired`.
4. QA executes required validation commands plus boundary counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across boundary-overlap and restart-from-boundary replay fixtures on mandatory chains.
2. `0` missing boundary events in deterministic fixture assertions across shifted window partitions.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-aggressive boundary dedupe can suppress legitimate distinct events when identity boundaries are underspecified.
- Fallback: use conservative boundary identity keying (`chain + canonical identity + event path + cursor scope`), emit deterministic boundary-collision diagnostics, and fail fast on unresolved ambiguity until contract extension is approved.

### M18. Watched-Address Fan-In Determinism Reliability Tranche C0012 (P0, Completed)

#### Objective
Eliminate duplicate and missing-event risk when the same logical transaction/signature is discovered through multiple watched-address streams, so fan-in ingestion converges to one deterministic canonical event set on mandatory chains.

#### Entry Gate
- `M17` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M18-S1` (`I-0155`): implement deterministic watched-address fan-in union/dedupe semantics across mandatory-chain fetch/normalize/ingest paths so replay-equivalent multi-address inputs cannot re-emit or drop logical canonical events.
2. `M18-S2` (`I-0156`): execute QA counterexample gate for fan-in overlap/replay determinism and invariant evidence.

#### Definition Of Done
1. Logical transactions/signatures observed from overlapping watched-address inputs converge to one canonical event set per logical balance delta.
2. Replay-equivalent runs over the same range with different watched-address ordering/partitioning produce identical ordered canonical tuples.
3. Fan-in replay remains idempotent with no balance double-apply side effects and no boundary event loss.
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject overlapping watched-address discovery patterns for both `solana-devnet` and `base-sepolia` and assert one canonical emission set per logical transaction/signature identity.
2. Deterministic tests replay the same logical range with at least two fixed watched-address order/partition strategies and assert `0` canonical tuple diffs.
3. Replay/idempotency/cursor tests remain green with no regressions on `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, and `chain_adapter_runtime_wired`.
4. QA executes required validation commands plus watched-address overlap counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across watched-address-overlap replay fixtures on mandatory chains.
2. `0` missing logical canonical events when comparing single-address baseline fixtures vs watched-address fan-in fixtures over equivalent ranges.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-aggressive fan-in dedupe may collapse legitimately distinct per-actor balance deltas that share transaction identity.
- Fallback: use conservative fan-in identity keying (`chain + canonical identity + actor + asset_id + event_path`), emit deterministic fan-in-collision diagnostics, and fail fast on unresolved ambiguity until contract extension is approved.

### M19. Lag-Aware Fan-In Cursor Continuity Reliability Tranche C0013 (P0, Completed)

#### Objective
Eliminate missing-event risk when overlapping watched-address groups have divergent cursor progress or membership churn across ticks, while preserving deterministic duplicate suppression on mandatory chains.

#### Entry Gate
- `M18` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M19-S1` (`I-0160`): implement deterministic lag-aware watched-address fan-in cursor reconciliation so overlap groups cannot skip lagging in-scope events or re-emit canonical duplicates under replay-equivalent runs.
2. `M19-S2` (`I-0161`): execute QA counterexample gate for divergent-cursor overlap and fan-in membership-churn determinism with invariant evidence.

#### Definition Of Done
1. Overlapping watched-address groups with mixed lagging/advanced cursors converge to one canonical event set without skipping lagging-range logical events.
2. Fan-in group membership churn across ticks does not create missing logical events or duplicate canonical IDs.
3. Replay/resume from partial fan-in progress remains idempotent with stable canonical tuple ordering and no balance double-apply side effects.
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject divergent-cursor fan-in overlap scenarios for both `solana-devnet` and `base-sepolia` and assert parity with union-address baseline logical-event coverage.
2. Deterministic tests inject fan-in membership churn across ticks (address add/remove/reorder permutations) and assert `0` canonical tuple diffs with `0` duplicate/missing logical events.
3. Replay/idempotency/cursor tests remain green with no regressions on `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, and `chain_adapter_runtime_wired`.
4. QA executes required validation commands plus lag-aware fan-in counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` missing logical canonical events in divergent-cursor fan-in fixtures when compared against union-address baseline fixtures on mandatory chains.
2. `0` duplicate canonical IDs and `0` canonical tuple diffs across fan-in membership-churn permutation fixtures.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: overly conservative lag-aware cursor reconciliation can widen replay windows and increase duplicate-pressure/performance cost.
- Fallback: keep lag reconciliation deterministic with bounded replay-window guardrails, emit explicit lag-merge diagnostics on ambiguous overlap ranges, and fail fast until fan-in lag-window contracts are extended.

### M20. Dual-Chain Tick Interleaving Determinism Reliability Tranche C0014 (P0, Next)

#### Objective
Eliminate missing/duplicate-event risk from nondeterministic mandatory-chain tick completion order so equivalent dual-chain runs converge to one deterministic canonical output and cursor state.

#### Entry Gate
- `M19` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M20-S1` (`I-0165`): implement deterministic dual-chain tick interleaving/commit semantics so latency-order variance between Solana/Base cannot change canonical output sets or induce cross-chain cursor drift.
2. `M20-S2` (`I-0166`): execute QA counterexample gate for latency-permuted interleaving determinism and cross-chain cursor isolation with invariant evidence.

#### Definition Of Done
1. Equivalent dual-chain logical input ranges produce identical canonical outputs regardless of chain job completion order.
2. A transient stall/failure on one mandatory chain does not cause missing logical events or cursor regression on the other chain.
3. Replay/resume of mixed interleaving outcomes remains idempotent with stable canonical tuple ordering and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for both mandatory chains.

#### Test Contract
1. Deterministic tests inject opposite Solana/Base completion-order permutations and assert `0` canonical tuple diffs for equivalent logical ranges.
2. Deterministic tests inject one-chain lag/transient failure while the other chain succeeds and assert chain-scoped cursor monotonicity plus no missing logical events.
3. Replay/idempotency/cursor tests remain green with no regressions on `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, and `chain_adapter_runtime_wired`.
4. QA executes required validation commands plus dual-chain interleaving counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across latency-permuted dual-chain completion-order fixtures on mandatory chains.
2. `0` cross-chain cursor bleed/regression events in one-chain-lag counterexample fixtures.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: deterministic interleaving barriers can reduce throughput or increase tick latency under asymmetric provider delay.
- Fallback: keep ordering rules chain-scoped with bounded skew guardrails, emit explicit interleaving-skew diagnostics, and fail fast on ambiguous commit ordering until the contract is extended.

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

Completed milestones/slices:
1. `I-0102`
2. `I-0103`
3. `I-0104`
4. `I-0105`
5. `I-0108`
6. `I-0109`
7. `I-0107`
8. `I-0110`
9. `I-0114`
10. `I-0115`
11. `I-0117`
12. `I-0118`
13. `I-0122`
14. `I-0123`
15. `I-0127`
16. `I-0128`
17. `I-0130`
18. `I-0131`
19. `I-0135`
20. `I-0136`
21. `I-0138`
22. `I-0139`
23. `I-0141`
24. `I-0142`
25. `I-0144`
26. `I-0145`
27. `I-0147`
28. `I-0148`
29. `I-0150`
30. `I-0151`
31. `I-0155`
32. `I-0156`
33. `I-0160`
34. `I-0161`

Active downstream queue from this plan:
1. `I-0165`
2. `I-0166`

Superseded issue:
- `I-0106` is superseded by `I-0108` + `I-0109` to keep M4 slices independently releasable.
- `I-0133` and `I-0134` are superseded by `I-0135` and `I-0136` for a clean planner-only handoff after prior scope-violation execution.
- `I-0153` and `I-0154` are superseded by `I-0155` and `I-0156` to replace generic cycle placeholders with executable watched-address fan-in reliability slices.
- `I-0158` and `I-0159` are superseded by `I-0160` and `I-0161` to replace generic cycle placeholders with executable lag-aware fan-in cursor continuity reliability slices.
- `I-0163` and `I-0164` are superseded by `I-0165` and `I-0166` to replace generic cycle placeholders with executable dual-chain tick interleaving reliability slices.
