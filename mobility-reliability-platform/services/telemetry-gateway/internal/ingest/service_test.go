package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

func TestServiceStoresCompressedBatchAndCompletesReceipt(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryArtifactStore()
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
	if result.Receipt.DeviceID != batch.DeviceID ||
		result.Receipt.TripID != batch.TripID ||
		result.Receipt.InstallationID != batch.InstallationID ||
		result.Receipt.ConsentRevisionID != batch.ConsentRevisionID ||
		result.Receipt.PayloadSchemaVersion != batch.SchemaVersion ||
		!result.Receipt.ExpiresAt.Equal(now.Add(ReceiptRetention)) {
		t.Fatalf("receipt lineage = %#v", result.Receipt)
	}
	firstCapturedAt, lastCapturedAt := capturedAtBounds(batch)
	wantManifest := BatchManifestInput{
		PayloadSchemaVersion: result.Receipt.PayloadSchemaVersion,
		TenantID:             result.Receipt.TenantID,
		DeviceID:             result.Receipt.DeviceID,
		TripID:               result.Receipt.TripID,
		InstallationID:       result.Receipt.InstallationID,
		BatchID:              result.Receipt.BatchID,
		ClientBatchID:        result.Receipt.ClientBatchID,
		ConsentRevisionID:    result.Receipt.ConsentRevisionID,
		BodyHash:             result.Receipt.BodyHash,
		SampleCount:          len(batch.Samples),
		FirstCapturedAt:      firstCapturedAt,
		LastCapturedAt:       lastCapturedAt,
		ReceivedAt:           result.Receipt.CreatedAt,
		ExpiresAt:            result.Receipt.ExpiresAt,
		ValidatorVersion:     TelemetryValidatorVersion,
	}
	if !reflect.DeepEqual(objects.lastWrite.Manifest, wantManifest) {
		t.Fatalf("artifact manifest input = %#v, want %#v", objects.lastWrite.Manifest, wantManifest)
	}
	if objects.lastWrite.ObjectPath != ExpectedTelemetryObjectPath(objects.lastWrite.Manifest) ||
		objects.lastWrite.ManifestPath != ExpectedTelemetryManifestPath(objects.lastWrite.Manifest) {
		t.Fatalf("artifact paths = %q / %q", objects.lastWrite.ObjectPath, objects.lastWrite.ManifestPath)
	}
	assertReceiptArtifactLineage(t, result.Receipt, objects.lastStored)
	if !reflect.DeepEqual(receipts.lastStoredData, StoredReceiptData{
		Artifacts:   objects.lastStored,
		SampleCount: len(batch.Samples),
	}) {
		t.Fatalf("finalizer data = %#v, stored = %#v", receipts.lastStoredData, objects.lastStored)
	}

	stored := objects.rawContentAt(t, result.Receipt.ObjectPath)
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
	objects := newMemoryArtifactStore()
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
	if objects.rawWrites != 1 || objects.manifestWrites != 1 || objects.storeCalls != 1 {
		t.Fatalf("artifact writes/calls = %d/%d/%d, want 1/1/1", objects.rawWrites, objects.manifestWrites, objects.storeCalls)
	}
	if receipts.authorizeCalls != 2 {
		t.Fatalf("authorization calls = %d, want 2", receipts.authorizeCalls)
	}
}

func TestServiceRejectsIdempotencyBodyConflict(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryArtifactStore()
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
	if receipts.authorizeCalls != 2 {
		t.Fatalf("authorization calls = %d, want 2", receipts.authorizeCalls)
	}
}

func TestServiceUsesFreshCompletionTimeAfterObjectStorage(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryArtifactStore()
	base := time.Date(2026, time.July, 21, 6, 0, 0, 0, time.UTC)
	call := 0
	service := mustService(t, receipts, objects, func() time.Time {
		value := base.Add(time.Duration(call) * time.Second)
		call++
		return value
	})

	result, err := service.Ingest(context.Background(), matchingPrincipal(batch), batch, raw)
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if !result.Receipt.CreatedAt.Equal(base) || !result.Receipt.UpdatedAt.Equal(base.Add(time.Second)) {
		t.Fatalf("receipt times = %s, %s", result.Receipt.CreatedAt, result.Receipt.UpdatedAt)
	}
}

