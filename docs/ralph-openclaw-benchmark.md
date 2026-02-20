# OpenClaw Benchmark For Ralph Autonomous Loop

## 목적
- `openclaw/openclaw`의 운영 패턴을 기준으로, 이 저장소의 Ralph 로컬 자율 루프에 바로 반영할 개선 포인트를 정리한다.
- 결과물은 "바로 실행 가능한 운영 체크리스트 + 단계별 적용 백로그"에 초점을 둔다.

## OpenClaw에서 벤치마킹할 핵심
- 정책 기반 큐 라우팅:
  큐 라우팅을 코드 하드코딩이 아니라 정책 파일(`includeLabels`, `excludeLabels`, `role`, `priority` 등)로 분리한다.
- 에러 유형별 재시도:
  `tool_error`, `validation_error`, `apply_failed` 같은 실패 유형별로 재시도 횟수/지연을 다르게 둔다.
- Gateway 단위 장기 실행 + 락:
  워커를 상시 실행하면서 workspace/gateway 단위 락으로 중복 실행을 차단한다.
- 설정 변경 즉시 반영:
  루프 재기동 없이 설정 변경을 반영하는 hot-reload 경로를 둔다.
- AGENTS 템플릿 기반 운영 메모리:
  heartbeat, checkpoint, memory compaction, notes archive를 표준화한다.
- 보안 경계:
  에이전트/도구별 sandbox와 command/file allowlist를 강제한다.

## Ralph 현재 대비 갭
- 강점:
  `scripts/ralph_local_run.sh`에 계약 게이트, 재큐잉, self-heal, 컨텍스트 압축, 리스크 기반 모델 라우팅이 이미 있다.
- 갭:
  큐 라우팅/재시도 정책이 환경변수 중심이라 선언적 정책 파일 기반 운영성이 약하다.
- 갭:
  `manager` 역할이 로컬 루프에서 1급 실행 주체가 아니라 보조 자동화 스크립트 중심이었다.
- 갭:
  busy-wait 상태를 구조화된 이벤트로 남기는 표준 로그가 부족했다.

## 이번 반영 (2026-02-20)
- `manager` role을 로컬 루프의 정식 역할로 승격:
  `scripts/ralph_local_new_issue.sh`, `scripts/ralph_issue_contract.sh`, `scripts/ralph_local_run.sh`, `scripts/ralph_local_agent_tracker.sh` 업데이트.
- manager 모델 라우팅 추가:
  `RALPH_MANAGER_MODEL`을 도입하고 프로필(`scripts/ralph_local_profile.sh`)에 반영.
- busy-wait 이벤트 기록 추가:
  `.ralph/reports/busywait-events.jsonl`, `.ralph/state.busywait.env`를 도입하고, blocked 정체 상태에서 이벤트를 남기도록 `scripts/ralph_local_run.sh` 업데이트.
- 운영 상태판 확장:
  `scripts/ralph_local_runtime_status.sh`에서 busy-wait 최신 이벤트를 확인 가능하게 확장.

## 권장 백로그 (우선순위)

### P0
- 큐 정책 파일 도입:
  `.ralph/policy/queue-routing.yaml`를 만들고 role/priority/include/exclude를 선언형으로 관리.
- 수용 기준:
  코드 수정 없이 정책 파일만 바꿔도 role별 선별 우선순위가 달라져야 한다.

### P1
- 에러 유형별 재시도 정책 분리:
  `.ralph/policy/retry-policy.yaml`로 실패 유형별 backoff/max_retries를 분리.
- 수용 기준:
  동일 실패라도 유형이 다르면 재시도 횟수/지연이 다르게 동작해야 한다.

### P2
- 프로젝트 fleet 제어 결합:
  외부 `ralphctl`의 fleet 개념(프로젝트별 독립 4-role 세트)을 현재 로컬 루프와 연결.
- 수용 기준:
  한 머신에서 N개 프로젝트를 동시에 돌리되, 프로젝트 간 queue/state/log 충돌이 없어야 한다.

## 참고 링크
- OpenClaw GitHub: https://github.com/openclaw/openclaw
- Queue Routing: https://docs.openclaw.ai/guide/queue-routing
- Retry Policy: https://docs.openclaw.ai/guide/retry-policy
- Agent Loop: https://docs.openclaw.ai/guide/agent-loop
- Gateway Architecture: https://docs.openclaw.ai/guide/gateway-architecture
- AGENTS Template: https://docs.openclaw.ai/guide/AGENTS
- Security: https://docs.openclaw.ai/guide/security-models
