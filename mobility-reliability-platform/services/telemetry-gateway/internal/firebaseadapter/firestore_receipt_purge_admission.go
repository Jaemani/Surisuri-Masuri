package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

type receiptPurgeJobRead struct {
	Job      firestoreReceiptPurgeJob
	ReadTime time.Time
}

type receiptPurgeTransaction interface {
	admissionTransaction
	ReadReceiptPurgeJob(context.Context, string) (receiptPurgeJobRead, bool, error)
}

type firestoreReceiptPurgeJob struct {
	SchemaVersion       string                        `firestore:"schema_version"`
	PolicyVersion       string                        `firestore:"policy_version"`
	PurgeKey            string                        `firestore:"purge_key"`
	TenantID            string                        `firestore:"tenant_id"`
	ReceiptID           string                        `firestore:"receipt_id"`
	ReceiptRevision     int64                         `firestore:"receipt_revision"`
	LinkageHash         string                        `firestore:"linkage_hash"`
	Status              ingest.ReceiptPurgeJobStatus  `firestore:"status"`
	Revision            int64                         `firestore:"revision"`
	AttemptCursor       string                        `firestore:"attempt_cursor,omitempty"`
	AttemptDeletedCount int64                         `firestore:"attempt_deleted_count"`
	LinkCursor          string                        `firestore:"link_cursor,omitempty"`
	TargetDeletedCount  int64                         `firestore:"target_deleted_count"`
	FindingDeletedCount int64                         `firestore:"finding_deleted_count"`
	VerifiedEmptyAt     time.Time                     `firestore:"verified_empty_at,omitempty"`
	LinkageDeletedAt    time.Time                     `firestore:"linkage_deleted_at,omitempty"`
	PurgeJobExpiresAt   time.Time                     `firestore:"purge_job_expires_at,omitempty"`
	CreatedAt           time.Time                     `firestore:"created_at"`
	UpdatedAt           time.Time                     `firestore:"updated_at"`
	HeldFromStatus      ingest.ReceiptPurgeJobStatus  `firestore:"held_from_status,omitempty"`
	ErrorClass          ingest.ReceiptPurgeErrorClass `firestore:"error_class,omitempty"`
}

func newFirestoreReceiptPurgeJob(job ingest.ReceiptPurgeJob) firestoreReceiptPurgeJob {
	return firestoreReceiptPurgeJob{
		SchemaVersion: job.SchemaVersion, PolicyVersion: job.PolicyVersion,
		PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		ReceiptRevision: job.ReceiptRevision, LinkageHash: job.LinkageHash,
		Status: job.Status, Revision: job.Revision,
		AttemptCursor: job.AttemptCursor, AttemptDeletedCount: job.AttemptDeletedCount,
		LinkCursor: job.LinkCursor, TargetDeletedCount: job.TargetDeletedCount,
		FindingDeletedCount: job.FindingDeletedCount, VerifiedEmptyAt: job.VerifiedEmptyAt,
		LinkageDeletedAt: job.LinkageDeletedAt, PurgeJobExpiresAt: job.PurgeJobExpiresAt,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
		HeldFromStatus: job.HeldFromStatus, ErrorClass: job.ErrorClass,
	}
}

func (job firestoreReceiptPurgeJob) toDomain() ingest.ReceiptPurgeJob {
	return ingest.ReceiptPurgeJob{
		SchemaVersion: job.SchemaVersion, PolicyVersion: job.PolicyVersion,
		PurgeKey: job.PurgeKey, TenantID: job.TenantID, ReceiptID: job.ReceiptID,
		ReceiptRevision: job.ReceiptRevision, LinkageHash: job.LinkageHash,
		Status: job.Status, Revision: job.Revision,
		AttemptCursor: job.AttemptCursor, AttemptDeletedCount: job.AttemptDeletedCount,
		LinkCursor: job.LinkCursor, TargetDeletedCount: job.TargetDeletedCount,
		FindingDeletedCount: job.FindingDeletedCount, VerifiedEmptyAt: job.VerifiedEmptyAt,
		LinkageDeletedAt: job.LinkageDeletedAt, PurgeJobExpiresAt: job.PurgeJobExpiresAt,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
		HeldFromStatus: job.HeldFromStatus, ErrorClass: job.ErrorClass,
	}
}

