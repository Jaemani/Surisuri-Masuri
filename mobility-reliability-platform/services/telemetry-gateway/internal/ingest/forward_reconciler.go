package ingest

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultForwardRecoveryPerReceiptTimeout = 2 * time.Minute
	DefaultForwardRecoveryOutcomeTimeout    = 5 * time.Second
	DefaultForwardRecoveryMaxEpochs         = 2
	DefaultForwardRecoveryMaxSteps          = 24
	maxForwardRecoveryFinalizerSteps        = 12
)

var (
	ErrInvalidForwardRecoveryExecution = errors.New("forward recovery execution is invalid")
	ErrForwardRecoveryBudgetExhausted  = errors.New("forward recovery execution budget is exhausted")
	ErrForwardRecoveryLeaseUnknown     = errors.New("forward recovery lease outcome is unknown")
	ErrForwardRecoveryCommitUnverified = errors.New("forward recovery commit outcome is unverified")
	ErrForwardRecoveryNotCommitted     = errors.New("forward recovery action was not committed")
)

// ForwardRecoveryControlStore is the provider-neutral control-plane surface
// needed by one already-claimed receipt. Candidate discovery and lease claim
// remain outside this R6 execution boundary.
type ForwardRecoveryControlStore interface {
	RecoveryLeaseStore
	ForwardRecoveryAuthorizationStore
	ForwardRecoveryActionStore
	ForwardRecoveryDispositionStore
	ForwardRecoveryAttemptStore
	ForwardRecoveryOutcomeAuthorizationStore
	ForwardRecoveryOutcomeStore
}

type ForwardRecoveryConfig struct {
	PerReceiptTimeout time.Duration
	OutcomeTimeout    time.Duration
	RenewalBefore     time.Duration
	RenewalDuration   time.Duration
	MaxEpochs         int
	MaxSteps          int
}

func DefaultForwardRecoveryConfig() ForwardRecoveryConfig {
	return ForwardRecoveryConfig{
		PerReceiptTimeout: DefaultForwardRecoveryPerReceiptTimeout,
		OutcomeTimeout:    DefaultForwardRecoveryOutcomeTimeout,
		RenewalBefore:     LeaseRenewalWindow,
		RenewalDuration:   DefaultRequestLeaseDuration,
		MaxEpochs:         DefaultForwardRecoveryMaxEpochs,
		MaxSteps:          DefaultForwardRecoveryMaxSteps,
	}
}

type ForwardRecoveryTask struct {
	TenantID       string
	ReservationKey string
	Lease          LeaseGrant
	Attempt        RecoveryAttemptProposal
}

type ForwardRecoveryExecutionStatus string

const (
	ForwardRecoveryExecutionCommitted  ForwardRecoveryExecutionStatus = "committed"
	ForwardRecoveryExecutionCorrelated ForwardRecoveryExecutionStatus = "committed_after_response_loss"
)

// ForwardRecoveryExecutionResult intentionally excludes artifact paths,
// payloads, coordinates and identity-provider identifiers. Detailed immutable
// lineage remains in the receipt and attempt transaction, not in orchestration
// logs or batch-worker results.
type ForwardRecoveryExecutionResult struct {
	Status                   ForwardRecoveryExecutionStatus
	DecisionDomain           ForwardRecoveryDecisionDomain
	Action                   ForwardRecoveryAction
	AuthorizationDisposition ForwardRecoveryAuthorizationDisposition
	Outcome                  RecoveryAttemptOutcome
	ReceiptState             ReceiptState
	ReceiptRevision          int64
	CorrelatedOutcome        RecoveryActionOutcome
	ClassificationPasses     int
	ManifestWrites           int
	Renewals                 int
	Steps                    int
	FinalizerSteps           int
}

type forwardRecoveryAuthorizer interface {
	Authorize(
		context.Context,
		string,
		string,
		LeaseGrant,
	) (ArtifactClassificationRequest, ArtifactReadAuthorizationGrant, error)
	AuthorizeManifestRepair(
		context.Context,
		string,
		string,
		LeaseGrant,
		ForwardRecoveryManifestEvidence,
		RecoveryManifestWrite,
	) (ArtifactClassificationRequest, ManifestRepairAuthorizationGrant, error)
	AuthorizeForwardRecoveryAction(
		context.Context,
		string,
		string,
		LeaseGrant,
		RecoveryAttemptProposal,
		ForwardRecoveryPlanInput,
	) (ForwardRecoveryActionCommand, ForwardRecoveryActionGrant, error)
	AuthorizeForwardRecoveryDisposition(
		context.Context,
		string,
		string,
		LeaseGrant,
		RecoveryAttemptProposal,
	) (ForwardRecoveryDispositionCommand, ForwardRecoveryDispositionGrant, error)
	AuthorizeForwardRecoveryAttemptFailure(
		context.Context,
		string,
		string,
		LeaseGrant,
		RecoveryAttemptProposal,
		RecoveryAttemptFailureCode,
	) (ForwardRecoveryAttemptFailure, ForwardRecoveryAttemptGrant, error)
}

