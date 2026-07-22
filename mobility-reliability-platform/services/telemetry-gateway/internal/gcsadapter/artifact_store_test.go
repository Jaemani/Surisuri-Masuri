package gcsadapter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestArtifactStoreWritesRawThenCanonicalManifest(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1,2]}`))

	stored, err := store.StoreBatch(context.Background(), write)
	if err != nil {
		t.Fatalf("StoreBatch() error = %v", err)
	}
	if stored.Object.Replay || stored.Manifest.Replay {
		t.Fatalf("new artifact marked replay = %#v", stored)
	}
	if got, want := backend.createOrder, []string{write.ObjectPath, write.ManifestPath}; !equalStrings(got, want) {
		t.Fatalf("create order = %#v, want %#v", got, want)
	}
	if stored.Object.SHA256 != ingest.ComputeArtifactDigest(write.CompressedBody).SHA256 ||
		stored.Object.Generation <= 0 || stored.Manifest.Generation <= 0 {
		t.Fatalf("stored lineage = %#v", stored)
	}

	manifestBytes := backend.contentAt(t, write.ManifestPath)
	var manifest ingest.TelemetryManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ObjectSHA256 != stored.Object.SHA256 ||
		manifest.ObjectCRC32C != stored.Object.CRC32C ||
		manifest.ObjectSize != stored.Object.Size ||
		manifest.ObjectGeneration != stored.Object.Generation ||
		manifest.ObjectMetageneration != stored.Object.Metageneration {
		t.Fatalf("manifest object lineage = %#v, stored = %#v", manifest, stored.Object)
	}
	if backend.objects[write.ObjectPath].spec.ContentEncoding != ingest.TelemetryCompression ||
		backend.objects[write.ManifestPath].spec.ContentEncoding != "" {
		t.Fatal("raw and manifest content encoding contract diverged")
	}
}

func TestNewArtifactStoreRejectsNilBucket(t *testing.T) {
	store, err := NewArtifactStore(nil)
	if err == nil || store != nil {
		t.Fatalf("NewArtifactStore(nil) = %#v, %v", store, err)
	}
}

func TestArtifactStoreCreatesOnlyRecoveryManifest(t *testing.T) {
	backend := newMemoryBackend()
	now, write, grant := recoveryManifestFixture(t)
	store := recoveryManifestTestStore(&ArtifactStore{
		backend: backend, recoveryReader: backend,
		now: func() time.Time { return now.Add(time.Second) },
	}, write, now.Add(30*time.Second))

	stored, err := store.CreateManifest(context.Background(), grant, write)
	if err != nil {
		t.Fatalf("CreateManifest() = %v", err)
	}
	if stored.Replay || stored.Path != write.ManifestPath {
		t.Fatalf("stored = %#v", stored)
	}
	if got, want := backend.createOrder, []string{write.ManifestPath}; !equalStrings(got, want) {
		t.Fatalf("create order = %#v, want %#v", got, want)
	}
	if _, rawCreated := backend.objects[write.Raw.Path]; rawCreated {
		t.Fatal("manifest recovery created or rewrote raw path")
	}
	if got := backend.contentAt(t, write.ManifestPath); !bytes.Equal(got, write.CanonicalBody) {
		t.Fatal("stored recovery manifest differs from authorized canonical bytes")
	}
}

func TestArtifactStoreRecoveryManifestExactReplayUsesCompleteInventory(t *testing.T) {
	backend := newMemoryBackend()
	now, write, grant := recoveryManifestFixture(t)
	store := recoveryManifestTestStore(&ArtifactStore{
		backend: backend, recoveryReader: backend,
		now: func() time.Time { return now.Add(time.Second) },
	}, write, now.Add(30*time.Second))
	first, err := store.CreateManifest(context.Background(), grant, write)
	if err != nil {
		t.Fatalf("first CreateManifest() = %v", err)
	}
	second, err := store.CreateManifest(context.Background(), grant, write)
	if err != nil {
		t.Fatalf("replay CreateManifest() = %v", err)
	}
	if !second.Replay || second.Generation != first.Generation || second.Metageneration != first.Metageneration {
		t.Fatalf("replay lineage = first:%#v second:%#v", first, second)
	}
	if backend.inventoryCalls != 2 {
		t.Fatalf("inventory calls = %d, want pre/post 2", backend.inventoryCalls)
	}
	if got, want := backend.createOrder, []string{write.ManifestPath, write.ManifestPath}; !equalStrings(got, want) {
		t.Fatalf("create order = %#v, want %#v", got, want)
	}
}

func TestArtifactStoreRecoveryManifestResolvesAmbiguousCreateOnlyByExactReplay(t *testing.T) {
	backend := newMemoryBackend()
	now, write, grant := recoveryManifestFixture(t)
	store := recoveryManifestTestStore(&ArtifactStore{
		backend:        &ambiguousCreateBackend{memoryBackend: backend, commit: true},
		recoveryReader: backend,
		now:            func() time.Time { return now.Add(time.Second) },
	}, write, now.Add(30*time.Second))
	stored, err := store.CreateManifest(context.Background(), grant, write)
	if err != nil {
		t.Fatalf("CreateManifest() = %v", err)
	}
	if !stored.Replay || len(backend.objects) != 1 {
		t.Fatalf("ambiguous replay = %#v, objects=%d", stored, len(backend.objects))
	}

	beforeCommit := newMemoryBackend()
	store = recoveryManifestTestStore(&ArtifactStore{
		backend:        &ambiguousCreateBackend{memoryBackend: beforeCommit, commit: false},
		recoveryReader: beforeCommit,
		now:            func() time.Time { return now.Add(time.Second) },
	}, write, now.Add(30*time.Second))
	_, err = store.CreateManifest(context.Background(), grant, write)
	if !errors.Is(err, ingest.ErrArtifactUnavailable) || len(beforeCommit.objects) != 0 {
		t.Fatalf("pre-commit timeout = %v, objects=%d", err, len(beforeCommit.objects))
	}
}

func TestArtifactStoreRecoveryManifestNeverOverwritesDivergentReplay(t *testing.T) {
	backend := newMemoryBackend()
	now, write, grant := recoveryManifestFixture(t)
	store := recoveryManifestTestStore(&ArtifactStore{
		backend: backend, recoveryReader: backend,
		now: func() time.Time { return now.Add(time.Second) },
	}, write, now.Add(30*time.Second))
	if _, err := store.CreateManifest(context.Background(), grant, write); err != nil {
		t.Fatalf("seed CreateManifest() = %v", err)
	}
	record := backend.objects[write.ManifestPath]
	record.content = append([]byte(nil), write.CanonicalBody...)
	record.content[len(record.content)-1] ^= 0xff
	record.digest = ingest.ComputeArtifactDigest(record.content)
	record.snapshot.CRC32C = record.digest.CRC32C
	record.snapshot.Size = record.digest.Size
	record.snapshot.Metadata["sha256"] = record.digest.SHA256
	backend.objects[write.ManifestPath] = record

	_, err := store.CreateManifest(context.Background(), grant, write)
	if !errors.Is(err, ingest.ErrArtifactConflict) || !errors.Is(err, ingest.ErrManifestArtifactConflict) {
		t.Fatalf("CreateManifest() = %v", err)
	}
	if len(backend.objects) != 1 || len(backend.createOrder) != 2 {
		t.Fatalf("divergent replay changed object set: objects=%d creates=%#v", len(backend.objects), backend.createOrder)
	}
}

func TestArtifactStoreRecoveryManifestRejectsAmbiguousVersionInventory(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(int, ingest.GenerationInventory) ingest.GenerationInventory
	}{
		{name: "multiple live generations", mutate: func(_ int, inventory ingest.GenerationInventory) ingest.GenerationInventory {
			extra := inventory.NonSoftDeleted.Candidates[0]
			extra.Generation++
			inventory.NonSoftDeleted.Candidates = append(inventory.NonSoftDeleted.Candidates, extra)
			return inventory
		}},
		{name: "soft deleted candidate", mutate: func(_ int, inventory ingest.GenerationInventory) ingest.GenerationInventory {
			soft := inventory.NonSoftDeleted.Candidates[0]
			soft.Generation++
			soft.SoftDeleted = true
			inventory.SoftDeleted.Candidates = []ingest.ArtifactSnapshot{soft}
			return inventory
		}},
		{name: "inventory changes after read", mutate: func(call int, inventory ingest.GenerationInventory) ingest.GenerationInventory {
			if call == 2 {
				inventory.NonSoftDeleted.Candidates[0].Metageneration++
			}
			return inventory
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newMemoryBackend()
			now, write, grant := recoveryManifestFixture(t)
			store := recoveryManifestTestStore(&ArtifactStore{
				backend: backend, recoveryReader: backend,
				now: func() time.Time { return now.Add(time.Second) },
			}, write, now.Add(30*time.Second))
			if _, err := store.CreateManifest(context.Background(), grant, write); err != nil {
				t.Fatalf("seed CreateManifest() = %v", err)
			}
			reader := &inventoryOverrideReader{memoryBackend: backend, mutate: test.mutate}
			store.recoveryReader = reader
			_, err := store.CreateManifest(context.Background(), grant, write)
			if !errors.Is(err, ingest.ErrArtifactResponseUnverifiable) {
				t.Fatalf("CreateManifest() = %v", err)
			}
			if len(backend.objects) != 1 || len(backend.createOrder) != 2 {
				t.Fatalf("ambiguous inventory changed object set: objects=%d creates=%#v", len(backend.objects), backend.createOrder)
			}
		})
	}
}

func TestArtifactStoreRecoveryManifestFailsClosedBeforeProviderIO(t *testing.T) {
	now, write, grant := recoveryManifestFixture(t)
	tests := []struct {
		name              string
		grant             ingest.ManifestRepairAuthorizationGrant
		write             ingest.RecoveryManifestWrite
		at                time.Time
		useTestCapability bool
		want              error
	}{
		{
			name: "zero grant", grant: ingest.ManifestRepairAuthorizationGrant{}, write: write,
			at: now.Add(time.Second), want: ingest.ErrInvalidManifestRepairAuthorization,
		},
		{
			name: "expired grant", grant: grant, write: write, at: now.Add(31 * time.Second),
			useTestCapability: true, want: ingest.ErrManifestRepairAuthorizationExpired,
		},
		{
			name: "mutated body", grant: grant, write: mutateRecoveryManifestBody(write),
			at: now.Add(time.Second), useTestCapability: true,
			want: ingest.ErrInvalidManifestRepairAuthorization,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			local := newMemoryBackend()
			store := &ArtifactStore{
				backend: local, recoveryReader: local,
				now: func() time.Time { return test.at },
			}
			if test.useTestCapability {
				store = recoveryManifestTestStore(store, write, now.Add(30*time.Second))
			}
			if _, err := store.CreateManifest(context.Background(), test.grant, test.write); !errors.Is(err, test.want) {
				t.Fatalf("CreateManifest() = %v, want %v", err, test.want)
			}
			if len(local.createOrder) != 0 || local.inventoryCalls != 0 || len(local.reads) != 0 {
				t.Fatalf("invalid capability reached provider: creates=%#v inventory=%d reads=%#v", local.createOrder, local.inventoryCalls, local.reads)
			}
		})
	}
}

func TestArtifactStoreRecoveryManifestKeepsProviderPermissionDistinct(t *testing.T) {
	backend := newMemoryBackend()
	now, write, grant := recoveryManifestFixture(t)
	backend.failCreate[write.ManifestPath] = []error{&googleapi.Error{Code: 403, Message: "secret detail"}}
	store := recoveryManifestTestStore(&ArtifactStore{
		backend: backend, recoveryReader: backend,
		now: func() time.Time { return now.Add(time.Second) },
	}, write, now.Add(30*time.Second))
	_, err := store.CreateManifest(context.Background(), grant, write)
	if !errors.Is(err, ingest.ErrArtifactPermissionDenied) || strings.Contains(err.Error(), "secret detail") {
		t.Fatalf("CreateManifest() = %v", err)
	}
	if backend.inventoryCalls != 0 || len(backend.objects) != 0 {
		t.Fatalf("permission denial triggered replay or mutation: inventory=%d objects=%d", backend.inventoryCalls, len(backend.objects))
	}
}

func TestArtifactStoreRecoveryManifestDistinguishesProviderCancellation(t *testing.T) {
	backend := newMemoryBackend()
	now, write, grant := recoveryManifestFixture(t)
	backend.failCreate[write.ManifestPath] = []error{context.Canceled}
	store := recoveryManifestTestStore(&ArtifactStore{
		backend: backend, recoveryReader: backend,
		now: func() time.Time { return now.Add(time.Second) },
	}, write, now.Add(30*time.Second))

	_, err := store.CreateManifest(context.Background(), grant, write)
	if !errors.Is(err, ingest.ErrArtifactProviderCancelled) || errors.Is(err, context.Canceled) {
		t.Fatalf("CreateManifest() = %v", err)
	}
	if backend.inventoryCalls != 0 || len(backend.objects) != 0 {
		t.Fatalf("provider cancellation triggered replay or mutation: inventory=%d objects=%d", backend.inventoryCalls, len(backend.objects))
	}
}

func TestArtifactStoreRecoveryManifestPreservesCallerCancellation(t *testing.T) {
	t.Run("before provider call", func(t *testing.T) {
		backend := newMemoryBackend()
		_, write, grant := recoveryManifestFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := (&ArtifactStore{backend: backend, recoveryReader: backend}).CreateManifest(ctx, grant, write)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ingest.ErrArtifactProviderCancelled) {
			t.Fatalf("CreateManifest() = %v", err)
		}
		if len(backend.createOrder) != 0 || backend.inventoryCalls != 0 {
			t.Fatalf("pre-cancelled call reached provider: creates=%#v inventory=%d", backend.createOrder, backend.inventoryCalls)
		}
	})

	t.Run("during provider call", func(t *testing.T) {
		backend := newMemoryBackend()
		now, write, grant := recoveryManifestFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		store := recoveryManifestTestStore(&ArtifactStore{
			backend:        &cancelingCreateBackend{memoryBackend: backend, cancel: cancel},
			recoveryReader: backend,
			now:            func() time.Time { return now.Add(time.Second) },
		}, write, now.Add(30*time.Second))

		_, err := store.CreateManifest(ctx, grant, write)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ingest.ErrArtifactProviderCancelled) {
			t.Fatalf("CreateManifest() = %v", err)
		}
		if backend.inventoryCalls != 0 || len(backend.objects) != 0 {
			t.Fatalf("caller cancellation triggered replay or mutation: inventory=%d objects=%d", backend.inventoryCalls, len(backend.objects))
		}
	})
}

func TestArtifactStoreRecoveryManifestRejectsLateCreateSuccessAfterAuthorizationExpiry(t *testing.T) {
	backend := newMemoryBackend()
	now, write, grant := recoveryManifestFixture(t)
	store := recoveryManifestTestStore(&ArtifactStore{
		backend: backend, recoveryReader: backend,
		now: scriptedClock(now.Add(time.Second), now.Add(31*time.Second)),
	}, write, now.Add(30*time.Second))

	stored, err := store.CreateManifest(context.Background(), grant, write)
	if !errors.Is(err, ingest.ErrManifestRepairAuthorizationExpired) {
		t.Fatalf("CreateManifest() = %#v, %v", stored, err)
	}
	if stored != (ingest.StoredArtifact{}) {
		t.Fatalf("expired late success returned trusted lineage = %#v", stored)
	}
	if len(backend.createOrder) != 1 || backend.inventoryCalls != 0 {
		t.Fatalf("late success caused additional provider work: creates=%#v inventory=%d", backend.createOrder, backend.inventoryCalls)
	}
	if _, committed := backend.objects[write.ManifestPath]; !committed {
		t.Fatal("test did not exercise a provider commit racing capability expiry")
	}
}

func TestArtifactStoreRecoveryManifestStopsReplayAtAuthorizationExpiry(t *testing.T) {
	backend := newMemoryBackend()
	now, write, grant := recoveryManifestFixture(t)
	seed := recoveryManifestTestStore(&ArtifactStore{
		backend: backend, recoveryReader: backend,
		now: func() time.Time { return now.Add(time.Second) },
	}, write, now.Add(30*time.Second))
	if _, err := seed.CreateManifest(context.Background(), grant, write); err != nil {
		t.Fatalf("seed CreateManifest() = %v", err)
	}

	store := recoveryManifestTestStore(&ArtifactStore{
		backend: backend, recoveryReader: backend,
		now: scriptedClock(
			now.Add(time.Second),
			now.Add(time.Second),
			now.Add(time.Second),
			now.Add(31*time.Second),
		),
	}, write, now.Add(30*time.Second))
	_, err := store.CreateManifest(context.Background(), grant, write)
	if !errors.Is(err, ingest.ErrManifestRepairAuthorizationExpired) {
		t.Fatalf("CreateManifest() = %v", err)
	}
	if backend.inventoryCalls != 1 || len(backend.reads) != 0 {
		t.Fatalf("expired replay crossed a later read boundary: inventory=%d reads=%#v", backend.inventoryCalls, backend.reads)
	}
	if len(backend.createOrder) != 2 || len(backend.objects) != 1 {
		t.Fatalf("expired replay changed immutable object set: creates=%#v objects=%d", backend.createOrder, len(backend.objects))
	}
}

func TestArtifactStoreExactReplayPinsGenerationAndCompressedBytes(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	first, err := store.StoreBatch(context.Background(), write)
	if err != nil {
		t.Fatalf("first StoreBatch() error = %v", err)
	}

	second, err := store.StoreBatch(context.Background(), write)
	if err != nil {
		t.Fatalf("replay StoreBatch() error = %v", err)
	}
	if !second.Object.Replay || !second.Manifest.Replay {
		t.Fatalf("replay flags = %#v", second)
	}
	if second.Object.Generation != first.Object.Generation ||
		second.Manifest.Generation != first.Manifest.Generation {
		t.Fatalf("replay changed generation: first=%#v second=%#v", first, second)
	}
	if got, want := backend.reads, []generationRead{
		{path: write.ObjectPath, generation: first.Object.Generation, compressed: true},
		{path: write.ManifestPath, generation: first.Manifest.Generation, compressed: false},
	}; !equalReads(got, want) {
		t.Fatalf("generation reads = %#v, want %#v", got, want)
	}
}

func TestArtifactStoreRecoversRawSuccessThenManifestFailure(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	backend.failCreate[write.ManifestPath] = []error{errors.New("temporary provider failure")}

	_, err := store.StoreBatch(context.Background(), write)
	if !errors.Is(err, ingest.ErrArtifactUnavailable) {
		t.Fatalf("first StoreBatch() error = %v", err)
	}
	if _, exists := backend.objects[write.ObjectPath]; !exists {
		t.Fatal("raw object did not survive manifest failure")
	}
	if _, exists := backend.objects[write.ManifestPath]; exists {
		t.Fatal("manifest unexpectedly exists after injected failure")
	}

	stored, err := store.StoreBatch(context.Background(), write)
	if err != nil {
		t.Fatalf("recovery StoreBatch() error = %v", err)
	}
	if !stored.Object.Replay || stored.Manifest.Replay {
		t.Fatalf("partial recovery replay flags = %#v", stored)
	}
}

func TestArtifactStoreRejectsExistingDifferentRawLineage(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	first := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	if _, err := store.StoreBatch(context.Background(), first); err != nil {
		t.Fatalf("seed StoreBatch() error = %v", err)
	}

	conflict := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[2]}`))
	_, err := store.StoreBatch(context.Background(), conflict)
	if !errors.Is(err, ingest.ErrArtifactConflict) || !errors.Is(err, ingest.ErrRawArtifactConflict) ||
		errors.Is(err, ingest.ErrManifestArtifactConflict) {
		t.Fatalf("conflict StoreBatch() error = %v", err)
	}
	if len(backend.objects) != 2 {
		t.Fatalf("conflict changed object count = %d", len(backend.objects))
	}
}

