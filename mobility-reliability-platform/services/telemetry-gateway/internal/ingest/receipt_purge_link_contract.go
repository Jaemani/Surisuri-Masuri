package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	ReceiptPurgeLinkSchemaVersion = "ingest-purge-link.v1"
	receiptPurgeLinkIDVersion     = "ingest-purge-link@1"
)

var ErrInvalidReceiptPurgeLink = errors.New("receipt purge inverse link is invalid")

type ReceiptPurgeLinkKind string

const (
	ReceiptPurgeLinkCleanupTarget    ReceiptPurgeLinkKind = "cleanup_target"
	ReceiptPurgeLinkIntegrityFinding ReceiptPurgeLinkKind = "integrity_finding"
)

// ReceiptPurgeLinkedChildIdentity is the bounded immutable identity shared by
// a top-level receipt-owned control document and its nested inverse link. It
// deliberately excludes document paths, payloads, Firebase UID and
// device/trip/person identity.
type ReceiptPurgeLinkedChildIdentity struct {
	TenantID   string
	ReceiptID  string
	Kind       ReceiptPurgeLinkKind
	DocumentID string
	CreatedAt  time.Time
}

// ReceiptPurgeInverseLink is stored below the receipt. LinkID is the nested
// Firestore document ID and is repeated here only so strict decoders can bind
// a snapshot's ID to its body before any create, backfill or purge mutation.
type ReceiptPurgeInverseLink struct {
	SchemaVersion string
	LinkID        string
	TenantID      string
	ReceiptID     string
	Kind          ReceiptPurgeLinkKind
	DocumentID    string
	CreatedAt     time.Time
}

func DeriveReceiptPurgeLinkID(kind ReceiptPurgeLinkKind, documentID string) (string, error) {
	if !validReceiptPurgeLinkedDocumentID(kind, documentID) {
		return "", ErrInvalidReceiptPurgeLink
	}
	digest := sha256.Sum256([]byte(
		receiptPurgeLinkIDVersion + "\x00" + string(kind) + "\x00" + documentID,
	))
	return hex.EncodeToString(digest[:]), nil
}

func BuildReceiptPurgeInverseLink(
	child ReceiptPurgeLinkedChildIdentity,
) (ReceiptPurgeInverseLink, error) {
	child.CreatedAt = child.CreatedAt.UTC()
	if ValidateReceiptPurgeLinkedChildIdentity(child) != nil {
		return ReceiptPurgeInverseLink{}, ErrInvalidReceiptPurgeLink
	}
	linkID, err := DeriveReceiptPurgeLinkID(child.Kind, child.DocumentID)
	if err != nil {
		return ReceiptPurgeInverseLink{}, ErrInvalidReceiptPurgeLink
	}
	link := ReceiptPurgeInverseLink{
		SchemaVersion: ReceiptPurgeLinkSchemaVersion,
		LinkID:        linkID,
		TenantID:      child.TenantID,
		ReceiptID:     child.ReceiptID,
		Kind:          child.Kind,
		DocumentID:    child.DocumentID,
		CreatedAt:     child.CreatedAt,
	}
	if ValidateReceiptPurgeInverseLink(link) != nil {
		return ReceiptPurgeInverseLink{}, ErrInvalidReceiptPurgeLink
	}
	return link, nil
}

func ValidateReceiptPurgeLinkedChildIdentity(
	child ReceiptPurgeLinkedChildIdentity,
) error {
	if !telemetry.IsUUID(child.TenantID) || !telemetry.IsUUID(child.ReceiptID) ||
		!validReceiptPurgeLinkedDocumentID(child.Kind, child.DocumentID) ||
		!validCleanupFirestoreTimestamp(child.CreatedAt.UTC()) {
		return ErrInvalidReceiptPurgeLink
	}
	return nil
}

// ValidateReceiptPurgeInverseLinkContext binds a decoded body to the actual
// nested Firestore snapshot and its expected parent receipt. Callers must use
// this contextual validator before treating a link as create replay, backfill
// coverage or purge authority.
func ValidateReceiptPurgeInverseLinkContext(
	link ReceiptPurgeInverseLink,
	snapshotDocumentID string,
	expectedTenantID string,
	expectedReceiptID string,
) error {
	if ValidateReceiptPurgeInverseLink(link) != nil ||
		link.LinkID != snapshotDocumentID || link.TenantID != expectedTenantID ||
		link.ReceiptID != expectedReceiptID || !telemetry.IsUUID(expectedTenantID) ||
		!telemetry.IsUUID(expectedReceiptID) {
		return ErrInvalidReceiptPurgeLink
	}
	return nil
}

func ValidateReceiptPurgeInverseLink(link ReceiptPurgeInverseLink) error {
	linkID, err := DeriveReceiptPurgeLinkID(link.Kind, link.DocumentID)
	if err != nil || link.SchemaVersion != ReceiptPurgeLinkSchemaVersion ||
		link.LinkID != linkID || !telemetry.IsUUID(link.TenantID) ||
		!telemetry.IsUUID(link.ReceiptID) ||
		!validCleanupFirestoreTimestamp(link.CreatedAt.UTC()) {
		return ErrInvalidReceiptPurgeLink
	}
	return nil
}

func ValidateReceiptPurgeInverseLinkPair(
	link ReceiptPurgeInverseLink,
	child ReceiptPurgeLinkedChildIdentity,
) error {
	if ValidateReceiptPurgeInverseLink(link) != nil ||
		ValidateReceiptPurgeLinkedChildIdentity(child) != nil ||
		link.TenantID != child.TenantID || link.ReceiptID != child.ReceiptID ||
		link.Kind != child.Kind || link.DocumentID != child.DocumentID ||
		!link.CreatedAt.Equal(child.CreatedAt) {
		return ErrInvalidReceiptPurgeLink
	}
	return nil
}

func validReceiptPurgeLinkKind(kind ReceiptPurgeLinkKind) bool {
	switch kind {
	case ReceiptPurgeLinkCleanupTarget, ReceiptPurgeLinkIntegrityFinding:
		return true
	default:
		return false
	}
}

func validReceiptPurgeLinkedDocumentID(kind ReceiptPurgeLinkKind, documentID string) bool {
	if !validReceiptPurgeLinkKind(kind) || !validFirestoreDocumentID(documentID) {
		return false
	}
	if kind == ReceiptPurgeLinkCleanupTarget {
		return telemetry.IsUUID(documentID)
	}
	// Integrity finding storage has not been implemented yet. Its future strict
	// child codec owns semantic ID validation; the inverse registry must also be
	// able to cover safe legacy Firestore IDs during backfill.
	return true
}

func validFirestoreDocumentID(documentID string) bool {
	if !safeRecoveryDocumentID(documentID, 1500) || !utf8.ValidString(documentID) ||
		documentID == "." || documentID == ".." {
		return false
	}
	return len(documentID) < 4 ||
		!strings.HasPrefix(documentID, "__") || !strings.HasSuffix(documentID, "__")
}
