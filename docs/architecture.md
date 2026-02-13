# 아키텍처 명세서: koda-custody-indexer

> 멀티체인 커스터디 인덱서 — Go 파이프라인 + Node.js Sidecar (gRPC 디코더)

---

## 1. 시스템 개요 및 설계 철학

### 1.1 해결하려는 문제

커스터디 서비스에서 블록체인 트랜잭션을 실시간으로 추적하여:
- 모니터링 대상 주소의 입출금 내역을 수집
- 토큰별 잔액을 정확하게 유지
- 멀티체인 환경에서 일관된 데이터 모델을 제공

### 1.2 설계 원칙

| 원칙 | 설명 | 구현 |
|------|------|------|
| **금융급 정합성 > 처리량** | 데이터 누락/중복보다 느린 처리가 낫다 | 단일 Writer Ingester, 원자적 DB 트랜잭션 |
| **단일 Writer** | 동시 쓰기로 인한 race condition 방지 | Ingester 1개 고루틴 (`ingester.go:52`) |
| **체인 무관 파이프라인** | 파이프라인 코어는 체인을 모른다 | `ChainAdapter` 인터페이스 (`adapter.go:10`) |
| **언어 경계 분리** | Go 파이프라인 + Node.js 디코딩 | gRPC sidecar (`decoder.proto`) |
| **통합 테이블 + JSONB** | 체인 추가 시 DDL 변경 없음 | `chain_data JSONB` ([상세](db-migration-rationale.md)) |

### 1.3 핵심 결정 및 트레이드오프

**Go 채널 vs Redis Streams (MVP):**
- 선택: Go 채널 (in-process)
- 이유: MVP에서 단일 프로세스로 충분, Redis Streams는 프로세스 분리 시 도입
- 영향: 수평 스케일링 불가 (단일 인스턴스)

**Node.js sidecar vs 순수 Go 디코딩:**
- 선택: Node.js sidecar (gRPC)
- 이유: `@solana/web3.js` 등 체인별 npm SDK 생태계 활용
- 영향: 프로세스 간 통신 오버헤드, 추가 배포 단위

**통합 테이블 + JSONB vs 체인별 스키마:**
- 선택: 통합 테이블 + JSONB
- 이유: 체인 추가 시 DDL 변경 없음, 크로스체인 쿼리 단순화
- 영향: JSONB 내부 쿼리 성능 제한 (GIN 인덱스로 보완)
- 상세: [DB 모델 변경 비교](db-migration-rationale.md)

---

## 2. C4 Model

### 2.1 Level 1: System Context

> 다이어그램: `docs/diagrams/c4-system-context.drawio`

```mermaid
C4Context
    title System Context — koda-custody-indexer

    Person(consumer, "외부 소비자", "대시보드, API 서버")

    System(indexer, "koda-custody-indexer", "멀티체인 트랜잭션 인덱서")

    System_Ext(solana_rpc, "Solana RPC", "JSON-RPC 2.0")
    SystemDb(postgres, "PostgreSQL 16", "트랜잭션, 잔액, 커서")
    SystemDb(redis, "Redis 7", "향후 프로세스 분리용")

    Rel(indexer, solana_rpc, "getSignaturesForAddress, getTransaction", "HTTPS")
    Rel(indexer, postgres, "upsert, query", "TCP :5433")
    Rel(indexer, redis, "(향후)", "TCP :6380")
    Rel(consumer, postgres, "SELECT", "TCP :5433")
```

### 2.2 Level 2: Container

> 다이어그램: `docs/diagrams/c4-container.drawio`

```mermaid
C4Container
    title Container — koda-custody-indexer

    Container(go_binary, "Go Indexer", "Go 1.24", "4단계 파이프라인: Coordinator→Fetcher→Normalizer→Ingester")
    Container(sidecar, "Node.js Sidecar", "Node 22", "gRPC 트랜잭션 디코더")
    ContainerDb(postgres, "PostgreSQL 16", "통합 테이블 + JSONB")
    ContainerDb(redis, "Redis 7", "향후 스트림")

    System_Ext(solana_rpc, "Solana RPC")

    Rel(go_binary, solana_rpc, "JSON-RPC 2.0", "HTTPS")
    Rel(go_binary, sidecar, "DecodeSolanaTransactionBatch", "gRPC :50051")
    Rel(go_binary, postgres, "sql.Tx upsert", "TCP :5433")
    Rel(sidecar, go_binary, "TransactionResult[]", "gRPC response")
```

### 2.3 Level 3: Component (Go Indexer)

> 다이어그램: (Mermaid로 표현)

```mermaid
graph TB
    subgraph "cmd/indexer"
        main["main.go<br/>엔트리포인트"]
    end

    subgraph "internal/config"
        config["config.go<br/>환경변수 로딩"]
    end

    subgraph "internal/pipeline"
        pipeline["pipeline.go<br/>오케스트레이터"]
        coord["coordinator.go<br/>주소 순회"]
        fetch["fetcher.go<br/>RPC fetch"]
        norm["normalizer.go<br/>gRPC decode"]
        ingest["ingester.go<br/>DB write"]
    end

    subgraph "internal/chain"
        adapter_if["adapter.go<br/>ChainAdapter 인터페이스"]
        sol_adapter["solana/adapter.go<br/>Solana 구현"]
        sol_rpc["solana/rpc/<br/>JSON-RPC 클라이언트"]
    end

    subgraph "internal/store/postgres"
        db["db.go<br/>연결 풀"]
        repos["*_repo.go<br/>7개 리포지토리"]
    end

    subgraph "internal/domain"
        models["model/<br/>DB 모델 (8개)"]
        events["event/<br/>파이프라인 이벤트 (3개)"]
    end

    main --> config
    main --> pipeline
    main --> sol_adapter
    main --> db
    pipeline --> coord
    pipeline --> fetch
    pipeline --> norm
    pipeline --> ingest
    fetch --> adapter_if
    sol_adapter --> sol_rpc
    sol_adapter -.-> adapter_if
    ingest --> repos
    repos --> db
    repos --> models
    coord --> events
    fetch --> events
    norm --> events
```

