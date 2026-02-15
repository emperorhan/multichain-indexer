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
21. `I-0138` (`M13-S1`): deterministic fetch-order canonicalization + overlap duplicate suppression hardening across mandatory-chain runtime paths.
22. `I-0139` (`M13-S2`): QA counterexample gate for fetch-order/overlap dedupe determinism + invariant safety.
23. `I-0141` (`M14-S1`): canonical identity alias normalization + duplicate-suppression boundary hardening across mandatory-chain runtime paths.
24. `I-0142` (`M14-S2`): QA counterexample gate for canonical identity alias determinism + invariant safety.
25. `I-0144` (`M15-S1`): finality-transition canonical dedupe/update hardening across mandatory-chain runtime paths.
26. `I-0145` (`M15-S2`): QA counterexample gate for finality-transition determinism + invariant safety.
27. `I-0147` (`M16-S1`): rollback/finality convergence dedupe hardening across mandatory-chain runtime recovery paths.
28. `I-0148` (`M16-S2`): QA counterexample gate for rollback/finality convergence determinism + invariant safety.
29. `I-0150` (`M17-S1`): cursor-boundary canonical continuity hardening across mandatory-chain runtime replay/resume paths.
30. `I-0151` (`M17-S2`): QA counterexample gate for cursor-boundary determinism + invariant safety.
31. `I-0155` (`M18-S1`): watched-address fan-in canonical dedupe hardening across mandatory-chain runtime paths.
32. `I-0156` (`M18-S2`): QA counterexample gate for watched-address fan-in determinism + invariant safety.
33. `I-0160` (`M19-S1`): lag-aware watched-address fan-in cursor continuity hardening across mandatory-chain runtime paths.
34. `I-0161` (`M19-S2`): QA counterexample gate for lag-aware fan-in cursor continuity + invariant safety.
35. `I-0165` (`M20-S1`): dual-chain tick interleaving determinism hardening across mandatory-chain runtime paths.
36. `I-0166` (`M20-S2`): QA counterexample gate for dual-chain interleaving determinism + invariant safety.
37. `I-0170` (`M21-S1`): tick checkpoint crash-recovery determinism hardening across mandatory-chain runtime paths.
38. `I-0171` (`M21-S2`): QA counterexample gate for crash-point permutation determinism + invariant safety.
39. `I-0175` (`M22-S1`): checkpoint integrity self-healing determinism hardening across mandatory-chain restart/resume paths.
40. `I-0176` (`M22-S2`): QA counterexample gate for checkpoint-integrity recovery determinism + invariant safety.

