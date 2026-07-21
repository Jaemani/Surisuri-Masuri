package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestComputeArtifactDigestKnownVector(t *testing.T) {
	digest := ComputeArtifactDigest([]byte("123456789"))
	if digest.SHA256 != "15e2b0d3c33891ebb0f1ef609ec419420c20e320ce94c65fbc8c3312448eb225" {
		t.Fatalf("SHA256 = %q", digest.SHA256)
	}
	if digest.CRC32C != 0xe3069283 {
		t.Fatalf("CRC32C = %#x, want 0xe3069283", digest.CRC32C)
	}
	if digest.Size != 9 {
		t.Fatalf("Size = %d, want 9", digest.Size)
	}
}

func TestCanonicalTelemetryManifestMatchesGoldenBytes(t *testing.T) {
	input, object := validManifestFixture()
	encoded, digest, err := CanonicalTelemetryManifest(input, object)
	if err != nil {
		t.Fatalf("CanonicalTelemetryManifest() error = %v", err)
	}
	const want = `{"manifest_version":1,"payload_schema_version":"telemetry-batch.v2","tenant_id":"11111111-1111-4111-8111-111111111111","device_id":"22222222-2222-4222-8222-222222222222","trip_id":"33333333-3333-4333-8333-333333333333","installation_id":"44444444-4444-4444-8444-444444444444","batch_id":"01982015-4400-7000-8000-000000000001","client_batch_id":"55555555-5555-4555-8555-555555555555","body_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","object_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","object_crc32c":305419896,"object_size":4096,"compression":"gzip","content_type":"application/json","sample_count":2,"first_captured_at":"2026-07-21T08:10:00Z","last_captured_at":"2026-07-21T08:29:50Z","object_path":"telemetry/v2/tenants/11111111-1111-4111-8111-111111111111/devices/22222222-2222-4222-8222-222222222222/trips/33333333-3333-4333-8333-333333333333/year=2026/month=07/day=21/01982015-4400-7000-8000-000000000001.json.gz","object_generation":1700000000000001,"object_metageneration":1,"received_at":"2026-07-21T09:00:00Z","expires_at":"2026-08-20T09:00:00Z","validator_version":"gateway-validator@abc123","consent_revision_id":"66666666-6666-4666-8666-666666666666"}`
	if string(encoded) != want {
		t.Fatalf("manifest bytes mismatch\n got: %s\nwant: %s", encoded, want)
	}
	if !json.Valid(encoded) {
		t.Fatal("manifest is not valid JSON")
	}
	if digest != ComputeArtifactDigest(encoded) {
		t.Fatalf("manifest digest = %#v", digest)
	}
	for _, forbidden := range [][]byte{
		[]byte("firebase_uid"),
		[]byte("app_check_app_id"),
		[]byte("phone_number"),
		[]byte("public_code"),
	} {
		if bytes.Contains(encoded, forbidden) {
			t.Fatalf("manifest contains forbidden field %q", forbidden)
		}
	}
}

func TestCanonicalTelemetryManifestNormalizesTimesAndIgnoresReplayFlag(t *testing.T) {
	input, object := validManifestFixture()
	first, firstDigest, err := CanonicalTelemetryManifest(input, object)
	if err != nil {
		t.Fatalf("first manifest error = %v", err)
	}

	input.FirstCapturedAt = input.FirstCapturedAt.UTC()
	input.LastCapturedAt = input.LastCapturedAt.UTC()
	input.ReceivedAt = input.ReceivedAt.UTC()
	input.ExpiresAt = input.ExpiresAt.UTC()
	object.Replay = true
	second, secondDigest, err := CanonicalTelemetryManifest(input, object)
	if err != nil {
		t.Fatalf("second manifest error = %v", err)
	}
	if !bytes.Equal(first, second) || firstDigest != secondDigest {
		t.Fatal("equivalent instants or replay metadata changed canonical bytes")
	}
}

func TestExpectedTelemetryArtifactPathsUseReceivedUTCDate(t *testing.T) {
	input, _ := validManifestFixture()
	const wantObject = "telemetry/v2/tenants/11111111-1111-4111-8111-111111111111/devices/22222222-2222-4222-8222-222222222222/trips/33333333-3333-4333-8333-333333333333/year=2026/month=07/day=21/01982015-4400-7000-8000-000000000001.json.gz"
	const wantManifest = "telemetry-manifests/v2/tenants/11111111-1111-4111-8111-111111111111/trips/33333333-3333-4333-8333-333333333333/year=2026/month=07/day=21/01982015-4400-7000-8000-000000000001.manifest.json"
	if got := ExpectedTelemetryObjectPath(input); got != wantObject {
		t.Fatalf("object path = %q", got)
	}
	if got := ExpectedTelemetryManifestPath(input); got != wantManifest {
		t.Fatalf("manifest path = %q", got)
	}
}

