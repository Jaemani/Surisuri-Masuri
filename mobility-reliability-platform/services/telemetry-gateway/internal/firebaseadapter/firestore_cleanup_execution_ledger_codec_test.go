package firebaseadapter

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupExecutionLedgerCodecTreatsPristineAttemptAsAbsent(t *testing.T) {
	observedAt, plan, _, attempt, _ := cleanupExecutionLedgerCodecFixture(t, false)

	ledger, present, err := decodeCleanupExecutionLedger(plan, attempt, observedAt)
	if err != nil || present || ledger != (ingest.CleanupExecutionLedger{}) {
		t.Fatalf("decodeCleanupExecutionLedger() = %#v, %t, %v", ledger, present, err)
	}
}

func TestCleanupExecutionLedgerCodecRoundTripsPlannedLedger(t *testing.T) {
	observedAt, plan, ledger, attempt, _ := cleanupExecutionLedgerCodecFixture(t, false)
	attempt = attemptWithCleanupExecutionLedger(attempt, ledger)

	decoded, present, err := decodeCleanupExecutionLedger(plan, attempt, observedAt)
	if err != nil || !present || !reflect.DeepEqual(decoded, ledger) {
		t.Fatalf("decodeCleanupExecutionLedger() = %#v, %t, %v; want %#v", decoded, present, err, ledger)
	}
	updates, err := cleanupExecutionLedgerUpdates(plan, ledger, observedAt)
	if err != nil {
		t.Fatalf("cleanupExecutionLedgerUpdates() = %v", err)
	}
	values := cleanupExecutionUpdateValues(updates)
	if len(values) != 9 || values["cleanup_phase"] != string(ingest.CleanupExecutionPhasePlanned) ||
		values["cleanup_execution_revision"] != int64(1) || values["cleanup_raw_targeted"] != true ||
		values["cleanup_manifest_targeted"] != true {
		t.Fatalf("planned updates = %#v", values)
	}
}

