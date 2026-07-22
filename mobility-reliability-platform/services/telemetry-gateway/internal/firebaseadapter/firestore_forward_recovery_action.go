package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var _ ingest.ForwardRecoveryActionStore = (*FirestoreAdmissionStore)(nil)

type recoveryAttemptRead struct {
	Attempt  firestoreRecoveryAttempt
	ReadTime time.Time
}

// forwardRecoveryActionTransaction is deliberately narrower than the mutable
// admission transaction. A transaction must explicitly opt in to both current
// relationship reads and exact recovery-attempt reads before it can finalize a
// sweeper action.
type forwardRecoveryActionTransaction interface {
	forwardRecoveryAuthorizationTransaction
	ReadRecoveryAttempt(context.Context, string) (recoveryAttemptRead, bool, error)
}

type currentForwardRecoveryActionValidator func(
	ingest.ForwardRecoveryActionGrant,
	ingest.ForwardRecoveryActionCommand,
	ingest.CurrentForwardRecoverySnapshot,
	ingest.CurrentForwardRecoveryAttempt,
	time.Time,
) error

func (s *FirestoreAdmissionStore) CommitForwardRecoveryAction(
	ctx context.Context,
	grant ingest.ForwardRecoveryActionGrant,
	command ingest.ForwardRecoveryActionCommand,
	observedAt time.Time,
) (ingest.Receipt, error) {
	command = cloneForwardRecoveryActionCommand(command)
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() ||
		!telemetry.IsUUID(command.TenantID) || !lowerHexDigest(command.ReservationKey) {
		return ingest.Receipt{}, ingest.ErrInvalidForwardRecoveryActionAuthorization
	}
	observedAt = observedAt.UTC()
	if err := ingest.ValidateForwardRecoveryActionAuthorization(grant, command, observedAt); err != nil {
		return ingest.Receipt{}, err
	}
	deadline, err := ingest.ForwardRecoveryActionAuthorizationDeadline(grant, command)
	if err != nil {
		return ingest.Receipt{}, err
	}
	actionContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	result, commitErr := s.commitForwardRecoveryAction(
		actionContext,
		grant,
		command,
		observedAt,
		ingest.ValidateCurrentForwardRecoveryAction,
	)
	if commitErr != nil {
		return ingest.Receipt{}, normalizeForwardRecoveryActionStoreError(ctx, actionContext, commitErr)
	}
	return result, nil
}

