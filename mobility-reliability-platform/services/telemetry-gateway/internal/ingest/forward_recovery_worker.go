package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	DefaultForwardRecoveryWorkerPageSize          = 25
	DefaultForwardRecoveryWorkerMaxPages          = 8
	DefaultForwardRecoveryWorkerMaxItems          = 100
	DefaultForwardRecoveryWorkerMaxPageAttempts   = 2
	DefaultForwardRecoveryWorkerPageTimeout       = 10 * time.Second
	DefaultForwardRecoveryWorkerCheckpointTimeout = 5 * time.Second
	DefaultForwardRecoveryWorkerClaimTimeout      = 10 * time.Second
	DefaultForwardRecoveryWorkerRunTimeout        = 15 * time.Minute
	DefaultForwardRecoveryWorkerMaxPanics         = 3
	MaxForwardRecoveryWorkerPages                 = 20
	MaxForwardRecoveryWorkerItems                 = 200
	MaxForwardRecoveryWorkerPageAttempts          = 3
	MaxForwardRecoveryWorkerRunTimeout            = 15 * time.Minute
)

var (
	ErrInvalidForwardRecoveryWorker      = errors.New("forward recovery worker is invalid")
	ErrForwardRecoveryWorkerBudget       = errors.New("forward recovery worker budget is exhausted")
	ErrForwardRecoveryWorkerPage         = errors.New("forward recovery worker page read failed")
	ErrForwardRecoveryWorkerPanicBreaker = errors.New("forward recovery worker panic breaker opened")
)

type ForwardRecoveryExecutor interface {
	Reconcile(context.Context, ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error)
}

type ForwardRecoveryWorkerConfig struct {
	PageSize          int
	MaxPages          int
	MaxItems          int
	MaxPageAttempts   int
	PageTimeout       time.Duration
	CheckpointTimeout time.Duration
	ClaimTimeout      time.Duration
	PerItemTimeout    time.Duration
	RunTimeout        time.Duration
	LeaseDuration     time.Duration
	MaxPanics         int
}

func DefaultForwardRecoveryWorkerConfig() ForwardRecoveryWorkerConfig {
	return ForwardRecoveryWorkerConfig{
		PageSize:          DefaultForwardRecoveryWorkerPageSize,
		MaxPages:          DefaultForwardRecoveryWorkerMaxPages,
		MaxItems:          DefaultForwardRecoveryWorkerMaxItems,
		MaxPageAttempts:   DefaultForwardRecoveryWorkerMaxPageAttempts,
		PageTimeout:       DefaultForwardRecoveryWorkerPageTimeout,
		CheckpointTimeout: DefaultForwardRecoveryWorkerCheckpointTimeout,
		ClaimTimeout:      DefaultForwardRecoveryWorkerClaimTimeout,
		PerItemTimeout:    DefaultForwardRecoveryPerReceiptTimeout,
		RunTimeout:        DefaultForwardRecoveryWorkerRunTimeout,
		LeaseDuration:     DefaultRequestLeaseDuration,
		MaxPanics:         DefaultForwardRecoveryWorkerMaxPanics,
	}
}

type ForwardRecoveryWorkerRunStatus string

const (
	ForwardRecoveryWorkerRunComplete    ForwardRecoveryWorkerRunStatus = "complete"
	ForwardRecoveryWorkerRunCanceled    ForwardRecoveryWorkerRunStatus = "canceled"
	ForwardRecoveryWorkerRunDeadline    ForwardRecoveryWorkerRunStatus = "deadline"
	ForwardRecoveryWorkerRunBudget      ForwardRecoveryWorkerRunStatus = "budget"
	ForwardRecoveryWorkerRunPageError   ForwardRecoveryWorkerRunStatus = "page_error"
	ForwardRecoveryWorkerRunInvalidPage ForwardRecoveryWorkerRunStatus = "invalid_page"
	ForwardRecoveryWorkerRunBreaker     ForwardRecoveryWorkerRunStatus = "breaker"
)

type ForwardRecoveryWorkerCheckpointStatus string

const (
	ForwardRecoveryWorkerCheckpointLoadUnavailable    ForwardRecoveryWorkerCheckpointStatus = "load_unavailable"
	ForwardRecoveryWorkerCheckpointInvalid            ForwardRecoveryWorkerCheckpointStatus = "invalid"
	ForwardRecoveryWorkerCheckpointSaved              ForwardRecoveryWorkerCheckpointStatus = "saved"
	ForwardRecoveryWorkerCheckpointReset              ForwardRecoveryWorkerCheckpointStatus = "reset"
	ForwardRecoveryWorkerCheckpointConflict           ForwardRecoveryWorkerCheckpointStatus = "conflict"
	ForwardRecoveryWorkerCheckpointPersistUnavailable ForwardRecoveryWorkerCheckpointStatus = "persist_unavailable"
)

