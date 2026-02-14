# Chain Invariants v1

Ralph local automation treats the following invariants as first-class quality gates.
Every developer/qa issue should declare which invariants it preserves or improves.

## Canonical Invariants

- `canonical_event_id_unique`
  - Canonical event ids are deterministic and collision-safe for the same logical event.
- `replay_idempotent`
  - Re-running the same source range must not create duplicated persisted events.
- `cursor_monotonic`
  - Cursor progression must be monotonic except explicit rollback/reorg workflows.
- `solana_fee_event_coverage`
  - Solana transaction fee events are always represented as signed balance deltas.
- `base_fee_split_coverage`
  - Base execution fee(L2) and data fee(L1) are represented distinctly when metadata exists.
- `reorg_recovery_deterministic`
  - Reorg rollback/replay yields deterministic end-state for the same chain input.
- `chain_adapter_runtime_wired`
  - Declared chain adapters are wired in runtime orchestration path, not test-only code.

## Contract Rule

- Issue header `invariants:` must use a comma-separated subset of the canonical invariant ids.
- Unknown invariant ids are rejected by the contract gate.
