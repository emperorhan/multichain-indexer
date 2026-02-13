# Multi-Chain Rollout Overview (Issue #17)

## Objective
Roll out indexing for:
- `solana-devnet`
- `base-sepolia`

while preserving current Solana behavior and keeping RPC endpoints secret-driven.

## Current Baseline Constraints
- `cmd/indexer/main.go` bootstraps one Solana adapter/pipeline only.
- `internal/config/config.go` currently models Solana-only RPC/network config.
- `proto/sidecar/v1/decoder.proto` exposes Solana-specific decode RPC only.
- Manager/QA scripts currently assume Solana address format and Solana whitelist source.

## Target Architecture (M1 Design Contract)
1. Runtime target model:
   - `INDEXER_TARGET_CHAINS=solana-devnet,base-sepolia`
   - each target maps to one adapter + one pipeline instance.
2. Bootstrap behavior:
   - for each configured target, initialize chain-specific adapter, watched-address sync, and pipeline config.
   - pipeline lifecycle is managed under one process-level context and health server.
3. Isolation:
   - chain failures should surface clearly with chain/network labels.
   - startup validation must fail fast when required target config is missing.

## Config Contract (Planned)
- Required for this rollout:
  - `INDEXER_TARGET_CHAINS`
  - `SOLANA_DEVNET_RPC_URL`
  - `BASE_SEPOLIA_RPC_URL`
- Existing compatibility:
  - keep current Solana env behavior backward-compatible during transition where feasible.
- Secret handling rule:
  - endpoint values are loaded from env/secrets only and must not be printed in clear text in logs/issues/PRs.

## Runtime Behavior Requirements
- Chain/network pairs remain explicit keys in DB writes and cursor updates.
- Watchlist syncing must become chain-aware (not hardcoded to `ChainSolana`).
- Sidecar decode path for Base Sepolia requires an approved API plan before implementation.

## Assumptions
- Existing `(chain, network)` schema constraints are sufficient for Base Sepolia onboarding.
- Dual-chain rollout can be completed without destructive migration.

## Unknowns
- Whether Base address sourcing for manager/QA should use a dedicated var/file pair.
- Whether sidecar should add Base-specific RPC methods or a generic decode contract.

## Decision Placeholder: DP-17-01 (`decision/major`)
- Topic: Runtime topology for dual-chain execution.
- Option A (recommended): single process, multi-pipeline bootstrap from `INDEXER_TARGET_CHAINS`.
- Option B: separate runtime process per chain.
- Why this is major:
  - affects failure domain, operational runbook complexity, and release strategy.
