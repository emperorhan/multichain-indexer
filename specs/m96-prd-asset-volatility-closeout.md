# M96 PRD-Traceable Asset-Volatility Closeout Gate

## Scope
- Milestone: `M96`
- Execution slice: `M96-S1`
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R1`: no-duplicate indexing.
- `R2`: full asset-volatility coverage.
- `R3`: chain-family fee completeness.
- `8.4`/`8.5`: failed-path continuity and fail-fast cursor/watermark safety.
- `9.4`: topology parity and continuity principles.
- `10`: deterministic replay acceptance behavior.

## Problem Statement
`I-0517` must close the remaining PRD class-coverage/replay gap by defining explicit, machine-checkable gates for mandatory-chain class completeness **before** optional reliability work.

## Coverage Matrix Contract (Mandatory-Chain Class Cells)
All required matrix rows must be present and set `evidence_present=true` in the required `M96` artifacts.

Mandatory rows:
- `solana-devnet`:
  - `TRANSFER`
  - `MINT`
  - `BURN`
  - `FEE`
- `base-sepolia`:
  - `TRANSFER`
  - `MINT`
  - `BURN`
  - `fee_execution_l2`
  - `fee_data_l1`
- `btc-testnet`:
  - `TRANSFER:vin`
  - `TRANSFER:vout`
  - `miner_fee` (canonical BTC miner-fee conservation row)

## Deterministic Replay and Duplicate-Risk Gates
- `event_id` generation and tuple ordering for all required class-path cells must be deterministic for replayed fixture permutations.
- Required permutation axis (exact enum):
  - `canonical_range_replay`
  - `replay_order_swap`
  - `one_chain_restart_perturbation`
- Every required row in the replay/duplication families must report:
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`

## Evidence Artifacts (to be produced by downstream `I-0542`/`I-0543`)
- `.ralph/reports/I-0518-m96-s1-class-coverage-matrix.md`
- `.ralph/reports/I-0518-m96-s1-dup-suppression-matrix.md`
- `.ralph/reports/I-0519-m96-s1-replay-continuity-matrix.md`
- `.ralph/reports/I-0519-m96-s1-chain-isolation-matrix.md`
- `.ralph/reports/I-0542-evidence.md`

## Machine-Checkable Evidence Schema
- Class-coverage matrix rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `outcome`, `failure_mode`, `notes`
- Duplicate-suppression matrix rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `canonical_id_count`, `evidence_present`, `outcome`, `failure_mode`
- Replay-continuity matrix rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `evidence_present`, `outcome`, `failure_mode`
- Chain-isolation matrix rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`

Required enum/value constraints:
- `outcome` must be `GO` or `NO-GO`.
- `evidence_present=true` is required for all `GO` rows.
- For `outcome=NO-GO`, `failure_mode` must be non-empty.
- `outcome=GO` requires all required gates true for that matrix row and `peer_cursor_delta=0`, `peer_watermark_delta=0` where those fields exist.

## Measurable Exit Gates
1. `0` missing required matrix cells for non-`NA` class-paths in `solana-devnet`, `base-sepolia`, and `btc-testnet`.
2. `0` duplicate canonical IDs across required replay permutations in `I-0519-m96-s1-replay-continuity-matrix.md`.
3. Replay permutation checks (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`) are true for all required rows.
4. Peer isolation checks report `peer_cursor_delta=0` and `peer_watermark_delta=0` for all required rows.
5. Validation commands remain unchanged and must pass: `make test`, `make test-sidecar`, `make lint`.

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `chain_adapter_runtime_wired`

### C0102 hard-stop execution addendum
- C0102 artifacts are required for planner handoff and must remain the only accepted proof of revalidation completion before optional refinement work resumes.
- Required hard-stop row fields and booleans:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
  - `failure_mode` must be empty for `GO` and non-empty for `NO-GO`.
- Required row contracts for C0102 artifacts:
  - `.ralph/reports/I-0560-m96-s1-class-coverage-revalidation-matrix.md`
  - `.ralph/reports/I-0560-m96-s1-chain-isolation-revalidation-matrix.md`
  - `.ralph/reports/I-0560-m96-s1-replay-continuity-revalidation-matrix.md`
- `DP-0118-C0102` stays blocked until all required rows in all three artifacts meet the hard-stop contract above and reference the explicit artifact paths.

## Decision Hook
- `DP-0109-M96`: Any required class-path cell missing evidence, or any required row with `outcome=NO-GO`/invalid fail-mode semantics, is a hard NO-GO for milestone promotion.
- `DP-0119-C0097`: Any required row with `outcome=NO-GO`, `evidence_present=false`, `peer_cursor_delta!=0`, or `peer_watermark_delta!=0` blocks promotion into optional post-PRD continuation.

## C0102 (`I-0559`) revalidation addendum
- Focus: PRD-priority class/fee coverage continuity revalidation before optional refinements resume.
- PRD traceability:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility coverage.
  - `R3`: chain-family fee completeness.
  - `10`: deterministic replay acceptance under one-chain perturbation.
- C0102 planner contract (`I-0559 -> I-0560 -> I-0561`) updates `specs/m96-prd-asset-volatility-closeout.md` to require:
  - `.ralph/reports/I-0560-m96-s1-class-coverage-revalidation-matrix.md`
  - `.ralph/reports/I-0560-m96-s1-chain-isolation-revalidation-matrix.md`
  - `.ralph/reports/I-0560-m96-s1-replay-continuity-revalidation-matrix.md`
- Required schema for C0102 evidence rows:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `permutation`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `outcome`, `failure_mode`
- Required hard-stop gates for C0102:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
  - `failure_mode` is non-empty for `outcome=NO-GO`.
- `DP-0118-C0102`: C0102 remains blocked until required rows in all three artifacts are `GO`, evidence is complete, invariants are true, and peer deltas are zero where required.

### C0107 (`I-0578`) handoff: continuity and adapter-wiring revalidation
- PRD traceability scope:
  - `R1`
  - `R2`
  - `8.5`
  - `10`
  - `chain_adapter_runtime_wired`
- Scope: mandatory-chain C0107 continuity continuity must remain duplicate-free, deterministic under perturbation, and one-chain isolated for adapter-wired paths.
- Required evidence artifacts:
  - `.ralph/reports/I-0578-m96-s1-coverage-revalidation-matrix.md`
  - `.ralph/reports/I-0578-m96-s2-dup-suppression-matrix.md`
  - `.ralph/reports/I-0578-m96-s3-chain-isolation-matrix.md`

#### C0107 Matrix Contracts (`I-0578`)
- `I-0578-m96-s1-coverage-revalidation-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0578-m96-s2-dup-suppression-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0578-m96-s3-chain-isolation-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`

