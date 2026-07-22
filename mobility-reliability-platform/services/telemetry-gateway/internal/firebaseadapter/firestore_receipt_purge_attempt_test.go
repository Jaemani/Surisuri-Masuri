package firebaseadapter

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreReceiptPurgeAttemptPageRereadsBeforeAtomicDeleteAndProgress(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	observation, err := fixture.store.ListReceiptPurgeAttemptPage(
		context.Background(), fixture.pageRequest(2),
	)
	if err != nil || len(observation.DeleteDocumentIDs) != 2 ||
		observation.LookaheadDocumentID != fixture.pageIDs[2] {
		t.Fatalf("ListReceiptPurgeAttemptPage() = %#v, %v", observation, err)
	}
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationProgressed ||
		result.Job.AttemptCursor != fixture.pageIDs[1] ||
		result.Job.AttemptDeletedCount != 2 || result.Job.Revision != fixture.job.Revision+1 ||
		ingest.ValidateReceiptPurgeMutationOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("CommitReceiptPurgeAttemptPage() = %#v, %v", result, err)
	}
	if len(fixture.transaction.deletes) != 2 ||
		fixture.transaction.deletes[0] != recoveryAttemptDocumentPath(
			fixture.job.TenantID, fixture.job.ReceiptID, fixture.pageIDs[0],
		) || fixture.transaction.deletes[1] != recoveryAttemptDocumentPath(
		fixture.job.TenantID, fixture.job.ReceiptID, fixture.pageIDs[1],
	) {
		t.Fatalf("delete paths = %#v", fixture.transaction.deletes)
	}
	for _, path := range fixture.transaction.deletes {
		if path == recoveryAttemptDocumentPath(fixture.job.TenantID, fixture.job.ReceiptID, fixture.pageIDs[2]) {
			t.Fatal("lookahead was deleted")
		}
	}
	if len(fixture.transaction.updates) != 1 ||
		fixture.transaction.updates[0].path != receiptPurgeJobDocumentPath(fixture.job.PurgeKey) {
		t.Fatalf("job updates = %#v", fixture.transaction.updates)
	}
	updates := firestoreUpdateMap(fixture.transaction.updates[0].updates)
	if updates["attempt_cursor"] != fixture.pageIDs[1] ||
		updates["attempt_deleted_count"] != int64(2) ||
		updates["revision"] != fixture.job.Revision+1 {
		t.Fatalf("progress updates = %#v", updates)
	}
	readIndex := operationIndex(fixture.transaction.operations, "attempt-read")
	receiptIndex := operationIndex(fixture.transaction.operations, "receipt-read")
	queryIndex := lastOperationIndex(fixture.transaction.operations, "query")
	deleteIndex := operationIndex(fixture.transaction.operations, "delete")
	updateIndex := operationIndex(fixture.transaction.operations, "update")
	if receiptIndex < 0 || queryIndex <= receiptIndex || readIndex <= queryIndex ||
		deleteIndex <= readIndex || updateIndex <= deleteIndex {
		t.Fatalf("operation order = %#v", fixture.transaction.operations)
	}
}

