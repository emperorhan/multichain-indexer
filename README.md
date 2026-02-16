# multichain-indexer

Backpressure-controlled parallel pipelining indexer for multi-chain custody services.

Go 채널 기반의 4단계 파이프라인과 Node.js gRPC 사이드카로 구성된 멀티체인 블록체인 인덱서입니다. 각 스테이지는 독립적으로 스케일링되며, 버퍼링된 채널을 통해 자연스러운 backpressure를 전파합니다.

> **MVP Target**: Solana devnet (SOL + SPL Token)

---

## Architecture Overview

> 상세 다이어그램: [`docs/diagrams/indexer_architecture.drawio`](docs/diagrams/indexer_architecture.drawio) (draw.io에서 열기)

```mermaid
graph LR
    subgraph external_left [" "]
        RPC["Solana RPC\n(JSON-RPC 2.0)"]
    end

    subgraph go_process ["Go Pipeline Process"]
        COORD["Coordinator\n1 goroutine · ticker"]
        FETCH["Fetcher Pool\nN workers (default 2)"]
        NORM["Normalizer\nN workers (default 2)"]
        INGEST["Ingester\n1 goroutine · single-writer"]

        COORD -- "jobCh\n(buffered)" --> FETCH
        FETCH -- "rawBatchCh\n(buffered)" --> NORM
        NORM -- "normalizedCh\n(BalanceEvents)" --> INGEST
    end

    subgraph sidecar ["Node.js Sidecar · gRPC :50051"]
        TX_DEC["Transaction Decoder\ninstruction ownership"]
        DISP["PluginDispatcher\npriority routing"]
        SPL["generic_spl_token\nSPL Token / Token-2022"]
        SYS["generic_system\nSystem Program"]
        TX_DEC --> DISP
        DISP --> SPL
        DISP --> SYS
    end

    subgraph external_right [" "]
        PG[("PostgreSQL 16\nbalance_events\n(signed delta) + JSONB")]
        REDIS[("Redis 7\n(future)")]
    end

    RPC -. "HTTPS" .-> FETCH
    NORM -. "gRPC" .-> TX_DEC
    INGEST -- "Atomic sql.Tx\nupsert + cursor" --> PG
    COORD -. "read config\n& cursors" .-> PG
```

### Pipeline Data Flow

```mermaid
sequenceDiagram
    participant C as Coordinator
    participant F as Fetcher (N workers)
    participant N as Normalizer (N workers)
    participant I as Ingester (1 writer)
    participant RPC as Solana RPC
    participant SC as Sidecar (gRPC)
    participant DB as PostgreSQL

    C->>DB: GetActive watched_addresses
    DB-->>C: []WatchedAddress
    C->>DB: Get address_cursors
    DB-->>C: []AddressCursor

    loop Per Address
        C->>F: FetchJob via jobCh
    end

    F->>RPC: getSignaturesForAddress(cursor)
    RPC-->>F: []SignatureInfo
    F->>RPC: getTransaction(sig) x N (parallel)
    RPC-->>F: []json.RawMessage
    F->>N: RawBatch via rawBatchCh

    N->>SC: DecodeSolanaTransactionBatch (gRPC)
    SC-->>N: []TransactionResult
    N->>I: NormalizedBatch via normalizedCh

    I->>DB: BEGIN tx
    I->>DB: upsert transactions, tokens, balance_events
    I->>DB: adjust balances (signed delta accumulation)
    I->>DB: update cursor + watermark
    I->>DB: COMMIT
```

---

## Backpressure Control

모든 스테이지는 **버퍼링된 Go 채널**로 연결되어, 별도의 동기화 로직 없이 자연스러운 backpressure가 전파됩니다. 다운스트림이 느려지면 업스트림 채널이 차면서 자동으로 속도를 조절합니다.

현재 동작은 채널 기반의 **수동(passive) backpressure**이며, coordinator의 지표 기반 동적 auto-tune 제어(예: lag 기반 tick/batch 조절)는 기본 런타임에 아직 포함되지 않습니다. 관련 구현은 `IMPLEMENTATION_PLAN.md`의 다음 신뢰성 트랜치에서 진행합니다.

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

| Channel | Type | Direction | Buffer |
|---------|------|-----------|--------|
| `jobCh` | `event.FetchJob` | Coordinator → Fetcher | configurable |
| `rawBatchCh` | `event.RawBatch` | Fetcher → Normalizer | configurable |
| `normalizedCh` | `event.NormalizedBatch` | Normalizer → Ingester | configurable |

**Max in-flight**: 3 x `CHANNEL_BUFFER_SIZE` batches (default 30)

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

