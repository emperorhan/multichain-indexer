# Ralph Local Offline Mode

## 목적
- GitHub Actions/Issue 없이 `.ralph` 로컬 큐만으로 Ralph 루프를 운영한다.
- 이 문서는 로컬 운영에 필요한 내용만 다룬다.

## 비범위
- 아래 항목은 이 문서 범위에서 제외한다.
  - `scripts/agent_loop.sh`, `scripts/qa_loop.sh`, `scripts/toggle_ralph_loop.sh` 기반 원격 GitHub 루프
  - 원격 runner/auto-merge/workflow_dispatch 운영

## 로컬 큐 구조
- `.ralph/issues/`: 대기
- `.ralph/in-progress/`: 진행 중
- `.ralph/done/`: 완료
- `.ralph/blocked/`: 차단
- `.ralph/reports/`: evidence/리포트
- `.ralph/logs/`: 실행 로그

## 최소 실행 절차
1. 초기화
   - `scripts/ralph_local_init.sh`
2. 인증 확인
   - `scripts/codex_auth_status.sh --require-chatgpt`
3. 시작
   - `scripts/ralph_local_daemon.sh start`
4. 이슈 추가
   - `scripts/ralph_local_new_issue.sh developer "<title>"`
5. 상태/로그 확인
   - `scripts/ralph_local_daemon.sh status`
   - `scripts/ralph_local_daemon.sh tail`
6. 중지
   - `scripts/ralph_local_daemon.sh stop`

## 실행 모드
- 기본(Safe): `RALPH_LOCAL_TRUST_MODE=false`
  - sandbox: `workspace-write`
- 선택(Trust): `RALPH_LOCAL_TRUST_MODE=true`
  - 시작 예시: `RALPH_LOCAL_TRUST_MODE=true scripts/ralph_local_daemon.sh start`

## 로컬에서 자주 쓰는 스크립트
- 필수
  - `scripts/ralph_local_init.sh`
  - `scripts/ralph_local_daemon.sh`
  - `scripts/ralph_local_new_issue.sh`
  - `scripts/codex_auth_status.sh`
- 점검/진단
  - `scripts/ralph_local_runtime_status.sh`
  - `scripts/ralph_local_preflight.sh`
  - `scripts/ralph_local_doctor.sh`
- 계약/게이트
  - `scripts/ralph_issue_contract.sh`
  - `scripts/ralph_invariants.sh`
  - `scripts/validate_planning_output.sh`

## 이슈 작성 원칙
- 권장: `scripts/ralph_local_new_issue.sh`로 생성
- 필수 검증 게이트
  - 계약 검증 통과
  - 스코프 가드(`allowed_paths`, `denied_paths`, `max_diff_scope`) 준수
  - 검증 명령 통과: `make test`, `make test-sidecar`, `make lint`

## 문제 발생 시
1. 서비스 상태: `scripts/ralph_local_daemon.sh status`
2. 런타임 스냅샷: `scripts/ralph_local_runtime_status.sh`
3. 환경 진단: `scripts/ralph_local_preflight.sh`
4. 종합 진단: `scripts/ralph_local_doctor.sh`

## 관련 문서
- `docs/ralph-local-script-map.md`
- `docs/ralph-loop-user-guide.md`
- `docs/ralph-loop-troubleshooter.md`