func TestFirestoreReceiptPurgeAttemptPoisonHoldsWithoutDelete(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*receiptPurgeAttemptAdapterFixture)
		errorClass ingest.ReceiptPurgeErrorClass
	}{
		{
			name: "malformed child",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				read := fixture.transaction.attemptReads[fixture.pageIDs[0]]
				read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
				fixture.transaction.attemptReads[fixture.pageIDs[0]] = read
			},
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
		},
		{
			name: "missing proposed child",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				read := fixture.transaction.attemptReads[fixture.pageIDs[0]]
				read.Exists = false
				read.Attempt = firestoreRecoveryAttempt{}
				fixture.transaction.attemptReads[fixture.pageIDs[0]] = read
			},
			errorClass: ingest.ReceiptPurgeErrorLinkageDrift,
		},
		{
			name: "partial receipt fence",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				receipt := fixture.transaction.receipts[admissionReceiptPath()]
				receipt.PurgeStartedAt = time.Time{}
				receipt.PurgeFenceVersion = ""
				fixture.transaction.receipts[admissionReceiptPath()] = receipt
			},
			errorClass: ingest.ReceiptPurgeErrorFencePartial,
		},
		{
			name: "foreign child",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				read := fixture.transaction.attemptReads[fixture.pageIDs[0]]
				read.Attempt.TenantID = "77777777-7777-4777-8777-777777777777"
				fixture.transaction.attemptReads[fixture.pageIDs[0]] = read
			},
			errorClass: ingest.ReceiptPurgeErrorChildForeign,
		},
		{
			name: "unsupported worker",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				read := fixture.transaction.attemptReads[fixture.pageIDs[0]]
				read.Attempt.WorkerVersion = "recovery-worker.v0"
				fixture.transaction.attemptReads[fixture.pageIDs[0]] = read
			},
			errorClass: ingest.ReceiptPurgeErrorUnsupportedVersion,
		},
		{
			name: "nonterminal child",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				read := fixture.transaction.attemptReads[fixture.pageIDs[0]]
				read.Attempt = firestoreRecoveryAttempt{
					AttemptID: read.Attempt.AttemptID, TenantID: read.Attempt.TenantID,
					ReceiptID: read.Attempt.ReceiptID, OwnerKind: ingest.LeaseOwnerSweeper,
					FencingToken:  read.Attempt.FencingToken,
					WorkerVersion: ingest.RecoveryWorkerVersion,
					Status:        ingest.RecoveryAttemptStarted, StartedAt: read.Attempt.StartedAt,
				}
				fixture.transaction.attemptReads[fixture.pageIDs[0]] = read
			},
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
		},
		{
			name: "post-fence terminal timestamp",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				read := fixture.transaction.attemptReads[fixture.pageIDs[0]]
				read.Attempt.StartedAt = fixture.job.CreatedAt.Add(-time.Second)
				read.Attempt.CompletedAt = fixture.job.CreatedAt.Add(time.Second)
				fixture.transaction.attemptReads[fixture.pageIDs[0]] = read
			},
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
		},
		{
			name: "forged failed cleanup progress",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				read := fixture.transaction.attemptReads[fixture.pageIDs[0]]
				read.Attempt = validFailedCleanupReceiptPurgeAttempt(
					fixture.job.TenantID, fixture.job.ReceiptID, read.DocumentID,
					read.Attempt.FencingToken, fixture.job.CreatedAt.Add(-3*time.Minute),
				)
				read.Attempt.CleanupExecutionRevision++
				fixture.transaction.attemptReads[fixture.pageIDs[0]] = read
			},
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
		},
		{
			name: "fully populated wrong fence",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				receipt := fixture.transaction.receipts[admissionReceiptPath()]
				receipt.PurgeJobID = strings.Repeat("f", 64)
				fixture.transaction.receipts[admissionReceiptPath()] = receipt
			},
			errorClass: ingest.ReceiptPurgeErrorLinkageDrift,
		},
		{
			name: "receipt linkage hash drift",
			mutate: func(fixture *receiptPurgeAttemptAdapterFixture) {
				receipt := fixture.transaction.receipts[admissionReceiptPath()]
				receipt.ReservationKey = strings.Repeat("e", 64)
				fixture.transaction.receipts[admissionReceiptPath()] = receipt
			},
			errorClass: ingest.ReceiptPurgeErrorLinkageDrift,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReceiptPurgeAttemptAdapterFixture(t)
			observation, err := fixture.store.ListReceiptPurgeAttemptPage(
				context.Background(), fixture.pageRequest(2),
			)
			if err != nil {
				t.Fatalf("ListReceiptPurgeAttemptPage() = %v", err)
			}
			test.mutate(fixture)
			result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
			if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
				result.Job.Status != ingest.ReceiptPurgeJobHold ||
				result.Job.ErrorClass != test.errorClass || len(fixture.transaction.deletes) != 0 {
				t.Fatalf("hold result = %#v, deletes=%#v, err=%v", result, fixture.transaction.deletes, err)
			}
			if len(fixture.transaction.updates) != 1 {
				t.Fatalf("hold updates = %#v", fixture.transaction.updates)
			}
			updates := firestoreUpdateMap(fixture.transaction.updates[0].updates)
			if updates["status"] != string(ingest.ReceiptPurgeJobHold) ||
				updates["held_from_status"] != string(ingest.ReceiptPurgeJobAttemptsPurging) ||
				updates["error_class"] != string(test.errorClass) {
				t.Fatalf("hold update fields = %#v", updates)
			}
		})
	}
}

