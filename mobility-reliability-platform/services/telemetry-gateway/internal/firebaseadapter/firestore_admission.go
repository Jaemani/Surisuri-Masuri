package firebaseadapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/cleanupattest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	maxAdmissionTransactionTimeout = 10 * time.Second
	maxAdmissionClockSkew          = 5 * time.Second
)

type authorizationRead struct {
	Snapshot authorization.Snapshot
	ReadTime time.Time
}

type receiptRead struct {
	Receipt  firestoreIngestReceipt
	ReadTime time.Time
}

type admissionTransaction interface {
	LoadAuthorization(context.Context, ingest.Principal, ingest.BatchScope) (authorizationRead, error)
	ReadIndex(context.Context, string) (firestoreIngestIndex, bool, error)
	ReadReceipt(context.Context, string) (receiptRead, bool, error)
	Create(context.Context, string, any) error
	Update(context.Context, string, []firestore.Update) error
}

type runAdmissionTransaction func(
	context.Context,
	func(context.Context, admissionTransaction) error,
) error

// FirestoreAdmissionStore keeps authorization, the two uniqueness indexes and
// the initial receipt in one Firestore transaction. Object storage deliberately
// remains outside this boundary and runs only after the transaction commits.
type FirestoreAdmissionStore struct {
	runTransaction                      runAdmissionTransaction
	now                                 func() time.Time
	cleanupAbsenceAuditEvidenceVerifier cleanupattest.Verifier
	cleanupAbsenceAuditContext          func(context.Context, time.Time) (context.Context, context.CancelFunc)
	cleanupArtifactExecutionContext     func(context.Context, time.Time) (context.Context, context.CancelFunc)
}

var _ ingest.AdmissionStore = (*FirestoreAdmissionStore)(nil)
var _ ingest.RecoveryLeaseStore = (*FirestoreAdmissionStore)(nil)
var _ ingest.CleanupTransitionStore = (*FirestoreAdmissionStore)(nil)

func NewFirestoreAdmissionStore(
	client *firestore.Client,
	timeout time.Duration,
	now func() time.Time,
) (*FirestoreAdmissionStore, error) {
	if client == nil {
		return nil, errors.New("Firestore client is required")
	}
	if timeout <= 0 || timeout > maxAdmissionTransactionTimeout {
		return nil, errors.New("Firestore admission timeout must be greater than zero and at most ten seconds")
	}
	if now == nil {
		now = time.Now
	}
	runner := func(
		ctx context.Context,
		operation func(context.Context, admissionTransaction) error,
	) error {
		transactionContext, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return client.RunTransaction(
			transactionContext,
			func(runContext context.Context, transaction *firestore.Transaction) error {
				return operation(runContext, firestoreAdmissionTransaction{
					client:      client,
					transaction: transaction,
				})
			},
		)
	}
	return &FirestoreAdmissionStore{runTransaction: runner, now: now}, nil
}

func (s *FirestoreAdmissionStore) AuthorizeAndReserve(
	ctx context.Context,
	principal ingest.Principal,
	scope ingest.BatchScope,
	reservation ingest.Reservation,
	leaseProposal ingest.LeaseProposal,
) (ingest.Receipt, ingest.LeaseGrant, ingest.ReservationStatus, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil {
		return ingest.Receipt{}, ingest.LeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}
	paths, err := admissionDocumentPaths(reservation)
	if err != nil {
		return ingest.Receipt{}, ingest.LeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}
	if ingest.ValidateLeaseProposal(leaseProposal) != nil || leaseProposal.Owner.Kind != ingest.LeaseOwnerRequest {
		return ingest.Receipt{}, ingest.LeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}
	if reservation.TenantID != scope.TenantID ||
		reservation.DeviceID != scope.DeviceID ||
		reservation.TripID != scope.TripID ||
		reservation.InstallationID != scope.InstallationID ||
		reservation.ConsentRevisionID != scope.ConsentRevisionID ||
		reservation.ExpectedSampleCount != scope.ExpectedSampleCount ||
		!reservation.FirstCapturedAt.Equal(scope.FirstCapturedAt) ||
		!reservation.LastCapturedAt.Equal(scope.LastCapturedAt) {
		return ingest.Receipt{}, ingest.LeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}

	var (
		resultReceipt ingest.Receipt
		resultLease   ingest.LeaseGrant
		resultStatus  ingest.ReservationStatus
	)
	err = s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		// Firestore may retry this callback. Reset result state and reevaluate the
		// current authorization snapshot on every attempt.
		resultReceipt = ingest.Receipt{}
		resultLease = ingest.LeaseGrant{}
		resultStatus = ""

		applicationNow := s.now().UTC()
		if applicationNow.IsZero() {
			return ingest.ErrAdmissionUnavailable
		}
		authorizationResult, loadErr := transaction.LoadAuthorization(runContext, principal, scope)
		if loadErr != nil {
			if errors.Is(loadErr, authorization.ErrSnapshotNotFound) {
				return ingest.ErrBatchUnauthorized
			}
			return normalizeAdmissionError(runContext, loadErr)
		}
		now, clockErr := conservativeAcceptanceTime(applicationNow, authorizationResult.ReadTime)
		if clockErr != nil {
			return ingest.ErrAdmissionUnavailable
		}
		if evaluationErr := authorization.EvaluateSnapshot(principal, scope, authorizationResult.Snapshot, now); evaluationErr != nil {
			if errors.Is(evaluationErr, ingest.ErrBatchUnauthorized) {
				return ingest.ErrBatchUnauthorized
			}
			return ingest.ErrAdmissionUnavailable
		}

		idempotencyIndex, hasIdempotencyIndex, readErr := transaction.ReadIndex(runContext, paths.idempotency)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		clientBatchIndex, hasClientBatchIndex, readErr := transaction.ReadIndex(runContext, paths.clientBatch)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}

		switch {
		case !hasIdempotencyIndex && !hasClientBatchIndex:
			if now.Before(reservation.CreatedAt) {
				return ingest.ErrAdmissionUnavailable
			}
			leaseExpiresAt := now.Add(leaseProposal.Duration)
			if leaseExpiresAt.After(reservation.ReservationDeadline) {
				return ingest.ErrAdmissionUnavailable
			}
			index := newFirestoreIngestIndex(reservation)
			receipt := newFirestoreIngestReceipt(reservation, leaseProposal.Owner, now, leaseExpiresAt)
			if validateReceiptLinkage(receipt, index) != nil || validateReceiptState(receipt) != nil {
				return ingest.ErrAdmissionUnavailable
			}
			// All reads above complete before the first create.
			if createErr := transaction.Create(runContext, paths.idempotency, index); createErr != nil {
				return normalizeAdmissionError(runContext, createErr)
			}
			if createErr := transaction.Create(runContext, paths.clientBatch, index); createErr != nil {
				return normalizeAdmissionError(runContext, createErr)
			}
			if createErr := transaction.Create(runContext, paths.receipt, receipt); createErr != nil {
				return normalizeAdmissionError(runContext, createErr)
			}
			resultReceipt = receipt.toDomain()
			resultLease = receipt.leaseGrant()
			resultStatus = ingest.ReservationCreatedLeaseAcquired
			return nil

		case hasIdempotencyIndex && !hasClientBatchIndex:
			return ingest.ErrAdmissionUnavailable

		case !hasIdempotencyIndex && hasClientBatchIndex:
			if validateIndexDocument(clientBatchIndex, reservation.TenantID) != nil ||
				clientBatchIndex.ClientBatchKey != reservation.ClientBatchKey {
				return ingest.ErrAdmissionUnavailable
			}
			if clientBatchIndex.ReservationKey == reservation.ReservationKey {
				return ingest.ErrAdmissionUnavailable
			}
			linkedIdempotencyPath := idempotencyDocumentPath(
				reservation.TenantID,
				clientBatchIndex.ReservationKey,
			)
			linkedIdempotencyIndex, exists, linkedErr := transaction.ReadIndex(runContext, linkedIdempotencyPath)
			if linkedErr != nil {
				return normalizeAdmissionError(runContext, linkedErr)
			}
			if !exists || validateIndexDocument(linkedIdempotencyIndex, reservation.TenantID) != nil ||
				!sameIngestIndex(linkedIdempotencyIndex, clientBatchIndex) {
				return ingest.ErrAdmissionUnavailable
			}
			linkedReceiptPath := receiptDocumentPath(reservation.TenantID, clientBatchIndex.ReceiptID)
			linkedReceiptResult, exists, linkedErr := transaction.ReadReceipt(runContext, linkedReceiptPath)
			if linkedErr != nil {
				return normalizeAdmissionError(runContext, linkedErr)
			}
			linkedReceipt := linkedReceiptResult.Receipt
			if !exists || validateReceiptLinkage(linkedReceipt, clientBatchIndex) != nil ||
				validateReceiptState(linkedReceipt) != nil {
				return ingest.ErrAdmissionUnavailable
			}
			resultStatus = ingest.ReservationClientBatchConflict
			return nil
		}

		if validateIndexPair(idempotencyIndex, clientBatchIndex, reservation) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		receiptPath := receiptDocumentPath(reservation.TenantID, idempotencyIndex.ReceiptID)
		storedReceiptResult, exists, readErr := transaction.ReadReceipt(runContext, receiptPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		storedReceipt := storedReceiptResult.Receipt
		if !exists || validateReceiptLinkage(storedReceipt, idempotencyIndex) != nil ||
			validateReceiptState(storedReceipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		if idempotencyIndex.BodyHash != reservation.BodyHash {
			resultStatus = ingest.ReservationConflict
			return nil
		}
		if validateReceiptReservation(storedReceipt, reservation) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		resultReceipt = storedReceipt.toDomain()
		switch storedReceipt.State {
		case ingest.ReceiptReserved:
			if !withinAdmissionClockSkew(authorizationResult.ReadTime, storedReceiptResult.ReadTime) {
				return ingest.ErrAdmissionUnavailable
			}
			replayNow, clockErr := conservativeAcceptanceTime(applicationNow, storedReceiptResult.ReadTime)
			if clockErr != nil {
				return ingest.ErrAdmissionUnavailable
			}
			if replayNow.After(now) {
				now = replayNow
			}
			// Receipt reads can observe a later server time than the authorization
			// documents. Re-evaluate the same coherent snapshot at the latest
			// conservative time before returning an active replay or taking over an
			// expired lease. This closes time-based consent/trip expiry boundaries.
			if evaluationErr := authorization.EvaluateSnapshot(principal, scope, authorizationResult.Snapshot, now); evaluationErr != nil {
				if errors.Is(evaluationErr, ingest.ErrBatchUnauthorized) {
					return ingest.ErrBatchUnauthorized
				}
				return ingest.ErrAdmissionUnavailable
			}
			if !now.Before(storedReceipt.ReservationDeadline) {
				return ingest.ErrAdmissionUnavailable
			}
			if receiptHasLeaseFields(storedReceipt) && now.Before(storedReceipt.LeaseExpiresAt) {
				resultStatus = ingest.ReservationReplayInProgress
				return nil
			}
			if !receiptHasLeaseFields(storedReceipt) && now.Before(storedReceipt.NextRecoveryAt) {
				resultStatus = ingest.ReservationReplayInProgress
				return nil
			}
			if ingest.ValidateRecoveryAttemptProposal(leaseProposal.Attempt) != nil {
				return ingest.ErrAdmissionUnavailable
			}
			nextToken := storedReceipt.FencingToken + 1
			nextRevision := storedReceipt.Revision + 1
			nextAttemptCount := storedReceipt.RecoveryAttemptCount + 1
			if nextToken <= 1 || nextRevision <= storedReceipt.Revision ||
				nextAttemptCount <= storedReceipt.RecoveryAttemptCount {
				return ingest.ErrAdmissionUnavailable
			}
			priorAttemptPath, priorAttemptUpdates, attemptEffectiveAt, priorAttemptErr := expiredPriorRecoveryAttemptClosure(
				runContext,
				transaction,
				storedReceipt,
				now,
			)
			if priorAttemptErr != nil {
				return priorAttemptErr
			}
			if attemptEffectiveAt.After(now) {
				now = attemptEffectiveAt
				if evaluationErr := authorization.EvaluateSnapshot(
					principal,
					scope,
					authorizationResult.Snapshot,
					now,
				); evaluationErr != nil {
					if errors.Is(evaluationErr, ingest.ErrBatchUnauthorized) {
						return ingest.ErrBatchUnauthorized
					}
					return ingest.ErrAdmissionUnavailable
				}
			}
			leaseExpiresAt := now.Add(leaseProposal.Duration)
			if !now.Before(storedReceipt.ReservationDeadline) ||
				leaseExpiresAt.After(storedReceipt.ReservationDeadline) {
				return ingest.ErrAdmissionUnavailable
			}
			if len(priorAttemptUpdates) > 0 {
				if updateErr := transaction.Update(runContext, priorAttemptPath, priorAttemptUpdates); updateErr != nil {
					return normalizeAdmissionError(runContext, updateErr)
				}
			}
			if updateErr := transaction.Update(runContext, receiptPath, leaseTakeoverUpdates(
				leaseProposal.Owner,
				now,
				leaseExpiresAt,
				nextToken,
				nextRevision,
				nextAttemptCount,
			)); updateErr != nil {
				return normalizeAdmissionError(runContext, updateErr)
			}
			attempt := newFirestoreRecoveryAttempt(
				leaseProposal.Attempt,
				storedReceipt.TenantID,
				storedReceipt.ReceiptID,
				leaseProposal.Owner.Kind,
				nextToken,
				now,
			)
			if createErr := transaction.Create(
				runContext,
				recoveryAttemptDocumentPath(storedReceipt.TenantID, storedReceipt.ReceiptID, attempt.AttemptID),
				attempt,
			); createErr != nil {
				return normalizeAdmissionError(runContext, createErr)
			}
			storedReceipt.applyLease(leaseProposal.Owner, now, leaseExpiresAt, nextToken)
			storedReceipt.Revision = nextRevision
			storedReceipt.RecoveryAttemptCount = nextAttemptCount
			storedReceipt.UpdatedAt = now
			resultReceipt = storedReceipt.toDomain()
			resultLease = storedReceipt.leaseGrant()
			resultStatus = ingest.ReservationReplayLeaseAcquired
		case ingest.ReceiptStored, ingest.ReceiptQueued, ingest.ReceiptProjected, ingest.ReceiptDeleting, ingest.ReceiptDeleted:
			resultStatus = ingest.ReservationReplayComplete
		case ingest.ReceiptRejected:
			resultStatus = ingest.ReservationReplayRejected
		case ingest.ReceiptRecoveryHold:
			resultStatus = ingest.ReservationReplayRecoveryHold
		case ingest.ReceiptCleanupPending, ingest.ReceiptExpired:
			resultStatus = ingest.ReservationReplayExpired
		default:
			return ingest.ErrAdmissionUnavailable
		}
		return nil
	})
	if err != nil {
		return ingest.Receipt{}, ingest.LeaseGrant{}, "", normalizeAdmissionError(ctx, err)
	}
	if resultStatus == "" {
		return ingest.Receipt{}, ingest.LeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}
	return resultReceipt, resultLease, resultStatus, nil
}

