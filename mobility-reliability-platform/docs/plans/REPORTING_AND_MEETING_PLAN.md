# 정기보고·회의 운영계획

## 1. 목적

이 계획은 2026년 5월~12월 동안 매월 2회, 총 16회의 기술 정기리포트를 실제 개발 속도와 분리해 운영하는 방법을 정의한다. 프로젝트가 이미 과거 현장 성과와 구현 경험을 갖고 있어 사업·행정 진척이 적은 회차에도 기술 판단·실험·증거를 안정적으로 제시할 수 있게 한다.

사전작성본은 계획과 검토 의제를 준비하는 도구다. 존재하지 않은 회의, 참석자, 사진, 지출, 구현, 배포, 실증을 만드는 용도가 아니다.

## 2. 고정 발행 일정

| 월 | 1차 | 2차 | 월 주제 |
| --- | --- | --- | --- |
| 5월 | [05-15](../reports/fixed/2026-05-15.md) | [05-31](../reports/fixed/2026-05-31.md) | 범위·모바일 GPS 설계 |
| 6월 | [06-15](../reports/fixed/2026-06-15.md) | [06-30](../reports/fixed/2026-06-30.md) | background·offline sync |
| 7월 | [07-15](../reports/fixed/2026-07-15.md) | [07-31](../reports/fixed/2026-07-31.md) | 인증 수집·Storage·privacy |
| 8월 | [08-15](../reports/fixed/2026-08-15.md) | [08-31](../reports/fixed/2026-08-31.md) | PyTorch·ONNX 품질 모델 |
| 9월 | [09-15](../reports/fixed/2026-09-15.md) | [09-30](../reports/fixed/2026-09-30.md) | digital twin·생존분석 |
| 10월 | [10-15](../reports/fixed/2026-10-15.md) | [10-31](../reports/fixed/2026-10-31.md) | calibration·근거형 agent |
| 11월 | [11-15](../reports/fixed/2026-11-15.md) | [11-30](../reports/fixed/2026-11-30.md) | console·pilot·운영 검증 |
| 12월 | [12-15](../reports/fixed/2026-12-15.md) | [12-31](../reports/fixed/2026-12-31.md) | 최종 평가·백서·데모 |

각 문서의 `계획상 논의`, `결정 예정`, `기대 산출물`, `검증 지표`는 baseline이다. 발행 시 `실제 진행 입력란`과 `증빙 링크 입력란`만 사실에 맞게 채운다.

## 3. 회차 운영 단위

각 정기리포트는 실제 15~30분의 검토 세션 하나로 운영할 수 있다.

### 회의 전

- 해당 fixed report의 계획 의제에서 실제 검토 가능한 항목을 고른다.
- 커밋, 화면, 테스트, 그래프, ADR 중 최소 하나를 준비한다.
- 실제 진행이 없으면 아래 기술 카드 중 하나를 작은 실험으로 준비한다.
- 미리 작성한 결론은 `결정 후보`로 표시하고 실제 결정을 확정하지 않는다.

### 회의 중

1. 계획상 위치와 실제 상태 차이를 확인한다.
2. 실제 산출물을 화면으로 검토한다.
3. 비교한 대안, 실패, 제약을 기록한다.
4. 결정·보류·폐기 항목을 나눈다.
5. 다음 기한과 검증 증거를 정한다.

### 회의 후

- 실제 일시·참석자·방식은 사실만 입력한다.
- 정기리포트의 실제란과 evidence link를 갱신한다.
- 중요한 선택은 ADR, 검증된 제품 변화는 Product Update, 심각한 실패는 Incident로 분리한다.
- 화면 캡처는 token·PII·좌표·기관 비공개 정보를 검토한 후 연결한다.

## 4. 회의록 최소 구조

```text
회의 목적
실제 검토한 산출물
비교한 대안
실제 결정 / 보류 / 폐기
결정 근거
다음 작업과 완료 조건
증빙 링크
실제 일시 / 실제 참석자 / 실제 회의 방식
```

참석사진·회의비가 필요한 외부 양식은 해당 양식 규칙과 실제 증빙을 따른다. 온라인 회의·지출이 없었다면 지출이나 사진을 만들어 채우지 않는다.

## 5. 회차별 발표 패키지

매 보고에는 다음 다섯 장을 기본 시각 구조로 사용한다.

1. 이번 회차의 질문 한 문장
2. 시스템 또는 사용자 흐름 변화 한 장
3. 실행 화면·지도·그래프 한 장
4. 성공/실패/제약을 함께 보이는 결과표 한 장
5. 결정과 다음 게이트 한 장

기능이 완성되지 않았을 때는 다음 구조로 바꾼다.

```text
가설 → 실험 조건 → 관측 결과 → 실패/한계 → 다음 결정
```

