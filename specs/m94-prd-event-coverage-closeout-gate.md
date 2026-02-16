# M94 PRD-Priority Event-Coverage + Duplicate-Free Closeout Gate

## Scope
- Milestone: `M94`
- Execution slices: `M94-S1` (`I-0491`), `M94-S2` (`I-0492`), `M94-S3` (`I-0496`/`I-0497`)
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R1`: no-duplicate indexing.
- `R2`: full in-scope asset-volatility event coverage.
- `R3`: chain-family fee completeness.
- `9.4`: parity/continuity validation principles.
- `10`: deterministic replay and no cross-chain cursor bleed acceptance.

## Problem Statement
After `M93` continuity hardening, `solana-devnet` and `base-sepolia` had temporary `NA` entries for required `mint` and `burn` classes. This tranche replaces that debt with deterministic mint/burn evidence so `R2` cannot pass by omission.

## Coverage Contract
1. For each mandatory chain and class combination below, required cells must be present in evidence artifacts with deterministic `canonical event` outputs:
   - `solana-devnet`:
     - `transfer` (`TRANSFER`)
     - `fee` (`FEE`)
     - `mint` (`MINT`) evidence required
     - `burn` (`BURN`) evidence required
   - `base-sepolia`:
     - `transfer` (`TRANSFER`)
     - `fee_execution_l2` (`fee_execution_l2`)
     - `fee_data_l1` (`fee_data_l1`)
     - `mint` (`MINT`) evidence required
     - `burn` (`BURN`) evidence required
   - `btc-testnet`:
     - transfer-path coverage (`vin`/`vout`, `TRANSFER`)
     - miner-fee conservation assertions (no synthetic fee event; deterministic delta set remains signer-consistent).
2. `solana-devnet`, `base-sepolia`, and `btc-testnet` must remain duplicate-free under equivalent replay, ordering swaps, and restart permutations.
3. Mandatory-chain coverage evidence must be chain-isolated: no missing class outputs are treated as coverage acceptance for a chain/family in the required matrix.

Required evidence artifacts:
- `.ralph/reports/I-0491-m94-s1-event-class-matrix.md`
- `.ralph/reports/I-0491-m94-s1-duplicate-suppression-matrix.md`
- `.ralph/reports/I-0496-m94-s3-mint-burn-class-matrix.md`
- `.ralph/reports/I-0496-m94-s3-mint-burn-duplicate-suppression-matrix.md`

For each mandatory chain-family, the class-path matrix must be non-empty for every required non-`NA` cell and empty/`NA` only where explicitly permitted. Required evidence rows are keyed by `(chain, network, class_family, class_path, evidence_present)`.

- `transfer` row class_path examples:
  - `solana-devnet` -> `TRANSFER`
  - `base-sepolia` -> `TRANSFER`
  - `btc-testnet` -> `TRANSFER`
- `mint` row class_path examples:
  - `solana-devnet` -> `MINT`
  - `base-sepolia` -> `MINT`
  - `btc-testnet` -> `NA`
- `burn` row class_path examples:
  - `solana-devnet` -> `BURN`
  - `base-sepolia` -> `BURN`
  - `btc-testnet` -> `NA`
- fee rows:
  - `solana-devnet` -> `FEE`
  - `base-sepolia` -> `fee_execution_l2`, `fee_data_l1`
  - `btc-testnet` -> `btc_miner_fee`

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `solana_fee_event_coverage`
- `base_fee_split_coverage`
- `chain_adapter_runtime_wired`

## Measurable Exit Gates
1. `0` missing required cells in the mandatory chain/class matrix.
2. `0` duplicate canonical IDs across required coverage families.
3. `0` replay tuple/balance drift for coverage fixtures across restart/replay permutations.
4. `0` cross-chain control/cursor bleed in one-chain perturbation or replay counterexamples.
5. Validation commands pass: `make test`, `make test-sidecar`, `make lint`.

## Test Matrix
1. Class matrix:
   - by chain family (`solana`, `base`, `btc`) and required classes (`transfer`, `mint`, `burn`, fee classes), with explicit `NA` for unsupported mandatory-class families.
   - all non-`NA` matrix cells must be observed in evidence and must not be empty.
   - measurable gate: `missing_cells == 0` for all mandatory `(chain, network, class_path)` cells where class is required and not `NA`.
2. Duplicate suppression matrix:
   - replay and replay-order permutations that converge to one canonical output set per chain.
   - measurable gate: `max(duplicate_count_by_class_path) == 0`.
3. Replay continuity matrix:
   - committed-boundary resume and chain-failover perturbation; balances and canonical tuples remain stable.
4. Chain-isolation inventory matrix:
   - mandatory-chain/class evidence coverage required for `solana-devnet`, `base-sepolia`, `btc-testnet`.

## Decision Hook
- `DP-0105-M94`: required event-class matrix coverage is measured as `(chain, network, class, evidence_present, class_path)`; any missing non-`NA` cell fails the closeout gate.
