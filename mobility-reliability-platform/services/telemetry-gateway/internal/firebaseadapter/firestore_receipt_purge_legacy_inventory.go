package firebaseadapter

import (
	"context"
	"errors"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var _ ingest.ReceiptPurgeLegacyInventoryStore = (*FirestoreAdmissionStore)(nil)

type receiptPurgeLegacyTargetRead struct {
	DocumentID string
	Target     ingest.CleanupTarget
	Status     ingest.ReceiptPurgeLegacyTargetStatus
}

type receiptPurgeLegacyInventoryTransaction interface {
	admissionTransaction
	QueryReceiptPurgeLegacyTargetPage(
		context.Context,
		ingest.ReceiptPurgeLegacyInventoryRequest,
	) ([]string, error)
	QueryFirstReceiptPurgeLegacyFinding(context.Context, string) (string, error)
	ReadReceiptPurgeLegacyTargets(
		context.Context,
		string,
		[]string,
	) ([]receiptPurgeLegacyTargetRead, error)
	ReadReceiptPurgeLink(
		context.Context,
		string,
		string,
		string,
	) (receiptPurgeLinkRead, bool, error)
}

// ListReceiptPurgeLegacyTargetPage returns advisory document identities only.
// BackfillReceiptPurgeLegacyTargetPage must re-read this exact page and every
// target, parent receipt, and inverse link inside its mutation transaction.
func (s *FirestoreAdmissionStore) ListReceiptPurgeLegacyTargetPage(
	ctx context.Context,
	request ingest.ReceiptPurgeLegacyInventoryRequest,
) (ingest.ReceiptPurgeLegacyInventoryPage, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		ingest.ValidateReceiptPurgeLegacyInventoryRequest(request) != nil {
		return ingest.ReceiptPurgeLegacyInventoryPage{}, ingest.ErrInvalidReceiptPurgeLegacyInventory
	}
	var result ingest.ReceiptPurgeLegacyInventoryPage
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		transaction, ok := base.(receiptPurgeLegacyInventoryTransaction)
		if !ok {
			return ingest.ErrReceiptPurgeLegacyInventoryUnavailable
		}
		documentIDs, queryErr := transaction.QueryReceiptPurgeLegacyTargetPage(runContext, request)
		if queryErr != nil {
			return queryErr
		}
		page, buildErr := ingest.BuildReceiptPurgeLegacyInventoryPage(request, documentIDs)
		if buildErr != nil {
			return buildErr
		}
		result = page
		return nil
	})
	if err != nil {
		return ingest.ReceiptPurgeLegacyInventoryPage{}, normalizeReceiptPurgeLegacyInventoryError(ctx, err)
	}
	if ingest.ValidateReceiptPurgeLegacyInventoryPage(result) != nil {
		return ingest.ReceiptPurgeLegacyInventoryPage{}, ingest.ErrReceiptPurgeLegacyInventoryUnavailable
	}
	return result, nil
}

