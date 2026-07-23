package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreReceiptPurgeLinkedPageRereadsExactPairsBeforeAtomicProgress(t *testing.T) {
	fixture := newReceiptPurgeLinkedAdapterFixture(t)
	observation := fixture.listPage(t)
	fixture.transaction.operations = nil

	result, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), observation)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationProgressed ||
		result.Job.LinkCursor != fixture.states[1].LinkDocumentID ||
		result.Job.TargetDeletedCount != 2 || result.Job.Revision != fixture.job.Revision+1 ||
		ingest.ValidateReceiptPurgeLinkedMutationOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("CommitReceiptPurgeLinkedPage() = %#v, %v", result, err)
	}
	wantDeletes := []string{
		cleanupTargetDocumentPath(fixture.job.TenantID, fixture.states[0].Child.DocumentID),
		receiptPurgeLinkDocumentPath(
			fixture.job.TenantID,
			fixture.job.ReceiptID,
			fixture.states[0].LinkDocumentID,
		),
		cleanupTargetDocumentPath(fixture.job.TenantID, fixture.states[1].Child.DocumentID),
		receiptPurgeLinkDocumentPath(
			fixture.job.TenantID,
			fixture.job.ReceiptID,
			fixture.states[1].LinkDocumentID,
		),
	}
	if !reflect.DeepEqual(fixture.transaction.deletes, wantDeletes) {
		t.Fatalf("linked deletes = %#v, want %#v", fixture.transaction.deletes, wantDeletes)
	}
	lookahead := fixture.states[2]
	for _, deletedPath := range fixture.transaction.deletes {
		if deletedPath == cleanupTargetDocumentPath(fixture.job.TenantID, lookahead.Child.DocumentID) ||
			deletedPath == receiptPurgeLinkDocumentPath(
				fixture.job.TenantID,
				fixture.job.ReceiptID,
				lookahead.LinkDocumentID,
			) {
			t.Fatal("lookahead linked pair was deleted")
		}
	}
	if len(fixture.transaction.updates) != 1 ||
		fixture.transaction.updates[0].path != receiptPurgeJobDocumentPath(fixture.job.PurgeKey) {
		t.Fatalf("linked job updates = %#v", fixture.transaction.updates)
	}
	updates := firestoreUpdateMap(fixture.transaction.updates[0].updates)
	if updates["link_cursor"] != fixture.states[1].LinkDocumentID ||
		updates["target_deleted_count"] != int64(2) ||
		updates["revision"] != fixture.job.Revision+1 {
		t.Fatalf("linked progress updates = %#v", updates)
	}
	queryIndex := lastOperationIndex(fixture.transaction.operations, "link-query")
	pairReadIndex := operationIndex(fixture.transaction.operations, "linked-read")
	deleteIndex := operationIndex(fixture.transaction.operations, "delete")
	updateIndex := operationIndex(fixture.transaction.operations, "update")
	if queryIndex < 0 || pairReadIndex <= queryIndex || deleteIndex <= pairReadIndex ||
		updateIndex <= deleteIndex {
		t.Fatalf("linked operation order = %#v", fixture.transaction.operations)
	}
}

func TestFirestoreReceiptPurgeLinkedPageRejectsStaleCurrentPageWithoutWrites(t *testing.T) {
	fixture := newReceiptPurgeLinkedAdapterFixture(t)
	observation := fixture.listPage(t)
	fixture.transaction.linkPageIDs = append(
		[]string{"0"},
		fixture.transaction.linkPageIDs...,
	)
	fixture.transaction.deletes = nil
	fixture.transaction.updates = nil

	result, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), observation)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) ||
		!reflect.DeepEqual(result, ingest.ReceiptPurgeLinkedMutationResult{}) ||
		len(fixture.transaction.deletes) != 0 || len(fixture.transaction.updates) != 0 {
		t.Fatalf(
			"stale linked page = %#v deletes=%#v updates=%#v err=%v",
			result,
			fixture.transaction.deletes,
			fixture.transaction.updates,
			err,
		)
	}
}

