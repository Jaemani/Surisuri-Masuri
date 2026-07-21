package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	TelemetryManifestVersion = 1
	TelemetryCompression     = "gzip"
	TelemetryContentType     = "application/json"
)

var ErrInvalidArtifactManifest = errors.New("telemetry artifact manifest is invalid")

var (
	ErrArtifactConflict         = errors.New("telemetry artifact path contains different content")
	ErrArtifactContentConflict  = errors.New("telemetry artifact bytes differ from the reserved lineage")
	ErrRawArtifactConflict      = errors.New("raw telemetry artifact conflict")
	ErrManifestArtifactConflict = errors.New("telemetry manifest artifact conflict")
	ErrArtifactUnavailable      = errors.New("telemetry artifact store is unavailable")
)

// TelemetryArtifactStore persists the immutable compressed batch and its
// server-produced manifest. Implementations must return the stable generation
// of both artifacts for new writes and verified replays.
//
// The interface replaces the path-only ObjectStore so the Cloud Storage
// adapter and Firestore finalizer exchange one complete artifact lineage.
type TelemetryArtifactStore interface {
	StoreBatch(context.Context, BatchArtifactWrite) (StoredBatchArtifacts, error)
}

// BatchArtifactWrite contains only validated domain lineage and server-created
// bytes. Firebase UID and App Check identity deliberately have no fields here.
type BatchArtifactWrite struct {
	ObjectPath     string
	ManifestPath   string
	CompressedBody []byte
	Manifest       BatchManifestInput
}

// BatchManifestInput is the provider-neutral input used to create the
// immutable snake_case Storage manifest.
type BatchManifestInput struct {
	PayloadSchemaVersion string
	TenantID             string
	DeviceID             string
	TripID               string
	InstallationID       string
	BatchID              string
	ClientBatchID        string
	ConsentRevisionID    string
	BodyHash             string
	SampleCount          int
	FirstCapturedAt      time.Time
	LastCapturedAt       time.Time
	ReceivedAt           time.Time
	ArtifactExpiresAt    time.Time
	ValidatorVersion     string
}

// StoredArtifact identifies one immutable object generation. CRC32C is a GCS
// integrity checksum and may legitimately be zero.
type StoredArtifact struct {
	Path           string
	SHA256         string
	CRC32C         uint32
	Size           int64
	Generation     int64
	Metageneration int64
	Replay         bool
}

type StoredBatchArtifacts struct {
	Object   StoredArtifact
	Manifest StoredArtifact
}

// ArtifactDigest contains hashes over the exact stored bytes. For a telemetry
// object these are the deterministic gzip bytes, not the decompressed JSON.
type ArtifactDigest struct {
	SHA256 string
	CRC32C uint32
	Size   int64
}

// TelemetryManifest is server-produced and uses an ordered struct rather than
// a map so encoding/json produces one stable byte representation.
type TelemetryManifest struct {
	ManifestVersion      int    `json:"manifest_version"`
	PayloadSchemaVersion string `json:"payload_schema_version"`
	TenantID             string `json:"tenant_id"`
	DeviceID             string `json:"device_id"`
	TripID               string `json:"trip_id"`
	InstallationID       string `json:"installation_id"`
	BatchID              string `json:"batch_id"`
	ClientBatchID        string `json:"client_batch_id"`
	BodyHash             string `json:"body_hash"`
	ObjectSHA256         string `json:"object_sha256"`
	ObjectCRC32C         uint32 `json:"object_crc32c"`
	ObjectSize           int64  `json:"object_size"`
	Compression          string `json:"compression"`
	ContentType          string `json:"content_type"`
	SampleCount          int    `json:"sample_count"`
	FirstCapturedAt      string `json:"first_captured_at"`
	LastCapturedAt       string `json:"last_captured_at"`
	ObjectPath           string `json:"object_path"`
	ObjectGeneration     int64  `json:"object_generation"`
	ObjectMetageneration int64  `json:"object_metageneration"`
	ReceivedAt           string `json:"received_at"`
	ExpiresAt            string `json:"expires_at"`
	ValidatorVersion     string `json:"validator_version"`
	ConsentRevisionID    string `json:"consent_revision_id"`
}

var castagnoliTable = crc32.MakeTable(crc32.Castagnoli)

func ComputeArtifactDigest(content []byte) ArtifactDigest {
	digest := sha256.Sum256(content)
	return ArtifactDigest{
		SHA256: hex.EncodeToString(digest[:]),
		CRC32C: crc32.Checksum(content, castagnoliTable),
		Size:   int64(len(content)),
	}
}

