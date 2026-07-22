package ingest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSystemRecoveryAuthorizerMintsFixedAuthorizationDispositions(t *testing.T) {
	now := artifactAuthNow()
	tests := []struct {
		name            string
		mutate          func(*CurrentForwardRecoverySnapshot)
		wantDisposition ForwardRecoveryAuthorizationDisposition
		wantHoldDue     bool
	}{
		{
			name: "withdrawn current consent is denied hold",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				snapshot.ConsentState.Status = "withdrawn"
				snapshot.ConsentState.EffectiveAt = now.Add(-time.Minute)
			},
			wantDisposition: ForwardRecoveryAuthorizationDenied,
			wantHoldDue:     true,
		},
		{
			name: "readable malformed installation is unavailable release",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				snapshot.Installation.Revision = 0
			},
			wantDisposition: ForwardRecoveryAuthorizationUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := artifactAuthSnapshot(now)
			test.mutate(&snapshot)
			store := &artifactAuthStoreStub{snapshot: snapshot}
			authorizer := artifactAuthAuthorizer(t, store, now)
			lease := artifactAuthLease(snapshot)
			attempt := RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion}

			command, grant, err := authorizer.AuthorizeForwardRecoveryDisposition(
				context.Background(), snapshot.Receipt.TenantID,
				snapshot.Receipt.ReservationKey, lease, attempt,
			)
			if err != nil {
				t.Fatalf("AuthorizeForwardRecoveryDisposition() = %v", err)
			}
			if command.Disposition != test.wantDisposition ||
				command.HoldReviewDueAt.IsZero() != !test.wantHoldDue ||
				command.ReceiptRevision != snapshot.Receipt.Revision ||
				!sameLeaseFence(command.Fence, lease.Fence) {
				t.Fatalf("command = %#v", command)
			}
			if err := ValidateForwardRecoveryDispositionAuthorization(
				grant, command, now.Add(time.Second),
			); err != nil {
				t.Fatalf("ValidateForwardRecoveryDispositionAuthorization() = %v", err)
			}
			if err := ValidateCurrentForwardRecoveryDisposition(
				grant, command, snapshot, forwardActionCurrentAttempt(snapshot), now.Add(time.Second),
			); err != nil {
				t.Fatalf("ValidateCurrentForwardRecoveryDisposition() = %v", err)
			}
		})
	}
}

func TestSystemRecoveryAuthorizerMintsNoDispositionForAllowedOrUnreadableState(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	lease := artifactAuthLease(snapshot)
	attempt := RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion}

	authorizer := artifactAuthAuthorizer(t, &artifactAuthStoreStub{snapshot: snapshot}, now)
	command, grant, err := authorizer.AuthorizeForwardRecoveryDisposition(
		context.Background(), snapshot.Receipt.TenantID, snapshot.Receipt.ReservationKey, lease, attempt,
	)
	if !errors.Is(err, ErrForwardRecoveryDispositionNotRequired) ||
		command != (ForwardRecoveryDispositionCommand{}) ||
		grant != (ForwardRecoveryDispositionGrant{}) {
		t.Fatalf("allowed disposition = %#v, %#v, %v", command, grant, err)
	}

	authorizer = artifactAuthAuthorizer(t, &artifactAuthStoreStub{err: errors.New("provider details")}, now)
	command, grant, err = authorizer.AuthorizeForwardRecoveryDisposition(
		context.Background(), snapshot.Receipt.TenantID, snapshot.Receipt.ReservationKey, lease, attempt,
	)
	if !errors.Is(err, ErrForwardRecoveryAuthorizationUnavailable) ||
		command != (ForwardRecoveryDispositionCommand{}) ||
		grant != (ForwardRecoveryDispositionGrant{}) {
		t.Fatalf("unreadable disposition = %#v, %#v, %v", command, grant, err)
	}
}

func TestForwardRecoveryDispositionGrantRejectsMutationExpiryAndDomainSwap(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	snapshot.ConsentState.Status = "withdrawn"
	snapshot.ConsentState.EffectiveAt = now.Add(-time.Minute)
	lease := artifactAuthLease(snapshot)
	attempt := RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion}
	authorizer := artifactAuthAuthorizer(t, &artifactAuthStoreStub{snapshot: snapshot}, now)
	command, grant, err := authorizer.AuthorizeForwardRecoveryDisposition(
		context.Background(), snapshot.Receipt.TenantID, snapshot.Receipt.ReservationKey, lease, attempt,
	)
	if err != nil {
		t.Fatalf("AuthorizeForwardRecoveryDisposition() = %v", err)
	}

	mutated := command
	mutated.Disposition = ForwardRecoveryAuthorizationUnavailable
	mutated.HoldReviewDueAt = time.Time{}
	if err := ValidateForwardRecoveryDispositionAuthorization(grant, mutated, now.Add(time.Second)); !errors.Is(err, ErrInvalidForwardRecoveryDispositionAuthorization) {
		t.Fatalf("mutated disposition validation = %v", err)
	}
	if err := ValidateForwardRecoveryDispositionAuthorization(
		grant, command, command.Fence.ExpiresAt,
	); !errors.Is(err, ErrForwardRecoveryDispositionAuthorizationExpired) {
		t.Fatalf("expired disposition validation = %v", err)
	}
	if err := ValidateForwardRecoveryActionAuthorization(
		ForwardRecoveryActionGrant{}, ForwardRecoveryActionCommand{}, now,
	); !errors.Is(err, ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("normal action accepted disposition grant boundary = %v", err)
	}

	current := snapshot
	current.ConsentState.Status = "granted"
	current.ConsentState.EffectiveAt = snapshot.Consent.GrantedAt.UTC()
	if err := ValidateCurrentForwardRecoveryDisposition(
		grant, command, current, forwardActionCurrentAttempt(snapshot), now.Add(time.Second),
	); !errors.Is(err, ErrInvalidForwardRecoveryDispositionAuthorization) {
		t.Fatalf("allowed current state validation = %v", err)
	}
}

