package ingest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestForwardRecoveryOutcomePublicSurfacesExcludePrivateData(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(ForwardRecoveryOutcomeQuery{}),
		reflect.TypeOf(RecoveryActionOutcome{}),
		reflect.TypeOf(CurrentForwardRecoveryOutcomeAttempt{}),
		reflect.TypeOf(CurrentForwardRecoveryOutcomeReceipt{}),
	}
	for _, surface := range types {
		for index := 0; index < surface.NumField(); index++ {
			field := surface.Field(index)
			name := strings.ToLower(field.Name)
			for _, forbidden := range []string{
				"path", "uid", "appid", "body", "coordinate", "latitude", "longitude",
				"deviceid", "tripid", "installationid", "consentrevisionid",
			} {
				if strings.Contains(name, forbidden) {
					t.Fatalf("%s.%s contains forbidden private surface %q", surface.Name(), field.Name, forbidden)
				}
			}
		}
	}
}

func TestSystemRecoveryOutcomeAuthorizerReturnsCommittedCorrelation(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeStoredCommand(t, now)
	query, err := ForwardRecoveryOutcomeQueryForAction(command)
	if err != nil {
		t.Fatalf("ForwardRecoveryOutcomeQueryForAction() = %v", err)
	}
	if !sameLeaseFence(query.ExpectedFence, command.Fence) {
		t.Fatalf("query fence = %#v, want %#v", query.ExpectedFence, command.Fence)
	}
	snapshot := forwardOutcomeCommittedSnapshot(command, now.Add(2*time.Second))
	store := &forwardOutcomeStoreStub{snapshot: snapshot}
	authorizer, err := NewSystemRecoveryOutcomeAuthorizer(store, func() time.Time { return now.Add(2 * time.Second) })
	if err != nil {
		t.Fatalf("NewSystemRecoveryOutcomeAuthorizer() = %v", err)
	}
	grant, err := authorizer.Authorize(context.Background(), query)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	if err := ValidateForwardRecoveryOutcomeAuthorization(grant, query, now.Add(3*time.Second)); err != nil {
		t.Fatalf("ValidateForwardRecoveryOutcomeAuthorization() = %v", err)
	}
	deadline, err := ForwardRecoveryOutcomeAuthorizationDeadline(grant, query)
	if err != nil || !deadline.Equal(now.Add(2*time.Second).Add(ForwardRecoveryOutcomeGrantTTL)) {
		t.Fatalf("ForwardRecoveryOutcomeAuthorizationDeadline() = %v, %v", deadline, err)
	}
	outcome, err := EvaluateForwardRecoveryActionOutcome(query, snapshot, now.Add(3*time.Second))
	if err != nil || outcome.CommitStatus != RecoveryActionCommitted ||
		outcome.Outcome != RecoveryAttemptOutcomeStored || outcome.ActionHash != query.ExpectedActionHash ||
		outcome.ReceiptRevision != query.ExpectedReceiptRevision ||
		!outcome.CompletedAt.Equal(snapshot.Attempt.CompletedAt) {
		t.Fatalf("EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
}

func TestForwardRecoveryOutcomeGrantRejectsMutationAndExpiry(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeStoredCommand(t, now)
	query, _ := ForwardRecoveryOutcomeQueryForAction(command)
	snapshot := forwardOutcomeCommittedSnapshot(command, now.Add(2*time.Second))
	store := &forwardOutcomeStoreStub{snapshot: snapshot}
	authorizer, _ := NewSystemRecoveryOutcomeAuthorizer(store, func() time.Time { return now.Add(2 * time.Second) })
	grant, err := authorizer.Authorize(context.Background(), query)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	mutated := query
	mutated.ExpectedReceiptRevision++
	zeroFence := query
	zeroFence.ExpectedFence = LeaseFence{}
	mutatedFenceOwner := query
	mutatedFenceOwner.AttemptID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	mutatedFenceOwner.ExpectedFence.OwnerID = mutatedFenceOwner.AttemptID
	mutatedFenceToken := query
	mutatedFenceToken.ExpectedFence.Token++
	mutatedFenceExpiry := query
	mutatedFenceExpiry.ExpectedFence.ExpiresAt = mutatedFenceExpiry.ExpectedFence.ExpiresAt.Add(time.Second)
	mutatedHash := query
	mutatedHash.ExpectedActionHash = strings.Repeat("d", 64)
	tests := []struct {
		name  string
		grant ForwardRecoveryOutcomeReadGrant
		query ForwardRecoveryOutcomeQuery
		at    time.Time
		want  error
	}{
		{name: "zero grant", query: query, at: now.Add(3 * time.Second), want: ErrInvalidForwardRecoveryOutcomeAuthorization},
		{name: "mutated query", grant: grant, query: mutated, at: now.Add(3 * time.Second), want: ErrInvalidForwardRecoveryOutcomeAuthorization},
		{name: "zero fence", grant: grant, query: zeroFence, at: now.Add(3 * time.Second), want: ErrInvalidForwardRecoveryOutcomeAuthorization},
		{name: "mutated fence owner", grant: grant, query: mutatedFenceOwner, at: now.Add(3 * time.Second), want: ErrInvalidForwardRecoveryOutcomeAuthorization},
		{name: "mutated fence token", grant: grant, query: mutatedFenceToken, at: now.Add(3 * time.Second), want: ErrInvalidForwardRecoveryOutcomeAuthorization},
		{name: "mutated fence expiry", grant: grant, query: mutatedFenceExpiry, at: now.Add(3 * time.Second), want: ErrInvalidForwardRecoveryOutcomeAuthorization},
		{name: "mutated action hash", grant: grant, query: mutatedHash, at: now.Add(3 * time.Second), want: ErrInvalidForwardRecoveryOutcomeAuthorization},
		{name: "before checked", grant: grant, query: query, at: now, want: ErrInvalidForwardRecoveryOutcomeAuthorization},
		{name: "expired", grant: grant, query: query, at: now.Add(33 * time.Second), want: ErrForwardRecoveryOutcomeAuthorizationExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateForwardRecoveryOutcomeAuthorization(
				test.grant, test.query, test.at,
			); !errors.Is(err, test.want) {
				t.Fatalf("ValidateForwardRecoveryOutcomeAuthorization() = %v, want %v", err, test.want)
			}
		})
	}
}

func TestEvaluateForwardRecoveryActionOutcomeDistinguishesNotCommittedAndUnverifiable(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeStoredCommand(t, now)
	query, _ := ForwardRecoveryOutcomeQueryForAction(command)
	committed := forwardOutcomeCommittedSnapshot(command, now.Add(2*time.Second))

	notCommitted := committed
	notCommitted.Receipt = forwardOutcomeReceiptProjection(artifactAuthSnapshot(now).Receipt)
	notCommitted.Attempt = CurrentForwardRecoveryOutcomeAttempt{
		AttemptID: command.Attempt.ID, TenantID: command.TenantID,
		ReceiptID: notCommitted.Receipt.ReceiptID, OwnerKind: LeaseOwnerSweeper,
		FencingToken: command.Fence.Token, WorkerVersion: command.Attempt.WorkerVersion,
		Status: RecoveryAttemptStarted, StartedAt: notCommitted.Receipt.LeaseAcquiredAt,
	}
	notCommitted.ReadTime = now.Add(2 * time.Second)
	outcome, err := EvaluateForwardRecoveryActionOutcome(query, notCommitted, now.Add(3*time.Second))
	if err != nil || outcome.CommitStatus != RecoveryActionNotCommitted || outcome.Outcome != "" ||
		outcome.ActionHash != "" {
		t.Fatalf("not committed = %#v, %v", outcome, err)
	}

	wrongHash := committed
	wrongHash.Attempt.ActionHash = command.ReservationKey
	outcome, err = EvaluateForwardRecoveryActionOutcome(query, wrongHash, now.Add(3*time.Second))
	if err != nil || outcome.CommitStatus != RecoveryActionUnverifiable || outcome.Outcome != "" ||
		outcome.ActionHash != "" {
		t.Fatalf("wrong hash = %#v, %v", outcome, err)
	}

	wrongReceipt := committed
	wrongReceipt.Receipt.State = ReceiptRejected
	wrongReceipt.Receipt.RejectionCode = "object_conflict"
	outcome, err = EvaluateForwardRecoveryActionOutcome(query, wrongReceipt, now.Add(3*time.Second))
	if err != nil || outcome.CommitStatus != RecoveryActionUnverifiable {
		t.Fatalf("wrong receipt = %#v, %v", outcome, err)
	}
}

func TestEvaluateForwardRecoveryActionOutcomeRejectsWrongCurrentFence(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeStoredCommand(t, now)
	query, _ := ForwardRecoveryOutcomeQueryForAction(command)

	tests := []struct {
		name   string
		status RecoveryAttemptStatus
		mutate func(*CurrentForwardRecoveryOutcomeSnapshot)
	}{
		{
			name:   "started attempt token",
			status: RecoveryAttemptStarted,
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.FencingToken++
			},
		},
		{
			name:   "started receipt owner",
			status: RecoveryAttemptStarted,
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Receipt.LeaseOwnerID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			},
		},
		{
			name:   "failed receipt token",
			status: RecoveryAttemptFailed,
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Receipt.FencingToken++
			},
		},
		{
			name:   "failed receipt expiry",
			status: RecoveryAttemptFailed,
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Receipt.LeaseExpiresAt = snapshot.Receipt.LeaseExpiresAt.Add(time.Second)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := forwardOutcomePendingSnapshot(command, now, test.status)
			test.mutate(&snapshot)
			outcome, err := EvaluateForwardRecoveryActionOutcome(
				query, snapshot, now.Add(3*time.Second),
			)
			if !errors.Is(err, ErrForwardRecoveryOutcomeUnavailable) ||
				outcome != (RecoveryActionOutcome{}) {
				t.Fatalf("EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
			}
		})
	}
}

