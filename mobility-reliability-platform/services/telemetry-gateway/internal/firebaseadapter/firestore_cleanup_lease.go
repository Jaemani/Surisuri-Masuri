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
		if receiptHasPurgeFenceFields(receipt) {
			resultStatus = ingest.LeaseStatusNotEligible
			return nil
		}
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
		var priorAttemptPath string
		var priorAttemptUpdates []firestore.Update
		if cleanupControlCursorPresent(receipt) {
			if receipt.CleanupControlDisposition == ingest.CleanupExecutionDispositionHold {
				// Review due is informational. A hold requires a future explicit
				// operator-release contract and never auto-claims a new lease.
				resultStatus = ingest.LeaseStatusNotEligible
				return nil
			}
			if receipt.CleanupControlDisposition != ingest.CleanupExecutionDispositionRetry {
				return ingest.ErrAdmissionUnavailable
			}
			if effectiveAt.Before(receipt.NextCleanupAt) {
				resultStatus = ingest.LeaseStatusNotDue
				return nil
			}
			if owner.ID == receipt.CleanupDispositionAttemptID {
				return ingest.ErrAdmissionUnavailable
			}
			var retryErr error
			effectiveAt, retryErr = validateCleanupRetryClaimCursor(
				runContext,
				transaction,
				receipt,
				requestedAt.UTC(),
				linked.Receipt.ReadTime,
			)
			if retryErr != nil {
				return retryErr
			}
			if effectiveAt.Before(receipt.NextCleanupAt) {
				resultStatus = ingest.LeaseStatusNotDue
				return nil
			}
		} else {
			var attemptEffectiveAt time.Time
			var priorAttemptErr error
			priorAttemptPath, priorAttemptUpdates, attemptEffectiveAt, priorAttemptErr =
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
		}
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
		receipt.CleanupDispositionAttemptID = ""
		receipt.CleanupControlDisposition = ""
		receipt.LastCleanupErrorClass = ""
		receipt.NextCleanupAt = time.Time{}
		receipt.CleanupHoldReviewDueAt = time.Time{}
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
		{Path: "cleanup_disposition_attempt_id", Value: firestore.Delete},
		{Path: "cleanup_control_disposition", Value: firestore.Delete},
		{Path: "last_cleanup_error_class", Value: firestore.Delete},
		{Path: "next_cleanup_at", Value: firestore.Delete},
		{Path: "cleanup_hold_review_due_at", Value: firestore.Delete},
		{Path: "fencing_token", Value: fencingToken},
		{Path: "recovery_attempt_count", Value: recoveryAttemptCount},
		{Path: "revision", Value: revision},
		{Path: "updated_at", Value: acquiredAt.UTC()},
	}
}

