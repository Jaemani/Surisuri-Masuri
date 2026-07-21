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
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const maxAdmissionTransactionTimeout = 10 * time.Second

type admissionTransaction interface {
	LoadAuthorization(context.Context, ingest.Principal, ingest.BatchScope) (authorization.Snapshot, error)
	ReadIndex(context.Context, string) (firestoreIngestIndex, bool, error)
	ReadReceipt(context.Context, string) (firestoreIngestReceipt, bool, error)
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
	runTransaction runAdmissionTransaction
	now            func() time.Time
}

var _ ingest.AdmissionStore = (*FirestoreAdmissionStore)(nil)

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
) (ingest.Receipt, ingest.ReservationStatus, error) {
	if s == nil || s.runTransaction == nil || s.now == nil || ctx == nil {
		return ingest.Receipt{}, "", ingest.ErrAdmissionUnavailable
	}
	paths, err := admissionDocumentPaths(reservation)
	if err != nil {
		return ingest.Receipt{}, "", ingest.ErrAdmissionUnavailable
	}
	if reservation.TenantID != scope.TenantID ||
		reservation.DeviceID != scope.DeviceID ||
		reservation.TripID != scope.TripID ||
		reservation.InstallationID != scope.InstallationID ||
		reservation.ConsentRevisionID != scope.ConsentRevisionID {
		return ingest.Receipt{}, "", ingest.ErrAdmissionUnavailable
	}

	var (
		resultReceipt ingest.Receipt
		resultStatus  ingest.ReservationStatus
	)
	err = s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		// Firestore may retry this callback. Reset result state and reevaluate the
		// current authorization snapshot on every attempt.
		resultReceipt = ingest.Receipt{}
		resultStatus = ""

		now := s.now().UTC()
		if now.IsZero() {
			return ingest.ErrAdmissionUnavailable
		}
		snapshot, loadErr := transaction.LoadAuthorization(runContext, principal, scope)
		if loadErr != nil {
			if errors.Is(loadErr, authorization.ErrSnapshotNotFound) {
				return ingest.ErrBatchUnauthorized
			}
			return normalizeAdmissionError(runContext, loadErr)
		}
		if evaluationErr := authorization.EvaluateSnapshot(principal, scope, snapshot, now); evaluationErr != nil {
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
			index := newFirestoreIngestIndex(reservation)
			receipt := newFirestoreIngestReceipt(reservation)
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
			resultStatus = ingest.ReservationCreated
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
			linkedReceipt, exists, linkedErr := transaction.ReadReceipt(runContext, linkedReceiptPath)
			if linkedErr != nil {
				return normalizeAdmissionError(runContext, linkedErr)
			}
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
		storedReceipt, exists, readErr := transaction.ReadReceipt(runContext, receiptPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
		if !exists || validateReceiptLinkage(storedReceipt, idempotencyIndex) != nil ||
			validateReceiptState(storedReceipt) != nil {
			return ingest.ErrAdmissionUnavailable
		}
		if idempotencyIndex.BodyHash != reservation.BodyHash {
			resultStatus = ingest.ReservationConflict
			return nil
		}
		if storedReceipt.State == ingest.ReceiptReserved && !now.Before(storedReceipt.ExpiresAt) {
			return ingest.ErrAdmissionUnavailable
		}
		resultReceipt = storedReceipt.toDomain()
		switch storedReceipt.State {
		case ingest.ReceiptReserved:
			resultStatus = ingest.ReservationReplayPending
		case ingest.ReceiptStored, "queued", "projected", "deleting", "deleted":
			resultStatus = ingest.ReservationReplayComplete
		case ingest.ReceiptRejected:
			resultStatus = ingest.ReservationReplayRejected
		default:
			return ingest.ErrAdmissionUnavailable
		}
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

func (s *FirestoreAdmissionStore) MarkStored(
	ctx context.Context,
	tenantID string,
	reservationKey string,
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
		receipt, exists, readErr := transaction.ReadReceipt(runContext, receiptPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
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
		if updatedAt.Before(receipt.UpdatedAt) || !updatedAt.Before(receipt.ExpiresAt) {
			return ingest.ErrAdmissionUnavailable
		}
		nextRevision := receipt.Revision + 1
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
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: updatedAt.UTC()},
		}); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		receipt.State = ingest.ReceiptStored
		receipt.applyStoredData(stored)
		receipt.Revision = nextRevision
		receipt.UpdatedAt = updatedAt.UTC()
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
		receipt, exists, readErr := transaction.ReadReceipt(runContext, receiptPath)
		if readErr != nil {
			return normalizeAdmissionError(runContext, readErr)
		}
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
		if updatedAt.Before(receipt.UpdatedAt) || !updatedAt.Before(receipt.ExpiresAt) {
			return ingest.ErrAdmissionUnavailable
		}
		nextRevision := receipt.Revision + 1
		if updateErr := transaction.Update(runContext, receiptPath, []firestore.Update{
			{Path: "status", Value: string(ingest.ReceiptRejected)},
			{Path: "rejection_code", Value: rejectionCode},
			{Path: "revision", Value: nextRevision},
			{Path: "updated_at", Value: updatedAt.UTC()},
		}); updateErr != nil {
			return normalizeAdmissionError(runContext, updateErr)
		}
		receipt.State = ingest.ReceiptRejected
		receipt.RejectionCode = rejectionCode
		receipt.Revision = nextRevision
		receipt.UpdatedAt = updatedAt.UTC()
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
		reservation.CreatedAt.IsZero() || reservation.ExpiresAt.IsZero() ||
		!reservation.ExpiresAt.Equal(reservation.CreatedAt.Add(ingest.ReceiptRetention)) {
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
	TenantID             string    `firestore:"tenant_id"`
	ReservationKey       string    `firestore:"reservation_key"`
	ClientBatchKey       string    `firestore:"client_batch_key"`
	ReceiptID            string    `firestore:"receipt_id"`
	BatchID              string    `firestore:"batch_id"`
	InstallationID       string    `firestore:"installation_id"`
	ClientBatchID        string    `firestore:"client_batch_id"`
	PayloadSchemaVersion string    `firestore:"payload_schema_version"`
	BodyHash             string    `firestore:"body_hash"`
	CreatedAt            time.Time `firestore:"created_at"`
	ExpiresAt            time.Time `firestore:"expires_at"`
}

func newFirestoreIngestIndex(reservation ingest.Reservation) firestoreIngestIndex {
	return firestoreIngestIndex{
		TenantID:             reservation.TenantID,
		ReservationKey:       reservation.ReservationKey,
		ClientBatchKey:       reservation.ClientBatchKey,
		ReceiptID:            reservation.ReceiptID,
		BatchID:              reservation.BatchID,
		InstallationID:       reservation.InstallationID,
		ClientBatchID:        reservation.ClientBatchID,
		PayloadSchemaVersion: reservation.PayloadSchemaVersion,
		BodyHash:             reservation.BodyHash,
		CreatedAt:            reservation.CreatedAt.UTC(),
		ExpiresAt:            reservation.ExpiresAt.UTC(),
	}
}

type firestoreIngestReceipt struct {
	ReservationKey         string              `firestore:"reservation_key"`
	ClientBatchKey         string              `firestore:"client_batch_key"`
	ReceiptID              string              `firestore:"receipt_id"`
	TenantID               string              `firestore:"tenant_id"`
	BatchID                string              `firestore:"batch_id"`
	DeviceID               string              `firestore:"device_id"`
	TripID                 string              `firestore:"trip_id"`
	InstallationID         string              `firestore:"installation_id"`
	ConsentRevisionID      string              `firestore:"consent_revision_id"`
	ClientBatchID          string              `firestore:"client_batch_id"`
	PayloadSchemaVersion   string              `firestore:"payload_schema_version"`
	BodyHash               string              `firestore:"body_hash"`
	ObjectPath             string              `firestore:"object_path,omitempty"`
	ObjectSHA256           string              `firestore:"object_sha256,omitempty"`
	ObjectCRC32C           int64               `firestore:"object_crc32c,omitempty"`
	ObjectSize             int64               `firestore:"object_size,omitempty"`
	ObjectGeneration       int64               `firestore:"object_generation,omitempty"`
	ObjectMetageneration   int64               `firestore:"object_metageneration,omitempty"`
	ManifestPath           string              `firestore:"manifest_path,omitempty"`
	ManifestSHA256         string              `firestore:"manifest_sha256,omitempty"`
	ManifestCRC32C         int64               `firestore:"manifest_crc32c,omitempty"`
	ManifestSize           int64               `firestore:"manifest_size,omitempty"`
	ManifestGeneration     int64               `firestore:"manifest_generation,omitempty"`
	ManifestMetageneration int64               `firestore:"manifest_metageneration,omitempty"`
	SampleCount            int                 `firestore:"sample_count"`
	State                  ingest.ReceiptState `firestore:"status"`
	RejectionCode          string              `firestore:"rejection_code,omitempty"`
	Revision               int64               `firestore:"revision"`
	CreatedAt              time.Time           `firestore:"created_at"`
	UpdatedAt              time.Time           `firestore:"updated_at"`
	ExpiresAt              time.Time           `firestore:"expires_at"`
}

func newFirestoreIngestReceipt(reservation ingest.Reservation) firestoreIngestReceipt {
	return firestoreIngestReceipt{
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
		State:                ingest.ReceiptReserved,
		Revision:             1,
		CreatedAt:            reservation.CreatedAt.UTC(),
		UpdatedAt:            reservation.CreatedAt.UTC(),
		ExpiresAt:            reservation.ExpiresAt.UTC(),
	}
}

func (receipt firestoreIngestReceipt) toDomain() ingest.Receipt {
	return ingest.Receipt{
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
		ObjectCRC32C:           uint32(receipt.ObjectCRC32C),
		ObjectSize:             receipt.ObjectSize,
		ObjectGeneration:       receipt.ObjectGeneration,
		ObjectMetageneration:   receipt.ObjectMetageneration,
		ManifestPath:           receipt.ManifestPath,
		ManifestSHA256:         receipt.ManifestSHA256,
		ManifestCRC32C:         uint32(receipt.ManifestCRC32C),
		ManifestSize:           receipt.ManifestSize,
		ManifestGeneration:     receipt.ManifestGeneration,
		ManifestMetageneration: receipt.ManifestMetageneration,
		SampleCount:            receipt.SampleCount,
		State:                  receipt.State,
		RejectionCode:          receipt.RejectionCode,
		Revision:               receipt.Revision,
		CreatedAt:              receipt.CreatedAt,
		UpdatedAt:              receipt.UpdatedAt,
		ExpiresAt:              receipt.ExpiresAt,
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

func validateIndexDocument(index firestoreIngestIndex, tenantID string) error {
	if !telemetry.IsUUID(index.TenantID) || index.TenantID != tenantID ||
		!lowerHexDigest(index.ReservationKey) ||
		!lowerHexDigest(index.ClientBatchKey) ||
		!lowerHexDigest(index.BodyHash) ||
		!telemetry.IsUUID(index.ReceiptID) || !telemetry.IsUUID(index.BatchID) ||
		index.ReceiptID != index.BatchID ||
		!telemetry.IsUUID(index.InstallationID) || !telemetry.IsUUID(index.ClientBatchID) ||
		index.PayloadSchemaVersion != telemetry.SchemaVersionV2 ||
		index.CreatedAt.IsZero() || index.ExpiresAt.IsZero() || !index.CreatedAt.Before(index.ExpiresAt) {
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
	if !index.ExpiresAt.Equal(index.CreatedAt.Add(ingest.ReceiptRetention)) {
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
		left.ExpiresAt.Equal(right.ExpiresAt)
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
		!receipt.CreatedAt.Equal(index.CreatedAt) || !receipt.ExpiresAt.Equal(index.ExpiresAt) ||
		!telemetry.IsUUID(receipt.DeviceID) || !telemetry.IsUUID(receipt.TripID) ||
		!telemetry.IsUUID(receipt.ConsentRevisionID) || receipt.Revision <= 0 ||
		receipt.CreatedAt.IsZero() || receipt.UpdatedAt.Before(receipt.CreatedAt) ||
		receipt.ExpiresAt.IsZero() || !receipt.CreatedAt.Before(receipt.ExpiresAt) {
		return ingest.ErrAdmissionUnavailable
	}
	return nil
}

func validateReceiptState(receipt firestoreIngestReceipt) error {
	switch receipt.State {
	case ingest.ReceiptReserved:
		if hasStoredArtifactData(receipt) || receipt.SampleCount != 0 || receipt.RejectionCode != "" {
			return ingest.ErrAdmissionUnavailable
		}
	case ingest.ReceiptStored, "queued", "projected", "deleting", "deleted":
		if validatePersistedArtifactData(receipt) != nil ||
			receipt.SampleCount <= 0 || receipt.SampleCount > telemetry.MaxSamples ||
			receipt.RejectionCode != "" {
			return ingest.ErrAdmissionUnavailable
		}
	case ingest.ReceiptRejected:
		if receipt.RejectionCode != "object_conflict" || hasStoredArtifactData(receipt) || receipt.SampleCount != 0 {
			return ingest.ErrAdmissionUnavailable
		}
	default:
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
) (authorization.Snapshot, error) {
	primaryPaths, err := primaryAuthorizationPaths(principal, scope)
	if err != nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}
	primaryDocuments, err := transaction.readExact(ctx, primaryPaths)
	if err != nil {
		return authorization.Snapshot{}, err
	}
	var tenant firestoreTenant
	var membership firestoreMembership
	var installation firestoreInstallation
	var trip firestoreTrip
	var consent firestoreConsentRevision
	for index, destination := range []any{&tenant, &membership, &installation, &trip, &consent} {
		if err := primaryDocuments[index].DataTo(destination); err != nil {
			return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
		}
	}
	relatedPaths, err := relatedAuthorizationPaths(scope.TenantID, trip.DeviceAssignmentID, trip.PersonID)
	if err != nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}
	relatedDocuments, err := transaction.readExact(ctx, relatedPaths)
	if err != nil {
		return authorization.Snapshot{}, err
	}
	var assignment firestoreDeviceAssignment
	var consentState firestoreConsentState
	if err := relatedDocuments[0].DataTo(&assignment); err != nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}
	if err := relatedDocuments[1].DataTo(&consentState); err != nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}
	return assembleAuthorizationSnapshot(
		tenant,
		membership,
		installation,
		trip,
		assignment,
		consent,
		consentState,
	), nil
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
) (firestoreIngestReceipt, bool, error) {
	document, err := transaction.transaction.Get(transaction.client.Doc(path))
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return firestoreIngestReceipt{}, false, nil
		}
		return firestoreIngestReceipt{}, false, normalizeAdmissionError(ctx, err)
	}
	var receipt firestoreIngestReceipt
	if document == nil || !document.Exists() || document.DataTo(&receipt) != nil {
		return firestoreIngestReceipt{}, false, ingest.ErrAdmissionUnavailable
	}
	return receipt, true, nil
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
