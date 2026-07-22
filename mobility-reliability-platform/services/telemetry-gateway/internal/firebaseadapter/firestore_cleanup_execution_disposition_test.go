package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreCleanupExecutionDispositionBuildsAtomicTwoDocumentMutation(t *testing.T) {
	tests := []struct {
		name         string
		errorClass   ingest.CleanupExecutionErrorClass
		disposition  ingest.CleanupExecutionDisposition
		attemptValue ingest.RecoveryAttemptOutcome
	}{
		{
			name: "retry", errorClass: ingest.CleanupExecutionErrorQuotaLimited,
			disposition:  ingest.CleanupExecutionDispositionRetry,
			attemptValue: ingest.RecoveryAttemptOutcomeCleanupRetry,
		},
		{
			name: "hold", errorClass: ingest.CleanupExecutionErrorPermissionDenied,
			disposition:  ingest.CleanupExecutionDispositionHold,
			attemptValue: ingest.RecoveryAttemptOutcomeCleanupHold,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCleanupExecutionLedgerStoreFixture(t)
			ledger := cleanupTakeoverLedgerAtPhase(
				t, fixture, ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
			)
			fixture.seedLedger(ledger)
			command, err := ingest.BuildCleanupExecutionDispositionCommand(
				fixture.plan, ledger, test.errorClass,
			)
			if err != nil {
				t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
			}
			result, err := fixture.store.disposeCleanupExecution(context.Background(), command)
			if err != nil {
				t.Fatalf("disposeCleanupExecution() = %v", err)
			}
			if result.Ledger.Phase != ledger.Phase || result.Ledger.Revision != ledger.Revision ||
				result.Ledger.Disposition != test.disposition ||
				result.Receipt.State != ingest.ReceiptCleanupPending ||
				result.Receipt.LeaseOwnerID != "" ||
				result.Receipt.CleanupDispositionAttemptID != fixture.query.AttemptID ||
				result.Receipt.CleanupControlDisposition != test.disposition ||
				result.Receipt.LastCleanupErrorClass != test.errorClass ||
				ingest.ValidateCleanupExecutionDispositionOutcomeQuery(result.OutcomeQuery) != nil {
				t.Fatalf("disposition result = %#v", result)
			}
			if test.disposition == ingest.CleanupExecutionDispositionRetry {
				if result.NextCleanupAt.IsZero() || !result.HoldReviewDueAt.IsZero() ||
					!result.Receipt.NextCleanupAt.Equal(result.NextCleanupAt) {
					t.Fatalf("retry cursor = %#v", result)
				}
			} else if !result.NextCleanupAt.IsZero() || result.HoldReviewDueAt.IsZero() ||
				!result.Receipt.CleanupHoldReviewDueAt.Equal(result.HoldReviewDueAt) {
				t.Fatalf("hold cursor = %#v", result)
			}
			if len(fixture.transaction.updates) != 2 || len(fixture.transaction.creates) != 0 {
				t.Fatalf("updates/creates = %d/%d", len(fixture.transaction.updates), len(fixture.transaction.creates))
			}
			updatesByPath := make(map[string]map[string]any, 2)
			for _, update := range fixture.transaction.updates {
				updatesByPath[update.path] = cleanupExecutionUpdateValues(update.updates)
			}
			attemptValues := updatesByPath[fixture.attemptPath]
			if attemptValues["status"] != string(ingest.RecoveryAttemptCompleted) ||
				attemptValues["outcome"] != string(test.attemptValue) ||
				attemptValues["cleanup_phase"] != string(ledger.Phase) ||
				attemptValues["cleanup_execution_revision"] != ledger.Revision ||
				attemptValues["cleanup_evidence_hash"] != result.Ledger.EvidenceHash {
				t.Fatalf("attempt updates = %#v", attemptValues)
			}
			receiptValues := updatesByPath[admissionReceiptPath()]
			if receiptValues["revision"] != result.Receipt.Revision ||
				receiptValues["cleanup_disposition_attempt_id"] != fixture.query.AttemptID ||
				receiptValues["cleanup_control_disposition"] != string(test.disposition) ||
				receiptValues["last_cleanup_error_class"] != string(test.errorClass) {
				t.Fatalf("receipt updates = %#v", receiptValues)
			}
			for _, path := range []string{
				cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID),
				idempotencyDocumentPath(fixture.query.TenantID, fixture.query.ReservationKey),
				clientBatchDocumentPath(
					fixture.query.TenantID,
					fixture.transaction.indexes[admissionIdempotencyPath()].ClientBatchKey,
				),
			} {
				if _, mutated := updatesByPath[path]; mutated {
					t.Fatalf("immutable document %q was mutated", path)
				}
			}
		})
	}
}

