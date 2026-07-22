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
	emulatorThirdReceiptID    = "01982015-4400-7000-8000-00000000010c"
	emulatorFirebaseUID       = "firestore-emulator-user"
	emulatorAppID             = "1:1234567890:android:emulator-app"
	emulatorBodyHash          = "16a42ebf7f1004546f39b50e452c7c5777c075bed43d90df49ab0cfba4e3748f"
	emulatorTransactionTimout = 10 * time.Second
)

func TestFirestoreAdmissionStoreEmulatorConcurrentSameBatch(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedAdmissionAuthorization(t, client, now)
	barrier := newMissingIndexBarrier(clientBatchDocumentPath(emulatorTenantID, ingest.DeriveClientBatchKey(
		emulatorTenantID,
		emulatorClientBatchID,
	)))
	store, err := newContendedAdmissionEmulatorStore(client, barrier)
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
		lease   ingest.LeaseGrant
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
			outcomes[index].receipt, outcomes[index].lease, outcomes[index].status, outcomes[index].err =
				store.AuthorizeAndReserve(
					context.Background(),
					principal,
					scope,
					reservations[index],
					emulatorLeaseProposal(reservations[index].ReceiptID),
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
	if statusCounts[ingest.ReservationCreatedLeaseAcquired] != 1 || statusCounts[ingest.ReservationReplayInProgress] != 1 {
		t.Fatalf("concurrent statuses = %#v, want one acquired and one in progress", statusCounts)
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
	now := time.Now().UTC().Truncate(time.Millisecond)
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

	_, _, _, err = store.AuthorizeAndReserve(
		context.Background(),
		principal,
		scope,
		emulatorReservation(now, emulatorFirstReceiptID),
		emulatorLeaseProposal(emulatorFirstReceiptID),
	)
	if !errors.Is(err, ingest.ErrBatchUnauthorized) {
		t.Fatalf("AuthorizeAndReserve() error = %v, want unauthorized", err)
	}
	assertAdmissionCollectionCount(t, client, "ingestIdempotency", 0)
	assertAdmissionCollectionCount(t, client, "ingestClientBatches", 0)
	assertAdmissionCollectionCount(t, client, "ingestReceipts", 0)
}

func TestFirestoreAdmissionStoreEmulatorConcurrentExpiredTakeover(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedAdmissionAuthorization(t, client, now)
	persisted := seedExpiredAdmissionReservation(t, client, now)
	principal, _ := emulatorAdmissionIdentity(now)
	scope, reservation := emulatorReplayRequest(persisted, now, emulatorSecondReceiptID)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}

	type outcome struct {
		receipt ingest.Receipt
		lease   ingest.LeaseGrant
		status  ingest.ReservationStatus
		err     error
	}
	ownerIDs := []string{emulatorSecondReceiptID, emulatorThirdReceiptID}
	outcomes := make([]outcome, len(ownerIDs))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index, ownerID := range ownerIDs {
		wait.Add(1)
		go func(index int, ownerID string) {
			defer wait.Done()
			<-start
			outcomes[index].receipt, outcomes[index].lease, outcomes[index].status, outcomes[index].err =
				store.AuthorizeAndReserve(
					context.Background(),
					principal,
					scope,
					reservation,
					emulatorLeaseProposal(ownerID),
				)
		}(index, ownerID)
	}
	close(start)
	wait.Wait()

	statusCounts := map[ingest.ReservationStatus]int{}
	var winner outcome
	for index, result := range outcomes {
		if result.err != nil {
			t.Fatalf("concurrent takeover %d error = %v", index, result.err)
		}
		statusCounts[result.status]++
		if result.status == ingest.ReservationReplayLeaseAcquired {
			winner = result
		}
	}
	if statusCounts[ingest.ReservationReplayLeaseAcquired] != 1 ||
		statusCounts[ingest.ReservationReplayInProgress] != 1 {
		t.Fatalf("takeover statuses = %#v, want one acquired and one in progress", statusCounts)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.State != ingest.ReceiptReserved || stored.FencingToken != 2 || stored.Revision != 2 ||
		stored.LeaseOwnerID != winner.lease.Fence.OwnerID || stored.RecoveryAttemptCount != 1 {
		t.Fatalf("stored takeover receipt = %#v, winner = %#v", stored, winner)
	}
	attempt := readAdmissionEmulatorAttempt(t, client, stored.TenantID, stored.ReceiptID, winner.lease.Fence.OwnerID)
	if attempt.Status != "started" || attempt.FencingToken != 2 ||
		attempt.OwnerKind != ingest.LeaseOwnerRequest || attempt.WorkerVersion != ingest.RecoveryWorkerVersion {
		t.Fatalf("stored takeover attempt = %#v", attempt)
	}
}

func TestFirestoreAdmissionStoreEmulatorExpiredOwnerCannotRaceTakeoverFinalizer(t *testing.T) {
	tests := []struct {
		name     string
		finalize func(*FirestoreAdmissionStore, firestoreIngestReceipt, ingest.LeaseFence, time.Time) error
	}{
		{
			name: "mark stored",
			finalize: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt, fence ingest.LeaseFence, now time.Time) error {
				_, err := store.MarkStored(
					context.Background(),
					receipt.TenantID,
					receipt.ReservationKey,
					fence,
					emulatorStoredReceiptData(receipt),
					now,
				)
				return err
			},
		},
		{
			name: "mark rejected",
			finalize: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt, fence ingest.LeaseFence, now time.Time) error {
				_, err := store.MarkRejected(
					context.Background(),
					receipt.TenantID,
					receipt.ReservationKey,
					fence,
					"object_conflict",
					now,
				)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newAdmissionEmulatorClient(t)
			clearAdmissionIngestCollections(t, client)
			now := time.Now().UTC().Truncate(time.Millisecond)
			seedAdmissionAuthorization(t, client, now)
			persisted := seedExpiredAdmissionReservation(t, client, now)
			oldFence := persisted.leaseGrant().Fence
			principal, _ := emulatorAdmissionIdentity(now)
			scope, reservation := emulatorReplayRequest(persisted, now, emulatorSecondReceiptID)
			store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
			if err != nil {
				t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
			}

			start := make(chan struct{})
			var wait sync.WaitGroup
			var takeoverStatus ingest.ReservationStatus
			var takeoverErr error
			var finalizerErr error
			wait.Add(2)
			go func() {
				defer wait.Done()
				<-start
				_, _, takeoverStatus, takeoverErr = store.AuthorizeAndReserve(
					context.Background(),
					principal,
					scope,
					reservation,
					emulatorLeaseProposal(emulatorSecondReceiptID),
				)
			}()
			go func() {
				defer wait.Done()
				<-start
				finalizerErr = test.finalize(store, persisted, oldFence, now)
			}()
			close(start)
			wait.Wait()

			if takeoverErr != nil || takeoverStatus != ingest.ReservationReplayLeaseAcquired {
				t.Fatalf("takeover result = %q, %v", takeoverStatus, takeoverErr)
			}
			if !errors.Is(finalizerErr, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("expired finalizer error = %v, want unavailable", finalizerErr)
			}
			stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
			if stored.State != ingest.ReceiptReserved || stored.FencingToken != 2 || stored.Revision != 2 ||
				stored.RecoveryAttemptCount != 1 ||
				stored.LeaseOwnerID != emulatorSecondReceiptID || hasStoredArtifactData(stored) || stored.RejectionCode != "" {
				t.Fatalf("receipt after takeover/finalizer race = %#v", stored)
			}
		})
	}
}

