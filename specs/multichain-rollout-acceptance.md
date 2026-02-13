# Multi-Chain Acceptance And Validation (Issue #17)

## Milestone In Scope
`M1. Dual-Chain Runtime Bootstrap`

## Acceptance Criteria (M1)
1. Planner artifacts are complete:
   - `IMPLEMENTATION_PLAN.md` updated
   - `specs/multichain-rollout-overview.md` updated
   - `specs/multichain-rollout-acceptance.md` updated
   - `specs/multichain-rollout-rollout.md` updated
2. Developer implementation slices are defined with small releasable scope and explicit DoD.
3. Config and runtime plan explicitly supports `solana-devnet` + `base-sepolia`.
4. Major unknowns are documented as `decision/major` placeholders.

## Implementation Validation Contract (for Developer Slices)
Every implementation PR under this milestone must pass:
1. `make test`
2. `make test-sidecar`
3. `make lint`

Additional expected checks per slice:
1. config parsing tests for new target-chain env contract.
2. chain bootstrap tests for multi-target startup sequencing.
3. sidecar decode compatibility tests for the selected Base strategy.
4. manager/qa script tests for chain-aware QA input parsing.

## Definition Of Done Per Slice
1. Scope-limited and releasable independently.
2. Includes tests for new behavior and regression guard for existing Solana behavior.
3. Updates docs (`README.md` or `docs/*`) when behavior/config changes.
4. Does not expose secret values in logs or committed artifacts.

## Non-Goals In M1 Validation
- No performance/SLO tuning beyond regression prevention.
- No production deployment automation changes.
- No mainnet chain enablement.

## Decision Placeholder: DP-17-02 (`decision/major`)
- Topic: Base Sepolia decode API boundary in sidecar.
- Decision required before implementation slices touching `proto/sidecar/v1/decoder.proto` are started.
