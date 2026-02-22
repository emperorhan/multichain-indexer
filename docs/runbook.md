# Runbook

## Scope
- 대상: `multichain-indexer` Go 파이프라인 + `sidecar` 디코더
- 목적: 장애 인지부터 복구, 사후 분석까지 일관된 대응
- 런타임 모드:
  - `like-group` (`solana-like`, `evm-like`, `btc-like`)
  - `independent` (단일 `chain-network` 타깃)

## Severity Levels
- `sev0`: 서비스 중단 또는 데이터 손상/유실
- `sev1`: 핵심 기능 장애, 심각한 지연
- `sev2`: 부분 기능 저하
- `sev3`: 경미한 문제

## First Response Checklist
1. 장애 티켓 생성 (`type/bug`, `sev*`, `priority/*`)
2. 영향 범위 확인:
   - 런타임 모드/선택 타깃 (`RUNTIME_DEPLOYMENT_MODE`, `RUNTIME_LIKE_GROUP`, `RUNTIME_CHAIN_TARGET(S)`)
   - 체인/네트워크
   - 영향 주소 수
   - 누락 또는 중복 가능성
3. 즉시 완화책 적용:
   - ingest 중단 필요 여부 판단
   - sidecar 또는 indexer 재시작
4. 의사결정 필요 시 `decision-needed` 이슈 생성

## Operational Checks

### Go Indexer
1. 프로세스 상태 확인
2. 최근 에러 로그 확인 (`coordinator`, `fetcher`, `normalizer`, `ingester`)
3. 병목 구간 확인:
   - RPC 호출 실패율
   - gRPC timeout
   - DB transaction 실패율
4. 런타임 wiring preflight 실패 여부 확인:
   - 선택된 타깃만 검증 대상으로 간주
   - 중복 target, nil adapter, adapter chain mismatch는 즉시 부팅 실패(의도된 fail-fast)
   - `independent` 모드에서는 정확히 1개 타깃만 허용
5. Preflight connectivity 검증 확인:
   - 시작 시 DB + RPC ping을 5s timeout으로 수행
   - 연결 실패 시 즉시 종료 (fail-fast)

### Sidecar
1. gRPC health check 상태 확인
2. 현재 sidecar는 단일 배포 단위(체인별 디코더 코드는 내부 분리)임을 전제로 장애 전파 여부 확인
3. payload 체인 분기 오류/디코더 예외 로그 확인
4. Sidecar test/build 재검증

### Database
1. 최근 `balance_events`, `transactions` 적재 추이 확인
2. 워터마크 전진 여부 확인 (`pipeline_watermarks`)
3. 최근 migration 적용 상태 확인

## Monitoring & Alerts

### Prometheus Alerts (`deployments/prometheus/alerts.yaml`)

| Alert | Condition | Severity | 대응 |
|-------|-----------|----------|------|
| `IngesterHighErrorRate` | `rate(indexer_ingester_errors_total[5m]) > 0.1` | critical | → Playbook C (DB 장애) 또는 Playbook E (벌크 인서트 실패) |
| `FetcherHighErrorRate` | `rate(indexer_fetcher_errors_total[5m]) > 0.1` | critical | → Playbook A (RPC 장애) |
| `NormalizerHighErrorRate` | `rate(indexer_normalizer_errors_total[5m]) > 0.1` | critical | → Playbook B (Sidecar 디코드 실패) |
| `IngesterLatencyHigh` | P99 > 5s | warning | batch_size 축소 또는 DB 성능 점검 |
| `CursorStalled` | 10분간 cursor 변동 없음 | warning | 파이프라인 전 스테이지 점검 |
| `DBPoolExhausted` | in_use/open > 90% | warning | `DB_MAX_OPEN_CONNS` 증가 또는 체인별 DB 풀 분리 |

### Grafana Dashboard

- 대시보드 위치: `deployments/grafana/dashboards/multichain-indexer.json`
- 4개 Row: Pipeline Overview, Latency, Ingester Details, Infrastructure
- 필터: `chain`, `network` template variables
- Grafana 접근: `http://localhost:3000` (docker-compose) 또는 K8s port-forward

### Alert System 동작 특성

- **Alerter state-transition awareness**: 알림 타입이 변경되면(예: UNHEALTHY -> RECOVERY) cooldown을 bypass하여 즉시 알림 발생
- **Reorg detector**: 연속 RPC 에러 카운터 관리. 5회 연속 실패 시 alert 발생
- **Per-key cooldown**: 동일 키에 대해 `ALERT_COOLDOWN_MS` (기본 30분) 동안 중복 알림 억제

### Key Metrics 체크리스트

