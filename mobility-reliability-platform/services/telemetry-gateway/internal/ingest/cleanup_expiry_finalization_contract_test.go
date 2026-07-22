package ingest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCompleteCleanupExecutionBuildsTerminalLedgerPurgeAndPreStateQuery(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ready := cleanupManifestAbsenceLedgerFixture(t, plan, now)
	completedAt := now.Add(7 * time.Second)
	completed, purgeEligibleAt, query, err := CompleteCleanupExecution(
		plan, ready, plan.Target.Command.CreatedAt.Add(ReceiptControlRetention), completedAt,
	)
	if err != nil {
		t.Fatalf("CompleteCleanupExecution() = %v", err)
	}
	if completed.Phase != CleanupExecutionPhaseCompleted || completed.Revision != 8 ||
		completed.Disposition != CleanupExecutionDispositionComplete ||
		completed.EvidenceHash == "" || !completed.CompletedAt.Equal(completedAt) {
		t.Fatalf("completed ledger = %#v", completed)
	}
	if !purgeEligibleAt.Equal(plan.Target.Command.CreatedAt.Add(ReceiptControlRetention)) {
		t.Fatalf("purge eligible = %v", purgeEligibleAt)
	}
	if query.ExpectedPreReceiptRevision != ready.ReceiptRevision ||
		query.ExpectedFinalReceiptRevision != ready.ReceiptRevision+1 ||
		query.ExpectedPreLedgerRevision != 7 || query.ExpectedFinalLedgerRevision != 8 ||
		query.ExpectedTargetHash != plan.Target.TargetHash || query.ExpectedPlanHash != plan.PlanHash ||
		query.ExpectedFence != ready.Fence ||
		ValidateCleanupExpiryFinalizationOutcomeQuery(query) != nil {
		t.Fatalf("outcome query = %#v", query)
	}
	evidence, err := CleanupExecutionEvidenceHash(plan, completed)
	if err != nil || evidence != completed.EvidenceHash {
		t.Fatalf("evidence = %q, %v", evidence, err)
	}
}

