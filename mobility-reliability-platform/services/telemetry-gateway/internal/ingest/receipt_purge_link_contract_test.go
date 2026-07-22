package ingest

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildReceiptPurgeInverseLinkBindsExactChildIdentity(t *testing.T) {
	createdAt := time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC)
	for _, kind := range []ReceiptPurgeLinkKind{
		ReceiptPurgeLinkCleanupTarget,
		ReceiptPurgeLinkIntegrityFinding,
	} {
		t.Run(string(kind), func(t *testing.T) {
			child := receiptPurgeLinkedChildFixture(kind, createdAt)
			link, err := BuildReceiptPurgeInverseLink(child)
			if err != nil {
				t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
			}
			if ValidateReceiptPurgeInverseLink(link) != nil ||
				ValidateReceiptPurgeInverseLinkPair(link, child) != nil ||
				link.SchemaVersion != ReceiptPurgeLinkSchemaVersion ||
				len(link.LinkID) != 64 || link.LinkID != strings.ToLower(link.LinkID) {
				t.Fatalf("inverse link = %#v", link)
			}
			replay, replayErr := BuildReceiptPurgeInverseLink(child)
			if replayErr != nil || replay != link {
				t.Fatalf("deterministic replay = %#v, %v; want %#v", replay, replayErr, link)
			}
		})
	}
}

func TestReceiptPurgeLinkIDSeparatesKindAndDocument(t *testing.T) {
	firstDocument := "019c5af2-7a6b-7d1a-850c-02dc21343a25"
	secondDocument := "019c5af2-7a6b-7d1a-850c-02dc21343a26"
	targetID, err := DeriveReceiptPurgeLinkID(ReceiptPurgeLinkCleanupTarget, firstDocument)
	if err != nil {
		t.Fatalf("DeriveReceiptPurgeLinkID(cleanup target) = %v", err)
	}
	findingID, err := DeriveReceiptPurgeLinkID(ReceiptPurgeLinkIntegrityFinding, firstDocument)
	if err != nil {
		t.Fatalf("DeriveReceiptPurgeLinkID(integrity finding) = %v", err)
	}
	otherID, err := DeriveReceiptPurgeLinkID(ReceiptPurgeLinkCleanupTarget, secondDocument)
	if err != nil {
		t.Fatalf("DeriveReceiptPurgeLinkID(other document) = %v", err)
	}
	if targetID == findingID || targetID == otherID || findingID == otherID {
		t.Fatalf("link IDs are not domain separated: %q %q %q", targetID, findingID, otherID)
	}
}

