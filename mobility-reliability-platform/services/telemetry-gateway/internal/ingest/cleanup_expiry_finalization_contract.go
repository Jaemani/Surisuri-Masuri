package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	CleanupExpiryFinalizationOutcomePolicyVersion = "cleanup-expiry-finalization-outcome.current-state@1"
	CleanupExpiryFinalizationOutcomeGrantTTL      = 30 * time.Second
	// A successful cleanup must finish inside one non-renewable cleanup lease.
	// Keeping the explicit evidence-age gate equal to the maximum lease makes
	// the terminal rule independently reviewable instead of relying only on the
	// fence validator as an accidental freshness check.
	CleanupExpiryFinalizationEvidenceMaxAge = MaxLeaseDuration

	cleanupExpiryFinalizationQueryBindingVersion = "cleanup-expiry-finalization-query@1"
	cleanupExpiryFinalizationGrantVersion        = "cleanup-expiry-finalization-grant@1"
)

var (
	ErrInvalidCleanupExpiryFinalization            = errors.New("cleanup expiry finalization is invalid")
	ErrCleanupExpiryFinalizationConflict           = errors.New("cleanup expiry finalization conflicts with durable state")
	ErrCleanupExpiryFinalizationUnavailable        = errors.New("cleanup expiry finalization is unavailable")
	ErrInvalidCleanupExpiryFinalizationOutcome     = errors.New("cleanup expiry finalization outcome authorization is invalid")
	ErrCleanupExpiryFinalizationOutcomeExpired     = errors.New("cleanup expiry finalization outcome authorization has expired")
	ErrCleanupExpiryFinalizationOutcomeUnavailable = errors.New("cleanup expiry finalization outcome is unavailable")
)

// CleanupExpiryFinalizationOutcomeQuery binds only durable pre-state. It does
// not preselect CompletedAt: Firestore may retry a transaction callback with a
// later trusted effective time. Committed correlation recomputes the evidence
// hash and purge time from the stored terminal timestamp.
type CleanupExpiryFinalizationOutcomeQuery struct {
	TenantID                     string
	ReservationKey               string
	AttemptID                    string
	ExpectedTargetHash           string
	ExpectedPlanHash             string
	ExpectedFence                LeaseFence
	ExpectedPreReceiptRevision   int64
	ExpectedFinalReceiptRevision int64
	ExpectedPreLedgerRevision    int64
	ExpectedFinalLedgerRevision  int64
}

type CleanupExpiryFinalizationOutcomeReadGrant struct {
	policyVersion    string
	checkedAt        time.Time
	expiresAt        time.Time
	queryBindingHash [sha256.Size]byte
	capabilitySeal   [sha256.Size]byte
}

type CleanupExpiryFinalizationCommitStatus string

const (
	CleanupExpiryFinalizationCommitted    CleanupExpiryFinalizationCommitStatus = "committed"
	CleanupExpiryFinalizationNotCommitted CleanupExpiryFinalizationCommitStatus = "not_committed"
	CleanupExpiryFinalizationUnverifiable CleanupExpiryFinalizationCommitStatus = "unverifiable"
)

type CleanupExpiryFinalizationOutcome struct {
	AttemptID       string
	CommitStatus    CleanupExpiryFinalizationCommitStatus
	ReceiptState    ReceiptState
	ReceiptRevision int64
	LedgerPhase     CleanupExecutionPhase
	LedgerRevision  int64
	EvidenceHash    string
	CompletedAt     time.Time
	PurgeEligibleAt time.Time
}

type CleanupExpiryFinalizationResult struct {
	Receipt      Receipt
	Ledger       CleanupExecutionLedger
	OutcomeQuery CleanupExpiryFinalizationOutcomeQuery
}

type CurrentCleanupExpiryFinalizationAttempt struct {
	AttemptID      string
	TenantID       string
	ReceiptID      string
	OwnerKind      LeaseOwnerKind
	FencingToken   int64
	WorkerVersion  string
	Status         RecoveryAttemptStatus
	Outcome        RecoveryAttemptOutcome
	StartedAt      time.Time
	CompletedAt    time.Time
	FailureCode    RecoveryAttemptFailureCode
	FailedAt       time.Time
	ForeignResidue bool
	Ledger         CleanupExecutionLedger
}

