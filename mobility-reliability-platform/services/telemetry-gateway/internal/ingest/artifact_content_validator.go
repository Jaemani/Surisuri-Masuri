package ingest

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
	"strconv"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	maxTelemetryArtifactBodyBytes = 2 * 1024 * 1024
	artifactCacheControlNoStore   = "no-store"
	telemetryGZIPCodecProfileV1   = "go-gzip-best-speed.v1"
)

// telemetryCodecGoldenRawV1 is a synthetic, canonical telemetry-batch.v2
// fixture. Its compressed digest pins the exact output of the codec profile;
// it contains no production identity or location data.
const telemetryCodecGoldenRawV1 = `{"schemaVersion":"telemetry-batch.v2","clientBatchId":"11111111-1111-4111-8111-111111111111","tenantId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","tripId":"44444444-4444-4444-8444-444444444444","clientSessionId":"55555555-5555-4555-8555-555555555555","installationId":"66666666-6666-4666-8666-666666666666","consentRevisionId":"77777777-7777-4777-8777-777777777777","sentAt":"2026-01-01T00:00:01Z","samples":[{"clientSampleId":"88888888-8888-4888-8888-888888888888","sequence":0,"capturedAt":"2026-01-01T00:00:00Z","latitude":0,"longitude":0,"horizontalAccuracyM":1,"source":"phone_gps"}]}`

// This value is deliberately literal rather than derived at runtime. The
// registry self-test must detect a toolchain or compressor output change.
const telemetryCodecGoldenCompressedSHA256V1 = "1dc9a9caaf7b442a80d7c73fb43b7982ba11e7ffa4d968eef7d0fe4d3171993d"

type artifactContentValidationStatus string

const (
	artifactContentValidationValid   artifactContentValidationStatus = "valid"
	artifactContentValidationInvalid artifactContentValidationStatus = "invalid"
)

// artifactContentValidationResult deliberately carries no decoded batch,
// source bytes, object path, identifier, or coordinate. A caller can only
// observe success or one bounded reason code.
type artifactContentValidationResult struct {
	Status     artifactContentValidationStatus
	ReasonCode ArtifactReasonCode
}

// artifactValidatedRawReference is the bounded manifest output needed by the
// classifier to inspect or compare the referenced raw generation. It contains
// no source bytes, decoded payload, identifier beyond the authorization-bound
// object path, or telemetry value.
type artifactValidatedRawReference struct {
	Target ArtifactTarget
	SHA256 string
	CRC32C uint32
	Size   int64
}

type artifactManifestValidationResult struct {
	Status        artifactContentValidationStatus
	ReasonCode    ArtifactReasonCode
	ReferencedRaw *artifactValidatedRawReference
}

func validArtifactContent() artifactContentValidationResult {
	return artifactContentValidationResult{Status: artifactContentValidationValid}
}

func invalidArtifactContent(reason ArtifactReasonCode) artifactContentValidationResult {
	return artifactContentValidationResult{
		Status:     artifactContentValidationInvalid,
		ReasonCode: reason,
	}
}

// telemetryArtifactValidator is the pure provider-neutral content boundary.
// Authorization, inventory, reads, classification, and receipt mutation are
// intentionally outside this interface.
type telemetryArtifactValidator interface {
	ValidateManifest(
		ArtifactClassificationRequest,
		ArtifactSnapshot,
		[]byte,
	) artifactManifestValidationResult
	ValidateRaw(
		ArtifactClassificationRequest,
		ArtifactSnapshot,
		[]byte,
	) artifactContentValidationResult
	Validate(
		ArtifactClassificationRequest,
		ArtifactSnapshot,
		[]byte,
		ArtifactSnapshot,
		[]byte,
	) artifactContentValidationResult
}

type telemetryArtifactDecoder func(io.Reader) (telemetry.Batch, error)
type telemetryArtifactPayloadValidator func(telemetry.Batch) *telemetry.ValidationError
type telemetryArtifactManifestBuilder func(BatchManifestInput, StoredArtifact) ([]byte, ArtifactDigest, error)
type telemetryArtifactCompressor func([]byte) ([]byte, error)

type telemetryArtifactCodecProfile struct {
	name                   string
	compress               telemetryArtifactCompressor
	goldenRaw              []byte
	goldenCompressedSHA256 string
	available              bool
}

