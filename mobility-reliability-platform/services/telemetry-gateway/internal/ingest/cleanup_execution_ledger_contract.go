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
	CleanupRetryBackoffTransient        = 15 * time.Minute
	CleanupRetryBackoffInventory        = 30 * time.Minute
	CleanupRetryBackoffQuota            = 60 * time.Minute
	CleanupHoldReviewWindow             = 24 * time.Hour

	cleanupExecutionLedgerPlanBindingVersion          = "cleanup-execution-ledger-plan@1"
	cleanupExecutionEvidenceBindingVersion            = "cleanup-execution-evidence@1"
	cleanupExecutionDispositionEvidenceBindingVersion = "cleanup-execution-disposition-evidence@1"
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
	ErrorClass    CleanupExecutionErrorClass
	ObservedAt    time.Time
}

// CleanupExecutionFailurePolicy is a closed mapping from a bounded execution
// error to one cleanup-only terminal disposition and its control delay.
// Delay means retry backoff for retry and human review window for hold.
type CleanupExecutionFailurePolicy struct {
	Disposition CleanupExecutionDisposition
	Delay       time.Duration
}

func CleanupExecutionFailurePolicyFor(
	errorClass CleanupExecutionErrorClass,
) (CleanupExecutionFailurePolicy, error) {
	switch errorClass {
	case CleanupExecutionErrorProviderTimeout,
		CleanupExecutionErrorProviderCancelled,
		CleanupExecutionErrorProviderUnavailable,
		CleanupExecutionErrorResponseUnverifiable:
		return CleanupExecutionFailurePolicy{
			Disposition: CleanupExecutionDispositionRetry,
			Delay:       CleanupRetryBackoffTransient,
		}, nil
	case CleanupExecutionErrorInventoryIncomplete:
		return CleanupExecutionFailurePolicy{
			Disposition: CleanupExecutionDispositionRetry,
			Delay:       CleanupRetryBackoffInventory,
		}, nil
	case CleanupExecutionErrorQuotaLimited:
		return CleanupExecutionFailurePolicy{
			Disposition: CleanupExecutionDispositionRetry,
			Delay:       CleanupRetryBackoffQuota,
		}, nil
	case CleanupExecutionErrorPermissionDenied,
		CleanupExecutionErrorPreconditionDrift,
		CleanupExecutionErrorGenerationDrift,
		CleanupExecutionErrorLineageMismatch:
		return CleanupExecutionFailurePolicy{
			Disposition: CleanupExecutionDispositionHold,
			Delay:       CleanupHoldReviewWindow,
		}, nil
	default:
		return CleanupExecutionFailurePolicy{}, ErrInvalidCleanupExecutionLedger
	}
}

