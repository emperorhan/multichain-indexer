# 테스트 방법론: multichain-indexer

> 금융급 정합성을 보장하기 위한 테스트 전략 및 구체적 시나리오

---

## 1. 현황 및 목표

### 1.1 현재 상태 (2026-02-20)

- Go 테스트 파일: **63개** `*_test.go`
- 테스트 범위: unit, benchmark, integration, E2E (runtime), error scenario
- 전체 테스트 `-race` 통과
- 인터페이스 기반 mock 주입 완료 (ingester, coordinator, fetcher 등)
- sidecar Jest 테스트: 별도 구성

### 1.2 테스트 목표

| 목표 | 설명 |
|------|------|
| 금융급 정합성 | 잔액 계산 정확성, 멱등 처리, 누락/중복 방지 |
| 회귀 방지 | 코드 변경 시 기존 동작 보장 |
| 리팩토링 안전망 | 인터페이스 추출, 구조 변경 시 계약 보존 |
| 배포 신뢰도 | CI에서 자동 검증 후 배포 |

### 1.3 테스트 철학

**"Contract 테스트, Boundary Mock"**

- 컴포넌트의 **계약**(input → output)을 테스트
- 외부 의존성(RPC, gRPC, DB) 경계에서만 mock
- 내부 구현 세부사항은 테스트하지 않음
- Table-driven 테스트 패턴 선호

---

## 2. 테스트 피라미드

```
        ╱ ╲
       ╱ E2E ╲        10% — 전체 파이프라인 + testcontainers
      ╱───────╲
     ╱ Integ.  ╲       20% — 실제 PostgreSQL + 리포지토리
    ╱───────────╲
   ╱    Unit     ╲      70% — 순수 함수, 비즈니스 로직
  ╱───────────────╲
```

### 2.1 Unit (70%)

- 순수 함수, 비즈니스 로직, 파서
- mock 또는 fake 의존성
- `go test -short` 로 빠르게 실행
- 목표 커버리지: Go 70%, Sidecar 80%

### 2.2 Integration (20%)

- 실제 PostgreSQL (testcontainers)
- 리포지토리 round-trip 테스트
- gRPC sidecar 통합 테스트
- `go test -tags integration`

### 2.3 E2E / Pipeline (10%)

- 전체 파이프라인 (mock RPC + mock sidecar + 실제 DB)
- Shutdown / Backpressure 시나리오
- `go test -tags e2e`

### 2.4 테스트하지 않을 것

- protobuf 생성 코드 (`pkg/generated/`)
- 3rd party 라이브러리 내부 동작
- SQL 구문 자체 (integration 테스트에서 간접 검증)
- Docker 인프라 설정

---

## 3. 테스트 인프라 (구현 완료)

### 3.1 인터페이스 기반 의존성 주입

모든 파이프라인 스테이지가 **인터페이스**에 의존하도록 리팩토링 완료:

- Ingester: `TransactionRepository`, `BalanceEventRepository`, `BalanceRepository`, `TokenRepository`, `CursorRepository`, `WatermarkRepository`, `WatchedAddressRepository` 등
- Coordinator: `WatchedAddressRepository`, `CursorReader`
- Pipeline: `Repos` 구조체가 인터페이스 필드로 구성

Go implicit interface 패턴으로 `postgres.*Repo` 구현체가 자동으로 인터페이스를 만족한다.

### 3.2 Mock 패턴

테스트에서는 hand-written fake/mock을 사용:

```go
// 함수 필드 기반 mock — 테스트별 동작 커스터마이즈
type mockBalanceEventRepo struct {
    BulkUpsertTxFunc func(ctx context.Context, tx *sql.Tx, events []model.BalanceEvent) error
}
```

별도의 mockgen 도구 없이 테스트 파일 내에서 직접 정의하는 방식을 선호한다.

---

## 4. Unit 테스트 — 컴포넌트별 상세 시나리오

### 4.1 Config 로딩

**파일**: `internal/config/config_test.go`

| # | 케이스 | 입력 | 기대 출력 |
|---|--------|------|----------|
| 1 | 기본값 로딩 | 환경변수 없음 | DB_URL="postgres://...", FETCH_WORKERS=2 등 |
| 2 | 환경변수 오버라이드 | `FETCH_WORKERS=4` | cfg.Pipeline.FetchWorkers == 4 |
| 3 | 주소 파싱 | `WATCHED_ADDRESSES=A,B,C` | len(cfg.Pipeline.WatchedAddresses) == 3 |
| 4 | 빈 주소 | `WATCHED_ADDRESSES=` | len(cfg.Pipeline.WatchedAddresses) == 0 |
| 5 | 유효하지 않은 정수 | `FETCH_WORKERS=abc` | 기본값 사용 또는 에러 |
| 6 | 로그 레벨 파싱 | `LOG_LEVEL=debug` | cfg.Log.Level == "debug" |

### 4.2 Coordinator

**파일**: `internal/pipeline/coordinator/coordinator_test.go`

| # | 케이스 | Setup | Assert |
|---|--------|-------|--------|
| 1 | 빈 주소 목록 | GetActive → [] | jobCh에 아무것도 전송 안 됨 |
| 2 | 정상 주소 1개 | GetActive → [addr1], Get cursor → nil | FetchJob{Address:addr1, CursorValue:nil} |
| 3 | 커서 있는 주소 | Get cursor → {CursorValue: "sig123"} | FetchJob{CursorValue: &"sig123"} |
| 4 | 커서 조회 에러 | Get cursor → error | 해당 주소 스킵, 로그 출력 |
| 5 | 다중 주소 | GetActive → [addr1, addr2] | 2개 FetchJob 전송 |
| 6 | context 취소 | cancel() 호출 | Run() 반환, ctx.Err() |
| 7 | WalletID/OrgID 전파 | WatchedAddress에 wallet/org 설정 | FetchJob에 동일 값 |
| 8 | 주기적 실행 | interval=10ms, 50ms 대기 | tick 최소 4회 이상 |

