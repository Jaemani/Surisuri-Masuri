package ingest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	workerTenantID = "11111111-1111-4111-8111-111111111111"
	workerAttemptA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
	workerAttemptB = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2"
	workerAttemptC = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3"
)

func TestForwardRecoveryWorkerWalksDeterministicPagesWithFixedCutoff(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	first := workerCandidate(1, now.Add(-3*time.Minute), "a")
	second := workerCandidate(2, now.Add(-2*time.Minute), "b")
	third := workerCandidate(3, now.Add(-time.Minute), "c")
	cursor := cursorForForwardRecoveryCandidate(second)
	store := &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{
		{Candidates: []ForwardRecoveryCandidate{first, second}, NextCursor: &cursor},
		{Candidates: []ForwardRecoveryCandidate{third}, Exhausted: true},
	}}
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{
		{status: LeaseStatusHeld},
		{status: LeaseStatusNotDue},
		{status: LeaseStatusNotEligible},
	}}
	worker := newWorkerHarness(t, store, leases, nil, []workerIDResult{
		{id: workerAttemptA}, {id: workerAttemptB}, {id: workerAttemptC},
	}, now, workerConfig(2, 2, 4), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ForwardRecoveryWorkerRunComplete || result.Pages != 2 ||
		result.Candidates != 3 || result.Claims != 3 || result.Skipped != 3 {
		t.Fatalf("Run() result = %#v", result)
	}
	if len(store.calls) != 2 || !store.calls[0].cutoff.Equal(now) ||
		!store.calls[1].cutoff.Equal(now) || store.calls[0].after != nil ||
		store.calls[1].after == nil || compareForwardRecoveryCursor(*store.calls[1].after, cursor) != 0 {
		t.Fatalf("candidate calls = %#v", store.calls)
	}
}

func TestForwardRecoveryWorkerExecutesOnlyAcquiredClaim(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	candidates := []ForwardRecoveryCandidate{
		workerCandidate(1, now.Add(-3*time.Minute), "a"),
		workerCandidate(2, now.Add(-2*time.Minute), "b"),
		workerCandidate(3, now.Add(-time.Minute), "c"),
	}
	store := &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{{
		Candidates: candidates,
		Exhausted:  true,
	}}}
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{
		{status: LeaseStatusHeld},
		{status: LeaseStatusAcquired, validGrant: true},
		{status: LeaseStatusDeadlineElapsed},
	}}
	var tasks []ForwardRecoveryTask
	executor := forwardRecoveryExecutorFunc(func(_ context.Context, task ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
		tasks = append(tasks, task)
		return workerStoredExecution(ForwardRecoveryExecutionCommitted), nil
	})
	worker := newWorkerHarness(t, store, leases, executor, []workerIDResult{
		{id: workerAttemptA}, {id: workerAttemptB}, {id: workerAttemptC},
	}, now, workerConfig(3, 1, 3), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.Committed != 1 || result.Acquired != 1 || result.Skipped != 2 {
		t.Fatalf("Run() = %#v, %v", result, err)
	}
	if len(tasks) != 1 || tasks[0].Attempt.ID != workerAttemptB ||
		tasks[0].Lease.Fence.OwnerID != workerAttemptB ||
		tasks[0].ReservationKey != candidates[1].ReservationKey {
		t.Fatalf("executor tasks = %#v", tasks)
	}
}

func TestForwardRecoveryWorkerIsolatesClaimUnknownAndContinues(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := candidateStoreFor(now, 2)
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{
		{err: errors.New("provider response lost")},
		{status: LeaseStatusAcquired, validGrant: true},
	}}
	executions := 0
	executor := forwardRecoveryExecutorFunc(func(context.Context, ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
		executions++
		return workerStoredExecution(ForwardRecoveryExecutionCommitted), nil
	})
	worker := newWorkerHarness(t, store, leases, executor, []workerIDResult{
		{id: workerAttemptA}, {id: workerAttemptB},
	}, now, workerConfig(2, 1, 2), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.ClaimUnknown != 1 || result.Committed != 1 || executions != 1 {
		t.Fatalf("Run() = %#v, %v; executions = %d", result, err, executions)
	}
}

func TestForwardRecoveryWorkerIsolatesExecutionErrorAndPanic(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := candidateStoreFor(now, 3)
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{
		{status: LeaseStatusAcquired, validGrant: true},
		{status: LeaseStatusAcquired, validGrant: true},
		{status: LeaseStatusAcquired, validGrant: true},
	}}
	call := 0
	executor := forwardRecoveryExecutorFunc(func(context.Context, ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
		call++
		switch call {
		case 1:
			return ForwardRecoveryExecutionResult{}, ErrForwardRecoveryCommitUnverified
		case 2:
			panic("sensitive provider failure")
		default:
			return workerStoredExecution(ForwardRecoveryExecutionCorrelated), nil
		}
	})
	worker := newWorkerHarness(t, store, leases, executor, []workerIDResult{
		{id: workerAttemptA}, {id: workerAttemptB}, {id: workerAttemptC},
	}, now, workerConfig(3, 1, 3), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.ItemFailures != 2 || result.Panics != 1 ||
		result.Correlated != 1 || call != 3 {
		t.Fatalf("Run() = %#v, %v; executions = %d", result, err, call)
	}
}

