package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	ForwardRecoveryActionPolicyVersion = "forward-recovery-action.current-state@1"
	DefaultRecoveryHoldReviewWindow    = 24 * time.Hour

	forwardRecoveryActionBindingVersion = "forward-recovery-action-command@1"
	forwardRecoveryActionGrantVersion   = "forward-recovery-action-grant@1"
)

var (
	ErrInvalidForwardRecoveryActionAuthorization = errors.New("forward recovery action authorization is invalid")
	ErrForwardRecoveryActionAuthorizationExpired = errors.New("forward recovery action authorization has expired")
)

// ForwardRecoveryActionCommand is a constructible description of one planner
// result. It is not authority by itself: every mutation boundary must validate
// the opaque grant against this exact command and a fresh transaction snapshot.
type ForwardRecoveryActionCommand struct {
	TenantID        string
	ReservationKey  string
	Attempt         RecoveryAttemptProposal
	ReceiptRevision int64
	Fence           LeaseFence
	Plan            ForwardRecoveryActionPlan
	HoldReviewDueAt time.Time
}

// CurrentForwardRecoveryAttempt is the provider-neutral started-attempt fact
// that must be read in the same transaction as the receipt and current
// authorization relations. A receipt fence alone is not proof that the exact
// recovery attempt still exists and is unfinished.
type CurrentForwardRecoveryAttempt struct {
	AttemptID     string
	TenantID      string
	ReceiptID     string
	OwnerKind     LeaseOwnerKind
	FencingToken  int64
	WorkerVersion string
	Status        RecoveryAttemptStatus
	StartedAt     time.Time
}

// ForwardRecoveryActionGrant is an opaque, short-lived in-process capability.
// It binds the current request, attempt, receipt revision/fence and the full
// phase-aware action command. Its zero value is always invalid.
type ForwardRecoveryActionGrant struct {
	policyVersion      string
	checkedAt          time.Time
	expiresAt          time.Time
	attemptID          string
	receiptRevision    int64
	forwardFence       LeaseFence
	requestBindingHash [sha256.Size]byte
	commandBindingHash [sha256.Size]byte
	capabilitySeal     [sha256.Size]byte
}

type ForwardRecoveryActionStore interface {
	CommitForwardRecoveryAction(
		context.Context,
		ForwardRecoveryActionGrant,
		ForwardRecoveryActionCommand,
		time.Time,
	) (Receipt, error)
}

// AuthorizeForwardRecoveryAction reruns the pure planner over genuine
// classifier evidence, refreshes current authorization, and only then mints a
// capability for a terminal receipt action. Confirm/create steps are not
// mutation commands and cannot receive an action grant.
func (a *SystemRecoveryAuthorizer) AuthorizeForwardRecoveryAction(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
	attempt RecoveryAttemptProposal,
	input ForwardRecoveryPlanInput,
) (ForwardRecoveryActionCommand, ForwardRecoveryActionGrant, error) {
	if a == nil || a.validator == nil || ValidateRecoveryAttemptProposal(attempt) != nil ||
		ValidateLeaseGrant(lease) != nil || lease.OwnerKind != LeaseOwnerSweeper ||
		attempt.ID != lease.Fence.OwnerID {
		return ForwardRecoveryActionCommand{}, ForwardRecoveryActionGrant{}, ErrInvalidForwardRecoveryActionAuthorization
	}
	input = cloneForwardRecoveryPlanInput(input)
	plan, err := PlanForwardRecoveryAction(input)
	if err != nil || !terminalForwardRecoveryAction(plan.Action) {
		return ForwardRecoveryActionCommand{}, ForwardRecoveryActionGrant{}, ErrInvalidForwardRecoveryActionAuthorization
	}
	request, readGrant, err := a.Authorize(ctx, tenantID, reservationKey, lease)
	if err != nil {
		return ForwardRecoveryActionCommand{}, ForwardRecoveryActionGrant{}, err
	}
	if canonicalArtifactClassificationRequestBinding(request) !=
		canonicalArtifactClassificationRequestBinding(input.Request) {
		return ForwardRecoveryActionCommand{}, ForwardRecoveryActionGrant{}, ErrInvalidForwardRecoveryActionAuthorization
	}
	command := ForwardRecoveryActionCommand{
		TenantID:        tenantID,
		ReservationKey:  reservationKey,
		Attempt:         attempt,
		ReceiptRevision: request.ReceiptRevision,
		Fence:           *request.ForwardFence,
		Plan:            cloneForwardRecoveryActionPlan(plan),
	}
	if plan.Action == ForwardRecoveryActionMarkHold {
		command.HoldReviewDueAt, err = boundedRecoveryHoldReviewDueAt(
			readGrant.checkedAt, request.ArtifactExpiresAt,
		)
		if err != nil {
			return ForwardRecoveryActionCommand{}, ForwardRecoveryActionGrant{},
				ErrInvalidForwardRecoveryActionAuthorization
		}
	}
	grant, err := mintForwardRecoveryActionGrant(
		command,
		canonicalArtifactClassificationRequestBinding(request),
		readGrant.checkedAt,
		readGrant.expiresAt,
	)
	if err != nil {
		return ForwardRecoveryActionCommand{}, ForwardRecoveryActionGrant{}, ErrInvalidForwardRecoveryActionAuthorization
	}
	return cloneForwardRecoveryActionCommand(command), grant, nil
}