// AdmitReceiptPurge creates the durable purge job and receipt-side writer
// fence in one transaction. It does not delete linked documents and is not
// connected to a scheduler or production runtime.
func (s *FirestoreAdmissionStore) AdmitReceiptPurge(
	ctx context.Context,
	command ingest.ReceiptPurgeAdmissionCommand,
) (ingest.ReceiptPurgeAdmissionResult, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateReceiptPurgeAdmissionCommand(command) != nil {
		return ingest.ReceiptPurgeAdmissionResult{}, ingest.ErrInvalidReceiptPurgeAdmission
	}
	var result ingest.ReceiptPurgeAdmissionResult
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.ReceiptPurgeAdmissionResult{}
		purgeTransaction, ok := transaction.(receiptPurgeTransaction)
		if !ok {
			return ingest.ErrReceiptPurgeAdmissionUnavailable
		}
		linked, loadErr := loadLinkedReceipt(
			runContext, transaction, command.TenantID, command.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}
		if linked.Index.ClientBatchKey != command.ClientBatchKey ||
			linked.Index.ReceiptID != command.ReceiptID {
			return ingest.ErrReceiptPurgeAdmissionConflict
		}
		jobPath := receiptPurgeJobDocumentPath(command.PurgeKey)
		jobResult, jobExists, jobErr := purgeTransaction.ReadReceiptPurgeJob(runContext, jobPath)
		if jobErr != nil {
			return jobErr
		}
		receipt := linked.Receipt.Receipt
		if jobExists || receiptHasPurgeFenceFields(receipt) {
			return replayReceiptPurgeAdmission(command, receipt, jobResult, jobExists, &result)
		}

		effectiveAt, clockErr := conservativeAcceptanceTime(s.now().UTC(), linked.Receipt.ReadTime)
		if clockErr != nil {
			return ingest.ErrReceiptPurgeAdmissionUnavailable
		}
		state := receiptPurgeAdmissionState(receipt, linked.Index)
		currentCommand, commandErr := ingest.BuildReceiptPurgeAdmissionCommand(state, effectiveAt)
		if commandErr != nil || currentCommand != command {
			return ingest.ErrReceiptPurgeAdmissionConflict
		}
		job, jobErr := ingest.BuildPostFenceReceiptPurgeJob(command, effectiveAt)
		if jobErr != nil {
			return ingest.ErrReceiptPurgeAdmissionConflict
		}
		outcomeQuery, queryErr := ingest.BuildReceiptPurgeAdmissionOutcomeQuery(command, effectiveAt)
		if queryErr != nil {
			return ingest.ErrReceiptPurgeAdmissionConflict
		}
		result = ingest.ReceiptPurgeAdmissionResult{
			Job: job, OutcomeQuery: outcomeQuery, Status: ingest.ReceiptPurgeAdmissionCreated,
		}

		fenced := receipt
		fenced.PurgeJobID = command.PurgeKey
		fenced.PurgeStartedAt = effectiveAt
		fenced.PurgeFenceVersion = ingest.ReceiptPurgeFenceVersion
		fenced.Revision = job.ReceiptRevision
		fenced.UpdatedAt = effectiveAt
		if validateReceiptLinkage(fenced, linked.Index) != nil || validateReceiptState(fenced) != nil {
			return ingest.ErrReceiptPurgeAdmissionConflict
		}
		if createErr := transaction.Create(runContext, jobPath, newFirestoreReceiptPurgeJob(job)); createErr != nil {
			return normalizeAdmissionError(runContext, createErr)
		}
		if updateErr := transaction.Update(runContext, linked.ReceiptPath, []firestore.Update{
			{Path: "purge_job_id", Value: command.PurgeKey},
			{Path: "purge_started_at", Value: effectiveAt},
			{Path: "purge_fence_version", Value: ingest.ReceiptPurgeFenceVersion},
			{Path: "revision", Value: job.ReceiptRevision},
			{Path: "updated_at", Value: effectiveAt},
		}); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		return nil
	})
	if err != nil {
		// Job and query were sealed before the first write so they remain usable
		// for read-only correlation. Status must stay empty because the commit is
		// not known to have succeeded.
		result.Status = ""
		return result, normalizeReceiptPurgeAdmissionError(ctx, err)
	}
	if ingest.ValidateReceiptPurgeJob(result.Job) != nil ||
		ingest.ValidateReceiptPurgeAdmissionOutcomeQuery(result.OutcomeQuery) != nil ||
		(result.Status != ingest.ReceiptPurgeAdmissionCreated &&
			result.Status != ingest.ReceiptPurgeAdmissionReplayed) {
		return ingest.ReceiptPurgeAdmissionResult{}, ingest.ErrReceiptPurgeAdmissionUnavailable
	}
	return result, nil
}