func TestForwardRecoveryWorkerDrainsAcquiredClaimAfterParentCancellation(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	store := candidateStoreFor(now, 2)
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{{
		status: LeaseStatusAcquired, validGrant: true, hook: cancel,
	}}}
	executedWithLiveContext := false
	executor := forwardRecoveryExecutorFunc(func(runContext context.Context, _ ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
		executedWithLiveContext = runContext.Err() == nil
		return workerStoredExecution(ForwardRecoveryExecutionCommitted), nil
	})
	worker := newWorkerHarness(t, store, leases, executor, []workerIDResult{{id: workerAttemptA}}, now, workerConfig(2, 1, 2), nil)

	result, err := worker.Run(ctx, workerTenantID)
	if !errors.Is(err, context.Canceled) || result.Status != ForwardRecoveryWorkerRunCanceled ||
		result.Committed != 1 || !executedWithLiveContext || leases.calls != 1 {
		t.Fatalf("Run() = %#v, %v; live = %v, claims = %d", result, err, executedWithLiveContext, leases.calls)
	}
}

func TestForwardRecoveryWorkerRejectsMalformedClaimResultsWithoutExecution(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := candidateStoreFor(now, 3)
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{
		{status: LeaseStatusHeld, validGrant: true},
		{status: LeaseStatusAcquired},
		{status: LeaseStatusAcquired, validGrant: true},
	}}
	executions := 0
	executor := forwardRecoveryExecutorFunc(func(context.Context, ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
		executions++
		return workerStoredExecution(ForwardRecoveryExecutionCommitted), nil
	})
	worker := newWorkerHarness(t, store, leases, executor, []workerIDResult{
		{id: workerAttemptA}, {id: workerAttemptB}, {id: workerAttemptC},
	}, now, workerConfig(3, 1, 3), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.ClaimUnknown != 2 || result.Committed != 1 || executions != 1 {
		t.Fatalf("Run() = %#v, %v; executions = %d", result, err, executions)
	}
}

func TestForwardRecoveryWorkerIsolatesAttemptIDFailure(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := candidateStoreFor(now, 3)
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{{status: LeaseStatusNotEligible}}}
	worker := newWorkerHarness(t, store, leases, nil, []workerIDResult{
		{err: errors.New("entropy source unavailable")},
		{id: "not-a-uuid"},
		{id: workerAttemptC},
	}, now, workerConfig(3, 1, 3), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.Poisoned != 2 || result.Claims != 1 || result.Skipped != 1 {
		t.Fatalf("Run() = %#v, %v", result, err)
	}
}

func TestForwardRecoveryWorkerStopsAtItemAndPageBudgets(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		config ForwardRecoveryWorkerConfig
		page   ForwardRecoveryCandidatePage
	}{
		{
			name:   "item budget",
			config: workerConfig(2, 1, 1),
			page: ForwardRecoveryCandidatePage{
				Candidates: []ForwardRecoveryCandidate{
					workerCandidate(1, now.Add(-2*time.Minute), "a"),
					workerCandidate(2, now.Add(-time.Minute), "b"),
				},
				Exhausted: true,
			},
		},
		{
			name:   "page budget",
			config: workerConfig(1, 1, 1),
			page: func() ForwardRecoveryCandidatePage {
				candidate := workerCandidate(1, now.Add(-time.Minute), "a")
				cursor := cursorForForwardRecoveryCandidate(candidate)
				return ForwardRecoveryCandidatePage{Candidates: []ForwardRecoveryCandidate{candidate}, NextCursor: &cursor}
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{test.page}}
			checkpoints := &memoryWorkerCheckpointStore{}
			leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{{status: LeaseStatusNotEligible}}}
			worker := newWorkerHarnessWithCheckpoint(
				t,
				store,
				checkpoints,
				leases,
				nil,
				[]workerIDResult{{id: workerAttemptA}},
				now,
				test.config,
				nil,
			)
			result, err := worker.Run(context.Background(), workerTenantID)
			if !errors.Is(err, ErrForwardRecoveryWorkerBudget) ||
				result.Status != ForwardRecoveryWorkerRunBudget || result.Claims != 1 ||
				checkpoints.checkpoint.Cursor == nil ||
				checkpoints.checkpoint.Cursor.DocumentID != test.page.Candidates[0].DocumentID ||
				!checkpoints.checkpoint.ScanCutoff.Equal(now) {
				t.Fatalf("Run() = %#v, %v; checkpoint = %#v", result, err, checkpoints.checkpoint)
			}
		})
	}
}

func TestForwardRecoveryWorkerCheckpointAdvancesPastPoisonAcrossRunsAndWraps(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	poison := workerCandidate(1, now.Add(-2*time.Minute), "a")
	poison.ReservationKey = "malformed"
	healthy := workerCandidate(2, now.Add(-time.Minute), "b")
	poisonCursor := cursorForForwardRecoveryCandidate(poison)
	store := &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{
		{Candidates: []ForwardRecoveryCandidate{poison}, NextCursor: &poisonCursor},
		{Candidates: []ForwardRecoveryCandidate{healthy}, Exhausted: true},
		{Exhausted: true},
	}}
	checkpoints := &memoryWorkerCheckpointStore{}
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{{status: LeaseStatusNotEligible}}}
	worker := newWorkerHarnessWithCheckpoint(
		t,
		store,
		checkpoints,
		leases,
		nil,
		[]workerIDResult{{id: workerAttemptA}},
		now,
		workerConfig(1, 1, 1),
		nil,
	)
	currentNow := now
	worker.now = func() time.Time { return currentNow }

	first, firstErr := worker.Run(context.Background(), workerTenantID)
	if !errors.Is(firstErr, ErrForwardRecoveryWorkerBudget) || first.Invalid != 1 || first.Claims != 0 ||
		checkpoints.checkpoint.Cursor == nil || checkpoints.checkpoint.Cursor.DocumentID != poison.DocumentID ||
		!checkpoints.checkpoint.ScanCutoff.Equal(now) {
		t.Fatalf("first Run() = %#v, %v; checkpoint = %#v", first, firstErr, checkpoints.checkpoint)
	}
	currentNow = now.Add(time.Hour)
	second, secondErr := worker.Run(context.Background(), workerTenantID)
	if secondErr != nil || second.Status != ForwardRecoveryWorkerRunComplete || second.Claims != 1 ||
		checkpoints.checkpoint.Cursor != nil || checkpoints.checkpoint.Revision != 2 {
		t.Fatalf("second Run() = %#v, %v; checkpoint = %#v", second, secondErr, checkpoints.checkpoint)
	}
	if len(store.calls) != 2 || store.calls[1].after == nil ||
		store.calls[1].after.DocumentID != poison.DocumentID || !store.calls[1].cutoff.Equal(now) {
		t.Fatalf("candidate calls = %#v", store.calls)
	}
	third, thirdErr := worker.Run(context.Background(), workerTenantID)
	if thirdErr != nil || third.Status != ForwardRecoveryWorkerRunComplete ||
		len(store.calls) != 3 || store.calls[2].after != nil ||
		!store.calls[2].cutoff.Equal(currentNow) || checkpoints.checkpoint.Revision != 2 {
		t.Fatalf("third Run() = %#v, %v; calls = %#v; checkpoint = %#v", third, thirdErr, store.calls, checkpoints.checkpoint)
	}
}

