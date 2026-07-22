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
- deadline cleanup에서 exact expired forward attempt를 같은 transaction으로 `failed/lease_expired` 종료하고 missing·malformed linkage를 write-zero로 거부하는 원장 경계
- immutable transition time·policy와 `11m > max lease 5m + complete StoreBatch 5m` quiet-period, cleanup-only fenced lease·attempt claim과 expired takeover
- caller deadline을 포함해 전체 raw+manifest `StoreBatch`를 최대 5분으로 제한하고 cancellation 뒤 추가 GCS create·trusted late-success를 차단하는 artifact mutation 경계
- replay authorization의 receipt read-time coherence와 deadline·clock skew·revision overflow fail-closed 검사
- forward recovery, accepted integrity audit와 `cleanup_dry_run`을 issuer·request shape·fence별로 분리하는 provider-neutral artifact classification 계약과 opaque read grant
- duplicate key·unknown field·invalid UTF-8·64KiB 초과 입력을 거부하는 strict telemetry manifest decoder
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
- HTTP Cloud Storage client와 read-only scope를 직접 소유하는 artifact inventory reader factory
- `Versions:true`와 `SoftDeleted:true`를 분리한 exact `Prefix`·`MatchGlob` generation inventory와 bounded `limit+1` 관찰
- exact generation+metageneration precondition, manifest/raw compressed flag 분리, `maxBytes+1` read와 typed provider error 경계
- generation-pinned classifier orchestration, manifest/raw cross-lineage 검증과 raw codec·validator registry
- classifier 결과를 소비하는 bounded single-receipt reconciler와 attempt completion/failure
- tenant-scoped due query, deterministic pagination, fixed-cutoff advisory checkpoint와 transactional claim을 합성한 bounded outer worker component
- cleanup first claim·expired takeover transaction adapter와 concurrent winner·rollback 회귀 검증
- exact cleanup receipt·started attempt·fence를 다시 확인하는 cleanup read authorizer, request와 mutable classification output 전체의 evidence seal
- attempt ID path에 exact generation/hash·bounded inventory를 create-once로 고정하는 cleanup dry-run target transaction, concurrent created/replayed 수렴·conflict write-zero와 client Rules deny
- concrete Firestore current target snapshot만 발급하는 30초 이하 cleanup delete grant와 zero/forged/stale fence 거부
- raw-first exact generation+metageneration conditional delete, regular/soft-deleted complete-empty audit와 raw-only/manifest-only counterpart path 확인
- delete·inspect 404 분리, timeout/cancel/unavailable 재감사 후 manifest mutation 0, permission/quota/412·soft-deleted/late generation fail-closed
- completion capability가 아닌 plan/target hash-bound non-authoritative success observation
- exact target·plan hash, receipt revision, cleanup fence, ledger revision, artifact·path·next phase를 묶는 30초 이하 Firestore current-state absence-audit grant
- private Ed25519 key를 내부 보관하고 paired verifier만 반환하는 exact-path inventory-only GCS auditor와 opaque evidence
- signed request·concrete grant binding·artifact·`ObservedAt`을 current transaction에서 재검증하는 raw·manifest absence phase persistence, exact replay write-zero와 generic progress 우회 차단
- expired current receipt·attempt·target의 historical binding과 live authority를 분리하고 7개 nonterminal phase를 persisted time에서 검증하는 progress-aware cleanup takeover
- prior progress 보존 `failed/lease_expired` closure, receipt fence·revision·attempt count +1과 pristine new attempt create의 원자 commit·duplicate rollback, immutable target·두 uniqueness index 불변
- exact target·plan·receipt revision·fence·artifact·pre-dispatch revision을 묶고 Firestore transaction `applied` winner만 non-zero single-artifact capability를 받는 durable cleanup dispatch
- raw 또는 manifest 하나의 bounded inventory·inspect·generation+metageneration conditional delete와 mutation 30초 상한·5초 outcome persistence grace, timeout/cancel/unavailable/response-unverifiable의 durable `unknown` 보존
- durable outcome 뒤 known result만 paired signed absence audit로 전진하고 raw absence 전 manifest 호출을 0으로 유지하는 cleanup phase executor. Replay dispatch는 `dispatch_pending`, 성공은 `ready_for_finalization`에서 정지
- exact `manifest_absence_confirmed/revision 7`, non-unknown outcome·fresh evidence와 immutable fence deadline만 받는 cleanup expiry finalizer
- attempt `completed/outcome=expired`, receipt `expired`와 receipt·두 uniqueness index의 같은 `purge_eligible_at`을 한 transaction으로 commit하고 immutable target을 보존하는 terminal 경계
- commit response loss에서 mutation을 반복하지 않고 terminal historical plan·evidence·purge를 재계산해 `committed|not_committed|unverifiable`만 반환하는 short-lived read-only correlation
- 10개 bounded cleanup error class의 exhaustive retry·hold policy와 durable `unknown` error-class persistence
- 실제 마지막 phase/revision을 보존한 `completed/cleanup_retry|cleanup_hold`, attempt+receipt 2문서 atomic commit과 immutable target·두 uniqueness index write 0
- cleanup 전용 receipt cursor, old fence expiry를 포함한 exact-boundary retry claim·pristine attempt, hold auto-claim 0과 commit-response-loss read-only correlation