### 2.4 Level 3: Component (Node.js Sidecar)

```mermaid
graph TB
    subgraph "src/"
        index["index.ts<br/>gRPC 서버 시작"]
        server["server.ts<br/>proto-loader, 핸들러 등록"]
    end

    subgraph "src/decoder/"
        decoder_idx["index.ts<br/>배치 디스패치"]

        subgraph "src/decoder/solana/"
            tx_decoder["transaction_decoder.ts<br/>메타데이터 추출, 인스트럭션 평탄화"]
            spl_parser["spl_token_parser.ts<br/>SPL transfer/transferChecked"]
            sys_parser["system_parser.ts<br/>SOL systemTransfer/createAccount"]
        end
    end

    index --> server
    server --> decoder_idx
    decoder_idx --> tx_decoder
    tx_decoder --> spl_parser
    tx_decoder --> sys_parser
```

---

## 3. 파이프라인 아키텍처 및 데이터 흐름

### 3.1 전체 파이프라인 개요

> 다이어그램: `docs/diagrams/pipeline-data-flow.drawio`

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

    loop 각 주소
        C->>F: FetchJob via jobCh
    end

    F->>RPC: getSignaturesForAddress(cursor)
    RPC-->>F: []SignatureInfo
    F->>RPC: getTransaction(sig) × N (parallel)
    RPC-->>F: []json.RawMessage

    F->>N: RawBatch via rawBatchCh

    N->>SC: DecodeSolanaTransactionBatch (gRPC)
    SC-->>N: []TransactionResult

    N->>I: NormalizedBatch via normalizedCh

    I->>DB: BEGIN tx
    I->>DB: upsert transactions
    I->>DB: upsert tokens
    I->>DB: upsert transfers
    I->>DB: adjust balances
    I->>DB: deduct fees
    I->>DB: update cursor
    I->>DB: update watermark
    I->>DB: COMMIT
```

### 3.2 채널 토폴로지 및 버퍼링

3개의 typed Go 채널이 파이프라인 스테이지를 연결한다 (`pipeline.go:66-71`):

```go
jobCh        := make(chan event.FetchJob, bufSize)        // Coordinator → Fetcher
rawBatchCh   := make(chan event.RawBatch, bufSize)         // Fetcher → Normalizer
normalizedCh := make(chan event.NormalizedBatch, bufSize)   // Normalizer → Ingester
```

| 채널 | 타입 | 방향 | 기본 버퍼 |
|------|------|------|----------|
| `jobCh` | `event.FetchJob` | Coordinator → Fetcher | 10 |
| `rawBatchCh` | `event.RawBatch` | Fetcher → Normalizer | 10 |
| `normalizedCh` | `event.NormalizedBatch` | Normalizer → Ingester | 10 |

**Backpressure 전파:**

```
Ingester 처리 느림
  → normalizedCh 가득 참
    → Normalizer 블로킹 (normalizer.go:178-185, select + ctx.Done)
      → rawBatchCh 가득 참
        → Fetcher 블로킹 (fetcher.go:120-128, select + ctx.Done)
          → jobCh 가득 참
            → Coordinator 블로킹 (coordinator.go:101-104, select + ctx.Done)
