package firebaseadapter

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreEmulatorReceiptPurgeLinkedPagination(t *testing.T) {
	fixture := prepareEmulatorReceiptPurgeLinked(t, 3)
	first := listEmulatorReceiptPurgeLinkedPage(t, fixture, fixture.job, 2)
	if len(first.DeleteDocumentIDs) != 2 ||
		first.DeleteDocumentIDs[0] != fixture.pairs[0].linkID ||
		first.DeleteDocumentIDs[1] != fixture.pairs[1].linkID ||
		first.LookaheadDocumentID != fixture.pairs[2].linkID {
		t.Fatalf("first linked page = %#v", first)
	}
	firstResult, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), first)
	if err != nil || firstResult.Status != ingest.ReceiptPurgeMutationProgressed ||
		firstResult.Job.LinkCursor != fixture.pairs[1].linkID ||
		firstResult.Job.TargetDeletedCount != 2 || firstResult.Job.FindingDeletedCount != 0 {
		t.Fatalf("first linked result = %#v, %v", firstResult, err)
	}
	for _, pair := range fixture.pairs[:2] {
		assertEmulatorReceiptPurgeLinkedPairMissing(t, fixture, pair)
	}
	assertEmulatorReceiptPurgeLinkedPairPresent(t, fixture, fixture.pairs[2])
	replayed, replayErr := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), first)
	if !errors.Is(replayErr, ingest.ErrReceiptPurgeMutationConflict) || replayed.Status != "" {
		t.Fatalf("replayed old linked page = %#v, %v", replayed, replayErr)
	}
	replayedJob := readEmulatorReceiptPurgeJob(
		t,
		fixture.expiredReceiptPurgeEmulatorFixture,
		fixture.job.PurgeKey,
	)
	if replayedJob.TargetDeletedCount != 2 || replayedJob.Revision != fixture.job.Revision+1 {
		t.Fatalf("old linked page replay changed progress = %#v", replayedJob)
	}

	second := listEmulatorReceiptPurgeLinkedPage(t, fixture, firstResult.Job, 2)
	if len(second.DeleteDocumentIDs) != 1 ||
		second.DeleteDocumentIDs[0] != fixture.pairs[2].linkID ||
		second.LookaheadDocumentID != "" {
		t.Fatalf("second linked page = %#v", second)
	}
	secondResult, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), second)
	if err != nil || secondResult.Status != ingest.ReceiptPurgeMutationProgressed ||
		secondResult.Job.LinkCursor != fixture.pairs[2].linkID ||
		secondResult.Job.TargetDeletedCount != 3 || secondResult.Job.FindingDeletedCount != 0 {
		t.Fatalf("second linked result = %#v, %v", secondResult, err)
	}
	assertEmulatorReceiptPurgeLinkedPairMissing(t, fixture, fixture.pairs[2])
	empty := listEmulatorReceiptPurgeLinkedPage(t, fixture, secondResult.Job, 2)
	if len(empty.DeleteDocumentIDs) != 0 || empty.LookaheadDocumentID != "" {
		t.Fatalf("empty linked page = %#v", empty)
	}
}

func TestFirestoreEmulatorReceiptPurgeLinkedConcurrentSingleWinner(t *testing.T) {
	fixture := prepareEmulatorReceiptPurgeLinked(t, 2)
	page := listEmulatorReceiptPurgeLinkedPage(t, fixture, fixture.job, 2)
	type call struct {
		result ingest.ReceiptPurgeLinkedMutationResult
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
			result, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), page)
			results <- call{result: result, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	conflicts := 0
	for call := range results {
		if call.err == nil {
			winners++
			if call.result.Status != ingest.ReceiptPurgeMutationProgressed {
				t.Fatalf("linked winner = %#v", call.result)
			}
			continue
		}
		if errors.Is(call.err, ingest.ErrReceiptPurgeMutationConflict) {
			conflicts++
			continue
		}
		t.Fatalf("unexpected linked concurrent result = %#v, %v", call.result, call.err)
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("linked winners/conflicts = %d/%d", winners, conflicts)
	}
	stored := readEmulatorReceiptPurgeJob(t, fixture.expiredReceiptPurgeEmulatorFixture, fixture.job.PurgeKey)
	if stored.TargetDeletedCount != 2 || stored.Revision != fixture.job.Revision+1 {
		t.Fatalf("linked concurrent job = %#v", stored)
	}
	for _, pair := range fixture.pairs {
		assertEmulatorReceiptPurgeLinkedPairMissing(t, fixture, pair)
	}
}