#### C0107 Hard-stop Rules
- Required `outcome=GO` row contract for required artifacts:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
- For required peer/isolation rows where peer deltas are present:
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
- Required `outcome=NO-GO` rows must include non-empty `failure_mode`.

#### C0107 Mandatory C0107 class rows
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

#### C0107 Decision Hook
- `DP-0138-C0107` blocks promotion if any required `I-0578` row fails the hard-stop contract above.

### C0112 (`I-0597`) revalidation addendum
- Focused unresolved PRD requirements from `PRD.md`:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `R3`: chain-family fee completeness.
  - `8.5`: failed-path cursor/watermark progression is forbidden.
  - `10`: deterministic replay and one-chain perturbation acceptance.
  - `chain_adapter_runtime_wired`: adapter/runtime wiring must stay invariant under one-chain perturbation.
- `solana_fee_event_coverage`: required explicit fee coverage for Solana transaction fee debit semantics.
- `base_fee_split_coverage`: required explicit split of Base fee semantics (`fee_execution_l2`, `fee_data_l1`).
- C0112 queue adjacency: hard dependency `I-0596 -> I-0597 -> I-0598`.
- C0112 hard-stop artifact requirements:
  - `.ralph/reports/I-0597-m96-s1-coverage-class-revalidation-matrix.md`
  - `.ralph/reports/I-0597-m96-s2-dup-suppression-matrix.md`
  - `.ralph/reports/I-0597-m96-s3-chain-isolation-matrix.md`

#### C0112 Mandatory coverage class rows
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

#### C0112 Replay perturbation axis
- `canonical_range_replay`
- `replay_order_swap`
- `one_chain_restart_perturbation`

#### C0112 Matrix Contracts (`I-0597`)
- `I-0597-m96-s1-coverage-class-revalidation-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0597-m96-s2-dup-suppression-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0597-m96-s3-chain-isolation-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`

