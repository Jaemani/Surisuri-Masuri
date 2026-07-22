package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func (s *FirestoreAdmissionStore) BeginCleanupArtifactExecution(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
	artifact ingest.CleanupArtifactExecutionArtifact,
) (
	ingest.CleanupArtifactExecutionRequest,
	CleanupArtifactExecutionAuthorizationGrant,
	ingest.CleanupExecutionLedger,
	ingest.CleanupExecutionMutationStatus,
	error,
) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionQuery(query) != nil ||
		(artifact != ingest.CleanupArtifactExecutionRaw &&
			artifact != ingest.CleanupArtifactExecutionManifest) {
		return ingest.CleanupArtifactExecutionRequest{},
			CleanupArtifactExecutionAuthorizationGrant{}, ingest.CleanupExecutionLedger{}, "",
			ingest.ErrInvalidCleanupExecutionLedger
	}
	if err := ctx.Err(); err != nil {
		return ingest.CleanupArtifactExecutionRequest{},
			CleanupArtifactExecutionAuthorizationGrant{}, ingest.CleanupExecutionLedger{}, "", err
	}
	leaseDeadline, err := s.cleanupExecutionLedgerDeadline(ctx, query)
	if err != nil {
		return ingest.CleanupArtifactExecutionRequest{},
			CleanupArtifactExecutionAuthorizationGrant{}, ingest.CleanupExecutionLedger{}, "",
			normalizeCleanupExecutionLedgerStoreError(ctx, err)
	}
	operationDeadline := leaseDeadline.Add(-CleanupArtifactExecutionOutcomePersistenceGrace)
	applicationDeadline := s.now().UTC().Add(CleanupArtifactExecutionMutationTTL)
	if applicationDeadline.Before(operationDeadline) {
		operationDeadline = applicationDeadline
	}
	if !s.now().UTC().Before(operationDeadline) {
		return ingest.CleanupArtifactExecutionRequest{},
			CleanupArtifactExecutionAuthorizationGrant{}, ingest.CleanupExecutionLedger{}, "",
			ErrCleanupArtifactExecutionAuthorizationExpired
	}
	contextFactory := s.cleanupArtifactExecutionContext
	if contextFactory == nil {
		contextFactory = context.WithDeadline
	}
	operationContext, cancel := contextFactory(ctx, operationDeadline)
	defer cancel()
	var request ingest.CleanupArtifactExecutionRequest
	var ledger ingest.CleanupExecutionLedger
	var mutationStatus ingest.CleanupExecutionMutationStatus
	var checkedAt time.Time
	var mutationExpiresAt time.Time
	var outcomeExpiresAt time.Time
	err = s.runTransaction(operationContext, func(runContext context.Context, transaction admissionTransaction) error {
		request = ingest.CleanupArtifactExecutionRequest{}
		ledger = ingest.CleanupExecutionLedger{}
		mutationStatus = ""
		checkedAt = time.Time{}
		mutationExpiresAt = time.Time{}
		outcomeExpiresAt = time.Time{}
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
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
		request, loadErr = ingest.BuildCleanupArtifactExecutionRequest(
			state.plan, current, artifact,
		)
		if loadErr != nil {
			return loadErr
		}
		checkedAt = state.effectiveAt
		if current.Phase == request.DispatchPhase &&
			current.Revision == request.DispatchRevision {
			ledger = current
			mutationStatus = ingest.CleanupExecutionMutationReplayed
			return nil
		}
		mutationExpiresAt, outcomeExpiresAt, loadErr = cleanupArtifactExecutionAuthorizationWindow(
			checkedAt, request.ExpectedFence.ExpiresAt,
		)
		if loadErr != nil {
			return loadErr
		}
		if current.Revision != request.ExpectedLedgerRevision {
			return ingest.ErrCleanupExecutionConflict
		}
		command, commandErr := ingest.BuildCleanupExecutionProgressCommand(
			state.plan, current, request.DispatchPhase, "", "",
		)
		if commandErr != nil {
			return commandErr
		}
		next, advanceErr := ingest.AdvanceCleanupExecutionLedger(
			state.plan,
			current,
			ingest.CleanupExecutionTransition{
				Phase: command.Phase, ObservedAt: state.effectiveAt,
			},
		)
		if advanceErr != nil {
			return advanceErr
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
		return ingest.CleanupArtifactExecutionRequest{},
			CleanupArtifactExecutionAuthorizationGrant{}, ingest.CleanupExecutionLedger{}, "",
			normalizeCleanupExecutionLedgerOperationError(ctx, operationContext, err)
	}
	if mutationStatus == "" || ingest.ValidateCleanupArtifactExecutionRequest(request) != nil {
		return ingest.CleanupArtifactExecutionRequest{},
			CleanupArtifactExecutionAuthorizationGrant{}, ingest.CleanupExecutionLedger{}, "",
			ingest.ErrCleanupExecutionUnavailable
	}
	if mutationStatus == ingest.CleanupExecutionMutationReplayed {
		return request, CleanupArtifactExecutionAuthorizationGrant{}, ledger, mutationStatus, nil
	}
	if mutationExpiresAt.IsZero() || outcomeExpiresAt.IsZero() {
		return ingest.CleanupArtifactExecutionRequest{},
			CleanupArtifactExecutionAuthorizationGrant{}, ingest.CleanupExecutionLedger{}, "",
			ingest.ErrCleanupExecutionUnavailable
	}
	grant := CleanupArtifactExecutionAuthorizationGrant{
		policyVersion: CleanupArtifactExecutionPolicyVersion,
		checkedAt:     checkedAt, mutationExpiresAt: mutationExpiresAt,
		outcomeExpiresAt: outcomeExpiresAt,
		requestHash:      request.RequestHash, targetHash: request.ExpectedTargetHash,
		planHash: request.ExpectedPlanHash, receiptRevision: request.ExpectedReceiptRevision,
		ownerID: request.ExpectedFence.OwnerID, fencingToken: request.ExpectedFence.Token,
		leaseExpiresAt:   request.ExpectedFence.ExpiresAt,
		dispatchRevision: request.DispatchRevision, dispatchPhase: request.DispatchPhase,
		artifact: request.Artifact, targeted: request.Targeted,
	}
	grant.capabilitySeal = cleanupArtifactExecutionCapabilitySeal(grant)
	if validationErr := ValidateCleanupArtifactExecutionAuthorization(
		grant, request, s.now().UTC(),
	); validationErr != nil {
		return ingest.CleanupArtifactExecutionRequest{},
			CleanupArtifactExecutionAuthorizationGrant{}, ingest.CleanupExecutionLedger{}, "",
			validationErr
	}
	return request, grant, ledger, mutationStatus, nil
}

// RecordCleanupArtifactExecutionOutcome persists only the bounded delete RPC
// outcome. ErrorClass remains in the in-process result for R8g reporting; the
// durable unknown outcome blocks signed audit and the counterpart artifact.
func (s *FirestoreAdmissionStore) RecordCleanupArtifactExecutionOutcome(
	ctx context.Context,
	grant CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
	result ingest.CleanupArtifactExecutionResult,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateCleanupArtifactExecutionResult(request, result) != nil {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrInvalidCleanupExecutionLedger
	}
	if err := ctx.Err(); err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	if err := ValidateCleanupArtifactExecutionOutcomeAuthorization(grant, request, result); err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	if err := ValidateCleanupArtifactExecutionOutcomePersistence(
		grant, request, s.now().UTC(),
	); err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	deadline, err := CleanupArtifactExecutionOutcomePersistenceDeadline(grant, request)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	contextFactory := s.cleanupArtifactExecutionContext
	if contextFactory == nil {
		contextFactory = context.WithDeadline
	}
	operationContext, cancel := contextFactory(ctx, deadline)
	defer cancel()
	command, err := ingest.BuildCleanupArtifactExecutionOutcomeCommand(request, result)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	var ledger ingest.CleanupExecutionLedger
	var mutationStatus ingest.CleanupExecutionMutationStatus
	err = s.runTransaction(operationContext, func(
		runContext context.Context,
		transaction admissionTransaction,
	) error {
		ledger = ingest.CleanupExecutionLedger{}
		mutationStatus = ""
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, request.Query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
		}
		if authErr := ValidateCleanupArtifactExecutionOutcomePersistence(
			grant, request, state.effectiveAt,
		); authErr != nil {
			return authErr
		}
		if result.ObservedAt.After(state.effectiveAt) &&
			!withinAdmissionClockSkew(result.ObservedAt, state.effectiveAt) {
			return ingest.ErrCleanupExecutionUnavailable
		}
		if state.plan.Target.TargetHash != request.ExpectedTargetHash ||
			state.plan.PlanHash != request.ExpectedPlanHash ||
			state.plan.Target.Command.ReceiptRevision != request.ExpectedReceiptRevision {
			return ingest.ErrCleanupExecutionConflict
		}
		current, present, decodeErr := decodeCleanupExecutionLedger(
			state.plan, state.attempt, state.effectiveAt,
		)
		if decodeErr != nil {
			return decodeErr
		}
		if !present || current.Fence != request.ExpectedFence {
			return ingest.ErrCleanupExecutionConflict
		}
		if ingest.CleanupExecutionProgressAlreadyApplied(state.plan, current, command) {
			ledger = current
			mutationStatus = ingest.CleanupExecutionMutationReplayed
			return nil
		}
		if current.Revision != request.DispatchRevision ||
			current.Phase != request.DispatchPhase {
			return ingest.ErrCleanupExecutionConflict
		}
		next, advanceErr := ingest.AdvanceCleanupExecutionLedger(
			state.plan,
			current,
			ingest.CleanupExecutionTransition{
				Phase: request.OutcomePhase, DeleteOutcome: result.DeleteOutcome,
				ObservedAt: state.effectiveAt,
			},
		)
		if advanceErr != nil {
			return ingest.ErrCleanupExecutionConflict
		}
		updates, encodeErr := cleanupExecutionLedgerUpdates(
			state.plan, next, state.effectiveAt,
		)
		if encodeErr != nil {
			return encodeErr
		}
		if updateErr := transaction.Update(
			runContext, state.attemptPath, updates,
		); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		ledger = next
		mutationStatus = ingest.CleanupExecutionMutationApplied
		return nil
	})
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "",
			normalizeCleanupArtifactExecutionPersistenceError(ctx, operationContext, err)
	}
	if mutationStatus == "" {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrCleanupExecutionUnavailable
	}
	return ledger, mutationStatus, nil
}

func normalizeCleanupArtifactExecutionPersistenceError(
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
		return ErrCleanupArtifactExecutionAuthorizationExpired
	}
	for _, bounded := range []error{
		ErrCleanupArtifactExecutionAuthorizationExpired,
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
	return ingest.ErrCleanupExecutionUnavailable
}
