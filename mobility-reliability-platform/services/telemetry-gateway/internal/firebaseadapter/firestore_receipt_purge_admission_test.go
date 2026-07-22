package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreReceiptPurgeAdmissionCreatesJobAndFenceOnly(t *testing.T) {
	fixture := newReceiptPurgeAdmissionFixture(t)

	result, err := fixture.store.AdmitReceiptPurge(context.Background(), fixture.command)
	if err != nil || result.Status != ingest.ReceiptPurgeAdmissionCreated {
		t.Fatalf("AdmitReceiptPurge() = %#v, %v", result, err)
	}
	if ingest.ValidateReceiptPurgeJob(result.Job) != nil ||
		ingest.ValidateReceiptPurgeAdmissionOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("invalid admission result = %#v", result)
	}
	if result.Job.ReceiptRevision != fixture.receipt.Revision+1 ||
		!result.Job.CreatedAt.Equal(fixture.now) ||
		result.OutcomeQuery.ExpectedPostReceiptRevision != result.Job.ReceiptRevision {
		t.Fatalf("unexpected job lineage = %#v", result)
	}
	if len(fixture.transaction.creates) != 1 || len(fixture.transaction.updates) != 1 {
		t.Fatalf("creates/updates = %d/%d, want 1/1", len(fixture.transaction.creates), len(fixture.transaction.updates))
	}
	if fixture.transaction.creates[0].path != receiptPurgeJobDocumentPath(fixture.command.PurgeKey) ||
		fixture.transaction.updates[0].path != admissionReceiptPath() {
		t.Fatalf("mutation paths = %#v / %#v", fixture.transaction.creates, fixture.transaction.updates)
	}
	updates := firestoreUpdateMap(fixture.transaction.updates[0].updates)
	if updates["purge_job_id"] != fixture.command.PurgeKey ||
		updates["purge_fence_version"] != ingest.ReceiptPurgeFenceVersion ||
		updates["revision"] != fixture.receipt.Revision+1 || updates["updated_at"] != fixture.now {
		t.Fatalf("receipt fence updates = %#v", updates)
	}
	for _, indexPath := range []string{admissionIdempotencyPath(), admissionClientBatchPath()} {
		if fixture.transaction.updates[0].path == indexPath {
			t.Fatalf("admission rewrote uniqueness index %q", indexPath)
		}
	}
}

