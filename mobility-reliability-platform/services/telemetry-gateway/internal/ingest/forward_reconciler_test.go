package ingest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestForwardRecoveryReconcilerCompletesTwoPassWithoutManifestWrite(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	raw := reconcilerPinned("b", 128, 91, 2)
	manifest := reconcilerPinned("c", 96, 92, 3)
	h.classifier.steps = []reconcilerClassifierStep{
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: h.now},
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: h.now.Add(time.Second)},
	}

	result, err := h.reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if result.Status != ForwardRecoveryExecutionCommitted ||
		result.DecisionDomain != ForwardRecoveryDecisionArtifactReconciliation ||
		result.Action != ForwardRecoveryActionMarkStored ||
		result.Outcome != RecoveryAttemptOutcomeStored || result.ClassificationPasses != 2 ||
		result.ManifestWrites != 0 || h.control.actionCalls != 1 || h.manifests.calls != 0 {
		t.Fatalf("result/side effects = %#v, actions=%d manifests=%d", result, h.control.actionCalls, h.manifests.calls)
	}
	if h.authorizer.actionInputs[0].Phase != RecoveryPhaseConfirmation ||
		h.authorizer.actionInputs[0].PriorResult == nil {
		t.Fatalf("terminal input = %#v", h.authorizer.actionInputs[0])
	}
	h.classifier.assertDone(t)
}

func TestForwardRecoveryReconcilerRepairsRawOnlyThenPostConfirmsStored(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	raw := reconcilerPinned("b", 128, 91, 2)
	write := reconcilerManifestWrite(t, h.request, raw)
	written := StoredArtifact{
		Path: write.ManifestPath, SHA256: write.Digest.SHA256, CRC32C: write.Digest.CRC32C,
		Size: write.Digest.Size, Generation: 92, Metageneration: 3,
	}
	manifest := artifactPinnedLineageFromStored(written)
	h.builder.write = write
	h.builder.reason = ""
	h.manifests.stored = written
	h.classifier.steps = []reconcilerClassifierStep{
		{classification: ArtifactClassificationValidRawOnly, reason: ArtifactReasonRawValidManifestAbsent, raw: &raw, observedAt: h.now},
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: h.now.Add(time.Second)},
	}

	result, err := h.reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if result.Status != ForwardRecoveryExecutionCommitted || result.Action != ForwardRecoveryActionMarkStored ||
		result.ClassificationPasses != 2 || result.ManifestWrites != 1 ||
		h.builder.calls != 1 || h.manifests.calls != 1 || h.control.actionCalls != 1 ||
		h.authorizer.manifestAuthorizationCalls != 1 {
		t.Fatalf("result/side effects = %#v builder=%d writer=%d action=%d auth=%d", result, h.builder.calls, h.manifests.calls, h.control.actionCalls, h.authorizer.manifestAuthorizationCalls)
	}
	input := h.authorizer.actionInputs[0]
	if input.Phase != RecoveryPhasePostManifestConfirmation || input.WrittenManifest == nil ||
		*input.WrittenManifest != written {
		t.Fatalf("post-manifest input = %#v", input)
	}
	h.classifier.assertDone(t)
}

func TestForwardRecoveryReconcilerUsesAuthorizationDispositionWithoutArtifactIO(t *testing.T) {
	tests := []struct {
		name         string
		authorizeErr error
		disposition  ForwardRecoveryAuthorizationDisposition
		wantAction   ForwardRecoveryAction
		wantOutcome  RecoveryAttemptOutcome
	}{
		{name: "denied", authorizeErr: ErrForwardRecoveryUnauthorized, disposition: ForwardRecoveryAuthorizationDenied, wantAction: ForwardRecoveryActionMarkHold, wantOutcome: RecoveryAttemptOutcomeHold},
		{name: "unavailable", authorizeErr: ErrForwardRecoveryAuthorizationUnavailable, disposition: ForwardRecoveryAuthorizationUnavailable, wantAction: ForwardRecoveryActionReleaseLease, wantOutcome: RecoveryAttemptOutcomeLeaseReleased},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newForwardReconcilerHarness(t)
			h.authorizer.authorizeErr = test.authorizeErr
			h.authorizer.disposition = test.disposition

			result, err := h.reconcile(context.Background())
			if err != nil {
				t.Fatalf("Reconcile() = %v", err)
			}
			if result.DecisionDomain != ForwardRecoveryDecisionCurrentAuthorization ||
				result.AuthorizationDisposition != test.disposition || result.Action != test.wantAction ||
				result.Outcome != test.wantOutcome || h.classifier.calls != 0 || h.manifests.calls != 0 ||
				h.control.actionCalls != 0 || h.control.dispositionCalls != 1 {
				t.Fatalf("result/side effects = %#v classifier=%d writer=%d action=%d disposition=%d", result, h.classifier.calls, h.manifests.calls, h.control.actionCalls, h.control.dispositionCalls)
			}
		})
	}
}

