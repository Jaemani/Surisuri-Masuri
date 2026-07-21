# Telemetry gateway

모바일 텔레메트리 수집에 집중하는 scale-to-zero Go Cloud Run 서비스입니다.

현재 구현된 kernel 책임:

- duplicate key·invalid UTF-8까지 거부하는 strict `telemetry-batch.v2` decode와 최대 500 sample 검증
- request body 2MiB 제한과 좌표값이 없는 안정적 오류 응답
- Firebase UID·App ID를 raw body와 분리한 verifier principal
- membership·기기 배정·server trip·installation·현재 정밀위치 동의를 서버 상태로 검사하는 authorizer 계약
- client 문자열 없이 파생한 idempotency key, client batch ID와 server UUIDv7 batch ID를 분리한 replay/conflict 계약
- 결정론적 gzip object와 receipt 상태 전이 interface
- 늦은 retry에서 object를 중복 생성하지 않는 `PutIfAbsent`와 terminal rejection 계약
- Cloud Run용 timeout·graceful shutdown·non-root distroless image

아직 구현하지 않은 production adapter:

- Firebase ID token과 App Check 검증
- tenant membership 및 기기·세션·동의 authorizer
- Firestore receipt store
- Cloud Storage object store와 lifecycle

adapter가 없는 현재 executable은 `/healthz`만 `200`으로 응답하고 `/readyz`와 ingest는 `503 adapters_unconfigured`로 닫힙니다. 인증 우회 local mode는 제공하지 않습니다. Firestore에는 GPS sample을 개별 document로 쓰지 않습니다.

## WSL2에서 검사

host Go가 없어도 고정 Docker image로 검사할 수 있습니다.

```bash
rtk docker run --rm \
  -v "$PWD:/workspace:ro" \
  -w /workspace/services/telemetry-gateway \
  golang:1.26.5-bookworm \
  sh -c 'go test -race ./... && go vet ./...'

rtk docker build \
  -f services/telemetry-gateway/Dockerfile \
  -t mobility-telemetry-gateway:dev .
```

Docker build context는 프로젝트 root이며 `.dockerignore` allowlist가 현재 gateway source와 사용하는 synthetic contract fixture 하나만 전달합니다.

Firestore Emulator가 WSL host의 `8080`을 사용하므로 gateway는 host `8085`로 노출합니다. container 내부와 Cloud Run의 `PORT`는 `8080`을 유지합니다.

```bash
rtk docker run --rm -p 127.0.0.1:8085:8080 mobility-telemetry-gateway:dev
```

host Go로 직접 실행하는 환경에서는 `PORT=8085`를 명시합니다.