func TestReceiptPurgeLinkIDGoldenVectors(t *testing.T) {
	tests := []struct {
		name       string
		kind       ReceiptPurgeLinkKind
		documentID string
		want       string
	}{
		{
			name: "cleanup target UUID", kind: ReceiptPurgeLinkCleanupTarget,
			documentID: "019c5af2-7a6b-7d1a-850c-02dc21343a25",
			want:       "9443a3ca2bfad8a79247c4826b2b8d9f4f32cc67f86f6927d87ed56f8b4b36a0",
		},
		{
			name: "legacy integrity finding ID", kind: ReceiptPurgeLinkIntegrityFinding,
			documentID: "finding-legacy-001",
			want:       "d4d8df03da1dc2c8e0c9b623d9170fecc2be54c3f6df39e1f3bb6466c785a094",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DeriveReceiptPurgeLinkID(test.kind, test.documentID)
			if err != nil || got != test.want {
				t.Fatalf("DeriveReceiptPurgeLinkID() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestValidateReceiptPurgeInverseLinkRejectsBindingDrift(t *testing.T) {
	child := receiptPurgeLinkedChildFixture(
		ReceiptPurgeLinkCleanupTarget,
		time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC),
	)
	valid, err := BuildReceiptPurgeInverseLink(child)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*ReceiptPurgeInverseLink)
	}{
		{name: "schema", mutate: func(link *ReceiptPurgeInverseLink) { link.SchemaVersion = "future" }},
		{name: "link id", mutate: func(link *ReceiptPurgeInverseLink) { link.LinkID = strings.Repeat("0", 64) }},
		{name: "tenant", mutate: func(link *ReceiptPurgeInverseLink) { link.TenantID = "foreign" }},
		{name: "receipt", mutate: func(link *ReceiptPurgeInverseLink) { link.ReceiptID = "foreign" }},
		{name: "kind", mutate: func(link *ReceiptPurgeInverseLink) { link.Kind = "future" }},
		{name: "document", mutate: func(link *ReceiptPurgeInverseLink) { link.DocumentID = "foreign" }},
		{name: "created at", mutate: func(link *ReceiptPurgeInverseLink) { link.CreatedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := ValidateReceiptPurgeInverseLink(candidate); !errors.Is(err, ErrInvalidReceiptPurgeLink) {
				t.Fatalf("ValidateReceiptPurgeInverseLink() = %v", err)
			}
		})
	}
}

func TestValidateReceiptPurgeInverseLinkPairRejectsChildDrift(t *testing.T) {
	child := receiptPurgeLinkedChildFixture(
		ReceiptPurgeLinkCleanupTarget,
		time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC),
	)
	link, err := BuildReceiptPurgeInverseLink(child)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*ReceiptPurgeLinkedChildIdentity)
	}{
		{name: "tenant", mutate: func(value *ReceiptPurgeLinkedChildIdentity) {
			value.TenantID = "019c5af2-7a6b-7d1a-850c-02dc21343a28"
		}},
		{name: "receipt", mutate: func(value *ReceiptPurgeLinkedChildIdentity) {
			value.ReceiptID = "019c5af2-7a6b-7d1a-850c-02dc21343a29"
		}},
		{name: "kind", mutate: func(value *ReceiptPurgeLinkedChildIdentity) {
			value.Kind = ReceiptPurgeLinkIntegrityFinding
		}},
		{name: "document", mutate: func(value *ReceiptPurgeLinkedChildIdentity) {
			value.DocumentID = "019c5af2-7a6b-7d1a-850c-02dc21343a30"
		}},
		{name: "created at", mutate: func(value *ReceiptPurgeLinkedChildIdentity) {
			value.CreatedAt = value.CreatedAt.Add(time.Second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := child
			test.mutate(&candidate)
			if err := ValidateReceiptPurgeInverseLinkPair(link, candidate); !errors.Is(err, ErrInvalidReceiptPurgeLink) {
				t.Fatalf("ValidateReceiptPurgeInverseLinkPair() = %v", err)
			}
		})
	}
}

func TestValidateReceiptPurgeInverseLinkContextRejectsMisfiledSnapshot(t *testing.T) {
	child := receiptPurgeLinkedChildFixture(
		ReceiptPurgeLinkCleanupTarget,
		time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC),
	)
	link, err := BuildReceiptPurgeInverseLink(child)
	if err != nil {
		t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
	}
	if err := ValidateReceiptPurgeInverseLinkContext(
		link, link.LinkID, child.TenantID, child.ReceiptID,
	); err != nil {
		t.Fatalf("ValidateReceiptPurgeInverseLinkContext(valid) = %v", err)
	}
	tests := []struct {
		name       string
		documentID string
		tenantID   string
		receiptID  string
	}{
		{
			name: "snapshot document ID", documentID: strings.Repeat("0", 64),
			tenantID: child.TenantID, receiptID: child.ReceiptID,
		},
		{
			name: "parent tenant", documentID: link.LinkID,
			tenantID: "019c5af2-7a6b-7d1a-850c-02dc21343a28", receiptID: child.ReceiptID,
		},
		{
			name: "parent receipt", documentID: link.LinkID,
			tenantID: child.TenantID, receiptID: "019c5af2-7a6b-7d1a-850c-02dc21343a29",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateReceiptPurgeInverseLinkContext(
				link, test.documentID, test.tenantID, test.receiptID,
			); !errors.Is(err, ErrInvalidReceiptPurgeLink) {
				t.Fatalf("ValidateReceiptPurgeInverseLinkContext() = %v", err)
			}
		})
	}
}

