package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreEmulatorReceiptPurgeLegacyInventoryBackfillsAndReplays(t *testing.T) {
	fixture := seedReceiptPurgeLegacyInventoryEmulatorFixture(t)
	deleteReceiptPurgeLegacyLink(t, fixture)

	page, err := fixture.store.ListReceiptPurgeLegacyTargetPage(
		context.Background(),
		fixture.request,
	)
	if err != nil || len(page.DocumentIDs) != 1 ||
		page.DocumentIDs[0] != fixture.target.Command.CleanupID || !page.ObservedExhausted {
		t.Fatalf("ListReceiptPurgeLegacyTargetPage() = %#v, %v", page, err)
	}
	plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(context.Background(), page)
	if err != nil || plan.ObservedUnregisteredCount != 1 ||
		len(plan.LinksToCreate) != 1 || plan.HeldStatus != "" {
		t.Fatalf("BackfillReceiptPurgeLegacyTargetPage() = %#v, %v", plan, err)
	}
	persisted := readReceiptPurgeLegacyLink(t, fixture)
	if ingest.ValidateReceiptPurgeInverseLinkPair(persisted.Link, fixture.child) != nil {
		t.Fatalf("persisted link = %#v", persisted.Link)
	}
	firstUpdateTime := receiptPurgeLegacyLinkSnapshot(t, fixture).UpdateTime

	replay, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(context.Background(), page)
	if err != nil || replay.ObservedRegisteredCount != 1 ||
		replay.ObservedUnregisteredCount != 0 || len(replay.LinksToCreate) != 0 {
		t.Fatalf("replay BackfillReceiptPurgeLegacyTargetPage() = %#v, %v", replay, err)
	}
	if updateTime := receiptPurgeLegacyLinkSnapshot(t, fixture).UpdateTime; !updateTime.Equal(firstUpdateTime) {
		t.Fatalf("replay update time = %v, want unchanged %v", updateTime, firstUpdateTime)
	}
}

func TestFirestoreEmulatorReceiptPurgeLegacyInventoryFindingStopsWrites(t *testing.T) {
	fixture := seedReceiptPurgeLegacyInventoryEmulatorFixture(t)
	deleteReceiptPurgeLegacyLink(t, fixture)
	if _, err := fixture.client.Doc(
		receiptPurgeLegacyFindingCollectionPath(fixture.request.TenantID)+"/legacy-finding-001",
	).Set(context.Background(), map[string]any{"unsupported_body": true}); err != nil {
		t.Fatalf("seed legacy finding: %v", err)
	}
	page, err := fixture.store.ListReceiptPurgeLegacyTargetPage(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("ListReceiptPurgeLegacyTargetPage() = %v", err)
	}
	plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeLegacyFindingUnsupported) ||
		!reflect.DeepEqual(plan, ingest.ReceiptPurgeLegacyBackfillPlan{}) {
		t.Fatalf("BackfillReceiptPurgeLegacyTargetPage() = %#v, %v", plan, err)
	}
	assertReceiptPurgeLegacyLinkMissing(t, fixture)
}

func TestFirestoreEmulatorReceiptPurgeLegacyInventoryMalformedTargetHoldsPage(t *testing.T) {
	fixture := seedReceiptPurgeLegacyInventoryEmulatorFixture(t)
	deleteReceiptPurgeLegacyLink(t, fixture)
	if _, err := fixture.client.Doc(fixture.targetPath).Set(context.Background(), map[string]any{
		"schema_version": ingest.CleanupTargetSchemaVersion,
		"cleanup_id":     fixture.target.Command.CleanupID,
		"tenant_id":      fixture.request.TenantID,
	}); err != nil {
		t.Fatalf("replace target with malformed legacy body: %v", err)
	}
	page, err := fixture.store.ListReceiptPurgeLegacyTargetPage(context.Background(), fixture.request)
	if err != nil || len(page.DocumentIDs) != 1 {
		t.Fatalf("ListReceiptPurgeLegacyTargetPage() = %#v, %v", page, err)
	}
	plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(context.Background(), page)
	if err != nil || plan.HeldDocumentID != fixture.target.Command.CleanupID ||
		plan.HeldStatus != ingest.ReceiptPurgeLegacyTargetMalformedChild ||
		plan.NextCursor != fixture.request.Cursor || len(plan.LinksToCreate) != 0 {
		t.Fatalf("BackfillReceiptPurgeLegacyTargetPage() = %#v, %v", plan, err)
	}
	assertReceiptPurgeLegacyLinkMissing(t, fixture)
}

