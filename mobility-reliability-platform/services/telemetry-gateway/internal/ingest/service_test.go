package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

func TestServiceStoresCompressedBatchAndCompletesReceipt(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryObjectStore()
	now := time.Date(2026, 7, 21, 6, 0, 0, 0, time.UTC)
	service := mustService(t, receipts, objects, func() time.Time { return now })

	result, err := service.Ingest(context.Background(), matchingPrincipal(batch), batch, raw)
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if result.Replay {
		t.Fatal("first ingest marked as replay")
	}
	if result.Receipt.State != ReceiptStored || result.Receipt.SampleCount != len(batch.Samples) {
		t.Fatalf("receipt = %#v", result.Receipt)
	}

	stored := objects.contentAt(t, result.Receipt.ObjectPath)
	reader, err := gzip.NewReader(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	uncompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if !bytes.Equal(uncompressed, raw) {
		t.Fatal("stored object does not round-trip to the request body")
	}
}

func TestServiceReturnsCompletedReplayWithoutSecondObjectWrite(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryObjectStore()
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)

	if _, err := service.Ingest(context.Background(), principal, batch, raw); err != nil {
		t.Fatalf("first Ingest() error = %v", err)
	}
	result, err := service.Ingest(context.Background(), principal, batch, raw)
	if err != nil {
		t.Fatalf("replay Ingest() error = %v", err)
	}
	if !result.Replay {
		t.Fatal("second ingest was not marked as replay")
	}
	if objects.putCount != 1 {
		t.Fatalf("object writes = %d, want 1", objects.putCount)
	}
}

func TestServiceRejectsIdempotencyBodyConflict(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryObjectStore()
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)

	if _, err := service.Ingest(context.Background(), principal, batch, raw); err != nil {
		t.Fatalf("first Ingest() error = %v", err)
	}
	semanticallySameDifferentBody := append(append([]byte(nil), raw...), '\n')
	_, err := service.Ingest(
		context.Background(),
		principal,
		batch,
		semanticallySameDifferentBody,
	)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestServiceRejectsUnverifiedBatchIdentity(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryObjectStore()
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)
	principal.ActorID = "00000000-0000-4000-8000-000000000099"

	_, err := service.Ingest(context.Background(), principal, batch, raw)
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("identity error = %v", err)
	}
	if len(receipts.receipts) != 0 || objects.putCount != 0 {
		t.Fatal("identity mismatch wrote storage state")
	}
}

func TestServiceRejectsUnauthorizedBatchScopeBeforeStorage(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryObjectStore()
	service, err := NewService(
		receipts,
		objects,
		authorizerFunc(func(context.Context, Principal, BatchScope) error {
			return ErrBatchUnauthorized
		}),
		nil,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	_, err = service.Ingest(context.Background(), matchingPrincipal(batch), batch, raw)
	if !errors.Is(err, ErrBatchUnauthorized) {
		t.Fatalf("authorization error = %v", err)
	}
	if len(receipts.receipts) != 0 || objects.putCount != 0 {
		t.Fatal("unauthorized batch wrote storage state")
	}
}

func TestServiceRejectsBatchIDReuseWithDifferentIdempotencyKey(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryObjectStore()
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)

	if _, err := service.Ingest(context.Background(), principal, batch, raw); err != nil {
		t.Fatalf("first Ingest() error = %v", err)
	}
	rotatedRaw := bytes.Replace(
		raw,
		[]byte("session-001-batch-0001"),
		[]byte("session-001-batch-rotated"),
		1,
	)
	rotatedBatch, err := telemetry.DecodeBatch(bytes.NewReader(rotatedRaw))
	if err != nil {
		t.Fatalf("DecodeBatch() error = %v", err)
	}
	_, err = service.Ingest(context.Background(), principal, rotatedBatch, rotatedRaw)
	if !errors.Is(err, ErrBatchIDConflict) {
		t.Fatalf("batch conflict error = %v", err)
	}
	if objects.putCount != 1 {
		t.Fatalf("object writes = %d, want 1", objects.putCount)
	}
}

func TestServiceRecoversPendingReceiptWithoutDuplicateObject(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	receipts.failNextMarkStored = true
	objects := newMemoryObjectStore()
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)

	if _, err := service.Ingest(context.Background(), principal, batch, raw); err == nil {
		t.Fatal("first Ingest() error = nil, want receipt completion failure")
	}
	result, err := service.Ingest(context.Background(), principal, batch, raw)
	if err != nil {
		t.Fatalf("retry Ingest() error = %v", err)
	}
	if !result.Replay || result.Receipt.State != ReceiptStored {
		t.Fatalf("retry result = %#v", result)
	}
	if objects.putCount != 1 {
		t.Fatalf("object writes = %d, want 1", objects.putCount)
	}
}

