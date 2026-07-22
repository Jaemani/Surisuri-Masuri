package cleanupflow

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestTerminalOrchestratorRoutesExactlyOneTerminalMutation(t *testing.T) {
	tests := []struct {
		name             string
		phaseResult      ExecutionResult
		phaseErr         error
		finalization     ingest.CleanupExpiryFinalizationResult
		disposition      ingest.CleanupExecutionDispositionResult
		wantKind         TerminalKind
		wantFinalization int
		wantDisposition  int
	}{
		{
			name: "success finalization", phaseResult: finalizationPhaseResult(t),
			finalization: directFinalizationResult(t), wantKind: TerminalKindFinalization,
			wantFinalization: 1,
		},
		{
			name: "retry disposition", phaseResult: dispositionPhaseResult(
				t, ingest.CleanupExecutionErrorQuotaLimited, ingest.ErrArtifactQuotaLimited,
			),
			phaseErr:    ingest.ErrArtifactQuotaLimited,
			disposition: directDispositionResult(t, ingest.CleanupExecutionErrorQuotaLimited),
			wantKind:    TerminalKindDisposition, wantDisposition: 1,
		},
		{
			name: "hold disposition", phaseResult: dispositionPhaseResult(
				t, ingest.CleanupExecutionErrorPermissionDenied, ingest.ErrArtifactPermissionDenied,
			),
			phaseErr:    ingest.ErrArtifactPermissionDenied,
			disposition: directDispositionResult(t, ingest.CleanupExecutionErrorPermissionDenied),
			wantKind:    TerminalKindDisposition, wantDisposition: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedPhaseRunner{result: test.phaseResult, err: test.phaseErr}
			mutator := &countingTerminalMutator{
				finalizationResult: test.finalization, dispositionResult: test.disposition,
			}
			orchestrator := mustTerminalOrchestrator(t, runner, mutator, &scriptedTerminalResolver{})
			result, err := orchestrator.Run(context.Background(), terminalQuery())
			if err != nil || result.TerminalKind != test.wantKind ||
				result.CommitStatus != TerminalCommitCommitted ||
				mutator.finalizationCalls != test.wantFinalization ||
				mutator.dispositionCalls != test.wantDisposition {
				t.Fatalf("Run() = %#v, %v; calls = %d/%d", result, err,
					mutator.finalizationCalls, mutator.dispositionCalls)
			}
		})
	}
}

func TestTerminalOrchestratorNeverDerivesDispositionFromPublicResultOrError(t *testing.T) {
	tests := []struct {
		name   string
		result ExecutionResult
		err    error
	}{
		{
			name: "bounded error alone",
			result: ExecutionResult{
				Status:         ExecutionReadyForDisposition,
				Phase:          ingest.CleanupExecutionPhaseRawDispatchRecorded,
				LedgerRevision: 2, ErrorClass: ingest.CleanupExecutionErrorQuotaLimited,
			},
			err: ingest.ErrArtifactQuotaLimited,
		},
		{
			name: "public success fields alone",
			result: ExecutionResult{
				Status:         ExecutionReadyForFinalization,
				Phase:          ingest.CleanupExecutionPhaseManifestAbsenceConfirmed,
				LedgerRevision: 7,
			},
		},
		{name: "generic unavailable", err: ingest.ErrCleanupExecutionUnavailable},
		{
			name: "sealed disposition without diagnostic",
			result: dispositionPhaseResult(
				t, ingest.CleanupExecutionErrorQuotaLimited, ingest.ErrArtifactQuotaLimited,
			),
		},
		{
			name:   "sealed finalization with error",
			result: finalizationPhaseResult(t), err: errors.New("unexpected finalization error"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutator := &countingTerminalMutator{}
			orchestrator := mustTerminalOrchestrator(
				t, &scriptedPhaseRunner{result: test.result, err: test.err},
				mutator, &scriptedTerminalResolver{},
			)
			_, err := orchestrator.Run(context.Background(), terminalQuery())
			if !errors.Is(err, ErrCleanupTerminalUnavailable) ||
				mutator.finalizationCalls != 0 || mutator.dispositionCalls != 0 {
				t.Fatalf("Run() error = %v; calls = %d/%d", err,
					mutator.finalizationCalls, mutator.dispositionCalls)
			}
		})
	}
}

