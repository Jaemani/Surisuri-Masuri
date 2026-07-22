package ingest

import (
	"errors"
	"testing"
	"time"
)

func TestCleanupExecutionFailurePolicyIsExhaustiveAndCleanupOnly(t *testing.T) {
	tests := []struct {
		errorClass  CleanupExecutionErrorClass
		disposition CleanupExecutionDisposition
		delay       time.Duration
	}{
		{CleanupExecutionErrorProviderTimeout, CleanupExecutionDispositionRetry, CleanupRetryBackoffTransient},
		{CleanupExecutionErrorProviderCancelled, CleanupExecutionDispositionRetry, CleanupRetryBackoffTransient},
		{CleanupExecutionErrorProviderUnavailable, CleanupExecutionDispositionRetry, CleanupRetryBackoffTransient},
		{CleanupExecutionErrorResponseUnverifiable, CleanupExecutionDispositionRetry, CleanupRetryBackoffTransient},
		{CleanupExecutionErrorQuotaLimited, CleanupExecutionDispositionRetry, CleanupRetryBackoffQuota},
		{CleanupExecutionErrorInventoryIncomplete, CleanupExecutionDispositionRetry, CleanupRetryBackoffInventory},
		{CleanupExecutionErrorPermissionDenied, CleanupExecutionDispositionHold, CleanupHoldReviewWindow},
		{CleanupExecutionErrorPreconditionDrift, CleanupExecutionDispositionHold, CleanupHoldReviewWindow},
		{CleanupExecutionErrorGenerationDrift, CleanupExecutionDispositionHold, CleanupHoldReviewWindow},
		{CleanupExecutionErrorLineageMismatch, CleanupExecutionDispositionHold, CleanupHoldReviewWindow},
	}
	for _, test := range tests {
		policy, err := CleanupExecutionFailurePolicyFor(test.errorClass)
		if err != nil || policy.Disposition != test.disposition || policy.Delay != test.delay {
			t.Fatalf("policy(%q) = %#v, %v", test.errorClass, policy, err)
		}
	}
	for _, value := range []CleanupExecutionErrorClass{"", "other", "provider payload"} {
		if _, err := CleanupExecutionFailurePolicyFor(value); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
			t.Fatalf("unknown policy %q error = %v", value, err)
		}
	}
	if ValidRecoveryAttemptOutcome(RecoveryAttemptOutcomeCleanupRetry) ||
		ValidRecoveryAttemptOutcome(RecoveryAttemptOutcomeCleanupHold) {
		t.Fatal("cleanup disposition leaked into forward recovery outcomes")
	}
}

func TestCleanupExecutionDispositionPreservesAllowedPhaseAndRevision(t *testing.T) {
	for _, phase := range []CleanupExecutionPhase{
		CleanupExecutionPhaseRawDispatchRecorded,
		CleanupExecutionPhaseRawOutcomeRecorded,
		CleanupExecutionPhaseManifestDispatchRecorded,
		CleanupExecutionPhaseManifestOutcomeRecorded,
	} {
		t.Run(string(phase), func(t *testing.T) {
			now, plan, ledger := cleanupDispositionLedgerAtPhase(t, phase)
			completedAt := now.Add(10 * time.Second)
			terminal, policy, err := CompleteCleanupExecutionDisposition(
				plan, ledger, CleanupExecutionErrorQuotaLimited, completedAt,
			)
			if err != nil || policy.Disposition != CleanupExecutionDispositionRetry {
				t.Fatalf("CompleteCleanupExecutionDisposition() = %#v, %#v, %v", terminal, policy, err)
			}
			if terminal.Phase != ledger.Phase || terminal.Revision != ledger.Revision ||
				terminal.Disposition != CleanupExecutionDispositionRetry ||
				terminal.ErrorClass != CleanupExecutionErrorQuotaLimited ||
				!terminal.CompletedAt.Equal(completedAt) || !isLowerHexDigest(terminal.EvidenceHash) ||
				ValidateCleanupExecutionLedger(plan, terminal, completedAt) != nil {
				t.Fatalf("phase-preserving terminal = %#v", terminal)
			}
			hash, hashErr := CleanupExecutionDispositionEvidenceHash(plan, terminal)
			if hashErr != nil || hash != terminal.EvidenceHash {
				t.Fatalf("evidence hash = %q, %v", hash, hashErr)
			}
		})
	}
}

func TestCleanupExecutionDispositionRequiresExactDurableUnknownClass(t *testing.T) {
	now, plan, ledger := cleanupDispositionLedgerAtPhase(t, CleanupExecutionPhaseRawDispatchRecorded)
	var err error
	ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, CleanupExecutionTransition{
		Phase:         CleanupExecutionPhaseRawOutcomeRecorded,
		DeleteOutcome: CleanupDeleteUnknown,
		ErrorClass:    CleanupExecutionErrorProviderTimeout,
		ObservedAt:    now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("persist unknown = %v", err)
	}
	terminal, _, err := CompleteCleanupExecutionDisposition(
		plan, ledger, CleanupExecutionErrorProviderTimeout, now.Add(3*time.Second),
	)
	if err != nil || terminal.Phase != CleanupExecutionPhaseRawOutcomeRecorded ||
		terminal.Revision != 3 || terminal.ErrorClass != CleanupExecutionErrorProviderTimeout {
		t.Fatalf("exact unknown disposition = %#v, %v", terminal, err)
	}
	if _, _, err := CompleteCleanupExecutionDisposition(
		plan, ledger, CleanupExecutionErrorProviderUnavailable, now.Add(3*time.Second),
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("different unknown class error = %v", err)
	}
	if _, _, err := CompleteCleanupExecutionDisposition(
		plan, ledger, CleanupExecutionErrorPermissionDenied, now.Add(3*time.Second),
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("unknown reclassified as hold error = %v", err)
	}
}

