package firebaseadapter

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorConcurrentCleanupClaimHasOneWinner(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	persisted := seedCleanupPendingReservation(t, client, now)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}
	type claimResult struct {
		proposal ingest.CleanupAttemptProposal
		grant    ingest.CleanupLeaseGrant
		status   ingest.LeaseStatus
		err      error
	}
	owners := []string{emulatorSecondReceiptID, emulatorThirdReceiptID}
	start := make(chan struct{})
	results := make(chan claimResult, len(owners))
	var wait sync.WaitGroup
	for _, ownerID := range owners {
		ownerID := ownerID
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			proposal := ingest.CleanupAttemptProposal{
				ID: ownerID, WorkerVersion: ingest.CleanupWorkerVersion,
			}
			grant, claimStatus, claimErr := store.ClaimCleanupLease(
				context.Background(),
				persisted.TenantID,
				persisted.ReservationKey,
				ingest.LeaseOwner{ID: ownerID, Kind: ingest.LeaseOwnerCleanup},
				proposal,
				now,
				ingest.DefaultRequestLeaseDuration,
			)
			results <- claimResult{proposal: proposal, grant: grant, status: claimStatus, err: claimErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	statusCounts := make(map[ingest.LeaseStatus]int)
	var winner claimResult
	for result := range results {
		if result.err != nil {
			t.Fatalf("ClaimCleanupLease() error = %v", result.err)
		}
		statusCounts[result.status]++
		if result.status == ingest.LeaseStatusAcquired {
			winner = result
		}
	}
	if statusCounts[ingest.LeaseStatusAcquired] != 1 || statusCounts[ingest.LeaseStatusHeld] != 1 {
		t.Fatalf("cleanup claim statuses = %#v, want one acquired and one held", statusCounts)
	}
	if ingest.ValidateCleanupLeaseGrant(winner.grant) != nil {
		t.Fatalf("winner cleanup grant = %#v", winner.grant)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.State != ingest.ReceiptCleanupPending || stored.FencingToken != persisted.FencingToken+1 ||
		stored.Revision != persisted.Revision+1 || stored.RecoveryAttemptCount != persisted.RecoveryAttemptCount+1 ||
		stored.LeaseOwnerKind != ingest.LeaseOwnerCleanup || stored.LeaseOwnerID != winner.proposal.ID ||
		!stored.CleanupTransitionedAt.Equal(persisted.CleanupTransitionedAt) ||
		!stored.CleanupQuiescenceUntil.Equal(persisted.CleanupQuiescenceUntil) ||
		stored.CleanupMode != persisted.CleanupMode || stored.CleanupOriginStatus != persisted.CleanupOriginStatus ||
		stored.CleanupPolicyVersion != persisted.CleanupPolicyVersion || !stored.NextRecoveryAt.IsZero() {
		t.Fatalf("stored cleanup claim = %#v", stored)
	}
	rawReceipt, err := client.Doc(receiptDocumentPath(stored.TenantID, stored.ReceiptID)).Get(context.Background())
	if err != nil {
		t.Fatalf("read raw cleanup receipt: %v", err)
	}
	if _, exists := rawReceipt.Data()["next_recovery_at"]; exists {
		t.Fatal("cleanup claim retained raw next_recovery_at field")
	}
	attempt := readAdmissionEmulatorAttempt(
		t,
		client,
		stored.TenantID,
		stored.ReceiptID,
		winner.proposal.ID,
	)
	if attempt.Status != ingest.RecoveryAttemptStarted || attempt.OwnerKind != ingest.LeaseOwnerCleanup ||
		attempt.WorkerVersion != ingest.CleanupWorkerVersion ||
		attempt.FencingToken != stored.FencingToken || !attempt.StartedAt.Equal(now) {
		t.Fatalf("stored cleanup winner attempt = %#v", attempt)
	}
	attempts, err := client.Collection(
		receiptDocumentPath(stored.TenantID, stored.ReceiptID) + "/recoveryAttempts",
	).Documents(context.Background()).GetAll()
	if err != nil {
		t.Fatalf("list cleanup attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("cleanup attempt count = %d, want 1", len(attempts))
	}
}

func TestFirestoreAdmissionStoreEmulatorConcurrentCleanupTakeoverHasOneWinner(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	persisted, prior := seedExpiredCleanupLease(t, client, now, false)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}
	type claimResult struct {
		owner  string
		grant  ingest.CleanupLeaseGrant
		status ingest.LeaseStatus
		err    error
	}
	owners := []string{emulatorFirstReceiptID, emulatorThirdReceiptID}
	start := make(chan struct{})
	results := make(chan claimResult, len(owners))
	var wait sync.WaitGroup
	for _, ownerID := range owners {
		ownerID := ownerID
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			proposal := ingest.CleanupAttemptProposal{ID: ownerID, WorkerVersion: ingest.CleanupWorkerVersion}
			grant, status, claimErr := store.ClaimCleanupLease(
				context.Background(),
				persisted.TenantID,
				persisted.ReservationKey,
				ingest.LeaseOwner{ID: ownerID, Kind: ingest.LeaseOwnerCleanup},
				proposal,
				now,
				ingest.DefaultRequestLeaseDuration,
			)
			results <- claimResult{owner: ownerID, grant: grant, status: status, err: claimErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	statusCounts := make(map[ingest.LeaseStatus]int)
	winner := ""
	for result := range results {
		if result.err != nil {
			t.Fatalf("ClaimCleanupLease() error = %v", result.err)
		}
		statusCounts[result.status]++
		if result.status == ingest.LeaseStatusAcquired {
			winner = result.owner
			if ingest.ValidateCleanupLeaseGrant(result.grant) != nil {
				t.Fatalf("winner grant = %#v", result.grant)
			}
		}
	}
	if statusCounts[ingest.LeaseStatusAcquired] != 1 || statusCounts[ingest.LeaseStatusHeld] != 1 {
		t.Fatalf("cleanup takeover statuses = %#v, want one acquired and one held", statusCounts)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.LeaseOwnerID != winner || stored.FencingToken != persisted.FencingToken+1 ||
		stored.Revision != persisted.Revision+1 || stored.RecoveryAttemptCount != persisted.RecoveryAttemptCount+1 {
		t.Fatalf("stored concurrent takeover = %#v", stored)
	}
	closed := readAdmissionEmulatorAttempt(t, client, persisted.TenantID, persisted.ReceiptID, prior.AttemptID)
	if closed.Status != ingest.RecoveryAttemptFailed ||
		closed.FailureCode != ingest.RecoveryAttemptFailureLeaseExpired || !closed.FailedAt.Equal(now) {
		t.Fatalf("closed prior attempt = %#v", closed)
	}
	attempts, err := client.Collection(
		receiptDocumentPath(stored.TenantID, stored.ReceiptID) + "/recoveryAttempts",
	).Documents(context.Background()).GetAll()
	if err != nil {
		t.Fatalf("list cleanup takeover attempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("cleanup takeover attempt count = %d, want 2", len(attempts))
	}
}

func TestFirestoreAdmissionStoreEmulatorCleanupTakeoverClosesPriorAttempt(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	persisted, prior := seedExpiredCleanupLease(t, client, now, false)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}
	proposal := ingest.CleanupAttemptProposal{
		ID: emulatorThirdReceiptID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, claimStatus, err := store.ClaimCleanupLease(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || claimStatus != ingest.LeaseStatusAcquired || ingest.ValidateCleanupLeaseGrant(grant) != nil {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v", grant, claimStatus, err)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.FencingToken != persisted.FencingToken+1 || stored.Revision != persisted.Revision+1 ||
		stored.RecoveryAttemptCount != persisted.RecoveryAttemptCount+1 ||
		stored.LeaseOwnerID != proposal.ID || stored.LeaseOwnerKind != ingest.LeaseOwnerCleanup ||
		!stored.CleanupTransitionedAt.Equal(persisted.CleanupTransitionedAt) ||
		!stored.CleanupQuiescenceUntil.Equal(persisted.CleanupQuiescenceUntil) ||
		stored.CleanupPolicyVersion != persisted.CleanupPolicyVersion {
		t.Fatalf("stored cleanup takeover = %#v", stored)
	}
	closed := readAdmissionEmulatorAttempt(t, client, persisted.TenantID, persisted.ReceiptID, prior.AttemptID)
	if closed.Status != ingest.RecoveryAttemptFailed ||
		closed.FailureCode != ingest.RecoveryAttemptFailureLeaseExpired ||
		!closed.FailedAt.Equal(now) || closed.FencingToken != prior.FencingToken {
		t.Fatalf("closed cleanup attempt = %#v", closed)
	}
	current := readAdmissionEmulatorAttempt(t, client, persisted.TenantID, persisted.ReceiptID, proposal.ID)
	if current.Status != ingest.RecoveryAttemptStarted || current.OwnerKind != ingest.LeaseOwnerCleanup ||
		current.WorkerVersion != ingest.CleanupWorkerVersion || current.FencingToken != stored.FencingToken ||
		!current.StartedAt.Equal(now) {
		t.Fatalf("current cleanup attempt = %#v", current)
	}
}

func TestFirestoreAdmissionStoreEmulatorDuplicateCleanupAttemptRollsBackTakeover(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	persisted, prior := seedExpiredCleanupLease(t, client, now, true)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}
	proposal := ingest.CleanupAttemptProposal{
		ID: emulatorThirdReceiptID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, claimStatus, err := store.ClaimCleanupLease(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) ||
		claimStatus != "" || grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want unavailable", grant, claimStatus, err)
	}
	stored := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if stored.State != persisted.State || stored.FencingToken != persisted.FencingToken ||
		stored.Revision != persisted.Revision || stored.RecoveryAttemptCount != persisted.RecoveryAttemptCount ||
		stored.LeaseOwnerID != persisted.LeaseOwnerID || stored.LeaseOwnerKind != persisted.LeaseOwnerKind ||
		!stored.LeaseAcquiredAt.Equal(persisted.LeaseAcquiredAt) ||
		!stored.LeaseHeartbeatAt.Equal(persisted.LeaseHeartbeatAt) ||
		!stored.LeaseExpiresAt.Equal(persisted.LeaseExpiresAt) ||
		!stored.CleanupTransitionedAt.Equal(persisted.CleanupTransitionedAt) ||
		!stored.CleanupQuiescenceUntil.Equal(persisted.CleanupQuiescenceUntil) ||
		stored.CleanupPolicyVersion != persisted.CleanupPolicyVersion {
		t.Fatalf("receipt changed after duplicate cleanup attempt: before=%#v after=%#v", persisted, stored)
	}
	unchangedPrior := readAdmissionEmulatorAttempt(t, client, persisted.TenantID, persisted.ReceiptID, prior.AttemptID)
	if unchangedPrior != prior {
		t.Fatalf("prior cleanup attempt changed after rollback: before=%#v after=%#v", prior, unchangedPrior)
	}
	duplicate := readAdmissionEmulatorAttempt(t, client, persisted.TenantID, persisted.ReceiptID, proposal.ID)
	if duplicate.Status != ingest.RecoveryAttemptStarted || duplicate.FencingToken != persisted.FencingToken+1 {
		t.Fatalf("duplicate cleanup attempt changed = %#v", duplicate)
	}
}

func seedCleanupPendingReservation(
	t *testing.T,
	client *firestore.Client,
	quietAt time.Time,
) firestoreIngestReceipt {
	t.Helper()
	transitionedAt := quietAt.Add(-ingest.DefaultCleanupLateWriteGrace)
	createdAt := transitionedAt.Add(-ingest.ReservationProcessingWindow)
	reservation := emulatorReservation(createdAt, emulatorFirstReceiptID)
	index := newFirestoreIngestIndex(reservation)
	receipt := newFirestoreIngestReceipt(
		reservation,
		ingest.LeaseOwner{ID: emulatorFirstReceiptID, Kind: ingest.LeaseOwnerRequest},
		createdAt,
		createdAt.Add(ingest.DefaultRequestLeaseDuration),
	)
	receipt.State = ingest.ReceiptCleanupPending
	receipt.clearLease()
	receipt.FencingToken = 2
	receipt.Revision = 2
	receipt.UpdatedAt = transitionedAt
	receipt.CleanupTransitionedAt = transitionedAt
	receipt.CleanupQuiescenceUntil = quietAt
	receipt.CleanupMode = ingest.CleanupModeReservationExpiry
	receipt.CleanupOriginStatus = ingest.ReceiptReserved
	receipt.CleanupPolicyVersion = ingest.CleanupTransitionPolicyV1
	batch := client.Batch()
	batch.Set(client.Doc(idempotencyDocumentPath(reservation.TenantID, reservation.ReservationKey)), index)
	batch.Set(client.Doc(clientBatchDocumentPath(reservation.TenantID, reservation.ClientBatchKey)), index)
	batch.Set(client.Doc(receiptDocumentPath(reservation.TenantID, reservation.ReceiptID)), receipt)
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed cleanup pending receipt: %v", err)
	}
	return receipt
}

func seedExpiredCleanupLease(
	t *testing.T,
	client *firestore.Client,
	now time.Time,
	seedDuplicateIncomingAttempt bool,
) (firestoreIngestReceipt, firestoreRecoveryAttempt) {
	t.Helper()
	leaseAcquiredAt := now.Add(-ingest.DefaultRequestLeaseDuration)
	quietAt := leaseAcquiredAt
	receipt := seedCleanupPendingReservation(t, client, quietAt)
	receipt.LeaseOwnerID = emulatorSecondReceiptID
	receipt.LeaseOwnerKind = ingest.LeaseOwnerCleanup
	receipt.LeaseAcquiredAt = leaseAcquiredAt
	receipt.LeaseHeartbeatAt = leaseAcquiredAt
	receipt.LeaseExpiresAt = now
	receipt.FencingToken = 3
	receipt.RecoveryAttemptCount = 1
	receipt.Revision = 3
	receipt.UpdatedAt = leaseAcquiredAt
	prior := newFirestoreCleanupAttempt(
		ingest.CleanupAttemptProposal{
			ID: emulatorSecondReceiptID, WorkerVersion: ingest.CleanupWorkerVersion,
		},
		receipt.TenantID,
		receipt.ReceiptID,
		receipt.FencingToken,
		leaseAcquiredAt,
	)
	batch := client.Batch()
	batch.Set(client.Doc(receiptDocumentPath(receipt.TenantID, receipt.ReceiptID)), receipt)
	batch.Set(client.Doc(recoveryAttemptDocumentPath(
		receipt.TenantID,
		receipt.ReceiptID,
		prior.AttemptID,
	)), prior)
	if seedDuplicateIncomingAttempt {
		duplicate := newFirestoreCleanupAttempt(
			ingest.CleanupAttemptProposal{
				ID: emulatorThirdReceiptID, WorkerVersion: ingest.CleanupWorkerVersion,
			},
			receipt.TenantID,
			receipt.ReceiptID,
			receipt.FencingToken+1,
			now,
		)
		batch.Set(client.Doc(recoveryAttemptDocumentPath(
			receipt.TenantID,
			receipt.ReceiptID,
			duplicate.AttemptID,
		)), duplicate)
	}
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed expired cleanup lease: %v", err)
	}
	return receipt, prior
}
