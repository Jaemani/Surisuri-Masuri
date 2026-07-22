package gcsadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/storage"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupGenerationDeleteBackendEmulatorPinsGenerationAndMetageneration(t *testing.T) {
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

	bucketName := fmt.Sprintf("mobility-cleanup-%d", time.Now().UnixNano())
	bucket := client.Bucket(bucketName)
	if err := bucket.Create(ctx, "demo-mobility-reliability", nil); err != nil {
		t.Fatalf("create emulator bucket: %v", err)
	}
	store, err := NewArtifactStore(bucket)
	if err != nil {
		t.Fatalf("NewArtifactStore(): %v", err)
	}
	write := artifactWriteFixture(t, []byte(`{"schemaVersion":"telemetry-batch.v2","samples":[1,2]}`))
	stored, err := store.StoreBatch(ctx, write)
	if err != nil {
		t.Fatalf("StoreBatch(): %v", err)
	}
	deleter := storageCleanupGenerationDeleteBackend{bucket: bucket}

	err = deleter.DeleteGeneration(
		ctx,
		stored.Object.Path,
		stored.Object.Generation,
		stored.Object.Metageneration+1,
	)
	if !errors.Is(err, ingest.ErrArtifactPreconditionDrift) {
		t.Fatalf("wrong metageneration delete = %v", err)
	}
	if _, err := bucket.Object(stored.Object.Path).Generation(stored.Object.Generation).Attrs(ctx); err != nil {
		t.Fatalf("wrong precondition changed raw generation: %v", err)
	}

	if err := deleter.DeleteGeneration(
		ctx,
		stored.Object.Path,
		stored.Object.Generation,
		stored.Object.Metageneration,
	); err != nil {
		t.Fatalf("exact generation delete = %v", err)
	}
	if _, err := bucket.Object(stored.Object.Path).Generation(stored.Object.Generation).Attrs(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Fatalf("deleted raw generation remains live: %v", err)
	}
	if _, err := bucket.Object(stored.Manifest.Path).Generation(stored.Manifest.Generation).Attrs(ctx); err != nil {
		t.Fatalf("raw delete changed manifest generation: %v", err)
	}
	err = deleter.DeleteGeneration(
		ctx,
		stored.Object.Path,
		stored.Object.Generation,
		stored.Object.Metageneration,
	)
	if !errors.Is(err, ingest.ErrArtifactGenerationNotFound) {
		t.Fatalf("repeat exact delete = %v", err)
	}
}