type telemetryArtifactValidatorProfile struct {
	version              string
	decode               telemetryArtifactDecoder
	validate             telemetryArtifactPayloadValidator
	buildManifest        telemetryArtifactManifestBuilder
	maxDecompressedBytes int64
	codec                telemetryArtifactCodecProfile
}

type telemetryArtifactValidatorRegistry struct {
	profiles map[string]telemetryArtifactValidatorProfile
}

type registeredTelemetryArtifactValidator struct {
	registry telemetryArtifactValidatorRegistry
}

var _ telemetryArtifactValidator = (*registeredTelemetryArtifactValidator)(nil)

func newTelemetryArtifactContentValidator() telemetryArtifactValidator {
	return newTelemetryArtifactContentValidatorWithProfiles(
		telemetryArtifactValidatorProfile{
			version:              TelemetryValidatorVersion,
			decode:               telemetry.DecodeBatch,
			validate:             func(batch telemetry.Batch) *telemetry.ValidationError { return batch.Validate() },
			buildManifest:        CanonicalTelemetryManifest,
			maxDecompressedBytes: maxTelemetryArtifactBodyBytes,
			codec: telemetryArtifactCodecProfile{
				name:                   telemetryGZIPCodecProfileV1,
				compress:               deterministicGZIP,
				goldenRaw:              []byte(telemetryCodecGoldenRawV1),
				goldenCompressedSHA256: telemetryCodecGoldenCompressedSHA256V1,
			},
		},
	)
}

// newTelemetryArtifactContentValidatorWithProfiles is an internal composition
// seam for registry and fail-closed codec tests. Duplicate or malformed
// profiles are omitted rather than resolved by registration order.
func newTelemetryArtifactContentValidatorWithProfiles(
	profiles ...telemetryArtifactValidatorProfile,
) telemetryArtifactValidator {
	registry := telemetryArtifactValidatorRegistry{
		profiles: make(map[string]telemetryArtifactValidatorProfile, len(profiles)),
	}
	versionOccurrences := make(map[string]int, len(profiles))
	for _, profile := range profiles {
		versionOccurrences[profile.version]++
	}
	for _, profile := range profiles {
		if versionOccurrences[profile.version] != 1 || !validTelemetryArtifactValidatorProfile(profile) {
			continue
		}
		profile.codec.available = telemetryCodecProfileSelfTest(profile.codec)
		registry.profiles[profile.version] = profile
	}
	return &registeredTelemetryArtifactValidator{registry: registry}
}

func validTelemetryArtifactValidatorProfile(profile telemetryArtifactValidatorProfile) bool {
	return validArtifactServerLabel(profile.version) &&
		profile.decode != nil && profile.validate != nil && profile.buildManifest != nil &&
		profile.maxDecompressedBytes > 0 && profile.maxDecompressedBytes <= maxTelemetryArtifactBodyBytes &&
		validArtifactServerLabel(profile.codec.name) && profile.codec.compress != nil &&
		len(profile.codec.goldenRaw) > 0 && isLowerHexDigest(profile.codec.goldenCompressedSHA256)
}

func telemetryCodecProfileSelfTest(profile telemetryArtifactCodecProfile) bool {
	compressed, err := profile.compress(profile.goldenRaw)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(compressed)
	return hex.EncodeToString(digest[:]) == profile.goldenCompressedSHA256
}

func (v *registeredTelemetryArtifactValidator) Validate(
	request ArtifactClassificationRequest,
	manifestSnapshot ArtifactSnapshot,
	manifestBytes []byte,
	rawSnapshot ArtifactSnapshot,
	rawCompressedBytes []byte,
) artifactContentValidationResult {
	manifestResult := v.ValidateManifest(request, manifestSnapshot, manifestBytes)
	rawResult := v.ValidateRaw(request, rawSnapshot, rawCompressedBytes)
	referenceReason := ArtifactReasonCode("")
	if manifestResult.Status == artifactContentValidationValid &&
		(manifestResult.ReferencedRaw == nil ||
			!validatedRawReferenceMatchesSnapshot(*manifestResult.ReferencedRaw, rawSnapshot)) {
		referenceReason = ArtifactReasonManifestLineageMismatch
	}
	reason := highestPriorityArtifactContentReason(
		manifestResult.ReasonCode,
		rawResult.ReasonCode,
		referenceReason,
	)
	if reason != "" {
		return invalidArtifactContent(reason)
	}
	return validArtifactContent()
}

