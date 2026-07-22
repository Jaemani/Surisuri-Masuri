package cleanupflow

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/cleanupattest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupPhaseExecutorRunsRawAuditBeforeManifestDispatch(t *testing.T) {
	harness := newCleanupPhaseHarness(t)
	result, err := harness.executor.Execute(context.Background(), harness.query)
	if err != nil || result.Status != ExecutionReadyForFinalization ||
		result.Phase != ingest.CleanupExecutionPhaseManifestAbsenceConfirmed ||
		result.LedgerRevision != 7 {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	want := []string{
		"initialize",
		"begin_raw", "delete_raw", "record_raw_outcome",
		"authorize_raw_audit", "audit_raw", "record_raw_audit",
		"begin_manifest", "delete_manifest", "record_manifest_outcome",
		"authorize_manifest_audit", "audit_manifest", "record_manifest_audit",
	}
	if !reflect.DeepEqual(harness.events, want) {
		t.Fatalf("events = %#v, want %#v", harness.events, want)
	}
}

func TestCleanupPhaseExecutorReplayedDispatchCallsProviderZero(t *testing.T) {
	harness := newCleanupPhaseHarness(t)
	harness.control.replayRaw = true
	result, err := harness.executor.Execute(context.Background(), harness.query)
	if !errors.Is(err, ErrCleanupDispatchPending) || result.Status != ExecutionDispatchPending ||
		result.Artifact != ingest.CleanupArtifactExecutionRaw {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	if !reflect.DeepEqual(harness.events, []string{"initialize", "begin_raw"}) {
		t.Fatalf("replay events = %#v", harness.events)
	}
}

func TestCleanupPhaseExecutorPersistsUnknownAndStopsBeforeAudit(t *testing.T) {
	harness := newCleanupPhaseHarness(t)
	harness.artifacts.unknownRaw = true
	result, err := harness.executor.Execute(context.Background(), harness.query)
	if !errors.Is(err, ErrCleanupOutcomeUnknown) ||
		!errors.Is(err, ingest.ErrArtifactProviderTimeout) ||
		result.Status != ExecutionReadyForDisposition ||
		result.DeleteOutcome != ingest.CleanupDeleteUnknown ||
		result.ErrorClass != ingest.CleanupExecutionErrorProviderTimeout ||
		result.terminalIntent.command.ErrorClass != ingest.CleanupExecutionErrorProviderTimeout {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	want := []string{"initialize", "begin_raw", "delete_raw", "record_raw_outcome"}
	if !reflect.DeepEqual(harness.events, want) {
		t.Fatalf("unknown events = %#v, want %#v", harness.events, want)
	}
	if harness.control.ledger.Phase != ingest.CleanupExecutionPhaseRawOutcomeRecorded ||
		harness.control.ledger.Raw.DeleteOutcome != ingest.CleanupDeleteUnknown {
		t.Fatalf("durable unknown ledger = %#v", harness.control.ledger)
	}
}

func TestCleanupPhaseExecutorPersistsUnverifiableAsUnknownAndStops(t *testing.T) {
	harness := newCleanupPhaseHarness(t)
	harness.artifacts.unknownRaw = true
	harness.artifacts.unknownRawErr = ingest.ErrArtifactResponseUnverifiable
	harness.artifacts.unknownRawClass = ingest.CleanupExecutionErrorResponseUnverifiable
	result, err := harness.executor.Execute(context.Background(), harness.query)
	if !errors.Is(err, ErrCleanupOutcomeUnknown) ||
		!errors.Is(err, ingest.ErrArtifactResponseUnverifiable) ||
		result.Status != ExecutionReadyForDisposition ||
		result.DeleteOutcome != ingest.CleanupDeleteUnknown ||
		result.ErrorClass != ingest.CleanupExecutionErrorResponseUnverifiable {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	want := []string{"initialize", "begin_raw", "delete_raw", "record_raw_outcome"}
	if !reflect.DeepEqual(harness.events, want) {
		t.Fatalf("unverifiable events = %#v, want %#v", harness.events, want)
	}
	if harness.control.ledger.Phase != ingest.CleanupExecutionPhaseRawOutcomeRecorded ||
		harness.control.ledger.Raw.DeleteOutcome != ingest.CleanupDeleteUnknown {
		t.Fatalf("durable unverifiable ledger = %#v", harness.control.ledger)
	}
}

func TestCleanupPhaseExecutorResumesKnownOutcomeAtSignedAudit(t *testing.T) {
	harness := newCleanupPhaseHarness(t)
	harness.control.ledger = terminalLedger(
		ingest.CleanupExecutionPhaseRawOutcomeRecorded, 3,
	)
	result, err := harness.executor.Execute(context.Background(), harness.query)
	if err != nil || result.Status != ExecutionReadyForFinalization {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	if len(harness.events) < 2 || harness.events[0] != "initialize" ||
		harness.events[1] != "authorize_raw_audit" {
		t.Fatalf("resume events = %#v", harness.events)
	}
	for _, event := range harness.events {
		if event == "begin_raw" || event == "delete_raw" {
			t.Fatalf("known outcome repeated raw mutation: %#v", harness.events)
		}
	}
}

func TestCleanupPhaseExecutorUnknownDurableOutcomeNeverAudits(t *testing.T) {
	harness := newCleanupPhaseHarness(t)
	harness.control.ledger = terminalLedger(
		ingest.CleanupExecutionPhaseRawOutcomeRecorded, 3,
	)
	harness.control.ledger.ErrorClass = ingest.CleanupExecutionErrorProviderTimeout
	harness.control.ledger.Raw.DeleteOutcome = ingest.CleanupDeleteUnknown
	result, err := harness.executor.Execute(context.Background(), harness.query)
	if !errors.Is(err, ErrCleanupOutcomeUnknown) || result.Status != ExecutionReadyForDisposition ||
		result.ErrorClass != ingest.CleanupExecutionErrorProviderTimeout ||
		result.terminalIntent.command.ExpectedPhase != ingest.CleanupExecutionPhaseRawOutcomeRecorded {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	if !reflect.DeepEqual(harness.events, []string{"initialize"}) {
		t.Fatalf("unknown durable events = %#v", harness.events)
	}
}

func TestCleanupPhaseExecutorRestoresManifestDurableUnknownDispositionIntent(t *testing.T) {
	harness := newCleanupPhaseHarness(t)
	harness.control.ledger = terminalLedger(
		ingest.CleanupExecutionPhaseManifestOutcomeRecorded, 6,
	)
	harness.control.ledger.ErrorClass = ingest.CleanupExecutionErrorResponseUnverifiable
	harness.control.ledger.Manifest.DeleteOutcome = ingest.CleanupDeleteUnknown
	harness.control.ledger.Manifest.AuditOutcome = ""
	result, err := harness.executor.Execute(context.Background(), harness.query)
	if !errors.Is(err, ErrCleanupOutcomeUnknown) || result.Status != ExecutionReadyForDisposition ||
		result.Artifact != ingest.CleanupArtifactExecutionManifest ||
		result.terminalIntent.command.ExpectedPhase != ingest.CleanupExecutionPhaseManifestOutcomeRecorded ||
		result.terminalIntent.command.ExpectedLedgerRevision != 6 ||
		result.terminalIntent.command.ErrorClass != ingest.CleanupExecutionErrorResponseUnverifiable {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	if !reflect.DeepEqual(harness.events, []string{"initialize"}) {
		t.Fatalf("durable manifest unknown events = %#v", harness.events)
	}
}

func TestCleanupPhaseExecutorSealsBoundedProviderAndAuditFailures(t *testing.T) {
	t.Run("provider hard failure", func(t *testing.T) {
		harness := newCleanupPhaseHarness(t)
		harness.artifacts.rawErr = ingest.ErrArtifactPermissionDenied
		result, err := harness.executor.Execute(context.Background(), harness.query)
		if !errors.Is(err, ingest.ErrArtifactPermissionDenied) ||
			result.Status != ExecutionReadyForDisposition ||
			result.ErrorClass != ingest.CleanupExecutionErrorPermissionDenied ||
			result.terminalIntent.command.ExpectedPhase != ingest.CleanupExecutionPhaseRawDispatchRecorded {
			t.Fatalf("Execute() = %#v, %v", result, err)
		}
	})

	t.Run("audit inventory incomplete", func(t *testing.T) {
		harness := newCleanupPhaseHarness(t)
		harness.control.ledger = terminalLedger(
			ingest.CleanupExecutionPhaseRawOutcomeRecorded, 3,
		)
		harness.control.ledger.Raw.AuditOutcome = ""
		harness.control.ledger.Manifest.DeleteOutcome = ""
		harness.control.ledger.Manifest.AuditOutcome = ""
		harness.auditor.err = ingest.ErrCleanupExecutionInventoryIncomplete
		result, err := harness.executor.Execute(context.Background(), harness.query)
		if !errors.Is(err, ingest.ErrCleanupExecutionInventoryIncomplete) ||
			result.Status != ExecutionReadyForDisposition ||
			result.ErrorClass != ingest.CleanupExecutionErrorInventoryIncomplete ||
			result.terminalIntent.command.ExpectedPhase != ingest.CleanupExecutionPhaseRawOutcomeRecorded {
			t.Fatalf("Execute() = %#v, %v", result, err)
		}
	})
}

func TestCleanupPhaseExecutorPersistenceAndControlFailuresHaveNoTerminalIntent(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*cleanupPhaseHarness)
	}{
		{
			name: "outcome persistence response loss",
			configure: func(h *cleanupPhaseHarness) {
				h.artifacts.unknownRaw = true
				h.control.outcomeErr = ingest.ErrArtifactPermissionDenied
			},
		},
		{
			name: "audit authorization control error",
			configure: func(h *cleanupPhaseHarness) {
				h.control.ledger = terminalLedger(
					ingest.CleanupExecutionPhaseRawOutcomeRecorded, 3,
				)
				h.control.ledger.Raw.AuditOutcome = ""
				h.control.ledger.Manifest.DeleteOutcome = ""
				h.control.ledger.Manifest.AuditOutcome = ""
				h.control.authorizeAuditErr = ingest.ErrArtifactPermissionDenied
			},
		},
		{
			name: "audit persistence response loss",
			configure: func(h *cleanupPhaseHarness) {
				h.control.ledger = terminalLedger(
					ingest.CleanupExecutionPhaseRawOutcomeRecorded, 3,
				)
				h.control.ledger.Raw.AuditOutcome = ""
				h.control.ledger.Manifest.DeleteOutcome = ""
				h.control.ledger.Manifest.AuditOutcome = ""
				h.control.auditPersistenceErr = ingest.ErrArtifactPermissionDenied
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newCleanupPhaseHarness(t)
			test.configure(harness)
			result, err := harness.executor.Execute(context.Background(), harness.query)
			if err == nil || result.terminalIntent != (cleanupTerminalIntent{}) ||
				result.Status == ExecutionReadyForDisposition ||
				result.Status == ExecutionReadyForFinalization {
				t.Fatalf("Execute() = %#v, %v", result, err)
			}
		})
	}
}

func TestCleanupPhaseExecutorRejectsUnknownMutationStatusWithoutTerminalIntent(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*cleanupPhaseHarness)
	}{
		{name: "initialize", configure: func(h *cleanupPhaseHarness) {
			h.control.initializeStatus = "unexpected"
		}},
		{name: "begin", configure: func(h *cleanupPhaseHarness) {
			h.control.beginStatus = "unexpected"
		}},
		{name: "outcome", configure: func(h *cleanupPhaseHarness) {
			h.control.outcomeStatus = "unexpected"
		}},
		{name: "audit", configure: func(h *cleanupPhaseHarness) {
			h.control.ledger = terminalLedger(ingest.CleanupExecutionPhaseRawOutcomeRecorded, 3)
			h.control.auditStatus = "unexpected"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newCleanupPhaseHarness(t)
			test.configure(harness)
			result, err := harness.executor.Execute(context.Background(), harness.query)
			if !errors.Is(err, ErrInvalidPhaseExecution) ||
				result.terminalIntent != (cleanupTerminalIntent{}) {
				t.Fatalf("Execute() = %#v, %v", result, err)
			}
		})
	}
}

func TestCleanupExecutionProviderFailureMapperIsExhaustiveAndRejectsAmbiguity(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		class ingest.CleanupExecutionErrorClass
		count int
	}{
		{name: "timeout", err: ingest.ErrArtifactProviderTimeout, class: ingest.CleanupExecutionErrorProviderTimeout, count: 1},
		{name: "cancelled", err: ingest.ErrArtifactProviderCancelled, class: ingest.CleanupExecutionErrorProviderCancelled, count: 1},
		{name: "unavailable", err: ingest.ErrArtifactProviderUnavailable, class: ingest.CleanupExecutionErrorProviderUnavailable, count: 1},
		{name: "unverifiable", err: ingest.ErrArtifactResponseUnverifiable, class: ingest.CleanupExecutionErrorResponseUnverifiable, count: 1},
		{name: "quota", err: ingest.ErrArtifactQuotaLimited, class: ingest.CleanupExecutionErrorQuotaLimited, count: 1},
		{name: "inventory", err: ingest.ErrCleanupExecutionInventoryIncomplete, class: ingest.CleanupExecutionErrorInventoryIncomplete, count: 1},
		{name: "permission", err: ingest.ErrArtifactPermissionDenied, class: ingest.CleanupExecutionErrorPermissionDenied, count: 1},
		{name: "precondition", err: ingest.ErrArtifactPreconditionDrift, class: ingest.CleanupExecutionErrorPreconditionDrift, count: 1},
		{name: "generation", err: ingest.ErrCleanupExecutionGenerationDrift, class: ingest.CleanupExecutionErrorGenerationDrift, count: 1},
		{name: "lineage", err: ingest.ErrCleanupExecutionLineageMismatch, class: ingest.CleanupExecutionErrorLineageMismatch, count: 1},
		{name: "same class join", err: errors.Join(context.DeadlineExceeded, ingest.ErrArtifactProviderTimeout), class: ingest.CleanupExecutionErrorProviderTimeout, count: 1},
		{name: "distinct class join", err: errors.Join(ingest.ErrArtifactQuotaLimited, ingest.ErrArtifactPermissionDenied), count: 2},
		{name: "typed plus generic", err: errors.Join(ingest.ErrArtifactPermissionDenied, ingest.ErrCleanupExecutionUnavailable), count: 0},
		{name: "typed plus internal", err: errors.Join(ingest.ErrArtifactPermissionDenied, errors.New("internal details")), count: 0},
		{name: "custom is spoof", err: spoofedCleanupTerminalError{}, count: 0},
		{name: "generic unavailable", err: ingest.ErrCleanupExecutionUnavailable, count: 0},
		{name: "internal", err: errors.New("internal details"), count: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			class, count := cleanupExecutionErrorClassForProviderFailure(test.err)
			if class != test.class || count != test.count {
				t.Fatalf("mapper(%v) = %q/%d, want %q/%d", test.err, class, count, test.class, test.count)
			}
		})
	}
}

type spoofedCleanupTerminalError struct{}

func (spoofedCleanupTerminalError) Error() string { return "spoofed typed error" }

func (spoofedCleanupTerminalError) Is(target error) bool {
	return target == ingest.ErrArtifactPermissionDenied
}

func TestCleanupTerminalDurableUnknownRejectsUnrecognizedDiagnosticResidue(t *testing.T) {
	ledger := terminalLedger(ingest.CleanupExecutionPhaseRawOutcomeRecorded, 3)
	ledger.Raw.AuditOutcome = ""
	ledger.Manifest.DeleteOutcome = ""
	ledger.Manifest.AuditOutcome = ""
	ledger.Raw.DeleteOutcome = ingest.CleanupDeleteUnknown
	ledger.ErrorClass = ingest.CleanupExecutionErrorProviderTimeout
	_, err := newCleanupDispositionIntent(
		terminalQuery(), ledger, ledger.ErrorClass, cleanupTerminalIntentDurableUnknown,
		errors.Join(ErrCleanupOutcomeUnknown, errors.New("unbounded residue")),
	)
	if !errors.Is(err, ErrInvalidPhaseExecution) {
		t.Fatalf("newCleanupDispositionIntent() error = %v", err)
	}
}

func TestCleanupTerminalIntentRejectsMalformedDurableLedgerShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ingest.CleanupExecutionLedger)
	}{
		{name: "unknown delete enum", mutate: func(ledger *ingest.CleanupExecutionLedger) {
			ledger.Raw.DeleteOutcome = "arbitrary"
		}},
		{name: "missing dispatch time", mutate: func(ledger *ingest.CleanupExecutionLedger) {
			ledger.Raw.DispatchedAt = time.Time{}
		}},
		{name: "outcome before dispatch", mutate: func(ledger *ingest.CleanupExecutionLedger) {
			ledger.Raw.OutcomeRecordedAt = ledger.Raw.DispatchedAt.Add(-time.Second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ledger := terminalLedger(ingest.CleanupExecutionPhaseManifestAbsenceConfirmed, 7)
			test.mutate(&ledger)
			_, err := newCleanupFinalizationIntent(terminalQuery(), ledger)
			if !errors.Is(err, ErrInvalidPhaseExecution) {
				t.Fatalf("newCleanupFinalizationIntent() error = %v", err)
			}
		})
	}
}

