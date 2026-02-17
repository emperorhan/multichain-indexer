# M99 PRD Runtime-Wire Topology Continuity Gate

## Scope
- Milestone: `M99`
- Execution slice: `C0103`
- Mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`

## PRD Traceability
- `R6`: deployment topology independence.
- `R7`: strict chain isolation.
- `10`: deterministic replay acceptance and one-chain perturbation peer isolation.
- `chain_adapter_runtime_wired`: chain adapter wiring must remain invariant under topology and perturbation.

## Problem Statement
After `C0102` class/fee revalidation, topology/runtime wiring parity remains PRD-critical but not explicitly re-closed as a hard-stop before optional refinements. This slice creates an explicit evidence contract for one-chain perturbation and adapter topology coupling checks across mandatory chains.

## C0103 Revalidation Contract (`I-0562 -> I-0565 -> I-0566`)
- Tranche: `C0103`.
- hard-stop required: `true`.
- dependency order: `I-0562` -> `I-0565` -> `I-0566`.
- Focus:
  - same mandatory chains as the mandatory contract set
  - topology family parity (`Topology A/B/C`) under equivalent fixture ranges
  - one-chain perturbation isolation on restart/continuity rows
- lock state: `C0103-PRD-RUNTIME-WIRE-REVALIDATION`.
- Hard-stop output artifacts for `I-0565`:
  - `.ralph/reports/I-0565-m99-s1-runtime-wiring-matrix.md`
  - `.ralph/reports/I-0565-m99-s2-one-chain-adapter-coupling-isolation-matrix.md`
- PRD traceability binding for hard-stop proof:
  - `R6`: deployment topology independence.
  - `R7`: strict chain isolation.
  - `10`: deterministic replay and one-chain perturbation acceptance.
  - `chain_adapter_runtime_wired`: adapter wiring remains invariant under topology and perturbation.

## Matrix Contract (M99-C0103)

### Runtime wiring matrix (`I-0565-m99-s1-runtime-wiring-matrix.md`)
Required matrix fields (machine-checkable):
- `fixture_id` (string, required)
- `fixture_seed` (string, required)
- `run_id` (string, required)
- `chain` (string, required; required values: `solana`, `base`, `btc`)
- `network` (string, required; required values: `devnet`, `sepolia`, `testnet`)
- `topology_mode` (string, required; required values: `A`, `B`, `C`)
- `permutation` (string, required)
- `class_path` (string, required)
- `canonical_event_id_unique_ok` (bool, required)
- `replay_idempotent_ok` (bool, required)
- `cursor_monotonic_ok` (bool, required)
- `signed_delta_conservation_ok` (bool, required)
- `chain_adapter_runtime_wired_ok` (bool, required)
- `evidence_present` (bool, required)
- `outcome` (enum, required; `GO` or `NO-GO`)
- `failure_mode` (string, required; empty string only for `GO`)
- `notes` (string, optional)

Required matrix constraints for `I-0565`:
- Mandatory chains: `solana`/`devnet`, `base`/`sepolia`, `btc`/`testnet`.
- Mandatory topology coverage: each mandatory chain must have topology modes `A`, `B`, and `C` in required rows.
- `outcome` is `GO` or `NO-GO`.
- For required `outcome=GO` rows:
  - all gate fields listed above are `true`
  - `evidence_present=true`
  - `failure_mode` empty
- For required `outcome=NO-GO` rows:
  - `failure_mode` is non-empty
  - `evidence_present` may be `false` if execution is blocked.

### Isolation matrix (`I-0565-m99-s2-one-chain-adapter-coupling-isolation-matrix.md`)
Required matrix fields (machine-checkable):
- `fixture_id` (string, required)
- `fixture_seed` (string, required)
- `run_id` (string, required)
- `chain` (string, required; required values: `solana`, `base`, `btc`)
- `network` (string, required; required values: `devnet`, `sepolia`, `testnet`)
- `peer_chain` (string, required; required values: `solana`, `base`, `btc`)
- `topology_mode` (string, required; required values: `A`, `B`, `C`)
- `peer_cursor_delta` (integer, required)
- `peer_watermark_delta` (integer, required)
- `evidence_present` (bool, required)
- `outcome` (enum, required; `GO` or `NO-GO`)
- `failure_mode` (string, required; empty string only for `GO`)
- `notes` (string, optional)

Required matrix constraints for `I-0565`:
- Mandatory chains: `solana`/`devnet`, `base`/`sepolia`, `btc`/`testnet`.
- `outcome` is `GO` or `NO-GO`.
- For required `outcome=GO` rows:
  - `evidence_present=true`
  - `peer_cursor_delta=0`
  - `peer_watermark_delta=0`
  - `failure_mode` empty
- For required `outcome=NO-GO` rows:
  - `failure_mode` is non-empty

## Measurable Exit Gates
1. `0` missing required C0103 evidence rows for all mandatory chains (`solana-devnet`, `base-sepolia`, `btc-testnet`) and all required topology modes (`A/B/C`) in both artifacts.
2. For required C0103 runtime rows, `GO` outcomes require:
   - `evidence_present=true`
   - all required invariants listed as `true`
   - `failure_mode` empty
3. For required C0103 isolation rows, `GO` outcomes require:
   - `evidence_present=true`
   - `peer_cursor_delta=0`
   - `peer_watermark_delta=0`
   - `failure_mode` empty
4. `NO-GO` rows must provide non-empty `failure_mode`.
5. Validation remains unchanged and must pass: `make test`, `make test-sidecar`, `make lint`.

## Invariants
- `canonical_event_id_unique`
- `replay_idempotent`
- `cursor_monotonic`
- `signed_delta_conservation`
- `chain_adapter_runtime_wired`

## C0103 decision hook
- `DP-0120-C0103`: C0103 remains blocked if any mandatory-chain required row in either artifact is missing, reports `outcome=NO-GO`, has `evidence_present=false`, any required invariant gate false, or required peer deltas are non-zero.
