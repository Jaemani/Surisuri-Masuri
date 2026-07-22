package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/cleanupattest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func NewFirestoreAdmissionStoreWithCleanupAbsenceAuditVerifier(
	client *firestore.Client,
	timeout time.Duration,
	now func() time.Time,
	verifier cleanupattest.Verifier,
) (*FirestoreAdmissionStore, error) {
	if !verifier.Valid() {
		return nil, errors.New("cleanup absence audit evidence verifier is required")
	}
	store, err := NewFirestoreAdmissionStore(client, timeout, now)
	if err != nil {
		return nil, err
	}
	store.cleanupAbsenceAuditEvidenceVerifier = verifier
	return store, nil
}

// RecordCleanupAbsenceAudit is the only public absence-confirmed persistence
// path. The generic progress method continues to reject both audit phases.
func (s *FirestoreAdmissionStore) RecordCleanupAbsenceAudit(
	ctx context.Context,
	grant CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
	evidence cleanupattest.Evidence,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		!s.cleanupAbsenceAuditEvidenceVerifier.Valid() ||
		ingest.ValidateCleanupAbsenceAuditRequest(request) != nil {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrInvalidCleanupExecutionLedger
	}
	if err := ctx.Err(); err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	binding, err := CleanupAbsenceAuditEvidenceBinding(grant, request)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	observation, err := s.cleanupAbsenceAuditEvidenceVerifier.VerifyCleanupAbsenceEvidence(
		request, binding, evidence,
	)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrCleanupExecutionUnauthorized
	}
	if err := ValidateCleanupAbsenceAuditAuthorization(
		grant, request, observation.ObservedAt,
	); err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	if err := ValidateCleanupAbsenceAuditAuthorization(grant, request, s.now().UTC()); err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	deadline, err := CleanupAbsenceAuditAuthorizationDeadline(grant, request)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	contextFactory := s.cleanupAbsenceAuditContext
	if contextFactory == nil {
		contextFactory = context.WithDeadline
	}
	operationContext, cancel := contextFactory(ctx, deadline)
	defer cancel()
	command, err := ingest.BuildCleanupAbsenceAuditProgressCommand(request, observation)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", err
	}
	var ledger ingest.CleanupExecutionLedger
	var mutationStatus ingest.CleanupExecutionMutationStatus
	err = s.runTransaction(operationContext, func(runContext context.Context, transaction admissionTransaction) error {
		ledger = ingest.CleanupExecutionLedger{}
		mutationStatus = ""
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, request.Query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
		}
		if authErr := ValidateCleanupAbsenceAuditAuthorization(
			grant, request, state.effectiveAt,
		); authErr != nil {
			return authErr
		}
		if observation.ObservedAt.After(state.effectiveAt) &&
			!withinAdmissionClockSkew(observation.ObservedAt, state.effectiveAt) {
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
		if !present {
			return ingest.ErrCleanupExecutionConflict
		}
		if ingest.CleanupExecutionProgressAlreadyApplied(state.plan, current, command) {
			if !cleanupAbsenceAuditObservationTime(current, request.Artifact).Equal(
				observation.ObservedAt,
			) {
				return ingest.ErrCleanupExecutionConflict
			}
			ledger = current
			mutationStatus = ingest.CleanupExecutionMutationReplayed
			return nil
		}
		if current.Revision != request.ExpectedLedgerRevision ||
			current.Fence != request.ExpectedFence {
			return ingest.ErrCleanupExecutionConflict
		}
		next, advanceErr := ingest.AdvanceCleanupExecutionLedger(
			state.plan,
			current,
			ingest.CleanupExecutionTransition{
				Phase: request.NextPhase, AuditOutcome: observation.Outcome,
				ObservedAt: observation.ObservedAt,
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
		if updateErr := transaction.Update(runContext, state.attemptPath, updates); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		ledger = next
		mutationStatus = ingest.CleanupExecutionMutationApplied
		return nil
	})
	if err != nil {
		return ingest.CleanupExecutionLedger{}, "", normalizeCleanupAbsenceAuditPersistenceError(
			ctx, operationContext, err,
		)
	}
	if mutationStatus == "" {
		return ingest.CleanupExecutionLedger{}, "", ingest.ErrCleanupExecutionUnavailable
	}
	return ledger, mutationStatus, nil
}

func cleanupAbsenceAuditObservationTime(
	ledger ingest.CleanupExecutionLedger,
	artifact ingest.CleanupAbsenceAuditArtifact,
) time.Time {
	switch artifact {
	case ingest.CleanupAbsenceAuditRaw:
		return ledger.Raw.AuditedAt.UTC()
	case ingest.CleanupAbsenceAuditManifest:
		return ledger.Manifest.AuditedAt.UTC()
	default:
		return time.Time{}
	}
}

func normalizeCleanupAbsenceAuditPersistenceError(
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
		return ErrCleanupAbsenceAuditAuthorizationExpired
	}
	for _, bounded := range []error{
		ErrCleanupAbsenceAuditAuthorizationExpired,
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
