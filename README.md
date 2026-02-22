# multichain-indexer

Backpressure-controlled parallel pipelining indexer for multi-chain custody services.

Go 채널 기반의 4단계 파이프라인과 Node.js gRPC 사이드카로 구성된 멀티체인 블록체인 인덱서입니다. 각 스테이지는 독립적으로 스케일링되며, 버퍼링된 채널을 통해 자연스러운 backpressure를 전파합니다.

> **Supported Chains**: Solana, Base, Ethereum, BTC, Polygon, Arbitrum, BSC
>
> **Current Runtime Targets**: `solana-devnet`, `base-sepolia`, `btc-testnet`, `ethereum-mainnet`, `polygon-mainnet`, `arbitrum-mainnet`, `bsc-mainnet`
>
> 실행 범위는 환경변수로 선택합니다.
> - `RUNTIME_DEPLOYMENT_MODE=like-group` + `RUNTIME_LIKE_GROUP=solana-like|evm-like|btc-like`
> - `RUNTIME_DEPLOYMENT_MODE=independent` + `RUNTIME_CHAIN_TARGET=<chain-network>`

---

## Architecture Overview

> 상세 다이어그램: [`docs/diagrams/indexer_architecture.drawio`](docs/diagrams/indexer_architecture.drawio) (draw.io에서 열기)

```mermaid
graph LR
    subgraph external_left [" "]
        RPC1["Solana RPC\n(JSON-RPC 2.0)"]
        RPC2["EVM RPCs\n(Base/ETH/Polygon/\nArbitrum/BSC)"]
        RPC3["BTC RPC\n(JSON-RPC 2.0)"]
    end

    subgraph go_process ["Go Pipeline Process"]
        COORD["Coordinator\n1 goroutine · ticker"]
        FETCH["Fetcher Pool\nN workers (default 1)"]
        NORM["Normalizer\nN workers (default 1)"]
        INGEST["Ingester\n1 goroutine · single-writer"]

        COORD -- "jobCh\n(buffered)" --> FETCH
        FETCH -- "rawBatchCh\n(buffered)" --> NORM
        NORM -- "normalizedCh\n(BalanceEvents)" --> INGEST
    end

    subgraph sidecar ["Node.js Sidecar · gRPC :50051"]
        TX_DEC["Transaction Decoder\n(chain dispatch)"]
        DISP["PluginDispatcher\npriority routing"]
        SPL["solana decoder\nplugins"]
        EVM["base decoder\nEVM log parser"]
        BTC["btc decoder\nUTXO parser"]
        TX_DEC --> DISP
        DISP --> SPL
        DISP --> EVM
        DISP --> BTC
    end

    subgraph external_right [" "]
        PG[("PostgreSQL 16\nbalance_events\n(signed delta) + JSONB")]
    end

    RPC1 -. "HTTPS" .-> FETCH
    RPC2 -. "HTTPS" .-> FETCH
    RPC3 -. "HTTPS" .-> FETCH
    NORM -. "gRPC" .-> TX_DEC
    INGEST -- "Atomic sql.Tx\nupsert + watermark" --> PG
    COORD -. "read config\n& watermarks" .-> PG
```

### Pipeline Data Flow

```mermaid
sequenceDiagram
    participant C as Coordinator
    participant F as Fetcher (N workers)
    participant N as Normalizer (N workers)
    participant I as Ingester (1 writer)
    participant RPC as Chain RPC (Solana/Base/BTC)
    participant SC as Sidecar (gRPC)
    participant DB as PostgreSQL

    C->>DB: GetActive watched_addresses
    DB-->>C: []WatchedAddress
    C->>DB: Get pipeline_watermarks
    DB-->>C: last ingested block/slot

    Note over C,F: Block-scan: single FetchJob per tick<br/>Cursor-based (Solana): per-address FetchJob
    C->>F: FetchJob via jobCh

    F->>RPC: fetch signatures/hashes(cursor)
    RPC-->>F: []SignatureInfo
    F->>RPC: get transaction payload x N (parallel)
    RPC-->>F: []json.RawMessage
    F->>N: RawBatch via rawBatchCh

    N->>SC: DecodeSolanaTransactionBatch (gRPC)
    SC-->>N: []TransactionResult
    N->>I: NormalizedBatch via normalizedCh

    I->>DB: BEGIN tx
    I->>DB: upsert transactions, tokens, balance_events
    I->>DB: adjust balances (signed delta accumulation)
    I->>DB: update watermark
    I->>DB: COMMIT
```