Required hard-stop checks for all required `GO` rows in C0112:
- `outcome=GO`
- `evidence_present=true`
- `canonical_event_id_unique_ok=true`
- `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
- `signed_delta_conservation_ok=true`
- `solana_fee_event_coverage_ok=true`
- `base_fee_split_coverage_ok=true`
- `chain_adapter_runtime_wired_ok=true`
- `failure_mode` is empty
- `peer_cursor_delta=0` and `peer_watermark_delta=0` where applicable in `I-0597-m96-s3-chain-isolation-matrix.md`
- `outcome=NO-GO` requires non-empty `failure_mode`.
- Downstream validation commands remain unchanged: `make test`, `make test-sidecar`, `make lint`.

Required PRD hard-stop decision hook:
- `DP-0145-C0112`: C0112 remains blocked unless all required `I-0597` rows for mandatory chains (`solana-devnet`, `base-sepolia`, `btc-testnet`) in the three C0112 artifacts are present and satisfy the hard-stop checks above.
- Required `NO-GO` rows in C0112 artifacts must include non-empty `failure_mode`.

### C0114 (`I-0605`) implementation tranche handoff
- Focused PRD traceability:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `R3`: chain-family fee completeness.
  - `8.5`: failed-path cursor/watermark progression remains prohibited.
  - `10`: deterministic replay and peer-isolation acceptance.
  - `solana_fee_event_coverage`: explicit Solana fee debit class row.
  - `base_fee_split_coverage`: explicit Base execution-vs-data fee class rows.
  - `chain_adapter_runtime_wired`: runtime adapters remain deterministic under one-chain perturbation.
- `C0114` queue adjacency: hard dependency `I-0604 -> I-0605 -> I-0606`.
- `I-0605` updates this spec with required hard-stop contracts for implementation + reproducible peer-isolation evidence.
- `I-0605` matrix contracts (`I-0605`):
  - `I-0605-m96-s1-coverage-class-hardening-matrix.md` required row fields:
    - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
  - `I-0605-m96-s2-dup-suppression-matrix.md` required row fields:
    - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
  - `I-0605-m96-s3-replay-continuity-matrix.md` required row fields:
    - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `C0114` hard-stop checks for all required `GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - all required hard-stop booleans true (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`)
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` in `I-0605-m96-s3-replay-continuity-matrix.md`
  - `failure_mode` is empty.
- For required `NO-GO` rows, `failure_mode` must be non-empty.
- C0114 decision hook:
  - `DP-0147-C0114`: C0114 remains blocked unless all required `I-0605` rows across all three artifacts and mandatory chains are present and satisfy `outcome=GO`, `evidence_present=true`, hard-stop booleans true, and zero peer deltas where required.

### C0115 (`I-0611`) implementation/continuity revalidation addendum
- PRD traceability focus:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility coverage.
  - `R3`: chain-family fee completeness.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `10`: deterministic replay acceptance under perturbation.
  - `solana_fee_event_coverage`: explicit Solana fee debit class row.
  - `base_fee_split_coverage`: explicit Base execution-vs-data fee class rows.
  - `chain_adapter_runtime_wired`: adapter/runtime wiring invariance under required counterexamples.
- C0115 queue adjacency: hard dependency `I-0608 -> I-0611 -> I-0612`.
- C0115 artifacts are required for the same mandatory class-paths as C0114 and include explicit counterexample columns:
  - `.ralph/reports/I-0611-m96-s1-coverage-class-hardening-matrix.md`
  - `.ralph/reports/I-0611-m96-s2-dup-suppression-matrix.md`
  - `.ralph/reports/I-0611-m96-s3-replay-continuity-matrix.md`

#### C0115 mandatory coverage contract
- Mandatory class-path cells:
  - `solana` + `solana-devnet` -> `TRANSFER`, `MINT`, `BURN`, `FEE`
  - `base` + `base-sepolia` -> `TRANSFER`, `MINT`, `BURN`, `fee_execution_l2`, `fee_data_l1`
  - `btc` + `btc-testnet` -> `TRANSFER:vin`, `TRANSFER:vout`, `miner_fee`
- Mandatory replay perturbations:
  - `canonical_range_replay`
  - `replay_order_swap`
  - `one_chain_restart_perturbation`

#### C0115 Machine-Checkable Matrix Contracts (`I-0611`)
  - `I-0611-m96-s1-coverage-class-hardening-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
  - `I-0611-m96-s2-dup-suppression-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
  - `I-0611-m96-s3-replay-continuity-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`

#### C0115 Hard-stop Checks
- For all required `GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `solana_fee_event_coverage_ok=true`
  - `base_fee_split_coverage_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where peer-delta columns are present
  - `failure_mode` is empty
- For required `NO-GO` rows:
  - `failure_mode` must be non-empty.

#### C0115 Decision Hook
- `DP-0150-C0115`: C0115 remains blocked unless all required `I-0611` rows for mandatory chains in the three artifacts are present and satisfy the hard-stop contract above.

### C0117 (`I-0617`) recovery-aware asset-volatility class/factor revalidation addendum
- Focused PRD requirements:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `R3`: chain-family fee completeness.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `reorg_recovery_deterministic`: recovery/replay determinism under fork and restart perturbation.
  - `chain_adapter_runtime_wired`: runtime/wiring invariance under required perturbations.
- `C0117` queue adjacency: hard dependency `I-0616 -> I-0617 -> I-0618`.
- `C0117` lock state: `C0117-PRD-RECONVERGED-CLASSEVENT-RECOVERY-REVALIDATION`.
- C0117 artifacts are required for the same mandatory class-path matrix families as previous `C0115` plus recovery perturbation columns:
  - `.ralph/reports/I-0617-m96-s1-coverage-recovery-hardening-matrix.md`
  - `.ralph/reports/I-0617-m96-s2-dup-suppression-recovery-matrix.md`
  - `.ralph/reports/I-0617-m96-s3-recovery-continuity-matrix.md`

#### C0117 Mandatory class/recovery contract rows
- Mandatory class-path cells:
  - `solana` + `solana-devnet` -> `TRANSFER`, `MINT`, `BURN`, `FEE`
  - `base` + `base-sepolia` -> `TRANSFER`, `MINT`, `BURN`, `fee_execution_l2`, `fee_data_l1`
  - `btc` + `btc-testnet` -> `TRANSFER:vin`, `TRANSFER:vout`, `miner_fee`
- Recovery perturbations:
  - `one_block_reorg`
  - `multi_block_reorg`
  - `canonical_range_replay`
  - `finalized_to_pending_crossover`
  - `restart_from_rollback_boundary`

#### C0117 Matrix Contracts (`I-0617`)
- `I-0617-m96-s1-coverage-recovery-hardening-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `fork_type`, `recovery_permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0617-m96-s2-dup-suppression-recovery-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0617-m96-s3-recovery-continuity-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`

#### C0117 Hard-stop checks
- For every required `GO` row:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `solana_fee_event_coverage_ok=true`
  - `base_fee_split_coverage_ok=true`
  - `reorg_recovery_deterministic_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
  - `failure_mode` is empty
- For required `NO-GO` rows:
  - `failure_mode` must be non-empty.

#### C0117 Decision Hook
- `DP-0152-C0117`: `C0117` remains blocked unless all required `I-0617` rows for mandatory chains in the three C0117 artifacts are present and satisfy all hard-stop checks above.

### C0118 (`I-0622`) implementation/continuity production handoff
- Focused PRD requirements:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `R3`: chain-family fee completeness.
  - `10`: deterministic replay and one-chain perturbation acceptance.
  - `chain_adapter_runtime_wired`: adapter/runtime wiring invariance under required perturbations.
- `C0118` queue adjacency: hard dependency `I-0619 -> I-0622 -> I-0623`.
- `C0118` lock state: `C0118-PRD-ASSET-VOLATILITY-CONTINUITY-IMPLEMENTATION`.
- C0118 required artifacts:
  - `.ralph/reports/I-0622-m96-s1-coverage-runtime-hardening-matrix.md`
  - `.ralph/reports/I-0622-m96-s2-dup-suppression-matrix.md`
  - `.ralph/reports/I-0622-m96-s3-one-chain-adapter-isolation-matrix.md`

#### C0118 Mandatory class-path coverage rows
- `solana` + `solana-devnet` -> `TRANSFER`, `MINT`, `BURN`, `FEE`
- `base` + `base-sepolia` -> `TRANSFER`, `MINT`, `BURN`, `fee_execution_l2`, `fee_data_l1`
- `btc` + `btc-testnet` -> `TRANSFER:vin`, `TRANSFER:vout`, `miner_fee`

#### C0118 Perturbation and proof constraints
- Required duplicate-suppression perturbation axis:
  - `canonical_range_replay`
  - `replay_order_swap`
  - `one_chain_restart_perturbation`
- All required rows in the C0118 artifacts must be machine-checkable:
  - `outcome` is only `GO` or `NO-GO`
  - `evidence_present=true` for every `GO`
  - `failure_mode` is empty for `GO` and non-empty for `NO-GO`

#### C0118 Matrix Contracts (`I-0622`)
- `I-0622-m96-s1-coverage-runtime-hardening-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0622-m96-s2-dup-suppression-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0622-m96-s3-one-chain-adapter-isolation-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`

#### C0118 Hard-stop checks
- Required `outcome=GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
  - peer isolation rows require `peer_cursor_delta=0` and `peer_watermark_delta=0`
- Required `NO-GO` rows must include non-empty `failure_mode`.

#### C0118 Decision Hook
- `DP-0153-C0118`: `C0118` remains blocked unless all required `I-0622` rows for mandatory chains (`solana-devnet`, `base-sepolia`, `btc-testnet`) in the three C0118 artifacts are present, satisfy hard-stop booleans above, and peer deltas are zero where required.

### C0122 (`I-0638`) implementation/recovery continuity handoff
- PRD traceability focus:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility coverage.
  - `R3`: chain-family fee completeness.
  - `10`: deterministic replay and one-chain perturbation acceptance.
  - `reorg_recovery_deterministic`: deterministic recovery/reorg continuity.
  - `chain_adapter_runtime_wired`: adapter/runtime wiring invariance under required perturbations.
  - `solana_fee_event_coverage`: explicit Solana fee debit class row.
  - `base_fee_split_coverage`: explicit Base execution-vs-data fee split classes.
- C0122 lock state: `C0122-PRD-ASSET-VOLATILITY-RECOVERY-COUNTEREXAMPLE-HARDENING`.
- C0122 queue adjacency: hard dependency `I-0637 -> I-0638 -> I-0639`.
- Required artifacts:
  - `.ralph/reports/I-0638-m96-s1-coverage-recovery-hardening-matrix.md`
  - `.ralph/reports/I-0638-m96-s2-dup-suppression-recovery-matrix.md`
  - `.ralph/reports/I-0638-m96-s3-recovery-continuity-matrix.md`

#### C0122 mandatory class-path coverage rows
| chain | network | class_path |
|---|---|---|
| `solana` | `solana-devnet` | `TRANSFER` |
| `solana` | `solana-devnet` | `MINT` |
| `solana` | `solana-devnet` | `BURN` |
| `solana` | `solana-devnet` | `FEE` |
| `base` | `base-sepolia` | `TRANSFER` |
| `base` | `base-sepolia` | `MINT` |
| `base` | `base-sepolia` | `BURN` |
| `base` | `base-sepolia` | `fee_execution_l2` |
| `base` | `base-sepolia` | `fee_data_l1` |
| `btc` | `btc-testnet` | `TRANSFER:vin` |
| `btc` | `btc-testnet` | `TRANSFER:vout` |
| `btc` | `btc-testnet` | `miner_fee` |

#### C0122 required perturbation axis
- `canonical_range_replay`
- `replay_order_swap`
- `one_chain_restart_perturbation`
- `one_block_reorg`
- `multi_block_reorg`
- `finalized_to_pending_crossover`
- `restart_from_rollback_boundary`

#### C0122 matrix contracts (`I-0638`)
- `I-0638-m96-s1-coverage-recovery-hardening-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `fork_type`, `recovery_permutation`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0638-m96-s2-dup-suppression-recovery-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0638-m96-s3-recovery-continuity-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`

#### C0122 hard-stop checks
- Required `outcome=GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `solana_fee_event_coverage_ok=true`
  - `base_fee_split_coverage_ok=true`
  - `reorg_recovery_deterministic_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
  - `failure_mode` is empty
- Required `outcome=NO-GO` rows must include non-empty `failure_mode`.

#### C0122 decision hook
- `DP-0157-C0122`: C0122 remains blocked unless all required `I-0638` rows for mandatory chains in the three artifacts are present with required `GO` hard-stop fields true, `evidence_present=true`, required peer deltas zero, and non-empty `failure_mode` for required `NO-GO` rows.

## C0125 (`I-0648`) implementation/continuity hardening handoff
- Focused PRD traceability:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility coverage.
  - `R3`: chain-family fee completeness.
  - `8.4`/`8.5`: failed-path cursor/watermark freeze on restart and replay boundaries.
  - `reorg_recovery_deterministic`: deterministic recovery/replay under one-chain perturbation.
  - `chain_adapter_runtime_wired`: runtime-wiring invariance under chain-isolated perturbation.
- C0125 lock state: `C0125-PRD-ASSET-VOLATILITY-COUNTEREXAMPLE-HARDENING`.
- C0125 queue adjacency: hard dependency `I-0647 -> I-0648 -> I-0649`.
- Required evidence artifacts:
  - `.ralph/reports/I-0648-m96-s1-asset-volatility-coverage-matrix.md`
  - `.ralph/reports/I-0648-m96-s2-dup-suppression-matrix.md`
  - `.ralph/reports/I-0648-m96-s3-one-chain-isolation-matrix.md`

#### C0125 Mandatory coverage class-path rows
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

#### C0125 required perturbation families
- `canonical_range_replay`
- `replay_order_swap`
- `one_chain_restart_perturbation`
- `failed_path_restart_recovery`

#### C0125 matrix contracts (`I-0648`)
- `I-0648-m96-s1-asset-volatility-coverage-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0648-m96-s2-dup-suppression-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0648-m96-s3-one-chain-isolation-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `reorg_recovery_deterministic_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`

#### C0125 hard-stop checks for required `GO` rows
- `outcome=GO`
- `evidence_present=true`
- `canonical_event_id_unique_ok=true`
- `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
- `signed_delta_conservation_ok=true`
- `solana_fee_event_coverage_ok=true`
- `base_fee_split_coverage_ok=true`
- `reorg_recovery_deterministic_ok=true`
- `chain_adapter_runtime_wired_ok=true`
- `peer_cursor_delta=0`
- `peer_watermark_delta=0` where applicable
- `failure_mode` is empty

Required machine-checkable constraints:
- `outcome` is `GO` or `NO-GO`.
- `evidence_present=true` is required for all required `GO` rows.
- For required `NO-GO` rows, `failure_mode` must be non-empty.
- Required `GO` rows in `I-0648-m96-s3-one-chain-isolation-matrix.md` must keep zero peer-chain deltas.

#### C0125 decision gate
- `DP-0160-C0125` blocks promotion unless all required `I-0648` rows for `solana-devnet`, `base-sepolia`, and `btc-testnet` in all three artifacts are present, `outcome=GO`, `evidence_present=true`, all required hard-stop booleans are true, and peer deltas are zero where required.

### C0130 (`I-0666`) implementation handoff addendum
- Focused PRD traceability from `PRD.md`:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `R3`: chain-family fee completeness.
  - `8.5`: failed-path cursor/watermark progression must not advance.
  - `chain_adapter_runtime_wired`: chain adapter and decoder integration remains invariant under per-chain perturbation.
- C0130 lock state: `C0130-PRD-SIDECAR-GOLDEN-CLASSPATH-COVERAGE`.
- C0130 queue adjacency: hard dependency `I-0665 -> I-0666 -> I-0667`.

### Required evidence artifacts for `I-0666`
- `.ralph/reports/I-0666-m96-s1-sidecar-class-coverage-matrix.md`
- `.ralph/reports/I-0666-m96-s2-sidecar-duplicate-suppression-matrix.md`
- `.ralph/reports/I-0666-m96-s3-sidecar-one-chain-isolation-matrix.md`

#### C0130 class-coverage contracts (`I-0666`)
- `I-0666-m96-s1-sidecar-class-coverage-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0666-m96-s2-sidecar-duplicate-suppression-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `evidence_present`, `outcome`, `failure_mode`
- `I-0666-m96-s3-sidecar-one-chain-isolation-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`

#### C0130 hard-stop checks for required `GO` rows
- `outcome=GO`
- `evidence_present=true`
- `canonical_event_id_unique_ok=true`
- `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
- `signed_delta_conservation_ok=true`
- `chain_adapter_runtime_wired_ok=true`
- `peer_cursor_delta=0` and `peer_watermark_delta=0` (where fields are present)
- `failure_mode` is empty.

#### C0130 mandatory class-path focus
- `solana` + `solana-devnet`: `TRANSFER`, `MINT`, `BURN`, `FEE`
- `base` + `base-sepolia`: `TRANSFER`, `MINT`, `BURN`, `fee_execution_l2`, `fee_data_l1`
- `btc` + `btc-testnet`: `TRANSFER:vin`, `TRANSFER:vout`, `miner_fee`

#### C0130 Decision Hook
- `DP-0179-C0130`: C0130 remains blocked unless all required `I-0666` rows for mandatory chains and required class-paths are present with hard-stop `GO` semantics and zero required peer deltas.

### C0131 (`I-0669`) implementation handoff addendum
- Focus: PRD-priority ethereum-mainnet chain target/runtime wiring before optional refinements resume.
- Focused unresolved requirement traceability:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage with replay deterministic outputs.
  - `8.5`: failed-path cursor/watermark freeze.
  - `10`: deterministic replay acceptance behavior.
  - `chain_adapter_runtime_wired`: chain-adapter/runtime wiring determinism.
- C0131 lock state: `C0131-PRD-ETHEREUM-TARGET-WIRING`.
- C0131 queue adjacency: `I-0668 -> I-0669 -> I-0670`.
- Required artifacts:
  - `.ralph/reports/I-0669-m96-s1-ethereum-target-coverage-matrix.md`
  - `.ralph/reports/I-0669-m96-s2-ethereum-runtime-wire-matrix.md`
  - `.ralph/reports/I-0669-m96-s3-ethereum-peer-isolation-matrix.md`

#### C0131 mandatory contracts (`I-0669`)
- `I-0669-m96-s1-ethereum-target-coverage-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `target_key`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0669-m96-s2-ethereum-runtime-wire-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `target_key`, `runtime_group`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0669-m96-s3-ethereum-peer-isolation-matrix.md` required row keys:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `target_key`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required mandatory rows for C0131:
  - `chain=ethereum`, `network=mainnet`, `target_key=ethereum-mainnet`

#### C0131 hard-stop checks
- `outcome=GO`
- `evidence_present=true`
- `canonical_event_id_unique_ok=true`
- `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
- `signed_delta_conservation_ok=true`
- `chain_adapter_runtime_wired_ok=true`
- required `peer_cursor_delta=0` and `peer_watermark_delta=0` where peer isolation columns are populated.
- required `NO-GO` rows must contain non-empty `failure_mode`.

#### C0131 decision hook
- `DP-0180-C0131` remains blocked unless required rows for `chain=ethereum`, `network=mainnet` are present in all three required artifacts and satisfy all C0131 hard-stop checks.

### C0132 (`I-0671`) implementation handoff addendum
- Focused PRD traceability from `PRD.md`:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `R3`: chain-family fee completeness.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `chain_adapter_runtime_wired`: chain adapter and decoder integration remains deterministic.
- C0132 lock state: `C0132-PRD-SIDECAR-GOLDEN-DECODER-PAIRING`.
- `C0132` queue adjacency: hard dependency `I-0671 -> I-0672 -> I-0673`.

#### Required evidence artifacts for `I-0672`
- `.ralph/reports/I-0672-m96-s1-sidecar-golden-class-coverage-matrix.md`
- `.ralph/reports/I-0672-m96-s2-sidecar-golden-dup-suppression-matrix.md`
- `.ralph/reports/I-0672-m96-s3-sidecar-golden-one-chain-isolation-matrix.md`

#### C0132 class-coverage contracts (`I-0672`)
- `I-0672-m96-s1-sidecar-golden-class-coverage-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0672-m96-s2-sidecar-golden-dup-suppression-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0672-m96-s3-sidecar-golden-one-chain-isolation-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_id_count`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `solana_fee_event_coverage_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`

#### C0132 mandatory class-path focus
- `solana` + `solana-devnet`: `TRANSFER`, `MINT`, `BURN`, `FEE`
- `base` + `base-sepolia`: `TRANSFER`, `MINT`, `BURN`, `fee_execution_l2`, `fee_data_l1`
- `btc` + `btc-testnet`: `TRANSFER:vin`, `TRANSFER:vout`, `miner_fee`

#### C0132 hard-stop checks for required `GO` rows
- `outcome=GO`
- `evidence_present=true`
- `canonical_event_id_unique_ok=true`
- `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
- `signed_delta_conservation_ok=true`
- `solana_fee_event_coverage_ok=true`
- `base_fee_split_coverage_ok=true`
- `chain_adapter_runtime_wired_ok=true`
- `peer_cursor_delta=0` and `peer_watermark_delta=0` where applicable
- `failure_mode` is empty.
- `canonical_id_count` should remain `1` for deterministic fixture rows.

#### C0132 decision hook
- `DP-0181-C0132` remains blocked unless required rows for `solana-devnet`, `base-sepolia`, and `btc-testnet` in all three artifacts are present and satisfy all required `GO` hard-stop checks.

### C0133 (`I-0675`) implementation handoff addendum
- Focused PRD traceability from `PRD.md`:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `chain_adapter_runtime_wired`: chain-adapter/runtime wiring remains deterministic and replay-safe.
- `C0133` lock state: `C0133-PRD-BTC-ADAPTER-COMPLETENESS`.
- `C0133` queue adjacency: hard dependency `I-0675 -> I-0676 -> I-0677`.

#### Required evidence artifacts for `I-0676`
- `.ralph/reports/I-0676-m96-s1-btc-adapter-completeness-matrix.md`
- `.ralph/reports/I-0676-m96-s2-btc-adapter-replay-matrix.md`
- `.ralph/reports/I-0676-m96-s3-btc-adapter-isolation-matrix.md`

#### C0133 mandatory BTC class-path contracts (`I-0676`)
- `I-0676-m96-s1-btc-adapter-completeness-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0676-m96-s2-btc-adapter-replay-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- `I-0676-m96-s3-btc-adapter-isolation-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`

#### C0133 mandatory class rows
- `btc` + `btc-testnet`: `TRANSFER:vin`, `TRANSFER:vout`, `miner_fee`

#### C0133 required perturbation families
- `scan_replay`
- `scan_batching`
- `envelope_fee_determinism`
- `replay_restart`

#### C0133 hard-stop checks for required `GO` rows
- `outcome=GO`
- `evidence_present=true`
- `canonical_event_id_unique_ok=true`
- `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
- `signed_delta_conservation_ok=true`
- `chain_adapter_runtime_wired_ok=true`
- required `peer_cursor_delta=0` and `peer_watermark_delta=0` where applicable
- `failure_mode` is empty.

#### C0133 decision hook
- `DP-0182-C0133` remains blocked unless required rows for `chain=btc`, `network=btc-testnet` in all three artifacts are present with:
  - `outcome=GO`
  - `evidence_present=true`
  - required hard-stop booleans true (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`)
  - required peer deltas equal zero where present (`peer_cursor_delta=0`, `peer_watermark_delta=0`)
  - and non-empty `failure_mode` for any required `NO-GO` row.

