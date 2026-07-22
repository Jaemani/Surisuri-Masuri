package cleanupattest

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const cleanupAbsenceEvidenceBindingVersion = "cleanup-absence-evidence@1"

var ErrInvalidCleanupAbsenceEvidence = errors.New("cleanup absence evidence is invalid")

// Verifier is paired with a provider auditor whose private signing key never
// leaves that adapter. Possessing a read grant or verifier alone cannot
// manufacture accepted absence evidence.
type Verifier struct {
	publicKey [ed25519.PublicKeySize]byte
}

// Evidence is opaque outside this package. Its signed payload is bound to one
// request, one concrete authorization grant, one artifact and one observation
// time.
type Evidence struct {
	requestHash          string
	authorizationBinding string
	artifact             ingest.CleanupAbsenceAuditArtifact
	outcome              ingest.CleanupAuditOutcome
	observedAt           time.Time
	signature            [ed25519.SignatureSize]byte
}

func NewVerifier(publicKey ed25519.PublicKey) (Verifier, error) {
	if len(publicKey) != ed25519.PublicKeySize || allZero(publicKey) {
		return Verifier{}, ErrInvalidCleanupAbsenceEvidence
	}
	var verifier Verifier
	copy(verifier.publicKey[:], publicKey)
	return verifier, nil
}

func (v Verifier) Valid() bool {
	return !allZero(v.publicKey[:])
}

func NewCleanupAbsenceEvidence(
	request ingest.CleanupAbsenceAuditRequest,
	authorizationBinding string,
	observedAt time.Time,
	sign func([]byte) ([]byte, error),
) (Evidence, error) {
	observation := ingest.CleanupAbsenceAuditObservation{
		RequestHash: request.RequestHash,
		Artifact:    request.Artifact,
		Outcome:     ingest.CleanupAuditConfirmedAbsent,
		ObservedAt:  observedAt.UTC(),
	}
	if sign == nil || !isLowerHexDigest(authorizationBinding) ||
		ingest.ValidateCleanupAbsenceAuditObservation(request, observation) != nil {
		return Evidence{}, ErrInvalidCleanupAbsenceEvidence
	}
	evidence := Evidence{
		requestHash: request.RequestHash, authorizationBinding: authorizationBinding,
		artifact: request.Artifact, outcome: observation.Outcome,
		observedAt: observation.ObservedAt,
	}
	signed := cleanupAbsenceEvidencePayload(evidence)
	if len(signed) == 0 {
		return Evidence{}, ErrInvalidCleanupAbsenceEvidence
	}
	signature, err := sign(signed)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Evidence{}, ErrInvalidCleanupAbsenceEvidence
	}
	copy(evidence.signature[:], signature)
	return evidence, nil
}

func (v Verifier) VerifyCleanupAbsenceEvidence(
	request ingest.CleanupAbsenceAuditRequest,
	authorizationBinding string,
	evidence Evidence,
) (ingest.CleanupAbsenceAuditObservation, error) {
	if !v.Valid() || !isLowerHexDigest(authorizationBinding) ||
		evidence.requestHash != request.RequestHash ||
		evidence.authorizationBinding != authorizationBinding ||
		evidence.artifact != request.Artifact ||
		evidence.outcome != ingest.CleanupAuditConfirmedAbsent ||
		evidence.observedAt.IsZero() ||
		!ed25519.Verify(
			ed25519.PublicKey(v.publicKey[:]),
			cleanupAbsenceEvidencePayload(evidence),
			evidence.signature[:],
		) {
		return ingest.CleanupAbsenceAuditObservation{}, ErrInvalidCleanupAbsenceEvidence
	}
	observation := ingest.CleanupAbsenceAuditObservation{
		RequestHash: evidence.requestHash,
		Artifact:    evidence.artifact,
		Outcome:     evidence.outcome,
		ObservedAt:  evidence.observedAt.UTC(),
	}
	if ingest.ValidateCleanupAbsenceAuditObservation(request, observation) != nil {
		return ingest.CleanupAbsenceAuditObservation{}, ErrInvalidCleanupAbsenceEvidence
	}
	return observation, nil
}

func cleanupAbsenceEvidencePayload(evidence Evidence) []byte {
	if !isLowerHexDigest(evidence.requestHash) ||
		!isLowerHexDigest(evidence.authorizationBinding) || evidence.observedAt.IsZero() {
		return nil
	}
	var payload bytes.Buffer
	appendBoundedString(&payload, cleanupAbsenceEvidenceBindingVersion)
	appendBoundedString(&payload, evidence.requestHash)
	appendBoundedString(&payload, evidence.authorizationBinding)
	appendBoundedString(&payload, string(evidence.artifact))
	appendBoundedString(&payload, string(evidence.outcome))
	_ = binary.Write(&payload, binary.BigEndian, evidence.observedAt.UTC().Unix())
	_ = binary.Write(&payload, binary.BigEndian, int32(evidence.observedAt.UTC().Nanosecond()))
	return payload.Bytes()
}

func appendBoundedString(buffer *bytes.Buffer, value string) {
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(value)))
	_, _ = buffer.WriteString(value)
}

func isLowerHexDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == string(bytes.ToLower([]byte(value)))
}

func allZero(value []byte) bool {
	for _, element := range value {
		if element != 0 {
			return false
		}
	}
	return true
}
