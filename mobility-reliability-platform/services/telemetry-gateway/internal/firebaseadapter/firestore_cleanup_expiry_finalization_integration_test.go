package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorFinalizesCleanupAtomically(t *testing.T) {
	fixture := seedReadyCleanupExpiryFinalizationEmulatorFixture(t)
	before := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())

	result, err := fixture.store.FinalizeExpiredCleanup(context.Background(), fixture.query)
	if err != nil {
		t.Fatalf("FinalizeExpiredCleanup() = %v", err)
	}
	if result.Receipt.State != ingest.ReceiptExpired ||
		result.Receipt.Revision != fixture.receipt.Revision+1 ||
		result.Receipt.PurgeEligibleAt == nil ||
		result.Ledger.Phase != ingest.CleanupExecutionPhaseCompleted ||
		result.Ledger.Revision != fixture.ready.Revision+1 ||
		result.Ledger.CompletedAt.Before(fixture.completedAt) ||
		!result.Ledger.CompletedAt.Equal(result.Receipt.UpdatedAt) {
		t.Fatalf("finalization result = %#v", result)
	}

	after := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	if !sameCleanupExpiryEmulatorDocument(before[fixture.targetPath], after[fixture.targetPath]) {
		t.Fatal("finalization mutated immutable cleanup target")
	}
	commitTime := after[fixture.attemptPath].updateTime
	for _, path := range []string{
		fixture.attemptPath,
		fixture.receiptPath,
		fixture.idempotencyPath,
		fixture.clientBatchPath,
	} {
		if !after[path].updateTime.After(before[path].updateTime) {
			t.Fatalf("finalization did not update %q", path)
		}
		if !after[path].updateTime.Equal(commitTime) {
			t.Fatalf("four-document commit time mismatch for %q: got=%v want=%v", path, after[path].updateTime, commitTime)
		}
	}

	receipt := readAdmissionEmulatorReceipt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID,
	)
	attempt := readAdmissionEmulatorAttempt(
		t,
		fixture.client,
		fixture.receipt.TenantID,
		fixture.receipt.ReceiptID,
		fixture.query.AttemptID,
	)
	if attempt.Status != ingest.RecoveryAttemptCompleted ||
		attempt.Outcome != ingest.RecoveryAttemptOutcomeExpired ||
		attempt.CleanupPhase != ingest.CleanupExecutionPhaseCompleted ||
		attempt.CleanupExecutionRevision != result.Ledger.Revision ||
		attempt.CleanupEvidenceHash != result.Ledger.EvidenceHash ||
		!attempt.CompletedAt.Equal(result.Ledger.CompletedAt) {
		t.Fatalf("persisted terminal attempt = %#v", attempt)
	}
	if receipt.State != ingest.ReceiptExpired ||
		receipt.Revision != fixture.receipt.Revision+1 ||
		receipt.LeaseOwnerID != "" || receipt.LeaseOwnerKind != "" ||
		!receipt.LeaseAcquiredAt.IsZero() || !receipt.LeaseHeartbeatAt.IsZero() ||
		!receipt.LeaseExpiresAt.IsZero() || !receipt.NextRecoveryAt.IsZero() ||
		receipt.LastRecoveryCode != "" {
		t.Fatalf("persisted terminal receipt = %#v", receipt)
	}
	idempotency := readCleanupExpiryEmulatorIndex(t, fixture.client, fixture.idempotencyPath)
	clientBatch := readCleanupExpiryEmulatorIndex(t, fixture.client, fixture.clientBatchPath)
	if receipt.PurgeEligibleAt == nil || idempotency.PurgeEligibleAt == nil ||
		clientBatch.PurgeEligibleAt == nil ||
		!receipt.PurgeEligibleAt.Equal(*result.Receipt.PurgeEligibleAt) ||
		!idempotency.PurgeEligibleAt.Equal(*receipt.PurgeEligibleAt) ||
		!clientBatch.PurgeEligibleAt.Equal(*receipt.PurgeEligibleAt) {
		t.Fatalf(
			"purge eligibility mismatch: result=%v receipt=%v idempotency=%v client_batch=%v",
			result.Receipt.PurgeEligibleAt,
			receipt.PurgeEligibleAt,
			idempotency.PurgeEligibleAt,
			clientBatch.PurgeEligibleAt,
		)
	}
}

