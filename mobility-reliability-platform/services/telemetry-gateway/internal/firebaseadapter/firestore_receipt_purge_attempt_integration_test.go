package firebaseadapter

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreEmulatorReceiptPurgeAttemptPaginationAndExhaustion(t *testing.T) {
	fixture, job, attemptIDs := prepareEmulatorReceiptPurgeAttempts(t, 3)

	first := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 2)
	if len(first.DeleteDocumentIDs) != 2 || first.LookaheadDocumentID != attemptIDs[2] {
		t.Fatalf("first page = %#v", first)
	}
	firstResult, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), first)
	if err != nil || firstResult.Status != ingest.ReceiptPurgeMutationProgressed ||
		firstResult.Job.AttemptCursor != attemptIDs[1] || firstResult.Job.AttemptDeletedCount != 2 {
		t.Fatalf("first page result = %#v, %v", firstResult, err)
	}
	assertEmulatorReceiptPurgeAttemptMissing(t, fixture, attemptIDs[0])
	assertEmulatorReceiptPurgeAttemptMissing(t, fixture, attemptIDs[1])
	assertEmulatorReceiptPurgeAttemptPresent(t, fixture, attemptIDs[2])

	second := listEmulatorReceiptPurgeAttemptPage(t, fixture, firstResult.Job, 2)
	if len(second.DeleteDocumentIDs) != 1 || second.DeleteDocumentIDs[0] != attemptIDs[2] ||
		second.LookaheadDocumentID != "" {
		t.Fatalf("second page = %#v", second)
	}
	secondResult, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), second)
	if err != nil || secondResult.Job.AttemptDeletedCount != 3 ||
		secondResult.Job.AttemptCursor != attemptIDs[2] {
		t.Fatalf("second page result = %#v, %v", secondResult, err)
	}
	empty := listEmulatorReceiptPurgeAttemptPage(t, fixture, secondResult.Job, 2)
	if len(empty.DeleteDocumentIDs) != 0 || empty.LookaheadDocumentID != "" {
		t.Fatalf("empty page = %#v", empty)
	}
	completed, err := fixture.store.CompleteReceiptPurgeAttempts(
		context.Background(), ingest.ReceiptPurgeAttemptPhaseCommand{
			Action: ingest.ReceiptPurgeAttemptPhaseComplete, PurgeKey: secondResult.Job.PurgeKey,
			TenantID: secondResult.Job.TenantID, ReceiptID: secondResult.Job.ReceiptID,
			ExpectedJobRevision: secondResult.Job.Revision, EmptyObservation: empty,
		},
	)
	if err != nil || completed.Job.Status != ingest.ReceiptPurgeJobLinkedDocumentsPurging ||
		completed.Job.AttemptDeletedCount != 3 || completed.Job.AttemptCursor != attemptIDs[2] {
		t.Fatalf("attempt exhaustion result = %#v, %v", completed, err)
	}
}

func TestFirestoreEmulatorReceiptPurgeAttemptConcurrentSingleWinner(t *testing.T) {
	fixture, job, _ := prepareEmulatorReceiptPurgeAttempts(t, 2)
	page := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 2)
	type call struct {
		result ingest.ReceiptPurgeMutationResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan call, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), page)
			results <- call{result: result, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded := 0
	conflicted := 0
	for result := range results {
		if result.err == nil {
			succeeded++
			if result.result.Status != ingest.ReceiptPurgeMutationProgressed {
				t.Fatalf("winner result = %#v", result.result)
			}
			continue
		}
		if errors.Is(result.err, ingest.ErrReceiptPurgeMutationConflict) {
			conflicted++
			continue
		}
		t.Fatalf("unexpected concurrent result = %#v, %v", result.result, result.err)
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("success/conflict = %d/%d, want 1/1", succeeded, conflicted)
	}
	stored := readEmulatorReceiptPurgeJob(t, fixture, job.PurgeKey)
	if stored.AttemptDeletedCount != 2 || stored.Revision != job.Revision+1 {
		t.Fatalf("concurrent stored job = %#v", stored)
	}
}

