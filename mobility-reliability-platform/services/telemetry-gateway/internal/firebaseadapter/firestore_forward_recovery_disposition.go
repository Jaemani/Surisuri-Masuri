package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var _ ingest.ForwardRecoveryDispositionStore = (*FirestoreAdmissionStore)(nil)

type currentForwardRecoveryDispositionValidator func(
	ingest.ForwardRecoveryDispositionGrant,
	ingest.ForwardRecoveryDispositionCommand,
	ingest.CurrentForwardRecoverySnapshot,
	ingest.CurrentForwardRecoveryAttempt,
	time.Time,
) error

func (s *FirestoreAdmissionStore) CommitForwardRecoveryDisposition(
	ctx context.Context,
	grant ingest.ForwardRecoveryDispositionGrant,
	command ingest.ForwardRecoveryDispositionCommand,
	observedAt time.Time,
) (ingest.Receipt, error) {
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() ||
		!telemetry.IsUUID(command.TenantID) || !lowerHexDigest(command.ReservationKey) {
		return ingest.Receipt{}, ingest.ErrInvalidForwardRecoveryDispositionAuthorization
	}
	observedAt = observedAt.UTC()
	if err := ingest.ValidateForwardRecoveryDispositionAuthorization(grant, command, observedAt); err != nil {
		return ingest.Receipt{}, err
	}
	deadline, err := ingest.ForwardRecoveryDispositionAuthorizationDeadline(grant, command)
	if err != nil {
		return ingest.Receipt{}, err
	}
	dispositionContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	result, commitErr := s.commitForwardRecoveryDisposition(
		dispositionContext,
		grant,
		command,
		observedAt,
		ingest.ValidateCurrentForwardRecoveryDisposition,
	)
	if commitErr != nil {
		return ingest.Receipt{}, normalizeForwardRecoveryDispositionStoreError(
			ctx, dispositionContext, commitErr,
		)
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) commitForwardRecoveryDisposition(
	ctx context.Context,
	grant ingest.ForwardRecoveryDispositionGrant,
	command ingest.ForwardRecoveryDispositionCommand,
	observedAt time.Time,
	validateCurrent currentForwardRecoveryDispositionValidator,
) (ingest.Receipt, error) {
	if s == nil || s.runTransaction == nil || ctx == nil || validateCurrent == nil {
		return ingest.Receipt{}, ingest.ErrAdmissionUnavailable
	}

	var result ingest.Receipt
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.Receipt{}
		linked, loadErr := loadLinkedReceipt(
			runContext, transaction, command.TenantID, command.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}
		dispositionTransaction, ok := transaction.(forwardRecoveryActionTransaction)
		if !ok {
			return ingest.ErrAdmissionUnavailable
		}
		snapshot, relationErr := dispositionTransaction.LoadCurrentForwardRecoveryRelations(
			runContext, linked.Receipt.Receipt,
		)
		if relationErr != nil {
			return relationErr
		}
		attemptPath := recoveryAttemptDocumentPath(
			linked.Receipt.Receipt.TenantID,
			linked.Receipt.Receipt.ReceiptID,
			command.Attempt.ID,
		)
		attemptResult, exists, attemptErr := dispositionTransaction.ReadRecoveryAttempt(
			runContext, attemptPath,
		)
		if attemptErr != nil {
			return attemptErr
		}
		if !exists || validateStartedRecoveryAttemptForOwner(
			attemptResult.Attempt,
			linked.Receipt.Receipt,
			command.Attempt,
			command.Fence,
			ingest.LeaseOwnerSweeper,
		) != nil {
			return ingest.ErrInvalidForwardRecoveryDispositionAuthorization
		}

		readTime, clockErr := coherentForwardRecoveryActionReadTime(
			linked.Receipt.ReadTime, snapshot.ReadTime, attemptResult.ReadTime,
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
		if validationErr := validateCurrent(
			grant,
			command,
			snapshot,
			currentRecoveryAttempt(attemptResult.Attempt),
			effectiveAt,
		); validationErr != nil {
			return validationErr
		}

		actionHash, hashErr := ingest.ForwardRecoveryDispositionHash(command)
		if hashErr != nil {
			return hashErr
		}
		receipt := linked.Receipt.Receipt
		receiptUpdates, mutationErr := applyForwardRecoveryDisposition(
			&receipt, command, effectiveAt,
		)
		if mutationErr != nil || validateReceiptLinkage(receipt, linked.Index) != nil ||
			validateReceiptState(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		attemptUpdates, completionErr := forwardRecoveryDispositionCompletionUpdates(
			attemptResult.Attempt, command, actionHash, effectiveAt,
		)
		if completionErr != nil {
			return completionErr
		}

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

func applyForwardRecoveryDisposition(
	receipt *firestoreIngestReceipt,
	command ingest.ForwardRecoveryDispositionCommand,
	effectiveAt time.Time,
) ([]firestore.Update, error) {
	if receipt == nil || effectiveAt.IsZero() || receipt.Revision != command.ReceiptRevision {
		return nil, ingest.ErrInvalidForwardRecoveryDispositionAuthorization
	}
	nextRevision := receipt.Revision + 1
	if nextRevision <= receipt.Revision {
		return nil, ingest.ErrAdmissionUnavailable
	}
	effectiveAt = effectiveAt.UTC()
	updates := []firestore.Update{
		{Path: "revision", Value: nextRevision},
		{Path: "updated_at", Value: effectiveAt},
		{Path: "rejection_code", Value: firestore.Delete},
		{Path: "lease_owner_id", Value: firestore.Delete},
		{Path: "lease_owner_kind", Value: firestore.Delete},
		{Path: "lease_acquired_at", Value: firestore.Delete},
		{Path: "lease_heartbeat_at", Value: firestore.Delete},
		{Path: "lease_expires_at", Value: firestore.Delete},
	}
	switch command.Disposition {
	case ingest.ForwardRecoveryAuthorizationDenied:
		updates = append(updates,
			firestore.Update{Path: "status", Value: string(ingest.ReceiptRecoveryHold)},
			firestore.Update{Path: "last_recovery_code", Value: firestore.Delete},
			firestore.Update{Path: "hold_reason", Value: string(ingest.RecoveryHoldCurrentAuthorizationDenied)},
			firestore.Update{Path: "hold_review_due_at", Value: command.HoldReviewDueAt.UTC()},
			firestore.Update{Path: "next_recovery_at", Value: firestore.Delete},
		)
		receipt.State = ingest.ReceiptRecoveryHold
		receipt.RejectionCode = ""
		receipt.LastRecoveryCode = ""
		receipt.RecoveryHoldCode = ingest.RecoveryHoldCurrentAuthorizationDenied
		receipt.RecoveryHoldReviewDueAt = command.HoldReviewDueAt.UTC()
		receipt.clearLease()
	case ingest.ForwardRecoveryAuthorizationUnavailable:
		nextRecoveryAt := effectiveAt.Add(ingest.InitialRecoveryBackoff)
		if nextRecoveryAt.After(receipt.ReservationDeadline) {
			nextRecoveryAt = receipt.ReservationDeadline.UTC()
		}
		updates = append(updates,
			firestore.Update{Path: "status", Value: string(ingest.ReceiptReserved)},
			firestore.Update{Path: "hold_reason", Value: firestore.Delete},
			firestore.Update{Path: "hold_review_due_at", Value: firestore.Delete},
			firestore.Update{Path: "next_recovery_at", Value: nextRecoveryAt},
			firestore.Update{Path: "last_recovery_code", Value: string(ingest.LeaseReleaseAuthorizationUnavailable)},
		)
		receipt.State = ingest.ReceiptReserved
		receipt.RejectionCode = ""
		receipt.RecoveryHoldCode = ""
		receipt.RecoveryHoldReviewDueAt = time.Time{}
		receipt.clearLease()
		receipt.NextRecoveryAt = nextRecoveryAt
		receipt.LastRecoveryCode = string(ingest.LeaseReleaseAuthorizationUnavailable)
	default:
		return nil, ingest.ErrInvalidForwardRecoveryDispositionAuthorization
	}
	receipt.Revision = nextRevision
	receipt.UpdatedAt = effectiveAt
	return updates, nil
}

func forwardRecoveryDispositionCompletionUpdates(
	attempt firestoreRecoveryAttempt,
	command ingest.ForwardRecoveryDispositionCommand,
	actionHash string,
	completedAt time.Time,
) ([]firestore.Update, error) {
	if !lowerHexDigest(actionHash) || completedAt.IsZero() ||
		!completedAt.After(attempt.StartedAt) {
		return nil, ingest.ErrInvalidForwardRecoveryDispositionAuthorization
	}
	updates := []firestore.Update{
		{Path: "status", Value: string(ingest.RecoveryAttemptCompleted)},
		{Path: "decision_domain", Value: string(ingest.ForwardRecoveryDecisionCurrentAuthorization)},
		{Path: "authorization_disposition", Value: string(command.Disposition)},
		{Path: "action_hash", Value: actionHash},
		{Path: "completed_at", Value: completedAt.UTC()},
	}
	switch command.Disposition {
	case ingest.ForwardRecoveryAuthorizationDenied:
		return append(updates,
			firestore.Update{Path: "action", Value: string(ingest.ForwardRecoveryActionMarkHold)},
			firestore.Update{Path: "outcome", Value: string(ingest.RecoveryAttemptOutcomeHold)},
			firestore.Update{Path: "hold_code", Value: string(ingest.RecoveryHoldCurrentAuthorizationDenied)},
			firestore.Update{Path: "hold_review_due_at", Value: command.HoldReviewDueAt.UTC()},
		), nil
	case ingest.ForwardRecoveryAuthorizationUnavailable:
		return append(updates,
			firestore.Update{Path: "action", Value: string(ingest.ForwardRecoveryActionReleaseLease)},
			firestore.Update{Path: "outcome", Value: string(ingest.RecoveryAttemptOutcomeLeaseReleased)},
			firestore.Update{Path: "release_code", Value: string(ingest.LeaseReleaseAuthorizationUnavailable)},
		), nil
	default:
		return nil, ingest.ErrInvalidForwardRecoveryDispositionAuthorization
	}
}

func normalizeForwardRecoveryDispositionStoreError(
	parent context.Context,
	dispositionContext context.Context,
	err error,
) error {
	if parent != nil {
		if contextErr := parent.Err(); contextErr != nil {
			return contextErr
		}
	}
	if dispositionContext != nil && errors.Is(dispositionContext.Err(), context.DeadlineExceeded) {
		return ingest.ErrForwardRecoveryDispositionAuthorizationExpired
	}
	if errors.Is(err, ingest.ErrInvalidForwardRecoveryDispositionAuthorization) ||
		errors.Is(err, ingest.ErrForwardRecoveryDispositionAuthorizationExpired) ||
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
