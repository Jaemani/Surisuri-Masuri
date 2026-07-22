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

func TestCleanupArtifactExecutorDeletesRawBeforeManifestAndAuditsEach(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidComplete, now)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
		inspectCall(*plan.Target.Command.Raw),
		deleteCall(*plan.Target.Command.Raw, nil),
		inventoryCall(plan.ExpectedRawPath, emptyCleanupInventory()),
		inventoryCall(plan.ExpectedManifestPath, liveCleanupInventory(*plan.Target.Command.Manifest)),
		inspectCall(*plan.Target.Command.Manifest),
		deleteCall(*plan.Target.Command.Manifest, nil),
		inventoryCall(plan.ExpectedManifestPath, emptyCleanupInventory()),
	}}
	executor := cleanupExecutorTestInstance(backend, now)

	observation, err := executor.ExecuteCleanupTarget(
		context.Background(),
		firebaseadapter.CleanupExecutionAuthorizationGrant{},
		plan,
	)
	if err != nil {
		t.Fatalf("ExecuteCleanupTarget() = %v", err)
	}
	if observation.Raw.DeleteOutcome != ingest.CleanupDeleteObserved ||
		observation.Raw.AuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		observation.Manifest.DeleteOutcome != ingest.CleanupDeleteObserved ||
		observation.Manifest.AuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		ingest.ValidateCleanupExecutionObservationShape(plan, observation) != nil {
		t.Fatalf("execution observation = %#v", observation)
	}
	backend.assertDone(t)
}

func TestCleanupArtifactExecutorStopsBeforeManifestWhenRawCannotConverge(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidComplete, now)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
		inspectCall(*plan.Target.Command.Raw),
		deleteCall(*plan.Target.Command.Raw, ingest.ErrArtifactPermissionDenied),
	}}
	executor := cleanupExecutorTestInstance(backend, now)

	observation, err := executor.ExecuteCleanupTarget(
		context.Background(),
		firebaseadapter.CleanupExecutionAuthorizationGrant{},
		plan,
	)
	if !errors.Is(err, ingest.ErrArtifactPermissionDenied) || observation != (ingest.CleanupExecutionObservation{}) {
		t.Fatalf("ExecuteCleanupTarget() = %#v, %v", observation, err)
	}
	backend.assertDone(t)
}

func TestCleanupArtifactExecutorAuditsAmbiguousDeleteButReturnsRetryableError(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidComplete, now)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
		inspectCall(*plan.Target.Command.Raw),
		deleteCall(*plan.Target.Command.Raw, ingest.ErrArtifactProviderTimeout),
		inventoryCall(plan.ExpectedRawPath, emptyCleanupInventory()),
	}}
	executor := cleanupExecutorTestInstance(backend, now)

	observation, err := executor.ExecuteCleanupTarget(
		context.Background(),
		firebaseadapter.CleanupExecutionAuthorizationGrant{},
		plan,
	)
	if !errors.Is(err, ingest.ErrArtifactProviderTimeout) ||
		observation != (ingest.CleanupExecutionObservation{}) {
		t.Fatalf("ambiguous execution observation = %#v, %v", observation, err)
	}
	backend.assertDone(t)
}

func TestCleanupArtifactExecutorRejectsSoftDeletedTarget(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	alreadyGone := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, softDeletedCleanupInventory(*plan.Target.Command.Raw)),
	}}
	observation, err := cleanupExecutorTestInstance(alreadyGone, now).ExecuteCleanupTarget(
		context.Background(),
		firebaseadapter.CleanupExecutionAuthorizationGrant{},
		plan,
	)
	if !errors.Is(err, ingest.ErrCleanupExecutionGenerationDrift) ||
		observation != (ingest.CleanupExecutionObservation{}) {
		t.Fatalf("soft-deleted target observation = %#v, %v", observation, err)
	}
	alreadyGone.assertDone(t)
}

