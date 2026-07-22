package ingest

import (
	"encoding/hex"
	"time"
)

const cleanupArtifactExecutionRequestBindingVersion = "cleanup-artifact-execution-request@1"

type CleanupArtifactExecutionArtifact string

const (
	CleanupArtifactExecutionRaw      CleanupArtifactExecutionArtifact = "raw"
	CleanupArtifactExecutionManifest CleanupArtifactExecutionArtifact = "manifest"
)

// CleanupArtifactExecutionRequest binds one provider mutation opportunity to
// one exact durable pre-dispatch ledger revision. It is bounded to one path and
// can never authorize the counterpart artifact or an absence audit.
type CleanupArtifactExecutionRequest struct {
	Query                   CleanupExecutionQuery
	ExpectedTargetHash      string
	ExpectedPlanHash        string
	ExpectedReceiptRevision int64
	ExpectedFence           LeaseFence
	ExpectedLedgerRevision  int64
	DispatchPhase           CleanupExecutionPhase
	DispatchRevision        int64
	OutcomePhase            CleanupExecutionPhase
	Artifact                CleanupArtifactExecutionArtifact
	ExpectedPath            string
	Targeted                bool
	Lineage                 *ArtifactLineage
	RequestHash             string
}

// CleanupArtifactExecutionResult is deliberately bounded. Provider messages,
// paths, identities, credentials and payload data must never be copied here.
// An ambiguous delete returns a valid unknown result and a matching bounded
// ErrorClass so the caller can persist unknown before returning the error.
type CleanupArtifactExecutionResult struct {
	RequestHash       string
	Artifact          CleanupArtifactExecutionArtifact
	DispatchRevision  int64
	DeleteOutcome     CleanupDeleteRPCOutcome
	ErrorClass        CleanupExecutionErrorClass
	MutationStartedAt time.Time
	ObservedAt        time.Time
}

func BuildCleanupArtifactExecutionRequest(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	artifact CleanupArtifactExecutionArtifact,
) (CleanupArtifactExecutionRequest, error) {
	observedAt := cleanupExecutionLedgerLatestTime(plan, ledger)
	if observedAt.IsZero() || ValidateCleanupExecutionLedger(plan, ledger, observedAt) != nil {
		return CleanupArtifactExecutionRequest{}, ErrInvalidCleanupExecutionLedger
	}
	request := CleanupArtifactExecutionRequest{
		Query: CleanupExecutionQuery{
			TenantID:       plan.Target.Command.TenantID,
			ReservationKey: plan.Target.Command.ReservationKey,
			AttemptID:      plan.Target.Command.AttemptID,
		},
		ExpectedTargetHash:      plan.Target.TargetHash,
		ExpectedPlanHash:        plan.PlanHash,
		ExpectedReceiptRevision: plan.Target.Command.ReceiptRevision,
		ExpectedFence: LeaseFence{
			OwnerID: ledger.Fence.OwnerID, Token: ledger.Fence.Token,
			ExpiresAt: ledger.Fence.ExpiresAt.UTC(),
		},
		Artifact: artifact,
	}
	switch artifact {
	case CleanupArtifactExecutionRaw:
		if ledger.Phase != CleanupExecutionPhasePlanned &&
			ledger.Phase != CleanupExecutionPhaseRawDispatchRecorded {
			return CleanupArtifactExecutionRequest{}, ErrCleanupExecutionConflict
		}
		request.ExpectedLedgerRevision = 1
		request.DispatchRevision = 2
		request.DispatchPhase = CleanupExecutionPhaseRawDispatchRecorded
		request.OutcomePhase = CleanupExecutionPhaseRawOutcomeRecorded
		request.ExpectedPath = plan.ExpectedRawPath
		request.Targeted = plan.Target.Command.Raw != nil
		request.Lineage = cloneCleanupArtifactLineage(plan.Target.Command.Raw)
	case CleanupArtifactExecutionManifest:
		if ledger.Phase != CleanupExecutionPhaseRawAbsenceConfirmed &&
			ledger.Phase != CleanupExecutionPhaseManifestDispatchRecorded {
			return CleanupArtifactExecutionRequest{}, ErrCleanupExecutionConflict
		}
		if ledger.Raw.DeleteOutcome == CleanupDeleteUnknown {
			return CleanupArtifactExecutionRequest{}, ErrCleanupExecutionConflict
		}
		request.ExpectedLedgerRevision = 4
		request.DispatchRevision = 5
		request.DispatchPhase = CleanupExecutionPhaseManifestDispatchRecorded
		request.OutcomePhase = CleanupExecutionPhaseManifestOutcomeRecorded
		request.ExpectedPath = plan.ExpectedManifestPath
		request.Targeted = plan.Target.Command.Manifest != nil
		request.Lineage = cloneCleanupArtifactLineage(plan.Target.Command.Manifest)
	default:
		return CleanupArtifactExecutionRequest{}, ErrInvalidCleanupExecutionLedger
	}
	request.RequestHash = cleanupArtifactExecutionRequestHash(request)
	if ValidateCleanupArtifactExecutionRequest(request) != nil {
		return CleanupArtifactExecutionRequest{}, ErrInvalidCleanupExecutionLedger
	}
	return request, nil
}