func TestCompleteCleanupExecutionRejectsNonterminalUnknownAndStaleEvidence(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ready := cleanupManifestAbsenceLedgerFixture(t, plan, now)
	completedAt := now.Add(7 * time.Second)

	tests := []struct {
		name   string
		mutate func(*CleanupExecutionLedger)
	}{
		{name: "nonterminal", mutate: func(value *CleanupExecutionLedger) {
			value.Phase = CleanupExecutionPhaseManifestOutcomeRecorded
			value.Revision = 6
			value.Manifest.AuditOutcome = ""
			value.Manifest.AuditedAt = time.Time{}
		}},
		{name: "unknown raw", mutate: func(value *CleanupExecutionLedger) {
			value.Raw.DeleteOutcome = CleanupDeleteUnknown
		}},
		{name: "unknown manifest", mutate: func(value *CleanupExecutionLedger) {
			value.Manifest.DeleteOutcome = CleanupDeleteUnknown
		}},
		{name: "stale raw evidence", mutate: func(value *CleanupExecutionLedger) {
			delta := CleanupExpiryFinalizationEvidenceMaxAge + time.Second
			value.Raw.DispatchedAt = value.Raw.DispatchedAt.Add(-delta)
			value.Raw.OutcomeRecordedAt = value.Raw.OutcomeRecordedAt.Add(-delta)
			value.Raw.AuditedAt = value.Raw.AuditedAt.Add(-delta)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := ready
			test.mutate(&mutated)
			if _, _, _, err := CompleteCleanupExecution(
				plan, mutated, plan.Target.Command.CreatedAt.Add(ReceiptControlRetention), completedAt,
			); !errors.Is(err, ErrInvalidCleanupExpiryFinalization) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCompletedCleanupExecutionPlanReconstructsOriginalBinding(t *testing.T) {
	now, snapshot := cleanupExecutionFixture(t, ArtifactClassificationValidComplete)
	query := cleanupExecutionQueryFixture(snapshot)
	plan, err := BuildCleanupExecutionLedgerPlan(query, snapshot, now)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}
	ready := cleanupManifestAbsenceLedgerFixture(t, plan, now)
	completedAt := now.Add(7 * time.Second)
	_, purgeEligibleAt, _, err := CompleteCleanupExecution(
		plan, ready, snapshot.Receipt.ReceiptRetentionFloor, completedAt,
	)
	if err != nil {
		t.Fatalf("CompleteCleanupExecution() = %v", err)
	}
	receipt := completedCleanupReceiptFixture(snapshot.Receipt, completedAt, purgeEligibleAt)
	reconstructed, err := BuildCompletedCleanupExecutionLedgerPlan(
		query, receipt, plan.Target, completedAt,
	)
	if err != nil || reconstructed.PlanHash != plan.PlanHash ||
		reconstructed.Target.TargetHash != plan.Target.TargetHash ||
		reconstructed.ExpectedRawPath != plan.ExpectedRawPath ||
		reconstructed.ExpectedManifestPath != plan.ExpectedManifestPath {
		t.Fatalf("reconstructed = %#v, %v", reconstructed, err)
	}

	tampered := receipt
	tampered.Revision++
	if _, err := BuildCompletedCleanupExecutionLedgerPlan(
		query, tampered, plan.Target, completedAt,
	); !errors.Is(err, ErrInvalidCleanupExpiryFinalization) {
		t.Fatalf("revision tamper error = %v", err)
	}
	tampered = receipt
	different := purgeEligibleAt.Add(time.Second)
	tampered.PurgeEligibleAt = &different
	if _, err := BuildCompletedCleanupExecutionLedgerPlan(
		query, tampered, plan.Target, completedAt,
	); !errors.Is(err, ErrInvalidCleanupExpiryFinalization) {
		t.Fatalf("purge tamper error = %v", err)
	}
}

func TestCleanupExpiryFinalizationOutcomeDistinguishesCommittedNotCommittedAndUnverifiable(t *testing.T) {
	now, snapshot := cleanupExecutionFixture(t, ArtifactClassificationValidComplete)
	executionQuery := cleanupExecutionQueryFixture(snapshot)
	plan, err := BuildCleanupExecutionLedgerPlan(executionQuery, snapshot, now)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}
	ready := cleanupManifestAbsenceLedgerFixture(t, plan, now)
	completedAt := now.Add(7 * time.Second)
	completed, purgeEligibleAt, query, err := CompleteCleanupExecution(
		plan, ready, snapshot.Receipt.ReceiptRetentionFloor, completedAt,
	)
	if err != nil {
		t.Fatalf("CompleteCleanupExecution() = %v", err)
	}

	prestate := cleanupFinalizationSnapshotFixture(snapshot.Receipt, plan, ready, snapshot.ReadTime)
	outcome, err := EvaluateCleanupExpiryFinalizationOutcome(query, prestate, snapshot.ReadTime)
	if err != nil || outcome.CommitStatus != CleanupExpiryFinalizationNotCommitted ||
		outcome.EvidenceHash != "" || !outcome.CompletedAt.IsZero() || !outcome.PurgeEligibleAt.IsZero() {
		t.Fatalf("not committed outcome = %#v, %v", outcome, err)
	}

	terminalReceipt := completedCleanupReceiptFixture(
		snapshot.Receipt, completedAt, purgeEligibleAt,
	)
	terminalPlan, err := BuildCompletedCleanupExecutionLedgerPlan(
		executionQuery, terminalReceipt, plan.Target, completedAt,
	)
	if err != nil {
		t.Fatalf("BuildCompletedCleanupExecutionLedgerPlan() = %v", err)
	}
	committed := cleanupFinalizationSnapshotFixture(
		terminalReceipt, terminalPlan, completed, completedAt.Add(time.Hour),
	)
	committed.Attempt.Status = RecoveryAttemptCompleted
	committed.Attempt.Outcome = RecoveryAttemptOutcomeExpired
	committed.Attempt.CompletedAt = completedAt
	outcome, err = EvaluateCleanupExpiryFinalizationOutcome(
		query, committed, committed.ReadTime,
	)
	if err != nil || outcome.CommitStatus != CleanupExpiryFinalizationCommitted ||
		outcome.EvidenceHash != completed.EvidenceHash ||
		!outcome.CompletedAt.Equal(completedAt) || !outcome.PurgeEligibleAt.Equal(purgeEligibleAt) {
		t.Fatalf("committed outcome = %#v, %v", outcome, err)
	}

	corrupt := committed
	corrupt.Attempt.Ledger.EvidenceHash = differentCleanupExecutionDigest(completed.EvidenceHash)
	outcome, err = EvaluateCleanupExpiryFinalizationOutcome(query, corrupt, corrupt.ReadTime)
	if err != nil || outcome.CommitStatus != CleanupExpiryFinalizationUnverifiable ||
		outcome.EvidenceHash != "" || !outcome.CompletedAt.IsZero() {
		t.Fatalf("corrupt outcome = %#v, %v", outcome, err)
	}
}

func TestCleanupExpiryFinalizationOutcomeGrantBindsExactQuery(t *testing.T) {
	now, snapshot := cleanupExecutionFixture(t, ArtifactClassificationValidComplete)
	plan, err := BuildCleanupExecutionLedgerPlan(cleanupExecutionQueryFixture(snapshot), snapshot, now)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}
	ready := cleanupManifestAbsenceLedgerFixture(t, plan, now)
	query, err := CleanupExpiryFinalizationOutcomeQueryForLedger(plan, ready)
	if err != nil {
		t.Fatalf("CleanupExpiryFinalizationOutcomeQueryForLedger() = %v", err)
	}
	current := cleanupFinalizationSnapshotFixture(snapshot.Receipt, plan, ready, snapshot.ReadTime)
	store := &cleanupFinalizationOutcomeStoreStub{snapshot: current}
	authorizer, err := NewSystemCleanupExpiryFinalizationOutcomeAuthorizer(
		store, func() time.Time { return snapshot.ReadTime },
	)
	if err != nil {
		t.Fatalf("NewSystemCleanupExpiryFinalizationOutcomeAuthorizer() = %v", err)
	}
	grant, err := authorizer.Authorize(context.Background(), query)
	if err != nil || ValidateCleanupExpiryFinalizationOutcomeAuthorization(
		grant, query, snapshot.ReadTime,
	) != nil {
		t.Fatalf("Authorize() = %#v, %v", grant, err)
	}
	mutated := query
	mutated.ExpectedPlanHash = differentCleanupExecutionDigest(query.ExpectedPlanHash)
	if !errors.Is(
		ValidateCleanupExpiryFinalizationOutcomeAuthorization(grant, mutated, snapshot.ReadTime),
		ErrInvalidCleanupExpiryFinalizationOutcome,
	) {
		t.Fatal("mutated query retained grant authority")
	}
	if !errors.Is(
		ValidateCleanupExpiryFinalizationOutcomeAuthorization(
			grant, query, snapshot.ReadTime.Add(CleanupExpiryFinalizationOutcomeGrantTTL),
		),
		ErrCleanupExpiryFinalizationOutcomeExpired,
	) {
		t.Fatal("exact grant expiry remained authorized")
	}
}

func TestCleanupExpiryFinalizationOutcomeQueryRejectsReceiptRevisionOverflow(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ready := cleanupManifestAbsenceLedgerFixture(t, plan, now)
	query, err := CleanupExpiryFinalizationOutcomeQueryForLedger(plan, ready)
	if err != nil {
		t.Fatalf("CleanupExpiryFinalizationOutcomeQueryForLedger() = %v", err)
	}
	query.ExpectedPreReceiptRevision = int64(^uint64(0) >> 1)
	query.ExpectedFinalReceiptRevision = query.ExpectedPreReceiptRevision + 1
	if !errors.Is(
		ValidateCleanupExpiryFinalizationOutcomeQuery(query),
		ErrInvalidCleanupExpiryFinalizationOutcome,
	) {
		t.Fatal("overflowed receipt revision query remained valid")
	}
}

func TestCleanupExpiryFinalizationOutcomeGrantHasNoExportedFields(t *testing.T) {
	typeOfGrant := reflect.TypeOf(CleanupExpiryFinalizationOutcomeReadGrant{})
	for index := 0; index < typeOfGrant.NumField(); index++ {
		if typeOfGrant.Field(index).IsExported() {
			t.Fatalf("exported grant field = %s", typeOfGrant.Field(index).Name)
		}
	}
}

func TestCleanupExpiredOutcomeRemainsCleanupOnly(t *testing.T) {
	if ValidRecoveryAttemptOutcome(RecoveryAttemptOutcomeExpired) {
		t.Fatal("cleanup-only expired outcome was admitted to forward recovery")
	}
}

type cleanupFinalizationOutcomeStoreStub struct {
	snapshot CurrentCleanupExpiryFinalizationSnapshot
	err      error
}

func (s *cleanupFinalizationOutcomeStoreStub) LoadCurrentCleanupExpiryFinalizationOutcome(
	context.Context,
	CleanupExpiryFinalizationOutcomeQuery,
) (CurrentCleanupExpiryFinalizationSnapshot, error) {
	return s.snapshot, s.err
}

func cleanupManifestAbsenceLedgerFixture(
	t *testing.T,
	plan CleanupExecutionLedgerPlan,
	now time.Time,
) CleanupExecutionLedger {
	t.Helper()
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	steps := []CleanupExecutionTransition{
		{Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second)},
		{Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteObserved, ObservedAt: now.Add(2 * time.Second)},
		{Phase: CleanupExecutionPhaseRawAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: now.Add(4 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestOutcomeRecorded, DeleteOutcome: CleanupDeleteNotFound, ObservedAt: now.Add(5 * time.Second)},
		{Phase: CleanupExecutionPhaseManifestAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(6 * time.Second)},
	}
	for _, step := range steps {
		ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, step)
		if err != nil {
			t.Fatalf("AdvanceCleanupExecutionLedger(%q) = %v", step.Phase, err)
		}
	}
	return ledger
}

