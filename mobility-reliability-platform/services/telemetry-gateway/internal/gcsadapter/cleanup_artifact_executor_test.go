package gcsadapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupSingleArtifactExecutorTouchesOnlyGrantedRawArtifact(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidComplete, now)
	request := cleanupSingleArtifactRequest(plan, ingest.CleanupArtifactExecutionRaw)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(request.ExpectedPath, liveCleanupInventory(*request.Lineage)),
		inspectCall(*request.Lineage),
		deleteCall(*request.Lineage, nil),
	}}
	result, err := cleanupSingleArtifactTestInstance(backend, now).ExecuteCleanupArtifact(
		context.Background(), firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, request,
	)
	if err != nil || result.DeleteOutcome != ingest.CleanupDeleteObserved ||
		result.Artifact != ingest.CleanupArtifactExecutionRaw || result.ErrorClass != "" {
		t.Fatalf("ExecuteCleanupArtifact() = %#v, %v", result, err)
	}
	backend.assertDone(t)
}

func TestCleanupSingleArtifactExecutorTouchesOnlyGrantedManifestArtifact(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationManifestOnly, now)
	request := cleanupSingleArtifactRequest(plan, ingest.CleanupArtifactExecutionManifest)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(request.ExpectedPath, liveCleanupInventory(*request.Lineage)),
		inspectCall(*request.Lineage),
		deleteCall(*request.Lineage, nil),
	}}
	result, err := cleanupSingleArtifactTestInstance(backend, now).ExecuteCleanupArtifact(
		context.Background(), firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, request,
	)
	if err != nil || result.DeleteOutcome != ingest.CleanupDeleteObserved ||
		result.Artifact != ingest.CleanupArtifactExecutionManifest {
		t.Fatalf("ExecuteCleanupArtifact() = %#v, %v", result, err)
	}
	backend.assertDone(t)
}

func TestCleanupSingleArtifactExecutorPreservesAmbiguousDeleteResultAndError(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, test := range []struct {
		name        string
		providerErr error
		errorClass  ingest.CleanupExecutionErrorClass
	}{
		{name: "timeout", providerErr: ingest.ErrArtifactProviderTimeout, errorClass: ingest.CleanupExecutionErrorProviderTimeout},
		{name: "cancelled", providerErr: ingest.ErrArtifactProviderCancelled, errorClass: ingest.CleanupExecutionErrorProviderCancelled},
		{name: "unavailable", providerErr: ingest.ErrArtifactProviderUnavailable, errorClass: ingest.CleanupExecutionErrorProviderUnavailable},
		{name: "response unverifiable", providerErr: ingest.ErrArtifactResponseUnverifiable, errorClass: ingest.CleanupExecutionErrorResponseUnverifiable},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
			request := cleanupSingleArtifactRequest(plan, ingest.CleanupArtifactExecutionRaw)
			backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
				inventoryCall(request.ExpectedPath, liveCleanupInventory(*request.Lineage)),
				inspectCall(*request.Lineage),
				deleteCall(*request.Lineage, test.providerErr),
			}}
			result, err := cleanupSingleArtifactTestInstance(backend, now).ExecuteCleanupArtifact(
				context.Background(), firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, request,
			)
			if !errors.Is(err, test.providerErr) || result.DeleteOutcome != ingest.CleanupDeleteUnknown ||
				result.ErrorClass != test.errorClass || result.RequestHash != request.RequestHash {
				t.Fatalf("ambiguous result = %#v, %v", result, err)
			}
			backend.assertDone(t)
		})
	}
}

func TestCleanupSingleArtifactExecutorPreservesUnknownWhenMutationDeadlineCrossesDuringDelete(
	t *testing.T,
) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	mutationDeadline := now.Add(time.Second)
	current := now
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	request := cleanupSingleArtifactRequest(plan, ingest.CleanupArtifactExecutionRaw)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(request.ExpectedPath, liveCleanupInventory(*request.Lineage)),
		inspectCall(*request.Lineage),
		deleteCall(*request.Lineage, nil),
	}}
	executor := cleanupSingleArtifactTestInstance(backend, now)
	executor.now = func() time.Time { return current }
	executor.authorizationDeadline = func(
		firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		ingest.CleanupArtifactExecutionRequest,
	) (time.Time, error) {
		return mutationDeadline, nil
	}
	executor.validateAuthorization = func(
		_ firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		_ ingest.CleanupArtifactExecutionRequest,
		observedAt time.Time,
	) error {
		if !observedAt.Before(mutationDeadline) {
			return firebaseadapter.ErrCleanupArtifactExecutionAuthorizationExpired
		}
		return nil
	}
	executor.deleter = &cleanupDeadlineCrossingDeleteBackend{
		backend: backend,
		afterDelete: func() {
			current = mutationDeadline.Add(time.Millisecond)
		},
	}

	result, err := executor.ExecuteCleanupArtifact(
		context.Background(), firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, request,
	)
	if !errors.Is(err, firebaseadapter.ErrCleanupArtifactExecutionAuthorizationExpired) ||
		result.DeleteOutcome != ingest.CleanupDeleteUnknown ||
		result.ErrorClass != ingest.CleanupExecutionErrorProviderTimeout ||
		result.MutationStartedAt.IsZero() || !result.ObservedAt.After(mutationDeadline) {
		t.Fatalf("deadline-crossing result = %#v, %v", result, err)
	}
	backend.assertDone(t)
}