type cleanupPhaseHarness struct {
	events    []string
	query     ingest.CleanupExecutionQuery
	control   *fakeCleanupPhaseControl
	artifacts *fakeCleanupArtifactExecutor
	auditor   *fakeCleanupAbsenceAuditor
	executor  *PhaseExecutor
}

func newCleanupPhaseHarness(t *testing.T) *cleanupPhaseHarness {
	t.Helper()
	harness := &cleanupPhaseHarness{
		query: ingest.CleanupExecutionQuery{
			TenantID:       "11111111-1111-4111-8111-111111111111",
			ReservationKey: strings.Repeat("a", 64),
			AttemptID:      "77777777-7777-4777-8777-777777777777",
		},
	}
	harness.control = &fakeCleanupPhaseControl{events: &harness.events}
	harness.control.ledger = ingest.CleanupExecutionLedger{
		SchemaVersion:  ingest.CleanupExecutionLedgerSchemaVersion,
		DecisionDomain: ingest.CleanupExecutionDecisionExpiry,
		TargetHash:     strings.Repeat("b", 64), PlanHash: strings.Repeat("c", 64),
		ReceiptRevision: 5,
		Fence: ingest.LeaseFence{
			OwnerID: harness.query.AttemptID, Token: 9,
			ExpiresAt: time.Date(2026, time.July, 23, 10, 0, 0, 0, time.UTC),
		},
		Revision: 1, Phase: ingest.CleanupExecutionPhasePlanned,
		Raw:      ingest.CleanupArtifactExecutionLedger{Targeted: true},
		Manifest: ingest.CleanupArtifactExecutionLedger{Targeted: true},
	}
	harness.artifacts = &fakeCleanupArtifactExecutor{events: &harness.events}
	harness.auditor = &fakeCleanupAbsenceAuditor{events: &harness.events}
	executor, err := newPhaseExecutor(
		harness.control, harness.artifacts, harness.auditor,
		time.Second, DefaultMaxPhaseSteps,
	)
	if err != nil {
		t.Fatalf("newPhaseExecutor() = %v", err)
	}
	harness.executor = executor
	return harness
}

