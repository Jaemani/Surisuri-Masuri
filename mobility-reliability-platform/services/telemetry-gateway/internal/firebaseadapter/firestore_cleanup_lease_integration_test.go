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

func TestFirestoreAdmissionStoreEmulatorPreservesProgressDuringCleanupTakeover(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	persisted, prior, targetPath := seedExpiredProgressCleanupLease(t, client, now, false)
	controlBefore := readProgressTakeoverImmutableControls(t, client, persisted, targetPath)
	store, err := NewFirestoreAdmissionStore(
		client, emulatorTransactionTimout, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}
	proposal := ingest.CleanupAttemptProposal{
		ID: emulatorThirdReceiptID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := store.ClaimCleanupLease(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired ||
		ingest.ValidateCleanupLeaseGrant(grant) != nil {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v", grant, status, err)
	}
	closed := readAdmissionEmulatorAttempt(
		t, client, persisted.TenantID, persisted.ReceiptID, prior.AttemptID,
	)
	if closed.Status != ingest.RecoveryAttemptFailed ||
		closed.FailureCode != ingest.RecoveryAttemptFailureLeaseExpired ||
		!closed.FailedAt.Equal(now) ||
		closed.DecisionDomain != prior.DecisionDomain ||
		closed.CleanupTargetHash != prior.CleanupTargetHash ||
		closed.CleanupPlanHash != prior.CleanupPlanHash ||
		closed.CleanupReceiptRevision != prior.CleanupReceiptRevision ||
		closed.CleanupExecutionRevision != prior.CleanupExecutionRevision ||
		closed.CleanupPhase != prior.CleanupPhase ||
		closed.CleanupRawDeleteOutcome != prior.CleanupRawDeleteOutcome ||
		closed.CleanupRawAuditOutcome != prior.CleanupRawAuditOutcome ||
		!closed.CleanupRawAuditedAt.Equal(prior.CleanupRawAuditedAt) {
		t.Fatalf("closed progress cleanup attempt = %#v, prior=%#v", closed, prior)
	}
	current := readAdmissionEmulatorAttempt(
		t, client, persisted.TenantID, persisted.ReceiptID, proposal.ID,
	)
	if current.Status != ingest.RecoveryAttemptStarted ||
		current.FencingToken != persisted.FencingToken+1 ||
		hasCleanupExecutionLedgerResidue(current) {
		t.Fatalf("new cleanup attempt inherited prior progress = %#v", current)
	}
	assertProgressTakeoverImmutableControls(t, client, controlBefore)
}

func TestFirestoreAdmissionStoreEmulatorProgressTakeoverRollsBackOnDuplicateAttempt(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	persisted, prior, targetPath := seedExpiredProgressCleanupLease(t, client, now, true)
	controlBefore := readProgressTakeoverImmutableControls(t, client, persisted, targetPath)
	store, err := NewFirestoreAdmissionStore(
		client, emulatorTransactionTimout, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() error = %v", err)
	}
	proposal := ingest.CleanupAttemptProposal{
		ID: emulatorThirdReceiptID, WorkerVersion: ingest.CleanupWorkerVersion,
	}

	grant, status, err := store.ClaimCleanupLease(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		ingest.LeaseOwner{ID: proposal.ID, Kind: ingest.LeaseOwnerCleanup},
		proposal,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) || status != "" ||
		grant != (ingest.CleanupLeaseGrant{}) {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v, want unavailable", grant, status, err)
	}
	unchanged := readAdmissionEmulatorAttempt(
		t, client, persisted.TenantID, persisted.ReceiptID, prior.AttemptID,
	)
	if unchanged.Status != ingest.RecoveryAttemptStarted ||
		unchanged.CleanupExecutionRevision != prior.CleanupExecutionRevision ||
		unchanged.CleanupPhase != prior.CleanupPhase ||
		unchanged.FailureCode != "" || !unchanged.FailedAt.IsZero() {
		t.Fatalf("prior progress changed after rollback = %#v", unchanged)
	}
	storedReceipt := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	if storedReceipt.FencingToken != persisted.FencingToken ||
		storedReceipt.Revision != persisted.Revision ||
		storedReceipt.RecoveryAttemptCount != persisted.RecoveryAttemptCount ||
		storedReceipt.LeaseOwnerID != persisted.LeaseOwnerID {
		t.Fatalf("receipt changed after progress rollback = %#v", storedReceipt)
	}
	assertProgressTakeoverImmutableControls(t, client, controlBefore)
}

type progressTakeoverControl struct {
	path       string
	updateTime time.Time
}

func seedExpiredProgressCleanupLease(
	t *testing.T,
	client *firestore.Client,
	now time.Time,
	duplicateIncoming bool,
) (firestoreIngestReceipt, firestoreRecoveryAttempt, string) {
	t.Helper()
	receipt, prior := seedExpiredCleanupLease(t, client, now, duplicateIncoming)
	command := cleanupTargetCommandFixture(t, receipt, ingest.ArtifactClassificationValidComplete)
	targetHash, err := ingest.CleanupTargetHash(command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	target := ingest.CleanupTarget{Command: command, TargetHash: targetHash}
	plan, err := ingest.BuildExpiredCleanupExecutionLedgerPlan(
		ingest.CleanupExecutionQuery{
			TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
			AttemptID: prior.AttemptID,
		},
		ingest.CurrentCleanupExecutionSnapshot{
			Receipt: receipt.toDomain(), Attempt: currentCleanupAttempt(prior),
			Target: target, ReadTime: now,
		},
		now,
	)
	if err != nil {
		t.Fatalf("BuildExpiredCleanupExecutionLedgerPlan() = %v", err)
	}
	ledger, err := ingest.NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	for _, step := range []ingest.CleanupExecutionTransition{
		{
			Phase:      ingest.CleanupExecutionPhaseRawDispatchRecorded,
			ObservedAt: command.CreatedAt.Add(time.Second),
		},
		{
			Phase:         ingest.CleanupExecutionPhaseRawOutcomeRecorded,
			DeleteOutcome: ingest.CleanupDeleteObserved,
			ObservedAt:    command.CreatedAt.Add(2 * time.Second),
		},
		{
			Phase:        ingest.CleanupExecutionPhaseRawAbsenceConfirmed,
			AuditOutcome: ingest.CleanupAuditConfirmedAbsent,
			ObservedAt:   command.CreatedAt.Add(3 * time.Second),
		},
	} {
		ledger, err = ingest.AdvanceCleanupExecutionLedger(plan, ledger, step)
		if err != nil {
			t.Fatalf("AdvanceCleanupExecutionLedger(%q) = %v", step.Phase, err)
		}
	}
	prior = attemptWithCleanupExecutionLedger(prior, ledger)
	targetPath := cleanupTargetDocumentPath(receipt.TenantID, prior.AttemptID)
	batch := client.Batch()
	batch.Set(client.Doc(recoveryAttemptDocumentPath(
		receipt.TenantID, receipt.ReceiptID, prior.AttemptID,
	)), prior)
	batch.Set(client.Doc(targetPath), newFirestoreCleanupTarget(command, targetHash))
	if _, err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("seed expired cleanup progress: %v", err)
	}
	return receipt, prior, targetPath
}

func readProgressTakeoverImmutableControls(
	t *testing.T,
	client *firestore.Client,
	receipt firestoreIngestReceipt,
	targetPath string,
) []progressTakeoverControl {
	t.Helper()
	paths := []string{
		idempotencyDocumentPath(receipt.TenantID, receipt.ReservationKey),
		clientBatchDocumentPath(receipt.TenantID, receipt.ClientBatchKey),
		targetPath,
	}
	controls := make([]progressTakeoverControl, 0, len(paths))
	for _, path := range paths {
		document, err := client.Doc(path).Get(context.Background())
		if err != nil {
			t.Fatalf("read progress takeover control %q: %v", path, err)
		}
		controls = append(controls, progressTakeoverControl{
			path: path, updateTime: document.UpdateTime.UTC(),
		})
	}
	return controls
}

func assertProgressTakeoverImmutableControls(
	t *testing.T,
	client *firestore.Client,
	controls []progressTakeoverControl,
) {
	t.Helper()
	for _, expected := range controls {
		document, err := client.Doc(expected.path).Get(context.Background())
		if err != nil {
			t.Fatalf("read progress takeover control %q: %v", expected.path, err)
		}
		if !document.UpdateTime.UTC().Equal(expected.updateTime) {
			t.Fatalf("progress takeover changed immutable control %q", expected.path)
		}
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