func (s *FirestoreAdmissionStore) MarkStored(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	fence ingest.LeaseFence,
	stored ingest.StoredReceiptData,
	updatedAt time.Time,
) (ingest.Receipt, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(tenantID) || !lowerHexDigest(reservationKey) ||
		validateStoredReceiptData(stored, tenantID) != nil || updatedAt.IsZero() {
		return ingest.Receipt{}, ingest.ErrAdmissionUnavailable
	}
	var result ingest.Receipt
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.Receipt{}
		indexPath := idempotencyDocumentPath(tenantID, reservationKey)
		index, exists, readErr := transaction.ReadIndex(runContext, indexPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		if !exists || validateIndexDocument(index, tenantID) != nil || index.ReservationKey != reservationKey {
			return ingest.ErrAdmissionUnavailable
		}
		clientBatchIndex, exists, readErr := transaction.ReadIndex(
			runContext,
			clientBatchDocumentPath(tenantID, index.ClientBatchKey),
		)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		if !exists || validateIndexDocument(clientBatchIndex, tenantID) != nil ||
			!sameIngestIndex(index, clientBatchIndex) {
			return ingest.ErrAdmissionUnavailable
		}
		receiptPath := receiptDocumentPath(tenantID, index.ReceiptID)
		receiptResult, exists, readErr := transaction.ReadReceipt(runContext, receiptPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		receipt := receiptResult.Receipt
		if !exists || validateReceiptLinkage(receipt, index) != nil || validateReceiptState(receipt) != nil ||
			stored.Artifacts.Object.Path != expectedObjectPath(receipt) ||
			stored.Artifacts.Manifest.Path != expectedManifestPath(receipt) {
			return ingest.ErrAdmissionUnavailable
		}
		switch receipt.State {
		case ingest.ReceiptStored:
			if !sameStoredReceiptData(receipt, stored) {
				return ingest.ErrAdmissionUnavailable
			}
			result = receipt.toDomain()
			return nil
		case ingest.ReceiptReserved:
		default:
			return ingest.ErrAdmissionUnavailable
		}
		if stored.SampleCount != receipt.ExpectedSampleCount {
			return ingest.ErrAdmissionUnavailable
		}
		effectiveAt, clockErr := conservativeAcceptanceTime(updatedAt.UTC(), receiptResult.ReadTime)
		if clockErr != nil || validateForwardFence(receipt, fence, effectiveAt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		nextRevision := receipt.Revision + 1
		if nextRevision <= receipt.Revision {
			return ingest.ErrAdmissionUnavailable
		}
		if updateErr := transaction.Update(runContext, receiptPath, []firestore.Update{
			{Path: "status", Value: string(ingest.ReceiptStored)},
			{Path: "object_path", Value: stored.Artifacts.Object.Path},
			{Path: "object_sha256", Value: stored.Artifacts.Object.SHA256},
			{Path: "object_crc32c", Value: int64(stored.Artifacts.Object.CRC32C)},
			{Path: "object_size", Value: stored.Artifacts.Object.Size},
			{Path: "object_generation", Value: stored.Artifacts.Object.Generation},
			{Path: "object_metageneration", Value: stored.Artifacts.Object.Metageneration},
			{Path: "manifest_path", Value: stored.Artifacts.Manifest.Path},
			{Path: "manifest_sha256", Value: stored.Artifacts.Manifest.SHA256},
			{Path: "manifest_crc32c", Value: int64(stored.Artifacts.Manifest.CRC32C)},
			{Path: "manifest_size", Value: stored.Artifacts.Manifest.Size},
			{Path: "manifest_generation", Value: stored.Artifacts.Manifest.Generation},
			{Path: "manifest_metageneration", Value: stored.Artifacts.Manifest.Metageneration},
			{Path: "sample_count", Value: stored.SampleCount},
			{Path: "lease_owner_id", Value: firestore.Delete},
			{Path: "lease_owner_kind", Value: firestore.Delete},
			{Path: "lease_acquired_at", Value: firestore.Delete},
			{Path: "lease_heartbeat_at", Value: firestore.Delete},
			{Path: "lease_expires_at", Value: firestore.Delete},
			{Path: "next_recovery_at", Value: firestore.Delete},
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: effectiveAt},
		}); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		receipt.State = ingest.ReceiptStored
		receipt.applyStoredData(stored)
		receipt.clearLease()
		receipt.Revision = nextRevision
		receipt.UpdatedAt = effectiveAt
		result = receipt.toDomain()
		return nil
	})
	if err != nil {
		return ingest.Receipt{}, normalizeAdmissionError(ctx, err)
	}
	if result.ReceiptID == "" {
		return ingest.Receipt{}, ingest.ErrAdmissionUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) MarkRejected(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	fence ingest.LeaseFence,
	rejectionCode string,
	updatedAt time.Time,
) (ingest.Receipt, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(tenantID) || !lowerHexDigest(reservationKey) ||
		rejectionCode != "object_conflict" || updatedAt.IsZero() {
		return ingest.Receipt{}, ingest.ErrAdmissionUnavailable
	}
	var result ingest.Receipt
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.Receipt{}
		indexPath := idempotencyDocumentPath(tenantID, reservationKey)
		index, exists, readErr := transaction.ReadIndex(runContext, indexPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		if !exists || validateIndexDocument(index, tenantID) != nil || index.ReservationKey != reservationKey {
			return ingest.ErrAdmissionUnavailable
		}
		clientBatchIndex, exists, readErr := transaction.ReadIndex(
			runContext,
			clientBatchDocumentPath(tenantID, index.ClientBatchKey),
		)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		if !exists || validateIndexDocument(clientBatchIndex, tenantID) != nil ||
			!sameIngestIndex(index, clientBatchIndex) {
			return ingest.ErrAdmissionUnavailable
		}
		receiptPath := receiptDocumentPath(tenantID, index.ReceiptID)
		receiptResult, exists, readErr := transaction.ReadReceipt(runContext, receiptPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		receipt := receiptResult.Receipt
		if !exists || validateReceiptLinkage(receipt, index) != nil || validateReceiptState(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		switch receipt.State {
		case ingest.ReceiptRejected:
			if receipt.RejectionCode != rejectionCode {
				return ingest.ErrAdmissionUnavailable
			}
			result = receipt.toDomain()
			return nil
		case ingest.ReceiptReserved:
		default:
			return ingest.ErrAdmissionUnavailable
		}
		effectiveAt, clockErr := conservativeAcceptanceTime(updatedAt.UTC(), receiptResult.ReadTime)
		if clockErr != nil || validateForwardFence(receipt, fence, effectiveAt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		nextRevision := receipt.Revision + 1
		if nextRevision <= receipt.Revision {
			return ingest.ErrAdmissionUnavailable
		}
		if updateErr := transaction.Update(runContext, receiptPath, []firestore.Update{
			{Path: "status", Value: string(ingest.ReceiptRejected)},
			{Path: "rejection_code", Value: rejectionCode},
			{Path: "lease_owner_id", Value: firestore.Delete},
			{Path: "lease_owner_kind", Value: firestore.Delete},
			{Path: "lease_acquired_at", Value: firestore.Delete},
			{Path: "lease_heartbeat_at", Value: firestore.Delete},
			{Path: "lease_expires_at", Value: firestore.Delete},
			{Path: "next_recovery_at", Value: firestore.Delete},
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: effectiveAt},
		}); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		receipt.State = ingest.ReceiptRejected
		receipt.RejectionCode = rejectionCode
		receipt.clearLease()
		receipt.Revision = nextRevision
		receipt.UpdatedAt = effectiveAt
		result = receipt.toDomain()
		return nil
	})
	if err != nil {
		return ingest.Receipt{}, normalizeAdmissionError(ctx, err)
	}
	if result.ReceiptID == "" {
		return ingest.Receipt{}, ingest.ErrAdmissionUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) ClaimRecoveryLease(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	owner ingest.LeaseOwner,
	attemptProposal ingest.RecoveryAttemptProposal,
	requestedAt time.Time,
	duration time.Duration,
) (ingest.LeaseGrant, ingest.LeaseStatus, error) {
	proposal := ingest.LeaseProposal{Owner: owner, Duration: duration, Attempt: attemptProposal}
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(tenantID) || !lowerHexDigest(reservationKey) || requestedAt.IsZero() ||
		ingest.ValidateLeaseProposal(proposal) != nil || owner.Kind != ingest.LeaseOwnerSweeper ||
		ingest.ValidateRecoveryAttemptProposal(attemptProposal) != nil {
		return ingest.LeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}
	var resultGrant ingest.LeaseGrant
	var resultStatus ingest.LeaseStatus
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		resultGrant = ingest.LeaseGrant{}
		resultStatus = ""
		linked, loadErr := loadLinkedReceipt(runContext, transaction, tenantID, reservationKey)
		if loadErr != nil {
			return loadErr
		}
		receipt := linked.Receipt.Receipt
		if receipt.State != ingest.ReceiptReserved {
			resultStatus = ingest.LeaseStatusNotEligible
			return nil
		}
		effectiveAt, clockErr := conservativeAcceptanceTime(requestedAt.UTC(), linked.Receipt.ReadTime)
		if clockErr != nil {
			return ingest.ErrAdmissionUnavailable
		}
		if !effectiveAt.Before(receipt.ReservationDeadline) {
			resultStatus = ingest.LeaseStatusDeadlineElapsed
			return nil
		}
		if receiptHasLeaseFields(receipt) && effectiveAt.Before(receipt.LeaseExpiresAt) {
			resultStatus = ingest.LeaseStatusHeld
			return nil
		}
		if !receiptHasLeaseFields(receipt) && effectiveAt.Before(receipt.NextRecoveryAt) {
			resultStatus = ingest.LeaseStatusNotDue
			return nil
		}
		nextToken := receipt.FencingToken + 1
		nextRevision := receipt.Revision + 1
		nextAttemptCount := receipt.RecoveryAttemptCount + 1
		if nextToken <= receipt.FencingToken || nextRevision <= receipt.Revision ||
			nextAttemptCount <= receipt.RecoveryAttemptCount {
			return ingest.ErrAdmissionUnavailable
		}
		priorAttemptPath, priorAttemptUpdates, attemptEffectiveAt, priorAttemptErr := expiredPriorRecoveryAttemptClosure(
			runContext,
			transaction,
			receipt,
			effectiveAt,
		)
		if priorAttemptErr != nil {
			return priorAttemptErr
		}
		effectiveAt = attemptEffectiveAt
		leaseExpiresAt := effectiveAt.Add(duration)
		if !effectiveAt.Before(receipt.ReservationDeadline) ||
			leaseExpiresAt.After(receipt.ReservationDeadline) {
			return ingest.ErrAdmissionUnavailable
		}
		if len(priorAttemptUpdates) > 0 {
			if updateErr := transaction.Update(runContext, priorAttemptPath, priorAttemptUpdates); updateErr != nil {
				return normalizeAdmissionError(runContext, updateErr)
			}
		}
		if updateErr := transaction.Update(runContext, linked.ReceiptPath, leaseTakeoverUpdates(
			owner,
			effectiveAt,
			leaseExpiresAt,
			nextToken,
			nextRevision,
			nextAttemptCount,
		)); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		attempt := newFirestoreRecoveryAttempt(
			attemptProposal,
			receipt.TenantID,
			receipt.ReceiptID,
			owner.Kind,
			nextToken,
			effectiveAt,
		)
		if createErr := transaction.Create(
			runContext,
			recoveryAttemptDocumentPath(receipt.TenantID, receipt.ReceiptID, attempt.AttemptID),
			attempt,
		); createErr != nil {
			return normalizeAdmissionError(runContext, createErr)
		}
		receipt.applyLease(owner, effectiveAt, leaseExpiresAt, nextToken)
		receipt.Revision = nextRevision
		receipt.RecoveryAttemptCount = nextAttemptCount
		receipt.UpdatedAt = effectiveAt
		resultGrant = receipt.leaseGrant()
		resultStatus = ingest.LeaseStatusAcquired
		return nil
	})
	if err != nil {
		return ingest.LeaseGrant{}, "", normalizeAdmissionError(ctx, err)
	}
	if resultStatus == "" {
		return ingest.LeaseGrant{}, "", ingest.ErrAdmissionUnavailable
	}
	return resultGrant, resultStatus, nil
}

func (s *FirestoreAdmissionStore) RenewLease(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	fence ingest.LeaseFence,
	renewedAt time.Time,
	duration time.Duration,
) (ingest.LeaseGrant, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(tenantID) || !lowerHexDigest(reservationKey) ||
		ingest.ValidateLeaseFence(fence) != nil || renewedAt.IsZero() ||
		duration < ingest.MinLeaseDuration || duration > ingest.MaxLeaseDuration {
		return ingest.LeaseGrant{}, ingest.ErrAdmissionUnavailable
	}
	var result ingest.LeaseGrant
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.LeaseGrant{}
		linked, loadErr := loadLinkedReceipt(runContext, transaction, tenantID, reservationKey)
		if loadErr != nil {
			return loadErr
		}
		receipt := linked.Receipt.Receipt
		if receipt.State != ingest.ReceiptReserved {
			return ingest.ErrAdmissionUnavailable
		}
		effectiveAt, clockErr := conservativeAcceptanceTime(renewedAt.UTC(), linked.Receipt.ReadTime)
		if clockErr != nil || validateForwardFence(receipt, fence, effectiveAt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		remaining := receipt.LeaseExpiresAt.Sub(effectiveAt)
		if remaining > ingest.LeaseRenewalWindow {
			return ingest.ErrAdmissionUnavailable
		}
		leaseExpiresAt := effectiveAt.Add(duration)
		if !leaseExpiresAt.After(receipt.LeaseExpiresAt) || leaseExpiresAt.After(receipt.ReservationDeadline) {
			return ingest.ErrAdmissionUnavailable
		}
		nextRevision := receipt.Revision + 1
		if nextRevision <= receipt.Revision {
			return ingest.ErrAdmissionUnavailable
		}
		if updateErr := transaction.Update(runContext, linked.ReceiptPath, []firestore.Update{
			{Path: "lease_heartbeat_at", Value: effectiveAt},
			{Path: "lease_expires_at", Value: leaseExpiresAt},
			{Path: "next_recovery_at", Value: leaseExpiresAt},
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: effectiveAt},
		}); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		receipt.LeaseHeartbeatAt = effectiveAt
		receipt.LeaseExpiresAt = leaseExpiresAt
		receipt.NextRecoveryAt = leaseExpiresAt
		receipt.Revision = nextRevision
		receipt.UpdatedAt = effectiveAt
		result = receipt.leaseGrant()
		if ingest.ValidateLeaseGrant(result) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		return nil
	})
	if err != nil {
		return ingest.LeaseGrant{}, normalizeAdmissionError(ctx, err)
	}
	if ingest.ValidateLeaseGrant(result) != nil {
		return ingest.LeaseGrant{}, ingest.ErrAdmissionUnavailable
	}
	return result, nil
}

