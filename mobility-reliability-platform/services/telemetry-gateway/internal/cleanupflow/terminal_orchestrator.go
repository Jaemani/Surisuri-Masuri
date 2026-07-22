package cleanupflow

import (
	"context"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const DefaultTerminalCorrelationTimeout = 5 * time.Second

var (
	ErrInvalidTerminalOrchestration = errors.New("cleanup terminal orchestration is invalid")
	ErrCleanupTerminalUnavailable   = errors.New("cleanup terminal outcome is unavailable")
	ErrCleanupTerminalNotCommitted  = errors.New("cleanup terminal mutation was not committed")
	ErrCleanupTerminalUnverifiable  = errors.New("cleanup terminal mutation is unverifiable")
)

type TerminalKind string

const (
	TerminalKindFinalization TerminalKind = "finalization"
	TerminalKindDisposition  TerminalKind = "disposition"
)

type TerminalCommitStatus string

const (
	TerminalCommitCommitted    TerminalCommitStatus = "committed"
	TerminalCommitNotCommitted TerminalCommitStatus = "not_committed"
	TerminalCommitUnverifiable TerminalCommitStatus = "unverifiable"
)

type TerminalDiagnostic string

const TerminalDiagnosticMutationResponseLost TerminalDiagnostic = "mutation_response_lost"

// TerminalResult deliberately excludes full receipts, commands, queries,
// object paths, identity records and telemetry payloads.
type TerminalResult struct {
	PhaseStatus     ExecutionStatus
	Phase           ingest.CleanupExecutionPhase
	Artifact        ingest.CleanupArtifactExecutionArtifact
	DeleteOutcome   ingest.CleanupDeleteRPCOutcome
	ErrorClass      ingest.CleanupExecutionErrorClass
	PhaseRevision   int64
	Steps           int
	TerminalKind    TerminalKind
	CommitStatus    TerminalCommitStatus
	Diagnostic      TerminalDiagnostic
	AttemptID       string
	ReceiptState    ingest.ReceiptState
	ReceiptRevision int64
	LedgerPhase     ingest.CleanupExecutionPhase
	LedgerRevision  int64
	Disposition     ingest.CleanupExecutionDisposition
	EvidenceHash    string
	CompletedAt     time.Time
	PurgeEligibleAt time.Time
	NextCleanupAt   time.Time
	HoldReviewDueAt time.Time
}

type phaseRunner interface {
	Execute(context.Context, ingest.CleanupExecutionQuery) (ExecutionResult, error)
}

type terminalMutator interface {
	ingest.CleanupExpiryFinalizationStore
	ingest.CleanupExecutionDispositionStore
}

type terminalOutcomeResolver interface {
	ResolveExpiryFinalization(
		context.Context,
		ingest.CleanupExpiryFinalizationOutcomeQuery,
	) (ingest.CleanupExpiryFinalizationOutcome, error)
	ResolveExecutionDisposition(
		context.Context,
		ingest.CleanupExecutionDispositionOutcomeQuery,
	) (ingest.CleanupExecutionDispositionOutcome, error)
}

var (
	_ phaseRunner             = (*PhaseExecutor)(nil)
	_ terminalMutator         = (*firebaseadapter.FirestoreAdmissionStore)(nil)
	_ terminalOutcomeResolver = (*systemTerminalOutcomeResolver)(nil)
)

type TerminalOrchestrator struct {
	runner             phaseRunner
	mutator            terminalMutator
	resolver           terminalOutcomeResolver
	correlationTimeout time.Duration
}

func NewTerminalOrchestrator(
	runner *PhaseExecutor,
	store *firebaseadapter.FirestoreAdmissionStore,
) (*TerminalOrchestrator, error) {
	if runner == nil || store == nil || runner.control != store {
		return nil, ErrInvalidTerminalOrchestration
	}
	resolver, err := newSystemTerminalOutcomeResolver(store, time.Now)
	if err != nil {
		return nil, err
	}
	return newTerminalOrchestrator(
		runner, store, resolver, DefaultTerminalCorrelationTimeout,
	)
}

func newTerminalOrchestrator(
	runner phaseRunner,
	mutator terminalMutator,
	resolver terminalOutcomeResolver,
	correlationTimeout time.Duration,
) (*TerminalOrchestrator, error) {
	if runner == nil || mutator == nil || resolver == nil || correlationTimeout <= 0 ||
		correlationTimeout > DefaultTerminalCorrelationTimeout ||
		correlationTimeout >= ingest.CleanupExpiryFinalizationOutcomeGrantTTL ||
		correlationTimeout >= ingest.CleanupExecutionDispositionOutcomeGrantTTL {
		return nil, ErrInvalidTerminalOrchestration
	}
	return &TerminalOrchestrator{
		runner: runner, mutator: mutator, resolver: resolver,
		correlationTimeout: correlationTimeout,
	}, nil
}

func (o *TerminalOrchestrator) Run(
	ctx context.Context,
	query ingest.CleanupExecutionQuery,
) (TerminalResult, error) {
	if o == nil || o.runner == nil || o.mutator == nil || o.resolver == nil || ctx == nil ||
		ingest.ValidateCleanupExecutionQuery(query) != nil || o.correlationTimeout <= 0 ||
		o.correlationTimeout > DefaultTerminalCorrelationTimeout {
		return TerminalResult{}, ErrInvalidTerminalOrchestration
	}
	phaseResult, phaseErr := o.runner.Execute(ctx, query)
	result := terminalResultFromPhase(phaseResult)
	if !cleanupTerminalIntentValid(phaseResult.terminalIntent, phaseResult, phaseErr) ||
		phaseResult.terminalIntent.query != query {
		return result, ErrCleanupTerminalUnavailable
	}
	switch phaseResult.terminalIntent.kind {
	case cleanupTerminalIntentFinalization:
		return o.finalize(ctx, phaseResult.terminalIntent, result)
	case cleanupTerminalIntentDisposition:
		return o.dispose(ctx, phaseResult.terminalIntent, result)
	default:
		return result, ErrCleanupTerminalUnavailable
	}
}

func (o *TerminalOrchestrator) finalize(
	ctx context.Context,
	intent cleanupTerminalIntent,
	result TerminalResult,
) (TerminalResult, error) {
	result.TerminalKind = TerminalKindFinalization
	committed, err := o.mutator.FinalizeExpiredCleanup(ctx, intent.query)
	if err == nil {
		if !directFinalizationResultValid(intent, committed) {
			return result, ErrCleanupTerminalUnavailable
		}
		return terminalResultFromFinalizationResult(result, committed), nil
	}
	if !finalizationOutcomeQueryMatchesIntent(committed.OutcomeQuery, intent) {
		return result, ErrCleanupTerminalUnavailable
	}
	result.Diagnostic = TerminalDiagnosticMutationResponseLost
	correlated, correlationErr := o.resolveFinalization(ctx, committed.OutcomeQuery)
	if correlationErr != nil {
		return result, ErrCleanupTerminalUnavailable
	}
	switch correlated.CommitStatus {
	case ingest.CleanupExpiryFinalizationCommitted:
		if !correlatedFinalizationOutcomeValid(intent, correlated) {
			return result, ErrCleanupTerminalUnavailable
		}
		return terminalResultFromFinalizationOutcome(result, correlated), nil
	case ingest.CleanupExpiryFinalizationNotCommitted:
		if !correlatedFinalizationPreStateValid(intent, correlated) {
			return result, ErrCleanupTerminalUnavailable
		}
		result.CommitStatus = TerminalCommitNotCommitted
		return result, ErrCleanupTerminalNotCommitted
	case ingest.CleanupExpiryFinalizationUnverifiable:
		result.CommitStatus = TerminalCommitUnverifiable
		return result, ErrCleanupTerminalUnverifiable
	default:
		return result, ErrCleanupTerminalUnavailable
	}
}

func (o *TerminalOrchestrator) dispose(
	ctx context.Context,
	intent cleanupTerminalIntent,
	result TerminalResult,
) (TerminalResult, error) {
	result.TerminalKind = TerminalKindDisposition
	committed, err := o.mutator.DisposeCleanupExecution(ctx, intent.command)
	if err == nil {
		if !directDispositionResultValid(intent, committed) {
			return result, ErrCleanupTerminalUnavailable
		}
		return terminalResultFromDispositionResult(result, committed), nil
	}
	if !dispositionOutcomeQueryMatchesIntent(committed.OutcomeQuery, intent) {
		return result, ErrCleanupTerminalUnavailable
	}
	result.Diagnostic = TerminalDiagnosticMutationResponseLost
	correlated, correlationErr := o.resolveDisposition(ctx, committed.OutcomeQuery)
	if correlationErr != nil {
		return result, ErrCleanupTerminalUnavailable
	}
	switch correlated.CommitStatus {
	case ingest.CleanupExecutionDispositionCommitted:
		if !correlatedDispositionOutcomeValid(intent, correlated) {
			return result, ErrCleanupTerminalUnavailable
		}
		return terminalResultFromDispositionOutcome(result, correlated), nil
	case ingest.CleanupExecutionDispositionNotCommitted:
		if !correlatedDispositionPreStateValid(intent, correlated) {
			return result, ErrCleanupTerminalUnavailable
		}
		result.CommitStatus = TerminalCommitNotCommitted
		return result, ErrCleanupTerminalNotCommitted
	case ingest.CleanupExecutionDispositionUnverifiable:
		result.CommitStatus = TerminalCommitUnverifiable
		return result, ErrCleanupTerminalUnverifiable
	default:
		return result, ErrCleanupTerminalUnavailable
	}
}

func (o *TerminalOrchestrator) resolveFinalization(
	parent context.Context,
	query ingest.CleanupExpiryFinalizationOutcomeQuery,
) (ingest.CleanupExpiryFinalizationOutcome, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), o.correlationTimeout)
	defer cancel()
	return o.resolver.ResolveExpiryFinalization(ctx, query)
}

