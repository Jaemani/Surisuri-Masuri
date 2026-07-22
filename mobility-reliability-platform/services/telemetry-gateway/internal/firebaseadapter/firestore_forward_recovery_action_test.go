package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreCommitsForwardRecoveryActionsAtomically(t *testing.T) {
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
			fixture := newForwardRecoveryActionFixture(t, test.action)
			store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
			validatorCalls := 0
			got, err := store.commitForwardRecoveryAction(
				context.Background(),
				ingest.ForwardRecoveryActionGrant{},
				fixture.command,
				fixture.observedAt,
				func(
					_ ingest.ForwardRecoveryActionGrant,
					command ingest.ForwardRecoveryActionCommand,
					snapshot ingest.CurrentForwardRecoverySnapshot,
					attempt ingest.CurrentForwardRecoveryAttempt,
					checkedAt time.Time,
				) error {
					validatorCalls++
					if command.Attempt.ID != fixture.command.Attempt.ID ||
						snapshot.Receipt.ReceiptID != admissionReceiptID ||
						attempt.AttemptID != fixture.command.Attempt.ID ||
						attempt.Status != ingest.RecoveryAttemptStarted ||
						!checkedAt.Equal(fixture.observedAt) {
						t.Fatalf("validator input = %#v %#v %v", snapshot.Receipt, attempt, checkedAt)
					}
					return nil
				},
			)
			if err != nil {
				t.Fatalf("commitForwardRecoveryAction() = %v", err)
			}
			if validatorCalls != 1 || got.State != test.wantState || got.Revision != fixture.command.ReceiptRevision+1 ||
				got.LeaseOwnerID != "" || !got.LeaseExpiresAt.IsZero() {
				t.Fatalf("result = %#v, validator calls = %d", got, validatorCalls)
			}
			if len(fixture.base.creates) != 0 || len(fixture.base.updates) != 2 {
				t.Fatalf("creates/updates = %d/%d, want 0/2", len(fixture.base.creates), len(fixture.base.updates))
			}
			if fixture.base.updates[0].path != admissionReceiptPath() ||
				fixture.base.updates[1].path != fixture.attemptPath {
				t.Fatalf("update paths = %q, %q", fixture.base.updates[0].path, fixture.base.updates[1].path)
			}
			attemptUpdate := firestoreUpdateMap(fixture.base.updates[1].updates)
			if attemptUpdate["status"] != string(ingest.RecoveryAttemptCompleted) ||
				attemptUpdate["outcome"] != string(test.wantOutcome) ||
				attemptUpdate["action"] != string(test.action) ||
				!lowerHexDigest(attemptUpdate["action_hash"].(string)) {
				t.Fatalf("attempt completion updates = %#v", attemptUpdate)
			}
			assertForwardRecoveryActionResult(t, fixture, got, test.action)
		})
	}
}

func TestFirestoreAdmissionStoreRejectsInvalidForwardRecoveryGrantBeforeTransaction(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	transactionCalls := 0
	store := admissionTestStore(fixture.observedAt, func(
		_ context.Context,
		_ func(context.Context, admissionTransaction) error,
	) error {
		transactionCalls++
		return nil
	})

	_, err := store.CommitForwardRecoveryAction(
		context.Background(),
		ingest.ForwardRecoveryActionGrant{},
		fixture.command,
		fixture.observedAt,
	)
	if !errors.Is(err, ingest.ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("CommitForwardRecoveryAction() = %v", err)
	}
	if transactionCalls != 0 {
		t.Fatalf("transaction calls = %d, want 0", transactionCalls)
	}
}

