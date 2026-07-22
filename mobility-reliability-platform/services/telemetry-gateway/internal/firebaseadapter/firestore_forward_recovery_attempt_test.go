package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreFailsOnlyCurrentRecoveryAttempt(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
	failure := forwardRecoveryAttemptFailure(fixture, ingest.RecoveryAttemptFailureInvalidContract)
	validatorCalls := 0

	err := store.failForwardRecoveryAttempt(
		context.Background(),
		ingest.ForwardRecoveryAttemptGrant{},
		failure,
		fixture.observedAt,
		func(
			_ ingest.ForwardRecoveryAttemptGrant,
			got ingest.ForwardRecoveryAttemptFailure,
			receipt ingest.Receipt,
			attempt ingest.CurrentForwardRecoveryAttempt,
			checkedAt time.Time,
		) error {
			validatorCalls++
			if got != failure || receipt.Revision != failure.ReceiptRevision ||
				attempt.AttemptID != failure.Attempt.ID || attempt.Status != ingest.RecoveryAttemptStarted ||
				!checkedAt.Equal(fixture.observedAt) {
				t.Fatalf("validator input = %#v %#v %#v %v", got, receipt, attempt, checkedAt)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("failForwardRecoveryAttempt() = %v", err)
	}
	if validatorCalls != 1 || len(fixture.base.creates) != 0 || len(fixture.base.updates) != 1 ||
		fixture.base.updates[0].path != fixture.attemptPath {
		t.Fatalf("validator/creates/updates = %d/%d/%#v", validatorCalls, len(fixture.base.creates), fixture.base.updates)
	}
	updates := firestoreUpdateMap(fixture.base.updates[0].updates)
	if updates["status"] != string(ingest.RecoveryAttemptFailed) ||
		updates["failure_code"] != string(ingest.RecoveryAttemptFailureInvalidContract) ||
		!updates["failed_at"].(time.Time).Equal(fixture.observedAt) {
		t.Fatalf("failure updates = %#v", updates)
	}
}

func TestFirestoreAdmissionStoreRejectsInvalidAttemptFailureGrantBeforeTransaction(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	transactionCalls := 0
	store := admissionTestStore(fixture.observedAt, func(
		_ context.Context,
		_ func(context.Context, admissionTransaction) error,
	) error {
		transactionCalls++
		return nil
	})
	err := store.FailForwardRecoveryAttempt(
		context.Background(),
		ingest.ForwardRecoveryAttemptGrant{},
		forwardRecoveryAttemptFailure(fixture, ingest.RecoveryAttemptFailureCallerCanceled),
		fixture.observedAt,
	)
	if !errors.Is(err, ingest.ErrInvalidForwardRecoveryAttemptAuthorization) {
		t.Fatalf("FailForwardRecoveryAttempt() = %v", err)
	}
	if transactionCalls != 0 {
		t.Fatalf("transaction calls = %d, want 0", transactionCalls)
	}
}

func TestFirestoreAdmissionStoreAttemptFailureRequiresStartedExactAttempt(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*forwardRecoveryActionFixture)
	}{
		{name: "missing", mutate: func(f *forwardRecoveryActionFixture) { delete(f.transaction.attempts, f.attemptPath) }},
		{name: "completed", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.Status = ingest.RecoveryAttemptCompleted
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "already failed", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.Status = ingest.RecoveryAttemptFailed
			attempt.FailureCode = ingest.RecoveryAttemptFailureInvalidContract
			attempt.FailedAt = f.observedAt
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "wrong owner kind", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.OwnerKind = ingest.LeaseOwnerRequest
			f.transaction.attempts[f.attemptPath] = attempt
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
			test.mutate(fixture)
			store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
			err := store.failForwardRecoveryAttempt(
				context.Background(),
				ingest.ForwardRecoveryAttemptGrant{},
				forwardRecoveryAttemptFailure(fixture, ingest.RecoveryAttemptFailureFinalizerAbort),
				fixture.observedAt,
				func(
					ingest.ForwardRecoveryAttemptGrant,
					ingest.ForwardRecoveryAttemptFailure,
					ingest.Receipt,
					ingest.CurrentForwardRecoveryAttempt,
					time.Time,
				) error {
					return nil
				},
			)
			if !errors.Is(err, ingest.ErrInvalidForwardRecoveryAttemptAuthorization) {
				t.Fatalf("failForwardRecoveryAttempt() = %v", err)
			}
			if len(fixture.base.updates) != 0 {
				t.Fatalf("updates = %d, want 0", len(fixture.base.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreClaimClosesExpiredPriorStartedAttempt(t *testing.T) {
	now := admissionTestNow()
	createdAt := now.Add(-5 * time.Minute)
	base, receipt := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
	receipt.FencingToken = 2
	receipt.LeaseOwnerID = admissionLeaseOwnerID
	receipt.LeaseOwnerKind = ingest.LeaseOwnerSweeper
	receipt.LeaseAcquiredAt = createdAt.Add(time.Minute)
	receipt.LeaseHeartbeatAt = receipt.LeaseAcquiredAt
	receipt.LeaseExpiresAt = createdAt.Add(3 * time.Minute)
	receipt.NextRecoveryAt = receipt.LeaseExpiresAt
	receipt.RecoveryAttemptCount = 1
	receipt.Revision = 2
	receipt.UpdatedAt = receipt.LeaseHeartbeatAt
	base.readTime = now
	base.receiptReadTime = now
	base.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
	prior := newFirestoreRecoveryAttempt(
		ingest.RecoveryAttemptProposal{ID: admissionLeaseOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion},
		receipt.TenantID,
		receipt.ReceiptID,
		ingest.LeaseOwnerSweeper,
		receipt.FencingToken,
		receipt.LeaseAcquiredAt,
	)
	priorPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, prior.AttemptID)
	base.attempts[priorPath] = prior
	store := admissionTestStore(now, admissionRunner(base))
	newAttempt := ingest.RecoveryAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion,
	}

	grant, status, err := store.ClaimRecoveryLease(
		context.Background(),
		receipt.TenantID,
		receipt.ReservationKey,
		ingest.LeaseOwner{ID: newAttempt.ID, Kind: ingest.LeaseOwnerSweeper},
		newAttempt,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired || grant.Fence.Token != 3 {
		t.Fatalf("ClaimRecoveryLease() = %#v, %q, %v", grant, status, err)
	}
	if len(base.updates) != 2 || base.updates[0].path != priorPath ||
		base.updates[1].path != admissionReceiptPath() || len(base.creates) != 1 {
		t.Fatalf("updates/creates = %#v / %#v", base.updates, base.creates)
	}
	closure := firestoreUpdateMap(base.updates[0].updates)
	if closure["status"] != string(ingest.RecoveryAttemptFailed) ||
		closure["failure_code"] != string(ingest.RecoveryAttemptFailureLeaseExpired) ||
		!closure["failed_at"].(time.Time).Equal(now) {
		t.Fatalf("prior closure = %#v", closure)
	}
}

func TestFirestoreAdmissionStoreClaimUsesPriorAttemptReadTime(t *testing.T) {
	now := admissionTestNow()
	createdAt := now.Add(-5 * time.Minute)
	base, receipt := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
	receipt.FencingToken = 2
	receipt.LeaseOwnerID = admissionLeaseOwnerID
	receipt.LeaseOwnerKind = ingest.LeaseOwnerSweeper
	receipt.LeaseAcquiredAt = createdAt.Add(time.Minute)
	receipt.LeaseHeartbeatAt = receipt.LeaseAcquiredAt
	receipt.LeaseExpiresAt = createdAt.Add(3 * time.Minute)
	receipt.NextRecoveryAt = receipt.LeaseExpiresAt
	receipt.RecoveryAttemptCount = 1
	receipt.Revision = 2
	receipt.UpdatedAt = receipt.LeaseHeartbeatAt
	base.readTime = now
	base.receiptReadTime = now
	base.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
	prior := newFirestoreRecoveryAttempt(
		ingest.RecoveryAttemptProposal{ID: admissionLeaseOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion},
		receipt.TenantID,
		receipt.ReceiptID,
		ingest.LeaseOwnerSweeper,
		receipt.FencingToken,
		receipt.LeaseAcquiredAt,
	)
	priorPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, prior.AttemptID)
	base.attempts[priorPath] = prior
	base.attemptReadTime = now.Add(2 * time.Second)
	store := admissionTestStore(now, admissionRunner(base))
	newAttempt := ingest.RecoveryAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion,
	}

	grant, status, err := store.ClaimRecoveryLease(
		context.Background(),
		receipt.TenantID,
		receipt.ReservationKey,
		ingest.LeaseOwner{ID: newAttempt.ID, Kind: ingest.LeaseOwnerSweeper},
		newAttempt,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired {
		t.Fatalf("ClaimRecoveryLease() = %#v, %q, %v", grant, status, err)
	}
	if !grant.AcquiredAt.Equal(base.attemptReadTime) ||
		!grant.Fence.ExpiresAt.Equal(base.attemptReadTime.Add(ingest.DefaultRequestLeaseDuration)) {
		t.Fatalf("grant times = %#v, want acquired %v", grant, base.attemptReadTime)
	}
	closure := firestoreUpdateMap(base.updates[0].updates)
	if !closure["failed_at"].(time.Time).Equal(base.attemptReadTime) {
		t.Fatalf("failed_at = %v, want %v", closure["failed_at"], base.attemptReadTime)
	}
}

func TestFirestoreAdmissionStoreClaimRejectsIncoherentPriorAttemptReadTime(t *testing.T) {
	now := admissionTestNow()
	createdAt := now.Add(-5 * time.Minute)
	base, receipt := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
	receipt.FencingToken = 2
	receipt.LeaseOwnerID = admissionLeaseOwnerID
	receipt.LeaseOwnerKind = ingest.LeaseOwnerSweeper
	receipt.LeaseAcquiredAt = createdAt.Add(time.Minute)
	receipt.LeaseHeartbeatAt = receipt.LeaseAcquiredAt
	receipt.LeaseExpiresAt = createdAt.Add(3 * time.Minute)
	receipt.NextRecoveryAt = receipt.LeaseExpiresAt
	receipt.RecoveryAttemptCount = 1
	receipt.Revision = 2
	base.readTime = now
	base.receiptReadTime = now
	base.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
	prior := newFirestoreRecoveryAttempt(
		ingest.RecoveryAttemptProposal{ID: admissionLeaseOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion},
		receipt.TenantID,
		receipt.ReceiptID,
		ingest.LeaseOwnerSweeper,
		receipt.FencingToken,
		receipt.LeaseAcquiredAt,
	)
	priorPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, prior.AttemptID)
	base.attempts[priorPath] = prior
	base.attemptReadTime = now.Add(maxAdmissionClockSkew + time.Nanosecond)
	store := admissionTestStore(now, admissionRunner(base))
	newAttempt := ingest.RecoveryAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion,
	}

	_, _, err := store.ClaimRecoveryLease(
		context.Background(),
		receipt.TenantID,
		receipt.ReservationKey,
		ingest.LeaseOwner{ID: newAttempt.ID, Kind: ingest.LeaseOwnerSweeper},
		newAttempt,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("ClaimRecoveryLease() = %v", err)
	}
	if len(base.updates) != 0 || len(base.creates) != 0 {
		t.Fatalf("updates/creates = %d/%d, want 0/0", len(base.updates), len(base.creates))
	}
}

func TestValidateFailedRecoveryAttemptForOwnerEnforcesFenceTime(t *testing.T) {
	now := admissionTestNow()
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	receipt := fixture.base.receipts[admissionReceiptPath()]
	expected := fixture.command.Attempt
	fence := fixture.command.Fence
	baseAttempt := fixture.transaction.attempts[fixture.attemptPath]
	baseAttempt.Status = ingest.RecoveryAttemptFailed
	tests := []struct {
		name       string
		code       ingest.RecoveryAttemptFailureCode
		failedAt   time.Time
		observedAt time.Time
		wantErr    bool
	}{
		{name: "lease expiry at fence", code: ingest.RecoveryAttemptFailureLeaseExpired, failedAt: fence.ExpiresAt, observedAt: fence.ExpiresAt, wantErr: false},
		{name: "lease expiry before fence", code: ingest.RecoveryAttemptFailureLeaseExpired, failedAt: fence.ExpiresAt.Add(-time.Nanosecond), observedAt: fence.ExpiresAt, wantErr: true},
		{name: "live failure before fence", code: ingest.RecoveryAttemptFailureCallerCanceled, failedAt: now.Add(time.Second), observedAt: fence.ExpiresAt, wantErr: false},
		{name: "live failure at fence", code: ingest.RecoveryAttemptFailureCallerCanceled, failedAt: fence.ExpiresAt, observedAt: fence.ExpiresAt, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempt := baseAttempt
			attempt.FailureCode = test.code
			attempt.FailedAt = test.failedAt
			err := validateFailedRecoveryAttemptForOwner(
				attempt, receipt, expected, fence, ingest.LeaseOwnerSweeper, test.observedAt,
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateFailedRecoveryAttemptForOwner() = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestExpiredPriorRecoveryAttemptClosureRejectsSweeperLeaseWithoutAttemptCount(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	receipt := fixture.base.receipts[admissionReceiptPath()]
	receipt.RecoveryAttemptCount = 0
	delete(fixture.transaction.attempts, fixture.attemptPath)

	path, updates, effectiveAt, err := expiredPriorRecoveryAttemptClosure(
		context.Background(),
		fixture.transaction,
		receipt,
		receipt.LeaseExpiresAt,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) || path != "" || updates != nil ||
		!effectiveAt.IsZero() {
		t.Fatalf("expiredPriorRecoveryAttemptClosure() = %q, %#v, %v, %v", path, updates, effectiveAt, err)
	}
	if len(fixture.base.creates) != 0 || len(fixture.base.updates) != 0 {
		t.Fatalf("creates/updates = %d/%d, want 0/0", len(fixture.base.creates), len(fixture.base.updates))
	}
}

func forwardRecoveryAttemptFailure(
	fixture *forwardRecoveryActionFixture,
	code ingest.RecoveryAttemptFailureCode,
) ingest.ForwardRecoveryAttemptFailure {
	return ingest.ForwardRecoveryAttemptFailure{
		TenantID: fixture.command.TenantID, ReservationKey: fixture.command.ReservationKey,
		Attempt: fixture.command.Attempt, ReceiptRevision: fixture.command.ReceiptRevision,
		Fence: fixture.command.Fence, FailureCode: code,
	}
}
