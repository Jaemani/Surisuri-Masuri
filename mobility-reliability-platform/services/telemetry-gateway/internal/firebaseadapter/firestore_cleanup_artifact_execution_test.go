package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreCleanupArtifactExecutionAppliesDispatchOnceAndReturnsWinnerGrant(t *testing.T) {
	fixture := newCleanupArtifactExecutionStoreFixture(t)
	fixture.seedLedger(fixture.ledger)

	request, grant, ledger, status, err := fixture.store.BeginCleanupArtifactExecution(
		context.Background(), fixture.query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhaseRawDispatchRecorded || ledger.Revision != 2 {
		t.Fatalf("BeginCleanupArtifactExecution() = %#v, %#v, %#v, %q, %v", request, grant, ledger, status, err)
	}
	if err := ValidateCleanupArtifactExecutionAuthorization(
		grant, request, fixture.store.now().UTC(),
	); err != nil {
		t.Fatalf("winner grant = %v", err)
	}
	if len(fixture.transaction.updates) != 1 ||
		fixture.transaction.updates[0].path != fixture.attemptPath {
		t.Fatalf("dispatch updates = %#v", fixture.transaction.updates)
	}

	fixture.seedLedger(ledger)
	fixture.transaction.updates = nil
	replayedRequest, replayedGrant, replayedLedger, status, err := fixture.store.BeginCleanupArtifactExecution(
		context.Background(), fixture.query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationReplayed ||
		!reflect.DeepEqual(replayedRequest, request) || !reflect.DeepEqual(replayedLedger, ledger) {
		t.Fatalf("dispatch replay = %#v, %#v, %#v, %q, %v", replayedRequest, replayedGrant, replayedLedger, status, err)
	}
	if err := ValidateCleanupArtifactExecutionAuthorization(
		replayedGrant, replayedRequest, fixture.store.now().UTC(),
	); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		t.Fatalf("replayed zero grant error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestCleanupArtifactExecutionGrantBindsRequestPhaseRevisionAndArtifact(t *testing.T) {
	fixture := newCleanupArtifactExecutionStoreFixture(t)
	fixture.seedLedger(fixture.ledger)
	request, grant, _, status, err := fixture.store.BeginCleanupArtifactExecution(
		context.Background(), fixture.query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("BeginCleanupArtifactExecution() = %q, %v", status, err)
	}

	tests := []struct {
		name   string
		mutate func(*CleanupArtifactExecutionAuthorizationGrant)
	}{
		{name: "request", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) {
			value.requestHash = differentCleanupLedgerDigest(value.requestHash)
		}},
		{name: "target", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) {
			value.targetHash = differentCleanupLedgerDigest(value.targetHash)
		}},
		{name: "plan", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) {
			value.planHash = differentCleanupLedgerDigest(value.planHash)
		}},
		{name: "receipt", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) { value.receiptRevision++ }},
		{name: "fence", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) { value.fencingToken++ }},
		{name: "revision", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) { value.dispatchRevision++ }},
		{name: "phase", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) {
			value.dispatchPhase = ingest.CleanupExecutionPhaseManifestDispatchRecorded
		}},
		{name: "artifact", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) {
			value.artifact = ingest.CleanupArtifactExecutionManifest
		}},
		{name: "targeted", mutate: func(value *CleanupArtifactExecutionAuthorizationGrant) { value.targeted = !value.targeted }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := grant
			test.mutate(&mutated)
			mutated.capabilitySeal = cleanupArtifactExecutionCapabilitySeal(mutated)
			if err := ValidateCleanupArtifactExecutionAuthorization(
				mutated, request, fixture.store.now().UTC(),
			); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
				t.Fatalf("mutated grant error = %v", err)
			}
		})
	}
	if _, err := CleanupArtifactExecutionAuthorizationDeadline(grant, request); err != nil {
		t.Fatalf("authorization deadline = %v", err)
	}
}

