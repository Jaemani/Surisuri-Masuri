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

구현됐지만 executable에 아직 연결하지 않은 adapter 기반:

- strict 단일 Authorization Bearer와 `X-Firebase-AppCheck` header parser
- Firebase Admin Go SDK 기반 ID token·App Check 검증 wrapper
- Firebase UID·App ID principal 분리와 App ID allowlist
- invalid `401`, unlisted app `403`, verifier provider failure `503` 오류 계약
- production SDK factory의 emulator 환경변수 fail-closed 검사
- tenant·beneficiary membership·app installation·server trip·exact 기기 배정·현재 정밀위치 동의를 교차 검사하는 pure policy
- 좌표/body 없이 bounded exact path만 읽는 Firestore authorization snapshot adapter
- pseudonymous current-consent state ID와 sample 최소·최대 시각 검사
- authorization 재평가와 두 uniqueness index·최초 receipt 생성을 결합한 Firestore transaction adapter
- replay·두 conflict·partial/corrupt linkage의 fail-closed 판정과 30일 receipt lineage

아직 구현하지 않은 production adapter:

- Cloud Storage object store와 lifecycle

verifier, authorization/admission transaction과 object store가 아직 `cmd/server`에 주입되지 않아 현재 executable은 `/healthz`만 `200`으로 응답하고 `/readyz`와 ingest는 `503 adapters_unconfigured`로 닫힙니다. transaction adapter는 local fake seam만 검증했으며 실제 Firestore Emulator 경쟁·ADC/IAM 검증은 후속 gate입니다. production factory guard도 server startup path가 연결되기 전에는 활성 runtime guard가 아닙니다. 인증 우회 local mode는 제공하지 않습니다. Firestore에는 GPS sample을 개별 document로 쓰지 않습니다.

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

Docker build context는 프로젝트 root이며 `.dockerignore` allowlist가 gateway `cmd`·`internal` source, Go module 파일과 synthetic contract JSON fixture만 전달합니다. 패키지 단위 allowlist이므로 새 Go 파일이 host CI에는 보이지만 image에서 조용히 누락되는 구조를 피합니다.

Firestore Emulator가 WSL host의 `8080`을 사용하므로 gateway는 host `8085`로 노출합니다. container 내부와 Cloud Run의 `PORT`는 `8080`을 유지합니다.

```bash
rtk docker run --rm -p 127.0.0.1:8085:8080 mobility-telemetry-gateway:dev
```

host Go로 직접 실행하는 환경에서는 `PORT=8085`를 명시합니다.
