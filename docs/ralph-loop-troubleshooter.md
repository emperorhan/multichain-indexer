# Ralph Loop Troubleshooter

## 목적
- Ralph 로컬 루프를 방해한 실제 장애 요인을 기록하고, 새 인스턴스 이식 시 재발을 막기 위한 기준 절차를 제공합니다.

## 실제 발생 장애 요인

### 1) 샌드박스/세션 환경에서 백그라운드 프로세스 소멸
- 증상:
  - `nohup ... &`로 시작했지만 프로세스가 즉시 사라짐
  - `runner.pid`만 남고 실제 PID 없음
- 원인:
  - 실행 컨텍스트 종료 시 자식 프로세스 정리되는 환경
- 대응:
  - 장기 실행은 반드시 `systemd --user` 서비스(`ralph-local.service`) 사용

### 2) 서비스 PATH 불일치로 커맨드 누락 (`missing command: rg`)
- 증상:
  - `journalctl --user -u ralph-local.service`에 `missing command: rg`
  - supervisor가 `runner crashed rc=1` 백오프 반복
- 원인:
  - systemd entry의 고정 PATH가 실제 바이너리 위치와 불일치
- 대응:
  - `scripts/ralph_local_systemd_entry.sh`를 동적 PATH 탐지 방식으로 변경
  - `rg`에 의존하던 런타임 코드를 `awk/grep` 기반 fallback으로 수정

### 3) Codex 연결 불안정(네트워크 transient)
- 증상:
  - `stream disconnected before completion`
  - `error sending request for url`
  - 이슈 재큐잉 + 지수 백오프
- 원인:
  - 외부 네트워크/세션 품질 불안정
- 대응:
  - transient 재시도/백오프 유지
  - `scripts/ralph_local_preflight.sh`의 connectivity smoke로 사전 점검

### 4) 서비스 중지 타임아웃/강제 킬
- 증상:
  - `State 'final-sigterm' timed out. Killing.`
  - `Failed with result 'timeout'`
- 원인:
  - runner 하위 프로세스 정리 지연
- 대응:
  - 서비스 유닛에 `TimeoutStopSec`, `KillMode` 명시
  - 실행/중지는 `systemctl --user`로 일관 처리

### 5) 큐 상태 잔재(파일만 남는 상태)
- 증상:
  - `warning: missing in-progress file ...`
  - 이슈가 `in-progress`/`issues` 사이에서 혼선
- 원인:
  - 중간 강제종료, 수동 조작, 이전 비원자 흐름
- 대응:
  - 재기동 전 `lrcheck`, `lrdoctor`로 큐 상태 점검
  - 필요 시 `in-progress`를 `issues`로 명시적으로 복구

## 신규 인스턴스 이식 표준 절차

1. 코드/의존 도구 배치
- 필수: `bash git codex make awk flock jq systemctl journalctl node npm`

2. 서비스 설치
- `scripts/install_ralph_local_service.sh`
- `systemctl --user daemon-reload`
- `systemctl --user enable ralph-local.service`

3. 쉘 alias 설치
- `scripts/install_ralph_aliases.sh ~/.bashrc`
- `source ~/.bashrc`

4. 사전점검
- `lrpreflight`
- 실패 항목이 있으면 먼저 해결 후 진행

5. 초기화/실행
- `lrinit`
- `lrstart`
- `lrstatus`
- `lrcheck`

6. 관찰
- `lrtail`
- `lrdoctor`

## 빠른 진단 명령
- 서비스 상태: `lrstatus`
- 런타임 상태: `lrcheck`
- 로그 tail: `lrtail`
- 진단 리포트: `lrdoctor`
- 사전점검: `lrpreflight`
- 계약 검증: `lrcontract .ralph/issues/<issue>.md`
- 플랜 스키마 검증: `lrplancheck <json>`

## 패키징 권장 산출물
- 서비스 설치 스크립트: `scripts/install_ralph_local_service.sh`
- 환경 사전점검 스크립트: `scripts/ralph_local_preflight.sh`
- 운영 진단 스크립트: `scripts/ralph_local_doctor.sh`
- 사용자 가이드: `docs/ralph-loop-user-guide.md`
- 본 트러블슈터: `docs/ralph-loop-troubleshooter.md`