func TestFirestoreEmulatorReceiptPurgeLinkedFindingHoldsWholePage(t *testing.T) {
	fixture := prepareEmulatorReceiptPurgeLinked(t, 1)
	findingChild := ingest.ReceiptPurgeLinkedChildIdentity{
		TenantID:   fixture.job.TenantID,
		ReceiptID:  fixture.job.ReceiptID,
		Kind:       ingest.ReceiptPurgeLinkIntegrityFinding,
		DocumentID: "legacy-finding-001",
		CreatedAt:  fixture.job.CreatedAt.Add(-time.Second),
	}
	findingLink, err := ingest.BuildReceiptPurgeInverseLink(findingChild)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink(finding) = %v", err)
	}
	findingPath := receiptPurgeLinkDocumentPath(
		findingLink.TenantID,
		findingLink.ReceiptID,
		findingLink.LinkID,
	)
	if _, err := fixture.client.Doc(findingPath).Set(
		context.Background(),
		newFirestoreReceiptPurgeLink(findingLink),
	); err != nil {
		t.Fatalf("seed unsupported finding link: %v", err)
	}
	page := listEmulatorReceiptPurgeLinkedPage(t, fixture, fixture.job, 100)
	result, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), page)
	if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
		result.Job.Status != ingest.ReceiptPurgeJobHold ||
		result.Job.HeldFromStatus != ingest.ReceiptPurgeJobLinkedDocumentsPurging ||
		result.Job.ErrorClass != ingest.ReceiptPurgeErrorUnsupportedVersion ||
		result.Job.LinkCursor != "" || result.Job.TargetDeletedCount != 0 ||
		result.Job.FindingDeletedCount != 0 {
		t.Fatalf("finding linked hold = %#v, %v", result, err)
	}
	assertEmulatorReceiptPurgeLinkedPairPresent(t, fixture, fixture.pairs[0])
	if _, err := fixture.client.Doc(findingPath).Get(context.Background()); err != nil {
		t.Fatalf("finding link was deleted: %v", err)
	}
}

func TestFirestoreEmulatorReceiptPurgeLinkedChangedLookaheadCannotCommit(t *testing.T) {
	fixture := prepareEmulatorReceiptPurgeLinked(t, 3)
	page := listEmulatorReceiptPurgeLinkedPage(t, fixture, fixture.job, 2)
	if _, err := fixture.client.Doc(fixture.pairs[2].linkPath).Delete(context.Background()); err != nil {
		t.Fatalf("delete linked lookahead after discovery: %v", err)
	}
	result, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) || result.Status != "" {
		t.Fatalf("changed linked lookahead result = %#v, %v", result, err)
	}
	stored := readEmulatorReceiptPurgeJob(t, fixture.expiredReceiptPurgeEmulatorFixture, fixture.job.PurgeKey)
	if stored.Revision != fixture.job.Revision || stored.LinkCursor != "" ||
		stored.TargetDeletedCount != 0 || stored.FindingDeletedCount != 0 {
		t.Fatalf("changed lookahead advanced job = %#v", stored)
	}
	for _, pair := range fixture.pairs[:2] {
		assertEmulatorReceiptPurgeLinkedPairPresent(t, fixture, pair)
	}
}

func TestFirestoreEmulatorReceiptPurgeLinkedResponseLossCorrelation(t *testing.T) {
	fixture := prepareEmulatorReceiptPurgeLinked(t, 1)
	page := listEmulatorReceiptPurgeLinkedPage(t, fixture, fixture.job, 1)
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
	result, err := lossy.CommitReceiptPurgeLinkedPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) || result.Status != "" ||
		ingest.ValidateReceiptPurgeLinkedMutationOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("lossy linked commit = %#v, %v", result, err)
	}
	assertEmulatorReceiptPurgeLinkedPairMissing(t, fixture, fixture.pairs[0])
	outcome, err := fixture.store.GetReceiptPurgeLinkedMutationOutcome(
		context.Background(),
		result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationCommitted ||
		outcome.JobRevision != fixture.job.Revision+1 || outcome.TargetDeletedCount != 1 ||
		outcome.FindingDeletedCount != 0 {
		t.Fatalf("linked response-loss outcome = %#v, %v", outcome, err)
	}
}

