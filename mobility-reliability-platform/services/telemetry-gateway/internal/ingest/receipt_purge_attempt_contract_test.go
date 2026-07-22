package ingest

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReceiptPurgeAttemptStateAndDigestAreStrict(t *testing.T) {
	job, _, now := receiptPurgeAttemptFixture(t)
	attempts := receiptPurgeAttemptStates(job, now)
	for _, attempt := range attempts {
		if err := ValidateReceiptPurgeAttemptState(attempt); err != nil {
			t.Fatalf("valid attempt rejected: %#v: %v", attempt, err)
		}
	}
	digest, err := ReceiptPurgeAttemptSetDigest(attempts)
	if err != nil || !isLowerHexDigest(digest) {
		t.Fatalf("ReceiptPurgeAttemptSetDigest() = %q, %v", digest, err)
	}
	changed := append([]ReceiptPurgeAttemptState(nil), attempts...)
	changed[0].FencingToken++
	changedDigest, err := ReceiptPurgeAttemptSetDigest(changed)
	if err != nil || changedDigest == digest {
		t.Fatalf("changed digest = %q, %v", changedDigest, err)
	}
	invalid := attempts[0]
	invalid.DocumentID = attempts[1].DocumentID
	if !errors.Is(ValidateReceiptPurgeAttemptState(invalid), ErrInvalidReceiptPurgeMutation) {
		t.Fatal("document/attempt mismatch accepted")
	}
	invalid = attempts[0]
	invalid.WorkerVersion = CleanupWorkerVersion
	if !errors.Is(ValidateReceiptPurgeAttemptState(invalid), ErrInvalidReceiptPurgeMutation) {
		t.Fatal("owner/worker schema mismatch accepted")
	}
	invalid = attempts[0]
	invalid.Outcome = RecoveryAttemptOutcomeExpired
	if !errors.Is(ValidateReceiptPurgeAttemptState(invalid), ErrInvalidReceiptPurgeMutation) {
		t.Fatal("cleanup-only outcome on sweeper accepted")
	}
	if _, err := ReceiptPurgeAttemptSetDigest([]ReceiptPurgeAttemptState{attempts[1], attempts[0]}); !errors.Is(err, ErrInvalidReceiptPurgeMutation) {
		t.Fatal("unordered attempt set accepted")
	}
}

func TestPlanReceiptPurgeAttemptPageAdvancesExactCommittedSet(t *testing.T) {
	job, receipt, now := receiptPurgeAttemptFixture(t)
	begin, err := PlanReceiptPurgeAttemptPhase(job, receipt, ReceiptPurgeAttemptPhaseCommand{
		Action: ReceiptPurgeAttemptPhaseBegin, PurgeKey: job.PurgeKey,
		TenantID: job.TenantID, ReceiptID: job.ReceiptID, ExpectedJobRevision: job.Revision,
	}, now.Add(time.Second))
	if err != nil || begin.NextJob.Status != ReceiptPurgeJobAttemptsPurging ||
		begin.NextJob.Revision != 2 || begin.Kind != ReceiptPurgeMutationAttemptPhaseBegin {
		t.Fatalf("begin plan = %#v, %v", begin, err)
	}
	attempts := receiptPurgeAttemptStates(job, now)
	request := ReceiptPurgePageRequest{
		PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		Kind: ReceiptPurgePageAttempts, ExpectedJobStatus: ReceiptPurgeJobAttemptsPurging,
		ExpectedJobRevision: begin.NextJob.Revision, PageSize: 2,
	}
	ids := []string{attempts[0].DocumentID, attempts[1].DocumentID, "55555555-5555-4555-8555-555555555555"}
	observation, err := BuildReceiptPurgePageObservation(request, ids, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("BuildReceiptPurgePageObservation() = %v", err)
	}
	plan, err := PlanReceiptPurgeAttemptPage(
		begin.NextJob, receipt, observation, attempts, now.Add(3*time.Second),
	)
	if err != nil || plan.Kind != ReceiptPurgeMutationAttemptPage ||
		plan.NextJob.AttemptCursor != attempts[1].DocumentID ||
		plan.NextJob.AttemptDeletedCount != 2 || plan.NextJob.Revision != 3 ||
		len(plan.DeleteDocumentIDs) != 2 || plan.DeleteDocumentIDs[1] != attempts[1].DocumentID ||
		observation.LookaheadDocumentID != ids[2] ||
		ValidateReceiptPurgeMutationOutcomeQuery(plan.OutcomeQuery) != nil {
		t.Fatalf("page plan = %#v, %v", plan, err)
	}
	observation.DeleteDocumentIDs[0] = "caller-mutated"
	if plan.DeleteDocumentIDs[0] != attempts[0].DocumentID ||
		plan.OutcomeQuery.DeleteDocumentIDs[0] != attempts[0].DocumentID {
		t.Fatal("mutation plan retained caller-owned slice")
	}
}