```go
func TestCoordinator_EmptyAddresses(t *testing.T) {
    mockWatched := &mockWatchedAddrRepo{
        getActiveFunc: func(ctx context.Context, chain model.Chain, network model.Network) ([]model.WatchedAddress, error) {
            return nil, nil
        },
    }
    mockCursor := &mockCursorRepo{}

    jobCh := make(chan event.FetchJob, 10)
    coord := coordinator.New(model.ChainSolana, model.NetworkDevnet, mockWatched, mockCursor, 100, time.Second, jobCh, slog.Default())

    ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
    defer cancel()

    coord.Run(ctx)
    assert.Equal(t, 0, len(jobCh))
}
```

### 4.3 Fetcher

**파일**: `internal/pipeline/fetcher/fetcher_test.go`

| # | 케이스 | Setup | Assert |
|---|--------|-------|--------|
| 1 | 빈 시그니처 | FetchNewSignatures → [] | rawBatchCh에 아무것도 전송 안 됨 |
| 2 | 정상 배치 | 3 sigs + 3 txs | RawBatch{len(RawTransactions)==3} |
| 3 | 커서 설정 | cursor="sig_newest" | NewCursorValue 확인 |
| 4 | 에러 전파 (signatures) | FetchNewSignatures → error | processJob error 반환, 로그 |
| 5 | 에러 전파 (transactions) | FetchTransactions → error | processJob error 반환 |
| 6 | oldest-first 정렬 | sigs 역순 반환 | 결과 oldest-first 확인 |

### 4.4 Normalizer

**파일**: `internal/pipeline/normalizer/normalizer_test.go`

| # | 케이스 | Setup | Assert |
|---|--------|-------|--------|
| 1 | 정상 gRPC 응답 | mock sidecar 1 result | NormalizedBatch{len(Transactions)==1} |
| 2 | 시간 변환 | block_time=1700000000 | BlockTime == 2023-11-14T... |
| 3 | 시간 없음 | block_time=0 | BlockTime == nil |
| 4 | ChainData 구성 | from_ata, to_ata, transfer_type | chain_data JSON 확인 |
| 5 | 에러 로깅 | resp.Errors=[{sig, err}] | 로그 출력 (경고), 정상 처리 |
| 6 | gRPC timeout | sidecar 응답 지연 > timeout | error 반환 |
| 7 | context 취소 | cancel() 호출 | 블로킹 해제, return |

### 4.5 Ingester (핵심 — 15개 케이스)

**파일**: `internal/pipeline/ingester/ingester_test.go`

이 섹션은 가장 복잡한 비즈니스 로직을 포함하므로 상세하게 기술한다.

#### 4.5.1 Transaction Upsert

| # | 케이스 | Input | Assert |
|---|--------|-------|--------|
| 1 | 정상 tx upsert | NormalizedTx{TxHash:"sig1", Status:SUCCESS} | txRepo.UpsertTx 호출, txID 반환 |
| 2 | ChainData nil | ntx.ChainData = nil | txModel.ChainData = `{}` |
| 3 | upsert 에러 | txRepo.UpsertTx → error | processBatch error, rollback |

#### 4.5.2 Token Upsert

| # | 케이스 | Input | Assert |
|---|--------|-------|--------|
| 4 | 정상 token upsert | transfer with ContractAddress | tokenRepo.UpsertTx 호출 |
| 5 | defaultTokenSymbol (Native) | TokenType=NATIVE, TokenSymbol="" | symbol="SOL" |
| 6 | defaultTokenSymbol (Fungible) | TokenType=FUNGIBLE, TokenSymbol="" | symbol="UNKNOWN" |
| 7 | defaultTokenName (Native) | TokenType=NATIVE, TokenName="" | name="Solana" |

#### 4.5.3 Direction 추론

| # | 케이스 | Input | Assert |
|---|--------|-------|--------|
| 8 | DEPOSIT | watched_address == to_address | direction = &DEPOSIT |
| 9 | WITHDRAWAL | watched_address == from_address | direction = &WITHDRAWAL |
| 10 | UNKNOWN | watched ≠ from ≠ to (edge case) | direction = nil |

```go
func TestIngester_DirectionDeposit(t *testing.T) {
    batch := event.NormalizedBatch{
        Address: "watched_addr_1",
        Transactions: []event.NormalizedTransaction{{
            TxHash: "sig1", Status: model.TxStatusSuccess,
            Transfers: []event.NormalizedTransfer{{
                FromAddress: "sender_addr",
                ToAddress:   "watched_addr_1",   // == batch.Address → DEPOSIT
                Amount:      "1000000000",        // 1 SOL
            }},
        }},
    }
    // ... assert direction == &model.DirectionDeposit
}
```

#### 4.5.4 Balance 조정

| # | 케이스 | Input | Assert |
|---|--------|-------|--------|
| 11 | 입금 잔액 | direction=DEPOSIT, amount="100" | AdjustBalanceTx(delta="100") |
| 12 | 출금 잔액 | direction=WITHDRAWAL, amount="100" | AdjustBalanceTx(delta="-100") |

#### 4.5.5 수수료 차감

