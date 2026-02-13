# DB 모델 변경 비교: AS-IS → TO-BE

> koda-custody-server (Kotlin/JPA) → koda-custody-indexer (Go/SQL) 데이터 모델 전환 분석

---

## 1. 개요

koda-custody-indexer는 기존 koda-custody-server의 JPA JOINED Inheritance 기반 데이터 모델을
**통합 테이블 + JSONB** 모델로 재설계했다.
이 문서는 AS-IS 모델의 비효율을 분석하고, TO-BE 모델의 설계 근거와 기대 효과를 정량적으로 비교한다.

---

## 2. AS-IS: Kotlin/JPA JOINED Inheritance 모델

### 2.1 엔티티 계층 구조

기존 시스템은 JPA의 `@Inheritance(strategy = InheritanceType.JOINED)` 전략을 사용하여
부모-자식 테이블 관계를 구현한다.

**Transaction 계층:**

```
Transaction (abstract, @Inheritance(JOINED))           ← transactions 테이블
  ├── SolTransaction (@DiscriminatorValue("SolTransaction"))  ← sol_transactions
  ├── EvmTransaction (@DiscriminatorValue("EvmTransaction"))  ← evm_transactions
  ├── BtcTransaction (@DiscriminatorValue("BtcTransaction"))  ← btc_transactions
  ├── EosioTransaction                                         ← eosio_transactions
  ├── AptosTransaction                                         ← aptos_transactions
  └── SuiTransaction                                           ← sui_transactions
```

**TransactionActivity 계층:**

```
TransactionActivity (abstract, @Inheritance(JOINED))   ← transaction_activities
  └── TransferActivity (abstract, @DiscriminatorValue("TRANSFER"))  ← transfer_activities
      ├── SolTransferActivity                           ← sol_transfer_activities
      ├── EvmTransferActivity                           ← evm_transfer_activities
      ├── BtcTransferActivity                           ← btc_transfer_activities
      ├── EosioTransferActivity                         ← eosio_transfer_activities
      ├── AptosTransferActivity                         ← aptos_transfer_activities
      └── SuiTransferActivity                           ← sui_transfer_activities
```

**보조 엔티티:**

```
TransferActivityCounterparty                            ← transfer_activity_counterparty
  └── @ElementCollection TransferActivityCounterpartyAddress  ← asset_activity_counterparty_addresses
```

### 2.2 AS-IS 핵심 코드 인용

**Transaction 부모 클래스** — `koda-custody-server/.../entities/Transaction.kt`:

```kotlin
@Entity
@Table(name = "transactions")
@Inheritance(strategy = InheritanceType.JOINED)
@DiscriminatorColumn(name = "discriminator", discriminatorType = DiscriminatorType.STRING)
abstract class Transaction protected constructor(
    @Column(name = "hash", length = 128, nullable = false) val hash: String,
    @Column(name = "blockchain", nullable = false) val blockchain: Blockchain,
    @Column(name = "block_number", nullable = true) val blockNumber: BigInteger? = null,
    @Enumerated(EnumType.STRING) @Column(name = "status", nullable = false) var status: TransactionStatus,
    @Column(name = "fee_payer") val feePayer: AddressVO? = null,
    @Embedded @AttributeOverride(name = "value",
        column = Column(name = "fee_amount", nullable = false, precision = 78))
    var feeAmount: Amount,
    @Column(name = "block_timestamp", nullable = true) val blockTimestamp: LocalDateTime? = null,
    @Column(name = "transaction_index", nullable = false) var transactionIndex: Int = 0,
    // ...
) : DomainEntity()
```

**TransactionActivity 부모 클래스** — `koda-custody-server/.../entities/TransactionActivity.kt`:

```kotlin
@Entity
@Table(name = "transaction_activities")
@Inheritance(strategy = InheritanceType.JOINED)
@DiscriminatorColumn(name = "discriminator", discriminatorType = DiscriminatorType.STRING)
abstract class TransactionActivity protected constructor(
    @Column(name = "organization_id", nullable = false) val organizationId: String,
    @Column(name = "wallet_id", nullable = false) val walletId: String,
    @Column(name = "blockchain", nullable = false) val blockchain: Blockchain,
    @Enumerated(EnumType.STRING) @Column(name = "status", nullable = false) var status: TransactionActivityStatus,
    @Embedded @AttributeOverride(name = "value",
        column = Column(name = "origin_balance", nullable = false, precision = 78))
    var originBalance: Amount,                                    // ← 서빙 데이터가 파이프라인 테이블에 혼재
    @ManyToOne(fetch = FetchType.EAGER, cascade = [CascadeType.PERSIST, CascadeType.MERGE])
    @JoinColumn(name = "transaction_id", nullable = false)
    val transaction: Transaction,
) : DomainEntity()
```

**SolTransferActivity** — `koda-custody-server/.../sol/entities/SolTransferActivity.kt`:

```kotlin
@Entity
@Table(name = "sol_transfer_activities")
@DiscriminatorValue("SolTransferActivity")
class SolTransferActivity(
    // ... 부모 필드 생략
    @Column(name = "from_address", nullable = false) val from: AddressVO,
    @Column(name = "to_address", nullable = false) val to: AddressVO,
    @Column(name = "instruction_index", nullable = false) val instructionIndex: Long,
) : TransferActivity(...)
```

**EvmTransferActivity** — `koda-custody-server/.../evm/entities/EvmTransferActivity.kt`:

```kotlin
@Entity
@Table(name = "evm_transfer_activities")
@DiscriminatorValue("EvmTransferActivity")
class EvmTransferActivity(
    // ... 부모 필드 생략
    @Column(name = "from_address", length = 42, nullable = false) val from: AddressVO,
    @Column(name = "to_address", length = 42, nullable = false) val to: AddressVO,
    @Column(name = "log_index") val logIndex: Int?,
    @Column(name = "transfer_standard", nullable = false) val transferStandard: EvmTransferStandard,
) : TransferActivity(...)
```

### 2.3 AS-IS 테이블 구조 (Flyway 마이그레이션 기반)

AS-IS 시스템의 실제 DDL (`V1_0__init.kt` Flyway 마이그레이션):

