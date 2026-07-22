package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	ReceiptPurgeJobSchemaVersion = "ingest-receipt-purge.v1"
	ReceiptPurgePolicyVersion    = "ingest-receipt-purge-policy.v1"
	ReceiptPurgeFenceVersion     = "ingest-receipt-purge-fence.v1"

	receiptPurgeKeyVersion         = "ingest-receipt-purge@1"
	receiptPurgeLinkageHashVersion = "ingest-receipt-purge-linkage@1"
	ReceiptPurgeMaxPageSize        = 100
)

var (
	ErrInvalidReceiptPurgeJob                  = errors.New("receipt purge job is invalid")
	ErrInvalidReceiptPurgeAdmission            = errors.New("receipt purge admission is invalid")
	ErrReceiptPurgeAdmissionConflict           = errors.New("receipt purge admission conflicts with durable state")
	ErrReceiptPurgeAdmissionUnavailable        = errors.New("receipt purge admission is unavailable")
	ErrReceiptPurgeAdmissionOutcomeUnavailable = errors.New("receipt purge admission outcome is unavailable")
)

type ReceiptPurgeJobStatus string

const (
	ReceiptPurgeJobPlanned                ReceiptPurgeJobStatus = "planned"
	ReceiptPurgeJobAttemptsPurging        ReceiptPurgeJobStatus = "attempts_purging"
	ReceiptPurgeJobLinkedDocumentsPurging ReceiptPurgeJobStatus = "linked_documents_purging"
	ReceiptPurgeJobReady                  ReceiptPurgeJobStatus = "ready"
	ReceiptPurgeJobLinkageDeleted         ReceiptPurgeJobStatus = "linkage_deleted"
	ReceiptPurgeJobHold                   ReceiptPurgeJobStatus = "hold"
)

type ReceiptPurgeErrorClass string

const (
	ReceiptPurgeErrorChildMalformed     ReceiptPurgeErrorClass = "child_malformed"
	ReceiptPurgeErrorChildForeign       ReceiptPurgeErrorClass = "child_foreign"
	ReceiptPurgeErrorLinkageDrift       ReceiptPurgeErrorClass = "linkage_drift"
	ReceiptPurgeErrorCursorRegression   ReceiptPurgeErrorClass = "cursor_regression"
	ReceiptPurgeErrorCountOverflow      ReceiptPurgeErrorClass = "count_overflow"
	ReceiptPurgeErrorUnsupportedVersion ReceiptPurgeErrorClass = "unsupported_version"
	ReceiptPurgeErrorFencePartial       ReceiptPurgeErrorClass = "fence_partial"
)

// ReceiptPurgeJob contains bounded control-plane state only. Cursor values are
// document IDs, never paths, and no user, device, trip, payload, or artifact
// identity is admitted to this contract.
type ReceiptPurgeJob struct {
	SchemaVersion       string
	PolicyVersion       string
	PurgeKey            string
	TenantID            string
	ReceiptID           string
	ReceiptRevision     int64
	LinkageHash         string
	Status              ReceiptPurgeJobStatus
	Revision            int64
	AttemptCursor       string
	AttemptDeletedCount int64
	LinkCursor          string
	TargetDeletedCount  int64
	FindingDeletedCount int64
	VerifiedEmptyAt     time.Time
	LinkageDeletedAt    time.Time
	PurgeJobExpiresAt   time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	HeldFromStatus      ReceiptPurgeJobStatus
	ErrorClass          ReceiptPurgeErrorClass
}

// ReceiptPurgeFence is the complete three-field receipt-side writer fence.
// A zero value means no fence. Partial values are always invalid.
type ReceiptPurgeFence struct {
	PurgeJobID string
	StartedAt  time.Time
	Version    string
}

// ReceiptPurgeReceiptState is the bounded receipt projection required by the
// purge protocol. It deliberately excludes Firebase UID, device/trip/person
// IDs, artifact paths, and payload data.
type ReceiptPurgeReceiptState struct {
	TenantID              string
	ReceiptID             string
	ReservationKey        string
	ClientBatchKey        string
	State                 ReceiptState
	Revision              int64
	UpdatedAt             time.Time
	ReceiptRetentionFloor time.Time
	PurgeEligibleAt       time.Time
	LeasePresent          bool
	ForwardCursorPresent  bool
	CleanupCursorPresent  bool
	RecoveryHoldPresent   bool
	Fence                 ReceiptPurgeFence
}