// BackfillReceiptPurgeLegacyTargetPage creates only missing inverse links. A
// single malformed, foreign, drifted, or post-fence unregistered target holds
// the whole page with zero writes and no cursor advance.
func (s *FirestoreAdmissionStore) BackfillReceiptPurgeLegacyTargetPage(
	ctx context.Context,
	advisory ingest.ReceiptPurgeLegacyInventoryPage,
) (ingest.ReceiptPurgeLegacyBackfillPlan, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		ingest.ValidateReceiptPurgeLegacyInventoryPage(advisory) != nil {
		return ingest.ReceiptPurgeLegacyBackfillPlan{}, ingest.ErrInvalidReceiptPurgeLegacyInventory
	}
	var result ingest.ReceiptPurgeLegacyBackfillPlan
	err := s.runTransaction(ctx, func(runContext context.Context, base admissionTransaction) error {
		result = ingest.ReceiptPurgeLegacyBackfillPlan{}
		transaction, ok := base.(receiptPurgeLegacyInventoryTransaction)
		if !ok {
			return ingest.ErrReceiptPurgeLegacyInventoryUnavailable
		}
		currentIDs, queryErr := transaction.QueryReceiptPurgeLegacyTargetPage(
			runContext,
			advisory.Request,
		)
		if queryErr != nil {
			return queryErr
		}
		current, buildErr := ingest.BuildReceiptPurgeLegacyInventoryPage(
			advisory.Request,
			currentIDs,
		)
		if buildErr != nil {
			return buildErr
		}
		if !ingest.SameReceiptPurgeLegacyInventoryPage(advisory, current) {
			return ingest.ErrReceiptPurgeLegacyInventoryConflict
		}

		findingID, findingErr := transaction.QueryFirstReceiptPurgeLegacyFinding(
			runContext,
			advisory.Request.TenantID,
		)
		if findingErr != nil {
			return findingErr
		}
		findingStatus, classifyErr := ingest.ClassifyReceiptPurgeLegacyFindingProbe(
			advisory.Request.TenantID,
			findingID,
		)
		if classifyErr != nil {
			return classifyErr
		}
		if findingStatus == ingest.ReceiptPurgeLegacyFindingUnsupported {
			return ingest.ErrReceiptPurgeLegacyFindingUnsupported
		}

		reads, readErr := transaction.ReadReceiptPurgeLegacyTargets(
			runContext,
			advisory.Request.TenantID,
			advisory.DocumentIDs,
		)
		if readErr != nil {
			return readErr
		}
		observations, observeErr := observeReceiptPurgeLegacyTargets(
			runContext,
			transaction,
			advisory.Request.TenantID,
			reads,
		)
		if observeErr != nil {
			return observeErr
		}
		plan, planErr := ingest.PlanReceiptPurgeLegacyBackfill(current, observations)
		if planErr != nil {
			return planErr
		}
		result = plan
		for _, link := range plan.LinksToCreate {
			if createErr := transaction.Create(
				runContext,
				receiptPurgeLinkDocumentPath(link.TenantID, link.ReceiptID, link.LinkID),
				newFirestoreReceiptPurgeLink(link),
			); createErr != nil {
				return normalizeAdmissionError(runContext, createErr)
			}
		}
		return nil
	})
	if err != nil {
		return ingest.ReceiptPurgeLegacyBackfillPlan{}, normalizeReceiptPurgeLegacyInventoryError(ctx, err)
	}
	if ingest.ValidateReceiptPurgeLegacyInventoryPage(result.Page) != nil {
		return ingest.ReceiptPurgeLegacyBackfillPlan{}, ingest.ErrReceiptPurgeLegacyInventoryUnavailable
	}
	return result, nil
}

func (transaction firestoreAdmissionTransaction) QueryReceiptPurgeLegacyTargetPage(
	ctx context.Context,
	request ingest.ReceiptPurgeLegacyInventoryRequest,
) ([]string, error) {
	if ctx == nil || transaction.client == nil || transaction.transaction == nil ||
		ingest.ValidateReceiptPurgeLegacyInventoryRequest(request) != nil {
		return nil, ingest.ErrInvalidReceiptPurgeLegacyInventory
	}
	query := transaction.client.Collection(receiptPurgeLegacyTargetCollectionPath(request.TenantID)).
		OrderBy(firestore.DocumentID, firestore.Asc).
		Limit(request.PageSize + 1)
	if request.Cursor != "" {
		query = query.StartAfter(request.Cursor)
	}
	documents, err := transaction.transaction.Documents(query).GetAll()
	if err != nil {
		return nil, normalizeAdmissionError(ctx, err)
	}
	documentIDs := make([]string, len(documents))
	for index, document := range documents {
		if document == nil || document.Ref == nil || document.ReadTime.IsZero() {
			return nil, ingest.ErrReceiptPurgeLegacyInventoryUnavailable
		}
		documentIDs[index] = document.Ref.ID
	}
	return documentIDs, nil
}

func (transaction firestoreAdmissionTransaction) QueryFirstReceiptPurgeLegacyFinding(
	ctx context.Context,
	tenantID string,
) (string, error) {
	if ctx == nil || transaction.client == nil || transaction.transaction == nil ||
		!telemetry.IsUUID(tenantID) {
		return "", ingest.ErrInvalidReceiptPurgeLegacyInventory
	}
	query := transaction.client.Collection(receiptPurgeLegacyFindingCollectionPath(tenantID)).
		OrderBy(firestore.DocumentID, firestore.Asc).
		Limit(1)
	documents, err := transaction.transaction.Documents(query).GetAll()
	if err != nil {
		return "", normalizeAdmissionError(ctx, err)
	}
	if len(documents) == 0 {
		return "", nil
	}
	if len(documents) != 1 || documents[0] == nil || documents[0].Ref == nil ||
		documents[0].ReadTime.IsZero() {
		return "", ingest.ErrReceiptPurgeLegacyInventoryUnavailable
	}
	return documents[0].Ref.ID, nil
}

