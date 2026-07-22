package ingest

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateForwardRecoveryCandidateRejectsIdentityAndCursorMaterial(t *testing.T) {
	candidate := ForwardRecoveryCandidate{
		TenantID:       "11111111-1111-4111-8111-111111111111",
		ReservationKey: strings.Repeat("a", 64),
		DocumentID:     "22222222-2222-4222-8222-222222222222",
		ReceiptID:      "22222222-2222-4222-8222-222222222222",
		State:          ReceiptReserved,
		NextRecoveryAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
	}
	if err := ValidateForwardRecoveryCandidate(candidate); err != nil {
		t.Fatalf("valid candidate error = %v", err)
	}
	invalid := candidate
	invalid.ReservationKey = strings.Repeat("A", 64)
	if err := ValidateForwardRecoveryCandidate(invalid); !errors.Is(err, ErrInvalidForwardRecoveryCandidate) {
		t.Fatalf("uppercase key error = %v", err)
	}
	invalid = candidate
	invalid.ReceiptID = "firebase-uid"
	if err := ValidateForwardRecoveryCandidate(invalid); !errors.Is(err, ErrInvalidForwardRecoveryCandidate) {
		t.Fatalf("invalid receipt error = %v", err)
	}
}

func TestValidateForwardRecoveryCandidatePageRequiresStrictCursorAdvance(t *testing.T) {
	cutoff := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	first := workerCandidate(1, cutoff.Add(-2*time.Minute), "a")
	second := workerCandidate(2, cutoff.Add(-time.Minute), "b")
	cursor := cursorForForwardRecoveryCandidate(second)
	page := ForwardRecoveryCandidatePage{
		Candidates: []ForwardRecoveryCandidate{first, second},
		NextCursor: &cursor,
	}
	if err := validateForwardRecoveryCandidatePage(page, workerTenantID, cutoff, nil, 2); err != nil {
		t.Fatalf("valid page error = %v", err)
	}
	reversed := page
	reversed.Candidates = []ForwardRecoveryCandidate{second, first}
	if err := validateForwardRecoveryCandidatePage(reversed, workerTenantID, cutoff, nil, 2); !errors.Is(err, ErrInvalidForwardRecoveryCandidatePage) {
		t.Fatalf("reversed page error = %v", err)
	}
	stale := cursorForForwardRecoveryCandidate(second)
	if err := validateForwardRecoveryCandidatePage(page, workerTenantID, cutoff, &stale, 2); !errors.Is(err, ErrInvalidForwardRecoveryCandidatePage) {
		t.Fatalf("nonadvancing page error = %v", err)
	}
}
