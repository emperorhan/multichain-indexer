# M95 PRD-Priority Chain-Scoped Throughput Control Isolation Gate

## Scope
- Milestone: `M95`
- Execution slices: `M95-S1` (`I-0501`), `M95-S2` (`I-0502`), `M95-S3` (`I-0507`), `M95-S4` (`I-0508`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R9`: chain-scoped adaptive throughput control.
- `9.4`: topology parity/continuity validation principles.
- `10`: deterministic replay and no cross-chain cursor bleed acceptance criteria.

## Problem Statement
PRD `R9` requires control-plane safety such that throughput-control behavior for one chain does not alter another chain’s cursor, watermark, or checkpoint progression.
After PRD core gates `M91`-`M94`, the remaining hardening is an explicit chain-scoped control contract and counterexample matrix that proves no control bleed across mandatory chains.

## Control Coupling Contract
- Cross-chain control coupling is defined as **control/cursor bleed** if any control cycle for chain `X` satisfies one or more:
  - `cross_chain_reads=true` when telemetry inputs include any chain `Y != X`.
  - `cross_chain_writes=true` when emitted control payload writes knobs for chain `Y != X`.
  - `peer_cursor_delta != 0` for any `Y != X`.
  - `peer_watermark_delta != 0` for any `Y != X`.
- Acceptance rule:
  - `cross_chain_reads` and `cross_chain_writes` must be strictly false for all decision cycles.
  - For all chains, all peer deltas must be zero for all `M95` slices.
- Hard constraints:
  - Auto-tune for one chain consumes only chain-local telemetry inputs and local override/policy inputs.
  - Auto-tune emits only local knob updates (`batch`, `tick_interval`, `concurrency`) for that same chain runtime.
  - A one-chain control perturbation must not change any peer-chain watermark or cursor path, even if that peer’s throughput profile differs.

## Reliability Contract
1. Auto-tune/control outputs for one chain are driven by chain-local metrics only.
2. One-chain control perturbation while peers progress produces `0` cross-chain control bleed and `0` cross-chain cursor/watermark bleed in deterministic counterexamples.
3. Fail-fast correctness remains unchanged under control perturbation (no failed-path cursor/watermark advancement).
4. Chain-scoped control behavior is represented with deterministic evidence artifacts under `.ralph/reports/`.
5. A counterexample outcome is `NO-GO` if any decision row has:
   - `cross_chain_reads=true` or `cross_chain_writes=true`
   - `peer_cursor_delta != 0` or `peer_watermark_delta != 0`
   - missing required row schema.

## Reproducibility Contract (`M95-S3`)
- `I-0507` requires replayable control-coupling counterexamples with fixed fixture metadata.
- Required matrix files:
  - `.ralph/reports/I-0507-m95-s3-control-coupling-reproducibility-matrix.md`
  - `.ralph/reports/I-0507-m95-s3-replay-continuity-matrix.md`
  - `.ralph/reports/I-0508-m95-s4-qa-repro-gate-matrix.md`
- Control-coupling reproducibility row keys (re-runnable from fixed `fixture_seed` + `run_id`):
  - `fixture_id` (string)
  - `fixture_seed` (string)
  - `run_id` (string)
  - `chain` (string)
  - `network` (string)
  - `test_id` (string)
  - `perturbation` (string)
  - `peer_chain` (string)
  - `cross_chain_reads` (boolean)
  - `cross_chain_writes` (boolean)
  - `peer_cursor_delta` (integer)
  - `peer_watermark_delta` (integer)
  - `outcome` (enum: `GO`/`NO-GO`)
  - `evidence_present` (boolean)
  - `failure_mode` (string, required when `outcome=NO-GO`; blank on `GO`)
- Replay-continuity row keys (fixed seed/replay permutation):
  - `fixture_id` (string)
  - `fixture_seed` (string)
  - `run_id` (string)
  - `test_id` (string)
  - `chain` (string)
  - `network` (string)
  - `perturbation` (string)
  - `peer_chain` (string)
  - `replay_phase` (string)
  - `cross_chain_reads` (boolean)
  - `cross_chain_writes` (boolean)
  - `peer_cursor_delta` (integer)
  - `peer_watermark_delta` (integer)
  - `canonical_event_id_unique_ok` (boolean)
  - `replay_idempotent_ok` (boolean)
  - `cursor_monotonic_ok` (boolean)
  - `signed_delta_conservation_ok` (boolean)
  - `outcome` (enum: `GO`/`NO-GO`)
  - `evidence_present` (boolean)
  - `failure_mode` (string, required when `outcome=NO-GO`; blank on `GO`)
- QA gate row keys:
  - `check`
  - `scope`
  - `required_artifacts_present`
  - `cross_chain_reads_ok`
  - `cross_chain_writes_ok`
  - `peer_cursor_delta_ok`
  - `peer_watermark_delta_ok`
  - `replay_continuity_ok`
  - `reproducible_fixture_ok`
  - `outcome`
  - `evidence_present`
  - `recommendation`
- Fixture replayability rule:
  - Fixed `fixture_seed` + `run_id` identifies a deterministic replay permutation.
  - `outcome` is `GO` only when all required slices complete with all peer deltas zero.
  - `evidence_present` must be `true` for every required row.
- Replay reproducibility rule:
  - In replay rows, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, and `signed_delta_conservation_ok` must all be `true`.
- Gate interpretation:
  - `peer_cursor_delta` and `peer_watermark_delta` must be `0` for every required row.
  - `replay_phase` rows must keep all required invariant checks true.
  - Any row with `cross_chain_reads=true`, `cross_chain_writes=true`, `outcome=NO-GO`, missing/blank `failure_mode` on `NO-GO`, or any required invariant false blocks promotion.
  - `I-0508` gate `recommendation=GO` and all checks true are required for `M95-S4` promotion.

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

## Counterexample Contract (Machine-Checkable)
- Matrix family: `I-0501-m95-s1-control-perturbation-continuity-matrix.md`.
- Deterministic perturbation vectors:
  - `single_chain_lag_spike`
  - `single_chain_queue_depth_jolt`
  - `single_chain_telemetry_glitch`
  - `single_chain_override_conflict`
- Each row must expose:
  - `test_id`
  - `mutated_chain`
  - `control_input_perturbation`
  - `peer_chain`
  - `cross_chain_reads`
  - `cross_chain_writes`
  - `peer_cursor_delta`
  - `peer_watermark_delta`
  - `outcome` (`GO` / `NO-GO`)
  - `evidence_present` (`true` / `false`)
- GO rule:
  - all perturbation rows for a test have `cross_chain_reads=false`, `cross_chain_writes=false`, `peer_cursor_delta=0`, `peer_watermark_delta=0`, `outcome=GO`, and `evidence_present=true`.
- NO-GO triggers if:
  - any row has `outcome=NO-GO`, or
  - any required row key is missing or malformed.

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
  - all required evidence artifacts exist
  - all control matrix rows have:
    - `cross_chain_reads=false`
    - `cross_chain_writes=false`
    - `peer_cursor_delta=0`
    - `peer_watermark_delta=0`
    - `outcome=GO`
  - all continuity rows pass invariants in matrix and contain deterministic fixtures.
- `NO-GO` if any of the above checks fail.

## Decision Hook
- `DP-0106-M95`: control telemetry and control outputs are chain-local for mandatory chains; any measurable cross-chain control coupling is a hard gate failure until remediated.