func TestPlanReceiptPurgeAttemptPageCountOverflowHoldsWithoutDelete(t *testing.T) {
	job, receipt, now := receiptPurgeAttemptFixture(t)
	job.Status = ReceiptPurgeJobAttemptsPurging
	job.Revision = 2
	job.AttemptCursor = "22222222-2222-4222-8222-222222222221"
	job.AttemptDeletedCount = math.MaxInt64
	job.UpdatedAt = now.Add(time.Second)
	attempt := receiptPurgeAttemptStates(job, now)[0]
	request := ReceiptPurgePageRequest{
		PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		Kind: ReceiptPurgePageAttempts, ExpectedJobStatus: job.Status,
		ExpectedJobRevision: job.Revision, AfterDocumentID: job.AttemptCursor, PageSize: 1,
	}
	observation, _ := BuildReceiptPurgePageObservation(
		request, []string{attempt.DocumentID}, now.Add(2*time.Second),
	)
	plan, err := PlanReceiptPurgeAttemptPage(job, receipt, observation, []ReceiptPurgeAttemptState{attempt}, now.Add(3*time.Second))
	if err != nil || plan.Kind != ReceiptPurgeMutationAttemptHold ||
		plan.NextJob.Status != ReceiptPurgeJobHold ||
		plan.NextJob.ErrorClass != ReceiptPurgeErrorCountOverflow ||
		len(plan.DeleteDocumentIDs) != 0 || plan.NextJob.AttemptCursor != job.AttemptCursor ||
		plan.NextJob.AttemptDeletedCount != job.AttemptDeletedCount {
		t.Fatalf("overflow plan = %#v, %v", plan, err)
	}
}

func TestPlanReceiptPurgeAttemptHoldAcceptsOnlyTrustedPartialFenceShape(t *testing.T) {
	job, receipt, now := receiptPurgeAttemptFixture(t)
	job.Status = ReceiptPurgeJobAttemptsPurging
	job.Revision = 2
	job.UpdatedAt = now.Add(time.Second)
	receipt.Fence.StartedAt = time.Time{}
	receipt.Fence.Version = ""
	plan, err := PlanReceiptPurgeAttemptHold(
		job, receipt, ReceiptPurgeErrorFencePartial, now.Add(2*time.Second),
	)
	if err != nil || plan.NextJob.Status != ReceiptPurgeJobHold ||
		plan.NextJob.HeldFromStatus != ReceiptPurgeJobAttemptsPurging ||
		plan.NextJob.ErrorClass != ReceiptPurgeErrorFencePartial {
		t.Fatalf("partial-fence hold = %#v, %v", plan, err)
	}
	receipt.Fence = ReceiptPurgeFence{}
	if _, err := PlanReceiptPurgeAttemptHold(
		job, receipt, ReceiptPurgeErrorFencePartial, now.Add(2*time.Second),
	); !errors.Is(err, ErrInvalidReceiptPurgeMutation) {
		t.Fatal("empty fence created a partial-fence hold")
	}
}

