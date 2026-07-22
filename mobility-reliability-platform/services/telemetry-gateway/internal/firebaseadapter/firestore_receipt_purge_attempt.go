package firebaseadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

type receiptPurgeAttemptRead struct {
	DocumentID     string
	DocumentDigest string
	Attempt        firestoreRecoveryAttempt
	ReadTime       time.Time
	Exists         bool
	ErrorClass     ingest.ReceiptPurgeErrorClass
}

// receiptPurgeAttemptTransaction is intentionally separate from
// admissionTransaction. Existing admission writers do not gain delete
// authority merely because receipt purge exists.
type receiptPurgeAttemptTransaction interface {
	receiptPurgeTransaction
	ReadReceiptPurgeAttempts(
		context.Context,
		string,
		string,
		[]string,
	) ([]receiptPurgeAttemptRead, error)
	QueryReceiptPurgeAttemptPage(
		context.Context,
		ingest.ReceiptPurgePageRequest,
	) ([]string, time.Time, error)
	Delete(context.Context, string) error
}

// ListReceiptPurgeAttemptPage performs bounded advisory discovery only. The
// returned IDs are not delete capabilities; CommitReceiptPurgeAttemptPage
// must reread each exact document in its mutation transaction.
func (s *FirestoreAdmissionStore) ListReceiptPurgeAttemptPage(
	ctx context.Context,
	request ingest.ReceiptPurgePageRequest,
) (ingest.ReceiptPurgePageObservation, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		request.Kind != ingest.ReceiptPurgePageAttempts ||
		ingest.ValidateReceiptPurgePageRequest(request) != nil {
		return ingest.ReceiptPurgePageObservation{}, ingest.ErrInvalidReceiptPurgeJob
	}
	var observation ingest.ReceiptPurgePageObservation
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		transaction, ok := base.(receiptPurgeAttemptTransaction)
		if !ok {
			return ingest.ErrReceiptPurgeMutationUnavailable
		}
		jobRead, exists, readErr := transaction.ReadReceiptPurgeJob(
			runContext, receiptPurgeJobDocumentPath(request.PurgeKey),
		)
		if readErr != nil {
			return readErr
		}
		job := jobRead.Job.toDomain()
		if !exists || ingest.ValidateReceiptPurgeJob(job) != nil ||
			job.PurgeKey != request.PurgeKey || job.TenantID != request.TenantID ||
			job.ReceiptID != request.ReceiptID || job.Status != request.ExpectedJobStatus ||
			job.Revision != request.ExpectedJobRevision || job.AttemptCursor != request.AfterDocumentID {
			return ingest.ErrReceiptPurgeMutationConflict
		}
		documentIDs, queryReadAt, queryErr := transaction.QueryReceiptPurgeAttemptPage(runContext, request)
		if queryErr != nil {
			return queryErr
		}
		readAt := jobRead.ReadTime.UTC()
		if queryReadAt.After(readAt) {
			readAt = queryReadAt.UTC()
		}
		built, buildErr := ingest.BuildReceiptPurgePageObservation(request, documentIDs, readAt)
		if buildErr != nil {
			return buildErr
		}
		observation = built
		return nil
	})
	if err != nil {
		return ingest.ReceiptPurgePageObservation{}, normalizeReceiptPurgeAttemptStoreError(ctx, err)
	}
	return observation, nil
}

func (transaction firestoreAdmissionTransaction) QueryReceiptPurgeAttemptPage(
	ctx context.Context,
	request ingest.ReceiptPurgePageRequest,
) ([]string, time.Time, error) {
	if ctx == nil || transaction.client == nil || transaction.transaction == nil ||
		request.Kind != ingest.ReceiptPurgePageAttempts ||
		ingest.ValidateReceiptPurgePageRequest(request) != nil {
		return nil, time.Time{}, ingest.ErrInvalidReceiptPurgeJob
	}
	query := transaction.client.Collection(receiptPurgeAttemptCollectionPath(request.TenantID, request.ReceiptID)).
		OrderBy(firestore.DocumentID, firestore.Asc).
		Limit(request.PageSize + 1)
	if request.AfterDocumentID != "" {
		query = query.StartAfter(request.AfterDocumentID)
	}
	documents, err := transaction.transaction.Documents(query).GetAll()
	if err != nil {
		return nil, time.Time{}, normalizeAdmissionError(ctx, err)
	}
	documentIDs := make([]string, len(documents))
	var readAt time.Time
	for index, document := range documents {
		if document == nil || document.Ref == nil || document.ReadTime.IsZero() {
			return nil, time.Time{}, ingest.ErrReceiptPurgeAdmissionUnavailable
		}
		documentIDs[index] = document.Ref.ID
		if document.ReadTime.After(readAt) {
			readAt = document.ReadTime.UTC()
		}
	}
	return documentIDs, readAt, nil
}

func receiptPurgeAttemptCollectionPath(tenantID, receiptID string) string {
	return receiptDocumentPath(tenantID, receiptID) + "/recoveryAttempts"
}

func (s *FirestoreAdmissionStore) BeginReceiptPurgeAttempts(
	ctx context.Context,
	command ingest.ReceiptPurgeAttemptPhaseCommand,
) (ingest.ReceiptPurgeMutationResult, error) {
	if command.Action != ingest.ReceiptPurgeAttemptPhaseBegin {
		return ingest.ReceiptPurgeMutationResult{}, ingest.ErrInvalidReceiptPurgeMutation
	}
	return s.commitReceiptPurgeAttemptPhase(ctx, command)
}

func (s *FirestoreAdmissionStore) CompleteReceiptPurgeAttempts(
	ctx context.Context,
	command ingest.ReceiptPurgeAttemptPhaseCommand,
) (ingest.ReceiptPurgeMutationResult, error) {
	if command.Action != ingest.ReceiptPurgeAttemptPhaseComplete ||
		ingest.ValidateReceiptPurgePageObservation(command.EmptyObservation) != nil ||
		len(command.EmptyObservation.DeleteDocumentIDs) != 0 ||
		command.EmptyObservation.LookaheadDocumentID != "" {
		return ingest.ReceiptPurgeMutationResult{}, ingest.ErrInvalidReceiptPurgeMutation
	}
	return s.commitReceiptPurgeAttemptPhase(ctx, command)
}

