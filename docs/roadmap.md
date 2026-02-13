# Roadmap

## Objective
운영 신뢰성을 갖춘 멀티체인 인덱서를 표준 운영 체계로 정착한다.

## Milestones

### M1. Collaboration Baseline
- 상태: done
- 산출물:
  - GitHub Issue/PR 템플릿
  - CODEOWNERS
  - CI 필수 게이트
  - 라벨 규칙 및 자동 라벨링

### M2. Reliability Hardening
- 상태: in-progress
- 목표:
  - 장애 탐지 지표/알람 정의
  - 재처리(replay) 절차 표준화
  - 운영 런북 기반 대응 훈련
- 완료 조건:
  - P0/P1 사고에 대해 재현 가능한 대응 절차 확보

### M3. Data Quality Guarantees
- 상태: planned
- 목표:
  - 중복/누락 감지 검증 잡
  - 커서 정합성 모니터링
  - 배치 처리 지연 및 실패율 대시보드
- 완료 조건:
  - 지표 기반으로 데이터 품질 이상을 15분 이내 탐지

### M4. Chain/Plugin Expansion
- 상태: planned
- 목표:
  - 신규 체인 어댑터 도입 절차 템플릿화
  - Sidecar 플러그인 추가/검증 규칙 표준화
  - 성능 리그레션 테스트 자동화
- 완료 조건:
  - 신규 플러그인 도입 시 표준 체크리스트 100% 통과

## Prioritization Rules
- `priority/p0`: 서비스 중단, 데이터 손상 위험
- `priority/p1`: 비즈니스 핵심 플로우 영향
- `priority/p2`: 기능/품질 개선
- `priority/p3`: 장기 최적화