func replayReceiptPurgeAdmission(
	command ingest.ReceiptPurgeAdmissionCommand,
	receipt firestoreIngestReceipt,
	jobResult receiptPurgeJobRead,
	jobExists bool,
	result *ingest.ReceiptPurgeAdmissionResult,
) error {
	if !jobExists || !receiptHasPurgeFenceFields(receipt) {
		return ingest.ErrReceiptPurgeAdmissionConflict
	}
	job := jobResult.Job.toDomain()
	if ingest.ValidateReceiptPurgeJob(job) != nil || job.PurgeKey != command.PurgeKey {
		return ingest.ErrReceiptPurgeAdmissionConflict
	}
	expected, err := ingest.BuildPostFenceReceiptPurgeJob(command, job.CreatedAt)
	if err != nil || !sameReceiptPurgeAdmissionBinding(job, expected) ||
		job.Status == ingest.ReceiptPurgeJobLinkageDeleted ||
		receipt.Revision != job.ReceiptRevision ||
		receipt.PurgeJobID != job.PurgeKey ||
		receipt.PurgeFenceVersion != ingest.ReceiptPurgeFenceVersion ||
		!receipt.PurgeStartedAt.Equal(job.CreatedAt) || !receipt.UpdatedAt.Equal(job.CreatedAt) {
		return ingest.ErrReceiptPurgeAdmissionConflict
	}
	query, err := ingest.BuildReceiptPurgeAdmissionOutcomeQuery(command, job.CreatedAt)
	if err != nil {
		return ingest.ErrReceiptPurgeAdmissionConflict
	}
	*result = ingest.ReceiptPurgeAdmissionResult{
		Job: job, OutcomeQuery: query, Status: ingest.ReceiptPurgeAdmissionReplayed,
	}
	return nil
}

func sameReceiptPurgeAdmissionBinding(current, expected ingest.ReceiptPurgeJob) bool {
	return current.SchemaVersion == expected.SchemaVersion &&
		current.PolicyVersion == expected.PolicyVersion && current.PurgeKey == expected.PurgeKey &&
		current.TenantID == expected.TenantID && current.ReceiptID == expected.ReceiptID &&
		current.ReceiptRevision == expected.ReceiptRevision &&
		current.LinkageHash == expected.LinkageHash && current.CreatedAt.Equal(expected.CreatedAt)
}

