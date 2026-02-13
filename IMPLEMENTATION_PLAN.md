# IMPLEMENTATION_PLAN.md

## Planning Context
- Source planning issue: `#17` (`[Ralph][Planner] Solana devnet + Base Sepolia multi-chain indexing rollout`)
- Objective: roll out dual-chain indexing for `solana-devnet` and `base-sepolia` without regressing current Solana behavior.
- Planning-only run: no production code changes in this milestone plan document.

## Active Milestone (Single Focus)
### M1. Dual-Chain Runtime Bootstrap
- Status: `in-progress` (planning complete, implementation pending)
- Goal: make one indexer runtime capable of loading chain targets from config and starting chain-specific pipeline instances for `solana-devnet` and `base-sepolia`.

### M1 Scope
- Runtime/config design for multi-chain target selection using `INDEXER_TARGET_CHAINS=solana-devnet,base-sepolia`.
- RPC secret/env contract using pre-provisioned secrets:
  - `SOLANA_DEVNET_RPC_URL`
  - `BASE_SEPOLIA_RPC_URL`
- Execution order for implementation slices that are safe for autonomous loop delivery.
- QA/manager loop update plan so QA set generation and validation are chain-aware.

### M1 Out Of Scope
- Mainnet rollout.
- Non-Base EVM networks.
- Performance tuning beyond regression guardrails.
- Production deployment policy changes.

### M1 Acceptance Criteria
- `IMPLEMENTATION_PLAN.md` and `specs/*` define:
  - chain architecture and runtime boundaries,
  - config contract and secret handling rules,
  - milestone execution order and rollback points,
  - risk matrix, assumptions, and unknowns.
- Developer-ready autonomous slices are documented with clear DoD and required validation commands.
- High-impact uncertainty points are captured as explicit `decision/major` placeholders.

### M1 Validation Strategy
- Planning artifact validation:
  - docs are internally consistent with current code layout (`cmd/indexer`, `internal/config`, `internal/chain`, `sidecar`, manager/qa scripts).
- Implementation validation contract for follow-up developer slices:
  - `make test`
  - `make test-sidecar`
  - `make lint`
- Rollout gate:
  - no chain secret values are written to repository files, issue bodies, PR descriptions, or CI logs.

## Milestone Queue (Locked Until M1 Exit)
- M2. Data correctness hardening for dual-chain replay/cursor edge cases.
- M3. Throughput tuning and operational SLO automation.

## Risk Matrix (M1)
| ID | Risk | Impact | Mitigation in M1 |
|---|---|---|---|
| R1 | Single-chain assumptions in config/bootstrap cause startup failure when two chains are enabled | High | Introduce chain-target config contract and phased implementation slices with tests first |
| R2 | Sidecar decode boundary for Base Sepolia is underspecified | High | Decision placeholder + explicit API compatibility plan before implementation starts |
| R3 | Manager/QA loops remain Solana-only, reducing confidence for Base Sepolia rollout | Medium | Add chain-aware QA set schema and validation requirements in rollout spec |
| R4 | RPC secrets leak through logs/issues | High | Keep URL values secret-driven only; document redaction and logging rules in specs |
| R5 | Autonomous slices are too large and unreleasable | Medium | Enforce small-slice sequencing with per-slice DoD and rollback checkpoints |

## Assumptions
- Current DB schema (chain/network keyed tables) can support Base Sepolia without destructive migration.
- Existing autonomous loop governance (`RALPH_LOOP_ENABLED`, `decision/major` guard) remains unchanged.
- Repo secrets for both RPC endpoints are already configured and accessible to GitHub Actions runner.

## Unknowns
- Final runtime topology preference for dual-chain execution (single process with multi-pipeline vs one process per chain).
- Sidecar RPC strategy for Base decoding (chain-specific RPC method vs generic method contract).
- Source of truth format for Base Sepolia QA address sets in manager loop.

## Decision Placeholders For Escalation (`decision/major`)
1. `DP-17-01` Runtime topology for dual-chain execution.
2. `DP-17-02` Sidecar decode API shape for Base Sepolia.
3. `DP-17-03` Manager/QA whitelist source contract for non-Solana chains.

Detailed decision templates and recommendation/tradeoff content are recorded in:
- `specs/multichain-rollout-rollout.md`
- `specs/multichain-rollout-overview.md`