| # | 케이스 | Input | Assert |
|---|--------|-------|--------|
| 13 | 수수료 차감 조건 충족 | fee_payer==watched, fee≠"0", status∈{SUCCESS,FAILED} | native token upsert + balance -= fee |
| 14 | 수수료 조건 불충족 (메타데이터 불완전) | fee_payer 없음 또는 fee="0" | 수수료 차감 안 함 |

#### 4.5.6 트랜잭션 무결성

| # | 케이스 | Input | Assert |
|---|--------|-------|--------|
| 15 | 에러 시 롤백 | 중간 단계에서 에러 주입 | dbTx.Rollback 호출, 커서 미전진 |

#### negateAmount 헬퍼

```go
func TestNegateAmount(t *testing.T) {
    tests := []struct{ input, expected string }{
        {"1000000000", "-1000000000"},
        {"0", "0"},
        {"999999999999999999", "-999999999999999999"},
    }
    for _, tt := range tests {
        assert.Equal(t, tt.expected, negateAmount(tt.input))
    }
}
```

### 4.6 Solana Adapter

**파일**: `internal/chain/solana/adapter_test.go`

| # | 케이스 | Setup | Assert |
|---|--------|-------|--------|
| 1 | 정상 페이지네이션 | mock RPC: 2 pages × 3 sigs | 6 sigs, oldest-first |
| 2 | 단일 페이지 | mock RPC: 1 page, < batchSize | 정확한 sig 수 |
| 3 | 빈 결과 | mock RPC: [] | 빈 배열 반환 |
| 4 | 커서 until 전달 | cursor="sig_last" | RPC opts.Until == "sig_last" |
| 5 | oldest-first 정렬 | RPC newest-first | 결과 reverse 확인 |
| 6 | 병렬 tx fetch | 5 sigs | 5 tx results (순서 보존) |
| 7 | 병렬 fetch 에러 | 3번째 tx 에러 | firstErr 반환 |
| 8 | maxConcurrentTxs | 15 sigs | semaphore 10 제한 |
| 9 | Chain() 반환값 | — | "solana" |

### 4.7 Solana RPC Client

**파일**: `internal/chain/solana/rpc/client_test.go`

| # | 케이스 | Setup | Assert |
|---|--------|-------|--------|
| 1 | 정상 JSON-RPC 응답 | mock HTTP: `{jsonrpc:"2.0", result:...}` | 파싱된 결과 |
| 2 | RPC 에러 응답 | mock HTTP: `{error:{code:-32600}}` | error 반환 |
| 3 | HTTP 에러 | mock HTTP: 500 Internal Server Error | error 반환 |
| 4 | request ID 증가 | 3회 호출 | ID 1, 2, 3 |
| 5 | GetTransaction 인코딩 | — | encoding="jsonParsed", commitment="confirmed" |

### 4.8 Sidecar 디코더 (Jest)

**파일**: `sidecar/src/decoder/solana/__tests__/transaction_decoder.test.ts`

| # | 카테고리 | 케이스 | 입력 | 기대 |
|---|---------|--------|------|------|
| 1 | SOL 전송 | systemTransfer | system program transfer | transferType="systemTransfer", amount=lamports |
| 2 | SOL 전송 | createAccount | system program createAccount | transferType="createAccount" |
| 3 | SPL transfer | transfer instruction | SPL Token transfer | transferType="transfer", fromAta/toAta |
| 4 | SPL transferChecked | transferChecked instruction | SPL Token transferChecked | transferType="transferChecked", mint, decimals |
| 5 | 실패 tx | meta.err != null | — | status="FAILED", transfers=[] |
| 6 | inner instruction | outer + inner instructions | — | 평탄화 후 모든 transfer 포함 |
| 7 | 0 lamports | lamports=0 | — | 빈 transfers (skip) |
| 8 | 필터링 | watched ∉ from/to | — | transfers에 미포함 |
| 9 | 필터링 (포함) | watched == to_address | — | transfers에 포함 |
| 10 | fee 추출 | meta.fee=5000 | — | feeAmount="5000" |
| 11 | fee_payer 추출 | accountKeys[0] | — | feePayer==accountKeys[0] |
| 12 | null response | parsed=null | — | error 배열에 추가 |
| 13 | authority 해석 | info.authority 존재 | — | fromAddress=authority (ATA 아님) |
| 14 | 다중 transfer | 1 tx에 3 transfer | — | results[0].transfers.length==3 |

**Golden file 테스트 (sidecar):**

```typescript
// sidecar/src/decoder/solana/__tests__/golden.test.ts
import fixture from '../../../testdata/sol_system_transfer.json';
import expected from '../../../testdata/sol_system_transfer.expected.json';

test('SOL system transfer golden', () => {
    const result = decodeSolanaTransaction(fixture, 'test_sig', new Set(['watched_addr']));
    expect(result).toEqual(expected);
});
```

---

## 5. Integration 테스트

### 5.1 리포지토리 통합 (testcontainers PostgreSQL)

**Setup:**

```go
// internal/store/postgres/testutil_test.go
func setupTestDB(t *testing.T) *postgres.DB {
    t.Helper()
    ctx := context.Background()

    container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: testcontainers.ContainerRequest{
            Image:        "postgres:16-alpine",
            ExposedPorts: []string{"5432/tcp"},
            Env: map[string]string{
                "POSTGRES_USER":     "test",
                "POSTGRES_PASSWORD": "test",
                "POSTGRES_DB":       "test",
            },
            WaitingFor: wait.ForListeningPort("5432/tcp"),
        },
        Started: true,
    })
    t.Cleanup(func() { container.Terminate(ctx) })

    // Run migrations
    // golang-migrate against container
    // Return connected DB
}
```

