package ingest

import (
	"errors"
	"testing"
	"time"
)

func TestCleanupExecutionLedgerPlanBindsImmutableTargetAndExpectedPaths(t *testing.T) {
	for _, classification := range []ArtifactClassification{
		ArtifactClassificationValidComplete,
		ArtifactClassificationNone,
	} {
		now, snapshot := cleanupExecutionFixture(t, classification)
		plan, err := BuildCleanupExecutionLedgerPlan(
			cleanupExecutionQueryFixture(snapshot),
			snapshot,
			now,
		)
		if err != nil || ValidateCleanupExecutionLedgerPlan(plan) != nil {
			t.Fatalf("BuildCleanupExecutionLedgerPlan(%q) = %#v, %v", classification, plan, err)
		}
		if !isLowerHexDigest(plan.PlanHash) {
			t.Fatalf("plan hash = %q", plan.PlanHash)
		}

		tampered := plan
		tampered.ExpectedRawPath += ".different"
		if !errors.Is(ValidateCleanupExecutionLedgerPlan(tampered), ErrInvalidCleanupExecutionLedger) {
			t.Fatal("changed expected raw path preserved a valid plan")
		}
		tampered = plan
		tampered.PlanHash = differentCleanupExecutionDigest(plan.PlanHash)
		if !errors.Is(ValidateCleanupExecutionLedgerPlan(tampered), ErrInvalidCleanupExecutionLedger) {
			t.Fatal("changed plan hash preserved a valid plan")
		}
	}

	holdNow, holdSnapshot := cleanupExecutionFixture(t, ArtifactClassificationGenerationDrift)
	if _, err := BuildCleanupExecutionLedgerPlan(
		cleanupExecutionQueryFixture(holdSnapshot),
		holdSnapshot,
		holdNow,
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("hold target plan error = %v", err)
	}
}

func TestCleanupExecutionLedgerPlanRequiresFreshCurrentSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CurrentCleanupExecutionSnapshot)
	}{
		{
			name: "stale receipt revision",
			mutate: func(value *CurrentCleanupExecutionSnapshot) {
				value.Receipt.Revision++
			},
		},
		{
			name: "completed attempt",
			mutate: func(value *CurrentCleanupExecutionSnapshot) {
				value.Attempt.Status = RecoveryAttemptCompleted
			},
		},
		{
			name: "fence drift",
			mutate: func(value *CurrentCleanupExecutionSnapshot) {
				value.Receipt.FencingToken++
			},
		},
		{
			name: "foreign target hash",
			mutate: func(value *CurrentCleanupExecutionSnapshot) {
				value.Target.TargetHash = differentCleanupExecutionDigest(value.Target.TargetHash)
			},
		},
		{
			name: "stale read",
			mutate: func(value *CurrentCleanupExecutionSnapshot) {
				value.ReadTime = value.ReadTime.Add(-MaxForwardRecoveryAuthorizationClockSkew - time.Nanosecond)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now, snapshot := cleanupExecutionFixture(t, ArtifactClassificationNone)
			query := cleanupExecutionQueryFixture(snapshot)
			test.mutate(&snapshot)
			if _, err := BuildCleanupExecutionLedgerPlan(query, snapshot, now); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
				t.Fatalf("BuildCleanupExecutionLedgerPlan() error = %v", err)
			}
		})
	}
}

func TestCleanupExecutionLedgerPlanRejectsReadAtExactFenceExpiry(t *testing.T) {
	_, snapshot := cleanupExecutionFixture(t, ArtifactClassificationNone)
	snapshot.ReadTime = snapshot.Receipt.LeaseExpiresAt
	if _, err := BuildCleanupExecutionLedgerPlan(
		cleanupExecutionQueryFixture(snapshot),
		snapshot,
		snapshot.Receipt.LeaseExpiresAt.Add(-time.Nanosecond),
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() error = %v", err)
	}
}