func (s *FirestoreAdmissionStore) commitReceiptPurgeAttemptPhase(
	ctx context.Context,
	command ingest.ReceiptPurgeAttemptPhaseCommand,
) (ingest.ReceiptPurgeMutationResult, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		!validReceiptPurgeAttemptPhaseCommandIdentity(command) {
		return ingest.ReceiptPurgeMutationResult{}, ingest.ErrInvalidReceiptPurgeMutation
	}
	var result ingest.ReceiptPurgeMutationResult
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		result = ingest.ReceiptPurgeMutationResult{}
		transaction, job, receipt, readTime, loadErr := loadReceiptPurgeAttemptMutationState(
			runContext, base, command.PurgeKey, command.TenantID, command.ReceiptID,
		)
		if loadErr != nil {
			return loadErr
		}
		if job.Revision != command.ExpectedJobRevision {
			return ingest.ErrReceiptPurgeMutationConflict
		}
		if errorClass := ingest.ReceiptPurgeMutationPoisonClass(job, receipt); errorClass != "" {
			effectiveAt, clockErr := receiptPurgeMutationEffectiveAt(s.now().UTC(), readTime)
			if clockErr != nil {
				return ingest.ErrReceiptPurgeMutationUnavailable
			}
			plan, planErr := ingest.PlanReceiptPurgeAttemptHold(job, receipt, errorClass, effectiveAt)
			if planErr != nil {
				return planErr
			}
			result = receiptPurgeMutationResult(plan, ingest.ReceiptPurgeMutationHeld)
			return writeReceiptPurgeJobMutation(runContext, transaction, plan.PreJob, plan.NextJob)
		}
		currentCommand := command
		if command.Action == ingest.ReceiptPurgeAttemptPhaseComplete {
			request := command.EmptyObservation.Request
			documentIDs, queryReadAt, queryErr := transaction.QueryReceiptPurgeAttemptPage(runContext, request)
			if queryErr != nil {
				return queryErr
			}
			if len(documentIDs) != 0 {
				return ingest.ErrReceiptPurgeMutationConflict
			}
			if queryReadAt.After(readTime) {
				readTime = queryReadAt.UTC()
			}
			fresh, buildErr := ingest.BuildReceiptPurgePageObservation(request, nil, readTime)
			if buildErr != nil {
				return buildErr
			}
			currentCommand.EmptyObservation = fresh
		}
		effectiveAt, clockErr := receiptPurgeMutationEffectiveAt(s.now().UTC(), readTime)
		if clockErr != nil {
			return ingest.ErrReceiptPurgeMutationUnavailable
		}
		plan, planErr := ingest.PlanReceiptPurgeAttemptPhase(job, receipt, currentCommand, effectiveAt)
		if planErr != nil {
			return planErr
		}
		result = receiptPurgeMutationResult(plan, ingest.ReceiptPurgeMutationPhaseTransitioned)
		return writeReceiptPurgeJobMutation(runContext, transaction, plan.PreJob, plan.NextJob)
	})
	if err != nil {
		result.Status = ""
		return result, normalizeReceiptPurgeAttemptStoreError(ctx, err)
	}
	if ingest.ValidateReceiptPurgeMutationOutcomeQuery(result.OutcomeQuery) != nil ||
		(result.Status != ingest.ReceiptPurgeMutationPhaseTransitioned &&
			result.Status != ingest.ReceiptPurgeMutationHeld) {
		return ingest.ReceiptPurgeMutationResult{}, ingest.ErrReceiptPurgeMutationUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) CommitReceiptPurgeAttemptPage(
	ctx context.Context,
	observation ingest.ReceiptPurgePageObservation,
) (ingest.ReceiptPurgeMutationResult, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateReceiptPurgePageObservation(observation) != nil ||
		observation.Request.Kind != ingest.ReceiptPurgePageAttempts ||
		len(observation.DeleteDocumentIDs) == 0 {
		return ingest.ReceiptPurgeMutationResult{}, ingest.ErrInvalidReceiptPurgeMutation
	}
	var result ingest.ReceiptPurgeMutationResult
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		result = ingest.ReceiptPurgeMutationResult{}
		request := observation.Request
		transaction, job, receipt, readTime, loadErr := loadReceiptPurgeAttemptMutationState(
			runContext, base, request.PurgeKey, request.TenantID, request.ReceiptID,
		)
		if loadErr != nil {
			return loadErr
		}
		if job.Status != request.ExpectedJobStatus || job.Revision != request.ExpectedJobRevision ||
			job.AttemptCursor != request.AfterDocumentID {
			return ingest.ErrReceiptPurgeMutationConflict
		}
		if errorClass := ingest.ReceiptPurgeMutationPoisonClass(job, receipt); errorClass != "" {
			effectiveAt, clockErr := receiptPurgeMutationEffectiveAt(s.now().UTC(), readTime)
			if clockErr != nil {
				return ingest.ErrReceiptPurgeMutationUnavailable
			}
			plan, planErr := ingest.PlanReceiptPurgeAttemptHold(job, receipt, errorClass, effectiveAt)
			if planErr != nil {
				return planErr
			}
			result = receiptPurgeMutationResult(plan, ingest.ReceiptPurgeMutationHeld)
			return writeReceiptPurgeJobMutation(runContext, transaction, plan.PreJob, plan.NextJob)
		}
		currentIDs, queryReadAt, queryErr := transaction.QueryReceiptPurgeAttemptPage(runContext, request)
		if queryErr != nil {
			return queryErr
		}
		if queryReadAt.After(readTime) {
			readTime = queryReadAt.UTC()
		}
		currentObservation, buildErr := ingest.BuildReceiptPurgePageObservation(
			request, currentIDs, readTime,
		)
		if buildErr != nil {
			return buildErr
		}
		if !sameReceiptPurgePageObservation(observation, currentObservation) {
			return ingest.ErrReceiptPurgeMutationConflict
		}
		reads, readErr := transaction.ReadReceiptPurgeAttempts(
			runContext, request.TenantID, request.ReceiptID, observation.DeleteDocumentIDs,
		)
		if readErr != nil {
			return readErr
		}
		attempts := make([]ingest.ReceiptPurgeAttemptState, 0, len(reads))
		errorClass := ingest.ReceiptPurgeErrorClass("")
		for _, read := range reads {
			if read.ReadTime.After(readTime) {
				readTime = read.ReadTime.UTC()
			}
			if !read.Exists {
				errorClass = ingest.ReceiptPurgeErrorLinkageDrift
				break
			}
			if read.ErrorClass != "" {
				errorClass = read.ErrorClass
				break
			}
			attempt := receiptPurgeAttemptState(
				read.DocumentID, read.DocumentDigest, read.Attempt,
			)
			if ingest.ValidateReceiptPurgeAttemptState(attempt) != nil ||
				attempt.Status == ingest.RecoveryAttemptStarted ||
				receiptPurgeAttemptHasPostFenceTime(read.Attempt, job.CreatedAt) {
				errorClass = ingest.ReceiptPurgeErrorChildMalformed
				break
			}
			attempts = append(attempts, attempt)
		}
		effectiveAt, clockErr := receiptPurgeMutationEffectiveAt(s.now().UTC(), readTime)
		if clockErr != nil {
			return ingest.ErrReceiptPurgeMutationUnavailable
		}
		var plan ingest.ReceiptPurgeMutationPlan
		var planErr error
		if errorClass != "" {
			plan, planErr = ingest.PlanReceiptPurgeAttemptHold(job, receipt, errorClass, effectiveAt)
		} else {
			plan, planErr = ingest.PlanReceiptPurgeAttemptPage(
				job, receipt, observation, attempts, effectiveAt,
			)
		}
		if planErr != nil {
			return planErr
		}
		status := ingest.ReceiptPurgeMutationProgressed
		if plan.Kind == ingest.ReceiptPurgeMutationAttemptHold {
			status = ingest.ReceiptPurgeMutationHeld
		}
		result = receiptPurgeMutationResult(plan, status)
		for _, documentID := range plan.DeleteDocumentIDs {
			if deleteErr := transaction.Delete(
				runContext, recoveryAttemptDocumentPath(request.TenantID, request.ReceiptID, documentID),
			); deleteErr != nil {
				return normalizeAdmissionError(runContext, deleteErr)
			}
		}
		return writeReceiptPurgeJobMutation(runContext, transaction, plan.PreJob, plan.NextJob)
	})
	if err != nil {
		result.Status = ""
		return result, normalizeReceiptPurgeAttemptStoreError(ctx, err)
	}
	if ingest.ValidateReceiptPurgeMutationOutcomeQuery(result.OutcomeQuery) != nil ||
		(result.Status != ingest.ReceiptPurgeMutationProgressed &&
			result.Status != ingest.ReceiptPurgeMutationHeld) {
		return ingest.ReceiptPurgeMutationResult{}, ingest.ErrReceiptPurgeMutationUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) GetReceiptPurgeMutationOutcome(
	ctx context.Context,
	query ingest.ReceiptPurgeMutationOutcomeQuery,
) (ingest.ReceiptPurgeMutationOutcome, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateReceiptPurgeMutationOutcomeQuery(query) != nil {
		return ingest.ReceiptPurgeMutationOutcome{}, ingest.ErrReceiptPurgeMutationOutcomeUnavailable
	}
	var snapshot ingest.ReceiptPurgeMutationOutcomeSnapshot
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		transaction, job, receipt, readTime, loadErr := loadReceiptPurgeAttemptMutationState(
			runContext, base, query.PreJob.PurgeKey, query.PreJob.TenantID, query.PreJob.ReceiptID,
		)
		if loadErr != nil {
			return loadErr
		}
		observations := make([]ingest.ReceiptPurgeAttemptOutcomeObservation, 0, len(query.DeleteDocumentIDs))
		if query.Kind == ingest.ReceiptPurgeMutationAttemptPage {
			reads, readErr := transaction.ReadReceiptPurgeAttempts(
				runContext, query.PreJob.TenantID, query.PreJob.ReceiptID, query.DeleteDocumentIDs,
			)
			if readErr != nil {
				return readErr
			}
			for _, read := range reads {
				if read.ReadTime.After(readTime) {
					readTime = read.ReadTime.UTC()
				}
				observation := ingest.ReceiptPurgeAttemptOutcomeObservation{DocumentID: read.DocumentID}
				if read.Exists {
					attempt := receiptPurgeAttemptState(
						read.DocumentID, read.DocumentDigest, read.Attempt,
					)
					observation.Present = true
					if read.ErrorClass != "" || ingest.ValidateReceiptPurgeAttemptState(attempt) != nil {
						observation.Invalid = true
					} else {
						observation.Attempt = attempt
					}
				}
				observations = append(observations, observation)
			}
		}
		snapshot = ingest.ReceiptPurgeMutationOutcomeSnapshot{
			JobPresent: true, ReceiptPresent: true, Job: job, Receipt: receipt,
			Attempts: observations, ReadTime: readTime,
		}
		return nil
	})
	if err != nil {
		return ingest.ReceiptPurgeMutationOutcome{}, normalizeReceiptPurgeMutationOutcomeError(ctx, err)
	}
	observedAt, clockErr := conservativeAcceptanceTime(s.now().UTC(), snapshot.ReadTime)
	if clockErr != nil {
		return ingest.ReceiptPurgeMutationOutcome{}, ingest.ErrReceiptPurgeMutationOutcomeUnavailable
	}
	outcome, outcomeErr := ingest.EvaluateReceiptPurgeMutationOutcome(query, snapshot, observedAt)
	if outcomeErr != nil {
		return ingest.ReceiptPurgeMutationOutcome{}, outcomeErr
	}
	return outcome, nil
}

