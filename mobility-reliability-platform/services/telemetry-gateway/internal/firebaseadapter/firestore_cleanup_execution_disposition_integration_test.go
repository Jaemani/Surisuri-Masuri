package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorDisposesCleanupAtomicallyAndCorrelatesReadOnly(t *testing.T) {
	fixture := seedCleanupExecutionDispositionEmulatorFixture(t)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, fixture.ready, ingest.CleanupExecutionErrorPermissionDenied,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	before := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	result, err := fixture.store.DisposeCleanupExecution(context.Background(), command)
	if err != nil {
		t.Fatalf("DisposeCleanupExecution() = %v", err)
	}
	if result.Receipt.State != ingest.ReceiptCleanupPending ||
		result.Receipt.Revision != fixture.receipt.Revision+1 ||
		result.Ledger.Phase != fixture.ready.Phase ||
		result.Ledger.Revision != fixture.ready.Revision ||
		result.Ledger.Disposition != ingest.CleanupExecutionDispositionHold ||
		!result.NextCleanupAt.IsZero() || result.HoldReviewDueAt.IsZero() {
		t.Fatalf("disposition result = %#v", result)
	}

	after := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	for _, path := range []string{
		fixture.targetPath, fixture.idempotencyPath, fixture.clientBatchPath,
	} {
		if !sameCleanupExpiryEmulatorDocument(before[path], after[path]) {
			t.Fatalf("disposition mutated immutable document %q", path)
		}
	}
	if !after[fixture.attemptPath].updateTime.After(before[fixture.attemptPath].updateTime) ||
		!after[fixture.receiptPath].updateTime.After(before[fixture.receiptPath].updateTime) ||
		!after[fixture.attemptPath].updateTime.Equal(after[fixture.receiptPath].updateTime) {
		t.Fatal("attempt and receipt were not one two-document commit")
	}
	attempt := readAdmissionEmulatorAttempt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID, fixture.query.AttemptID,
	)
	receipt := readAdmissionEmulatorReceipt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID,
	)
	if attempt.Status != ingest.RecoveryAttemptCompleted ||
		attempt.Outcome != ingest.RecoveryAttemptOutcomeCleanupHold ||
		attempt.CleanupPhase != fixture.ready.Phase ||
		attempt.CleanupExecutionRevision != fixture.ready.Revision ||
		attempt.CleanupEvidenceHash != result.Ledger.EvidenceHash ||
		receipt.CleanupDispositionAttemptID != fixture.query.AttemptID ||
		receipt.CleanupControlDisposition != ingest.CleanupExecutionDispositionHold ||
		receipt.LastCleanupErrorClass != ingest.CleanupExecutionErrorPermissionDenied ||
		!receipt.CleanupHoldReviewDueAt.Equal(result.HoldReviewDueAt) ||
		!receipt.NextCleanupAt.IsZero() {
		t.Fatalf("persisted disposition attempt=%#v receipt=%#v", attempt, receipt)
	}

	beforeCorrelation := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	authorizer, err := ingest.NewSystemCleanupExecutionDispositionOutcomeAuthorizer(
		fixture.store, func() time.Time { return result.Ledger.CompletedAt },
	)
	if err != nil {
		t.Fatalf("NewSystemCleanupExecutionDispositionOutcomeAuthorizer() = %v", err)
	}
	grant, err := authorizer.Authorize(context.Background(), result.OutcomeQuery)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	outcome, err := fixture.store.GetCleanupExecutionDispositionOutcome(
		context.Background(), grant, result.OutcomeQuery, time.Now().UTC().Add(time.Second),
	)
	if err != nil || outcome.CommitStatus != ingest.CleanupExecutionDispositionCommitted ||
		outcome.EvidenceHash != result.Ledger.EvidenceHash ||
		!outcome.HoldReviewDueAt.Equal(result.HoldReviewDueAt) {
		t.Fatalf("GetCleanupExecutionDispositionOutcome() = %#v, %v", outcome, err)
	}
	afterCorrelation := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	assertCleanupExpiryEmulatorDocumentsUnchanged(t, beforeCorrelation, afterCorrelation)
}

