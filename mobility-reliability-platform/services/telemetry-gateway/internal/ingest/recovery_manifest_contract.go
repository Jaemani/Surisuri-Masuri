package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

var (
	ErrInvalidRecoveryManifestWrite       = errors.New("recovery manifest write is invalid")
	ErrInvalidManifestRepairAuthorization = errors.New("manifest repair authorization is invalid")
	ErrManifestRepairAuthorizationExpired = errors.New("manifest repair authorization has expired")
)

// RecoveryManifestWrite is the only artifact mutation allowed during forward
// reconciliation. It deliberately contains no raw body and exposes no raw
// create, rewrite, or delete operation.
type RecoveryManifestWrite struct {
	ManifestPath  string
	ManifestInput BatchManifestInput
	Raw           StoredArtifact
	CanonicalBody []byte
	Digest        ArtifactDigest
}

// TelemetryManifestRecoveryStore creates one immutable manifest. A read grant
// cannot be passed because manifest repair uses a separate opaque capability.
type TelemetryManifestRecoveryStore interface {
	CreateManifest(
		context.Context,
		ManifestRepairAuthorizationGrant,
		RecoveryManifestWrite,
	) (StoredArtifact, error)
}

// ManifestRepairAuthorizationGrant is an opaque, short-lived in-process
// capability. Package-external adapters can validate it but cannot construct
// a non-zero trusted value.
type ManifestRepairAuthorizationGrant struct {
	policyVersion      string
	checkedAt          time.Time
	expiresAt          time.Time
	receiptRevision    int64
	forwardFence       LeaseFence
	requestBindingHash [sha256.Size]byte
	writeBindingHash   [sha256.Size]byte
	capabilitySeal     [sha256.Size]byte
}

const (
	manifestRepairWriteBindingVersion = "telemetry-manifest-repair-write@1"
	manifestRepairGrantSealVersion    = "telemetry-manifest-repair-grant@1"
)

func ValidateRecoveryManifestWrite(write RecoveryManifestWrite) error {
	if write.ManifestPath == "" || write.ManifestPath != ExpectedTelemetryManifestPath(write.ManifestInput) ||
		write.Raw.Path != ExpectedTelemetryObjectPath(write.ManifestInput) || write.Raw.Replay ||
		validateManifestInput(write.ManifestInput, write.Raw) != nil ||
		len(write.CanonicalBody) == 0 || len(write.CanonicalBody) > MaxTelemetryManifestBytes {
		return ErrInvalidRecoveryManifestWrite
	}
	wantDigest := ComputeArtifactDigest(write.CanonicalBody)
	if write.Digest != wantDigest {
		return ErrInvalidRecoveryManifestWrite
	}
	return nil
}

// ValidateManifestRepairAuthorization is the provider-boundary gate. The
// trusted current-state authorizer remains responsible for minting the grant;
// this function verifies its seal, exact write binding, and time/fence bounds.
func ValidateManifestRepairAuthorization(
	grant ManifestRepairAuthorizationGrant,
	write RecoveryManifestWrite,
	observedAt time.Time,
) error {
	if ValidateRecoveryManifestWrite(write) != nil || observedAt.IsZero() {
		return ErrInvalidManifestRepairAuthorization
	}
	if !validArtifactServerLabel(grant.policyVersion) || grant.receiptRevision <= 0 ||
		ValidateLeaseFence(grant.forwardFence) != nil || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.expiresAt.After(grant.forwardFence.ExpiresAt) {
		return ErrInvalidManifestRepairAuthorization
	}
	wantWriteBinding := canonicalRecoveryManifestWriteBinding(write)
	if grant.writeBindingHash != wantWriteBinding {
		return ErrInvalidManifestRepairAuthorization
	}
	wantSeal := manifestRepairCapabilitySeal(grant)
	if grant.capabilitySeal != wantSeal {
		return ErrInvalidManifestRepairAuthorization
	}
	if observedAt.Before(grant.checkedAt) {
		return ErrInvalidManifestRepairAuthorization
	}
	if !observedAt.Before(grant.expiresAt) || !observedAt.Before(grant.forwardFence.ExpiresAt) {
		return ErrManifestRepairAuthorizationExpired
	}
	return nil
}