Dependency graph:
`I-0102 -> I-0103 -> (I-0104 || I-0105) -> I-0108 -> I-0109 -> I-0107 -> I-0110 -> I-0114 -> I-0115 -> I-0117 -> I-0118 -> I-0122 -> I-0123 -> I-0127 -> I-0128 -> I-0130 -> I-0131 -> I-0135 -> I-0136 -> I-0138 -> I-0139 -> I-0141 -> I-0142 -> I-0144 -> I-0145 -> I-0147 -> I-0148 -> I-0150 -> I-0151 -> I-0155 -> I-0156 -> I-0160 -> I-0161 -> I-0165 -> I-0166 -> I-0170 -> I-0171 -> I-0175 -> I-0176`

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
19. Before `I-0138`: `I-0136` QA report is `PASS` and no unresolved decode-error isolation blocker remains.
20. Before `I-0139`: `I-0138` emits deterministic evidence that order-permuted and overlap-duplicated fetch inputs converge to identical canonical tuple outputs with zero duplicate canonical IDs.
21. Before `I-0141`: `I-0139` QA report is `PASS` and no unresolved fetch-order/overlap dedupe blocker remains.
22. Before `I-0142`: `I-0141` emits deterministic evidence that canonical identity alias variants converge to one canonical tuple output set with zero duplicate canonical IDs.
23. Before `I-0144`: `I-0142` QA report is `PASS` and no unresolved canonical identity alias determinism blocker remains.
24. Before `I-0145`: `I-0144` emits deterministic evidence that mixed-finality replay-equivalent inputs converge to one canonical identity set with zero duplicate canonical IDs and no balance double-apply side effects.
25. Before `I-0147`: `I-0145` QA report is `PASS` and no unresolved finality-transition determinism blocker remains.
26. Before `I-0148`: `I-0147` emits deterministic evidence that rollback-invalidated finality-promoted events do not survive as stale canonical IDs and do not induce balance double-apply side effects.
27. Before `I-0150`: `I-0148` QA report is `PASS` and no unresolved rollback/finality convergence blocker remains.
28. Before `I-0151`: `I-0150` emits deterministic evidence that boundary-overlap and restart-from-boundary inputs converge to one canonical output set with zero duplicate canonical IDs and zero missing boundary events.
29. Before `I-0155`: `I-0151` QA report is `PASS` and no unresolved cursor-boundary continuity blocker remains.
30. Before `I-0156`: `I-0155` emits deterministic evidence that overlapping watched-address discovery paths converge to one canonical output set with zero duplicate canonical IDs and zero missing logical events.
31. Before `I-0160`: `I-0156` QA report is `PASS` and no unresolved watched-address fan-in determinism blocker remains.
32. Before `I-0161`: `I-0160` emits deterministic evidence that divergent-cursor and fan-in-membership-churn inputs converge to one canonical output set with zero duplicate and zero missing logical events.
33. Before `I-0165`: `I-0161` QA report is `PASS` and no unresolved lag-aware fan-in cursor continuity blocker remains.
34. Before `I-0166`: `I-0165` emits deterministic evidence that dual-chain completion-order permutations converge to one canonical output set with zero tuple diffs and no cross-chain cursor bleed.
35. Before `I-0170`: `I-0166` QA report is `PASS` and no unresolved dual-chain interleaving determinism blocker remains.
36. Before `I-0171`: `I-0170` emits deterministic evidence that crash cutpoint permutations converge with zero duplicate/missing logical events and no cross-chain cursor bleed.
37. Before `I-0175`: `I-0171` QA report is `PASS` and no unresolved crash-recovery checkpoint determinism blocker remains.
38. Before `I-0176`: `I-0175` emits deterministic evidence that checkpoint corruption/integrity recovery permutations converge with zero duplicate/missing logical events and chain-scoped cursor monotonicity.

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
10. If overlap dedupe boundaries are ambiguous, use conservative dedupe keying and deterministic collision diagnostics, then fail fast for unresolved ambiguity instead of silently dropping records.
11. If identity alias normalization introduces ambiguous collisions, keep chain-scoped conservative normalization, emit deterministic alias-collision diagnostics, and fail fast until alias contracts are explicitly extended.
12. If finality-transition unification boundaries are ambiguous, keep chain-scoped conservative transition handling, emit deterministic transition-collision diagnostics, and fail fast until explicit lifecycle contracts are extended.
13. If rollback reconciliation boundaries are ambiguous after finality promotion, keep reconciliation fork-range scoped, emit deterministic rollback-collision diagnostics, and fail fast until ancestry contracts are explicitly extended.
14. If cursor-boundary continuity semantics are ambiguous across provider paging behavior, keep boundary handling conservative and deterministic, emit boundary-collision diagnostics, and fail fast until boundary identity contracts are explicitly extended.
15. If watched-address fan-in dedupe boundaries are ambiguous, keep fan-in identity keying conservative (`chain + canonical identity + actor + asset_id + event_path`), emit deterministic fan-in-collision diagnostics, and fail fast until fan-in contracts are explicitly extended.
16. If lag-aware fan-in cursor reconciliation widens replay windows excessively, keep deterministic bounded replay-window guardrails, emit explicit lag-merge diagnostics, and fail fast on ambiguous lag-range overlaps until lag-window contracts are explicitly extended.
17. If deterministic dual-chain interleaving barriers add unacceptable latency under asymmetric chain delay, keep chain-scoped ordering with bounded skew guardrails, emit explicit interleaving-skew diagnostics, and fail fast on ambiguous commit ordering until interleaving contracts are explicitly extended.
18. If crash-safe checkpoint ordering increases restart/tick latency under repeated failures, keep deterministic replay-from-last-safe-checkpoint semantics, emit explicit crash-cutpoint diagnostics, and fail fast on ambiguous resume boundaries until checkpoint contracts are explicitly extended.
19. If checkpoint-integrity validation and repair path increases restart cost or reveals ambiguous corrupted-state boundaries, keep deterministic fail-fast plus replay-from-last-known-good checkpoint semantics with explicit checkpoint-integrity diagnostics until repair granularity is explicitly extended.

## Completion Evidence
1. Developer slice output:
- code + tests + docs updated in same issue scope.
- measurable exit gate evidence attached in issue note.

2. QA slice output:
- `.ralph/reports/` artifact with per-invariant pass/fail and release recommendation.
- follow-up developer issues created for each failing invariant.
