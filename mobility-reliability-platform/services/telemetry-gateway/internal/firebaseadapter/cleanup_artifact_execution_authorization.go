package firebaseadapter

import (
	"crypto/sha256"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const (
	CleanupArtifactExecutionPolicyVersion           = "cleanup-artifact-execution.firestore-ledger@2"
	CleanupArtifactExecutionMutationTTL             = 30 * time.Second
	CleanupArtifactExecutionOutcomePersistenceGrace = 5 * time.Second

	cleanupArtifactExecutionCapabilityBindingVersion = "cleanup-artifact-execution-firestore-capability@2"
)

var ErrCleanupArtifactExecutionAuthorizationExpired = errors.New("cleanup artifact execution authorization has expired")

// CleanupArtifactExecutionAuthorizationGrant is minted only after the same
// Firestore transaction durably records the exact artifact dispatch. A replay
// receives a zero grant and therefore cannot reach the provider mutation port.
type CleanupArtifactExecutionAuthorizationGrant struct {
	policyVersion     string
	checkedAt         time.Time
	mutationExpiresAt time.Time
	outcomeExpiresAt  time.Time
	requestHash       string
	targetHash        string
	planHash          string
	receiptRevision   int64
	ownerID           string
	fencingToken      int64
	leaseExpiresAt    time.Time
	dispatchRevision  int64
	dispatchPhase     ingest.CleanupExecutionPhase
	artifact          ingest.CleanupArtifactExecutionArtifact
	targeted          bool
	capabilitySeal    [sha256.Size]byte
}

func ValidateCleanupArtifactExecutionAuthorization(
	grant CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
	observedAt time.Time,
) error {
	if err := validateCleanupArtifactExecutionAuthorizationBinding(grant, request); err != nil {
		return err
	}
	return validateCleanupArtifactExecutionAuthorizationTime(
		observedAt, grant.checkedAt, grant.mutationExpiresAt,
	)
}

// ValidateCleanupArtifactExecutionOutcomeAuthorization accepts a result after
// the provider mutation deadline only when the mutation started inside that
// deadline and completion was observed inside the bounded persistence grace.
func ValidateCleanupArtifactExecutionOutcomeAuthorization(
	grant CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
	result ingest.CleanupArtifactExecutionResult,
) error {
	if err := validateCleanupArtifactExecutionAuthorizationBinding(grant, request); err != nil {
		return err
	}
	if ingest.ValidateCleanupArtifactExecutionResult(request, result) != nil {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	if !result.MutationStartedAt.IsZero() {
		if err := validateCleanupArtifactExecutionAuthorizationTime(
			result.MutationStartedAt, grant.checkedAt, grant.mutationExpiresAt,
		); err != nil {
			return err
		}
	}
	return validateCleanupArtifactExecutionAuthorizationTime(
		result.ObservedAt, grant.checkedAt, grant.outcomeExpiresAt,
	)
}

// ValidateCleanupArtifactExecutionOutcomePersistence bounds the trusted
// Firestore application/transaction time, rather than trusting only the
// caller-supplied result timestamp.
func ValidateCleanupArtifactExecutionOutcomePersistence(
	grant CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
	observedAt time.Time,
) error {
	if err := validateCleanupArtifactExecutionAuthorizationBinding(grant, request); err != nil {
		return err
	}
	return validateCleanupArtifactExecutionAuthorizationTime(
		observedAt, grant.checkedAt, grant.outcomeExpiresAt,
	)
}

func validateCleanupArtifactExecutionAuthorizationBinding(
	grant CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
) error {
	request, err := ingest.CloneCleanupArtifactExecutionRequest(request)
	expectedMutationExpiry, expectedOutcomeExpiry, windowErr :=
		cleanupArtifactExecutionAuthorizationWindow(grant.checkedAt, grant.leaseExpiresAt)
	if err != nil || windowErr != nil ||
		grant.policyVersion != CleanupArtifactExecutionPolicyVersion ||
		grant.checkedAt.IsZero() || grant.mutationExpiresAt.IsZero() ||
		grant.outcomeExpiresAt.IsZero() ||
		!grant.mutationExpiresAt.Equal(expectedMutationExpiry) ||
		!grant.outcomeExpiresAt.Equal(expectedOutcomeExpiry) ||
		grant.requestHash != request.RequestHash ||
		grant.targetHash != request.ExpectedTargetHash ||
		grant.planHash != request.ExpectedPlanHash ||
		grant.receiptRevision != request.ExpectedReceiptRevision ||
		grant.ownerID != request.ExpectedFence.OwnerID ||
		grant.fencingToken != request.ExpectedFence.Token ||
		!grant.leaseExpiresAt.Equal(request.ExpectedFence.ExpiresAt) ||
		grant.dispatchRevision != request.DispatchRevision ||
		grant.dispatchPhase != request.DispatchPhase ||
		grant.artifact != request.Artifact || grant.targeted != request.Targeted ||
		grant.capabilitySeal != cleanupArtifactExecutionCapabilitySeal(grant) {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	return nil
}

func validateCleanupArtifactExecutionAuthorizationTime(
	observedAt time.Time,
	checkedAt time.Time,
	expiresAt time.Time,
) error {
	if observedAt.IsZero() {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	effectiveAt := observedAt.UTC()
	if effectiveAt.Before(checkedAt) {
		if !withinAdmissionClockSkew(effectiveAt, checkedAt) {
			return ingest.ErrCleanupExecutionUnavailable
		}
		effectiveAt = checkedAt
	}
	if !effectiveAt.Before(expiresAt) {
		return ErrCleanupArtifactExecutionAuthorizationExpired
	}
	return nil
}

func CleanupArtifactExecutionAuthorizationDeadline(
	grant CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
) (time.Time, error) {
	if err := ValidateCleanupArtifactExecutionAuthorization(grant, request, grant.checkedAt); err != nil {
		return time.Time{}, err
	}
	return grant.mutationExpiresAt, nil
}

func CleanupArtifactExecutionOutcomePersistenceDeadline(
	grant CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
) (time.Time, error) {
	if err := validateCleanupArtifactExecutionAuthorizationBinding(grant, request); err != nil {
		return time.Time{}, err
	}
	return grant.outcomeExpiresAt, nil
}

func cleanupArtifactExecutionAuthorizationWindow(
	checkedAt time.Time,
	leaseExpiresAt time.Time,
) (time.Time, time.Time, error) {
	if checkedAt.IsZero() || leaseExpiresAt.IsZero() {
		return time.Time{}, time.Time{}, ingest.ErrCleanupExecutionUnauthorized
	}
	checkedAt = checkedAt.UTC()
	leaseExpiresAt = leaseExpiresAt.UTC()
	mutationExpiresAt := checkedAt.Add(CleanupArtifactExecutionMutationTTL)
	outcomeExpiresAt := mutationExpiresAt.Add(CleanupArtifactExecutionOutcomePersistenceGrace)
	if leaseExpiresAt.Before(outcomeExpiresAt) {
		outcomeExpiresAt = leaseExpiresAt
		mutationExpiresAt = outcomeExpiresAt.Add(-CleanupArtifactExecutionOutcomePersistenceGrace)
	}
	if !checkedAt.Before(mutationExpiresAt) ||
		!mutationExpiresAt.Before(outcomeExpiresAt) ||
		outcomeExpiresAt.After(leaseExpiresAt) {
		return time.Time{}, time.Time{}, ErrCleanupArtifactExecutionAuthorizationExpired
	}
	return mutationExpiresAt, outcomeExpiresAt, nil
}

func cleanupArtifactExecutionCapabilitySeal(
	grant CleanupArtifactExecutionAuthorizationGrant,
) [sha256.Size]byte {
	encoder := newCleanupCapabilityEncoder(cleanupArtifactExecutionCapabilityBindingVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.mutationExpiresAt)
	encoder.addTime(grant.outcomeExpiresAt)
	encoder.addString(grant.requestHash)
	encoder.addString(grant.targetHash)
	encoder.addString(grant.planHash)
	encoder.addInt64(grant.receiptRevision)
	encoder.addString(grant.ownerID)
	encoder.addInt64(grant.fencingToken)
	encoder.addTime(grant.leaseExpiresAt)
	encoder.addInt64(grant.dispatchRevision)
	encoder.addString(string(grant.dispatchPhase))
	encoder.addString(string(grant.artifact))
	if grant.targeted {
		encoder.addInt64(1)
	} else {
		encoder.addInt64(0)
	}
	return encoder.sum()
}