```sql
-- 부모 테이블
CREATE TABLE "transactions" (
    "discriminator" varchar(31) NOT NULL,     -- JPA 식별자
    "id"            varchar(255) NOT NULL,
    "hash"          varchar(128) NOT NULL,
    "blockchain"    varchar(255) NOT NULL,
    "block_number"  numeric(38,0),
    "block_timestamp" timestamp,
    "fee_amount"    numeric(78,0) NOT NULL,
    "fee_payer"     varchar(255),
    "status"        varchar(255) NOT NULL,
    "indexed_at"    timestamp NOT NULL,
    PRIMARY KEY ("id")
);

CREATE TABLE "transaction_activities" (
    "discriminator"   varchar(31) NOT NULL,   -- JPA 식별자
    "id"              varchar(255) NOT NULL,
    "activity_type"   varchar(255) NOT NULL,
    "blockchain"      varchar(255) NOT NULL,
    "organization_id" varchar(255) NOT NULL,
    "origin_balance"  numeric(78,0) NOT NULL, -- 파이프라인 상태가 서빙 테이블에
    "status"          varchar(255) NOT NULL,  -- 인덱싱 상태
    "wallet_id"       varchar(255) NOT NULL,
    "transaction_id"  varchar(255) NOT NULL,
    PRIMARY KEY ("id")
);

CREATE TABLE "transfer_activities" (
    "amount"       numeric(78,0) NOT NULL,
    "is_withdrawal" bool NOT NULL,
    "id"           varchar(255) NOT NULL,
    "token_id"     varchar(255) NOT NULL,
    "transfer_activity_counterparty_id" varchar(255),
    PRIMARY KEY ("id")
);

-- 체인별 자식 테이블
CREATE TABLE "sol_transactions" (
    "err" text, "id" varchar(255) NOT NULL, PRIMARY KEY ("id")
);
CREATE TABLE "sol_transfer_activities" (
    "from_address" varchar(255) NOT NULL,
    "instruction_index" int8 NOT NULL,
    "to_address" varchar(255) NOT NULL,
    "id" varchar(255) NOT NULL, PRIMARY KEY ("id")
);

CREATE TABLE "evm_transactions" (
    "block_hash" varchar(66) NOT NULL, "contract_address" varchar(42),
    "error" text, "from_address" varchar(42) NOT NULL,
    "gas_limit" numeric(38,0) NOT NULL, "gas_price" numeric(38,0) NOT NULL,
    "gas_used" numeric(38,0), "input" text NOT NULL,
    "is_contract_creation" bool NOT NULL, "nonce" numeric(38,0) NOT NULL,
    "to_address" varchar(42), "value" numeric(38,0) NOT NULL,
    "id" varchar(255) NOT NULL, PRIMARY KEY ("id")
);
CREATE TABLE "evm_transfer_activities" (
    "from_address" varchar(42) NOT NULL, "log_index" int4,
    "to_address" varchar(42) NOT NULL, "transfer_standard" int2 NOT NULL,
    "id" varchar(255) NOT NULL, PRIMARY KEY ("id")
);

CREATE TABLE "btc_transactions" ("id" varchar(255) NOT NULL, PRIMARY KEY ("id"));
CREATE TABLE "btc_transfer_activities" (
    "from_address" varchar(255), "hex" text NOT NULL,
    "output_index" int8 NOT NULL, "to_address" varchar(255) NOT NULL,
    "tx_id" varchar(255) NOT NULL,
    "id" varchar(255) NOT NULL, PRIMARY KEY ("id")
);

-- 보조 테이블
CREATE TABLE "transfer_activity_counterparty" (
    "id" varchar(255) NOT NULL, PRIMARY KEY ("id")
);
CREATE TABLE "asset_activity_counterparty_addresses" (
    "id" varchar(255) NOT NULL,
    "address" varchar(255) NOT NULL,
    "address_book_id" varchar(255)
);
```

**전체 테이블 수 (6개 체인 기준): 15+ 테이블**

| 카테고리 | 테이블 수 | 테이블명 |
|---------|----------|---------|
| 부모 | 3 | transactions, transaction_activities, transfer_activities |
| Solana | 2 | sol_transactions, sol_transfer_activities |
| EVM | 2 | evm_transactions, evm_transfer_activities |
| Bitcoin | 2 | btc_transactions, btc_transfer_activities |
| EOSIO | 2 | eosio_transactions, eosio_transfer_activities |
| Aptos | 2 | aptos_transactions, aptos_transfer_activities |
| Sui | 2 | sui_transactions, sui_transfer_activities |
| 보조 | 2 | transfer_activity_counterparty, asset_activity_counterparty_addresses |
| **합계** | **17** | |

---

## 3. AS-IS 비효율 분석

### 3.1 JOINED Inheritance의 쿼리 비용

**단일 체인 Transfer 조회 시 (예: Solana):**

```sql
-- JPA가 생성하는 실제 쿼리 (3-way JOIN)
SELECT ta.*, tr.*, sol.*
FROM transaction_activities ta
JOIN transfer_activities tr ON ta.id = tr.id
JOIN sol_transfer_activities sol ON tr.id = sol.id
WHERE ta.blockchain = 'SOLANA'
  AND ta.organization_id = ?;
```

- **3-way JOIN**: `transaction_activities` → `transfer_activities` → `sol_transfer_activities`
- EAGER fetch로 `transaction` 관계 추가 시: `JOIN transactions t ON ta.transaction_id = t.id` → **4-way JOIN**

**크로스체인 Transfer 조회 시:**