func TestFirestoreAdmissionStoreEmulatorCorrelatesCommittedDispositionAfterResponseLoss(t *testing.T) {
	fixture := seedCleanupExecutionDispositionEmulatorFixture(t)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, fixture.ready, ingest.CleanupExecutionErrorProviderUnavailable,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	expectedQuery, err := ingest.CleanupExecutionDispositionOutcomeQueryForLedger(
		fixture.plan, fixture.ready, command.ErrorClass,
	)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionOutcomeQueryForLedger() = %v", err)
	}
	before := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	originalRunner := fixture.store.runTransaction
	transactionCalls := 0
	fixture.store.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		transactionCalls++
		if err := originalRunner(ctx, operation); err != nil {
			return err
		}
		if transactionCalls == 2 {
			return errors.New("commit response unavailable")
		}
		return nil
	}

	result, err := fixture.store.DisposeCleanupExecution(context.Background(), command)
	if !errors.Is(err, ingest.ErrCleanupExecutionDispositionUnavailable) {
		t.Fatalf("DisposeCleanupExecution() error = %v", err)
	}
	if result.OutcomeQuery != expectedQuery ||
		ingest.ValidateCleanupExecutionDispositionOutcomeQuery(result.OutcomeQuery) != nil ||
		result.Ledger.Disposition != ingest.CleanupExecutionDispositionRetry ||
		result.Receipt.CleanupDispositionAttemptID != fixture.query.AttemptID {
		t.Fatalf("response-loss result = %#v", result)
	}
	afterCommit := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	for _, path := range []string{
		fixture.targetPath, fixture.idempotencyPath, fixture.clientBatchPath,
	} {
		if !sameCleanupExpiryEmulatorDocument(before[path], afterCommit[path]) {
			t.Fatalf("response-loss commit mutated immutable document %q", path)
		}
	}
	if !afterCommit[fixture.attemptPath].updateTime.After(before[fixture.attemptPath].updateTime) ||
		!afterCommit[fixture.receiptPath].updateTime.After(before[fixture.receiptPath].updateTime) ||
		!afterCommit[fixture.attemptPath].updateTime.Equal(afterCommit[fixture.receiptPath].updateTime) {
		t.Fatal("response-loss disposition was not durably committed atomically")
	}

	beforeCorrelation := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	authorizer, err := ingest.NewSystemCleanupExecutionDispositionOutcomeAuthorizer(
		fixture.store, func() time.Time { return result.Ledger.CompletedAt },
	)
	if err != nil {
		t.Fatalf("NewSystemCleanupExecutionDispositionOutcomeAuthorizer() = %v", err)
	}
	grant, err := authorizer.Authorize(context.Background(), result.OutcomeQuery)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	outcome, err := fixture.store.GetCleanupExecutionDispositionOutcome(
		context.Background(), grant, result.OutcomeQuery, time.Now().UTC().Add(time.Second),
	)
	if err != nil || outcome.CommitStatus != ingest.CleanupExecutionDispositionCommitted ||
		outcome.Disposition != ingest.CleanupExecutionDispositionRetry ||
		outcome.ErrorClass != command.ErrorClass ||
		outcome.EvidenceHash != result.Ledger.EvidenceHash ||
		!outcome.NextCleanupAt.Equal(result.NextCleanupAt) {
		t.Fatalf("GetCleanupExecutionDispositionOutcome() = %#v, %v", outcome, err)
	}
	afterCorrelation := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	assertCleanupExpiryEmulatorDocumentsUnchanged(t, beforeCorrelation, afterCorrelation)
}