func TestCleanupArtifactExecutorManifestOnlyRequiresFreshRawEmptyAudit(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationManifestOnly, now)
	lateRaw := ingest.ArtifactLineage{
		Path:   plan.ExpectedRawPath,
		SHA256: strings.Repeat("d", 64), CRC32C: 10, Size: 12,
		Generation: 99, Metageneration: 1,
	}
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(lateRaw)),
	}}
	observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
		context.Background(),
		firebaseadapter.CleanupExecutionAuthorizationGrant{},
		plan,
	)
	if !errors.Is(err, ingest.ErrCleanupExecutionGenerationDrift) ||
		observation != (ingest.CleanupExecutionObservation{}) {
		t.Fatalf("late raw execution = %#v, %v", observation, err)
	}
	backend.assertDone(t)

	safe := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, emptyCleanupInventory()),
		inventoryCall(plan.ExpectedManifestPath, liveCleanupInventory(*plan.Target.Command.Manifest)),
		inspectCall(*plan.Target.Command.Manifest),
		deleteCall(*plan.Target.Command.Manifest, nil),
		inventoryCall(plan.ExpectedManifestPath, emptyCleanupInventory()),
	}}
	observation, err = cleanupExecutorTestInstance(safe, now).ExecuteCleanupTarget(
		context.Background(),
		firebaseadapter.CleanupExecutionAuthorizationGrant{},
		plan,
	)
	if err != nil || observation.Raw.DeleteOutcome != ingest.CleanupDeleteNotAttempted ||
		observation.Raw.AuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		observation.Manifest.DeleteOutcome != ingest.CleanupDeleteObserved ||
		observation.Manifest.AuditOutcome != ingest.CleanupAuditConfirmedAbsent {
		t.Fatalf("manifest-only execution = %#v, %v", observation, err)
	}
	safe.assertDone(t)
}

func TestCleanupArtifactExecutorRejectsUnexpectedGenerationAndUnauthorizedGrantBeforeMutation(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	unexpected := *plan.Target.Command.Raw
	unexpected.Generation++
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(unexpected)),
	}}
	observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
		context.Background(),
		firebaseadapter.CleanupExecutionAuthorizationGrant{},
		plan,
	)
	if !errors.Is(err, ingest.ErrCleanupExecutionGenerationDrift) ||
		observation != (ingest.CleanupExecutionObservation{}) {
		t.Fatalf("unexpected generation execution = %#v, %v", observation, err)
	}
	backend.assertDone(t)

	denied := &scriptedCleanupExecutionBackend{t: t}
	executor := cleanupExecutorTestInstance(denied, now)
	executor.validateAuthorization = func(
		firebaseadapter.CleanupExecutionAuthorizationGrant,
		ingest.CleanupExecutionPlan,
		time.Time,
	) error {
		return ingest.ErrCleanupExecutionUnauthorized
	}
	observation, err = executor.ExecuteCleanupTarget(
		context.Background(),
		firebaseadapter.CleanupExecutionAuthorizationGrant{},
		plan,
	)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) ||
		observation != (ingest.CleanupExecutionObservation{}) {
		t.Fatalf("unauthorized execution = %#v, %v", observation, err)
	}
	denied.assertDone(t)
}

func TestCleanupArtifactExecutorRawOnlyAuditsLateManifestAfterRawAbsence(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	lateManifest := ingest.ArtifactLineage{
		Path: plan.ExpectedManifestPath, SHA256: strings.Repeat("d", 64), CRC32C: 10, Size: 12,
		Generation: 99, Metageneration: 1,
	}
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
		inspectCall(*plan.Target.Command.Raw),
		deleteCall(*plan.Target.Command.Raw, nil),
		inventoryCall(plan.ExpectedRawPath, emptyCleanupInventory()),
		inventoryCall(plan.ExpectedManifestPath, liveCleanupInventory(lateManifest)),
	}}

	observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
		context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
	)
	if !errors.Is(err, ingest.ErrCleanupExecutionGenerationDrift) ||
		observation != (ingest.CleanupExecutionObservation{}) {
		t.Fatalf("late manifest execution = %#v, %v", observation, err)
	}
	backend.assertDone(t)
}

