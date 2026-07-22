package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSystemRecoveryAuthorizerMintsExactTerminalActionGrant(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)
	request, _, err := authorizer.Authorize(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
	)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	input := forwardActionStoredInput(request, now)
	attempt := RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion}

	command, grant, err := authorizer.AuthorizeForwardRecoveryAction(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		attempt,
		input,
	)
	if err != nil {
		t.Fatalf("AuthorizeForwardRecoveryAction() = %v", err)
	}
	if command.Plan.Action != ForwardRecoveryActionMarkStored ||
		command.Plan.Phase != RecoveryPhaseConfirmation || command.Plan.Raw == nil ||
		command.Plan.Manifest == nil || command.ReceiptRevision != request.ReceiptRevision ||
		!sameLeaseFence(command.Fence, lease.Fence) || command.Attempt != attempt {
		t.Fatalf("command = %#v", command)
	}
	if err := ValidateForwardRecoveryActionAuthorization(grant, command, now.Add(time.Second)); err != nil {
		t.Fatalf("ValidateForwardRecoveryActionAuthorization() = %v", err)
	}
	deadline, err := ForwardRecoveryActionAuthorizationDeadline(grant, command)
	if err != nil || !deadline.Equal(now.Add(ForwardRecoveryArtifactReadGrantTTL)) {
		t.Fatalf("ForwardRecoveryActionAuthorizationDeadline() = %v, %v", deadline, err)
	}
	actionHash, err := ForwardRecoveryActionHash(command)
	if err != nil || len(actionHash) != 64 || !isLowerHexDigest(actionHash) {
		t.Fatalf("ForwardRecoveryActionHash() = %q, %v", actionHash, err)
	}
	if store.calls != 2 {
		t.Fatalf("current-state reads = %d, want initial plus action authorization", store.calls)
	}
}