func TestFirestoreAdmissionStoreEmulatorConcurrentSweeperClaim(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedAdmissionAuthorization(t, client, now)
	persisted := seedExpiredAdmissionReservation(t, client, now)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}

	type outcome struct {
		grant  ingest.LeaseGrant
		status ingest.LeaseStatus
		err    error
	}
	ownerIDs := []string{emulatorSecondReceiptID, emulatorThirdReceiptID}
	outcomes := make([]outcome, len(ownerIDs))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index, ownerID := range ownerIDs {
		wait.Add(1)
		go func(index int, ownerID string) {
			defer wait.Done()
			<-start
			outcomes[index].grant, outcomes[index].status, outcomes[index].err = store.ClaimRecoveryLease(
				context.Background(),
				persisted.TenantID,
				persisted.ReservationKey,
				ingest.LeaseOwner{ID: ownerID, Kind: ingest.LeaseOwnerSweeper},
				ingest.RecoveryAttemptProposal{ID: ownerID, WorkerVersion: ingest.RecoveryWorkerVersion},
				now,
				ingest.DefaultRequestLeaseDuration,
			)
		}(index, ownerID)
	}
	close(start)
	wait.Wait()

	statusCounts := map[ingest.LeaseStatus]int{}
	var winner outcome
	for index, result := range outcomes {
		if result.err != nil {
			t.Fatalf("concurrent sweeper claim %d error = %v", index, result.err)
		}
		statusCounts[result.status]++
		if result.status == ingest.LeaseStatusAcquired {
			winner = result
		}
	}
	if statusCounts[ingest.LeaseStatusAcquired] != 1 || statusCounts[ingest.LeaseStatusHeld] != 1 {
		t.Fatalf("sweeper claim statuses = %#v, want one acquired and one held", statusCounts)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.FencingToken != 2 || stored.Revision != 2 || stored.RecoveryAttemptCount != 1 ||
		stored.LeaseOwnerKind != ingest.LeaseOwnerSweeper || stored.LeaseOwnerID != winner.grant.Fence.OwnerID {
		t.Fatalf("stored sweeper claim = %#v, winner = %#v", stored, winner)
	}
	attempt := readAdmissionEmulatorAttempt(t, client, stored.TenantID, stored.ReceiptID, winner.grant.Fence.OwnerID)
	if attempt.FencingToken != 2 || attempt.OwnerKind != ingest.LeaseOwnerSweeper || attempt.Status != "started" {
		t.Fatalf("stored sweeper attempt = %#v", attempt)
	}
}

