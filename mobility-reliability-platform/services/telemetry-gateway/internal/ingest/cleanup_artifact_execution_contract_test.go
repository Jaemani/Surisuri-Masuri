package ingest

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCleanupArtifactExecutionResultHasNoSensitiveSurface(t *testing.T) {
	typeOfResult := reflect.TypeOf(CleanupArtifactExecutionResult{})
	for index := 0; index < typeOfResult.NumField(); index++ {
		name := strings.ToLower(typeOfResult.Field(index).Name)
		for _, forbidden := range []string{
			"path", "message", "credential", "token", "uid", "appid", "coordinate", "payload",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("result field %q exposes forbidden surface %q", name, forbidden)
			}
		}
	}
}

func TestCleanupArtifactExecutionRequestBindsExactPhaseRevisionAndArtifact(t *testing.T) {
	_, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	request, err := BuildCleanupArtifactExecutionRequest(plan, ledger, CleanupArtifactExecutionRaw)
	if err != nil || ValidateCleanupArtifactExecutionRequest(request) != nil {
		t.Fatalf("BuildCleanupArtifactExecutionRequest() = %#v, %v", request, err)
	}
	if request.Artifact != CleanupArtifactExecutionRaw || !request.Targeted ||
		request.Lineage == nil || request.ExpectedLedgerRevision != 1 ||
		request.DispatchRevision != 2 || request.DispatchPhase != CleanupExecutionPhaseRawDispatchRecorded ||
		request.OutcomePhase != CleanupExecutionPhaseRawOutcomeRecorded {
		t.Fatalf("raw execution request = %#v", request)
	}

	tests := []struct {
		name   string
		mutate func(*CleanupArtifactExecutionRequest)
	}{
		{name: "target hash", mutate: func(value *CleanupArtifactExecutionRequest) {
			value.ExpectedTargetHash = differentCleanupExecutionDigest(value.ExpectedTargetHash)
		}},
		{name: "plan hash", mutate: func(value *CleanupArtifactExecutionRequest) {
			value.ExpectedPlanHash = differentCleanupExecutionDigest(value.ExpectedPlanHash)
		}},
		{name: "receipt revision", mutate: func(value *CleanupArtifactExecutionRequest) { value.ExpectedReceiptRevision++ }},
		{name: "fence", mutate: func(value *CleanupArtifactExecutionRequest) { value.ExpectedFence.Token++ }},
		{name: "ledger revision", mutate: func(value *CleanupArtifactExecutionRequest) { value.ExpectedLedgerRevision++ }},
		{name: "dispatch phase", mutate: func(value *CleanupArtifactExecutionRequest) {
			value.DispatchPhase = CleanupExecutionPhaseManifestDispatchRecorded
		}},
		{name: "artifact", mutate: func(value *CleanupArtifactExecutionRequest) { value.Artifact = CleanupArtifactExecutionManifest }},
		{name: "path", mutate: func(value *CleanupArtifactExecutionRequest) { value.ExpectedPath += ".different" }},
		{name: "lineage", mutate: func(value *CleanupArtifactExecutionRequest) { value.Lineage.Generation++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated, cloneErr := CloneCleanupArtifactExecutionRequest(request)
			if cloneErr != nil {
				t.Fatalf("CloneCleanupArtifactExecutionRequest() = %v", cloneErr)
			}
			test.mutate(&mutated)
			if !errors.Is(ValidateCleanupArtifactExecutionRequest(mutated), ErrInvalidCleanupExecutionLedger) {
				t.Fatalf("mutated request remained valid: %#v", mutated)
			}
		})
	}
}

