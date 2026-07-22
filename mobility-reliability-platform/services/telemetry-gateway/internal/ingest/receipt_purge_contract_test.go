package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeriveReceiptPurgeKeyUsesExactNULSeparatedBinding(t *testing.T) {
	state, _ := receiptPurgeAdmissionFixture(t)
	wantDigest := sha256.Sum256([]byte(
		receiptPurgeKeyVersion + "\x00" + state.Receipt.TenantID + "\x00" + state.Receipt.ReceiptID,
	))
	want := hex.EncodeToString(wantDigest[:])
	got, err := DeriveReceiptPurgeKey(state.Receipt.TenantID, state.Receipt.ReceiptID)
	if err != nil || got != want || !isLowerHexDigest(got) {
		t.Fatalf("DeriveReceiptPurgeKey() = %q, %v; want %q", got, err, want)
	}
	otherTenant, err := DeriveReceiptPurgeKey(
		"22222222-2222-4222-8222-222222222222", state.Receipt.ReceiptID,
	)
	if err != nil || otherTenant == got {
		t.Fatalf("tenant-separated purge key = %q, %v", otherTenant, err)
	}
	if _, err := DeriveReceiptPurgeKey("invalid", state.Receipt.ReceiptID); !errors.Is(
		err, ErrInvalidReceiptPurgeAdmission,
	) {
		t.Fatalf("invalid tenant error = %v", err)
	}
}

func TestReceiptPurgeLinkageHashBindsEveryBoundedField(t *testing.T) {
	state, _ := receiptPurgeAdmissionFixture(t)
	linkage := receiptPurgeLinkageFromState(state, state.Receipt.Revision+1)
	want, err := ReceiptPurgeLinkageHash(linkage)
	if err != nil || !isLowerHexDigest(want) {
		t.Fatalf("ReceiptPurgeLinkageHash() = %q, %v", want, err)
	}
	mutations := []func(*ReceiptPurgeLinkage){
		func(value *ReceiptPurgeLinkage) { value.TenantID = "22222222-2222-4222-8222-222222222222" },
		func(value *ReceiptPurgeLinkage) { value.ReceiptID = "33333333-3333-4333-8333-333333333333" },
		func(value *ReceiptPurgeLinkage) {
			value.ReservationKey = strings.Repeat("c", 64)
			value.ReservationIndexDocumentID = value.ReservationKey
		},
		func(value *ReceiptPurgeLinkage) {
			value.ClientBatchKey = strings.Repeat("d", 64)
			value.ClientBatchIndexDocumentID = value.ClientBatchKey
		},
		func(value *ReceiptPurgeLinkage) { value.PostFenceReceiptRevision++ },
		func(value *ReceiptPurgeLinkage) { value.PurgeEligibleAt = value.PurgeEligibleAt.Add(time.Nanosecond) },
	}
	for index, mutate := range mutations {
		changed := linkage
		mutate(&changed)
		got, hashErr := ReceiptPurgeLinkageHash(changed)
		if hashErr != nil || got == want {
			t.Fatalf("mutation %d hash = %q, %v", index, got, hashErr)
		}
	}
	invalid := linkage
	invalid.ReservationIndexDocumentID = strings.Repeat("f", 64)
	if _, err := ReceiptPurgeLinkageHash(invalid); !errors.Is(err, ErrInvalidReceiptPurgeAdmission) {
		t.Fatalf("mismatched index ID error = %v", err)
	}
}