// ReceiptPurgeIndexState is a minimal projection shared by both uniqueness
// indexes. DocumentID is the exact Firestore document ID, not a path.
type ReceiptPurgeIndexState struct {
	DocumentID      string
	TenantID        string
	ReceiptID       string
	ReservationKey  string
	ClientBatchKey  string
	PurgeEligibleAt time.Time
}

type ReceiptPurgeAdmissionState struct {
	Receipt          ReceiptPurgeReceiptState
	ReservationIndex ReceiptPurgeIndexState
	ClientBatchIndex ReceiptPurgeIndexState
}

type ReceiptPurgeLinkage struct {
	TenantID                   string
	ReceiptID                  string
	ReservationKey             string
	ClientBatchKey             string
	ReservationIndexDocumentID string
	ClientBatchIndexDocumentID string
	PostFenceReceiptRevision   int64
	PurgeEligibleAt            time.Time
}

type ReceiptPurgeAdmissionCommand struct {
	PurgeKey                   string
	TenantID                   string
	ReceiptID                  string
	ReservationKey             string
	ClientBatchKey             string
	ReservationIndexDocumentID string
	ClientBatchIndexDocumentID string
	ExpectedPreReceiptRevision int64
	ExpectedReceiptUpdatedAt   time.Time
	ReceiptRetentionFloor      time.Time
	PurgeEligibleAt            time.Time
	LinkageHash                string
}

type ReceiptPurgeAdmissionOutcomeQuery struct {
	Command                     ReceiptPurgeAdmissionCommand
	ExpectedPostReceiptRevision int64
	ExpectedPurgeStartedAt      time.Time
}

type ReceiptPurgeAdmissionStatus string

const (
	ReceiptPurgeAdmissionCreated  ReceiptPurgeAdmissionStatus = "created"
	ReceiptPurgeAdmissionReplayed ReceiptPurgeAdmissionStatus = "replayed"
)

// ReceiptPurgeAdmissionResult preserves the pre-write job and outcome query
// even when the adapter cannot observe the commit response. Status remains
// empty on that ambiguous error path.
type ReceiptPurgeAdmissionResult struct {
	Job          ReceiptPurgeJob
	OutcomeQuery ReceiptPurgeAdmissionOutcomeQuery
	Status       ReceiptPurgeAdmissionStatus
}

type ReceiptPurgeAdmissionOutcomeSnapshot struct {
	ReceiptPresent          bool
	ReservationIndexPresent bool
	ClientBatchIndexPresent bool
	JobPresent              bool
	Receipt                 ReceiptPurgeReceiptState
	ReservationIndex        ReceiptPurgeIndexState
	ClientBatchIndex        ReceiptPurgeIndexState
	Job                     ReceiptPurgeJob
	ReadTime                time.Time
}

type ReceiptPurgeAdmissionCommitStatus string

const (
	ReceiptPurgeAdmissionCommitted    ReceiptPurgeAdmissionCommitStatus = "committed"
	ReceiptPurgeAdmissionNotCommitted ReceiptPurgeAdmissionCommitStatus = "not_committed"
	ReceiptPurgeAdmissionUnverifiable ReceiptPurgeAdmissionCommitStatus = "unverifiable"
)

type ReceiptPurgeAdmissionOutcome struct {
	PurgeKey        string
	CommitStatus    ReceiptPurgeAdmissionCommitStatus
	ReceiptRevision int64
	JobStatus       ReceiptPurgeJobStatus
	JobRevision     int64
	PurgeStartedAt  time.Time
}

type ReceiptPurgePageKind string

const (
	ReceiptPurgePageAttempts ReceiptPurgePageKind = "attempts"
	ReceiptPurgePageLinks    ReceiptPurgePageKind = "links"
)

// ReceiptPurgePageRequest is a bounded advisory discovery request. It never
// grants delete authority; R8k-b/c must reread every exact document inside the
// transaction that deletes it and advances the durable cursor.
type ReceiptPurgePageRequest struct {
	PurgeKey            string
	TenantID            string
	ReceiptID           string
	Kind                ReceiptPurgePageKind
	ExpectedJobStatus   ReceiptPurgeJobStatus
	ExpectedJobRevision int64
	AfterDocumentID     string
	PageSize            int
}

// ReceiptPurgePageObservation contains at most PageSize exact candidates and
// one separate lookahead ID. Document IDs are bounded control identifiers,
// never Firestore paths or document payloads.
type ReceiptPurgePageObservation struct {
	Request             ReceiptPurgePageRequest
	DeleteDocumentIDs   []string
	LookaheadDocumentID string
	ReadAt              time.Time
}

