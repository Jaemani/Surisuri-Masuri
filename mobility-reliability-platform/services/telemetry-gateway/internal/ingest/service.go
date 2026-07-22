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
	ErrInvalidPrincipal     = errors.New("verified principal is incomplete")
	ErrBatchUnauthorized    = errors.New("verified principal is not authorized for the batch scope")
	ErrIdempotencyConflict  = errors.New("idempotency key reused with a different body")
	ErrClientBatchConflict  = errors.New("client batch id reused in a different installation or body")
	ErrObjectConflict       = errors.New("object path already contains different content")
	ErrIngestInProgress     = errors.New("telemetry batch is already being processed")
	ErrAdmissionUnavailable = errors.New("telemetry admission store is unavailable")
)

const (
	ReservationProcessingWindow = 15 * time.Minute
	TelemetryArtifactRetention  = 30 * 24 * time.Hour
	ReceiptControlRetention     = 37 * 24 * time.Hour
	TelemetryValidatorVersion   = "telemetry-gateway-validator@1"
)

type Principal struct {
	FirebaseUID string
	AppID       string
}

// BatchScope contains only identifiers and sample time bounds needed for a
// server-side authorization decision. Coordinates and all other telemetry
// values are deliberately excluded.
type BatchScope struct {
	TenantID            string
	DeviceID            string
	TripID              string
	ClientSessionID     string
	InstallationID      string
	ConsentRevisionID   string
	ExpectedSampleCount int
	FirstCapturedAt     time.Time
	LastCapturedAt      time.Time
}

type ServerBatchIDGenerator interface {
	NewID() (string, error)
}

type ReceiptState string

const (
	ReceiptReserved       ReceiptState = "reserved"
	ReceiptStored         ReceiptState = "stored"
	ReceiptRejected       ReceiptState = "rejected"
	ReceiptQueued         ReceiptState = "queued"
	ReceiptProjected      ReceiptState = "projected"
	ReceiptDeleting       ReceiptState = "deleting"
	ReceiptDeleted        ReceiptState = "deleted"
	ReceiptCleanupPending ReceiptState = "cleanup_pending"
	ReceiptExpired        ReceiptState = "expired"
	ReceiptRecoveryHold   ReceiptState = "recovery_hold"
)

type Receipt struct {
	ReservationKey              string
	ClientBatchKey              string
	ReceiptID                   string
	TenantID                    string
	BatchID                     string
	DeviceID                    string
	TripID                      string
	InstallationID              string
	ConsentRevisionID           string
	ClientBatchID               string
	PayloadSchemaVersion        string
	BodyHash                    string
	ObjectPath                  string
	ObjectSHA256                string
	ObjectCRC32C                uint32
	ObjectSize                  int64
	ObjectGeneration            int64
	ObjectMetageneration        int64
	ManifestPath                string
	ManifestSHA256              string
	ManifestCRC32C              uint32
	ManifestSize                int64
	ManifestGeneration          int64
	ManifestMetageneration      int64
	ExpectedSampleCount         int
	SampleCount                 int
	FirstCapturedAt             time.Time
	LastCapturedAt              time.Time
	ValidatorVersion            string
	State                       ReceiptState
	RejectionCode               string
	FencingToken                int64
	LeaseOwnerID                string
	LeaseOwnerKind              LeaseOwnerKind
	LeaseAcquiredAt             time.Time
	LeaseHeartbeatAt            time.Time
	LeaseExpiresAt              time.Time
	RecoveryAttemptCount        int64
	NextRecoveryAt              time.Time
	LastRecoveryCode            string
	RecoveryHoldCode            RecoveryHoldCode
	RecoveryHoldReviewDueAt     time.Time
	CleanupTransitionedAt       time.Time
	CleanupQuiescenceUntil      time.Time
	CleanupMode                 CleanupMode
	CleanupOriginStatus         ReceiptState
	CleanupPolicyVersion        string
	CleanupDispositionAttemptID string
	CleanupControlDisposition   CleanupExecutionDisposition
	LastCleanupErrorClass       CleanupExecutionErrorClass
	NextCleanupAt               time.Time
	CleanupHoldReviewDueAt      time.Time
	Revision                    int64
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
	ReservationDeadline         time.Time
	ArtifactExpiresAt           time.Time
	ReceiptRetentionFloor       time.Time
	PurgeEligibleAt             *time.Time
	PurgeJobID                  string
	PurgeStartedAt              time.Time
	PurgeFenceVersion           string
}