type ForwardRecoveryWorkerPageStatus string

const (
	ForwardRecoveryWorkerPageOK          ForwardRecoveryWorkerPageStatus = "ok"
	ForwardRecoveryWorkerPageUnavailable ForwardRecoveryWorkerPageStatus = "unavailable"
	ForwardRecoveryWorkerPageInvalid     ForwardRecoveryWorkerPageStatus = "invalid"
)

type ForwardRecoveryWorkerCandidateStatus string

const (
	ForwardRecoveryWorkerCandidateValid     ForwardRecoveryWorkerCandidateStatus = "valid"
	ForwardRecoveryWorkerCandidateInvalid   ForwardRecoveryWorkerCandidateStatus = "invalid"
	ForwardRecoveryWorkerCandidateDuplicate ForwardRecoveryWorkerCandidateStatus = "duplicate"
	ForwardRecoveryWorkerCandidatePoison    ForwardRecoveryWorkerCandidateStatus = "poison"
)

type ForwardRecoveryWorkerClaimStatus string

const (
	ForwardRecoveryWorkerClaimAcquired        ForwardRecoveryWorkerClaimStatus = "acquired"
	ForwardRecoveryWorkerClaimHeld            ForwardRecoveryWorkerClaimStatus = "held"
	ForwardRecoveryWorkerClaimNotDue          ForwardRecoveryWorkerClaimStatus = "not_due"
	ForwardRecoveryWorkerClaimDeadlineElapsed ForwardRecoveryWorkerClaimStatus = "deadline_elapsed"
	ForwardRecoveryWorkerClaimNotEligible     ForwardRecoveryWorkerClaimStatus = "not_eligible"
	ForwardRecoveryWorkerClaimUnknown         ForwardRecoveryWorkerClaimStatus = "unknown"
	ForwardRecoveryWorkerClaimInvalid         ForwardRecoveryWorkerClaimStatus = "invalid"
)

type ForwardRecoveryWorkerItemStatus string

const (
	ForwardRecoveryWorkerItemCommitted        ForwardRecoveryWorkerItemStatus = "committed"
	ForwardRecoveryWorkerItemCorrelated       ForwardRecoveryWorkerItemStatus = "correlated"
	ForwardRecoveryWorkerItemNotCommitted     ForwardRecoveryWorkerItemStatus = "not_committed"
	ForwardRecoveryWorkerItemCommitUnverified ForwardRecoveryWorkerItemStatus = "commit_unverified"
	ForwardRecoveryWorkerItemLeaseUnknown     ForwardRecoveryWorkerItemStatus = "lease_unknown"
	ForwardRecoveryWorkerItemCanceled         ForwardRecoveryWorkerItemStatus = "canceled"
	ForwardRecoveryWorkerItemDeadline         ForwardRecoveryWorkerItemStatus = "deadline"
	ForwardRecoveryWorkerItemBudget           ForwardRecoveryWorkerItemStatus = "budget"
	ForwardRecoveryWorkerItemInvalid          ForwardRecoveryWorkerItemStatus = "invalid"
	ForwardRecoveryWorkerItemPanic            ForwardRecoveryWorkerItemStatus = "panic"
	ForwardRecoveryWorkerItemOther            ForwardRecoveryWorkerItemStatus = "other"
)

type ForwardRecoveryWorkerItemObservation struct {
	Status                   ForwardRecoveryWorkerItemStatus
	DecisionDomain           ForwardRecoveryDecisionDomain
	Classification           ArtifactClassification
	ReasonCode               ArtifactReasonCode
	Action                   ForwardRecoveryAction
	AuthorizationDisposition ForwardRecoveryAuthorizationDisposition
	Outcome                  RecoveryAttemptOutcome
	ControlState             ReceiptState
}

// ForwardRecoveryWorkerObserver exposes only fixed-cardinality enums and
// aggregate sizes/timings. Implementations cannot receive tenant, receipt,
// attempt, cursor, artifact or provider-error values through this interface.
type ForwardRecoveryWorkerObserver interface {
	ObserveForwardRecoveryRun(ForwardRecoveryWorkerRunStatus, time.Duration)
	ObserveForwardRecoveryCheckpoint(ForwardRecoveryWorkerCheckpointStatus)
	ObserveForwardRecoveryPage(ForwardRecoveryWorkerPageStatus, int, time.Duration)
	ObserveForwardRecoveryCandidate(ForwardRecoveryWorkerCandidateStatus)
	ObserveForwardRecoveryClaim(ForwardRecoveryWorkerClaimStatus, time.Duration)
	ObserveForwardRecoveryItem(ForwardRecoveryWorkerItemObservation, time.Duration)
}