func DeriveReceiptPurgeKey(tenantID, receiptID string) (string, error) {
	if !telemetry.IsUUID(tenantID) || !telemetry.IsUUID(receiptID) {
		return "", ErrInvalidReceiptPurgeAdmission
	}
	digest := sha256.Sum256([]byte(receiptPurgeKeyVersion + "\x00" + tenantID + "\x00" + receiptID))
	return hex.EncodeToString(digest[:]), nil
}

func ValidateReceiptPurgePageRequest(request ReceiptPurgePageRequest) error {
	key, err := DeriveReceiptPurgeKey(request.TenantID, request.ReceiptID)
	if err != nil || request.PurgeKey != key || request.ExpectedJobRevision <= 0 ||
		request.PageSize < 1 || request.PageSize > ReceiptPurgeMaxPageSize ||
		!validReceiptPurgeCursor(request.AfterDocumentID) {
		return ErrInvalidReceiptPurgeJob
	}
	switch request.Kind {
	case ReceiptPurgePageAttempts:
		if request.ExpectedJobStatus != ReceiptPurgeJobAttemptsPurging {
			return ErrInvalidReceiptPurgeJob
		}
	case ReceiptPurgePageLinks:
		if request.ExpectedJobStatus != ReceiptPurgeJobLinkedDocumentsPurging {
			return ErrInvalidReceiptPurgeJob
		}
	default:
		return ErrInvalidReceiptPurgeJob
	}
	return nil
}

// BuildReceiptPurgePageObservation splits a query result of at most
// page_size+1 IDs into the exact transaction candidate set and a lookahead.
// The input must already be ordered by Firestore document ID ascending.
func BuildReceiptPurgePageObservation(
	request ReceiptPurgePageRequest,
	orderedDocumentIDs []string,
	readAt time.Time,
) (ReceiptPurgePageObservation, error) {
	if ValidateReceiptPurgePageRequest(request) != nil ||
		!validCleanupFirestoreTimestamp(readAt.UTC()) ||
		len(orderedDocumentIDs) > request.PageSize+1 ||
		validateReceiptPurgeOrderedDocumentIDs(request.AfterDocumentID, orderedDocumentIDs) != nil {
		return ReceiptPurgePageObservation{}, ErrInvalidReceiptPurgeJob
	}
	deleteCount := len(orderedDocumentIDs)
	lookahead := ""
	if deleteCount == request.PageSize+1 {
		deleteCount = request.PageSize
		lookahead = orderedDocumentIDs[deleteCount]
	}
	observation := ReceiptPurgePageObservation{
		Request: request, DeleteDocumentIDs: append([]string(nil), orderedDocumentIDs[:deleteCount]...),
		LookaheadDocumentID: lookahead, ReadAt: readAt.UTC(),
	}
	if ValidateReceiptPurgePageObservation(observation) != nil {
		return ReceiptPurgePageObservation{}, ErrInvalidReceiptPurgeJob
	}
	return observation, nil
}

func ValidateReceiptPurgePageObservation(observation ReceiptPurgePageObservation) error {
	if ValidateReceiptPurgePageRequest(observation.Request) != nil ||
		!validCleanupFirestoreTimestamp(observation.ReadAt.UTC()) ||
		len(observation.DeleteDocumentIDs) > observation.Request.PageSize ||
		(observation.LookaheadDocumentID != "" &&
			len(observation.DeleteDocumentIDs) != observation.Request.PageSize) {
		return ErrInvalidReceiptPurgeJob
	}
	ordered := append([]string(nil), observation.DeleteDocumentIDs...)
	if observation.LookaheadDocumentID != "" {
		ordered = append(ordered, observation.LookaheadDocumentID)
	}
	return validateReceiptPurgeOrderedDocumentIDs(observation.Request.AfterDocumentID, ordered)
}

func validateReceiptPurgeOrderedDocumentIDs(after string, values []string) error {
	previous := after
	for _, value := range values {
		if !safeRecoveryDocumentID(value, 1500) || value <= previous {
			return ErrInvalidReceiptPurgeJob
		}
		previous = value
	}
	return nil
}