func TestForwardRecoveryReconcilerDiscardsEvidenceAfterRenewalAndRestartsInitial(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	h.config.RenewalBefore = 30 * time.Second
	h.config.RenewalDuration = 2 * time.Minute
	nearExpiry := h.now.Add(105 * time.Second)
	h.clock = newReconcilerScriptedClock(
		h.now, h.now, nearExpiry, nearExpiry, nearExpiry, nearExpiry, nearExpiry, nearExpiry,
	)
	renewed := h.task.Lease
	renewed.HeartbeatAt = nearExpiry
	renewed.Fence.ExpiresAt = nearExpiry.Add(h.config.RenewalDuration)
	h.control.renewed = renewed
	raw := reconcilerPinned("b", 128, 91, 2)
	manifest := reconcilerPinned("c", 96, 92, 3)
	h.classifier.steps = []reconcilerClassifierStep{
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: h.now},
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: nearExpiry.Add(time.Second)},
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: nearExpiry.Add(2 * time.Second)},
	}

	result, err := h.reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if result.Renewals != 1 || result.ClassificationPasses != 3 || h.control.renewCalls != 1 ||
		h.control.actionCalls != 1 || len(h.authorizer.authorizeLeases) != 3 ||
		sameLeaseFence(h.authorizer.authorizeLeases[0].Fence, renewed.Fence) ||
		!sameLeaseFence(h.authorizer.authorizeLeases[1].Fence, renewed.Fence) ||
		!sameLeaseFence(h.authorizer.authorizeLeases[2].Fence, renewed.Fence) {
		t.Fatalf("renewal result = %#v renew=%d leases=%#v", result, h.control.renewCalls, h.authorizer.authorizeLeases)
	}
	input := h.authorizer.actionInputs[0]
	if input.PriorResult == nil || !input.PriorResult.ObservedAt.Equal(nearExpiry.Add(time.Second)) ||
		input.PriorResult.ObservedAt.Equal(h.now) {
		t.Fatalf("stale initial evidence reached action = %#v", input.PriorResult)
	}
	h.classifier.assertDone(t)
}

func TestForwardRecoveryReconcilerCorrelatesCommittedOutcomeAfterResponseLoss(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	raw := reconcilerPinned("b", 128, 91, 2)
	manifest := reconcilerPinned("c", 96, 92, 3)
	h.classifier.steps = []reconcilerClassifierStep{
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: h.now},
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: h.now.Add(time.Second)},
	}
	h.control.actionErr = ErrAdmissionUnavailable
	h.control.outcome = RecoveryActionOutcome{
		CommitStatus: RecoveryActionCommitted, Outcome: RecoveryAttemptOutcomeStored,
		CompletedAt: h.now.Add(2 * time.Second),
	}

	result, err := h.reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if result.Status != ForwardRecoveryExecutionCorrelated || result.Outcome != RecoveryAttemptOutcomeStored ||
		h.control.actionCalls != 1 || h.control.outcomeCalls != 1 || h.outcomeAuthorizer.calls != 1 ||
		h.control.failureCalls != 0 {
		t.Fatalf("correlation result = %#v action=%d outcome=%d auth=%d failure=%d", result, h.control.actionCalls, h.control.outcomeCalls, h.outcomeAuthorizer.calls, h.control.failureCalls)
	}
	if h.control.lastOutcomeQuery.ExpectedActionHash == "" ||
		h.control.lastOutcomeQuery.ExpectedDecisionDomain != ForwardRecoveryDecisionArtifactReconciliation {
		t.Fatalf("outcome query = %#v", h.control.lastOutcomeQuery)
	}
}