// ForwardRecoveryWorkerResult is safe for aggregate operational reporting. It
// intentionally contains no domain identifier, cursor, path or provider error.
type ForwardRecoveryWorkerResult struct {
	Status              ForwardRecoveryWorkerRunStatus
	Pages               int
	Candidates          int
	Invalid             int
	Duplicates          int
	Poisoned            int
	Claims              int
	Acquired            int
	Skipped             int
	ClaimUnknown        int
	Committed           int
	Correlated          int
	Holds               int
	CheckpointFailures  int
	CheckpointConflicts int
	ItemFailures        int
	Panics              int
}

type ForwardRecoveryWorker struct {
	candidates  ForwardRecoveryCandidateStore
	checkpoints ForwardRecoveryCheckpointStore
	leases      RecoveryLeaseStore
	executor    ForwardRecoveryExecutor
	attemptIDs  ServerBatchIDGenerator
	observer    ForwardRecoveryWorkerObserver
	now         func() time.Time
	config      ForwardRecoveryWorkerConfig
	pageRetry   forwardRecoveryPageRetry
}

func NewForwardRecoveryWorker(
	candidates ForwardRecoveryCandidateStore,
	checkpoints ForwardRecoveryCheckpointStore,
	leases RecoveryLeaseStore,
	executor ForwardRecoveryExecutor,
	attemptIDs ServerBatchIDGenerator,
	observer ForwardRecoveryWorkerObserver,
	now func() time.Time,
	config ForwardRecoveryWorkerConfig,
) (*ForwardRecoveryWorker, error) {
	if candidates == nil || checkpoints == nil || leases == nil || executor == nil || attemptIDs == nil {
		return nil, ErrInvalidForwardRecoveryWorker
	}
	if now == nil {
		now = time.Now
	}
	normalized, err := normalizeForwardRecoveryWorkerConfig(config)
	if err != nil {
		return nil, err
	}
	return &ForwardRecoveryWorker{
		candidates:  candidates,
		checkpoints: checkpoints,
		leases:      leases,
		executor:    executor,
		attemptIDs:  attemptIDs,
		observer:    observer,
		now:         now,
		config:      normalized,
		pageRetry:   newForwardRecoveryPageRetry(),
	}, nil
}

func normalizeForwardRecoveryWorkerConfig(
	config ForwardRecoveryWorkerConfig,
) (ForwardRecoveryWorkerConfig, error) {
	defaults := DefaultForwardRecoveryWorkerConfig()
	if config == (ForwardRecoveryWorkerConfig{}) {
		return defaults, nil
	}
	if config.PageSize == 0 {
		config.PageSize = defaults.PageSize
	}
	if config.MaxPages == 0 {
		config.MaxPages = defaults.MaxPages
	}
	if config.MaxItems == 0 {
		config.MaxItems = defaults.MaxItems
	}
	if config.MaxPageAttempts == 0 {
		config.MaxPageAttempts = defaults.MaxPageAttempts
	}
	if config.PageTimeout == 0 {
		config.PageTimeout = defaults.PageTimeout
	}
	if config.CheckpointTimeout == 0 {
		config.CheckpointTimeout = defaults.CheckpointTimeout
	}
	if config.ClaimTimeout == 0 {
		config.ClaimTimeout = defaults.ClaimTimeout
	}
	if config.PerItemTimeout == 0 {
		config.PerItemTimeout = defaults.PerItemTimeout
	}
	if config.RunTimeout == 0 {
		config.RunTimeout = defaults.RunTimeout
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaults.LeaseDuration
	}
	if config.MaxPanics == 0 {
		config.MaxPanics = defaults.MaxPanics
	}
	if config.PageSize < 1 || config.PageSize > MaxForwardRecoveryCandidatePageSize ||
		config.MaxPages < 1 || config.MaxPages > MaxForwardRecoveryWorkerPages ||
		config.MaxItems < 1 || config.MaxItems > MaxForwardRecoveryWorkerItems ||
		config.MaxPageAttempts < 1 || config.MaxPageAttempts > MaxForwardRecoveryWorkerPageAttempts ||
		config.MaxItems > config.PageSize*config.MaxPages ||
		config.PageTimeout <= 0 || config.PageTimeout > 30*time.Second ||
		config.CheckpointTimeout <= 0 || config.CheckpointTimeout > 30*time.Second ||
		config.ClaimTimeout <= 0 || config.ClaimTimeout > 30*time.Second ||
		config.PerItemTimeout < DefaultForwardRecoveryPerReceiptTimeout ||
		config.PerItemTimeout > MaxLeaseDuration ||
		config.RunTimeout < config.PageTimeout+config.ClaimTimeout+config.PerItemTimeout ||
		config.RunTimeout > MaxForwardRecoveryWorkerRunTimeout ||
		config.LeaseDuration < MinLeaseDuration || config.LeaseDuration > MaxLeaseDuration ||
		config.MaxPanics < 1 || config.MaxPanics > 10 {
		return ForwardRecoveryWorkerConfig{}, ErrInvalidForwardRecoveryWorker
	}
	return config, nil
}