func TestCleanupArtifactExecutionAuthorizationSeparatesMutationAndOutcomeExpiry(t *testing.T) {
	fixture := newCleanupArtifactExecutionStoreFixture(t)
	fixture.seedLedger(fixture.ledger)
	request, grant, _, status, err := fixture.store.BeginCleanupArtifactExecution(
		context.Background(), fixture.query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("BeginCleanupArtifactExecution() = %q, %v", status, err)
	}
	if !grant.checkedAt.Before(grant.mutationExpiresAt) ||
		!grant.mutationExpiresAt.Before(grant.outcomeExpiresAt) ||
		grant.outcomeExpiresAt.After(grant.leaseExpiresAt) ||
		grant.outcomeExpiresAt.Sub(grant.mutationExpiresAt) !=
			CleanupArtifactExecutionOutcomePersistenceGrace {
		t.Fatalf("authorization window = %#v", grant)
	}
	if err := ValidateCleanupArtifactExecutionAuthorization(
		grant, request, grant.mutationExpiresAt.Add(-time.Nanosecond),
	); err != nil {
		t.Fatalf("last mutation instant = %v", err)
	}
	if err := ValidateCleanupArtifactExecutionAuthorization(
		grant, request, grant.mutationExpiresAt,
	); !errors.Is(err, ErrCleanupArtifactExecutionAuthorizationExpired) {
		t.Fatalf("exact mutation expiry error = %v", err)
	}
	result := ingest.CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision:  request.DispatchRevision,
		DeleteOutcome:     ingest.CleanupDeleteUnknown,
		ErrorClass:        ingest.CleanupExecutionErrorProviderTimeout,
		MutationStartedAt: grant.mutationExpiresAt.Add(-time.Nanosecond),
		ObservedAt:        grant.outcomeExpiresAt.Add(-time.Nanosecond),
	}
	if err := ValidateCleanupArtifactExecutionOutcomeAuthorization(
		grant, request, result,
	); err != nil {
		t.Fatalf("last outcome instant = %v", err)
	}
	result.ObservedAt = grant.outcomeExpiresAt
	if err := ValidateCleanupArtifactExecutionOutcomeAuthorization(
		grant, request, result,
	); !errors.Is(err, ErrCleanupArtifactExecutionAuthorizationExpired) {
		t.Fatalf("exact outcome expiry error = %v", err)
	}
	mutationDeadline, err := CleanupArtifactExecutionAuthorizationDeadline(grant, request)
	if err != nil || !mutationDeadline.Equal(grant.mutationExpiresAt) {
		t.Fatalf("mutation deadline = %s, %v", mutationDeadline, err)
	}
	outcomeDeadline, err := CleanupArtifactExecutionOutcomePersistenceDeadline(grant, request)
	if err != nil || !outcomeDeadline.Equal(grant.outcomeExpiresAt) {
		t.Fatalf("outcome deadline = %s, %v", outcomeDeadline, err)
	}
	tampered := grant
	tampered.outcomeExpiresAt = tampered.outcomeExpiresAt.Add(time.Nanosecond)
	tampered.capabilitySeal = cleanupArtifactExecutionCapabilitySeal(tampered)
	if err := ValidateCleanupArtifactExecutionAuthorization(
		tampered, request, tampered.checkedAt,
	); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		t.Fatalf("tampered deadline error = %v", err)
	}
}