func TestValidateReceiptPurgeJobAcceptsEveryDefinedStateShape(t *testing.T) {
	state, checkedAt := receiptPurgeAdmissionFixture(t)
	command, err := BuildReceiptPurgeAdmissionCommand(state, checkedAt)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeAdmissionCommand() = %v", err)
	}
	planned, err := BuildPostFenceReceiptPurgeJob(command, checkedAt)
	if err != nil {
		t.Fatalf("BuildPostFenceReceiptPurgeJob() = %v", err)
	}
	attempts := planned
	attempts.Status = ReceiptPurgeJobAttemptsPurging
	attempts.Revision = 2
	attempts.UpdatedAt = checkedAt.Add(time.Second)
	attemptsWithProgress := attempts
	attemptsWithProgress.Revision = 3
	attemptsWithProgress.AttemptCursor = "018f1f4e-2f5e-7d31-8c77-43b50f4c91ac"
	attemptsWithProgress.AttemptDeletedCount = 1
	linked := attemptsWithProgress
	linked.Status = ReceiptPurgeJobLinkedDocumentsPurging
	linked.Revision = 4
	linked.UpdatedAt = checkedAt.Add(2 * time.Second)
	linked.LinkCursor = "77777777-7777-4777-8777-777777777777"
	linked.TargetDeletedCount = 1
	ready := linked
	ready.Status = ReceiptPurgeJobReady
	ready.Revision = 5
	ready.VerifiedEmptyAt = checkedAt.Add(3 * time.Second)
	ready.UpdatedAt = ready.VerifiedEmptyAt
	deleted := ready
	deleted.Status = ReceiptPurgeJobLinkageDeleted
	deleted.Revision = 6
	deleted.LinkageDeletedAt = checkedAt.Add(4 * time.Second)
	deleted.PurgeJobExpiresAt = checkedAt.Add(7 * 24 * time.Hour)
	deleted.UpdatedAt = deleted.LinkageDeletedAt
	hold := linked
	hold.Status = ReceiptPurgeJobHold
	hold.Revision = 5
	hold.HeldFromStatus = ReceiptPurgeJobLinkedDocumentsPurging
	hold.ErrorClass = ReceiptPurgeErrorChildMalformed
	hold.UpdatedAt = checkedAt.Add(3 * time.Second)

	for _, job := range []ReceiptPurgeJob{
		planned, attempts, attemptsWithProgress, linked, ready, deleted, hold,
	} {
		t.Run(string(job.Status)+"/"+string(job.HeldFromStatus), func(t *testing.T) {
			if err := ValidateReceiptPurgeJob(job); err != nil {
				t.Fatalf("valid job rejected: %#v: %v", job, err)
			}
		})
	}
}

func TestValidateReceiptPurgeJobRejectsEnumResidueAndMonotonicityDrift(t *testing.T) {
	state, checkedAt := receiptPurgeAdmissionFixture(t)
	command, _ := BuildReceiptPurgeAdmissionCommand(state, checkedAt)
	valid, _ := BuildPostFenceReceiptPurgeJob(command, checkedAt)
	mutations := []struct {
		name   string
		mutate func(*ReceiptPurgeJob)
	}{
		{name: "schema", mutate: func(value *ReceiptPurgeJob) { value.SchemaVersion = "future" }},
		{name: "policy", mutate: func(value *ReceiptPurgeJob) { value.PolicyVersion = "future" }},
		{name: "unknown status", mutate: func(value *ReceiptPurgeJob) { value.Status = "unknown" }},
		{name: "negative count", mutate: func(value *ReceiptPurgeJob) { value.AttemptDeletedCount = -1 }},
		{name: "cursor without count", mutate: func(value *ReceiptPurgeJob) { value.AttemptCursor = "cursor" }},
		{name: "count without cursor", mutate: func(value *ReceiptPurgeJob) { value.AttemptDeletedCount = 1 }},
		{name: "planned future residue", mutate: func(value *ReceiptPurgeJob) {
			value.VerifiedEmptyAt = value.UpdatedAt
		}},
		{name: "timestamp regression", mutate: func(value *ReceiptPurgeJob) {
			value.UpdatedAt = value.CreatedAt.Add(-time.Nanosecond)
		}},
		{name: "hold residue on active", mutate: func(value *ReceiptPurgeJob) {
			value.HeldFromStatus = ReceiptPurgeJobPlanned
			value.ErrorClass = ReceiptPurgeErrorLinkageDrift
		}},
		{name: "hold unknown class", mutate: func(value *ReceiptPurgeJob) {
			value.Status = ReceiptPurgeJobHold
			value.Revision = 2
			value.HeldFromStatus = ReceiptPurgeJobPlanned
			value.ErrorClass = "provider payload"
		}},
		{name: "hold from terminal", mutate: func(value *ReceiptPurgeJob) {
			value.Status = ReceiptPurgeJobHold
			value.Revision = 6
			value.HeldFromStatus = ReceiptPurgeJobLinkageDeleted
			value.ErrorClass = ReceiptPurgeErrorLinkageDrift
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			job := valid
			test.mutate(&job)
			if !errors.Is(ValidateReceiptPurgeJob(job), ErrInvalidReceiptPurgeJob) {
				t.Fatalf("invalid job accepted: %#v", job)
			}
		})
	}
}