func TestCleanupArtifactExecutorRequiresEmptyPostDeleteAudit(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidComplete, now)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
		inspectCall(*plan.Target.Command.Raw),
		deleteCall(*plan.Target.Command.Raw, nil),
		inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
	}}

	observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
		context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
	)
	if !errors.Is(err, ingest.ErrCleanupExecutionGenerationDrift) ||
		observation != (ingest.CleanupExecutionObservation{}) {
		t.Fatalf("non-empty post-delete audit = %#v, %v", observation, err)
	}
	backend.assertDone(t)
}

func TestCleanupArtifactExecutorReauditsInspectNotFound(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)

	t.Run("empty", func(t *testing.T) {
		backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
			inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
			inspectErrorCall(*plan.Target.Command.Raw, ingest.ErrArtifactGenerationNotFound),
			inventoryCall(plan.ExpectedRawPath, emptyCleanupInventory()),
			inventoryCall(plan.ExpectedManifestPath, emptyCleanupInventory()),
		}}
		observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
			context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
		)
		if err != nil || observation.Raw.DeleteOutcome != ingest.CleanupDeleteNotAttempted ||
			observation.Raw.AuditOutcome != ingest.CleanupAuditConfirmedAbsent {
			t.Fatalf("inspect 404 empty audit = %#v, %v", observation, err)
		}
		backend.assertDone(t)
	})

	t.Run("late live generation", func(t *testing.T) {
		backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
			inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
			inspectErrorCall(*plan.Target.Command.Raw, ingest.ErrArtifactGenerationNotFound),
			inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
		}}
		observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
			context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
		)
		if !errors.Is(err, ingest.ErrCleanupExecutionGenerationDrift) ||
			observation != (ingest.CleanupExecutionObservation{}) {
			t.Fatalf("inspect 404 live audit = %#v, %v", observation, err)
		}
		backend.assertDone(t)
	})
}

func TestCleanupArtifactExecutorDistinguishesDeleteNotFound(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)

	t.Run("empty audit", func(t *testing.T) {
		backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
			inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
			inspectCall(*plan.Target.Command.Raw),
			deleteCall(*plan.Target.Command.Raw, ingest.ErrArtifactGenerationNotFound),
			inventoryCall(plan.ExpectedRawPath, emptyCleanupInventory()),
			inventoryCall(plan.ExpectedManifestPath, emptyCleanupInventory()),
		}}
		observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
			context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
		)
		if err != nil || observation.Raw.DeleteOutcome != ingest.CleanupDeleteNotFound ||
			observation.Raw.AuditOutcome != ingest.CleanupAuditConfirmedAbsent {
			t.Fatalf("delete 404 empty audit = %#v, %v", observation, err)
		}
		backend.assertDone(t)
	})

	t.Run("live audit", func(t *testing.T) {
		backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
			inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
			inspectCall(*plan.Target.Command.Raw),
			deleteCall(*plan.Target.Command.Raw, ingest.ErrArtifactGenerationNotFound),
			inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
		}}
		observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
			context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
		)
		if !errors.Is(err, ingest.ErrCleanupExecutionGenerationDrift) ||
			observation != (ingest.CleanupExecutionObservation{}) {
			t.Fatalf("delete 404 live audit = %#v, %v", observation, err)
		}
		backend.assertDone(t)
	})
}