func TestForwardRecoveryWorkerTreatsCheckpointUnavailableAsAdvisory(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := candidateStoreFor(now, 1)
	checkpoints := &memoryWorkerCheckpointStore{loadErr: errors.New("provider secret")}
	worker := newWorkerHarnessWithCheckpoint(
		t,
		store,
		checkpoints,
		&scriptedWorkerLeaseStore{responses: []workerClaimResponse{{status: LeaseStatusNotEligible}}},
		nil,
		[]workerIDResult{{id: workerAttemptA}},
		now,
		workerConfig(1, 1, 1),
		nil,
	)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.Status != ForwardRecoveryWorkerRunComplete ||
		result.CheckpointFailures != 1 || len(store.calls) != 1 || result.Claims != 1 {
		t.Fatalf("Run() = %#v, %v; candidate calls = %d", result, err, len(store.calls))
	}
}

func TestForwardRecoveryWorkerRejectsIncoherentPage(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	candidate := workerCandidate(1, now.Add(-time.Minute), "a")
	wrong := ForwardRecoveryCursor{NextRecoveryAt: candidate.NextRecoveryAt, DocumentID: workerReceiptID(2)}
	store := &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{{
		Candidates: []ForwardRecoveryCandidate{candidate},
		NextCursor: &wrong,
	}}}
	worker := newWorkerHarness(t, store, &scriptedWorkerLeaseStore{}, nil, nil, now, workerConfig(1, 1, 1), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if !errors.Is(err, ErrInvalidForwardRecoveryCandidatePage) ||
		result.Status != ForwardRecoveryWorkerRunInvalidPage || result.Claims != 0 {
		t.Fatalf("Run() = %#v, %v", result, err)
	}
}

func TestForwardRecoveryWorkerStopsOnPageFailureWithoutClaim(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := &scriptedWorkerCandidateStore{
		errs: []error{errors.New("provider detail must not escape")},
	}
	worker := newWorkerHarness(t, store, &scriptedWorkerLeaseStore{}, nil, nil, now, workerConfig(1, 1, 1), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if !errors.Is(err, ErrForwardRecoveryWorkerPage) ||
		result.Status != ForwardRecoveryWorkerRunPageError || result.Claims != 0 ||
		strings.Contains(err.Error(), "provider detail") {
		t.Fatalf("Run() = %#v, %v", result, err)
	}
}

func TestForwardRecoveryWorkerRetriesReadOnlyPageWithDeterministicSeam(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := &scriptedWorkerCandidateStore{
		pages: []ForwardRecoveryCandidatePage{{}, {Exhausted: true}},
		errs:  []error{errors.New("transient query failure"), nil},
	}
	retry := &recordingWorkerPageRetry{}
	worker := newWorkerHarness(t, store, &scriptedWorkerLeaseStore{}, nil, nil, now, workerConfig(1, 1, 1), nil)
	worker.pageRetry = retry

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.Status != ForwardRecoveryWorkerRunComplete ||
		len(store.calls) != 2 || len(retry.failures) != 1 || retry.failures[0] != 1 {
		t.Fatalf("Run() = %#v, %v; calls = %d, retries = %#v", result, err, len(store.calls), retry.failures)
	}
}

func TestForwardRecoveryWorkerDeduplicatesReservationWithinRun(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	first := workerCandidate(1, now.Add(-2*time.Minute), "a")
	duplicate := workerCandidate(2, now.Add(-time.Minute), "a")
	store := &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{{
		Candidates: []ForwardRecoveryCandidate{first, duplicate},
		Exhausted:  true,
	}}}
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{{status: LeaseStatusNotEligible}}}
	worker := newWorkerHarness(t, store, leases, nil, []workerIDResult{{id: workerAttemptA}}, now, workerConfig(2, 1, 2), nil)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.Duplicates != 1 || result.Claims != 1 || result.Skipped != 1 {
		t.Fatalf("Run() = %#v, %v", result, err)
	}
}

func TestForwardRecoveryWorkerStopsBeforeAnyWorkWhenCallerCanceled(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := candidateStoreFor(now, 1)
	worker := newWorkerHarness(t, store, &scriptedWorkerLeaseStore{}, nil, nil, now, workerConfig(1, 1, 1), nil)

	result, err := worker.Run(ctx, workerTenantID)
	if !errors.Is(err, context.Canceled) || result.Status != ForwardRecoveryWorkerRunCanceled ||
		len(store.calls) != 0 || result.Claims != 0 {
		t.Fatalf("Run() = %#v, %v; page calls = %d", result, err, len(store.calls))
	}
}