func TestArtifactStoreKeepsManifestConflictDistinctFromRawConflict(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	if _, err := store.StoreBatch(context.Background(), write); err != nil {
		t.Fatalf("seed StoreBatch() error = %v", err)
	}
	record := backend.objects[write.ManifestPath]
	record.snapshot.Metadata["sha256"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	backend.objects[write.ManifestPath] = record

	_, err := store.StoreBatch(context.Background(), write)
	if !errors.Is(err, ingest.ErrArtifactConflict) || !errors.Is(err, ingest.ErrManifestArtifactConflict) ||
		errors.Is(err, ingest.ErrRawArtifactConflict) {
		t.Fatalf("manifest conflict classification = %v", err)
	}
}

func TestArtifactStoreFailsClosedOnCorruptStoredAttributes(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	if _, err := store.StoreBatch(context.Background(), write); err != nil {
		t.Fatalf("seed StoreBatch() error = %v", err)
	}
	record := backend.objects[write.ObjectPath]
	record.snapshot.Metageneration = 0
	backend.objects[write.ObjectPath] = record

	_, err := store.StoreBatch(context.Background(), write)
	if !errors.Is(err, ingest.ErrArtifactUnavailable) {
		t.Fatalf("corrupt replay error = %v", err)
	}
}

func TestArtifactStoreRejectsMissingNoStoreOnReplay(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	if _, err := store.StoreBatch(context.Background(), write); err != nil {
		t.Fatalf("seed StoreBatch() error = %v", err)
	}
	record := backend.objects[write.ObjectPath]
	record.snapshot.CacheControl = ""
	backend.objects[write.ObjectPath] = record

	_, err := store.StoreBatch(context.Background(), write)
	if !errors.Is(err, ingest.ErrArtifactConflict) || errors.Is(err, ingest.ErrRawArtifactConflict) {
		t.Fatalf("missing no-store replay error = %v", err)
	}
}

func TestArtifactStoreAcceptsOnlyVerifiedTestbenchMetadata(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	first, err := store.StoreBatch(context.Background(), write)
	if err != nil {
		t.Fatalf("seed StoreBatch() error = %v", err)
	}
	record := backend.objects[write.ObjectPath]
	record.snapshot.Metadata["x_emulator_crc32c"] = encodedCRC32C(first.Object.CRC32C)
	record.snapshot.Metadata["x_emulator_upload"] = "multipart"
	record.snapshot.Metadata["x_testbench_crc32c"] = encodedCRC32C(first.Object.CRC32C)
	record.snapshot.Metadata["x_testbench_upload"] = "multipart"
	backend.objects[write.ObjectPath] = record

	if _, err := store.StoreBatch(context.Background(), write); err != nil {
		t.Fatalf("verified testbench metadata replay = %v", err)
	}

	record = backend.objects[write.ObjectPath]
	record.snapshot.Metadata["unknown_provider_key"] = "unverified"
	backend.objects[write.ObjectPath] = record
	_, err = store.StoreBatch(context.Background(), write)
	if !errors.Is(err, ingest.ErrArtifactConflict) || errors.Is(err, ingest.ErrRawArtifactConflict) {
		t.Fatalf("unknown metadata classification = %v", err)
	}
}

func TestArtifactStoreRejectsIncorrectTestbenchMetadataValue(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	if _, err := store.StoreBatch(context.Background(), write); err != nil {
		t.Fatalf("seed StoreBatch() error = %v", err)
	}
	record := backend.objects[write.ObjectPath]
	record.snapshot.Metadata["x_testbench_crc32c"] = "AAAAAA=="
	backend.objects[write.ObjectPath] = record

	_, err := store.StoreBatch(context.Background(), write)
	if !errors.Is(err, ingest.ErrArtifactConflict) || errors.Is(err, ingest.ErrRawArtifactConflict) {
		t.Fatalf("incorrect testbench metadata classification = %v", err)
	}
}

func TestArtifactStoreFailsClosedOnMetagenerationDriftDuringReplay(t *testing.T) {
	backend := newMemoryBackend()
	store := &ArtifactStore{backend: backend}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1]}`))
	if _, err := store.StoreBatch(context.Background(), write); err != nil {
		t.Fatalf("seed StoreBatch() error = %v", err)
	}
	store.backend = &metagenerationDriftBackend{memoryBackend: backend, driftPath: write.ObjectPath}

	_, err := store.StoreBatch(context.Background(), write)
	if !errors.Is(err, ingest.ErrArtifactUnavailable) || errors.Is(err, ingest.ErrRawArtifactConflict) {
		t.Fatalf("metageneration drift classification = %v", err)
	}
}

func TestArtifactStoreValidatesGzipBodyHashAndPathsBeforeWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ingest.BatchArtifactWrite)
	}{
		{name: "invalid gzip", mutate: func(write *ingest.BatchArtifactWrite) {
			write.CompressedBody = []byte("not-gzip")
		}},
		{name: "body hash mismatch", mutate: func(write *ingest.BatchArtifactWrite) {
			write.Manifest.BodyHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
		{name: "raw path mismatch", mutate: func(write *ingest.BatchArtifactWrite) {
			write.ObjectPath = "telemetry/v2/other.json.gz"
		}},
		{name: "manifest path mismatch", mutate: func(write *ingest.BatchArtifactWrite) {
			write.ManifestPath = "telemetry-manifests/v2/other.json"
		}},
		{name: "invalid manifest lineage", mutate: func(write *ingest.BatchArtifactWrite) {
			write.Manifest.TenantID = "not-a-uuid"
			write.ObjectPath = ingest.ExpectedTelemetryObjectPath(write.Manifest)
			write.ManifestPath = ingest.ExpectedTelemetryManifestPath(write.Manifest)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newMemoryBackend()
			store := &ArtifactStore{backend: backend}
			write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2"}`))
			test.mutate(&write)
			_, err := store.StoreBatch(context.Background(), write)
			if !errors.Is(err, ingest.ErrInvalidArtifactManifest) {
				t.Fatalf("StoreBatch() error = %v", err)
			}
			if len(backend.createOrder) != 0 {
				t.Fatalf("invalid write reached backend = %#v", backend.createOrder)
			}
		})
	}
}