func (transaction firestoreAdmissionTransaction) ReadReceiptPurgeAttempts(
	ctx context.Context,
	tenantID string,
	receiptID string,
	documentIDs []string,
) ([]receiptPurgeAttemptRead, error) {
	if ctx == nil || !telemetry.IsUUID(tenantID) || !telemetry.IsUUID(receiptID) ||
		len(documentIDs) > ingest.ReceiptPurgeMaxPageSize {
		return nil, ingest.ErrReceiptPurgeAdmissionUnavailable
	}
	references := make([]*firestore.DocumentRef, len(documentIDs))
	for index, documentID := range documentIDs {
		if !validReceiptPurgeAttemptDocumentID(documentID) {
			return nil, ingest.ErrReceiptPurgeAdmissionUnavailable
		}
		references[index] = transaction.client.Doc(
			recoveryAttemptDocumentPath(tenantID, receiptID, documentID),
		)
	}
	if len(references) == 0 {
		return []receiptPurgeAttemptRead{}, nil
	}
	documents, err := transaction.transaction.GetAll(references)
	if err != nil {
		return nil, normalizeAdmissionError(ctx, err)
	}
	if len(documents) != len(references) {
		return nil, ingest.ErrReceiptPurgeAdmissionUnavailable
	}
	reads := make([]receiptPurgeAttemptRead, len(documents))
	for index, document := range documents {
		read := receiptPurgeAttemptRead{DocumentID: documentIDs[index]}
		if document == nil || document.Ref == nil || document.Ref.ID != documentIDs[index] ||
			document.ReadTime.IsZero() {
			return nil, ingest.ErrReceiptPurgeAdmissionUnavailable
		}
		read.ReadTime = document.ReadTime.UTC()
		if !document.Exists() {
			reads[index] = read
			continue
		}
		read.Exists = true
		data := document.Data()
		if validateReceiptPurgeAttemptDocumentShape(data) != nil ||
			document.DataTo(&read.Attempt) != nil {
			read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
			reads[index] = read
			continue
		}
		read.DocumentDigest = receiptPurgeAttemptDocumentDigest(data)
		if read.DocumentDigest == "" {
			read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
			reads[index] = read
			continue
		}
		read.ErrorClass = classifyReceiptPurgeAttempt(
			document.Ref.ID,
			tenantID,
			receiptID,
			read.Attempt,
		)
		reads[index] = read
	}
	return reads, nil
}