func TestFirestoreReceiptPurgeAttemptDeletesValidProgressPreservingCleanupFailure(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	documentID := fixture.pageIDs[0]
	read := fixture.transaction.attemptReads[documentID]
	read.Attempt = validFailedCleanupReceiptPurgeAttempt(
		fixture.job.TenantID, fixture.job.ReceiptID, documentID,
		read.Attempt.FencingToken, fixture.job.CreatedAt.Add(-3*time.Minute),
	)
	fixture.transaction.attemptReads[documentID] = read
	observation, err := fixture.store.ListReceiptPurgeAttemptPage(
		context.Background(), fixture.pageRequest(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationProgressed ||
		result.Job.AttemptDeletedCount != 1 || result.Job.AttemptCursor != documentID ||
		len(fixture.transaction.deletes) != 1 {
		t.Fatalf("progress-preserving cleanup purge = %#v deletes=%#v, %v", result, fixture.transaction.deletes, err)
	}
}

func TestFirestoreReceiptPurgeAttemptCountOverflowHoldsBeforeDelete(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	overflow := fixture.job
	overflow.AttemptCursor = "22222222-2222-4222-8222-222222222222"
	overflow.AttemptDeletedCount = math.MaxInt64
	fixture.job = overflow
	fixture.setJob(overflow, fixture.now)
	observation, err := fixture.store.ListReceiptPurgeAttemptPage(
		context.Background(), fixture.pageRequest(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
		result.Job.ErrorClass != ingest.ReceiptPurgeErrorCountOverflow ||
		result.Job.AttemptCursor != overflow.AttemptCursor ||
		result.Job.AttemptDeletedCount != math.MaxInt64 ||
		len(fixture.transaction.deletes) != 0 {
		t.Fatalf("overflow hold = %#v deletes=%#v, %v", result, fixture.transaction.deletes, err)
	}
}

func TestFirestoreReceiptPurgeAttemptMaxRevisionWritesNothing(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	maximum := fixture.job
	maximum.Revision = math.MaxInt64
	fixture.job = maximum
	fixture.setJob(maximum, fixture.now)
	observation, err := fixture.store.ListReceiptPurgeAttemptPage(
		context.Background(), fixture.pageRequest(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
	if err == nil || result.Status != "" || len(fixture.transaction.deletes) != 0 ||
		len(fixture.transaction.updates) != 0 {
		t.Fatalf("max revision = %#v deletes=%#v updates=%#v, %v", result, fixture.transaction.deletes, fixture.transaction.updates, err)
	}
}

func TestFirestoreReceiptPurgeAttemptStaleOrQueryFailureWritesNothing(t *testing.T) {
	t.Run("forged observation cannot omit current prefix", func(t *testing.T) {
		fixture := newReceiptPurgeAttemptAdapterFixture(t)
		forged, err := ingest.BuildReceiptPurgePageObservation(
			fixture.pageRequest(2), fixture.pageIDs[1:], fixture.now,
		)
		if err != nil {
			t.Fatal(err)
		}
		result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), forged)
		if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) || result.Status != "" ||
			len(fixture.transaction.deletes) != 0 || len(fixture.transaction.updates) != 0 {
			t.Fatalf("forged omitted-prefix commit = %#v deletes=%#v updates=%#v, %v", result, fixture.transaction.deletes, fixture.transaction.updates, err)
		}
	})

	t.Run("inserted prefix changes exact page", func(t *testing.T) {
		fixture := newReceiptPurgeAttemptAdapterFixture(t)
		observation, err := fixture.store.ListReceiptPurgeAttemptPage(
			context.Background(), fixture.pageRequest(2),
		)
		if err != nil {
			t.Fatal(err)
		}
		fixture.transaction.pageIDs = append(
			[]string{"22222222-2222-4222-8222-222222222222"}, fixture.transaction.pageIDs...,
		)
		result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
		if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) || result.Status != "" ||
			len(fixture.transaction.deletes) != 0 || len(fixture.transaction.updates) != 0 {
			t.Fatalf("inserted prefix commit = %#v deletes=%#v updates=%#v, %v", result, fixture.transaction.deletes, fixture.transaction.updates, err)
		}
	})

	t.Run("inserted middle changes exact page", func(t *testing.T) {
		fixture := newReceiptPurgeAttemptAdapterFixture(t)
		observation, err := fixture.store.ListReceiptPurgeAttemptPage(
			context.Background(), fixture.pageRequest(2),
		)
		if err != nil {
			t.Fatal(err)
		}
		fixture.transaction.pageIDs = append(
			append([]string(nil), fixture.transaction.pageIDs[:1]...),
			append([]string{"3fffffff-ffff-4fff-8fff-ffffffffffff"}, fixture.transaction.pageIDs[1:]...)...,
		)
		result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
		if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) || result.Status != "" ||
			len(fixture.transaction.deletes) != 0 || len(fixture.transaction.updates) != 0 {
			t.Fatalf("inserted-middle commit = %#v deletes=%#v updates=%#v, %v", result, fixture.transaction.deletes, fixture.transaction.updates, err)
		}
	})

	t.Run("stale revision", func(t *testing.T) {
		fixture := newReceiptPurgeAttemptAdapterFixture(t)
		observation, err := fixture.store.ListReceiptPurgeAttemptPage(
			context.Background(), fixture.pageRequest(2),
		)
		if err != nil {
			t.Fatal(err)
		}
		progressed := fixture.job
		progressed.Revision++
		progressed.UpdatedAt = fixture.now
		fixture.setJob(progressed, fixture.now)
		result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
		if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) ||
			result.Status != "" || result.Job.PurgeKey != "" ||
			result.OutcomeQuery.Kind != "" || len(result.OutcomeQuery.DeleteDocumentIDs) != 0 ||
			len(fixture.transaction.deletes) != 0 ||
			len(fixture.transaction.updates) != 0 {
			t.Fatalf("stale commit = %#v, deletes=%#v updates=%#v err=%v", result, fixture.transaction.deletes, fixture.transaction.updates, err)
		}
	})

	t.Run("query unavailable", func(t *testing.T) {
		fixture := newReceiptPurgeAttemptAdapterFixture(t)
		fixture.transaction.queryErr = errors.New("query unavailable")
		observation, err := fixture.store.ListReceiptPurgeAttemptPage(
			context.Background(), fixture.pageRequest(2),
		)
		if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) ||
			observation.Request != (ingest.ReceiptPurgePageRequest{}) ||
			len(observation.DeleteDocumentIDs) != 0 || observation.LookaheadDocumentID != "" ||
			!observation.ReadAt.IsZero() ||
			len(fixture.transaction.deletes) != 0 || len(fixture.transaction.updates) != 0 {
			t.Fatalf("query failure = %#v, %v", observation, err)
		}
	})
}

func TestFirestoreReceiptPurgeAttemptPreservesQueryOnCommitResponseLoss(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	observation, err := fixture.store.ListReceiptPurgeAttemptPage(
		context.Background(), fixture.pageRequest(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	commitResponseLost := errors.New("commit response lost")
	fixture.store.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		if operationErr := operation(ctx, fixture.transaction); operationErr != nil {
			return operationErr
		}
		return commitResponseLost
	}
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) || result.Status != "" ||
		ingest.ValidateReceiptPurgeMutationOutcomeQuery(result.OutcomeQuery) != nil ||
		len(fixture.transaction.deletes) != 2 || len(fixture.transaction.updates) != 1 {
		t.Fatalf("response loss result = %#v, deletes=%#v updates=%#v err=%v", result, fixture.transaction.deletes, fixture.transaction.updates, err)
	}
}