func TestCleanupArtifactExecutorPreservesDeleteErrorTaxonomy(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidComplete, now)
	tests := []struct {
		name       string
		err        error
		auditAfter bool
	}{
		{name: "timeout", err: ingest.ErrArtifactProviderTimeout, auditAfter: true},
		{name: "provider cancellation", err: ingest.ErrArtifactProviderCancelled, auditAfter: true},
		{name: "unavailable", err: ingest.ErrArtifactProviderUnavailable, auditAfter: true},
		{name: "permission", err: ingest.ErrArtifactPermissionDenied},
		{name: "quota", err: ingest.ErrArtifactQuotaLimited},
		{name: "precondition", err: ingest.ErrArtifactPreconditionDrift},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := []cleanupExecutionCall{
				inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(*plan.Target.Command.Raw)),
				inspectCall(*plan.Target.Command.Raw),
				deleteCall(*plan.Target.Command.Raw, test.err),
			}
			if test.auditAfter {
				calls = append(calls, inventoryCall(plan.ExpectedRawPath, emptyCleanupInventory()))
			}
			backend := &scriptedCleanupExecutionBackend{t: t, calls: calls}
			observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
				context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
			)
			if !errors.Is(err, test.err) || observation != (ingest.CleanupExecutionObservation{}) {
				t.Fatalf("delete error = %#v, %v, want %v", observation, err, test.err)
			}
			backend.assertDone(t)
		})
	}
}

func TestCleanupArtifactExecutorRejectsIncompleteOrMalformedInventory(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	tests := []struct {
		name   string
		mutate func(*ingest.GenerationInventory)
	}{
		{name: "coverage incomplete", mutate: func(value *ingest.GenerationInventory) {
			value.Coverage = ingest.ArtifactInventoryCoverageIncomplete
		}},
		{name: "regular not performed", mutate: func(value *ingest.GenerationInventory) {
			value.NonSoftDeleted.Performed = false
		}},
		{name: "soft deleted not performed", mutate: func(value *ingest.GenerationInventory) {
			value.SoftDeleted.Performed = false
		}},
		{name: "regular truncated", mutate: func(value *ingest.GenerationInventory) {
			value.NonSoftDeleted.Truncated = true
		}},
		{name: "malformed digest", mutate: func(value *ingest.GenerationInventory) {
			value.NonSoftDeleted.Candidates[0].SHA256 = "provider-secret"
		}},
		{name: "wrong path", mutate: func(value *ingest.GenerationInventory) {
			value.NonSoftDeleted.Candidates[0].Path = plan.ExpectedManifestPath
		}},
		{name: "duplicate across sets", mutate: func(value *ingest.GenerationInventory) {
			duplicate := value.NonSoftDeleted.Candidates[0]
			duplicate.SoftDeleted = true
			value.SoftDeleted.Candidates = []ingest.ArtifactSnapshot{duplicate}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory := liveCleanupInventory(*plan.Target.Command.Raw)
			test.mutate(&inventory)
			backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
				inventoryCall(plan.ExpectedRawPath, inventory),
			}}
			observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
				context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
			)
			if !errors.Is(err, ingest.ErrCleanupExecutionUnavailable) ||
				observation != (ingest.CleanupExecutionObservation{}) {
				t.Fatalf("invalid inventory = %#v, %v", observation, err)
			}
			backend.assertDone(t)
		})
	}
}

func TestCleanupArtifactExecutorRejectsEveryLineageMismatch(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)
	tests := []struct {
		name   string
		mutate func(*ingest.ArtifactLineage)
	}{
		{name: "sha256", mutate: func(value *ingest.ArtifactLineage) { value.SHA256 = strings.Repeat("d", 64) }},
		{name: "crc32c", mutate: func(value *ingest.ArtifactLineage) { value.CRC32C++ }},
		{name: "size", mutate: func(value *ingest.ArtifactLineage) { value.Size++ }},
		{name: "generation", mutate: func(value *ingest.ArtifactLineage) { value.Generation++ }},
		{name: "metageneration", mutate: func(value *ingest.ArtifactLineage) { value.Metageneration++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unexpected := *plan.Target.Command.Raw
			test.mutate(&unexpected)
			backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
				inventoryCall(plan.ExpectedRawPath, liveCleanupInventory(unexpected)),
			}}
			observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
				context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
			)
			if !errors.Is(err, ingest.ErrCleanupExecutionGenerationDrift) ||
				observation != (ingest.CleanupExecutionObservation{}) {
				t.Fatalf("lineage mismatch = %#v, %v", observation, err)
			}
			backend.assertDone(t)
		})
	}
}

