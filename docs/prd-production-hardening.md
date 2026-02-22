# PRD: Production Hardening — 상용화 최종 점검 대응

## 배경

P0(보안) + P1(안정성) + P2(운영 성숙도) 구현 완료 후 상용화 점검을 수행한 결과,
Critical 4건 / High 11건 / Medium 18건의 이슈가 식별되었다.
이 문서는 상용 배포 전 반드시 해결해야 할 항목과 이후 안정화 단계에서 처리할 항목을 정의한다.

---

## P0 — Critical (상용 배포 차단)

### C-1. Admin API 인증 추가

- **현상**: Admin API(`/admin/v1/*`)에 인증 없이 누구나 감시 주소 추가/조회 가능
- **위치**: `cmd/indexer/main.go:806-826`, `internal/admin/server.go`
- **해결**: 기존 metrics Basic Auth 미들웨어(`basicAuthMiddleware`) 재활용하여 Admin API에 적용
- **환경변수**: `ADMIN_AUTH_USER`, `ADMIN_AUTH_PASS` (설정 안 하면 시작 시 경고)
- **검증**: `go test ./...`, 인증 없는 요청 → 401

### C-2. Bulk 메서드 통합 테스트 추가

- **현상**: 프로덕션 핫패스(BulkUpsertTx, BulkAdjustBalanceTx, BulkGetAmountWithExistsTx, BulkIsDeniedTx) 실제 PostgreSQL 대상 테스트 0건
- **위치**: `internal/store/postgres/integration_test.go`
- **해결**: `integration_test.go`에 Bulk 메서드별 테스트 추가
  - `TestBulkUpsertTx_Transactions` — 다건 insert + 중복 ON CONFLICT + RETURNING id 매핑
  - `TestBulkUpsertTx_Tokens` — 다건 insert + 중복 + id 매핑
  - `TestBulkIsDeniedTx` — denied/allowed 토큰 혼합 조회
  - `TestBulkGetAmountWithExistsTx` — 존재/미존재 잔고 조회 + FOR UPDATE
  - `TestBulkAdjustBalanceTx` — 다건 delta 적용 + ON CONFLICT 잔고 합산
  - `TestBulkUpsertTx_BalanceEvents` — canonical ID 일괄 해석 + 다건 insert
- **검증**: `go test -tags integration ./internal/store/postgres/ -run TestBulk`

### C-3. Statement timeout 커넥션 풀 전체 적용

- **현상**: `SET statement_timeout`이 단일 커넥션에만 적용, 나머지 풀 커넥션은 무제한
- **위치**: `internal/store/postgres/db.go:50-55`
- **해결**: `SET statement_timeout` 세션 실행 대신, DB URL에 `options=-c statement_timeout=30000` 파라미터 추가 방식으로 전환. 또는 `sql.DB.SetConnInitFunc`(Go 1.24 미지원 시 `connector` 커스텀) 사용
- **검증**: 풀에서 꺼낸 커넥션마다 `SHOW statement_timeout` 확인 테스트

### C-4. `big.Int.SetString` 실패 시 에러 반환

- **현상**: delta 파싱 실패 시 `delta=0`으로 무시, 잔고 미반영
- **위치**: `internal/pipeline/ingester/ingester.go:698-699`, `:729`
- **해결**: `SetString` 반환값 확인, `false`면 해당 이벤트 에러 로깅 + 배치 실패 반환
- **검증**: 잘못된 delta 문자열 입력 시 에러 반환 단위 테스트

---

## P1 — High (1주 내 해결)

### H-1. Admin API CORS/CSRF 보호

- **현상**: Cross-origin 요청 제한 없음
- **해결**: Admin API에 `Access-Control-Allow-Origin` 헤더 명시적 설정 (기본: 없음 → 브라우저 차단)
- **위치**: `internal/admin/server.go`

### H-2. Helm values.yaml 기본 DB URL 제거

- **현상**: `dbUrl: "postgres://indexer:indexer@..."` 하드코딩
- **해결**: 기본값을 빈 문자열로 변경, 필수 값 미설정 시 Helm install 실패하도록 `required` 체크
- **위치**: `deployments/helm/multichain-indexer/values.yaml:186`

### H-3. Admin API 입력 검증 강화

- **현상**: chain/network가 허용값 외 임의 문자열 허용, address 형식 미검증
- **해결**: `model.Chain`, `model.Network` 상수 기반 화이트리스트 검증 추가
- **위치**: `internal/admin/server.go`

