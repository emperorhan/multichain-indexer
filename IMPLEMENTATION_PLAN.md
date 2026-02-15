# IMPLEMENTATION_PLAN.md

- Source PRD: `PRD.md v2.3` (2026-02-15)
- Execution mode: local Ralph loop (`.ralph/` markdown queue)
- Mandatory runtime targets: `solana-devnet`, `base-sepolia`, `btc-testnet`
- Mission-critical target: canonical normalizer that indexes all asset-volatility events without duplicates

## Program Graph
`M1 -> (M2 || M3) -> M4 -> M5 -> M6 -> M7 -> M8 -> M9 -> M10 -> M11 -> M12 -> M13 -> M14 -> M15 -> M16 -> M17 -> M18 -> M19 -> M20 -> M21 -> M22 -> M23 -> M24 -> M25 -> M26 -> M27 -> M28 -> M29 -> M30 -> M31 -> M32 -> M33 -> M34 -> M35 -> M36 -> M37 -> M38 -> M39 -> M40 -> M41 -> M42 -> M43 -> M44 -> M45 -> M46 -> M47 -> M48 -> M49 -> M50 -> M51 -> M52 -> M53 -> M54 -> M55 -> M56 -> M57 -> M58 -> M59 -> M60 -> M61`

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
37. `I-0170` (`M21-S1`) tick checkpoint crash-recovery determinism hardening
38. `I-0171` (`M21-S2`) QA counterexample gate for crash-point permutation determinism + invariant safety
39. `I-0175` (`M22-S1`) checkpoint integrity self-healing determinism hardening
40. `I-0176` (`M22-S2`) QA counterexample gate for checkpoint-integrity recovery determinism + invariant safety
41. `I-0178` (`M23-S1`) sidecar decode degradation determinism hardening
42. `I-0179` (`M23-S2`) QA counterexample gate for sidecar-degradation determinism + invariant safety
43. `I-0183` (`M24-S1`) ambiguous ingest-commit acknowledgment determinism hardening
44. `I-0184` (`M24-S2`) QA counterexample gate for commit-ambiguity replay determinism + invariant safety
45. `I-0188` (`M25-S1`) batch-partition variance determinism hardening
46. `I-0189` (`M25-S2`) QA counterexample gate for batch-partition replay determinism + invariant safety
47. `I-0191` (`M26-S1`) moving-head fetch cutoff determinism hardening
48. `I-0192` (`M26-S2`) QA counterexample gate for moving-head fetch determinism + invariant safety
49. `I-0194` (`M27-S1`) volatility-burst normalizer canonical fold determinism hardening
50. `I-0195` (`M27-S2`) QA counterexample gate for volatility-burst normalizer determinism + invariant safety
51. `I-0199` (`M28-S1`) deferred sidecar-recovery backfill determinism hardening
52. `I-0200` (`M28-S2`) QA counterexample gate for deferred sidecar-recovery determinism + invariant safety
53. `I-0204` (`M29-S1`) live/backfill overlap canonical convergence determinism hardening
54. `I-0205` (`M29-S2`) QA counterexample gate for live/backfill overlap determinism + invariant safety
55. `I-0209` (`M30-S1`) decoder-version transition canonical convergence determinism hardening
56. `I-0210` (`M30-S2`) QA counterexample gate for decoder-version transition determinism + invariant safety
57. `I-0214` (`M31-S1`) incremental decode-coverage canonical convergence determinism hardening
58. `I-0215` (`M31-S2`) QA counterexample gate for incremental decode-coverage determinism + invariant safety
59. `I-0219` (`M32-S1`) decode-coverage regression flap canonical stability determinism hardening
60. `I-0220` (`M32-S2`) QA counterexample gate for decode-coverage regression flap determinism + invariant safety
61. `I-0224` (`M33-S1`) fee-component availability flap canonical convergence determinism hardening
62. `I-0225` (`M33-S2`) QA counterexample gate for fee-component availability flap determinism + invariant safety
63. `I-0226` (`M34-S1`) panic-on-error fail-fast contract hardening (no unsafe progression)
64. `I-0227` (`M34-S2`) QA failure-injection gate for fail-fast panic + cursor/watermark safety
65. `I-0228` (`M35-S1`) BTC-like runtime activation (`btc-testnet`) + canonical UTXO/fee semantics wiring
66. `I-0229` (`M35-S2`) QA golden/invariant/topology-parity gate for BTC-like activation
67. `I-0232` (`M36-S1`) BTC reorg/finality flap canonical convergence determinism hardening
68. `I-0233` (`M36-S2`) QA counterexample gate for BTC reorg/finality flap recovery determinism + invariant safety
69. `I-0237` (`M37-S1`) tri-chain volatility-burst interleaving deterministic convergence hardening
70. `I-0238` (`M37-S2`) QA counterexample gate for tri-chain volatility/interleaving determinism + invariant safety
71. `I-0242` (`M38-S1`) tri-chain late-arrival backfill canonical closure determinism hardening
72. `I-0243` (`M38-S2`) QA counterexample gate for tri-chain late-arrival closure determinism + invariant safety
73. `I-0247` (`M39-S1`) tri-chain volatility-event completeness reconciliation determinism hardening
74. `I-0248` (`M39-S2`) QA counterexample gate for tri-chain volatility-event completeness determinism + invariant safety
75. `I-0249` (`M40-S1`) chain-scoped coordinator auto-tune control loop hardening
76. `I-0250` (`M40-S2`) QA counterexample gate for chain-scoped auto-tune + fail-fast/backpressure invariants
77. `I-0254` (`M41-S1`) auto-tune restart/profile-transition determinism hardening
78. `I-0255` (`M41-S2`) QA counterexample gate for auto-tune restart/profile-transition determinism + invariant safety
79. `I-0259` (`M42-S1`) auto-tune signal-flap hysteresis determinism hardening
80. `I-0260` (`M42-S2`) QA counterexample gate for auto-tune signal-flap hysteresis determinism + invariant safety
81. `I-0264` (`M43-S1`) auto-tune saturation/de-saturation envelope determinism hardening
82. `I-0265` (`M43-S2`) QA counterexample gate for auto-tune saturation/de-saturation determinism + invariant safety
83. `I-0267` (`M44-S1`) auto-tune telemetry-staleness fallback determinism hardening
84. `I-0268` (`M44-S2`) QA counterexample gate for auto-tune telemetry-staleness fallback determinism + invariant safety
85. `I-0272` (`M45-S1`) auto-tune operator-override reconciliation determinism hardening
86. `I-0273` (`M45-S2`) QA counterexample gate for auto-tune operator-override reconciliation determinism + invariant safety
87. `I-0277` (`M46-S1`) auto-tune policy-version rollout reconciliation determinism hardening
88. `I-0278` (`M46-S2`) QA counterexample gate for auto-tune policy-version rollout determinism + invariant safety
89. `I-0282` (`M47-S1`) auto-tune policy-manifest refresh reconciliation determinism hardening
90. `I-0283` (`M47-S2`) QA counterexample gate for auto-tune policy-manifest refresh determinism + invariant safety
91. `I-0287` (`M48-S1`) auto-tune policy-manifest sequence-gap recovery reconciliation determinism hardening
92. `I-0288` (`M48-S2`) QA counterexample gate for auto-tune policy-manifest sequence-gap recovery determinism + invariant safety
93. `I-0292` (`M49-S1`) auto-tune policy-manifest snapshot-cutover reconciliation determinism hardening
94. `I-0293` (`M49-S2`) QA counterexample gate for auto-tune policy-manifest snapshot-cutover determinism + invariant safety
95. `I-0297` (`M50-S1`) auto-tune policy-manifest rollback-lineage reconciliation determinism hardening
96. `I-0298` (`M50-S2`) QA counterexample gate for auto-tune policy-manifest rollback-lineage determinism + invariant safety
97. `I-0300` (`M51-S1`) auto-tune policy-manifest rollback-crashpoint replay determinism hardening
98. `I-0301` (`M51-S2`) QA counterexample gate for auto-tune policy-manifest rollback-crashpoint determinism + invariant safety
99. `I-0305` (`M52-S1`) auto-tune policy-manifest rollback-crashpoint checkpoint-fence determinism hardening
100. `I-0306` (`M52-S2`) QA counterexample gate for auto-tune policy-manifest rollback-crashpoint checkpoint-fence determinism + invariant safety
101. `I-0308` (`M53-S1`) auto-tune policy-manifest rollback checkpoint-fence epoch-compaction determinism hardening
102. `I-0309` (`M53-S2`) QA counterexample gate for auto-tune rollback checkpoint-fence epoch-compaction determinism + invariant safety
103. `I-0313` (`M54-S1`) auto-tune policy-manifest rollback checkpoint-fence tombstone-expiry determinism hardening
104. `I-0314` (`M54-S2`) QA counterexample gate for auto-tune rollback checkpoint-fence tombstone-expiry determinism + invariant safety
105. `I-0318` (`M55-S1`) auto-tune policy-manifest rollback checkpoint-fence post-expiry late-marker quarantine determinism hardening
106. `I-0319` (`M55-S2`) QA counterexample gate for auto-tune rollback checkpoint-fence post-expiry late-marker quarantine determinism + invariant safety
107. `I-0321` (`M56-S1`) auto-tune policy-manifest rollback checkpoint-fence post-quarantine release-window determinism hardening
108. `I-0322` (`M56-S2`) QA counterexample gate for auto-tune rollback checkpoint-fence post-quarantine release-window determinism + invariant safety
109. `I-0326` (`M57-S1`) auto-tune policy-manifest rollback checkpoint-fence post-release-window epoch-rollover determinism hardening
110. `I-0327` (`M57-S2`) QA counterexample gate for auto-tune rollback checkpoint-fence post-release-window epoch-rollover determinism + invariant safety
111. `I-0328` (`M57-F1`) close M57 QA one-chain isolation gap with deterministic post-release-window epoch-rollover isolation coverage
112. `I-0330` (`M58-S1`) auto-tune policy-manifest rollback checkpoint-fence post-epoch-rollover late-bridge reconciliation determinism hardening
113. `I-0331` (`M58-S2`) QA counterexample gate for post-epoch-rollover late-bridge reconciliation determinism + invariant safety
114. `I-0335` (`M59-S1`) auto-tune policy-manifest rollback checkpoint-fence post-late-bridge backlog-drain determinism hardening
115. `I-0336` (`M59-S2`) QA counterexample gate for post-late-bridge backlog-drain determinism + invariant safety
116. `I-0340` (`M60-S1`) auto-tune policy-manifest rollback checkpoint-fence post-backlog-drain live-catchup determinism hardening
117. `I-0341` (`M60-S2`) QA counterexample gate for post-backlog-drain live-catchup determinism + invariant safety
118. `I-0344` (`M61-S1`) auto-tune policy-manifest rollback checkpoint-fence post-live-catchup steady-state rebaseline determinism hardening
119. `I-0345` (`M61-S2`) QA counterexample gate for post-live-catchup steady-state rebaseline determinism + invariant safety

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
- BTC miner fee + deterministic vin/vout delta conservation.
5. Fail-fast safety contract:
- any correctness-impacting stage error triggers immediate process abort (`panic`).
- failed-path cursor/watermark advancement is prohibited.
- recovery path is restart + deterministic replay from last committed boundary.
6. Runtime wiring invariants stay enforced for mandatory chains (`solana-devnet`, `base-sepolia`, `btc-testnet`).

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
1. Runtime target builder includes the mandatory chains for that tranche in deterministic order.
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
1. Runtime unit/integration tests assert target-to-adapter parity for the mandatory chains for that tranche.
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
3. Recovery behavior is proven for the mandatory chains for that tranche without regressing runtime adapter wiring guarantees.
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
3. Transient fail-first/retry paths continue to recover deterministically on the mandatory chains for that tranche with stable canonical tuples and no duplicate canonical IDs.
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
4. Cursor/watermark progression remains monotonic for the processed range and runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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
4. Cursor/watermark progression remains monotonic and runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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

### M20. Dual-Chain Tick Interleaving Determinism Reliability Tranche C0014 (P0, Completed)

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
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

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

### M21. Tick Checkpoint Crash-Recovery Determinism Reliability Tranche C0015 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when process crashes occur at partial dual-chain tick progress boundaries, so restart/resume always converges to one deterministic canonical output and cursor state.

#### Entry Gate
- `M20` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M21-S1` (`I-0170`): implement deterministic crash-safe tick checkpointing and resume reconciliation so crash-point permutations cannot change canonical outputs, induce duplicate canonical IDs, or regress chain-scoped cursors.
2. `M21-S2` (`I-0171`): execute QA counterexample gate for crash-point permutation determinism and restart/resume invariant evidence.

#### Definition Of Done
1. Restart after any modeled crash cutpoint in mixed Solana/Base tick execution converges to one canonical event set for equivalent logical input.
2. Partial pre-crash progress cannot cause missing logical events, duplicate canonical IDs, or cross-chain cursor bleed/regression on resume.
3. Replay/resume under repeated crash/restart permutations remains idempotent with stable canonical tuple ordering and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject crash cutpoints around fetch/normalize/ingest/cursor-commit boundaries across opposite Solana/Base completion-order permutations and assert `0` canonical tuple diffs for equivalent ranges.
2. Deterministic tests assert resume from every cutpoint preserves chain-scoped cursor monotonicity and `0` missing logical events on the non-crashed chain path.
3. Deterministic repeated crash/restart replay loops assert `0` duplicate canonical IDs and `0` balance drift.
4. QA executes required validation commands plus crash-point permutation counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across crash-point permutation fixtures on mandatory chains.
2. `0` duplicate canonical IDs and `0` missing logical events after crash/restart resume fixtures.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: strict checkpoint ordering and resume reconciliation may increase tick latency or replay window size under repeated crashes.
- Fallback: use deterministic replay-from-last-safe-checkpoint semantics with explicit crash-cutpoint diagnostics and bounded replay window guardrails until optimized checkpoint granularity is proven safe.

### M22. Checkpoint Integrity Self-Healing Determinism Reliability Tranche C0016 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when persisted tick checkpoint state is partially written, stale, or chain-inconsistent, so restart/resume converges to one deterministic canonical output and cursor state without manual repair.

#### Entry Gate
- `M21` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M22-S1` (`I-0175`): implement deterministic checkpoint integrity validation + last-safe reconciliation so corrupted/stale/inconsistent persisted checkpoint state cannot induce duplicate canonical IDs, missing logical events, or cursor regression.
2. `M22-S2` (`I-0176`): execute QA counterexample gate for checkpoint corruption/recovery determinism and restart/resume invariant evidence.