func TestFirestoreReceiptPurgeLinkedPagePoisonHoldsWholePageWithoutDelete(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(*receiptPurgeLinkedRead)
		errorClass ingest.ReceiptPurgeErrorClass
	}{
		{
			name: "missing target",
			mutate: func(read *receiptPurgeLinkedRead) {
				read.ChildPresent = false
			},
			errorClass: ingest.ReceiptPurgeErrorLinkageDrift,
		},
		{
			name: "missing link",
			mutate: func(read *receiptPurgeLinkedRead) {
				read.LinkPresent = false
			},
			errorClass: ingest.ReceiptPurgeErrorLinkageDrift,
		},
		{
			name: "malformed child",
			mutate: func(read *receiptPurgeLinkedRead) {
				read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
			},
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
		},
		{
			name: "foreign child",
			mutate: func(read *receiptPurgeLinkedRead) {
				read.ErrorClass = ingest.ReceiptPurgeErrorChildForeign
			},
			errorClass: ingest.ReceiptPurgeErrorChildForeign,
		},
		{
			name: "pair drift",
			mutate: func(read *receiptPurgeLinkedRead) {
				read.ErrorClass = ingest.ReceiptPurgeErrorLinkageDrift
			},
			errorClass: ingest.ReceiptPurgeErrorLinkageDrift,
		},
		{
			name: "integrity finding unsupported",
			mutate: func(read *receiptPurgeLinkedRead) {
				read.ErrorClass = ingest.ReceiptPurgeErrorUnsupportedVersion
			},
			errorClass: ingest.ReceiptPurgeErrorUnsupportedVersion,
		},
		{
			name: "unknown kind",
			mutate: func(read *receiptPurgeLinkedRead) {
				read.ErrorClass = ingest.ReceiptPurgeErrorUnsupportedVersion
			},
			errorClass: ingest.ReceiptPurgeErrorUnsupportedVersion,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReceiptPurgeLinkedAdapterFixture(t)
			poisoned := fixture.transaction.linkedReads[fixture.states[1].LinkDocumentID]
			test.mutate(&poisoned)
			fixture.transaction.linkedReads[fixture.states[1].LinkDocumentID] = poisoned
			observation := fixture.listPage(t)

			result, err := fixture.store.CommitReceiptPurgeLinkedPage(
				context.Background(),
				observation,
			)
			if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
				result.Job.Status != ingest.ReceiptPurgeJobHold ||
				result.Job.HeldFromStatus != ingest.ReceiptPurgeJobLinkedDocumentsPurging ||
				result.Job.ErrorClass != test.errorClass || len(fixture.transaction.deletes) != 0 ||
				len(result.OutcomeQuery.Children) != 0 {
				t.Fatalf(
					"linked poison hold = %#v deletes=%#v err=%v",
					result,
					fixture.transaction.deletes,
					err,
				)
			}
			if len(fixture.transaction.updates) != 1 {
				t.Fatalf("linked hold updates = %#v", fixture.transaction.updates)
			}
			updates := firestoreUpdateMap(fixture.transaction.updates[0].updates)
			if updates["status"] != string(ingest.ReceiptPurgeJobHold) ||
				updates["error_class"] != string(test.errorClass) ||
				updates["held_from_status"] != string(ingest.ReceiptPurgeJobLinkedDocumentsPurging) {
				t.Fatalf("linked hold fields = %#v", updates)
			}
		})
	}
}