func TestFirestoreEmulatorReceiptPurgeLinkedPoisonHoldsWholePage(t *testing.T) {
	for _, test := range []struct {
		name       string
		errorClass ingest.ReceiptPurgeErrorClass
		mutate     func(*testing.T, *receiptPurgeLinkedEmulatorFixture, receiptPurgeLinkedEmulatorPair)
	}{
		{
			name:       "malformed target",
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
			mutate: func(t *testing.T, fixture *receiptPurgeLinkedEmulatorFixture, pair receiptPurgeLinkedEmulatorPair) {
				if _, err := fixture.client.Doc(pair.targetPath).Set(
					context.Background(),
					map[string]any{"schema_version": ingest.CleanupTargetSchemaVersion},
				); err != nil {
					t.Fatalf("replace target with malformed body: %v", err)
				}
			},
		},
		{
			name:       "malformed link",
			errorClass: ingest.ReceiptPurgeErrorChildMalformed,
			mutate: func(t *testing.T, fixture *receiptPurgeLinkedEmulatorFixture, pair receiptPurgeLinkedEmulatorPair) {
				if _, err := fixture.client.Doc(pair.linkPath).Set(
					context.Background(),
					map[string]any{"schema_version": ingest.ReceiptPurgeLinkSchemaVersion},
				); err != nil {
					t.Fatalf("replace link with malformed body: %v", err)
				}
			},
		},
		{
			name:       "foreign link",
			errorClass: ingest.ReceiptPurgeErrorChildForeign,
			mutate: func(t *testing.T, fixture *receiptPurgeLinkedEmulatorFixture, pair receiptPurgeLinkedEmulatorPair) {
				if _, err := fixture.client.Doc(pair.linkPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "tenant_id", Value: "99999999-9999-4999-8999-999999999999"}},
				); err != nil {
					t.Fatalf("make link foreign: %v", err)
				}
			},
		},
		{
			name:       "foreign target",
			errorClass: ingest.ReceiptPurgeErrorChildForeign,
			mutate: func(t *testing.T, fixture *receiptPurgeLinkedEmulatorFixture, pair receiptPurgeLinkedEmulatorPair) {
				if _, err := fixture.client.Doc(pair.targetPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "tenant_id", Value: "99999999-9999-4999-8999-999999999999"}},
				); err != nil {
					t.Fatalf("make target foreign: %v", err)
				}
			},
		},
		{
			name:       "pair created-at drift",
			errorClass: ingest.ReceiptPurgeErrorLinkageDrift,
			mutate: func(t *testing.T, fixture *receiptPurgeLinkedEmulatorFixture, pair receiptPurgeLinkedEmulatorPair) {
				if _, err := fixture.client.Doc(pair.linkPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "created_at", Value: fixture.job.CreatedAt.Add(-time.Hour)}},
				); err != nil {
					t.Fatalf("drift link created_at: %v", err)
				}
			},
		},
		{
			name:       "missing target",
			errorClass: ingest.ReceiptPurgeErrorLinkageDrift,
			mutate: func(t *testing.T, fixture *receiptPurgeLinkedEmulatorFixture, pair receiptPurgeLinkedEmulatorPair) {
				if _, err := fixture.client.Doc(pair.targetPath).Delete(context.Background()); err != nil {
					t.Fatalf("delete linked target: %v", err)
				}
			},
		},
		{
			name:       "unknown kind",
			errorClass: ingest.ReceiptPurgeErrorUnsupportedVersion,
			mutate: func(t *testing.T, fixture *receiptPurgeLinkedEmulatorFixture, pair receiptPurgeLinkedEmulatorPair) {
				if _, err := fixture.client.Doc(pair.linkPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "kind", Value: "future_kind"}},
				); err != nil {
					t.Fatalf("set unknown link kind: %v", err)
				}
			},
		},
		{
			name:       "unknown link schema",
			errorClass: ingest.ReceiptPurgeErrorUnsupportedVersion,
			mutate: func(t *testing.T, fixture *receiptPurgeLinkedEmulatorFixture, pair receiptPurgeLinkedEmulatorPair) {
				if _, err := fixture.client.Doc(pair.linkPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "schema_version", Value: "ingest-purge-link.v2"}},
				); err != nil {
					t.Fatalf("set unknown link schema: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareEmulatorReceiptPurgeLinked(t, 2)
			page := listEmulatorReceiptPurgeLinkedPage(t, fixture, fixture.job, 100)
			poison := fixture.pairs[1]
			test.mutate(t, fixture, poison)
			result, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), page)
			if err != nil || result.Status != ingest.ReceiptPurgeMutationHeld ||
				result.Job.ErrorClass != test.errorClass || result.Job.LinkCursor != "" ||
				result.Job.TargetDeletedCount != 0 || result.Job.FindingDeletedCount != 0 {
				t.Fatalf("linked poison result = %#v, %v", result, err)
			}
			assertEmulatorReceiptPurgeLinkedPairPresent(t, fixture, fixture.pairs[0])
			if _, err := fixture.client.Doc(poison.linkPath).Get(context.Background()); err != nil {
				t.Fatalf("poison link was deleted: %v", err)
			}
			stored := readEmulatorReceiptPurgeJob(
				t,
				fixture.expiredReceiptPurgeEmulatorFixture,
				fixture.job.PurgeKey,
			)
			if stored.Status != ingest.ReceiptPurgeJobHold || stored.ErrorClass != test.errorClass ||
				stored.TargetDeletedCount != 0 || stored.LinkCursor != "" {
				t.Fatalf("linked poison stored job = %#v", stored)
			}
		})
	}
}