func TestFirestoreReceiptPurgeAdmissionExactReplayWritesNothing(t *testing.T) {
	fixture := newReceiptPurgeAdmissionFixture(t)
	job, err := ingest.BuildPostFenceReceiptPurgeJob(fixture.command, fixture.now)
	if err != nil {
		t.Fatalf("BuildPostFenceReceiptPurgeJob() = %v", err)
	}
	fixture.seedCommitted(job)

	result, err := fixture.store.AdmitReceiptPurge(context.Background(), fixture.command)
	if err != nil || result.Status != ingest.ReceiptPurgeAdmissionReplayed || result.Job != job {
		t.Fatalf("AdmitReceiptPurge(replay) = %#v, %v", result, err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreReceiptPurgeAdmissionProgressedJobReplayWritesNothing(t *testing.T) {
	fixture := newReceiptPurgeAdmissionFixture(t)
	job, err := ingest.BuildPostFenceReceiptPurgeJob(fixture.command, fixture.now)
	if err != nil {
		t.Fatalf("BuildPostFenceReceiptPurgeJob() = %v", err)
	}
	job.Status = ingest.ReceiptPurgeJobAttemptsPurging
	job.Revision = 2
	if ingest.ValidateReceiptPurgeJob(job) != nil {
		t.Fatalf("progressed job fixture invalid: %#v", job)
	}
	fixture.seedCommitted(job)

	result, err := fixture.store.AdmitReceiptPurge(context.Background(), fixture.command)
	if err != nil || result.Status != ingest.ReceiptPurgeAdmissionReplayed || result.Job != job {
		t.Fatalf("AdmitReceiptPurge(progressed replay) = %#v, %v", result, err)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreReceiptPurgeAdmissionRejectsPartialOrConflictingFence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*receiptPurgeAdmissionFixture)
	}{
		{
			name: "job without receipt fence",
			mutate: func(fixture *receiptPurgeAdmissionFixture) {
				job, _ := ingest.BuildPostFenceReceiptPurgeJob(fixture.command, fixture.now)
				fixture.transaction.jobs[receiptPurgeJobDocumentPath(job.PurgeKey)] = receiptPurgeJobRead{
					Job: newFirestoreReceiptPurgeJob(job), ReadTime: fixture.now,
				}
			},
		},
		{
			name: "partial receipt fence",
			mutate: func(fixture *receiptPurgeAdmissionFixture) {
				receipt := fixture.transaction.receipts[admissionReceiptPath()]
				receipt.PurgeJobID = fixture.command.PurgeKey
				fixture.transaction.receipts[admissionReceiptPath()] = receipt
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReceiptPurgeAdmissionFixture(t)
			test.mutate(fixture)
			result, err := fixture.store.AdmitReceiptPurge(context.Background(), fixture.command)
			if err == nil || result != (ingest.ReceiptPurgeAdmissionResult{}) {
				t.Fatalf("AdmitReceiptPurge() = %#v, %v, want fail-closed", result, err)
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestFirestoreReceiptPurgeAdmissionPreservesOutcomeQueryOnCommitResponseLoss(t *testing.T) {
	fixture := newReceiptPurgeAdmissionFixture(t)
	commitResponseLost := errors.New("commit response lost")
	fixture.store.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		if err := operation(ctx, fixture.transaction); err != nil {
			return err
		}
		return commitResponseLost
	}

	result, err := fixture.store.AdmitReceiptPurge(context.Background(), fixture.command)
	if !errors.Is(err, ingest.ErrReceiptPurgeAdmissionUnavailable) ||
		result.Status != "" ||
		ingest.ValidateReceiptPurgeAdmissionOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("AdmitReceiptPurge(response loss) = %#v, %v", result, err)
	}
	if len(fixture.transaction.creates) != 1 || len(fixture.transaction.updates) != 1 {
		t.Fatalf("response-loss mutation calls = %d/%d, want one transaction attempt", len(fixture.transaction.creates), len(fixture.transaction.updates))
	}
}

func TestFirestoreReceiptPurgeAdmissionOutcomeCorrelation(t *testing.T) {
	t.Run("not committed", func(t *testing.T) {
		fixture := newReceiptPurgeAdmissionFixture(t)
		// The store clock is sampled after the read. A Firestore read timestamp
		// slightly ahead of that clock is normalized within the bounded skew.
		fixture.transaction.receiptReadTime = fixture.now.Add(time.Second)
		query, err := ingest.BuildReceiptPurgeAdmissionOutcomeQuery(fixture.command, fixture.now)
		if err != nil {
			t.Fatalf("BuildReceiptPurgeAdmissionOutcomeQuery() = %v", err)
		}
		outcome, err := fixture.store.GetReceiptPurgeAdmissionOutcome(
			context.Background(), query,
		)
		if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeAdmissionNotCommitted ||
			outcome.ReceiptRevision != fixture.receipt.Revision {
			t.Fatalf("not-committed outcome = %#v, %v", outcome, err)
		}
		fixture.assertNoWrites(t)
	})

	t.Run("committed", func(t *testing.T) {
		fixture := newReceiptPurgeAdmissionFixture(t)
		job, _ := ingest.BuildPostFenceReceiptPurgeJob(fixture.command, fixture.now)
		query, _ := ingest.BuildReceiptPurgeAdmissionOutcomeQuery(fixture.command, fixture.now)
		fixture.seedCommitted(job)
		outcome, err := fixture.store.GetReceiptPurgeAdmissionOutcome(
			context.Background(), query,
		)
		if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeAdmissionCommitted ||
			outcome.JobRevision != 1 || !outcome.PurgeStartedAt.Equal(fixture.now) {
			t.Fatalf("committed outcome = %#v, %v", outcome, err)
		}
		fixture.assertNoWrites(t)
	})
}

func TestReceiptPurgeJobDocumentShapeRejectsUnknownAndMissingFields(t *testing.T) {
	valid := map[string]any{
		"schema_version": "v", "policy_version": "v", "purge_key": "k", "tenant_id": "t",
		"receipt_id": "r", "receipt_revision": int64(1), "linkage_hash": "h", "status": "planned",
		"revision": int64(1), "attempt_deleted_count": int64(0),
		"target_deleted_count": int64(0), "finding_deleted_count": int64(0),
		"created_at": admissionTestNow(), "updated_at": admissionTestNow(),
	}
	if err := validateReceiptPurgeJobDocumentShape(valid); err != nil {
		t.Fatalf("valid document shape rejected: %v", err)
	}
	withUnknown := make(map[string]any, len(valid)+1)
	for key, value := range valid {
		withUnknown[key] = value
	}
	withUnknown["device_id"] = admissionDeviceID
	if err := validateReceiptPurgeJobDocumentShape(withUnknown); !errors.Is(err, ingest.ErrReceiptPurgeAdmissionUnavailable) {
		t.Fatal("unknown identity field was accepted")
	}
	delete(valid, "receipt_revision")
	if err := validateReceiptPurgeJobDocumentShape(valid); !errors.Is(err, ingest.ErrReceiptPurgeAdmissionUnavailable) {
		t.Fatal("missing required field was accepted")
	}
}

func TestPurgeFenceBlocksAttemptAndCleanupTargetCreation(t *testing.T) {
	fixture := newReceiptPurgeAdmissionFixture(t)
	job, _ := ingest.BuildPostFenceReceiptPurgeJob(fixture.command, fixture.now)
	fixture.seedCommitted(job)

	proposal := ingest.RecoveryAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion,
	}
	_, status, err := fixture.store.ClaimRecoveryLease(
		context.Background(), admissionTenantID, admissionReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerSweeper},
		proposal, fixture.now, ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusNotEligible {
		t.Fatalf("ClaimRecoveryLease(fenced) = %q, %v", status, err)
	}
	cleanupProposal := ingest.CleanupAttemptProposal{
		ID: admissionTakeoverOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	_, status, err = fixture.store.ClaimCleanupLease(
		context.Background(), admissionTenantID, admissionReservationKey,
		ingest.LeaseOwner{ID: cleanupProposal.ID, Kind: ingest.LeaseOwnerCleanup},
		cleanupProposal, fixture.now, ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusNotEligible {
		t.Fatalf("ClaimCleanupLease(fenced) = %q, %v", status, err)
	}
	fixture.assertNoWrites(t)

	targetFixture := newCleanupTargetAdapterFixture(t)
	receipt := targetFixture.transaction.receipts[admissionReceiptPath()]
	receipt.State = ingest.ReceiptExpired
	receipt.clearLease()
	receipt.CleanupDispositionAttemptID = ""
	receipt.CleanupControlDisposition = ""
	receipt.LastCleanupErrorClass = ""
	receipt.NextCleanupAt = time.Time{}
	receipt.CleanupHoldReviewDueAt = time.Time{}
	purgeEligibleAt := receipt.ReceiptRetentionFloor
	receipt.PurgeEligibleAt = &purgeEligibleAt
	receipt.PurgeJobID = fixture.command.PurgeKey
	receipt.PurgeStartedAt = purgeEligibleAt.Add(time.Second)
	receipt.PurgeFenceVersion = ingest.ReceiptPurgeFenceVersion
	receipt.UpdatedAt = receipt.PurgeStartedAt
	targetFixture.transaction.receipts[admissionReceiptPath()] = receipt
	for path, index := range targetFixture.transaction.indexes {
		index.PurgeEligibleAt = cloneOptionalTime(&purgeEligibleAt)
		targetFixture.transaction.indexes[path] = index
	}
	_, _, err = targetFixture.store.createCleanupDryRunTarget(
		context.Background(), ingest.CleanupTargetAuthorizationGrant{}, targetFixture.command,
		targetFixture.observedAt, validateCleanupTargetAdapterSnapshot,
	)
	if !errors.Is(err, ingest.ErrInvalidCleanupTarget) {
		t.Fatalf("createCleanupDryRunTarget(fenced) = %v", err)
	}
	targetFixture.assertNoWrites(t)
}

type receiptPurgeAdmissionFixture struct {
	transaction *fakeReceiptPurgeTransaction
	store       *FirestoreAdmissionStore
	command     ingest.ReceiptPurgeAdmissionCommand
	receipt     firestoreIngestReceipt
	now         time.Time
}

func newReceiptPurgeAdmissionFixture(t *testing.T) *receiptPurgeAdmissionFixture {
	t.Helper()
	createdAt := admissionTestNow()
	transitionedAt := createdAt.Add(ingest.ReservationProcessingWindow)
	base, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
	completedAt := receipt.CleanupQuiescenceUntil.Add(time.Minute)
	receipt.State = ingest.ReceiptExpired
	receipt.UpdatedAt = completedAt
	receipt.Revision++
	purgeEligibleAt, err := ingest.CleanupPurgeEligibleAt(receipt.ReceiptRetentionFloor, completedAt)
	if err != nil {
		t.Fatalf("CleanupPurgeEligibleAt() = %v", err)
	}
	receipt.PurgeEligibleAt = &purgeEligibleAt
	for path, index := range base.indexes {
		index.PurgeEligibleAt = cloneOptionalTime(&purgeEligibleAt)
		base.indexes[path] = index
	}
	base.receipts[admissionReceiptPath()] = receipt
	now := purgeEligibleAt.Add(time.Second)
	base.readTime = now
	transaction := &fakeReceiptPurgeTransaction{
		fakeAdmissionTransaction: base,
		jobs:                     make(map[string]receiptPurgeJobRead),
		jobReadTime:              now,
	}
	command, err := ingest.BuildReceiptPurgeAdmissionCommand(
		receiptPurgeAdmissionState(receipt, base.indexes[admissionIdempotencyPath()]), now,
	)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeAdmissionCommand() = %v", err)
	}
	return &receiptPurgeAdmissionFixture{
		transaction: transaction,
		store:       admissionTestStore(now, admissionRunner(transaction)),
		command:     command, receipt: receipt, now: now,
	}
}

func (fixture *receiptPurgeAdmissionFixture) seedCommitted(job ingest.ReceiptPurgeJob) {
	path := receiptPurgeJobDocumentPath(job.PurgeKey)
	fixture.transaction.jobs[path] = receiptPurgeJobRead{
		Job: newFirestoreReceiptPurgeJob(job), ReadTime: fixture.now,
	}
	receipt := fixture.transaction.receipts[admissionReceiptPath()]
	receipt.PurgeJobID = job.PurgeKey
	receipt.PurgeStartedAt = job.CreatedAt
	receipt.PurgeFenceVersion = ingest.ReceiptPurgeFenceVersion
	receipt.Revision = job.ReceiptRevision
	receipt.UpdatedAt = job.CreatedAt
	fixture.transaction.receipts[admissionReceiptPath()] = receipt
}

func (fixture *receiptPurgeAdmissionFixture) assertNoWrites(t *testing.T) {
	t.Helper()
	if len(fixture.transaction.creates) != 0 || len(fixture.transaction.updates) != 0 {
		t.Fatalf("creates/updates = %d/%d, want 0/0", len(fixture.transaction.creates), len(fixture.transaction.updates))
	}
}

type fakeReceiptPurgeTransaction struct {
	*fakeAdmissionTransaction
	jobs        map[string]receiptPurgeJobRead
	jobReadTime time.Time
}

func (transaction *fakeReceiptPurgeTransaction) ReadReceiptPurgeJob(
	_ context.Context,
	path string,
) (receiptPurgeJobRead, bool, error) {
	value, exists := transaction.jobs[path]
	if exists && value.ReadTime.IsZero() {
		value.ReadTime = transaction.jobReadTime
	}
	return value, exists, nil
}