func TestEvaluateForwardRecoveryActionOutcomeRequiresExactPendingReceiptShape(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeStoredCommand(t, now)
	query, _ := ForwardRecoveryOutcomeQueryForAction(command)
	tests := []struct {
		name   string
		mutate func(*CurrentForwardRecoveryOutcomeReceipt)
	}{
		{name: "sample already accepted", mutate: func(receipt *CurrentForwardRecoveryOutcomeReceipt) {
			receipt.SampleCount = 1
		}},
		{name: "artifact lineage present", mutate: func(receipt *CurrentForwardRecoveryOutcomeReceipt) {
			receipt.ObjectSHA256 = command.ReservationKey
		}},
		{name: "rejection present", mutate: func(receipt *CurrentForwardRecoveryOutcomeReceipt) {
			receipt.RejectionCode = "object_conflict"
		}},
		{name: "next recovery drift", mutate: func(receipt *CurrentForwardRecoveryOutcomeReceipt) {
			receipt.NextRecoveryAt = receipt.NextRecoveryAt.Add(time.Nanosecond)
		}},
		{name: "updated heartbeat drift", mutate: func(receipt *CurrentForwardRecoveryOutcomeReceipt) {
			receipt.UpdatedAt = receipt.UpdatedAt.Add(time.Nanosecond)
		}},
		{name: "zero expected samples", mutate: func(receipt *CurrentForwardRecoveryOutcomeReceipt) {
			receipt.ExpectedSampleCount = 0
		}},
		{name: "nonpositive artifact window", mutate: func(receipt *CurrentForwardRecoveryOutcomeReceipt) {
			receipt.ArtifactExpiresAt = receipt.ReservationDeadline
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := forwardOutcomePendingSnapshot(command, now, RecoveryAttemptStarted)
			test.mutate(&snapshot.Receipt)
			outcome, err := EvaluateForwardRecoveryActionOutcome(
				query, snapshot, now.Add(3*time.Second),
			)
			if !errors.Is(err, ErrForwardRecoveryOutcomeUnavailable) ||
				outcome != (RecoveryActionOutcome{}) {
				t.Fatalf("EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
			}
		})
	}
}

func TestSystemRecoveryOutcomeAuthorizerRejectsFutureReadTime(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeStoredCommand(t, now)
	query, _ := ForwardRecoveryOutcomeQueryForAction(command)
	snapshot := forwardOutcomeCommittedSnapshot(command, now.Add(2*time.Second))
	snapshot.ReadTime = now.Add(MaxForwardRecoveryAuthorizationClockSkew + time.Nanosecond)
	store := &forwardOutcomeStoreStub{snapshot: snapshot}
	authorizer, err := NewSystemRecoveryOutcomeAuthorizer(store, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewSystemRecoveryOutcomeAuthorizer() = %v", err)
	}

	grant, err := authorizer.Authorize(context.Background(), query)
	if !errors.Is(err, ErrForwardRecoveryOutcomeUnavailable) ||
		grant != (ForwardRecoveryOutcomeReadGrant{}) || store.calls != 1 {
		t.Fatalf("Authorize() = %#v, %v, calls %d", grant, err, store.calls)
	}

	snapshot.ReadTime = now.Add(3*time.Second + time.Nanosecond)
	outcome, err := EvaluateForwardRecoveryActionOutcome(query, snapshot, now.Add(3*time.Second))
	if !errors.Is(err, ErrForwardRecoveryOutcomeUnavailable) ||
		outcome != (RecoveryActionOutcome{}) {
		t.Fatalf("future EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
	}
}

func TestEvaluateForwardRecoveryActionOutcomeRejectsStoredSummaryDrift(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeCommand(t, now, ForwardRecoveryActionMarkStored)
	query, _ := ForwardRecoveryOutcomeQueryForAction(command)

	tests := []struct {
		name   string
		mutate func(*CurrentForwardRecoveryOutcomeSnapshot)
	}{
		{
			name: "receipt raw digest",
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Receipt.ObjectSHA256 = strings.Repeat("d", 64)
			},
		},
		{
			name: "receipt manifest generation",
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Receipt.ManifestGeneration++
			},
		},
		{
			name: "attempt raw digest",
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.RawSHA256 = strings.Repeat("d", 64)
			},
		},
		{
			name: "attempt manifest generation",
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.ManifestGeneration++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := forwardOutcomeCompletedSnapshot(command, now.Add(2*time.Second))
			test.mutate(&snapshot)
			outcome, err := EvaluateForwardRecoveryActionOutcome(
				query, snapshot, now.Add(3*time.Second),
			)
			if err != nil || outcome.CommitStatus != RecoveryActionUnverifiable ||
				outcome.ActionHash != "" || outcome.Outcome != "" {
				t.Fatalf("EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
			}
		})
	}
}

