# PRD — Multi-Chain Asset-Volatility Indexer

- Version: `v2.3`
- Last updated: `2026-02-15`
- Initial mandatory chains: `solana-devnet`, `base-sepolia`, `btc-testnet`
- Target architecture families: `solana-like`, `evm-like`, `btc-like`

## 1. Product Goal
Build a production-grade multi-chain indexer that captures **all asset-volatility on-chain events** without duplicate indexing.

This product must:
- share implementation by chain family (`solana-like`, `evm-like`, `btc-like`) while keeping chain-specific semantics explicit,
- decouple logical runtime model from deployment topology (chain-per-deploy, family-per-deploy, or hybrid),
- keep deterministic normalization behavior and idempotent ingestion,
- support replay/recovery/reorg/finality semantics,
- provide QA-verifiable correctness for every indexed balance delta.

## 2. Scope

### 2.1 In Scope
- Family-shared adapter/normalizer contracts for:
  - Solana-like chains (account + instruction-path model),
  - EVM-like chains (account + log/call-path model),
  - BTC-like chains (UTXO model; runtime activation in this phase).
- Mandatory runtime targets for this phase:
  - Solana Devnet
  - Base Sepolia (OP Stack L2)
  - BTC Testnet
- Event classes that change account assets:
  - native token transfer delta
  - fungible token transfer delta (SPL, ERC-20)
  - mint/burn delta
  - fee delta
  - bridge/deposit/withdrawal delta when represented in transaction effects
- Deterministic normalizer and idempotent ingestion.
- Replay and recovery from persisted normalized artifacts.
- QA harness for no-duplicate and balance-consistency invariants.
- Topology-independent correctness: per-chain canonical outputs must remain equivalent across supported deployment topologies.

### 2.2 Out of Scope (for this PRD phase)
- Mainnet rollout
- NFT metadata enrichment
- Full protocol-specific interpretation for every DEX/DeFi contract at launch

## 3. Core Requirements

### R1. No-Duplicate Indexing
The system must never create duplicate balance delta rows for the same canonical event.

Mandatory controls:
- canonical event id generation in normalizer,
- deterministic ownership rules per chain family,
- DB unique constraints over canonical identity,
- reprocessing idempotency.

### R2. Full Asset-Volatility Coverage (within chain transaction effects)
For every indexed transaction, all balance-affecting deltas in scope must be normalized into explicit signed delta events (`+/-`).

### R3. Chain-/Family-Specific Fee Completeness
- Solana-like: include transaction fee debit from signer/payer balance.
- EVM-like (Base Sepolia in this phase): include
  - L2 execution fee,
  - L1 data/rollup fee (when receipt fields exist),
  - total fee debit from payer balance.
- BTC-like (BTC Testnet in this phase): include miner fee and explicit input/output delta conservation semantics.

### R4. Deterministic Replay
Re-running the same chain cursor range must produce identical canonical event IDs and zero net duplication.

### R5. Operational Continuity
Indexer must preserve no-loss continuity by fail-fast process restart semantics.
On processing failure, the runtime must terminate immediately (`panic`) and rely on restart replay from the last committed cursor/watermark boundary.

### R6. Deployment Topology Independence
For identical chain input, canonical output equivalence must hold regardless of deployment shape:
- chain-per-deployment,
- family-per-deployment,
- hybrid/mixed deployment.

### R7. Strict Chain Isolation
State progression (cursor, watermark, commit outcome) must be isolated per `chain + network`, even when multiple chains share one process/pod.

### R8. Fail-Fast Error Contract (No Silent Progress)
- Any stage-level processing error that could affect correctness must trigger immediate process abort (`panic`).
- In-process skip/continue behavior for failed batches is prohibited.
- Cursor/watermark must not advance on failed path before process abort.
- Recovery is achieved by deterministic replay after restart, not by best-effort continuation in the same process.

### R9. Chain-Scoped Adaptive Throughput Control (Auto-Tune)
- Auto-tune (if enabled) may adjust throughput knobs only within a single `ChainRuntime` (for example: coordinator batch size/tick interval, fetch concurrency).
- Auto-tune inputs must be chain-scoped metrics only (for example: chain-local lag, channel depth, RPC error budget, DB commit latency).
- Cross-chain coupled control is prohibited; one chain's lag/error must never directly throttle or accelerate another chain.
- Auto-tune must never weaken fail-fast safety: correctness-impacting errors still trigger immediate process abort (`panic`).

## 4. Normalizer Architecture (Most Critical)

