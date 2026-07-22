package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var _ ingest.ForwardRecoveryAttemptStore = (*FirestoreAdmissionStore)(nil)

type recoveryAttemptReaderTransaction interface {
	ReadRecoveryAttempt(context.Context, string) (recoveryAttemptRead, bool, error)
}

type currentForwardRecoveryAttemptFailureValidator func(
	ingest.ForwardRecoveryAttemptGrant,
	ingest.ForwardRecoveryAttemptFailure,
	ingest.Receipt,
	ingest.CurrentForwardRecoveryAttempt,
	time.Time,
) error

func (s *FirestoreAdmissionStore) FailForwardRecoveryAttempt(
	ctx context.Context,
	grant ingest.ForwardRecoveryAttemptGrant,
	failure ingest.ForwardRecoveryAttemptFailure,
	observedAt time.Time,
) error {
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() ||
		!telemetry.IsUUID(failure.TenantID) || !lowerHexDigest(failure.ReservationKey) {
		return ingest.ErrInvalidForwardRecoveryAttemptAuthorization
	}
	observedAt = observedAt.UTC()
	if err := ingest.ValidateForwardRecoveryAttemptAuthorization(grant, failure, observedAt); err != nil {
		return err
	}
	deadline, err := ingest.ForwardRecoveryAttemptAuthorizationDeadline(grant, failure)
	if err != nil {
		return err
	}
	attemptContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	err = s.failForwardRecoveryAttempt(
		attemptContext,
		grant,
		failure,
		observedAt,
		ingest.ValidateCurrentForwardRecoveryAttemptFailure,
	)
	return normalizeForwardRecoveryAttemptStoreError(ctx, attemptContext, err)
}

func (s *FirestoreAdmissionStore) failForwardRecoveryAttempt(
	ctx context.Context,
	grant ingest.ForwardRecoveryAttemptGrant,
	failure ingest.ForwardRecoveryAttemptFailure,
	observedAt time.Time,
	validateCurrent currentForwardRecoveryAttemptFailureValidator,
) error {
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() || validateCurrent == nil {
		return ingest.ErrAdmissionUnavailable
	}
	return s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		linked, loadErr := loadLinkedReceipt(
			runContext,
			transaction,
			failure.TenantID,
			failure.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}
		attemptReader, ok := transaction.(recoveryAttemptReaderTransaction)
		if !ok {
			return ingest.ErrAdmissionUnavailable
		}
		attemptPath := recoveryAttemptDocumentPath(
			linked.Receipt.Receipt.TenantID,
			linked.Receipt.Receipt.ReceiptID,
			failure.Attempt.ID,
		)
		attemptResult, exists, attemptErr := attemptReader.ReadRecoveryAttempt(runContext, attemptPath)
		if attemptErr != nil {
			return attemptErr
		}
		if !exists || validateStartedRecoveryAttempt(
			attemptResult.Attempt,
			linked.Receipt.Receipt,
			ingest.ForwardRecoveryActionCommand{
				TenantID: failure.TenantID, ReservationKey: failure.ReservationKey,
				Attempt: failure.Attempt, ReceiptRevision: failure.ReceiptRevision, Fence: failure.Fence,
			},
		) != nil {
			return ingest.ErrInvalidForwardRecoveryAttemptAuthorization
		}
		readTime, clockErr := coherentForwardRecoveryActionReadTime(
			linked.Receipt.ReadTime,
			attemptResult.ReadTime,
		)
		if clockErr != nil {
			return clockErr
		}
		effectiveAt, clockErr := conservativeAcceptanceTime(observedAt.UTC(), readTime)
		if clockErr != nil {
			return ingest.ErrForwardRecoveryAuthorizationUnavailable
		}
		if validationErr := validateCurrent(
			grant,
			failure,
			linked.Receipt.Receipt.toDomain(),
			currentRecoveryAttempt(attemptResult.Attempt),
			effectiveAt,
		); validationErr != nil {
			return validationErr
		}
		if !effectiveAt.After(attemptResult.Attempt.StartedAt) {
			return ingest.ErrInvalidForwardRecoveryAttemptAuthorization
		}
		return transaction.Update(runContext, attemptPath, []firestore.Update{
			{Path: "status", Value: string(ingest.RecoveryAttemptFailed)},
			{Path: "failure_code", Value: string(failure.FailureCode)},
			{Path: "failed_at", Value: effectiveAt},
		})
	})
}