func TestEvaluateForwardRecoveryActionOutcomeRejectsStoredInitialPhase(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeCommand(t, now, ForwardRecoveryActionMarkStored)
	query, _ := ForwardRecoveryOutcomeQueryForAction(command)
	snapshot := forwardOutcomeCompletedSnapshot(command, now.Add(2*time.Second))
	snapshot.Attempt.Phase = RecoveryPhaseInitial

	outcome, err := EvaluateForwardRecoveryActionOutcome(query, snapshot, now.Add(3*time.Second))
	if !errors.Is(err, ErrForwardRecoveryOutcomeUnavailable) ||
		outcome != (RecoveryActionOutcome{}) {
		t.Fatalf("EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
	}
}

func TestEvaluateForwardRecoveryActionOutcomeRequiresRejectedRawOnlySummary(t *testing.T) {
	now := artifactAuthNow()
	command := forwardOutcomeCommand(t, now, ForwardRecoveryActionMarkRejected)
	query, _ := ForwardRecoveryOutcomeQueryForAction(command)
	valid := forwardOutcomeCompletedSnapshot(command, now.Add(2*time.Second))

	outcome, err := EvaluateForwardRecoveryActionOutcome(query, valid, now.Add(3*time.Second))
	if err != nil || outcome.CommitStatus != RecoveryActionCommitted ||
		outcome.Outcome != RecoveryAttemptOutcomeRejected || outcome.ActionHash != query.ExpectedActionHash ||
		valid.Attempt.RawSHA256 == "" || !emptyOutcomeManifestLineage(valid.Attempt) {
		t.Fatalf("valid rejected outcome = %#v, %v, attempt %#v", outcome, err, valid.Attempt)
	}

	tests := []struct {
		name   string
		mutate func(*CurrentForwardRecoveryOutcomeSnapshot)
	}{
		{
			name: "missing raw",
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.RawSHA256 = ""
			},
		},
		{
			name: "unexpected manifest",
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.ManifestSHA256 = strings.Repeat("d", 64)
				snapshot.Attempt.ManifestSize = 1
				snapshot.Attempt.ManifestGeneration = 1
				snapshot.Attempt.ManifestMetageneration = 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := valid
			test.mutate(&snapshot)
			outcome, err := EvaluateForwardRecoveryActionOutcome(
				query, snapshot, now.Add(3*time.Second),
			)
			if !errors.Is(err, ErrForwardRecoveryOutcomeUnavailable) ||
				outcome != (RecoveryActionOutcome{}) {
				t.Fatalf("EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
			}
		})
	}
}