```

모든 채널 send는 `select` 문으로 `ctx.Done()`과 함께 사용되어 context 취소 시 블로킹 해제.

### 3.3 Coordinator

**파일**: `internal/pipeline/coordinator/coordinator.go`

**역할**: 모니터링 대상 주소를 주기적으로 순회하며 FetchJob 생성.

**실행 흐름:**
1. `time.NewTicker(interval)` 시작 (기본 5초, `coordinator.go:50`)
2. 시작 즉시 첫 tick 실행 (`coordinator.go:54`)
3. 매 tick 마다:
   - `GetActive()`: `watched_addresses` 테이블에서 `is_active=true` 주소 조회 (`coordinator.go:72`)
   - 각 주소의 `address_cursors`에서 현재 커서 조회 (`coordinator.go:80`)
   - `FetchJob` 생성 → `jobCh`로 전송 (`coordinator.go:91-105`)

**특성:**
- 단일 고루틴 (직렬 처리)
- 에러 발생 시 해당 주소 스킵 후 계속 (`coordinator.go:82-84`)
- 채널 가득 시 `ctx.Done()` 확인하며 블로킹

### 3.4 Fetcher

**파일**: `internal/pipeline/fetcher/fetcher.go`

**역할**: RPC에서 서명과 원본 트랜잭션 데이터를 가져옴.

**Worker Pool 패턴:**

```go
// fetcher.go:37-52
var wg sync.WaitGroup
for i := 0; i < f.workerCount; i++ {
    wg.Add(1)
    go func(workerID int) {
        defer wg.Done()
        f.worker(ctx, workerID)   // 공유 jobCh에서 공정 분배
    }(i)
}
wg.Wait()
```

- 기본 2 workers (`FETCH_WORKERS` 환경변수)
- 공유 `jobCh`에서 fair scheduling (Go 채널 특성)

**처리 흐름 (`processJob`, fetcher.go:75-132):**
1. `FetchNewSignatures()`: 커서 이후 서명 수집 (oldest-first)
2. 서명 없으면 조기 반환
3. `FetchTransactions()`: 원본 트랜잭션 JSON fetch (최대 10 병렬)
4. `RawBatch` 구성 → `rawBatchCh`로 전송
5. 새 커서 = 배치의 마지막(가장 최신) 서명

### 3.5 Normalizer

**파일**: `internal/pipeline/normalizer/normalizer.go`

**역할**: 원본 트랜잭션을 gRPC sidecar로 디코딩.

**gRPC 연결:**

```go
// normalizer.go:51-54
conn, err := grpc.NewClient(
    n.sidecarAddr,
    grpc.WithTransportCredentials(insecure.NewCredentials()),
)
client := sidecarv1.NewChainDecoderClient(conn)  // 모든 worker가 공유
```

- 단일 gRPC 연결을 모든 worker가 공유 (gRPC는 멀티플렉싱 지원)
- 기본 2 workers (`NORMALIZER_WORKERS` 환경변수)
- 배치별 timeout: `SIDECAR_TIMEOUT_SEC` (기본 30초, `normalizer.go:107`)

**Response 변환 (`normalizer.go:134-176`):**
- `TransactionResult` → `NormalizedTransaction`
- `TransferInfo` → `NormalizedTransfer`
- `block_time` (unix timestamp) → `time.Time` 변환
- `chain_data` JSONB 구성: `from_ata`, `to_ata`, `transfer_type`

### 3.6 Ingester

**파일**: `internal/pipeline/ingester/ingester.go`

**역할**: 정규화된 데이터를 PostgreSQL에 원자적으로 기록. **단일 writer**.

> 다이어그램: `docs/diagrams/ingester-tx-boundary.drawio`

**트랜잭션 경계 (`processBatch`, ingester.go:74-243):**

```
BEGIN sql.Tx
  ├── defer Rollback (에러 시 자동 롤백)
  │
  ├── FOR EACH NormalizedTransaction:
  │   ├── Step 1: Upsert transaction (ON CONFLICT chain/network/tx_hash)
  │   │          → returns txID (UUID)
  │   │
  │   ├── FOR EACH NormalizedTransfer:
  │   │   ├── Step 2a: Upsert token (ON CONFLICT chain/network/contract_address)
  │   │   │           → returns tokenID (UUID)
  │   │   ├── Step 2b: Determine direction
  │   │   │           watched_address == to_address → DEPOSIT
  │   │   │           watched_address == from_address → WITHDRAWAL
  │   │   ├── Step 2c: Upsert transfer (ON CONFLICT DO NOTHING)
  │   │   └── Step 2d: Adjust balance (amount + delta, GREATEST cursor)
  │   │
  │   └── Step 3: Deduct fee
  │              조건: fee_payer == watched_address
  │                    AND fee_amount != "0"
  │                    AND status == "SUCCESS"
  │              → native token upsert + balance -= fee
  │
  ├── Step 4: Update cursor (cursor_value, cursor_sequence, items_processed += N)
  ├── Step 5: Update watermark (GREATEST ingested_sequence)
  └── COMMIT
```

**주요 설계 결정:**
- 에러 시 전체 배치 롤백 (부분 커밋 없음)
- 로그 후 다음 배치 처리 계속 (파이프라인 중단 없음, `ingester.go:64-69`)
- `negateAmount()`: Go `math/big.Int`로 문자열 금액 부호 반전 (`ingester.go:245-250`)

---

## 4. 데이터베이스 스키마 설계

### 4.1 스키마 전략

통합 테이블 + JSONB. AS-IS 모델과의 상세 비교는 [DB 모델 변경 비교](db-migration-rationale.md) 참조.

### 4.2 파이프라인 상태 테이블 (4개)

**`indexer_configs`** — 체인/네트워크별 인덱서 설정:

| 컬럼 | 타입 | 설명 |
|------|------|------|
| chain, network | VARCHAR(20) | 복합 유니크 키 |
| is_active | BOOLEAN | 인덱서 활성 여부 |
| target_batch_size | INT | 기본 100 |
| indexing_interval_ms | INT | 기본 5000ms |
| chain_config | JSONB | 체인별 설정 |

**`watched_addresses`** — 모니터링 대상 주소:

| 컬럼 | 타입 | 설명 |
|------|------|------|
| chain, network, address | 복합 유니크 키 | 주소 식별 |
| wallet_id | VARCHAR(100) | 고객 식별자 |
| organization_id | VARCHAR(100) | 사업 단위 |
| is_active | BOOLEAN | 부분 인덱스 사용 (`WHERE is_active = true`) |
| source | VARCHAR(20) | `"db"` 또는 `"env"` |

**`address_cursors`** — 주소별 페이지네이션 상태:

| 컬럼 | 타입 | 설명 |
|------|------|------|
| cursor_value | VARCHAR(128) | 마지막 처리 서명 (Solana signature) |
| cursor_sequence | BIGINT | 마지막 처리 슬롯/블록 |
| items_processed | BIGINT | 누적 처리 건수 |
| FK | (chain, network, address) → watched_addresses | 참조 무결성 |

**`pipeline_watermarks`** — 글로벌 인덱싱 진행:

| 컬럼 | 타입 | 설명 |
|------|------|------|
| head_sequence | BIGINT | 온체인 최신 슬롯 |
| ingested_sequence | BIGINT | DB에 기록된 최신 슬롯 (GREATEST 보장) |

### 4.3 서빙 데이터 테이블 (4개)

**`tokens`** — 토큰 메타데이터:
- 유니크: `(chain, network, contract_address)`
- Solana 네이티브: `contract_address = '11111111111111111111111111111111'`

**`transactions`** — 모든 체인 트랜잭션:
- 유니크: `(chain, network, tx_hash)`
- `chain_data JSONB`: 체인별 확장 데이터

**`transfers`** — 개별 이체 내역:
- 유니크: `(chain, network, tx_hash, instruction_index, watched_address)`
- `direction`: `DEPOSIT` / `WITHDRAWAL` / `NULL`
- FK: `transaction_id → transactions(id)`, `token_id → tokens(id)`

**`balances`** — 주소/토큰별 현재 잔액:
- 유니크: `(chain, network, address, token_id)`
- `amount NUMERIC(78,0)`: 누적 조정으로 갱신

### 4.4 인덱싱 전략

```sql
-- 활성 주소 빠른 조회
CREATE INDEX idx_watched_addr_active ON watched_addresses (chain, network)
    WHERE is_active = true;

