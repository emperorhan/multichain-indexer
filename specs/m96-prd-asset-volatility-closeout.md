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