func validateCleanupRetryClaimCursor(
	ctx context.Context,
	transaction admissionTransaction,
	receipt firestoreIngestReceipt,
	applicationTime time.Time,
	receiptReadTime time.Time,
) (time.Time, error) {
	reader, ok := transaction.(cleanupTargetTransaction)
	if !ok || receipt.CleanupControlDisposition != ingest.CleanupExecutionDispositionRetry ||
		receipt.CleanupDispositionAttemptID == "" || receipt.NextCleanupAt.IsZero() ||
		!receipt.CleanupHoldReviewDueAt.IsZero() || receiptHasLeaseFields(receipt) {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	attemptPath := recoveryAttemptDocumentPath(
		receipt.TenantID, receipt.ReceiptID, receipt.CleanupDispositionAttemptID,
	)
	attemptResult, exists, err := reader.ReadRecoveryAttempt(ctx, attemptPath)
	if err != nil {
		return time.Time{}, err
	}
	if !exists {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	targetResult, exists, err := reader.ReadCleanupTarget(
		ctx,
		cleanupTargetDocumentPath(receipt.TenantID, receipt.CleanupDispositionAttemptID),
	)
	if err != nil {
		return time.Time{}, err
	}
	if !exists {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	effectiveAt, err := coherentCleanupTransitionTime(
		applicationTime,
		receiptReadTime,
		attemptResult.ReadTime,
		targetResult.ReadTime,
	)
	if err != nil {
		return time.Time{}, err
	}
	target, err := targetResult.Target.toDomain()
	if err != nil {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	query := ingest.CleanupExecutionQuery{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		AttemptID: receipt.CleanupDispositionAttemptID,
	}
	plan, err := ingest.BuildDisposedCleanupExecutionLedgerPlan(
		query, receipt.toDomain(), target, attemptResult.Attempt.CompletedAt,
	)
	if err != nil {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	ledger, present, err := projectCleanupExecutionLedger(target, attemptResult.Attempt)
	if err != nil || !present ||
		attemptResult.Attempt.Status != ingest.RecoveryAttemptCompleted ||
		attemptResult.Attempt.Outcome != ingest.RecoveryAttemptOutcomeCleanupRetry ||
		attemptResult.Attempt.OwnerKind != ingest.LeaseOwnerCleanup ||
		attemptResult.Attempt.WorkerVersion != ingest.CleanupWorkerVersion ||
		attemptResult.Attempt.FailureCode != "" || !attemptResult.Attempt.FailedAt.IsZero() ||
		cleanupExpiryFinalizationAttemptHasForeignResidue(attemptResult.Attempt) ||
		ledger.Disposition != ingest.CleanupExecutionDispositionRetry ||
		ledger.ErrorClass != receipt.LastCleanupErrorClass ||
		!ledger.CompletedAt.Equal(receipt.UpdatedAt) ||
		ingest.ValidateCleanupExecutionLedger(plan, ledger, ledger.CompletedAt) != nil {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	evidenceHash, err := ingest.CleanupExecutionDispositionEvidenceHash(plan, ledger)
	nextCleanupAt, holdReviewDueAt, cursorErr := ingest.CleanupExecutionDispositionCursorAt(
		ledger.Fence, ledger.ErrorClass, ledger.CompletedAt,
	)
	if err != nil || cursorErr != nil || evidenceHash != ledger.EvidenceHash ||
		!nextCleanupAt.Equal(receipt.NextCleanupAt) || !holdReviewDueAt.IsZero() ||
		effectiveAt.Before(nextCleanupAt) || effectiveAt.Before(ledger.Fence.ExpiresAt) {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	return effectiveAt, nil
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
		if hasCleanupExecutionLedgerResidue(attemptResult.Attempt) {
			return expiredProgressingCleanupAttemptClosure(
				ctx,
				transaction,
				receipt,
				attemptPath,
				attemptResult,
				applicationTime,
				receiptReadTime,
			)
		}
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

func expiredProgressingCleanupAttemptClosure(
	ctx context.Context,
	transaction admissionTransaction,
	receipt firestoreIngestReceipt,
	attemptPath string,
	attemptResult recoveryAttemptRead,
	applicationTime time.Time,
	receiptReadTime time.Time,
) (string, []firestore.Update, time.Time, error) {
	reader, ok := transaction.(cleanupTargetTransaction)
	if !ok || validateCleanupExecutionAttemptIdentity(
		attemptResult.Attempt, receipt, attemptResult.Attempt.AttemptID,
	) != nil {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	targetResult, exists, err := reader.ReadCleanupTarget(
		ctx,
		cleanupTargetDocumentPath(receipt.TenantID, attemptResult.Attempt.AttemptID),
	)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if !exists {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	effectiveAt, err := coherentCleanupTransitionTime(
		applicationTime,
		receiptReadTime,
		attemptResult.ReadTime,
		targetResult.ReadTime,
	)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	if effectiveAt.Before(receipt.LeaseExpiresAt) {
		return "", nil, effectiveAt, nil
	}
	readTime, err := coherentCleanupTargetReadTime(
		receiptReadTime,
		attemptResult.ReadTime,
		targetResult.ReadTime,
	)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	target, err := targetResult.Target.toDomain()
	if err != nil {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	query := ingest.CleanupExecutionQuery{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		AttemptID: attemptResult.Attempt.AttemptID,
	}
	plan, err := ingest.BuildExpiredCleanupExecutionLedgerPlan(
		query,
		ingest.CurrentCleanupExecutionSnapshot{
			Receipt: receipt.toDomain(), Attempt: currentCleanupAttempt(attemptResult.Attempt),
			Target: target, ReadTime: readTime,
		},
		applicationTime,
	)
	if err != nil {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	ledger, present, err := decodeHistoricalCleanupExecutionLedger(plan, attemptResult.Attempt)
	if err != nil || !present || ledger.Phase == ingest.CleanupExecutionPhaseCompleted ||
		ledger.Disposition != "" || ledger.EvidenceHash != "" ||
		!ledger.CompletedAt.IsZero() {
		return "", nil, time.Time{}, ingest.ErrAdmissionUnavailable
	}
	return attemptPath, []firestore.Update{
		{Path: "status", Value: string(ingest.RecoveryAttemptFailed)},
		{Path: "failure_code", Value: string(ingest.RecoveryAttemptFailureLeaseExpired)},
		{Path: "failed_at", Value: effectiveAt.UTC()},
	}, effectiveAt, nil
}