func TestServicePersistsTerminalObjectConflict(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryObjectStore()
	objectPath := "telemetry/" + batch.TenantID + "/" + batch.SessionID + "/" + batch.BatchID + ".json.gz"
	objects.objects[objectPath] = storedObject{bodyHash: "different", content: []byte("different")}
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)

	for attempt := 0; attempt < 2; attempt++ {
		_, err := service.Ingest(context.Background(), principal, batch, raw)
		if !errors.Is(err, ErrObjectConflict) {
			t.Fatalf("attempt %d error = %v", attempt+1, err)
		}
	}
	receipt := receipts.receipts[batch.TenantID+":"+batch.IdempotencyKey]
	if receipt.State != ReceiptRejected || receipt.RejectionCode != "object_conflict" {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestDeterministicGZIP(t *testing.T) {
	raw := []byte("same telemetry body")
	first, err := deterministicGZIP(raw)
	if err != nil {
		t.Fatalf("first gzip: %v", err)
	}
	second, err := deterministicGZIP(raw)
	if err != nil {
		t.Fatalf("second gzip: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("gzip output is not deterministic")
	}
}

type memoryReceiptStore struct {
	mu                 sync.Mutex
	receipts           map[string]Receipt
	batchReservations  map[string]string
	failNextMarkStored bool
}

func newMemoryReceiptStore() *memoryReceiptStore {
	return &memoryReceiptStore{
		receipts:          make(map[string]Receipt),
		batchReservations: make(map[string]string),
	}
}

func (s *memoryReceiptStore) Reserve(
	_ context.Context,
	reservation Reservation,
) (Receipt, ReservationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if current, ok := s.receipts[reservation.ReservationKey]; ok {
		if current.BodyHash != reservation.BodyHash || current.BatchKey != reservation.BatchKey {
			return current, ReservationConflict, nil
		}
		if current.State == ReceiptStored {
			return current, ReservationReplayComplete, nil
		}
		if current.State == ReceiptRejected {
			return current, ReservationReplayRejected, nil
		}
		return current, ReservationReplayPending, nil
	}
	if _, ok := s.batchReservations[reservation.BatchKey]; ok {
		return Receipt{}, ReservationBatchConflict, nil
	}

	receipt := Receipt{
		ReservationKey: reservation.ReservationKey,
		BatchKey:       reservation.BatchKey,
		ReceiptID:      reservation.ReceiptID,
		TenantID:       reservation.TenantID,
		BatchID:        reservation.BatchID,
		BodyHash:       reservation.BodyHash,
		State:          ReceiptReserved,
		CreatedAt:      reservation.CreatedAt,
		UpdatedAt:      reservation.CreatedAt,
	}
	s.receipts[reservation.ReservationKey] = receipt
	s.batchReservations[reservation.BatchKey] = reservation.ReservationKey
	return receipt, ReservationCreated, nil
}

func (s *memoryReceiptStore) MarkStored(
	_ context.Context,
	reservationKey string,
	objectPath string,
	sampleCount int,
	updatedAt time.Time,
) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	receipt, ok := s.receipts[reservationKey]
	if !ok {
		return Receipt{}, errors.New("receipt not found")
	}
	if s.failNextMarkStored {
		s.failNextMarkStored = false
		return Receipt{}, errors.New("synthetic receipt completion failure")
	}
	receipt.ObjectPath = objectPath
	receipt.SampleCount = sampleCount
	receipt.State = ReceiptStored
	receipt.UpdatedAt = updatedAt
	s.receipts[reservationKey] = receipt
	return receipt, nil
}

func (s *memoryReceiptStore) MarkRejected(
	_ context.Context,
	reservationKey string,
	rejectionCode string,
	updatedAt time.Time,
) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	receipt, ok := s.receipts[reservationKey]
	if !ok {
		return Receipt{}, errors.New("receipt not found")
	}
	receipt.State = ReceiptRejected
	receipt.RejectionCode = rejectionCode
	receipt.UpdatedAt = updatedAt
	s.receipts[reservationKey] = receipt
	return receipt, nil
}

type storedObject struct {
	bodyHash string
	content  []byte
}

type memoryObjectStore struct {
	mu       sync.Mutex
	objects  map[string]storedObject
	putCount int
}

func newMemoryObjectStore() *memoryObjectStore {
	return &memoryObjectStore{objects: make(map[string]storedObject)}
}

func (s *memoryObjectStore) PutIfAbsent(
	_ context.Context,
	path string,
	content []byte,
	bodyHash string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.objects[path]; ok {
		if existing.bodyHash != bodyHash {
			return ErrObjectConflict
		}
		return nil
	}
	s.objects[path] = storedObject{bodyHash: bodyHash, content: append([]byte(nil), content...)}
	s.putCount++
	return nil
}

func (s *memoryObjectStore) contentAt(t *testing.T, path string) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[path]
	if !ok {
		t.Fatalf("object %q not found", path)
	}
	return append([]byte(nil), object.content...)
}

func mustService(
	t *testing.T,
	receipts ReceiptStore,
	objects ObjectStore,
	now func() time.Time,
) *Service {
	t.Helper()
	service, err := NewService(
		receipts,
		objects,
		authorizerFunc(func(context.Context, Principal, BatchScope) error { return nil }),
		now,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

type authorizerFunc func(context.Context, Principal, BatchScope) error

func (f authorizerFunc) Authorize(ctx context.Context, principal Principal, scope BatchScope) error {
	return f(ctx, principal, scope)
}

func matchingPrincipal(batch telemetry.Batch) Principal {
	return Principal{TenantID: batch.TenantID, ActorID: batch.ActorID}
}

func validBatch(t *testing.T) (telemetry.Batch, []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "packages", "contracts", "fixtures", "telemetry-batch.valid.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	batch, err := telemetry.DecodeBatch(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeBatch() error = %v", err)
	}
	return batch, raw
}