func TestForwardRecoveryReconcilerPreservesCallerCancellation(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.classifier.steps = []reconcilerClassifierStep{{
		before: func() { cancel() }, err: context.Canceled,
	}}

	_, err := h.reconcile(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile() = %v, want context.Canceled", err)
	}
	if h.manifests.calls != 0 || h.control.actionCalls != 0 || h.control.dispositionCalls != 0 ||
		h.control.failureCalls != 1 || h.control.outcomeCalls != 0 ||
		len(h.authorizer.failureCodes) != 1 ||
		h.authorizer.failureCodes[0] != RecoveryAttemptFailureCallerCanceled {
		t.Fatalf("cancellation side effects writer=%d action=%d disposition=%d failure=%d outcome=%d", h.manifests.calls, h.control.actionCalls, h.control.dispositionCalls, h.control.failureCalls, h.control.outcomeCalls)
	}
}

func TestForwardRecoveryReconcilerCorrelatesCommittedOutcomeAfterCommitCancelsParent(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	h.scriptCompleteTwoPass()
	ctx, cancel := context.WithCancel(context.Background())
	h.control.actionHook = cancel
	h.control.actionErr = ErrAdmissionUnavailable
	h.control.outcomes = []RecoveryActionOutcome{{
		CommitStatus: RecoveryActionCommitted, Outcome: RecoveryAttemptOutcomeStored,
		ReceiptState: ReceiptStored, CompletedAt: h.now.Add(2 * time.Second),
	}}

	result, err := h.reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if ctx.Err() != context.Canceled || result.Status != ForwardRecoveryExecutionCorrelated ||
		result.Outcome != RecoveryAttemptOutcomeStored || h.control.actionCalls != 1 ||
		h.control.outcomeCalls != 1 || h.control.failureCalls != 0 {
		t.Fatalf("post-commit cancellation result = %#v ctx=%v action=%d outcome=%d failure=%d", result, ctx.Err(), h.control.actionCalls, h.control.outcomeCalls, h.control.failureCalls)
	}
}

func TestForwardRecoveryReconcilerRechecksExactOldOutcomeWhenFailureBarrierDenied(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	h.scriptCompleteTwoPass()
	h.control.actionErr = ErrAdmissionUnavailable
	h.control.outcomes = []RecoveryActionOutcome{
		{CommitStatus: RecoveryActionNotCommitted},
		{CommitStatus: RecoveryActionCommitted, Outcome: RecoveryAttemptOutcomeStored, ReceiptState: ReceiptStored, CompletedAt: h.now.Add(2 * time.Second)},
	}
	h.authorizer.failureErrs = []error{ErrForwardRecoveryUnauthorized}

	result, err := h.reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if result.Status != ForwardRecoveryExecutionCorrelated || h.control.actionCalls != 1 ||
		h.control.outcomeCalls != 2 || h.control.failureCalls != 0 ||
		h.authorizer.failureAuthorizationCalls != 1 || len(h.control.outcomeQueries) != 2 ||
		!reflect.DeepEqual(h.control.outcomeQueries[0], h.control.outcomeQueries[1]) {
		t.Fatalf("late correlation result = %#v action=%d outcomes=%#v failure=%d/%d", result, h.control.actionCalls, h.control.outcomeQueries, h.authorizer.failureAuthorizationCalls, h.control.failureCalls)
	}
}

func TestForwardRecoveryReconcilerStopsAfterSuccessfulNotCommittedBarrier(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	h.scriptCompleteTwoPass()
	h.control.actionErr = ErrAdmissionUnavailable
	h.control.outcomes = []RecoveryActionOutcome{{CommitStatus: RecoveryActionNotCommitted}}

	_, err := h.reconcile(context.Background())
	if !errors.Is(err, ErrForwardRecoveryNotCommitted) {
		t.Fatalf("Reconcile() = %v, want ErrForwardRecoveryNotCommitted", err)
	}
	if h.control.actionCalls != 1 || h.control.outcomeCalls != 1 || h.control.failureCalls != 1 ||
		h.authorizer.failureAuthorizationCalls != 1 {
		t.Fatalf("barrier calls action=%d outcome=%d failure=%d auth=%d", h.control.actionCalls, h.control.outcomeCalls, h.control.failureCalls, h.authorizer.failureAuthorizationCalls)
	}
}

