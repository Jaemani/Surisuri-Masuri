package cleanupflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const cleanupTerminalIntentSealVersion = "cleanup-terminal-intent@1"

type cleanupTerminalIntentKind string

const (
	cleanupTerminalIntentFinalization cleanupTerminalIntentKind = "finalization"
	cleanupTerminalIntentDisposition  cleanupTerminalIntentKind = "disposition"
)

type cleanupTerminalIntentSource string

const (
	cleanupTerminalIntentSuccess        cleanupTerminalIntentSource = "success"
	cleanupTerminalIntentDurableUnknown cleanupTerminalIntentSource = "durable_unknown"
	cleanupTerminalIntentBoundedFailure cleanupTerminalIntentSource = "bounded_failure"
)

// cleanupTerminalIntent is package-private on purpose. An exported
// ExecutionResult literal is diagnostic data, not terminal authority.
type cleanupTerminalIntent struct {
	kind            cleanupTerminalIntentKind
	source          cleanupTerminalIntentSource
	query           ingest.CleanupExecutionQuery
	command         ingest.CleanupExecutionDispositionCommand
	fence           ingest.LeaseFence
	targetHash      string
	planHash        string
	receiptRevision int64
	phase           ingest.CleanupExecutionPhase
	ledgerRevision  int64
	errorClass      ingest.CleanupExecutionErrorClass
	seal            [sha256.Size]byte
}

type cleanupTerminalIntentSealPayload struct {
	Version         string
	Kind            cleanupTerminalIntentKind
	Source          cleanupTerminalIntentSource
	TenantID        string
	ReservationKey  string
	AttemptID       string
	FenceOwnerID    string
	FenceToken      int64
	FenceExpiresAt  time.Time
	TargetHash      string
	PlanHash        string
	ReceiptRevision int64
	Phase           ingest.CleanupExecutionPhase
	LedgerRevision  int64
	ErrorClass      ingest.CleanupExecutionErrorClass
	Command         ingest.CleanupExecutionDispositionCommand
}

func newCleanupFinalizationIntent(
	query ingest.CleanupExecutionQuery,
	ledger ingest.CleanupExecutionLedger,
) (cleanupTerminalIntent, error) {
	if !cleanupTerminalLedgerBindingValid(query, ledger) ||
		ledger.Phase != ingest.CleanupExecutionPhaseManifestAbsenceConfirmed ||
		ledger.Revision != 7 || ledger.Raw.AuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		ledger.Manifest.AuditOutcome != ingest.CleanupAuditConfirmedAbsent ||
		ledger.Raw.DeleteOutcome == ingest.CleanupDeleteUnknown ||
		ledger.Manifest.DeleteOutcome == ingest.CleanupDeleteUnknown || ledger.ErrorClass != "" {
		return cleanupTerminalIntent{}, ErrInvalidPhaseExecution
	}
	intent := cleanupTerminalIntent{
		kind: cleanupTerminalIntentFinalization, source: cleanupTerminalIntentSuccess,
		query: query, fence: ledger.Fence, targetHash: ledger.TargetHash,
		planHash: ledger.PlanHash, receiptRevision: ledger.ReceiptRevision,
		phase: ledger.Phase, ledgerRevision: ledger.Revision,
	}
	intent.seal = cleanupTerminalIntentSeal(intent)
	return intent, nil
}

