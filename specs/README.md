# specs/

Planner-owned technical specs for local Ralph execution.

Current planning set for `I-0101` (`PRD.md v2.0`, 2026-02-13):
- `specs/multichain-rollout-overview.md`
- `specs/multichain-rollout-acceptance.md`
- `specs/multichain-rollout-rollout.md`

Execution rules:
1. Keep scope executable and dependency-ordered.
2. Keep acceptance criteria measurable and test-linked.
3. Make major decisions explicit with fallback options.
4. Keep developer and QA slices independently releasable.

Program graph:
`M1 -> (M2 || M3) -> M4 -> M5`

Local queue mapping:
`I-0102 -> I-0103 -> (I-0104 || I-0105) -> I-0108 -> I-0109 -> I-0107`
