# PRD — Multi-Chain Indexer Pipeline (Go + Node SDK Sidecar)

> 목적: Claude Code(또는 유사 코딩 에이전트)가 “바로 제품 개발”을 시작할 수 있는 수준의 요구사항/아키텍처/운영 설계를 제공한다.

## 1. Overview

### 1.1 Problem

토스 코어/뱅크/증권 등 Web2 백엔드에서 블록체인을 **백엔드 인프라 레일**처럼 사용하려면:

- 온체인 데이터(트랜잭션/이벤트/상태)를 안정적으로 수집·정규화·적재해야 하며
- 대규모 사용자(수천만) 트래픽에서도 **정합성/재처리/감사**가 가능한 “금융 등급” 인덱싱 파이프라인이 필요하다.

온체인 노드 호출 비용(레이트리밋/장애)과 체인별 SDK 유지보수 비용이 크므로,
**단순한 운영 모델 + 높은 가성비 + 안정성**이 핵심 목표다.

### 1.2 Goals

- 체인별 Cursor 기반 append 인덱싱 (EVM: height, Solana: slot, Sui/Aptos: version)
- (Reorg 체인) cursor + block_hash 기반 reorg 감지/롤백/재시작
- 파이프라인 분리(Producer/Consumer)로 병렬성 확보
- Node SDK sidecar로 체인별 디코딩/시리얼라이징 복잡도 격리
- Redis TTL로 메모리 누수 방지 + **Normalized durable backup**으로 재처리 비용 고정
- 단순 운영 config: `activation`, `targetBatchSize`, `confirmations`
- Ingester는 **체인별 single-writer**(단일 write loop)로 DB 정합성과 순서 문제를 구조적으로 단순화

### 1.3 Non-Goals

- 초기에 체인별 모든 프로토콜/프로그램/DEX 완전 지원 (단계적 확장)
- Exactly-once 메시징(큐 레벨) 보장 (대신 DB 멱등/단일 writer로 수렴)
- 실시간(초단위) 즉시성 최적화보다 안정성/정합성 우선 (SLA에 따라 튜닝)

---

## 2. Key Concepts

### 2.1 Cursor

- 체인 네이티브 단조 증가 값
  - EVM: `block_height`
  - Solana: `slot`
  - Sui/Aptos: `version` (또는 유사 sequence)
- 저장/조회/재처리의 기본 축(append 기준)
- 시간 범위 조회는 별도 매핑/인덱스로 `time -> cursor range`로 변환

### 2.2 Canonical Identity (block_hash 등)

- Reorg 체인에서 동일 cursor에서 내용이 바뀌는 것을 감지하기 위한 정체성 값
- `(cursor, block_hash)` mismatch → reorg 후보

### 2.3 Pipeline Buffer

- 파이프라인 단계 간 결합을 끊는 “버퍼”
- Redis Streams(큐) + Redis Keys(임시 payload) 조합을 기본으로 사용
- Redis TTL은 “누수 방지용 안전장치”
- **중요: 재처리 안전성은 Redis가 아니라 durable normalized backup으로 확보**

---

## 3. System Architecture

## 3.1 High-Level Components (per chain)

- **Chain Coordinator (Go)**
  - DB config sync: activation, targetBatchSize
  - targetBatchSize auto-tune (DB 강제 변경 없을 때)
  - pipeline head control(헤드 잠금/해제)
  - cursor 워터마크 관리
  - reorg 감지/롤백/재시작
- **Fetcher Pool (Go)**
  - cursor range 작업을 받아 raw 온체인 데이터 수집
  - provider pool(primary + fallback)
  - retry/backoff + adaptive effective batch sizing
- **Normalizer (Go)**
  - raw batch를 Node sidecar(gRPC)로 보내 디코딩/정규화
  - normalized batch 생성
  - normalized batch를 durable store에 백업(enqueue/async)
- **Node SDK Sidecar (Node)**
  - 체인별 디코더/시리얼라이저(가장 유지보수 잘 되는 npm 라이브러리 사용)
  - gRPC server
  - Plugin-based balance event detection: EventPlugin 인터페이스 → PluginDispatcher가 instruction ownership 기반으로 플러그인 라우팅
  - Instruction ownership 모델로 CPI 이중 기록 방지 (outer program이 inner instruction도 소유)
