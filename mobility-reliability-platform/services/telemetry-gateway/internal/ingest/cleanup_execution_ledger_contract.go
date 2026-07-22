package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

const (
	CleanupExecutionLedgerSchemaVersion = "telemetry-cleanup-execution.v1"
	CleanupCompletionAuditWindow        = 7 * 24 * time.Hour

	cleanupExecutionLedgerPlanBindingVersion = "cleanup-execution-ledger-plan@1"
	cleanupExecutionEvidenceBindingVersion   = "cleanup-execution-evidence@1"
)

var (
	ErrInvalidCleanupExecutionLedger = errors.New("cleanup execution ledger is invalid")
	ErrCleanupExecutionConflict      = errors.New("cleanup execution transition conflicts with durable progress")
)

// CleanupExecutionDecisionExpiry is intentionally a value of the existing
// attempt decision-domain type. Forward validators continue to accept only
// their two forward values; cleanup validators opt in to this value explicitly.
const CleanupExecutionDecisionExpiry ForwardRecoveryDecisionDomain = "expiry_cleanup"

type CleanupExecutionPhase string

const (
	CleanupExecutionPhasePlanned                  CleanupExecutionPhase = "planned"
	CleanupExecutionPhaseRawDispatchRecorded      CleanupExecutionPhase = "raw_dispatch_recorded"
	CleanupExecutionPhaseRawOutcomeRecorded       CleanupExecutionPhase = "raw_outcome_recorded"
	CleanupExecutionPhaseRawAbsenceConfirmed      CleanupExecutionPhase = "raw_absence_confirmed"
	CleanupExecutionPhaseManifestDispatchRecorded CleanupExecutionPhase = "manifest_dispatch_recorded"
	CleanupExecutionPhaseManifestOutcomeRecorded  CleanupExecutionPhase = "manifest_outcome_recorded"
	CleanupExecutionPhaseManifestAbsenceConfirmed CleanupExecutionPhase = "manifest_absence_confirmed"
	CleanupExecutionPhaseCompleted                CleanupExecutionPhase = "completed"
)

type CleanupExecutionDisposition string

const (
	CleanupExecutionDispositionComplete CleanupExecutionDisposition = "complete"
	CleanupExecutionDispositionRetry    CleanupExecutionDisposition = "retry"
	CleanupExecutionDispositionHold     CleanupExecutionDisposition = "hold"
)

// CleanupExecutionErrorClass is deliberately bounded. It must never carry a
// provider message, object path, credential, UID, App ID, body, or coordinate.
type CleanupExecutionErrorClass string

const (
	CleanupExecutionErrorProviderTimeout      CleanupExecutionErrorClass = "provider_timeout"
	CleanupExecutionErrorProviderCancelled    CleanupExecutionErrorClass = "provider_cancelled"
	CleanupExecutionErrorProviderUnavailable  CleanupExecutionErrorClass = "provider_unavailable"
	CleanupExecutionErrorResponseUnverifiable CleanupExecutionErrorClass = "response_unverifiable"
	CleanupExecutionErrorQuotaLimited         CleanupExecutionErrorClass = "quota_limited"
	CleanupExecutionErrorPermissionDenied     CleanupExecutionErrorClass = "permission_denied"
	CleanupExecutionErrorPreconditionDrift    CleanupExecutionErrorClass = "precondition_drift"
	CleanupExecutionErrorGenerationDrift      CleanupExecutionErrorClass = "generation_drift"
	CleanupExecutionErrorLineageMismatch      CleanupExecutionErrorClass = "lineage_mismatch"
	CleanupExecutionErrorInventoryIncomplete  CleanupExecutionErrorClass = "inventory_incomplete"
)

type CleanupExecutionLedgerPlan struct {
	Target               CleanupTarget
	ExpectedRawPath      string
	ExpectedManifestPath string
	PlanHash             string
	requestBindingHash   [sha256.Size]byte
}

type CleanupArtifactExecutionLedger struct {
	Targeted          bool
	DispatchedAt      time.Time
	DeleteOutcome     CleanupDeleteRPCOutcome
	OutcomeRecordedAt time.Time
	AuditOutcome      CleanupAuditOutcome
	AuditedAt         time.Time
}