func ReceiptPurgeLinkageHash(linkage ReceiptPurgeLinkage) (string, error) {
	if validateReceiptPurgeLinkage(linkage) != nil {
		return "", ErrInvalidReceiptPurgeAdmission
	}
	encoder := newArtifactBindingEncoder(receiptPurgeLinkageHashVersion)
	encoder.addString(linkage.TenantID)
	encoder.addString(linkage.ReceiptID)
	encoder.addString(linkage.ReservationKey)
	encoder.addString(linkage.ClientBatchKey)
	encoder.addString(linkage.ReservationIndexDocumentID)
	encoder.addString(linkage.ClientBatchIndexDocumentID)
	encoder.addString(strconv.FormatInt(linkage.PostFenceReceiptRevision, 10))
	encoder.addTime(linkage.PurgeEligibleAt.UTC())
	digest := encoder.sum()
	return hex.EncodeToString(digest[:]), nil
}

func ValidateReceiptPurgeJob(job ReceiptPurgeJob) error {
	key, keyErr := DeriveReceiptPurgeKey(job.TenantID, job.ReceiptID)
	linkedDeletedCount, countOverflow := receiptPurgeLinkedDeletedCount(job)
	if keyErr != nil || job.SchemaVersion != ReceiptPurgeJobSchemaVersion ||
		job.PolicyVersion != ReceiptPurgePolicyVersion || job.PurgeKey != key ||
		!isLowerHexDigest(job.LinkageHash) || job.ReceiptRevision <= 0 ||
		job.Revision <= 0 || job.AttemptDeletedCount < 0 || job.TargetDeletedCount < 0 ||
		job.FindingDeletedCount < 0 || countOverflow ||
		!validCleanupFirestoreTimestamp(job.CreatedAt.UTC()) ||
		!validCleanupFirestoreTimestamp(job.UpdatedAt.UTC()) || job.UpdatedAt.Before(job.CreatedAt) ||
		!validReceiptPurgeCursor(job.AttemptCursor) || !validReceiptPurgeCursor(job.LinkCursor) ||
		(job.AttemptDeletedCount == 0) != (job.AttemptCursor == "") ||
		(linkedDeletedCount == 0) != (job.LinkCursor == "") {
		return ErrInvalidReceiptPurgeJob
	}
	if job.Status == ReceiptPurgeJobHold {
		if job.Revision < 2 || !validReceiptPurgeHoldSource(job.HeldFromStatus) ||
			!validReceiptPurgeErrorClass(job.ErrorClass) ||
			validateReceiptPurgeJobResidue(job, job.HeldFromStatus) != nil {
			return ErrInvalidReceiptPurgeJob
		}
		return nil
	}
	if job.HeldFromStatus != "" || job.ErrorClass != "" ||
		validateReceiptPurgeJobResidue(job, job.Status) != nil {
		return ErrInvalidReceiptPurgeJob
	}
	return nil
}

func BuildReceiptPurgeAdmissionCommand(
	state ReceiptPurgeAdmissionState,
	checkedAt time.Time,
) (ReceiptPurgeAdmissionCommand, error) {
	checkedAt = checkedAt.UTC()
	if validateReceiptPurgeAdmissionState(state, checkedAt) != nil ||
		state.Receipt.Revision == int64(^uint64(0)>>1) {
		return ReceiptPurgeAdmissionCommand{}, ErrInvalidReceiptPurgeAdmission
	}
	key, err := DeriveReceiptPurgeKey(state.Receipt.TenantID, state.Receipt.ReceiptID)
	if err != nil {
		return ReceiptPurgeAdmissionCommand{}, ErrInvalidReceiptPurgeAdmission
	}
	linkage := receiptPurgeLinkageFromState(state, state.Receipt.Revision+1)
	linkageHash, err := ReceiptPurgeLinkageHash(linkage)
	if err != nil {
		return ReceiptPurgeAdmissionCommand{}, ErrInvalidReceiptPurgeAdmission
	}
	command := ReceiptPurgeAdmissionCommand{
		PurgeKey: key, TenantID: linkage.TenantID, ReceiptID: linkage.ReceiptID,
		ReservationKey: linkage.ReservationKey, ClientBatchKey: linkage.ClientBatchKey,
		ReservationIndexDocumentID: linkage.ReservationIndexDocumentID,
		ClientBatchIndexDocumentID: linkage.ClientBatchIndexDocumentID,
		ExpectedPreReceiptRevision: state.Receipt.Revision,
		ExpectedReceiptUpdatedAt:   state.Receipt.UpdatedAt.UTC(),
		ReceiptRetentionFloor:      state.Receipt.ReceiptRetentionFloor.UTC(),
		PurgeEligibleAt:            linkage.PurgeEligibleAt.UTC(), LinkageHash: linkageHash,
	}
	if ValidateReceiptPurgeAdmissionCommand(command) != nil {
		return ReceiptPurgeAdmissionCommand{}, ErrInvalidReceiptPurgeAdmission
	}
	return command, nil
}

