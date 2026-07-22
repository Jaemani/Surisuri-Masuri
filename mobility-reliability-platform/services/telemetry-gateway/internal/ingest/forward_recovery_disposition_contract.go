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
	ForwardRecoveryDispositionPolicyVersion = "forward-recovery-disposition.current-state@1"

	forwardRecoveryDispositionBindingVersion = "forward-recovery-disposition-command@1"
	forwardRecoveryDispositionGrantVersion   = "forward-recovery-disposition-grant@1"
)

var (
	ErrInvalidForwardRecoveryDispositionAuthorization = errors.New("forward recovery disposition authorization is invalid")
	ErrForwardRecoveryDispositionAuthorizationExpired = errors.New("forward recovery disposition authorization has expired")
	ErrForwardRecoveryDispositionNotRequired          = errors.New("forward recovery authorization disposition is not required")
)

// ForwardRecoveryAuthorizationDisposition is derived exclusively from one
// coherent current-state snapshot. Callers never choose the receipt action or
// its bounded reason code.
type ForwardRecoveryAuthorizationDisposition string

const (
	ForwardRecoveryAuthorizationDenied      ForwardRecoveryAuthorizationDisposition = "denied"
	ForwardRecoveryAuthorizationUnavailable ForwardRecoveryAuthorizationDisposition = "unavailable"
)

type ForwardRecoveryDecisionDomain string

const (
	ForwardRecoveryDecisionArtifactReconciliation ForwardRecoveryDecisionDomain = "artifact_reconciliation"
	ForwardRecoveryDecisionCurrentAuthorization   ForwardRecoveryDecisionDomain = "current_authorization"
)

func validForwardRecoveryDecisionDomain(domain ForwardRecoveryDecisionDomain) bool {
	return domain == ForwardRecoveryDecisionArtifactReconciliation ||
		domain == ForwardRecoveryDecisionCurrentAuthorization
}

// ForwardRecoveryDispositionCommand intentionally has no Action, HoldCode,
// ReleaseCode, provider error, artifact path or lineage fields. The domain maps
// denied to a current-authorization hold and unavailable to a bounded release.
type ForwardRecoveryDispositionCommand struct {
	TenantID        string
	ReservationKey  string
	Attempt         RecoveryAttemptProposal
	ReceiptRevision int64
	Fence           LeaseFence
	Disposition     ForwardRecoveryAuthorizationDisposition
	HoldReviewDueAt time.Time
}

