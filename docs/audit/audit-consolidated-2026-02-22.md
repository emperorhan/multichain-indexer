# Consolidated Audit Report — 2026-02-22

Cross-verified against codebase. 12 confirmed issues fixed; 4 false positives documented.
This file now also includes a supplemental pipeline efficiency/data-integrity re-audit with open risks.
DB schema unnecessary-structure audit is documented separately in `docs/audit/db-schema-audit-2026-02-22.md`.

---

## Confirmed Issues (12)

| ID | Severity | Category | Summary | Status |
|----|----------|----------|---------|--------|
| AUD-C-01 | Critical | Config | Helm REDIS_URL env var referencing removed dependency | **Fixed** |
| AUD-C-02 | Critical | Concurrency | Block-scan address cache RLock→Lock race window | **Fixed** |
| AUD-C-03 | Critical | Startup | No preflight connectivity validation (DB, RPC) | **Fixed** |
| AUD-H-01 | High | Security | Admin audit log leaks request body content | **Fixed** |
| AUD-H-02 | High | Security | X-Forwarded-For trusted without proxy validation | **Fixed** |
| AUD-H-03 | High | Security | Dashboard served without authentication | **Fixed** |
| AUD-M-01 | Medium | Infra | NetworkPolicy allows unrestricted egress | **Fixed** |
| AUD-M-02 | Medium | Integrity | Negative balance clamped to zero silently | **Fixed** |
| AUD-M-03 | Medium | Integrity | Reorg 가능 체인의 기본 finality가 과도하게 낙관적일 수 있음 | **Fixed** |
| AUD-M-04 | Medium | Reliability | Pipeline exits on error instead of auto-restarting | **Fixed** |
| AUD-M-05 | Medium | Security | Example config defaults to sslmode=disable, TLS off | **Fixed** |
| AUD-M-06 | Medium | Reliability | Health check timeout too aggressive (3s) | **Fixed** |

---

## Issue Details & Fixes

### AUD-C-01: Helm REDIS_URL Reference

**File:** `deployments/helm/multichain-indexer/templates/deployment.yaml`
**Problem:** REDIS_URL env var block remained after Redis was fully removed from the codebase.
**Fix:** Deleted the REDIS_URL env var block (lines 64-68) and updated NOTES.txt.

### AUD-C-02: Block-Scan Address Cache Race

**File:** `internal/pipeline/ingester/ingester.go`
**Problem:** `getBlockScanAddrMap` had a gap between RUnlock and Lock where another goroutine could trigger a duplicate DB fetch.
**Fix:** Applied double-check locking: re-validate cache under write-lock before fetching from DB.

### AUD-C-03: Preflight Connectivity

**File:** `cmd/indexer/main.go`
**Problem:** Pipeline started without verifying DB or RPC endpoints were reachable, causing delayed error discovery.
**Fix:** Added `preflightConnectivity()` after `validateRuntimeWiring()` — pings DB and calls `GetHeadSequence` for each chain adapter with 5s timeout.

### AUD-H-01: Audit Log Body Leak

**File:** `internal/admin/audit.go`
**Problem:** `AuditMiddleware` logged up to 1KB of request body content, which could include sensitive data (API keys, addresses, credentials).
**Fix:** Replaced `body_summary` with `body_size` (integer). Body content is no longer logged.

### AUD-H-02: X-Forwarded-For Spoofing

**File:** `internal/admin/ratelimit.go`
**Problem:** `extractClientIP` unconditionally trusted X-Forwarded-For and X-Real-IP headers, allowing any client to spoof their IP for rate-limiting bypass.
**Fix:** Only trust proxy headers when `r.RemoteAddr` is a private/loopback IP (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8, ::1, fc00::/7).

### AUD-H-03: Dashboard Without Auth

**File:** `internal/admin/server.go`
**Problem:** `/dashboard/` routes were served without `authMiddleware`, exposing operational data to unauthenticated users.
**Fix:** Wrapped both `/dashboard/` and `/dashboard` handlers with `s.authMiddleware()`.

### AUD-M-01: NetworkPolicy Unrestricted Egress

**File:** `deployments/helm/multichain-indexer/templates/networkpolicy.yaml`
**Problem:** Egress rule was `- {}` (allow all), defeating the purpose of the NetworkPolicy.
**Fix:** Restricted egress to DNS (53/UDP+TCP), PostgreSQL (5432-5433/TCP), gRPC sidecar (50051/TCP), and HTTPS (443/TCP). Also set `networkPolicy.enabled: true` as default in values.yaml.

### AUD-M-02: Negative Balance Silent Clamping

**File:** `internal/pipeline/ingester/ingester.go`
**Problem:** Negative balances were silently clamped to "0", hiding potential double-spend or accounting errors.
**Fix:** Negative balance now logs at ERROR level and sets `BalanceApplied = false`. The balance event is still recorded for forensic analysis, but no balance state mutation occurs. Manual investigation and reprocessing is required.

