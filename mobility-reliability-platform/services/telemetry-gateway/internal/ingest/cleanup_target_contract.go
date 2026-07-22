package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	CleanupArtifactReadPolicyVersion = "cleanup-artifact-read.current-fence@1"
	CleanupTargetPolicyVersion       = "cleanup-target.create-once@1"
	CleanupTargetSchemaVersion       = "telemetry-cleanup-target.v1"
	CleanupArtifactReadGrantTTL      = 30 * time.Second

	cleanupTargetBindingVersion = "cleanup-target-command@1"
	cleanupTargetGrantVersion   = "cleanup-target-grant@1"
)

var (
	ErrCleanupArtifactUnauthorized             = errors.New("cleanup artifact read is unauthorized")
	ErrCleanupArtifactAuthorizationUnavailable = errors.New("cleanup artifact authorization is unavailable")
	ErrInvalidCleanupTarget                    = errors.New("cleanup target is invalid")
	ErrCleanupTargetAuthorizationExpired       = errors.New("cleanup target authorization has expired")
	ErrCleanupTargetUnavailable                = errors.New("cleanup target evidence is unavailable")
	ErrCleanupTargetConflict                   = errors.New("cleanup target conflicts with immutable evidence")
)

type CurrentCleanupAttempt struct {
	AttemptID     string
	TenantID      string
	ReceiptID     string
	OwnerKind     LeaseOwnerKind
	FencingToken  int64
	WorkerVersion string
	Status        RecoveryAttemptStatus
	StartedAt     time.Time
}

type CurrentCleanupSnapshot struct {
	Receipt  Receipt
	Attempt  CurrentCleanupAttempt
	ReadTime time.Time
}

type CleanupArtifactAuthorizationQuery struct {
	TenantID       string
	ReservationKey string
	AttemptID      string
}

type CleanupArtifactAuthorizationStore interface {
	LoadCurrentCleanup(context.Context, CleanupArtifactAuthorizationQuery) (CurrentCleanupSnapshot, error)
}

type CleanupTargetDecision string

const (
	CleanupTargetDeleteCandidate CleanupTargetDecision = "delete_candidate"
	CleanupTargetVerifiedEmpty   CleanupTargetDecision = "verified_empty"
	CleanupTargetHold            CleanupTargetDecision = "hold"
)

type CleanupTargetStatus string

const (
	CleanupTargetStatusPlanned CleanupTargetStatus = "planned"
	CleanupTargetStatusHold    CleanupTargetStatus = "hold"
)

type CleanupTargetCommand struct {
	SchemaVersion          string
	CleanupID              string
	TenantID               string
	ReceiptID              string
	ReservationKey         string
	AttemptID              string
	Mode                   CleanupMode
	OriginStatus           ReceiptState
	CleanupPolicyVersion   string
	CleanupTransitionedAt  time.Time
	CleanupQuiescenceUntil time.Time
	ReceiptRevision        int64
	FencingToken           int64
	LeaseAcquiredAt        time.Time
	LeaseHeartbeatAt       time.Time
	LeaseExpiresAt         time.Time
	WorkerVersion          string
	Status                 CleanupTargetStatus
	Decision               CleanupTargetDecision
	Classification         ArtifactClassification
	ReasonCode             ArtifactReasonCode
	RetentionPhase         ArtifactRetentionPhase
	ValidatorVersion       string
	ClassifiedAt           time.Time
	ManifestInventory      ArtifactInventorySummary
	RawInventory           ArtifactInventorySummary
	Raw                    *ArtifactLineage
	Manifest               *ArtifactLineage
	CreatedAt              time.Time
}

type CleanupTarget struct {
	Command    CleanupTargetCommand
	TargetHash string
}

type CleanupTargetCreateStatus string

const (
	CleanupTargetCreated  CleanupTargetCreateStatus = "created"
	CleanupTargetReplayed CleanupTargetCreateStatus = "replayed"
)

type CleanupTargetAuthorizationGrant struct {
	policyVersion      string
	checkedAt          time.Time
	expiresAt          time.Time
	receiptRevision    int64
	cleanupFence       LeaseFence
	requestBindingHash [sha256.Size]byte
	commandBindingHash [sha256.Size]byte
	capabilitySeal     [sha256.Size]byte
}

