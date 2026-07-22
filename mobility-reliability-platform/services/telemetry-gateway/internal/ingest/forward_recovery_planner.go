package ingest

import (
	"errors"
)

var ErrInvalidForwardRecoveryPlan = errors.New("forward recovery plan is invalid")

type RecoveryActionPhase string

const (
	RecoveryPhaseInitial                  RecoveryActionPhase = "initial"
	RecoveryPhaseConfirmation             RecoveryActionPhase = "confirmation"
	RecoveryPhasePostManifestConfirmation RecoveryActionPhase = "post_manifest_confirmation"
)

type ForwardRecoveryAction string

const (
	ForwardRecoveryActionConfirmComplete    ForwardRecoveryAction = "confirm_complete"
	ForwardRecoveryActionConfirmRawConflict ForwardRecoveryAction = "confirm_raw_conflict"
	ForwardRecoveryActionCreateManifest     ForwardRecoveryAction = "create_manifest"
	ForwardRecoveryActionMarkStored         ForwardRecoveryAction = "mark_stored"
	ForwardRecoveryActionMarkRejected       ForwardRecoveryAction = "mark_rejected"
	ForwardRecoveryActionMarkHold           ForwardRecoveryAction = "mark_hold"
	ForwardRecoveryActionReleaseLease       ForwardRecoveryAction = "release_lease"
)

type RecoveryHoldCode string

const (
	RecoveryHoldManifestOnly               RecoveryHoldCode = "manifest_only"
	RecoveryHoldManifestConflict           RecoveryHoldCode = "manifest_conflict"
	RecoveryHoldMetadataConflict           RecoveryHoldCode = "metadata_conflict"
	RecoveryHoldGenerationDrift            RecoveryHoldCode = "generation_drift"
	RecoveryHoldValidatorUnavailable       RecoveryHoldCode = "validator_unavailable"
	RecoveryHoldCodecUnavailable           RecoveryHoldCode = "codec_profile_unavailable"
	RecoveryHoldInventoryIncomplete        RecoveryHoldCode = "inventory_coverage_incomplete"
	RecoveryHoldResponseUnverifiable       RecoveryHoldCode = "response_unverifiable"
	RecoveryHoldArtifactPermissionDenied   RecoveryHoldCode = "artifact_permission_denied"
	RecoveryHoldCurrentAuthorizationDenied RecoveryHoldCode = "current_authorization_denied"
	RecoveryHoldConfirmationDrift          RecoveryHoldCode = "confirmation_drift"
	RecoveryHoldPostManifestDivergence     RecoveryHoldCode = "post_manifest_divergence"
)

type ForwardRecoveryPlanInput struct {
	Phase           RecoveryActionPhase
	Request         ArtifactClassificationRequest
	Result          ArtifactClassificationResult
	PriorResult     *ArtifactClassificationResult
	WrittenManifest *StoredArtifact
}

type ForwardRecoveryActionPlan struct {
	Phase          RecoveryActionPhase
	Action         ForwardRecoveryAction
	Classification ArtifactClassification
	ReasonCode     ArtifactReasonCode
	ReleaseCode    LeaseReleaseCode
	HoldCode       RecoveryHoldCode
	RejectionCode  string
	Raw            *ArtifactLineage
	Manifest       *ArtifactLineage
}

func PlanForwardRecoveryAction(input ForwardRecoveryPlanInput) (ForwardRecoveryActionPlan, error) {
	if !validRecoveryActionPhase(input.Phase) ||
		validateForwardRecoveryClassification(input.Request, input.Result) != nil {
		return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
	}
	switch input.Phase {
	case RecoveryPhaseInitial:
		if input.PriorResult != nil || input.WrittenManifest != nil {
			return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
		}
		return planInitialForwardRecovery(input.Request, input.Result)
	case RecoveryPhaseConfirmation:
		if input.PriorResult == nil || input.WrittenManifest != nil ||
			validateForwardRecoveryClassification(input.Request, *input.PriorResult) != nil ||
			!input.Result.ObservedAt.After(input.PriorResult.ObservedAt) {
			return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
		}
		return planForwardRecoveryConfirmation(input.Request, *input.PriorResult, input.Result)
	case RecoveryPhasePostManifestConfirmation:
		if input.PriorResult == nil || input.WrittenManifest == nil ||
			validateForwardRecoveryClassification(input.Request, *input.PriorResult) != nil ||
			input.PriorResult.Classification != ArtifactClassificationValidRawOnly ||
			!input.Result.ObservedAt.After(input.PriorResult.ObservedAt) {
			return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
		}
		return planPostManifestConfirmation(
			input.Request,
			*input.PriorResult,
			input.Result,
			*input.WrittenManifest,
		)
	default:
		return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
	}
}