### AUD-M-03: Finality Default Too Optimistic

**File:** `internal/pipeline/normalizer/normalizer.go`
**Problem:** 기존 기본값이 지나치게 강한 finality로 귀결될 경우, **reorg가 발생할 수 있는 체인(EVM/BTC 등)**에서 조기 확정 반영 위험이 있었다.  
결론적으로 이 리스크는 모든 체인 공통이 아니라 reorg 가능 체인에 한정된다.
**Fix:** Solana는 `"finalized"` 기본값을 유지하고, reorg 가능 체인은 메타데이터 기반 최종성 신호가 확인되기 전 `"confirmed"`를 기본값으로 사용하도록 정리했다.

### AUD-M-04: Pipeline No Auto-Restart

**File:** `internal/pipeline/pipeline.go`
**Problem:** Pipeline `Run()` returned immediately on error, requiring external restart (process manager or main.go logic).
**Fix:** Added exponential backoff restart (1s → 2s → 4s → ... → 5min cap). Counter resets on success. Only `context.Canceled` causes return. Added `consecutiveFailures` field and `calcRestartBackoff()` method.

### AUD-M-05: Example Config Insecure Defaults

**File:** `configs/config.example.yaml`
**Problem:** Default DB URL used `sslmode=disable` and sidecar TLS was `false`, which could be copied to production.
**Fix:** Changed to `sslmode=require` and `tls_enabled: true` with comments noting local dev override.

### AUD-M-06: Health Check Timeout

**File:** `cmd/indexer/main.go`
**Problem:** 3-second health check timeout caused false failures under load or cross-region DB connections.
**Fix:** Increased to 10 seconds.

---

## False Positives (4)

| ID | Claimed Issue | Reason for Dismissal |
|----|---------------|---------------------|
| FP-01 | "Missing DB statement timeout" | Already configured via `db.statement_timeout_ms` in config (default 30s), applied in `initDatabase()` |
| FP-02 | "No circuit breaker on RPC calls" | Circuit breaker already implemented in `internal/circuitbreaker/breaker.go`, integrated into Fetcher and Normalizer stages |
| FP-03 | "Ingester lacks retry on DB write failure" | Ingester has configurable retry with exponential backoff (IngesterStageConfig.RetryMaxAttempts) |
| FP-04 | "No graceful shutdown timeout" | Shutdown timeout already exists: `pipeline.ShutdownTimeoutSec` config with `os.Exit(1)` fallback in main.go |

---

## Supplemental Re-Audit — Pipeline Efficiency & Integrity (2026-02-22 PM)

### Scope

- Pipeline stage split validity: `coordinator -> fetcher -> normalizer -> ingester -> optional finalizer`
- Throughput efficiency under block-scan workload
- Memory efficiency under large batch payloads
- Data integrity (no-loss/no-dup, balance consistency)
- Maintainability and context-token efficiency for operators/developers

### Overall Assessment

- Stage split itself is mostly reasonable; each stage has a distinct operational boundary (scheduling, RPC fetch, decode/normalization, DB commit).
- `finalizer` is already conditional and only runs when the adapter is reorg-aware (not universal to all chains): `internal/pipeline/pipeline.go:672`.
- All Critical/High pipeline issues have been resolved. No remaining blockers for 금융권 요구사항(no-loss/no-dup, stable throughput).

### Findings (5 fixed, 1 resolved, 1 mitigated)

| ID | Severity | Area | Summary | Evidence | Status |
|----|----------|------|---------|----------|--------|
| AUD-P-01 | Critical | Progress/Liveness | Empty block ranges can stall watermark progression indefinitely, repeatedly rescanning the same range and risking missed forward progress. | Fetcher emits empty sentinel RawBatch; normalizer passes through without gRPC call; ingester advances watermark. | **Fixed** |
| AUD-P-02 | High | Throughput/Memory | Block-scan path builds and decodes very large single batches without tx-count/byte chunking, creating memory spikes and timeout/restart risk. | Fetcher `BlockScanMaxBatchTxs` config (default 500); splits large batches into sub-batches with cursor-safe progression. 4 new tests. | **Fixed** |
| AUD-P-03 | High | Throughput | Deterministic commit interleaver can inject up to 250ms wait per acquire when preferred chain is absent/idle, reducing effective commit throughput. | `InterleaveMaxSkewMs` config field (default 250); auto-disabled for single-chain deployments (`len(targets) <= 1`). | **Fixed** |
| AUD-P-04 | Medium | Queue Efficiency | Coordinator can enqueue duplicate same-range jobs before watermark advances, wasting fetch/normalize resources. | `hasEnqueued`/`lastEnqueuedStart`/`lastEnqueuedEnd` tracking; dedup gated on `configRepo != nil`. | **Fixed** |
| AUD-P-05 | Medium | Benchmark Reliability | Fetcher hot-path benchmark currently panics (nil adaptive cache), so key throughput baseline is missing. | `batchSizeByAddress` LRU initialized in `BenchmarkFetchSignatures` and `BenchmarkFetchTransactions`. | **Fixed** |
| AUD-P-06 | Medium | Maintainability | ~~Coordinator runtime uses block-scan tick path, but large legacy per-address helper surface remains in same file.~~ | Commit `4465838`: 17개 dead 함수/타입 + AddressCursor 모델 전면 제거 | **Resolved** |
| AUD-P-07 | Low | Accounting Safety | Balance storage layer still clamps result with `GREATEST(0, ...)`; AUD-M-02 수정으로 ingester 단에서 먼저 음수 잔고 차단하므로 DB 클램핑은 최종 안전장치 역할. | `balance_repo.go:181`, `balance_repo.go:186` | **Mitigated** |

