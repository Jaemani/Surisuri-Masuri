package firebaseadapter

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	emulatorProjectID         = "demo-mobility-reliability"
	emulatorTenantID          = "01982015-4400-7000-8000-000000000101"
	emulatorInstallationID    = "01982015-4400-7000-8000-000000000102"
	emulatorTripID            = "01982015-4400-7000-8000-000000000103"
	emulatorConsentID         = "01982015-4400-7000-8000-000000000104"
	emulatorAssignmentID      = "01982015-4400-7000-8000-000000000105"
	emulatorPersonID          = "01982015-4400-7000-8000-000000000106"
	emulatorDeviceID          = "01982015-4400-7000-8000-000000000107"
	emulatorClientSessionID   = "01982015-4400-7000-8000-000000000108"
	emulatorClientBatchID     = "01982015-4400-7000-8000-000000000109"
	emulatorFirstReceiptID    = "01982015-4400-7000-8000-00000000010a"
	emulatorSecondReceiptID   = "01982015-4400-7000-8000-00000000010b"
	emulatorFirebaseUID       = "firestore-emulator-user"
	emulatorAppID             = "1:1234567890:android:emulator-app"
	emulatorBodyHash          = "16a42ebf7f1004546f39b50e452c7c5777c075bed43d90df49ab0cfba4e3748f"
	emulatorTransactionTimout = 10 * time.Second
)

func TestFirestoreAdmissionStoreEmulatorConcurrentSameBatch(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	seedAdmissionAuthorization(t, client, now)
	barrier := newMissingIndexBarrier(clientBatchDocumentPath(emulatorTenantID, ingest.DeriveClientBatchKey(
		emulatorTenantID,
		emulatorClientBatchID,
	)))
	store, err := newContendedAdmissionEmulatorStore(client, now, barrier)
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}
	principal, scope := emulatorAdmissionIdentity(now)

	reservations := []ingest.Reservation{
		emulatorReservation(now, emulatorFirstReceiptID),
		emulatorReservation(now, emulatorSecondReceiptID),
	}
	type outcome struct {
		receipt ingest.Receipt
		status  ingest.ReservationStatus
		err     error
	}
	outcomes := make([]outcome, len(reservations))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range reservations {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			outcomes[index].receipt, outcomes[index].status, outcomes[index].err =
				store.AuthorizeAndReserve(
					context.Background(),
					principal,
					scope,
					reservations[index],
				)
		}(index)
	}
	close(start)
	wait.Wait()

	statusCounts := map[ingest.ReservationStatus]int{}
	for index, result := range outcomes {
		if result.err != nil {
			t.Fatalf("concurrent admission %d error = %v", index, result.err)
		}
		statusCounts[result.status]++
	}
	if statusCounts[ingest.ReservationCreated] != 1 || statusCounts[ingest.ReservationReplayPending] != 1 {
		t.Fatalf("concurrent statuses = %#v, want one created and one replay pending", statusCounts)
	}
	if loads := barrier.authorizationLoads.Load(); loads < 3 {
		t.Fatalf("authorization callback loads = %d, want at least 3 to prove transaction retry", loads)
	}
	if outcomes[0].receipt.BatchID != outcomes[1].receipt.BatchID ||
		outcomes[0].receipt.ReceiptID != outcomes[1].receipt.ReceiptID {
		t.Fatalf("concurrent receipts diverged = %#v / %#v", outcomes[0].receipt, outcomes[1].receipt)
	}

	assertAdmissionCollectionCount(t, client, "ingestIdempotency", 1)
	assertAdmissionCollectionCount(t, client, "ingestClientBatches", 1)
	assertAdmissionCollectionCount(t, client, "ingestReceipts", 1)

	winningReceipt := outcomes[0].receipt
	idempotencyDocument, err := client.Doc(idempotencyDocumentPath(
		emulatorTenantID,
		reservations[0].ReservationKey,
	)).Get(context.Background())
	if err != nil {
		t.Fatalf("read idempotency index: %v", err)
	}
	clientBatchDocument, err := client.Doc(clientBatchDocumentPath(
		emulatorTenantID,
		reservations[0].ClientBatchKey,
	)).Get(context.Background())
	if err != nil {
		t.Fatalf("read client-batch index: %v", err)
	}
	var idempotencyIndex firestoreIngestIndex
	var clientBatchIndex firestoreIngestIndex
	if err := idempotencyDocument.DataTo(&idempotencyIndex); err != nil {
		t.Fatalf("decode idempotency index: %v", err)
	}
	if err := clientBatchDocument.DataTo(&clientBatchIndex); err != nil {
		t.Fatalf("decode client-batch index: %v", err)
	}
	if !sameIngestIndex(idempotencyIndex, clientBatchIndex) ||
		idempotencyIndex.ReceiptID != winningReceipt.ReceiptID ||
		idempotencyIndex.BatchID != winningReceipt.BatchID {
		t.Fatalf("winning index lineage diverged = %#v / %#v", idempotencyIndex, clientBatchIndex)
	}
	storedDocument, err := client.Doc(receiptDocumentPath(emulatorTenantID, winningReceipt.ReceiptID)).Get(context.Background())
	if err != nil {
		t.Fatalf("read winning receipt: %v", err)
	}
	var stored firestoreIngestReceipt
	if err := storedDocument.DataTo(&stored); err != nil {
		t.Fatalf("decode winning receipt: %v", err)
	}
	if stored.toDomain() != winningReceipt {
		t.Fatalf("stored receipt = %#v, returned = %#v", stored.toDomain(), winningReceipt)
	}
	losingReceiptID := reservations[0].ReceiptID
	if losingReceiptID == winningReceipt.ReceiptID {
		losingReceiptID = reservations[1].ReceiptID
	}
	if _, err := client.Doc(receiptDocumentPath(emulatorTenantID, losingReceiptID)).Get(context.Background()); status.Code(err) != codes.NotFound {
		t.Fatalf("losing proposed receipt read error = %v, want not found", err)
	}
}

