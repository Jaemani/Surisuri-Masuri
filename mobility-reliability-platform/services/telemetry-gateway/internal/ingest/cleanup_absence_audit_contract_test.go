package ingest

import (
	"errors"
	"testing"
	"time"
)

func TestCleanupAbsenceAuditRequestBindsExactRawAndManifestProgress(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	ledger = advanceCleanupLedgerForAuditTest(t, plan, ledger,
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second)},
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteObserved, ObservedAt: now.Add(2 * time.Second)},
	)
	rawRequest, err := BuildCleanupAbsenceAuditRequest(plan, ledger, CleanupAbsenceAuditRaw, now.Add(2*time.Second))
	if err != nil || ValidateCleanupAbsenceAuditRequest(rawRequest) != nil {
		t.Fatalf("raw request = %#v, %v", rawRequest, err)
	}
	if rawRequest.ExpectedPath != plan.ExpectedRawPath ||
		rawRequest.NextPhase != CleanupExecutionPhaseRawAbsenceConfirmed ||
		rawRequest.ExpectedLedgerRevision != ledger.Revision || !isLowerHexDigest(rawRequest.RequestHash) {
		t.Fatalf("raw request binding = %#v", rawRequest)
	}
	if _, err := BuildCleanupAbsenceAuditRequest(
		plan, ledger, CleanupAbsenceAuditManifest, now.Add(2*time.Second),
	); !errors.Is(err, ErrCleanupExecutionConflict) {
		t.Fatalf("early manifest request error = %v", err)
	}

	ledger = advanceCleanupLedgerForAuditTest(t, plan, ledger,
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseRawAbsenceConfirmed, AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second)},
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseManifestDispatchRecorded, ObservedAt: now.Add(4 * time.Second)},
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseManifestOutcomeRecorded, DeleteOutcome: CleanupDeleteNotFound, ObservedAt: now.Add(5 * time.Second)},
	)
	manifestRequest, err := BuildCleanupAbsenceAuditRequest(
		plan, ledger, CleanupAbsenceAuditManifest, now.Add(5*time.Second),
	)
	if err != nil || manifestRequest.ExpectedPath != plan.ExpectedManifestPath ||
		manifestRequest.NextPhase != CleanupExecutionPhaseManifestAbsenceConfirmed {
		t.Fatalf("manifest request = %#v, %v", manifestRequest, err)
	}
}

func TestCleanupAbsenceAuditObservationBuildsEvidenceOnlyProgressCommand(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationNone)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	ledger = advanceCleanupLedgerForAuditTest(t, plan, ledger,
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second)},
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteNotAttempted, ObservedAt: now.Add(2 * time.Second)},
	)
	request, err := BuildCleanupAbsenceAuditRequest(plan, ledger, CleanupAbsenceAuditRaw, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("BuildCleanupAbsenceAuditRequest() = %v", err)
	}
	observation := CleanupAbsenceAuditObservation{
		RequestHash: request.RequestHash,
		Artifact:    request.Artifact,
		Outcome:     CleanupAuditConfirmedAbsent,
		ObservedAt:  now.Add(3 * time.Second),
	}
	command, err := BuildCleanupAbsenceAuditProgressCommand(request, observation)
	if err != nil || command.Phase != CleanupExecutionPhaseRawAbsenceConfirmed ||
		command.AuditOutcome != CleanupAuditConfirmedAbsent ||
		command.DeleteOutcome != "" || !CleanupExecutionProgressRequiresAbsenceEvidence(command) {
		t.Fatalf("audit command = %#v, %v", command, err)
	}

	for name, mutate := range map[string]func(*CleanupAbsenceAuditObservation){
		"request hash": func(value *CleanupAbsenceAuditObservation) {
			value.RequestHash = differentCleanupExecutionDigest(value.RequestHash)
		},
		"artifact":     func(value *CleanupAbsenceAuditObservation) { value.Artifact = CleanupAbsenceAuditManifest },
		"outcome":      func(value *CleanupAbsenceAuditObservation) { value.Outcome = "unknown" },
		"exact expiry": func(value *CleanupAbsenceAuditObservation) { value.ObservedAt = request.ExpectedFence.ExpiresAt },
	} {
		t.Run(name, func(t *testing.T) {
			forged := observation
			mutate(&forged)
			if _, err := BuildCleanupAbsenceAuditProgressCommand(request, forged); !errors.Is(err, ErrInvalidCleanupExecutionLedger) {
				t.Fatalf("forged observation error = %v", err)
			}
		})
	}
}