func TestFirestoreAdmissionStoreEmulatorConcurrentCleanupFinalizersHaveOneWinner(t *testing.T) {
	fixture := seedReadyCleanupExpiryFinalizationEmulatorFixture(t)
	beforeTarget := readCleanupExpiryEmulatorDocument(t, fixture.client, fixture.targetPath)
	preflightBarrier := newReceiptReadBarrier(fixture.receiptPath)
	mutationBarrier := newReceiptReadBarrier(fixture.receiptPath)
	store, err := newCleanupFinalizationContendedEmulatorStore(
		fixture.client, fixture.completedAt, preflightBarrier, mutationBarrier,
	)
	if err != nil {
		t.Fatalf("newCleanupFinalizationContendedEmulatorStore() = %v", err)
	}

	type finalizationCall struct {
		result ingest.CleanupExpiryFinalizationResult
		err    error
	}
	results := make(chan finalizationCall, 2)
	for range 2 {
		go func() {
			result, callErr := store.FinalizeExpiredCleanup(context.Background(), fixture.query)
			results <- finalizationCall{result: result, err: callErr}
		}()
	}

	winners := 0
	losers := 0
	var loserErrors []error
	for range 2 {
		call := <-results
		if call.err == nil {
			winners++
			if call.result.Receipt.Revision != fixture.receipt.Revision+1 ||
				call.result.Ledger.Phase != ingest.CleanupExecutionPhaseCompleted {
				t.Fatalf("winner result = %#v", call.result)
			}
			continue
		}
		losers++
		loserErrors = append(loserErrors, call.err)
		if !errors.Is(call.err, ingest.ErrCleanupExpiryFinalizationConflict) &&
			!errors.Is(call.err, ingest.ErrCleanupExpiryFinalizationUnavailable) &&
			!errors.Is(call.err, ingest.ErrInvalidCleanupExecutionLedger) {
			t.Fatalf("losing finalizer error = %v", call.err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf(
			"concurrent finalizers winners/losers = %d/%d, want 1/1; errors=%v",
			winners, losers, loserErrors,
		)
	}

	receipt := readAdmissionEmulatorReceipt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID,
	)
	if receipt.State != ingest.ReceiptExpired || receipt.Revision != fixture.receipt.Revision+1 {
		t.Fatalf("persisted concurrent result = %#v", receipt)
	}
	afterTarget := readCleanupExpiryEmulatorDocument(t, fixture.client, fixture.targetPath)
	if !sameCleanupExpiryEmulatorDocument(beforeTarget, afterTarget) {
		t.Fatal("concurrent finalizers mutated immutable cleanup target")
	}
}

func TestFirestoreAdmissionStoreEmulatorCleanupFinalizationRejectsStaleDispositionWithoutPartialLineage(t *testing.T) {
	fixture := seedReadyCleanupExpiryFinalizationEmulatorFixture(t)
	staleLedger := fixture.ready
	staleLedger.Phase = ingest.CleanupExecutionPhaseManifestOutcomeRecorded
	staleLedger.Revision = 6
	staleLedger.Manifest.AuditOutcome = ""
	staleLedger.Manifest.AuditedAt = time.Time{}
	staleCommand, err := ingest.BuildCleanupExecutionDispositionCommand(
		fixture.plan, staleLedger, ingest.CleanupExecutionErrorProviderUnavailable,
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionDispositionCommand() = %v", err)
	}
	staleOutcomeQuery, err := ingest.CleanupExecutionDispositionOutcomeQueryForLedger(
		fixture.plan, staleLedger, staleCommand.ErrorClass,
	)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionOutcomeQueryForLedger() = %v", err)
	}

	before := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	preflightBarrier := newReceiptReadBarrier(fixture.receiptPath)
	mutationBarrier := newReceiptReadBarrier(fixture.receiptPath)
	store, err := newCleanupFinalizationContendedEmulatorStore(
		fixture.client, fixture.completedAt, preflightBarrier, mutationBarrier,
	)
	if err != nil {
		t.Fatalf("newCleanupFinalizationContendedEmulatorStore() = %v", err)
	}

	type finalizationCall struct {
		result ingest.CleanupExpiryFinalizationResult
		err    error
	}
	type dispositionCall struct {
		result ingest.CleanupExecutionDispositionResult
		err    error
	}
	finalizationResult := make(chan finalizationCall, 1)
	dispositionResult := make(chan dispositionCall, 1)
	go func() {
		result, callErr := store.FinalizeExpiredCleanup(context.Background(), fixture.query)
		finalizationResult <- finalizationCall{result: result, err: callErr}
	}()
	go func() {
		result, callErr := store.DisposeCleanupExecution(context.Background(), staleCommand)
		dispositionResult <- dispositionCall{result: result, err: callErr}
	}()

	finalization := <-finalizationResult
	disposition := <-dispositionResult
	if finalization.err != nil {
		t.Fatalf("FinalizeExpiredCleanup() = %v", finalization.err)
	}
	if !errors.Is(disposition.err, ingest.ErrCleanupExecutionDispositionConflict) {
		t.Fatalf("DisposeCleanupExecution(stale) error = %v", disposition.err)
	}
	if disposition.result.Receipt.ReceiptID != "" ||
		disposition.result.Ledger.Phase != "" ||
		ingest.ValidateCleanupExecutionDispositionOutcomeQuery(disposition.result.OutcomeQuery) == nil {
		t.Fatalf("stale disposition exposed a terminal result = %#v", disposition.result)
	}
	if finalization.result.Receipt.State != ingest.ReceiptExpired ||
		finalization.result.Receipt.Revision != fixture.receipt.Revision+1 ||
		finalization.result.Ledger.Phase != ingest.CleanupExecutionPhaseCompleted ||
		finalization.result.Ledger.Revision != fixture.ready.Revision+1 ||
		finalization.result.Ledger.Disposition != ingest.CleanupExecutionDispositionComplete {
		t.Fatalf("winning finalization result = %#v", finalization.result)
	}

	after := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	if !sameCleanupExpiryEmulatorDocument(before[fixture.targetPath], after[fixture.targetPath]) {
		t.Fatal("terminal race mutated immutable cleanup target")
	}
	commitTime := after[fixture.attemptPath].updateTime
	for _, path := range []string{
		fixture.attemptPath,
		fixture.receiptPath,
		fixture.idempotencyPath,
		fixture.clientBatchPath,
	} {
		if !after[path].updateTime.After(before[path].updateTime) {
			t.Fatalf("terminal race did not update finalization document %q", path)
		}
		if !after[path].updateTime.Equal(commitTime) {
			t.Fatalf("terminal race left a partial commit at %q: got=%v want=%v", path, after[path].updateTime, commitTime)
		}
	}

	receipt := readAdmissionEmulatorReceipt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID,
	)
	attempt := readAdmissionEmulatorAttempt(
		t, fixture.client, fixture.receipt.TenantID, fixture.receipt.ReceiptID,
		fixture.query.AttemptID,
	)
	idempotency := readCleanupExpiryEmulatorIndex(t, fixture.client, fixture.idempotencyPath)
	clientBatch := readCleanupExpiryEmulatorIndex(t, fixture.client, fixture.clientBatchPath)
	if receipt.State != ingest.ReceiptExpired || receipt.Revision != fixture.receipt.Revision+1 ||
		receipt.PurgeEligibleAt == nil || receipt.CleanupDispositionAttemptID != "" ||
		receipt.CleanupControlDisposition != "" || receipt.LastCleanupErrorClass != "" ||
		!receipt.NextCleanupAt.IsZero() || !receipt.CleanupHoldReviewDueAt.IsZero() {
		t.Fatalf("terminal race receipt has partial disposition state = %#v", receipt)
	}
	if attempt.Status != ingest.RecoveryAttemptCompleted ||
		attempt.Outcome != ingest.RecoveryAttemptOutcomeExpired ||
		attempt.CleanupPhase != ingest.CleanupExecutionPhaseCompleted ||
		attempt.CleanupExecutionRevision != fixture.ready.Revision+1 ||
		attempt.CleanupDisposition != ingest.CleanupExecutionDispositionComplete ||
		attempt.CleanupErrorClass != "" ||
		attempt.CleanupEvidenceHash != finalization.result.Ledger.EvidenceHash ||
		!attempt.CompletedAt.Equal(finalization.result.Ledger.CompletedAt) {
		t.Fatalf("terminal race attempt has partial disposition state = %#v", attempt)
	}
	if idempotency.PurgeEligibleAt == nil || clientBatch.PurgeEligibleAt == nil ||
		!idempotency.PurgeEligibleAt.Equal(*receipt.PurgeEligibleAt) ||
		!clientBatch.PurgeEligibleAt.Equal(*receipt.PurgeEligibleAt) {
		t.Fatalf(
			"terminal race index lineage mismatch: receipt=%v idempotency=%v client_batch=%v",
			receipt.PurgeEligibleAt, idempotency.PurgeEligibleAt, clientBatch.PurgeEligibleAt,
		)
	}

	authorizer, err := ingest.NewSystemCleanupExecutionDispositionOutcomeAuthorizer(
		store, func() time.Time { return fixture.completedAt },
	)
	if err != nil {
		t.Fatalf("NewSystemCleanupExecutionDispositionOutcomeAuthorizer() = %v", err)
	}
	grant, err := authorizer.Authorize(context.Background(), staleOutcomeQuery)
	if err != nil {
		t.Fatalf("Authorize(stale disposition outcome) = %v", err)
	}
	deadline, err := ingest.CleanupExecutionDispositionOutcomeAuthorizationDeadline(
		grant, staleOutcomeQuery,
	)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionOutcomeAuthorizationDeadline() = %v", err)
	}
	staleOutcome, err := store.GetCleanupExecutionDispositionOutcome(
		context.Background(), grant, staleOutcomeQuery,
		deadline.Add(-ingest.CleanupExecutionDispositionOutcomeGrantTTL),
	)
	if err != nil ||
		staleOutcome.CommitStatus != ingest.CleanupExecutionDispositionUnverifiable ||
		staleOutcome.Disposition != "" || staleOutcome.ErrorClass != "" ||
		staleOutcome.EvidenceHash != "" || !staleOutcome.CompletedAt.IsZero() {
		t.Fatalf("stale disposition correlation = %#v, %v", staleOutcome, err)
	}
	readOnlyAfter := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	assertCleanupExpiryEmulatorDocumentsUnchanged(t, after, readOnlyAfter)
}