func TestForwardRecoveryDispositionPublicInputExcludesActionAndEvidence(t *testing.T) {
	surface := reflect.TypeOf(ForwardRecoveryDispositionCommand{})
	for index := 0; index < surface.NumField(); index++ {
		name := strings.ToLower(surface.Field(index).Name)
		for _, forbidden := range []string{
			"action", "holdcode", "releasecode", "error", "path", "raw", "manifest",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("%s contains caller-controlled %q", surface.Field(index).Name, forbidden)
			}
		}
	}
}

func TestRecoveryHoldReviewDueIsStrictlyBeforeArtifactExpiry(t *testing.T) {
	now := artifactAuthNow()
	expiresAt := now.Add(2 * time.Nanosecond)
	dueAt, err := boundedRecoveryHoldReviewDueAt(now, expiresAt)
	if err != nil || !now.Before(dueAt) || !dueAt.Before(expiresAt) ||
		!dueAt.Equal(expiresAt.Add(-time.Nanosecond)) {
		t.Fatalf("boundedRecoveryHoldReviewDueAt() = %v, %v", dueAt, err)
	}
	if _, err := boundedRecoveryHoldReviewDueAt(now, now.Add(time.Nanosecond)); !errors.Is(err, ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("one-nanosecond window = %v", err)
	}
}

func TestNormalForwardRecoveryActionRejectsAuthorizationOnlyCodes(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	base := ForwardRecoveryActionCommand{
		TenantID: snapshot.Receipt.TenantID, ReservationKey: snapshot.Receipt.ReservationKey,
		Attempt:         RecoveryAttemptProposal{ID: snapshot.Receipt.LeaseOwnerID, WorkerVersion: RecoveryWorkerVersion},
		ReceiptRevision: snapshot.Receipt.Revision,
		Fence: LeaseFence{OwnerID: snapshot.Receipt.LeaseOwnerID, Token: snapshot.Receipt.FencingToken,
			ExpiresAt: snapshot.Receipt.LeaseExpiresAt},
	}
	hold := base
	hold.Plan = ForwardRecoveryActionPlan{
		Phase: RecoveryPhaseInitial, Action: ForwardRecoveryActionMarkHold,
		Classification: ArtifactClassificationManifestOnly,
		ReasonCode:     ArtifactReasonReferencedRawNotFound,
		HoldCode:       RecoveryHoldCurrentAuthorizationDenied,
	}
	hold.HoldReviewDueAt = now.Add(time.Hour)
	if !errors.Is(validateForwardRecoveryActionCommand(hold), ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatal("normal action accepted current authorization hold code")
	}
	release := base
	release.Plan = ForwardRecoveryActionPlan{
		Phase: RecoveryPhaseInitial, Action: ForwardRecoveryActionReleaseLease,
		Classification: ArtifactClassificationNone, ReasonCode: ArtifactReasonNoCandidates,
		ReleaseCode: LeaseReleaseAuthorizationUnavailable,
	}
	if !errors.Is(validateForwardRecoveryActionCommand(release), ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatal("normal action accepted authorization unavailable release code")
	}
}