func TestForwardRecoveryReconcilerJoinsCancellationWithUnverifiableDispositionCommit(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.classifier.steps = []reconcilerClassifierStep{{before: cancel, err: context.Canceled}}
	h.authorizer.failureErrs = []error{ErrForwardRecoveryUnauthorized}
	h.authorizer.disposition = ForwardRecoveryAuthorizationDenied
	h.control.dispositionErr = ErrAdmissionUnavailable
	h.control.outcomes = []RecoveryActionOutcome{{CommitStatus: RecoveryActionUnverifiable}}

	_, err := h.reconcile(ctx)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrForwardRecoveryCommitUnverified) {
		t.Fatalf("Reconcile() = %v, want cancellation and unverified commit", err)
	}
	if h.control.actionCalls != 0 || h.control.dispositionCalls != 1 ||
		h.control.outcomeCalls != 1 || h.control.failureCalls != 0 ||
		h.authorizer.failureAuthorizationCalls != 1 {
		t.Fatalf("cancellation finalizer calls action=%d disposition=%d outcome=%d failure=%d auth=%d", h.control.actionCalls, h.control.dispositionCalls, h.control.outcomeCalls, h.control.failureCalls, h.authorizer.failureAuthorizationCalls)
	}
}

func TestForwardRecoveryReconcilerJoinsRenewalUncertaintyWithCallerCancellation(t *testing.T) {
	h := newForwardReconcilerHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.task.Lease.Fence.ExpiresAt = h.now.Add(10 * time.Second)
	h.authorizer.baseRequest.ForwardFence = &h.task.Lease.Fence
	h.authorizer.baseFence = h.task.Lease.Fence
	h.control.renewHook = cancel
	h.control.renewErr = errors.New("provider details")

	_, err := h.reconcile(ctx)
	if !errors.Is(err, ErrForwardRecoveryLeaseUnknown) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile() = %v, want lease unknown and cancellation", err)
	}
	if h.control.renewCalls != 1 || h.classifier.calls != 0 || h.control.actionCalls != 0 {
		t.Fatalf("renewal calls renew=%d classifier=%d action=%d", h.control.renewCalls, h.classifier.calls, h.control.actionCalls)
	}
}

