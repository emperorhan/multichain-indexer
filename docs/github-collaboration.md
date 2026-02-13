# GitHub Collaboration Standard

## Core Principle
- 구현은 비동기로 진행하되, 의사결정과 품질 상태는 GitHub에서 추적 가능해야 한다.
- 사람 개입은 최소화하고, 필요한 경우에만 `decision-needed`로 에스컬레이션한다.

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
6. 보호 규칙 충족 후 merge (팀 운영 시 CODEOWNER 승인 권장)

## Autonomous Loop
- 워크플로우: `.github/workflows/agent-loop.yml`
- 큐 입력 라벨: `autonomous + ready`
- 실행 중 라벨: `in-progress`
- 결과 라벨: `ready-for-review` 또는 `blocked`
- 실행 명령은 repository variable `AGENT_EXEC_CMD`로 주입한다.
- runner는 `AGENT_RUNNER` variable로 지정한다 (비어 있으면 `ubuntu-latest`).

권장 실행 순서:
1. `Autonomous Task` 이슈 생성
2. `autonomous`, `ready`, `priority/*`, `area/*` 라벨 설정
3. 에이전트 루프가 브랜치/PR 생성 후 테스트
4. CI 통과 확인 후 merge

## Decision Protocol
- 선택지가 필요한 경우 `Decision Needed` 이슈를 사용한다.
- Option A를 기본안으로 제시한다.
- 마감 시간까지 응답이 없으면 기본안으로 진행한다.

## Label Taxonomy
- `type/task`, `type/bug`, `type/docs`, `type/chore`
- `area/pipeline`, `area/sidecar`, `area/storage`, `area/infra`
- `priority/p0` ~ `priority/p3`
- `sev0` ~ `sev3`
- `decision-needed`, `blocked`, `ready-for-review`

## Required Status Checks
- `Go Test + Build`
- `Go Lint`
- `Sidecar Test + Build`

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