```sql
-- 모든 체인의 transfer를 조회하려면 LEFT JOIN으로 모든 자식 테이블
SELECT ta.*, tr.*, sol.*, evm.*, btc.*, eosio.*, aptos.*, sui.*
FROM transaction_activities ta
JOIN transfer_activities tr ON ta.id = tr.id
LEFT JOIN sol_transfer_activities sol ON tr.id = sol.id
LEFT JOIN evm_transfer_activities evm ON tr.id = evm.id
LEFT JOIN btc_transfer_activities btc ON tr.id = btc.id
LEFT JOIN eosio_transfer_activities eosio ON tr.id = eosio.id
LEFT JOIN aptos_transfer_activities aptos ON tr.id = aptos.id
LEFT JOIN sui_transfer_activities sui ON tr.id = sui.id
WHERE ta.organization_id = ?;
```

- **최대 7-way LEFT JOIN**: 체인 수에 비례하여 JOIN 증가
- 대부분의 LEFT JOIN 결과는 NULL (해당 체인이 아닌 행)

### 3.2 스키마 관리 비용

체인 추가 시 필요한 작업:

1. `XxxTransaction` 엔티티 클래스 생성
2. `XxxTransferActivity` 엔티티 클래스 생성
3. Flyway 마이그레이션으로 2개 자식 테이블 생성
4. `@DiscriminatorValue` 등록
5. 기존 쿼리에 LEFT JOIN 추가
6. JPA 매핑 설정

**결과**: 6개 체인 × 2 타입(Transaction + TransferActivity) = **12개 자식 테이블** + 3개 부모 + 2개 보조

### 3.3 파이프라인/서빙 데이터 혼재

```
transaction_activities 테이블:
  ├── origin_balance  ← 파이프라인이 계산한 잔액 (파이프라인 상태)
  ├── status          ← 인덱싱 상태 (PENDING/CONFIRMED)
  ├── organization_id ← 서빙 데이터
  └── wallet_id       ← 서빙 데이터
```

**문제점:**
- `origin_balance`는 인덱싱 프로세스가 계산하는 값이지만, 서빙 테이블에 저장됨
- 인덱서 재처리 시 `origin_balance` 값이 오염될 수 있음
- 커서/워터마크 개념이 없어 부분 재처리 불가능
- JPA `merge()` 의존으로 멱등성 보장이 ORM 레벨에 종속

### 3.4 주소 길이 비일관성

| 체인 | 주소 길이 | AS-IS VARCHAR |
|------|----------|--------------|
| Solana | 44 chars (base58) | `varchar(255)` |
| EVM | 42 chars (0x prefix) | `varchar(42)` |
| Aptos | 66 chars (0x prefix) | `varchar(66)` |
| Sui | 66 chars (0x prefix) | `varchar(66)` |
| Bitcoin | 25-62 chars | `varchar(255)` |
| EOSIO | 1-12 chars | `varchar(14)` |

각 자식 테이블마다 다른 `VARCHAR` 길이를 사용하여 일관성이 없음.

### 3.5 Counterparty 모델 복잡성

```
TransferActivity
  └── @OneToOne counterparty → TransferActivityCounterparty (별도 테이블)
       └── @ElementCollection addresses → asset_activity_counterparty_addresses (별도 테이블)
```

단일 Transfer를 조회하기 위해 **추가 2개 테이블 JOIN** 필요:

```sql
-- 단일 SOL Transfer 전체 조회 시 실제 JOIN 수
transaction_activities          -- (1) 부모
JOIN transfer_activities        -- (2) 중간 부모
JOIN sol_transfer_activities    -- (3) 체인별 자식
JOIN transactions               -- (4) FK: 트랜잭션
JOIN sol_transactions            -- (5) FK: 체인별 트랜잭션
JOIN transfer_activity_counterparty    -- (6) 상대방
JOIN asset_activity_counterparty_addresses  -- (7) 상대방 주소
-- 총 7-way JOIN
```

---

## 4. TO-BE: 통합 테이블 + JSONB 모델

### 4.1 설계 원칙

1. **모든 체인 공통 필드 → 정규 컬럼**: `chain`, `network`, `tx_hash`, `amount`, `from_address`, `to_address`
2. **체인 고유 필드 → `chain_data JSONB`**: Solana ATA, EVM gas_price, BTC output_index 등
3. **파이프라인 상태 / 서빙 데이터 완전 분리**: 4+4 테이블 구조
4. **통합 주소 길이**: 모든 체인 `VARCHAR(128)`