// CurrentCleanupExpiryFinalizationSnapshot is server-internal. Outcome results
// never expose target paths, receipt identity fields or the immutable plan.
type CurrentCleanupExpiryFinalizationSnapshot struct {
	Receipt   Receipt
	Attempt   CurrentCleanupExpiryFinalizationAttempt
	Plan      CleanupExecutionLedgerPlan
	PlanValid bool
	ReadTime  time.Time
}

type CleanupExpiryFinalizationStore interface {
	FinalizeExpiredCleanup(
		context.Context,
		CleanupExecutionQuery,
	) (CleanupExpiryFinalizationResult, error)
}

type CleanupExpiryFinalizationOutcomeAuthorizationStore interface {
	LoadCurrentCleanupExpiryFinalizationOutcome(
		context.Context,
		CleanupExpiryFinalizationOutcomeQuery,
	) (CurrentCleanupExpiryFinalizationSnapshot, error)
}

type CleanupExpiryFinalizationOutcomeStore interface {
	GetCleanupExpiryFinalizationOutcome(
		context.Context,
		CleanupExpiryFinalizationOutcomeReadGrant,
		CleanupExpiryFinalizationOutcomeQuery,
		time.Time,
	) (CleanupExpiryFinalizationOutcome, error)
}

type SystemCleanupExpiryFinalizationOutcomeAuthorizer struct {
	store CleanupExpiryFinalizationOutcomeAuthorizationStore
	now   func() time.Time
}

