package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	ForwardRecoveryAttemptFailurePolicyVersion = "forward-recovery-attempt-failure.current-state@1"

	forwardRecoveryAttemptFailureBindingVersion = "forward-recovery-attempt-failure-command@1"
	forwardRecoveryAttemptFailureGrantVersion   = "forward-recovery-attempt-failure-grant@1"
)

var (
	ErrInvalidForwardRecoveryAttemptAuthorization = errors.New("forward recovery attempt authorization is invalid")
	ErrForwardRecoveryAttemptAuthorizationExpired = errors.New("forward recovery attempt authorization has expired")
)

type RecoveryAttemptFailureCode string

const (
	RecoveryAttemptFailureInvalidContract RecoveryAttemptFailureCode = "invalid_contract"
	RecoveryAttemptFailureCallerCanceled  RecoveryAttemptFailureCode = "caller_canceled"
	RecoveryAttemptFailureCallerDeadline  RecoveryAttemptFailureCode = "caller_deadline"
	RecoveryAttemptFailureFinalizerAbort  RecoveryAttemptFailureCode = "finalizer_aborted"
	// LeaseExpired is reserved for the claim transaction that closes an exact
	// prior started attempt. A live worker cannot request this disposition.
	RecoveryAttemptFailureLeaseExpired RecoveryAttemptFailureCode = "lease_expired"
)

func ValidRecoveryAttemptFailureCode(code RecoveryAttemptFailureCode) bool {
	switch code {
	case RecoveryAttemptFailureInvalidContract,
		RecoveryAttemptFailureCallerCanceled,
		RecoveryAttemptFailureCallerDeadline,
		RecoveryAttemptFailureFinalizerAbort,
		RecoveryAttemptFailureLeaseExpired:
		return true
	default:
		return false
	}
}

func validLiveRecoveryAttemptFailureCode(code RecoveryAttemptFailureCode) bool {
	return ValidRecoveryAttemptFailureCode(code) && code != RecoveryAttemptFailureLeaseExpired
}

// ForwardRecoveryAttemptFailure is a bounded, constructible description. It
// contains no provider error string, artifact path, body, identity or location.
// The opaque grant is the authority to write it.
type ForwardRecoveryAttemptFailure struct {
	TenantID        string
	ReservationKey  string
	Attempt         RecoveryAttemptProposal
	ReceiptRevision int64
	Fence           LeaseFence
	FailureCode     RecoveryAttemptFailureCode
}

type ForwardRecoveryAttemptGrant struct {
	policyVersion      string
	checkedAt          time.Time
	expiresAt          time.Time
	attemptID          string
	receiptRevision    int64
	forwardFence       LeaseFence
	failureBindingHash [sha256.Size]byte
	capabilitySeal     [sha256.Size]byte
}

type ForwardRecoveryAttemptStore interface {
	FailForwardRecoveryAttempt(
		context.Context,
		ForwardRecoveryAttemptGrant,
		ForwardRecoveryAttemptFailure,
		time.Time,
	) error
}

func (a *SystemRecoveryAuthorizer) AuthorizeForwardRecoveryAttemptFailure(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
	attempt RecoveryAttemptProposal,
	failureCode RecoveryAttemptFailureCode,
) (ForwardRecoveryAttemptFailure, ForwardRecoveryAttemptGrant, error) {
	if a == nil || a.validator == nil || ValidateRecoveryAttemptProposal(attempt) != nil ||
		ValidateLeaseGrant(lease) != nil || lease.OwnerKind != LeaseOwnerSweeper ||
		attempt.ID != lease.Fence.OwnerID || !validLiveRecoveryAttemptFailureCode(failureCode) {
		return ForwardRecoveryAttemptFailure{}, ForwardRecoveryAttemptGrant{}, ErrInvalidForwardRecoveryAttemptAuthorization
	}
	request, readGrant, err := a.Authorize(ctx, tenantID, reservationKey, lease)
	if err != nil {
		return ForwardRecoveryAttemptFailure{}, ForwardRecoveryAttemptGrant{}, err
	}
	failure := ForwardRecoveryAttemptFailure{
		TenantID: tenantID, ReservationKey: reservationKey, Attempt: attempt,
		ReceiptRevision: request.ReceiptRevision, Fence: *request.ForwardFence,
		FailureCode: failureCode,
	}
	grant, err := mintForwardRecoveryAttemptGrant(
		failure,
		readGrant.checkedAt,
		readGrant.expiresAt,
	)
	if err != nil {
		return ForwardRecoveryAttemptFailure{}, ForwardRecoveryAttemptGrant{}, ErrInvalidForwardRecoveryAttemptAuthorization
	}
	return failure, grant, nil
}

func ValidateForwardRecoveryAttemptAuthorization(
	grant ForwardRecoveryAttemptGrant,
	failure ForwardRecoveryAttemptFailure,
	observedAt time.Time,
) error {
	if validateForwardRecoveryAttemptFailure(failure) != nil || observedAt.IsZero() ||
		!validArtifactServerLabel(grant.policyVersion) || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.attemptID != failure.Attempt.ID || grant.receiptRevision != failure.ReceiptRevision ||
		!sameLeaseFence(grant.forwardFence, failure.Fence) ||
		grant.failureBindingHash != canonicalForwardRecoveryAttemptFailureBinding(failure) ||
		grant.capabilitySeal != forwardRecoveryAttemptCapabilitySeal(grant) ||
		observedAt.Before(grant.checkedAt) {
		return ErrInvalidForwardRecoveryAttemptAuthorization
	}
	if !observedAt.Before(grant.expiresAt) || !observedAt.Before(grant.forwardFence.ExpiresAt) {
		return ErrForwardRecoveryAttemptAuthorizationExpired
	}
	return nil
}

