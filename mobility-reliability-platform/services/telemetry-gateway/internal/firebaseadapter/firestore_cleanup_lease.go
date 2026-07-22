package firebaseadapter

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var _ ingest.CleanupLeaseStore = (*FirestoreAdmissionStore)(nil)

func (s *FirestoreAdmissionStore) ClaimCleanupLease(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	owner ingest.LeaseOwner,
	attemptProposal ingest.CleanupAttemptProposal,
	requestedAt time.Time,
	duration time.Duration,
) (ingest.CleanupLeaseGrant, ingest.LeaseStatus, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(tenantID) || !lowerHexDigest(reservationKey) || requestedAt.IsZero() ||
		owner.Kind != ingest.LeaseOwnerCleanup || owner.ID != attemptProposal.ID ||
		ingest.ValidateCleanupAttemptProposal(attemptProposal) != nil ||
		duration < ingest.MinLeaseDuration || duration > ingest.MaxLeaseDuration ||
		ingest.ValidateCleanupTimingPolicy() != nil {
		return ingest.CleanupLeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}
	var resultGrant ingest.CleanupLeaseGrant
	var resultStatus ingest.LeaseStatus
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		resultGrant = ingest.CleanupLeaseGrant{}
		resultStatus = ""
		linked, loadErr := loadLinkedReceipt(runContext, transaction, tenantID, reservationKey)
		if loadErr != nil {
			return loadErr
		}
		receipt := linked.Receipt.Receipt
		if receipt.State != ingest.ReceiptCleanupPending ||
			receipt.CleanupMode != ingest.CleanupModeReservationExpiry ||
			receipt.CleanupOriginStatus != ingest.ReceiptReserved {
			resultStatus = ingest.LeaseStatusNotEligible
			return nil
		}
		effectiveAt, clockErr := coherentCleanupTransitionTime(requestedAt.UTC(), linked.Receipt.ReadTime)
		if clockErr != nil || effectiveAt.Before(receipt.UpdatedAt) {
			return ingest.ErrAdmissionUnavailable
		}
		if effectiveAt.Before(receipt.CleanupQuiescenceUntil) {
			resultStatus = ingest.LeaseStatusNotDue
			return nil
		}
		if receiptHasLeaseFields(receipt) && effectiveAt.Before(receipt.LeaseExpiresAt) {
			resultStatus = ingest.LeaseStatusHeld
			return nil
		}
		priorAttemptPath, priorAttemptUpdates, attemptEffectiveAt, priorAttemptErr :=
			expiredPriorCleanupAttemptClosure(
				runContext,
				transaction,
				receipt,
				requestedAt.UTC(),
				linked.Receipt.ReadTime,
			)
		if priorAttemptErr != nil {
			return priorAttemptErr
		}
		effectiveAt = attemptEffectiveAt
		if effectiveAt.Before(receipt.UpdatedAt) {
			return ingest.ErrAdmissionUnavailable
		}
		if effectiveAt.Before(receipt.CleanupQuiescenceUntil) {
			resultStatus = ingest.LeaseStatusNotDue
			return nil
		}
		if receiptHasLeaseFields(receipt) && effectiveAt.Before(receipt.LeaseExpiresAt) {
			resultStatus = ingest.LeaseStatusHeld
			return nil
		}
		nextToken := receipt.FencingToken + 1
		nextRevision := receipt.Revision + 1
		nextAttemptCount := receipt.RecoveryAttemptCount + 1
		leaseExpiresAt := effectiveAt.Add(duration)
		if nextToken <= receipt.FencingToken || nextRevision <= receipt.Revision ||
			nextAttemptCount <= receipt.RecoveryAttemptCount ||
			!leaseExpiresAt.After(effectiveAt) {
			return ingest.ErrAdmissionUnavailable
		}
		if len(priorAttemptUpdates) > 0 {
			if updateErr := transaction.Update(runContext, priorAttemptPath, priorAttemptUpdates); updateErr != nil {
				return normalizeAdmissionError(runContext, updateErr)
			}
		}
		if updateErr := transaction.Update(runContext, linked.ReceiptPath, cleanupLeaseUpdates(
			owner,
			effectiveAt,
			leaseExpiresAt,
			nextToken,
			nextRevision,
			nextAttemptCount,
		)); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		attempt := newFirestoreCleanupAttempt(
			attemptProposal,
			receipt.TenantID,
			receipt.ReceiptID,
			nextToken,
			effectiveAt,
		)
		if createErr := transaction.Create(
			runContext,
			recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, attempt.AttemptID),
			attempt,
		); createErr != nil {
			return normalizeAdmissionError(runContext, createErr)
		}
		receipt.LeaseOwnerID = owner.ID
		receipt.LeaseOwnerKind = owner.Kind
		receipt.LeaseAcquiredAt = effectiveAt
		receipt.LeaseHeartbeatAt = effectiveAt
		receipt.LeaseExpiresAt = leaseExpiresAt
		receipt.NextRecoveryAt = time.Time{}
		receipt.LastRecoveryCode = ""
		receipt.FencingToken = nextToken
		receipt.Revision = nextRevision
		receipt.RecoveryAttemptCount = nextAttemptCount
		receipt.UpdatedAt = effectiveAt
		if validateReceiptState(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		resultGrant = cleanupLeaseGrant(receipt)
		if ingest.ValidateCleanupLeaseGrant(resultGrant) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		resultStatus = ingest.LeaseStatusAcquired
		return nil
	})
	if err != nil {
		return ingest.CleanupLeaseGrant{}, "", normalizeAdmissionError(ctx, err)
	}
	if resultStatus == "" {
		return ingest.CleanupLeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}
	return resultGrant, resultStatus, nil
}