func (worker *ForwardRecoveryWorker) Run(
	ctx context.Context,
	tenantID string,
) (ForwardRecoveryWorkerResult, error) {
	startedAt := time.Now()
	result := ForwardRecoveryWorkerResult{}
	finish := func(status ForwardRecoveryWorkerRunStatus, err error) (ForwardRecoveryWorkerResult, error) {
		result.Status = status
		worker.observe(func(observer ForwardRecoveryWorkerObserver) {
			observer.ObserveForwardRecoveryRun(status, time.Since(startedAt))
		})
		return result, err
	}
	if worker == nil || worker.candidates == nil || worker.checkpoints == nil || worker.leases == nil ||
		worker.executor == nil || worker.attemptIDs == nil || worker.now == nil ||
		worker.pageRetry == nil ||
		ctx == nil || !telemetry.IsUUID(tenantID) {
		return result, ErrInvalidForwardRecoveryWorker
	}
	if err := ctx.Err(); err != nil {
		return finish(runStatusForContext(ctx, nil), err)
	}

	cutoff := worker.now().UTC()
	if cutoff.IsZero() {
		return finish(ForwardRecoveryWorkerRunPageError, ErrInvalidForwardRecoveryWorker)
	}
	runStartedAt := time.Now()
	runDeadline := runStartedAt.Add(worker.config.RunTimeout)
	runContext, cancelRun := context.WithTimeout(ctx, worker.config.RunTimeout)
	defer cancelRun()

	checkpointContext, cancelCheckpoint := context.WithTimeout(runContext, worker.config.CheckpointTimeout)
	checkpoint, checkpointErr := worker.checkpoints.LoadForwardRecoveryCheckpoint(checkpointContext, tenantID)
	cancelCheckpoint()
	checkpointWritable := true
	if checkpointErr != nil {
		result.CheckpointFailures++
		checkpointWritable = false
		checkpoint = ForwardRecoveryCheckpoint{}
		worker.observe(func(observer ForwardRecoveryWorkerObserver) {
			observer.ObserveForwardRecoveryCheckpoint(ForwardRecoveryWorkerCheckpointLoadUnavailable)
		})
	} else if ValidateForwardRecoveryCheckpoint(checkpoint) != nil {
		result.CheckpointFailures++
		checkpointWritable = false
		checkpoint = ForwardRecoveryCheckpoint{}
		worker.observe(func(observer ForwardRecoveryWorkerObserver) {
			observer.ObserveForwardRecoveryCheckpoint(ForwardRecoveryWorkerCheckpointInvalid)
		})
	}
	checkpointRevision := checkpoint.Revision
	checkpointHasCursor := checkpoint.Cursor != nil
	var cursor *ForwardRecoveryCursor
	if checkpoint.Cursor != nil {
		cursor = cloneForwardRecoveryCursor(*checkpoint.Cursor)
		cutoff = checkpoint.ScanCutoff.UTC()
	}
	persistCheckpoint := func(next *ForwardRecoveryCursor) {
		if !checkpointWritable {
			return
		}
		persistContext, cancelPersist := context.WithTimeout(runContext, worker.config.CheckpointTimeout)
		defer cancelPersist()
		scanCutoff := time.Time{}
		if next != nil {
			scanCutoff = cutoff
		}
		swapped, persistErr := worker.checkpoints.CompareAndSetForwardRecoveryCheckpoint(
			persistContext,
			tenantID,
			checkpointRevision,
			next,
			scanCutoff,
			worker.now().UTC(),
		)
		if persistErr != nil {
			result.CheckpointFailures++
			checkpointWritable = false
			worker.observe(func(observer ForwardRecoveryWorkerObserver) {
				observer.ObserveForwardRecoveryCheckpoint(ForwardRecoveryWorkerCheckpointPersistUnavailable)
			})
			return
		}
		if !swapped {
			result.CheckpointConflicts++
			checkpointWritable = false
			worker.observe(func(observer ForwardRecoveryWorkerObserver) {
				observer.ObserveForwardRecoveryCheckpoint(ForwardRecoveryWorkerCheckpointConflict)
			})
			return
		}
		checkpointRevision++
		checkpointHasCursor = next != nil
		worker.observe(func(observer ForwardRecoveryWorkerObserver) {
			if next == nil {
				observer.ObserveForwardRecoveryCheckpoint(ForwardRecoveryWorkerCheckpointReset)
				return
			}
			observer.ObserveForwardRecoveryCheckpoint(ForwardRecoveryWorkerCheckpointSaved)
		})
	}

	seen := make(map[string]struct{}, worker.config.MaxItems)
	var lastScanned *ForwardRecoveryCursor
	if cursor != nil {
		lastScanned = cloneForwardRecoveryCursor(*cursor)
	}
	for pageIndex := 0; pageIndex < worker.config.MaxPages; pageIndex++ {
		if err := runContext.Err(); err != nil {
			return finish(runStatusForContext(ctx, runContext), contextErrorForRun(ctx, runContext))
		}
		var page ForwardRecoveryCandidatePage
		var pageReadDuration time.Duration
		for pageAttempt := 1; pageAttempt <= worker.config.MaxPageAttempts; pageAttempt++ {
			pageStartedAt := time.Now()
			pageContext, cancelPage := context.WithTimeout(runContext, worker.config.PageTimeout)
			var pageErr error
			page, pageErr = worker.candidates.ListDueForwardRecoveryCandidates(
				pageContext,
				tenantID,
				cutoff,
				cursor,
				worker.config.PageSize,
			)
			cancelPage()
			if pageErr == nil {
				pageReadDuration = time.Since(pageStartedAt)
				break
			}
			worker.observe(func(observer ForwardRecoveryWorkerObserver) {
				observer.ObserveForwardRecoveryPage(
					ForwardRecoveryWorkerPageUnavailable,
					0,
					time.Since(pageStartedAt),
				)
			})
			if runContext.Err() != nil {
				return finish(runStatusForContext(ctx, runContext), contextErrorForRun(ctx, runContext))
			}
			if pageAttempt == worker.config.MaxPageAttempts {
				return finish(ForwardRecoveryWorkerRunPageError, ErrForwardRecoveryWorkerPage)
			}
			if retryErr := worker.pageRetry.Wait(runContext, pageAttempt); retryErr != nil {
				if runContext.Err() != nil {
					return finish(runStatusForContext(ctx, runContext), contextErrorForRun(ctx, runContext))
				}
				return finish(ForwardRecoveryWorkerRunPageError, ErrForwardRecoveryWorkerPage)
			}
		}
		if validateForwardRecoveryCandidatePage(
			page,
			tenantID,
			cutoff,
			cursor,
			worker.config.PageSize,
		) != nil {
			worker.observe(func(observer ForwardRecoveryWorkerObserver) {
				observer.ObserveForwardRecoveryPage(
					ForwardRecoveryWorkerPageInvalid,
					len(page.Candidates),
					pageReadDuration,
				)
			})
			return finish(ForwardRecoveryWorkerRunInvalidPage, ErrInvalidForwardRecoveryCandidatePage)
		}
		result.Pages++
		worker.observe(func(observer ForwardRecoveryWorkerObserver) {
			observer.ObserveForwardRecoveryPage(
				ForwardRecoveryWorkerPageOK,
				len(page.Candidates),
				pageReadDuration,
			)
		})

		for _, candidate := range page.Candidates {
			if result.Candidates >= worker.config.MaxItems {
				if runContext.Err() != nil {
					return finish(runStatusForContext(ctx, runContext), contextErrorForRun(ctx, runContext))
				}
				persistCheckpoint(lastScanned)
				return finish(ForwardRecoveryWorkerRunBudget, ErrForwardRecoveryWorkerBudget)
			}
			result.Candidates++
			if candidate.TenantID != tenantID || ValidateForwardRecoveryCandidate(candidate) != nil {
				result.Invalid++
				worker.observe(func(observer ForwardRecoveryWorkerObserver) {
					observer.ObserveForwardRecoveryCandidate(ForwardRecoveryWorkerCandidateInvalid)
				})
				lastScanned = cloneForwardRecoveryCursor(cursorForForwardRecoveryCandidate(candidate))
				continue
			}
			dedupeKey := candidate.TenantID + ":" + candidate.ReservationKey
			if _, exists := seen[dedupeKey]; exists {
				result.Duplicates++
				worker.observe(func(observer ForwardRecoveryWorkerObserver) {
					observer.ObserveForwardRecoveryCandidate(ForwardRecoveryWorkerCandidateDuplicate)
				})
				lastScanned = cloneForwardRecoveryCursor(cursorForForwardRecoveryCandidate(candidate))
				continue
			}
			seen[dedupeKey] = struct{}{}
			worker.observe(func(observer ForwardRecoveryWorkerObserver) {
				observer.ObserveForwardRecoveryCandidate(ForwardRecoveryWorkerCandidateValid)
			})

			if err := runContext.Err(); err != nil {
				return finish(runStatusForContext(ctx, runContext), contextErrorForRun(ctx, runContext))
			}
			minimumRemaining := worker.config.ClaimTimeout + worker.config.PerItemTimeout
			if time.Until(runDeadline) < minimumRemaining {
				persistCheckpoint(lastScanned)
				return finish(ForwardRecoveryWorkerRunBudget, ErrForwardRecoveryWorkerBudget)
			}
			attemptID, idErr := worker.attemptIDs.NewID()
			if idErr != nil || !telemetry.IsUUID(attemptID) {
				result.Poisoned++
				worker.observe(func(observer ForwardRecoveryWorkerObserver) {
					observer.ObserveForwardRecoveryCandidate(ForwardRecoveryWorkerCandidatePoison)
				})
				lastScanned = cloneForwardRecoveryCursor(cursorForForwardRecoveryCandidate(candidate))
				continue
			}

			attempt := RecoveryAttemptProposal{ID: attemptID, WorkerVersion: RecoveryWorkerVersion}
			owner := LeaseOwner{ID: attemptID, Kind: LeaseOwnerSweeper}
			claimStartedAt := time.Now()
			claimContext, cancelClaim := context.WithTimeout(runContext, worker.config.ClaimTimeout)
			grant, claimStatus, claimErr := worker.leases.ClaimRecoveryLease(
				claimContext,
				candidate.TenantID,
				candidate.ReservationKey,
				owner,
				attempt,
				worker.now().UTC(),
				worker.config.LeaseDuration,
			)
			cancelClaim()
			result.Claims++
			if claimErr != nil {
				result.ClaimUnknown++
				worker.observe(func(observer ForwardRecoveryWorkerObserver) {
					observer.ObserveForwardRecoveryClaim(
						ForwardRecoveryWorkerClaimUnknown,
						time.Since(claimStartedAt),
					)
				})
				if runContext.Err() != nil {
					return finish(runStatusForContext(ctx, runContext), contextErrorForRun(ctx, runContext))
				}
				lastScanned = cloneForwardRecoveryCursor(cursorForForwardRecoveryCandidate(candidate))
				continue
			}
			mappedClaim, validClaim := validateWorkerClaim(claimStatus, grant, attemptID)
			worker.observe(func(observer ForwardRecoveryWorkerObserver) {
				observer.ObserveForwardRecoveryClaim(mappedClaim, time.Since(claimStartedAt))
			})
			if !validClaim {
				result.ClaimUnknown++
				lastScanned = cloneForwardRecoveryCursor(cursorForForwardRecoveryCandidate(candidate))
				continue
			}
			if claimStatus != LeaseStatusAcquired {
				result.Skipped++
				lastScanned = cloneForwardRecoveryCursor(cursorForForwardRecoveryCandidate(candidate))
				continue
			}

			result.Acquired++
			itemStartedAt := time.Now()
			// A successful claim already created a started attempt. Complete the
			// bounded R6 handoff even if the parent is canceled immediately after
			// the transaction response; no later candidate is claimed afterward.
			itemContext, cancelItem := context.WithTimeout(
				context.WithoutCancel(ctx),
				worker.config.PerItemTimeout,
			)
			execution, executionErr, panicked := executeForwardRecoverySafely(
				itemContext,
				worker.executor,
				ForwardRecoveryTask{
					TenantID:       candidate.TenantID,
					ReservationKey: candidate.ReservationKey,
					Lease:          grant,
					Attempt:        attempt,
				},
			)
			cancelItem()
			itemStatus := classifyForwardRecoveryWorkerItem(execution, executionErr, panicked)
			itemObservation := normalizeForwardRecoveryWorkerItemObservation(execution, itemStatus)
			itemStatus = itemObservation.Status
			worker.observe(func(observer ForwardRecoveryWorkerObserver) {
				observer.ObserveForwardRecoveryItem(itemObservation, time.Since(itemStartedAt))
			})
			if itemObservation.Outcome == RecoveryAttemptOutcomeHold &&
				itemObservation.ControlState == ReceiptRecoveryHold {
				result.Holds++
			}
			switch itemStatus {
			case ForwardRecoveryWorkerItemCommitted:
				result.Committed++
			case ForwardRecoveryWorkerItemCorrelated:
				result.Correlated++
			case ForwardRecoveryWorkerItemPanic:
				result.Panics++
				result.ItemFailures++
				lastScanned = cloneForwardRecoveryCursor(cursorForForwardRecoveryCandidate(candidate))
				if result.Panics >= worker.config.MaxPanics {
					persistCheckpoint(lastScanned)
					return finish(ForwardRecoveryWorkerRunBreaker, ErrForwardRecoveryWorkerPanicBreaker)
				}
			default:
				result.ItemFailures++
			}
			lastScanned = cloneForwardRecoveryCursor(cursorForForwardRecoveryCandidate(candidate))
			if runContext.Err() != nil {
				return finish(runStatusForContext(ctx, runContext), contextErrorForRun(ctx, runContext))
			}
		}

		if runContext.Err() != nil {
			return finish(runStatusForContext(ctx, runContext), contextErrorForRun(ctx, runContext))
		}
		if page.Exhausted {
			if checkpointHasCursor {
				persistCheckpoint(nil)
			}
			return finish(ForwardRecoveryWorkerRunComplete, nil)
		}
		persistCheckpoint(page.NextCursor)
		copy := *page.NextCursor
		cursor = &copy
	}
	return finish(ForwardRecoveryWorkerRunBudget, ErrForwardRecoveryWorkerBudget)
}