func (transaction firestoreAdmissionTransaction) ReadReceiptPurgeLegacyTargets(
	ctx context.Context,
	tenantID string,
	documentIDs []string,
) ([]receiptPurgeLegacyTargetRead, error) {
	if ctx == nil || transaction.client == nil || transaction.transaction == nil ||
		!telemetry.IsUUID(tenantID) ||
		len(documentIDs) > ingest.MaxReceiptPurgeLegacyInventoryPageSize {
		return nil, ingest.ErrInvalidReceiptPurgeLegacyInventory
	}
	references := make([]*firestore.DocumentRef, len(documentIDs))
	for index, documentID := range documentIDs {
		if !validReceiptPurgeAttemptDocumentID(documentID) {
			return nil, ingest.ErrInvalidReceiptPurgeLegacyInventory
		}
		references[index] = transaction.client.Doc(cleanupTargetDocumentPath(tenantID, documentID))
	}
	if len(references) == 0 {
		return []receiptPurgeLegacyTargetRead{}, nil
	}
	documents, err := transaction.transaction.GetAll(references)
	if err != nil {
		return nil, normalizeAdmissionError(ctx, err)
	}
	if len(documents) != len(references) {
		return nil, ingest.ErrReceiptPurgeLegacyInventoryUnavailable
	}
	reads := make([]receiptPurgeLegacyTargetRead, len(documents))
	for index, document := range documents {
		read := receiptPurgeLegacyTargetRead{DocumentID: documentIDs[index]}
		if document == nil || document.Ref == nil || document.Ref.ID != documentIDs[index] ||
			document.Ref.Path != references[index].Path || document.ReadTime.IsZero() {
			return nil, ingest.ErrReceiptPurgeLegacyInventoryUnavailable
		}
		if !document.Exists() {
			read.Status = ingest.ReceiptPurgeLegacyTargetLinkageDrift
			reads[index] = read
			continue
		}
		if !telemetry.IsUUID(document.Ref.ID) {
			read.Status = ingest.ReceiptPurgeLegacyTargetMalformedChild
			reads[index] = read
			continue
		}
		var stored firestoreIngestCleanupTarget
		if validateCleanupTargetDocumentShape(document.Data()) != nil ||
			document.DataTo(&stored) != nil {
			read.Status = ingest.ReceiptPurgeLegacyTargetMalformedChild
			reads[index] = read
			continue
		}
		target, domainErr := stored.toDomain()
		if domainErr != nil {
			read.Status = ingest.ReceiptPurgeLegacyTargetMalformedChild
			reads[index] = read
			continue
		}
		switch {
		case target.Command.TenantID != tenantID:
			read.Status = ingest.ReceiptPurgeLegacyTargetForeignChild
		case target.Command.CleanupID != document.Ref.ID:
			read.Status = ingest.ReceiptPurgeLegacyTargetLinkageDrift
		default:
			read.Target = target
		}
		reads[index] = read
	}
	return reads, nil
}

func observeReceiptPurgeLegacyTargets(
	ctx context.Context,
	transaction receiptPurgeLegacyInventoryTransaction,
	tenantID string,
	reads []receiptPurgeLegacyTargetRead,
) ([]ingest.ReceiptPurgeLegacyTargetObservation, error) {
	observations := make([]ingest.ReceiptPurgeLegacyTargetObservation, len(reads))
	for index, read := range reads {
		observation := ingest.ReceiptPurgeLegacyTargetObservation{
			DocumentID: read.DocumentID,
			Status:     read.Status,
		}
		if read.Status != "" {
			observations[index] = observation
			continue
		}
		command := read.Target.Command
		parent, exists, receiptErr := transaction.ReadReceipt(
			ctx,
			receiptDocumentPath(tenantID, command.ReceiptID),
		)
		if receiptErr != nil {
			return nil, receiptErr
		}
		if !exists || parent.ReadTime.IsZero() ||
			validateReceiptPurgeLegacyParent(parent.Receipt, command) != nil {
			observation.Status = ingest.ReceiptPurgeLegacyTargetLinkageDrift
			observations[index] = observation
			continue
		}
		child := cleanupTargetPurgeChildIdentity(command)
		link, linkErr := ingest.BuildReceiptPurgeInverseLink(child)
		if linkErr != nil {
			observation.Status = ingest.ReceiptPurgeLegacyTargetLinkageDrift
			observations[index] = observation
			continue
		}
		persisted, linkExists, readLinkErr := transaction.ReadReceiptPurgeLink(
			ctx,
			receiptPurgeLinkDocumentPath(tenantID, command.ReceiptID, link.LinkID),
			tenantID,
			command.ReceiptID,
		)
		if readLinkErr != nil {
			if errors.Is(readLinkErr, ingest.ErrInvalidReceiptPurgeLink) {
				observation.Status = ingest.ReceiptPurgeLegacyTargetLinkageDrift
				observations[index] = observation
				continue
			}
			return nil, readLinkErr
		}
		observation.ExpectedLink = link
		if linkExists {
			if ingest.ValidateReceiptPurgeInverseLinkPair(persisted.Link, child) != nil {
				observation.ExpectedLink = ingest.ReceiptPurgeInverseLink{}
				observation.Status = ingest.ReceiptPurgeLegacyTargetLinkageDrift
			} else {
				observation.Status = ingest.ReceiptPurgeLegacyTargetRegistered
			}
		} else if receiptHasPurgeFenceFields(parent.Receipt) {
			observation.ExpectedLink = ingest.ReceiptPurgeInverseLink{}
			observation.Status = ingest.ReceiptPurgeLegacyTargetFencedUnregistered
		} else {
			observation.Status = ingest.ReceiptPurgeLegacyTargetUnregistered
		}
		observations[index] = observation
	}
	return observations, nil
}