#### Definition Of Done
1. Startup/resume validates persisted checkpoint payload integrity and chain-scoped consistency before processing new ranges.
2. Corrupted or chain-inconsistent checkpoint state deterministically recovers to one last-safe boundary (or fails fast with explicit diagnostics) without silent duplicate or missing-event side effects.
3. Replay/resume after integrity-triggered reconciliation remains idempotent with stable canonical tuple ordering and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject checkpoint corruption modes (truncated payload, stale checkpoint cursor, cross-chain checkpoint mix-up) across Solana/Base tick resume paths and assert one canonical output set for equivalent logical input.
2. Deterministic tests assert recovery from each corruption mode yields `0` duplicate canonical IDs, `0` missing logical events, and chain-scoped cursor monotonicity.
3. Deterministic repeated recover/restart loops assert `0` canonical tuple diffs and `0` balance drift.
4. QA executes required validation commands plus checkpoint-integrity counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across checkpoint-corruption permutation fixtures on mandatory chains.
2. `0` duplicate canonical IDs and `0` missing logical events after integrity-triggered recovery/resume fixtures.
3. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: strict checkpoint-integrity validation and recovery can increase restart latency and replay-window cost under repeated corruption scenarios.
- Fallback: use deterministic fail-fast plus replay-from-last-known-good checkpoint semantics with explicit checkpoint-integrity diagnostics and bounded recovery window guardrails until optimized repair granularity is proven safe.

### M23. Sidecar Decode Degradation Determinism Reliability Tranche C0017 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when sidecar decode availability or schema compatibility degrades intermittently, so mandatory-chain runs converge to deterministic canonical output and cursor behavior under replay/resume.

#### Entry Gate
- `M22` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M23-S1` (`I-0178`): implement deterministic sidecar-degradation classification and continuation semantics so transient decode outages and schema-incompatible signatures cannot induce duplicate canonical IDs, missing decodable-event coverage, or cursor regression.
2. `M23-S2` (`I-0179`): execute QA counterexample gate for sidecar-degradation determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical input with sidecar-degradation permutations (temporary unavailable, schema mismatch, parse failure) converges to one deterministic canonical tuple output for all decodable signatures.
2. Sidecar-transient unavailability paths use deterministic bounded retries and do not advance cursor/watermark on failed attempts.
3. Sidecar terminal decode failures are isolated or fail-fast by explicit deterministic rules with reproducible signature-level diagnostics and no silent event loss.
4. Replay/resume after degradation permutations remains idempotent with stable canonical ordering, no duplicate canonical IDs, and no balance double-apply side effects.
5. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject sidecar-unavailable bursts before and during decode and assert bounded retry behavior plus no cursor advancement before success.
2. Deterministic tests inject mixed schema-mismatch/parse-failure signatures with decodable suffix signatures and assert deterministic isolation/continuation outputs across independent runs.
3. Deterministic tests replay equivalent ranges under at least two fixed degradation permutations and assert `0` duplicate canonical IDs, `0` tuple diffs for decodable outputs, and `0` balance drift.
4. QA executes required validation commands plus sidecar-degradation counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across sidecar-degradation replay fixtures on mandatory chains.
2. `0` cursor monotonicity regressions in sidecar-unavailable retry/resume fixtures.
3. `0` canonical tuple diffs for decodable outputs across deterministic degradation permutations.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-aggressive continuation under sidecar degradation can hide broad decode outages and silently drop logical events.
- Fallback: preserve deterministic fail-fast guardrails for full-batch degradation, keep bounded per-signature isolation only for explicitly classified partial decode failures, and emit stage-scoped diagnostics until sidecar reliability contracts are extended.

### M24. Ambiguous Ingest-Commit Acknowledgment Determinism Reliability Tranche C0018 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when ingest commit outcome is ambiguous (for example, commit-ack timeout or transport interruption after write), so replay/resume converges to one deterministic canonical output and cursor state on mandatory chains.

#### Entry Gate
- `M23` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M24-S1` (`I-0183`): implement deterministic ambiguous-commit outcome reconciliation so commit-ack loss/timeouts cannot induce duplicate canonical IDs, missing logical events, or cursor regression.
2. `M24-S2` (`I-0184`): execute QA counterexample gate for ambiguous-commit replay determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical input under commit-ack ambiguity permutations (ack timeout, post-write disconnect, retry-after-unknown) converges to one deterministic canonical tuple output on the mandatory chains for that tranche.
2. Unknown commit outcomes are reconciled by deterministic commit-state rules before retry/resume cursor advancement, with reproducible diagnostics for every ambiguity path.
3. Replay/resume after ambiguous-commit permutations remains idempotent with `0` duplicate canonical IDs, `0` missing logical events, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject commit-ack timeout/disconnect permutations across Solana/Base ingest paths and assert one canonical output set for equivalent logical ranges.
2. Deterministic tests inject retry-after-unknown permutations and assert `0` duplicate canonical IDs plus `0` missing logical events across independent runs.
3. Deterministic replay/resume tests from ambiguous commit boundaries assert chain-scoped cursor monotonicity and `0` balance drift.
4. QA executes required validation commands plus ambiguous-commit counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across ambiguous-commit replay fixtures on mandatory chains.
2. `0` missing logical events across commit-ack timeout/disconnect permutation fixtures.
3. `0` cursor monotonicity regressions in retry-after-unknown replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: incorrect ambiguous-commit reconciliation can misclassify committed vs uncommitted writes, causing either duplicate replay or silent event loss.
- Fallback: preserve deterministic fail-fast behavior for unresolved commit ambiguity, emit explicit commit-ambiguity diagnostics, and replay from last-safe cursor until reconciliation rules are fully proven.

### M25. Batch-Partition Variance Determinism Reliability Tranche C0019 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when equivalent logical ranges are processed under different deterministic batch partitions (for example, page-size drift, retry-induced split/merge boundaries, or resume seam overlap), so mandatory-chain replay/resume converges to one canonical output and cursor progression.

#### Entry Gate
- `M24` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M25-S1` (`I-0188`): implement deterministic partition-invariant boundary handling so chunk-size/partition permutations cannot induce duplicate canonical IDs, missing logical events, or cursor regression.
2. `M25-S2` (`I-0189`): execute QA counterexample gate for batch-partition replay determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical ranges processed under at least two deterministic partition variants converge to one canonical tuple output on the mandatory chains for that tranche.
2. Partition boundary carryover (overlap seam, retry split/merge seam, resume seam) follows deterministic identity and dedupe rules with reproducible diagnostics.
3. Replay/resume after partition-variant permutations remains idempotent with `0` duplicate canonical IDs, `0` missing logical events, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject equivalent Solana/Base logical ranges under multiple partition sizes/orders and assert one canonical output set for equivalent ranges.
2. Deterministic tests inject retry-induced split/merge boundaries and assert `0` duplicate canonical IDs plus `0` missing logical events across independent runs.
3. Deterministic replay/resume tests from partition seam boundaries assert chain-scoped cursor monotonicity and `0` balance drift.
4. QA executes required validation commands plus batch-partition counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across partition-variance replay fixtures on mandatory chains.
2. `0` missing logical events across partition split/merge and resume seam permutation fixtures.
3. `0` cursor monotonicity regressions across partition-variant replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: incorrect partition-boundary identity reconciliation can either suppress valid events (over-dedupe) or duplicate seam events (under-dedupe).
- Fallback: preserve deterministic fail-fast on unresolved seam-identity ambiguity, emit explicit boundary diagnostics, and replay from last-safe cursor until partition-boundary contracts are fully proven.

### M26. Moving-Head Fetch Cutoff Determinism Reliability Tranche C0020 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when chain heads advance during fetch pagination, so each mandatory-chain tick processes a deterministic closed range and replay/resume converges to one canonical output and cursor progression.

#### Entry Gate
- `M25` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M26-S1` (`I-0191`): implement deterministic per-chain fetch-cutoff snapshot and carryover semantics so head-advance permutations cannot induce duplicate canonical IDs, missing logical events, or cursor regression.
2. `M26-S2` (`I-0192`): execute QA counterexample gate for moving-head fetch determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical ranges processed under deterministic moving-head permutations (head advances while paging, late append during tick, resume from partial page) converge to one canonical tuple output on the mandatory chains for that tranche.
2. Per-chain cursor/watermark advancement is bounded to a deterministic pinned fetch cutoff, and late-arriving head data beyond cutoff is deferred to the next tick without loss or duplication.
3. Replay/resume from moving-head boundaries remains idempotent with `0` duplicate canonical IDs, `0` missing logical events, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject head-advance-during-pagination permutations for equivalent Solana/Base logical ranges and assert one canonical output set against fixed-head baseline expectations.
2. Deterministic tests inject late-append and page-boundary overlap permutations and assert `0` duplicate canonical IDs plus `0` missing logical events across independent runs.
3. Deterministic replay/resume tests from pinned-cutoff boundaries assert chain-scoped cursor monotonicity and `0` balance drift.
4. QA executes required validation commands plus moving-head counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across moving-head replay fixtures on mandatory chains.
2. `0` missing logical events when comparing moving-head permutation fixtures against deterministic fixed-head baselines.
3. `0` cursor monotonicity regressions across pinned-cutoff replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: strict fetch-cutoff pinning can increase backlog/lag under rapid head growth or provider latency skew.
- Fallback: keep deterministic cutoff pinning with bounded lag-window guardrails, emit explicit cutoff-lag diagnostics, and fail fast on unresolved head-boundary ambiguity until fetch-cutoff contracts are extended.

### M27. Volatility-Burst Normalizer Canonical Fold Determinism Reliability Tranche C0021 (P0, Completed)

#### Objective
Eliminate duplicate/missing signed-delta risk when high-volatility transactions emit dense multi-actor/multi-asset balance deltas, so equivalent logical inputs converge to one canonical event set with deterministic fee coverage and replay-safe cursor progression.

#### Entry Gate
- `M26` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M27-S1` (`I-0194`): implement deterministic volatility-burst canonical fold and event-path disambiguation semantics so decode/order variance cannot induce duplicate canonical IDs, missing logical events, or signed-delta drift.
2. `M27-S2` (`I-0195`): execute QA counterexample gate for volatility-burst normalizer determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent high-volatility logical inputs (inner-op reorder, repeated transfer legs, mixed fee+transfer deltas) converge to one canonical tuple output on the mandatory chains for that tranche.
2. Per-transaction actor/asset signed-delta conservation is preserved with deterministic explicit fee-event coexistence (Solana transaction fee and Base L2/L1 fee components where source fields exist), without duplicate canonical IDs.
3. Replay/resume from volatility-burst boundaries remains idempotent with `0` duplicate canonical IDs, `0` missing logical events, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject high-volatility Solana/Base fixtures with operation-order and decode-order permutations and assert one canonical output set for equivalent logical ranges.
2. Deterministic tests inject mixed fee+transfer same-transaction permutations and assert actor/asset signed-delta conservation plus explicit fee-event coverage expectations.
3. Deterministic replay/resume tests from volatility-burst boundaries assert chain-scoped cursor monotonicity and `0` balance drift.
4. QA executes required validation commands plus volatility-burst counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across volatility-burst permutation fixtures on mandatory chains.
2. `0` signed-delta conservation violations across actor/asset transaction aggregates in volatility-burst fixtures.
3. `0` cursor monotonicity regressions across volatility-burst replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-aggressive fold/collapse logic can accidentally merge distinct logical events, while under-constrained fold boundaries can re-emit duplicate canonical events.
- Fallback: keep deterministic conservative folding boundaries with explicit collision diagnostics, fail fast on unresolved fold ambiguity, and replay from last-safe cursor until fold contracts are extended.

### M28. Deferred Sidecar-Recovery Backfill Determinism Reliability Tranche C0022 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when previously undecodable signatures later become decodable, so replay/backfill converges to one deterministic canonical output set with signed-delta and fee-event invariants preserved.

#### Entry Gate
- `M27` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M28-S1` (`I-0199`): implement deterministic deferred-signature recovery/backfill identity and emission semantics so sidecar-unavailable/schema-mismatch recovery cannot induce duplicate canonical IDs, missing logical events, or signed-delta drift.
2. `M28-S2` (`I-0200`): execute QA counterexample gate for deferred sidecar-recovery backfill determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical ranges under degradation->recovery permutations (temporary sidecar unavailable, terminal decode mismatch later recovered, mixed recovered+already-decoded signatures) converge to one canonical tuple output set on the mandatory chains for that tranche.
2. Recovered signatures follow deterministic canonical identity reconciliation against previously processed ranges, with `0` duplicate canonical IDs and deterministic explicit fee-event coexistence where source fields exist.
3. Replay/resume from recovery boundaries remains idempotent with `0` missing logical events, chain-scoped cursor monotonicity, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject sidecar-unavailable/schema-mismatch then recovery permutations for equivalent Solana/Base logical ranges and assert one canonical output set against fully-decodable baseline expectations.
2. Deterministic tests inject mixed recovered+already-decoded signature sets and assert `0` duplicate canonical IDs plus signed-delta conservation with explicit fee-event coexistence expectations.
3. Deterministic replay/resume tests from recovery seam boundaries assert chain-scoped cursor monotonicity and `0` balance drift.
4. QA executes required validation commands plus deferred-recovery counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across deferred-recovery permutation fixtures on mandatory chains.
2. `0` missing logical events when comparing deferred-recovery fixtures against fully-decodable baseline fixtures.
3. `0` cursor monotonicity regressions across deferred-recovery replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-broad recovery matching can remap recovered signatures to incorrect canonical identities or re-emit previously ingested logical events.
- Fallback: keep deterministic conservative recovery reconciliation keyed by chain/signature/canonical-path boundaries, emit explicit recovery-collision diagnostics, and fail fast on unresolved recovery ambiguity until backfill contracts are extended.