func normalizeForwardRecoveryWorkerItemObservation(
	result ForwardRecoveryExecutionResult,
	status ForwardRecoveryWorkerItemStatus,
) ForwardRecoveryWorkerItemObservation {
	observation := ForwardRecoveryWorkerItemObservation{Status: status}
	if status != ForwardRecoveryWorkerItemCommitted && status != ForwardRecoveryWorkerItemCorrelated {
		return observation
	}
	if result.ReceiptRevision <= 0 || !validOutcomeReceiptState(result.ReceiptState) ||
		!terminalForwardRecoveryAction(result.Action) || !ValidRecoveryAttemptOutcome(result.Outcome) {
		observation.Status = ForwardRecoveryWorkerItemInvalid
		return observation
	}
	expectedOutcome, outcomeErr := forwardRecoveryAttemptOutcomeForDomain(result.Action)
	expectedState := receiptStateForForwardRecoveryAction(result.Action)
	if outcomeErr != nil || result.Outcome != expectedOutcome || result.ReceiptState != expectedState {
		observation.Status = ForwardRecoveryWorkerItemInvalid
		return observation
	}
	switch result.DecisionDomain {
	case ForwardRecoveryDecisionArtifactReconciliation:
		if result.AuthorizationDisposition != "" ||
			!validForwardRecoveryArtifactTerminalTuple(
				result.Action,
				result.Classification,
				result.ReasonCode,
			) {
			observation.Status = ForwardRecoveryWorkerItemInvalid
			return observation
		}
	case ForwardRecoveryDecisionCurrentAuthorization:
		expectedAction, expectedDispositionOutcome := dispositionActionOutcome(result.AuthorizationDisposition)
		if expectedAction == "" || result.Classification != "" || result.ReasonCode != "" ||
			result.Action != expectedAction || result.Outcome != expectedDispositionOutcome {
			observation.Status = ForwardRecoveryWorkerItemInvalid
			return observation
		}
	default:
		observation.Status = ForwardRecoveryWorkerItemInvalid
		return observation
	}
	observation.DecisionDomain = result.DecisionDomain
	observation.Classification = result.Classification
	observation.ReasonCode = result.ReasonCode
	observation.Action = result.Action
	observation.AuthorizationDisposition = result.AuthorizationDisposition
	observation.Outcome = result.Outcome
	observation.ControlState = result.ReceiptState
	return observation
}

