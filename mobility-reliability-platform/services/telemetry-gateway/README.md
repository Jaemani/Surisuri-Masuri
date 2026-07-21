# Telemetry gateway

모바일 텔레메트리 수집에 집중하는 scale-to-zero Go Cloud Run 서비스입니다.

현재 구현된 kernel 책임:

- duplicate key·invalid UTF-8까지 거부하는 strict `telemetry-batch.v2` decode와 최대 500 sample 검증
- request body 2MiB 제한과 좌표값이 없는 안정적 오류 응답
- Firebase UID·App ID를 raw body와 분리한 verifier principal
- membership·기기 배정·server trip·installation·현재 정밀위치 동의를 서버 상태로 검사하는 authorizer 계약
- client 문자열 없이 파생한 idempotency key, client batch ID와 server UUIDv7 batch ID를 분리한 replay/conflict 계약
- 결정론적 gzip raw object와 canonical immutable manifest 계약
- `DoesNotExist`, exact generation read, SHA-256·CRC32C·size 검증을 사용하는 Cloud Storage adapter
- raw·manifest의 path/hash/checksum/size/generation 전체를 고정하는 Firestore receipt finalizer
- raw 충돌만 terminal rejection으로, manifest 충돌은 복구 가능한 reserved 상태로 유지하는 단계별 오류 계약
- reserved receipt의 최초 lease, active replay 차단, 만료 takeover와 단조 증가 fencing token
- current fence를 요구하는 `MarkStored`·`MarkRejected`와 safe lease release
- request lease 갱신, sweeper 전용 recovery claim, recovery attempt `started` 원장과 reserved-origin cleanup transition
- replay authorization의 receipt read-time coherence와 deadline·clock skew·revision overflow fail-closed 검사
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
- raw/manifest partial failure와 finalizer retry를 같은 artifact generation으로 수렴시키는 ingest service

아직 구현하지 않은 production 운영 경계:

- generation-pinned read-only artifact classifier와 Storage version inventory adapter
- classifier 결과를 소비하는 forward reconciler, attempt completion/failure와 bounded sweeper runtime
- cleanup lease·target, generation-pinned delete, nested ledger purge와 accepted deletion auditor
- staging bucket IAM·lifecycle·retention·soft-delete policy와 실제 삭제 drill

verifier, authorization/admission transaction, artifact store와 recovery control plane이 아직 `cmd/server`에 주입되지 않아 현재 executable은 `/healthz`만 `200`으로 응답하고 `/readyz`와 ingest는 `503 adapters_unconfigured`로 닫힙니다. Firestore transaction은 local Emulator에서, Storage generation/replay는 official testbench에서 검증했지만 ADC/IAM·staging lifecycle 증거는 아닙니다. lease claim은 artifact read 권한이 아니며 current authorization과 classifier가 연결되기 전에는 worker나 scheduler를 활성화하지 않습니다. production factory guard도 server startup path가 연결되기 전에는 활성 runtime guard가 아닙니다. 인증 우회 local mode는 제공하지 않습니다. Firestore에는 GPS sample을 개별 document로 쓰지 않습니다.

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

Firestore transaction의 실제 commit/retry와 concurrent same-batch 직렬화는 Firebase Emulator에서 별도 integration test로 검증합니다. host Go가 없는 WSL용 전체 명령은 [WSL Runbook](../../docs/development/WSL_RUNBOOK.md)에 있습니다. 일반 `go test`는 emulator 환경변수가 없으면 해당 integration case만 skip합니다.

Cloud Storage의 `DoesNotExist`, generation-pinned compressed-byte replay는 pinned official Storage testbench에서 별도 integration test로 검증합니다. 일반 `go test`에서는 `STORAGE_EMULATOR_HOST`가 없으면 해당 case만 skip합니다.

Docker build context는 프로젝트 root이며 `.dockerignore` allowlist가 gateway `cmd`·`internal` source, Go module 파일과 synthetic contract JSON fixture만 전달합니다. 패키지 단위 allowlist이므로 새 Go 파일이 host CI에는 보이지만 image에서 조용히 누락되는 구조를 피합니다.

Firestore Emulator가 WSL host의 `8080`을 사용하므로 gateway는 host `8085`로 노출합니다. container 내부와 Cloud Run의 `PORT`는 `8080`을 유지합니다.

```bash
rtk docker run --rm -p 127.0.0.1:8085:8080 mobility-telemetry-gateway:dev
```

host Go로 직접 실행하는 환경에서는 `PORT=8085`를 명시합니다.