func TestCleanupExecutionLedgerCodecRoundTripsRecordedOutcome(t *testing.T) {
	observedAt, plan, ledger, attempt, _ := cleanupExecutionLedgerCodecFixture(t, false)
	var err error
	ledger, err = ingest.AdvanceCleanupExecutionLedger(plan, ledger, ingest.CleanupExecutionTransition{
		Phase: ingest.CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: observedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("raw dispatch transition = %v", err)
	}
	ledger, err = ingest.AdvanceCleanupExecutionLedger(plan, ledger, ingest.CleanupExecutionTransition{
		Phase:         ingest.CleanupExecutionPhaseRawOutcomeRecorded,
		DeleteOutcome: ingest.CleanupDeleteObserved, ObservedAt: observedAt.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("raw outcome transition = %v", err)
	}
	attempt = attemptWithCleanupExecutionLedger(attempt, ledger)

	decoded, present, err := decodeCleanupExecutionLedger(plan, attempt, observedAt.Add(2*time.Second))
	if err != nil || !present || !reflect.DeepEqual(decoded, ledger) ||
		decoded.Raw.DeleteOutcome != ingest.CleanupDeleteObserved ||
		!decoded.Raw.OutcomeRecordedAt.Equal(observedAt.Add(2*time.Second)) {
		t.Fatalf("recorded outcome decode = %#v, %t, %v", decoded, present, err)
	}
	updates, err := cleanupExecutionLedgerUpdates(plan, ledger, observedAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("cleanupExecutionLedgerUpdates() = %v", err)
	}
	values := cleanupExecutionUpdateValues(updates)
	if values["cleanup_raw_delete_outcome"] != string(ingest.CleanupDeleteObserved) ||
		values["cleanup_raw_outcome_recorded_at"] != observedAt.Add(2*time.Second).UTC() {
		t.Fatalf("recorded outcome updates = %#v", values)
	}
}

func TestCleanupExecutionLedgerCodecRejectsPartialOrTamperedResidue(t *testing.T) {
	observedAt, plan, ledger, pristine, _ := cleanupExecutionLedgerCodecFixture(t, false)
	tests := []struct {
		name   string
		mutate func(*firestoreRecoveryAttempt)
	}{
		{name: "missing raw targeted", mutate: func(value *firestoreRecoveryAttempt) { value.CleanupRawTargeted = nil }},
		{name: "target hash", mutate: func(value *firestoreRecoveryAttempt) {
			value.CleanupTargetHash = differentCleanupLedgerDigest(value.CleanupTargetHash)
		}},
		{name: "attempt fence", mutate: func(value *firestoreRecoveryAttempt) { value.FencingToken++ }},
		{name: "phase residue", mutate: func(value *firestoreRecoveryAttempt) { value.CleanupRawDispatchAt = observedAt }},
		{name: "foreign decision", mutate: func(value *firestoreRecoveryAttempt) {
			value.DecisionDomain = ingest.ForwardRecoveryDecisionArtifactReconciliation
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempt := attemptWithCleanupExecutionLedger(pristine, ledger)
			test.mutate(&attempt)
			decoded, present, err := decodeCleanupExecutionLedger(plan, attempt, observedAt)
			if !present || !errors.Is(err, ingest.ErrInvalidCleanupExecutionLedger) ||
				decoded != (ingest.CleanupExecutionLedger{}) {
				t.Fatalf("decodeCleanupExecutionLedger() = %#v, %t, %v", decoded, present, err)
			}
		})
	}
}

func TestCleanupExecutionLedgerCodecPreservesVerifiedEmptyFalseTargets(t *testing.T) {
	observedAt, plan, ledger, attempt, _ := cleanupExecutionLedgerCodecFixture(t, true)
	if ledger.Raw.Targeted || ledger.Manifest.Targeted {
		t.Fatalf("verified-empty ledger targeted = raw:%t manifest:%t", ledger.Raw.Targeted, ledger.Manifest.Targeted)
	}
	attempt = attemptWithCleanupExecutionLedger(attempt, ledger)
	if attempt.CleanupRawTargeted == nil || *attempt.CleanupRawTargeted ||
		attempt.CleanupManifestTargeted == nil || *attempt.CleanupManifestTargeted {
		t.Fatalf("stored false targeted pointers = %#v / %#v", attempt.CleanupRawTargeted, attempt.CleanupManifestTargeted)
	}
	decoded, present, err := decodeCleanupExecutionLedger(plan, attempt, observedAt)
	if err != nil || !present || decoded.Raw.Targeted || decoded.Manifest.Targeted {
		t.Fatalf("verified-empty decode = %#v, %t, %v", decoded, present, err)
	}
	updates, err := cleanupExecutionLedgerUpdates(plan, ledger, observedAt)
	if err != nil {
		t.Fatalf("cleanupExecutionLedgerUpdates() = %v", err)
	}
	values := cleanupExecutionUpdateValues(updates)
	if raw, ok := values["cleanup_raw_targeted"].(bool); !ok || raw {
		t.Fatalf("raw targeted update = %#v", values["cleanup_raw_targeted"])
	}
	if manifest, ok := values["cleanup_manifest_targeted"].(bool); !ok || manifest {
		t.Fatalf("manifest targeted update = %#v", values["cleanup_manifest_targeted"])
	}
}

func TestStartedRecoveryAttemptValidatorRejectsCleanupLedgerResidue(t *testing.T) {
	_, _, _, attempt, receipt := cleanupExecutionLedgerCodecFixture(t, false)
	attempt.CleanupPlanHash = "residue"
	expected := ingest.RecoveryAttemptProposal{ID: attempt.AttemptID, WorkerVersion: attempt.WorkerVersion}
	fence := ingest.LeaseFence{
		OwnerID: receipt.LeaseOwnerID, Token: receipt.FencingToken, ExpiresAt: receipt.LeaseExpiresAt,
	}

	err := validateStartedRecoveryAttemptForOwner(attempt, receipt, expected, fence, ingest.LeaseOwnerCleanup)
	if !errors.Is(err, ingest.ErrInvalidForwardRecoveryActionAuthorization) {
		t.Fatalf("validateStartedRecoveryAttemptForOwner() = %v", err)
	}
}

func TestFailedAndOutcomeAttemptValidatorsRejectCleanupLedgerResidue(t *testing.T) {
	observedAt, _, _, attempt, receipt := cleanupExecutionLedgerCodecFixture(t, false)
	attempt.Status = ingest.RecoveryAttemptFailed
	attempt.FailureCode = ingest.RecoveryAttemptFailureInvalidContract
	attempt.FailedAt = attempt.StartedAt.Add(time.Second)
	attempt.CleanupPlanHash = "residue"
	expected := ingest.RecoveryAttemptProposal{ID: attempt.AttemptID, WorkerVersion: attempt.WorkerVersion}
	fence := ingest.LeaseFence{
		OwnerID: receipt.LeaseOwnerID, Token: receipt.FencingToken, ExpiresAt: receipt.LeaseExpiresAt,
	}
	if err := validateFailedRecoveryAttemptForOwner(
		attempt, receipt, expected, fence, ingest.LeaseOwnerCleanup, observedAt,
	); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("validateFailedRecoveryAttemptForOwner() = %v", err)
	}
	if _, err := currentForwardRecoveryOutcomeAttempt(attempt); !errors.Is(
		err, ingest.ErrForwardRecoveryOutcomeUnavailable,
	) {
		t.Fatalf("currentForwardRecoveryOutcomeAttempt() = %v", err)
	}
}

func cleanupExecutionLedgerCodecFixture(
	t *testing.T,
	verifiedEmpty bool,
) (time.Time, ingest.CleanupExecutionLedgerPlan, ingest.CleanupExecutionLedger, firestoreRecoveryAttempt, firestoreIngestReceipt) {
	t.Helper()
	fixture := newCleanupTargetAdapterFixture(t)
	command := fixture.command
	if verifiedEmpty {
		command.Decision = ingest.CleanupTargetVerifiedEmpty
		command.Classification = ingest.ArtifactClassificationNone
		command.ReasonCode = ingest.ArtifactReasonNoCandidates
		command.RawInventory.NonSoftDeletedCount = 0
		command.ManifestInventory.NonSoftDeletedCount = 0
		command.Raw = nil
		command.Manifest = nil
	}
	if err := ingest.ValidateCleanupTargetCommand(command); err != nil {
		t.Fatalf("ValidateCleanupTargetCommand() = %v", err)
	}
	targetHash, err := ingest.CleanupTargetHash(command)
	if err != nil {
		t.Fatalf("CleanupTargetHash() = %v", err)
	}
	receipt := fixture.transaction.receipts[admissionReceiptPath()]
	attempt := fixture.transaction.attempts[fixture.attemptPath]
	snapshot := ingest.CurrentCleanupExecutionSnapshot{
		Receipt: receipt.toDomain(), Attempt: currentCleanupAttempt(attempt),
		Target:   ingest.CleanupTarget{Command: command, TargetHash: targetHash},
		ReadTime: fixture.observedAt,
	}
	plan, err := ingest.BuildCleanupExecutionLedgerPlan(ingest.CleanupExecutionQuery{
		TenantID: command.TenantID, ReservationKey: command.ReservationKey, AttemptID: command.AttemptID,
	}, snapshot, fixture.observedAt)
	if err != nil {
		t.Fatalf("BuildCleanupExecutionLedgerPlan() = %v", err)
	}
	ledger, err := ingest.NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	return fixture.observedAt, plan, ledger, attempt, receipt
}

func attemptWithCleanupExecutionLedger(
	attempt firestoreRecoveryAttempt,
	ledger ingest.CleanupExecutionLedger,
) firestoreRecoveryAttempt {
	rawTargeted := ledger.Raw.Targeted
	manifestTargeted := ledger.Manifest.Targeted
	attempt.DecisionDomain = ledger.DecisionDomain
	attempt.CleanupSchemaVersion = ledger.SchemaVersion
	attempt.CleanupTargetHash = ledger.TargetHash
	attempt.CleanupPlanHash = ledger.PlanHash
	attempt.CleanupReceiptRevision = ledger.ReceiptRevision
	attempt.CleanupExecutionRevision = ledger.Revision
	attempt.CleanupPhase = ledger.Phase
	attempt.CleanupRawTargeted = &rawTargeted
	attempt.CleanupRawDispatchAt = ledger.Raw.DispatchedAt
	attempt.CleanupRawDeleteOutcome = ledger.Raw.DeleteOutcome
	attempt.CleanupRawOutcomeRecordedAt = ledger.Raw.OutcomeRecordedAt
	attempt.CleanupRawAuditOutcome = ledger.Raw.AuditOutcome
	attempt.CleanupRawAuditedAt = ledger.Raw.AuditedAt
	attempt.CleanupManifestTargeted = &manifestTargeted
	attempt.CleanupManifestDispatchAt = ledger.Manifest.DispatchedAt
	attempt.CleanupManifestDeleteOutcome = ledger.Manifest.DeleteOutcome
	attempt.CleanupManifestOutcomeRecordedAt = ledger.Manifest.OutcomeRecordedAt
	attempt.CleanupManifestAuditOutcome = ledger.Manifest.AuditOutcome
	attempt.CleanupManifestAuditedAt = ledger.Manifest.AuditedAt
	attempt.CleanupDisposition = ledger.Disposition
	attempt.CleanupErrorClass = ledger.ErrorClass
	attempt.CleanupEvidenceHash = ledger.EvidenceHash
	attempt.CompletedAt = ledger.CompletedAt
	return attempt
}

func cleanupExecutionUpdateValues(updates []firestore.Update) map[string]any {
	values := make(map[string]any, len(updates))
	for _, update := range updates {
		values[update.Path] = update.Value
	}
	return values
}

func differentCleanupLedgerDigest(value string) string {
	if value[0] == 'f' {
		return "e" + value[1:]
	}
	return "f" + value[1:]
}