// commitForwardRecoveryAction keeps the validation seam package-private so
// adapter tests can exercise atomic mutation without exposing a way to mint or
// bypass opaque grants to production callers. The public method always passes
// the domain validator above.
func (s *FirestoreAdmissionStore) commitForwardRecoveryAction(
	ctx context.Context,
	grant ingest.ForwardRecoveryActionGrant,
	command ingest.ForwardRecoveryActionCommand,
	observedAt time.Time,
	validateCurrent currentForwardRecoveryActionValidator,
) (ingest.Receipt, error) {
	command = cloneForwardRecoveryActionCommand(command)
	if s == nil || s.runTransaction == nil || ctx == nil || validateCurrent == nil {
		return ingest.Receipt{}, ingest.ErrAdmissionUnavailable
	}

	var result ingest.Receipt
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.Receipt{}
		linked, loadErr := loadLinkedReceipt(
			runContext,
			transaction,
			command.TenantID,
			command.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}

		actionTransaction, ok := transaction.(forwardRecoveryActionTransaction)
		if !ok {
			return ingest.ErrAdmissionUnavailable
		}
		snapshot, relationErr := actionTransaction.LoadCurrentForwardRecoveryRelations(
			runContext,
			linked.Receipt.Receipt,
		)
		if relationErr != nil {
			return relationErr
		}
		attemptPath := recoveryAttemptDocumentPath(
			linked.Receipt.Receipt.TenantID,
			linked.Receipt.Receipt.ReceiptID,
			command.Attempt.ID,
		)
		attemptResult, exists, attemptErr := actionTransaction.ReadRecoveryAttempt(runContext, attemptPath)
		if attemptErr != nil {
			return attemptErr
		}
		if !exists || validateStartedRecoveryAttempt(
			attemptResult.Attempt,
			linked.Receipt.Receipt,
			command,
		) != nil {
			return ingest.ErrInvalidForwardRecoveryActionAuthorization
		}

		readTime, clockErr := coherentForwardRecoveryActionReadTime(
			linked.Receipt.ReadTime,
			snapshot.ReadTime,
			attemptResult.ReadTime,
		)
		if clockErr != nil {
			return clockErr
		}
		effectiveAt, clockErr := conservativeAcceptanceTime(observedAt, readTime)
		if clockErr != nil {
			return ingest.ErrForwardRecoveryAuthorizationUnavailable
		}
		snapshot.Receipt = linked.Receipt.Receipt.toDomain()
		snapshot.ReadTime = readTime
		currentAttempt := currentRecoveryAttempt(attemptResult.Attempt)
		if validationErr := validateCurrent(
			grant,
			command,
			snapshot,
			currentAttempt,
			effectiveAt,
		); validationErr != nil {
			return validationErr
		}

		actionHash, hashErr := ingest.ForwardRecoveryActionHash(command)
		if hashErr != nil {
			return hashErr
		}
		receipt := linked.Receipt.Receipt
		receiptUpdates, mutationErr := applyForwardRecoveryReceiptAction(
			&receipt,
			command,
			effectiveAt,
		)
		if mutationErr != nil || validateReceiptLinkage(receipt, linked.Index) != nil ||
			validateReceiptState(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		attemptUpdates, completionErr := forwardRecoveryAttemptCompletionUpdates(
			attemptResult.Attempt,
			command,
			actionHash,
			effectiveAt,
		)
		if completionErr != nil {
			return completionErr
		}

		// All authoritative reads and pure validation finish before either write.
		if updateErr := transaction.Update(runContext, linked.ReceiptPath, receiptUpdates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		if updateErr := transaction.Update(runContext, attemptPath, attemptUpdates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		result = receipt.toDomain()
		return nil
	})
	if err != nil {
		return ingest.Receipt{}, err
	}
	if result.ReceiptID == "" {
		return ingest.Receipt{}, ingest.ErrAdmissionUnavailable
	}
	return result, nil
}

func cloneForwardRecoveryActionCommand(
	command ingest.ForwardRecoveryActionCommand,
) ingest.ForwardRecoveryActionCommand {
	if command.Plan.Raw != nil {
		raw := *command.Plan.Raw
		command.Plan.Raw = &raw
	}
	if command.Plan.Manifest != nil {
		manifest := *command.Plan.Manifest
		command.Plan.Manifest = &manifest
	}
	return command
}

func (transaction firestoreAdmissionTransaction) ReadRecoveryAttempt(
	ctx context.Context,
	path string,
) (recoveryAttemptRead, bool, error) {
	document, err := transaction.transaction.Get(transaction.client.Doc(path))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return recoveryAttemptRead{}, false, nil
		}
		return recoveryAttemptRead{}, false, normalizeAdmissionError(ctx, err)
	}
	var attempt firestoreRecoveryAttempt
	if document == nil || !document.Exists() || document.ReadTime.IsZero() || document.DataTo(&attempt) != nil {
		return recoveryAttemptRead{}, false, ingest.ErrAdmissionUnavailable
	}
	return recoveryAttemptRead{Attempt: attempt, ReadTime: document.ReadTime.UTC()}, true, nil
}

