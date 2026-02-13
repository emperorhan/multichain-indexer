# Canonical Normalizer Rollout Sequence (I-0101)

## Execution Order
1. `I-0102` (`M1-S1`): canonical envelope/schema scaffolding.
2. `I-0103` (`M1-S2/S3`): deterministic `event_id` + idempotent ingestion.
3. `I-0104` (`M2`): Solana dedup + fee completeness.
4. `I-0105` (`M3`): Base fee decomposition.
5. `I-0108` (`M4-S1`): reorg detection + rollback orchestration.
6. `I-0109` (`M4-S2`): replay determinism + cursor monotonicity.
7. `I-0107` (`M5`): QA goldens + invariants + release recommendation.

Dependency graph:
`I-0102 -> I-0103 -> (I-0104 || I-0105) -> I-0108 -> I-0109 -> I-0107`

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

## Fallback Paths
1. If canonical key migration is risky, keep temporary dual unique protections.
2. If Base L1 data fee fields are unavailable, emit deterministic execution fee plus explicit unavailable marker metadata.
3. If reorg path is unstable, run finalized-only ingest mode until rollback tests pass.

## Completion Evidence
1. Developer slice output:
- code + tests + docs updated in same issue scope.
- measurable exit gate evidence attached in issue note.

2. QA slice output:
- `.ralph/reports/` artifact with per-invariant pass/fail and release recommendation.
- follow-up developer issues created for each failing invariant.