func ValidateReceiptPurgeAdmissionCommand(command ReceiptPurgeAdmissionCommand) error {
	key, keyErr := DeriveReceiptPurgeKey(command.TenantID, command.ReceiptID)
	if keyErr != nil || command.PurgeKey != key || command.ExpectedPreReceiptRevision <= 0 ||
		command.ExpectedPreReceiptRevision == int64(^uint64(0)>>1) ||
		!validCleanupFirestoreTimestamp(command.ExpectedReceiptUpdatedAt.UTC()) ||
		!validCleanupFirestoreTimestamp(command.ReceiptRetentionFloor.UTC()) ||
		!validCleanupFirestoreTimestamp(command.PurgeEligibleAt.UTC()) {
		return ErrInvalidReceiptPurgeAdmission
	}
	linkage := receiptPurgeLinkageFromCommand(command)
	hash, err := ReceiptPurgeLinkageHash(linkage)
	if err != nil || hash != command.LinkageHash {
		return ErrInvalidReceiptPurgeAdmission
	}
	expectedPurge, err := CleanupPurgeEligibleAt(
		command.ReceiptRetentionFloor.UTC(), command.ExpectedReceiptUpdatedAt.UTC(),
	)
	if err != nil || !expectedPurge.Equal(command.PurgeEligibleAt) {
		return ErrInvalidReceiptPurgeAdmission
	}
	return nil
}

func BuildPostFenceReceiptPurgeJob(
	command ReceiptPurgeAdmissionCommand,
	startedAt time.Time,
) (ReceiptPurgeJob, error) {
	startedAt = startedAt.UTC()
	if ValidateReceiptPurgeAdmissionCommand(command) != nil ||
		!validCleanupFirestoreTimestamp(startedAt) || startedAt.Before(command.PurgeEligibleAt) {
		return ReceiptPurgeJob{}, ErrInvalidReceiptPurgeAdmission
	}
	job := ReceiptPurgeJob{
		SchemaVersion: ReceiptPurgeJobSchemaVersion, PolicyVersion: ReceiptPurgePolicyVersion,
		PurgeKey: command.PurgeKey, TenantID: command.TenantID, ReceiptID: command.ReceiptID,
		ReceiptRevision: command.ExpectedPreReceiptRevision + 1, LinkageHash: command.LinkageHash,
		Status: ReceiptPurgeJobPlanned, Revision: 1, CreatedAt: startedAt, UpdatedAt: startedAt,
	}
	if ValidateReceiptPurgeJob(job) != nil {
		return ReceiptPurgeJob{}, ErrInvalidReceiptPurgeAdmission
	}
	return job, nil
}

func BuildReceiptPurgeAdmissionOutcomeQuery(
	command ReceiptPurgeAdmissionCommand,
	startedAt time.Time,
) (ReceiptPurgeAdmissionOutcomeQuery, error) {
	job, err := BuildPostFenceReceiptPurgeJob(command, startedAt)
	if err != nil {
		return ReceiptPurgeAdmissionOutcomeQuery{}, ErrInvalidReceiptPurgeAdmission
	}
	query := ReceiptPurgeAdmissionOutcomeQuery{
		Command: command, ExpectedPostReceiptRevision: job.ReceiptRevision,
		ExpectedPurgeStartedAt: job.CreatedAt,
	}
	if ValidateReceiptPurgeAdmissionOutcomeQuery(query) != nil {
		return ReceiptPurgeAdmissionOutcomeQuery{}, ErrInvalidReceiptPurgeAdmission
	}
	return query, nil
}

func ValidateReceiptPurgeAdmissionOutcomeQuery(query ReceiptPurgeAdmissionOutcomeQuery) error {
	if ValidateReceiptPurgeAdmissionCommand(query.Command) != nil ||
		query.ExpectedPostReceiptRevision != query.Command.ExpectedPreReceiptRevision+1 ||
		!validCleanupFirestoreTimestamp(query.ExpectedPurgeStartedAt.UTC()) ||
		query.ExpectedPurgeStartedAt.Before(query.Command.PurgeEligibleAt) {
		return ErrInvalidReceiptPurgeAdmission
	}
	return nil
}

