package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	ForwardRecoveryOutcomePolicyVersion = "forward-recovery-outcome.current-state@1"
	ForwardRecoveryOutcomeGrantTTL      = 30 * time.Second

	forwardRecoveryOutcomeQueryBindingVersion = "forward-recovery-outcome-query@1"
	forwardRecoveryOutcomeGrantVersion        = "forward-recovery-outcome-grant@1"
)

var (
	ErrInvalidForwardRecoveryOutcomeAuthorization = errors.New("forward recovery outcome authorization is invalid")
	ErrForwardRecoveryOutcomeAuthorizationExpired = errors.New("forward recovery outcome authorization has expired")
	ErrForwardRecoveryOutcomeUnavailable          = errors.New("forward recovery outcome is unavailable")
)

type ForwardRecoveryOutcomeQuery struct {
	TenantID                string
	ReservationKey          string
	AttemptID               string
	ExpectedDecisionDomain  ForwardRecoveryDecisionDomain
	ExpectedFence           LeaseFence
	ExpectedActionHash      string
	ExpectedReceiptRevision int64
}

type ForwardRecoveryOutcomeReadGrant struct {
	policyVersion    string
	checkedAt        time.Time
	expiresAt        time.Time
	queryBindingHash [sha256.Size]byte
	capabilitySeal   [sha256.Size]byte
}

type RecoveryActionCommitStatus string

const (
	RecoveryActionCommitted    RecoveryActionCommitStatus = "committed"
	RecoveryActionNotCommitted RecoveryActionCommitStatus = "not_committed"
	RecoveryActionUnverifiable RecoveryActionCommitStatus = "unverifiable"
)

type RecoveryActionOutcome struct {
	AttemptID       string
	ActionHash      string
	CommitStatus    RecoveryActionCommitStatus
	Outcome         RecoveryAttemptOutcome
	ReceiptRevision int64
	ReceiptState    ReceiptState
	CompletedAt     time.Time
}

type CurrentForwardRecoveryOutcomeAttempt struct {
	AttemptID                string
	TenantID                 string
	ReceiptID                string
	OwnerKind                LeaseOwnerKind
	FencingToken             int64
	WorkerVersion            string
	Status                   RecoveryAttemptStatus
	DecisionDomain           ForwardRecoveryDecisionDomain
	AuthorizationDisposition ForwardRecoveryAuthorizationDisposition
	Phase                    RecoveryActionPhase
	Classification           ArtifactClassification
	ReasonCode               ArtifactReasonCode
	Action                   ForwardRecoveryAction
	Outcome                  RecoveryAttemptOutcome
	ActionHash               string
	HoldCode                 RecoveryHoldCode
	ReleaseCode              LeaseReleaseCode
	RejectionCode            string
	RawSHA256                string
	RawCRC32C                uint32
	RawSize                  int64
	RawGeneration            int64
	RawMetageneration        int64
	ManifestSHA256           string
	ManifestCRC32C           uint32
	ManifestSize             int64
	ManifestGeneration       int64
	ManifestMetageneration   int64
	HoldReviewDueAt          time.Time
	StartedAt                time.Time
	CompletedAt              time.Time
	FailureCode              RecoveryAttemptFailureCode
	FailedAt                 time.Time
}

// CurrentForwardRecoveryOutcomeReceipt is the minimum receipt projection
// needed for response-loss correlation. Artifact paths and device, trip,
// installation, consent and user identifiers never cross this read boundary.
type CurrentForwardRecoveryOutcomeReceipt struct {
	TenantID                string
	ReservationKey          string
	ReceiptID               string
	State                   ReceiptState
	Revision                int64
	ExpectedSampleCount     int
	SampleCount             int
	ObjectSHA256            string
	ObjectCRC32C            uint32
	ObjectSize              int64
	ObjectGeneration        int64
	ObjectMetageneration    int64
	ManifestSHA256          string
	ManifestCRC32C          uint32
	ManifestSize            int64
	ManifestGeneration      int64
	ManifestMetageneration  int64
	RejectionCode           string
	RecoveryHoldCode        RecoveryHoldCode
	RecoveryHoldReviewDueAt time.Time
	FencingToken            int64
	LeaseOwnerID            string
	LeaseOwnerKind          LeaseOwnerKind
	LeaseAcquiredAt         time.Time
	LeaseHeartbeatAt        time.Time
	LeaseExpiresAt          time.Time
	NextRecoveryAt          time.Time
	LastRecoveryCode        string
	ReservationDeadline     time.Time
	ArtifactExpiresAt       time.Time
	UpdatedAt               time.Time
}

