package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var _ ingest.CleanupArtifactAuthorizationStore = (*FirestoreAdmissionStore)(nil)
var _ ingest.CleanupTargetStore = (*FirestoreAdmissionStore)(nil)

type cleanupTargetRead struct {
	Target   firestoreIngestCleanupTarget
	ReadTime time.Time
}

type cleanupAuthorizationTransaction interface {
	ReadRecoveryAttempt(context.Context, string) (recoveryAttemptRead, bool, error)
}

type cleanupTargetTransaction interface {
	cleanupAuthorizationTransaction
	ReadCleanupTarget(context.Context, string) (cleanupTargetRead, bool, error)
}

type currentCleanupTargetValidator func(
	ingest.CleanupTargetAuthorizationGrant,
	ingest.CleanupTargetCommand,
	ingest.CurrentCleanupSnapshot,
	time.Time,
) error

func (s *FirestoreAdmissionStore) LoadCurrentCleanup(
	ctx context.Context,
	query ingest.CleanupArtifactAuthorizationQuery,
) (ingest.CurrentCleanupSnapshot, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(query.TenantID) || !lowerHexDigest(query.ReservationKey) ||
		!telemetry.IsUUID(query.AttemptID) {
		return ingest.CurrentCleanupSnapshot{}, ingest.ErrCleanupArtifactAuthorizationUnavailable
	}
	var result ingest.CurrentCleanupSnapshot
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.CurrentCleanupSnapshot{}
		linked, loadErr := loadLinkedReceipt(runContext, transaction, query.TenantID, query.ReservationKey)
		if loadErr != nil {
			return loadErr
		}
		reader, ok := transaction.(cleanupAuthorizationTransaction)
		if !ok {
			return ingest.ErrCleanupArtifactAuthorizationUnavailable
		}
		attemptResult, exists, readErr := reader.ReadRecoveryAttempt(
			runContext,
			recoveryAttemptDocumentPath(query.TenantID, linked.Receipt.Receipt.ReceiptID, query.AttemptID),
		)
		if readErr != nil {
			return readErr
		}
		if !exists || validateCleanupStartedAttempt(attemptResult.Attempt, linked.Receipt.Receipt, query.AttemptID) != nil {
			return ingest.ErrCleanupArtifactUnauthorized
		}
		readTime, clockErr := coherentCleanupTargetReadTime(linked.Receipt.ReadTime, attemptResult.ReadTime)
		if clockErr != nil {
			return clockErr
		}
		result = ingest.CurrentCleanupSnapshot{
			Receipt:  linked.Receipt.Receipt.toDomain(),
			Attempt:  currentCleanupAttempt(attemptResult.Attempt),
			ReadTime: readTime,
		}
		return nil
	})
	if err != nil {
		return ingest.CurrentCleanupSnapshot{}, normalizeCleanupTargetStoreError(ctx, ctx, err)
	}
	if result.Receipt.ReceiptID == "" || result.ReadTime.IsZero() {
		return ingest.CurrentCleanupSnapshot{}, ingest.ErrCleanupArtifactAuthorizationUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) CreateCleanupDryRunTarget(
	ctx context.Context,
	grant ingest.CleanupTargetAuthorizationGrant,
	command ingest.CleanupTargetCommand,
	observedAt time.Time,
) (ingest.CleanupTarget, ingest.CleanupTargetCreateStatus, error) {
	command = cloneCleanupTargetCommand(command)
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() ||
		!telemetry.IsUUID(command.TenantID) || !lowerHexDigest(command.ReservationKey) {
		return ingest.CleanupTarget{}, "", ingest.ErrInvalidCleanupTarget
	}
	observedAt = observedAt.UTC()
	if err := ingest.ValidateCleanupTargetAuthorization(grant, command, observedAt); err != nil {
		return ingest.CleanupTarget{}, "", err
	}
	deadline, err := ingest.CleanupTargetAuthorizationDeadline(grant, command)
	if err != nil {
		return ingest.CleanupTarget{}, "", err
	}
	targetContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result, resultStatus, createErr := s.createCleanupDryRunTarget(
		targetContext,
		grant,
		command,
		observedAt,
		ingest.ValidateCurrentCleanupTarget,
	)
	if createErr != nil {
		return ingest.CleanupTarget{}, "", normalizeCleanupTargetStoreError(ctx, targetContext, createErr)
	}
	return result, resultStatus, nil
}