**리포지토리 테스트 시나리오:**

| # | 리포지토리 | 케이스 | Assert |
|---|-----------|--------|--------|
| 1 | TransactionRepo | Upsert → Get round-trip | 동일 데이터 |
| 2 | TransactionRepo | 동일 tx_hash 2회 upsert | 1행, 멱등 |
| 3 | TransferRepo | Upsert → 유니크 제약 확인 | ON CONFLICT DO NOTHING |
| 4 | TransferRepo | 동일 (tx_hash, instruction_index, watched_address) 2회 | 1행 |
| 5 | BalanceRepo | AdjustBalanceTx +100, +200 | amount == 300 |
| 6 | BalanceRepo | AdjustBalanceTx +100, -30 | amount == 70 |
| 7 | BalanceRepo | GREATEST cursor | 높은 cursor만 저장 |
| 8 | CursorRepo | UpsertTx items_processed 누적 | 10 + 5 = 15 |
| 9 | WatermarkRepo | UpdateWatermarkTx GREATEST | 비퇴행 확인 |
| 10 | WatchedAddressRepo | GetActive is_active=true만 | 비활성 제외 |
| 11 | TokenRepo | Upsert RETURNING id | 기존 행 ID 반환 |

**멱등성 테스트:**

```go
func TestTransactionRepo_IdempotentUpsert(t *testing.T) {
    db := setupTestDB(t)
    repo := postgres.NewTransactionRepo(db)

    tx := &model.Transaction{
        Chain: model.ChainSolana, Network: model.NetworkDevnet,
        TxHash: "sig_test_1", BlockCursor: 100, FeeAmount: "5000",
        FeePayer: "payer", Status: model.TxStatusSuccess,
        ChainData: json.RawMessage("{}"),
    }

    dbTx, _ := db.BeginTx(context.Background(), nil)

    id1, err := repo.UpsertTx(context.Background(), dbTx, tx)
    require.NoError(t, err)

    id2, err := repo.UpsertTx(context.Background(), dbTx, tx)
    require.NoError(t, err)

    assert.Equal(t, id1, id2) // 같은 ID 반환
    dbTx.Commit()

    // 행 수 확인
    var count int
    db.QueryRow("SELECT COUNT(*) FROM transactions WHERE tx_hash='sig_test_1'").Scan(&count)
    assert.Equal(t, 1, count)
}
```

**Balance 누적 정확성:**

```go
func TestBalanceRepo_AccumulateDelta(t *testing.T) {
    db := setupTestDB(t)
    repo := postgres.NewBalanceRepo(db)
    tokenID := seedToken(t, db)

    dbTx1, _ := db.BeginTx(context.Background(), nil)
    repo.AdjustBalanceTx(ctx, dbTx1, model.ChainSolana, model.NetworkDevnet,
        "addr1", tokenID, nil, nil, "1000000000", 100, "tx1") // +1 SOL
    dbTx1.Commit()

    dbTx2, _ := db.BeginTx(context.Background(), nil)
    repo.AdjustBalanceTx(ctx, dbTx2, model.ChainSolana, model.NetworkDevnet,
        "addr1", tokenID, nil, nil, "-300000000", 200, "tx2") // -0.3 SOL
    dbTx2.Commit()

    balances, _ := repo.GetByAddress(ctx, model.ChainSolana, model.NetworkDevnet, "addr1")
    assert.Equal(t, "700000000", balances[0].Amount) // 0.7 SOL
    assert.Equal(t, int64(200), balances[0].LastUpdatedCursor)
}
```

### 5.2 Sidecar gRPC 통합

| # | 케이스 | Setup | Assert |
|---|--------|-------|--------|
| 1 | 실제 fixture 디코딩 | SOL system transfer JSON | 정확한 TransactionResult |
| 2 | 대용량 배치 | 100 tx batch | 100 results (타임아웃 없음) |
| 3 | 비정상 JSON | `{invalid json` | DecodeError 반환 (크래시 아님) |
| 4 | null response | `null` JSON | DecodeError 반환 |

### 5.3 Ingester + PostgreSQL 통합 (핵심)

**파일**: `internal/pipeline/ingester/ingester_integration_test.go`

```go
//go:build integration

func TestIngester_ProcessBatch_Integration(t *testing.T) {
    db := setupTestDB(t)
    repos := createRepos(db)
    ing := ingester.New(db, repos..., make(chan event.NormalizedBatch, 1), slog.Default())

    // Seed watched address
    seedWatchedAddress(t, db, "watched_addr_1")

    batch := event.NormalizedBatch{
        Chain: model.ChainSolana, Network: model.NetworkDevnet,
        Address: "watched_addr_1",
        Transactions: []event.NormalizedTransaction{{
            TxHash: "sig1", BlockCursor: 100, FeeAmount: "5000",
            FeePayer: "watched_addr_1", Status: model.TxStatusSuccess,
            Transfers: []event.NormalizedTransfer{{
                InstructionIndex: 0, ContractAddress: "11111111111111111111111111111111",
                FromAddress: "sender", ToAddress: "watched_addr_1",
                Amount: "1000000000", TokenType: model.TokenTypeNative,
                TokenDecimals: 9,
            }},
        }},
        NewCursorValue: strPtr("sig1"), NewCursorSequence: 100,
    }

    err := ing.ProcessBatch(context.Background(), batch)
    require.NoError(t, err)

    // Assert: transaction
    var txCount int
    db.QueryRow("SELECT COUNT(*) FROM transactions WHERE tx_hash='sig1'").Scan(&txCount)
    assert.Equal(t, 1, txCount)

    // Assert: transfer with DEPOSIT direction
    var direction string
    db.QueryRow("SELECT direction FROM transfers WHERE tx_hash='sig1'").Scan(&direction)
    assert.Equal(t, "DEPOSIT", direction)

    // Assert: balance = 1 SOL - fee (0.000005 SOL)
    var amount string
    db.QueryRow("SELECT amount FROM balances WHERE address='watched_addr_1'").Scan(&amount)
    assert.Equal(t, "999995000", amount) // 1 SOL - 5000 lamports fee

    // Assert: cursor updated
    var cursorValue string
    db.QueryRow("SELECT cursor_value FROM address_cursors WHERE address='watched_addr_1'").Scan(&cursorValue)
    assert.Equal(t, "sig1", cursorValue)
}
```