**Default total**: ~12 goroutines (1 + 3 + 4 + 4)

---

## Pipeline Stages

### 1. Coordinator

모니터링 대상 주소를 주기적으로 순회하며 `FetchJob`을 생성합니다.

- `time.NewTicker(interval)`로 주기 실행 (default 5s)
- `watched_addresses` 테이블에서 `is_active=true` 주소 조회
- 각 주소의 현재 커서를 읽어 `FetchJob` 구성 → `jobCh`로 전송
- 에러 발생 시 해당 주소 스킵 후 계속 진행

### 2. Fetcher

RPC에서 서명과 원본 트랜잭션 데이터를 병렬로 가져옵니다.

- N workers가 `jobCh`에서 공정 분배 (Go 채널 특성 활용)
- `getSignaturesForAddress()` → 커서 이후 서명 수집 (oldest-first)
- `getTransaction()` x N 병렬 fetch (semaphore 10 제한)
- `RawBatch` 구성 → `rawBatchCh`로 전송

### 3. Normalizer

원본 트랜잭션을 gRPC sidecar로 디코딩합니다.

- N workers, 단일 gRPC 연결 공유 (gRPC multiplexing)
- `DecodeSolanaTransactionBatch` 호출 (배치별 timeout)
- Response → `NormalizedTransaction` + `NormalizedBalanceEvent` 변환
- `NormalizedBatch` 구성 → `normalizedCh`로 전송

### 4. Ingester

정규화된 데이터를 PostgreSQL에 **원자적으로** 기록합니다.

- **Single-writer**: 동시 쓰기 race condition 원천 차단
- 배치 단위 `sql.Tx`: upsert → balance adjust → cursor update → commit
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
  ├── Update cursor
  ├── Update watermark (GREATEST — non-regressing)
  └── COMMIT
