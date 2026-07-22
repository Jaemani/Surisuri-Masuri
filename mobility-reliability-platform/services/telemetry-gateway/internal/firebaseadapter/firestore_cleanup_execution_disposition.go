package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

var _ ingest.CleanupExecutionDispositionStore = (*FirestoreAdmissionStore)(nil)
var _ ingest.CleanupExecutionDispositionOutcomeAuthorizationStore = (*FirestoreAdmissionStore)(nil)
var _ ingest.CleanupExecutionDispositionOutcomeStore = (*FirestoreAdmissionStore)(nil)

func (s *FirestoreAdmissionStore) DisposeCleanupExecution(
	ctx context.Context,
	command ingest.CleanupExecutionDispositionCommand,
) (ingest.CleanupExecutionDispositionResult, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionDispositionCommand(command) != nil {
		return ingest.CleanupExecutionDispositionResult{},
			ingest.ErrInvalidCleanupExecutionDisposition
	}
	if err := ctx.Err(); err != nil {
		return ingest.CleanupExecutionDispositionResult{}, err
	}
	deadline, err := s.cleanupExecutionLedgerDeadline(ctx, command.Query)
	if err != nil {
		return ingest.CleanupExecutionDispositionResult{},
			normalizeCleanupExecutionDispositionError(ctx, err)
	}
	if !s.now().UTC().Before(deadline) {
		return ingest.CleanupExecutionDispositionResult{},
			ingest.ErrCleanupExecutionUnauthorized
	}
	contextFactory := s.cleanupExecutionDispositionContext
	if contextFactory == nil {
		contextFactory = context.WithDeadline
	}
	operationContext, cancel := contextFactory(ctx, deadline)
	defer cancel()
	result, err := s.disposeCleanupExecution(operationContext, command)
	if err != nil {
		// Preserve a sealed pre-state query when the commit response is lost.
		return result, normalizeCleanupExecutionDispositionOperationError(
			ctx, operationContext, err,
		)
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) disposeCleanupExecution(
	ctx context.Context,
	command ingest.CleanupExecutionDispositionCommand,
) (ingest.CleanupExecutionDispositionResult, error) {
	var result ingest.CleanupExecutionDispositionResult
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, command.Query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
		}
		if state.plan.Target.TargetHash != command.ExpectedTargetHash ||
			state.plan.PlanHash != command.ExpectedPlanHash ||
			state.plan.Target.Command.ReceiptRevision != command.ExpectedReceiptRevision {
			return ingest.ErrCleanupExecutionDispositionConflict
		}
		current, present, decodeErr := decodeCleanupExecutionLedger(
			state.plan, state.attempt, state.effectiveAt,
		)
		if decodeErr != nil || !present || current.Revision != command.ExpectedLedgerRevision ||
			current.Phase != command.ExpectedPhase {
			return ingest.ErrCleanupExecutionDispositionConflict
		}
		terminal, policy, completionErr := ingest.CompleteCleanupExecutionDisposition(
			state.plan, current, command.ErrorClass, state.effectiveAt,
		)
		if completionErr != nil {
			return completionErr
		}
		nextCleanupAt, holdReviewDueAt, cursorErr := ingest.CleanupExecutionDispositionCursorAt(
			terminal.Fence, terminal.ErrorClass, terminal.CompletedAt,
		)
		if cursorErr != nil {
			return cursorErr
		}
		outcomeQuery, queryErr := ingest.CleanupExecutionDispositionOutcomeQueryForLedger(
			state.plan, current, command.ErrorClass,
		)
		if queryErr != nil || outcomeQuery.ExpectedDisposition != policy.Disposition {
			return ingest.ErrCleanupExecutionDispositionUnavailable
		}

		receipt := state.linked.Receipt.Receipt
		nextRevision := receipt.Revision + 1
		if nextRevision <= receipt.Revision ||
			nextRevision != outcomeQuery.ExpectedFinalReceiptRevision {
			return ingest.ErrCleanupExecutionDispositionUnavailable
		}
		receipt.Revision = nextRevision
		receipt.UpdatedAt = terminal.CompletedAt
		receipt.clearLease()
		receipt.NextRecoveryAt = time.Time{}
		receipt.LastRecoveryCode = ""
		receipt.CleanupDispositionAttemptID = command.Query.AttemptID
		receipt.CleanupControlDisposition = terminal.Disposition
		receipt.LastCleanupErrorClass = terminal.ErrorClass
		receipt.NextCleanupAt = nextCleanupAt
		receipt.CleanupHoldReviewDueAt = holdReviewDueAt
		if validateReceiptLinkage(receipt, state.linked.Index) != nil ||
			validateReceiptState(receipt) != nil {
			return ingest.ErrCleanupExecutionDispositionUnavailable
		}

		attemptUpdates, encodeErr := cleanupExecutionLedgerUpdates(
			state.plan, terminal, terminal.CompletedAt,
		)
		if encodeErr != nil {
			return encodeErr
		}
		attemptUpdates = append(attemptUpdates,
			firestore.Update{Path: "status", Value: string(ingest.RecoveryAttemptCompleted)},
			firestore.Update{Path: "outcome", Value: string(cleanupExecutionDispositionAttemptOutcome(policy.Disposition))},
		)
		receiptUpdates := []firestore.Update{
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: terminal.CompletedAt.UTC()},
			{Path: "cleanup_disposition_attempt_id", Value: command.Query.AttemptID},
			{Path: "cleanup_control_disposition", Value: string(terminal.Disposition)},
			{Path: "last_cleanup_error_class", Value: string(terminal.ErrorClass)},
			{Path: "lease_owner_id", Value: firestore.Delete},
			{Path: "lease_owner_kind", Value: firestore.Delete},
			{Path: "lease_acquired_at", Value: firestore.Delete},
			{Path: "lease_heartbeat_at", Value: firestore.Delete},
			{Path: "lease_expires_at", Value: firestore.Delete},
			{Path: "next_recovery_at", Value: firestore.Delete},
			{Path: "last_recovery_code", Value: firestore.Delete},
		}
		if terminal.Disposition == ingest.CleanupExecutionDispositionRetry {
			receiptUpdates = append(receiptUpdates,
				firestore.Update{Path: "next_cleanup_at", Value: nextCleanupAt.UTC()},
				firestore.Update{Path: "cleanup_hold_review_due_at", Value: firestore.Delete},
			)
		} else {
			receiptUpdates = append(receiptUpdates,
				firestore.Update{Path: "next_cleanup_at", Value: firestore.Delete},
				firestore.Update{Path: "cleanup_hold_review_due_at", Value: holdReviewDueAt.UTC()},
			)
		}
		result = ingest.CleanupExecutionDispositionResult{
			Receipt: receipt.toDomain(), Ledger: terminal,
			NextCleanupAt: nextCleanupAt, HoldReviewDueAt: holdReviewDueAt,
			OutcomeQuery: outcomeQuery,
		}

		// The immutable target and both uniqueness indexes are read-only. The
		// terminal attempt and receipt cursor are one atomic two-document commit.
		if updateErr := transaction.Update(runContext, state.attemptPath, attemptUpdates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		if updateErr := transaction.Update(
			runContext, state.linked.ReceiptPath, receiptUpdates,
		); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if result.Receipt.ReceiptID == "" ||
		ingest.ValidateCleanupExecutionDispositionOutcomeQuery(result.OutcomeQuery) != nil {
		return ingest.CleanupExecutionDispositionResult{},
			ingest.ErrCleanupExecutionDispositionUnavailable
	}
	return result, nil
}

