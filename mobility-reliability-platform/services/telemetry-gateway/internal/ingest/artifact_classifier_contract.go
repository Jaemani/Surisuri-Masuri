package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var (
	ErrInvalidArtifactClassificationRequest = errors.New("artifact classification request is invalid")
	ErrInvalidArtifactReadAuthorization     = errors.New("artifact read authorization is invalid")
	ErrArtifactReadAuthorizationExpired     = errors.New("artifact read authorization has expired")
	ErrArtifactPermissionDenied             = errors.New("telemetry artifact provider permission denied")
	ErrArtifactQuotaLimited                 = errors.New("telemetry artifact provider quota limited")
	ErrArtifactProviderTimeout              = errors.New("telemetry artifact provider timed out")
	ErrArtifactProviderCancelled            = errors.New("telemetry artifact provider cancelled the operation")
	ErrArtifactProviderUnavailable          = errors.New("telemetry artifact provider is unavailable")
	ErrArtifactResponseUnverifiable         = errors.New("telemetry artifact provider response is unverifiable")
	ErrArtifactReadLimitExceeded            = errors.New("telemetry artifact read limit exceeded")
	ErrArtifactGenerationNotFound           = errors.New("telemetry artifact generation was not found")
	ErrArtifactPreconditionDrift            = errors.New("telemetry artifact generation precondition drifted")
)

// ArtifactReadPurpose separates pending forward recovery from audits of an
// already accepted immutable lineage. A grant minted for one purpose cannot be
// reused for the other.
type ArtifactReadPurpose string

const (
	ArtifactReadForwardRecovery        ArtifactReadPurpose = "forward_recovery"
	ArtifactReadAcceptedIntegrityAudit ArtifactReadPurpose = "accepted_integrity_audit"
)

// ArtifactClassification is intentionally coarse. ReasonCode carries the
// bounded detail without turning a classification into a recovery action.
type ArtifactClassification string

const (
	ArtifactClassificationNone               ArtifactClassification = "none"
	ArtifactClassificationValidRawOnly       ArtifactClassification = "valid_raw_only"
	ArtifactClassificationValidComplete      ArtifactClassification = "valid_complete"
	ArtifactClassificationManifestOnly       ArtifactClassification = "manifest_only"
	ArtifactClassificationRawContentConflict ArtifactClassification = "raw_content_conflict"
	ArtifactClassificationManifestConflict   ArtifactClassification = "manifest_conflict"
	ArtifactClassificationMetadataConflict   ArtifactClassification = "metadata_conflict"
	ArtifactClassificationGenerationDrift    ArtifactClassification = "generation_drift"
	ArtifactClassificationStoredMissing      ArtifactClassification = "stored_missing"
	ArtifactClassificationUnavailable        ArtifactClassification = "unavailable"
)

type ArtifactReasonCode string

