package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreReceiptPurgeLegacyBackfillCreatesOnlyMissingLinks(t *testing.T) {
	fixture := newReceiptPurgeLegacyInventoryAdapterFixture(t, 2)
	fixture.seedRegistered(t, fixture.commands[0])

	page := fixture.listPage(t)
	plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(context.Background(), page)
	if err != nil {
		t.Fatalf("BackfillReceiptPurgeLegacyTargetPage() = %v", err)
	}
	if plan.ObservedRegisteredCount != 1 || plan.ObservedUnregisteredCount != 1 ||
		len(plan.LinksToCreate) != 1 || plan.NextCursor != fixture.documentIDs[1] ||
		!plan.ObservedExhausted || plan.HeldStatus != "" {
		t.Fatalf("mixed backfill plan = %#v", plan)
	}
	wantLink := receiptPurgeLegacyExpectedLink(t, fixture.commands[1])
	if plan.LinksToCreate[0] != wantLink {
		t.Fatalf("created link plan = %#v, want %#v", plan.LinksToCreate[0], wantLink)
	}
	if len(fixture.transaction.creates) != 1 || len(fixture.transaction.updates) != 0 {
		t.Fatalf(
			"transaction creates/updates = %d/%d, want 1/0",
			len(fixture.transaction.creates),
			len(fixture.transaction.updates),
		)
	}
	created := fixture.transaction.creates[0]
	wantPath := receiptPurgeLinkDocumentPath(
		wantLink.TenantID,
		wantLink.ReceiptID,
		wantLink.LinkID,
	)
	if created.path != wantPath {
		t.Fatalf("created path = %q, want %q", created.path, wantPath)
	}
	persisted, ok := created.value.(firestoreReceiptPurgeLink)
	if !ok {
		t.Fatalf("created value type = %T, want firestoreReceiptPurgeLink", created.value)
	}
	roundTripped, err := persisted.toDomain(
		wantLink.LinkID,
		wantLink.TenantID,
		wantLink.ReceiptID,
	)
	if err != nil || roundTripped != wantLink {
		t.Fatalf("created link = %#v, %v; want %#v", roundTripped, err, wantLink)
	}
	registeredLink := receiptPurgeLegacyExpectedLink(t, fixture.commands[0])
	if _, exists := fixture.transaction.links[receiptPurgeLinkDocumentPath(
		registeredLink.TenantID,
		registeredLink.ReceiptID,
		registeredLink.LinkID,
	)]; !exists {
		t.Fatal("pre-registered link was lost")
	}
}

