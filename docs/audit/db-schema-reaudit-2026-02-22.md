# DB Schema Re-Audit — 2026-02-22 (2nd Pass)

## 목적
- 기존 스키마 감사 이후, 현재 코드 기준으로 불필요 구조가 남아있는지 재점검한다.
- 기준은 운영 효율(throughput/메모리), 유지보수성, 데이터 정합성(누락/중복/잔고 계산)이다.

## 범위
- 마이그레이션: `internal/store/postgres/migrations/001~024`
- 런타임 SQL 사용처: `internal/store/postgres/*`, `internal/pipeline/*`, `internal/admin/*`, `cmd/indexer/*`

## 요약 결론
- `023_schema_cleanup`으로 기존 dead schema가 정리되었다.
- 2차 점검에서 식별된 5건도 migration 024 + Go 코드 리팩터링으로 **전부 해결 완료**.

## 023 적용으로 정리된 항목 (재확인)
- 레거시 테이블 제거: `023_schema_cleanup.up.sql`
- `pipeline_watermarks.head_sequence` 제거
- `indexer_configs.rpc_url` 제거
- `balances.pending_withdrawal_amount` 제거
- `idx_runtime_configs_lookup` 제거
- `idx_indexed_blocks_cursor` 제거

## 2차 점검 Findings

| ID | Severity | 내용 | 조치 | Status |
|---|---|---|---|---|
| DB-RA-01 | High | `indexer_configs` 테이블 유휴 — 프로덕션 read/write 경로 0건 | Migration 024: `DROP TABLE`. Go 코드: `IndexerConfigRepository` → `WatermarkRepository` 리네이밍, `Upsert` 제거, `IndexerConfig` 모델 삭제 | **Fixed** |
| DB-RA-02 | Medium | `transactions`의 `idx_tx_fee_payer`, `idx_tx_block_time` 미사용 | Migration 024: `DROP INDEX` (2개) | **Fixed** |
| DB-RA-03 | Medium | `balances.idx_balances_wallet` 미사용 — wallet_id WHERE 쿼리 0건 | Migration 024: `DROP INDEX` | **Fixed** |
| DB-RA-04 | Medium | `balance_events` 보조 인덱스 과다 — 11개 중 5개 미사용 + 1개 컬럼 불일치 | Migration 024: 미사용 5개 DROP + `idx_be_watched`(block_time) → `idx_be_watched_cursor`(block_cursor) 교체 | **Fixed** |
| DB-RA-05 | Low-Medium | `balance_reconciliation_snapshots` 인덱스 3개 — SELECT 쿼리 0건 (INSERT only) | Migration 024: `DROP INDEX` (3개) | **Fixed** |

## Migration 024 요약

**파일:** `internal/store/postgres/migrations/024_index_and_table_cleanup.up.sql`

| 항목 | SQL |
|------|-----|
| DB-RA-01 | `DROP TABLE IF EXISTS indexer_configs CASCADE` |
| DB-RA-02 | `DROP INDEX idx_tx_fee_payer`, `DROP INDEX idx_tx_block_time` |
| DB-RA-03 | `DROP INDEX idx_balances_wallet` |
| DB-RA-04 | `DROP INDEX idx_be_address/wallet/token/activity/wallet_activity` (5개) + `DROP idx_be_watched` → `CREATE idx_be_watched_cursor (chain,network,watched_address,block_cursor)` |
| DB-RA-05 | `DROP INDEX idx_recon_snapshots_chain_network/checked_at/mismatch` (3개) |

**총 제거:** 테이블 1개, 인덱스 12개 DROP + 인덱스 1개 교체 생성

## Go 코드 리팩터링 요약

| 변경 | 내용 |
|------|------|
| 인터페이스 | `IndexerConfigRepository` → `WatermarkRepository` |
| 구현체 | `indexerConfigRepo` → `watermarkRepo`, `NewIndexerConfigRepo` → `NewWatermarkRepo` |
| 모델 | `IndexerConfig` 구조체 삭제 |
| 메서드 | `Upsert()` 제거 (테이블 DROP됨) |
| 필드명 | `configRepo` → `wmRepo` (coordinator, ingester, replay, admin, registry) |
| 파이프라인 | `Repos.Config` → `Repos.Watermark` |
| 테스트 | 전체 mock/test fixture에서 Upsert/Get 제거, 타입명 갱신 |

## 유지 권고 구조
- `token_deny_log`: 보안/감사 목적으로 활성 사용
- `address_books`: admin 기능 경로에서 활성 사용
- `balance_reconciliation_snapshots` 테이블 자체: reconciliation 감사 이력 저장 목적 유효 (인덱스만 제거)

## 비고
- 빌드 및 전체 테스트 통과 확인됨.
- 운영 환경 적용 시 migration 024는 인덱스 DROP이 대부분이므로 즉시 완료되나, `indexer_configs` DROP 전 백업 권고.