func newCleanupDispositionIntent(
	query ingest.CleanupExecutionQuery,
	ledger ingest.CleanupExecutionLedger,
	errorClass ingest.CleanupExecutionErrorClass,
	source cleanupTerminalIntentSource,
	diagnostic error,
) (cleanupTerminalIntent, error) {
	if diagnostic == nil || !cleanupTerminalLedgerBindingValid(query, ledger) ||
		(source != cleanupTerminalIntentDurableUnknown &&
			source != cleanupTerminalIntentBoundedFailure) {
		return cleanupTerminalIntent{}, ErrInvalidPhaseExecution
	}
	command := ingest.CleanupExecutionDispositionCommand{
		Query: query, ExpectedTargetHash: ledger.TargetHash, ExpectedPlanHash: ledger.PlanHash,
		ExpectedReceiptRevision: ledger.ReceiptRevision,
		ExpectedLedgerRevision:  ledger.Revision,
		ExpectedPhase:           ledger.Phase,
		ErrorClass:              errorClass,
	}
	if ingest.ValidateCleanupExecutionDispositionCommand(command) != nil ||
		!cleanupTerminalDiagnosticMatches(source, errorClass, diagnostic) {
		return cleanupTerminalIntent{}, ErrInvalidPhaseExecution
	}
	if source == cleanupTerminalIntentDurableUnknown {
		if !cleanupTerminalLedgerHasUnknownAtCurrentPhase(ledger) ||
			ledger.ErrorClass != errorClass || !cleanupTerminalAmbiguousErrorClass(errorClass) {
			return cleanupTerminalIntent{}, ErrInvalidPhaseExecution
		}
	} else if cleanupTerminalLedgerHasUnknownAtCurrentPhase(ledger) || ledger.ErrorClass != "" {
		return cleanupTerminalIntent{}, ErrInvalidPhaseExecution
	}
	intent := cleanupTerminalIntent{
		kind: cleanupTerminalIntentDisposition, source: source, query: query,
		command: command, fence: ledger.Fence, targetHash: ledger.TargetHash,
		planHash: ledger.PlanHash, receiptRevision: ledger.ReceiptRevision,
		phase: ledger.Phase, ledgerRevision: ledger.Revision, errorClass: errorClass,
	}
	intent.seal = cleanupTerminalIntentSeal(intent)
	return intent, nil
}

func cleanupTerminalIntentValid(
	intent cleanupTerminalIntent,
	result ExecutionResult,
	diagnostic error,
) bool {
	if intent == (cleanupTerminalIntent{}) || intent.seal != cleanupTerminalIntentSeal(intent) ||
		ingest.ValidateCleanupExecutionQuery(intent.query) != nil ||
		ingest.ValidateLeaseFence(intent.fence) != nil ||
		intent.fence.OwnerID != intent.query.AttemptID ||
		!cleanupTerminalDigestValid(intent.targetHash) ||
		!cleanupTerminalDigestValid(intent.planHash) || intent.receiptRevision <= 0 ||
		result.Phase != intent.phase || result.LedgerRevision != intent.ledgerRevision ||
		result.ErrorClass != intent.errorClass {
		return false
	}
	switch intent.kind {
	case cleanupTerminalIntentFinalization:
		return diagnostic == nil && result.Status == ExecutionReadyForFinalization &&
			intent.source == cleanupTerminalIntentSuccess &&
			intent.phase == ingest.CleanupExecutionPhaseManifestAbsenceConfirmed &&
			intent.ledgerRevision == 7 && intent.command == (ingest.CleanupExecutionDispositionCommand{}) &&
			intent.errorClass == ""
	case cleanupTerminalIntentDisposition:
		return diagnostic != nil && result.Status == ExecutionReadyForDisposition &&
			ingest.ValidateCleanupExecutionDispositionCommand(intent.command) == nil &&
			intent.command.Query == intent.query &&
			intent.command.ExpectedTargetHash == intent.targetHash &&
			intent.command.ExpectedPlanHash == intent.planHash &&
			intent.command.ExpectedReceiptRevision == intent.receiptRevision &&
			intent.command.ExpectedLedgerRevision == intent.ledgerRevision &&
			intent.command.ExpectedPhase == intent.phase &&
			intent.command.ErrorClass == intent.errorClass &&
			cleanupTerminalDiagnosticMatches(intent.source, intent.errorClass, diagnostic)
	default:
		return false
	}
}