type CurrentForwardRecoveryOutcomeSnapshot struct {
	Receipt  CurrentForwardRecoveryOutcomeReceipt
	Attempt  CurrentForwardRecoveryOutcomeAttempt
	ReadTime time.Time
}

type ForwardRecoveryOutcomeAuthorizationStore interface {
	LoadCurrentForwardRecoveryOutcome(
		context.Context,
		ForwardRecoveryOutcomeQuery,
	) (CurrentForwardRecoveryOutcomeSnapshot, error)
}

type ForwardRecoveryOutcomeStore interface {
	GetForwardRecoveryActionOutcome(
		context.Context,
		ForwardRecoveryOutcomeReadGrant,
		ForwardRecoveryOutcomeQuery,
		time.Time,
	) (RecoveryActionOutcome, error)
}

type SystemRecoveryOutcomeAuthorizer struct {
	store ForwardRecoveryOutcomeAuthorizationStore
	now   func() time.Time
}

func NewSystemRecoveryOutcomeAuthorizer(
	store ForwardRecoveryOutcomeAuthorizationStore,
	now func() time.Time,
) (*SystemRecoveryOutcomeAuthorizer, error) {
	if store == nil {
		return nil, errors.New("forward recovery outcome authorization store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &SystemRecoveryOutcomeAuthorizer{store: store, now: now}, nil
}

func ForwardRecoveryOutcomeQueryForAction(
	command ForwardRecoveryActionCommand,
) (ForwardRecoveryOutcomeQuery, error) {
	command = cloneForwardRecoveryActionCommand(command)
	actionHash, err := ForwardRecoveryActionHash(command)
	if err != nil || command.ReceiptRevision == int64(^uint64(0)>>1) {
		return ForwardRecoveryOutcomeQuery{}, ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	query := ForwardRecoveryOutcomeQuery{
		TenantID: command.TenantID, ReservationKey: command.ReservationKey,
		AttemptID: command.Attempt.ID, ExpectedFence: command.Fence,
		ExpectedDecisionDomain:  ForwardRecoveryDecisionArtifactReconciliation,
		ExpectedActionHash:      actionHash,
		ExpectedReceiptRevision: command.ReceiptRevision + 1,
	}
	if validateForwardRecoveryOutcomeQuery(query) != nil {
		return ForwardRecoveryOutcomeQuery{}, ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	return query, nil
}

func ForwardRecoveryOutcomeQueryForDisposition(
	command ForwardRecoveryDispositionCommand,
) (ForwardRecoveryOutcomeQuery, error) {
	actionHash, err := ForwardRecoveryDispositionHash(command)
	if err != nil || command.ReceiptRevision == int64(^uint64(0)>>1) {
		return ForwardRecoveryOutcomeQuery{}, ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	query := ForwardRecoveryOutcomeQuery{
		TenantID: command.TenantID, ReservationKey: command.ReservationKey,
		AttemptID: command.Attempt.ID, ExpectedFence: command.Fence,
		ExpectedDecisionDomain:  ForwardRecoveryDecisionCurrentAuthorization,
		ExpectedActionHash:      actionHash,
		ExpectedReceiptRevision: command.ReceiptRevision + 1,
	}
	if validateForwardRecoveryOutcomeQuery(query) != nil {
		return ForwardRecoveryOutcomeQuery{}, ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	return query, nil
}

func (a *SystemRecoveryOutcomeAuthorizer) Authorize(
	ctx context.Context,
	query ForwardRecoveryOutcomeQuery,
) (ForwardRecoveryOutcomeReadGrant, error) {
	if a == nil || a.store == nil || a.now == nil || ctx == nil ||
		validateForwardRecoveryOutcomeQuery(query) != nil {
		return ForwardRecoveryOutcomeReadGrant{}, ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	snapshot, err := a.store.LoadCurrentForwardRecoveryOutcome(ctx, query)
	if err != nil {
		return ForwardRecoveryOutcomeReadGrant{}, err
	}
	checkedAt, err := forwardRecoveryAuthorizationTime(a.now().UTC(), snapshot.ReadTime.UTC())
	if err != nil || validateCurrentForwardRecoveryOutcomeShape(query, snapshot, checkedAt) != nil {
		return ForwardRecoveryOutcomeReadGrant{}, ErrForwardRecoveryOutcomeUnavailable
	}
	grant := ForwardRecoveryOutcomeReadGrant{
		policyVersion:    ForwardRecoveryOutcomePolicyVersion,
		checkedAt:        checkedAt,
		expiresAt:        checkedAt.Add(ForwardRecoveryOutcomeGrantTTL),
		queryBindingHash: canonicalForwardRecoveryOutcomeQueryBinding(query),
	}
	grant.capabilitySeal = forwardRecoveryOutcomeCapabilitySeal(grant)
	return grant, nil
}

func ValidateForwardRecoveryOutcomeAuthorization(
	grant ForwardRecoveryOutcomeReadGrant,
	query ForwardRecoveryOutcomeQuery,
	observedAt time.Time,
) error {
	if validateForwardRecoveryOutcomeQuery(query) != nil || observedAt.IsZero() ||
		!validArtifactServerLabel(grant.policyVersion) || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.queryBindingHash != canonicalForwardRecoveryOutcomeQueryBinding(query) ||
		grant.capabilitySeal != forwardRecoveryOutcomeCapabilitySeal(grant) ||
		observedAt.Before(grant.checkedAt) {
		return ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	if !observedAt.Before(grant.expiresAt) {
		return ErrForwardRecoveryOutcomeAuthorizationExpired
	}
	return nil
}

func ForwardRecoveryOutcomeAuthorizationDeadline(
	grant ForwardRecoveryOutcomeReadGrant,
	query ForwardRecoveryOutcomeQuery,
) (time.Time, error) {
	if validateForwardRecoveryOutcomeQuery(query) != nil ||
		!validArtifactServerLabel(grant.policyVersion) || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.queryBindingHash != canonicalForwardRecoveryOutcomeQueryBinding(query) ||
		grant.capabilitySeal != forwardRecoveryOutcomeCapabilitySeal(grant) {
		return time.Time{}, ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	return grant.expiresAt, nil
}

func EvaluateForwardRecoveryActionOutcome(
	query ForwardRecoveryOutcomeQuery,
	snapshot CurrentForwardRecoveryOutcomeSnapshot,
	observedAt time.Time,
) (RecoveryActionOutcome, error) {
	if validateForwardRecoveryOutcomeQuery(query) != nil ||
		validateCurrentForwardRecoveryOutcomeShape(query, snapshot, observedAt) != nil {
		return RecoveryActionOutcome{}, ErrForwardRecoveryOutcomeUnavailable
	}
	attempt := snapshot.Attempt
	receipt := snapshot.Receipt
	result := RecoveryActionOutcome{
		AttemptID:       attempt.AttemptID,
		ReceiptRevision: receipt.Revision, ReceiptState: receipt.State,
	}
	switch attempt.Status {
	case RecoveryAttemptStarted, RecoveryAttemptFailed:
		if receipt.Revision != query.ExpectedReceiptRevision-1 || receipt.State != ReceiptReserved {
			result.CommitStatus = RecoveryActionUnverifiable
			return result, nil
		}
		result.CommitStatus = RecoveryActionNotCommitted
		return result, nil
	case RecoveryAttemptCompleted:
		if attempt.ActionHash != query.ExpectedActionHash ||
			receipt.Revision != query.ExpectedReceiptRevision ||
			!attempt.CompletedAt.Equal(receipt.UpdatedAt) ||
			!completedOutcomeMatchesReceipt(attempt, receipt) {
			result.CommitStatus = RecoveryActionUnverifiable
			return result, nil
		}
		result.CommitStatus = RecoveryActionCommitted
		result.ActionHash = attempt.ActionHash
		result.Outcome = attempt.Outcome
		result.CompletedAt = attempt.CompletedAt
		return result, nil
	default:
		return RecoveryActionOutcome{}, ErrForwardRecoveryOutcomeUnavailable
	}
}

func validateForwardRecoveryOutcomeQuery(query ForwardRecoveryOutcomeQuery) error {
	if !telemetry.IsUUID(query.TenantID) || !isLowerHexDigest(query.ReservationKey) ||
		!telemetry.IsUUID(query.AttemptID) || !isLowerHexDigest(query.ExpectedActionHash) ||
		!validForwardRecoveryDecisionDomain(query.ExpectedDecisionDomain) ||
		ValidateLeaseFence(query.ExpectedFence) != nil ||
		query.AttemptID != query.ExpectedFence.OwnerID || query.ExpectedReceiptRevision <= 1 {
		return ErrInvalidForwardRecoveryOutcomeAuthorization
	}
	return nil
}

func validateCurrentForwardRecoveryOutcomeShape(
	query ForwardRecoveryOutcomeQuery,
	snapshot CurrentForwardRecoveryOutcomeSnapshot,
	observedAt time.Time,
) error {
	receipt := snapshot.Receipt
	attempt := snapshot.Attempt
	if observedAt.IsZero() || snapshot.ReadTime.IsZero() || snapshot.ReadTime.After(observedAt) ||
		receipt.TenantID != query.TenantID || receipt.ReservationKey != query.ReservationKey ||
		!telemetry.IsUUID(receipt.ReceiptID) || !validOutcomeReceiptState(receipt.State) ||
		receipt.Revision <= 0 || receipt.FencingToken <= 0 || receipt.UpdatedAt.IsZero() ||
		receipt.UpdatedAt.After(observedAt) || receipt.ExpectedSampleCount < 1 ||
		receipt.ExpectedSampleCount > telemetry.MaxSamples || receipt.SampleCount < 0 ||
		receipt.SampleCount > receipt.ExpectedSampleCount || receipt.ReservationDeadline.IsZero() ||
		!receipt.ReservationDeadline.Before(receipt.ArtifactExpiresAt) ||
		attempt.AttemptID != query.AttemptID || attempt.TenantID != query.TenantID ||
		attempt.ReceiptID != receipt.ReceiptID || attempt.OwnerKind != LeaseOwnerSweeper ||
		attempt.FencingToken != query.ExpectedFence.Token ||
		receipt.FencingToken != query.ExpectedFence.Token ||
		attempt.WorkerVersion != RecoveryWorkerVersion ||
		attempt.StartedAt.IsZero() || attempt.StartedAt.After(observedAt) ||
		!attempt.StartedAt.Before(query.ExpectedFence.ExpiresAt) ||
		query.ExpectedFence.ExpiresAt.After(receipt.ReservationDeadline) {
		return ErrForwardRecoveryOutcomeUnavailable
	}
	switch attempt.Status {
	case RecoveryAttemptStarted:
		if !emptyOutcomeAttemptTerminalFields(attempt) ||
			!currentOutcomeFenceMatchesReceipt(query, attempt, receipt) {
			return ErrForwardRecoveryOutcomeUnavailable
		}
	case RecoveryAttemptFailed:
		if !ValidRecoveryAttemptFailureCode(attempt.FailureCode) || attempt.FailedAt.IsZero() ||
			!attempt.FailedAt.After(attempt.StartedAt) || attempt.FailedAt.After(observedAt) ||
			attempt.DecisionDomain != "" || attempt.AuthorizationDisposition != "" ||
			attempt.Phase != "" || attempt.Classification != "" || attempt.ReasonCode != "" ||
			attempt.Action != "" || attempt.Outcome != "" || attempt.ActionHash != "" ||
			attempt.HoldCode != "" || attempt.ReleaseCode != "" || attempt.RejectionCode != "" ||
			!emptyOutcomeAttemptLineage(attempt) || !attempt.HoldReviewDueAt.IsZero() ||
			!attempt.CompletedAt.IsZero() ||
			!currentOutcomeFenceMatchesReceipt(query, attempt, receipt) ||
			!validFailedOutcomeAttemptTime(attempt, query.ExpectedFence) ||
			!attempt.FailedAt.Before(receipt.ReservationDeadline) {
			return ErrForwardRecoveryOutcomeUnavailable
		}
	case RecoveryAttemptCompleted:
		if attempt.DecisionDomain != query.ExpectedDecisionDomain ||
			!validCompletedOutcomeAttempt(attempt) || attempt.FailureCode != "" ||
			!attempt.FailedAt.IsZero() || attempt.CompletedAt.After(observedAt) ||
			!attempt.CompletedAt.Before(receipt.ReservationDeadline) ||
			!attempt.CompletedAt.Before(query.ExpectedFence.ExpiresAt) {
			return ErrForwardRecoveryOutcomeUnavailable
		}
	default:
		return ErrForwardRecoveryOutcomeUnavailable
	}
	return nil
}

func emptyOutcomeAttemptTerminalFields(attempt CurrentForwardRecoveryOutcomeAttempt) bool {
	return attempt.DecisionDomain == "" && attempt.AuthorizationDisposition == "" &&
		attempt.Phase == "" && attempt.Classification == "" && attempt.ReasonCode == "" &&
		attempt.Action == "" && attempt.Outcome == "" && attempt.ActionHash == "" &&
		attempt.HoldCode == "" && attempt.ReleaseCode == "" && attempt.RejectionCode == "" &&
		emptyOutcomeAttemptLineage(attempt) && attempt.HoldReviewDueAt.IsZero() && attempt.CompletedAt.IsZero() &&
		attempt.FailureCode == "" && attempt.FailedAt.IsZero()
}

func emptyOutcomeAttemptLineage(attempt CurrentForwardRecoveryOutcomeAttempt) bool {
	return attempt.RawSHA256 == "" && attempt.RawCRC32C == 0 && attempt.RawSize == 0 &&
		attempt.RawGeneration == 0 && attempt.RawMetageneration == 0 &&
		attempt.ManifestSHA256 == "" && attempt.ManifestCRC32C == 0 &&
		attempt.ManifestSize == 0 && attempt.ManifestGeneration == 0 &&
		attempt.ManifestMetageneration == 0
}

func currentOutcomeFenceMatchesReceipt(
	query ForwardRecoveryOutcomeQuery,
	attempt CurrentForwardRecoveryOutcomeAttempt,
	receipt CurrentForwardRecoveryOutcomeReceipt,
) bool {
	return receipt.State == ReceiptReserved &&
		receipt.SampleCount == 0 && outcomeReceiptHasNoArtifactLineage(receipt) &&
		receipt.RejectionCode == "" && receipt.RecoveryHoldCode == "" &&
		receipt.RecoveryHoldReviewDueAt.IsZero() && receipt.LastRecoveryCode == "" &&
		receipt.LeaseOwnerID == query.ExpectedFence.OwnerID &&
		receipt.LeaseOwnerKind == LeaseOwnerSweeper &&
		receipt.FencingToken == query.ExpectedFence.Token &&
		receipt.LeaseExpiresAt.Equal(query.ExpectedFence.ExpiresAt) &&
		!receipt.LeaseAcquiredAt.IsZero() &&
		receipt.LeaseAcquiredAt.Equal(attempt.StartedAt) &&
		!receipt.LeaseHeartbeatAt.Before(receipt.LeaseAcquiredAt) &&
		receipt.LeaseHeartbeatAt.Before(receipt.LeaseExpiresAt) &&
		receipt.UpdatedAt.Equal(receipt.LeaseHeartbeatAt) &&
		receipt.NextRecoveryAt.Equal(receipt.LeaseExpiresAt)
}

func validFailedOutcomeAttemptTime(
	attempt CurrentForwardRecoveryOutcomeAttempt,
	fence LeaseFence,
) bool {
	if attempt.FailureCode == RecoveryAttemptFailureLeaseExpired {
		return !attempt.FailedAt.Before(fence.ExpiresAt)
	}
	return attempt.FailedAt.Before(fence.ExpiresAt)
}

func validCompletedOutcomeAttempt(attempt CurrentForwardRecoveryOutcomeAttempt) bool {
	if !isLowerHexDigest(attempt.ActionHash) || attempt.CompletedAt.IsZero() ||
		!attempt.CompletedAt.After(attempt.StartedAt) ||
		!ValidRecoveryAttemptOutcome(attempt.Outcome) ||
		!validForwardRecoveryDecisionDomain(attempt.DecisionDomain) {
		return false
	}
	if attempt.DecisionDomain == ForwardRecoveryDecisionCurrentAuthorization {
		return validCompletedAuthorizationDispositionOutcome(attempt)
	}
	if attempt.AuthorizationDisposition != "" || !validRecoveryActionPhase(attempt.Phase) ||
		!validArtifactClassificationOutcome(ArtifactReadForwardRecovery, attempt.Classification, attempt.ReasonCode) {
		return false
	}
	switch attempt.Action {
	case ForwardRecoveryActionMarkStored:
		return attempt.Outcome == RecoveryAttemptOutcomeStored &&
			(attempt.Phase == RecoveryPhaseConfirmation ||
				attempt.Phase == RecoveryPhasePostManifestConfirmation) &&
			attempt.Classification == ArtifactClassificationValidComplete &&
			attempt.ReasonCode == ArtifactReasonManifestAndReferencedRawValid &&
			validOutcomeLineageSummary(
				attempt.RawSHA256, attempt.RawSize,
				attempt.RawGeneration, attempt.RawMetageneration,
			) && validOutcomeLineageSummary(
			attempt.ManifestSHA256, attempt.ManifestSize,
			attempt.ManifestGeneration, attempt.ManifestMetageneration,
		) &&
			attempt.HoldCode == "" && attempt.ReleaseCode == "" && attempt.RejectionCode == "" &&
			attempt.HoldReviewDueAt.IsZero()
	case ForwardRecoveryActionMarkRejected:
		return attempt.Outcome == RecoveryAttemptOutcomeRejected &&
			attempt.Phase == RecoveryPhaseConfirmation &&
			attempt.Classification == ArtifactClassificationRawContentConflict &&
			validOutcomeLineageSummary(
				attempt.RawSHA256, attempt.RawSize,
				attempt.RawGeneration, attempt.RawMetageneration,
			) && emptyOutcomeManifestLineage(attempt) &&
			attempt.RejectionCode == "object_conflict" && attempt.HoldCode == "" &&
			attempt.ReleaseCode == "" && attempt.HoldReviewDueAt.IsZero()
	case ForwardRecoveryActionMarkHold:
		return attempt.Outcome == RecoveryAttemptOutcomeHold && validOutcomeHoldMapping(attempt) &&
			emptyOutcomeAttemptLineage(attempt) &&
			attempt.ReleaseCode == "" && attempt.RejectionCode == "" &&
			!attempt.HoldReviewDueAt.IsZero() && attempt.CompletedAt.Before(attempt.HoldReviewDueAt)
	case ForwardRecoveryActionReleaseLease:
		return attempt.Outcome == RecoveryAttemptOutcomeLeaseReleased &&
			validOutcomeReleaseMapping(attempt) && emptyOutcomeAttemptLineage(attempt) &&
			attempt.HoldCode == "" &&
			attempt.RejectionCode == "" && attempt.HoldReviewDueAt.IsZero()
	default:
		return false
	}
}

// ValidateCompletedForwardRecoveryAttemptForPurge validates the full bounded
// terminal attempt union without reconstructing historical receipt authority.
// Receipt purge uses it only after the current receipt/job fence is validated.
func ValidateCompletedForwardRecoveryAttemptForPurge(
	attempt CurrentForwardRecoveryOutcomeAttempt,
) error {
	if !telemetry.IsUUID(attempt.AttemptID) || !telemetry.IsUUID(attempt.TenantID) ||
		!telemetry.IsUUID(attempt.ReceiptID) ||
		(attempt.OwnerKind != LeaseOwnerRequest && attempt.OwnerKind != LeaseOwnerSweeper) ||
		attempt.FencingToken <= 0 || attempt.WorkerVersion != RecoveryWorkerVersion ||
		attempt.Status != RecoveryAttemptCompleted || attempt.StartedAt.IsZero() ||
		attempt.CompletedAt.IsZero() || !attempt.CompletedAt.After(attempt.StartedAt) ||
		attempt.FailureCode != "" || !attempt.FailedAt.IsZero() ||
		!validCompletedOutcomeAttempt(attempt) {
		return ErrForwardRecoveryOutcomeUnavailable
	}
	return nil
}

func validCompletedAuthorizationDispositionOutcome(
	attempt CurrentForwardRecoveryOutcomeAttempt,
) bool {
	if attempt.Phase != "" || attempt.Classification != "" || attempt.ReasonCode != "" ||
		!emptyOutcomeAttemptLineage(attempt) || attempt.RejectionCode != "" {
		return false
	}
	switch attempt.AuthorizationDisposition {
	case ForwardRecoveryAuthorizationDenied:
		return attempt.Action == ForwardRecoveryActionMarkHold &&
			attempt.Outcome == RecoveryAttemptOutcomeHold &&
			attempt.HoldCode == RecoveryHoldCurrentAuthorizationDenied &&
			attempt.ReleaseCode == "" && !attempt.HoldReviewDueAt.IsZero() &&
			attempt.CompletedAt.Before(attempt.HoldReviewDueAt)
	case ForwardRecoveryAuthorizationUnavailable:
		return attempt.Action == ForwardRecoveryActionReleaseLease &&
			attempt.Outcome == RecoveryAttemptOutcomeLeaseReleased &&
			attempt.ReleaseCode == LeaseReleaseAuthorizationUnavailable &&
			attempt.HoldCode == "" && attempt.HoldReviewDueAt.IsZero()
	default:
		return false
	}
}

func validOutcomeLineageSummary(sha256 string, size, generation, metageneration int64) bool {
	return isLowerHexDigest(sha256) && size > 0 && generation > 0 && metageneration > 0
}

func emptyOutcomeManifestLineage(attempt CurrentForwardRecoveryOutcomeAttempt) bool {
	return attempt.ManifestSHA256 == "" && attempt.ManifestCRC32C == 0 &&
		attempt.ManifestSize == 0 && attempt.ManifestGeneration == 0 &&
		attempt.ManifestMetageneration == 0
}

func validOutcomeHoldMapping(attempt CurrentForwardRecoveryOutcomeAttempt) bool {
	if !ValidRecoveryHoldCode(attempt.HoldCode) {
		return false
	}
	switch attempt.Phase {
	case RecoveryPhaseInitial:
		switch attempt.Classification {
		case ArtifactClassificationManifestOnly:
			return attempt.ReasonCode == ArtifactReasonReferencedRawNotFound &&
				attempt.HoldCode == RecoveryHoldManifestOnly
		case ArtifactClassificationManifestConflict:
			return attempt.HoldCode == RecoveryHoldManifestConflict
		case ArtifactClassificationMetadataConflict:
			return attempt.HoldCode == RecoveryHoldMetadataConflict
		case ArtifactClassificationGenerationDrift:
			return attempt.HoldCode == RecoveryHoldGenerationDrift
		case ArtifactClassificationUnavailable:
			return nontransientOutcomeHoldCode(attempt.ReasonCode) == attempt.HoldCode
		default:
			return false
		}
	case RecoveryPhaseConfirmation:
		return !transientOutcomeReason(attempt.Classification, attempt.ReasonCode) &&
			attempt.HoldCode == RecoveryHoldConfirmationDrift
	case RecoveryPhasePostManifestConfirmation:
		return !transientOutcomeReason(attempt.Classification, attempt.ReasonCode) &&
			attempt.HoldCode == RecoveryHoldPostManifestDivergence
	default:
		return false
	}
}

func validOutcomeReleaseMapping(attempt CurrentForwardRecoveryOutcomeAttempt) bool {
	switch attempt.Phase {
	case RecoveryPhaseInitial:
		return attempt.Classification == ArtifactClassificationNone &&
			attempt.ReasonCode == ArtifactReasonNoCandidates &&
			attempt.ReleaseCode == LeaseReleaseAwaitingClientReplay ||
			transientOutcomeReason(attempt.Classification, attempt.ReasonCode) &&
				attempt.ReleaseCode == LeaseReleaseArtifactUnavailable
	case RecoveryPhaseConfirmation, RecoveryPhasePostManifestConfirmation:
		return transientOutcomeReason(attempt.Classification, attempt.ReasonCode) &&
			attempt.ReleaseCode == LeaseReleaseArtifactUnavailable
	default:
		return false
	}
}

func transientOutcomeReason(classification ArtifactClassification, reason ArtifactReasonCode) bool {
	if classification != ArtifactClassificationUnavailable {
		return false
	}
	switch reason {
	case ArtifactReasonQuotaLimited,
		ArtifactReasonProviderTimeout,
		ArtifactReasonProviderCancelled,
		ArtifactReasonProviderUnavailable:
		return true
	default:
		return false
	}
}

func nontransientOutcomeHoldCode(reason ArtifactReasonCode) RecoveryHoldCode {
	switch reason {
	case ArtifactReasonPermissionDenied:
		return RecoveryHoldArtifactPermissionDenied
	case ArtifactReasonValidatorUnavailable:
		return RecoveryHoldValidatorUnavailable
	case ArtifactReasonCodecProfileUnavailable:
		return RecoveryHoldCodecUnavailable
	case ArtifactReasonInventoryCoverageIncomplete:
		return RecoveryHoldInventoryIncomplete
	case ArtifactReasonResponseUnverifiable:
		return RecoveryHoldResponseUnverifiable
	default:
		return ""
	}
}

func completedOutcomeMatchesReceipt(
	attempt CurrentForwardRecoveryOutcomeAttempt,
	receipt CurrentForwardRecoveryOutcomeReceipt,
) bool {
	switch attempt.Outcome {
	case RecoveryAttemptOutcomeStored:
		return receipt.State == ReceiptStored && receipt.SampleCount == receipt.ExpectedSampleCount &&
			receipt.ObjectSHA256 == attempt.RawSHA256 && receipt.ObjectCRC32C == attempt.RawCRC32C &&
			receipt.ObjectSize == attempt.RawSize && receipt.ObjectGeneration == attempt.RawGeneration &&
			receipt.ObjectMetageneration == attempt.RawMetageneration &&
			receipt.ManifestSHA256 == attempt.ManifestSHA256 &&
			receipt.ManifestCRC32C == attempt.ManifestCRC32C &&
			receipt.ManifestSize == attempt.ManifestSize &&
			receipt.ManifestGeneration == attempt.ManifestGeneration &&
			receipt.ManifestMetageneration == attempt.ManifestMetageneration &&
			receipt.RejectionCode == "" && receipt.RecoveryHoldCode == "" &&
			receipt.RecoveryHoldReviewDueAt.IsZero() && receipt.NextRecoveryAt.IsZero() &&
			receipt.LastRecoveryCode == "" && outcomeReceiptHasNoLease(receipt)
	case RecoveryAttemptOutcomeRejected:
		return receipt.State == ReceiptRejected && receipt.RejectionCode == attempt.RejectionCode &&
			outcomeReceiptHasNoArtifactLineage(receipt) && receipt.SampleCount == 0 &&
			receipt.RecoveryHoldCode == "" && receipt.RecoveryHoldReviewDueAt.IsZero() &&
			receipt.NextRecoveryAt.IsZero() && receipt.LastRecoveryCode == "" &&
			outcomeReceiptHasNoLease(receipt)
	case RecoveryAttemptOutcomeHold:
		return receipt.State == ReceiptRecoveryHold && receipt.RecoveryHoldCode == attempt.HoldCode &&
			receipt.RecoveryHoldReviewDueAt.Equal(attempt.HoldReviewDueAt) &&
			receipt.RecoveryHoldReviewDueAt.Before(receipt.ArtifactExpiresAt) &&
			outcomeReceiptHasNoArtifactLineage(receipt) && receipt.SampleCount == 0 &&
			receipt.RejectionCode == "" && receipt.NextRecoveryAt.IsZero() &&
			receipt.LastRecoveryCode == "" && outcomeReceiptHasNoLease(receipt)
	case RecoveryAttemptOutcomeLeaseReleased:
		return receipt.State == ReceiptReserved && outcomeReceiptHasNoLease(receipt) &&
			receipt.LastRecoveryCode == string(attempt.ReleaseCode) &&
			receipt.RejectionCode == "" && receipt.RecoveryHoldCode == "" &&
			receipt.RecoveryHoldReviewDueAt.IsZero() &&
			outcomeReceiptHasNoArtifactLineage(receipt) && receipt.SampleCount == 0 &&
			receipt.NextRecoveryAt.Equal(expectedOutcomeNextRecoveryAt(attempt, receipt))
	default:
		return false
	}
}

func expectedOutcomeNextRecoveryAt(
	attempt CurrentForwardRecoveryOutcomeAttempt,
	receipt CurrentForwardRecoveryOutcomeReceipt,
) time.Time {
	next := attempt.CompletedAt.Add(InitialRecoveryBackoff)
	if next.After(receipt.ReservationDeadline) {
		return receipt.ReservationDeadline
	}
	return next
}

func outcomeReceiptHasNoLease(receipt CurrentForwardRecoveryOutcomeReceipt) bool {
	return receipt.LeaseOwnerID == "" && receipt.LeaseOwnerKind == "" &&
		receipt.LeaseAcquiredAt.IsZero() && receipt.LeaseHeartbeatAt.IsZero() &&
		receipt.LeaseExpiresAt.IsZero()
}

func validOutcomeReceiptState(state ReceiptState) bool {
	switch state {
	case ReceiptReserved, ReceiptStored, ReceiptRejected, ReceiptRecoveryHold:
		return true
	default:
		return false
	}
}

func outcomeReceiptHasNoArtifactLineage(receipt CurrentForwardRecoveryOutcomeReceipt) bool {
	return receipt.ObjectSHA256 == "" && receipt.ObjectCRC32C == 0 && receipt.ObjectSize == 0 &&
		receipt.ObjectGeneration == 0 && receipt.ObjectMetageneration == 0 &&
		receipt.ManifestSHA256 == "" && receipt.ManifestCRC32C == 0 && receipt.ManifestSize == 0 &&
		receipt.ManifestGeneration == 0 && receipt.ManifestMetageneration == 0
}

func canonicalForwardRecoveryOutcomeQueryBinding(
	query ForwardRecoveryOutcomeQuery,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(forwardRecoveryOutcomeQueryBindingVersion)
	encoder.addString(query.TenantID)
	encoder.addString(query.ReservationKey)
	encoder.addString(query.AttemptID)
	encoder.addString(string(query.ExpectedDecisionDomain))
	encoder.addLeaseFence(&query.ExpectedFence)
	encoder.addString(query.ExpectedActionHash)
	encoder.addInt64(query.ExpectedReceiptRevision)
	return encoder.sum()
}

func forwardRecoveryOutcomeCapabilitySeal(
	grant ForwardRecoveryOutcomeReadGrant,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(forwardRecoveryOutcomeGrantVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addBytes(grant.queryBindingHash[:])
	return encoder.sum()
}
