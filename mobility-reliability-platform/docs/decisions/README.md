# Architecture Decision Records

되돌리기 어렵거나 여러 컴포넌트에 영향을 미치는 기술·제품 결정을 기록합니다.

## 작성 조건

- 데이터 소유권·보존·삭제 방식이 달라질 때
- 서비스 또는 언어 경계를 새로 만들 때
- 공개 API·이벤트 계약이 바뀔 때
- 사용자 안전이나 개인정보에 영향을 줄 때
- 모델 책임, 평가 기준, fallback이 바뀔 때

## 상태

- `proposed`: 검토 중
- `accepted`: 현재 적용 기준
- `superseded`: 후속 ADR로 대체됨
- `rejected`: 검토했으나 선택하지 않음

결정문은 제품 업데이트나 장애 기록을 대신하지 않습니다. 실제 제품 반영은 해당 product update를 링크하고, 실패는 incident를 링크합니다.
