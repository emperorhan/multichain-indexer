# Sidecar Deployment Architecture Decision

- Decision ID: `ADR-2026-02-16-sidecar-deployment-unit`
- Date: `2026-02-16`
- Status: `accepted`
- Scope: `multichain-indexer` sidecar deployment, protobuf contract, on-call operations

## 1. Context

현재 sidecar는 체인별 디코더 구현이 분리되어 있지만, 배포 단위는 단일 서비스입니다.

- implementation 분리:
  - `sidecar/src/decoder/solana/*`
  - `sidecar/src/decoder/base/*`
  - `sidecar/src/decoder/btc/*`
- runtime 경계:
  - Go indexer는 `solana-devnet`, `base-sepolia`, `btc-testnet` 타깃을 동시에 실행할 수 있음
- API naming debt:
  - protobuf/gRPC 이름이 `DecodeSolanaTransactionBatch`로 고정되어 의미가 실제와 어긋남

문제는 다음 두 가지를 동시에 만족해야 한다는 점입니다.

1. 운영 복잡도(배포/롤백/온콜/호환성)를 과도하게 늘리지 않아야 함
2. 체인별 장애가 전체 디코딩 품질에 전이되지 않도록 해야 함

## 2. Decision

기본 전략은 `단일 배포 단위 유지 + 분리 가능한 구조로 운영`이다.

1. Sidecar는 기본적으로 환경별 `하나의 배포 단위`로 유지한다.
2. 코드 경계는 체인별 모듈을 유지하고, 체인별 리소스/타임아웃/동시성 제한을 독립 구성으로 관리한다.
3. protobuf는 체인-중립 단일 인터페이스로 정리한다.
4. 운영 지표가 임계값을 넘으면 단계적으로 분리 배포한다.

즉시 완전 분리는 하지 않고, 명시된 트리거를 만족할 때만 분리한다.

## 3. Protobuf Contract Rule

목표는 `하나의 인터페이스`로 모든 체인을 처리하는 것이다.

1. 표준 RPC 이름: `DecodeTransactionBatch` (chain-agnostic)
2. 요청 공통 필드:
   - `chain`
   - `network`
   - `transactions`
   - `watched_addresses`
3. 응답 공통 필드:
   - `results`
   - `errors`
   - `metadata` (체인 특화 확장용)
4. 마이그레이션 원칙:
   - 신규 RPC 추가
   - 클라이언트 전환
   - 기존 `DecodeSolanaTransactionBatch` deprecate 후 제거

## 4. SLO (Sidecar)

SLO는 체인별로 측정하며, 단일 배포에서도 chain label로 분리 집계한다.

| SLI | Target | Window | Notes |
|---|---|---|---|
| gRPC availability (`healthCheck`, decode endpoint success) | `>= 99.95%` | 30d | infra/network 제외 정책은 runbook에 따름 |
| Valid payload decode success ratio | `>= 99.90%` | 30d | malformed payload는 분모 제외 |
| Decode latency p95 | `<= 300ms` | 7d | chain별 집계 (`solana`, `base`, `btc`) |
| Decode latency p99 | `<= 800ms` | 7d | 배치 크기 표준값 기준 |
| Cross-chain blast radius | `0` | incident basis | 한 체인 장애가 타 체인 error budget에 영향을 주면 위반 |

## 5. Split Triggers

아래 트리거는 분리 배포 의사결정의 기준이다.

### 5.1 Stage-1 Trigger (같은 이미지, 체인별 별도 배포)

다음 조건 중 `2개 이상`이 `연속 2주` 발생하면 Stage-1 분리를 실행한다.

1. 특정 체인 피크 부하 시 타 체인 p95 latency가 기준 대비 `30%+` 악화
2. 특정 체인 장애가 타 체인 decode error budget을 `10%+` 소모
3. 체인별 release cadence 충돌로 hotfix lead time이 `2배+` 증가
4. 체인별 라이브러리/런타임 보안패치 일정 충돌이 월 `2회+` 발생
5. 온콜에서 체인 분리 재시작 필요 케이스가 월 `3회+` 발생

### 5.2 Stage-2 Trigger (서비스 완전 분리)

다음 조건 중 `1개 이상`이 `연속 4주` 발생하면 Stage-2를 계획한다.

1. Stage-1 이후에도 cross-chain blast radius 위반이 `2회+` 재발
2. 체인별 독립 스케일 요구로 비용/지연 최적화 차이가 `40%+` 발생
3. 보안/컴플라이언스 요구로 체인별 독립 배포 경계가 필수화

## 6. Operating Rules

### 6.1 Deploy and Rollback

1. 기본은 단일 sidecar blue/green 또는 canary 배포
2. protobuf 변경은 backward compatible 우선
3. RPC 추가/전환 시 indexer-sidecar 버전 매트릭스를 release note에 명시
4. rollback은 sidecar 먼저, 그다음 indexer 순서로 수행

### 6.2 Incident Rules

1. 체인 특화 장애는 먼저 chain label로 분리 진단
2. 단일 배포에서 장애 전파가 관찰되면 chain별 동시성 제한을 즉시 낮춤
3. SLO burn rate가 임계값 초과 시 Stage-1 trigger 평가를 즉시 시작
4. `decision-needed` 이슈에 trigger evidence와 24h 액션 계획을 기록

### 6.3 Change Freeze Rules

1. protobuf 계약 변경 주간에는 loop 자동 병합을 비활성화
2. major sidecar 구조 변경 시 필수 검증:
   - `make test`
   - `make test-sidecar`
   - `make lint`
3. 룰 위반 상태에서는 `ready` 승격 금지

### 6.4 Ownership and Review Cadence

1. Owner: `area/sidecar` + `area/pipeline` 공동
2. SLO/trigger 리뷰 주기: 매주
3. 본 ADR 재검토 주기: 월 1회 또는 sev1+ 사고 직후

## 7. Consequences

### 7.1 Benefits

1. 초기/중간 규모에서 운영 복잡도와 배포 부담을 최소화
2. 코드 구조를 유지한 채 필요 시 분리 배포로 확장 가능
3. protobuf 의미 정합성을 확보해 체인 추가 비용을 낮춤

### 7.2 Costs

1. 단일 배포 기간에는 잠재적인 장애 전파 위험이 남음
2. 체인별 독립 스케일링 최적화는 Stage-1 이전에는 제한적

## 8. Execution Checklist

1. protobuf chain-neutral RPC 추가 이슈 생성
2. indexer client 전환 이슈 생성
3. 기존 Solana-named RPC deprecate 일정 수립
4. SLO dashboard에 chain label 분리 지표 추가
5. Stage-1/2 trigger 자동 집계 쿼리(runbook 링크 포함) 추가