const (
	ArtifactReasonNoCandidates                            ArtifactReasonCode = "no_candidates"
	ArtifactReasonRawValidManifestAbsent                  ArtifactReasonCode = "raw_valid_manifest_absent"
	ArtifactReasonManifestAndReferencedRawValid           ArtifactReasonCode = "manifest_and_referenced_raw_valid"
	ArtifactReasonReferencedRawNotFound                   ArtifactReasonCode = "referenced_raw_not_found"
	ArtifactReasonDecompressedBodyHashMismatch            ArtifactReasonCode = "decompressed_body_hash_mismatch"
	ArtifactReasonPayloadLineageMismatch                  ArtifactReasonCode = "payload_lineage_mismatch"
	ArtifactReasonStrictPayloadInvalid                    ArtifactReasonCode = "strict_payload_invalid"
	ArtifactReasonManifestMalformed                       ArtifactReasonCode = "manifest_malformed"
	ArtifactReasonManifestNoncanonical                    ArtifactReasonCode = "manifest_noncanonical"
	ArtifactReasonManifestLineageMismatch                 ArtifactReasonCode = "manifest_lineage_mismatch"
	ArtifactReasonAttrsMalformed                          ArtifactReasonCode = "attrs_malformed"
	ArtifactReasonRequiredMetadataMismatch                ArtifactReasonCode = "required_metadata_mismatch"
	ArtifactReasonContentHeadersMismatch                  ArtifactReasonCode = "content_headers_mismatch"
	ArtifactReasonMultipleManifestGenerations             ArtifactReasonCode = "multiple_manifest_generations"
	ArtifactReasonMultipleRawGenerations                  ArtifactReasonCode = "multiple_raw_generations"
	ArtifactReasonReferencedGenerationMissingOtherPresent ArtifactReasonCode = "referenced_generation_missing_other_present"
	ArtifactReasonAcceptedGenerationMissingOtherPresent   ArtifactReasonCode = "accepted_generation_missing_other_present"
	ArtifactReasonSoftDeletedCandidatePresent             ArtifactReasonCode = "soft_deleted_candidate_present"
	ArtifactReasonGenerationChangedDuringRead             ArtifactReasonCode = "generation_changed_during_read"
	ArtifactReasonMetagenerationChangedDuringRead         ArtifactReasonCode = "metageneration_changed_during_read"
	ArtifactReasonAcceptedManifestMissing                 ArtifactReasonCode = "accepted_manifest_missing"
	ArtifactReasonAcceptedRawMissing                      ArtifactReasonCode = "accepted_raw_missing"
	ArtifactReasonAcceptedBothMissing                     ArtifactReasonCode = "accepted_both_missing"
	ArtifactReasonAcceptedGenerationSoftDeleted           ArtifactReasonCode = "accepted_generation_soft_deleted"
	ArtifactReasonPermissionDenied                        ArtifactReasonCode = "permission_denied"
	ArtifactReasonQuotaLimited                            ArtifactReasonCode = "quota_limited"
	ArtifactReasonProviderTimeout                         ArtifactReasonCode = "provider_timeout"
	ArtifactReasonProviderCancelled                       ArtifactReasonCode = "provider_cancelled"
	ArtifactReasonProviderUnavailable                     ArtifactReasonCode = "provider_unavailable"
	ArtifactReasonValidatorUnavailable                    ArtifactReasonCode = "validator_unavailable"
	ArtifactReasonCodecProfileUnavailable                 ArtifactReasonCode = "codec_profile_unavailable"
	ArtifactReasonInventoryCoverageIncomplete             ArtifactReasonCode = "inventory_coverage_incomplete"
	ArtifactReasonResponseUnverifiable                    ArtifactReasonCode = "response_unverifiable"
)

type ArtifactRetentionPhase string

const (
	ArtifactRetentionBeforeExpiry  ArtifactRetentionPhase = "before_expiry"
	ArtifactRetentionAtAfterExpiry ArtifactRetentionPhase = "at_or_after_expiry"
)

type ArtifactInventoryCoverage string

const (
	ArtifactInventoryCoverageUnknown    ArtifactInventoryCoverage = "unknown"
	ArtifactInventoryCoverageComplete   ArtifactInventoryCoverage = "complete"
	ArtifactInventoryCoverageIncomplete ArtifactInventoryCoverage = "incomplete"
)

// ArtifactSnapshot is a provider-neutral view of one exact object generation.
// Metadata is internal classifier input and must not be logged wholesale.
type ArtifactSnapshot struct {
	Path            string
	SHA256          string
	CRC32C          uint32
	Size            int64
	Generation      int64
	Metageneration  int64
	ContentType     string
	ContentEncoding string
	CacheControl    string
	Metadata        map[string]string
	SoftDeleted     bool
}

type ArtifactGenerationSet struct {
	Performed  bool
	Truncated  bool
	Candidates []ArtifactSnapshot
}

// GenerationInventory keeps regular live/noncurrent versions separate from
// soft-deleted versions because GCS exposes them through separate queries.
type GenerationInventory struct {
	NonSoftDeleted ArtifactGenerationSet
	SoftDeleted    ArtifactGenerationSet
	Coverage       ArtifactInventoryCoverage
}

type ArtifactTarget struct {
	Path           string
	Generation     int64
	Metageneration int64
}

type ArtifactInventoryReader interface {
	ListExactPathGenerations(context.Context, string, int) (GenerationInventory, error)
	InspectGeneration(context.Context, string, int64) (ArtifactSnapshot, error)
	ReadManifestGeneration(context.Context, ArtifactTarget, int64) ([]byte, error)
	ReadRawGenerationCompressed(context.Context, ArtifactTarget, int64) ([]byte, error)
}