### H-4. Admin API 테스트 작성 -- DONE

- **현상**: `internal/admin/` 테스트 0건
- **해결**: `server_test.go` 작성 — httptest 기반 핸들러 테스트 (정상, 400, 500)
- **위치**: `internal/admin/server_test.go`
- **상태**: DONE (`internal/admin/server_test.go` 구현 완료)

### H-5. Rate limiter 테스트 작성 -- DONE

- **현상**: `internal/chain/ratelimit/` 테스트 0건
- **해결**: `limiter_test.go` 작성 — Allow/Wait 동작, 컨텍스트 취소, 메트릭 카운터 검증
- **위치**: `internal/chain/ratelimit/limiter_test.go`
- **상태**: DONE (`internal/chain/ratelimit/limiter_test.go` 구현 완료)

### H-6. Partition manager 테스트 작성 -- OBSOLETE

- **현상**: 파티션 생성/삭제 코드 테스트 0건
- **상태**: OBSOLETE -- PartitionManager가 완전 삭제됨. `balance_events`는 flat 테이블로 전환되어 daily partitioning이 제거됨. 해당 항목은 더 이상 유효하지 않음

### H-7. Pool registry 테스트 작성

- **현상**: 체인별 DB 풀 관리 코드 테스트 0건
- **해결**: `pool_registry_test.go` 작성 — Register, Get (fallback), Close 검증
- **위치**: `internal/store/postgres/pool_registry_test.go` (신규)

### H-8. 음수 잔고 보호

- **현상**: `balances.amount`에 DB CHECK 제약 없음
- **해결**: 마이그레이션 추가: `ALTER TABLE balances ADD CONSTRAINT chk_balance_non_negative CHECK (amount >= 0)`
  - 단, reorg 복구 시 일시적 음수 가능 → ingester에서 사전 검증 추가 (delta 적용 전 잔고 확인)
- **위치**: `internal/store/postgres/migrations/012_balance_non_negative.up.sql` (신규)
- **참고**: 즉시 적용 시 기존 음수 잔고가 있으면 마이그레이션 실패. `NOT VALID` + `VALIDATE` 분리 적용 권장

### H-9. 마이그레이션 버전 추적

- **현상**: `RunMigrations`가 매번 모든 SQL 실행, 멱등성 미보장
- **해결**: `schema_migrations` 테이블 + 적용 기록 관리. 또는 `golang-migrate/migrate` 라이브러리 채택
- **위치**: `internal/store/postgres/db.go:69-85`

### H-10. Dockerfile non-root USER 추가

- **현상**: scratch 이미지에서 root(UID 0)로 실행
- **해결**: `USER 65534:65534` 추가 (Dockerfile + sidecar Dockerfile)
- **위치**: `Dockerfile`, `sidecar/Dockerfile`

### H-11. Partition swap DOWN 마이그레이션 안전성

- **현상**: `_old` 테이블 존재 가정, 수동 삭제 시 롤백 불가
- **해결**: DOWN 마이그레이션에 `_old` 테이블 존재 여부 체크 추가 (IF EXISTS + 경고)
- **위치**: `migrations/007...down.sql`, `migrations/011...down.sql`

---

## P2 — Medium (상용 안정화 단계)

