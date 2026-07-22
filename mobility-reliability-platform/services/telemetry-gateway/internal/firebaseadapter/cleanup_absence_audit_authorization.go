package firebaseadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const (
	CleanupAbsenceAuditPolicyVersion = "cleanup-absence-audit.firestore-current-ledger@1"
	CleanupAbsenceAuditGrantTTL      = 30 * time.Second

	cleanupAbsenceAuditCapabilityBindingVersion = "cleanup-absence-audit-firestore-capability@1"
)

var ErrCleanupAbsenceAuditAuthorizationExpired = errors.New("cleanup absence audit authorization has expired")

// CleanupAbsenceAuditAuthorizationGrant is intentionally a different concrete
// type from the destructive cleanup grant. It authorizes exact-path inventory
// reads only and cannot be passed to the delete backend.
type CleanupAbsenceAuditAuthorizationGrant struct {
	policyVersion   string
	checkedAt       time.Time
	expiresAt       time.Time
	requestHash     string
	receiptRevision int64
	ownerID         string
	fencingToken    int64
	leaseExpiresAt  time.Time
	ledgerRevision  int64
	artifact        ingest.CleanupAbsenceAuditArtifact
	nextPhase       ingest.CleanupExecutionPhase
	capabilitySeal  [sha256.Size]byte
}

func (s *FirestoreAdmissionStore) AuthorizeCleanupAbsenceAudit(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
	artifact ingest.CleanupAbsenceAuditArtifact,
) (ingest.CleanupAbsenceAuditRequest, CleanupAbsenceAuditAuthorizationGrant, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionQuery(query) != nil ||
		(artifact != ingest.CleanupAbsenceAuditRaw && artifact != ingest.CleanupAbsenceAuditManifest) {
		return ingest.CleanupAbsenceAuditRequest{}, CleanupAbsenceAuditAuthorizationGrant{}, ingest.ErrCleanupExecutionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ingest.CleanupAbsenceAuditRequest{}, CleanupAbsenceAuditAuthorizationGrant{}, err
	}
	var request ingest.CleanupAbsenceAuditRequest
	var grant CleanupAbsenceAuditAuthorizationGrant
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		state, loadErr := loadCurrentCleanupExecutionLedgerState(
			runContext, transaction, query, s.now().UTC(),
		)
		if loadErr != nil {
			return loadErr
		}
		ledger, present, decodeErr := decodeCleanupExecutionLedger(
			state.plan, state.attempt, state.effectiveAt,
		)
		if decodeErr != nil {
			return decodeErr
		}
		if !present {
			return ingest.ErrCleanupExecutionConflict
		}
		request, loadErr = ingest.BuildCleanupAbsenceAuditRequest(
			state.plan, ledger, artifact, state.effectiveAt,
		)
		if loadErr != nil {
			return loadErr
		}
		expiresAt := state.effectiveAt.Add(CleanupAbsenceAuditGrantTTL)
		if request.ExpectedFence.ExpiresAt.Before(expiresAt) {
			expiresAt = request.ExpectedFence.ExpiresAt
		}
		if !state.effectiveAt.Before(expiresAt) {
			return ingest.ErrCleanupExecutionUnauthorized
		}
		grant = CleanupAbsenceAuditAuthorizationGrant{
			policyVersion: CleanupAbsenceAuditPolicyVersion,
			checkedAt:     state.effectiveAt, expiresAt: expiresAt,
			requestHash: request.RequestHash, receiptRevision: request.ExpectedReceiptRevision,
			ownerID: request.ExpectedFence.OwnerID, fencingToken: request.ExpectedFence.Token,
			leaseExpiresAt: request.ExpectedFence.ExpiresAt,
			ledgerRevision: request.ExpectedLedgerRevision,
			artifact:       request.Artifact, nextPhase: request.NextPhase,
		}
		grant.capabilitySeal = cleanupAbsenceAuditCapabilitySeal(grant)
		return nil
	})
	if err != nil {
		return ingest.CleanupAbsenceAuditRequest{}, CleanupAbsenceAuditAuthorizationGrant{},
			normalizeCleanupExecutionLedgerStoreError(ctx, err)
	}
	if ingest.ValidateCleanupAbsenceAuditRequest(request) != nil ||
		ValidateCleanupAbsenceAuditAuthorization(grant, request, grant.checkedAt) != nil {
		return ingest.CleanupAbsenceAuditRequest{}, CleanupAbsenceAuditAuthorizationGrant{}, ingest.ErrCleanupExecutionUnavailable
	}
	return request, grant, nil
}

func ValidateCleanupAbsenceAuditAuthorization(
	grant CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
	observedAt time.Time,
) error {
	if ingest.ValidateCleanupAbsenceAuditRequest(request) != nil || observedAt.IsZero() ||
		grant.policyVersion != CleanupAbsenceAuditPolicyVersion ||
		grant.checkedAt.IsZero() || grant.expiresAt.IsZero() ||
		!grant.checkedAt.Before(grant.expiresAt) || grant.requestHash != request.RequestHash ||
		grant.receiptRevision != request.ExpectedReceiptRevision ||
		grant.ownerID != request.ExpectedFence.OwnerID ||
		grant.fencingToken != request.ExpectedFence.Token ||
		!grant.leaseExpiresAt.Equal(request.ExpectedFence.ExpiresAt) ||
		grant.ledgerRevision != request.ExpectedLedgerRevision ||
		grant.artifact != request.Artifact || grant.nextPhase != request.NextPhase ||
		grant.capabilitySeal != cleanupAbsenceAuditCapabilitySeal(grant) {
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
		return ErrCleanupAbsenceAuditAuthorizationExpired
	}
	return nil
}

func CleanupAbsenceAuditAuthorizationDeadline(
	grant CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
) (time.Time, error) {
	if err := ValidateCleanupAbsenceAuditAuthorization(grant, request, grant.checkedAt); err != nil {
		return time.Time{}, err
	}
	if grant.leaseExpiresAt.Before(grant.expiresAt) {
		return grant.leaseExpiresAt, nil
	}
	return grant.expiresAt, nil
}

// CleanupAbsenceAuditEvidenceBinding identifies one concrete Firestore grant.
// The signed provider evidence must carry this binding, so evidence from a
// previous authorization cannot be replayed under a later grant.
func CleanupAbsenceAuditEvidenceBinding(
	grant CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
) (string, error) {
	if err := ValidateCleanupAbsenceAuditAuthorization(grant, request, grant.checkedAt); err != nil {
		return "", err
	}
	return hex.EncodeToString(grant.capabilitySeal[:]), nil
}

func cleanupAbsenceAuditCapabilitySeal(
	grant CleanupAbsenceAuditAuthorizationGrant,
) [sha256.Size]byte {
	encoder := newCleanupCapabilityEncoder(cleanupAbsenceAuditCapabilityBindingVersion)
	encoder.addString(grant.policyVersion)
	encoder.addTime(grant.checkedAt)
	encoder.addTime(grant.expiresAt)
	encoder.addString(grant.requestHash)
	encoder.addInt64(grant.receiptRevision)
	encoder.addString(grant.ownerID)
	encoder.addInt64(grant.fencingToken)
	encoder.addTime(grant.leaseExpiresAt)
	encoder.addInt64(grant.ledgerRevision)
	encoder.addString(string(grant.artifact))
	encoder.addString(string(grant.nextPhase))
	return encoder.sum()
}