“진행 중” 화면만 반복하지 않고 측정 가능하거나 비교 가능한 산출물을 한 개 만든다.

## 6. 사업·행정 진척이 없는 회차의 기술 카드

| 카드 | 1~3일 산출물 | 사용할 수 있는 시각 |
| --- | --- | --- |
| 위치 권한 상태 | Android/iOS 상태표와 UX | permission state diagram |
| sampling 비교 | cadence·accuracy·battery | line/bar chart |
| 앱 재시작 복구 | queue 전후 count | recovery timeline |
| offline sync | 비행기 모드 batch 흐름 | 30초 영상·state chart |
| 중복 방지 | 같은 batch 반복 결과 | receipt table |
| header 공격 | duplicate/combined/oversized token | HTTP failure matrix |
| GPS 필터 | raw/filtered/masked | 세 경로 지도 |
| 위치 삭제 | object→receipt→projection 삭제 | lineage diagram |
| QR 인식 | 밝기·각도·훼손별 결과 | heatmap/table |
| tenant 격리 | allow/deny rules | role matrix |
| model baseline | rules vs model | confusion matrix |
| ONNX 경량화 | size/latency/parity | before/after chart |
| projection replay | checksum과 failure recovery | event timeline |
| survival calibration | predicted/observed | calibration curve |
| agent 공격 | grounded/unsupported claims | error taxonomy |
| 장애 주입 | 탐지→완화→복구 | incident timeline |
| 접근성 | focus/크기/과업 결과 | before/after screens |
| 양식 adapter | canonical→기관 필드 | mapping table |

카드가 합성 데이터나 mock을 사용하면 제목과 결과에 `synthetic` 또는 `prototype`을 표시한다.

## 7. 계획과 실제를 다루는 문장 규칙

| 상태 | 허용 표현 | 피할 표현 |
| --- | --- | --- |
| 계획만 있음 | 설계할 예정이다, 검토 기준을 확정했다 | 구현했다, 개선했다 |
| unit/local만 통과 | 합성 fixture의 단위검사가 통과했다 | 운영 검증 완료 |
| adapter 미연결 | 검증기 모듈을 구현했다, 연결 전이다 | 인증을 적용했다 |
| 실기기 일부 | 기록한 장비·조건에서 확인했다 | Android/iOS 전체 지원 완료 |
| field 미실시 | 현장 검증이 남아 있다 | 사용자 효과가 입증됐다 |
| 모델 미달 | baseline 유지, 배포 유보 | AI 예측 기능 고도화 완료 |

## 8. EvidenceOps 자동화 경계

향후 Git·테스트·Figma·모델 결과·실제 회의 메모를 정규화해 문서 초안을 만드는 EvidenceOps 도구를 만들 수 있다.

자동화가 할 수 있는 일:

- commit/PR/test/model artifact의 식별자와 hash 수집
- fixed report의 실제란 초안과 링크 후보 생성
- 주장에 근거가 없으면 `[확인 필요]` 표시
- 반복 문장·상충 상태·누락 evidence 경고
- Markdown/DOCX/PDF 초안 생성

자동화가 하면 안 되는 일:

- 참석자, 일시, 사진, 지출, 사용자 수 자동 생성
- 계획을 완료 실적으로 변경
- 실제 회의 없이 회의 결론 확정
- 민감정보를 모델 prompt나 Git 문서로 전송
- 사람 검토 없이 `verified` 또는 공식 제출 상태로 승격

## 9. 발행 전 체크리스트

- [ ] 문서 날짜가 계획 기준인지 실제 발행일인지 구분했다.
- [ ] 실제 수행·결정·수치에 evidence link가 있다.
- [ ] 합성·로컬·실기기·staging·field가 구분됐다.
- [ ] 아직 연결되지 않은 모듈을 운영 적용으로 표현하지 않았다.
- [ ] 실제 참석자·시간·사진·지출만 사용했다.
- [ ] 좌표·PII·token·기관 비공개 정보가 없다.
- [ ] 계획과 차이, 실패, 보류가 포함됐다.
- [ ] 다음 회차의 판단 질문과 완료 조건이 있다.

## 10. 월말 품질 감사

매월 말일 리포트 후 다음을 점검한다.

- 같은 성과가 여러 회차에서 새 성과처럼 반복되지 않았는가
- fixed report, Product Update, Evidence의 상태가 충돌하지 않는가
- 숫자의 기간·모수·단위·버전이 있는가
- 실제 회의 증빙과 기술 증거가 분리돼 있는가
- 다음 달 게이트가 현재 미완료 위험을 숨기지 않는가

충돌이 있으면 기존 문서를 삭제해 숨기지 않고 정정·대체 이유를 남긴다.