type CleanupExecutionLedger struct {
	SchemaVersion   string
	DecisionDomain  ForwardRecoveryDecisionDomain
	TargetHash      string
	PlanHash        string
	ReceiptRevision int64
	Fence           LeaseFence
	Revision        int64
	Phase           CleanupExecutionPhase
	Raw             CleanupArtifactExecutionLedger
	Manifest        CleanupArtifactExecutionLedger
	Disposition     CleanupExecutionDisposition
	ErrorClass      CleanupExecutionErrorClass
	EvidenceHash    string
	CompletedAt     time.Time
}

type CleanupExecutionTransition struct {
	Phase         CleanupExecutionPhase
	DeleteOutcome CleanupDeleteRPCOutcome
	AuditOutcome  CleanupAuditOutcome
	ObservedAt    time.Time
}

func BuildCleanupExecutionLedgerPlan(
	query CleanupExecutionQuery,
	snapshot CurrentCleanupExecutionSnapshot,
	checkedAt time.Time,
) (CleanupExecutionLedgerPlan, error) {
	if ValidateCleanupExecutionQuery(query) != nil || snapshot.ReadTime.IsZero() || checkedAt.IsZero() {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	checkedAt, err := forwardRecoveryAuthorizationTime(checkedAt.UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	target, err := cleanupExecutionTargetForPlan(query, snapshot, checkedAt)
	if err != nil {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	lease := cleanupExecutionTargetLease(target)
	if evaluateCurrentCleanup(CurrentCleanupSnapshot{
		Receipt: snapshot.Receipt, Attempt: snapshot.Attempt, ReadTime: snapshot.ReadTime,
	}, query.TenantID, query.ReservationKey, lease, checkedAt) != nil {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	return buildCleanupExecutionLedgerPlanFromTarget(query, snapshot.Receipt, target)
}

// BuildExpiredCleanupExecutionLedgerPlan reconstructs the immutable plan for
// an exact expired, still-current started cleanup attempt. It validates
// historical binding only and must never be used to authorize provider I/O.
func BuildExpiredCleanupExecutionLedgerPlan(
	query CleanupExecutionQuery,
	snapshot CurrentCleanupExecutionSnapshot,
	checkedAt time.Time,
) (CleanupExecutionLedgerPlan, error) {
	if ValidateCleanupExecutionQuery(query) != nil || snapshot.ReadTime.IsZero() || checkedAt.IsZero() {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	checkedAt, err := forwardRecoveryAuthorizationTime(checkedAt.UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	target, err := cleanupExecutionTargetForPlan(query, snapshot, checkedAt)
	if err != nil {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	lease := cleanupExecutionTargetLease(target)
	if evaluateExpiredCurrentCleanup(CurrentCleanupSnapshot{
		Receipt: snapshot.Receipt, Attempt: snapshot.Attempt, ReadTime: snapshot.ReadTime,
	}, query.TenantID, query.ReservationKey, lease, checkedAt) != nil {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	return buildCleanupExecutionLedgerPlanFromTarget(query, snapshot.Receipt, target)
}

func cleanupExecutionTargetForPlan(
	query CleanupExecutionQuery,
	snapshot CurrentCleanupExecutionSnapshot,
	checkedAt time.Time,
) (CleanupTarget, error) {
	target, err := CloneCleanupTarget(snapshot.Target)
	if err != nil ||
		target.Command.Status != CleanupTargetStatusPlanned ||
		(target.Command.Decision != CleanupTargetDeleteCandidate &&
			target.Command.Decision != CleanupTargetVerifiedEmpty) ||
		target.Command.TenantID != query.TenantID ||
		target.Command.ReservationKey != query.ReservationKey ||
		target.Command.AttemptID != query.AttemptID ||
		target.Command.CleanupID != query.AttemptID ||
		target.Command.ReceiptID != snapshot.Receipt.ReceiptID ||
		target.Command.CreatedAt.After(checkedAt) {
		return CleanupTarget{}, ErrInvalidCleanupExecutionLedger
	}
	return target, nil
}

func cleanupExecutionTargetLease(target CleanupTarget) CleanupLeaseGrant {
	command := target.Command
	return CleanupLeaseGrant{
		Lease: LeaseGrant{
			Fence: LeaseFence{
				OwnerID: command.AttemptID, Token: command.FencingToken,
				ExpiresAt: command.LeaseExpiresAt,
			},
			OwnerKind: LeaseOwnerCleanup, AcquiredAt: command.LeaseAcquiredAt,
			HeartbeatAt: command.LeaseHeartbeatAt,
		},
		ReceiptRevision: command.ReceiptRevision, Mode: command.Mode,
		OriginStatus: command.OriginStatus, PolicyVersion: command.CleanupPolicyVersion,
		TransitionedAt: command.CleanupTransitionedAt, QuiescenceUntil: command.CleanupQuiescenceUntil,
	}
}

func buildCleanupExecutionLedgerPlanFromTarget(
	query CleanupExecutionQuery,
	receipt Receipt,
	target CleanupTarget,
) (CleanupExecutionLedgerPlan, error) {
	command := target.Command
	request, err := cleanupClassificationRequest(receipt)
	if err != nil || request.Purpose != ArtifactReadCleanupDryRun ||
		request.TenantID != command.TenantID || request.ReceiptID != command.ReceiptID ||
		request.ReservationKey != command.ReservationKey ||
		request.ReceiptRevision != command.ReceiptRevision ||
		request.ValidatorVersion != command.ValidatorVersion || request.CleanupFence == nil ||
		request.CleanupFence.OwnerID != command.AttemptID ||
		request.CleanupFence.Token != command.FencingToken ||
		!request.CleanupFence.ExpiresAt.Equal(command.LeaseExpiresAt) {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	if target.Command.Raw != nil && target.Command.Raw.Path != request.ExpectedRawPath {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	if target.Command.Manifest != nil && target.Command.Manifest.Path != request.ExpectedManifestPath {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	if target.Command.Decision == CleanupTargetVerifiedEmpty &&
		(target.Command.Raw != nil || target.Command.Manifest != nil) {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	plan := CleanupExecutionLedgerPlan{
		Target: target, ExpectedRawPath: request.ExpectedRawPath,
		ExpectedManifestPath: request.ExpectedManifestPath,
		requestBindingHash:   canonicalArtifactClassificationRequestBinding(request),
	}
	plan.PlanHash = cleanupExecutionLedgerPlanHash(plan)
	if !isLowerHexDigest(plan.PlanHash) {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionLedger
	}
	return plan, nil
}

func ValidateCleanupExecutionLedgerPlan(plan CleanupExecutionLedgerPlan) error {
	target, err := CloneCleanupTarget(plan.Target)
	if err != nil || plan.ExpectedRawPath == "" || plan.ExpectedManifestPath == "" ||
		plan.ExpectedRawPath == plan.ExpectedManifestPath ||
		plan.requestBindingHash == ([sha256.Size]byte{}) ||
		target.Command.Status != CleanupTargetStatusPlanned ||
		(target.Command.Decision != CleanupTargetDeleteCandidate &&
			target.Command.Decision != CleanupTargetVerifiedEmpty) ||
		target.Command.Raw != nil && target.Command.Raw.Path != plan.ExpectedRawPath ||
		target.Command.Manifest != nil && target.Command.Manifest.Path != plan.ExpectedManifestPath ||
		target.Command.Decision == CleanupTargetVerifiedEmpty &&
			(target.Command.Raw != nil || target.Command.Manifest != nil) ||
		plan.PlanHash != cleanupExecutionLedgerPlanHash(plan) {
		return ErrInvalidCleanupExecutionLedger
	}
	return nil
}

func NewCleanupExecutionLedger(plan CleanupExecutionLedgerPlan) (CleanupExecutionLedger, error) {
	if ValidateCleanupExecutionLedgerPlan(plan) != nil {
		return CleanupExecutionLedger{}, ErrInvalidCleanupExecutionLedger
	}
	command := plan.Target.Command
	ledger := CleanupExecutionLedger{
		SchemaVersion:   CleanupExecutionLedgerSchemaVersion,
		DecisionDomain:  CleanupExecutionDecisionExpiry,
		TargetHash:      plan.Target.TargetHash,
		PlanHash:        plan.PlanHash,
		ReceiptRevision: command.ReceiptRevision,
		Fence: LeaseFence{
			OwnerID: command.AttemptID, Token: command.FencingToken, ExpiresAt: command.LeaseExpiresAt,
		},
		Revision: 1,
		Phase:    CleanupExecutionPhasePlanned,
		Raw:      CleanupArtifactExecutionLedger{Targeted: command.Raw != nil},
		Manifest: CleanupArtifactExecutionLedger{Targeted: command.Manifest != nil},
	}
	if ValidateCleanupExecutionLedger(plan, ledger, command.CreatedAt) != nil {
		return CleanupExecutionLedger{}, ErrInvalidCleanupExecutionLedger
	}
	return ledger, nil
}

func ValidateCleanupExecutionLedger(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	observedAt time.Time,
) error {
	if ValidateCleanupExecutionLedgerPlan(plan) != nil || observedAt.IsZero() ||
		observedAt.Before(plan.Target.Command.CreatedAt) ||
		ledger.SchemaVersion != CleanupExecutionLedgerSchemaVersion ||
		ledger.DecisionDomain != CleanupExecutionDecisionExpiry ||
		ledger.TargetHash != plan.Target.TargetHash || ledger.PlanHash != plan.PlanHash ||
		ledger.ReceiptRevision != plan.Target.Command.ReceiptRevision ||
		ledger.Fence.OwnerID != plan.Target.Command.AttemptID ||
		ledger.Fence.Token != plan.Target.Command.FencingToken ||
		!ledger.Fence.ExpiresAt.Equal(plan.Target.Command.LeaseExpiresAt) ||
		ValidateLeaseFence(ledger.Fence) != nil || !observedAt.Before(ledger.Fence.ExpiresAt) ||
		ledger.Raw.Targeted != (plan.Target.Command.Raw != nil) ||
		ledger.Manifest.Targeted != (plan.Target.Command.Manifest != nil) ||
		ledger.Revision != cleanupExecutionPhaseRevision(ledger.Phase) {
		return ErrInvalidCleanupExecutionLedger
	}

	if validateCleanupExecutionPhaseShape(ledger, observedAt.UTC()) != nil {
		return ErrInvalidCleanupExecutionLedger
	}
	return nil
}

// AdvanceCleanupExecutionLedger currently implements only the monotonic
// success path. Bounded retry/hold disposition and error-class persistence are
// deliberately deferred to the follow-up policy increment.
func AdvanceCleanupExecutionLedger(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	transition CleanupExecutionTransition,
) (CleanupExecutionLedger, error) {
	if transition.ObservedAt.IsZero() ||
		ValidateCleanupExecutionLedger(plan, ledger, transition.ObservedAt.UTC()) != nil ||
		transition.Phase != nextCleanupExecutionPhase(ledger.Phase) {
		return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
	}
	next := ledger
	next.Phase = transition.Phase
	next.Revision++
	observedAt := transition.ObservedAt.UTC()

	switch transition.Phase {
	case CleanupExecutionPhaseRawDispatchRecorded:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != "" {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Raw.DispatchedAt = observedAt
	case CleanupExecutionPhaseRawOutcomeRecorded:
		if transition.AuditOutcome != "" ||
			!validCleanupLedgerDeleteOutcome(next.Raw.Targeted, transition.DeleteOutcome) {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Raw.DeleteOutcome = transition.DeleteOutcome
		next.Raw.OutcomeRecordedAt = observedAt
	case CleanupExecutionPhaseRawAbsenceConfirmed:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != CleanupAuditConfirmedAbsent ||
			next.Raw.DeleteOutcome == CleanupDeleteUnknown {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Raw.AuditOutcome = transition.AuditOutcome
		next.Raw.AuditedAt = observedAt
	case CleanupExecutionPhaseManifestDispatchRecorded:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != "" ||
			next.Raw.DeleteOutcome == CleanupDeleteUnknown {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Manifest.DispatchedAt = observedAt
	case CleanupExecutionPhaseManifestOutcomeRecorded:
		if transition.AuditOutcome != "" ||
			!validCleanupLedgerDeleteOutcome(next.Manifest.Targeted, transition.DeleteOutcome) {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Manifest.DeleteOutcome = transition.DeleteOutcome
		next.Manifest.OutcomeRecordedAt = observedAt
	case CleanupExecutionPhaseManifestAbsenceConfirmed:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != CleanupAuditConfirmedAbsent ||
			next.Manifest.DeleteOutcome == CleanupDeleteUnknown {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Manifest.AuditOutcome = transition.AuditOutcome
		next.Manifest.AuditedAt = observedAt
	case CleanupExecutionPhaseCompleted:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != "" ||
			next.Raw.DeleteOutcome == CleanupDeleteUnknown ||
			next.Manifest.DeleteOutcome == CleanupDeleteUnknown {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Disposition = CleanupExecutionDispositionComplete
		next.CompletedAt = observedAt
		next.EvidenceHash = cleanupExecutionEvidenceHash(ledger, observedAt)
	default:
		return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
	}

	if ValidateCleanupExecutionLedger(plan, next, observedAt) != nil {
		return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
	}
	return next, nil
}

func CleanupExecutionEvidenceHash(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
) (string, error) {
	if ledger.Phase != CleanupExecutionPhaseCompleted ||
		ValidateCleanupExecutionLedger(plan, ledger, ledger.CompletedAt) != nil {
		return "", ErrInvalidCleanupExecutionLedger
	}
	prior := ledger
	prior.Phase = CleanupExecutionPhaseManifestAbsenceConfirmed
	prior.Revision--
	prior.Disposition = ""
	prior.EvidenceHash = ""
	prior.CompletedAt = time.Time{}
	return cleanupExecutionEvidenceHash(prior, ledger.CompletedAt), nil
}

func CleanupPurgeEligibleAt(receiptRetentionFloor, completedAt time.Time) (time.Time, error) {
	receiptRetentionFloor = receiptRetentionFloor.UTC()
	completedAt = completedAt.UTC()
	if !validCleanupFirestoreTimestamp(receiptRetentionFloor) ||
		!validCleanupFirestoreTimestamp(completedAt) {
		return time.Time{}, ErrInvalidCleanupExecutionLedger
	}
	auditFloor := completedAt.Add(CleanupCompletionAuditWindow)
	if !auditFloor.After(completedAt) || !validCleanupFirestoreTimestamp(auditFloor) {
		return time.Time{}, ErrInvalidCleanupExecutionLedger
	}
	if receiptRetentionFloor.After(auditFloor) {
		return receiptRetentionFloor, nil
	}
	return auditFloor, nil
}

func cleanupExecutionLedgerPlanHash(plan CleanupExecutionLedgerPlan) string {
	encoder := newArtifactBindingEncoder(cleanupExecutionLedgerPlanBindingVersion)
	encoder.addString(plan.Target.TargetHash)
	encoder.addString(plan.ExpectedRawPath)
	encoder.addString(plan.ExpectedManifestPath)
	encoder.addBytes(plan.requestBindingHash[:])
	sum := encoder.sum()
	return hex.EncodeToString(sum[:])
}

// validCleanupFirestoreTimestamp mirrors the timestamp range accepted by
// Firestore/protobuf. UTC normalization happens before this helper is called.
func validCleanupFirestoreTimestamp(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}

func cleanupExecutionEvidenceHash(ledger CleanupExecutionLedger, completedAt time.Time) string {
	encoder := newArtifactBindingEncoder(cleanupExecutionEvidenceBindingVersion)
	encoder.addString(ledger.SchemaVersion)
	encoder.addString(string(ledger.DecisionDomain))
	encoder.addString(ledger.TargetHash)
	encoder.addString(ledger.PlanHash)
	encoder.addInt64(ledger.ReceiptRevision)
	encoder.addLeaseFence(&ledger.Fence)
	encoder.addInt64(ledger.Revision)
	encoder.addString(string(ledger.Phase))
	addCleanupArtifactExecutionLedgerBinding(encoder, ledger.Raw)
	addCleanupArtifactExecutionLedgerBinding(encoder, ledger.Manifest)
	encoder.addString(string(CleanupExecutionDispositionComplete))
	encoder.addTime(completedAt)
	sum := encoder.sum()
	return hex.EncodeToString(sum[:])
}

func addCleanupArtifactExecutionLedgerBinding(
	encoder *artifactBindingEncoder,
	record CleanupArtifactExecutionLedger,
) {
	if record.Targeted {
		encoder.addInt64(1)
	} else {
		encoder.addInt64(0)
	}
	encoder.addTime(record.DispatchedAt)
	encoder.addString(string(record.DeleteOutcome))
	encoder.addTime(record.OutcomeRecordedAt)
	encoder.addString(string(record.AuditOutcome))
	encoder.addTime(record.AuditedAt)
}

func validateCleanupExecutionPhaseShape(ledger CleanupExecutionLedger, observedAt time.Time) error {
	emptyTerminal := ledger.Disposition == "" && ledger.ErrorClass == "" &&
		ledger.EvidenceHash == "" && ledger.CompletedAt.IsZero()
	switch ledger.Phase {
	case CleanupExecutionPhasePlanned:
		if !emptyTerminal || !emptyCleanupArtifactProgress(ledger.Raw) ||
			!emptyCleanupArtifactProgress(ledger.Manifest) {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseRawDispatchRecorded:
		if !emptyTerminal || !dispatchOnlyCleanupArtifactProgress(ledger.Raw, observedAt) ||
			!emptyCleanupArtifactProgress(ledger.Manifest) {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseRawOutcomeRecorded:
		if !emptyTerminal || !outcomeCleanupArtifactProgress(ledger.Raw, observedAt) ||
			!emptyCleanupArtifactProgress(ledger.Manifest) {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseRawAbsenceConfirmed:
		if !emptyTerminal || !auditedCleanupArtifactProgress(ledger.Raw, observedAt) ||
			ledger.Raw.DeleteOutcome == CleanupDeleteUnknown ||
			!emptyCleanupArtifactProgress(ledger.Manifest) {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseManifestDispatchRecorded:
		if !emptyTerminal || !auditedCleanupArtifactProgress(ledger.Raw, observedAt) ||
			ledger.Raw.DeleteOutcome == CleanupDeleteUnknown ||
			!dispatchOnlyCleanupArtifactProgress(ledger.Manifest, observedAt) ||
			ledger.Manifest.DispatchedAt.Before(ledger.Raw.AuditedAt) {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseManifestOutcomeRecorded:
		if !emptyTerminal || !auditedCleanupArtifactProgress(ledger.Raw, observedAt) ||
			ledger.Raw.DeleteOutcome == CleanupDeleteUnknown ||
			!outcomeCleanupArtifactProgress(ledger.Manifest, observedAt) ||
			ledger.Manifest.DispatchedAt.Before(ledger.Raw.AuditedAt) {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseManifestAbsenceConfirmed:
		if !emptyTerminal || !auditedCleanupArtifactProgress(ledger.Raw, observedAt) ||
			ledger.Raw.DeleteOutcome == CleanupDeleteUnknown ||
			!auditedCleanupArtifactProgress(ledger.Manifest, observedAt) ||
			ledger.Manifest.DeleteOutcome == CleanupDeleteUnknown ||
			ledger.Manifest.DispatchedAt.Before(ledger.Raw.AuditedAt) {
			return ErrInvalidCleanupExecutionLedger
		}
	case CleanupExecutionPhaseCompleted:
		if ledger.Disposition != CleanupExecutionDispositionComplete || ledger.ErrorClass != "" ||
			!isLowerHexDigest(ledger.EvidenceHash) || ledger.CompletedAt.IsZero() ||
			ledger.CompletedAt.After(observedAt) || ledger.CompletedAt.Before(ledger.Manifest.AuditedAt) ||
			ledger.Raw.DeleteOutcome == CleanupDeleteUnknown ||
			ledger.Manifest.DeleteOutcome == CleanupDeleteUnknown ||
			!auditedCleanupArtifactProgress(ledger.Raw, observedAt) ||
			!auditedCleanupArtifactProgress(ledger.Manifest, observedAt) {
			return ErrInvalidCleanupExecutionLedger
		}
		prior := ledger
		prior.Phase = CleanupExecutionPhaseManifestAbsenceConfirmed
		prior.Revision--
		prior.Disposition = ""
		prior.EvidenceHash = ""
		prior.CompletedAt = time.Time{}
		if ledger.EvidenceHash != cleanupExecutionEvidenceHash(prior, ledger.CompletedAt) {
			return ErrInvalidCleanupExecutionLedger
		}
	default:
		return ErrInvalidCleanupExecutionLedger
	}
	return nil
}

func validCleanupLedgerDeleteOutcome(targeted bool, outcome CleanupDeleteRPCOutcome) bool {
	if !targeted {
		return outcome == CleanupDeleteNotAttempted
	}
	switch outcome {
	case CleanupDeleteNotAttempted, CleanupDeleteObserved, CleanupDeleteNotFound, CleanupDeleteUnknown:
		return true
	default:
		return false
	}
}

func emptyCleanupArtifactProgress(record CleanupArtifactExecutionLedger) bool {
	return record.DispatchedAt.IsZero() && record.DeleteOutcome == "" &&
		record.OutcomeRecordedAt.IsZero() && record.AuditOutcome == "" && record.AuditedAt.IsZero()
}

func dispatchOnlyCleanupArtifactProgress(
	record CleanupArtifactExecutionLedger,
	observedAt time.Time,
) bool {
	return !record.DispatchedAt.IsZero() && !record.DispatchedAt.After(observedAt) &&
		record.DeleteOutcome == "" && record.OutcomeRecordedAt.IsZero() &&
		record.AuditOutcome == "" && record.AuditedAt.IsZero()
}

func outcomeCleanupArtifactProgress(
	record CleanupArtifactExecutionLedger,
	observedAt time.Time,
) bool {
	return !record.DispatchedAt.IsZero() && !record.DispatchedAt.After(observedAt) &&
		validCleanupLedgerDeleteOutcome(record.Targeted, record.DeleteOutcome) &&
		!record.OutcomeRecordedAt.IsZero() && !record.OutcomeRecordedAt.Before(record.DispatchedAt) &&
		!record.OutcomeRecordedAt.After(observedAt) && record.AuditOutcome == "" && record.AuditedAt.IsZero()
}

func auditedCleanupArtifactProgress(
	record CleanupArtifactExecutionLedger,
	observedAt time.Time,
) bool {
	return outcomeCleanupArtifactProgress(CleanupArtifactExecutionLedger{
		Targeted: record.Targeted, DispatchedAt: record.DispatchedAt,
		DeleteOutcome: record.DeleteOutcome, OutcomeRecordedAt: record.OutcomeRecordedAt,
	}, observedAt) && record.AuditOutcome == CleanupAuditConfirmedAbsent &&
		!record.AuditedAt.IsZero() && !record.AuditedAt.Before(record.OutcomeRecordedAt) &&
		!record.AuditedAt.After(observedAt)
}

func cleanupExecutionPhaseRevision(phase CleanupExecutionPhase) int64 {
	switch phase {
	case CleanupExecutionPhasePlanned:
		return 1
	case CleanupExecutionPhaseRawDispatchRecorded:
		return 2
	case CleanupExecutionPhaseRawOutcomeRecorded:
		return 3
	case CleanupExecutionPhaseRawAbsenceConfirmed:
		return 4
	case CleanupExecutionPhaseManifestDispatchRecorded:
		return 5
	case CleanupExecutionPhaseManifestOutcomeRecorded:
		return 6
	case CleanupExecutionPhaseManifestAbsenceConfirmed:
		return 7
	case CleanupExecutionPhaseCompleted:
		return 8
	default:
		return 0
	}
}

func nextCleanupExecutionPhase(phase CleanupExecutionPhase) CleanupExecutionPhase {
	switch phase {
	case CleanupExecutionPhasePlanned:
		return CleanupExecutionPhaseRawDispatchRecorded
	case CleanupExecutionPhaseRawDispatchRecorded:
		return CleanupExecutionPhaseRawOutcomeRecorded
	case CleanupExecutionPhaseRawOutcomeRecorded:
		return CleanupExecutionPhaseRawAbsenceConfirmed
	case CleanupExecutionPhaseRawAbsenceConfirmed:
		return CleanupExecutionPhaseManifestDispatchRecorded
	case CleanupExecutionPhaseManifestDispatchRecorded:
		return CleanupExecutionPhaseManifestOutcomeRecorded
	case CleanupExecutionPhaseManifestOutcomeRecorded:
		return CleanupExecutionPhaseManifestAbsenceConfirmed
	case CleanupExecutionPhaseManifestAbsenceConfirmed:
		return CleanupExecutionPhaseCompleted
	default:
		return ""
	}
}