**멱등성 integration 테스트:**

```go
func TestIngester_Idempotent_Integration(t *testing.T) {
    db := setupTestDB(t)
    // ... setup

    // 동일 배치 2회 처리
    err1 := ing.ProcessBatch(ctx, batch)
    require.NoError(t, err1)
    err2 := ing.ProcessBatch(ctx, batch)
    require.NoError(t, err2)

    // Assert: tx 1건, transfer 1건
    var txCount, transferCount int
    db.QueryRow("SELECT COUNT(*) FROM transactions WHERE tx_hash='sig1'").Scan(&txCount)
    db.QueryRow("SELECT COUNT(*) FROM transfers WHERE tx_hash='sig1'").Scan(&transferCount)
    assert.Equal(t, 1, txCount)
    assert.Equal(t, 1, transferCount)

    // Assert: balance 1배 (2배 아님)
    var amount string
    db.QueryRow("SELECT amount FROM balances WHERE address='watched_addr_1'").Scan(&amount)
    // 주의: 현재 구현에서 balance는 ON CONFLICT 시 amount += delta
    // 동일 배치 2회 → 2배 잔액 문제 발생 가능
    // 이는 알려진 갭: ingester가 cursor 전진 후 동일 배치를 받지 않도록
    // coordinator → fetcher → normalizer 경로에서 방어해야 함
}
```

---

## 6. E2E / 파이프라인 테스트

### 6.1 전체 파이프라인

**파일**: `internal/pipeline/pipeline_e2e_test.go`

```go
//go:build e2e

func TestPipeline_E2E(t *testing.T) {
    // 1. testcontainers PostgreSQL
    db := setupTestDB(t)

    // 2. Seed watched_addresses
    seedWatchedAddress(t, db, "devnet_addr_1")

    // 3. Mock HTTP RPC server
    rpcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Parse JSON-RPC method
        // Return: getSlot → 100
        //         getSignaturesForAddress → [sig1, sig2, sig3, sig4, sig5]
        //         getTransaction → fixture JSON
    }))
    defer rpcServer.Close()

    // 4. Mock gRPC sidecar
    sidecarServer := startMockSidecar(t) // returns addr like "localhost:50052"
    defer sidecarServer.Stop()

    // 5. Create pipeline with mock dependencies
    adapter := solana.NewAdapter(rpcServer.URL, slog.Default())
    cfg := pipeline.Config{
        Chain: model.ChainSolana, Network: model.NetworkDevnet,
        BatchSize: 100, IndexingInterval: 100 * time.Millisecond,
        FetchWorkers: 1, NormalizerWorkers: 1,
        ChannelBufferSize: 5, SidecarAddr: sidecarServer.Addr,
        SidecarTimeout: 10 * time.Second,
    }
    p := pipeline.New(cfg, adapter, db, repos, slog.Default())

    // 6. Run pipeline for limited time
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    go p.Run(ctx)

    // 7. Wait for ingestion (poll DB)
    require.Eventually(t, func() bool {
        var count int
        db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&count)
        return count >= 5
    }, 5*time.Second, 100*time.Millisecond)

    // 8. Assert DB state
    var txCount, transferCount int
    db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&txCount)
    db.QueryRow("SELECT COUNT(*) FROM transfers").Scan(&transferCount)
    assert.Equal(t, 5, txCount)
    assert.GreaterOrEqual(t, transferCount, 1)

    // Assert cursor updated
    var cursorValue string
    db.QueryRow("SELECT cursor_value FROM address_cursors WHERE address='devnet_addr_1'").Scan(&cursorValue)
    assert.NotEmpty(t, cursorValue)

    // Assert watermark
    var ingestedSeq int64
    db.QueryRow("SELECT ingested_sequence FROM pipeline_watermarks WHERE chain='solana'").Scan(&ingestedSeq)
    assert.Greater(t, ingestedSeq, int64(0))
}
```

### 6.2 Shutdown 테스트

```go
func TestPipeline_GracefulShutdown(t *testing.T) {
    // Start pipeline
    ctx, cancel := context.WithCancel(context.Background())
    errCh := make(chan error, 1)
    go func() { errCh <- p.Run(ctx) }()

    // Wait for pipeline to start
    time.Sleep(500 * time.Millisecond)

    // Send cancel signal
    cancel()

    // Assert: pipeline exits without panic
    select {
    case err := <-errCh:
        assert.ErrorIs(t, err, context.Canceled)
    case <-time.After(10 * time.Second):
        t.Fatal("pipeline did not shut down within 10 seconds")
    }
}
```

### 6.3 Backpressure 테스트

