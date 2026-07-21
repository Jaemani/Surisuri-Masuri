package ingest

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPlanForwardRecoveryInitialMatrix(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	observedAt := fixture.request.ReceivedAt.Add(2 * time.Minute)
	tests := []struct {
		name        string
		class       ArtifactClassification
		reason      ArtifactReasonCode
		wantAction  ForwardRecoveryAction
		wantRelease LeaseReleaseCode
		wantHold    RecoveryHoldCode
	}{
		{name: "none waits for client", class: ArtifactClassificationNone, reason: ArtifactReasonNoCandidates, wantAction: ForwardRecoveryActionReleaseLease, wantRelease: LeaseReleaseAwaitingClientReplay},
		{name: "raw only repairs manifest", class: ArtifactClassificationValidRawOnly, reason: ArtifactReasonRawValidManifestAbsent, wantAction: ForwardRecoveryActionCreateManifest},
		{name: "complete requires confirmation", class: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, wantAction: ForwardRecoveryActionConfirmComplete},
		{name: "raw conflict requires confirmation", class: ArtifactClassificationRawContentConflict, reason: ArtifactReasonDecompressedBodyHashMismatch, wantAction: ForwardRecoveryActionConfirmRawConflict},
		{name: "manifest only holds", class: ArtifactClassificationManifestOnly, reason: ArtifactReasonReferencedRawNotFound, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldManifestOnly},
		{name: "manifest conflict holds", class: ArtifactClassificationManifestConflict, reason: ArtifactReasonManifestNoncanonical, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldManifestConflict},
		{name: "metadata conflict holds", class: ArtifactClassificationMetadataConflict, reason: ArtifactReasonRequiredMetadataMismatch, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldMetadataConflict},
		{name: "generation drift holds", class: ArtifactClassificationGenerationDrift, reason: ArtifactReasonMultipleRawGenerations, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldGenerationDrift},
		{name: "permission holds", class: ArtifactClassificationUnavailable, reason: ArtifactReasonPermissionDenied, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldArtifactPermissionDenied},
		{name: "validator holds", class: ArtifactClassificationUnavailable, reason: ArtifactReasonValidatorUnavailable, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldValidatorUnavailable},
		{name: "codec holds", class: ArtifactClassificationUnavailable, reason: ArtifactReasonCodecProfileUnavailable, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldCodecUnavailable},
		{name: "inventory holds", class: ArtifactClassificationUnavailable, reason: ArtifactReasonInventoryCoverageIncomplete, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldInventoryIncomplete},
		{name: "unverifiable holds", class: ArtifactClassificationUnavailable, reason: ArtifactReasonResponseUnverifiable, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldResponseUnverifiable},
		{name: "quota releases", class: ArtifactClassificationUnavailable, reason: ArtifactReasonQuotaLimited, wantAction: ForwardRecoveryActionReleaseLease, wantRelease: LeaseReleaseArtifactUnavailable},
		{name: "timeout releases", class: ArtifactClassificationUnavailable, reason: ArtifactReasonProviderTimeout, wantAction: ForwardRecoveryActionReleaseLease, wantRelease: LeaseReleaseArtifactUnavailable},
		{name: "provider unavailable releases", class: ArtifactClassificationUnavailable, reason: ArtifactReasonProviderUnavailable, wantAction: ForwardRecoveryActionReleaseLease, wantRelease: LeaseReleaseArtifactUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := forwardRecoveryResultFixture(fixture, test.class, test.reason, observedAt)
			plan, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
				Phase:   RecoveryPhaseInitial,
				Request: fixture.request,
				Result:  result,
			})
			if err != nil {
				t.Fatalf("PlanForwardRecoveryAction() = %v", err)
			}
			if plan.Action != test.wantAction || plan.ReleaseCode != test.wantRelease || plan.HoldCode != test.wantHold {
				t.Fatalf("plan = %#v", plan)
			}
		})
	}
}

