package gcsadapter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"time"

	"cloud.google.com/go/storage"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/cleanupattest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

type cleanupAbsenceInventoryReader interface {
	ListExactPathGenerations(context.Context, string, int) (ingest.GenerationInventory, error)
}

// CleanupAbsenceAuditor owns no delete backend. Its only provider surface is a
// bounded exact-path inventory read.
type CleanupAbsenceAuditor struct {
	reader                cleanupAbsenceInventoryReader
	now                   func() time.Time
	validateAuthorization func(
		firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
		ingest.CleanupAbsenceAuditRequest,
		time.Time,
	) error
	authorizationDeadline func(
		firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
		ingest.CleanupAbsenceAuditRequest,
	) (time.Time, error)
	authorizationBinding func(
		firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
		ingest.CleanupAbsenceAuditRequest,
	) (string, error)
	evidencePrivateKey [ed25519.PrivateKeySize]byte
}

func NewCleanupAbsenceAuditor(
	bucket *storage.BucketHandle,
) (*CleanupAbsenceAuditor, cleanupattest.Verifier, error) {
	if bucket == nil {
		return nil, cleanupattest.Verifier{}, errors.New("Cloud Storage bucket is required")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, cleanupattest.Verifier{}, errors.New("cleanup absence evidence key generation failed")
	}
	verifier, err := cleanupattest.NewVerifier(publicKey)
	if err != nil {
		return nil, cleanupattest.Verifier{}, errors.New("cleanup absence evidence verifier creation failed")
	}
	auditor := &CleanupAbsenceAuditor{
		reader: &HTTPArtifactInventoryReader{
			backend: storageArtifactInventoryReadBackend{bucket: bucket},
		},
		now:                   time.Now,
		validateAuthorization: firebaseadapter.ValidateCleanupAbsenceAuditAuthorization,
		authorizationDeadline: firebaseadapter.CleanupAbsenceAuditAuthorizationDeadline,
		authorizationBinding:  firebaseadapter.CleanupAbsenceAuditEvidenceBinding,
	}
	copy(auditor.evidencePrivateKey[:], privateKey)
	return auditor, verifier, nil
}

func (a *CleanupAbsenceAuditor) AuditCleanupAbsence(
	ctx context.Context,
	grant firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
) (cleanupattest.Evidence, error) {
	if a == nil || a.reader == nil || a.now == nil || a.validateAuthorization == nil ||
		a.authorizationDeadline == nil || a.authorizationBinding == nil ||
		!a.hasEvidencePrivateKey() || ctx == nil {
		return cleanupattest.Evidence{}, ingest.ErrCleanupExecutionUnavailable
	}
	if err := a.validateAuthorization(grant, request, a.trustedNow()); err != nil {
		return cleanupattest.Evidence{}, err
	}
	deadline, err := a.authorizationDeadline(grant, request)
	if err != nil {
		return cleanupattest.Evidence{}, err
	}
	boundary := newCleanupAbsenceAuditBoundary(ctx, deadline)
	defer boundary.cancel()
	if err := a.checkBoundary(boundary, grant, request); err != nil {
		return cleanupattest.Evidence{}, err
	}
	inventory, providerErr := a.reader.ListExactPathGenerations(
		boundary.ctx, request.ExpectedPath, cleanupExecutionInventoryLimit,
	)
	if err := a.completeProviderCall(boundary, grant, request, providerErr); err != nil {
		return cleanupattest.Evidence{}, err
	}
	if providerErr != nil {
		return cleanupattest.Evidence{},
			normalizeCleanupExecutionProviderError(boundary.ctx, providerErr)
	}
	if err := cleanupInventoryCompletenessError(inventory, request.ExpectedPath); err != nil {
		return cleanupattest.Evidence{}, err
	}
	if len(inventory.NonSoftDeleted.Candidates) != 0 || len(inventory.SoftDeleted.Candidates) != 0 {
		return cleanupattest.Evidence{}, ingest.ErrCleanupExecutionGenerationDrift
	}
	observedAt := a.trustedNow()
	if err := a.checkBoundary(boundary, grant, request); err != nil {
		return cleanupattest.Evidence{}, err
	}
	binding, err := a.authorizationBinding(grant, request)
	if err != nil {
		return cleanupattest.Evidence{}, ingest.ErrCleanupExecutionUnauthorized
	}
	evidence, err := cleanupattest.NewCleanupAbsenceEvidence(
		request, binding, observedAt,
		func(payload []byte) ([]byte, error) {
			return ed25519.Sign(ed25519.PrivateKey(a.evidencePrivateKey[:]), payload), nil
		},
	)
	if err != nil {
		return cleanupattest.Evidence{}, ingest.ErrInvalidCleanupExecutionObservation
	}
	return evidence, nil
}

func (a *CleanupAbsenceAuditor) hasEvidencePrivateKey() bool {
	if a == nil {
		return false
	}
	for _, value := range a.evidencePrivateKey {
		if value != 0 {
			return true
		}
	}
	return false
}

func (a *CleanupAbsenceAuditor) checkBoundary(
	boundary cleanupAbsenceAuditBoundary,
	grant firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
) error {
	if err := boundary.contextError(); err != nil {
		return err
	}
	return a.validateAuthorization(grant, request, a.trustedNow())
}

func (a *CleanupAbsenceAuditor) completeProviderCall(
	boundary cleanupAbsenceAuditBoundary,
	grant firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
	providerErr error,
) error {
	if err := a.checkBoundary(boundary, grant, request); err != nil {
		return err
	}
	if boundaryErr := boundary.contextError(); boundaryErr != nil {
		return boundaryErr
	}
	if providerErr != nil && boundary.parent != nil && boundary.parent.Err() != nil {
		return boundary.parent.Err()
	}
	return nil
}

func (a *CleanupAbsenceAuditor) trustedNow() time.Time {
	if a != nil && a.now != nil {
		return a.now().UTC()
	}
	return time.Now().UTC()
}

type cleanupAbsenceAuditBoundary struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	parent               context.Context
	authorizationLimited bool
}

func newCleanupAbsenceAuditBoundary(
	parent context.Context,
	authorizationDeadline time.Time,
) cleanupAbsenceAuditBoundary {
	authorizationLimited := true
	if parentDeadline, exists := parent.Deadline(); exists &&
		!authorizationDeadline.Before(parentDeadline) {
		authorizationLimited = false
	}
	ctx, cancel := context.WithDeadline(parent, authorizationDeadline)
	return cleanupAbsenceAuditBoundary{
		ctx: ctx, cancel: cancel, parent: parent,
		authorizationLimited: authorizationLimited,
	}
}

func (b cleanupAbsenceAuditBoundary) contextError() error {
	if b.parent != nil {
		if parentErr := b.parent.Err(); parentErr != nil {
			return parentErr
		}
	}
	if b.ctx != nil && b.ctx.Err() != nil {
		if b.authorizationLimited && errors.Is(b.ctx.Err(), context.DeadlineExceeded) {
			return firebaseadapter.ErrCleanupAbsenceAuditAuthorizationExpired
		}
		return b.ctx.Err()
	}
	return nil
}