func TestFirestoreEmulatorReceiptPurgeLegacyInventoryClassifiesPoisonBindings(t *testing.T) {
	for _, test := range []struct {
		name   string
		status ingest.ReceiptPurgeLegacyTargetStatus
		mutate func(*testing.T, *receiptPurgeLegacyInventoryEmulatorFixture)
	}{
		{
			name:   "foreign target",
			status: ingest.ReceiptPurgeLegacyTargetForeignChild,
			mutate: func(t *testing.T, fixture *receiptPurgeLegacyInventoryEmulatorFixture) {
				deleteReceiptPurgeLegacyLink(t, fixture)
				command := fixture.target.Command
				command.TenantID = "11111111-1111-4111-8111-111111111111"
				replaceReceiptPurgeLegacyTarget(t, fixture, command)
			},
		},
		{
			name:   "target identity drift",
			status: ingest.ReceiptPurgeLegacyTargetLinkageDrift,
			mutate: func(t *testing.T, fixture *receiptPurgeLegacyInventoryEmulatorFixture) {
				deleteReceiptPurgeLegacyLink(t, fixture)
				command := fixture.target.Command
				command.CleanupID = "22222222-2222-4222-8222-222222222222"
				command.AttemptID = command.CleanupID
				replaceReceiptPurgeLegacyTarget(t, fixture, command)
			},
		},
		{
			name:   "target reservation drift",
			status: ingest.ReceiptPurgeLegacyTargetLinkageDrift,
			mutate: func(t *testing.T, fixture *receiptPurgeLegacyInventoryEmulatorFixture) {
				deleteReceiptPurgeLegacyLink(t, fixture)
				command := fixture.target.Command
				command.ReservationKey = strings.Repeat("b", 64)
				replaceReceiptPurgeLegacyTarget(t, fixture, command)
			},
		},
		{
			name:   "invalid parent state",
			status: ingest.ReceiptPurgeLegacyTargetLinkageDrift,
			mutate: func(t *testing.T, fixture *receiptPurgeLegacyInventoryEmulatorFixture) {
				deleteReceiptPurgeLegacyLink(t, fixture)
				if _, err := fixture.client.Doc(fixture.receiptPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "status", Value: "future_state"}},
				); err != nil {
					t.Fatalf("corrupt parent receipt state: %v", err)
				}
			},
		},
		{
			name:   "corrupt existing link",
			status: ingest.ReceiptPurgeLegacyTargetLinkageDrift,
			mutate: func(t *testing.T, fixture *receiptPurgeLegacyInventoryEmulatorFixture) {
				if _, err := fixture.client.Doc(fixture.linkPath).Update(
					context.Background(),
					[]firestore.Update{{Path: "document_id", Value: "33333333-3333-4333-8333-333333333333"}},
				); err != nil {
					t.Fatalf("corrupt inverse link: %v", err)
				}
			},
		},
		{
			name:   "unregistered after fence",
			status: ingest.ReceiptPurgeLegacyTargetFencedUnregistered,
			mutate: func(t *testing.T, fixture *receiptPurgeLegacyInventoryEmulatorFixture) {
				deleteReceiptPurgeLegacyLink(t, fixture)
				startedAt := fixture.receipt.ReceiptRetentionFloor.Add(time.Hour).UTC()
				purgeKey, err := ingest.DeriveReceiptPurgeKey(
					fixture.target.Command.TenantID,
					fixture.target.Command.ReceiptID,
				)
				if err != nil {
					t.Fatalf("DeriveReceiptPurgeKey() = %v", err)
				}
				if _, err := fixture.client.Doc(fixture.receiptPath).Update(
					context.Background(),
					[]firestore.Update{
						{Path: "status", Value: string(ingest.ReceiptExpired)},
						{Path: "lease_owner_id", Value: ""},
						{Path: "lease_owner_kind", Value: ""},
						{Path: "lease_acquired_at", Value: time.Time{}},
						{Path: "lease_heartbeat_at", Value: time.Time{}},
						{Path: "lease_expires_at", Value: time.Time{}},
						{Path: "next_recovery_at", Value: time.Time{}},
						{Path: "last_recovery_code", Value: ""},
						{Path: "purge_eligible_at", Value: fixture.receipt.ReceiptRetentionFloor},
						{Path: "purge_job_id", Value: purgeKey},
						{Path: "purge_started_at", Value: startedAt},
						{Path: "purge_fence_version", Value: ingest.ReceiptPurgeFenceVersion},
						{Path: "updated_at", Value: startedAt},
					},
				); err != nil {
					t.Fatalf("fence parent receipt: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := seedReceiptPurgeLegacyInventoryEmulatorFixture(t)
			test.mutate(t, fixture)
			page, err := fixture.store.ListReceiptPurgeLegacyTargetPage(
				context.Background(), fixture.request,
			)
			if err != nil {
				t.Fatalf("ListReceiptPurgeLegacyTargetPage() = %v", err)
			}
			plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(
				context.Background(), page,
			)
			if err != nil || plan.HeldDocumentID != fixture.target.Command.CleanupID ||
				plan.HeldStatus != test.status || len(plan.LinksToCreate) != 0 ||
				plan.NextCursor != fixture.request.Cursor {
				t.Fatalf("BackfillReceiptPurgeLegacyTargetPage() = %#v, %v", plan, err)
			}
		})
	}
}