func TestReceiptPurgeAdmissionBuildsPostFencePlannedJob(t *testing.T) {
	state, checkedAt := receiptPurgeAdmissionFixture(t)
	command, err := BuildReceiptPurgeAdmissionCommand(state, checkedAt)
	if err != nil || ValidateReceiptPurgeAdmissionCommand(command) != nil {
		t.Fatalf("BuildReceiptPurgeAdmissionCommand() = %#v, %v", command, err)
	}
	job, err := BuildPostFenceReceiptPurgeJob(command, checkedAt)
	if err != nil || job.ReceiptRevision != state.Receipt.Revision+1 ||
		job.Status != ReceiptPurgeJobPlanned || job.Revision != 1 ||
		job.PurgeKey != command.PurgeKey || job.LinkageHash != command.LinkageHash ||
		!job.CreatedAt.Equal(checkedAt) || !job.UpdatedAt.Equal(checkedAt) {
		t.Fatalf("BuildPostFenceReceiptPurgeJob() = %#v, %v", job, err)
	}
	query, err := BuildReceiptPurgeAdmissionOutcomeQuery(command, checkedAt)
	if err != nil || query.ExpectedPostReceiptRevision != job.ReceiptRevision ||
		!query.ExpectedPurgeStartedAt.Equal(checkedAt) {
		t.Fatalf("BuildReceiptPurgeAdmissionOutcomeQuery() = %#v, %v", query, err)
	}
}

func TestReceiptPurgeAdmissionRejectsIneligibleOrDriftedState(t *testing.T) {
	valid, checkedAt := receiptPurgeAdmissionFixture(t)
	mutations := []struct {
		name   string
		mutate func(*ReceiptPurgeAdmissionState)
	}{
		{name: "not expired", mutate: func(value *ReceiptPurgeAdmissionState) {
			value.Receipt.State = ReceiptCleanupPending
		}},
		{name: "lease", mutate: func(value *ReceiptPurgeAdmissionState) { value.Receipt.LeasePresent = true }},
		{name: "forward cursor", mutate: func(value *ReceiptPurgeAdmissionState) {
			value.Receipt.ForwardCursorPresent = true
		}},
		{name: "cleanup cursor", mutate: func(value *ReceiptPurgeAdmissionState) {
			value.Receipt.CleanupCursorPresent = true
		}},
		{name: "partial fence", mutate: func(value *ReceiptPurgeAdmissionState) {
			value.Receipt.Fence.PurgeJobID = strings.Repeat("e", 64)
		}},
		{name: "reservation index drift", mutate: func(value *ReceiptPurgeAdmissionState) {
			value.ReservationIndex.ReceiptID = "33333333-3333-4333-8333-333333333333"
		}},
		{name: "client index time drift", mutate: func(value *ReceiptPurgeAdmissionState) {
			value.ClientBatchIndex.PurgeEligibleAt = value.ClientBatchIndex.PurgeEligibleAt.Add(time.Nanosecond)
		}},
		{name: "policy drift", mutate: func(value *ReceiptPurgeAdmissionState) {
			value.Receipt.PurgeEligibleAt = value.Receipt.PurgeEligibleAt.Add(time.Nanosecond)
			value.ReservationIndex.PurgeEligibleAt = value.Receipt.PurgeEligibleAt
			value.ClientBatchIndex.PurgeEligibleAt = value.Receipt.PurgeEligibleAt
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			state := valid
			test.mutate(&state)
			if _, err := BuildReceiptPurgeAdmissionCommand(state, checkedAt); !errors.Is(
				err, ErrInvalidReceiptPurgeAdmission,
			) {
				t.Fatalf("invalid state error = %v", err)
			}
		})
	}
	if _, err := BuildReceiptPurgeAdmissionCommand(valid, valid.Receipt.PurgeEligibleAt.Add(-time.Nanosecond)); !errors.Is(err, ErrInvalidReceiptPurgeAdmission) {
		t.Fatalf("early admission error = %v", err)
	}
}