func TestEvaluateForwardRecoveryActionOutcomeRejectsHoldAndReleaseMappingMismatch(t *testing.T) {
	now := artifactAuthNow()
	tests := []struct {
		name   string
		action ForwardRecoveryAction
		mutate func(*CurrentForwardRecoveryOutcomeSnapshot)
	}{
		{
			name:   "hold code does not match classification",
			action: ForwardRecoveryActionMarkHold,
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.HoldCode = RecoveryHoldManifestConflict
			},
		},
		{
			name:   "hold reason does not match classification",
			action: ForwardRecoveryActionMarkHold,
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.ReasonCode = ArtifactReasonManifestNoncanonical
			},
		},
		{
			name:   "release code does not match no candidates",
			action: ForwardRecoveryActionReleaseLease,
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.ReleaseCode = LeaseReleaseArtifactUnavailable
			},
		},
		{
			name:   "release reason does not match classification",
			action: ForwardRecoveryActionReleaseLease,
			mutate: func(snapshot *CurrentForwardRecoveryOutcomeSnapshot) {
				snapshot.Attempt.ReasonCode = ArtifactReasonProviderTimeout
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := forwardOutcomeCommand(t, now, test.action)
			query, _ := ForwardRecoveryOutcomeQueryForAction(command)
			snapshot := forwardOutcomeCompletedSnapshot(command, now.Add(2*time.Second))
			test.mutate(&snapshot)
			outcome, err := EvaluateForwardRecoveryActionOutcome(
				query, snapshot, now.Add(3*time.Second),
			)
			if !errors.Is(err, ErrForwardRecoveryOutcomeUnavailable) ||
				outcome != (RecoveryActionOutcome{}) {
				t.Fatalf("EvaluateForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
			}
		})
	}
}

