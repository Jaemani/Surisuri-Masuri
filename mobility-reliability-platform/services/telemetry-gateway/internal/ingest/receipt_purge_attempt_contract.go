package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	receiptPurgeAttemptSetDigestVersion = "ingest-receipt-purge-attempt-set@1"
	ReceiptPurgeEmptyObservationMaxAge  = 30 * time.Second
)

var (
	ErrInvalidReceiptPurgeMutation            = errors.New("receipt purge mutation is invalid")
	ErrReceiptPurgeMutationConflict           = errors.New("receipt purge mutation conflicts with durable state")
	ErrReceiptPurgeMutationUnavailable        = errors.New("receipt purge mutation is unavailable")
	ErrReceiptPurgeMutationOutcomeUnavailable = errors.New("receipt purge mutation outcome is unavailable")
)

// ReceiptPurgeAttemptState is the bounded projection needed to authorize an
// attempt delete. It deliberately excludes provider payloads, object paths,
// Firebase UID and device/trip/person identity.
type ReceiptPurgeAttemptState struct {
	DocumentID     string
	AttemptID      string
	TenantID       string
	ReceiptID      string
	OwnerKind      LeaseOwnerKind
	FencingToken   int64
	WorkerVersion  string
	DocumentDigest string
	Status         RecoveryAttemptStatus
	Outcome        RecoveryAttemptOutcome
	FailureCode    RecoveryAttemptFailureCode
	StartedAt      time.Time
	CompletedAt    time.Time
	FailedAt       time.Time
}

type ReceiptPurgeMutationKind string

const (
	ReceiptPurgeMutationAttemptPage          ReceiptPurgeMutationKind = "attempt_page"
	ReceiptPurgeMutationAttemptHold          ReceiptPurgeMutationKind = "attempt_hold"
	ReceiptPurgeMutationAttemptPhaseBegin    ReceiptPurgeMutationKind = "attempt_phase_begin"
	ReceiptPurgeMutationAttemptPhaseComplete ReceiptPurgeMutationKind = "attempt_phase_complete"
)

type ReceiptPurgeMutationPlan struct {
	Kind              ReceiptPurgeMutationKind
	PreJob            ReceiptPurgeJob
	NextJob           ReceiptPurgeJob
	DeleteDocumentIDs []string
	DeleteSetDigest   string
	OutcomeQuery      ReceiptPurgeMutationOutcomeQuery
}

type ReceiptPurgeMutationOutcomeQuery struct {
	Kind              ReceiptPurgeMutationKind
	PreJob            ReceiptPurgeJob
	NextJob           ReceiptPurgeJob
	DeleteDocumentIDs []string
	DeleteSetDigest   string
}

type ReceiptPurgeAttemptOutcomeObservation struct {
	DocumentID string
	Present    bool
	Invalid    bool
	Attempt    ReceiptPurgeAttemptState
}

type ReceiptPurgeMutationOutcomeSnapshot struct {
	JobPresent     bool
	ReceiptPresent bool
	Job            ReceiptPurgeJob
	Receipt        ReceiptPurgeReceiptState
	Attempts       []ReceiptPurgeAttemptOutcomeObservation
	ReadTime       time.Time
}

type ReceiptPurgeMutationCommitStatus string

const (
	ReceiptPurgeMutationCommitted    ReceiptPurgeMutationCommitStatus = "committed"
	ReceiptPurgeMutationNotCommitted ReceiptPurgeMutationCommitStatus = "not_committed"
	ReceiptPurgeMutationUnverifiable ReceiptPurgeMutationCommitStatus = "unverifiable"
)

type ReceiptPurgeMutationOutcome struct {
	PurgeKey            string
	CommitStatus        ReceiptPurgeMutationCommitStatus
	JobStatus           ReceiptPurgeJobStatus
	JobRevision         int64
	AttemptCursor       string
	AttemptDeletedCount int64
}

type ReceiptPurgeMutationStatus string