func TestFirestoreAdmissionStoreEmulatorRejectsInvalidCleanupFinalizationWithoutWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *cleanupExpiryFinalizationEmulatorFixture)
	}{
		{
			name: "malformed ledger binding",
			mutate: func(t *testing.T, fixture *cleanupExpiryFinalizationEmulatorFixture) {
				t.Helper()
				if _, err := fixture.client.Doc(fixture.attemptPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "cleanup_plan_hash", Value: strings.Repeat("f", 64)}},
				); err != nil {
					t.Fatalf("seed malformed ledger: %v", err)
				}
			},
		},
		{
			name: "mismatched indexes",
			mutate: func(t *testing.T, fixture *cleanupExpiryFinalizationEmulatorFixture) {
				t.Helper()
				if _, err := fixture.client.Doc(fixture.clientBatchPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "body_hash", Value: strings.Repeat("a", 64)}},
				); err != nil {
					t.Fatalf("seed mismatched index: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := seedReadyCleanupExpiryFinalizationEmulatorFixture(t)
			test.mutate(t, fixture)
			before := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())

			if _, err := fixture.store.FinalizeExpiredCleanup(
				context.Background(), fixture.query,
			); err == nil {
				t.Fatal("invalid cleanup state was finalized")
			}

			after := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
			assertCleanupExpiryEmulatorDocumentsUnchanged(t, before, after)
		})
	}
}