type forwardRecoveryOutcomeAuthorizer interface {
	Authorize(context.Context, ForwardRecoveryOutcomeQuery) (ForwardRecoveryOutcomeReadGrant, error)
}

type recoveryManifestBuilder interface {
	buildRecoveryManifest(
		ArtifactClassificationRequest,
		ArtifactPinnedLineage,
	) (RecoveryManifestWrite, ArtifactReasonCode)
}

// ForwardRecoveryReconciler owns one bounded two-pass protocol. It cannot
// mutate raw artifacts because its only Storage mutation dependency is the
// manifest-only port.
type ForwardRecoveryReconciler struct {
	control           ForwardRecoveryControlStore
	manifests         TelemetryManifestRecoveryStore
	classifier        ArtifactClassifier
	manifestBuilder   recoveryManifestBuilder
	authorizer        forwardRecoveryAuthorizer
	outcomeAuthorizer forwardRecoveryOutcomeAuthorizer
	now               func() time.Time
	config            ForwardRecoveryConfig
}

func NewForwardRecoveryReconciler(
	reader ArtifactInventoryReader,
	manifests TelemetryManifestRecoveryStore,
	control ForwardRecoveryControlStore,
	now func() time.Time,
	config ForwardRecoveryConfig,
) (*ForwardRecoveryReconciler, error) {
	if reader == nil || manifests == nil || control == nil {
		return nil, ErrInvalidForwardRecoveryExecution
	}
	if now == nil {
		now = time.Now
	}
	config, err := normalizeForwardRecoveryConfig(config)
	if err != nil {
		return nil, err
	}
	authorizer, err := NewSystemRecoveryAuthorizer(control, now)
	if err != nil {
		return nil, ErrInvalidForwardRecoveryExecution
	}
	classifier, err := newReadOnlyArtifactClassifier(reader, authorizer.validator, now)
	if err != nil {
		return nil, ErrInvalidForwardRecoveryExecution
	}
	outcomeAuthorizer, err := NewSystemRecoveryOutcomeAuthorizer(control, now)
	if err != nil {
		return nil, ErrInvalidForwardRecoveryExecution
	}
	return newForwardRecoveryReconciler(
		control,
		manifests,
		classifier,
		authorizer.validator,
		authorizer,
		outcomeAuthorizer,
		now,
		config,
	)
}

// newForwardRecoveryReconciler is the package-private test seam. Production
// callers cannot inject an arbitrary classifier or capability minter.
func newForwardRecoveryReconciler(
	control ForwardRecoveryControlStore,
	manifests TelemetryManifestRecoveryStore,
	classifier ArtifactClassifier,
	manifestBuilder recoveryManifestBuilder,
	authorizer forwardRecoveryAuthorizer,
	outcomeAuthorizer forwardRecoveryOutcomeAuthorizer,
	now func() time.Time,
	config ForwardRecoveryConfig,
) (*ForwardRecoveryReconciler, error) {
	config, err := normalizeForwardRecoveryConfig(config)
	if err != nil || control == nil || manifests == nil || classifier == nil ||
		manifestBuilder == nil || authorizer == nil || outcomeAuthorizer == nil || now == nil {
		return nil, ErrInvalidForwardRecoveryExecution
	}
	return &ForwardRecoveryReconciler{
		control: control, manifests: manifests, classifier: classifier,
		manifestBuilder: manifestBuilder, authorizer: authorizer,
		outcomeAuthorizer: outcomeAuthorizer, now: now, config: config,
	}, nil
}

func normalizeForwardRecoveryConfig(config ForwardRecoveryConfig) (ForwardRecoveryConfig, error) {
	defaults := DefaultForwardRecoveryConfig()
	if config == (ForwardRecoveryConfig{}) {
		return defaults, nil
	}
	if config.PerReceiptTimeout == 0 {
		config.PerReceiptTimeout = defaults.PerReceiptTimeout
	}
	if config.OutcomeTimeout == 0 {
		config.OutcomeTimeout = defaults.OutcomeTimeout
	}
	if config.RenewalBefore == 0 {
		config.RenewalBefore = defaults.RenewalBefore
	}
	if config.RenewalDuration == 0 {
		config.RenewalDuration = defaults.RenewalDuration
	}
	if config.MaxEpochs == 0 {
		config.MaxEpochs = defaults.MaxEpochs
	}
	if config.MaxSteps == 0 {
		config.MaxSteps = defaults.MaxSteps
	}
	minimumStepBudget := config.MaxEpochs*8 + 8
	if config.PerReceiptTimeout <= config.OutcomeTimeout ||
		config.PerReceiptTimeout > MaxLeaseDuration ||
		config.OutcomeTimeout <= 0 || config.OutcomeTimeout > ForwardRecoveryOutcomeGrantTTL ||
		config.RenewalBefore < 0 || config.RenewalBefore > LeaseRenewalWindow ||
		config.RenewalBefore >= config.RenewalDuration ||
		config.RenewalDuration < MinLeaseDuration || config.RenewalDuration > MaxLeaseDuration ||
		config.MaxEpochs < 2 || config.MaxEpochs > 3 ||
		config.MaxSteps < minimumStepBudget || config.MaxSteps > 64 {
		return ForwardRecoveryConfig{}, ErrInvalidForwardRecoveryExecution
	}
	return config, nil
}