func TestPlanForwardRecoveryConfirmationRequiresExactCrossPassEvidence(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	firstAt := fixture.request.ReceivedAt.Add(2 * time.Minute)
	secondAt := firstAt.Add(time.Second)
	prior := forwardRecoveryResultFixture(
		fixture,
		ArtifactClassificationValidComplete,
		ArtifactReasonManifestAndReferencedRawValid,
		firstAt,
	)
	current := forwardRecoveryResultFixture(
		fixture,
		ArtifactClassificationValidComplete,
		ArtifactReasonManifestAndReferencedRawValid,
		secondAt,
	)

	plan, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
		Phase:       RecoveryPhaseConfirmation,
		Request:     fixture.request,
		Result:      current,
		PriorResult: &prior,
	})
	if err != nil || plan.Action != ForwardRecoveryActionMarkStored || plan.Raw == nil || plan.Manifest == nil {
		t.Fatalf("PlanForwardRecoveryAction() = %#v, %v", plan, err)
	}

	t.Run("manifest generation replaced", func(t *testing.T) {
		changed := current
		changedPin := *changed.PinnedManifest
		changedPin.Generation++
		changed.PinnedManifest = &changedPin
		plan, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
			Phase: RecoveryPhaseConfirmation, Request: fixture.request, Result: changed, PriorResult: &prior,
		})
		if err != nil || plan.Action != ForwardRecoveryActionMarkHold || plan.HoldCode != RecoveryHoldConfirmationDrift {
			t.Fatalf("PlanForwardRecoveryAction() = %#v, %v", plan, err)
		}
	})

	t.Run("raw metageneration replaced", func(t *testing.T) {
		changed := current
		changedPin := *changed.PinnedRaw
		changedPin.Metageneration++
		changed.PinnedRaw = &changedPin
		plan, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
			Phase: RecoveryPhaseConfirmation, Request: fixture.request, Result: changed, PriorResult: &prior,
		})
		if err != nil || plan.Action != ForwardRecoveryActionMarkHold || plan.HoldCode != RecoveryHoldConfirmationDrift {
			t.Fatalf("PlanForwardRecoveryAction() = %#v, %v", plan, err)
		}
	})
}

func TestPlanForwardRecoveryDiscardsEvidenceAcrossLeaseRenewal(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	firstAt := fixture.request.ReceivedAt.Add(2 * time.Minute)
	prior := forwardRecoveryResultFixture(
		fixture,
		ArtifactClassificationValidComplete,
		ArtifactReasonManifestAndReferencedRawValid,
		firstAt,
	)

	renewedRequest := fixture.request
	renewedRequest.ReceiptRevision++
	renewedFence := *fixture.request.ForwardFence
	renewedFence.ExpiresAt = renewedFence.ExpiresAt.Add(time.Minute)
	renewedRequest.ForwardFence = &renewedFence
	renewedFixture := fixture
	renewedFixture.request = renewedRequest
	current := forwardRecoveryResultFixture(
		renewedFixture,
		ArtifactClassificationValidComplete,
		ArtifactReasonManifestAndReferencedRawValid,
		firstAt.Add(time.Second),
	)

	_, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
		Phase: RecoveryPhaseConfirmation, Request: renewedRequest, Result: current, PriorResult: &prior,
	})
	if !errors.Is(err, ErrInvalidForwardRecoveryPlan) {
		t.Fatalf("PlanForwardRecoveryAction() = %v", err)
	}
}

func TestPlanForwardRecoveryRawConflictRejectsOnlySamePinnedEvidence(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	firstAt := fixture.request.ReceivedAt.Add(2 * time.Minute)
	prior := forwardRecoveryResultFixture(
		fixture,
		ArtifactClassificationRawContentConflict,
		ArtifactReasonPayloadLineageMismatch,
		firstAt,
	)
	current := forwardRecoveryResultFixture(
		fixture,
		ArtifactClassificationRawContentConflict,
		ArtifactReasonPayloadLineageMismatch,
		firstAt.Add(time.Second),
	)
	plan, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
		Phase: RecoveryPhaseConfirmation, Request: fixture.request, Result: current, PriorResult: &prior,
	})
	if err != nil || plan.Action != ForwardRecoveryActionMarkRejected || plan.RejectionCode != "object_conflict" {
		t.Fatalf("PlanForwardRecoveryAction() = %#v, %v", plan, err)
	}

	changed := current
	changed.ReasonCode = ArtifactReasonStrictPayloadInvalid
	plan, err = PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
		Phase: RecoveryPhaseConfirmation, Request: fixture.request, Result: changed, PriorResult: &prior,
	})
	if err != nil || plan.Action != ForwardRecoveryActionMarkHold || plan.HoldCode != RecoveryHoldConfirmationDrift {
		t.Fatalf("changed reason plan = %#v, %v", plan, err)
	}
}