```

---

## Database

PostgreSQL 16 — 통합 테이블 + JSONB 전략. 체인 추가 시 DDL 변경 없음.

```mermaid
erDiagram
    watched_addresses ||--o{ address_cursors : "FK (chain,network,address)"
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

    address_cursors {
        UUID id PK
        VARCHAR cursor_value
        BIGINT cursor_sequence
        BIGINT items_processed
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

**8 tables total** (4 pipeline state + 4 serving data) — 체인 수에 무관하게 고정.

---

## Node.js Sidecar (gRPC Decoder)

체인별 npm SDK 생태계(`@solana/web3.js` 등)를 활용한 플러그인 기반 balance event 디코더.

```protobuf
service ChainDecoder {
  rpc DecodeSolanaTransactionBatch(...)
      returns (DecodeSolanaTransactionBatchResponse);
  rpc HealthCheck(...) returns (HealthCheckResponse);
}
```

- **Plugin-based detection**: `EventPlugin` 인터페이스 → `PluginDispatcher`가 priority 순으로 라우팅
- **Instruction ownership**: outer program이 inner instructions를 소유하여 CPI 이중 기록 방지
- **Builtin plugins**: SPL Token/Token-2022 (`generic_spl_token`), System Program (`generic_system`)
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

# Set watched addresses and run
export WATCHED_ADDRESSES=YourSolanaAddress1,YourSolanaAddress2
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
| `BASE_SEPOLIA_RPC_URL` | — | Base Sepolia RPC endpoint |
| `DB_URL` | `postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable` | PostgreSQL |
| `SIDECAR_ADDR` | `localhost:50051` | gRPC sidecar address |
| `WATCHED_ADDRESSES` | — | Comma-separated Solana addresses |
| `BASE_WATCHED_ADDRESSES` | — | Comma-separated Base addresses |
| `RUNTIME_DEPLOYMENT_MODE` | `like-group` | Runtime target selection mode (`like-group` or `independent`) |
| `RUNTIME_LIKE_GROUP` | — | Limit targets to one group in `like-group` mode (`solana-like`, `evm-like`, `btc-like`) |
| `RUNTIME_CHAIN_TARGET` | — | Single target for independent deployment (e.g. `base-sepolia`) |
| `RUNTIME_CHAIN_TARGETS` | — | CSV override target list (e.g. `solana-devnet,base-sepolia`) |
| `FETCH_WORKERS` | `2` | Parallel RPC fetch workers |
| `NORMALIZER_WORKERS` | `2` | Parallel gRPC decode workers |
| `BATCH_SIZE` | `100` | Signatures per fetch batch |
| `INDEXING_INTERVAL_MS` | `5000` | Coordinator tick interval (ms) |
| `CHANNEL_BUFFER_SIZE` | `10` | Inter-stage channel buffer |
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
├── cmd/indexer/              # Entry point
├── internal/
│   ├── config/               # Environment config loading
│   ├── domain/
│   │   ├── model/            # DB models (8 types)
│   │   └── event/            # Pipeline events (FetchJob, RawBatch, NormalizedBatch)
│   ├── chain/
│   │   ├── adapter.go        # ChainAdapter interface
│   │   └── solana/           # Solana implementation + RPC client
│   ├── pipeline/
│   │   ├── pipeline.go       # Orchestrator (errgroup + channels)
│   │   ├── coordinator/      # Stage 1: address scanning
│   │   ├── fetcher/          # Stage 2: parallel RPC fetch
│   │   ├── normalizer/       # Stage 3: gRPC decode
│   │   └── ingester/         # Stage 4: atomic DB write
│   └── store/
│       └── postgres/         # Repository implementations + migrations
├── proto/sidecar/v1/         # Protobuf definitions
├── pkg/generated/            # Generated Go protobuf code
├── sidecar/                  # Node.js gRPC decoder (plugin-based balance events)
├── deployments/              # Docker Compose
├── docs/                     # Architecture, testing, DB rationale
└── Makefile
```

## Adding a New Chain

`ChainAdapter` 인터페이스를 구현하면 파이프라인 코어 변경 없이 새 체인을 추가할 수 있습니다.

```go
type ChainAdapter interface {
    Chain() string
    GetHeadSequence(ctx context.Context) (int64, error)
    FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]SignatureInfo, error)
    FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error)
}
```

1. `internal/chain/<name>/adapter.go` — `ChainAdapter` 구현
2. `sidecar/src/decoder/<name>/` — Transaction decoder 추가
3. `proto/sidecar/v1/decoder.proto` — gRPC RPC 메서드 추가
4. `chain_data` JSONB 구조 정의

DDL 변경 없음. 기존 8개 테이블이 모든 체인을 수용합니다.

## Ralph Loop (Local First)

로컬 Ralph 루프 기준으로 최소 실행 절차만 유지합니다.

1. 초기화:
   - `scripts/ralph_local_init.sh`
2. 인증 확인:
   - `scripts/codex_auth_status.sh --require-chatgpt`
3. 데몬 시작:
   - `scripts/ralph_local_daemon.sh start`
4. 로컬 이슈 추가:
   - `scripts/ralph_local_new_issue.sh developer "<title>"`
5. 상태/로그:
   - `scripts/ralph_local_daemon.sh status`
   - `scripts/ralph_local_daemon.sh tail`
6. 종료:
   - `scripts/ralph_local_daemon.sh stop`

신뢰 모드가 필요하면 시작 시 1회만 설정합니다.
- `RALPH_LOCAL_TRUST_MODE=true scripts/ralph_local_daemon.sh start`

GitHub 기반 루프(원격 runner, workflow 토글, auto-merge)는 기본 흐름에서 분리했고, 필요할 때만 아래 문서를 참고합니다.
- `docs/autonomy-policy.md`
- `docs/github-collaboration.md`

## Docs

- [Ralph Local Offline Mode](docs/ralph-local-offline-mode.md) — 로컬 루프 운영 정책(로컬 범위만)
- [Ralph Local Script Map](docs/ralph-local-script-map.md) — 로컬 루프 기준 스크립트 분류표
- [Ralph Loop Usage Guide](docs/ralph-loop-user-guide.md) — 시작/점검/중지 커맨드 요약
- [Ralph Loop Troubleshooter](docs/ralph-loop-troubleshooter.md) — 실제 장애 원인/복구 체크리스트
- [Architecture](docs/architecture.md) — 상세 아키텍처 명세서
- [Sidecar Deployment Decision](docs/sidecar-deployment-decision.md) — sidecar 분리 트리거/SLO/운영 룰 ADR
- [Testing](docs/testing.md) — 테스트 방법론 및 시나리오
- [DB Migration Rationale](docs/db-migration-rationale.md) — AS-IS (JPA JOINED) → TO-BE (JSONB) 비교
- [Roadmap](docs/roadmap.md) — 고도화 마일스톤과 우선순위
- [Runbook](docs/runbook.md) — 장애 대응 및 복구 절차
- [Definition Of Done](docs/definition-of-done.md) — 작업 완료 기준
- [GitHub Collaboration](docs/github-collaboration.md) — 이슈/PR/라벨/승인 운영 규칙
- [Autonomy Policy](docs/autonomy-policy.md) — 에이전트 자율 실행 정책 및 큐 규칙

## License

Private