func TestFirestoreReceiptPurgeLinkedPagePreservesQueryOnResponseLossAndOutcomeIsReadOnly(t *testing.T) {
	fixture := newReceiptPurgeLinkedAdapterFixture(t)
	observation := fixture.listPage(t)
	baseRunner := fixture.store.runTransaction
	fixture.store.runTransaction = receiptPurgeLostResponseRunner(baseRunner)

	result, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), observation)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) || result.Status != "" ||
		ingest.ValidateReceiptPurgeLinkedMutationOutcomeQuery(result.OutcomeQuery) != nil ||
		len(result.OutcomeQuery.Children) != 2 || len(fixture.transaction.deletes) != 4 ||
		len(fixture.transaction.updates) != 1 {
		t.Fatalf(
			"linked response loss = %#v deletes=%#v updates=%#v err=%v",
			result,
			fixture.transaction.deletes,
			fixture.transaction.updates,
			err,
		)
	}

	fixture.store.runTransaction = baseRunner
	fixture.setJob(result.Job, fixture.now)
	fixture.transaction.deletes = nil
	fixture.transaction.updates = nil
	for _, child := range result.OutcomeQuery.Children {
		fixture.transaction.outcomeReads[child.LinkDocumentID] = receiptPurgeLinkedRead{
			LinkDocumentID: child.LinkDocumentID,
			ReadTime:       fixture.now,
		}
	}
	partial := result.OutcomeQuery.Children[0]
	fixture.transaction.outcomeReads[partial.LinkDocumentID] = receiptPurgeLinkedRead{
		LinkDocumentID: partial.LinkDocumentID,
		LinkPresent:    true,
		ChildPresent:   false,
		ReadTime:       fixture.now,
	}
	outcome, outcomeErr := fixture.store.GetReceiptPurgeLinkedMutationOutcome(
		context.Background(),
		result.OutcomeQuery,
	)
	if outcomeErr != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationUnverifiable ||
		len(fixture.transaction.deletes) != 0 || len(fixture.transaction.updates) != 0 {
		t.Fatalf(
			"partial linked outcome = %#v deletes=%#v updates=%#v err=%v",
			outcome,
			fixture.transaction.deletes,
			fixture.transaction.updates,
			outcomeErr,
		)
	}
}

func TestFirestoreReceiptPurgeLinkedPageResetsOuterResultAcrossTransactionRetry(t *testing.T) {
	first := newReceiptPurgeLinkedAdapterFixture(t)
	second := newReceiptPurgeLinkedAdapterFixture(t)
	poisoned := second.transaction.linkedReads[second.states[0].LinkDocumentID]
	poisoned.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
	second.transaction.linkedReads[second.states[0].LinkDocumentID] = poisoned
	first.transaction.deleteErr = errors.New("synthetic transaction retry")
	observation := first.listPage(t)
	calls := 0
	store := admissionTestStore(first.now, func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		calls++
		if firstErr := operation(ctx, first.transaction); firstErr == nil {
			return errors.New("first callback unexpectedly succeeded")
		}
		calls++
		return operation(ctx, second.transaction)
	})

	result, err := store.CommitReceiptPurgeLinkedPage(context.Background(), observation)
	if err != nil || calls != 2 || result.Status != ingest.ReceiptPurgeMutationHeld ||
		result.Job.ErrorClass != ingest.ReceiptPurgeErrorChildMalformed ||
		result.OutcomeQuery.Kind != ingest.ReceiptPurgeLinkedMutationHold ||
		len(result.OutcomeQuery.Children) != 0 || len(second.transaction.deletes) != 0 ||
		len(second.transaction.updates) != 1 {
		t.Fatalf(
			"linked transaction retry = %#v calls=%d firstDeletes=%#v secondDeletes=%#v err=%v",
			result,
			calls,
			first.transaction.deletes,
			second.transaction.deletes,
			err,
		)
	}
}

type receiptPurgeLinkedAdapterFixture struct {
	base        *receiptPurgeAttemptAdapterFixture
	transaction *fakeReceiptPurgeLinkedTransaction
	store       *FirestoreAdmissionStore
	job         ingest.ReceiptPurgeJob
	states      []ingest.ReceiptPurgeLinkedChildState
	now         time.Time
}