func (v *registeredTelemetryArtifactValidator) ValidateManifest(
	request ArtifactClassificationRequest,
	snapshot ArtifactSnapshot,
	manifestBytes []byte,
) artifactManifestValidationResult {
	profile, reason := v.profileForRequest(request)
	if reason != "" {
		return invalidArtifactManifestContent(reason)
	}
	if !validArtifactSnapshotShape(snapshot) || snapshot.Path != request.ExpectedManifestPath {
		return invalidArtifactManifestContent(ArtifactReasonAttrsMalformed)
	}
	if request.AcceptedManifestLineage != nil &&
		!snapshotMatchesArtifactLineage(snapshot, *request.AcceptedManifestLineage) {
		return invalidArtifactManifestContent(ArtifactReasonRequiredMetadataMismatch)
	}

	manifestDigest := ComputeArtifactDigest(manifestBytes)
	if !snapshotMatchesDigest(snapshot, manifestDigest) {
		return invalidArtifactManifestContent(ArtifactReasonRequiredMetadataMismatch)
	}
	if !artifactHeadersMatch(snapshot, TelemetryContentType, "") {
		return invalidArtifactManifestContent(ArtifactReasonContentHeadersMismatch)
	}
	manifest, err := DecodeTelemetryManifestV1(manifestBytes)
	if err != nil {
		return invalidArtifactManifestContent(ArtifactReasonManifestMalformed)
	}
	if !manifestMatchesReceiptIdentity(manifest, request) || !validManifestRawReference(manifest) {
		return invalidArtifactManifestContent(ArtifactReasonManifestLineageMismatch)
	}
	if request.AcceptedRawLineage != nil &&
		!manifestRawReferenceMatchesLineage(manifest, *request.AcceptedRawLineage) {
		return invalidArtifactManifestContent(ArtifactReasonManifestLineageMismatch)
	}

	expectedManifestMetadata := map[string]string{
		"artifact_kind":     "telemetry_manifest",
		"artifact_version":  strconv.Itoa(TelemetryManifestVersion),
		"batch_id":          request.BatchID,
		"expires_at":        canonicalTime(request.ArtifactExpiresAt),
		"object_generation": strconv.FormatInt(manifest.ObjectGeneration, 10),
		"sha256":            manifestDigest.SHA256,
		"tenant_id":         request.TenantID,
	}
	if !artifactMetadataMatches(snapshot.Metadata, expectedManifestMetadata, manifestDigest.CRC32C) {
		return invalidArtifactManifestContent(ArtifactReasonRequiredMetadataMismatch)
	}

	manifestInput := manifestInputFromClassificationRequest(request)
	expectedManifestBytes, _, err := profile.buildManifest(
		manifestInput,
		storedArtifactFromManifest(manifest),
	)
	if err != nil {
		return invalidArtifactManifestContent(ArtifactReasonResponseUnverifiable)
	}
	if !bytes.Equal(manifestBytes, expectedManifestBytes) {
		return invalidArtifactManifestContent(ArtifactReasonManifestNoncanonical)
	}
	return artifactManifestValidationResult{
		Status: artifactContentValidationValid,
		ReferencedRaw: &artifactValidatedRawReference{
			Target: ArtifactTarget{
				Path:           manifest.ObjectPath,
				Generation:     manifest.ObjectGeneration,
				Metageneration: manifest.ObjectMetageneration,
			},
			SHA256: manifest.ObjectSHA256,
			CRC32C: manifest.ObjectCRC32C,
			Size:   manifest.ObjectSize,
		},
	}
}