func TestForwardRecoveryWorkerPanicBreakerStopsFurtherClaims(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	first := workerCandidate(1, now.Add(-2*time.Minute), "a")
	second := workerCandidate(2, now.Add(-time.Minute), "b")
	store := &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{
		{Candidates: []ForwardRecoveryCandidate{first, second}, Exhausted: true},
		{Candidates: []ForwardRecoveryCandidate{second}, Exhausted: true},
	}}
	checkpoints := &memoryWorkerCheckpointStore{}
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{
		{status: LeaseStatusAcquired, validGrant: true},
		{status: LeaseStatusAcquired, validGrant: true},
	}}
	executions := 0
	executor := forwardRecoveryExecutorFunc(func(context.Context, ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
		executions++
		if executions == 1 {
			panic("poison")
		}
		return workerStoredExecution(ForwardRecoveryExecutionCommitted), nil
	})
	config := workerConfig(2, 1, 2)
	config.MaxPanics = 1
	worker := newWorkerHarnessWithCheckpoint(
		t,
		store,
		checkpoints,
		leases,
		executor,
		[]workerIDResult{{id: workerAttemptA}, {id: workerAttemptB}},
		now,
		config,
		nil,
	)

	result, err := worker.Run(context.Background(), workerTenantID)
	if !errors.Is(err, ErrForwardRecoveryWorkerPanicBreaker) ||
		result.Status != ForwardRecoveryWorkerRunBreaker || result.Claims != 1 || result.Panics != 1 ||
		checkpoints.checkpoint.Cursor == nil || checkpoints.checkpoint.Cursor.DocumentID != first.DocumentID ||
		!checkpoints.checkpoint.ScanCutoff.Equal(now) {
		t.Fatalf("Run() = %#v, %v; checkpoint = %#v", result, err, checkpoints.checkpoint)
	}

	resumed, resumedErr := worker.Run(context.Background(), workerTenantID)
	if resumedErr != nil || resumed.Status != ForwardRecoveryWorkerRunComplete ||
		resumed.Committed != 1 || executions != 2 || len(store.calls) != 2 ||
		store.calls[1].after == nil || store.calls[1].after.DocumentID != first.DocumentID ||
		!store.calls[1].cutoff.Equal(now) || checkpoints.checkpoint.Cursor != nil {
		t.Fatalf("resumed Run() = %#v, %v; executions = %d; calls = %#v; checkpoint = %#v", resumed, resumedErr, executions, store.calls, checkpoints.checkpoint)
	}
}

func TestForwardRecoveryWorkerMetricsAndResultExcludeProviderSecrets(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	secret := "uid=user@example.com path=tenant/raw/lat=37.1"
	store := candidateStoreFor(now, 1)
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{{err: errors.New(secret)}}}
	observer := &recordingWorkerObserver{}
	worker := newWorkerHarness(t, store, leases, nil, []workerIDResult{{id: workerAttemptA}}, now, workerConfig(1, 1, 1), observer)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	serialized := fmt.Sprintf("%#v %#v", result, observer)
	if strings.Contains(serialized, secret) || strings.Contains(serialized, "user@example.com") ||
		strings.Contains(serialized, "lat=37.1") {
		t.Fatalf("operational output leaked provider detail: %s", serialized)
	}
	assertWorkerPublicResultPrivacy(t, reflect.TypeOf(result))
	assertWorkerPublicResultPrivacy(t, reflect.TypeOf(ForwardRecoveryWorkerItemObservation{}))

	maliciousObserver := &recordingWorkerObserver{}
	maliciousExecutor := forwardRecoveryExecutorFunc(func(context.Context, ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
		return ForwardRecoveryExecutionResult{
			Status:          ForwardRecoveryExecutionCommitted,
			DecisionDomain:  ForwardRecoveryDecisionDomain(secret),
			Classification:  ArtifactClassification(secret),
			ReasonCode:      ArtifactReasonCode(secret),
			Action:          ForwardRecoveryAction(secret),
			Outcome:         RecoveryAttemptOutcome(secret),
			ReceiptState:    ReceiptState(secret),
			ReceiptRevision: 2,
		}, nil
	})
	maliciousWorker := newWorkerHarness(
		t,
		candidateStoreFor(now, 1),
		&scriptedWorkerLeaseStore{responses: []workerClaimResponse{{status: LeaseStatusAcquired, validGrant: true}}},
		maliciousExecutor,
		[]workerIDResult{{id: workerAttemptB}},
		now,
		workerConfig(1, 1, 1),
		maliciousObserver,
	)
	maliciousResult, maliciousErr := maliciousWorker.Run(context.Background(), workerTenantID)
	if maliciousErr != nil || maliciousResult.ItemFailures != 1 || len(maliciousObserver.items) != 1 ||
		maliciousObserver.items[0] != (ForwardRecoveryWorkerItemObservation{Status: ForwardRecoveryWorkerItemInvalid}) ||
		strings.Contains(fmt.Sprintf("%#v %#v", maliciousResult, maliciousObserver), secret) {
		t.Fatalf("malicious Run() = %#v, %v; observations = %#v", maliciousResult, maliciousErr, maliciousObserver.items)
	}
}