func validReceiptPurgeAttemptDocumentID(value string) bool {
	return value != "" && len(value) <= 1500 && !strings.ContainsAny(value, "/\x00") &&
		value != "." && value != ".."
}

func (transaction firestoreAdmissionTransaction) Delete(_ context.Context, path string) error {
	return transaction.transaction.Delete(transaction.client.Doc(path))
}

func validateReceiptPurgeAttemptDocumentShape(data map[string]any) error {
	for field := range receiptPurgeAttemptRequiredFields {
		if _, exists := data[field]; !exists {
			return ingest.ErrInvalidReceiptPurgeJob
		}
	}
	for field, value := range data {
		if _, allowed := receiptPurgeAttemptAllowedFields[field]; !allowed {
			return ingest.ErrInvalidReceiptPurgeJob
		}
		if !validReceiptPurgeAttemptFieldValue(receiptPurgeAttemptFieldTypes[field], value) {
			return ingest.ErrInvalidReceiptPurgeJob
		}
	}
	return nil
}

var receiptPurgeAttemptRequiredFields = map[string]struct{}{
	"attempt_id": {}, "tenant_id": {}, "receipt_id": {}, "owner_kind": {},
	"fencing_token": {}, "worker_version": {}, "status": {}, "started_at": {},
}

var receiptPurgeAttemptFieldTypes = func() map[string]reflect.Type {
	result := make(map[string]reflect.Type)
	typeOfAttempt := reflect.TypeOf(firestoreRecoveryAttempt{})
	for index := 0; index < typeOfAttempt.NumField(); index++ {
		field := typeOfAttempt.Field(index)
		name := strings.Split(field.Tag.Get("firestore"), ",")[0]
		if name != "" && name != "-" {
			result[name] = field.Type
		}
	}
	return result
}()

var receiptPurgeAttemptAllowedFields = func() map[string]struct{} {
	result := make(map[string]struct{}, len(receiptPurgeAttemptFieldTypes))
	for name := range receiptPurgeAttemptFieldTypes {
		result[name] = struct{}{}
	}
	return result
}()

func validReceiptPurgeAttemptFieldValue(expected reflect.Type, value any) bool {
	if expected == nil || value == nil {
		return false
	}
	if expected == reflect.TypeOf(time.Time{}) {
		_, ok := value.(time.Time)
		return ok
	}
	if expected == reflect.TypeOf((*bool)(nil)) {
		_, ok := value.(bool)
		return ok
	}
	switch expected.Kind() {
	case reflect.String:
		_, ok := value.(string)
		return ok
	case reflect.Int64:
		_, ok := value.(int64)
		return ok
	default:
		return false
	}
}

func classifyReceiptPurgeAttempt(
	documentID string,
	tenantID string,
	receiptID string,
	attempt firestoreRecoveryAttempt,
) ingest.ReceiptPurgeErrorClass {
	if !telemetry.IsUUID(documentID) || attempt.AttemptID != documentID ||
		attempt.TenantID != tenantID || attempt.ReceiptID != receiptID {
		return ingest.ReceiptPurgeErrorChildForeign
	}
	if attempt.FencingToken <= 0 || !validReceiptPurgeAttemptTimestamp(attempt.StartedAt) {
		return ingest.ReceiptPurgeErrorChildMalformed
	}
	switch attempt.OwnerKind {
	case ingest.LeaseOwnerRequest, ingest.LeaseOwnerSweeper:
		if attempt.WorkerVersion != ingest.RecoveryWorkerVersion {
			return ingest.ReceiptPurgeErrorUnsupportedVersion
		}
	case ingest.LeaseOwnerCleanup:
		if attempt.WorkerVersion != ingest.CleanupWorkerVersion ||
			(hasCleanupExecutionLedgerResidue(attempt) &&
				attempt.CleanupSchemaVersion != ingest.CleanupExecutionLedgerSchemaVersion) {
			return ingest.ReceiptPurgeErrorUnsupportedVersion
		}
	default:
		return ingest.ReceiptPurgeErrorChildMalformed
	}
	switch attempt.Status {
	case ingest.RecoveryAttemptStarted:
		if attempt.Outcome != "" || !attempt.CompletedAt.IsZero() ||
			attempt.FailureCode != "" || !attempt.FailedAt.IsZero() {
			return ingest.ReceiptPurgeErrorChildMalformed
		}
	case ingest.RecoveryAttemptFailed:
		if attempt.Outcome != "" || !ingest.ValidRecoveryAttemptFailureCode(attempt.FailureCode) ||
			!validReceiptPurgeAttemptTimestamp(attempt.FailedAt) ||
			!attempt.FailedAt.After(attempt.StartedAt) || !attempt.CompletedAt.IsZero() ||
			!validReceiptPurgeFailedAttempt(attempt) {
			return ingest.ReceiptPurgeErrorChildMalformed
		}
	case ingest.RecoveryAttemptCompleted:
		if !validReceiptPurgeAttemptTimestamp(attempt.CompletedAt) ||
			!attempt.CompletedAt.After(attempt.StartedAt) || attempt.FailureCode != "" ||
			!attempt.FailedAt.IsZero() || !validReceiptPurgeCompletedAttempt(attempt) {
			return ingest.ReceiptPurgeErrorChildMalformed
		}
	default:
		return ingest.ReceiptPurgeErrorChildMalformed
	}
	return ""
}

func validReceiptPurgeFailedAttempt(attempt firestoreRecoveryAttempt) bool {
	switch attempt.OwnerKind {
	case ingest.LeaseOwnerRequest, ingest.LeaseOwnerSweeper:
		return !receiptPurgeAttemptHasSemanticResidue(attempt)
	case ingest.LeaseOwnerCleanup:
		if attempt.FailureCode != ingest.RecoveryAttemptFailureLeaseExpired ||
			receiptPurgeAttemptHasForwardResidue(attempt) {
			return false
		}
		if !hasCleanupExecutionLedgerResidue(attempt) {
			return attempt.DecisionDomain == ""
		}
		return validReceiptPurgeFailedCleanupAttempt(attempt)
	default:
		return false
	}
}

