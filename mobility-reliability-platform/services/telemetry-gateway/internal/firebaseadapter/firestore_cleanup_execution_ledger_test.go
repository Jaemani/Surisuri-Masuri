package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreCleanupExecutionLedgerInitializesExactAttemptOnly(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)

	ledger, mutationStatus, err := fixture.store.initializeCleanupExecutionLedger(
		context.Background(), fixture.query,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("initializeCleanupExecutionLedger() = %#v, %q, %v", ledger, mutationStatus, err)
	}
	if !reflect.DeepEqual(ledger, fixture.ledger) {
		t.Fatalf("initialized ledger = %#v, want %#v", ledger, fixture.ledger)
	}
	if len(fixture.transaction.updates) != 1 || len(fixture.transaction.creates) != 0 {
		t.Fatalf(
			"creates/updates = %d/%d, want 0/1",
			len(fixture.transaction.creates), len(fixture.transaction.updates),
		)
	}
	update := fixture.transaction.updates[0]
	if update.path != fixture.attemptPath {
		t.Fatalf("update path = %q, want %q", update.path, fixture.attemptPath)
	}
	values := cleanupExecutionUpdateValues(update.updates)
	if values["cleanup_execution_revision"] != int64(1) ||
		values["cleanup_phase"] != string(ingest.CleanupExecutionPhasePlanned) ||
		values["cleanup_target_hash"] != fixture.plan.Target.TargetHash ||
		values["cleanup_plan_hash"] != fixture.plan.PlanHash {
		t.Fatalf("planned updates = %#v", values)
	}
}

