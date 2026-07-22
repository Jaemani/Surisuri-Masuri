package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorPersistsCleanupAbsenceAuditAndReplayWritesZero(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	store, receipt, attempt, query, _, _ := seedRawOutcomeCleanupExecutionFixture(t, client)
	evidenceSign := configureCleanupAbsenceAuditEvidence(t, store)
	controlBefore := readCleanupAbsenceControlSnapshots(t, client, receipt, attempt.AttemptID)

	request, grant, err := store.AuthorizeCleanupAbsenceAudit(
		context.Background(), query, ingest.CleanupAbsenceAuditRaw,
	)
	if err != nil {
		t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
	}
	store.now = func() time.Time { return grant.checkedAt }
	evidence := issueCleanupAbsenceAuditEvidence(
		t, evidenceSign, grant, request, grant.checkedAt,
	)
	ledger, mutationStatus, err := store.RecordCleanupAbsenceAudit(
		context.Background(), grant, request, evidence,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhaseRawAbsenceConfirmed ||
		ledger.Raw.AuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		!ledger.Raw.AuditedAt.Equal(grant.checkedAt) {
		t.Fatalf("RecordCleanupAbsenceAudit() = %#v, %q, %v", ledger, mutationStatus, err)
	}
	attemptAfter, appliedUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receipt, attempt.AttemptID,
	)
	if attemptAfter.CleanupExecutionRevision != request.ExpectedLedgerRevision+1 ||
		attemptAfter.CleanupPhase != ingest.CleanupExecutionPhaseRawAbsenceConfirmed ||
		attemptAfter.CleanupRawAuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		!attemptAfter.CleanupRawAuditedAt.Equal(grant.checkedAt) {
		t.Fatalf("persisted absence ledger = %#v", attemptAfter)
	}
	assertCleanupAbsenceControlSnapshotsUnchanged(t, client, controlBefore)

	replayed, mutationStatus, err := store.RecordCleanupAbsenceAudit(
		context.Background(), grant, request, evidence,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationReplayed ||
		!reflect.DeepEqual(replayed, ledger) {
		t.Fatalf("absence replay = %#v, %q, %v", replayed, mutationStatus, err)
	}
	_, replayUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receipt, attempt.AttemptID,
	)
	if !replayUpdateTime.Equal(appliedUpdateTime) {
		t.Fatalf("replay changed attempt update time: before=%v after=%v", appliedUpdateTime, replayUpdateTime)
	}
	assertCleanupAbsenceControlSnapshotsUnchanged(t, client, controlBefore)
}

