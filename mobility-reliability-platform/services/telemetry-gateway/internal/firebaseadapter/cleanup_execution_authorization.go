package firebaseadapter

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const (
	CleanupExecutionPolicyVersion = "cleanup-execution.firestore-current-target@1"
	CleanupExecutionGrantTTL      = 30 * time.Second
)

var ErrCleanupExecutionAuthorizationExpired = errors.New("cleanup execution authorization has expired")

// CleanupExecutionAuthorizationGrant can only be populated by this package's
// concrete Firestore-backed authorizer. Its fields intentionally remain
// unexported because it directly gates destructive Cloud Storage mutation.
type CleanupExecutionAuthorizationGrant struct {
	policyVersion   string
	checkedAt       time.Time
	expiresAt       time.Time
	receiptRevision int64
	ownerID         string
	fencingToken    int64
	leaseExpiresAt  time.Time
	planHash        string
	capabilitySeal  [sha256.Size]byte
}

func (s *FirestoreAdmissionStore) AuthorizeCleanupExecution(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
) (ingest.CleanupExecutionPlan, CleanupExecutionAuthorizationGrant, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionQuery(query) != nil {
		return ingest.CleanupExecutionPlan{}, CleanupExecutionAuthorizationGrant{}, ingest.ErrCleanupExecutionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ingest.CleanupExecutionPlan{}, CleanupExecutionAuthorizationGrant{}, err
	}
	snapshot, err := s.LoadCurrentCleanupExecution(ctx, query)
	if err != nil {
		return ingest.CleanupExecutionPlan{}, CleanupExecutionAuthorizationGrant{}, err
	}
	checkedAt, err := conservativeAcceptanceTime(s.now().UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return ingest.CleanupExecutionPlan{}, CleanupExecutionAuthorizationGrant{}, ingest.ErrCleanupExecutionUnavailable
	}
	plan, err := ingest.BuildCleanupExecutionPlan(query, snapshot, checkedAt)
	if err != nil {
		return ingest.CleanupExecutionPlan{}, CleanupExecutionAuthorizationGrant{}, err
	}
	planHash, err := ingest.CleanupExecutionPlanHash(plan)
	if err != nil {
		return ingest.CleanupExecutionPlan{}, CleanupExecutionAuthorizationGrant{}, ingest.ErrCleanupExecutionUnavailable
	}
	expiresAt := checkedAt.Add(CleanupExecutionGrantTTL)
	if plan.Target.Command.LeaseExpiresAt.Before(expiresAt) {
		expiresAt = plan.Target.Command.LeaseExpiresAt
	}
	if !checkedAt.Before(expiresAt) {
		return ingest.CleanupExecutionPlan{}, CleanupExecutionAuthorizationGrant{}, ingest.ErrCleanupExecutionUnauthorized
	}
	grant := CleanupExecutionAuthorizationGrant{
		policyVersion:   CleanupExecutionPolicyVersion,
		checkedAt:       checkedAt,
		expiresAt:       expiresAt,
		receiptRevision: plan.Target.Command.ReceiptRevision,
		ownerID:         plan.Target.Command.AttemptID,
		fencingToken:    plan.Target.Command.FencingToken,
		leaseExpiresAt:  plan.Target.Command.LeaseExpiresAt,
		planHash:        planHash,
	}
	grant.capabilitySeal = cleanupExecutionCapabilitySeal(grant)
	return plan, grant, nil
}

func ValidateCleanupExecutionAuthorization(
	grant CleanupExecutionAuthorizationGrant,
	plan ingest.CleanupExecutionPlan,
	observedAt time.Time,
) error {
	plan, err := ingest.CloneCleanupExecutionPlan(plan)
	if err != nil || observedAt.IsZero() || grant.policyVersion != CleanupExecutionPolicyVersion ||
		grant.checkedAt.IsZero() || grant.expiresAt.IsZero() || !grant.checkedAt.Before(grant.expiresAt) ||
		grant.receiptRevision != plan.Target.Command.ReceiptRevision ||
		grant.ownerID != plan.Target.Command.AttemptID ||
		grant.fencingToken != plan.Target.Command.FencingToken ||
		!grant.leaseExpiresAt.Equal(plan.Target.Command.LeaseExpiresAt) ||
		grant.capabilitySeal != cleanupExecutionCapabilitySeal(grant) {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	planHash, err := ingest.CleanupExecutionPlanHash(plan)
	if err != nil || grant.planHash != planHash {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	effectiveAt := observedAt.UTC()
	if effectiveAt.Before(grant.checkedAt) {
		if !withinAdmissionClockSkew(effectiveAt, grant.checkedAt) {
			return ingest.ErrCleanupExecutionUnavailable
		}
		effectiveAt = grant.checkedAt
	}
	if !effectiveAt.Before(grant.expiresAt) || !effectiveAt.Before(grant.leaseExpiresAt) {
		return ErrCleanupExecutionAuthorizationExpired
	}
	return nil
}

func CleanupExecutionAuthorizationDeadline(
	grant CleanupExecutionAuthorizationGrant,
	plan ingest.CleanupExecutionPlan,
) (time.Time, error) {
	if err := ValidateCleanupExecutionAuthorization(grant, plan, grant.checkedAt); err != nil {
		return time.Time{}, err
	}
	if grant.leaseExpiresAt.Before(grant.expiresAt) {
		return grant.leaseExpiresAt, nil
	}
	return grant.expiresAt, nil
}

func cleanupExecutionCapabilitySeal(grant CleanupExecutionAuthorizationGrant) [sha256.Size]byte {
	encoder := newCleanupCapabilityEncoder(cleanupExecutionCapabilityBindingVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addInt64(grant.receiptRevision)
	encoder.addString(grant.ownerID)
	encoder.addInt64(grant.fencingToken)
	encoder.addTime(grant.leaseExpiresAt)
	encoder.addString(grant.planHash)
	return encoder.sum()
}

const cleanupExecutionCapabilityBindingVersion = "cleanup-execution-firestore-capability@1"

type cleanupCapabilityEncoder struct {
	hash hash.Hash
}

func newCleanupCapabilityEncoder(version string) *cleanupCapabilityEncoder {
	encoder := &cleanupCapabilityEncoder{hash: sha256.New()}
	encoder.addString(version)
	return encoder
}

func (e *cleanupCapabilityEncoder) addString(value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = e.hash.Write(length[:])
	_, _ = e.hash.Write([]byte(value))
}

func (e *cleanupCapabilityEncoder) addInt64(value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = e.hash.Write(encoded[:])
}

func (e *cleanupCapabilityEncoder) addTime(value time.Time) {
	e.addString(value.UTC().Format(time.RFC3339Nano))
}

func (e *cleanupCapabilityEncoder) sum() [sha256.Size]byte {
	var result [sha256.Size]byte
	copy(result[:], e.hash.Sum(nil))
	return result
}