func TestNewServiceRequiresEveryDependency(t *testing.T) {
	admissions := newMemoryReceiptStore()
	objects := newMemoryArtifactStore()
	batchIDs := fixedBatchIDGenerator()
	tests := []struct {
		name       string
		admissions AdmissionStore
		objects    TelemetryArtifactStore
		batchIDs   ServerBatchIDGenerator
	}{
		{name: "nil admissions", objects: objects, batchIDs: batchIDs},
		{name: "nil objects", admissions: admissions, batchIDs: batchIDs},
		{name: "nil batch IDs", admissions: admissions, objects: objects},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewService(test.admissions, test.objects, test.batchIDs, nil); err == nil {
				t.Fatal("NewService() error = nil")
			}
		})
	}
}

func TestServiceRejectsIncompleteVerifiedPrincipal(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryArtifactStore()
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)
	principal.AppID = ""

	_, err := service.Ingest(context.Background(), principal, batch, raw)
	if !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("principal error = %v", err)
	}
	if len(receipts.receipts) != 0 || objects.storeCalls != 0 {
		t.Fatal("identity mismatch wrote storage state")
	}
}

func TestServiceRejectsUnauthorizedBatchScopeBeforeStorage(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	receipts.authorize = func(context.Context, Principal, BatchScope) error {
		return ErrBatchUnauthorized
	}
	objects := newMemoryArtifactStore()
	service, err := NewService(
		receipts,
		objects,
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
	if len(receipts.receipts) != 0 || objects.storeCalls != 0 {
		t.Fatal("unauthorized batch wrote storage state")
	}
}

func TestServicePassesIdentityAndCapturedAtBoundsToAtomicAdmission(t *testing.T) {
	batch, raw := validBatch(t)
	original := batch.Samples[0]
	second := original
	second.ClientSampleID = "85fd63ad-0358-49dd-9dad-dbcdfd324818"
	second.CapturedAt = "2026-07-21T08:10:00Z"
	sequence := int64(1)
	second.Sequence = &sequence
	batch.Samples = append(batch.Samples, second)
	receipts := newMemoryReceiptStore()
	receipts.authorize = func(context.Context, Principal, BatchScope) error {
		return ErrBatchUnauthorized
	}
	objects := newMemoryArtifactStore()
	service := mustService(t, receipts, objects, nil)
	principal := matchingPrincipal(batch)

	if _, err := service.Ingest(context.Background(), principal, batch, raw); !errors.Is(err, ErrBatchUnauthorized) {
		t.Fatalf("Ingest() error = %v", err)
	}
	if receipts.authorizeCalls != 1 {
		t.Fatalf("authorization calls = %d", receipts.authorizeCalls)
	}
	if receipts.lastPrincipal != principal {
		t.Fatalf("principal = %#v", receipts.lastPrincipal)
	}
	wantFirst := time.Date(2026, time.July, 21, 8, 10, 0, 0, time.UTC)
	wantLast := time.Date(2026, time.July, 21, 8, 29, 50, 0, time.UTC)
	if !receipts.lastScope.FirstCapturedAt.Equal(wantFirst) ||
		!receipts.lastScope.LastCapturedAt.Equal(wantLast) {
		t.Fatalf("scope times = %s, %s", receipts.lastScope.FirstCapturedAt, receipts.lastScope.LastCapturedAt)
	}
}

func TestCapturedAtBoundsUsesTimeOrderInsteadOfSampleOrder(t *testing.T) {
	batch, _ := validBatch(t)
	original := batch.Samples[0]
	middle := original
	middle.CapturedAt = "2026-07-21T08:20:00Z"
	first := original
	first.CapturedAt = "2026-07-21T08:10:00Z"
	batch.Samples = []telemetry.Sample{original, middle, first}

	gotFirst, gotLast := capturedAtBounds(batch)
	wantFirst := time.Date(2026, time.July, 21, 8, 10, 0, 0, time.UTC)
	wantLast := time.Date(2026, time.July, 21, 8, 29, 50, 0, time.UTC)
	if !gotFirst.Equal(wantFirst) || !gotLast.Equal(wantLast) {
		t.Fatalf("capturedAtBounds() = %s, %s", gotFirst, gotLast)
	}
}

func TestServiceRejectsClientBatchReuseAcrossInstallations(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryArtifactStore()
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
	if objects.rawWrites != 1 || objects.manifestWrites != 1 {
		t.Fatalf("artifact writes = %d/%d, want 1/1", objects.rawWrites, objects.manifestWrites)
	}
}

func TestServiceRetriesAfterRawArtifactFailure(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	artifacts := newMemoryArtifactStore()
	artifacts.failNextRaw = true
	service := mustService(t, receipts, artifacts, nil)
	principal := matchingPrincipal(batch)

	if _, err := service.Ingest(context.Background(), principal, batch, raw); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("first Ingest() error = %v, want artifact unavailable", err)
	}
	if artifacts.rawWrites != 0 || artifacts.manifestWrites != 0 || receipts.markStoredCalls != 0 {
		t.Fatalf("failed raw attempt wrote state = %d/%d/%d", artifacts.rawWrites, artifacts.manifestWrites, receipts.markStoredCalls)
	}
	result, err := service.Ingest(context.Background(), principal, batch, raw)
	if err != nil {
		t.Fatalf("retry Ingest() error = %v", err)
	}
	if !result.Replay || artifacts.rawWrites != 1 || artifacts.manifestWrites != 1 || receipts.markStoredCalls != 1 {
		t.Fatalf("retry state = %#v, writes/finalizers = %d/%d/%d", result, artifacts.rawWrites, artifacts.manifestWrites, receipts.markStoredCalls)
	}
}

