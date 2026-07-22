package cleanupattest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupAbsenceEvidenceBindsRequestAuthorizationAndObservation(t *testing.T) {
	verifier, sign := cleanupAbsenceEvidenceSigner(t)
	now := time.Now().UTC()
	request := cleanupAbsenceEvidenceRequest(now)
	binding := strings.Repeat("e", 64)
	evidence, err := NewCleanupAbsenceEvidence(request, binding, now, sign)
	if err != nil {
		t.Fatalf("NewCleanupAbsenceEvidence() = %v", err)
	}
	observation, err := verifier.VerifyCleanupAbsenceEvidence(request, binding, evidence)
	if err != nil || observation.RequestHash != request.RequestHash ||
		observation.Artifact != request.Artifact ||
		observation.Outcome != ingest.CleanupAuditConfirmedAbsent ||
		!observation.ObservedAt.Equal(now) {
		t.Fatalf("VerifyCleanupAbsenceEvidence() = %#v, %v", observation, err)
	}

	otherVerifier, otherSign := cleanupAbsenceEvidenceSigner(t)
	if _, err := otherVerifier.VerifyCleanupAbsenceEvidence(
		request, binding, evidence,
	); !errors.Is(err, ErrInvalidCleanupAbsenceEvidence) {
		t.Fatalf("wrong verifier error = %v", err)
	}
	otherEvidence, err := NewCleanupAbsenceEvidence(request, binding, now, otherSign)
	if err != nil {
		t.Fatalf("NewCleanupAbsenceEvidence(other) = %v", err)
	}
	if _, err := verifier.VerifyCleanupAbsenceEvidence(
		request, binding, otherEvidence,
	); !errors.Is(err, ErrInvalidCleanupAbsenceEvidence) {
		t.Fatalf("wrong issuer error = %v", err)
	}
	if _, err := verifier.VerifyCleanupAbsenceEvidence(
		request, strings.Repeat("f", 64), evidence,
	); !errors.Is(err, ErrInvalidCleanupAbsenceEvidence) {
		t.Fatalf("wrong authorization binding error = %v", err)
	}
}

func TestCleanupAbsenceEvidenceRejectsMissingSignerAndInvalidTime(t *testing.T) {
	now := time.Now().UTC()
	request := cleanupAbsenceEvidenceRequest(now)
	binding := strings.Repeat("e", 64)
	if _, err := NewCleanupAbsenceEvidence(
		request, binding, now, nil,
	); !errors.Is(err, ErrInvalidCleanupAbsenceEvidence) {
		t.Fatalf("missing signer error = %v", err)
	}
	_, sign := cleanupAbsenceEvidenceSigner(t)
	if _, err := NewCleanupAbsenceEvidence(
		request, binding, request.ExpectedFence.ExpiresAt, sign,
	); !errors.Is(err, ErrInvalidCleanupAbsenceEvidence) {
		t.Fatalf("exact expiry issue error = %v", err)
	}
	if _, err := (Verifier{}).VerifyCleanupAbsenceEvidence(
		request, binding, Evidence{},
	); !errors.Is(err, ErrInvalidCleanupAbsenceEvidence) {
		t.Fatalf("zero verifier error = %v", err)
	}
}

func cleanupAbsenceEvidenceSigner(
	t *testing.T,
) (Verifier, func([]byte) ([]byte, error)) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() = %v", err)
	}
	verifier, err := NewVerifier(publicKey)
	if err != nil {
		t.Fatalf("NewVerifier() = %v", err)
	}
	return verifier, func(payload []byte) ([]byte, error) {
		return ed25519.Sign(privateKey, payload), nil
	}
}

func cleanupAbsenceEvidenceRequest(now time.Time) ingest.CleanupAbsenceAuditRequest {
	request := ingest.CleanupAbsenceAuditRequest{
		Query: ingest.CleanupExecutionQuery{
			TenantID:       "11111111-1111-4111-8111-111111111111",
			ReservationKey: strings.Repeat("a", 64),
			AttemptID:      "77777777-7777-4777-8777-777777777777",
		},
		ExpectedTargetHash:      strings.Repeat("b", 64),
		ExpectedPlanHash:        strings.Repeat("c", 64),
		ExpectedReceiptRevision: 5,
		ExpectedFence: ingest.LeaseFence{
			OwnerID: "77777777-7777-4777-8777-777777777777",
			Token:   4, ExpiresAt: now.Add(time.Minute),
		},
		ExpectedLedgerRevision: 3,
		NextPhase:              ingest.CleanupExecutionPhaseRawAbsenceConfirmed,
		Artifact:               ingest.CleanupAbsenceAuditRaw,
		ExpectedPath:           "telemetry/tenant/raw.json.gz",
	}
	request.RequestHash = cleanupAbsenceEvidenceRequestHash(request)
	return request
}

func cleanupAbsenceEvidenceRequestHash(request ingest.CleanupAbsenceAuditRequest) string {
	encoder := &cleanupAbsenceEvidenceTestEncoder{digest: sha256.New()}
	encoder.addString("cleanup-absence-audit-request@1")
	encoder.addString(request.Query.TenantID)
	encoder.addString(request.Query.ReservationKey)
	encoder.addString(request.Query.AttemptID)
	encoder.addString(request.ExpectedTargetHash)
	encoder.addString(request.ExpectedPlanHash)
	encoder.addInt64(request.ExpectedReceiptRevision)
	encoder.addString(request.ExpectedFence.OwnerID)
	encoder.addInt64(request.ExpectedFence.Token)
	encoder.addTime(request.ExpectedFence.ExpiresAt)
	encoder.addInt64(request.ExpectedLedgerRevision)
	encoder.addString(string(request.NextPhase))
	encoder.addString(string(request.Artifact))
	encoder.addString(request.ExpectedPath)
	return hex.EncodeToString(encoder.digest.Sum(nil))
}

type cleanupAbsenceEvidenceTestEncoder struct {
	digest hash.Hash
}

func (e *cleanupAbsenceEvidenceTestEncoder) addString(value string) {
	e.addBytes([]byte(value))
}

func (e *cleanupAbsenceEvidenceTestEncoder) addBytes(value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = e.digest.Write(length[:])
	_, _ = e.digest.Write(value)
}

func (e *cleanupAbsenceEvidenceTestEncoder) addInt64(value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	e.addBytes(encoded[:])
}

func (e *cleanupAbsenceEvidenceTestEncoder) addTime(value time.Time) {
	e.addString(value.UTC().Format(time.RFC3339Nano))
}
