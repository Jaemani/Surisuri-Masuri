package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"
)

const (
	CleanupExecutionDispositionOutcomePolicyVersion = "cleanup-execution-disposition-outcome.current-state@1"
	CleanupExecutionDispositionOutcomeGrantTTL      = 30 * time.Second

	cleanupExecutionDispositionQueryBindingVersion = "cleanup-execution-disposition-query@1"
	cleanupExecutionDispositionGrantVersion        = "cleanup-execution-disposition-grant@1"
)

var (
	ErrInvalidCleanupExecutionDisposition            = errors.New("cleanup execution disposition is invalid")
	ErrCleanupExecutionDispositionConflict           = errors.New("cleanup execution disposition conflicts with durable state")
	ErrCleanupExecutionDispositionUnavailable        = errors.New("cleanup execution disposition is unavailable")
	ErrInvalidCleanupExecutionDispositionOutcome     = errors.New("cleanup execution disposition outcome authorization is invalid")
	ErrCleanupExecutionDispositionOutcomeExpired     = errors.New("cleanup execution disposition outcome authorization has expired")
	ErrCleanupExecutionDispositionOutcomeUnavailable = errors.New("cleanup execution disposition outcome is unavailable")
)

// CleanupExecutionDispositionCommand binds a terminal retry/hold decision to
// one exact active ledger revision. It contains only bounded control data.
type CleanupExecutionDispositionCommand struct {
	Query                   CleanupExecutionQuery
	ExpectedTargetHash      string
	ExpectedPlanHash        string
	ExpectedReceiptRevision int64
	ExpectedLedgerRevision  int64
	ExpectedPhase           CleanupExecutionPhase
	ErrorClass              CleanupExecutionErrorClass
}

// CleanupExecutionDispositionOutcomeQuery seals pre-state only. CompletedAt
// remains transaction-derived so a Firestore callback retry may select a later
// coherent trusted time without invalidating response-loss correlation.
type CleanupExecutionDispositionOutcomeQuery struct {
	TenantID                     string
	ReservationKey               string
	AttemptID                    string
	ExpectedTargetHash           string
	ExpectedPlanHash             string
	ExpectedFence                LeaseFence
	ExpectedPreReceiptRevision   int64
	ExpectedFinalReceiptRevision int64
	ExpectedLedgerRevision       int64
	ExpectedPhase                CleanupExecutionPhase
	ExpectedDisposition          CleanupExecutionDisposition
	ExpectedErrorClass           CleanupExecutionErrorClass
}

type CleanupExecutionDispositionResult struct {
	Receipt         Receipt
	Ledger          CleanupExecutionLedger
	NextCleanupAt   time.Time
	HoldReviewDueAt time.Time
	OutcomeQuery    CleanupExecutionDispositionOutcomeQuery
}

type CleanupExecutionDispositionCommitStatus string

const (
	CleanupExecutionDispositionCommitted    CleanupExecutionDispositionCommitStatus = "committed"
	CleanupExecutionDispositionNotCommitted CleanupExecutionDispositionCommitStatus = "not_committed"
	CleanupExecutionDispositionUnverifiable CleanupExecutionDispositionCommitStatus = "unverifiable"
)

type CleanupExecutionDispositionOutcome struct {
	AttemptID       string
	CommitStatus    CleanupExecutionDispositionCommitStatus
	ReceiptRevision int64
	LedgerPhase     CleanupExecutionPhase
	LedgerRevision  int64
	Disposition     CleanupExecutionDisposition
	ErrorClass      CleanupExecutionErrorClass
	EvidenceHash    string
	CompletedAt     time.Time
	NextCleanupAt   time.Time
	HoldReviewDueAt time.Time
}

type CleanupExecutionDispositionOutcomeReadGrant struct {
	policyVersion    string
	checkedAt        time.Time
	expiresAt        time.Time
	queryBindingHash [sha256.Size]byte
	capabilitySeal   [sha256.Size]byte
}

