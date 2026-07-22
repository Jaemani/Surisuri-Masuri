package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupTakeoverClosesEveryNonTerminalProgressPhaseWithoutInheritance(t *testing.T) {
	phases := []ingest.CleanupExecutionPhase{
		ingest.CleanupExecutionPhasePlanned,
		ingest.CleanupExecutionPhaseRawDispatchRecorded,
		ingest.CleanupExecutionPhaseRawOutcomeRecorded,
		ingest.CleanupExecutionPhaseRawAbsenceConfirmed,
		ingest.CleanupExecutionPhaseManifestDispatchRecorded,
		ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
		ingest.CleanupExecutionPhaseManifestAbsenceConfirmed,
	}
	for _, phase := range phases {
		t.Run(string(phase), func(t *testing.T) {
			fixture := newCleanupExecutionLedgerStoreFixture(t)
			ledger := cleanupTakeoverLedgerAtPhase(t, fixture, phase)
			fixture.seedLedger(ledger)
			expiresAt := fixture.plan.Target.Command.LeaseExpiresAt.UTC()
			fixture.transaction.readTime = expiresAt
			target := fixture.transaction.targets[cleanupTargetDocumentPath(
				fixture.query.TenantID, fixture.query.AttemptID,
			)]
			target.ReadTime = expiresAt
			fixture.transaction.targets[cleanupTargetDocumentPath(
				fixture.query.TenantID, fixture.query.AttemptID,
			)] = target
			proposal := ingest.CleanupAttemptProposal{
				ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}

			grant, status, err := fixture.store.ClaimCleanupLease(
				context.Background(),
				fixture.query.TenantID,
				fixture.query.ReservationKey,
				ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
				proposal,
				expiresAt,
				ingest.DefaultRequestLeaseDuration,
			)
			if err != nil || status != ingest.LeaseStatusAcquired ||
				ingest.ValidateCleanupLeaseGrant(grant) != nil {
				t.Fatalf("ClaimCleanupLease(%q) = %#v, %q, %v", phase, grant, status, err)
			}
			if len(fixture.transaction.updates) != 2 || len(fixture.transaction.creates) != 1 {
				t.Fatalf(
					"progress takeover writes = updates:%#v creates:%#v",
					fixture.transaction.updates,
					fixture.transaction.creates,
				)
			}
			closure := fixture.transaction.updates[0]
			if closure.path != fixture.attemptPath {
				t.Fatalf("closure path = %q", closure.path)
			}
			values := firestoreUpdateMap(closure.updates)
			if len(values) != 3 || values["status"] != string(ingest.RecoveryAttemptFailed) ||
				values["failure_code"] != string(ingest.RecoveryAttemptFailureLeaseExpired) ||
				values["failed_at"] != expiresAt {
				t.Fatalf("progress closure = %#v", values)
			}
			created, ok := fixture.transaction.creates[0].value.(firestoreRecoveryAttempt)
			if !ok || created.AttemptID != proposal.ID ||
				created.Status != ingest.RecoveryAttemptStarted ||
				hasCleanupExecutionLedgerResidue(created) {
				t.Fatalf("new cleanup attempt inherited prior progress = %#v", created)
			}
			for _, update := range fixture.transaction.updates {
				if update.path == cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID) {
					t.Fatalf("immutable target was updated: %#v", update)
				}
			}
		})
	}
}