func TestFirestoreAdmissionStoreEmulatorDuplicateAttemptRollsBackSweeperClaim(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedAdmissionAuthorization(t, client, now)
	persisted := seedExpiredAdmissionReservation(t, client, now)
	attemptProposal := ingest.RecoveryAttemptProposal{
		ID:            emulatorSecondReceiptID,
		WorkerVersion: ingest.RecoveryWorkerVersion,
	}
	existingAttempt := newFirestoreRecoveryAttempt(
		attemptProposal,
		persisted.TenantID,
		persisted.ReceiptID,
		ingest.LeaseOwnerSweeper,
		persisted.FencingToken+1,
		now.Add(-time.Second),
	)
	if _, err := client.Doc(recoveryAttemptDocumentPath(
		persisted.TenantID,
		persisted.ReceiptID,
		attemptProposal.ID,
	)).Create(context.Background(), existingAttempt); err != nil {
		t.Fatalf("seed duplicate recovery attempt: %v", err)
	}
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}

	grant, status, err := store.ClaimRecoveryLease(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		ingest.LeaseOwner{ID: emulatorSecondReceiptID, Kind: ingest.LeaseOwnerSweeper},
		attemptProposal,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("ClaimRecoveryLease() error = %v, want admission unavailable", err)
	}
	if status != "" || grant != (ingest.LeaseGrant{}) {
		t.Fatalf("ClaimRecoveryLease() result = %#v, %q, want zero grant/status", grant, status)
	}

	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.State != persisted.State || stored.FencingToken != persisted.FencingToken ||
		stored.Revision != persisted.Revision || stored.RecoveryAttemptCount != persisted.RecoveryAttemptCount ||
		stored.LeaseOwnerID != persisted.LeaseOwnerID || stored.LeaseOwnerKind != persisted.LeaseOwnerKind ||
		!stored.LeaseAcquiredAt.Equal(persisted.LeaseAcquiredAt) ||
		!stored.LeaseHeartbeatAt.Equal(persisted.LeaseHeartbeatAt) ||
		!stored.LeaseExpiresAt.Equal(persisted.LeaseExpiresAt) ||
		!stored.NextRecoveryAt.Equal(persisted.NextRecoveryAt) ||
		stored.LastRecoveryCode != persisted.LastRecoveryCode || !stored.UpdatedAt.Equal(persisted.UpdatedAt) {
		t.Fatalf("receipt changed after duplicate attempt rollback: before=%#v after=%#v", persisted, stored)
	}
	storedAttempt := readAdmissionEmulatorAttempt(
		t,
		client,
		persisted.TenantID,
		persisted.ReceiptID,
		attemptProposal.ID,
	)
	if storedAttempt != existingAttempt {
		t.Fatalf("duplicate attempt was changed: before=%#v after=%#v", existingAttempt, storedAttempt)
	}
	attempts, err := client.Collection(
		receiptDocumentPath(persisted.TenantID, persisted.ReceiptID) + "/recoveryAttempts",
	).Documents(context.Background()).GetAll()
	if err != nil {
		t.Fatalf("list recovery attempts after rollback: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("recovery attempt count after rollback = %d, want 1", len(attempts))
	}
}