func TestFirestoreReceiptPurgeAttemptOutcomeIsReadOnly(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	observation, err := fixture.store.ListReceiptPurgeAttemptPage(
		context.Background(), fixture.pageRequest(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}
	fixture.transaction.deletes = nil
	fixture.transaction.updates = nil
	outcome, err := fixture.store.GetReceiptPurgeMutationOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationNotCommitted {
		t.Fatalf("not committed outcome = %#v, %v", outcome, err)
	}
	fixture.setJob(result.Job, fixture.now)
	for _, documentID := range result.OutcomeQuery.DeleteDocumentIDs {
		read := fixture.transaction.attemptReads[documentID]
		read.Exists = false
		read.Attempt = firestoreRecoveryAttempt{}
		fixture.transaction.attemptReads[documentID] = read
	}
	outcome, err = fixture.store.GetReceiptPurgeMutationOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationCommitted ||
		outcome.JobRevision != result.Job.Revision || len(fixture.transaction.deletes) != 0 ||
		len(fixture.transaction.updates) != 0 {
		t.Fatalf("committed outcome = %#v, deletes=%#v updates=%#v err=%v", outcome, fixture.transaction.deletes, fixture.transaction.updates, err)
	}
}

func TestFirestoreReceiptPurgeAttemptOutcomeTreatsChildDriftAsUnverifiable(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	observation, err := fixture.store.ListReceiptPurgeAttemptPage(
		context.Background(), fixture.pageRequest(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.CommitReceiptPurgeAttemptPage(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}
	read := fixture.transaction.attemptReads[result.OutcomeQuery.DeleteDocumentIDs[0]]
	read.Attempt.ActionHash = strings.Repeat("f", 64)
	read.DocumentDigest = receiptPurgeAttemptDocumentDigest(read.Attempt)
	fixture.transaction.attemptReads[read.DocumentID] = read
	outcome, err := fixture.store.GetReceiptPurgeMutationOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationUnverifiable {
		t.Fatalf("changed known field outcome = %#v, %v", outcome, err)
	}
	read.ErrorClass = ingest.ReceiptPurgeErrorChildForeign
	read.DocumentDigest = ""
	fixture.transaction.attemptReads[read.DocumentID] = read
	outcome, err = fixture.store.GetReceiptPurgeMutationOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationUnverifiable {
		t.Fatalf("invalid child outcome = %#v, %v", outcome, err)
	}
}

func TestReceiptPurgeAttemptRawDigestDistinguishesOptionalFieldPresence(t *testing.T) {
	without := map[string]any{"attempt_id": "a", "status": "completed"}
	withEmpty := map[string]any{"attempt_id": "a", "status": "completed", "release_code": ""}
	if receiptPurgeAttemptDocumentDigest(without) == receiptPurgeAttemptDocumentDigest(withEmpty) {
		t.Fatal("raw-map digest erased optional field presence")
	}
}

func TestClassifyReceiptPurgeAttemptValidatesTerminalUnion(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	id := fixture.pageIDs[0]
	forward := validForwardReceiptPurgeAttempt(
		fixture.job.TenantID, fixture.job.ReceiptID, id, 1, fixture.job.CreatedAt.Add(-time.Minute),
	)
	cleanup := validCleanupReceiptPurgeAttempt(
		fixture.job.TenantID, fixture.job.ReceiptID, id, 1, fixture.job.CreatedAt.Add(-2*time.Minute),
	)
	if got := classifyReceiptPurgeAttempt(id, fixture.job.TenantID, fixture.job.ReceiptID, forward); got != "" {
		t.Fatalf("valid forward classified %q", got)
	}
	if got := classifyReceiptPurgeAttempt(id, fixture.job.TenantID, fixture.job.ReceiptID, cleanup); got != "" {
		t.Fatalf("valid cleanup classified %q", got)
	}
	tests := []struct {
		name    string
		attempt firestoreRecoveryAttempt
	}{
		{name: "unknown forward decision", attempt: func() firestoreRecoveryAttempt {
			value := forward
			value.DecisionDomain = "unknown"
			return value
		}()},
		{name: "failed action residue", attempt: func() firestoreRecoveryAttempt {
			value := forward
			value.Status = ingest.RecoveryAttemptFailed
			value.DecisionDomain = ""
			value.AuthorizationDisposition = ""
			value.Outcome = ""
			value.Action = ingest.ForwardRecoveryActionReleaseLease
			value.ActionHash = ""
			value.ReleaseCode = ""
			value.CompletedAt = time.Time{}
			value.FailureCode = ingest.RecoveryAttemptFailureInvalidContract
			value.FailedAt = value.StartedAt.Add(time.Second)
			return value
		}()},
		{name: "unknown cleanup schema", attempt: func() firestoreRecoveryAttempt {
			value := cleanup
			value.CleanupSchemaVersion = "telemetry-cleanup-execution.v0"
			return value
		}()},
		{name: "cleanup phase outcome mismatch", attempt: func() firestoreRecoveryAttempt {
			value := cleanup
			value.CleanupPhase = ingest.CleanupExecutionPhaseManifestAbsenceConfirmed
			value.CleanupExecutionRevision = 7
			return value
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyReceiptPurgeAttempt(
				id, fixture.job.TenantID, fixture.job.ReceiptID, test.attempt,
			); got == "" {
				t.Fatal("malformed terminal union accepted")
			}
		})
	}
}

func TestClassifyReceiptPurgeAttemptAcceptsFailedCleanupProgressPhases(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	id := fixture.pageIDs[0]
	base := validFailedCleanupReceiptPurgeAttempt(
		fixture.job.TenantID, fixture.job.ReceiptID, id, 1,
		fixture.job.CreatedAt.Add(-3*time.Minute),
	)
	planned := base
	planned.CleanupPhase = ingest.CleanupExecutionPhasePlanned
	planned.CleanupExecutionRevision = 1
	planned.CleanupRawDispatchAt = time.Time{}
	planned.CleanupRawDeleteOutcome = ""
	planned.CleanupRawOutcomeRecordedAt = time.Time{}
	rawDispatch := base
	rawDispatch.CleanupPhase = ingest.CleanupExecutionPhaseRawDispatchRecorded
	rawDispatch.CleanupExecutionRevision = 2
	rawDispatch.CleanupRawDeleteOutcome = ""
	rawDispatch.CleanupRawOutcomeRecordedAt = time.Time{}
	rawAbsence := base
	rawAbsence.CleanupPhase = ingest.CleanupExecutionPhaseRawAbsenceConfirmed
	rawAbsence.CleanupExecutionRevision = 4
	rawAbsence.CleanupRawAuditOutcome = ingest.CleanupAuditConfirmedAbsent
	rawAbsence.CleanupRawAuditedAt = base.StartedAt.Add(30 * time.Second)
	manifestDispatch := rawAbsence
	manifestDispatch.CleanupPhase = ingest.CleanupExecutionPhaseManifestDispatchRecorded
	manifestDispatch.CleanupExecutionRevision = 5
	manifestDispatch.CleanupManifestDispatchAt = base.StartedAt.Add(35 * time.Second)
	manifestOutcome := manifestDispatch
	manifestOutcome.CleanupPhase = ingest.CleanupExecutionPhaseManifestOutcomeRecorded
	manifestOutcome.CleanupExecutionRevision = 6
	manifestOutcome.CleanupManifestDeleteOutcome = ingest.CleanupDeleteNotAttempted
	manifestOutcome.CleanupManifestOutcomeRecordedAt = base.StartedAt.Add(40 * time.Second)
	manifestAbsence := manifestOutcome
	manifestAbsence.CleanupPhase = ingest.CleanupExecutionPhaseManifestAbsenceConfirmed
	manifestAbsence.CleanupExecutionRevision = 7
	manifestAbsence.CleanupManifestAuditOutcome = ingest.CleanupAuditConfirmedAbsent
	manifestAbsence.CleanupManifestAuditedAt = base.StartedAt.Add(45 * time.Second)
	for _, test := range []struct {
		name    string
		attempt firestoreRecoveryAttempt
	}{
		{name: "planned", attempt: planned},
		{name: "raw dispatch", attempt: rawDispatch},
		{name: "raw outcome", attempt: base},
		{name: "raw absence", attempt: rawAbsence},
		{name: "manifest dispatch", attempt: manifestDispatch},
		{name: "manifest outcome", attempt: manifestOutcome},
		{name: "manifest absence", attempt: manifestAbsence},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyReceiptPurgeAttempt(
				id, fixture.job.TenantID, fixture.job.ReceiptID, test.attempt,
			); got != "" {
				t.Fatalf("valid progress-preserving failure classified %q", got)
			}
		})
	}
}

func TestFirestoreReceiptPurgeAttemptPhaseBeginAndExactEmptyComplete(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	planned, err := ingest.BuildPostFenceReceiptPurgeJob(fixture.admission.command, fixture.admission.now)
	if err != nil {
		t.Fatal(err)
	}
	fixture.setJob(planned, fixture.admission.now)
	fixture.transaction.pageIDs = nil
	fixture.transaction.updates = nil
	beginCommand := ingest.ReceiptPurgeAttemptPhaseCommand{
		Action: ingest.ReceiptPurgeAttemptPhaseBegin, PurgeKey: planned.PurgeKey,
		TenantID: planned.TenantID, ReceiptID: planned.ReceiptID,
		ExpectedJobRevision: planned.Revision,
	}
	begin, err := fixture.store.BeginReceiptPurgeAttempts(context.Background(), beginCommand)
	if err != nil || begin.Status != ingest.ReceiptPurgeMutationPhaseTransitioned ||
		begin.Job.Status != ingest.ReceiptPurgeJobAttemptsPurging {
		t.Fatalf("BeginReceiptPurgeAttempts() = %#v, %v", begin, err)
	}
	fixture.setJob(begin.Job, fixture.now)
	fixture.transaction.updates = nil
	request := fixture.pageRequest(2)
	request.ExpectedJobRevision = begin.Job.Revision
	request.AfterDocumentID = begin.Job.AttemptCursor
	empty, err := fixture.store.ListReceiptPurgeAttemptPage(context.Background(), request)
	if err != nil || len(empty.DeleteDocumentIDs) != 0 || empty.LookaheadDocumentID != "" {
		t.Fatalf("empty page = %#v, %v", empty, err)
	}
	complete, err := fixture.store.CompleteReceiptPurgeAttempts(
		context.Background(), ingest.ReceiptPurgeAttemptPhaseCommand{
			Action: ingest.ReceiptPurgeAttemptPhaseComplete, PurgeKey: begin.Job.PurgeKey,
			TenantID: begin.Job.TenantID, ReceiptID: begin.Job.ReceiptID,
			ExpectedJobRevision: begin.Job.Revision, EmptyObservation: empty,
		},
	)
	if err != nil || complete.Status != ingest.ReceiptPurgeMutationPhaseTransitioned ||
		complete.Job.Status != ingest.ReceiptPurgeJobLinkedDocumentsPurging ||
		len(fixture.transaction.updates) != 1 {
		t.Fatalf("CompleteReceiptPurgeAttempts() = %#v updates=%#v, %v", complete, fixture.transaction.updates, err)
	}
}

func TestFirestoreReceiptPurgeAttemptPhaseStructuralDriftHolds(t *testing.T) {
	fixture := newReceiptPurgeAttemptAdapterFixture(t)
	planned, err := ingest.BuildPostFenceReceiptPurgeJob(fixture.admission.command, fixture.admission.now)
	if err != nil {
		t.Fatal(err)
	}
	fixture.setJob(planned, fixture.admission.now)
	receipt := fixture.transaction.receipts[admissionReceiptPath()]
	receipt.PurgeFenceVersion = ""
	fixture.transaction.receipts[admissionReceiptPath()] = receipt
	result, err := fixture.store.BeginReceiptPurgeAttempts(
		context.Background(), ingest.ReceiptPurgeAttemptPhaseCommand{
			Action: ingest.ReceiptPurgeAttemptPhaseBegin, PurgeKey: planned.PurgeKey,
			TenantID: planned.TenantID, ReceiptID: planned.ReceiptID,
			ExpectedJobRevision: planned.Revision,
		},
	)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
		result.Job.Status != ingest.ReceiptPurgeJobHold ||
		result.Job.HeldFromStatus != ingest.ReceiptPurgeJobPlanned ||
		result.Job.ErrorClass != ingest.ReceiptPurgeErrorFencePartial ||
		len(fixture.transaction.deletes) != 0 {
		t.Fatalf("planned poison hold = %#v deletes=%#v, %v", result, fixture.transaction.deletes, err)
	}
}

func TestFirestoreReceiptPurgeAttemptPhaseResponseLossCorrelation(t *testing.T) {
	t.Run("begin", func(t *testing.T) {
		fixture := newReceiptPurgeAttemptAdapterFixture(t)
		planned, err := ingest.BuildPostFenceReceiptPurgeJob(
			fixture.admission.command, fixture.admission.now,
		)
		if err != nil {
			t.Fatal(err)
		}
		fixture.setJob(planned, fixture.admission.now)
		baseRunner := fixture.store.runTransaction
		fixture.store.runTransaction = receiptPurgeLostResponseRunner(baseRunner)
		result, err := fixture.store.BeginReceiptPurgeAttempts(
			context.Background(), ingest.ReceiptPurgeAttemptPhaseCommand{
				Action: ingest.ReceiptPurgeAttemptPhaseBegin, PurgeKey: planned.PurgeKey,
				TenantID: planned.TenantID, ReceiptID: planned.ReceiptID,
				ExpectedJobRevision: planned.Revision,
			},
		)
		if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) || result.Status != "" ||
			ingest.ValidateReceiptPurgeMutationOutcomeQuery(result.OutcomeQuery) != nil {
			t.Fatalf("lost begin = %#v, %v", result, err)
		}
		fixture.store.runTransaction = baseRunner
		outcome, err := fixture.store.GetReceiptPurgeMutationOutcome(
			context.Background(), result.OutcomeQuery,
		)
		if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationNotCommitted {
			t.Fatalf("begin pre-state = %#v, %v", outcome, err)
		}
		fixture.setJob(result.Job, fixture.now)
		outcome, err = fixture.store.GetReceiptPurgeMutationOutcome(
			context.Background(), result.OutcomeQuery,
		)
		if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationCommitted {
			t.Fatalf("begin committed = %#v, %v", outcome, err)
		}
	})

	t.Run("complete", func(t *testing.T) {
		fixture := newReceiptPurgeAttemptAdapterFixture(t)
		fixture.transaction.pageIDs = nil
		empty, err := fixture.store.ListReceiptPurgeAttemptPage(
			context.Background(), fixture.pageRequest(2),
		)
		if err != nil {
			t.Fatal(err)
		}
		baseRunner := fixture.store.runTransaction
		fixture.store.runTransaction = receiptPurgeLostResponseRunner(baseRunner)
		result, err := fixture.store.CompleteReceiptPurgeAttempts(
			context.Background(), ingest.ReceiptPurgeAttemptPhaseCommand{
				Action: ingest.ReceiptPurgeAttemptPhaseComplete, PurgeKey: fixture.job.PurgeKey,
				TenantID: fixture.job.TenantID, ReceiptID: fixture.job.ReceiptID,
				ExpectedJobRevision: fixture.job.Revision, EmptyObservation: empty,
			},
		)
		if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) || result.Status != "" ||
			ingest.ValidateReceiptPurgeMutationOutcomeQuery(result.OutcomeQuery) != nil {
			t.Fatalf("lost complete = %#v, %v", result, err)
		}
		fixture.store.runTransaction = baseRunner
		outcome, err := fixture.store.GetReceiptPurgeMutationOutcome(
			context.Background(), result.OutcomeQuery,
		)
		if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationNotCommitted {
			t.Fatalf("complete pre-state = %#v, %v", outcome, err)
		}
		fixture.setJob(result.Job, fixture.now)
		outcome, err = fixture.store.GetReceiptPurgeMutationOutcome(
			context.Background(), result.OutcomeQuery,
		)
		if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationCommitted {
			t.Fatalf("complete committed = %#v, %v", outcome, err)
		}
	})
}