func TestPreconditionClassificationIsNarrow(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "HTTP 412", err: &googleapi.Error{Code: 412}, want: true},
		{name: "wrapped HTTP 412", err: fmt.Errorf("upload: %w", &googleapi.Error{Code: 412}), want: true},
		{name: "gRPC failed precondition", err: status.Error(codes.FailedPrecondition, "condition"), want: true},
		{name: "HTTP conflict is not precondition", err: &googleapi.Error{Code: 409}, want: false},
		{name: "gRPC already exists is not precondition", err: status.Error(codes.AlreadyExists, "exists"), want: false},
		{name: "generic", err: errors.New("failed"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isPreconditionFailure(test.err); got != test.want {
				t.Fatalf("isPreconditionFailure() = %v, want %v", got, test.want)
			}
		})
	}
}

type memoryObject struct {
	content  []byte
	digest   ingest.ArtifactDigest
	spec     objectWriteSpec
	snapshot objectSnapshot
}

type generationRead struct {
	path       string
	generation int64
	compressed bool
}

type memoryBackend struct {
	objects        map[string]memoryObject
	failCreate     map[string][]error
	createOrder    []string
	reads          []generationRead
	nextGen        int64
	inventoryCalls int
}

type metagenerationDriftBackend struct {
	*memoryBackend
	driftPath string
}