// ForwardRecoveryDispositionGrant is not interchangeable with artifact read,
// manifest write, normal recovery action or attempt-only failure grants.
type ForwardRecoveryDispositionGrant struct {
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

type ForwardRecoveryDispositionStore interface {
	CommitForwardRecoveryDisposition(
		context.Context,
		ForwardRecoveryDispositionGrant,
		ForwardRecoveryDispositionCommand,
		time.Time,
	) (Receipt, error)
}

// AuthorizeForwardRecoveryDisposition mints a capability only when a coherent
// receipt/fence can be read and current relations evaluate to a bounded denial
// or a readable-but-invalid unavailable state. Store/transport failures never
// mint a release capability because they provide no current-state authority.
func (a *SystemRecoveryAuthorizer) AuthorizeForwardRecoveryDisposition(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
	attempt RecoveryAttemptProposal,
) (ForwardRecoveryDispositionCommand, ForwardRecoveryDispositionGrant, error) {
	if a == nil || a.store == nil || a.now == nil || ctx == nil ||
		validateForwardRecoveryAuthorizationInput(tenantID, reservationKey, lease) != nil ||
		ValidateRecoveryAttemptProposal(attempt) != nil || attempt.ID != lease.Fence.OwnerID {
		return ForwardRecoveryDispositionCommand{}, ForwardRecoveryDispositionGrant{},
			ErrInvalidForwardRecoveryDispositionAuthorization
	}
	if err := ctx.Err(); err != nil {
		return ForwardRecoveryDispositionCommand{}, ForwardRecoveryDispositionGrant{}, err
	}
	snapshot, err := a.store.LoadCurrentForwardRecovery(ctx, ForwardRecoveryAuthorizationQuery{
		TenantID: tenantID, ReservationKey: reservationKey,
	})
	if err != nil {
		return ForwardRecoveryDispositionCommand{}, ForwardRecoveryDispositionGrant{},
			normalizeForwardRecoveryAuthorizationError(ctx, err)
	}
	snapshot = cloneCurrentForwardRecoverySnapshot(snapshot)
	checkedAt, err := forwardRecoveryAuthorizationTime(a.now().UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return ForwardRecoveryDispositionCommand{}, ForwardRecoveryDispositionGrant{}, err
	}
	disposition, requestBinding, err := deriveForwardRecoveryAuthorizationDisposition(
		snapshot, tenantID, reservationKey, lease, checkedAt,
	)
	if err != nil {
		return ForwardRecoveryDispositionCommand{}, ForwardRecoveryDispositionGrant{}, err
	}
	if disposition == "" {
		return ForwardRecoveryDispositionCommand{}, ForwardRecoveryDispositionGrant{},
			ErrForwardRecoveryDispositionNotRequired
	}
	command := ForwardRecoveryDispositionCommand{
		TenantID: tenantID, ReservationKey: reservationKey, Attempt: attempt,
		ReceiptRevision: snapshot.Receipt.Revision,
		Fence: LeaseFence{
			OwnerID: snapshot.Receipt.LeaseOwnerID, Token: snapshot.Receipt.FencingToken,
			ExpiresAt: snapshot.Receipt.LeaseExpiresAt,
		},
		Disposition: disposition,
	}
	if disposition == ForwardRecoveryAuthorizationDenied {
		command.HoldReviewDueAt, err = boundedRecoveryHoldReviewDueAt(
			checkedAt, snapshot.Receipt.ArtifactExpiresAt,
		)
		if err != nil {
			return ForwardRecoveryDispositionCommand{}, ForwardRecoveryDispositionGrant{},
				ErrInvalidForwardRecoveryDispositionAuthorization
		}
	}
	expiresAt := earlierRecoveryTime(
		checkedAt.Add(ForwardRecoveryArtifactReadGrantTTL),
		command.Fence.ExpiresAt,
	)
	expiresAt = earlierRecoveryTime(expiresAt, snapshot.Receipt.ReservationDeadline)
	grant, err := mintForwardRecoveryDispositionGrant(
		command, requestBinding, checkedAt, expiresAt,
	)
	if err != nil {
		return ForwardRecoveryDispositionCommand{}, ForwardRecoveryDispositionGrant{},
			ErrInvalidForwardRecoveryDispositionAuthorization
	}
	return command, grant, nil
}

func ValidateForwardRecoveryDispositionAuthorization(
	grant ForwardRecoveryDispositionGrant,
	command ForwardRecoveryDispositionCommand,
	observedAt time.Time,
) error {
	if validateForwardRecoveryDispositionCommand(command) != nil || observedAt.IsZero() ||
		grant.policyVersion != ForwardRecoveryDispositionPolicyVersion || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.attemptID != command.Attempt.ID || grant.receiptRevision != command.ReceiptRevision ||
		!sameLeaseFence(grant.forwardFence, command.Fence) ||
		grant.commandBindingHash != canonicalForwardRecoveryDispositionBinding(command) ||
		grant.capabilitySeal != forwardRecoveryDispositionCapabilitySeal(grant) ||
		observedAt.Before(grant.checkedAt) {
		return ErrInvalidForwardRecoveryDispositionAuthorization
	}
	if !observedAt.Before(grant.expiresAt) || !observedAt.Before(grant.forwardFence.ExpiresAt) {
		return ErrForwardRecoveryDispositionAuthorizationExpired
	}
	return nil
}

func ForwardRecoveryDispositionAuthorizationDeadline(
	grant ForwardRecoveryDispositionGrant,
	command ForwardRecoveryDispositionCommand,
) (time.Time, error) {
	if validateForwardRecoveryDispositionCommand(command) != nil ||
		grant.policyVersion != ForwardRecoveryDispositionPolicyVersion || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.attemptID != command.Attempt.ID || grant.receiptRevision != command.ReceiptRevision ||
		!sameLeaseFence(grant.forwardFence, command.Fence) ||
		grant.commandBindingHash != canonicalForwardRecoveryDispositionBinding(command) ||
		grant.capabilitySeal != forwardRecoveryDispositionCapabilitySeal(grant) {
		return time.Time{}, ErrInvalidForwardRecoveryDispositionAuthorization
	}
	return earlierRecoveryTime(grant.expiresAt, grant.forwardFence.ExpiresAt), nil
}

func ValidateCurrentForwardRecoveryDisposition(
	grant ForwardRecoveryDispositionGrant,
	command ForwardRecoveryDispositionCommand,
	snapshot CurrentForwardRecoverySnapshot,
	attempt CurrentForwardRecoveryAttempt,
	observedAt time.Time,
) error {
	if err := ValidateForwardRecoveryDispositionAuthorization(grant, command, observedAt); err != nil {
		return err
	}
	snapshot = cloneCurrentForwardRecoverySnapshot(snapshot)
	if snapshot.Receipt.Revision != command.ReceiptRevision ||
		snapshot.Receipt.LeaseOwnerKind != LeaseOwnerSweeper ||
		snapshot.Receipt.LeaseOwnerID != command.Attempt.ID ||
		snapshot.Receipt.FencingToken != command.Fence.Token ||
		!snapshot.Receipt.LeaseExpiresAt.Equal(command.Fence.ExpiresAt) {
		return ErrInvalidForwardRecoveryDispositionAuthorization
	}
	checkedAt, err := forwardRecoveryAuthorizationTime(observedAt.UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return err
	}
	if err := ValidateForwardRecoveryDispositionAuthorization(grant, command, checkedAt); err != nil {
		return err
	}
	if validateCurrentForwardRecoveryDispositionAttempt(attempt, command, snapshot, checkedAt) != nil {
		return ErrInvalidForwardRecoveryDispositionAuthorization
	}
	lease := LeaseGrant{
		Fence: command.Fence, OwnerKind: snapshot.Receipt.LeaseOwnerKind,
		AcquiredAt:  snapshot.Receipt.LeaseAcquiredAt,
		HeartbeatAt: snapshot.Receipt.LeaseHeartbeatAt,
	}
	disposition, requestBinding, err := deriveForwardRecoveryAuthorizationDisposition(
		snapshot, command.TenantID, command.ReservationKey, lease, checkedAt,
	)
	if err != nil || disposition != command.Disposition || requestBinding != grant.requestBindingHash {
		return ErrInvalidForwardRecoveryDispositionAuthorization
	}
	if command.Disposition == ForwardRecoveryAuthorizationDenied &&
		(!checkedAt.Before(command.HoldReviewDueAt) ||
			!command.HoldReviewDueAt.Before(snapshot.Receipt.ArtifactExpiresAt)) {
		return ErrInvalidForwardRecoveryDispositionAuthorization
	}
	return nil
}

func ForwardRecoveryDispositionHash(command ForwardRecoveryDispositionCommand) (string, error) {
	if validateForwardRecoveryDispositionCommand(command) != nil {
		return "", ErrInvalidForwardRecoveryDispositionAuthorization
	}
	digest := canonicalForwardRecoveryDispositionBinding(command)
	return hex.EncodeToString(digest[:]), nil
}

func deriveForwardRecoveryAuthorizationDisposition(
	snapshot CurrentForwardRecoverySnapshot,
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
	checkedAt time.Time,
) (ForwardRecoveryAuthorizationDisposition, [sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	if checkedAt.IsZero() || validateForwardRecoveryReceiptEligibility(
		snapshot.Receipt, tenantID, reservationKey, lease,
	) != nil {
		return "", zero, ErrInvalidForwardRecoveryDispositionAuthorization
	}
	request, err := forwardRecoveryClassificationRequest(snapshot.Receipt)
	if err != nil || request.ForwardFence == nil ||
		!sameLeaseFence(*request.ForwardFence, lease.Fence) ||
		!checkedAt.Before(snapshot.Receipt.LeaseExpiresAt) ||
		!checkedAt.Before(snapshot.Receipt.ReservationDeadline) {
		return "", zero, ErrInvalidForwardRecoveryDispositionAuthorization
	}
	requestBinding := canonicalArtifactClassificationRequestBinding(request)
	if validateCurrentForwardRecoverySnapshotShape(snapshot) != nil {
		return ForwardRecoveryAuthorizationUnavailable, requestBinding, nil
	}
	err = evaluateCurrentForwardRecovery(snapshot, tenantID, reservationKey, lease, checkedAt)
	switch {
	case err == nil:
		return "", requestBinding, nil
	case errors.Is(err, ErrForwardRecoveryUnauthorized):
		return ForwardRecoveryAuthorizationDenied, requestBinding, nil
	case errors.Is(err, ErrForwardRecoveryAuthorizationUnavailable):
		return ForwardRecoveryAuthorizationUnavailable, requestBinding, nil
	default:
		return "", zero, ErrInvalidForwardRecoveryDispositionAuthorization
	}
}

func validateCurrentForwardRecoveryDispositionAttempt(
	attempt CurrentForwardRecoveryAttempt,
	command ForwardRecoveryDispositionCommand,
	snapshot CurrentForwardRecoverySnapshot,
	checkedAt time.Time,
) error {
	if attempt.AttemptID != command.Attempt.ID || attempt.TenantID != command.TenantID ||
		attempt.ReceiptID != snapshot.Receipt.ReceiptID || attempt.OwnerKind != LeaseOwnerSweeper ||
		attempt.FencingToken != command.Fence.Token ||
		attempt.WorkerVersion != command.Attempt.WorkerVersion ||
		attempt.Status != RecoveryAttemptStarted || attempt.StartedAt.IsZero() ||
		!attempt.StartedAt.Equal(snapshot.Receipt.LeaseAcquiredAt) || attempt.StartedAt.After(checkedAt) {
		return ErrInvalidForwardRecoveryDispositionAuthorization
	}
	return nil
}

func mintForwardRecoveryDispositionGrant(
	command ForwardRecoveryDispositionCommand,
	requestBinding [sha256.Size]byte,
	checkedAt time.Time,
	expiresAt time.Time,
) (ForwardRecoveryDispositionGrant, error) {
	if validateForwardRecoveryDispositionCommand(command) != nil || checkedAt.IsZero() ||
		expiresAt.IsZero() || !checkedAt.Before(expiresAt) || expiresAt.After(command.Fence.ExpiresAt) {
		return ForwardRecoveryDispositionGrant{}, ErrInvalidForwardRecoveryDispositionAuthorization
	}
	grant := ForwardRecoveryDispositionGrant{
		policyVersion: ForwardRecoveryDispositionPolicyVersion,
		checkedAt:     checkedAt.UTC(), expiresAt: expiresAt.UTC(),
		attemptID: command.Attempt.ID, receiptRevision: command.ReceiptRevision,
		forwardFence: command.Fence, requestBindingHash: requestBinding,
		commandBindingHash: canonicalForwardRecoveryDispositionBinding(command),
	}
	grant.capabilitySeal = forwardRecoveryDispositionCapabilitySeal(grant)
	return grant, nil
}

func validateForwardRecoveryDispositionCommand(command ForwardRecoveryDispositionCommand) error {
	if !telemetry.IsUUID(command.TenantID) || !isLowerHexDigest(command.ReservationKey) ||
		ValidateRecoveryAttemptProposal(command.Attempt) != nil || command.ReceiptRevision <= 0 ||
		ValidateLeaseFence(command.Fence) != nil || command.Attempt.ID != command.Fence.OwnerID {
		return ErrInvalidForwardRecoveryDispositionAuthorization
	}
	switch command.Disposition {
	case ForwardRecoveryAuthorizationDenied:
		if command.HoldReviewDueAt.IsZero() || !command.HoldReviewDueAt.Before(command.Fence.ExpiresAt.Add(TelemetryArtifactRetention)) {
			return ErrInvalidForwardRecoveryDispositionAuthorization
		}
	case ForwardRecoveryAuthorizationUnavailable:
		if !command.HoldReviewDueAt.IsZero() {
			return ErrInvalidForwardRecoveryDispositionAuthorization
		}
	default:
		return ErrInvalidForwardRecoveryDispositionAuthorization
	}
	return nil
}

func canonicalForwardRecoveryDispositionBinding(
	command ForwardRecoveryDispositionCommand,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(forwardRecoveryDispositionBindingVersion)
	encoder.addString(command.TenantID)
	encoder.addString(command.ReservationKey)
	encoder.addString(command.Attempt.ID)
	encoder.addString(command.Attempt.WorkerVersion)
	encoder.addInt64(command.ReceiptRevision)
	encoder.addLeaseFence(&command.Fence)
	encoder.addString(string(command.Disposition))
	encoder.addTime(command.HoldReviewDueAt)
	return encoder.sum()
}

func forwardRecoveryDispositionCapabilitySeal(
	grant ForwardRecoveryDispositionGrant,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(forwardRecoveryDispositionGrantVersion)
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