func TestFirestoreAdmissionStoreEmulatorRenewVersusTakeover(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	serverNow := time.Now().UTC().Truncate(time.Millisecond)
	seedAdmissionAuthorization(t, client, serverNow)
	createdAt := serverNow.Add(-ingest.DefaultRequestLeaseDuration + 2*time.Second)
	persisted := seedAdmissionReservation(t, client, createdAt)
	oldFence := persisted.leaseGrant().Fence
	renewedAt := persisted.LeaseExpiresAt.Add(-time.Millisecond)
	takeoverAt := persisted.LeaseExpiresAt.Add(time.Millisecond)
	barrier := newReceiptReadBarrier(receiptDocumentPath(persisted.TenantID, persisted.ReceiptID))
	renewStore, err := newReceiptContendedAdmissionEmulatorStore(client, renewedAt, barrier)
	if err != nil {
		t.Fatalf("renew store error = %v", err)
	}
	claimStore, err := newReceiptContendedAdmissionEmulatorStore(client, takeoverAt, barrier)
	if err != nil {
		t.Fatalf("claim store error = %v", err)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	var renewGrant ingest.LeaseGrant
	var renewErr error
	var claimGrant ingest.LeaseGrant
	var claimStatus ingest.LeaseStatus
	var claimErr error
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		renewGrant, renewErr = renewStore.RenewLease(
			context.Background(),
			persisted.TenantID,
			persisted.ReservationKey,
			oldFence,
			renewedAt,
			ingest.DefaultRequestLeaseDuration,
		)
	}()
	go func() {
		defer wait.Done()
		<-start
		claimGrant, claimStatus, claimErr = claimStore.ClaimRecoveryLease(
			context.Background(),
			persisted.TenantID,
			persisted.ReservationKey,
			ingest.LeaseOwner{ID: emulatorSecondReceiptID, Kind: ingest.LeaseOwnerSweeper},
			ingest.RecoveryAttemptProposal{ID: emulatorSecondReceiptID, WorkerVersion: ingest.RecoveryWorkerVersion},
			takeoverAt,
			ingest.DefaultRequestLeaseDuration,
		)
	}()
	close(start)
	wait.Wait()

	if claimErr != nil {
		t.Fatalf("ClaimRecoveryLease() error = %v", claimErr)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	switch claimStatus {
	case ingest.LeaseStatusHeld:
		if renewErr != nil || renewGrant.Fence.Token != 1 || stored.FencingToken != 1 ||
			stored.LeaseOwnerID != persisted.LeaseOwnerID || stored.RecoveryAttemptCount != 0 ||
			!stored.LeaseExpiresAt.Equal(renewGrant.Fence.ExpiresAt) {
			t.Fatalf("renew winner = renew:%#v/%v claim:%#v/%q receipt:%#v", renewGrant, renewErr, claimGrant, claimStatus, stored)
		}
	case ingest.LeaseStatusAcquired:
		if !errors.Is(renewErr, ingest.ErrAdmissionUnavailable) || claimGrant.Fence.Token != 2 ||
			stored.FencingToken != 2 || stored.LeaseOwnerID != emulatorSecondReceiptID ||
			stored.RecoveryAttemptCount != 1 {
			t.Fatalf("takeover winner = renew:%#v/%v claim:%#v/%q receipt:%#v", renewGrant, renewErr, claimGrant, claimStatus, stored)
		}
	default:
		t.Fatalf("renew/takeover claim status = %q", claimStatus)
	}
	if stored.Revision != 2 {
		t.Fatalf("renew/takeover revision = %d, want one committed mutation", stored.Revision)
	}
}

func TestFirestoreAdmissionStoreEmulatorCleanupTransitionWinsDeadlineRaces(t *testing.T) {
	tests := []struct {
		name    string
		compete func(*FirestoreAdmissionStore, firestoreIngestReceipt, time.Time) (ingest.LeaseStatus, error)
	}{
		{
			name: "recovery claim",
			compete: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt, now time.Time) (ingest.LeaseStatus, error) {
				_, status, err := store.ClaimRecoveryLease(
					context.Background(),
					receipt.TenantID,
					receipt.ReservationKey,
					ingest.LeaseOwner{ID: emulatorSecondReceiptID, Kind: ingest.LeaseOwnerSweeper},
					ingest.RecoveryAttemptProposal{ID: emulatorSecondReceiptID, WorkerVersion: ingest.RecoveryWorkerVersion},
					now,
					ingest.DefaultRequestLeaseDuration,
				)
				return status, err
			},
		},
		{
			name: "stale stored finalizer",
			compete: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt, now time.Time) (ingest.LeaseStatus, error) {
				_, err := store.MarkStored(
					context.Background(),
					receipt.TenantID,
					receipt.ReservationKey,
					receipt.leaseGrant().Fence,
					emulatorStoredReceiptData(receipt),
					now,
				)
				return "", err
			},
		},
		{
			name: "stale rejected finalizer",
			compete: func(store *FirestoreAdmissionStore, receipt firestoreIngestReceipt, now time.Time) (ingest.LeaseStatus, error) {
				_, err := store.MarkRejected(
					context.Background(),
					receipt.TenantID,
					receipt.ReservationKey,
					receipt.leaseGrant().Fence,
					"object_conflict",
					now,
				)
				return "", err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newAdmissionEmulatorClient(t)
			clearAdmissionIngestCollections(t, client)
			now := time.Now().UTC().Truncate(time.Millisecond)
			seedAdmissionAuthorization(t, client, now)
			persisted := seedAdmissionReservation(t, client, now.Add(-ingest.ReservationProcessingWindow))
			store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
			if err != nil {
				t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
			}

			start := make(chan struct{})
			var wait sync.WaitGroup
			var transitionStatus ingest.TransitionStatus
			var transitionErr error
			var competingStatus ingest.LeaseStatus
			var competingErr error
			wait.Add(2)
			go func() {
				defer wait.Done()
				<-start
				_, transitionStatus, transitionErr = store.BeginCleanupTransition(
					context.Background(),
					persisted.TenantID,
					persisted.ReservationKey,
					now,
				)
			}()
			go func() {
				defer wait.Done()
				<-start
				competingStatus, competingErr = test.compete(store, persisted, now)
			}()
			close(start)
			wait.Wait()

			if transitionErr != nil || transitionStatus != ingest.TransitionStatusStarted {
				t.Fatalf("cleanup transition = %q, %v", transitionStatus, transitionErr)
			}
			if test.name == "recovery claim" {
				if competingErr != nil ||
					(competingStatus != ingest.LeaseStatusDeadlineElapsed && competingStatus != ingest.LeaseStatusNotEligible) {
					t.Fatalf("deadline recovery claim = %q, %v", competingStatus, competingErr)
				}
			} else if !errors.Is(competingErr, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("stale finalizer error = %v, want unavailable", competingErr)
			}
			stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
			if stored.State != ingest.ReceiptCleanupPending || stored.FencingToken != 2 || stored.Revision != 2 ||
				stored.RecoveryAttemptCount != 0 || receiptHasLeaseFields(stored) ||
				stored.CleanupMode != ingest.CleanupModeReservationExpiry ||
				stored.CleanupOriginStatus != ingest.ReceiptReserved ||
				stored.CleanupPolicyVersion != ingest.CleanupTransitionPolicyV1 ||
				!stored.CleanupTransitionedAt.Equal(now) ||
				!stored.CleanupQuiescenceUntil.Equal(now.Add(ingest.DefaultCleanupLateWriteGrace)) ||
				hasStoredArtifactData(stored) || stored.RejectionCode != "" {
				t.Fatalf("receipt after deadline race = %#v", stored)
			}
		})
	}
}