func TestCanonicalTelemetryManifestRejectsInvalidLineage(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BatchManifestInput, *StoredArtifact)
	}{
		{name: "payload schema", mutate: func(input *BatchManifestInput, _ *StoredArtifact) {
			input.PayloadSchemaVersion = "telemetry-batch.v1"
		}},
		{name: "tenant id", mutate: func(input *BatchManifestInput, _ *StoredArtifact) {
			input.TenantID = "not-a-uuid"
		}},
		{name: "body hash", mutate: func(input *BatchManifestInput, _ *StoredArtifact) {
			input.BodyHash = "ABC"
		}},
		{name: "sample count", mutate: func(input *BatchManifestInput, _ *StoredArtifact) {
			input.SampleCount = 0
		}},
		{name: "recorded order", mutate: func(input *BatchManifestInput, _ *StoredArtifact) {
			input.LastCapturedAt = input.FirstCapturedAt.Add(-time.Second)
		}},
		{name: "expiration", mutate: func(input *BatchManifestInput, _ *StoredArtifact) {
			input.ExpiresAt = input.ReceivedAt
		}},
		{name: "validator", mutate: func(input *BatchManifestInput, _ *StoredArtifact) {
			input.ValidatorVersion = "  "
		}},
		{name: "object path lineage", mutate: func(_ *BatchManifestInput, object *StoredArtifact) {
			object.Path = "telemetry/v2/other.json.gz"
		}},
		{name: "object hash", mutate: func(_ *BatchManifestInput, object *StoredArtifact) {
			object.SHA256 = "ABC"
		}},
		{name: "object size", mutate: func(_ *BatchManifestInput, object *StoredArtifact) {
			object.Size = 0
		}},
		{name: "object generation", mutate: func(_ *BatchManifestInput, object *StoredArtifact) {
			object.Generation = 0
		}},
		{name: "object metageneration", mutate: func(_ *BatchManifestInput, object *StoredArtifact) {
			object.Metageneration = 0
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, object := validManifestFixture()
			test.mutate(&input, &object)
			encoded, digest, err := CanonicalTelemetryManifest(input, object)
			if !errors.Is(err, ErrInvalidArtifactManifest) {
				t.Fatalf("error = %v", err)
			}
			if encoded != nil || digest != (ArtifactDigest{}) {
				t.Fatalf("invalid result = %q, %#v", encoded, digest)
			}
		})
	}
}

type compileTimeArtifactStore struct{}

func (compileTimeArtifactStore) StoreBatch(
	context.Context,
	BatchArtifactWrite,
) (StoredBatchArtifacts, error) {
	return StoredBatchArtifacts{}, nil
}

var _ TelemetryArtifactStore = compileTimeArtifactStore{}

func validManifestFixture() (BatchManifestInput, StoredArtifact) {
	korea := time.FixedZone("Asia/Seoul", 9*60*60)
	input := BatchManifestInput{
		PayloadSchemaVersion: "telemetry-batch.v2",
		TenantID:             "11111111-1111-4111-8111-111111111111",
		DeviceID:             "22222222-2222-4222-8222-222222222222",
		TripID:               "33333333-3333-4333-8333-333333333333",
		InstallationID:       "44444444-4444-4444-8444-444444444444",
		BatchID:              "01982015-4400-7000-8000-000000000001",
		ClientBatchID:        "55555555-5555-4555-8555-555555555555",
		ConsentRevisionID:    "66666666-6666-4666-8666-666666666666",
		BodyHash:             "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SampleCount:          2,
		FirstCapturedAt:      time.Date(2026, time.July, 21, 17, 10, 0, 0, korea),
		LastCapturedAt:       time.Date(2026, time.July, 21, 17, 29, 50, 0, korea),
		ReceivedAt:           time.Date(2026, time.July, 21, 18, 0, 0, 0, korea),
		ExpiresAt:            time.Date(2026, time.August, 20, 18, 0, 0, 0, korea),
		ValidatorVersion:     "gateway-validator@abc123",
	}
	return input, StoredArtifact{
		Path:           ExpectedTelemetryObjectPath(input),
		SHA256:         "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CRC32C:         0x12345678,
		Size:           4096,
		Generation:     1700000000000001,
		Metageneration: 1,
	}
}
