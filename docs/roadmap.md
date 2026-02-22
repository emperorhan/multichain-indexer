# Roadmap

## Objective
운영 신뢰성을 갖춘 멀티체인 인덱서를 표준 운영 체계로 정착한다.

## Milestones

### M1. Collaboration Baseline
- 상태: done
- 산출물:
  - GitHub Issue/PR 템플릿
  - CODEOWNERS
  - CI 필수 게이트
  - 라벨 규칙 및 자동 라벨링

### M2. Reliability Hardening
- 상태: **done**
- 구현 완료:
  - Prometheus 메트릭 + Grafana 대시보드 (`internal/metrics/`)
  - Alert 시스템: Slack/Webhook, per-key cooldown (`internal/alert/`)
  - Replay 서비스: Admin API 기반 재처리 (`internal/pipeline/replay/`)
  - Reorg 감지 + 롤백 (`internal/pipeline/reorgdetector/`)
  - OpenTelemetry 분산 트레이싱 (`internal/tracing/`)
  - 운영 런북 + Recovery Playbooks (`docs/runbook.md`)
  - 63개 테스트 파일, race detector 전체 통과

### M3. Data Quality Guarantees
- 상태: **done**
- 구현 완료:
  - Reconciliation 서비스: 온체인 vs DB 잔액 검증 (`internal/reconciliation/`)
  - `balance_events` event_id 기반 UNIQUE INDEX dedup
  - Finality 상태 관리 + Finality promotion (`internal/pipeline/finalizer/`)
  - 커서 정합성: GREATEST watermark (비퇴행)
  - 배치 처리 메트릭: throughput, latency (P50/P95/P99), error rate

### M4. Chain/Plugin Expansion
- 상태: **done**
- 구현 완료:
  - 7개 체인 어댑터: Solana, Base, Ethereum, BTC, Polygon, Arbitrum, BSC
  - `BlockScanAdapter` 인터페이스로 EVM/BTC 블록 범위 스캔 표준화
  - 체인별 normalizer 분리: `normalizer_balance.go` (공통+Solana), `normalizer_balance_evm.go`, `normalizer_balance_btc.go`
  - 체인별 RPC rate limiting (`internal/chain/ratelimit/`)
  - Sidecar 디코더: Solana, Base, BTC

### M5. Production Hardening
- 상태: **done**
- 구현 완료:
  - Circuit breaker: Fetcher(RPC) + Normalizer(sidecar) 통합 (`internal/circuitbreaker/breaker.go`), 설정 외부화 (YAML+env vars)
  - Pipeline auto-restart: exponential backoff (1s -> 2s -> 4s -> ... -> 5min cap), `context.Canceled`만 영구 종료
  - Worker panic recovery: fetcher/normalizer goroutine + ingester Run loop에 defer-recover 적용
  - Shutdown timeout: 30s 초과 시 `os.Exit(1)` (stuck goroutine 방지)
  - Preflight connectivity validation: 시작 시 DB + RPC ping (5s timeout)
  - Config validation: `MaxOpenConns` <= 200, `BatchSize` <= 10000, worker count 1-100 clamp
  - Admin dashboard: `/dashboard`에 내장, auth middleware, rate limiting, 3개 JSON API
  - Alerter state-transition awareness: 타입 변경 시 cooldown bypass
  - Reorg detector: `consecutiveRPCErrs` 카운터, 5회 연속 실패 시 alert
  - 6차례 성능 최적화: proto.Clone 제거, Solana 단일파싱, SHA-256 풀, goroutine 누수 수정, DB 쿼리 병합, 할당 제거 등
  - Block-scan batch chunking: `BlockScanMaxBatchTxs` (기본 500)
  - Empty sentinel batch: 빈 블록 범위에서도 워터마크 전진
  - Configurable interleaver: `InterleaveMaxSkewMs` (기본 250, 단일 체인 시 자동 비활성화)
  - Coordinator job dedup: `lastEnqueued` range tracking
  - BTC batch RPC: `GetBlocks`, `prefetchPrevouts`, `prefetchBlocks`
  - EVM: concurrent topic logs (errgroup), partial receipt retry
  - `finality_rank()` PG function: 3곳 hardcoded CASE WHEN 대체
  - Per-address cursor 제거: watermark-only 방식으로 전환
  - PartitionManager 삭제: daily partitioning 제거, flat `balance_events` 테이블
  - Redis 완전 제거: StreamTransport, RedisConfig 삭제, Go channel-only 파이프라인
  - Migration 022번까지 적용 (operational_indexes 포함)
  - Audit 발견 사항 12+5건 해결 (PRD production-hardening 참조)

### M6. Future Work
- 상태: **planned**
- 향후 구현 대상:
  - DLQ (Dead Letter Queue): 처리 실패 이벤트 별도 저장 + 재처리 플로우
  - gRPC mutual TLS: sidecar 통신 상호 인증
  - Horizontal scaling: 다중 인스턴스 워터마크 조율 (leader election 또는 파티셔닝)
  - CDC (Change Data Capture): DB 변경 이벤트 스트리밍 (downstream 시스템 연동)

## Prioritization Rules
- `priority/p0`: 서비스 중단, 데이터 손상 위험
- `priority/p1`: 비즈니스 핵심 플로우 영향
- `priority/p2`: 기능/품질 개선
- `priority/p3`: 장기 최적화