func ExpectedTelemetryObjectPath(input BatchManifestInput) string {
	receivedAt := input.ReceivedAt.UTC()
	return fmt.Sprintf(
		"telemetry/v2/tenants/%s/devices/%s/trips/%s/year=%04d/month=%02d/day=%02d/%s.json.gz",
		input.TenantID,
		input.DeviceID,
		input.TripID,
		receivedAt.Year(),
		receivedAt.Month(),
		receivedAt.Day(),
		input.BatchID,
	)
}

func ExpectedTelemetryManifestPath(input BatchManifestInput) string {
	receivedAt := input.ReceivedAt.UTC()
	return fmt.Sprintf(
		"telemetry-manifests/v2/tenants/%s/trips/%s/year=%04d/month=%02d/day=%02d/%s.manifest.json",
		input.TenantID,
		input.TripID,
		receivedAt.Year(),
		receivedAt.Month(),
		receivedAt.Day(),
		input.BatchID,
	)
}

// CanonicalTelemetryManifest serializes a manifest for an already resolved
// immutable object generation. All times are normalized to UTC RFC3339Nano and
// no trailing newline is appended.
func CanonicalTelemetryManifest(
	input BatchManifestInput,
	object StoredArtifact,
) ([]byte, ArtifactDigest, error) {
	if err := validateManifestInput(input, object); err != nil {
		return nil, ArtifactDigest{}, err
	}
	manifest := TelemetryManifest{
		ManifestVersion:      TelemetryManifestVersion,
		PayloadSchemaVersion: input.PayloadSchemaVersion,
		TenantID:             input.TenantID,
		DeviceID:             input.DeviceID,
		TripID:               input.TripID,
		InstallationID:       input.InstallationID,
		BatchID:              input.BatchID,
		ClientBatchID:        input.ClientBatchID,
		BodyHash:             input.BodyHash,
		ObjectSHA256:         object.SHA256,
		ObjectCRC32C:         object.CRC32C,
		ObjectSize:           object.Size,
		Compression:          TelemetryCompression,
		ContentType:          TelemetryContentType,
		SampleCount:          input.SampleCount,
		FirstCapturedAt:      canonicalTime(input.FirstCapturedAt),
		LastCapturedAt:       canonicalTime(input.LastCapturedAt),
		ObjectPath:           object.Path,
		ObjectGeneration:     object.Generation,
		ObjectMetageneration: object.Metageneration,
		ReceivedAt:           canonicalTime(input.ReceivedAt),
		ExpiresAt:            canonicalTime(input.ArtifactExpiresAt),
		ValidatorVersion:     input.ValidatorVersion,
		ConsentRevisionID:    input.ConsentRevisionID,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, ArtifactDigest{}, fmt.Errorf("marshal telemetry manifest: %w", err)
	}
	return encoded, ComputeArtifactDigest(encoded), nil
}

func validateManifestInput(input BatchManifestInput, object StoredArtifact) error {
	identifiers := []struct {
		field string
		value string
	}{
		{field: "tenant_id", value: input.TenantID},
		{field: "device_id", value: input.DeviceID},
		{field: "trip_id", value: input.TripID},
		{field: "installation_id", value: input.InstallationID},
		{field: "batch_id", value: input.BatchID},
		{field: "client_batch_id", value: input.ClientBatchID},
		{field: "consent_revision_id", value: input.ConsentRevisionID},
	}
	for _, identifier := range identifiers {
		if !telemetry.IsUUID(identifier.value) {
			return invalidManifest(identifier.field)
		}
	}
	if input.PayloadSchemaVersion != telemetry.SchemaVersionV2 {
		return invalidManifest("payload_schema_version")
	}
	if !isLowerHexDigest(input.BodyHash) {
		return invalidManifest("body_hash")
	}
	if input.SampleCount < 1 || input.SampleCount > telemetry.MaxSamples {
		return invalidManifest("sample_count")
	}
	if input.FirstCapturedAt.IsZero() || input.LastCapturedAt.IsZero() ||
		input.LastCapturedAt.Before(input.FirstCapturedAt) {
		return invalidManifest("captured_at")
	}
	if input.ReceivedAt.IsZero() || input.ArtifactExpiresAt.IsZero() ||
		!input.ReceivedAt.Before(input.ArtifactExpiresAt) {
		return invalidManifest("expires_at")
	}
	if strings.TrimSpace(input.ValidatorVersion) == "" {
		return invalidManifest("validator_version")
	}
	if object.Path != ExpectedTelemetryObjectPath(input) {
		return invalidManifest("object_path")
	}
	if !isLowerHexDigest(object.SHA256) {
		return invalidManifest("object_sha256")
	}
	if object.Size <= 0 {
		return invalidManifest("object_size")
	}
	if object.Generation <= 0 {
		return invalidManifest("object_generation")
	}
	if object.Metageneration <= 0 {
		return invalidManifest("object_metageneration")
	}
	return nil
}

func invalidManifest(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArtifactManifest, field)
}

func isLowerHexDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func canonicalTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