func TestServiceRecoversManifestFailureWithoutDuplicateRaw(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	artifacts := newMemoryArtifactStore()
	artifacts.failNextManifest = true
	service := mustService(t, receipts, artifacts, nil)
	principal := matchingPrincipal(batch)

	if _, err := service.Ingest(context.Background(), principal, batch, raw); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("first Ingest() error = %v, want artifact unavailable", err)
	}
	if artifacts.rawWrites != 1 || artifacts.manifestWrites != 0 || receipts.markStoredCalls != 0 {
		t.Fatalf("manifest failure state = %d/%d/%d", artifacts.rawWrites, artifacts.manifestWrites, receipts.markStoredCalls)
	}
	result, err := service.Ingest(context.Background(), principal, batch, raw)
	if err != nil {
		t.Fatalf("retry Ingest() error = %v", err)
	}
	if !result.Replay || artifacts.rawWrites != 1 || artifacts.manifestWrites != 1 || receipts.markStoredCalls != 1 {
		t.Fatalf("retry state = %#v, writes/finalizers = %d/%d/%d", result, artifacts.rawWrites, artifacts.manifestWrites, receipts.markStoredCalls)
	}
	if !artifacts.lastStored.Object.Replay || artifacts.lastStored.Manifest.Replay {
		t.Fatalf("replay flags = %#v", artifacts.lastStored)
	}
}

func TestServiceRecoversFinalizerFailureWithoutDuplicateArtifacts(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	receipts.failNextMarkStored = true
	objects := newMemoryArtifactStore()
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
	if objects.rawWrites != 1 || objects.manifestWrites != 1 || objects.storeCalls != 2 || receipts.markStoredCalls != 2 {
		t.Fatalf("writes/calls/finalizers = %d/%d/%d/%d, want 1/1/2/2", objects.rawWrites, objects.manifestWrites, objects.storeCalls, receipts.markStoredCalls)
	}
	if !objects.lastStored.Object.Replay || !objects.lastStored.Manifest.Replay {
		t.Fatalf("finalizer retry artifact flags = %#v", objects.lastStored)
	}
	if !reflect.DeepEqual(receipts.lastStoredData.Artifacts, objects.lastStored) {
		t.Fatalf("finalizer retry data = %#v, stored = %#v", receipts.lastStoredData, objects.lastStored)
	}
}

func TestServicePersistsTerminalObjectConflict(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	objects := newMemoryArtifactStore()
	now := time.Date(2026, 7, 21, 6, 0, 0, 0, time.UTC)
	objects.rawConflict = true
	service := mustService(t, receipts, objects, func() time.Time { return now })
	principal := matchingPrincipal(batch)

	for attempt := 0; attempt < 2; attempt++ {
		_, err := service.Ingest(context.Background(), principal, batch, raw)
		if !errors.Is(err, ErrObjectConflict) {
			t.Fatalf("attempt %d error = %v", attempt+1, err)
		}
	}
	receipt := receipts.receipts[DeriveReservationKey(
		batch.SchemaVersion,
		batch.TenantID,
		batch.InstallationID,
		batch.ClientBatchID,
	)]
	if receipt.State != ReceiptRejected || receipt.RejectionCode != "object_conflict" {
		t.Fatalf("receipt = %#v", receipt)
	}
	if receipts.markRejectedCalls != 1 {
		t.Fatalf("rejection finalizer calls = %d, want 1", receipts.markRejectedCalls)
	}
}