type ambiguousCreateBackend struct {
	*memoryBackend
	commit bool
}

type cancelingCreateBackend struct {
	*memoryBackend
	cancel context.CancelFunc
}

type inventoryOverrideReader struct {
	*memoryBackend
	mutate func(int, ingest.GenerationInventory) ingest.GenerationInventory
	calls  int
}

func (r *inventoryOverrideReader) ListExactPathGenerations(
	ctx context.Context,
	path string,
	limit int,
) (ingest.GenerationInventory, error) {
	inventory, err := r.memoryBackend.ListExactPathGenerations(ctx, path, limit)
	if err != nil {
		return inventory, err
	}
	r.calls++
	return r.mutate(r.calls, inventory), nil
}

func (b *ambiguousCreateBackend) Create(
	ctx context.Context,
	path string,
	content []byte,
	digest ingest.ArtifactDigest,
	spec objectWriteSpec,
) (objectSnapshot, error) {
	if b.commit {
		_, _ = b.memoryBackend.Create(ctx, path, content, digest, spec)
	} else {
		b.createOrder = append(b.createOrder, path)
	}
	return objectSnapshot{}, &googleapi.Error{Code: 504, Message: "ambiguous upload"}
}

func (b *cancelingCreateBackend) Create(
	_ context.Context,
	_ string,
	_ []byte,
	_ ingest.ArtifactDigest,
	_ objectWriteSpec,
) (objectSnapshot, error) {
	b.cancel()
	return objectSnapshot{}, context.Canceled
}