type CurrentCleanupExecutionDispositionAttempt struct {
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

type CurrentCleanupExecutionDispositionSnapshot struct {
	Receipt   Receipt
	Attempt   CurrentCleanupExecutionDispositionAttempt
	Plan      CleanupExecutionLedgerPlan
	PlanValid bool
	ReadTime  time.Time
}

type CleanupExecutionDispositionStore interface {
	DisposeCleanupExecution(
		context.Context,
		CleanupExecutionDispositionCommand,
	) (CleanupExecutionDispositionResult, error)
}

type CleanupExecutionDispositionOutcomeAuthorizationStore interface {
	LoadCurrentCleanupExecutionDispositionOutcome(
		context.Context,
		CleanupExecutionDispositionOutcomeQuery,
	) (CurrentCleanupExecutionDispositionSnapshot, error)
}

type CleanupExecutionDispositionOutcomeStore interface {
	GetCleanupExecutionDispositionOutcome(
		context.Context,
		CleanupExecutionDispositionOutcomeReadGrant,
		CleanupExecutionDispositionOutcomeQuery,
		time.Time,
	) (CleanupExecutionDispositionOutcome, error)
}

type SystemCleanupExecutionDispositionOutcomeAuthorizer struct {
	store CleanupExecutionDispositionOutcomeAuthorizationStore
	now   func() time.Time
}

func NewSystemCleanupExecutionDispositionOutcomeAuthorizer(
	store CleanupExecutionDispositionOutcomeAuthorizationStore,
	now func() time.Time,
) (*SystemCleanupExecutionDispositionOutcomeAuthorizer, error) {
	if store == nil {
		return nil, errors.New("cleanup execution disposition outcome authorization store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &SystemCleanupExecutionDispositionOutcomeAuthorizer{store: store, now: now}, nil
}

func BuildCleanupExecutionDispositionCommand(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	errorClass CleanupExecutionErrorClass,
) (CleanupExecutionDispositionCommand, error) {
	observedAt := cleanupExecutionLedgerLatestTime(plan, ledger)
	command := CleanupExecutionDispositionCommand{
		Query: CleanupExecutionQuery{
			TenantID: plan.Target.Command.TenantID, ReservationKey: plan.Target.Command.ReservationKey,
			AttemptID: plan.Target.Command.AttemptID,
		},
		ExpectedTargetHash:      plan.Target.TargetHash,
		ExpectedPlanHash:        plan.PlanHash,
		ExpectedReceiptRevision: ledger.ReceiptRevision,
		ExpectedLedgerRevision:  ledger.Revision,
		ExpectedPhase:           ledger.Phase,
		ErrorClass:              errorClass,
	}
	if observedAt.IsZero() || ValidateCleanupExecutionLedger(plan, ledger, observedAt) != nil ||
		ValidateCleanupExecutionDispositionCommand(command) != nil {
		return CleanupExecutionDispositionCommand{}, ErrInvalidCleanupExecutionDisposition
	}
	if cleanupExecutionLedgerHasAmbiguousOutcome(ledger) && ledger.ErrorClass != errorClass {
		return CleanupExecutionDispositionCommand{}, ErrInvalidCleanupExecutionDisposition
	}
	return command, nil
}

func ValidateCleanupExecutionDispositionCommand(
	command CleanupExecutionDispositionCommand,
) error {
	if ValidateCleanupExecutionQuery(command.Query) != nil ||
		!isLowerHexDigest(command.ExpectedTargetHash) ||
		!isLowerHexDigest(command.ExpectedPlanHash) ||
		command.ExpectedReceiptRevision <= 0 ||
		command.ExpectedReceiptRevision == int64(^uint64(0)>>1) ||
		!cleanupExecutionDispositionPhaseAllowed(command.ExpectedPhase) ||
		command.ExpectedLedgerRevision != cleanupExecutionPhaseRevision(command.ExpectedPhase) {
		return ErrInvalidCleanupExecutionDisposition
	}
	if _, err := CleanupExecutionFailurePolicyFor(command.ErrorClass); err != nil {
		return ErrInvalidCleanupExecutionDisposition
	}
	return nil
}

func CleanupExecutionDispositionOutcomeQueryForLedger(
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	errorClass CleanupExecutionErrorClass,
) (CleanupExecutionDispositionOutcomeQuery, error) {
	command, err := BuildCleanupExecutionDispositionCommand(plan, ledger, errorClass)
	policy, policyErr := CleanupExecutionFailurePolicyFor(errorClass)
	if err != nil || policyErr != nil {
		return CleanupExecutionDispositionOutcomeQuery{}, ErrInvalidCleanupExecutionDisposition
	}
	query := CleanupExecutionDispositionOutcomeQuery{
		TenantID: command.Query.TenantID, ReservationKey: command.Query.ReservationKey,
		AttemptID:          command.Query.AttemptID,
		ExpectedTargetHash: command.ExpectedTargetHash, ExpectedPlanHash: command.ExpectedPlanHash,
		ExpectedFence: ledger.Fence, ExpectedPreReceiptRevision: ledger.ReceiptRevision,
		ExpectedFinalReceiptRevision: ledger.ReceiptRevision + 1,
		ExpectedLedgerRevision:       ledger.Revision, ExpectedPhase: ledger.Phase,
		ExpectedDisposition: policy.Disposition, ExpectedErrorClass: errorClass,
	}
	if ValidateCleanupExecutionDispositionOutcomeQuery(query) != nil {
		return CleanupExecutionDispositionOutcomeQuery{}, ErrInvalidCleanupExecutionDisposition
	}
	return query, nil
}

func ValidateCleanupExecutionDispositionOutcomeQuery(
	query CleanupExecutionDispositionOutcomeQuery,
) error {
	command := CleanupExecutionDispositionCommand{
		Query: CleanupExecutionQuery{
			TenantID: query.TenantID, ReservationKey: query.ReservationKey, AttemptID: query.AttemptID,
		},
		ExpectedTargetHash: query.ExpectedTargetHash, ExpectedPlanHash: query.ExpectedPlanHash,
		ExpectedReceiptRevision: query.ExpectedPreReceiptRevision,
		ExpectedLedgerRevision:  query.ExpectedLedgerRevision,
		ExpectedPhase:           query.ExpectedPhase,
		ErrorClass:              query.ExpectedErrorClass,
	}
	policy, err := CleanupExecutionFailurePolicyFor(query.ExpectedErrorClass)
	if ValidateCleanupExecutionDispositionCommand(command) != nil || err != nil ||
		ValidateLeaseFence(query.ExpectedFence) != nil || query.ExpectedFence.OwnerID != query.AttemptID ||
		query.ExpectedFinalReceiptRevision != query.ExpectedPreReceiptRevision+1 ||
		query.ExpectedFinalReceiptRevision <= query.ExpectedPreReceiptRevision ||
		policy.Disposition != query.ExpectedDisposition {
		return ErrInvalidCleanupExecutionDisposition
	}
	return nil
}

// BuildDisposedCleanupExecutionLedgerPlan reconstructs only historical plan
// binding from a no-lease retry/hold receipt. It never grants provider I/O.
func BuildDisposedCleanupExecutionLedgerPlan(
	query CleanupExecutionQuery,
	receipt Receipt,
	target CleanupTarget,
	completedAt time.Time,
) (CleanupExecutionLedgerPlan, error) {
	target, err := CloneCleanupTarget(target)
	completedAt = completedAt.UTC()
	if err != nil || ValidateCleanupExecutionQuery(query) != nil || completedAt.IsZero() ||
		target.Command.Status != CleanupTargetStatusPlanned ||
		target.Command.TenantID != query.TenantID ||
		target.Command.ReservationKey != query.ReservationKey ||
		target.Command.AttemptID != query.AttemptID || target.Command.CleanupID != query.AttemptID ||
		receipt.TenantID != query.TenantID || receipt.ReservationKey != query.ReservationKey ||
		receipt.ReceiptID != target.Command.ReceiptID || receipt.State != ReceiptCleanupPending ||
		target.Command.ReceiptRevision == int64(^uint64(0)>>1) ||
		receipt.Revision != target.Command.ReceiptRevision+1 ||
		receipt.FencingToken != target.Command.FencingToken ||
		receipt.LeaseOwnerID != "" || receipt.LeaseOwnerKind != "" ||
		!receipt.LeaseAcquiredAt.IsZero() || !receipt.LeaseHeartbeatAt.IsZero() ||
		!receipt.LeaseExpiresAt.IsZero() || !receipt.NextRecoveryAt.IsZero() ||
		receipt.LastRecoveryCode != "" || receipt.PurgeEligibleAt != nil ||
		receipt.CleanupDispositionAttemptID != query.AttemptID ||
		!receipt.UpdatedAt.Equal(completedAt) ||
		receipt.CleanupMode != target.Command.Mode ||
		receipt.CleanupOriginStatus != target.Command.OriginStatus ||
		receipt.CleanupPolicyVersion != target.Command.CleanupPolicyVersion ||
		!receipt.CleanupTransitionedAt.Equal(target.Command.CleanupTransitionedAt) ||
		!receipt.CleanupQuiescenceUntil.Equal(target.Command.CleanupQuiescenceUntil) {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionDispositionOutcome
	}
	policy, policyErr := CleanupExecutionFailurePolicyFor(receipt.LastCleanupErrorClass)
	nextCleanupAt, holdReviewDueAt, cursorErr := CleanupExecutionDispositionCursorAt(
		cleanupExecutionTargetLease(target).Lease.Fence,
		receipt.LastCleanupErrorClass,
		completedAt,
	)
	if policyErr != nil || cursorErr != nil || policy.Disposition != receipt.CleanupControlDisposition ||
		!receipt.NextCleanupAt.Equal(nextCleanupAt) ||
		!receipt.CleanupHoldReviewDueAt.Equal(holdReviewDueAt) {
		return CleanupExecutionLedgerPlan{}, ErrInvalidCleanupExecutionDispositionOutcome
	}
	historical := receipt
	historical.Revision = target.Command.ReceiptRevision
	historical.LeaseOwnerID = target.Command.AttemptID
	historical.LeaseOwnerKind = LeaseOwnerCleanup
	historical.LeaseAcquiredAt = target.Command.LeaseAcquiredAt.UTC()
	historical.LeaseHeartbeatAt = target.Command.LeaseHeartbeatAt.UTC()
	historical.LeaseExpiresAt = target.Command.LeaseExpiresAt.UTC()
	historical.CleanupDispositionAttemptID = ""
	historical.CleanupControlDisposition = ""
	historical.LastCleanupErrorClass = ""
	historical.NextCleanupAt = time.Time{}
	historical.CleanupHoldReviewDueAt = time.Time{}
	historical.UpdatedAt = target.Command.LeaseHeartbeatAt.UTC()
	return buildCleanupExecutionLedgerPlanFromTarget(query, historical, target)
}

func (a *SystemCleanupExecutionDispositionOutcomeAuthorizer) Authorize(
	ctx context.Context,
	query CleanupExecutionDispositionOutcomeQuery,
) (CleanupExecutionDispositionOutcomeReadGrant, error) {
	if a == nil || a.store == nil || a.now == nil || ctx == nil ||
		ValidateCleanupExecutionDispositionOutcomeQuery(query) != nil {
		return CleanupExecutionDispositionOutcomeReadGrant{},
			ErrInvalidCleanupExecutionDispositionOutcome
	}
	snapshot, err := a.store.LoadCurrentCleanupExecutionDispositionOutcome(ctx, query)
	if err != nil {
		return CleanupExecutionDispositionOutcomeReadGrant{}, err
	}
	checkedAt, err := forwardRecoveryAuthorizationTime(a.now().UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return CleanupExecutionDispositionOutcomeReadGrant{},
			ErrCleanupExecutionDispositionOutcomeUnavailable
	}
	if _, err := EvaluateCleanupExecutionDispositionOutcome(query, snapshot, checkedAt); err != nil {
		return CleanupExecutionDispositionOutcomeReadGrant{}, err
	}
	grant := CleanupExecutionDispositionOutcomeReadGrant{
		policyVersion: CleanupExecutionDispositionOutcomePolicyVersion,
		checkedAt:     checkedAt, expiresAt: checkedAt.Add(CleanupExecutionDispositionOutcomeGrantTTL),
		queryBindingHash: canonicalCleanupExecutionDispositionOutcomeQueryBinding(query),
	}
	grant.capabilitySeal = cleanupExecutionDispositionOutcomeCapabilitySeal(grant)
	return grant, nil
}

func ValidateCleanupExecutionDispositionOutcomeAuthorization(
	grant CleanupExecutionDispositionOutcomeReadGrant,
	query CleanupExecutionDispositionOutcomeQuery,
	observedAt time.Time,
) error {
	if ValidateCleanupExecutionDispositionOutcomeQuery(query) != nil || observedAt.IsZero() ||
		grant.policyVersion != CleanupExecutionDispositionOutcomePolicyVersion ||
		grant.checkedAt.IsZero() || grant.expiresAt.IsZero() ||
		!grant.checkedAt.Before(grant.expiresAt) ||
		grant.queryBindingHash != canonicalCleanupExecutionDispositionOutcomeQueryBinding(query) ||
		grant.capabilitySeal != cleanupExecutionDispositionOutcomeCapabilitySeal(grant) ||
		observedAt.Before(grant.checkedAt) {
		return ErrInvalidCleanupExecutionDispositionOutcome
	}
	if !observedAt.Before(grant.expiresAt) {
		return ErrCleanupExecutionDispositionOutcomeExpired
	}
	return nil
}

func CleanupExecutionDispositionOutcomeAuthorizationDeadline(
	grant CleanupExecutionDispositionOutcomeReadGrant,
	query CleanupExecutionDispositionOutcomeQuery,
) (time.Time, error) {
	if ValidateCleanupExecutionDispositionOutcomeQuery(query) != nil ||
		grant.policyVersion != CleanupExecutionDispositionOutcomePolicyVersion ||
		grant.checkedAt.IsZero() || grant.expiresAt.IsZero() ||
		!grant.checkedAt.Before(grant.expiresAt) ||
		grant.queryBindingHash != canonicalCleanupExecutionDispositionOutcomeQueryBinding(query) ||
		grant.capabilitySeal != cleanupExecutionDispositionOutcomeCapabilitySeal(grant) {
		return time.Time{}, ErrInvalidCleanupExecutionDispositionOutcome
	}
	return grant.expiresAt, nil
}

func EvaluateCleanupExecutionDispositionOutcome(
	query CleanupExecutionDispositionOutcomeQuery,
	snapshot CurrentCleanupExecutionDispositionSnapshot,
	observedAt time.Time,
) (CleanupExecutionDispositionOutcome, error) {
	_, clockErr := forwardRecoveryAuthorizationTime(observedAt.UTC(), snapshot.ReadTime.UTC())
	if ValidateCleanupExecutionDispositionOutcomeQuery(query) != nil || observedAt.IsZero() ||
		snapshot.ReadTime.IsZero() || snapshot.Receipt.TenantID != query.TenantID ||
		snapshot.Receipt.ReservationKey != query.ReservationKey ||
		snapshot.Attempt.AttemptID != query.AttemptID ||
		snapshot.Attempt.TenantID != query.TenantID ||
		snapshot.Attempt.ReceiptID != snapshot.Receipt.ReceiptID ||
		observedAt.Before(snapshot.ReadTime) || clockErr != nil {
		return CleanupExecutionDispositionOutcome{},
			ErrCleanupExecutionDispositionOutcomeUnavailable
	}
	result := CleanupExecutionDispositionOutcome{
		AttemptID: query.AttemptID, ReceiptRevision: snapshot.Receipt.Revision,
		LedgerPhase: snapshot.Attempt.Ledger.Phase, LedgerRevision: snapshot.Attempt.Ledger.Revision,
	}
	if cleanupExecutionDispositionPreStateMatches(query, snapshot) {
		result.CommitStatus = CleanupExecutionDispositionNotCommitted
		return result, nil
	}
	if cleanupExecutionDispositionCommittedStateMatches(query, snapshot) {
		ledger := snapshot.Attempt.Ledger
		result.CommitStatus = CleanupExecutionDispositionCommitted
		result.Disposition = ledger.Disposition
		result.ErrorClass = ledger.ErrorClass
		result.EvidenceHash = ledger.EvidenceHash
		result.CompletedAt = ledger.CompletedAt.UTC()
		result.NextCleanupAt = snapshot.Receipt.NextCleanupAt.UTC()
		result.HoldReviewDueAt = snapshot.Receipt.CleanupHoldReviewDueAt.UTC()
		return result, nil
	}
	result.CommitStatus = CleanupExecutionDispositionUnverifiable
	return result, nil
}

func cleanupExecutionDispositionPreStateMatches(
	query CleanupExecutionDispositionOutcomeQuery,
	snapshot CurrentCleanupExecutionDispositionSnapshot,
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
		receipt.CleanupDispositionAttemptID != "" || receipt.CleanupControlDisposition != "" ||
		receipt.LastCleanupErrorClass != "" || !receipt.NextCleanupAt.IsZero() ||
		!receipt.CleanupHoldReviewDueAt.IsZero() ||
		attempt.ForeignResidue || attempt.OwnerKind != LeaseOwnerCleanup ||
		attempt.FencingToken != query.ExpectedFence.Token ||
		attempt.WorkerVersion != CleanupWorkerVersion || attempt.Status != RecoveryAttemptStarted ||
		attempt.Outcome != "" || !attempt.CompletedAt.IsZero() ||
		attempt.FailureCode != "" || !attempt.FailedAt.IsZero() ||
		ledger.Phase != query.ExpectedPhase || ledger.Revision != query.ExpectedLedgerRevision ||
		ledger.TargetHash != query.ExpectedTargetHash || ledger.PlanHash != query.ExpectedPlanHash ||
		ledger.ReceiptRevision != query.ExpectedPreReceiptRevision || ledger.Fence != query.ExpectedFence ||
		ledger.Disposition != "" || ledger.EvidenceHash != "" || !ledger.CompletedAt.IsZero() {
		return false
	}
	if cleanupExecutionLedgerHasAmbiguousOutcome(ledger) {
		if ledger.ErrorClass != query.ExpectedErrorClass {
			return false
		}
	} else if ledger.ErrorClass != "" {
		return false
	}
	latest := cleanupExecutionLedgerLatestTime(snapshot.Plan, ledger)
	return !latest.IsZero() && ValidateCleanupExecutionLedger(snapshot.Plan, ledger, latest) == nil
}

func cleanupExecutionDispositionCommittedStateMatches(
	query CleanupExecutionDispositionOutcomeQuery,
	snapshot CurrentCleanupExecutionDispositionSnapshot,
) bool {
	receipt := snapshot.Receipt
	attempt := snapshot.Attempt
	ledger := attempt.Ledger
	expectedAttemptOutcome := RecoveryAttemptOutcomeCleanupRetry
	if query.ExpectedDisposition == CleanupExecutionDispositionHold {
		expectedAttemptOutcome = RecoveryAttemptOutcomeCleanupHold
	}
	if !snapshot.PlanValid || ValidateCleanupExecutionLedgerPlan(snapshot.Plan) != nil ||
		snapshot.Plan.Target.TargetHash != query.ExpectedTargetHash ||
		snapshot.Plan.PlanHash != query.ExpectedPlanHash ||
		receipt.State != ReceiptCleanupPending ||
		receipt.Revision != query.ExpectedFinalReceiptRevision || receipt.PurgeEligibleAt != nil ||
		receipt.LeaseOwnerID != "" || receipt.LeaseOwnerKind != "" ||
		!receipt.LeaseAcquiredAt.IsZero() || !receipt.LeaseHeartbeatAt.IsZero() ||
		!receipt.LeaseExpiresAt.IsZero() || !receipt.NextRecoveryAt.IsZero() ||
		receipt.LastRecoveryCode != "" ||
		receipt.CleanupDispositionAttemptID != query.AttemptID ||
		receipt.CleanupControlDisposition != query.ExpectedDisposition ||
		receipt.LastCleanupErrorClass != query.ExpectedErrorClass ||
		attempt.ForeignResidue || attempt.OwnerKind != LeaseOwnerCleanup ||
		attempt.FencingToken != query.ExpectedFence.Token || attempt.WorkerVersion != CleanupWorkerVersion ||
		attempt.Status != RecoveryAttemptCompleted || attempt.Outcome != expectedAttemptOutcome ||
		attempt.CompletedAt.IsZero() || !attempt.CompletedAt.Equal(receipt.UpdatedAt) ||
		attempt.FailureCode != "" || !attempt.FailedAt.IsZero() ||
		ledger.Phase != query.ExpectedPhase || ledger.Revision != query.ExpectedLedgerRevision ||
		ledger.TargetHash != query.ExpectedTargetHash || ledger.PlanHash != query.ExpectedPlanHash ||
		ledger.ReceiptRevision != query.ExpectedPreReceiptRevision || ledger.Fence != query.ExpectedFence ||
		ledger.Disposition != query.ExpectedDisposition || ledger.ErrorClass != query.ExpectedErrorClass ||
		!ledger.CompletedAt.Equal(attempt.CompletedAt) ||
		ValidateCleanupExecutionLedger(snapshot.Plan, ledger, ledger.CompletedAt) != nil {
		return false
	}
	expectedEvidence, err := CleanupExecutionDispositionEvidenceHash(snapshot.Plan, ledger)
	if err != nil || expectedEvidence != ledger.EvidenceHash {
		return false
	}
	nextCleanupAt, holdReviewDueAt, err := CleanupExecutionDispositionCursorAt(
		ledger.Fence, ledger.ErrorClass, ledger.CompletedAt,
	)
	return err == nil && receipt.NextCleanupAt.Equal(nextCleanupAt) &&
		receipt.CleanupHoldReviewDueAt.Equal(holdReviewDueAt)
}

func canonicalCleanupExecutionDispositionOutcomeQueryBinding(
	query CleanupExecutionDispositionOutcomeQuery,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(cleanupExecutionDispositionQueryBindingVersion)
	encoder.addString(query.TenantID)
	encoder.addString(query.ReservationKey)
	encoder.addString(query.AttemptID)
	encoder.addString(query.ExpectedTargetHash)
	encoder.addString(query.ExpectedPlanHash)
	encoder.addLeaseFence(&query.ExpectedFence)
	encoder.addInt64(query.ExpectedPreReceiptRevision)
	encoder.addInt64(query.ExpectedFinalReceiptRevision)
	encoder.addInt64(query.ExpectedLedgerRevision)
	encoder.addString(string(query.ExpectedPhase))
	encoder.addString(string(query.ExpectedDisposition))
	encoder.addString(string(query.ExpectedErrorClass))
	return encoder.sum()
}

func cleanupExecutionDispositionOutcomeCapabilitySeal(
	grant CleanupExecutionDispositionOutcomeReadGrant,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(cleanupExecutionDispositionGrantVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addBytes(grant.queryBindingHash[:])
	return encoder.sum()
}