func receiptPurgeLostResponseRunner(
	base runAdmissionTransaction,
) runAdmissionTransaction {
	return func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		if err := base(ctx, operation); err != nil {
			return err
		}
		return errors.New("commit response lost")
	}
}

func TestReceiptPurgeAttemptDocumentShapeRejectsUnknownAndMissing(t *testing.T) {
	valid := map[string]any{
		"attempt_id": "a", "tenant_id": "t", "receipt_id": "r", "owner_kind": "sweeper",
		"fencing_token": int64(1), "worker_version": ingest.RecoveryWorkerVersion,
		"status": "completed", "started_at": admissionTestNow(),
	}
	if err := validateReceiptPurgeAttemptDocumentShape(valid); err != nil {
		t.Fatalf("valid shape rejected: %v", err)
	}
	valid["device_id"] = admissionDeviceID
	if err := validateReceiptPurgeAttemptDocumentShape(valid); err == nil {
		t.Fatal("unknown field accepted")
	}
	delete(valid, "device_id")
	valid["release_code"] = nil
	if err := validateReceiptPurgeAttemptDocumentShape(valid); err == nil {
		t.Fatal("known optional null accepted")
	}
	delete(valid, "release_code")
	valid["fencing_token"] = "1"
	if err := validateReceiptPurgeAttemptDocumentShape(valid); err == nil {
		t.Fatal("wrong known field type accepted")
	}
	valid["fencing_token"] = int64(1)
	delete(valid, "worker_version")
	if err := validateReceiptPurgeAttemptDocumentShape(valid); err == nil {
		t.Fatal("missing worker schema accepted")
	}
}