func (b *metagenerationDriftBackend) ReadGeneration(
	ctx context.Context,
	path string,
	generation int64,
	compressed bool,
	limit int64,
) ([]byte, error) {
	content, err := b.memoryBackend.ReadGeneration(ctx, path, generation, compressed, limit)
	if err == nil && path == b.driftPath {
		record := b.objects[path]
		record.snapshot.Metageneration++
		b.objects[path] = record
	}
	return content, err
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{
		objects:    make(map[string]memoryObject),
		failCreate: make(map[string][]error),
		nextGen:    100,
	}
}

func (b *memoryBackend) Create(
	_ context.Context,
	path string,
	content []byte,
	digest ingest.ArtifactDigest,
	spec objectWriteSpec,
) (objectSnapshot, error) {
	b.createOrder = append(b.createOrder, path)
	if failures := b.failCreate[path]; len(failures) > 0 {
		b.failCreate[path] = failures[1:]
		return objectSnapshot{}, failures[0]
	}
	if _, exists := b.objects[path]; exists {
		return objectSnapshot{}, &googleapi.Error{Code: 412, Message: "conditionNotMet"}
	}
	b.nextGen++
	snapshot := objectSnapshot{
		Path:            path,
		CRC32C:          digest.CRC32C,
		Size:            digest.Size,
		Generation:      b.nextGen,
		Metageneration:  1,
		ContentType:     spec.ContentType,
		ContentEncoding: spec.ContentEncoding,
		CacheControl:    spec.CacheControl,
		Metadata:        cloneMetadata(spec.Metadata),
	}
	b.objects[path] = memoryObject{
		content:  bytes.Clone(content),
		digest:   digest,
		spec:     spec,
		snapshot: snapshot,
	}
	return snapshot, nil
}