func (s *FirestoreAdmissionStore) BeginCleanupTransition(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	requestedAt time.Time,
) (ingest.Receipt, ingest.TransitionStatus, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(tenantID) || !lowerHexDigest(reservationKey) || requestedAt.IsZero() ||
		ingest.ValidateCleanupTimingPolicy() != nil {
		return ingest.Receipt{}, "", ingest.ErrAdmissionUnavailable
	}
	var resultReceipt ingest.Receipt
	var resultStatus ingest.TransitionStatus
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		resultReceipt = ingest.Receipt{}
		resultStatus = ""
		linked, loadErr := loadLinkedReceipt(runContext, transaction, tenantID, reservationKey)
		if loadErr != nil {
			return loadErr
		}
		receipt := linked.Receipt.Receipt
		if receipt.State == ingest.ReceiptCleanupPending &&
			receipt.CleanupMode == ingest.CleanupModeReservationExpiry &&
			receipt.CleanupOriginStatus == ingest.ReceiptReserved {
			resultReceipt = receipt.toDomain()
			resultStatus = ingest.TransitionStatusAlreadyStarted
			return nil
		}
		if receipt.State != ingest.ReceiptReserved {
			resultStatus = ingest.TransitionStatusNotEligible
			return nil
		}
		cleanupAt, clockErr := conservativeCleanupTime(requestedAt.UTC(), linked.Receipt.ReadTime)
		if clockErr != nil || cleanupAt.Before(receipt.UpdatedAt) {
			return ingest.ErrAdmissionUnavailable
		}
		if cleanupAt.Before(receipt.ReservationDeadline) {
			resultReceipt = receipt.toDomain()
			resultStatus = ingest.TransitionStatusNotReady
			return nil
		}
		if receiptHasLeaseFields(receipt) && cleanupAt.Before(receipt.LeaseExpiresAt) {
			resultReceipt = receipt.toDomain()
			resultStatus = ingest.TransitionStatusNotReady
			return nil
		}
		priorAttemptPath, priorAttemptUpdates, attemptEffectiveAt, priorAttemptErr :=
			expiredPriorRecoveryAttemptClosureForCleanup(
				runContext,
				transaction,
				receipt,
				requestedAt.UTC(),
				linked.Receipt.ReadTime,
			)
		if priorAttemptErr != nil {
			return priorAttemptErr
		}
		cleanupAt = attemptEffectiveAt
		if cleanupAt.Before(receipt.ReservationDeadline) ||
			(receiptHasLeaseFields(receipt) && cleanupAt.Before(receipt.LeaseExpiresAt)) {
			resultReceipt = receipt.toDomain()
			resultStatus = ingest.TransitionStatusNotReady
			return nil
		}
		quiescenceBase := cleanupAt
		if receipt.LeaseExpiresAt.After(quiescenceBase) {
			quiescenceBase = receipt.LeaseExpiresAt
		}
		quiescenceUntil := quiescenceBase.Add(ingest.DefaultCleanupLateWriteGrace)
		nextToken := receipt.FencingToken + 1
		nextRevision := receipt.Revision + 1
		if nextToken <= receipt.FencingToken || nextRevision <= receipt.Revision ||
			!quiescenceUntil.After(cleanupAt) {
			return ingest.ErrAdmissionUnavailable
		}
		if len(priorAttemptUpdates) > 0 {
			if updateErr := transaction.Update(runContext, priorAttemptPath, priorAttemptUpdates); updateErr != nil {
				return normalizeAdmissionError(runContext, updateErr)
			}
		}
		if updateErr := transaction.Update(runContext, linked.ReceiptPath, []firestore.Update{
			{Path: "status", Value: string(ingest.ReceiptCleanupPending)},
			{Path: "fencing_token", Value: nextToken},
			{Path: "lease_owner_id", Value: firestore.Delete},
			{Path: "lease_owner_kind", Value: firestore.Delete},
			{Path: "lease_acquired_at", Value: firestore.Delete},
			{Path: "lease_heartbeat_at", Value: firestore.Delete},
			{Path: "lease_expires_at", Value: firestore.Delete},
			{Path: "next_recovery_at", Value: firestore.Delete},
			{Path: "cleanup_transitioned_at", Value: cleanupAt},
			{Path: "cleanup_quiescence_until", Value: quiescenceUntil},
			{Path: "cleanup_mode", Value: string(ingest.CleanupModeReservationExpiry)},
			{Path: "cleanup_origin_status", Value: string(ingest.ReceiptReserved)},
			{Path: "cleanup_policy_version", Value: ingest.CleanupTransitionPolicyV1},
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: cleanupAt},
		}); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		receipt.State = ingest.ReceiptCleanupPending
		receipt.FencingToken = nextToken
		receipt.clearLease()
		receipt.CleanupTransitionedAt = cleanupAt
		receipt.CleanupQuiescenceUntil = quiescenceUntil
		receipt.CleanupMode = ingest.CleanupModeReservationExpiry
		receipt.CleanupOriginStatus = ingest.ReceiptReserved
		receipt.CleanupPolicyVersion = ingest.CleanupTransitionPolicyV1
		receipt.Revision = nextRevision
		receipt.UpdatedAt = cleanupAt
		if validateReceiptState(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		resultReceipt = receipt.toDomain()
		resultStatus = ingest.TransitionStatusStarted
		return nil
	})
	if err != nil {
		return ingest.Receipt{}, "", normalizeAdmissionError(ctx, err)
	}
	if resultStatus == "" {
		return ingest.Receipt{}, "", ingest.ErrAdmissionUnavailable
	}
	return resultReceipt, resultStatus, nil
}

