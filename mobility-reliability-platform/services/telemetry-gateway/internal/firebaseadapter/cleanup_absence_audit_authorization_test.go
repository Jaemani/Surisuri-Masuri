package firebaseadapter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/cleanupattest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupAbsenceAuditAuthorizationBindsFreshRawLedgerState(t *testing.T) {
	fixture := cleanupAbsenceAuditAuthorizationFixture(t)
	request, grant, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditRaw,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	if request.ExpectedPath != fixture.plan.ExpectedRawPath ||
		request.ExpectedLedgerRevision != fixture.ledger.Revision ||
		request.NextPhase != ingest.CleanupExecutionPhaseRawAbsenceConfirmed ||
		request.RequestHash == "" {
		t.Fatalf("request = %#v", request)
	}
	if err := ValidateCleanupAbsenceAuditAuthorization(grant, request, grant.checkedAt); err != nil {
		t.Fatalf("ValidateCleanupAbsenceAuditAuthorization() = %v", err)
	}
	deadline, err := CleanupAbsenceAuditAuthorizationDeadline(grant, request)
	if err != nil || !deadline.Equal(grant.expiresAt) ||
		deadline.Sub(grant.checkedAt) > CleanupAbsenceAuditGrantTTL {
		t.Fatalf("deadline = %s, %v", deadline, err)
	}
	if err := ValidateCleanupAbsenceAuditAuthorization(grant, request, grant.expiresAt); !errors.Is(err, ErrCleanupAbsenceAuditAuthorizationExpired) {
		t.Fatalf("exact expiry error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestCleanupAbsenceAuditAuthorizationBindsFreshManifestLedgerState(t *testing.T) {
	fixture := cleanupManifestAbsenceAuditAuthorizationFixture(t)
	request, grant, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditManifest,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	if request.ExpectedPath != fixture.plan.ExpectedManifestPath ||
		request.ExpectedLedgerRevision != fixture.ledger.Revision ||
		request.NextPhase != ingest.CleanupExecutionPhaseManifestAbsenceConfirmed ||
		request.Artifact != ingest.CleanupAbsenceAuditManifest {
		t.Fatalf("manifest request = %#v", request)
	}
	if err := ValidateCleanupAbsenceAuditAuthorization(grant, request, grant.checkedAt); err != nil {
		t.Fatalf("ValidateCleanupAbsenceAuditAuthorization() = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestCleanupAbsenceAuditAuthorizationRejectsWrongPhaseAndTamper(t *testing.T) {
	fixture := cleanupAbsenceAuditAuthorizationFixture(t)
	if _, _, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditManifest,
	); !errors.Is(err, ingest.ErrCleanupExecutionConflict) {
		t.Fatalf("early manifest authorization error = %v", err)
	}
	request, grant, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditRaw,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*CleanupAbsenceAuditAuthorizationGrant, *ingest.CleanupAbsenceAuditRequest)
	}{
		{name: "request path", mutate: func(_ *CleanupAbsenceAuditAuthorizationGrant, value *ingest.CleanupAbsenceAuditRequest) {
			value.ExpectedPath += ".forged"
		}},
		{name: "request hash", mutate: func(_ *CleanupAbsenceAuditAuthorizationGrant, value *ingest.CleanupAbsenceAuditRequest) {
			value.RequestHash = differentCleanupLedgerDigest(value.RequestHash)
		}},
		{name: "grant seal", mutate: func(value *CleanupAbsenceAuditAuthorizationGrant, _ *ingest.CleanupAbsenceAuditRequest) {
			value.capabilitySeal[0] ^= 0xff
		}},
		{name: "ledger revision", mutate: func(value *CleanupAbsenceAuditAuthorizationGrant, _ *ingest.CleanupAbsenceAuditRequest) {
			value.ledgerRevision++
		}},
		{name: "artifact", mutate: func(value *CleanupAbsenceAuditAuthorizationGrant, _ *ingest.CleanupAbsenceAuditRequest) {
			value.artifact = ingest.CleanupAbsenceAuditManifest
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forgedGrant := grant
			forgedRequest := request
			test.mutate(&forgedGrant, &forgedRequest)
			if err := ValidateCleanupAbsenceAuditAuthorization(
				forgedGrant, forgedRequest, grant.checkedAt,
			); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
				t.Fatalf("tamper error = %v", err)
			}
		})
	}
	fixture.assertNoWrites(t)
}

