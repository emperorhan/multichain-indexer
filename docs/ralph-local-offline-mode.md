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
2. ON:
   - `scripts/ralph_local_control.sh on`
3. 루프 실행:
   - `MAX_LOOPS=0 scripts/ralph_local_run.sh`
4. 상태 확인:
   - `scripts/ralph_local_status.sh`
5. OFF:
   - `scripts/ralph_local_control.sh off`

## 모델 라우팅
- Planner: `PLANNING_CODEX_MODEL` (기본 `gpt-5.3-codex`)
- Developer: `AGENT_CODEX_MODEL_FAST`/`AGENT_CODEX_MODEL_COMPLEX`
  - `complexity: high|critical` -> complex 모델
- QA: `QA_TRIAGE_CODEX_MODEL` (기본 `gpt-5.3-codex`)

## 검증/커밋 정책
- role이 `developer`, `qa`면 `RALPH_VALIDATE_CMD`를 실행한다.
- 기본값: `make test && make test-sidecar && make lint`
- 성공 시 이슈 단위 로컬 커밋 생성:
  - `ralph(local): <id> <title>`
- 실패 시 `.ralph/blocked/`로 이동하고 실패 로그 경로를 이슈 하단에 기록한다.

## 운영 팁
- 서버에서 장시간 실행:
  - `nohup MAX_LOOPS=0 scripts/ralph_local_run.sh > .ralph/logs/runner.out 2>&1 &`
- task 분할:
  - Planner 이슈에서 후속 `I-xxxx` 이슈를 `.ralph/issues/`에 직접 추가
- major decision:
  - `.ralph/plans/decision-*.md` 파일로 명시하고 연관 이슈 `depends_on`으로 연결
