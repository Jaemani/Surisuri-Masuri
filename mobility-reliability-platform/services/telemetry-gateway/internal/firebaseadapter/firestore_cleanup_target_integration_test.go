package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorLoadsCurrentCleanupExecutionTarget(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, receiptBefore, attemptBefore := seedClaimedCleanupTargetFixture(t, client, now)
	command := cleanupTargetCommandFixture(t, receiptBefore, ingest.ArtifactClassificationValidComplete)
	target, status, err := store.createCleanupDryRunTarget(
		context.Background(),
		ingest.CleanupTargetAuthorizationGrant{},
		command,
		command.CreatedAt,
		exactCleanupTargetSnapshotValidator(receiptBefore, attemptBefore),
	)
	if err != nil || status != ingest.CleanupTargetCreated {
		t.Fatalf("createCleanupDryRunTarget() = %#v, %q, %v", target, status, err)
	}

	snapshot, err := store.LoadCurrentCleanupExecution(context.Background(), ingest.CleanupExecutionQuery{
		TenantID:       receiptBefore.TenantID,
		ReservationKey: receiptBefore.ReservationKey,
		AttemptID:      attemptBefore.AttemptID,
	})
	if err != nil {
		t.Fatalf("LoadCurrentCleanupExecution() = %v", err)
	}
	if snapshot.ReadTime.IsZero() || snapshot.Receipt.ReceiptID != receiptBefore.ReceiptID ||
		snapshot.Attempt.AttemptID != attemptBefore.AttemptID ||
		snapshot.Target.TargetHash != target.TargetHash ||
		!reflect.DeepEqual(snapshot.Target.Command, command) {
		t.Fatalf("cleanup execution snapshot = %#v", snapshot)
	}
	plan, grant, err := store.AuthorizeCleanupExecution(context.Background(), ingest.CleanupExecutionQuery{
		TenantID:       receiptBefore.TenantID,
		ReservationKey: receiptBefore.ReservationKey,
		AttemptID:      attemptBefore.AttemptID,
	})
	if err != nil || plan.Target.TargetHash != target.TargetHash ||
		ValidateCleanupExecutionAuthorization(grant, plan, now) != nil ||
		!grant.expiresAt.After(grant.checkedAt) ||
		grant.expiresAt.Sub(grant.checkedAt) > CleanupExecutionGrantTTL ||
		grant.expiresAt.After(plan.Target.Command.LeaseExpiresAt) {
		t.Fatalf("AuthorizeCleanupExecution() = %#v, %#v, %v", plan, grant, err)
	}
	assertCleanupControlDocumentsUnchanged(t, client, receiptBefore, attemptBefore)
}

func TestFirestoreAdmissionStoreEmulatorRejectsMissingOrMalformedCleanupExecutionTarget(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, receiptBefore, attemptBefore := seedClaimedCleanupTargetFixture(t, client, now)
	query := ingest.CleanupExecutionQuery{
		TenantID:       receiptBefore.TenantID,
		ReservationKey: receiptBefore.ReservationKey,
		AttemptID:      attemptBefore.AttemptID,
	}

	_, err := store.LoadCurrentCleanupExecution(context.Background(), query)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) {
		t.Fatalf("missing target error = %v", err)
	}
	plan, grant, err := store.AuthorizeCleanupExecution(context.Background(), query)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) ||
		plan != (ingest.CleanupExecutionPlan{}) || grant != (CleanupExecutionAuthorizationGrant{}) {
		t.Fatalf("missing target authorization = %#v, %#v, %v", plan, grant, err)
	}
	command := cleanupTargetCommandFixture(t, receiptBefore, ingest.ArtifactClassificationValidComplete)
	targetHash, err := ingest.CleanupTargetHash(command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	malformed := newFirestoreCleanupTarget(command, targetHash)
	malformed.TargetHash = strings.Repeat("f", 64)
	if _, err := client.Doc(cleanupTargetDocumentPath(
		receiptBefore.TenantID,
		attemptBefore.AttemptID,
	)).Create(context.Background(), malformed); err != nil {
		t.Fatalf("create malformed target: %v", err)
	}
	_, err = store.LoadCurrentCleanupExecution(context.Background(), query)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnavailable) {
		t.Fatalf("malformed target error = %v", err)
	}
	plan, grant, err = store.AuthorizeCleanupExecution(context.Background(), query)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnavailable) ||
		plan != (ingest.CleanupExecutionPlan{}) || grant != (CleanupExecutionAuthorizationGrant{}) {
		t.Fatalf("malformed target authorization = %#v, %#v, %v", plan, grant, err)
	}
	assertCleanupControlDocumentsUnchanged(t, client, receiptBefore, attemptBefore)
}