func TestPlanForwardRecoveryRawConflictRequiresCompletePinnedInventory(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	result := forwardRecoveryResultFixture(
		fixture,
		ArtifactClassificationRawContentConflict,
		ArtifactReasonStrictPayloadInvalid,
		fixture.request.ReceivedAt.Add(2*time.Minute),
	)
	tests := []struct {
		name   string
		mutate func(*ArtifactClassificationResult)
	}{
		{name: "raw inventory not performed", mutate: func(value *ArtifactClassificationResult) { value.RawInventory.Performed = false }},
		{name: "raw inventory incomplete", mutate: func(value *ArtifactClassificationResult) {
			value.RawInventory.Coverage = ArtifactInventoryCoverageIncomplete
		}},
		{name: "raw inventory ambiguous", mutate: func(value *ArtifactClassificationResult) { value.RawInventory.NonSoftDeletedCount = 2 }},
		{name: "raw pin missing", mutate: func(value *ArtifactClassificationResult) { value.PinnedRaw = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneArtifactClassificationResult(result)
			test.mutate(&changed)
			_, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
				Phase: RecoveryPhaseInitial, Request: fixture.request, Result: changed,
			})
			if !errors.Is(err, ErrInvalidForwardRecoveryPlan) {
				t.Fatalf("PlanForwardRecoveryAction() = %v", err)
			}
		})
	}
}

func TestPlanPostManifestConfirmationCannotRepairReleaseNoneOrReject(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	firstAt := fixture.request.ReceivedAt.Add(2 * time.Minute)
	prior := forwardRecoveryResultFixture(
		fixture,
		ArtifactClassificationValidRawOnly,
		ArtifactReasonRawValidManifestAbsent,
		firstAt,
	)
	written := storedArtifactFromSnapshot(fixture.manifestSnapshot)
	tests := []struct {
		name        string
		class       ArtifactClassification
		reason      ArtifactReasonCode
		wantAction  ForwardRecoveryAction
		wantHold    RecoveryHoldCode
		wantRelease LeaseReleaseCode
	}{
		{name: "exact complete stores", class: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, wantAction: ForwardRecoveryActionMarkStored},
		{name: "raw only holds", class: ArtifactClassificationValidRawOnly, reason: ArtifactReasonRawValidManifestAbsent, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldPostManifestDivergence},
		{name: "none holds", class: ArtifactClassificationNone, reason: ArtifactReasonNoCandidates, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldPostManifestDivergence},
		{name: "raw conflict holds", class: ArtifactClassificationRawContentConflict, reason: ArtifactReasonStrictPayloadInvalid, wantAction: ForwardRecoveryActionMarkHold, wantHold: RecoveryHoldPostManifestDivergence},
		{name: "provider timeout releases", class: ArtifactClassificationUnavailable, reason: ArtifactReasonProviderTimeout, wantAction: ForwardRecoveryActionReleaseLease, wantRelease: LeaseReleaseArtifactUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := forwardRecoveryResultFixture(fixture, test.class, test.reason, firstAt.Add(time.Second))
			plan, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
				Phase: RecoveryPhasePostManifestConfirmation, Request: fixture.request,
				Result: current, PriorResult: &prior, WrittenManifest: &written,
			})
			if err != nil {
				t.Fatalf("PlanForwardRecoveryAction() = %v", err)
			}
			if plan.Action != test.wantAction || plan.HoldCode != test.wantHold || plan.ReleaseCode != test.wantRelease {
				t.Fatalf("plan = %#v", plan)
			}
		})
	}

	t.Run("competing manifest generation holds", func(t *testing.T) {
		current := forwardRecoveryResultFixture(
			fixture,
			ArtifactClassificationValidComplete,
			ArtifactReasonManifestAndReferencedRawValid,
			firstAt.Add(time.Second),
		)
		changed := written
		changed.Generation++
		plan, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
			Phase: RecoveryPhasePostManifestConfirmation, Request: fixture.request,
			Result: current, PriorResult: &prior, WrittenManifest: &changed,
		})
		if err != nil || plan.Action != ForwardRecoveryActionMarkHold || plan.HoldCode != RecoveryHoldPostManifestDivergence {
			t.Fatalf("PlanForwardRecoveryAction() = %#v, %v", plan, err)
		}
	})
}