func validReceiptPurgeFailedCleanupAttempt(attempt firestoreRecoveryAttempt) bool {
	if attempt.DecisionDomain != ingest.CleanupExecutionDecisionExpiry ||
		attempt.CleanupSchemaVersion != ingest.CleanupExecutionLedgerSchemaVersion ||
		!validReceiptPurgeDigest(attempt.CleanupTargetHash) ||
		!validReceiptPurgeDigest(attempt.CleanupPlanHash) ||
		attempt.CleanupReceiptRevision <= 0 || attempt.CleanupRawTargeted == nil ||
		attempt.CleanupManifestTargeted == nil || attempt.CleanupDisposition != "" ||
		attempt.CleanupEvidenceHash != "" ||
		attempt.CleanupExecutionRevision != receiptPurgeCleanupPhaseRevision(attempt.CleanupPhase) {
		return false
	}
	raw := receiptPurgeCleanupArtifact{
		targeted: *attempt.CleanupRawTargeted, dispatchedAt: attempt.CleanupRawDispatchAt,
		deleteOutcome:     attempt.CleanupRawDeleteOutcome,
		outcomeRecordedAt: attempt.CleanupRawOutcomeRecordedAt,
		auditOutcome:      attempt.CleanupRawAuditOutcome, auditedAt: attempt.CleanupRawAuditedAt,
	}
	manifest := receiptPurgeCleanupArtifact{
		targeted: *attempt.CleanupManifestTargeted, dispatchedAt: attempt.CleanupManifestDispatchAt,
		deleteOutcome:     attempt.CleanupManifestDeleteOutcome,
		outcomeRecordedAt: attempt.CleanupManifestOutcomeRecordedAt,
		auditOutcome:      attempt.CleanupManifestAuditOutcome, auditedAt: attempt.CleanupManifestAuditedAt,
	}
	return validReceiptPurgeCleanupNonterminal(
		attempt.CleanupPhase, raw, manifest, attempt.CleanupErrorClass,
		attempt.StartedAt, attempt.FailedAt,
	)
}

func validReceiptPurgeCleanupNonterminal(
	phase ingest.CleanupExecutionPhase,
	raw receiptPurgeCleanupArtifact,
	manifest receiptPurgeCleanupArtifact,
	errorClass ingest.CleanupExecutionErrorClass,
	startedAt time.Time,
	observedAt time.Time,
) bool {
	switch phase {
	case ingest.CleanupExecutionPhasePlanned:
		return errorClass == "" && emptyReceiptPurgeCleanupArtifact(raw) &&
			emptyReceiptPurgeCleanupArtifact(manifest)
	case ingest.CleanupExecutionPhaseRawDispatchRecorded:
		return errorClass == "" &&
			validReceiptPurgeCleanupDispatchOnly(raw, startedAt, observedAt) &&
			emptyReceiptPurgeCleanupArtifact(manifest)
	case ingest.CleanupExecutionPhaseRawOutcomeRecorded:
		return validReceiptPurgeCleanupOutcome(raw, startedAt, observedAt) &&
			emptyReceiptPurgeCleanupArtifact(manifest) &&
			validReceiptPurgeCleanupProgressError(raw.deleteOutcome, errorClass)
	case ingest.CleanupExecutionPhaseRawAbsenceConfirmed:
		return errorClass == "" &&
			validReceiptPurgeCleanupAudited(raw, startedAt, observedAt) &&
			raw.deleteOutcome != ingest.CleanupDeleteUnknown &&
			emptyReceiptPurgeCleanupArtifact(manifest)
	case ingest.CleanupExecutionPhaseManifestDispatchRecorded:
		return errorClass == "" &&
			validReceiptPurgeCleanupAudited(raw, startedAt, observedAt) &&
			raw.deleteOutcome != ingest.CleanupDeleteUnknown &&
			validReceiptPurgeCleanupDispatchOnly(manifest, startedAt, observedAt) &&
			!manifest.dispatchedAt.Before(raw.auditedAt)
	case ingest.CleanupExecutionPhaseManifestOutcomeRecorded:
		return validReceiptPurgeCleanupAudited(raw, startedAt, observedAt) &&
			raw.deleteOutcome != ingest.CleanupDeleteUnknown &&
			validReceiptPurgeCleanupOutcome(manifest, startedAt, observedAt) &&
			!manifest.dispatchedAt.Before(raw.auditedAt) &&
			validReceiptPurgeCleanupProgressError(manifest.deleteOutcome, errorClass)
	case ingest.CleanupExecutionPhaseManifestAbsenceConfirmed:
		return errorClass == "" &&
			validReceiptPurgeCleanupAudited(raw, startedAt, observedAt) &&
			raw.deleteOutcome != ingest.CleanupDeleteUnknown &&
			validReceiptPurgeCleanupAudited(manifest, startedAt, observedAt) &&
			manifest.deleteOutcome != ingest.CleanupDeleteUnknown &&
			!manifest.dispatchedAt.Before(raw.auditedAt)
	default:
		return false
	}
}

func validReceiptPurgeCleanupProgressError(
	outcome ingest.CleanupDeleteRPCOutcome,
	errorClass ingest.CleanupExecutionErrorClass,
) bool {
	if outcome != ingest.CleanupDeleteUnknown {
		return errorClass == ""
	}
	return validReceiptPurgeCleanupAmbiguousOutcome(outcome, errorClass)
}

func validReceiptPurgeCompletedAttempt(attempt firestoreRecoveryAttempt) bool {
	switch attempt.OwnerKind {
	case ingest.LeaseOwnerRequest, ingest.LeaseOwnerSweeper:
		projected, err := currentForwardRecoveryOutcomeAttempt(attempt)
		return err == nil && ingest.ValidateCompletedForwardRecoveryAttemptForPurge(projected) == nil
	case ingest.LeaseOwnerCleanup:
		return validReceiptPurgeCompletedCleanupAttempt(attempt)
	default:
		return false
	}
}

func validReceiptPurgeCompletedCleanupAttempt(attempt firestoreRecoveryAttempt) bool {
	if attempt.DecisionDomain != ingest.CleanupExecutionDecisionExpiry ||
		attempt.CleanupSchemaVersion != ingest.CleanupExecutionLedgerSchemaVersion ||
		!validReceiptPurgeDigest(attempt.CleanupTargetHash) ||
		!validReceiptPurgeDigest(attempt.CleanupPlanHash) ||
		attempt.CleanupReceiptRevision <= 0 || attempt.CleanupRawTargeted == nil ||
		attempt.CleanupManifestTargeted == nil || receiptPurgeAttemptHasForwardResidue(attempt) ||
		attempt.CleanupExecutionRevision != receiptPurgeCleanupPhaseRevision(attempt.CleanupPhase) ||
		!validReceiptPurgeCleanupTerminal(attempt) {
		return false
	}
	return true
}