func planInitialForwardRecovery(
	request ArtifactClassificationRequest,
	result ArtifactClassificationResult,
) (ForwardRecoveryActionPlan, error) {
	plan := baseForwardRecoveryPlan(RecoveryPhaseInitial, result)
	switch result.Classification {
	case ArtifactClassificationNone:
		plan.Action = ForwardRecoveryActionReleaseLease
		plan.ReleaseCode = LeaseReleaseAwaitingClientReplay
	case ArtifactClassificationValidRawOnly:
		plan.Action = ForwardRecoveryActionCreateManifest
		plan.Raw, _ = artifactLineageFromPinned(request.ExpectedRawPath, result.PinnedRaw)
	case ArtifactClassificationValidComplete:
		plan.Action = ForwardRecoveryActionConfirmComplete
		plan.Raw, _ = artifactLineageFromPinned(request.ExpectedRawPath, result.PinnedRaw)
		plan.Manifest, _ = artifactLineageFromPinned(request.ExpectedManifestPath, result.PinnedManifest)
	case ArtifactClassificationRawContentConflict:
		plan.Action = ForwardRecoveryActionConfirmRawConflict
		plan.Raw, _ = artifactLineageFromPinned(request.ExpectedRawPath, result.PinnedRaw)
	case ArtifactClassificationManifestOnly,
		ArtifactClassificationManifestConflict,
		ArtifactClassificationMetadataConflict,
		ArtifactClassificationGenerationDrift,
		ArtifactClassificationUnavailable:
		return planHoldOrTransientRelease(plan, result)
	default:
		return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
	}
	return plan, nil
}

func planForwardRecoveryConfirmation(
	request ArtifactClassificationRequest,
	prior ArtifactClassificationResult,
	current ArtifactClassificationResult,
) (ForwardRecoveryActionPlan, error) {
	plan := baseForwardRecoveryPlan(RecoveryPhaseConfirmation, current)
	if prior.Classification != ArtifactClassificationValidComplete &&
		prior.Classification != ArtifactClassificationRawContentConflict {
		return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
	}
	if transientArtifactReason(current) {
		plan.Action = ForwardRecoveryActionReleaseLease
		plan.ReleaseCode = LeaseReleaseArtifactUnavailable
		return plan, nil
	}
	switch prior.Classification {
	case ArtifactClassificationValidComplete:
		if current.Classification == ArtifactClassificationValidComplete &&
			samePinnedLineage(prior.PinnedRaw, current.PinnedRaw) &&
			samePinnedLineage(prior.PinnedManifest, current.PinnedManifest) {
			plan.Action = ForwardRecoveryActionMarkStored
			plan.Raw, _ = artifactLineageFromPinned(request.ExpectedRawPath, current.PinnedRaw)
			plan.Manifest, _ = artifactLineageFromPinned(request.ExpectedManifestPath, current.PinnedManifest)
			return plan, nil
		}
	case ArtifactClassificationRawContentConflict:
		if current.Classification == ArtifactClassificationRawContentConflict &&
			current.ReasonCode == prior.ReasonCode &&
			samePinnedLineage(prior.PinnedRaw, current.PinnedRaw) {
			plan.Action = ForwardRecoveryActionMarkRejected
			plan.RejectionCode = "object_conflict"
			plan.Raw, _ = artifactLineageFromPinned(request.ExpectedRawPath, current.PinnedRaw)
			return plan, nil
		}
	}
	plan.Action = ForwardRecoveryActionMarkHold
	plan.HoldCode = RecoveryHoldConfirmationDrift
	return plan, nil
}