func NewSystemCleanupExpiryFinalizationOutcomeAuthorizer(
	store CleanupExpiryFinalizationOutcomeAuthorizationStore,
	now func() time.Time,
) (*SystemCleanupExpiryFinalizationOutcomeAuthorizer, error) {
	if store == nil {
		return nil, errors.New("cleanup expiry finalization outcome authorization store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &SystemCleanupExpiryFinalizationOutcomeAuthorizer{store: store, now: now}, nil
}

// CompleteCleanupExecution builds the only success-path terminal ledger and
// its response-loss query. The caller must supply a trusted transaction time.
func CompleteCleanupExecution(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	receiptRetentionFloor time.Time,
	completedAt time.Time,
) (
	CleanupExecutionLedger,
	time.Time,
	CleanupExpiryFinalizationOutcomeQuery,
	error,
) {
	completedAt = completedAt.UTC()
	if completedAt.IsZero() || ledger.Phase != CleanupExecutionPhaseManifestAbsenceConfirmed ||
		ledger.Revision != cleanupExecutionPhaseRevision(CleanupExecutionPhaseManifestAbsenceConfirmed) ||
		ValidateCleanupExecutionLedger(plan, ledger, completedAt) != nil ||
		ledger.Raw.AuditOutcome != CleanupAuditConfirmedAbsent ||
		ledger.Manifest.AuditOutcome != CleanupAuditConfirmedAbsent ||
		ledger.Raw.DeleteOutcome == CleanupDeleteUnknown ||
		ledger.Manifest.DeleteOutcome == CleanupDeleteUnknown ||
		!cleanupFinalizationEvidenceFresh(ledger.Raw.AuditedAt, completedAt) ||
		!cleanupFinalizationEvidenceFresh(ledger.Manifest.AuditedAt, completedAt) {
		return CleanupExecutionLedger{}, time.Time{}, CleanupExpiryFinalizationOutcomeQuery{},
			ErrInvalidCleanupExpiryFinalization
	}
	if ledger.ReceiptRevision == int64(^uint64(0)>>1) {
		return CleanupExecutionLedger{}, time.Time{}, CleanupExpiryFinalizationOutcomeQuery{},
			ErrInvalidCleanupExpiryFinalization
	}
	query := CleanupExpiryFinalizationOutcomeQuery{
		TenantID: plan.Target.Command.TenantID, ReservationKey: plan.Target.Command.ReservationKey,
		AttemptID:          plan.Target.Command.AttemptID,
		ExpectedTargetHash: plan.Target.TargetHash, ExpectedPlanHash: plan.PlanHash,
		ExpectedFence:                ledger.Fence,
		ExpectedPreReceiptRevision:   ledger.ReceiptRevision,
		ExpectedFinalReceiptRevision: ledger.ReceiptRevision + 1,
		ExpectedPreLedgerRevision:    ledger.Revision,
		ExpectedFinalLedgerRevision:  ledger.Revision + 1,
	}
	if validateCleanupExpiryFinalizationOutcomeQuery(query) != nil {
		return CleanupExecutionLedger{}, time.Time{}, CleanupExpiryFinalizationOutcomeQuery{},
			ErrInvalidCleanupExpiryFinalization
	}
	completed, err := AdvanceCleanupExecutionLedger(plan, ledger, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseCompleted, ObservedAt: completedAt,
	})
	if err != nil || completed.Revision != query.ExpectedFinalLedgerRevision {
		return CleanupExecutionLedger{}, time.Time{}, CleanupExpiryFinalizationOutcomeQuery{},
			ErrInvalidCleanupExpiryFinalization
	}
	purgeEligibleAt, err := CleanupPurgeEligibleAt(receiptRetentionFloor, completedAt)
	if err != nil {
		return CleanupExecutionLedger{}, time.Time{}, CleanupExpiryFinalizationOutcomeQuery{},
			ErrInvalidCleanupExpiryFinalization
	}
	return completed, purgeEligibleAt, query, nil
}

// BuildCompletedCleanupExecutionLedgerPlan reconstructs the original plan
// without pretending a terminal receipt still has live authority. It first
// validates the exact expired shape, then restores only the immutable
// pre-finalization fields required to recompute the original plan hash.
func BuildCompletedCleanupExecutionLedgerPlan(
	query CleanupExecutionQuery,
	receipt Receipt,
	target CleanupTarget,
	completedAt time.Time,
) (CleanupExecutionLedgerPlan, error) {
	target, err := CloneCleanupTarget(target)
	completedAt = completedAt.UTC()
	if err != nil || ValidateCleanupExecutionQuery(query) != nil || completedAt.IsZero() ||
		target.Command.TenantID != query.TenantID ||
		target.Command.ReservationKey != query.ReservationKey ||
		target.Command.AttemptID != query.AttemptID ||
		target.Command.CleanupID != query.AttemptID ||
		receipt.TenantID != query.TenantID || receipt.ReservationKey != query.ReservationKey ||
		receipt.ReceiptID != target.Command.ReceiptID || receipt.State != ReceiptExpired ||
		target.Command.ReceiptRevision == int64(^uint64(0)>>1) ||
		receipt.Revision != target.Command.ReceiptRevision+1 ||
		receipt.FencingToken != target.Command.FencingToken ||
		receipt.LeaseOwnerID != "" || receipt.LeaseOwnerKind != "" ||
		!receipt.LeaseAcquiredAt.IsZero() || !receipt.LeaseHeartbeatAt.IsZero() ||
		!receipt.LeaseExpiresAt.IsZero() || !receipt.NextRecoveryAt.IsZero() ||
		receipt.LastRecoveryCode != "" || receipt.PurgeEligibleAt == nil ||
		!receipt.UpdatedAt.Equal(completedAt) || !completedAt.Before(target.Command.LeaseExpiresAt) ||
		receipt.CleanupMode != target.Command.Mode ||
		receipt.CleanupOriginStatus != target.Command.OriginStatus ||
		receipt.CleanupPolicyVersion != target.Command.CleanupPolicyVersion ||
		!receipt.CleanupTransitionedAt.Equal(target.Command.CleanupTransitionedAt) ||
		!receipt.CleanupQuiescenceUntil.Equal(target.Command.CleanupQuiescenceUntil) {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExpiryFinalization
	}
	expectedPurge, err := CleanupPurgeEligibleAt(receipt.ReceiptRetentionFloor, completedAt)
	if err != nil || !receipt.PurgeEligibleAt.Equal(expectedPurge) {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExpiryFinalization
	}
	historical := receipt
	historical.State = ReceiptCleanupPending
	historical.Revision = target.Command.ReceiptRevision
	historical.LeaseOwnerID = target.Command.AttemptID
	historical.LeaseOwnerKind = LeaseOwnerCleanup
	historical.LeaseAcquiredAt = target.Command.LeaseAcquiredAt.UTC()
	historical.LeaseHeartbeatAt = target.Command.LeaseHeartbeatAt.UTC()
	historical.LeaseExpiresAt = target.Command.LeaseExpiresAt.UTC()
	historical.NextRecoveryAt = time.Time{}
	historical.LastRecoveryCode = ""
	historical.UpdatedAt = target.Command.LeaseHeartbeatAt.UTC()
	historical.PurgeEligibleAt = nil
	plan, err := buildCleanupExecutionLedgerPlanFromTarget(query, historical, target)
	if err != nil {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExpiryFinalization
	}
	return plan, nil
}

func CleanupExpiryFinalizationOutcomeQueryForLedger(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
) (CleanupExpiryFinalizationOutcomeQuery, error) {
	observedAt := cleanupExecutionLedgerLatestTime(plan, ledger)
	if observedAt.IsZero() || ledger.Phase != CleanupExecutionPhaseManifestAbsenceConfirmed ||
		ValidateCleanupExecutionLedger(plan, ledger, observedAt) != nil ||
		ledger.ReceiptRevision == int64(^uint64(0)>>1) {
		return CleanupExpiryFinalizationOutcomeQuery{}, ErrInvalidCleanupExpiryFinalizationOutcome
	}
	query := CleanupExpiryFinalizationOutcomeQuery{
		TenantID: plan.Target.Command.TenantID, ReservationKey: plan.Target.Command.ReservationKey,
		AttemptID:          plan.Target.Command.AttemptID,
		ExpectedTargetHash: plan.Target.TargetHash, ExpectedPlanHash: plan.PlanHash,
		ExpectedFence:                ledger.Fence,
		ExpectedPreReceiptRevision:   ledger.ReceiptRevision,
		ExpectedFinalReceiptRevision: ledger.ReceiptRevision + 1,
		ExpectedPreLedgerRevision:    ledger.Revision,
		ExpectedFinalLedgerRevision:  ledger.Revision + 1,
	}
	if validateCleanupExpiryFinalizationOutcomeQuery(query) != nil {
		return CleanupExpiryFinalizationOutcomeQuery{}, ErrInvalidCleanupExpiryFinalizationOutcome
	}
	return query, nil
}

func ValidateCleanupExpiryFinalizationOutcomeQuery(
	query CleanupExpiryFinalizationOutcomeQuery,
) error {
	return validateCleanupExpiryFinalizationOutcomeQuery(query)
}

func (a *SystemCleanupExpiryFinalizationOutcomeAuthorizer) Authorize(
	ctx context.Context,
	query CleanupExpiryFinalizationOutcomeQuery,
) (CleanupExpiryFinalizationOutcomeReadGrant, error) {
	if a == nil || a.store == nil || a.now == nil || ctx == nil ||
		validateCleanupExpiryFinalizationOutcomeQuery(query) != nil {
		return CleanupExpiryFinalizationOutcomeReadGrant{}, ErrInvalidCleanupExpiryFinalizationOutcome
	}
	snapshot, err := a.store.LoadCurrentCleanupExpiryFinalizationOutcome(ctx, query)
	if err != nil {
		return CleanupExpiryFinalizationOutcomeReadGrant{}, err
	}
	checkedAt, err := forwardRecoveryAuthorizationTime(a.now().UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return CleanupExpiryFinalizationOutcomeReadGrant{}, ErrCleanupExpiryFinalizationOutcomeUnavailable
	}
	if _, err := EvaluateCleanupExpiryFinalizationOutcome(query, snapshot, checkedAt); err != nil {
		return CleanupExpiryFinalizationOutcomeReadGrant{}, err
	}
	grant := CleanupExpiryFinalizationOutcomeReadGrant{
		policyVersion: CleanupExpiryFinalizationOutcomePolicyVersion,
		checkedAt:     checkedAt, expiresAt: checkedAt.Add(CleanupExpiryFinalizationOutcomeGrantTTL),
		queryBindingHash: canonicalCleanupExpiryFinalizationOutcomeQueryBinding(query),
	}
	grant.capabilitySeal = cleanupExpiryFinalizationOutcomeCapabilitySeal(grant)
	return grant, nil
}

func ValidateCleanupExpiryFinalizationOutcomeAuthorization(
	grant CleanupExpiryFinalizationOutcomeReadGrant,
	query CleanupExpiryFinalizationOutcomeQuery,
	observedAt time.Time,
) error {
	if validateCleanupExpiryFinalizationOutcomeQuery(query) != nil || observedAt.IsZero() ||
		grant.policyVersion != CleanupExpiryFinalizationOutcomePolicyVersion ||
		grant.checkedAt.IsZero() || grant.expiresAt.IsZero() ||
		!grant.checkedAt.Before(grant.expiresAt) ||
		grant.queryBindingHash != canonicalCleanupExpiryFinalizationOutcomeQueryBinding(query) ||
		grant.capabilitySeal != cleanupExpiryFinalizationOutcomeCapabilitySeal(grant) ||
		observedAt.Before(grant.checkedAt) {
		return ErrInvalidCleanupExpiryFinalizationOutcome
	}
	if !observedAt.Before(grant.expiresAt) {
		return ErrCleanupExpiryFinalizationOutcomeExpired
	}
	return nil
}

func CleanupExpiryFinalizationOutcomeAuthorizationDeadline(
	grant CleanupExpiryFinalizationOutcomeReadGrant,
	query CleanupExpiryFinalizationOutcomeQuery,
) (time.Time, error) {
	if validateCleanupExpiryFinalizationOutcomeQuery(query) != nil ||
		grant.policyVersion != CleanupExpiryFinalizationOutcomePolicyVersion ||
		grant.checkedAt.IsZero() || grant.expiresAt.IsZero() ||
		!grant.checkedAt.Before(grant.expiresAt) ||
		grant.queryBindingHash != canonicalCleanupExpiryFinalizationOutcomeQueryBinding(query) ||
		grant.capabilitySeal != cleanupExpiryFinalizationOutcomeCapabilitySeal(grant) {
		return time.Time{}, ErrInvalidCleanupExpiryFinalizationOutcome
	}
	return grant.expiresAt, nil
}

func EvaluateCleanupExpiryFinalizationOutcome(
	query CleanupExpiryFinalizationOutcomeQuery,
	snapshot CurrentCleanupExpiryFinalizationSnapshot,
	observedAt time.Time,
) (CleanupExpiryFinalizationOutcome, error) {
	_, clockErr := forwardRecoveryAuthorizationTime(observedAt.UTC(), snapshot.ReadTime.UTC())
	if validateCleanupExpiryFinalizationOutcomeQuery(query) != nil || observedAt.IsZero() ||
		snapshot.ReadTime.IsZero() || snapshot.Receipt.TenantID != query.TenantID ||
		snapshot.Receipt.ReservationKey != query.ReservationKey ||
		snapshot.Attempt.AttemptID != query.AttemptID ||
		snapshot.Attempt.TenantID != query.TenantID ||
		snapshot.Attempt.ReceiptID != snapshot.Receipt.ReceiptID ||
		observedAt.Before(snapshot.ReadTime) || clockErr != nil {
		return CleanupExpiryFinalizationOutcome{}, ErrCleanupExpiryFinalizationOutcomeUnavailable
	}
	result := CleanupExpiryFinalizationOutcome{
		AttemptID: query.AttemptID, ReceiptState: snapshot.Receipt.State,
		ReceiptRevision: snapshot.Receipt.Revision,
		LedgerPhase:     snapshot.Attempt.Ledger.Phase,
		LedgerRevision:  snapshot.Attempt.Ledger.Revision,
	}
	if cleanupExpiryFinalizationPreStateMatches(query, snapshot) {
		result.CommitStatus = CleanupExpiryFinalizationNotCommitted
		return result, nil
	}
	if cleanupExpiryFinalizationCommittedStateMatches(query, snapshot) {
		result.CommitStatus = CleanupExpiryFinalizationCommitted
		result.EvidenceHash = snapshot.Attempt.Ledger.EvidenceHash
		result.CompletedAt = snapshot.Attempt.Ledger.CompletedAt.UTC()
		result.PurgeEligibleAt = snapshot.Receipt.PurgeEligibleAt.UTC()
		return result, nil
	}
	result.CommitStatus = CleanupExpiryFinalizationUnverifiable
	return result, nil
}

func cleanupExpiryFinalizationPreStateMatches(
	query CleanupExpiryFinalizationOutcomeQuery,
	snapshot CurrentCleanupExpiryFinalizationSnapshot,
) bool {
	receipt := snapshot.Receipt
	attempt := snapshot.Attempt
	ledger := attempt.Ledger
	if !snapshot.PlanValid || ValidateCleanupExecutionLedgerPlan(snapshot.Plan) != nil ||
		snapshot.Plan.Target.TargetHash != query.ExpectedTargetHash ||
		snapshot.Plan.PlanHash != query.ExpectedPlanHash ||
		receipt.State != ReceiptCleanupPending ||
		receipt.Revision != query.ExpectedPreReceiptRevision || receipt.PurgeEligibleAt != nil ||
		receipt.LeaseOwnerID != query.ExpectedFence.OwnerID ||
		receipt.LeaseOwnerKind != LeaseOwnerCleanup ||
		receipt.FencingToken != query.ExpectedFence.Token ||
		!receipt.LeaseExpiresAt.Equal(query.ExpectedFence.ExpiresAt) ||
		attempt.ForeignResidue || attempt.OwnerKind != LeaseOwnerCleanup ||
		attempt.FencingToken != query.ExpectedFence.Token ||
		attempt.WorkerVersion != CleanupWorkerVersion ||
		attempt.Status != RecoveryAttemptStarted || attempt.Outcome != "" ||
		!attempt.CompletedAt.IsZero() || attempt.FailureCode != "" || !attempt.FailedAt.IsZero() ||
		ledger.Phase != CleanupExecutionPhaseManifestAbsenceConfirmed ||
		ledger.Revision != query.ExpectedPreLedgerRevision ||
		ledger.TargetHash != query.ExpectedTargetHash || ledger.PlanHash != query.ExpectedPlanHash ||
		ledger.ReceiptRevision != query.ExpectedPreReceiptRevision || ledger.Fence != query.ExpectedFence {
		return false
	}
	observedAt := cleanupExecutionLedgerLatestTime(snapshot.Plan, ledger)
	return !observedAt.IsZero() && ValidateCleanupExecutionLedger(snapshot.Plan, ledger, observedAt) == nil
}

func cleanupExpiryFinalizationCommittedStateMatches(
	query CleanupExpiryFinalizationOutcomeQuery,
	snapshot CurrentCleanupExpiryFinalizationSnapshot,
) bool {
	receipt := snapshot.Receipt
	attempt := snapshot.Attempt
	ledger := attempt.Ledger
	if !snapshot.PlanValid || ValidateCleanupExecutionLedgerPlan(snapshot.Plan) != nil ||
		snapshot.Plan.Target.TargetHash != query.ExpectedTargetHash ||
		snapshot.Plan.PlanHash != query.ExpectedPlanHash ||
		receipt.State != ReceiptExpired ||
		receipt.Revision != query.ExpectedFinalReceiptRevision || receipt.PurgeEligibleAt == nil ||
		receipt.LeaseOwnerID != "" || receipt.LeaseOwnerKind != "" ||
		!receipt.LeaseAcquiredAt.IsZero() || !receipt.LeaseHeartbeatAt.IsZero() ||
		!receipt.LeaseExpiresAt.IsZero() || !receipt.NextRecoveryAt.IsZero() ||
		attempt.ForeignResidue || attempt.OwnerKind != LeaseOwnerCleanup ||
		attempt.FencingToken != query.ExpectedFence.Token ||
		attempt.WorkerVersion != CleanupWorkerVersion ||
		attempt.Status != RecoveryAttemptCompleted ||
		attempt.Outcome != RecoveryAttemptOutcomeExpired ||
		attempt.CompletedAt.IsZero() || !attempt.CompletedAt.Equal(receipt.UpdatedAt) ||
		attempt.FailureCode != "" || !attempt.FailedAt.IsZero() ||
		ledger.Phase != CleanupExecutionPhaseCompleted ||
		ledger.Revision != query.ExpectedFinalLedgerRevision ||
		ledger.TargetHash != query.ExpectedTargetHash || ledger.PlanHash != query.ExpectedPlanHash ||
		ledger.ReceiptRevision != query.ExpectedPreReceiptRevision || ledger.Fence != query.ExpectedFence ||
		!ledger.CompletedAt.Equal(attempt.CompletedAt) ||
		ValidateCleanupExecutionLedger(snapshot.Plan, ledger, ledger.CompletedAt) != nil ||
		!cleanupFinalizationEvidenceFresh(ledger.Raw.AuditedAt, ledger.CompletedAt) ||
		!cleanupFinalizationEvidenceFresh(ledger.Manifest.AuditedAt, ledger.CompletedAt) {
		return false
	}
	expectedEvidence, err := CleanupExecutionEvidenceHash(snapshot.Plan, ledger)
	if err != nil || expectedEvidence != ledger.EvidenceHash {
		return false
	}
	expectedPurge, err := CleanupPurgeEligibleAt(receipt.ReceiptRetentionFloor, ledger.CompletedAt)
	return err == nil && receipt.PurgeEligibleAt.Equal(expectedPurge)
}

func validateCleanupExpiryFinalizationOutcomeQuery(
	query CleanupExpiryFinalizationOutcomeQuery,
) error {
	if !telemetry.IsUUID(query.TenantID) || !isLowerHexDigest(query.ReservationKey) ||
		!telemetry.IsUUID(query.AttemptID) || !isLowerHexDigest(query.ExpectedTargetHash) ||
		!isLowerHexDigest(query.ExpectedPlanHash) || ValidateLeaseFence(query.ExpectedFence) != nil ||
		query.AttemptID != query.ExpectedFence.OwnerID ||
		query.ExpectedPreReceiptRevision <= 0 ||
		query.ExpectedPreReceiptRevision == int64(^uint64(0)>>1) ||
		query.ExpectedFinalReceiptRevision <= 0 ||
		query.ExpectedFinalReceiptRevision != query.ExpectedPreReceiptRevision+1 ||
		query.ExpectedPreLedgerRevision != cleanupExecutionPhaseRevision(
			CleanupExecutionPhaseManifestAbsenceConfirmed,
		) || query.ExpectedFinalLedgerRevision != cleanupExecutionPhaseRevision(
		CleanupExecutionPhaseCompleted,
	) {
		return ErrInvalidCleanupExpiryFinalizationOutcome
	}
	return nil
}

func cleanupFinalizationEvidenceFresh(auditedAt, completedAt time.Time) bool {
	auditedAt = auditedAt.UTC()
	completedAt = completedAt.UTC()
	return !auditedAt.IsZero() && !completedAt.IsZero() && !auditedAt.After(completedAt) &&
		completedAt.Sub(auditedAt) <= CleanupExpiryFinalizationEvidenceMaxAge
}

func canonicalCleanupExpiryFinalizationOutcomeQueryBinding(
	query CleanupExpiryFinalizationOutcomeQuery,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(cleanupExpiryFinalizationQueryBindingVersion)
	encoder.addString(query.TenantID)
	encoder.addString(query.ReservationKey)
	encoder.addString(query.AttemptID)
	encoder.addString(query.ExpectedTargetHash)
	encoder.addString(query.ExpectedPlanHash)
	encoder.addLeaseFence(&query.ExpectedFence)
	encoder.addInt64(query.ExpectedPreReceiptRevision)
	encoder.addInt64(query.ExpectedFinalReceiptRevision)
	encoder.addInt64(query.ExpectedPreLedgerRevision)
	encoder.addInt64(query.ExpectedFinalLedgerRevision)
	return encoder.sum()
}

func cleanupExpiryFinalizationOutcomeCapabilitySeal(
	grant CleanupExpiryFinalizationOutcomeReadGrant,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(cleanupExpiryFinalizationGrantVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addBytes(grant.queryBindingHash[:])
	return encoder.sum()
}
