package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreCleanupExpiryFinalizationBuildsFourDocumentAtomicMutation(t *testing.T) {
	fixture, _, completedAt := newCleanupExpiryFinalizationFixture(t)
	result, err := fixture.store.finalizeExpiredCleanup(context.Background(), fixture.query)
	if err != nil {
		t.Fatalf("finalizeExpiredCleanup() = %v", err)
	}
	if result.Receipt.State != ingest.ReceiptExpired ||
		result.Receipt.Revision != fixture.plan.Target.Command.ReceiptRevision+1 ||
		result.Receipt.PurgeEligibleAt == nil || result.Receipt.LeaseOwnerID != "" ||
		result.Ledger.Phase != ingest.CleanupExecutionPhaseCompleted ||
		result.Ledger.Revision != 8 || !result.Ledger.CompletedAt.Equal(completedAt) ||
		ingest.ValidateCleanupExpiryFinalizationOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("finalization result = %#v", result)
	}
	if len(fixture.transaction.updates) != 4 || len(fixture.transaction.creates) != 0 {
		t.Fatalf("creates/updates = %d/%d", len(fixture.transaction.creates), len(fixture.transaction.updates))
	}

	updatesByPath := make(map[string]map[string]any, len(fixture.transaction.updates))
	for _, update := range fixture.transaction.updates {
		updatesByPath[update.path] = cleanupExecutionUpdateValues(update.updates)
	}
	targetPath := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
	if _, mutated := updatesByPath[targetPath]; mutated {
		t.Fatal("immutable cleanup target was mutated")
	}
	attemptValues := updatesByPath[fixture.attemptPath]
	if attemptValues["status"] != string(ingest.RecoveryAttemptCompleted) ||
		attemptValues["outcome"] != string(ingest.RecoveryAttemptOutcomeExpired) ||
		attemptValues["cleanup_phase"] != string(ingest.CleanupExecutionPhaseCompleted) ||
		attemptValues["cleanup_execution_revision"] != int64(8) ||
		attemptValues["cleanup_evidence_hash"] != result.Ledger.EvidenceHash ||
		attemptValues["completed_at"] != completedAt {
		t.Fatalf("attempt updates = %#v", attemptValues)
	}
	receiptValues := updatesByPath[admissionReceiptPath()]
	if receiptValues["status"] != string(ingest.ReceiptExpired) ||
		receiptValues["revision"] != result.Receipt.Revision ||
		receiptValues["purge_eligible_at"] != *result.Receipt.PurgeEligibleAt ||
		receiptValues["updated_at"] != completedAt {
		t.Fatalf("receipt updates = %#v", receiptValues)
	}
	idempotencyPath := idempotencyDocumentPath(fixture.query.TenantID, fixture.query.ReservationKey)
	clientBatchPath := clientBatchDocumentPath(
		fixture.query.TenantID,
		fixture.transaction.indexes[idempotencyPath].ClientBatchKey,
	)
	for _, path := range []string{idempotencyPath, clientBatchPath} {
		values := updatesByPath[path]
		if len(values) != 1 || values["purge_eligible_at"] != *result.Receipt.PurgeEligibleAt {
			t.Fatalf("index %q updates = %#v", path, values)
		}
	}
}

func TestFirestoreCleanupExpiryFinalizationPreservesCorrelationQueryOnEveryWriteError(t *testing.T) {
	for failUpdateAt := 1; failUpdateAt <= 4; failUpdateAt++ {
		t.Run(string(rune('0'+failUpdateAt)), func(t *testing.T) {
			fixture, _, _ := newCleanupExpiryFinalizationFixture(t)
			fixture.transaction.updateErr = errors.New("commit response unavailable")
			fixture.transaction.failUpdateAt = failUpdateAt
			result, err := fixture.store.FinalizeExpiredCleanup(context.Background(), fixture.query)
			if !errors.Is(err, ingest.ErrCleanupExpiryFinalizationUnavailable) {
				t.Fatalf("FinalizeExpiredCleanup() error = %v", err)
			}
			if ingest.ValidateCleanupExpiryFinalizationOutcomeQuery(result.OutcomeQuery) != nil ||
				result.Ledger.Phase != ingest.CleanupExecutionPhaseCompleted ||
				result.Receipt.State != ingest.ReceiptExpired {
				t.Fatalf("response-loss result = %#v", result)
			}
		})
	}
}