type forwardRecoveryRun struct {
	reconciler  *ForwardRecoveryReconciler
	task        ForwardRecoveryTask
	result      ForwardRecoveryExecutionResult
	runDeadline time.Time
	finalizers  int
}

func (r *ForwardRecoveryReconciler) Reconcile(
	ctx context.Context,
	task ForwardRecoveryTask,
) (ForwardRecoveryExecutionResult, error) {
	if r == nil || r.control == nil || r.manifests == nil || r.classifier == nil ||
		r.manifestBuilder == nil || r.authorizer == nil || r.outcomeAuthorizer == nil ||
		r.now == nil || ctx == nil || validateForwardRecoveryTask(task) != nil {
		return ForwardRecoveryExecutionResult{}, ErrInvalidForwardRecoveryExecution
	}
	if err := ctx.Err(); err != nil {
		return ForwardRecoveryExecutionResult{}, err
	}
	wallStartedAt := time.Now().UTC()
	runDeadline := wallStartedAt.Add(r.config.PerReceiptTimeout)
	operationalTimeout := r.config.PerReceiptTimeout - r.config.OutcomeTimeout
	runContext, cancel := context.WithTimeout(ctx, operationalTimeout)
	defer cancel()
	// Context scheduling uses monotonic process time. Domain acceptance still
	// uses the injected trusted UTC clock at every authorization boundary.
	run := &forwardRecoveryRun{reconciler: r, task: task, runDeadline: runDeadline}
	return run.execute(runContext)
}

func validateForwardRecoveryTask(task ForwardRecoveryTask) error {
	if validateForwardRecoveryAuthorizationInput(
		task.TenantID,
		task.ReservationKey,
		task.Lease,
	) != nil || ValidateRecoveryAttemptProposal(task.Attempt) != nil ||
		task.Attempt.ID != task.Lease.Fence.OwnerID {
		return ErrInvalidForwardRecoveryExecution
	}
	return nil
}

func (run *forwardRecoveryRun) execute(
	ctx context.Context,
) (ForwardRecoveryExecutionResult, error) {
	for epoch := 0; epoch < run.reconciler.config.MaxEpochs; epoch++ {
		renewed, err := run.renewIfNeeded(ctx)
		if err != nil {
			return run.result, err
		}
		if renewed {
			continue
		}
		epochRemaining := run.task.Lease.Fence.ExpiresAt.Sub(run.reconciler.now().UTC())
		if epochRemaining <= 0 {
			return run.result, ErrForwardRecoveryLeaseUnknown
		}
		epochContext, cancel := context.WithTimeout(ctx, epochRemaining)
		restart, result, err := run.executeEpoch(epochContext)
		cancel()
		if err != nil {
			return run.result, err
		}
		if !restart {
			return result, nil
		}
	}
	if ctx.Err() == nil {
		run.failAttempt(ctx, RecoveryAttemptFailureFinalizerAbort)
	}
	return run.result, ErrForwardRecoveryBudgetExhausted
}

