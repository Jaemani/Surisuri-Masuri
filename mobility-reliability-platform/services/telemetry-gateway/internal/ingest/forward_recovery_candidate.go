package ingest

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const MaxForwardRecoveryCandidatePageSize = 100

var (
	ErrInvalidForwardRecoveryCandidate      = errors.New("forward recovery candidate is invalid")
	ErrInvalidForwardRecoveryCandidatePage  = errors.New("forward recovery candidate page is invalid")
	ErrForwardRecoveryCandidateUnavailable  = errors.New("forward recovery candidate store is unavailable")
	ErrInvalidForwardRecoveryCheckpoint     = errors.New("forward recovery checkpoint is invalid")
	ErrForwardRecoveryCheckpointUnavailable = errors.New("forward recovery checkpoint store is unavailable")
)

// ForwardRecoveryCandidate is advisory control-plane input. It deliberately
// excludes artifact lineage, device/user identity, credentials and telemetry.
// ClaimRecoveryLease must still re-read the authoritative receipt transaction.
type ForwardRecoveryCandidate struct {
	TenantID       string
	ReservationKey string
	DocumentID     string
	ReceiptID      string
	State          ReceiptState
	NextRecoveryAt time.Time
}

type ForwardRecoveryCursor struct {
	NextRecoveryAt time.Time
	DocumentID     string
}

// ForwardRecoveryCandidatePage uses an exact last-returned cursor whenever a
// later page exists. Exhausted pages never carry a cursor.
type ForwardRecoveryCandidatePage struct {
	Candidates []ForwardRecoveryCandidate
	NextCursor *ForwardRecoveryCursor
	Exhausted  bool
}

type ForwardRecoveryCandidateStore interface {
	ListDueForwardRecoveryCandidates(
		context.Context,
		string,
		time.Time,
		*ForwardRecoveryCursor,
		int,
	) (ForwardRecoveryCandidatePage, error)
}

// ForwardRecoveryCheckpoint is advisory scan progress, never receipt
// ownership. A lost update can only cause duplicate scanning because every
// candidate still needs a fresh fenced claim.
type ForwardRecoveryCheckpoint struct {
	Cursor     *ForwardRecoveryCursor
	ScanCutoff time.Time
	Revision   int64
}

type ForwardRecoveryCheckpointStore interface {
	LoadForwardRecoveryCheckpoint(context.Context, string) (ForwardRecoveryCheckpoint, error)
	CompareAndSetForwardRecoveryCheckpoint(
		context.Context,
		string,
		int64,
		*ForwardRecoveryCursor,
		time.Time,
		time.Time,
	) (bool, error)
}

func ValidateForwardRecoveryCheckpoint(checkpoint ForwardRecoveryCheckpoint) error {
	if checkpoint.Revision < 0 || checkpoint.Revision == math.MaxInt64 ||
		(checkpoint.Revision == 0 && checkpoint.Cursor != nil) ||
		((checkpoint.Cursor == nil) != checkpoint.ScanCutoff.IsZero()) ||
		(checkpoint.Cursor != nil && checkpoint.Cursor.NextRecoveryAt.After(checkpoint.ScanCutoff)) ||
		(checkpoint.Cursor != nil && ValidateForwardRecoveryCursor(*checkpoint.Cursor) != nil) {
		return ErrInvalidForwardRecoveryCheckpoint
	}
	return nil
}

func ValidateForwardRecoveryCandidate(candidate ForwardRecoveryCandidate) error {
	if !telemetry.IsUUID(candidate.TenantID) ||
		!validRecoveryReservationKey(candidate.ReservationKey) ||
		!validForwardRecoveryDocumentID(candidate.DocumentID) ||
		!telemetry.IsUUID(candidate.ReceiptID) || candidate.ReceiptID != candidate.DocumentID ||
		candidate.State != ReceiptReserved ||
		candidate.NextRecoveryAt.IsZero() {
		return ErrInvalidForwardRecoveryCandidate
	}
	return nil
}

func ValidateForwardRecoveryCursor(cursor ForwardRecoveryCursor) error {
	if cursor.NextRecoveryAt.IsZero() || !validForwardRecoveryDocumentID(cursor.DocumentID) {
		return ErrInvalidForwardRecoveryCandidatePage
	}
	return nil
}

func validateForwardRecoveryCandidatePage(
	page ForwardRecoveryCandidatePage,
	tenantID string,
	cutoff time.Time,
	after *ForwardRecoveryCursor,
	limit int,
) error {
	if !telemetry.IsUUID(tenantID) || cutoff.IsZero() || limit <= 0 ||
		limit > MaxForwardRecoveryCandidatePageSize || len(page.Candidates) > limit {
		return ErrInvalidForwardRecoveryCandidatePage
	}
	if after != nil && ValidateForwardRecoveryCursor(*after) != nil {
		return ErrInvalidForwardRecoveryCandidatePage
	}
	if len(page.Candidates) == 0 {
		if !page.Exhausted || page.NextCursor != nil {
			return ErrInvalidForwardRecoveryCandidatePage
		}
		return nil
	}
	var prior *ForwardRecoveryCursor
	if after != nil {
		copy := *after
		prior = &copy
	}
	for _, candidate := range page.Candidates {
		cursor := cursorForForwardRecoveryCandidate(candidate)
		if candidate.NextRecoveryAt.After(cutoff) ||
			ValidateForwardRecoveryCursor(cursor) != nil ||
			(prior != nil && compareForwardRecoveryCursor(cursor, *prior) <= 0) {
			return ErrInvalidForwardRecoveryCandidatePage
		}
		copy := cursor
		prior = &copy
	}
	if page.Exhausted {
		if page.NextCursor != nil {
			return ErrInvalidForwardRecoveryCandidatePage
		}
		return nil
	}
	if page.NextCursor == nil || ValidateForwardRecoveryCursor(*page.NextCursor) != nil ||
		compareForwardRecoveryCursor(*page.NextCursor, *prior) != 0 {
		return ErrInvalidForwardRecoveryCandidatePage
	}
	return nil
}

func cursorForForwardRecoveryCandidate(candidate ForwardRecoveryCandidate) ForwardRecoveryCursor {
	return ForwardRecoveryCursor{
		NextRecoveryAt: candidate.NextRecoveryAt.UTC(),
		DocumentID:     candidate.DocumentID,
	}
}

func compareForwardRecoveryCursor(left, right ForwardRecoveryCursor) int {
	if left.NextRecoveryAt.Before(right.NextRecoveryAt) {
		return -1
	}
	if left.NextRecoveryAt.After(right.NextRecoveryAt) {
		return 1
	}
	if left.DocumentID < right.DocumentID {
		return -1
	}
	if left.DocumentID > right.DocumentID {
		return 1
	}
	return 0
}

func validForwardRecoveryDocumentID(value string) bool {
	if value == "" || len(value) > 1500 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character == '/' || character == 0 {
			return false
		}
	}
	return true
}

func validRecoveryReservationKey(value string) bool {
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
