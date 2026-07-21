# Project instructions

## Shell

- 모든 셸 명령은 `rtk`를 접두사로 사용한다.
- 파일 검색은 `rtk grep`, `rtk find`, `rtk read`를 우선한다.
- 파일 편집은 `apply_patch`를 사용한다.

## Greenfield boundary

- 상위 폴더의 `soo-ri`, `soo-ri-admin`, `power_assist_device_helper_backend` 코드에 runtime 의존성을 만들지 않는다.
- 기존 코드, 브랜드, 화면, 로고, 문구를 복사하지 않는다.
- 승인된 DB 형식과 수리데이터는 `docs/data/MIGRATION_GATES.md`를 통과한 뒤 명시적 importer로만 이관한다.
- 외부 IoT 센서와 과거 GPS API는 신규 텔레메트리의 의존성이 아니다. 모바일 기기의 자체 GPS가 1차 데이터원이다.

## Documentation streams

코드 변경 시 아래 트리거를 확인한다.

- 복잡한 선택 또는 되돌리기 어려운 선택: `docs/decisions/`
- 사용자에게 보이는 제품 변화: `docs/product-updates/`
- Sev-1 또는 Sev-2 오류: `docs/incidents/`
- 팀·복지관·지원기관에 전달할 문서: `docs/reports/`
- 성능·기능 주장의 근거: `docs/evidence/`

한 문서가 둘 이상의 역할을 대신하지 않는다. 각 문서는 관련 문서를 링크한다.

## Truthfulness

- 계획, 실제 진행, 검증된 완료를 명시적으로 분리한다.
- 회의 일시, 참석자, 사진, 지출, 사용자 반응을 생성하거나 추정하지 않는다.
- 합성 데이터와 실제 사용자 데이터를 같은 성과로 집계하지 않는다.
- 모델의 입력 데이터와 평가셋이 없으면 성능을 주장하지 않는다.
- 중대 오류를 일반 제품 업데이트에 숨기지 않는다.

## Product constraints

- 위치 원본은 최소 수집·최소 보존하며 사용자 삭제 요구를 지원한다.
- 모바일 주행 세션은 명시적 시작을 기본으로 한다. 자동 감지는 보조 신호다.
- LLM은 고장 위험을 계산하지 않는다. 검증된 사실과 모델 출력을 설명한다.
- `Digital Twin` 명칭은 이벤트 재생으로 시점별 기기 상태를 복원할 수 있을 때만 사용한다.
- 멀티테넌트 경계를 모든 API와 데이터 모델의 기본 조건으로 취급한다.