func coherentForwardRecoveryActionReadTime(readTimes ...time.Time) (time.Time, error) {
	var earliest time.Time
	var latest time.Time
	for _, readTime := range readTimes {
		readTime = readTime.UTC()
		if readTime.IsZero() {
			return time.Time{}, ingest.ErrForwardRecoveryAuthorizationUnavailable
		}
		if earliest.IsZero() || readTime.Before(earliest) {
			earliest = readTime
		}
		if readTime.After(latest) {
			latest = readTime
		}
	}
	if !withinAdmissionClockSkew(earliest, latest) {
		return time.Time{}, ingest.ErrForwardRecoveryAuthorizationUnavailable
	}
	return latest, nil
}

func currentRecoveryAttempt(attempt firestoreRecoveryAttempt) ingest.CurrentForwardRecoveryAttempt {
	return ingest.CurrentForwardRecoveryAttempt{
		AttemptID:     attempt.AttemptID,
		TenantID:      attempt.TenantID,
		ReceiptID:     attempt.ReceiptID,
		OwnerKind:     attempt.OwnerKind,
		FencingToken:  attempt.FencingToken,
		WorkerVersion: attempt.WorkerVersion,
		Status:        attempt.Status,
		StartedAt:     attempt.StartedAt.UTC(),
	}
}

func validateStartedRecoveryAttempt(
	attempt firestoreRecoveryAttempt,
	receipt firestoreIngestReceipt,
	command ingest.ForwardRecoveryActionCommand,
) error {
	if attempt.AttemptID != command.Attempt.ID || attempt.TenantID != command.TenantID ||
		attempt.ReceiptID != receipt.ReceiptID || attempt.OwnerKind != ingest.LeaseOwnerSweeper ||
		attempt.FencingToken != command.Fence.Token ||
		attempt.WorkerVersion != command.Attempt.WorkerVersion ||
		attempt.Status != ingest.RecoveryAttemptStarted || attempt.StartedAt.IsZero() ||
		!attempt.StartedAt.Equal(receipt.LeaseAcquiredAt) ||
		attempt.Phase != "" || attempt.Classification != "" || attempt.ReasonCode != "" ||
		attempt.Action != "" || attempt.Outcome != "" || attempt.ActionHash != "" ||
		attempt.HoldCode != "" || attempt.ReleaseCode != "" || attempt.RejectionCode != "" ||
		attempt.RawSHA256 != "" || attempt.RawCRC32C != 0 || attempt.RawSize != 0 ||
		attempt.RawGeneration != 0 || attempt.RawMetageneration != 0 ||
		attempt.ManifestSHA256 != "" || attempt.ManifestCRC32C != 0 || attempt.ManifestSize != 0 ||
		attempt.ManifestGeneration != 0 || attempt.ManifestMetageneration != 0 ||
		!attempt.HoldReviewDueAt.IsZero() || !attempt.CompletedAt.IsZero() {
		return ingest.ErrInvalidForwardRecoveryActionAuthorization
	}
	return nil
}