---

## Backpressure Control

모든 스테이지는 **버퍼링된 Go 채널**로 연결되어, 별도의 동기화 로직 없이 자연스러운 backpressure가 전파됩니다. 다운스트림이 느려지면 업스트림 채널이 차면서 자동으로 속도를 조절합니다.

현재 동작은 채널 기반의 **수동(passive) backpressure**가 기본이며, coordinator의 지표 기반 동적 auto-tune은 옵션(`COORDINATOR_AUTOTUNE_ENABLED=true`)으로 활성화할 수 있습니다.

```mermaid
graph BT
    DB_SLOW["DB Write Slow"] -->|"normalizedCh full"| NORM_BLOCK["Normalizer Workers Block"]
    NORM_BLOCK -->|"rawBatchCh full"| FETCH_BLOCK["Fetcher Workers Block"]
    FETCH_BLOCK -->|"jobCh full"| COORD_BLOCK["Coordinator Blocks"]
    COORD_BLOCK -->|"Waits for next tick"| STABLE["System Stabilizes — Zero Data Loss"]

    style DB_SLOW fill:#ff6b6b,color:#fff
    style NORM_BLOCK fill:#ffa500,color:#fff
    style FETCH_BLOCK fill:#ffd700,color:#000
    style COORD_BLOCK fill:#87ceeb,color:#000
    style STABLE fill:#90ee90,color:#000
```

모든 채널 send는 `select` + `ctx.Done()`으로 보호되어, context 취소 시 블로킹이 즉시 해제됩니다.

```go
// Every channel send follows this pattern
select {
case ch <- data:
    // sent
case <-ctx.Done():
    return ctx.Err()  // graceful exit
}
```

### Channel Topology

| Channel | Type | Direction | Default Buffer |
|---------|------|-----------|----------------|
| `jobCh` | `event.FetchJob` | Coordinator → Fetcher | 20 |
| `rawBatchCh` | `event.RawBatch` | Fetcher → Normalizer | 10 |
| `normalizedCh` | `event.NormalizedBatch` | Normalizer → Ingester | 5 |

**Max in-flight**: 20 + 10 + 5 = 35 batches (per-stage sizing for optimal backpressure)

---

## Concurrency Model

```mermaid
graph TB
    main["main()"]

    subgraph eg1["errgroup (outer)"]
        health["Health Server"]
        pipeline_run["Pipeline.Run()"]
        signal["Signal Handler\nSIGINT / SIGTERM"]
    end

    subgraph eg2["errgroup (inner)"]
        coord["Coordinator\n1 goroutine"]
        fetcher_run["Fetcher.Run()"]
        norm_run["Normalizer.Run()"]
        ingest_run["Ingester\n1 goroutine"]
    end

    subgraph wg1["sync.WaitGroup"]
        fw1["Fetcher Worker 0"]
        fw2["Fetcher Worker 1"]
        fwN["...Worker N"]
    end

    subgraph wg2["sync.WaitGroup"]
        nw1["Normalizer Worker 0"]
        nw2["Normalizer Worker 1"]
        nwN["...Worker N"]
    end

    main --> eg1
    pipeline_run --> eg2
    fetcher_run --> wg1
    norm_run --> wg2
```

| Component | Goroutines | Scaling |
|-----------|-----------|---------|
| Coordinator | 1 (fixed) | Single-writer, ticker-based |
| Fetcher | N (`FETCH_WORKERS`) | Worker pool on shared `jobCh` |
| Normalizer | N (`NORMALIZER_WORKERS`) | Worker pool, shared gRPC connection |
| Ingester | 1 (fixed) | Single-writer for financial consistency |

**Default total**: ~4 goroutines (1 + 1 + 1 + 1) with default workers=1 for strict block ordering

---

## Pipeline Stages

### 1. Coordinator

모니터링 대상 주소를 주기적으로 순회하며 `FetchJob`을 생성합니다.