type ReservationStatus string

const (
	ReservationCreatedLeaseAcquired ReservationStatus = "created_lease_acquired"
	ReservationReplayLeaseAcquired  ReservationStatus = "replay_lease_acquired"
	ReservationReplayInProgress     ReservationStatus = "replay_in_progress"
	ReservationReplayComplete       ReservationStatus = "replay_complete"
	ReservationReplayRejected       ReservationStatus = "replay_rejected"
	ReservationReplayRecoveryHold   ReservationStatus = "replay_recovery_hold"
	ReservationReplayExpired        ReservationStatus = "replay_expired"
	ReservationConflict             ReservationStatus = "idempotency_conflict"
	ReservationClientBatchConflict  ReservationStatus = "client_batch_conflict"
)

type Reservation struct {
	ReservationKey        string
	ClientBatchKey        string
	ReceiptID             string
	TenantID              string
	BatchID               string
	DeviceID              string
	TripID                string
	InstallationID        string
	ConsentRevisionID     string
	ClientBatchID         string
	PayloadSchemaVersion  string
	BodyHash              string
	ExpectedSampleCount   int
	FirstCapturedAt       time.Time
	LastCapturedAt        time.Time
	ValidatorVersion      string
	CreatedAt             time.Time
	ReservationDeadline   time.Time
	ArtifactExpiresAt     time.Time
	ReceiptRetentionFloor time.Time
	PurgeEligibleAt       *time.Time
}

// StoredReceiptData carries the complete immutable raw and manifest lineage
// into the control-plane finalizer. SampleCount remains explicit because it is
// receipt state rather than provider-returned artifact metadata.
type StoredReceiptData struct {
	Artifacts   StoredBatchArtifacts
	SampleCount int
}

type AdmissionStore interface {
	AuthorizeAndReserve(context.Context, Principal, BatchScope, Reservation, LeaseProposal) (Receipt, LeaseGrant, ReservationStatus, error)
	MarkStored(context.Context, string, string, LeaseFence, StoredReceiptData, time.Time) (Receipt, error)
	MarkRejected(context.Context, string, string, LeaseFence, string, time.Time) (Receipt, error)
	ReleaseLease(context.Context, string, string, LeaseFence, time.Time, LeaseReleaseCode) error
}

type Service struct {
	admissions    AdmissionStore
	artifacts     TelemetryArtifactStore
	batchIDs      ServerBatchIDGenerator
	leaseOwnerIDs ServerBatchIDGenerator
	now           func() time.Time
}

type Result struct {
	Receipt Receipt
	Replay  bool
}