func completedCleanupReceiptFixture(
	receipt Receipt,
	completedAt time.Time,
	purgeEligibleAt time.Time,
) Receipt {
	receipt.State = ReceiptExpired
	receipt.Revision++
	receipt.LeaseOwnerID = ""
	receipt.LeaseOwnerKind = ""
	receipt.LeaseAcquiredAt = time.Time{}
	receipt.LeaseHeartbeatAt = time.Time{}
	receipt.LeaseExpiresAt = time.Time{}
	receipt.NextRecoveryAt = time.Time{}
	receipt.LastRecoveryCode = ""
	receipt.UpdatedAt = completedAt.UTC()
	receipt.PurgeEligibleAt = cloneCleanupFinalizationTime(purgeEligibleAt)
	return receipt
}

func cleanupFinalizationSnapshotFixture(
	receipt Receipt,
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	readTime time.Time,
) CurrentCleanupExpiryFinalizationSnapshot {
	return CurrentCleanupExpiryFinalizationSnapshot{
		Receipt: receipt,
		Attempt: CurrentCleanupExpiryFinalizationAttempt{
			AttemptID: plan.Target.Command.AttemptID, TenantID: plan.Target.Command.TenantID,
			ReceiptID: plan.Target.Command.ReceiptID, OwnerKind: LeaseOwnerCleanup,
			FencingToken:  plan.Target.Command.FencingToken,
			WorkerVersion: plan.Target.Command.WorkerVersion,
			Status:        RecoveryAttemptStarted, StartedAt: plan.Target.Command.LeaseAcquiredAt,
			Ledger: ledger,
		},
		Plan: plan, PlanValid: true, ReadTime: readTime.UTC(),
	}
}

func cloneCleanupFinalizationTime(value time.Time) *time.Time {
	cloned := value.UTC()
	return &cloned
}