// ArtifactLineage is the receipt-pinned identity of an accepted artifact. It
// deliberately excludes replay state and provider-specific handles.
type ArtifactLineage struct {
	Path           string
	SHA256         string
	CRC32C         uint32
	Size           int64
	Generation     int64
	Metageneration int64
}

type ArtifactClassificationRequest struct {
	Purpose ArtifactReadPurpose

	ReceiptID       string
	ReservationKey  string
	ReceiptState    ReceiptState
	ReceiptRevision int64

	TenantID             string
	DeviceID             string
	TripID               string
	InstallationID       string
	BatchID              string
	ClientBatchID        string
	ConsentRevisionID    string
	PayloadSchemaVersion string
	ValidatorVersion     string
	BodyHash             string
	ExpectedSampleCount  int
	FirstCapturedAt      time.Time
	LastCapturedAt       time.Time
	ReceivedAt           time.Time
	ArtifactExpiresAt    time.Time
	ExpectedRawPath      string
	ExpectedManifestPath string

	AcceptedRawLineage      *ArtifactLineage
	AcceptedManifestLineage *ArtifactLineage
	ForwardFence            *LeaseFence
}

type ArtifactInventorySummary struct {
	Performed           bool
	NonSoftDeletedCount int
	SoftDeletedCount    int
	Truncated           bool
	Coverage            ArtifactInventoryCoverage
}

type ArtifactPinnedLineage struct {
	SHA256         string
	CRC32C         uint32
	Size           int64
	Generation     int64
	Metageneration int64
}

type ArtifactClassificationResult struct {
	Classification     ArtifactClassification
	ReasonCode         ArtifactReasonCode
	RetentionPhase     ArtifactRetentionPhase
	ManifestInventory  ArtifactInventorySummary
	RawInventory       ArtifactInventorySummary
	PinnedManifest     *ArtifactPinnedLineage
	PinnedRaw          *ArtifactPinnedLineage
	ValidatorVersion   string
	ObservedAt         time.Time
	requestBindingHash [sha256.Size]byte
}

type ArtifactClassifier interface {
	Classify(
		context.Context,
		ArtifactReadAuthorizationGrant,
		ArtifactClassificationRequest,
	) (ArtifactClassificationResult, error)
}

// ArtifactReadAuthorizationGrant is an opaque in-process capability. All
// fields are unexported so code outside package ingest cannot construct a
// trusted grant by filling a DTO. The zero value is always invalid.
type ArtifactReadAuthorizationGrant struct {
	issuer             artifactReadGrantIssuer
	policyVersion      string
	checkedAt          time.Time
	expiresAt          time.Time
	requestBindingHash [sha256.Size]byte
	capabilitySeal     [sha256.Size]byte
}

type artifactReadGrantIssuer uint8

const (
	artifactReadGrantIssuerUnknown artifactReadGrantIssuer = iota
	artifactReadGrantIssuerForwardRecovery
	artifactReadGrantIssuerAcceptedIntegrityAudit
)

const artifactRequestBindingVersion = "telemetry-artifact-read-request@1"
const artifactGrantSealVersion = "telemetry-artifact-read-grant@1"