func TestBuildReceiptPurgeInverseLinkRejectsInvalidChild(t *testing.T) {
	valid := receiptPurgeLinkedChildFixture(
		ReceiptPurgeLinkCleanupTarget,
		time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC),
	)
	tests := []struct {
		name   string
		mutate func(*ReceiptPurgeLinkedChildIdentity)
	}{
		{name: "tenant", mutate: func(value *ReceiptPurgeLinkedChildIdentity) { value.TenantID = "" }},
		{name: "receipt", mutate: func(value *ReceiptPurgeLinkedChildIdentity) { value.ReceiptID = "" }},
		{name: "kind", mutate: func(value *ReceiptPurgeLinkedChildIdentity) { value.Kind = "" }},
		{name: "document", mutate: func(value *ReceiptPurgeLinkedChildIdentity) { value.DocumentID = "" }},
		{name: "created at", mutate: func(value *ReceiptPurgeLinkedChildIdentity) { value.CreatedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if _, err := BuildReceiptPurgeInverseLink(candidate); !errors.Is(err, ErrInvalidReceiptPurgeLink) {
				t.Fatalf("BuildReceiptPurgeInverseLink() = %v", err)
			}
		})
	}
}

func TestReceiptPurgeLinkedDocumentIDPolicyIsKindSpecific(t *testing.T) {
	createdAt := time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC)
	legacyFinding := receiptPurgeLinkedChildFixture(ReceiptPurgeLinkIntegrityFinding, createdAt)
	legacyFinding.DocumentID = "finding-legacy-001"
	if _, err := BuildReceiptPurgeInverseLink(legacyFinding); err != nil {
		t.Fatalf("legacy integrity finding ID rejected: %v", err)
	}
	nonUUIDTarget := receiptPurgeLinkedChildFixture(ReceiptPurgeLinkCleanupTarget, createdAt)
	nonUUIDTarget.DocumentID = legacyFinding.DocumentID
	if _, err := BuildReceiptPurgeInverseLink(nonUUIDTarget); !errors.Is(err, ErrInvalidReceiptPurgeLink) {
		t.Fatalf("non-UUID cleanup target ID accepted: %v", err)
	}
	for _, invalid := range []string{
		"",
		"nested/document",
		"line\nbreak",
		string([]byte{0xff}),
		".",
		"..",
		"__reserved__",
		strings.Repeat("a", 1501),
	} {
		candidate := legacyFinding
		candidate.DocumentID = invalid
		if _, err := BuildReceiptPurgeInverseLink(candidate); !errors.Is(err, ErrInvalidReceiptPurgeLink) {
			t.Fatalf("unsafe integrity finding ID %q accepted: %v", invalid, err)
		}
	}
	for _, valid := range []string{
		"__",
		"__prefix",
		"suffix__",
		strings.Repeat("a", 1500),
	} {
		candidate := legacyFinding
		candidate.DocumentID = valid
		if _, err := BuildReceiptPurgeInverseLink(candidate); err != nil {
			t.Fatalf("Firestore-legal integrity finding ID length=%d rejected: %v", len(valid), err)
		}
	}
}

func receiptPurgeLinkedChildFixture(
	kind ReceiptPurgeLinkKind,
	createdAt time.Time,
) ReceiptPurgeLinkedChildIdentity {
	return ReceiptPurgeLinkedChildIdentity{
		TenantID:   "019c5af2-7a6b-7d1a-850c-02dc21343a23",
		ReceiptID:  "019c5af2-7a6b-7d1a-850c-02dc21343a24",
		Kind:       kind,
		DocumentID: "019c5af2-7a6b-7d1a-850c-02dc21343a25",
		CreatedAt:  createdAt,
	}
}