func TestTerminalOrchestratorCorrelatesFinalizationResponseLossWithoutMutationRetry(t *testing.T) {
	tests := []struct {
		name       string
		outcome    ingest.CleanupExpiryFinalizationOutcome
		resolveErr error
		wantErr    error
	}{
		{name: "committed", outcome: committedFinalizationOutcome()},
		{name: "not committed", outcome: notCommittedFinalizationOutcome(), wantErr: ErrCleanupTerminalNotCommitted},
		{name: "unverifiable", outcome: ingest.CleanupExpiryFinalizationOutcome{
			AttemptID:    terminalQuery().AttemptID,
			CommitStatus: ingest.CleanupExpiryFinalizationUnverifiable,
		}, wantErr: ErrCleanupTerminalUnverifiable},
		{name: "resolver unavailable", resolveErr: errors.New("read unavailable"), wantErr: ErrCleanupTerminalUnavailable},
		{name: "outcome and error", outcome: committedFinalizationOutcome(),
			resolveErr: errors.New("ambiguous resolver"), wantErr: ErrCleanupTerminalUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := finalizationOutcomeQuery()
			mutator := &countingTerminalMutator{
				finalizationResult: ingest.CleanupExpiryFinalizationResult{OutcomeQuery: query},
				finalizationErr:    errors.New("commit response lost"),
			}
			resolver := &scriptedTerminalResolver{
				finalizationOutcome: test.outcome, finalizationErr: test.resolveErr,
			}
			orchestrator := mustTerminalOrchestrator(
				t, &scriptedPhaseRunner{result: finalizationPhaseResult(t)}, mutator, resolver,
			)
			result, err := orchestrator.Run(context.Background(), terminalQuery())
			if !sameError(err, test.wantErr) || mutator.finalizationCalls != 1 ||
				mutator.dispositionCalls != 0 || resolver.finalizationCalls != 1 {
				t.Fatalf("Run() = %#v, %v; calls = %d/%d/%d", result, err,
					mutator.finalizationCalls, mutator.dispositionCalls, resolver.finalizationCalls)
			}
			if test.wantErr == nil && (result.CommitStatus != TerminalCommitCommitted ||
				result.Diagnostic != TerminalDiagnosticMutationResponseLost) {
				t.Fatalf("correlated committed result = %#v", result)
			}
		})
	}
}

func TestTerminalOrchestratorCorrelatesDispositionResponseLossWithoutMutationRetry(t *testing.T) {
	class := ingest.CleanupExecutionErrorPermissionDenied
	diagnostic := ingest.ErrArtifactPermissionDenied
	tests := []struct {
		name       string
		outcome    ingest.CleanupExecutionDispositionOutcome
		resolveErr error
		wantErr    error
	}{
		{name: "committed", outcome: committedDispositionOutcome(class)},
		{name: "not committed", outcome: notCommittedDispositionOutcome(), wantErr: ErrCleanupTerminalNotCommitted},
		{name: "unverifiable", outcome: ingest.CleanupExecutionDispositionOutcome{
			AttemptID:    terminalQuery().AttemptID,
			CommitStatus: ingest.CleanupExecutionDispositionUnverifiable,
		}, wantErr: ErrCleanupTerminalUnverifiable},
		{name: "resolver unavailable", resolveErr: errors.New("read unavailable"), wantErr: ErrCleanupTerminalUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutator := &countingTerminalMutator{
				dispositionResult: ingest.CleanupExecutionDispositionResult{
					OutcomeQuery: dispositionOutcomeQuery(class),
				},
				dispositionErr: errors.New("commit response lost"),
			}
			resolver := &scriptedTerminalResolver{
				dispositionOutcome: test.outcome, dispositionErr: test.resolveErr,
			}
			orchestrator := mustTerminalOrchestrator(t, &scriptedPhaseRunner{
				result: dispositionPhaseResult(t, class, diagnostic), err: diagnostic,
			}, mutator, resolver)
			result, err := orchestrator.Run(context.Background(), terminalQuery())
			if !sameError(err, test.wantErr) || mutator.finalizationCalls != 0 ||
				mutator.dispositionCalls != 1 || resolver.dispositionCalls != 1 {
				t.Fatalf("Run() = %#v, %v; calls = %d/%d/%d", result, err,
					mutator.finalizationCalls, mutator.dispositionCalls, resolver.dispositionCalls)
			}
		})
	}
}