func TestCleanupArtifactExecutorHonorsCancellationAndAuthorizationBoundary(t *testing.T) {
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	plan := cleanupExecutorPlanFixture(t, ingest.ArtifactClassificationValidRawOnly, now)

	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		backend := &scriptedCleanupExecutionBackend{t: t}
		observation, err := cleanupExecutorTestInstance(backend, now).ExecuteCleanupTarget(
			ctx, firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
		)
		if !errors.Is(err, context.Canceled) || observation != (ingest.CleanupExecutionObservation{}) {
			t.Fatalf("cancelled execution = %#v, %v", observation, err)
		}
		backend.assertDone(t)
	})

	t.Run("expired authorization deadline", func(t *testing.T) {
		backend := &scriptedCleanupExecutionBackend{t: t}
		executor := cleanupExecutorTestInstance(backend, now)
		executor.authorizationDeadline = func(
			firebaseadapter.CleanupExecutionAuthorizationGrant,
			ingest.CleanupExecutionPlan,
		) (time.Time, error) {
			return time.Now().UTC().Add(-time.Second), nil
		}
		observation, err := executor.ExecuteCleanupTarget(
			context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
		)
		if !errors.Is(err, firebaseadapter.ErrCleanupExecutionAuthorizationExpired) ||
			observation != (ingest.CleanupExecutionObservation{}) {
			t.Fatalf("expired execution = %#v, %v", observation, err)
		}
		backend.assertDone(t)
	})

	t.Run("zero production grant", func(t *testing.T) {
		backend := &scriptedCleanupExecutionBackend{t: t}
		executor := cleanupExecutorTestInstance(backend, now)
		executor.validateAuthorization = firebaseadapter.ValidateCleanupExecutionAuthorization
		executor.authorizationDeadline = firebaseadapter.CleanupExecutionAuthorizationDeadline
		observation, err := executor.ExecuteCleanupTarget(
			context.Background(), firebaseadapter.CleanupExecutionAuthorizationGrant{}, plan,
		)
		if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) ||
			observation != (ingest.CleanupExecutionObservation{}) {
			t.Fatalf("zero grant execution = %#v, %v", observation, err)
		}
		backend.assertDone(t)
	})
}

type cleanupExecutionCallKind string

const (
	cleanupExecutionCallInventory cleanupExecutionCallKind = "inventory"
	cleanupExecutionCallInspect   cleanupExecutionCallKind = "inspect"
	cleanupExecutionCallDelete    cleanupExecutionCallKind = "delete"
)

type cleanupExecutionCall struct {
	kind           cleanupExecutionCallKind
	path           string
	generation     int64
	metageneration int64
	inventory      ingest.GenerationInventory
	snapshot       ingest.ArtifactSnapshot
	err            error
}

type scriptedCleanupExecutionBackend struct {
	t     *testing.T
	calls []cleanupExecutionCall
}

func (b *scriptedCleanupExecutionBackend) ListExactPathGenerations(
	_ context.Context,
	path string,
	limit int,
) (ingest.GenerationInventory, error) {
	call := b.next(cleanupExecutionCallInventory, path, 0, 0)
	if limit != cleanupExecutionInventoryLimit {
		b.t.Fatalf("inventory limit = %d", limit)
	}
	return call.inventory, call.err
}

func (b *scriptedCleanupExecutionBackend) InspectGeneration(
	_ context.Context,
	path string,
	generation int64,
) (ingest.ArtifactSnapshot, error) {
	call := b.next(cleanupExecutionCallInspect, path, generation, 0)
	return call.snapshot, call.err
}

func (b *scriptedCleanupExecutionBackend) DeleteGeneration(
	_ context.Context,
	path string,
	generation int64,
	metageneration int64,
) error {
	return b.next(cleanupExecutionCallDelete, path, generation, metageneration).err
}