func applyForwardRecoveryReceiptAction(
	receipt *firestoreIngestReceipt,
	command ingest.ForwardRecoveryActionCommand,
	effectiveAt time.Time,
) ([]firestore.Update, error) {
	if receipt == nil || effectiveAt.IsZero() || receipt.Revision != command.ReceiptRevision {
		return nil, ingest.ErrInvalidForwardRecoveryActionAuthorization
	}
	nextRevision := receipt.Revision + 1
	if nextRevision <= receipt.Revision {
		return nil, ingest.ErrAdmissionUnavailable
	}
	effectiveAt = effectiveAt.UTC()
	leaseClears := []firestore.Update{
		{Path: "lease_owner_id", Value: firestore.Delete},
		{Path: "lease_owner_kind", Value: firestore.Delete},
		{Path: "lease_acquired_at", Value: firestore.Delete},
		{Path: "lease_heartbeat_at", Value: firestore.Delete},
		{Path: "lease_expires_at", Value: firestore.Delete},
	}
	updates := []firestore.Update{{Path: "revision", Value: nextRevision}, {Path: "updated_at", Value: effectiveAt}}

	switch command.Plan.Action {
	case ingest.ForwardRecoveryActionMarkStored:
		stored, err := storedDataFromForwardRecoveryAction(command, receipt.ExpectedSampleCount)
		if err != nil || validateStoredReceiptData(stored, receipt.TenantID) != nil ||
			stored.Artifacts.Object.Path != expectedObjectPath(*receipt) ||
			stored.Artifacts.Manifest.Path != expectedManifestPath(*receipt) {
			return nil, ingest.ErrInvalidForwardRecoveryActionAuthorization
		}
		updates = append(updates,
			firestore.Update{Path: "status", Value: string(ingest.ReceiptStored)},
			firestore.Update{Path: "object_path", Value: stored.Artifacts.Object.Path},
			firestore.Update{Path: "object_sha256", Value: stored.Artifacts.Object.SHA256},
			firestore.Update{Path: "object_crc32c", Value: int64(stored.Artifacts.Object.CRC32C)},
			firestore.Update{Path: "object_size", Value: stored.Artifacts.Object.Size},
			firestore.Update{Path: "object_generation", Value: stored.Artifacts.Object.Generation},
			firestore.Update{Path: "object_metageneration", Value: stored.Artifacts.Object.Metageneration},
			firestore.Update{Path: "manifest_path", Value: stored.Artifacts.Manifest.Path},
			firestore.Update{Path: "manifest_sha256", Value: stored.Artifacts.Manifest.SHA256},
			firestore.Update{Path: "manifest_crc32c", Value: int64(stored.Artifacts.Manifest.CRC32C)},
			firestore.Update{Path: "manifest_size", Value: stored.Artifacts.Manifest.Size},
			firestore.Update{Path: "manifest_generation", Value: stored.Artifacts.Manifest.Generation},
			firestore.Update{Path: "manifest_metageneration", Value: stored.Artifacts.Manifest.Metageneration},
			firestore.Update{Path: "sample_count", Value: stored.SampleCount},
			firestore.Update{Path: "rejection_code", Value: firestore.Delete},
			firestore.Update{Path: "last_recovery_code", Value: firestore.Delete},
			firestore.Update{Path: "hold_reason", Value: firestore.Delete},
			firestore.Update{Path: "hold_review_due_at", Value: firestore.Delete},
			firestore.Update{Path: "next_recovery_at", Value: firestore.Delete},
		)
		updates = append(updates, leaseClears...)
		receipt.State = ingest.ReceiptStored
		receipt.RejectionCode = ""
		receipt.LastRecoveryCode = ""
		receipt.RecoveryHoldCode = ""
		receipt.RecoveryHoldReviewDueAt = time.Time{}
		receipt.applyStoredData(stored)
		receipt.clearLease()

	case ingest.ForwardRecoveryActionMarkRejected:
		if command.Plan.Raw == nil || command.Plan.Raw.Path != expectedObjectPath(*receipt) {
			return nil, ingest.ErrInvalidForwardRecoveryActionAuthorization
		}
		updates = append(updates,
			firestore.Update{Path: "status", Value: string(ingest.ReceiptRejected)},
			firestore.Update{Path: "rejection_code", Value: command.Plan.RejectionCode},
			firestore.Update{Path: "last_recovery_code", Value: firestore.Delete},
			firestore.Update{Path: "hold_reason", Value: firestore.Delete},
			firestore.Update{Path: "hold_review_due_at", Value: firestore.Delete},
			firestore.Update{Path: "next_recovery_at", Value: firestore.Delete},
		)
		updates = append(updates, leaseClears...)
		receipt.State = ingest.ReceiptRejected
		receipt.RejectionCode = command.Plan.RejectionCode
		receipt.LastRecoveryCode = ""
		receipt.RecoveryHoldCode = ""
		receipt.RecoveryHoldReviewDueAt = time.Time{}
		receipt.clearLease()

	case ingest.ForwardRecoveryActionMarkHold:
		updates = append(updates,
			firestore.Update{Path: "status", Value: string(ingest.ReceiptRecoveryHold)},
			firestore.Update{Path: "rejection_code", Value: firestore.Delete},
			firestore.Update{Path: "last_recovery_code", Value: firestore.Delete},
			firestore.Update{Path: "hold_reason", Value: string(command.Plan.HoldCode)},
			firestore.Update{Path: "hold_review_due_at", Value: command.HoldReviewDueAt.UTC()},
			firestore.Update{Path: "next_recovery_at", Value: firestore.Delete},
		)
		updates = append(updates, leaseClears...)
		receipt.State = ingest.ReceiptRecoveryHold
		receipt.RejectionCode = ""
		receipt.LastRecoveryCode = ""
		receipt.RecoveryHoldCode = command.Plan.HoldCode
		receipt.RecoveryHoldReviewDueAt = command.HoldReviewDueAt.UTC()
		receipt.clearLease()

	case ingest.ForwardRecoveryActionReleaseLease:
		nextRecoveryAt := effectiveAt.Add(ingest.InitialRecoveryBackoff)
		if nextRecoveryAt.After(receipt.ReservationDeadline) {
			nextRecoveryAt = receipt.ReservationDeadline.UTC()
		}
		updates = append(updates,
			firestore.Update{Path: "status", Value: string(ingest.ReceiptReserved)},
			firestore.Update{Path: "rejection_code", Value: firestore.Delete},
			firestore.Update{Path: "hold_reason", Value: firestore.Delete},
			firestore.Update{Path: "hold_review_due_at", Value: firestore.Delete},
			firestore.Update{Path: "next_recovery_at", Value: nextRecoveryAt},
			firestore.Update{Path: "last_recovery_code", Value: string(command.Plan.ReleaseCode)},
		)
		updates = append(updates, leaseClears...)
		receipt.State = ingest.ReceiptReserved
		receipt.RejectionCode = ""
		receipt.RecoveryHoldCode = ""
		receipt.RecoveryHoldReviewDueAt = time.Time{}
		receipt.clearLease()
		receipt.NextRecoveryAt = nextRecoveryAt
		receipt.LastRecoveryCode = string(command.Plan.ReleaseCode)

	default:
		return nil, ingest.ErrInvalidForwardRecoveryActionAuthorization
	}
	receipt.Revision = nextRevision
	receipt.UpdatedAt = effectiveAt
	return updates, nil
}