func TestFirestoreAdmissionStoreEmulatorCleanupTransitionClosesExpiredStartedAttempt(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	persisted, priorAttempt := seedAdmissionReservationWithExpiredRecoveryAttempt(t, client, now, true)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}

	result, status, err := store.BeginCleanupTransition(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		now,
	)
	if err != nil || status != ingest.TransitionStatusStarted {
		t.Fatalf("BeginCleanupTransition() = %#v, %q, %v", result, status, err)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.State != ingest.ReceiptCleanupPending || stored.FencingToken != persisted.FencingToken+1 ||
		stored.Revision != persisted.Revision+1 || stored.RecoveryAttemptCount != persisted.RecoveryAttemptCount ||
		receiptHasLeaseFields(stored) || stored.CleanupMode != ingest.CleanupModeReservationExpiry ||
		stored.CleanupOriginStatus != ingest.ReceiptReserved ||
		stored.CleanupPolicyVersion != ingest.CleanupTransitionPolicyV1 ||
		!stored.CleanupTransitionedAt.Equal(now) ||
		!stored.CleanupQuiescenceUntil.Equal(now.Add(ingest.DefaultCleanupLateWriteGrace)) {
		t.Fatalf("stored cleanup transition = %#v", stored)
	}
	storedAttempt := readAdmissionEmulatorAttempt(
		t,
		client,
		persisted.TenantID,
		persisted.ReceiptID,
		priorAttempt.AttemptID,
	)
	if storedAttempt.Status != ingest.RecoveryAttemptFailed ||
		storedAttempt.FailureCode != ingest.RecoveryAttemptFailureLeaseExpired ||
		!storedAttempt.FailedAt.Equal(now) || !storedAttempt.StartedAt.Equal(priorAttempt.StartedAt) ||
		storedAttempt.FencingToken != priorAttempt.FencingToken {
		t.Fatalf("stored expired attempt closure = %#v", storedAttempt)
	}
}