func TestCleanupSingleArtifactExecutorUsesOneCompletionTimeForKnownDelete(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	mutationDeadline := now.Add(time.Second)
	afterDelete := false
	completionReads := 0
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	request := cleanupSingleArtifactRequest(plan, ingest.CleanupArtifactExecutionRaw)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(request.ExpectedPath, liveCleanupInventory(*request.Lineage)),
		inspectCall(*request.Lineage),
		deleteCall(*request.Lineage, nil),
	}}
	executor := cleanupSingleArtifactTestInstance(backend, now)
	executor.now = func() time.Time {
		if !afterDelete {
			return now
		}
		completionReads++
		if completionReads == 1 {
			return mutationDeadline.Add(-time.Nanosecond)
		}
		return mutationDeadline
	}
	executor.authorizationDeadline = func(
		firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		ingest.CleanupArtifactExecutionRequest,
	) (time.Time, error) {
		return mutationDeadline, nil
	}
	executor.validateAuthorization = func(
		_ firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		_ ingest.CleanupArtifactExecutionRequest,
		observedAt time.Time,
	) error {
		if !observedAt.Before(mutationDeadline) {
			return firebaseadapter.ErrCleanupArtifactExecutionAuthorizationExpired
		}
		return nil
	}
	executor.deleter = &cleanupDeadlineCrossingDeleteBackend{
		backend: backend,
		afterDelete: func() {
			afterDelete = true
		},
	}

	result, err := executor.ExecuteCleanupArtifact(
		context.Background(), firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, request,
	)
	if err != nil || result.DeleteOutcome != ingest.CleanupDeleteObserved ||
		!result.ObservedAt.Equal(mutationDeadline.Add(-time.Nanosecond)) ||
		completionReads != 1 {
		t.Fatalf("known boundary result = %#v, reads=%d, err=%v", result, completionReads, err)
	}
	backend.assertDone(t)
}

func TestCleanupSingleArtifactExecutorNonTargetedSkipsProvider(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	request := cleanupSingleArtifactRequest(plan, ingest.CleanupArtifactExecutionManifest)
	if request.Targeted || request.Lineage != nil {
		t.Fatalf("manifest request unexpectedly targeted: %#v", request)
	}
	backend := &scriptedCleanupExecutionBackend{t: t}
	result, err := cleanupSingleArtifactTestInstance(backend, now).ExecuteCleanupArtifact(
		context.Background(), firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, request,
	)
	if err != nil || result.DeleteOutcome != ingest.CleanupDeleteNotAttempted {
		t.Fatalf("non-targeted result = %#v, %v", result, err)
	}
	backend.assertDone(t)
}

func TestCleanupSingleArtifactExecutorDoesNotFabricateKnownOutcomeForHardFailure(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	request := cleanupSingleArtifactRequest(plan, ingest.CleanupArtifactExecutionRaw)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(request.ExpectedPath, liveCleanupInventory(*request.Lineage)),
		inspectCall(*request.Lineage),
		deleteCall(*request.Lineage, ingest.ErrArtifactPermissionDenied),
	}}
	result, err := cleanupSingleArtifactTestInstance(backend, now).ExecuteCleanupArtifact(
		context.Background(), firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, request,
	)
	if !errors.Is(err, ingest.ErrArtifactPermissionDenied) ||
		result != (ingest.CleanupArtifactExecutionResult{}) {
		t.Fatalf("permission result = %#v, %v", result, err)
	}
	backend.assertDone(t)
}