func TestFirestoreCleanupArtifactExecutionPersistsUnknownAndReplaysExactly(t *testing.T) {
	fixture := newCleanupArtifactExecutionStoreFixture(t)
	fixture.seedLedger(fixture.ledger)
	request, grant, dispatched, status, err := fixture.store.BeginCleanupArtifactExecution(
		context.Background(), fixture.query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("BeginCleanupArtifactExecution() = %q, %v", status, err)
	}
	fixture.seedLedger(dispatched)
	fixture.transaction.updates = nil
	result := ingest.CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision: request.DispatchRevision, DeleteOutcome: ingest.CleanupDeleteUnknown,
		ErrorClass:        ingest.CleanupExecutionErrorProviderTimeout,
		MutationStartedAt: fixture.store.now().UTC(),
		ObservedAt:        fixture.store.now().UTC(),
	}
	ledger, status, err := fixture.store.RecordCleanupArtifactExecutionOutcome(
		context.Background(), grant, request, result,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhaseRawOutcomeRecorded ||
		ledger.Raw.DeleteOutcome != ingest.CleanupDeleteUnknown ||
		ledger.ErrorClass != ingest.CleanupExecutionErrorProviderTimeout {
		t.Fatalf("RecordCleanupArtifactExecutionOutcome() = %#v, %q, %v", ledger, status, err)
	}
	if len(fixture.transaction.updates) != 1 {
		t.Fatalf("outcome updates = %#v", fixture.transaction.updates)
	}

	fixture.seedLedger(ledger)
	fixture.transaction.updates = nil
	replayed, status, err := fixture.store.RecordCleanupArtifactExecutionOutcome(
		context.Background(), grant, request, result,
	)
	if err != nil || status != ingest.CleanupExecutionMutationReplayed ||
		!reflect.DeepEqual(replayed, ledger) {
		t.Fatalf("unknown outcome replay = %#v, %q, %v", replayed, status, err)
	}
	fixture.assertNoWrites(t)

	differentClass := result
	differentClass.ErrorClass = ingest.CleanupExecutionErrorProviderUnavailable
	if _, _, err := fixture.store.RecordCleanupArtifactExecutionOutcome(
		context.Background(), grant, request, differentClass,
	); !errors.Is(err, ingest.ErrCleanupExecutionConflict) {
		t.Fatalf("different error-class replay error = %v", err)
	}
	fixture.assertNoWrites(t)

	if _, _, err := fixture.store.RecordCleanupArtifactExecutionOutcome(
		context.Background(), grant, request, ingest.CleanupArtifactExecutionResult{
			RequestHash: request.RequestHash, Artifact: request.Artifact,
			DispatchRevision: request.DispatchRevision, DeleteOutcome: ingest.CleanupDeleteObserved,
			MutationStartedAt: fixture.store.now().UTC(),
			ObservedAt:        fixture.store.now().UTC(),
		},
	); !errors.Is(err, ingest.ErrCleanupExecutionConflict) &&
		!errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		t.Fatalf("different outcome replay error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupArtifactExecutionPersistsUnknownWithinOutcomeGrace(t *testing.T) {
	fixture := newCleanupArtifactExecutionStoreFixture(t)
	fixture.seedLedger(fixture.ledger)
	request, grant, dispatched, status, err := fixture.store.BeginCleanupArtifactExecution(
		context.Background(), fixture.query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("BeginCleanupArtifactExecution() = %q, %v", status, err)
	}
	fixture.seedLedger(dispatched)
	fixture.transaction.updates = nil
	result := ingest.CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision:  request.DispatchRevision,
		DeleteOutcome:     ingest.CleanupDeleteUnknown,
		ErrorClass:        ingest.CleanupExecutionErrorProviderTimeout,
		MutationStartedAt: grant.mutationExpiresAt.Add(-time.Millisecond),
		ObservedAt:        grant.mutationExpiresAt.Add(time.Millisecond),
	}
	advanceCleanupArtifactExecutionFixtureTime(
		t, fixture, result.ObservedAt.Add(time.Millisecond),
	)
	ledger, status, err := fixture.store.RecordCleanupArtifactExecutionOutcome(
		context.Background(), grant, request, result,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhaseRawOutcomeRecorded ||
		ledger.Raw.DeleteOutcome != ingest.CleanupDeleteUnknown {
		t.Fatalf("grace outcome = %#v, %q, %v", ledger, status, err)
	}
}

func TestFirestoreCleanupArtifactExecutionRejectsOutcomeAtGraceExpiryWithoutWrite(t *testing.T) {
	fixture := newCleanupArtifactExecutionStoreFixture(t)
	fixture.seedLedger(fixture.ledger)
	request, grant, dispatched, status, err := fixture.store.BeginCleanupArtifactExecution(
		context.Background(), fixture.query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("BeginCleanupArtifactExecution() = %q, %v", status, err)
	}
	fixture.seedLedger(dispatched)
	fixture.transaction.updates = nil
	result := ingest.CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision:  request.DispatchRevision,
		DeleteOutcome:     ingest.CleanupDeleteUnknown,
		ErrorClass:        ingest.CleanupExecutionErrorProviderTimeout,
		MutationStartedAt: grant.mutationExpiresAt.Add(-time.Millisecond),
		ObservedAt:        grant.outcomeExpiresAt.Add(-time.Millisecond),
	}
	advanceCleanupArtifactExecutionFixtureTime(t, fixture, grant.outcomeExpiresAt)
	if _, _, err := fixture.store.RecordCleanupArtifactExecutionOutcome(
		context.Background(), grant, request, result,
	); !errors.Is(err, ErrCleanupArtifactExecutionAuthorizationExpired) {
		t.Fatalf("exact grace expiry error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupArtifactExecutionRejectsManifestBeforeRawAbsence(t *testing.T) {
	fixture := newCleanupArtifactExecutionStoreFixture(t)
	fixture.seedLedger(fixture.ledger)
	if _, _, _, _, err := fixture.store.BeginCleanupArtifactExecution(
		context.Background(), fixture.query, ingest.CleanupArtifactExecutionManifest,
	); !errors.Is(err, ingest.ErrCleanupExecutionConflict) {
		t.Fatalf("early manifest dispatch error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func advanceCleanupArtifactExecutionFixtureTime(
	t *testing.T,
	fixture *cleanupExecutionLedgerStoreFixture,
	observedAt time.Time,
) {
	t.Helper()
	observedAt = observedAt.UTC()
	fixture.store.now = func() time.Time { return observedAt }
	fixture.transaction.readTime = observedAt
	targetPath := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
	target, exists := fixture.transaction.targets[targetPath]
	if !exists {
		t.Fatalf("missing cleanup target %q", targetPath)
	}
	target.ReadTime = observedAt
	fixture.transaction.targets[targetPath] = target
}

func newCleanupArtifactExecutionStoreFixture(
	t *testing.T,
) *cleanupExecutionLedgerStoreFixture {
	t.Helper()
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	fixture.store.cleanupArtifactExecutionContext = func(
		parent context.Context,
		_ time.Time,
	) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}
	return fixture
}
