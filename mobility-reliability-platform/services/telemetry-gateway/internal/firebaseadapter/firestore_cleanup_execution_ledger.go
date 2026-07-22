package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

var _ ingest.CleanupExecutionLedgerStore = (*FirestoreAdmissionStore)(nil)

type cleanupExecutionLedgerState struct {
	attemptPath string
	attempt     firestoreRecoveryAttempt
	plan        ingest.CleanupExecutionLedgerPlan
	effectiveAt time.Time
	linked      linkedReceiptRead
}

func (s *FirestoreAdmissionStore) InitializeCleanupExecutionLedger(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionQuery(query) != nil {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrInvalidCleanupExecutionLedger
	}
	deadline, err := s.cleanupExecutionLedgerDeadline(ctx, query)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", normalizeCleanupExecutionLedgerStoreError(ctx, err)
	}
	operationContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ledger, mutationStatus, err := s.initializeCleanupExecutionLedger(operationContext, query)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", normalizeCleanupExecutionLedgerOperationError(
			ctx, operationContext, err,
		)
	}
	return ledger, mutationStatus, nil
}

func (s *FirestoreAdmissionStore) initializeCleanupExecutionLedger(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	var ledger ingest.CleanupExecutionLedger
	var mutationStatus ingest.CleanupExecutionMutationStatus
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		ledger = ingest.CleanupExecutionLedger{}
		mutationStatus = ""
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
		}
		persisted, present, decodeErr := decodeCleanupExecutionLedger(
			state.plan, state.attempt, state.effectiveAt,
		)
		if decodeErr != nil {
			return decodeErr
		}
		if present {
			ledger = persisted
			mutationStatus = ingest.CleanupExecutionMutationReplayed
			return nil
		}
		ledger, loadErr = ingest.NewCleanupExecutionLedger(state.plan)
		if loadErr != nil {
			return loadErr
		}
		updates, encodeErr := cleanupExecutionLedgerUpdates(state.plan, ledger, state.effectiveAt)
		if encodeErr != nil {
			return encodeErr
		}
		if updateErr := transaction.Update(runContext, state.attemptPath, updates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		mutationStatus = ingest.CleanupExecutionMutationApplied
		return nil
	})
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	if mutationStatus == "" {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrCleanupExecutionUnavailable
	}
	return ledger, mutationStatus, nil
}

func (s *FirestoreAdmissionStore) RecordCleanupExecutionProgress(
	ctx context.Context,
	command ingest.CleanupExecutionProgressCommand,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionProgressCommand(command) != nil ||
		ingest.CleanupExecutionProgressRequiresAbsenceEvidence(command) {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrInvalidCleanupExecutionLedger
	}
	deadline, err := s.cleanupExecutionLedgerDeadline(ctx, command.Query)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", normalizeCleanupExecutionLedgerStoreError(ctx, err)
	}
	operationContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ledger, mutationStatus, err := s.recordCleanupExecutionProgress(operationContext, command)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", normalizeCleanupExecutionLedgerOperationError(
			ctx, operationContext, err,
		)
	}
	return ledger, mutationStatus, nil
}

func (s *FirestoreAdmissionStore) recordCleanupExecutionProgress(
	ctx context.Context,
	command ingest.CleanupExecutionProgressCommand,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	var ledger ingest.CleanupExecutionLedger
	var mutationStatus ingest.CleanupExecutionMutationStatus
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		ledger = ingest.CleanupExecutionLedger{}
		mutationStatus = ""
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, command.Query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
		}
		if state.plan.Target.TargetHash != command.ExpectedTargetHash ||
			state.plan.PlanHash != command.ExpectedPlanHash ||
			state.plan.Target.Command.ReceiptRevision != command.ExpectedReceiptRevision {
			return ingest.ErrCleanupExecutionConflict
		}
		current, present, decodeErr := decodeCleanupExecutionLedger(
			state.plan, state.attempt, state.effectiveAt,
		)
		if decodeErr != nil {
			return decodeErr
		}
		if !present {
			return ingest.ErrCleanupExecutionConflict
		}
		if ingest.CleanupExecutionProgressAlreadyApplied(state.plan, current, command) {
			ledger = current
			mutationStatus = ingest.CleanupExecutionMutationReplayed
			return nil
		}
		if current.Revision != command.ExpectedLedgerRevision {
			return ingest.ErrCleanupExecutionConflict
		}
		next, advanceErr := ingest.AdvanceCleanupExecutionLedger(
			state.plan,
			current,
			ingest.CleanupExecutionTransition{
				Phase: command.Phase, DeleteOutcome: command.DeleteOutcome,
				AuditOutcome: command.AuditOutcome, ObservedAt: state.effectiveAt,
			},
		)
		if advanceErr != nil {
			return ingest.ErrCleanupExecutionConflict
		}
		updates, encodeErr := cleanupExecutionLedgerUpdates(state.plan, next, state.effectiveAt)
		if encodeErr != nil {
			return encodeErr
		}
		if updateErr := transaction.Update(runContext, state.attemptPath, updates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		ledger = next
		mutationStatus = ingest.CleanupExecutionMutationApplied
		return nil
	})
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	if mutationStatus == "" {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrCleanupExecutionUnavailable
	}
	return ledger, mutationStatus, nil
}

func (s *FirestoreAdmissionStore) cleanupExecutionLedgerDeadline(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
) (time.Time, error) {
	var deadline time.Time
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
		}
		deadline = state.plan.Target.Command.LeaseExpiresAt.UTC()
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	if deadline.IsZero() {
		return time.Time{}, ingest.ErrCleanupExecutionUnavailable
	}
	return deadline, nil
}

