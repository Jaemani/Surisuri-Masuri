package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorForwardRecoveryCurrentConsentGate(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedAdmissionAuthorization(t, client, now)
	persisted := seedExpiredAdmissionReservation(t, client, now)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, time.Now)
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}

	lease, status, err := store.ClaimRecoveryLease(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		ingest.LeaseOwner{ID: emulatorSecondReceiptID, Kind: ingest.LeaseOwnerSweeper},
		ingest.RecoveryAttemptProposal{
			ID:            emulatorSecondReceiptID,
			WorkerVersion: ingest.RecoveryWorkerVersion,
		},
		time.Now().UTC(),
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired {
		t.Fatalf("ClaimRecoveryLease() = %#v, %q, %v", lease, status, err)
	}
	authorizer, err := ingest.NewSystemRecoveryAuthorizer(store, time.Now)
	if err != nil {
		t.Fatalf("NewSystemRecoveryAuthorizer() error = %v", err)
	}

	request, grant, err := authorizer.Authorize(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		lease,
	)
	if err != nil {
		t.Fatalf("Authorize() before withdrawal error = %v", err)
	}
	if request.ReceiptID != persisted.ReceiptID || request.ReceiptRevision != persisted.Revision+1 ||
		request.ForwardFence == nil || request.ForwardFence.OwnerID != lease.Fence.OwnerID ||
		request.ForwardFence.Token != lease.Fence.Token ||
		!request.ForwardFence.ExpiresAt.Equal(lease.Fence.ExpiresAt) {
		t.Fatalf("authoritative request = %#v, lease = %#v", request, lease)
	}
	if err := ingest.ValidateArtifactReadAuthorization(grant, request, time.Now().UTC()); err != nil {
		t.Fatalf("ValidateArtifactReadAuthorization() = %v", err)
	}

	consentStateID := authorization.ConsentStateDocumentID(
		emulatorPersonID,
		authorization.PreciseLocationPurpose,
	)
	if _, err := client.Doc(
		"tenants/"+emulatorTenantID+"/consentStates/"+consentStateID,
	).Update(context.Background(), []firestore.Update{
		{Path: "status", Value: "withdrawn"},
	}); err != nil {
		t.Fatalf("withdraw current consent: %v", err)
	}

	_, _, err = authorizer.Authorize(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		lease,
	)
	if !errors.Is(err, ingest.ErrForwardRecoveryUnauthorized) {
		t.Fatalf("Authorize() after withdrawal error = %v, want denial", err)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.State != ingest.ReceiptReserved || stored.FencingToken != lease.Fence.Token ||
		stored.Revision != persisted.Revision+1 || stored.LeaseOwnerID != lease.Fence.OwnerID {
		t.Fatalf("authorization mutated receipt = %#v", stored)
	}
	assertAdmissionCollectionCount(t, client, "ingestIdempotency", 1)
	assertAdmissionCollectionCount(t, client, "ingestClientBatches", 1)
	assertAdmissionCollectionCount(t, client, "ingestReceipts", 1)
}
