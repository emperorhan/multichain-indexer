# PRD — Multi-Chain Asset-Volatility Indexer

- Version: `v2.0`
- Last updated: `2026-02-13`
- Target runtime: `solana-devnet`, `base-sepolia`

## 1. Product Goal
Build a production-grade multi-chain indexer that captures **all asset-volatility on-chain events** without duplicate indexing.

This product must:
- run both chains concurrently in one operational model,
- keep deterministic normalization behavior,
- support replay/recovery/reorg/finality semantics,
- provide QA-verifiable correctness for every indexed balance delta,
- include L2-specific fee semantics for Base Sepolia (execution fee + rollup data fee).

## 2. Scope

### 2.1 In Scope
- Chain support:
  - Solana Devnet
  - Base Sepolia (OP Stack L2)
- Event classes that change account assets:
  - native token transfer delta
  - fungible token transfer delta (SPL, ERC-20)
  - mint/burn delta
  - fee delta
  - bridge/deposit/withdrawal delta when represented in transaction effects
- Deterministic normalizer and idempotent ingestion.
- Replay and recovery from persisted normalized artifacts.
- QA harness for no-duplicate and balance-consistency invariants.

### 2.2 Out of Scope (for this PRD phase)
- Mainnet rollout
- NFT metadata enrichment
- Full protocol-specific interpretation for every DEX/DeFi contract at launch

## 3. Core Requirements

### R1. No-Duplicate Indexing
The system must never create duplicate balance delta rows for the same canonical event.

Mandatory controls:
- canonical event id generation in normalizer,
- deterministic ownership rules per chain,
- DB unique constraints over canonical identity,
- reprocessing idempotency.

### R2. Full Asset-Volatility Coverage (within chain transaction effects)
For every indexed transaction, all balance-affecting deltas in scope must be normalized into explicit signed delta events (`+/-`).

### R3. Chain-Specific Fee Completeness
- Solana: include transaction fee debit from signer balance.
- Base Sepolia: include
  - L2 execution fee,
  - L1 data/rollup fee (when receipt fields exist),
  - total fee debit from payer balance.

### R4. Deterministic Replay
Re-running the same cursor range must produce identical canonical event IDs and zero net duplication.

### R5. Operational Continuity
Indexer must recover from transient RPC/decoder failures and continue processing without manual data repair.

## 4. Normalizer Architecture (Most Critical)

## 4.1 Canonical Normalized Event Envelope
Every normalized event must include:
- `event_id` (deterministic hash key)
- `chain` (`solana` or `base`)
- `network` (`devnet` or `sepolia`)
- `block_cursor` (slot/height)
- `block_hash`
- `tx_hash` (or Solana signature)
- `tx_index`
- `event_path`:
  - Solana: `outer_instruction_index`, `inner_instruction_index`, `account_index`
  - Base: `log_index` or call-trace index
- `actor_address`
- `asset_type` (`native`, `fungible_token`, `fee`)
- `asset_id` (mint/contract/native symbol id)
- `delta` (signed decimal string)
- `event_category` (`transfer`, `mint`, `burn`, `fee_execution_l2`, `fee_data_l1`, ...)
- `finality_state` (`pending`, `finalized`)
- `decoder_version`
- `schema_version`

## 4.2 Canonical Event ID Rules
`event_id` must be generated from stable identity fields only:
- chain/network
- tx_hash/signature
- instruction/log path
- actor address
- asset id
- category

Do not include mutable fields (timestamps from external systems, ingestion time, retry count).

## 4.3 Dedup Strategy
- L1: Normalizer ownership rule
  - Solana: outer instruction owner claims inner CPI events.
  - Base: one effect source per log/call index.
- L2: Plugin dispatch exclusivity
  - only one plugin may emit an event per canonical path key.
- L3: Ingestion unique key
  - DB unique on `event_id`.
- L4: Replay idempotency
  - upsert/ignore duplicate on conflict.

## 4.4 Plugin Contract
Normalizer plugins must output canonical envelopes, not chain-specific ad hoc rows.

Each plugin requires:
- deterministic input -> deterministic output,
- no random/source-time fields,
- conflict-free `event_path` emission.

## 5. Chain Semantics

## 5.1 Solana Devnet
- Cursor: slot
- Final identity: signature + instruction path
- Fee:
  - transaction fee debit from payer (lamports)
  - record as `asset_type=fee`, `event_category=fee_execution_l1`

## 5.2 Base Sepolia (L2)
- Cursor: block height
- Final identity: tx hash + log/call path
- Fee components:
  - `execution_fee_l2 = gas_used * effective_gas_price`
  - `data_fee_l1` from receipt extensions when provided by endpoint/client
  - `total_fee = execution_fee_l2 + data_fee_l1`
- Fee events:
  - one event for L2 execution fee
  - one event for L1 data fee (if available)
  - both debiting payer native balance deltas

## 6. Data Model Requirements

Tables must support:
- raw transactions (`transactions`)
- normalized signed deltas (`balance_events`)
- materialized balances (`balances`)
- cursor/finality state (`cursors`)

Required constraints:
- `UNIQUE(event_id)` on normalized event storage
- deterministic cursor progression checks per chain/network

## 7. Runtime and Pipeline

Stages:
1. Coordinator (chain-aware cursor jobs)
2. Fetcher (RPC pull)
3. Normalizer (sidecar decode + canonical envelope generation)
4. Ingester (idempotent write)
5. Finalizer (where applicable)

Rules:
- chain-specific workers, shared framework
- per-chain backpressure and retry control
- strict separation of fetch and normalize concerns

## 8. Failure and Recovery

## 8.1 Transient RPC/Decoder Failure
- retry with backoff,
- keep job in queue,
- do not advance committed cursor until normalized+ingested success.

## 8.2 Reorg / Canonicality Drift
- detect block hash mismatch at same cursor,
- rollback from fork cursor,
- replay deterministically.

## 8.3 Replay from Normalized Backup
- persistent normalized batch storage must allow exact range replay,
- replay must preserve `event_id` determinism.

## 9. QA Strategy

## 9.1 Invariant Tests
- No duplicate event IDs after replay.
- Sum of per-tx deltas must match transaction effect semantics.
- Fee deltas must be present for every successful transaction with fee burn/debit.

## 9.2 Golden Dataset Tests
- fixed Solana sample tx set
- fixed Base sample tx set including rollup fee-bearing transactions
- expected canonical normalized events snapshot

## 9.3 Cross-Run Determinism
Same input range across two independent runs must produce identical ordered `(event_id, delta, category)` tuples.

## 10. Acceptance Criteria

The PRD is considered implemented when:
- both chains index concurrently,
- all scoped asset-volatility events are normalized into canonical signed deltas,
- duplicate indexing is prevented by design and verified by QA,
- Base L2 execution fee + L1 data fee are modeled as fee deltas,
- replay/recovery tests pass,
- QA report confirms deterministic output and no duplicate rows.

## 11. Milestones

- M1: Canonical normalizer contract + event_id + DB uniqueness
- M2: Solana fee/delta completeness hardening
- M3: Base fee decomposition (execution + data fee) and normalization
- M4: Replay/reorg deterministic recovery
- M5: QA goldens + invariant gates + release readiness

## 12. Ralph Loop Execution Contract (Local)
- Planner agent updates specs/implementation plan from this PRD.
- Developer agent implements milestone slices in small commits.
- QA agent enforces invariant and golden checks.
- Loop runs continuously in local daemon mode until manually stopped.