func (b *scriptedCleanupExecutionBackend) next(
	kind cleanupExecutionCallKind,
	path string,
	generation int64,
	metageneration int64,
) cleanupExecutionCall {
	b.t.Helper()
	if len(b.calls) == 0 {
		b.t.Fatalf("unexpected %s call for %q", kind, path)
	}
	call := b.calls[0]
	b.calls = b.calls[1:]
	if call.kind != kind || call.path != path ||
		(generation != 0 && call.generation != generation) ||
		(metageneration != 0 && call.metageneration != metageneration) {
		b.t.Fatalf("call = %#v, got %s %q generation=%d metageneration=%d", call, kind, path, generation, metageneration)
	}
	return call
}

func (b *scriptedCleanupExecutionBackend) assertDone(t *testing.T) {
	t.Helper()
	if len(b.calls) != 0 {
		t.Fatalf("unconsumed cleanup calls = %#v", b.calls)
	}
}

func inventoryCall(path string, inventory ingest.GenerationInventory) cleanupExecutionCall {
	return cleanupExecutionCall{kind: cleanupExecutionCallInventory, path: path, inventory: inventory}
}

func inspectCall(lineage ingest.ArtifactLineage) cleanupExecutionCall {
	return cleanupExecutionCall{
		kind:       cleanupExecutionCallInspect,
		path:       lineage.Path,
		generation: lineage.Generation,
		snapshot:   cleanupSnapshot(lineage, false),
	}
}

func inspectErrorCall(lineage ingest.ArtifactLineage, err error) cleanupExecutionCall {
	call := inspectCall(lineage)
	call.snapshot = ingest.ArtifactSnapshot{}
	call.err = err
	return call
}

func deleteCall(lineage ingest.ArtifactLineage, err error) cleanupExecutionCall {
	return cleanupExecutionCall{
		kind:           cleanupExecutionCallDelete,
		path:           lineage.Path,
		generation:     lineage.Generation,
		metageneration: lineage.Metageneration,
		err:            err,
	}
}

func cleanupExecutorTestInstance(
	backend *scriptedCleanupExecutionBackend,
	now time.Time,
) *CleanupArtifactExecutor {
	// The zero grant below is accepted only by these injected orchestration
	// seams. Production executors always use the concrete firebaseadapter
	// validator and a zero grant reaches no provider call.
	return &CleanupArtifactExecutor{
		reader:  backend,
		deleter: backend,
		now:     func() time.Time { return now },
		validateAuthorization: func(
			firebaseadapter.CleanupExecutionAuthorizationGrant,
			ingest.CleanupExecutionPlan,
			time.Time,
		) error {
			return nil
		},
		authorizationDeadline: func(
			firebaseadapter.CleanupExecutionAuthorizationGrant,
			ingest.CleanupExecutionPlan,
		) (time.Time, error) {
			return now.Add(time.Minute), nil
		},
	}
}

