package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

var _ ingest.CleanupExpiryFinalizationStore = (*FirestoreAdmissionStore)(nil)
var _ ingest.CleanupExpiryFinalizationOutcomeAuthorizationStore = (*FirestoreAdmissionStore)(nil)
var _ ingest.CleanupExpiryFinalizationOutcomeStore = (*FirestoreAdmissionStore)(nil)

func (s *FirestoreAdmissionStore) FinalizeExpiredCleanup(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
) (ingest.CleanupExpiryFinalizationResult, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionQuery(query) != nil {
		return ingest.CleanupExpiryFinalizationResult{}, ingest.ErrInvalidCleanupExpiryFinalization
	}
	if err := ctx.Err(); err != nil {
		return ingest.CleanupExpiryFinalizationResult{}, err
	}
	deadline, err := s.cleanupExecutionLedgerDeadline(ctx, query)
	if err != nil {
		return ingest.CleanupExpiryFinalizationResult{},
			normalizeCleanupExpiryFinalizationError(ctx, err)
	}
	if !s.now().UTC().Before(deadline) {
		return ingest.CleanupExpiryFinalizationResult{},
			ingest.ErrCleanupExecutionUnauthorized
	}
	contextFactory := s.cleanupExpiryFinalizationContext
	if contextFactory == nil {
		contextFactory = context.WithDeadline
	}
	operationContext, cancel := contextFactory(ctx, deadline)
	defer cancel()
	result, err := s.finalizeExpiredCleanup(operationContext, query)
	if err != nil {
		// A non-zero OutcomeQuery is deliberately preserved on error. It was
		// built after all reads and validation but before writes, so a caller can
		// correlate a Firestore commit response loss without repeating mutation.
		return result, normalizeCleanupExpiryFinalizationOperationError(
			ctx, operationContext, err,
		)
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) finalizeExpiredCleanup(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
) (ingest.CleanupExpiryFinalizationResult, error) {
	var result ingest.CleanupExpiryFinalizationResult
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
		}
		current, present, decodeErr := decodeCleanupExecutionLedger(
			state.plan, state.attempt, state.effectiveAt,
		)
		if decodeErr != nil || !present {
			return ingest.ErrCleanupExpiryFinalizationConflict
		}
		completed, purgeEligibleAt, outcomeQuery, completionErr := ingest.CompleteCleanupExecution(
			state.plan,
			current,
			state.linked.Receipt.Receipt.ReceiptRetentionFloor,
			state.effectiveAt,
		)
		if completionErr != nil {
			return completionErr
		}

		receipt := state.linked.Receipt.Receipt
		nextRevision := receipt.Revision + 1
		if nextRevision <= receipt.Revision || nextRevision != outcomeQuery.ExpectedFinalReceiptRevision {
			return ingest.ErrCleanupExpiryFinalizationUnavailable
		}
		idempotencyIndex := state.linked.Index
		clientBatchIndex := state.linked.Index
		idempotencyIndex.PurgeEligibleAt = cloneOptionalTime(&purgeEligibleAt)
		clientBatchIndex.PurgeEligibleAt = cloneOptionalTime(&purgeEligibleAt)
		receipt.State = ingest.ReceiptExpired
		receipt.Revision = nextRevision
		receipt.UpdatedAt = state.effectiveAt.UTC()
		receipt.PurgeEligibleAt = cloneOptionalTime(&purgeEligibleAt)
		receipt.clearLease()

		if validateIndexDocument(idempotencyIndex, query.TenantID) != nil ||
			validateIndexDocument(clientBatchIndex, query.TenantID) != nil ||
			!sameIngestIndex(idempotencyIndex, clientBatchIndex) ||
			validateReceiptLinkage(receipt, idempotencyIndex) != nil ||
			validateReceiptState(receipt) != nil {
			return ingest.ErrCleanupExpiryFinalizationUnavailable
		}
		attemptUpdates, encodeErr := cleanupExecutionLedgerUpdates(
			state.plan, completed, state.effectiveAt,
		)
		if encodeErr != nil {
			return encodeErr
		}
		attemptUpdates = append(attemptUpdates,
			firestore.Update{Path: "status", Value: string(ingest.RecoveryAttemptCompleted)},
			firestore.Update{Path: "outcome", Value: string(ingest.RecoveryAttemptOutcomeExpired)},
		)
		receiptUpdates := []firestore.Update{
			{Path: "status", Value: string(ingest.ReceiptExpired)},
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: state.effectiveAt.UTC()},
			{Path: "purge_eligible_at", Value: purgeEligibleAt.UTC()},
			{Path: "lease_owner_id", Value: firestore.Delete},
			{Path: "lease_owner_kind", Value: firestore.Delete},
			{Path: "lease_acquired_at", Value: firestore.Delete},
			{Path: "lease_heartbeat_at", Value: firestore.Delete},
			{Path: "lease_expires_at", Value: firestore.Delete},
			{Path: "next_recovery_at", Value: firestore.Delete},
			{Path: "last_recovery_code", Value: firestore.Delete},
		}
		indexUpdates := []firestore.Update{{Path: "purge_eligible_at", Value: purgeEligibleAt.UTC()}}
		idempotencyPath := idempotencyDocumentPath(query.TenantID, query.ReservationKey)
		clientBatchPath := clientBatchDocumentPath(query.TenantID, state.linked.Index.ClientBatchKey)
		result = ingest.CleanupExpiryFinalizationResult{
			Receipt: receipt.toDomain(), Ledger: completed, OutcomeQuery: outcomeQuery,
		}

		// Every authoritative read and pure validation finishes before the four
		// writes. The immutable cleanup target remains read-only.
		if updateErr := transaction.Update(runContext, state.attemptPath, attemptUpdates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		if updateErr := transaction.Update(runContext, state.linked.ReceiptPath, receiptUpdates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		if updateErr := transaction.Update(runContext, idempotencyPath, indexUpdates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		if updateErr := transaction.Update(runContext, clientBatchPath, indexUpdates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if result.Receipt.ReceiptID == "" ||
		ingest.ValidateCleanupExpiryFinalizationOutcomeQuery(result.OutcomeQuery) != nil {
		return ingest.CleanupExpiryFinalizationResult{}, ingest.ErrCleanupExpiryFinalizationUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) LoadCurrentCleanupExpiryFinalizationOutcome(
	ctx context.Context,
	query ingest.CleanupExpiryFinalizationOutcomeQuery,
) (ingest.CurrentCleanupExpiryFinalizationSnapshot, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		ingest.ValidateCleanupExpiryFinalizationOutcomeQuery(query) != nil {
		return ingest.CurrentCleanupExpiryFinalizationSnapshot{},
			ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable
	}
	var result ingest.CurrentCleanupExpiryFinalizationSnapshot
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.CurrentCleanupExpiryFinalizationSnapshot{}
		linked, loadErr := loadLinkedReceipt(
			runContext, transaction, query.TenantID, query.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}
		reader, ok := transaction.(cleanupTargetTransaction)
		if !ok {
			return ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable
		}
		attemptResult, exists, attemptErr := reader.ReadRecoveryAttempt(
			runContext,
			recoveryAttemptDocumentPath(query.TenantID, linked.Receipt.Receipt.ReceiptID, query.AttemptID),
		)
		if attemptErr != nil {
			return attemptErr
		}
		if !exists {
			return ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable
		}
		targetResult, exists, targetErr := reader.ReadCleanupTarget(
			runContext, cleanupTargetDocumentPath(query.TenantID, query.AttemptID),
		)
		if targetErr != nil {
			return targetErr
		}
		if !exists {
			return ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable
		}
		target, targetErr := targetResult.Target.toDomain()
		if targetErr != nil {
			return ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable
		}
		readTime, clockErr := coherentCleanupTargetReadTime(
			linked.Receipt.ReadTime, attemptResult.ReadTime, targetResult.ReadTime,
		)
		if clockErr != nil {
			return clockErr
		}
		plan, planValid := cleanupExpiryFinalizationCorrelationPlan(
			query, linked.Receipt.Receipt, attemptResult.Attempt, target, readTime,
		)
		ledger, present, projectErr := projectCleanupExecutionLedger(target, attemptResult.Attempt)
		if projectErr != nil || !present {
			planValid = false
			ledger = ingest.CleanupExecutionLedger{}
		}
		result = ingest.CurrentCleanupExpiryFinalizationSnapshot{
			Receipt: linked.Receipt.Receipt.toDomain(),
			Attempt: currentCleanupExpiryFinalizationAttempt(attemptResult.Attempt, ledger),
			Plan:    plan, PlanValid: planValid, ReadTime: readTime,
		}
		return nil
	})
	if err != nil {
		return ingest.CurrentCleanupExpiryFinalizationSnapshot{},
			normalizeCleanupExpiryFinalizationOutcomeError(ctx, nil, err)
	}
	if result.Receipt.ReceiptID == "" || result.Attempt.AttemptID == "" || result.ReadTime.IsZero() {
		return ingest.CurrentCleanupExpiryFinalizationSnapshot{},
			ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable
	}
	if _, err := ingest.EvaluateCleanupExpiryFinalizationOutcome(query, result, result.ReadTime); err != nil {
		return ingest.CurrentCleanupExpiryFinalizationSnapshot{}, err
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) GetCleanupExpiryFinalizationOutcome(
	ctx context.Context,
	grant ingest.CleanupExpiryFinalizationOutcomeReadGrant,
	query ingest.CleanupExpiryFinalizationOutcomeQuery,
	observedAt time.Time,
) (ingest.CleanupExpiryFinalizationOutcome, error) {
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() {
		return ingest.CleanupExpiryFinalizationOutcome{},
			ingest.ErrInvalidCleanupExpiryFinalizationOutcome
	}
	observedAt = observedAt.UTC()
	if err := ingest.ValidateCleanupExpiryFinalizationOutcomeAuthorization(
		grant, query, observedAt,
	); err != nil {
		return ingest.CleanupExpiryFinalizationOutcome{}, err
	}
	deadline, err := ingest.CleanupExpiryFinalizationOutcomeAuthorizationDeadline(grant, query)
	if err != nil {
		return ingest.CleanupExpiryFinalizationOutcome{}, err
	}
	operationContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	snapshot, err := s.LoadCurrentCleanupExpiryFinalizationOutcome(operationContext, query)
	if err != nil {
		return ingest.CleanupExpiryFinalizationOutcome{},
			normalizeCleanupExpiryFinalizationOutcomeError(ctx, operationContext, err)
	}
	effectiveAt, err := conservativeAcceptanceTime(observedAt, snapshot.ReadTime)
	if err != nil {
		return ingest.CleanupExpiryFinalizationOutcome{},
			ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable
	}
	if err := ingest.ValidateCleanupExpiryFinalizationOutcomeAuthorization(
		grant, query, effectiveAt,
	); err != nil {
		return ingest.CleanupExpiryFinalizationOutcome{}, err
	}
	result, err := ingest.EvaluateCleanupExpiryFinalizationOutcome(query, snapshot, effectiveAt)
	if err != nil {
		return ingest.CleanupExpiryFinalizationOutcome{},
			normalizeCleanupExpiryFinalizationOutcomeError(ctx, operationContext, err)
	}
	return result, nil
}

func cleanupExpiryFinalizationCorrelationPlan(
	query ingest.CleanupExpiryFinalizationOutcomeQuery,
	receipt firestoreIngestReceipt,
	attempt firestoreRecoveryAttempt,
	target ingest.CleanupTarget,
	readTime time.Time,
) (ingest.CleanupExecutionLedgerPlan, bool) {
	executionQuery := ingest.CleanupExecutionQuery{
		TenantID: query.TenantID, ReservationKey: query.ReservationKey, AttemptID: query.AttemptID,
	}
	if receipt.State == ingest.ReceiptExpired && attempt.Status == ingest.RecoveryAttemptCompleted {
		plan, err := ingest.BuildCompletedCleanupExecutionLedgerPlan(
			executionQuery, receipt.toDomain(), target, attempt.CompletedAt,
		)
		return plan, err == nil
	}
	snapshot := ingest.CurrentCleanupExecutionSnapshot{
		Receipt: receipt.toDomain(), Attempt: currentCleanupAttempt(attempt),
		Target: target, ReadTime: readTime,
	}
	var plan ingest.CleanupExecutionLedgerPlan
	var err error
	if readTime.Before(target.Command.LeaseExpiresAt) {
		plan, err = ingest.BuildCleanupExecutionLedgerPlan(executionQuery, snapshot, readTime)
	} else {
		plan, err = ingest.BuildExpiredCleanupExecutionLedgerPlan(executionQuery, snapshot, readTime)
	}
	return plan, err == nil
}

func currentCleanupExpiryFinalizationAttempt(
	attempt firestoreRecoveryAttempt,
	ledger ingest.CleanupExecutionLedger,
) ingest.CurrentCleanupExpiryFinalizationAttempt {
	return ingest.CurrentCleanupExpiryFinalizationAttempt{
		AttemptID: attempt.AttemptID, TenantID: attempt.TenantID, ReceiptID: attempt.ReceiptID,
		OwnerKind: attempt.OwnerKind, FencingToken: attempt.FencingToken,
		WorkerVersion: attempt.WorkerVersion, Status: attempt.Status, Outcome: attempt.Outcome,
		StartedAt: attempt.StartedAt.UTC(), CompletedAt: attempt.CompletedAt.UTC(),
		FailureCode: attempt.FailureCode, FailedAt: attempt.FailedAt.UTC(),
		ForeignResidue: cleanupExpiryFinalizationAttemptHasForeignResidue(attempt),
		Ledger:         ledger,
	}
}

func cleanupExpiryFinalizationAttemptHasForeignResidue(attempt firestoreRecoveryAttempt) bool {
	return attempt.AuthorizationDisposition != "" || attempt.Phase != "" ||
		attempt.Classification != "" || attempt.ReasonCode != "" || attempt.Action != "" ||
		attempt.ActionHash != "" || attempt.HoldCode != "" || attempt.ReleaseCode != "" ||
		attempt.RejectionCode != "" || attempt.RawSHA256 != "" || attempt.RawCRC32C != 0 ||
		attempt.RawSize != 0 || attempt.RawGeneration != 0 || attempt.RawMetageneration != 0 ||
		attempt.ManifestSHA256 != "" || attempt.ManifestCRC32C != 0 ||
		attempt.ManifestSize != 0 || attempt.ManifestGeneration != 0 ||
		attempt.ManifestMetageneration != 0 || !attempt.HoldReviewDueAt.IsZero()
}

func normalizeCleanupExpiryFinalizationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	for _, bounded := range []error{
		ingest.ErrInvalidCleanupExpiryFinalization,
		ingest.ErrCleanupExpiryFinalizationConflict,
		ingest.ErrCleanupExpiryFinalizationUnavailable,
		ingest.ErrInvalidCleanupExecutionLedger,
		ingest.ErrCleanupExecutionConflict,
		ingest.ErrCleanupExecutionUnauthorized,
	} {
		if errors.Is(err, bounded) {
			return bounded
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return ingest.ErrCleanupExpiryFinalizationUnavailable
}

func normalizeCleanupExpiryFinalizationOperationError(
	parent context.Context,
	operation context.Context,
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
	if operation != nil && errors.Is(operation.Err(), context.DeadlineExceeded) {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	return normalizeCleanupExpiryFinalizationError(parent, err)
}

func normalizeCleanupExpiryFinalizationOutcomeError(
	parent context.Context,
	operation context.Context,
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
	if operation != nil && errors.Is(operation.Err(), context.DeadlineExceeded) {
		return ingest.ErrCleanupExpiryFinalizationOutcomeExpired
	}
	for _, bounded := range []error{
		ingest.ErrInvalidCleanupExpiryFinalizationOutcome,
		ingest.ErrCleanupExpiryFinalizationOutcomeExpired,
		ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable,
	} {
		if errors.Is(err, bounded) {
			return bounded
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable
}
