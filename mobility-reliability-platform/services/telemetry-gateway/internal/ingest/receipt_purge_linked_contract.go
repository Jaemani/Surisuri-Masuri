package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"time"
)

const receiptPurgeLinkedSetDigestVersion = "ingest-receipt-purge-linked-set@1"

// ReceiptPurgeLinkedChildState is the exact, bounded pre-delete state for one
// inverse link and its top-level child. This first R8k-c increment accepts
// cleanup targets only. Integrity findings remain unsupported until their
// owning package defines a strict immutable child codec.
type ReceiptPurgeLinkedChildState struct {
	LinkDocumentID      string
	LinkDocumentDigest  string
	Link                ReceiptPurgeInverseLink
	ChildDocumentDigest string
	Child               ReceiptPurgeLinkedChildIdentity
}

type ReceiptPurgeLinkedMutationKind string

const (
	ReceiptPurgeLinkedMutationPage ReceiptPurgeLinkedMutationKind = "linked_page"
	ReceiptPurgeLinkedMutationHold ReceiptPurgeLinkedMutationKind = "linked_hold"
)

type ReceiptPurgeLinkedMutationPlan struct {
	Kind            ReceiptPurgeLinkedMutationKind
	PreJob          ReceiptPurgeJob
	NextJob         ReceiptPurgeJob
	Children        []ReceiptPurgeLinkedChildState
	DeleteSetDigest string
	OutcomeQuery    ReceiptPurgeLinkedMutationOutcomeQuery
}

type ReceiptPurgeLinkedMutationOutcomeQuery struct {
	Kind            ReceiptPurgeLinkedMutationKind
	PreJob          ReceiptPurgeJob
	NextJob         ReceiptPurgeJob
	Children        []ReceiptPurgeLinkedChildState
	DeleteSetDigest string
}

type ReceiptPurgeLinkedChildOutcomeObservation struct {
	LinkDocumentID string
	LinkPresent    bool
	ChildPresent   bool
	Invalid        bool
	State          ReceiptPurgeLinkedChildState
}

type ReceiptPurgeLinkedMutationOutcomeSnapshot struct {
	JobPresent     bool
	ReceiptPresent bool
	Job            ReceiptPurgeJob
	Receipt        ReceiptPurgeReceiptState
	Children       []ReceiptPurgeLinkedChildOutcomeObservation
	ReadTime       time.Time
}

type ReceiptPurgeLinkedMutationOutcome struct {
	PurgeKey            string
	CommitStatus        ReceiptPurgeMutationCommitStatus
	JobStatus           ReceiptPurgeJobStatus
	JobRevision         int64
	LinkCursor          string
	TargetDeletedCount  int64
	FindingDeletedCount int64
}

// ReceiptPurgeLinkedMutationResult preserves the sealed pre-state even when
// the adapter cannot observe the transaction response. Status remains empty
// on that ambiguous path.
type ReceiptPurgeLinkedMutationResult struct {
	Job          ReceiptPurgeJob
	OutcomeQuery ReceiptPurgeLinkedMutationOutcomeQuery
	Status       ReceiptPurgeMutationStatus
}

func ValidateReceiptPurgeLinkedChildState(state ReceiptPurgeLinkedChildState) error {
	if state.LinkDocumentID != state.Link.LinkID ||
		!isLowerHexDigest(state.LinkDocumentDigest) ||
		!isLowerHexDigest(state.ChildDocumentDigest) ||
		ValidateReceiptPurgeInverseLinkContext(
			state.Link,
			state.LinkDocumentID,
			state.Child.TenantID,
			state.Child.ReceiptID,
		) != nil ||
		ValidateReceiptPurgeInverseLinkPair(state.Link, state.Child) != nil ||
		state.Link.Kind != ReceiptPurgeLinkCleanupTarget ||
		state.Child.Kind != ReceiptPurgeLinkCleanupTarget {
		return ErrInvalidReceiptPurgeMutation
	}
	return nil
}