func TestCleanupExecutionLedgerAdvancesRawBeforeManifestAndCompletes(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	if !ledger.Raw.Targeted || !ledger.Manifest.Targeted || ledger.Revision != 1 {
		t.Fatalf("initial ledger = %#v", ledger)
	}

	steps := []CleanupExecutionTransition{
		{Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second)},
		{Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteObserved, ObservedAt: now.Add(2 * time.Second)},
		{Phase: CleanupExecutionPhaseRawAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: now.Add(4 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestOutcomeRecorded, DeleteOutcome: CleanupDeleteNotFound, ObservedAt: now.Add(5 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(6 * time.Second)},
		{Phase: CleanupExecutionPhaseCompleted, ObservedAt: now.Add(7 * time.Second)},
	}
	for index, step := range steps {
		ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, step)
		if err != nil {
			t.Fatalf("step %d (%q) = %v", index, step.Phase, err)
		}
	}
	if ledger.Phase != CleanupExecutionPhaseCompleted || ledger.Revision != 8 ||
		ledger.Disposition != CleanupExecutionDispositionComplete || !isLowerHexDigest(ledger.EvidenceHash) {
		t.Fatalf("completed ledger = %#v", ledger)
	}
	evidenceHash, err := CleanupExecutionEvidenceHash(plan, ledger)
	if err != nil || evidenceHash != ledger.EvidenceHash {
		t.Fatalf("CleanupExecutionEvidenceHash() = %q, %v", evidenceHash, err)
	}
}

func TestCleanupExecutionLedgerSupportsVerifiedEmptyWithReadOnlyAudit(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationNone)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	if ledger.Raw.Targeted || ledger.Manifest.Targeted {
		t.Fatalf("verified-empty targeted flags = raw:%t manifest:%t", ledger.Raw.Targeted, ledger.Manifest.Targeted)
	}
	steps := []CleanupExecutionTransition{
		{Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second)},
		{Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteNotAttempted, ObservedAt: now.Add(2 * time.Second)},
		{Phase: CleanupExecutionPhaseRawAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: now.Add(4 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestOutcomeRecorded, DeleteOutcome: CleanupDeleteNotAttempted, ObservedAt: now.Add(5 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(6 * time.Second)},
		{Phase: CleanupExecutionPhaseCompleted, ObservedAt: now.Add(7 * time.Second)},
	}
	for _, step := range steps {
		ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, step)
		if err != nil {
			t.Fatalf("AdvanceCleanupExecutionLedger(%q) = %v", step.Phase, err)
		}
	}
}

func TestCleanupExecutionLedgerRejectsSkippedConflictingAndUnknownProgress(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	initial, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}

	if _, err := AdvanceCleanupExecutionLedger(plan, initial, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteObserved,
		ObservedAt: now.Add(time.Second),
	}); !errors.Is(err, ErrCleanupExecutionConflict) {
		t.Fatalf("skipped dispatch error = %v", err)
	}

	verifiedNow, verifiedPlan := cleanupLedgerPlanFixture(t, ArtifactClassificationNone)
	verified, err := NewCleanupExecutionLedger(verifiedPlan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger(verified-empty) = %v", err)
	}
	verified, err = AdvanceCleanupExecutionLedger(verifiedPlan, verified, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: verifiedNow.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("verified raw dispatch = %v", err)
	}
	if _, err := AdvanceCleanupExecutionLedger(verifiedPlan, verified, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteObserved,
		ObservedAt: verifiedNow.Add(2 * time.Second),
	}); !errors.Is(err, ErrCleanupExecutionConflict) {
		t.Fatalf("non-targeted delete outcome error = %v", err)
	}

	unknown := initial
	for _, step := range []CleanupExecutionTransition{
		{Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second)},
		{Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteUnknown, ObservedAt: now.Add(2 * time.Second)},
		{Phase: CleanupExecutionPhaseRawAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second)},
	} {
		unknown, err = AdvanceCleanupExecutionLedger(plan, unknown, step)
		if err != nil {
			t.Fatalf("unknown raw step %q = %v", step.Phase, err)
		}
	}
	if _, err := AdvanceCleanupExecutionLedger(plan, unknown, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: now.Add(4 * time.Second),
	}); !errors.Is(err, ErrCleanupExecutionConflict) {
		t.Fatalf("unknown raw outcome advanced to manifest: %v", err)
	}
}