// createCleanupDryRunTarget keeps opaque capability validation injectable only
// inside this package. The public path always supplies the domain validator;
// adapter tests can exercise transaction atomicity without forging a grant.
func (s *FirestoreAdmissionStore) createCleanupDryRunTarget(
	ctx context.Context,
	grant ingest.CleanupTargetAuthorizationGrant,
	command ingest.CleanupTargetCommand,
	observedAt time.Time,
	validateCurrent currentCleanupTargetValidator,
) (ingest.CleanupTarget, ingest.CleanupTargetCreateStatus, error) {
	command = cloneCleanupTargetCommand(command)
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() || validateCurrent == nil {
		return ingest.CleanupTarget{}, "", ingest.ErrCleanupArtifactAuthorizationUnavailable
	}

	var result ingest.CleanupTarget
	var resultStatus ingest.CleanupTargetCreateStatus
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.CleanupTarget{}
		resultStatus = ""
		linked, loadErr := loadLinkedReceipt(runContext, transaction, command.TenantID, command.ReservationKey)
		if loadErr != nil {
			return loadErr
		}
		cleanupTransaction, ok := transaction.(cleanupTargetTransaction)
		if !ok {
			return ingest.ErrCleanupArtifactAuthorizationUnavailable
		}
		attemptResult, exists, attemptErr := cleanupTransaction.ReadRecoveryAttempt(
			runContext,
			recoveryAttemptDocumentPath(command.TenantID, linked.Receipt.Receipt.ReceiptID, command.AttemptID),
		)
		if attemptErr != nil {
			return attemptErr
		}
		if !exists || validateCleanupStartedAttempt(
			attemptResult.Attempt,
			linked.Receipt.Receipt,
			command.AttemptID,
		) != nil {
			return ingest.ErrInvalidCleanupTarget
		}

		targetPath := cleanupTargetDocumentPath(command.TenantID, command.CleanupID)
		existing, targetExists, targetErr := cleanupTransaction.ReadCleanupTarget(runContext, targetPath)
		if targetErr != nil {
			return targetErr
		}
		readTimes := []time.Time{linked.Receipt.ReadTime, attemptResult.ReadTime}
		if targetExists {
			readTimes = append(readTimes, existing.ReadTime)
		}
		readTime, clockErr := coherentCleanupTargetReadTime(readTimes...)
		if clockErr != nil {
			return clockErr
		}
		effectiveAt, clockErr := conservativeAcceptanceTime(observedAt, readTime)
		if clockErr != nil {
			return ingest.ErrCleanupArtifactAuthorizationUnavailable
		}
		snapshot := ingest.CurrentCleanupSnapshot{
			Receipt:  linked.Receipt.Receipt.toDomain(),
			Attempt:  currentCleanupAttempt(attemptResult.Attempt),
			ReadTime: readTime,
		}
		if validationErr := validateCurrent(
			grant, command, snapshot, effectiveAt,
		); validationErr != nil {
			return validationErr
		}
		targetHash, hashErr := ingest.CleanupTargetHash(command)
		if hashErr != nil {
			return hashErr
		}
		result = ingest.CleanupTarget{Command: cloneCleanupTargetCommand(command), TargetHash: targetHash}
		if targetExists {
			persisted, persistedErr := existing.Target.toDomain()
			if persistedErr != nil || persisted.TargetHash != targetHash {
				return ingest.ErrCleanupTargetConflict
			}
			resultStatus = ingest.CleanupTargetReplayed
			return nil
		}
		if createErr := transaction.Create(
			runContext,
			targetPath,
			newFirestoreCleanupTarget(command, targetHash),
		); createErr != nil {
			return normalizeAdmissionError(runContext, createErr)
		}
		resultStatus = ingest.CleanupTargetCreated
		return nil
	})
	if err != nil {
		return ingest.CleanupTarget{}, "", err
	}
	if resultStatus == "" || result.TargetHash == "" {
		return ingest.CleanupTarget{}, "", ingest.ErrCleanupArtifactAuthorizationUnavailable
	}
	return result, resultStatus, nil
}

