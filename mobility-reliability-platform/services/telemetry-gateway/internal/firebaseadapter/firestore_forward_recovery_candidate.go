package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const maxForwardRecoveryCandidateQueryTimeout = 30 * time.Second

type FirestoreForwardRecoveryCandidateStore struct {
	client  *firestore.Client
	timeout time.Duration
}

var _ ingest.ForwardRecoveryCandidateStore = (*FirestoreForwardRecoveryCandidateStore)(nil)

func NewFirestoreForwardRecoveryCandidateStore(
	client *firestore.Client,
	timeout time.Duration,
) (*FirestoreForwardRecoveryCandidateStore, error) {
	if client == nil || timeout <= 0 || timeout > maxForwardRecoveryCandidateQueryTimeout {
		return nil, errors.New("Firestore forward recovery candidate store requires a client and bounded timeout")
	}
	return &FirestoreForwardRecoveryCandidateStore{client: client, timeout: timeout}, nil
}

func (store *FirestoreForwardRecoveryCandidateStore) ListDueForwardRecoveryCandidates(
	ctx context.Context,
	tenantID string,
	cutoff time.Time,
	after *ingest.ForwardRecoveryCursor,
	limit int,
) (ingest.ForwardRecoveryCandidatePage, error) {
	if store == nil || store.client == nil || store.timeout <= 0 || ctx == nil ||
		!telemetry.IsUUID(tenantID) || cutoff.IsZero() || limit <= 0 ||
		limit > ingest.MaxForwardRecoveryCandidatePageSize ||
		(after != nil && ingest.ValidateForwardRecoveryCursor(*after) != nil) {
		return ingest.ForwardRecoveryCandidatePage{}, ingest.ErrForwardRecoveryCandidateUnavailable
	}

	queryContext, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	query := store.client.Collection("tenants/"+tenantID+"/ingestReceipts").
		Where("status", "==", ingest.ReceiptReserved).
		Where("next_recovery_at", "<=", cutoff.UTC()).
		OrderBy("next_recovery_at", firestore.Asc).
		OrderBy(firestore.DocumentID, firestore.Asc).
		Limit(limit + 1)
	if after != nil {
		query = query.StartAfter(after.NextRecoveryAt.UTC(), after.DocumentID)
	}
	documents, err := query.Documents(queryContext).GetAll()
	if err != nil {
		return ingest.ForwardRecoveryCandidatePage{}, normalizeForwardRecoveryCandidateError(queryContext, err)
	}
	if len(documents) > limit+1 {
		return ingest.ForwardRecoveryCandidatePage{}, ingest.ErrForwardRecoveryCandidateUnavailable
	}

	hasMore := len(documents) > limit
	if hasMore {
		documents = documents[:limit]
	}
	candidates := make([]ingest.ForwardRecoveryCandidate, 0, len(documents))
	for _, document := range documents {
		candidate, decodeErr := decodeForwardRecoveryCandidate(document, tenantID, cutoff)
		if decodeErr != nil {
			return ingest.ForwardRecoveryCandidatePage{}, ingest.ErrForwardRecoveryCandidateUnavailable
		}
		candidates = append(candidates, candidate)
	}
	page := ingest.ForwardRecoveryCandidatePage{
		Candidates: candidates,
		Exhausted:  !hasMore,
	}
	if hasMore {
		last := candidates[len(candidates)-1]
		page.NextCursor = &ingest.ForwardRecoveryCursor{
			NextRecoveryAt: last.NextRecoveryAt,
			DocumentID:     last.DocumentID,
		}
	}
	return page, nil
}

func decodeForwardRecoveryCandidate(
	document *firestore.DocumentSnapshot,
	tenantID string,
	cutoff time.Time,
) (ingest.ForwardRecoveryCandidate, error) {
	if document == nil || document.Ref == nil || !document.Exists() {
		return ingest.ForwardRecoveryCandidate{}, ingest.ErrForwardRecoveryCandidateUnavailable
	}
	data := document.Data()
	nextRecoveryAt, hasNextRecoveryAt := data["next_recovery_at"].(time.Time)
	if !hasNextRecoveryAt || nextRecoveryAt.IsZero() || nextRecoveryAt.After(cutoff.UTC()) ||
		!validForwardRecoveryCandidateDocumentID(document.Ref.ID) {
		return ingest.ForwardRecoveryCandidate{}, ingest.ErrForwardRecoveryCandidateUnavailable
	}
	tenantIDValue, _ := data["tenant_id"].(string)
	reservationKey, _ := data["reservation_key"].(string)
	receiptID, _ := data["receipt_id"].(string)
	stateValue, _ := data["status"].(string)
	candidate := ingest.ForwardRecoveryCandidate{
		TenantID:       tenantIDValue,
		ReservationKey: reservationKey,
		DocumentID:     document.Ref.ID,
		ReceiptID:      receiptID,
		State:          ingest.ReceiptState(stateValue),
		NextRecoveryAt: nextRecoveryAt.UTC(),
	}
	if candidate.TenantID != tenantID {
		// Keep the document cursor so the worker can isolate this malformed
		// advisory item instead of retrying the same poisoned page forever.
		candidate.TenantID = ""
	}
	return candidate, nil
}

func validForwardRecoveryCandidateDocumentID(value string) bool {
	if value == "" || len(value) > 1500 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character == '/' || character == 0 {
			return false
		}
	}
	return true
}

func normalizeForwardRecoveryCandidateError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ingest.ErrForwardRecoveryCandidateUnavailable
}