func TestTerminalOrchestratorCorrelationSurvivesParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	mutator := &countingTerminalMutator{
		finalizationResult: ingest.CleanupExpiryFinalizationResult{
			OutcomeQuery: finalizationOutcomeQuery(),
		},
		finalizationErr: errors.New("commit response lost"), cancel: cancel,
	}
	resolver := &scriptedTerminalResolver{
		finalizationOutcome: committedFinalizationOutcome(), inspectContext: true,
	}
	orchestrator := mustTerminalOrchestrator(
		t, &scriptedPhaseRunner{result: finalizationPhaseResult(t)}, mutator, resolver,
	)
	result, err := orchestrator.Run(parent, terminalQuery())
	if err != nil || result.CommitStatus != TerminalCommitCommitted || parent.Err() == nil ||
		!resolver.contextWasLive || !resolver.contextHadDeadline ||
		mutator.finalizationCalls != 1 || mutator.dispositionCalls != 0 {
		t.Fatalf("Run() = %#v, %v; parent=%v resolver=%v/%v calls=%d/%d", result, err,
			parent.Err(), resolver.contextWasLive, resolver.contextHadDeadline,
			mutator.finalizationCalls, mutator.dispositionCalls)
	}
}

func TestTerminalOrchestratorInvalidResponseLossQueryDoesNotCorrelate(t *testing.T) {
	mutator := &countingTerminalMutator{
		finalizationErr: errors.New("mutation failed without query"),
	}
	resolver := &scriptedTerminalResolver{finalizationOutcome: committedFinalizationOutcome()}
	orchestrator := mustTerminalOrchestrator(
		t, &scriptedPhaseRunner{result: finalizationPhaseResult(t)}, mutator, resolver,
	)
	_, err := orchestrator.Run(context.Background(), terminalQuery())
	if !errors.Is(err, ErrCleanupTerminalUnavailable) || mutator.finalizationCalls != 1 ||
		resolver.finalizationCalls != 0 {
		t.Fatalf("Run() error = %v; calls = %d/%d", err,
			mutator.finalizationCalls, resolver.finalizationCalls)
	}
}

func TestTerminalOrchestratorRejectsCorruptDirectTerminalPayload(t *testing.T) {
	tests := []struct {
		name   string
		result ingest.CleanupExpiryFinalizationResult
	}{
		{name: "wrong ledger target", result: func() ingest.CleanupExpiryFinalizationResult {
			result := directFinalizationResult(t)
			result.Ledger.TargetHash = strings.Repeat("f", 64)
			return result
		}()},
		{name: "sensitive malformed evidence", result: func() ingest.CleanupExpiryFinalizationResult {
			result := directFinalizationResult(t)
			result.Ledger.EvidenceHash = "private/object/path"
			return result
		}()},
		{name: "active lease residue", result: func() ingest.CleanupExpiryFinalizationResult {
			result := directFinalizationResult(t)
			result.Receipt.LeaseOwnerID = terminalQuery().AttemptID
			return result
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutator := &countingTerminalMutator{finalizationResult: test.result}
			resolver := &scriptedTerminalResolver{}
			orchestrator := mustTerminalOrchestrator(
				t, &scriptedPhaseRunner{result: finalizationPhaseResult(t)}, mutator, resolver,
			)
			_, err := orchestrator.Run(context.Background(), terminalQuery())
			if !errors.Is(err, ErrCleanupTerminalUnavailable) ||
				mutator.finalizationCalls != 1 || mutator.dispositionCalls != 0 ||
				resolver.finalizationCalls != 0 {
				t.Fatalf("Run() error = %v; calls = %d/%d/%d", err,
					mutator.finalizationCalls, mutator.dispositionCalls, resolver.finalizationCalls)
			}
		})
	}
}

func TestTerminalOrchestratorRejectsMalformedCorrelatedCommittedOutcome(t *testing.T) {
	outcome := committedFinalizationOutcome()
	outcome.EvidenceHash = "tenant/private/path"
	mutator := &countingTerminalMutator{
		finalizationResult: ingest.CleanupExpiryFinalizationResult{
			OutcomeQuery: finalizationOutcomeQuery(),
		},
		finalizationErr: errors.New("commit response lost"),
	}
	resolver := &scriptedTerminalResolver{finalizationOutcome: outcome}
	orchestrator := mustTerminalOrchestrator(
		t, &scriptedPhaseRunner{result: finalizationPhaseResult(t)}, mutator, resolver,
	)
	result, err := orchestrator.Run(context.Background(), terminalQuery())
	if !errors.Is(err, ErrCleanupTerminalUnavailable) ||
		result.Diagnostic != TerminalDiagnosticMutationResponseLost ||
		result.EvidenceHash != "" || mutator.finalizationCalls != 1 ||
		resolver.finalizationCalls != 1 {
		t.Fatalf("Run() = %#v, %v; calls = %d/%d", result, err,
			mutator.finalizationCalls, resolver.finalizationCalls)
	}
}