### C0136 (`I-0684`) implementation handoff addendum
- Focused PRD traceability:
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `chain_adapter_runtime_wired`: chain adapter/runtime wiring should remain deterministic and replay-safe under transport initialization outcomes.
- C0136 lock state: `C0136-PRD-STREAM-TRANSPORT-FAILFAST`.
- `C0136` queue adjacency: hard dependency `I-0684 -> I-0687 -> I-0688`.

#### Required evidence artifact for `I-0687`
- `.ralph/reports/I-0687-m96-s1-stream-failfast-matrix.md`

#### C0136 transport fail-fast contracts (`I-0687`)
- Required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `stream_transport_enabled`, `redis_url`, `stream_backend_mode`, `fallback_used`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows:
  - `chain=solana`, `network=devnet`, `stream_transport_enabled=true`
  - `chain=base`, `network=sepolia`, `stream_transport_enabled=true`
  - `chain=btc`, `network=testnet`, `stream_transport_enabled=true`
  - `stream_transport_enabled=false` baseline rows may remain documented for operational continuity checks.

#### C0136 hard-stop checks
- `outcome=GO`
- `evidence_present=true`
- `canonical_event_id_unique_ok=true`
- `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
- `signed_delta_conservation_ok=true`
- `chain_adapter_runtime_wired_ok=true`
- `failure_mode` is empty.
- For `stream_transport_enabled=true`, `fallback_used=false` and `stream_backend_mode=redis_required`.
- For required `NO-GO` rows, `failure_mode` is non-empty.

#### C0136 decision hook
- `DP-0185-C0136` remains blocked until required rows in `.ralph/reports/I-0687-m96-s1-stream-failfast-matrix.md` for `chain=solana`, `base`, and `btc` are present with `outcome=GO`, `evidence_present=true`, required hard-stop booleans true, and `fallback_used=false` where stream mode is enabled.

### C0137 (`I-0689`) implementation handoff addendum
- Focused PRD traceability:
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `chain_adapter_runtime_wired`: stream boundaries remain deterministic across restart.
  - `cursor_monotonic`: required ordering and continuity hold across stream session resumes.
- `C0137` lock state: `C0137-PRD-STREAM-RESTART-CHECKPOINT`.
- `C0137` queue adjacency: hard dependency `I-0689 -> I-0690 -> I-0691`.

#### C0137 checkpoint restart contracts (`I-0690`)
- Required artifact path:
  - `.ralph/reports/I-0690-m96-s1-stream-restart-checkpoint-matrix.md`
- Required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `stream_transport_enabled`, `stream_restart`, `stream_boundary`, `checkpoint_key`, `checkpoint_persisted`, `checkpoint_loaded`, `checkpoint_start_id`, `checkpoint_end_id`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows:
  - `chain=solana`, `network=devnet`, `stream_transport_enabled=true`, `stream_restart=true`
  - `chain=base`, `network=sepolia`, `stream_transport_enabled=true`, `stream_restart=true`
  - `chain=btc`, `network=testnet`, `stream_transport_enabled=true`, `stream_restart=true`
- Required hard-stop checks:
  - `outcome=GO`
  - `evidence_present=true`
  - `checkpoint_persisted=true`
  - `checkpoint_loaded=true`
  - `stream_restart=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where required
