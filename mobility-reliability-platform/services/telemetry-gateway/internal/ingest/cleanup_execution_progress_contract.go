package ingest

import (
	"context"
	"time"
)

type CleanupExecutionMutationStatus string

const (
	CleanupExecutionMutationApplied  CleanupExecutionMutationStatus = "applied"
	CleanupExecutionMutationReplayed CleanupExecutionMutationStatus = "replayed"
)

// CleanupExecutionProgressCommand carries only bounded control data. The
// persistence boundary supplies the trusted transaction/application time for
// the actual transition and must never accept an object path or provider
// error through this command.
type CleanupExecutionProgressCommand struct {
	Query                   CleanupExecutionQuery
	ExpectedTargetHash      string
	ExpectedPlanHash        string
	ExpectedReceiptRevision int64
	ExpectedLedgerRevision  int64
	Phase                   CleanupExecutionPhase
	DeleteOutcome           CleanupDeleteRPCOutcome
	AuditOutcome            CleanupAuditOutcome
	// ErrorClass is accepted only by the artifact-outcome persistence path.
	// Generic progress persistence rejects a non-empty value.
	ErrorClass CleanupExecutionErrorClass
}

type CleanupExecutionLedgerStore interface {
	InitializeCleanupExecutionLedger(
		context.Context,
		CleanupExecutionQuery,
	) (CleanupExecutionLedger, CleanupExecutionMutationStatus, error)
	RecordCleanupExecutionProgress(
		context.Context,
		CleanupExecutionProgressCommand,
	) (CleanupExecutionLedger, CleanupExecutionMutationStatus, error)
}

// CleanupExecutionProgressRequiresAbsenceEvidence marks transitions that a
// generic progress store must not persist from command shape alone. A separate
// read-only absence-audit capability is required before these phases can
// become durable evidence.
func CleanupExecutionProgressRequiresAbsenceEvidence(
	command CleanupExecutionProgressCommand,
) bool {
	return command.Phase == CleanupExecutionPhaseRawAbsenceConfirmed ||
		command.Phase == CleanupExecutionPhaseManifestAbsenceConfirmed
}

func BuildCleanupExecutionProgressCommand(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	phase CleanupExecutionPhase,
	deleteOutcome CleanupDeleteRPCOutcome,
	auditOutcome CleanupAuditOutcome,
) (CleanupExecutionProgressCommand, error) {
	observedAt := cleanupExecutionLedgerLatestTime(plan, ledger)
	if observedAt.IsZero() || ValidateCleanupExecutionLedger(plan, ledger, observedAt) != nil ||
		phase == CleanupExecutionPhaseCompleted {
		return CleanupExecutionProgressCommand{}, ErrInvalidCleanupExecutionLedger
	}
	transition := CleanupExecutionTransition{
		Phase: phase, DeleteOutcome: deleteOutcome, AuditOutcome: auditOutcome, ObservedAt: observedAt,
	}
	if _, err := AdvanceCleanupExecutionLedger(plan, ledger, transition); err != nil {
		return CleanupExecutionProgressCommand{}, ErrInvalidCleanupExecutionLedger
	}
	command := CleanupExecutionProgressCommand{
		Query: CleanupExecutionQuery{
			TenantID:       plan.Target.Command.TenantID,
			ReservationKey: plan.Target.Command.ReservationKey,
			AttemptID:      plan.Target.Command.AttemptID,
		},
		ExpectedTargetHash:      plan.Target.TargetHash,
		ExpectedPlanHash:        plan.PlanHash,
		ExpectedReceiptRevision: plan.Target.Command.ReceiptRevision,
		ExpectedLedgerRevision:  ledger.Revision,
		Phase:                   phase,
		DeleteOutcome:           deleteOutcome,
		AuditOutcome:            auditOutcome,
	}
	if ValidateCleanupExecutionProgressCommand(command) != nil {
		return CleanupExecutionProgressCommand{}, ErrInvalidCleanupExecutionLedger
	}
	return command, nil
}

