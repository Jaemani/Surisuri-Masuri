package ingest

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBuildCleanupExecutionPlanBindsCurrentPersistedDeleteTarget(t *testing.T) {
	now, snapshot := cleanupExecutionFixture(t, ArtifactClassificationValidComplete)
	query := CleanupExecutionQuery{
		TenantID:       cleanupTargetTenantID,
		ReservationKey: snapshot.Receipt.ReservationKey,
		AttemptID:      cleanupTargetAttemptID,
	}
	plan, err := BuildCleanupExecutionPlan(query, snapshot, now)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionPlan() = %v", err)
	}
	if plan.Target.TargetHash != snapshot.Target.TargetHash || plan.ExpectedRawPath == "" ||
		plan.ExpectedManifestPath == "" || ValidateCleanupExecutionPlan(plan) != nil {
		t.Fatalf("execution plan = %#v", plan)
	}
	planHash, err := CleanupExecutionPlanHash(plan)
	if err != nil || !isLowerHexDigest(planHash) {
		t.Fatalf("CleanupExecutionPlanHash() = %q, %v", planHash, err)
	}

	tampered, err := CloneCleanupExecutionPlan(plan)
	if err != nil {
		t.Fatalf("CloneCleanupExecutionPlan() = %v", err)
	}
	tampered.ExpectedRawPath = tampered.ExpectedManifestPath
	if !errors.Is(ValidateCleanupExecutionPlan(tampered), ErrCleanupExecutionUnauthorized) {
		t.Fatal("plan accepted a substituted expected path")
	}
}

func TestBuildCleanupExecutionPlanRejectsNonDeleteAndStaleCurrentState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CurrentCleanupExecutionSnapshot)
	}{
		{
			name: "verified empty target",
			mutate: func(snapshot *CurrentCleanupExecutionSnapshot) {
				_, empty := cleanupExecutionFixture(t, ArtifactClassificationNone)
				snapshot.Target = empty.Target
			},
		},
		{
			name: "hold target",
			mutate: func(snapshot *CurrentCleanupExecutionSnapshot) {
				_, hold := cleanupExecutionFixture(t, ArtifactClassificationGenerationDrift)
				snapshot.Target = hold.Target
			},
		},
		{
			name: "stale receipt revision",
			mutate: func(snapshot *CurrentCleanupExecutionSnapshot) {
				snapshot.Receipt.Revision++
			},
		},
		{
			name: "completed attempt",
			mutate: func(snapshot *CurrentCleanupExecutionSnapshot) {
				snapshot.Attempt.Status = RecoveryAttemptCompleted
			},
		},
		{
			name: "foreign target hash",
			mutate: func(snapshot *CurrentCleanupExecutionSnapshot) {
				snapshot.Target.TargetHash = differentCleanupExecutionDigest(snapshot.Target.TargetHash)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now, snapshot := cleanupExecutionFixture(t, ArtifactClassificationValidComplete)
			test.mutate(&snapshot)
			plan, err := BuildCleanupExecutionPlan(CleanupExecutionQuery{
				TenantID:       cleanupTargetTenantID,
				ReservationKey: snapshot.Receipt.ReservationKey,
				AttemptID:      cleanupTargetAttemptID,
			}, snapshot, now)
			if !errors.Is(err, ErrCleanupExecutionUnauthorized) || plan != (CleanupExecutionPlan{}) {
				t.Fatalf("BuildCleanupExecutionPlan() = %#v, %v", plan, err)
			}
		})
	}
}

func TestCleanupExecutionObservationIsNonAuthoritativeButShapeChecked(t *testing.T) {
	now, snapshot := cleanupExecutionFixture(t, ArtifactClassificationManifestOnly)
	plan, err := BuildCleanupExecutionPlan(CleanupExecutionQuery{
		TenantID:       cleanupTargetTenantID,
		ReservationKey: snapshot.Receipt.ReservationKey,
		AttemptID:      cleanupTargetAttemptID,
	}, snapshot, now)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionPlan() = %v", err)
	}
	planHash, err := CleanupExecutionPlanHash(plan)
	if err != nil {
		t.Fatalf("CleanupExecutionPlanHash() = %v", err)
	}
	observation := CleanupExecutionObservation{
		PlanHash:   planHash,
		TargetHash: plan.Target.TargetHash,
		Raw: CleanupArtifactExecutionObservation{
			DeleteOutcome: CleanupDeleteNotAttempted,
			AuditOutcome:  CleanupAuditConfirmedAbsent,
		},
		Manifest: CleanupArtifactExecutionObservation{
			DeleteOutcome: CleanupDeleteObserved,
			AuditOutcome:  CleanupAuditConfirmedAbsent,
		},
		CompletedAt: now.Add(time.Second),
	}
	if err := ValidateCleanupExecutionObservationShape(plan, observation); err != nil {
		t.Fatalf("ValidateCleanupExecutionObservationShape() = %v", err)
	}

	mutations := []func(*CleanupExecutionObservation){
		func(value *CleanupExecutionObservation) {
			value.PlanHash = differentCleanupExecutionDigest(value.PlanHash)
		},
		func(value *CleanupExecutionObservation) {
			value.TargetHash = differentCleanupExecutionDigest(value.TargetHash)
		},
		func(value *CleanupExecutionObservation) { value.Raw.AuditOutcome = CleanupAuditOutcome("soft_deleted") },
		func(value *CleanupExecutionObservation) { value.Raw.DeleteOutcome = CleanupDeleteObserved },
		func(value *CleanupExecutionObservation) { value.Manifest.DeleteOutcome = CleanupDeleteUnknown },
	}
	for index, mutate := range mutations {
		tampered := observation
		mutate(&tampered)
		if !errors.Is(
			ValidateCleanupExecutionObservationShape(plan, tampered),
			ErrInvalidCleanupExecutionObservation,
		) {
			t.Fatalf("mutation %d preserved a valid observation", index)
		}
	}
}

func cleanupExecutionFixture(
	t *testing.T,
	classification ArtifactClassification,
) (time.Time, CurrentCleanupExecutionSnapshot) {
	t.Helper()
	baseNow, current, lease, attempt := cleanupTargetFixture(t)
	cleanupAuthorizer := mustCleanupAuthorizer(
		t,
		&cleanupAuthorizationStoreStub{snapshot: current},
		baseNow,
	)
	request, _, err := cleanupAuthorizer.AuthorizeArtifactRead(
		context.Background(),
		cleanupTargetTenantID,
		current.Receipt.ReservationKey,
		lease,
	)
	if err != nil {
		t.Fatalf("AuthorizeArtifactRead() = %v", err)
	}
	classificationResult := cleanupClassificationResultFixture(
		request,
		classification,
		baseNow.Add(-time.Second),
	)
	command, _, err := cleanupAuthorizer.AuthorizeTargetCreation(
		context.Background(),
		cleanupTargetTenantID,
		current.Receipt.ReservationKey,
		lease,
		attempt,
		request,
		classificationResult,
	)
	if err != nil {
		t.Fatalf("AuthorizeTargetCreation() = %v", err)
	}
	targetHash, err := CleanupTargetHash(command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	now := baseNow.Add(time.Second)
	return now, CurrentCleanupExecutionSnapshot{
		Receipt:  current.Receipt,
		Attempt:  current.Attempt,
		Target:   CleanupTarget{Command: command, TargetHash: targetHash},
		ReadTime: now.Add(-time.Second),
	}
}

func differentCleanupExecutionDigest(value string) string {
	if value[0] == 'f' {
		return "e" + value[1:]
	}
	return "f" + value[1:]
}