type CleanupTargetStore interface {
	CreateCleanupDryRunTarget(
		context.Context,
		CleanupTargetAuthorizationGrant,
		CleanupTargetCommand,
		time.Time,
	) (CleanupTarget, CleanupTargetCreateStatus, error)
}

type SystemCleanupAuthorizer struct {
	store CleanupArtifactAuthorizationStore
	now   func() time.Time
}

func NewSystemCleanupAuthorizer(
	store CleanupArtifactAuthorizationStore,
	now func() time.Time,
) (*SystemCleanupAuthorizer, error) {
	if store == nil {
		return nil, errors.New("cleanup authorization store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &SystemCleanupAuthorizer{store: store, now: now}, nil
}

func (a *SystemCleanupAuthorizer) AuthorizeArtifactRead(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	lease CleanupLeaseGrant,
) (ArtifactClassificationRequest, ArtifactReadAuthorizationGrant, error) {
	if a == nil || a.store == nil || a.now == nil || ctx == nil ||
		!telemetry.IsUUID(tenantID) || !isLowerHexDigest(reservationKey) ||
		ValidateCleanupLeaseGrant(lease) != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{},
			ErrCleanupArtifactAuthorizationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}
	snapshot, err := a.store.LoadCurrentCleanup(ctx, CleanupArtifactAuthorizationQuery{
		TenantID: tenantID, ReservationKey: reservationKey, AttemptID: lease.Lease.Fence.OwnerID,
	})
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{},
			normalizeCleanupAuthorizationError(ctx, err)
	}
	checkedAt, err := forwardRecoveryAuthorizationTime(a.now().UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{},
			ErrCleanupArtifactAuthorizationUnavailable
	}
	if err := evaluateCurrentCleanup(snapshot, tenantID, reservationKey, lease, checkedAt); err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}
	request, err := cleanupClassificationRequest(snapshot.Receipt)
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}
	expiresAt := earlierRecoveryTime(checkedAt.Add(CleanupArtifactReadGrantTTL), lease.Lease.Fence.ExpiresAt)
	if !checkedAt.Before(expiresAt) {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, ErrCleanupArtifactUnauthorized
	}
	grant, err := mintArtifactReadAuthorizationGrant(
		artifactReadGrantIssuerCleanupDryRun,
		CleanupArtifactReadPolicyVersion,
		request,
		checkedAt,
		expiresAt,
	)
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{},
			ErrCleanupArtifactAuthorizationUnavailable
	}
	return request, grant, nil
}