func TestFirestoreEmulatorReceiptPurgeAttemptMalformedChildHolds(t *testing.T) {
	fixture := seedExpiredReceiptPurgeEmulatorFixture(t)
	fixture.store.now = time.Now
	attemptID := emulatorReceiptPurgeAttemptIDs()[0]
	attempt := emulatorReceiptPurgeAttempt(fixture, attemptID, 1)
	data := map[string]any{
		"attempt_id": attempt.AttemptID, "tenant_id": attempt.TenantID,
		"receipt_id": attempt.ReceiptID, "owner_kind": string(attempt.OwnerKind),
		"fencing_token": attempt.FencingToken, "worker_version": attempt.WorkerVersion,
		"status": string(attempt.Status), "outcome": string(attempt.Outcome),
		"started_at": attempt.StartedAt, "completed_at": attempt.CompletedAt,
		"device_id": emulatorFirstReceiptID,
	}
	path := recoveryAttemptDocumentPath(fixture.command.TenantID, fixture.command.ReceiptID, attemptID)
	if _, err := fixture.client.Doc(path).Set(context.Background(), data); err != nil {
		t.Fatalf("seed malformed attempt: %v", err)
	}
	job := admitAndBeginEmulatorReceiptPurgeAttempts(t, fixture)
	page := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 1)
	before := readReceiptPurgeDocument(t, fixture.client, path)
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), page)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
		result.Job.ErrorClass != ingest.ReceiptPurgeErrorChildMalformed ||
		result.Job.AttemptDeletedCount != 0 || result.Job.AttemptCursor != "" {
		t.Fatalf("malformed hold = %#v, %v", result, err)
	}
	after := readReceiptPurgeDocument(t, fixture.client, path)
	if !before.updateTime.Equal(after.updateTime) {
		t.Fatal("malformed attempt was mutated")
	}
}

func TestFirestoreEmulatorReceiptPurgeAttemptTerminalPoisonHolds(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*firestoreRecoveryAttempt, ingest.ReceiptPurgeJob)
		errorClass ingest.ReceiptPurgeErrorClass
	}{
		{
			name: "unsupported worker",
			mutate: func(attempt *firestoreRecoveryAttempt, _ ingest.ReceiptPurgeJob) {
				attempt.WorkerVersion = "recovery-worker.v0"
			},
			errorClass: ingest.ReceiptPurgeErrorUnsupportedVersion,
		},
		{
			name: "post-fence completion",
			mutate: func(attempt *firestoreRecoveryAttempt, job ingest.ReceiptPurgeJob) {
				attempt.StartedAt = job.CreatedAt.Add(-time.Second)
				attempt.CompletedAt = job.CreatedAt.Add(time.Second)
			},
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
		},
		{
			name: "invalid terminal decision",
			mutate: func(attempt *firestoreRecoveryAttempt, _ ingest.ReceiptPurgeJob) {
				attempt.DecisionDomain = "unknown"
			},
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
		},
		{
			name: "forged failed cleanup progress",
			mutate: func(attempt *firestoreRecoveryAttempt, job ingest.ReceiptPurgeJob) {
				*attempt = validFailedCleanupReceiptPurgeAttempt(
					job.TenantID, job.ReceiptID, attempt.AttemptID,
					attempt.FencingToken, job.CreatedAt.Add(-3*time.Minute),
				)
				attempt.CleanupExecutionRevision++
			},
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := seedExpiredReceiptPurgeEmulatorFixture(t)
			fixture.store.now = time.Now
			job := admitAndBeginEmulatorReceiptPurgeAttempts(t, fixture)
			attemptID := emulatorReceiptPurgeAttemptIDs()[0]
			attempt := emulatorReceiptPurgeAttempt(fixture, attemptID, 1)
			test.mutate(&attempt, job)
			path := recoveryAttemptDocumentPath(job.TenantID, job.ReceiptID, attemptID)
			if _, err := fixture.client.Doc(path).Set(context.Background(), attempt); err != nil {
				t.Fatalf("seed poison attempt: %v", err)
			}
			page := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 1)
			result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), page)
			if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
				result.Job.ErrorClass != test.errorClass || result.Job.AttemptDeletedCount != 0 ||
				result.Job.AttemptCursor != "" {
				t.Fatalf("terminal poison hold = %#v, %v", result, err)
			}
			assertEmulatorReceiptPurgeAttemptPresent(t, fixture, attemptID)
		})
	}
}