func TestFirestoreAdmissionStoreEmulatorCleanupTransitionRejectsMissingPriorAttemptAtomically(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	persisted, _ := seedAdmissionReservationWithExpiredRecoveryAttempt(t, client, now, false)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}

	result, status, err := store.BeginCleanupTransition(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		now,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) || status != "" || result != (ingest.Receipt{}) {
		t.Fatalf("BeginCleanupTransition() = %#v, %q, %v, want unavailable", result, status, err)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.State != persisted.State || stored.FencingToken != persisted.FencingToken ||
		stored.Revision != persisted.Revision || stored.RecoveryAttemptCount != persisted.RecoveryAttemptCount ||
		stored.LeaseOwnerID != persisted.LeaseOwnerID || stored.LeaseOwnerKind != persisted.LeaseOwnerKind ||
		!stored.LeaseAcquiredAt.Equal(persisted.LeaseAcquiredAt) ||
		!stored.LeaseHeartbeatAt.Equal(persisted.LeaseHeartbeatAt) ||
		!stored.LeaseExpiresAt.Equal(persisted.LeaseExpiresAt) ||
		!stored.NextRecoveryAt.Equal(persisted.NextRecoveryAt) || stored.CleanupMode != "" ||
		!stored.CleanupTransitionedAt.IsZero() || !stored.CleanupQuiescenceUntil.IsZero() ||
		stored.CleanupPolicyVersion != "" {
		t.Fatalf("receipt changed after missing attempt rollback: before=%#v after=%#v", persisted, stored)
	}
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

type receiptReadBarrier struct {
	path     string
	ready    chan struct{}
	mu       sync.Mutex
	arrivals int
}

func newReceiptReadBarrier(path string) *receiptReadBarrier {
	return &receiptReadBarrier{path: path, ready: make(chan struct{})}
}

func (barrier *receiptReadBarrier) waitForTwo() {
	barrier.mu.Lock()
	barrier.arrivals++
	if barrier.arrivals == 2 {
		close(barrier.ready)
	}
	barrier.mu.Unlock()
	<-barrier.ready
}

type receiptBarrierAdmissionTransaction struct {
	admissionTransaction
	barrier *receiptReadBarrier
}

func (transaction receiptBarrierAdmissionTransaction) ReadReceipt(
	ctx context.Context,
	path string,
) (receiptRead, bool, error) {
	receipt, exists, err := transaction.admissionTransaction.ReadReceipt(ctx, path)
	if err == nil && exists && path == transaction.barrier.path {
		transaction.barrier.waitForTwo()
	}
	return receipt, exists, err
}

func (transaction barrierAdmissionTransaction) LoadAuthorization(
	ctx context.Context,
	principal ingest.Principal,
	scope ingest.BatchScope,
) (authorizationRead, error) {
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
	barrier *missingIndexBarrier,
) (*FirestoreAdmissionStore, error) {
	store, err := NewFirestoreAdmissionStore(
		client,
		emulatorTransactionTimout,
		func() time.Time { return time.Now().UTC() },
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

func newReceiptContendedAdmissionEmulatorStore(
	client *firestore.Client,
	now time.Time,
	barrier *receiptReadBarrier,
) (*FirestoreAdmissionStore, error) {
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
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
				return operation(runContext, receiptBarrierAdmissionTransaction{
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
			TenantID:            emulatorTenantID,
			DeviceID:            emulatorDeviceID,
			TripID:              emulatorTripID,
			ClientSessionID:     emulatorClientSessionID,
			InstallationID:      emulatorInstallationID,
			ConsentRevisionID:   emulatorConsentID,
			ExpectedSampleCount: 42,
			FirstCapturedAt:     now.Add(-5 * time.Minute),
			LastCapturedAt:      now.Add(-time.Minute),
		}
}

func emulatorReservation(now time.Time, receiptID string) ingest.Reservation {
	_, scope := emulatorAdmissionIdentity(now)
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
		ExpectedSampleCount: 42,
		FirstCapturedAt:     scope.FirstCapturedAt, LastCapturedAt: scope.LastCapturedAt,
		ValidatorVersion:      ingest.TelemetryValidatorVersion,
		CreatedAt:             now,
		ReservationDeadline:   now.Add(ingest.ReservationProcessingWindow),
		ArtifactExpiresAt:     now.Add(ingest.TelemetryArtifactRetention),
		ReceiptRetentionFloor: now.Add(ingest.ReceiptControlRetention),
	}
}

func emulatorLeaseProposal(ownerID string) ingest.LeaseProposal {
	return ingest.LeaseProposal{
		Owner:    ingest.LeaseOwner{ID: ownerID, Kind: ingest.LeaseOwnerRequest},
		Duration: ingest.DefaultRequestLeaseDuration,
		Attempt: ingest.RecoveryAttemptProposal{
			ID:            ownerID,
			WorkerVersion: ingest.RecoveryWorkerVersion,
		},
	}
}

func seedExpiredAdmissionReservation(
	t *testing.T,
	client *firestore.Client,
	now time.Time,
) firestoreIngestReceipt {
	t.Helper()
	createdAt := now.Add(-3 * time.Minute)
	reservation := emulatorReservation(createdAt, emulatorFirstReceiptID)
	index := newFirestoreIngestIndex(reservation)
	receipt := newFirestoreIngestReceipt(
		reservation,
		ingest.LeaseOwner{ID: emulatorFirstReceiptID, Kind: ingest.LeaseOwnerRequest},
		createdAt,
		createdAt.Add(ingest.DefaultRequestLeaseDuration),
	)
	batch := client.Batch()
	batch.Set(client.Doc(idempotencyDocumentPath(reservation.TenantID, reservation.ReservationKey)), index)
	batch.Set(client.Doc(clientBatchDocumentPath(reservation.TenantID, reservation.ClientBatchKey)), index)
	batch.Set(client.Doc(receiptDocumentPath(reservation.TenantID, reservation.ReceiptID)), receipt)
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed expired admission reservation: %v", err)
	}
	return receipt
}

func seedAdmissionReservation(
	t *testing.T,
	client *firestore.Client,
	createdAt time.Time,
) firestoreIngestReceipt {
	t.Helper()
	reservation := emulatorReservation(createdAt, emulatorFirstReceiptID)
	index := newFirestoreIngestIndex(reservation)
	receipt := newFirestoreIngestReceipt(
		reservation,
		ingest.LeaseOwner{ID: emulatorFirstReceiptID, Kind: ingest.LeaseOwnerRequest},
		createdAt,
		createdAt.Add(ingest.DefaultRequestLeaseDuration),
	)
	batch := client.Batch()
	batch.Set(client.Doc(idempotencyDocumentPath(reservation.TenantID, reservation.ReservationKey)), index)
	batch.Set(client.Doc(clientBatchDocumentPath(reservation.TenantID, reservation.ClientBatchKey)), index)
	batch.Set(client.Doc(receiptDocumentPath(reservation.TenantID, reservation.ReceiptID)), receipt)
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed admission reservation: %v", err)
	}
	return receipt
}

func seedAdmissionReservationWithExpiredRecoveryAttempt(
	t *testing.T,
	client *firestore.Client,
	cleanupAt time.Time,
	includeAttempt bool,
) (firestoreIngestReceipt, firestoreRecoveryAttempt) {
	t.Helper()
	createdAt := cleanupAt.Add(-ingest.ReservationProcessingWindow)
	reservation := emulatorReservation(createdAt, emulatorFirstReceiptID)
	index := newFirestoreIngestIndex(reservation)
	leaseAcquiredAt := cleanupAt.Add(-ingest.DefaultRequestLeaseDuration)
	receipt := newFirestoreIngestReceipt(
		reservation,
		ingest.LeaseOwner{ID: emulatorSecondReceiptID, Kind: ingest.LeaseOwnerSweeper},
		leaseAcquiredAt,
		cleanupAt,
	)
	receipt.FencingToken = 2
	receipt.RecoveryAttemptCount = 1
	receipt.Revision = 2
	attempt := newFirestoreRecoveryAttempt(
		ingest.RecoveryAttemptProposal{
			ID: emulatorSecondReceiptID, WorkerVersion: ingest.RecoveryWorkerVersion,
		},
		receipt.TenantID,
		receipt.ReceiptID,
		receipt.LeaseOwnerKind,
		receipt.FencingToken,
		leaseAcquiredAt,
	)
	batch := client.Batch()
	batch.Set(client.Doc(idempotencyDocumentPath(reservation.TenantID, reservation.ReservationKey)), index)
	batch.Set(client.Doc(clientBatchDocumentPath(reservation.TenantID, reservation.ClientBatchKey)), index)
	batch.Set(client.Doc(receiptDocumentPath(reservation.TenantID, reservation.ReceiptID)), receipt)
	if includeAttempt {
		batch.Set(client.Doc(recoveryAttemptDocumentPath(
			reservation.TenantID,
			reservation.ReceiptID,
			attempt.AttemptID,
		)), attempt)
	}
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed expired recovery attempt reservation: %v", err)
	}
	return receipt, attempt
}