func TestCleanupAbsenceAuditRequestRejectsTamperAndWrongPhase(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	if _, err := BuildCleanupAbsenceAuditRequest(plan, ledger, CleanupAbsenceAuditRaw, now); !errors.Is(err, ErrCleanupExecutionConflict) {
		t.Fatalf("planned audit request error = %v", err)
	}
	ledger = advanceCleanupLedgerForAuditTest(t, plan, ledger,
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second)},
		CleanupExecutionTransition{Phase: CleanupExecutionPhaseRawOutcomeRecorded, DeleteOutcome: CleanupDeleteObserved, ObservedAt: now.Add(2 * time.Second)},
	)
	request, err := BuildCleanupAbsenceAuditRequest(plan, ledger, CleanupAbsenceAuditRaw, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("BuildCleanupAbsenceAuditRequest() = %v", err)
	}
	for name, mutate := range map[string]func(*CleanupAbsenceAuditRequest){
		"path": func(value *CleanupAbsenceAuditRequest) { value.ExpectedPath += ".forged" },
		"plan hash": func(value *CleanupAbsenceAuditRequest) {
			value.ExpectedPlanHash = differentCleanupExecutionDigest(value.ExpectedPlanHash)
		},
		"revision": func(value *CleanupAbsenceAuditRequest) { value.ExpectedLedgerRevision++ },
		"phase": func(value *CleanupAbsenceAuditRequest) {
			value.NextPhase = CleanupExecutionPhaseManifestAbsenceConfirmed
		},
		"artifact": func(value *CleanupAbsenceAuditRequest) { value.Artifact = CleanupAbsenceAuditManifest },
	} {
		t.Run(name, func(t *testing.T) {
			forged := request
			mutate(&forged)
			if ValidateCleanupAbsenceAuditRequest(forged) == nil {
				t.Fatal("tampered request remained valid")
			}
		})
	}
}

func TestCleanupAbsenceAuditRequestRejectsUnknownDeleteOutcome(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("raw dispatch = %v", err)
	}
	ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, CleanupExecutionTransition{
		Phase:         CleanupExecutionPhaseRawOutcomeRecorded,
		DeleteOutcome: CleanupDeleteUnknown,
		ErrorClass:    CleanupExecutionErrorProviderTimeout,
		ObservedAt:    now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("raw unknown outcome = %v", err)
	}
	if _, err := BuildCleanupAbsenceAuditRequest(
		plan, ledger, CleanupAbsenceAuditRaw, now.Add(2*time.Second),
	); !errors.Is(err, ErrCleanupExecutionConflict) {
		t.Fatalf("unknown raw audit request error = %v", err)
	}
	if _, err := AdvanceCleanupExecutionLedger(plan, ledger, CleanupExecutionTransition{
		Phase:        CleanupExecutionPhaseRawAbsenceConfirmed,
		AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second),
	}); !errors.Is(err, ErrCleanupExecutionConflict) {
		t.Fatalf("unknown raw audit transition error = %v", err)
	}
}

func advanceCleanupLedgerForAuditTest(
	t *testing.T,
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	steps ...CleanupExecutionTransition,
) CleanupExecutionLedger {
	t.Helper()
	var err error
	for _, step := range steps {
		ledger, err = AdvanceCleanupExecutionLedger(plan, ledger, step)
		if err != nil {
			t.Fatalf("AdvanceCleanupExecutionLedger(%q) = %v", step.Phase, err)
		}
	}
	return ledger
}