func (s *FirestoreAdmissionStore) ReleaseLease(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	fence ingest.LeaseFence,
	releasedAt time.Time,
	code ingest.LeaseReleaseCode,
) error {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(tenantID) || !lowerHexDigest(reservationKey) ||
		ingest.ValidateLeaseFence(fence) != nil || releasedAt.IsZero() ||
		!ingest.ValidLeaseReleaseCode(code) {
		return ingest.ErrAdmissionUnavailable
	}
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		indexPath := idempotencyDocumentPath(tenantID, reservationKey)
		index, exists, readErr := transaction.ReadIndex(runContext, indexPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		if !exists || validateIndexDocument(index, tenantID) != nil || index.ReservationKey != reservationKey {
			return ingest.ErrAdmissionUnavailable
		}
		clientBatchIndex, exists, readErr := transaction.ReadIndex(
			runContext,
			clientBatchDocumentPath(tenantID, index.ClientBatchKey),
		)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		if !exists || validateIndexDocument(clientBatchIndex, tenantID) != nil ||
			!sameIngestIndex(index, clientBatchIndex) {
			return ingest.ErrAdmissionUnavailable
		}
		receiptPath := receiptDocumentPath(tenantID, index.ReceiptID)
		receiptResult, exists, readErr := transaction.ReadReceipt(runContext, receiptPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		receipt := receiptResult.Receipt
		if !exists || validateReceiptLinkage(receipt, index) != nil ||
			validateReceiptState(receipt) != nil || receipt.State != ingest.ReceiptReserved {
			return ingest.ErrAdmissionUnavailable
		}
		if !receiptHasLeaseFields(receipt) {
			return ingest.ErrAdmissionUnavailable
		}
		effectiveAt, clockErr := conservativeAcceptanceTime(releasedAt.UTC(), receiptResult.ReadTime)
		if clockErr != nil || validateForwardFence(receipt, fence, effectiveAt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		nextRevision := receipt.Revision + 1
		if nextRevision <= receipt.Revision {
			return ingest.ErrAdmissionUnavailable
		}
		nextRecoveryAt := effectiveAt.Add(ingest.InitialRecoveryBackoff)
		if !nextRecoveryAt.Before(receipt.ReservationDeadline) {
			nextRecoveryAt = receipt.ReservationDeadline
		}
		if updateErr := transaction.Update(runContext, receiptPath, []firestore.Update{
			{Path: "lease_owner_id", Value: firestore.Delete},
			{Path: "lease_owner_kind", Value: firestore.Delete},
			{Path: "lease_acquired_at", Value: firestore.Delete},
			{Path: "lease_heartbeat_at", Value: firestore.Delete},
			{Path: "lease_expires_at", Value: firestore.Delete},
			{Path: "next_recovery_at", Value: nextRecoveryAt},
			{Path: "last_recovery_code", Value: string(code)},
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: effectiveAt},
		}); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		return nil
	})
	return normalizeAdmissionError(ctx, err)
}

type linkedReceiptRead struct {
	Index       firestoreIngestIndex
	Receipt     receiptRead
	ReceiptPath string
}

func loadLinkedReceipt(
	ctx context.Context,
	transaction admissionTransaction,
	tenantID string,
	reservationKey string,
) (linkedReceiptRead, error) {
	indexPath := idempotencyDocumentPath(tenantID, reservationKey)
	index, exists, err := transaction.ReadIndex(ctx, indexPath)
	if err != nil {
		return linkedReceiptRead{}, normalizeAdmissionError(ctx, err)
	}
	if !exists || validateIndexDocument(index, tenantID) != nil || index.ReservationKey != reservationKey {
		return linkedReceiptRead{}, ingest.ErrAdmissionUnavailable
	}
	clientBatchIndex, exists, err := transaction.ReadIndex(
		ctx,
		clientBatchDocumentPath(tenantID, index.ClientBatchKey),
	)
	if err != nil {
		return linkedReceiptRead{}, normalizeAdmissionError(ctx, err)
	}
	if !exists || validateIndexDocument(clientBatchIndex, tenantID) != nil ||
		!sameIngestIndex(index, clientBatchIndex) {
		return linkedReceiptRead{}, ingest.ErrAdmissionUnavailable
	}
	receiptPath := receiptDocumentPath(tenantID, index.ReceiptID)
	receipt, exists, err := transaction.ReadReceipt(ctx, receiptPath)
	if err != nil {
		return linkedReceiptRead{}, normalizeAdmissionError(ctx, err)
	}
	if !exists || validateReceiptLinkage(receipt.Receipt, index) != nil ||
		validateReceiptState(receipt.Receipt) != nil {
		return linkedReceiptRead{}, ingest.ErrAdmissionUnavailable
	}
	return linkedReceiptRead{Index: index, Receipt: receipt, ReceiptPath: receiptPath}, nil
}

type admissionPaths struct {
	idempotency string
	clientBatch string
	receipt     string
}

func admissionDocumentPaths(reservation ingest.Reservation) (admissionPaths, error) {
	if validateReservation(reservation) != nil {
		return admissionPaths{}, ingest.ErrAdmissionUnavailable
	}
	return admissionPaths{
		idempotency: idempotencyDocumentPath(reservation.TenantID, reservation.ReservationKey),
		clientBatch: clientBatchDocumentPath(reservation.TenantID, reservation.ClientBatchKey),
		receipt:     receiptDocumentPath(reservation.TenantID, reservation.ReceiptID),
	}, nil
}

func idempotencyDocumentPath(tenantID, reservationKey string) string {
	return "tenants/" + tenantID + "/ingestIdempotency/" + reservationKey
}

func clientBatchDocumentPath(tenantID, clientBatchKey string) string {
	return "tenants/" + tenantID + "/ingestClientBatches/" + clientBatchKey
}

func receiptDocumentPath(tenantID, receiptID string) string {
	return "tenants/" + tenantID + "/ingestReceipts/" + receiptID
}

func recoveryAttemptDocumentPath(tenantID, receiptID, attemptID string) string {
	return receiptDocumentPath(tenantID, receiptID) + "/recoveryAttempts/" + attemptID
}

func validateReservation(reservation ingest.Reservation) error {
	for _, identifier := range []string{
		reservation.TenantID,
		reservation.BatchID,
		reservation.ReceiptID,
		reservation.DeviceID,
		reservation.TripID,
		reservation.InstallationID,
		reservation.ConsentRevisionID,
		reservation.ClientBatchID,
	} {
		if !telemetry.IsUUID(identifier) {
			return ingest.ErrAdmissionUnavailable
		}
	}
	if reservation.ReceiptID != reservation.BatchID ||
		!lowerHexDigest(reservation.ReservationKey) ||
		!lowerHexDigest(reservation.ClientBatchKey) ||
		!lowerHexDigest(reservation.BodyHash) ||
		reservation.ReservationKey != ingest.DeriveReservationKey(
			reservation.PayloadSchemaVersion,
			reservation.TenantID,
			reservation.InstallationID,
			reservation.ClientBatchID,
		) ||
		reservation.ClientBatchKey != ingest.DeriveClientBatchKey(
			reservation.TenantID,
			reservation.ClientBatchID,
		) ||
		reservation.PayloadSchemaVersion != telemetry.SchemaVersionV2 ||
		reservation.ExpectedSampleCount <= 0 || reservation.ExpectedSampleCount > telemetry.MaxSamples ||
		reservation.FirstCapturedAt.IsZero() || reservation.LastCapturedAt.IsZero() ||
		reservation.LastCapturedAt.Before(reservation.FirstCapturedAt) ||
		reservation.ValidatorVersion != ingest.TelemetryValidatorVersion ||
		reservation.CreatedAt.IsZero() || reservation.ReservationDeadline.IsZero() ||
		reservation.ArtifactExpiresAt.IsZero() || reservation.ReceiptRetentionFloor.IsZero() ||
		!reservation.ReservationDeadline.Equal(reservation.CreatedAt.Add(ingest.ReservationProcessingWindow)) ||
		!reservation.ArtifactExpiresAt.Equal(reservation.CreatedAt.Add(ingest.TelemetryArtifactRetention)) ||
		!reservation.ReceiptRetentionFloor.Equal(reservation.CreatedAt.Add(ingest.ReceiptControlRetention)) ||
		!reservation.CreatedAt.Before(reservation.ReservationDeadline) ||
		!reservation.ReservationDeadline.Before(reservation.ArtifactExpiresAt) ||
		reservation.ReceiptRetentionFloor.Before(reservation.ArtifactExpiresAt) ||
		reservation.PurgeEligibleAt != nil {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func lowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func expectedObjectPath(receipt firestoreIngestReceipt) string {
	receivedAt := receipt.CreatedAt.UTC()
	return fmt.Sprintf(
		"telemetry/v2/tenants/%s/devices/%s/trips/%s/year=%04d/month=%02d/day=%02d/%s.json.gz",
		receipt.TenantID,
		receipt.DeviceID,
		receipt.TripID,
		receivedAt.Year(),
		receivedAt.Month(),
		receivedAt.Day(),
		receipt.BatchID,
	)
}

func expectedManifestPath(receipt firestoreIngestReceipt) string {
	receivedAt := receipt.CreatedAt.UTC()
	return fmt.Sprintf(
		"telemetry-manifests/v2/tenants/%s/trips/%s/year=%04d/month=%02d/day=%02d/%s.manifest.json",
		receipt.TenantID,
		receipt.TripID,
		receivedAt.Year(),
		receivedAt.Month(),
		receivedAt.Day(),
		receipt.BatchID,
	)
}

func normalizeAdmissionError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ingest.ErrBatchUnauthorized) {
		return ingest.ErrBatchUnauthorized
	}
	if errors.Is(err, ingest.ErrAdmissionUnavailable) {
		return ingest.ErrAdmissionUnavailable
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return context.DeadlineExceeded
	}
	return ingest.ErrAdmissionUnavailable
}

