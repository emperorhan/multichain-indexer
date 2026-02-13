### Objective
Investigate and fix the recurring CI failure so autonomous delivery remains healthy.

### Discovery
- source: automated issue scout (recent failed GitHub Actions run)
- workflow: `Sync Labels`
- run: https://github.com/emperorhan/multichain-indexer/actions/runs/21985994090
- branch: `main`
- event: `workflow_dispatch`
- commit: `f29d9befd677b36b5f5cb6175cc717c64172ba53`
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

[agent-scout:ci-failure-21985994090]
