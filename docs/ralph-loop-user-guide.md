# Ralph Loop Usage Guide

## 목적
- 운영자가 로컬 Ralph 루프를 시작/점검/중지할 때 필요한 실행 커맨드만 제공한다.

## 1분 시작
1. `lrinit`
2. `lrauth`
3. `lrstart`
4. `lrcheck`
5. `lrtail`

`lr*` alias가 없다면 `scripts/install_ralph_aliases.sh ~/.bashrc` 후 `source ~/.bashrc`를 실행한다.

## 기본 운영 명령
- 시작/중지/재시작: `lrstart`, `lrstop`, `lrrestart`
- 상태: `lrstatus`, `lrcheck`
- 로그: `lrtail`
- 이슈 추가: `lrnew <role> "<title>"`

alias 없이 실행할 때:
- `scripts/ralph_local_daemon.sh start|stop|status|tail`
- `scripts/ralph_local_runtime_status.sh`
- `scripts/ralph_local_new_issue.sh <role> "<title>"`

## 점검 명령
- 사전점검: `lrpreflight`
- 종합진단: `lrdoctor`
- 계약검증: `lrcontract .ralph/issues/<issue>.md`
- 플랜검증: `lrplancheck .ralph/plans/plan-output-<id>.json`

## 운영 순서 권장
1. `lrcheck`로 큐 상태 확인
2. 필요 시 `lrnew`로 이슈 추가
3. `lrstart`로 실행
4. `lrtail`로 관찰
5. `lrcheck`로 완료/차단 결과 확인

## 관련 문서
- 스크립트 구분: `docs/ralph-local-script-map.md`
- 오프라인 운영 정책: `docs/ralph-local-offline-mode.md`
- 장애 복구: `docs/ralph-loop-troubleshooter.md`
