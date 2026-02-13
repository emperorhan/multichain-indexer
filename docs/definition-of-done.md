# Definition Of Done

## Engineering DoD

1. 기능 요구사항과 Acceptance Criteria를 모두 충족한다.
2. 변경 동작을 검증하는 테스트가 추가/갱신된다.
3. 다음 검증을 모두 통과한다.
   - `make test`
   - `make test-sidecar`
   - `make lint`
   - `go build ./cmd/indexer`
   - `cd sidecar && npm run build`
4. 운영 영향(성능, 장애 대응, 롤백 전략)을 PR에 명시한다.
5. 동작 변경이 있으면 `README.md` 또는 `docs/*`를 업데이트한다.
6. 마이그레이션 변경 시 up/down 경로와 데이터 영향 분석을 포함한다.
7. CI 필수 체크가 모두 초록 상태여야 한다.
8. CODEOWNER 리뷰 또는 승인 규칙을 충족한다.

## Release DoD

1. 배포 전후 검증 지표가 정의되어 있다.
2. 장애 시 복구 절차가 `docs/runbook.md`에 반영되어 있다.
3. 미해결 리스크가 있으면 이슈로 등록되고 우선순위가 지정된다.