### 4.2 테이블 구조 (8개 테이블)

**파이프라인 상태 테이블 (4개):**

| 테이블 | 목적 | 유니크 제약 |
|--------|------|-----------|
| `indexer_configs` | 체인/네트워크별 인덱서 설정 | `(chain, network)` |
| `watched_addresses` | 모니터링 대상 주소 | `(chain, network, address)` |
| `address_cursors` | 주소별 페이지네이션 상태 | `(chain, network, address)` |
| `pipeline_watermarks` | 글로벌 인덱싱 진행 상태 | `(chain, network)` |

**서빙 데이터 테이블 (4개):**

| 테이블 | 목적 | 유니크 제약 |
|--------|------|-----------|
| `tokens` | 토큰 메타데이터 | `(chain, network, contract_address)` |
| `transactions` | 모든 체인 트랜잭션 | `(chain, network, tx_hash)` |
| `transfers` | 개별 이체 내역 | `(chain, network, tx_hash, instruction_index, watched_address)` |
| `balances` | 주소/토큰별 현재 잔액 | `(chain, network, address, token_id)` |

### 4.3 TO-BE 실제 SQL 스키마 (마이그레이션 001 + 002)

**파이프라인 테이블** — `001_create_pipeline_tables.up.sql`:

```sql
CREATE TABLE watched_addresses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           VARCHAR(20) NOT NULL,
    network         VARCHAR(20) NOT NULL,
    address         VARCHAR(128) NOT NULL,        -- 모든 체인 통합 길이
    wallet_id       VARCHAR(100),
    organization_id VARCHAR(100),
    label           VARCHAR(255),
    is_active       BOOLEAN NOT NULL DEFAULT true,
    source          VARCHAR(20) NOT NULL DEFAULT 'db',
    CONSTRAINT uq_watched_address UNIQUE (chain, network, address)
);

CREATE TABLE address_cursors (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain           VARCHAR(20) NOT NULL,
    network         VARCHAR(20) NOT NULL,
    address         VARCHAR(128) NOT NULL,
    cursor_value    VARCHAR(128),                  -- last processed signature
    cursor_sequence BIGINT NOT NULL DEFAULT 0,     -- last processed slot/block
    items_processed BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT uq_address_cursor UNIQUE (chain, network, address),
    CONSTRAINT fk_cursor_watched_addr
        FOREIGN KEY (chain, network, address)
        REFERENCES watched_addresses(chain, network, address)
);

CREATE TABLE pipeline_watermarks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain             VARCHAR(20) NOT NULL,
    network           VARCHAR(20) NOT NULL,
    head_sequence     BIGINT NOT NULL DEFAULT 0,   -- 온체인 최신 슬롯
    ingested_sequence BIGINT NOT NULL DEFAULT 0,   -- DB에 기록된 최신 슬롯
    CONSTRAINT uq_watermark UNIQUE (chain, network)
);
```

**서빙 테이블** — `002_create_serving_tables.up.sql`:

