# ADR-0009: Fail-closed 텔레메트리 수집 kernel

- 상태: accepted
- 결정일: 2026-07-21
- 관련 결정: [ADR-0007](./ADR-0007-firebase-first-hybrid.md)

## 맥락

모바일 outbox 이후 서버의 첫 책임은 범용 CRUD가 아니라 인증된 주체의 batch를 제한된 크기로 받아 계약·tenant·멱등성을 검증하는 것이다. Firebase Auth/App Check, Firestore receipt와 Cloud Storage adapter를 한 번에 연결하면 HTTP 계약과 외부 자격증명 문제를 분리해 테스트하기 어렵다. 반대로 개발 편의를 위한 인증 우회가 executable 기본값에 남으면 production 오배포 시 위치정보를 무인증으로 받을 위험이 있다.

## 검토한 선택지

1. Cloud Function에서 인증·파싱·Storage·receipt를 한 함수에 직접 구현한다.
2. 인증 없는 local endpoint를 먼저 만들고 production 배포 때 검사를 추가한다.
3. 외부 서비스와 분리된 ingest kernel을 만들고 Auth·App Check, object store와 receipt store를 interface로 주입한다. adapter가 없으면 ingest는 실패한다.

## 결정

선택지 3을 채택한다.

- Go 표준 라이브러리 HTTP 경계와 순수 ingest service를 우선 구현한다.
- request body는 제한된 크기로 읽고 unknown field·trailing JSON을 거부한다.
- duplicate JSON key와 올바르지 않은 UTF-8을 거부해 parser 간 해석 차이를 막는다.
- batch는 최대 500 samples이며 wire schema의 ID·시각·좌표·optional sensor 범위를 검증한다.
- tenant와 actor는 payload를 신뢰하지 않고 verifier가 반환한 principal과 일치하는지 다시 확인한다.
- 별도 authorizer가 서버 상태에서 기기·세션 소유권과 현재 유효한 정밀위치 동의를 확인하기 전에는 receipt를 예약하지 않는다.
- idempotency key와 batch ID는 모두 tenant 범위에서 하나의 transaction으로 고유성을 확인한다. 같은 key·batch·body hash는 replay이고, 어느 한 식별자라도 다른 조합으로 재사용되면 terminal conflict다.
- object 경로에 다른 content가 이미 있으면 receipt를 `rejected`로 기록하고 `409`로 종료해 무한 재시도를 막는다.
- 오류 응답과 application log에 좌표·원문 body를 포함하지 않는다.
- production Firebase ID token·App Check verifier, Firestore receipt와 Cloud Storage adapter가 없으면 ingest executable은 요청을 허용하지 않는다.
- in-memory adapter는 단위·HTTP 테스트에서만 사용하며 production mode로 선택할 수 없게 한다.

## 결과

- 외부 자격증명 없이도 계약, 권한·scope 불일치, 멱등 replay·conflict, batch 고유성과 object write 횟수를 결정론적으로 검증할 수 있다.
- Firebase/GCP adapter 구현 전에는 production ingest 완료를 주장할 수 없다.
- raw batch 보존·삭제·lifecycle과 실제 Firestore transaction·receipt recovery는 후속 adapter integration gate에서 검증한다.
- host Go가 없는 현재 WSL2에서는 고정된 Go Docker image로 같은 test command를 재현하고, CI에는 동일 Go major를 명시한다.