func TestFirestoreCleanupExecutionLedgerInitializationReplaysWithoutWrite(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	fixture.seedLedger(fixture.ledger)

	ledger, mutationStatus, err := fixture.store.initializeCleanupExecutionLedger(
		context.Background(), fixture.query,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationReplayed ||
		!reflect.DeepEqual(ledger, fixture.ledger) {
		t.Fatalf("initialize replay = %#v, %q, %v", ledger, mutationStatus, err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupExecutionLedgerPersistsAndReplaysExactProgress(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	fixture.seedLedger(fixture.ledger)
	command, err := ingest.BuildCleanupExecutionProgressCommand(
		fixture.plan,
		fixture.ledger,
		ingest.CleanupExecutionPhaseRawDispatchRecorded,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionProgressCommand() = %v", err)
	}

	next, mutationStatus, err := fixture.store.recordCleanupExecutionProgress(
		context.Background(), command,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
		next.Revision != fixture.ledger.Revision+1 ||
		next.Phase != ingest.CleanupExecutionPhaseRawDispatchRecorded {
		t.Fatalf("recordCleanupExecutionProgress() = %#v, %q, %v", next, mutationStatus, err)
	}
	if len(fixture.transaction.updates) != 1 ||
		fixture.transaction.updates[0].path != fixture.attemptPath {
		t.Fatalf("progress updates = %#v", fixture.transaction.updates)
	}

	fixture.seedLedger(next)
	fixture.transaction.updates = nil
	replayed, mutationStatus, err := fixture.store.recordCleanupExecutionProgress(
		context.Background(), command,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationReplayed ||
		!reflect.DeepEqual(replayed, next) {
		t.Fatalf("progress replay = %#v, %q, %v", replayed, mutationStatus, err)
	}
	fixture.assertNoWrites(t)

	outcomeCommand, err := ingest.BuildCleanupExecutionProgressCommand(
		fixture.plan,
		next,
		ingest.CleanupExecutionPhaseRawOutcomeRecorded,
		ingest.CleanupDeleteObserved,
		"",
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionProgressCommand(raw outcome) = %v", err)
	}
	outcome, mutationStatus, err := fixture.store.recordCleanupExecutionProgress(
		context.Background(), outcomeCommand,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
		outcome.Phase != ingest.CleanupExecutionPhaseRawOutcomeRecorded ||
		outcome.Raw.DeleteOutcome != ingest.CleanupDeleteObserved ||
		outcome.Raw.OutcomeRecordedAt.IsZero() {
		t.Fatalf("raw outcome progress = %#v, %q, %v", outcome, mutationStatus, err)
	}
}

func TestFirestoreCleanupExecutionLedgerRejectsConflictAndPartialResidueWithoutWrite(t *testing.T) {
	t.Run("absence without evidence capability", func(t *testing.T) {
		fixture := newCleanupExecutionLedgerStoreFixture(t)
		ledger := fixture.ledger
		var err error
		ledger, err = ingest.AdvanceCleanupExecutionLedger(fixture.plan, ledger, ingest.CleanupExecutionTransition{
			Phase:      ingest.CleanupExecutionPhaseRawDispatchRecorded,
			ObservedAt: fixture.plan.Target.Command.CreatedAt.Add(time.Second),
		})
		if err != nil {
			t.Fatalf("raw dispatch = %v", err)
		}
		ledger, err = ingest.AdvanceCleanupExecutionLedger(fixture.plan, ledger, ingest.CleanupExecutionTransition{
			Phase:         ingest.CleanupExecutionPhaseRawOutcomeRecorded,
			DeleteOutcome: ingest.CleanupDeleteObserved,
			ObservedAt:    fixture.plan.Target.Command.CreatedAt.Add(2 * time.Second),
		})
		if err != nil {
			t.Fatalf("raw outcome = %v", err)
		}
		command, err := ingest.BuildCleanupExecutionProgressCommand(
			fixture.plan, ledger, ingest.CleanupExecutionPhaseRawAbsenceConfirmed,
			"", ingest.CleanupAuditConfirmedAbsent,
		)
		if err != nil {
			t.Fatalf("BuildCleanupExecutionProgressCommand() = %v", err)
		}
		if _, _, err := fixture.store.RecordCleanupExecutionProgress(
			context.Background(), command,
		); !errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) {
			t.Fatalf("unauthorized absence persistence error = %v", err)
		}
		fixture.assertNoWrites(t)
	})

	t.Run("plan hash conflict", func(t *testing.T) {
		fixture := newCleanupExecutionLedgerStoreFixture(t)
		fixture.seedLedger(fixture.ledger)
		command, err := ingest.BuildCleanupExecutionProgressCommand(
			fixture.plan,
			fixture.ledger,
			ingest.CleanupExecutionPhaseRawDispatchRecorded,
			"",
			"",
		)
		if err != nil {
			t.Fatalf("BuildCleanupExecutionProgressCommand() = %v", err)
		}
		command.ExpectedPlanHash = differentCleanupLedgerDigest(command.ExpectedPlanHash)
		if _, _, err := fixture.store.recordCleanupExecutionProgress(
			context.Background(), command,
		); !errors.Is(err, ingest.ErrCleanupExecutionConflict) {
			t.Fatalf("plan conflict error = %v", err)
		}
		fixture.assertNoWrites(t)
	})

	t.Run("partial initialization residue", func(t *testing.T) {
		fixture := newCleanupExecutionLedgerStoreFixture(t)
		attempt := fixture.transaction.attempts[fixture.attemptPath]
		attempt.CleanupPlanHash = fixture.plan.PlanHash
		fixture.transaction.attempts[fixture.attemptPath] = attempt
		if _, _, err := fixture.store.initializeCleanupExecutionLedger(
			context.Background(), fixture.query,
		); !errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) {
			t.Fatalf("partial residue error = %v", err)
		}
		fixture.assertNoWrites(t)
	})

	t.Run("receipt revision drift", func(t *testing.T) {
		fixture := newCleanupExecutionLedgerStoreFixture(t)
		receipt := fixture.transaction.receipts[admissionReceiptPath()]
		receipt.Revision++
		fixture.transaction.receipts[admissionReceiptPath()] = receipt
		if _, _, err := fixture.store.initializeCleanupExecutionLedger(
			context.Background(), fixture.query,
		); err == nil {
			t.Fatal("revision drift initialized a ledger")
		}
		fixture.assertNoWrites(t)
	})

	t.Run("exact lease expiry", func(t *testing.T) {
		fixture := newCleanupExecutionLedgerStoreFixture(t)
		expiresAt := fixture.plan.Target.Command.LeaseExpiresAt
		fixture.store.now = func() time.Time {
			return expiresAt
		}
		fixture.transaction.readTime = expiresAt
		targetPath := cleanupTargetDocumentPath(
			fixture.query.TenantID, fixture.query.AttemptID,
		)
		target := fixture.transaction.targets[targetPath]
		target.ReadTime = expiresAt
		fixture.transaction.targets[targetPath] = target
		if _, _, err := fixture.store.initializeCleanupExecutionLedger(
			context.Background(), fixture.query,
		); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
			t.Fatalf("exact expiry state gate error = %v", err)
		}
		if _, _, err := fixture.store.InitializeCleanupExecutionLedger(
			context.Background(), fixture.query,
		); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
			t.Fatalf("exact expiry error = %v", err)
		}
		fixture.assertNoWrites(t)
	})

	t.Run("incoherent application and read clocks", func(t *testing.T) {
		fixture := newCleanupExecutionLedgerStoreFixture(t)
		fixture.store.now = func() time.Time {
			return fixture.plan.Target.Command.LeaseExpiresAt
		}
		if _, _, err := fixture.store.InitializeCleanupExecutionLedger(
			context.Background(), fixture.query,
		); !errors.Is(err, ingest.ErrCleanupExecutionUnavailable) {
			t.Fatalf("incoherent clock error = %v", err)
		}
		fixture.assertNoWrites(t)
	})
}

type cleanupExecutionLedgerStoreFixture struct {
	store       *FirestoreAdmissionStore
	transaction *fakeCleanupTargetTransaction
	query       ingest.CleanupExecutionQuery
	plan        ingest.CleanupExecutionLedgerPlan
	ledger      ingest.CleanupExecutionLedger
	attemptPath string
}

func newCleanupExecutionLedgerStoreFixture(t *testing.T) *cleanupExecutionLedgerStoreFixture {
	t.Helper()
	targetFixture := newCleanupTargetAdapterFixture(t)
	targetFixture.seedExactTarget(t)
	targetFixture.store.now = func() time.Time { return targetFixture.observedAt }
	receipt := targetFixture.transaction.receipts[admissionReceiptPath()]
	attempt := targetFixture.transaction.attempts[targetFixture.attemptPath]
	targetResult := targetFixture.transaction.targets[targetFixture.targetPath]
	target, err := targetResult.Target.toDomain()
	if err != nil {
		t.Fatalf("target toDomain() = %v", err)
	}
	query := ingest.CleanupExecutionQuery{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		AttemptID: attempt.AttemptID,
	}
	plan, err := ingest.BuildCleanupExecutionLedgerPlan(query, ingest.CurrentCleanupExecutionSnapshot{
		Receipt: receipt.toDomain(), Attempt: currentCleanupAttempt(attempt),
		Target: target, ReadTime: targetFixture.observedAt,
	}, targetFixture.observedAt)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}
	ledger, err := ingest.NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	return &cleanupExecutionLedgerStoreFixture{
		store: targetFixture.store, transaction: targetFixture.transaction,
		query: query, plan: plan, ledger: ledger, attemptPath: targetFixture.attemptPath,
	}
}

func (fixture *cleanupExecutionLedgerStoreFixture) seedLedger(ledger ingest.CleanupExecutionLedger) {
	attempt := fixture.transaction.attempts[fixture.attemptPath]
	fixture.transaction.attempts[fixture.attemptPath] = attemptWithCleanupExecutionLedger(attempt, ledger)
}

func (fixture *cleanupExecutionLedgerStoreFixture) assertNoWrites(t *testing.T) {
	t.Helper()
	if len(fixture.transaction.creates) != 0 || len(fixture.transaction.updates) != 0 {
		t.Fatalf(
			"creates/updates = %d/%d, want 0/0",
			len(fixture.transaction.creates), len(fixture.transaction.updates),
		)
	}
}