// ValidateArtifactClassificationRequest validates only receipt-derived shape
// and invariants. It performs no provider calls and is safe to invoke before a
// reader is selected.
func ValidateArtifactClassificationRequest(request ArtifactClassificationRequest) error {
	if !validArtifactReadPurpose(request.Purpose) {
		return invalidArtifactClassificationRequest("purpose")
	}
	identifiers := []struct {
		field string
		value string
	}{
		{field: "receipt_id", value: request.ReceiptID},
		{field: "tenant_id", value: request.TenantID},
		{field: "device_id", value: request.DeviceID},
		{field: "trip_id", value: request.TripID},
		{field: "installation_id", value: request.InstallationID},
		{field: "batch_id", value: request.BatchID},
		{field: "client_batch_id", value: request.ClientBatchID},
		{field: "consent_revision_id", value: request.ConsentRevisionID},
	}
	for _, identifier := range identifiers {
		if !telemetry.IsUUID(identifier.value) {
			return invalidArtifactClassificationRequest(identifier.field)
		}
	}
	if request.ReceiptID != request.BatchID {
		return invalidArtifactClassificationRequest("receipt_id")
	}
	if !isLowerHexDigest(request.ReservationKey) || request.ReservationKey != DeriveReservationKey(
		request.PayloadSchemaVersion,
		request.TenantID,
		request.InstallationID,
		request.ClientBatchID,
	) {
		return invalidArtifactClassificationRequest("reservation_key")
	}
	if request.ReceiptRevision <= 0 {
		return invalidArtifactClassificationRequest("receipt_revision")
	}
	if request.PayloadSchemaVersion != telemetry.SchemaVersionV2 {
		return invalidArtifactClassificationRequest("payload_schema_version")
	}
	if !validArtifactServerLabel(request.ValidatorVersion) {
		return invalidArtifactClassificationRequest("validator_version")
	}
	if !isLowerHexDigest(request.BodyHash) {
		return invalidArtifactClassificationRequest("body_hash")
	}
	if request.ExpectedSampleCount < 1 || request.ExpectedSampleCount > telemetry.MaxSamples {
		return invalidArtifactClassificationRequest("expected_sample_count")
	}
	if request.FirstCapturedAt.IsZero() || request.LastCapturedAt.IsZero() ||
		request.LastCapturedAt.Before(request.FirstCapturedAt) {
		return invalidArtifactClassificationRequest("captured_at")
	}
	if request.ReceivedAt.IsZero() || request.ArtifactExpiresAt.IsZero() ||
		!request.ReceivedAt.Before(request.ArtifactExpiresAt) ||
		!request.ArtifactExpiresAt.Equal(request.ReceivedAt.Add(TelemetryArtifactRetention)) {
		return invalidArtifactClassificationRequest("artifact_expires_at")
	}

	manifestInput := BatchManifestInput{
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
	if request.ExpectedRawPath != ExpectedTelemetryObjectPath(manifestInput) {
		return invalidArtifactClassificationRequest("expected_raw_path")
	}
	if request.ExpectedManifestPath != ExpectedTelemetryManifestPath(manifestInput) {
		return invalidArtifactClassificationRequest("expected_manifest_path")
	}

	switch request.Purpose {
	case ArtifactReadForwardRecovery:
		if request.ReceiptState != ReceiptReserved {
			return invalidArtifactClassificationRequest("receipt_state")
		}
		if request.AcceptedRawLineage != nil || request.AcceptedManifestLineage != nil {
			return invalidArtifactClassificationRequest("accepted_lineage")
		}
		if request.ForwardFence == nil || ValidateLeaseFence(*request.ForwardFence) != nil {
			return invalidArtifactClassificationRequest("forward_fence")
		}
	case ArtifactReadAcceptedIntegrityAudit:
		if !acceptedIntegrityReceiptState(request.ReceiptState) {
			return invalidArtifactClassificationRequest("receipt_state")
		}
		if request.ForwardFence != nil {
			return invalidArtifactClassificationRequest("forward_fence")
		}
		if validateArtifactLineage(request.AcceptedRawLineage, request.ExpectedRawPath) != nil {
			return invalidArtifactClassificationRequest("accepted_raw_lineage")
		}
		if validateArtifactLineage(request.AcceptedManifestLineage, request.ExpectedManifestPath) != nil {
			return invalidArtifactClassificationRequest("accepted_manifest_lineage")
		}
	}
	return nil
}

// ValidateArtifactReadAuthorization is the pure gate a classifier must invoke
// before each reader boundary. observedAt must come from the classifier's
// trusted clock, not from the request or an external caller.
func ValidateArtifactReadAuthorization(
	grant ArtifactReadAuthorizationGrant,
	request ArtifactClassificationRequest,
	observedAt time.Time,
) error {
	if err := ValidateArtifactClassificationRequest(request); err != nil {
		return err
	}
	if observedAt.IsZero() {
		return invalidArtifactReadAuthorization("observed_at")
	}
	if !validArtifactReadGrantIssuer(grant.issuer) ||
		!issuerAllowsPurpose(grant.issuer, request.Purpose) ||
		!validArtifactServerLabel(grant.policyVersion) ||
		grant.checkedAt.IsZero() || grant.expiresAt.IsZero() ||
		!grant.checkedAt.Before(grant.expiresAt) {
		return invalidArtifactReadAuthorization("grant")
	}

	wantBinding := canonicalArtifactClassificationRequestBinding(request)
	if grant.requestBindingHash != wantBinding {
		return invalidArtifactReadAuthorization("request_binding")
	}
	wantSeal := artifactReadCapabilitySeal(
		grant.issuer,
		grant.policyVersion,
		grant.checkedAt,
		grant.expiresAt,
		grant.requestBindingHash,
	)
	if grant.capabilitySeal != wantSeal {
		return invalidArtifactReadAuthorization("capability")
	}
	if observedAt.Before(grant.checkedAt) {
		return invalidArtifactReadAuthorization("observed_at")
	}
	if !observedAt.Before(grant.expiresAt) {
		return ErrArtifactReadAuthorizationExpired
	}
	if request.Purpose == ArtifactReadForwardRecovery &&
		!observedAt.Before(request.ForwardFence.ExpiresAt) {
		return ErrArtifactReadAuthorizationExpired
	}
	return nil
}

// mintArtifactReadAuthorizationGrant is intentionally unexported. Production
// authorizers in package ingest choose one of the trusted issuer constants
// only after their external policy checks have succeeded.
func mintArtifactReadAuthorizationGrant(
	issuer artifactReadGrantIssuer,
	policyVersion string,
	request ArtifactClassificationRequest,
	checkedAt time.Time,
	expiresAt time.Time,
) (ArtifactReadAuthorizationGrant, error) {
	if err := ValidateArtifactClassificationRequest(request); err != nil {
		return ArtifactReadAuthorizationGrant{}, err
	}
	if !validArtifactReadGrantIssuer(issuer) || !issuerAllowsPurpose(issuer, request.Purpose) {
		return ArtifactReadAuthorizationGrant{}, invalidArtifactReadAuthorization("issuer")
	}
	if !validArtifactServerLabel(policyVersion) {
		return ArtifactReadAuthorizationGrant{}, invalidArtifactReadAuthorization("policy_version")
	}
	if checkedAt.IsZero() || expiresAt.IsZero() || !checkedAt.Before(expiresAt) {
		return ArtifactReadAuthorizationGrant{}, invalidArtifactReadAuthorization("grant_time")
	}
	if request.Purpose == ArtifactReadForwardRecovery && !checkedAt.Before(request.ForwardFence.ExpiresAt) {
		return ArtifactReadAuthorizationGrant{}, ErrArtifactReadAuthorizationExpired
	}

	binding := canonicalArtifactClassificationRequestBinding(request)
	grant := ArtifactReadAuthorizationGrant{
		issuer:             issuer,
		policyVersion:      policyVersion,
		checkedAt:          checkedAt.UTC(),
		expiresAt:          expiresAt.UTC(),
		requestBindingHash: binding,
	}
	grant.capabilitySeal = artifactReadCapabilitySeal(
		grant.issuer,
		grant.policyVersion,
		grant.checkedAt,
		grant.expiresAt,
		grant.requestBindingHash,
	)
	return grant, nil
}

func canonicalArtifactClassificationRequestBinding(request ArtifactClassificationRequest) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(artifactRequestBindingVersion)
	encoder.addString(string(request.Purpose))
	encoder.addString(request.ReceiptID)
	encoder.addString(request.ReservationKey)
	encoder.addString(string(request.ReceiptState))
	encoder.addInt64(request.ReceiptRevision)
	encoder.addString(request.TenantID)
	encoder.addString(request.DeviceID)
	encoder.addString(request.TripID)
	encoder.addString(request.InstallationID)
	encoder.addString(request.BatchID)
	encoder.addString(request.ClientBatchID)
	encoder.addString(request.ConsentRevisionID)
	encoder.addString(request.PayloadSchemaVersion)
	encoder.addString(request.ValidatorVersion)
	encoder.addString(request.BodyHash)
	encoder.addInt64(int64(request.ExpectedSampleCount))
	encoder.addTime(request.FirstCapturedAt)
	encoder.addTime(request.LastCapturedAt)
	encoder.addTime(request.ReceivedAt)
	encoder.addTime(request.ArtifactExpiresAt)
	encoder.addString(request.ExpectedRawPath)
	encoder.addString(request.ExpectedManifestPath)
	encoder.addArtifactLineage(request.AcceptedRawLineage)
	encoder.addArtifactLineage(request.AcceptedManifestLineage)
	encoder.addLeaseFence(request.ForwardFence)
	return encoder.sum()
}

