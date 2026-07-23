package firebaseadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

type receiptPurgeLinkedRead struct {
	LinkDocumentID string
	LinkPresent    bool
	ChildPresent   bool
	State          ingest.ReceiptPurgeLinkedChildState
	ReadTime       time.Time
	ErrorClass     ingest.ReceiptPurgeErrorClass
}

// receiptPurgeLinkedTransaction is deliberately separate from target writers.
// An inverse link is advisory until this transaction rereads the current job,
// receipt fence, exact link and exact top-level child.
type receiptPurgeLinkedTransaction interface {
	receiptPurgeTransaction
	Delete(context.Context, string) error
	QueryReceiptPurgeLinkPage(
		context.Context,
		ingest.ReceiptPurgePageRequest,
	) ([]string, time.Time, error)
	ReadReceiptPurgeLinkedPage(
		context.Context,
		ingest.ReceiptPurgeJob,
		firestoreIngestReceipt,
		[]string,
	) ([]receiptPurgeLinkedRead, error)
	ReadReceiptPurgeLinkedOutcome(
		context.Context,
		ingest.ReceiptPurgeJob,
		firestoreIngestReceipt,
		[]ingest.ReceiptPurgeLinkedChildState,
	) ([]receiptPurgeLinkedRead, error)
}

// ListReceiptPurgeLinkedPage performs bounded advisory discovery only.
func (s *FirestoreAdmissionStore) ListReceiptPurgeLinkedPage(
	ctx context.Context,
	request ingest.ReceiptPurgePageRequest,
) (ingest.ReceiptPurgePageObservation, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		request.Kind != ingest.ReceiptPurgePageLinks ||
		ingest.ValidateReceiptPurgePageRequest(request) != nil {
		return ingest.ReceiptPurgePageObservation{}, ingest.ErrInvalidReceiptPurgeJob
	}
	var observation ingest.ReceiptPurgePageObservation
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		transaction, ok := base.(receiptPurgeLinkedTransaction)
		if !ok {
			return ingest.ErrReceiptPurgeMutationUnavailable
		}
		jobRead, exists, readErr := transaction.ReadReceiptPurgeJob(
			runContext,
			receiptPurgeJobDocumentPath(request.PurgeKey),
		)
		if readErr != nil {
			return readErr
		}
		job := jobRead.Job.toDomain()
		if !exists || ingest.ValidateReceiptPurgeJob(job) != nil ||
			job.PurgeKey != request.PurgeKey || job.TenantID != request.TenantID ||
			job.ReceiptID != request.ReceiptID || job.Status != request.ExpectedJobStatus ||
			job.Revision != request.ExpectedJobRevision || job.LinkCursor != request.AfterDocumentID {
			return ingest.ErrReceiptPurgeMutationConflict
		}
		documentIDs, queryReadAt, queryErr := transaction.QueryReceiptPurgeLinkPage(
			runContext,
			request,
		)
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
		return ingest.ReceiptPurgePageObservation{}, normalizeReceiptPurgeLinkedStoreError(ctx, err)
	}
	return observation, nil
}

func (transaction firestoreAdmissionTransaction) QueryReceiptPurgeLinkPage(
	ctx context.Context,
	request ingest.ReceiptPurgePageRequest,
) ([]string, time.Time, error) {
	if ctx == nil || transaction.client == nil || transaction.transaction == nil ||
		request.Kind != ingest.ReceiptPurgePageLinks ||
		ingest.ValidateReceiptPurgePageRequest(request) != nil {
		return nil, time.Time{}, ingest.ErrInvalidReceiptPurgeJob
	}
	query := transaction.client.Collection(receiptPurgeLinkCollectionPath(request.TenantID, request.ReceiptID)).
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
			return nil, time.Time{}, ingest.ErrReceiptPurgeMutationUnavailable
		}
		documentIDs[index] = document.Ref.ID
		if document.ReadTime.After(readAt) {
			readAt = document.ReadTime.UTC()
		}
	}
	return documentIDs, readAt, nil
}

func receiptPurgeLinkCollectionPath(tenantID, receiptID string) string {
	return receiptDocumentPath(tenantID, receiptID) + "/purgeLinks"
}