func TestForwardRecoveryWorkerZeroizesEachInvalidMetricDimensionAndImpossibleTuple(t *testing.T) {
	secret := "uid=user@example.com path=tenant/raw/lat=37.1"
	tests := []struct {
		name   string
		mutate func(*ForwardRecoveryExecutionResult)
	}{
		{name: "decision domain", mutate: func(result *ForwardRecoveryExecutionResult) {
			result.DecisionDomain = ForwardRecoveryDecisionDomain(secret)
		}},
		{name: "classification", mutate: func(result *ForwardRecoveryExecutionResult) {
			result.Classification = ArtifactClassification(secret)
		}},
		{name: "reason", mutate: func(result *ForwardRecoveryExecutionResult) {
			result.ReasonCode = ArtifactReasonCode(secret)
		}},
		{name: "action", mutate: func(result *ForwardRecoveryExecutionResult) {
			result.Action = ForwardRecoveryAction(secret)
		}},
		{name: "authorization disposition", mutate: func(result *ForwardRecoveryExecutionResult) {
			result.AuthorizationDisposition = ForwardRecoveryAuthorizationDisposition(secret)
		}},
		{name: "outcome", mutate: func(result *ForwardRecoveryExecutionResult) {
			result.Outcome = RecoveryAttemptOutcome(secret)
		}},
		{name: "receipt state", mutate: func(result *ForwardRecoveryExecutionResult) {
			result.ReceiptState = ReceiptState(secret)
		}},
		{name: "valid enums but impossible terminal tuple", mutate: func(result *ForwardRecoveryExecutionResult) {
			result.Classification = ArtifactClassificationNone
			result.ReasonCode = ArtifactReasonNoCandidates
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := workerStoredExecution(ForwardRecoveryExecutionCommitted)
			test.mutate(&result)
			observation := normalizeForwardRecoveryWorkerItemObservation(
				result,
				ForwardRecoveryWorkerItemCommitted,
			)
			if observation != (ForwardRecoveryWorkerItemObservation{Status: ForwardRecoveryWorkerItemInvalid}) ||
				strings.Contains(fmt.Sprintf("%#v", observation), secret) {
				t.Fatalf("observation = %#v", observation)
			}
		})
	}

	if status := classifyForwardRecoveryWorkerItem(
		workerStoredExecution(ForwardRecoveryExecutionCommitted),
		errors.New("incoherent success response"),
		false,
	); status != ForwardRecoveryWorkerItemInvalid {
		t.Fatalf("success result with error status = %q", status)
	}
}

func TestForwardRecoveryWorkerObservesBoundedActionAndHoldWithoutIdentifiers(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := candidateStoreFor(now, 1)
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{{
		status: LeaseStatusAcquired, validGrant: true,
	}}}
	observer := &recordingWorkerObserver{}
	executor := forwardRecoveryExecutorFunc(func(runContext context.Context, _ ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
		deadline, hasDeadline := runContext.Deadline()
		if !hasDeadline || time.Until(deadline) > DefaultForwardRecoveryPerReceiptTimeout {
			t.Fatalf("item context deadline = %v, %v", deadline, hasDeadline)
		}
		return ForwardRecoveryExecutionResult{
			Status:          ForwardRecoveryExecutionCommitted,
			DecisionDomain:  ForwardRecoveryDecisionArtifactReconciliation,
			Classification:  ArtifactClassificationManifestOnly,
			ReasonCode:      ArtifactReasonReferencedRawNotFound,
			Action:          ForwardRecoveryActionMarkHold,
			Outcome:         RecoveryAttemptOutcomeHold,
			ReceiptState:    ReceiptRecoveryHold,
			ReceiptRevision: 2,
		}, nil
	})
	worker := newWorkerHarness(t, store, leases, executor, []workerIDResult{{id: workerAttemptA}}, now, workerConfig(1, 1, 1), observer)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.Holds != 1 || len(observer.items) != 1 ||
		observer.items[0].Classification != ArtifactClassificationManifestOnly ||
		observer.items[0].ReasonCode != ArtifactReasonReferencedRawNotFound ||
		observer.items[0].Action != ForwardRecoveryActionMarkHold ||
		observer.items[0].Outcome != RecoveryAttemptOutcomeHold ||
		observer.items[0].ControlState != ReceiptRecoveryHold {
		t.Fatalf("Run() = %#v, %v; observations = %#v", result, err, observer.items)
	}
}

func TestForwardRecoveryWorkerBoundsPageClaimAndRunTimeouts(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	t.Run("page timeout", func(t *testing.T) {
		config := workerConfig(1, 1, 1)
		config.PageTimeout = 10 * time.Millisecond
		worker := newWorkerHarness(
			t,
			blockingWorkerCandidateStore{},
			&scriptedWorkerLeaseStore{},
			nil,
			nil,
			now,
			config,
			nil,
		)
		result, err := worker.Run(context.Background(), workerTenantID)
		if !errors.Is(err, ErrForwardRecoveryWorkerPage) || result.Status != ForwardRecoveryWorkerRunPageError {
			t.Fatalf("Run() = %#v, %v", result, err)
		}
	})
	t.Run("claim timeout is unknown and isolated", func(t *testing.T) {
		config := workerConfig(1, 1, 1)
		config.ClaimTimeout = 10 * time.Millisecond
		leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{{waitForContext: true}}}
		worker := newWorkerHarness(
			t,
			candidateStoreFor(now, 1),
			leases,
			nil,
			[]workerIDResult{{id: workerAttemptA}},
			now,
			config,
			nil,
		)
		result, err := worker.Run(context.Background(), workerTenantID)
		if err != nil || result.Status != ForwardRecoveryWorkerRunComplete || result.ClaimUnknown != 1 {
			t.Fatalf("Run() = %#v, %v", result, err)
		}
	})
	t.Run("parent deadline stops run", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		worker := newWorkerHarness(
			t,
			blockingWorkerCandidateStore{},
			&scriptedWorkerLeaseStore{},
			nil,
			nil,
			now,
			workerConfig(1, 1, 1),
			nil,
		)
		result, err := worker.Run(ctx, workerTenantID)
		if !errors.Is(err, context.DeadlineExceeded) || result.Status != ForwardRecoveryWorkerRunDeadline {
			t.Fatalf("Run() = %#v, %v", result, err)
		}
	})
	t.Run("worker run timeout stops page read", func(t *testing.T) {

		worker := newWorkerHarness(
			t,
			blockingWorkerCandidateStore{},
			&scriptedWorkerLeaseStore{},
			nil,
			nil,
			now,
			workerConfig(1, 1, 1),
			nil,
		)
		worker.config.RunTimeout = 10 * time.Millisecond
		result, err := worker.Run(context.Background(), workerTenantID)
		if !errors.Is(err, context.DeadlineExceeded) || result.Status != ForwardRecoveryWorkerRunDeadline {
			t.Fatalf("Run() = %#v, %v", result, err)
		}
	})
	t.Run("per-item timeout is isolated", func(t *testing.T) {
		store := candidateStoreFor(now, 2)
		leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{
			{status: LeaseStatusAcquired, validGrant: true},
			{status: LeaseStatusAcquired, validGrant: true},
		}}
		calls := 0
		executor := forwardRecoveryExecutorFunc(func(runContext context.Context, _ ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
			calls++
			if calls == 1 {
				<-runContext.Done()
				return ForwardRecoveryExecutionResult{}, runContext.Err()
			}
			return workerStoredExecution(ForwardRecoveryExecutionCommitted), nil
		})
		observer := &recordingWorkerObserver{}
		worker := newWorkerHarness(
			t,
			store,
			leases,
			executor,
			[]workerIDResult{{id: workerAttemptA}, {id: workerAttemptB}},
			now,
			workerConfig(2, 1, 2),
			observer,
		)
		worker.config.PerItemTimeout = 10 * time.Millisecond
		result, err := worker.Run(context.Background(), workerTenantID)
		if err != nil || result.ItemFailures != 1 || result.Committed != 1 || calls != 2 ||
			len(observer.items) != 2 || observer.items[0].Status != ForwardRecoveryWorkerItemDeadline ||
			observer.items[1].Status != ForwardRecoveryWorkerItemCommitted {
			t.Fatalf("Run() = %#v, %v; calls = %d; observations = %#v", result, err, calls, observer.items)
		}
	})
}

