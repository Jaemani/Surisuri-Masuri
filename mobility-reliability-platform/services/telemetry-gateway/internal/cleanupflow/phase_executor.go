package cleanupflow

import (
	"context"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/cleanupattest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/gcsadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const (
	DefaultOutcomePersistenceTimeout = 5 * time.Second
	DefaultMaxPhaseSteps             = 12
)

var (
	ErrInvalidPhaseExecution     = errors.New("cleanup phase execution is invalid")
	ErrCleanupDispatchPending    = errors.New("cleanup artifact dispatch is already durable")
	ErrCleanupOutcomeUnknown     = errors.New("cleanup artifact outcome is unknown")
	ErrCleanupPhaseBudgetReached = errors.New("cleanup phase execution budget reached")
)

var (
	_ ControlStore     = (*firebaseadapter.FirestoreAdmissionStore)(nil)
	_ artifactExecutor = (*gcsadapter.CleanupSingleArtifactExecutor)(nil)
	_ absenceAuditor   = (*gcsadapter.CleanupAbsenceAuditor)(nil)
)

type ControlStore interface {
	InitializeCleanupExecutionLedger(
		context.Context,
		ingest.CleanupExecutionQuery,
	) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error)
	BeginCleanupArtifactExecution(
		context.Context,
		ingest.CleanupExecutionQuery,
		ingest.CleanupArtifactExecutionArtifact,
	) (
		ingest.CleanupArtifactExecutionRequest,
		firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		ingest.CleanupExecutionLedger,
		ingest.CleanupExecutionMutationStatus,
		error,
	)
	RecordCleanupArtifactExecutionOutcome(
		context.Context,
		firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		ingest.CleanupArtifactExecutionRequest,
		ingest.CleanupArtifactExecutionResult,
	) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error)
	AuthorizeCleanupAbsenceAudit(
		context.Context,
		ingest.CleanupExecutionQuery,
		ingest.CleanupAbsenceAuditArtifact,
	) (
		ingest.CleanupAbsenceAuditRequest,
		firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
		error,
	)
	RecordCleanupAbsenceAudit(
		context.Context,
		firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
		ingest.CleanupAbsenceAuditRequest,
		cleanupattest.Evidence,
	) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error)
}

type artifactExecutor interface {
	ExecuteCleanupArtifact(
		context.Context,
		firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		ingest.CleanupArtifactExecutionRequest,
	) (ingest.CleanupArtifactExecutionResult, error)
}

type absenceAuditor interface {
	AuditCleanupAbsence(
		context.Context,
		firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
		ingest.CleanupAbsenceAuditRequest,
	) (cleanupattest.Evidence, error)
}

type ExecutionStatus string

const (
	ExecutionReadyForFinalization ExecutionStatus = "ready_for_finalization"
	ExecutionDispatchPending      ExecutionStatus = "dispatch_pending"
	ExecutionUnknownOutcome       ExecutionStatus = "unknown_outcome"
)

type ExecutionResult struct {
	Status         ExecutionStatus
	Phase          ingest.CleanupExecutionPhase
	Artifact       ingest.CleanupArtifactExecutionArtifact
	DeleteOutcome  ingest.CleanupDeleteRPCOutcome
	ErrorClass     ingest.CleanupExecutionErrorClass
	LedgerRevision int64
	Steps          int
}

type PhaseExecutor struct {
	control                   ControlStore
	artifacts                 artifactExecutor
	auditor                   absenceAuditor
	outcomePersistenceTimeout time.Duration
	maxSteps                  int
}

func NewPhaseExecutor(
	control ControlStore,
	artifacts *gcsadapter.CleanupSingleArtifactExecutor,
	auditor *gcsadapter.CleanupAbsenceAuditor,
) (*PhaseExecutor, error) {
	if artifacts == nil || auditor == nil {
		return nil, ErrInvalidPhaseExecution
	}
	return newPhaseExecutor(
		control, artifacts, auditor,
		DefaultOutcomePersistenceTimeout, DefaultMaxPhaseSteps,
	)
}

func newPhaseExecutor(
	control ControlStore,
	artifacts artifactExecutor,
	auditor absenceAuditor,
	outcomePersistenceTimeout time.Duration,
	maxSteps int,
) (*PhaseExecutor, error) {
	if control == nil || artifacts == nil || auditor == nil ||
		outcomePersistenceTimeout <= 0 ||
		outcomePersistenceTimeout > DefaultOutcomePersistenceTimeout ||
		maxSteps < 1 || maxSteps > DefaultMaxPhaseSteps {
		return nil, ErrInvalidPhaseExecution
	}
	return &PhaseExecutor{
		control: control, artifacts: artifacts, auditor: auditor,
		outcomePersistenceTimeout: outcomePersistenceTimeout,
		maxSteps:                  maxSteps,
	}, nil
}

