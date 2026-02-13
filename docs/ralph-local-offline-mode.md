# Ralph Local Offline Mode

## 목적
- GitHub API/Actions 의존 없이, 로컬 markdown + commit 기반으로 Ralph loop를 지속 실행한다.
- 멀티 에이전트 역할(Planner/Developer/QA)을 로컬 큐로 오케스트레이션한다.

## 디렉터리 구조
- `.ralph/issues/`: 실행 대기 이슈(md)
- `.ralph/in-progress/`: 현재 처리 중
- `.ralph/done/`: 완료 이슈
- `.ralph/blocked/`: 실패/결정 필요 이슈
- `.ralph/reports/`: QA 리포트
- `.ralph/logs/`: 에이전트 실행 로그
- `.ralph/context.md`: 공통 컨텍스트
- `.ralph/state.env`: ON/OFF 상태

## 이슈 포맷
`scripts/ralph_local_new_issue.sh`가 아래 헤더를 생성한다.

```md
id: I-0001
role: planner
status: ready
priority: p0
depends_on:
title: M1 planning
complexity: high
---
## Objective
- ...
```

## 실행 순서
1. 초기화:
   - `scripts/ralph_local_init.sh`
2. 백그라운드 시작:
   - `scripts/ralph_local_daemon.sh start`
   - 기본값: `RALPH_LOCAL_TRUST_MODE=true` (approval 대기 없음, `danger-full-access`)
   - 보수 모드: `RALPH_LOCAL_TRUST_MODE=false scripts/ralph_local_daemon.sh start`
   - 인증 점검: `scripts/codex_auth_status.sh` (별칭: `lrauth`)
3. 루프 실행(내부):
   - `MAX_LOOPS=0 scripts/ralph_local_run.sh` (`daemon.sh start`가 내부에서 실행)
4. 상태 확인:
   - `scripts/ralph_local_daemon.sh status`
5. OFF:
   - `scripts/ralph_local_daemon.sh stop`

## 모델 라우팅
- Planner: `PLANNING_CODEX_MODEL` (기본 `gpt-5.3-codex`)
- Developer: `AGENT_CODEX_MODEL_FAST`/`AGENT_CODEX_MODEL_COMPLEX`
  - `complexity: high|critical` -> complex 모델
- QA: `QA_TRIAGE_CODEX_MODEL` (기본 `gpt-5.3-codex`)

## 권한 모드
- Trust mode(`RALPH_LOCAL_TRUST_MODE=true`, 기본):
  - `AGENT_CODEX_APPROVAL=never`
  - `AGENT_CODEX_SANDBOX=danger-full-access`
  - `OMX_SAFE_MODE=false`로 safety guard sandbox 차단 해제
- Safe mode(`RALPH_LOCAL_TRUST_MODE=false`):
  - 기존 sandbox/guard 정책 유지

## 인증 모드 (Pro 사용량 기반)
- 로컬 Ralph는 기본적으로 `ChatGPT 로그인` 모드만 허용한다.
  - 강제 변수: `RALPH_REQUIRE_CHATGPT_AUTH=true` (기본)
  - 시작 전 점검: `scripts/codex_auth_status.sh --require-chatgpt`
- 데몬 시작 시 `OPENAI_API_KEY` 등 API 키 관련 환경변수는 자동으로 제거되어 자식 프로세스에 전달되지 않는다.
- `codex_auth_status.sh` 출력이 아래와 같아야 정상:
  - `codex_auth_mode=chatgpt`
  - `openai_api_key_env=unset`

## 자가 복구 동작
- 모델 네트워크 오류(예: stream disconnect, rate-limit, timeout)는 기본적으로 `blocked` 처리하지 않고 자동 재큐잉한다.
- 제어 변수:
  - `RALPH_TRANSIENT_REQUEUE_ENABLED` (기본 `true`)
  - `RALPH_TRANSIENT_RETRY_SLEEP_SEC` (기본 `20`)

## 자동 main 반영
- 로컬 루프는 커밋 누적이 임계치를 넘으면 `main` 반영을 자동 시도한다.
- 제어 변수:
  - `RALPH_AUTO_PUBLISH_ENABLED` (기본 `true`)
  - `RALPH_AUTO_PUBLISH_MIN_COMMITS` (기본 `3`)
  - `RALPH_AUTO_PUBLISH_TARGET_BRANCH` (기본 `main`)
  - `RALPH_AUTO_PUBLISH_REMOTE` (기본 `origin`)
  - `RALPH_BRANCH_STRATEGY` (기본 `main`; `main|current|feature`)
- 동작:
  - 작업 브랜치 커밋이 임계치 이상 누적되면 `main`으로 merge 후 push 시도
  - 네트워크/권한/충돌 실패 시 루프는 중단되지 않고 다음 사이클에서 재시도

### Direct Main Push
- 브랜치 보호가 direct push를 막으면 자동 publish는 계속 실패한다.
- trusted solo 환경에서만 아래를 사용:
  - `scripts/enable_direct_main_push.sh <owner/repo> main`

## 검증/커밋 정책
- role이 `developer`, `qa`면 `RALPH_VALIDATE_CMD`를 실행한다.
- 기본값: `make test && make test-sidecar && make lint`
- 성공 시 이슈 단위 로컬 커밋 생성:
  - `ralph(local): <id> <title>`
- 실패 시 `.ralph/blocked/`로 이동하고 실패 로그 경로를 이슈 하단에 기록한다.

## 운영 팁
- 서버에서 장시간 실행:
  - 권장: `scripts/ralph_local_daemon.sh start`
  - 로그 tail: `scripts/ralph_local_daemon.sh tail`
- task 분할:
  - Planner 이슈에서 후속 `I-xxxx` 이슈를 `.ralph/issues/`에 직접 추가
- major decision:
  - `.ralph/plans/decision-*.md` 파일로 명시하고 연관 이슈 `depends_on`으로 연결