func storedDataFromForwardRecoveryAction(
	command ingest.ForwardRecoveryActionCommand,
	sampleCount int,
) (ingest.StoredReceiptData, error) {
	if command.Plan.Raw == nil || command.Plan.Manifest == nil {
		return ingest.StoredReceiptData{}, ingest.ErrInvalidForwardRecoveryActionAuthorization
	}
	return ingest.StoredReceiptData{
		Artifacts: ingest.StoredBatchArtifacts{
			Object:   storedArtifactFromLineage(*command.Plan.Raw),
			Manifest: storedArtifactFromLineage(*command.Plan.Manifest),
		},
		SampleCount: sampleCount,
	}, nil
}

func storedArtifactFromLineage(lineage ingest.ArtifactLineage) ingest.StoredArtifact {
	return ingest.StoredArtifact{
		Path: lineage.Path, SHA256: lineage.SHA256, CRC32C: lineage.CRC32C,
		Size: lineage.Size, Generation: lineage.Generation, Metageneration: lineage.Metageneration,
	}
}

func forwardRecoveryAttemptCompletionUpdates(
	attempt firestoreRecoveryAttempt,
	command ingest.ForwardRecoveryActionCommand,
	actionHash string,
	completedAt time.Time,
) ([]firestore.Update, error) {
	outcome, err := forwardRecoveryAttemptOutcome(command.Plan.Action)
	if err != nil || !lowerHexDigest(actionHash) || completedAt.IsZero() ||
		!completedAt.After(attempt.StartedAt) {
		return nil, ingest.ErrInvalidForwardRecoveryActionAuthorization
	}
	updates := []firestore.Update{
		{Path: "status", Value: string(ingest.RecoveryAttemptCompleted)},
		{Path: "phase", Value: string(command.Plan.Phase)},
		{Path: "classification", Value: string(command.Plan.Classification)},
		{Path: "reason_code", Value: string(command.Plan.ReasonCode)},
		{Path: "action", Value: string(command.Plan.Action)},
		{Path: "outcome", Value: string(outcome)},
		{Path: "action_hash", Value: actionHash},
		{Path: "completed_at", Value: completedAt.UTC()},
	}
	if command.Plan.HoldCode != "" {
		updates = append(updates, firestore.Update{Path: "hold_code", Value: string(command.Plan.HoldCode)})
	}
	if command.Plan.ReleaseCode != "" {
		updates = append(updates, firestore.Update{Path: "release_code", Value: string(command.Plan.ReleaseCode)})
	}
	if command.Plan.RejectionCode != "" {
		updates = append(updates, firestore.Update{Path: "rejection_code", Value: command.Plan.RejectionCode})
	}
	if command.Plan.Raw != nil {
		updates = append(updates, artifactLineageSummaryUpdates("raw", *command.Plan.Raw)...)
	}
	if command.Plan.Manifest != nil {
		updates = append(updates, artifactLineageSummaryUpdates("manifest", *command.Plan.Manifest)...)
	}
	if !command.HoldReviewDueAt.IsZero() {
		updates = append(updates, firestore.Update{Path: "hold_review_due_at", Value: command.HoldReviewDueAt.UTC()})
	}
	return updates, nil
}