func planPostManifestConfirmation(
	request ArtifactClassificationRequest,
	prior ArtifactClassificationResult,
	current ArtifactClassificationResult,
	writtenManifest StoredArtifact,
) (ForwardRecoveryActionPlan, error) {
	plan := baseForwardRecoveryPlan(RecoveryPhasePostManifestConfirmation, current)
	writtenLineage := artifactLineageFromStored(writtenManifest)
	if validateArtifactLineage(&writtenLineage, request.ExpectedManifestPath) != nil {
		return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
	}
	if transientArtifactReason(current) {
		plan.Action = ForwardRecoveryActionReleaseLease
		plan.ReleaseCode = LeaseReleaseArtifactUnavailable
		return plan, nil
	}
	if current.Classification == ArtifactClassificationValidComplete &&
		samePinnedLineage(prior.PinnedRaw, current.PinnedRaw) {
		currentManifest, err := artifactLineageFromPinned(request.ExpectedManifestPath, current.PinnedManifest)
		if err == nil && sameArtifactLineage(*currentManifest, writtenLineage) {
			plan.Action = ForwardRecoveryActionMarkStored
			plan.Raw, _ = artifactLineageFromPinned(request.ExpectedRawPath, current.PinnedRaw)
			plan.Manifest = currentManifest
			return plan, nil
		}
	}
	plan.Action = ForwardRecoveryActionMarkHold
	plan.HoldCode = RecoveryHoldPostManifestDivergence
	return plan, nil
}

func planHoldOrTransientRelease(
	plan ForwardRecoveryActionPlan,
	result ArtifactClassificationResult,
) (ForwardRecoveryActionPlan, error) {
	if transientArtifactReason(result) {
		plan.Action = ForwardRecoveryActionReleaseLease
		plan.ReleaseCode = LeaseReleaseArtifactUnavailable
		return plan, nil
	}
	plan.Action = ForwardRecoveryActionMarkHold
	switch result.Classification {
	case ArtifactClassificationManifestOnly:
		plan.HoldCode = RecoveryHoldManifestOnly
	case ArtifactClassificationManifestConflict:
		plan.HoldCode = RecoveryHoldManifestConflict
	case ArtifactClassificationMetadataConflict:
		plan.HoldCode = RecoveryHoldMetadataConflict
	case ArtifactClassificationGenerationDrift:
		plan.HoldCode = RecoveryHoldGenerationDrift
	case ArtifactClassificationUnavailable:
		switch result.ReasonCode {
		case ArtifactReasonPermissionDenied:
			plan.HoldCode = RecoveryHoldArtifactPermissionDenied
		case ArtifactReasonValidatorUnavailable:
			plan.HoldCode = RecoveryHoldValidatorUnavailable
		case ArtifactReasonCodecProfileUnavailable:
			plan.HoldCode = RecoveryHoldCodecUnavailable
		case ArtifactReasonInventoryCoverageIncomplete:
			plan.HoldCode = RecoveryHoldInventoryIncomplete
		case ArtifactReasonResponseUnverifiable:
			plan.HoldCode = RecoveryHoldResponseUnverifiable
		default:
			return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
		}
	default:
		return ForwardRecoveryActionPlan{}, ErrInvalidForwardRecoveryPlan
	}
	return plan, nil
}

func validateForwardRecoveryClassification(
	request ArtifactClassificationRequest,
	result ArtifactClassificationResult,
) error {
	if request.Purpose != ArtifactReadForwardRecovery ||
		ValidateArtifactClassificationRequest(request) != nil ||
		!validArtifactClassificationOutcome(request.Purpose, result.Classification, result.ReasonCode) ||
		result.ValidatorVersion != request.ValidatorVersion || result.ObservedAt.IsZero() ||
		!validArtifactClassificationEvidence(request, result) ||
		result.ObservedAt.Before(request.ReceivedAt) ||
		!result.ObservedAt.Before(request.ArtifactExpiresAt) ||
		request.ForwardFence == nil || !result.ObservedAt.Before(request.ForwardFence.ExpiresAt) ||
		result.RetentionPhase != ArtifactRetentionBeforeExpiry ||
		!validArtifactInventorySummary(result.ManifestInventory) ||
		!validArtifactInventorySummary(result.RawInventory) {
		return ErrInvalidForwardRecoveryPlan
	}
	if result.PinnedRaw != nil {
		if _, err := artifactLineageFromPinned(request.ExpectedRawPath, result.PinnedRaw); err != nil {
			return ErrInvalidForwardRecoveryPlan
		}
	}
	if result.PinnedManifest != nil {
		if _, err := artifactLineageFromPinned(request.ExpectedManifestPath, result.PinnedManifest); err != nil {
			return ErrInvalidForwardRecoveryPlan
		}
	}
	switch result.Classification {
	case ArtifactClassificationNone:
		if result.PinnedRaw != nil || result.PinnedManifest != nil ||
			!completeInventoryCount(result.ManifestInventory, 0) ||
			!completeInventoryCount(result.RawInventory, 0) {
			return ErrInvalidForwardRecoveryPlan
		}
	case ArtifactClassificationValidRawOnly:
		if result.PinnedRaw == nil || result.PinnedManifest != nil ||
			!completeInventoryCount(result.ManifestInventory, 0) ||
			!completeInventoryCount(result.RawInventory, 1) {
			return ErrInvalidForwardRecoveryPlan
		}
	case ArtifactClassificationValidComplete:
		if result.PinnedRaw == nil || result.PinnedManifest == nil ||
			!completeInventoryCount(result.ManifestInventory, 1) ||
			!completeInventoryCount(result.RawInventory, 1) {
			return ErrInvalidForwardRecoveryPlan
		}
	case ArtifactClassificationManifestOnly:
		if result.PinnedManifest == nil || result.PinnedRaw != nil ||
			!completeInventoryCount(result.ManifestInventory, 1) ||
			!completeInventoryCount(result.RawInventory, 0) {
			return ErrInvalidForwardRecoveryPlan
		}
	case ArtifactClassificationRawContentConflict:
		manifestEvidenceValid := completeInventoryCount(result.ManifestInventory, 0) &&
			result.PinnedManifest == nil ||
			completeInventoryCount(result.ManifestInventory, 1) && result.PinnedManifest != nil
		if result.PinnedRaw == nil || !completeInventoryCount(result.RawInventory, 1) ||
			!manifestEvidenceValid {
			return ErrInvalidForwardRecoveryPlan
		}
	}
	return nil
}