func TestFirestoreEmulatorReceiptPurgeLinkedMissingCurrentLinkCannotReplay(t *testing.T) {
	fixture := prepareEmulatorReceiptPurgeLinked(t, 1)
	page := listEmulatorReceiptPurgeLinkedPage(t, fixture, fixture.job, 1)
	if _, err := fixture.client.Doc(fixture.pairs[0].linkPath).Delete(context.Background()); err != nil {
		t.Fatalf("delete current linked page member: %v", err)
	}
	result, err := fixture.store.CommitReceiptPurgeLinkedPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) || result.Status != "" {
		t.Fatalf("missing current link result = %#v, %v", result, err)
	}
	if _, err := fixture.client.Doc(fixture.pairs[0].targetPath).Get(context.Background()); err != nil {
		t.Fatalf("missing current link deleted target: %v", err)
	}
	stored := readEmulatorReceiptPurgeJob(
		t,
		fixture.expiredReceiptPurgeEmulatorFixture,
		fixture.job.PurgeKey,
	)
	if stored.Revision != fixture.job.Revision || stored.TargetDeletedCount != 0 || stored.LinkCursor != "" {
		t.Fatalf("missing current link advanced job = %#v", stored)
	}
}

func TestFirestoreEmulatorReceiptPurgeLinkedNotCommittedAndPartialOutcome(t *testing.T) {
	fixture := prepareEmulatorReceiptPurgeLinked(t, 1)
	page := listEmulatorReceiptPurgeLinkedPage(t, fixture, fixture.job, 1)
	baseRunner := fixture.store.runTransaction
	aborted := *fixture.store
	aborted.runTransaction = func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		return baseRunner(ctx, func(runContext context.Context, transaction admissionTransaction) error {
			if err := operation(runContext, transaction); err != nil {
				return err
			}
			return errors.New("abort before commit")
		})
	}
	result, err := aborted.CommitReceiptPurgeLinkedPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) || result.Status != "" ||
		ingest.ValidateReceiptPurgeLinkedMutationOutcomeQuery(result.OutcomeQuery) != nil {
		t.Fatalf("aborted linked commit = %#v, %v", result, err)
	}
	assertEmulatorReceiptPurgeLinkedPairPresent(t, fixture, fixture.pairs[0])
	outcome, err := fixture.store.GetReceiptPurgeLinkedMutationOutcome(
		context.Background(),
		result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeMutationNotCommitted {
		t.Fatalf("not-committed linked outcome = %#v, %v", outcome, err)
	}
	if _, err := fixture.client.Doc(fixture.pairs[0].targetPath).Delete(context.Background()); err != nil {
		t.Fatalf("create partial linked outcome: %v", err)
	}
	partial, err := fixture.store.GetReceiptPurgeLinkedMutationOutcome(
		context.Background(),
		result.OutcomeQuery,
	)
	if err != nil || partial.CommitStatus != ingest.ReceiptPurgeMutationUnverifiable {
		t.Fatalf("partial linked outcome = %#v, %v", partial, err)
	}
}

type receiptPurgeLinkedEmulatorPair struct {
	targetID   string
	linkID     string
	targetPath string
	linkPath   string
}