func ForwardRecoveryAttemptAuthorizationDeadline(
	grant ForwardRecoveryAttemptGrant,
	failure ForwardRecoveryAttemptFailure,
) (time.Time, error) {
	if validateForwardRecoveryAttemptFailure(failure) != nil ||
		!validArtifactServerLabel(grant.policyVersion) || grant.checkedAt.IsZero() ||
		grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.attemptID != failure.Attempt.ID || grant.receiptRevision != failure.ReceiptRevision ||
		!sameLeaseFence(grant.forwardFence, failure.Fence) ||
		grant.failureBindingHash != canonicalForwardRecoveryAttemptFailureBinding(failure) ||
		grant.capabilitySeal != forwardRecoveryAttemptCapabilitySeal(grant) {
		return time.Time{}, ErrInvalidForwardRecoveryAttemptAuthorization
	}
	return earlierRecoveryTime(grant.expiresAt, grant.forwardFence.ExpiresAt), nil
}

func ValidateCurrentForwardRecoveryAttemptFailure(
	grant ForwardRecoveryAttemptGrant,
	failure ForwardRecoveryAttemptFailure,
	receipt Receipt,
	attempt CurrentForwardRecoveryAttempt,
	observedAt time.Time,
) error {
	if err := ValidateForwardRecoveryAttemptAuthorization(grant, failure, observedAt); err != nil {
		return err
	}
	request, err := forwardRecoveryClassificationRequest(receipt)
	if err != nil || request.ReceiptRevision != failure.ReceiptRevision ||
		request.TenantID != failure.TenantID || request.ReservationKey != failure.ReservationKey ||
		request.ForwardFence == nil || !sameLeaseFence(*request.ForwardFence, failure.Fence) ||
		attempt.AttemptID != failure.Attempt.ID || attempt.TenantID != failure.TenantID ||
		attempt.ReceiptID != receipt.ReceiptID || attempt.OwnerKind != LeaseOwnerSweeper ||
		attempt.FencingToken != failure.Fence.Token ||
		attempt.WorkerVersion != failure.Attempt.WorkerVersion ||
		attempt.Status != RecoveryAttemptStarted || attempt.StartedAt.IsZero() ||
		!attempt.StartedAt.Equal(receipt.LeaseAcquiredAt) || attempt.StartedAt.After(observedAt) {
		return ErrInvalidForwardRecoveryAttemptAuthorization
	}
	return nil
}

func mintForwardRecoveryAttemptGrant(
	failure ForwardRecoveryAttemptFailure,
	checkedAt time.Time,
	expiresAt time.Time,
) (ForwardRecoveryAttemptGrant, error) {
	if validateForwardRecoveryAttemptFailure(failure) != nil || checkedAt.IsZero() || expiresAt.IsZero() ||
		!checkedAt.Before(expiresAt) || expiresAt.After(failure.Fence.ExpiresAt) {
		return ForwardRecoveryAttemptGrant{}, ErrInvalidForwardRecoveryAttemptAuthorization
	}
	grant := ForwardRecoveryAttemptGrant{
		policyVersion: ForwardRecoveryAttemptFailurePolicyVersion,
		checkedAt:     checkedAt.UTC(), expiresAt: expiresAt.UTC(),
		attemptID: failure.Attempt.ID, receiptRevision: failure.ReceiptRevision,
		forwardFence:       failure.Fence,
		failureBindingHash: canonicalForwardRecoveryAttemptFailureBinding(failure),
	}
	grant.capabilitySeal = forwardRecoveryAttemptCapabilitySeal(grant)
	return grant, nil
}

func validateForwardRecoveryAttemptFailure(failure ForwardRecoveryAttemptFailure) error {
	if !telemetry.IsUUID(failure.TenantID) || !isLowerHexDigest(failure.ReservationKey) ||
		ValidateRecoveryAttemptProposal(failure.Attempt) != nil || failure.ReceiptRevision <= 0 ||
		ValidateLeaseFence(failure.Fence) != nil || failure.Attempt.ID != failure.Fence.OwnerID ||
		!validLiveRecoveryAttemptFailureCode(failure.FailureCode) {
		return ErrInvalidForwardRecoveryAttemptAuthorization
	}
	return nil
}

func canonicalForwardRecoveryAttemptFailureBinding(
	failure ForwardRecoveryAttemptFailure,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(forwardRecoveryAttemptFailureBindingVersion)
	encoder.addString(failure.TenantID)
	encoder.addString(failure.ReservationKey)
	encoder.addString(failure.Attempt.ID)
	encoder.addString(failure.Attempt.WorkerVersion)
	encoder.addInt64(failure.ReceiptRevision)
	encoder.addLeaseFence(&failure.Fence)
	encoder.addString(string(failure.FailureCode))
	return encoder.sum()
}

func forwardRecoveryAttemptCapabilitySeal(
	grant ForwardRecoveryAttemptGrant,
) [sha256.Size]byte {
	encoder := newArtifactBindingEncoder(forwardRecoveryAttemptFailureGrantVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addString(grant.attemptID)
	encoder.addInt64(grant.receiptRevision)
	encoder.addLeaseFence(&grant.forwardFence)
	encoder.addBytes(grant.failureBindingHash[:])
	return encoder.sum()
}