func TestNewTerminalOrchestratorRejectsDifferentControlStore(t *testing.T) {
	storeA := &firebaseadapter.FirestoreAdmissionStore{}
	storeB := &firebaseadapter.FirestoreAdmissionStore{}
	events := []string{}
	runner, err := newPhaseExecutor(
		storeA,
		&fakeCleanupArtifactExecutor{events: &events},
		&fakeCleanupAbsenceAuditor{events: &events},
		time.Second,
		DefaultMaxPhaseSteps,
	)
	if err != nil {
		t.Fatalf("newPhaseExecutor() = %v", err)
	}
	if _, err := NewTerminalOrchestrator(runner, storeB); !errors.Is(err, ErrInvalidTerminalOrchestration) {
		t.Fatalf("NewTerminalOrchestrator() error = %v", err)
	}
	if _, err := NewTerminalOrchestrator(runner, storeA); err != nil {
		t.Fatalf("NewTerminalOrchestrator(same store) error = %v", err)
	}
}

func TestTerminalResultHasNoFullControlOrSensitiveTypes(t *testing.T) {
	typeOf := reflect.TypeOf(TerminalResult{})
	for i := 0; i < typeOf.NumField(); i++ {
		field := typeOf.Field(i)
		lower := strings.ToLower(field.Name)
		for _, forbidden := range []string{"path", "payload", "coordinate", "firebaseuid", "personuid"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("TerminalResult exposes forbidden field %s", field.Name)
			}
		}
		switch field.Type {
		case reflect.TypeOf(ingest.Receipt{}),
			reflect.TypeOf(ingest.CleanupExecutionDispositionCommand{}),
			reflect.TypeOf(ingest.CleanupExpiryFinalizationOutcomeQuery{}),
			reflect.TypeOf(ingest.CleanupExecutionDispositionOutcomeQuery{}):
			t.Fatalf("TerminalResult exposes full control type %s", field.Type)
		}
	}
}

type scriptedPhaseRunner struct {
	result ExecutionResult
	err    error
	calls  int
}

func (r *scriptedPhaseRunner) Execute(
	_ context.Context,
	_ ingest.CleanupExecutionQuery,
) (ExecutionResult, error) {
	r.calls++
	return r.result, r.err
}

type countingTerminalMutator struct {
	finalizationResult ingest.CleanupExpiryFinalizationResult
	finalizationErr    error
	dispositionResult  ingest.CleanupExecutionDispositionResult
	dispositionErr     error
	finalizationCalls  int
	dispositionCalls   int
	cancel             context.CancelFunc
}

func (m *countingTerminalMutator) FinalizeExpiredCleanup(
	ctx context.Context,
	_ ingest.CleanupExecutionQuery,
) (ingest.CleanupExpiryFinalizationResult, error) {
	m.finalizationCalls++
	if m.cancel != nil {
		m.cancel()
	}
	_ = ctx.Err()
	return m.finalizationResult, m.finalizationErr
}

func (m *countingTerminalMutator) DisposeCleanupExecution(
	_ context.Context,
	_ ingest.CleanupExecutionDispositionCommand,
) (ingest.CleanupExecutionDispositionResult, error) {
	m.dispositionCalls++
	return m.dispositionResult, m.dispositionErr
}

type scriptedTerminalResolver struct {
	finalizationOutcome ingest.CleanupExpiryFinalizationOutcome
	finalizationErr     error
	dispositionOutcome  ingest.CleanupExecutionDispositionOutcome
	dispositionErr      error
	finalizationCalls   int
	dispositionCalls    int
	inspectContext      bool
	contextWasLive      bool
	contextHadDeadline  bool
}

func (r *scriptedTerminalResolver) ResolveExpiryFinalization(
	ctx context.Context,
	_ ingest.CleanupExpiryFinalizationOutcomeQuery,
) (ingest.CleanupExpiryFinalizationOutcome, error) {
	r.finalizationCalls++
	if r.inspectContext {
		r.contextWasLive = ctx.Err() == nil
		_, r.contextHadDeadline = ctx.Deadline()
	}
	return r.finalizationOutcome, r.finalizationErr
}

