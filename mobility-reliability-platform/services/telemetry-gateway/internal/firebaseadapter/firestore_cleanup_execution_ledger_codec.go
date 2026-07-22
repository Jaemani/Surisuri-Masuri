package firebaseadapter

import (
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

// decodeCleanupExecutionLedger reconstructs the cleanup fence exclusively from
// the immutable target bound into plan. The mutable attempt stores only the
// bounded ledger surface; it cannot supply or extend its own authority.
func decodeCleanupExecutionLedger(
	plan ingest.CleanupExecutionLedgerPlan,
	attempt firestoreRecoveryAttempt,
	observedAt time.Time,
) (ingest.CleanupExecutionLedger, bool, error) {
	if !hasCleanupExecutionLedgerResidue(attempt) {
		return ingest.CleanupExecutionLedger{}, false, nil
	}
	command := plan.Target.Command
	if observedAt.IsZero() || attempt.CleanupRawTargeted == nil ||
		attempt.CleanupManifestTargeted == nil ||
		attempt.AttemptID != command.AttemptID || attempt.TenantID != command.TenantID ||
		attempt.ReceiptID != command.ReceiptID || attempt.OwnerKind != ingest.LeaseOwnerCleanup ||
		attempt.FencingToken != command.FencingToken ||
		attempt.WorkerVersion != command.WorkerVersion ||
		!attempt.StartedAt.Equal(command.LeaseAcquiredAt) {
		return ingest.CleanupExecutionLedger{}, true, ingest.ErrInvalidCleanupExecutionLedger
	}
	ledger := ingest.CleanupExecutionLedger{
		SchemaVersion:   attempt.CleanupSchemaVersion,
		DecisionDomain:  attempt.DecisionDomain,
		TargetHash:      attempt.CleanupTargetHash,
		PlanHash:        attempt.CleanupPlanHash,
		ReceiptRevision: attempt.CleanupReceiptRevision,
		Fence: ingest.LeaseFence{
			OwnerID:   command.AttemptID,
			Token:     command.FencingToken,
			ExpiresAt: command.LeaseExpiresAt.UTC(),
		},
		Revision: attempt.CleanupExecutionRevision,
		Phase:    attempt.CleanupPhase,
		Raw: ingest.CleanupArtifactExecutionLedger{
			Targeted:          *attempt.CleanupRawTargeted,
			DispatchedAt:      attempt.CleanupRawDispatchAt.UTC(),
			DeleteOutcome:     attempt.CleanupRawDeleteOutcome,
			OutcomeRecordedAt: attempt.CleanupRawOutcomeRecordedAt.UTC(),
			AuditOutcome:      attempt.CleanupRawAuditOutcome,
			AuditedAt:         attempt.CleanupRawAuditedAt.UTC(),
		},
		Manifest: ingest.CleanupArtifactExecutionLedger{
			Targeted:          *attempt.CleanupManifestTargeted,
			DispatchedAt:      attempt.CleanupManifestDispatchAt.UTC(),
			DeleteOutcome:     attempt.CleanupManifestDeleteOutcome,
			OutcomeRecordedAt: attempt.CleanupManifestOutcomeRecordedAt.UTC(),
			AuditOutcome:      attempt.CleanupManifestAuditOutcome,
			AuditedAt:         attempt.CleanupManifestAuditedAt.UTC(),
		},
		Disposition:  attempt.CleanupDisposition,
		ErrorClass:   attempt.CleanupErrorClass,
		EvidenceHash: attempt.CleanupEvidenceHash,
		CompletedAt:  attempt.CompletedAt.UTC(),
	}
	if err := ingest.ValidateCleanupExecutionLedger(plan, ledger, observedAt.UTC()); err != nil {
		return ingest.CleanupExecutionLedger{}, true, ingest.ErrInvalidCleanupExecutionLedger
	}
	return ledger, true, nil
}

// decodeHistoricalCleanupExecutionLedger validates progress at its last
// persisted phase time. A post-expiry takeover time is deliberately not used:
// historical progress had to be valid before the old fence expired.
func decodeHistoricalCleanupExecutionLedger(
	plan ingest.CleanupExecutionLedgerPlan,
	attempt firestoreRecoveryAttempt,
) (ingest.CleanupExecutionLedger, bool, error) {
	observedAt := cleanupExecutionAttemptPersistedAt(plan, attempt)
	if observedAt.IsZero() {
		return ingest.CleanupExecutionLedger{}, hasCleanupExecutionLedgerResidue(attempt),
			ingest.ErrInvalidCleanupExecutionLedger
	}
	return decodeCleanupExecutionLedger(plan, attempt, observedAt)
}

func cleanupExecutionAttemptPersistedAt(
	plan ingest.CleanupExecutionLedgerPlan,
	attempt firestoreRecoveryAttempt,
) time.Time {
	switch attempt.CleanupPhase {
	case ingest.CleanupExecutionPhasePlanned:
		return plan.Target.Command.CreatedAt.UTC()
	case ingest.CleanupExecutionPhaseRawDispatchRecorded:
		return attempt.CleanupRawDispatchAt.UTC()
	case ingest.CleanupExecutionPhaseRawOutcomeRecorded:
		return attempt.CleanupRawOutcomeRecordedAt.UTC()
	case ingest.CleanupExecutionPhaseRawAbsenceConfirmed:
		return attempt.CleanupRawAuditedAt.UTC()
	case ingest.CleanupExecutionPhaseManifestDispatchRecorded:
		return attempt.CleanupManifestDispatchAt.UTC()
	case ingest.CleanupExecutionPhaseManifestOutcomeRecorded:
		return attempt.CleanupManifestOutcomeRecordedAt.UTC()
	case ingest.CleanupExecutionPhaseManifestAbsenceConfirmed:
		return attempt.CleanupManifestAuditedAt.UTC()
	case ingest.CleanupExecutionPhaseCompleted:
		return attempt.CompletedAt.UTC()
	default:
		return time.Time{}
	}
}

// cleanupExecutionLedgerUpdates encodes a complete, already validated ledger.
// Optional future-phase fields are omitted; monotonic callers must validate the
// persisted prior revision before applying these updates.
func cleanupExecutionLedgerUpdates(
	plan ingest.CleanupExecutionLedgerPlan,
	ledger ingest.CleanupExecutionLedger,
	observedAt time.Time,
) ([]firestore.Update, error) {
	if err := ingest.ValidateCleanupExecutionLedger(plan, ledger, observedAt.UTC()); err != nil {
		return nil, ingest.ErrInvalidCleanupExecutionLedger
	}
	updates := []firestore.Update{
		{Path: "decision_domain", Value: string(ledger.DecisionDomain)},
		{Path: "cleanup_schema_version", Value: ledger.SchemaVersion},
		{Path: "cleanup_target_hash", Value: ledger.TargetHash},
		{Path: "cleanup_plan_hash", Value: ledger.PlanHash},
		{Path: "cleanup_receipt_revision", Value: ledger.ReceiptRevision},
		{Path: "cleanup_execution_revision", Value: ledger.Revision},
		{Path: "cleanup_phase", Value: string(ledger.Phase)},
		{Path: "cleanup_raw_targeted", Value: ledger.Raw.Targeted},
		{Path: "cleanup_manifest_targeted", Value: ledger.Manifest.Targeted},
	}
	updates = appendCleanupArtifactExecutionUpdates(updates, "cleanup_raw", ledger.Raw)
	updates = appendCleanupArtifactExecutionUpdates(updates, "cleanup_manifest", ledger.Manifest)
	if ledger.Disposition != "" {
		updates = append(updates, firestore.Update{Path: "cleanup_disposition", Value: string(ledger.Disposition)})
	}
	if ledger.ErrorClass != "" {
		updates = append(updates, firestore.Update{Path: "cleanup_error_class", Value: string(ledger.ErrorClass)})
	}
	if ledger.EvidenceHash != "" {
		updates = append(updates, firestore.Update{Path: "cleanup_evidence_hash", Value: ledger.EvidenceHash})
	}
	if !ledger.CompletedAt.IsZero() {
		updates = append(updates, firestore.Update{Path: "completed_at", Value: ledger.CompletedAt.UTC()})
	}
	return updates, nil
}

func appendCleanupArtifactExecutionUpdates(
	updates []firestore.Update,
	prefix string,
	record ingest.CleanupArtifactExecutionLedger,
) []firestore.Update {
	if !record.DispatchedAt.IsZero() {
		updates = append(updates, firestore.Update{Path: prefix + "_dispatch_at", Value: record.DispatchedAt.UTC()})
	}
	if record.DeleteOutcome != "" {
		updates = append(updates, firestore.Update{Path: prefix + "_delete_outcome", Value: string(record.DeleteOutcome)})
	}
	if !record.OutcomeRecordedAt.IsZero() {
		updates = append(updates, firestore.Update{
			Path: prefix + "_outcome_recorded_at", Value: record.OutcomeRecordedAt.UTC(),
		})
	}
	if record.AuditOutcome != "" {
		updates = append(updates, firestore.Update{Path: prefix + "_audit_outcome", Value: string(record.AuditOutcome)})
	}
	if !record.AuditedAt.IsZero() {
		updates = append(updates, firestore.Update{Path: prefix + "_audited_at", Value: record.AuditedAt.UTC()})
	}
	return updates
}

// hasCleanupExecutionLedgerResidue distinguishes an absent pristine cleanup
// ledger from a partial or tampered ledger. Pointer booleans make stored false
// distinguishable from an omitted field.
func hasCleanupExecutionLedgerResidue(attempt firestoreRecoveryAttempt) bool {
	return attempt.DecisionDomain == ingest.CleanupExecutionDecisionExpiry ||
		attempt.CleanupSchemaVersion != "" || attempt.CleanupTargetHash != "" ||
		attempt.CleanupPlanHash != "" || attempt.CleanupReceiptRevision != 0 ||
		attempt.CleanupExecutionRevision != 0 || attempt.CleanupPhase != "" ||
		attempt.CleanupRawTargeted != nil || !attempt.CleanupRawDispatchAt.IsZero() ||
		attempt.CleanupRawDeleteOutcome != "" || !attempt.CleanupRawOutcomeRecordedAt.IsZero() ||
		attempt.CleanupRawAuditOutcome != "" || !attempt.CleanupRawAuditedAt.IsZero() ||
		attempt.CleanupManifestTargeted != nil || !attempt.CleanupManifestDispatchAt.IsZero() ||
		attempt.CleanupManifestDeleteOutcome != "" ||
		!attempt.CleanupManifestOutcomeRecordedAt.IsZero() ||
		attempt.CleanupManifestAuditOutcome != "" || !attempt.CleanupManifestAuditedAt.IsZero() ||
		attempt.CleanupDisposition != "" || attempt.CleanupErrorClass != "" ||
		attempt.CleanupEvidenceHash != ""
}