func cleanupExecutionDispositionAttemptOutcome(
	disposition ingest.CleanupExecutionDisposition,
) ingest.RecoveryAttemptOutcome {
	if disposition == ingest.CleanupExecutionDispositionRetry {
		return ingest.RecoveryAttemptOutcomeCleanupRetry
	}
	if disposition == ingest.CleanupExecutionDispositionHold {
		return ingest.RecoveryAttemptOutcomeCleanupHold
	}
	return ""
}

func (s *FirestoreAdmissionStore) LoadCurrentCleanupExecutionDispositionOutcome(
	ctx context.Context,
	query ingest.CleanupExecutionDispositionOutcomeQuery,
) (ingest.CurrentCleanupExecutionDispositionSnapshot, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionDispositionOutcomeQuery(query) != nil {
		return ingest.CurrentCleanupExecutionDispositionSnapshot{},
			ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
	}
	var result ingest.CurrentCleanupExecutionDispositionSnapshot
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.CurrentCleanupExecutionDispositionSnapshot{}
		linked, loadErr := loadLinkedReceipt(
			runContext, transaction, query.TenantID, query.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}
		reader, ok := transaction.(cleanupTargetTransaction)
		if !ok {
			return ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
		}
		attemptResult, exists, attemptErr := reader.ReadRecoveryAttempt(
			runContext,
			recoveryAttemptDocumentPath(
				query.TenantID, linked.Receipt.Receipt.ReceiptID, query.AttemptID,
			),
		)
		if attemptErr != nil {
			return attemptErr
		}
		if !exists {
			return ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
		}
		targetResult, exists, targetErr := reader.ReadCleanupTarget(
			runContext, cleanupTargetDocumentPath(query.TenantID, query.AttemptID),
		)
		if targetErr != nil {
			return targetErr
		}
		if !exists {
			return ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
		}
		target, targetErr := targetResult.Target.toDomain()
		if targetErr != nil {
			return ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
		}
		readTime, clockErr := coherentCleanupTargetReadTime(
			linked.Receipt.ReadTime, attemptResult.ReadTime, targetResult.ReadTime,
		)
		if clockErr != nil {
			return clockErr
		}
		plan, planValid := cleanupExecutionDispositionCorrelationPlan(
			query, linked.Receipt.Receipt, attemptResult.Attempt, target, readTime,
		)
		ledger, present, projectErr := projectCleanupExecutionLedger(target, attemptResult.Attempt)
		if !present {
			return ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
		}
		if projectErr != nil {
			// Missing pointer-backed fields make the stored ledger structurally
			// unreadable. Identity or fence drift is instead a semantic mismatch:
			// preserve the readable snapshot and let the pure evaluator classify it
			// as unverifiable without exposing any terminal evidence.
			if attemptResult.Attempt.CleanupRawTargeted == nil ||
				attemptResult.Attempt.CleanupManifestTargeted == nil {
				return ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
			}
			planValid = false
			ledger = ingest.CleanupExecutionLedger{}
		}
		result = ingest.CurrentCleanupExecutionDispositionSnapshot{
			Receipt: linked.Receipt.Receipt.toDomain(),
			Attempt: currentCleanupExecutionDispositionAttempt(attemptResult.Attempt, ledger),
			Plan:    plan, PlanValid: planValid, ReadTime: readTime,
		}
		return nil
	})
	if err != nil {
		return ingest.CurrentCleanupExecutionDispositionSnapshot{},
			normalizeCleanupExecutionDispositionOutcomeError(ctx, nil, err)
	}
	if result.Receipt.ReceiptID == "" || result.Attempt.AttemptID == "" || result.ReadTime.IsZero() {
		return ingest.CurrentCleanupExecutionDispositionSnapshot{},
			ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
	}
	if _, err := ingest.EvaluateCleanupExecutionDispositionOutcome(
		query, result, result.ReadTime,
	); err != nil {
		return ingest.CurrentCleanupExecutionDispositionSnapshot{}, err
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) GetCleanupExecutionDispositionOutcome(
	ctx context.Context,
	grant ingest.CleanupExecutionDispositionOutcomeReadGrant,
	query ingest.CleanupExecutionDispositionOutcomeQuery,
	observedAt time.Time,
) (ingest.CleanupExecutionDispositionOutcome, error) {
	if s == nil || s.runTransaction == nil || ctx == nil || observedAt.IsZero() {
		return ingest.CleanupExecutionDispositionOutcome{},
			ingest.ErrInvalidCleanupExecutionDispositionOutcome
	}
	observedAt = observedAt.UTC()
	if err := ingest.ValidateCleanupExecutionDispositionOutcomeAuthorization(
		grant, query, observedAt,
	); err != nil {
		return ingest.CleanupExecutionDispositionOutcome{}, err
	}
	deadline, err := ingest.CleanupExecutionDispositionOutcomeAuthorizationDeadline(grant, query)
	if err != nil {
		return ingest.CleanupExecutionDispositionOutcome{}, err
	}
	operationContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	snapshot, err := s.LoadCurrentCleanupExecutionDispositionOutcome(operationContext, query)
	if err != nil {
		return ingest.CleanupExecutionDispositionOutcome{},
			normalizeCleanupExecutionDispositionOutcomeError(ctx, operationContext, err)
	}
	effectiveAt, err := conservativeAcceptanceTime(observedAt, snapshot.ReadTime)
	if err != nil {
		return ingest.CleanupExecutionDispositionOutcome{},
			ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
	}
	if err := ingest.ValidateCleanupExecutionDispositionOutcomeAuthorization(
		grant, query, effectiveAt,
	); err != nil {
		return ingest.CleanupExecutionDispositionOutcome{}, err
	}
	result, err := ingest.EvaluateCleanupExecutionDispositionOutcome(query, snapshot, effectiveAt)
	if err != nil {
		return ingest.CleanupExecutionDispositionOutcome{},
			normalizeCleanupExecutionDispositionOutcomeError(ctx, operationContext, err)
	}
	return result, nil
}

