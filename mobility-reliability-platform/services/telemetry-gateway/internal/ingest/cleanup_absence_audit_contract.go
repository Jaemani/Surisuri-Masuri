package ingest

import (
	"encoding/hex"
	"time"
)

const cleanupAbsenceAuditRequestBindingVersion = "cleanup-absence-audit-request@1"

type CleanupAbsenceAuditArtifact string

const (
	CleanupAbsenceAuditRaw      CleanupAbsenceAuditArtifact = "raw"
	CleanupAbsenceAuditManifest CleanupAbsenceAuditArtifact = "manifest"
)

// CleanupAbsenceAuditRequest is an in-process, read-only request. ExpectedPath
// is required by the provider adapter but must not be persisted to the attempt
// ledger or copied into logs and human reports.
type CleanupAbsenceAuditRequest struct {
	Query                   CleanupExecutionQuery
	ExpectedTargetHash      string
	ExpectedPlanHash        string
	ExpectedReceiptRevision int64
	ExpectedFence           LeaseFence
	ExpectedLedgerRevision  int64
	NextPhase               CleanupExecutionPhase
	Artifact                CleanupAbsenceAuditArtifact
	ExpectedPath            string
	RequestHash             string
}

// CleanupAbsenceAuditObservation is bounded success evidence from a fresh
// inventory-only provider read. It is not terminal receipt authority.
type CleanupAbsenceAuditObservation struct {
	RequestHash string
	Artifact    CleanupAbsenceAuditArtifact
	Outcome     CleanupAuditOutcome
	ObservedAt  time.Time
}

func BuildCleanupAbsenceAuditRequest(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	artifact CleanupAbsenceAuditArtifact,
	checkedAt time.Time,
) (CleanupAbsenceAuditRequest, error) {
	if checkedAt.IsZero() || ValidateCleanupExecutionLedger(plan, ledger, checkedAt.UTC()) != nil {
		return CleanupAbsenceAuditRequest{}, ErrInvalidCleanupExecutionLedger
	}
	request := CleanupAbsenceAuditRequest{
		Query: CleanupExecutionQuery{
			TenantID:       plan.Target.Command.TenantID,
			ReservationKey: plan.Target.Command.ReservationKey,
			AttemptID:      plan.Target.Command.AttemptID,
		},
		ExpectedTargetHash:      plan.Target.TargetHash,
		ExpectedPlanHash:        plan.PlanHash,
		ExpectedReceiptRevision: plan.Target.Command.ReceiptRevision,
		ExpectedFence:           ledger.Fence,
		ExpectedLedgerRevision:  ledger.Revision,
		Artifact:                artifact,
	}
	switch artifact {
	case CleanupAbsenceAuditRaw:
		if ledger.Phase != CleanupExecutionPhaseRawOutcomeRecorded {
			return CleanupAbsenceAuditRequest{}, ErrCleanupExecutionConflict
		}
		request.NextPhase = CleanupExecutionPhaseRawAbsenceConfirmed
		request.ExpectedPath = plan.ExpectedRawPath
	case CleanupAbsenceAuditManifest:
		if ledger.Phase != CleanupExecutionPhaseManifestOutcomeRecorded {
			return CleanupAbsenceAuditRequest{}, ErrCleanupExecutionConflict
		}
		request.NextPhase = CleanupExecutionPhaseManifestAbsenceConfirmed
		request.ExpectedPath = plan.ExpectedManifestPath
	default:
		return CleanupAbsenceAuditRequest{}, ErrInvalidCleanupExecutionLedger
	}
	request.RequestHash = cleanupAbsenceAuditRequestHash(request)
	if ValidateCleanupAbsenceAuditRequest(request) != nil {
		return CleanupAbsenceAuditRequest{}, ErrInvalidCleanupExecutionLedger
	}
	return request, nil
}