```go
func TestPipeline_Backpressure(t *testing.T) {
    // Create pipeline with tiny buffer (1) and slow mock ingester
    cfg := pipeline.Config{
        ChannelBufferSize: 1,
        FetchWorkers: 2,
        NormalizerWorkers: 2,
        // ...
    }

    // Mock ingester: sleep 1 second per batch
    // Assert: fetcher/normalizer eventually block
    // Assert: no goroutine leak (runtime.NumGoroutine stable)
    // Assert: no panic
}
```

---

## 7. 테스트 픽스처

### 7.1 필요한 Solana devnet 트랜잭션 픽스처

| # | 파일명 | 트랜잭션 유형 | 핵심 특성 |
|---|--------|-------------|----------|
| 1 | `sol_system_transfer.json` | SOL system transfer | system program, lamports 이체 |
| 2 | `spl_transfer.json` | SPL Token transfer | TokenkegQ..., ATA 주소 |
| 3 | `spl_transfer_checked.json` | SPL Token transferChecked | mint, decimals 포함 |
| 4 | `failed_tx.json` | 실패 트랜잭션 | meta.err != null |
| 5 | `inner_instructions.json` | inner instruction 포함 | CPI (Cross-Program Invocation) |
| 6 | `create_account.json` | createAccount | system program, lamports 이체 |
| 7 | `unrelated_tx.json` | 무관한 트랜잭션 | watched address 미포함 |

### 7.2 픽스처 형식 및 위치

```
testdata/
├── solana/
│   ├── sol_system_transfer.json           # getTransaction jsonParsed 응답
│   ├── sol_system_transfer.expected.json   # golden: 기대 디코딩 결과
│   ├── spl_transfer.json
│   ├── spl_transfer.expected.json
│   ├── spl_transfer_checked.json
│   ├── spl_transfer_checked.expected.json
│   ├── failed_tx.json
│   ├── failed_tx.expected.json
│   ├── inner_instructions.json
│   ├── inner_instructions.expected.json
│   ├── create_account.json
│   ├── create_account.expected.json
│   └── unrelated_tx.json
└── signatures/
    └── get_signatures_response.json        # getSignaturesForAddress 응답
```

### 7.3 픽스처 예시: SOL System Transfer

**입력** (`testdata/solana/sol_system_transfer.json`):

```json
{
  "slot": 280000000,
  "blockTime": 1700000000,
  "transaction": {
    "message": {
      "accountKeys": [
        {"pubkey": "SenderPubkey11111111111111111111111111111111", "signer": true, "writable": true},
        {"pubkey": "ReceiverPubkey1111111111111111111111111111111", "signer": false, "writable": true},
        {"pubkey": "11111111111111111111111111111111", "signer": false, "writable": false}
      ],
      "instructions": [
        {
          "programId": "11111111111111111111111111111111",
          "parsed": {
            "type": "transfer",
            "info": {
              "source": "SenderPubkey11111111111111111111111111111111",
              "destination": "ReceiverPubkey1111111111111111111111111111111",
              "lamports": 1000000000
            }
          }
        }
      ]
    }
  },
  "meta": {
    "err": null,
    "fee": 5000,
    "preBalances": [2000000000, 0, 1],
    "postBalances": [999995000, 1000000000, 1],
    "innerInstructions": [],
    "logMessages": ["Program 11111111111111111111111111111111 invoke [1]", "Program 11111111111111111111111111111111 success"]
  }
}
```

**기대 출력** (`testdata/solana/sol_system_transfer.expected.json`):

```json
{
  "txHash": "test_signature",
  "blockCursor": 280000000,
  "blockTime": 1700000000,
  "feeAmount": "5000",
  "feePayer": "SenderPubkey11111111111111111111111111111111",
  "status": "SUCCESS",
  "transfers": [
    {
      "instructionIndex": 0,
      "programId": "11111111111111111111111111111111",
      "fromAddress": "SenderPubkey11111111111111111111111111111111",
      "toAddress": "ReceiverPubkey1111111111111111111111111111111",
      "amount": "1000000000",
      "contractAddress": "11111111111111111111111111111111",
      "transferType": "systemTransfer",
      "fromAta": "",
      "toAta": "",
      "tokenSymbol": "SOL",
      "tokenName": "Solana",
      "tokenDecimals": 9,
      "tokenType": "NATIVE"
    }
  ]
}
```

---

## 8. 정합성 검증 시나리오

### 8.1 멱등성

**원칙**: 동일 `NormalizedBatch`를 N회 처리해도 결과가 1회 처리와 동일해야 한다.

**메커니즘:**

| 테이블 | ON CONFLICT | 멱등 보장 |
|--------|-------------|----------|
| transactions | `DO UPDATE SET chain = transactions.chain` (no-op) | 완전 멱등 (ID 반환) |
| transfers | `DO NOTHING` | 완전 멱등 (무시) |
| tokens | `DO UPDATE SET ... RETURNING id` | 완전 멱등 (ID 반환) |
| balances | `DO UPDATE SET amount = amount + $7` | **비멱등** (누적) |
| cursors | `DO UPDATE SET cursor_value = EXCLUDED` | 멱등 (덮어쓰기) |

**중요**: `balances`의 `amount += delta`는 멱등하지 않다.
동일 배치 2회 처리 시 잔액이 2배가 된다.

**방어 메커니즘**: 커서 기반 — Ingester가 커서를 COMMIT 내에서 전진시키므로,
동일 데이터를 다시 fetch하지 않음. 크래시 시에도 커서가 전진하지 않은 상태이므로
재처리 시 동일 데이터를 받지만, `balance += delta`로 중복 반영됨.

**테스트 방법:**

