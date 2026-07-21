package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

func TestDecodeTelemetryManifestV1AcceptsCanonicalManifest(t *testing.T) {
	raw, want := manifestReaderFixture(t)
	raw = append(raw, '\n')

	got, err := DecodeTelemetryManifestV1(raw)
	if err != nil {
		t.Fatalf("DecodeTelemetryManifestV1() error = %v", err)
	}
	if got != want {
		t.Fatalf("DecodeTelemetryManifestV1() = %#v, want %#v", got, want)
	}
}

func TestDecodeTelemetryManifestV1RejectsUnsafeJSON(t *testing.T) {
	valid, _ := manifestReaderFixture(t)
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "empty", raw: nil},
		{name: "oversized", raw: bytes.Repeat([]byte{' '}, MaxTelemetryManifestBytes+1)},
		{name: "invalid UTF-8", raw: []byte{'{', '"', 0xff, '"', ':', '1', '}'}},
		{
			name: "duplicate key",
			raw: bytes.Replace(
				valid,
				[]byte(`{"manifest_version":1,`),
				[]byte(`{"manifest_version":1,"manifest_version":1,`),
				1,
			),
		},
		{
			name: "escaped duplicate key",
			raw: bytes.Replace(
				valid,
				[]byte(`{"manifest_version":1,`),
				[]byte(`{"manifest_version":1,"manifest\u005fversion":1,`),
				1,
			),
		},
		{
			name: "unknown field",
			raw: bytes.Replace(
				valid,
				[]byte(`{"manifest_version":1,`),
				[]byte(`{"manifest_version":1,"unknown":"sensitive-value",`),
				1,
			),
		},
		{name: "trailing value", raw: append(append([]byte{}, valid...), []byte(` {}`)...)},
		{name: "top-level array", raw: []byte(`[]`)},
		{name: "type mismatch with sensitive value", raw: []byte(`{"manifest_version":"sensitive-value"}`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, err := DecodeTelemetryManifestV1(test.raw)
			if !errors.Is(err, ErrInvalidArtifactManifest) {
				t.Fatalf("DecodeTelemetryManifestV1() = %#v, %v; want invalid manifest", manifest, err)
			}
			if manifest != (TelemetryManifest{}) {
				t.Fatalf("rejected manifest = %#v, want zero value", manifest)
			}
			if strings.Contains(err.Error(), "sensitive-value") {
				t.Fatalf("error exposed manifest content: %v", err)
			}
		})
	}
}

func TestDecodeTelemetryManifestV1RejectsInvalidShape(t *testing.T) {
	_, valid := manifestReaderFixture(t)
	tests := []struct {
		name   string
		field  string
		mutate func(*TelemetryManifest)
	}{
		{name: "manifest version", field: "manifest_version", mutate: func(value *TelemetryManifest) {
			value.ManifestVersion = TelemetryManifestVersion + 1
		}},
		{name: "payload schema", field: "payload_schema_version", mutate: func(value *TelemetryManifest) {
			value.PayloadSchemaVersion = "telemetry-batch.v1"
		}},
		{name: "compression", field: "compression", mutate: func(value *TelemetryManifest) {
			value.Compression = "br"
		}},
		{name: "content type", field: "content_type", mutate: func(value *TelemetryManifest) {
			value.ContentType = "text/plain"
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			raw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			_, err = DecodeTelemetryManifestV1(raw)
			if !errors.Is(err, ErrInvalidArtifactManifest) || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("DecodeTelemetryManifestV1() error = %v, want %s", err, test.field)
			}
		})
	}
}

func TestDecodeTelemetryManifestV1ParsesAllRFC3339NanoTimestamps(t *testing.T) {
	_, valid := manifestReaderFixture(t)
	tests := []struct {
		name   string
		field  string
		mutate func(*TelemetryManifest)
	}{
		{name: "first captured", field: "first_captured_at", mutate: func(value *TelemetryManifest) {
			value.FirstCapturedAt = "2026-07-21 08:10:00Z"
		}},
		{name: "last captured", field: "last_captured_at", mutate: func(value *TelemetryManifest) {
			value.LastCapturedAt = "not-a-time"
		}},
		{name: "received", field: "received_at", mutate: func(value *TelemetryManifest) {
			value.ReceivedAt = ""
		}},
		{name: "expires", field: "expires_at", mutate: func(value *TelemetryManifest) {
			value.ExpiresAt = "2026-08-20T09:00:00.123456789Zextra"
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			raw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			_, err = DecodeTelemetryManifestV1(raw)
			if !errors.Is(err, ErrInvalidArtifactManifest) || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("DecodeTelemetryManifestV1() error = %v, want %s", err, test.field)
			}
		})
	}
}

func manifestReaderFixture(t *testing.T) ([]byte, TelemetryManifest) {
	t.Helper()
	receivedAt := time.Date(2026, time.July, 21, 9, 0, 0, 123456789, time.UTC)
	input := BatchManifestInput{
		PayloadSchemaVersion: telemetry.SchemaVersionV2,
		TenantID:             "11111111-1111-4111-8111-111111111111",
		DeviceID:             "22222222-2222-4222-8222-222222222222",
		TripID:               "33333333-3333-4333-8333-333333333333",
		InstallationID:       "44444444-4444-4444-8444-444444444444",
		BatchID:              "01982015-4400-7000-8000-000000000001",
		ClientBatchID:        "55555555-5555-4555-8555-555555555555",
		ConsentRevisionID:    "66666666-6666-4666-8666-666666666666",
		BodyHash:             strings.Repeat("a", 64),
		SampleCount:          2,
		FirstCapturedAt:      receivedAt.Add(-50 * time.Minute),
		LastCapturedAt:       receivedAt.Add(-30 * time.Minute),
		ReceivedAt:           receivedAt,
		ArtifactExpiresAt:    receivedAt.Add(30 * 24 * time.Hour),
		ValidatorVersion:     "gateway-validator@abc123",
	}
	object := StoredArtifact{
		Path:           ExpectedTelemetryObjectPath(input),
		SHA256:         strings.Repeat("b", 64),
		CRC32C:         0x12345678,
		Size:           4096,
		Generation:     1700000000000001,
		Metageneration: 1,
	}
	raw, _, err := CanonicalTelemetryManifest(input, object)
	if err != nil {
		t.Fatalf("CanonicalTelemetryManifest() error = %v", err)
	}
	var manifest TelemetryManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return raw, manifest
}
