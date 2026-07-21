package firebaseadapter

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	admissionLeaseOwnerID      = "018f1f4e-2f5e-7d31-8c77-43b50f4c91aa"
	admissionTakeoverOwnerID   = "018f1f4e-2f5e-7d31-8c77-43b50f4c91ab"
	admissionUID               = "firebase-user"
	admissionAppID             = "firebase-app"
	admissionReservationKey    = "f2007d291f0564dcf0b1bc0de777b10829405bbbf1fb76d0528cbe796dead994"
	admissionClientBatchKey    = "b020d4b1daf3c31758024b62101b74e852095a1d135644d4d6012cf2da7a5eda"
)

func TestFirestoreAdmissionStoreAuthorizesBeforeIndexReadsAndCreatesAtomicTriplet(t *testing.T) {
	now := admissionTestNow()
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
	store := admissionTestStore(now, admissionRunner(tx))

	receipt, lease, status, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(now),
		admissionTestReservation(now),
		admissionTestLeaseProposal(),
	)
	if err != nil {
		t.Fatalf("AuthorizeAndReserve() error = %v", err)
	}
	if status != ingest.ReservationCreatedLeaseAcquired {
		t.Fatalf("AuthorizeAndReserve() status = %q, want %q", status, ingest.ReservationCreatedLeaseAcquired)
	}
	if ingest.ValidateLeaseGrant(lease) != nil || lease.Fence.Token != 1 {
		t.Fatalf("AuthorizeAndReserve() lease = %#v", lease)
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
	assertAdmissionReceiptArtifactsEmpty(t, receipt)

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

			_, _, _, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				admissionTestReservation(now),
				admissionTestLeaseProposal(),
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
		{name: "expected sample count", mutate: func(value *ingest.Reservation) { value.ExpectedSampleCount++ }},
		{name: "first captured", mutate: func(value *ingest.Reservation) { value.FirstCapturedAt = value.FirstCapturedAt.Add(time.Second) }},
		{name: "last captured", mutate: func(value *ingest.Reservation) { value.LastCapturedAt = value.LastCapturedAt.Add(time.Second) }},
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

			_, _, _, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				scope,
				reservation,
				admissionTestLeaseProposal(),
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
		{name: "reserved active lease remains in progress", state: ingest.ReceiptReserved, wantStatus: ingest.ReservationReplayInProgress},
		{name: "stored is complete", state: ingest.ReceiptStored, wantStatus: ingest.ReservationReplayComplete},
		{name: "rejected remains rejected", state: ingest.ReceiptRejected, wantStatus: ingest.ReservationReplayRejected},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, expected := admissionReplayTransaction(t, now, test.state)
			store := admissionTestStore(now, admissionRunner(tx))

			got, _, status, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				admissionTestReservation(now),
				admissionTestLeaseProposal(),
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
			if test.state == ingest.ReceiptStored {
				assertAdmissionStoredReceiptData(t, got, admissionStoredReceiptData(admissionTestReservation(now)))
			} else {
				assertAdmissionReceiptArtifactsEmpty(t, got)
			}
			if len(tx.creates) != 0 || len(tx.updates) != 0 {
				t.Fatalf("replay creates/updates = %d/%d, want 0/0", len(tx.creates), len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreExpiredLeaseTakeoverIncrementsFence(t *testing.T) {
	createdAt := admissionTestNow()
	takeoverAt := createdAt.Add(ingest.DefaultRequestLeaseDuration)
	tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
	tx.snapshot = admissionTestSnapshot(takeoverAt)
	tx.readTime = takeoverAt
	store := admissionTestStore(takeoverAt, admissionRunner(tx))
	proposal := admissionTestLeaseProposal()
	proposal.Owner.ID = admissionTakeoverOwnerID
	proposal.Attempt.ID = admissionTakeoverOwnerID
	replayScope, replayReservation := admissionReplayRequest(createdAt, takeoverAt)

	receipt, lease, status, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		replayScope,
		replayReservation,
		proposal,
	)
	if err != nil {
		t.Fatalf("AuthorizeAndReserve() error = %v", err)
	}
	if status != ingest.ReservationReplayLeaseAcquired || receipt.FencingToken != 2 ||
		lease.Fence.Token != 2 || lease.Fence.OwnerID != admissionTakeoverOwnerID ||
		receipt.RecoveryAttemptCount != 1 {
		t.Fatalf("takeover result = status:%q receipt:%#v lease:%#v", status, receipt, lease)
	}
	if len(tx.updates) != 1 || tx.updates[0].path != admissionReceiptPath() {
		t.Fatalf("takeover updates = %#v", tx.updates)
	}
	if len(tx.creates) != 1 || tx.creates[0].path != recoveryAttemptDocumentPath(
		admissionTenantID,
		admissionReceiptID,
		admissionTakeoverOwnerID,
	) {
		t.Fatalf("takeover attempt creates = %#v", tx.creates)
	}
	attempt, ok := tx.creates[0].value.(firestoreRecoveryAttempt)
	if !ok || attempt.Status != "started" || attempt.FencingToken != 2 ||
		attempt.OwnerKind != ingest.LeaseOwnerRequest || attempt.WorkerVersion != ingest.RecoveryWorkerVersion {
		t.Fatalf("takeover attempt = %#v", tx.creates[0].value)
	}
}

func TestFirestoreAdmissionStoreReplayRechecksAuthorizationAtReceiptReadTime(t *testing.T) {
	createdAt := admissionTestNow()
	takeoverAt := createdAt.Add(ingest.DefaultRequestLeaseDuration)
	receiptReadAt := takeoverAt.Add(4 * time.Second)
	tests := []struct {
		name       string
		expiresAt  time.Time
		wantDenied bool
	}{
		{name: "consent expires before receipt read", expiresAt: takeoverAt.Add(2 * time.Second), wantDenied: true},
		{name: "consent remains valid through receipt read", expiresAt: takeoverAt.Add(5 * time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
			tx.snapshot = admissionTestSnapshot(takeoverAt)
			tx.snapshot.Consent.ExpiresAt = &test.expiresAt
			tx.snapshot.ConsentState.ExpiresAt = &test.expiresAt
			tx.authorizationReadTime = takeoverAt
			tx.receiptReadTime = receiptReadAt
			store := admissionTestStore(takeoverAt, admissionRunner(tx))
			proposal := admissionTestLeaseProposal()
			proposal.Owner.ID = admissionTakeoverOwnerID
			proposal.Attempt.ID = admissionTakeoverOwnerID
			replayScope, replayReservation := admissionReplayRequest(createdAt, takeoverAt)

			_, _, status, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				replayScope,
				replayReservation,
				proposal,
			)
			if test.wantDenied {
				if !errors.Is(err, ingest.ErrBatchUnauthorized) {
					t.Fatalf("AuthorizeAndReserve() error = %v, want unauthorized", err)
				}
				if len(tx.updates) != 0 || len(tx.creates) != 0 {
					t.Fatalf("expired authorization updates/creates = %d/%d, want 0/0", len(tx.updates), len(tx.creates))
				}
				return
			}
			if err != nil || status != ingest.ReservationReplayLeaseAcquired {
				t.Fatalf("AuthorizeAndReserve() = %q, %v; want takeover", status, err)
			}
			if len(tx.updates) != 1 || len(tx.creates) != 1 {
				t.Fatalf("valid authorization updates/creates = %d/%d, want 1/1", len(tx.updates), len(tx.creates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreClaimRecoveryLeaseStatuses(t *testing.T) {
	createdAt := admissionTestNow()
	tests := []struct {
		name        string
		requestedAt time.Time
		state       ingest.ReceiptState
		configure   func(*firestoreIngestReceipt)
		wantStatus  ingest.LeaseStatus
		wantToken   int64
		wantWrite   bool
	}{
		{
			name:        "expired lease acquired",
			requestedAt: createdAt.Add(ingest.DefaultRequestLeaseDuration),
			state:       ingest.ReceiptReserved,
			wantStatus:  ingest.LeaseStatusAcquired,
			wantToken:   2,
			wantWrite:   true,
		},
		{
			name:        "active lease held",
			requestedAt: createdAt.Add(time.Minute),
			state:       ingest.ReceiptReserved,
			wantStatus:  ingest.LeaseStatusHeld,
		},
		{
			name:        "released lease not due",
			requestedAt: createdAt.Add(time.Minute),
			state:       ingest.ReceiptReserved,
			configure: func(receipt *firestoreIngestReceipt) {
				receipt.clearLease()
				receipt.NextRecoveryAt = createdAt.Add(90 * time.Second)
				receipt.LastRecoveryCode = string(ingest.LeaseReleaseArtifactUnavailable)
			},
			wantStatus: ingest.LeaseStatusNotDue,
		},
		{
			name:        "deadline elapsed",
			requestedAt: createdAt.Add(ingest.ReservationProcessingWindow),
			state:       ingest.ReceiptReserved,
			wantStatus:  ingest.LeaseStatusDeadlineElapsed,
		},
		{
			name:        "terminal receipt not eligible",
			requestedAt: createdAt.Add(time.Minute),
			state:       ingest.ReceiptStored,
			wantStatus:  ingest.LeaseStatusNotEligible,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, _ := admissionReplayTransaction(t, createdAt, test.state)
			receipt := tx.receipts[admissionReceiptPath()]
			if test.configure != nil {
				test.configure(&receipt)
				tx.receipts[admissionReceiptPath()] = receipt
			}
			tx.readTime = test.requestedAt
			store := admissionTestStore(test.requestedAt, admissionRunner(tx))
			grant, status, err := store.ClaimRecoveryLease(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				ingest.LeaseOwner{ID: admissionTakeoverOwnerID, Kind: ingest.LeaseOwnerSweeper},
				ingest.RecoveryAttemptProposal{ID: admissionTakeoverOwnerID, WorkerVersion: ingest.RecoveryWorkerVersion},
				test.requestedAt,
				ingest.DefaultRequestLeaseDuration,
			)
			if err != nil || status != test.wantStatus {
				t.Fatalf("ClaimRecoveryLease() = %#v, %q, %v", grant, status, err)
			}
			if test.wantToken != 0 && (grant.Fence.Token != test.wantToken || grant.OwnerKind != ingest.LeaseOwnerSweeper) {
				t.Fatalf("ClaimRecoveryLease() grant = %#v", grant)
			}
			if test.wantWrite {
				if len(tx.updates) != 1 || len(tx.creates) != 1 {
					t.Fatalf("claim updates/creates = %d/%d, want 1/1", len(tx.updates), len(tx.creates))
				}
				attempt, ok := tx.creates[0].value.(firestoreRecoveryAttempt)
				if !ok || attempt.OwnerKind != ingest.LeaseOwnerSweeper || attempt.FencingToken != test.wantToken {
					t.Fatalf("claim attempt = %#v", tx.creates[0].value)
				}
			} else if len(tx.updates) != 0 || len(tx.creates) != 0 {
				t.Fatalf("read-only claim updates/creates = %d/%d, want zero", len(tx.updates), len(tx.creates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreRenewLeaseWithinWindow(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	renewedAt := reservation.CreatedAt.Add(ingest.DefaultRequestLeaseDuration - 30*time.Second)
	tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
	tx.readTime = renewedAt
	store := admissionTestStore(renewedAt, admissionRunner(tx))

	grant, err := store.RenewLease(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		admissionTestFence(reservation),
		renewedAt,
		ingest.DefaultRequestLeaseDuration,
	)
	if err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}
	if grant.Fence.Token != 1 || grant.Fence.OwnerID != admissionLeaseOwnerID ||
		!grant.AcquiredAt.Equal(createdAt) || !grant.HeartbeatAt.Equal(renewedAt) ||
		!grant.Fence.ExpiresAt.Equal(renewedAt.Add(ingest.DefaultRequestLeaseDuration)) {
		t.Fatalf("RenewLease() grant = %#v", grant)
	}
	if len(tx.updates) != 1 || len(tx.creates) != 0 {
		t.Fatalf("renew updates/creates = %d/%d, want 1/0", len(tx.updates), len(tx.creates))
	}
}

func TestFirestoreAdmissionStoreRenewLeaseRejectsIneligibleCalls(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	tests := []struct {
		name      string
		renewedAt time.Time
		fence     ingest.LeaseFence
		duration  time.Duration
	}{
		{
			name:      "too early",
			renewedAt: createdAt.Add(time.Minute),
			fence:     admissionTestFence(reservation),
			duration:  ingest.DefaultRequestLeaseDuration,
		},
		{
			name:      "at expiry",
			renewedAt: createdAt.Add(ingest.DefaultRequestLeaseDuration),
			fence:     admissionTestFence(reservation),
			duration:  ingest.DefaultRequestLeaseDuration,
		},
		{
			name:      "stale owner",
			renewedAt: createdAt.Add(ingest.DefaultRequestLeaseDuration - 30*time.Second),
			fence: ingest.LeaseFence{
				OwnerID:   admissionTakeoverOwnerID,
				Token:     1,
				ExpiresAt: createdAt.Add(ingest.DefaultRequestLeaseDuration),
			},
			duration: ingest.DefaultRequestLeaseDuration,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
			tx.readTime = test.renewedAt
			store := admissionTestStore(test.renewedAt, admissionRunner(tx))
			_, err := store.RenewLease(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				test.fence,
				test.renewedAt,
				test.duration,
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("RenewLease() error = %v, want unavailable", err)
			}
			if len(tx.updates) != 0 || len(tx.creates) != 0 {
				t.Fatalf("ineligible renew updates/creates = %d/%d, want zero", len(tx.updates), len(tx.creates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreBeginCleanupTransitionAtDeadline(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	cleanupAt := reservation.ReservationDeadline
	tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
	tx.readTime = cleanupAt
	store := admissionTestStore(cleanupAt, admissionRunner(tx))

	receipt, status, err := store.BeginCleanupTransition(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		cleanupAt,
	)
	if err != nil || status != ingest.TransitionStatusStarted {
		t.Fatalf("BeginCleanupTransition() = %#v, %q, %v", receipt, status, err)
	}
	if receipt.State != ingest.ReceiptCleanupPending || receipt.FencingToken != 2 || receipt.Revision != 2 ||
		receipt.LeaseOwnerID != "" || !receipt.LeaseExpiresAt.IsZero() ||
		receipt.CleanupMode != ingest.CleanupModeReservationExpiry ||
		receipt.CleanupOriginStatus != ingest.ReceiptReserved ||
		!receipt.CleanupQuiescenceUntil.Equal(cleanupAt.Add(ingest.DefaultCleanupLateWriteGrace)) {
		t.Fatalf("cleanup transition receipt = %#v", receipt)
	}
	if len(tx.updates) != 1 || len(tx.creates) != 0 {
		t.Fatalf("cleanup transition updates/creates = %d/%d, want 1/0", len(tx.updates), len(tx.creates))
	}
}

func TestFirestoreAdmissionStoreBeginCleanupTransitionReadOnlyStatuses(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	tests := []struct {
		name        string
		requestedAt time.Time
		readTime    time.Time
		state       ingest.ReceiptState
		configure   func(*firestoreIngestReceipt)
		wantStatus  ingest.TransitionStatus
	}{
		{
			name:        "before deadline",
			requestedAt: reservation.ReservationDeadline.Add(-time.Second),
			readTime:    reservation.ReservationDeadline.Add(-time.Second),
			state:       ingest.ReceiptReserved,
			wantStatus:  ingest.TransitionStatusNotReady,
		},
		{
			name:        "server time prevents early cleanup",
			requestedAt: reservation.ReservationDeadline,
			readTime:    reservation.ReservationDeadline.Add(-time.Nanosecond),
			state:       ingest.ReceiptReserved,
			wantStatus:  ingest.TransitionStatusNotReady,
		},
		{
			name:        "terminal receipt not eligible",
			requestedAt: reservation.ReservationDeadline,
			readTime:    reservation.ReservationDeadline,
			state:       ingest.ReceiptStored,
			wantStatus:  ingest.TransitionStatusNotEligible,
		},
		{
			name:        "existing transition",
			requestedAt: reservation.ReservationDeadline.Add(time.Second),
			readTime:    reservation.ReservationDeadline.Add(time.Second),
			state:       ingest.ReceiptCleanupPending,
			configure: func(receipt *firestoreIngestReceipt) {
				receipt.clearLease()
				receipt.CleanupMode = ingest.CleanupModeReservationExpiry
				receipt.CleanupOriginStatus = ingest.ReceiptReserved
				receipt.UpdatedAt = reservation.ReservationDeadline
				receipt.CleanupQuiescenceUntil = reservation.ReservationDeadline.Add(ingest.DefaultCleanupLateWriteGrace)
				receipt.FencingToken = 2
				receipt.Revision = 2
			},
			wantStatus: ingest.TransitionStatusAlreadyStarted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, _ := admissionReplayTransaction(t, createdAt, test.state)
			receipt := tx.receipts[admissionReceiptPath()]
			if test.configure != nil {
				test.configure(&receipt)
				tx.receipts[admissionReceiptPath()] = receipt
			}
			tx.readTime = test.readTime
			store := admissionTestStore(test.requestedAt, admissionRunner(tx))
			_, status, err := store.BeginCleanupTransition(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				test.requestedAt,
			)
			if err != nil || status != test.wantStatus {
				t.Fatalf("BeginCleanupTransition() = %q, %v, want %q", status, err, test.wantStatus)
			}
			if len(tx.updates) != 0 || len(tx.creates) != 0 {
				t.Fatalf("read-only transition updates/creates = %d/%d, want zero", len(tx.updates), len(tx.creates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreCleanupTransitionFencesStaleFinalizer(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	receipt := admissionTestReceiptDTO(admissionTestReceipt(reservation, ingest.ReceiptCleanupPending))
	receipt.clearLease()
	receipt.CleanupMode = ingest.CleanupModeReservationExpiry
	receipt.CleanupOriginStatus = ingest.ReceiptReserved
	receipt.UpdatedAt = reservation.ReservationDeadline
	receipt.CleanupQuiescenceUntil = reservation.ReservationDeadline.Add(ingest.DefaultCleanupLateWriteGrace)
	receipt.FencingToken = 2
	receipt.Revision = 2
	index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(reservation.ReservationDeadline))
	tx.readTime = reservation.ReservationDeadline
	tx.indexes[admissionIdempotencyPath()] = index
	tx.indexes[admissionClientBatchPath()] = index
	tx.receipts[admissionReceiptPath()] = receipt
	store := admissionTestStore(reservation.ReservationDeadline, admissionRunner(tx))

	_, err := store.MarkStored(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		admissionTestFence(reservation),
		admissionStoredReceiptData(reservation),
		reservation.ReservationDeadline,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("MarkStored() stale cleanup fence error = %v", err)
	}
	if len(tx.updates) != 0 {
		t.Fatalf("stale cleanup finalizer updates = %d, want zero", len(tx.updates))
	}
}

func TestFirestoreAdmissionStoreStaleFenceCannotFinalizeAfterTakeover(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
	receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
	receipt.FencingToken = 2
	receipt.LeaseOwnerID = admissionTakeoverOwnerID
	receipt.LeaseAcquiredAt = createdAt.Add(time.Minute)
	receipt.LeaseHeartbeatAt = receipt.LeaseAcquiredAt
	receipt.LeaseExpiresAt = receipt.LeaseAcquiredAt.Add(ingest.DefaultRequestLeaseDuration)
	receipt.NextRecoveryAt = receipt.LeaseExpiresAt
	receipt.Revision = 2
	receipt.UpdatedAt = receipt.LeaseAcquiredAt
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(receipt.LeaseAcquiredAt))
	tx.indexes[admissionIdempotencyPath()] = index
	tx.indexes[admissionClientBatchPath()] = index
	tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
	store := admissionTestStore(receipt.LeaseAcquiredAt, admissionRunner(tx))

	_, err := store.MarkStored(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		admissionTestFence(reservation),
		admissionStoredReceiptData(reservation),
		receipt.LeaseAcquiredAt.Add(time.Second),
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("MarkStored() stale fence error = %v", err)
	}
	if len(tx.updates) != 0 {
		t.Fatalf("stale finalizer updates = %d, want zero", len(tx.updates))
	}
}

func TestFirestoreAdmissionStoreReleaseLeaseSchedulesBoundedRecovery(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
	store := admissionTestStore(createdAt, admissionRunner(tx))
	releasedAt := createdAt.Add(time.Minute)
	tx.readTime = releasedAt

	err := store.ReleaseLease(
		context.Background(),
		admissionTenantID,
		admissionReservationKey,
		admissionTestFence(reservation),
		releasedAt,
		ingest.LeaseReleaseArtifactUnavailable,
	)
	if err != nil {
		t.Fatalf("ReleaseLease() error = %v", err)
	}
	if len(tx.updates) != 1 {
		t.Fatalf("ReleaseLease() updates = %d, want one", len(tx.updates))
	}
	updates := make(map[string]any)
	for _, update := range tx.updates[0].updates {
		updates[update.Path] = update.Value
	}
	if got, ok := updates["next_recovery_at"].(time.Time); !ok ||
		!got.Equal(releasedAt.Add(ingest.InitialRecoveryBackoff)) {
		t.Fatalf("next_recovery_at = %#v", updates["next_recovery_at"])
	}
	if updates["last_recovery_code"] != string(ingest.LeaseReleaseArtifactUnavailable) {
		t.Fatalf("last_recovery_code = %#v", updates["last_recovery_code"])
	}
}

func TestConservativeAdmissionTimesAreOperationSpecific(t *testing.T) {
	applicationTime := admissionTestNow()
	readTime := applicationTime.Add(2 * time.Second)
	acceptance, err := conservativeAcceptanceTime(applicationTime, readTime)
	if err != nil || !acceptance.Equal(readTime) {
		t.Fatalf("acceptance time = %s, %v; want later read time", acceptance, err)
	}
	cleanup, err := conservativeCleanupTime(applicationTime, readTime)
	if err != nil || !cleanup.Equal(applicationTime) {
		t.Fatalf("cleanup time = %s, %v; want earlier application time", cleanup, err)
	}
	if _, err := conservativeAcceptanceTime(applicationTime, applicationTime.Add(maxAdmissionClockSkew+time.Nanosecond)); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("excess skew error = %v", err)
	}
	for _, extreme := range []time.Time{
		time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC),
	} {
		if _, err := conservativeAcceptanceTime(applicationTime, extreme); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
			t.Fatalf("extreme acceptance skew %s error = %v", extreme, err)
		}
		if _, err := conservativeCleanupTime(applicationTime, extreme); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
			t.Fatalf("extreme cleanup skew %s error = %v", extreme, err)
		}
	}
}

func TestFirestoreAdmissionStoreForwardMutationsRejectRevisionOverflow(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	updatedAt := createdAt.Add(time.Minute)
	mutations := []struct {
		name   string
		invoke func(*FirestoreAdmissionStore) error
	}{
		{
			name: "mark stored",
			invoke: func(store *FirestoreAdmissionStore) error {
				_, err := store.MarkStored(
					context.Background(), admissionTenantID, admissionReservationKey,
					admissionTestFence(reservation), admissionStoredReceiptData(reservation), updatedAt,
				)
				return err
			},
		},
		{
			name: "mark rejected",
			invoke: func(store *FirestoreAdmissionStore) error {
				_, err := store.MarkRejected(
					context.Background(), admissionTenantID, admissionReservationKey,
					admissionTestFence(reservation), "object_conflict", updatedAt,
				)
				return err
			},
		},
		{
			name: "release lease",
			invoke: func(store *FirestoreAdmissionStore) error {
				return store.ReleaseLease(
					context.Background(), admissionTenantID, admissionReservationKey,
					admissionTestFence(reservation), updatedAt, ingest.LeaseReleaseArtifactUnavailable,
				)
			},
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
			receipt := tx.receipts[admissionReceiptPath()]
			receipt.Revision = math.MaxInt64
			tx.receipts[admissionReceiptPath()] = receipt
			tx.readTime = updatedAt
			store := admissionTestStore(updatedAt, admissionRunner(tx))

			if err := mutation.invoke(store); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("mutation error = %v, want unavailable", err)
			}
			if len(tx.updates) != 0 {
				t.Fatalf("overflow updates = %d, want zero", len(tx.updates))
			}
		})
	}
}

func TestValidateReservedOriginCleanupReceiptState(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	transitionAt := reservation.ReservationDeadline
	quiescenceUntil := transitionAt.Add(ingest.DefaultCleanupLateWriteGrace)
	pending := admissionTestReceiptDTO(admissionTestReceipt(reservation, ingest.ReceiptReserved))
	pending.State = ingest.ReceiptCleanupPending
	pending.clearLease()
	pending.CleanupMode = ingest.CleanupModeReservationExpiry
	pending.CleanupOriginStatus = ingest.ReceiptReserved
	pending.CleanupQuiescenceUntil = quiescenceUntil
	pending.UpdatedAt = transitionAt
	if err := validateReceiptState(pending); err != nil {
		t.Fatalf("valid cleanup_pending rejected: %v", err)
	}
	withArtifact := pending
	withArtifact.ObjectPath = expectedObjectPath(withArtifact)
	if err := validateReceiptState(withArtifact); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatal("cleanup_pending with stored artifact fields was accepted")
	}
	expired := pending
	expired.State = ingest.ReceiptExpired
	expired.UpdatedAt = quiescenceUntil
	if err := validateReceiptState(expired); err != nil {
		t.Fatalf("valid expired receipt rejected: %v", err)
	}
	expired.UpdatedAt = quiescenceUntil.Add(-time.Nanosecond)
	if err := validateReceiptState(expired); !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatal("expired receipt completed before quiescence was accepted")
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

			_, _, status, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				reservation,
				admissionTestLeaseProposal(),
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
				index.ReceiptRetentionFloor = reservation.CreatedAt.Add(31 * 24 * time.Hour)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				receipt.ReceiptRetentionFloor = index.ReceiptRetentionFloor
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
		{
			name: "stored receipt has incomplete artifact lineage",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptStored)
				receipt.ManifestSHA256 = ""
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
		{
			name: "reserved receipt missing expected sample count",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				receipt.ExpectedSampleCount = 0
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
		{
			name: "reserved receipt has partial lease",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				receipt.LeaseOwnerID = ""
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
		{
			name: "rejected receipt has stale artifact lineage",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptRejected)
				applyAdmissionStoredReceiptData(&receipt, admissionStoredReceiptData(reservation))
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
			test.configure(tx)
			store := admissionTestStore(now, admissionRunner(tx))

			_, _, _, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				reservation,
				admissionTestLeaseProposal(),
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

	receipt, _, status, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(now),
		admissionTestReservation(now),
		admissionTestLeaseProposal(),
	)
	if err != nil {
		t.Fatalf("AuthorizeAndReserve() error = %v", err)
	}
	if authorizationCalls != 2 || first.authorizationLoads != 1 || second.authorizationLoads != 1 {
		t.Fatalf("authorization evaluations = runner:%d first:%d second:%d, want 2/1/1", authorizationCalls, first.authorizationLoads, second.authorizationLoads)
	}
	if status != ingest.ReservationCreatedLeaseAcquired {
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
	persisted := admissionTestReservation(now.Add(-ingest.ReservationProcessingWindow))
	index := admissionTestIndex(persisted, persisted.ReceiptID, persisted.BodyHash)
	receipt := admissionTestReceipt(persisted, ingest.ReceiptReserved)
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
	tx.indexes[admissionIdempotencyPath()] = index
	tx.indexes[admissionClientBatchPath()] = index
	tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
	store := admissionTestStore(now, admissionRunner(tx))

	_, _, _, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(now),
		proposal,
		admissionTestLeaseProposal(),
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

	_, _, _, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(now),
		admissionTestReservation(now),
		admissionTestLeaseProposal(),
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
	updatedAt := createdAt.Add(time.Minute)
	reservation := admissionTestReservation(createdAt)
	tests := []struct {
		name          string
		invoke        func(*FirestoreAdmissionStore) (ingest.Receipt, error)
		wantState     ingest.ReceiptState
		wantPath      string
		wantRejection string
	}{
		{
			name: "mark stored",
			invoke: func(store *FirestoreAdmissionStore) (ingest.Receipt, error) {
				return store.MarkStored(
					context.Background(),
					admissionTenantID,
					admissionReservationKey,
					admissionTestFence(reservation),
					admissionStoredReceiptData(reservation),
					updatedAt,
				)
			},
			wantState: ingest.ReceiptStored,
			wantPath:  admissionReceiptPath(),
		},
		{
			name: "mark rejected",
			invoke: func(store *FirestoreAdmissionStore) (ingest.Receipt, error) {
				return store.MarkRejected(context.Background(), admissionTenantID, admissionReservationKey, admissionTestFence(reservation), "object_conflict", updatedAt)
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
			tx.readTime = updatedAt
			store := admissionTestStore(createdAt, admissionRunner(tx))

			got, err := test.invoke(store)
			if err != nil {
				t.Fatalf("finalize error = %v", err)
			}
			if got.State != test.wantState || got.Revision != 2 || !got.UpdatedAt.Equal(updatedAt) {
				t.Fatalf("final receipt state/revision/time = %q/%d/%s, want %q/2/%s", got.State, got.Revision, got.UpdatedAt, test.wantState, updatedAt)
			}
			if got.RejectionCode != test.wantRejection {
				t.Fatalf("final receipt rejection = %q, want %q", got.RejectionCode, test.wantRejection)
			}
			if test.wantState == ingest.ReceiptStored {
				assertAdmissionStoredReceiptData(t, got, admissionStoredReceiptData(reservation))
			} else {
				assertAdmissionReceiptArtifactsEmpty(t, got)
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
			name: "reserved receipt has stale artifact fields",
			configure: func(tx *fakeAdmissionTransaction) {
				index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
				tx.indexes[admissionIdempotencyPath()] = index
				tx.indexes[admissionClientBatchPath()] = index
				receipt := admissionTestReceipt(reservation, ingest.ReceiptReserved)
				applyAdmissionStoredReceiptData(&receipt, admissionStoredReceiptData(reservation))
				tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
			tx.readTime = now.Add(time.Minute)
			if test.configure != nil {
				test.configure(tx)
			}
			store := admissionTestStore(now, admissionRunner(tx))

			_, err := store.MarkStored(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				admissionTestFence(reservation),
				admissionStoredReceiptData(reservation),
				now.Add(time.Minute),
			)
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
					ingest.LeaseFence{},
					admissionStoredReceiptData(reservation),
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
					ingest.LeaseFence{},
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
			if test.state == ingest.ReceiptStored {
				assertAdmissionStoredReceiptData(t, got, admissionStoredReceiptData(reservation))
			} else {
				assertAdmissionReceiptArtifactsEmpty(t, got)
			}
			if len(tx.updates) != 0 {
				t.Fatalf("terminal replay updates = %d, want zero", len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreStoredReplayRejectsArtifactMismatch(t *testing.T) {
	now := admissionTestNow()
	reservation := admissionTestReservation(now)
	mutations := []struct {
		name   string
		mutate func(*ingest.StoredReceiptData)
	}{
		{name: "object path", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Object.Path += ".other" }},
		{name: "object sha256", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Object.SHA256 = strings.Repeat("c", 64) }},
		{name: "object crc32c", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Object.CRC32C++ }},
		{name: "object size", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Object.Size++ }},
		{name: "object generation", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Object.Generation++ }},
		{name: "object metageneration", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Object.Metageneration++ }},
		{name: "manifest path", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Manifest.Path += ".other" }},
		{name: "manifest sha256", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Manifest.SHA256 = strings.Repeat("d", 64) }},
		{name: "manifest crc32c", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Manifest.CRC32C++ }},
		{name: "manifest size", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Manifest.Size++ }},
		{name: "manifest generation", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Manifest.Generation++ }},
		{name: "manifest metageneration", mutate: func(stored *ingest.StoredReceiptData) { stored.Artifacts.Manifest.Metageneration++ }},
		{name: "sample count", mutate: func(stored *ingest.StoredReceiptData) { stored.SampleCount++ }},
	}

	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			tx := newFakeAdmissionTransaction(admissionTestSnapshot(now))
			index := admissionTestIndex(reservation, admissionReceiptID, reservation.BodyHash)
			tx.indexes[admissionIdempotencyPath()] = index
			tx.indexes[admissionClientBatchPath()] = index
			receipt := admissionTestReceipt(reservation, ingest.ReceiptStored)
			receipt.Revision = 2
			tx.receipts[admissionReceiptPath()] = admissionTestReceiptDTO(receipt)
			store := admissionTestStore(now, admissionRunner(tx))
			stored := admissionStoredReceiptData(reservation)
			test.mutate(&stored)

			_, err := store.MarkStored(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				admissionTestFence(reservation),
				stored,
				now.Add(time.Minute),
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("MarkStored() error = %v, want artifact mismatch unavailable", err)
			}
			if len(tx.updates) != 0 {
				t.Fatalf("artifact mismatch updates = %d, want zero", len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreRejectsExpiredReservedFinalizer(t *testing.T) {
	now := admissionTestNow()
	reservation := admissionTestReservation(now.Add(-ingest.ReservationProcessingWindow))
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
		admissionTestFence(reservation),
		admissionStoredReceiptData(reservation),
		now,
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("MarkStored() error = %v, want expired reserved unavailable", err)
	}
	if len(tx.updates) != 0 {
		t.Fatalf("expired reserved updates = %d, want zero", len(tx.updates))
	}
}

func TestFirestoreAdmissionStoreForwardMutationsUseReceiptReadTime(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	leaseExpiresAt := reservation.CreatedAt.Add(ingest.DefaultRequestLeaseDuration)
	tests := []struct {
		name       string
		callerTime time.Time
		readTime   time.Time
	}{
		{
			name:       "server observes expired lease while caller clock is still before expiry",
			callerTime: leaseExpiresAt.Add(-2 * time.Second),
			readTime:   leaseExpiresAt.Add(2 * time.Second),
		},
		{
			name:       "caller and server clock skew exceeds bound",
			callerTime: createdAt.Add(time.Minute),
			readTime:   createdAt.Add(time.Minute + maxAdmissionClockSkew + time.Nanosecond),
		},
	}
	mutations := []struct {
		name   string
		invoke func(*FirestoreAdmissionStore, time.Time) error
	}{
		{
			name: "mark stored",
			invoke: func(store *FirestoreAdmissionStore, at time.Time) error {
				_, err := store.MarkStored(
					context.Background(),
					admissionTenantID,
					admissionReservationKey,
					admissionTestFence(reservation),
					admissionStoredReceiptData(reservation),
					at,
				)
				return err
			},
		},
		{
			name: "mark rejected",
			invoke: func(store *FirestoreAdmissionStore, at time.Time) error {
				_, err := store.MarkRejected(
					context.Background(),
					admissionTenantID,
					admissionReservationKey,
					admissionTestFence(reservation),
					"object_conflict",
					at,
				)
				return err
			},
		},
		{
			name: "release lease",
			invoke: func(store *FirestoreAdmissionStore, at time.Time) error {
				return store.ReleaseLease(
					context.Background(),
					admissionTenantID,
					admissionReservationKey,
					admissionTestFence(reservation),
					at,
					ingest.LeaseReleaseFinalizerUnavailable,
				)
			},
		},
	}
	for _, test := range tests {
		for _, mutation := range mutations {
			t.Run(test.name+"/"+mutation.name, func(t *testing.T) {
				tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
				tx.readTime = test.readTime
				store := admissionTestStore(test.callerTime, admissionRunner(tx))

				err := mutation.invoke(store, test.callerTime)
				if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
					t.Fatalf("forward mutation error = %v, want unavailable", err)
				}
				if len(tx.updates) != 0 {
					t.Fatalf("forward mutation updates = %d, want zero", len(tx.updates))
				}
			})
		}
	}
}

func TestFirestoreAdmissionStoreMarkStoredRequiresExpectedSampleCount(t *testing.T) {
	createdAt := admissionTestNow()
	reservation := admissionTestReservation(createdAt)
	updatedAt := createdAt.Add(time.Minute)
	for _, sampleCount := range []int{reservation.ExpectedSampleCount - 1, reservation.ExpectedSampleCount + 1} {
		t.Run(fmt.Sprintf("sample_count_%d", sampleCount), func(t *testing.T) {
			tx, _ := admissionReplayTransaction(t, createdAt, ingest.ReceiptReserved)
			tx.readTime = updatedAt
			store := admissionTestStore(updatedAt, admissionRunner(tx))
			stored := admissionStoredReceiptData(reservation)
			stored.SampleCount = sampleCount

			_, err := store.MarkStored(
				context.Background(),
				admissionTenantID,
				admissionReservationKey,
				admissionTestFence(reservation),
				stored,
				updatedAt,
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("MarkStored() error = %v, want sample count mismatch unavailable", err)
			}
			if len(tx.updates) != 0 {
				t.Fatalf("sample count mismatch updates = %d, want zero", len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreReplayBindsReceiptImmutablesToReservation(t *testing.T) {
	now := admissionTestNow()
	tests := []struct {
		name   string
		mutate func(*firestoreIngestReceipt)
	}{
		{name: "device", mutate: func(receipt *firestoreIngestReceipt) { receipt.DeviceID = "01982015-4400-7000-8000-000000000091" }},
		{name: "trip", mutate: func(receipt *firestoreIngestReceipt) { receipt.TripID = "01982015-4400-7000-8000-000000000092" }},
		{name: "consent", mutate: func(receipt *firestoreIngestReceipt) {
			receipt.ConsentRevisionID = "01982015-4400-7000-8000-000000000093"
		}},
		{name: "expected sample count", mutate: func(receipt *firestoreIngestReceipt) { receipt.ExpectedSampleCount++ }},
		{name: "first captured time", mutate: func(receipt *firestoreIngestReceipt) {
			receipt.FirstCapturedAt = receipt.FirstCapturedAt.Add(time.Nanosecond)
		}},
		{name: "last captured time", mutate: func(receipt *firestoreIngestReceipt) {
			receipt.LastCapturedAt = receipt.LastCapturedAt.Add(-time.Nanosecond)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, _ := admissionReplayTransaction(t, now, ingest.ReceiptReserved)
			receipt := tx.receipts[admissionReceiptPath()]
			test.mutate(&receipt)
			tx.receipts[admissionReceiptPath()] = receipt
			store := admissionTestStore(now, admissionRunner(tx))

			_, _, _, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				admissionTestReservation(now),
				admissionTestLeaseProposal(),
			)
			if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
				t.Fatalf("AuthorizeAndReserve() error = %v, want immutable mismatch unavailable", err)
			}
			if len(tx.creates) != 0 || len(tx.updates) != 0 {
				t.Fatalf("immutable mismatch creates/updates = %d/%d, want zero", len(tx.creates), len(tx.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreValidatesPersistedLeaseDuration(t *testing.T) {
	now := admissionTestNow()
	tests := []struct {
		name     string
		duration time.Duration
		wantErr  bool
	}{
		{name: "below minimum", duration: ingest.MinLeaseDuration - time.Nanosecond, wantErr: true},
		{name: "minimum", duration: ingest.MinLeaseDuration},
		{name: "maximum", duration: ingest.MaxLeaseDuration},
		{name: "above maximum", duration: ingest.MaxLeaseDuration + time.Nanosecond, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, _ := admissionReplayTransaction(t, now, ingest.ReceiptReserved)
			receipt := tx.receipts[admissionReceiptPath()]
			receipt.LeaseExpiresAt = receipt.LeaseHeartbeatAt.Add(test.duration)
			receipt.NextRecoveryAt = receipt.LeaseExpiresAt
			tx.receipts[admissionReceiptPath()] = receipt
			store := admissionTestStore(now, admissionRunner(tx))

			_, _, status, err := store.AuthorizeAndReserve(
				context.Background(),
				admissionTestPrincipal(),
				admissionTestScope(now),
				admissionTestReservation(now),
				admissionTestLeaseProposal(),
			)
			if test.wantErr {
				if !errors.Is(err, ingest.ErrAdmissionUnavailable) || len(tx.updates) != 0 {
					t.Fatalf("invalid duration result = %q, %v, updates %d", status, err, len(tx.updates))
				}
				return
			}
			if err != nil || status != ingest.ReservationReplayInProgress {
				t.Fatalf("valid duration result = %q, %v", status, err)
			}
		})
	}
}

func TestFirestoreAdmissionStoreRejectsInitialLeaseBeforeReservationCreation(t *testing.T) {
	createdAt := admissionTestNow()
	trustedNow := createdAt.Add(-time.Nanosecond)
	tx := newFakeAdmissionTransaction(admissionTestSnapshot(createdAt))
	tx.readTime = trustedNow
	store := admissionTestStore(trustedNow, admissionRunner(tx))

	_, _, _, err := store.AuthorizeAndReserve(
		context.Background(),
		admissionTestPrincipal(),
		admissionTestScope(createdAt),
		admissionTestReservation(createdAt),
		admissionTestLeaseProposal(),
	)
	if !errors.Is(err, ingest.ErrAdmissionUnavailable) {
		t.Fatalf("AuthorizeAndReserve() error = %v, want time ordering unavailable", err)
	}
	if len(tx.creates) != 0 || len(tx.updates) != 0 {
		t.Fatalf("invalid initial time creates/updates = %d/%d, want zero", len(tx.creates), len(tx.updates))
	}
}

type fakeAdmissionTransaction struct {
	snapshot              authorization.Snapshot
	readTime              time.Time
	authorizationReadTime time.Time
	receiptReadTime       time.Time
	authorizationErr      error
	indexes               map[string]firestoreIngestIndex
	receipts              map[string]firestoreIngestReceipt
	calls                 []string
	creates               []fakeAdmissionCreate
	updates               []fakeAdmissionUpdate
	authorizationLoads    int
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
		readTime: snapshot.Trip.StartedAt.Add(time.Hour).UTC(),
		indexes:  make(map[string]firestoreIngestIndex),
		receipts: make(map[string]firestoreIngestReceipt),
	}
}

func (tx *fakeAdmissionTransaction) LoadAuthorization(
	_ context.Context,
	_ ingest.Principal,
	_ ingest.BatchScope,
) (authorizationRead, error) {
	tx.calls = append(tx.calls, "authorization")
	tx.authorizationLoads++
	readTime := tx.authorizationReadTime
	if readTime.IsZero() {
		readTime = tx.readTime
	}
	return authorizationRead{Snapshot: tx.snapshot, ReadTime: readTime}, tx.authorizationErr
}

func (tx *fakeAdmissionTransaction) ReadIndex(_ context.Context, path string) (firestoreIngestIndex, bool, error) {
	tx.calls = append(tx.calls, "index:"+path)
	value, exists := tx.indexes[path]
	return value, exists, nil
}

func (tx *fakeAdmissionTransaction) ReadReceipt(_ context.Context, path string) (receiptRead, bool, error) {
	tx.calls = append(tx.calls, "receipt:"+path)
	value, exists := tx.receipts[path]
	readTime := tx.receiptReadTime
	if readTime.IsZero() {
		readTime = tx.readTime
	}
	return receiptRead{Receipt: value, ReadTime: readTime}, exists, nil
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
		TenantID:            admissionTenantID,
		DeviceID:            admissionDeviceID,
		TripID:              admissionTripID,
		ClientSessionID:     admissionClientSessionID,
		InstallationID:      admissionInstallationID,
		ConsentRevisionID:   admissionConsentRevisionID,
		ExpectedSampleCount: 42,
		FirstCapturedAt:     now.Add(-5 * time.Minute),
		LastCapturedAt:      now.Add(-time.Minute),
	}
}

func admissionTestReservation(now time.Time) ingest.Reservation {
	scope := admissionTestScope(now)
	return ingest.Reservation{
		ReservationKey:        admissionReservationKey,
		ClientBatchKey:        admissionClientBatchKey,
		ReceiptID:             admissionReceiptID,
		TenantID:              admissionTenantID,
		BatchID:               admissionReceiptID,
		DeviceID:              admissionDeviceID,
		TripID:                admissionTripID,
		InstallationID:        admissionInstallationID,
		ConsentRevisionID:     admissionConsentRevisionID,
		ClientBatchID:         "018f1f4e-2f5e-7d31-8c77-43b50f4c91ab",
		PayloadSchemaVersion:  telemetry.SchemaVersionV2,
		BodyHash:              "7d6db7b45493315b87f4333993082edab4fcc2db365001f91dfc4a57d23f40f4",
		ExpectedSampleCount:   42,
		FirstCapturedAt:       scope.FirstCapturedAt,
		LastCapturedAt:        scope.LastCapturedAt,
		ValidatorVersion:      ingest.TelemetryValidatorVersion,
		CreatedAt:             now,
		ReservationDeadline:   now.Add(ingest.ReservationProcessingWindow),
		ArtifactExpiresAt:     now.Add(ingest.TelemetryArtifactRetention),
		ReceiptRetentionFloor: now.Add(ingest.ReceiptControlRetention),
	}
}

func admissionTestLeaseProposal() ingest.LeaseProposal {
	return ingest.LeaseProposal{
		Owner:    ingest.LeaseOwner{ID: admissionLeaseOwnerID, Kind: ingest.LeaseOwnerRequest},
		Duration: ingest.DefaultRequestLeaseDuration,
		Attempt: ingest.RecoveryAttemptProposal{
			ID:            admissionLeaseOwnerID,
			WorkerVersion: ingest.RecoveryWorkerVersion,
		},
	}
}

func admissionTestFence(reservation ingest.Reservation) ingest.LeaseFence {
	return ingest.LeaseFence{
		OwnerID:   admissionLeaseOwnerID,
		Token:     1,
		ExpiresAt: reservation.CreatedAt.Add(ingest.DefaultRequestLeaseDuration),
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
		TenantID:              reservation.TenantID,
		ReservationKey:        reservation.ReservationKey,
		ClientBatchKey:        reservation.ClientBatchKey,
		ReceiptID:             receiptID,
		BatchID:               reservation.BatchID,
		InstallationID:        reservation.InstallationID,
		ClientBatchID:         reservation.ClientBatchID,
		PayloadSchemaVersion:  reservation.PayloadSchemaVersion,
		BodyHash:              bodyHash,
		CreatedAt:             reservation.CreatedAt,
		ReceiptRetentionFloor: reservation.ReceiptRetentionFloor,
	}
}

func admissionTestReceipt(reservation ingest.Reservation, state ingest.ReceiptState) ingest.Receipt {
	receipt := ingest.Receipt{
		ReservationKey:        reservation.ReservationKey,
		ClientBatchKey:        reservation.ClientBatchKey,
		ReceiptID:             reservation.ReceiptID,
		TenantID:              reservation.TenantID,
		BatchID:               reservation.BatchID,
		DeviceID:              reservation.DeviceID,
		TripID:                reservation.TripID,
		InstallationID:        reservation.InstallationID,
		ConsentRevisionID:     reservation.ConsentRevisionID,
		ClientBatchID:         reservation.ClientBatchID,
		PayloadSchemaVersion:  reservation.PayloadSchemaVersion,
		BodyHash:              reservation.BodyHash,
		ExpectedSampleCount:   reservation.ExpectedSampleCount,
		FirstCapturedAt:       reservation.FirstCapturedAt,
		LastCapturedAt:        reservation.LastCapturedAt,
		ValidatorVersion:      reservation.ValidatorVersion,
		State:                 state,
		FencingToken:          1,
		Revision:              1,
		CreatedAt:             reservation.CreatedAt,
		UpdatedAt:             reservation.CreatedAt,
		ReservationDeadline:   reservation.ReservationDeadline,
		ArtifactExpiresAt:     reservation.ArtifactExpiresAt,
		ReceiptRetentionFloor: reservation.ReceiptRetentionFloor,
	}
	if state == ingest.ReceiptReserved {
		receipt.LeaseOwnerID = admissionLeaseOwnerID
		receipt.LeaseOwnerKind = ingest.LeaseOwnerRequest
		receipt.LeaseAcquiredAt = reservation.CreatedAt
		receipt.LeaseHeartbeatAt = reservation.CreatedAt
		receipt.LeaseExpiresAt = reservation.CreatedAt.Add(ingest.DefaultRequestLeaseDuration)
		receipt.NextRecoveryAt = receipt.LeaseExpiresAt
	}
	if state == ingest.ReceiptStored || state == "queued" || state == "projected" || state == "deleting" || state == "deleted" {
		applyAdmissionStoredReceiptData(&receipt, admissionStoredReceiptData(reservation))
	}
	if state == ingest.ReceiptRejected {
		receipt.RejectionCode = "object_conflict"
	}
	return receipt
}

func admissionTestReceiptDTO(receipt ingest.Receipt) firestoreIngestReceipt {
	return firestoreIngestReceipt{
		ReservationKey:         receipt.ReservationKey,
		ClientBatchKey:         receipt.ClientBatchKey,
		ReceiptID:              receipt.ReceiptID,
		TenantID:               receipt.TenantID,
		BatchID:                receipt.BatchID,
		DeviceID:               receipt.DeviceID,
		TripID:                 receipt.TripID,
		InstallationID:         receipt.InstallationID,
		ConsentRevisionID:      receipt.ConsentRevisionID,
		ClientBatchID:          receipt.ClientBatchID,
		PayloadSchemaVersion:   receipt.PayloadSchemaVersion,
		BodyHash:               receipt.BodyHash,
		ObjectPath:             receipt.ObjectPath,
		ObjectSHA256:           receipt.ObjectSHA256,
		ObjectCRC32C:           int64(receipt.ObjectCRC32C),
		ObjectSize:             receipt.ObjectSize,
		ObjectGeneration:       receipt.ObjectGeneration,
		ObjectMetageneration:   receipt.ObjectMetageneration,
		ManifestPath:           receipt.ManifestPath,
		ManifestSHA256:         receipt.ManifestSHA256,
		ManifestCRC32C:         int64(receipt.ManifestCRC32C),
		ManifestSize:           receipt.ManifestSize,
		ManifestGeneration:     receipt.ManifestGeneration,
		ManifestMetageneration: receipt.ManifestMetageneration,
		ExpectedSampleCount:    receipt.ExpectedSampleCount,
		SampleCount:            receipt.SampleCount,
		FirstCapturedAt:        receipt.FirstCapturedAt,
		LastCapturedAt:         receipt.LastCapturedAt,
		ValidatorVersion:       receipt.ValidatorVersion,
		State:                  receipt.State,
		RejectionCode:          receipt.RejectionCode,
		FencingToken:           receipt.FencingToken,
		LeaseOwnerID:           receipt.LeaseOwnerID,
		LeaseOwnerKind:         receipt.LeaseOwnerKind,
		LeaseAcquiredAt:        receipt.LeaseAcquiredAt,
		LeaseHeartbeatAt:       receipt.LeaseHeartbeatAt,
		LeaseExpiresAt:         receipt.LeaseExpiresAt,
		RecoveryAttemptCount:   receipt.RecoveryAttemptCount,
		NextRecoveryAt:         receipt.NextRecoveryAt,
		LastRecoveryCode:       receipt.LastRecoveryCode,
		CleanupQuiescenceUntil: receipt.CleanupQuiescenceUntil,
		CleanupMode:            receipt.CleanupMode,
		CleanupOriginStatus:    receipt.CleanupOriginStatus,
		Revision:               receipt.Revision,
		CreatedAt:              receipt.CreatedAt,
		UpdatedAt:              receipt.UpdatedAt,
		ReservationDeadline:    receipt.ReservationDeadline,
		ArtifactExpiresAt:      receipt.ArtifactExpiresAt,
		ReceiptRetentionFloor:  receipt.ReceiptRetentionFloor,
		PurgeEligibleAt:        receipt.PurgeEligibleAt,
	}
}

func admissionStoredReceiptData(reservation ingest.Reservation) ingest.StoredReceiptData {
	receivedAt := reservation.CreatedAt.UTC()
	return ingest.StoredReceiptData{
		Artifacts: ingest.StoredBatchArtifacts{
			Object: ingest.StoredArtifact{
				Path: fmt.Sprintf(
					"telemetry/v2/tenants/%s/devices/%s/trips/%s/year=%04d/month=%02d/day=%02d/%s.json.gz",
					reservation.TenantID,
					reservation.DeviceID,
					reservation.TripID,
					receivedAt.Year(),
					receivedAt.Month(),
					receivedAt.Day(),
					reservation.BatchID,
				),
				SHA256:         strings.Repeat("a", 64),
				CRC32C:         0x12345678,
				Size:           2048,
				Generation:     1700000000000001,
				Metageneration: 1,
			},
			Manifest: ingest.StoredArtifact{
				Path: fmt.Sprintf(
					"telemetry-manifests/v2/tenants/%s/trips/%s/year=%04d/month=%02d/day=%02d/%s.manifest.json",
					reservation.TenantID,
					reservation.TripID,
					receivedAt.Year(),
					receivedAt.Month(),
					receivedAt.Day(),
					reservation.BatchID,
				),
				SHA256:         strings.Repeat("b", 64),
				CRC32C:         0x87654321,
				Size:           1024,
				Generation:     1700000000000002,
				Metageneration: 1,
			},
		},
		SampleCount: 42,
	}
}

func applyAdmissionStoredReceiptData(receipt *ingest.Receipt, stored ingest.StoredReceiptData) {
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

func admissionReplayRequest(createdAt, requestAt time.Time) (ingest.BatchScope, ingest.Reservation) {
	stable := admissionTestReservation(createdAt)
	reservation := admissionTestReservation(requestAt)
	reservation.ExpectedSampleCount = stable.ExpectedSampleCount
	reservation.FirstCapturedAt = stable.FirstCapturedAt
	reservation.LastCapturedAt = stable.LastCapturedAt
	reservation.ValidatorVersion = stable.ValidatorVersion
	scope := admissionTestScope(requestAt)
	scope.ExpectedSampleCount = stable.ExpectedSampleCount
	scope.FirstCapturedAt = stable.FirstCapturedAt
	scope.LastCapturedAt = stable.LastCapturedAt
	return scope, reservation
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
		got.ExpectedSampleCount != reservation.ExpectedSampleCount ||
		!got.FirstCapturedAt.Equal(reservation.FirstCapturedAt) ||
		!got.LastCapturedAt.Equal(reservation.LastCapturedAt) ||
		got.ValidatorVersion != reservation.ValidatorVersion ||
		!got.CreatedAt.Equal(reservation.CreatedAt) ||
		!got.ReservationDeadline.Equal(reservation.ReservationDeadline) ||
		!got.ArtifactExpiresAt.Equal(reservation.ArtifactExpiresAt) ||
		!got.ReceiptRetentionFloor.Equal(reservation.ReceiptRetentionFloor) {
		t.Fatalf("receipt lineage = %#v, want reservation %#v", got, reservation)
	}
}

func assertAdmissionStoredReceiptData(
	t *testing.T,
	receipt ingest.Receipt,
	want ingest.StoredReceiptData,
) {
	t.Helper()
	got := ingest.StoredReceiptData{
		Artifacts: ingest.StoredBatchArtifacts{
			Object: ingest.StoredArtifact{
				Path: receipt.ObjectPath, SHA256: receipt.ObjectSHA256,
				CRC32C: receipt.ObjectCRC32C, Size: receipt.ObjectSize,
				Generation: receipt.ObjectGeneration, Metageneration: receipt.ObjectMetageneration,
			},
			Manifest: ingest.StoredArtifact{
				Path: receipt.ManifestPath, SHA256: receipt.ManifestSHA256,
				CRC32C: receipt.ManifestCRC32C, Size: receipt.ManifestSize,
				Generation: receipt.ManifestGeneration, Metageneration: receipt.ManifestMetageneration,
			},
		},
		SampleCount: receipt.SampleCount,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored receipt data = %#v, want %#v", got, want)
	}
}

func assertAdmissionReceiptArtifactsEmpty(t *testing.T, receipt ingest.Receipt) {
	t.Helper()
	if receipt.ObjectPath != "" || receipt.ObjectSHA256 != "" || receipt.ObjectCRC32C != 0 ||
		receipt.ObjectSize != 0 || receipt.ObjectGeneration != 0 || receipt.ObjectMetageneration != 0 ||
		receipt.ManifestPath != "" || receipt.ManifestSHA256 != "" || receipt.ManifestCRC32C != 0 ||
		receipt.ManifestSize != 0 || receipt.ManifestGeneration != 0 || receipt.ManifestMetageneration != 0 ||
		receipt.SampleCount != 0 {
		t.Fatalf("receipt has unexpected artifact data: %#v", receipt)
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
		want["object_sha256"] = receipt.ObjectSHA256
		want["object_crc32c"] = int64(receipt.ObjectCRC32C)
		want["object_size"] = receipt.ObjectSize
		want["object_generation"] = receipt.ObjectGeneration
		want["object_metageneration"] = receipt.ObjectMetageneration
		want["manifest_path"] = receipt.ManifestPath
		want["manifest_sha256"] = receipt.ManifestSHA256
		want["manifest_crc32c"] = int64(receipt.ManifestCRC32C)
		want["manifest_size"] = receipt.ManifestSize
		want["manifest_generation"] = receipt.ManifestGeneration
		want["manifest_metageneration"] = receipt.ManifestMetageneration
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
