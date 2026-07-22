package firebaseadapter

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreEmulatorForwardRecoveryCheckpointCompareAndSetAndReset(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	tenantID := "abababab-abab-4bab-8bab-abababababab"
	resetForwardRecoveryCheckpoint(t, client, tenantID)
	store, err := NewFirestoreForwardRecoveryCheckpointStore(client, 5*time.Second)
	if err != nil {
		t.Fatalf("NewFirestoreForwardRecoveryCheckpointStore() error = %v", err)
	}
	initial, err := store.LoadForwardRecoveryCheckpoint(context.Background(), tenantID)
	if err != nil || initial.Revision != 0 || initial.Cursor != nil {
		t.Fatalf("initial checkpoint = %#v, %v", initial, err)
	}
	cursor := ingest.ForwardRecoveryCursor{
		NextRecoveryAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		DocumentID:     "cdcdcdcd-cdcd-4dcd-8dcd-cdcdcdcdcdcd",
	}
	scanCutoff := cursor.NextRecoveryAt.Add(30 * time.Second)
	swapped, err := store.CompareAndSetForwardRecoveryCheckpoint(
		context.Background(), tenantID, 0, &cursor, scanCutoff, cursor.NextRecoveryAt.Add(time.Minute),
	)
	if err != nil || !swapped {
		t.Fatalf("first compare-and-set = %v, %v", swapped, err)
	}
	loaded, err := store.LoadForwardRecoveryCheckpoint(context.Background(), tenantID)
	if err != nil || loaded.Revision != 1 || loaded.Cursor == nil ||
		loaded.Cursor.DocumentID != cursor.DocumentID ||
		!loaded.Cursor.NextRecoveryAt.Equal(cursor.NextRecoveryAt) ||
		!loaded.ScanCutoff.Equal(scanCutoff) {
		t.Fatalf("loaded checkpoint = %#v, %v", loaded, err)
	}
	staleSwap, err := store.CompareAndSetForwardRecoveryCheckpoint(
		context.Background(), tenantID, 0, nil, time.Time{}, cursor.NextRecoveryAt.Add(2*time.Minute),
	)
	if err != nil || staleSwap {
		t.Fatalf("stale compare-and-set = %v, %v", staleSwap, err)
	}
	reset, err := store.CompareAndSetForwardRecoveryCheckpoint(
		context.Background(), tenantID, 1, nil, time.Time{}, cursor.NextRecoveryAt.Add(3*time.Minute),
	)
	if err != nil || !reset {
		t.Fatalf("reset compare-and-set = %v, %v", reset, err)
	}
	cleared, err := store.LoadForwardRecoveryCheckpoint(context.Background(), tenantID)
	if err != nil || cleared.Revision != 2 || cleared.Cursor != nil {
		t.Fatalf("cleared checkpoint = %#v, %v", cleared, err)
	}
}

func TestFirestoreAdmissionStoreEmulatorForwardRecoveryCheckpointConcurrentCASHasOneWinner(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	tenantID := "dededede-dede-4ede-8ede-dededededede"
	resetForwardRecoveryCheckpoint(t, client, tenantID)
	store, err := NewFirestoreForwardRecoveryCheckpointStore(client, 5*time.Second)
	if err != nil {
		t.Fatalf("NewFirestoreForwardRecoveryCheckpointStore() error = %v", err)
	}
	updatedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	cursors := []ingest.ForwardRecoveryCursor{
		{NextRecoveryAt: updatedAt.Add(-2 * time.Minute), DocumentID: "efefefef-efef-4fef-8fef-efefefefefe1"},
		{NextRecoveryAt: updatedAt.Add(-time.Minute), DocumentID: "efefefef-efef-4fef-8fef-efefefefefe2"},
	}
	type outcome struct {
		swapped bool
		err     error
	}
	outcomes := make([]outcome, len(cursors))
	var wait sync.WaitGroup
	for index := range cursors {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			outcomes[index].swapped, outcomes[index].err = store.CompareAndSetForwardRecoveryCheckpoint(
				context.Background(), tenantID, 0, &cursors[index], updatedAt, updatedAt,
			)
		}(index)
	}
	wait.Wait()
	winners := 0
	for _, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent compare-and-set error = %v", outcome.err)
		}
		if outcome.swapped {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent winners = %d; outcomes = %#v", winners, outcomes)
	}
	loaded, err := store.LoadForwardRecoveryCheckpoint(context.Background(), tenantID)
	if err != nil || loaded.Revision != 1 || loaded.Cursor == nil {
		t.Fatalf("loaded checkpoint = %#v, %v", loaded, err)
	}
}

func TestFirestoreAdmissionStoreEmulatorForwardRecoveryCheckpointFailsClosedOnMalformedState(t *testing.T) {
	client := newAdmissionEmulatorClient(t)
	tenantID := "fafafafa-fafa-4afa-8afa-fafafafafafa"
	_, err := client.Doc(forwardRecoveryCheckpointDocumentPath(tenantID)).Set(
		context.Background(),
		map[string]any{
			"revision":         int64(1),
			"next_recovery_at": time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
			"updated_at":       time.Date(2026, 7, 22, 12, 1, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatalf("seed malformed checkpoint: %v", err)
	}
	store, err := NewFirestoreForwardRecoveryCheckpointStore(client, 5*time.Second)
	if err != nil {
		t.Fatalf("NewFirestoreForwardRecoveryCheckpointStore() error = %v", err)
	}
	checkpoint, err := store.LoadForwardRecoveryCheckpoint(context.Background(), tenantID)
	if !errors.Is(err, ingest.ErrForwardRecoveryCheckpointUnavailable) || checkpoint.Cursor != nil {
		t.Fatalf("LoadForwardRecoveryCheckpoint() = %#v, %v", checkpoint, err)
	}
}

func resetForwardRecoveryCheckpoint(t *testing.T, client *firestore.Client, tenantID string) {
	t.Helper()
	if _, err := client.Doc(forwardRecoveryCheckpointDocumentPath(tenantID)).Delete(context.Background()); err != nil {
		t.Fatalf("reset forward recovery checkpoint: %v", err)
	}
}