// ManifestRepairAuthorizationDeadline returns the provider deadline only
// after the exact write binding and opaque seal have been verified.
func ManifestRepairAuthorizationDeadline(
	grant ManifestRepairAuthorizationGrant,
	write RecoveryManifestWrite,
) (time.Time, error) {
	if ValidateRecoveryManifestWrite(write) != nil ||
		!validArtifactServerLabel(grant.policyVersion) || grant.receiptRevision <= 0 ||
		ValidateLeaseFence(grant.forwardFence) != nil || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.expiresAt.After(grant.forwardFence.ExpiresAt) ||
		grant.writeBindingHash != canonicalRecoveryManifestWriteBinding(write) ||
		grant.capabilitySeal != manifestRepairCapabilitySeal(grant) {
		return time.Time{}, ErrInvalidManifestRepairAuthorization
	}
	return grant.expiresAt, nil
}

func (v *registeredTelemetryArtifactValidator) mintManifestRepairAuthorizationGrant(
	policyVersion string,
	request ArtifactClassificationRequest,
	write RecoveryManifestWrite,
	checkedAt time.Time,
	expiresAt time.Time,
) (ManifestRepairAuthorizationGrant, error) {
	if ValidateArtifactClassificationRequest(request) != nil ||
		request.Purpose != ArtifactReadForwardRecovery || request.ForwardFence == nil ||
		v.validateRecoveryManifestWriteForRequest(request, write) != nil ||
		!validArtifactServerLabel(policyVersion) || checkedAt.IsZero() || expiresAt.IsZero() ||
		checkedAt.Before(request.ReceivedAt) || !checkedAt.Before(expiresAt) ||
		!checkedAt.Before(request.ForwardFence.ExpiresAt) ||
		!checkedAt.Before(request.ArtifactExpiresAt) ||
		expiresAt.After(request.ForwardFence.ExpiresAt) ||
		expiresAt.After(request.ArtifactExpiresAt) {
		return ManifestRepairAuthorizationGrant{}, ErrInvalidManifestRepairAuthorization
	}
	grant := ManifestRepairAuthorizationGrant{
		policyVersion:      policyVersion,
		checkedAt:          checkedAt.UTC(),
		expiresAt:          expiresAt.UTC(),
		receiptRevision:    request.ReceiptRevision,
		forwardFence:       *request.ForwardFence,
		requestBindingHash: canonicalArtifactClassificationRequestBinding(request),
		writeBindingHash:   canonicalRecoveryManifestWriteBinding(write),
	}
	grant.capabilitySeal = manifestRepairCapabilitySeal(grant)
	return grant, nil
}

func (v *registeredTelemetryArtifactValidator) validateRecoveryManifestWriteForRequest(
	request ArtifactClassificationRequest,
	write RecoveryManifestWrite,
) error {
	if ValidateRecoveryManifestWrite(write) != nil ||
		!recoveryManifestWriteMatchesRequest(write, request) {
		return ErrInvalidRecoveryManifestWrite
	}
	profile, reason := v.profileForRequest(request)
	if reason != "" {
		return ErrInvalidRecoveryManifestWrite
	}
	wantBody, wantDigest, err := profile.buildManifest(write.ManifestInput, write.Raw)
	if err != nil || !bytes.Equal(write.CanonicalBody, wantBody) || write.Digest != wantDigest {
		return ErrInvalidRecoveryManifestWrite
	}
	return nil
}

