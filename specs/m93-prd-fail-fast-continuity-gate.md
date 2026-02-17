# M93 PRD-Priority Fail-Fast + Continuity Gate

## Scope
- Milestone: `M93`
- Execution slices: `M93-S1` (`I-0486`), `M93-S2` (`I-0487`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `8.4`: Restart + failed-path replay continuity while preserving cursor/watermark correctness.
- `8.5`: Fail-fast safety must abort on error classes without unsafe cursor/watermark advancement.
- `10`: deterministic replay acceptance, including one-chain perturbation isolation.

## C0095 Tranche Issue Mapping
- tranche: `C0095` (`I-0532 -> I-0533 -> I-0534`)
- `I-0533` defines the planner handoff artifacts for:
  - PRD `8.4`/`8.5` continuity proof on mandatory chains
  - `I-0533-m93-s1-fail-fast-continuity-matrix.md`
  - `I-0533-m93-s2-one-chain-isolation-matrix.md`
- `I-0534` consumes those artifacts and enforces NO-GO if any required row is missing, any `outcome=NO-GO`, or any one-chain isolation violation.

## C0101 Revalidation Handoff Mapping
- tranche: `C0101` (`I-0556 -> I-0557 -> I-0558`)
- `I-0557` refreshes PRD `8.4`/`8.5`/`10` machine-checkable evidence artifacts under this tranche.
- `I-0558` verifies `GO`/`evidence_present=true` continuity for required peer-isolation rows with zero bleed.
- Required tranche artifacts for `I-0557`:
  - `.ralph/reports/I-0557-m93-s1-fail-fast-continuity-matrix.md`
  - `.ralph/reports/I-0557-m93-s2-one-chain-isolation-matrix.md`
- Hard-stop expectation for C0101:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0` where required
  - `peer_watermark_delta=0` where required
- `DP-0117-C0101`: any required C0101 continuation/continuity row with `outcome=NO-GO`, `evidence_present=false`, or any required peer delta not equal to zero blocks `I-0558` and optional-refinement unblocking.

## Problem Statement
The PRD `R5/R8` controls are still not closed with an explicit, testable gate. Current execution requires a definitive contract proving correctness-impacting failures always abort immediately, cannot advance failed-path state, and that restart/replay reproduces equivalent outputs across mandatory chains.

## Reliability Contract
1. Correctness-impacting defects must abort immediately (`panic`) on first failure path and must not advance cursor or watermark on failed execution.
2. Replay from last-safe committed boundary reproduces canonical tuple outcomes and materialized-balance outcomes deterministically across mandatory chains.
3. One-chain fail-fast perturbation while peer chains progress produces no cross-chain control bleed and no cross-chain cursor/watermark bleed.
4. Fail-fast and restart evidence is represented as reproducible matrix artifacts and mandatory follow-up tickets on any regression.

## Required Matrices (C0095 handoff)
- Fail-fast continuity matrix (`.ralph/reports/I-0530-m93-s1-fail-fast-continuity-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- One-chain isolation matrix (`.ralph/reports/I-0530-m93-s2-one-chain-isolation-matrix.md`) required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`
- For `I-0533` tranche completeness, these matrix files must be the following active counterparts:
  - `.ralph/reports/I-0533-m93-s1-fail-fast-continuity-matrix.md`
  - `.ralph/reports/I-0533-m93-s2-one-chain-isolation-matrix.md`

## Matrix Contract (Machine-Checkable)
- `outcome` is `GO` or `NO-GO`.
- `evidence_present=true` is required for all required `GO` rows.
- For `outcome=NO-GO`, `failure_mode` must be non-empty.
- For required fail-fast continuity rows, invariants `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, and `chain_adapter_runtime_wired_ok` must all be `true` on `GO`.
- For required one-chain isolation rows with `GO`, `peer_cursor_delta=0` and `peer_watermark_delta=0`.
- For C0095 acceptance, `I-0533` must include all mandatory-chain rows for:
  - `solana-devnet`
  - `base-sepolia`
  - `btc-testnet`

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `solana_fee_event_coverage`
- `base_fee_split_coverage`
- `reorg_recovery_deterministic`
- `chain_adapter_runtime_wired`

## Measurable Exit Gates
1. `0` duplicate canonical IDs under defined fail-fast/restart fixture families.
2. `0` replay-path duplicate deltas and `0` materialized-balance divergence under repeated fail/recover runs.
3. `0` cross-chain control/cursor bleed assertions under one-chain panic/restart counterexamples.
4. `0` failed-path cursor/watermark progressions in all required fail-fast fault cases.
5. PRD handoff matrix files exist and are bounded: `I-0533-m93-s1-fail-fast-continuity-matrix.md` and `I-0533-m93-s2-one-chain-isolation-matrix.md`.
6. Validation commands pass: `make test`, `make test-sidecar`, `make lint`.

## Test Matrix
1. Fault-injection matrix:
 - fail-fast correctness-impacting failure classes in `solana-devnet`, `base-sepolia`, `btc-testnet`.
2. Restart matrix:
 - replay from deterministic committed boundaries with canonical tuple and balance equivalence assertions.
3. Peer-progress counterexamples:
 - one-chain fail-fast perturbation while peers progress, assert no cross-chain control/cursor bleed.
4. Continuity assertions:
 - explicit continuity checks for fee/event coverage and signed-delta conservation.
5. Evidence output:
 - `.ralph/reports` entries with matrix status and fail/recovery recommendation.

## Decision Hook
- `DP-0103-M93`: treat any observed failed-path cursor/watermark progression as a hard contract failure; fail gate for the corresponding slice until deterministic replay and fault-matrix parity are re-proven.
- `DP-0113-C0095`: C0095 remains blocked unless required `I-0533` matrix artifacts for all mandatory chains have `outcome=GO`, `evidence_present=true`, and required invariants true.

## C0101 Decision and Artifact Gate
- `DP-0117-C0101`: C0101 remains blocked unless the `I-0557` fail-fast continuity and one-chain isolation artifacts show all required mandatory-chain rows as:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0` where required
  - `peer_watermark_delta=0` where required
- Required artifacts for hard-stop handoff are:
  - `.ralph/reports/I-0557-m93-s1-fail-fast-continuity-matrix.md`
  - `.ralph/reports/I-0557-m93-s2-one-chain-isolation-matrix.md`

## C0109 (`I-0584`) Tranche Issue Mapping
- tranche: `C0109` (`I-0584 -> I-0585 -> I-0587`)
- `I-0585` defines C0109 PRD handoff artifacts on `I-0584` fail-fast continuity continuation under mandatory-chain one-chain perturbation:
  - `.ralph/reports/I-0585-m93-s1-fail-fast-continuity-matrix.md`
  - `.ralph/reports/I-0585-m93-s2-one-chain-isolation-matrix.md`
- Required row keys for `I-0585-m93-s1-fail-fast-continuity-matrix.md`:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- Required row keys for `I-0585-m93-s2-one-chain-isolation-matrix.md`:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`
- Required C0109 hard-stop checks for `GO` rows:
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `evidence_present=true`
  - `outcome=GO`
  - `peer_cursor_delta=0` where required
  - `peer_watermark_delta=0` where required
- `I-0585` must report non-empty `failure_mode` on required `NO-GO` rows.
- `I-0587` verifies `I-0585` rows for mandatory chains (`solana-devnet`, `base-sepolia`, `btc-testnet`) and blocks C0109 on any required `NO-GO`, missing evidence, false required booleans, or non-zero peer deltas.
- Required C0109 decision hook:
  - `DP-0142-C0109`: C0109 remains blocked unless all required `I-0585` matrix rows for mandatory chains are present and satisfy `outcome=GO`, `evidence_present=true`, required hard-stop booleans, and required peer deltas zero where required.

### C0119 (`I-0627`) handoff: fail-fast restart continuity revalidation
- PRD traceability for C0119:
  - `8.4`: failed-path replay continuity with cursor/watermark rollback safety.
  - `8.5`: correctness-impacting path abort semantics remain fail-fast.
  - `10`: deterministic replay and peer-isolation acceptance under one-chain perturbation.
- C0119 lock state: `C0119-PRD-FAILFAST-RESTART-ISOLATION-HARDENING`.
- C0119 queue adjacency: hard dependency `I-0624 -> I-0627 -> I-0628`.
- Required artifacts:
  - `.ralph/reports/I-0627-m93-s1-fail-fast-continuity-matrix.md`
  - `.ralph/reports/I-0627-m93-s2-one-chain-isolation-matrix.md`

#### C0119 mandatory class coverage rows
| chain | network | class_path |
|---|---|---|
| `solana` | `devnet` | `TRANSFER` |
| `solana` | `devnet` | `MINT` |
| `solana` | `devnet` | `BURN` |
| `solana` | `devnet` | `FEE` |
| `base` | `sepolia` | `TRANSFER` |
| `base` | `sepolia` | `MINT` |
| `base` | `sepolia` | `BURN` |
| `base` | `sepolia` | `fee_execution_l2` |
| `base` | `sepolia` | `fee_data_l1` |
| `btc` | `testnet` | `TRANSFER:vin` |
| `btc` | `testnet` | `TRANSFER:vout` |
| `btc` | `testnet` | `miner_fee` |

#### C0119 matrix contracts for `I-0627`
- `I-0627-m93-s1-fail-fast-continuity-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0627-m93-s2-one-chain-isolation-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`
- Required `permutation` enum for continuity rows:
  - `canonical_range_replay`
  - `replay_order_swap`
  - `one_chain_restart_perturbation`

#### C0119 hard-stop checks
- For required `I-0627` rows with `outcome=GO`:
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
- For required one-chain isolation rows with `outcome=GO`:
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
  - `evidence_present=true`
  - `failure_mode` is empty
- Required `NO-GO` rows in either artifact must include non-empty `failure_mode`.

#### C0119 decision hook
- `DP-0154-C0119`: C0119 remains blocked unless all required `I-0627` rows for mandatory chains in both C0119 artifacts are present, `outcome=GO`, `evidence_present=true`, hard-stop booleans true, and required peer deltas are zero where required.

### C0120 (`I-0630`) handoff: fail-fast restart continuity counterexample revalidation
- PRD traceability for C0120:
  - `8.4`: failed-path replay continuity with cursor/watermark rollback safety.
  - `8.5`: correctness-impacting path abort semantics remain fail-fast.
  - `10`: deterministic replay and peer-isolation acceptance under one-chain perturbation.
- C0120 lock state: `C0120-PRD-FAILFAST-RESTART-COUNTEREXAMPLE-REVALIDATION`.
- C0120 queue adjacency: hard dependency `I-0629 -> I-0630 -> I-0631`.
- Required artifacts:
  - `.ralph/reports/I-0630-m93-s1-fail-fast-continuity-revalidation-matrix.md`
  - `.ralph/reports/I-0630-m93-s2-one-chain-isolation-revalidation-matrix.md`

#### C0120 mandatory class coverage rows
| chain | network | class_path |
|---|---|---|
| `solana` | `devnet` | `TRANSFER` |
| `solana` | `devnet` | `MINT` |
| `solana` | `devnet` | `BURN` |
| `solana` | `devnet` | `FEE` |
| `base` | `sepolia` | `TRANSFER` |
| `base` | `sepolia` | `MINT` |
| `base` | `sepolia` | `BURN` |
| `base` | `sepolia` | `fee_execution_l2` |
| `base` | `sepolia` | `fee_data_l1` |
| `btc` | `testnet` | `TRANSFER:vin` |
| `btc` | `testnet` | `TRANSFER:vout` |
| `btc` | `testnet` | `miner_fee` |

#### C0120 matrix contracts for `I-0630`
- `I-0630-m93-s1-fail-fast-continuity-revalidation-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0630-m93-s2-one-chain-isolation-revalidation-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`
- Required `permutation` enum for continuity rows:
  - `canonical_range_replay`
  - `replay_order_swap`
  - `failed_path_restart_recovery`
  - `one_chain_restart_perturbation`

#### C0120 hard-stop checks
- For required `I-0630` rows with `outcome=GO`:
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
- For required one-chain isolation rows with `outcome=GO`:
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
  - `evidence_present=true`
  - `failure_mode` is empty
- Required `NO-GO` rows in either artifact must include non-empty `failure_mode`.

#### C0120 decision hook
- `DP-0155-C0120`: C0120 remains blocked unless all required `I-0630` rows for mandatory chains in both C0120 artifacts are present, `outcome=GO`, `evidence_present=true`, hard-stop booleans true, required peer deltas are zero where required, and non-empty `failure_mode` for required `NO-GO` rows.

### C0121 (`I-0635`) handoff: fail-fast restart continuity + adapter wiring counterexample
- PRD traceability for C0121:
  - `R1`: no duplicate event ID outcomes.
  - `8.4`: failed-path replay continuity with cursor/watermark rollback safety.
  - `8.5`: correctness-impacting failures remain fail-fast.
  - `10`: deterministic replay and peer-isolation acceptance under one-chain perturbation.
- C0121 lock state: `C0121-PRD-FAILFAST-CHAIN-ADAPTER-WIRING-RESTART-HARDENING`.
- C0121 queue adjacency: hard dependency `I-0632 -> I-0635 -> I-0636`.
- Required artifacts:
  - `.ralph/reports/I-0635-m93-s1-fail-fast-continuity-matrix.md`
  - `.ralph/reports/I-0635-m93-s2-one-chain-isolation-matrix.md`

#### C0121 mandatory class coverage rows
| chain | network | class_path |
|---|---|---|
| `solana` | `devnet` | `TRANSFER` |
| `solana` | `devnet` | `MINT` |
| `solana` | `devnet` | `BURN` |
| `solana` | `devnet` | `FEE` |
| `base` | `sepolia` | `TRANSFER` |
| `base` | `sepolia` | `MINT` |
| `base` | `sepolia` | `BURN` |
| `base` | `sepolia` | `fee_execution_l2` |
| `base` | `sepolia` | `fee_data_l1` |
| `btc` | `testnet` | `TRANSFER:vin` |
| `btc` | `testnet` | `TRANSFER:vout` |
| `btc` | `testnet` | `miner_fee` |

#### C0121 matrix contracts for `I-0635`
- `I-0635-m93-s1-fail-fast-continuity-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0635-m93-s2-one-chain-isolation-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`

#### C0121 required permutation axes
- `canonical_range_replay`
- `replay_order_swap`
- `failed_path_restart_recovery`
- `one_chain_restart_perturbation`

#### C0121 (I-0635) matrix addendum
- Required artifact files:
  - `.ralph/reports/I-0635-m93-s1-fail-fast-continuity-matrix.md`
  - `.ralph/reports/I-0635-m93-s2-one-chain-isolation-matrix.md`
- Required row keys for I-0635 continuity rows:
  - `fixture_id`
  - `fixture_seed`
  - `run_id`
  - `chain`
  - `network`
  - `permutation`
  - `class_path`
  - `peer_chain`
  - `canonical_event_id_unique_ok`
  - `replay_idempotent_ok`
  - `cursor_monotonic_ok`
  - `signed_delta_conservation_ok`
  - `chain_adapter_runtime_wired_ok`
  - `evidence_present`
  - `outcome`
  - `failure_mode`
- Required row keys for I-0635 one-chain isolation rows:
  - `fixture_id`
  - `fixture_seed`
  - `run_id`
  - `chain`
  - `network`
  - `peer_chain`
  - `peer_cursor_delta`
  - `peer_watermark_delta`
  - `evidence_present`
  - `outcome`
  - `failure_mode`
- I-0635 hard-stop conditions:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` for required isolation rows

#### C0121 hard-stop checks
- For required `I-0635` rows with `outcome=GO`:
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
- For required one-chain isolation rows with `outcome=GO`:
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
  - `evidence_present=true`
  - `failure_mode` is empty
- Required `NO-GO` rows in either artifact must include non-empty `failure_mode`.

#### C0121 decision hook
- `DP-0156-C0121`: C0121 remains blocked unless all required `I-0635` rows for `solana-devnet`, `base-sepolia`, `btc-testnet` in both artifacts are present, `outcome=GO`, `evidence_present=true`, required hard-stop booleans true, and required peer deltas are zero where required for one-chain-isolation rows.
