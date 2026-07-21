package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	admissionTenantID          = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a1"
	admissionInstallationID    = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a2"
	admissionTripID            = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a3"
	admissionConsentRevisionID = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a4"
	admissionAssignmentID      = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a5"
	admissionPersonID          = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a6"
	admissionDeviceID          = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a7"
	admissionClientSessionID   = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a8"
	admissionReceiptID         = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a9"
	admissionUID               = "firebase-user"
	admissionAppID             = "firebase-app"
	admissionReservationKey    = "f2007d291f0564dcf0b1bc0de777b10829405bbbf1fb76d0528cbe796dead994"
	admissionClientBatchKey    = "b020d4b1daf3c31758024b62101b74e852095a1d135644d4d6012cf2da7a5eda"
)

func TestFirestoreAdmissionStoreAuthorizesBeforeIndexReadsAndCreatesAtomicTriplet(t *testing.T) {
	now := admissionTestNow()
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
	store := admissionTestStore(now, admissionRunner(tx))

	receipt, status, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(now),
		admissionTestReservation(now),
	)
	if err != nil {
		t.Fatalf("AuthorizeAndReserve() error = %v", err)
	}
	if status != ingest.ReservationCreated {
		t.Fatalf("AuthorizeAndReserve() status = %q, want %q", status, ingest.ReservationCreated)
	}
	if len(tx.calls) < 3 || tx.calls[0] != "authorization" {
		t.Fatalf("transaction call order = %#v; authorization must be first", tx.calls)
	}
	for _, call := range tx.calls[1:] {
		if call == "authorization" {
			t.Fatalf("authorization was evaluated more than once in one callback: %#v", tx.calls)
		}
		if strings.HasPrefix(call, "index:") {
			break
		}
	}

	wantPaths := []string{
		admissionIdempotencyPath(),
		admissionClientBatchPath(),
		admissionReceiptPath(),
	}
	if got := tx.createdPaths(); !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("created paths = %#v, want %#v", got, wantPaths)
	}
	if len(tx.updates) != 0 {
		t.Fatalf("new admission performed %d updates, want zero", len(tx.updates))
	}
	assertAdmissionReceiptMatchesReservation(t, receipt, admissionTestReservation(now))
	if receipt.State != ingest.ReceiptReserved || receipt.Revision != 1 {
		t.Fatalf("created receipt state/revision = %q/%d, want reserved/1", receipt.State, receipt.Revision)
	}

	createdReceipt, ok := tx.createValue(admissionReceiptPath()).(firestoreIngestReceipt)
	if !ok {
		t.Fatalf("receipt create value type = %T, want firestoreIngestReceipt", tx.createValue(admissionReceiptPath()))
	}
	if !reflect.DeepEqual(createdReceipt.toDomain(), receipt) {
		t.Fatalf("persisted receipt = %#v, returned receipt = %#v", createdReceipt.toDomain(), receipt)
	}
}

func TestFirestoreAdmissionStoreDenialsAndProviderFailuresDoNotTouchIndexesOrWrites(t *testing.T) {
	now := admissionTestNow()
	providerFailure := errors.New("provider detail that must not escape")
	tests := []struct {
		name       string
		configure  func(*fakeAdmissionTransaction)
		want       error
		wantPublic string
	}{
		{
			name: "authorization denied",
			configure: func(tx *fakeAdmissionTransaction) {
				tx.snapshot.Tenant.Status = "suspended"
			},
			want: ingest.ErrBatchUnauthorized,
		},
		{
			name: "snapshot provider unavailable",
			configure: func(tx *fakeAdmissionTransaction) {
				tx.authorizationErr = providerFailure
			},
			want:       ingest.ErrAdmissionUnavailable,
			wantPublic: providerFailure.Error(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
			test.configure(tx)
			store := admissionTestStore(now, admissionRunner(tx))

			_, _, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				admissionTestReservation(now),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("AuthorizeAndReserve() error = %v, want %v", err, test.want)
			}
			if len(tx.calls) != 1 || tx.calls[0] != "authorization" {
				t.Fatalf("denied transaction calls = %#v, want authorization only", tx.calls)
			}
			if len(tx.creates) != 0 || len(tx.updates) != 0 {
				t.Fatalf("denied transaction creates/updates = %d/%d, want 0/0", len(tx.creates), len(tx.updates))
			}
			if test.wantPublic != "" && err.Error() == test.wantPublic {
				t.Fatal("provider-specific error escaped the adapter")
			}
		})
	}
}