- `time.NewTicker(interval)`로 주기 실행 (default 5s)
- `watched_addresses` 테이블에서 `is_active=true` 주소 조회
- 워터마크 기반으로 `FetchJob` 구성 → `jobCh`로 전송
- 에러 발생 시 해당 주소 스킵 후 계속 진행

### 2. Fetcher

RPC에서 서명과 원본 트랜잭션 데이터를 병렬로 가져옵니다.

- N workers가 `jobCh`에서 공정 분배 (Go 채널 특성 활용)
- `getSignaturesForAddress()` → 커서 이후 서명 수집 (oldest-first)
- `getTransaction()` x N 병렬 fetch (semaphore 10 제한)
- Circuit breaker wraps RPC calls (configurable thresholds)
- Empty block ranges emit sentinel `RawBatch` to advance watermark
- `RawBatch` 구성 → `rawBatchCh`로 전송

### 3. Normalizer

원본 트랜잭션을 gRPC sidecar로 디코딩합니다.

- N workers, 단일 gRPC 연결 공유 (gRPC multiplexing)
- Circuit breaker wraps sidecar gRPC calls
- `DecodeSolanaTransactionBatch` 호출 (배치별 timeout)
- Response → `NormalizedTransaction` + `NormalizedBalanceEvent` 변환
- `NormalizedBatch` 구성 → `normalizedCh`로 전송

### 4. Ingester

정규화된 데이터를 PostgreSQL에 **원자적으로** 기록합니다.

- **Single-writer**: 동시 쓰기 race condition 원천 차단
- 배치 단위 `sql.Tx`: upsert → balance adjust → watermark update → commit
- 에러 시 전체 배치 롤백 (부분 커밋 없음)
- `ON CONFLICT` 기반 멱등 upsert

```
BEGIN sql.Tx
  ├── FOR EACH transaction:
  │   ├── Upsert transaction (ON CONFLICT DO UPDATE → returns txID)
  │   ├── FOR EACH balance_event:
  │   │   ├── Upsert token → returns tokenID
  │   │   ├── Upsert balance_event (ON CONFLICT DO NOTHING)
  │   │   └── Adjust balance (amount += signed delta)
  ├── Update watermark (GREATEST — non-regressing)
  └── COMMIT
```

---

## Database

PostgreSQL 16 — 통합 테이블 + JSONB 전략. 체인 추가 시 DDL 변경 없음.

```mermaid
erDiagram
    transactions ||--o{ balance_events : "transaction_id"
    tokens ||--o{ balance_events : "token_id"
    tokens ||--o{ balances : "token_id"

    watched_addresses {
        UUID id PK
        VARCHAR chain
        VARCHAR network
        VARCHAR address
        BOOLEAN is_active
    }

    transactions {
        UUID id PK
        VARCHAR tx_hash
        BIGINT block_cursor
        NUMERIC fee_amount
        VARCHAR status
        JSONB chain_data
    }

    balance_events {
        UUID id PK
        UUID transaction_id FK
        UUID token_id FK
        VARCHAR event_category
        VARCHAR event_action
        VARCHAR program_id
        VARCHAR address
        VARCHAR counterparty_address
        NUMERIC delta
        JSONB chain_data
    }

    balances {
        UUID id PK
        UUID token_id FK
        VARCHAR address
        NUMERIC amount
        BIGINT last_updated_cursor
    }

    tokens {
        UUID id PK
        VARCHAR contract_address
        VARCHAR symbol
        INT decimals
    }
```

**11+ tables total** (pipeline state + serving data + operational) — 체인 수에 무관하게 고정.
Core: `transactions`, `balance_events`, `balances`, `tokens`, `watched_addresses`, `indexer_configs`, `indexed_blocks`, `pipeline_watermarks`.
Operational: `address_books`, `balance_reconciliation_snapshots`, `runtime_configs`.
`balance_events`는 flat 테이블 (파티셔닝 제거됨), UNIQUE INDEX on `event_id`로 dedup.

---

## Operational Resilience

### Circuit Breaker

`internal/circuitbreaker/breaker.go` -- integrated in Fetcher (RPC calls) and Normalizer (sidecar gRPC). Configuration externalized via `CircuitBreakerConfig` in `FetcherStageConfig`/`NormalizerStageConfig` (YAML + env vars).