func TestFirestoreCleanupExpiryFinalizationPreservesFirstCorrelationQueryWhenRetryDrifts(t *testing.T) {
	fixture, _, _ := newCleanupExpiryFinalizationFixture(t)
	transactionCalls := 0
	fixture.store.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		transactionCalls++
		if transactionCalls == 1 {
			return operation(ctx, fixture.transaction)
		}
		if err := operation(ctx, fixture.transaction); err != nil {
			return err
		}
		receipt := fixture.transaction.receipts[admissionReceiptPath()]
		receipt.Revision++
		fixture.transaction.receipts[admissionReceiptPath()] = receipt
		return operation(ctx, fixture.transaction)
	}

	result, err := fixture.store.FinalizeExpiredCleanup(context.Background(), fixture.query)
	if !errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("FinalizeExpiredCleanup() error = %v", err)
	}
	if ingest.ValidateCleanupExpiryFinalizationOutcomeQuery(result.OutcomeQuery) != nil ||
		result.Ledger.Phase != ingest.CleanupExecutionPhaseCompleted ||
		result.Receipt.State != ingest.ReceiptExpired {
		t.Fatalf("retry response-loss result = %#v", result)
	}
}

func TestFirestoreCleanupExpiryFinalizationCapsTransactionAtImmutableFenceDeadline(t *testing.T) {
	fixture, _, _ := newCleanupExpiryFinalizationFixture(t)
	var capturedDeadline time.Time
	fixture.store.cleanupExpiryFinalizationContext = func(
		parent context.Context,
		deadline time.Time,
	) (context.Context, context.CancelFunc) {
		capturedDeadline = deadline
		return context.WithCancel(parent)
	}

	if _, err := fixture.store.FinalizeExpiredCleanup(
		context.Background(), fixture.query,
	); err != nil {
		t.Fatalf("FinalizeExpiredCleanup() = %v", err)
	}
	if !capturedDeadline.Equal(fixture.plan.Target.Command.LeaseExpiresAt) {
		t.Fatalf(
			"transaction deadline = %s, want immutable fence %s",
			capturedDeadline,
			fixture.plan.Target.Command.LeaseExpiresAt,
		)
	}
}

func TestFirestoreCleanupExpiryFinalizationPreservesQueryWhenFenceDeadlineLosesCommitResponse(t *testing.T) {
	fixture, _, _ := newCleanupExpiryFinalizationFixture(t)
	originalRunner := fixture.store.runTransaction
	transactionCalls := 0
	fixture.store.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		transactionCalls++
		if transactionCalls == 1 {
			return originalRunner(ctx, operation)
		}
		if err := operation(ctx, fixture.transaction); err != nil {
			return err
		}
		return context.DeadlineExceeded
	}
	fixture.store.cleanupExpiryFinalizationContext = func(
		parent context.Context,
		_ time.Time,
	) (context.Context, context.CancelFunc) {
		return context.WithDeadline(parent, time.Now().Add(-time.Second))
	}

	result, err := fixture.store.FinalizeExpiredCleanup(context.Background(), fixture.query)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		t.Fatalf("FinalizeExpiredCleanup() error = %v", err)
	}
	if ingest.ValidateCleanupExpiryFinalizationOutcomeQuery(result.OutcomeQuery) != nil ||
		result.Ledger.Phase != ingest.CleanupExecutionPhaseCompleted ||
		result.Receipt.State != ingest.ReceiptExpired {
		t.Fatalf("deadline response-loss result = %#v", result)
	}
}

