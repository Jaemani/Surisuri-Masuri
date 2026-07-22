package ingest

import (
	"errors"
	"testing"
	"time"
)

func TestValidateLeaseProposal(t *testing.T) {
	valid := LeaseProposal{
		Owner:    LeaseOwner{ID: "01982015-4400-7000-8000-000000000002", Kind: LeaseOwnerRequest},
		Duration: DefaultRequestLeaseDuration,
	}
	tests := []struct {
		name     string
		proposal LeaseProposal
		wantErr  bool
	}{
		{name: "valid", proposal: valid},
		{name: "invalid owner id", proposal: LeaseProposal{Owner: LeaseOwner{ID: "request-1", Kind: LeaseOwnerRequest}, Duration: valid.Duration}, wantErr: true},
		{name: "invalid owner kind", proposal: LeaseProposal{Owner: LeaseOwner{ID: valid.Owner.ID, Kind: "client"}, Duration: valid.Duration}, wantErr: true},
		{name: "too short", proposal: LeaseProposal{Owner: valid.Owner, Duration: MinLeaseDuration - time.Nanosecond}, wantErr: true},
		{name: "too long", proposal: LeaseProposal{Owner: valid.Owner, Duration: MaxLeaseDuration + time.Nanosecond}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateLeaseProposal(test.proposal)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateLeaseProposal() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.wantErr && !errors.Is(err, ErrInvalidLease) {
				t.Fatalf("ValidateLeaseProposal() error = %v, want %v", err, ErrInvalidLease)
			}
		})
	}
}

func TestValidateLeaseGrant(t *testing.T) {
	acquiredAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	valid := LeaseGrant{
		Fence: LeaseFence{
			OwnerID:   "01982015-4400-7000-8000-000000000002",
			Token:     1,
			ExpiresAt: acquiredAt.Add(DefaultRequestLeaseDuration),
		},
		OwnerKind:   LeaseOwnerRequest,
		AcquiredAt:  acquiredAt,
		HeartbeatAt: acquiredAt,
	}
	if err := ValidateLeaseGrant(valid); err != nil {
		t.Fatalf("ValidateLeaseGrant() error = %v", err)
	}
	invalid := valid
	invalid.Fence.Token = 0
	if !errors.Is(ValidateLeaseGrant(invalid), ErrInvalidLease) {
		t.Fatal("ValidateLeaseGrant() accepted zero fencing token")
	}
	invalid = valid
	invalid.AcquiredAt = invalid.Fence.ExpiresAt
	if !errors.Is(ValidateLeaseGrant(invalid), ErrInvalidLease) {
		t.Fatal("ValidateLeaseGrant() accepted empty lease interval")
	}
	invalid = valid
	invalid.Fence.ExpiresAt = invalid.AcquiredAt.Add(MinLeaseDuration - time.Nanosecond)
	if !errors.Is(ValidateLeaseGrant(invalid), ErrInvalidLease) {
		t.Fatal("ValidateLeaseGrant() accepted a lease shorter than the minimum")
	}
	invalid = valid
	invalid.Fence.ExpiresAt = invalid.AcquiredAt.Add(MaxLeaseDuration + time.Nanosecond)
	if !errors.Is(ValidateLeaseGrant(invalid), ErrInvalidLease) {
		t.Fatal("ValidateLeaseGrant() accepted a lease longer than the maximum")
	}
}

func TestValidLeaseReleaseCode(t *testing.T) {
	if !ValidLeaseReleaseCode(LeaseReleaseArtifactUnavailable) ||
		!ValidLeaseReleaseCode(LeaseReleaseAuthorizationUnavailable) ||
		!ValidLeaseReleaseCode(LeaseReleaseAwaitingClientReplay) ||
		!ValidLeaseReleaseCode(LeaseReleaseFinalizerUnavailable) {
		t.Fatal("documented release code was rejected")
	}
	if ValidLeaseReleaseCode("client_cancelled") {
		t.Fatal("unknown release code was accepted")
	}
}