### Startup and Shutdown

- **Preflight connectivity validation**: DB + RPC ping on startup before pipeline begins
- **Pipeline auto-restart**: Exponential backoff (1s -> 5min cap); `context.Canceled` only causes return
- **Worker panic recovery**: `defer`-`recover` in fetcher/normalizer goroutines + ingester Run loop
- **Shutdown timeout**: 30s graceful shutdown -> `os.Exit(1)` for stuck goroutines

### Config Validation

- `MaxOpenConns` upper bound 200, `BatchSize` upper bound 10000
- Worker count bounds validation (1-100 clamp)
- `ConnMaxIdleTime` default 2min

### Pipeline Operational Features

- **Empty sentinel batch**: Fetcher emits empty `RawBatch` for empty block ranges; normalizer passes through; ingester advances watermark
- **Block-scan batch chunking**: `BlockScanMaxBatchTxs` (default 500) limits transactions per batch
- **Configurable interleaver**: `InterleaveMaxSkewMs` (default 250, auto-disabled for single-chain)
- **Coordinator job dedup**: `lastEnqueued` range tracking prevents duplicate FetchJobs
- **Alerter state-transition awareness**: type change (e.g. UNHEALTHY -> RECOVERY) bypasses cooldown
- **Reorg detector**: consecutive RPC error alerts after 5 failures

### Admin Dashboard

Embedded at `/dashboard` via `embed.FS` with auth middleware. 3 JSON APIs under `/admin/v1/dashboard/`.

### Chain-Specific Optimizations

- **BTC batch RPC**: `GetBlocks` batch method; `prefetchPrevouts` + `prefetchBlocks` for parallel I/O; head block 5s TTL cache
- **EVM optimizations**: concurrent topic logs via `errgroup` for 3 topic positions; partial receipt retry (only missing indices)
- **finality_rank()**: PG function (migration 020) replacing hardcoded `CASE WHEN` in query locations

### E2E Latency Metrics

`RawBatch.CreatedAt` -> `NormalizedBatch.FetchedAt`/`NormalizedAt` -> ingester observes end-to-end pipeline latency.

---

## Node.js Sidecar (gRPC Decoder)

체인별 디코딩 라이브러리를 활용한 플러그인 기반 balance event 디코더.

```protobuf
service ChainDecoder {
  rpc DecodeSolanaTransactionBatch(...)
      returns (DecodeSolanaTransactionBatchResponse);
  rpc HealthCheck(...) returns (HealthCheckResponse);
}
```

- **Current contract state**: RPC 이름은 `DecodeSolanaTransactionBatch`지만, payload를 보고 Solana/Base/BTC 디코더로 분기합니다.
- **Naming debt**: chain-neutral RPC로 마이그레이션 예정이며 정책은 `docs/sidecar-deployment-decision.md`를 따릅니다.
- **Plugin-based detection**: Solana 경로는 `EventPlugin` 인터페이스 → `PluginDispatcher`가 priority 순으로 라우팅
- **Instruction ownership**: Solana outer program이 inner instructions를 소유하여 CPI 이중 기록 방지
- **Multi-chain decode modules**: `sidecar/src/decoder/solana/*`, `sidecar/src/decoder/base/*`, `sidecar/src/decoder/btc/*`
- **Signed delta**: positive = inflow, negative = outflow (direction 판단 불필요)
- **Watched address 필터링**: `Set<string>` O(1) lookup

---

## Quick Start

```bash
# Start infrastructure
docker-compose -f deployments/docker-compose.yaml up -d

# Run migrations
make migrate

# Generate protobuf code
make proto

# Example 1) EVM-like 런타임만 실행 (Base Sepolia)
export RUNTIME_DEPLOYMENT_MODE=like-group
export RUNTIME_LIKE_GROUP=evm-like
export BASE_SEPOLIA_RPC_URL=https://your-base-rpc
export BASE_WATCHED_ADDRESSES=0xYourAddress1,0xYourAddress2
make run

# Example 2) 단일 타깃 독립 실행 (BTC testnet)
export RUNTIME_DEPLOYMENT_MODE=independent
export RUNTIME_CHAIN_TARGET=btc-testnet
export BTC_TESTNET_RPC_URL=https://your-btc-rpc
export BTC_WATCHED_ADDRESSES=tb1youraddress1,tb1youraddress2
make run
```

