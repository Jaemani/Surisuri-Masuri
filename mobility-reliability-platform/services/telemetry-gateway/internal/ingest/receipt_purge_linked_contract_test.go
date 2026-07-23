package ingest

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestPlanReceiptPurgeLinkedPageAdvancesCleanupTargetsAndBindsDigest(t *testing.T) {
	job, receipt, now, children := receiptPurgeLinkedFixture(t)
	observation := receiptPurgeLinkedObservation(t, job, children, now.Add(2*time.Second))

	plan, err := PlanReceiptPurgeLinkedPage(
		job,
		receipt,
		observation,
		children,
		now.Add(3*time.Second),
	)
	if err != nil || plan.Kind != ReceiptPurgeLinkedMutationPage ||
		plan.NextJob.LinkCursor != children[len(children)-1].LinkDocumentID ||
		plan.NextJob.TargetDeletedCount != job.TargetDeletedCount+int64(len(children)) ||
		plan.NextJob.FindingDeletedCount != job.FindingDeletedCount ||
		plan.NextJob.Revision != job.Revision+1 ||
		!plan.NextJob.UpdatedAt.Equal(now.Add(3*time.Second)) ||
		!isLowerHexDigest(plan.DeleteSetDigest) ||
		ValidateReceiptPurgeLinkedMutationOutcomeQuery(plan.OutcomeQuery) != nil {
		t.Fatalf("linked page plan = %#v, %v", plan, err)
	}
	wantDigest, err := ReceiptPurgeLinkedSetDigest(children)
	if err != nil || plan.DeleteSetDigest != wantDigest ||
		plan.OutcomeQuery.DeleteSetDigest != wantDigest {
		t.Fatalf("linked set digest = %q/%q, %v; want %q", plan.DeleteSetDigest, plan.OutcomeQuery.DeleteSetDigest, err, wantDigest)
	}
	changed := append([]ReceiptPurgeLinkedChildState(nil), children...)
	changed[0].ChildDocumentDigest = strings.Repeat("e", 64)
	changedDigest, err := ReceiptPurgeLinkedSetDigest(changed)
	if err != nil || changedDigest == wantDigest {
		t.Fatalf("changed linked set digest = %q, %v", changedDigest, err)
	}
	children[0].ChildDocumentDigest = strings.Repeat("f", 64)
	if plan.Children[0].ChildDocumentDigest == children[0].ChildDocumentDigest ||
		plan.OutcomeQuery.Children[0].ChildDocumentDigest == children[0].ChildDocumentDigest {
		t.Fatal("linked page plan retained caller-owned children")
	}
}

func TestPlanReceiptPurgeLinkedPageRejectsMalformedForeignAndFindingChildren(t *testing.T) {
	job, receipt, now, validChildren := receiptPurgeLinkedFixture(t)
	malformed := validChildren[0]
	malformed.ChildDocumentDigest = "not-a-digest"
	foreign := receiptPurgeLinkedStateFixture(
		t,
		"99999999-9999-4999-8999-999999999999",
		job.ReceiptID,
		ReceiptPurgeLinkCleanupTarget,
		"55555555-5555-4555-8555-555555555555",
		now.Add(-time.Minute),
		"e",
		"f",
	)
	finding := receiptPurgeLinkedStateFixture(
		t,
		job.TenantID,
		job.ReceiptID,
		ReceiptPurgeLinkIntegrityFinding,
		"legacy-finding-001",
		now.Add(-time.Minute),
		"e",
		"f",
	)
	for _, test := range []struct {
		name  string
		child ReceiptPurgeLinkedChildState
	}{
		{name: "malformed", child: malformed},
		{name: "foreign", child: foreign},
		{name: "integrity finding unsupported", child: finding},
	} {
		t.Run(test.name, func(t *testing.T) {
			children := []ReceiptPurgeLinkedChildState{test.child}
			observation := receiptPurgeLinkedObservation(
				t,
				job,
				children,
				now.Add(2*time.Second),
			)
			plan, err := PlanReceiptPurgeLinkedPage(
				job,
				receipt,
				observation,
				children,
				now.Add(3*time.Second),
			)
			if !errors.Is(err, ErrInvalidReceiptPurgeMutation) ||
				!reflect.DeepEqual(plan, ReceiptPurgeLinkedMutationPlan{}) {
				t.Fatalf("invalid linked child plan = %#v, %v", plan, err)
			}
		})
	}
}