func (v *registeredTelemetryArtifactValidator) ValidateRaw(
	request ArtifactClassificationRequest,
	snapshot ArtifactSnapshot,
	compressedBytes []byte,
) artifactContentValidationResult {
	profile, reason := v.profileForRequest(request)
	if reason != "" {
		return invalidArtifactContent(reason)
	}
	if !validArtifactSnapshotShape(snapshot) || snapshot.Path != request.ExpectedRawPath {
		return invalidArtifactContent(ArtifactReasonAttrsMalformed)
	}
	if request.AcceptedRawLineage != nil &&
		!snapshotMatchesArtifactLineage(snapshot, *request.AcceptedRawLineage) {
		return invalidArtifactContent(ArtifactReasonRequiredMetadataMismatch)
	}

	compressedDigest := ComputeArtifactDigest(compressedBytes)
	if !snapshotMatchesDigest(snapshot, compressedDigest) {
		return invalidArtifactContent(ArtifactReasonRequiredMetadataMismatch)
	}
	if !artifactHeadersMatch(snapshot, TelemetryContentType, TelemetryCompression) {
		return invalidArtifactContent(ArtifactReasonContentHeadersMismatch)
	}
	expectedRawMetadata := map[string]string{
		"artifact_kind":    "telemetry_raw",
		"artifact_version": strconv.Itoa(TelemetryManifestVersion),
		"batch_id":         request.BatchID,
		"body_sha256":      request.BodyHash,
		"expires_at":       canonicalTime(request.ArtifactExpiresAt),
		"sha256":           compressedDigest.SHA256,
		"tenant_id":        request.TenantID,
	}
	if !artifactMetadataMatches(snapshot.Metadata, expectedRawMetadata, compressedDigest.CRC32C) {
		return invalidArtifactContent(ArtifactReasonRequiredMetadataMismatch)
	}

	rawBody, ok := boundedSingleStreamGZIP(compressedBytes, profile.maxDecompressedBytes)
	if !ok {
		return invalidArtifactContent(ArtifactReasonStrictPayloadInvalid)
	}
	bodyDigest := sha256.Sum256(rawBody)
	if hex.EncodeToString(bodyDigest[:]) != request.BodyHash {
		return invalidArtifactContent(ArtifactReasonDecompressedBodyHashMismatch)
	}
	recompressed, err := profile.codec.compress(rawBody)
	if err != nil || !bytes.Equal(recompressed, compressedBytes) {
		return invalidArtifactContent(ArtifactReasonCodecProfileUnavailable)
	}

	batch, err := profile.decode(bytes.NewReader(rawBody))
	if err != nil || profile.validate(batch) != nil {
		return invalidArtifactContent(ArtifactReasonStrictPayloadInvalid)
	}
	if !telemetryBatchMatchesClassificationRequest(batch, request) {
		return invalidArtifactContent(ArtifactReasonPayloadLineageMismatch)
	}
	return validArtifactContent()
}

func (v *registeredTelemetryArtifactValidator) profileForRequest(
	request ArtifactClassificationRequest,
) (telemetryArtifactValidatorProfile, ArtifactReasonCode) {
	if v == nil {
		return telemetryArtifactValidatorProfile{}, ArtifactReasonValidatorUnavailable
	}
	profile, supported := v.registry.profiles[request.ValidatorVersion]
	if !supported {
		return telemetryArtifactValidatorProfile{}, ArtifactReasonValidatorUnavailable
	}
	if !profile.codec.available {
		return telemetryArtifactValidatorProfile{}, ArtifactReasonCodecProfileUnavailable
	}
	if ValidateArtifactClassificationRequest(request) != nil {
		return telemetryArtifactValidatorProfile{}, ArtifactReasonResponseUnverifiable
	}
	return profile, ""
}

func invalidArtifactManifestContent(reason ArtifactReasonCode) artifactManifestValidationResult {
	return artifactManifestValidationResult{
		Status:     artifactContentValidationInvalid,
		ReasonCode: reason,
	}
}

func highestPriorityArtifactContentReason(reasons ...ArtifactReasonCode) ArtifactReasonCode {
	best := ArtifactReasonCode("")
	bestPriority := int(^uint(0) >> 1)
	for _, reason := range reasons {
		if reason == "" {
			continue
		}
		priority := artifactContentReasonPriority(reason)
		if priority < bestPriority {
			best = reason
			bestPriority = priority
		}
	}
	return best
}