func TestFirestoreAdmissionStoreRejectsScopeReservationMismatchBeforeTransaction(t *testing.T) {
	now := admissionTestNow()
	scope := admissionTestScope(now)
	tests := []struct {
		name   string
		mutate func(*ingest.Reservation)
	}{
		{name: "tenant", mutate: func(value *ingest.Reservation) { value.TenantID = admissionReceiptID }},
		{name: "device", mutate: func(value *ingest.Reservation) { value.DeviceID = admissionReceiptID }},
		{name: "trip", mutate: func(value *ingest.Reservation) { value.TripID = admissionReceiptID }},
		{name: "installation", mutate: func(value *ingest.Reservation) { value.InstallationID = admissionReceiptID }},
		{name: "consent revision", mutate: func(value *ingest.Reservation) { value.ConsentRevisionID = admissionReceiptID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reservation := admissionTestReservation(now)
			test.mutate(&reservation)
			transactionCalls := 0
			store := admissionTestStore(now, func(
				context.Context,
				func(context.Context, admissionTransaction) error,
			) error {
				transactionCalls++
				return nil
			})

			_, _, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				scope,
				reservation,
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("AuthorizeAndReserve() error = %v, want admission unavailable", err)
			}
			if transactionCalls != 0 {
				t.Fatalf("transaction calls = %d, want zero", transactionCalls)
			}
		})
	}
}