func cleanupTerminalLedgerBindingValid(
	query ingest.CleanupExecutionQuery,
	ledger ingest.CleanupExecutionLedger,
) bool {
	return ingest.ValidateCleanupExecutionQuery(query) == nil &&
		ledger.SchemaVersion == ingest.CleanupExecutionLedgerSchemaVersion &&
		ledger.DecisionDomain == ingest.CleanupExecutionDecisionExpiry &&
		ingest.ValidateLeaseFence(ledger.Fence) == nil && ledger.Fence.OwnerID == query.AttemptID &&
		cleanupTerminalDigestValid(ledger.TargetHash) && cleanupTerminalDigestValid(ledger.PlanHash) &&
		ledger.ReceiptRevision > 0 && ledger.Revision > 0 && ledger.Disposition == "" &&
		ledger.EvidenceHash == "" && ledger.CompletedAt.IsZero() &&
		cleanupTerminalLedgerPhaseShapeValid(ledger) &&
		cleanupTerminalLedgerProgressBeforeFence(ledger)
}

func cleanupTerminalLedgerPhaseShapeValid(ledger ingest.CleanupExecutionLedger) bool {
	switch ledger.Phase {
	case ingest.CleanupExecutionPhaseRawDispatchRecorded:
		return ledger.Revision == 2 && cleanupTerminalArtifactDispatchOnly(ledger.Raw) &&
			cleanupTerminalArtifactEmpty(ledger.Manifest)
	case ingest.CleanupExecutionPhaseRawOutcomeRecorded:
		return ledger.Revision == 3 && cleanupTerminalArtifactOutcomeOnly(ledger.Raw) &&
			cleanupTerminalArtifactEmpty(ledger.Manifest)
	case ingest.CleanupExecutionPhaseManifestDispatchRecorded:
		return ledger.Revision == 5 && cleanupTerminalArtifactAudited(ledger.Raw) &&
			cleanupTerminalArtifactDispatchOnly(ledger.Manifest) &&
			!ledger.Manifest.DispatchedAt.Before(ledger.Raw.AuditedAt)
	case ingest.CleanupExecutionPhaseManifestOutcomeRecorded:
		return ledger.Revision == 6 && cleanupTerminalArtifactAudited(ledger.Raw) &&
			cleanupTerminalArtifactOutcomeOnly(ledger.Manifest) &&
			!ledger.Manifest.DispatchedAt.Before(ledger.Raw.AuditedAt)
	case ingest.CleanupExecutionPhaseManifestAbsenceConfirmed:
		return ledger.Revision == 7 && cleanupTerminalArtifactAudited(ledger.Raw) &&
			cleanupTerminalArtifactAudited(ledger.Manifest) &&
			!ledger.Manifest.DispatchedAt.Before(ledger.Raw.AuditedAt)
	default:
		return false
	}
}

func cleanupTerminalLedgerProgressBeforeFence(ledger ingest.CleanupExecutionLedger) bool {
	for _, observedAt := range []time.Time{
		ledger.Raw.DispatchedAt,
		ledger.Raw.OutcomeRecordedAt,
		ledger.Raw.AuditedAt,
		ledger.Manifest.DispatchedAt,
		ledger.Manifest.OutcomeRecordedAt,
		ledger.Manifest.AuditedAt,
	} {
		if !observedAt.IsZero() && !observedAt.Before(ledger.Fence.ExpiresAt) {
			return false
		}
	}
	return true
}

func cleanupTerminalArtifactEmpty(artifact ingest.CleanupArtifactExecutionLedger) bool {
	return artifact.DispatchedAt.IsZero() && artifact.DeleteOutcome == "" &&
		artifact.OutcomeRecordedAt.IsZero() && artifact.AuditOutcome == "" &&
		artifact.AuditedAt.IsZero()
}

func cleanupTerminalArtifactDispatchOnly(artifact ingest.CleanupArtifactExecutionLedger) bool {
	return !artifact.DispatchedAt.IsZero() && artifact.DeleteOutcome == "" &&
		artifact.OutcomeRecordedAt.IsZero() && artifact.AuditOutcome == "" &&
		artifact.AuditedAt.IsZero()
}

func cleanupTerminalArtifactOutcomeOnly(artifact ingest.CleanupArtifactExecutionLedger) bool {
	return !artifact.DispatchedAt.IsZero() && !artifact.OutcomeRecordedAt.IsZero() &&
		!artifact.OutcomeRecordedAt.Before(artifact.DispatchedAt) &&
		cleanupTerminalDeleteOutcomeValid(artifact) && artifact.AuditOutcome == "" &&
		artifact.AuditedAt.IsZero()
}

