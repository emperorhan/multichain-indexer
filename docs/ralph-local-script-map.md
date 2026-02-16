# Ralph Local Script Map

로컬 Ralph 루프에서 필요한 스크립트만 빠르게 찾기 위한 맵입니다.

## A. 코어 (항상 사용)
- `scripts/ralph_local_init.sh`: `.ralph` 로컬 큐/상태 초기화
- `scripts/ralph_local_daemon.sh`: 시작/중지/상태/로그 tail
- `scripts/ralph_local_run.sh`: 루프 단일/반복 실행 엔트리
- `scripts/ralph_local_new_issue.sh`: 로컬 이슈 파일 생성
- `scripts/codex_auth_status.sh`: ChatGPT 인증 모드 확인

## B. 운영 점검 (문제 있을 때)
- `scripts/ralph_local_runtime_status.sh`: 큐/서비스 런타임 스냅샷
- `scripts/ralph_local_preflight.sh`: 실행 전 환경 점검
- `scripts/ralph_local_doctor.sh`: 장애 진단 리포트
- `scripts/ralph_local_agent_tracker.sh`: 에이전트별 처리 추적
- `scripts/ralph_context_compact.sh`: 긴 `context.md`/`state.learning.md`를 압축하고 원문을 `.ralph/archive/`에 보관

## C. 정책/검증 게이트
- `scripts/ralph_issue_contract.sh`: 이슈 계약 검증
- `scripts/ralph_invariants.sh`: 불변식 목록/검증
- `scripts/validate_planning_output.sh`: planner 출력 JSON 검증

## D. 선택 기능 (필요할 때만)
- `scripts/ralphctl.sh`: 로컬 운영 단축 제어 래퍼(`on|off|status|kick|scout|profile`)
- `scripts/ralph_local_profile.sh`: 절약/최적/퍼포먼스 프로필 전환 및 상태 조회
- `scripts/ralph_local_manager_autofill.sh`: ready 이슈가 없을 때 자동 이슈 생성
- `scripts/install_ralph_local_service.sh`: user systemd 서비스 설치
- `scripts/install_ralph_aliases.sh`: `lr*`/`r*` alias 설치
- `scripts/enable_direct_main_push.sh`: trusted 환경에서 direct main push 허용 설정

## E. 로컬 루프 비핵심 (기본적으로 무시)
아래는 GitHub 기반 루프/원격 운영 용도라 로컬 오프라인 운영의 필수 경로가 아닙니다.

- `scripts/agent_loop.sh`
- `scripts/qa_loop.sh`
- `scripts/planning_executor.sh`
- `scripts/qa_executor.sh`
- `scripts/qa_triage_executor.sh`
- `scripts/planner_fanout.sh`
- `scripts/toggle_ralph_loop.sh`
- `scripts/setup_agent_loop.sh`
- `scripts/setup_branch_protection.sh`
- `scripts/issue_scout.sh`