func TestEvaluateReceiptPurgeAdmissionOutcomeClassifiesExactStates(t *testing.T) {
	state, checkedAt := receiptPurgeAdmissionFixture(t)
	command, _ := BuildReceiptPurgeAdmissionCommand(state, checkedAt)
	query, _ := BuildReceiptPurgeAdmissionOutcomeQuery(command, checkedAt)

	pre := ReceiptPurgeAdmissionOutcomeSnapshot{
		ReceiptPresent: true, ReservationIndexPresent: true, ClientBatchIndexPresent: true,
		Receipt: state.Receipt, ReservationIndex: state.ReservationIndex,
		ClientBatchIndex: state.ClientBatchIndex, ReadTime: checkedAt,
	}
	outcome, err := EvaluateReceiptPurgeAdmissionOutcome(query, pre, checkedAt)
	if err != nil || outcome.CommitStatus != ReceiptPurgeAdmissionNotCommitted ||
		outcome.ReceiptRevision != command.ExpectedPreReceiptRevision {
		t.Fatalf("pre outcome = %#v, %v", outcome, err)
	}

	committed := pre
	committed.JobPresent = true
	committed.Job, _ = BuildPostFenceReceiptPurgeJob(command, checkedAt)
	committed.Receipt.Revision = query.ExpectedPostReceiptRevision
	committed.Receipt.UpdatedAt = checkedAt
	committed.Receipt.Fence = ReceiptPurgeFence{
		PurgeJobID: command.PurgeKey, StartedAt: checkedAt, Version: ReceiptPurgeFenceVersion,
	}
	outcome, err = EvaluateReceiptPurgeAdmissionOutcome(query, committed, checkedAt)
	if err != nil || outcome.CommitStatus != ReceiptPurgeAdmissionCommitted ||
		outcome.JobStatus != ReceiptPurgeJobPlanned || outcome.JobRevision != 1 ||
		!outcome.PurgeStartedAt.Equal(checkedAt) {
		t.Fatalf("committed outcome = %#v, %v", outcome, err)
	}
	progressed := committed
	progressed.Job.Status = ReceiptPurgeJobAttemptsPurging
	progressed.Job.Revision = 2
	outcome, err = EvaluateReceiptPurgeAdmissionOutcome(query, progressed, checkedAt)
	if err != nil || outcome.CommitStatus != ReceiptPurgeAdmissionCommitted ||
		outcome.JobStatus != ReceiptPurgeJobAttemptsPurging || outcome.JobRevision != 2 {
		t.Fatalf("progressed committed outcome = %#v, %v", outcome, err)
	}

	partial := pre
	partial.Receipt.Fence.PurgeJobID = command.PurgeKey
	outcome, err = EvaluateReceiptPurgeAdmissionOutcome(query, partial, checkedAt)
	if err != nil || outcome.CommitStatus != ReceiptPurgeAdmissionUnverifiable {
		t.Fatalf("partial outcome = %#v, %v", outcome, err)
	}

	malformedJob := committed
	malformedJob.Job.SchemaVersion = "future"
	if _, err := EvaluateReceiptPurgeAdmissionOutcome(query, malformedJob, checkedAt); !errors.Is(
		err, ErrReceiptPurgeAdmissionOutcomeUnavailable,
	) {
		t.Fatalf("malformed job outcome error = %v", err)
	}
	missingIndex := pre
	missingIndex.ClientBatchIndexPresent = false
	if _, err := EvaluateReceiptPurgeAdmissionOutcome(query, missingIndex, checkedAt); !errors.Is(
		err, ErrReceiptPurgeAdmissionOutcomeUnavailable,
	) {
		t.Fatalf("missing linkage outcome error = %v", err)
	}
}

