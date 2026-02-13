### Objective
Investigate and fix the recurring CI failure so autonomous delivery remains healthy.

### Discovery
- source: automated issue scout (recent failed GitHub Actions run)
- workflow: `Agent Loop`
- run: https://github.com/emperorhan/multichain-indexer/actions/runs/21992010404
- branch: `main`
- event: `workflow_dispatch`
- commit: `861483c45ab99f144b3e3d7d45ea6313a176fdb2`
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

[agent-scout:ci-failure-21992010404]