```go
func TestIdempotency_BalanceRisk(t *testing.T) {
    // 1. processBatch(batch) → 성공
    // 2. processBatch(batch) → 성공 (같은 배치)
    // 3. Assert: balance == 2 × expected (현재 동작)
    //    이 테스트는 "현재 동작"을 문서화하는 regression 테스트
    //    향후 balance 멱등성 개선 시 이 테스트를 수정
}
```

### 8.2 크래시 복구

**시나리오**: Ingester `processBatch` 중간에 프로세스 크래시

```
Step 1: BEGIN tx
Step 2: upsert transactions ← 성공
Step 3: upsert transfers    ← 성공
Step 4: adjust balance      ← 크래시 발생!
        → dbTx 자동 rollback (PostgreSQL 연결 끊김)
        → cursor 미전진 (Step 4 이후 커밋 전)
Step 5: 프로세스 재시작
        → Coordinator: cursor = 이전 값 → 같은 데이터 재fetch
        → Ingester: 처음부터 재처리
        → 결과: 정상 처리 (at-least-once)
```

**테스트 방법:**

```go
func TestCrashRecovery(t *testing.T) {
    // 1. Mock balanceRepo.AdjustBalanceTx → error 반환
    // 2. processBatch → error (rollback)
    // 3. Assert: cursor 미전진
    // 4. Mock 정상화
    // 5. processBatch 다시 호출 → 성공
    // 6. Assert: cursor 전진, balance 정확
}
```

### 8.3 순서 보장

**3단계 순서 보장:**

1. **Signature 수집**: Solana RPC newest-first → adapter에서 reverse → oldest-first (`adapter.go:92-105`)
2. **Single-writer 순차 처리**: Ingester 1 goroutine, 채널 FIFO (`ingester.go:52-72`)
3. **GREATEST() watermark**: 재처리 시에도 워터마크 비퇴행 (`indexer_config_repo.go:61`)

**테스트 방법:**

```go
func TestOrderGuarantee(t *testing.T) {
    // 1. 3개 batch 순서대로 전송: cursor_seq 100, 200, 300
    // 2. Assert: cursor_sequence == 300 (마지막)
    // 3. Assert: watermark ingested_sequence == 300
    // 4. 역순 batch: cursor_seq 250
    // 5. Assert: watermark == 300 (GREATEST, 비퇴행)
}
```

### 8.4 잔액 정확성

**시나리오**: 입금 100 SOL, 출금 30 SOL, 수수료 0.000005 SOL

```
초기 잔액: 0

Batch 1 (입금):
  transfer: to_address == watched, amount = 100_000_000_000 (100 SOL)
  direction: DEPOSIT
  balance += 100_000_000_000
  fee: fee_payer == watched, fee = 5000
  balance -= 5000

  → 잔액: 99_999_995_000

Batch 2 (출금):
  transfer: from_address == watched, amount = 30_000_000_000 (30 SOL)
  direction: WITHDRAWAL
  balance -= 30_000_000_000
  fee: fee_payer == watched, fee = 5000
  balance -= 5000

  → 잔액: 69_999_990_000 (≈ 69.99999 SOL)
```

### 8.5 Watched Address 필터링

**시나리오**: 1 트랜잭션에 5개 transfer, watched address 관련 2개만

```
Transaction:
  transfer[0]: A → B (무관)
  transfer[1]: A → watched_addr (입금) ← 저장
  transfer[2]: C → D (무관)
  transfer[3]: watched_addr → E (출금) ← 저장
  transfer[4]: C → F (무관)

→ transfers 테이블에 2건만 저장
→ balance: +transfer[1].amount - transfer[3].amount - fee
```

**테스트 방법:**

```go
func TestWatchedAddressFiltering(t *testing.T) {
    batch := event.NormalizedBatch{
        Address: "watched_addr",
        Transactions: []event.NormalizedTransaction{{
            Transfers: []event.NormalizedTransfer{
                {FromAddress: "A", ToAddress: "B", Amount: "100"},       // 무관
                {FromAddress: "A", ToAddress: "watched_addr", Amount: "200"}, // 입금
                {FromAddress: "watched_addr", ToAddress: "C", Amount: "50"}, // 출금
            },
        }},
    }
    // 주의: 현재 Sidecar에서 필터링하므로, Ingester에는 이미 필터링된 데이터가 옴
    // 이 테스트는 Sidecar 테스트에서 수행
}
```

---

## 9. 성능 / 부하 테스트

### 9.1 Go Benchmark

```go
// internal/pipeline/ingester/ingester_bench_test.go
func BenchmarkIngester_ProcessBatch(b *testing.B) {
    db := setupBenchDB(b)
    ing := setupIngester(db)
    batch := createLargeBatch(100) // 100 transactions

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        ing.ProcessBatch(context.Background(), batch)
    }
}

// internal/chain/solana/adapter_bench_test.go
func BenchmarkAdapter_FetchNewSignatures(b *testing.B) {
    // Mock HTTP server with large response
    // Benchmark pagination + reverse logic
}
```

### 9.2 부하 시나리오

| 시나리오 | 설정 | 측정 항목 |
|---------|------|----------|
| 100 주소 × 1000 pending tx | BATCH_SIZE=100, workers=4 | 전체 처리 시간, 메모리 |
| 대량 배치 | 1 주소 × 10000 signatures | 페이지네이션 시간 |
| 동시 접근 | 100 watched addresses, interval=1s | DB 커넥션 풀 사용률 |

### 9.3 동시성 스트레스

```bash
# Race condition 탐지
go test ./... -race -count=5

# Worker 확대
FETCH_WORKERS=10 NORMALIZER_WORKERS=10 go test ./... -race
```