func (s *FirestoreAdmissionStore) CommitReceiptPurgeLinkedPage(
	ctx context.Context,
	observation ingest.ReceiptPurgePageObservation,
) (ingest.ReceiptPurgeLinkedMutationResult, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateReceiptPurgePageObservation(observation) != nil ||
		observation.Request.Kind != ingest.ReceiptPurgePageLinks ||
		len(observation.DeleteDocumentIDs) == 0 {
		return ingest.ReceiptPurgeLinkedMutationResult{}, ingest.ErrInvalidReceiptPurgeMutation
	}
	var result ingest.ReceiptPurgeLinkedMutationResult
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		result = ingest.ReceiptPurgeLinkedMutationResult{}
		request := observation.Request
		transaction, job, receipt, rawReceipt, readTime, loadErr := loadReceiptPurgeLinkedMutationState(
			runContext,
			base,
			request.PurgeKey,
			request.TenantID,
			request.ReceiptID,
		)
		if loadErr != nil {
			return loadErr
		}
		if job.Status != request.ExpectedJobStatus || job.Revision != request.ExpectedJobRevision ||
			job.LinkCursor != request.AfterDocumentID {
			return ingest.ErrReceiptPurgeMutationConflict
		}
		if errorClass := ingest.ReceiptPurgeMutationPoisonClass(job, receipt); errorClass != "" {
			effectiveAt, clockErr := receiptPurgeMutationEffectiveAt(s.now().UTC(), readTime)
			if clockErr != nil {
				return ingest.ErrReceiptPurgeMutationUnavailable
			}
			plan, planErr := ingest.PlanReceiptPurgeLinkedHold(job, receipt, errorClass, effectiveAt)
			if planErr != nil {
				return planErr
			}
			result = receiptPurgeLinkedMutationResult(plan, ingest.ReceiptPurgeMutationHeld)
			return writeReceiptPurgeJobMutation(runContext, transaction, plan.PreJob, plan.NextJob)
		}

		currentIDs, queryReadAt, queryErr := transaction.QueryReceiptPurgeLinkPage(runContext, request)
		if queryErr != nil {
			return queryErr
		}
		if queryReadAt.After(readTime) {
			readTime = queryReadAt.UTC()
		}
		currentObservation, buildErr := ingest.BuildReceiptPurgePageObservation(
			request,
			currentIDs,
			readTime,
		)
		if buildErr != nil {
			return buildErr
		}
		if !sameReceiptPurgePageObservation(observation, currentObservation) {
			return ingest.ErrReceiptPurgeMutationConflict
		}

		reads, readErr := transaction.ReadReceiptPurgeLinkedPage(
			runContext,
			job,
			rawReceipt,
			observation.DeleteDocumentIDs,
		)
		if readErr != nil {
			return readErr
		}
		children := make([]ingest.ReceiptPurgeLinkedChildState, 0, len(reads))
		errorClass := ingest.ReceiptPurgeErrorClass("")
		for _, read := range reads {
			if read.ReadTime.After(readTime) {
				readTime = read.ReadTime.UTC()
			}
			if read.ErrorClass != "" {
				errorClass = read.ErrorClass
				break
			}
			if !read.LinkPresent || !read.ChildPresent {
				errorClass = ingest.ReceiptPurgeErrorLinkageDrift
				break
			}
			if ingest.ValidateReceiptPurgeLinkedChildState(read.State) != nil {
				errorClass = ingest.ReceiptPurgeErrorChildMalformed
				break
			}
			children = append(children, read.State)
		}
		effectiveAt, clockErr := receiptPurgeMutationEffectiveAt(s.now().UTC(), readTime)
		if clockErr != nil {
			return ingest.ErrReceiptPurgeMutationUnavailable
		}
		var plan ingest.ReceiptPurgeLinkedMutationPlan
		var planErr error
		if errorClass != "" {
			plan, planErr = ingest.PlanReceiptPurgeLinkedHold(job, receipt, errorClass, effectiveAt)
		} else {
			plan, planErr = ingest.PlanReceiptPurgeLinkedPage(
				job,
				receipt,
				observation,
				children,
				effectiveAt,
			)
		}
		if planErr != nil {
			return planErr
		}
		mutationStatus := ingest.ReceiptPurgeMutationProgressed
		if plan.Kind == ingest.ReceiptPurgeLinkedMutationHold {
			mutationStatus = ingest.ReceiptPurgeMutationHeld
		}
		result = receiptPurgeLinkedMutationResult(plan, mutationStatus)
		for _, child := range plan.Children {
			if deleteErr := transaction.Delete(
				runContext,
				cleanupTargetDocumentPath(child.Link.TenantID, child.Link.DocumentID),
			); deleteErr != nil {
				return normalizeAdmissionError(runContext, deleteErr)
			}
			if deleteErr := transaction.Delete(
				runContext,
				receiptPurgeLinkDocumentPath(
					child.Link.TenantID,
					child.Link.ReceiptID,
					child.Link.LinkID,
				),
			); deleteErr != nil {
				return normalizeAdmissionError(runContext, deleteErr)
			}
		}
		return writeReceiptPurgeJobMutation(runContext, transaction, plan.PreJob, plan.NextJob)
	})
	if err != nil {
		result.Status = ""
		return result, normalizeReceiptPurgeLinkedStoreError(ctx, err)
	}
	if ingest.ValidateReceiptPurgeLinkedMutationOutcomeQuery(result.OutcomeQuery) != nil ||
		(result.Status != ingest.ReceiptPurgeMutationProgressed &&
			result.Status != ingest.ReceiptPurgeMutationHeld) {
		return ingest.ReceiptPurgeLinkedMutationResult{}, ingest.ErrReceiptPurgeMutationUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) GetReceiptPurgeLinkedMutationOutcome(
	ctx context.Context,
	query ingest.ReceiptPurgeLinkedMutationOutcomeQuery,
) (ingest.ReceiptPurgeLinkedMutationOutcome, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil ||
		ingest.ValidateReceiptPurgeLinkedMutationOutcomeQuery(query) != nil {
		return ingest.ReceiptPurgeLinkedMutationOutcome{}, ingest.ErrReceiptPurgeMutationOutcomeUnavailable
	}
	var snapshot ingest.ReceiptPurgeLinkedMutationOutcomeSnapshot
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		transaction, job, receipt, rawReceipt, readTime, loadErr := loadReceiptPurgeLinkedMutationState(
			runContext,
			base,
			query.PreJob.PurgeKey,
			query.PreJob.TenantID,
			query.PreJob.ReceiptID,
		)
		if loadErr != nil {
			return loadErr
		}
		observations := make([]ingest.ReceiptPurgeLinkedChildOutcomeObservation, 0, len(query.Children))
		if query.Kind == ingest.ReceiptPurgeLinkedMutationPage {
			reads, readErr := transaction.ReadReceiptPurgeLinkedOutcome(
				runContext,
				query.PreJob,
				rawReceipt,
				query.Children,
			)
			if readErr != nil {
				return readErr
			}
			for _, read := range reads {
				if read.ReadTime.After(readTime) {
					readTime = read.ReadTime.UTC()
				}
				observation := ingest.ReceiptPurgeLinkedChildOutcomeObservation{
					LinkDocumentID: read.LinkDocumentID,
					LinkPresent:    read.LinkPresent,
					ChildPresent:   read.ChildPresent,
					Invalid:        read.ErrorClass != "",
				}
				if read.ErrorClass == "" && read.LinkPresent && read.ChildPresent {
					observation.State = read.State
				}
				observations = append(observations, observation)
			}
		}
		snapshot = ingest.ReceiptPurgeLinkedMutationOutcomeSnapshot{
			JobPresent:     true,
			ReceiptPresent: true,
			Job:            job,
			Receipt:        receipt,
			Children:       observations,
			ReadTime:       readTime,
		}
		return nil
	})
	if err != nil {
		return ingest.ReceiptPurgeLinkedMutationOutcome{}, normalizeReceiptPurgeMutationOutcomeError(ctx, err)
	}
	observedAt, clockErr := conservativeAcceptanceTime(s.now().UTC(), snapshot.ReadTime)
	if clockErr != nil {
		return ingest.ReceiptPurgeLinkedMutationOutcome{}, ingest.ErrReceiptPurgeMutationOutcomeUnavailable
	}
	return ingest.EvaluateReceiptPurgeLinkedMutationOutcome(query, snapshot, observedAt)
}