type forwardOutcomeStoreStub struct {
	snapshot CurrentForwardRecoveryOutcomeSnapshot
	err      error
	calls    int
}

func (s *forwardOutcomeStoreStub) LoadCurrentForwardRecoveryOutcome(
	_ context.Context,
	_ ForwardRecoveryOutcomeQuery,
) (CurrentForwardRecoveryOutcomeSnapshot, error) {
	s.calls++
	return s.snapshot, s.err
}

func forwardOutcomeStoredCommand(t *testing.T, now time.Time) ForwardRecoveryActionCommand {
	t.Helper()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)
	request, _, err := authorizer.Authorize(
		context.Background(), snapshot.Receipt.TenantID, snapshot.Receipt.ReservationKey, lease,
	)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	command, _, err := authorizer.AuthorizeForwardRecoveryAction(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion},
		forwardActionStoredInput(request, now),
	)
	if err != nil {
		t.Fatalf("AuthorizeForwardRecoveryAction() = %v", err)
	}
	return command
}

func forwardOutcomeCommand(
	t *testing.T,
	now time.Time,
	action ForwardRecoveryAction,
) ForwardRecoveryActionCommand {
	t.Helper()
	command := forwardOutcomeStoredCommand(t, now)
	raw := *command.Plan.Raw
	command.HoldReviewDueAt = time.Time{}
	switch action {
	case ForwardRecoveryActionMarkStored:
	case ForwardRecoveryActionMarkRejected:
		command.Plan = ForwardRecoveryActionPlan{
			Phase:          RecoveryPhaseConfirmation,
			Action:         action,
			Classification: ArtifactClassificationRawContentConflict,
			ReasonCode:     ArtifactReasonStrictPayloadInvalid,
			RejectionCode:  "object_conflict",
			Raw:            &raw,
		}
	case ForwardRecoveryActionMarkHold:
		command.Plan = ForwardRecoveryActionPlan{
			Phase:          RecoveryPhaseInitial,
			Action:         action,
			Classification: ArtifactClassificationManifestOnly,
			ReasonCode:     ArtifactReasonReferencedRawNotFound,
			HoldCode:       RecoveryHoldManifestOnly,
		}
		command.HoldReviewDueAt = now.Add(time.Hour)
	case ForwardRecoveryActionReleaseLease:
		command.Plan = ForwardRecoveryActionPlan{
			Phase:          RecoveryPhaseInitial,
			Action:         action,
			Classification: ArtifactClassificationNone,
			ReasonCode:     ArtifactReasonNoCandidates,
			ReleaseCode:    LeaseReleaseAwaitingClientReplay,
		}
	default:
		t.Fatalf("unsupported forward recovery action %q", action)
	}
	if _, err := ForwardRecoveryActionHash(command); err != nil {
		t.Fatalf("ForwardRecoveryActionHash() = %v", err)
	}
	return command
}