## Key Commands

```bash
make build          # Build Go binary
make run            # Run indexer
make test           # Run Go tests (race + count=1)
make test-sidecar   # Run sidecar vitest
make migrate        # Run DB migrations
make migrate-down   # Rollback 1 migration
make proto          # Generate protobuf code
make sidecar-build  # Build sidecar Docker image
make mock-gen       # Regenerate mocks
make lint           # Run golangci-lint
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `SOLANA_RPC_URL` | `https://api.devnet.solana.com` | Solana RPC endpoint |
| `SOLANA_WATCHED_ADDRESSES` | — | Comma-separated Solana addresses |
| `BASE_SEPOLIA_RPC_URL` | — | Base Sepolia RPC endpoint |
| `BASE_WATCHED_ADDRESSES` | — | Comma-separated Base addresses |
| `BTC_TESTNET_RPC_URL` | — | BTC Testnet RPC endpoint |
| `BTC_WATCHED_ADDRESSES` | — | Comma-separated BTC addresses |
| `DB_URL` | `postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable` | PostgreSQL |
| `SIDECAR_ADDR` | `localhost:50051` | gRPC sidecar address |
| `WATCHED_ADDRESSES` | — | Legacy alias of `SOLANA_WATCHED_ADDRESSES` |
| `RUNTIME_DEPLOYMENT_MODE` | `like-group` | Runtime target selection mode (`like-group` or `independent`) |
| `RUNTIME_LIKE_GROUP` | — | Limit targets to one group in `like-group` mode (`solana-like`, `evm-like`, `btc-like`) |
| `RUNTIME_CHAIN_TARGET` | — | Single target for independent deployment (e.g. `base-sepolia`) |
| `RUNTIME_CHAIN_TARGETS` | — | CSV override target list (e.g. `solana-devnet,base-sepolia`) |
| `FETCH_WORKERS` | `1` | Parallel RPC fetch workers (default 1 for strict block ordering) |
| `NORMALIZER_WORKERS` | `1` | Parallel gRPC decode workers (default 1 for strict block ordering) |
| `BATCH_SIZE` | `100` | Signatures per fetch batch |
| `INDEXING_INTERVAL_MS` | `5000` | Coordinator tick interval (ms) |
| `JOB_CH_BUFFER` | `20` | jobCh buffer (Coordinator -> Fetcher) |
| `RAW_BATCH_CH_BUFFER` | `10` | rawBatchCh buffer (Fetcher -> Normalizer) |
| `NORMALIZED_CH_BUFFER` | `5` | normalizedCh buffer (Normalizer -> Ingester) |
| `COORDINATOR_AUTOTUNE_ENABLED` | `false` | Enable chain-scoped coordinator batch auto-tune |
| `COORDINATOR_AUTOTUNE_MIN_BATCH_SIZE` | `10` | Lower batch bound for auto-tune |
| `COORDINATOR_AUTOTUNE_MAX_BATCH_SIZE` | `BATCH_SIZE` | Upper batch bound for auto-tune |
| `COORDINATOR_AUTOTUNE_STEP_UP` | `10` | Batch step increase when lag pressure is sustained |
| `COORDINATOR_AUTOTUNE_STEP_DOWN` | `10` | Batch step decrease when queue pressure is sustained |
| `COORDINATOR_AUTOTUNE_LAG_HIGH_WATERMARK` | `500` | Lag threshold to trigger increase |
| `COORDINATOR_AUTOTUNE_LAG_LOW_WATERMARK` | `100` | Lag threshold considered healthy for decrease decisions |
| `COORDINATOR_AUTOTUNE_QUEUE_HIGH_PCT` | `80` | Job channel occupancy threshold to trigger decrease |
| `COORDINATOR_AUTOTUNE_QUEUE_LOW_PCT` | `30` | Job channel occupancy threshold considered low pressure |
| `COORDINATOR_AUTOTUNE_HYSTERESIS_TICKS` | `2` | Consecutive same-signal ticks required before changing batch |
| `COORDINATOR_AUTOTUNE_TELEMETRY_STALE_TICKS` | `2` | Invalid/missing lag telemetry ticks before stale fallback hold |
| `COORDINATOR_AUTOTUNE_TELEMETRY_RECOVERY_TICKS` | `1` | Healthy telemetry ticks required before leaving stale fallback hold |
| `COORDINATOR_AUTOTUNE_OPERATOR_OVERRIDE_BATCH_SIZE` | `0` | Optional fixed manual batch override while non-zero |
| `COORDINATOR_AUTOTUNE_OPERATOR_RELEASE_HOLD_TICKS` | `2` | Hold ticks after manual override release |
| `COORDINATOR_AUTOTUNE_POLICY_VERSION` | `policy-v1` | Auto-tune policy version lineage key |
| `COORDINATOR_AUTOTUNE_POLICY_MANIFEST_DIGEST` | `manifest-v1` | Policy-manifest digest lineage key used for deterministic refresh/reject/re-apply reconciliation |
| `COORDINATOR_AUTOTUNE_POLICY_MANIFEST_REFRESH_EPOCH` | `0` | Monotonic refresh epoch; stale or ambiguous manifest refreshes are rejected |
| `COORDINATOR_AUTOTUNE_POLICY_ACTIVATION_HOLD_TICKS` | `1` | Hold ticks at accepted policy/manifest transition boundaries |
| `SIDECAR_TIMEOUT_SEC` | `30` | gRPC decode timeout (s) |
| `LOG_LEVEL` | `info` | Log level (debug/info/warn/error) |

