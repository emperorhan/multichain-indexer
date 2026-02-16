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

## Control Coupling Contract
- Cross-chain control coupling is a **control/cursor bleed**: any control output decision for chain `X` that consumes telemetry from a chain `Y != X` or writes to chain `Y` cursor/watermark controls.
- Hard constraints:
  - Auto-tune for one chain consumes only chain-local telemetry inputs and local override/policy inputs.
  - Auto-tune emits only local knob updates (`batch`, `tick_interval`, `concurrency`) for that same chain runtime.
  - A control perturbation in one chain must not change any peer-chain watermark or cursor path in a deterministic assertion.
- Cross-chain control coupling is only tolerated if the value is `NO-OP` and never changes routing, commit order, cursor, or watermark of peers.

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

## Deterministic Control Metric Inventory
- For every `solana-devnet`, `base-sepolia`, and `btc-testnet`, producers of auto-tune signals must expose:
  - `chain_lag` (chain-local lag or head-distance)
  - `queue_depth` / `processing_depth`
  - `queue_capacity`
  - `rpc_error_ratio` and `rpc_error_budget`
  - `commit_latency_ms_p95`
  - `failed_batch_count`
  - `safe_max_batch` (effective hard cap)
- Forbidden signal classes:
  - any metric containing peer chain identity values
  - any global scheduler aggregate used to compute per-chain decisions

Required artifact schema: one row per control cycle, keyed by `(chain, network, cycle_seq, decision_epoch_ms)`, including:
`local_inputs_digest`, `decision_inputs_hash`, `decision_inputs_chain_scoped`, `decision_outputs`, `decision_scope`, `cross_chain_reads`, `cross_chain_writes`, `changed_peer_cursor`, `changed_peer_watermark`.

## Test Matrix
1. Control scope matrix:
- for each mandatory chain, prove control/auto-tune decision inputs and outputs use chain-local telemetry and control paths only.
- record whether each decision cycle has:
  - `cross_chain_reads=false`
  - `cross_chain_writes=false`
  - `decision_scope=<this-chain-only>`
- required evidence file: `.ralph/reports/I-0501-m95-s1-control-scope-metric-matrix.md`
2. Cross-coupling matrix:
- one-chain control perturbation in chain-local telemetry under peer-chain activity.
- each case asserts no peer-chain cursor/watermark path changes attributable to foreign control inputs.
- required evidence file: `.ralph/reports/I-0501-m95-s1-cross-coupling-matrix.md`
3. Continuity matrix:
- replay from control-perturbed committed boundaries with invariant assertions on canonical IDs and replay outputs.
- required evidence file: `.ralph/reports/I-0501-m95-s1-control-perturbation-continuity-matrix.md`
4. Regression matrix:
- for each mandatory chain, include one-chain perturbation vectors that force:
  - decision branch flip (high/low lag)
  - telemetry glitch burst
  - override conflict
  - stale/fresh control window transitions
- each vector must be deterministic and re-runnable from fixtures.

## GO/NO-GO Acceptance Logic
- `GO` requires:
  - all control matrix rows have `cross_chain_reads=false` and `cross_chain_writes=false`
  - all perturbation rows have peer cursor delta = 0 and peer watermark delta = 0
  - all continuity rows pass invariants in matrix
- `NO-GO` if any of the above checks fail or if any evidence artifact is missing.

## Decision Hook
- `DP-0106-M95`: control telemetry and control outputs are chain-local for mandatory chains; any measurable cross-chain control coupling is a hard gate failure until remediated.
