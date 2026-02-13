# GitHub Collaboration Standard

## Core Principle
- 구현은 비동기로 진행하되, 의사결정과 품질 상태는 GitHub에서 추적 가능해야 한다.
- 사람 개입은 최소화하고, 필요한 경우에만 `decision-needed + needs-opinion`으로 에스컬레이션한다.
- GitHub API/Actions 장애 시에는 `.ralph/*.md` 큐를 사용하는 로컬 오프라인 루프(`docs/ralph-local-offline-mode.md`)로 즉시 전환한다.
- 로컬 우선 운영 기간에는 GitHub 워크플로우 자동 트리거를 끄고 수동 실행만 허용한다.

## Work Item Lifecycle
1. Issue 생성:
   - 작업: `Task`
   - 장애: `Bug Report`
   - 의사결정: `Decision Needed`
2. 라벨 지정:
   - `type/*`, `area/*`, `priority/*`, 필요 시 `sev*`, `decision-needed`
3. 브랜치 생성 후 Draft PR 오픈
4. PR 본문에 변경 내용, 검증 결과, 리스크, 롤백 전략 누적
5. CI 통과 후 `ready-for-review` 라벨 부여
6. 에이전트 PR은 `Agent Auto Merge`가 조건 충족 시 자동 merge
7. 브랜치 푸시 실패(예: 기존 브랜치 충돌/권한 문제) 시 이슈는 `ready`로 되돌려 재시도 및 실패 사유 코멘트

## Autonomous Loop
- 워크플로우: `.github/workflows/agent-loop.yml`
- 큐 입력 라벨: `autonomous + ready`
- 실행 중 라벨: `in-progress`
- 결과 라벨: `ready-for-review` 또는 `blocked + decision-needed + needs-opinion`
- 실행 명령은 repository variable `AGENT_EXEC_CMD`로 주입한다.
- runner는 `AGENT_RUNNER` variable로 지정한다 (비어 있으면 `ubuntu-latest`).
- 연속 실행 워크플로우: `.github/workflows/agent-loop-autopilot.yml`
  - Agent Loop 완료 후 큐에 `autonomous + ready`가 남아 있으면 다음 run 자동 시작
  - 변수: `RALPH_AUTOPILOT_ENABLED`, `RALPH_AUTOPILOT_MAX_ISSUES`
- planner/developer 큐는 분리되어 실행된다.
  - planner runner: `AGENT_RUNNER_PLANNER` (fallback: `AGENT_RUNNER`)
  - developer runner: `AGENT_RUNNER_DEVELOPER` (fallback: `AGENT_RUNNER`)
- 라벨 필터:
  - planner queue: `role/planner` 포함 이슈만 처리
  - developer queue: `role/planner`, `role/qa`, `decision/major` 제외 이슈 처리 (기본)
- `risk/high` 라벨 이슈는 사람 확인 전 자동 실행하지 않는다.
- `AGENT_MAX_AUTO_RETRIES`를 넘겨 실패하면 사람 확인 상태로 자동 전환한다.
- `RALPH_SELF_HEAL_ENABLED=true`면 PR 생성 권한 오류 시 Actions workflow 권한 자동 복구를 시도한다.

## Autonomous Merge
- 워크플로우: `.github/workflows/agent-auto-merge.yml`
- 트리거: `pull_request_target` + 15분 주기 스캔 + 수동 `workflow_dispatch`
- 대상 PR:
  - head branch가 `agent-issue-*`
  - 또는 제목이 `chore(agent):`로 시작
- 블로킹 조건:
  - 연동 이슈에 `blocked`, `decision-needed`, `needs-opinion`, `decision/major`, `risk/high` 라벨이 있으면 merge 중단
- merge 방식:
  - 우선 auto-merge(squash) 활성화
  - `mergeable_state=behind`면 base 최신으로 branch update 시도 후 auto-merge 재시도
  - 불가 시 체크가 이미 green이면 즉시 squash merge 시도
- 선택 설정:
  - secret `AGENT_GH_TOKEN`(classic PAT, `repo` + `workflow` scope)을 설정하면 workflow 파일을 수정한 PR도 자동 merge 가능
  - workflow scope 부족으로 막히면 PR에 self-heal 안내 코멘트를 자동 남긴다.

## Issue Discovery Loop
- 워크플로우: `.github/workflows/issue-scout.yml`
- 발굴 대상:
  - 최근 실패한 CI 실행
  - 코드 내 `TODO|FIXME|HACK|XXX`
- 생성 이슈 라벨:
  - `agent/discovered`
  - `autonomous + ready`