func TestCleanupExecutionDispositionRejectsUnsupportedProgress(t *testing.T) {
	for _, phase := range []CleanupExecutionPhase{
		CleanupExecutionPhasePlanned,
		CleanupExecutionPhaseRawAbsenceConfirmed,
		CleanupExecutionPhaseManifestAbsenceConfirmed,
		CleanupExecutionPhaseCompleted,
	} {
		t.Run(string(phase), func(t *testing.T) {
			now, plan, ledger := cleanupDispositionLedgerAtPhase(t, phase)
			if _, _, err := CompleteCleanupExecutionDisposition(
				plan, ledger, CleanupExecutionErrorPermissionDenied, now.Add(10*time.Second),
			); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
				t.Fatalf("unsupported phase accepted: %#v, %v", ledger, err)
			}
		})
	}
}

func TestCleanupExecutionDispositionEvidenceRejectsTampering(t *testing.T) {
	now, plan, ledger := cleanupDispositionLedgerAtPhase(t, CleanupExecutionPhaseManifestOutcomeRecorded)
	terminal, _, err := CompleteCleanupExecutionDisposition(
		plan, ledger, CleanupExecutionErrorGenerationDrift, now.Add(10*time.Second),
	)
	if err != nil {
		t.Fatalf("CompleteCleanupExecutionDisposition() = %v", err)
	}
	mutations := []func(*CleanupExecutionLedger){
		func(value *CleanupExecutionLedger) {
			value.TargetHash = differentCleanupExecutionDigest(value.TargetHash)
		},
		func(value *CleanupExecutionLedger) { value.PlanHash = differentCleanupExecutionDigest(value.PlanHash) },
		func(value *CleanupExecutionLedger) { value.Fence.Token++ },
		func(value *CleanupExecutionLedger) { value.Manifest.DeleteOutcome = CleanupDeleteNotFound },
		func(value *CleanupExecutionLedger) { value.Disposition = CleanupExecutionDispositionRetry },
		func(value *CleanupExecutionLedger) { value.ErrorClass = CleanupExecutionErrorLineageMismatch },
		func(value *CleanupExecutionLedger) { value.CompletedAt = value.CompletedAt.Add(time.Nanosecond) },
		func(value *CleanupExecutionLedger) {
			value.EvidenceHash = differentCleanupExecutionDigest(value.EvidenceHash)
		},
	}
	for index, mutate := range mutations {
		tampered := terminal
		mutate(&tampered)
		if !errors.Is(
			ValidateCleanupExecutionLedger(plan, tampered, tampered.CompletedAt),
			ErrInvalidCleanupExecutionLedger,
		) {
			t.Fatalf("tamper %d remained valid: %#v", index, tampered)
		}
	}
}

func TestCleanupExecutionDispositionCursorUsesPolicyAndOldFence(t *testing.T) {
	completedAt := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	fence := LeaseFence{
		OwnerID: "11111111-1111-4111-8111-111111111111",
		Token:   1, ExpiresAt: completedAt.Add(5 * time.Minute),
	}
	next, review, err := CleanupExecutionDispositionCursorAt(
		fence, CleanupExecutionErrorProviderTimeout, completedAt,
	)
	if err != nil || !next.Equal(completedAt.Add(CleanupRetryBackoffTransient)) || !review.IsZero() {
		t.Fatalf("transient cursor = %v, %v, %v", next, review, err)
	}
	fence.ExpiresAt = completedAt.Add(2 * time.Hour)
	next, review, err = CleanupExecutionDispositionCursorAt(
		fence, CleanupExecutionErrorInventoryIncomplete, completedAt,
	)
	if err != nil || !next.Equal(fence.ExpiresAt) || !review.IsZero() {
		t.Fatalf("fence-bounded cursor = %v, %v, %v", next, review, err)
	}
	next, review, err = CleanupExecutionDispositionCursorAt(
		fence, CleanupExecutionErrorPermissionDenied, completedAt,
	)
	if err != nil || !next.IsZero() || !review.Equal(completedAt.Add(CleanupHoldReviewWindow)) {
		t.Fatalf("hold cursor = %v, %v, %v", next, review, err)
	}
	if _, _, err := CleanupExecutionDispositionCursorAt(
		fence, CleanupExecutionErrorClass("other"), completedAt,
	); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("unknown cursor error = %v", err)
	}
}

func cleanupDispositionLedgerAtPhase(
	t *testing.T,
	phase CleanupExecutionPhase,
) (time.Time, CleanupExecutionLedgerPlan, CleanupExecutionLedger) {
	t.Helper()
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	steps := []CleanupExecutionTransition{
		{Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second)},
		{Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteObserved, ObservedAt: now.Add(2 * time.Second)},
		{Phase: CleanupExecutionPhaseRawAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: now.Add(4 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestOutcomeRecorded, DeleteOutcome: CleanupDeleteObserved, ObservedAt: now.Add(5 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(6 * time.Second)},
		{Phase: CleanupExecutionPhaseCompleted, ObservedAt: now.Add(7 * time.Second)},
	}
	if phase == CleanupExecutionPhasePlanned {
		return now, plan, ledger
	}
	for _, step := range steps {
		ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, step)
		if err != nil {
			t.Fatalf("advance %q = %v", step.Phase, err)
		}
		if step.Phase == phase {
			return now, plan, ledger
		}
	}
	t.Fatalf("unsupported fixture phase %q", phase)
	return time.Time{}, CleanupExecutionLedgerPlan{}, CleanupExecutionLedger{}
}