func TestFirestoreAdmissionStoreMapsReplayReceiptStates(t *testing.T) {
	now := admissionTestNow()
	tests := []struct {
		name       string
		state      ingest.ReceiptState
		wantStatus ingest.ReservationStatus
	}{
		{name: "reserved remains pending", state: ingest.ReceiptReserved, wantStatus: ingest.ReservationReplayPending},
		{name: "stored is complete", state: ingest.ReceiptStored, wantStatus: ingest.ReservationReplayComplete},
		{name: "rejected remains rejected", state: ingest.ReceiptRejected, wantStatus: ingest.ReservationReplayRejected},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, expected := admissionReplayTransaction(t, now, test.state)
			store := admissionTestStore(now, admissionRunner(tx))

			got, status, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				admissionTestReservation(now),
			)
			if err != nil {
				t.Fatalf("AuthorizeAndReserve() error = %v", err)
			}
			if status != test.wantStatus {
				t.Fatalf("AuthorizeAndReserve() status = %q, want %q", status, test.wantStatus)
			}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("AuthorizeAndReserve() receipt = %#v, want existing %#v", got, expected)
			}
			if len(tx.creates) != 0 || len(tx.updates) != 0 {
				t.Fatalf("replay creates/updates = %d/%d, want 0/0", len(tx.creates), len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreDistinguishesIdempotencyAndClientBatchConflicts(t *testing.T) {
	now := admissionTestNow()
	reservation := admissionTestReservation(now)
	tests := []struct {
		name       string
		configure  func(*fakeAdmissionTransaction)
		wantStatus ingest.ReservationStatus
	}{
		{
			name: "idempotency key has different body",
			configure: func(tx *fakeAdmissionTransaction) {
				bodyHash := strings.Repeat("f", 64)
				index := admissionTestIndex(reservation, admissionReceiptID, bodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				receipt.BodyHash = bodyHash
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
			wantStatus: ingest.ReservationConflict,
		},
		{
			name: "client batch belongs to different reservation",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				index.InstallationID = admissionReceiptID
				index.ReservationKey = ingest.DeriveReservationKey(
					index.PayloadSchemaVersion,
					index.TenantID,
					index.InstallationID,
					index.ClientBatchID,
				)
				tx.indexes[admissionClientBatchPath()] = index
				tx.indexes[idempotencyDocumentPath(admissionTenantID, index.ReservationKey)] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				receipt.ReservationKey = index.ReservationKey
				receipt.InstallationID = index.InstallationID
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
			wantStatus: ingest.ReservationClientBatchConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
			test.configure(tx)
			store := admissionTestStore(now, admissionRunner(tx))

			_, status, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				reservation,
			)
			if err != nil {
				t.Fatalf("AuthorizeAndReserve() error = %v", err)
			}
			if status != test.wantStatus {
				t.Fatalf("AuthorizeAndReserve() status = %q, want %q", status, test.wantStatus)
			}
			if len(tx.creates) != 0 || len(tx.updates) != 0 {
				t.Fatalf("conflict creates/updates = %d/%d, want 0/0", len(tx.creates), len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreTreatsCorruptAdmissionLinkageAsUnavailable(t *testing.T) {
	now := admissionTestNow()
	reservation := admissionTestReservation(now)
	tests := []struct {
		name      string
		configure func(*fakeAdmissionTransaction)
	}{
		{
			name: "matching idempotency index without client batch index",
			configure: func(tx *fakeAdmissionTransaction) {
				tx.indexes[admissionIdempotencyPath()] = admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
			},
		},
		{
			name: "matching client batch index without idempotency index",
			configure: func(tx *fakeAdmissionTransaction) {
				tx.indexes[admissionClientBatchPath()] = admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
			},
		},
		{
			name: "different client batch reservation has no linked idempotency index",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				index.InstallationID = admissionReceiptID
				index.ReservationKey = ingest.DeriveReservationKey(
					index.PayloadSchemaVersion,
					index.TenantID,
					index.InstallationID,
					index.ClientBatchID,
				)
				tx.indexes[admissionClientBatchPath()] = index
			},
		},
		{
			name: "both indexes but receipt missing",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
			},
		},
		{
			name: "index receipt linkage mismatch",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				other := index
				other.ReceiptID = "018f1f4e-2f5e-7d31-8c77-43b50f4c91aa"
				tx.indexes[admissionClientBatchPath()] = other
			},
		},
		{
			name: "receipt has unknown state",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptState("future-state"))
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
		{
			name: "persisted lineage has noncanonical retention",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				index.ExpiresAt = reservation.CreatedAt.Add(31 * 24 * time.Hour)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				receipt.ExpiresAt = index.ExpiresAt
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
			test.configure(tx)
			store := admissionTestStore(now, admissionRunner(tx))

			_, _, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				reservation,
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("AuthorizeAndReserve() error = %v, want generic admission unavailable", err)
			}
			if len(tx.creates) != 0 || len(tx.updates) != 0 {
				t.Fatalf("corrupt transaction creates/updates = %d/%d, want 0/0", len(tx.creates), len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreResetsOuterResultAcrossTransactionRetries(t *testing.T) {
	now := admissionTestNow()
	first, _ := admissionReplayTransaction(t, now, ingest.ReceiptStored)
	second := newFakeAdmissionTransaction(admissionTestSnapshot(now.Add(time.Second)))
	authorizationCalls := 0
	runner := runAdmissionTransaction(func(ctx context.Context, callback func(context.Context, admissionTransaction) error) error {
		if err := callback(ctx, first); err != nil {
			return err
		}
		authorizationCalls++
		if err := callback(ctx, second); err != nil {
			return err
		}
		authorizationCalls++
		return nil
	})
	store := admissionTestStore(now, runner)

	receipt, status, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(now),
		admissionTestReservation(now),
	)
	if err != nil {
		t.Fatalf("AuthorizeAndReserve() error = %v", err)
	}
	if authorizationCalls != 2 || first.authorizationLoads != 1 || second.authorizationLoads != 1 {
		t.Fatalf("authorization evaluations = runner:%d first:%d second:%d, want 2/1/1", authorizationCalls, first.authorizationLoads, second.authorizationLoads)
	}
	if status != ingest.ReservationCreated {
		t.Fatalf("final status = %q, want created from final callback", status)
	}
	if receipt.State != ingest.ReceiptReserved {
		t.Fatalf("final receipt state = %q, want reserved from final callback", receipt.State)
	}
	if len(first.creates) != 0 || len(second.creates) != 3 {
		t.Fatalf("retry create attempts = first:%d second:%d, want 0/3", len(first.creates), len(second.creates))
	}
}

func TestFirestoreAdmissionStoreRejectsExpiredPendingReplay(t *testing.T) {
	now := admissionTestNow()
	proposal := admissionTestReservation(now)
	persisted := admissionTestReservation(now.Add(-ingest.ReceiptRetention))
	index := admissionTestIndex(persisted, persisted.ReceiptID, persisted.BodyHash)
	receipt := admissionTestReceipt(persisted, ingest.ReceiptReserved)
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
	tx.indexes[admissionIdempotencyPath()] = index
	tx.indexes[admissionClientBatchPath()] = index
	tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
	store := admissionTestStore(now, admissionRunner(tx))

	_, _, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(now),
		proposal,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("AuthorizeAndReserve() error = %v, want expired pending unavailable", err)
	}
	if len(tx.creates) != 0 || len(tx.updates) != 0 {
		t.Fatalf("expired pending creates/updates = %d/%d, want zero", len(tx.creates), len(tx.updates))
	}
}

func TestFirestoreAdmissionStoreReauthorizesOnRetryAndStopsAfterRevocation(t *testing.T) {
	now := admissionTestNow()
	first := newFakeAdmissionTransaction(admissionTestSnapshot(now))
	secondSnapshot := admissionTestSnapshot(now.Add(time.Second))
	secondSnapshot.ConsentState.Status = "withdrawn"
	second := newFakeAdmissionTransaction(secondSnapshot)
	runner := runAdmissionTransaction(func(ctx context.Context, callback func(context.Context, admissionTransaction) error) error {
		if err := callback(ctx, first); err != nil {
			return err
		}
		return callback(ctx, second)
	})
	store := admissionTestStore(now, runner)

	_, _, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(now),
		admissionTestReservation(now),
	)
	if !errors.Is(err, ingest.ErrBatchUnauthorized) {
		t.Fatalf("AuthorizeAndReserve() error = %v, want revoked authorization denial", err)
	}
	if first.authorizationLoads != 1 || second.authorizationLoads != 1 {
		t.Fatalf("authorization loads = first:%d second:%d, want 1/1", first.authorizationLoads, second.authorizationLoads)
	}
	if len(second.calls) != 1 || second.calls[0] != "authorization" {
		t.Fatalf("revoked retry calls = %#v, want authorization only", second.calls)
	}
	if len(second.creates) != 0 || len(second.updates) != 0 {
		t.Fatalf("revoked retry creates/updates = %d/%d, want 0/0", len(second.creates), len(second.updates))
	}
}

func TestFirestoreAdmissionStoreMarkStoredAndRejectedPreserveLinkageAndAdvanceRevision(t *testing.T) {
	createdAt := admissionTestNow()
	updatedAt := createdAt.Add(17 * time.Minute)
	reservation := admissionTestReservation(createdAt)
	tests := []struct {
		name          string
		invoke        func(*FirestoreAdmissionStore) (ingest.Receipt, error)
		wantState     ingest.ReceiptState
		wantPath      string
		wantObject    string
		wantSamples   int
		wantRejection string
	}{
		{
			name: "mark stored",
			invoke: func(store *FirestoreAdmissionStore) (ingest.Receipt, error) {
				return store.MarkStored(context.Background(), admissionTenantID, admissionReservationKey, admissionObjectPath(), 42, updatedAt)
			},
			wantState:   ingest.ReceiptStored,
			wantPath:    admissionReceiptPath(),
			wantObject:  admissionObjectPath(),
			wantSamples: 42,
		},
		{
			name: "mark rejected",
			invoke: func(store *FirestoreAdmissionStore) (ingest.Receipt, error) {
				return store.MarkRejected(context.Background(), admissionTenantID, admissionReservationKey, "object_conflict", updatedAt)
			},
			wantState:     ingest.ReceiptRejected,
			wantPath:      admissionReceiptPath(),
			wantRejection: "object_conflict",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(createdAt))
			index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
			tx.indexes[admissionIdempotencyPath()] = index
			tx.indexes[admissionClientBatchPath()] = index
			tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(admissionTestReceipt(reservation, ingest.ReceiptReserved))
			store := admissionTestStore(createdAt, admissionRunner(tx))

			got, err := test.invoke(store)
			if err != nil {
				t.Fatalf("finalize error = %v", err)
			}
			if got.State != test.wantState || got.Revision != 2 || !got.UpdatedAt.Equal(updatedAt) {
				t.Fatalf("final receipt state/revision/time = %q/%d/%s, want %q/2/%s", got.State, got.Revision, got.UpdatedAt, test.wantState, updatedAt)
			}
			if got.ObjectPath != test.wantObject || got.SampleCount != test.wantSamples || got.RejectionCode != test.wantRejection {
				t.Fatalf("final receipt payload = object:%q samples:%d rejection:%q", got.ObjectPath, got.SampleCount, got.RejectionCode)
			}
			if len(tx.creates) != 0 || len(tx.updates) != 1 || tx.updates[0].path != test.wantPath {
				t.Fatalf("finalizer creates/updates = %d/%#v", len(tx.creates), tx.updates)
			}
			assertFirestoreUpdates(t, tx.updates[0].updates, got)
		})
	}
}

func TestFirestoreAdmissionStoreFinalizersRejectBrokenLinkageWithoutUpdate(t *testing.T) {
	now := admissionTestNow()
	reservation := admissionTestReservation(now)
	tests := []struct {
		name      string
		configure func(*fakeAdmissionTransaction)
	}{
		{name: "missing reservation index"},
		{
			name: "missing client batch index",
			configure: func(tx *fakeAdmissionTransaction) {
				tx.indexes[admissionIdempotencyPath()] = admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
			},
		},
		{
			name: "missing receipt",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
			},
		},
		{
			name: "receipt reservation mismatch",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				receipt.ReservationKey = "different-reservation"
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
		{
			name: "reserved receipt has stale object fields",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				receipt.ObjectPath = admissionObjectPath()
				receipt.SampleCount = 1
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
			if test.configure != nil {
				test.configure(tx)
			}
			store := admissionTestStore(now, admissionRunner(tx))

			_, err := store.MarkStored(context.Background(), admissionTenantID, admissionReservationKey, admissionObjectPath(), 1, now.Add(time.Minute))
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("MarkStored() error = %v, want generic admission unavailable", err)
			}
			if len(tx.updates) != 0 || len(tx.creates) != 0 {
				t.Fatalf("broken linkage creates/updates = %d/%d, want 0/0", len(tx.creates), len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreFinalizerTerminalReplayIgnoresOlderCallerTime(t *testing.T) {
	now := admissionTestNow()
	reservation := admissionTestReservation(now)
	tests := []struct {
		name   string
		state  ingest.ReceiptState
		invoke func(*FirestoreAdmissionStore) (ingest.Receipt, error)
	}{
		{
			name:  "stored",
			state: ingest.ReceiptStored,
			invoke: func(store *FirestoreAdmissionStore) (ingest.Receipt, error) {
				return store.MarkStored(
					context.Background(),
					admissionTenantID,
					admissionReservationKey,
					admissionObjectPath(),
					42,
					now.Add(time.Minute),
				)
			},
		},
		{
			name:  "rejected",
			state: ingest.ReceiptRejected,
			invoke: func(store *FirestoreAdmissionStore) (ingest.Receipt, error) {
				return store.MarkRejected(
					context.Background(),
					admissionTenantID,
					admissionReservationKey,
					"object_conflict",
					now.Add(time.Minute),
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
			index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
			tx.indexes[admissionIdempotencyPath()] = index
			tx.indexes[admissionClientBatchPath()] = index
			receipt := admissionTestReceipt(reservation, test.state)
			receipt.Revision = 2
			receipt.UpdatedAt = now.Add(2 * time.Minute)
			tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			store := admissionTestStore(now, admissionRunner(tx))

			got, err := test.invoke(store)
			if err != nil {
				t.Fatalf("terminal replay error = %v", err)
			}
			if !got.UpdatedAt.Equal(receipt.UpdatedAt) || got.Revision != receipt.Revision {
				t.Fatalf("terminal replay receipt = %#v, want existing %#v", got, receipt)
			}
			if len(tx.updates) != 0 {
				t.Fatalf("terminal replay updates = %d, want zero", len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreRejectsExpiredReservedFinalizer(t *testing.T) {
	now := admissionTestNow()
	reservation := admissionTestReservation(now.Add(-ingest.ReceiptRetention))
	index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
	receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
	receiptDTO := admissionTestReceiptDTO(receipt)
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
	tx.indexes[admissionIdempotencyPath()] = index
	tx.indexes[admissionClientBatchPath()] = index
	tx.receipts[admissionReceiptPath()] = receiptDTO
	store := admissionTestStore(now, admissionRunner(tx))

	_, err := store.MarkStored(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		expectedObjectPath(receiptDTO),
		1,
		now,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("MarkStored() error = %v, want expired reserved unavailable", err)
	}
	if len(tx.updates) != 0 {
		t.Fatalf("expired reserved updates = %d, want zero", len(tx.updates))
	}
}

type fakeAdmissionTransaction struct {
	snapshot           authorization.Snapshot
	authorizationErr   error
	indexes            map[string]firestoreIngestIndex
	receipts           map[string]firestoreIngestReceipt
	calls              []string
	creates            []fakeAdmissionCreate
	updates            []fakeAdmissionUpdate
	authorizationLoads int
}

type fakeAdmissionCreate struct {
	path  string
	value any
}

type fakeAdmissionUpdate struct {
	path    string
	updates []firestore.Update
}

func newFakeAdmissionTransaction(snapshot authorization.Snapshot) *fakeAdmissionTransaction {
	return &fakeAdmissionTransaction{
		snapshot: snapshot,
		indexes:  make(map[string]firestoreIngestIndex),
		receipts: make(map[string]firestoreIngestReceipt),
	}
}

func (tx *fakeAdmissionTransaction) LoadAuthorization(
	_ context.Context,
	_ ingest.Principal,
	_ ingest.BatchScope,
) (authorization.Snapshot, error) {
	tx.calls = append(tx.calls, "authorization")
	tx.authorizationLoads++
	return tx.snapshot, tx.authorizationErr
}

func (tx *fakeAdmissionTransaction) ReadIndex(_ context.Context, path string) (firestoreIngestIndex, bool, error) {
	tx.calls = append(tx.calls, "index:"+path)
	value, exists := tx.indexes[path]
	return value, exists, nil
}

func (tx *fakeAdmissionTransaction) ReadReceipt(_ context.Context, path string) (firestoreIngestReceipt, bool, error) {
	tx.calls = append(tx.calls, "receipt:"+path)
	value, exists := tx.receipts[path]
	return value, exists, nil
}

func (tx *fakeAdmissionTransaction) Create(_ context.Context, path string, value any) error {
	tx.calls = append(tx.calls, "create:"+path)
	tx.creates = append(tx.creates, fakeAdmissionCreate{path: path, value: value})
	return nil
}

func (tx *fakeAdmissionTransaction) Update(_ context.Context, path string, updates []firestore.Update) error {
	tx.calls = append(tx.calls, "update:"+path)
	tx.updates = append(tx.updates, fakeAdmissionUpdate{path: path, updates: append([]firestore.Update(nil), updates...)})
	return nil
}

func (tx *fakeAdmissionTransaction) createdPaths() []string {
	paths := make([]string, len(tx.creates))
	for index, create := range tx.creates {
		paths[index] = create.path
	}
	return paths
}

func (tx *fakeAdmissionTransaction) createValue(path string) any {
	for _, create := range tx.creates {
		if create.path == path {
			return create.value
		}
	}
	return nil
}

func admissionRunner(tx admissionTransaction) runAdmissionTransaction {
	return func(ctx context.Context, callback func(context.Context, admissionTransaction) error) error {
		return callback(ctx, tx)
	}
}

func admissionTestStore(now time.Time, runner runAdmissionTransaction) *FirestoreAdmissionStore {
	return &FirestoreAdmissionStore{
		runTransaction: runner,
		now:            func() time.Time { return now },
	}
}

func admissionTestNow() time.Time {
	return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
}

func admissionTestPrincipal() ingest.Principal {
	return ingest.Principal{FirebaseUID: admissionUID, AppID: admissionAppID}
}

func admissionTestScope(now time.Time) ingest.BatchScope {
	return ingest.BatchScope{
		TenantID:          admissionTenantID,
		DeviceID:          admissionDeviceID,
		TripID:            admissionTripID,
		ClientSessionID:   admissionClientSessionID,
		InstallationID:    admissionInstallationID,
		ConsentRevisionID: admissionConsentRevisionID,
		FirstCapturedAt:   now.Add(-5 * time.Minute),
		LastCapturedAt:    now.Add(-time.Minute),
	}
}

func admissionTestReservation(now time.Time) ingest.Reservation {
	return ingest.Reservation{
		ReservationKey:       admissionReservationKey,
		ClientBatchKey:       admissionClientBatchKey,
		ReceiptID:            admissionReceiptID,
		TenantID:             admissionTenantID,
		BatchID:              admissionReceiptID,
		DeviceID:             admissionDeviceID,
		TripID:               admissionTripID,
		InstallationID:       admissionInstallationID,
		ConsentRevisionID:    admissionConsentRevisionID,
		ClientBatchID:        "018f1f4e-2f5e-7d31-8c77-43b50f4c91ab",
		PayloadSchemaVersion: telemetry.SchemaVersionV2,
		BodyHash:             "7d6db7b45493315b87f4333993082edab4fcc2db365001f91dfc4a57d23f40f4",
		CreatedAt:            now,
		ExpiresAt:            now.Add(ingest.ReceiptRetention),
	}
}

func admissionTestSnapshot(now time.Time) authorization.Snapshot {
	grantedAt := now.Add(-24 * time.Hour)
	return authorization.Snapshot{
		Tenant: authorization.Tenant{TenantID: admissionTenantID, Status: "active"},
		Membership: authorization.Membership{
			TenantID: admissionTenantID, FirebaseUID: admissionUID, PersonID: admissionPersonID,
			Roles: []string{"beneficiary"}, Status: "active", ValidFrom: grantedAt,
		},
		Installation: authorization.Installation{
			TenantID: admissionTenantID, InstallationID: admissionInstallationID,
			FirebaseUID: admissionUID, AppID: admissionAppID, Status: "active",
			SchemaVersion: 1, Revision: 1, RegisteredAt: grantedAt,
			CreatedAt: grantedAt, UpdatedAt: grantedAt,
		},
		Trip: authorization.Trip{
			TenantID: admissionTenantID, TripID: admissionTripID, DeviceID: admissionDeviceID,
			PersonID: admissionPersonID, DeviceAssignmentID: admissionAssignmentID,
			InstallationID: admissionInstallationID, ClientSessionID: admissionClientSessionID,
			ConsentRevisionID: admissionConsentRevisionID, StartedAt: now.Add(-time.Hour),
			IngestExpiresAt: now.Add(time.Hour), CaptureMode: "background", Status: "recording",
		},
		Assignment: authorization.DeviceAssignment{
			TenantID: admissionTenantID, AssignmentID: admissionAssignmentID,
			DeviceID: admissionDeviceID, PersonID: admissionPersonID,
			AssignmentType: "primary_user", Status: "active", ValidFrom: grantedAt,
		},
		Consent: authorization.ConsentRevision{
			TenantID: admissionTenantID, ConsentRevisionID: admissionConsentRevisionID,
			PersonID: admissionPersonID, PurposeCode: authorization.PreciseLocationPurpose,
			Status: "granted", GrantedAt: &grantedAt,
		},
		ConsentState: authorization.ConsentState{
			TenantID: admissionTenantID, PersonID: admissionPersonID,
			PurposeCode:       authorization.PreciseLocationPurpose,
			CurrentRevisionID: admissionConsentRevisionID, Status: "granted", EffectiveAt: grantedAt,
		},
	}
}

func admissionTestIndex(reservation ingest.Reservation, receiptID, bodyHash string) firestoreIngestIndex {
	return firestoreIngestIndex{
		TenantID:             reservation.TenantID,
		ReservationKey:       reservation.ReservationKey,
		ClientBatchKey:       reservation.ClientBatchKey,
		ReceiptID:            receiptID,
		BatchID:              reservation.BatchID,
		InstallationID:       reservation.InstallationID,
		ClientBatchID:        reservation.ClientBatchID,
		PayloadSchemaVersion: reservation.PayloadSchemaVersion,
		BodyHash:             bodyHash,
		CreatedAt:            reservation.CreatedAt,
		ExpiresAt:            reservation.ExpiresAt,
	}
}

func admissionTestReceipt(reservation ingest.Reservation, state ingest.ReceiptState) ingest.Receipt {
	receipt := ingest.Receipt{
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
		State:                state,
		Revision:             1,
		CreatedAt:            reservation.CreatedAt,
		UpdatedAt:            reservation.CreatedAt,
		ExpiresAt:            reservation.ExpiresAt,
	}
	if state == ingest.ReceiptStored || state == "queued" || state == "projected" || state == "deleting" || state == "deleted" {
		receipt.ObjectPath = admissionObjectPath()
		receipt.SampleCount = 42
	}
	if state == ingest.ReceiptRejected {
		receipt.RejectionCode = "object_conflict"
	}
	return receipt
}

func admissionTestReceiptDTO(receipt ingest.Receipt) firestoreIngestReceipt {
	return firestoreIngestReceipt{
		ReservationKey:       receipt.ReservationKey,
		ClientBatchKey:       receipt.ClientBatchKey,
		ReceiptID:            receipt.ReceiptID,
		TenantID:             receipt.TenantID,
		BatchID:              receipt.BatchID,
		DeviceID:             receipt.DeviceID,
		TripID:               receipt.TripID,
		InstallationID:       receipt.InstallationID,
		ConsentRevisionID:    receipt.ConsentRevisionID,
		ClientBatchID:        receipt.ClientBatchID,
		PayloadSchemaVersion: receipt.PayloadSchemaVersion,
		BodyHash:             receipt.BodyHash,
		ObjectPath:           receipt.ObjectPath,
		SampleCount:          receipt.SampleCount,
		State:                receipt.State,
		RejectionCode:        receipt.RejectionCode,
		Revision:             receipt.Revision,
		CreatedAt:            receipt.CreatedAt,
		UpdatedAt:            receipt.UpdatedAt,
		ExpiresAt:            receipt.ExpiresAt,
	}
}

func admissionReplayTransaction(
	t *testing.T,
	now time.Time,
	state ingest.ReceiptState,
) (*fakeAdmissionTransaction, ingest.Receipt) {
	t.Helper()
	reservation := admissionTestReservation(now)
	index := admissionTestIndex(reservation, reservation.ReceiptID, reservation.BodyHash)
	receipt := admissionTestReceipt(reservation, state)
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
	tx.indexes[admissionIdempotencyPath()] = index
	tx.indexes[admissionClientBatchPath()] = index
	tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
	return tx, receipt
}

func admissionIdempotencyPath() string {
	return "tenants/" + admissionTenantID + "/ingestIdempotency/" + admissionReservationKey
}

func admissionClientBatchPath() string {
	return "tenants/" + admissionTenantID + "/ingestClientBatches/" + admissionClientBatchKey
}

func admissionReceiptPath() string {
	return "tenants/" + admissionTenantID + "/ingestReceipts/" + admissionReceiptID
}

func admissionObjectPath() string {
	return "telemetry/v2/tenants/" + admissionTenantID + "/devices/" + admissionDeviceID + "/trips/" + admissionTripID + "/year=2026/month=07/day=21/" + admissionReceiptID + ".json.gz"
}

func assertAdmissionReceiptMatchesReservation(t *testing.T, got ingest.Receipt, reservation ingest.Reservation) {
	t.Helper()
	if got.ReservationKey != reservation.ReservationKey ||
		got.ClientBatchKey != reservation.ClientBatchKey ||
		got.ReceiptID != reservation.ReceiptID ||
		got.TenantID != reservation.TenantID ||
		got.BatchID != reservation.BatchID ||
		got.DeviceID != reservation.DeviceID ||
		got.TripID != reservation.TripID ||
		got.InstallationID != reservation.InstallationID ||
		got.ConsentRevisionID != reservation.ConsentRevisionID ||
		got.ClientBatchID != reservation.ClientBatchID ||
		got.PayloadSchemaVersion != reservation.PayloadSchemaVersion ||
		got.BodyHash != reservation.BodyHash ||
		!got.CreatedAt.Equal(reservation.CreatedAt) ||
		!got.ExpiresAt.Equal(reservation.ExpiresAt) {
		t.Fatalf("receipt lineage = %#v, want reservation %#v", got, reservation)
	}
}

func assertFirestoreUpdates(t *testing.T, updates []firestore.Update, receipt ingest.Receipt) {
	t.Helper()
	got := make(map[string]any, len(updates))
	for _, update := range updates {
		got[update.Path] = update.Value
	}
	want := map[string]any{
		"status":     string(receipt.State),
		"revision":   receipt.Revision,
		"updated_at": receipt.UpdatedAt,
	}
	if receipt.State == ingest.ReceiptStored {
		want["object_path"] = receipt.ObjectPath
		want["sample_count"] = receipt.SampleCount
	}
	if receipt.State == ingest.ReceiptRejected {
		want["rejection_code"] = receipt.RejectionCode
	}
	for path, value := range want {
		if !reflect.DeepEqual(got[path], value) {
			t.Fatalf("Firestore update %q = %#v, want %#v; all updates %#v", path, got[path], value, got)
		}
	}
}
