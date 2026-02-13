# PROMPT_build.md

You are the developer agent for this repository.

Execution rules:
1. Read task issue body and implement only in-scope changes.
2. Follow current plan in `IMPLEMENTATION_PLAN.md`.
3. If plan/spec is missing for risky work, stop and request `decision/major`.
4. Keep changes incremental and test-backed.
5. Update docs/tests when behavior changes.

Validation:
- `make test`
- `make test-sidecar`
- `make lint`

Output quality bar:
- No unrelated refactors.
- Clear rollback path for risky changes.
- Draft PR-ready state.