func artifactLineageSummaryUpdates(prefix string, lineage ingest.ArtifactLineage) []firestore.Update {
	return []firestore.Update{
		{Path: prefix + "_sha256", Value: lineage.SHA256},
		{Path: prefix + "_crc32c", Value: int64(lineage.CRC32C)},
		{Path: prefix + "_size", Value: lineage.Size},
		{Path: prefix + "_generation", Value: lineage.Generation},
		{Path: prefix + "_metageneration", Value: lineage.Metageneration},
	}
}

func forwardRecoveryAttemptOutcome(
	action ingest.ForwardRecoveryAction,
) (ingest.RecoveryAttemptOutcome, error) {
	switch action {
	case ingest.ForwardRecoveryActionMarkStored:
		return ingest.RecoveryAttemptOutcomeStored, nil
	case ingest.ForwardRecoveryActionMarkRejected:
		return ingest.RecoveryAttemptOutcomeRejected, nil
	case ingest.ForwardRecoveryActionMarkHold:
		return ingest.RecoveryAttemptOutcomeHold, nil
	case ingest.ForwardRecoveryActionReleaseLease:
		return ingest.RecoveryAttemptOutcomeLeaseReleased, nil
	default:
		return "", ingest.ErrInvalidForwardRecoveryActionAuthorization
	}
}

func normalizeForwardRecoveryActionStoreError(
	parent context.Context,
	actionContext context.Context,
	err error,
) error {
	if parent != nil {
		if contextErr := parent.Err(); contextErr != nil {
			return contextErr
		}
	}
	if actionContext != nil && errors.Is(actionContext.Err(), context.DeadlineExceeded) {
		return ingest.ErrForwardRecoveryActionAuthorizationExpired
	}
	if errors.Is(err, ingest.ErrInvalidForwardRecoveryActionAuthorization) ||
		errors.Is(err, ingest.ErrForwardRecoveryActionAuthorizationExpired) ||
		errors.Is(err, ingest.ErrForwardRecoveryUnauthorized) ||
		errors.Is(err, ingest.ErrForwardRecoveryAuthorizationUnavailable) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ingest.ErrAdmissionUnavailable
}