func ValidateCleanupExecutionProgressCommand(command CleanupExecutionProgressCommand) error {
	if ValidateCleanupExecutionQuery(command.Query) != nil ||
		!isLowerHexDigest(command.ExpectedTargetHash) ||
		!isLowerHexDigest(command.ExpectedPlanHash) ||
		command.ExpectedReceiptRevision <= 0 || command.ExpectedLedgerRevision <= 0 ||
		cleanupExecutionPhaseRevision(command.Phase) != command.ExpectedLedgerRevision+1 {
		return ErrInvalidCleanupExecutionLedger
	}
	switch command.Phase {
	case CleanupExecutionPhaseRawDispatchRecorded,
		CleanupExecutionPhaseManifestDispatchRecorded:
		if command.DeleteOutcome != "" || command.AuditOutcome != "" || command.ErrorClass != "" {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseRawOutcomeRecorded,
		CleanupExecutionPhaseManifestOutcomeRecorded:
		if command.AuditOutcome != "" || !validCleanupDeleteOutcome(command.DeleteOutcome) ||
			!validCleanupOutcomeErrorClass(command.DeleteOutcome, command.ErrorClass) {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseRawAbsenceConfirmed,
		CleanupExecutionPhaseManifestAbsenceConfirmed:
		if command.DeleteOutcome != "" || command.AuditOutcome != CleanupAuditConfirmedAbsent ||
			command.ErrorClass != "" {
			return ErrInvalidCleanupExecutionLedger
		}
	default:
		return ErrInvalidCleanupExecutionLedger
	}
	return nil
}

// CleanupExecutionProgressAlreadyApplied recognizes only the exact next
// revision and semantic command. A later phase is deliberately not guessed as
// a replay; callers must use a separate correlation read after response loss.
func CleanupExecutionProgressAlreadyApplied(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	command CleanupExecutionProgressCommand,
) bool {
	observedAt := cleanupExecutionLedgerLatestTime(plan, ledger)
	if observedAt.IsZero() || ValidateCleanupExecutionLedger(plan, ledger, observedAt) != nil ||
		ValidateCleanupExecutionProgressCommand(command) != nil ||
		command.Query.TenantID != plan.Target.Command.TenantID ||
		command.Query.ReservationKey != plan.Target.Command.ReservationKey ||
		command.Query.AttemptID != plan.Target.Command.AttemptID ||
		ledger.TargetHash != command.ExpectedTargetHash ||
		ledger.PlanHash != command.ExpectedPlanHash ||
		ledger.ReceiptRevision != command.ExpectedReceiptRevision ||
		ledger.Revision != command.ExpectedLedgerRevision+1 ||
		ledger.Phase != command.Phase {
		return false
	}
	switch command.Phase {
	case CleanupExecutionPhaseRawDispatchRecorded,
		CleanupExecutionPhaseManifestDispatchRecorded:
		return true
	case CleanupExecutionPhaseRawOutcomeRecorded:
		return ledger.Raw.DeleteOutcome == command.DeleteOutcome && ledger.ErrorClass == command.ErrorClass
	case CleanupExecutionPhaseRawAbsenceConfirmed:
		return ledger.Raw.AuditOutcome == command.AuditOutcome
	case CleanupExecutionPhaseManifestOutcomeRecorded:
		return ledger.Manifest.DeleteOutcome == command.DeleteOutcome && ledger.ErrorClass == command.ErrorClass
	case CleanupExecutionPhaseManifestAbsenceConfirmed:
		return ledger.Manifest.AuditOutcome == command.AuditOutcome
	default:
		return false
	}
}

func validCleanupDeleteOutcome(outcome CleanupDeleteRPCOutcome) bool {
	switch outcome {
	case CleanupDeleteNotAttempted, CleanupDeleteObserved, CleanupDeleteNotFound, CleanupDeleteUnknown:
		return true
	default:
		return false
	}
}

func cleanupExecutionLedgerLatestTime(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
) time.Time {
	switch ledger.Phase {
	case CleanupExecutionPhasePlanned:
		return plan.Target.Command.CreatedAt.UTC()
	case CleanupExecutionPhaseRawDispatchRecorded:
		return ledger.Raw.DispatchedAt.UTC()
	case CleanupExecutionPhaseRawOutcomeRecorded:
		return ledger.Raw.OutcomeRecordedAt.UTC()
	case CleanupExecutionPhaseRawAbsenceConfirmed:
		return ledger.Raw.AuditedAt.UTC()
	case CleanupExecutionPhaseManifestDispatchRecorded:
		return ledger.Manifest.DispatchedAt.UTC()
	case CleanupExecutionPhaseManifestOutcomeRecorded:
		return ledger.Manifest.OutcomeRecordedAt.UTC()
	case CleanupExecutionPhaseManifestAbsenceConfirmed:
		return ledger.Manifest.AuditedAt.UTC()
	case CleanupExecutionPhaseCompleted:
		return ledger.CompletedAt.UTC()
	default:
		return time.Time{}
	}
}