-- 트랜잭션 시계열 조회
CREATE INDEX idx_tx_chain_cursor ON transactions (chain, network, block_cursor);
CREATE INDEX idx_tx_block_time ON transactions (block_time);
CREATE INDEX idx_tx_fee_payer ON transactions (fee_payer);

-- Transfer 다차원 조회
CREATE INDEX idx_transfers_watched ON transfers (chain, network, watched_address, block_time DESC);
CREATE INDEX idx_transfers_cursor ON transfers (chain, network, block_cursor);
CREATE INDEX idx_transfers_from ON transfers (from_address);
CREATE INDEX idx_transfers_to ON transfers (to_address);
CREATE INDEX idx_transfers_wallet ON transfers (wallet_id);
CREATE INDEX idx_transfers_token ON transfers (token_id);

-- 잔액 조회
CREATE INDEX idx_balances_wallet ON balances (wallet_id);
CREATE INDEX idx_balances_address ON balances (chain, network, address);
```

### 4.5 트랜잭션 무결성

**멱등 Upsert:**

| 테이블 | ON CONFLICT 행동 | 근거 |
|--------|-----------------|------|
| transactions | DO UPDATE SET chain = transactions.chain (no-op) | ID 반환 필요 |
| transfers | DO NOTHING | 완전 멱등 (중복 무시) |
| tokens | DO UPDATE SET ... RETURNING id | ID 반환 + 메타데이터 갱신 |
| balances | DO UPDATE SET amount = amount + delta | 누적 산술 연산 |
| cursors | DO UPDATE SET cursor_value = EXCLUDED | 진행 상태 갱신 |
| watermarks | DO UPDATE SET ingested_sequence = GREATEST(...) | 비퇴행 보장 |

**GREATEST() 워터마크** (`indexer_config_repo.go:61`):

```sql
ingested_sequence = GREATEST(pipeline_watermarks.ingested_sequence, $3)
```

재처리 시 워터마크가 역행하지 않음을 SQL 레벨에서 보장.

### 4.6 ERD

> 다이어그램: `docs/diagrams/database-erd.drawio`

```mermaid
erDiagram
    indexer_configs {
        UUID id PK
        VARCHAR chain
        VARCHAR network
        BOOLEAN is_active
        INT target_batch_size
        JSONB chain_config
    }

    watched_addresses {
        UUID id PK
        VARCHAR chain
        VARCHAR network
        VARCHAR address
        VARCHAR wallet_id
        BOOLEAN is_active
        VARCHAR source
    }

    address_cursors {
        UUID id PK
        VARCHAR chain
        VARCHAR network
        VARCHAR address
        VARCHAR cursor_value
        BIGINT cursor_sequence
        BIGINT items_processed
    }

    pipeline_watermarks {
        UUID id PK
        VARCHAR chain
        VARCHAR network
        BIGINT head_sequence
        BIGINT ingested_sequence
    }

    tokens {
        UUID id PK
        VARCHAR chain
        VARCHAR network
        VARCHAR contract_address
        VARCHAR symbol
        INT decimals
        VARCHAR token_type
    }

    transactions {
        UUID id PK
        VARCHAR chain
        VARCHAR network
        VARCHAR tx_hash
        BIGINT block_cursor
        NUMERIC fee_amount
        VARCHAR status
        JSONB chain_data
    }

    transfers {
        UUID id PK
        UUID transaction_id FK
        UUID token_id FK
        VARCHAR from_address
        VARCHAR to_address
        NUMERIC amount
        VARCHAR direction
        VARCHAR watched_address
        JSONB chain_data
    }

    balances {
        UUID id PK
        UUID token_id FK
        VARCHAR address
        NUMERIC amount
        BIGINT last_updated_cursor
    }

    watched_addresses ||--o{ address_cursors : "FK (chain,network,address)"
    transactions ||--o{ transfers : "transaction_id"
    tokens ||--o{ transfers : "token_id"
    tokens ||--o{ balances : "token_id"
```

---

## 5. 동시성 모델

### 5.1 고루틴 토폴로지

> 다이어그램: `docs/diagrams/goroutine-topology.drawio`

```mermaid
graph TB
    main["main()"]

    subgraph eg1["errgroup (main.go:106)"]
        health["Health Server<br/>goroutine"]
        pipeline_run["Pipeline.Run()"]
        signal["Signal Handler<br/>SIGINT/SIGTERM"]
    end

    subgraph eg2["errgroup (pipeline.go:109)"]
        coord["Coordinator<br/>1 goroutine"]
        fetcher_run["Fetcher.Run()"]
        norm_run["Normalizer.Run()"]
        ingest_run["Ingester<br/>1 goroutine"]
    end

    subgraph wg1["sync.WaitGroup (fetcher.go:40)"]
        fw1["Fetcher Worker 0"]
        fw2["Fetcher Worker 1"]
        fwN["Fetcher Worker N..."]
    end

    subgraph wg2["sync.WaitGroup (normalizer.go:62)"]
        nw1["Normalizer Worker 0"]
        nw2["Normalizer Worker 1"]
        nwN["Normalizer Worker N..."]
    end

    main --> eg1
    pipeline_run --> eg2
    fetcher_run --> wg1
    norm_run --> wg2
```

**총 고루틴 수 (기본 설정):**
- main: 1
- errgroup (outer): health(1) + pipeline(1) + signal(1) = 3
- errgroup (inner): coordinator(1) + fetcher(1) + normalizer(1) + ingester(1) = 4
- WaitGroup: fetcher workers(2) + normalizer workers(2) = 4
- **합계: ~12 고루틴**

### 5.2 Context 전파 및 알려진 갭

**정상 전파 경로:**

```
main() context.WithCancel
  └── errgroup.WithContext (main.go:106)
       ├── health server (gCtx)
       ├── pipeline.Run(gCtx) → errgroup.WithContext (pipeline.go:109)
       │    ├── coordinator.Run(gCtx) ← ctx.Done() 감지
       │    ├── fetcher.Run(gCtx) ← ctx.Done() 감지
       │    ├── normalizer.Run(gCtx) ← ctx.Done() 감지
       │    └── ingester.Run(gCtx) ← ctx.Done() 감지
       └── signal handler → cancel()
```

**알려진 갭: Fetcher/Normalizer의 `sync.WaitGroup` 문제:**

```go
// fetcher.go:37-52
func (f *Fetcher) Run(ctx context.Context) error {
    var wg sync.WaitGroup
    for i := 0; i < f.workerCount; i++ {
        wg.Add(1)
        go func(workerID int) {
            defer wg.Done()
            f.worker(ctx, workerID)
        }(i)
    }
    wg.Wait()           // ← ctx 취소되어도 worker가 끝날 때까지 대기
    return ctx.Err()     // ← errgroup에 에러 전파
}
```

**문제**: `wg.Wait()`는 context를 감시하지 않는다. worker 내부에서 `ctx.Done()`을 감지하여
반환하지만, 진행 중인 RPC 호출이 끝나야 worker가 종료된다.

**영향**: Graceful shutdown 시 진행 중인 RPC 호출 완료까지 지연 가능.

**해결 방향**: `errgroup` 으로 worker 관리 교체, 또는 worker 내부에서 context가 취소된
RPC 호출을 즉시 중단.

### 5.3 Worker Pool 패턴

Fetcher와 Normalizer 모두 **공유 채널 기반 공정 분배** 패턴:

```go
// N workers가 동일 채널에서 읽기
for {
    select {
    case <-ctx.Done():
        return
    case job, ok := <-f.jobCh:    // Go 런타임이 공정 분배
        if !ok { return }
        f.processJob(ctx, log, job)
    }
}
```

- Go 채널의 FIFO + 공정 분배 특성 활용
- 별도 작업 분배 로직 불필요
- 느린 worker가 있어도 다른 worker가 자동 보상

### 5.4 Backpressure 전파 경로

```
Ingester (느림)
  ↑ normalizedCh 버퍼(10) 가득 참
Normalizer worker (블로킹)
  ↑ rawBatchCh 버퍼(10) 가득 참
Fetcher worker (블로킹)
  ↑ jobCh 버퍼(10) 가득 참
Coordinator tick (블로킹)
  ↑ 다음 tick까지 대기
```

최대 버퍼: 10 (FetchJob) + 10 (RawBatch) + 10 (NormalizedBatch) = **30 배치**

---

## 6. Cross-Process 통신: gRPC Sidecar 프로토콜

### 6.1 Proto 정의

**파일**: `proto/sidecar/v1/decoder.proto`

```protobuf
service ChainDecoder {
  rpc DecodeSolanaTransactionBatch(DecodeSolanaTransactionBatchRequest)
      returns (DecodeSolanaTransactionBatchResponse);
  rpc HealthCheck(HealthCheckRequest) returns (HealthCheckResponse);
}
```

**핵심 메시지:**

| 메시지 | 필드 | 설명 |
|--------|------|------|
| `RawTransaction` | signature, raw_json (bytes) | 원본 트랜잭션 JSON |
| `TransactionResult` | tx_hash, block_cursor, fee_amount, status, transfers[] | 디코딩 결과 |
| `TransferInfo` | from_address, to_address (owner), from_ata, to_ata, amount, contract_address | 이체 상세 |
| `DecodeError` | signature, error | 디코딩 실패 건 |

### 6.2 전송 설정

```go
// normalizer.go:51-54
conn, err := grpc.NewClient(
    n.sidecarAddr,                                    // "localhost:50051"
    grpc.WithTransportCredentials(insecure.NewCredentials()), // TLS 없음
)
```

- Insecure 연결 (컨테이너 네트워크 내부 통신 전제)
- 단일 연결, gRPC 멀티플렉싱으로 동시 호출
- 배치별 timeout: 30초 기본

### 6.3 Sidecar 내부

**Instruction 평탄화** (`transaction_decoder.ts:flattenInstructions`):

```
Input:
  outer instructions: [A, B, C]
  inner instructions: [{index:0, instructions:[A1,A2]}, {index:2, instructions:[C1]}]

Output (global index):
  [{A, 0}, {A1, 1}, {A2, 2}, {B, 3}, {C, 4}, {C1, 5}]
```

- outer instruction 순회 → 해당 inner instructions 삽입 → 다음 outer
- global index 연속 증가 (transfers의 `instruction_index`로 사용)

**Program 분기** (`transaction_decoder.ts:decodeSolanaTransaction`):

| Program ID | 파서 | 처리 타입 |
|-----------|------|----------|
| `TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA` (SPL Token) | `spl_token_parser.ts` | transfer, transferChecked |
| `TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb` (Token-2022) | `spl_token_parser.ts` | transfer, transferChecked |
| `11111111111111111111111111111111` (System) | `system_parser.ts` | systemTransfer, createAccount |

**ATA → Owner 해석:**
1. `info.authority` 필드 (우선) → owner 주소
2. `meta.preTokenBalances` / `meta.postTokenBalances` 조회 (fallback)
3. ATA 주소 자체 (최종 fallback)

**Watched Address 필터링:**
- `Set<string>` 으로 O(1) lookup
- `from_address` 또는 `to_address`가 watched set에 포함된 transfer만 반환

### 6.4 에러 핸들링

| 수준 | 처리 | 영향 |
|------|------|------|
| 배치 전체 | gRPC INTERNAL 에러 반환 | Normalizer가 에러 로그 후 드랍 |
| 개별 트랜잭션 | `DecodeError[]`에 추가 | 나머지 트랜잭션 정상 처리 |
| 실패 트랜잭션 (meta.err != null) | 조기 반환 (transfers 빈 배열) | fee 정보만 기록 |

---

## 7. Chain Adapter 패턴 및 확장성

### 7.1 ChainAdapter 인터페이스

**파일**: `internal/chain/adapter.go:10-23`

```go
type ChainAdapter interface {
    Chain() string
    GetHeadSequence(ctx context.Context) (int64, error)
    FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]SignatureInfo, error)
    FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error)
}
```

- 4개 메서드로 모든 체인 추상화
- `SignatureInfo`: `Hash` (tx_hash), `Sequence` (block_cursor), `Time` (block_time)
- 파이프라인 코어는 이 인터페이스만 의존

### 7.2 Solana 구현 상세

**파일**: `internal/chain/solana/adapter.go`

**페이지네이션** (`adapter.go:44-114`):
1. `getSignaturesForAddress(address, {until: cursor, limit: min(remaining, 1000)})` 반복 호출
2. RPC 결과는 newest-first → 전체 수집 후 reverse하여 oldest-first 정렬
3. `before` 파라미터로 다음 페이지 요청

**병렬 tx fetch** (`adapter.go:117-157`):
```go
sem := make(chan struct{}, maxConcurrentTxs)  // maxConcurrentTxs = 10
```
- semaphore 패턴으로 최대 10개 동시 RPC 호출
- 결과 배열 인덱스 보존 (순서 유지)
- 첫 에러 발생 시 기록하고 나머지 완료 대기 후 에러 반환

**Commitment Level**: `"confirmed"` (finalized보다 빠르지만, 극히 드물게 되돌릴 수 있음)

### 7.3 EVM 체인 추가 가이드

새 체인을 추가하려면 다음 6단계를 따른다:

**Step 1: ChainAdapter 구현**

```go
// internal/chain/ethereum/adapter.go
package ethereum

type Adapter struct {
    client *ethclient.Client
    logger *slog.Logger
}

var _ chain.ChainAdapter = (*Adapter)(nil)

func (a *Adapter) Chain() string { return "ethereum" }

func (a *Adapter) GetHeadSequence(ctx context.Context) (int64, error) {
    header, err := a.client.HeaderByNumber(ctx, nil)
    return header.Number.Int64(), err
}

func (a *Adapter) FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]chain.SignatureInfo, error) {
    // EVM: block range scan → transaction hash 추출
    // cursor = block number (string)
    // block range: cursor+1 ~ latest
}

