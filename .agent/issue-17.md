### Objective
Design and execute a phased Ralph-loop implementation to support **two live chains**:
- Solana devnet
- Base Sepolia

### Inputs (already injected as repo secrets)
- `SOLANA_DEVNET_RPC_URL`
- `BASE_SEPOLIA_RPC_URL`

### Scope
1. Planner agent must produce implementation artifacts first:
   - update `IMPLEMENTATION_PLAN.md`
   - add/refresh `specs/*` for multi-chain architecture, config, runtime, and test strategy
2. Then generate developer-ready autonomous issues for iterative implementation PRs.
3. Ensure each PR is releasable in small increments (semver automation already enabled).
4. Keep owner-decision points explicit:
   - if major architecture tradeoff appears, create/route to `decision/major` and pause that path.

### Constraints
- Do not expose RPC secrets in logs, issues, PR descriptions, or committed files.
- Use env/secret-driven config only.
- Preserve existing Solana behavior while adding Base Sepolia.

### Acceptance Criteria
- Planner outputs concrete phased plan with milestones and risk matrix.
- Follow-up autonomous implementation issues are created with clear DoD and tests.
- First implementation slice should be executable by agent loop without human terminal intervention.

### Notes
- Target chains variable: `INDEXER_TARGET_CHAINS=solana-devnet,base-sepolia`