func validReceiptPurgeCleanupTerminal(attempt firestoreRecoveryAttempt) bool {
	raw := receiptPurgeCleanupArtifact{
		targeted: *attempt.CleanupRawTargeted, dispatchedAt: attempt.CleanupRawDispatchAt,
		deleteOutcome:     attempt.CleanupRawDeleteOutcome,
		outcomeRecordedAt: attempt.CleanupRawOutcomeRecordedAt,
		auditOutcome:      attempt.CleanupRawAuditOutcome, auditedAt: attempt.CleanupRawAuditedAt,
	}
	manifest := receiptPurgeCleanupArtifact{
		targeted: *attempt.CleanupManifestTargeted, dispatchedAt: attempt.CleanupManifestDispatchAt,
		deleteOutcome:     attempt.CleanupManifestDeleteOutcome,
		outcomeRecordedAt: attempt.CleanupManifestOutcomeRecordedAt,
		auditOutcome:      attempt.CleanupManifestAuditOutcome, auditedAt: attempt.CleanupManifestAuditedAt,
	}
	if !validReceiptPurgeDigest(attempt.CleanupEvidenceHash) {
		return false
	}
	switch attempt.Outcome {
	case ingest.RecoveryAttemptOutcomeExpired:
		return attempt.CleanupPhase == ingest.CleanupExecutionPhaseCompleted &&
			attempt.CleanupDisposition == ingest.CleanupExecutionDispositionComplete &&
			attempt.CleanupErrorClass == "" &&
			validReceiptPurgeCleanupAudited(raw, attempt.StartedAt, attempt.CompletedAt) &&
			validReceiptPurgeCleanupAudited(manifest, attempt.StartedAt, attempt.CompletedAt) &&
			raw.deleteOutcome != ingest.CleanupDeleteUnknown &&
			manifest.deleteOutcome != ingest.CleanupDeleteUnknown &&
			!manifest.dispatchedAt.Before(raw.auditedAt)
	case ingest.RecoveryAttemptOutcomeCleanupRetry, ingest.RecoveryAttemptOutcomeCleanupHold:
		expected := ingest.CleanupExecutionDispositionRetry
		if attempt.Outcome == ingest.RecoveryAttemptOutcomeCleanupHold {
			expected = ingest.CleanupExecutionDispositionHold
		}
		policy, err := ingest.CleanupExecutionFailurePolicyFor(attempt.CleanupErrorClass)
		return err == nil && policy.Disposition == expected &&
			attempt.CleanupDisposition == expected &&
			validReceiptPurgeCleanupDispositionPhase(
				attempt.CleanupPhase, raw, manifest, attempt.CleanupErrorClass,
				attempt.StartedAt, attempt.CompletedAt,
			)
	default:
		return false
	}
}

type receiptPurgeCleanupArtifact struct {
	targeted          bool
	dispatchedAt      time.Time
	deleteOutcome     ingest.CleanupDeleteRPCOutcome
	outcomeRecordedAt time.Time
	auditOutcome      ingest.CleanupAuditOutcome
	auditedAt         time.Time
}

func validReceiptPurgeCleanupDispositionPhase(
	phase ingest.CleanupExecutionPhase,
	raw receiptPurgeCleanupArtifact,
	manifest receiptPurgeCleanupArtifact,
	errorClass ingest.CleanupExecutionErrorClass,
	startedAt time.Time,
	completedAt time.Time,
) bool {
	switch phase {
	case ingest.CleanupExecutionPhaseRawDispatchRecorded:
		return validReceiptPurgeCleanupDispatchOnly(raw, startedAt, completedAt) &&
			emptyReceiptPurgeCleanupArtifact(manifest)
	case ingest.CleanupExecutionPhaseRawOutcomeRecorded:
		return validReceiptPurgeCleanupOutcome(raw, startedAt, completedAt) &&
			emptyReceiptPurgeCleanupArtifact(manifest) &&
			validReceiptPurgeCleanupAmbiguousOutcome(raw.deleteOutcome, errorClass)
	case ingest.CleanupExecutionPhaseManifestDispatchRecorded:
		return validReceiptPurgeCleanupAudited(raw, startedAt, completedAt) &&
			raw.deleteOutcome != ingest.CleanupDeleteUnknown &&
			validReceiptPurgeCleanupDispatchOnly(manifest, startedAt, completedAt) &&
			!manifest.dispatchedAt.Before(raw.auditedAt)
	case ingest.CleanupExecutionPhaseManifestOutcomeRecorded:
		return validReceiptPurgeCleanupAudited(raw, startedAt, completedAt) &&
			raw.deleteOutcome != ingest.CleanupDeleteUnknown &&
			validReceiptPurgeCleanupOutcome(manifest, startedAt, completedAt) &&
			!manifest.dispatchedAt.Before(raw.auditedAt) &&
			validReceiptPurgeCleanupAmbiguousOutcome(manifest.deleteOutcome, errorClass)
	default:
		return false
	}
}

func validReceiptPurgeCleanupAmbiguousOutcome(
	outcome ingest.CleanupDeleteRPCOutcome,
	errorClass ingest.CleanupExecutionErrorClass,
) bool {
	if outcome != ingest.CleanupDeleteUnknown {
		return true
	}
	switch errorClass {
	case ingest.CleanupExecutionErrorProviderTimeout,
		ingest.CleanupExecutionErrorProviderCancelled,
		ingest.CleanupExecutionErrorProviderUnavailable,
		ingest.CleanupExecutionErrorResponseUnverifiable:
		return true
	default:
		return false
	}
}

func validReceiptPurgeCleanupDispatchOnly(
	record receiptPurgeCleanupArtifact,
	startedAt time.Time,
	observedAt time.Time,
) bool {
	return validReceiptPurgeAttemptTimestamp(record.dispatchedAt) &&
		!record.dispatchedAt.Before(startedAt) && !record.dispatchedAt.After(observedAt) &&
		record.deleteOutcome == "" && record.outcomeRecordedAt.IsZero() &&
		record.auditOutcome == "" && record.auditedAt.IsZero()
}

func validReceiptPurgeCleanupOutcome(
	record receiptPurgeCleanupArtifact,
	startedAt time.Time,
	observedAt time.Time,
) bool {
	return validReceiptPurgeAttemptTimestamp(record.dispatchedAt) &&
		!record.dispatchedAt.Before(startedAt) && !record.dispatchedAt.After(observedAt) &&
		validReceiptPurgeCleanupDeleteOutcome(record.targeted, record.deleteOutcome) &&
		validReceiptPurgeAttemptTimestamp(record.outcomeRecordedAt) &&
		!record.outcomeRecordedAt.Before(record.dispatchedAt) &&
		!record.outcomeRecordedAt.After(observedAt) &&
		record.auditOutcome == "" && record.auditedAt.IsZero()
}