func TestFirestoreEmulatorReceiptPurgeLegacyInventoryRejectsStalePage(t *testing.T) {
	fixture := seedReceiptPurgeLegacyInventoryEmulatorFixture(t)
	deleteReceiptPurgeLegacyLink(t, fixture)
	page, err := fixture.store.ListReceiptPurgeLegacyTargetPage(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("ListReceiptPurgeLegacyTargetPage() = %v", err)
	}
	insertedID := "00000000-0000-4000-8000-000000000001"
	if _, err := fixture.client.Doc(
		cleanupTargetDocumentPath(fixture.request.TenantID, insertedID),
	).Set(context.Background(), map[string]any{"legacy": true}); err != nil {
		t.Fatalf("insert stale-page target: %v", err)
	}
	plan, err := fixture.store.BackfillReceiptPurgeLegacyTargetPage(context.Background(), page)
	if !errors.Is(err, ingest.ErrReceiptPurgeLegacyInventoryConflict) ||
		!reflect.DeepEqual(plan, ingest.ReceiptPurgeLegacyBackfillPlan{}) {
		t.Fatalf("BackfillReceiptPurgeLegacyTargetPage() = %#v, %v", plan, err)
	}
	assertReceiptPurgeLegacyLinkMissing(t, fixture)
}

func TestFirestoreEmulatorReceiptPurgeLegacyInventoryConcurrentBackfillConverges(t *testing.T) {
	fixture := seedReceiptPurgeLegacyInventoryEmulatorFixture(t)
	deleteReceiptPurgeLegacyLink(t, fixture)
	page, err := fixture.store.ListReceiptPurgeLegacyTargetPage(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("ListReceiptPurgeLegacyTargetPage() = %v", err)
	}
	type outcome struct {
		plan ingest.ReceiptPurgeLegacyBackfillPlan
		err  error
	}
	outcomes := make(chan outcome, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			plan, backfillErr := fixture.store.BackfillReceiptPurgeLegacyTargetPage(
				context.Background(), page,
			)
			outcomes <- outcome{plan: plan, err: backfillErr}
		}()
	}
	workers.Wait()
	close(outcomes)
	created := 0
	replayed := 0
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("concurrent BackfillReceiptPurgeLegacyTargetPage() = %v", result.err)
		}
		if result.plan.ObservedUnregisteredCount == 1 && len(result.plan.LinksToCreate) == 1 {
			created++
		}
		if result.plan.ObservedRegisteredCount == 1 && len(result.plan.LinksToCreate) == 0 {
			replayed++
		}
	}
	if created != 1 || replayed != 1 {
		t.Fatalf("concurrent outcomes created/replayed = %d/%d", created, replayed)
	}
	readReceiptPurgeLegacyLink(t, fixture)
}