func TestPlanReceiptPurgeLinkedPageCountOverflowCreatesLinkedHold(t *testing.T) {
	job, receipt, now, children := receiptPurgeLinkedFixture(t)
	job.LinkCursor = "0"
	job.TargetDeletedCount = math.MaxInt64
	observation := receiptPurgeLinkedObservation(t, job, children[:1], now.Add(2*time.Second))

	plan, err := PlanReceiptPurgeLinkedPage(
		job,
		receipt,
		observation,
		children[:1],
		now.Add(3*time.Second),
	)
	if err != nil || plan.Kind != ReceiptPurgeLinkedMutationHold ||
		plan.NextJob.Status != ReceiptPurgeJobHold ||
		plan.NextJob.HeldFromStatus != ReceiptPurgeJobLinkedDocumentsPurging ||
		plan.NextJob.ErrorClass != ReceiptPurgeErrorCountOverflow ||
		plan.NextJob.LinkCursor != job.LinkCursor ||
		plan.NextJob.TargetDeletedCount != math.MaxInt64 ||
		len(plan.Children) != 0 || plan.DeleteSetDigest != "" ||
		ValidateReceiptPurgeLinkedMutationOutcomeQuery(plan.OutcomeQuery) != nil {
		t.Fatalf("linked overflow hold = %#v, %v", plan, err)
	}
}

func TestPlanReceiptPurgeLinkedPageRejectsReceiptAndFenceDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ReceiptPurgeReceiptState)
	}{
		{name: "receipt revision", mutate: func(receipt *ReceiptPurgeReceiptState) {
			receipt.Revision++
		}},
		{name: "wrong purge job", mutate: func(receipt *ReceiptPurgeReceiptState) {
			receipt.Fence.PurgeJobID = strings.Repeat("f", 64)
		}},
		{name: "partial fence", mutate: func(receipt *ReceiptPurgeReceiptState) {
			receipt.Fence.StartedAt = time.Time{}
			receipt.Fence.Version = ""
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			job, receipt, now, children := receiptPurgeLinkedFixture(t)
			test.mutate(&receipt)
			observation := receiptPurgeLinkedObservation(
				t,
				job,
				children,
				now.Add(2*time.Second),
			)
			plan, err := PlanReceiptPurgeLinkedPage(
				job,
				receipt,
				observation,
				children,
				now.Add(3*time.Second),
			)
			if !errors.Is(err, ErrReceiptPurgeMutationConflict) ||
				!reflect.DeepEqual(plan, ReceiptPurgeLinkedMutationPlan{}) {
				t.Fatalf("drifted receipt plan = %#v, %v", plan, err)
			}
		})
	}
}