func (r *scriptedTerminalResolver) ResolveExecutionDisposition(
	ctx context.Context,
	_ ingest.CleanupExecutionDispositionOutcomeQuery,
) (ingest.CleanupExecutionDispositionOutcome, error) {
	r.dispositionCalls++
	if r.inspectContext {
		r.contextWasLive = ctx.Err() == nil
		_, r.contextHadDeadline = ctx.Deadline()
	}
	return r.dispositionOutcome, r.dispositionErr
}

func mustTerminalOrchestrator(
	t *testing.T,
	runner phaseRunner,
	mutator terminalMutator,
	resolver terminalOutcomeResolver,
) *TerminalOrchestrator {
	t.Helper()
	orchestrator, err := newTerminalOrchestrator(
		runner, mutator, resolver, 100*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("newTerminalOrchestrator() = %v", err)
	}
	return orchestrator
}

func terminalQuery() ingest.CleanupExecutionQuery {
	return ingest.CleanupExecutionQuery{
		TenantID:       "11111111-1111-4111-8111-111111111111",
		ReservationKey: strings.Repeat("a", 64),
		AttemptID:      "77777777-7777-4777-8777-777777777777",
	}
}

func terminalLedger(
	phase ingest.CleanupExecutionPhase,
	revision int64,
) ingest.CleanupExecutionLedger {
	t0 := time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC)
	ledger := ingest.CleanupExecutionLedger{
		SchemaVersion:  ingest.CleanupExecutionLedgerSchemaVersion,
		DecisionDomain: ingest.CleanupExecutionDecisionExpiry,
		TargetHash:     strings.Repeat("b", 64), PlanHash: strings.Repeat("c", 64),
		ReceiptRevision: 5,
		Fence: ingest.LeaseFence{
			OwnerID: terminalQuery().AttemptID, Token: 9,
			ExpiresAt: time.Date(2026, time.July, 23, 10, 0, 0, 0, time.UTC),
		},
		Phase: phase, Revision: revision,
		Raw:      ingest.CleanupArtifactExecutionLedger{Targeted: true},
		Manifest: ingest.CleanupArtifactExecutionLedger{Targeted: true},
	}
	if revision >= 2 {
		ledger.Raw.DispatchedAt = t0
	}
	if revision >= 3 {
		ledger.Raw.DeleteOutcome = ingest.CleanupDeleteObserved
		ledger.Raw.OutcomeRecordedAt = t0.Add(time.Minute)
	}
	if revision >= 4 {
		ledger.Raw.AuditOutcome = ingest.CleanupAuditConfirmedAbsent
		ledger.Raw.AuditedAt = t0.Add(2 * time.Minute)
	}
	if revision >= 5 {
		ledger.Manifest.DispatchedAt = t0.Add(3 * time.Minute)
	}
	if revision >= 6 {
		ledger.Manifest.DeleteOutcome = ingest.CleanupDeleteObserved
		ledger.Manifest.OutcomeRecordedAt = t0.Add(4 * time.Minute)
	}
	if revision >= 7 {
		ledger.Manifest.AuditOutcome = ingest.CleanupAuditConfirmedAbsent
		ledger.Manifest.AuditedAt = t0.Add(5 * time.Minute)
	}
	return ledger
}

func finalizationPhaseResult(t *testing.T) ExecutionResult {
	t.Helper()
	ledger := terminalLedger(ingest.CleanupExecutionPhaseManifestAbsenceConfirmed, 7)
	intent, err := newCleanupFinalizationIntent(terminalQuery(), ledger)
	if err != nil {
		t.Fatalf("newCleanupFinalizationIntent() = %v", err)
	}
	return ExecutionResult{
		Status: ExecutionReadyForFinalization,
		Phase:  ledger.Phase, LedgerRevision: ledger.Revision, Steps: 1,
		terminalIntent: intent,
	}
}