func TestFirestoreAdmissionStoreEmulatorMissingAuthorizationCreatesNothing(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	seedAdmissionAuthorization(t, client, now)
	consentStateID := authorization.ConsentStateDocumentID(
		emulatorPersonID,
		authorization.PreciseLocationPurpose,
	)
	if _, err := client.Doc(
		"tenants/" + emulatorTenantID + "/consentStates/" + consentStateID,
	).Delete(context.Background()); err != nil {
		t.Fatalf("delete consent state: %v", err)
	}
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}
	principal, scope := emulatorAdmissionIdentity(now)

	_, _, err = store.AuthorizeAndReserve(
		context.Background(),
		principal,
		scope,
		emulatorReservation(now, emulatorFirstReceiptID),
	)
	if !errors.Is(err, ingest.ErrBatchUnauthorized) {
		t.Fatalf("AuthorizeAndReserve() error = %v, want unauthorized", err)
	}
	assertAdmissionCollectionCount(t, client, "ingestIdempotency", 0)
	assertAdmissionCollectionCount(t, client, "ingestClientBatches", 0)
	assertAdmissionCollectionCount(t, client, "ingestReceipts", 0)
}

func newAdmissionEmulatorClient(t *testing.T) *firestore.Client {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST is required for transaction integration")
	}
	client, err := firestore.NewClient(context.Background(), emulatorProjectID)
	if err != nil {
		t.Fatalf("firestore.NewClient() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Firestore client: %v", err)
		}
	})
	return client
}

type missingIndexBarrier struct {
	path               string
	ready              chan struct{}
	mu                 sync.Mutex
	arrivals           int
	authorizationLoads atomic.Int32
}

func newMissingIndexBarrier(path string) *missingIndexBarrier {
	return &missingIndexBarrier{path: path, ready: make(chan struct{})}
}