type receiptPurgeLinkedEmulatorFixture struct {
	*expiredReceiptPurgeEmulatorFixture
	job   ingest.ReceiptPurgeJob
	pairs []receiptPurgeLinkedEmulatorPair
}

func prepareEmulatorReceiptPurgeLinked(
	t *testing.T,
	count int,
) *receiptPurgeLinkedEmulatorFixture {
	t.Helper()
	if count < 1 || count > 3 {
		t.Fatalf("invalid linked pair count %d", count)
	}
	base := seedExpiredReceiptPurgeEmulatorFixture(t)
	receiptSnapshot, err := base.client.Doc(base.receiptPath).Get(context.Background())
	if err != nil {
		t.Fatalf("read linked receipt: %v", err)
	}
	var receipt firestoreIngestReceipt
	if receiptSnapshot.DataTo(&receipt) != nil || validateReceiptState(receipt) != nil {
		t.Fatal("decode linked receipt")
	}
	historical := receipt
	historical.LeaseOwnerID = "33333333-3333-4333-8333-333333333333"
	historical.LeaseOwnerKind = ingest.LeaseOwnerCleanup
	historical.LeaseAcquiredAt = receipt.CleanupQuiescenceUntil.Add(time.Minute)
	historical.LeaseHeartbeatAt = historical.LeaseAcquiredAt
	historical.LeaseExpiresAt = historical.LeaseAcquiredAt.Add(5 * time.Minute)
	baseCommand := cleanupTargetCommandFixture(
		t,
		historical,
		ingest.ArtifactClassificationValidComplete,
	)
	commands := []ingest.CleanupTargetCommand{baseCommand}
	extraIDs := []string{
		"44444444-4444-4444-8444-444444444444",
		"55555555-5555-4555-8555-555555555555",
	}
	for index := 1; index < count; index++ {
		command := baseCommand
		command.CleanupID = extraIDs[index-1]
		command.AttemptID = extraIDs[index-1]
		if ingest.ValidateCleanupTargetCommand(command) != nil {
			t.Fatalf("extra linked target command %d invalid", index)
		}
		commands = append(commands, command)
	}
	pairs := make([]receiptPurgeLinkedEmulatorPair, 0, len(commands))
	batch := base.client.Batch()
	for index, command := range commands {
		child := cleanupTargetPurgeChildIdentity(command)
		link, linkErr := ingest.BuildReceiptPurgeInverseLink(child)
		if linkErr != nil {
			t.Fatalf("BuildReceiptPurgeInverseLink(%d) = %v", index, linkErr)
		}
		targetPath := cleanupTargetDocumentPath(command.TenantID, command.CleanupID)
		linkPath := receiptPurgeLinkDocumentPath(command.TenantID, command.ReceiptID, link.LinkID)
		targetHash, hashErr := ingest.CleanupTargetHash(command)
		if hashErr != nil {
			t.Fatalf("CleanupTargetHash(%d) = %v", index, hashErr)
		}
		batch.Set(base.client.Doc(targetPath), newFirestoreCleanupTarget(command, targetHash))
		batch.Set(base.client.Doc(linkPath), newFirestoreReceiptPurgeLink(link))
		pairs = append(pairs, receiptPurgeLinkedEmulatorPair{
			targetID:   command.CleanupID,
			linkID:     link.LinkID,
			targetPath: targetPath,
			linkPath:   linkPath,
		})
	}
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed linked pairs: %v", err)
	}
	base.store.now = time.Now
	admission, err := base.store.AdmitReceiptPurge(context.Background(), base.command)
	if err != nil {
		t.Fatalf("AdmitReceiptPurge() = %v", err)
	}
	begin, err := base.store.BeginReceiptPurgeAttempts(
		context.Background(),
		ingest.ReceiptPurgeAttemptPhaseCommand{
			Action:              ingest.ReceiptPurgeAttemptPhaseBegin,
			PurgeKey:            admission.Job.PurgeKey,
			TenantID:            admission.Job.TenantID,
			ReceiptID:           admission.Job.ReceiptID,
			ExpectedJobRevision: admission.Job.Revision,
		},
	)
	if err != nil {
		t.Fatalf("BeginReceiptPurgeAttempts() = %v", err)
	}
	attemptPage := listReceiptPurgeAttemptPageForLinkedFixture(t, base.store, begin.Job, 100)
	currentJob := begin.Job
	if len(attemptPage.DeleteDocumentIDs) != 0 {
		attemptResult, commitErr := base.store.CommitReceiptPurgeAttemptPage(
			context.Background(),
			attemptPage,
		)
		if commitErr != nil {
			t.Fatalf("CommitReceiptPurgeAttemptPage() = %v", commitErr)
		}
		currentJob = attemptResult.Job
	}
	empty := listReceiptPurgeAttemptPageForLinkedFixture(t, base.store, currentJob, 100)
	complete, err := base.store.CompleteReceiptPurgeAttempts(
		context.Background(),
		ingest.ReceiptPurgeAttemptPhaseCommand{
			Action:              ingest.ReceiptPurgeAttemptPhaseComplete,
			PurgeKey:            currentJob.PurgeKey,
			TenantID:            currentJob.TenantID,
			ReceiptID:           currentJob.ReceiptID,
			ExpectedJobRevision: currentJob.Revision,
			EmptyObservation:    empty,
		},
	)
	if err != nil || complete.Job.Status != ingest.ReceiptPurgeJobLinkedDocumentsPurging {
		t.Fatalf("CompleteReceiptPurgeAttempts() = %#v, %v", complete, err)
	}
	sort.Slice(pairs, func(left, right int) bool { return pairs[left].linkID < pairs[right].linkID })
	return &receiptPurgeLinkedEmulatorFixture{
		expiredReceiptPurgeEmulatorFixture: base,
		job:                                complete.Job,
		pairs:                              pairs,
	}
}