func TestFirestoreAdmissionStoreEmulatorRejectsStaleCleanupExecutionControlState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *firestore.Client, firestoreIngestReceipt, firestoreRecoveryAttempt)
	}{
		{
			name: "receipt revision",
			mutate: func(t *testing.T, client *firestore.Client, receipt firestoreIngestReceipt, _ firestoreRecoveryAttempt) {
				t.Helper()
				if _, err := client.Doc(receiptDocumentPath(receipt.TenantID, receipt.ReceiptID)).Update(
					context.Background(), []firestore.Update{{Path: "revision", Value: receipt.Revision + 1}},
				); err != nil {
					t.Fatalf("advance receipt revision: %v", err)
				}
			},
		},
		{
			name: "receipt fence",
			mutate: func(t *testing.T, client *firestore.Client, receipt firestoreIngestReceipt, _ firestoreRecoveryAttempt) {
				t.Helper()
				if _, err := client.Doc(receiptDocumentPath(receipt.TenantID, receipt.ReceiptID)).Update(
					context.Background(), []firestore.Update{{Path: "fencing_token", Value: receipt.FencingToken + 1}},
				); err != nil {
					t.Fatalf("advance receipt fence: %v", err)
				}
			},
		},
		{
			name: "terminal attempt",
			mutate: func(t *testing.T, client *firestore.Client, receipt firestoreIngestReceipt, attempt firestoreRecoveryAttempt) {
				t.Helper()
				if _, err := client.Doc(recoveryAttemptDocumentPath(
					receipt.TenantID, receipt.ReceiptID, attempt.AttemptID,
				)).Update(context.Background(), []firestore.Update{{
					Path: "status", Value: ingest.RecoveryAttemptCompleted,
				}}); err != nil {
					t.Fatalf("complete cleanup attempt: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newAdmissionEmulatorClient(t)
			clearAdmissionIngestCollections(t, client)
			now := time.Now().UTC().Truncate(time.Millisecond)
			store, receipt, attempt := seedClaimedCleanupTargetFixture(t, client, now)
			command := cleanupTargetCommandFixture(t, receipt, ingest.ArtifactClassificationValidComplete)
			if _, status, err := store.createCleanupDryRunTarget(
				context.Background(), ingest.CleanupTargetAuthorizationGrant{}, command,
				command.CreatedAt, exactCleanupTargetSnapshotValidator(receipt, attempt),
			); err != nil || status != ingest.CleanupTargetCreated {
				t.Fatalf("createCleanupDryRunTarget() status=%q err=%v", status, err)
			}
			test.mutate(t, client, receipt, attempt)
			plan, grant, err := store.AuthorizeCleanupExecution(
				context.Background(), ingest.CleanupExecutionQuery{
					TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
					AttemptID: attempt.AttemptID,
				},
			)
			if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) ||
				plan != (ingest.CleanupExecutionPlan{}) || grant != (CleanupExecutionAuthorizationGrant{}) {
				t.Fatalf("stale control authorization = %#v, %#v, %v", plan, grant, err)
			}
		})
	}
}