func TestForwardRecoveryExecutionResultHasNoPrivateDataSurface(t *testing.T) {
	forbidden := []string{"path", "body", "firebase", "uid", "appid", "coordinate", "latitude", "longitude"}
	visited := map[reflect.Type]bool{}
	var inspect func(reflect.Type, string)
	inspect = func(current reflect.Type, prefix string) {
		for current.Kind() == reflect.Pointer || current.Kind() == reflect.Slice || current.Kind() == reflect.Array {
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct || visited[current] || current.PkgPath() == "time" {
			return
		}
		visited[current] = true
		for index := 0; index < current.NumField(); index++ {
			field := current.Field(index)
			if !field.IsExported() {
				continue
			}
			name := strings.ToLower(field.Name)
			for _, token := range forbidden {
				if strings.Contains(name, token) {
					t.Fatalf("private surface %s%s contains %q", prefix, field.Name, token)
				}
			}
			inspect(field.Type, prefix+field.Name+".")
		}
	}
	inspect(reflect.TypeOf(ForwardRecoveryExecutionResult{}), "ForwardRecoveryExecutionResult.")
}

type forwardReconcilerHarness struct {
	now               time.Time
	request           ArtifactClassificationRequest
	task              ForwardRecoveryTask
	config            ForwardRecoveryConfig
	clock             func() time.Time
	classifier        *reconcilerScriptedClassifier
	authorizer        *reconcilerFakeAuthorizer
	outcomeAuthorizer *reconcilerFakeOutcomeAuthorizer
	control           *reconcilerFakeControl
	manifests         *reconcilerFakeManifestStore
	builder           *reconcilerFakeManifestBuilder
}

func newForwardReconcilerHarness(t *testing.T) *forwardReconcilerHarness {
	t.Helper()
	now := time.Now().UTC().Add(10 * time.Second).Truncate(time.Millisecond)
	snapshot := artifactAuthSnapshot(now)
	request, err := forwardRecoveryClassificationRequest(snapshot.Receipt)
	if err != nil {
		t.Fatalf("forwardRecoveryClassificationRequest() = %v", err)
	}
	lease := artifactAuthLease(snapshot)
	task := ForwardRecoveryTask{
		TenantID: snapshot.Receipt.TenantID, ReservationKey: snapshot.Receipt.ReservationKey,
		Lease: lease, Attempt: RecoveryAttemptProposal{ID: lease.Fence.OwnerID, WorkerVersion: RecoveryWorkerVersion},
	}
	config := DefaultForwardRecoveryConfig()
	config.PerReceiptTimeout = 30 * time.Second
	config.RenewalBefore = 30 * time.Second
	config.RenewalDuration = 2 * time.Minute
	return &forwardReconcilerHarness{
		now: now, request: request, task: task, config: config,
		clock:             func() time.Time { return now },
		classifier:        &reconcilerScriptedClassifier{},
		authorizer:        &reconcilerFakeAuthorizer{baseRequest: request, baseFence: lease.Fence},
		outcomeAuthorizer: &reconcilerFakeOutcomeAuthorizer{},
		control:           &reconcilerFakeControl{receipt: snapshot.Receipt},
		manifests:         &reconcilerFakeManifestStore{},
		builder:           &reconcilerFakeManifestBuilder{reason: ArtifactReasonResponseUnverifiable},
	}
}

func (h *forwardReconcilerHarness) reconcile(ctx context.Context) (ForwardRecoveryExecutionResult, error) {
	h.authorizer.clock = h.clock
	reconciler, err := newForwardRecoveryReconciler(
		h.control, h.manifests, h.classifier, h.builder, h.authorizer,
		h.outcomeAuthorizer, h.clock, h.config,
	)
	if err != nil {
		return ForwardRecoveryExecutionResult{}, err
	}
	return reconciler.Reconcile(ctx, h.task)
}

func (h *forwardReconcilerHarness) scriptCompleteTwoPass() {
	raw := reconcilerPinned("b", 128, 91, 2)
	manifest := reconcilerPinned("c", 96, 92, 3)
	h.classifier.steps = []reconcilerClassifierStep{
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: h.now},
		{classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, raw: &raw, manifest: &manifest, observedAt: h.now.Add(time.Second)},
	}
}

type reconcilerClassifierStep struct {
	classification ArtifactClassification
	reason         ArtifactReasonCode
	raw            *ArtifactPinnedLineage
	manifest       *ArtifactPinnedLineage
	observedAt     time.Time
	before         func()
	err            error
}

type reconcilerScriptedClassifier struct {
	steps []reconcilerClassifierStep
	calls int
}

func (c *reconcilerScriptedClassifier) Classify(
	_ context.Context,
	_ ArtifactReadAuthorizationGrant,
	request ArtifactClassificationRequest,
) (ArtifactClassificationResult, error) {
	if c.calls >= len(c.steps) {
		return ArtifactClassificationResult{}, errors.New("unexpected classifier call")
	}
	step := c.steps[c.calls]
	c.calls++
	if step.before != nil {
		step.before()
	}
	if step.err != nil {
		return ArtifactClassificationResult{}, step.err
	}
	return reconcilerClassificationResult(request, step), nil
}

func (c *reconcilerScriptedClassifier) assertDone(t *testing.T) {
	t.Helper()
	if c.calls != len(c.steps) {
		t.Fatalf("classifier calls = %d, want %d", c.calls, len(c.steps))
	}
}

func reconcilerClassificationResult(
	request ArtifactClassificationRequest,
	step reconcilerClassifierStep,
) ArtifactClassificationResult {
	result := ArtifactClassificationResult{
		Classification: step.classification, ReasonCode: step.reason,
		RetentionPhase:    ArtifactRetentionBeforeExpiry,
		ManifestInventory: ArtifactInventorySummary{Coverage: ArtifactInventoryCoverageComplete, Performed: true},
		RawInventory:      ArtifactInventorySummary{Coverage: ArtifactInventoryCoverageComplete, Performed: true},
		ValidatorVersion:  request.ValidatorVersion, ObservedAt: step.observedAt,
		requestBindingHash: canonicalArtifactClassificationRequestBinding(request),
	}
	if step.raw != nil {
		value := *step.raw
		result.PinnedRaw = &value
		result.RawInventory.NonSoftDeletedCount = 1
	}
	if step.manifest != nil {
		value := *step.manifest
		result.PinnedManifest = &value
		result.ManifestInventory.NonSoftDeletedCount = 1
	}
	return result
}