func TestForwardRecoveryActionGrantRejectsMutationAndExpiry(t *testing.T) {
	now := artifactAuthNow()
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
	command, grant, err := authorizer.AuthorizeForwardRecoveryAction(
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

	mutated := cloneForwardRecoveryActionCommand(command)
	mutated.Plan.Manifest.Generation++
	tests := []struct {
		name    string
		grant   ForwardRecoveryActionGrant
		command ForwardRecoveryActionCommand
		at      time.Time
		want    error
	}{
		{name: "zero grant", command: command, at: now.Add(time.Second), want: ErrInvalidForwardRecoveryActionAuthorization},
		{name: "mutated exact pin", grant: grant, command: mutated, at: now.Add(time.Second), want: ErrInvalidForwardRecoveryActionAuthorization},
		{name: "before checked time", grant: grant, command: command, at: now.Add(-time.Nanosecond), want: ErrInvalidForwardRecoveryActionAuthorization},
		{name: "expired", grant: grant, command: command, at: now.Add(31 * time.Second), want: ErrForwardRecoveryActionAuthorizationExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateForwardRecoveryActionAuthorization(test.grant, test.command, test.at); !errors.Is(err, test.want) {
				t.Fatalf("ValidateForwardRecoveryActionAuthorization() = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := ForwardRecoveryActionAuthorizationDeadline(grant, mutated); !errors.Is(err, ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("mutated deadline authorization = %v", err)
	}
}

func TestSystemRecoveryAuthorizerInvalidatesActionEvidenceAfterLeaseRenewal(t *testing.T) {
	now := artifactAuthNow()
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
	input := forwardActionStoredInput(request, now)
	store.snapshot.Receipt.Revision++
	store.snapshot.Receipt.LeaseHeartbeatAt = now
	store.snapshot.Receipt.LeaseExpiresAt = now.Add(3 * time.Minute)
	store.snapshot.Receipt.NextRecoveryAt = store.snapshot.Receipt.LeaseExpiresAt
	store.snapshot.Receipt.UpdatedAt = now
	renewed := artifactAuthLease(store.snapshot)
	store.calls = 0

	command, grant, err := authorizer.AuthorizeForwardRecoveryAction(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		renewed,
		RecoveryAttemptProposal{ID: renewed.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion},
		input,
	)
	if !errors.Is(err, ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("AuthorizeForwardRecoveryAction() = %v", err)
	}
	if command != (ForwardRecoveryActionCommand{}) || grant != (ForwardRecoveryActionGrant{}) {
		t.Fatalf("renewal received stale command/grant = %#v %#v", command, grant)
	}
	if store.calls != 1 {
		t.Fatalf("fresh current-state reads = %d, want 1", store.calls)
	}
}

func TestValidateCurrentForwardRecoveryActionReevaluatesConsentAndReceipt(t *testing.T) {
	now := artifactAuthNow()
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
	command, grant, err := authorizer.AuthorizeForwardRecoveryAction(
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
	attempt := forwardActionCurrentAttempt(snapshot)
	if err := ValidateCurrentForwardRecoveryAction(grant, command, snapshot, attempt, now.Add(time.Second)); err != nil {
		t.Fatalf("active snapshot validation = %v", err)
	}

	withdrawn := cloneCurrentForwardRecoverySnapshot(snapshot)
	withdrawnAt := now.Add(time.Second)
	withdrawn.Consent.Status = "withdrawn"
	withdrawn.Consent.WithdrawnAt = &withdrawnAt
	withdrawn.ConsentState.Status = "withdrawn"
	withdrawn.ConsentState.EffectiveAt = withdrawnAt
	if err := ValidateCurrentForwardRecoveryAction(grant, command, withdrawn, attempt, now.Add(time.Second)); !errors.Is(err, ErrForwardRecoveryUnauthorized) {
		t.Fatalf("withdrawn snapshot validation = %v", err)
	}

	stale := cloneCurrentForwardRecoverySnapshot(snapshot)
	stale.Receipt.Revision++
	if err := ValidateCurrentForwardRecoveryAction(grant, command, stale, attempt, now.Add(time.Second)); !errors.Is(err, ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("revision drift validation = %v", err)
	}
}

func TestValidateCurrentForwardRecoveryActionRequiresExactStartedAttemptAndConservativeExpiry(t *testing.T) {
	now := artifactAuthNow()
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
	command, grant, err := authorizer.AuthorizeForwardRecoveryAction(
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

	attempt := forwardActionCurrentAttempt(snapshot)
	completed := attempt
	completed.Status = RecoveryAttemptCompleted
	if err := ValidateCurrentForwardRecoveryAction(
		grant, command, snapshot, completed, now.Add(time.Second),
	); !errors.Is(err, ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("completed attempt validation = %v", err)
	}

	// observedAt remains inside the grant, but the transaction's authoritative
	// read time is beyond it. The conservative recheck must reject the action.
	futureRead := cloneCurrentForwardRecoverySnapshot(snapshot)
	futureRead.ReadTime = now.Add(31 * time.Second)
	if err := ValidateCurrentForwardRecoveryAction(
		grant, command, futureRead, attempt, now.Add(29*time.Second),
	); !errors.Is(err, ErrForwardRecoveryActionAuthorizationExpired) {
		t.Fatalf("future read-time expiry validation = %v", err)
	}
}

func TestSystemRecoveryAuthorizerDerivesBoundedHoldReviewTime(t *testing.T) {
	now := artifactAuthNow()
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
	manifest := ArtifactPinnedLineage{
		SHA256: strings.Repeat("c", 64), Size: 96, Generation: 92, Metageneration: 3,
	}
	result := ArtifactClassificationResult{
		Classification: ArtifactClassificationManifestOnly,
		ReasonCode:     ArtifactReasonReferencedRawNotFound,
		RetentionPhase: ArtifactRetentionBeforeExpiry,
		ManifestInventory: ArtifactInventorySummary{
			Performed: true, NonSoftDeletedCount: 1, Coverage: ArtifactInventoryCoverageComplete,
		},
		RawInventory: ArtifactInventorySummary{
			Performed: true, Coverage: ArtifactInventoryCoverageComplete,
		},
		PinnedManifest:     &manifest,
		ValidatorVersion:   request.ValidatorVersion,
		ObservedAt:         now,
		requestBindingHash: canonicalArtifactClassificationRequestBinding(request),
	}
	result = sealArtifactClassificationResult(request, result)
	command, grant, err := authorizer.AuthorizeForwardRecoveryAction(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion},
		ForwardRecoveryPlanInput{Phase: RecoveryPhaseInitial, Request: request, Result: result},
	)
	if err != nil {
		t.Fatalf("AuthorizeForwardRecoveryAction() = %v", err)
	}
	if command.Plan.Action != ForwardRecoveryActionMarkHold ||
		command.Plan.HoldCode != RecoveryHoldManifestOnly ||
		!command.HoldReviewDueAt.Equal(now.Add(DefaultRecoveryHoldReviewWindow)) {
		t.Fatalf("hold command = %#v", command)
	}
	if err := ValidateForwardRecoveryActionAuthorization(grant, command, now.Add(time.Second)); err != nil {
		t.Fatalf("hold grant validation = %v", err)
	}
}

func forwardActionStoredInput(
	request ArtifactClassificationRequest,
	observedAt time.Time,
) ForwardRecoveryPlanInput {
	raw := ArtifactPinnedLineage{
		SHA256: strings.Repeat("b", 64), Size: 128, Generation: 91, Metageneration: 2,
	}
	manifest := ArtifactPinnedLineage{
		SHA256: strings.Repeat("c", 64), Size: 96, Generation: 92, Metageneration: 3,
	}
	complete := func(pinRaw, pinManifest bool, at time.Time) ArtifactClassificationResult {
		result := ArtifactClassificationResult{
			Classification: ArtifactClassificationValidComplete,
			ReasonCode:     ArtifactReasonManifestAndReferencedRawValid,
			RetentionPhase: ArtifactRetentionBeforeExpiry,
			ManifestInventory: ArtifactInventorySummary{
				Performed: true, NonSoftDeletedCount: 1, Coverage: ArtifactInventoryCoverageComplete,
			},
			RawInventory: ArtifactInventorySummary{
				Performed: true, NonSoftDeletedCount: 1, Coverage: ArtifactInventoryCoverageComplete,
			},
			ValidatorVersion:   request.ValidatorVersion,
			ObservedAt:         at,
			requestBindingHash: canonicalArtifactClassificationRequestBinding(request),
		}
		if pinRaw {
			value := raw
			result.PinnedRaw = &value
		}
		if pinManifest {
			value := manifest
			result.PinnedManifest = &value
		}
		return sealArtifactClassificationResult(request, result)
	}
	prior := complete(true, true, observedAt.Add(-time.Second))
	current := complete(true, true, observedAt)
	return ForwardRecoveryPlanInput{
		Phase:       RecoveryPhaseConfirmation,
		Request:     request,
		Result:      current,
		PriorResult: &prior,
	}
}

func forwardActionCurrentAttempt(snapshot CurrentForwardRecoverySnapshot) CurrentForwardRecoveryAttempt {
	return CurrentForwardRecoveryAttempt{
		AttemptID:     snapshot.Receipt.LeaseOwnerID,
		TenantID:      snapshot.Receipt.TenantID,
		ReceiptID:     snapshot.Receipt.ReceiptID,
		OwnerKind:     snapshot.Receipt.LeaseOwnerKind,
		FencingToken:  snapshot.Receipt.FencingToken,
		WorkerVersion: RecoveryWorkerVersion,
		Status:        RecoveryAttemptStarted,
		StartedAt:     snapshot.Receipt.LeaseAcquiredAt,
	}
}
