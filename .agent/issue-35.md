### Objective
Investigate and fix the recurring CI failure so autonomous delivery remains healthy.

### Discovery
- source: automated issue scout (recent failed GitHub Actions run)
- workflow: `Agent Auto Merge`
- run: https://github.com/emperorhan/multichain-indexer/actions/runs/21997348183
- branch: `feat/ralph-self-heal-hardening`
- event: `push`
- commit: `a4c6db3b18d3b01ddcb33b3f5e64b5d8f5c59b36`
- failed within last: 24h

### In Scope
- reproduce the failure
- fix root cause in code/workflow/config
- add guardrails/tests where practical

### Out of Scope
- unrelated feature work

### Risk Level
medium

### Acceptance Criteria
- [ ] root cause identified and documented in PR
- [ ] failing check turns green
- [ ] regression prevention added when possible

[agent-scout:ci-failure-21997348183]
