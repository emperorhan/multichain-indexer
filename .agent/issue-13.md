### Objective
Investigate and fix the recurring CI failure so autonomous delivery remains healthy.

### Discovery
- source: automated issue scout (recent failed GitHub Actions run)
- workflow: `Agent Loop`
- run: https://github.com/emperorhan/multichain-indexer/actions/runs/21992657071
- branch: `main`
- event: `workflow_dispatch`
- commit: `5c3ccac8bf82f5e8d436be67b87f53f01bb1e3fa`
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

[agent-scout:ci-failure-21992657071]