func TestServiceLeavesManifestConflictReserved(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	artifacts := newMemoryArtifactStore()
	artifacts.manifestConflict = true
	service := mustService(t, receipts, artifacts, nil)
	principal := matchingPrincipal(batch)

	for attempt := 0; attempt < 2; attempt++ {
		_, err := service.Ingest(context.Background(), principal, batch, raw)
		if !errors.Is(err, ErrManifestArtifactConflict) {
			t.Fatalf("attempt %d error = %v, want manifest conflict", attempt+1, err)
		}
	}
	receipt := receipts.receipts[DeriveReservationKey(
		batch.SchemaVersion,
		batch.TenantID,
		batch.InstallationID,
		batch.ClientBatchID,
	)]
	if receipt.State != ReceiptReserved || receipt.RejectionCode != "" {
		t.Fatalf("manifest conflict receipt = %#v, want reserved", receipt)
	}
	if receipts.markRejectedCalls != 0 || receipts.markStoredCalls != 0 {
		t.Fatalf("manifest conflict finalizers = %d/%d, want 0/0", receipts.markRejectedCalls, receipts.markStoredCalls)
	}
	if artifacts.rawWrites != 1 || artifacts.manifestWrites != 0 {
		t.Fatalf("manifest conflict artifact writes = %d/%d, want 1/0", artifacts.rawWrites, artifacts.manifestWrites)
	}
}

func TestServiceLeavesGenericArtifactConflictReserved(t *testing.T) {
	batch, raw := validBatch(t)
	receipts := newMemoryReceiptStore()
	artifacts := newMemoryArtifactStore()
	artifacts.genericConflict = true
	service := mustService(t, receipts, artifacts, nil)

	_, err := service.Ingest(context.Background(), matchingPrincipal(batch), batch, raw)
	if !errors.Is(err, ErrArtifactConflict) || errors.Is(err, ErrRawArtifactConflict) {
		t.Fatalf("generic conflict error = %v", err)
	}
	receipt := receipts.receipts[DeriveReservationKey(
		batch.SchemaVersion,
		batch.TenantID,
		batch.InstallationID,
		batch.ClientBatchID,
	)]
	if receipt.State != ReceiptReserved || receipts.markRejectedCalls != 0 || receipts.markStoredCalls != 0 {
		t.Fatalf("generic conflict state = %#v, finalizers = %d/%d", receipt, receipts.markRejectedCalls, receipts.markStoredCalls)
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
	if actual := DeriveReservationKey(batch.SchemaVersion, batch.TenantID, batch.InstallationID, batch.ClientBatchID); actual != expected {
		t.Fatalf("DeriveReservationKey() = %q, want %q", actual, expected)
	}
}

func TestDerivedClientBatchKeyMatchesCrossLanguageContract(t *testing.T) {
	batch, _ := validBatch(t)
	const expected = "0a1c43bb1a86c8b2ec556feed55c7595552dbed7a8a0d86ba24c418a641e1828"
	if actual := DeriveClientBatchKey(batch.TenantID, batch.ClientBatchID); actual != expected {
		t.Fatalf("DeriveClientBatchKey() = %q, want %q", actual, expected)
	}
}

type memoryReceiptStore struct {
	mu                      sync.Mutex
	receipts                map[string]Receipt
	clientBatchReservations map[string]string
	failNextMarkStored      bool
	markStoredCalls         int
	markRejectedCalls       int
	lastStoredData          StoredReceiptData
	authorize               func(context.Context, Principal, BatchScope) error
	authorizeCalls          int
	lastPrincipal           Principal
	lastScope               BatchScope
}

func newMemoryReceiptStore() *memoryReceiptStore {
	return &memoryReceiptStore{
		receipts:                make(map[string]Receipt),
		clientBatchReservations: make(map[string]string),
	}
}

func (s *memoryReceiptStore) AuthorizeAndReserve(
	ctx context.Context,
	principal Principal,
	scope BatchScope,
	reservation Reservation,
) (Receipt, ReservationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorizeCalls++
	s.lastPrincipal = principal
	s.lastScope = scope
	if s.authorize != nil {
		if err := s.authorize(ctx, principal, scope); err != nil {
			return Receipt{}, "", err
		}
	}

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
		ReservationKey:       reservation.ReservationKey,
		ClientBatchKey:       reservation.ClientBatchKey,
		ReceiptID:            reservation.ReceiptID,
		TenantID:             reservation.TenantID,
		BatchID:              reservation.BatchID,
		DeviceID:             reservation.DeviceID,
		TripID:               reservation.TripID,
		InstallationID:       reservation.InstallationID,
		ConsentRevisionID:    reservation.ConsentRevisionID,
		ClientBatchID:        reservation.ClientBatchID,
		PayloadSchemaVersion: reservation.PayloadSchemaVersion,
		BodyHash:             reservation.BodyHash,
		State:                ReceiptReserved,
		Revision:             1,
		CreatedAt:            reservation.CreatedAt,
		UpdatedAt:            reservation.CreatedAt,
		ExpiresAt:            reservation.ExpiresAt,
	}
	s.receipts[reservation.ReservationKey] = receipt
	s.clientBatchReservations[reservation.ClientBatchKey] = reservation.ReservationKey
	return receipt, ReservationCreated, nil
}