func TestCleanupTakeoverRejectsMissingOrTamperedProgressEvidenceWithoutWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*cleanupExecutionLedgerStoreFixture)
	}{
		{
			name: "missing target",
			mutate: func(fixture *cleanupExecutionLedgerStoreFixture) {
				delete(fixture.transaction.targets, cleanupTargetDocumentPath(
					fixture.query.TenantID, fixture.query.AttemptID,
				))
			},
		},
		{
			name: "target hash drift",
			mutate: func(fixture *cleanupExecutionLedgerStoreFixture) {
				path := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
				value := fixture.transaction.targets[path]
				value.Target.TargetHash = differentCleanupLedgerDigest(value.Target.TargetHash)
				fixture.transaction.targets[path] = value
			},
		},
		{
			name: "plan hash drift",
			mutate: func(fixture *cleanupExecutionLedgerStoreFixture) {
				attempt := fixture.transaction.attempts[fixture.attemptPath]
				attempt.CleanupPlanHash = differentCleanupLedgerDigest(attempt.CleanupPlanHash)
				fixture.transaction.attempts[fixture.attemptPath] = attempt
			},
		},
		{
			name: "ledger revision drift",
			mutate: func(fixture *cleanupExecutionLedgerStoreFixture) {
				attempt := fixture.transaction.attempts[fixture.attemptPath]
				attempt.CleanupExecutionRevision++
				fixture.transaction.attempts[fixture.attemptPath] = attempt
			},
		},
		{
			name: "terminal disposition residue",
			mutate: func(fixture *cleanupExecutionLedgerStoreFixture) {
				attempt := fixture.transaction.attempts[fixture.attemptPath]
				attempt.CleanupDisposition = ingest.CleanupExecutionDispositionComplete
				fixture.transaction.attempts[fixture.attemptPath] = attempt
			},
		},
		{
			name: "failed progress attempt",
			mutate: func(fixture *cleanupExecutionLedgerStoreFixture) {
				attempt := fixture.transaction.attempts[fixture.attemptPath]
				attempt.Status = ingest.RecoveryAttemptFailed
				attempt.FailureCode = ingest.RecoveryAttemptFailureLeaseExpired
				attempt.FailedAt = fixture.plan.Target.Command.LeaseExpiresAt
				fixture.transaction.attempts[fixture.attemptPath] = attempt
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCleanupExecutionLedgerStoreFixture(t)
			ledger := cleanupTakeoverLedgerAtPhase(
				t, fixture, ingest.CleanupExecutionPhaseRawOutcomeRecorded,
			)
			fixture.seedLedger(ledger)
			expiresAt := fixture.plan.Target.Command.LeaseExpiresAt.UTC()
			fixture.transaction.readTime = expiresAt
			path := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
			target := fixture.transaction.targets[path]
			target.ReadTime = expiresAt
			fixture.transaction.targets[path] = target
			test.mutate(fixture)
			proposal := ingest.CleanupAttemptProposal{
				ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}

			grant, status, err := fixture.store.ClaimCleanupLease(
				context.Background(),
				fixture.query.TenantID,
				fixture.query.ReservationKey,
				ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
				proposal,
				expiresAt,
				ingest.DefaultRequestLeaseDuration,
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) || status != "" ||
				grant != (ingest.CleanupLeaseGrant{}) {
				t.Fatalf("ClaimCleanupLease() = %#v, %q, %v", grant, status, err)
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestCleanupTakeoverUsesTargetReadClockBeforeClosingProgress(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	fixture.seedLedger(cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseRawDispatchRecorded,
	))
	expiresAt := fixture.plan.Target.Command.LeaseExpiresAt.UTC()
	fixture.transaction.readTime = expiresAt
	path := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
	target := fixture.transaction.targets[path]
	target.ReadTime = expiresAt.Add(-time.Nanosecond)
	fixture.transaction.targets[path] = target
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := fixture.store.ClaimCleanupLease(
		context.Background(),
		fixture.query.TenantID,
		fixture.query.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		expiresAt,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusHeld || grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want held", grant, status, err)
	}
	fixture.assertNoWrites(t)
}

func TestCleanupTakeoverRejectsIncoherentTargetReadClockWithoutWrite(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	fixture.seedLedger(cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
	))
	expiresAt := fixture.plan.Target.Command.LeaseExpiresAt.UTC()
	fixture.transaction.readTime = expiresAt
	path := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
	target := fixture.transaction.targets[path]
	target.ReadTime = expiresAt.Add(-maxAdmissionClockSkew - time.Nanosecond)
	fixture.transaction.targets[path] = target
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := fixture.store.ClaimCleanupLease(
		context.Background(),
		fixture.query.TenantID,
		fixture.query.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		expiresAt,
		ingest.DefaultRequestLeaseDuration,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) || status != "" ||
		grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want unavailable", grant, status, err)
	}
	fixture.assertNoWrites(t)
}

func cleanupTakeoverLedgerAtPhase(
	t *testing.T,
	fixture *cleanupExecutionLedgerStoreFixture,
	phase ingest.CleanupExecutionPhase,
) ingest.CleanupExecutionLedger {
	t.Helper()
	ledger := fixture.ledger
	if phase == ingest.CleanupExecutionPhasePlanned {
		return ledger
	}
	base := fixture.plan.Target.Command.CreatedAt.UTC()
	steps := []ingest.CleanupExecutionTransition{
		{Phase: ingest.CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: base.Add(time.Second)},
		{
			Phase:         ingest.CleanupExecutionPhaseRawOutcomeRecorded,
			DeleteOutcome: ingest.CleanupDeleteObserved, ObservedAt: base.Add(2 * time.Second),
		},
		{
			Phase:        ingest.CleanupExecutionPhaseRawAbsenceConfirmed,
			AuditOutcome: ingest.CleanupAuditConfirmedAbsent, ObservedAt: base.Add(3 * time.Second),
		},
		{Phase: ingest.CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: base.Add(4 * time.Second)},
		{
			Phase:         ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
			DeleteOutcome: ingest.CleanupDeleteNotFound, ObservedAt: base.Add(5 * time.Second),
		},
		{
			Phase:        ingest.CleanupExecutionPhaseManifestAbsenceConfirmed,
			AuditOutcome: ingest.CleanupAuditConfirmedAbsent, ObservedAt: base.Add(6 * time.Second),
		},
	}
	for _, step := range steps {
		var err error
		ledger, err = ingest.AdvanceCleanupExecutionLedger(fixture.plan, ledger, step)
		if err != nil {
			t.Fatalf("AdvanceCleanupExecutionLedger(%q) = %v", step.Phase, err)
		}
		if step.Phase == phase {
			return ledger
		}
	}
	t.Fatalf("unsupported cleanup phase %q", phase)
	return ingest.CleanupExecutionLedger{}
}