## Project Structure

```
multichain-indexer/
├── cmd/indexer/              # Entry point (main.go)
├── configs/                  # YAML config example (config.example.yaml)
├── internal/
│   ├── addressindex/         # Address index management
│   ├── admin/                # Admin REST API (server, audit, rate limiting)
│   ├── alert/                # Alert system (Slack, Webhook) with per-key cooldown
│   ├── cache/                # LRU caching utilities (ShardedLRU for ingester deniedCache)
│   ├── circuitbreaker/       # Circuit breaker (Fetcher RPC + Normalizer sidecar)
│   ├── chain/
│   │   ├── adapter.go        # ChainAdapter + BlockScanAdapter interfaces
│   │   ├── solana/           # Solana adapter (cursor-based)
│   │   ├── base/             # Base adapter (block-scan)
│   │   ├── btc/              # BTC adapter (block-scan)
│   │   ├── ethereum/         # Ethereum adapter (block-scan)
│   │   ├── polygon/          # Polygon adapter (block-scan)
│   │   ├── arbitrum/         # Arbitrum adapter (block-scan)
│   │   ├── bsc/              # BSC adapter (block-scan)
│   │   └── ratelimit/        # Per-chain RPC rate limiting
│   ├── config/               # YAML + env layered config loading
│   ├── domain/
│   │   ├── model/            # DB models + chain/network enums
│   │   └── event/            # Pipeline events (FetchJob, RawBatch, NormalizedBatch)
│   ├── metrics/              # Prometheus metrics definitions
│   ├── pipeline/
│   │   ├── pipeline.go       # Orchestrator (errgroup + channels)
│   │   ├── registry.go       # Chain:Network → Pipeline registry
│   │   ├── health.go         # Per-chain health monitoring (HEALTHY/UNHEALTHY/INACTIVE)
│   │   ├── config_watcher.go # Hot config reload via runtime_configs polling
│   │   ├── coordinator/      # Stage 1: address scanning + auto-tune
│   │   ├── fetcher/          # Stage 2: parallel RPC fetch
│   │   ├── normalizer/       # Stage 3: gRPC decode (balance event canonicalization)
│   │   ├── ingester/         # Stage 4: atomic DB write (bulk operations)
│   │   ├── finalizer/        # Block finality promotion
│   │   ├── reorgdetector/    # Chain reorg detection + rollback
│   │   ├── replay/           # Historical data replay service
│   │   ├── retry/            # Retry classification (transient vs terminal)
│   │   └── identity/         # Shared canonicalization functions
│   ├── reconciliation/       # Balance reconciliation (on-chain vs DB)
│   ├── store/
│   │   └── postgres/         # 11 repository implementations + 22 migrations (001-022)
│   └── tracing/              # OpenTelemetry tracing integration
├── proto/sidecar/v1/         # Protobuf definitions
├── pkg/generated/            # Generated Go protobuf code
├── sidecar/                  # Node.js gRPC decoder (solana/base/btc)
├── deployments/              # Docker Compose + Helm charts
├── docs/                     # Architecture, testing, runbook, etc.
├── test/loadtest/            # Load testing tools
└── Makefile
```