func cleanupExecutionDispositionCorrelationPlan(
	query ingest.CleanupExecutionDispositionOutcomeQuery,
	receipt firestoreIngestReceipt,
	attempt firestoreRecoveryAttempt,
	target ingest.CleanupTarget,
	readTime time.Time,
) (ingest.CleanupExecutionLedgerPlan, bool) {
	executionQuery := ingest.CleanupExecutionQuery{
		TenantID: query.TenantID, ReservationKey: query.ReservationKey, AttemptID: query.AttemptID,
	}
	if receipt.State == ingest.ReceiptCleanupPending &&
		receipt.CleanupDispositionAttemptID != "" &&
		attempt.Status == ingest.RecoveryAttemptCompleted {
		plan, err := ingest.BuildDisposedCleanupExecutionLedgerPlan(
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

func currentCleanupExecutionDispositionAttempt(
	attempt firestoreRecoveryAttempt,
	ledger ingest.CleanupExecutionLedger,
) ingest.CurrentCleanupExecutionDispositionAttempt {
	return ingest.CurrentCleanupExecutionDispositionAttempt{
		AttemptID: attempt.AttemptID, TenantID: attempt.TenantID, ReceiptID: attempt.ReceiptID,
		OwnerKind: attempt.OwnerKind, FencingToken: attempt.FencingToken,
		WorkerVersion: attempt.WorkerVersion, Status: attempt.Status, Outcome: attempt.Outcome,
		StartedAt: attempt.StartedAt.UTC(), CompletedAt: attempt.CompletedAt.UTC(),
		FailureCode: attempt.FailureCode, FailedAt: attempt.FailedAt.UTC(),
		ForeignResidue: cleanupExpiryFinalizationAttemptHasForeignResidue(attempt),
		Ledger:         ledger,
	}
}

func normalizeCleanupExecutionDispositionError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	for _, bounded := range []error{
		ingest.ErrInvalidCleanupExecutionDisposition,
		ingest.ErrCleanupExecutionDispositionConflict,
		ingest.ErrCleanupExecutionDispositionUnavailable,
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
	return ingest.ErrCleanupExecutionDispositionUnavailable
}

func normalizeCleanupExecutionDispositionOperationError(
	parent context.Context,
	operation context.Context,
	err error,
) error {
	if parent != nil {
		if contextErr := parent.Err(); contextErr != nil {
			return contextErr
		}
	}
	if operation != nil && errors.Is(operation.Err(), context.DeadlineExceeded) {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	return normalizeCleanupExecutionDispositionError(parent, err)
}

func normalizeCleanupExecutionDispositionOutcomeError(
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
		return ingest.ErrCleanupExecutionDispositionOutcomeExpired
	}
	for _, bounded := range []error{
		ingest.ErrInvalidCleanupExecutionDispositionOutcome,
		ingest.ErrCleanupExecutionDispositionOutcomeExpired,
		ingest.ErrCleanupExecutionDispositionOutcomeUnavailable,
	} {
		if errors.Is(err, bounded) {
			return bounded
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return ingest.ErrCleanupExecutionDispositionOutcomeUnavailable
}
