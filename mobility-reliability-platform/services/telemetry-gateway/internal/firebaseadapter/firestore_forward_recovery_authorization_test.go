package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreLoadsCurrentForwardRecoveryReadOnly(t *testing.T) {
	now := admissionTestNow()
	base, receipt := admissionReplayTransaction(t, now, ingest.ReceiptReserved)
	receipt.LeaseOwnerID = admissionTakeoverOwnerID
	receipt.LeaseOwnerKind = ingest.LeaseOwnerSweeper
	receipt.LeaseAcquiredAt = now
	receipt.LeaseHeartbeatAt = now
	receipt.LeaseExpiresAt = now.Add(ingest.DefaultRequestLeaseDuration)
	receipt.NextRecoveryAt = receipt.LeaseExpiresAt
	receipt.RecoveryAttemptCount = 1
	receipt.Revision = 2
	base.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)

	relations := ingest.CurrentForwardRecoverySnapshot{ReadTime: now.Add(time.Second)}
	tx := &fakeForwardRecoveryAuthorizationTransaction{
		admissionTransaction: base,
		snapshot:             relations,
	}
	store := admissionTestStore(now, admissionRunner(tx))

	got, err := store.LoadCurrentForwardRecovery(context.Background(), ingest.ForwardRecoveryAuthorizationQuery{
		TenantID:       admissionTenantID,
		ReservationKey: admissionReservationKey,
	})
	if err != nil {
		t.Fatalf("LoadCurrentForwardRecovery() error = %v", err)
	}
	if got.Receipt.ReceiptID != receipt.ReceiptID || got.Receipt.Revision != receipt.Revision ||
		got.Receipt.LeaseOwnerID != admissionTakeoverOwnerID || !got.ReadTime.Equal(relations.ReadTime) {
		t.Fatalf("snapshot = %#v, want authoritative receipt and relation read time", got)
	}
	if tx.relationLoads != 1 {
		t.Fatalf("relation loads = %d, want 1", tx.relationLoads)
	}
	if len(base.creates) != 0 || len(base.updates) != 0 {
		t.Fatalf("creates/updates = %d/%d, want read-only", len(base.creates), len(base.updates))
	}
	wantCalls := []string{
		"index:" + admissionIdempotencyPath(),
		"index:" + admissionClientBatchPath(),
		"receipt:" + admissionReceiptPath(),
	}
	if !reflect.DeepEqual(base.calls, wantCalls) {
		t.Fatalf("control-plane calls = %v, want %v", base.calls, wantCalls)
	}
}