func TestFirestoreAdmissionStoreEmulatorCleanupFinalizationOutcomeReadWritesZero(t *testing.T) {
	fixture := seedReadyCleanupExpiryFinalizationEmulatorFixture(t)
	result, err := fixture.store.FinalizeExpiredCleanup(context.Background(), fixture.query)
	if err != nil {
		t.Fatalf("FinalizeExpiredCleanup() = %v", err)
	}
	before := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	authorizer, err := ingest.NewSystemCleanupExpiryFinalizationOutcomeAuthorizer(
		fixture.store, func() time.Time { return fixture.completedAt },
	)
	if err != nil {
		t.Fatalf("NewSystemCleanupExpiryFinalizationOutcomeAuthorizer() = %v", err)
	}
	grant, err := authorizer.Authorize(context.Background(), result.OutcomeQuery)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	deadline, err := ingest.CleanupExpiryFinalizationOutcomeAuthorizationDeadline(
		grant, result.OutcomeQuery,
	)
	if err != nil {
		t.Fatalf("CleanupExpiryFinalizationOutcomeAuthorizationDeadline() = %v", err)
	}
	outcome, err := fixture.store.GetCleanupExpiryFinalizationOutcome(
		context.Background(), grant, result.OutcomeQuery,
		deadline.Add(-ingest.CleanupExpiryFinalizationOutcomeGrantTTL),
	)
	if err != nil || outcome.CommitStatus != ingest.CleanupExpiryFinalizationCommitted ||
		outcome.EvidenceHash != result.Ledger.EvidenceHash ||
		!outcome.CompletedAt.Equal(result.Ledger.CompletedAt) ||
		!outcome.PurgeEligibleAt.Equal(*result.Receipt.PurgeEligibleAt) {
		t.Fatalf("GetCleanupExpiryFinalizationOutcome() = %#v, %v", outcome, err)
	}
	after := readCleanupExpiryEmulatorDocuments(t, fixture.client, fixture.paths())
	assertCleanupExpiryEmulatorDocumentsUnchanged(t, before, after)
}