func TestPlanForwardRecoveryRejectsInvalidResultContracts(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	observedAt := fixture.request.ReceivedAt.Add(2 * time.Minute)
	valid := forwardRecoveryResultFixture(
		fixture,
		ArtifactClassificationValidComplete,
		ArtifactReasonManifestAndReferencedRawValid,
		observedAt,
	)
	tests := []struct {
		name   string
		mutate func(*ArtifactClassificationResult)
	}{
		{name: "unknown classification", mutate: func(value *ArtifactClassificationResult) { value.Classification = "unknown" }},
		{name: "invalid pair", mutate: func(value *ArtifactClassificationResult) { value.ReasonCode = ArtifactReasonNoCandidates }},
		{name: "validator", mutate: func(value *ArtifactClassificationResult) { value.ValidatorVersion = "other@1" }},
		{name: "observed at zero", mutate: func(value *ArtifactClassificationResult) { value.ObservedAt = time.Time{} }},
		{name: "observed after fence", mutate: func(value *ArtifactClassificationResult) { value.ObservedAt = fixture.request.ForwardFence.ExpiresAt }},
		{name: "retention", mutate: func(value *ArtifactClassificationResult) { value.RetentionPhase = ArtifactRetentionAtAfterExpiry }},
		{name: "raw pin missing", mutate: func(value *ArtifactClassificationResult) { value.PinnedRaw = nil }},
		{name: "manifest pin malformed", mutate: func(value *ArtifactClassificationResult) { value.PinnedManifest.SHA256 = "invalid" }},
		{name: "inventory incomplete", mutate: func(value *ArtifactClassificationResult) {
			value.RawInventory.Coverage = ArtifactInventoryCoverageIncomplete
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneArtifactClassificationResult(valid)
			test.mutate(&changed)
			_, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
				Phase: RecoveryPhaseInitial, Request: fixture.request, Result: changed,
			})
			if !errors.Is(err, ErrInvalidForwardRecoveryPlan) {
				t.Fatalf("PlanForwardRecoveryAction() = %v", err)
			}
		})
	}
}

func TestReadOnlyClassifierPinsStableRawConflictForConfirmation(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	fixture.rawCompressed = append([]byte(nil), fixture.rawCompressed...)
	fixture.rawCompressed[len(fixture.rawCompressed)-1] ^= 0xff
	refreshRawSnapshotAndManifest(t, &fixture)
	now := fixture.request.ReceivedAt.Add(2 * time.Minute)
	reader := newScriptedArtifactReader(t, forwardRawOnlyCalls(fixture)...)
	classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
	result, err := classifier.Classify(
		context.Background(),
		mintClassifierGrant(t, fixture.request, now),
		fixture.request,
	)
	if err != nil {
		t.Fatalf("Classify() = %v", err)
	}
	if result.Classification != ArtifactClassificationRawContentConflict || result.PinnedRaw == nil {
		t.Fatalf("result = %#v", result)
	}
	reader.assertDone(t)
}

func forwardRecoveryResultFixture(
	fixture artifactContentTestFixture,
	class ArtifactClassification,
	reason ArtifactReasonCode,
	observedAt time.Time,
) ArtifactClassificationResult {
	result := ArtifactClassificationResult{
		Classification:     class,
		ReasonCode:         reason,
		RetentionPhase:     ArtifactRetentionBeforeExpiry,
		ManifestInventory:  ArtifactInventorySummary{Coverage: ArtifactInventoryCoverageUnknown},
		RawInventory:       ArtifactInventorySummary{Coverage: ArtifactInventoryCoverageUnknown},
		ValidatorVersion:   fixture.request.ValidatorVersion,
		ObservedAt:         observedAt,
		requestBindingHash: canonicalArtifactClassificationRequestBinding(fixture.request),
	}
	complete := func(count int) ArtifactInventorySummary {
		return ArtifactInventorySummary{
			Performed: true, NonSoftDeletedCount: count,
			Coverage: ArtifactInventoryCoverageComplete,
		}
	}
	switch class {
	case ArtifactClassificationNone:
		result.ManifestInventory = complete(0)
		result.RawInventory = complete(0)
	case ArtifactClassificationValidRawOnly:
		result.ManifestInventory = complete(0)
		result.RawInventory = complete(1)
		result.PinnedRaw = artifactPinnedLineageFromSnapshot(fixture.rawSnapshot)
	case ArtifactClassificationValidComplete:
		result.ManifestInventory = complete(1)
		result.RawInventory = complete(1)
		result.PinnedManifest = artifactPinnedLineageFromSnapshot(fixture.manifestSnapshot)
		result.PinnedRaw = artifactPinnedLineageFromSnapshot(fixture.rawSnapshot)
	case ArtifactClassificationManifestOnly:
		result.ManifestInventory = complete(1)
		result.RawInventory = complete(0)
		result.PinnedManifest = artifactPinnedLineageFromSnapshot(fixture.manifestSnapshot)
	case ArtifactClassificationRawContentConflict:
		result.ManifestInventory = complete(0)
		result.RawInventory = complete(1)
		result.PinnedRaw = artifactPinnedLineageFromSnapshot(fixture.rawSnapshot)
	}
	return result
}

func cloneArtifactClassificationResult(result ArtifactClassificationResult) ArtifactClassificationResult {
	cloned := result
	if result.PinnedRaw != nil {
		raw := *result.PinnedRaw
		cloned.PinnedRaw = &raw
	}
	if result.PinnedManifest != nil {
		manifest := *result.PinnedManifest
		cloned.PinnedManifest = &manifest
	}
	return cloned
}
