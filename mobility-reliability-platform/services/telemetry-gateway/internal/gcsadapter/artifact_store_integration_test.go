package gcsadapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/storage"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestArtifactStoreEmulatorImmutableReplayAndGenerationRead(t *testing.T) {
	if os.Getenv("STORAGE_EMULATOR_HOST") == "" {
		t.Skip("STORAGE_EMULATOR_HOST is required for Cloud Storage integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage.NewClient(): %v", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("storage client close: %v", closeErr)
		}
	}()

	bucketName := fmt.Sprintf("mobility-artifacts-%d", time.Now().UnixNano())
	bucket := client.Bucket(bucketName)
	if err := bucket.Create(ctx, "demo-mobility-reliability", nil); err != nil {
		t.Fatalf("create emulator bucket: %v", err)
	}
	store, err := NewArtifactStore(bucket)
	if err != nil {
		t.Fatalf("NewArtifactStore(): %v", err)
	}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1,2]}`))

	first, err := store.StoreBatch(ctx, write)
	if err != nil {
		t.Fatalf("first StoreBatch(): %v", err)
	}
	second, err := store.StoreBatch(ctx, write)
	if err != nil {
		t.Fatalf("replay StoreBatch(): %v", err)
	}
	if !second.Object.Replay || !second.Manifest.Replay ||
		second.Object.Generation != first.Object.Generation ||
		second.Manifest.Generation != first.Manifest.Generation {
		t.Fatalf("immutable replay lineage: first=%#v second=%#v", first, second)
	}

	rawReader, err := bucket.Object(first.Object.Path).
		Generation(first.Object.Generation).
		ReadCompressed(true).
		NewReader(ctx)
	if err != nil {
		t.Fatalf("open exact raw generation: %v", err)
	}
	rawBytes, readErr := io.ReadAll(rawReader)
	closeErr := rawReader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read exact raw generation: read=%v close=%v", readErr, closeErr)
	}
	if !bytes.Equal(rawBytes, write.CompressedBody) ||
		ingest.ComputeArtifactDigest(rawBytes).SHA256 != first.Object.SHA256 {
		t.Fatal("exact generation did not preserve deterministic gzip bytes")
	}

	conflict := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[9]}`))
	_, err = store.StoreBatch(ctx, conflict)
	if !errors.Is(err, ingest.ErrRawArtifactConflict) || !errors.Is(err, ingest.ErrArtifactConflict) {
		t.Fatalf("different bytes conflict = %v", err)
	}

	objectAttrs, err := bucket.Object(first.Object.Path).Generation(first.Object.Generation).Attrs(ctx)
	if err != nil {
		t.Fatalf("read raw attrs: %v", err)
	}
	manifestAttrs, err := bucket.Object(first.Manifest.Path).Generation(first.Manifest.Generation).Attrs(ctx)
	if err != nil {
		t.Fatalf("read manifest attrs: %v", err)
	}
	if objectAttrs.Metadata["sha256"] != first.Object.SHA256 ||
		manifestAttrs.Metadata["sha256"] != first.Manifest.SHA256 ||
		objectAttrs.Generation != first.Object.Generation ||
		manifestAttrs.Generation != first.Manifest.Generation {
		t.Fatalf("provider attrs diverged: object=%#v manifest=%#v", objectAttrs, manifestAttrs)
	}
}
