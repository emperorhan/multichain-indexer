# Ralph Loop Usage Guide

## 목적
- 이 문서는 운영자가 직접 Ralph 로컬 루프를 실행/중지/점검할 때 필요한 실무 절차를 제공합니다.
- 대상 범위는 로컬 오프라인 루프(`.ralph` 기반)입니다.

## 권장 실행 방식
- 기본은 `ralph-local.service`(user systemd)로 실행합니다.
- 이유:
  - 터미널 세션이 종료되어도 루프가 유지됩니다.
  - `systemctl --user`와 `journalctl --user`로 상태/로그 추적이 쉽습니다.

## 사전 준비
1. 저장소 루트로 이동합니다.
2. alias 설치/반영:
   - `scripts/install_ralph_aliases.sh ~/.bashrc`
   - `source ~/.bashrc`
3. 서비스 설치(신규 인스턴스):
   - `lrsvcinstall`
   - `systemctl --user daemon-reload`
4. 사전점검:
   - `lrpreflight`
5. 인증 확인:
   - `lrauth`
6. 로컬 큐 초기화:
   - `lrinit`

## 빠른 시작
1. 상태 확인:
   - `lrcheck`
2. 루프 시작:
   - `lrstart`
3. 실행 상태 확인:
   - `lrstatus`
   - `lrcheck`
4. 실시간 로그 확인:
   - `lrtail`

## 중지/재시작
- 중지: `lrstop`
- 재시작: `lrrestart`
- 상태: `lrstatus`

## 핵심 alias
- 서비스 제어:
  - `lrstart`, `lrstop`, `lrrestart`, `lrstatus`, `lrtail`
- 런타임 상태:
  - `lrcheck` (`lrwhat`)
- 계약/불변식 점검:
  - `lrinvariants`
  - `lrcontract <issue-file>`
  - `lrplancheck <planning-output-json>`
- 이식/사전점검:
  - `lrsvcinstall`
  - `lrpreflight`
- 데몬 스크립트 직접 제어(대체 경로):
  - `lrdaemonstart`, `lrdaemonstop`, `lrdaemonstatus`, `lrdaemontail`

## 이슈 작성 규칙
- 루프 입력 큐: `.ralph/issues/*.md`
- 권장 생성 명령:
  - `lrnew <role> "<title>" [depends_on] [priority] [complexity] [risk_class] [max_diff_scope] [invariants]`
- `role` 허용값:
  - `planner`, `developer`, `qa`

### 필수 계약 헤더
- `risk_class`: `low|medium|high|critical`
- `max_diff_scope`: 양의 정수
- `allowed_paths`, `denied_paths`
- `acceptance_tests`
- `invariants`
- `evidence_required`

### 필수 섹션
- `## Objective`
- `## In Scope`
- `## Out of Scope`
- `## Acceptance Criteria`
- `## Non Goals`

## 상태 파일/디렉터리 해석
- `.ralph/issues/`: 대기
- `.ralph/in-progress/`: 현재 처리 중
- `.ralph/done/`: 완료
- `.ralph/blocked/`: 차단됨
- `.ralph/reports/`: QA 리포트 및 evidence pack
- `.ralph/logs/`: 실행 로그

## 완료 기준(자동 게이트)
- 계약 검증 통과(`scripts/ralph_issue_contract.sh`)
- 스코프 가드 통과(`allowed_paths`, `denied_paths`, `max_diff_scope`)
- 검증 명령 통과(`make test`, `make test-sidecar`, `make lint`)
- Evidence Pack 생성(`.ralph/reports/<issue>-evidence.md`)
- planner 이슈는 계획 JSON 스키마 검증 추가 통과

## 자주 막히는 케이스
- 서비스가 `failed` 상태:
  - `lrstatus`
  - `journalctl --user -u ralph-local.service -n 80 --no-pager`
  - `lrrestart`
- 계약 게이트 실패:
  - `lrcontract .ralph/issues/<issue>.md`
  - 누락 헤더/섹션 보완
- planner 출력 스키마 실패:
  - `lrplancheck .ralph/plans/plan-output-<id>.json`
- 스코프 가드 실패:
  - 이슈의 `allowed_paths`, `denied_paths`, `max_diff_scope` 조정 또는 변경 범위 축소

## 권장 운영 루틴
1. `lrcheck`로 큐/현재 작업 확인
2. 필요한 이슈 추가(`lrnew ...`)
3. `lrstart`로 실행
4. `lrtail`로 관찰
5. 완료 후 `lrcheck`로 결과 확인
6. 필요 시 `lrstop`로 종료

## 참고 문서
- `docs/ralph-local-offline-mode.md`
- `docs/autonomy-policy.md`