func (transaction firestoreAdmissionTransaction) ReadReceiptPurgeLinkedPage(
	ctx context.Context,
	job ingest.ReceiptPurgeJob,
	receipt firestoreIngestReceipt,
	linkDocumentIDs []string,
) ([]receiptPurgeLinkedRead, error) {
	if ctx == nil || transaction.client == nil || transaction.transaction == nil ||
		ingest.ValidateReceiptPurgeJob(job) != nil ||
		len(linkDocumentIDs) > ingest.ReceiptPurgeMaxPageSize {
		return nil, ingest.ErrReceiptPurgeMutationUnavailable
	}
	reads := make([]receiptPurgeLinkedRead, len(linkDocumentIDs))
	for index, linkDocumentID := range linkDocumentIDs {
		read, readErr := transaction.readReceiptPurgeLinkedPair(
			ctx,
			job,
			receipt,
			linkDocumentID,
			"",
		)
		if readErr != nil {
			return nil, readErr
		}
		reads[index] = read
	}
	return reads, nil
}

func (transaction firestoreAdmissionTransaction) ReadReceiptPurgeLinkedOutcome(
	ctx context.Context,
	job ingest.ReceiptPurgeJob,
	receipt firestoreIngestReceipt,
	expected []ingest.ReceiptPurgeLinkedChildState,
) ([]receiptPurgeLinkedRead, error) {
	if ctx == nil || transaction.client == nil || transaction.transaction == nil ||
		ingest.ValidateReceiptPurgeJob(job) != nil || len(expected) > ingest.ReceiptPurgeMaxPageSize {
		return nil, ingest.ErrReceiptPurgeMutationOutcomeUnavailable
	}
	reads := make([]receiptPurgeLinkedRead, len(expected))
	for index, child := range expected {
		if ingest.ValidateReceiptPurgeLinkedChildState(child) != nil {
			return nil, ingest.ErrReceiptPurgeMutationOutcomeUnavailable
		}
		read, readErr := transaction.readReceiptPurgeLinkedPair(
			ctx,
			job,
			receipt,
			child.LinkDocumentID,
			child.Child.DocumentID,
		)
		if readErr != nil {
			return nil, readErr
		}
		reads[index] = read
	}
	return reads, nil
}