type fakeCleanupPhaseControl struct {
	events              *[]string
	ledger              ingest.CleanupExecutionLedger
	replayRaw           bool
	outcomeErr          error
	authorizeAuditErr   error
	auditPersistenceErr error
	initializeStatus    ingest.CleanupExecutionMutationStatus
	beginStatus         ingest.CleanupExecutionMutationStatus
	outcomeStatus       ingest.CleanupExecutionMutationStatus
	auditStatus         ingest.CleanupExecutionMutationStatus
}

func (f *fakeCleanupPhaseControl) InitializeCleanupExecutionLedger(
	_ context.Context,
	_ ingest.CleanupExecutionQuery,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	f.add("initialize")
	status := f.initializeStatus
	if status == "" {
		status = ingest.CleanupExecutionMutationReplayed
	}
	return f.ledger, status, nil
}

func (f *fakeCleanupPhaseControl) BeginCleanupArtifactExecution(
	_ context.Context,
	query ingest.CleanupExecutionQuery,
	artifact ingest.CleanupArtifactExecutionArtifact,
) (
	ingest.CleanupArtifactExecutionRequest,
	firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	ingest.CleanupExecutionLedger,
	ingest.CleanupExecutionMutationStatus,
	error,
) {
	f.add("begin_" + string(artifact))
	request := ingest.CleanupArtifactExecutionRequest{
		Query: query, ExpectedTargetHash: strings.Repeat("b", 64),
		ExpectedPlanHash: strings.Repeat("c", 64), ExpectedReceiptRevision: 5,
		Artifact: artifact, RequestHash: strings.Repeat("d", 64), Targeted: true,
	}
	status := f.beginStatus
	if status == "" {
		status = ingest.CleanupExecutionMutationApplied
	}
	if artifact == ingest.CleanupArtifactExecutionRaw {
		request.DispatchRevision = 2
		request.DispatchPhase = ingest.CleanupExecutionPhaseRawDispatchRecorded
		request.OutcomePhase = ingest.CleanupExecutionPhaseRawOutcomeRecorded
		f.ledger.Phase = ingest.CleanupExecutionPhaseRawDispatchRecorded
		f.ledger.Revision = 2
		f.ledger.Raw.DispatchedAt = cleanupPhaseObservedAt(0)
		if f.replayRaw {
			status = ingest.CleanupExecutionMutationReplayed
		}
	} else {
		request.DispatchRevision = 5
		request.DispatchPhase = ingest.CleanupExecutionPhaseManifestDispatchRecorded
		request.OutcomePhase = ingest.CleanupExecutionPhaseManifestOutcomeRecorded
		f.ledger.Phase = ingest.CleanupExecutionPhaseManifestDispatchRecorded
		f.ledger.Revision = 5
		f.ledger.Manifest.DispatchedAt = cleanupPhaseObservedAt(3)
	}
	return request, firebaseadapter.CleanupArtifactExecutionAuthorizationGrant{}, f.ledger, status, nil
}