func (v *registeredTelemetryArtifactValidator) buildRecoveryManifest(
	request ArtifactClassificationRequest,
	pinnedRaw ArtifactPinnedLineage,
) (RecoveryManifestWrite, ArtifactReasonCode) {
	if request.Purpose != ArtifactReadForwardRecovery ||
		ValidateArtifactClassificationRequest(request) != nil {
		return RecoveryManifestWrite{}, ArtifactReasonResponseUnverifiable
	}
	rawLineage, err := artifactLineageFromPinned(request.ExpectedRawPath, &pinnedRaw)
	if err != nil {
		return RecoveryManifestWrite{}, ArtifactReasonResponseUnverifiable
	}
	profile, reason := v.profileForRequest(request)
	if reason != "" {
		return RecoveryManifestWrite{}, reason
	}
	input := manifestInputFromClassificationRequest(request)
	raw := storedArtifactFromLineage(*rawLineage)
	body, digest, err := profile.buildManifest(input, raw)
	if err != nil {
		return RecoveryManifestWrite{}, ArtifactReasonResponseUnverifiable
	}
	write := RecoveryManifestWrite{
		ManifestPath:  request.ExpectedManifestPath,
		ManifestInput: input,
		Raw:           raw,
		CanonicalBody: body,
		Digest:        digest,
	}
	if ValidateRecoveryManifestWrite(write) != nil {
		return RecoveryManifestWrite{}, ArtifactReasonResponseUnverifiable
	}
	return write, ""
}

func recoveryManifestWriteMatchesRequest(
	write RecoveryManifestWrite,
	request ArtifactClassificationRequest,
) bool {
	if write.ManifestPath != request.ExpectedManifestPath || write.Raw.Path != request.ExpectedRawPath {
		return false
	}
	want := manifestInputFromClassificationRequest(request)
	got := write.ManifestInput
	return got.PayloadSchemaVersion == want.PayloadSchemaVersion &&
		got.TenantID == want.TenantID && got.DeviceID == want.DeviceID &&
		got.TripID == want.TripID && got.InstallationID == want.InstallationID &&
		got.BatchID == want.BatchID && got.ClientBatchID == want.ClientBatchID &&
		got.ConsentRevisionID == want.ConsentRevisionID && got.BodyHash == want.BodyHash &&
		got.SampleCount == want.SampleCount && got.FirstCapturedAt.Equal(want.FirstCapturedAt) &&
		got.LastCapturedAt.Equal(want.LastCapturedAt) && got.ReceivedAt.Equal(want.ReceivedAt) &&
		got.ArtifactExpiresAt.Equal(want.ArtifactExpiresAt) &&
		got.ValidatorVersion == want.ValidatorVersion
}

func canonicalRecoveryManifestWriteBinding(write RecoveryManifestWrite) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(manifestRepairWriteBindingVersion)
	encoder.addString(write.ManifestPath)
	input := write.ManifestInput
	encoder.addString(input.PayloadSchemaVersion)
	encoder.addString(input.TenantID)
	encoder.addString(input.DeviceID)
	encoder.addString(input.TripID)
	encoder.addString(input.InstallationID)
	encoder.addString(input.BatchID)
	encoder.addString(input.ClientBatchID)
	encoder.addString(input.ConsentRevisionID)
	encoder.addString(input.BodyHash)
	encoder.addInt64(int64(input.SampleCount))
	encoder.addTime(input.FirstCapturedAt)
	encoder.addTime(input.LastCapturedAt)
	encoder.addTime(input.ReceivedAt)
	encoder.addTime(input.ArtifactExpiresAt)
	encoder.addString(input.ValidatorVersion)
	encoder.addArtifactLineage(&ArtifactLineage{
		Path:           write.Raw.Path,
		SHA256:         write.Raw.SHA256,
		CRC32C:         write.Raw.CRC32C,
		Size:           write.Raw.Size,
		Generation:     write.Raw.Generation,
		Metageneration: write.Raw.Metageneration,
	})
	encoder.addString(write.Digest.SHA256)
	encoder.addUint32(write.Digest.CRC32C)
	encoder.addInt64(write.Digest.Size)
	return encoder.sum()
}

func manifestRepairCapabilitySeal(grant ManifestRepairAuthorizationGrant) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(manifestRepairGrantSealVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addInt64(grant.receiptRevision)
	encoder.addLeaseFence(&grant.forwardFence)
	encoder.addBytes(grant.requestBindingHash[:])
	encoder.addBytes(grant.writeBindingHash[:])
	return encoder.sum()
}

func storedArtifactFromLineage(lineage ArtifactLineage) StoredArtifact {
	return StoredArtifact{
		Path:           lineage.Path,
		SHA256:         lineage.SHA256,
		CRC32C:         lineage.CRC32C,
		Size:           lineage.Size,
		Generation:     lineage.Generation,
		Metageneration: lineage.Metageneration,
	}
}