func (a *SystemCleanupAuthorizer) AuthorizeTargetCreation(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	lease CleanupLeaseGrant,
	attempt CleanupAttemptProposal,
	evidenceRequest ArtifactClassificationRequest,
	result ArtifactClassificationResult,
) (CleanupTargetCommand, CleanupTargetAuthorizationGrant, error) {
	if a == nil || ValidateCleanupAttemptProposal(attempt) != nil ||
		ValidateCleanupLeaseGrant(lease) != nil || attempt.ID != lease.Lease.Fence.OwnerID {
		return CleanupTargetCommand{}, CleanupTargetAuthorizationGrant{}, ErrInvalidCleanupTarget
	}
	evidenceRequest = cloneArtifactClassificationRequest(evidenceRequest)
	result = cloneCleanupArtifactClassificationResult(result)
	decision, status, err := cleanupTargetDisposition(evidenceRequest, result)
	if err != nil {
		return CleanupTargetCommand{}, CleanupTargetAuthorizationGrant{}, err
	}
	currentRequest, readGrant, err := a.AuthorizeArtifactRead(ctx, tenantID, reservationKey, lease)
	if err != nil {
		return CleanupTargetCommand{}, CleanupTargetAuthorizationGrant{}, err
	}
	if canonicalArtifactClassificationRequestBinding(currentRequest) !=
		canonicalArtifactClassificationRequestBinding(evidenceRequest) {
		return CleanupTargetCommand{}, CleanupTargetAuthorizationGrant{}, ErrInvalidCleanupTarget
	}
	command := CleanupTargetCommand{
		SchemaVersion: CleanupTargetSchemaVersion,
		CleanupID:     attempt.ID, TenantID: tenantID, ReceiptID: currentRequest.ReceiptID,
		ReservationKey: reservationKey, AttemptID: attempt.ID,
		Mode: lease.Mode, OriginStatus: lease.OriginStatus,
		CleanupPolicyVersion:   lease.PolicyVersion,
		CleanupTransitionedAt:  lease.TransitionedAt.UTC(),
		CleanupQuiescenceUntil: lease.QuiescenceUntil.UTC(),
		ReceiptRevision:        currentRequest.ReceiptRevision,
		FencingToken:           currentRequest.CleanupFence.Token,
		LeaseAcquiredAt:        lease.Lease.AcquiredAt.UTC(),
		LeaseHeartbeatAt:       lease.Lease.HeartbeatAt.UTC(),
		LeaseExpiresAt:         lease.Lease.Fence.ExpiresAt.UTC(),
		WorkerVersion:          attempt.WorkerVersion,
		Status:                 status, Decision: decision,
		Classification: result.Classification, ReasonCode: result.ReasonCode,
		RetentionPhase: result.RetentionPhase, ValidatorVersion: result.ValidatorVersion,
		ClassifiedAt:      result.ObservedAt.UTC(),
		ManifestInventory: result.ManifestInventory, RawInventory: result.RawInventory,
		CreatedAt: readGrant.checkedAt.UTC(),
	}
	if result.PinnedRaw != nil {
		command.Raw, err = artifactLineageFromPinned(currentRequest.ExpectedRawPath, result.PinnedRaw)
		if err != nil {
			return CleanupTargetCommand{}, CleanupTargetAuthorizationGrant{}, ErrInvalidCleanupTarget
		}
	}
	if result.PinnedManifest != nil {
		command.Manifest, err = artifactLineageFromPinned(currentRequest.ExpectedManifestPath, result.PinnedManifest)
		if err != nil {
			return CleanupTargetCommand{}, CleanupTargetAuthorizationGrant{}, ErrInvalidCleanupTarget
		}
	}
	if err := ValidateCleanupTargetCommand(command); err != nil {
		return CleanupTargetCommand{}, CleanupTargetAuthorizationGrant{}, err
	}
	grant := mintCleanupTargetAuthorizationGrant(
		command,
		canonicalArtifactClassificationRequestBinding(currentRequest),
		readGrant.checkedAt,
		readGrant.expiresAt,
	)
	return cloneCleanupTargetCommand(command), grant, nil
}