func (o *TerminalOrchestrator) resolveDisposition(
	parent context.Context,
	query ingest.CleanupExecutionDispositionOutcomeQuery,
) (ingest.CleanupExecutionDispositionOutcome, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), o.correlationTimeout)
	defer cancel()
	return o.resolver.ResolveExecutionDisposition(ctx, query)
}

type systemTerminalOutcomeResolver struct {
	finalizationAuthorizer *ingest.SystemCleanupExpiryFinalizationOutcomeAuthorizer
	dispositionAuthorizer  *ingest.SystemCleanupExecutionDispositionOutcomeAuthorizer
	store                  terminalOutcomeStore
}

type terminalOutcomeStore interface {
	ingest.CleanupExpiryFinalizationOutcomeAuthorizationStore
	ingest.CleanupExpiryFinalizationOutcomeStore
	ingest.CleanupExecutionDispositionOutcomeAuthorizationStore
	ingest.CleanupExecutionDispositionOutcomeStore
}

func newSystemTerminalOutcomeResolver(
	store terminalOutcomeStore,
	now func() time.Time,
) (*systemTerminalOutcomeResolver, error) {
	if store == nil {
		return nil, ErrInvalidTerminalOrchestration
	}
	if now == nil {
		now = time.Now
	}
	finalizationAuthorizer, err := ingest.NewSystemCleanupExpiryFinalizationOutcomeAuthorizer(
		store, now,
	)
	if err != nil {
		return nil, ErrInvalidTerminalOrchestration
	}
	dispositionAuthorizer, err := ingest.NewSystemCleanupExecutionDispositionOutcomeAuthorizer(
		store, now,
	)
	if err != nil {
		return nil, ErrInvalidTerminalOrchestration
	}
	return &systemTerminalOutcomeResolver{
		finalizationAuthorizer: finalizationAuthorizer,
		dispositionAuthorizer:  dispositionAuthorizer,
		store:                  store,
	}, nil
}