func TestForwardRecoveryWorkerStopsCheckpointWritesAfterCASConflict(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	first := workerCandidate(1, now.Add(-2*time.Minute), "a")
	second := workerCandidate(2, now.Add(-time.Minute), "b")
	firstCursor := cursorForForwardRecoveryCandidate(first)
	store := &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{
		{Candidates: []ForwardRecoveryCandidate{first}, NextCursor: &firstCursor},
		{Candidates: []ForwardRecoveryCandidate{second}, Exhausted: true},
	}}
	checkpoints := &memoryWorkerCheckpointStore{forceConflict: true}
	leases := &scriptedWorkerLeaseStore{responses: []workerClaimResponse{
		{status: LeaseStatusNotEligible}, {status: LeaseStatusNotEligible},
	}}
	worker := newWorkerHarnessWithCheckpoint(
		t,
		store,
		checkpoints,
		leases,
		nil,
		[]workerIDResult{{id: workerAttemptA}, {id: workerAttemptB}},
		now,
		workerConfig(1, 2, 2),
		nil,
	)

	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.Status != ForwardRecoveryWorkerRunComplete ||
		result.CheckpointConflicts != 1 || checkpoints.casCalls != 1 || result.Claims != 2 {
		t.Fatalf("Run() = %#v, %v; checkpoint CAS calls = %d", result, err, checkpoints.casCalls)
	}
}

func TestForwardRecoveryWorkerIgnoresObserverPanic(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	worker := newWorkerHarness(
		t,
		candidateStoreFor(now, 0),
		&scriptedWorkerLeaseStore{},
		nil,
		nil,
		now,
		workerConfig(1, 1, 1),
		panickingWorkerObserver{},
	)
	result, err := worker.Run(context.Background(), workerTenantID)
	if err != nil || result.Status != ForwardRecoveryWorkerRunComplete {
		t.Fatalf("Run() = %#v, %v", result, err)
	}
}

func TestForwardRecoveryWorkerConfigRejectsUnboundedValues(t *testing.T) {
	config := DefaultForwardRecoveryWorkerConfig()
	config.MaxItems = MaxForwardRecoveryWorkerItems + 1
	if _, err := normalizeForwardRecoveryWorkerConfig(config); !errors.Is(err, ErrInvalidForwardRecoveryWorker) {
		t.Fatalf("normalize config error = %v", err)
	}
	config = DefaultForwardRecoveryWorkerConfig()
	config.PerItemTimeout = DefaultForwardRecoveryPerReceiptTimeout - time.Nanosecond
	if _, err := normalizeForwardRecoveryWorkerConfig(config); !errors.Is(err, ErrInvalidForwardRecoveryWorker) {
		t.Fatalf("normalize config error = %v", err)
	}
}

func workerConfig(pageSize, maxPages, maxItems int) ForwardRecoveryWorkerConfig {
	config := DefaultForwardRecoveryWorkerConfig()
	config.PageSize = pageSize
	config.MaxPages = maxPages
	config.MaxItems = maxItems
	return config
}

func workerStoredExecution(status ForwardRecoveryExecutionStatus) ForwardRecoveryExecutionResult {
	return ForwardRecoveryExecutionResult{
		Status:          status,
		DecisionDomain:  ForwardRecoveryDecisionArtifactReconciliation,
		Classification:  ArtifactClassificationValidComplete,
		ReasonCode:      ArtifactReasonManifestAndReferencedRawValid,
		Action:          ForwardRecoveryActionMarkStored,
		Outcome:         RecoveryAttemptOutcomeStored,
		ReceiptState:    ReceiptStored,
		ReceiptRevision: 2,
	}
}

func workerCandidate(index int, dueAt time.Time, keyCharacter string) ForwardRecoveryCandidate {
	return ForwardRecoveryCandidate{
		TenantID:       workerTenantID,
		ReservationKey: strings.Repeat(keyCharacter, 64),
		DocumentID:     workerReceiptID(index),
		ReceiptID:      workerReceiptID(index),
		State:          ReceiptReserved,
		NextRecoveryAt: dueAt.UTC(),
	}
}