func ValidateCleanupTargetCommand(command CleanupTargetCommand) error {
	if command.SchemaVersion != CleanupTargetSchemaVersion ||
		!telemetry.IsUUID(command.CleanupID) || command.CleanupID != command.AttemptID ||
		!telemetry.IsUUID(command.TenantID) || !telemetry.IsUUID(command.ReceiptID) ||
		!isLowerHexDigest(command.ReservationKey) ||
		command.Mode != CleanupModeReservationExpiry || command.OriginStatus != ReceiptReserved ||
		command.CleanupPolicyVersion != CleanupTransitionPolicyV1 ||
		command.CleanupTransitionedAt.IsZero() || command.CleanupQuiescenceUntil.IsZero() ||
		!command.CleanupQuiescenceUntil.Equal(command.CleanupTransitionedAt.Add(DefaultCleanupLateWriteGrace)) ||
		command.ReceiptRevision <= 0 || command.FencingToken <= 0 ||
		command.WorkerVersion != CleanupWorkerVersion ||
		command.LeaseAcquiredAt.IsZero() || command.LeaseHeartbeatAt.IsZero() ||
		command.LeaseExpiresAt.IsZero() || command.LeaseHeartbeatAt.Before(command.LeaseAcquiredAt) ||
		!command.LeaseHeartbeatAt.Before(command.LeaseExpiresAt) ||
		command.ClassifiedAt.IsZero() || command.CreatedAt.IsZero() ||
		command.ClassifiedAt.Before(command.CleanupQuiescenceUntil) ||
		command.CreatedAt.Before(command.ClassifiedAt) || !command.CreatedAt.Before(command.LeaseExpiresAt) ||
		!validArtifactClassificationOutcome(ArtifactReadCleanupDryRun, command.Classification, command.ReasonCode) ||
		!validArtifactInventorySummary(command.ManifestInventory) ||
		!validArtifactInventorySummary(command.RawInventory) ||
		!validArtifactServerLabel(command.ValidatorVersion) {
		return ErrInvalidCleanupTarget
	}
	for _, lineage := range []*ArtifactLineage{command.Raw, command.Manifest} {
		if lineage != nil && validateArtifactLineage(lineage, lineage.Path) != nil {
			return ErrInvalidCleanupTarget
		}
	}
	switch command.Decision {
	case CleanupTargetDeleteCandidate:
		if command.Status != CleanupTargetStatusPlanned || command.Raw == nil && command.Manifest == nil {
			return ErrInvalidCleanupTarget
		}
		switch command.Classification {
		case ArtifactClassificationValidRawOnly:
			if command.Raw == nil || command.Manifest != nil ||
				!completeInventoryCount(command.RawInventory, 1) ||
				!completeInventoryCount(command.ManifestInventory, 0) {
				return ErrInvalidCleanupTarget
			}
		case ArtifactClassificationValidComplete:
			if command.Raw == nil || command.Manifest == nil ||
				!completeInventoryCount(command.RawInventory, 1) ||
				!completeInventoryCount(command.ManifestInventory, 1) {
				return ErrInvalidCleanupTarget
			}
		case ArtifactClassificationManifestOnly:
			if command.Raw != nil || command.Manifest == nil ||
				!completeInventoryCount(command.RawInventory, 0) ||
				!completeInventoryCount(command.ManifestInventory, 1) {
				return ErrInvalidCleanupTarget
			}
		default:
			return ErrInvalidCleanupTarget
		}
	case CleanupTargetVerifiedEmpty:
		if command.Status != CleanupTargetStatusPlanned ||
			command.Classification != ArtifactClassificationNone ||
			command.Raw != nil || command.Manifest != nil ||
			!completeInventoryCount(command.RawInventory, 0) ||
			!completeInventoryCount(command.ManifestInventory, 0) {
			return ErrInvalidCleanupTarget
		}
	case CleanupTargetHold:
		if command.Status != CleanupTargetStatusHold ||
			command.Classification == ArtifactClassificationNone ||
			command.Classification == ArtifactClassificationValidRawOnly ||
			command.Classification == ArtifactClassificationValidComplete ||
			command.Classification == ArtifactClassificationManifestOnly ||
			command.Classification == ArtifactClassificationUnavailable ||
			command.Classification == ArtifactClassificationStoredMissing {
			return ErrInvalidCleanupTarget
		}
	default:
		return ErrInvalidCleanupTarget
	}
	return nil
}

func ValidateCleanupTargetAuthorization(
	grant CleanupTargetAuthorizationGrant,
	command CleanupTargetCommand,
	observedAt time.Time,
) error {
	command = cloneCleanupTargetCommand(command)
	if ValidateCleanupTargetCommand(command) != nil || observedAt.IsZero() ||
		grant.policyVersion != CleanupTargetPolicyVersion ||
		grant.checkedAt.IsZero() || grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.receiptRevision != command.ReceiptRevision ||
		grant.cleanupFence.OwnerID != command.AttemptID ||
		grant.cleanupFence.Token != command.FencingToken ||
		!grant.cleanupFence.ExpiresAt.Equal(command.LeaseExpiresAt) ||
		grant.commandBindingHash != canonicalCleanupTargetBinding(command) ||
		grant.capabilitySeal != cleanupTargetCapabilitySeal(grant) ||
		observedAt.Before(grant.checkedAt) {
		return ErrInvalidCleanupTarget
	}
	if !observedAt.Before(grant.expiresAt) || !observedAt.Before(grant.cleanupFence.ExpiresAt) {
		return ErrCleanupTargetAuthorizationExpired
	}
	return nil
}

func CleanupTargetAuthorizationDeadline(
	grant CleanupTargetAuthorizationGrant,
	command CleanupTargetCommand,
) (time.Time, error) {
	if err := ValidateCleanupTargetAuthorization(grant, command, grant.checkedAt); err != nil {
		return time.Time{}, err
	}
	return earlierRecoveryTime(grant.expiresAt, grant.cleanupFence.ExpiresAt), nil
}