func (r *systemTerminalOutcomeResolver) ResolveExpiryFinalization(
	ctx context.Context,
	query ingest.CleanupExpiryFinalizationOutcomeQuery,
) (ingest.CleanupExpiryFinalizationOutcome, error) {
	if r == nil || r.finalizationAuthorizer == nil || r.store == nil || ctx == nil {
		return ingest.CleanupExpiryFinalizationOutcome{}, ErrCleanupTerminalUnavailable
	}
	grant, err := r.finalizationAuthorizer.Authorize(ctx, query)
	if err != nil {
		return ingest.CleanupExpiryFinalizationOutcome{}, err
	}
	deadline, err := ingest.CleanupExpiryFinalizationOutcomeAuthorizationDeadline(grant, query)
	if err != nil {
		return ingest.CleanupExpiryFinalizationOutcome{}, err
	}
	observedAt := deadline.Add(-ingest.CleanupExpiryFinalizationOutcomeGrantTTL).UTC()
	return r.store.GetCleanupExpiryFinalizationOutcome(ctx, grant, query, observedAt)
}

func (r *systemTerminalOutcomeResolver) ResolveExecutionDisposition(
	ctx context.Context,
	query ingest.CleanupExecutionDispositionOutcomeQuery,
) (ingest.CleanupExecutionDispositionOutcome, error) {
	if r == nil || r.dispositionAuthorizer == nil || r.store == nil || ctx == nil {
		return ingest.CleanupExecutionDispositionOutcome{}, ErrCleanupTerminalUnavailable
	}
	grant, err := r.dispositionAuthorizer.Authorize(ctx, query)
	if err != nil {
		return ingest.CleanupExecutionDispositionOutcome{}, err
	}
	deadline, err := ingest.CleanupExecutionDispositionOutcomeAuthorizationDeadline(grant, query)
	if err != nil {
		return ingest.CleanupExecutionDispositionOutcome{}, err
	}
	observedAt := deadline.Add(-ingest.CleanupExecutionDispositionOutcomeGrantTTL).UTC()
	return r.store.GetCleanupExecutionDispositionOutcome(ctx, grant, query, observedAt)
}