const (
	ReceiptPurgeMutationProgressed        ReceiptPurgeMutationStatus = "progressed"
	ReceiptPurgeMutationHeld              ReceiptPurgeMutationStatus = "held"
	ReceiptPurgeMutationPhaseTransitioned ReceiptPurgeMutationStatus = "phase_transitioned"
)

// ReceiptPurgeMutationResult preserves a sealed read-only outcome query when
// the adapter cannot observe the commit response. Status must remain empty on
// that ambiguous path.
type ReceiptPurgeMutationResult struct {
	Job          ReceiptPurgeJob
	OutcomeQuery ReceiptPurgeMutationOutcomeQuery
	Status       ReceiptPurgeMutationStatus
}

type ReceiptPurgeAttemptPhaseAction string

const (
	ReceiptPurgeAttemptPhaseBegin    ReceiptPurgeAttemptPhaseAction = "begin"
	ReceiptPurgeAttemptPhaseComplete ReceiptPurgeAttemptPhaseAction = "complete"
)

type ReceiptPurgeAttemptPhaseCommand struct {
	Action              ReceiptPurgeAttemptPhaseAction
	PurgeKey            string
	TenantID            string
	ReceiptID           string
	ExpectedJobRevision int64
	EmptyObservation    ReceiptPurgePageObservation
}

func ValidateReceiptPurgeAttemptState(attempt ReceiptPurgeAttemptState) error {
	if !safeRecoveryDocumentID(attempt.DocumentID, 1500) ||
		attempt.DocumentID != attempt.AttemptID || !telemetry.IsUUID(attempt.AttemptID) ||
		!telemetry.IsUUID(attempt.TenantID) || !telemetry.IsUUID(attempt.ReceiptID) ||
		attempt.FencingToken <= 0 || !validCleanupFirestoreTimestamp(attempt.StartedAt.UTC()) ||
		!validReceiptPurgeAttemptOwnerSchema(attempt.OwnerKind, attempt.WorkerVersion) ||
		!isLowerHexDigest(attempt.DocumentDigest) {
		return ErrInvalidReceiptPurgeMutation
	}
	switch attempt.Status {
	case RecoveryAttemptStarted:
		if attempt.Outcome != "" || attempt.FailureCode != "" ||
			!attempt.CompletedAt.IsZero() || !attempt.FailedAt.IsZero() {
			return ErrInvalidReceiptPurgeMutation
		}
	case RecoveryAttemptCompleted:
		if !validReceiptPurgeAttemptOutcome(attempt.OwnerKind, attempt.Outcome) || attempt.FailureCode != "" ||
			!validCleanupFirestoreTimestamp(attempt.CompletedAt.UTC()) ||
			!attempt.CompletedAt.After(attempt.StartedAt) || !attempt.FailedAt.IsZero() {
			return ErrInvalidReceiptPurgeMutation
		}
	case RecoveryAttemptFailed:
		if attempt.Outcome != "" || !ValidRecoveryAttemptFailureCode(attempt.FailureCode) ||
			!validCleanupFirestoreTimestamp(attempt.FailedAt.UTC()) ||
			!attempt.FailedAt.After(attempt.StartedAt) || !attempt.CompletedAt.IsZero() {
			return ErrInvalidReceiptPurgeMutation
		}
	default:
		return ErrInvalidReceiptPurgeMutation
	}
	return nil
}

func ReceiptPurgeAttemptSetDigest(attempts []ReceiptPurgeAttemptState) (string, error) {
	if len(attempts) < 1 || len(attempts) > ReceiptPurgeMaxPageSize {
		return "", ErrInvalidReceiptPurgeMutation
	}
	encoder := newArtifactBindingEncoder(receiptPurgeAttemptSetDigestVersion)
	previous := ""
	for _, attempt := range attempts {
		if ValidateReceiptPurgeAttemptState(attempt) != nil ||
			(previous != "" && attempt.DocumentID <= previous) {
			return "", ErrInvalidReceiptPurgeMutation
		}
		encoder.addString(attempt.DocumentID)
		encoder.addString(attempt.AttemptID)
		encoder.addString(attempt.TenantID)
		encoder.addString(attempt.ReceiptID)
		encoder.addString(string(attempt.OwnerKind))
		encoder.addInt64(attempt.FencingToken)
		encoder.addString(attempt.WorkerVersion)
		encoder.addString(attempt.DocumentDigest)
		encoder.addString(string(attempt.Status))
		encoder.addString(string(attempt.Outcome))
		encoder.addString(string(attempt.FailureCode))
		encoder.addTime(attempt.StartedAt.UTC())
		encoder.addTime(attempt.CompletedAt.UTC())
		encoder.addTime(attempt.FailedAt.UTC())
		previous = attempt.DocumentID
	}
	digest := encoder.sum()
	return hex.EncodeToString(digest[:]), nil
}