func ValidateCurrentCleanupTarget(
	grant CleanupTargetAuthorizationGrant,
	command CleanupTargetCommand,
	snapshot CurrentCleanupSnapshot,
	observedAt time.Time,
) error {
	if err := ValidateCleanupTargetAuthorization(grant, command, observedAt); err != nil {
		return err
	}
	lease := CleanupLeaseGrant{
		Lease: LeaseGrant{
			Fence:     LeaseFence{OwnerID: command.AttemptID, Token: command.FencingToken, ExpiresAt: command.LeaseExpiresAt},
			OwnerKind: LeaseOwnerCleanup, AcquiredAt: command.LeaseAcquiredAt, HeartbeatAt: command.LeaseHeartbeatAt,
		},
		ReceiptRevision: command.ReceiptRevision, Mode: command.Mode, OriginStatus: command.OriginStatus,
		PolicyVersion: command.CleanupPolicyVersion, TransitionedAt: command.CleanupTransitionedAt,
		QuiescenceUntil: command.CleanupQuiescenceUntil,
	}
	if err := evaluateCurrentCleanup(snapshot, command.TenantID, command.ReservationKey, lease, observedAt); err != nil {
		return err
	}
	request, err := cleanupClassificationRequest(snapshot.Receipt)
	if err != nil || canonicalArtifactClassificationRequestBinding(request) != grant.requestBindingHash {
		return ErrInvalidCleanupTarget
	}
	return nil
}

func CleanupTargetHash(command CleanupTargetCommand) (string, error) {
	if err := ValidateCleanupTargetCommand(command); err != nil {
		return "", err
	}
	sum := canonicalCleanupTargetBinding(command)
	return hex.EncodeToString(sum[:]), nil
}

func evaluateCurrentCleanup(
	snapshot CurrentCleanupSnapshot,
	tenantID string,
	reservationKey string,
	lease CleanupLeaseGrant,
	checkedAt time.Time,
) error {
	if ValidateCleanupLeaseGrant(lease) != nil || snapshot.ReadTime.IsZero() || checkedAt.IsZero() ||
		snapshot.Receipt.TenantID != tenantID || snapshot.Receipt.ReservationKey != reservationKey ||
		snapshot.Receipt.State != ReceiptCleanupPending ||
		snapshot.Receipt.Revision != lease.ReceiptRevision ||
		snapshot.Receipt.CleanupMode != lease.Mode ||
		snapshot.Receipt.CleanupOriginStatus != lease.OriginStatus ||
		snapshot.Receipt.CleanupPolicyVersion != lease.PolicyVersion ||
		!snapshot.Receipt.CleanupTransitionedAt.Equal(lease.TransitionedAt) ||
		!snapshot.Receipt.CleanupQuiescenceUntil.Equal(lease.QuiescenceUntil) ||
		snapshot.Receipt.LeaseOwnerKind != LeaseOwnerCleanup ||
		snapshot.Receipt.LeaseOwnerID != lease.Lease.Fence.OwnerID ||
		snapshot.Receipt.FencingToken != lease.Lease.Fence.Token ||
		!snapshot.Receipt.LeaseAcquiredAt.Equal(lease.Lease.AcquiredAt) ||
		!snapshot.Receipt.LeaseHeartbeatAt.Equal(lease.Lease.HeartbeatAt) ||
		!snapshot.Receipt.LeaseExpiresAt.Equal(lease.Lease.Fence.ExpiresAt) ||
		checkedAt.Before(lease.QuiescenceUntil) || !checkedAt.Before(lease.Lease.Fence.ExpiresAt) ||
		validateCurrentCleanupAttempt(snapshot.Attempt, snapshot.Receipt, lease) != nil {
		return ErrCleanupArtifactUnauthorized
	}
	return nil
}

func validateCurrentCleanupAttempt(
	attempt CurrentCleanupAttempt,
	receipt Receipt,
	lease CleanupLeaseGrant,
) error {
	if attempt.AttemptID != lease.Lease.Fence.OwnerID || attempt.TenantID != receipt.TenantID ||
		attempt.ReceiptID != receipt.ReceiptID || attempt.OwnerKind != LeaseOwnerCleanup ||
		attempt.FencingToken != lease.Lease.Fence.Token || attempt.WorkerVersion != CleanupWorkerVersion ||
		attempt.Status != RecoveryAttemptStarted || attempt.StartedAt.IsZero() ||
		!attempt.StartedAt.Equal(receipt.LeaseAcquiredAt) {
		return ErrCleanupArtifactUnauthorized
	}
	return nil
}