// CleanupExecutionDispositionCursorAt returns exactly one cleanup control
// cursor. Retry waits for both policy backoff and the old provider fence;
// hold exposes only a deterministic human-review due time.
func CleanupExecutionDispositionCursorAt(
	fence LeaseFence,
	errorClass CleanupExecutionErrorClass,
	completedAt time.Time,
) (nextCleanupAt time.Time, holdReviewDueAt time.Time, err error) {
	completedAt = completedAt.UTC()
	policy, policyErr := CleanupExecutionFailurePolicyFor(errorClass)
	cursorAt := completedAt.Add(policy.Delay)
	if policyErr != nil || ValidateLeaseFence(fence) != nil ||
		!validCleanupFirestoreTimestamp(completedAt) ||
		!validCleanupFirestoreTimestamp(cursorAt) || !cursorAt.After(completedAt) {
		return time.Time{}, time.Time{}, ErrInvalidCleanupExecutionLedger
	}
	if policy.Disposition == CleanupExecutionDispositionRetry {
		if fence.ExpiresAt.UTC().After(cursorAt) {
			cursorAt = fence.ExpiresAt.UTC()
		}
		return cursorAt, time.Time{}, nil
	}
	return time.Time{}, cursorAt, nil
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
		if transition.DeleteOutcome != "" || transition.AuditOutcome != "" || transition.ErrorClass != "" {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Raw.DispatchedAt = observedAt
	case CleanupExecutionPhaseRawOutcomeRecorded:
		if transition.AuditOutcome != "" ||
			!validCleanupLedgerDeleteOutcome(next.Raw.Targeted, transition.DeleteOutcome) ||
			!validCleanupOutcomeErrorClass(transition.DeleteOutcome, transition.ErrorClass) {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Raw.DeleteOutcome = transition.DeleteOutcome
		next.Raw.OutcomeRecordedAt = observedAt
		next.ErrorClass = transition.ErrorClass
	case CleanupExecutionPhaseRawAbsenceConfirmed:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != CleanupAuditConfirmedAbsent ||
			transition.ErrorClass != "" ||
			next.Raw.DeleteOutcome == CleanupDeleteUnknown {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Raw.AuditOutcome = transition.AuditOutcome
		next.Raw.AuditedAt = observedAt
	case CleanupExecutionPhaseManifestDispatchRecorded:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != "" || transition.ErrorClass != "" ||
			next.Raw.DeleteOutcome == CleanupDeleteUnknown {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Manifest.DispatchedAt = observedAt
	case CleanupExecutionPhaseManifestOutcomeRecorded:
		if transition.AuditOutcome != "" ||
			!validCleanupLedgerDeleteOutcome(next.Manifest.Targeted, transition.DeleteOutcome) ||
			!validCleanupOutcomeErrorClass(transition.DeleteOutcome, transition.ErrorClass) {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Manifest.DeleteOutcome = transition.DeleteOutcome
		next.Manifest.OutcomeRecordedAt = observedAt
		next.ErrorClass = transition.ErrorClass
	case CleanupExecutionPhaseManifestAbsenceConfirmed:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != CleanupAuditConfirmedAbsent ||
			transition.ErrorClass != "" ||
			next.Manifest.DeleteOutcome == CleanupDeleteUnknown {
			return CleanupExecutionLedger{}, ErrCleanupExecutionConflict
		}
		next.Manifest.AuditOutcome = transition.AuditOutcome
		next.Manifest.AuditedAt = observedAt
	case CleanupExecutionPhaseCompleted:
		if transition.DeleteOutcome != "" || transition.AuditOutcome != "" || transition.ErrorClass != "" ||
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

// CompleteCleanupExecutionDisposition closes an execution-stage cleanup
// failure without inventing later success progress. Phase and revision remain
// exactly as persisted; only bounded terminal evidence is added.
func CompleteCleanupExecutionDisposition(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	errorClass CleanupExecutionErrorClass,
	completedAt time.Time,
) (CleanupExecutionLedger, CleanupExecutionFailurePolicy, error) {
	completedAt = completedAt.UTC()
	policy, err := CleanupExecutionFailurePolicyFor(errorClass)
	if err != nil || !cleanupExecutionDispositionPhaseAllowed(ledger.Phase) ||
		ledger.Revision != cleanupExecutionPhaseRevision(ledger.Phase) ||
		ledger.Disposition != "" || ledger.EvidenceHash != "" || !ledger.CompletedAt.IsZero() ||
		ValidateCleanupExecutionLedger(plan, ledger, completedAt) != nil {
		return CleanupExecutionLedger{}, CleanupExecutionFailurePolicy{},
			ErrInvalidCleanupExecutionLedger
	}
	if cleanupExecutionLedgerHasAmbiguousOutcome(ledger) {
		if ledger.ErrorClass != errorClass {
			return CleanupExecutionLedger{}, CleanupExecutionFailurePolicy{},
				ErrInvalidCleanupExecutionLedger
		}
	} else if ledger.ErrorClass != "" {
		return CleanupExecutionLedger{}, CleanupExecutionFailurePolicy{},
			ErrInvalidCleanupExecutionLedger
	}

	terminal := ledger
	terminal.Disposition = policy.Disposition
	terminal.ErrorClass = errorClass
	terminal.CompletedAt = completedAt
	terminal.EvidenceHash = cleanupExecutionDispositionEvidenceHash(
		ledger, policy.Disposition, errorClass, completedAt,
	)
	if ValidateCleanupExecutionLedger(plan, terminal, completedAt) != nil {
		return CleanupExecutionLedger{}, CleanupExecutionFailurePolicy{},
			ErrInvalidCleanupExecutionLedger
	}
	return terminal, policy, nil
}

// CleanupExecutionDispositionEvidenceHash recomputes the terminal evidence
// after validating the complete phase-preserving ledger.
func CleanupExecutionDispositionEvidenceHash(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
) (string, error) {
	if !cleanupExecutionTerminalDisposition(ledger.Disposition) ||
		ValidateCleanupExecutionLedger(plan, ledger, ledger.CompletedAt) != nil {
		return "", ErrInvalidCleanupExecutionLedger
	}
	prior, err := cleanupExecutionLedgerBeforeDisposition(ledger)
	if err != nil {
		return "", ErrInvalidCleanupExecutionLedger
	}
	return cleanupExecutionDispositionEvidenceHash(
		prior, ledger.Disposition, ledger.ErrorClass, ledger.CompletedAt,
	), nil
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

func cleanupExecutionDispositionEvidenceHash(
	ledger CleanupExecutionLedger,
	disposition CleanupExecutionDisposition,
	errorClass CleanupExecutionErrorClass,
	completedAt time.Time,
) string {
	encoder := newArtifactBindingEncoder(cleanupExecutionDispositionEvidenceBindingVersion)
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
	encoder.addString(string(ledger.ErrorClass))
	encoder.addString(string(disposition))
	encoder.addString(string(errorClass))
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
	if cleanupExecutionTerminalDisposition(ledger.Disposition) {
		return validateCleanupExecutionDispositionShape(ledger, observedAt)
	}
	emptyTerminal := ledger.Disposition == "" && ledger.ErrorClass == "" &&
		ledger.EvidenceHash == "" && ledger.CompletedAt.IsZero()
	ambiguousOutcome := ledger.Disposition == "" &&
		validAmbiguousCleanupExecutionErrorClass(ledger.ErrorClass) &&
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
		if (!emptyTerminal && !(ambiguousOutcome && ledger.Raw.DeleteOutcome == CleanupDeleteUnknown)) ||
			!outcomeCleanupArtifactProgress(ledger.Raw, observedAt) ||
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
		if (!emptyTerminal && !(ambiguousOutcome && ledger.Manifest.DeleteOutcome == CleanupDeleteUnknown)) ||
			!auditedCleanupArtifactProgress(ledger.Raw, observedAt) ||
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

func validateCleanupExecutionDispositionShape(
	ledger CleanupExecutionLedger,
	observedAt time.Time,
) error {
	policy, err := CleanupExecutionFailurePolicyFor(ledger.ErrorClass)
	if err != nil || policy.Disposition != ledger.Disposition ||
		!cleanupExecutionDispositionPhaseAllowed(ledger.Phase) ||
		!isLowerHexDigest(ledger.EvidenceHash) || ledger.CompletedAt.IsZero() ||
		ledger.CompletedAt.After(observedAt) {
		return ErrInvalidCleanupExecutionLedger
	}
	prior, err := cleanupExecutionLedgerBeforeDisposition(ledger)
	if err != nil || validateCleanupExecutionPhaseShape(prior, ledger.CompletedAt) != nil ||
		ledger.EvidenceHash != cleanupExecutionDispositionEvidenceHash(
			prior, ledger.Disposition, ledger.ErrorClass, ledger.CompletedAt,
		) {
		return ErrInvalidCleanupExecutionLedger
	}
	return nil
}

func cleanupExecutionLedgerBeforeDisposition(
	ledger CleanupExecutionLedger,
) (CleanupExecutionLedger, error) {
	if !cleanupExecutionTerminalDisposition(ledger.Disposition) ||
		!cleanupExecutionDispositionPhaseAllowed(ledger.Phase) {
		return CleanupExecutionLedger{}, ErrInvalidCleanupExecutionLedger
	}
	prior := ledger
	prior.Disposition = ""
	prior.EvidenceHash = ""
	prior.CompletedAt = time.Time{}
	if !cleanupExecutionLedgerHasAmbiguousOutcome(prior) {
		prior.ErrorClass = ""
	}
	return prior, nil
}

func cleanupExecutionLedgerHasAmbiguousOutcome(ledger CleanupExecutionLedger) bool {
	switch ledger.Phase {
	case CleanupExecutionPhaseRawOutcomeRecorded:
		return ledger.Raw.DeleteOutcome == CleanupDeleteUnknown
	case CleanupExecutionPhaseManifestOutcomeRecorded:
		return ledger.Manifest.DeleteOutcome == CleanupDeleteUnknown
	default:
		return false
	}
}

func cleanupExecutionTerminalDisposition(disposition CleanupExecutionDisposition) bool {
	return disposition == CleanupExecutionDispositionRetry ||
		disposition == CleanupExecutionDispositionHold
}

func cleanupExecutionDispositionPhaseAllowed(phase CleanupExecutionPhase) bool {
	switch phase {
	case CleanupExecutionPhaseRawDispatchRecorded,
		CleanupExecutionPhaseRawOutcomeRecorded,
		CleanupExecutionPhaseManifestDispatchRecorded,
		CleanupExecutionPhaseManifestOutcomeRecorded:
		return true
	default:
		return false
	}
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

func validCleanupOutcomeErrorClass(
	outcome CleanupDeleteRPCOutcome,
	errorClass CleanupExecutionErrorClass,
) bool {
	if outcome == CleanupDeleteUnknown {
		return validAmbiguousCleanupExecutionErrorClass(errorClass)
	}
	return errorClass == ""
}

func validAmbiguousCleanupExecutionErrorClass(value CleanupExecutionErrorClass) bool {
	switch value {
	case CleanupExecutionErrorProviderTimeout,
		CleanupExecutionErrorProviderCancelled,
		CleanupExecutionErrorProviderUnavailable,
		CleanupExecutionErrorResponseUnverifiable:
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
