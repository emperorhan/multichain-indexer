# Autonomy Policy

## Goal
- 사람 개입을 최소화하고, 필요할 때만 의사결정을 요청하는 자율 개발 루프를 운영한다.
- GitHub API가 불안정할 때는 로컬 오프라인 모드(`scripts/ralph_local_*`)로 동일한 목적을 유지한다.

## Ralph Loop Switch
- 전역 토글 변수: `RALPH_LOOP_ENABLED`
  - `true`: 모든 자율 루프 활성화
  - `false`: `agent-loop`, `issue-scout`, `manager-loop`, `qa-loop` 모두 정지
- 연속 실행 변수: `RALPH_AUTOPILOT_ENABLED`
  - `true`: Agent Loop 완료 후 큐가 남아 있으면 다음 run 자동 시작
  - `false`: 스케줄/수동 kick만 동작
- 운영 토글 수단:
  - workflow: `.github/workflows/ralph-loop-control.yml`
  - CLI: `scripts/toggle_ralph_loop.sh on|off|status`
  - short alias: `scripts/install_ralph_aliases.sh` 후 `ron|roff|rstat|rkick|rscout`
  - mobile: GitHub App > Actions > `Ralph Loop Control` (on/off/status + optional kick)
  - quick view: Actions > `Ralph Status Board` 또는 이슈 `[Ops] Ralph Loop Status Board`

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
- 정리 정책:
  - `agent-loop`가 실행될 때 `ready-for-review` 이슈 중 merge 완료 PR이 연결된 항목은 자동 close
  - `agent/discovered` 중복 이슈(동일 제목)는 활성 항목 1건을 남기고 나머지를 deprecated duplicate로 자동 close

## Role Collaboration Loops
- Planner flow:
  - planner 이슈(`role/planner + autonomous + ready`)는 `scripts/planning_executor.sh`로 처리
  - 산출물: `IMPLEMENTATION_PLAN.md`, `specs/*`
  - 큰 범위 작업이면 fanout 파일(`.agent/planner-fanout-<issue>.json`)을 통해 child issue 자동 생성
- Manager loop: `.github/workflows/manager-loop.yml`
  - 멀티체인 화이트리스트 주소 셋(`QA_CHAIN_TARGETS`)에서 QA용 샘플을 생성
  - 기본 타겟: `solana-devnet`, `base-sepolia`
  - 결과 이슈 라벨: `role/manager + qa-ready`
- QA loop: `.github/workflows/qa-loop.yml`
  - `qa-ready` 이슈를 집어 검증 실행(`scripts/qa_executor.sh`)
  - 성공: `qa/passed` 후 이슈 종료
  - 실패: `qa/failed` + model triage + 개발자용 버그 이슈(`autonomous + ready`) 자동 생성

## Model Allocation
- Planner agent:
  - `PLANNING_CODEX_MODEL` (`gpt-5.3-codex`)
- Manager agent: deterministic rules 기반(모델 비의존)으로 whitelist 셋 생성
- Developer agent:
  - 기본: `AGENT_CODEX_MODEL_FAST` (`gpt-5.3-codex-spark`)
  - 복잡 라우팅: `scripts/codex_model_router.sh`가 라벨 기반으로 `AGENT_CODEX_MODEL_COMPLEX`를 선택
  - 기본 복잡 라벨: `risk/high, priority/p0, sev0, sev1, decision/major, area/storage, type/bug`