### Stage-Split Judgment

- `coordinator`: needed for head/watermark bounded scheduling and backpressure/autotune.
- `fetcher`: needed to isolate chain RPC retries/circuit-breaker and boundary-overlap logic.
- `normalizer`: needed to isolate sidecar decode contract and canonical event building.
- `ingester`: single-writer commit boundary is justified for deterministic balance and watermark semantics.
- `finalizer`: optional/chain-conditional is correct (`ReorgAwareAdapter` chains only).

Conclusion: stage decomposition is sound. Previously identified issues (empty-range watermark, batch sizing, interleaver wait policy) have all been resolved.

### Measured Evidence (Local Bench Run)

Environment:
- Date: 2026-02-22
- Host: Darwin arm64 (Apple M4 Pro)

Observed:
- Normalizer benchmark (`events=20`): ~27us/op, ~54.9KB/op, ~722 allocs/op
- Ingester benchmark (`100tx`): ~278us/op, ~549.9KB/op, ~3675 allocs/op
- Fetcher micro-bench utilities and end-to-end `processJob` bench all run successfully (AUD-P-05 fixed).

Interpretation:
- For larger batches, memory per batch scales quickly in normalize+ingest stages.
- Fetcher block-scan chunking (AUD-P-02, default 500 txs/batch) bounds worst-case burst memory.

### Resolution Summary

1. **[AUD-P-01] Fixed** — Fetcher emits empty sentinel RawBatch for empty block ranges; normalizer passes through without gRPC call; ingester advances watermark.
2. **[AUD-P-02] Fixed** — `BlockScanMaxBatchTxs` config (default 500) in fetcher; large batches split into sub-batches with cursor-safe progression (only last sub-batch advances watermark). 4 new tests.
3. **[AUD-P-03] Fixed** — `InterleaveMaxSkewMs` config field (default 250, env: `INTERLEAVE_MAX_SKEW_MS`); auto-disabled for single-chain deployments.
4. **[AUD-P-04] Fixed** — `hasEnqueued`/`lastEnqueuedStart`/`lastEnqueuedEnd` tracking in coordinator; dedup gated on `configRepo != nil`.
5. **[AUD-P-05] Fixed** — `batchSizeByAddress` LRU cache initialized in `BenchmarkFetchSignatures` and `BenchmarkFetchTransactions`.
6. ~~[AUD-P-06]~~ **Resolved** — legacy per-address code removed (commit `4465838`).
7. **[AUD-P-07] Mitigated** — AUD-M-02 ingester guard covers upstream; DB `GREATEST(0, ...)` clamping is final safety net. No code change needed.

## Verification Commands

```bash
# Build
go build ./cmd/indexer

# Unit tests
go test ./... -count=1

# Race detector (affected packages)
go test -race ./internal/pipeline/ingester/... -count=1
go test -race ./internal/pipeline/normalizer/... -count=1
go test -race ./internal/admin/... -count=1

# Benchmark spot checks used in supplemental re-audit
go test ./internal/pipeline/normalizer -run '^$' -bench 'Benchmark(BuildCanonicalSolanaBalanceEvents|BuildCanonicalBaseBalanceEvents|NormalizedTxFromResult)$' -benchmem -count=1
go test ./internal/pipeline/ingester -run '^$' -bench 'BenchmarkProcessBatch_(1Tx|10Tx|50Tx|100Tx)$' -benchmem -count=1
go test ./internal/pipeline/fetcher -run '^$' -bench 'Benchmark(CanonicalizeSignatures|SuppressBoundaryCursorSignatures|SuppressPostCutoffSignatures|SuppressPreCursorSequenceCarryover|ReduceBatchSize|RetryDelay|CanonicalizeWatchedAddressIdentity|ResolveBatchSize)$' -benchmem -count=1

# Helm template validation
helm template deployments/helm/multichain-indexer/ --debug 2>&1 | head -20
```