아직 구현하지 않은 production 운영 경계:

- scheduler·startup composition과 실제 metrics exporter를 포함한 bounded sweeper runtime
- [ADR-0031](../../docs/decisions/ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md)까지 local adapter로 구현했지만 phase executor의 typed-error→disposition/terminal call wiring, operator hold release와 nested ledger purge는 미구현
- accepted deletion auditor, held/rejected cleanup과 auditor key rotation·cross-process lifecycle. Immutable target은 execution state로 갱신하지 않고 target 생성 뒤 renewal도 허용하지 않음
- staging bucket IAM·lifecycle·retention·soft-delete policy와 실제 삭제 drill

현재 absence evidence는 원자적 Cloud Storage snapshot이 아니다. HTTP reader가 regular generation과 soft-deleted generation을 순차 조회하므로 두 호출 사이의 out-of-band writer race가 남는다. Post-quiescence application writer fencing과 staging의 least-privilege IAM/write exclusion을 검증하기 전에는 production readiness 또는 point-in-time proof로 해석하지 않는다.

verifier, authorization/admission transaction, artifact store, artifact inventory reader와 recovery control plane이 아직 `cmd/server`에 주입되지 않아 현재 executable은 `/healthz`만 `200`으로 응답하고 `/readyz`와 ingest는 `503 adapters_unconfigured`로 닫힙니다. Firestore transaction과 bounded candidate/checkpoint component는 local Emulator에서, Storage generation/replay는 official testbench에서 검증했지만 ADC/IAM·staging lifecycle·production composite index 증거는 아닙니다. Candidate와 advisory checkpoint는 artifact 권한이 아니며 fresh transaction claim의 검증된 `Acquired`만 single-receipt reconciler 진입을 허용합니다. `status`·`next_recovery_at` 누락 receipt는 due query에 보이지 않아 별도 control-integrity audit가 필요합니다. Scheduler/startup과 metrics exporter를 연결하기 전에는 worker를 활성화하지 않습니다. production factory guard도 server startup path가 연결되기 전에는 활성 runtime guard가 아닙니다. 인증 우회 local mode는 제공하지 않습니다. Firestore에는 GPS sample을 개별 document로 쓰지 않습니다.

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

Cloud Storage의 `DoesNotExist`, generation-pinned compressed-byte replay와 exact generation+metageneration cleanup delete backend는 pinned official Storage testbench에서 별도 integration test로 검증합니다. Testbench delete는 synthetic object에만 적용하며 versioning·soft-delete inventory 의미와 full executor는 staging 증거가 아닙니다. 일반 `go test`에서는 `STORAGE_EMULATOR_HOST`가 없으면 해당 case만 skip합니다.

Docker build context는 프로젝트 root이며 `.dockerignore` allowlist가 gateway `cmd`·`internal` source, Go module 파일과 synthetic contract JSON fixture만 전달합니다. 패키지 단위 allowlist이므로 새 Go 파일이 host CI에는 보이지만 image에서 조용히 누락되는 구조를 피합니다.

Firestore Emulator가 WSL host의 `8080`을 사용하므로 gateway는 host `8085`로 노출합니다. container 내부와 Cloud Run의 `PORT`는 `8080`을 유지합니다.

```bash
rtk docker run --rm -p 127.0.0.1:8085:8080 mobility-telemetry-gateway:dev
```

host Go로 직접 실행하는 환경에서는 `PORT=8085`를 명시합니다.
