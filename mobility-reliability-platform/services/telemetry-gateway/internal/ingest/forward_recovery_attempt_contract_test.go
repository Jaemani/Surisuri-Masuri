package ingest

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSystemRecoveryAuthorizerMintsExactAttemptFailureGrant(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)
	attempt := RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion}

	failure, grant, err := authorizer.AuthorizeForwardRecoveryAttemptFailure(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		attempt,
		RecoveryAttemptFailureInvalidContract,
	)
	if err != nil {
		t.Fatalf("AuthorizeForwardRecoveryAttemptFailure() = %v", err)
	}
	if failure.TenantID != snapshot.Receipt.TenantID ||
		failure.ReservationKey != snapshot.Receipt.ReservationKey || failure.Attempt != attempt ||
		failure.ReceiptRevision != snapshot.Receipt.Revision ||
		!sameLeaseFence(failure.Fence, lease.Fence) ||
		failure.FailureCode != RecoveryAttemptFailureInvalidContract {
		t.Fatalf("failure = %#v", failure)
	}
	if err := ValidateForwardRecoveryAttemptAuthorization(grant, failure, now.Add(time.Second)); err != nil {
		t.Fatalf("ValidateForwardRecoveryAttemptAuthorization() = %v", err)
	}
	deadline, err := ForwardRecoveryAttemptAuthorizationDeadline(grant, failure)
	if err != nil || !deadline.Equal(now.Add(ForwardRecoveryArtifactReadGrantTTL)) {
		t.Fatalf("ForwardRecoveryAttemptAuthorizationDeadline() = %v, %v", deadline, err)
	}
	if err := ValidateCurrentForwardRecoveryAttemptFailure(
		grant,
		failure,
		snapshot.Receipt,
		forwardActionCurrentAttempt(snapshot),
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("ValidateCurrentForwardRecoveryAttemptFailure() = %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("current-state reads = %d, want 1", store.calls)
	}
}

func TestForwardRecoveryAttemptGrantRejectsMutationExpiryAndWrongAttempt(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)
	failure, grant, err := authorizer.AuthorizeForwardRecoveryAttemptFailure(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion},
		RecoveryAttemptFailureCallerCanceled,
	)
	if err != nil {
		t.Fatalf("AuthorizeForwardRecoveryAttemptFailure() = %v", err)
	}
	mutated := failure
	mutated.FailureCode = RecoveryAttemptFailureCallerDeadline
	tests := []struct {
		name    string
		grant   ForwardRecoveryAttemptGrant
		failure ForwardRecoveryAttemptFailure
		at      time.Time
		want    error
	}{
		{name: "zero grant", failure: failure, at: now.Add(time.Second), want: ErrInvalidForwardRecoveryAttemptAuthorization},
		{name: "mutated failure", grant: grant, failure: mutated, at: now.Add(time.Second), want: ErrInvalidForwardRecoveryAttemptAuthorization},
		{name: "before checked", grant: grant, failure: failure, at: now.Add(-time.Nanosecond), want: ErrInvalidForwardRecoveryAttemptAuthorization},
		{name: "expired", grant: grant, failure: failure, at: now.Add(31 * time.Second), want: ErrForwardRecoveryAttemptAuthorizationExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateForwardRecoveryAttemptAuthorization(
				test.grant, test.failure, test.at,
			); !errors.Is(err, test.want) {
				t.Fatalf("ValidateForwardRecoveryAttemptAuthorization() = %v, want %v", err, test.want)
			}
		})
	}

	completed := forwardActionCurrentAttempt(snapshot)
	completed.Status = RecoveryAttemptCompleted
	if err := ValidateCurrentForwardRecoveryAttemptFailure(
		grant, failure, snapshot.Receipt, completed, now.Add(time.Second),
	); !errors.Is(err, ErrInvalidForwardRecoveryAttemptAuthorization) {
		t.Fatalf("completed attempt validation = %v", err)
	}
}

func TestSystemRecoveryAuthorizerRejectsLeaseExpiredAsLiveFailure(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)

	failure, grant, err := authorizer.AuthorizeForwardRecoveryAttemptFailure(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion},
		RecoveryAttemptFailureLeaseExpired,
	)
	if !errors.Is(err, ErrInvalidForwardRecoveryAttemptAuthorization) ||
		failure != (ForwardRecoveryAttemptFailure{}) || grant != (ForwardRecoveryAttemptGrant{}) {
		t.Fatalf("lease-expired live failure = %#v %#v %v", failure, grant, err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}