func (transaction firestoreAdmissionTransaction) readReceiptPurgeLinkedPair(
	ctx context.Context,
	job ingest.ReceiptPurgeJob,
	receipt firestoreIngestReceipt,
	linkDocumentID string,
	expectedChildDocumentID string,
) (receiptPurgeLinkedRead, error) {
	read := receiptPurgeLinkedRead{LinkDocumentID: linkDocumentID}
	if !validReceiptPurgeAttemptDocumentID(linkDocumentID) ||
		(expectedChildDocumentID != "" && !validReceiptPurgeAttemptDocumentID(expectedChildDocumentID)) {
		return read, ingest.ErrReceiptPurgeMutationUnavailable
	}
	linkReference := transaction.client.Doc(receiptPurgeLinkDocumentPath(
		job.TenantID,
		job.ReceiptID,
		linkDocumentID,
	))
	linkDocument, linkErr := transaction.transaction.Get(linkReference)
	if linkErr != nil && status.Code(linkErr) != codes.NotFound {
		return read, normalizeAdmissionError(ctx, linkErr)
	}
	if linkErr == nil {
		if linkDocument == nil || !linkDocument.Exists() || linkDocument.Ref == nil ||
			linkDocument.Ref.Path != linkReference.Path || linkDocument.ReadTime.IsZero() {
			return read, ingest.ErrReceiptPurgeMutationUnavailable
		}
		read.LinkPresent = true
		read.ReadTime = linkDocument.ReadTime.UTC()
		linkData := linkDocument.Data()
		if validateReceiptPurgeLinkDocumentShape(linkData) != nil {
			read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
		} else {
			var stored firestoreReceiptPurgeLink
			if linkDocument.DataTo(&stored) != nil {
				read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
			} else if stored.TenantID != job.TenantID || stored.ReceiptID != job.ReceiptID {
				read.ErrorClass = ingest.ReceiptPurgeErrorChildForeign
			} else if stored.SchemaVersion != ingest.ReceiptPurgeLinkSchemaVersion ||
				stored.Kind != ingest.ReceiptPurgeLinkCleanupTarget {
				read.ErrorClass = ingest.ReceiptPurgeErrorUnsupportedVersion
			} else {
				link, domainErr := stored.toDomain(linkDocumentID, job.TenantID, job.ReceiptID)
				if domainErr != nil {
					read.ErrorClass = ingest.ReceiptPurgeErrorLinkageDrift
				} else {
					read.State.LinkDocumentID = linkDocumentID
					read.State.Link = link
					read.State.LinkDocumentDigest = receiptPurgeLinkedDocumentDigest("link", linkData)
					if read.State.LinkDocumentDigest == "" {
						read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
					}
				}
			}
		}
	}

	childDocumentID := expectedChildDocumentID
	if childDocumentID == "" && read.ErrorClass == "" && read.LinkPresent {
		childDocumentID = read.State.Link.DocumentID
	}
	if childDocumentID == "" {
		return read, nil
	}
	childReference := transaction.client.Doc(cleanupTargetDocumentPath(job.TenantID, childDocumentID))
	childDocument, childErr := transaction.transaction.Get(childReference)
	if childErr != nil && status.Code(childErr) != codes.NotFound {
		return read, normalizeAdmissionError(ctx, childErr)
	}
	if childErr != nil {
		return read, nil
	}
	if childDocument == nil || !childDocument.Exists() || childDocument.Ref == nil ||
		childDocument.Ref.Path != childReference.Path || childDocument.Ref.ID != childDocumentID ||
		childDocument.ReadTime.IsZero() {
		return read, ingest.ErrReceiptPurgeMutationUnavailable
	}
	read.ChildPresent = true
	if childDocument.ReadTime.After(read.ReadTime) {
		read.ReadTime = childDocument.ReadTime.UTC()
	}
	childData := childDocument.Data()
	if validateCleanupTargetDocumentShape(childData) != nil {
		if read.ErrorClass == "" {
			read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
		}
		return read, nil
	}
	var storedTarget firestoreIngestCleanupTarget
	if childDocument.DataTo(&storedTarget) != nil {
		if read.ErrorClass == "" {
			read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
		}
		return read, nil
	}
	if storedTarget.TenantID != job.TenantID || storedTarget.ReceiptID != job.ReceiptID {
		if read.ErrorClass == "" {
			read.ErrorClass = ingest.ReceiptPurgeErrorChildForeign
		}
		return read, nil
	}
	target, targetErr := storedTarget.toDomain()
	if targetErr != nil {
		if read.ErrorClass == "" {
			read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
		}
		return read, nil
	}
	child := cleanupTargetPurgeChildIdentity(target.Command)
	if target.Command.CleanupID != childDocumentID ||
		validateReceiptPurgeLegacyParent(receipt, target.Command) != nil ||
		target.Command.CreatedAt.After(job.CreatedAt) {
		if read.ErrorClass == "" {
			read.ErrorClass = ingest.ReceiptPurgeErrorLinkageDrift
		}
		return read, nil
	}
	if read.LinkPresent && read.ErrorClass == "" &&
		ingest.ValidateReceiptPurgeInverseLinkPair(read.State.Link, child) != nil {
		read.ErrorClass = ingest.ReceiptPurgeErrorLinkageDrift
		return read, nil
	}
	if read.ErrorClass == "" && read.LinkPresent {
		read.State.Child = child
		read.State.ChildDocumentDigest = receiptPurgeLinkedDocumentDigest("cleanup-target", childData)
		if read.State.ChildDocumentDigest == "" ||
			ingest.ValidateReceiptPurgeLinkedChildState(read.State) != nil {
			read.ErrorClass = ingest.ReceiptPurgeErrorChildMalformed
			read.State = ingest.ReceiptPurgeLinkedChildState{}
		}
	}
	return read, nil
}