func TestBuildReceiptPurgePageObservationSeparatesLookahead(t *testing.T) {
	state, readAt := receiptPurgeAdmissionFixture(t)
	key, _ := DeriveReceiptPurgeKey(state.Receipt.TenantID, state.Receipt.ReceiptID)
	request := ReceiptPurgePageRequest{
		PurgeKey: key, TenantID: state.Receipt.TenantID, ReceiptID: state.Receipt.ReceiptID,
		Kind: ReceiptPurgePageAttempts, ExpectedJobStatus: ReceiptPurgeJobAttemptsPurging,
		ExpectedJobRevision: 2, PageSize: 2,
	}
	ids := []string{"attempt-001", "attempt-002", "attempt-003"}
	observation, err := BuildReceiptPurgePageObservation(request, ids, readAt)
	if err != nil || len(observation.DeleteDocumentIDs) != 2 ||
		observation.DeleteDocumentIDs[0] != ids[0] || observation.DeleteDocumentIDs[1] != ids[1] ||
		observation.LookaheadDocumentID != ids[2] || !observation.ReadAt.Equal(readAt) {
		t.Fatalf("BuildReceiptPurgePageObservation() = %#v, %v", observation, err)
	}
	ids[0] = "caller-mutated"
	if observation.DeleteDocumentIDs[0] != "attempt-001" {
		t.Fatal("page observation retained caller-owned slice")
	}
	withoutLookahead, err := BuildReceiptPurgePageObservation(
		request, []string{"attempt-001"}, readAt,
	)
	if err != nil || len(withoutLookahead.DeleteDocumentIDs) != 1 ||
		withoutLookahead.LookaheadDocumentID != "" {
		t.Fatalf("short page observation = %#v, %v", withoutLookahead, err)
	}
}

func TestReceiptPurgePageContractRejectsUnboundedOrUnorderedInput(t *testing.T) {
	state, readAt := receiptPurgeAdmissionFixture(t)
	key, _ := DeriveReceiptPurgeKey(state.Receipt.TenantID, state.Receipt.ReceiptID)
	valid := ReceiptPurgePageRequest{
		PurgeKey: key, TenantID: state.Receipt.TenantID, ReceiptID: state.Receipt.ReceiptID,
		Kind: ReceiptPurgePageLinks, ExpectedJobStatus: ReceiptPurgeJobLinkedDocumentsPurging,
		ExpectedJobRevision: 3, AfterDocumentID: "link-000", PageSize: 2,
	}
	requestMutations := []struct {
		name   string
		mutate func(*ReceiptPurgePageRequest)
	}{
		{name: "zero page", mutate: func(value *ReceiptPurgePageRequest) { value.PageSize = 0 }},
		{name: "oversized page", mutate: func(value *ReceiptPurgePageRequest) { value.PageSize = ReceiptPurgeMaxPageSize + 1 }},
		{name: "wrong status", mutate: func(value *ReceiptPurgePageRequest) { value.ExpectedJobStatus = ReceiptPurgeJobAttemptsPurging }},
		{name: "unknown kind", mutate: func(value *ReceiptPurgePageRequest) { value.Kind = "provider" }},
		{name: "path cursor", mutate: func(value *ReceiptPurgePageRequest) { value.AfterDocumentID = "links/child" }},
	}
	for _, test := range requestMutations {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if !errors.Is(ValidateReceiptPurgePageRequest(request), ErrInvalidReceiptPurgeJob) {
				t.Fatalf("invalid request accepted: %#v", request)
			}
		})
	}
	invalidIDs := [][]string{
		{"link-002", "link-001"},
		{"link-001", "link-001"},
		{"link-000"},
		{"link-001", "link-002", "link-003", "link-004"},
		{"links/child"},
	}
	for _, ids := range invalidIDs {
		if _, err := BuildReceiptPurgePageObservation(valid, ids, readAt); !errors.Is(err, ErrInvalidReceiptPurgeJob) {
			t.Fatalf("invalid ordered IDs accepted: %#v", ids)
		}
	}
	invalidObservation := ReceiptPurgePageObservation{
		Request: valid, DeleteDocumentIDs: []string{"link-001"},
		LookaheadDocumentID: "link-002", ReadAt: readAt,
	}
	if !errors.Is(ValidateReceiptPurgePageObservation(invalidObservation), ErrInvalidReceiptPurgeJob) {
		t.Fatal("lookahead on a short page was accepted")
	}
}