func validReceiptPurgeCleanupAudited(
	record receiptPurgeCleanupArtifact,
	startedAt time.Time,
	observedAt time.Time,
) bool {
	prior := record
	prior.auditOutcome = ""
	prior.auditedAt = time.Time{}
	return validReceiptPurgeCleanupOutcome(prior, startedAt, observedAt) &&
		record.auditOutcome == ingest.CleanupAuditConfirmedAbsent &&
		validReceiptPurgeAttemptTimestamp(record.auditedAt) &&
		!record.auditedAt.Before(record.outcomeRecordedAt) &&
		!record.auditedAt.After(observedAt)
}

func validReceiptPurgeCleanupDeleteOutcome(
	targeted bool,
	outcome ingest.CleanupDeleteRPCOutcome,
) bool {
	if !targeted {
		return outcome == ingest.CleanupDeleteNotAttempted
	}
	switch outcome {
	case ingest.CleanupDeleteNotAttempted, ingest.CleanupDeleteObserved,
		ingest.CleanupDeleteNotFound, ingest.CleanupDeleteUnknown:
		return true
	default:
		return false
	}
}

func emptyReceiptPurgeCleanupArtifact(record receiptPurgeCleanupArtifact) bool {
	return record.dispatchedAt.IsZero() && record.deleteOutcome == "" &&
		record.outcomeRecordedAt.IsZero() && record.auditOutcome == "" && record.auditedAt.IsZero()
}

func receiptPurgeCleanupPhaseRevision(phase ingest.CleanupExecutionPhase) int64 {
	switch phase {
	case ingest.CleanupExecutionPhasePlanned:
		return 1
	case ingest.CleanupExecutionPhaseRawDispatchRecorded:
		return 2
	case ingest.CleanupExecutionPhaseRawOutcomeRecorded:
		return 3
	case ingest.CleanupExecutionPhaseRawAbsenceConfirmed:
		return 4
	case ingest.CleanupExecutionPhaseManifestDispatchRecorded:
		return 5
	case ingest.CleanupExecutionPhaseManifestOutcomeRecorded:
		return 6
	case ingest.CleanupExecutionPhaseManifestAbsenceConfirmed:
		return 7
	case ingest.CleanupExecutionPhaseCompleted:
		return 8
	default:
		return 0
	}
}

func receiptPurgeAttemptHasSemanticResidue(attempt firestoreRecoveryAttempt) bool {
	return attempt.DecisionDomain != "" || receiptPurgeAttemptHasForwardResidue(attempt) ||
		hasCleanupExecutionLedgerResidue(attempt)
}

func receiptPurgeAttemptHasForwardResidue(attempt firestoreRecoveryAttempt) bool {
	return attempt.AuthorizationDisposition != "" || attempt.Phase != "" ||
		attempt.Classification != "" || attempt.ReasonCode != "" || attempt.Action != "" ||
		attempt.ActionHash != "" || attempt.HoldCode != "" || attempt.ReleaseCode != "" ||
		attempt.RejectionCode != "" || attempt.RawSHA256 != "" || attempt.RawCRC32C != 0 ||
		attempt.RawSize != 0 || attempt.RawGeneration != 0 || attempt.RawMetageneration != 0 ||
		attempt.ManifestSHA256 != "" || attempt.ManifestCRC32C != 0 ||
		attempt.ManifestSize != 0 || attempt.ManifestGeneration != 0 ||
		attempt.ManifestMetageneration != 0 || !attempt.HoldReviewDueAt.IsZero()
}

func validReceiptPurgeDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validReceiptPurgeAttemptTimestamp(value time.Time) bool {
	return !value.IsZero() && value.Equal(value.UTC()) && value.Year() >= 2000 && value.Year() <= 9999
}

func loadReceiptPurgeAttemptMutationState(
	ctx context.Context,
	base admissionTransaction,
	purgeKey string,
	tenantID string,
	receiptID string,
) (
	receiptPurgeAttemptTransaction,
	ingest.ReceiptPurgeJob,
	ingest.ReceiptPurgeReceiptState,
	time.Time,
	error,
) {
	transaction, ok := base.(receiptPurgeAttemptTransaction)
	if !ok || ctx == nil || !telemetry.IsUUID(tenantID) || !telemetry.IsUUID(receiptID) {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{}, time.Time{},
			ingest.ErrReceiptPurgeMutationUnavailable
	}
	derivedKey, keyErr := ingest.DeriveReceiptPurgeKey(tenantID, receiptID)
	if keyErr != nil || purgeKey != derivedKey {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{}, time.Time{},
			ingest.ErrInvalidReceiptPurgeMutation
	}
	jobRead, jobExists, jobErr := transaction.ReadReceiptPurgeJob(
		ctx, receiptPurgeJobDocumentPath(purgeKey),
	)
	if jobErr != nil {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{}, time.Time{}, jobErr
	}
	receiptRead, receiptExists, receiptErr := transaction.ReadReceipt(
		ctx, receiptDocumentPath(tenantID, receiptID),
	)
	if receiptErr != nil {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{}, time.Time{}, receiptErr
	}
	if !jobExists || !receiptExists || jobRead.ReadTime.IsZero() || receiptRead.ReadTime.IsZero() {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{}, time.Time{},
			ingest.ErrReceiptPurgeMutationUnavailable
	}
	job := jobRead.Job.toDomain()
	if ingest.ValidateReceiptPurgeJob(job) != nil || job.PurgeKey != purgeKey ||
		job.TenantID != tenantID || job.ReceiptID != receiptID {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{}, time.Time{},
			ingest.ErrReceiptPurgeMutationUnavailable
	}
	readTime := jobRead.ReadTime.UTC()
	if receiptRead.ReadTime.After(readTime) {
		readTime = receiptRead.ReadTime.UTC()
	}
	return transaction, job, receiptPurgeReceiptStateForMutation(receiptRead.Receipt), readTime, nil
}

func receiptPurgeReceiptStateForMutation(receipt firestoreIngestReceipt) ingest.ReceiptPurgeReceiptState {
	purgeEligibleAt := time.Time{}
	if receipt.PurgeEligibleAt != nil {
		purgeEligibleAt = receipt.PurgeEligibleAt.UTC()
	}
	return ingest.ReceiptPurgeReceiptState{
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
}

func receiptPurgeAttemptState(
	documentID string,
	documentDigest string,
	attempt firestoreRecoveryAttempt,
) ingest.ReceiptPurgeAttemptState {
	return ingest.ReceiptPurgeAttemptState{
		DocumentID: documentID, AttemptID: attempt.AttemptID,
		TenantID: attempt.TenantID, ReceiptID: attempt.ReceiptID,
		OwnerKind: attempt.OwnerKind, FencingToken: attempt.FencingToken,
		WorkerVersion: attempt.WorkerVersion, DocumentDigest: documentDigest,
		Status:  attempt.Status,
		Outcome: attempt.Outcome, FailureCode: attempt.FailureCode,
		StartedAt: attempt.StartedAt.UTC(), CompletedAt: attempt.CompletedAt.UTC(),
		FailedAt: attempt.FailedAt.UTC(),
	}
}

func receiptPurgeAttemptDocumentDigest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append(
		[]byte("ingest-receipt-purge-attempt-document@1\x00"), encoded...,
	))
	return hex.EncodeToString(digest[:])
}