func TestCleanupArtifactExecutionRequestEnforcesRawBeforeManifest(t *testing.T) {
	now, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidComplete)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	if _, err := BuildCleanupArtifactExecutionRequest(
		plan, ledger, CleanupArtifactExecutionManifest,
	); !errors.Is(err, ErrCleanupExecutionConflict) {
		t.Fatalf("early manifest request error = %v", err)
	}
	ledger = advanceCleanupArtifactContractLedger(t, plan, ledger, CleanupExecutionTransition{
		Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second),
	})
	ledger = advanceCleanupArtifactContractLedger(t, plan, ledger, CleanupExecutionTransition{
		Phase:         CleanupExecutionPhaseRawOutcomeRecorded,
		DeleteOutcome: CleanupDeleteObserved, ObservedAt: now.Add(2 * time.Second),
	})
	ledger = advanceCleanupArtifactContractLedger(t, plan, ledger, CleanupExecutionTransition{
		Phase:        CleanupExecutionPhaseRawAbsenceConfirmed,
		AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second),
	})
	request, err := BuildCleanupArtifactExecutionRequest(
		plan, ledger, CleanupArtifactExecutionManifest,
	)
	if err != nil || request.ExpectedLedgerRevision != 4 || request.DispatchRevision != 5 ||
		request.Artifact != CleanupArtifactExecutionManifest {
		t.Fatalf("manifest request = %#v, %v", request, err)
	}
}

func TestCleanupArtifactExecutionResultPreservesUnknownWithBoundedError(t *testing.T) {
	_, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationValidRawOnly)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	request, err := BuildCleanupArtifactExecutionRequest(plan, ledger, CleanupArtifactExecutionRaw)
	if err != nil {
		t.Fatalf("BuildCleanupArtifactExecutionRequest() = %v", err)
	}
	result := CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision: request.DispatchRevision, DeleteOutcome: CleanupDeleteUnknown,
		ErrorClass:        CleanupExecutionErrorProviderTimeout,
		MutationStartedAt: plan.Target.Command.CreatedAt.Add(500 * time.Millisecond),
		ObservedAt:        plan.Target.Command.CreatedAt.Add(time.Second),
	}
	if ValidateCleanupArtifactExecutionResult(request, result) != nil {
		t.Fatalf("unknown result rejected: %#v", result)
	}
	command, err := BuildCleanupArtifactExecutionOutcomeCommand(request, result)
	if err != nil || command.Phase != CleanupExecutionPhaseRawOutcomeRecorded ||
		command.ExpectedLedgerRevision != request.DispatchRevision ||
		command.DeleteOutcome != CleanupDeleteUnknown ||
		command.ErrorClass != CleanupExecutionErrorProviderTimeout {
		t.Fatalf("unknown outcome command = %#v, %v", command, err)
	}

	missingClass := result
	missingClass.ErrorClass = ""
	if !errors.Is(
		ValidateCleanupArtifactExecutionResult(request, missingClass),
		ErrInvalidCleanupExecutionObservation,
	) {
		t.Fatal("unknown result without bounded error class was accepted")
	}
	for _, hardClass := range []CleanupExecutionErrorClass{
		CleanupExecutionErrorQuotaLimited,
		CleanupExecutionErrorInventoryIncomplete,
		CleanupExecutionErrorPermissionDenied,
		CleanupExecutionErrorGenerationDrift,
	} {
		hardFailure := result
		hardFailure.ErrorClass = hardClass
		if !errors.Is(
			ValidateCleanupArtifactExecutionResult(request, hardFailure),
			ErrInvalidCleanupExecutionObservation,
		) {
			t.Fatalf("unknown result accepted non-ambiguous class %q", hardClass)
		}
	}
	missingStart := result
	missingStart.MutationStartedAt = time.Time{}
	if !errors.Is(
		ValidateCleanupArtifactExecutionResult(request, missingStart),
		ErrInvalidCleanupExecutionObservation,
	) {
		t.Fatal("unknown result without trusted mutation start was accepted")
	}
	knownWithError := result
	knownWithError.DeleteOutcome = CleanupDeleteObserved
	if !errors.Is(
		ValidateCleanupArtifactExecutionResult(request, knownWithError),
		ErrInvalidCleanupExecutionObservation,
	) {
		t.Fatal("known result with error class was accepted")
	}
}