func normalizeForwardRecoveryAttemptStoreError(
	parent context.Context,
	attemptContext context.Context,
	err error,
) error {
	if err == nil {
		return nil
	}
	if parent != nil {
		if contextErr := parent.Err(); contextErr != nil {
			return contextErr
		}
	}
	if attemptContext != nil && errors.Is(attemptContext.Err(), context.DeadlineExceeded) {
		return ingest.ErrForwardRecoveryAttemptAuthorizationExpired
	}
	if errors.Is(err, ingest.ErrInvalidForwardRecoveryAttemptAuthorization) ||
		errors.Is(err, ingest.ErrForwardRecoveryAttemptAuthorizationExpired) ||
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

func expiredPriorRecoveryAttemptClosure(
	ctx context.Context,
	transaction admissionTransaction,
	receipt firestoreIngestReceipt,
	effectiveAt time.Time,
) (string, []firestore.Update, time.Time, error) {
	if !receiptHasLeaseFields(receipt) {
		return "", nil, effectiveAt, nil
	}
	if receipt.RecoveryAttemptCount == 0 {
		if receipt.LeaseOwnerKind != ingest.LeaseOwnerRequest {
			return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
		}
		return "", nil, effectiveAt, nil
	}
	if receipt.RecoveryAttemptCount < 0 ||
		(receipt.LeaseOwnerKind != ingest.LeaseOwnerRequest &&
			receipt.LeaseOwnerKind != ingest.LeaseOwnerSweeper) {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	if effectiveAt.IsZero() || effectiveAt.Before(receipt.LeaseExpiresAt) ||
		!effectiveAt.Before(receipt.ReservationDeadline) {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	reader, ok := transaction.(recoveryAttemptReaderTransaction)
	if !ok {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	attemptPath := recoveryAttemptDocumentPath(
		receipt.TenantID,
		receipt.ReceiptID,
		receipt.LeaseOwnerID,
	)
	attemptResult, exists, err := reader.ReadRecoveryAttempt(ctx, attemptPath)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if !exists {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	effectiveAt, err = conservativeAcceptanceTime(effectiveAt, attemptResult.ReadTime)
	if err != nil || effectiveAt.Before(receipt.LeaseExpiresAt) ||
		!effectiveAt.Before(receipt.ReservationDeadline) {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	expected := ingest.RecoveryAttemptProposal{
		ID: receipt.LeaseOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion,
	}
	fence := ingest.LeaseFence{
		OwnerID: receipt.LeaseOwnerID, Token: receipt.FencingToken, ExpiresAt: receipt.LeaseExpiresAt,
	}
	switch attemptResult.Attempt.Status {
	case ingest.RecoveryAttemptStarted:
		if validateStartedRecoveryAttemptForOwner(
			attemptResult.Attempt,
			receipt,
			expected,
			fence,
			receipt.LeaseOwnerKind,
		) != nil || !effectiveAt.After(attemptResult.Attempt.StartedAt) {
			return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
		}
		return attemptPath, []firestore.Update{
			{Path: "status", Value: string(ingest.RecoveryAttemptFailed)},
			{Path: "failure_code", Value: string(ingest.RecoveryAttemptFailureLeaseExpired)},
			{Path: "failed_at", Value: effectiveAt.UTC()},
		}, effectiveAt, nil
	case ingest.RecoveryAttemptFailed:
		if validateFailedRecoveryAttemptForOwner(
			attemptResult.Attempt,
			receipt,
			expected,
			fence,
			receipt.LeaseOwnerKind,
			effectiveAt,
		) != nil {
			return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
		}
		return "", nil, effectiveAt, nil
	default:
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
}

func validateFailedRecoveryAttemptForOwner(
	attempt firestoreRecoveryAttempt,
	receipt firestoreIngestReceipt,
	expected ingest.RecoveryAttemptProposal,
	fence ingest.LeaseFence,
	ownerKind ingest.LeaseOwnerKind,
	observedAt time.Time,
) error {
	if attempt.AttemptID != expected.ID || attempt.TenantID != receipt.TenantID ||
		attempt.ReceiptID != receipt.ReceiptID || attempt.OwnerKind != ownerKind ||
		attempt.FencingToken != fence.Token || attempt.WorkerVersion != expected.WorkerVersion ||
		attempt.Status != ingest.RecoveryAttemptFailed || attempt.StartedAt.IsZero() ||
		!attempt.StartedAt.Equal(receipt.LeaseAcquiredAt) ||
		!ingest.ValidRecoveryAttemptFailureCode(attempt.FailureCode) || attempt.FailedAt.IsZero() ||
		!attempt.FailedAt.After(attempt.StartedAt) || attempt.FailedAt.After(observedAt) ||
		attempt.Phase != "" || attempt.Classification != "" || attempt.ReasonCode != "" ||
		attempt.Action != "" || attempt.Outcome != "" || attempt.ActionHash != "" ||
		attempt.HoldCode != "" || attempt.ReleaseCode != "" || attempt.RejectionCode != "" ||
		attempt.RawSHA256 != "" || attempt.RawCRC32C != 0 || attempt.RawSize != 0 ||
		attempt.RawGeneration != 0 || attempt.RawMetageneration != 0 ||
		attempt.ManifestSHA256 != "" || attempt.ManifestCRC32C != 0 || attempt.ManifestSize != 0 ||
		attempt.ManifestGeneration != 0 || attempt.ManifestMetageneration != 0 ||
		!attempt.HoldReviewDueAt.IsZero() || !attempt.CompletedAt.IsZero() {
		return ingest.ErrAdmissionUnavailable
	}
	if attempt.FailureCode == ingest.RecoveryAttemptFailureLeaseExpired {
		if attempt.FailedAt.Before(fence.ExpiresAt) {
			return ingest.ErrAdmissionUnavailable
		}
	} else if !attempt.FailedAt.Before(fence.ExpiresAt) {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}
