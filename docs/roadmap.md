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

## Prioritization Rules
- `priority/p0`: 서비스 중단, 데이터 손상 위험
- `priority/p1`: 비즈니스 핵심 플로우 영향
- `priority/p2`: 기능/품질 개선
- `priority/p3`: 장기 최적화