```sql
CREATE TABLE transactions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain        VARCHAR(20) NOT NULL,
    network      VARCHAR(20) NOT NULL,
    tx_hash      VARCHAR(128) NOT NULL,
    block_cursor BIGINT NOT NULL,
    block_time   TIMESTAMPTZ,
    fee_amount   NUMERIC(78,0) NOT NULL DEFAULT 0,
    fee_payer    VARCHAR(128) NOT NULL,
    status       VARCHAR(20) NOT NULL,
    err          TEXT,
    chain_data   JSONB NOT NULL DEFAULT '{}',      -- EVM: gas_price, SOL: err 등
    CONSTRAINT uq_transaction UNIQUE (chain, network, tx_hash)
);

CREATE TABLE transfers (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain             VARCHAR(20) NOT NULL,
    network           VARCHAR(20) NOT NULL,
    transaction_id    UUID NOT NULL REFERENCES transactions(id),
    tx_hash           VARCHAR(128) NOT NULL,
    instruction_index INT NOT NULL,
    token_id          UUID NOT NULL REFERENCES tokens(id),
    from_address      VARCHAR(128) NOT NULL,
    to_address        VARCHAR(128) NOT NULL,
    amount            NUMERIC(78,0) NOT NULL,
    direction         VARCHAR(10),                 -- DEPOSIT / WITHDRAWAL / NULL
    watched_address   VARCHAR(128),
    wallet_id         VARCHAR(100),
    organization_id   VARCHAR(100),
    block_cursor      BIGINT NOT NULL,
    block_time        TIMESTAMPTZ,
    chain_data        JSONB NOT NULL DEFAULT '{}', -- SOL: from_ata, to_ata, transfer_type
    CONSTRAINT uq_transfer UNIQUE (chain, network, tx_hash, instruction_index, watched_address)
);

CREATE TABLE balances (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain                       VARCHAR(20) NOT NULL,
    network                     VARCHAR(20) NOT NULL,
    address                     VARCHAR(128) NOT NULL,
    token_id                    UUID NOT NULL REFERENCES tokens(id),
    amount                      NUMERIC(78,0) NOT NULL DEFAULT 0,
    pending_withdrawal_amount   NUMERIC(78,0) NOT NULL DEFAULT 0,
    last_updated_cursor         BIGINT NOT NULL DEFAULT 0,
    last_updated_tx_hash        VARCHAR(128),
    CONSTRAINT uq_balance UNIQUE (chain, network, address, token_id)
);
```

### 4.4 핵심 변경점

| AS-IS | TO-BE | 변경 이유 |
|-------|-------|----------|
| `@Inheritance(JOINED)` 3단계 상속 | 단일 `transfers` 테이블 | JOIN 제거, 쿼리 단순화 |
| 체인별 자식 테이블 (sol_transfer_activities 등) | `chain_data JSONB` | DDL 변경 없이 체인 확장 |
| `discriminator` 컬럼 | `chain` + `network` 컬럼 | 명시적 분류, JSONB 쿼리 가능 |
| `origin_balance` in transaction_activities | 별도 `balances` 테이블 | 파이프라인/서빙 분리 |
| JPA `merge()` 기반 갱신 | `ON CONFLICT` upsert | DB 레벨 멱등성 보장 |
| TransferActivityCounterparty (3 테이블) | `from_address` + `to_address` (인라인) | 불필요한 관계 제거 |
| 커서 없음 | `address_cursors` 전용 테이블 | 투명한 재처리 |
| 워터마크 없음 | `pipeline_watermarks` 전용 테이블 | 인덱싱 진행 모니터링 |

---

## 5. 효율 비교 및 기대 효과

### 5.1 쿼리 성능

| 시나리오 | AS-IS | TO-BE | 개선 |
|---------|-------|-------|------|
| 단일 체인 transfer 조회 | 3-way JOIN (transaction_activities → transfer_activities → sol_transfer_activities) | 단일 테이블 `SELECT * FROM transfers WHERE chain='solana'` | JOIN 제거, ~2-3x 빠름 |
| 크로스체인 transfer 조회 | 7-way LEFT JOIN (모든 자식 테이블) | `SELECT * FROM transfers WHERE chain IN ('solana','ethereum')` | JOIN 제거, ~5-10x 빠름 |
| 주소별 잔액 조회 | origin_balance 계산 필요 (aggregate) | `SELECT * FROM balances WHERE address=?` | 실시간 O(1) 조회 |
| Transfer + 트랜잭션 JOIN | 5-7 way JOIN | 2 테이블 JOIN (transfers + transactions) | JOIN 수 60-70% 감소 |
| 커서/워터마크 조회 | 혼재 (별도 메커니즘 없음) | 전용 테이블 직접 조회 | 명확한 분리 |

### 5.2 스키마 관리