func loadCurrentCleanupExecutionLedgerState(
	ctx context.Context,
	transaction admissionTransaction,
	query ingest.CleanupExecutionQuery,
	applicationTime time.Time,
) (cleanupExecutionLedgerState, error) {
	if ctx == nil || transaction == nil || ingest.ValidateCleanupExecutionQuery(query) != nil ||
		applicationTime.IsZero() {
		return cleanupExecutionLedgerState{}, ingest.ErrCleanupExecutionUnavailable
	}
	linked, err := loadLinkedReceipt(ctx, transaction, query.TenantID, query.ReservationKey)
	if err != nil {
		return cleanupExecutionLedgerState{}, err
	}
	reader, ok := transaction.(cleanupTargetTransaction)
	if !ok {
		return cleanupExecutionLedgerState{}, ingest.ErrCleanupExecutionUnavailable
	}
	attemptPath := recoveryAttemptDocumentPath(
		query.TenantID, linked.Receipt.Receipt.ReceiptID, query.AttemptID,
	)
	attemptResult, exists, err := reader.ReadRecoveryAttempt(ctx, attemptPath)
	if err != nil {
		return cleanupExecutionLedgerState{}, err
	}
	if !exists || validateCleanupExecutionAttemptIdentity(
		attemptResult.Attempt, linked.Receipt.Receipt, query.AttemptID,
	) != nil {
		return cleanupExecutionLedgerState{}, ingest.ErrInvalidCleanupExecutionLedger
	}
	targetResult, exists, err := reader.ReadCleanupTarget(
		ctx, cleanupTargetDocumentPath(query.TenantID, query.AttemptID),
	)
	if err != nil {
		return cleanupExecutionLedgerState{}, err
	}
	if !exists {
		return cleanupExecutionLedgerState{}, ingest.ErrInvalidCleanupExecutionLedger
	}
	target, err := targetResult.Target.toDomain()
	if err != nil {
		return cleanupExecutionLedgerState{}, ingest.ErrInvalidCleanupExecutionLedger
	}
	readTime, err := coherentCleanupTargetReadTime(
		linked.Receipt.ReadTime, attemptResult.ReadTime, targetResult.ReadTime,
	)
	if err != nil {
		return cleanupExecutionLedgerState{}, err
	}
	effectiveAt, err := conservativeAcceptanceTime(applicationTime.UTC(), readTime)
	if err != nil {
		return cleanupExecutionLedgerState{}, ingest.ErrCleanupExecutionUnavailable
	}
	if !effectiveAt.Before(target.Command.LeaseExpiresAt) {
		return cleanupExecutionLedgerState{}, ingest.ErrCleanupExecutionUnauthorized
	}
	snapshot := ingest.CurrentCleanupExecutionSnapshot{
		Receipt: linked.Receipt.Receipt.toDomain(),
		Attempt: currentCleanupAttempt(attemptResult.Attempt),
		Target:  target, ReadTime: readTime,
	}
	plan, err := ingest.BuildCleanupExecutionLedgerPlan(query, snapshot, effectiveAt)
	if err != nil {
		return cleanupExecutionLedgerState{}, err
	}
	return cleanupExecutionLedgerState{
		attemptPath: attemptPath, attempt: attemptResult.Attempt,
		plan: plan, effectiveAt: effectiveAt, linked: linked,
	}, nil
}

func validateCleanupExecutionAttemptIdentity(
	attempt firestoreRecoveryAttempt,
	receipt firestoreIngestReceipt,
	attemptID string,
) error {
	if attempt.AttemptID != attemptID || attempt.TenantID != receipt.TenantID ||
		attempt.ReceiptID != receipt.ReceiptID || attempt.OwnerKind != ingest.LeaseOwnerCleanup ||
		attempt.FencingToken != receipt.FencingToken ||
		attempt.WorkerVersion != ingest.CleanupWorkerVersion ||
		attempt.Status != ingest.RecoveryAttemptStarted || attempt.StartedAt.IsZero() ||
		!attempt.StartedAt.Equal(receipt.LeaseAcquiredAt) ||
		attempt.AuthorizationDisposition != "" || attempt.Phase != "" ||
		attempt.Classification != "" || attempt.ReasonCode != "" || attempt.Action != "" ||
		attempt.Outcome != "" || attempt.ActionHash != "" || attempt.HoldCode != "" ||
		attempt.ReleaseCode != "" || attempt.RejectionCode != "" ||
		attempt.RawSHA256 != "" || attempt.RawCRC32C != 0 || attempt.RawSize != 0 ||
		attempt.RawGeneration != 0 || attempt.RawMetageneration != 0 ||
		attempt.ManifestSHA256 != "" || attempt.ManifestCRC32C != 0 ||
		attempt.ManifestSize != 0 || attempt.ManifestGeneration != 0 ||
		attempt.ManifestMetageneration != 0 || !attempt.HoldReviewDueAt.IsZero() ||
		!attempt.CompletedAt.IsZero() || attempt.FailureCode != "" || !attempt.FailedAt.IsZero() ||
		(attempt.DecisionDomain != "" &&
			attempt.DecisionDomain != ingest.CleanupExecutionDecisionExpiry) {
		return ingest.ErrInvalidCleanupExecutionLedger
	}
	return nil
}

func normalizeCleanupExecutionLedgerStoreError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) ||
		errors.Is(err, ingest.ErrCleanupExecutionConflict) ||
		errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ingest.ErrCleanupExecutionUnavailable
	}
	return ingest.ErrCleanupExecutionUnavailable
}

func normalizeCleanupExecutionLedgerOperationError(
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
	return normalizeCleanupExecutionLedgerStoreError(parent, err)
}