func TestFirestoreCleanupExpiryFinalizationRejectsUnsafeStateWithoutWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*cleanupExecutionLedgerStoreFixture, *ingest.CleanupExecutionLedger)
	}{
		{name: "phase not ready", mutate: func(_ *cleanupExecutionLedgerStoreFixture, ledger *ingest.CleanupExecutionLedger) {
			ledger.Phase = ingest.CleanupExecutionPhaseManifestOutcomeRecorded
			ledger.Revision = 6
			ledger.Manifest.AuditOutcome = ""
			ledger.Manifest.AuditedAt = time.Time{}
		}},
		{name: "unknown raw", mutate: func(_ *cleanupExecutionLedgerStoreFixture, ledger *ingest.CleanupExecutionLedger) {
			ledger.Raw.DeleteOutcome = ingest.CleanupDeleteUnknown
		}},
		{name: "receipt revision drift", mutate: func(fixture *cleanupExecutionLedgerStoreFixture, _ *ingest.CleanupExecutionLedger) {
			receipt := fixture.transaction.receipts[admissionReceiptPath()]
			receipt.Revision++
			fixture.transaction.receipts[admissionReceiptPath()] = receipt
		}},
		{name: "exact lease expiry", mutate: func(fixture *cleanupExecutionLedgerStoreFixture, _ *ingest.CleanupExecutionLedger) {
			expiresAt := fixture.plan.Target.Command.LeaseExpiresAt
			fixture.store.now = func() time.Time { return expiresAt }
			fixture.transaction.readTime = expiresAt
			targetPath := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
			target := fixture.transaction.targets[targetPath]
			target.ReadTime = expiresAt
			fixture.transaction.targets[targetPath] = target
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, ready, _ := newCleanupExpiryFinalizationFixture(t)
			test.mutate(fixture, &ready)
			fixture.seedLedger(ready)
			fixture.transaction.updates = nil
			fixture.transaction.updateCalls = 0
			if _, err := fixture.store.FinalizeExpiredCleanup(
				context.Background(), fixture.query,
			); err == nil {
				t.Fatal("unsafe state was finalized")
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestFirestoreCleanupExpiryFinalizationOutcomeCorrelatesBeforeAndAfterLeaseExpiry(t *testing.T) {
	fixture, ready, _ := newCleanupExpiryFinalizationFixture(t)
	query, err := ingest.CleanupExpiryFinalizationOutcomeQueryForLedger(fixture.plan, ready)
	if err != nil {
		t.Fatalf("CleanupExpiryFinalizationOutcomeQueryForLedger() = %v", err)
	}

	prestate, err := fixture.store.LoadCurrentCleanupExpiryFinalizationOutcome(
		context.Background(), query,
	)
	if err != nil {
		t.Fatalf("LoadCurrentCleanupExpiryFinalizationOutcome(prestate) = %v", err)
	}
	preOutcome, err := ingest.EvaluateCleanupExpiryFinalizationOutcome(
		query, prestate, prestate.ReadTime,
	)
	if err != nil || preOutcome.CommitStatus != ingest.CleanupExpiryFinalizationNotCommitted {
		t.Fatalf("prestate outcome = %#v, %v", preOutcome, err)
	}

	result, err := fixture.store.finalizeExpiredCleanup(context.Background(), fixture.query)
	if err != nil {
		t.Fatalf("finalizeExpiredCleanup() = %v", err)
	}
	seedCommittedCleanupExpiryFinalization(fixture, result)
	readAfterExpiry := fixture.plan.Target.Command.LeaseExpiresAt.Add(time.Hour)
	fixture.transaction.readTime = readAfterExpiry
	targetPath := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
	target := fixture.transaction.targets[targetPath]
	target.ReadTime = readAfterExpiry
	fixture.transaction.targets[targetPath] = target
	fixture.store.now = func() time.Time { return readAfterExpiry }
	fixture.transaction.updates = nil
	fixture.transaction.updateCalls = 0

	committed, err := fixture.store.LoadCurrentCleanupExpiryFinalizationOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil {
		t.Fatalf("LoadCurrentCleanupExpiryFinalizationOutcome(committed) = %v", err)
	}
	authorizer, err := ingest.NewSystemCleanupExpiryFinalizationOutcomeAuthorizer(
		fixture.store, func() time.Time { return readAfterExpiry },
	)
	if err != nil {
		t.Fatalf("NewSystemCleanupExpiryFinalizationOutcomeAuthorizer() = %v", err)
	}
	grant, err := authorizer.Authorize(context.Background(), result.OutcomeQuery)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	outcome, err := fixture.store.GetCleanupExpiryFinalizationOutcome(
		context.Background(), grant, result.OutcomeQuery, readAfterExpiry,
	)
	if err != nil || outcome.CommitStatus != ingest.CleanupExpiryFinalizationCommitted ||
		outcome.EvidenceHash != result.Ledger.EvidenceHash ||
		!outcome.CompletedAt.Equal(result.Ledger.CompletedAt) ||
		!outcome.PurgeEligibleAt.Equal(*result.Receipt.PurgeEligibleAt) {
		t.Fatalf("committed outcome = %#v, %v; snapshot=%#v", outcome, err, committed)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupExpiryFinalizationOutcomeMarksCorruptTerminalStateUnverifiable(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*cleanupExecutionLedgerStoreFixture, ingest.CleanupExpiryFinalizationResult)
	}{
		{name: "different evidence digest", mutate: func(fixture *cleanupExecutionLedgerStoreFixture, _ ingest.CleanupExpiryFinalizationResult) {
			attempt := fixture.transaction.attempts[fixture.attemptPath]
			attempt.CleanupEvidenceHash = differentCleanupLedgerDigest(attempt.CleanupEvidenceHash)
			fixture.transaction.attempts[fixture.attemptPath] = attempt
		}},
		{name: "empty evidence digest", mutate: func(fixture *cleanupExecutionLedgerStoreFixture, _ ingest.CleanupExpiryFinalizationResult) {
			attempt := fixture.transaction.attempts[fixture.attemptPath]
			attempt.CleanupEvidenceHash = ""
			fixture.transaction.attempts[fixture.attemptPath] = attempt
		}},
		{name: "malformed evidence digest", mutate: func(fixture *cleanupExecutionLedgerStoreFixture, _ ingest.CleanupExpiryFinalizationResult) {
			attempt := fixture.transaction.attempts[fixture.attemptPath]
			attempt.CleanupEvidenceHash = "not-a-digest"
			fixture.transaction.attempts[fixture.attemptPath] = attempt
		}},
		{name: "missing purge time", mutate: func(fixture *cleanupExecutionLedgerStoreFixture, _ ingest.CleanupExpiryFinalizationResult) {
			setCleanupFinalizationLinkedPurge(fixture, nil)
		}},
		{name: "wrong linked purge time", mutate: func(fixture *cleanupExecutionLedgerStoreFixture, result ingest.CleanupExpiryFinalizationResult) {
			wrong := result.Receipt.PurgeEligibleAt.Add(time.Second)
			setCleanupFinalizationLinkedPurge(fixture, &wrong)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, _, _ := newCleanupExpiryFinalizationFixture(t)
			result, err := fixture.store.finalizeExpiredCleanup(context.Background(), fixture.query)
			if err != nil {
				t.Fatalf("finalizeExpiredCleanup() = %v", err)
			}
			seedCommittedCleanupExpiryFinalization(fixture, result)
			test.mutate(fixture, result)
			fixture.transaction.updates = nil
			fixture.transaction.updateCalls = 0

			snapshot, err := fixture.store.LoadCurrentCleanupExpiryFinalizationOutcome(
				context.Background(), result.OutcomeQuery,
			)
			if err != nil {
				t.Fatalf("LoadCurrentCleanupExpiryFinalizationOutcome() = %v", err)
			}
			outcome, err := ingest.EvaluateCleanupExpiryFinalizationOutcome(
				result.OutcomeQuery, snapshot, snapshot.ReadTime,
			)
			if err != nil || outcome.CommitStatus != ingest.CleanupExpiryFinalizationUnverifiable ||
				outcome.EvidenceHash != "" || !outcome.CompletedAt.IsZero() ||
				!outcome.PurgeEligibleAt.IsZero() {
				t.Fatalf("corrupt terminal outcome = %#v, %v", outcome, err)
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestFirestoreCleanupExpiryFinalizationOutcomeRejectsStructurallyInvalidPurge(t *testing.T) {
	fixture, _, _ := newCleanupExpiryFinalizationFixture(t)
	result, err := fixture.store.finalizeExpiredCleanup(context.Background(), fixture.query)
	if err != nil {
		t.Fatalf("finalizeExpiredCleanup() = %v", err)
	}
	seedCommittedCleanupExpiryFinalization(fixture, result)
	invalid := result.Receipt.ReceiptRetentionFloor.Add(-time.Second)
	setCleanupFinalizationLinkedPurge(fixture, &invalid)
	fixture.transaction.updates = nil
	fixture.transaction.updateCalls = 0

	if _, err := fixture.store.LoadCurrentCleanupExpiryFinalizationOutcome(
		context.Background(), result.OutcomeQuery,
	); !errors.Is(err, ingest.ErrCleanupExpiryFinalizationOutcomeUnavailable) {
		t.Fatalf("LoadCurrentCleanupExpiryFinalizationOutcome() error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func newCleanupExpiryFinalizationFixture(
	t *testing.T,
) (*cleanupExecutionLedgerStoreFixture, ingest.CleanupExecutionLedger, time.Time) {
	t.Helper()
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ready := fixture.ledger
	base := fixture.plan.Target.Command.CreatedAt
	steps := []ingest.CleanupExecutionTransition{
		{Phase: ingest.CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: base.Add(time.Second)},
		{Phase: ingest.CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: ingest.CleanupDeleteObserved, ObservedAt: base.Add(2 * time.Second)},
		{Phase: ingest.CleanupExecutionPhaseRawAbsenceConfirmed, AuditOutcome: ingest.CleanupAuditConfirmedAbsent, ObservedAt: base.Add(3 * time.Second)},
		{Phase: ingest.CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: base.Add(4 * time.Second)},
		{Phase: ingest.CleanupExecutionPhaseManifestOutcomeRecorded, DeleteOutcome: ingest.CleanupDeleteNotFound, ObservedAt: base.Add(5 * time.Second)},
		{Phase: ingest.CleanupExecutionPhaseManifestAbsenceConfirmed, AuditOutcome: ingest.CleanupAuditConfirmedAbsent, ObservedAt: base.Add(6 * time.Second)},
	}
	var err error
	for _, step := range steps {
		ready, err = ingest.AdvanceCleanupExecutionLedger(fixture.plan, ready, step)
		if err != nil {
			t.Fatalf("AdvanceCleanupExecutionLedger(%q) = %v", step.Phase, err)
		}
	}
	fixture.seedLedger(ready)
	completedAt := base.Add(7 * time.Second)
	fixture.store.now = func() time.Time { return completedAt }
	fixture.transaction.readTime = completedAt
	targetPath := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
	target := fixture.transaction.targets[targetPath]
	target.ReadTime = completedAt
	fixture.transaction.targets[targetPath] = target
	fixture.transaction.updates = nil
	fixture.transaction.updateCalls = 0
	fixture.store.cleanupExpiryFinalizationContext = func(
		parent context.Context,
		_ time.Time,
	) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}
	return fixture, ready, completedAt
}

func seedCommittedCleanupExpiryFinalization(
	fixture *cleanupExecutionLedgerStoreFixture,
	result ingest.CleanupExpiryFinalizationResult,
) {
	receipt := fixture.transaction.receipts[admissionReceiptPath()]
	receipt.State = ingest.ReceiptExpired
	receipt.Revision = result.Receipt.Revision
	receipt.UpdatedAt = result.Receipt.UpdatedAt
	receipt.PurgeEligibleAt = cloneOptionalTime(result.Receipt.PurgeEligibleAt)
	receipt.clearLease()
	fixture.transaction.receipts[admissionReceiptPath()] = receipt
	idempotencyPath := idempotencyDocumentPath(fixture.query.TenantID, fixture.query.ReservationKey)
	index := fixture.transaction.indexes[idempotencyPath]
	index.PurgeEligibleAt = cloneOptionalTime(result.Receipt.PurgeEligibleAt)
	fixture.transaction.indexes[idempotencyPath] = index
	clientBatchPath := clientBatchDocumentPath(fixture.query.TenantID, index.ClientBatchKey)
	fixture.transaction.indexes[clientBatchPath] = index
	attempt := fixture.transaction.attempts[fixture.attemptPath]
	attempt = attemptWithCleanupExecutionLedger(attempt, result.Ledger)
	attempt.Status = ingest.RecoveryAttemptCompleted
	attempt.Outcome = ingest.RecoveryAttemptOutcomeExpired
	fixture.transaction.attempts[fixture.attemptPath] = attempt
}

func setCleanupFinalizationLinkedPurge(
	fixture *cleanupExecutionLedgerStoreFixture,
	value *time.Time,
) {
	receipt := fixture.transaction.receipts[admissionReceiptPath()]
	receipt.PurgeEligibleAt = cloneOptionalTime(value)
	fixture.transaction.receipts[admissionReceiptPath()] = receipt
	idempotencyPath := idempotencyDocumentPath(fixture.query.TenantID, fixture.query.ReservationKey)
	index := fixture.transaction.indexes[idempotencyPath]
	index.PurgeEligibleAt = cloneOptionalTime(value)
	fixture.transaction.indexes[idempotencyPath] = index
	fixture.transaction.indexes[clientBatchDocumentPath(
		fixture.query.TenantID, index.ClientBatchKey,
	)] = index
}

var _ = firestore.Delete