```bash
# 파이프라인 처리량 확인 (Prometheus query)
rate(indexer_ingester_batches_processed_total[5m])

# P99 레이턴시 확인
histogram_quantile(0.99, rate(indexer_ingester_batch_duration_seconds_bucket[5m]))

# DB 풀 사용량
indexer_postgres_db_pool_in_use / indexer_postgres_db_pool_open

# 커서 진행 확인
indexer_pipeline_cursor_sequence

# RPC 레이트 리밋 대기
rate(indexer_rpc_rate_limit_waits_total[5m])

# 토큰 deny 캐시 히트율
indexer_denied_cache_hits_total / (indexer_denied_cache_hits_total + indexer_denied_cache_misses_total)
```

## Admin API

Admin API가 활성화된 경우 (`ADMIN_ADDR` 설정 시):

```bash
# 감시 주소 조회
curl http://localhost:9090/admin/v1/watched-addresses?chain=solana&network=devnet

# 감시 주소 추가
curl -X POST http://localhost:9090/admin/v1/watched-addresses \
  -H 'Content-Type: application/json' \
  -d '{"chain":"solana","network":"devnet","address":"NEW_ADDRESS"}'

# 파이프라인 상태 (워터마크) 조회
curl http://localhost:9090/admin/v1/status?chain=solana&network=devnet
```

### Admin Dashboard

Admin API가 활성화된 경우, 내장 대시보드를 `/dashboard`에서 접근 가능 (인증 미들웨어 적용):

```bash
# 브라우저에서 접근
open http://localhost:9090/dashboard

# Dashboard JSON API
curl http://localhost:9090/admin/v1/dashboard/overview
curl http://localhost:9090/admin/v1/dashboard/chains
curl http://localhost:9090/admin/v1/dashboard/events
```

## Circuit Breaker Troubleshooting

Circuit breaker는 Fetcher(RPC)와 Normalizer(sidecar)에 적용되어 있다 (`internal/circuitbreaker/breaker.go`).

### 상태 진단

| 상태 | 의미 | 대응 |
|------|------|------|
| CLOSED | 정상 동작 | 조치 불필요 |
| OPEN | 연속 실패 임계 초과, 요청 즉시 차단 | RPC/sidecar 상태 확인 후 복구 대기 |
| HALF-OPEN | 프로빙 시작, 소량 요청 전달 | 프로빙 성공/실패 모니터링 |

### 주요 점검

1. **OPEN 진입 원인**: 로그에서 `circuit breaker open` 메시지 확인. 연속 실패 횟수 및 원인(timeout, connection refused 등) 파악
2. **HALF-OPEN 프로빙**: OPEN 상태 유지 시간 경과 후 자동으로 HALF-OPEN 전환. 프로빙 요청 성공 시 CLOSED 복귀
3. **수동 개입**: CB 설정은 YAML/환경변수로 외부화 (`CircuitBreakerConfig` in `FetcherStageConfig`/`NormalizerStageConfig`). 임계값 조정 후 재시작 가능
4. **Alerter 연동**: CB OPEN 시 alert 발생. 상태 전환(예: UNHEALTHY -> RECOVERY)은 cooldown을 bypass함

### 관련 설정

```yaml
fetcher:
  circuit_breaker:
    max_failures: 5          # OPEN 전환 임계값
    timeout: 30s             # OPEN 유지 시간 (이후 HALF-OPEN)
    half_open_max_calls: 2   # HALF-OPEN 프로빙 요청 수

normalizer:
  circuit_breaker:
    max_failures: 5
    timeout: 30s
    half_open_max_calls: 2
```

## Shutdown

종료 시 30초 타임아웃이 적용된다. 30초 내에 graceful shutdown이 완료되지 않으면 `os.Exit(1)`로 강제 종료된다.
파이프라인 종료 시 진행 중인 배치 처리 완료를 대기하되, 타임아웃 초과 시 stuck goroutine 방지를 위해 강제 종료한다.

## Recovery Playbooks

### A. RPC Degradation
1. RPC endpoint 장애 여부 확인
2. **Circuit breaker 상태 확인**: Fetcher CB가 OPEN 상태인지 로그에서 확인 (`circuit breaker open` 메시지). OPEN 상태이면 RPC 호출이 즉시 실패 처리됨
3. `indexer_rpc_rate_limit_waits_total` 메트릭으로 레이트 리밋 확인
4. endpoint failover 또는 요청 간격 완화
5. 체인별 RPC rate limit 조정 (`SOLANA_RPC_RATE_LIMIT`, `BASE_RPC_RATE_LIMIT` 등)
6. fetch workers와 batch size 임시 조정
7. **참고**: 파이프라인은 자동 재시작됨 (exponential backoff: 1s -> 2s -> 4s -> ... -> 5min cap). `context.Canceled`만 영구 종료를 유발함. Worker goroutine 패닉 시 defer-recover로 복구 후 재시작됨