| 항목 | AS-IS | TO-BE | 개선 |
|------|-------|-------|------|
| 체인 추가 시 새 테이블 수 | +2개 자식 테이블 (xxx_transactions + xxx_transfer_activities) | 0개 (chain_data JSONB 확장) | DDL 변경 없음 |
| 체인 추가 시 마이그레이션 | Flyway 마이그레이션 + Entity 클래스 2개 + @DiscriminatorValue 등록 | chain_data 필드 정의만 (애플리케이션 레벨) | 무중단 배포 |
| 총 테이블 수 (6체인 기준) | 17개 테이블 | 8개 테이블 (고정) | ~53% 감소 |
| 체인 추가 후 총 테이블 | 19개 (+2) | 8개 (변동 없음) | 선형 증가 → 상수 |

### 5.3 데이터 정합성

| 항목 | AS-IS | TO-BE | 개선 |
|------|-------|-------|------|
| 멱등성 보장 | JPA `merge()` 의존 (ORM 레벨) | `ON CONFLICT` upsert (DB 레벨) | DB 레벨 원자적 보장 |
| 트랜잭션 원자성 | JPA `flush()` + `@Transactional` | `sql.Tx` + `defer Rollback` | 명시적 트랜잭션 경계 |
| 파이프라인/서빙 분리 | 혼재 (origin_balance, status) | 완전 분리 (4 + 4 테이블) | 독립 재처리 가능 |
| 커서 관리 | 암묵적 (없음) | `address_cursors` 전용 테이블 | 투명한 진행 상태 |
| 워터마크 비퇴행 | 보장 없음 | `GREATEST()` 함수 | SQL 레벨 보장 |
| 잔액 정확성 | origin_balance 일괄 계산 | `amount + delta` 누적 연산 | 실시간 정합 |

**워터마크 비퇴행 예시** — `indexer_config_repo.go:57-64`:

```sql
ON CONFLICT (chain, network) DO UPDATE SET
    ingested_sequence = GREATEST(pipeline_watermarks.ingested_sequence, $3),
    -- GREATEST() 보장: 새 값이 기존보다 작으면 무시
```

**커서 누적 카운터** — `cursor_repo.go:40-49`:

```sql
ON CONFLICT (chain, network, address) DO UPDATE SET
    cursor_value = EXCLUDED.cursor_value,
    items_processed = address_cursors.items_processed + EXCLUDED.items_processed,
    -- items_processed 누적으로 총 처리 건수 추적
```

### 5.4 저장 효율

| 항목 | AS-IS | TO-BE | 개선 |
|------|-------|-------|------|
| NULL 컬럼 | 자식별 대량 (7-way LEFT JOIN 시 6/7이 NULL) | 0개 (JSONB가 흡수) | 스토리지 낭비 제거 |
| 주소 컬럼 길이 | 테이블별 상이 (42/66/255) | `VARCHAR(128)` 통합 | 일관성 |
| discriminator 컬럼 | 각 부모 테이블에 존재 | 불필요 (chain 컬럼이 대체) | 컬럼 제거 |
| origin_balance 중복 | 모든 transfer_activity 행에 저장 | balances 테이블 분리 (주소×토큰 단위) | 중복 제거 |
| ID 타입 | `varchar(255)` (JPA 기본) | `UUID` | 16바이트 vs 36바이트 문자열 |
| Counterparty 관계 | 3 테이블 (1:1:N) | `from_address` + `to_address` 인라인 | 테이블 2개 제거 |

### 5.5 개발 생산성

**새 체인 추가 비교:**

AS-IS (6단계):
1. `XxxTransaction` 엔티티 클래스 생성
2. `XxxTransferActivity` 엔티티 클래스 생성
3. Flyway 마이그레이션 작성 (2개 테이블 DDL)
4. JPA Repository 인터페이스 작성
5. `@DiscriminatorValue` 등록
6. 기존 크로스체인 쿼리에 LEFT JOIN 추가

TO-BE (2단계):
1. `ChainAdapter` 인터페이스 구현
2. `chain_data` JSONB 구조 정의 (애플리케이션 코드에서)