- 중복 생성 방지:
  - 이슈 본문 fingerprint(`[agent-scout:*]`) 기준 dedup
- 자동 정리:
  - `agent-loop` 실행 시 merge 완료 PR이 연결된 `ready-for-review` 이슈는 자동 close
  - 같은 제목의 `agent/discovered` 중복 이슈는 활성 건 1개를 남기고 deprecated duplicate로 자동 close

## Multi-Agent Collaboration
- `Planner`:
  - `role/planner` 이슈에서 spec/plan 문서 갱신
  - 산출물: `IMPLEMENTATION_PLAN.md`, `specs/*`
  - 큰 이슈면 fanout 파일(`.agent/planner-fanout-<issue>.json`)을 통해 child issue 자동 생성
- `Manager`:
  - `solana-devnet` + `base-sepolia` 화이트리스트 주소셋에서 QA 검증 이슈 생성 (`qa-ready`)
  - 이슈 본문에 `QA_CHAIN`, `QA_WATCHED_ADDRESSES`를 포함해 QA 실행기를 체인별로 구동
- `Developer`:
  - `autonomous + ready` 이슈 구현 및 PR 생성 (`agent-loop`)
  - 기본 모델: `gpt-5.3-codex-spark`
  - 라벨 기반 복잡도 라우팅(`scripts/codex_model_router.sh`)으로 complex 이슈는 `gpt-5.3-codex` 자동 선택
  - `scripts/codex_safety_guard.sh`로 omx unsafe 플래그/샌드박스를 차단
- `QA`:
  - `qa-ready` 이슈 검증
  - 실패 시 model triage 후 developer 큐로 bug 이슈 자동 생성

권장 실행 순서:
1. `Autonomous Task` 이슈 생성
2. `autonomous`, `ready`, `priority/*`, `area/*` 라벨 설정
3. 기획이 큰 경우 `role/planner`로 시작하고 fanout child issue를 자동 생성
4. 에이전트 루프가 브랜치/PR 생성 후 테스트
5. CI 통과 확인 후 merge

## Decision Protocol
- 선택지가 필요한 경우 `Decision Needed` 이슈를 사용한다.
- 영향이 큰 결정은 `Major Decision` 이슈 템플릿(`decision/major`)을 사용한다.
- Option A를 기본안으로 제시한다.
- 마감 시간까지 응답이 없으면 기본안으로 진행한다.
- 실행기는 결정 필요 시 원본 이슈 코멘트에서 1/2/3 옵션으로 응답을 요청한다.
- `decision/major` 이슈는 owner 입력 전 실행 큐(`ready`, `qa-ready`)로 진행되지 않는다.

## Label Taxonomy
- `type/task`, `type/bug`, `type/docs`, `type/chore`
- `area/pipeline`, `area/sidecar`, `area/storage`, `area/infra`
- `priority/p0` ~ `priority/p3`
- `sev0` ~ `sev3`
- `decision-needed`, `needs-opinion`, `blocked`, `ready-for-review`
- `decision/major`
- `agent/discovered`
- `role/manager`, `role/developer`, `role/qa`
- `role/planner`
- `qa-ready`, `qa-in-progress`, `qa/passed`, `qa/failed`

## Required Status Checks
- `Go Test + Build`
- `Go Lint`
- `Sidecar Test + Build`

## Release Automation
- 워크플로우: `.github/workflows/release.yml`
- 트리거: `main` push 또는 수동 실행
- 버전 규칙: `vX.Y.Z` (SemVer)
  - `release/major` 또는 `decision/major` -> major bump
  - `release/minor` 또는 `type/task` -> minor bump
  - 그 외(`release/patch`, `type/bug`, `type/chore`, `type/docs`) -> patch bump
- 릴리즈 노트: GitHub 자동 생성 노트 + `.github/release.yml` 카테고리 적용

## Branch Protection (GitHub Settings)
기본 브랜치(`main`)에 아래 규칙 적용:
1. Require a pull request before merging
2. Require approvals (solo 운영 기본 0, 팀 운영 권장 1+)
3. Require review from Code Owners (팀 운영 시 권장)
4. Require status checks to pass before merging
5. Include administrators
6. Block force pushes and branch deletion

CLI로 자동 적용하려면:
```bash
scripts/setup_branch_protection.sh emperorhan/multichain-indexer main
```

## SLA Suggestion
- `sev0`: 1시간 내 초기 대응
- `sev1`: 4시간 내 초기 대응
- `decision-needed`: 24시간 내 의사결정