func PlanReceiptPurgeAttemptPage(
	job ReceiptPurgeJob,
	receipt ReceiptPurgeReceiptState,
	observation ReceiptPurgePageObservation,
	attempts []ReceiptPurgeAttemptState,
	observedAt time.Time,
) (ReceiptPurgeMutationPlan, error) {
	observedAt = observedAt.UTC()
	if validateReceiptPurgeMutationBinding(job, receipt) != nil ||
		ValidateReceiptPurgePageObservation(observation) != nil ||
		observation.Request.Kind != ReceiptPurgePageAttempts ||
		observation.Request.PurgeKey != job.PurgeKey ||
		observation.Request.TenantID != job.TenantID ||
		observation.Request.ReceiptID != job.ReceiptID ||
		observation.Request.ExpectedJobStatus != job.Status ||
		observation.Request.ExpectedJobRevision != job.Revision ||
		observation.Request.AfterDocumentID != job.AttemptCursor ||
		job.Status != ReceiptPurgeJobAttemptsPurging ||
		len(observation.DeleteDocumentIDs) == 0 || len(attempts) != len(observation.DeleteDocumentIDs) ||
		!validCleanupFirestoreTimestamp(observedAt) || observedAt.Before(observation.ReadAt) ||
		observedAt.Before(job.UpdatedAt) || job.Revision == math.MaxInt64 {
		return ReceiptPurgeMutationPlan{}, ErrReceiptPurgeMutationConflict
	}
	for index, attempt := range attempts {
		if ValidateReceiptPurgeAttemptState(attempt) != nil ||
			attempt.DocumentID != observation.DeleteDocumentIDs[index] ||
			attempt.TenantID != job.TenantID || attempt.ReceiptID != job.ReceiptID ||
			attempt.Status == RecoveryAttemptStarted {
			return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
		}
	}
	digest, err := ReceiptPurgeAttemptSetDigest(attempts)
	if err != nil {
		return ReceiptPurgeMutationPlan{}, err
	}
	if job.AttemptDeletedCount > math.MaxInt64-int64(len(attempts)) {
		return PlanReceiptPurgeAttemptHold(
			job, receipt, ReceiptPurgeErrorCountOverflow, observedAt,
		)
	}
	next := job
	next.AttemptCursor = attempts[len(attempts)-1].DocumentID
	next.AttemptDeletedCount += int64(len(attempts))
	next.Revision++
	next.UpdatedAt = observedAt
	if ValidateReceiptPurgeJob(next) != nil {
		return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	return newReceiptPurgeMutationPlan(
		ReceiptPurgeMutationAttemptPage, job, next, observation.DeleteDocumentIDs, digest,
	)
}

func PlanReceiptPurgeAttemptHold(
	job ReceiptPurgeJob,
	receipt ReceiptPurgeReceiptState,
	errorClass ReceiptPurgeErrorClass,
	observedAt time.Time,
) (ReceiptPurgeMutationPlan, error) {
	observedAt = observedAt.UTC()
	if ValidateReceiptPurgeJob(job) != nil ||
		(job.Status != ReceiptPurgeJobPlanned && job.Status != ReceiptPurgeJobAttemptsPurging) ||
		!validReceiptPurgeErrorClass(errorClass) ||
		!validCleanupFirestoreTimestamp(observedAt) || observedAt.Before(job.UpdatedAt) ||
		job.Revision == math.MaxInt64 {
		return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	poisonClass := ReceiptPurgeMutationPoisonClass(job, receipt)
	if errorClass == ReceiptPurgeErrorFencePartial {
		if poisonClass != ReceiptPurgeErrorFencePartial {
			return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
		}
	} else if errorClass == ReceiptPurgeErrorLinkageDrift {
		if poisonClass != ReceiptPurgeErrorLinkageDrift &&
			(poisonClass != "" || validateReceiptPurgeReceiptCore(job, receipt) != nil ||
				validateReceiptPurgeMutationFence(job, receipt.Fence) != nil) {
			return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
		}
	} else if poisonClass != "" || validateReceiptPurgeReceiptCore(job, receipt) != nil ||
		validateReceiptPurgeMutationFence(job, receipt.Fence) != nil {
		return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	next := job
	next.Status = ReceiptPurgeJobHold
	next.Revision++
	next.UpdatedAt = observedAt
	next.HeldFromStatus = job.Status
	next.ErrorClass = errorClass
	if ValidateReceiptPurgeJob(next) != nil {
		return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	return newReceiptPurgeMutationPlan(ReceiptPurgeMutationAttemptHold, job, next, nil, "")
}

func PlanReceiptPurgeAttemptPhase(
	job ReceiptPurgeJob,
	receipt ReceiptPurgeReceiptState,
	command ReceiptPurgeAttemptPhaseCommand,
	observedAt time.Time,
) (ReceiptPurgeMutationPlan, error) {
	observedAt = observedAt.UTC()
	if validateReceiptPurgeMutationBinding(job, receipt) != nil ||
		command.PurgeKey != job.PurgeKey || command.TenantID != job.TenantID ||
		command.ReceiptID != job.ReceiptID || command.ExpectedJobRevision != job.Revision ||
		!validCleanupFirestoreTimestamp(observedAt) || observedAt.Before(job.UpdatedAt) ||
		job.Revision == math.MaxInt64 {
		return ReceiptPurgeMutationPlan{}, ErrReceiptPurgeMutationConflict
	}
	next := job
	next.Revision++
	next.UpdatedAt = observedAt
	kind := ReceiptPurgeMutationAttemptPhaseBegin
	switch command.Action {
	case ReceiptPurgeAttemptPhaseBegin:
		if job.Status != ReceiptPurgeJobPlanned || !emptyReceiptPurgePageObservation(command.EmptyObservation) {
			return ReceiptPurgeMutationPlan{}, ErrReceiptPurgeMutationConflict
		}
		next.Status = ReceiptPurgeJobAttemptsPurging
	case ReceiptPurgeAttemptPhaseComplete:
		kind = ReceiptPurgeMutationAttemptPhaseComplete
		observation := command.EmptyObservation
		if job.Status != ReceiptPurgeJobAttemptsPurging ||
			ValidateReceiptPurgePageObservation(observation) != nil ||
			observation.Request.Kind != ReceiptPurgePageAttempts ||
			observation.Request.PurgeKey != job.PurgeKey ||
			observation.Request.TenantID != job.TenantID ||
			observation.Request.ReceiptID != job.ReceiptID ||
			observation.Request.ExpectedJobStatus != job.Status ||
			observation.Request.ExpectedJobRevision != job.Revision ||
			observation.Request.AfterDocumentID != job.AttemptCursor ||
			len(observation.DeleteDocumentIDs) != 0 || observation.LookaheadDocumentID != "" ||
			observedAt.Before(observation.ReadAt) ||
			observedAt.Sub(observation.ReadAt) > ReceiptPurgeEmptyObservationMaxAge {
			return ReceiptPurgeMutationPlan{}, ErrReceiptPurgeMutationConflict
		}
		next.Status = ReceiptPurgeJobLinkedDocumentsPurging
	default:
		return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	if ValidateReceiptPurgeJob(next) != nil {
		return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	return newReceiptPurgeMutationPlan(kind, job, next, nil, "")
}

func ValidateReceiptPurgeMutationOutcomeQuery(query ReceiptPurgeMutationOutcomeQuery) error {
	if ValidateReceiptPurgeJob(query.PreJob) != nil || ValidateReceiptPurgeJob(query.NextJob) != nil ||
		!sameReceiptPurgeImmutableBinding(query.PreJob, query.NextJob) ||
		query.PreJob.Revision == math.MaxInt64 || query.NextJob.Revision != query.PreJob.Revision+1 ||
		query.NextJob.UpdatedAt.Before(query.PreJob.UpdatedAt) {
		return ErrInvalidReceiptPurgeMutation
	}
	switch query.Kind {
	case ReceiptPurgeMutationAttemptPage:
		if query.PreJob.Status != ReceiptPurgeJobAttemptsPurging ||
			query.NextJob.Status != query.PreJob.Status || len(query.DeleteDocumentIDs) < 1 ||
			len(query.DeleteDocumentIDs) > ReceiptPurgeMaxPageSize || !isLowerHexDigest(query.DeleteSetDigest) ||
			query.NextJob.AttemptCursor != query.DeleteDocumentIDs[len(query.DeleteDocumentIDs)-1] ||
			query.NextJob.AttemptDeletedCount != query.PreJob.AttemptDeletedCount+int64(len(query.DeleteDocumentIDs)) ||
			query.NextJob.LinkCursor != query.PreJob.LinkCursor ||
			query.NextJob.TargetDeletedCount != query.PreJob.TargetDeletedCount ||
			query.NextJob.FindingDeletedCount != query.PreJob.FindingDeletedCount ||
			validateReceiptPurgeOrderedDocumentIDs(query.PreJob.AttemptCursor, query.DeleteDocumentIDs) != nil {
			return ErrInvalidReceiptPurgeMutation
		}
	case ReceiptPurgeMutationAttemptHold:
		if (query.PreJob.Status != ReceiptPurgeJobPlanned &&
			query.PreJob.Status != ReceiptPurgeJobAttemptsPurging) ||
			query.NextJob.Status != ReceiptPurgeJobHold ||
			query.NextJob.HeldFromStatus != query.PreJob.Status ||
			!validReceiptPurgeErrorClass(query.NextJob.ErrorClass) ||
			len(query.DeleteDocumentIDs) != 0 || query.DeleteSetDigest != "" ||
			!sameReceiptPurgeProgressResidue(query.PreJob, query.NextJob) {
			return ErrInvalidReceiptPurgeMutation
		}
	case ReceiptPurgeMutationAttemptPhaseBegin:
		if query.PreJob.Status != ReceiptPurgeJobPlanned ||
			query.NextJob.Status != ReceiptPurgeJobAttemptsPurging ||
			len(query.DeleteDocumentIDs) != 0 || query.DeleteSetDigest != "" ||
			!sameReceiptPurgeProgressResidue(query.PreJob, query.NextJob) {
			return ErrInvalidReceiptPurgeMutation
		}
	case ReceiptPurgeMutationAttemptPhaseComplete:
		if query.PreJob.Status != ReceiptPurgeJobAttemptsPurging ||
			query.NextJob.Status != ReceiptPurgeJobLinkedDocumentsPurging ||
			len(query.DeleteDocumentIDs) != 0 || query.DeleteSetDigest != "" ||
			!sameReceiptPurgeProgressResidue(query.PreJob, query.NextJob) {
			return ErrInvalidReceiptPurgeMutation
		}
	default:
		return ErrInvalidReceiptPurgeMutation
	}
	return nil
}

func EvaluateReceiptPurgeMutationOutcome(
	query ReceiptPurgeMutationOutcomeQuery,
	snapshot ReceiptPurgeMutationOutcomeSnapshot,
	observedAt time.Time,
) (ReceiptPurgeMutationOutcome, error) {
	observedAt = observedAt.UTC()
	if ValidateReceiptPurgeMutationOutcomeQuery(query) != nil || !snapshot.JobPresent ||
		!snapshot.ReceiptPresent || ValidateReceiptPurgeJob(snapshot.Job) != nil ||
		!validCleanupFirestoreTimestamp(snapshot.ReadTime.UTC()) ||
		!validCleanupFirestoreTimestamp(observedAt) || observedAt.Before(snapshot.ReadTime) ||
		validateReceiptPurgeMutationOutcomeReceipt(query, snapshot.Receipt) != nil {
		return ReceiptPurgeMutationOutcome{}, ErrReceiptPurgeMutationOutcomeUnavailable
	}
	outcome := ReceiptPurgeMutationOutcome{
		PurgeKey: snapshot.Job.PurgeKey, JobStatus: snapshot.Job.Status,
		JobRevision: snapshot.Job.Revision, AttemptCursor: snapshot.Job.AttemptCursor,
		AttemptDeletedCount: snapshot.Job.AttemptDeletedCount,
	}
	preAttempts, committedAttempts, attemptErr := receiptPurgeMutationAttemptOutcomeState(query, snapshot.Attempts)
	if attemptErr != nil {
		return ReceiptPurgeMutationOutcome{}, attemptErr
	}
	if sameReceiptPurgeJob(snapshot.Job, query.NextJob) && committedAttempts {
		outcome.CommitStatus = ReceiptPurgeMutationCommitted
		return outcome, nil
	}
	if sameReceiptPurgeJob(snapshot.Job, query.PreJob) && preAttempts {
		outcome.CommitStatus = ReceiptPurgeMutationNotCommitted
		return outcome, nil
	}
	outcome.CommitStatus = ReceiptPurgeMutationUnverifiable
	return outcome, nil
}

func validateReceiptPurgeMutationOutcomeReceipt(
	query ReceiptPurgeMutationOutcomeQuery,
	receipt ReceiptPurgeReceiptState,
) error {
	if query.Kind == ReceiptPurgeMutationAttemptHold &&
		query.NextJob.ErrorClass == ReceiptPurgeErrorFencePartial {
		if ReceiptPurgeMutationPoisonClass(query.PreJob, receipt) != ReceiptPurgeErrorFencePartial {
			return ErrReceiptPurgeMutationOutcomeUnavailable
		}
		return nil
	}
	if query.Kind == ReceiptPurgeMutationAttemptHold &&
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

func newReceiptPurgeMutationPlan(
	kind ReceiptPurgeMutationKind,
	preJob ReceiptPurgeJob,
	nextJob ReceiptPurgeJob,
	deleteDocumentIDs []string,
	deleteSetDigest string,
) (ReceiptPurgeMutationPlan, error) {
	query := ReceiptPurgeMutationOutcomeQuery{
		Kind: kind, PreJob: preJob, NextJob: nextJob,
		DeleteDocumentIDs: append([]string(nil), deleteDocumentIDs...),
		DeleteSetDigest:   deleteSetDigest,
	}
	if ValidateReceiptPurgeMutationOutcomeQuery(query) != nil {
		return ReceiptPurgeMutationPlan{}, ErrInvalidReceiptPurgeMutation
	}
	return ReceiptPurgeMutationPlan{
		Kind: kind, PreJob: preJob, NextJob: nextJob,
		DeleteDocumentIDs: append([]string(nil), deleteDocumentIDs...),
		DeleteSetDigest:   deleteSetDigest, OutcomeQuery: query,
	}, nil
}

func receiptPurgeMutationAttemptOutcomeState(
	query ReceiptPurgeMutationOutcomeQuery,
	observations []ReceiptPurgeAttemptOutcomeObservation,
) (bool, bool, error) {
	if query.Kind != ReceiptPurgeMutationAttemptPage {
		if len(observations) != 0 {
			return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
		}
		return true, true, nil
	}
	if len(observations) != len(query.DeleteDocumentIDs) {
		return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
	}
	allAbsent := true
	attempts := make([]ReceiptPurgeAttemptState, 0, len(observations))
	for index, observation := range observations {
		if observation.DocumentID != query.DeleteDocumentIDs[index] {
			return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
		}
		if !observation.Present {
			if observation.Invalid || observation.Attempt != (ReceiptPurgeAttemptState{}) {
				return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
			}
			continue
		}
		allAbsent = false
		if observation.Invalid {
			if observation.Attempt != (ReceiptPurgeAttemptState{}) {
				return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
			}
			continue
		}
		if observation.Attempt.DocumentID != observation.DocumentID ||
			ValidateReceiptPurgeAttemptState(observation.Attempt) != nil {
			return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
		}
		attempts = append(attempts, observation.Attempt)
	}
	if allAbsent {
		return false, true, nil
	}
	if len(attempts) != len(observations) {
		return false, false, nil
	}
	digest, err := ReceiptPurgeAttemptSetDigest(attempts)
	if err != nil {
		return false, false, ErrReceiptPurgeMutationOutcomeUnavailable
	}
	return digest == query.DeleteSetDigest, false, nil
}

func validateReceiptPurgeMutationBinding(job ReceiptPurgeJob, receipt ReceiptPurgeReceiptState) error {
	if ValidateReceiptPurgeJob(job) != nil || validateReceiptPurgeReceiptCore(job, receipt) != nil ||
		validateReceiptPurgeMutationFence(job, receipt.Fence) != nil {
		return ErrReceiptPurgeMutationConflict
	}
	return nil
}

// ReceiptPurgeMutationPoisonClass classifies only durable receipt/job
// structural drift. A transient query/read failure is not a poison class.
func ReceiptPurgeMutationPoisonClass(
	job ReceiptPurgeJob,
	receipt ReceiptPurgeReceiptState,
) ReceiptPurgeErrorClass {
	if ValidateReceiptPurgeJob(job) != nil {
		return ""
	}
	if !receiptPurgeFenceEmpty(receipt.Fence) && !receiptPurgeFenceFullyPopulated(receipt.Fence) {
		return ReceiptPurgeErrorFencePartial
	}
	if validateReceiptPurgeReceiptCore(job, receipt) != nil ||
		validateReceiptPurgeMutationFence(job, receipt.Fence) != nil {
		return ReceiptPurgeErrorLinkageDrift
	}
	return ""
}

func validateReceiptPurgeReceiptCore(job ReceiptPurgeJob, receipt ReceiptPurgeReceiptState) error {
	if !telemetry.IsUUID(receipt.TenantID) || !telemetry.IsUUID(receipt.ReceiptID) ||
		receipt.TenantID != job.TenantID || receipt.ReceiptID != job.ReceiptID ||
		receipt.State != ReceiptExpired || receipt.Revision != job.ReceiptRevision ||
		!receipt.UpdatedAt.Equal(job.CreatedAt) ||
		receipt.LeasePresent || receipt.ForwardCursorPresent || receipt.CleanupCursorPresent ||
		receipt.RecoveryHoldPresent || !validCleanupFirestoreTimestamp(receipt.PurgeEligibleAt.UTC()) {
		return ErrReceiptPurgeMutationConflict
	}
	linkageHash, err := ReceiptPurgeLinkageHash(ReceiptPurgeLinkage{
		TenantID: receipt.TenantID, ReceiptID: receipt.ReceiptID,
		ReservationKey: receipt.ReservationKey, ClientBatchKey: receipt.ClientBatchKey,
		ReservationIndexDocumentID: receipt.ReservationKey,
		ClientBatchIndexDocumentID: receipt.ClientBatchKey,
		PostFenceReceiptRevision:   receipt.Revision,
		PurgeEligibleAt:            receipt.PurgeEligibleAt.UTC(),
	})
	if err != nil || linkageHash != job.LinkageHash {
		return ErrReceiptPurgeMutationConflict
	}
	return nil
}

func validateReceiptPurgeMutationFence(job ReceiptPurgeJob, fence ReceiptPurgeFence) error {
	if !receiptPurgeFenceFullyPopulated(fence) || fence.PurgeJobID != job.PurgeKey ||
		fence.Version != ReceiptPurgeFenceVersion || !fence.StartedAt.Equal(job.CreatedAt) {
		return ErrReceiptPurgeMutationConflict
	}
	return nil
}

func receiptPurgeFenceFullyPopulated(fence ReceiptPurgeFence) bool {
	return fence.PurgeJobID != "" && !fence.StartedAt.IsZero() && fence.Version != ""
}

func validReceiptPurgeAttemptOwnerSchema(owner LeaseOwnerKind, workerVersion string) bool {
	switch owner {
	case LeaseOwnerCleanup:
		return workerVersion == CleanupWorkerVersion
	case LeaseOwnerRequest, LeaseOwnerSweeper:
		return workerVersion == RecoveryWorkerVersion
	default:
		return false
	}
}

func validReceiptPurgeAttemptOutcome(owner LeaseOwnerKind, outcome RecoveryAttemptOutcome) bool {
	if owner == LeaseOwnerCleanup {
		return outcome == RecoveryAttemptOutcomeExpired ||
			outcome == RecoveryAttemptOutcomeCleanupRetry || outcome == RecoveryAttemptOutcomeCleanupHold
	}
	return ValidRecoveryAttemptOutcome(outcome)
}

func sameReceiptPurgeImmutableBinding(left, right ReceiptPurgeJob) bool {
	return left.SchemaVersion == right.SchemaVersion && left.PolicyVersion == right.PolicyVersion &&
		left.PurgeKey == right.PurgeKey && left.TenantID == right.TenantID &&
		left.ReceiptID == right.ReceiptID && left.ReceiptRevision == right.ReceiptRevision &&
		left.LinkageHash == right.LinkageHash && left.CreatedAt.Equal(right.CreatedAt)
}

func sameReceiptPurgeProgressResidue(left, right ReceiptPurgeJob) bool {
	return left.AttemptCursor == right.AttemptCursor &&
		left.AttemptDeletedCount == right.AttemptDeletedCount &&
		left.LinkCursor == right.LinkCursor && left.TargetDeletedCount == right.TargetDeletedCount &&
		left.FindingDeletedCount == right.FindingDeletedCount &&
		left.VerifiedEmptyAt.Equal(right.VerifiedEmptyAt) &&
		left.LinkageDeletedAt.Equal(right.LinkageDeletedAt) &&
		left.PurgeJobExpiresAt.Equal(right.PurgeJobExpiresAt)
}

func sameReceiptPurgeJob(left, right ReceiptPurgeJob) bool {
	return sameReceiptPurgeImmutableBinding(left, right) && left.Status == right.Status &&
		left.Revision == right.Revision && sameReceiptPurgeProgressResidue(left, right) &&
		left.UpdatedAt.Equal(right.UpdatedAt) && left.HeldFromStatus == right.HeldFromStatus &&
		left.ErrorClass == right.ErrorClass
}

// ReceiptPurgeMutationBinding is a compact diagnostic string for tests and
// evidence. It does not grant mutation authority.
func ReceiptPurgeMutationBinding(query ReceiptPurgeMutationOutcomeQuery) (string, error) {
	if ValidateReceiptPurgeMutationOutcomeQuery(query) != nil {
		return "", ErrInvalidReceiptPurgeMutation
	}
	encoder := newArtifactBindingEncoder("ingest-receipt-purge-mutation@1")
	encoder.addString(string(query.Kind))
	encoder.addString(query.PreJob.PurgeKey)
	encoder.addString(strconv.FormatInt(query.PreJob.Revision, 10))
	encoder.addString(strconv.FormatInt(query.NextJob.Revision, 10))
	encoder.addString(query.DeleteSetDigest)
	for _, documentID := range query.DeleteDocumentIDs {
		encoder.addString(documentID)
	}
	binding := encoder.sum()
	digest := sha256.Sum256(binding[:])
	return hex.EncodeToString(digest[:]), nil
}

func emptyReceiptPurgePageObservation(observation ReceiptPurgePageObservation) bool {
	return observation.Request == (ReceiptPurgePageRequest{}) &&
		len(observation.DeleteDocumentIDs) == 0 && observation.LookaheadDocumentID == "" &&
		observation.ReadAt.IsZero()
}