func TestValidateRecoveryAttemptProposal(t *testing.T) {
	valid := RecoveryAttemptProposal{
		ID:            "01982015-4400-7000-8000-000000000003",
		WorkerVersion: RecoveryWorkerVersion,
	}
	if err := ValidateRecoveryAttemptProposal(valid); err != nil {
		t.Fatalf("ValidateRecoveryAttemptProposal() error = %v", err)
	}
	for _, invalid := range []RecoveryAttemptProposal{
		{ID: "not-a-uuid", WorkerVersion: RecoveryWorkerVersion},
		{ID: valid.ID},
		{ID: valid.ID, WorkerVersion: "contains space"},
		{ID: valid.ID, WorkerVersion: "user@example.com"},
		{ID: valid.ID, WorkerVersion: "telemetry-recovery.v2"},
	} {
		if !errors.Is(ValidateRecoveryAttemptProposal(invalid), ErrInvalidLease) {
			t.Fatalf("ValidateRecoveryAttemptProposal(%#v) accepted invalid input", invalid)
		}
	}
}

func TestCleanupLeaseContracts(t *testing.T) {
	transitionedAt := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	acquiredAt := transitionedAt.Add(DefaultCleanupLateWriteGrace)
	ownerID := "01982015-4400-7000-8000-000000000004"
	proposal := CleanupAttemptProposal{ID: ownerID, WorkerVersion: CleanupWorkerVersion}
	if err := ValidateCleanupAttemptProposal(proposal); err != nil {
		t.Fatalf("ValidateCleanupAttemptProposal() error = %v", err)
	}
	for _, invalid := range []CleanupAttemptProposal{
		{ID: "not-a-uuid", WorkerVersion: CleanupWorkerVersion},
		{ID: ownerID, WorkerVersion: RecoveryWorkerVersion},
		{ID: ownerID, WorkerVersion: "telemetry-cleanup.v2"},
	} {
		if !errors.Is(ValidateCleanupAttemptProposal(invalid), ErrInvalidLease) {
			t.Fatalf("ValidateCleanupAttemptProposal(%#v) accepted invalid input", invalid)
		}
	}
	grant := CleanupLeaseGrant{
		Lease: LeaseGrant{
			Fence: LeaseFence{
				OwnerID: ownerID, Token: 3, ExpiresAt: acquiredAt.Add(DefaultRequestLeaseDuration),
			},
			OwnerKind: LeaseOwnerCleanup, AcquiredAt: acquiredAt, HeartbeatAt: acquiredAt,
		},
		ReceiptRevision: 3,
		Mode:            CleanupModeReservationExpiry,
		OriginStatus:    ReceiptReserved,
		PolicyVersion:   CleanupTransitionPolicyV1,
		TransitionedAt:  transitionedAt,
		QuiescenceUntil: acquiredAt,
	}
	if err := ValidateCleanupLeaseGrant(grant); err != nil {
		t.Fatalf("ValidateCleanupLeaseGrant() error = %v", err)
	}
	invalidGrant := grant
	invalidGrant.Lease.OwnerKind = LeaseOwnerSweeper
	if !errors.Is(ValidateCleanupLeaseGrant(invalidGrant), ErrInvalidLease) {
		t.Fatal("ValidateCleanupLeaseGrant() accepted sweeper owner")
	}
	invalidGrant = grant
	invalidGrant.QuiescenceUntil = acquiredAt.Add(time.Nanosecond)
	if !errors.Is(ValidateCleanupLeaseGrant(invalidGrant), ErrInvalidLease) {
		t.Fatal("ValidateCleanupLeaseGrant() accepted lease before quiescence")
	}
}

func TestCleanupTimingPolicyStrictlyCoversLeaseAndArtifactOperation(t *testing.T) {
	if err := ValidateCleanupTimingPolicy(); err != nil {
		t.Fatalf("ValidateCleanupTimingPolicy() error = %v", err)
	}
	if DefaultCleanupLateWriteGrace <= MaxLeaseDuration+MaxArtifactOperationTimeout {
		t.Fatalf(
			"cleanup grace = %v, want > lease %v + artifact operation %v",
			DefaultCleanupLateWriteGrace,
			MaxLeaseDuration,
			MaxArtifactOperationTimeout,
		)
	}
}