func terminalResultFromPhase(result ExecutionResult) TerminalResult {
	return TerminalResult{
		PhaseStatus: result.Status, Phase: result.Phase, Artifact: result.Artifact,
		DeleteOutcome: result.DeleteOutcome, ErrorClass: result.ErrorClass,
		PhaseRevision: result.LedgerRevision, Steps: result.Steps,
	}
}

func terminalResultFromFinalizationResult(
	result TerminalResult,
	committed ingest.CleanupExpiryFinalizationResult,
) TerminalResult {
	result.CommitStatus = TerminalCommitCommitted
	result.AttemptID = committed.OutcomeQuery.AttemptID
	result.ReceiptState = committed.Receipt.State
	result.ReceiptRevision = committed.Receipt.Revision
	result.LedgerPhase = committed.Ledger.Phase
	result.LedgerRevision = committed.Ledger.Revision
	result.Disposition = committed.Ledger.Disposition
	result.EvidenceHash = committed.Ledger.EvidenceHash
	result.CompletedAt = committed.Ledger.CompletedAt.UTC()
	if committed.Receipt.PurgeEligibleAt != nil {
		result.PurgeEligibleAt = committed.Receipt.PurgeEligibleAt.UTC()
	}
	return result
}

func terminalResultFromFinalizationOutcome(
	result TerminalResult,
	committed ingest.CleanupExpiryFinalizationOutcome,
) TerminalResult {
	result.CommitStatus = TerminalCommitCommitted
	result.AttemptID = committed.AttemptID
	result.ReceiptState = committed.ReceiptState
	result.ReceiptRevision = committed.ReceiptRevision
	result.LedgerPhase = committed.LedgerPhase
	result.LedgerRevision = committed.LedgerRevision
	result.Disposition = ingest.CleanupExecutionDispositionComplete
	result.EvidenceHash = committed.EvidenceHash
	result.CompletedAt = committed.CompletedAt.UTC()
	result.PurgeEligibleAt = committed.PurgeEligibleAt.UTC()
	return result
}