func TestCleanupArtifactExecutionNonTargetedAllowsOnlyNotAttempted(t *testing.T) {
	_, plan := cleanupLedgerPlanFixture(t, ArtifactClassificationNone)
	ledger, err := NewCleanupExecutionLedger(plan)
	if err != nil {
		t.Fatalf("NewCleanupExecutionLedger() = %v", err)
	}
	request, err := BuildCleanupArtifactExecutionRequest(plan, ledger, CleanupArtifactExecutionRaw)
	if err != nil || request.Targeted || request.Lineage != nil {
		t.Fatalf("non-targeted request = %#v, %v", request, err)
	}
	result := CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision: request.DispatchRevision, DeleteOutcome: CleanupDeleteNotAttempted,
		ObservedAt: plan.Target.Command.CreatedAt.Add(time.Second),
	}
	if ValidateCleanupArtifactExecutionResult(request, result) != nil {
		t.Fatalf("non-targeted result rejected: %#v", result)
	}
	result.DeleteOutcome = CleanupDeleteObserved
	if !errors.Is(
		ValidateCleanupArtifactExecutionResult(request, result),
		ErrInvalidCleanupExecutionObservation,
	) {
		t.Fatal("non-targeted delete observation was accepted")
	}
}

func TestCleanupArtifactExecutionRequestTargetMatrix(t *testing.T) {
	for _, test := range []struct {
		classification   ArtifactClassification
		rawTargeted      bool
		manifestTargeted bool
	}{
		{classification: ArtifactClassificationValidComplete, rawTargeted: true, manifestTargeted: true},
		{classification: ArtifactClassificationValidRawOnly, rawTargeted: true, manifestTargeted: false},
		{classification: ArtifactClassificationManifestOnly, rawTargeted: false, manifestTargeted: true},
		{classification: ArtifactClassificationNone, rawTargeted: false, manifestTargeted: false},
	} {
		t.Run(string(test.classification), func(t *testing.T) {
			now, plan := cleanupLedgerPlanFixture(t, test.classification)
			ledger, err := NewCleanupExecutionLedger(plan)
			if err != nil {
				t.Fatalf("NewCleanupExecutionLedger() = %v", err)
			}
			raw, err := BuildCleanupArtifactExecutionRequest(
				plan, ledger, CleanupArtifactExecutionRaw,
			)
			if err != nil || raw.Targeted != test.rawTargeted ||
				raw.Targeted != (raw.Lineage != nil) {
				t.Fatalf("raw request = %#v, %v", raw, err)
			}
			ledger = advanceCleanupArtifactContractLedger(t, plan, ledger, CleanupExecutionTransition{
				Phase: CleanupExecutionPhaseRawDispatchRecorded, ObservedAt: now.Add(time.Second),
			})
			rawOutcome := CleanupDeleteNotAttempted
			if test.rawTargeted {
				rawOutcome = CleanupDeleteObserved
			}
			ledger = advanceCleanupArtifactContractLedger(t, plan, ledger, CleanupExecutionTransition{
				Phase:         CleanupExecutionPhaseRawOutcomeRecorded,
				DeleteOutcome: rawOutcome, ObservedAt: now.Add(2 * time.Second),
			})
			ledger = advanceCleanupArtifactContractLedger(t, plan, ledger, CleanupExecutionTransition{
				Phase:        CleanupExecutionPhaseRawAbsenceConfirmed,
				AuditOutcome: CleanupAuditConfirmedAbsent, ObservedAt: now.Add(3 * time.Second),
			})
			manifest, err := BuildCleanupArtifactExecutionRequest(
				plan, ledger, CleanupArtifactExecutionManifest,
			)
			if err != nil || manifest.Targeted != test.manifestTargeted ||
				manifest.Targeted != (manifest.Lineage != nil) {
				t.Fatalf("manifest request = %#v, %v", manifest, err)
			}
		})
	}
}

func advanceCleanupArtifactContractLedger(
	t *testing.T,
	plan CleanupExecutionLedgerPlan,
	ledger CleanupExecutionLedger,
	transition CleanupExecutionTransition,
) CleanupExecutionLedger {
	t.Helper()
	next, err := AdvanceCleanupExecutionLedger(plan, ledger, transition)
	if err != nil {
		t.Fatalf("AdvanceCleanupExecutionLedger(%q) = %v", transition.Phase, err)
	}
	return next
}