func workerReceiptID(index int) string {
	return fmt.Sprintf("22222222-2222-4222-8222-%012d", index)
}

func candidateStoreFor(now time.Time, count int) *scriptedWorkerCandidateStore {
	candidates := make([]ForwardRecoveryCandidate, 0, count)
	for index := 1; index <= count; index++ {
		candidates = append(candidates, workerCandidate(
			index,
			now.Add(time.Duration(index-count-1)*time.Minute),
			string(rune('a'+index-1)),
		))
	}
	return &scriptedWorkerCandidateStore{pages: []ForwardRecoveryCandidatePage{{
		Candidates: candidates,
		Exhausted:  true,
	}}}
}

type workerCandidateCall struct {
	cutoff time.Time
	after  *ForwardRecoveryCursor
	limit  int
}

type scriptedWorkerCandidateStore struct {
	pages []ForwardRecoveryCandidatePage
	errs  []error
	calls []workerCandidateCall
}

type blockingWorkerCandidateStore struct{}

func (blockingWorkerCandidateStore) ListDueForwardRecoveryCandidates(
	ctx context.Context,
	_ string,
	_ time.Time,
	_ *ForwardRecoveryCursor,
	_ int,
) (ForwardRecoveryCandidatePage, error) {
	<-ctx.Done()
	return ForwardRecoveryCandidatePage{}, ctx.Err()
}

type recordingWorkerPageRetry struct {
	failures []int
	err      error
}

func (retry *recordingWorkerPageRetry) Wait(_ context.Context, failure int) error {
	retry.failures = append(retry.failures, failure)
	return retry.err
}

func (store *scriptedWorkerCandidateStore) ListDueForwardRecoveryCandidates(
	_ context.Context,
	_ string,
	cutoff time.Time,
	after *ForwardRecoveryCursor,
	limit int,
) (ForwardRecoveryCandidatePage, error) {
	var copied *ForwardRecoveryCursor
	if after != nil {
		value := *after
		copied = &value
	}
	store.calls = append(store.calls, workerCandidateCall{cutoff: cutoff, after: copied, limit: limit})
	index := len(store.calls) - 1
	if index < len(store.errs) && store.errs[index] != nil {
		return ForwardRecoveryCandidatePage{}, store.errs[index]
	}
	if index >= len(store.pages) {
		return ForwardRecoveryCandidatePage{}, errors.New("unexpected candidate page call")
	}
	return store.pages[index], nil
}

type workerClaimResponse struct {
	status         LeaseStatus
	err            error
	validGrant     bool
	hook           func()
	waitForContext bool
}

type scriptedWorkerLeaseStore struct {
	mu        sync.Mutex
	responses []workerClaimResponse
	calls     int
}

func (store *scriptedWorkerLeaseStore) ClaimRecoveryLease(
	ctx context.Context,
	_ string,
	_ string,
	owner LeaseOwner,
	_ RecoveryAttemptProposal,
	requestedAt time.Time,
	duration time.Duration,
) (LeaseGrant, LeaseStatus, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.calls >= len(store.responses) {
		return LeaseGrant{}, "", errors.New("unexpected claim")
	}
	response := store.responses[store.calls]
	store.calls++
	if response.waitForContext {
		store.mu.Unlock()
		<-ctx.Done()
		store.mu.Lock()
		return LeaseGrant{}, "", ctx.Err()
	}
	var grant LeaseGrant
	if response.validGrant {
		grant = LeaseGrant{
			Fence: LeaseFence{
				OwnerID:   owner.ID,
				Token:     1,
				ExpiresAt: requestedAt.Add(duration),
			},
			OwnerKind:   owner.Kind,
			AcquiredAt:  requestedAt,
			HeartbeatAt: requestedAt,
		}
	}
	if response.hook != nil {
		response.hook()
	}
	return grant, response.status, response.err
}

func (*scriptedWorkerLeaseStore) RenewLease(
	context.Context,
	string,
	string,
	LeaseFence,
	time.Time,
	time.Duration,
) (LeaseGrant, error) {
	return LeaseGrant{}, errors.New("worker must not renew directly")
}

type workerIDResult struct {
	id  string
	err error
}

type sequenceWorkerIDGenerator struct {
	results []workerIDResult
	index   int
}

func (generator *sequenceWorkerIDGenerator) NewID() (string, error) {
	if generator.index >= len(generator.results) {
		return "", errors.New("unexpected id request")
	}
	result := generator.results[generator.index]
	generator.index++
	return result.id, result.err
}

type forwardRecoveryExecutorFunc func(context.Context, ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error)

func (function forwardRecoveryExecutorFunc) Reconcile(
	ctx context.Context,
	task ForwardRecoveryTask,
) (ForwardRecoveryExecutionResult, error) {
	return function(ctx, task)
}

func newWorkerHarness(
	t *testing.T,
	candidates ForwardRecoveryCandidateStore,
	leases RecoveryLeaseStore,
	executor ForwardRecoveryExecutor,
	ids []workerIDResult,
	now time.Time,
	config ForwardRecoveryWorkerConfig,
	observer ForwardRecoveryWorkerObserver,
) *ForwardRecoveryWorker {
	return newWorkerHarnessWithCheckpoint(
		t,
		candidates,
		&memoryWorkerCheckpointStore{},
		leases,
		executor,
		ids,
		now,
		config,
		observer,
	)
}