- QA agent:
  - 검증 실행: deterministic 테스트 러너
  - 실패 triage 요약: `QA_TRIAGE_CODEX_MODEL` (`gpt-5.3-codex`)

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
5. `decision/major` 라벨이 붙은 주요 의사결정

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
- `AGENT_MAX_PLANNER_ISSUES_PER_RUN` (기본 `1`): planner queue 1회 실행당 처리 이슈 수
- `AGENT_MAX_AUTO_RETRIES` (기본 `2`): 동일 이슈 자동 재시도 횟수
- `AGENT_IN_PROGRESS_TIMEOUT_HOURS` (기본 `6`): `in-progress` 정체 이슈 자동 복구 임계시간
- `AGENT_AUTO_CLEANUP_ENABLED` (기본 `true`): 해결/폐기 이슈 자동 정리 단계 활성화
- `AGENT_DEPRECATE_DUPLICATES_ENABLED` (기본 `true`): `agent/discovered` 중복 이슈 자동 정리 활성화
- `RALPH_SELF_HEAL_ENABLED` (기본 `true`): 권한/운영 오류 자동 복구 시도 활성화
- `DECISION_REMINDER_HOURS` (기본 `24`): `decision-needed` 리마인드 코멘트 간격(시간)
- `AGENT_DEVELOPER_INCLUDE_LABELS` (기본 비어 있음): developer queue 포함 필터(CSV, 모든 라벨 일치)
- `AGENT_DEVELOPER_EXCLUDE_LABELS` (기본 `role/planner,role/qa,decision/major`): developer queue 제외 필터(CSV)
- `AGENT_SCOUT_ENABLED` (기본 `true`): issue scout 활성화
- `SCOUT_MAX_ISSUES_PER_RUN` (기본 `2`): issue scout 1회 실행당 생성할 최대 이슈 수
- `SCOUT_FAIL_WINDOW_HOURS` (기본 `24`): 실패 CI 발굴 시간창
- `SCOUT_TODO_LIMIT` (기본 `20`): TODO/FIXME 스캔 상한
- `AGENT_CODEX_MODEL` (기본 `gpt-5.3-codex-spark`): developer executor 모델
- `AGENT_CODEX_MODEL` (기본 빈값): developer 모델 강제 오버라이드(설정 시 fast/complex 분기 무시)
- `AGENT_CODEX_MODEL_FAST` (기본 `gpt-5.3-codex-spark`): 일반 개발 이슈 모델
- `AGENT_CODEX_MODEL_COMPLEX` (기본 `gpt-5.3-codex`): 고위험/고우선 개발 이슈 모델
- `AGENT_CODEX_COMPLEX_LABELS` (기본 `risk/high,priority/p0,sev0,sev1,decision/major,area/storage,type/bug`): complex 모델 라우팅 라벨(CSV)
- `AGENT_CODEX_SANDBOX` (기본 `workspace-write`): codex sandbox 모드
- `AGENT_CODEX_APPROVAL` (기본 `never`): codex 승인 정책
- `AGENT_CODEX_SEARCH` (기본 `true`): codex 웹 검색 사용 여부
- `OMX_SAFE_MODE` (기본 `true`): oh-my-codex/omx unsafe 모드 차단 활성화
- `OMX_FORBIDDEN_FLAGS` (기본 `--madmax,--yolo,--dangerously-bypass-approvals-and-sandbox`): 차단 플래그 목록
- `OMX_FORBIDDEN_SANDBOXES` (기본 `danger-full-access`): 차단 sandbox 목록
- `AGENT_AUTO_MERGE_ENABLED` (기본 `true`): 에이전트 PR 자동 머지 활성화
- `MANAGER_LOOP_ENABLED` (기본 `true`): manager loop 활성화
- `MANAGER_MAX_SETS_PER_RUN` (기본 `2`): manager loop 1회 실행당 QA 셋 생성 수
- `QA_ADDRESS_BATCH_SIZE` (기본 `5`): QA 검증용 주소 수
- `QA_CHAIN_TARGETS` (기본 `solana-devnet,base-sepolia`): manager가 QA 셋을 만들 체인 목록(CSV)
- `SOLANA_WHITELIST_FILE` (기본 `configs/solana_whitelist_addresses.txt`): whitelist 파일 경로
- `SOLANA_WHITELIST_CSV` (선택): whitelist를 CSV 변수로 직접 주입
- `BASE_WHITELIST_FILE` (기본 `configs/base_whitelist_addresses.txt`): base sepolia whitelist 파일 경로
- `BASE_WHITELIST_CSV` (선택): base sepolia whitelist를 CSV 변수로 직접 주입
- `QA_LOOP_ENABLED` (기본 `true`): qa loop 활성화
- `QA_MAX_ISSUES_PER_RUN` (기본 `1`): qa loop 1회 실행당 처리 이슈 수
- `QA_EXEC_CMD` (기본 `scripts/qa_executor.sh`): QA 실행 명령
- `QA_TRIAGE_ENABLED` (기본 `true`): QA 실패 triage 활성화
- `QA_TRIAGE_EXEC_CMD` (기본 `scripts/qa_triage_executor.sh`): QA triage 실행 명령
- `QA_TRIAGE_CODEX_MODEL` (기본 `gpt-5.3-codex`): QA triage 모델
- `QA_TRIAGE_CODEX_SANDBOX` (기본 `workspace-write`): QA triage sandbox 모드
- `QA_TRIAGE_CODEX_APPROVAL` (기본 `never`): QA triage 승인 정책
- `QA_TRIAGE_CODEX_SEARCH` (기본 `false`): QA triage 웹 검색 사용 여부
- `RALPH_LOOP_ENABLED` (기본 `true`): 자율 루프 전역 ON/OFF
- `RALPH_AUTOPILOT_ENABLED` (기본 `true`): Agent Loop 자동 연속 재기동 ON/OFF
- `RALPH_AUTOPILOT_MAX_ISSUES` (기본 `2`): autopilot이 시작하는 run의 `max_issues`
- `PLANNING_EXEC_CMD` (기본 `scripts/planning_executor.sh`): planner 실행 명령
- `PLANNING_CODEX_MODEL` (기본 `gpt-5.3-codex`): planner 모델
- `PLANNING_CODEX_SANDBOX` (기본 `workspace-write`): planner sandbox 모드
- `PLANNING_CODEX_APPROVAL` (기본 `never`): planner 승인 정책
- `PLANNING_CODEX_SEARCH` (기본 `true`): planner 웹 검색 사용 여부
- `PLANNER_FANOUT_ENABLED` (기본 `true`): planner child issue fanout 활성화
- `PLANNER_FANOUT_CMD` (기본 `scripts/planner_fanout.sh`): planner fanout 실행 명령
- `PLANNER_FANOUT_MAX_ISSUES` (기본 `8`): planner fanout으로 생성할 최대 child issue 수