func forwardOutcomeCommittedSnapshot(
	command ForwardRecoveryActionCommand,
	completedAt time.Time,
) CurrentForwardRecoveryOutcomeSnapshot {
	return forwardOutcomeCompletedSnapshot(command, completedAt)
}

func forwardOutcomeCompletedSnapshot(
	command ForwardRecoveryActionCommand,
	completedAt time.Time,
) CurrentForwardRecoveryOutcomeSnapshot {
	receipt := artifactAuthSnapshot(artifactAuthNow()).Receipt
	startedAt := receipt.LeaseAcquiredAt
	receipt.LeaseOwnerID = ""
	receipt.LeaseOwnerKind = ""
	receipt.LeaseAcquiredAt = time.Time{}
	receipt.LeaseHeartbeatAt = time.Time{}
	receipt.LeaseExpiresAt = time.Time{}
	receipt.NextRecoveryAt = time.Time{}
	receipt.Revision = command.ReceiptRevision + 1
	receipt.UpdatedAt = completedAt
	actionHash, _ := ForwardRecoveryActionHash(command)
	attempt := CurrentForwardRecoveryOutcomeAttempt{
		AttemptID: command.Attempt.ID, TenantID: command.TenantID,
		ReceiptID: receipt.ReceiptID, OwnerKind: LeaseOwnerSweeper,
		FencingToken: command.Fence.Token, WorkerVersion: command.Attempt.WorkerVersion,
		Status:         RecoveryAttemptCompleted,
		DecisionDomain: ForwardRecoveryDecisionArtifactReconciliation,
		Phase:          command.Plan.Phase,
		Classification: command.Plan.Classification, ReasonCode: command.Plan.ReasonCode,
		Action: command.Plan.Action, ActionHash: actionHash, StartedAt: startedAt,
		HoldCode: command.Plan.HoldCode, ReleaseCode: command.Plan.ReleaseCode,
		RejectionCode: command.Plan.RejectionCode, HoldReviewDueAt: command.HoldReviewDueAt,
		CompletedAt: completedAt,
	}
	if command.Plan.Raw != nil {
		attempt.RawSHA256 = command.Plan.Raw.SHA256
		attempt.RawCRC32C = command.Plan.Raw.CRC32C
		attempt.RawSize = command.Plan.Raw.Size
		attempt.RawGeneration = command.Plan.Raw.Generation
		attempt.RawMetageneration = command.Plan.Raw.Metageneration
	}
	if command.Plan.Manifest != nil {
		attempt.ManifestSHA256 = command.Plan.Manifest.SHA256
		attempt.ManifestCRC32C = command.Plan.Manifest.CRC32C
		attempt.ManifestSize = command.Plan.Manifest.Size
		attempt.ManifestGeneration = command.Plan.Manifest.Generation
		attempt.ManifestMetageneration = command.Plan.Manifest.Metageneration
	}
	switch command.Plan.Action {
	case ForwardRecoveryActionMarkStored:
		attempt.Outcome = RecoveryAttemptOutcomeStored
		receipt.State = ReceiptStored
		receipt.ObjectPath = command.Plan.Raw.Path
		receipt.ObjectSHA256 = command.Plan.Raw.SHA256
		receipt.ObjectCRC32C = command.Plan.Raw.CRC32C
		receipt.ObjectSize = command.Plan.Raw.Size
		receipt.ObjectGeneration = command.Plan.Raw.Generation
		receipt.ObjectMetageneration = command.Plan.Raw.Metageneration
		receipt.ManifestPath = command.Plan.Manifest.Path
		receipt.ManifestSHA256 = command.Plan.Manifest.SHA256
		receipt.ManifestCRC32C = command.Plan.Manifest.CRC32C
		receipt.ManifestSize = command.Plan.Manifest.Size
		receipt.ManifestGeneration = command.Plan.Manifest.Generation
		receipt.ManifestMetageneration = command.Plan.Manifest.Metageneration
		receipt.SampleCount = receipt.ExpectedSampleCount
	case ForwardRecoveryActionMarkRejected:
		attempt.Outcome = RecoveryAttemptOutcomeRejected
		receipt.State = ReceiptRejected
		receipt.RejectionCode = command.Plan.RejectionCode
	case ForwardRecoveryActionMarkHold:
		attempt.Outcome = RecoveryAttemptOutcomeHold
		receipt.State = ReceiptRecoveryHold
		receipt.RecoveryHoldCode = command.Plan.HoldCode
		receipt.RecoveryHoldReviewDueAt = command.HoldReviewDueAt
	case ForwardRecoveryActionReleaseLease:
		attempt.Outcome = RecoveryAttemptOutcomeLeaseReleased
		receipt.State = ReceiptReserved
		receipt.NextRecoveryAt = completedAt.Add(InitialRecoveryBackoff)
		receipt.LastRecoveryCode = string(command.Plan.ReleaseCode)
	}
	return CurrentForwardRecoveryOutcomeSnapshot{
		Receipt:  receiptToForwardOutcomeProjection(receipt),
		Attempt:  attempt,
		ReadTime: completedAt,
	}
}