type receiptPurgeAttemptAdapterFixture struct {
	admission   *receiptPurgeAdmissionFixture
	transaction *fakeReceiptPurgeAttemptTransaction
	store       *FirestoreAdmissionStore
	job         ingest.ReceiptPurgeJob
	pageIDs     []string
	now         time.Time
}

func newReceiptPurgeAttemptAdapterFixture(t *testing.T) *receiptPurgeAttemptAdapterFixture {
	t.Helper()
	admission := newReceiptPurgeAdmissionFixture(t)
	job, err := ingest.BuildPostFenceReceiptPurgeJob(admission.command, admission.now)
	if err != nil {
		t.Fatalf("BuildPostFenceReceiptPurgeJob() = %v", err)
	}
	job.Status = ingest.ReceiptPurgeJobAttemptsPurging
	job.Revision = 2
	admission.seedCommitted(job)
	now := admission.now.Add(2 * time.Second)
	pageIDs := []string{
		"33333333-3333-4333-8333-333333333333",
		"44444444-4444-4444-8444-444444444444",
		"55555555-5555-4555-8555-555555555555",
	}
	transaction := &fakeReceiptPurgeAttemptTransaction{
		fakeReceiptPurgeTransaction: admission.transaction,
		pageIDs:                     append([]string(nil), pageIDs...),
		queryReadTime:               admission.now.Add(time.Second),
		attemptReads:                make(map[string]receiptPurgeAttemptRead),
	}
	for index, documentID := range pageIDs {
		startedAt := admission.now.Add(-3 * time.Minute)
		attempt := validForwardReceiptPurgeAttempt(
			job.TenantID, job.ReceiptID, documentID, int64(index+1), startedAt,
		)
		if index == 1 {
			attempt = validCleanupReceiptPurgeAttempt(
				job.TenantID, job.ReceiptID, documentID, int64(index+1), startedAt,
			)
		}
		transaction.attemptReads[documentID] = receiptPurgeAttemptRead{
			DocumentID: documentID, Exists: true, ReadTime: admission.now.Add(time.Second),
			Attempt: attempt,
		}
	}
	store := admissionTestStore(now, admissionRunner(transaction))
	return &receiptPurgeAttemptAdapterFixture{
		admission: admission, transaction: transaction, store: store,
		job: job, pageIDs: pageIDs, now: now,
	}
}