func TestFirestoreAdmissionStoreEmulatorRejectsStaleCleanupAbsenceAuditGrantWithoutWrite(t *testing.T) {
	for _, test := range []struct {
		name   string
		update func(ingest.CleanupAbsenceAuditRequest) firestore.Update
	}{
		{
			name: "receipt revision",
			update: func(request ingest.CleanupAbsenceAuditRequest) firestore.Update {
				return firestore.Update{
					Path: "revision", Value: request.ExpectedReceiptRevision + 1,
				}
			},
		},
		{
			name: "fence",
			update: func(request ingest.CleanupAbsenceAuditRequest) firestore.Update {
				return firestore.Update{Path: "fencing_token", Value: request.ExpectedFence.Token + 1}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newAdmissionEmulatorClient(t)
			clearAdmissionIngestCollections(t, client)
			store, receipt, attempt, query, _, _ := seedRawOutcomeCleanupExecutionFixture(t, client)
			evidenceSign := configureCleanupAbsenceAuditEvidence(t, store)

			request, grant, err := store.AuthorizeCleanupAbsenceAudit(
				context.Background(), query, ingest.CleanupAbsenceAuditRaw,
			)
			if err != nil {
				t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
			}
			store.now = func() time.Time { return grant.checkedAt }
			evidence := issueCleanupAbsenceAuditEvidence(
				t, evidenceSign, grant, request, grant.checkedAt,
			)
			receiptPath := receiptDocumentPath(
				receipt.TenantID, receipt.ReceiptID,
			)
			if _, err := client.Doc(receiptPath).Update(
				context.Background(), []firestore.Update{test.update(request)},
			); err != nil {
				t.Fatalf("seed stale receipt state: %v", err)
			}
			_, attemptUpdateTime := readCleanupExecutionAttemptSnapshot(
				t, client, receipt, attempt.AttemptID,
			)
			controlBefore := readCleanupAbsenceControlSnapshots(t, client, receipt, attempt.AttemptID)

			if _, _, err := store.RecordCleanupAbsenceAudit(
				context.Background(), grant, request, evidence,
			); !errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) {
				t.Fatalf("stale absence audit error = %v", err)
			}
			_, afterUpdateTime := readCleanupExecutionAttemptSnapshot(
				t, client, receipt, attempt.AttemptID,
			)
			if !afterUpdateTime.Equal(attemptUpdateTime) {
				t.Fatalf("rejected audit changed attempt update time: before=%v after=%v", attemptUpdateTime, afterUpdateTime)
			}
			assertCleanupAbsenceControlSnapshotsUnchanged(t, client, controlBefore)
		})
	}

	t.Run("later ledger revision", func(t *testing.T) {
		client := newAdmissionEmulatorClient(t)
		clearAdmissionIngestCollections(t, client)
		store, receipt, attempt, query, plan, _ := seedRawOutcomeCleanupExecutionFixture(t, client)
		evidenceSign := configureCleanupAbsenceAuditEvidence(t, store)
		request, grant, err := store.AuthorizeCleanupAbsenceAudit(
			context.Background(), query, ingest.CleanupAbsenceAuditRaw,
		)
		if err != nil {
			t.Fatalf("AuthorizeCleanupAbsenceAudit() = %v", err)
		}
		store.now = func() time.Time { return grant.checkedAt }
		evidence := issueCleanupAbsenceAuditEvidence(
			t, evidenceSign, grant, request, grant.checkedAt,
		)
		ledger, mutationStatus, err := store.RecordCleanupAbsenceAudit(
			context.Background(), grant, request, evidence,
		)
		if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied {
			t.Fatalf("RecordCleanupAbsenceAudit() = %#v, %q, %v", ledger, mutationStatus, err)
		}
		store.now = func() time.Time { return grant.checkedAt.Add(time.Second) }
		command, err := ingest.BuildCleanupExecutionProgressCommand(
			plan, ledger, ingest.CleanupExecutionPhaseManifestDispatchRecorded, "", "",
		)
		if err != nil {
			t.Fatalf("BuildCleanupExecutionProgressCommand(manifest dispatch) = %v", err)
		}
		later, mutationStatus, err := store.RecordCleanupExecutionProgress(
			context.Background(), command,
		)
		if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
			later.Phase != ingest.CleanupExecutionPhaseManifestDispatchRecorded {
			t.Fatalf("RecordCleanupExecutionProgress(manifest dispatch) = %#v, %q, %v", later, mutationStatus, err)
		}
		_, laterUpdateTime := readCleanupExecutionAttemptSnapshot(
			t, client, receipt, attempt.AttemptID,
		)
		controlBefore := readCleanupAbsenceControlSnapshots(t, client, receipt, attempt.AttemptID)

		if _, _, err := store.RecordCleanupAbsenceAudit(
			context.Background(), grant, request, evidence,
		); !errors.Is(err, ingest.ErrCleanupExecutionConflict) {
			t.Fatalf("later ledger stale grant error = %v", err)
		}
		_, afterUpdateTime := readCleanupExecutionAttemptSnapshot(
			t, client, receipt, attempt.AttemptID,
		)
		if !afterUpdateTime.Equal(laterUpdateTime) {
			t.Fatalf("stale grant changed later ledger update time: before=%v after=%v", laterUpdateTime, afterUpdateTime)
		}
		assertCleanupAbsenceControlSnapshotsUnchanged(t, client, controlBefore)
	})
}

