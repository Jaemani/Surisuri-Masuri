package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestNewFirestoreForwardRecoveryCandidateStoreRejectsInvalidDependencies(t *testing.T) {
	if _, err := NewFirestoreForwardRecoveryCandidateStore(nil, time.Second); err == nil {
		t.Fatal("constructor error = nil")
	}
	if _, err := NewFirestoreForwardRecoveryCandidateStore(nil, 31*time.Second); err == nil {
		t.Fatal("constructor unbounded-timeout error = nil")
	}
}

func TestFirestoreForwardRecoveryCandidateStoreRejectsInvalidRequestBeforeQuery(t *testing.T) {
	store := &FirestoreForwardRecoveryCandidateStore{timeout: time.Second}
	page, err := store.ListDueForwardRecoveryCandidates(
		context.Background(),
		"not-a-tenant",
		time.Now(),
		nil,
		1,
	)
	if !errors.Is(err, ingest.ErrForwardRecoveryCandidateUnavailable) || len(page.Candidates) != 0 {
		t.Fatalf("ListDueForwardRecoveryCandidates() = %#v, %v", page, err)
	}
}

func TestNormalizeForwardRecoveryCandidateErrorPreservesOnlyContextClass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := normalizeForwardRecoveryCandidateError(ctx, errors.New("provider secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("normalize error = %v", err)
	}
	if err := normalizeForwardRecoveryCandidateError(context.Background(), errors.New("provider secret")); !errors.Is(err, ingest.ErrForwardRecoveryCandidateUnavailable) || err.Error() == "provider secret" {
		t.Fatalf("normalize error = %v", err)
	}
}
