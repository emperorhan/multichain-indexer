# Runbook

## Scope
- 대상: `multichain-indexer` Go 파이프라인 + `sidecar` 디코더
- 목적: 장애 인지부터 복구, 사후 분석까지 일관된 대응

## Severity Levels
- `sev0`: 서비스 중단 또는 데이터 손상/유실
- `sev1`: 핵심 기능 장애, 심각한 지연
- `sev2`: 부분 기능 저하
- `sev3`: 경미한 문제

## First Response Checklist
1. 장애 티켓 생성 (`type/bug`, `sev*`, `priority/*`)
2. 영향 범위 확인:
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
   - 필수 타겟: `solana-devnet`, `base-sepolia`
   - 시작 시 `mandatory chain runtime wiring parity check failed` 에러가 발생하면 체인/네트워크 매핑과 adapter 연결 상태를 먼저 수정
   - 중복 target, nil adapter, adapter chain mismatch는 즉시 부팅 실패(의도된 fail-fast)

### Sidecar
1. gRPC health check 상태 확인
2. 플러그인 parse 예외 로그 확인
3. Sidecar test/build 재검증

### Database
1. 최근 `balance_events`, `transactions` 적재 추이 확인
2. 커서 전진 여부 확인 (`address_cursors`, `pipeline_watermarks`)
3. 최근 migration 적용 상태 확인

## Recovery Playbooks

### A. RPC Degradation
1. RPC endpoint 장애 여부 확인
2. endpoint failover 또는 요청 간격 완화
3. fetch workers와 batch size 임시 조정

### B. Sidecar Decode Failures
1. 실패 트랜잭션 샘플 추출
2. 플러그인 로직 회귀 여부 확인
3. hotfix PR 생성 후 CI 통과 확인, 단계적 배포

### C. DB Write Failures
1. DB 연결/락/트랜잭션 오류 구분
2. 일시 장애면 재시작 후 커서 기반 재처리
3. 지속 장애면 ingest 중단 후 원인 제거

## Rollback Policy
1. 배포 단위를 PR 단위로 롤백
2. 스키마 변경 포함 시 down migration 영향 검토 후 실행
3. 롤백 후 무결성 검증:
   - 중복 이벤트 발생 여부
   - 커서 역행 여부

## Postmortem
1. 24시간 내 회고 문서 작성
2. 재발 방지 액션을 Issue로 생성
3. 런북/테스트/알람 규칙 업데이트
