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

func TestFirestoreAdmissionStoreEmulatorCommitsForwardRecoveryActionsAtomically(t *testing.T) {
	tests := []struct {
		name        string
		action      ingest.ForwardRecoveryAction
		wantState   ingest.ReceiptState
		wantOutcome ingest.RecoveryAttemptOutcome
	}{
		{name: "stored", action: ingest.ForwardRecoveryActionMarkStored, wantState: ingest.ReceiptStored, wantOutcome: ingest.RecoveryAttemptOutcomeStored},
		{name: "rejected", action: ingest.ForwardRecoveryActionMarkRejected, wantState: ingest.ReceiptRejected, wantOutcome: ingest.RecoveryAttemptOutcomeRejected},
		{name: "hold", action: ingest.ForwardRecoveryActionMarkHold, wantState: ingest.ReceiptRecoveryHold, wantOutcome: ingest.RecoveryAttemptOutcomeHold},
		{name: "release", action: ingest.ForwardRecoveryActionReleaseLease, wantState: ingest.ReceiptReserved, wantOutcome: ingest.RecoveryAttemptOutcomeLeaseReleased},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEmulatorForwardRecoveryActionFixture(t, test.action)
			got, err := fixture.store.commitForwardRecoveryAction(
				context.Background(),
				ingest.ForwardRecoveryActionGrant{},
				fixture.command,
				fixture.observedAt,
				emulatorForwardRecoveryActionValidator,
			)
			if err != nil {
				t.Fatalf("commitForwardRecoveryAction() = %v", err)
			}
			persistedReceipt := readAdmissionEmulatorReceipt(
				t, fixture.client, fixture.command.TenantID, got.ReceiptID,
			)
			persistedAttempt := readAdmissionEmulatorAttempt(
				t, fixture.client, fixture.command.TenantID, got.ReceiptID, fixture.command.Attempt.ID,
			)
			if persistedReceipt.State != test.wantState ||
				persistedReceipt.Revision != fixture.command.ReceiptRevision+1 ||
				persistedAttempt.Status != ingest.RecoveryAttemptCompleted ||
				persistedAttempt.Outcome != test.wantOutcome ||
				persistedAttempt.Action != test.action || !lowerHexDigest(persistedAttempt.ActionHash) ||
				!persistedAttempt.CompletedAt.Equal(persistedReceipt.UpdatedAt) {
				t.Fatalf("receipt/attempt = %#v / %#v", persistedReceipt, persistedAttempt)
			}
			if receiptHasLeaseFields(persistedReceipt) {
				t.Fatalf("terminal action retained lease = %#v", persistedReceipt)
			}
			assertAdmissionCollectionCount(t, fixture.client, "ingestIdempotency", 1)
			assertAdmissionCollectionCount(t, fixture.client, "ingestClientBatches", 1)
			assertAdmissionCollectionCount(t, fixture.client, "ingestReceipts", 1)
		})
	}
}

func TestFirestoreAdmissionStoreEmulatorForwardRecoveryConsentWithdrawalWritesZero(t *testing.T) {
	fixture := newEmulatorForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	before := readAdmissionEmulatorReceipt(
		t, fixture.client, fixture.command.TenantID, fixture.receiptID,
	)
	consentStateID := authorization.ConsentStateDocumentID(
		emulatorPersonID,
		authorization.PreciseLocationPurpose,
	)
	if _, err := fixture.client.Doc(
		"tenants/"+emulatorTenantID+"/consentStates/"+consentStateID,
	).Update(context.Background(), []firestore.Update{{Path: "status", Value: "withdrawn"}}); err != nil {
		t.Fatalf("withdraw consent: %v", err)
	}

	_, err := fixture.store.commitForwardRecoveryAction(
		context.Background(),
		ingest.ForwardRecoveryActionGrant{},
		fixture.command,
		fixture.observedAt,
		emulatorForwardRecoveryActionValidator,
	)
	if !errors.Is(err, ingest.ErrForwardRecoveryUnauthorized) {
		t.Fatalf("commitForwardRecoveryAction() = %v", err)
	}
	after := readAdmissionEmulatorReceipt(t, fixture.client, fixture.command.TenantID, fixture.receiptID)
	attempt := readAdmissionEmulatorAttempt(
		t, fixture.client, fixture.command.TenantID, fixture.receiptID, fixture.command.Attempt.ID,
	)
	if after.State != ingest.ReceiptReserved || after.Revision != before.Revision ||
		after.LeaseOwnerID != before.LeaseOwnerID || attempt.Status != ingest.RecoveryAttemptStarted ||
		!attempt.CompletedAt.IsZero() {
		t.Fatalf("withdrawal mutated receipt/attempt = %#v / %#v", after, attempt)
	}
}

func TestFirestoreAdmissionStoreEmulatorMissingAttemptRollsBackReceiptAction(t *testing.T) {
	fixture := newEmulatorForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	before := readAdmissionEmulatorReceipt(
		t, fixture.client, fixture.command.TenantID, fixture.receiptID,
	)
	if _, err := fixture.client.Doc(recoveryAttemptDocumentPath(
		fixture.command.TenantID,
		fixture.receiptID,
		fixture.command.Attempt.ID,
	)).Delete(context.Background()); err != nil {
		t.Fatalf("delete recovery attempt: %v", err)
	}

	_, err := fixture.store.commitForwardRecoveryAction(
		context.Background(),
		ingest.ForwardRecoveryActionGrant{},
		fixture.command,
		fixture.observedAt,
		emulatorForwardRecoveryActionValidator,
	)
	if !errors.Is(err, ingest.ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("commitForwardRecoveryAction() = %v", err)
	}
	after := readAdmissionEmulatorReceipt(t, fixture.client, fixture.command.TenantID, fixture.receiptID)
	if after.State != before.State || after.Revision != before.Revision ||
		after.LeaseOwnerID != before.LeaseOwnerID || !after.LeaseExpiresAt.Equal(before.LeaseExpiresAt) {
		t.Fatalf("missing attempt mutated receipt = %#v, before %#v", after, before)
	}
}