func (b *memoryBackend) Inspect(
	_ context.Context,
	path string,
	generation int64,
) (objectSnapshot, error) {
	record, exists := b.objects[path]
	if !exists || generation > 0 && generation != record.snapshot.Generation {
		return objectSnapshot{}, errors.New("not found")
	}
	return record.snapshot, nil
}

func (b *memoryBackend) ReadGeneration(
	_ context.Context,
	path string,
	generation int64,
	compressed bool,
	limit int64,
) ([]byte, error) {
	record, exists := b.objects[path]
	if !exists || generation != record.snapshot.Generation {
		return nil, errors.New("not found")
	}
	b.reads = append(b.reads, generationRead{path: path, generation: generation, compressed: compressed})
	content := record.content
	if int64(len(content)) > limit {
		content = content[:limit]
	}
	return bytes.Clone(content), nil
}

func (b *memoryBackend) ListExactPathGenerations(
	_ context.Context,
	path string,
	_ int,
) (ingest.GenerationInventory, error) {
	b.inventoryCalls++
	inventory := ingest.GenerationInventory{
		Coverage:       ingest.ArtifactInventoryCoverageComplete,
		NonSoftDeleted: ingest.ArtifactGenerationSet{Performed: true},
		SoftDeleted:    ingest.ArtifactGenerationSet{Performed: true},
	}
	if record, exists := b.objects[path]; exists {
		inventory.NonSoftDeleted.Candidates = []ingest.ArtifactSnapshot{
			artifactSnapshotFromMemoryObject(record),
		}
	}
	return inventory, nil
}