type firestoreIngestIndex struct {
	TenantID              string     `firestore:"tenant_id"`
	ReservationKey        string     `firestore:"reservation_key"`
	ClientBatchKey        string     `firestore:"client_batch_key"`
	ReceiptID             string     `firestore:"receipt_id"`
	BatchID               string     `firestore:"batch_id"`
	InstallationID        string     `firestore:"installation_id"`
	ClientBatchID         string     `firestore:"client_batch_id"`
	PayloadSchemaVersion  string     `firestore:"payload_schema_version"`
	BodyHash              string     `firestore:"body_hash"`
	CreatedAt             time.Time  `firestore:"created_at"`
	ReceiptRetentionFloor time.Time  `firestore:"receipt_retention_floor"`
	PurgeEligibleAt       *time.Time `firestore:"purge_eligible_at,omitempty"`
}

func newFirestoreIngestIndex(reservation ingest.Reservation) firestoreIngestIndex {
	return firestoreIngestIndex{
		TenantID:              reservation.TenantID,
		ReservationKey:        reservation.ReservationKey,
		ClientBatchKey:        reservation.ClientBatchKey,
		ReceiptID:             reservation.ReceiptID,
		BatchID:               reservation.BatchID,
		InstallationID:        reservation.InstallationID,
		ClientBatchID:         reservation.ClientBatchID,
		PayloadSchemaVersion:  reservation.PayloadSchemaVersion,
		BodyHash:              reservation.BodyHash,
		CreatedAt:             reservation.CreatedAt.UTC(),
		ReceiptRetentionFloor: reservation.ReceiptRetentionFloor.UTC(),
		PurgeEligibleAt:       cloneOptionalTime(reservation.PurgeEligibleAt),
	}
}

type firestoreIngestReceipt struct {
	ReservationKey          string                  `firestore:"reservation_key"`
	ClientBatchKey          string                  `firestore:"client_batch_key"`
	ReceiptID               string                  `firestore:"receipt_id"`
	TenantID                string                  `firestore:"tenant_id"`
	BatchID                 string                  `firestore:"batch_id"`
	DeviceID                string                  `firestore:"device_id"`
	TripID                  string                  `firestore:"trip_id"`
	InstallationID          string                  `firestore:"installation_id"`
	ConsentRevisionID       string                  `firestore:"consent_revision_id"`
	ClientBatchID           string                  `firestore:"client_batch_id"`
	PayloadSchemaVersion    string                  `firestore:"payload_schema_version"`
	BodyHash                string                  `firestore:"body_hash"`
	ObjectPath              string                  `firestore:"object_path,omitempty"`
	ObjectSHA256            string                  `firestore:"object_sha256,omitempty"`
	ObjectCRC32C            int64                   `firestore:"object_crc32c,omitempty"`
	ObjectSize              int64                   `firestore:"object_size,omitempty"`
	ObjectGeneration        int64                   `firestore:"object_generation,omitempty"`
	ObjectMetageneration    int64                   `firestore:"object_metageneration,omitempty"`
	ManifestPath            string                  `firestore:"manifest_path,omitempty"`
	ManifestSHA256          string                  `firestore:"manifest_sha256,omitempty"`
	ManifestCRC32C          int64                   `firestore:"manifest_crc32c,omitempty"`
	ManifestSize            int64                   `firestore:"manifest_size,omitempty"`
	ManifestGeneration      int64                   `firestore:"manifest_generation,omitempty"`
	ManifestMetageneration  int64                   `firestore:"manifest_metageneration,omitempty"`
	ExpectedSampleCount     int                     `firestore:"expected_sample_count"`
	SampleCount             int                     `firestore:"sample_count"`
	FirstCapturedAt         time.Time               `firestore:"first_captured_at"`
	LastCapturedAt          time.Time               `firestore:"last_captured_at"`
	ValidatorVersion        string                  `firestore:"validator_version"`
	State                   ingest.ReceiptState     `firestore:"status"`
	RejectionCode           string                  `firestore:"rejection_code,omitempty"`
	FencingToken            int64                   `firestore:"fencing_token"`
	LeaseOwnerID            string                  `firestore:"lease_owner_id,omitempty"`
	LeaseOwnerKind          ingest.LeaseOwnerKind   `firestore:"lease_owner_kind,omitempty"`
	LeaseAcquiredAt         time.Time               `firestore:"lease_acquired_at,omitempty"`
	LeaseHeartbeatAt        time.Time               `firestore:"lease_heartbeat_at,omitempty"`
	LeaseExpiresAt          time.Time               `firestore:"lease_expires_at,omitempty"`
	RecoveryAttemptCount    int64                   `firestore:"recovery_attempt_count"`
	NextRecoveryAt          time.Time               `firestore:"next_recovery_at,omitempty"`
	LastRecoveryCode        string                  `firestore:"last_recovery_code,omitempty"`
	RecoveryHoldCode        ingest.RecoveryHoldCode `firestore:"hold_reason,omitempty"`
	RecoveryHoldReviewDueAt time.Time               `firestore:"hold_review_due_at,omitempty"`
	CleanupTransitionedAt   time.Time               `firestore:"cleanup_transitioned_at,omitempty"`
	CleanupQuiescenceUntil  time.Time               `firestore:"cleanup_quiescence_until,omitempty"`
	CleanupMode             ingest.CleanupMode      `firestore:"cleanup_mode,omitempty"`
	CleanupOriginStatus     ingest.ReceiptState     `firestore:"cleanup_origin_status,omitempty"`
	CleanupPolicyVersion    string                  `firestore:"cleanup_policy_version,omitempty"`
	Revision                int64                   `firestore:"revision"`
	CreatedAt               time.Time               `firestore:"created_at"`
	UpdatedAt               time.Time               `firestore:"updated_at"`
	ReservationDeadline     time.Time               `firestore:"reservation_deadline"`
	ArtifactExpiresAt       time.Time               `firestore:"artifact_expires_at"`
	ReceiptRetentionFloor   time.Time               `firestore:"receipt_retention_floor"`
	PurgeEligibleAt         *time.Time              `firestore:"purge_eligible_at,omitempty"`
}

type firestoreRecoveryAttempt struct {
	AttemptID                        string                                         `firestore:"attempt_id"`
	TenantID                         string                                         `firestore:"tenant_id"`
	ReceiptID                        string                                         `firestore:"receipt_id"`
	OwnerKind                        ingest.LeaseOwnerKind                          `firestore:"owner_kind"`
	FencingToken                     int64                                          `firestore:"fencing_token"`
	WorkerVersion                    string                                         `firestore:"worker_version"`
	Status                           ingest.RecoveryAttemptStatus                   `firestore:"status"`
	DecisionDomain                   ingest.ForwardRecoveryDecisionDomain           `firestore:"decision_domain,omitempty"`
	AuthorizationDisposition         ingest.ForwardRecoveryAuthorizationDisposition `firestore:"authorization_disposition,omitempty"`
	Phase                            ingest.RecoveryActionPhase                     `firestore:"phase,omitempty"`
	Classification                   ingest.ArtifactClassification                  `firestore:"classification,omitempty"`
	ReasonCode                       ingest.ArtifactReasonCode                      `firestore:"reason_code,omitempty"`
	Action                           ingest.ForwardRecoveryAction                   `firestore:"action,omitempty"`
	Outcome                          ingest.RecoveryAttemptOutcome                  `firestore:"outcome,omitempty"`
	ActionHash                       string                                         `firestore:"action_hash,omitempty"`
	HoldCode                         ingest.RecoveryHoldCode                        `firestore:"hold_code,omitempty"`
	ReleaseCode                      ingest.LeaseReleaseCode                        `firestore:"release_code,omitempty"`
	RejectionCode                    string                                         `firestore:"rejection_code,omitempty"`
	RawSHA256                        string                                         `firestore:"raw_sha256,omitempty"`
	RawCRC32C                        int64                                          `firestore:"raw_crc32c,omitempty"`
	RawSize                          int64                                          `firestore:"raw_size,omitempty"`
	RawGeneration                    int64                                          `firestore:"raw_generation,omitempty"`
	RawMetageneration                int64                                          `firestore:"raw_metageneration,omitempty"`
	ManifestSHA256                   string                                         `firestore:"manifest_sha256,omitempty"`
	ManifestCRC32C                   int64                                          `firestore:"manifest_crc32c,omitempty"`
	ManifestSize                     int64                                          `firestore:"manifest_size,omitempty"`
	ManifestGeneration               int64                                          `firestore:"manifest_generation,omitempty"`
	ManifestMetageneration           int64                                          `firestore:"manifest_metageneration,omitempty"`
	CleanupSchemaVersion             string                                         `firestore:"cleanup_schema_version,omitempty"`
	CleanupTargetHash                string                                         `firestore:"cleanup_target_hash,omitempty"`
	CleanupPlanHash                  string                                         `firestore:"cleanup_plan_hash,omitempty"`
	CleanupReceiptRevision           int64                                          `firestore:"cleanup_receipt_revision,omitempty"`
	CleanupExecutionRevision         int64                                          `firestore:"cleanup_execution_revision,omitempty"`
	CleanupPhase                     ingest.CleanupExecutionPhase                   `firestore:"cleanup_phase,omitempty"`
	CleanupRawTargeted               *bool                                          `firestore:"cleanup_raw_targeted,omitempty"`
	CleanupRawDispatchAt             time.Time                                      `firestore:"cleanup_raw_dispatch_at,omitempty"`
	CleanupRawDeleteOutcome          ingest.CleanupDeleteRPCOutcome                 `firestore:"cleanup_raw_delete_outcome,omitempty"`
	CleanupRawOutcomeRecordedAt      time.Time                                      `firestore:"cleanup_raw_outcome_recorded_at,omitempty"`
	CleanupRawAuditOutcome           ingest.CleanupAuditOutcome                     `firestore:"cleanup_raw_audit_outcome,omitempty"`
	CleanupRawAuditedAt              time.Time                                      `firestore:"cleanup_raw_audited_at,omitempty"`
	CleanupManifestTargeted          *bool                                          `firestore:"cleanup_manifest_targeted,omitempty"`
	CleanupManifestDispatchAt        time.Time                                      `firestore:"cleanup_manifest_dispatch_at,omitempty"`
	CleanupManifestDeleteOutcome     ingest.CleanupDeleteRPCOutcome                 `firestore:"cleanup_manifest_delete_outcome,omitempty"`
	CleanupManifestOutcomeRecordedAt time.Time                                      `firestore:"cleanup_manifest_outcome_recorded_at,omitempty"`
	CleanupManifestAuditOutcome      ingest.CleanupAuditOutcome                     `firestore:"cleanup_manifest_audit_outcome,omitempty"`
	CleanupManifestAuditedAt         time.Time                                      `firestore:"cleanup_manifest_audited_at,omitempty"`
	CleanupDisposition               ingest.CleanupExecutionDisposition             `firestore:"cleanup_disposition,omitempty"`
	CleanupErrorClass                ingest.CleanupExecutionErrorClass              `firestore:"cleanup_error_class,omitempty"`
	CleanupEvidenceHash              string                                         `firestore:"cleanup_evidence_hash,omitempty"`
	HoldReviewDueAt                  time.Time                                      `firestore:"hold_review_due_at,omitempty"`
	StartedAt                        time.Time                                      `firestore:"started_at"`
	CompletedAt                      time.Time                                      `firestore:"completed_at,omitempty"`
	FailureCode                      ingest.RecoveryAttemptFailureCode              `firestore:"failure_code,omitempty"`
	FailedAt                         time.Time                                      `firestore:"failed_at,omitempty"`
}