func TestFirestoreAdmissionStoreRejectsInvalidForwardRecoveryQueryBeforeTransaction(t *testing.T) {
	calls := 0
	store := admissionTestStore(admissionTestNow(), func(
		_ context.Context,
		_ func(context.Context, admissionTransaction) error,
	) error {
		calls++
		return nil
	})

	_, err := store.LoadCurrentForwardRecovery(context.Background(), ingest.ForwardRecoveryAuthorizationQuery{
		TenantID:       "not-a-uuid",
		ReservationKey: admissionReservationKey,
	})
	if !errors.Is(err, ingest.ErrForwardRecoveryAuthorizationUnavailable) {
		t.Fatalf("LoadCurrentForwardRecovery() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("transaction calls = %d, want 0", calls)
	}
}

func TestFirestoreAdmissionStoreRequiresRecoveryRelationReader(t *testing.T) {
	now := admissionTestNow()
	base, _ := admissionReplayTransaction(t, now, ingest.ReceiptReserved)
	store := admissionTestStore(now, admissionRunner(base))

	_, err := store.LoadCurrentForwardRecovery(context.Background(), ingest.ForwardRecoveryAuthorizationQuery{
		TenantID:       admissionTenantID,
		ReservationKey: admissionReservationKey,
	})
	if !errors.Is(err, ingest.ErrForwardRecoveryAuthorizationUnavailable) {
		t.Fatalf("LoadCurrentForwardRecovery() error = %v", err)
	}
	if len(base.creates) != 0 || len(base.updates) != 0 {
		t.Fatalf("creates/updates = %d/%d, want zero", len(base.creates), len(base.updates))
	}
}

func TestFirestoreAdmissionStoreNormalizesForwardRecoveryRelationErrors(t *testing.T) {
	tests := []struct {
		name    string
		given   error
		want    error
		context func() context.Context
	}{
		{
			name:    "missing relation is bounded denial",
			given:   authorization.ErrSnapshotNotFound,
			want:    ingest.ErrForwardRecoveryUnauthorized,
			context: context.Background,
		},
		{
			name:    "provider detail is unavailable",
			given:   errors.New("secret/project/path credential failure"),
			want:    ingest.ErrForwardRecoveryAuthorizationUnavailable,
			context: context.Background,
		},
		{
			name:  "cancel is preserved",
			given: context.Canceled,
			want:  context.Canceled,
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := admissionTestNow()
			base, _ := admissionReplayTransaction(t, now, ingest.ReceiptReserved)
			tx := &fakeForwardRecoveryAuthorizationTransaction{
				admissionTransaction: base,
				err:                  test.given,
			}
			store := admissionTestStore(now, admissionRunner(tx))

			_, err := store.LoadCurrentForwardRecovery(test.context(), ingest.ForwardRecoveryAuthorizationQuery{
				TenantID:       admissionTenantID,
				ReservationKey: admissionReservationKey,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("LoadCurrentForwardRecovery() error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "credential") {
				t.Fatalf("error leaked provider detail: %v", err)
			}
		})
	}
}

func TestFirestoreAdmissionStoreRejectsIncoherentRecoveryReadTime(t *testing.T) {
	now := admissionTestNow()
	base, _ := admissionReplayTransaction(t, now, ingest.ReceiptReserved)
	base.receiptReadTime = now
	tx := &fakeForwardRecoveryAuthorizationTransaction{
		admissionTransaction: base,
		snapshot: ingest.CurrentForwardRecoverySnapshot{
			ReadTime: now.Add(maxAdmissionClockSkew + time.Nanosecond),
		},
	}
	store := admissionTestStore(now, admissionRunner(tx))

	_, err := store.LoadCurrentForwardRecovery(context.Background(), ingest.ForwardRecoveryAuthorizationQuery{
		TenantID:       admissionTenantID,
		ReservationKey: admissionReservationKey,
	})
	if !errors.Is(err, ingest.ErrForwardRecoveryAuthorizationUnavailable) {
		t.Fatalf("LoadCurrentForwardRecovery() error = %v", err)
	}
}

func TestCurrentForwardRecoveryPathsAreExactAndPseudonymous(t *testing.T) {
	receipt := admissionTestReceiptDTO(admissionTestReceipt(
		admissionTestReservation(admissionTestNow()),
		ingest.ReceiptReserved,
	))
	primary, err := currentForwardRecoveryPrimaryPaths(receipt)
	if err != nil {
		t.Fatalf("currentForwardRecoveryPrimaryPaths() error = %v", err)
	}
	wantPrimary := []string{
		"tenants/" + admissionTenantID,
		"tenants/" + admissionTenantID + "/appInstallations/" + admissionInstallationID,
		"tenants/" + admissionTenantID + "/trips/" + admissionTripID,
		"tenants/" + admissionTenantID + "/consentRevisions/" + admissionConsentRevisionID,
	}
	if !reflect.DeepEqual(primary, wantPrimary) {
		t.Fatalf("primary paths = %v, want %v", primary, wantPrimary)
	}

	related, err := currentForwardRecoveryRelatedPaths(
		admissionTenantID,
		admissionUID,
		admissionAssignmentID,
		admissionPersonID,
	)
	if err != nil {
		t.Fatalf("currentForwardRecoveryRelatedPaths() error = %v", err)
	}
	wantStateID := authorization.ConsentStateDocumentID(
		admissionPersonID,
		authorization.PreciseLocationPurpose,
	)
	wantRelated := []string{
		"tenants/" + admissionTenantID + "/memberships/" + admissionUID,
		"tenants/" + admissionTenantID + "/deviceAssignments/" + admissionAssignmentID,
		"tenants/" + admissionTenantID + "/consentStates/" + wantStateID,
	}
	if !reflect.DeepEqual(related, wantRelated) {
		t.Fatalf("related paths = %v, want %v", related, wantRelated)
	}
	if strings.Contains(related[2], admissionPersonID) {
		t.Fatalf("consent-state path exposes person id: %s", related[2])
	}
}

type fakeForwardRecoveryAuthorizationTransaction struct {
	admissionTransaction
	snapshot      ingest.CurrentForwardRecoverySnapshot
	err           error
	relationLoads int
}

func (tx *fakeForwardRecoveryAuthorizationTransaction) LoadCurrentForwardRecoveryRelations(
	_ context.Context,
	_ firestoreIngestReceipt,
) (ingest.CurrentForwardRecoverySnapshot, error) {
	tx.relationLoads++
	return tx.snapshot, tx.err
}
