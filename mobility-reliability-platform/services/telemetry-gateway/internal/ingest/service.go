package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var (
	ErrInvalidPrincipal    = errors.New("verified principal is incomplete")
	ErrBatchUnauthorized   = errors.New("verified principal is not authorized for the batch scope")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different body")
	ErrClientBatchConflict = errors.New("client batch id reused in a different installation or body")
	ErrObjectConflict      = errors.New("object path already contains different content")
)

type Principal struct {
	FirebaseUID string
	AppID       string
}

// BatchScope contains only identifiers needed for a server-side authorization
// decision. Coordinates and other telemetry values are deliberately excluded.
type BatchScope struct {
	TenantID          string
	DeviceID          string
	TripID            string
	ClientSessionID   string
	InstallationID    string
	ConsentRevisionID string
}

type BatchAuthorizer interface {
	Authorize(context.Context, Principal, BatchScope) error
}

type ServerBatchIDGenerator interface {
	NewID() (string, error)
}

type ReceiptState string

const (
	ReceiptReserved ReceiptState = "reserved"
	ReceiptStored   ReceiptState = "stored"
	ReceiptRejected ReceiptState = "rejected"
)

type Receipt struct {
	ReservationKey string
	ClientBatchKey string
	ReceiptID      string
	TenantID       string
	BatchID        string
	ClientBatchID  string
	BodyHash       string
	ObjectPath     string
	SampleCount    int
	State          ReceiptState
	RejectionCode  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ReservationStatus string

const (
	ReservationCreated             ReservationStatus = "created"
	ReservationReplayPending       ReservationStatus = "replay_pending"
	ReservationReplayComplete      ReservationStatus = "replay_complete"
	ReservationReplayRejected      ReservationStatus = "replay_rejected"
	ReservationConflict            ReservationStatus = "idempotency_conflict"
	ReservationClientBatchConflict ReservationStatus = "client_batch_conflict"
)

type Reservation struct {
	ReservationKey string
	ClientBatchKey string
	ReceiptID      string
	TenantID       string
	BatchID        string
	ClientBatchID  string
	BodyHash       string
	CreatedAt      time.Time
}

type ReceiptStore interface {
	Reserve(context.Context, Reservation) (Receipt, ReservationStatus, error)
	MarkStored(context.Context, string, string, int, time.Time) (Receipt, error)
	MarkRejected(context.Context, string, string, time.Time) (Receipt, error)
}

type ObjectStore interface {
	PutIfAbsent(context.Context, string, []byte, string) error
}

type Service struct {
	receipts   ReceiptStore
	objects    ObjectStore
	authorizer BatchAuthorizer
	batchIDs   ServerBatchIDGenerator
	now        func() time.Time
}

type Result struct {
	Receipt Receipt
	Replay  bool
}

func NewService(
	receipts ReceiptStore,
	objects ObjectStore,
	authorizer BatchAuthorizer,
	batchIDs ServerBatchIDGenerator,
	now func() time.Time,
) (*Service, error) {
	if receipts == nil || objects == nil || authorizer == nil || batchIDs == nil {
		return nil, errors.New("ingest stores, authorizer and batch id generator are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		receipts:   receipts,
		objects:    objects,
		authorizer: authorizer,
		batchIDs:   batchIDs,
		now:        now,
	}, nil
}

func (s *Service) Ingest(
	ctx context.Context,
	principal Principal,
	batch telemetry.Batch,
	rawBody []byte,
) (Result, error) {
	if validationErr := batch.Validate(); validationErr != nil {
		return Result{}, validationErr
	}
	if principal.FirebaseUID == "" || principal.AppID == "" {
		return Result{}, ErrInvalidPrincipal
	}
	if err := s.authorizer.Authorize(ctx, principal, BatchScope{
		TenantID:          batch.TenantID,
		DeviceID:          batch.DeviceID,
		TripID:            batch.TripID,
		ClientSessionID:   batch.ClientSessionID,
		InstallationID:    batch.InstallationID,
		ConsentRevisionID: batch.ConsentRevisionID,
	}); err != nil {
		if errors.Is(err, ErrBatchUnauthorized) {
			return Result{}, ErrBatchUnauthorized
		}
		return Result{}, fmt.Errorf("authorize batch: %w", err)
	}

	bodyDigest := sha256.Sum256(rawBody)
	bodyHash := hex.EncodeToString(bodyDigest[:])
	now := s.now().UTC()
	serverBatchID, err := s.batchIDs.NewID()
	if err != nil {
		return Result{}, fmt.Errorf("generate batch id: %w", err)
	}
	if !telemetry.IsUUID(serverBatchID) {
		return Result{}, errors.New("generated batch id is not a UUID")
	}
	receiptID := serverBatchID
	reservationKey := deriveReservationKey(batch)
	clientBatchKey := batch.TenantID + ":" + batch.ClientBatchID
	receipt, status, err := s.receipts.Reserve(ctx, Reservation{
		ReservationKey: reservationKey,
		ClientBatchKey: clientBatchKey,
		ReceiptID:      receiptID,
		TenantID:       batch.TenantID,
		BatchID:        serverBatchID,
		ClientBatchID:  batch.ClientBatchID,
		BodyHash:       bodyHash,
		CreatedAt:      now,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reserve receipt: %w", err)
	}
	switch status {
	case ReservationConflict:
		return Result{}, ErrIdempotencyConflict
	case ReservationClientBatchConflict:
		return Result{}, ErrClientBatchConflict
	case ReservationReplayComplete:
		return Result{Receipt: receipt, Replay: true}, nil
	case ReservationReplayRejected:
		return Result{}, rejectionError(receipt.RejectionCode)
	case ReservationCreated, ReservationReplayPending:
	default:
		return Result{}, errors.New("unknown reservation status")
	}

	compressed, err := deterministicGZIP(rawBody)
	if err != nil {
		return Result{}, fmt.Errorf("compress batch: %w", err)
	}
	if receipt.CreatedAt.IsZero() || !telemetry.IsUUID(receipt.BatchID) {
		return Result{}, errors.New("reserved receipt is missing stable batch identity")
	}
	receivedAt := receipt.CreatedAt.UTC()
	objectPath := fmt.Sprintf(
		"telemetry/v2/tenants/%s/devices/%s/trips/%s/year=%04d/month=%02d/day=%02d/%s.json.gz",
		batch.TenantID,
		batch.DeviceID,
		batch.TripID,
		receivedAt.Year(),
		receivedAt.Month(),
		receivedAt.Day(),
		receipt.BatchID,
	)
	if err := s.objects.PutIfAbsent(ctx, objectPath, compressed, bodyHash); err != nil {
		if errors.Is(err, ErrObjectConflict) {
			if _, rejectErr := s.receipts.MarkRejected(
				ctx,
				reservationKey,
				"object_conflict",
				s.now().UTC(),
			); rejectErr != nil {
				return Result{}, fmt.Errorf("reject receipt after object conflict: %w", rejectErr)
			}
			return Result{}, ErrObjectConflict
		}
		return Result{}, fmt.Errorf("store batch object: %w", err)
	}

	receipt, err = s.receipts.MarkStored(
		ctx,
		reservationKey,
		objectPath,
		len(batch.Samples),
		now,
	)
	if err != nil {
		return Result{}, fmt.Errorf("complete receipt: %w", err)
	}
	return Result{Receipt: receipt, Replay: status == ReservationReplayPending}, nil
}

func deriveReservationKey(batch telemetry.Batch) string {
	material := strings.Join([]string{
		batch.SchemaVersion,
		batch.TenantID,
		batch.InstallationID,
		batch.ClientBatchID,
	}, "\x1f")
	digest := sha256.Sum256([]byte(material))
	return hex.EncodeToString(digest[:])
}

func rejectionError(code string) error {
	switch code {
	case "object_conflict":
		return ErrObjectConflict
	default:
		return errors.New("receipt rejected")
	}
}

func deterministicGZIP(raw []byte) ([]byte, error) {
	var destination bytes.Buffer
	writer, err := gzip.NewWriterLevel(&destination, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	writer.Header.ModTime = time.Unix(0, 0).UTC()
	writer.Header.OS = 255
	if _, err := writer.Write(raw); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return destination.Bytes(), nil
}