### 9.4 지속 실행

```bash
# Devnet 1시간 실행, 메모리/고루틴 모니터링
WATCHED_ADDRESSES=실제주소1,실제주소2 \
LOG_LEVEL=debug \
go run ./cmd/indexer &

# 모니터링 (pprof)
go tool pprof http://localhost:8080/debug/pprof/heap
go tool pprof http://localhost:8080/debug/pprof/goroutine
```

---

## 10. CI/CD 통합

### 10.1 테스트 카테고리 및 Build Tag

| 카테고리 | Build Tag | 실행 시간 | 의존성 |
|---------|-----------|----------|--------|
| Unit | (없음) | < 30초 | 없음 |
| Integration | `integration` | < 2분 | Docker (PostgreSQL) |
| E2E | `e2e` | < 5분 | Docker (PostgreSQL + Sidecar) |
| Race | `-race` | × 2-10배 | 없음 |
| Benchmark | `-bench` | 가변 | Docker (PostgreSQL) |

### 10.2 CI 파이프라인

```yaml
# .github/workflows/test.yaml
stages:
  1. lint:        golangci-lint run ./...
  2. unit:        go test ./... -short -count=1
  3. integration: go test ./... -tags integration -count=1
  4. e2e:         go test ./... -tags e2e -count=1 -timeout 10m
  5. race:        go test ./... -race -short
  6. sidecar:     cd sidecar && npm test
  7. coverage:    go test ./... -coverprofile=coverage.out
                  go tool cover -func=coverage.out
```

### 10.3 인프라 요구사항

| 요구사항 | 버전 | 용도 |
|---------|------|------|
| Go | 1.24+ | 빌드 및 테스트 |
| Node.js | 22 LTS | Sidecar 빌드 및 테스트 |
| Docker | 24+ | testcontainers (PostgreSQL 16) |
| Docker-in-Docker | — | CI 환경에서 testcontainers 실행 |

### 10.4 커버리지 게이트

| 대상 | 최소 커버리지 | 제외 |
|------|-------------|------|
| Go 전체 | 70% | `pkg/generated/`, `cmd/` |
| `internal/pipeline/ingester/` | 85% | — |
| `internal/store/postgres/` | 60% | — |
| Sidecar | 80% | `dist/`, `node_modules/` |

---

## 11. 테스트 파일 구조 (현재)

### 11.1 Go (63개 테스트 파일)

```
cmd/indexer/
├── db_pool_metrics_test.go
└── main_test.go
internal/
├── addressindex/
│   ├── bloom_test.go
│   └── tiered_test.go
├── admin/
│   ├── audit_test.go
│   ├── ratelimit_test.go
│   └── server_test.go
├── alert/
│   └── alerter_test.go
├── cache/
│   ├── lru_bench_test.go
│   └── lru_test.go
├── chain/
│   ├── arbitrum/adapter_test.go
│   ├── base/
│   │   ├── adapter_test.go
│   │   └── rpc/{client_test.go, methods_test.go}
│   ├── bsc/adapter_test.go
│   ├── btc/
│   │   ├── adapter_test.go
│   │   └── rpc/{client_test.go, methods_test.go}
│   ├── ethereum/adapter_test.go
│   ├── polygon/adapter_test.go
│   ├── ratelimit/limiter_test.go
│   └── solana/
│       ├── adapter_test.go
│       └── rpc/{client_test.go, methods_test.go}
├── config/
│   └── config_test.go
├── domain/
│   ├── event/normalized_batch_test.go
│   └── model/{activity_classifier_test.go, balance_event_test.go, chain_test.go}
├── metrics/
│   └── metrics_test.go
├── pipeline/
│   ├── config_watcher_test.go
│   ├── health_test.go
│   ├── pipeline_test.go
│   ├── registry_test.go
│   ├── coordinator/
│   │   ├── coordinator_test.go
│   │   └── autotune/{autotune_test.go, autotune_scenario_test.go}
│   ├── fetcher/{fetcher_test.go, fetcher_bench_test.go}
│   ├── finalizer/finalizer_test.go
│   ├── ingester/
│   │   ├── ingester_test.go
│   │   ├── ingester_bench_test.go
│   │   ├── helpers_test.go
│   │   ├── interleaver_test.go
│   │   ├── reorg_test.go
│   │   ├── reorg_interleave_test.go
│   │   └── test_fixtures_test.go
│   ├── normalizer/
│   │   ├── normalizer_test.go
│   │   ├── normalizer_bench_test.go
│   │   ├── normalizer_error_test.go
│   │   ├── normalizer_balance_evm_chains_test.go
│   │   ├── base_runtime_e2e_test.go
│   │   ├── btc_runtime_e2e_test.go
│   │   └── solana_runtime_e2e_test.go
│   ├── reorgdetector/detector_test.go
│   └── replay/service_test.go
├── reconciliation/
│   └── service_test.go
├── store/
│   └── postgres/{db_test.go, integration_test.go, testutil_test.go}
├── tracing/
│   └── tracing_test.go
└── pipeline/retry/
    └── retry_test.go
```

### 11.2 Sidecar (Jest)

```
sidecar/
├── src/decoder/{solana,base,btc}/__tests__/
├── testdata/
├── jest.config.ts
└── package.json
```

### 11.3 테스트 실행

```bash
# 전체 테스트 (race detector 포함)
go test ./... -count=1 -race

# 특정 패키지
go test ./internal/pipeline/ingester/... -count=1 -race

# 벤치마크
go test ./internal/pipeline/ingester/... -bench=. -benchmem
```