func (a *Adapter) FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error) {
    // eth_getTransactionReceipt for each hash
}
```

**Step 2: Sidecar에 디코더 추가**

```typescript
// sidecar/src/decoder/ethereum/transaction_decoder.ts
export function decodeEvmTransaction(txReceipt: any, signature: string, watchedAddresses: Set<string>): TransactionResult {
    // ERC-20 Transfer event 파싱 (topic[0] = 0xddf2...)
    // ERC-721 Transfer event 파싱
    // Native ETH transfer 파싱
}
```

**Step 3: Proto에 RPC 추가**

```protobuf
service ChainDecoder {
    rpc DecodeSolanaTransactionBatch(...) returns (...);
    rpc DecodeEvmTransactionBatch(DecodeEvmTransactionBatchRequest) returns (DecodeEvmTransactionBatchResponse);
}
```

**Step 4: chain_data JSONB 구조 정의**

```json
{
    "gas_price": "20000000000",
    "gas_used": "21000",
    "log_index": 0,
    "transfer_standard": "ERC20"
}
```

**Step 5: main.go에서 adapter 등록**

```go
// 체인별 분기 또는 레지스트리 패턴
adapter := ethereum.NewAdapter(cfg.Ethereum.RPCURL, logger)
```

**Step 6: Normalizer에서 chain별 gRPC 메서드 분기**

```go
switch batch.Chain {
case model.ChainSolana:
    resp, err = client.DecodeSolanaTransactionBatch(...)
case model.ChainEthereum:
    resp, err = client.DecodeEvmTransactionBatch(...)
}
```

---

## 8. 장애 모드 및 복구

### 8.1 RPC 장애

- **증상**: Fetcher에서 HTTP 에러 또는 timeout
- **처리**: 에러 로그 → 해당 FetchJob 드랍 → 커서 미전진
- **복구**: 다음 Coordinator tick에서 같은 주소/커서로 재시도
- **알려진 갭**: 재시도 로직 없음 (지수 백오프 미구현)

### 8.2 Sidecar 장애

- **증상**: gRPC 연결 실패 또는 timeout (30초)
- **처리**: 에러 로그 → 해당 RawBatch 드랍
- **복구**: 다음 Coordinator tick에서 같은 데이터 재처리
- **알려진 갭**: DLQ(Dead Letter Queue) 없음, 드랍된 배치 추적 불가

### 8.3 DB 장애

- **증상**: `sql.Tx` 에러 (연결 끊김, 제약 위반 등)
- **처리**: `defer dbTx.Rollback()` → 전체 배치 롤백
- **복구**: 커서가 전진하지 않았으므로 다음 tick에서 재처리
- **영향**: 해당 배치의 모든 변경 소실 (원자적 롤백)

### 8.4 프로세스 크래시

- **증상**: OOM, SIGKILL, 패닉 등
- **복구**: 커서 기반 at-least-once 재처리
  - 커서는 COMMIT 내에서만 전진 (`ingester.go:210-220`)
  - 크래시 시 마지막 커밋된 커서부터 재시작
  - `ON CONFLICT DO NOTHING`으로 중복 transfer 무시

### 8.5 데이터 정합성 보장

| 보장 | 메커니즘 | 코드 위치 |
|------|---------|----------|
| 멱등 upsert | `ON CONFLICT` | 각 repo의 UpsertTx |
| 단일 writer | Ingester 1 goroutine | `ingester.go:52` |
| 원자적 배치 | `sql.Tx` + `defer Rollback` | `ingester.go:75-79` |
| 커서 원자적 전진 | 같은 `sql.Tx` 내 | `ingester.go:211-220` |
| 워터마크 비퇴행 | `GREATEST()` | `indexer_config_repo.go:61` |
| 잔액 산술 정합 | `amount + $7::numeric` | `balance_repo.go:27` |

---

## 9. 운영 특성

### 9.1 Health Check

```go
// main.go:159-183
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
})
```

- HTTP GET `/healthz` → 200 OK
- 기본 포트: 8080 (`HEALTH_PORT`)
- Kubernetes liveness/readiness probe 호환

### 9.2 로깅

- **라이브러리**: `log/slog` (Go 표준)
- **포맷**: JSON (`slog.NewJSONHandler`, `main.go:39`)
- **레벨**: `LOG_LEVEL` 환경변수 (debug/info/warn/error)
- **스코프**: 컴포넌트별 `.With("component", "xxx")`
  - coordinator, fetcher, normalizer, ingester, solana

### 9.3 설정

2개 소스:
1. **환경변수**: `config.Load()` (`config.go`)
2. **DB 테이블**: `indexer_configs`, `watched_addresses`

환경변수 우선 → DB 동기화 (`main.go:138-157`, `syncWatchedAddresses`)

### 9.4 배포

**Docker Compose** (`deployments/docker-compose.yaml`):

| 서비스 | 이미지 | 포트 | 역할 |
|--------|-------|------|------|
| postgres | postgres:16-alpine | 5433:5432 | 관계형 DB |
| redis | redis:7-alpine | 6380:6379 | 향후 사용 |
| migrate | migrate/migrate:v4.17.0 | — | 스키마 마이그레이션 |
| sidecar | (빌드) | 50051:50051 | gRPC 디코더 |

### 9.5 Graceful Shutdown 시퀀스

> 다이어그램: `docs/diagrams/graceful-shutdown.drawio`

```mermaid
sequenceDiagram
    participant OS as OS Signal
    participant SH as Signal Handler
    participant Ctx as Context
    participant C as Coordinator
    participant F as Fetcher
    participant N as Normalizer
    participant I as Ingester
    participant HS as Health Server

    OS->>SH: SIGINT/SIGTERM
    SH->>Ctx: cancel()

    par Context 전파
        Ctx->>C: ctx.Done()
        C->>C: ticker.Stop(), return
    and
        Ctx->>F: ctx.Done()
        F->>F: worker select 탈출
        F->>F: wg.Wait()
    and
        Ctx->>N: ctx.Done()
        N->>N: worker select 탈출
        N->>N: wg.Wait()
    and
        Ctx->>I: ctx.Done()
        I->>I: 현재 배치 완료 후 return
    and
        Ctx->>HS: ctx.Done()
        HS->>HS: server.Shutdown(5s timeout)
    end

    Note over SH: errgroup.Wait() 완료
    Note over SH: db.Close()
    Note over SH: exit(0)