func TestFirestoreEmulatorReceiptPurgeAttemptDeletesProgressPreservingCleanupFailure(t *testing.T) {
	fixture := seedExpiredReceiptPurgeEmulatorFixture(t)
	fixture.store.now = time.Now
	job := admitAndBeginEmulatorReceiptPurgeAttempts(t, fixture)
	attemptID := emulatorReceiptPurgeAttemptIDs()[0]
	attempt := validFailedCleanupReceiptPurgeAttempt(
		job.TenantID, job.ReceiptID, attemptID, 1, job.CreatedAt.Add(-3*time.Minute),
	)
	path := recoveryAttemptDocumentPath(job.TenantID, job.ReceiptID, attemptID)
	if _, err := fixture.client.Doc(path).Set(context.Background(), attempt); err != nil {
		t.Fatalf("seed progress-preserving cleanup attempt: %v", err)
	}
	page := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 1)
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), page)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationProgressed ||
		result.Job.AttemptDeletedCount != 1 || result.Job.AttemptCursor != attemptID {
		t.Fatalf("progress-preserving cleanup purge = %#v, %v", result, err)
	}
	assertEmulatorReceiptPurgeAttemptMissing(t, fixture, attemptID)
}

func TestFirestoreEmulatorReceiptPurgeAttemptCountOverflowHolds(t *testing.T) {
	fixture, job, attemptIDs := prepareEmulatorReceiptPurgeAttempts(t, 1)
	job.AttemptCursor = "22222222-2222-4222-8222-222222222222"
	job.AttemptDeletedCount = math.MaxInt64
	if _, err := fixture.client.Doc(receiptPurgeJobDocumentPath(job.PurgeKey)).Update(
		context.Background(), []firestore.Update{
			{Path: "attempt_cursor", Value: job.AttemptCursor},
			{Path: "attempt_deleted_count", Value: job.AttemptDeletedCount},
		},
	); err != nil {
		t.Fatalf("seed overflow job: %v", err)
	}
	page := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 1)
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), page)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
		result.Job.ErrorClass != ingest.ReceiptPurgeErrorCountOverflow ||
		result.Job.AttemptCursor != job.AttemptCursor ||
		result.Job.AttemptDeletedCount != math.MaxInt64 {
		t.Fatalf("overflow hold = %#v, %v", result, err)
	}
	assertEmulatorReceiptPurgeAttemptPresent(t, fixture, attemptIDs[0])
}

func TestFirestoreEmulatorReceiptPurgeAttemptResponseLossCorrelation(t *testing.T) {
	fixture, job, attemptIDs := prepareEmulatorReceiptPurgeAttempts(t, 1)
	page := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 1)
	baseRunner := fixture.store.runTransaction
	lossy := *fixture.store
	lossy.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		if err := baseRunner(ctx, operation); err != nil {
			return err
		}
		return errors.New("commit response lost")
	}
	result, err := lossy.CommitReceiptPurgeAttemptPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) || result.Status != "" ||
		ingest.ValidateReceiptPurgeMutationOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("lossy commit = %#v, %v", result, err)
	}
	assertEmulatorReceiptPurgeAttemptMissing(t, fixture, attemptIDs[0])
	outcome, err := fixture.store.GetReceiptPurgeMutationOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationCommitted ||
		outcome.JobRevision != job.Revision+1 || outcome.AttemptDeletedCount != 1 {
		t.Fatalf("response-loss outcome = %#v, %v", outcome, err)
	}
}

func TestFirestoreEmulatorReceiptPurgeAttemptPhaseResponseLossCorrelation(t *testing.T) {
	fixture, job, _ := prepareEmulatorReceiptPurgeAttempts(t, 0)
	empty := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 1)
	baseRunner := fixture.store.runTransaction
	lossy := *fixture.store
	lossy.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		if err := baseRunner(ctx, operation); err != nil {
			return err
		}
		return errors.New("commit response lost")
	}
	result, err := lossy.CompleteReceiptPurgeAttempts(
		context.Background(), ingest.ReceiptPurgeAttemptPhaseCommand{
			Action: ingest.ReceiptPurgeAttemptPhaseComplete, PurgeKey: job.PurgeKey,
			TenantID: job.TenantID, ReceiptID: job.ReceiptID,
			ExpectedJobRevision: job.Revision, EmptyObservation: empty,
		},
	)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) || result.Status != "" ||
		ingest.ValidateReceiptPurgeMutationOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("lossy phase = %#v, %v", result, err)
	}
	outcome, err := fixture.store.GetReceiptPurgeMutationOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationCommitted ||
		outcome.JobStatus != ingest.ReceiptPurgeJobLinkedDocumentsPurging {
		t.Fatalf("phase response-loss outcome = %#v, %v", outcome, err)
	}
}

