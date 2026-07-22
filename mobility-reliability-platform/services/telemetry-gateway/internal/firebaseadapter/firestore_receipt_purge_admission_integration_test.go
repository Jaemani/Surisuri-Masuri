package firebaseadapter

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorReceiptPurgeConcurrentAdmission(t *testing.T) {
	fixture := seedExpiredReceiptPurgeEmulatorFixture(t)
	beforeReservation := readReceiptPurgeDocument(t, fixture.client, fixture.idempotencyPath)
	beforeClientBatch := readReceiptPurgeDocument(t, fixture.client, fixture.clientBatchPath)

	type admissionCall struct {
		result ingest.ReceiptPurgeAdmissionResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan admissionCall, 2)
	for range 2 {
		go func() {
			<-start
			result, err := fixture.store.AdmitReceiptPurge(context.Background(), fixture.command)
			results <- admissionCall{result: result, err: err}
		}()
	}
	close(start)
	created := 0
	replayed := 0
	var result ingest.ReceiptPurgeAdmissionResult
	for range 2 {
		call := <-results
		if call.err != nil {
			t.Fatalf("concurrent AdmitReceiptPurge() = %#v, %v", call.result, call.err)
		}
		result = call.result
		switch call.result.Status {
		case ingest.ReceiptPurgeAdmissionCreated:
			created++
		case ingest.ReceiptPurgeAdmissionReplayed:
			replayed++
		default:
			t.Fatalf("concurrent admission status = %q", call.result.Status)
		}
	}
	if created != 1 || replayed != 1 {
		t.Fatalf("created/replayed = %d/%d, want 1/1", created, replayed)
	}

	jobPath := receiptPurgeJobDocumentPath(fixture.command.PurgeKey)
	jobDocument := readReceiptPurgeDocument(t, fixture.client, jobPath)
	receiptDocument := readReceiptPurgeDocument(t, fixture.client, fixture.receiptPath)
	var job firestoreReceiptPurgeJob
	var receipt firestoreIngestReceipt
	if jobDocument.snapshot.DataTo(&job) != nil || receiptDocument.snapshot.DataTo(&receipt) != nil {
		t.Fatal("decode committed purge job or receipt")
	}
	if ingest.ValidateReceiptPurgeJob(job.toDomain()) != nil ||
		receipt.PurgeJobID != job.PurgeKey || receipt.Revision != job.ReceiptRevision ||
		receipt.PurgeFenceVersion != ingest.ReceiptPurgeFenceVersion ||
		!receipt.PurgeStartedAt.Equal(job.CreatedAt) || !receipt.UpdatedAt.Equal(job.CreatedAt) {
		t.Fatalf("committed purge lineage = job %#v receipt %#v", job, receipt)
	}
	if !jobDocument.updateTime.Equal(receiptDocument.updateTime) {
		t.Fatalf("job/receipt commit lineage update times = %v/%v", jobDocument.updateTime, receiptDocument.updateTime)
	}
	afterReservation := readReceiptPurgeDocument(t, fixture.client, fixture.idempotencyPath)
	afterClientBatch := readReceiptPurgeDocument(t, fixture.client, fixture.clientBatchPath)
	if !afterReservation.updateTime.Equal(beforeReservation.updateTime) ||
		!afterClientBatch.updateTime.Equal(beforeClientBatch.updateTime) {
		t.Fatalf("purge admission rewrote uniqueness indexes")
	}

	outcome, err := fixture.store.GetReceiptPurgeAdmissionOutcome(
		context.Background(), result.OutcomeQuery,
	)
	if err != nil || outcome.CommitStatus != ingest.ReceiptPurgeAdmissionCommitted ||
		outcome.ReceiptRevision != job.ReceiptRevision || outcome.JobRevision != job.Revision {
		t.Fatalf("GetReceiptPurgeAdmissionOutcome() = %#v, %v", outcome, err)
	}
}

func TestFirestoreAdmissionStoreEmulatorReceiptPurgePartialFenceWritesNothing(t *testing.T) {
	fixture := seedExpiredReceiptPurgeEmulatorFixture(t)
	if _, err := fixture.client.Doc(fixture.receiptPath).Update(context.Background(), []firestore.Update{
		{Path: "purge_job_id", Value: fixture.command.PurgeKey},
	}); err != nil {
		t.Fatalf("seed partial purge fence: %v", err)
	}
	before := readReceiptPurgeDocument(t, fixture.client, fixture.receiptPath)

	result, err := fixture.store.AdmitReceiptPurge(context.Background(), fixture.command)
	if err == nil || result != (ingest.ReceiptPurgeAdmissionResult{}) {
		t.Fatalf("AdmitReceiptPurge(partial fence) = %#v, %v", result, err)
	}
	after := readReceiptPurgeDocument(t, fixture.client, fixture.receiptPath)
	if !after.updateTime.Equal(before.updateTime) {
		t.Fatalf("partial fence receipt was mutated: %v -> %v", before.updateTime, after.updateTime)
	}
	_, err = fixture.client.Doc(receiptPurgeJobDocumentPath(fixture.command.PurgeKey)).Get(context.Background())
	if status.Code(err) != codes.NotFound {
		t.Fatalf("partial fence created purge job: %v", err)
	}
}