func ReceiptPurgeLinkedSetDigest(children []ReceiptPurgeLinkedChildState) (string, error) {
	if len(children) < 1 || len(children) > ReceiptPurgeMaxPageSize {
		return "", ErrInvalidReceiptPurgeMutation
	}
	encoder := newArtifactBindingEncoder(receiptPurgeLinkedSetDigestVersion)
	previous := ""
	for _, child := range children {
		if ValidateReceiptPurgeLinkedChildState(child) != nil ||
			(previous != "" && child.LinkDocumentID <= previous) {
			return "", ErrInvalidReceiptPurgeMutation
		}
		encoder.addString(child.LinkDocumentID)
		encoder.addString(child.LinkDocumentDigest)
		encoder.addString(child.Link.SchemaVersion)
		encoder.addString(child.Link.TenantID)
		encoder.addString(child.Link.ReceiptID)
		encoder.addString(string(child.Link.Kind))
		encoder.addString(child.Link.DocumentID)
		encoder.addTime(child.Link.CreatedAt.UTC())
		encoder.addString(child.ChildDocumentDigest)
		encoder.addString(child.Child.DocumentID)
		previous = child.LinkDocumentID
	}
	digest := encoder.sum()
	return hex.EncodeToString(digest[:]), nil
}

func PlanReceiptPurgeLinkedPage(
	job ReceiptPurgeJob,
	receipt ReceiptPurgeReceiptState,
	observation ReceiptPurgePageObservation,
	children []ReceiptPurgeLinkedChildState,
	observedAt time.Time,
) (ReceiptPurgeLinkedMutationPlan, error) {
	observedAt = observedAt.UTC()
	if validateReceiptPurgeMutationBinding(job, receipt) != nil ||
		ValidateReceiptPurgePageObservation(observation) != nil ||
		observation.Request.Kind != ReceiptPurgePageLinks ||
		observation.Request.PurgeKey != job.PurgeKey ||
		observation.Request.TenantID != job.TenantID ||
		observation.Request.ReceiptID != job.ReceiptID ||
		observation.Request.ExpectedJobStatus != job.Status ||
		observation.Request.ExpectedJobRevision != job.Revision ||
		observation.Request.AfterDocumentID != job.LinkCursor ||
		job.Status != ReceiptPurgeJobLinkedDocumentsPurging ||
		len(observation.DeleteDocumentIDs) == 0 ||
		len(children) != len(observation.DeleteDocumentIDs) ||
		!validCleanupFirestoreTimestamp(observedAt) ||
		observedAt.Before(observation.ReadAt) || observedAt.Before(job.UpdatedAt) ||
		job.Revision == math.MaxInt64 {
		return ReceiptPurgeLinkedMutationPlan{}, ErrReceiptPurgeMutationConflict
	}
	for index, child := range children {
		if ValidateReceiptPurgeLinkedChildState(child) != nil ||
			child.LinkDocumentID != observation.DeleteDocumentIDs[index] ||
			child.Link.TenantID != job.TenantID || child.Link.ReceiptID != job.ReceiptID {
			return ReceiptPurgeLinkedMutationPlan{}, ErrInvalidReceiptPurgeMutation
		}
	}
	digest, err := ReceiptPurgeLinkedSetDigest(children)
	if err != nil {
		return ReceiptPurgeLinkedMutationPlan{}, err
	}
	if job.TargetDeletedCount > math.MaxInt64-int64(len(children)) {
		return PlanReceiptPurgeLinkedHold(
			job,
			receipt,
			ReceiptPurgeErrorCountOverflow,
			observedAt,
		)
	}
	next := job
	next.LinkCursor = children[len(children)-1].LinkDocumentID
	next.TargetDeletedCount += int64(len(children))
	next.Revision++
	next.UpdatedAt = observedAt
	if ValidateReceiptPurgeJob(next) != nil {
		return ReceiptPurgeLinkedMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	return newReceiptPurgeLinkedMutationPlan(
		ReceiptPurgeLinkedMutationPage,
		job,
		next,
		children,
		digest,
	)
}

func PlanReceiptPurgeLinkedHold(
	job ReceiptPurgeJob,
	receipt ReceiptPurgeReceiptState,
	errorClass ReceiptPurgeErrorClass,
	observedAt time.Time,
) (ReceiptPurgeLinkedMutationPlan, error) {
	observedAt = observedAt.UTC()
	if ValidateReceiptPurgeJob(job) != nil ||
		job.Status != ReceiptPurgeJobLinkedDocumentsPurging ||
		!validReceiptPurgeErrorClass(errorClass) ||
		!validCleanupFirestoreTimestamp(observedAt) || observedAt.Before(job.UpdatedAt) ||
		job.Revision == math.MaxInt64 {
		return ReceiptPurgeLinkedMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	poisonClass := ReceiptPurgeMutationPoisonClass(job, receipt)
	if errorClass == ReceiptPurgeErrorFencePartial {
		if poisonClass != ReceiptPurgeErrorFencePartial {
			return ReceiptPurgeLinkedMutationPlan{}, ErrInvalidReceiptPurgeMutation
		}
	} else if errorClass == ReceiptPurgeErrorLinkageDrift {
		if poisonClass != ReceiptPurgeErrorLinkageDrift &&
			(poisonClass != "" || validateReceiptPurgeReceiptCore(job, receipt) != nil ||
				validateReceiptPurgeMutationFence(job, receipt.Fence) != nil) {
			return ReceiptPurgeLinkedMutationPlan{}, ErrInvalidReceiptPurgeMutation
		}
	} else if poisonClass != "" || validateReceiptPurgeReceiptCore(job, receipt) != nil ||
		validateReceiptPurgeMutationFence(job, receipt.Fence) != nil {
		return ReceiptPurgeLinkedMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	next := job
	next.Status = ReceiptPurgeJobHold
	next.Revision++
	next.UpdatedAt = observedAt
	next.HeldFromStatus = job.Status
	next.ErrorClass = errorClass
	if ValidateReceiptPurgeJob(next) != nil {
		return ReceiptPurgeLinkedMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	return newReceiptPurgeLinkedMutationPlan(
		ReceiptPurgeLinkedMutationHold,
		job,
		next,
		nil,
		"",
	)
}

func ValidateReceiptPurgeLinkedMutationOutcomeQuery(
	query ReceiptPurgeLinkedMutationOutcomeQuery,
) error {
	if ValidateReceiptPurgeJob(query.PreJob) != nil || ValidateReceiptPurgeJob(query.NextJob) != nil ||
		!sameReceiptPurgeImmutableBinding(query.PreJob, query.NextJob) ||
		query.PreJob.Revision == math.MaxInt64 || query.NextJob.Revision != query.PreJob.Revision+1 ||
		query.NextJob.UpdatedAt.Before(query.PreJob.UpdatedAt) {
		return ErrInvalidReceiptPurgeMutation
	}
	switch query.Kind {
	case ReceiptPurgeLinkedMutationPage:
		if query.PreJob.Status != ReceiptPurgeJobLinkedDocumentsPurging ||
			query.NextJob.Status != query.PreJob.Status ||
			len(query.Children) < 1 || len(query.Children) > ReceiptPurgeMaxPageSize ||
			!isLowerHexDigest(query.DeleteSetDigest) ||
			query.NextJob.LinkCursor != query.Children[len(query.Children)-1].LinkDocumentID ||
			query.NextJob.TargetDeletedCount != query.PreJob.TargetDeletedCount+int64(len(query.Children)) ||
			query.NextJob.FindingDeletedCount != query.PreJob.FindingDeletedCount ||
			query.NextJob.AttemptCursor != query.PreJob.AttemptCursor ||
			query.NextJob.AttemptDeletedCount != query.PreJob.AttemptDeletedCount ||
			validateReceiptPurgeLinkedChildren(query.Children, query.PreJob.LinkCursor) != nil {
			return ErrInvalidReceiptPurgeMutation
		}
		digest, err := ReceiptPurgeLinkedSetDigest(query.Children)
		if err != nil || digest != query.DeleteSetDigest {
			return ErrInvalidReceiptPurgeMutation
		}
	case ReceiptPurgeLinkedMutationHold:
		if query.PreJob.Status != ReceiptPurgeJobLinkedDocumentsPurging ||
			query.NextJob.Status != ReceiptPurgeJobHold ||
			query.NextJob.HeldFromStatus != query.PreJob.Status ||
			!validReceiptPurgeErrorClass(query.NextJob.ErrorClass) ||
			len(query.Children) != 0 || query.DeleteSetDigest != "" ||
			!sameReceiptPurgeProgressResidue(query.PreJob, query.NextJob) {
			return ErrInvalidReceiptPurgeMutation
		}
	default:
		return ErrInvalidReceiptPurgeMutation
	}
	return nil
}

func EvaluateReceiptPurgeLinkedMutationOutcome(
	query ReceiptPurgeLinkedMutationOutcomeQuery,
	snapshot ReceiptPurgeLinkedMutationOutcomeSnapshot,
	observedAt time.Time,
) (ReceiptPurgeLinkedMutationOutcome, error) {
	observedAt = observedAt.UTC()
	if ValidateReceiptPurgeLinkedMutationOutcomeQuery(query) != nil ||
		!snapshot.JobPresent || !snapshot.ReceiptPresent ||
		ValidateReceiptPurgeJob(snapshot.Job) != nil ||
		!validCleanupFirestoreTimestamp(snapshot.ReadTime.UTC()) ||
		!validCleanupFirestoreTimestamp(observedAt) || observedAt.Before(snapshot.ReadTime) ||
		validateReceiptPurgeLinkedOutcomeReceipt(query, snapshot.Receipt) != nil {
		return ReceiptPurgeLinkedMutationOutcome{}, ErrReceiptPurgeMutationOutcomeUnavailable
	}
	outcome := ReceiptPurgeLinkedMutationOutcome{
		PurgeKey:            snapshot.Job.PurgeKey,
		JobStatus:           snapshot.Job.Status,
		JobRevision:         snapshot.Job.Revision,
		LinkCursor:          snapshot.Job.LinkCursor,
		TargetDeletedCount:  snapshot.Job.TargetDeletedCount,
		FindingDeletedCount: snapshot.Job.FindingDeletedCount,
	}
	preChildren, committedChildren, childErr := receiptPurgeLinkedMutationOutcomeState(
		query,
		snapshot.Children,
	)
	if childErr != nil {
		return ReceiptPurgeLinkedMutationOutcome{}, childErr
	}
	if sameReceiptPurgeJob(snapshot.Job, query.NextJob) && committedChildren {
		outcome.CommitStatus = ReceiptPurgeMutationCommitted
		return outcome, nil
	}
	if sameReceiptPurgeJob(snapshot.Job, query.PreJob) && preChildren {
		outcome.CommitStatus = ReceiptPurgeMutationNotCommitted
		return outcome, nil
	}
	outcome.CommitStatus = ReceiptPurgeMutationUnverifiable
	return outcome, nil
}

func ReceiptPurgeLinkedMutationBinding(query ReceiptPurgeLinkedMutationOutcomeQuery) (string, error) {
	if ValidateReceiptPurgeLinkedMutationOutcomeQuery(query) != nil {
		return "", ErrInvalidReceiptPurgeMutation
	}
	encoder := newArtifactBindingEncoder("ingest-receipt-purge-linked-mutation@1")
	encoder.addString(string(query.Kind))
	encoder.addString(query.PreJob.PurgeKey)
	encoder.addString(strconv.FormatInt(query.PreJob.Revision, 10))
	encoder.addString(strconv.FormatInt(query.NextJob.Revision, 10))
	encoder.addString(query.DeleteSetDigest)
	for _, child := range query.Children {
		encoder.addString(child.LinkDocumentID)
		encoder.addString(child.LinkDocumentDigest)
		encoder.addString(child.ChildDocumentDigest)
	}
	binding := encoder.sum()
	digest := sha256.Sum256(binding[:])
	return hex.EncodeToString(digest[:]), nil
}

func newReceiptPurgeLinkedMutationPlan(
	kind ReceiptPurgeLinkedMutationKind,
	preJob ReceiptPurgeJob,
	nextJob ReceiptPurgeJob,
	children []ReceiptPurgeLinkedChildState,
	deleteSetDigest string,
) (ReceiptPurgeLinkedMutationPlan, error) {
	query := ReceiptPurgeLinkedMutationOutcomeQuery{
		Kind:            kind,
		PreJob:          preJob,
		NextJob:         nextJob,
		Children:        append([]ReceiptPurgeLinkedChildState(nil), children...),
		DeleteSetDigest: deleteSetDigest,
	}
	if ValidateReceiptPurgeLinkedMutationOutcomeQuery(query) != nil {
		return ReceiptPurgeLinkedMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	return ReceiptPurgeLinkedMutationPlan{
		Kind:            kind,
		PreJob:          preJob,
		NextJob:         nextJob,
		Children:        append([]ReceiptPurgeLinkedChildState(nil), children...),
		DeleteSetDigest: deleteSetDigest,
		OutcomeQuery:    query,
	}, nil
}

func validateReceiptPurgeLinkedChildren(
	children []ReceiptPurgeLinkedChildState,
	after string,
) error {
	previous := after
	for _, child := range children {
		if ValidateReceiptPurgeLinkedChildState(child) != nil ||
			child.LinkDocumentID <= previous {
			return ErrInvalidReceiptPurgeMutation
		}
		previous = child.LinkDocumentID
	}
	return nil
}

func receiptPurgeLinkedMutationOutcomeState(
	query ReceiptPurgeLinkedMutationOutcomeQuery,
	observations []ReceiptPurgeLinkedChildOutcomeObservation,
) (bool, bool, error) {
	if query.Kind != ReceiptPurgeLinkedMutationPage {
		if len(observations) != 0 {
			return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
		}
		return true, true, nil
	}
	if len(observations) != len(query.Children) {
		return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
	}
	allAbsent := true
	allExact := true
	for index, observation := range observations {
		expected := query.Children[index]
		if observation.LinkDocumentID != expected.LinkDocumentID {
			return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
		}
		if observation.Invalid {
			if observation.State != (ReceiptPurgeLinkedChildState{}) ||
				(!observation.LinkPresent && !observation.ChildPresent) {
				return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
			}
			allAbsent = false
			allExact = false
			continue
		}
		if !observation.LinkPresent && !observation.ChildPresent {
			if observation.State != (ReceiptPurgeLinkedChildState{}) {
				return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
			}
			allExact = false
			continue
		}
		allAbsent = false
		if !observation.LinkPresent || !observation.ChildPresent ||
			ValidateReceiptPurgeLinkedChildState(observation.State) != nil ||
			observation.State != expected {
			allExact = false
		}
	}
	return allExact, allAbsent, nil
}

func validateReceiptPurgeLinkedOutcomeReceipt(
	query ReceiptPurgeLinkedMutationOutcomeQuery,
	receipt ReceiptPurgeReceiptState,
) error {
	if query.Kind == ReceiptPurgeLinkedMutationHold &&
		query.NextJob.ErrorClass == ReceiptPurgeErrorFencePartial {
		if ReceiptPurgeMutationPoisonClass(query.PreJob, receipt) != ReceiptPurgeErrorFencePartial {
			return ErrReceiptPurgeMutationOutcomeUnavailable
		}
		return nil
	}
	if query.Kind == ReceiptPurgeLinkedMutationHold &&
		query.NextJob.ErrorClass == ReceiptPurgeErrorLinkageDrift {
		poisonClass := ReceiptPurgeMutationPoisonClass(query.PreJob, receipt)
		if poisonClass == ReceiptPurgeErrorLinkageDrift {
			return nil
		}
		if poisonClass != "" {
			return ErrReceiptPurgeMutationOutcomeUnavailable
		}
	}
	if validateReceiptPurgeReceiptCore(query.PreJob, receipt) != nil ||
		validateReceiptPurgeMutationFence(query.PreJob, receipt.Fence) != nil {
		return ErrReceiptPurgeMutationOutcomeUnavailable
	}
	return nil
}