func TestPlanReceiptPurgeAttemptHoldPersistsPlannedLinkageDrift(t *testing.T) {
	job, receipt, now := receiptPurgeAttemptFixture(t)
	receipt.Fence.PurgeJobID = strings.Repeat("f", 64)
	if ReceiptPurgeMutationPoisonClass(job, receipt) != ReceiptPurgeErrorLinkageDrift {
		t.Fatal("fully populated wrong fence was not classified")
	}
	plan, err := PlanReceiptPurgeAttemptHold(
		job, receipt, ReceiptPurgeErrorLinkageDrift, now.Add(time.Second),
	)
	if err != nil || plan.NextJob.Status != ReceiptPurgeJobHold ||
		plan.NextJob.HeldFromStatus != ReceiptPurgeJobPlanned ||
		plan.NextJob.ErrorClass != ReceiptPurgeErrorLinkageDrift {
		t.Fatalf("planned linkage hold = %#v, %v", plan, err)
	}
	snapshot := ReceiptPurgeMutationOutcomeSnapshot{
		JobPresent: true, ReceiptPresent: true, Job: plan.PreJob, Receipt: receipt,
		ReadTime: now.Add(2 * time.Second),
	}
	outcome, err := EvaluateReceiptPurgeMutationOutcome(
		plan.OutcomeQuery, snapshot, now.Add(3*time.Second),
	)
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationNotCommitted {
		t.Fatalf("drift hold pre-state = %#v, %v", outcome, err)
	}
	snapshot.Job = plan.NextJob
	outcome, err = EvaluateReceiptPurgeMutationOutcome(
		plan.OutcomeQuery, snapshot, now.Add(3*time.Second),
	)
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationCommitted {
		t.Fatalf("drift hold committed = %#v, %v", outcome, err)
	}
}

func TestPlanReceiptPurgeAttemptPhaseRequiresFreshExactEmptyObservation(t *testing.T) {
	job, receipt, now := receiptPurgeAttemptFixture(t)
	job.Status = ReceiptPurgeJobAttemptsPurging
	job.Revision = 4
	job.AttemptCursor = "44444444-4444-4444-8444-444444444444"
	job.AttemptDeletedCount = 2
	job.UpdatedAt = now.Add(time.Second)
	request := ReceiptPurgePageRequest{
		PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		Kind: ReceiptPurgePageAttempts, ExpectedJobStatus: job.Status,
		ExpectedJobRevision: job.Revision, AfterDocumentID: job.AttemptCursor, PageSize: 2,
	}
	empty, err := BuildReceiptPurgePageObservation(request, nil, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("empty observation = %v", err)
	}
	command := ReceiptPurgeAttemptPhaseCommand{
		Action: ReceiptPurgeAttemptPhaseComplete, PurgeKey: job.PurgeKey,
		TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		ExpectedJobRevision: job.Revision, EmptyObservation: empty,
	}
	plan, err := PlanReceiptPurgeAttemptPhase(job, receipt, command, now.Add(3*time.Second))
	if err != nil || plan.NextJob.Status != ReceiptPurgeJobLinkedDocumentsPurging ||
		plan.NextJob.AttemptCursor != job.AttemptCursor || plan.NextJob.AttemptDeletedCount != 2 {
		t.Fatalf("complete phase plan = %#v, %v", plan, err)
	}
	if _, err := PlanReceiptPurgeAttemptPhase(
		job, receipt, command, empty.ReadAt.Add(ReceiptPurgeEmptyObservationMaxAge+time.Nanosecond),
	); !errors.Is(err, ErrReceiptPurgeMutationConflict) {
		t.Fatal("stale empty observation transitioned phase")
	}
}

