package firebaseadapter

import (
	"context"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorPersistsCleanupExecutionProgressAndReplayWritesZero(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, receiptBefore, attemptBefore := seedClaimedCleanupTargetFixture(t, client, now)
	targetCommand := cleanupTargetCommandFixture(
		t, receiptBefore, ingest.ArtifactClassificationValidComplete,
	)
	target, createStatus, err := store.createCleanupDryRunTarget(
		context.Background(),
		ingest.CleanupTargetAuthorizationGrant{},
		targetCommand,
		targetCommand.CreatedAt,
		exactCleanupTargetSnapshotValidator(receiptBefore, attemptBefore),
	)
	if err != nil || createStatus != ingest.CleanupTargetCreated {
		t.Fatalf("createCleanupDryRunTarget() = %#v, %q, %v", target, createStatus, err)
	}
	store.now = func() time.Time { return time.Now().UTC() }
	query := ingest.CleanupExecutionQuery{
		TenantID: receiptBefore.TenantID, ReservationKey: receiptBefore.ReservationKey,
		AttemptID: attemptBefore.AttemptID,
	}
	plan, err := ingest.BuildCleanupExecutionLedgerPlan(query, ingest.CurrentCleanupExecutionSnapshot{
		Receipt: receiptBefore.toDomain(), Attempt: currentCleanupAttempt(attemptBefore),
		Target: target, ReadTime: now,
	}, now)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}

	ledger, mutationStatus, err := store.InitializeCleanupExecutionLedger(context.Background(), query)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhasePlanned || ledger.Revision != 1 {
		t.Fatalf("InitializeCleanupExecutionLedger() = %#v, %q, %v", ledger, mutationStatus, err)
	}
	attemptAfterInitialize, initializeUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receiptBefore, attemptBefore.AttemptID,
	)
	decoded, present, err := decodeCleanupExecutionLedger(plan, attemptAfterInitialize, now)
	if err != nil || !present || !reflect.DeepEqual(decoded, ledger) {
		t.Fatalf("initialized durable ledger = %#v, %t, %v", decoded, present, err)
	}

	command, err := ingest.BuildCleanupExecutionProgressCommand(
		plan, ledger, ingest.CleanupExecutionPhaseRawDispatchRecorded, "", "",
	)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionProgressCommand() = %v", err)
	}
	next, mutationStatus, err := store.RecordCleanupExecutionProgress(context.Background(), command)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
		next.Phase != ingest.CleanupExecutionPhaseRawDispatchRecorded || next.Revision != 2 {
		t.Fatalf("RecordCleanupExecutionProgress() = %#v, %q, %v", next, mutationStatus, err)
	}
	attemptAfterProgress, progressUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receiptBefore, attemptBefore.AttemptID,
	)
	if !progressUpdateTime.After(initializeUpdateTime) {
		t.Fatalf("progress update time = %v, initialize = %v", progressUpdateTime, initializeUpdateTime)
	}
	decoded, present, err = decodeCleanupExecutionLedger(plan, attemptAfterProgress, progressUpdateTime)
	if err != nil || !present || !reflect.DeepEqual(decoded, next) {
		t.Fatalf("progress durable ledger = %#v, %t, %v", decoded, present, err)
	}

	replayed, mutationStatus, err := store.RecordCleanupExecutionProgress(context.Background(), command)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationReplayed ||
		!reflect.DeepEqual(replayed, next) {
		t.Fatalf("progress replay = %#v, %q, %v", replayed, mutationStatus, err)
	}
	_, replayUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receiptBefore, attemptBefore.AttemptID,
	)
	if !replayUpdateTime.Equal(progressUpdateTime) {
		t.Fatalf("replay changed update time: before=%v after=%v", progressUpdateTime, replayUpdateTime)
	}

	receiptAfter := readAdmissionEmulatorReceipt(
		t, client, receiptBefore.TenantID, receiptBefore.ReceiptID,
	)
	targetAfter := readCleanupTargetEmulator(
		t, client, receiptBefore.TenantID, attemptBefore.AttemptID,
	)
	if receiptAfter != receiptBefore {
		t.Fatalf("intermediate progress changed receipt: before=%#v after=%#v", receiptBefore, receiptAfter)
	}
	if !reflect.DeepEqual(targetAfter, newFirestoreCleanupTarget(targetCommand, target.TargetHash)) {
		t.Fatalf("intermediate progress changed immutable target: %#v", targetAfter)
	}
}

func readCleanupExecutionAttemptSnapshot(
	t *testing.T,
	client *firestore.Client,
	receipt firestoreIngestReceipt,
	attemptID string,
) (firestoreRecoveryAttempt, time.Time) {
	t.Helper()
	document, err := client.Doc(recoveryAttemptDocumentPath(
		receipt.TenantID, receipt.ReceiptID, attemptID,
	)).Get(context.Background())
	if err != nil {
		t.Fatalf("read cleanup execution attempt: %v", err)
	}
	var attempt firestoreRecoveryAttempt
	if err := document.DataTo(&attempt); err != nil {
		t.Fatalf("decode cleanup execution attempt: %v", err)
	}
	return attempt, document.UpdateTime.UTC()
}