func TestFirestoreReceiptPurgeLegacyBackfillRegisteredReplayWritesNothing(t *testing.T) {
	fixture := newReceiptPurgeLegacyInventoryAdapterFixture(t, 1)
	fixture.seedRegistered(t, fixture.commands[0])

	page := fixture.listPage(t)
	plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(context.Background(), page)
	if err != nil {
		t.Fatalf("BackfillReceiptPurgeLegacyTargetPage() = %v", err)
	}
	if plan.ObservedRegisteredCount != 1 || plan.ObservedUnregisteredCount != 0 ||
		len(plan.LinksToCreate) != 0 || plan.NextCursor != fixture.documentIDs[0] ||
		!plan.ObservedExhausted || plan.HeldStatus != "" {
		t.Fatalf("registered replay plan = %#v", plan)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreReceiptPurgeLegacyBackfillRejectsStaleExactPage(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*receiptPurgeLegacyInventoryAdapterFixture)
	}{
		{
			name: "inserted prefix",
			mutate: func(fixture *receiptPurgeLegacyInventoryAdapterFixture) {
				fixture.transaction.pageIDs = append(
					[]string{"018f1f4e-2f5e-7d31-8c77-43b50f4c9199"},
					fixture.transaction.pageIDs...,
				)
			},
		},
		{
			name: "changed lookahead",
			mutate: func(fixture *receiptPurgeLegacyInventoryAdapterFixture) {
				fixture.transaction.pageIDs[2] = "018f1f4e-2f5e-7d31-8c77-43b50f4c91ad"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReceiptPurgeLegacyInventoryAdapterFixture(t, 3)
			fixture.request.PageSize = 2
			page := fixture.listPage(t)
			test.mutate(fixture)

			plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(
				context.Background(),
				page,
			)
			if !errors.Is(err, ingest.ErrReceiptPurgeLegacyInventoryConflict) ||
				!reflect.DeepEqual(plan, ingest.ReceiptPurgeLegacyBackfillPlan{}) {
				t.Fatalf("stale exact page = %#v, %v", plan, err)
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestFirestoreReceiptPurgeLegacyBackfillFindingUnsupportedWritesNothing(t *testing.T) {
	fixture := newReceiptPurgeLegacyInventoryAdapterFixture(t, 1)
	page := fixture.listPage(t)
	fixture.transaction.findingID = "legacy-finding-001"

	plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeLegacyFindingUnsupported) ||
		!reflect.DeepEqual(plan, ingest.ReceiptPurgeLegacyBackfillPlan{}) {
		t.Fatalf("unsupported finding result = %#v, %v", plan, err)
	}
	fixture.assertNoWrites(t)
	if fixture.transaction.targetReadCalls != 0 {
		t.Fatalf("target reads after unsupported finding = %d, want 0", fixture.transaction.targetReadCalls)
	}
}

func TestFirestoreReceiptPurgeLegacyBackfillPoisonHoldsWholePage(t *testing.T) {
	for _, poison := range []ingest.ReceiptPurgeLegacyTargetStatus{
		ingest.ReceiptPurgeLegacyTargetMalformedChild,
		ingest.ReceiptPurgeLegacyTargetForeignChild,
		ingest.ReceiptPurgeLegacyTargetLinkageDrift,
		ingest.ReceiptPurgeLegacyTargetFencedUnregistered,
	} {
		t.Run(string(poison), func(t *testing.T) {
			fixture := newReceiptPurgeLegacyInventoryAdapterFixture(t, 2)
			poisoned := fixture.transaction.targetReads[fixture.documentIDs[1]]
			poisoned.Target = ingest.CleanupTarget{}
			poisoned.Status = poison
			fixture.transaction.targetReads[fixture.documentIDs[1]] = poisoned

			page := fixture.listPage(t)
			plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(
				context.Background(),
				page,
			)
			if err != nil {
				t.Fatalf("BackfillReceiptPurgeLegacyTargetPage() = %v", err)
			}
			if plan.HeldDocumentID != fixture.documentIDs[1] ||
				plan.HeldStatus != poison || len(plan.LinksToCreate) != 0 ||
				plan.NextCursor != fixture.request.Cursor || plan.ObservedExhausted ||
				plan.ObservedUnregisteredCount != 1 {
				t.Fatalf("poison hold plan = %#v", plan)
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestNormalizeReceiptPurgeLegacyInventoryErrorPreservesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := normalizeReceiptPurgeLegacyInventoryError(
		ctx,
		status.Error(codes.Aborted, "transaction aborted after cancellation"),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("normalizeReceiptPurgeLegacyInventoryError() = %v, want canceled", err)
	}
}

type receiptPurgeLegacyInventoryAdapterFixture struct {
	base        *cleanupTargetAdapterFixture
	transaction *fakeReceiptPurgeLegacyInventoryTransaction
	store       *FirestoreAdmissionStore
	request     ingest.ReceiptPurgeLegacyInventoryRequest
	commands    []ingest.CleanupTargetCommand
	documentIDs []string
}

func newReceiptPurgeLegacyInventoryAdapterFixture(
	t *testing.T,
	targetCount int,
) *receiptPurgeLegacyInventoryAdapterFixture {
	t.Helper()
	if targetCount < 1 || targetCount > 3 {
		t.Fatalf("unsupported legacy target fixture count %d", targetCount)
	}
	base := newCleanupTargetAdapterFixture(t)
	documentIDs := []string{
		base.command.CleanupID,
		"018f1f4e-2f5e-7d31-8c77-43b50f4c91ab",
		"018f1f4e-2f5e-7d31-8c77-43b50f4c91ac",
	}[:targetCount]
	commands := make([]ingest.CleanupTargetCommand, targetCount)
	targetReads := make(map[string]receiptPurgeLegacyTargetRead, targetCount)
	for index, documentID := range documentIDs {
		command := cloneCleanupTargetCommand(base.command)
		command.CleanupID = documentID
		command.AttemptID = documentID
		if err := ingest.ValidateCleanupTargetCommand(command); err != nil {
			t.Fatalf("legacy cleanup target command %d = %v", index, err)
		}
		targetHash, err := ingest.CleanupTargetHash(command)
		if err != nil {
			t.Fatalf("CleanupTargetHash(command %d) = %v", index, err)
		}
		commands[index] = command
		targetReads[documentID] = receiptPurgeLegacyTargetRead{
			DocumentID: documentID,
			Target: ingest.CleanupTarget{
				Command: command, TargetHash: targetHash,
			},
		}
	}
	transaction := &fakeReceiptPurgeLegacyInventoryTransaction{
		fakeCleanupTargetTransaction: base.transaction,
		pageIDs:                      append([]string(nil), documentIDs...),
		targetReads:                  targetReads,
	}
	store := admissionTestStore(base.observedAt, admissionRunner(transaction))
	return &receiptPurgeLegacyInventoryAdapterFixture{
		base: base, transaction: transaction, store: store,
		request: ingest.ReceiptPurgeLegacyInventoryRequest{
			TenantID: base.command.TenantID, PageSize: targetCount,
		},
		commands: commands, documentIDs: append([]string(nil), documentIDs...),
	}
}

func (fixture *receiptPurgeLegacyInventoryAdapterFixture) listPage(
	t *testing.T,
) ingest.ReceiptPurgeLegacyInventoryPage {
	t.Helper()
	page, err := fixture.store.ListReceiptPurgeLegacyTargetPage(
		context.Background(),
		fixture.request,
	)
	if err != nil {
		t.Fatalf("ListReceiptPurgeLegacyTargetPage() = %v", err)
	}
	return page
}

func (fixture *receiptPurgeLegacyInventoryAdapterFixture) seedRegistered(
	t *testing.T,
	command ingest.CleanupTargetCommand,
) {
	t.Helper()
	link := receiptPurgeLegacyExpectedLink(t, command)
	path := receiptPurgeLinkDocumentPath(link.TenantID, link.ReceiptID, link.LinkID)
	fixture.transaction.links[path] = receiptPurgeLinkRead{
		Link: link, ReadTime: fixture.base.observedAt,
	}
}

func (fixture *receiptPurgeLegacyInventoryAdapterFixture) assertNoWrites(t *testing.T) {
	t.Helper()
	if len(fixture.transaction.creates) != 0 || len(fixture.transaction.updates) != 0 {
		t.Fatalf(
			"transaction creates/updates = %d/%d, want 0/0",
			len(fixture.transaction.creates),
			len(fixture.transaction.updates),
		)
	}
}

func receiptPurgeLegacyExpectedLink(
	t *testing.T,
	command ingest.CleanupTargetCommand,
) ingest.ReceiptPurgeInverseLink {
	t.Helper()
	link, err := ingest.BuildReceiptPurgeInverseLink(cleanupTargetPurgeChildIdentity(command))
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	return link
}

type fakeReceiptPurgeLegacyInventoryTransaction struct {
	*fakeCleanupTargetTransaction
	pageIDs         []string
	findingID       string
	targetReads     map[string]receiptPurgeLegacyTargetRead
	targetReadCalls int
}

func (transaction *fakeReceiptPurgeLegacyInventoryTransaction) QueryReceiptPurgeLegacyTargetPage(
	_ context.Context,
	request ingest.ReceiptPurgeLegacyInventoryRequest,
) ([]string, error) {
	values := make([]string, 0, request.PageSize+1)
	for _, documentID := range transaction.pageIDs {
		if documentID <= request.Cursor {
			continue
		}
		values = append(values, documentID)
		if len(values) == request.PageSize+1 {
			break
		}
	}
	return values, nil
}

func (transaction *fakeReceiptPurgeLegacyInventoryTransaction) QueryFirstReceiptPurgeLegacyFinding(
	context.Context,
	string,
) (string, error) {
	return transaction.findingID, nil
}

func (transaction *fakeReceiptPurgeLegacyInventoryTransaction) ReadReceiptPurgeLegacyTargets(
	_ context.Context,
	_ string,
	documentIDs []string,
) ([]receiptPurgeLegacyTargetRead, error) {
	transaction.targetReadCalls++
	reads := make([]receiptPurgeLegacyTargetRead, len(documentIDs))
	for index, documentID := range documentIDs {
		read, exists := transaction.targetReads[documentID]
		if !exists {
			read = receiptPurgeLegacyTargetRead{
				DocumentID: documentID,
				Status:     ingest.ReceiptPurgeLegacyTargetLinkageDrift,
			}
		}
		reads[index] = read
	}
	return reads, nil
}