func newFirestoreRecoveryAttempt(
	proposal ingest.RecoveryAttemptProposal,
	tenantID string,
	receiptID string,
	ownerKind ingest.LeaseOwnerKind,
	fencingToken int64,
	startedAt time.Time,
) firestoreRecoveryAttempt {
	return firestoreRecoveryAttempt{
		AttemptID:     proposal.ID,
		TenantID:      tenantID,
		ReceiptID:     receiptID,
		OwnerKind:     ownerKind,
		FencingToken:  fencingToken,
		WorkerVersion: proposal.WorkerVersion,
		Status:        ingest.RecoveryAttemptStarted,
		StartedAt:     startedAt.UTC(),
	}
}

func newFirestoreIngestReceipt(
	reservation ingest.Reservation,
	owner ingest.LeaseOwner,
	leaseAcquiredAt time.Time,
	leaseExpiresAt time.Time,
) firestoreIngestReceipt {
	return firestoreIngestReceipt{
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
		FirstCapturedAt:       reservation.FirstCapturedAt.UTC(),
		LastCapturedAt:        reservation.LastCapturedAt.UTC(),
		ValidatorVersion:      reservation.ValidatorVersion,
		State:                 ingest.ReceiptReserved,
		FencingToken:          1,
		LeaseOwnerID:          owner.ID,
		LeaseOwnerKind:        owner.Kind,
		LeaseAcquiredAt:       leaseAcquiredAt.UTC(),
		LeaseHeartbeatAt:      leaseAcquiredAt.UTC(),
		LeaseExpiresAt:        leaseExpiresAt.UTC(),
		NextRecoveryAt:        leaseExpiresAt.UTC(),
		Revision:              1,
		CreatedAt:             reservation.CreatedAt.UTC(),
		UpdatedAt:             leaseAcquiredAt.UTC(),
		ReservationDeadline:   reservation.ReservationDeadline.UTC(),
		ArtifactExpiresAt:     reservation.ArtifactExpiresAt.UTC(),
		ReceiptRetentionFloor: reservation.ReceiptRetentionFloor.UTC(),
		PurgeEligibleAt:       cloneOptionalTime(reservation.PurgeEligibleAt),
	}
}

func (receipt firestoreIngestReceipt) toDomain() ingest.Receipt {
	return ingest.Receipt{
		ReservationKey:          receipt.ReservationKey,
		ClientBatchKey:          receipt.ClientBatchKey,
		ReceiptID:               receipt.ReceiptID,
		TenantID:                receipt.TenantID,
		BatchID:                 receipt.BatchID,
		DeviceID:                receipt.DeviceID,
		TripID:                  receipt.TripID,
		InstallationID:          receipt.InstallationID,
		ConsentRevisionID:       receipt.ConsentRevisionID,
		ClientBatchID:           receipt.ClientBatchID,
		PayloadSchemaVersion:    receipt.PayloadSchemaVersion,
		BodyHash:                receipt.BodyHash,
		ObjectPath:              receipt.ObjectPath,
		ObjectSHA256:            receipt.ObjectSHA256,
		ObjectCRC32C:            uint32(receipt.ObjectCRC32C),
		ObjectSize:              receipt.ObjectSize,
		ObjectGeneration:        receipt.ObjectGeneration,
		ObjectMetageneration:    receipt.ObjectMetageneration,
		ManifestPath:            receipt.ManifestPath,
		ManifestSHA256:          receipt.ManifestSHA256,
		ManifestCRC32C:          uint32(receipt.ManifestCRC32C),
		ManifestSize:            receipt.ManifestSize,
		ManifestGeneration:      receipt.ManifestGeneration,
		ManifestMetageneration:  receipt.ManifestMetageneration,
		ExpectedSampleCount:     receipt.ExpectedSampleCount,
		SampleCount:             receipt.SampleCount,
		FirstCapturedAt:         receipt.FirstCapturedAt,
		LastCapturedAt:          receipt.LastCapturedAt,
		ValidatorVersion:        receipt.ValidatorVersion,
		State:                   receipt.State,
		RejectionCode:           receipt.RejectionCode,
		FencingToken:            receipt.FencingToken,
		LeaseOwnerID:            receipt.LeaseOwnerID,
		LeaseOwnerKind:          receipt.LeaseOwnerKind,
		LeaseAcquiredAt:         receipt.LeaseAcquiredAt,
		LeaseHeartbeatAt:        receipt.LeaseHeartbeatAt,
		LeaseExpiresAt:          receipt.LeaseExpiresAt,
		RecoveryAttemptCount:    receipt.RecoveryAttemptCount,
		NextRecoveryAt:          receipt.NextRecoveryAt,
		LastRecoveryCode:        receipt.LastRecoveryCode,
		RecoveryHoldCode:        receipt.RecoveryHoldCode,
		RecoveryHoldReviewDueAt: receipt.RecoveryHoldReviewDueAt,
		CleanupTransitionedAt:   receipt.CleanupTransitionedAt,
		CleanupQuiescenceUntil:  receipt.CleanupQuiescenceUntil,
		CleanupMode:             receipt.CleanupMode,
		CleanupOriginStatus:     receipt.CleanupOriginStatus,
		CleanupPolicyVersion:    receipt.CleanupPolicyVersion,
		Revision:                receipt.Revision,
		CreatedAt:               receipt.CreatedAt,
		UpdatedAt:               receipt.UpdatedAt,
		ReservationDeadline:     receipt.ReservationDeadline,
		ArtifactExpiresAt:       receipt.ArtifactExpiresAt,
		ReceiptRetentionFloor:   receipt.ReceiptRetentionFloor,
		PurgeEligibleAt:         cloneOptionalTime(receipt.PurgeEligibleAt),
	}
}

func (receipt *firestoreIngestReceipt) applyStoredData(stored ingest.StoredReceiptData) {
	receipt.ObjectPath = stored.Artifacts.Object.Path
	receipt.ObjectSHA256 = stored.Artifacts.Object.SHA256
	receipt.ObjectCRC32C = int64(stored.Artifacts.Object.CRC32C)
	receipt.ObjectSize = stored.Artifacts.Object.Size
	receipt.ObjectGeneration = stored.Artifacts.Object.Generation
	receipt.ObjectMetageneration = stored.Artifacts.Object.Metageneration
	receipt.ManifestPath = stored.Artifacts.Manifest.Path
	receipt.ManifestSHA256 = stored.Artifacts.Manifest.SHA256
	receipt.ManifestCRC32C = int64(stored.Artifacts.Manifest.CRC32C)
	receipt.ManifestSize = stored.Artifacts.Manifest.Size
	receipt.ManifestGeneration = stored.Artifacts.Manifest.Generation
	receipt.ManifestMetageneration = stored.Artifacts.Manifest.Metageneration
	receipt.SampleCount = stored.SampleCount
}

func (receipt *firestoreIngestReceipt) applyLease(
	owner ingest.LeaseOwner,
	acquiredAt time.Time,
	expiresAt time.Time,
	token int64,
) {
	receipt.FencingToken = token
	receipt.LeaseOwnerID = owner.ID
	receipt.LeaseOwnerKind = owner.Kind
	receipt.LeaseAcquiredAt = acquiredAt.UTC()
	receipt.LeaseHeartbeatAt = acquiredAt.UTC()
	receipt.LeaseExpiresAt = expiresAt.UTC()
	receipt.NextRecoveryAt = expiresAt.UTC()
	receipt.LastRecoveryCode = ""
	receipt.RecoveryHoldCode = ""
	receipt.RecoveryHoldReviewDueAt = time.Time{}
}

func (receipt *firestoreIngestReceipt) clearLease() {
	receipt.LeaseOwnerID = ""
	receipt.LeaseOwnerKind = ""
	receipt.LeaseAcquiredAt = time.Time{}
	receipt.LeaseHeartbeatAt = time.Time{}
	receipt.LeaseExpiresAt = time.Time{}
	receipt.NextRecoveryAt = time.Time{}
}

func (receipt firestoreIngestReceipt) leaseGrant() ingest.LeaseGrant {
	return ingest.LeaseGrant{
		Fence: ingest.LeaseFence{
			OwnerID:   receipt.LeaseOwnerID,
			Token:     receipt.FencingToken,
			ExpiresAt: receipt.LeaseExpiresAt,
		},
		OwnerKind:   receipt.LeaseOwnerKind,
		AcquiredAt:  receipt.LeaseAcquiredAt,
		HeartbeatAt: receipt.LeaseHeartbeatAt,
	}
}

func leaseTakeoverUpdates(
	owner ingest.LeaseOwner,
	acquiredAt time.Time,
	expiresAt time.Time,
	token int64,
	revision int64,
	recoveryAttemptCount int64,
) []firestore.Update {
	return []firestore.Update{
		{Path: "fencing_token", Value: token},
		{Path: "lease_owner_id", Value: owner.ID},
		{Path: "lease_owner_kind", Value: string(owner.Kind)},
		{Path: "lease_acquired_at", Value: acquiredAt.UTC()},
		{Path: "lease_heartbeat_at", Value: acquiredAt.UTC()},
		{Path: "lease_expires_at", Value: expiresAt.UTC()},
		{Path: "next_recovery_at", Value: expiresAt.UTC()},
		{Path: "last_recovery_code", Value: firestore.Delete},
		{Path: "recovery_attempt_count", Value: recoveryAttemptCount},
		{Path: "revision", Value: revision},
		{Path: "updated_at", Value: acquiredAt.UTC()},
	}
}