func emulatorReplayRequest(
	persisted firestoreIngestReceipt,
	requestAt time.Time,
	receiptID string,
) (ingest.BatchScope, ingest.Reservation) {
	_, scope := emulatorAdmissionIdentity(requestAt)
	scope.ExpectedSampleCount = persisted.ExpectedSampleCount
	scope.FirstCapturedAt = persisted.FirstCapturedAt
	scope.LastCapturedAt = persisted.LastCapturedAt
	reservation := emulatorReservation(requestAt, receiptID)
	reservation.ExpectedSampleCount = persisted.ExpectedSampleCount
	reservation.FirstCapturedAt = persisted.FirstCapturedAt
	reservation.LastCapturedAt = persisted.LastCapturedAt
	reservation.ValidatorVersion = persisted.ValidatorVersion
	return scope, reservation
}

func emulatorStoredReceiptData(receipt firestoreIngestReceipt) ingest.StoredReceiptData {
	return ingest.StoredReceiptData{
		Artifacts: ingest.StoredBatchArtifacts{
			Object: ingest.StoredArtifact{
				Path: expectedObjectPath(receipt), SHA256: emulatorBodyHash,
				CRC32C: 1, Size: 1024, Generation: 1, Metageneration: 1,
			},
			Manifest: ingest.StoredArtifact{
				Path: expectedManifestPath(receipt), SHA256: emulatorBodyHash,
				CRC32C: 2, Size: 512, Generation: 2, Metageneration: 1,
			},
		},
		SampleCount: receipt.ExpectedSampleCount,
	}
}