func TestFirestoreAdmissionStoreEmulatorCompetingCleanupDispositionsHaveOneWinner(t *testing.T) {
	fixture := seedCleanupExecutionDispositionEmulatorFixture(t)
	before := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	commands := make([]ingest.CleanupExecutionDispositionCommand, 0, 2)
	for _, errorClass := range []ingest.CleanupExecutionErrorClass{
		ingest.CleanupExecutionErrorProviderTimeout,
		ingest.CleanupExecutionErrorPermissionDenied,
	} {
		command, err := ingest.BuildCleanupExecutionDispositionCommand(
			fixture.plan, fixture.ready, errorClass,
		)
		if err != nil {
			t.Fatalf("BuildCleanupExecutionDispositionCommand(%q) = %v", errorClass, err)
		}
		commands = append(commands, command)
	}
	type dispositionCall struct {
		result ingest.CleanupExecutionDispositionResult
		err    error
	}
	results := make(chan dispositionCall, len(commands))
	for _, command := range commands {
		command := command
		go func() {
			result, callErr := fixture.store.DisposeCleanupExecution(context.Background(), command)
			results <- dispositionCall{result: result, err: callErr}
		}()
	}
	winners := 0
	losers := 0
	var winner ingest.CleanupExecutionDispositionResult
	for range commands {
		call := <-results
		if call.err == nil {
			winners++
			winner = call.result
			continue
		}
		losers++
		if !errors.Is(call.err, ingest.ErrCleanupExecutionDispositionConflict) &&
			!errors.Is(call.err, ingest.ErrCleanupExecutionDispositionUnavailable) &&
			!errors.Is(call.err, ingest.ErrInvalidCleanupExecutionLedger) {
			t.Fatalf("losing disposition error = %v", call.err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("competing disposition winners/losers = %d/%d", winners, losers)
	}
	after := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	for _, path := range []string{
		fixture.targetPath, fixture.idempotencyPath, fixture.clientBatchPath,
	} {
		if !sameCleanupExpiryEmulatorDocument(before[path], after[path]) {
			t.Fatalf("competing disposition mutated immutable document %q", path)
		}
	}
	receipt := readAdmissionEmulatorReceipt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID,
	)
	attempt := readAdmissionEmulatorAttempt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID, fixture.query.AttemptID,
	)
	if receipt.CleanupControlDisposition != winner.Ledger.Disposition ||
		receipt.LastCleanupErrorClass != winner.Ledger.ErrorClass ||
		attempt.CleanupDisposition != winner.Ledger.Disposition ||
		attempt.CleanupErrorClass != winner.Ledger.ErrorClass ||
		attempt.CleanupEvidenceHash != winner.Ledger.EvidenceHash {
		t.Fatalf("competing disposition partial state: receipt=%#v attempt=%#v winner=%#v", receipt, attempt, winner)
	}
}

func TestFirestoreAdmissionStoreEmulatorRetryCursorHasOneBoundaryClaimWinner(t *testing.T) {
	fixture := seedCleanupExecutionDispositionEmulatorFixture(t)
	command, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, fixture.ready, ingest.CleanupExecutionErrorProviderTimeout,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	result, err := fixture.store.DisposeCleanupExecution(context.Background(), command)
	if err != nil {
		t.Fatalf("DisposeCleanupExecution() = %v", err)
	}
	before := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	store := newCleanupDispositionFixedReadTimeStore(t, fixture.client, result.NextCleanupAt)
	proposals := []ingest.CleanupAttemptProposal{
		{ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion},
		{ID: "018f1f4e-2f5e-7d31-8c77-43b50f4c91ac", WorkerVersion: ingest.CleanupWorkerVersion},
	}
	type claimResult struct {
		grant  ingest.CleanupLeaseGrant
		status ingest.LeaseStatus
		err    error
	}
	results := make(chan claimResult, len(proposals))
	for _, proposal := range proposals {
		proposal := proposal
		go func() {
			grant, status, callErr := store.ClaimCleanupLease(
				context.Background(), fixture.query.TenantID, fixture.query.ReservationKey,
				ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup}, proposal,
				result.NextCleanupAt, ingest.DefaultRequestLeaseDuration,
			)
			results <- claimResult{grant: grant, status: status, err: callErr}
		}()
	}
	winners := 0
	held := 0
	var winnerID string
	for range proposals {
		call := <-results
		if call.err != nil {
			t.Fatalf("retry boundary claim error = %v", call.err)
		}
		switch call.status {
		case ingest.LeaseStatusAcquired:
			winners++
			winnerID = call.grant.Lease.Fence.OwnerID
		case ingest.LeaseStatusHeld:
			held++
		default:
			t.Fatalf("retry boundary status = %q", call.status)
		}
	}
	if winners != 1 || held != 1 {
		t.Fatalf("retry boundary winners/held = %d/%d", winners, held)
	}
	after := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	for _, path := range []string{
		fixture.targetPath, fixture.idempotencyPath, fixture.clientBatchPath, fixture.attemptPath,
	} {
		if !sameCleanupExpiryEmulatorDocument(before[path], after[path]) {
			t.Fatalf("retry claim mutated prior immutable document %q", path)
		}
	}
	receipt := readAdmissionEmulatorReceipt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID,
	)
	if receipt.LeaseOwnerID != winnerID || receipt.CleanupDispositionAttemptID != "" ||
		receipt.CleanupControlDisposition != "" || receipt.LastCleanupErrorClass != "" ||
		!receipt.NextCleanupAt.IsZero() || !receipt.CleanupHoldReviewDueAt.IsZero() {
		t.Fatalf("retry winner receipt = %#v", receipt)
	}
	newAttempt := readAdmissionEmulatorAttempt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID, winnerID,
	)
	if newAttempt.Status != ingest.RecoveryAttemptStarted || newAttempt.Outcome != "" ||
		newAttempt.DecisionDomain != "" || hasCleanupExecutionLedgerResidue(newAttempt) ||
		!newAttempt.CompletedAt.IsZero() || newAttempt.FailureCode != "" {
		t.Fatalf("retry winner attempt inherited prior state: %#v", newAttempt)
	}
}