func seedRawOutcomeCleanupExecutionFixture(
	t *testing.T,
	client *firestore.Client,
) (
	*FirestoreAdmissionStore,
	firestoreIngestReceipt,
	firestoreRecoveryAttempt,
	ingest.CleanupExecutionQuery,
	ingest.CleanupExecutionLedgerPlan,
	ingest.CleanupExecutionLedger,
) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, receipt, attempt := seedClaimedCleanupTargetFixture(t, client, now)
	targetCommand := cleanupTargetCommandFixture(t, receipt, ingest.ArtifactClassificationValidComplete)
	target, createStatus, err := store.createCleanupDryRunTarget(
		context.Background(),
		ingest.CleanupTargetAuthorizationGrant{},
		targetCommand,
		targetCommand.CreatedAt,
		exactCleanupTargetSnapshotValidator(receipt, attempt),
	)
	if err != nil || createStatus != ingest.CleanupTargetCreated {
		t.Fatalf("createCleanupDryRunTarget() = %#v, %q, %v", target, createStatus, err)
	}
	store.now = func() time.Time { return time.Now().UTC() }
	query := ingest.CleanupExecutionQuery{
		TenantID:       receipt.TenantID,
		ReservationKey: receipt.ReservationKey,
		AttemptID:      attempt.AttemptID,
	}
	plan, err := ingest.BuildCleanupExecutionLedgerPlan(query, ingest.CurrentCleanupExecutionSnapshot{
		Receipt: receipt.toDomain(), Attempt: currentCleanupAttempt(attempt),
		Target: target, ReadTime: now,
	}, now)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}
	ledger, mutationStatus, err := store.InitializeCleanupExecutionLedger(context.Background(), query)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("InitializeCleanupExecutionLedger() = %#v, %q, %v", ledger, mutationStatus, err)
	}
	for _, step := range []struct {
		phase   ingest.CleanupExecutionPhase
		outcome ingest.CleanupDeleteRPCOutcome
	}{
		{phase: ingest.CleanupExecutionPhaseRawDispatchRecorded},
		{phase: ingest.CleanupExecutionPhaseRawOutcomeRecorded, outcome: ingest.CleanupDeleteObserved},
	} {
		command, buildErr := ingest.BuildCleanupExecutionProgressCommand(
			plan, ledger, step.phase, step.outcome, "",
		)
		if buildErr != nil {
			t.Fatalf("BuildCleanupExecutionProgressCommand(%q) = %v", step.phase, buildErr)
		}
		ledger, mutationStatus, err = store.RecordCleanupExecutionProgress(
			context.Background(), command,
		)
		if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied {
			t.Fatalf("RecordCleanupExecutionProgress(%q) = %#v, %q, %v", step.phase, ledger, mutationStatus, err)
		}
	}
	return store, receipt, attempt, query, plan, ledger
}

type cleanupAbsenceControlSnapshot struct {
	path       string
	data       map[string]interface{}
	updateTime time.Time
}

func readCleanupAbsenceControlSnapshots(
	t *testing.T,
	client *firestore.Client,
	receipt firestoreIngestReceipt,
	attemptID string,
) []cleanupAbsenceControlSnapshot {
	t.Helper()
	paths := []string{
		idempotencyDocumentPath(receipt.TenantID, receipt.ReservationKey),
		clientBatchDocumentPath(receipt.TenantID, receipt.ClientBatchKey),
		receiptDocumentPath(receipt.TenantID, receipt.ReceiptID),
		cleanupTargetDocumentPath(receipt.TenantID, attemptID),
	}
	snapshots := make([]cleanupAbsenceControlSnapshot, 0, len(paths))
	for _, path := range paths {
		document, err := client.Doc(path).Get(context.Background())
		if err != nil {
			t.Fatalf("read cleanup absence control %q: %v", path, err)
		}
		snapshots = append(snapshots, cleanupAbsenceControlSnapshot{
			path: path, data: document.Data(), updateTime: document.UpdateTime.UTC(),
		})
	}
	return snapshots
}

func assertCleanupAbsenceControlSnapshotsUnchanged(
	t *testing.T,
	client *firestore.Client,
	before []cleanupAbsenceControlSnapshot,
) {
	t.Helper()
	for _, expected := range before {
		document, err := client.Doc(expected.path).Get(context.Background())
		if err != nil {
			t.Fatalf("read cleanup absence control %q: %v", expected.path, err)
		}
		if !document.UpdateTime.UTC().Equal(expected.updateTime) ||
			!reflect.DeepEqual(document.Data(), expected.data) {
			t.Fatalf("cleanup absence audit changed control document %q", expected.path)
		}
	}
}
