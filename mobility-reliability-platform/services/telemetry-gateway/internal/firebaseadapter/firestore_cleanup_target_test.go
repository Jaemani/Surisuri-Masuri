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

func TestFirestoreCleanupTargetCreatesValidImmutableTarget(t *testing.T) {
	fixture := newCleanupTargetAdapterFixture(t)

	target, createStatus, err := fixture.store.createCleanupDryRunTarget(
		context.Background(),
		ingest.CleanupTargetAuthorizationGrant{},
		fixture.command,
		fixture.observedAt,
		validateCleanupTargetAdapterSnapshot,
	)
	if err != nil {
		t.Fatalf("createCleanupDryRunTarget() = %v", err)
	}
	if createStatus != ingest.CleanupTargetCreated {
		t.Fatalf("create status = %q, want %q", createStatus, ingest.CleanupTargetCreated)
	}
	wantHash, err := ingest.CleanupTargetHash(fixture.command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	if target.TargetHash != wantHash || !reflect.DeepEqual(target.Command, fixture.command) {
		t.Fatalf("created target = %#v, want command hash %s", target, wantHash)
	}
	if len(fixture.transaction.creates) != 1 || len(fixture.transaction.updates) != 0 {
		t.Fatalf(
			"transaction creates/updates = %d/%d, want 1/0",
			len(fixture.transaction.creates),
			len(fixture.transaction.updates),
		)
	}
	created := fixture.transaction.creates[0]
	if created.path != fixture.targetPath {
		t.Fatalf("created path = %q, want %q", created.path, fixture.targetPath)
	}
	persisted, ok := created.value.(firestoreIngestCleanupTarget)
	if !ok {
		t.Fatalf("created value type = %T, want firestoreIngestCleanupTarget", created.value)
	}
	roundTripped, err := persisted.toDomain()
	if err != nil {
		t.Fatalf("created target toDomain() = %v", err)
	}
	if roundTripped.TargetHash != wantHash || !reflect.DeepEqual(roundTripped.Command, fixture.command) {
		t.Fatalf("persisted target round trip = %#v", roundTripped)
	}
}

func TestFirestoreCleanupTargetExactReplayWritesNothing(t *testing.T) {
	fixture := newCleanupTargetAdapterFixture(t)
	fixture.seedExactTarget(t)

	target, createStatus, err := fixture.store.createCleanupDryRunTarget(
		context.Background(),
		ingest.CleanupTargetAuthorizationGrant{},
		fixture.command,
		fixture.observedAt,
		validateCleanupTargetAdapterSnapshot,
	)
	if err != nil {
		t.Fatalf("createCleanupDryRunTarget() = %v", err)
	}
	if createStatus != ingest.CleanupTargetReplayed {
		t.Fatalf("create status = %q, want %q", createStatus, ingest.CleanupTargetReplayed)
	}
	wantHash, _ := ingest.CleanupTargetHash(fixture.command)
	if target.TargetHash != wantHash || !reflect.DeepEqual(target.Command, fixture.command) {
		t.Fatalf("replayed target = %#v", target)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupTargetConflictingTargetWritesNothing(t *testing.T) {
	fixture := newCleanupTargetAdapterFixture(t)
	conflicting := cloneCleanupTargetCommand(fixture.command)
	conflicting.CreatedAt = conflicting.CreatedAt.Add(time.Nanosecond)
	conflictingHash, err := ingest.CleanupTargetHash(conflicting)
	if err != nil {
		t.Fatalf("CleanupTargetHash(conflicting) = %v", err)
	}
	fixture.transaction.targets[fixture.targetPath] = cleanupTargetRead{
		Target:   newFirestoreCleanupTarget(conflicting, conflictingHash),
		ReadTime: fixture.observedAt,
	}

	target, createStatus, err := fixture.store.createCleanupDryRunTarget(
		context.Background(),
		ingest.CleanupTargetAuthorizationGrant{},
		fixture.command,
		fixture.observedAt,
		validateCleanupTargetAdapterSnapshot,
	)
	if !errors.Is(err, ingest.ErrCleanupTargetConflict) {
		t.Fatalf("createCleanupDryRunTarget() = %v, want %v", err, ingest.ErrCleanupTargetConflict)
	}
	if target != (ingest.CleanupTarget{}) || createStatus != "" {
		t.Fatalf("conflicting result = %#v, %q, want zero", target, createStatus)
	}
	fixture.assertNoWrites(t)
}

func TestFirestoreCleanupTargetRejectsStaleRevisionAndFence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*cleanupTargetAdapterFixture)
	}{
		{
			name: "receipt revision",
			mutate: func(fixture *cleanupTargetAdapterFixture) {
				receipt := fixture.transaction.receipts[admissionReceiptPath()]
				receipt.Revision++
				fixture.transaction.receipts[admissionReceiptPath()] = receipt
			},
		},
		{
			name: "receipt and attempt fence",
			mutate: func(fixture *cleanupTargetAdapterFixture) {
				receipt := fixture.transaction.receipts[admissionReceiptPath()]
				receipt.FencingToken++
				fixture.transaction.receipts[admissionReceiptPath()] = receipt
				attempt := fixture.transaction.attempts[fixture.attemptPath]
				attempt.FencingToken = receipt.FencingToken
				fixture.transaction.attempts[fixture.attemptPath] = attempt
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCleanupTargetAdapterFixture(t)
			test.mutate(fixture)

			target, createStatus, err := fixture.store.createCleanupDryRunTarget(
				context.Background(),
				ingest.CleanupTargetAuthorizationGrant{},
				fixture.command,
				fixture.observedAt,
				validateCleanupTargetAdapterSnapshot,
			)
			if !errors.Is(err, ingest.ErrInvalidCleanupTarget) {
				t.Fatalf("createCleanupDryRunTarget() = %v, want %v", err, ingest.ErrInvalidCleanupTarget)
			}
			if target != (ingest.CleanupTarget{}) || createStatus != "" {
				t.Fatalf("stale result = %#v, %q, want zero", target, createStatus)
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestFirestoreCleanupTargetRejectsMissingMalformedAndTerminalAttempt(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*cleanupTargetAdapterFixture)
	}{
		{
			name: "missing",
			mutate: func(fixture *cleanupTargetAdapterFixture) {
				delete(fixture.transaction.attempts, fixture.attemptPath)
			},
		},
		{
			name: "malformed fence",
			mutate: func(fixture *cleanupTargetAdapterFixture) {
				attempt := fixture.transaction.attempts[fixture.attemptPath]
				attempt.FencingToken++
				fixture.transaction.attempts[fixture.attemptPath] = attempt
			},
		},
		{
			name: "terminal completed",
			mutate: func(fixture *cleanupTargetAdapterFixture) {
				attempt := fixture.transaction.attempts[fixture.attemptPath]
				attempt.Status = ingest.RecoveryAttemptCompleted
				attempt.CompletedAt = fixture.observedAt
				fixture.transaction.attempts[fixture.attemptPath] = attempt
			},
		},
		{
			name: "started with terminal residue",
			mutate: func(fixture *cleanupTargetAdapterFixture) {
				attempt := fixture.transaction.attempts[fixture.attemptPath]
				attempt.Outcome = ingest.RecoveryAttemptOutcomeStored
				fixture.transaction.attempts[fixture.attemptPath] = attempt
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCleanupTargetAdapterFixture(t)
			test.mutate(fixture)

			target, createStatus, err := fixture.store.createCleanupDryRunTarget(
				context.Background(),
				ingest.CleanupTargetAuthorizationGrant{},
				fixture.command,
				fixture.observedAt,
				validateCleanupTargetAdapterSnapshot,
			)
			if !errors.Is(err, ingest.ErrInvalidCleanupTarget) {
				t.Fatalf("createCleanupDryRunTarget() = %v, want %v", err, ingest.ErrInvalidCleanupTarget)
			}
			if target != (ingest.CleanupTarget{}) || createStatus != "" {
				t.Fatalf("invalid attempt result = %#v, %q, want zero", target, createStatus)
			}
			fixture.assertNoWrites(t)
		})
	}
}

func TestFirestoreCleanupTargetResetsOuterResultAcrossTransactionRetry(t *testing.T) {
	fixture := newCleanupTargetAdapterFixture(t)
	first := fixture.transaction
	first.createErr = errors.New("synthetic transaction retry")
	secondFixture := newCleanupTargetAdapterFixture(t)
	secondFixture.seedExactTarget(t)
	second := secondFixture.transaction
	transactionCalls := 0
	store := &FirestoreAdmissionStore{
		runTransaction: func(
			ctx context.Context,
			operation func(context.Context, admissionTransaction) error,
		) error {
			transactionCalls++
			if firstErr := operation(ctx, first); firstErr == nil {
				return errors.New("first callback unexpectedly succeeded")
			}
			transactionCalls++
			return operation(ctx, second)
		},
	}

	target, createStatus, err := store.createCleanupDryRunTarget(
		context.Background(),
		ingest.CleanupTargetAuthorizationGrant{},
		fixture.command,
		fixture.observedAt,
		validateCleanupTargetAdapterSnapshot,
	)
	if err != nil {
		t.Fatalf("createCleanupDryRunTarget() = %v", err)
	}
	if transactionCalls != 2 || createStatus != ingest.CleanupTargetReplayed {
		t.Fatalf("transaction calls/status = %d/%q, want 2/%q", transactionCalls, createStatus, ingest.CleanupTargetReplayed)
	}
	wantHash, _ := ingest.CleanupTargetHash(fixture.command)
	if target.TargetHash != wantHash || !reflect.DeepEqual(target.Command, fixture.command) {
		t.Fatalf("retry result = %#v", target)
	}
	if len(first.creates) != 1 || len(first.updates) != 0 {
		t.Fatalf("first retry callback creates/updates = %d/%d, want 1/0", len(first.creates), len(first.updates))
	}
	secondFixture.assertNoWrites(t)
}

func TestFirestoreCleanupTargetPersistenceHashRoundTrip(t *testing.T) {
	fixture := newCleanupTargetAdapterFixture(t)
	targetHash, err := ingest.CleanupTargetHash(fixture.command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	persisted := newFirestoreCleanupTarget(fixture.command, targetHash)

	roundTripped, err := persisted.toDomain()
	if err != nil {
		t.Fatalf("toDomain() = %v", err)
	}
	if roundTripped.TargetHash != targetHash || !reflect.DeepEqual(roundTripped.Command, fixture.command) {
		t.Fatalf("round-tripped target = %#v", roundTripped)
	}
	recomputed, err := ingest.CleanupTargetHash(roundTripped.Command)
	if err != nil || recomputed != targetHash {
		t.Fatalf("round-tripped hash = %q, %v, want %q", recomputed, err, targetHash)
	}
}

type cleanupTargetAdapterFixture struct {
	transaction *fakeCleanupTargetTransaction
	store       *FirestoreAdmissionStore
	command     ingest.CleanupTargetCommand
	attemptPath string
	targetPath  string
	observedAt  time.Time
}

func newCleanupTargetAdapterFixture(t *testing.T) *cleanupTargetAdapterFixture {
	t.Helper()
	observedAt := admissionTestNow()
	transitionedAt := observedAt.Add(-ingest.DefaultCleanupLateWriteGrace - time.Minute)
	base, receipt := admissionCleanupPendingTransaction(t, transitionedAt)
	proposal := ingest.CleanupAttemptProposal{
		ID: admissionLeaseOwnerID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	configureActiveCleanupLease(&receipt, proposal, receipt.CleanupQuiescenceUntil)
	attempt := newFirestoreCleanupAttempt(
		proposal,
		receipt.TenantID,
		receipt.ReceiptID,
		receipt.FencingToken,
		receipt.LeaseAcquiredAt,
	)
	attemptPath := recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, proposal.ID)
	transaction := &fakeCleanupTargetTransaction{
		readTime: observedAt,
		indexes: map[string]firestoreIngestIndex{
			admissionIdempotencyPath(): base.indexes[admissionIdempotencyPath()],
			admissionClientBatchPath(): base.indexes[admissionClientBatchPath()],
		},
		receipts: map[string]firestoreIngestReceipt{admissionReceiptPath(): receipt},
		attempts: map[string]firestoreRecoveryAttempt{attemptPath: attempt},
		targets:  make(map[string]cleanupTargetRead),
	}
	stored := admissionStoredReceiptData(admissionTestReservation(receipt.CreatedAt))
	raw := artifactLineageFromStoredData(stored.Artifacts.Object)
	manifest := artifactLineageFromStoredData(stored.Artifacts.Manifest)
	completeOne := ingest.ArtifactInventorySummary{
		Performed: true, NonSoftDeletedCount: 1,
		Coverage: ingest.ArtifactInventoryCoverageComplete,
	}
	command := ingest.CleanupTargetCommand{
		SchemaVersion: ingest.CleanupTargetSchemaVersion,
		CleanupID:     proposal.ID, TenantID: receipt.TenantID, ReceiptID: receipt.ReceiptID,
		ReservationKey: receipt.ReservationKey, AttemptID: proposal.ID,
		Mode: receipt.CleanupMode, OriginStatus: receipt.CleanupOriginStatus,
		CleanupPolicyVersion:   receipt.CleanupPolicyVersion,
		CleanupTransitionedAt:  receipt.CleanupTransitionedAt,
		CleanupQuiescenceUntil: receipt.CleanupQuiescenceUntil,
		ReceiptRevision:        receipt.Revision, FencingToken: receipt.FencingToken,
		LeaseAcquiredAt: receipt.LeaseAcquiredAt, LeaseHeartbeatAt: receipt.LeaseHeartbeatAt,
		LeaseExpiresAt: receipt.LeaseExpiresAt, WorkerVersion: ingest.CleanupWorkerVersion,
		Status: ingest.CleanupTargetStatusPlanned, Decision: ingest.CleanupTargetDeleteCandidate,
		Classification:    ingest.ArtifactClassificationValidComplete,
		ReasonCode:        ingest.ArtifactReasonManifestAndReferencedRawValid,
		RetentionPhase:    ingest.ArtifactRetentionBeforeExpiry,
		ValidatorVersion:  receipt.ValidatorVersion,
		ClassifiedAt:      observedAt.Add(-30 * time.Second),
		ManifestInventory: completeOne, RawInventory: completeOne,
		Raw: &raw, Manifest: &manifest, CreatedAt: observedAt.Add(-15 * time.Second),
	}
	if err := ingest.ValidateCleanupTargetCommand(command); err != nil {
		t.Fatalf("cleanup target fixture command = %v", err)
	}
	targetPath := cleanupTargetDocumentPath(command.TenantID, command.CleanupID)
	return &cleanupTargetAdapterFixture{
		transaction: transaction,
		store: &FirestoreAdmissionStore{
			runTransaction: func(
				ctx context.Context,
				operation func(context.Context, admissionTransaction) error,
			) error {
				return operation(ctx, transaction)
			},
		},
		command: command, attemptPath: attemptPath, targetPath: targetPath, observedAt: observedAt,
	}
}

func (fixture *cleanupTargetAdapterFixture) seedExactTarget(t *testing.T) {
	t.Helper()
	targetHash, err := ingest.CleanupTargetHash(fixture.command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	fixture.transaction.targets[fixture.targetPath] = cleanupTargetRead{
		Target: newFirestoreCleanupTarget(fixture.command, targetHash), ReadTime: fixture.observedAt,
	}
}

func (fixture *cleanupTargetAdapterFixture) assertNoWrites(t *testing.T) {
	t.Helper()
	if len(fixture.transaction.creates) != 0 || len(fixture.transaction.updates) != 0 {
		t.Fatalf(
			"transaction creates/updates = %d/%d, want 0/0",
			len(fixture.transaction.creates),
			len(fixture.transaction.updates),
		)
	}
}

func validateCleanupTargetAdapterSnapshot(
	_ ingest.CleanupTargetAuthorizationGrant,
	command ingest.CleanupTargetCommand,
	snapshot ingest.CurrentCleanupSnapshot,
	observedAt time.Time,
) error {
	receipt := snapshot.Receipt
	attempt := snapshot.Attempt
	if snapshot.ReadTime.IsZero() || observedAt.IsZero() ||
		receipt.TenantID != command.TenantID || receipt.ReceiptID != command.ReceiptID ||
		receipt.ReservationKey != command.ReservationKey ||
		receipt.State != ingest.ReceiptCleanupPending || receipt.Revision != command.ReceiptRevision ||
		receipt.CleanupMode != command.Mode || receipt.CleanupOriginStatus != command.OriginStatus ||
		receipt.CleanupPolicyVersion != command.CleanupPolicyVersion ||
		!receipt.CleanupTransitionedAt.Equal(command.CleanupTransitionedAt) ||
		!receipt.CleanupQuiescenceUntil.Equal(command.CleanupQuiescenceUntil) ||
		receipt.LeaseOwnerKind != ingest.LeaseOwnerCleanup || receipt.LeaseOwnerID != command.AttemptID ||
		receipt.FencingToken != command.FencingToken ||
		!receipt.LeaseAcquiredAt.Equal(command.LeaseAcquiredAt) ||
		!receipt.LeaseHeartbeatAt.Equal(command.LeaseHeartbeatAt) ||
		!receipt.LeaseExpiresAt.Equal(command.LeaseExpiresAt) ||
		attempt.AttemptID != command.AttemptID || attempt.TenantID != command.TenantID ||
		attempt.ReceiptID != command.ReceiptID || attempt.OwnerKind != ingest.LeaseOwnerCleanup ||
		attempt.FencingToken != command.FencingToken || attempt.WorkerVersion != command.WorkerVersion ||
		attempt.Status != ingest.RecoveryAttemptStarted ||
		!attempt.StartedAt.Equal(command.LeaseAcquiredAt) ||
		observedAt.Before(command.CleanupQuiescenceUntil) || !observedAt.Before(command.LeaseExpiresAt) {
		return ingest.ErrInvalidCleanupTarget
	}
	return nil
}

type fakeCleanupTargetTransaction struct {
	readTime  time.Time
	indexes   map[string]firestoreIngestIndex
	receipts  map[string]firestoreIngestReceipt
	attempts  map[string]firestoreRecoveryAttempt
	targets   map[string]cleanupTargetRead
	creates   []cleanupTargetAdapterCreate
	updates   []cleanupTargetAdapterUpdate
	createErr error
}

type cleanupTargetAdapterCreate struct {
	path  string
	value any
}

type cleanupTargetAdapterUpdate struct {
	path    string
	updates []firestore.Update
}

func (transaction *fakeCleanupTargetTransaction) LoadAuthorization(
	context.Context,
	ingest.Principal,
	ingest.BatchScope,
) (authorizationRead, error) {
	return authorizationRead{}, ingest.ErrAdmissionUnavailable
}

func (transaction *fakeCleanupTargetTransaction) ReadIndex(
	_ context.Context,
	path string,
) (firestoreIngestIndex, bool, error) {
	value, exists := transaction.indexes[path]
	return value, exists, nil
}

func (transaction *fakeCleanupTargetTransaction) ReadReceipt(
	_ context.Context,
	path string,
) (receiptRead, bool, error) {
	value, exists := transaction.receipts[path]
	return receiptRead{Receipt: value, ReadTime: transaction.readTime}, exists, nil
}

func (transaction *fakeCleanupTargetTransaction) ReadRecoveryAttempt(
	_ context.Context,
	path string,
) (recoveryAttemptRead, bool, error) {
	value, exists := transaction.attempts[path]
	return recoveryAttemptRead{Attempt: value, ReadTime: transaction.readTime}, exists, nil
}

func (transaction *fakeCleanupTargetTransaction) ReadCleanupTarget(
	_ context.Context,
	path string,
) (cleanupTargetRead, bool, error) {
	value, exists := transaction.targets[path]
	return value, exists, nil
}

func (transaction *fakeCleanupTargetTransaction) Create(
	_ context.Context,
	path string,
	value any,
) error {
	transaction.creates = append(transaction.creates, cleanupTargetAdapterCreate{path: path, value: value})
	if transaction.createErr != nil {
		return transaction.createErr
	}
	if target, ok := value.(firestoreIngestCleanupTarget); ok {
		transaction.targets[path] = cleanupTargetRead{Target: target, ReadTime: transaction.readTime}
	}
	return nil
}

func (transaction *fakeCleanupTargetTransaction) Update(
	_ context.Context,
	path string,
	updates []firestore.Update,
) error {
	transaction.updates = append(transaction.updates, cleanupTargetAdapterUpdate{
		path: path, updates: append([]firestore.Update(nil), updates...),
	})
	return nil
}