func listReceiptPurgeAttemptPageForLinkedFixture(
	t *testing.T,
	store *FirestoreAdmissionStore,
	job ingest.ReceiptPurgeJob,
	pageSize int,
) ingest.ReceiptPurgePageObservation {
	t.Helper()
	observation, err := store.ListReceiptPurgeAttemptPage(
		context.Background(),
		ingest.ReceiptPurgePageRequest{
			PurgeKey:            job.PurgeKey,
			TenantID:            job.TenantID,
			ReceiptID:           job.ReceiptID,
			Kind:                ingest.ReceiptPurgePageAttempts,
			ExpectedJobStatus:   job.Status,
			ExpectedJobRevision: job.Revision,
			AfterDocumentID:     job.AttemptCursor,
			PageSize:            pageSize,
		},
	)
	if err != nil {
		t.Fatalf("ListReceiptPurgeAttemptPage() = %v", err)
	}
	return observation
}

func listEmulatorReceiptPurgeLinkedPage(
	t *testing.T,
	fixture *receiptPurgeLinkedEmulatorFixture,
	job ingest.ReceiptPurgeJob,
	pageSize int,
) ingest.ReceiptPurgePageObservation {
	t.Helper()
	observation, err := fixture.store.ListReceiptPurgeLinkedPage(
		context.Background(),
		ingest.ReceiptPurgePageRequest{
			PurgeKey:            job.PurgeKey,
			TenantID:            job.TenantID,
			ReceiptID:           job.ReceiptID,
			Kind:                ingest.ReceiptPurgePageLinks,
			ExpectedJobStatus:   job.Status,
			ExpectedJobRevision: job.Revision,
			AfterDocumentID:     job.LinkCursor,
			PageSize:            pageSize,
		},
	)
	if err != nil {
		t.Fatalf("ListReceiptPurgeLinkedPage() = %v", err)
	}
	return observation
}

func assertEmulatorReceiptPurgeLinkedPairMissing(
	t *testing.T,
	fixture *receiptPurgeLinkedEmulatorFixture,
	pair receiptPurgeLinkedEmulatorPair,
) {
	t.Helper()
	for _, path := range []string{pair.targetPath, pair.linkPath} {
		_, err := fixture.client.Doc(path).Get(context.Background())
		if status.Code(err) != codes.NotFound {
			t.Fatalf("linked document %q still present: %v", path, err)
		}
	}
}

func assertEmulatorReceiptPurgeLinkedPairPresent(
	t *testing.T,
	fixture *receiptPurgeLinkedEmulatorFixture,
	pair receiptPurgeLinkedEmulatorPair,
) {
	t.Helper()
	for _, path := range []string{pair.targetPath, pair.linkPath} {
		if _, err := fixture.client.Doc(path).Get(context.Background()); err != nil {
			t.Fatalf("linked document %q missing: %v", path, err)
		}
	}
}