func (f *fakeCleanupPhaseControl) RecordCleanupArtifactExecutionOutcome(
	_ context.Context,
	_ firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
	result ingest.CleanupArtifactExecutionResult,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	f.add("record_" + string(request.Artifact) + "_outcome")
	if f.outcomeErr != nil {
		return f.ledger, "", f.outcomeErr
	}
	f.ledger.Phase = request.OutcomePhase
	f.ledger.Revision = request.DispatchRevision + 1
	if request.Artifact == ingest.CleanupArtifactExecutionRaw {
		f.ledger.Raw.Targeted = true
		f.ledger.Raw.DeleteOutcome = result.DeleteOutcome
		f.ledger.Raw.OutcomeRecordedAt = cleanupPhaseObservedAt(1)
	} else {
		f.ledger.Manifest.Targeted = true
		f.ledger.Manifest.DeleteOutcome = result.DeleteOutcome
		f.ledger.Manifest.OutcomeRecordedAt = cleanupPhaseObservedAt(4)
	}
	f.ledger.ErrorClass = result.ErrorClass
	status := f.outcomeStatus
	if status == "" {
		status = ingest.CleanupExecutionMutationApplied
	}
	return f.ledger, status, nil
}

func (f *fakeCleanupPhaseControl) AuthorizeCleanupAbsenceAudit(
	_ context.Context,
	_ ingest.CleanupExecutionQuery,
	artifact ingest.CleanupAbsenceAuditArtifact,
) (
	ingest.CleanupAbsenceAuditRequest,
	firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
	error,
) {
	f.add("authorize_" + string(artifact) + "_audit")
	if f.authorizeAuditErr != nil {
		return ingest.CleanupAbsenceAuditRequest{},
			firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, f.authorizeAuditErr
	}
	return ingest.CleanupAbsenceAuditRequest{Artifact: artifact},
		firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, nil
}