### 2) Runner 선택 (권장)
- 이름: `AGENT_RUNNER`
- 기본값: 비어 있으면 `ubuntu-latest`
- 권장값: `self-hosted`
- 역할별 runner override:
  - `AGENT_RUNNER_PLANNER` (planner queue)
  - `AGENT_RUNNER_DEVELOPER` (developer queue)
  - `MANAGER_RUNNER` (manager loop)
  - `QA_RUNNER` (qa loop)
  - `SCOUT_RUNNER` (issue scout)

실제 코드 자동 구현까지 돌리려면 self-hosted runner에서 에이전트 CLI/자격증명을 준비하는 것이 안전하다.

### 3) Scheduled Worker
- 워크플로우: `.github/workflows/agent-loop.yml`
- 주기: 15분
- 수동 실행: `workflow_dispatch`
- 워크플로우: `.github/workflows/agent-loop-autopilot.yml`
  - 트리거: Agent Loop 완료 + 5분 주기 + 수동 실행
  - 조건: `autonomous + ready` 큐가 남아 있고 Agent Loop active run이 없을 때 다음 run 자동 시작
- PR 자동 머지 워크플로우: `.github/workflows/agent-auto-merge.yml`
  - 트리거: PR 이벤트 + 15분 주기 스캔 + 수동 실행
  - 대상: agent가 생성한 PR(`agent-issue-*`, `chore(agent):*`)
  - 조건: 차단 라벨(`decision-needed`, `needs-opinion`, `blocked`, `decision/major`, `risk/high`) 없음
  - 선택: secret `AGENT_GH_TOKEN`(`repo` + `workflow` scope) 설정 시 workflow 파일 변경 PR까지 자동 머지 가능
  - self-heal: workflow scope 부족으로 자동 머지가 막히면 PR에 안내 코멘트 자동 생성

## Operational Notes
- 기본 실행자는 `ubuntu-latest`이다.
- 진짜 상시 실행(긴 작업/대용량 작업)이 필요하면 self-hosted runner로 전환한다.
- 브랜치 보호를 활성화해 직접 푸시를 금지하고 PR 경로만 허용한다.
- GitHub 외부 의존 없이 운영하려면 `docs/ralph-local-offline-mode.md`를 따른다.
- 현재 기본 운영 모드는 로컬 우선이며, `.github/workflows/*` 자동 트리거는 비활성화(수동 `workflow_dispatch` 전용)로 유지한다.
