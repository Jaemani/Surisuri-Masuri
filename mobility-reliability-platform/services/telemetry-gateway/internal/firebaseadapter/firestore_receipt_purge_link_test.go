package firebaseadapter

import (
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreReceiptPurgeLinkRoundTripsExactContext(t *testing.T) {
	child := ingest.ReceiptPurgeLinkedChildIdentity{
		TenantID:   "019c5af2-7a6b-7d1a-850c-02dc21343a23",
		ReceiptID:  "019c5af2-7a6b-7d1a-850c-02dc21343a24",
		Kind:       ingest.ReceiptPurgeLinkCleanupTarget,
		DocumentID: "019c5af2-7a6b-7d1a-850c-02dc21343a25",
		CreatedAt:  time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC),
	}
	link, err := ingest.BuildReceiptPurgeInverseLink(child)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	stored := newFirestoreReceiptPurgeLink(link)
	roundTripped, err := stored.toDomain(link.LinkID, child.TenantID, child.ReceiptID)
	if err != nil || roundTripped != link {
		t.Fatalf("toDomain() = %#v, %v; want %#v", roundTripped, err, link)
	}
	wantPath := "tenants/" + child.TenantID + "/ingestReceipts/" + child.ReceiptID +
		"/purgeLinks/" + link.LinkID
	if got := receiptPurgeLinkDocumentPath(child.TenantID, child.ReceiptID, link.LinkID); got != wantPath {
		t.Fatalf("receiptPurgeLinkDocumentPath() = %q, want %q", got, wantPath)
	}
}

func TestFirestoreReceiptPurgeLinkRejectsContextDrift(t *testing.T) {
	child := ingest.ReceiptPurgeLinkedChildIdentity{
		TenantID:   "019c5af2-7a6b-7d1a-850c-02dc21343a23",
		ReceiptID:  "019c5af2-7a6b-7d1a-850c-02dc21343a24",
		Kind:       ingest.ReceiptPurgeLinkCleanupTarget,
		DocumentID: "019c5af2-7a6b-7d1a-850c-02dc21343a25",
		CreatedAt:  time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC),
	}
	link, err := ingest.BuildReceiptPurgeInverseLink(child)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	stored := newFirestoreReceiptPurgeLink(link)
	for _, test := range []struct {
		name      string
		document  string
		tenantID  string
		receiptID string
	}{
		{name: "snapshot", document: "wrong", tenantID: child.TenantID, receiptID: child.ReceiptID},
		{name: "tenant", document: link.LinkID, tenantID: child.ReceiptID, receiptID: child.ReceiptID},
		{name: "receipt", document: link.LinkID, tenantID: child.TenantID, receiptID: child.TenantID},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := stored.toDomain(test.document, test.tenantID, test.receiptID); !errors.Is(err, ingest.ErrInvalidReceiptPurgeLink) {
				t.Fatalf("toDomain() = %v", err)
			}
		})
	}
}

func TestValidateReceiptPurgeLinkDocumentShapeIsStrict(t *testing.T) {
	valid := map[string]any{
		"schema_version": ingest.ReceiptPurgeLinkSchemaVersion,
		"link_id":        "link",
		"tenant_id":      "tenant",
		"receipt_id":     "receipt",
		"kind":           string(ingest.ReceiptPurgeLinkCleanupTarget),
		"document_id":    "document",
		"created_at":     time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC),
	}
	if err := validateReceiptPurgeLinkDocumentShape(valid); err != nil {
		t.Fatalf("valid shape rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown", mutate: func(data map[string]any) { data["future"] = "field" }},
		{name: "missing", mutate: func(data map[string]any) { delete(data, "link_id") }},
		{name: "wrong string type", mutate: func(data map[string]any) { data["kind"] = int64(1) }},
		{name: "wrong time type", mutate: func(data map[string]any) { data["created_at"] = "now" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := make(map[string]any, len(valid))
			for key, value := range valid {
				candidate[key] = value
			}
			test.mutate(candidate)
			if err := validateReceiptPurgeLinkDocumentShape(candidate); !errors.Is(err, ingest.ErrInvalidReceiptPurgeLink) {
				t.Fatalf("shape validation = %v", err)
			}
		})
	}
}