func EvaluateReceiptPurgeAdmissionOutcome(
	query ReceiptPurgeAdmissionOutcomeQuery,
	snapshot ReceiptPurgeAdmissionOutcomeSnapshot,
	observedAt time.Time,
) (ReceiptPurgeAdmissionOutcome, error) {
	observedAt = observedAt.UTC()
	if ValidateReceiptPurgeAdmissionOutcomeQuery(query) != nil ||
		!snapshot.ReceiptPresent || !snapshot.ReservationIndexPresent ||
		!snapshot.ClientBatchIndexPresent || !validCleanupFirestoreTimestamp(snapshot.ReadTime.UTC()) ||
		!validCleanupFirestoreTimestamp(observedAt) || observedAt.Before(snapshot.ReadTime) ||
		validateReceiptPurgeOutcomeLinkage(query.Command, snapshot) != nil {
		return ReceiptPurgeAdmissionOutcome{}, ErrReceiptPurgeAdmissionOutcomeUnavailable
	}
	outcome := ReceiptPurgeAdmissionOutcome{
		PurgeKey: query.Command.PurgeKey, ReceiptRevision: snapshot.Receipt.Revision,
	}
	if !snapshot.JobPresent && receiptPurgeFenceEmpty(snapshot.Receipt.Fence) &&
		snapshot.Receipt.Revision == query.Command.ExpectedPreReceiptRevision &&
		snapshot.Receipt.UpdatedAt.Equal(query.Command.ExpectedReceiptUpdatedAt) {
		outcome.CommitStatus = ReceiptPurgeAdmissionNotCommitted
		return outcome, nil
	}
	if snapshot.JobPresent {
		if ValidateReceiptPurgeJob(snapshot.Job) != nil {
			return ReceiptPurgeAdmissionOutcome{}, ErrReceiptPurgeAdmissionOutcomeUnavailable
		}
		expectedJob, err := BuildPostFenceReceiptPurgeJob(query.Command, query.ExpectedPurgeStartedAt)
		if err != nil {
			return ReceiptPurgeAdmissionOutcome{}, ErrReceiptPurgeAdmissionOutcomeUnavailable
		}
		if sameReceiptPurgeAdmissionBinding(snapshot.Job, expectedJob) &&
			snapshot.Job.Status != ReceiptPurgeJobLinkageDeleted &&
			snapshot.Receipt.Revision == query.ExpectedPostReceiptRevision &&
			snapshot.Receipt.UpdatedAt.Equal(query.ExpectedPurgeStartedAt) &&
			receiptPurgeFenceMatches(snapshot.Receipt.Fence, query) {
			outcome.CommitStatus = ReceiptPurgeAdmissionCommitted
			outcome.JobStatus = snapshot.Job.Status
			outcome.JobRevision = snapshot.Job.Revision
			outcome.PurgeStartedAt = snapshot.Job.CreatedAt.UTC()
			return outcome, nil
		}
	}
	outcome.CommitStatus = ReceiptPurgeAdmissionUnverifiable
	return outcome, nil
}

func sameReceiptPurgeAdmissionBinding(current, expected ReceiptPurgeJob) bool {
	return current.SchemaVersion == expected.SchemaVersion &&
		current.PolicyVersion == expected.PolicyVersion && current.PurgeKey == expected.PurgeKey &&
		current.TenantID == expected.TenantID && current.ReceiptID == expected.ReceiptID &&
		current.ReceiptRevision == expected.ReceiptRevision &&
		current.LinkageHash == expected.LinkageHash && current.CreatedAt.Equal(expected.CreatedAt)
}