func validateReceiptPurgeLegacyParent(
	receipt firestoreIngestReceipt,
	command ingest.CleanupTargetCommand,
) error {
	indexProjection := firestoreIngestIndex{
		TenantID: receipt.TenantID, ReservationKey: receipt.ReservationKey,
		ClientBatchKey: receipt.ClientBatchKey, ReceiptID: receipt.ReceiptID,
		BatchID: receipt.BatchID, InstallationID: receipt.InstallationID,
		ClientBatchID: receipt.ClientBatchID, PayloadSchemaVersion: receipt.PayloadSchemaVersion,
		BodyHash: receipt.BodyHash, CreatedAt: receipt.CreatedAt.UTC(),
		ReceiptRetentionFloor: receipt.ReceiptRetentionFloor.UTC(),
		PurgeEligibleAt:       cloneOptionalTime(receipt.PurgeEligibleAt),
	}
	if validateIndexDocument(indexProjection, command.TenantID) != nil ||
		validateReceiptLinkage(receipt, indexProjection) != nil ||
		validateReceiptState(receipt) != nil || receipt.TenantID != command.TenantID ||
		receipt.ReceiptID != command.ReceiptID || receipt.ReservationKey != command.ReservationKey ||
		receipt.CleanupMode != command.Mode || receipt.CleanupOriginStatus != command.OriginStatus ||
		receipt.CleanupPolicyVersion != command.CleanupPolicyVersion ||
		!receipt.CleanupTransitionedAt.Equal(command.CleanupTransitionedAt) ||
		!receipt.CleanupQuiescenceUntil.Equal(command.CleanupQuiescenceUntil) {
		return ingest.ErrReceiptPurgeLegacyInventoryConflict
	}
	if receiptHasPurgeFenceFields(receipt) {
		purgeKey, err := ingest.DeriveReceiptPurgeKey(receipt.TenantID, receipt.ReceiptID)
		if err != nil || receipt.PurgeJobID != purgeKey {
			return ingest.ErrReceiptPurgeLegacyInventoryConflict
		}
	}
	return nil
}

func receiptPurgeLegacyTargetCollectionPath(tenantID string) string {
	return "tenants/" + tenantID + "/ingestCleanupTargets"
}

func receiptPurgeLegacyFindingCollectionPath(tenantID string) string {
	return "tenants/" + tenantID + "/ingestIntegrityFindings"
}

func normalizeReceiptPurgeLegacyInventoryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ingest.ErrInvalidReceiptPurgeLegacyInventory) ||
		errors.Is(err, ingest.ErrReceiptPurgeLegacyInventoryConflict) ||
		errors.Is(err, ingest.ErrReceiptPurgeLegacyInventoryUnavailable) ||
		errors.Is(err, ingest.ErrReceiptPurgeLegacyFindingUnsupported) {
		return err
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if status.Code(err) == codes.Aborted {
		return ingest.ErrReceiptPurgeLegacyInventoryConflict
	}
	normalized := normalizeAdmissionError(ctx, err)
	if errors.Is(normalized, ingest.ErrAdmissionUnavailable) {
		return ingest.ErrReceiptPurgeLegacyInventoryUnavailable
	}
	return normalized
}
