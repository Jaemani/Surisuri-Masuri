package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/cleanupattest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreCleanupAbsenceAuditPersistsAndReplaysExactEvidence(t *testing.T) {
	fixture := cleanupAbsenceAuditAuthorizationFixture(t)
	request, grant, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditRaw,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	observedAt := grant.checkedAt.Add(time.Second)
	fixture.store.now = func() time.Time { return observedAt }
	evidence := issueCleanupAbsenceAuditEvidence(
		t, fixture.evidenceSign, grant, request, observedAt,
	)
	ledger, mutationStatus, err := fixture.store.RecordCleanupAbsenceAudit(
		context.Background(), grant, request, evidence,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhaseRawAbsenceConfirmed ||
		ledger.Raw.AuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		!ledger.Raw.AuditedAt.Equal(observedAt) {
		t.Fatalf("RecordCleanupAbsenceAudit() = %#v, %q, %v", ledger, mutationStatus, err)
	}
	if len(fixture.transaction.updates) != 1 ||
		fixture.transaction.updates[0].path != fixture.attemptPath {
		t.Fatalf("audit updates = %#v", fixture.transaction.updates)
	}
	values := cleanupExecutionUpdateValues(fixture.transaction.updates[0].updates)
	if values["cleanup_phase"] != string(ingest.CleanupExecutionPhaseRawAbsenceConfirmed) ||
		values["cleanup_raw_audit_outcome"] != string(ingest.CleanupAuditConfirmedAbsent) ||
		!values["cleanup_raw_audited_at"].(time.Time).Equal(observedAt) {
		t.Fatalf("audit update values = %#v", values)
	}

	fixture.seedLedger(ledger)
	fixture.transaction.updates = nil
	replayed, mutationStatus, err := fixture.store.RecordCleanupAbsenceAudit(
		context.Background(), grant, request, evidence,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationReplayed ||
		!reflect.DeepEqual(replayed, ledger) {
		t.Fatalf("audit replay = %#v, %q, %v", replayed, mutationStatus, err)
	}
	fixture.assertNoWrites(t)

	differentObservedAt := observedAt.Add(time.Second)
	fixture.store.now = func() time.Time { return differentObservedAt }
	differentEvidence := issueCleanupAbsenceAuditEvidence(
		t, fixture.evidenceSign, grant, request, differentObservedAt,
	)
	if _, _, err := fixture.store.RecordCleanupAbsenceAudit(
		context.Background(), grant, request, differentEvidence,
	); !errors.Is(err, ingest.ErrCleanupExecutionConflict) {
		t.Fatalf("different evidence replay error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupAbsenceAuditPersistsManifestEvidence(t *testing.T) {
	fixture := cleanupManifestAbsenceAuditAuthorizationFixture(t)
	request, grant, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditManifest,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	observedAt := grant.checkedAt.Add(time.Second)
	fixture.store.now = func() time.Time { return observedAt }
	evidence := issueCleanupAbsenceAuditEvidence(
		t, fixture.evidenceSign, grant, request, observedAt,
	)
	ledger, mutationStatus, err := fixture.store.RecordCleanupAbsenceAudit(
		context.Background(), grant, request, evidence,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhaseManifestAbsenceConfirmed ||
		ledger.Manifest.AuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		!ledger.Manifest.AuditedAt.Equal(observedAt) {
		t.Fatalf("RecordCleanupAbsenceAudit() = %#v, %q, %v", ledger, mutationStatus, err)
	}
	if len(fixture.transaction.updates) != 1 {
		t.Fatalf("manifest audit updates = %#v", fixture.transaction.updates)
	}
	values := cleanupExecutionUpdateValues(fixture.transaction.updates[0].updates)
	if values["cleanup_phase"] != string(ingest.CleanupExecutionPhaseManifestAbsenceConfirmed) ||
		values["cleanup_manifest_audit_outcome"] != string(ingest.CleanupAuditConfirmedAbsent) ||
		!values["cleanup_manifest_audited_at"].(time.Time).Equal(observedAt) {
		t.Fatalf("manifest audit update values = %#v", values)
	}
}

func TestFirestoreCleanupAbsenceAuditRejectsForgeryExpiryAndDriftWithoutWrite(t *testing.T) {
	for _, test := range []struct {
		name              string
		mutate            func(*cleanupAbsenceAuditAuthorizationTestFixture, *CleanupAbsenceAuditAuthorizationGrant, *ingest.CleanupAbsenceAuditRequest, *time.Time)
		want              error
		zeroEvidence      bool
		wrongEvidenceKey  bool
		wrongGrantBinding bool
	}{
		{name: "zero evidence", zeroEvidence: true, want: ingest.ErrCleanupExecutionUnauthorized},
		{name: "wrong evidence key", wrongEvidenceKey: true, want: ingest.ErrCleanupExecutionUnauthorized},
		{name: "wrong grant binding", wrongGrantBinding: true, want: ingest.ErrCleanupExecutionUnauthorized},
		{name: "expired grant", mutate: func(fixture *cleanupAbsenceAuditAuthorizationTestFixture, grant *CleanupAbsenceAuditAuthorizationGrant, _ *ingest.CleanupAbsenceAuditRequest, _ *time.Time) {
			fixture.store.now = func() time.Time { return grant.expiresAt }
		}, want: ErrCleanupAbsenceAuditAuthorizationExpired},
		{name: "observation before grant", mutate: func(_ *cleanupAbsenceAuditAuthorizationTestFixture, grant *CleanupAbsenceAuditAuthorizationGrant, _ *ingest.CleanupAbsenceAuditRequest, observedAt *time.Time) {
			*observedAt = grant.checkedAt.Add(-maxAdmissionClockSkew - time.Nanosecond)
		}, want: ingest.ErrCleanupExecutionUnavailable},
		{name: "observation at grant expiry", mutate: func(_ *cleanupAbsenceAuditAuthorizationTestFixture, grant *CleanupAbsenceAuditAuthorizationGrant, _ *ingest.CleanupAbsenceAuditRequest, observedAt *time.Time) {
			*observedAt = grant.expiresAt
		}, want: ErrCleanupAbsenceAuditAuthorizationExpired},
		{name: "ledger revision drift", mutate: func(fixture *cleanupAbsenceAuditAuthorizationTestFixture, _ *CleanupAbsenceAuditAuthorizationGrant, _ *ingest.CleanupAbsenceAuditRequest, _ *time.Time) {
			ledger := fixture.ledger
			ledger.Revision++
			fixture.seedLedger(ledger)
		}, want: ingest.ErrInvalidCleanupExecutionLedger},
		{name: "receipt revision drift", mutate: func(fixture *cleanupAbsenceAuditAuthorizationTestFixture, _ *CleanupAbsenceAuditAuthorizationGrant, _ *ingest.CleanupAbsenceAuditRequest, _ *time.Time) {
			receipt := fixture.transaction.receipts[admissionReceiptPath()]
			receipt.Revision++
			fixture.transaction.receipts[admissionReceiptPath()] = receipt
		}, want: ingest.ErrInvalidCleanupExecutionLedger},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := cleanupAbsenceAuditAuthorizationFixture(t)
			request, grant, err := fixture.store.AuthorizeCleanupAbsenceAudit(
				context.Background(), fixture.query, ingest.CleanupAbsenceAuditRaw,
			)
			if err != nil {
				t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
			}
			observedAt := grant.checkedAt.Add(time.Second)
			fixture.store.now = func() time.Time { return observedAt }
			if test.mutate != nil {
				test.mutate(fixture, &grant, &request, &observedAt)
			}
			var evidence cleanupattest.Evidence
			switch {
			case test.zeroEvidence:
			case test.wrongEvidenceKey:
				wrongSign := configureCleanupAbsenceAuditEvidence(t, &FirestoreAdmissionStore{})
				evidence = issueCleanupAbsenceAuditEvidence(
					t, wrongSign, grant, request, observedAt,
				)
			case test.wrongGrantBinding:
				evidence, err = cleanupattest.NewCleanupAbsenceEvidence(
					request, differentCleanupLedgerDigest(grant.requestHash), observedAt,
					fixture.evidenceSign,
				)
				if err != nil {
					t.Fatalf("NewCleanupAbsenceEvidence(wrong binding) = %v", err)
				}
			default:
				evidence = issueCleanupAbsenceAuditEvidence(
					t, fixture.evidenceSign, grant, request, observedAt,
				)
			}
			if _, _, err := fixture.store.RecordCleanupAbsenceAudit(
				context.Background(), grant, request, evidence,
			); !errors.Is(err, test.want) {
				t.Fatalf("RecordCleanupAbsenceAudit() error = %v, want %v", err, test.want)
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestGenericCleanupProgressStillRejectsAbsenceCommand(t *testing.T) {
	fixture := cleanupAbsenceAuditAuthorizationFixture(t)
	request, _, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditRaw,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	observation := ingest.CleanupAbsenceAuditObservation{
		RequestHash: request.RequestHash,
		Artifact:    request.Artifact,
		Outcome:     ingest.CleanupAuditConfirmedAbsent,
		ObservedAt:  fixture.store.now().UTC(),
	}
	command, err := ingest.BuildCleanupAbsenceAuditProgressCommand(request, observation)
	if err != nil {
		t.Fatalf("BuildCleanupAbsenceAuditProgressCommand() = %v", err)
	}
	if _, _, err := fixture.store.RecordCleanupExecutionProgress(
		context.Background(), command,
	); !errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("generic absence progress error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupAbsenceAuditBoundsTransactionByAuthorizationDeadline(t *testing.T) {
	fixture := cleanupAbsenceAuditAuthorizationFixture(t)
	request, grant, err := fixture.store.AuthorizeCleanupAbsenceAudit(
		context.Background(), fixture.query, ingest.CleanupAbsenceAuditRaw,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	fixture.store.now = func() time.Time { return grant.checkedAt }
	evidence := issueCleanupAbsenceAuditEvidence(
		t, fixture.evidenceSign, grant, request, grant.checkedAt,
	)
	var capturedDeadline time.Time
	fixture.store.cleanupAbsenceAuditContext = func(
		parent context.Context,
		deadline time.Time,
	) (context.Context, context.CancelFunc) {
		capturedDeadline = deadline
		return context.WithTimeout(parent, time.Millisecond)
	}
	fixture.store.runTransaction = func(
		ctx context.Context,
		_ func(context.Context, admissionTransaction) error,
	) error {
		<-ctx.Done()
		return ctx.Err()
	}
	if _, _, err := fixture.store.RecordCleanupAbsenceAudit(
		context.Background(), grant, request, evidence,
	); !errors.Is(err, ErrCleanupAbsenceAuditAuthorizationExpired) {
		t.Fatalf("authorization-bounded transaction error = %v", err)
	}
	wantDeadline, err := CleanupAbsenceAuditAuthorizationDeadline(grant, request)
	if err != nil || !capturedDeadline.Equal(wantDeadline) {
		t.Fatalf("captured deadline = %v, want %v, err=%v", capturedDeadline, wantDeadline, err)
	}
	fixture.assertNoWrites(t)
}