func (s *memoryReceiptStore) MarkStored(
	_ context.Context,
	_ string,
	reservationKey string,
	stored StoredReceiptData,
	updatedAt time.Time,
) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markStoredCalls++
	s.lastStoredData = stored

	receipt, ok := s.receipts[reservationKey]
	if !ok {
		return Receipt{}, errors.New("receipt not found")
	}
	if s.failNextMarkStored {
		s.failNextMarkStored = false
		return Receipt{}, errors.New("synthetic receipt completion failure")
	}
	receipt.ObjectPath = stored.Artifacts.Object.Path
	receipt.ObjectSHA256 = stored.Artifacts.Object.SHA256
	receipt.ObjectCRC32C = stored.Artifacts.Object.CRC32C
	receipt.ObjectSize = stored.Artifacts.Object.Size
	receipt.ObjectGeneration = stored.Artifacts.Object.Generation
	receipt.ObjectMetageneration = stored.Artifacts.Object.Metageneration
	receipt.ManifestPath = stored.Artifacts.Manifest.Path
	receipt.ManifestSHA256 = stored.Artifacts.Manifest.SHA256
	receipt.ManifestCRC32C = stored.Artifacts.Manifest.CRC32C
	receipt.ManifestSize = stored.Artifacts.Manifest.Size
	receipt.ManifestGeneration = stored.Artifacts.Manifest.Generation
	receipt.ManifestMetageneration = stored.Artifacts.Manifest.Metageneration
	receipt.SampleCount = stored.SampleCount
	receipt.State = ReceiptStored
	receipt.Revision++
	receipt.UpdatedAt = updatedAt
	s.receipts[reservationKey] = receipt
	return receipt, nil
}

func (s *memoryReceiptStore) MarkRejected(
	_ context.Context,
	_ string,
	reservationKey string,
	rejectionCode string,
	updatedAt time.Time,
) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markRejectedCalls++

	receipt, ok := s.receipts[reservationKey]
	if !ok {
		return Receipt{}, errors.New("receipt not found")
	}
	receipt.State = ReceiptRejected
	receipt.RejectionCode = rejectionCode
	receipt.Revision++
	receipt.UpdatedAt = updatedAt
	s.receipts[reservationKey] = receipt
	return receipt, nil
}

type memoryStoredArtifact struct {
	content  []byte
	artifact StoredArtifact
}

type memoryArtifactStore struct {
	mu               sync.Mutex
	raw              map[string]memoryStoredArtifact
	manifests        map[string]memoryStoredArtifact
	storeCalls       int
	rawWrites        int
	manifestWrites   int
	nextGeneration   int64
	failNextRaw      bool
	failNextManifest bool
	rawConflict      bool
	manifestConflict bool
	genericConflict  bool
	lastWrite        BatchArtifactWrite
	lastStored       StoredBatchArtifacts
}

func newMemoryArtifactStore() *memoryArtifactStore {
	return &memoryArtifactStore{
		raw:            make(map[string]memoryStoredArtifact),
		manifests:      make(map[string]memoryStoredArtifact),
		nextGeneration: 1000,
	}
}