- **Ingester (Go, Single Writer per chain)**
  - normalized batch 및 finalize 이벤트를 단일 루프로 소비
  - batch compute + batch write로 DB 반영
- **Finalizer (Go, reorg 체인만)**
  - DB에서 confirmations 매 사이클 조회
  - 확정 대상 receipt/status 조회
  - `FinalizeTx` 이벤트를 Ingester로 발행(Redis)

---

## 4. Data Flow

### 4.1 Normal Indexing

1. Coordinator가 head cursor를 관측하고, `activation=ON`이면 FetchJob을 생성하여 enqueue
2. Fetcher는 FetchJob(cursor range)을 처리하며:
   - raw 수집
   - 실패/레이트리밋 시 effective batch 감소(최악 single-block 폴백)
   - raw payload는 Redis에 오래 쌓지 않고 곧바로 Normalizer로 흘림(또는 짧은 TTL)
3. Normalizer는 Node sidecar로 raw를 보내 decode/normalize하여 `NormalizedBatch` 생성
4. Normalizer는 `NormalizedBatch`를:
   - (a) Redis 큐에 포인터 또는 작은 이벤트로 enqueue
   - (b) Durable store에 비동기 백업 enqueue(또는 즉시 저장)
5. Ingester(single-writer)가 normalized를 소비하여 DB에 반영

### 4.2 Finalization (reorg 체인)

1. Finalizer는 매 사이클 DB에서 confirmations를 읽음
2. `finalizable_cursor <= head_cursor - confirmations` 구간 대상으로 receipt/status 확인
3. `FinalizeTx` 이벤트를 Redis에 발행
4. Ingester가 `FinalizeTx`도 소비하여 단일 시퀀스에서 DB 업데이트(confirmed 전환, 비가용→가용 잔고 전환 등)

### 4.3 Reorg Handling (cursor + block_hash)

1. Coordinator가 `(cursor, block_hash)` mismatch 감지 → fork cursor 탐색
2. **Pipeline head lock**: 새로운 FetchJob enqueue 중단
3. In-flight graceful stop: fetcher 정리(필요 시 cancel), downstream은 drain/idle
4. DB 롤백: `cursor >= fork_cursor` 삭제/무효화
5. cursor 워터마크 되돌림: `ingested_cursor = fork_cursor - 1`
6. head unlock & 재시작: 다시 enqueue 시작

---

## 5. Storage Model

### 5.1 Redis (Buffer Only)

- Redis Streams: `Q_fetch`, `Q_norm`, `Q_ingest`, `Q_finalize`
- Redis Keys:
  - raw payload key(선택): 매우 짧은 TTL 또는 최소화
  - normalized payload key(선택): TTL (누수 방지), 단 **durable backup이 진짜 안전장치**
- 원칙:
  - 큐에는 큰 payload 직접 저장 금지(가능하면 pointer)
  - TTL은 “누수 방지” 목적

### 5.2 Durable Normalized Backup

- 목표: Redis TTL/장애와 무관하게 재처리 입력을 보존
- 저장 방식(택1 또는 혼합):
  - (A) DB 테이블 `normalized_batches` (메타 + payload 일부/전체)
  - (B) Object storage(GCS 등) + DB에는 메타/키만 저장
- 최소 요구:
  - chain_id, batch_id, cursor_range
  - normalized payload 위치/해시
  - 생성 시각, schema_version, decoder_version(node sidecar version)

### 5.3 Serving DB (Final)

- Core tables: `transactions`, `balance_events`, `balances`, `tokens`
- `balance_events`: signed delta 모델 (+deposit, -withdrawal), `event_category`/`event_action` 컬럼으로 이벤트 유형 구분
- `balances`: 주소별 토큰 잔고 aggregate
- 읽기 성능을 위해 read replica 활용(서빙/감사/리포트)
- reorg 체인은 "pending/unavailable vs confirmed/available" 상태 머신 포함

---

## 6. Configuration

### 6.1 Config Items (DB Source)

- `activation` (ON/OFF, per chain)
- `targetBatchSize` (default 10k, per chain)
- `confirmations` (per chain, finalizer only; reorg 체인에서만 의미)

### 6.2 Graceful Apply Rules

- activation:
  - OFF 즉시 pipeline head lock(enqueue stop)
  - 필요 시 fetcher cancel → downstream idle로 수렴
- targetBatchSize:
  - 다음 FetchJob/다음 사이클부터 적용 (in-flight 유지)
  - fetcher 내부 effective batch는 adaptive