- `outcome=NO-GO` rows must keep a non-empty `failure_mode`.

#### C0137 Decision Hook
- `DP-0186-C0137`: `C0137` remains blocked until required rows in `.ralph/reports/I-0690-m96-s1-stream-restart-checkpoint-matrix.md` for mandatory chains are present with the hard-stop checks above.

### C0138 (`I-0693`) implementation handoff addendum
- Focused PRD traceability:
  - `R5`: deterministic replay continuity under restart.
  - `R7`: chain/network state isolation.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `chain_adapter_runtime_wired`: runtime stream-boundary checkpoints stay deterministic across session restarts.
- `C0138` lock state: `C0138-PRD-STREAM-CHECKPOINT-SESSION-ISOLATION`.
- `C0138` queue adjacency: hard dependency `I-0693 -> I-0696 -> I-0697`.

#### C0138 session-isolated checkpoint contracts (`I-0696`)
- Required artifact path:
  - `.ralph/reports/I-0696-m96-s1-stream-session-checkpoint-matrix.md`
- Required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `stream_session_id`, `stream_boundary`, `checkpoint_key`, `checkpoint_key_scope_includes_session`, `checkpoint_key_legacy_read`, `checkpoint_key_new_write`, `checkpoint_persisted`, `checkpoint_loaded`, `checkpoint_start_id`, `checkpoint_end_id`, `stream_restart`, `legacy_compatibility_row_present`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows:
  - `chain=solana`, `network=devnet`, `stream_session_id=session-a`, `stream_restart=true`
  - `chain=base`, `network=sepolia`, `stream_session_id=session-a`, `stream_restart=true`
  - `chain=btc`, `network=testnet`, `stream_session_id=session-a`, `stream_restart=true`
