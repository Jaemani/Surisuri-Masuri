# 2026년 5–12월 기술 로드맵

이 문서는 월별 기술 게이트의 기준선이다. 반월 단위 작업·의존성·완료 정의는 [마스터 실행계획](./plans/MASTER_EXECUTION_PLAN.md), 테스트와 성취 판단은 [검증·증거 계획](./plans/VALIDATION_AND_EVIDENCE_PLAN.md), 월 2회 운영은 [정기보고·회의 운영계획](./plans/REPORTING_AND_MEETING_PLAN.md)을 따른다.

## 운영 방식

- 매월 하나의 기술 게이트를 통과한다.
- 매월 15일과 말일에 계획 기준 기술 정기리포트를 발행한다.
- 정기리포트는 실제 진척을 꾸미지 않는다. 계획상 논의와 실제 증빙을 별도 칸에 기록한다.
- 영업·행정 성과가 없는 기간에는 기술 실험 하나를 완결해 지도, 영상, 그래프, 테스트표 중 하나를 남긴다.

## 월별 게이트

| 월 | 기술 게이트 | 핵심 구현 | 발표 가능한 결과 | 정기리포트 |
| --- | --- | --- | --- | --- |
| 5월 | Greenfield Definition | IP 경계, 제품 헌장, 모바일 정보구조, GPS vertical slice 설계 | 신규 아키텍처와 모바일 흐름 | R01, R02 |
| 6월 | Reliable Mobile Capture | Android/iOS 권한, background GPS, SQLite 이벤트 로그, offline sync | 비행기 모드 복구와 배터리·정확도 비교 | R03, R04 |
| 7월 | Trusted Telemetry Platform | Go Cloud Run ingest, idempotency, Firestore receipt, Cloud Storage batch, fenced recovery, 위치 필터·보존정책 | 정제 전후 경로와 부하·중복·복구·비용 테스트 | R05, R06 |
| 8월 | On-device Data Quality ML | 라벨링, 규칙 baseline, PyTorch 시계열 모델, ONNX 배포 | confusion matrix와 모바일 추론 | R07, R08 |
| 9월 | Asset State & Reliability | 이벤트 projection, 수리 importer, 부품 상태, 생존분석 baseline | 기기 타임라인과 위험곡선 | R09, R10 |
| 10월 | Calibrated Decision Support | calibration, abstention, 수리사 피드백, fact store, AI 보고서 | 근거 클릭형 위험 설명과 평가표 | R11, R12 |
| 11월 | Field Operations | 신규 기관 콘솔, 문서 adapter, pilot, observability, security, accessibility | 운영 대시보드와 장애·접근성 전후 비교 | R13, R14 |
| 12월 | Reproducible Final Evidence | 시간분할 최종평가, 장애 훈련, 데이터·모델 카드, 데모·백서 | 5분 데모와 재현 패키지 | R15, R16 |

## 월별 완료 조건

### 5월

- 신규 저장소가 기존 세 코드베이스에 runtime 의존하지 않는다.
- 모바일 자체 GPS가 유일한 필수 주행 데이터원으로 정의된다.
- 텔레메트리 이벤트 v1과 위치정보 생명주기가 문서화된다.

### 6월

- foreground/background/권한 거부/앱 재시작 시나리오가 실기기에서 재현된다.
- 네트워크가 없는 동안 수집한 이벤트가 재연결 후 중복 없이 전달된다.
- 수집 주기별 배터리와 정확도 측정 방법이 고정된다.

### 7월

- Cloud Run 수집 경계가 Firebase Auth/App Check, 계약 위반 batch와 tenant 위반 요청을 거부한다.
- 중복 전송·순서 역전·부분 실패가 재현 가능하게 테스트된다.
- active lease owner 하나와 단조 증가 fencing token으로 stale finalizer가 차단되고, raw-only/no-artifact/manifest-only/stored-missing 복구 분류와 deadline cleanup 전환 경쟁이 검증된다.
- Firestore에는 GPS sample을 개별 문서로 쓰지 않고 receipt와 파생 상태만 저장한다.
- Cloud Storage 원본 batch와 BigQuery/파생 집계의 보존·삭제 정책이 분리된다.

### 8월

- 데이터 품질 모델이 규칙 기반 판별과 동일 평가셋에서 비교된다.
- 모델 크기·지연시간·정확도 변화가 양자화 전후로 기록된다.
- 모델은 주행 여부를 확정하지 않고 검토·보정 신호로 사용된다.

### 9월

- 이벤트 재생으로 임의 시점의 기기·부품 상태를 복원한다.
- 이관 데이터의 출처와 변환 결과가 추적된다.
- 신뢰성 모델은 고정 점검주기와 비교되고 데이터 부족 시 abstain한다.

### 10월

- 위험도와 사용자 설명 생성이 분리된다.
- 주요 보고서 문장마다 fact ID 또는 `확인 필요` 상태가 존재한다.
- 수리사 피드백이 원 예측을 덮어쓰지 않고 새 이벤트로 보존된다.

### 11월

- 복지관별 사용자·기기·보고서가 논리적으로 격리된다.
- 위치·알림·QR 핵심 흐름의 접근성 검토 결과가 남는다.
- 수집·동기화·모델·보고서 실패를 하나의 trace로 추적할 수 있다.

### 12월

- 최종 평가는 학습 시점 이후 데이터로 수행한다.
- 성공 사례뿐 아니라 대표 실패 유형과 제한사항을 발표한다.
- 데모, 기술백서, 데이터카드, 모델카드, 재현 명령이 연결된다.

## 영업 성과가 없는 주의 기술 카드

1. 위치 권한 거부 UX 비교
2. GPS 샘플링 간격별 배터리·정확도 실험
3. 앱 강제종료·재부팅 후 이벤트 복구
4. 동일 배치 10회 전송 멱등성 검증
5. GPS 노이즈 필터 전후 경로
6. 출발·도착 민감 위치 마스킹
7. QR 저조도·훼손 인식률
8. 모델 양자화 전후 크기·지연시간
9. tenant 간 권한 침범 테스트
10. 보고서 근거 누락·오염 입력 공격 테스트
11. 서버 장애 후 이벤트 재처리
12. 접근성 전후 과업 완료시간 비교