**크로스체인 대시보드 쿼리:**

```sql
-- AS-IS: 7-way LEFT JOIN
SELECT ta.*, tr.*, sol.*, evm.*, btc.*, eosio.*, aptos.*, sui.*
FROM transaction_activities ta
JOIN transfer_activities tr ON ta.id = tr.id
LEFT JOIN sol_transfer_activities sol ON tr.id = sol.id
LEFT JOIN evm_transfer_activities evm ON tr.id = evm.id
LEFT JOIN btc_transfer_activities btc ON tr.id = btc.id
LEFT JOIN eosio_transfer_activities eosio ON tr.id = eosio.id
LEFT JOIN aptos_transfer_activities aptos ON tr.id = aptos.id
LEFT JOIN sui_transfer_activities sui ON tr.id = sui.id
WHERE ta.organization_id = ?
ORDER BY ta.created_at DESC;

-- TO-BE: 단순 WHERE
SELECT * FROM transfers
WHERE organization_id = ?
ORDER BY block_time DESC;
```

---

## 6. 트레이드오프

### 6.1 JSONB 쿼리 성능

`chain_data` JSONB 필드 내부 값으로의 쿼리는 정규 컬럼보다 느리다.

```sql
-- JSONB 내부 쿼리 예시
SELECT * FROM transfers
WHERE chain_data->>'transfer_type' = 'transferChecked';
```

**완화 방법:**
- GIN 인덱스: `CREATE INDEX idx_transfers_chain_data ON transfers USING GIN (chain_data);`
- 자주 쿼리하는 필드는 정규 컬럼으로 승격 (마이그레이션으로)
- 현재 설계에서 `chain_data`는 주로 보조 정보 (from_ata, to_ata, transfer_type)

### 6.2 스키마 유연성 vs 타입 안전성

| 측면 | AS-IS (JPA) | TO-BE (JSONB) |
|------|------------|--------------|
| 컴파일 타임 검증 | Entity 클래스로 타입 체크 | Go struct으로 타입 체크 |
| DB 레벨 검증 | NOT NULL, FK, CHECK | 정규 컬럼만 (JSONB 내부 검증 없음) |
| 스키마 진화 | DDL 마이그레이션 필수 | JSONB 필드 추가는 무중단 |

**완화 방법:**
- Go 코드에서 `json.RawMessage` → 구조체 변환 시 유효성 검증
- PostgreSQL CHECK 제약 또는 JSON Schema 검증 (선택적)

### 6.3 Raw SQL vs JPA

| 측면 | AS-IS (JPA) | TO-BE (Raw SQL) |
|------|------------|----------------|
| 쿼리 작성 | JPQL/Criteria API, 자동 생성 | 직접 SQL 작성 |
| 타입 안전성 | 컴파일 타임 | 런타임 (테스트로 보완) |
| 성능 예측 | N+1 문제 발생 가능 | 예측 가능한 쿼리 실행 |
| 복잡도 | 높음 (ORM 추상화 레이어) | 낮음 (SQL 직접 제어) |

---

## 7. 요약

```
AS-IS                                  TO-BE
─────────────────────────              ─────────────────────────
17개 테이블 (6체인)                     8개 테이블 (N체인)
7-way JOIN 쿼리                        단일 테이블 쿼리
JPA merge() 멱등성                     ON CONFLICT upsert
origin_balance 혼재                     balances 테이블 분리
커서 없음                               address_cursors 전용
discriminator 기반 분류                 chain + network 정규 컬럼
VARCHAR(42~255) 비일관                  VARCHAR(128) 통합
ORM 추상화 오버헤드                     SQL 직접 제어
체인 추가 = +2 테이블                   체인 추가 = JSONB 확장
```

핵심 전환 가치: **스키마 복잡도를 체인 수에서 분리**하여, 멀티체인 확장 시
테이블 구조 변경 없이 `ChainAdapter` 구현과 `chain_data` 정의만으로 새 체인을 추가할 수 있다.