type emulatorForwardRecoveryActionFixture struct {
	client     *firestore.Client
	store      *FirestoreAdmissionStore
	command    ingest.ForwardRecoveryActionCommand
	receiptID  string
	observedAt time.Time
}

func newEmulatorForwardRecoveryActionFixture(
	t *testing.T,
	action ingest.ForwardRecoveryAction,
) emulatorForwardRecoveryActionFixture {
	t.Helper()
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedAdmissionAuthorization(t, client, now)
	persisted := seedExpiredAdmissionReservation(t, client, now)
	store, err := NewFirestoreAdmissionStore(client, emulatorTransactionTimout, time.Now)
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() = %v", err)
	}
	attempt := ingest.RecoveryAttemptProposal{
		ID: emulatorSecondReceiptID, WorkerVersion: ingest.RecoveryWorkerVersion,
	}
	lease, status, err := store.ClaimRecoveryLease(
		context.Background(),
		persisted.TenantID,
		persisted.ReservationKey,
		ingest.LeaseOwner{ID: attempt.ID, Kind: ingest.LeaseOwnerSweeper},
		attempt,
		time.Now().UTC(),
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired {
		t.Fatalf("ClaimRecoveryLease() = %#v, %q, %v", lease, status, err)
	}
	receipt := readAdmissionEmulatorReceipt(t, client, persisted.TenantID, persisted.ReceiptID)
	observedAt := time.Now().UTC()
	command := emulatorForwardRecoveryActionCommand(t, receipt, attempt, lease, action, observedAt)
	return emulatorForwardRecoveryActionFixture{
		client: client, store: store, command: command,
		receiptID: receipt.ReceiptID, observedAt: observedAt,
	}
}

func emulatorForwardRecoveryActionCommand(
	t *testing.T,
	receipt firestoreIngestReceipt,
	attempt ingest.RecoveryAttemptProposal,
	lease ingest.LeaseGrant,
	action ingest.ForwardRecoveryAction,
	observedAt time.Time,
) ingest.ForwardRecoveryActionCommand {
	t.Helper()
	stored := emulatorStoredReceiptData(receipt)
	raw := artifactLineageFromStoredData(stored.Artifacts.Object)
	manifest := artifactLineageFromStoredData(stored.Artifacts.Manifest)
	command := ingest.ForwardRecoveryActionCommand{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		Attempt: attempt, ReceiptRevision: receipt.Revision, Fence: lease.Fence,
	}
	switch action {
	case ingest.ForwardRecoveryActionMarkStored:
		command.Plan = ingest.ForwardRecoveryActionPlan{
			Phase: ingest.RecoveryPhaseConfirmation, Action: action,
			Classification: ingest.ArtifactClassificationValidComplete,
			ReasonCode:     ingest.ArtifactReasonManifestAndReferencedRawValid,
			Raw:            &raw, Manifest: &manifest,
		}
	case ingest.ForwardRecoveryActionMarkRejected:
		command.Plan = ingest.ForwardRecoveryActionPlan{
			Phase: ingest.RecoveryPhaseConfirmation, Action: action,
			Classification: ingest.ArtifactClassificationRawContentConflict,
			ReasonCode:     ingest.ArtifactReasonStrictPayloadInvalid,
			RejectionCode:  "object_conflict", Raw: &raw,
		}
	case ingest.ForwardRecoveryActionMarkHold:
		command.Plan = ingest.ForwardRecoveryActionPlan{
			Phase: ingest.RecoveryPhaseInitial, Action: action,
			Classification: ingest.ArtifactClassificationManifestOnly,
			ReasonCode:     ingest.ArtifactReasonReferencedRawNotFound,
			HoldCode:       ingest.RecoveryHoldManifestOnly,
		}
		command.HoldReviewDueAt = observedAt.Add(ingest.DefaultRecoveryHoldReviewWindow)
	case ingest.ForwardRecoveryActionReleaseLease:
		command.Plan = ingest.ForwardRecoveryActionPlan{
			Phase: ingest.RecoveryPhaseInitial, Action: action,
			Classification: ingest.ArtifactClassificationNone,
			ReasonCode:     ingest.ArtifactReasonNoCandidates,
			ReleaseCode:    ingest.LeaseReleaseAwaitingClientReplay,
		}
	default:
		t.Fatalf("unsupported action %q", action)
	}
	if _, err := ingest.ForwardRecoveryActionHash(command); err != nil {
		t.Fatalf("ForwardRecoveryActionHash() = %v", err)
	}
	return command
}

func emulatorForwardRecoveryActionValidator(
	_ ingest.ForwardRecoveryActionGrant,
	command ingest.ForwardRecoveryActionCommand,
	snapshot ingest.CurrentForwardRecoverySnapshot,
	attempt ingest.CurrentForwardRecoveryAttempt,
	_ time.Time,
) error {
	if snapshot.Consent.Status != "granted" || snapshot.ConsentState.Status != "granted" ||
		snapshot.ConsentState.CurrentRevisionID != snapshot.Receipt.ConsentRevisionID {
		return ingest.ErrForwardRecoveryUnauthorized
	}
	if snapshot.Receipt.Revision != command.ReceiptRevision ||
		attempt.AttemptID != command.Attempt.ID || attempt.Status != ingest.RecoveryAttemptStarted {
		return ingest.ErrInvalidForwardRecoveryActionAuthorization
	}
	return nil
}