func artifactContentReasonPriority(reason ArtifactReasonCode) int {
	switch reason {
	case ArtifactReasonValidatorUnavailable,
		ArtifactReasonCodecProfileUnavailable,
		ArtifactReasonResponseUnverifiable:
		return 0
	case ArtifactReasonAttrsMalformed,
		ArtifactReasonRequiredMetadataMismatch,
		ArtifactReasonContentHeadersMismatch:
		return 1
	case ArtifactReasonManifestMalformed,
		ArtifactReasonManifestNoncanonical,
		ArtifactReasonManifestLineageMismatch:
		return 2
	case ArtifactReasonDecompressedBodyHashMismatch,
		ArtifactReasonPayloadLineageMismatch,
		ArtifactReasonStrictPayloadInvalid:
		return 3
	default:
		// An unexpected internal reason must fail ahead of content-conflict
		// results instead of being silently dropped or reordered below them.
		return 0
	}
}

func validArtifactSnapshotShape(snapshot ArtifactSnapshot) bool {
	return snapshot.Path != "" && snapshot.Size > 0 &&
		snapshot.Generation > 0 && snapshot.Metageneration > 0 &&
		snapshot.Metadata != nil && !snapshot.SoftDeleted
}

func snapshotMatchesArtifactLineage(snapshot ArtifactSnapshot, lineage ArtifactLineage) bool {
	return snapshot.Path == lineage.Path && snapshot.SHA256 == lineage.SHA256 &&
		snapshot.CRC32C == lineage.CRC32C && snapshot.Size == lineage.Size &&
		snapshot.Generation == lineage.Generation && snapshot.Metageneration == lineage.Metageneration
}

func validatedRawReferenceMatchesSnapshot(reference artifactValidatedRawReference, snapshot ArtifactSnapshot) bool {
	return reference.Target.Path == snapshot.Path &&
		reference.Target.Generation == snapshot.Generation &&
		reference.Target.Metageneration == snapshot.Metageneration &&
		reference.SHA256 == snapshot.SHA256 && reference.CRC32C == snapshot.CRC32C &&
		reference.Size == snapshot.Size
}

func snapshotMatchesDigest(snapshot ArtifactSnapshot, digest ArtifactDigest) bool {
	return snapshot.SHA256 == digest.SHA256 && snapshot.CRC32C == digest.CRC32C && snapshot.Size == digest.Size
}

func artifactHeadersMatch(snapshot ArtifactSnapshot, contentType, contentEncoding string) bool {
	return snapshot.ContentType == contentType && snapshot.ContentEncoding == contentEncoding &&
		snapshot.CacheControl == artifactCacheControlNoStore
}