// validForwardRecoveryArtifactTerminalTuple narrows a fixed-cardinality
// classification/reason pair to terminal combinations the R6 planner can
// actually commit. This prevents a buggy executor from emitting individually
// valid but semantically impossible metric labels.
func validForwardRecoveryArtifactTerminalTuple(
	action ForwardRecoveryAction,
	classification ArtifactClassification,
	reason ArtifactReasonCode,
) bool {
	if !validArtifactClassificationOutcome(ArtifactReadForwardRecovery, classification, reason) {
		return false
	}
	switch action {
	case ForwardRecoveryActionMarkStored:
		return classification == ArtifactClassificationValidComplete &&
			reason == ArtifactReasonManifestAndReferencedRawValid
	case ForwardRecoveryActionMarkRejected:
		return classification == ArtifactClassificationRawContentConflict
	case ForwardRecoveryActionMarkHold:
		return classification != ArtifactClassificationUnavailable ||
			!transientForwardRecoveryArtifactReason(reason)
	case ForwardRecoveryActionReleaseLease:
		return classification == ArtifactClassificationNone ||
			(classification == ArtifactClassificationUnavailable &&
				transientForwardRecoveryArtifactReason(reason))
	default:
		return false
	}
}

func transientForwardRecoveryArtifactReason(reason ArtifactReasonCode) bool {
	switch reason {
	case ArtifactReasonQuotaLimited,
		ArtifactReasonProviderTimeout,
		ArtifactReasonProviderCancelled,
		ArtifactReasonProviderUnavailable:
		return true
	default:
		return false
	}
}