type expiredReceiptPurgeEmulatorFixture struct {
	client          *firestore.Client
	store           *FirestoreAdmissionStore
	command         ingest.ReceiptPurgeAdmissionCommand
	idempotencyPath string
	clientBatchPath string
	receiptPath     string
}

func seedExpiredReceiptPurgeEmulatorFixture(t *testing.T) *expiredReceiptPurgeEmulatorFixture {
	t.Helper()
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	t.Cleanup(func() {
		clearAdmissionIngestCollections(t, client)
	})
	createdAt := time.Now().UTC().Add(-45 * 24 * time.Hour).Truncate(time.Millisecond)
	reservation := emulatorReservation(createdAt, emulatorFirstReceiptID)
	receipt := admissionTestReceiptDTO(admissionTestReceipt(reservation, ingest.ReceiptReserved))
	receipt.State = ingest.ReceiptExpired
	receipt.clearLease()
	receipt.CleanupTransitionedAt = reservation.ReservationDeadline
	receipt.CleanupQuiescenceUntil = receipt.CleanupTransitionedAt.Add(ingest.DefaultCleanupLateWriteGrace)
	receipt.CleanupMode = ingest.CleanupModeReservationExpiry
	receipt.CleanupOriginStatus = ingest.ReceiptReserved
	receipt.CleanupPolicyVersion = ingest.CleanupTransitionPolicyV1
	receipt.FencingToken = 2
	receipt.Revision = 3
	receipt.UpdatedAt = receipt.CleanupQuiescenceUntil.Add(time.Minute)
	purgeEligibleAt, err := ingest.CleanupPurgeEligibleAt(
		receipt.ReceiptRetentionFloor, receipt.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("CleanupPurgeEligibleAt() = %v", err)
	}
	receipt.PurgeEligibleAt = cloneOptionalTime(&purgeEligibleAt)
	index := newFirestoreIngestIndex(reservation)
	index.PurgeEligibleAt = cloneOptionalTime(&purgeEligibleAt)
	if validateIndexDocument(index, reservation.TenantID) != nil ||
		validateReceiptLinkage(receipt, index) != nil || validateReceiptState(receipt) != nil {
		t.Fatal("invalid synthetic expired receipt purge fixture")
	}
	idempotencyPath := idempotencyDocumentPath(reservation.TenantID, reservation.ReservationKey)
	clientBatchPath := clientBatchDocumentPath(reservation.TenantID, reservation.ClientBatchKey)
	receiptPath := receiptDocumentPath(reservation.TenantID, reservation.ReceiptID)
	batch := client.Batch()
	batch.Set(client.Doc(idempotencyPath), index)
	batch.Set(client.Doc(clientBatchPath), index)
	batch.Set(client.Doc(receiptPath), receipt)
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed expired receipt purge linkage: %v", err)
	}
	checkedAt := time.Now().UTC()
	command, err := ingest.BuildReceiptPurgeAdmissionCommand(
		receiptPurgeAdmissionState(receipt, index), checkedAt,
	)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeAdmissionCommand() = %v", err)
	}
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return checkedAt })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() = %v", err)
	}
	return &expiredReceiptPurgeEmulatorFixture{
		client: client, store: store, command: command,
		idempotencyPath: idempotencyPath, clientBatchPath: clientBatchPath,
		receiptPath: receiptPath,
	}
}

type receiptPurgeEmulatorDocument struct {
	snapshot   *firestore.DocumentSnapshot
	updateTime time.Time
}

func readReceiptPurgeDocument(
	t *testing.T,
	client *firestore.Client,
	path string,
) receiptPurgeEmulatorDocument {
	t.Helper()
	document, err := client.Doc(path).Get(context.Background())
	if err != nil {
		t.Fatalf("read receipt purge document %q: %v", path, err)
	}
	return receiptPurgeEmulatorDocument{snapshot: document, updateTime: document.UpdateTime.UTC()}
}