```

---

## 10. 알려진 제약 및 로드맵

### 10.1 현재 아키텍처 갭

| # | 갭 | 영향도 | 카테고리 |
|---|------|-------|---------|
| 1 | 재시도 로직 없음 (RPC, gRPC) | 높음 | 안정성 |
| 2 | DLQ 없음 (실패 배치 추적 불가) | 높음 | 운영성 |
| 3 | 메트릭/모니터링 없음 (Prometheus) | 높음 | 운영성 |
| 4 | Fetcher/Normalizer WaitGroup context 갭 | 중간 | 안정성 |
| 5 | gRPC insecure 연결 | 중간 | 보안 |
| 6 | 단일 인스턴스 (수평 스케일링 불가) | 중간 | 확장성 |
| 7 | Sidecar ATA→Owner 해석 불완전 | 낮음 | 정확성 |
| 8 | SPL 토큰 메타데이터 없음 (symbol/name) | 낮음 | 기능 |
| 9 | Rate limiting 없음 (RPC 호출) | 중간 | 안정성 |
| 10 | 테스트 커버리지 0% | 높음 | 품질 |

### 10.2 단기 개선 (프로덕션 전)

1. **재시도 로직**: 지수 백오프 + jitter (RPC, gRPC 호출)
2. **메트릭**: Prometheus 엔드포인트 (배치 처리량, 지연, 에러율, 잔액)
3. **테스트**: 핵심 경로 단위 테스트 + integration 테스트 ([테스트 방법론](testing.md))
4. **Rate limiting**: RPC 호출 속도 제한 (devnet 제한 준수)
5. **WaitGroup → errgroup**: context 전파 갭 해결

### 10.3 중기 (멀티체인 대비)

1. **EVM ChainAdapter**: Ethereum, Polygon, Arbitrum 지원
2. **Sidecar 디코더 확장**: ERC-20/ERC-721 Transfer 이벤트 파싱
3. **chain_data 스키마 정의**: 체인별 JSONB 구조 문서화
4. **다중 파이프라인**: 체인별 독립 파이프라인 인스턴스

### 10.4 장기 (프로덕션 스케일)

1. **Redis Streams**: 프로세스 분리 (Fetcher → Redis → Normalizer)
2. **수평 스케일링**: 주소 범위별 파티셔닝
3. **CDC (Change Data Capture)**: DB 변경 스트림 → 외부 소비자
4. **Circuit Breaker**: RPC/sidecar 장애 격리
5. **분산 트레이싱**: OpenTelemetry

---

## 부록

### A. 환경변수 레퍼런스

| 변수 | 기본값 | 설명 |
|------|-------|------|
| `DB_URL` | `postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable` | PostgreSQL 연결 문자열 |
| `DB_MAX_OPEN_CONNS` | 25 | 최대 연결 수 |
| `DB_MAX_IDLE_CONNS` | 5 | 유휴 연결 수 |
| `DB_CONN_MAX_LIFETIME_MIN` | 30 | 연결 최대 수명 (분) |
| `REDIS_URL` | `redis://localhost:6380` | Redis 연결 (향후) |
| `SIDECAR_ADDR` | `localhost:50051` | gRPC sidecar 주소 |
| `SIDECAR_TIMEOUT_SEC` | 30 | gRPC 호출 timeout (초) |
| `SOLANA_RPC_URL` | `https://api.devnet.solana.com` | Solana RPC 엔드포인트 |
| `SOLANA_NETWORK` | `devnet` | 네트워크 식별자 |
| `FETCH_WORKERS` | 2 | Fetcher 동시 worker 수 |
| `NORMALIZER_WORKERS` | 2 | Normalizer 동시 worker 수 |
| `BATCH_SIZE` | 100 | 배치당 서명 수 |
| `INDEXING_INTERVAL_MS` | 5000 | Coordinator tick 간격 (ms) |
| `CHANNEL_BUFFER_SIZE` | 10 | 채널 버퍼 크기 |
| `HEALTH_PORT` | 8080 | Health check 포트 |
| `LOG_LEVEL` | `info` | 로그 레벨 |
| `WATCHED_ADDRESSES` | — | 모니터링 주소 (쉼표 구분) |