func (run *forwardRecoveryRun) executeEpoch(
	ctx context.Context,
) (bool, ForwardRecoveryExecutionResult, error) {
	request, initial, err := run.freshClassify(ctx)
	if err != nil {
		return run.handleExecutionError(ctx, err, RecoveryAttemptFailureFinalizerAbort)
	}
	initialInput := ForwardRecoveryPlanInput{
		Phase: RecoveryPhaseInitial, Request: request, Result: initial,
	}
	initialPlan, err := PlanForwardRecoveryAction(initialInput)
	if err != nil {
		run.failAttempt(ctx, RecoveryAttemptFailureInvalidContract)
		return false, run.result, ErrInvalidForwardRecoveryExecution
	}

	renewed, err := run.renewIfNeeded(ctx)
	if err != nil {
		return false, run.result, err
	}
	if renewed {
		return true, run.result, nil
	}

	switch initialPlan.Action {
	case ForwardRecoveryActionCreateManifest:
		if initial.PinnedRaw == nil {
			run.failAttempt(ctx, RecoveryAttemptFailureInvalidContract)
			return false, run.result, ErrInvalidForwardRecoveryExecution
		}
		write, reason := run.reconciler.manifestBuilder.buildRecoveryManifest(
			request,
			*initial.PinnedRaw,
		)
		if reason != "" || ValidateRecoveryManifestWrite(write) != nil {
			run.failAttempt(ctx, RecoveryAttemptFailureInvalidContract)
			return false, run.result, ErrInvalidForwardRecoveryExecution
		}
		written, err := run.repairManifestOnce(ctx, request, initial, write)
		if err != nil {
			return run.handleExecutionError(ctx, err, RecoveryAttemptFailureFinalizerAbort)
		}
		renewed, err = run.renewIfNeeded(ctx)
		if err != nil {
			return false, run.result, err
		}
		if renewed {
			return true, run.result, nil
		}
		confirmedRequest, confirmed, err := run.freshClassify(ctx)
		if err != nil {
			return run.handleExecutionError(ctx, err, RecoveryAttemptFailureFinalizerAbort)
		}
		if !sameClassificationRequest(request, confirmedRequest) {
			run.failAttempt(ctx, RecoveryAttemptFailureInvalidContract)
			return false, run.result, ErrInvalidForwardRecoveryExecution
		}
		input := ForwardRecoveryPlanInput{
			Phase:   RecoveryPhasePostManifestConfirmation,
			Request: confirmedRequest, Result: confirmed,
			PriorResult: &initial, WrittenManifest: &written,
		}
		return run.commitPlannedAction(ctx, input)
	case ForwardRecoveryActionConfirmComplete, ForwardRecoveryActionConfirmRawConflict:
		confirmedRequest, confirmed, err := run.freshClassify(ctx)
		if err != nil {
			return run.handleExecutionError(ctx, err, RecoveryAttemptFailureFinalizerAbort)
		}
		if !sameClassificationRequest(request, confirmedRequest) {
			run.failAttempt(ctx, RecoveryAttemptFailureInvalidContract)
			return false, run.result, ErrInvalidForwardRecoveryExecution
		}
		input := ForwardRecoveryPlanInput{
			Phase: RecoveryPhaseConfirmation, Request: confirmedRequest,
			Result: confirmed, PriorResult: &initial,
		}
		return run.commitPlannedAction(ctx, input)
	default:
		if !terminalForwardRecoveryAction(initialPlan.Action) {
			run.failAttempt(ctx, RecoveryAttemptFailureInvalidContract)
			return false, run.result, ErrInvalidForwardRecoveryExecution
		}
		return run.commitPlannedAction(ctx, initialInput)
	}
}

func (run *forwardRecoveryRun) freshClassify(
	ctx context.Context,
) (ArtifactClassificationRequest, ArtifactClassificationResult, error) {
	if err := run.consume(2); err != nil {
		return ArtifactClassificationRequest{}, ArtifactClassificationResult{}, err
	}
	request, grant, err := run.reconciler.authorizer.Authorize(
		ctx,
		run.task.TenantID,
		run.task.ReservationKey,
		run.task.Lease,
	)
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactClassificationResult{}, err
	}
	result, err := run.reconciler.classifier.Classify(ctx, grant, request)
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactClassificationResult{}, err
	}
	run.result.ClassificationPasses++
	return request, result, nil
}

func (run *forwardRecoveryRun) repairManifestOnce(
	ctx context.Context,
	request ArtifactClassificationRequest,
	result ArtifactClassificationResult,
	write RecoveryManifestWrite,
) (StoredArtifact, error) {
	if err := run.consume(2); err != nil {
		return StoredArtifact{}, err
	}
	freshRequest, grant, err := run.reconciler.authorizer.AuthorizeManifestRepair(
		ctx,
		run.task.TenantID,
		run.task.ReservationKey,
		run.task.Lease,
		ForwardRecoveryManifestEvidence{Request: request, Result: result},
		write,
	)
	if err != nil {
		return StoredArtifact{}, err
	}
	if !sameClassificationRequest(request, freshRequest) {
		return StoredArtifact{}, ErrInvalidForwardRecoveryExecution
	}
	stored, err := run.reconciler.manifests.CreateManifest(ctx, grant, write)
	if err != nil {
		return StoredArtifact{}, err
	}
	run.result.ManifestWrites++
	return stored, nil
}