| # | 이슈 | 위치 | 상태 |
|---|------|------|------|
| M-1 | Admin API `MaxBytesReader` 적용 | `admin/server.go` | DONE (H-3에서 구현) |
| M-2 | TLS 기본 활성화 (Helm) | `values.yaml` | DONE |
| M-3 | Redis URL 로그 마스킹 누락 경로 | `main.go:113` | OBSOLETE (Redis 완전 제거됨 -- StreamTransport, RedisConfig 삭제) |
| M-4 | RPC URL 로그 API 키 마스킹 | `main.go:362-385` | DONE |
| M-5 | 잘못된 정수/실수 env var silent fallback | `config.go:getEnvInt` | DONE |
| M-6 | ADMIN_ADDR, rate limit 값 검증 | `config.go` | DONE |
| M-7 | Watermark GREATEST reorg 되감기 불가 | `indexer_config_repo.go:80` | DONE |
| M-8 | 과거 데이터 DEFAULT 파티션 잔류 | `migrations/011` | OBSOLETE (파티셔닝 제거됨, flat 테이블 전환) |
| M-9 | `id` 쿼리 시 전 파티션 스캔 | `balance_event_repo.go:92` | OBSOLETE (파티셔닝 제거됨, flat 테이블이므로 파티션 스캔 이슈 해당 없음) |
| M-10 | `balance_applied` 인덱스 누락 | `migrations/013` | DONE |
| M-11 | 커넥션 풀 기본값 7체인 부족 | `config.go:201` | DONE (25→50/10) |
| M-12 | OTel 샘플러 미설정 | `tracing.go` | DONE (TraceIDRatioBased + ParentBased) |
| M-13 | Coordinator tick 히스토그램 누락 | `metrics.go` | DONE |
| M-14 | 히스토그램 버킷 불일치 | `metrics.go` | DONE (스테이지별 커스텀 버킷) |
| M-15 | Helm PDB/NetworkPolicy 누락 | `helm/templates/` | DONE |
| M-16 | Readiness probe `/readyz` 전환 | `values.yaml` | DONE |
| M-17 | E2E 테스트 에러 시나리오 추가 | `normalizer/*_error_test.go` | DONE |
| M-18 | 부하 테스트 데이터 무결성 검증 | `test/loadtest/main.go` | DONE |

---

## 추가 구현 완료 항목 (Production Hardening)

상용화 점검 이후 추가로 구현된 안정성/운영 강화 항목:

| # | 항목 | 위치 | 상태 |
|---|------|------|------|
| PH-1 | Circuit breaker (Fetcher RPC + Normalizer sidecar) | `internal/circuitbreaker/breaker.go` | DONE |
| PH-2 | CB 설정 외부화 (YAML+env vars) | `FetcherStageConfig`, `NormalizerStageConfig` | DONE |
| PH-3 | Pipeline auto-restart (exponential backoff 1s->5min cap) | `internal/pipeline/pipeline.go` | DONE |
| PH-4 | Worker panic recovery (defer-recover) | fetcher/normalizer goroutine, ingester Run loop | DONE |
| PH-5 | Shutdown timeout 30s -> os.Exit(1) | `cmd/indexer/main.go` | DONE |
| PH-6 | Preflight connectivity validation (DB+RPC ping, 5s timeout) | `cmd/indexer/main.go` | DONE |
| PH-7 | Config validation (MaxOpenConns<=200, BatchSize<=10000) | `internal/config/` | DONE |
| PH-8 | Alerter state-transition awareness (type change bypasses cooldown) | `internal/alert/alerter.go` | DONE |
| PH-9 | Reorg detector consecutiveRPCErrs (5회 연속 실패 alert) | `internal/pipeline/reorgdetector/` | DONE |
| PH-10 | Empty sentinel batch (빈 블록 범위 워터마크 전진) | fetcher/normalizer/ingester | DONE |
| PH-11 | Block-scan batch chunking (BlockScanMaxBatchTxs=500) | coordinator/fetcher | DONE |
| PH-12 | Configurable interleaver (InterleaveMaxSkewMs=250) | coordinator | DONE |
| PH-13 | Coordinator job dedup (lastEnqueued range tracking) | coordinator | DONE |
| PH-14 | 6차례 성능 최적화 | 전체 파이프라인 | DONE |
| PH-15 | Redis 완전 제거 | 전체 | DONE |
| PH-16 | PartitionManager 삭제 + flat 테이블 전환 | store/postgres | DONE |
| PH-17 | Per-address cursor 제거 (watermark-only) | ingester | DONE |

---

## 구현 순서

| 단계 | 항목 | 비고 |
|------|------|------|
| 1 | C-1 (Admin Auth) + C-4 (delta 파싱) | 즉시, 독립적 |
| 2 | C-3 (Statement timeout) + H-10 (Dockerfile USER) | 즉시, 독립적 |
| 3 | H-1~H-3 (Admin 보안 강화) + H-2 (Helm 자격증명) | Admin 관련 일괄 |
| 4 | H-4~H-7 (테스트 추가 4건) | 병렬 작성 가능 |
| 5 | C-2 (Bulk 통합 테스트) | DB 필요, 별도 |
| 6 | H-8 (음수 잔고) + H-9 (마이그레이션 추적) + H-11 (DOWN 안전성) | DB/마이그레이션 |
| 7 | M-1~M-18 (Medium 항목) | 안정화 단계 |

## 검증

각 단계 완료 후:
```bash
go build ./cmd/indexer
go test ./... -count=1
go vet ./...
```