func forwardOutcomePendingSnapshot(
	command ForwardRecoveryActionCommand,
	now time.Time,
	status RecoveryAttemptStatus,
) CurrentForwardRecoveryOutcomeSnapshot {
	receipt := artifactAuthSnapshot(now).Receipt
	attempt := CurrentForwardRecoveryOutcomeAttempt{
		AttemptID: command.Attempt.ID, TenantID: command.TenantID,
		ReceiptID: receipt.ReceiptID, OwnerKind: LeaseOwnerSweeper,
		FencingToken: command.Fence.Token, WorkerVersion: command.Attempt.WorkerVersion,
		Status: status, StartedAt: receipt.LeaseAcquiredAt,
	}
	if status == RecoveryAttemptFailed {
		attempt.FailureCode = RecoveryAttemptFailureInvalidContract
		attempt.FailedAt = now.Add(time.Second)
	}
	return CurrentForwardRecoveryOutcomeSnapshot{
		Receipt:  receiptToForwardOutcomeProjection(receipt),
		Attempt:  attempt,
		ReadTime: now.Add(2 * time.Second),
	}
}

func forwardOutcomeReceiptProjection(receipt Receipt) CurrentForwardRecoveryOutcomeReceipt {
	return receiptToForwardOutcomeProjection(receipt)
}

func receiptToForwardOutcomeProjection(receipt Receipt) CurrentForwardRecoveryOutcomeReceipt {
	return CurrentForwardRecoveryOutcomeReceipt{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		ReceiptID: receipt.ReceiptID, State: receipt.State, Revision: receipt.Revision,
		ExpectedSampleCount: receipt.ExpectedSampleCount, SampleCount: receipt.SampleCount,
		ObjectSHA256: receipt.ObjectSHA256, ObjectCRC32C: receipt.ObjectCRC32C,
		ObjectSize: receipt.ObjectSize, ObjectGeneration: receipt.ObjectGeneration,
		ObjectMetageneration: receipt.ObjectMetageneration,
		ManifestSHA256:       receipt.ManifestSHA256, ManifestCRC32C: receipt.ManifestCRC32C,
		ManifestSize: receipt.ManifestSize, ManifestGeneration: receipt.ManifestGeneration,
		ManifestMetageneration: receipt.ManifestMetageneration,
		RejectionCode:          receipt.RejectionCode, RecoveryHoldCode: receipt.RecoveryHoldCode,
		RecoveryHoldReviewDueAt: receipt.RecoveryHoldReviewDueAt,
		FencingToken:            receipt.FencingToken, LeaseOwnerID: receipt.LeaseOwnerID,
		LeaseOwnerKind: receipt.LeaseOwnerKind, LeaseAcquiredAt: receipt.LeaseAcquiredAt,
		LeaseHeartbeatAt: receipt.LeaseHeartbeatAt, LeaseExpiresAt: receipt.LeaseExpiresAt,
		NextRecoveryAt: receipt.NextRecoveryAt, LastRecoveryCode: receipt.LastRecoveryCode,
		ReservationDeadline: receipt.ReservationDeadline,
		ArtifactExpiresAt:   receipt.ArtifactExpiresAt,
		UpdatedAt:           receipt.UpdatedAt,
	}
}
