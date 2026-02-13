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
  - `needs-opinion`
  - `in-progress`
  - `agent/needs-config`

## Label State Machine
1. 준비: `autonomous + ready`
2. 실행 시작: `in-progress` (에이전트가 claim)
3. 코드 제출: `ready-for-review` (Draft PR 링크 코멘트)
4. 중단/승인대기: `blocked + decision-needed + needs-opinion`

## Discovery Loop (Issue Scout)
- 워크플로우: `.github/workflows/issue-scout.yml`
- 주기: 30분
- 발굴 소스:
  - 최근 실패한 GitHub Actions run
  - 코드의 `TODO|FIXME|HACK|XXX` 마커
- 생성 라벨:
  - `agent/discovered`
  - `autonomous + ready` (즉시 실행 가능한 큐 입력)
- 중복 방지:
  - 이슈 본문의 `[agent-scout:<fingerprint>]` 지문으로 dedup

## Role Collaboration Loops
- Manager loop: `.github/workflows/manager-loop.yml`
  - 화이트리스트 주소 셋(`SOLANA_WHITELIST_CSV` 또는 `configs/solana_whitelist_addresses.txt`)에서 QA용 샘플을 생성
  - 결과 이슈 라벨: `role/manager + qa-ready`
- QA loop: `.github/workflows/qa-loop.yml`
  - `qa-ready` 이슈를 집어 검증 실행(`scripts/qa_executor.sh`)
  - 성공: `qa/passed` 후 이슈 종료
  - 실패: `qa/failed` + 개발자용 버그 이슈(`autonomous + ready`) 자동 생성

## Retry and Escalation
- 실행기(`AGENT_EXEC_CMD`) 실패 시 자동 재시도한다.
- 재시도 한도(`AGENT_MAX_AUTO_RETRIES`)를 초과하면 자동 실행을 멈추고 `decision-needed + needs-opinion`으로 전환한다.
- 이슈에 `risk/high`가 붙으면 코드 변경 전에 즉시 사람 의사결정을 요청한다.

## Human Escalation Rules
아래 조건이면 자동 진행 중지 후 사람 판단 요청:
1. 스키마 파괴적 변경 또는 데이터 손실 위험
2. 인프라 비용/권한/보안 정책 변경
3. 고위험 변경 (`risk/high`)
4. 반복 실패(동일 이슈에서 `AGENT_MAX_AUTO_RETRIES` 초과)

## Required Repository Settings

### 1) Actions Variable
- 이름: `AGENT_EXEC_CMD`
- 의미: 에이전트 구현/테스트 명령
- 예시:
  - `make test && make test-sidecar && make lint`
  - `./scripts/your_agent_executor.sh`

`AGENT_EXEC_CMD`가 비어 있으면 에이전트는 이슈를 `agent/needs-config`로 표시하고 중단한다.

### 1-1) Optional Throughput/Safety Variables
- `AGENT_MAX_ISSUES_PER_RUN` (기본 `2`): 한 번의 루프에서 처리할 최대 이슈 수
- `AGENT_MAX_AUTO_RETRIES` (기본 `2`): 동일 이슈 자동 재시도 횟수
- `DECISION_REMINDER_HOURS` (기본 `24`): `decision-needed` 리마인드 코멘트 간격(시간)
- `AGENT_SCOUT_ENABLED` (기본 `true`): issue scout 활성화
- `SCOUT_MAX_ISSUES_PER_RUN` (기본 `2`): issue scout 1회 실행당 생성할 최대 이슈 수
- `SCOUT_FAIL_WINDOW_HOURS` (기본 `24`): 실패 CI 발굴 시간창
- `SCOUT_TODO_LIMIT` (기본 `20`): TODO/FIXME 스캔 상한
- `AGENT_CODEX_MODEL` (기본 `gpt-5.3-codex-spark`): developer executor 모델
- `AGENT_CODEX_SANDBOX` (기본 `workspace-write`): codex sandbox 모드
- `AGENT_CODEX_APPROVAL` (기본 `never`): codex 승인 정책
- `AGENT_CODEX_SEARCH` (기본 `true`): codex 웹 검색 사용 여부
- `MANAGER_LOOP_ENABLED` (기본 `true`): manager loop 활성화
- `MANAGER_MAX_SETS_PER_RUN` (기본 `1`): manager loop 1회 실행당 QA 셋 생성 수
- `QA_ADDRESS_BATCH_SIZE` (기본 `5`): QA 검증용 주소 수
- `SOLANA_WHITELIST_FILE` (기본 `configs/solana_whitelist_addresses.txt`): whitelist 파일 경로
- `SOLANA_WHITELIST_CSV` (선택): whitelist를 CSV 변수로 직접 주입
- `QA_LOOP_ENABLED` (기본 `true`): qa loop 활성화
- `QA_MAX_ISSUES_PER_RUN` (기본 `1`): qa loop 1회 실행당 처리 이슈 수
- `QA_EXEC_CMD` (기본 `scripts/qa_executor.sh`): QA 실행 명령

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