type cleanupExpiryFinalizationEmulatorFixture struct {
	client          *firestore.Client
	store           *FirestoreAdmissionStore
	receipt         firestoreIngestReceipt
	query           ingest.CleanupExecutionQuery
	plan            ingest.CleanupExecutionLedgerPlan
	ready           ingest.CleanupExecutionLedger
	completedAt     time.Time
	idempotencyPath string
	clientBatchPath string
	receiptPath     string
	attemptPath     string
	targetPath      string
}

func (fixture *cleanupExpiryFinalizationEmulatorFixture) paths() []string {
	return []string{
		fixture.idempotencyPath,
		fixture.clientBatchPath,
		fixture.receiptPath,
		fixture.attemptPath,
		fixture.targetPath,
	}
}

func seedReadyCleanupExpiryFinalizationEmulatorFixture(
	t *testing.T,
) *cleanupExpiryFinalizationEmulatorFixture {
	t.Helper()
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, receipt, attempt := seedClaimedCleanupTargetFixture(t, client, now)
	command := cleanupTargetCommandFixture(t, receipt, ingest.ArtifactClassificationValidComplete)
	target, status, err := store.createCleanupDryRunTarget(
		context.Background(), ingest.CleanupTargetAuthorizationGrant{}, command,
		command.CreatedAt, exactCleanupTargetSnapshotValidator(receipt, attempt),
	)
	if err != nil || status != ingest.CleanupTargetCreated {
		t.Fatalf("createCleanupDryRunTarget() = %#v, %q, %v", target, status, err)
	}
	query := ingest.CleanupExecutionQuery{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		AttemptID: attempt.AttemptID,
	}
	plan, err := ingest.BuildCleanupExecutionLedgerPlan(query, ingest.CurrentCleanupExecutionSnapshot{
		Receipt: receipt.toDomain(), Attempt: currentCleanupAttempt(attempt),
		Target: target, ReadTime: now,
	}, now)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}
	ready, mutationStatus, err := store.InitializeCleanupExecutionLedger(
		context.Background(), query,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("InitializeCleanupExecutionLedger() = %#v, %q, %v", ready, mutationStatus, err)
	}
	base := command.CreatedAt
	for _, transition := range []ingest.CleanupExecutionTransition{
		{Phase: ingest.CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: base.Add(100 * time.Millisecond)},
		{Phase: ingest.CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: ingest.CleanupDeleteObserved, ObservedAt: base.Add(200 * time.Millisecond)},
		{Phase: ingest.CleanupExecutionPhaseRawAbsenceConfirmed, AuditOutcome: ingest.CleanupAuditConfirmedAbsent, ObservedAt: base.Add(300 * time.Millisecond)},
		{Phase: ingest.CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: base.Add(400 * time.Millisecond)},
		{Phase: ingest.CleanupExecutionPhaseManifestOutcomeRecorded, DeleteOutcome: ingest.CleanupDeleteNotFound, ObservedAt: base.Add(500 * time.Millisecond)},
		{Phase: ingest.CleanupExecutionPhaseManifestAbsenceConfirmed, AuditOutcome: ingest.CleanupAuditConfirmedAbsent, ObservedAt: base.Add(600 * time.Millisecond)},
	} {
		ready, err = ingest.AdvanceCleanupExecutionLedger(plan, ready, transition)
		if err != nil {
			t.Fatalf("AdvanceCleanupExecutionLedger(%q) = %v", transition.Phase, err)
		}
	}
	attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, attempt.AttemptID)
	updates, err := cleanupExecutionLedgerUpdates(plan, ready, base.Add(600*time.Millisecond))
	if err != nil {
		t.Fatalf("cleanupExecutionLedgerUpdates() = %v", err)
	}
	if _, err := client.Doc(attemptPath).Update(context.Background(), updates); err != nil {
		t.Fatalf("seed ready cleanup ledger: %v", err)
	}
	completedAt := base.Add(700 * time.Millisecond)
	store.now = func() time.Time { return completedAt }
	return &cleanupExpiryFinalizationEmulatorFixture{
		client: client, store: store, receipt: receipt, query: query, plan: plan, ready: ready,
		completedAt:     completedAt,
		idempotencyPath: idempotencyDocumentPath(receipt.TenantID, receipt.ReservationKey),
		clientBatchPath: clientBatchDocumentPath(receipt.TenantID, receipt.ClientBatchKey),
		receiptPath:     receiptDocumentPath(receipt.TenantID, receipt.ReceiptID),
		attemptPath:     attemptPath,
		targetPath:      cleanupTargetDocumentPath(receipt.TenantID, attempt.AttemptID),
	}
}