func TestCleanupExecutionLedgerRejectsTamperedDurableFields(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	mutations := []func(*CleanupExecutionLedger){
		func(value *CleanupExecutionLedger) { value.SchemaVersion = "other" },
		func(value *CleanupExecutionLedger) {
			value.DecisionDomain = ForwardRecoveryDecisionCurrentAuthorization
		},
		func(value *CleanupExecutionLedger) {
			value.TargetHash = differentCleanupExecutionDigest(value.TargetHash)
		},
		func(value *CleanupExecutionLedger) { value.PlanHash = differentCleanupExecutionDigest(value.PlanHash) },
		func(value *CleanupExecutionLedger) { value.ReceiptRevision++ },
		func(value *CleanupExecutionLedger) { value.Fence.Token++ },
		func(value *CleanupExecutionLedger) { value.Revision++ },
		func(value *CleanupExecutionLedger) { value.Raw.Targeted = false },
		func(value *CleanupExecutionLedger) { value.Disposition = CleanupExecutionDispositionComplete },
	}
	for index, mutate := range mutations {
		tampered := ledger
		mutate(&tampered)
		if !errors.Is(
			ValidateCleanupExecutionLedger(plan, tampered, now),
			ErrInvalidCleanupExecutionLedger,
		) {
			t.Fatalf("tamper %d preserved a valid ledger: %#v", index, tampered)
		}
	}
	if !errors.Is(
		ValidateCleanupExecutionLedger(plan, ledger, plan.Target.Command.CreatedAt.Add(-time.Nanosecond)),
		ErrInvalidCleanupExecutionLedger,
	) {
		t.Fatal("observation before target creation remained valid")
	}
	if !errors.Is(
		ValidateCleanupExecutionLedger(plan, ledger, ledger.Fence.ExpiresAt),
		ErrInvalidCleanupExecutionLedger,
	) {
		t.Fatal("observation at exact lease expiry remained valid")
	}
}

func TestCleanupPurgeEligibleAtUsesLaterControlFloor(t *testing.T) {
	completedAt := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	auditFloor := completedAt.Add(CleanupCompletionAuditWindow)

	got, err := CleanupPurgeEligibleAt(completedAt.Add(30*24*time.Hour), completedAt)
	if err != nil || !got.Equal(completedAt.Add(30*24*time.Hour)) {
		t.Fatalf("retention floor result = %v, %v", got, err)
	}
	got, err = CleanupPurgeEligibleAt(completedAt.Add(time.Hour), completedAt)
	if err != nil || !got.Equal(auditFloor) {
		t.Fatalf("audit floor result = %v, %v", got, err)
	}
	if _, err := CleanupPurgeEligibleAt(time.Time{}, completedAt); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("zero retention floor error = %v", err)
	}
}

func TestCleanupPurgeEligibleAtRejectsFirestoreTimestampOverflow(t *testing.T) {
	latestCompletedAt := time.Date(9999, time.December, 25, 0, 0, 0, 0, time.UTC)
	if _, err := CleanupPurgeEligibleAt(latestCompletedAt, latestCompletedAt); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("overflowing audit floor error = %v", err)
	}
	if _, err := CleanupPurgeEligibleAt(
		time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(9999, time.January, 1, 0, 0, 0, 0, time.UTC),
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("out-of-range retention floor error = %v", err)
	}
	if _, err := CleanupPurgeEligibleAt(
		time.Date(1, time.January, 8, 0, 0, 0, 0, time.UTC),
		time.Date(0, time.December, 31, 0, 0, 0, 0, time.UTC),
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("out-of-range completion time error = %v", err)
	}
}

func cleanupLedgerPlanFixture(
	t *testing.T,
	classification ArtifactClassification,
) (time.Time, CleanupExecutionLedgerPlan) {
	t.Helper()
	now, snapshot := cleanupExecutionFixture(t, classification)
	plan, err := BuildCleanupExecutionLedgerPlan(
		cleanupExecutionQueryFixture(snapshot),
		snapshot,
		now,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}
	return now, plan
}

func cleanupExecutionQueryFixture(snapshot CurrentCleanupExecutionSnapshot) CleanupExecutionQuery {
	return CleanupExecutionQuery{
		TenantID:       snapshot.Receipt.TenantID,
		ReservationKey: snapshot.Receipt.ReservationKey,
		AttemptID:      snapshot.Attempt.AttemptID,
	}
}