func TestCleanupSingleArtifactExecutorRejectsAuthorizationBeforeProvider(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	request := cleanupSingleArtifactRequest(plan, ingest.CleanupArtifactExecutionRaw)
	backend := &scriptedCleanupExecutionBackend{t: t}
	executor := cleanupSingleArtifactTestInstance(backend, now)
	executor.validateAuthorization = func(
		firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		ingest.CleanupArtifactExecutionRequest,
		time.Time,
	) error {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	result, err := executor.ExecuteCleanupArtifact(
		context.Background(), firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, request,
	)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) ||
		result != (ingest.CleanupArtifactExecutionResult{}) {
		t.Fatalf("unauthorized result = %#v, %v", result, err)
	}
	backend.assertDone(t)
}

func cleanupSingleArtifactTestInstance(
	backend *scriptedCleanupExecutionBackend,
	now time.Time,
) *CleanupSingleArtifactExecutor {
	return &CleanupSingleArtifactExecutor{
		reader: backend, deleter: backend, now: func() time.Time { return now },
		cloneRequest: func(
			request ingest.CleanupArtifactExecutionRequest,
		) (ingest.CleanupArtifactExecutionRequest, error) {
			cloned := request
			if request.Lineage != nil {
				lineage := *request.Lineage
				cloned.Lineage = &lineage
			}
			return cloned, nil
		},
		validateResult: func(
			ingest.CleanupArtifactExecutionRequest,
			ingest.CleanupArtifactExecutionResult,
		) error {
			return nil
		},
		validateAuthorization: func(
			firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
			ingest.CleanupArtifactExecutionRequest,
			time.Time,
		) error {
			return nil
		},
		authorizationDeadline: func(
			firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
			ingest.CleanupArtifactExecutionRequest,
		) (time.Time, error) {
			return now.Add(time.Minute), nil
		},
	}
}

type cleanupDeadlineCrossingDeleteBackend struct {
	backend     *scriptedCleanupExecutionBackend
	afterDelete func()
}

func (b *cleanupDeadlineCrossingDeleteBackend) DeleteGeneration(
	ctx context.Context,
	path string,
	generation int64,
	metageneration int64,
) error {
	err := b.backend.DeleteGeneration(ctx, path, generation, metageneration)
	if b.afterDelete != nil {
		b.afterDelete()
	}
	return err
}

func cleanupSingleArtifactRequest(
	plan ingest.CleanupExecutionPlan,
	artifact ingest.CleanupArtifactExecutionArtifact,
) ingest.CleanupArtifactExecutionRequest {
	request := ingest.CleanupArtifactExecutionRequest{
		Query: ingest.CleanupExecutionQuery{
			TenantID:       plan.Target.Command.TenantID,
			ReservationKey: plan.Target.Command.ReservationKey,
			AttemptID:      plan.Target.Command.AttemptID,
		},
		ExpectedTargetHash:      plan.Target.TargetHash,
		ExpectedPlanHash:        strings.Repeat("d", 64),
		ExpectedReceiptRevision: plan.Target.Command.ReceiptRevision,
		ExpectedFence: ingest.LeaseFence{
			OwnerID:   plan.Target.Command.AttemptID,
			Token:     plan.Target.Command.FencingToken,
			ExpiresAt: plan.Target.Command.LeaseExpiresAt,
		},
		Artifact: artifact, RequestHash: strings.Repeat("e", 64),
	}
	if artifact == ingest.CleanupArtifactExecutionRaw {
		request.ExpectedLedgerRevision = 1
		request.DispatchRevision = 2
		request.DispatchPhase = ingest.CleanupExecutionPhaseRawDispatchRecorded
		request.OutcomePhase = ingest.CleanupExecutionPhaseRawOutcomeRecorded
		request.ExpectedPath = plan.ExpectedRawPath
		request.Targeted = plan.Target.Command.Raw != nil
		if plan.Target.Command.Raw != nil {
			lineage := *plan.Target.Command.Raw
			request.Lineage = &lineage
		}
	} else {
		request.ExpectedLedgerRevision = 4
		request.DispatchRevision = 5
		request.DispatchPhase = ingest.CleanupExecutionPhaseManifestDispatchRecorded
		request.OutcomePhase = ingest.CleanupExecutionPhaseManifestOutcomeRecorded
		request.ExpectedPath = plan.ExpectedManifestPath
		request.Targeted = plan.Target.Command.Manifest != nil
		if plan.Target.Command.Manifest != nil {
			lineage := *plan.Target.Command.Manifest
			request.Lineage = &lineage
		}
	}
	return request
}