func TestPlanReceiptPurgeLinkedPageRejectsOrderedCursorAndStaleObservationDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ReceiptPurgePageObservation)
	}{
		{name: "unordered", mutate: func(observation *ReceiptPurgePageObservation) {
			observation.DeleteDocumentIDs[0], observation.DeleteDocumentIDs[1] =
				observation.DeleteDocumentIDs[1], observation.DeleteDocumentIDs[0]
		}},
		{name: "cursor", mutate: func(observation *ReceiptPurgePageObservation) {
			observation.Request.AfterDocumentID = "0"
		}},
		{name: "stale job revision", mutate: func(observation *ReceiptPurgePageObservation) {
			observation.Request.ExpectedJobRevision--
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			job, receipt, now, children := receiptPurgeLinkedFixture(t)
			observation := receiptPurgeLinkedObservation(
				t,
				job,
				children,
				now.Add(2*time.Second),
			)
			test.mutate(&observation)
			plan, err := PlanReceiptPurgeLinkedPage(
				job,
				receipt,
				observation,
				children,
				now.Add(3*time.Second),
			)
			if !errors.Is(err, ErrReceiptPurgeMutationConflict) ||
				!reflect.DeepEqual(plan, ReceiptPurgeLinkedMutationPlan{}) {
				t.Fatalf("stale observation plan = %#v, %v", plan, err)
			}
		})
	}
	job, receipt, now, children := receiptPurgeLinkedFixture(t)
	observation := receiptPurgeLinkedObservation(t, job, children, now.Add(2*time.Second))
	if _, err := PlanReceiptPurgeLinkedPage(
		job,
		receipt,
		observation,
		children,
		observation.ReadAt.Add(-time.Nanosecond),
	); !errors.Is(err, ErrReceiptPurgeMutationConflict) {
		t.Fatalf("pre-observation commit time error = %v", err)
	}
}

func TestEvaluateReceiptPurgeLinkedMutationOutcomeClassifiesExactAndPartialDeletion(t *testing.T) {
	job, receipt, now, children := receiptPurgeLinkedFixture(t)
	observation := receiptPurgeLinkedObservation(t, job, children, now.Add(2*time.Second))
	plan, err := PlanReceiptPurgeLinkedPage(
		job,
		receipt,
		observation,
		children,
		now.Add(3*time.Second),
	)
	if err != nil {
		t.Fatalf("PlanReceiptPurgeLinkedPage() = %v", err)
	}
	present := make([]ReceiptPurgeLinkedChildOutcomeObservation, len(children))
	absent := make([]ReceiptPurgeLinkedChildOutcomeObservation, len(children))
	for index, child := range children {
		present[index] = ReceiptPurgeLinkedChildOutcomeObservation{
			LinkDocumentID: child.LinkDocumentID,
			LinkPresent:    true,
			ChildPresent:   true,
			State:          child,
		}
		absent[index] = ReceiptPurgeLinkedChildOutcomeObservation{
			LinkDocumentID: child.LinkDocumentID,
		}
	}
	snapshot := ReceiptPurgeLinkedMutationOutcomeSnapshot{
		JobPresent: true, ReceiptPresent: true, Job: plan.PreJob, Receipt: receipt,
		Children: present, ReadTime: now.Add(4 * time.Second),
	}
	outcome, err := EvaluateReceiptPurgeLinkedMutationOutcome(
		plan.OutcomeQuery,
		snapshot,
		now.Add(5*time.Second),
	)
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationNotCommitted ||
		outcome.JobRevision != plan.PreJob.Revision ||
		outcome.TargetDeletedCount != plan.PreJob.TargetDeletedCount {
		t.Fatalf("not-committed linked outcome = %#v, %v", outcome, err)
	}

	snapshot.Job = plan.NextJob
	snapshot.Children = absent
	outcome, err = EvaluateReceiptPurgeLinkedMutationOutcome(
		plan.OutcomeQuery,
		snapshot,
		now.Add(5*time.Second),
	)
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationCommitted ||
		outcome.JobRevision != plan.NextJob.Revision ||
		outcome.LinkCursor != plan.NextJob.LinkCursor ||
		outcome.TargetDeletedCount != plan.NextJob.TargetDeletedCount {
		t.Fatalf("committed linked outcome = %#v, %v", outcome, err)
	}

	partial := append([]ReceiptPurgeLinkedChildOutcomeObservation(nil), absent...)
	partial[0] = present[0]
	snapshot.Children = partial
	outcome, err = EvaluateReceiptPurgeLinkedMutationOutcome(
		plan.OutcomeQuery,
		snapshot,
		now.Add(5*time.Second),
	)
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationUnverifiable {
		t.Fatalf("partial linked deletion outcome = %#v, %v", outcome, err)
	}

	invalid := append([]ReceiptPurgeLinkedChildOutcomeObservation(nil), absent...)
	invalid[0] = ReceiptPurgeLinkedChildOutcomeObservation{
		LinkDocumentID: children[0].LinkDocumentID,
		LinkPresent:    true,
		Invalid:        true,
	}
	snapshot.Children = invalid
	outcome, err = EvaluateReceiptPurgeLinkedMutationOutcome(
		plan.OutcomeQuery,
		snapshot,
		now.Add(5*time.Second),
	)
	if err != nil || outcome.CommitStatus != ReceiptPurgeMutationUnverifiable {
		t.Fatalf("invalid linked observation outcome = %#v, %v", outcome, err)
	}

	invalid[0].State = children[0]
	snapshot.Children = invalid
	if _, err = EvaluateReceiptPurgeLinkedMutationOutcome(
		plan.OutcomeQuery,
		snapshot,
		now.Add(5*time.Second),
	); !errors.Is(err, ErrReceiptPurgeMutationOutcomeUnavailable) {
		t.Fatalf("invalid linked observation with state error = %v", err)
	}

	invalid[0] = ReceiptPurgeLinkedChildOutcomeObservation{
		LinkDocumentID: children[0].LinkDocumentID,
		Invalid:        true,
	}
	snapshot.Children = invalid
	if _, err = EvaluateReceiptPurgeLinkedMutationOutcome(
		plan.OutcomeQuery,
		snapshot,
		now.Add(5*time.Second),
	); !errors.Is(err, ErrReceiptPurgeMutationOutcomeUnavailable) {
		t.Fatalf("invalid absent linked observation error = %v", err)
	}

	invalid[0] = ReceiptPurgeLinkedChildOutcomeObservation{
		LinkDocumentID: children[0].LinkDocumentID,
		State:          children[0],
	}
	snapshot.Children = invalid
	if _, err = EvaluateReceiptPurgeLinkedMutationOutcome(
		plan.OutcomeQuery,
		snapshot,
		now.Add(5*time.Second),
	); !errors.Is(err, ErrReceiptPurgeMutationOutcomeUnavailable) {
		t.Fatalf("absent linked observation with state error = %v", err)
	}
}