// GetReceiptPurgeAdmissionOutcome performs read-only correlation after an
// ambiguous transaction response. It never retries the admission mutation.
func (s *FirestoreAdmissionStore) GetReceiptPurgeAdmissionOutcome(
	ctx context.Context,
	query ingest.ReceiptPurgeAdmissionOutcomeQuery,
) (ingest.ReceiptPurgeAdmissionOutcome, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateReceiptPurgeAdmissionOutcomeQuery(query) != nil {
		return ingest.ReceiptPurgeAdmissionOutcome{}, ingest.ErrReceiptPurgeAdmissionOutcomeUnavailable
	}
	var snapshot ingest.ReceiptPurgeAdmissionOutcomeSnapshot
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		snapshot = ingest.ReceiptPurgeAdmissionOutcomeSnapshot{}
		purgeTransaction, ok := transaction.(receiptPurgeTransaction)
		if !ok {
			return ingest.ErrReceiptPurgeAdmissionOutcomeUnavailable
		}
		linked, loadErr := loadLinkedReceipt(
			runContext, transaction, query.Command.TenantID, query.Command.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}
		jobResult, jobExists, jobErr := purgeTransaction.ReadReceiptPurgeJob(
			runContext, receiptPurgeJobDocumentPath(query.Command.PurgeKey),
		)
		if jobErr != nil {
			return jobErr
		}
		readTime := linked.Receipt.ReadTime
		if jobExists && jobResult.ReadTime.After(readTime) {
			readTime = jobResult.ReadTime
		}
		snapshot = receiptPurgeAdmissionOutcomeSnapshot(
			linked.Receipt.Receipt, linked.Index, jobResult.Job, jobExists, readTime,
		)
		return nil
	})
	if err != nil {
		return ingest.ReceiptPurgeAdmissionOutcome{}, normalizeReceiptPurgeOutcomeError(ctx, err)
	}
	observedAt, clockErr := conservativeAcceptanceTime(s.now().UTC(), snapshot.ReadTime)
	if clockErr != nil {
		return ingest.ReceiptPurgeAdmissionOutcome{}, ingest.ErrReceiptPurgeAdmissionOutcomeUnavailable
	}
	result, err := ingest.EvaluateReceiptPurgeAdmissionOutcome(query, snapshot, observedAt)
	if err != nil {
		return ingest.ReceiptPurgeAdmissionOutcome{}, err
	}
	return result, nil
}

func receiptPurgeAdmissionState(
	receipt firestoreIngestReceipt,
	index firestoreIngestIndex,
) ingest.ReceiptPurgeAdmissionState {
	purgeEligibleAt := time.Time{}
	if receipt.PurgeEligibleAt != nil {
		purgeEligibleAt = receipt.PurgeEligibleAt.UTC()
	}
	receiptState := ingest.ReceiptPurgeReceiptState{
		TenantID: receipt.TenantID, ReceiptID: receipt.ReceiptID,
		ReservationKey: receipt.ReservationKey, ClientBatchKey: receipt.ClientBatchKey,
		State: receipt.State, Revision: receipt.Revision, UpdatedAt: receipt.UpdatedAt.UTC(),
		ReceiptRetentionFloor: receipt.ReceiptRetentionFloor.UTC(), PurgeEligibleAt: purgeEligibleAt,
		LeasePresent:         receiptHasLeaseFields(receipt),
		ForwardCursorPresent: !receipt.NextRecoveryAt.IsZero() || receipt.LastRecoveryCode != "",
		CleanupCursorPresent: cleanupControlCursorPresent(receipt),
		RecoveryHoldPresent:  receipt.RecoveryHoldCode != "" || !receipt.RecoveryHoldReviewDueAt.IsZero(),
		Fence: ingest.ReceiptPurgeFence{
			PurgeJobID: receipt.PurgeJobID, StartedAt: receipt.PurgeStartedAt.UTC(),
			Version: receipt.PurgeFenceVersion,
		},
	}
	indexState := func(documentID string) ingest.ReceiptPurgeIndexState {
		eligibleAt := time.Time{}
		if index.PurgeEligibleAt != nil {
			eligibleAt = index.PurgeEligibleAt.UTC()
		}
		return ingest.ReceiptPurgeIndexState{
			DocumentID: documentID, TenantID: index.TenantID, ReceiptID: index.ReceiptID,
			ReservationKey: index.ReservationKey, ClientBatchKey: index.ClientBatchKey,
			PurgeEligibleAt: eligibleAt,
		}
	}
	return ingest.ReceiptPurgeAdmissionState{
		Receipt:          receiptState,
		ReservationIndex: indexState(index.ReservationKey),
		ClientBatchIndex: indexState(index.ClientBatchKey),
	}
}