func TestFirestoreAdmissionStoreEmulatorConcurrentCleanupTargetCreateConverges(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, receiptBefore, attemptBefore := seedClaimedCleanupTargetFixture(t, client, now)
	command := cleanupTargetCommandFixture(t, receiptBefore, ingest.ArtifactClassificationValidComplete)
	wantHash, err := ingest.CleanupTargetHash(command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	validateCurrent := exactCleanupTargetSnapshotValidator(receiptBefore, attemptBefore)

	type createResult struct {
		target ingest.CleanupTarget
		status ingest.CleanupTargetCreateStatus
		err    error
	}
	start := make(chan struct{})
	results := make(chan createResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			target, status, createErr := store.createCleanupDryRunTarget(
				context.Background(),
				ingest.CleanupTargetAuthorizationGrant{},
				command,
				command.CreatedAt,
				validateCurrent,
			)
			results <- createResult{target: target, status: status, err: createErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	statusCounts := make(map[ingest.CleanupTargetCreateStatus]int)
	for result := range results {
		if result.err != nil {
			t.Fatalf("createCleanupDryRunTarget() = %v", result.err)
		}
		statusCounts[result.status]++
		if result.target.TargetHash != wantHash || !reflect.DeepEqual(result.target.Command, command) {
			t.Fatalf("target = %#v, want command/hash %#v/%q", result.target, command, wantHash)
		}
	}
	if statusCounts[ingest.CleanupTargetCreated] != 1 ||
		statusCounts[ingest.CleanupTargetReplayed] != 1 {
		t.Fatalf("target create statuses = %#v, want one created and one replayed", statusCounts)
	}

	targets, err := client.Collection(
		"tenants/" + receiptBefore.TenantID + "/ingestCleanupTargets",
	).Documents(context.Background()).GetAll()
	if err != nil {
		t.Fatalf("list cleanup targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("cleanup target count = %d, want 1", len(targets))
	}
	persisted := readCleanupTargetEmulator(t, client, receiptBefore.TenantID, command.CleanupID)
	domainTarget, err := persisted.toDomain()
	if err != nil || domainTarget.TargetHash != wantHash ||
		!reflect.DeepEqual(domainTarget.Command, command) {
		t.Fatalf("persisted cleanup target = %#v, %v", persisted, err)
	}
	assertCleanupControlDocumentsUnchanged(t, client, receiptBefore, attemptBefore)
}

func TestFirestoreAdmissionStoreEmulatorConflictingCleanupTargetWritesNothing(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	clearAdmissionIngestCollections(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, receiptBefore, attemptBefore := seedClaimedCleanupTargetFixture(t, client, now)
	command := cleanupTargetCommandFixture(t, receiptBefore, ingest.ArtifactClassificationValidComplete)
	conflictingCommand := cleanupTargetCommandFixture(
		t,
		receiptBefore,
		ingest.ArtifactClassificationValidRawOnly,
	)
	conflictingHash, err := ingest.CleanupTargetHash(conflictingCommand)
	if err != nil {
		t.Fatalf("CleanupTargetHash(conflict) = %v", err)
	}
	conflictingTarget := newFirestoreCleanupTarget(conflictingCommand, conflictingHash)
	if _, err := client.Doc(cleanupTargetDocumentPath(
		receiptBefore.TenantID,
		conflictingCommand.CleanupID,
	)).Create(context.Background(), conflictingTarget); err != nil {
		t.Fatalf("preseed conflicting cleanup target: %v", err)
	}
	targetBefore := readCleanupTargetEmulator(
		t,
		client,
		receiptBefore.TenantID,
		conflictingCommand.CleanupID,
	)

	target, status, err := store.createCleanupDryRunTarget(
		context.Background(),
		ingest.CleanupTargetAuthorizationGrant{},
		command,
		command.CreatedAt,
		exactCleanupTargetSnapshotValidator(receiptBefore, attemptBefore),
	)
	if !errors.Is(err, ingest.ErrCleanupTargetConflict) || status != "" ||
		target != (ingest.CleanupTarget{}) {
		t.Fatalf("createCleanupDryRunTarget() = %#v, %q, %v, want conflict", target, status, err)
	}
	targetAfter := readCleanupTargetEmulator(
		t,
		client,
		receiptBefore.TenantID,
		conflictingCommand.CleanupID,
	)
	if !reflect.DeepEqual(targetAfter, targetBefore) {
		t.Fatalf("conflicting target changed: before=%#v after=%#v", targetBefore, targetAfter)
	}
	targets, err := client.Collection(
		"tenants/" + receiptBefore.TenantID + "/ingestCleanupTargets",
	).Documents(context.Background()).GetAll()
	if err != nil {
		t.Fatalf("list cleanup targets after conflict: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("cleanup target count after conflict = %d, want 1", len(targets))
	}
	assertCleanupControlDocumentsUnchanged(t, client, receiptBefore, attemptBefore)
}

func seedClaimedCleanupTargetFixture(
	t *testing.T,
	client *firestore.Client,
	now time.Time,
) (*FirestoreAdmissionStore, firestoreIngestReceipt, firestoreRecoveryAttempt) {
	t.Helper()
	pending := seedCleanupPendingReservation(t, client, now.Add(-time.Second))
	store, err := NewFirestoreAdmissionStore(
		client,
		emulatorTransactionTimout,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewFirestoreAdmissionStore() = %v", err)
	}
	attempt := ingest.CleanupAttemptProposal{
		ID: emulatorSecondReceiptID, WorkerVersion: ingest.CleanupWorkerVersion,
	}
	grant, status, err := store.ClaimCleanupLease(
		context.Background(),
		pending.TenantID,
		pending.ReservationKey,
		ingest.LeaseOwner{ID: attempt.ID, Kind: ingest.LeaseOwnerCleanup},
		attempt,
		now,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil || status != ingest.LeaseStatusAcquired ||
		ingest.ValidateCleanupLeaseGrant(grant) != nil {
		t.Fatalf("ClaimCleanupLease() = %#v, %q, %v", grant, status, err)
	}
	receipt := readAdmissionEmulatorReceipt(t, client, pending.TenantID, pending.ReceiptID)
	storedAttempt := readAdmissionEmulatorAttempt(
		t,
		client,
		receipt.TenantID,
		receipt.ReceiptID,
		attempt.ID,
	)
	return store, receipt, storedAttempt
}

func cleanupTargetCommandFixture(
	t *testing.T,
	receipt firestoreIngestReceipt,
	classification ingest.ArtifactClassification,
) ingest.CleanupTargetCommand {
	t.Helper()
	complete := func(count int) ingest.ArtifactInventorySummary {
		return ingest.ArtifactInventorySummary{
			Performed: true, NonSoftDeletedCount: count,
			Coverage: ingest.ArtifactInventoryCoverageComplete,
		}
	}
	raw := &ingest.ArtifactLineage{
		Path: expectedObjectPath(receipt), SHA256: strings.Repeat("b", 64),
		CRC32C: 0x12345678, Size: 4096, Generation: 1700000000000001, Metageneration: 1,
	}
	manifest := &ingest.ArtifactLineage{
		Path: expectedManifestPath(receipt), SHA256: strings.Repeat("c", 64),
		CRC32C: 0x87654321, Size: 1024, Generation: 1700000000000002, Metageneration: 1,
	}
	command := ingest.CleanupTargetCommand{
		SchemaVersion: ingest.CleanupTargetSchemaVersion,
		CleanupID:     receipt.LeaseOwnerID, TenantID: receipt.TenantID,
		ReceiptID: receipt.ReceiptID, ReservationKey: receipt.ReservationKey,
		AttemptID: receipt.LeaseOwnerID, Mode: receipt.CleanupMode,
		OriginStatus:           receipt.CleanupOriginStatus,
		CleanupPolicyVersion:   receipt.CleanupPolicyVersion,
		CleanupTransitionedAt:  receipt.CleanupTransitionedAt,
		CleanupQuiescenceUntil: receipt.CleanupQuiescenceUntil,
		ReceiptRevision:        receipt.Revision, FencingToken: receipt.FencingToken,
		LeaseAcquiredAt: receipt.LeaseAcquiredAt, LeaseHeartbeatAt: receipt.LeaseHeartbeatAt,
		LeaseExpiresAt: receipt.LeaseExpiresAt, WorkerVersion: ingest.CleanupWorkerVersion,
		Status: ingest.CleanupTargetStatusPlanned, Decision: ingest.CleanupTargetDeleteCandidate,
		RetentionPhase:   ingest.ArtifactRetentionBeforeExpiry,
		ValidatorVersion: receipt.ValidatorVersion,
		ClassifiedAt:     receipt.LeaseAcquiredAt, CreatedAt: receipt.LeaseAcquiredAt,
	}
	switch classification {
	case ingest.ArtifactClassificationValidComplete:
		command.Classification = ingest.ArtifactClassificationValidComplete
		command.ReasonCode = ingest.ArtifactReasonManifestAndReferencedRawValid
		command.RawInventory = complete(1)
		command.ManifestInventory = complete(1)
		command.Raw = raw
		command.Manifest = manifest
	case ingest.ArtifactClassificationValidRawOnly:
		command.Classification = ingest.ArtifactClassificationValidRawOnly
		command.ReasonCode = ingest.ArtifactReasonRawValidManifestAbsent
		command.RawInventory = complete(1)
		command.ManifestInventory = complete(0)
		command.Raw = raw
	default:
		t.Fatalf("unsupported cleanup target fixture classification %q", classification)
	}
	if err := ingest.ValidateCleanupTargetCommand(command); err != nil {
		t.Fatalf("ValidateCleanupTargetCommand() = %v", err)
	}
	return command
}

func exactCleanupTargetSnapshotValidator(
	receipt firestoreIngestReceipt,
	attempt firestoreRecoveryAttempt,
) currentCleanupTargetValidator {
	wantReceipt := receipt.toDomain()
	wantAttempt := currentCleanupAttempt(attempt)
	return func(
		_ ingest.CleanupTargetAuthorizationGrant,
		_ ingest.CleanupTargetCommand,
		current ingest.CurrentCleanupSnapshot,
		_ time.Time,
	) error {
		if current.Receipt != wantReceipt || current.Attempt != wantAttempt {
			return ingest.ErrInvalidCleanupTarget
		}
		return nil
	}
}

func readCleanupTargetEmulator(
	t *testing.T,
	client *firestore.Client,
	tenantID string,
	cleanupID string,
) firestoreIngestCleanupTarget {
	t.Helper()
	document, err := client.Doc(cleanupTargetDocumentPath(tenantID, cleanupID)).Get(context.Background())
	if err != nil {
		t.Fatalf("read cleanup target: %v", err)
	}
	var target firestoreIngestCleanupTarget
	if err := document.DataTo(&target); err != nil {
		t.Fatalf("decode cleanup target: %v", err)
	}
	return target
}

func assertCleanupControlDocumentsUnchanged(
	t *testing.T,
	client *firestore.Client,
	receiptBefore firestoreIngestReceipt,
	attemptBefore firestoreRecoveryAttempt,
) {
	t.Helper()
	receiptAfter := readAdmissionEmulatorReceipt(
		t,
		client,
		receiptBefore.TenantID,
		receiptBefore.ReceiptID,
	)
	attemptAfter := readAdmissionEmulatorAttempt(
		t,
		client,
		receiptBefore.TenantID,
		receiptBefore.ReceiptID,
		attemptBefore.AttemptID,
	)
	if receiptAfter != receiptBefore {
		t.Fatalf("receipt changed during target creation: before=%#v after=%#v", receiptBefore, receiptAfter)
	}
	if attemptAfter != attemptBefore {
		t.Fatalf("attempt changed during target creation: before=%#v after=%#v", attemptBefore, attemptAfter)
	}
}