type reconcilerFakeAuthorizer struct {
	baseRequest                   ArtifactClassificationRequest
	baseFence                     LeaseFence
	clock                         func() time.Time
	authorizeErr                  error
	disposition                   ForwardRecoveryAuthorizationDisposition
	authorizeLeases               []LeaseGrant
	actionInputs                  []ForwardRecoveryPlanInput
	actionLeases                  []LeaseGrant
	manifestAuthorizationCalls    int
	dispositionAuthorizationCalls int
	failureAuthorizationCalls     int
	failureCodes                  []RecoveryAttemptFailureCode
	failureErrs                   []error
}

func (a *reconcilerFakeAuthorizer) requestForLease(lease LeaseGrant) ArtifactClassificationRequest {
	request := cloneArtifactClassificationRequest(a.baseRequest)
	fence := lease.Fence
	request.ForwardFence = &fence
	if !sameLeaseFence(a.baseFence, lease.Fence) {
		request.ReceiptRevision++
	}
	return request
}

func (a *reconcilerFakeAuthorizer) Authorize(
	_ context.Context, _ string, _ string, lease LeaseGrant,
) (ArtifactClassificationRequest, ArtifactReadAuthorizationGrant, error) {
	a.authorizeLeases = append(a.authorizeLeases, lease)
	if a.authorizeErr != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, a.authorizeErr
	}
	return a.requestForLease(lease), ArtifactReadAuthorizationGrant{}, nil
}

func (a *reconcilerFakeAuthorizer) AuthorizeManifestRepair(
	_ context.Context, _ string, _ string, lease LeaseGrant,
	_ ForwardRecoveryManifestEvidence, _ RecoveryManifestWrite,
) (ArtifactClassificationRequest, ManifestRepairAuthorizationGrant, error) {
	a.manifestAuthorizationCalls++
	return a.requestForLease(lease), ManifestRepairAuthorizationGrant{}, nil
}

func (a *reconcilerFakeAuthorizer) AuthorizeForwardRecoveryAction(
	_ context.Context, tenantID string, reservationKey string, lease LeaseGrant,
	attempt RecoveryAttemptProposal, input ForwardRecoveryPlanInput,
) (ForwardRecoveryActionCommand, ForwardRecoveryActionGrant, error) {
	plan, err := PlanForwardRecoveryAction(input)
	if err != nil {
		return ForwardRecoveryActionCommand{}, ForwardRecoveryActionGrant{}, err
	}
	a.actionInputs = append(a.actionInputs, cloneForwardRecoveryPlanInput(input))
	a.actionLeases = append(a.actionLeases, lease)
	command := ForwardRecoveryActionCommand{
		TenantID: tenantID, ReservationKey: reservationKey, Attempt: attempt,
		ReceiptRevision: input.Request.ReceiptRevision, Fence: lease.Fence, Plan: plan,
	}
	if plan.Action == ForwardRecoveryActionMarkHold {
		command.HoldReviewDueAt = a.clock().Add(time.Hour)
	}
	return command, ForwardRecoveryActionGrant{}, nil
}

func (a *reconcilerFakeAuthorizer) AuthorizeForwardRecoveryDisposition(
	_ context.Context, tenantID string, reservationKey string, lease LeaseGrant,
	attempt RecoveryAttemptProposal,
) (ForwardRecoveryDispositionCommand, ForwardRecoveryDispositionGrant, error) {
	a.dispositionAuthorizationCalls++
	command := ForwardRecoveryDispositionCommand{
		TenantID: tenantID, ReservationKey: reservationKey, Attempt: attempt,
		ReceiptRevision: a.requestForLease(lease).ReceiptRevision, Fence: lease.Fence,
		Disposition: a.disposition,
	}
	if a.disposition == ForwardRecoveryAuthorizationDenied {
		command.HoldReviewDueAt = a.clock().Add(time.Hour)
	}
	return command, ForwardRecoveryDispositionGrant{}, nil
}