func (fixture *receiptPurgeAttemptAdapterFixture) pageRequest(pageSize int) ingest.ReceiptPurgePageRequest {
	return ingest.ReceiptPurgePageRequest{
		PurgeKey: fixture.job.PurgeKey, TenantID: fixture.job.TenantID, ReceiptID: fixture.job.ReceiptID,
		Kind: ingest.ReceiptPurgePageAttempts, ExpectedJobStatus: ingest.ReceiptPurgeJobAttemptsPurging,
		ExpectedJobRevision: fixture.job.Revision, AfterDocumentID: fixture.job.AttemptCursor,
		PageSize: pageSize,
	}
}

func (fixture *receiptPurgeAttemptAdapterFixture) setJob(job ingest.ReceiptPurgeJob, readTime time.Time) {
	fixture.transaction.jobs[receiptPurgeJobDocumentPath(job.PurgeKey)] = receiptPurgeJobRead{
		Job: newFirestoreReceiptPurgeJob(job), ReadTime: readTime,
	}
}

type fakeReceiptPurgeAttemptTransaction struct {
	*fakeReceiptPurgeTransaction
	pageIDs       []string
	queryReadTime time.Time
	queryErr      error
	attemptReads  map[string]receiptPurgeAttemptRead
	deletes       []string
	operations    []string
}

func (transaction *fakeReceiptPurgeAttemptTransaction) ReadReceiptPurgeJob(
	ctx context.Context,
	path string,
) (receiptPurgeJobRead, bool, error) {
	transaction.operations = append(transaction.operations, "job-read")
	return transaction.fakeReceiptPurgeTransaction.ReadReceiptPurgeJob(ctx, path)
}

func (transaction *fakeReceiptPurgeAttemptTransaction) ReadReceipt(
	ctx context.Context,
	path string,
) (receiptRead, bool, error) {
	transaction.operations = append(transaction.operations, "receipt-read")
	return transaction.fakeReceiptPurgeTransaction.ReadReceipt(ctx, path)
}

func (transaction *fakeReceiptPurgeAttemptTransaction) QueryReceiptPurgeAttemptPage(
	_ context.Context,
	request ingest.ReceiptPurgePageRequest,
) ([]string, time.Time, error) {
	transaction.operations = append(transaction.operations, "query")
	if transaction.queryErr != nil {
		return nil, time.Time{}, transaction.queryErr
	}
	values := make([]string, 0, len(transaction.pageIDs))
	for _, documentID := range transaction.pageIDs {
		if documentID > request.AfterDocumentID {
			values = append(values, documentID)
		}
		if len(values) == request.PageSize+1 {
			break
		}
	}
	return values, transaction.queryReadTime, nil
}

func (transaction *fakeReceiptPurgeAttemptTransaction) ReadReceiptPurgeAttempts(
	_ context.Context,
	tenantID string,
	receiptID string,
	documentIDs []string,
) ([]receiptPurgeAttemptRead, error) {
	transaction.operations = append(transaction.operations, "attempt-read")
	reads := make([]receiptPurgeAttemptRead, len(documentIDs))
	for index, documentID := range documentIDs {
		read, exists := transaction.attemptReads[documentID]
		if !exists {
			read = receiptPurgeAttemptRead{
				DocumentID: documentID, ReadTime: transaction.queryReadTime,
			}
		} else if read.Exists && read.ErrorClass == "" {
			read.ErrorClass = classifyReceiptPurgeAttempt(
				documentID, tenantID, receiptID, read.Attempt,
			)
		}
		if read.Exists && read.ErrorClass == "" && read.DocumentDigest == "" {
			read.DocumentDigest = receiptPurgeAttemptDocumentDigest(read.Attempt)
		}
		reads[index] = read
	}
	return reads, nil
}