func (f *fakeCleanupPhaseControl) RecordCleanupAbsenceAudit(
	_ context.Context,
	_ firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
	_ cleanupattest.Evidence,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	f.add("record_" + string(request.Artifact) + "_audit")
	if f.auditPersistenceErr != nil {
		return f.ledger, "", f.auditPersistenceErr
	}
	if request.Artifact == ingest.CleanupAbsenceAuditRaw {
		f.ledger.Phase = ingest.CleanupExecutionPhaseRawAbsenceConfirmed
		f.ledger.Revision = 4
		f.ledger.Raw.AuditOutcome = ingest.CleanupAuditConfirmedAbsent
		f.ledger.Raw.AuditedAt = cleanupPhaseObservedAt(2)
	} else {
		f.ledger.Phase = ingest.CleanupExecutionPhaseManifestAbsenceConfirmed
		f.ledger.Revision = 7
		f.ledger.Manifest.AuditOutcome = ingest.CleanupAuditConfirmedAbsent
		f.ledger.Manifest.AuditedAt = cleanupPhaseObservedAt(5)
	}
	status := f.auditStatus
	if status == "" {
		status = ingest.CleanupExecutionMutationApplied
	}
	return f.ledger, status, nil
}

func (f *fakeCleanupPhaseControl) add(event string) {
	*f.events = append(*f.events, event)
}