func (barrier *missingIndexBarrier) waitForTwo() {
	barrier.mu.Lock()
	barrier.arrivals++
	if barrier.arrivals == 2 {
		close(barrier.ready)
	}
	barrier.mu.Unlock()
	<-barrier.ready
}

type barrierAdmissionTransaction struct {
	admissionTransaction
	barrier *missingIndexBarrier
}

func (transaction barrierAdmissionTransaction) LoadAuthorization(
	ctx context.Context,
	principal ingest.Principal,
	scope ingest.BatchScope,
) (authorization.Snapshot, error) {
	transaction.barrier.authorizationLoads.Add(1)
	return transaction.admissionTransaction.LoadAuthorization(ctx, principal, scope)
}

func (transaction barrierAdmissionTransaction) ReadIndex(
	ctx context.Context,
	path string,
) (firestoreIngestIndex, bool, error) {
	index, exists, err := transaction.admissionTransaction.ReadIndex(ctx, path)
	if err == nil && !exists && path == transaction.barrier.path {
		transaction.barrier.waitForTwo()
	}
	return index, exists, err
}

func newContendedAdmissionEmulatorStore(
	client *firestore.Client,
	now time.Time,
	barrier *missingIndexBarrier,
) (*FirestoreAdmissionStore, error) {
	store, err := NewFirestoreAdmissionStore(
		client,
		emulatorTransactionTimout,
		func() time.Time { return now },
	)
	if err != nil {
		return nil, err
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
				base := firestoreAdmissionTransaction{client: client, transaction: transaction}
				return operation(runContext, barrierAdmissionTransaction{
					admissionTransaction: base,
					barrier:              barrier,
				})
			},
		)
	}
	return store, nil
}

func seedAdmissionAuthorization(t *testing.T, client *firestore.Client, now time.Time) {
	t.Helper()
	validFrom := now.Add(-24 * time.Hour)
	consentStateID := authorization.ConsentStateDocumentID(
		emulatorPersonID,
		authorization.PreciseLocationPurpose,
	)
	tenantPrefix := "tenants/" + emulatorTenantID
	batch := client.Batch()
	batch.Set(client.Doc(tenantPrefix), firestoreTenant{
		TenantID: emulatorTenantID,
		Status:   "active",
	})
	batch.Set(client.Doc(tenantPrefix+"/memberships/"+emulatorFirebaseUID), firestoreMembership{
		TenantID: emulatorTenantID, FirebaseUID: emulatorFirebaseUID, PersonID: emulatorPersonID,
		Roles: []string{"beneficiary"}, Status: "active", ValidFrom: validFrom,
	})
	batch.Set(client.Doc(tenantPrefix+"/appInstallations/"+emulatorInstallationID), firestoreInstallation{
		TenantID: emulatorTenantID, InstallationID: emulatorInstallationID,
		FirebaseUID: emulatorFirebaseUID, AppID: emulatorAppID, Status: "active",
		SchemaVersion: 1, Revision: 1, RegisteredAt: validFrom,
		CreatedAt: validFrom, UpdatedAt: validFrom,
	})
	batch.Set(client.Doc(tenantPrefix+"/trips/"+emulatorTripID), firestoreTrip{
		TenantID: emulatorTenantID, TripID: emulatorTripID, DeviceID: emulatorDeviceID,
		PersonID: emulatorPersonID, DeviceAssignmentID: emulatorAssignmentID,
		InstallationID: emulatorInstallationID, ClientSessionID: emulatorClientSessionID,
		ConsentRevisionID: emulatorConsentID, StartedAt: now.Add(-time.Hour),
		IngestExpiresAt: now.Add(time.Hour), CaptureMode: "background", Status: "recording",
	})
	batch.Set(client.Doc(tenantPrefix+"/deviceAssignments/"+emulatorAssignmentID), firestoreDeviceAssignment{
		TenantID: emulatorTenantID, AssignmentID: emulatorAssignmentID,
		DeviceID: emulatorDeviceID, PersonID: emulatorPersonID,
		AssignmentType: "primary_user", Status: "active", ValidFrom: validFrom,
	})
	batch.Set(client.Doc(tenantPrefix+"/consentRevisions/"+emulatorConsentID), firestoreConsentRevision{
		TenantID: emulatorTenantID, ConsentRevisionID: emulatorConsentID,
		PersonID: emulatorPersonID, PurposeCode: authorization.PreciseLocationPurpose,
		Status: "granted", GrantedAt: &validFrom,
	})
	batch.Set(client.Doc(tenantPrefix+"/consentStates/"+consentStateID), firestoreConsentState{
		TenantID: emulatorTenantID, PersonID: emulatorPersonID,
		PurposeCode:       authorization.PreciseLocationPurpose,
		CurrentRevisionID: emulatorConsentID, Status: "granted", EffectiveAt: validFrom,
	})
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed authorization documents: %v", err)
	}
}