- confirmations:
  - finalizer의 “대상 선정” 사이클마다 읽어서 자동 반영

### 6.3 Auto-Tune (Coordinator, optional)

- DB에서 targetBatchSize를 강제로 변경하지 않는 경우에만 동작
- 입력 지표:
  - Redis lag(queue depth)
  - RPC error/timeout rate
  - fetch latency(p95), throughput(ingested_cursor가 head를 따라가는 속도)
- 제어:
  - targetBatchSize 상/하향(천천히 올리고 크게 내리기)
  - enqueue 속도 조절(head lock/soft throttle)

---

## 7. Reliability & Correctness

### 7.1 Ordering & Duplicates

- Ingester는 **체인별 single-writer**로 DB write 시퀀스를 단일화
- 큐는 at-least-once를 전제로 하되, DB 반영은:
  - 기본적으로 single-writer + cursor 워터마크로 순서 수렴
  - 3-layer dedup 모델:
    - **L1 — Instruction Ownership**: outer program이 CPI inner instruction을 소유하여 이중 기록 방지
    - **L2 — Plugin Priority**: PluginDispatcher가 우선순위 기반으로 단일 플러그인만 이벤트 생성
    - **L3 — DB Unique Constraint**: `(chain, network, tx_hash, outer_instruction_index, inner_instruction_index, address, watched_address)` 유니크 제약

### 7.2 Backpressure

- Coordinator는 Redis lag를 보고 enqueue 조절
- 파이프라인이 “쌓이기 전에” head에서 조절하는 것이 비용 최적

### 7.3 Timeouts / Circuit Breaker

- Node sidecar gRPC 호출:
  - per-request timeout/deadline 필수
  - 오류율 높으면 degrade 정책(재시도/대기/DLQ)
- RPC provider:
  - failover + exponential backoff
  - batch 축소 및 single-block 폴백

### 7.4 Reprocessing

- Redis TTL로 payload가 사라져도, durable normalized backup이 재처리 입력을 보장
- 재처리 트리거:
  - 특정 cursor range 재처리
  - 특정 배치(batch_id) 재처리
  - decoder 버전 변경 시 “최근 N일 재정규화”(옵션)

---

## 8. Observability (MVP)

- 체인별 워터마크:
  - head_cursor, ingested_cursor, finalized_cursor
- 큐 지표:
  - stream lag(Q_fetch/Q_norm/Q_ingest/Q_finalize)
- RPC 지표:
  - 성공률, timeout률, rate-limit, p95 latency
- sidecar 지표:
  - decode p95, error rate, timeout rate
- ingest 지표:
  - batch write latency, rows/sec, 실패율
- 알림:
  - lag 지속 증가
  - reorg 감지 빈도 급증
  - sidecar 오류 급증
  - provider failover 지속

---

## 9. MVP Scope

- 체인 1개(EVM 계열)부터 시작 권장
- 파이프라인:
  - activation ON/OFF
  - targetBatchSize 기본 10k + fetcher adaptive 축소
  - Redis lag 기반 enqueue 제어(간단 규칙)
  - normalized durable backup (DB 테이블 or object storage)
- reorg 체인일 경우:
  - confirmations 기반 finalizer
  - reorg 감지/롤백/재시작(최근 N 범위 내)

---

## 10. Open Questions (Implementation Choices)

- Redis Streams vs List
- durable backup 저장 위치: DB vs object storage
- normalized payload의 “재처리 충분성” 기준(필드 범주 확정)
- Solana 계정 중심 모델을 FetchJob 형태로 어떻게 추상화할지
- failover provider 계약/관측(QuickNode 등) 방식

---

## 11. Acceptance Criteria

- activation OFF 시 신규 enqueue가 멈추고 파이프라인이 안정적으로 idle로 수렴
- targetBatchSize 변경이 in-flight를 깨지 않고 다음 사이클부터 반영
- confirmations 변경이 다음 finalizer 선정 사이클부터 반영
- 노드 레이트리밋/오류 상황에서 fetcher가 effective batch를 줄이며 결국 범위를 채움
- Redis TTL로 payload가 사라져도 durable normalized backup으로 동일 범위 재처리 가능
- reorg 발생 시 fork cursor 이후 데이터가 롤백되고 재인덱싱 후 정합성이 회복됨