func terminalResultFromDispositionResult(
	result TerminalResult,
	committed ingest.CleanupExecutionDispositionResult,
) TerminalResult {
	result.CommitStatus = TerminalCommitCommitted
	result.AttemptID = committed.OutcomeQuery.AttemptID
	result.ReceiptState = committed.Receipt.State
	result.ReceiptRevision = committed.Receipt.Revision
	result.LedgerPhase = committed.Ledger.Phase
	result.LedgerRevision = committed.Ledger.Revision
	result.Disposition = committed.Ledger.Disposition
	result.EvidenceHash = committed.Ledger.EvidenceHash
	result.CompletedAt = committed.Ledger.CompletedAt.UTC()
	result.NextCleanupAt = committed.NextCleanupAt.UTC()
	result.HoldReviewDueAt = committed.HoldReviewDueAt.UTC()
	return result
}

func terminalResultFromDispositionOutcome(
	result TerminalResult,
	committed ingest.CleanupExecutionDispositionOutcome,
) TerminalResult {
	result.CommitStatus = TerminalCommitCommitted
	result.AttemptID = committed.AttemptID
	result.ReceiptState = ingest.ReceiptCleanupPending
	result.ReceiptRevision = committed.ReceiptRevision
	result.LedgerPhase = committed.LedgerPhase
	result.LedgerRevision = committed.LedgerRevision
	result.Disposition = committed.Disposition
	result.ErrorClass = committed.ErrorClass
	result.EvidenceHash = committed.EvidenceHash
	result.CompletedAt = committed.CompletedAt.UTC()
	result.NextCleanupAt = committed.NextCleanupAt.UTC()
	result.HoldReviewDueAt = committed.HoldReviewDueAt.UTC()
	return result
}

func directFinalizationResultValid(
	intent cleanupTerminalIntent,
	result ingest.CleanupExpiryFinalizationResult,
) bool {
	expectedPurge, purgeErr := ingest.CleanupPurgeEligibleAt(
		result.Receipt.ReceiptRetentionFloor, result.Ledger.CompletedAt,
	)
	return purgeErr == nil && finalizationOutcomeQueryMatchesIntent(result.OutcomeQuery, intent) &&
		result.Receipt.TenantID == intent.query.TenantID &&
		result.Receipt.ReservationKey == intent.query.ReservationKey &&
		result.Receipt.State == ingest.ReceiptExpired &&
		result.Receipt.Revision == result.OutcomeQuery.ExpectedFinalReceiptRevision &&
		result.Receipt.FencingToken == intent.fence.Token && cleanupTerminalReceiptLeaseCleared(result.Receipt) &&
		result.Receipt.CleanupDispositionAttemptID == "" &&
		result.Receipt.CleanupControlDisposition == "" &&
		result.Receipt.LastCleanupErrorClass == "" && result.Receipt.NextCleanupAt.IsZero() &&
		result.Receipt.CleanupHoldReviewDueAt.IsZero() && result.Receipt.LastRecoveryCode == "" &&
		result.Ledger.SchemaVersion == ingest.CleanupExecutionLedgerSchemaVersion &&
		result.Ledger.DecisionDomain == ingest.CleanupExecutionDecisionExpiry &&
		result.Ledger.TargetHash == intent.targetHash && result.Ledger.PlanHash == intent.planHash &&
		result.Ledger.ReceiptRevision == intent.receiptRevision && result.Ledger.Fence == intent.fence &&
		result.Ledger.Phase == ingest.CleanupExecutionPhaseCompleted &&
		result.Ledger.Revision == result.OutcomeQuery.ExpectedFinalLedgerRevision &&
		result.Ledger.Disposition == ingest.CleanupExecutionDispositionComplete &&
		result.Ledger.ErrorClass == "" && cleanupTerminalDigestValid(result.Ledger.EvidenceHash) &&
		!result.Ledger.CompletedAt.IsZero() && result.Receipt.UpdatedAt.Equal(result.Ledger.CompletedAt) &&
		result.Receipt.PurgeEligibleAt != nil && result.Receipt.PurgeEligibleAt.Equal(expectedPurge)
}

