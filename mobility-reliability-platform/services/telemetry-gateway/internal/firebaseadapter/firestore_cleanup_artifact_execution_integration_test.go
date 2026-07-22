package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorConcurrentCleanupArtifactExecutionHasOneWinner(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	store, receipt, attempt, query := seedCleanupArtifactExecutionFixture(t, client)
	controlsBefore := readCleanupAbsenceControlSnapshots(t, client, receipt, attempt.AttemptID)

	type beginResult struct {
		request ingest.CleanupArtifactExecutionRequest
		grant   CleanupArtifactExecutionAuthorizationGrant
		ledger  ingest.CleanupExecutionLedger
		status  ingest.CleanupExecutionMutationStatus
		err     error
	}
	start := make(chan struct{})
	results := make(chan beginResult, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			request, grant, ledger, status, err := store.BeginCleanupArtifactExecution(
				context.Background(), query, ingest.CleanupArtifactExecutionRaw,
			)
			results <- beginResult{
				request: request, grant: grant, ledger: ledger, status: status, err: err,
			}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	applied := 0
	replayed := 0
	var winner beginResult
	for result := range results {
		if result.err != nil {
			t.Fatalf("BeginCleanupArtifactExecution() error = %v", result.err)
		}
		switch result.status {
		case ingest.CleanupExecutionMutationApplied:
			applied++
			winner = result
			if err := ValidateCleanupArtifactExecutionAuthorization(
				result.grant, result.request, time.Now().UTC(),
			); err != nil {
				t.Fatalf("winner grant = %v", err)
			}
		case ingest.CleanupExecutionMutationReplayed:
			replayed++
			if err := ValidateCleanupArtifactExecutionAuthorization(
				result.grant, result.request, time.Now().UTC(),
			); !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
				t.Fatalf("replayed grant error = %v", err)
			}
		default:
			t.Fatalf("unexpected mutation status = %q", result.status)
		}
	}
	if applied != 1 || replayed != 1 {
		t.Fatalf("applied/replayed = %d/%d", applied, replayed)
	}
	attemptAfter, dispatchUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receipt, attempt.AttemptID,
	)
	if attemptAfter.CleanupPhase != ingest.CleanupExecutionPhaseRawDispatchRecorded ||
		attemptAfter.CleanupExecutionRevision != 2 {
		t.Fatalf("durable dispatch attempt = %#v", attemptAfter)
	}
	assertCleanupAbsenceControlSnapshotsUnchanged(t, client, controlsBefore)

	request, grant, ledger, status, err := store.BeginCleanupArtifactExecution(
		context.Background(), query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationReplayed ||
		request.RequestHash != winner.request.RequestHash ||
		!reflect.DeepEqual(ledger, winner.ledger) {
		t.Fatalf("exact replay = %#v, %#v, %#v, %q, %v", request, grant, ledger, status, err)
	}
	_, replayUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receipt, attempt.AttemptID,
	)
	if !replayUpdateTime.Equal(dispatchUpdateTime) {
		t.Fatalf("dispatch replay changed UpdateTime: before=%v after=%v", dispatchUpdateTime, replayUpdateTime)
	}
}

func TestFirestoreAdmissionStoreEmulatorPersistsUnknownCleanupArtifactOutcome(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	store, receipt, attempt, query := seedCleanupArtifactExecutionFixture(t, client)
	controlsBefore := readCleanupAbsenceControlSnapshots(t, client, receipt, attempt.AttemptID)
	request, grant, _, status, err := store.BeginCleanupArtifactExecution(
		context.Background(), query, ingest.CleanupArtifactExecutionRaw,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied {
		t.Fatalf("BeginCleanupArtifactExecution() = %q, %v", status, err)
	}
	result := ingest.CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision: request.DispatchRevision, DeleteOutcome: ingest.CleanupDeleteUnknown,
		ErrorClass:        ingest.CleanupExecutionErrorProviderTimeout,
		MutationStartedAt: time.Now().UTC(),
		ObservedAt:        time.Now().UTC(),
	}
	ledger, status, err := store.RecordCleanupArtifactExecutionOutcome(
		context.Background(), grant, request, result,
	)
	if err != nil || status != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhaseRawOutcomeRecorded ||
		ledger.Raw.DeleteOutcome != ingest.CleanupDeleteUnknown ||
		ledger.ErrorClass != ingest.CleanupExecutionErrorProviderTimeout {
		t.Fatalf("RecordCleanupArtifactExecutionOutcome() = %#v, %q, %v", ledger, status, err)
	}
	_, outcomeUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receipt, attempt.AttemptID,
	)
	if _, _, err := store.AuthorizeCleanupAbsenceAudit(
		context.Background(), query, ingest.CleanupAbsenceAuditRaw,
	); !errors.Is(err, ingest.ErrCleanupExecutionConflict) {
		t.Fatalf("unknown outcome audit authorization error = %v", err)
	}
	_, afterDeniedAudit := readCleanupExecutionAttemptSnapshot(
		t, client, receipt, attempt.AttemptID,
	)
	if !afterDeniedAudit.Equal(outcomeUpdateTime) {
		t.Fatalf("denied unknown audit changed attempt: before=%v after=%v", outcomeUpdateTime, afterDeniedAudit)
	}
	assertCleanupAbsenceControlSnapshotsUnchanged(t, client, controlsBefore)

	replayed, status, err := store.RecordCleanupArtifactExecutionOutcome(
		context.Background(), grant, request, result,
	)
	if err != nil || status != ingest.CleanupExecutionMutationReplayed ||
		!reflect.DeepEqual(replayed, ledger) {
		t.Fatalf("unknown outcome replay = %#v, %q, %v", replayed, status, err)
	}
	_, replayUpdateTime := readCleanupExecutionAttemptSnapshot(
		t, client, receipt, attempt.AttemptID,
	)
	if !replayUpdateTime.Equal(outcomeUpdateTime) {
		t.Fatalf("unknown replay changed UpdateTime: before=%v after=%v", outcomeUpdateTime, replayUpdateTime)
	}
}

func seedCleanupArtifactExecutionFixture(
	t *testing.T,
	client *firestore.Client,
) (*FirestoreAdmissionStore, firestoreIngestReceipt, firestoreRecoveryAttempt, ingest.CleanupExecutionQuery) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, receipt, attempt := seedClaimedCleanupTargetFixture(t, client, now)
	command := cleanupTargetCommandFixture(
		t, receipt, ingest.ArtifactClassificationValidComplete,
	)
	_, status, err := store.createCleanupDryRunTarget(
		context.Background(), ingest.CleanupTargetAuthorizationGrant{}, command, command.CreatedAt,
		exactCleanupTargetSnapshotValidator(receipt, attempt),
	)
	if err != nil || status != ingest.CleanupTargetCreated {
		t.Fatalf("createCleanupDryRunTarget() = %q, %v", status, err)
	}
	store.now = func() time.Time { return time.Now().UTC() }
	query := ingest.CleanupExecutionQuery{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		AttemptID: attempt.AttemptID,
	}
	ledger, mutationStatus, err := store.InitializeCleanupExecutionLedger(
		context.Background(), query,
	)
	if err != nil || mutationStatus != ingest.CleanupExecutionMutationApplied ||
		ledger.Phase != ingest.CleanupExecutionPhasePlanned {
		t.Fatalf("InitializeCleanupExecutionLedger() = %#v, %q, %v", ledger, mutationStatus, err)
	}
	return store, receipt, attempt, query
}