func receiptPurgeAttemptHasPostFenceTime(attempt firestoreRecoveryAttempt, purgeStartedAt time.Time) bool {
	if purgeStartedAt.IsZero() {
		return true
	}
	value := reflect.ValueOf(attempt)
	timeType := reflect.TypeOf(time.Time{})
	for index := 0; index < value.NumField(); index++ {
		if value.Type().Field(index).Type != timeType {
			continue
		}
		observed := value.Field(index).Interface().(time.Time)
		if !observed.IsZero() && observed.After(purgeStartedAt) {
			return true
		}
	}
	return false
}

func receiptPurgeMutationResult(
	plan ingest.ReceiptPurgeMutationPlan,
	status ingest.ReceiptPurgeMutationStatus,
) ingest.ReceiptPurgeMutationResult {
	return ingest.ReceiptPurgeMutationResult{
		Job: plan.NextJob, OutcomeQuery: plan.OutcomeQuery, Status: status,
	}
}

func writeReceiptPurgeJobMutation(
	ctx context.Context,
	transaction receiptPurgeAttemptTransaction,
	preJob ingest.ReceiptPurgeJob,
	nextJob ingest.ReceiptPurgeJob,
) error {
	if ctx == nil || transaction == nil || ingest.ValidateReceiptPurgeJob(preJob) != nil ||
		ingest.ValidateReceiptPurgeJob(nextJob) != nil || nextJob.Revision != preJob.Revision+1 {
		return ingest.ErrReceiptPurgeMutationUnavailable
	}
	updates := []firestore.Update{
		{Path: "status", Value: string(nextJob.Status)},
		{Path: "revision", Value: nextJob.Revision},
		{Path: "updated_at", Value: nextJob.UpdatedAt.UTC()},
	}
	if nextJob.AttemptCursor != preJob.AttemptCursor {
		updates = append(updates, firestore.Update{Path: "attempt_cursor", Value: nextJob.AttemptCursor})
	}
	if nextJob.AttemptDeletedCount != preJob.AttemptDeletedCount {
		updates = append(updates, firestore.Update{
			Path: "attempt_deleted_count", Value: nextJob.AttemptDeletedCount,
		})
	}
	if nextJob.Status == ingest.ReceiptPurgeJobHold {
		updates = append(updates,
			firestore.Update{Path: "held_from_status", Value: string(nextJob.HeldFromStatus)},
			firestore.Update{Path: "error_class", Value: string(nextJob.ErrorClass)},
		)
	}
	if updateErr := transaction.Update(
		ctx, receiptPurgeJobDocumentPath(preJob.PurgeKey), updates,
	); updateErr != nil {
		return normalizeAdmissionError(ctx, updateErr)
	}
	return nil
}

func validReceiptPurgeAttemptPhaseCommandIdentity(command ingest.ReceiptPurgeAttemptPhaseCommand) bool {
	key, err := ingest.DeriveReceiptPurgeKey(command.TenantID, command.ReceiptID)
	if err != nil || command.PurgeKey != key || command.ExpectedJobRevision <= 0 {
		return false
	}
	switch command.Action {
	case ingest.ReceiptPurgeAttemptPhaseBegin:
		return len(command.EmptyObservation.DeleteDocumentIDs) == 0 &&
			command.EmptyObservation.LookaheadDocumentID == "" &&
			command.EmptyObservation.ReadAt.IsZero() &&
			command.EmptyObservation.Request == (ingest.ReceiptPurgePageRequest{})
	case ingest.ReceiptPurgeAttemptPhaseComplete:
		return ingest.ValidateReceiptPurgePageObservation(command.EmptyObservation) == nil &&
			len(command.EmptyObservation.DeleteDocumentIDs) == 0 &&
			command.EmptyObservation.LookaheadDocumentID == ""
	default:
		return false
	}
}

func sameReceiptPurgePageObservation(
	left ingest.ReceiptPurgePageObservation,
	right ingest.ReceiptPurgePageObservation,
) bool {
	if left.Request != right.Request || left.LookaheadDocumentID != right.LookaheadDocumentID ||
		len(left.DeleteDocumentIDs) != len(right.DeleteDocumentIDs) {
		return false
	}
	for index := range left.DeleteDocumentIDs {
		if left.DeleteDocumentIDs[index] != right.DeleteDocumentIDs[index] {
			return false
		}
	}
	return true
}

func receiptPurgeMutationEffectiveAt(applicationTime, readTime time.Time) (time.Time, error) {
	effectiveAt, err := conservativeAcceptanceTime(applicationTime.UTC(), readTime.UTC())
	if err != nil {
		return time.Time{}, err
	}
	// Firestore timestamps round-trip at microsecond precision. Seal the exact
	// value that the outcome reader can recover after a lost commit response.
	effectiveAt = effectiveAt.Truncate(time.Microsecond)
	if effectiveAt.Before(readTime) {
		return time.Time{}, ingest.ErrReceiptPurgeMutationUnavailable
	}
	return effectiveAt, nil
}

func normalizeReceiptPurgeAttemptStoreError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ingest.ErrInvalidReceiptPurgeMutation) ||
		errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) ||
		errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) {
		return err
	}
	if status.Code(err) == codes.Aborted {
		return ingest.ErrReceiptPurgeMutationConflict
	}
	normalized := normalizeAdmissionError(ctx, err)
	if errors.Is(normalized, ingest.ErrAdmissionUnavailable) {
		return ingest.ErrReceiptPurgeMutationUnavailable
	}
	return normalized
}

func normalizeReceiptPurgeMutationOutcomeError(ctx context.Context, err error) error {
	if errors.Is(err, ingest.ErrReceiptPurgeMutationOutcomeUnavailable) {
		return err
	}
	normalized := normalizeReceiptPurgeAttemptStoreError(ctx, err)
	if errors.Is(normalized, ingest.ErrReceiptPurgeMutationConflict) ||
		errors.Is(normalized, ingest.ErrReceiptPurgeMutationUnavailable) ||
		errors.Is(normalized, ingest.ErrInvalidReceiptPurgeMutation) {
		return ingest.ErrReceiptPurgeMutationOutcomeUnavailable
	}
	return normalized
}
