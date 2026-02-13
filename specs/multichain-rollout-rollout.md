# Multi-Chain Rollout Sequence (Issue #17)

## Milestone Strategy
One focused milestone is active: `M1. Dual-Chain Runtime Bootstrap`.

Implementation must proceed in small autonomous slices in this order.

## Slice Plan (Developer-Ready Backlog)
1. Slice S1: Multi-target config scaffolding
- Scope:
  - define target-chain env contract (`INDEXER_TARGET_CHAINS`, per-chain RPC env keys).
  - keep backward compatibility for current Solana-only startup path.
- DoD:
  - config unit tests cover valid/invalid target sets and missing RPC envs.
  - no runtime behavior change yet beyond parsing/validation.

2. Slice S2: Runtime bootstrap fan-out
- Scope:
  - `cmd/indexer/main.go` startup wiring to instantiate per-target adapter + pipeline.
  - watched-address sync path becomes chain-aware.
- DoD:
  - startup tests confirm both targets initialize.
  - Solana-only mode remains functional.

3. Slice S3: Base Sepolia adapter + decode integration
- Scope:
  - add Base chain adapter implementation and sidecar decode path based on approved decision.
- DoD:
  - adapter tests + decode integration tests for Base transactions.
  - no regression in existing Solana decode tests.

4. Slice S4: Manager/QA chain-aware validation loop
- Scope:
  - manager set generation supports chain/network tagging.
  - QA executor validates addresses by chain type instead of Solana-only regex.
- DoD:
  - QA issues can carry either Solana or Base address sets.
  - failing validations produce developer bug issues with clear repro context.

5. Slice S5: Documentation + rollout guardrails
- Scope:
  - update README/docs with multi-chain env and operational runbook deltas.
  - finalize release guard checks for dual-chain startup.
- DoD:
  - docs reflect exact env contract and rollback behavior.
  - required validation commands pass.

## Risk Controls During Rollout
- Do not merge a slice that changes behavior for both chains at once without tests.
- Keep each PR small enough to be reverted independently.
- Block slices touching major unknowns until owner input is captured via `decision/major`.

## Decision Placeholders (Escalation Templates)
1. `DP-17-01` Runtime topology (single process multi-pipeline vs per-chain process).
- Suggested issue title:
  - `[Major Decision] Runtime topology for solana-devnet + base-sepolia indexer`
- Recommended deadline:
  - `2026-02-20 18:00 UTC`

2. `DP-17-02` Sidecar API contract for Base decoding.
- Suggested issue title:
  - `[Major Decision] Sidecar decode API contract for Base Sepolia`
- Recommended deadline:
  - `2026-02-20 18:00 UTC`

3. `DP-17-03` Manager/QA source-of-truth for Base address sets.
- Suggested issue title:
  - `[Major Decision] Base Sepolia whitelist source for manager/qa loops`
- Recommended deadline:
  - `2026-02-21 18:00 UTC`

## Escalation Rule
If any `DP-17-*` issue remains unresolved, affected slices must not be labeled `ready` for autonomous execution.