func TestReceiptPurgeLinkedMutationBindingRejectsTamperAndChangesWithChildDigests(t *testing.T) {
	job, receipt, now, children := receiptPurgeLinkedFixture(t)
	observation := receiptPurgeLinkedObservation(t, job, children, now.Add(2*time.Second))
	plan, err := PlanReceiptPurgeLinkedPage(
		job,
		receipt,
		observation,
		children,
		now.Add(3*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ReceiptPurgeLinkedMutationBinding(plan.OutcomeQuery)
	if err != nil || !isLowerHexDigest(binding) {
		t.Fatalf("ReceiptPurgeLinkedMutationBinding() = %q, %v", binding, err)
	}

	changedChildren := append([]ReceiptPurgeLinkedChildState(nil), plan.Children...)
	changedChildren[0].LinkDocumentDigest = strings.Repeat("e", 64)
	changedDigest, err := ReceiptPurgeLinkedSetDigest(changedChildren)
	if err != nil {
		t.Fatal(err)
	}
	changedPlan, err := newReceiptPurgeLinkedMutationPlan(
		ReceiptPurgeLinkedMutationPage,
		plan.PreJob,
		plan.NextJob,
		changedChildren,
		changedDigest,
	)
	if err != nil {
		t.Fatalf("changed linked mutation plan = %v", err)
	}
	changedBinding, err := ReceiptPurgeLinkedMutationBinding(changedPlan.OutcomeQuery)
	if err != nil || changedBinding == binding {
		t.Fatalf("changed linked binding = %q, %v; original %q", changedBinding, err, binding)
	}

	for _, mutate := range []func(*ReceiptPurgeLinkedMutationOutcomeQuery){
		func(query *ReceiptPurgeLinkedMutationOutcomeQuery) {
			query.DeleteSetDigest = strings.Repeat("f", 64)
		},
		func(query *ReceiptPurgeLinkedMutationOutcomeQuery) {
			query.NextJob.TargetDeletedCount++
		},
		func(query *ReceiptPurgeLinkedMutationOutcomeQuery) {
			query.Children = append([]ReceiptPurgeLinkedChildState(nil), query.Children...)
			query.Children[0].ChildDocumentDigest = strings.Repeat("f", 64)
		},
	} {
		tampered := plan.OutcomeQuery
		tampered.Children = append([]ReceiptPurgeLinkedChildState(nil), plan.OutcomeQuery.Children...)
		mutate(&tampered)
		if _, err := ReceiptPurgeLinkedMutationBinding(tampered); !errors.Is(
			err,
			ErrInvalidReceiptPurgeMutation,
		) {
			t.Fatalf("tampered binding query accepted: %#v, %v", tampered, err)
		}
	}
}

func receiptPurgeLinkedFixture(
	t *testing.T,
) (ReceiptPurgeJob, ReceiptPurgeReceiptState, time.Time, []ReceiptPurgeLinkedChildState) {
	t.Helper()
	job, receipt, now := receiptPurgeAttemptFixture(t)
	job.Status = ReceiptPurgeJobLinkedDocumentsPurging
	job.Revision = 3
	job.UpdatedAt = now.Add(time.Second)
	if err := ValidateReceiptPurgeJob(job); err != nil {
		t.Fatalf("linked job fixture = %v", err)
	}
	children := []ReceiptPurgeLinkedChildState{
		receiptPurgeLinkedStateFixture(
			t,
			job.TenantID,
			job.ReceiptID,
			ReceiptPurgeLinkCleanupTarget,
			"33333333-3333-4333-8333-333333333333",
			now.Add(-2*time.Minute),
			"a",
			"b",
		),
		receiptPurgeLinkedStateFixture(
			t,
			job.TenantID,
			job.ReceiptID,
			ReceiptPurgeLinkCleanupTarget,
			"44444444-4444-4444-8444-444444444444",
			now.Add(-time.Minute),
			"c",
			"d",
		),
	}
	sort.Slice(children, func(left, right int) bool {
		return children[left].LinkDocumentID < children[right].LinkDocumentID
	})
	return job, receipt, now, children
}

func receiptPurgeLinkedStateFixture(
	t *testing.T,
	tenantID string,
	receiptID string,
	kind ReceiptPurgeLinkKind,
	documentID string,
	createdAt time.Time,
	linkDigestCharacter string,
	childDigestCharacter string,
) ReceiptPurgeLinkedChildState {
	t.Helper()
	child := ReceiptPurgeLinkedChildIdentity{
		TenantID: tenantID, ReceiptID: receiptID, Kind: kind,
		DocumentID: documentID, CreatedAt: createdAt,
	}
	link, err := BuildReceiptPurgeInverseLink(child)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	return ReceiptPurgeLinkedChildState{
		LinkDocumentID:      link.LinkID,
		LinkDocumentDigest:  strings.Repeat(linkDigestCharacter, 64),
		Link:                link,
		ChildDocumentDigest: strings.Repeat(childDigestCharacter, 64),
		Child:               child,
	}
}

func receiptPurgeLinkedObservation(
	t *testing.T,
	job ReceiptPurgeJob,
	children []ReceiptPurgeLinkedChildState,
	readAt time.Time,
) ReceiptPurgePageObservation {
	t.Helper()
	ids := make([]string, len(children))
	for index, child := range children {
		ids[index] = child.LinkDocumentID
	}
	observation, err := BuildReceiptPurgePageObservation(
		ReceiptPurgePageRequest{
			PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
			Kind: ReceiptPurgePageLinks, ExpectedJobStatus: job.Status,
			ExpectedJobRevision: job.Revision, AfterDocumentID: job.LinkCursor,
			PageSize: len(children),
		},
		ids,
		readAt,
	)
	if err != nil {
		t.Fatalf("BuildReceiptPurgePageObservation() = %v", err)
	}
	return observation
}