func newWorkerHarnessWithCheckpoint(
	t *testing.T,
	candidates ForwardRecoveryCandidateStore,
	checkpoints ForwardRecoveryCheckpointStore,
	leases RecoveryLeaseStore,
	executor ForwardRecoveryExecutor,
	ids []workerIDResult,
	now time.Time,
	config ForwardRecoveryWorkerConfig,
	observer ForwardRecoveryWorkerObserver,
) *ForwardRecoveryWorker {
	t.Helper()
	if executor == nil {
		executor = forwardRecoveryExecutorFunc(func(context.Context, ForwardRecoveryTask) (ForwardRecoveryExecutionResult, error) {
			return ForwardRecoveryExecutionResult{}, errors.New("unexpected execution")
		})
	}
	worker, err := NewForwardRecoveryWorker(
		candidates,
		checkpoints,
		leases,
		executor,
		&sequenceWorkerIDGenerator{results: ids},
		observer,
		func() time.Time { return now },
		config,
	)
	if err != nil {
		t.Fatalf("NewForwardRecoveryWorker() error = %v", err)
	}
	return worker
}

type memoryWorkerCheckpointStore struct {
	mu            sync.Mutex
	checkpoint    ForwardRecoveryCheckpoint
	loadErr       error
	persistErr    error
	forceConflict bool
	casCalls      int
	saves         []*ForwardRecoveryCursor
}

func (store *memoryWorkerCheckpointStore) LoadForwardRecoveryCheckpoint(
	context.Context,
	string,
) (ForwardRecoveryCheckpoint, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	checkpoint := store.checkpoint
	if checkpoint.Cursor != nil {
		checkpoint.Cursor = cloneForwardRecoveryCursor(*checkpoint.Cursor)
	}
	return checkpoint, store.loadErr
}

func (store *memoryWorkerCheckpointStore) CompareAndSetForwardRecoveryCheckpoint(
	_ context.Context,
	_ string,
	expectedRevision int64,
	next *ForwardRecoveryCursor,
	scanCutoff time.Time,
	_ time.Time,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.casCalls++
	if store.persistErr != nil {
		return false, store.persistErr
	}
	if store.forceConflict {
		return false, nil
	}
	if store.checkpoint.Revision != expectedRevision {
		return false, nil
	}
	store.checkpoint.Revision++
	if next == nil {
		store.checkpoint.Cursor = nil
		store.checkpoint.ScanCutoff = time.Time{}
		store.saves = append(store.saves, nil)
		return true, nil
	}
	store.checkpoint.Cursor = cloneForwardRecoveryCursor(*next)
	store.checkpoint.ScanCutoff = scanCutoff.UTC()
	store.saves = append(store.saves, cloneForwardRecoveryCursor(*next))
	return true, nil
}

type recordingWorkerObserver struct {
	runs        []ForwardRecoveryWorkerRunStatus
	checkpoints []ForwardRecoveryWorkerCheckpointStatus
	pages       []ForwardRecoveryWorkerPageStatus
	candidates  []ForwardRecoveryWorkerCandidateStatus
	claims      []ForwardRecoveryWorkerClaimStatus
	items       []ForwardRecoveryWorkerItemObservation
}

func (observer *recordingWorkerObserver) ObserveForwardRecoveryRun(status ForwardRecoveryWorkerRunStatus, _ time.Duration) {
	observer.runs = append(observer.runs, status)
}

func (observer *recordingWorkerObserver) ObserveForwardRecoveryCheckpoint(status ForwardRecoveryWorkerCheckpointStatus) {
	observer.checkpoints = append(observer.checkpoints, status)
}

func (observer *recordingWorkerObserver) ObserveForwardRecoveryPage(status ForwardRecoveryWorkerPageStatus, _ int, _ time.Duration) {
	observer.pages = append(observer.pages, status)
}

func (observer *recordingWorkerObserver) ObserveForwardRecoveryCandidate(status ForwardRecoveryWorkerCandidateStatus) {
	observer.candidates = append(observer.candidates, status)
}

func (observer *recordingWorkerObserver) ObserveForwardRecoveryClaim(status ForwardRecoveryWorkerClaimStatus, _ time.Duration) {
	observer.claims = append(observer.claims, status)
}

func (observer *recordingWorkerObserver) ObserveForwardRecoveryItem(observation ForwardRecoveryWorkerItemObservation, _ time.Duration) {
	observer.items = append(observer.items, observation)
}

type panickingWorkerObserver struct{}

func (panickingWorkerObserver) ObserveForwardRecoveryRun(ForwardRecoveryWorkerRunStatus, time.Duration) {
	panic("observer")
}
func (panickingWorkerObserver) ObserveForwardRecoveryCheckpoint(ForwardRecoveryWorkerCheckpointStatus) {
	panic("observer")
}
func (panickingWorkerObserver) ObserveForwardRecoveryPage(ForwardRecoveryWorkerPageStatus, int, time.Duration) {
	panic("observer")
}
func (panickingWorkerObserver) ObserveForwardRecoveryCandidate(ForwardRecoveryWorkerCandidateStatus) {
	panic("observer")
}
func (panickingWorkerObserver) ObserveForwardRecoveryClaim(ForwardRecoveryWorkerClaimStatus, time.Duration) {
	panic("observer")
}
func (panickingWorkerObserver) ObserveForwardRecoveryItem(ForwardRecoveryWorkerItemObservation, time.Duration) {
	panic("observer")
}

func assertWorkerPublicResultPrivacy(t *testing.T, resultType reflect.Type) {
	t.Helper()
	for index := 0; index < resultType.NumField(); index++ {
		name := strings.ToLower(resultType.Field(index).Name)
		for _, forbidden := range []string{
			"tenant", "receipt", "reservation", "attempt", "device", "trip",
			"installation", "uid", "appid", "path", "cursor", "artifact",
			"coordinate", "latitude", "longitude", "error",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("public result field %q contains forbidden identifier/error material", name)
			}
		}
	}
}