func cleanupTerminalArtifactAudited(artifact ingest.CleanupArtifactExecutionLedger) bool {
	return !artifact.DispatchedAt.IsZero() && !artifact.OutcomeRecordedAt.IsZero() &&
		!artifact.OutcomeRecordedAt.Before(artifact.DispatchedAt) &&
		cleanupTerminalDeleteOutcomeKnown(artifact) &&
		artifact.AuditOutcome == ingest.CleanupAuditConfirmedAbsent &&
		!artifact.AuditedAt.IsZero() && !artifact.AuditedAt.Before(artifact.OutcomeRecordedAt)
}

func cleanupTerminalDeleteOutcomeValid(artifact ingest.CleanupArtifactExecutionLedger) bool {
	return artifact.DeleteOutcome == ingest.CleanupDeleteUnknown ||
		cleanupTerminalDeleteOutcomeKnown(artifact)
}

func cleanupTerminalDeleteOutcomeKnown(artifact ingest.CleanupArtifactExecutionLedger) bool {
	if !artifact.Targeted {
		return artifact.DeleteOutcome == ingest.CleanupDeleteNotAttempted
	}
	switch artifact.DeleteOutcome {
	case ingest.CleanupDeleteNotAttempted,
		ingest.CleanupDeleteObserved,
		ingest.CleanupDeleteNotFound:
		return true
	default:
		return false
	}
}

func cleanupTerminalAmbiguousErrorClass(class ingest.CleanupExecutionErrorClass) bool {
	switch class {
	case ingest.CleanupExecutionErrorProviderTimeout,
		ingest.CleanupExecutionErrorProviderCancelled,
		ingest.CleanupExecutionErrorProviderUnavailable,
		ingest.CleanupExecutionErrorResponseUnverifiable:
		return true
	default:
		return false
	}
}

func cleanupTerminalLedgerHasUnknownAtCurrentPhase(
	ledger ingest.CleanupExecutionLedger,
) bool {
	switch ledger.Phase {
	case ingest.CleanupExecutionPhaseRawOutcomeRecorded:
		return ledger.Raw.DeleteOutcome == ingest.CleanupDeleteUnknown
	case ingest.CleanupExecutionPhaseManifestOutcomeRecorded:
		return ledger.Manifest.DeleteOutcome == ingest.CleanupDeleteUnknown
	default:
		return false
	}
}

func cleanupTerminalDiagnosticMatches(
	source cleanupTerminalIntentSource,
	errorClass ingest.CleanupExecutionErrorClass,
	diagnostic error,
) bool {
	if diagnostic == nil {
		return false
	}
	switch source {
	case cleanupTerminalIntentDurableUnknown:
		mapped, count, valid := cleanupExecutionDiagnosticClasses(diagnostic, true)
		return valid && errors.Is(diagnostic, ErrCleanupOutcomeUnknown) &&
			(count == 0 || count == 1 && mapped == errorClass)
	case cleanupTerminalIntentBoundedFailure:
		mapped, count, valid := cleanupExecutionDiagnosticClasses(diagnostic, false)
		return valid && count == 1 && mapped == errorClass
	default:
		return false
	}
}

// cleanupExecutionErrorClassForProviderFailure returns the number of distinct
// recognized classes. Callers must accept exactly one; first-match mapping is
// intentionally forbidden for joined errors.
func cleanupExecutionErrorClassForProviderFailure(
	err error,
) (ingest.CleanupExecutionErrorClass, int) {
	class, count, valid := cleanupExecutionDiagnosticClasses(err, false)
	if !valid {
		return "", 0
	}
	return class, count
}