func TestFirestoreEmulatorReceiptPurgeAttemptStaleEmptyCannotTransition(t *testing.T) {
	fixture, job, _ := prepareEmulatorReceiptPurgeAttempts(t, 0)
	empty := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 2)
	attemptID := emulatorReceiptPurgeAttemptIDs()[0]
	path := recoveryAttemptDocumentPath(job.TenantID, job.ReceiptID, attemptID)
	if _, err := fixture.client.Doc(path).Set(
		context.Background(), emulatorReceiptPurgeAttempt(fixture, attemptID, 1),
	); err != nil {
		t.Fatalf("insert after empty observation: %v", err)
	}
	result, err := fixture.store.CompleteReceiptPurgeAttempts(
		context.Background(), ingest.ReceiptPurgeAttemptPhaseCommand{
			Action: ingest.ReceiptPurgeAttemptPhaseComplete, PurgeKey: job.PurgeKey,
			TenantID: job.TenantID, ReceiptID: job.ReceiptID,
			ExpectedJobRevision: job.Revision, EmptyObservation: empty,
		},
	)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) || result.Status != "" {
		t.Fatalf("stale empty transition = %#v, %v", result, err)
	}
	stored := readEmulatorReceiptPurgeJob(t, fixture, job.PurgeKey)
	if stored.Status != ingest.ReceiptPurgeJobAttemptsPurging || stored.Revision != job.Revision {
		t.Fatalf("stale empty changed job = %#v", stored)
	}
}

func TestFirestoreEmulatorReceiptPurgeAttemptInsertedPrefixCannotBeSkipped(t *testing.T) {
	fixture, job, attemptIDs := prepareEmulatorReceiptPurgeAttempts(t, 2)
	page := listEmulatorReceiptPurgeAttemptPage(t, fixture, job, 2)
	insertedID := "22222222-2222-4222-8222-222222222222"
	if _, err := fixture.client.Doc(recoveryAttemptDocumentPath(
		job.TenantID, job.ReceiptID, insertedID,
	)).Set(context.Background(), emulatorReceiptPurgeAttempt(fixture, insertedID, 3)); err != nil {
		t.Fatalf("insert prefix after discovery: %v", err)
	}
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) || result.Status != "" {
		t.Fatalf("inserted-prefix commit = %#v, %v", result, err)
	}
	stored := readEmulatorReceiptPurgeJob(t, fixture, job.PurgeKey)
	if stored.Revision != job.Revision || stored.AttemptCursor != "" || stored.AttemptDeletedCount != 0 {
		t.Fatalf("inserted prefix advanced job = %#v", stored)
	}
	assertEmulatorReceiptPurgeAttemptPresent(t, fixture, insertedID)
	for _, attemptID := range attemptIDs {
		assertEmulatorReceiptPurgeAttemptPresent(t, fixture, attemptID)
	}
}

func prepareEmulatorReceiptPurgeAttempts(
	t *testing.T,
	count int,
) (*expiredReceiptPurgeEmulatorFixture, ingest.ReceiptPurgeJob, []string) {
	t.Helper()
	fixture := seedExpiredReceiptPurgeEmulatorFixture(t)
	fixture.store.now = time.Now
	allIDs := emulatorReceiptPurgeAttemptIDs()
	if count < 0 || count > len(allIDs) {
		t.Fatalf("invalid attempt count %d", count)
	}
	ids := append([]string(nil), allIDs[:count]...)
	if count > 0 {
		batch := fixture.client.Batch()
		for index, attemptID := range ids {
			batch.Set(
				fixture.client.Doc(recoveryAttemptDocumentPath(
					fixture.command.TenantID, fixture.command.ReceiptID, attemptID,
				)),
				emulatorReceiptPurgeAttempt(fixture, attemptID, int64(index+1)),
			)
		}
		if _, err := batch.Commit(context.Background()); err != nil {
			t.Fatalf("seed purge attempts: %v", err)
		}
	}
	job := admitAndBeginEmulatorReceiptPurgeAttempts(t, fixture)
	return fixture, job, ids
}