func TestCleanupAbsenceAuditAuthorizationCapsGrantAtLeaseExpiry(t *testing.T) {
	fixture := cleanupAbsenceAuditAuthorizationFixture(t)
	expiresAt := fixture.plan.Target.Command.LeaseExpiresAt
	checkedAt := expiresAt.Add(-5 * time.Second)
	fixture.store.now = func() time.Time { return checkedAt }
	fixture.transaction.readTime = checkedAt
	targetPath := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
	targetRead := fixture.transaction.targets[targetPath]
	targetRead.ReadTime = checkedAt
	fixture.transaction.targets[targetPath] = targetRead
	request, grant, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditRaw,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	deadline, err := CleanupAbsenceAuditAuthorizationDeadline(grant, request)
	if err != nil || !deadline.Equal(expiresAt) || !grant.expiresAt.Equal(expiresAt) {
		t.Fatalf("lease-capped deadline = %s, grant=%s, err=%v", deadline, grant.expiresAt, err)
	}
	fixture.assertNoWrites(t)
}

type cleanupAbsenceAuditAuthorizationTestFixture struct {
	*cleanupExecutionLedgerStoreFixture
	evidenceSign func([]byte) ([]byte, error)
}

func cleanupAbsenceAuditAuthorizationFixture(t *testing.T) *cleanupAbsenceAuditAuthorizationTestFixture {
	t.Helper()
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	evidenceSign := configureCleanupAbsenceAuditEvidence(t, fixture.store)
	fixture.store.cleanupAbsenceAuditContext = func(
		parent context.Context,
		_ time.Time,
	) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}
	createdAt := fixture.plan.Target.Command.CreatedAt
	ledger := fixture.ledger
	var err error
	ledger, err = ingest.AdvanceCleanupExecutionLedger(fixture.plan, ledger, ingest.CleanupExecutionTransition{
		Phase: ingest.CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: createdAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("raw dispatch = %v", err)
	}
	ledger, err = ingest.AdvanceCleanupExecutionLedger(fixture.plan, ledger, ingest.CleanupExecutionTransition{
		Phase:         ingest.CleanupExecutionPhaseRawOutcomeRecorded,
		DeleteOutcome: ingest.CleanupDeleteObserved, ObservedAt: createdAt.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("raw outcome = %v", err)
	}
	fixture.ledger = ledger
	fixture.seedLedger(ledger)
	return &cleanupAbsenceAuditAuthorizationTestFixture{
		cleanupExecutionLedgerStoreFixture: fixture,
		evidenceSign:                       evidenceSign,
	}
}

func cleanupManifestAbsenceAuditAuthorizationFixture(t *testing.T) *cleanupAbsenceAuditAuthorizationTestFixture {
	t.Helper()
	fixture := cleanupAbsenceAuditAuthorizationFixture(t)
	createdAt := fixture.plan.Target.Command.CreatedAt
	ledger := fixture.ledger
	var err error
	for _, transition := range []ingest.CleanupExecutionTransition{
		{
			Phase:        ingest.CleanupExecutionPhaseRawAbsenceConfirmed,
			AuditOutcome: ingest.CleanupAuditConfirmedAbsent,
			ObservedAt:   createdAt.Add(3 * time.Second),
		},
		{
			Phase:      ingest.CleanupExecutionPhaseManifestDispatchRecorded,
			ObservedAt: createdAt.Add(4 * time.Second),
		},
		{
			Phase:         ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
			DeleteOutcome: ingest.CleanupDeleteObserved,
			ObservedAt:    createdAt.Add(5 * time.Second),
		},
	} {
		ledger, err = ingest.AdvanceCleanupExecutionLedger(fixture.plan, ledger, transition)
		if err != nil {
			t.Fatalf("advance manifest audit fixture %q = %v", transition.Phase, err)
		}
	}
	fixture.ledger = ledger
	fixture.seedLedger(ledger)
	return fixture
}

func configureCleanupAbsenceAuditEvidence(
	t *testing.T,
	store *FirestoreAdmissionStore,
) func([]byte) ([]byte, error) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() = %v", err)
	}
	verifier, err := cleanupattest.NewVerifier(publicKey)
	if err != nil {
		t.Fatalf("cleanupattest.NewVerifier() = %v", err)
	}
	store.cleanupAbsenceAuditEvidenceVerifier = verifier
	return func(payload []byte) ([]byte, error) {
		return ed25519.Sign(privateKey, payload), nil
	}
}

func issueCleanupAbsenceAuditEvidence(
	t *testing.T,
	sign func([]byte) ([]byte, error),
	grant CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
	observedAt time.Time,
) cleanupattest.Evidence {
	t.Helper()
	binding, err := CleanupAbsenceAuditEvidenceBinding(grant, request)
	if err != nil {
		t.Fatalf("CleanupAbsenceAuditEvidenceBinding() = %v", err)
	}
	evidence, err := cleanupattest.NewCleanupAbsenceEvidence(
		request, binding, observedAt, sign,
	)
	if err != nil {
		t.Fatalf("NewCleanupAbsenceEvidence() = %v", err)
	}
	return evidence
}