func (a *reconcilerFakeAuthorizer) AuthorizeForwardRecoveryAttemptFailure(
	_ context.Context, tenantID string, reservationKey string, lease LeaseGrant,
	attempt RecoveryAttemptProposal, code RecoveryAttemptFailureCode,
) (ForwardRecoveryAttemptFailure, ForwardRecoveryAttemptGrant, error) {
	a.failureAuthorizationCalls++
	a.failureCodes = append(a.failureCodes, code)
	if len(a.failureErrs) >= a.failureAuthorizationCalls {
		if err := a.failureErrs[a.failureAuthorizationCalls-1]; err != nil {
			return ForwardRecoveryAttemptFailure{}, ForwardRecoveryAttemptGrant{}, err
		}
	}
	return ForwardRecoveryAttemptFailure{
		TenantID: tenantID, ReservationKey: reservationKey, Attempt: attempt,
		ReceiptRevision: a.requestForLease(lease).ReceiptRevision, Fence: lease.Fence, FailureCode: code,
	}, ForwardRecoveryAttemptGrant{}, nil
}

type reconcilerFakeOutcomeAuthorizer struct{ calls int }

func (a *reconcilerFakeOutcomeAuthorizer) Authorize(
	_ context.Context, _ ForwardRecoveryOutcomeQuery,
) (ForwardRecoveryOutcomeReadGrant, error) {
	a.calls++
	return ForwardRecoveryOutcomeReadGrant{}, nil
}

type reconcilerFakeManifestBuilder struct {
	write  RecoveryManifestWrite
	reason ArtifactReasonCode
	calls  int
}

func (b *reconcilerFakeManifestBuilder) buildRecoveryManifest(
	_ ArtifactClassificationRequest, _ ArtifactPinnedLineage,
) (RecoveryManifestWrite, ArtifactReasonCode) {
	b.calls++
	return cloneManifestRepairWrite(b.write), b.reason
}

type reconcilerFakeManifestStore struct {
	stored StoredArtifact
	err    error
	calls  int
	writes []RecoveryManifestWrite
}

func (s *reconcilerFakeManifestStore) CreateManifest(
	_ context.Context, _ ManifestRepairAuthorizationGrant, write RecoveryManifestWrite,
) (StoredArtifact, error) {
	s.calls++
	s.writes = append(s.writes, cloneManifestRepairWrite(write))
	return s.stored, s.err
}

type reconcilerFakeControl struct {
	receipt          Receipt
	renewed          LeaseGrant
	renewErr         error
	renewHook        func()
	renewCalls       int
	actionErr        error
	actionHook       func()
	actionCalls      int
	actionCommands   []ForwardRecoveryActionCommand
	dispositionErr   error
	dispositionCalls int
	failureCalls     int
	outcome          RecoveryActionOutcome
	outcomeErr       error
	outcomeCalls     int
	lastOutcomeQuery ForwardRecoveryOutcomeQuery
	outcomes         []RecoveryActionOutcome
	outcomeQueries   []ForwardRecoveryOutcomeQuery
}

func (s *reconcilerFakeControl) ClaimRecoveryLease(
	context.Context, string, string, LeaseOwner, RecoveryAttemptProposal, time.Time, time.Duration,
) (LeaseGrant, LeaseStatus, error) {
	return LeaseGrant{}, "", ErrAdmissionUnavailable
}

func (s *reconcilerFakeControl) RenewLease(
	_ context.Context, _ string, _ string, _ LeaseFence, _ time.Time, _ time.Duration,
) (LeaseGrant, error) {
	s.renewCalls++
	if s.renewHook != nil {
		s.renewHook()
	}
	return s.renewed, s.renewErr
}

func (s *reconcilerFakeControl) LoadCurrentForwardRecovery(
	context.Context, ForwardRecoveryAuthorizationQuery,
) (CurrentForwardRecoverySnapshot, error) {
	return CurrentForwardRecoverySnapshot{}, ErrForwardRecoveryAuthorizationUnavailable
}