func directDispositionResultValid(
	intent cleanupTerminalIntent,
	result ingest.CleanupExecutionDispositionResult,
) bool {
	expectedNext, expectedHold, err := ingest.CleanupExecutionDispositionCursorAt(
		intent.fence, intent.errorClass, result.Ledger.CompletedAt,
	)
	return err == nil && dispositionOutcomeQueryMatchesIntent(result.OutcomeQuery, intent) &&
		result.Receipt.TenantID == intent.query.TenantID &&
		result.Receipt.ReservationKey == intent.query.ReservationKey &&
		result.Receipt.State == ingest.ReceiptCleanupPending &&
		result.Receipt.Revision == result.OutcomeQuery.ExpectedFinalReceiptRevision &&
		result.Receipt.FencingToken == intent.fence.Token && cleanupTerminalReceiptLeaseCleared(result.Receipt) &&
		result.Receipt.CleanupDispositionAttemptID == intent.query.AttemptID &&
		result.Receipt.CleanupControlDisposition == result.OutcomeQuery.ExpectedDisposition &&
		result.Receipt.LastCleanupErrorClass == intent.errorClass &&
		result.Receipt.NextCleanupAt.Equal(expectedNext) &&
		result.Receipt.CleanupHoldReviewDueAt.Equal(expectedHold) &&
		result.Receipt.PurgeEligibleAt == nil && result.Receipt.LastRecoveryCode == "" &&
		result.Ledger.SchemaVersion == ingest.CleanupExecutionLedgerSchemaVersion &&
		result.Ledger.DecisionDomain == ingest.CleanupExecutionDecisionExpiry &&
		result.Ledger.TargetHash == intent.targetHash && result.Ledger.PlanHash == intent.planHash &&
		result.Ledger.ReceiptRevision == intent.receiptRevision && result.Ledger.Fence == intent.fence &&
		result.Ledger.Phase == intent.phase && result.Ledger.Revision == intent.ledgerRevision &&
		result.Ledger.Disposition == result.OutcomeQuery.ExpectedDisposition &&
		result.Ledger.ErrorClass == intent.errorClass && cleanupTerminalDigestValid(result.Ledger.EvidenceHash) &&
		!result.Ledger.CompletedAt.IsZero() && result.Receipt.UpdatedAt.Equal(result.Ledger.CompletedAt) &&
		result.NextCleanupAt.Equal(expectedNext) &&
		result.HoldReviewDueAt.Equal(expectedHold)
}

func correlatedFinalizationOutcomeValid(
	intent cleanupTerminalIntent,
	outcome ingest.CleanupExpiryFinalizationOutcome,
) bool {
	return outcome.AttemptID == intent.query.AttemptID &&
		outcome.ReceiptState == ingest.ReceiptExpired &&
		outcome.ReceiptRevision == intent.receiptRevision+1 &&
		outcome.LedgerPhase == ingest.CleanupExecutionPhaseCompleted &&
		outcome.LedgerRevision == intent.ledgerRevision+1 && cleanupTerminalDigestValid(outcome.EvidenceHash) &&
		!outcome.CompletedAt.IsZero() && outcome.PurgeEligibleAt.After(outcome.CompletedAt)
}

func correlatedFinalizationPreStateValid(
	intent cleanupTerminalIntent,
	outcome ingest.CleanupExpiryFinalizationOutcome,
) bool {
	return outcome.AttemptID == intent.query.AttemptID &&
		outcome.ReceiptState == ingest.ReceiptCleanupPending &&
		outcome.ReceiptRevision == intent.receiptRevision &&
		outcome.LedgerPhase == intent.phase && outcome.LedgerRevision == intent.ledgerRevision &&
		outcome.EvidenceHash == "" && outcome.CompletedAt.IsZero() &&
		outcome.PurgeEligibleAt.IsZero()
}