func artifactLineageFromPinned(
	path string,
	pinned *ArtifactPinnedLineage,
) (*ArtifactLineage, error) {
	if pinned == nil {
		return nil, ErrInvalidForwardRecoveryPlan
	}
	lineage := &ArtifactLineage{
		Path:           path,
		SHA256:         pinned.SHA256,
		CRC32C:         pinned.CRC32C,
		Size:           pinned.Size,
		Generation:     pinned.Generation,
		Metageneration: pinned.Metageneration,
	}
	if validateArtifactLineage(lineage, path) != nil {
		return nil, ErrInvalidForwardRecoveryPlan
	}
	return lineage, nil
}

func artifactLineageFromStored(stored StoredArtifact) ArtifactLineage {
	return ArtifactLineage{
		Path:           stored.Path,
		SHA256:         stored.SHA256,
		CRC32C:         stored.CRC32C,
		Size:           stored.Size,
		Generation:     stored.Generation,
		Metageneration: stored.Metageneration,
	}
}

func samePinnedLineage(left, right *ArtifactPinnedLineage) bool {
	return left != nil && right != nil && *left == *right
}

func sameArtifactLineage(left, right ArtifactLineage) bool {
	return left == right
}

func validArtifactInventorySummary(summary ArtifactInventorySummary) bool {
	return summary.NonSoftDeletedCount >= 0 && summary.SoftDeletedCount >= 0 &&
		(summary.Coverage == ArtifactInventoryCoverageUnknown ||
			summary.Coverage == ArtifactInventoryCoverageComplete ||
			summary.Coverage == ArtifactInventoryCoverageIncomplete)
}

func completeInventoryCount(summary ArtifactInventorySummary, regular int) bool {
	return summary.Performed && !summary.Truncated &&
		summary.Coverage == ArtifactInventoryCoverageComplete &&
		summary.NonSoftDeletedCount == regular && summary.SoftDeletedCount == 0
}

func transientArtifactReason(result ArtifactClassificationResult) bool {
	if result.Classification != ArtifactClassificationUnavailable {
		return false
	}
	switch result.ReasonCode {
	case ArtifactReasonQuotaLimited,
		ArtifactReasonProviderTimeout,
		ArtifactReasonProviderCancelled,
		ArtifactReasonProviderUnavailable:
		return true
	default:
		return false
	}
}

func validRecoveryActionPhase(phase RecoveryActionPhase) bool {
	switch phase {
	case RecoveryPhaseInitial, RecoveryPhaseConfirmation, RecoveryPhasePostManifestConfirmation:
		return true
	default:
		return false
	}
}

func baseForwardRecoveryPlan(
	phase RecoveryActionPhase,
	result ArtifactClassificationResult,
) ForwardRecoveryActionPlan {
	return ForwardRecoveryActionPlan{
		Phase:          phase,
		Classification: result.Classification,
		ReasonCode:     result.ReasonCode,
	}
}
