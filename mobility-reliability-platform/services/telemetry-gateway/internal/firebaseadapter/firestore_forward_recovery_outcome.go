package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var _ ingest.ForwardRecoveryOutcomeAuthorizationStore = (*FirestoreAdmissionStore)(nil)
var _ ingest.ForwardRecoveryOutcomeStore = (*FirestoreAdmissionStore)(nil)

func (s *FirestoreAdmissionStore) LoadCurrentForwardRecoveryOutcome(
	ctx context.Context,
	query ingest.ForwardRecoveryOutcomeQuery,
) (ingest.CurrentForwardRecoveryOutcomeSnapshot, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(query.TenantID) || !lowerHexDigest(query.ReservationKey) ||
		!telemetry.IsUUID(query.AttemptID) || !lowerHexDigest(query.ExpectedActionHash) ||
		ingest.ValidateLeaseFence(query.ExpectedFence) != nil ||
		query.AttemptID != query.ExpectedFence.OwnerID || query.ExpectedReceiptRevision <= 1 {
		return ingest.CurrentForwardRecoveryOutcomeSnapshot{}, ingest.ErrForwardRecoveryOutcomeUnavailable
	}
	var result ingest.CurrentForwardRecoveryOutcomeSnapshot
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.CurrentForwardRecoveryOutcomeSnapshot{}
		linked, loadErr := loadLinkedReceipt(
			runContext,
			transaction,
			query.TenantID,
			query.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}
		reader, ok := transaction.(recoveryAttemptReaderTransaction)
		if !ok {
			return ingest.ErrForwardRecoveryOutcomeUnavailable
		}
		attemptPath := recoveryAttemptDocumentPath(
			linked.Receipt.Receipt.TenantID,
			linked.Receipt.Receipt.ReceiptID,
			query.AttemptID,
		)
		attemptResult, exists, attemptErr := reader.ReadRecoveryAttempt(runContext, attemptPath)
		if attemptErr != nil {
			return attemptErr
		}
		if !exists {
			return ingest.ErrForwardRecoveryOutcomeUnavailable
		}
		readTime, clockErr := coherentForwardRecoveryActionReadTime(
			linked.Receipt.ReadTime,
			attemptResult.ReadTime,
		)
		if clockErr != nil {
			return clockErr
		}
		receipt, projectionErr := currentForwardRecoveryOutcomeReceipt(linked.Receipt.Receipt)
		if projectionErr != nil {
			return projectionErr
		}
		attempt, projectionErr := currentForwardRecoveryOutcomeAttempt(attemptResult.Attempt)
		if projectionErr != nil {
			return projectionErr
		}
		result = ingest.CurrentForwardRecoveryOutcomeSnapshot{
			Receipt:  receipt,
			Attempt:  attempt,
			ReadTime: readTime,
		}
		if _, evaluateErr := ingest.EvaluateForwardRecoveryActionOutcome(query, result, readTime); evaluateErr != nil {
			return evaluateErr
		}
		return nil
	})
	if err != nil {
		return ingest.CurrentForwardRecoveryOutcomeSnapshot{}, normalizeForwardRecoveryOutcomeStoreError(ctx, nil, err)
	}
	if result.Receipt.ReceiptID == "" || result.Attempt.AttemptID == "" || result.ReadTime.IsZero() {
		return ingest.CurrentForwardRecoveryOutcomeSnapshot{}, ingest.ErrForwardRecoveryOutcomeUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) GetForwardRecoveryActionOutcome(
	ctx context.Context,
	grant ingest.ForwardRecoveryOutcomeReadGrant,
	query ingest.ForwardRecoveryOutcomeQuery,
	observedAt time.Time,
) (ingest.RecoveryActionOutcome, error) {
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() {
		return ingest.RecoveryActionOutcome{}, ingest.ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	observedAt = observedAt.UTC()
	if err := ingest.ValidateForwardRecoveryOutcomeAuthorization(grant, query, observedAt); err != nil {
		return ingest.RecoveryActionOutcome{}, err
	}
	deadline, err := ingest.ForwardRecoveryOutcomeAuthorizationDeadline(grant, query)
	if err != nil {
		return ingest.RecoveryActionOutcome{}, err
	}
	outcomeContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	snapshot, err := s.LoadCurrentForwardRecoveryOutcome(outcomeContext, query)
	if err != nil {
		return ingest.RecoveryActionOutcome{}, normalizeForwardRecoveryOutcomeStoreError(ctx, outcomeContext, err)
	}
	effectiveAt, err := conservativeAcceptanceTime(observedAt, snapshot.ReadTime)
	if err != nil {
		return ingest.RecoveryActionOutcome{}, ingest.ErrForwardRecoveryOutcomeUnavailable
	}
	if err := ingest.ValidateForwardRecoveryOutcomeAuthorization(grant, query, effectiveAt); err != nil {
		return ingest.RecoveryActionOutcome{}, err
	}
	result, err := ingest.EvaluateForwardRecoveryActionOutcome(query, snapshot, effectiveAt)
	if err != nil {
		return ingest.RecoveryActionOutcome{}, normalizeForwardRecoveryOutcomeStoreError(ctx, outcomeContext, err)
	}
	return result, nil
}

func currentForwardRecoveryOutcomeAttempt(
	attempt firestoreRecoveryAttempt,
) (ingest.CurrentForwardRecoveryOutcomeAttempt, error) {
	if !validFirestoreCRC32C(attempt.RawCRC32C) || !validFirestoreCRC32C(attempt.ManifestCRC32C) {
		return ingest.CurrentForwardRecoveryOutcomeAttempt{}, ingest.ErrForwardRecoveryOutcomeUnavailable
	}
	return ingest.CurrentForwardRecoveryOutcomeAttempt{
		AttemptID: attempt.AttemptID, TenantID: attempt.TenantID, ReceiptID: attempt.ReceiptID,
		OwnerKind: attempt.OwnerKind, FencingToken: attempt.FencingToken,
		WorkerVersion: attempt.WorkerVersion, Status: attempt.Status,
		Phase: attempt.Phase, Classification: attempt.Classification, ReasonCode: attempt.ReasonCode,
		Action: attempt.Action, Outcome: attempt.Outcome, ActionHash: attempt.ActionHash,
		HoldCode: attempt.HoldCode, ReleaseCode: attempt.ReleaseCode,
		RejectionCode: attempt.RejectionCode,
		RawSHA256:     attempt.RawSHA256, RawCRC32C: uint32(attempt.RawCRC32C),
		RawSize: attempt.RawSize, RawGeneration: attempt.RawGeneration,
		RawMetageneration: attempt.RawMetageneration,
		ManifestSHA256:    attempt.ManifestSHA256, ManifestCRC32C: uint32(attempt.ManifestCRC32C),
		ManifestSize: attempt.ManifestSize, ManifestGeneration: attempt.ManifestGeneration,
		ManifestMetageneration: attempt.ManifestMetageneration,
		HoldReviewDueAt:        attempt.HoldReviewDueAt.UTC(),
		StartedAt:              attempt.StartedAt.UTC(), CompletedAt: attempt.CompletedAt.UTC(),
		FailureCode: attempt.FailureCode, FailedAt: attempt.FailedAt.UTC(),
	}, nil
}

func currentForwardRecoveryOutcomeReceipt(
	receipt firestoreIngestReceipt,
) (ingest.CurrentForwardRecoveryOutcomeReceipt, error) {
	if !validFirestoreCRC32C(receipt.ObjectCRC32C) ||
		!validFirestoreCRC32C(receipt.ManifestCRC32C) ||
		!validFirestoreOutcomeReceiptSurface(receipt) {
		return ingest.CurrentForwardRecoveryOutcomeReceipt{}, ingest.ErrForwardRecoveryOutcomeUnavailable
	}
	return ingest.CurrentForwardRecoveryOutcomeReceipt{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		ReceiptID: receipt.ReceiptID, State: receipt.State, Revision: receipt.Revision,
		ExpectedSampleCount: receipt.ExpectedSampleCount, SampleCount: receipt.SampleCount,
		ObjectSHA256: receipt.ObjectSHA256, ObjectCRC32C: uint32(receipt.ObjectCRC32C),
		ObjectSize: receipt.ObjectSize, ObjectGeneration: receipt.ObjectGeneration,
		ObjectMetageneration: receipt.ObjectMetageneration,
		ManifestSHA256:       receipt.ManifestSHA256, ManifestCRC32C: uint32(receipt.ManifestCRC32C),
		ManifestSize: receipt.ManifestSize, ManifestGeneration: receipt.ManifestGeneration,
		ManifestMetageneration: receipt.ManifestMetageneration,
		RejectionCode:          receipt.RejectionCode, RecoveryHoldCode: receipt.RecoveryHoldCode,
		RecoveryHoldReviewDueAt: receipt.RecoveryHoldReviewDueAt.UTC(),
		FencingToken:            receipt.FencingToken, LeaseOwnerID: receipt.LeaseOwnerID,
		LeaseOwnerKind: receipt.LeaseOwnerKind, LeaseAcquiredAt: receipt.LeaseAcquiredAt.UTC(),
		LeaseHeartbeatAt: receipt.LeaseHeartbeatAt.UTC(), LeaseExpiresAt: receipt.LeaseExpiresAt.UTC(),
		NextRecoveryAt: receipt.NextRecoveryAt.UTC(), LastRecoveryCode: receipt.LastRecoveryCode,
		ReservationDeadline: receipt.ReservationDeadline.UTC(),
		ArtifactExpiresAt:   receipt.ArtifactExpiresAt.UTC(),
		UpdatedAt:           receipt.UpdatedAt.UTC(),
	}, nil
}

func validFirestoreCRC32C(value int64) bool {
	return value >= 0 && value <= int64(^uint32(0))
}

func validFirestoreOutcomeReceiptSurface(receipt firestoreIngestReceipt) bool {
	if receipt.RejectionCode != "" && receipt.RejectionCode != "object_conflict" {
		return false
	}
	if receipt.RecoveryHoldCode != "" && !ingest.ValidRecoveryHoldCode(receipt.RecoveryHoldCode) {
		return false
	}
	if receipt.LastRecoveryCode != "" &&
		!ingest.ValidLeaseReleaseCode(ingest.LeaseReleaseCode(receipt.LastRecoveryCode)) {
		return false
	}
	if receipt.LeaseOwnerID != "" && !telemetry.IsUUID(receipt.LeaseOwnerID) {
		return false
	}
	switch receipt.LeaseOwnerKind {
	case "", ingest.LeaseOwnerRequest, ingest.LeaseOwnerSweeper, ingest.LeaseOwnerCleanup:
		return true
	default:
		return false
	}
}

func normalizeForwardRecoveryOutcomeStoreError(
	parent context.Context,
	outcomeContext context.Context,
	err error,
) error {
	if parent != nil {
		if contextErr := parent.Err(); contextErr != nil {
			return contextErr
		}
	}
	if outcomeContext != nil && errors.Is(outcomeContext.Err(), context.DeadlineExceeded) {
		return ingest.ErrForwardRecoveryOutcomeAuthorizationExpired
	}
	if errors.Is(err, ingest.ErrInvalidForwardRecoveryOutcomeAuthorization) ||
		errors.Is(err, ingest.ErrForwardRecoveryOutcomeAuthorizationExpired) ||
		errors.Is(err, ingest.ErrForwardRecoveryOutcomeUnavailable) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ingest.ErrForwardRecoveryOutcomeUnavailable
}