func (b *memoryBackend) InspectGeneration(
	ctx context.Context,
	path string,
	generation int64,
) (ingest.ArtifactSnapshot, error) {
	snapshot, err := b.Inspect(ctx, path, generation)
	if err != nil {
		return ingest.ArtifactSnapshot{}, ingest.ErrArtifactGenerationNotFound
	}
	record := b.objects[path]
	record.snapshot = snapshot
	return artifactSnapshotFromMemoryObject(record), nil
}

func (b *memoryBackend) ReadManifestGeneration(
	ctx context.Context,
	target ingest.ArtifactTarget,
	maxBytes int64,
) ([]byte, error) {
	content, err := b.ReadGeneration(ctx, target.Path, target.Generation, false, maxBytes+1)
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, ingest.ErrArtifactReadLimitExceeded
	}
	return content, nil
}

func (b *memoryBackend) ReadRawGenerationCompressed(
	ctx context.Context,
	target ingest.ArtifactTarget,
	maxBytes int64,
) ([]byte, error) {
	content, err := b.ReadGeneration(ctx, target.Path, target.Generation, true, maxBytes+1)
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, ingest.ErrArtifactReadLimitExceeded
	}
	return content, nil
}

func artifactSnapshotFromMemoryObject(record memoryObject) ingest.ArtifactSnapshot {
	return ingest.ArtifactSnapshot{
		Path:            record.snapshot.Path,
		SHA256:          record.snapshot.Metadata["sha256"],
		CRC32C:          record.snapshot.CRC32C,
		Size:            record.snapshot.Size,
		Generation:      record.snapshot.Generation,
		Metageneration:  record.snapshot.Metageneration,
		ContentType:     record.snapshot.ContentType,
		ContentEncoding: record.snapshot.ContentEncoding,
		CacheControl:    record.snapshot.CacheControl,
		Metadata:        cloneMetadata(record.snapshot.Metadata),
	}
}

func (b *memoryBackend) contentAt(t *testing.T, path string) []byte {
	t.Helper()
	record, exists := b.objects[path]
	if !exists {
		t.Fatalf("object %q does not exist", path)
	}
	return bytes.Clone(record.content)
}

func artifactWriteFixture(t *testing.T, raw []byte) ingest.BatchArtifactWrite {
	t.Helper()
	compressed := deterministicGzip(t, raw)
	bodyHash := ingest.ComputeArtifactDigest(raw).SHA256
	receivedAt := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	manifest := ingest.BatchManifestInput{
		PayloadSchemaVersion: "telemetry-batch.v2",
		TenantID:             "11111111-1111-4111-8111-111111111111",
		DeviceID:             "22222222-2222-4222-8222-222222222222",
		TripID:               "33333333-3333-4333-8333-333333333333",
		InstallationID:       "44444444-4444-4444-8444-444444444444",
		BatchID:              "01982015-4400-7000-8000-000000000001",
		ClientBatchID:        "55555555-5555-4555-8555-555555555555",
		ConsentRevisionID:    "66666666-6666-4666-8666-666666666666",
		BodyHash:             bodyHash,
		SampleCount:          2,
		FirstCapturedAt:      receivedAt.Add(-2 * time.Minute),
		LastCapturedAt:       receivedAt.Add(-time.Minute),
		ReceivedAt:           receivedAt,
		ArtifactExpiresAt:    receivedAt.Add(ingest.TelemetryArtifactRetention),
		ValidatorVersion:     "gateway-validator@test",
	}
	return ingest.BatchArtifactWrite{
		ObjectPath:     ingest.ExpectedTelemetryObjectPath(manifest),
		ManifestPath:   ingest.ExpectedTelemetryManifestPath(manifest),
		CompressedBody: compressed,
		Manifest:       manifest,
	}
}