func loadReceiptPurgeLinkedMutationState(
	ctx context.Context,
	base admissionTransaction,
	purgeKey string,
	tenantID string,
	receiptID string,
) (
	receiptPurgeLinkedTransaction,
	ingest.ReceiptPurgeJob,
	ingest.ReceiptPurgeReceiptState,
	firestoreIngestReceipt,
	time.Time,
	error,
) {
	transaction, ok := base.(receiptPurgeLinkedTransaction)
	if !ok || ctx == nil || !telemetry.IsUUID(tenantID) || !telemetry.IsUUID(receiptID) {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{},
			firestoreIngestReceipt{}, time.Time{}, ingest.ErrReceiptPurgeMutationUnavailable
	}
	derivedKey, keyErr := ingest.DeriveReceiptPurgeKey(tenantID, receiptID)
	if keyErr != nil || purgeKey != derivedKey {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{},
			firestoreIngestReceipt{}, time.Time{}, ingest.ErrInvalidReceiptPurgeMutation
	}
	jobRead, jobExists, jobErr := transaction.ReadReceiptPurgeJob(
		ctx,
		receiptPurgeJobDocumentPath(purgeKey),
	)
	if jobErr != nil {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{},
			firestoreIngestReceipt{}, time.Time{}, jobErr
	}
	receiptRead, receiptExists, receiptErr := transaction.ReadReceipt(
		ctx,
		receiptDocumentPath(tenantID, receiptID),
	)
	if receiptErr != nil {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{},
			firestoreIngestReceipt{}, time.Time{}, receiptErr
	}
	if !jobExists || !receiptExists || jobRead.ReadTime.IsZero() || receiptRead.ReadTime.IsZero() {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{},
			firestoreIngestReceipt{}, time.Time{}, ingest.ErrReceiptPurgeMutationUnavailable
	}
	job := jobRead.Job.toDomain()
	if ingest.ValidateReceiptPurgeJob(job) != nil || job.PurgeKey != purgeKey ||
		job.TenantID != tenantID || job.ReceiptID != receiptID {
		return nil, ingest.ReceiptPurgeJob{}, ingest.ReceiptPurgeReceiptState{},
			firestoreIngestReceipt{}, time.Time{}, ingest.ErrReceiptPurgeMutationUnavailable
	}
	readTime := jobRead.ReadTime.UTC()
	if receiptRead.ReadTime.After(readTime) {
		readTime = receiptRead.ReadTime.UTC()
	}
	return transaction, job, receiptPurgeReceiptStateForMutation(receiptRead.Receipt),
		receiptRead.Receipt, readTime, nil
}

func receiptPurgeLinkedMutationResult(
	plan ingest.ReceiptPurgeLinkedMutationPlan,
	status ingest.ReceiptPurgeMutationStatus,
) ingest.ReceiptPurgeLinkedMutationResult {
	return ingest.ReceiptPurgeLinkedMutationResult{
		Job:          plan.NextJob,
		OutcomeQuery: plan.OutcomeQuery,
		Status:       status,
	}
}

func receiptPurgeLinkedDocumentDigest(domain string, value map[string]any) string {
	if domain == "" || value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append(
		[]byte("ingest-receipt-purge-linked-document@1\x00"+domain+"\x00"),
		encoded...,
	))
	return hex.EncodeToString(digest[:])
}

func normalizeReceiptPurgeLinkedStoreError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ingest.ErrInvalidReceiptPurgeMutation) ||
		errors.Is(err, ingest.ErrReceiptPurgeMutationConflict) ||
		errors.Is(err, ingest.ErrReceiptPurgeMutationUnavailable) {
		return err
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if status.Code(err) == codes.Aborted {
		return ingest.ErrReceiptPurgeMutationConflict
	}
	return normalizeReceiptPurgeAttemptStoreError(ctx, err)
}
