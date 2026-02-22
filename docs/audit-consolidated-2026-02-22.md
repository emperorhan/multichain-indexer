# Consolidated Audit Report — 2026-02-22

Cross-verified against codebase. 12 confirmed issues fixed; 4 false positives documented.

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
| AUD-M-03 | Medium | Integrity | Default finality "finalized" for all chains (too optimistic) | **Fixed** |
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
**Problem:** `defaultFinalityState` returned "finalized" for all chains including EVM and BTC, where finality is not instantaneous.
**Fix:** Solana retains "finalized" (explicit RPC guarantee). EVM/BTC/other chains default to "confirmed" until chain-specific finality signals upgrade the state.

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

# Helm template validation
helm template deployments/helm/multichain-indexer/ --debug 2>&1 | head -20
```