func (transaction firestoreAdmissionTransaction) ReadCleanupTarget(
	ctx context.Context,
	path string,
) (cleanupTargetRead, bool, error) {
	document, err := transaction.transaction.Get(transaction.client.Doc(path))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return cleanupTargetRead{}, false, nil
		}
		return cleanupTargetRead{}, false, normalizeAdmissionError(ctx, err)
	}
	var target firestoreIngestCleanupTarget
	if document == nil || !document.Exists() || document.ReadTime.IsZero() || document.DataTo(&target) != nil {
		return cleanupTargetRead{}, false, ingest.ErrCleanupArtifactAuthorizationUnavailable
	}
	return cleanupTargetRead{Target: target, ReadTime: document.ReadTime.UTC()}, true, nil
}

func cleanupTargetDocumentPath(tenantID, cleanupID string) string {
	return "tenants/" + tenantID + "/ingestCleanupTargets/" + cleanupID
}

func validateCleanupStartedAttempt(
	attempt firestoreRecoveryAttempt,
	receipt firestoreIngestReceipt,
	attemptID string,
) error {
	return validateStartedRecoveryAttemptForOwner(
		attempt,
		receipt,
		ingest.RecoveryAttemptProposal{ID: attemptID, WorkerVersion: ingest.CleanupWorkerVersion},
		ingest.LeaseFence{OwnerID: attemptID, Token: receipt.FencingToken, ExpiresAt: receipt.LeaseExpiresAt},
		ingest.LeaseOwnerCleanup,
	)
}

func currentCleanupAttempt(attempt firestoreRecoveryAttempt) ingest.CurrentCleanupAttempt {
	return ingest.CurrentCleanupAttempt{
		AttemptID: attempt.AttemptID, TenantID: attempt.TenantID, ReceiptID: attempt.ReceiptID,
		OwnerKind: attempt.OwnerKind, FencingToken: attempt.FencingToken,
		WorkerVersion: attempt.WorkerVersion, Status: attempt.Status, StartedAt: attempt.StartedAt.UTC(),
	}
}

func coherentCleanupTargetReadTime(readTimes ...time.Time) (time.Time, error) {
	var earliest time.Time
	var latest time.Time
	for _, readTime := range readTimes {
		readTime = readTime.UTC()
		if readTime.IsZero() {
			return time.Time{}, ingest.ErrCleanupArtifactAuthorizationUnavailable
		}
		if earliest.IsZero() || readTime.Before(earliest) {
			earliest = readTime
		}
		if readTime.After(latest) {
			latest = readTime
		}
	}
	if !withinAdmissionClockSkew(earliest, latest) {
		return time.Time{}, ingest.ErrCleanupArtifactAuthorizationUnavailable
	}
	return latest.UTC(), nil
}

func normalizeCleanupTargetStoreError(
	parentContext context.Context,
	operationContext context.Context,
	err error,
) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ingest.ErrInvalidCleanupTarget) ||
		errors.Is(err, ingest.ErrCleanupTargetAuthorizationExpired) ||
		errors.Is(err, ingest.ErrCleanupTargetConflict) ||
		errors.Is(err, ingest.ErrCleanupArtifactUnauthorized) {
		return err
	}
	if parentContext != nil {
		if contextErr := parentContext.Err(); contextErr != nil {
			return contextErr
		}
	}
	if operationContext != nil {
		if contextErr := operationContext.Err(); contextErr != nil {
			if errors.Is(contextErr, context.DeadlineExceeded) {
				return ingest.ErrCleanupTargetAuthorizationExpired
			}
			return contextErr
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ingest.ErrCleanupTargetAuthorizationExpired
	}
	return ingest.ErrCleanupArtifactAuthorizationUnavailable
}