func ValidateForwardRecoveryActionAuthorization(
	grant ForwardRecoveryActionGrant,
	command ForwardRecoveryActionCommand,
	observedAt time.Time,
) error {
	command = cloneForwardRecoveryActionCommand(command)
	if validateForwardRecoveryActionCommand(command) != nil || observedAt.IsZero() ||
		!validArtifactServerLabel(grant.policyVersion) || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.attemptID != command.Attempt.ID || grant.receiptRevision != command.ReceiptRevision ||
		!sameLeaseFence(grant.forwardFence, command.Fence) ||
		grant.commandBindingHash != canonicalForwardRecoveryActionBinding(command) ||
		grant.capabilitySeal != forwardRecoveryActionCapabilitySeal(grant) ||
		observedAt.Before(grant.checkedAt) {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	if !observedAt.Before(grant.expiresAt) || !observedAt.Before(grant.forwardFence.ExpiresAt) {
		return ErrForwardRecoveryActionAuthorizationExpired
	}
	return nil
}

// ForwardRecoveryActionAuthorizationDeadline exposes only the effective
// provider deadline after the opaque grant and exact command binding have been
// verified. Adapters must clamp their transaction context to this value.
func ForwardRecoveryActionAuthorizationDeadline(
	grant ForwardRecoveryActionGrant,
	command ForwardRecoveryActionCommand,
) (time.Time, error) {
	command = cloneForwardRecoveryActionCommand(command)
	if validateForwardRecoveryActionCommand(command) != nil ||
		!validArtifactServerLabel(grant.policyVersion) || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.attemptID != command.Attempt.ID || grant.receiptRevision != command.ReceiptRevision ||
		!sameLeaseFence(grant.forwardFence, command.Fence) ||
		grant.commandBindingHash != canonicalForwardRecoveryActionBinding(command) ||
		grant.capabilitySeal != forwardRecoveryActionCapabilitySeal(grant) {
		return time.Time{}, ErrInvalidForwardRecoveryActionAuthorization
	}
	return earlierRecoveryTime(grant.expiresAt, grant.forwardFence.ExpiresAt), nil
}

// ValidateCurrentForwardRecoveryAction reevaluates the same domain policy over
// the transaction-coherent current snapshot immediately before the receipt and
// attempt updates. This is deliberately separate from pre-action grant minting
// so consent or relationship changes cannot race the final transaction.
func ValidateCurrentForwardRecoveryAction(
	grant ForwardRecoveryActionGrant,
	command ForwardRecoveryActionCommand,
	snapshot CurrentForwardRecoverySnapshot,
	attempt CurrentForwardRecoveryAttempt,
	observedAt time.Time,
) error {
	if err := ValidateForwardRecoveryActionAuthorization(grant, command, observedAt); err != nil {
		return err
	}
	snapshot = cloneCurrentForwardRecoverySnapshot(snapshot)
	if validateCurrentForwardRecoverySnapshotShape(snapshot) != nil ||
		snapshot.Receipt.Revision != command.ReceiptRevision ||
		snapshot.Receipt.LeaseOwnerKind != LeaseOwnerSweeper ||
		snapshot.Receipt.LeaseOwnerID != command.Attempt.ID ||
		snapshot.Receipt.FencingToken != command.Fence.Token ||
		!snapshot.Receipt.LeaseExpiresAt.Equal(command.Fence.ExpiresAt) {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	checkedAt, err := forwardRecoveryAuthorizationTime(observedAt.UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return err
	}
	// The transaction read time can be slightly later than the application
	// clock. Recheck the capability and fence at that conservative time so the
	// permitted clock skew cannot extend either expiry boundary.
	if err := ValidateForwardRecoveryActionAuthorization(grant, command, checkedAt); err != nil {
		return err
	}
	if validateCurrentForwardRecoveryAttempt(attempt, command, snapshot, checkedAt) != nil {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	lease := LeaseGrant{
		Fence:       command.Fence,
		OwnerKind:   snapshot.Receipt.LeaseOwnerKind,
		AcquiredAt:  snapshot.Receipt.LeaseAcquiredAt,
		HeartbeatAt: snapshot.Receipt.LeaseHeartbeatAt,
	}
	if err := evaluateCurrentForwardRecovery(
		snapshot,
		command.TenantID,
		command.ReservationKey,
		lease,
		checkedAt,
	); err != nil {
		return err
	}
	request, err := forwardRecoveryClassificationRequest(snapshot.Receipt)
	if err != nil || canonicalArtifactClassificationRequestBinding(request) != grant.requestBindingHash {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	if command.Plan.Action == ForwardRecoveryActionMarkHold &&
		(!checkedAt.Before(command.HoldReviewDueAt) ||
			!command.HoldReviewDueAt.Before(snapshot.Receipt.ArtifactExpiresAt)) {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	return nil
}

func boundedRecoveryHoldReviewDueAt(
	checkedAt time.Time,
	artifactExpiresAt time.Time,
) (time.Time, error) {
	if checkedAt.IsZero() || artifactExpiresAt.IsZero() || !checkedAt.Before(artifactExpiresAt) {
		return time.Time{}, ErrInvalidForwardRecoveryActionAuthorization
	}
	dueAt := checkedAt.Add(DefaultRecoveryHoldReviewWindow)
	if !dueAt.Before(artifactExpiresAt) {
		dueAt = artifactExpiresAt.Add(-time.Nanosecond)
	}
	if !checkedAt.Before(dueAt) {
		return time.Time{}, ErrInvalidForwardRecoveryActionAuthorization
	}
	return dueAt.UTC(), nil
}

func validateCurrentForwardRecoveryAttempt(
	attempt CurrentForwardRecoveryAttempt,
	command ForwardRecoveryActionCommand,
	snapshot CurrentForwardRecoverySnapshot,
	checkedAt time.Time,
) error {
	if attempt.AttemptID != command.Attempt.ID ||
		attempt.TenantID != command.TenantID ||
		attempt.ReceiptID != snapshot.Receipt.ReceiptID ||
		attempt.OwnerKind != LeaseOwnerSweeper ||
		attempt.FencingToken != command.Fence.Token ||
		attempt.WorkerVersion != command.Attempt.WorkerVersion ||
		attempt.Status != RecoveryAttemptStarted || attempt.StartedAt.IsZero() ||
		!attempt.StartedAt.Equal(snapshot.Receipt.LeaseAcquiredAt) ||
		attempt.StartedAt.After(checkedAt) {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	return nil
}

// ForwardRecoveryActionHash is a stable, non-secret correlation value for the
// exact authorized command. It contains no body, coordinate, UID or App ID.
func ForwardRecoveryActionHash(command ForwardRecoveryActionCommand) (string, error) {
	command = cloneForwardRecoveryActionCommand(command)
	if validateForwardRecoveryActionCommand(command) != nil {
		return "", ErrInvalidForwardRecoveryActionAuthorization
	}
	digest := canonicalForwardRecoveryActionBinding(command)
	return hex.EncodeToString(digest[:]), nil
}

func mintForwardRecoveryActionGrant(
	command ForwardRecoveryActionCommand,
	requestBinding [sha256.Size]byte,
	checkedAt time.Time,
	expiresAt time.Time,
) (ForwardRecoveryActionGrant, error) {
	if validateForwardRecoveryActionCommand(command) != nil || checkedAt.IsZero() || expiresAt.IsZero() ||
		!checkedAt.Before(expiresAt) || expiresAt.After(command.Fence.ExpiresAt) {
		return ForwardRecoveryActionGrant{}, ErrInvalidForwardRecoveryActionAuthorization
	}
	grant := ForwardRecoveryActionGrant{
		policyVersion:      ForwardRecoveryActionPolicyVersion,
		checkedAt:          checkedAt.UTC(),
		expiresAt:          expiresAt.UTC(),
		attemptID:          command.Attempt.ID,
		receiptRevision:    command.ReceiptRevision,
		forwardFence:       command.Fence,
		requestBindingHash: requestBinding,
		commandBindingHash: canonicalForwardRecoveryActionBinding(command),
	}
	grant.capabilitySeal = forwardRecoveryActionCapabilitySeal(grant)
	return grant, nil
}

func validateForwardRecoveryActionCommand(command ForwardRecoveryActionCommand) error {
	plan := command.Plan
	if !telemetry.IsUUID(command.TenantID) || !isLowerHexDigest(command.ReservationKey) ||
		ValidateRecoveryAttemptProposal(command.Attempt) != nil || command.ReceiptRevision <= 0 ||
		ValidateLeaseFence(command.Fence) != nil || command.Attempt.ID != command.Fence.OwnerID ||
		!validRecoveryActionPhase(plan.Phase) || !terminalForwardRecoveryAction(plan.Action) ||
		!validArtifactClassificationOutcome(ArtifactReadForwardRecovery, plan.Classification, plan.ReasonCode) {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	if plan.Raw != nil && validateArtifactLineage(plan.Raw, plan.Raw.Path) != nil {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	if plan.Manifest != nil && validateArtifactLineage(plan.Manifest, plan.Manifest.Path) != nil {
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	switch plan.Action {
	case ForwardRecoveryActionMarkStored:
		if plan.Phase != RecoveryPhaseConfirmation && plan.Phase != RecoveryPhasePostManifestConfirmation {
			return ErrInvalidForwardRecoveryActionAuthorization
		}
		if plan.Classification != ArtifactClassificationValidComplete ||
			plan.ReasonCode != ArtifactReasonManifestAndReferencedRawValid ||
			plan.Raw == nil || plan.Manifest == nil || plan.ReleaseCode != "" ||
			plan.HoldCode != "" || plan.RejectionCode != "" || !command.HoldReviewDueAt.IsZero() {
			return ErrInvalidForwardRecoveryActionAuthorization
		}
	case ForwardRecoveryActionMarkRejected:
		if plan.Phase != RecoveryPhaseConfirmation ||
			plan.Classification != ArtifactClassificationRawContentConflict ||
			plan.Raw == nil || plan.Manifest != nil || plan.RejectionCode != "object_conflict" ||
			plan.ReleaseCode != "" || plan.HoldCode != "" || !command.HoldReviewDueAt.IsZero() {
			return ErrInvalidForwardRecoveryActionAuthorization
		}
	case ForwardRecoveryActionMarkHold:
		if !ValidRecoveryHoldCode(plan.HoldCode) ||
			plan.HoldCode == RecoveryHoldCurrentAuthorizationDenied ||
			plan.Raw != nil || plan.Manifest != nil ||
			plan.ReleaseCode != "" || plan.RejectionCode != "" || command.HoldReviewDueAt.IsZero() {
			return ErrInvalidForwardRecoveryActionAuthorization
		}
	case ForwardRecoveryActionReleaseLease:
		if !ValidLeaseReleaseCode(plan.ReleaseCode) ||
			plan.ReleaseCode == LeaseReleaseAuthorizationUnavailable ||
			plan.Raw != nil || plan.Manifest != nil ||
			plan.HoldCode != "" || plan.RejectionCode != "" || !command.HoldReviewDueAt.IsZero() {
			return ErrInvalidForwardRecoveryActionAuthorization
		}
	default:
		return ErrInvalidForwardRecoveryActionAuthorization
	}
	return nil
}

func terminalForwardRecoveryAction(action ForwardRecoveryAction) bool {
	switch action {
	case ForwardRecoveryActionMarkStored,
		ForwardRecoveryActionMarkRejected,
		ForwardRecoveryActionMarkHold,
		ForwardRecoveryActionReleaseLease:
		return true
	default:
		return false
	}
}

func ValidRecoveryHoldCode(code RecoveryHoldCode) bool {
	switch code {
	case RecoveryHoldManifestOnly,
		RecoveryHoldManifestConflict,
		RecoveryHoldMetadataConflict,
		RecoveryHoldGenerationDrift,
		RecoveryHoldValidatorUnavailable,
		RecoveryHoldCodecUnavailable,
		RecoveryHoldInventoryIncomplete,
		RecoveryHoldResponseUnverifiable,
		RecoveryHoldArtifactPermissionDenied,
		RecoveryHoldCurrentAuthorizationDenied,
		RecoveryHoldConfirmationDrift,
		RecoveryHoldPostManifestDivergence:
		return true
	default:
		return false
	}
}

func cloneForwardRecoveryPlanInput(input ForwardRecoveryPlanInput) ForwardRecoveryPlanInput {
	input.Request = cloneArtifactClassificationRequest(input.Request)
	input.Result = cloneManifestRepairClassificationResult(input.Result)
	if input.PriorResult != nil {
		prior := cloneManifestRepairClassificationResult(*input.PriorResult)
		input.PriorResult = &prior
	}
	if input.WrittenManifest != nil {
		manifest := *input.WrittenManifest
		input.WrittenManifest = &manifest
	}
	return input
}

func cloneForwardRecoveryActionCommand(command ForwardRecoveryActionCommand) ForwardRecoveryActionCommand {
	command.Plan = cloneForwardRecoveryActionPlan(command.Plan)
	return command
}

func cloneForwardRecoveryActionPlan(plan ForwardRecoveryActionPlan) ForwardRecoveryActionPlan {
	if plan.Raw != nil {
		raw := *plan.Raw
		plan.Raw = &raw
	}
	if plan.Manifest != nil {
		manifest := *plan.Manifest
		plan.Manifest = &manifest
	}
	return plan
}

func canonicalForwardRecoveryActionBinding(command ForwardRecoveryActionCommand) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(forwardRecoveryActionBindingVersion)
	encoder.addString(command.TenantID)
	encoder.addString(command.ReservationKey)
	encoder.addString(command.Attempt.ID)
	encoder.addString(command.Attempt.WorkerVersion)
	encoder.addInt64(command.ReceiptRevision)
	encoder.addLeaseFence(&command.Fence)
	encoder.addString(string(command.Plan.Phase))
	encoder.addString(string(command.Plan.Action))
	encoder.addString(string(command.Plan.Classification))
	encoder.addString(string(command.Plan.ReasonCode))
	encoder.addString(string(command.Plan.ReleaseCode))
	encoder.addString(string(command.Plan.HoldCode))
	encoder.addString(command.Plan.RejectionCode)
	encoder.addArtifactLineage(command.Plan.Raw)
	encoder.addArtifactLineage(command.Plan.Manifest)
	encoder.addTime(command.HoldReviewDueAt)
	return encoder.sum()
}

func forwardRecoveryActionCapabilitySeal(grant ForwardRecoveryActionGrant) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(forwardRecoveryActionGrantVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addString(grant.attemptID)
	encoder.addInt64(grant.receiptRevision)
	encoder.addLeaseFence(&grant.forwardFence)
	encoder.addBytes(grant.requestBindingHash[:])
	encoder.addBytes(grant.commandBindingHash[:])
	return encoder.sum()
}

func sameLeaseFence(left, right LeaseFence) bool {
	return left.OwnerID == right.OwnerID && left.Token == right.Token &&
		left.ExpiresAt.Equal(right.ExpiresAt)
}