### M29. Live/Backfill Overlap Canonical Convergence Determinism Reliability Tranche C0023 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when equivalent logical events are observed through both live ingestion ticks and deferred backfill/recovery paths, so source-order permutations converge to one deterministic canonical output set with replay-safe cursor progression.

#### Entry Gate
- `M28` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M29-S1` (`I-0204`): implement deterministic live/backfill overlap reconciliation semantics so live-first, backfill-first, and interleaved source-order permutations cannot induce duplicate canonical IDs, missing logical events, or signed-delta/fee-event drift.
2. `M29-S2` (`I-0205`): execute QA counterexample gate for live/backfill overlap determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical ranges processed under live-first, backfill-first, and interleaved overlap permutations converge to one canonical tuple output set on the mandatory chains for that tranche.
2. Overlap reconciliation applies deterministic source-merge precedence keyed by canonical identity boundaries, with `0` duplicate canonical IDs and deterministic explicit fee-event coexistence where source fields exist.
3. Replay/resume from live/backfill overlap boundaries remains idempotent with `0` missing logical events, chain-scoped cursor monotonicity, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject equivalent Solana/Base logical ranges through live-first, backfill-first, and interleaved overlap permutations and assert one canonical output set against single-source baseline expectations.
2. Deterministic tests inject mixed recovered+live duplicate signature permutations and assert `0` duplicate canonical IDs plus signed-delta conservation with explicit fee-event coexistence expectations.
3. Deterministic replay/resume tests from live/backfill overlap boundaries assert chain-scoped cursor monotonicity and `0` balance drift.
4. QA executes required validation commands plus live/backfill-overlap counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across live/backfill overlap permutation fixtures on mandatory chains.
2. `0` missing logical events when comparing overlap-permutation fixtures against deterministic single-source baseline fixtures.
3. `0` cursor monotonicity regressions across live/backfill overlap replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: incorrect live/backfill overlap precedence can suppress valid backfill-only corrections or re-emit already-committed logical events.
- Fallback: keep deterministic conservative overlap reconciliation with explicit source-conflict diagnostics, fail fast on unresolved overlap ambiguity, and replay from last-safe cursor until overlap contracts are extended.

### M30. Decoder-Version Transition Canonical Convergence Determinism Reliability Tranche C0024 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when decoder output shape or metadata fidelity changes across decoder-version upgrades, so equivalent logical events converge to one deterministic canonical output set during mixed-version live/replay/backfill operation.

#### Entry Gate
- `M29` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M30-S1` (`I-0209`): implement deterministic decoder-version transition reconciliation semantics so legacy-vs-upgraded decode permutations cannot induce duplicate canonical IDs, missing logical events, or signed-delta/fee-event drift.
2. `M30-S2` (`I-0210`): execute QA counterexample gate for decoder-version transition determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical ranges processed with legacy decoder outputs, upgraded decoder outputs, and mixed-version interleaving converge to one canonical tuple output set on the mandatory chains for that tranche.
2. Decoder-version transition reconciliation preserves deterministic canonical identity boundaries with `0` duplicate canonical IDs while tolerating version-scoped metadata enrichment that does not change logical economic meaning.
3. Replay/resume from decoder-version transition boundaries remains idempotent with `0` missing logical events, chain-scoped cursor monotonicity, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject equivalent Solana/Base logical ranges encoded via legacy, upgraded, and mixed decoder-version outputs and assert one canonical output set against stable baseline expectations.
2. Deterministic tests inject decoder-version metadata enrichment variance and assert `0` duplicate canonical IDs plus signed-delta conservation with explicit fee-event coexistence expectations.
3. Deterministic replay/resume tests from decoder-version transition boundaries assert chain-scoped cursor monotonicity and `0` balance drift.
4. QA executes required validation commands plus decoder-version-transition counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across decoder-version transition permutation fixtures on mandatory chains.
2. `0` missing logical events when comparing mixed-version fixtures against deterministic single-version baseline fixtures.
3. `0` cursor monotonicity regressions across decoder-version transition replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-aggressive cross-version equivalence matching can collapse truly distinct logical events, while under-constrained matching can re-emit duplicate canonical events across upgrade boundaries.
- Fallback: keep deterministic conservative version-bridge reconciliation with explicit version-conflict diagnostics, fail fast on unresolved equivalence ambiguity, and replay from last-safe cursor until decoder transition contracts are extended.

### M31. Incremental Decode-Coverage Canonical Convergence Determinism Reliability Tranche C0025 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when decoder coverage evolves from partial extraction to enriched extraction for the same transaction/signature, so all economically meaningful asset-volatility events are indexed exactly once under mixed live/replay/backfill operation.

