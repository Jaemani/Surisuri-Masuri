package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	DefaultRequestLeaseDuration  = 2 * time.Minute
	MinLeaseDuration             = 30 * time.Second
	MaxLeaseDuration             = 5 * time.Minute
	LeaseRenewalWindow           = 45 * time.Second
	InitialRecoveryBackoff       = 5 * time.Second
	RecoveryWorkerVersion        = "telemetry-recovery.v1"
	DefaultCleanupLateWriteGrace = 6 * time.Minute
)

var ErrInvalidLease = errors.New("telemetry ingest lease is invalid")

type LeaseOwnerKind string

const (
	LeaseOwnerRequest LeaseOwnerKind = "request"
	LeaseOwnerSweeper LeaseOwnerKind = "sweeper"
	LeaseOwnerCleanup LeaseOwnerKind = "cleanup"
)

type LeaseOwner struct {
	ID   string
	Kind LeaseOwnerKind
}

type LeaseProposal struct {
	Owner    LeaseOwner
	Duration time.Duration
	Attempt  RecoveryAttemptProposal
}

type LeaseFence struct {
	OwnerID   string
	Token     int64
	ExpiresAt time.Time
}

type LeaseGrant struct {
	Fence       LeaseFence
	OwnerKind   LeaseOwnerKind
	AcquiredAt  time.Time
	HeartbeatAt time.Time
}

type RecoveryAttemptProposal struct {
	ID            string
	WorkerVersion string
}

type RecoveryAttemptStatus string

const RecoveryAttemptStarted RecoveryAttemptStatus = "started"

type LeaseStatus string

const (
	LeaseStatusAcquired        LeaseStatus = "lease_acquired"
	LeaseStatusHeld            LeaseStatus = "lease_held"
	LeaseStatusNotDue          LeaseStatus = "lease_not_due"
	LeaseStatusDeadlineElapsed LeaseStatus = "deadline_elapsed"
	LeaseStatusNotEligible     LeaseStatus = "not_eligible"
)

type CleanupMode string

const CleanupModeReservationExpiry CleanupMode = "reservation_expiry"

type TransitionStatus string

const (
	TransitionStatusStarted        TransitionStatus = "transition_started"
	TransitionStatusAlreadyStarted TransitionStatus = "transition_already_started"
	TransitionStatusNotReady       TransitionStatus = "transition_not_ready"
	TransitionStatusNotEligible    TransitionStatus = "transition_not_eligible"
)

type RecoveryLeaseStore interface {
	ClaimRecoveryLease(
		context.Context,
		string,
		string,
		LeaseOwner,
		RecoveryAttemptProposal,
		time.Time,
		time.Duration,
	) (LeaseGrant, LeaseStatus, error)
	RenewLease(context.Context, string, string, LeaseFence, time.Time, time.Duration) (LeaseGrant, error)
}

type CleanupTransitionStore interface {
	BeginCleanupTransition(context.Context, string, string, time.Time) (Receipt, TransitionStatus, error)
}

type LeaseReleaseCode string

const (
	LeaseReleaseArtifactUnavailable  LeaseReleaseCode = "artifact_unavailable"
	LeaseReleaseFinalizerUnavailable LeaseReleaseCode = "finalizer_unavailable"
)

func ValidLeaseReleaseCode(code LeaseReleaseCode) bool {
	switch code {
	case LeaseReleaseArtifactUnavailable, LeaseReleaseFinalizerUnavailable:
		return true
	default:
		return false
	}
}

func ValidateLeaseProposal(proposal LeaseProposal) error {
	if !telemetry.IsUUID(proposal.Owner.ID) ||
		!validLeaseOwnerKind(proposal.Owner.Kind) ||
		proposal.Duration < MinLeaseDuration || proposal.Duration > MaxLeaseDuration ||
		(!emptyRecoveryAttemptProposal(proposal.Attempt) && ValidateRecoveryAttemptProposal(proposal.Attempt) != nil) {
		return ErrInvalidLease
	}
	return nil
}

func ValidateRecoveryAttemptProposal(proposal RecoveryAttemptProposal) error {
	if !telemetry.IsUUID(proposal.ID) || !validWorkerVersion(proposal.WorkerVersion) {
		return ErrInvalidLease
	}
	return nil
}

func ValidateLeaseFence(fence LeaseFence) error {
	if !telemetry.IsUUID(fence.OwnerID) || fence.Token <= 0 || fence.ExpiresAt.IsZero() {
		return ErrInvalidLease
	}
	return nil
}

func ValidateLeaseGrant(grant LeaseGrant) error {
	duration := grant.Fence.ExpiresAt.Sub(grant.HeartbeatAt)
	if ValidateLeaseFence(grant.Fence) != nil ||
		!validLeaseOwnerKind(grant.OwnerKind) ||
		grant.AcquiredAt.IsZero() || grant.HeartbeatAt.IsZero() ||
		grant.HeartbeatAt.Before(grant.AcquiredAt) ||
		!grant.HeartbeatAt.Before(grant.Fence.ExpiresAt) ||
		duration < MinLeaseDuration || duration > MaxLeaseDuration {
		return ErrInvalidLease
	}
	return nil
}

func emptyRecoveryAttemptProposal(proposal RecoveryAttemptProposal) bool {
	return proposal.ID == "" && proposal.WorkerVersion == ""
}

func validWorkerVersion(version string) bool {
	// Worker versions are server-controlled provenance, not caller-provided
	// labels. Keep the accepted set explicit so identifiers such as emails,
	// Firebase UIDs or App IDs cannot be smuggled into the attempt ledger.
	return version == RecoveryWorkerVersion
}

func validLeaseOwnerKind(kind LeaseOwnerKind) bool {
	switch kind {
	case LeaseOwnerRequest, LeaseOwnerSweeper, LeaseOwnerCleanup:
		return true
	default:
		return false
	}
}