type receiptPurgeLegacyInventoryEmulatorFixture struct {
	*cleanupExpiryFinalizationEmulatorFixture
	request    ingest.ReceiptPurgeLegacyInventoryRequest
	target     ingest.CleanupTarget
	targetPath string
	child      ingest.ReceiptPurgeLinkedChildIdentity
	linkPath   string
}

func seedReceiptPurgeLegacyInventoryEmulatorFixture(
	t *testing.T,
) *receiptPurgeLegacyInventoryEmulatorFixture {
	t.Helper()
	base := seedReadyCleanupExpiryFinalizationEmulatorFixture(t)
	snapshot, err := base.client.Doc(base.targetPath).Get(context.Background())
	if err != nil {
		t.Fatalf("read cleanup target: %v", err)
	}
	var stored firestoreIngestCleanupTarget
	if validateCleanupTargetDocumentShape(snapshot.Data()) != nil || snapshot.DataTo(&stored) != nil {
		t.Fatal("decode strict cleanup target fixture")
	}
	target, err := stored.toDomain()
	if err != nil {
		t.Fatalf("cleanup target toDomain() = %v", err)
	}
	child := cleanupTargetPurgeChildIdentity(target.Command)
	link, err := ingest.BuildReceiptPurgeInverseLink(child)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	return &receiptPurgeLegacyInventoryEmulatorFixture{
		cleanupExpiryFinalizationEmulatorFixture: base,
		request: ingest.ReceiptPurgeLegacyInventoryRequest{
			TenantID: target.Command.TenantID,
			PageSize: ingest.MaxReceiptPurgeLegacyInventoryPageSize,
		},
		target: target, targetPath: base.targetPath, child: child,
		linkPath: receiptPurgeLinkDocumentPath(
			target.Command.TenantID,
			target.Command.ReceiptID,
			link.LinkID,
		),
	}
}

func deleteReceiptPurgeLegacyLink(
	t *testing.T,
	fixture *receiptPurgeLegacyInventoryEmulatorFixture,
) {
	t.Helper()
	if _, err := fixture.client.Doc(fixture.linkPath).Delete(context.Background()); err != nil {
		t.Fatalf("delete inverse link to simulate legacy target: %v", err)
	}
}

func replaceReceiptPurgeLegacyTarget(
	t *testing.T,
	fixture *receiptPurgeLegacyInventoryEmulatorFixture,
	command ingest.CleanupTargetCommand,
) {
	t.Helper()
	targetHash, err := ingest.CleanupTargetHash(command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	if _, err := fixture.client.Doc(fixture.targetPath).Set(
		context.Background(),
		newFirestoreCleanupTarget(command, targetHash),
	); err != nil {
		t.Fatalf("replace cleanup target: %v", err)
	}
}

func receiptPurgeLegacyLinkSnapshot(
	t *testing.T,
	fixture *receiptPurgeLegacyInventoryEmulatorFixture,
) *firestore.DocumentSnapshot {
	t.Helper()
	snapshot, err := fixture.client.Doc(fixture.linkPath).Get(context.Background())
	if err != nil {
		t.Fatalf("read inverse link: %v", err)
	}
	return snapshot
}

func readReceiptPurgeLegacyLink(
	t *testing.T,
	fixture *receiptPurgeLegacyInventoryEmulatorFixture,
) receiptPurgeLinkRead {
	t.Helper()
	snapshot := receiptPurgeLegacyLinkSnapshot(t, fixture)
	var stored firestoreReceiptPurgeLink
	if validateReceiptPurgeLinkDocumentShape(snapshot.Data()) != nil || snapshot.DataTo(&stored) != nil {
		t.Fatal("decode strict inverse link")
	}
	link, err := stored.toDomain(
		snapshot.Ref.ID,
		fixture.target.Command.TenantID,
		fixture.target.Command.ReceiptID,
	)
	if err != nil {
		t.Fatalf("inverse link toDomain() = %v", err)
	}
	return receiptPurgeLinkRead{Link: link, ReadTime: snapshot.ReadTime.UTC()}
}

func assertReceiptPurgeLegacyLinkMissing(
	t *testing.T,
	fixture *receiptPurgeLegacyInventoryEmulatorFixture,
) {
	t.Helper()
	_, err := fixture.client.Doc(fixture.linkPath).Get(context.Background())
	if status.Code(err) != codes.NotFound {
		t.Fatalf("inverse link read = %v, want not found", err)
	}
}