func receiptStateForForwardRecoveryAction(action ForwardRecoveryAction) ReceiptState {
	switch action {
	case ForwardRecoveryActionMarkStored:
		return ReceiptStored
	case ForwardRecoveryActionMarkRejected:
		return ReceiptRejected
	case ForwardRecoveryActionMarkHold:
		return ReceiptRecoveryHold
	case ForwardRecoveryActionReleaseLease:
		return ReceiptReserved
	default:
		return ""
	}
}

func cloneForwardRecoveryCursor(cursor ForwardRecoveryCursor) *ForwardRecoveryCursor {
	copy := cursor
	return &copy
}

func validateWorkerClaim(
	status LeaseStatus,
	grant LeaseGrant,
	attemptID string,
) (ForwardRecoveryWorkerClaimStatus, bool) {
	if status == LeaseStatusAcquired {
		valid := ValidateLeaseGrant(grant) == nil && grant.OwnerKind == LeaseOwnerSweeper &&
			grant.Fence.OwnerID == attemptID
		if !valid {
			return ForwardRecoveryWorkerClaimInvalid, false
		}
		return ForwardRecoveryWorkerClaimAcquired, true
	}
	if grant != (LeaseGrant{}) {
		return ForwardRecoveryWorkerClaimInvalid, false
	}
	switch status {
	case LeaseStatusHeld:
		return ForwardRecoveryWorkerClaimHeld, true
	case LeaseStatusNotDue:
		return ForwardRecoveryWorkerClaimNotDue, true
	case LeaseStatusDeadlineElapsed:
		return ForwardRecoveryWorkerClaimDeadlineElapsed, true
	case LeaseStatusNotEligible:
		return ForwardRecoveryWorkerClaimNotEligible, true
	default:
		return ForwardRecoveryWorkerClaimInvalid, false
	}
}