func artifactReadCapabilitySeal(
	issuer artifactReadGrantIssuer,
	policyVersion string,
	checkedAt time.Time,
	expiresAt time.Time,
	requestBinding [sha256.Size]byte,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(artifactGrantSealVersion)
	encoder.addInt64(int64(issuer))
	encoder.addString(policyVersion)
	encoder.addTime(checkedAt)
	encoder.addTime(expiresAt)
	encoder.addBytes(requestBinding[:])
	return encoder.sum()
}

func validateArtifactLineage(lineage *ArtifactLineage, expectedPath string) error {
	if lineage == nil || lineage.Path != expectedPath || !isLowerHexDigest(lineage.SHA256) ||
		lineage.Size <= 0 || lineage.Generation <= 0 || lineage.Metageneration <= 0 {
		return ErrInvalidArtifactClassificationRequest
	}
	return nil
}

func validArtifactReadPurpose(purpose ArtifactReadPurpose) bool {
	return purpose == ArtifactReadForwardRecovery || purpose == ArtifactReadAcceptedIntegrityAudit
}

func acceptedIntegrityReceiptState(state ReceiptState) bool {
	switch state {
	case ReceiptStored, ReceiptQueued, ReceiptProjected:
		return true
	default:
		return false
	}
}

func validArtifactReadGrantIssuer(issuer artifactReadGrantIssuer) bool {
	return issuer == artifactReadGrantIssuerForwardRecovery ||
		issuer == artifactReadGrantIssuerAcceptedIntegrityAudit
}