func validateForwardFence(
	receipt firestoreIngestReceipt,
	fence ingest.LeaseFence,
	updatedAt time.Time,
) error {
	updatedAt = updatedAt.UTC()
	if receipt.State != ingest.ReceiptReserved || validateActiveReceiptLease(receipt) != nil ||
		ingest.ValidateLeaseFence(fence) != nil ||
		fence.OwnerID != receipt.LeaseOwnerID || fence.Token != receipt.FencingToken ||
		!fence.ExpiresAt.Equal(receipt.LeaseExpiresAt) ||
		updatedAt.Before(receipt.UpdatedAt) || !updatedAt.Before(receipt.LeaseExpiresAt) ||
		!updatedAt.Before(receipt.ReservationDeadline) {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateReservedReceiptLease(receipt firestoreIngestReceipt) error {
	if receiptHasLeaseFields(receipt) {
		return validateActiveReceiptLease(receipt)
	}
	if receipt.NextRecoveryAt.IsZero() || receipt.NextRecoveryAt.Before(receipt.UpdatedAt) ||
		!ingest.ValidLeaseReleaseCode(ingest.LeaseReleaseCode(receipt.LastRecoveryCode)) {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateActiveReceiptLease(receipt firestoreIngestReceipt) error {
	grant := receipt.leaseGrant()
	leaseDuration := receipt.LeaseExpiresAt.Sub(receipt.LeaseHeartbeatAt)
	if !receiptHasLeaseFields(receipt) || ingest.ValidateLeaseGrant(grant) != nil ||
		receipt.LeaseHeartbeatAt.Before(receipt.LeaseAcquiredAt) ||
		!receipt.LeaseHeartbeatAt.Before(receipt.LeaseExpiresAt) ||
		leaseDuration < ingest.MinLeaseDuration || leaseDuration > ingest.MaxLeaseDuration ||
		receipt.LeaseExpiresAt.After(receipt.ReservationDeadline) ||
		!receipt.NextRecoveryAt.Equal(receipt.LeaseExpiresAt) || receipt.LastRecoveryCode != "" {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateNoReceiptLease(receipt firestoreIngestReceipt) error {
	if receiptHasLeaseFields(receipt) || !receipt.NextRecoveryAt.IsZero() {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func receiptHasLeaseFields(receipt firestoreIngestReceipt) bool {
	return receipt.LeaseOwnerID != "" || receipt.LeaseOwnerKind != "" ||
		!receipt.LeaseAcquiredAt.IsZero() || !receipt.LeaseHeartbeatAt.IsZero() ||
		!receipt.LeaseExpiresAt.IsZero()
}

func cloneOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func optionalTimesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func conservativeAcceptanceTime(applicationTime, readTime time.Time) (time.Time, error) {
	applicationTime = applicationTime.UTC()
	readTime = readTime.UTC()
	if !withinAdmissionClockSkew(applicationTime, readTime) {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	if readTime.After(applicationTime) {
		return readTime, nil
	}
	return applicationTime, nil
}

func conservativeCleanupTime(applicationTime, readTime time.Time) (time.Time, error) {
	return coherentCleanupTransitionTime(applicationTime, readTime)
}

func coherentCleanupTransitionTime(times ...time.Time) (time.Time, error) {
	var earliest time.Time
	var latest time.Time
	for _, candidate := range times {
		candidate = candidate.UTC()
		if candidate.IsZero() {
			return time.Time{}, ingest.ErrAdmissionUnavailable
		}
		if earliest.IsZero() || candidate.Before(earliest) {
			earliest = candidate
		}
		if latest.IsZero() || candidate.After(latest) {
			latest = candidate
		}
	}
	if earliest.IsZero() || latest.IsZero() || latest.Sub(earliest) > maxAdmissionClockSkew {
		return time.Time{}, ingest.ErrAdmissionUnavailable
	}
	return earliest, nil
}

func withinAdmissionClockSkew(left, right time.Time) bool {
	left = left.UTC()
	right = right.UTC()
	if left.IsZero() || right.IsZero() {
		return false
	}
	delta := left.Sub(right)
	return delta <= maxAdmissionClockSkew && delta >= -maxAdmissionClockSkew
}

func validateIndexDocument(index firestoreIngestIndex, tenantID string) error {
	if !telemetry.IsUUID(index.TenantID) || index.TenantID != tenantID ||
		!lowerHexDigest(index.ReservationKey) ||
		!lowerHexDigest(index.ClientBatchKey) ||
		!lowerHexDigest(index.BodyHash) ||
		!telemetry.IsUUID(index.ReceiptID) || !telemetry.IsUUID(index.BatchID) ||
		index.ReceiptID != index.BatchID ||
		!telemetry.IsUUID(index.InstallationID) || !telemetry.IsUUID(index.ClientBatchID) ||
		index.PayloadSchemaVersion != telemetry.SchemaVersionV2 ||
		index.CreatedAt.IsZero() || index.ReceiptRetentionFloor.IsZero() ||
		!index.CreatedAt.Before(index.ReceiptRetentionFloor) {
		return ingest.ErrAdmissionUnavailable
	}
	if index.ReservationKey != ingest.DeriveReservationKey(
		index.PayloadSchemaVersion,
		index.TenantID,
		index.InstallationID,
		index.ClientBatchID,
	) || index.ClientBatchKey != ingest.DeriveClientBatchKey(index.TenantID, index.ClientBatchID) {
		return ingest.ErrAdmissionUnavailable
	}
	if !index.ReceiptRetentionFloor.Equal(index.CreatedAt.Add(ingest.ReceiptControlRetention)) ||
		(index.PurgeEligibleAt != nil && index.PurgeEligibleAt.Before(index.ReceiptRetentionFloor)) {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateIndexPair(
	idempotency firestoreIngestIndex,
	clientBatch firestoreIngestIndex,
	reservation ingest.Reservation,
) error {
	if validateIndexDocument(idempotency, reservation.TenantID) != nil ||
		validateIndexDocument(clientBatch, reservation.TenantID) != nil {
		return ingest.ErrAdmissionUnavailable
	}
	if !sameIngestIndex(idempotency, clientBatch) ||
		idempotency.ReservationKey != reservation.ReservationKey ||
		idempotency.ClientBatchKey != reservation.ClientBatchKey ||
		idempotency.InstallationID != reservation.InstallationID ||
		idempotency.ClientBatchID != reservation.ClientBatchID ||
		idempotency.PayloadSchemaVersion != reservation.PayloadSchemaVersion {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func sameIngestIndex(left, right firestoreIngestIndex) bool {
	return left.TenantID == right.TenantID &&
		left.ReservationKey == right.ReservationKey &&
		left.ClientBatchKey == right.ClientBatchKey &&
		left.ReceiptID == right.ReceiptID &&
		left.BatchID == right.BatchID &&
		left.InstallationID == right.InstallationID &&
		left.ClientBatchID == right.ClientBatchID &&
		left.PayloadSchemaVersion == right.PayloadSchemaVersion &&
		left.BodyHash == right.BodyHash &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		left.ReceiptRetentionFloor.Equal(right.ReceiptRetentionFloor) &&
		optionalTimesEqual(left.PurgeEligibleAt, right.PurgeEligibleAt)
}

func validateReceiptLinkage(receipt firestoreIngestReceipt, index firestoreIngestIndex) error {
	if receipt.TenantID != index.TenantID ||
		receipt.ReservationKey != index.ReservationKey ||
		receipt.ClientBatchKey != index.ClientBatchKey ||
		receipt.ReceiptID != index.ReceiptID ||
		receipt.BatchID != index.BatchID ||
		receipt.InstallationID != index.InstallationID ||
		receipt.ClientBatchID != index.ClientBatchID ||
		receipt.PayloadSchemaVersion != index.PayloadSchemaVersion ||
		receipt.BodyHash != index.BodyHash ||
		!receipt.CreatedAt.Equal(index.CreatedAt) ||
		!receipt.ReceiptRetentionFloor.Equal(index.ReceiptRetentionFloor) ||
		!optionalTimesEqual(receipt.PurgeEligibleAt, index.PurgeEligibleAt) ||
		!telemetry.IsUUID(receipt.DeviceID) || !telemetry.IsUUID(receipt.TripID) ||
		!telemetry.IsUUID(receipt.ConsentRevisionID) || receipt.Revision <= 0 ||
		receipt.CreatedAt.IsZero() || receipt.UpdatedAt.Before(receipt.CreatedAt) ||
		receipt.ExpectedSampleCount <= 0 || receipt.ExpectedSampleCount > telemetry.MaxSamples ||
		receipt.FirstCapturedAt.IsZero() || receipt.LastCapturedAt.Before(receipt.FirstCapturedAt) ||
		receipt.ValidatorVersion != ingest.TelemetryValidatorVersion ||
		receipt.ReservationDeadline.IsZero() || receipt.ArtifactExpiresAt.IsZero() ||
		!receipt.CreatedAt.Before(receipt.ReservationDeadline) ||
		!receipt.ReservationDeadline.Before(receipt.ArtifactExpiresAt) ||
		receipt.ReceiptRetentionFloor.Before(receipt.ArtifactExpiresAt) ||
		!receipt.ReservationDeadline.Equal(receipt.CreatedAt.Add(ingest.ReservationProcessingWindow)) ||
		!receipt.ArtifactExpiresAt.Equal(receipt.CreatedAt.Add(ingest.TelemetryArtifactRetention)) ||
		!receipt.ReceiptRetentionFloor.Equal(receipt.CreatedAt.Add(ingest.ReceiptControlRetention)) ||
		(receipt.PurgeEligibleAt != nil && receipt.PurgeEligibleAt.Before(receipt.ReceiptRetentionFloor)) ||
		receipt.FencingToken <= 0 || receipt.RecoveryAttemptCount < 0 {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateReceiptReservation(receipt firestoreIngestReceipt, reservation ingest.Reservation) error {
	if receipt.TenantID != reservation.TenantID ||
		receipt.ReservationKey != reservation.ReservationKey ||
		receipt.ClientBatchKey != reservation.ClientBatchKey ||
		receipt.DeviceID != reservation.DeviceID ||
		receipt.TripID != reservation.TripID ||
		receipt.InstallationID != reservation.InstallationID ||
		receipt.ConsentRevisionID != reservation.ConsentRevisionID ||
		receipt.ClientBatchID != reservation.ClientBatchID ||
		receipt.PayloadSchemaVersion != reservation.PayloadSchemaVersion ||
		receipt.BodyHash != reservation.BodyHash ||
		receipt.ExpectedSampleCount != reservation.ExpectedSampleCount ||
		!receipt.FirstCapturedAt.Equal(reservation.FirstCapturedAt) ||
		!receipt.LastCapturedAt.Equal(reservation.LastCapturedAt) ||
		receipt.ValidatorVersion != reservation.ValidatorVersion {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateReceiptState(receipt firestoreIngestReceipt) error {
	switch receipt.State {
	case ingest.ReceiptReserved:
		if hasStoredArtifactData(receipt) || receipt.SampleCount != 0 || receipt.RejectionCode != "" ||
			validateReservedReceiptLease(receipt) != nil || validateNoRecoveryHold(receipt) != nil ||
			validateNoCleanupTransition(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
	case ingest.ReceiptStored, ingest.ReceiptQueued, ingest.ReceiptProjected, ingest.ReceiptDeleting, ingest.ReceiptDeleted:
		if validatePersistedArtifactData(receipt) != nil ||
			receipt.SampleCount != receipt.ExpectedSampleCount ||
			receipt.RejectionCode != "" || validateNoReceiptLease(receipt) != nil ||
			validateNoRecoveryHold(receipt) != nil || validateNoCleanupTransition(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
	case ingest.ReceiptRejected:
		if receipt.RejectionCode != "object_conflict" || hasStoredArtifactData(receipt) || receipt.SampleCount != 0 ||
			validateNoReceiptLease(receipt) != nil || validateNoRecoveryHold(receipt) != nil ||
			validateNoCleanupTransition(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
	case ingest.ReceiptRecoveryHold:
		if receipt.RejectionCode != "" || hasStoredArtifactData(receipt) || receipt.SampleCount != 0 ||
			validateNoReceiptLease(receipt) != nil || validateRecoveryHold(receipt) != nil ||
			validateNoCleanupTransition(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
	case ingest.ReceiptCleanupPending:
		if receipt.RejectionCode != "" ||
			hasStoredArtifactData(receipt) || receipt.SampleCount != 0 ||
			validateNoRecoveryHold(receipt) != nil || validateCleanupTransition(receipt) != nil ||
			validateCleanupPendingLease(receipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
	case ingest.ReceiptExpired:
		if receipt.RejectionCode != "" || validateNoReceiptLease(receipt) != nil ||
			hasStoredArtifactData(receipt) || receipt.SampleCount != 0 ||
			validateNoRecoveryHold(receipt) != nil || validateCleanupTransition(receipt) != nil ||
			receipt.UpdatedAt.Before(receipt.CleanupQuiescenceUntil) {
			return ingest.ErrAdmissionUnavailable
		}
	default:
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateRecoveryHold(receipt firestoreIngestReceipt) error {
	if !ingest.ValidRecoveryHoldCode(receipt.RecoveryHoldCode) ||
		receipt.RecoveryHoldReviewDueAt.IsZero() ||
		!receipt.UpdatedAt.Before(receipt.RecoveryHoldReviewDueAt) ||
		!receipt.RecoveryHoldReviewDueAt.Before(receipt.ArtifactExpiresAt) ||
		receipt.LastRecoveryCode != "" {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateNoRecoveryHold(receipt firestoreIngestReceipt) error {
	if receipt.RecoveryHoldCode != "" || !receipt.RecoveryHoldReviewDueAt.IsZero() {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateCleanupTransition(receipt firestoreIngestReceipt) error {
	if receipt.CleanupMode != ingest.CleanupModeReservationExpiry ||
		receipt.CleanupOriginStatus != ingest.ReceiptReserved ||
		receipt.CleanupPolicyVersion != ingest.CleanupTransitionPolicyV1 ||
		receipt.CleanupTransitionedAt.IsZero() ||
		receipt.CleanupTransitionedAt.Before(receipt.ReservationDeadline) ||
		!receipt.CleanupQuiescenceUntil.Equal(
			receipt.CleanupTransitionedAt.Add(ingest.DefaultCleanupLateWriteGrace),
		) || receipt.UpdatedAt.Before(receipt.CleanupTransitionedAt) {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateCleanupPendingLease(receipt firestoreIngestReceipt) error {
	if !receiptHasLeaseFields(receipt) {
		if validateNoReceiptLease(receipt) != nil || !receipt.UpdatedAt.Equal(receipt.CleanupTransitionedAt) {
			return ingest.ErrAdmissionUnavailable
		}
		return nil
	}
	grant := receipt.leaseGrant()
	if ingest.ValidateLeaseGrant(grant) != nil || grant.OwnerKind != ingest.LeaseOwnerCleanup ||
		receipt.RecoveryAttemptCount <= 0 || !receipt.NextRecoveryAt.IsZero() ||
		receipt.LastRecoveryCode != "" ||
		receipt.LeaseAcquiredAt.Before(receipt.CleanupQuiescenceUntil) ||
		!receipt.UpdatedAt.Equal(receipt.LeaseHeartbeatAt) {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateNoCleanupTransition(receipt firestoreIngestReceipt) error {
	if !receipt.CleanupTransitionedAt.IsZero() || !receipt.CleanupQuiescenceUntil.IsZero() ||
		receipt.CleanupMode != "" || receipt.CleanupOriginStatus != "" || receipt.CleanupPolicyVersion != "" {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

const maxCRC32C = int64(1<<32 - 1)

func validateStoredReceiptData(stored ingest.StoredReceiptData, tenantID string) error {
	object := stored.Artifacts.Object
	manifest := stored.Artifacts.Manifest
	if !telemetry.IsUUID(tenantID) || stored.SampleCount <= 0 || stored.SampleCount > telemetry.MaxSamples ||
		!lowerHexDigest(object.SHA256) || object.Size <= 0 || object.Generation <= 0 || object.Metageneration <= 0 ||
		!lowerHexDigest(manifest.SHA256) || manifest.Size <= 0 || manifest.Generation <= 0 || manifest.Metageneration <= 0 {
		return ingest.ErrAdmissionUnavailable
	}
	if object.Path == "" || manifest.Path == "" {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func sameStoredReceiptData(receipt firestoreIngestReceipt, stored ingest.StoredReceiptData) bool {
	return receipt.ObjectPath == stored.Artifacts.Object.Path &&
		receipt.ObjectSHA256 == stored.Artifacts.Object.SHA256 &&
		receipt.ObjectCRC32C == int64(stored.Artifacts.Object.CRC32C) &&
		receipt.ObjectSize == stored.Artifacts.Object.Size &&
		receipt.ObjectGeneration == stored.Artifacts.Object.Generation &&
		receipt.ObjectMetageneration == stored.Artifacts.Object.Metageneration &&
		receipt.ManifestPath == stored.Artifacts.Manifest.Path &&
		receipt.ManifestSHA256 == stored.Artifacts.Manifest.SHA256 &&
		receipt.ManifestCRC32C == int64(stored.Artifacts.Manifest.CRC32C) &&
		receipt.ManifestSize == stored.Artifacts.Manifest.Size &&
		receipt.ManifestGeneration == stored.Artifacts.Manifest.Generation &&
		receipt.ManifestMetageneration == stored.Artifacts.Manifest.Metageneration &&
		receipt.SampleCount == stored.SampleCount
}

func validatePersistedArtifactData(receipt firestoreIngestReceipt) error {
	if receipt.ObjectPath != expectedObjectPath(receipt) ||
		receipt.ManifestPath != expectedManifestPath(receipt) ||
		!lowerHexDigest(receipt.ObjectSHA256) || !lowerHexDigest(receipt.ManifestSHA256) ||
		receipt.ObjectCRC32C < 0 || receipt.ObjectCRC32C > maxCRC32C ||
		receipt.ManifestCRC32C < 0 || receipt.ManifestCRC32C > maxCRC32C ||
		receipt.ObjectSize <= 0 || receipt.ManifestSize <= 0 ||
		receipt.ObjectGeneration <= 0 || receipt.ObjectMetageneration <= 0 ||
		receipt.ManifestGeneration <= 0 || receipt.ManifestMetageneration <= 0 {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func hasStoredArtifactData(receipt firestoreIngestReceipt) bool {
	return receipt.ObjectPath != "" || receipt.ObjectSHA256 != "" || receipt.ObjectCRC32C != 0 ||
		receipt.ObjectSize != 0 || receipt.ObjectGeneration != 0 || receipt.ObjectMetageneration != 0 ||
		receipt.ManifestPath != "" || receipt.ManifestSHA256 != "" || receipt.ManifestCRC32C != 0 ||
		receipt.ManifestSize != 0 || receipt.ManifestGeneration != 0 || receipt.ManifestMetageneration != 0
}

type firestoreAdmissionTransaction struct {
	client      *firestore.Client
	transaction *firestore.Transaction
}

func (transaction firestoreAdmissionTransaction) LoadAuthorization(
	ctx context.Context,
	principal ingest.Principal,
	scope ingest.BatchScope,
) (authorizationRead, error) {
	primaryPaths, err := primaryAuthorizationPaths(principal, scope)
	if err != nil {
		return authorizationRead{}, authorization.ErrSnapshotUnavailable
	}
	primaryDocuments, err := transaction.readExact(ctx, primaryPaths)
	if err != nil {
		return authorizationRead{}, err
	}
	readTime, err := coherentSnapshotReadTime(primaryDocuments)
	if err != nil {
		return authorizationRead{}, err
	}
	var tenant firestoreTenant
	var membership firestoreMembership
	var installation firestoreInstallation
	var trip firestoreTrip
	var consent firestoreConsentRevision
	for index, destination := range []any{&tenant, &membership, &installation, &trip, &consent} {
		if err := primaryDocuments[index].DataTo(destination); err != nil {
			return authorizationRead{}, authorization.ErrSnapshotUnavailable
		}
	}
	relatedPaths, err := relatedAuthorizationPaths(scope.TenantID, trip.DeviceAssignmentID, trip.PersonID)
	if err != nil {
		return authorizationRead{}, authorization.ErrSnapshotUnavailable
	}
	relatedDocuments, err := transaction.readExact(ctx, relatedPaths)
	if err != nil {
		return authorizationRead{}, err
	}
	relatedReadTime, err := coherentSnapshotReadTime(relatedDocuments)
	if err != nil || !withinAdmissionClockSkew(relatedReadTime, readTime) {
		return authorizationRead{}, authorization.ErrSnapshotUnavailable
	}
	if relatedReadTime.After(readTime) {
		readTime = relatedReadTime
	}
	var assignment firestoreDeviceAssignment
	var consentState firestoreConsentState
	if err := relatedDocuments[0].DataTo(&assignment); err != nil {
		return authorizationRead{}, authorization.ErrSnapshotUnavailable
	}
	if err := relatedDocuments[1].DataTo(&consentState); err != nil {
		return authorizationRead{}, authorization.ErrSnapshotUnavailable
	}
	return authorizationRead{
		Snapshot: assembleAuthorizationSnapshot(
			tenant,
			membership,
			installation,
			trip,
			assignment,
			consent,
			consentState,
		),
		ReadTime: readTime,
	}, nil
}

func (transaction firestoreAdmissionTransaction) ReadIndex(
	ctx context.Context,
	path string,
) (firestoreIngestIndex, bool, error) {
	document, err := transaction.transaction.Get(transaction.client.Doc(path))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return firestoreIngestIndex{}, false, nil
		}
		return firestoreIngestIndex{}, false, normalizeAdmissionError(ctx, err)
	}
	var index firestoreIngestIndex
	if document == nil || !document.Exists() || document.DataTo(&index) != nil {
		return firestoreIngestIndex{}, false, ingest.ErrAdmissionUnavailable
	}
	return index, true, nil
}

func (transaction firestoreAdmissionTransaction) ReadReceipt(
	ctx context.Context,
	path string,
) (receiptRead, bool, error) {
	document, err := transaction.transaction.Get(transaction.client.Doc(path))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return receiptRead{}, false, nil
		}
		return receiptRead{}, false, normalizeAdmissionError(ctx, err)
	}
	var receipt firestoreIngestReceipt
	if document == nil || !document.Exists() || document.ReadTime.IsZero() || document.DataTo(&receipt) != nil {
		return receiptRead{}, false, ingest.ErrAdmissionUnavailable
	}
	return receiptRead{Receipt: receipt, ReadTime: document.ReadTime.UTC()}, true, nil
}

func (transaction firestoreAdmissionTransaction) Create(
	_ context.Context,
	path string,
	value any,
) error {
	return transaction.transaction.Create(transaction.client.Doc(path), value)
}

func (transaction firestoreAdmissionTransaction) Update(
	_ context.Context,
	path string,
	updates []firestore.Update,
) error {
	return transaction.transaction.Update(transaction.client.Doc(path), updates)
}

func (transaction firestoreAdmissionTransaction) readExact(
	ctx context.Context,
	paths []string,
) ([]*firestore.DocumentSnapshot, error) {
	references := make([]*firestore.DocumentRef, len(paths))
	for index, path := range paths {
		references[index] = transaction.client.Doc(path)
	}
	documents, err := transaction.transaction.GetAll(references)
	if err != nil {
		return nil, mapSnapshotReadError(ctx, err)
	}
	if len(documents) != len(references) {
		return nil, authorization.ErrSnapshotUnavailable
	}
	for _, document := range documents {
		if document == nil {
			return nil, authorization.ErrSnapshotUnavailable
		}
		if !document.Exists() {
			return nil, authorization.ErrSnapshotNotFound
		}
	}
	return documents, nil
}

func coherentSnapshotReadTime(documents []*firestore.DocumentSnapshot) (time.Time, error) {
	var earliest time.Time
	var latest time.Time
	for _, document := range documents {
		if document == nil || document.ReadTime.IsZero() {
			return time.Time{}, authorization.ErrSnapshotUnavailable
		}
		current := document.ReadTime.UTC()
		if earliest.IsZero() || current.Before(earliest) {
			earliest = current
		}
		if latest.IsZero() || current.After(latest) {
			latest = current
		}
	}
	if earliest.IsZero() || latest.IsZero() || latest.Sub(earliest) > maxAdmissionClockSkew {
		return time.Time{}, authorization.ErrSnapshotUnavailable
	}
	return latest, nil
}