func NewService(
	admissions AdmissionStore,
	artifacts TelemetryArtifactStore,
	batchIDs ServerBatchIDGenerator,
	leaseOwnerIDs ServerBatchIDGenerator,
	now func() time.Time,
) (*Service, error) {
	if admissions == nil || artifacts == nil || batchIDs == nil || leaseOwnerIDs == nil {
		return nil, errors.New("ingest admission store, artifact store, batch id generator and lease owner id generator are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		admissions:    admissions,
		artifacts:     artifacts,
		batchIDs:      batchIDs,
		leaseOwnerIDs: leaseOwnerIDs,
		now:           now,
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
	firstCapturedAt, lastCapturedAt := capturedAtBounds(batch)
	scope := BatchScope{
		TenantID:            batch.TenantID,
		DeviceID:            batch.DeviceID,
		TripID:              batch.TripID,
		ClientSessionID:     batch.ClientSessionID,
		InstallationID:      batch.InstallationID,
		ConsentRevisionID:   batch.ConsentRevisionID,
		ExpectedSampleCount: len(batch.Samples),
		FirstCapturedAt:     firstCapturedAt,
		LastCapturedAt:      lastCapturedAt,
	}

	bodyDigest := sha256.Sum256(rawBody)
	bodyHash := hex.EncodeToString(bodyDigest[:])
	now := s.now().UTC()
	leaseOwnerID, err := s.leaseOwnerIDs.NewID()
	if err != nil {
		return Result{}, fmt.Errorf("generate lease owner id: %w", err)
	}
	if !telemetry.IsUUID(leaseOwnerID) {
		return Result{}, errors.New("generated lease owner id is not a UUID")
	}
	serverBatchID, err := s.batchIDs.NewID()
	if err != nil {
		return Result{}, fmt.Errorf("generate batch id: %w", err)
	}
	if !telemetry.IsUUID(serverBatchID) {
		return Result{}, errors.New("generated batch id is not a UUID")
	}
	receiptID := serverBatchID
	reservationKey := DeriveReservationKey(
		batch.SchemaVersion,
		batch.TenantID,
		batch.InstallationID,
		batch.ClientBatchID,
	)
	clientBatchKey := DeriveClientBatchKey(batch.TenantID, batch.ClientBatchID)
	receipt, lease, status, err := s.admissions.AuthorizeAndReserve(ctx, principal, scope, Reservation{
		ReservationKey:        reservationKey,
		ClientBatchKey:        clientBatchKey,
		ReceiptID:             receiptID,
		TenantID:              batch.TenantID,
		BatchID:               serverBatchID,
		DeviceID:              batch.DeviceID,
		TripID:                batch.TripID,
		InstallationID:        batch.InstallationID,
		ConsentRevisionID:     batch.ConsentRevisionID,
		ClientBatchID:         batch.ClientBatchID,
		PayloadSchemaVersion:  batch.SchemaVersion,
		BodyHash:              bodyHash,
		ExpectedSampleCount:   len(batch.Samples),
		FirstCapturedAt:       firstCapturedAt,
		LastCapturedAt:        lastCapturedAt,
		ValidatorVersion:      TelemetryValidatorVersion,
		CreatedAt:             now,
		ReservationDeadline:   now.Add(ReservationProcessingWindow),
		ArtifactExpiresAt:     now.Add(TelemetryArtifactRetention),
		ReceiptRetentionFloor: now.Add(ReceiptControlRetention),
	}, LeaseProposal{
		Owner:    LeaseOwner{ID: leaseOwnerID, Kind: LeaseOwnerRequest},
		Duration: DefaultRequestLeaseDuration,
		Attempt: RecoveryAttemptProposal{
			ID:            leaseOwnerID,
			WorkerVersion: RecoveryWorkerVersion,
		},
	})
	if err != nil {
		if errors.Is(err, ErrBatchUnauthorized) {
			return Result{}, ErrBatchUnauthorized
		}
		return Result{}, fmt.Errorf("authorize and reserve receipt: %w", err)
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
	case ReservationReplayInProgress:
		return Result{Receipt: receipt, Replay: true}, ErrIngestInProgress
	case ReservationReplayRecoveryHold, ReservationReplayExpired:
		return Result{}, ErrAdmissionUnavailable
	case ReservationCreatedLeaseAcquired, ReservationReplayLeaseAcquired:
	default:
		return Result{}, errors.New("unknown reservation status")
	}
	if ValidateLeaseGrant(lease) != nil || lease.OwnerKind != LeaseOwnerRequest {
		return Result{}, ErrAdmissionUnavailable
	}

	compressed, err := deterministicGZIP(rawBody)
	if err != nil {
		return Result{}, fmt.Errorf("compress batch: %w", err)
	}
	if receipt.State != ReceiptReserved || receipt.CreatedAt.IsZero() || !telemetry.IsUUID(receipt.BatchID) ||
		receipt.TenantID != batch.TenantID ||
		receipt.DeviceID != batch.DeviceID ||
		receipt.TripID != batch.TripID ||
		receipt.InstallationID != batch.InstallationID ||
		receipt.ConsentRevisionID != batch.ConsentRevisionID ||
		receipt.ClientBatchID != batch.ClientBatchID ||
		receipt.PayloadSchemaVersion != batch.SchemaVersion ||
		receipt.ReservationKey != reservationKey ||
		receipt.ClientBatchKey != clientBatchKey ||
		receipt.BodyHash != bodyHash ||
		receipt.ExpectedSampleCount != len(batch.Samples) ||
		!receipt.FirstCapturedAt.Equal(firstCapturedAt) ||
		!receipt.LastCapturedAt.Equal(lastCapturedAt) ||
		receipt.ValidatorVersion != TelemetryValidatorVersion ||
		receipt.ReservationDeadline.IsZero() || receipt.ArtifactExpiresAt.IsZero() ||
		receipt.LeaseOwnerID != leaseOwnerID ||
		receipt.LeaseOwnerKind != LeaseOwnerRequest ||
		receipt.FencingToken != lease.Fence.Token ||
		receipt.LeaseOwnerID != lease.Fence.OwnerID ||
		!receipt.LeaseExpiresAt.Equal(lease.Fence.ExpiresAt) ||
		!receipt.LeaseAcquiredAt.Equal(lease.AcquiredAt) ||
		!receipt.LeaseHeartbeatAt.Equal(lease.HeartbeatAt) {
		return Result{}, errors.New("reserved receipt is missing stable batch identity")
	}
	manifestInput := BatchManifestInput{
		PayloadSchemaVersion: receipt.PayloadSchemaVersion,
		TenantID:             receipt.TenantID,
		DeviceID:             receipt.DeviceID,
		TripID:               receipt.TripID,
		InstallationID:       receipt.InstallationID,
		BatchID:              receipt.BatchID,
		ClientBatchID:        receipt.ClientBatchID,
		ConsentRevisionID:    receipt.ConsentRevisionID,
		BodyHash:             receipt.BodyHash,
		SampleCount:          receipt.ExpectedSampleCount,
		FirstCapturedAt:      receipt.FirstCapturedAt,
		LastCapturedAt:       receipt.LastCapturedAt,
		ReceivedAt:           receipt.CreatedAt.UTC(),
		ArtifactExpiresAt:    receipt.ArtifactExpiresAt.UTC(),
		ValidatorVersion:     receipt.ValidatorVersion,
	}
	objectPath := ExpectedTelemetryObjectPath(manifestInput)
	manifestPath := ExpectedTelemetryManifestPath(manifestInput)
	artifactCtx, cancelArtifactOperation := context.WithTimeout(ctx, MaxArtifactOperationTimeout)
	storedArtifacts, err := s.artifacts.StoreBatch(artifactCtx, BatchArtifactWrite{
		ObjectPath:     objectPath,
		ManifestPath:   manifestPath,
		CompressedBody: compressed,
		Manifest:       manifestInput,
	})
	cancelArtifactOperation()
	if err != nil {
		if errors.Is(err, ErrRawArtifactConflict) {
			if _, rejectErr := s.admissions.MarkRejected(
				ctx,
				receipt.TenantID,
				receipt.ReservationKey,
				lease.Fence,
				"object_conflict",
				s.now().UTC(),
			); rejectErr != nil {
				return Result{}, fmt.Errorf("reject receipt after artifact conflict: %w", rejectErr)
			}
			return Result{}, ErrObjectConflict
		}
		if releaseErr := s.admissions.ReleaseLease(
			ctx,
			receipt.TenantID,
			receipt.ReservationKey,
			lease.Fence,
			s.now().UTC(),
			LeaseReleaseArtifactUnavailable,
		); releaseErr != nil {
			return Result{}, fmt.Errorf("store batch artifacts: %w; release lease: %v", err, releaseErr)
		}
		return Result{}, fmt.Errorf("store batch artifacts: %w", err)
	}

	storedReceipt, err := s.admissions.MarkStored(
		ctx,
		receipt.TenantID,
		receipt.ReservationKey,
		lease.Fence,
		StoredReceiptData{
			Artifacts:   storedArtifacts,
			SampleCount: len(batch.Samples),
		},
		s.now().UTC(),
	)
	if err != nil {
		if releaseErr := s.admissions.ReleaseLease(
			ctx,
			receipt.TenantID,
			receipt.ReservationKey,
			lease.Fence,
			s.now().UTC(),
			LeaseReleaseFinalizerUnavailable,
		); releaseErr != nil {
			return Result{}, fmt.Errorf("complete receipt: %w; release lease: %v", err, releaseErr)
		}
		return Result{}, fmt.Errorf("complete receipt: %w", err)
	}
	receipt = storedReceipt
	return Result{Receipt: receipt, Replay: status == ReservationReplayLeaseAcquired}, nil
}

func capturedAtBounds(batch telemetry.Batch) (time.Time, time.Time) {
	first, _ := time.Parse(time.RFC3339, batch.Samples[0].CapturedAt)
	last := first
	for i := 1; i < len(batch.Samples); i++ {
		capturedAt, _ := time.Parse(time.RFC3339, batch.Samples[i].CapturedAt)
		if capturedAt.Before(first) {
			first = capturedAt
		}
		if capturedAt.After(last) {
			last = capturedAt
		}
	}
	return first, last
}

func DeriveReservationKey(schemaVersion, tenantID, installationID, clientBatchID string) string {
	material := strings.Join([]string{
		schemaVersion,
		tenantID,
		installationID,
		clientBatchID,
	}, "\x1f")
	digest := sha256.Sum256([]byte(material))
	return hex.EncodeToString(digest[:])
}

func DeriveClientBatchKey(tenantID, clientBatchID string) string {
	material := tenantID + "\x1f" + clientBatchID
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