func cloneCleanupTargetCommand(command ingest.CleanupTargetCommand) ingest.CleanupTargetCommand {
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

type firestoreArtifactInventorySummary struct {
	Performed           bool                             `firestore:"performed"`
	NonSoftDeletedCount int                              `firestore:"non_soft_deleted_count"`
	SoftDeletedCount    int                              `firestore:"soft_deleted_count"`
	Truncated           bool                             `firestore:"truncated"`
	Coverage            ingest.ArtifactInventoryCoverage `firestore:"coverage"`
}

type firestoreArtifactLineage struct {
	Path           string `firestore:"path"`
	SHA256         string `firestore:"sha256"`
	CRC32C         int64  `firestore:"crc32c"`
	Size           int64  `firestore:"size"`
	Generation     int64  `firestore:"generation"`
	Metageneration int64  `firestore:"metageneration"`
}

type firestoreIngestCleanupTarget struct {
	SchemaVersion          string                            `firestore:"schema_version"`
	CleanupID              string                            `firestore:"cleanup_id"`
	TenantID               string                            `firestore:"tenant_id"`
	ReceiptID              string                            `firestore:"receipt_id"`
	ReservationKey         string                            `firestore:"reservation_key"`
	AttemptID              string                            `firestore:"attempt_id"`
	Mode                   ingest.CleanupMode                `firestore:"mode"`
	OriginStatus           ingest.ReceiptState               `firestore:"cleanup_origin_status"`
	CleanupPolicyVersion   string                            `firestore:"cleanup_policy_version"`
	CleanupTransitionedAt  time.Time                         `firestore:"cleanup_transitioned_at"`
	CleanupQuiescenceUntil time.Time                         `firestore:"cleanup_quiescence_until"`
	ReceiptRevision        int64                             `firestore:"receipt_revision"`
	FencingToken           int64                             `firestore:"fencing_token"`
	LeaseAcquiredAt        time.Time                         `firestore:"lease_acquired_at"`
	LeaseHeartbeatAt       time.Time                         `firestore:"lease_heartbeat_at"`
	LeaseExpiresAt         time.Time                         `firestore:"lease_expires_at"`
	WorkerVersion          string                            `firestore:"worker_version"`
	Status                 ingest.CleanupTargetStatus        `firestore:"status"`
	Decision               ingest.CleanupTargetDecision      `firestore:"decision"`
	Classification         ingest.ArtifactClassification     `firestore:"classification"`
	ReasonCode             ingest.ArtifactReasonCode         `firestore:"reason_code"`
	RetentionPhase         ingest.ArtifactRetentionPhase     `firestore:"retention_phase"`
	ValidatorVersion       string                            `firestore:"validator_version"`
	ClassifiedAt           time.Time                         `firestore:"classified_at"`
	ManifestInventory      firestoreArtifactInventorySummary `firestore:"manifest_inventory"`
	RawInventory           firestoreArtifactInventorySummary `firestore:"raw_inventory"`
	Raw                    *firestoreArtifactLineage         `firestore:"raw,omitempty"`
	Manifest               *firestoreArtifactLineage         `firestore:"manifest,omitempty"`
	CreatedAt              time.Time                         `firestore:"created_at"`
	UpdatedAt              time.Time                         `firestore:"updated_at"`
	TargetHash             string                            `firestore:"target_hash"`
}

func newFirestoreCleanupTarget(
	command ingest.CleanupTargetCommand,
	targetHash string,
) firestoreIngestCleanupTarget {
	return firestoreIngestCleanupTarget{
		SchemaVersion: command.SchemaVersion, CleanupID: command.CleanupID,
		TenantID: command.TenantID, ReceiptID: command.ReceiptID,
		ReservationKey: command.ReservationKey, AttemptID: command.AttemptID,
		Mode: command.Mode, OriginStatus: command.OriginStatus,
		CleanupPolicyVersion:   command.CleanupPolicyVersion,
		CleanupTransitionedAt:  command.CleanupTransitionedAt.UTC(),
		CleanupQuiescenceUntil: command.CleanupQuiescenceUntil.UTC(),
		ReceiptRevision:        command.ReceiptRevision, FencingToken: command.FencingToken,
		LeaseAcquiredAt:  command.LeaseAcquiredAt.UTC(),
		LeaseHeartbeatAt: command.LeaseHeartbeatAt.UTC(),
		LeaseExpiresAt:   command.LeaseExpiresAt.UTC(), WorkerVersion: command.WorkerVersion,
		Status: command.Status, Decision: command.Decision,
		Classification: command.Classification, ReasonCode: command.ReasonCode,
		RetentionPhase: command.RetentionPhase, ValidatorVersion: command.ValidatorVersion,
		ClassifiedAt:      command.ClassifiedAt.UTC(),
		ManifestInventory: firestoreInventorySummary(command.ManifestInventory),
		RawInventory:      firestoreInventorySummary(command.RawInventory),
		Raw:               firestoreLineage(command.Raw), Manifest: firestoreLineage(command.Manifest),
		CreatedAt: command.CreatedAt.UTC(), UpdatedAt: command.CreatedAt.UTC(), TargetHash: targetHash,
	}
}

func (target firestoreIngestCleanupTarget) toDomain() (ingest.CleanupTarget, error) {
	command := ingest.CleanupTargetCommand{
		SchemaVersion: target.SchemaVersion, CleanupID: target.CleanupID,
		TenantID: target.TenantID, ReceiptID: target.ReceiptID,
		ReservationKey: target.ReservationKey, AttemptID: target.AttemptID,
		Mode: target.Mode, OriginStatus: target.OriginStatus,
		CleanupPolicyVersion:   target.CleanupPolicyVersion,
		CleanupTransitionedAt:  target.CleanupTransitionedAt.UTC(),
		CleanupQuiescenceUntil: target.CleanupQuiescenceUntil.UTC(),
		ReceiptRevision:        target.ReceiptRevision, FencingToken: target.FencingToken,
		LeaseAcquiredAt:  target.LeaseAcquiredAt.UTC(),
		LeaseHeartbeatAt: target.LeaseHeartbeatAt.UTC(),
		LeaseExpiresAt:   target.LeaseExpiresAt.UTC(), WorkerVersion: target.WorkerVersion,
		Status: target.Status, Decision: target.Decision,
		Classification: target.Classification, ReasonCode: target.ReasonCode,
		RetentionPhase: target.RetentionPhase, ValidatorVersion: target.ValidatorVersion,
		ClassifiedAt:      target.ClassifiedAt.UTC(),
		ManifestInventory: target.ManifestInventory.toDomain(), RawInventory: target.RawInventory.toDomain(),
		Raw: target.Raw.toDomain(), Manifest: target.Manifest.toDomain(), CreatedAt: target.CreatedAt.UTC(),
	}
	if !target.UpdatedAt.Equal(target.CreatedAt) || !lowerHexDigest(target.TargetHash) ||
		ingest.ValidateCleanupTargetCommand(command) != nil {
		return ingest.CleanupTarget{}, ingest.ErrCleanupTargetConflict
	}
	hash, err := ingest.CleanupTargetHash(command)
	if err != nil || hash != target.TargetHash {
		return ingest.CleanupTarget{}, ingest.ErrCleanupTargetConflict
	}
	return ingest.CleanupTarget{Command: command, TargetHash: target.TargetHash}, nil
}

func firestoreInventorySummary(summary ingest.ArtifactInventorySummary) firestoreArtifactInventorySummary {
	return firestoreArtifactInventorySummary{
		Performed: summary.Performed, NonSoftDeletedCount: summary.NonSoftDeletedCount,
		SoftDeletedCount: summary.SoftDeletedCount, Truncated: summary.Truncated, Coverage: summary.Coverage,
	}
}

func (summary firestoreArtifactInventorySummary) toDomain() ingest.ArtifactInventorySummary {
	return ingest.ArtifactInventorySummary{
		Performed: summary.Performed, NonSoftDeletedCount: summary.NonSoftDeletedCount,
		SoftDeletedCount: summary.SoftDeletedCount, Truncated: summary.Truncated, Coverage: summary.Coverage,
	}
}

func firestoreLineage(lineage *ingest.ArtifactLineage) *firestoreArtifactLineage {
	if lineage == nil {
		return nil
	}
	return &firestoreArtifactLineage{
		Path: lineage.Path, SHA256: lineage.SHA256, CRC32C: int64(lineage.CRC32C),
		Size: lineage.Size, Generation: lineage.Generation, Metageneration: lineage.Metageneration,
	}
}

func (lineage *firestoreArtifactLineage) toDomain() *ingest.ArtifactLineage {
	if lineage == nil {
		return nil
	}
	if lineage.CRC32C < 0 || lineage.CRC32C > int64(^uint32(0)) {
		return &ingest.ArtifactLineage{}
	}
	return &ingest.ArtifactLineage{
		Path: lineage.Path, SHA256: lineage.SHA256, CRC32C: uint32(lineage.CRC32C),
		Size: lineage.Size, Generation: lineage.Generation, Metageneration: lineage.Metageneration,
	}
}