func (e *PhaseExecutor) Execute(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
) (ExecutionResult, error) {
	if e == nil || e.control == nil || e.artifacts == nil || e.auditor == nil ||
		ctx == nil || ingest.ValidateCleanupExecutionQuery(query) != nil {
		return ExecutionResult{}, ErrInvalidPhaseExecution
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}
	ledger, _, err := e.control.InitializeCleanupExecutionLedger(ctx, query)
	if err != nil {
		return ExecutionResult{}, err
	}
	result := executionResultFromLedger(ledger)
	for step := 0; step < e.maxSteps; step++ {
		result.Steps = step + 1
		switch ledger.Phase {
		case ingest.CleanupExecutionPhasePlanned:
			ledger, result, err = e.executeArtifact(
				ctx, query, ingest.CleanupArtifactExecutionRaw, result,
			)
		case ingest.CleanupExecutionPhaseRawDispatchRecorded:
			result.Status = ExecutionDispatchPending
			result.Artifact = ingest.CleanupArtifactExecutionRaw
			return result, ErrCleanupDispatchPending
		case ingest.CleanupExecutionPhaseRawOutcomeRecorded:
			if ledger.Raw.DeleteOutcome == ingest.CleanupDeleteUnknown {
				result.Status = ExecutionUnknownOutcome
				result.Artifact = ingest.CleanupArtifactExecutionRaw
				result.DeleteOutcome = ledger.Raw.DeleteOutcome
				return result, ErrCleanupOutcomeUnknown
			}
			ledger, result, err = e.auditArtifact(
				ctx, query, ingest.CleanupAbsenceAuditRaw, result,
			)
		case ingest.CleanupExecutionPhaseRawAbsenceConfirmed:
			ledger, result, err = e.executeArtifact(
				ctx, query, ingest.CleanupArtifactExecutionManifest, result,
			)
		case ingest.CleanupExecutionPhaseManifestDispatchRecorded:
			result.Status = ExecutionDispatchPending
			result.Artifact = ingest.CleanupArtifactExecutionManifest
			return result, ErrCleanupDispatchPending
		case ingest.CleanupExecutionPhaseManifestOutcomeRecorded:
			if ledger.Manifest.DeleteOutcome == ingest.CleanupDeleteUnknown {
				result.Status = ExecutionUnknownOutcome
				result.Artifact = ingest.CleanupArtifactExecutionManifest
				result.DeleteOutcome = ledger.Manifest.DeleteOutcome
				return result, ErrCleanupOutcomeUnknown
			}
			ledger, result, err = e.auditArtifact(
				ctx, query, ingest.CleanupAbsenceAuditManifest, result,
			)
		case ingest.CleanupExecutionPhaseManifestAbsenceConfirmed:
			result.Status = ExecutionReadyForFinalization
			result.Phase = ledger.Phase
			result.LedgerRevision = ledger.Revision
			return result, nil
		default:
			return result, ErrInvalidPhaseExecution
		}
		if err != nil {
			return result, err
		}
		result.Phase = ledger.Phase
		result.LedgerRevision = ledger.Revision
	}
	return result, ErrCleanupPhaseBudgetReached
}

func (e *PhaseExecutor) executeArtifact(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
	artifact ingest.CleanupArtifactExecutionArtifact,
	result ExecutionResult,
) (ingest.CleanupExecutionLedger, ExecutionResult, error) {
	request, grant, ledger, status, err := e.control.BeginCleanupArtifactExecution(
		ctx, query, artifact,
	)
	if err != nil {
		return ledger, result, err
	}
	result.Phase = ledger.Phase
	result.LedgerRevision = ledger.Revision
	result.Artifact = artifact
	if status != ingest.CleanupExecutionMutationApplied {
		result.Status = ExecutionDispatchPending
		return ledger, result, ErrCleanupDispatchPending
	}
	providerResult, providerErr := e.artifacts.ExecuteCleanupArtifact(ctx, grant, request)
	if providerResult == (ingest.CleanupArtifactExecutionResult{}) {
		return ledger, result, providerErr
	}
	result.DeleteOutcome = providerResult.DeleteOutcome
	result.ErrorClass = providerResult.ErrorClass
	persistContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), e.outcomePersistenceTimeout,
	)
	persisted, _, persistErr := e.control.RecordCleanupArtifactExecutionOutcome(
		persistContext, grant, request, providerResult,
	)
	cancel()
	if persistErr != nil {
		return ledger, result, errors.Join(providerErr, persistErr)
	}
	result.Phase = persisted.Phase
	result.LedgerRevision = persisted.Revision
	if providerResult.DeleteOutcome == ingest.CleanupDeleteUnknown {
		result.Status = ExecutionUnknownOutcome
		return persisted, result, errors.Join(providerErr, ErrCleanupOutcomeUnknown)
	}
	if providerErr != nil {
		return persisted, result, providerErr
	}
	return persisted, result, nil
}

func (e *PhaseExecutor) auditArtifact(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
	artifact ingest.CleanupAbsenceAuditArtifact,
	result ExecutionResult,
) (ingest.CleanupExecutionLedger, ExecutionResult, error) {
	request, grant, err := e.control.AuthorizeCleanupAbsenceAudit(ctx, query, artifact)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, result, err
	}
	evidence, err := e.auditor.AuditCleanupAbsence(ctx, grant, request)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, result, err
	}
	ledger, _, err := e.control.RecordCleanupAbsenceAudit(ctx, grant, request, evidence)
	if err != nil {
		return ingest.CleanupExecutionLedger{}, result, err
	}
	result.Phase = ledger.Phase
	result.LedgerRevision = ledger.Revision
	return ledger, result, nil
}

func executionResultFromLedger(ledger ingest.CleanupExecutionLedger) ExecutionResult {
	return ExecutionResult{Phase: ledger.Phase, LedgerRevision: ledger.Revision}
}