func cleanupClassificationRequest(receipt Receipt) (ArtifactClassificationRequest, error) {
	request, err := classificationRequestFromReceipt(receipt, ArtifactReadCleanupDryRun)
	if err != nil {
		return ArtifactClassificationRequest{}, err
	}
	request.CleanupFence = &LeaseFence{
		OwnerID: receipt.LeaseOwnerID, Token: receipt.FencingToken, ExpiresAt: receipt.LeaseExpiresAt,
	}
	if receipt.State != ReceiptCleanupPending || receipt.CleanupMode != CleanupModeReservationExpiry ||
		receipt.CleanupOriginStatus != ReceiptReserved ||
		receipt.CleanupPolicyVersion != CleanupTransitionPolicyV1 ||
		receipt.CleanupTransitionedAt.IsZero() || receipt.CleanupQuiescenceUntil.IsZero() ||
		!receipt.CleanupQuiescenceUntil.Equal(receipt.CleanupTransitionedAt.Add(DefaultCleanupLateWriteGrace)) ||
		receipt.LeaseOwnerKind != LeaseOwnerCleanup || receipt.RecoveryAttemptCount <= 0 ||
		!receipt.NextRecoveryAt.IsZero() || receipt.LastRecoveryCode != "" ||
		!receipt.UpdatedAt.Equal(receipt.LeaseHeartbeatAt) ||
		ValidateArtifactClassificationRequest(request) != nil {
		return ArtifactClassificationRequest{}, ErrCleanupArtifactAuthorizationUnavailable
	}
	return request, nil
}

func classificationRequestFromReceipt(
	receipt Receipt,
	purpose ArtifactReadPurpose,
) (ArtifactClassificationRequest, error) {
	manifestInput := BatchManifestInput{
		PayloadSchemaVersion: receipt.PayloadSchemaVersion, TenantID: receipt.TenantID,
		DeviceID: receipt.DeviceID, TripID: receipt.TripID, InstallationID: receipt.InstallationID,
		BatchID: receipt.BatchID, ClientBatchID: receipt.ClientBatchID,
		ConsentRevisionID: receipt.ConsentRevisionID, BodyHash: receipt.BodyHash,
		SampleCount: receipt.ExpectedSampleCount, FirstCapturedAt: receipt.FirstCapturedAt,
		LastCapturedAt: receipt.LastCapturedAt, ReceivedAt: receipt.CreatedAt,
		ArtifactExpiresAt: receipt.ArtifactExpiresAt, ValidatorVersion: receipt.ValidatorVersion,
	}
	request := ArtifactClassificationRequest{
		Purpose: purpose, ReceiptID: receipt.ReceiptID, ReservationKey: receipt.ReservationKey,
		ReceiptState: receipt.State, ReceiptRevision: receipt.Revision, TenantID: receipt.TenantID,
		DeviceID: receipt.DeviceID, TripID: receipt.TripID, InstallationID: receipt.InstallationID,
		BatchID: receipt.BatchID, ClientBatchID: receipt.ClientBatchID,
		ConsentRevisionID: receipt.ConsentRevisionID, PayloadSchemaVersion: receipt.PayloadSchemaVersion,
		ValidatorVersion: receipt.ValidatorVersion, BodyHash: receipt.BodyHash,
		ExpectedSampleCount: receipt.ExpectedSampleCount, FirstCapturedAt: receipt.FirstCapturedAt,
		LastCapturedAt: receipt.LastCapturedAt, ReceivedAt: receipt.CreatedAt,
		ArtifactExpiresAt:    receipt.ArtifactExpiresAt,
		ExpectedRawPath:      ExpectedTelemetryObjectPath(manifestInput),
		ExpectedManifestPath: ExpectedTelemetryManifestPath(manifestInput),
	}
	if !isLowerHexDigest(receipt.ClientBatchKey) ||
		receipt.ClientBatchKey != DeriveClientBatchKey(receipt.TenantID, receipt.ClientBatchID) ||
		receipt.ReceiptID != receipt.BatchID || receipt.Revision <= 0 ||
		receipt.CreatedAt.IsZero() || receipt.UpdatedAt.IsZero() || receipt.UpdatedAt.Before(receipt.CreatedAt) ||
		!receipt.ReservationDeadline.Equal(receipt.CreatedAt.Add(ReservationProcessingWindow)) ||
		!receipt.ArtifactExpiresAt.Equal(receipt.CreatedAt.Add(TelemetryArtifactRetention)) ||
		!receipt.ReceiptRetentionFloor.Equal(receipt.CreatedAt.Add(ReceiptControlRetention)) ||
		!receipt.CreatedAt.Before(receipt.ReservationDeadline) ||
		!receipt.ReservationDeadline.Before(receipt.ArtifactExpiresAt) || receipt.PurgeEligibleAt != nil ||
		receipt.ObjectPath != "" || receipt.ManifestPath != "" ||
		receipt.ObjectSHA256 != "" || receipt.ManifestSHA256 != "" ||
		receipt.ObjectCRC32C != 0 || receipt.ManifestCRC32C != 0 ||
		receipt.ObjectSize != 0 || receipt.ManifestSize != 0 ||
		receipt.ObjectGeneration != 0 || receipt.ObjectMetageneration != 0 ||
		receipt.ManifestGeneration != 0 || receipt.ManifestMetageneration != 0 ||
		receipt.SampleCount != 0 || receipt.RejectionCode != "" ||
		receipt.RecoveryHoldCode != "" || !receipt.RecoveryHoldReviewDueAt.IsZero() {
		return ArtifactClassificationRequest{}, ErrCleanupArtifactAuthorizationUnavailable
	}
	return request, nil
}