## Adding a New Chain

`ChainAdapter` 인터페이스를 구현하면 파이프라인 코어 변경 없이 새 체인을 추가할 수 있습니다. EVM/BTC 계열은 `BlockScanAdapter`를 추가 구현하여 블록 범위 스캔을 지원합니다.

```go
// 기본 인터페이스 (모든 체인)
type ChainAdapter interface {
    Chain() string
    GetHeadSequence(ctx context.Context) (int64, error)
    FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]SignatureInfo, error)
    FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error)
}

// 블록 스캔 (EVM/BTC): 단일 블록 범위에서 모든 감시 주소 이벤트 수집
type BlockScanAdapter interface {
    ChainAdapter
    ScanBlocks(ctx context.Context, startBlock, endBlock int64, watchedAddresses []string) ([]SignatureInfo, error)
}

// 선택적 확장 인터페이스
type ReorgAwareAdapter interface { ... }     // Reorg 감지 + 롤백
type BalanceQueryAdapter interface { ... }   // 온체인 잔액 조회 (reconciliation용)
```

1. `internal/chain/<name>/adapter.go` — `ChainAdapter` (또는 `BlockScanAdapter`) 구현
2. `sidecar/src/decoder/<name>/` — 체인 디코더 추가 + `sidecar/src/decoder/index.ts` 분기 추가
3. `internal/config/config.go` + `cmd/indexer/main.go` — runtime target/like-group wiring 추가
4. `internal/pipeline/normalizer/normalizer_balance_*.go` — 체인별 balance event 정규화 로직 추가
5. `chain_data` JSONB 구조 정의 + 테스트/런북 업데이트

DDL 변경 없음. 기존 테이블이 모든 체인을 수용합니다.

## Management Structure

1. **런타임 관리** (서비스 경계/실행 범위)
   - `RUNTIME_DEPLOYMENT_MODE`, `RUNTIME_LIKE_GROUP`, `RUNTIME_CHAIN_TARGET(S)`로 실행 범위 관리
   - 코드 기준: `internal/config/config.go`, `cmd/indexer/main.go`
2. **배포 관리** (sidecar 단위 전략)
   - 기본: 단일 sidecar 배포 단위
   - 분리 트리거/SLO/운영룰: `docs/sidecar-deployment-decision.md`
3. **장애 대응/검증 관리**
   - 운영 체크/복구: `docs/runbook.md`
   - 테스트 전략: `docs/testing.md`
   - 거버넌스: `AGENTS.md`, `docs/autonomy-policy.md`

## Docs

- [Architecture](docs/architecture.md) — 상세 아키텍처 명세서 (C4 모델, 파이프라인, DB 스키마)
- [Testing](docs/testing.md) — 테스트 방법론 및 시나리오
- [Runbook](docs/runbook.md) — 장애 대응 및 복구 절차
- [Roadmap](docs/roadmap.md) — 고도화 마일스톤과 우선순위
- [DB Migration Rationale](docs/db-migration-rationale.md) — AS-IS (JPA JOINED) → TO-BE (JSONB) 비교
- [Sidecar Deployment Decision](docs/sidecar-deployment-decision.md) — sidecar 분리 트리거/SLO/운영 룰 ADR
- [PRD: Production Hardening](docs/prd-production-hardening.md) — 프로덕션 하드닝 요구사항
- [Definition Of Done](docs/definition-of-done.md) — 작업 완료 기준
- [GitHub Collaboration](docs/github-collaboration.md) — 이슈/PR/라벨/승인 운영 규칙
- [Autonomy Policy](docs/autonomy-policy.md) — 에이전트 자율 실행 정책 및 큐 규칙

## License

Private
