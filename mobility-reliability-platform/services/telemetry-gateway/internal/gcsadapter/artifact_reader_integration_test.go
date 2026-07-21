package gcsadapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/storage"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestArtifactReaderEmulatorExactGenerationRead(t *testing.T) {
	if os.Getenv("STORAGE_EMULATOR_HOST") == "" {
		t.Skip("STORAGE_EMULATOR_HOST is required for Cloud Storage integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	writerClient, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatal("create emulator writer client failed")
	}
	defer func() {
		if closeErr := writerClient.Close(); closeErr != nil {
			t.Error("close emulator writer client failed")
		}
	}()

	bucketName := fmt.Sprintf("mobility-reader-%d", time.Now().UnixNano())
	bucket := writerClient.Bucket(bucketName)
	if err := bucket.Create(ctx, "demo-mobility-reliability", nil); err != nil {
		t.Fatal("create emulator bucket failed")
	}
	store, err := NewArtifactStore(bucket)
	if err != nil {
		t.Fatal("create artifact store failed")
	}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1,2]}`))
	stored, err := store.StoreBatch(ctx, write)
	if err != nil {
		t.Fatal("store artifact fixture failed")
	}

	reader, err := NewHTTPArtifactInventoryReader(ctx, bucketName)
	if err != nil {
		t.Fatal("create HTTP artifact reader failed")
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			t.Error("close HTTP artifact reader failed")
		}
	}()

	rawSnapshot, err := reader.InspectGeneration(ctx, stored.Object.Path, stored.Object.Generation)
	if err != nil {
		t.Fatal("inspect exact raw generation failed")
	}
	if rawSnapshot.Path != stored.Object.Path || rawSnapshot.SHA256 != stored.Object.SHA256 ||
		rawSnapshot.CRC32C != stored.Object.CRC32C || rawSnapshot.Size != stored.Object.Size ||
		rawSnapshot.Generation != stored.Object.Generation ||
		rawSnapshot.Metageneration != stored.Object.Metageneration || rawSnapshot.SoftDeleted {
		t.Fatal("exact raw generation lineage mismatch")
	}

	rawTarget := ingest.ArtifactTarget{
		Path:           stored.Object.Path,
		Generation:     stored.Object.Generation,
		Metageneration: stored.Object.Metageneration,
	}
	rawBytes, err := reader.ReadRawGenerationCompressed(ctx, rawTarget, int64(len(write.CompressedBody)))
	if err != nil {
		t.Fatal("read exact compressed raw generation failed")
	}
	wantRawDigest := ingest.ComputeArtifactDigest(write.CompressedBody)
	if !bytes.Equal(rawBytes, write.CompressedBody) ||
		ingest.ComputeArtifactDigest(rawBytes) != wantRawDigest ||
		wantRawDigest.SHA256 != stored.Object.SHA256 ||
		wantRawDigest.CRC32C != stored.Object.CRC32C ||
		wantRawDigest.Size != stored.Object.Size {
		t.Fatal("exact compressed raw generation bytes or digest mismatch")
	}

	wantManifest, wantManifestDigest, err := ingest.CanonicalTelemetryManifest(write.Manifest, stored.Object)
	if err != nil {
		t.Fatal("build canonical manifest fixture failed")
	}
	manifestTarget := ingest.ArtifactTarget{
		Path:           stored.Manifest.Path,
		Generation:     stored.Manifest.Generation,
		Metageneration: stored.Manifest.Metageneration,
	}
	manifestBytes, err := reader.ReadManifestGeneration(ctx, manifestTarget, int64(len(wantManifest)))
	if err != nil {
		t.Fatal("read exact manifest generation failed")
	}
	if !bytes.Equal(manifestBytes, wantManifest) ||
		ingest.ComputeArtifactDigest(manifestBytes) != wantManifestDigest ||
		wantManifestDigest.SHA256 != stored.Manifest.SHA256 ||
		wantManifestDigest.CRC32C != stored.Manifest.CRC32C ||
		wantManifestDigest.Size != stored.Manifest.Size {
		t.Fatal("exact manifest generation bytes or digest mismatch")
	}

	wrongMetageneration := rawTarget
	wrongMetageneration.Metageneration++
	_, err = reader.ReadRawGenerationCompressed(
		ctx,
		wrongMetageneration,
		int64(len(write.CompressedBody)),
	)
	requireRedactedArtifactReaderError(t, err, ingest.ErrArtifactPreconditionDrift, "wrong metageneration read")

	absentGeneration := stored.Object.Generation + 10_000
	_, err = reader.InspectGeneration(ctx, stored.Object.Path, absentGeneration)
	requireRedactedArtifactReaderError(t, err, ingest.ErrArtifactGenerationNotFound, "absent generation inspect")
	_, err = reader.ReadRawGenerationCompressed(
		ctx,
		ingest.ArtifactTarget{
			Path:           stored.Object.Path,
			Generation:     absentGeneration,
			Metageneration: stored.Object.Metageneration,
		},
		int64(len(write.CompressedBody)),
	)
	requireRedactedArtifactReaderError(t, err, ingest.ErrArtifactGenerationNotFound, "absent generation read")
}

func requireRedactedArtifactReaderError(t *testing.T, err, want error, operation string) {
	t.Helper()
	if err == nil || !errors.Is(err, want) || err.Error() != want.Error() {
		t.Fatalf("%s did not return the expected redacted error", operation)
	}
}