func TestEvaluateReceiptPurgeMutationOutcomeClassifiesExactPageStates(t *testing.T) {
	job, receipt, now := receiptPurgeAttemptFixture(t)
	job.Status = ReceiptPurgeJobAttemptsPurging
	job.Revision = 2
	job.UpdatedAt = now.Add(time.Second)
	attempts := receiptPurgeAttemptStates(job, now)
	request := ReceiptPurgePageRequest{
		PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		Kind: ReceiptPurgePageAttempts, ExpectedJobStatus: job.Status,
		ExpectedJobRevision: job.Revision, PageSize: 2,
	}
	observation, _ := BuildReceiptPurgePageObservation(
		request, []string{attempts[0].DocumentID, attempts[1].DocumentID}, now.Add(2*time.Second),
	)
	plan, err := PlanReceiptPurgeAttemptPage(job, receipt, observation, attempts, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("page plan = %v", err)
	}
	present := make([]ReceiptPurgeAttemptOutcomeObservation, len(attempts))
	absent := make([]ReceiptPurgeAttemptOutcomeObservation, len(attempts))
	for index, attempt := range attempts {
		present[index] = ReceiptPurgeAttemptOutcomeObservation{
			DocumentID: attempt.DocumentID, Present: true, Attempt: attempt,
		}
		absent[index] = ReceiptPurgeAttemptOutcomeObservation{DocumentID: attempt.DocumentID}
	}
	snapshot := ReceiptPurgeMutationOutcomeSnapshot{
		JobPresent: true, ReceiptPresent: true, Job: plan.PreJob, Receipt: receipt,
		Attempts: present, ReadTime: now.Add(4 * time.Second),
	}
	outcome, err := EvaluateReceiptPurgeMutationOutcome(plan.OutcomeQuery, snapshot, now.Add(5*time.Second))
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationNotCommitted {
		t.Fatalf("not committed outcome = %#v, %v", outcome, err)
	}
	snapshot.Job = plan.NextJob
	snapshot.Attempts = absent
	outcome, err = EvaluateReceiptPurgeMutationOutcome(plan.OutcomeQuery, snapshot, now.Add(5*time.Second))
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationCommitted ||
		outcome.JobRevision != plan.NextJob.Revision {
		t.Fatalf("committed outcome = %#v, %v", outcome, err)
	}
	snapshot.Attempts[0] = present[0]
	outcome, err = EvaluateReceiptPurgeMutationOutcome(plan.OutcomeQuery, snapshot, now.Add(5*time.Second))
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationUnverifiable {
		t.Fatalf("partial outcome = %#v, %v", outcome, err)
	}
	snapshot.Attempts = absent
	snapshot.Job.Revision++
	snapshot.Job.UpdatedAt = now.Add(4 * time.Second)
	if ValidateReceiptPurgeJob(snapshot.Job) != nil {
		t.Fatal("progressed job fixture invalid")
	}
	outcome, err = EvaluateReceiptPurgeMutationOutcome(plan.OutcomeQuery, snapshot, now.Add(5*time.Second))
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationUnverifiable {
		t.Fatalf("progressed winner outcome = %#v, %v", outcome, err)
	}
}