### B. Sidecar Decode Failures
1. 실패 트랜잭션 샘플 추출
2. **Circuit breaker 상태 확인**: Normalizer CB가 OPEN 상태인지 확인. OPEN이면 sidecar 호출이 즉시 실패 처리됨. HALF-OPEN 프로빙이 시작되면 소량 요청이 sidecar로 전달됨
3. 체인별로 분리 진단 (`solana`, `base`, `btc`) 후 blast radius 확인
4. OTel 트레이스로 `normalizer.grpcDecode` 스팬 확인
5. 디코더 로직 회귀 여부 확인
6. hotfix PR 생성 후 CI 통과 확인, 단계적 배포
7. **참고**: Worker goroutine 패닉 시 defer-recover로 복구됨. 파이프라인은 exponential backoff로 자동 재시작

### C. DB Write Failures
1. DB 연결/락/트랜잭션 오류 구분
2. DB 풀 메트릭 확인: `indexer_postgres_db_pool_*`
3. 벌크 인서트 에러 로그 확인 (phase2_prefetch, phase4_write)
4. 일시 장애면 재시작 후 커서 기반 재처리
5. 지속 장애면 ingest 중단 후 원인 제거
6. 체인별 DB 풀 분리 고려 (`SOLANA_DB_URL`, `BASE_DB_URL` 등)

### D. Ingester Bulk Insert 실패
1. OTel 트레이스에서 `ingester.collectEvents`, `ingester.prefetchBulkData`, `ingester.buildEventModels`, `ingester.writeBulkAndCommit` 스팬 확인
2. 에러 로그에서 실패한 bulk 연산 식별 (BulkUpsertTx, BulkAdjustBalanceTx 등)
3. 특정 트랜잭션/토큰/밸런스 키의 데이터 이상 여부 확인
4. 필요시 Admin API `/admin/v1/replay`로 특정 주소 리플레이
5. **참고**: Ingester Run loop에 defer-recover가 적용되어 패닉 시 자동 복구. 파이프라인은 exponential backoff로 자동 재시작

## Kubernetes Operations (Helm)

### 배포

```bash
# 설치
helm install indexer deployments/helm/multichain-indexer/ \
  --set secret.dbUrl="postgres://..."

# 업그레이드
helm upgrade indexer deployments/helm/multichain-indexer/ \
  --set image.tag=v1.2.3

# 롤백
helm rollback indexer [REVISION]

# 상태 확인
helm status indexer
kubectl get pods -l app.kubernetes.io/name=multichain-indexer
```

### HPA (Horizontal Pod Autoscaler)

```bash
# HPA 활성화
helm upgrade indexer deployments/helm/multichain-indexer/ \
  --set autoscaling.enabled=true \
  --set autoscaling.minReplicas=2 \
  --set autoscaling.maxReplicas=8

# HPA 상태 확인
kubectl get hpa
kubectl describe hpa multichain-indexer
```

### 트러블슈팅

```bash
# 파드 로그
kubectl logs -f deployment/multichain-indexer

# 헬스체크
kubectl port-forward svc/multichain-indexer 8080:8080
curl http://localhost:8080/healthz

# 리소스 사용량
kubectl top pods -l app.kubernetes.io/name=multichain-indexer
```

## Rollback Policy
1. 배포 단위를 PR 단위로 롤백 (Helm: `helm rollback indexer`)
2. 스키마 변경 포함 시 down migration 영향 검토 후 실행 (현재 022번까지 적용)
3. `balance_events`는 flat 테이블 (파티셔닝 제거됨, migration 018)
4. sidecar/indexer 계약 변경이 포함된 경우 sidecar 우선 롤백 후 indexer 정합성 확인
5. 롤백 후 무결성 검증:
   - 중복 이벤트 발생 여부 (`event_id` UNIQUE INDEX로 dedup)
   - 커서 역행 여부

## Load Testing

```bash
# 기본 부하 테스트 (로컬 DB)
go run ./test/loadtest

# 커스텀 파라미터
go run ./test/loadtest \
  -db-url "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable" \
  -batch-size 100 \
  -concurrency 8 \
  -duration 60s \
  -migrate
```

결과물: throughput (batches/sec, events/sec), latency (p50/p95/p99), error rate

## Postmortem
1. 24시간 내 회고 문서 작성
2. 재발 방지 액션을 Issue로 생성
3. 런북/테스트/알람 규칙 업데이트