func (run *forwardRecoveryRun) commitPlannedAction(
	ctx context.Context,
	input ForwardRecoveryPlanInput,
) (bool, ForwardRecoveryExecutionResult, error) {
	renewed, err := run.renewIfNeeded(ctx)
	if err != nil {
		return false, run.result, err
	}
	if renewed {
		return true, run.result, nil
	}
	if err := run.consume(2); err != nil {
		return false, run.result, err
	}
	command, grant, err := run.reconciler.authorizer.AuthorizeForwardRecoveryAction(
		ctx,
		run.task.TenantID,
		run.task.ReservationKey,
		run.task.Lease,
		run.task.Attempt,
		input,
	)
	if err != nil {
		return run.handleExecutionError(ctx, err, RecoveryAttemptFailureFinalizerAbort)
	}
	receipt, commitErr := run.reconciler.control.CommitForwardRecoveryAction(
		ctx,
		grant,
		command,
		run.reconciler.now().UTC(),
	)
	if commitErr == nil {
		run.result.Status = ForwardRecoveryExecutionCommitted
		run.result.DecisionDomain = ForwardRecoveryDecisionArtifactReconciliation
		run.result.Action = command.Plan.Action
		run.result.Outcome = outcomeForForwardRecoveryAction(command.Plan.Action)
		run.result.ReceiptState = receipt.State
		run.result.ReceiptRevision = receipt.Revision
		return false, run.result, nil
	}
	query, err := ForwardRecoveryOutcomeQueryForAction(command)
	if err != nil {
		return false, run.result, ErrForwardRecoveryCommitUnverified
	}
	return run.correlateCommit(ctx, query, command.Plan.Action, "", commitErr)
}

func (run *forwardRecoveryRun) handleExecutionError(
	ctx context.Context,
	err error,
	failureCode RecoveryAttemptFailureCode,
) (bool, ForwardRecoveryExecutionResult, error) {
	if err == nil {
		return false, run.result, ErrInvalidForwardRecoveryExecution
	}
	if callerErr := ctx.Err(); callerErr != nil {
		finalizerErr := run.finalizeCallerTermination(ctx, callerErr)
		if run.result.Status == ForwardRecoveryExecutionCommitted ||
			run.result.Status == ForwardRecoveryExecutionCorrelated {
			return false, run.result, nil
		}
		if finalizerErr != nil {
			return false, run.result, errors.Join(callerErr, finalizerErr)
		}
		return false, run.result, callerErr
	}
	if forwardRecoveryCapabilityExpired(err) {
		return true, run.result, nil
	}
	if !errors.Is(err, ErrForwardRecoveryUnauthorized) &&
		!errors.Is(err, ErrForwardRecoveryAuthorizationUnavailable) {
		run.failAttempt(ctx, failureCode)
		return false, run.result, err
	}
	if err := run.consume(2); err != nil {
		return false, run.result, err
	}
	command, grant, dispositionErr := run.reconciler.authorizer.AuthorizeForwardRecoveryDisposition(
		ctx,
		run.task.TenantID,
		run.task.ReservationKey,
		run.task.Lease,
		run.task.Attempt,
	)
	if errors.Is(dispositionErr, ErrForwardRecoveryDispositionNotRequired) {
		return true, run.result, nil
	}
	if dispositionErr != nil {
		if callerErr := ctx.Err(); callerErr != nil {
			return false, run.result, callerErr
		}
		return false, run.result, dispositionErr
	}
	receipt, commitErr := run.reconciler.control.CommitForwardRecoveryDisposition(
		ctx,
		grant,
		command,
		run.reconciler.now().UTC(),
	)
	action, outcome := dispositionActionOutcome(command.Disposition)
	if commitErr == nil {
		run.result.Status = ForwardRecoveryExecutionCommitted
		run.result.DecisionDomain = ForwardRecoveryDecisionCurrentAuthorization
		run.result.AuthorizationDisposition = command.Disposition
		run.result.Action = action
		run.result.Outcome = outcome
		run.result.ReceiptState = receipt.State
		run.result.ReceiptRevision = receipt.Revision
		return false, run.result, nil
	}
	query, queryErr := ForwardRecoveryOutcomeQueryForDisposition(command)
	if queryErr != nil {
		return false, run.result, ErrForwardRecoveryCommitUnverified
	}
	return run.correlateCommit(ctx, query, action, command.Disposition, commitErr)
}

func (run *forwardRecoveryRun) correlateCommit(
	ctx context.Context,
	query ForwardRecoveryOutcomeQuery,
	action ForwardRecoveryAction,
	disposition ForwardRecoveryAuthorizationDisposition,
	commitErr error,
) (bool, ForwardRecoveryExecutionResult, error) {
	outcome, err := run.readCommitOutcome(ctx, query)
	if err != nil {
		return false, run.result, err
	}
	switch outcome.CommitStatus {
	case RecoveryActionCommitted:
		run.applyCorrelatedOutcome(query, action, disposition, outcome)
		return false, run.result, nil
	case RecoveryActionNotCommitted:
		closeErr := run.closeNotCommittedAction(ctx, query, action, disposition)
		if run.result.Status == ForwardRecoveryExecutionCommitted ||
			run.result.Status == ForwardRecoveryExecutionCorrelated {
			return false, run.result, nil
		}
		if closeErr == nil {
			closeErr = ErrForwardRecoveryNotCommitted
		}
		if callerErr := ctx.Err(); callerErr != nil {
			closeErr = errors.Join(closeErr, callerErr)
		}
		_ = commitErr
		return false, run.result, closeErr
	case RecoveryActionUnverifiable:
		return false, run.result, commitCorrelationError(ctx, ErrForwardRecoveryCommitUnverified)
	default:
		return false, run.result, commitCorrelationError(ctx, ErrForwardRecoveryCommitUnverified)
	}
}

