# Canonical Normalizer Rollout Sequence (I-0101)

## Execution Order
1. `I-0102` (`M1-S1`): canonical envelope/schema scaffolding.
2. `I-0103` (`M1-S2/S3`): deterministic `event_id` + idempotent ingestion.
3. `I-0104` (`M2`): Solana dedup + fee completeness.
4. `I-0105` (`M3`): Base fee decomposition.
5. `I-0108` (`M4-S1`): reorg detection + rollback orchestration.
6. `I-0109` (`M4-S2`): replay determinism + cursor monotonicity.
7. `I-0107` (`M5`): QA goldens + invariants + release recommendation.
8. `I-0110` (`M6`): Base runtime pipeline wiring.
9. `I-0114` (`M7-S1`): runtime wiring drift guard + dual-chain replay smoke.
10. `I-0115` (`M7-S2`): QA counterexample gate for runtime wiring/replay reliability.
11. `I-0117` (`M8-S1`): failed-transaction fee completeness + replay safety hardening.
12. `I-0118` (`M8-S2`): QA counterexample gate for failed-transaction fee coverage.
13. `I-0122` (`M9-S1`): mandatory-chain adapter RPC contract parity hardening + deterministic drift guard.
14. `I-0123` (`M9-S2`): QA counterexample gate for adapter contract drift + runtime/replay invariants.
15. `I-0127` (`M10-S1`): transient-failure recovery hardening + deterministic retry-resume guard.
16. `I-0128` (`M10-S2`): QA counterexample gate for transient-failure recovery + duplicate/cursor invariants.
17. `I-0130` (`M11-S1`): deterministic retry-boundary hardening (transient vs terminal) across fetch/normalize/ingest.
18. `I-0131` (`M11-S2`): QA counterexample gate for retry-boundary classification + invariant safety.
19. `I-0135` (`M12-S1`): deterministic decode-error isolation + suffix continuity hardening across mandatory-chain runtime paths.
20. `I-0136` (`M12-S2`): QA counterexample gate for decode-error isolation + invariant safety.

Dependency graph:
`I-0102 -> I-0103 -> (I-0104 || I-0105) -> I-0108 -> I-0109 -> I-0107 -> I-0110 -> I-0114 -> I-0115 -> I-0117 -> I-0118 -> I-0122 -> I-0123 -> I-0127 -> I-0128 -> I-0130 -> I-0131 -> I-0135 -> I-0136`

## Slice Size Rule
Each slice must be independently releasable:
1. One milestone sub-goal only.
2. Bounded code surface with no cross-milestone coupling.
3. Tests fail before and pass after the slice.
4. Revert of slice does not undo unrelated milestone progress.

## Risk Gates By Stage
1. Before `I-0103`: canonical key schema strategy reviewed, migration fallback documented.
2. Before `I-0104`/`I-0105`: `I-0102` + `I-0103` acceptance green.
3. Before `I-0108`: Solana and Base completeness slices green.
4. Before `I-0109`: rollback simulation evidence from `I-0108` green.
5. Before `I-0107`: replay determinism and cursor monotonicity evidence green.
6. Before `I-0110`: QA release gate from `I-0107` passes.
7. Before `I-0114`: runtime wiring baseline from `I-0110` passes on both mandatory chains.
8. Before `I-0115`: `I-0114` emits deterministic replay/wiring evidence and no unresolved blockers.
9. Before `I-0117`: `I-0115` QA report is `PASS` with no unresolved blocker invariants.
10. Before `I-0118`: `I-0117` emits deterministic failed-transaction fee coverage evidence for both mandatory chains.
11. Before `I-0122`: `I-0118` QA report is `PASS` and no unresolved failed-fee coverage blocker remains.
12. Before `I-0123`: `I-0122` emits deterministic adapter RPC contract parity evidence for both mandatory chains.
13. Before `I-0127`: `I-0123` QA report is `PASS` and no unresolved adapter contract drift blocker remains.
14. Before `I-0128`: `I-0127` emits deterministic fail-first/retry recovery evidence for both mandatory chains.
15. Before `I-0130`: `I-0128` QA report is `PASS` and no unresolved transient-failure recovery blocker remains.
16. Before `I-0131`: `I-0130` emits deterministic evidence that terminal failures fail-fast (no retry) and transient failures recover with bounded retries.
17. Before `I-0135`: `I-0131` QA report is `PASS` and no unresolved retry-boundary blocker remains.
18. Before `I-0136`: `I-0135` emits deterministic evidence that single-signature decode failures do not block decodable suffix signatures.

## Fallback Paths
1. If canonical key migration is risky, keep temporary dual unique protections.
2. If Base L1 data fee fields are unavailable, emit deterministic execution fee plus explicit unavailable marker metadata.
3. If reorg path is unstable, run finalized-only ingest mode until rollback tests pass.
4. If strict runtime wiring preflight is operationally disruptive, keep strict checks in tests/CI and use explicit local override with warning + QA follow-up.
5. If failed-transaction fee metadata is missing from provider responses, preserve deterministic no-op behavior and record explicit unavailable markers for follow-up.
6. If mandatory-chain RPC contract parity checks expose broad legacy fake-client drift, enforce parity on mandatory runtime paths first and file bounded follow-up issues for remaining adapters.
7. If transient failure retry hardening blurs permanent-error boundaries, keep bounded retries with explicit `transient_recovery_exhausted` diagnostics and require QA follow-up issue fanout.
8. If retry-boundary classification is ambiguous for a provider error class, default to terminal handling until deterministic retryability tests and QA counterexample evidence are added.
9. If decode-error isolation risks masking broad sidecar outages, keep deterministic full-batch-collapse fail-fast guardrails while allowing bounded per-signature isolation for partial decode failures.

## Completion Evidence
1. Developer slice output:
- code + tests + docs updated in same issue scope.
- measurable exit gate evidence attached in issue note.

2. QA slice output:
- `.ralph/reports/` artifact with per-invariant pass/fail and release recommendation.
- follow-up developer issues created for each failing invariant.