func TestFirestoreCleanupExecutionDispositionPreservesCorrelationQueryOnWriteError(t *testing.T) {
	for failUpdateAt := 1; failUpdateAt <= 2; failUpdateAt++ {
		t.Run(string(rune('0'+failUpdateAt)), func(t *testing.T) {
			fixture := newCleanupExecutionLedgerStoreFixture(t)
			ledger := cleanupTakeoverLedgerAtPhase(
				t, fixture, ingest.CleanupExecutionPhaseRawDispatchRecorded,
			)
			fixture.seedLedger(ledger)
			command, err := ingest.BuildCleanupExecutionDispositionCommand(
				fixture.plan, ledger, ingest.CleanupExecutionErrorProviderUnavailable,
			)
			if err != nil {
				t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
			}
			fixture.transaction.updateErr = errors.New("commit response unavailable")
			fixture.transaction.failUpdateAt = failUpdateAt
			fixture.store.cleanupExecutionDispositionContext = func(
				parent context.Context,
				_ time.Time,
			) (context.Context, context.CancelFunc) {
				return context.WithCancel(parent)
			}
			result, err := fixture.store.DisposeCleanupExecution(context.Background(), command)
			if !errors.Is(err, ingest.ErrCleanupExecutionDispositionUnavailable) {
				t.Fatalf("DisposeCleanupExecution() error = %v", err)
			}
			if ingest.ValidateCleanupExecutionDispositionOutcomeQuery(result.OutcomeQuery) != nil ||
				result.Ledger.Disposition != ingest.CleanupExecutionDispositionRetry ||
				result.Receipt.CleanupDispositionAttemptID != fixture.query.AttemptID {
				t.Fatalf("response-loss result = %#v", result)
			}
		})
	}
}

func TestFirestoreCleanupExecutionDispositionPreservesFirstCorrelationQueryWhenRetryDrifts(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseRawDispatchRecorded,
	)
	fixture.seedLedger(ledger)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, ledger, ingest.CleanupExecutionErrorProviderUnavailable,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	expectedQuery, err := ingest.CleanupExecutionDispositionOutcomeQueryForLedger(
		fixture.plan, ledger, command.ErrorClass,
	)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionOutcomeQueryForLedger() = %v", err)
	}
	fixture.store.cleanupExecutionDispositionContext = func(
		parent context.Context,
		_ time.Time,
	) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}
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

	result, err := fixture.store.DisposeCleanupExecution(context.Background(), command)
	if !errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("DisposeCleanupExecution() error = %v", err)
	}
	if result.OutcomeQuery != expectedQuery ||
		ingest.ValidateCleanupExecutionDispositionOutcomeQuery(result.OutcomeQuery) != nil ||
		result.Ledger.Phase != ledger.Phase || result.Ledger.Revision != ledger.Revision ||
		result.Ledger.Disposition != ingest.CleanupExecutionDispositionRetry ||
		result.Receipt.CleanupDispositionAttemptID != fixture.query.AttemptID {
		t.Fatalf("retry response-loss result = %#v", result)
	}
}

func TestFirestoreCleanupExecutionDispositionPreservesQueryWhenFenceDeadlineLosesCommitResponse(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
	)
	fixture.seedLedger(ledger)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, ledger, ingest.CleanupExecutionErrorProviderTimeout,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	expectedQuery, err := ingest.CleanupExecutionDispositionOutcomeQueryForLedger(
		fixture.plan, ledger, command.ErrorClass,
	)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionOutcomeQueryForLedger() = %v", err)
	}
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
	fixture.store.cleanupExecutionDispositionContext = func(
		parent context.Context,
		_ time.Time,
	) (context.Context, context.CancelFunc) {
		return context.WithDeadline(parent, time.Now().Add(-time.Second))
	}

	result, err := fixture.store.DisposeCleanupExecution(context.Background(), command)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		t.Fatalf("DisposeCleanupExecution() error = %v", err)
	}
	if fixture.transaction.updateCalls != 2 || result.OutcomeQuery != expectedQuery ||
		ingest.ValidateCleanupExecutionDispositionOutcomeQuery(result.OutcomeQuery) != nil ||
		result.Ledger.Phase != ledger.Phase || result.Ledger.Revision != ledger.Revision ||
		result.Ledger.Disposition != ingest.CleanupExecutionDispositionRetry ||
		result.Receipt.CleanupDispositionAttemptID != fixture.query.AttemptID {
		t.Fatalf("deadline response-loss result = %#v", result)
	}
}

