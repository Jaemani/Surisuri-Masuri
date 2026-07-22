package firebaseadapter

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const candidateEmulatorTenantID = "77777777-7777-4777-8777-777777777777"

func TestFirestoreAdmissionStoreEmulatorForwardRecoveryCandidatePagination(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	cutoff := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	dueAt := cutoff.Add(-2 * time.Minute)
	receiptIDs := []string{
		"88888888-8888-4888-8888-888888888881",
		"88888888-8888-4888-8888-888888888882",
		"88888888-8888-4888-8888-888888888883",
	}
	for index, receiptID := range receiptIDs {
		nextRecoveryAt := dueAt
		if index == 2 {
			nextRecoveryAt = dueAt.Add(time.Minute)
		}
		seedForwardRecoveryCandidate(
			t,
			client,
			candidateEmulatorTenantID,
			receiptID,
			string(rune('a'+index)),
			ingest.ReceiptReserved,
			nextRecoveryAt,
		)
	}
	seedForwardRecoveryCandidate(
		t,
		client,
		candidateEmulatorTenantID,
		"88888888-8888-4888-8888-888888888884",
		"d",
		ingest.ReceiptReserved,
		cutoff.Add(time.Minute),
	)
	seedForwardRecoveryCandidate(
		t,
		client,
		candidateEmulatorTenantID,
		"88888888-8888-4888-8888-888888888885",
		"e",
		ingest.ReceiptStored,
		dueAt,
	)

	store, err := NewFirestoreForwardRecoveryCandidateStore(client, 5*time.Second)
	if err != nil {
		t.Fatalf("NewFirestoreForwardRecoveryCandidateStore() error = %v", err)
	}
	first, err := store.ListDueForwardRecoveryCandidates(
		context.Background(),
		candidateEmulatorTenantID,
		cutoff,
		nil,
		2,
	)
	if err != nil {
		t.Fatalf("first ListDueForwardRecoveryCandidates() error = %v", err)
	}
	if first.Exhausted || first.NextCursor == nil || len(first.Candidates) != 2 ||
		first.Candidates[0].ReceiptID != receiptIDs[0] ||
		first.Candidates[1].ReceiptID != receiptIDs[1] ||
		first.NextCursor.DocumentID != receiptIDs[1] {
		t.Fatalf("first page = %#v", first)
	}
	second, err := store.ListDueForwardRecoveryCandidates(
		context.Background(),
		candidateEmulatorTenantID,
		cutoff,
		first.NextCursor,
		2,
	)
	if err != nil {
		t.Fatalf("second ListDueForwardRecoveryCandidates() error = %v", err)
	}
	if !second.Exhausted || second.NextCursor != nil || len(second.Candidates) != 1 ||
		second.Candidates[0].ReceiptID != receiptIDs[2] {
		t.Fatalf("second page = %#v", second)
	}
}

func TestFirestoreAdmissionStoreEmulatorForwardRecoveryCandidatePreservesMalformedDocumentCursor(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	tenantID := "99999999-9999-4999-8999-999999999999"
	cutoff := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	_, err := client.Doc(receiptDocumentPath(
		tenantID,
		"88888888-8888-4888-8888-888888888889",
	)).Set(context.Background(), map[string]any{
		"tenant_id":        tenantID,
		"reservation_key":  repeatCandidateKey("f"),
		"receipt_id":       "88888888-8888-4888-8888-888888888880",
		"status":           ingest.ReceiptReserved,
		"next_recovery_at": cutoff.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("seed malformed candidate: %v", err)
	}
	store, err := NewFirestoreForwardRecoveryCandidateStore(client, 5*time.Second)
	if err != nil {
		t.Fatalf("NewFirestoreForwardRecoveryCandidateStore() error = %v", err)
	}
	page, err := store.ListDueForwardRecoveryCandidates(
		context.Background(),
		tenantID,
		cutoff,
		nil,
		10,
	)
	if err != nil || !page.Exhausted || len(page.Candidates) != 1 ||
		ingest.ValidateForwardRecoveryCandidate(page.Candidates[0]) != ingest.ErrInvalidForwardRecoveryCandidate ||
		page.Candidates[0].DocumentID != "88888888-8888-4888-8888-888888888889" {
		t.Fatalf("ListDueForwardRecoveryCandidates() = %#v, %v", page, err)
	}
}

func seedForwardRecoveryCandidate(
	t *testing.T,
	client *firestore.Client,
	tenantID string,
	receiptID string,
	keyCharacter string,
	state ingest.ReceiptState,
	nextRecoveryAt time.Time,
) {
	t.Helper()
	_, err := client.Doc(receiptDocumentPath(tenantID, receiptID)).Set(
		context.Background(),
		map[string]any{
			"tenant_id":        tenantID,
			"reservation_key":  repeatCandidateKey(keyCharacter),
			"receipt_id":       receiptID,
			"status":           state,
			"next_recovery_at": nextRecoveryAt.UTC(),
		},
	)
	if err != nil {
		t.Fatalf("seed forward recovery candidate: %v", err)
	}
}

func repeatCandidateKey(character string) string {
	value := ""
	for len(value) < 64 {
		value += character
	}
	return value
}
