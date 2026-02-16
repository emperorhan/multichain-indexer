# M95 PRD-Priority Chain-Scoped Throughput Control Isolation Gate

## Scope
- Milestone: `M95`
- Execution slices: `M95-S1` (`I-0501`), `M95-S2` (`I-0502`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R9`: chain-scoped adaptive throughput control.
- `9.4`: topology parity/continuity validation principles.
- `10`: deterministic replay and no cross-chain cursor bleed acceptance criteria.

## Problem Statement
PRD `R9` requires control-plane safety such that throughput-control behavior for one chain does not alter another chain’s cursor, watermark, or checkpoint progression.
After PRD core gates `M91`-`M94`, the remaining hardening is an explicit chain-scoped control contract and counterexample matrix that proves no control bleed across mandatory chains.

## Reliability Contract
1. Auto-tune/control outputs for one chain are driven by chain-local metrics only.
2. One-chain control perturbation while peers progress produces `0` cross-chain control bleed and `0` cross-chain cursor/watermark bleed in deterministic counterexamples.
3. Fail-fast correctness remains unchanged under control perturbation (no failed-path cursor/watermark advancement).
4. Chain-scoped control behavior is represented with deterministic evidence artifacts under `.ralph/reports/`.

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `chain_adapter_runtime_wired`

## Measurable Exit Gates
1. `0` cross-chain control bleed findings in control perturbation matrices for mandatory chains.
2. `0` failed-path cursor/watermark advancement findings caused by control perturbation in the same evidence family.
3. `0` regressions on `canonical_event_id_unique`, `replay_idempotent`, `cursor_monotonic`, `signed_delta_conservation`, `chain_adapter_runtime_wired`.
4. Validation passes: `make test`, `make test-sidecar`, `make lint`.

## Test Matrix
1. Control scope matrix:
- for each mandatory chain, prove control/auto-tune decision inputs and outputs use chain-local telemetry and control paths only.
2. Cross-coupling matrix:
- one-chain control perturbation in chain-local telemetry under peer-chain activity.
- assert no peer-chain cursor/watermark path changes attributable to foreign control inputs.
3. Continuity matrix:
- replay from control-perturbed committed boundaries with invariant assertions on canonical IDs and replay outputs.

## Decision Hook
- `DP-0106-M95`: control telemetry and control outputs are chain-local for mandatory chains; any measurable cross-chain control coupling is a hard gate failure until remediated.