func ValidateCleanupAbsenceAuditRequest(request CleanupAbsenceAuditRequest) error {
	if ValidateCleanupExecutionQuery(request.Query) != nil ||
		!isLowerHexDigest(request.ExpectedTargetHash) ||
		!isLowerHexDigest(request.ExpectedPlanHash) ||
		request.ExpectedReceiptRevision <= 0 || request.ExpectedLedgerRevision <= 0 ||
		ValidateLeaseFence(request.ExpectedFence) != nil || request.ExpectedPath == "" ||
		!isLowerHexDigest(request.RequestHash) ||
		request.RequestHash != cleanupAbsenceAuditRequestHash(request) ||
		cleanupExecutionPhaseRevision(request.NextPhase) != request.ExpectedLedgerRevision+1 {
		return ErrInvalidCleanupExecutionLedger
	}
	switch request.Artifact {
	case CleanupAbsenceAuditRaw:
		if request.NextPhase != CleanupExecutionPhaseRawAbsenceConfirmed {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupAbsenceAuditManifest:
		if request.NextPhase != CleanupExecutionPhaseManifestAbsenceConfirmed {
			return ErrInvalidCleanupExecutionLedger
		}
	default:
		return ErrInvalidCleanupExecutionLedger
	}
	return nil
}

func ValidateCleanupAbsenceAuditObservation(
	request CleanupAbsenceAuditRequest,
	observation CleanupAbsenceAuditObservation,
) error {
	if ValidateCleanupAbsenceAuditRequest(request) != nil || observation.ObservedAt.IsZero() ||
		observation.RequestHash != request.RequestHash ||
		observation.Artifact != request.Artifact ||
		observation.Outcome != CleanupAuditConfirmedAbsent ||
		!observation.ObservedAt.Before(request.ExpectedFence.ExpiresAt) {
		return ErrInvalidCleanupExecutionObservation
	}
	return nil
}

func BuildCleanupAbsenceAuditProgressCommand(
	request CleanupAbsenceAuditRequest,
	observation CleanupAbsenceAuditObservation,
) (CleanupExecutionProgressCommand, error) {
	if ValidateCleanupAbsenceAuditObservation(request, observation) != nil {
		return CleanupExecutionProgressCommand{}, ErrInvalidCleanupExecutionLedger
	}
	command := CleanupExecutionProgressCommand{
		Query:                   request.Query,
		ExpectedTargetHash:      request.ExpectedTargetHash,
		ExpectedPlanHash:        request.ExpectedPlanHash,
		ExpectedReceiptRevision: request.ExpectedReceiptRevision,
		ExpectedLedgerRevision:  request.ExpectedLedgerRevision,
		Phase:                   request.NextPhase,
		AuditOutcome:            observation.Outcome,
	}
	if ValidateCleanupExecutionProgressCommand(command) != nil ||
		!CleanupExecutionProgressRequiresAbsenceEvidence(command) {
		return CleanupExecutionProgressCommand{}, ErrInvalidCleanupExecutionLedger
	}
	return command, nil
}

func cleanupAbsenceAuditRequestHash(request CleanupAbsenceAuditRequest) string {
	encoder := newArtifactBindingEncoder(cleanupAbsenceAuditRequestBindingVersion)
	encoder.addString(request.Query.TenantID)
	encoder.addString(request.Query.ReservationKey)
	encoder.addString(request.Query.AttemptID)
	encoder.addString(request.ExpectedTargetHash)
	encoder.addString(request.ExpectedPlanHash)
	encoder.addInt64(request.ExpectedReceiptRevision)
	encoder.addString(request.ExpectedFence.OwnerID)
	encoder.addInt64(request.ExpectedFence.Token)
	encoder.addTime(request.ExpectedFence.ExpiresAt)
	encoder.addInt64(request.ExpectedLedgerRevision)
	encoder.addString(string(request.NextPhase))
	encoder.addString(string(request.Artifact))
	encoder.addString(request.ExpectedPath)
	sum := encoder.sum()
	return hex.EncodeToString(sum[:])
}

func cloneCleanupAbsenceAuditRequest(
	request CleanupAbsenceAuditRequest,
) (CleanupAbsenceAuditRequest, error) {
	if ValidateCleanupAbsenceAuditRequest(request) != nil {
		return CleanupAbsenceAuditRequest{}, ErrInvalidCleanupExecutionLedger
	}
	cloned := request
	cloned.ExpectedFence = LeaseFence{
		OwnerID:   request.ExpectedFence.OwnerID,
		Token:     request.ExpectedFence.Token,
		ExpiresAt: request.ExpectedFence.ExpiresAt.UTC(),
	}
	return cloned, nil
}