func newReceiptPurgeLinkedAdapterFixture(t *testing.T) *receiptPurgeLinkedAdapterFixture {
	t.Helper()
	base := newReceiptPurgeAttemptAdapterFixture(t)
	job := base.job
	job.Status = ingest.ReceiptPurgeJobLinkedDocumentsPurging
	job.Revision = 3
	job.UpdatedAt = base.admission.now.Add(time.Second)
	if err := ingest.ValidateReceiptPurgeJob(job); err != nil {
		t.Fatalf("linked job fixture = %v", err)
	}
	base.job = job
	base.setJob(job, job.UpdatedAt)
	states := []ingest.ReceiptPurgeLinkedChildState{
		firestoreReceiptPurgeLinkedStateFixture(
			t,
			job,
			"33333333-3333-4333-8333-333333333333",
			job.CreatedAt.Add(-3*time.Minute),
			"a",
			"b",
		),
		firestoreReceiptPurgeLinkedStateFixture(
			t,
			job,
			"44444444-4444-4444-8444-444444444444",
			job.CreatedAt.Add(-2*time.Minute),
			"c",
			"d",
		),
		firestoreReceiptPurgeLinkedStateFixture(
			t,
			job,
			"55555555-5555-4555-8555-555555555555",
			job.CreatedAt.Add(-time.Minute),
			"e",
			"f",
		),
	}
	sort.Slice(states, func(left, right int) bool {
		return states[left].LinkDocumentID < states[right].LinkDocumentID
	})
	readTime := base.now.Add(-time.Second)
	linkedReads := make(map[string]receiptPurgeLinkedRead, len(states))
	for _, state := range states {
		linkedReads[state.LinkDocumentID] = receiptPurgeLinkedRead{
			LinkDocumentID: state.LinkDocumentID,
			LinkPresent:    true,
			ChildPresent:   true,
			State:          state,
			ReadTime:       readTime,
		}
	}
	transaction := &fakeReceiptPurgeLinkedTransaction{
		fakeReceiptPurgeAttemptTransaction: base.transaction,
		linkPageIDs: append([]string(nil),
			states[0].LinkDocumentID,
			states[1].LinkDocumentID,
			states[2].LinkDocumentID,
		),
		linkQueryReadTime: readTime,
		linkedReads:       linkedReads,
		outcomeReads:      make(map[string]receiptPurgeLinkedRead),
	}
	store := admissionTestStore(base.now, admissionRunner(transaction))
	return &receiptPurgeLinkedAdapterFixture{
		base: base, transaction: transaction, store: store, job: job,
		states: states, now: base.now,
	}
}

func (fixture *receiptPurgeLinkedAdapterFixture) request() ingest.ReceiptPurgePageRequest {
	return ingest.ReceiptPurgePageRequest{
		PurgeKey: fixture.job.PurgeKey, TenantID: fixture.job.TenantID,
		ReceiptID: fixture.job.ReceiptID, Kind: ingest.ReceiptPurgePageLinks,
		ExpectedJobStatus: fixture.job.Status, ExpectedJobRevision: fixture.job.Revision,
		AfterDocumentID: fixture.job.LinkCursor, PageSize: 2,
	}
}

func (fixture *receiptPurgeLinkedAdapterFixture) listPage(
	t *testing.T,
) ingest.ReceiptPurgePageObservation {
	t.Helper()
	observation, err := fixture.store.ListReceiptPurgeLinkedPage(
		context.Background(),
		fixture.request(),
	)
	if err != nil {
		t.Fatalf("ListReceiptPurgeLinkedPage() = %v", err)
	}
	if len(observation.DeleteDocumentIDs) != 2 ||
		observation.LookaheadDocumentID != fixture.states[2].LinkDocumentID {
		t.Fatalf("linked page observation = %#v", observation)
	}
	return observation
}

func (fixture *receiptPurgeLinkedAdapterFixture) setJob(
	job ingest.ReceiptPurgeJob,
	readTime time.Time,
) {
	fixture.transaction.jobs[receiptPurgeJobDocumentPath(job.PurgeKey)] = receiptPurgeJobRead{
		Job: newFirestoreReceiptPurgeJob(job), ReadTime: readTime,
	}
}

