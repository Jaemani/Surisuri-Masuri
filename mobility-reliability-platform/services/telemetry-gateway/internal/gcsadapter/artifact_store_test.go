package gcsadapter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	objects     map[string]memoryObject
	failCreate  map[string][]error
	createOrder []string
	reads       []generationRead
	nextGen     int64
}

type metagenerationDriftBackend struct {
	*memoryBackend
	driftPath string
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
		ExpiresAt:            receivedAt.Add(ingest.ReceiptRetention),
		ValidatorVersion:     "gateway-validator@test",
	}
	return ingest.BatchArtifactWrite{
		ObjectPath:     ingest.ExpectedTelemetryObjectPath(manifest),
		ManifestPath:   ingest.ExpectedTelemetryManifestPath(manifest),
		CompressedBody: compressed,
		Manifest:       manifest,
	}
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