func correlatedDispositionOutcomeValid(
	intent cleanupTerminalIntent,
	outcome ingest.CleanupExecutionDispositionOutcome,
) bool {
	expectedNext, expectedHold, err := ingest.CleanupExecutionDispositionCursorAt(
		intent.fence, intent.errorClass, outcome.CompletedAt,
	)
	policy, policyErr := ingest.CleanupExecutionFailurePolicyFor(intent.errorClass)
	return err == nil && policyErr == nil && outcome.AttemptID == intent.query.AttemptID &&
		outcome.ReceiptRevision == intent.receiptRevision+1 &&
		outcome.LedgerPhase == intent.phase && outcome.LedgerRevision == intent.ledgerRevision &&
		outcome.Disposition == policy.Disposition && outcome.ErrorClass == intent.errorClass &&
		cleanupTerminalDigestValid(outcome.EvidenceHash) && !outcome.CompletedAt.IsZero() &&
		outcome.NextCleanupAt.Equal(expectedNext) && outcome.HoldReviewDueAt.Equal(expectedHold)
}

func correlatedDispositionPreStateValid(
	intent cleanupTerminalIntent,
	outcome ingest.CleanupExecutionDispositionOutcome,
) bool {
	return outcome.AttemptID == intent.query.AttemptID &&
		outcome.ReceiptRevision == intent.receiptRevision &&
		outcome.LedgerPhase == intent.phase && outcome.LedgerRevision == intent.ledgerRevision &&
		outcome.Disposition == "" && outcome.ErrorClass == "" && outcome.EvidenceHash == "" &&
		outcome.CompletedAt.IsZero() && outcome.NextCleanupAt.IsZero() &&
		outcome.HoldReviewDueAt.IsZero()
}

func cleanupTerminalReceiptLeaseCleared(receipt ingest.Receipt) bool {
	return receipt.LeaseOwnerID == "" && receipt.LeaseOwnerKind == "" &&
		receipt.LeaseAcquiredAt.IsZero() && receipt.LeaseHeartbeatAt.IsZero() &&
		receipt.LeaseExpiresAt.IsZero() && receipt.NextRecoveryAt.IsZero()
}

func finalizationOutcomeQueryMatchesIntent(
	query ingest.CleanupExpiryFinalizationOutcomeQuery,
	intent cleanupTerminalIntent,
) bool {
	return intent.kind == cleanupTerminalIntentFinalization &&
		ingest.ValidateCleanupExpiryFinalizationOutcomeQuery(query) == nil &&
		query.TenantID == intent.query.TenantID &&
		query.ReservationKey == intent.query.ReservationKey &&
		query.AttemptID == intent.query.AttemptID &&
		query.ExpectedTargetHash == intent.targetHash &&
		query.ExpectedPlanHash == intent.planHash && query.ExpectedFence == intent.fence &&
		query.ExpectedPreReceiptRevision == intent.receiptRevision &&
		query.ExpectedPreLedgerRevision == intent.ledgerRevision
}

func dispositionOutcomeQueryMatchesIntent(
	query ingest.CleanupExecutionDispositionOutcomeQuery,
	intent cleanupTerminalIntent,
) bool {
	policy, err := ingest.CleanupExecutionFailurePolicyFor(intent.errorClass)
	return err == nil && intent.kind == cleanupTerminalIntentDisposition &&
		ingest.ValidateCleanupExecutionDispositionOutcomeQuery(query) == nil &&
		query.TenantID == intent.query.TenantID &&
		query.ReservationKey == intent.query.ReservationKey &&
		query.AttemptID == intent.query.AttemptID &&
		query.ExpectedTargetHash == intent.targetHash &&
		query.ExpectedPlanHash == intent.planHash && query.ExpectedFence == intent.fence &&
		query.ExpectedPreReceiptRevision == intent.receiptRevision &&
		query.ExpectedLedgerRevision == intent.ledgerRevision &&
		query.ExpectedPhase == intent.phase && query.ExpectedErrorClass == intent.errorClass &&
		query.ExpectedDisposition == policy.Disposition
}