func issuerAllowsPurpose(issuer artifactReadGrantIssuer, purpose ArtifactReadPurpose) bool {
	return issuer == artifactReadGrantIssuerForwardRecovery && purpose == ArtifactReadForwardRecovery ||
		issuer == artifactReadGrantIssuerAcceptedIntegrityAudit && purpose == ArtifactReadAcceptedIntegrityAudit
}

func validArtifactServerLabel(value string) bool {
	if value == "" || len(value) > 128 || !artifactLabelAlphaNumeric(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		character := value[i]
		if artifactLabelAlphaNumeric(character) {
			continue
		}
		switch character {
		case '.', '_', '@', '+', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func artifactLabelAlphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}

func invalidArtifactClassificationRequest(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArtifactClassificationRequest, field)
}

func invalidArtifactReadAuthorization(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArtifactReadAuthorization, field)
}

type artifactBindingEncoder struct {
	digest hash.Hash
}

func newArtifactBindingEncoder(domain string) *artifactBindingEncoder {
	encoder := &artifactBindingEncoder{digest: sha256.New()}
	encoder.addString(domain)
	return encoder
}

func (e *artifactBindingEncoder) addString(value string) {
	e.addBytes([]byte(value))
}

func (e *artifactBindingEncoder) addBytes(value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = e.digest.Write(length[:])
	_, _ = e.digest.Write(value)
}

func (e *artifactBindingEncoder) addInt64(value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	e.addBytes(encoded[:])
}

func (e *artifactBindingEncoder) addUint32(value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	e.addBytes(encoded[:])
}

func (e *artifactBindingEncoder) addTime(value time.Time) {
	e.addString(value.UTC().Format(time.RFC3339Nano))
}

func (e *artifactBindingEncoder) addArtifactLineage(lineage *ArtifactLineage) {
	if lineage == nil {
		e.addBytes(nil)
		return
	}
	e.addBytes([]byte{1})
	e.addString(lineage.Path)
	e.addString(lineage.SHA256)
	e.addUint32(lineage.CRC32C)
	e.addInt64(lineage.Size)
	e.addInt64(lineage.Generation)
	e.addInt64(lineage.Metageneration)
}

func (e *artifactBindingEncoder) addLeaseFence(fence *LeaseFence) {
	if fence == nil {
		e.addBytes(nil)
		return
	}
	e.addBytes([]byte{1})
	e.addString(fence.OwnerID)
	e.addInt64(fence.Token)
	e.addTime(fence.ExpiresAt)
}

func (e *artifactBindingEncoder) sum() [sha256.Size]byte {
	var result [sha256.Size]byte
	copy(result[:], e.digest.Sum(nil))
	return result
}