func dispositionPhaseResult(
	t *testing.T,
	class ingest.CleanupExecutionErrorClass,
	diagnostic error,
) ExecutionResult {
	t.Helper()
	ledger := terminalLedger(ingest.CleanupExecutionPhaseRawDispatchRecorded, 2)
	ledger.Raw.DeleteOutcome = ""
	ledger.Raw.AuditOutcome = ""
	ledger.Manifest.DeleteOutcome = ""
	ledger.Manifest.AuditOutcome = ""
	intent, err := newCleanupDispositionIntent(
		terminalQuery(), ledger, class, cleanupTerminalIntentBoundedFailure, diagnostic,
	)
	if err != nil {
		t.Fatalf("newCleanupDispositionIntent() = %v", err)
	}
	return ExecutionResult{
		Status: ExecutionReadyForDisposition,
		Phase:  ledger.Phase, LedgerRevision: ledger.Revision,
		ErrorClass: class, Steps: 1, terminalIntent: intent,
	}
}

func finalizationOutcomeQuery() ingest.CleanupExpiryFinalizationOutcomeQuery {
	ledger := terminalLedger(ingest.CleanupExecutionPhaseManifestAbsenceConfirmed, 7)
	return ingest.CleanupExpiryFinalizationOutcomeQuery{
		TenantID: terminalQuery().TenantID, ReservationKey: terminalQuery().ReservationKey,
		AttemptID: terminalQuery().AttemptID, ExpectedTargetHash: ledger.TargetHash,
		ExpectedPlanHash: ledger.PlanHash, ExpectedFence: ledger.Fence,
		ExpectedPreReceiptRevision: 5, ExpectedFinalReceiptRevision: 6,
		ExpectedPreLedgerRevision: 7, ExpectedFinalLedgerRevision: 8,
	}
}

func dispositionOutcomeQuery(
	class ingest.CleanupExecutionErrorClass,
) ingest.CleanupExecutionDispositionOutcomeQuery {
	ledger := terminalLedger(ingest.CleanupExecutionPhaseRawDispatchRecorded, 2)
	ledger.Raw.DeleteOutcome = ""
	ledger.Raw.AuditOutcome = ""
	ledger.Manifest.DeleteOutcome = ""
	ledger.Manifest.AuditOutcome = ""
	policy, _ := ingest.CleanupExecutionFailurePolicyFor(class)
	return ingest.CleanupExecutionDispositionOutcomeQuery{
		TenantID: terminalQuery().TenantID, ReservationKey: terminalQuery().ReservationKey,
		AttemptID: terminalQuery().AttemptID, ExpectedTargetHash: ledger.TargetHash,
		ExpectedPlanHash: ledger.PlanHash, ExpectedFence: ledger.Fence,
		ExpectedPreReceiptRevision: 5, ExpectedFinalReceiptRevision: 6,
		ExpectedLedgerRevision: 2, ExpectedPhase: ingest.CleanupExecutionPhaseRawDispatchRecorded,
		ExpectedDisposition: policy.Disposition, ExpectedErrorClass: class,
	}
}

func directFinalizationResult(t *testing.T) ingest.CleanupExpiryFinalizationResult {
	t.Helper()
	completedAt := time.Date(2026, time.July, 23, 9, 0, 0, 0, time.UTC)
	retentionFloor := completedAt.Add(24 * time.Hour)
	purge, err := ingest.CleanupPurgeEligibleAt(retentionFloor, completedAt)
	if err != nil {
		t.Fatalf("CleanupPurgeEligibleAt() = %v", err)
	}
	ledger := terminalLedger(ingest.CleanupExecutionPhaseManifestAbsenceConfirmed, 7)
	ledger.Phase = ingest.CleanupExecutionPhaseCompleted
	ledger.Revision = 8
	ledger.Disposition = ingest.CleanupExecutionDispositionComplete
	ledger.EvidenceHash = strings.Repeat("e", 64)
	ledger.CompletedAt = completedAt
	return ingest.CleanupExpiryFinalizationResult{
		Receipt: ingest.Receipt{
			TenantID: terminalQuery().TenantID, ReservationKey: terminalQuery().ReservationKey,
			State: ingest.ReceiptExpired, Revision: 6, FencingToken: 9,
			UpdatedAt: completedAt, ReceiptRetentionFloor: retentionFloor, PurgeEligibleAt: &purge,
		},
		Ledger:       ledger,
		OutcomeQuery: finalizationOutcomeQuery(),
	}
}