## 4.1 Canonical Normalized Event Envelope
Every normalized event must include:
- `event_id` (deterministic hash key)
- `chain_family` (`solana-like`, `evm-like`, `btc-like`)
- `chain` (e.g., `solana`, `base`)
- `network` (e.g., `devnet`, `sepolia`)
- `block_cursor` (slot/height/chain-specific cursor)
- `block_hash`
- `tx_hash` (or equivalent canonical transaction identity)
- `tx_index`
- `event_path`:
  - Solana-like: `outer_instruction_index`, `inner_instruction_index`, `account_index`
  - EVM-like: `log_index` or call-trace index
  - BTC-like: `vin/vout index` + script path
- `actor_address`
- `asset_type` (`native`, `fungible_token`, `fee`, `utxo`)
- `asset_id` (mint/contract/native symbol id)
- `delta` (signed decimal string)
- `event_category` (`transfer`, `mint`, `burn`, `fee_execution_l2`, `fee_data_l1`, ...)
- `finality_state` (`pending`, `finalized`)
- `decoder_version`
- `schema_version`

## 4.2 Canonical Event ID Rules
`event_id` must be generated from stable identity fields only:
- chain/network
- tx_hash/signature/txid
- instruction/log/utxo path
- actor address
- asset id
- category

Do not include mutable fields (timestamps from external systems, ingestion time, retry count).

## 4.3 Dedup Strategy
- L1: Normalizer ownership rule
  - Solana-like: outer instruction owner claims inner CPI events.
  - EVM-like: one effect source per log/call index.
  - BTC-like: one canonical owner per input/output effect path.
- L2: Plugin dispatch exclusivity
  - only one plugin may emit an event per canonical path key.
- L3: Ingestion unique key
  - DB unique on canonical identity (`event_id` or equivalent deterministic tuple).
- L4: Replay idempotency
  - upsert/ignore duplicate on conflict.

## 4.4 Plugin/Adapter Contract
Normalizer plugins and chain adapters must output canonical envelopes, not chain-specific ad hoc rows.

Each plugin/adapter requires:
- deterministic input -> deterministic output,
- no random/source-time fields,
- conflict-free canonical path emission,
- strict chain-family schema compatibility checks at runtime startup.

## 5. Chain Family Semantics

## 5.1 Solana-like (active in this phase)
- Cursor: slot
- Final identity: signature + instruction path
- Fee:
  - transaction fee debit from payer (lamports)
  - record as `asset_type=fee`, `event_category=fee_execution_l1`

## 5.2 EVM-like (active in this phase via Base Sepolia)
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

## 5.3 BTC-like (active in this phase via BTC Testnet)
- Cursor: block height + tx index
- Final identity: txid + vin/vout path
- Fee:
  - explicit miner fee attribution from input/output difference
  - deterministic handling for coinbase and script-type specific ownership

## 6. Data Model Requirements

Tables must support:
- raw transactions (`transactions`)
- normalized signed deltas (`balance_events`)
- materialized balances (`balances`)
- cursor/finality state (`cursors`)

Required constraints:
- canonical uniqueness (`UNIQUE(event_id)` or equivalent deterministic composite key including `chain` + `network`)
- deterministic cursor progression checks per `chain + network`
- no mutable shared cursor/watermark row across different chains

## 7. Runtime and Deployment Best Practice

## 7.1 Logical Runtime Unit
Each `ChainRuntime` executes:
1. Coordinator (chain-aware cursor jobs)
2. Fetcher (RPC pull)
3. Normalizer (sidecar decode + canonical envelope generation)
4. Ingester (idempotent write)
5. Finalizer (where applicable)

`ChainRuntime` is the correctness boundary; deployment packaging can vary.

## 7.2 Family-Sharing Strategy
- Share implementation at family layer (decoder contracts, canonicalization utilities, retry/reorg framework).
- Keep chain profile overlays for RPC methods, fee fields, finality rules, and cursor semantics.

## 7.3 Deployment Topologies
- `Topology B (recommended default)`: family-per-deployment (`like-group`) for baseline operational efficiency and implementation sharing.
- `Topology A`: chain-per-deployment for high-SLO or high-traffic chains that need strict fault isolation and dedicated scaling ownership.
- `Topology C`: hybrid (some chains dedicated, others grouped) for cost/SLO balancing.

## 7.4 Commit Scheduling Policy
- Default policy: commit scheduling is chain-scoped per `ChainRuntime`.
- Cross-chain shared commit scheduler is not part of the production runtime contract.
- Cursor/watermark progression must remain strictly chain-scoped with no cross-chain progression dependency.