func TestEvaluateReceiptPurgeMutationOutcomeRejectsForgedInvalidObservation(t *testing.T) {
	job, receipt, now := receiptPurgeAttemptFixture(t)
	job.Status = ReceiptPurgeJobAttemptsPurging
	job.Revision = 2
	job.UpdatedAt = now.Add(time.Second)
	attempts := receiptPurgeAttemptStates(job, now)
	request := ReceiptPurgePageRequest{
		PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		Kind: ReceiptPurgePageAttempts, ExpectedJobStatus: job.Status,
		ExpectedJobRevision: job.Revision, PageSize: 2,
	}
	observation, _ := BuildReceiptPurgePageObservation(
		request, []string{attempts[0].DocumentID, attempts[1].DocumentID}, now.Add(2*time.Second),
	)
	plan, err := PlanReceiptPurgeAttemptPage(job, receipt, observation, attempts, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	forged := []ReceiptPurgeAttemptOutcomeObservation{
		{DocumentID: attempts[0].DocumentID, Present: true, Invalid: true, Attempt: attempts[0]},
		{DocumentID: attempts[1].DocumentID, Present: true, Attempt: attempts[1]},
	}
	_, err = EvaluateReceiptPurgeMutationOutcome(
		plan.OutcomeQuery,
		ReceiptPurgeMutationOutcomeSnapshot{
			JobPresent: true, ReceiptPresent: true, Job: plan.PreJob, Receipt: receipt,
			Attempts: forged, ReadTime: now.Add(4 * time.Second),
		},
		now.Add(5*time.Second),
	)
	if !errors.Is(err, ErrReceiptPurgeMutationOutcomeUnavailable) {
		t.Fatalf("forged invalid observation err = %v", err)
	}
}

func TestReceiptPurgeMutationPublicContractHasNoSensitiveSurface(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(ReceiptPurgeAttemptState{}), reflect.TypeOf(ReceiptPurgeMutationPlan{}),
		reflect.TypeOf(ReceiptPurgeMutationOutcomeQuery{}),
		reflect.TypeOf(ReceiptPurgeAttemptOutcomeObservation{}),
		reflect.TypeOf(ReceiptPurgeMutationOutcomeSnapshot{}),
		reflect.TypeOf(ReceiptPurgeMutationOutcome{}), reflect.TypeOf(ReceiptPurgeMutationResult{}),
		reflect.TypeOf(ReceiptPurgeAttemptPhaseCommand{}),
	}
	for _, value := range types {
		for index := 0; index < value.NumField(); index++ {
			name := strings.ToLower(value.Field(index).Name)
			for _, forbidden := range []string{"uid", "path", "payload", "device", "trip", "person", "object"} {
				if strings.Contains(name, forbidden) {
					t.Fatalf("%s exposes forbidden field %q", value.Name(), value.Field(index).Name)
				}
			}
		}
	}
}

func receiptPurgeAttemptFixture(t *testing.T) (ReceiptPurgeJob, ReceiptPurgeReceiptState, time.Time) {
	t.Helper()
	state, now := receiptPurgeAdmissionFixture(t)
	command, err := BuildReceiptPurgeAdmissionCommand(state, now)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeAdmissionCommand() = %v", err)
	}
	job, err := BuildPostFenceReceiptPurgeJob(command, now)
	if err != nil {
		t.Fatalf("BuildPostFenceReceiptPurgeJob() = %v", err)
	}
	receipt := state.Receipt
	receipt.Revision = job.ReceiptRevision
	receipt.UpdatedAt = now
	receipt.Fence = ReceiptPurgeFence{
		PurgeJobID: job.PurgeKey, StartedAt: job.CreatedAt, Version: ReceiptPurgeFenceVersion,
	}
	return job, receipt, now
}

func receiptPurgeAttemptStates(job ReceiptPurgeJob, now time.Time) []ReceiptPurgeAttemptState {
	startedAt := now.Add(-2 * time.Minute)
	return []ReceiptPurgeAttemptState{
		{
			DocumentID: "33333333-3333-4333-8333-333333333333",
			AttemptID:  "33333333-3333-4333-8333-333333333333",
			TenantID:   job.TenantID, ReceiptID: job.ReceiptID,
			OwnerKind: LeaseOwnerSweeper, FencingToken: 1, WorkerVersion: RecoveryWorkerVersion,
			DocumentDigest: strings.Repeat("a", 64),
			Status:         RecoveryAttemptCompleted, Outcome: RecoveryAttemptOutcomeStored,
			StartedAt: startedAt, CompletedAt: startedAt.Add(time.Minute),
		},
		{
			DocumentID: "44444444-4444-4444-8444-444444444444",
			AttemptID:  "44444444-4444-4444-8444-444444444444",
			TenantID:   job.TenantID, ReceiptID: job.ReceiptID,
			OwnerKind: LeaseOwnerCleanup, FencingToken: 2, WorkerVersion: CleanupWorkerVersion,
			DocumentDigest: strings.Repeat("b", 64),
			Status:         RecoveryAttemptCompleted, Outcome: RecoveryAttemptOutcomeExpired,
			StartedAt: startedAt, CompletedAt: startedAt.Add(90 * time.Second),
		},
	}
}