func validForwardReceiptPurgeAttempt(
	tenantID string,
	receiptID string,
	attemptID string,
	token int64,
	startedAt time.Time,
) firestoreRecoveryAttempt {
	return firestoreRecoveryAttempt{
		AttemptID: attemptID, TenantID: tenantID, ReceiptID: receiptID,
		OwnerKind: ingest.LeaseOwnerSweeper, FencingToken: token,
		WorkerVersion: ingest.RecoveryWorkerVersion, Status: ingest.RecoveryAttemptCompleted,
		DecisionDomain:           ingest.ForwardRecoveryDecisionCurrentAuthorization,
		AuthorizationDisposition: ingest.ForwardRecoveryAuthorizationUnavailable,
		Action:                   ingest.ForwardRecoveryActionReleaseLease,
		Outcome:                  ingest.RecoveryAttemptOutcomeLeaseReleased,
		ActionHash:               strings.Repeat("a", 64),
		ReleaseCode:              ingest.LeaseReleaseAuthorizationUnavailable,
		StartedAt:                startedAt.UTC(), CompletedAt: startedAt.Add(time.Minute).UTC(),
	}
}

func validCleanupReceiptPurgeAttempt(
	tenantID string,
	receiptID string,
	attemptID string,
	token int64,
	startedAt time.Time,
) firestoreRecoveryAttempt {
	rawTargeted := false
	manifestTargeted := false
	return firestoreRecoveryAttempt{
		AttemptID: attemptID, TenantID: tenantID, ReceiptID: receiptID,
		OwnerKind: ingest.LeaseOwnerCleanup, FencingToken: token,
		WorkerVersion: ingest.CleanupWorkerVersion, Status: ingest.RecoveryAttemptCompleted,
		DecisionDomain:         ingest.CleanupExecutionDecisionExpiry,
		Outcome:                ingest.RecoveryAttemptOutcomeExpired,
		CleanupSchemaVersion:   ingest.CleanupExecutionLedgerSchemaVersion,
		CleanupTargetHash:      strings.Repeat("b", 64),
		CleanupPlanHash:        strings.Repeat("c", 64),
		CleanupReceiptRevision: 1, CleanupExecutionRevision: 8,
		CleanupPhase:                     ingest.CleanupExecutionPhaseCompleted,
		CleanupRawTargeted:               &rawTargeted,
		CleanupRawDispatchAt:             startedAt.Add(10 * time.Second).UTC(),
		CleanupRawDeleteOutcome:          ingest.CleanupDeleteNotAttempted,
		CleanupRawOutcomeRecordedAt:      startedAt.Add(20 * time.Second).UTC(),
		CleanupRawAuditOutcome:           ingest.CleanupAuditConfirmedAbsent,
		CleanupRawAuditedAt:              startedAt.Add(30 * time.Second).UTC(),
		CleanupManifestTargeted:          &manifestTargeted,
		CleanupManifestDispatchAt:        startedAt.Add(40 * time.Second).UTC(),
		CleanupManifestDeleteOutcome:     ingest.CleanupDeleteNotAttempted,
		CleanupManifestOutcomeRecordedAt: startedAt.Add(50 * time.Second).UTC(),
		CleanupManifestAuditOutcome:      ingest.CleanupAuditConfirmedAbsent,
		CleanupManifestAuditedAt:         startedAt.Add(60 * time.Second).UTC(),
		CleanupDisposition:               ingest.CleanupExecutionDispositionComplete,
		CleanupEvidenceHash:              strings.Repeat("d", 64),
		StartedAt:                        startedAt.UTC(), CompletedAt: startedAt.Add(70 * time.Second).UTC(),
	}
}

func validFailedCleanupReceiptPurgeAttempt(
	tenantID string,
	receiptID string,
	attemptID string,
	token int64,
	startedAt time.Time,
) firestoreRecoveryAttempt {
	rawTargeted := false
	manifestTargeted := false
	return firestoreRecoveryAttempt{
		AttemptID: attemptID, TenantID: tenantID, ReceiptID: receiptID,
		OwnerKind: ingest.LeaseOwnerCleanup, FencingToken: token,
		WorkerVersion: ingest.CleanupWorkerVersion, Status: ingest.RecoveryAttemptFailed,
		DecisionDomain:         ingest.CleanupExecutionDecisionExpiry,
		CleanupSchemaVersion:   ingest.CleanupExecutionLedgerSchemaVersion,
		CleanupTargetHash:      strings.Repeat("b", 64),
		CleanupPlanHash:        strings.Repeat("c", 64),
		CleanupReceiptRevision: 1, CleanupExecutionRevision: 3,
		CleanupPhase:                ingest.CleanupExecutionPhaseRawOutcomeRecorded,
		CleanupRawTargeted:          &rawTargeted,
		CleanupRawDispatchAt:        startedAt.Add(10 * time.Second).UTC(),
		CleanupRawDeleteOutcome:     ingest.CleanupDeleteNotAttempted,
		CleanupRawOutcomeRecordedAt: startedAt.Add(20 * time.Second).UTC(),
		CleanupManifestTargeted:     &manifestTargeted,
		StartedAt:                   startedAt.UTC(),
		FailureCode:                 ingest.RecoveryAttemptFailureLeaseExpired,
		FailedAt:                    startedAt.Add(time.Minute).UTC(),
	}
}

func (transaction *fakeReceiptPurgeAttemptTransaction) Delete(_ context.Context, path string) error {
	transaction.operations = append(transaction.operations, "delete")
	transaction.deletes = append(transaction.deletes, path)
	return nil
}

func (transaction *fakeReceiptPurgeAttemptTransaction) Update(
	ctx context.Context,
	path string,
	updates []firestore.Update,
) error {
	transaction.operations = append(transaction.operations, "update")
	return transaction.fakeReceiptPurgeTransaction.Update(ctx, path, updates)
}

func operationIndex(operations []string, target string) int {
	for index, operation := range operations {
		if operation == target {
			return index
		}
	}
	return -1
}

func lastOperationIndex(operations []string, target string) int {
	for index := len(operations) - 1; index >= 0; index-- {
		if operations[index] == target {
			return index
		}
	}
	return -1
}