func firestoreReceiptPurgeLinkedStateFixture(
	t *testing.T,
	job ingest.ReceiptPurgeJob,
	documentID string,
	createdAt time.Time,
	linkDigestCharacter string,
	childDigestCharacter string,
) ingest.ReceiptPurgeLinkedChildState {
	t.Helper()
	child := ingest.ReceiptPurgeLinkedChildIdentity{
		TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		Kind: ingest.ReceiptPurgeLinkCleanupTarget, DocumentID: documentID,
		CreatedAt: createdAt,
	}
	link, err := ingest.BuildReceiptPurgeInverseLink(child)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	state := ingest.ReceiptPurgeLinkedChildState{
		LinkDocumentID:      link.LinkID,
		LinkDocumentDigest:  strings.Repeat(linkDigestCharacter, 64),
		Link:                link,
		ChildDocumentDigest: strings.Repeat(childDigestCharacter, 64),
		Child:               child,
	}
	if err := ingest.ValidateReceiptPurgeLinkedChildState(state); err != nil {
		t.Fatalf("linked state fixture = %v", err)
	}
	return state
}

type fakeReceiptPurgeLinkedTransaction struct {
	*fakeReceiptPurgeAttemptTransaction
	linkPageIDs       []string
	linkQueryReadTime time.Time
	linkedReads       map[string]receiptPurgeLinkedRead
	outcomeReads      map[string]receiptPurgeLinkedRead
	deleteErr         error
}

func (transaction *fakeReceiptPurgeLinkedTransaction) QueryReceiptPurgeLinkPage(
	_ context.Context,
	request ingest.ReceiptPurgePageRequest,
) ([]string, time.Time, error) {
	transaction.operations = append(transaction.operations, "link-query")
	values := make([]string, 0, request.PageSize+1)
	for _, documentID := range transaction.linkPageIDs {
		if documentID <= request.AfterDocumentID {
			continue
		}
		values = append(values, documentID)
		if len(values) == request.PageSize+1 {
			break
		}
	}
	return values, transaction.linkQueryReadTime, nil
}

func (transaction *fakeReceiptPurgeLinkedTransaction) ReadReceiptPurgeLinkedPage(
	_ context.Context,
	_ ingest.ReceiptPurgeJob,
	_ firestoreIngestReceipt,
	documentIDs []string,
) ([]receiptPurgeLinkedRead, error) {
	transaction.operations = append(transaction.operations, "linked-read")
	reads := make([]receiptPurgeLinkedRead, len(documentIDs))
	for index, documentID := range documentIDs {
		read, exists := transaction.linkedReads[documentID]
		if !exists {
			read = receiptPurgeLinkedRead{
				LinkDocumentID: documentID,
				ReadTime:       transaction.linkQueryReadTime,
			}
		}
		reads[index] = read
	}
	return reads, nil
}

func (transaction *fakeReceiptPurgeLinkedTransaction) ReadReceiptPurgeLinkedOutcome(
	_ context.Context,
	_ ingest.ReceiptPurgeJob,
	_ firestoreIngestReceipt,
	expected []ingest.ReceiptPurgeLinkedChildState,
) ([]receiptPurgeLinkedRead, error) {
	transaction.operations = append(transaction.operations, "linked-outcome-read")
	reads := make([]receiptPurgeLinkedRead, len(expected))
	for index, child := range expected {
		read, exists := transaction.outcomeReads[child.LinkDocumentID]
		if !exists {
			read = transaction.linkedReads[child.LinkDocumentID]
		}
		reads[index] = read
	}
	return reads, nil
}

func (transaction *fakeReceiptPurgeLinkedTransaction) Delete(
	_ context.Context,
	path string,
) error {
	transaction.operations = append(transaction.operations, "delete")
	transaction.deletes = append(transaction.deletes, path)
	if transaction.deleteErr != nil {
		return transaction.deleteErr
	}
	return nil
}