func (run *forwardRecoveryRun) readCommitOutcome(
	ctx context.Context,
	query ForwardRecoveryOutcomeQuery,
) (RecoveryActionOutcome, error) {
	if err := run.consumeFinalizer(2); err != nil {
		return RecoveryActionOutcome{}, errors.Join(ErrForwardRecoveryCommitUnverified, err)
	}
	outcomeContext, cancel := run.outcomeContext(ctx)
	defer cancel()
	grant, err := run.reconciler.outcomeAuthorizer.Authorize(outcomeContext, query)
	if err != nil {
		return RecoveryActionOutcome{}, commitCorrelationError(ctx, err)
	}
	outcome, err := run.reconciler.control.GetForwardRecoveryActionOutcome(
		outcomeContext,
		grant,
		query,
		run.reconciler.now().UTC(),
	)
	if err != nil {
		return RecoveryActionOutcome{}, commitCorrelationError(ctx, err)
	}
	return outcome, nil
}

func (run *forwardRecoveryRun) applyCorrelatedOutcome(
	query ForwardRecoveryOutcomeQuery,
	action ForwardRecoveryAction,
	disposition ForwardRecoveryAuthorizationDisposition,
	outcome RecoveryActionOutcome,
) {
	run.result.Status = ForwardRecoveryExecutionCorrelated
	run.result.DecisionDomain = query.ExpectedDecisionDomain
	run.result.AuthorizationDisposition = disposition
	run.result.Action = action
	run.result.Outcome = outcome.Outcome
	run.result.ReceiptState = outcome.ReceiptState
	run.result.ReceiptRevision = outcome.ReceiptRevision
	run.result.CorrelatedOutcome = outcome
}

// closeNotCommittedAction establishes a transaction barrier before forgetting
// a pending command. A successful attempt-failure write conflicts with any
// late action transaction. If that barrier cannot be written, the exact old
// outcome is read once more before a fresh current-authorization disposition
// is allowed to compete.
func (run *forwardRecoveryRun) closeNotCommittedAction(
	ctx context.Context,
	query ForwardRecoveryOutcomeQuery,
	action ForwardRecoveryAction,
	disposition ForwardRecoveryAuthorizationDisposition,
) error {
	finalizerContext, cancel := run.outcomeContext(ctx)
	defer cancel()
	failureCode := RecoveryAttemptFailureFinalizerAbort
	if callerErr := ctx.Err(); callerErr != nil {
		failureCode = recoveryAttemptFailureForContext(callerErr)
	}
	failureErr := run.failAttemptWithBudget(
		finalizerContext,
		failureCode,
		true,
	)
	if failureErr == nil {
		return ErrForwardRecoveryNotCommitted
	}

	lateOutcome, lateErr := run.readCommitOutcome(finalizerContext, query)
	if lateErr != nil {
		return errors.Join(ErrForwardRecoveryCommitUnverified, failureErr, lateErr)
	}
	if lateOutcome.CommitStatus == RecoveryActionCommitted {
		run.applyCorrelatedOutcome(query, action, disposition, lateOutcome)
		return nil
	}
	if lateOutcome.CommitStatus != RecoveryActionNotCommitted {
		return errors.Join(ErrForwardRecoveryCommitUnverified, failureErr)
	}
	if !errors.Is(failureErr, ErrForwardRecoveryUnauthorized) &&
		!errors.Is(failureErr, ErrForwardRecoveryAuthorizationUnavailable) {
		return errors.Join(ErrForwardRecoveryCommitUnverified, failureErr)
	}
	dispositionErr := run.finalizeAuthorizationDisposition(finalizerContext)
	if errors.Is(dispositionErr, ErrForwardRecoveryDispositionNotRequired) {
		dispositionErr = run.failAttemptWithBudget(
			finalizerContext,
			failureCode,
			true,
		)
	}
	if run.result.Status == ForwardRecoveryExecutionCommitted ||
		run.result.Status == ForwardRecoveryExecutionCorrelated {
		return nil
	}
	if dispositionErr != nil {
		return errors.Join(ErrForwardRecoveryCommitUnverified, dispositionErr)
	}
	return ErrForwardRecoveryNotCommitted
}

