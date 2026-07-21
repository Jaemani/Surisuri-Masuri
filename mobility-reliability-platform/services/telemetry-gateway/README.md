# Telemetry gateway

모바일 텔레메트리 수집에 집중하는 scale-to-zero Go Cloud Run 서비스입니다.

초기 책임:

- Firebase ID token과 App Check 검증
- 인증 주체와 tenant membership 검증
- telemetry batch v1 JSON Schema 검증
- `tenant_id + idempotency_key` 중복 제거
- accepted/rejected sample receipt
- 압축 Cloud Storage object 저장과 Firestore receipt 기록
- trace/correlation ID 생성

Firestore에는 GPS sample을 개별 document로 쓰지 않습니다. 현재 환경에는 Go toolchain이 확인되지 않았으므로 구현 전에 재현 가능한 버전을 프로젝트 도구로 고정합니다.