func cleanupTargetDisposition(
	request ArtifactClassificationRequest,
	result ArtifactClassificationResult,
) (CleanupTargetDecision, CleanupTargetStatus, error) {
	if validateCleanupClassification(request, result) != nil {
		return "", "", ErrInvalidCleanupTarget
	}
	switch result.Classification {
	case ArtifactClassificationNone:
		return CleanupTargetVerifiedEmpty, CleanupTargetStatusPlanned, nil
	case ArtifactClassificationValidRawOnly,
		ArtifactClassificationValidComplete,
		ArtifactClassificationManifestOnly:
		return CleanupTargetDeleteCandidate, CleanupTargetStatusPlanned, nil
	case ArtifactClassificationUnavailable:
		return "", "", ErrCleanupTargetUnavailable
	case ArtifactClassificationRawContentConflict,
		ArtifactClassificationManifestConflict,
		ArtifactClassificationMetadataConflict,
		ArtifactClassificationGenerationDrift:
		return CleanupTargetHold, CleanupTargetStatusHold, nil
	default:
		return "", "", ErrInvalidCleanupTarget
	}
}

func validateCleanupClassification(
	request ArtifactClassificationRequest,
	result ArtifactClassificationResult,
) error {
	if request.Purpose != ArtifactReadCleanupDryRun || ValidateArtifactClassificationRequest(request) != nil ||
		!validArtifactClassificationOutcome(request.Purpose, result.Classification, result.ReasonCode) ||
		result.ValidatorVersion != request.ValidatorVersion || result.ObservedAt.IsZero() ||
		!validArtifactClassificationEvidence(request, result) ||
		result.ObservedAt.Before(request.ReceivedAt) || request.CleanupFence == nil ||
		!result.ObservedAt.Before(request.CleanupFence.ExpiresAt) ||
		result.RetentionPhase != artifactRetentionPhaseAt(request, result.ObservedAt) ||
		!validArtifactInventorySummary(result.ManifestInventory) ||
		!validArtifactInventorySummary(result.RawInventory) {
		return ErrInvalidCleanupTarget
	}
	if result.PinnedRaw != nil {
		if _, err := artifactLineageFromPinned(request.ExpectedRawPath, result.PinnedRaw); err != nil {
			return ErrInvalidCleanupTarget
		}
	}
	if result.PinnedManifest != nil {
		if _, err := artifactLineageFromPinned(request.ExpectedManifestPath, result.PinnedManifest); err != nil {
			return ErrInvalidCleanupTarget
		}
	}
	switch result.Classification {
	case ArtifactClassificationNone:
		if result.PinnedRaw != nil || result.PinnedManifest != nil ||
			!completeInventoryCount(result.RawInventory, 0) ||
			!completeInventoryCount(result.ManifestInventory, 0) {
			return ErrInvalidCleanupTarget
		}
	case ArtifactClassificationValidRawOnly:
		if result.PinnedRaw == nil || result.PinnedManifest != nil ||
			!completeInventoryCount(result.RawInventory, 1) ||
			!completeInventoryCount(result.ManifestInventory, 0) {
			return ErrInvalidCleanupTarget
		}
	case ArtifactClassificationValidComplete:
		if result.PinnedRaw == nil || result.PinnedManifest == nil ||
			!completeInventoryCount(result.RawInventory, 1) ||
			!completeInventoryCount(result.ManifestInventory, 1) {
			return ErrInvalidCleanupTarget
		}
	case ArtifactClassificationManifestOnly:
		if result.PinnedRaw != nil || result.PinnedManifest == nil ||
			!completeInventoryCount(result.RawInventory, 0) ||
			!completeInventoryCount(result.ManifestInventory, 1) {
			return ErrInvalidCleanupTarget
		}
	}
	return nil
}

