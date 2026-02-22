# DB Schema Audit — 2026-02-22

## 목적
- 금융권 운영 기준에서 DB 스키마의 불필요 구조를 식별한다.
- 기준은 `누락/중복 없는 이벤트 적재`, `트랜잭션 분류`, `최소 단위 잔고 변화 추적`, `잔고 계산 정합성`이다.

## 범위
- 마이그레이션: `internal/store/postgres/migrations/001~023`
- 런타임 SQL 사용처: `internal/store/postgres/*`, `internal/pipeline/*`, `internal/admin/*`

## 결론 요약
- 핵심 정합성 경로(`event_id` dedup + watermark + 단일 커밋 경계)는 유지되고 있다.
- 7건의 스키마 이슈가 식별되었으며, migration 023 + Go 코드 수정으로 **전부 해결 완료**.

## Findings

| ID | Severity | 판정 | 내용 | 조치 | Status |
|---|---|---|---|---|---|
| DB-AUD-01 | High | 불필요(조건부) | `balance_events_*_old` 계열 레거시 테이블이 up 경로에서 장기 잔존 | Migration 023: `DROP TABLE IF EXISTS ... CASCADE` (4개 테이블) | **Fixed** |
| DB-AUD-02 | High | 불필요 | `pipeline_watermarks.head_sequence`는 쓰기 경로 없는 dead column | Migration 023: `ALTER TABLE DROP COLUMN`. Go 코드: model/repo/admin에서 HeadSequence 제거 | **Fixed** |
| DB-AUD-03 | High | 불필요 | `indexer_configs.rpc_url`는 스키마에만 있고 저장/적용 경로가 없음 | Migration 023: `ALTER TABLE DROP COLUMN`. Go 코드: `ConfigKeyRPCURL` 상수 제거 | **Fixed** |
| DB-AUD-04 | Medium | 불필요 | `balances.pending_withdrawal_amount`는 조회만 있고 갱신 경로 부재 | Migration 023: `ALTER TABLE DROP COLUMN`. Go 코드: model/repo에서 필드 제거 | **Fixed** |
| DB-AUD-05 | Medium | 중복 인덱스 | `runtime_configs`에 `idx_runtime_config_active`(partial)와 `idx_runtime_configs_lookup`(full)가 동시 존재 | Migration 023: `DROP INDEX idx_runtime_configs_lookup` | **Fixed** |
| DB-AUD-06 | Medium | 중복 인덱스 | `indexed_blocks`의 `idx_indexed_blocks_cursor`(DESC)는 PK backward scan으로 대체 가능 | Migration 023: `DROP INDEX idx_indexed_blocks_cursor` | **Fixed** |
| DB-AUD-07 | Critical | 정합성 리스크 | `balance_events.id`는 UUID인데 replay service가 `int64`로 scan → purge 전체 동작 불가 | `replay/service.go`: `rollbackEvent.id` int64→uuid.UUID, 페이지네이션 변수 uuid.UUID로 전환 | **Fixed** |

## 유지 권고 구조
- `token_deny_log`: 실제 write 경로 사용 중이므로 유지.
  - `internal/store/postgres/migrations/008_token_scam_detection.up.sql:13`
  - `internal/store/postgres/token_repo.go:90`
- `address_books`: admin 조회/관리 경로에서 활성 사용 중이므로 유지.
  - `internal/store/postgres/migrations/017_address_books.up.sql:1`
  - `internal/store/postgres/address_book_repo.go:22`
- `balance_reconciliation_snapshots`: reconciliation 스냅샷 저장 경로에서 사용 중이므로 유지.
  - `internal/store/postgres/migrations/016_reconciliation_snapshots.up.sql:1`
  - `internal/store/postgres/reconciliation_repo.go:43`

## FK 관점 정리
- 현재 설계는 throughput 우선으로 `balance_events`의 강한 FK 의존을 줄인 상태다.
- 이 방향 자체는 가능하지만, FK를 약하게 가져갈수록 보상 통제가 필요하다.
- 최소 보상 통제는 다음 3개다.
1. orphan 레코드 감시 쿼리(일 배치)
2. replay/reorg 경로의 정합성 테스트(실DB 통합)
3. watermark/event dedup 불변식 모니터링

## Migration 023 요약

**파일:** `internal/store/postgres/migrations/023_schema_cleanup.up.sql`

| 항목 | SQL |
|------|-----|
| DB-AUD-01 | `DROP TABLE IF EXISTS balance_events_old, balance_events_monthly_old, balance_events_monthly_old_default, balance_events_partitioned_old CASCADE` |
| DB-AUD-02 | `ALTER TABLE pipeline_watermarks DROP COLUMN IF EXISTS head_sequence` |
| DB-AUD-03 | `ALTER TABLE indexer_configs DROP COLUMN IF EXISTS rpc_url` |
| DB-AUD-04 | `ALTER TABLE balances DROP COLUMN IF EXISTS pending_withdrawal_amount` |
| DB-AUD-05 | `DROP INDEX IF EXISTS idx_runtime_configs_lookup` |
| DB-AUD-06 | `DROP INDEX IF EXISTS idx_indexed_blocks_cursor` |

Down migration은 인덱스/컬럼 복원을 지원하나, `_old` 테이블 데이터는 복원 불가(백업 필요).

## 비고
- 본 문서는 구조 점검 결과를 정리한 것으로, migration 023 적용 전 운영 DB에서 사전 점검 SQL 실행을 권고한다.
- 모든 코드 변경은 빌드 및 전체 테스트 통과 확인됨.