- Required hard-stop checks for required `GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `checkpoint_key_scope_includes_session=true`
  - `checkpoint_key_legacy_read=true`
  - `checkpoint_key_new_write=true`
  - `checkpoint_persisted=true`
  - `checkpoint_loaded=true`
  - `stream_restart=true`
  - `legacy_compatibility_row_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where required.
- `outcome=NO-GO` rows require non-empty `failure_mode`.

#### C0138 Decision Hook
- `DP-0187-C0138`: `C0138` remains blocked until required rows in `.ralph/reports/I-0696-m96-s1-stream-session-checkpoint-matrix.md` for mandatory chains are present with the hard-stop checks above.

### C0139 (`I-0698`) implementation handoff addendum
- Focused PRD traceability:
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `cursor_monotonic`: stream restart behavior must preserve boundary ordering.
  - `chain_adapter_runtime_wired`: stream runtime determinism must include session identity defaults.
- `C0139` lock state: `C0139-PRD-STREAM-SESSION-ID-DEFAULT-DETERMINISM`.
- `C0139` queue adjacency: hard dependency `I-0698 -> I-0699 -> I-0700`.

#### C0139 stream session default contracts (`I-0699`)
- Required artifact path:
  - `.ralph/reports/I-0699-m96-s1-stream-session-default-matrix.md`
- Required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `stream_session_id`, `stream_boundary`, `checkpoint_key`, `checkpoint_persisted`, `checkpoint_loaded`, `checkpoint_start_id`, `checkpoint_end_id`, `stream_restart`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows:
  - `chain=solana`, `network=devnet`, `stream_session_id=default`, `stream_restart=true`
  - `chain=base`, `network=sepolia`, `stream_session_id=default`, `stream_restart=true`
  - `chain=btc`, `network=testnet`, `stream_session_id=default`, `stream_restart=true`
- Required hard-stop checks for required `GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `checkpoint_persisted=true`
  - `checkpoint_loaded=true`
  - `stream_restart=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where required.
- `outcome=NO-GO` rows must keep a non-empty `failure_mode`.