## 7.5 Kubernetes Guidance
- One reconciliation loop per chain regardless of pod grouping.
- Horizontal scaling decisions must be chain-aware (lag, error budget, RPC quota).
- Pod grouping decisions must be reversible without changing canonical output semantics.

## 7.6 Backpressure and Auto-Tune Guardrails
- Buffered channel backpressure remains the baseline safety mechanism for bounded in-process flow control.
- Auto-tune is an additive control plane, not a correctness boundary; disabling it must not change canonical output semantics.
- Auto-tune actions must be auditable via per-chain diagnostics (input metrics, selected knobs, effective limits).
- If control input is ambiguous or stale, runtime must prefer deterministic safe fallback and preserve fail-fast behavior.

## 8. Failure and Recovery

## 8.1 Transient RPC/Decoder Failure
- do not silently continue in-process on failed batch,
- do not advance committed cursor until normalized+ingested success,
- trigger immediate process abort (`panic`) on failure path that cannot prove deterministic committed state,
- rely on orchestrator restart + deterministic replay from last committed cursor.

## 8.2 Reorg / Canonicality Drift
- detect block hash mismatch at same cursor,
- rollback from fork cursor,
- replay deterministically.

## 8.3 Replay from Normalized Backup
- persistent normalized batch storage must allow exact range replay,
- replay must preserve `event_id` determinism.

## 8.4 Topology Migration Safety
Switching a chain between topology modes (dedicated <-> grouped) must not require data repair and must preserve cursor monotonicity.

## 8.5 Fail-Fast Safety Guarantees
- Failure injection at each stage boundary (coordinator/fetcher/normalizer/ingester) must show:
  - immediate process abort (`panic`),
  - no cursor/watermark advancement on failed chain path,
  - deterministic replay convergence after restart with no duplicate/missing canonical events.

## 9. QA Strategy

## 9.1 Invariant Tests
- No duplicate event IDs after replay.
- Sum of per-tx deltas must match transaction effect semantics.
- Fee deltas must be present for every successful transaction with fee burn/debit.
- Stage failure injection triggers fail-fast abort before unsafe progression.

## 9.2 Golden Dataset Tests
- fixed Solana sample tx set
- fixed Base sample tx set including rollup fee-bearing transactions
- fixed BTC sample tx set including coinbase + multi-input/output fee semantics
- expected canonical normalized events snapshot

## 9.3 Cross-Run Determinism
Same chain input range across two independent runs must produce identical ordered `(event_id, delta, category)` tuples.

## 9.4 Topology Parity Tests
For the same chain fixture, `Topology A/B/C` executions must converge to identical per-chain canonical tuple outputs and cursor/watermark end-state.

## 10. Acceptance Criteria

The PRD is considered implemented when:
- scoped chains index concurrently with chain-scoped correctness guarantees,
- all scoped asset-volatility events are normalized into canonical signed deltas,
- duplicate indexing is prevented by design and verified by QA,
- Base L2 execution fee + L1 data fee are modeled as fee deltas,
- BTC input/output/fee semantics are modeled as deterministic signed deltas on BTC Testnet,
- replay/recovery tests pass,
- fail-fast failure-injection tests prove panic-on-error with no unsafe cursor progression,
- topology parity tests pass (chain-per-deploy vs family-per-deploy vs hybrid),
- auto-tune behavior (when enabled) is chain-scoped, auditable, and preserves fail-fast safety invariants,
- QA report confirms deterministic output, no duplicate rows, and no cross-chain cursor bleed.

## 11. Milestones

- M0: family abstraction + deployment topology decoupling foundation
- M1: canonical normalizer contract + event_id + DB uniqueness
- M2: Solana-like fee/delta completeness hardening
- M3: EVM-like fee decomposition (execution + data fee) and normalization
- M4: replay/reorg deterministic recovery
- M5: fail-fast panic safety contract hardening (no silent progression)
- M6: BTC-like runtime activation (btc-testnet adapter + normalize + ingest + invariant gate)
- M7: topology parity QA gate + release readiness
- M8: chain-scoped auto-tune control loop hardening (lag-aware adaptive throughput with fail-fast invariants)

## 12. Ralph Loop Execution Contract (Local)
- Planner agent updates specs/implementation plan from this PRD.
- Developer agent implements milestone slices in small commits.
- QA agent enforces invariant, fail-fast, golden, and topology-parity checks.
- Loop runs continuously in local daemon mode until manually stopped.
