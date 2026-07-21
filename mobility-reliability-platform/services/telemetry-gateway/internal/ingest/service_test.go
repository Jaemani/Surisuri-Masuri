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

func TestServiceRejectsIncompleteVerifiedPrincipal(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryObjectStore()
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)
	principal.AppID = ""

	_, err := service.Ingest(context.Background(), principal, batch, raw)
	if !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("principal error = %v", err)
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
		fixedBatchIDGenerator(),
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

func TestServiceRejectsClientBatchReuseAcrossInstallations(t *testing.T) {
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
		[]byte(batch.InstallationID),
		[]byte("9e98ed5b-ce6a-407f-90ba-b452150dc9db"),
		1,
	)
	rotatedBatch, err := telemetry.DecodeBatch(bytes.NewReader(rotatedRaw))
	if err != nil {
		t.Fatalf("DecodeBatch() error = %v", err)
	}
	_, err = service.Ingest(context.Background(), principal, rotatedBatch, rotatedRaw)
	if !errors.Is(err, ErrClientBatchConflict) {
		t.Fatalf("client batch conflict error = %v", err)
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
	now := time.Date(2026, 7, 21, 6, 0, 0, 0, time.UTC)
	objectPath := "telemetry/v2/tenants/" + batch.TenantID +
		"/devices/" + batch.DeviceID +
		"/trips/" + batch.TripID +
		"/year=2026/month=07/day=21/01982015-4400-7000-8000-000000000001.json.gz"
	objects.objects[objectPath] = storedObject{bodyHash: "different", content: []byte("different")}
	service := mustService(t, receipts, objects, func() time.Time { return now })
	principal := matchingPrincipal(batch)

	for attempt := 0; attempt < 2; attempt++ {
		_, err := service.Ingest(context.Background(), principal, batch, raw)
		if !errors.Is(err, ErrObjectConflict) {
			t.Fatalf("attempt %d error = %v", attempt+1, err)
		}
	}
	receipt := receipts.receipts[deriveReservationKey(batch)]
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

func TestDerivedReservationKeyMatchesCrossLanguageContract(t *testing.T) {
	batch, _ := validBatch(t)
	const expected = "b8443d7fe776ca88dc5e738732a31419aad494de9313d71f60d4893c75157023"
	if actual := deriveReservationKey(batch); actual != expected {
		t.Fatalf("deriveReservationKey() = %q, want %q", actual, expected)
	}
}

type memoryReceiptStore struct {
	mu                      sync.Mutex
	receipts                map[string]Receipt
	clientBatchReservations map[string]string
	failNextMarkStored      bool
}

func newMemoryReceiptStore() *memoryReceiptStore {
	return &memoryReceiptStore{
		receipts:                make(map[string]Receipt),
		clientBatchReservations: make(map[string]string),
	}
}

func (s *memoryReceiptStore) Reserve(
	_ context.Context,
	reservation Reservation,
) (Receipt, ReservationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if current, ok := s.receipts[reservation.ReservationKey]; ok {
		if current.BodyHash != reservation.BodyHash || current.ClientBatchKey != reservation.ClientBatchKey {
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
	if _, ok := s.clientBatchReservations[reservation.ClientBatchKey]; ok {
		return Receipt{}, ReservationClientBatchConflict, nil
	}

	receipt := Receipt{
		ReservationKey: reservation.ReservationKey,
		ClientBatchKey: reservation.ClientBatchKey,
		ReceiptID:      reservation.ReceiptID,
		TenantID:       reservation.TenantID,
		BatchID:        reservation.BatchID,
		ClientBatchID:  reservation.ClientBatchID,
		BodyHash:       reservation.BodyHash,
		State:          ReceiptReserved,
		CreatedAt:      reservation.CreatedAt,
		UpdatedAt:      reservation.CreatedAt,
	}
	s.receipts[reservation.ReservationKey] = receipt
	s.clientBatchReservations[reservation.ClientBatchKey] = reservation.ReservationKey
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
		fixedBatchIDGenerator(),
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

type batchIDGeneratorFunc func() (string, error)

func (f batchIDGeneratorFunc) NewID() (string, error) {
	return f()
}

func fixedBatchIDGenerator() ServerBatchIDGenerator {
	return batchIDGeneratorFunc(func() (string, error) {
		return "01982015-4400-7000-8000-000000000001", nil
	})
}

func matchingPrincipal(_ telemetry.Batch) Principal {
	return Principal{FirebaseUID: "synthetic-firebase-uid", AppID: "synthetic-app-id"}
}

func validBatch(t *testing.T) (telemetry.Batch, []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "packages", "contracts", "fixtures", "telemetry-batch.v2.valid.json")
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
