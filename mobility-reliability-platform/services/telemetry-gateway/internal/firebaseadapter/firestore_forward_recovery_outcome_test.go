package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreLoadsForwardRecoveryOutcomeReadOnly(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
	query, err := ingest.ForwardRecoveryOutcomeQueryForAction(fixture.command)
	if err != nil {
		t.Fatalf("ForwardRecoveryOutcomeQueryForAction() = %v", err)
	}

	snapshot, err := store.LoadCurrentForwardRecoveryOutcome(context.Background(), query)
	if err != nil {
		t.Fatalf("LoadCurrentForwardRecoveryOutcome() = %v", err)
	}
	if snapshot.Receipt.ReceiptID != admissionReceiptID ||
		snapshot.Attempt.AttemptID != fixture.command.Attempt.ID ||
		snapshot.Attempt.Status != ingest.RecoveryAttemptStarted ||
		!snapshot.ReadTime.Equal(fixture.base.readTime) {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(fixture.base.creates) != 0 || len(fixture.base.updates) != 0 {
		t.Fatalf("creates/updates = %d/%d, want 0/0", len(fixture.base.creates), len(fixture.base.updates))
	}
}

func TestFirestoreAdmissionStoreRejectsIncoherentForwardRecoveryOutcomeReadTime(t *testing.T) {
	tests := []struct {
		name          string
		receiptOffset time.Duration
		attemptOffset time.Duration
	}{
		{name: "attempt ahead", attemptOffset: maxAdmissionClockSkew + time.Nanosecond},
		{name: "receipt ahead", receiptOffset: maxAdmissionClockSkew + time.Nanosecond},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
			baseReadTime := fixture.base.readTime
			fixture.base.receiptReadTime = baseReadTime.Add(test.receiptOffset)
			fixture.transaction.attemptReadTime = baseReadTime.Add(test.attemptOffset)
			store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
			query, err := ingest.ForwardRecoveryOutcomeQueryForAction(fixture.command)
			if err != nil {
				t.Fatalf("ForwardRecoveryOutcomeQueryForAction() = %v", err)
			}

			_, err = store.LoadCurrentForwardRecoveryOutcome(context.Background(), query)
			if !errors.Is(err, ingest.ErrForwardRecoveryOutcomeUnavailable) {
				t.Fatalf("LoadCurrentForwardRecoveryOutcome() = %v", err)
			}
			if len(fixture.base.creates) != 0 || len(fixture.base.updates) != 0 {
				t.Fatalf("creates/updates = %d/%d, want 0/0", len(fixture.base.creates), len(fixture.base.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreRejectsOutOfRangeOutcomeCRC32C(t *testing.T) {
	tests := []struct {
		name          string
		mutateReceipt func(*firestoreIngestReceipt)
		mutateAttempt func(*firestoreRecoveryAttempt)
	}{
		{
			name: "negative receipt crc",
			mutateReceipt: func(receipt *firestoreIngestReceipt) {
				receipt.ObjectCRC32C = -1
			},
		},
		{
			name: "overflow attempt crc",
			mutateAttempt: func(attempt *firestoreRecoveryAttempt) {
				attempt.RawCRC32C = int64(^uint32(0)) + 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
			receipt := fixture.base.receipts[admissionReceiptPath()]
			attempt := fixture.transaction.attempts[fixture.attemptPath]
			if test.mutateReceipt != nil {
				test.mutateReceipt(&receipt)
			}
			if test.mutateAttempt != nil {
				test.mutateAttempt(&attempt)
			}
			fixture.base.receipts[admissionReceiptPath()] = receipt
			fixture.transaction.attempts[fixture.attemptPath] = attempt
			store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
			query, err := ingest.ForwardRecoveryOutcomeQueryForAction(fixture.command)
			if err != nil {
				t.Fatalf("ForwardRecoveryOutcomeQueryForAction() = %v", err)
			}

			_, err = store.LoadCurrentForwardRecoveryOutcome(context.Background(), query)
			if !errors.Is(err, ingest.ErrForwardRecoveryOutcomeUnavailable) {
				t.Fatalf("LoadCurrentForwardRecoveryOutcome() = %v", err)
			}
			if len(fixture.base.creates) != 0 || len(fixture.base.updates) != 0 {
				t.Fatalf("creates/updates = %d/%d, want 0/0", len(fixture.base.creates), len(fixture.base.updates))
			}
		})
	}
}

func TestCurrentForwardRecoveryOutcomeReceiptRejectsUnboundedControlFields(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	baseReceipt := fixture.base.receipts[admissionReceiptPath()]
	tests := []struct {
		name   string
		mutate func(*firestoreIngestReceipt)
	}{
		{name: "rejection", mutate: func(receipt *firestoreIngestReceipt) {
			receipt.RejectionCode = "provider-error-text"
		}},
		{name: "hold", mutate: func(receipt *firestoreIngestReceipt) {
			receipt.RecoveryHoldCode = "custom-hold"
		}},
		{name: "release", mutate: func(receipt *firestoreIngestReceipt) {
			receipt.LastRecoveryCode = "custom-release"
		}},
		{name: "lease owner", mutate: func(receipt *firestoreIngestReceipt) {
			receipt.LeaseOwnerID = "firebase-user-id"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := baseReceipt
			test.mutate(&receipt)
			projection, err := currentForwardRecoveryOutcomeReceipt(receipt)
			if !errors.Is(err, ingest.ErrForwardRecoveryOutcomeUnavailable) ||
				projection != (ingest.CurrentForwardRecoveryOutcomeReceipt{}) {
				t.Fatalf("currentForwardRecoveryOutcomeReceipt() = %#v, %v", projection, err)
			}
		})
	}
}

func TestFirestoreAdmissionStoreRejectsInvalidOutcomeGrantBeforeTransaction(t *testing.T) {
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	query, _ := ingest.ForwardRecoveryOutcomeQueryForAction(fixture.command)
	transactionCalls := 0
	store := admissionTestStore(fixture.observedAt, func(
		_ context.Context,
		_ func(context.Context, admissionTransaction) error,
	) error {
		transactionCalls++
		return nil
	})
	_, err := store.GetForwardRecoveryActionOutcome(
		context.Background(),
		ingest.ForwardRecoveryOutcomeReadGrant{},
		query,
		fixture.observedAt,
	)
	if !errors.Is(err, ingest.ErrInvalidForwardRecoveryOutcomeAuthorization) {
		t.Fatalf("GetForwardRecoveryActionOutcome() = %v", err)
	}
	if transactionCalls != 0 {
		t.Fatalf("transaction calls = %d, want 0", transactionCalls)
	}
}
