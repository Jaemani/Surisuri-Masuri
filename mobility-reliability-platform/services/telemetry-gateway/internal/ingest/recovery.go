package ingest

import (
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	DefaultRequestLeaseDuration = 2 * time.Minute
	MinLeaseDuration            = 30 * time.Second
	MaxLeaseDuration            = 5 * time.Minute
	InitialRecoveryBackoff      = 5 * time.Second
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
}

type LeaseFence struct {
	OwnerID   string
	Token     int64
	ExpiresAt time.Time
}

type LeaseGrant struct {
	Fence      LeaseFence
	OwnerKind  LeaseOwnerKind
	AcquiredAt time.Time
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
		proposal.Duration < MinLeaseDuration || proposal.Duration > MaxLeaseDuration {
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
	duration := grant.Fence.ExpiresAt.Sub(grant.AcquiredAt)
	if ValidateLeaseFence(grant.Fence) != nil ||
		!validLeaseOwnerKind(grant.OwnerKind) ||
		grant.AcquiredAt.IsZero() || !grant.AcquiredAt.Before(grant.Fence.ExpiresAt) ||
		duration < MinLeaseDuration || duration > MaxLeaseDuration {
		return ErrInvalidLease
	}
	return nil
}

func validLeaseOwnerKind(kind LeaseOwnerKind) bool {
	switch kind {
	case LeaseOwnerRequest, LeaseOwnerSweeper, LeaseOwnerCleanup:
		return true
	default:
		return false
	}
}