func cleanupExecutorPlanFixture(
	t *testing.T,
	classification ingest.ArtifactClassification,
	now time.Time,
) ingest.CleanupExecutionPlan {
	t.Helper()
	complete := func(count int) ingest.ArtifactInventorySummary {
		return ingest.ArtifactInventorySummary{
			Performed:           true,
			NonSoftDeletedCount: count,
			Coverage:            ingest.ArtifactInventoryCoverageComplete,
		}
	}
	raw := &ingest.ArtifactLineage{
		Path:   "telemetry/tenant/raw.json.gz",
		SHA256: strings.Repeat("b", 64), CRC32C: 123, Size: 4096,
		Generation: 1700000000000001, Metageneration: 1,
	}
	manifest := &ingest.ArtifactLineage{
		Path:   "telemetry/tenant/manifest.json",
		SHA256: strings.Repeat("c", 64), CRC32C: 456, Size: 1024,
		Generation: 1700000000000002, Metageneration: 1,
	}
	command := ingest.CleanupTargetCommand{
		SchemaVersion:          ingest.CleanupTargetSchemaVersion,
		CleanupID:              "77777777-7777-4777-8777-777777777777",
		TenantID:               "11111111-1111-4111-8111-111111111111",
		ReceiptID:              "01982015-4400-7000-8000-000000000001",
		ReservationKey:         strings.Repeat("a", 64),
		AttemptID:              "77777777-7777-4777-8777-777777777777",
		Mode:                   ingest.CleanupModeReservationExpiry,
		OriginStatus:           ingest.ReceiptReserved,
		CleanupPolicyVersion:   ingest.CleanupTransitionPolicyV1,
		CleanupTransitionedAt:  now.Add(-15 * time.Minute),
		CleanupQuiescenceUntil: now.Add(-4 * time.Minute),
		ReceiptRevision:        5,
		FencingToken:           4,
		LeaseAcquiredAt:        now.Add(-3 * time.Minute),
		LeaseHeartbeatAt:       now.Add(-time.Minute),
		LeaseExpiresAt:         now.Add(2 * time.Minute),
		WorkerVersion:          ingest.CleanupWorkerVersion,
		Status:                 ingest.CleanupTargetStatusPlanned,
		Decision:               ingest.CleanupTargetDeleteCandidate,
		RetentionPhase:         ingest.ArtifactRetentionBeforeExpiry,
		ValidatorVersion:       ingest.TelemetryValidatorVersion,
		ClassifiedAt:           now.Add(-45 * time.Second),
		CreatedAt:              now.Add(-30 * time.Second),
	}
	switch classification {
	case ingest.ArtifactClassificationValidComplete:
		command.Classification = classification
		command.ReasonCode = ingest.ArtifactReasonManifestAndReferencedRawValid
		command.RawInventory = complete(1)
		command.ManifestInventory = complete(1)
		command.Raw = raw
		command.Manifest = manifest
	case ingest.ArtifactClassificationValidRawOnly:
		command.Classification = classification
		command.ReasonCode = ingest.ArtifactReasonRawValidManifestAbsent
		command.RawInventory = complete(1)
		command.ManifestInventory = complete(0)
		command.Raw = raw
	case ingest.ArtifactClassificationManifestOnly:
		command.Classification = classification
		command.ReasonCode = ingest.ArtifactReasonReferencedRawNotFound
		command.RawInventory = complete(0)
		command.ManifestInventory = complete(1)
		command.Manifest = manifest
	default:
		t.Fatalf("unsupported classification %q", classification)
	}
	targetHash, err := ingest.CleanupTargetHash(command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	plan := ingest.CleanupExecutionPlan{
		Target:               ingest.CleanupTarget{Command: command, TargetHash: targetHash},
		ExpectedRawPath:      raw.Path,
		ExpectedManifestPath: manifest.Path,
	}
	if _, err := ingest.CloneCleanupExecutionPlan(plan); err != nil {
		t.Fatalf("CloneCleanupExecutionPlan() = %v", err)
	}
	return plan
}

func emptyCleanupInventory() ingest.GenerationInventory {
	return ingest.GenerationInventory{
		Coverage:       ingest.ArtifactInventoryCoverageComplete,
		NonSoftDeleted: ingest.ArtifactGenerationSet{Performed: true},
		SoftDeleted:    ingest.ArtifactGenerationSet{Performed: true},
	}
}

func liveCleanupInventory(lineage ingest.ArtifactLineage) ingest.GenerationInventory {
	inventory := emptyCleanupInventory()
	inventory.NonSoftDeleted.Candidates = []ingest.ArtifactSnapshot{cleanupSnapshot(lineage, false)}
	return inventory
}

func softDeletedCleanupInventory(lineage ingest.ArtifactLineage) ingest.GenerationInventory {
	inventory := emptyCleanupInventory()
	inventory.SoftDeleted.Candidates = []ingest.ArtifactSnapshot{cleanupSnapshot(lineage, true)}
	return inventory
}

func cleanupSnapshot(lineage ingest.ArtifactLineage, softDeleted bool) ingest.ArtifactSnapshot {
	return ingest.ArtifactSnapshot{
		Path:           lineage.Path,
		SHA256:         lineage.SHA256,
		CRC32C:         lineage.CRC32C,
		Size:           lineage.Size,
		Generation:     lineage.Generation,
		Metageneration: lineage.Metageneration,
		SoftDeleted:    softDeleted,
	}
}