func executeForwardRecoverySafely(
	ctx context.Context,
	executor ForwardRecoveryExecutor,
	task ForwardRecoveryTask,
) (result ForwardRecoveryExecutionResult, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			result = ForwardRecoveryExecutionResult{}
			err = nil
			panicked = true
		}
	}()
	result, err = executor.Reconcile(ctx, task)
	return result, err, false
}

func classifyForwardRecoveryWorkerItem(
	result ForwardRecoveryExecutionResult,
	err error,
	panicked bool,
) ForwardRecoveryWorkerItemStatus {
	if panicked {
		return ForwardRecoveryWorkerItemPanic
	}
	if err != nil && (result.Status == ForwardRecoveryExecutionCommitted ||
		result.Status == ForwardRecoveryExecutionCorrelated) {
		return ForwardRecoveryWorkerItemInvalid
	}
	switch result.Status {
	case ForwardRecoveryExecutionCommitted:
		return ForwardRecoveryWorkerItemCommitted
	case ForwardRecoveryExecutionCorrelated:
		return ForwardRecoveryWorkerItemCorrelated
	}
	switch {
	case errors.Is(err, ErrForwardRecoveryNotCommitted):
		return ForwardRecoveryWorkerItemNotCommitted
	case errors.Is(err, ErrForwardRecoveryCommitUnverified):
		return ForwardRecoveryWorkerItemCommitUnverified
	case errors.Is(err, ErrForwardRecoveryLeaseUnknown):
		return ForwardRecoveryWorkerItemLeaseUnknown
	case errors.Is(err, context.Canceled):
		return ForwardRecoveryWorkerItemCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ForwardRecoveryWorkerItemDeadline
	case errors.Is(err, ErrForwardRecoveryBudgetExhausted):
		return ForwardRecoveryWorkerItemBudget
	case errors.Is(err, ErrInvalidForwardRecoveryExecution), err == nil:
		return ForwardRecoveryWorkerItemInvalid
	default:
		return ForwardRecoveryWorkerItemOther
	}
}

func runStatusForContext(
	parent context.Context,
	run context.Context,
) ForwardRecoveryWorkerRunStatus {
	if parent != nil && errors.Is(parent.Err(), context.Canceled) {
		return ForwardRecoveryWorkerRunCanceled
	}
	if parent != nil && errors.Is(parent.Err(), context.DeadlineExceeded) {
		return ForwardRecoveryWorkerRunDeadline
	}
	if run != nil && errors.Is(run.Err(), context.Canceled) {
		return ForwardRecoveryWorkerRunCanceled
	}
	return ForwardRecoveryWorkerRunDeadline
}

func contextErrorForRun(parent context.Context, run context.Context) error {
	if parent != nil && parent.Err() != nil {
		return parent.Err()
	}
	if run != nil && run.Err() != nil {
		return run.Err()
	}
	return context.DeadlineExceeded
}

func (worker *ForwardRecoveryWorker) observe(
	operation func(ForwardRecoveryWorkerObserver),
) {
	if worker == nil || worker.observer == nil || operation == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	operation(worker.observer)
}
