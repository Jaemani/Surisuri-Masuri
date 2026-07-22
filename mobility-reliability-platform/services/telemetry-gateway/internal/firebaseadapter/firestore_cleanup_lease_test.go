package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreClaimCleanupLeaseAtQuietBoundary(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
	quietAt := receipt.CleanupQuiescenceUntil
	tx.readTime = quietAt
	store := admissionTestStore(quietAt, admissionRunner(tx))
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := store.ClaimCleanupLease(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		quietAt,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired || ingest.ValidateCleanupLeaseGrant(grant) != nil {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v", grant, status, err)
	}
	if grant.Lease.Fence.Token != receipt.FencingToken+1 ||
		grant.ReceiptRevision != receipt.Revision+1 ||
		!grant.TransitionedAt.Equal(receipt.CleanupTransitionedAt) ||
		!grant.QuiescenceUntil.Equal(receipt.CleanupQuiescenceUntil) {
		t.Fatalf("cleanup grant = %#v", grant)
	}
	if len(tx.updates) != 1 || tx.updates[0].path != admissionReceiptPath() || len(tx.creates) != 1 {
		t.Fatalf("cleanup claim updates/creates = %#v/%#v", tx.updates, tx.creates)
	}
	updates := firestoreUpdateMap(tx.updates[0].updates)
	if updates["lease_owner_kind"] != string(ingest.LeaseOwnerCleanup) ||
		updates["fencing_token"] != receipt.FencingToken+1 ||
		updates["revision"] != receipt.Revision+1 ||
		updates["recovery_attempt_count"] != receipt.RecoveryAttemptCount+1 {
		t.Fatalf("cleanup receipt updates = %#v", updates)
	}
	for _, immutable := range []string{
		"cleanup_transitioned_at", "cleanup_quiescence_until", "cleanup_mode",
		"cleanup_origin_status", "cleanup_policy_version",
	} {
		if _, exists := updates[immutable]; exists {
			t.Fatalf("cleanup claim rewrote immutable %s", immutable)
		}
	}
	attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, proposal.ID)
	attempt, ok := tx.createValue(attemptPath).(firestoreRecoveryAttempt)
	if !ok || attempt.Status != ingest.RecoveryAttemptStarted ||
		attempt.OwnerKind != ingest.LeaseOwnerCleanup ||
		attempt.WorkerVersion != ingest.CleanupWorkerVersion ||
		attempt.FencingToken != receipt.FencingToken+1 || !attempt.StartedAt.Equal(quietAt) {
		t.Fatalf("cleanup attempt = %#v", tx.createValue(attemptPath))
	}
}

func TestFirestoreAdmissionStoreClaimCleanupLeaseRejectsInvalidProposalBeforeTransaction(t *testing.T) {
	validOwnerID := admissionTakeoverOwnerID
	tests := []struct {
		name     string
		owner    ingest.LeaseOwner
		proposal ingest.CleanupAttemptProposal
		duration time.Duration
	}{
		{
			name:  "sweeper owner",
			owner: ingest.LeaseOwner{ID: validOwnerID, Kind: ingest.LeaseOwnerSweeper},
			proposal: ingest.CleanupAttemptProposal{
				ID: validOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			},
			duration: ingest.DefaultRequestLeaseDuration,
		},
		{
			name:  "owner attempt mismatch",
			owner: ingest.LeaseOwner{ID: validOwnerID, Kind: ingest.LeaseOwnerCleanup},
			proposal: ingest.CleanupAttemptProposal{
				ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			},
			duration: ingest.DefaultRequestLeaseDuration,
		},
		{
			name:  "forward worker version",
			owner: ingest.LeaseOwner{ID: validOwnerID, Kind: ingest.LeaseOwnerCleanup},
			proposal: ingest.CleanupAttemptProposal{
				ID: validOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion,
			},
			duration: ingest.DefaultRequestLeaseDuration,
		},
		{
			name:  "duration above maximum",
			owner: ingest.LeaseOwner{ID: validOwnerID, Kind: ingest.LeaseOwnerCleanup},
			proposal: ingest.CleanupAttemptProposal{
				ID: validOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			},
			duration: ingest.MaxLeaseDuration + time.Nanosecond,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transactionCalls := 0
			store := admissionTestStore(admissionTestNow(), func(
				context.Context,
				func(context.Context, admissionTransaction) error,
			) error {
				transactionCalls++
				return nil
			})
			grant, status, err := store.ClaimCleanupLease(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				test.owner,
				test.proposal,
				admissionTestNow(),
				test.duration,
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) ||
				grant != (ingest.CleanupLeaseGrant{}) || status != "" || transactionCalls != 0 {
				t.Fatalf(
					"ClaimCleanupLease() = %#v, %q, %v, transaction calls %d",
					grant,
					status,
					err,
					transactionCalls,
				)
			}
		})
	}
}