func TestFirestoreAdmissionStoreRequiresExactStartedRecoveryAttempt(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*forwardRecoveryActionFixture)
	}{
		{name: "missing", mutate: func(f *forwardRecoveryActionFixture) { delete(f.transaction.attempts, f.attemptPath) }},
		{name: "completed", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.Status = ingest.RecoveryAttemptCompleted
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "wrong token", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.FencingToken++
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "wrong worker", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.WorkerVersion = "unregistered-worker"
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "prepopulated outcome", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.Outcome = ingest.RecoveryAttemptOutcomeStored
			f.transaction.attempts[f.attemptPath] = attempt
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
			test.mutate(fixture)
			store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
			_, err := store.commitForwardRecoveryAction(
				context.Background(),
				ingest.ForwardRecoveryActionGrant{},
				fixture.command,
				fixture.observedAt,
				func(
					ingest.ForwardRecoveryActionGrant,
					ingest.ForwardRecoveryActionCommand,
					ingest.CurrentForwardRecoverySnapshot,
					ingest.CurrentForwardRecoveryAttempt,
					time.Time,
				) error {
					return nil
				},
			)
			if !errors.Is(err, ingest.ErrInvalidForwardRecoveryActionAuthorization) {
				t.Fatalf("commitForwardRecoveryAction() = %v", err)
			}
			if len(fixture.base.updates) != 0 {
				t.Fatalf("updates = %d, want 0", len(fixture.base.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreReevaluatesForwardRecoveryRelationsBeforeWrites(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))

	_, err := store.commitForwardRecoveryAction(
		context.Background(),
		ingest.ForwardRecoveryActionGrant{},
		fixture.command,
		fixture.observedAt,
		func(
			ingest.ForwardRecoveryActionGrant,
			ingest.ForwardRecoveryActionCommand,
			ingest.CurrentForwardRecoverySnapshot,
			ingest.CurrentForwardRecoveryAttempt,
			time.Time,
		) error {
			return ingest.ErrForwardRecoveryUnauthorized
		},
	)
	if !errors.Is(err, ingest.ErrForwardRecoveryUnauthorized) {
		t.Fatalf("commitForwardRecoveryAction() = %v", err)
	}
	if len(fixture.base.updates) != 0 {
		t.Fatalf("updates = %d, want 0", len(fixture.base.updates))
	}
}

func TestForwardRecoveryActionReadsAllEvidenceBeforeWrites(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
	_, err := store.commitForwardRecoveryAction(
		context.Background(),
		ingest.ForwardRecoveryActionGrant{},
		fixture.command,
		fixture.observedAt,
		func(
			ingest.ForwardRecoveryActionGrant,
			ingest.ForwardRecoveryActionCommand,
			ingest.CurrentForwardRecoverySnapshot,
			ingest.CurrentForwardRecoveryAttempt,
			time.Time,
		) error {
			return nil
		},
	)
	if err != nil {
		t.Fatalf("commitForwardRecoveryAction() = %v", err)
	}
	want := []string{
		"index:" + admissionIdempotencyPath(),
		"index:" + admissionClientBatchPath(),
		"receipt:" + admissionReceiptPath(),
		"relations",
		"attempt:" + fixture.attemptPath,
		"update:" + admissionReceiptPath(),
		"update:" + fixture.attemptPath,
	}
	if !reflect.DeepEqual(fixture.base.calls, want) {
		t.Fatalf("calls = %v, want %v", fixture.base.calls, want)
	}
}

func TestForwardRecoveryActionClonesCallerLineageBeforeValidation(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
	originalRawSHA := fixture.command.Plan.Raw.SHA256
	originalManifestGeneration := fixture.command.Plan.Manifest.Generation
	validatorEntered := make(chan struct{})
	allowValidation := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		_, err := store.commitForwardRecoveryAction(
			context.Background(),
			ingest.ForwardRecoveryActionGrant{},
			fixture.command,
			fixture.observedAt,
			func(
				ingest.ForwardRecoveryActionGrant,
				ingest.ForwardRecoveryActionCommand,
				ingest.CurrentForwardRecoverySnapshot,
				ingest.CurrentForwardRecoveryAttempt,
				time.Time,
			) error {
				close(validatorEntered)
				<-allowValidation
				return nil
			},
		)
		result <- err
	}()
	<-validatorEntered
	fixture.command.Plan.Raw.SHA256 = strings.Repeat("f", 64)
	fixture.command.Plan.Manifest.Generation++
	close(allowValidation)
	if err := <-result; err != nil {
		t.Fatalf("commitForwardRecoveryAction() = %v", err)
	}

	receiptUpdate := firestoreUpdateMap(fixture.base.updates[0].updates)
	if receiptUpdate["object_sha256"] != originalRawSHA ||
		receiptUpdate["manifest_generation"] != originalManifestGeneration {
		t.Fatalf("caller mutation reached receipt updates = %#v", receiptUpdate)
	}
}

func TestValidateReceiptStateRecoveryHoldInvariants(t *testing.T) {
	now := admissionTestNow()
	valid := admissionTestReceiptDTO(admissionTestReceipt(admissionTestReservation(now), ingest.ReceiptReserved))
	valid.State = ingest.ReceiptRecoveryHold
	valid.clearLease()
	valid.UpdatedAt = now.Add(time.Second)
	valid.RecoveryHoldCode = ingest.RecoveryHoldManifestOnly
	valid.RecoveryHoldReviewDueAt = now.Add(time.Hour)
	if err := validateReceiptState(valid); err != nil {
		t.Fatalf("valid hold = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*firestoreIngestReceipt)
	}{
		{name: "unknown code", mutate: func(r *firestoreIngestReceipt) { r.RecoveryHoldCode = "unknown" }},
		{name: "zero review", mutate: func(r *firestoreIngestReceipt) { r.RecoveryHoldReviewDueAt = time.Time{} }},
		{name: "review not future", mutate: func(r *firestoreIngestReceipt) { r.RecoveryHoldReviewDueAt = r.UpdatedAt }},
		{name: "review after artifact expiry", mutate: func(r *firestoreIngestReceipt) { r.RecoveryHoldReviewDueAt = r.ArtifactExpiresAt.Add(time.Nanosecond) }},
		{name: "artifact", mutate: func(r *firestoreIngestReceipt) { r.ObjectPath = "unexpected" }},
		{name: "sample", mutate: func(r *firestoreIngestReceipt) { r.SampleCount = 1 }},
		{name: "last recovery", mutate: func(r *firestoreIngestReceipt) { r.LastRecoveryCode = string(ingest.LeaseReleaseArtifactUnavailable) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := valid
			test.mutate(&receipt)
			if validateReceiptState(receipt) == nil {
				t.Fatalf("invalid hold accepted: %#v", receipt)
			}
		})
	}

	reserved := admissionTestReceiptDTO(admissionTestReceipt(admissionTestReservation(now), ingest.ReceiptReserved))
	reserved.RecoveryHoldCode = ingest.RecoveryHoldManifestOnly
	reserved.RecoveryHoldReviewDueAt = now.Add(time.Hour)
	if validateReceiptState(reserved) == nil {
		t.Fatal("reserved receipt accepted stale hold fields")
	}
}

func TestFirestoreRecoveryAttemptCompletionHasBoundedPrivateShape(t *testing.T) {
	typeOfAttempt := reflect.TypeOf(firestoreRecoveryAttempt{})
	for index := 0; index < typeOfAttempt.NumField(); index++ {
		field := typeOfAttempt.Field(index)
		name := strings.ToLower(field.Name + " " + field.Tag.Get("firestore"))
		for _, forbidden := range []string{
			"firebase_uid", "firebaseuid", "app_id", "appid", "coordinate", "latitude", "longitude",
			"raw_body", "canonical_body", "provider_error", "raw_path", "manifest_path",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("attempt field %s contains forbidden private surface %q", field.Name, forbidden)
			}
		}
	}
}

type forwardRecoveryActionFixture struct {
	base        *fakeAdmissionTransaction
	transaction *fakeForwardRecoveryActionTransaction
	command     ingest.ForwardRecoveryActionCommand
	attemptPath string
	observedAt  time.Time
}

type fakeForwardRecoveryActionTransaction struct {
	*fakeAdmissionTransaction
	snapshot        ingest.CurrentForwardRecoverySnapshot
	attempts        map[string]firestoreRecoveryAttempt
	attemptReadTime time.Time
}

func (tx *fakeForwardRecoveryActionTransaction) LoadCurrentForwardRecoveryRelations(
	_ context.Context,
	_ firestoreIngestReceipt,
) (ingest.CurrentForwardRecoverySnapshot, error) {
	tx.calls = append(tx.calls, "relations")
	return tx.snapshot, nil
}

func (tx *fakeForwardRecoveryActionTransaction) ReadRecoveryAttempt(
	_ context.Context,
	path string,
) (recoveryAttemptRead, bool, error) {
	tx.calls = append(tx.calls, "attempt:"+path)
	attempt, exists := tx.attempts[path]
	return recoveryAttemptRead{Attempt: attempt, ReadTime: tx.attemptReadTime}, exists, nil
}

func newForwardRecoveryActionFixture(
	t *testing.T,
	action ingest.ForwardRecoveryAction,
) *forwardRecoveryActionFixture {
	t.Helper()
	now := admissionTestNow()
	base, receipt := admissionReplayTransaction(t, now, ingest.ReceiptReserved)
	receipt.FencingToken = 2
	receipt.LeaseOwnerID = admissionTakeoverOwnerID
	receipt.LeaseOwnerKind = ingest.LeaseOwnerSweeper
	receipt.LeaseAcquiredAt = now
	receipt.LeaseHeartbeatAt = now
	receipt.LeaseExpiresAt = now.Add(ingest.DefaultRequestLeaseDuration)
	receipt.NextRecoveryAt = receipt.LeaseExpiresAt
	receipt.RecoveryAttemptCount = 1
	receipt.Revision = 2
	receipt.UpdatedAt = now
	base.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
	base.readTime = now.Add(time.Second)
	base.receiptReadTime = now.Add(time.Second)

	attemptProposal := ingest.RecoveryAttemptProposal{
		ID:            admissionTakeoverOwnerID,
		WorkerVersion: ingest.RecoveryWorkerVersion,
	}
	attempt := newFirestoreRecoveryAttempt(
		attemptProposal,
		receipt.TenantID,
		receipt.ReceiptID,
		ingest.LeaseOwnerSweeper,
		receipt.FencingToken,
		now,
	)
	attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, attempt.AttemptID)
	transaction := &fakeForwardRecoveryActionTransaction{
		fakeAdmissionTransaction: base,
		snapshot:                 ingest.CurrentForwardRecoverySnapshot{ReadTime: now.Add(time.Second)},
		attempts:                 map[string]firestoreRecoveryAttempt{attemptPath: attempt},
		attemptReadTime:          now.Add(time.Second),
	}
	command := forwardRecoveryActionCommand(t, receipt, attemptProposal, action)
	return &forwardRecoveryActionFixture{
		base: base, transaction: transaction, command: command,
		attemptPath: attemptPath, observedAt: now.Add(2 * time.Second),
	}
}

func forwardRecoveryActionCommand(
	t *testing.T,
	receipt ingest.Receipt,
	attempt ingest.RecoveryAttemptProposal,
	action ingest.ForwardRecoveryAction,
) ingest.ForwardRecoveryActionCommand {
	t.Helper()
	stored := admissionStoredReceiptData(admissionTestReservation(receipt.CreatedAt))
	raw := artifactLineageFromStoredData(stored.Artifacts.Object)
	manifest := artifactLineageFromStoredData(stored.Artifacts.Manifest)
	command := ingest.ForwardRecoveryActionCommand{
		TenantID:        receipt.TenantID,
		ReservationKey:  receipt.ReservationKey,
		Attempt:         attempt,
		ReceiptRevision: receipt.Revision,
		Fence: ingest.LeaseFence{
			OwnerID: attempt.ID, Token: receipt.FencingToken, ExpiresAt: receipt.LeaseExpiresAt,
		},
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
		command.HoldReviewDueAt = receipt.CreatedAt.Add(ingest.DefaultRecoveryHoldReviewWindow)
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

func artifactLineageFromStoredData(stored ingest.StoredArtifact) ingest.ArtifactLineage {
	return ingest.ArtifactLineage{
		Path: stored.Path, SHA256: stored.SHA256, CRC32C: stored.CRC32C,
		Size: stored.Size, Generation: stored.Generation, Metageneration: stored.Metageneration,
	}
}

func firestoreUpdateMap(updates []firestore.Update) map[string]any {
	result := make(map[string]any, len(updates))
	for _, update := range updates {
		result[update.Path] = update.Value
	}
	return result
}

func assertForwardRecoveryActionResult(
	t *testing.T,
	fixture *forwardRecoveryActionFixture,
	receipt ingest.Receipt,
	action ingest.ForwardRecoveryAction,
) {
	t.Helper()
	switch action {
	case ingest.ForwardRecoveryActionMarkStored:
		if receipt.ObjectPath == "" || receipt.ManifestPath == "" ||
			receipt.SampleCount != receipt.ExpectedSampleCount {
			t.Fatalf("stored receipt = %#v", receipt)
		}
	case ingest.ForwardRecoveryActionMarkRejected:
		if receipt.RejectionCode != "object_conflict" || receipt.ObjectPath != "" || receipt.SampleCount != 0 {
			t.Fatalf("rejected receipt = %#v", receipt)
		}
	case ingest.ForwardRecoveryActionMarkHold:
		if receipt.RecoveryHoldCode != ingest.RecoveryHoldManifestOnly ||
			!receipt.RecoveryHoldReviewDueAt.Equal(fixture.command.HoldReviewDueAt) || receipt.ObjectPath != "" {
			t.Fatalf("hold receipt = %#v", receipt)
		}
	case ingest.ForwardRecoveryActionReleaseLease:
		if receipt.LastRecoveryCode != string(ingest.LeaseReleaseAwaitingClientReplay) ||
			!receipt.NextRecoveryAt.Equal(fixture.observedAt.Add(ingest.InitialRecoveryBackoff)) {
			t.Fatalf("released receipt = %#v", receipt)
		}
	}
}