func validateReceiptPurgeJobResidue(job ReceiptPurgeJob, status ReceiptPurgeJobStatus) error {
	switch status {
	case ReceiptPurgeJobPlanned:
		if job.Status == ReceiptPurgeJobPlanned && job.Revision != 1 ||
			job.AttemptCursor != "" || job.AttemptDeletedCount != 0 || job.LinkCursor != "" ||
			job.TargetDeletedCount != 0 || job.FindingDeletedCount != 0 ||
			!job.VerifiedEmptyAt.IsZero() || !job.LinkageDeletedAt.IsZero() ||
			!job.PurgeJobExpiresAt.IsZero() {
			return ErrInvalidReceiptPurgeJob
		}
	case ReceiptPurgeJobAttemptsPurging:
		if job.Revision < 2 || job.LinkCursor != "" || job.TargetDeletedCount != 0 ||
			job.FindingDeletedCount != 0 || !job.VerifiedEmptyAt.IsZero() ||
			!job.LinkageDeletedAt.IsZero() || !job.PurgeJobExpiresAt.IsZero() {
			return ErrInvalidReceiptPurgeJob
		}
	case ReceiptPurgeJobLinkedDocumentsPurging:
		if job.Revision < 3 || !job.VerifiedEmptyAt.IsZero() ||
			!job.LinkageDeletedAt.IsZero() || !job.PurgeJobExpiresAt.IsZero() {
			return ErrInvalidReceiptPurgeJob
		}
	case ReceiptPurgeJobReady:
		if job.Revision < 4 || !validCleanupFirestoreTimestamp(job.VerifiedEmptyAt.UTC()) ||
			job.VerifiedEmptyAt.Before(job.CreatedAt) || job.VerifiedEmptyAt.After(job.UpdatedAt) ||
			!job.LinkageDeletedAt.IsZero() || !job.PurgeJobExpiresAt.IsZero() {
			return ErrInvalidReceiptPurgeJob
		}
	case ReceiptPurgeJobLinkageDeleted:
		if job.Revision < 5 || !validCleanupFirestoreTimestamp(job.VerifiedEmptyAt.UTC()) ||
			!validCleanupFirestoreTimestamp(job.LinkageDeletedAt.UTC()) ||
			!validCleanupFirestoreTimestamp(job.PurgeJobExpiresAt.UTC()) ||
			job.VerifiedEmptyAt.Before(job.CreatedAt) ||
			job.LinkageDeletedAt.Before(job.VerifiedEmptyAt) ||
			job.LinkageDeletedAt.After(job.UpdatedAt) ||
			!job.PurgeJobExpiresAt.After(job.LinkageDeletedAt) {
			return ErrInvalidReceiptPurgeJob
		}
	default:
		return ErrInvalidReceiptPurgeJob
	}
	return nil
}

func validateReceiptPurgeAdmissionState(state ReceiptPurgeAdmissionState, checkedAt time.Time) error {
	receipt := state.Receipt
	if !validCleanupFirestoreTimestamp(checkedAt) || !telemetry.IsUUID(receipt.TenantID) ||
		!telemetry.IsUUID(receipt.ReceiptID) || !isLowerHexDigest(receipt.ReservationKey) ||
		!isLowerHexDigest(receipt.ClientBatchKey) || receipt.State != ReceiptExpired ||
		receipt.Revision <= 0 || !validCleanupFirestoreTimestamp(receipt.UpdatedAt.UTC()) ||
		!validCleanupFirestoreTimestamp(receipt.ReceiptRetentionFloor.UTC()) ||
		!validCleanupFirestoreTimestamp(receipt.PurgeEligibleAt.UTC()) ||
		checkedAt.Before(receipt.PurgeEligibleAt) || receipt.LeasePresent ||
		receipt.ForwardCursorPresent || receipt.CleanupCursorPresent || receipt.RecoveryHoldPresent ||
		!receiptPurgeFenceEmpty(receipt.Fence) {
		return ErrInvalidReceiptPurgeAdmission
	}
	expectedPurge, err := CleanupPurgeEligibleAt(receipt.ReceiptRetentionFloor, receipt.UpdatedAt)
	if err != nil || !expectedPurge.Equal(receipt.PurgeEligibleAt) ||
		validateReceiptPurgeIndex(state.ReservationIndex, receipt, receipt.ReservationKey) != nil ||
		validateReceiptPurgeIndex(state.ClientBatchIndex, receipt, receipt.ClientBatchKey) != nil {
		return ErrInvalidReceiptPurgeAdmission
	}
	return nil
}

func validateReceiptPurgeIndex(
	index ReceiptPurgeIndexState,
	receipt ReceiptPurgeReceiptState,
	expectedDocumentID string,
) error {
	if index.DocumentID != expectedDocumentID || !isLowerHexDigest(index.DocumentID) ||
		index.TenantID != receipt.TenantID || index.ReceiptID != receipt.ReceiptID ||
		index.ReservationKey != receipt.ReservationKey || index.ClientBatchKey != receipt.ClientBatchKey ||
		!index.PurgeEligibleAt.Equal(receipt.PurgeEligibleAt) {
		return ErrInvalidReceiptPurgeAdmission
	}
	return nil
}

func validateReceiptPurgeLinkage(linkage ReceiptPurgeLinkage) error {
	if !telemetry.IsUUID(linkage.TenantID) || !telemetry.IsUUID(linkage.ReceiptID) ||
		!isLowerHexDigest(linkage.ReservationKey) || !isLowerHexDigest(linkage.ClientBatchKey) ||
		linkage.ReservationIndexDocumentID != linkage.ReservationKey ||
		linkage.ClientBatchIndexDocumentID != linkage.ClientBatchKey ||
		linkage.PostFenceReceiptRevision <= 1 ||
		!validCleanupFirestoreTimestamp(linkage.PurgeEligibleAt.UTC()) {
		return ErrInvalidReceiptPurgeAdmission
	}
	return nil
}