func cleanupExecutionDiagnosticClasses(
	err error,
	allowOutcomeUnknown bool,
) (ingest.CleanupExecutionErrorClass, int, bool) {
	classes := make(map[ingest.CleanupExecutionErrorClass]struct{}, 2)
	valid := err != nil
	var visit func(error)
	visit = func(current error) {
		if current == nil || !valid {
			valid = false
			return
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			if len(children) == 0 {
				valid = false
				return
			}
			for _, child := range children {
				visit(child)
			}
			return
		}
		if wrapped, ok := current.(interface{ Unwrap() error }); ok {
			visit(wrapped.Unwrap())
			return
		}
		if allowOutcomeUnknown && errors.Is(current, ErrCleanupOutcomeUnknown) {
			return
		}
		class, recognized := cleanupExecutionErrorClassForLeaf(current)
		if !recognized {
			valid = false
			return
		}
		classes[class] = struct{}{}
	}
	visit(err)
	if !valid {
		return "", 0, false
	}
	if len(classes) != 1 {
		return "", len(classes), true
	}
	for class := range classes {
		return class, 1, true
	}
	return "", 0, true
}

func cleanupExecutionErrorClassForLeaf(
	err error,
) (ingest.CleanupExecutionErrorClass, bool) {
	match := func(sentinels ...error) bool {
		for _, sentinel := range sentinels {
			if cleanupTerminalErrorIdentityEqual(err, sentinel) {
				return true
			}
		}
		return false
	}
	switch {
	case match(
		ingest.ErrArtifactProviderTimeout, context.DeadlineExceeded,
		firebaseadapter.ErrCleanupArtifactExecutionAuthorizationExpired,
		firebaseadapter.ErrCleanupAbsenceAuditAuthorizationExpired):
		return ingest.CleanupExecutionErrorProviderTimeout, true
	case match(ingest.ErrArtifactProviderCancelled, context.Canceled):
		return ingest.CleanupExecutionErrorProviderCancelled, true
	case match(ingest.ErrArtifactProviderUnavailable):
		return ingest.CleanupExecutionErrorProviderUnavailable, true
	case match(ingest.ErrArtifactResponseUnverifiable):
		return ingest.CleanupExecutionErrorResponseUnverifiable, true
	case match(ingest.ErrArtifactQuotaLimited):
		return ingest.CleanupExecutionErrorQuotaLimited, true
	case match(ingest.ErrCleanupExecutionInventoryIncomplete):
		return ingest.CleanupExecutionErrorInventoryIncomplete, true
	case match(ingest.ErrArtifactPermissionDenied):
		return ingest.CleanupExecutionErrorPermissionDenied, true
	case match(ingest.ErrArtifactPreconditionDrift):
		return ingest.CleanupExecutionErrorPreconditionDrift, true
	case match(ingest.ErrCleanupExecutionGenerationDrift):
		return ingest.CleanupExecutionErrorGenerationDrift, true
	case match(ingest.ErrCleanupExecutionLineageMismatch):
		return ingest.CleanupExecutionErrorLineageMismatch, true
	default:
		return "", false
	}
}

func cleanupTerminalErrorIdentityEqual(left, right error) bool {
	leftType := reflect.TypeOf(left)
	return leftType != nil && leftType == reflect.TypeOf(right) && leftType.Comparable() &&
		left == right
}

func cleanupTerminalIntentSeal(intent cleanupTerminalIntent) [sha256.Size]byte {
	payload, err := json.Marshal(cleanupTerminalIntentSealPayload{
		Version: cleanupTerminalIntentSealVersion, Kind: intent.kind, Source: intent.source,
		TenantID: intent.query.TenantID, ReservationKey: intent.query.ReservationKey,
		AttemptID: intent.query.AttemptID, FenceOwnerID: intent.fence.OwnerID,
		FenceToken: intent.fence.Token, FenceExpiresAt: intent.fence.ExpiresAt.UTC(),
		TargetHash: intent.targetHash, PlanHash: intent.planHash,
		ReceiptRevision: intent.receiptRevision, Phase: intent.phase,
		LedgerRevision: intent.ledgerRevision, ErrorClass: intent.errorClass,
		Command: intent.command,
	})
	if err != nil {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(payload)
}

func cleanupTerminalDigestValid(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