### B. Proto API 레퍼런스

```protobuf
service ChainDecoder {
    // Solana 트랜잭션 배치 디코딩
    rpc DecodeSolanaTransactionBatch(DecodeSolanaTransactionBatchRequest)
        returns (DecodeSolanaTransactionBatchResponse);

    // 헬스 체크
    rpc HealthCheck(HealthCheckRequest)
        returns (HealthCheckResponse);
}
```

**Request/Response 크기:**
- Request: N × (signature 88 bytes + raw_json ~2-10 KB) + watched_addresses
- Response: N × TransactionResult (~500 bytes) + M × TransferInfo (~200 bytes)
- 일반적 배치: 5-100 트랜잭션

### C. 프로젝트 디렉토리 구조

```
koda-custody-indexer/
├── cmd/indexer/
│   └── main.go                         # 엔트리포인트 (184줄)
├── internal/
│   ├── config/
│   │   └── config.go                   # 환경변수 로딩
│   ├── domain/
│   │   ├── event/
│   │   │   ├── fetch_job.go            # FetchJob 이벤트
│   │   │   ├── raw_batch.go            # RawBatch 이벤트
│   │   │   └── normalized_batch.go     # NormalizedBatch 이벤트
│   │   └── model/
│   │       ├── chain.go                # 열거형 (Chain, Network, Status 등)
│   │       ├── transaction.go          # Transaction 모델
│   │       ├── transfer.go             # Transfer 모델
│   │       ├── token.go                # Token 모델
│   │       ├── cursor.go               # AddressCursor, IndexerConfig, Watermark
│   │       ├── watched_address.go      # WatchedAddress 모델
│   │       └── balance.go              # Balance 모델
│   ├── chain/
│   │   ├── adapter.go                  # ChainAdapter 인터페이스 (31줄)
│   │   └── solana/
│   │       ├── adapter.go              # Solana 구현 (158줄)
│   │       └── rpc/
│   │           ├── client.go           # JSON-RPC 클라이언트
│   │           ├── methods.go          # GetSlot, GetSignatures, GetTransaction
│   │           └── types.go            # RPC 타입 정의
│   ├── pipeline/
│   │   ├── pipeline.go                 # 오케스트레이터 (126줄)
│   │   ├── coordinator/
│   │   │   └── coordinator.go          # Stage 1 (110줄)
│   │   ├── fetcher/
│   │   │   └── fetcher.go             # Stage 2 (133줄)
│   │   ├── normalizer/
│   │   │   └── normalizer.go          # Stage 3 (190줄)
│   │   └── ingester/
│   │       └── ingester.go            # Stage 4 (271줄)
│   └── store/
│       ├── redis/
│       │   └── stream.go              # Redis (향후)
│       └── postgres/
│           ├── db.go                  # 연결 풀
│           ├── migrations/
│           │   ├── 001_create_pipeline_tables.up.sql
│           │   ├── 001_create_pipeline_tables.down.sql
│           │   ├── 002_create_serving_tables.up.sql
│           │   └── 002_create_serving_tables.down.sql
│           ├── transaction_repo.go
│           ├── transfer_repo.go
│           ├── token_repo.go
│           ├── balance_repo.go
│           ├── cursor_repo.go
│           ├── watched_address_repo.go
│           └── indexer_config_repo.go
├── proto/sidecar/v1/
│   └── decoder.proto                  # gRPC 서비스 정의
├── pkg/generated/sidecar/v1/          # Go protobuf 생성 코드
├── sidecar/
│   ├── src/
│   │   ├── index.ts                   # gRPC 서버 시작
│   │   ├── server.ts                  # proto-loader, 핸들러
│   │   └── decoder/
│   │       ├── index.ts               # 배치 디스패치
│   │       └── solana/
│   │           ├── transaction_decoder.ts  # 메타데이터 + instruction 평탄화
│   │           ├── spl_token_parser.ts     # SPL transfer 파싱
│   │           └── system_parser.ts        # SOL system transfer 파싱
│   ├── package.json
│   ├── tsconfig.json
│   └── Dockerfile
├── deployments/
│   └── docker-compose.yaml
├── docs/
│   ├── architecture.md                # 이 문서
│   ├── testing.md                     # 테스트 방법론
│   ├── db-migration-rationale.md      # DB 모델 변경 비교
│   └── diagrams/                      # draw.io 다이어그램
├── Makefile
├── go.mod
├── go.sum
└── CLAUDE.md
```