type fakeCleanupArtifactExecutor struct {
	events          *[]string
	unknownRaw      bool
	unknownRawErr   error
	unknownRawClass ingest.CleanupExecutionErrorClass
	rawErr          error
}

func (f *fakeCleanupArtifactExecutor) ExecuteCleanupArtifact(
	_ context.Context,
	_ firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
) (ingest.CleanupArtifactExecutionResult, error) {
	*f.events = append(*f.events, "delete_"+string(request.Artifact))
	if request.Artifact == ingest.CleanupArtifactExecutionRaw && f.rawErr != nil {
		return ingest.CleanupArtifactExecutionResult{}, f.rawErr
	}
	result := ingest.CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision: request.DispatchRevision,
		DeleteOutcome:    ingest.CleanupDeleteObserved,
		ObservedAt:       time.Date(2026, time.July, 22, 14, 0, 0, 0, time.UTC),
	}
	if request.Artifact == ingest.CleanupArtifactExecutionRaw && f.unknownRaw {
		result.DeleteOutcome = ingest.CleanupDeleteUnknown
		result.ErrorClass = f.unknownRawClass
		if result.ErrorClass == "" {
			result.ErrorClass = ingest.CleanupExecutionErrorProviderTimeout
		}
		if f.unknownRawErr == nil {
			f.unknownRawErr = ingest.ErrArtifactProviderTimeout
		}
		return result, f.unknownRawErr
	}
	return result, nil
}

type fakeCleanupAbsenceAuditor struct {
	events *[]string
	err    error
}

func (f *fakeCleanupAbsenceAuditor) AuditCleanupAbsence(
	_ context.Context,
	_ firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
) (cleanupattest.Evidence, error) {
	*f.events = append(*f.events, "audit_"+string(request.Artifact))
	if f.err != nil {
		return cleanupattest.Evidence{}, f.err
	}
	return cleanupattest.Evidence{}, nil
}

func cleanupPhaseObservedAt(offsetMinutes int) time.Time {
	return time.Date(2026, time.July, 23, 8, offsetMinutes, 0, 0, time.UTC)
}