#### C0139 Decision Hook
- `DP-0188-C0139`: `C0139` remains blocked until required rows in `.ralph/reports/I-0699-m96-s1-stream-session-default-matrix.md` for mandatory chains are present with:
  - `stream_session_id=default`
  - `outcome=GO`
  - `evidence_present=true`
  - `checkpoint_persisted=true`
  - `checkpoint_loaded=true`
  - `stream_restart=true`
  - required hard-stop booleans (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`) true
  - `failure_mode` is empty
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where required.

### C0134 (`I-0678`) implementation handoff addendum
- Focus: PRD-traceable DB statement-timeout governance before optional refinements continue.
- Focused unresolved requirement traceability from `PRD.md`:
  - `R4`: deterministic replay.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `chain_adapter_runtime_wired`: chain adapter/normalizer runtime behavior remains deterministic under fail-safe constraints.
- C0134 lock state: `C0134-PRD-DB-STATEMENT-TIMEOUT-GOVERNANCE`.
- `C0134` queue adjacency: hard dependency `I-0678 -> I-0679 -> I-0680`.

#### Required evidence artifacts for `I-0679`
- `.ralph/reports/I-0679-m96-s1-db-query-timeout-matrix.md`

#### C0134 hard-stop contracts (`I-0679`)
- `.ralph/reports/I-0679-m96-s1-db-query-timeout-matrix.md` required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `statement_timeout_ms`, `timeout_override`, `applied_to_statement_session`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows:
  - `chain=solana`, `network=devnet`
  - `chain=base`, `network=sepolia`
  - `chain=btc`, `network=testnet`

#### C0134 hard-stop checks for required `GO` rows
- `outcome=GO`
- `evidence_present=true`
- `statement_timeout_ms` is parsed from config and non-negative.
- `applied_to_statement_session=true` for at least one mandatory-chain row where timeout is enforced.
- `canonical_event_id_unique_ok=true`
- `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
- `signed_delta_conservation_ok=true`
- `chain_adapter_runtime_wired_ok=true`
- `failure_mode` is empty.
- For required `NO-GO` rows, `failure_mode` is non-empty.

#### C0134 decision hook
- `DP-0183-C0134` remains blocked unless all required `I-0679-m96-s1-db-query-timeout-matrix.md` rows for `solana`, `base`, and `btc` are present with `outcome=GO`, `evidence_present=true`, required timeout governance checks above, and hard-stop booleans true.

### C0140 (`I-0702`) implementation handoff addendum
- Focused PRD traceability:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `R3`: chain-family fee completeness.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `chain_adapter_runtime_wired`: chain adapter and normalizer-runtime wiring remains deterministic across optional EVM alias targets.
- `C0140` lock state: `C0140-PRD-ETHEREUM-MAINNET-NORMALIZER-COMPATIBILITY`.
- `C0140` queue adjacency: hard dependency `I-0701 -> I-0702 -> I-0703`.

#### C0140 mandatory contracts (`I-0702`)
- Required artifact path:
  - `.ralph/reports/I-0702-m96-s1-ethereum-mainnet-normalizer-matrix.md`
- Required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `target_key`, `normalizer_family`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required row for C0140:
  - `chain=ethereum`
  - `network=mainnet`
  - `target_key=ethereum-mainnet`
  - `normalizer_family=base`
- Required hard-stop checks:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
- `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `base_fee_split_coverage_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
- `outcome=NO-GO` rows must have non-empty `failure_mode`.

#### C0140 decision hook
- `DP-0189-C0140`: `C0140` remains blocked until required rows in `.ralph/reports/I-0702-m96-s1-ethereum-mainnet-normalizer-matrix.md` for `chain=ethereum`, `network=mainnet` are present with:
  - `target_key=ethereum-mainnet`
  - `normalizer_family=base`
  - `outcome=GO`
  - `evidence_present=true`
  - required hard-stop booleans true (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `base_fee_split_coverage_ok`, `chain_adapter_runtime_wired_ok`)
  - `failure_mode` is empty

### C0141 (`I-0705`) implementation handoff addendum
- Focused PRD traceability:
  - `R1`: no-duplicate indexing.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `cursor_monotonic`: stream checkpoint replay must preserve boundary ordering.
  - `chain_adapter_runtime_wired`: checkpoint namespace scoping must be deterministic for restart behavior.
- `C0141` lock state: `C0141-PRD-STREAM-NAMESPACE-CHECKPOINT-SCOPE`.
- `C0141` queue adjacency: hard dependency `I-0704 -> I-0705 -> I-0706`.

#### C0141 mandatory contracts (`I-0705`)
- Required artifact path:
  - `.ralph/reports/I-0705-m96-s1-stream-namespace-checkpoint-key-matrix.md`
- Required row fields:
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `stream_namespace`, `stream_session_id`, `stream_boundary`, `checkpoint_key`, `checkpoint_key_scope_includes_namespace`, `checkpoint_key_scope_includes_session`, `checkpoint_key_legacy_read`, `checkpoint_key_new_write`, `checkpoint_persisted`, `checkpoint_loaded`, `checkpoint_start_id`, `checkpoint_end_id`, `stream_restart`, `legacy_compatibility_row_present`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows:
  - `chain=solana`, `network=devnet`, `stream_namespace=ns-a`, `stream_session_id=session-a`, `stream_restart=true`
  - `chain=base`, `network=sepolia`, `stream_namespace=ns-a`, `stream_session_id=session-a`, `stream_restart=true`
  - `chain=btc`, `network=testnet`, `stream_namespace=ns-a`, `stream_session_id=session-a`, `stream_restart=true`
- Required hard-stop checks for required `GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `checkpoint_key_scope_includes_namespace=true`
  - `checkpoint_key_scope_includes_session=true`
  - `checkpoint_persisted=true`
  - `checkpoint_loaded=true`
  - `stream_restart=true`
  - `legacy_compatibility_row_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where required
  - `failure_mode` is empty
- `outcome=NO-GO` rows must keep a non-empty `failure_mode`.

#### C0141 decision hook
- `DP-0190-C0141`: `C0141` remains blocked until required rows in `.ralph/reports/I-0705-m96-s1-stream-namespace-checkpoint-key-matrix.md` for `chain=solana`, `base`, and `btc` are present with:
  - `stream_namespace=ns-a`
  - `outcome=GO`
  - `evidence_present=true`
  - required hard-stop booleans above (`checkpoint_key_scope_includes_namespace`, `checkpoint_key_scope_includes_session`, `checkpoint_persisted`, `checkpoint_loaded`, `stream_restart`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`)
  - `failure_mode` is empty
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where required

### C0142 (`I-0708`) implementation handoff addendum
- Focused PRD traceability:
  - `R1`: no-duplicate indexing.
  - `R2`: full in-scope asset-volatility event coverage.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `cursor_monotonic`: replay ordering remains deterministic.
  - `chain_adapter_runtime_wired`: sidecar/decoder wiring remains deterministic under replay.
- `C0142` lock state: `C0142-PRD-SOLANA-SYSTEM-INSTRUCTION-DECODE-COVERAGE`.
- `C0142` queue adjacency: hard dependency `I-0707 -> I-0708 -> I-0709`.

#### C0142 implementation contracts (`I-0708`)
- Required artifact path:
  - `.ralph/reports/I-0708-m96-s1-solana-system-instruction-coverage-matrix.md`
  - `.ralph/reports/I-0708-m96-s2-solana-system-instruction-replay-matrix.md`
  - `.ralph/reports/I-0708-m96-s3-solana-system-isolation-matrix.md`
- Required row fields (`s1`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `class_path`, `peer_chain`, `evidence_present`, `outcome`, `failure_mode`, `notes`
- Required row fields (`s2`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required row fields (`s3`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows:
  - `chain=solana`, `network=devnet`, `class_path=TRANSFER`
  - `chain=solana`, `network=devnet`, `class_path=MINT`
  - `chain=solana`, `network=devnet`, `class_path=BURN`
  - `chain=solana`, `network=devnet`, `class_path=FEE`
- Required hard-stop checks for required `GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where required
  - `failure_mode` is empty
- `outcome=NO-GO` rows must keep a non-empty `failure_mode`.

#### C0142 decision hook
- `DP-0191-C0142`: `C0142` remains blocked until required rows in the three `I-0708` artifacts are present for all required `solana-devnet` rows with:
  - `outcome=GO`
  - `evidence_present=true`
  - required hard-stop booleans true (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`)
  - `failure_mode` empty for `GO` rows and non-empty for `NO-GO` rows.
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where required.

### C0144 (`I-0713`) implementation handoff addendum
- Focused PRD traceability:
  - `R1`: no-duplicate indexing.
  - `R4`: replay/recovery determinism under restart.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `cursor_monotonic`: replay ordering remains deterministic.
  - `chain_adapter_runtime_wired`: stream boundaries and runtime checkpoints remain deterministic.
- `C0144` lock state: `C0144-PRD-STREAM-BOUNDARY-RESTART-ISOLATION`.
- `C0144` queue adjacency: hard dependency `I-0713 -> I-0714 -> I-0715`.

#### C0144 implementation contracts (`I-0714`)
- Required artifact path:
  - `.ralph/reports/I-0714-m96-s1-stream-boundary-isolation-coverage-matrix.md`
  - `.ralph/reports/I-0714-m96-s2-stream-boundary-replay-isolation-matrix.md`
  - `.ralph/reports/I-0714-m96-s3-stream-boundary-peer-isolation-matrix.md`
- Required row fields (`s1`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `stream_transport_enabled`, `stream_namespace`, `stream_session_id`, `stream_boundary`, `peer_chain`, `evidence_present`, `outcome`, `failure_mode`, `notes`
- Required row fields (`s2`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `stream_transport_enabled`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required row fields (`s3`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows:
  - `chain=solana`, `network=devnet`, `peer_chain=base`, `stream_transport_enabled=true`, `stream_namespace=pipeline`, `stream_session_id=default`
  - `chain=base`, `network=sepolia`, `peer_chain=btc`, `stream_transport_enabled=true`, `stream_namespace=pipeline`, `stream_session_id=default`
  - `chain=btc`, `network=testnet`, `peer_chain=solana`, `stream_transport_enabled=true`, `stream_namespace=pipeline`, `stream_session_id=default`
- Required hard-stop checks for required `GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where present
  - `failure_mode` is empty
- `outcome=NO-GO` rows must keep a non-empty `failure_mode`.

#### C0144 decision hook
- `DP-0192-C0144`: `C0144` remains blocked until required rows in the three `I-0714` artifacts are present with:
  - required chains `solana`, `base`, and `btc`
  - `evidence_present=true`
  - hard-stop booleans true (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`)
  - `peer_cursor_delta=0` and `peer_watermark_delta=0` where present
  - `failure_mode` is empty for `GO` rows and non-empty for `NO-GO` rows.

### C0145 (`I-0716`) implementation handoff addendum
- Focused PRD traceability:
  - `R1`: no-duplicate indexing.
  - `R4`: deterministic replay.
  - `8.5`: failed-path cursor/watermark progression is prohibited.
  - `cursor_monotonic`: replay ordering must remain deterministic under signature overlap.
  - `chain_adapter_runtime_wired`: normalizer wiring and envelope ownership deterministic under adapter-family variance.
- `C0145` lock state: `C0145-PRD-FINALITY-METADATA-DETERMINISM`.
- `C0145` queue adjacency: hard dependency `I-0716 -> I-0717 -> I-0718`.

#### C0145 implementation contracts (`I-0717`)
- Required artifact path:
  - `.ralph/reports/I-0717-m96-s1-finality-metadata-resolution-matrix.md`
  - `.ralph/reports/I-0717-m96-s2-finality-replay-invariance-matrix.md`
  - `.ralph/reports/I-0717-m96-s3-finality-peer-isolation-matrix.md`
- Required row fields (`s1`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `finality_probe`, `class_path`, `peer_chain`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required row fields (`s2`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `permutation`, `finality_probe`, `class_path`, `peer_chain`, `canonical_id_count`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required row fields (`s3`):
  - `fixture_id`, `fixture_seed`, `run_id`, `chain`, `network`, `peer_chain`, `peer_cursor_delta`, `peer_watermark_delta`, `evidence_present`, `canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`, `outcome`, `failure_mode`
- Required rows for finality probes:
  - `chain=solana`, `network=devnet`, `finality_probe=mixed_metadata`
  - `chain=base`, `network=sepolia`, `finality_probe=mixed_metadata`
  - `chain=btc`, `network=testnet`, `finality_probe=fallback_default`
- Required hard-stop checks for `GO` rows:
  - `outcome=GO`
  - `evidence_present=true`
  - `canonical_event_id_unique_ok=true`
  - `replay_idempotent_ok=true`
  - `cursor_monotonic_ok=true`
  - `signed_delta_conservation_ok=true`
  - `chain_adapter_runtime_wired_ok=true`
  - `failure_mode` is empty
  - in `s2`, `canonical_id_count=1`
  - in `s3`, `peer_cursor_delta=0` and `peer_watermark_delta=0`
- For required `NO-GO` rows, `failure_mode` must be non-empty.

#### C0145 decision hook
- `DP-0193-C0145`: `C0145` remains blocked until required rows in:
  - `.ralph/reports/I-0717-m96-s1-finality-metadata-resolution-matrix.md`
  - `.ralph/reports/I-0717-m96-s2-finality-replay-invariance-matrix.md`
  - `.ralph/reports/I-0717-m96-s3-finality-peer-isolation-matrix.md`
  for `chain=solana`, `base`, and `btc` are present with:
  - `outcome=GO`
  - `evidence_present=true`
  - required hard-stop booleans true (`canonical_event_id_unique_ok`, `replay_idempotent_ok`, `cursor_monotonic_ok`, `signed_delta_conservation_ok`, `chain_adapter_runtime_wired_ok`)
  - required `finality_probe` values above
  - `failure_mode` is empty for `GO` rows and non-empty for `NO-GO` rows.