func directDispositionResult(
	t *testing.T,
	class ingest.CleanupExecutionErrorClass,
) ingest.CleanupExecutionDispositionResult {
	t.Helper()
	completedAt := time.Date(2026, time.July, 23, 9, 0, 0, 0, time.UTC)
	ledger := terminalLedger(ingest.CleanupExecutionPhaseRawDispatchRecorded, 2)
	ledger.Raw.DeleteOutcome = ""
	ledger.Raw.AuditOutcome = ""
	ledger.Manifest.DeleteOutcome = ""
	ledger.Manifest.AuditOutcome = ""
	policy, _ := ingest.CleanupExecutionFailurePolicyFor(class)
	ledger.Disposition = policy.Disposition
	ledger.ErrorClass = class
	ledger.EvidenceHash = strings.Repeat("e", 64)
	ledger.CompletedAt = completedAt
	next, hold, err := ingest.CleanupExecutionDispositionCursorAt(ledger.Fence, class, completedAt)
	if err != nil {
		t.Fatalf("CleanupExecutionDispositionCursorAt() = %v", err)
	}
	return ingest.CleanupExecutionDispositionResult{
		Receipt: ingest.Receipt{
			TenantID: terminalQuery().TenantID, ReservationKey: terminalQuery().ReservationKey,
			State: ingest.ReceiptCleanupPending, Revision: 6, FencingToken: 9,
			UpdatedAt: completedAt, CleanupDispositionAttemptID: terminalQuery().AttemptID,
			CleanupControlDisposition: policy.Disposition, LastCleanupErrorClass: class,
			NextCleanupAt: next, CleanupHoldReviewDueAt: hold,
		},
		Ledger: ledger, NextCleanupAt: next, HoldReviewDueAt: hold,
		OutcomeQuery: dispositionOutcomeQuery(class),
	}
}

func committedFinalizationOutcome() ingest.CleanupExpiryFinalizationOutcome {
	completedAt := time.Date(2026, time.July, 23, 9, 0, 0, 0, time.UTC)
	return ingest.CleanupExpiryFinalizationOutcome{
		AttemptID:    terminalQuery().AttemptID,
		CommitStatus: ingest.CleanupExpiryFinalizationCommitted,
		ReceiptState: ingest.ReceiptExpired, ReceiptRevision: 6,
		LedgerPhase: ingest.CleanupExecutionPhaseCompleted, LedgerRevision: 8,
		EvidenceHash: strings.Repeat("e", 64), CompletedAt: completedAt,
		PurgeEligibleAt: completedAt.Add(24 * time.Hour),
	}
}

func notCommittedFinalizationOutcome() ingest.CleanupExpiryFinalizationOutcome {
	return ingest.CleanupExpiryFinalizationOutcome{
		AttemptID:    terminalQuery().AttemptID,
		CommitStatus: ingest.CleanupExpiryFinalizationNotCommitted,
		ReceiptState: ingest.ReceiptCleanupPending, ReceiptRevision: 5,
		LedgerPhase: ingest.CleanupExecutionPhaseManifestAbsenceConfirmed, LedgerRevision: 7,
	}
}

func committedDispositionOutcome(
	class ingest.CleanupExecutionErrorClass,
) ingest.CleanupExecutionDispositionOutcome {
	completedAt := time.Date(2026, time.July, 23, 9, 0, 0, 0, time.UTC)
	ledger := terminalLedger(ingest.CleanupExecutionPhaseRawDispatchRecorded, 2)
	policy, _ := ingest.CleanupExecutionFailurePolicyFor(class)
	next, hold, _ := ingest.CleanupExecutionDispositionCursorAt(ledger.Fence, class, completedAt)
	return ingest.CleanupExecutionDispositionOutcome{
		AttemptID:       terminalQuery().AttemptID,
		CommitStatus:    ingest.CleanupExecutionDispositionCommitted,
		ReceiptRevision: 6, LedgerPhase: ledger.Phase, LedgerRevision: ledger.Revision,
		Disposition: policy.Disposition, ErrorClass: class,
		EvidenceHash: strings.Repeat("e", 64), CompletedAt: completedAt,
		NextCleanupAt: next, HoldReviewDueAt: hold,
	}
}

func notCommittedDispositionOutcome() ingest.CleanupExecutionDispositionOutcome {
	return ingest.CleanupExecutionDispositionOutcome{
		AttemptID:       terminalQuery().AttemptID,
		CommitStatus:    ingest.CleanupExecutionDispositionNotCommitted,
		ReceiptRevision: 5, LedgerPhase: ingest.CleanupExecutionPhaseRawDispatchRecorded,
		LedgerRevision: 2,
	}
}

func sameError(got error, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}
