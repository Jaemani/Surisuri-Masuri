package firebaseadapter

import (
	"context"
	"errors"
	"math"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

type FirestoreForwardRecoveryCheckpointStore struct {
	client  *firestore.Client
	timeout time.Duration
}

var _ ingest.ForwardRecoveryCheckpointStore = (*FirestoreForwardRecoveryCheckpointStore)(nil)

func NewFirestoreForwardRecoveryCheckpointStore(
	client *firestore.Client,
	timeout time.Duration,
) (*FirestoreForwardRecoveryCheckpointStore, error) {
	if client == nil || timeout <= 0 || timeout > maxForwardRecoveryCandidateQueryTimeout {
		return nil, errors.New("Firestore forward recovery checkpoint store requires a client and bounded timeout")
	}
	return &FirestoreForwardRecoveryCheckpointStore{client: client, timeout: timeout}, nil
}

func (store *FirestoreForwardRecoveryCheckpointStore) LoadForwardRecoveryCheckpoint(
	ctx context.Context,
	tenantID string,
) (ingest.ForwardRecoveryCheckpoint, error) {
	if store == nil || store.client == nil || store.timeout <= 0 || ctx == nil ||
		!telemetry.IsUUID(tenantID) {
		return ingest.ForwardRecoveryCheckpoint{}, ingest.ErrForwardRecoveryCheckpointUnavailable
	}
	readContext, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	document, err := store.client.Doc(forwardRecoveryCheckpointDocumentPath(tenantID)).Get(readContext)
	if status.Code(err) == codes.NotFound {
		return ingest.ForwardRecoveryCheckpoint{}, nil
	}
	if err != nil {
		return ingest.ForwardRecoveryCheckpoint{}, normalizeForwardRecoveryCheckpointError(readContext, err)
	}
	checkpoint, decodeErr := decodeFirestoreForwardRecoveryCheckpoint(document)
	if decodeErr != nil {
		return ingest.ForwardRecoveryCheckpoint{}, ingest.ErrForwardRecoveryCheckpointUnavailable
	}
	return checkpoint, nil
}

func (store *FirestoreForwardRecoveryCheckpointStore) CompareAndSetForwardRecoveryCheckpoint(
	ctx context.Context,
	tenantID string,
	expectedRevision int64,
	next *ingest.ForwardRecoveryCursor,
	scanCutoff time.Time,
	updatedAt time.Time,
) (bool, error) {
	if store == nil || store.client == nil || store.timeout <= 0 || ctx == nil ||
		!telemetry.IsUUID(tenantID) || expectedRevision < 0 || updatedAt.IsZero() ||
		((next == nil) != scanCutoff.IsZero()) ||
		(next != nil && next.NextRecoveryAt.After(scanCutoff)) ||
		(next != nil && updatedAt.Before(scanCutoff)) ||
		(next != nil && ingest.ValidateForwardRecoveryCursor(*next) != nil) {
		return false, ingest.ErrForwardRecoveryCheckpointUnavailable
	}
	transactionContext, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	result := false
	err := store.client.RunTransaction(
		transactionContext,
		func(runContext context.Context, transaction *firestore.Transaction) error {
			result = false
			reference := store.client.Doc(forwardRecoveryCheckpointDocumentPath(tenantID))
			document, getErr := transaction.Get(reference)
			currentRevision := int64(0)
			if getErr != nil && status.Code(getErr) != codes.NotFound {
				return normalizeForwardRecoveryCheckpointError(runContext, getErr)
			}
			if getErr == nil {
				checkpoint, decodeErr := decodeFirestoreForwardRecoveryCheckpoint(document)
				if decodeErr != nil {
					return ingest.ErrForwardRecoveryCheckpointUnavailable
				}
				currentRevision = checkpoint.Revision
			}
			if currentRevision != expectedRevision {
				return nil
			}
			if currentRevision == math.MaxInt64 {
				return ingest.ErrForwardRecoveryCheckpointUnavailable
			}
			stored := firestoreForwardRecoveryCheckpoint{
				Revision:  currentRevision + 1,
				UpdatedAt: updatedAt.UTC(),
			}
			if next != nil {
				stored.NextRecoveryAt = next.NextRecoveryAt.UTC()
				stored.DocumentID = next.DocumentID
				stored.ScanCutoff = scanCutoff.UTC()
			}
			if getErr != nil {
				if createErr := transaction.Create(reference, stored); createErr != nil {
					return normalizeForwardRecoveryCheckpointError(runContext, createErr)
				}
			} else if setErr := transaction.Set(reference, stored); setErr != nil {
				return normalizeForwardRecoveryCheckpointError(runContext, setErr)
			}
			result = true
			return nil
		},
	)
	if err != nil {
		return false, normalizeForwardRecoveryCheckpointError(transactionContext, err)
	}
	return result, nil
}

type firestoreForwardRecoveryCheckpoint struct {
	Revision       int64     `firestore:"revision"`
	NextRecoveryAt time.Time `firestore:"next_recovery_at,omitempty"`
	DocumentID     string    `firestore:"document_id,omitempty"`
	ScanCutoff     time.Time `firestore:"scan_cutoff,omitempty"`
	UpdatedAt      time.Time `firestore:"updated_at"`
}

func decodeFirestoreForwardRecoveryCheckpoint(
	document *firestore.DocumentSnapshot,
) (ingest.ForwardRecoveryCheckpoint, error) {
	if document == nil || !document.Exists() {
		return ingest.ForwardRecoveryCheckpoint{}, ingest.ErrForwardRecoveryCheckpointUnavailable
	}
	var stored firestoreForwardRecoveryCheckpoint
	if document.DataTo(&stored) != nil || stored.Revision <= 0 || stored.UpdatedAt.IsZero() ||
		(stored.NextRecoveryAt.IsZero() != (stored.DocumentID == "")) ||
		(stored.NextRecoveryAt.IsZero() != stored.ScanCutoff.IsZero()) {
		return ingest.ForwardRecoveryCheckpoint{}, ingest.ErrForwardRecoveryCheckpointUnavailable
	}
	checkpoint := ingest.ForwardRecoveryCheckpoint{Revision: stored.Revision}
	if !stored.NextRecoveryAt.IsZero() {
		checkpoint.ScanCutoff = stored.ScanCutoff.UTC()
		checkpoint.Cursor = &ingest.ForwardRecoveryCursor{
			NextRecoveryAt: stored.NextRecoveryAt.UTC(),
			DocumentID:     stored.DocumentID,
		}
	}
	if ingest.ValidateForwardRecoveryCheckpoint(checkpoint) != nil {
		return ingest.ForwardRecoveryCheckpoint{}, ingest.ErrForwardRecoveryCheckpointUnavailable
	}
	return checkpoint, nil
}

func forwardRecoveryCheckpointDocumentPath(tenantID string) string {
	return "tenants/" + tenantID + "/recoveryWorkerState/forward"
}

func normalizeForwardRecoveryCheckpointError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ingest.ErrForwardRecoveryCheckpointUnavailable
}