func mintCleanupTargetAuthorizationGrant(
	command CleanupTargetCommand,
	requestBinding [sha256.Size]byte,
	checkedAt time.Time,
	expiresAt time.Time,
) CleanupTargetAuthorizationGrant {
	grant := CleanupTargetAuthorizationGrant{
		policyVersion: CleanupTargetPolicyVersion, checkedAt: checkedAt.UTC(), expiresAt: expiresAt.UTC(),
		receiptRevision:    command.ReceiptRevision,
		cleanupFence:       LeaseFence{OwnerID: command.AttemptID, Token: command.FencingToken, ExpiresAt: command.LeaseExpiresAt},
		requestBindingHash: requestBinding, commandBindingHash: canonicalCleanupTargetBinding(command),
	}
	grant.capabilitySeal = cleanupTargetCapabilitySeal(grant)
	return grant
}

func canonicalCleanupTargetBinding(command CleanupTargetCommand) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(cleanupTargetBindingVersion)
	encoder.addString(command.SchemaVersion)
	encoder.addString(command.CleanupID)
	encoder.addString(command.TenantID)
	encoder.addString(command.ReceiptID)
	encoder.addString(command.ReservationKey)
	encoder.addString(command.AttemptID)
	encoder.addString(string(command.Mode))
	encoder.addString(string(command.OriginStatus))
	encoder.addString(command.CleanupPolicyVersion)
	encoder.addTime(command.CleanupTransitionedAt)
	encoder.addTime(command.CleanupQuiescenceUntil)
	encoder.addInt64(command.ReceiptRevision)
	encoder.addInt64(command.FencingToken)
	encoder.addTime(command.LeaseAcquiredAt)
	encoder.addTime(command.LeaseHeartbeatAt)
	encoder.addTime(command.LeaseExpiresAt)
	encoder.addString(command.WorkerVersion)
	encoder.addString(string(command.Status))
	encoder.addString(string(command.Decision))
	encoder.addString(string(command.Classification))
	encoder.addString(string(command.ReasonCode))
	encoder.addString(string(command.RetentionPhase))
	encoder.addString(command.ValidatorVersion)
	encoder.addTime(command.ClassifiedAt)
	addArtifactInventoryBinding(encoder, command.ManifestInventory)
	addArtifactInventoryBinding(encoder, command.RawInventory)
	encoder.addArtifactLineage(command.Raw)
	encoder.addArtifactLineage(command.Manifest)
	encoder.addTime(command.CreatedAt)
	return encoder.sum()
}

func cleanupTargetCapabilitySeal(grant CleanupTargetAuthorizationGrant) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(cleanupTargetGrantVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addInt64(grant.receiptRevision)
	encoder.addLeaseFence(&grant.cleanupFence)
	encoder.addBytes(grant.requestBindingHash[:])
	encoder.addBytes(grant.commandBindingHash[:])
	return encoder.sum()
}

func cloneCleanupArtifactClassificationResult(result ArtifactClassificationResult) ArtifactClassificationResult {
	cloned := result
	if result.PinnedRaw != nil {
		lineage := *result.PinnedRaw
		cloned.PinnedRaw = &lineage
	}
	if result.PinnedManifest != nil {
		lineage := *result.PinnedManifest
		cloned.PinnedManifest = &lineage
	}
	return cloned
}

func cloneCleanupTargetCommand(command CleanupTargetCommand) CleanupTargetCommand {
	if command.Raw != nil {
		lineage := *command.Raw
		command.Raw = &lineage
	}
	if command.Manifest != nil {
		lineage := *command.Manifest
		command.Manifest = &lineage
	}
	return command
}

func normalizeCleanupAuthorizationError(ctx context.Context, err error) error {
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, ErrCleanupArtifactUnauthorized) {
		return ErrCleanupArtifactUnauthorized
	}
	return ErrCleanupArtifactAuthorizationUnavailable
}