func ValidateCleanupArtifactExecutionRequest(request CleanupArtifactExecutionRequest) error {
	if ValidateCleanupExecutionQuery(request.Query) != nil ||
		!isLowerHexDigest(request.ExpectedTargetHash) ||
		!isLowerHexDigest(request.ExpectedPlanHash) ||
		request.ExpectedReceiptRevision <= 0 ||
		ValidateLeaseFence(request.ExpectedFence) != nil ||
		request.ExpectedLedgerRevision <= 0 ||
		request.DispatchRevision != request.ExpectedLedgerRevision+1 ||
		cleanupExecutionPhaseRevision(request.DispatchPhase) != request.DispatchRevision ||
		cleanupExecutionPhaseRevision(request.OutcomePhase) != request.DispatchRevision+1 ||
		request.ExpectedPath == "" || !isLowerHexDigest(request.RequestHash) ||
		request.RequestHash != cleanupArtifactExecutionRequestHash(request) ||
		request.Targeted != (request.Lineage != nil) {
		return ErrInvalidCleanupExecutionLedger
	}
	if request.Lineage != nil && validateArtifactLineage(request.Lineage, request.ExpectedPath) != nil {
		return ErrInvalidCleanupExecutionLedger
	}
	switch request.Artifact {
	case CleanupArtifactExecutionRaw:
		if request.ExpectedLedgerRevision != 1 ||
			request.DispatchPhase != CleanupExecutionPhaseRawDispatchRecorded ||
			request.OutcomePhase != CleanupExecutionPhaseRawOutcomeRecorded {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupArtifactExecutionManifest:
		if request.ExpectedLedgerRevision != 4 ||
			request.DispatchPhase != CleanupExecutionPhaseManifestDispatchRecorded ||
			request.OutcomePhase != CleanupExecutionPhaseManifestOutcomeRecorded {
			return ErrInvalidCleanupExecutionLedger
		}
	default:
		return ErrInvalidCleanupExecutionLedger
	}
	return nil
}

func CloneCleanupArtifactExecutionRequest(
	request CleanupArtifactExecutionRequest,
) (CleanupArtifactExecutionRequest, error) {
	if ValidateCleanupArtifactExecutionRequest(request) != nil {
		return CleanupArtifactExecutionRequest{}, ErrInvalidCleanupExecutionLedger
	}
	cloned := request
	cloned.ExpectedFence = LeaseFence{
		OwnerID:   request.ExpectedFence.OwnerID,
		Token:     request.ExpectedFence.Token,
		ExpiresAt: request.ExpectedFence.ExpiresAt.UTC(),
	}
	cloned.Lineage = cloneCleanupArtifactLineage(request.Lineage)
	return cloned, nil
}

func ValidateCleanupArtifactExecutionResult(
	request CleanupArtifactExecutionRequest,
	result CleanupArtifactExecutionResult,
) error {
	if ValidateCleanupArtifactExecutionRequest(request) != nil || result.ObservedAt.IsZero() ||
		result.RequestHash != request.RequestHash || result.Artifact != request.Artifact ||
		result.DispatchRevision != request.DispatchRevision ||
		!result.ObservedAt.Before(request.ExpectedFence.ExpiresAt) ||
		(!result.MutationStartedAt.IsZero() &&
			(result.MutationStartedAt.After(result.ObservedAt) ||
				!result.MutationStartedAt.Before(request.ExpectedFence.ExpiresAt))) {
		return ErrInvalidCleanupExecutionObservation
	}
	if !request.Targeted {
		if result.DeleteOutcome != CleanupDeleteNotAttempted || result.ErrorClass != "" ||
			!result.MutationStartedAt.IsZero() {
			return ErrInvalidCleanupExecutionObservation
		}
		return nil
	}
	switch result.DeleteOutcome {
	case CleanupDeleteNotAttempted:
		if result.ErrorClass != "" || !result.MutationStartedAt.IsZero() {
			return ErrInvalidCleanupExecutionObservation
		}
	case CleanupDeleteObserved, CleanupDeleteNotFound:
		if result.ErrorClass != "" || result.MutationStartedAt.IsZero() {
			return ErrInvalidCleanupExecutionObservation
		}
	case CleanupDeleteUnknown:
		if result.MutationStartedAt.IsZero() ||
			!validCleanupExecutionErrorClass(result.ErrorClass) {
			return ErrInvalidCleanupExecutionObservation
		}
	default:
		return ErrInvalidCleanupExecutionObservation
	}
	return nil
}

func BuildCleanupArtifactExecutionOutcomeCommand(
	request CleanupArtifactExecutionRequest,
	result CleanupArtifactExecutionResult,
) (CleanupExecutionProgressCommand, error) {
	if ValidateCleanupArtifactExecutionResult(request, result) != nil {
		return CleanupExecutionProgressCommand{}, ErrInvalidCleanupExecutionLedger
	}
	command := CleanupExecutionProgressCommand{
		Query:                   request.Query,
		ExpectedTargetHash:      request.ExpectedTargetHash,
		ExpectedPlanHash:        request.ExpectedPlanHash,
		ExpectedReceiptRevision: request.ExpectedReceiptRevision,
		ExpectedLedgerRevision:  request.DispatchRevision,
		Phase:                   request.OutcomePhase,
		DeleteOutcome:           result.DeleteOutcome,
	}
	if ValidateCleanupExecutionProgressCommand(command) != nil {
		return CleanupExecutionProgressCommand{}, ErrInvalidCleanupExecutionLedger
	}
	return command, nil
}

func CleanupArtifactExecutionOutcomeKnown(result CleanupArtifactExecutionResult) bool {
	return result.DeleteOutcome == CleanupDeleteNotAttempted ||
		result.DeleteOutcome == CleanupDeleteObserved ||
		result.DeleteOutcome == CleanupDeleteNotFound
}

func validCleanupExecutionErrorClass(value CleanupExecutionErrorClass) bool {
	switch value {
	case CleanupExecutionErrorProviderTimeout,
		CleanupExecutionErrorProviderCancelled,
		CleanupExecutionErrorProviderUnavailable,
		CleanupExecutionErrorResponseUnverifiable,
		CleanupExecutionErrorQuotaLimited,
		CleanupExecutionErrorPermissionDenied,
		CleanupExecutionErrorPreconditionDrift,
		CleanupExecutionErrorGenerationDrift,
		CleanupExecutionErrorLineageMismatch,
		CleanupExecutionErrorInventoryIncomplete:
		return true
	default:
		return false
	}
}

func cleanupArtifactExecutionRequestHash(request CleanupArtifactExecutionRequest) string {
	encoder := newArtifactBindingEncoder(cleanupArtifactExecutionRequestBindingVersion)
	encoder.addString(request.Query.TenantID)
	encoder.addString(request.Query.ReservationKey)
	encoder.addString(request.Query.AttemptID)
	encoder.addString(request.ExpectedTargetHash)
	encoder.addString(request.ExpectedPlanHash)
	encoder.addInt64(request.ExpectedReceiptRevision)
	encoder.addLeaseFence(&request.ExpectedFence)
	encoder.addInt64(request.ExpectedLedgerRevision)
	encoder.addString(string(request.DispatchPhase))
	encoder.addInt64(request.DispatchRevision)
	encoder.addString(string(request.OutcomePhase))
	encoder.addString(string(request.Artifact))
	encoder.addString(request.ExpectedPath)
	if request.Targeted {
		encoder.addInt64(1)
	} else {
		encoder.addInt64(0)
	}
	encoder.addArtifactLineage(request.Lineage)
	sum := encoder.sum()
	return hex.EncodeToString(sum[:])
}

func cloneCleanupArtifactLineage(lineage *ArtifactLineage) *ArtifactLineage {
	if lineage == nil {
		return nil
	}
	cloned := *lineage
	return &cloned
}