#### Entry Gate
- `M30` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M31-S1` (`I-0214`): implement deterministic incremental decode-coverage reconciliation semantics so sparse-vs-enriched decode permutations cannot induce duplicate canonical IDs, missing logical events, or signed-delta/fee-event drift.
2. `M31-S2` (`I-0215`): execute QA counterexample gate for incremental decode-coverage determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical ranges processed with sparse decoder outputs, enriched decoder outputs, and mixed sparse/enriched interleaving converge to one canonical tuple output set on the mandatory chains for that tranche.
2. Incremental coverage reconciliation emits newly discoverable logical events exactly once while preserving canonical identity stability for already-materialized logical events.
3. Replay/resume from incremental-coverage boundaries remains idempotent with `0` missing logical events, chain-scoped cursor monotonicity, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject equivalent Solana/Base logical ranges under sparse, enriched, and mixed decode-coverage permutations and assert one canonical output set against enriched-baseline expectations.
2. Deterministic tests inject incremental-coverage replay permutations and assert `0` duplicate canonical IDs for already-emitted logical events plus deterministic one-time emission for newly discovered logical events.
3. Deterministic replay/resume tests from incremental-coverage boundaries assert chain-scoped cursor monotonicity, signed-delta conservation, and explicit fee-event coexistence with `0` balance drift.
4. QA executes required validation commands plus incremental-coverage counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across incremental decode-coverage permutation fixtures on mandatory chains.
2. `0` missing logical events when comparing sparse/enriched mixed fixtures against deterministic enriched baseline fixtures.
3. `0` cursor monotonicity regressions across incremental decode-coverage replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-aggressive sparse-vs-enriched equivalence matching can suppress legitimate newly discovered logical events, while under-constrained matching can re-emit already-materialized events as duplicates.
- Fallback: keep deterministic conservative coverage-lineage reconciliation with explicit coverage-conflict diagnostics, fail fast on unresolved sparse/enriched ambiguity, and replay from last-safe cursor until incremental-coverage contracts are extended.

### M32. Decode-Coverage Regression Flap Canonical Stability Determinism Reliability Tranche C0026 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when decode coverage regresses from enriched back to sparse (and later re-enriches) for the same transaction/signature range, so coverage flapping cannot erase previously learned logical events or reintroduce duplicate canonical emissions.

#### Entry Gate
- `M31` exit gate green.
- Mandatory chain runtime targets remain fixed to `solana-devnet` and `base-sepolia`.

#### Slices
1. `M32-S1` (`I-0219`): implement deterministic coverage-regression reconciliation semantics so enriched->sparse->enriched flap permutations cannot induce duplicate canonical IDs, missing logical events, or signed-delta/fee-event drift.
2. `M32-S2` (`I-0220`): execute QA counterexample gate for decode-coverage regression flap determinism and invariant evidence across mandatory chains.

#### Definition Of Done
1. Equivalent logical ranges processed with enriched-first, sparse-regression, and re-enriched permutations converge to one canonical tuple output set on the mandatory chains for that tranche.
2. Coverage regression handling preserves already-materialized logical events during sparse fallback and avoids duplicate re-emission when enrichment returns.
3. Replay/resume from coverage-flap boundaries remains idempotent with `0` missing logical events, chain-scoped cursor monotonicity, and no balance double-apply side effects.
4. Runtime adapter wiring invariants remain green for the mandatory chains for that tranche.

#### Test Contract
1. Deterministic tests inject equivalent Solana/Base logical ranges under enriched-first, sparse-regression, and re-enriched permutations and assert one canonical output set against enriched-baseline expectations.
2. Deterministic tests inject multi-cycle enrichment flap permutations and assert `0` duplicate canonical IDs plus stable persistence of previously discovered logical events.
3. Deterministic replay/resume tests from coverage-flap boundaries assert chain-scoped cursor monotonicity, signed-delta conservation, and explicit fee-event coexistence with `0` balance drift.
4. QA executes required validation commands plus coverage-regression-flap counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across decode-coverage regression flap permutation fixtures on mandatory chains.
2. `0` missing logical events when comparing flap permutations against deterministic enriched baseline fixtures.
3. `0` cursor monotonicity regressions across decode-coverage flap replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: naive sparse-regression handling can either drop previously discovered enriched logical events or repeatedly re-emit them when enrichment returns.
- Fallback: keep deterministic conservative coverage-floor reconciliation with explicit regression-conflict diagnostics, fail fast on unresolved coverage flap ambiguity, and replay from last-safe cursor until flap contracts are extended.

### M33. Fee-Component Availability Flap Canonical Convergence Determinism Reliability Tranche C0027 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when fee-component availability for equivalent logical transactions flaps across runtime passes (for example Base execution fee always present while L1 data fee temporarily unavailable and later recovered), so fee-event coverage converges to one deterministic canonical output set without replay drift.

#### Entry Gate
- `M32` exit gate green.
- Active M33 scope targets remain `solana-devnet` and `base-sepolia` (BTC activation is gated by `M35`).

#### Slices
1. `M33-S1` (`I-0224`): implement deterministic fee-component availability reconciliation semantics so complete-fee, partial-fee, and recovered-fee permutations cannot induce duplicate canonical IDs, missing logical events, or fee split drift.
2. `M33-S2` (`I-0225`): execute QA counterexample gate for fee-component availability flap determinism and invariant evidence across M33 scope targets.

#### Definition Of Done
1. Equivalent logical ranges processed under complete-fee, partial-fee (`fee_data_l1` unavailable), and recovered-fee permutations converge to one canonical tuple output set on M33 scope targets (`solana-devnet`, `base-sepolia`).
2. Base fee split handling preserves deterministic coexistence of execution/data components and deterministic unavailable-marker semantics without duplicate fee-event re-emission.
3. Solana fee-event coverage remains explicit and deterministic under mixed replay/resume permutations that also include Base fee-availability flaps.
4. Replay/resume from fee-availability flap boundaries remains idempotent with `0` missing logical events, chain-scoped cursor monotonicity, and no balance double-apply side effects.
5. Runtime adapter wiring invariants remain green for M33 scope targets.

#### Test Contract
1. Deterministic tests inject equivalent Base logical ranges under full-fee, data-fee-missing, and data-fee-recovered permutations and assert one canonical output set against deterministic full-fee baseline expectations.
2. Deterministic tests inject repeated fee-field flap permutations and assert `0` duplicate canonical IDs plus deterministic Base fee split coexistence (`fee_execution_l2`, `fee_data_l1` when source fields exist, deterministic unavailable marker otherwise).
3. Deterministic replay/resume tests from fee-availability flap boundaries assert chain-scoped cursor monotonicity, signed-delta conservation, Solana fee-event continuity, and `0` balance drift.
4. QA executes required validation commands plus fee-availability-flap counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across fee-component availability flap permutation fixtures on M33 scope targets.
2. `0` missing required fee logical events when comparing flap permutations against deterministic baseline expectations per fee-field availability.
3. `0` cursor monotonicity regressions across fee-availability flap replay/resume fixtures.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: inconsistent provider fee-field availability can cause unstable fee-component identity that either suppresses legitimate recovered data-fee events or re-emits already-materialized fee events as duplicates.
- Fallback: preserve deterministic conservative fee-floor reconciliation (`fee_execution_l2` always deterministic, `fee_data_l1` only when source fields are provably available, explicit unavailable-marker diagnostics otherwise) and replay from last-safe cursor until fee-availability contracts are extended.

### M34. Fail-Fast Panic Contract Hardening Tranche C0028 (P0, Completed)

#### Objective
Enforce strict fail-fast safety so correctness-impacting stage failures cannot continue in-process and cannot advance cursor/watermark before process abort.

#### Entry Gate
- `M33` exit gate green.
- Runtime selection supports chain-scoped deployment modes (`like-group` and `independent`) without cross-chain commit dependency.

#### Slices
1. `M34-S1` (`I-0226`): harden stage-level failure handling so coordinator/fetcher/normalizer/ingester correctness-impacting errors panic immediately, with explicit chain-scoped diagnostics.
2. `M34-S2` (`I-0227`): execute QA failure-injection counterexample gate proving panic-on-error and zero unsafe cursor/watermark progression.

#### Definition Of Done
1. Stage failure policy is explicit and deterministic: no skip/continue path for correctness-impacting failures.
2. Failed batch path cannot commit cursor/watermark updates before abort.
3. Restart replay from last committed boundary converges deterministically with `0` duplicate/missing canonical events.
4. Chain-scoped commit scheduling contract is preserved; no shared cross-chain interleaver is present in production runtime wiring.

#### Test Contract
1. Failure-injection tests for each stage boundary assert immediate panic behavior.
2. Cursor/watermark safety tests assert failed path never advances persisted progress.
3. Restart replay tests assert canonical tuple equivalence and idempotent ingestion after injected failure.
4. QA records evidence under `.ralph/reports/` with per-stage fail-fast verdicts.

#### Exit Gate (Measurable)
1. `0` failure-injection cases that continue processing after correctness-impacting stage error.
2. `0` failed-path cursor/watermark advancement violations.
3. `0` replay divergence after fail-fast restart scenarios.
4. Validation commands pass.

#### Risk Gate + Fallback
- Gate: over-broad panic classification can reduce availability if non-critical errors are incorrectly escalated.
- Fallback: keep deterministic error classification matrix under planner control, defaulting unknown correctness-impacting errors to panic.

### M35. BTC-Like Runtime Activation Tranche C0029 (P0, Completed)

#### Objective
Activate `btc-like` runtime support (`btc-testnet`) with deterministic UTXO canonicalization, explicit fee semantics, and topology-independent correctness.

#### Entry Gate
- `M34` exit gate green.
- BTC-like adapter/normalizer contracts are planner-approved in `specs/*`.

#### Slices
1. `M35-S1` (`I-0228`): implement BTC-like runtime target wiring, fetch/decode/normalize/ingest path, and deterministic vin/vout ownership + miner-fee semantics.
2. `M35-S2` (`I-0229`): execute QA golden/invariant/topology-parity gate for BTC-like activation across chain-per-deployment and family-per-deployment modes.

#### Definition Of Done
1. `btc-testnet` is a first-class runtime target with chain-scoped cursor/watermark progression and replay idempotency.
2. BTC canonical event identity (`txid + vin/vout path`) is deterministic under replay/restart permutations.
3. Miner-fee and input/output delta conservation semantics are represented as explicit canonical signed deltas.
4. Topology migration parity holds: BTC canonical outputs remain equivalent between independent deployment and grouped deployment modes.
5. Mandatory chain set (`solana-devnet`, `base-sepolia`, `btc-testnet`) is runtime-wireable under documented deployment configuration.

#### Test Contract
1. BTC fixture suite covers coinbase, multi-input/output, change output, and fee attribution edge cases.
2. Cross-run determinism tests assert ordered tuple equality `(event_id, delta, category)` for BTC ranges.
3. Topology parity tests assert canonical equivalence for independent vs grouped deployment modes.
4. QA executes required validation commands plus BTC golden/invariant runners and stores report artifacts under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across BTC replay fixtures.
2. `0` signed-delta conservation violations in BTC invariants.
3. `0` topology parity diffs for BTC canonical tuple outputs.
4. `0` runtime wiring parity regressions across all mandatory chains.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: RPC/script-decoder heterogeneity can destabilize deterministic vin/vout ownership classification.
- Fallback: enforce deterministic conservative ownership rules with explicit unresolved-script diagnostics and fail fast on ambiguity that threatens canonical correctness.

### M36. BTC Reorg/Finality Flap Canonical Convergence Tranche C0030 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when BTC branch history reorgs across runtime passes (including one-block and deeper rollback windows), so post-reorg replay converges to one deterministic canonical output set with preserved signed-delta conservation and cursor safety.

#### Entry Gate
- `M35` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M36-S1` (`I-0232`): harden BTC rollback/replay convergence semantics so competing-branch permutations cannot induce duplicate canonical IDs, missing logical events, signed-delta drift, or cursor regression.
2. `M36-S2` (`I-0233`): execute QA counterexample gate for BTC reorg/finality flap determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent BTC logical ranges processed under canonical-branch, one-block-reorg, and multi-block-reorg permutations converge to one canonical tuple output set.
2. Rollback of orphaned BTC branch outputs deterministically removes stale canonical outputs and replays replacement-branch outputs exactly once.
3. BTC miner-fee plus vin/vout signed-delta conservation remains valid after rollback/replay permutations.
4. Replay/resume from reorg boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject BTC competing-branch permutations (single-depth and multi-depth) and assert canonical tuple convergence to post-reorg deterministic baseline outputs.
2. Deterministic tests assert orphaned-branch canonical IDs do not survive after rollback and replacement-branch events emit once.
3. Deterministic replay/resume tests from BTC reorg boundaries assert signed-delta conservation, `0` balance drift, and cursor/watermark safety.
4. QA executes required validation commands plus BTC reorg/finality-flap counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` duplicate canonical IDs across BTC reorg/finality flap permutation fixtures.
2. `0` missing logical events when comparing permutations against deterministic post-reorg baseline fixtures.
3. `0` signed-delta conservation violations after rollback/replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in BTC reorg recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `reorg_recovery_deterministic`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: ambiguous BTC fork ancestry near moving head can destabilize rollback-window selection and replacement-branch ordering.
- Fallback: enforce deterministic conservative rollback-window policy with explicit fork-ambiguity diagnostics, fail fast on unresolved ancestry ambiguity, and replay from last committed safe boundary.

### M37. Tri-Chain Volatility-Burst Interleaving Determinism Tranche C0031 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when `solana-devnet`, `base-sepolia`, and `btc-testnet` emit concurrent volatility bursts with asymmetric latency/retry pressure, so equivalent logical ranges converge to one deterministic canonical output set per chain without cursor bleed or fee/signed-delta regressions.

#### Entry Gate
- `M36` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M37-S1` (`I-0237`): harden tri-chain scheduler/interleaving convergence semantics so completion-order and backlog-pressure permutations cannot induce duplicate canonical IDs, missing logical events, signed-delta drift, fee-coverage regressions, or cursor regression.
2. `M37-S2` (`I-0238`): execute QA counterexample gate for tri-chain volatility/interleaving determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under deterministic completion-order permutations converge to one canonical tuple output set per chain.
2. Chain-local backlog or retry pressure on any one chain cannot induce duplicate/missing logical events on the other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under mixed interleaving + replay/resume permutations.
4. Replay/resume from mixed tri-chain boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject at least two tri-chain completion-order permutations for equivalent logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain backlog/retry pressure while the other two chains progress and assert `0` duplicate canonical IDs and `0` missing logical events across chains.
3. Deterministic replay/resume tests from mixed tri-chain boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus tri-chain interleaving counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic tri-chain completion-order permutation fixtures.
2. `0` duplicate canonical IDs and `0` missing logical events under one-chain backlog/retry counterexample fixtures.
3. `0` signed-delta conservation violations and `0` fee-event coverage regressions under tri-chain replay/resume permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in tri-chain replay/recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: tri-chain completion-order variance can reintroduce non-deterministic commit timing and hidden cross-chain starvation under volatility spikes.
- Fallback: enforce deterministic chain-scoped commit fences with bounded backlog budgets, emit explicit tri-chain scheduler diagnostics, and fail fast on unresolved interleaving ambiguity.

### M38. Tri-Chain Late-Arrival Backfill Canonical Closure Tranche C0032 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event risk when late-arriving transactions are observed after initial tri-chain range processing (RPC lag, sidecar recovery, retry-after-timeout), so replay/backfill closure converges to one deterministic canonical output set per chain with fee/signed-delta invariants and cursor safety preserved.

#### Entry Gate
- `M37` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M38-S1` (`I-0242`): harden tri-chain late-arrival closure semantics so delayed transaction discovery and mixed replay/backfill permutations cannot induce duplicate canonical IDs, missing logical events, fee/signed-delta drift, or cursor regression.
2. `M38-S2` (`I-0243`): execute QA counterexample gate for tri-chain late-arrival closure determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under early-arrival and delayed-arrival permutations converge to one canonical tuple output set per chain.
2. Late-arrival recovery on any one chain cannot induce duplicate/missing logical events or cursor bleed on the other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under mixed late-arrival + replay/backfill closure permutations.
4. Replay/resume from late-arrival closure boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject at least two tri-chain late-arrival permutations (on-time discovery, delayed discovery after initial pass) for equivalent logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain delayed backfill/retry while the other two chains continue and assert `0` duplicate canonical IDs and `0` missing logical events across chains.
3. Deterministic replay/backfill-closure tests from mixed tri-chain boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus tri-chain late-arrival closure counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic tri-chain delayed-arrival permutation fixtures.
2. `0` duplicate canonical IDs and `0` missing logical events under one-chain delayed backfill/retry counterexample fixtures.
3. `0` signed-delta conservation violations and `0` fee-event coverage regressions under tri-chain replay/backfill-closure permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in tri-chain late-arrival recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: delayed-arrival closure windows can become ambiguous near moving heads and reintroduce non-deterministic include/exclude behavior across replay/backfill passes.
- Fallback: enforce deterministic closed-range reconciliation fences with explicit late-arrival diagnostics, fail fast on unresolved closure-window ambiguity, and replay from last committed safe boundary.

### M39. Tri-Chain Volatility-Event Completeness Reconciliation Tranche C0033 (P0, Completed)

#### Objective
Eliminate duplicate/missing volatility-event risk when tri-chain decode coverage shifts from partial to enriched across delayed sidecar/RPC discovery and replay/backfill boundaries, so equivalent logical ranges converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M38` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M39-S1` (`I-0247`): harden tri-chain volatility-event completeness reconciliation semantics so partial-decode->enriched-decode and delayed-enrichment permutations cannot induce duplicate canonical IDs, missing logical volatility events, fee/signed-delta drift, or cursor regression.
2. `M39-S2` (`I-0248`): execute QA counterexample gate for tri-chain volatility-event completeness determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under partial-first and enriched-first decode permutations converge to one canonical tuple output set per chain.
2. Delayed enrichment on one chain cannot induce duplicate/missing logical volatility events or cursor bleed on the other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under mixed partial/enriched replay/backfill permutations.
4. Replay/resume from enrichment boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject at least two tri-chain decode-coverage permutations (partial-first then enriched, enriched-first baseline) for equivalent logical ranges and assert canonical tuple convergence to one deterministic output set.
2. Deterministic tests inject one-chain delayed enrichment while the other two chains progress and assert `0` duplicate canonical IDs and `0` missing logical volatility events across chains.
3. Deterministic replay/backfill tests from mixed enrichment boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus tri-chain volatility-completeness counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic tri-chain partial/enriched permutation fixtures.
2. `0` duplicate canonical IDs and `0` missing logical volatility events under one-chain delayed-enrichment counterexample fixtures.
3. `0` signed-delta conservation violations and `0` fee-event coverage regressions under tri-chain replay/backfill enrichment permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in tri-chain enrichment recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: partial/enriched equivalence matching can collapse distinct volatility-event legs or re-emit enriched events as duplicates near late-discovery boundaries.
- Fallback: enforce deterministic enrichment-lineage reconciliation keys with explicit lineage-collision diagnostics, fail fast on unresolved ambiguity, and replay from last committed safe boundary.

### M40. Chain-Scoped Auto-Tune Backpressure Control Tranche C0034 (P0, Completed)

#### Objective
Introduce chain-scoped coordinator auto-tune control so throughput knobs adapt to per-chain lag/latency pressure without introducing cross-chain coupling, replay divergence, or fail-fast safety regressions.

#### Entry Gate
- `M39` exit gate green.
- Deployment mode support remains topology-independent (`like-group`, `independent`, hybrid).
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.

#### Slices
1. `M40-S1` (`I-0249`): implement deterministic chain-scoped auto-tune control inputs/decisions for coordinator throughput knobs (tick/batch/fetch envelope), with explicit safe bounds and per-chain diagnostics.
2. `M40-S2` (`I-0250`): execute QA counterexample gate proving chain-scoped isolation, topology parity, and fail-fast safety under lag/latency pressure permutations.

#### Definition Of Done
1. Auto-tune decisions are computed only from chain-local signals (lag, queue depth, error budget, commit latency) and never read cross-chain pressure as direct control input.
2. For identical chain input and fixed control config, repeated runs converge to deterministic canonical tuple outputs and cursor/watermark end-state.
3. Auto-tune remains optional and reversible; disabling auto-tune does not change canonical output semantics.
4. Correctness-impacting failures still panic immediately, with no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains and deployment modes.

#### Test Contract
1. Deterministic tests inject per-chain lag increase/decrease permutations and assert stable canonical output equivalence with and without auto-tune.
2. Deterministic tests inject asymmetric pressure (one chain heavily lagged, others healthy) and assert `0` cross-chain throttle bleed in control decisions.
3. Failure-injection tests under auto-tune-on mode assert panic-on-error and `0` failed-path cursor/watermark advancement.
4. Topology parity tests assert canonical equivalence between grouped and independent deployment modes with auto-tune enabled.
5. QA executes required validation commands plus auto-tune counterexample checks and records invariant evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs between auto-tune-on and auto-tune-off baseline runs for equivalent fixture ranges.
2. `0` cross-chain control-coupling violations in asymmetric lag counterexamples.
3. `0` fail-fast regressions under auto-tune-enabled failure injection.
4. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `chain_adapter_runtime_wired`.
5. Validation commands pass.

#### Risk Gate + Fallback
- Gate: aggressive auto-tune loops can oscillate throughput knobs and amplify tail latency or retry storms under noisy lag signals.
- Fallback: clamp control changes with bounded step-size/hysteresis, default to deterministic conservative profile on signal ambiguity, and preserve fail-fast abort semantics.

### M41. Auto-Tune Restart/Profile-Transition Determinism Tranche C0035 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when auto-tune-enabled runtimes restart or switch control profiles under live tri-chain pressure, so cold-start, warm-start, and profile-transition permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M40` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M41-S1` (`I-0254`): harden deterministic auto-tune restart/profile-transition semantics so cold-start, warm-start, and live-profile-switch permutations cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M41-S2` (`I-0255`): execute QA counterexample gate for auto-tune restart/profile-transition determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under cold-start, warm-start, and profile-transition permutations converge to one canonical tuple output set per chain.
2. Restarting one chain under lag pressure cannot induce cross-chain throughput-control bleed or cursor bleed on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under restart/profile-transition replay permutations.
4. Replay/resume from restart/profile-transition boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject cold-start, warm-start, and profile-transition permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain restart under asymmetric lag while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from restart/profile-transition boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune restart/profile-transition counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic cold-start, warm-start, and profile-transition permutation fixtures.
2. `0` cross-chain control-coupling violations under one-chain restart lag-pressure counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under restart/profile-transition replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in restart/profile-transition recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: restart/profile-transition handling can reset or over-apply control state, causing non-deterministic throughput envelopes and replay drift near boundary ticks.
- Fallback: enforce deterministic reset-to-baseline plus bounded warm-start adoption guarded by chain-local proofs, emit explicit restart-profile diagnostics, and fail fast on unresolved boundary ambiguity.

### M42. Auto-Tune Signal-Flap Hysteresis Determinism Tranche C0036 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when auto-tune operates under noisy lag/latency signal flaps, so hysteresis-boundary crossings, cooldown windows, and recovery-to-steady-state permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M41` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M42-S1` (`I-0259`): harden deterministic auto-tune signal-flap hysteresis/cooldown semantics so noisy lag jitter and threshold-adjacent oscillation cannot induce duplicate canonical IDs, missing logical events, cross-chain throttle bleed, or cursor regression.
2. `M42-S2` (`I-0260`): execute QA counterexample gate for auto-tune signal-flap hysteresis determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under steady-state, jitter-heavy, and recovery-to-steady-state control-signal permutations converge to one canonical tuple output set per chain.
2. Threshold-adjacent oscillation on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under signal-flap replay/resume permutations.
4. Replay/resume from hysteresis and cooldown boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject steady-state, threshold-jitter, and recovery permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain oscillation-heavy lag jitter while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from hysteresis/cooldown boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune signal-flap hysteresis counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic steady-state, jitter-heavy, and recovery permutation fixtures.
2. `0` cross-chain control-coupling violations under one-chain oscillation-lag counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under hysteresis/cooldown replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in hysteresis/cooldown recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: noisy control signals can trigger hysteresis boundary chatter and nondeterministic knob toggling that amplifies replay boundary sensitivity.
- Fallback: enforce deterministic debounced hysteresis with bounded cooldown floors, emit explicit control-flap diagnostics, and fail fast on unresolved oscillation-boundary ambiguity.

### M43. Auto-Tune Saturation/De-Saturation Envelope Determinism Tranche C0037 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when auto-tune enters sustained saturation and recovers during backlog drain, so saturation-entry, clamp-held, and de-saturation recovery permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M42` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M43-S1` (`I-0264`): harden deterministic auto-tune saturation/de-saturation envelope semantics so sustained lag pressure and backlog-drain transitions cannot induce duplicate canonical IDs, missing logical events, cross-chain throttle bleed, or cursor regression.
2. `M43-S2` (`I-0265`): execute QA counterexample gate for auto-tune saturation/de-saturation determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under saturation-entry, sustained-saturation, and de-saturation recovery permutations converge to one canonical tuple output set per chain.
2. Sustained saturation on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under saturation/de-saturation replay/resume permutations.
4. Replay/resume from saturation clamp and de-saturation boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject saturation-entry, sustained-saturation, and de-saturation recovery permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain sustained saturation while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from saturation clamp/de-saturation boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune saturation/de-saturation counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic saturation-entry, sustained-saturation, and de-saturation recovery fixtures.
2. `0` cross-chain control-coupling violations under one-chain sustained-saturation counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under saturation/de-saturation replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in saturation/de-saturation recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: prolonged saturation clamps can create non-deterministic de-saturation transitions and replay boundary drift when backlog pressure relaxes abruptly.
- Fallback: enforce deterministic saturation-floor/cap reconciliation with bounded de-saturation steps, emit explicit saturation-transition diagnostics, and fail fast on unresolved saturation-boundary ambiguity.

### M44. Auto-Tune Telemetry-Staleness Fallback Determinism Tranche C0038 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when auto-tune control telemetry becomes stale or partially unavailable, so fresh-telemetry, stale-telemetry fallback, and telemetry-recovery permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M43` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M44-S1` (`I-0267`): harden deterministic auto-tune telemetry-staleness fallback semantics so telemetry blackout, stale-sample windows, and partial recovery cannot induce duplicate canonical IDs, missing logical events, cross-chain throttle bleed, or cursor regression.
2. `M44-S2` (`I-0268`): execute QA counterexample gate for auto-tune telemetry-staleness fallback determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under fresh telemetry, stale-telemetry fallback, and telemetry-recovery permutations converge to one canonical tuple output set per chain.
2. Telemetry staleness or blackout on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under telemetry-fallback replay/resume permutations.
4. Replay/resume from telemetry-staleness fallback boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject fresh-telemetry, stale-telemetry fallback, and telemetry-recovery permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain telemetry blackout/staleness while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from telemetry-fallback boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune telemetry-staleness fallback counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic fresh-telemetry, stale-telemetry fallback, and telemetry-recovery fixtures.
2. `0` cross-chain control-coupling violations under one-chain telemetry-blackout counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under telemetry-fallback replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in telemetry-fallback recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: stale or partially missing control telemetry can cause non-deterministic fallback/profile toggling and replay boundary drift during telemetry recovery windows.
- Fallback: enforce deterministic telemetry-staleness TTL gating with bounded fallback hold windows, emit explicit telemetry-fallback diagnostics, and fail fast on unresolved telemetry-boundary ambiguity.

### M45. Auto-Tune Operator-Override Reconciliation Determinism Tranche C0039 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when operators pin, unpin, or temporarily disable auto-tune control policies, so auto-mode, manual-profile override, and operator-return-to-auto permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M44` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M45-S1` (`I-0272`): harden deterministic auto-tune operator-override reconciliation semantics so manual profile pinning/unpinning, temporary auto-tune disable, and return-to-auto transitions cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M45-S2` (`I-0273`): execute QA counterexample gate for auto-tune operator-override reconciliation determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under auto-mode, manual-profile override, and override-release-to-auto permutations converge to one canonical tuple output set per chain.
2. Operator override transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under operator-override replay/resume permutations.
4. Replay/resume from operator-override transition boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject auto-mode, manual-override hold, and override-release permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain manual-profile pin/unpin transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from operator-override boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune operator-override reconciliation counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic auto-mode, manual-override hold, and override-release fixtures.
2. `0` cross-chain control-coupling violations under one-chain operator-override transition counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under operator-override replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in operator-override recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: operator override/unoverride transitions can race with backlog pressure and trigger non-deterministic control-state reconciliation near boundary ticks.
- Fallback: enforce deterministic override state machine with explicit hold/release boundaries, emit per-chain operator-override diagnostics, and fail fast on unresolved override-boundary ambiguity.

### M46. Auto-Tune Policy-Version Rollout Reconciliation Determinism Tranche C0040 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when auto-tune policy versions roll forward, roll back, or partially apply while runtimes continue indexing, so policy-v1 baseline, policy-v2 rollout, and rollback/re-apply permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M45` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M46-S1` (`I-0277`): harden deterministic auto-tune policy-version rollout reconciliation semantics so policy roll-forward, partial-apply, rollback, and re-apply transitions cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M46-S2` (`I-0278`): execute QA counterexample gate for auto-tune policy-version rollout determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under policy-v1 baseline, policy-v2 rollout, and rollback-to-v1/re-apply-to-v2 permutations converge to one canonical tuple output set per chain.
2. Policy rollout or rollback transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under policy-version rollout replay/resume permutations.
4. Replay/resume from policy-version transition boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject policy-v1 baseline, policy-v2 rollout, rollback-to-v1, and re-apply-to-v2 permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain policy-version rollout/rollback transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from policy-version transition boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune policy-version rollout counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic policy-v1 baseline, policy-v2 rollout, rollback-to-v1, and re-apply-to-v2 fixtures.
2. `0` cross-chain control-coupling violations under one-chain policy-version transition counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under policy-version rollout replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in policy-version transition recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: policy-version rollout/rollback boundaries can race with in-flight control-state reconciliation and create non-deterministic mixed-policy windows near tick boundaries.
- Fallback: enforce deterministic policy-version activation fences with explicit per-chain rollout epoch markers, pin conservative policy on unresolved mixed-policy windows, and fail fast on policy-boundary ambiguity.

### M47. Auto-Tune Policy-Manifest Refresh Reconciliation Determinism Tranche C0041 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when auto-tune policy manifests refresh out-of-order, arrive stale, or re-apply after transient config-channel drift while runtimes continue indexing, so manifest-v2a baseline, manifest-v2b refresh, stale-refresh reject, and digest re-apply permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M46` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M47-S1` (`I-0282`): harden deterministic auto-tune policy-manifest refresh reconciliation semantics so refresh-apply, stale-refresh reject, and digest re-apply transitions cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M47-S2` (`I-0283`): execute QA counterexample gate for auto-tune policy-manifest refresh determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under manifest-v2a baseline, manifest-v2b refresh, stale-refresh reject, and digest re-apply permutations converge to one canonical tuple output set per chain.
2. Manifest refresh or stale-refresh reject transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under manifest-refresh replay/resume permutations.
4. Replay/resume from policy-manifest transition boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject manifest-v2a baseline, manifest-v2b refresh, stale-refresh delivery, and digest re-apply permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain policy-manifest refresh/reject transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from policy-manifest transition boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune policy-manifest refresh counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic manifest-v2a baseline, manifest-v2b refresh, stale-refresh reject, and digest re-apply fixtures.
2. `0` cross-chain control-coupling violations under one-chain policy-manifest transition counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under policy-manifest refresh replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in policy-manifest transition recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: out-of-order or stale policy-manifest refresh signals can race with active policy-version state and create non-deterministic digest lineage near control-loop tick boundaries.
- Fallback: enforce deterministic policy-manifest digest lineage fences with monotonic refresh epochs, pin last-verified digest on unresolved lineage ambiguity, and fail fast on manifest-boundary ambiguity.

### M48. Auto-Tune Policy-Manifest Sequence-Gap Recovery Determinism Tranche C0042 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when policy-manifest sequence updates arrive with transient gaps, duplicate segment re-delivery, or late gap-fill recovery while runtimes continue indexing, so sequence-complete baseline, gap-hold, late-gap-fill apply, and duplicate segment re-apply permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M47` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M48-S1` (`I-0287`): harden deterministic auto-tune policy-manifest sequence-gap reconciliation semantics so sequence-gap hold, late gap-fill apply, and duplicate segment re-delivery cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M48-S2` (`I-0288`): execute QA counterexample gate for auto-tune policy-manifest sequence-gap recovery determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under sequence-complete baseline, sequence-gap hold, late gap-fill apply, and duplicate segment re-apply permutations converge to one canonical tuple output set per chain.
2. Sequence-gap hold and late gap-fill transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under policy-manifest sequence-gap replay/resume permutations.
4. Replay/resume from policy-manifest sequence-gap recovery boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject sequence-complete baseline, sequence-gap hold, late gap-fill apply, and duplicate segment re-apply permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain policy-manifest sequence-gap recovery while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from policy-manifest sequence-gap recovery boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune policy-manifest sequence-gap recovery counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic sequence-complete baseline, sequence-gap hold, late gap-fill apply, and duplicate segment re-apply fixtures.
2. `0` cross-chain control-coupling violations under one-chain policy-manifest sequence-gap recovery counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under policy-manifest sequence-gap recovery replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in policy-manifest sequence-gap recovery fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: sequence-gap boundaries can race with manifest digest lineage advancement and create non-deterministic deferred-apply windows near control-loop tick boundaries.
- Fallback: enforce deterministic contiguous-sequence apply fences with explicit gap-hold diagnostics, pin last contiguous verified sequence on ambiguity, and fail fast on unresolved sequence-gap boundary conflicts.

### M49. Auto-Tune Policy-Manifest Snapshot-Cutover Determinism Tranche C0043 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when policy-manifest delivery shifts between segment-tail updates and compact snapshot cutovers, including stale snapshot delivery and snapshot-tail re-apply during replay/resume, so sequence-tail baseline, snapshot-cutover apply, stale-snapshot reject, and snapshot+tail re-apply permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M48` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M49-S1` (`I-0292`): harden deterministic auto-tune policy-manifest snapshot-cutover reconciliation semantics so snapshot-cutover apply, stale snapshot reject, and snapshot+tail re-apply cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M49-S2` (`I-0293`): execute QA counterexample gate for auto-tune policy-manifest snapshot-cutover determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under sequence-tail baseline, snapshot-cutover apply, stale snapshot reject, and snapshot+tail re-apply permutations converge to one canonical tuple output set per chain.
2. Snapshot-cutover apply or stale snapshot reject transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under policy-manifest snapshot-cutover replay/resume permutations.
4. Replay/resume from policy-manifest snapshot-cutover boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject sequence-tail baseline, snapshot-cutover apply, stale snapshot reject, and snapshot+tail re-apply permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain policy-manifest snapshot-cutover transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from policy-manifest snapshot-cutover boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune policy-manifest snapshot-cutover counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic sequence-tail baseline, snapshot-cutover apply, stale snapshot reject, and snapshot+tail re-apply fixtures.
2. `0` cross-chain control-coupling violations under one-chain policy-manifest snapshot-cutover counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under policy-manifest snapshot-cutover replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in policy-manifest snapshot-cutover fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: snapshot base-sequence boundaries can race with in-flight segment-tail windows and create non-deterministic overlap ownership near control-loop tick boundaries.
- Fallback: enforce deterministic snapshot-base activation fences with explicit snapshot lineage diagnostics, pin last verified snapshot+tail boundary on ambiguity, and fail fast on unresolved snapshot-overlap conflicts.

### M50. Auto-Tune Policy-Manifest Rollback-Lineage Determinism Tranche C0044 (P0)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when policy-manifest lineage must roll back to a previously valid digest due to control-plane correction or source rewind and then re-advance, so forward-lineage baseline, rollback-apply, stale-rollback reject, and rollback+re-forward re-apply permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M49` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M50-S1` (`I-0297`): harden deterministic auto-tune policy-manifest rollback-lineage reconciliation semantics so rollback-apply, stale rollback reject, and rollback+re-forward re-apply cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M50-S2` (`I-0298`): execute QA counterexample gate for auto-tune policy-manifest rollback-lineage determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under forward-lineage baseline, rollback-apply, stale rollback reject, and rollback+re-forward re-apply permutations converge to one canonical tuple output set per chain.
2. Rollback-lineage apply or stale rollback reject transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under policy-manifest rollback-lineage replay/resume permutations.
4. Replay/resume from policy-manifest rollback-lineage boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject forward-lineage baseline, rollback-apply, stale rollback reject, and rollback+re-forward re-apply permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain policy-manifest rollback-lineage transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from policy-manifest rollback-lineage boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus auto-tune policy-manifest rollback-lineage counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic forward-lineage baseline, rollback-apply, stale rollback reject, and rollback+re-forward re-apply fixtures.
2. `0` cross-chain control-coupling violations under one-chain policy-manifest rollback-lineage counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under policy-manifest rollback-lineage replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in policy-manifest rollback-lineage fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: rollback-lineage boundaries can race with in-flight snapshot+tail state and create non-deterministic digest ownership near control-loop tick boundaries.
- Fallback: enforce deterministic rollback activation fences with explicit lineage-epoch diagnostics, pin last verified rollback-safe digest on ambiguity, and fail fast on unresolved rollback-lineage ownership conflicts.

### M51. Auto-Tune Policy-Manifest Rollback-Crashpoint Replay Determinism Tranche C0045 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when process abort/restart occurs during rollback-lineage transition windows, so forward-lineage baseline, rollback-apply crash, rollback checkpoint-resume, and rollback+re-forward crash-resume permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M50` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M51-S1` (`I-0300`): harden deterministic rollback-lineage phase checkpointing and replay semantics so crash/restart during rollback-apply or rollback+re-forward transitions cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M51-S2` (`I-0301`): execute QA counterexample gate for rollback-crashpoint replay determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under forward-lineage baseline, rollback-apply crash, rollback checkpoint-resume, and rollback+re-forward crash-resume permutations converge to one canonical tuple output set per chain.
2. Crash/restart during rollback transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under rollback-crashpoint replay/resume permutations.
4. Replay/resume from rollback transition checkpoints remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject forward-lineage baseline, rollback-apply crash, rollback checkpoint-resume, and rollback+re-forward crash-resume permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain rollback-crashpoint transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from rollback transition checkpoints assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus rollback-crashpoint replay counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic forward-lineage baseline, rollback-apply crash, rollback checkpoint-resume, and rollback+re-forward crash-resume fixtures.
2. `0` cross-chain control-coupling violations under one-chain rollback-crashpoint counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under rollback-crashpoint replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in rollback-crashpoint fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: rollback transition phase checkpoints can race with in-flight re-forward application and create non-deterministic resume ownership near process restart boundaries.
- Fallback: enforce deterministic rollback phase fences with explicit checkpoint-phase diagnostics, pin last verified rollback-safe phase boundary on ambiguity, and fail fast on unresolved rollback phase ownership conflicts.

### M52. Auto-Tune Policy-Manifest Rollback-Crashpoint Checkpoint-Fence Determinism Tranche C0046 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when rollback-crashpoint recovery replays across checkpoint flush/ownership fence boundaries, so no-fence baseline, crash-before-fence-flush, crash-after-fence-flush, and rollback+re-forward fence-resume permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M51` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M52-S1` (`I-0305`): harden deterministic rollback checkpoint-fence ownership reconciliation so crash/restart around fence flush/restore cannot induce duplicate canonical IDs, missing logical events, cross-chain control bleed, or cursor regression.
2. `M52-S2` (`I-0306`): execute QA counterexample gate for rollback checkpoint-fence determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under no-fence baseline, crash-before-fence-flush, crash-after-fence-flush, and rollback+re-forward fence-resume permutations converge to one canonical tuple output set per chain.
2. Rollback fence replay/restore on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under rollback checkpoint-fence replay/resume permutations.
4. Replay/resume from rollback checkpoint-fence boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject no-fence baseline, crash-before-fence-flush, crash-after-fence-flush, and rollback+re-forward fence-resume permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain rollback checkpoint-fence replay/restore transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from rollback checkpoint-fence boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus rollback checkpoint-fence counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic no-fence baseline, crash-before-fence-flush, crash-after-fence-flush, and rollback+re-forward fence-resume fixtures.
2. `0` cross-chain control-coupling violations under one-chain rollback checkpoint-fence counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under rollback checkpoint-fence replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in rollback checkpoint-fence fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: checkpoint-fence ownership state can diverge from persisted rollback phase markers under crash timing races near flush/restore boundaries.
- Fallback: enforce deterministic fence ownership epochs with explicit fence-state diagnostics, pin last verified rollback-safe fence boundary on ambiguity, and fail fast on unresolved fence ownership conflicts.

### M53. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Epoch-Compaction Determinism Tranche C0047 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when long-running rollback checkpoint-fence state is compacted or epoch-pruned, so no-compaction baseline, live compaction, crash-during-compaction restart, and rollback+re-forward after compaction permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M52` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M53-S1` (`I-0308`): harden deterministic rollback checkpoint-fence epoch-compaction reconciliation so compaction/pruning and restart timing cannot reactivate stale fence ownership, induce duplicate canonical IDs, suppress valid logical events, or regress cursor monotonicity.
2. `M53-S2` (`I-0309`): execute QA counterexample gate for rollback checkpoint-fence epoch-compaction determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under no-compaction baseline, live compaction, crash-during-compaction restart, and rollback+re-forward after compaction permutations converge to one canonical tuple output set per chain.
2. Compaction/pruning transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under rollback checkpoint-fence epoch-compaction replay/resume permutations.
4. Replay/resume from rollback checkpoint-fence compaction boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject no-compaction baseline, live compaction, crash-during-compaction restart, and rollback+re-forward after-compaction permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain rollback checkpoint-fence compaction/replay transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from rollback checkpoint-fence compaction boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus rollback checkpoint-fence compaction counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic no-compaction baseline, live compaction, crash-during-compaction restart, and rollback+re-forward after-compaction fixtures.
2. `0` cross-chain control-coupling violations under one-chain rollback checkpoint-fence compaction counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under rollback checkpoint-fence compaction replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in rollback checkpoint-fence compaction fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: epoch-compaction pruning can race with rollback fence replay markers and create non-deterministic stale-fence reactivation near restart boundaries.
- Fallback: enforce deterministic fence-epoch tombstones with explicit compaction-lineage diagnostics, pin last verified rollback-safe compaction boundary on ambiguity, and fail fast on unresolved stale-fence ownership conflicts.

### M54. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Tombstone-Expiry Determinism Tranche C0048 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when rollback checkpoint-fence tombstones are age-pruned after epoch compaction, so tombstone-retained baseline, tombstone-expiry sweep, crash-during-expiry restart, and rollback+re-forward after-expiry permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M53` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M54-S1` (`I-0313`): harden deterministic rollback checkpoint-fence tombstone-expiry reconciliation so expiry/pruning and restart timing cannot reopen stale rollback ownership, induce duplicate canonical IDs, suppress valid logical events, or regress cursor monotonicity.
2. `M54-S2` (`I-0314`): execute QA counterexample gate for rollback checkpoint-fence tombstone-expiry determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under tombstone-retained baseline, tombstone-expiry sweep, crash-during-expiry restart, and rollback+re-forward after-expiry permutations converge to one canonical tuple output set per chain.
2. Tombstone expiry/pruning transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under rollback checkpoint-fence tombstone-expiry replay/resume permutations.
4. Replay/resume from rollback checkpoint-fence tombstone-expiry boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject tombstone-retained baseline, tombstone-expiry sweep, crash-during-expiry restart, and rollback+re-forward after-expiry permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain rollback checkpoint-fence tombstone-expiry transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from rollback checkpoint-fence tombstone-expiry boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus rollback checkpoint-fence tombstone-expiry counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic tombstone-retained baseline, tombstone-expiry sweep, crash-during-expiry restart, and rollback+re-forward after-expiry fixtures.
2. `0` cross-chain control-coupling violations under one-chain rollback checkpoint-fence tombstone-expiry counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under rollback checkpoint-fence tombstone-expiry replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in rollback checkpoint-fence tombstone-expiry fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: tombstone-expiry windows can race with late rollback replay markers and create non-deterministic post-expiry stale-fence ownership reactivation near restart boundaries.
- Fallback: enforce deterministic minimum-retention expiry epochs with explicit tombstone-expiry lineage diagnostics, pin last verified rollback-safe pre-expiry boundary on ambiguity, and fail fast on unresolved post-expiry ownership conflicts.

### M55. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Post-Expiry Late-Marker Quarantine Determinism Tranche C0049 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when rollback markers arrive after tombstone expiry, so on-time marker baseline, late-marker quarantine, crash-during-quarantine restart, and rollback+re-forward after-quarantine-release permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M54` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M55-S1` (`I-0318`): harden deterministic rollback checkpoint-fence post-expiry late-marker quarantine reconciliation so delayed rollback markers cannot reopen stale ownership, re-emit canonical IDs, suppress valid logical events, or regress cursor monotonicity.
2. `M55-S2` (`I-0319`): execute QA counterexample gate for rollback checkpoint-fence post-expiry late-marker quarantine determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under on-time marker baseline, late-marker quarantine, crash-during-quarantine restart, and rollback+re-forward after-quarantine-release permutations converge to one canonical tuple output set per chain.
2. Post-expiry late-marker quarantine transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under rollback checkpoint-fence post-expiry late-marker replay/resume permutations.
4. Replay/resume from rollback checkpoint-fence post-expiry late-marker quarantine boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject on-time marker baseline, late-marker quarantine, crash-during-quarantine restart, and rollback+re-forward after-quarantine-release permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain rollback checkpoint-fence post-expiry late-marker quarantine transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from rollback checkpoint-fence post-expiry late-marker quarantine boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus rollback checkpoint-fence post-expiry late-marker quarantine counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic on-time marker baseline, late-marker quarantine, crash-during-quarantine restart, and rollback+re-forward after-quarantine-release fixtures.
2. `0` cross-chain control-coupling violations under one-chain rollback checkpoint-fence post-expiry late-marker quarantine counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under rollback checkpoint-fence post-expiry late-marker replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in rollback checkpoint-fence post-expiry late-marker quarantine fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: late rollback markers can arrive after expiry pruning and race with quarantine-release checkpoints, creating non-deterministic stale-ownership resurrection near restart boundaries.
- Fallback: enforce deterministic late-marker quarantine epochs with explicit post-expiry lineage diagnostics, pin last verified rollback-safe pre-release boundary on ambiguity, and fail fast on unresolved post-expiry marker ownership conflicts.

### M56. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Post-Quarantine Release-Window Determinism Tranche C0050 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when quarantined rollback markers are released under concurrent live marker flow, so deterministic release-only baseline, staggered release window, crash-during-release restart, and rollback+re-forward after-release-window permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M55` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M56-S1` (`I-0321`): harden deterministic rollback checkpoint-fence post-quarantine release-window reconciliation so released delayed rollback markers and live markers cannot interleave into stale ownership reopening, canonical ID re-emission, logical event suppression, or cursor monotonicity regression.
2. `M56-S2` (`I-0322`): execute QA counterexample gate for rollback checkpoint-fence post-quarantine release-window determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under deterministic release-only baseline, staggered release window, crash-during-release restart, and rollback+re-forward after-release-window permutations converge to one canonical tuple output set per chain.
2. Post-quarantine release-window transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under rollback checkpoint-fence post-quarantine release-window replay/resume permutations.
4. Replay/resume from rollback checkpoint-fence post-quarantine release boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject release-only baseline, staggered release window, crash-during-release restart, and rollback+re-forward after-release-window permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain rollback checkpoint-fence post-quarantine release-window transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from rollback checkpoint-fence post-quarantine release boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus rollback checkpoint-fence post-quarantine release-window counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic release-only baseline, staggered release window, crash-during-release restart, and rollback+re-forward after-release-window fixtures.
2. `0` cross-chain control-coupling violations under one-chain rollback checkpoint-fence post-quarantine release-window counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under rollback checkpoint-fence post-quarantine release replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in rollback checkpoint-fence post-quarantine release-window fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: release-window batching can race with freshly observed rollback markers and create non-deterministic marker-order ownership arbitration near restart boundaries.
- Fallback: enforce deterministic release-window sequencing with explicit release-watermark lineage diagnostics, pin last verified rollback-safe pre-release-window boundary on ambiguity, and fail fast on unresolved release-window ownership conflicts.

### M57. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Post-Release-Window Epoch-Rollover Determinism Tranche C0051 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when release-window state crosses policy-manifest epoch boundaries, so release-window-closed baseline, epoch-rollover adoption, crash-during-rollover restart, and rollback+re-forward after-rollover permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M56` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M57-S1` (`I-0326`): harden deterministic rollback checkpoint-fence post-release-window epoch-rollover reconciliation so prior-epoch delayed markers and current-epoch live markers cannot interleave into stale ownership reopening, canonical ID re-emission, logical event suppression, or cursor monotonicity regression.
2. `M57-S2` (`I-0327`): execute QA counterexample gate for rollback checkpoint-fence post-release-window epoch-rollover determinism and invariant evidence, including reproducible failure fanout when invariants fail.
3. `M57-F1` (`I-0328`): close the QA-discovered one-chain coverage gap by adding deterministic post-release-window epoch-rollover isolation coverage and stale prior-epoch fence rejection checks.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under release-window-closed baseline, epoch-rollover adoption, crash-during-rollover restart, and rollback+re-forward after-rollover permutations converge to one canonical tuple output set per chain.
2. Post-release-window epoch-rollover transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under rollback checkpoint-fence post-release-window epoch-rollover replay/resume permutations.
4. Replay/resume from rollback checkpoint-fence post-release-window epoch-rollover boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject release-window-closed baseline, epoch-rollover adoption, crash-during-rollover restart, and rollback+re-forward after-rollover permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain rollback checkpoint-fence post-release-window epoch-rollover transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from rollback checkpoint-fence post-release-window epoch-rollover boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus rollback checkpoint-fence post-release-window epoch-rollover counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic release-window-closed baseline, epoch-rollover adoption, crash-during-rollover restart, and rollback+re-forward after-rollover fixtures.
2. `0` cross-chain control-coupling violations under one-chain rollback checkpoint-fence post-release-window epoch-rollover counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under rollback checkpoint-fence post-release-window epoch-rollover replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in rollback checkpoint-fence post-release-window epoch-rollover fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: epoch-rollover activation can race with late prior-epoch release markers and create non-deterministic epoch/watermark ownership arbitration near restart boundaries.
- Fallback: enforce deterministic `(epoch, release_watermark)` ordering with explicit epoch-rollover lineage diagnostics, pin last verified rollback-safe pre-rollover boundary on ambiguity, and fail fast on unresolved cross-epoch ownership conflicts.

### M58. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Post-Epoch-Rollover Late-Bridge Reconciliation Determinism Tranche C0052 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when delayed rollback markers bridge multiple policy-manifest epochs, so single-epoch baseline, multi-epoch late-bridge replay, crash-during-bridge reconciliation restart, and rollback+re-forward after bridge adoption permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M57` exit gate green, including the one-chain epoch-rollover isolation coverage fixed in `I-0328`.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M58-S1` (`I-0330`): harden deterministic post-epoch-rollover late-bridge reconciliation so delayed markers from prior epochs cannot reclaim stale ownership, re-emit canonical IDs, suppress valid logical events, or regress cursor monotonicity when newer epoch state is already active.
2. `M58-S2` (`I-0331`): execute QA counterexample gate for post-epoch-rollover late-bridge reconciliation determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under single-epoch baseline, multi-epoch late-bridge replay, crash-during-bridge reconciliation restart, and rollback+re-forward after bridge adoption permutations converge to one canonical tuple output set per chain.
2. Post-epoch-rollover late-bridge transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under post-epoch-rollover late-bridge replay/resume permutations.
4. Replay/resume from post-epoch-rollover late-bridge boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject single-epoch baseline, multi-epoch late-bridge replay, crash-during-bridge reconciliation restart, and rollback+re-forward after bridge adoption permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain post-epoch-rollover late-bridge transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from post-epoch-rollover late-bridge boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus post-epoch-rollover late-bridge counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic single-epoch baseline, multi-epoch late-bridge replay, crash-during-bridge reconciliation restart, and rollback+re-forward after bridge adoption fixtures.
2. `0` cross-chain control-coupling violations under one-chain post-epoch-rollover late-bridge counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under post-epoch-rollover late-bridge replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in post-epoch-rollover late-bridge fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: late-bridge reconciliation across multiple epochs can race with live current-epoch markers and create non-deterministic cross-epoch ownership arbitration near restart boundaries.
- Fallback: enforce deterministic `(epoch, bridge_sequence, release_watermark)` ordering with explicit late-bridge lineage diagnostics, pin last verified rollback-safe pre-bridge boundary on ambiguity, and fail fast on unresolved cross-epoch bridge ownership conflicts.

### M59. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Post-Late-Bridge Backlog-Drain Determinism Tranche C0053 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk when quarantined late-bridge markers are drained back into live flow, so no-backlog baseline, staged backlog-drain replay, crash-during-drain restart, and rollback+re-forward after-drain permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M58` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M59-S1` (`I-0335`): harden deterministic post-late-bridge backlog-drain reconciliation so drained delayed bridge markers and new live markers cannot interleave into stale ownership reopening, canonical ID re-emission, logical event suppression, or cursor monotonicity regression.
2. `M59-S2` (`I-0336`): execute QA counterexample gate for post-late-bridge backlog-drain determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under no-backlog baseline, staged backlog-drain replay, crash-during-drain restart, and rollback+re-forward after-drain permutations converge to one canonical tuple output set per chain.
2. Post-late-bridge backlog-drain transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under post-late-bridge backlog-drain replay/resume permutations.
4. Replay/resume from post-late-bridge backlog-drain boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject no-backlog baseline, staged backlog-drain replay, crash-during-drain restart, and rollback+re-forward after-drain permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain post-late-bridge backlog-drain transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from post-late-bridge backlog-drain boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus post-late-bridge backlog-drain counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic no-backlog baseline, staged backlog-drain replay, crash-during-drain restart, and rollback+re-forward after-drain fixtures.
2. `0` cross-chain control-coupling violations under one-chain post-late-bridge backlog-drain counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under post-late-bridge backlog-drain replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in post-late-bridge backlog-drain fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: backlog-drain activation can race with newly observed live bridge markers and create non-deterministic drain-order ownership arbitration near restart boundaries.
- Fallback: enforce deterministic `(epoch, bridge_sequence, drain_watermark)` ordering with explicit backlog-drain lineage diagnostics, pin last verified rollback-safe pre-drain boundary on ambiguity, and fail fast on unresolved post-bridge drain ownership conflicts.

### M60. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Post-Backlog-Drain Live-Catchup Determinism Tranche C0054 (P0, Completed)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk at the drain-to-live handoff boundary, so live-only baseline, drain-to-live catchup replay, crash-at-drain-complete restart, and rollback+re-forward after catchup permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M59` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M60-S1` (`I-0340`): harden deterministic post-backlog-drain live-catchup handoff so drain completion and concurrent live marker progression cannot reopen stale ownership, re-emit canonical IDs, suppress valid logical events, or regress cursor monotonicity.
2. `M60-S2` (`I-0341`): execute QA counterexample gate for post-backlog-drain live-catchup determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under live-only baseline, drain-to-live catchup replay, crash-at-drain-complete restart, and rollback+re-forward after catchup permutations converge to one canonical tuple output set per chain.
2. Post-backlog-drain live-catchup transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under post-backlog-drain live-catchup replay/resume permutations.
4. Replay/resume from post-backlog-drain live-catchup boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject live-only baseline, drain-to-live catchup replay, crash-at-drain-complete restart, and rollback+re-forward after catchup permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain post-backlog-drain live-catchup transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from post-backlog-drain live-catchup boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus post-backlog-drain live-catchup counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic live-only baseline, drain-to-live catchup replay, crash-at-drain-complete restart, and rollback+re-forward after catchup fixtures.
2. `0` cross-chain control-coupling violations under one-chain post-backlog-drain live-catchup counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under post-backlog-drain live-catchup replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in post-backlog-drain live-catchup fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: drain completion can race with new live-head marker advancement and create non-deterministic handoff ownership arbitration near restart boundaries.
- Fallback: enforce deterministic `(epoch, bridge_sequence, drain_watermark, live_head)` handoff ordering with explicit drain-complete lineage diagnostics, pin last verified rollback-safe pre-handoff boundary on ambiguity, and fail fast on unresolved drain/live ownership conflicts.

### M61. Auto-Tune Policy-Manifest Rollback Checkpoint-Fence Post-Live-Catchup Steady-State Rebaseline Determinism Tranche C0055 (P0, Next)

#### Objective
Eliminate duplicate/missing-event and cursor-safety risk at the catchup-to-steady rebaseline boundary, so steady-state-only baseline, catchup-to-steady rebaseline replay, crash-after-rebaseline restart, and rollback+re-forward after-rebaseline permutations converge to one deterministic canonical output set per chain.

#### Entry Gate
- `M60` exit gate green.
- Fail-fast panic contract from `M34` remains enforced for correctness-impacting failures.
- Mandatory runtime targets (`solana-devnet`, `base-sepolia`, `btc-testnet`) are wireable in chain-scoped deployment modes.

#### Slices
1. `M61-S1` (`I-0344`): harden deterministic post-live-catchup steady-state rebaseline reconciliation so rebaseline commitment and concurrent late-catchup marker arrival cannot reopen stale ownership, re-emit canonical IDs, suppress valid logical events, or regress cursor monotonicity.
2. `M61-S2` (`I-0345`): execute QA counterexample gate for post-live-catchup steady-state rebaseline determinism and invariant evidence, including reproducible failure fanout when invariants fail.

#### Definition Of Done
1. Equivalent tri-chain logical ranges processed under steady-state-only baseline, catchup-to-steady rebaseline replay, crash-after-rebaseline restart, and rollback+re-forward after-rebaseline permutations converge to one canonical tuple output set per chain.
2. Post-live-catchup steady-state rebaseline transitions on one chain cannot induce cross-chain control coupling, cross-chain cursor bleed, or fail-fast regressions on other mandatory chains.
3. Solana/Base fee-event semantics and BTC signed-delta conservation remain deterministic under post-live-catchup steady-state rebaseline replay/resume permutations.
4. Replay/resume from post-live-catchup steady-state rebaseline boundaries remains idempotent with chain-scoped cursor monotonicity and no failed-path cursor/watermark progression.
5. Runtime wiring invariants remain green across all mandatory chains.

#### Test Contract
1. Deterministic tests inject steady-state-only baseline, catchup-to-steady rebaseline replay, crash-after-rebaseline restart, and rollback+re-forward after-rebaseline permutations for equivalent tri-chain logical ranges and assert canonical tuple convergence to one deterministic baseline output set.
2. Deterministic tests inject one-chain post-live-catchup steady-state rebaseline transitions while the other two chains progress and assert `0` cross-chain control-coupling violations plus `0` duplicate/missing logical events.
3. Deterministic replay/resume tests from post-live-catchup steady-state rebaseline boundaries assert Solana/Base fee-event continuity, BTC signed-delta conservation, `0` balance drift, and chain-scoped cursor/watermark safety.
4. QA executes required validation commands plus post-live-catchup steady-state rebaseline counterexample checks and records invariant-level evidence under `.ralph/reports/`.

#### Exit Gate (Measurable)
1. `0` canonical tuple diffs across deterministic steady-state-only baseline, catchup-to-steady rebaseline replay, crash-after-rebaseline restart, and rollback+re-forward after-rebaseline fixtures.
2. `0` cross-chain control-coupling violations under one-chain post-live-catchup steady-state rebaseline counterexamples.
3. `0` duplicate canonical IDs and `0` missing logical events under post-live-catchup steady-state rebaseline replay permutations.
4. `0` cursor monotonicity or failed-path watermark-safety violations in post-live-catchup steady-state rebaseline fixtures.
5. `0` regressions on invariants: `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `solana_fee_event_coverage`, `base_fee_split_coverage`, `chain_adapter_runtime_wired`.
6. Validation commands pass.

#### Risk Gate + Fallback
- Gate: steady-state rebaseline activation can race with delayed catchup-tail markers and create non-deterministic steady-watermark ownership arbitration near restart boundaries.
- Fallback: enforce deterministic `(epoch, bridge_sequence, drain_watermark, live_head, steady_state_watermark)` rebaseline ordering with explicit rebaseline lineage diagnostics, pin last verified rollback-safe pre-rebaseline boundary on ambiguity, and fail fast on unresolved catchup/steady ownership conflicts.

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

4. `DP-0101-D`: commit scheduling scope.
- Preferred: chain-scoped commit scheduler per `ChainRuntime`; no cross-chain shared interleaver in production runtime.
- Fallback: if legacy shared scheduling code remains, keep it non-routable and block startup when enabled.

5. `DP-0101-E`: auto-tune control scope.
- Preferred: per-chain control loop with chain-local inputs only; no cross-chain coupled throttling.
- Fallback: disable auto-tune and run deterministic fixed coordinator profile until signal quality is restored.

6. `DP-0101-F`: auto-tune restart/profile-transition state policy.
- Preferred: deterministic chain-local restart/profile-transition state handoff with explicit baseline reset and bounded warm-start adoption.
- Fallback: force deterministic baseline profile on every restart/profile transition until restart-state contracts are extended.

7. `DP-0101-G`: auto-tune signal-flap hysteresis policy.
- Preferred: deterministic chain-local hysteresis/debounce evaluation with bounded cooldown transitions and explicit flap diagnostics.
- Fallback: pin deterministic conservative control profile during sustained signal-flap windows until hysteresis contracts are extended.

8. `DP-0101-H`: auto-tune saturation/de-saturation policy.
- Preferred: deterministic chain-local saturation-cap enforcement with bounded de-saturation transitions and explicit saturation-boundary diagnostics.
- Fallback: pin deterministic conservative profile while saturation pressure is unresolved, then replay from last-safe boundary once saturation-transition contracts are restored.

9. `DP-0101-I`: auto-tune telemetry-staleness fallback policy.
- Preferred: deterministic chain-local telemetry-staleness TTL evaluation with explicit fallback hold/release semantics and per-chain telemetry-fallback diagnostics.
- Fallback: pin deterministic conservative profile while telemetry freshness is unresolved, then replay from last-safe boundary once telemetry-freshness contracts are restored.

10. `DP-0101-J`: auto-tune operator-override reconciliation policy.
- Preferred: deterministic chain-local override state machine with explicit profile-pin hold semantics and deterministic release-to-auto boundaries.
- Fallback: pin deterministic conservative profile during override transitions and resume auto-tune only after replay-safe boundary confirmation is proven.

11. `DP-0101-K`: auto-tune policy-version rollout policy.
- Preferred: deterministic chain-local policy-version activation/rollback state machine with explicit rollout epoch boundaries and replay-stable policy lineage markers.
- Fallback: pin deterministic conservative policy version during mixed-policy ambiguity and resume rollout only after replay-safe boundary confirmation is proven.

12. `DP-0101-L`: auto-tune policy-manifest refresh policy.
- Preferred: deterministic chain-local policy-manifest digest lineage state machine with monotonic refresh epochs, stale-refresh rejection, and replay-stable manifest markers.
- Fallback: pin last-verified deterministic manifest digest during lineage ambiguity and resume refresh only after replay-safe boundary confirmation is proven.

13. `DP-0101-M`: auto-tune policy-manifest sequence-gap recovery policy.
- Preferred: deterministic chain-local contiguous sequence-apply state machine with explicit gap-hold windows, late gap-fill apply, and replay-stable segment lineage markers.
- Fallback: pin last contiguous verified sequence while manifest segment gaps are unresolved, reject ambiguous duplicate segment lineage, and resume sequence apply only after replay-safe boundary confirmation is proven.

14. `DP-0101-N`: auto-tune policy-manifest snapshot-cutover reconciliation policy.
- Preferred: deterministic chain-local snapshot-cutover state machine with explicit base-sequence activation fences, stale-snapshot rejection, and replay-stable snapshot+tail lineage markers.
- Fallback: pin last verified snapshot+tail boundary while snapshot lineage is ambiguous, reject overlapping snapshot/segment ownership, and resume snapshot apply only after replay-safe boundary confirmation is proven.

15. `DP-0101-O`: auto-tune policy-manifest rollback-lineage reconciliation policy.
- Preferred: deterministic chain-local rollback-lineage state machine with explicit rollback epoch fences, stale-rollback rejection, and replay-stable rollback+re-forward lineage markers.
- Fallback: pin last verified rollback-safe digest while rollback lineage is ambiguous, reject overlapping forward/rollback ownership windows, and resume lineage apply only after replay-safe boundary confirmation is proven.

16. `DP-0101-P`: auto-tune policy-manifest rollback-crashpoint replay policy.
- Preferred: deterministic chain-local rollback transition phase checkpoints with replay-stable rollback/apply/resume markers, explicit crash-boundary ownership fencing, and stale-phase resume rejection.
- Fallback: pin last verified rollback-safe phase boundary during restart ambiguity, reject overlapping rollback/re-forward resume ownership windows, and resume lineage apply only after replay-safe checkpoint confirmation is proven.

17. `DP-0101-Q`: auto-tune policy-manifest rollback checkpoint-fence ownership policy.
- Preferred: deterministic chain-local checkpoint-fence ownership epochs with replay-stable fence flush/restore markers, explicit crash-boundary fence arbitration, and stale-fence replay rejection.
- Fallback: pin last verified rollback-safe fence boundary during restart ambiguity, reject overlapping fence ownership windows, and resume lineage apply only after replay-safe fence confirmation is proven.

18. `DP-0101-R`: auto-tune policy-manifest rollback checkpoint-fence epoch-compaction policy.
- Preferred: deterministic chain-local fence-epoch compaction state machine with explicit tombstone lineage markers, replay-stable compaction boundaries, and stale-epoch ownership rejection.
- Fallback: pin last verified rollback-safe pre-compaction boundary during lineage ambiguity, reject compaction windows with unresolved stale-fence ownership overlap, and resume compaction only after replay-safe boundary confirmation is proven.

19. `DP-0101-S`: auto-tune policy-manifest rollback checkpoint-fence tombstone-expiry policy.
- Preferred: deterministic chain-local tombstone-retention and expiry state machine with explicit expiry-epoch lineage markers, replay-stable post-expiry boundaries, and stale post-expiry ownership rejection.
- Fallback: pin last verified rollback-safe pre-expiry boundary during expiry ambiguity, reject expiry windows with unresolved late rollback marker overlap, and resume expiry only after replay-safe boundary confirmation is proven.

20. `DP-0101-T`: auto-tune policy-manifest rollback checkpoint-fence post-expiry late-marker quarantine policy.
- Preferred: deterministic chain-local late-marker quarantine state machine with explicit post-expiry marker-hold epochs, replay-stable quarantine-release boundaries, and stale post-expiry marker ownership rejection.
- Fallback: pin last verified rollback-safe pre-release boundary during late-marker ambiguity, reject quarantine-release windows with unresolved delayed marker overlap, and resume post-expiry marker release only after replay-safe boundary confirmation is proven.

21. `DP-0101-U`: auto-tune policy-manifest rollback checkpoint-fence post-quarantine release-window policy.
- Preferred: deterministic chain-local release-window state machine with explicit release sequence fences, replay-stable release-watermark markers, and stale/duplicate release marker ownership rejection.
- Fallback: pin last verified rollback-safe pre-release-window boundary during release ambiguity, keep delayed markers quarantined, and resume release only after replay-safe release-window confirmation is proven.

22. `DP-0101-V`: auto-tune policy-manifest rollback checkpoint-fence post-release-window epoch-rollover policy.
- Preferred: deterministic chain-local epoch-rollover state machine with explicit `(epoch, release_watermark)` fences, replay-stable cross-epoch lineage markers, and stale prior-epoch marker ownership rejection.
- Fallback: pin last verified rollback-safe pre-rollover boundary during cross-epoch ambiguity, quarantine unresolved prior-epoch delayed markers, and resume epoch rollover only after replay-safe epoch lineage confirmation is proven.

23. `DP-0101-W`: auto-tune policy-manifest rollback checkpoint-fence post-epoch-rollover late-bridge reconciliation policy.
- Preferred: deterministic chain-local late-bridge reconciliation state machine with explicit `(epoch, bridge_sequence, release_watermark)` ownership fences, replay-stable multi-epoch bridge lineage markers, and stale bridge marker ownership rejection.
- Fallback: pin last verified rollback-safe pre-bridge boundary during cross-epoch bridge ambiguity, quarantine unresolved delayed bridge markers, and resume late-bridge reconciliation only after replay-safe multi-epoch lineage confirmation is proven.

24. `DP-0101-X`: auto-tune policy-manifest rollback checkpoint-fence post-late-bridge backlog-drain policy.
- Preferred: deterministic chain-local backlog-drain state machine with explicit `(epoch, bridge_sequence, drain_watermark)` ownership fences, replay-stable drained-bridge lineage markers, and stale drained-bridge marker ownership rejection.
- Fallback: pin last verified rollback-safe pre-drain boundary during post-bridge drain ambiguity, keep delayed bridge backlog quarantined, and resume backlog drain only after replay-safe drain lineage confirmation is proven.

25. `DP-0101-Y`: auto-tune policy-manifest rollback checkpoint-fence post-backlog-drain live-catchup policy.
- Preferred: deterministic chain-local drain-to-live handoff state machine with explicit `(epoch, bridge_sequence, drain_watermark, live_head)` ownership fences, replay-stable handoff lineage markers, and stale pre-handoff marker ownership rejection.
- Fallback: pin last verified rollback-safe pre-handoff boundary during drain/live ambiguity, quarantine unresolved handoff markers, and resume live catchup only after replay-safe handoff lineage confirmation is proven.

26. `DP-0101-Z`: auto-tune policy-manifest rollback checkpoint-fence post-live-catchup steady-state rebaseline policy.
- Preferred: deterministic chain-local catchup-to-steady rebaseline state machine with explicit `(epoch, bridge_sequence, drain_watermark, live_head, steady_state_watermark)` ownership fences, replay-stable rebaseline lineage markers, and stale pre-rebaseline marker ownership rejection.
- Fallback: pin last verified rollback-safe pre-rebaseline boundary during catchup/steady ambiguity, quarantine unresolved rebaseline markers, and resume steady-state rebaseline only after replay-safe lineage confirmation is proven.

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
35. `I-0165`
36. `I-0166`
37. `I-0170`
38. `I-0171`
39. `I-0175`
40. `I-0176`
41. `I-0178`
42. `I-0179`
43. `I-0183`
44. `I-0184`
45. `I-0188`
46. `I-0189`
47. `I-0191`
48. `I-0192`
49. `I-0194`
50. `I-0195`
51. `I-0199`
52. `I-0200`
53. `I-0204`
54. `I-0205`
55. `I-0209`
56. `I-0210`
57. `I-0214`
58. `I-0215`
59. `I-0219`
60. `I-0220`
61. `I-0224`
62. `I-0225`
63. `I-0226`
64. `I-0227`
65. `I-0228`
66. `I-0229`
67. `I-0230`
68. `I-0232`
69. `I-0233`
70. `I-0237`
71. `I-0238`
72. `I-0242`
73. `I-0243`
74. `I-0247`
75. `I-0248`
76. `I-0249`
77. `I-0250`
78. `I-0254`
79. `I-0255`
80. `I-0259`
81. `I-0260`
82. `I-0264`
83. `I-0265`
84. `I-0267`
85. `I-0268`
86. `I-0272`
87. `I-0273`
88. `I-0277`
89. `I-0278`
90. `I-0282`
91. `I-0283`
92. `I-0287`
93. `I-0288`
94. `I-0292`
95. `I-0293`
96. `I-0297`
97. `I-0298`
98. `I-0300`
99. `I-0301`
100. `I-0305`
101. `I-0306`
102. `I-0308`
103. `I-0309`
104. `I-0313`
105. `I-0314`
106. `I-0318`
107. `I-0319`
108. `I-0321`
109. `I-0322`
110. `I-0326`
111. `I-0327`
112. `I-0328`
113. `I-0330`
114. `I-0331`
115. `I-0335`
116. `I-0336`
117. `I-0340`
118. `I-0341`

Active downstream queue from this plan:
1. `I-0344`
2. `I-0345`

Planned next tranche queue:
1. `TBD by next planner slice after M61-S2`

Superseded issues:
- `I-0106` is superseded by `I-0108` + `I-0109` to keep M4 slices independently releasable.
- `I-0133` and `I-0134` are superseded by `I-0135` and `I-0136` for a clean planner-only handoff after prior scope-violation execution.
- `I-0153` and `I-0154` are superseded by `I-0155` and `I-0156` to replace generic cycle placeholders with executable watched-address fan-in reliability slices.
- `I-0158` and `I-0159` are superseded by `I-0160` and `I-0161` to replace generic cycle placeholders with executable lag-aware fan-in cursor continuity reliability slices.
- `I-0163` and `I-0164` are superseded by `I-0165` and `I-0166` to replace generic cycle placeholders with executable dual-chain tick interleaving reliability slices.
- `I-0168` and `I-0169` are superseded by `I-0170` and `I-0171` to replace generic cycle placeholders with executable crash-recovery checkpoint determinism slices.
- `I-0173` and `I-0174` are superseded by `I-0175` and `I-0176` to replace generic cycle placeholders with executable checkpoint-integrity recovery determinism slices.
- `I-0181` and `I-0182` are superseded by `I-0183` and `I-0184` to replace generic cycle placeholders with executable ambiguous-commit determinism slices.
- `I-0186` and `I-0187` are superseded by `I-0188` and `I-0189` to replace generic cycle placeholders with executable batch-partition determinism slices.
- `I-0197` and `I-0198` are superseded by `I-0199` and `I-0200` to replace generic cycle placeholders with executable deferred sidecar-recovery backfill determinism slices.
- `I-0202` and `I-0203` are superseded by `I-0204` and `I-0205` to replace generic cycle placeholders with executable live/backfill overlap canonical convergence determinism slices.
- `I-0207` and `I-0208` are superseded by `I-0209` and `I-0210` to replace generic cycle placeholders with executable decoder-version transition canonical convergence determinism slices.
- `I-0212` and `I-0213` are superseded by `I-0214` and `I-0215` to replace generic cycle placeholders with executable incremental decode-coverage canonical convergence determinism slices.
- `I-0217` and `I-0218` are superseded by `I-0219` and `I-0220` to replace generic cycle placeholders with executable decode-coverage regression flap canonical stability determinism slices.
- `I-0222` and `I-0223` are superseded by `I-0224` and `I-0225` to replace generic cycle placeholders with executable fee-component availability flap canonical convergence determinism slices.
- `I-0235` and `I-0236` are superseded by `I-0237` and `I-0238` to replace generic cycle placeholders with executable tri-chain volatility/interleaving determinism slices.
- `I-0240` and `I-0241` are superseded by `I-0242` and `I-0243` to replace generic cycle placeholders with executable tri-chain late-arrival closure determinism slices.
- `I-0245` and `I-0246` are superseded by `I-0247` and `I-0248` to replace generic cycle placeholders with executable tri-chain volatility-event completeness determinism slices.
- `I-0257` and `I-0258` are superseded by `I-0259` and `I-0260` to replace generic cycle placeholders with executable auto-tune signal-flap hysteresis determinism slices.
- `I-0262` and `I-0263` are superseded by `I-0264` and `I-0265` to replace generic cycle placeholders with executable auto-tune saturation/de-saturation determinism slices.
- `I-0275` and `I-0276` are superseded by `I-0277` and `I-0278` to replace generic cycle placeholders with executable auto-tune policy-version rollout determinism slices.
- `I-0280` and `I-0281` are superseded by `I-0282` and `I-0283` to replace generic cycle placeholders with executable auto-tune policy-manifest refresh determinism slices.
- `I-0285` and `I-0286` are superseded by `I-0287` and `I-0288` to replace generic cycle placeholders with executable auto-tune policy-manifest sequence-gap recovery determinism slices.
- `I-0290` and `I-0291` are superseded by `I-0292` and `I-0293` to replace generic cycle placeholders with executable auto-tune policy-manifest snapshot-cutover determinism slices.
- `I-0295` and `I-0296` are superseded by `I-0297` and `I-0298` to replace generic cycle placeholders with executable auto-tune policy-manifest rollback-lineage determinism slices.
- `I-0303` and `I-0304` are superseded by `I-0305` and `I-0306` to replace generic cycle placeholders with executable auto-tune policy-manifest rollback checkpoint-fence determinism slices.
- `I-0311` and `I-0312` are superseded by `I-0313` and `I-0314` to replace generic cycle placeholders with executable auto-tune policy-manifest rollback checkpoint-fence tombstone-expiry determinism slices.
- `I-0316` and `I-0317` are superseded by `I-0318` and `I-0319` to replace generic cycle placeholders with executable post-expiry late-marker quarantine determinism slices.
- `I-0324` and `I-0325` are superseded by `I-0326` and `I-0327` to replace generic cycle placeholders with executable post-release-window epoch-rollover determinism slices.