func seedCleanupExecutionDispositionEmulatorFixture(
	t *testing.T,
) *cleanupExpiryFinalizationEmulatorFixture {
	t.Helper()
	fixture := seedReadyCleanupExpiryFinalizationEmulatorFixture(t)
	ledger := fixture.ready
	ledger.Phase = ingest.CleanupExecutionPhaseManifestOutcomeRecorded
	ledger.Revision = 6
	ledger.Manifest.AuditOutcome = ""
	ledger.Manifest.AuditedAt = time.Time{}
	completedAt := fixture.completedAt
	updates, err := cleanupExecutionLedgerUpdates(fixture.plan, ledger, completedAt)
	if err != nil {
		t.Fatalf("cleanupExecutionLedgerUpdates() = %v", err)
	}
	updates = append(updates,
		firestore.Update{Path: "cleanup_manifest_audit_outcome", Value: firestore.Delete},
		firestore.Update{Path: "cleanup_manifest_audited_at", Value: firestore.Delete},
	)
	if _, err := fixture.client.Doc(fixture.attemptPath).Update(
		context.Background(), updates,
	); err != nil {
		t.Fatalf("seed disposition ledger: %v", err)
	}
	fixture.ready = ledger
	fixture.store.now = func() time.Time { return completedAt }
	return fixture
}

type cleanupDispositionFixedReadTimeTransaction struct {
	firestoreAdmissionTransaction
	readTime time.Time
}

func (transaction cleanupDispositionFixedReadTimeTransaction) ReadReceipt(
	ctx context.Context,
	path string,
) (receiptRead, bool, error) {
	result, exists, err := transaction.firestoreAdmissionTransaction.ReadReceipt(ctx, path)
	result.ReadTime = transaction.readTime
	return result, exists, err
}

func (transaction cleanupDispositionFixedReadTimeTransaction) ReadRecoveryAttempt(
	ctx context.Context,
	path string,
) (recoveryAttemptRead, bool, error) {
	result, exists, err := transaction.firestoreAdmissionTransaction.ReadRecoveryAttempt(ctx, path)
	result.ReadTime = transaction.readTime
	return result, exists, err
}

func (transaction cleanupDispositionFixedReadTimeTransaction) ReadCleanupTarget(
	ctx context.Context,
	path string,
) (cleanupTargetRead, bool, error) {
	result, exists, err := transaction.firestoreAdmissionTransaction.ReadCleanupTarget(ctx, path)
	result.ReadTime = transaction.readTime
	return result, exists, err
}

func newCleanupDispositionFixedReadTimeStore(
	t *testing.T,
	client *firestore.Client,
	readTime time.Time,
) *FirestoreAdmissionStore {
	t.Helper()
	store, err := NewFirestoreAdmissionStore(
		client, emulatorTransactionTimout, func() time.Time { return readTime },
	)
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() = %v", err)
	}
	store.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		transactionContext, cancel := context.WithTimeout(ctx, emulatorTransactionTimout)
		defer cancel()
		return client.RunTransaction(
			transactionContext,
			func(runContext context.Context, transaction *firestore.Transaction) error {
				return operation(runContext, cleanupDispositionFixedReadTimeTransaction{
					firestoreAdmissionTransaction: firestoreAdmissionTransaction{
						client: client, transaction: transaction,
					},
					readTime: readTime.UTC(),
				})
			},
		)
	}
	return store
}