func emulatorAdmissionIdentity(now time.Time) (ingest.Principal, ingest.BatchScope) {
	return ingest.Principal{
			FirebaseUID: emulatorFirebaseUID,
			AppID:       emulatorAppID,
		}, ingest.BatchScope{
			TenantID:          emulatorTenantID,
			DeviceID:          emulatorDeviceID,
			TripID:            emulatorTripID,
			ClientSessionID:   emulatorClientSessionID,
			InstallationID:    emulatorInstallationID,
			ConsentRevisionID: emulatorConsentID,
			FirstCapturedAt:   now.Add(-5 * time.Minute),
			LastCapturedAt:    now.Add(-time.Minute),
		}
}

func emulatorReservation(now time.Time, receiptID string) ingest.Reservation {
	return ingest.Reservation{
		ReservationKey: ingest.DeriveReservationKey(
			telemetry.SchemaVersionV2,
			emulatorTenantID,
			emulatorInstallationID,
			emulatorClientBatchID,
		),
		ClientBatchKey: ingest.DeriveClientBatchKey(emulatorTenantID, emulatorClientBatchID),
		ReceiptID:      receiptID, TenantID: emulatorTenantID, BatchID: receiptID,
		DeviceID: emulatorDeviceID, TripID: emulatorTripID, InstallationID: emulatorInstallationID,
		ConsentRevisionID: emulatorConsentID, ClientBatchID: emulatorClientBatchID,
		PayloadSchemaVersion: telemetry.SchemaVersionV2, BodyHash: emulatorBodyHash,
		CreatedAt: now, ExpiresAt: now.Add(ingest.ReceiptRetention),
	}
}

func assertAdmissionCollectionCount(
	t *testing.T,
	client *firestore.Client,
	collection string,
	want int,
) {
	t.Helper()
	documents, err := client.Collection(
		"tenants/" + emulatorTenantID + "/" + collection,
	).Documents(context.Background()).GetAll()
	if err != nil {
		t.Fatalf("list %s: %v", collection, err)
	}
	if len(documents) != want {
		t.Fatalf("%s document count = %d, want %d", collection, len(documents), want)
	}
}

func clearAdmissionIngestCollections(t *testing.T, client *firestore.Client) {
	t.Helper()
	for _, collection := range []string{
		"ingestIdempotency",
		"ingestClientBatches",
		"ingestReceipts",
	} {
		documents, err := client.Collection(
			"tenants/" + emulatorTenantID + "/" + collection,
		).Documents(context.Background()).GetAll()
		if err != nil {
			t.Fatalf("list %s for cleanup: %v", collection, err)
		}
		batch := client.Batch()
		for _, document := range documents {
			batch.Delete(document.Ref)
		}
		if len(documents) > 0 {
			if _, err := batch.Commit(context.Background()); err != nil {
				t.Fatalf("clear %s: %v", collection, err)
			}
		}
	}
}