func TestReceiptPurgePublicContractHasNoSensitiveSurface(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(ReceiptPurgeJob{}), reflect.TypeOf(ReceiptPurgeFence{}),
		reflect.TypeOf(ReceiptPurgeReceiptState{}), reflect.TypeOf(ReceiptPurgeIndexState{}),
		reflect.TypeOf(ReceiptPurgeLinkage{}), reflect.TypeOf(ReceiptPurgeAdmissionCommand{}),
		reflect.TypeOf(ReceiptPurgeAdmissionOutcomeQuery{}), reflect.TypeOf(ReceiptPurgeAdmissionResult{}),
		reflect.TypeOf(ReceiptPurgeAdmissionOutcomeSnapshot{}), reflect.TypeOf(ReceiptPurgeAdmissionOutcome{}),
		reflect.TypeOf(ReceiptPurgePageRequest{}), reflect.TypeOf(ReceiptPurgePageObservation{}),
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

func receiptPurgeAdmissionFixture(t *testing.T) (ReceiptPurgeAdmissionState, time.Time) {
	t.Helper()
	completedAt := time.Date(2026, time.July, 23, 10, 0, 0, 0, time.UTC)
	retentionFloor := completedAt.Add(time.Hour)
	purgeEligibleAt, err := CleanupPurgeEligibleAt(retentionFloor, completedAt)
	if err != nil {
		t.Fatalf("CleanupPurgeEligibleAt() = %v", err)
	}
	receipt := ReceiptPurgeReceiptState{
		TenantID:       "11111111-1111-4111-8111-111111111111",
		ReceiptID:      "22222222-2222-4222-8222-222222222222",
		ReservationKey: strings.Repeat("a", 64), ClientBatchKey: strings.Repeat("b", 64),
		State: ReceiptExpired, Revision: 8, UpdatedAt: completedAt,
		ReceiptRetentionFloor: retentionFloor, PurgeEligibleAt: purgeEligibleAt,
	}
	index := func(documentID string) ReceiptPurgeIndexState {
		return ReceiptPurgeIndexState{
			DocumentID: documentID, TenantID: receipt.TenantID, ReceiptID: receipt.ReceiptID,
			ReservationKey: receipt.ReservationKey, ClientBatchKey: receipt.ClientBatchKey,
			PurgeEligibleAt: purgeEligibleAt,
		}
	}
	return ReceiptPurgeAdmissionState{
		Receipt: receipt, ReservationIndex: index(receipt.ReservationKey),
		ClientBatchIndex: index(receipt.ClientBatchKey),
	}, purgeEligibleAt
}