type cleanupExpiryEmulatorDocument struct {
	data       map[string]interface{}
	updateTime time.Time
}

func readCleanupExpiryEmulatorDocument(
	t *testing.T,
	client *firestore.Client,
	path string,
) cleanupExpiryEmulatorDocument {
	t.Helper()
	document, err := client.Doc(path).Get(context.Background())
	if err != nil {
		t.Fatalf("read cleanup finalization document %q: %v", path, err)
	}
	return cleanupExpiryEmulatorDocument{
		data: document.Data(), updateTime: document.UpdateTime.UTC(),
	}
}

func readCleanupExpiryEmulatorDocuments(
	t *testing.T,
	client *firestore.Client,
	paths []string,
) map[string]cleanupExpiryEmulatorDocument {
	t.Helper()
	documents := make(map[string]cleanupExpiryEmulatorDocument, len(paths))
	for _, path := range paths {
		documents[path] = readCleanupExpiryEmulatorDocument(t, client, path)
	}
	return documents
}

func readCleanupExpiryEmulatorIndex(
	t *testing.T,
	client *firestore.Client,
	path string,
) firestoreIngestIndex {
	t.Helper()
	document, err := client.Doc(path).Get(context.Background())
	if err != nil {
		t.Fatalf("read cleanup finalization index %q: %v", path, err)
	}
	var index firestoreIngestIndex
	if err := document.DataTo(&index); err != nil {
		t.Fatalf("decode cleanup finalization index %q: %v", path, err)
	}
	return index
}

func assertCleanupExpiryEmulatorDocumentsUnchanged(
	t *testing.T,
	before map[string]cleanupExpiryEmulatorDocument,
	after map[string]cleanupExpiryEmulatorDocument,
) {
	t.Helper()
	for path, expected := range before {
		if !sameCleanupExpiryEmulatorDocument(expected, after[path]) {
			t.Fatalf("rejected/read-only finalization changed %q", path)
		}
	}
}

func sameCleanupExpiryEmulatorDocument(
	left cleanupExpiryEmulatorDocument,
	right cleanupExpiryEmulatorDocument,
) bool {
	return left.updateTime.Equal(right.updateTime) && reflect.DeepEqual(left.data, right.data)
}

type cleanupFinalizationReceiptBarrierTransaction struct {
	admissionTransaction
	cleanupTargetTransaction
	barrier *receiptReadBarrier
}

func (transaction cleanupFinalizationReceiptBarrierTransaction) ReadReceipt(
	ctx context.Context,
	path string,
) (receiptRead, bool, error) {
	receipt, exists, err := transaction.admissionTransaction.ReadReceipt(ctx, path)
	if err == nil && exists && path == transaction.barrier.path {
		transaction.barrier.waitForTwo()
	}
	return receipt, exists, err
}

func newCleanupFinalizationContendedEmulatorStore(
	client *firestore.Client,
	now time.Time,
	preflightBarrier *receiptReadBarrier,
	mutationBarrier *receiptReadBarrier,
) (*FirestoreAdmissionStore, error) {
	store, err := NewFirestoreAdmissionStore(
		client, emulatorTransactionTimout, func() time.Time { return now },
	)
	if err != nil {
		return nil, err
	}
	var transactionCalls atomic.Int64
	store.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		barrier := mutationBarrier
		if transactionCalls.Add(1) <= 2 {
			barrier = preflightBarrier
		}
		transactionContext, cancel := context.WithTimeout(ctx, emulatorTransactionTimout)
		defer cancel()
		return client.RunTransaction(
			transactionContext,
			func(runContext context.Context, transaction *firestore.Transaction) error {
				base := firestoreAdmissionTransaction{client: client, transaction: transaction}
				return operation(runContext, cleanupFinalizationReceiptBarrierTransaction{
					admissionTransaction:     base,
					cleanupTargetTransaction: base,
					barrier:                  barrier,
				})
			},
		)
	}
	return store, nil
}
