package firebaseadapter

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

type receiptPurgeLinkRead struct {
	Link     ingest.ReceiptPurgeInverseLink
	ReadTime time.Time
}

type firestoreReceiptPurgeLink struct {
	SchemaVersion string                      `firestore:"schema_version"`
	LinkID        string                      `firestore:"link_id"`
	TenantID      string                      `firestore:"tenant_id"`
	ReceiptID     string                      `firestore:"receipt_id"`
	Kind          ingest.ReceiptPurgeLinkKind `firestore:"kind"`
	DocumentID    string                      `firestore:"document_id"`
	CreatedAt     time.Time                   `firestore:"created_at"`
}

func (transaction firestoreAdmissionTransaction) ReadReceiptPurgeLink(
	ctx context.Context,
	path string,
	expectedTenantID string,
	expectedReceiptID string,
) (receiptPurgeLinkRead, bool, error) {
	expectedReference := transaction.client.Doc(path)
	document, err := transaction.transaction.Get(expectedReference)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return receiptPurgeLinkRead{}, false, nil
		}
		return receiptPurgeLinkRead{}, false, normalizeAdmissionError(ctx, err)
	}
	if document == nil || !document.Exists() || document.Ref == nil ||
		document.ReadTime.IsZero() || document.Ref.Path != expectedReference.Path ||
		path != receiptPurgeLinkDocumentPath(
			expectedTenantID,
			expectedReceiptID,
			document.Ref.ID,
		) || validateReceiptPurgeLinkDocumentShape(document.Data()) != nil {
		return receiptPurgeLinkRead{}, false, ingest.ErrInvalidReceiptPurgeLink
	}
	var stored firestoreReceiptPurgeLink
	if document.DataTo(&stored) != nil {
		return receiptPurgeLinkRead{}, false, ingest.ErrInvalidReceiptPurgeLink
	}
	link, linkErr := stored.toDomain(document.Ref.ID, expectedTenantID, expectedReceiptID)
	if linkErr != nil {
		return receiptPurgeLinkRead{}, false, ingest.ErrInvalidReceiptPurgeLink
	}
	return receiptPurgeLinkRead{Link: link, ReadTime: document.ReadTime.UTC()}, true, nil
}

func receiptPurgeLinkDocumentPath(tenantID, receiptID, linkID string) string {
	return "tenants/" + tenantID + "/ingestReceipts/" + receiptID + "/purgeLinks/" + linkID
}

func newFirestoreReceiptPurgeLink(link ingest.ReceiptPurgeInverseLink) firestoreReceiptPurgeLink {
	return firestoreReceiptPurgeLink{
		SchemaVersion: link.SchemaVersion,
		LinkID:        link.LinkID,
		TenantID:      link.TenantID,
		ReceiptID:     link.ReceiptID,
		Kind:          link.Kind,
		DocumentID:    link.DocumentID,
		CreatedAt:     link.CreatedAt.UTC(),
	}
}

func (link firestoreReceiptPurgeLink) toDomain(
	snapshotDocumentID string,
	expectedTenantID string,
	expectedReceiptID string,
) (ingest.ReceiptPurgeInverseLink, error) {
	domain := ingest.ReceiptPurgeInverseLink{
		SchemaVersion: link.SchemaVersion,
		LinkID:        link.LinkID,
		TenantID:      link.TenantID,
		ReceiptID:     link.ReceiptID,
		Kind:          link.Kind,
		DocumentID:    link.DocumentID,
		CreatedAt:     link.CreatedAt.UTC(),
	}
	if ingest.ValidateReceiptPurgeInverseLinkContext(
		domain,
		snapshotDocumentID,
		expectedTenantID,
		expectedReceiptID,
	) != nil {
		return ingest.ReceiptPurgeInverseLink{}, ingest.ErrInvalidReceiptPurgeLink
	}
	return domain, nil
}

func validateReceiptPurgeLinkDocumentShape(data map[string]any) error {
	if len(data) != 7 {
		return ingest.ErrInvalidReceiptPurgeLink
	}
	for _, field := range []string{
		"schema_version",
		"link_id",
		"tenant_id",
		"receipt_id",
		"kind",
		"document_id",
	} {
		value, exists := data[field]
		if !exists {
			return ingest.ErrInvalidReceiptPurgeLink
		}
		if _, valid := value.(string); !valid {
			return ingest.ErrInvalidReceiptPurgeLink
		}
	}
	if _, valid := data["created_at"].(time.Time); !valid {
		return ingest.ErrInvalidReceiptPurgeLink
	}
	return nil
}