func commitCorrelationError(ctx context.Context, err error) error {
	if errors.Is(err, ErrForwardRecoveryNotCommitted) {
		return err
	}
	if ctx != nil {
		if callerErr := ctx.Err(); callerErr != nil {
			return errors.Join(ErrForwardRecoveryCommitUnverified, callerErr)
		}
	}
	return ErrForwardRecoveryCommitUnverified
}

func (run *forwardRecoveryRun) outcomeContext(
	ctx context.Context,
) (context.Context, context.CancelFunc) {
	// This context is used only after a commit was invoked or for bounded
	// attempt/disposition finalization. It deliberately ignores caller
	// cancellation while preserving values, and never permits artifact I/O,
	// lease renewal or a normal action replay.
	base := context.WithoutCancel(ctx)
	deadline := time.Now().UTC().Add(run.reconciler.config.OutcomeTimeout)
	if run.runDeadline.Before(deadline) {
		deadline = run.runDeadline
	}
	return context.WithDeadline(base, deadline)
}

func (run *forwardRecoveryRun) renewIfNeeded(ctx context.Context) (bool, error) {
	now := run.reconciler.now().UTC()
	if !now.Before(run.task.Lease.Fence.ExpiresAt) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return false, ErrForwardRecoveryLeaseUnknown
	}
	if run.task.Lease.Fence.ExpiresAt.Sub(now) > run.reconciler.config.RenewalBefore {
		return false, nil
	}
	if err := run.consume(1); err != nil {
		return false, err
	}
	renewed, err := run.reconciler.control.RenewLease(
		ctx,
		run.task.TenantID,
		run.task.ReservationKey,
		run.task.Lease.Fence,
		now,
		run.reconciler.config.RenewalDuration,
	)
	if err != nil {
		if callerErr := ctx.Err(); callerErr != nil {
			return false, errors.Join(ErrForwardRecoveryLeaseUnknown, callerErr)
		}
		return false, ErrForwardRecoveryLeaseUnknown
	}
	if ValidateLeaseGrant(renewed) != nil || renewed.OwnerKind != LeaseOwnerSweeper ||
		renewed.Fence.OwnerID != run.task.Lease.Fence.OwnerID ||
		renewed.Fence.Token != run.task.Lease.Fence.Token ||
		!renewed.Fence.ExpiresAt.After(run.task.Lease.Fence.ExpiresAt) ||
		!renewed.AcquiredAt.Equal(run.task.Lease.AcquiredAt) ||
		renewed.HeartbeatAt.Before(run.task.Lease.HeartbeatAt) {
		return false, ErrForwardRecoveryLeaseUnknown
	}
	run.task.Lease = renewed
	run.result.Renewals++
	return true, nil
}

func (run *forwardRecoveryRun) failAttempt(
	ctx context.Context,
	code RecoveryAttemptFailureCode,
) {
	_ = run.failAttemptWithBudget(ctx, code, false)
}

func (run *forwardRecoveryRun) failAttemptWithBudget(
	ctx context.Context,
	code RecoveryAttemptFailureCode,
	finalizer bool,
) error {
	if ctx == nil || ctx.Err() != nil {
		return context.Canceled
	}
	var budgetErr error
	if finalizer {
		budgetErr = run.consumeFinalizer(2)
	} else {
		budgetErr = run.consume(2)
	}
	if budgetErr != nil {
		return budgetErr
	}
	failure, grant, err := run.reconciler.authorizer.AuthorizeForwardRecoveryAttemptFailure(
		ctx,
		run.task.TenantID,
		run.task.ReservationKey,
		run.task.Lease,
		run.task.Attempt,
		code,
	)
	if err != nil {
		return err
	}
	if err := run.reconciler.control.FailForwardRecoveryAttempt(
		ctx,
		grant,
		failure,
		run.reconciler.now().UTC(),
	); err != nil {
		return errors.Join(ErrForwardRecoveryCommitUnverified, err)
	}
	return nil
}

func (run *forwardRecoveryRun) finalizeCallerTermination(
	ctx context.Context,
	cause error,
) error {
	if ctx == nil || cause == nil {
		return nil
	}
	finalizerContext, cancel := run.outcomeContext(ctx)
	defer cancel()
	code := recoveryAttemptFailureForContext(cause)
	err := run.failAttemptWithBudget(finalizerContext, code, true)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrForwardRecoveryUnauthorized) &&
		!errors.Is(err, ErrForwardRecoveryAuthorizationUnavailable) {
		if errors.Is(err, ErrForwardRecoveryCommitUnverified) {
			return err
		}
		return nil
	}
	dispositionErr := run.finalizeAuthorizationDisposition(finalizerContext)
	if errors.Is(dispositionErr, ErrForwardRecoveryDispositionNotRequired) {
		retryErr := run.failAttemptWithBudget(finalizerContext, code, true)
		if retryErr == nil {
			return nil
		}
		if errors.Is(retryErr, ErrForwardRecoveryCommitUnverified) {
			return retryErr
		}
		return nil
	}
	return dispositionErr
}