func readAdmissionEmulatorReceipt(
	t *testing.T,
	client *firestore.Client,
	tenantID string,
	receiptID string,
) firestoreIngestReceipt {
	t.Helper()
	document, err := client.Doc(receiptDocumentPath(tenantID, receiptID)).Get(context.Background())
	if err != nil {
		t.Fatalf("read emulator receipt: %v", err)
	}
	var receipt firestoreIngestReceipt
	if err := document.DataTo(&receipt); err != nil {
		t.Fatalf("decode emulator receipt: %v", err)
	}
	return receipt
}

func readAdmissionEmulatorAttempt(
	t *testing.T,
	client *firestore.Client,
	tenantID string,
	receiptID string,
	attemptID string,
) firestoreRecoveryAttempt {
	t.Helper()
	document, err := client.Doc(recoveryAttemptDocumentPath(tenantID, receiptID, attemptID)).Get(context.Background())
	if err != nil {
		t.Fatalf("read emulator recovery attempt: %v", err)
	}
	var attempt firestoreRecoveryAttempt
	if err := document.DataTo(&attempt); err != nil {
		t.Fatalf("decode emulator recovery attempt: %v", err)
	}
	return attempt
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
	receipts, err := client.Collection(
		"tenants/" + emulatorTenantID + "/ingestReceipts",
	).Documents(context.Background()).GetAll()
	if err != nil {
		t.Fatalf("list ingestReceipts for nested cleanup: %v", err)
	}
	for _, receipt := range receipts {
		attempts, attemptErr := receipt.Ref.Collection("recoveryAttempts").Documents(context.Background()).GetAll()
		if attemptErr != nil {
			t.Fatalf("list recovery attempts for cleanup: %v", attemptErr)
		}
		if len(attempts) == 0 {
			continue
		}
		batch := client.Batch()
		for _, attempt := range attempts {
			batch.Delete(attempt.Ref)
		}
		if _, commitErr := batch.Commit(context.Background()); commitErr != nil {
			t.Fatalf("clear recovery attempts: %v", commitErr)
		}
	}
	for _, collection := range []string{
		"ingestCleanupTargets",
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
