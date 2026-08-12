# UPD-20260813-05 — 완료 수리 이력 materialization

- 기준일: 2026-08-13
- 상태: 구현·local 검증 완료 / 사람 검토 대기
- 환경: WSL2, Firestore Emulator, synthetic fixture

## 결과

복지관이 수리사를 배정할 때 선택한 수리소가 tenant 내 활성 수리소인지 exact read로 검증한다. 수리 요청이 `completed`로 전환되면 같은 Firestore transaction에서 가변 작업지시서와 별도의 불변 `/repairs/{repairId}` 사실 기록을 생성한다.

완료 이력에는 work order·기기·수리소·수리사·발생/기록 시각·청구금액·검증 품질만 기록하며, 원본 증상 자유문이나 미검증 memo는 복사하지 않는다. 이 이력은 기기 타임라인과 후속 신뢰성 모델이 운영 상태 문서 대신 검증된 수리 사실을 사용할 기반이다.

## 한계

- 수리 품목(`/items`) 입력 계약은 아직 구현하지 않았다.
- 부품 설치·제거 이력과 다음 점검 materialization은 후속 범위다.
- 기존 완료 수리 레거시 이관은 별도 migration gate를 거친다.
- production Firebase 배포와 실제 수리 완료를 증명하지 않는다.

