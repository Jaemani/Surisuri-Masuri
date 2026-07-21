package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var (
	ErrIdentityMismatch    = errors.New("verified identity does not match batch")
	ErrBatchUnauthorized   = errors.New("verified principal is not authorized for the batch scope")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different body")
	ErrBatchIDConflict     = errors.New("batch id reused with a different idempotency key or body")
	ErrObjectConflict      = errors.New("object path already contains different content")
)

type Principal struct {
	TenantID string
	ActorID  string
}

// BatchScope contains only identifiers needed for a server-side authorization
// decision. Coordinates and other telemetry values are deliberately excluded.
type BatchScope struct {
	TenantID         string
	ActorID          string
	MobilityDeviceID string
	SessionID        string
	ConsentVersion   string
}

type BatchAuthorizer interface {
	Authorize(context.Context, Principal, BatchScope) error
}

type ReceiptState string

const (
	ReceiptReserved ReceiptState = "reserved"
	ReceiptStored   ReceiptState = "stored"
	ReceiptRejected ReceiptState = "rejected"
)

type Receipt struct {
	ReservationKey string
	BatchKey       string
	ReceiptID      string
	TenantID       string
	BatchID        string
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
	ReservationCreated        ReservationStatus = "created"
	ReservationReplayPending  ReservationStatus = "replay_pending"
	ReservationReplayComplete ReservationStatus = "replay_complete"
	ReservationReplayRejected ReservationStatus = "replay_rejected"
	ReservationConflict       ReservationStatus = "idempotency_conflict"
	ReservationBatchConflict  ReservationStatus = "batch_conflict"
)

type Reservation struct {
	ReservationKey string
	BatchKey       string
	ReceiptID      string
	TenantID       string
	IdempotencyKey string
	BatchID        string
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
	now func() time.Time,
) (*Service, error) {
	if receipts == nil || objects == nil || authorizer == nil {
		return nil, errors.New("ingest stores and authorizer are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{receipts: receipts, objects: objects, authorizer: authorizer, now: now}, nil
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
	if principal.TenantID == "" || principal.ActorID == "" ||
		principal.TenantID != batch.TenantID || principal.ActorID != batch.ActorID {
		return Result{}, ErrIdentityMismatch
	}
	if err := s.authorizer.Authorize(ctx, principal, BatchScope{
		TenantID:         batch.TenantID,
		ActorID:          batch.ActorID,
		MobilityDeviceID: batch.MobilityDeviceID,
		SessionID:        batch.SessionID,
		ConsentVersion:   batch.ConsentVersion,
	}); err != nil {
		if errors.Is(err, ErrBatchUnauthorized) {
			return Result{}, ErrBatchUnauthorized
		}
		return Result{}, fmt.Errorf("authorize batch: %w", err)
	}

	bodyDigest := sha256.Sum256(rawBody)
	bodyHash := hex.EncodeToString(bodyDigest[:])
	now := s.now().UTC()
	receiptID := batch.BatchID
	reservationKey := principal.TenantID + ":" + batch.IdempotencyKey
	batchKey := principal.TenantID + ":" + batch.BatchID
	receipt, status, err := s.receipts.Reserve(ctx, Reservation{
		ReservationKey: reservationKey,
		BatchKey:       batchKey,
		ReceiptID:      receiptID,
		TenantID:       principal.TenantID,
		IdempotencyKey: batch.IdempotencyKey,
		BatchID:        batch.BatchID,
		BodyHash:       bodyHash,
		CreatedAt:      now,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reserve receipt: %w", err)
	}
	switch status {
	case ReservationConflict:
		return Result{}, ErrIdempotencyConflict
	case ReservationBatchConflict:
		return Result{}, ErrBatchIDConflict
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
	objectPath := fmt.Sprintf(
		"telemetry/%s/%s/%s.json.gz",
		principal.TenantID,
		batch.SessionID,
		batch.BatchID,
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
