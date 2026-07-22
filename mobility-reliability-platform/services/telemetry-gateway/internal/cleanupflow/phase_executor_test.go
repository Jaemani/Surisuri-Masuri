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
		result.Status != ExecutionUnknownOutcome ||
		result.DeleteOutcome != ingest.CleanupDeleteUnknown ||
		result.ErrorClass != ingest.CleanupExecutionErrorProviderTimeout {
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
		result.Status != ExecutionUnknownOutcome ||
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
	harness.control.ledger = ingest.CleanupExecutionLedger{
		Revision: 3, Phase: ingest.CleanupExecutionPhaseRawOutcomeRecorded,
		Raw: ingest.CleanupArtifactExecutionLedger{
			Targeted: true, DeleteOutcome: ingest.CleanupDeleteObserved,
		},
	}
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
	harness.control.ledger = ingest.CleanupExecutionLedger{
		Revision: 3, Phase: ingest.CleanupExecutionPhaseRawOutcomeRecorded,
		Raw: ingest.CleanupArtifactExecutionLedger{
			Targeted: true, DeleteOutcome: ingest.CleanupDeleteUnknown,
		},
	}
	result, err := harness.executor.Execute(context.Background(), harness.query)
	if !errors.Is(err, ErrCleanupOutcomeUnknown) || result.Status != ExecutionUnknownOutcome {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	if !reflect.DeepEqual(harness.events, []string{"initialize"}) {
		t.Fatalf("unknown durable events = %#v", harness.events)
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
	events    *[]string
	ledger    ingest.CleanupExecutionLedger
	replayRaw bool
}

func (f *fakeCleanupPhaseControl) InitializeCleanupExecutionLedger(
	_ context.Context,
	_ ingest.CleanupExecutionQuery,
) (ingest.CleanupExecutionLedger, ingest.CleanupExecutionMutationStatus, error) {
	f.add("initialize")
	return f.ledger, ingest.CleanupExecutionMutationReplayed, nil
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
	status := ingest.CleanupExecutionMutationApplied
	if artifact == ingest.CleanupArtifactExecutionRaw {
		request.DispatchRevision = 2
		request.DispatchPhase = ingest.CleanupExecutionPhaseRawDispatchRecorded
		request.OutcomePhase = ingest.CleanupExecutionPhaseRawOutcomeRecorded
		f.ledger.Phase = ingest.CleanupExecutionPhaseRawDispatchRecorded
		f.ledger.Revision = 2
		if f.replayRaw {
			status = ingest.CleanupExecutionMutationReplayed
		}
	} else {
		request.DispatchRevision = 5
		request.DispatchPhase = ingest.CleanupExecutionPhaseManifestDispatchRecorded
		request.OutcomePhase = ingest.CleanupExecutionPhaseManifestOutcomeRecorded
		f.ledger.Phase = ingest.CleanupExecutionPhaseManifestDispatchRecorded
		f.ledger.Revision = 5
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
	f.ledger.Phase = request.OutcomePhase
	f.ledger.Revision = request.DispatchRevision + 1
	if request.Artifact == ingest.CleanupArtifactExecutionRaw {
		f.ledger.Raw.Targeted = true
		f.ledger.Raw.DeleteOutcome = result.DeleteOutcome
	} else {
		f.ledger.Manifest.Targeted = true
		f.ledger.Manifest.DeleteOutcome = result.DeleteOutcome
	}
	return f.ledger, ingest.CleanupExecutionMutationApplied, nil
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
	if request.Artifact == ingest.CleanupAbsenceAuditRaw {
		f.ledger.Phase = ingest.CleanupExecutionPhaseRawAbsenceConfirmed
		f.ledger.Revision = 4
		f.ledger.Raw.AuditOutcome = ingest.CleanupAuditConfirmedAbsent
	} else {
		f.ledger.Phase = ingest.CleanupExecutionPhaseManifestAbsenceConfirmed
		f.ledger.Revision = 7
		f.ledger.Manifest.AuditOutcome = ingest.CleanupAuditConfirmedAbsent
	}
	return f.ledger, ingest.CleanupExecutionMutationApplied, nil
}

func (f *fakeCleanupPhaseControl) add(event string) {
	*f.events = append(*f.events, event)
}

type fakeCleanupArtifactExecutor struct {
	events          *[]string
	unknownRaw      bool
	unknownRawErr   error
	unknownRawClass ingest.CleanupExecutionErrorClass
}

func (f *fakeCleanupArtifactExecutor) ExecuteCleanupArtifact(
	_ context.Context,
	_ firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
) (ingest.CleanupArtifactExecutionResult, error) {
	*f.events = append(*f.events, "delete_"+string(request.Artifact))
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
}

func (f *fakeCleanupAbsenceAuditor) AuditCleanupAbsence(
	_ context.Context,
	_ firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
	request ingest.CleanupAbsenceAuditRequest,
) (cleanupattest.Evidence, error) {
	*f.events = append(*f.events, "audit_"+string(request.Artifact))
	return cleanupattest.Evidence{}, nil
}