func (s *reconcilerFakeControl) CommitForwardRecoveryAction(
	_ context.Context, _ ForwardRecoveryActionGrant, command ForwardRecoveryActionCommand, _ time.Time,
) (Receipt, error) {
	s.actionCalls++
	s.actionCommands = append(s.actionCommands, cloneForwardRecoveryActionCommand(command))
	if s.actionHook != nil {
		s.actionHook()
	}
	receipt := s.receipt
	receipt.Revision = command.ReceiptRevision + 1
	receipt.State = ReceiptStored
	return receipt, s.actionErr
}

func (s *reconcilerFakeControl) CommitForwardRecoveryDisposition(
	_ context.Context, _ ForwardRecoveryDispositionGrant, command ForwardRecoveryDispositionCommand, _ time.Time,
) (Receipt, error) {
	s.dispositionCalls++
	receipt := s.receipt
	receipt.Revision = command.ReceiptRevision + 1
	if command.Disposition == ForwardRecoveryAuthorizationDenied {
		receipt.State = ReceiptRecoveryHold
	} else {
		receipt.State = ReceiptReserved
	}
	return receipt, s.dispositionErr
}

func (s *reconcilerFakeControl) FailForwardRecoveryAttempt(
	context.Context, ForwardRecoveryAttemptGrant, ForwardRecoveryAttemptFailure, time.Time,
) error {
	s.failureCalls++
	return nil
}

func (s *reconcilerFakeControl) LoadCurrentForwardRecoveryOutcome(
	context.Context, ForwardRecoveryOutcomeQuery,
) (CurrentForwardRecoveryOutcomeSnapshot, error) {
	return CurrentForwardRecoveryOutcomeSnapshot{}, ErrForwardRecoveryOutcomeUnavailable
}

func (s *reconcilerFakeControl) GetForwardRecoveryActionOutcome(
	_ context.Context, _ ForwardRecoveryOutcomeReadGrant, query ForwardRecoveryOutcomeQuery, _ time.Time,
) (RecoveryActionOutcome, error) {
	s.outcomeCalls++
	s.lastOutcomeQuery = query
	s.outcomeQueries = append(s.outcomeQueries, query)
	outcome := s.outcome
	if len(s.outcomes) >= s.outcomeCalls {
		outcome = s.outcomes[s.outcomeCalls-1]
	}
	outcome.ActionHash = query.ExpectedActionHash
	outcome.AttemptID = query.AttemptID
	outcome.ReceiptRevision = query.ExpectedReceiptRevision
	return outcome, s.outcomeErr
}

func reconcilerPinned(fill string, size, generation, metageneration int64) ArtifactPinnedLineage {
	return ArtifactPinnedLineage{
		SHA256: strings.Repeat(fill, 64), Size: size,
		Generation: generation, Metageneration: metageneration,
	}
}

func artifactPinnedLineageFromStored(stored StoredArtifact) ArtifactPinnedLineage {
	return ArtifactPinnedLineage{
		SHA256: stored.SHA256, CRC32C: stored.CRC32C, Size: stored.Size,
		Generation: stored.Generation, Metageneration: stored.Metageneration,
	}
}

func reconcilerManifestWrite(
	t *testing.T,
	request ArtifactClassificationRequest,
	pinned ArtifactPinnedLineage,
) RecoveryManifestWrite {
	t.Helper()
	raw := storedArtifactFromLineage(ArtifactLineage{
		Path: request.ExpectedRawPath, SHA256: pinned.SHA256, CRC32C: pinned.CRC32C,
		Size: pinned.Size, Generation: pinned.Generation, Metageneration: pinned.Metageneration,
	})
	input := manifestInputFromClassificationRequest(request)
	body, digest, err := CanonicalTelemetryManifest(input, raw)
	if err != nil {
		t.Fatalf("CanonicalTelemetryManifest() = %v", err)
	}
	write := RecoveryManifestWrite{
		ManifestPath: request.ExpectedManifestPath, ManifestInput: input,
		Raw: raw, CanonicalBody: body, Digest: digest,
	}
	if err := ValidateRecoveryManifestWrite(write); err != nil {
		t.Fatalf("ValidateRecoveryManifestWrite() = %v", err)
	}
	return write
}

func newReconcilerScriptedClock(values ...time.Time) func() time.Time {
	var mu sync.Mutex
	index := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}