func cleanupLeaseUpdates(
	owner ingest.LeaseOwner,
	acquiredAt time.Time,
	expiresAt time.Time,
	fencingToken int64,
	revision int64,
	recoveryAttemptCount int64,
) []firestore.Update {
	return []firestore.Update{
		{Path: "lease_owner_id", Value: owner.ID},
		{Path: "lease_owner_kind", Value: string(owner.Kind)},
		{Path: "lease_acquired_at", Value: acquiredAt.UTC()},
		{Path: "lease_heartbeat_at", Value: acquiredAt.UTC()},
		{Path: "lease_expires_at", Value: expiresAt.UTC()},
		{Path: "next_recovery_at", Value: firestore.Delete},
		{Path: "last_recovery_code", Value: firestore.Delete},
		{Path: "fencing_token", Value: fencingToken},
		{Path: "recovery_attempt_count", Value: recoveryAttemptCount},
		{Path: "revision", Value: revision},
		{Path: "updated_at", Value: acquiredAt.UTC()},
	}
}

func newFirestoreCleanupAttempt(
	proposal ingest.CleanupAttemptProposal,
	tenantID string,
	receiptID string,
	fencingToken int64,
	startedAt time.Time,
) firestoreRecoveryAttempt {
	return firestoreRecoveryAttempt{
		AttemptID:     proposal.ID,
		TenantID:      tenantID,
		ReceiptID:     receiptID,
		OwnerKind:     ingest.LeaseOwnerCleanup,
		FencingToken:  fencingToken,
		WorkerVersion: proposal.WorkerVersion,
		Status:        ingest.RecoveryAttemptStarted,
		StartedAt:     startedAt.UTC(),
	}
}

func cleanupLeaseGrant(receipt firestoreIngestReceipt) ingest.CleanupLeaseGrant {
	return ingest.CleanupLeaseGrant{
		Lease:           receipt.leaseGrant(),
		ReceiptRevision: receipt.Revision,
		Mode:            receipt.CleanupMode,
		OriginStatus:    receipt.CleanupOriginStatus,
		PolicyVersion:   receipt.CleanupPolicyVersion,
		TransitionedAt:  receipt.CleanupTransitionedAt,
		QuiescenceUntil: receipt.CleanupQuiescenceUntil,
	}
}

func expiredPriorCleanupAttemptClosure(
	ctx context.Context,
	transaction admissionTransaction,
	receipt firestoreIngestReceipt,
	applicationTime time.Time,
	receiptReadTime time.Time,
) (string, []firestore.Update, time.Time, error) {
	effectiveAt, err := coherentCleanupTransitionTime(applicationTime, receiptReadTime)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if !receiptHasLeaseFields(receipt) {
		return "", nil, effectiveAt, nil
	}
	if receipt.LeaseOwnerKind != ingest.LeaseOwnerCleanup || receipt.RecoveryAttemptCount <= 0 {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	reader, ok := transaction.(recoveryAttemptReaderTransaction)
	if !ok {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, receipt.LeaseOwnerID)
	attemptResult, exists, readErr := reader.ReadRecoveryAttempt(ctx, attemptPath)
	if readErr != nil {
		return "", nil, time.Time{}, readErr
	}
	if !exists {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	effectiveAt, err = coherentCleanupTransitionTime(
		applicationTime,
		receiptReadTime,
		attemptResult.ReadTime,
	)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if effectiveAt.Before(receipt.LeaseExpiresAt) {
		return "", nil, effectiveAt, nil
	}
	expected := ingest.RecoveryAttemptProposal{
		ID: receipt.LeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
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
			ingest.LeaseOwnerCleanup,
		) != nil || !effectiveAt.After(attemptResult.Attempt.StartedAt) {
			return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
		}
		return attemptPath, []firestore.Update{
			{Path: "status", Value: string(ingest.RecoveryAttemptFailed)},
			{Path: "failure_code", Value: string(ingest.RecoveryAttemptFailureLeaseExpired)},
			{Path: "failed_at", Value: effectiveAt.UTC()},
		}, effectiveAt, nil
	case ingest.RecoveryAttemptFailed:
		// R8a has exactly one cleanup-attempt terminal path: takeover closes an
		// expired lease. Do not import forward-worker failure semantics before a
		// cleanup executor defines and writes its own bounded failure contract.
		if attemptResult.Attempt.FailureCode != ingest.RecoveryAttemptFailureLeaseExpired ||
			validateFailedRecoveryAttemptForOwner(
				attemptResult.Attempt,
				receipt,
				expected,
				fence,
				ingest.LeaseOwnerCleanup,
				effectiveAt,
			) != nil {
			return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
		}
		return "", nil, effectiveAt, nil
	default:
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
}