func recoveryManifestFixture(
	t *testing.T,
) (time.Time, ingest.RecoveryManifestWrite, ingest.ManifestRepairAuthorizationGrant) {
	t.Helper()
	now := time.Now().UTC()
	receivedAt := now.Add(-5 * time.Minute)
	input := ingest.BatchManifestInput{
		PayloadSchemaVersion: "telemetry-batch.v2",
		TenantID:             "11111111-1111-4111-8111-111111111111",
		DeviceID:             "22222222-2222-4222-8222-222222222222",
		TripID:               "33333333-3333-4333-8333-333333333333",
		InstallationID:       "44444444-4444-4444-8444-444444444444",
		BatchID:              "01982015-4400-7000-8000-000000000001",
		ClientBatchID:        "55555555-5555-4555-8555-555555555555",
		ConsentRevisionID:    "66666666-6666-4666-8666-666666666666",
		BodyHash:             strings.Repeat("a", 64),
		SampleCount:          2,
		FirstCapturedAt:      receivedAt.Add(-2 * time.Minute),
		LastCapturedAt:       receivedAt.Add(-time.Minute),
		ReceivedAt:           receivedAt,
		ArtifactExpiresAt:    receivedAt.Add(ingest.TelemetryArtifactRetention),
		ValidatorVersion:     ingest.TelemetryValidatorVersion,
	}
	raw := ingest.StoredArtifact{
		Path: ingest.ExpectedTelemetryObjectPath(input), SHA256: strings.Repeat("b", 64),
		Size: 128, Generation: 91, Metageneration: 2,
	}
	body, digest, err := ingest.CanonicalTelemetryManifest(input, raw)
	if err != nil {
		t.Fatalf("CanonicalTelemetryManifest() = %v", err)
	}
	write := ingest.RecoveryManifestWrite{
		ManifestPath: ingest.ExpectedTelemetryManifestPath(input), ManifestInput: input,
		Raw: raw, CanonicalBody: body, Digest: digest,
	}
	return now, write, ingest.ManifestRepairAuthorizationGrant{}
}

func recoveryManifestTestStore(
	store *ArtifactStore,
	expected ingest.RecoveryManifestWrite,
	expiresAt time.Time,
) *ArtifactStore {
	store.validateManifestRepair = func(
		_ ingest.ManifestRepairAuthorizationGrant,
		write ingest.RecoveryManifestWrite,
		observedAt time.Time,
	) error {
		if write.ManifestPath != expected.ManifestPath ||
			write.ManifestInput != expected.ManifestInput || write.Raw != expected.Raw ||
			!bytes.Equal(write.CanonicalBody, expected.CanonicalBody) || write.Digest != expected.Digest {
			return ingest.ErrInvalidManifestRepairAuthorization
		}
		if !observedAt.Before(expiresAt) {
			return ingest.ErrManifestRepairAuthorizationExpired
		}
		return nil
	}
	store.manifestRepairDeadline = func(
		_ ingest.ManifestRepairAuthorizationGrant,
		write ingest.RecoveryManifestWrite,
	) (time.Time, error) {
		if write.ManifestPath != expected.ManifestPath ||
			write.ManifestInput != expected.ManifestInput || write.Raw != expected.Raw ||
			!bytes.Equal(write.CanonicalBody, expected.CanonicalBody) || write.Digest != expected.Digest {
			return time.Time{}, ingest.ErrInvalidManifestRepairAuthorization
		}
		return expiresAt, nil
	}
	return store
}

func scriptedClock(values ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}

func mutateRecoveryManifestBody(write ingest.RecoveryManifestWrite) ingest.RecoveryManifestWrite {
	write.CanonicalBody = append([]byte(nil), write.CanonicalBody...)
	write.CanonicalBody = append(write.CanonicalBody, ' ')
	write.Digest = ingest.ComputeArtifactDigest(write.CanonicalBody)
	return write
}

func deterministicGzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	var destination bytes.Buffer
	writer, err := gzip.NewWriterLevel(&destination, gzip.BestSpeed)
	if err != nil {
		t.Fatalf("gzip writer: %v", err)
	}
	writer.Header.ModTime = time.Unix(0, 0).UTC()
	writer.Header.OS = 255
	if _, err := writer.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return destination.Bytes()
}

func equalStrings(left, right []string) bool {
	return fmt.Sprint(left) == fmt.Sprint(right)
}

func equalReads(left, right []generationRead) bool {
	return fmt.Sprint(left) == fmt.Sprint(right)
}