func admitAndBeginEmulatorReceiptPurgeAttempts(
	t *testing.T,
	fixture *expiredReceiptPurgeEmulatorFixture,
) ingest.ReceiptPurgeJob {
	t.Helper()
	admission, err := fixture.store.AdmitReceiptPurge(context.Background(), fixture.command)
	if err != nil || admission.Status != ingest.ReceiptPurgeAdmissionCreated {
		t.Fatalf("AdmitReceiptPurge() = %#v, %v", admission, err)
	}
	begin, err := fixture.store.BeginReceiptPurgeAttempts(
		context.Background(), ingest.ReceiptPurgeAttemptPhaseCommand{
			Action: ingest.ReceiptPurgeAttemptPhaseBegin, PurgeKey: admission.Job.PurgeKey,
			TenantID: admission.Job.TenantID, ReceiptID: admission.Job.ReceiptID,
			ExpectedJobRevision: admission.Job.Revision,
		},
	)
	if err != nil || begin.Job.Status != ingest.ReceiptPurgeJobAttemptsPurging {
		t.Fatalf("BeginReceiptPurgeAttempts() = %#v, %v", begin, err)
	}
	return begin.Job
}

func listEmulatorReceiptPurgeAttemptPage(
	t *testing.T,
	fixture *expiredReceiptPurgeEmulatorFixture,
	job ingest.ReceiptPurgeJob,
	pageSize int,
) ingest.ReceiptPurgePageObservation {
	t.Helper()
	observation, err := fixture.store.ListReceiptPurgeAttemptPage(
		context.Background(), ingest.ReceiptPurgePageRequest{
			PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
			Kind: ingest.ReceiptPurgePageAttempts, ExpectedJobStatus: job.Status,
			ExpectedJobRevision: job.Revision, AfterDocumentID: job.AttemptCursor,
			PageSize: pageSize,
		},
	)
	if err != nil {
		t.Fatalf("ListReceiptPurgeAttemptPage() = %v", err)
	}
	return observation
}

func emulatorReceiptPurgeAttempt(
	fixture *expiredReceiptPurgeEmulatorFixture,
	attemptID string,
	token int64,
) firestoreRecoveryAttempt {
	startedAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Millisecond)
	return validForwardReceiptPurgeAttempt(
		fixture.command.TenantID, fixture.command.ReceiptID, attemptID, token, startedAt,
	)
}

func emulatorReceiptPurgeAttemptIDs() []string {
	return []string{
		"33333333-3333-4333-8333-333333333333",
		"44444444-4444-4444-8444-444444444444",
		"55555555-5555-4555-8555-555555555555",
	}
}

func readEmulatorReceiptPurgeJob(
	t *testing.T,
	fixture *expiredReceiptPurgeEmulatorFixture,
	purgeKey string,
) ingest.ReceiptPurgeJob {
	t.Helper()
	document := readReceiptPurgeDocument(t, fixture.client, receiptPurgeJobDocumentPath(purgeKey))
	var stored firestoreReceiptPurgeJob
	if document.snapshot.DataTo(&stored) != nil || ingest.ValidateReceiptPurgeJob(stored.toDomain()) != nil {
		t.Fatal("decode receipt purge job")
	}
	return stored.toDomain()
}

func assertEmulatorReceiptPurgeAttemptMissing(
	t *testing.T,
	fixture *expiredReceiptPurgeEmulatorFixture,
	attemptID string,
) {
	t.Helper()
	_, err := fixture.client.Doc(recoveryAttemptDocumentPath(
		fixture.command.TenantID, fixture.command.ReceiptID, attemptID,
	)).Get(context.Background())
	if status.Code(err) != codes.NotFound {
		t.Fatalf("attempt %s still present: %v", attemptID, err)
	}
}

func assertEmulatorReceiptPurgeAttemptPresent(
	t *testing.T,
	fixture *expiredReceiptPurgeEmulatorFixture,
	attemptID string,
) {
	t.Helper()
	if _, err := fixture.client.Doc(recoveryAttemptDocumentPath(
		fixture.command.TenantID, fixture.command.ReceiptID, attemptID,
	)).Get(context.Background()); err != nil {
		t.Fatalf("attempt %s missing: %v", attemptID, err)
	}
}