func TestEvaluateForwardRecoveryOutcomeCorrelatesAuthorizationDispositions(t *testing.T) {
	now := artifactAuthNow()
	tests := []struct {
		name   string
		mutate func(*CurrentForwardRecoverySnapshot)
		want   RecoveryAttemptOutcome
	}{
		{
			name: "denied hold",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				snapshot.ConsentState.Status = "withdrawn"
				snapshot.ConsentState.EffectiveAt = now.Add(-time.Minute)
			},
			want: RecoveryAttemptOutcomeHold,
		},
		{
			name: "unavailable release",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				snapshot.Installation.Revision = 0
			},
			want: RecoveryAttemptOutcomeLeaseReleased,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := artifactAuthSnapshot(now)
			test.mutate(&snapshot)
			lease := artifactAuthLease(snapshot)
			attempt := RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion}
			authorizer := artifactAuthAuthorizer(t, &artifactAuthStoreStub{snapshot: snapshot}, now)
			command, _, err := authorizer.AuthorizeForwardRecoveryDisposition(
				context.Background(), snapshot.Receipt.TenantID,
				snapshot.Receipt.ReservationKey, lease, attempt,
			)
			if err != nil {
				t.Fatalf("AuthorizeForwardRecoveryDisposition() = %v", err)
			}
			query, err := ForwardRecoveryOutcomeQueryForDisposition(command)
			if err != nil {
				t.Fatalf("ForwardRecoveryOutcomeQueryForDisposition() = %v", err)
			}
			completedAt := now.Add(time.Second)
			outcomeSnapshot := forwardDispositionCompletedSnapshot(snapshot, command, completedAt)
			outcome, err := EvaluateForwardRecoveryActionOutcome(
				query, outcomeSnapshot, completedAt.Add(time.Second),
			)
			if err != nil || outcome.CommitStatus != RecoveryActionCommitted ||
				outcome.Outcome != test.want || outcome.ActionHash != query.ExpectedActionHash {
				t.Fatalf("EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
			}
			wrongDomainQuery := query
			wrongDomainQuery.ExpectedDecisionDomain = ForwardRecoveryDecisionArtifactReconciliation
			if outcome, err := EvaluateForwardRecoveryActionOutcome(
				wrongDomainQuery, outcomeSnapshot, completedAt.Add(time.Second),
			); err == nil && outcome.CommitStatus == RecoveryActionCommitted {
				t.Fatalf("wrong decision domain was committed: %#v", outcome)
			}

			outcomeSnapshot.Attempt.AuthorizationDisposition = ForwardRecoveryAuthorizationDenied
			if command.Disposition == ForwardRecoveryAuthorizationDenied {
				outcomeSnapshot.Attempt.AuthorizationDisposition = ForwardRecoveryAuthorizationUnavailable
			}
			if outcome, err := EvaluateForwardRecoveryActionOutcome(
				query, outcomeSnapshot, completedAt.Add(time.Second),
			); err == nil && outcome.CommitStatus == RecoveryActionCommitted {
				t.Fatalf("swapped disposition was committed: %#v", outcome)
			}
		})
	}
}

func forwardDispositionCompletedSnapshot(
	snapshot CurrentForwardRecoverySnapshot,
	command ForwardRecoveryDispositionCommand,
	completedAt time.Time,
) CurrentForwardRecoveryOutcomeSnapshot {
	receipt := snapshot.Receipt
	startedAt := receipt.LeaseAcquiredAt
	receipt.LeaseOwnerID = ""
	receipt.LeaseOwnerKind = ""
	receipt.LeaseAcquiredAt = time.Time{}
	receipt.LeaseHeartbeatAt = time.Time{}
	receipt.LeaseExpiresAt = time.Time{}
	receipt.Revision = command.ReceiptRevision + 1
	receipt.UpdatedAt = completedAt
	actionHash, _ := ForwardRecoveryDispositionHash(command)
	attempt := CurrentForwardRecoveryOutcomeAttempt{
		AttemptID: command.Attempt.ID, TenantID: command.TenantID,
		ReceiptID: receipt.ReceiptID, OwnerKind: LeaseOwnerSweeper,
		FencingToken: command.Fence.Token, WorkerVersion: command.Attempt.WorkerVersion,
		Status:                   RecoveryAttemptCompleted,
		DecisionDomain:           ForwardRecoveryDecisionCurrentAuthorization,
		AuthorizationDisposition: command.Disposition,
		ActionHash:               actionHash, StartedAt: startedAt, CompletedAt: completedAt,
	}
	switch command.Disposition {
	case ForwardRecoveryAuthorizationDenied:
		attempt.Action = ForwardRecoveryActionMarkHold
		attempt.Outcome = RecoveryAttemptOutcomeHold
		attempt.HoldCode = RecoveryHoldCurrentAuthorizationDenied
		attempt.HoldReviewDueAt = command.HoldReviewDueAt
		receipt.State = ReceiptRecoveryHold
		receipt.RecoveryHoldCode = RecoveryHoldCurrentAuthorizationDenied
		receipt.RecoveryHoldReviewDueAt = command.HoldReviewDueAt
		receipt.NextRecoveryAt = time.Time{}
		receipt.LastRecoveryCode = ""
	case ForwardRecoveryAuthorizationUnavailable:
		attempt.Action = ForwardRecoveryActionReleaseLease
		attempt.Outcome = RecoveryAttemptOutcomeLeaseReleased
		attempt.ReleaseCode = LeaseReleaseAuthorizationUnavailable
		receipt.State = ReceiptReserved
		receipt.RecoveryHoldCode = ""
		receipt.RecoveryHoldReviewDueAt = time.Time{}
		receipt.NextRecoveryAt = completedAt.Add(InitialRecoveryBackoff)
		if receipt.NextRecoveryAt.After(receipt.ReservationDeadline) {
			receipt.NextRecoveryAt = receipt.ReservationDeadline
		}
		receipt.LastRecoveryCode = string(LeaseReleaseAuthorizationUnavailable)
	}
	return CurrentForwardRecoveryOutcomeSnapshot{
		Receipt:  receiptToForwardOutcomeProjection(receipt),
		Attempt:  attempt,
		ReadTime: completedAt,
	}
}