func TestFirestoreCleanupExecutionDispositionRejectsDriftWithoutWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*cleanupExecutionLedgerStoreFixture, *ingest.CleanupExecutionDispositionCommand)
	}{
		{
			name: "target hash drift",
			mutate: func(_ *cleanupExecutionLedgerStoreFixture, command *ingest.CleanupExecutionDispositionCommand) {
				command.ExpectedTargetHash = differentCleanupLedgerDigest(command.ExpectedTargetHash)
			},
		},
		{
			name: "receipt revision drift",
			mutate: func(fixture *cleanupExecutionLedgerStoreFixture, _ *ingest.CleanupExecutionDispositionCommand) {
				receipt := fixture.transaction.receipts[admissionReceiptPath()]
				receipt.Revision++
				fixture.transaction.receipts[admissionReceiptPath()] = receipt
			},
		},
		{
			name: "ledger revision drift",
			mutate: func(_ *cleanupExecutionLedgerStoreFixture, command *ingest.CleanupExecutionDispositionCommand) {
				command.ExpectedLedgerRevision--
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCleanupExecutionLedgerStoreFixture(t)
			ledger := cleanupTakeoverLedgerAtPhase(
				t, fixture, ingest.CleanupExecutionPhaseManifestDispatchRecorded,
			)
			fixture.seedLedger(ledger)
			command, err := ingest.BuildCleanupExecutionDispositionCommand(
				fixture.plan, ledger, ingest.CleanupExecutionErrorLineageMismatch,
			)
			if err != nil {
				t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
			}
			test.mutate(fixture, &command)
			if _, err := fixture.store.disposeCleanupExecution(
				context.Background(), command,
			); err == nil {
				t.Fatal("drifted disposition was accepted")
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestFirestoreCleanupExecutionDispositionOutcomeCorrelatesPrestateAndCommit(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
	)
	fixture.seedLedger(ledger)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, ledger, ingest.CleanupExecutionErrorProviderUnavailable,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	query, err := ingest.CleanupExecutionDispositionOutcomeQueryForLedger(
		fixture.plan, ledger, command.ErrorClass,
	)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionOutcomeQueryForLedger() = %v", err)
	}
	prestate, err := fixture.store.LoadCurrentCleanupExecutionDispositionOutcome(
		context.Background(), query,
	)
	if err != nil {
		t.Fatalf("LoadCurrentCleanupExecutionDispositionOutcome(prestate) = %v", err)
	}
	preOutcome, err := ingest.EvaluateCleanupExecutionDispositionOutcome(
		query, prestate, prestate.ReadTime,
	)
	if err != nil || preOutcome.CommitStatus != ingest.CleanupExecutionDispositionNotCommitted {
		t.Fatalf("prestate outcome = %#v, %v", preOutcome, err)
	}

	result, err := fixture.store.disposeCleanupExecution(context.Background(), command)
	if err != nil {
		t.Fatalf("disposeCleanupExecution() = %v", err)
	}
	seedCommittedCleanupExecutionDisposition(fixture, result)
	fixture.transaction.updates = nil
	fixture.transaction.updateCalls = 0
	committed, err := fixture.store.LoadCurrentCleanupExecutionDispositionOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil {
		t.Fatalf("LoadCurrentCleanupExecutionDispositionOutcome(committed) = %v", err)
	}
	outcome, err := ingest.EvaluateCleanupExecutionDispositionOutcome(
		result.OutcomeQuery, committed, committed.ReadTime,
	)
	if err != nil || outcome.CommitStatus != ingest.CleanupExecutionDispositionCommitted ||
		outcome.Disposition != ingest.CleanupExecutionDispositionRetry ||
		outcome.ErrorClass != command.ErrorClass ||
		outcome.EvidenceHash != result.Ledger.EvidenceHash ||
		!outcome.CompletedAt.Equal(result.Ledger.CompletedAt) ||
		!outcome.NextCleanupAt.Equal(result.NextCleanupAt) || !outcome.HoldReviewDueAt.IsZero() {
		t.Fatalf("committed outcome = %#v, %v", outcome, err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupExecutionDispositionOutcomeMarksSemanticMismatchUnverifiable(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseRawDispatchRecorded,
	)
	fixture.seedLedger(ledger)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, ledger, ingest.CleanupExecutionErrorProviderTimeout,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	result, err := fixture.store.disposeCleanupExecution(context.Background(), command)
	if err != nil {
		t.Fatalf("disposeCleanupExecution() = %v", err)
	}
	seedCommittedCleanupExecutionDisposition(fixture, result)
	receipt := fixture.transaction.receipts[admissionReceiptPath()]
	receipt.LastCleanupErrorClass = ingest.CleanupExecutionErrorProviderUnavailable
	fixture.transaction.receipts[admissionReceiptPath()] = receipt
	fixture.transaction.updates = nil
	fixture.transaction.updateCalls = 0

	snapshot, err := fixture.store.LoadCurrentCleanupExecutionDispositionOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil {
		t.Fatalf("LoadCurrentCleanupExecutionDispositionOutcome() = %v", err)
	}
	outcome, err := ingest.EvaluateCleanupExecutionDispositionOutcome(
		result.OutcomeQuery, snapshot, snapshot.ReadTime,
	)
	if err != nil || outcome.CommitStatus != ingest.CleanupExecutionDispositionUnverifiable ||
		outcome.EvidenceHash != "" || !outcome.CompletedAt.IsZero() {
		t.Fatalf("semantic mismatch outcome = %#v, %v", outcome, err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupExecutionDispositionOutcomeMarksFenceMismatchUnverifiable(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseRawOutcomeRecorded,
	)
	fixture.seedLedger(ledger)
	query, err := ingest.CleanupExecutionDispositionOutcomeQueryForLedger(
		fixture.plan, ledger, ingest.CleanupExecutionErrorProviderTimeout,
	)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionOutcomeQueryForLedger() = %v", err)
	}
	attemptPath := recoveryAttemptDocumentPath(
		fixture.query.TenantID, fixture.plan.Target.Command.ReceiptID, fixture.query.AttemptID,
	)
	attempt := fixture.transaction.attempts[attemptPath]
	attempt.FencingToken++
	fixture.transaction.attempts[attemptPath] = attempt

	snapshot, err := fixture.store.LoadCurrentCleanupExecutionDispositionOutcome(
		context.Background(), query,
	)
	if err != nil {
		t.Fatalf("LoadCurrentCleanupExecutionDispositionOutcome() = %v", err)
	}
	outcome, err := ingest.EvaluateCleanupExecutionDispositionOutcome(
		query, snapshot, snapshot.ReadTime,
	)
	if err != nil || outcome.CommitStatus != ingest.CleanupExecutionDispositionUnverifiable ||
		outcome.EvidenceHash != "" || !outcome.CompletedAt.IsZero() {
		t.Fatalf("fence mismatch outcome = %#v, %v", outcome, err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupExecutionDispositionOutcomeRequiresStructuralDocuments(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseManifestDispatchRecorded,
	)
	fixture.seedLedger(ledger)
	query, err := ingest.CleanupExecutionDispositionOutcomeQueryForLedger(
		fixture.plan, ledger, ingest.CleanupExecutionErrorPermissionDenied,
	)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionOutcomeQueryForLedger() = %v", err)
	}
	delete(
		fixture.transaction.targets,
		cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID),
	)
	if _, err := fixture.store.LoadCurrentCleanupExecutionDispositionOutcome(
		context.Background(), query,
	); !errors.Is(err, ingest.ErrCleanupExecutionDispositionOutcomeUnavailable) {
		t.Fatalf("missing target error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupRetryClaimWaitsThenCreatesPristineAttempt(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseRawDispatchRecorded,
	)
	fixture.seedLedger(ledger)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, ledger, ingest.CleanupExecutionErrorProviderTimeout,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	result, err := fixture.store.disposeCleanupExecution(context.Background(), command)
	if err != nil {
		t.Fatalf("disposeCleanupExecution() = %v", err)
	}
	seedCommittedCleanupExecutionDisposition(fixture, result)
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	setCleanupDispositionFixtureReadTime(fixture, result.NextCleanupAt.Add(-time.Nanosecond))
	fixture.transaction.updates = nil
	fixture.transaction.creates = nil
	grant, status, err := fixture.store.ClaimCleanupLease(
		context.Background(), fixture.query.TenantID, fixture.query.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup}, proposal,
		result.NextCleanupAt.Add(-time.Nanosecond), ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusNotDue || grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("pre-boundary claim = %#v, %q, %v", grant, status, err)
	}
	fixture.assertNoWrites(t)

	setCleanupDispositionFixtureReadTime(fixture, result.NextCleanupAt)
	grant, status, err = fixture.store.ClaimCleanupLease(
		context.Background(), fixture.query.TenantID, fixture.query.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup}, proposal,
		result.NextCleanupAt, ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired ||
		ingest.ValidateCleanupLeaseGrant(grant) != nil {
		t.Fatalf("boundary claim = %#v, %q, %v", grant, status, err)
	}
	if len(fixture.transaction.updates) != 1 || len(fixture.transaction.creates) != 1 {
		t.Fatalf("retry claim updates/creates = %d/%d", len(fixture.transaction.updates), len(fixture.transaction.creates))
	}
	values := cleanupExecutionUpdateValues(fixture.transaction.updates[0].updates)
	for _, field := range []string{
		"cleanup_disposition_attempt_id", "cleanup_control_disposition",
		"last_cleanup_error_class", "next_cleanup_at", "cleanup_hold_review_due_at",
	} {
		if values[field] != firestore.Delete {
			t.Fatalf("retry claim did not clear %q: %#v", field, values)
		}
	}
	newAttemptPath := recoveryAttemptDocumentPath(
		fixture.query.TenantID, result.Receipt.ReceiptID, proposal.ID,
	)
	var createdValue any
	for _, create := range fixture.transaction.creates {
		if create.path == newAttemptPath {
			createdValue = create.value
		}
	}
	created, ok := createdValue.(firestoreRecoveryAttempt)
	if !ok || created.Status != ingest.RecoveryAttemptStarted || created.Outcome != "" ||
		created.DecisionDomain != "" || hasCleanupExecutionLedgerResidue(created) ||
		!created.CompletedAt.IsZero() || created.FailureCode != "" {
		t.Fatalf("new retry attempt inherited old state: %#v", created)
	}
}

func TestFirestoreCleanupHoldNeverAutoClaimsAfterReviewDue(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseManifestDispatchRecorded,
	)
	fixture.seedLedger(ledger)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, ledger, ingest.CleanupExecutionErrorPermissionDenied,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	result, err := fixture.store.disposeCleanupExecution(context.Background(), command)
	if err != nil {
		t.Fatalf("disposeCleanupExecution() = %v", err)
	}
	seedCommittedCleanupExecutionDisposition(fixture, result)
	requestedAt := result.HoldReviewDueAt.Add(24 * time.Hour)
	setCleanupDispositionFixtureReadTime(fixture, requestedAt)
	fixture.transaction.updates = nil
	fixture.transaction.creates = nil
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	grant, status, err := fixture.store.ClaimCleanupLease(
		context.Background(), fixture.query.TenantID, fixture.query.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup}, proposal,
		requestedAt, ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusNotEligible ||
		grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("hold auto-claim = %#v, %q, %v", grant, status, err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupRetryClaimRejectsTamperedTerminalEvidence(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
	)
	fixture.seedLedger(ledger)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, ledger, ingest.CleanupExecutionErrorInventoryIncomplete,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	result, err := fixture.store.disposeCleanupExecution(context.Background(), command)
	if err != nil {
		t.Fatalf("disposeCleanupExecution() = %v", err)
	}
	seedCommittedCleanupExecutionDisposition(fixture, result)
	attempt := fixture.transaction.attempts[fixture.attemptPath]
	attempt.CleanupEvidenceHash = differentCleanupLedgerDigest(attempt.CleanupEvidenceHash)
	fixture.transaction.attempts[fixture.attemptPath] = attempt
	setCleanupDispositionFixtureReadTime(fixture, result.NextCleanupAt)
	fixture.transaction.updates = nil
	fixture.transaction.creates = nil
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	if _, _, err := fixture.store.ClaimCleanupLease(
		context.Background(), fixture.query.TenantID, fixture.query.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup}, proposal,
		result.NextCleanupAt, ingest.DefaultRequestLeaseDuration,
	); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("tampered retry claim error = %v", err)
	}
	fixture.assertNoWrites(t)
}

func TestCleanupExecutionDispositionHistoricalCodecUsesCompletedAt(t *testing.T) {
	fixture := newCleanupExecutionLedgerStoreFixture(t)
	ledger := cleanupTakeoverLedgerAtPhase(
		t, fixture, ingest.CleanupExecutionPhaseManifestOutcomeRecorded,
	)
	completedAt := fixture.store.now().UTC()
	terminal, _, err := ingest.CompleteCleanupExecutionDisposition(
		fixture.plan, ledger, ingest.CleanupExecutionErrorPermissionDenied, completedAt,
	)
	if err != nil {
		t.Fatalf("CompleteCleanupExecutionDisposition() = %v", err)
	}
	attempt := attemptWithCleanupExecutionLedger(
		fixture.transaction.attempts[fixture.attemptPath], terminal,
	)
	attempt.Status = ingest.RecoveryAttemptCompleted
	attempt.Outcome = ingest.RecoveryAttemptOutcomeCleanupHold
	decoded, present, err := decodeHistoricalCleanupExecutionLedger(fixture.plan, attempt)
	if err != nil || !present || decoded.EvidenceHash != terminal.EvidenceHash ||
		!cleanupExecutionAttemptPersistedAt(fixture.plan, attempt).Equal(completedAt) {
		t.Fatalf("historical disposition decode = %#v, %t, %v", decoded, present, err)
	}

	attempt.CompletedAt = time.Time{}
	if _, _, err := decodeHistoricalCleanupExecutionLedger(
		fixture.plan, attempt,
	); !errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) {
		t.Fatalf("missing terminal completed_at error = %v", err)
	}
}

func seedCommittedCleanupExecutionDisposition(
	fixture *cleanupExecutionLedgerStoreFixture,
	result ingest.CleanupExecutionDispositionResult,
) {
	receipt := fixture.transaction.receipts[admissionReceiptPath()]
	receipt.Revision = result.Receipt.Revision
	receipt.UpdatedAt = result.Ledger.CompletedAt
	receipt.clearLease()
	receipt.NextRecoveryAt = time.Time{}
	receipt.LastRecoveryCode = ""
	receipt.CleanupDispositionAttemptID = result.Receipt.CleanupDispositionAttemptID
	receipt.CleanupControlDisposition = result.Receipt.CleanupControlDisposition
	receipt.LastCleanupErrorClass = result.Receipt.LastCleanupErrorClass
	receipt.NextCleanupAt = result.NextCleanupAt
	receipt.CleanupHoldReviewDueAt = result.HoldReviewDueAt
	fixture.transaction.receipts[admissionReceiptPath()] = receipt

	attempt := fixture.transaction.attempts[fixture.attemptPath]
	attempt = attemptWithCleanupExecutionLedger(attempt, result.Ledger)
	attempt.Status = ingest.RecoveryAttemptCompleted
	attempt.Outcome = cleanupExecutionDispositionAttemptOutcome(result.Ledger.Disposition)
	fixture.transaction.attempts[fixture.attemptPath] = attempt
}

func setCleanupDispositionFixtureReadTime(
	fixture *cleanupExecutionLedgerStoreFixture,
	readTime time.Time,
) {
	readTime = readTime.UTC()
	fixture.transaction.readTime = readTime
	fixture.store.now = func() time.Time { return readTime }
	targetPath := cleanupTargetDocumentPath(fixture.query.TenantID, fixture.query.AttemptID)
	target := fixture.transaction.targets[targetPath]
	target.ReadTime = readTime
	fixture.transaction.targets[targetPath] = target
}
