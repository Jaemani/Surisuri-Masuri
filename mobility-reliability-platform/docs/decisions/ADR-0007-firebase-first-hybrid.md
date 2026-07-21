# ADR-0007: Firebase 우선 하이브리드 운영 구조

- 상태: accepted
- 결정일: 2026-07-21
- 대체: [ADR-0004](./ADR-0004-runtime-boundaries.md)

## 맥락

신규 제품은 적은 운영 인원과 초기 실증 규모에서 시작한다. 프로젝트 소유자는 Firebase를 최대한 활용해 배포와 관리 비용을 줄이길 원한다. 동시에 GPS sample을 Firestore 문서로 직접 저장하면 write 수, index 저장량, listener read가 주행시간에 비례해 증가하고 공간·ML 분석에도 불리하다.

필요한 것은 Firebase를 버리거나 모든 데이터를 한 서비스에 넣는 양자택일이 아니라, 관리형 서비스의 장점과 텔레메트리 특성을 분리하는 구조다.

## 검토한 선택지

### A. Firestore 단일 저장소

- 장점: SDK와 보안 규칙이 단순하고 실시간 UI 개발이 빠르다.
- 단점: 위치 sample별 write·index 비용, 대량 경로 조회, 시계열·공간 분석, 모델용 export가 불리하다.

### B. PostgreSQL/PostGIS 중심

- 장점: 관계·공간 query, RLS, transaction, ML feature query가 강하다.
- 단점: 초기 인스턴스 운영과 백업·패치·connection 관리가 필요하며 현재 실증 규모에서 고정비와 운영 부담이 커질 수 있다.

### C. Firebase control plane + GCP telemetry data plane

- 장점: Auth·푸시·배포·클라이언트 SDK를 활용하면서 raw telemetry의 write amplification을 피한다. 관리형·scale-to-zero 구성과 분석 확장이 가능하다.
- 단점: Firestore, Cloud Storage, Cloud Run, 선택적 BigQuery 사이의 계보·삭제·로컬 재현을 직접 설계해야 한다.

## 결정

선택지 C를 채택한다.

### Firebase control plane

- **Firebase Auth**: 사용자·수리사·복지관 운영자 인증
- **App Check**: 모바일 앱과 웹 콘솔 요청의 앱 무결성 신호
- **Cloud Firestore**: 기관 membership, 기기, 수리, 점검, 동의, 주행 세션 metadata, ingest receipt, 현재 상태 projection, 알림, 예측 요약, 보고서 상태
- **Firebase Cloud Messaging**: 점검·수리·동의 관련 push
- **Crashlytics**: 모바일 crash와 non-fatal 수집. 원본 좌표와 PII는 custom key/log에 넣지 않는다.
- **Remote Config**: GPS sampling과 기능 flag. 안전·동의 정책을 우회하는 값은 허용하지 않는다.
- **Firebase Hosting**: 신규 기관 콘솔 배포

### Telemetry data plane

- **Go on Cloud Run**: Firebase ID token과 App Check 검증, batch schema·tenant·idempotency 확인, 수신 receipt 생성
- **Cloud Storage**: 압축된 주행 batch 원본. sample 하나당 Firestore write를 만들지 않는다.
- **Firestore receipt**: batch ID, body hash, object path, 처리 상태, accepted/rejected count, 오류 reason만 저장
- **비동기 projector**: Storage batch에서 trip summary와 기기 현재 상태를 계산해 Firestore에 제한된 문서 수로 반영
- **BigQuery**: 기본 필수 서비스가 아니다. 코호트·공간 집계·ML 학습 query가 필요해지는 시점에 날짜 partition과 tenant/device clustering으로 활성화한다.
- **BigQuery GIS**: 익명 지역 집계가 필요한 경우 PostGIS 대신 우선 검토한다.

### 애플리케이션과 ML

- 모바일: Expo/React Native + TypeScript, SQLite outbox
- 기관 콘솔: Next.js + TypeScript
- 텔레메트리 경계: Go Cloud Run, min instances `0`
- ML: Python/PyTorch, 승인된 Storage/BigQuery snapshot 사용
- 온디바이스: ONNX
- wire contract: versioned JSON Schema

## 비용 경계

1. GPS sample별 Firestore document write를 금지한다.
2. 한 주행의 sample은 제한된 크기의 압축 batch로 묶는다.
3. Firestore realtime listener는 운영상 필요한 작은 projection에만 사용한다.
4. raw Storage object는 lifecycle rule로 만료하고, 파생 데이터는 별도 보존정책을 가진다.
5. BigQuery는 partition filter를 강제하고 개발 query 상한을 둔다.
6. Cloud Run은 min instances `0`, 명시적 concurrency·memory·timeout 한도를 사용한다.
7. GCP budget alert와 서비스별 usage dashboard를 실증 전 설정한다.
8. Emulator Suite와 합성 fixture를 기본 개발환경으로 사용해 불필요한 운영 호출을 막는다.

## 데이터와 보안 경계

- tenant는 client payload가 아니라 검증된 Firebase custom claim/membership에서 결정한다.
- 정밀 위치 object path에는 이름·전화번호·공개 QR 코드를 넣지 않는다.
- Firestore Security Rules는 client가 raw prediction, tenant, role을 임의로 쓰지 못하게 한다.
- Storage object와 Firestore receipt는 같은 `tenant_id + batch_id + body_hash` 계보를 공유한다.
- 사용자 삭제는 Firestore metadata만 지우는 것으로 끝내지 않고 Storage object와 BigQuery partition/delete job까지 추적한다.
- BigQuery 활성화 전까지 ML batch는 승인된 Storage manifest를 사용한다.

## 단계적 도입

### Stage 1 — 개발·소규모 실증

Auth, App Check, Firestore, Storage, Cloud Run, FCM, Crashlytics, Hosting을 사용한다. trip summary와 기기 상태는 Firestore에서 제공한다.

### Stage 2 — 분석·모델 학습

Storage batch 수와 query 요구가 실제로 확인되면 BigQuery load pipeline을 추가한다. 시간분할 dataset과 비용 측정이 먼저다.

### Stage 3 — 공간 운영 query가 필요한 경우

BigQuery GIS의 지연시간과 비용이 제품 요구를 충족하지 못할 때만 관리형 PostgreSQL/PostGIS를 다시 평가한다.

## 결과

- 초기 운영 부담과 고정비를 줄이면서 Firebase 생태계를 적극 활용한다.
- Firestore를 raw telemetry 저장소로 사용하지 않아 sample 수에 따른 write 폭증을 피한다.
- Firebase vendor lock-in은 versioned contract, Storage object manifest, BigQuery 표준 export로 완화한다.
- 기존 PostgreSQL 중심 도메인 문서는 Firestore aggregate와 분석 table 관점으로 재작성해야 한다.