func (run *forwardRecoveryRun) finalizeAuthorizationDisposition(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || run.consumeFinalizer(2) != nil {
		return ErrForwardRecoveryCommitUnverified
	}
	command, grant, err := run.reconciler.authorizer.AuthorizeForwardRecoveryDisposition(
		ctx,
		run.task.TenantID,
		run.task.ReservationKey,
		run.task.Lease,
		run.task.Attempt,
	)
	if err != nil {
		return err
	}
	receipt, commitErr := run.reconciler.control.CommitForwardRecoveryDisposition(
		ctx,
		grant,
		command,
		run.reconciler.now().UTC(),
	)
	action, outcome := dispositionActionOutcome(command.Disposition)
	if commitErr == nil {
		run.result.Status = ForwardRecoveryExecutionCommitted
		run.result.DecisionDomain = ForwardRecoveryDecisionCurrentAuthorization
		run.result.AuthorizationDisposition = command.Disposition
		run.result.Action = action
		run.result.Outcome = outcome
		run.result.ReceiptState = receipt.State
		run.result.ReceiptRevision = receipt.Revision
		return nil
	}
	query, queryErr := ForwardRecoveryOutcomeQueryForDisposition(command)
	if queryErr != nil {
		return ErrForwardRecoveryCommitUnverified
	}
	_, _, correlationErr := run.correlateCommit(
		ctx,
		query,
		action,
		command.Disposition,
		commitErr,
	)
	return correlationErr
}

func (run *forwardRecoveryRun) consume(steps int) error {
	if steps <= 0 || run.result.Steps > run.reconciler.config.MaxSteps-steps {
		return ErrForwardRecoveryBudgetExhausted
	}
	run.result.Steps += steps
	return nil
}

func (run *forwardRecoveryRun) consumeFinalizer(steps int) error {
	if steps <= 0 || run.finalizers > maxForwardRecoveryFinalizerSteps-steps {
		return ErrForwardRecoveryBudgetExhausted
	}
	run.finalizers += steps
	run.result.FinalizerSteps = run.finalizers
	return nil
}

func forwardRecoveryCapabilityExpired(err error) bool {
	return errors.Is(err, ErrArtifactReadAuthorizationExpired) ||
		errors.Is(err, ErrManifestRepairAuthorizationExpired) ||
		errors.Is(err, ErrForwardRecoveryActionAuthorizationExpired) ||
		errors.Is(err, ErrForwardRecoveryDispositionAuthorizationExpired)
}

func recoveryAttemptFailureForContext(err error) RecoveryAttemptFailureCode {
	if errors.Is(err, context.DeadlineExceeded) {
		return RecoveryAttemptFailureCallerDeadline
	}
	return RecoveryAttemptFailureCallerCanceled
}

func sameClassificationRequest(
	left ArtifactClassificationRequest,
	right ArtifactClassificationRequest,
) bool {
	return canonicalArtifactClassificationRequestBinding(left) ==
		canonicalArtifactClassificationRequestBinding(right)
}

func outcomeForForwardRecoveryAction(action ForwardRecoveryAction) RecoveryAttemptOutcome {
	outcome, _ := forwardRecoveryAttemptOutcomeForDomain(action)
	return outcome
}

func forwardRecoveryAttemptOutcomeForDomain(
	action ForwardRecoveryAction,
) (RecoveryAttemptOutcome, error) {
	switch action {
	case ForwardRecoveryActionMarkStored:
		return RecoveryAttemptOutcomeStored, nil
	case ForwardRecoveryActionMarkRejected:
		return RecoveryAttemptOutcomeRejected, nil
	case ForwardRecoveryActionMarkHold:
		return RecoveryAttemptOutcomeHold, nil
	case ForwardRecoveryActionReleaseLease:
		return RecoveryAttemptOutcomeLeaseReleased, nil
	default:
		return "", ErrInvalidForwardRecoveryExecution
	}
}

func dispositionActionOutcome(
	disposition ForwardRecoveryAuthorizationDisposition,
) (ForwardRecoveryAction, RecoveryAttemptOutcome) {
	if disposition == ForwardRecoveryAuthorizationDenied {
		return ForwardRecoveryActionMarkHold, RecoveryAttemptOutcomeHold
	}
	if disposition == ForwardRecoveryAuthorizationUnavailable {
		return ForwardRecoveryActionReleaseLease, RecoveryAttemptOutcomeLeaseReleased
	}
	return "", ""
}
