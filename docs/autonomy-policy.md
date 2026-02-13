# Autonomy Policy

## Goal
- 사람 개입을 최소화하고, 필요할 때만 의사결정을 요청하는 자율 개발 루프를 운영한다.

## Queue Contract
- 에이전트가 집는 이슈 조건:
  - `autonomous`
  - `ready`
- 에이전트가 자동으로 제외하는 이슈:
  - `blocked`
  - `decision-needed`
  - `in-progress`
  - `agent/needs-config`

## Label State Machine
1. 준비: `autonomous + ready`
2. 실행 시작: `in-progress` (에이전트가 claim)
3. 코드 제출: `ready-for-review` (Draft PR 링크 코멘트)
4. 중단: `blocked` 또는 `decision-needed`

## Human Escalation Rules
아래 조건이면 자동 진행 중지 후 사람 판단 요청:
1. 스키마 파괴적 변경 또는 데이터 손실 위험
2. 인프라 비용/권한/보안 정책 변경
3. 고위험 변경 (`risk/high`)
4. 반복 실패(동일 이슈 3회 이상)

## Required Repository Settings

### 1) Actions Variable
- 이름: `AGENT_EXEC_CMD`
- 의미: 에이전트 구현/테스트 명령
- 예시:
  - `make test && make test-sidecar && make lint`
  - `./scripts/your_agent_executor.sh`

`AGENT_EXEC_CMD`가 비어 있으면 에이전트는 이슈를 `agent/needs-config`로 표시하고 중단한다.

### 2) Runner 선택 (권장)
- 이름: `AGENT_RUNNER`
- 기본값: 비어 있으면 `ubuntu-latest`
- 권장값: `self-hosted`

실제 코드 자동 구현까지 돌리려면 self-hosted runner에서 에이전트 CLI/자격증명을 준비하는 것이 안전하다.

### 3) Scheduled Worker
- 워크플로우: `.github/workflows/agent-loop.yml`
- 주기: 15분
- 수동 실행: `workflow_dispatch`

## Operational Notes
- 기본 실행자는 `ubuntu-latest`이다.
- 진짜 상시 실행(긴 작업/대용량 작업)이 필요하면 self-hosted runner로 전환한다.
- 브랜치 보호를 활성화해 직접 푸시를 금지하고 PR 경로만 허용한다.