func receiptPurgeAdmissionOutcomeSnapshot(
	receipt firestoreIngestReceipt,
	index firestoreIngestIndex,
	job firestoreReceiptPurgeJob,
	jobExists bool,
	readTime time.Time,
) ingest.ReceiptPurgeAdmissionOutcomeSnapshot {
	state := receiptPurgeAdmissionState(receipt, index)
	return ingest.ReceiptPurgeAdmissionOutcomeSnapshot{
		ReceiptPresent: true, ReservationIndexPresent: true, ClientBatchIndexPresent: true,
		JobPresent: jobExists, Receipt: state.Receipt,
		ReservationIndex: state.ReservationIndex, ClientBatchIndex: state.ClientBatchIndex,
		Job: job.toDomain(), ReadTime: readTime.UTC(),
	}
}

func (transaction firestoreAdmissionTransaction) ReadReceiptPurgeJob(
	ctx context.Context,
	path string,
) (receiptPurgeJobRead, bool, error) {
	document, err := transaction.transaction.Get(transaction.client.Doc(path))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return receiptPurgeJobRead{}, false, nil
		}
		return receiptPurgeJobRead{}, false, normalizeAdmissionError(ctx, err)
	}
	var job firestoreReceiptPurgeJob
	if document == nil || !document.Exists() || document.ReadTime.IsZero() ||
		validateReceiptPurgeJobDocumentShape(document.Data()) != nil || document.DataTo(&job) != nil ||
		ingest.ValidateReceiptPurgeJob(job.toDomain()) != nil {
		return receiptPurgeJobRead{}, false, ingest.ErrReceiptPurgeAdmissionUnavailable
	}
	return receiptPurgeJobRead{Job: job, ReadTime: document.ReadTime.UTC()}, true, nil
}

func receiptPurgeJobDocumentPath(purgeKey string) string {
	return "ingestPurgeJobs/" + purgeKey
}

func validateReceiptPurgeJobDocumentShape(data map[string]any) error {
	required := map[string]struct{}{
		"schema_version": {}, "policy_version": {}, "purge_key": {}, "tenant_id": {},
		"receipt_id": {}, "receipt_revision": {}, "linkage_hash": {}, "status": {},
		"revision": {}, "attempt_deleted_count": {}, "target_deleted_count": {},
		"finding_deleted_count": {}, "created_at": {}, "updated_at": {},
	}
	allowed := map[string]struct{}{
		"schema_version": {}, "policy_version": {}, "purge_key": {}, "tenant_id": {},
		"receipt_id": {}, "receipt_revision": {}, "linkage_hash": {}, "status": {},
		"revision": {}, "attempt_cursor": {}, "attempt_deleted_count": {}, "link_cursor": {},
		"target_deleted_count": {}, "finding_deleted_count": {}, "verified_empty_at": {},
		"linkage_deleted_at": {}, "purge_job_expires_at": {}, "created_at": {}, "updated_at": {},
		"held_from_status": {}, "error_class": {},
	}
	if len(data) < len(required) {
		return ingest.ErrReceiptPurgeAdmissionUnavailable
	}
	for field := range required {
		if _, exists := data[field]; !exists {
			return ingest.ErrReceiptPurgeAdmissionUnavailable
		}
	}
	for field := range data {
		if _, exists := allowed[field]; !exists {
			return ingest.ErrReceiptPurgeAdmissionUnavailable
		}
	}
	return nil
}

func normalizeReceiptPurgeAdmissionError(ctx context.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ingest.ErrInvalidReceiptPurgeAdmission),
		errors.Is(err, ingest.ErrReceiptPurgeAdmissionConflict),
		errors.Is(err, ingest.ErrReceiptPurgeAdmissionUnavailable):
		return err
	default:
		normalized := normalizeAdmissionError(ctx, err)
		if errors.Is(normalized, ingest.ErrAdmissionUnavailable) {
			return ingest.ErrReceiptPurgeAdmissionUnavailable
		}
		return normalized
	}
}

func normalizeReceiptPurgeOutcomeError(ctx context.Context, err error) error {
	if errors.Is(err, ingest.ErrReceiptPurgeAdmissionOutcomeUnavailable) {
		return err
	}
	normalized := normalizeAdmissionError(ctx, err)
	if errors.Is(normalized, ingest.ErrAdmissionUnavailable) {
		return ingest.ErrReceiptPurgeAdmissionOutcomeUnavailable
	}
	return normalized
}