func TestFirestoreAdmissionStoreClaimCleanupLeaseUsesEarliestQuietClock(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tests := []struct {
		name        string
		requestedAt func(time.Time) time.Time
		readTime    func(time.Time) time.Time
	}{
		{
			name:        "application before quiet",
			requestedAt: func(quiet time.Time) time.Time { return quiet.Add(-time.Nanosecond) },
			readTime:    func(quiet time.Time) time.Time { return quiet },
		},
		{
			name:        "snapshot before quiet",
			requestedAt: func(quiet time.Time) time.Time { return quiet },
			readTime:    func(quiet time.Time) time.Time { return quiet.Add(-time.Nanosecond) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
			quietAt := receipt.CleanupQuiescenceUntil
			tx.readTime = test.readTime(quietAt)
			store := admissionTestStore(test.requestedAt(quietAt), admissionRunner(tx))
			proposal := ingest.CleanupAttemptProposal{
				ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}

			grant, status, err := store.ClaimCleanupLease(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
				proposal,
				test.requestedAt(quietAt),
				ingest.DefaultRequestLeaseDuration,
			)
			if err != nil || status != ingest.LeaseStatusNotDue || grant != (ingest.CleanupLeaseGrant{}) {
				t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want not due", grant, status, err)
			}
			if len(tx.updates) != 0 || len(tx.creates) != 0 {
				t.Fatalf("early cleanup claim updates/creates = %d/%d", len(tx.updates), len(tx.creates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreClaimCleanupLeaseReturnsHeldForActiveOwner(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
	oldProposal := ingest.CleanupAttemptProposal{
		ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	configureActiveCleanupLease(&receipt, oldProposal, receipt.CleanupQuiescenceUntil)
	tx.receipts[admissionReceiptPath()] = receipt
	requestedAt := receipt.LeaseExpiresAt.Add(-time.Nanosecond)
	tx.readTime = requestedAt
	store := admissionTestStore(requestedAt, admissionRunner(tx))
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := store.ClaimCleanupLease(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		requestedAt,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusHeld || grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want held", grant, status, err)
	}
	if len(tx.updates) != 0 || len(tx.creates) != 0 {
		t.Fatalf("held cleanup claim updates/creates = %d/%d", len(tx.updates), len(tx.creates))
	}
}

func TestFirestoreAdmissionStoreClaimCleanupLeaseClosesExpiredAttempt(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
	oldProposal := ingest.CleanupAttemptProposal{
		ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	configureActiveCleanupLease(&receipt, oldProposal, receipt.CleanupQuiescenceUntil)
	tx.receipts[admissionReceiptPath()] = receipt
	oldAttemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, oldProposal.ID)
	tx.attempts[oldAttemptPath] = newFirestoreCleanupAttempt(
		oldProposal,
		receipt.TenantID,
		receipt.ReceiptID,
		receipt.FencingToken,
		receipt.LeaseAcquiredAt,
	)
	requestedAt := receipt.LeaseExpiresAt
	tx.readTime = requestedAt
	store := admissionTestStore(requestedAt, admissionRunner(tx))
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := store.ClaimCleanupLease(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		requestedAt,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v", grant, status, err)
	}
	if grant.Lease.Fence.Token != receipt.FencingToken+1 ||
		grant.ReceiptRevision != receipt.Revision+1 || len(tx.updates) != 2 || len(tx.creates) != 1 {
		t.Fatalf("cleanup takeover result = %#v, updates/creates=%d/%d", grant, len(tx.updates), len(tx.creates))
	}
	if tx.updates[0].path != oldAttemptPath || tx.updates[1].path != admissionReceiptPath() {
		t.Fatalf("cleanup takeover update order = %#v", tx.updates)
	}
	closure := firestoreUpdateMap(tx.updates[0].updates)
	if closure["status"] != string(ingest.RecoveryAttemptFailed) ||
		closure["failure_code"] != string(ingest.RecoveryAttemptFailureLeaseExpired) ||
		closure["failed_at"] != requestedAt {
		t.Fatalf("prior cleanup attempt closure = %#v", closure)
	}
}

func TestFirestoreAdmissionStoreCleanupTakeoverUsesEarliestAttemptClock(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
	oldProposal := ingest.CleanupAttemptProposal{
		ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	configureActiveCleanupLease(&receipt, oldProposal, receipt.CleanupQuiescenceUntil)
	tx.receipts[admissionReceiptPath()] = receipt
	attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, oldProposal.ID)
	tx.attempts[attemptPath] = newFirestoreCleanupAttempt(
		oldProposal,
		receipt.TenantID,
		receipt.ReceiptID,
		receipt.FencingToken,
		receipt.LeaseAcquiredAt,
	)
	requestedAt := receipt.LeaseExpiresAt
	tx.readTime = requestedAt
	tx.attemptReadTime = requestedAt.Add(-time.Nanosecond)
	store := admissionTestStore(requestedAt, admissionRunner(tx))
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := store.ClaimCleanupLease(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		requestedAt,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusHeld || grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want held", grant, status, err)
	}
	if len(tx.updates) != 0 || len(tx.creates) != 0 {
		t.Fatalf("pre-expiry attempt clock updates/creates = %d/%d", len(tx.updates), len(tx.creates))
	}
}

func TestFirestoreAdmissionStoreCleanupTakeoverRejectsIncoherentThreeClockWidth(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
	oldProposal := ingest.CleanupAttemptProposal{
		ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	configureActiveCleanupLease(&receipt, oldProposal, receipt.CleanupQuiescenceUntil)
	tx.receipts[admissionReceiptPath()] = receipt
	attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, oldProposal.ID)
	tx.attempts[attemptPath] = newFirestoreCleanupAttempt(
		oldProposal,
		receipt.TenantID,
		receipt.ReceiptID,
		receipt.FencingToken,
		receipt.LeaseAcquiredAt,
	)
	requestedAt := receipt.LeaseExpiresAt.Add(maxAdmissionClockSkew + time.Nanosecond)
	tx.readTime = requestedAt
	tx.attemptReadTime = receipt.LeaseExpiresAt
	store := admissionTestStore(requestedAt, admissionRunner(tx))
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := store.ClaimCleanupLease(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		requestedAt,
		ingest.DefaultRequestLeaseDuration,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) || status != "" || grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want unavailable", grant, status, err)
	}
	if len(tx.updates) != 0 || len(tx.creates) != 0 {
		t.Fatalf("incoherent clocks updates/creates = %d/%d", len(tx.updates), len(tx.creates))
	}
}

func TestFirestoreAdmissionStoreCleanupTakeoverAcceptsOnlyLeaseExpiredPriorFailure(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tests := []struct {
		name      string
		code      ingest.RecoveryAttemptFailureCode
		failedAt  func(firestoreIngestReceipt) time.Time
		wantError bool
	}{
		{
			name: "lease expired", code: ingest.RecoveryAttemptFailureLeaseExpired,
			failedAt: func(receipt firestoreIngestReceipt) time.Time { return receipt.LeaseExpiresAt },
		},
		{
			name: "forward caller cancellation", code: ingest.RecoveryAttemptFailureCallerCanceled,
			failedAt: func(receipt firestoreIngestReceipt) time.Time {
				return receipt.LeaseExpiresAt.Add(-time.Nanosecond)
			},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
			oldProposal := ingest.CleanupAttemptProposal{
				ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}
			configureActiveCleanupLease(&receipt, oldProposal, receipt.CleanupQuiescenceUntil)
			tx.receipts[admissionReceiptPath()] = receipt
			attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, oldProposal.ID)
			attempt := newFirestoreCleanupAttempt(
				oldProposal,
				receipt.TenantID,
				receipt.ReceiptID,
				receipt.FencingToken,
				receipt.LeaseAcquiredAt,
			)
			attempt.Status = ingest.RecoveryAttemptFailed
			attempt.FailureCode = test.code
			attempt.FailedAt = test.failedAt(receipt)
			tx.attempts[attemptPath] = attempt
			requestedAt := receipt.LeaseExpiresAt
			tx.readTime = requestedAt
			store := admissionTestStore(requestedAt, admissionRunner(tx))
			proposal := ingest.CleanupAttemptProposal{
				ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}

			grant, status, err := store.ClaimCleanupLease(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
				proposal,
				requestedAt,
				ingest.DefaultRequestLeaseDuration,
			)
			if test.wantError {
				if !errors.Is(err, ingest.ErrAdmissionUnavailable) || status != "" || grant != (ingest.CleanupLeaseGrant{}) {
					t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want unavailable", grant, status, err)
				}
				if len(tx.updates) != 0 || len(tx.creates) != 0 {
					t.Fatalf("foreign failure updates/creates = %d/%d", len(tx.updates), len(tx.creates))
				}
				return
			}
			if err != nil || status != ingest.LeaseStatusAcquired || ingest.ValidateCleanupLeaseGrant(grant) != nil {
				t.Fatalf("ClaimCleanupLease() = %#v, %q, %v", grant, status, err)
			}
			if len(tx.updates) != 1 || tx.updates[0].path != admissionReceiptPath() || len(tx.creates) != 1 {
				t.Fatalf("closed prior failure was rewritten: updates=%#v creates=%#v", tx.updates, tx.creates)
			}
		})
	}
}

func TestFirestoreAdmissionStoreClaimCleanupLeaseRejectsMalformedPriorAttempt(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tests := []struct {
		name      string
		omit      bool
		configure func(*firestoreRecoveryAttempt)
	}{
		{name: "missing", omit: true},
		{name: "foreign token", configure: func(attempt *firestoreRecoveryAttempt) { attempt.FencingToken++ }},
		{name: "foreign tenant", configure: func(attempt *firestoreRecoveryAttempt) {
			attempt.TenantID = admissionReceiptID
		}},
		{name: "foreign receipt", configure: func(attempt *firestoreRecoveryAttempt) {
			attempt.ReceiptID = admissionTakeoverOwnerID
		}},
		{name: "forward owner", configure: func(attempt *firestoreRecoveryAttempt) {
			attempt.OwnerKind = ingest.LeaseOwnerSweeper
		}},
		{name: "wrong worker", configure: func(attempt *firestoreRecoveryAttempt) {
			attempt.WorkerVersion = ingest.RecoveryWorkerVersion
		}},
		{name: "wrong started at", configure: func(attempt *firestoreRecoveryAttempt) {
			attempt.StartedAt = attempt.StartedAt.Add(time.Nanosecond)
		}},
		{name: "terminal residue", configure: func(attempt *firestoreRecoveryAttempt) {
			attempt.ActionHash = "not-allowed"
		}},
		{name: "completed", configure: func(attempt *firestoreRecoveryAttempt) {
			attempt.Status = ingest.RecoveryAttemptCompleted
			attempt.CompletedAt = attempt.StartedAt.Add(time.Second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
			oldProposal := ingest.CleanupAttemptProposal{
				ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}
			configureActiveCleanupLease(&receipt, oldProposal, receipt.CleanupQuiescenceUntil)
			tx.receipts[admissionReceiptPath()] = receipt
			attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, oldProposal.ID)
			attempt := newFirestoreCleanupAttempt(
				oldProposal,
				receipt.TenantID,
				receipt.ReceiptID,
				receipt.FencingToken,
				receipt.LeaseAcquiredAt,
			)
			if test.configure != nil {
				test.configure(&attempt)
			}
			if !test.omit {
				tx.attempts[attemptPath] = attempt
			}
			requestedAt := receipt.LeaseExpiresAt
			tx.readTime = requestedAt
			store := admissionTestStore(requestedAt, admissionRunner(tx))
			proposal := ingest.CleanupAttemptProposal{
				ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}

			grant, status, err := store.ClaimCleanupLease(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
				proposal,
				requestedAt,
				ingest.DefaultRequestLeaseDuration,
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) ||
				status != "" || grant != (ingest.CleanupLeaseGrant{}) {
				t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want unavailable", grant, status, err)
			}
			if len(tx.updates) != 0 || len(tx.creates) != 0 {
				t.Fatalf("malformed prior attempt updates/creates = %d/%d", len(tx.updates), len(tx.creates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreCleanupLeaseRejectsForwardMutations(t *testing.T) {
	transitionedAt := admissionTestNow().Add(ingest.ReservationProcessingWindow)
	tests := []struct {
		name   string
		invoke func(*FirestoreAdmissionStore, firestoreIngestReceipt) error
	}{
		{
			name: "renew",
			invoke: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt) error {
				_, err := store.RenewLease(
					context.Background(), admissionTenantID, admissionReservationKey,
					receipt.leaseGrant().Fence, receipt.LeaseHeartbeatAt, ingest.DefaultRequestLeaseDuration,
				)
				return err
			},
		},
		{
			name: "release",
			invoke: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt) error {
				return store.ReleaseLease(
					context.Background(), admissionTenantID, admissionReservationKey,
					receipt.leaseGrant().Fence, receipt.LeaseHeartbeatAt,
					ingest.LeaseReleaseArtifactUnavailable,
				)
			},
		},
		{
			name: "mark stored",
			invoke: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt) error {
				_, err := store.MarkStored(
					context.Background(), admissionTenantID, admissionReservationKey,
					receipt.leaseGrant().Fence, admissionStoredReceiptData(admissionTestReservation(receipt.CreatedAt)),
					receipt.LeaseHeartbeatAt,
				)
				return err
			},
		},
		{
			name: "mark rejected",
			invoke: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt) error {
				_, err := store.MarkRejected(
					context.Background(), admissionTenantID, admissionReservationKey,
					receipt.leaseGrant().Fence, "object_conflict", receipt.LeaseHeartbeatAt,
				)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
			proposal := ingest.CleanupAttemptProposal{
				ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}
			configureActiveCleanupLease(&receipt, proposal, receipt.CleanupQuiescenceUntil)
			tx.receipts[admissionReceiptPath()] = receipt
			tx.readTime = receipt.LeaseHeartbeatAt
			store := admissionTestStore(receipt.LeaseHeartbeatAt, admissionRunner(tx))

			if err := test.invoke(store, receipt); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("forward mutation error = %v, want unavailable", err)
			}
			if len(tx.updates) != 0 || len(tx.creates) != 0 {
				t.Fatalf("forward mutation updates/creates = %d/%d", len(tx.updates), len(tx.creates))
			}
		})
	}
}

func admissionCleanupPendingTransaction(
	t *testing.T,
	transitionedAt time.Time,
) (*fakeAdmissionTransaction, firestoreIngestReceipt) {
	t.Helper()
	createdAt := transitionedAt.Add(-ingest.ReservationProcessingWindow)
	tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
	receipt := tx.receipts[admissionReceiptPath()]
	receipt.State = ingest.ReceiptCleanupPending
	receipt.clearLease()
	receipt.CleanupTransitionedAt = transitionedAt
	receipt.CleanupQuiescenceUntil = transitionedAt.Add(ingest.DefaultCleanupLateWriteGrace)
	receipt.CleanupMode = ingest.CleanupModeReservationExpiry
	receipt.CleanupOriginStatus = ingest.ReceiptReserved
	receipt.CleanupPolicyVersion = ingest.CleanupTransitionPolicyV1
	receipt.FencingToken = 2
	receipt.Revision = 2
	receipt.UpdatedAt = transitionedAt
	tx.receipts[admissionReceiptPath()] = receipt
	return tx, receipt
}

func configureActiveCleanupLease(
	receipt *firestoreIngestReceipt,
	proposal ingest.CleanupAttemptProposal,
	acquiredAt time.Time,
) {
	receipt.LeaseOwnerID = proposal.ID
	receipt.LeaseOwnerKind = ingest.LeaseOwnerCleanup
	receipt.LeaseAcquiredAt = acquiredAt
	receipt.LeaseHeartbeatAt = acquiredAt
	receipt.LeaseExpiresAt = acquiredAt.Add(ingest.DefaultRequestLeaseDuration)
	receipt.NextRecoveryAt = time.Time{}
	receipt.LastRecoveryCode = ""
	receipt.FencingToken = 3
	receipt.RecoveryAttemptCount = 1
	receipt.Revision = 3
	receipt.UpdatedAt = acquiredAt
}