func artifactMetadataMatches(actual, expected map[string]string, checksum uint32) bool {
	if actual == nil {
		return false
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	var checksumBytes [4]byte
	binary.BigEndian.PutUint32(checksumBytes[:], checksum)
	encodedChecksum := base64.StdEncoding.EncodeToString(checksumBytes[:])
	allowedExtras := map[string]string{
		"x_emulator_crc32c":  encodedChecksum,
		"x_emulator_upload":  "multipart",
		"x_testbench_crc32c": encodedChecksum,
		"x_testbench_upload": "multipart",
	}
	for key, value := range actual {
		if _, required := expected[key]; required {
			continue
		}
		if allowedValue, allowed := allowedExtras[key]; !allowed || value != allowedValue {
			return false
		}
	}
	return true
}

func storedArtifactFromSnapshot(snapshot ArtifactSnapshot) StoredArtifact {
	return StoredArtifact{
		Path:           snapshot.Path,
		SHA256:         snapshot.SHA256,
		CRC32C:         snapshot.CRC32C,
		Size:           snapshot.Size,
		Generation:     snapshot.Generation,
		Metageneration: snapshot.Metageneration,
	}
}

func storedArtifactFromManifest(manifest TelemetryManifest) StoredArtifact {
	return StoredArtifact{
		Path:           manifest.ObjectPath,
		SHA256:         manifest.ObjectSHA256,
		CRC32C:         manifest.ObjectCRC32C,
		Size:           manifest.ObjectSize,
		Generation:     manifest.ObjectGeneration,
		Metageneration: manifest.ObjectMetageneration,
	}
}

func manifestMatchesReceiptIdentity(
	manifest TelemetryManifest,
	request ArtifactClassificationRequest,
) bool {
	if manifest.PayloadSchemaVersion != request.PayloadSchemaVersion ||
		manifest.TenantID != request.TenantID ||
		manifest.DeviceID != request.DeviceID ||
		manifest.TripID != request.TripID ||
		manifest.InstallationID != request.InstallationID ||
		manifest.BatchID != request.BatchID ||
		manifest.ClientBatchID != request.ClientBatchID ||
		manifest.ConsentRevisionID != request.ConsentRevisionID ||
		manifest.BodyHash != request.BodyHash ||
		manifest.SampleCount != request.ExpectedSampleCount ||
		manifest.ValidatorVersion != request.ValidatorVersion ||
		manifest.ObjectPath != request.ExpectedRawPath {
		return false
	}
	return manifestTimeEquals(manifest.FirstCapturedAt, request.FirstCapturedAt) &&
		manifestTimeEquals(manifest.LastCapturedAt, request.LastCapturedAt) &&
		manifestTimeEquals(manifest.ReceivedAt, request.ReceivedAt) &&
		manifestTimeEquals(manifest.ExpiresAt, request.ArtifactExpiresAt)
}

func manifestTimeEquals(raw string, expected time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && parsed.Equal(expected)
}

func validManifestRawReference(manifest TelemetryManifest) bool {
	return isLowerHexDigest(manifest.ObjectSHA256) && manifest.ObjectSize > 0 &&
		manifest.ObjectGeneration > 0 && manifest.ObjectMetageneration > 0
}

func manifestRawReferenceMatchesLineage(manifest TelemetryManifest, lineage ArtifactLineage) bool {
	return manifest.ObjectPath == lineage.Path && manifest.ObjectSHA256 == lineage.SHA256 &&
		manifest.ObjectCRC32C == lineage.CRC32C && manifest.ObjectSize == lineage.Size &&
		manifest.ObjectGeneration == lineage.Generation &&
		manifest.ObjectMetageneration == lineage.Metageneration
}

func manifestInputFromClassificationRequest(request ArtifactClassificationRequest) BatchManifestInput {
	return BatchManifestInput{
		PayloadSchemaVersion: request.PayloadSchemaVersion,
		TenantID:             request.TenantID,
		DeviceID:             request.DeviceID,
		TripID:               request.TripID,
		InstallationID:       request.InstallationID,
		BatchID:              request.BatchID,
		ClientBatchID:        request.ClientBatchID,
		ConsentRevisionID:    request.ConsentRevisionID,
		BodyHash:             request.BodyHash,
		SampleCount:          request.ExpectedSampleCount,
		FirstCapturedAt:      request.FirstCapturedAt,
		LastCapturedAt:       request.LastCapturedAt,
		ReceivedAt:           request.ReceivedAt,
		ArtifactExpiresAt:    request.ArtifactExpiresAt,
		ValidatorVersion:     request.ValidatorVersion,
	}
}

func boundedSingleStreamGZIP(compressed []byte, maxDecompressedBytes int64) ([]byte, bool) {
	if len(compressed) == 0 || maxDecompressedBytes <= 0 {
		return nil, false
	}
	source := bytes.NewReader(compressed)
	reader, err := gzip.NewReader(source)
	if err != nil {
		return nil, false
	}
	reader.Multistream(false)

	decompressed, readErr := io.ReadAll(io.LimitReader(reader, maxDecompressedBytes+1))
	if readErr != nil || int64(len(decompressed)) > maxDecompressedBytes {
		_ = reader.Close()
		return nil, false
	}
	var probe [1]byte
	readCount, terminalErr := reader.Read(probe[:])
	if readCount != 0 || terminalErr != io.EOF {
		_ = reader.Close()
		return nil, false
	}
	if err := reader.Close(); err != nil || source.Len() != 0 {
		return nil, false
	}
	return decompressed, true
}

func telemetryBatchMatchesClassificationRequest(
	batch telemetry.Batch,
	request ArtifactClassificationRequest,
) bool {
	if batch.SchemaVersion != request.PayloadSchemaVersion ||
		batch.TenantID != request.TenantID ||
		batch.DeviceID != request.DeviceID ||
		batch.TripID != request.TripID ||
		batch.InstallationID != request.InstallationID ||
		batch.ConsentRevisionID != request.ConsentRevisionID ||
		batch.ClientBatchID != request.ClientBatchID ||
		len(batch.Samples) != request.ExpectedSampleCount {
		return false
	}
	firstCapturedAt, lastCapturedAt := capturedAtBounds(batch)
	return firstCapturedAt.Equal(request.FirstCapturedAt) &&
		lastCapturedAt.Equal(request.LastCapturedAt)
}