func (s *memoryArtifactStore) StoreBatch(
	_ context.Context,
	write BatchArtifactWrite,
) (StoredBatchArtifacts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storeCalls++
	s.lastWrite = BatchArtifactWrite{
		ObjectPath:     write.ObjectPath,
		ManifestPath:   write.ManifestPath,
		CompressedBody: append([]byte(nil), write.CompressedBody...),
		Manifest:       write.Manifest,
	}

	if write.ObjectPath != ExpectedTelemetryObjectPath(write.Manifest) ||
		write.ManifestPath != ExpectedTelemetryManifestPath(write.Manifest) {
		return StoredBatchArtifacts{}, ErrArtifactUnavailable
	}
	if s.failNextRaw {
		s.failNextRaw = false
		return StoredBatchArtifacts{}, ErrArtifactUnavailable
	}
	if s.rawConflict {
		return StoredBatchArtifacts{}, errors.Join(ErrRawArtifactConflict, ErrArtifactConflict)
	}
	if s.genericConflict {
		return StoredBatchArtifacts{}, ErrArtifactConflict
	}

	objectDigest := ComputeArtifactDigest(write.CompressedBody)
	object, exists := s.raw[write.ObjectPath]
	objectArtifact := object.artifact
	if exists {
		if !bytes.Equal(object.content, write.CompressedBody) {
			return StoredBatchArtifacts{}, errors.Join(ErrRawArtifactConflict, ErrArtifactConflict)
		}
		objectArtifact.Replay = true
	} else {
		objectArtifact = s.newArtifact(write.ObjectPath, objectDigest)
		s.raw[write.ObjectPath] = memoryStoredArtifact{
			content:  append([]byte(nil), write.CompressedBody...),
			artifact: objectArtifact,
		}
		s.rawWrites++
	}

	if s.failNextManifest {
		s.failNextManifest = false
		return StoredBatchArtifacts{}, ErrArtifactUnavailable
	}
	manifestBytes, manifestDigest, err := CanonicalTelemetryManifest(write.Manifest, objectArtifact)
	if err != nil {
		return StoredBatchArtifacts{}, err
	}
	if s.manifestConflict {
		return StoredBatchArtifacts{}, errors.Join(ErrManifestArtifactConflict, ErrArtifactConflict)
	}
	manifest, exists := s.manifests[write.ManifestPath]
	manifestArtifact := manifest.artifact
	if exists {
		if !bytes.Equal(manifest.content, manifestBytes) {
			return StoredBatchArtifacts{}, ErrArtifactUnavailable
		}
		manifestArtifact.Replay = true
	} else {
		manifestArtifact = s.newArtifact(write.ManifestPath, manifestDigest)
		s.manifests[write.ManifestPath] = memoryStoredArtifact{
			content:  append([]byte(nil), manifestBytes...),
			artifact: manifestArtifact,
		}
		s.manifestWrites++
	}

	stored := StoredBatchArtifacts{Object: objectArtifact, Manifest: manifestArtifact}
	s.lastStored = stored
	return stored, nil
}

func (s *memoryArtifactStore) newArtifact(path string, digest ArtifactDigest) StoredArtifact {
	s.nextGeneration++
	return StoredArtifact{
		Path:           path,
		SHA256:         digest.SHA256,
		CRC32C:         digest.CRC32C,
		Size:           digest.Size,
		Generation:     s.nextGeneration,
		Metageneration: 1,
	}
}

func (s *memoryArtifactStore) rawContentAt(t *testing.T, path string) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.raw[path]
	if !ok {
		t.Fatalf("object %q not found", path)
	}
	return append([]byte(nil), object.content...)
}

func mustService(
	t *testing.T,
	receipts AdmissionStore,
	objects TelemetryArtifactStore,
	now func() time.Time,
) *Service {
	t.Helper()
	service, err := NewService(
		receipts,
		objects,
		fixedBatchIDGenerator(),
		now,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func assertReceiptArtifactLineage(t *testing.T, receipt Receipt, stored StoredBatchArtifacts) {
	t.Helper()
	if receipt.ObjectPath != stored.Object.Path ||
		receipt.ObjectSHA256 != stored.Object.SHA256 ||
		receipt.ObjectCRC32C != stored.Object.CRC32C ||
		receipt.ObjectSize != stored.Object.Size ||
		receipt.ObjectGeneration != stored.Object.Generation ||
		receipt.ObjectMetageneration != stored.Object.Metageneration ||
		receipt.ManifestPath != stored.Manifest.Path ||
		receipt.ManifestSHA256 != stored.Manifest.SHA256 ||
		receipt.ManifestCRC32C != stored.Manifest.CRC32C ||
		receipt.ManifestSize != stored.Manifest.Size ||
		receipt.ManifestGeneration != stored.Manifest.Generation ||
		receipt.ManifestMetageneration != stored.Manifest.Metageneration {
		t.Fatalf("receipt artifacts = %#v, stored = %#v", receipt, stored)
	}
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