func validateReceiptPurgeOutcomeLinkage(
	command ReceiptPurgeAdmissionCommand,
	snapshot ReceiptPurgeAdmissionOutcomeSnapshot,
) error {
	receipt := snapshot.Receipt
	if receipt.TenantID != command.TenantID || receipt.ReceiptID != command.ReceiptID ||
		receipt.ReservationKey != command.ReservationKey || receipt.ClientBatchKey != command.ClientBatchKey ||
		receipt.State != ReceiptExpired ||
		!receipt.ReceiptRetentionFloor.Equal(command.ReceiptRetentionFloor) ||
		!receipt.PurgeEligibleAt.Equal(command.PurgeEligibleAt) || receipt.LeasePresent ||
		receipt.ForwardCursorPresent || receipt.CleanupCursorPresent || receipt.RecoveryHoldPresent ||
		validateReceiptPurgeIndex(snapshot.ReservationIndex, receipt, command.ReservationIndexDocumentID) != nil ||
		validateReceiptPurgeIndex(snapshot.ClientBatchIndex, receipt, command.ClientBatchIndexDocumentID) != nil {
		return ErrInvalidReceiptPurgeAdmission
	}
	return nil
}

func receiptPurgeLinkageFromState(
	state ReceiptPurgeAdmissionState,
	postFenceRevision int64,
) ReceiptPurgeLinkage {
	return ReceiptPurgeLinkage{
		TenantID: state.Receipt.TenantID, ReceiptID: state.Receipt.ReceiptID,
		ReservationKey: state.Receipt.ReservationKey, ClientBatchKey: state.Receipt.ClientBatchKey,
		ReservationIndexDocumentID: state.ReservationIndex.DocumentID,
		ClientBatchIndexDocumentID: state.ClientBatchIndex.DocumentID,
		PostFenceReceiptRevision:   postFenceRevision, PurgeEligibleAt: state.Receipt.PurgeEligibleAt,
	}
}

func receiptPurgeLinkageFromCommand(command ReceiptPurgeAdmissionCommand) ReceiptPurgeLinkage {
	return ReceiptPurgeLinkage{
		TenantID: command.TenantID, ReceiptID: command.ReceiptID,
		ReservationKey: command.ReservationKey, ClientBatchKey: command.ClientBatchKey,
		ReservationIndexDocumentID: command.ReservationIndexDocumentID,
		ClientBatchIndexDocumentID: command.ClientBatchIndexDocumentID,
		PostFenceReceiptRevision:   command.ExpectedPreReceiptRevision + 1,
		PurgeEligibleAt:            command.PurgeEligibleAt,
	}
}

func validReceiptPurgeCursor(value string) bool {
	return value == "" || safeRecoveryDocumentID(value, 1500)
}

func validReceiptPurgeHoldSource(status ReceiptPurgeJobStatus) bool {
	switch status {
	case ReceiptPurgeJobPlanned, ReceiptPurgeJobAttemptsPurging,
		ReceiptPurgeJobLinkedDocumentsPurging, ReceiptPurgeJobReady:
		return true
	default:
		return false
	}
}

func validReceiptPurgeErrorClass(value ReceiptPurgeErrorClass) bool {
	switch value {
	case ReceiptPurgeErrorChildMalformed, ReceiptPurgeErrorChildForeign,
		ReceiptPurgeErrorLinkageDrift, ReceiptPurgeErrorCursorRegression,
		ReceiptPurgeErrorCountOverflow, ReceiptPurgeErrorUnsupportedVersion,
		ReceiptPurgeErrorFencePartial:
		return true
	default:
		return false
	}
}

func receiptPurgeFenceEmpty(fence ReceiptPurgeFence) bool {
	return fence.PurgeJobID == "" && fence.StartedAt.IsZero() && fence.Version == ""
}

func receiptPurgeFenceMatches(
	fence ReceiptPurgeFence,
	query ReceiptPurgeAdmissionOutcomeQuery,
) bool {
	return fence.PurgeJobID == query.Command.PurgeKey &&
		fence.Version == ReceiptPurgeFenceVersion &&
		fence.StartedAt.Equal(query.ExpectedPurgeStartedAt)
}

func receiptPurgeLinkedDeletedCount(job ReceiptPurgeJob) (int64, bool) {
	count := job.TargetDeletedCount + job.FindingDeletedCount
	return count, count < job.TargetDeletedCount || count < job.FindingDeletedCount
}
