package ingest

import (
	"context"
	"errors"
	"time"
)

const (
	artifactClassifierInventoryLimit = 2

	// MaxTelemetryRawArtifactCompressedBytes is the domain-owned read bound for
	// deterministic gzip artifacts. The validated decompressed request is at
	// most 2 MiB; 3 MiB leaves bounded codec overhead without inheriting the
	// provider adapter's broader transport ceiling.
	MaxTelemetryRawArtifactCompressedBytes int64 = 3 * 1024 * 1024
)

type readOnlyArtifactClassifier struct {
	reader    ArtifactInventoryReader
	validator telemetryArtifactValidator
	now       func() time.Time
}

var _ ArtifactClassifier = (*readOnlyArtifactClassifier)(nil)

// newReadOnlyArtifactClassifier remains package-private so low-level Storage
// capability cannot be wired directly into handlers or schedulers. A trusted
// authorizer must mint the opaque grant before Classify can cross the reader
// boundary.
func newReadOnlyArtifactClassifier(
	reader ArtifactInventoryReader,
	validator telemetryArtifactValidator,
	now func() time.Time,
) (*readOnlyArtifactClassifier, error) {
	if reader == nil || validator == nil {
		return nil, ErrArtifactUnavailable
	}
	if now == nil {
		now = time.Now
	}
	return &readOnlyArtifactClassifier{reader: reader, validator: validator, now: now}, nil
}

func (c *readOnlyArtifactClassifier) Classify(
	ctx context.Context,
	grant ArtifactReadAuthorizationGrant,
	request ArtifactClassificationRequest,
) (ArtifactClassificationResult, error) {
	if c == nil || c.reader == nil || c.validator == nil || c.now == nil {
		return ArtifactClassificationResult{}, ErrArtifactUnavailable
	}
	if ctx == nil {
		return ArtifactClassificationResult{}, ErrInvalidArtifactReadAuthorization
	}
	if err := ctx.Err(); err != nil {
		return ArtifactClassificationResult{}, err
	}
	request = cloneArtifactClassificationRequest(request)
	observedAt := c.now().UTC()
	if err := ValidateArtifactReadAuthorization(grant, request, observedAt); err != nil {
		return ArtifactClassificationResult{}, err
	}

	run := &artifactClassificationRun{
		classifier: c,
		ctx:        ctx,
		grant:      grant,
		request:    request,
		result: ArtifactClassificationResult{
			RetentionPhase:   artifactRetentionPhaseAt(request, observedAt),
			ValidatorVersion: request.ValidatorVersion,
			ObservedAt:       observedAt,
		},
	}
	switch request.Purpose {
	case ArtifactReadForwardRecovery:
		return run.classifyForward()
	case ArtifactReadAcceptedIntegrityAudit:
		return run.classifyAccepted()
	default:
		return ArtifactClassificationResult{}, ErrInvalidArtifactClassificationRequest
	}
}

type artifactClassificationRun struct {
	classifier *readOnlyArtifactClassifier
	ctx        context.Context
	grant      ArtifactReadAuthorizationGrant
	request    ArtifactClassificationRequest
	result     ArtifactClassificationResult
}

type artifactStableRead struct {
	snapshot ArtifactSnapshot
	content  []byte
}

type artifactInventoryResolution struct {
	candidate *ArtifactSnapshot
	missing   bool
	terminal  bool
	class     ArtifactClassification
	reason    ArtifactReasonCode
}

func (r *artifactClassificationRun) classifyForward() (ArtifactClassificationResult, error) {
	manifestInventory, err := r.listInventory(r.request.ExpectedManifestPath)
	r.result.ManifestInventory = artifactInventorySummary(manifestInventory)
	if err != nil {
		return r.handleInventoryError(err)
	}
	if class, reason, terminal := validateArtifactInventoryShape(
		manifestInventory,
		r.request.ExpectedManifestPath,
	); terminal {
		return r.terminal(class, reason)
	}
	if artifactGenerationSetAmbiguous(manifestInventory.NonSoftDeleted) {
		return r.terminal(ArtifactClassificationGenerationDrift, ArtifactReasonMultipleManifestGenerations)
	}
	if len(manifestInventory.SoftDeleted.Candidates) > 0 {
		return r.terminal(ArtifactClassificationGenerationDrift, ArtifactReasonSoftDeletedCandidatePresent)
	}
	if len(manifestInventory.NonSoftDeleted.Candidates) == 0 {
		return r.classifyForwardWithoutManifest()
	}

	manifestRead, class, reason, err := r.readStableArtifact(
		manifestInventory.NonSoftDeleted.Candidates[0],
		true,
	)
	if err != nil {
		return ArtifactClassificationResult{}, err
	}
	if class != "" {
		return r.terminal(class, reason)
	}
	if err := r.checkAuthorization(); err != nil {
		return ArtifactClassificationResult{}, err
	}
	manifestValidation := r.classifier.validator.ValidateManifest(
		r.request,
		manifestRead.snapshot,
		manifestRead.content,
	)
	if manifestValidation.Status != artifactContentValidationValid ||
		manifestValidation.ReasonCode != "" || manifestValidation.ReferencedRaw == nil {
		if manifestValidation.Status == artifactContentValidationInvalid && manifestValidation.ReasonCode != "" {
			class, reason = classificationForContentReason(manifestValidation.ReasonCode)
			return r.terminal(class, reason)
		}
		return r.terminal(ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable)
	}
	r.result.PinnedManifest = artifactPinnedLineageFromSnapshot(manifestRead.snapshot)
	return r.classifyForwardReferencedRaw(manifestRead, *manifestValidation.ReferencedRaw)
}

func (r *artifactClassificationRun) classifyForwardWithoutManifest() (ArtifactClassificationResult, error) {
	rawInventory, err := r.listInventory(r.request.ExpectedRawPath)
	r.result.RawInventory = artifactInventorySummary(rawInventory)
	if err != nil {
		return r.handleInventoryError(err)
	}
	if class, reason, terminal := validateArtifactInventoryShape(
		rawInventory,
		r.request.ExpectedRawPath,
	); terminal {
		return r.terminal(class, reason)
	}
	if artifactGenerationSetAmbiguous(rawInventory.NonSoftDeleted) {
		return r.terminal(ArtifactClassificationGenerationDrift, ArtifactReasonMultipleRawGenerations)
	}
	if len(rawInventory.SoftDeleted.Candidates) > 0 {
		return r.terminal(ArtifactClassificationGenerationDrift, ArtifactReasonSoftDeletedCandidatePresent)
	}
	if len(rawInventory.NonSoftDeleted.Candidates) == 0 {
		return r.terminal(ArtifactClassificationNone, ArtifactReasonNoCandidates)
	}

	rawRead, class, reason, err := r.readStableArtifact(
		rawInventory.NonSoftDeleted.Candidates[0],
		false,
	)
	if err != nil {
		return ArtifactClassificationResult{}, err
	}
	if class != "" {
		return r.terminal(class, reason)
	}
	if err := r.checkAuthorization(); err != nil {
		return ArtifactClassificationResult{}, err
	}
	rawValidation := r.classifier.validator.ValidateRaw(r.request, rawRead.snapshot, rawRead.content)
	if rawValidation.Status != artifactContentValidationValid || rawValidation.ReasonCode != "" {
		if rawValidation.Status == artifactContentValidationInvalid && rawValidation.ReasonCode != "" {
			class, reason = classificationForContentReason(rawValidation.ReasonCode)
			return r.terminal(class, reason)
		}
		return r.terminal(ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable)
	}
	r.result.PinnedRaw = artifactPinnedLineageFromSnapshot(rawRead.snapshot)
	return r.terminal(ArtifactClassificationValidRawOnly, ArtifactReasonRawValidManifestAbsent)
}

func (r *artifactClassificationRun) classifyForwardReferencedRaw(
	manifestRead artifactStableRead,
	reference artifactValidatedRawReference,
) (ArtifactClassificationResult, error) {
	rawInventory, err := r.listInventory(reference.Target.Path)
	r.result.RawInventory = artifactInventorySummary(rawInventory)
	if err != nil {
		return r.handleInventoryError(err)
	}
	if class, reason, terminal := validateArtifactInventoryShape(rawInventory, reference.Target.Path); terminal {
		return r.terminal(class, reason)
	}
	if artifactGenerationSetAmbiguous(rawInventory.NonSoftDeleted) {
		return r.terminal(ArtifactClassificationGenerationDrift, ArtifactReasonMultipleRawGenerations)
	}
	if len(rawInventory.SoftDeleted.Candidates) > 0 {
		return r.terminal(ArtifactClassificationGenerationDrift, ArtifactReasonSoftDeletedCandidatePresent)
	}
	if len(rawInventory.NonSoftDeleted.Candidates) == 0 {
		return r.terminal(ArtifactClassificationManifestOnly, ArtifactReasonReferencedRawNotFound)
	}
	candidate := rawInventory.NonSoftDeleted.Candidates[0]
	if candidate.Generation != reference.Target.Generation {
		return r.terminal(
			ArtifactClassificationGenerationDrift,
			ArtifactReasonReferencedGenerationMissingOtherPresent,
		)
	}
	if candidate.Metageneration != reference.Target.Metageneration {
		return r.terminal(
			ArtifactClassificationGenerationDrift,
			ArtifactReasonMetagenerationChangedDuringRead,
		)
	}

	rawRead, class, reason, err := r.readStableArtifact(candidate, false)
	if err != nil {
		return ArtifactClassificationResult{}, err
	}
	if class != "" {
		return r.terminal(class, reason)
	}
	if err := r.checkAuthorization(); err != nil {
		return ArtifactClassificationResult{}, err
	}
	contentValidation := r.classifier.validator.Validate(
		r.request,
		manifestRead.snapshot,
		manifestRead.content,
		rawRead.snapshot,
		rawRead.content,
	)
	if contentValidation.Status != artifactContentValidationValid || contentValidation.ReasonCode != "" {
		if contentValidation.Status == artifactContentValidationInvalid && contentValidation.ReasonCode != "" {
			class, reason = classificationForContentReason(contentValidation.ReasonCode)
			return r.terminal(class, reason)
		}
		return r.terminal(ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable)
	}
	r.result.PinnedRaw = artifactPinnedLineageFromSnapshot(rawRead.snapshot)
	return r.terminal(ArtifactClassificationValidComplete, ArtifactReasonManifestAndReferencedRawValid)
}

func (r *artifactClassificationRun) classifyAccepted() (ArtifactClassificationResult, error) {
	manifestInventory, err := r.listInventory(r.request.ExpectedManifestPath)
	r.result.ManifestInventory = artifactInventorySummary(manifestInventory)
	if err != nil {
		return r.handleInventoryError(err)
	}
	manifestResolution := resolveAcceptedArtifactInventory(
		manifestInventory,
		r.request.ExpectedManifestPath,
		*r.request.AcceptedManifestLineage,
		true,
	)
	if manifestResolution.terminal {
		return r.terminal(manifestResolution.class, manifestResolution.reason)
	}

	rawInventory, err := r.listInventory(r.request.ExpectedRawPath)
	r.result.RawInventory = artifactInventorySummary(rawInventory)
	if err != nil {
		return r.handleInventoryError(err)
	}
	rawResolution := resolveAcceptedArtifactInventory(
		rawInventory,
		r.request.ExpectedRawPath,
		*r.request.AcceptedRawLineage,
		false,
	)
	if rawResolution.terminal {
		return r.terminal(rawResolution.class, rawResolution.reason)
	}

	if manifestResolution.missing && rawResolution.missing {
		return r.terminal(ArtifactClassificationStoredMissing, ArtifactReasonAcceptedBothMissing)
	}

	var manifestRead artifactStableRead
	if manifestResolution.candidate != nil {
		manifestRead, manifestResolution.class, manifestResolution.reason, err = r.readStableArtifact(
			*manifestResolution.candidate,
			true,
		)
		if err != nil {
			return ArtifactClassificationResult{}, err
		}
		if manifestResolution.class != "" {
			return r.terminal(manifestResolution.class, manifestResolution.reason)
		}
	}

	var rawRead artifactStableRead
	if rawResolution.candidate != nil {
		rawRead, rawResolution.class, rawResolution.reason, err = r.readStableArtifact(
			*rawResolution.candidate,
			false,
		)
		if err != nil {
			return ArtifactClassificationResult{}, err
		}
		if rawResolution.class != "" {
			return r.terminal(rawResolution.class, rawResolution.reason)
		}
	}
	if err := r.checkAuthorization(); err != nil {
		return ArtifactClassificationResult{}, err
	}

	switch {
	case manifestResolution.candidate != nil && rawResolution.candidate != nil:
		validation := r.classifier.validator.Validate(
			r.request,
			manifestRead.snapshot,
			manifestRead.content,
			rawRead.snapshot,
			rawRead.content,
		)
		if validation.Status != artifactContentValidationValid || validation.ReasonCode != "" {
			if validation.Status == artifactContentValidationInvalid && validation.ReasonCode != "" {
				class, reason := classificationForContentReason(validation.ReasonCode)
				return r.terminal(class, reason)
			}
			return r.terminal(ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable)
		}
		r.result.PinnedManifest = artifactPinnedLineageFromSnapshot(manifestRead.snapshot)
		r.result.PinnedRaw = artifactPinnedLineageFromSnapshot(rawRead.snapshot)
	case manifestResolution.candidate != nil:
		validation := r.classifier.validator.ValidateManifest(
			r.request,
			manifestRead.snapshot,
			manifestRead.content,
		)
		if validation.Status != artifactContentValidationValid ||
			validation.ReasonCode != "" || validation.ReferencedRaw == nil {
			if validation.Status == artifactContentValidationInvalid && validation.ReasonCode != "" {
				class, reason := classificationForContentReason(validation.ReasonCode)
				return r.terminal(class, reason)
			}
			return r.terminal(ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable)
		}
		r.result.PinnedManifest = artifactPinnedLineageFromSnapshot(manifestRead.snapshot)
	case rawResolution.candidate != nil:
		validation := r.classifier.validator.ValidateRaw(
			r.request,
			rawRead.snapshot,
			rawRead.content,
		)
		if validation.Status != artifactContentValidationValid || validation.ReasonCode != "" {
			if validation.Status == artifactContentValidationInvalid && validation.ReasonCode != "" {
				class, reason := classificationForContentReason(validation.ReasonCode)
				return r.terminal(class, reason)
			}
			return r.terminal(ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable)
		}
		r.result.PinnedRaw = artifactPinnedLineageFromSnapshot(rawRead.snapshot)
	}

	switch {
	case manifestResolution.missing:
		return r.terminal(ArtifactClassificationStoredMissing, ArtifactReasonAcceptedManifestMissing)
	case rawResolution.missing:
		return r.terminal(ArtifactClassificationStoredMissing, ArtifactReasonAcceptedRawMissing)
	default:
		return r.terminal(ArtifactClassificationValidComplete, ArtifactReasonManifestAndReferencedRawValid)
	}
}

func resolveAcceptedArtifactInventory(
	inventory GenerationInventory,
	exactPath string,
	expected ArtifactLineage,
	manifest bool,
) artifactInventoryResolution {
	if class, reason, terminal := validateArtifactInventoryShape(inventory, exactPath); terminal {
		return artifactInventoryResolution{terminal: true, class: class, reason: reason}
	}

	exactSoftDeleted := false
	for _, candidate := range inventory.SoftDeleted.Candidates {
		if candidate.Generation == expected.Generation {
			exactSoftDeleted = true
			break
		}
	}
	if exactSoftDeleted && len(inventory.SoftDeleted.Candidates) == 1 &&
		len(inventory.NonSoftDeleted.Candidates) == 0 &&
		!inventory.SoftDeleted.Truncated {
		return artifactInventoryResolution{
			terminal: true,
			class:    ArtifactClassificationStoredMissing,
			reason:   ArtifactReasonAcceptedGenerationSoftDeleted,
		}
	}
	if artifactGenerationSetAmbiguous(inventory.NonSoftDeleted) {
		reason := ArtifactReasonMultipleRawGenerations
		if manifest {
			reason = ArtifactReasonMultipleManifestGenerations
		}
		return artifactInventoryResolution{
			terminal: true,
			class:    ArtifactClassificationGenerationDrift,
			reason:   reason,
		}
	}
	if len(inventory.SoftDeleted.Candidates) > 0 {
		return artifactInventoryResolution{
			terminal: true,
			class:    ArtifactClassificationGenerationDrift,
			reason:   ArtifactReasonSoftDeletedCandidatePresent,
		}
	}
	if len(inventory.NonSoftDeleted.Candidates) == 0 {
		return artifactInventoryResolution{missing: true}
	}
	candidate := inventory.NonSoftDeleted.Candidates[0]
	if candidate.Generation != expected.Generation {
		return artifactInventoryResolution{
			terminal: true,
			class:    ArtifactClassificationGenerationDrift,
			reason:   ArtifactReasonAcceptedGenerationMissingOtherPresent,
		}
	}
	if candidate.Metageneration != expected.Metageneration {
		return artifactInventoryResolution{
			terminal: true,
			class:    ArtifactClassificationGenerationDrift,
			reason:   ArtifactReasonMetagenerationChangedDuringRead,
		}
	}
	return artifactInventoryResolution{candidate: &candidate}
}

func (r *artifactClassificationRun) readStableArtifact(
	candidate ArtifactSnapshot,
	manifest bool,
) (artifactStableRead, ArtifactClassification, ArtifactReasonCode, error) {
	preRead, err := r.inspectGeneration(candidate.Path, candidate.Generation)
	if err != nil {
		class, reason, controlErr := classificationForPinnedReadError(err, manifest)
		return artifactStableRead{}, class, reason, controlErr
	}
	if class, reason, terminal := compareArtifactSnapshots(candidate, preRead); terminal {
		return artifactStableRead{}, class, reason, nil
	}

	target := ArtifactTarget{
		Path:           preRead.Path,
		Generation:     preRead.Generation,
		Metageneration: preRead.Metageneration,
	}
	var content []byte
	if manifest {
		content, err = r.readManifest(target)
	} else {
		content, err = r.readRaw(target)
	}
	if err != nil {
		class, reason, controlErr := classificationForPinnedReadError(err, manifest)
		return artifactStableRead{}, class, reason, controlErr
	}

	postRead, err := r.inspectGeneration(candidate.Path, candidate.Generation)
	if err != nil {
		class, reason, controlErr := classificationForPinnedReadError(err, manifest)
		return artifactStableRead{}, class, reason, controlErr
	}
	if class, reason, terminal := compareArtifactSnapshots(preRead, postRead); terminal {
		return artifactStableRead{}, class, reason, nil
	}
	return artifactStableRead{snapshot: postRead, content: content}, "", "", nil
}

func (r *artifactClassificationRun) checkAuthorization() error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	return ValidateArtifactReadAuthorization(r.grant, r.request, r.classifier.now().UTC())
}

type artifactBoundaryContext struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	authorizationLimited bool
}

func (r *artifactClassificationRun) newBoundaryContext() (artifactBoundaryContext, error) {
	if err := r.checkAuthorization(); err != nil {
		return artifactBoundaryContext{}, err
	}
	authorizationDeadline := r.grant.expiresAt
	if r.request.Purpose == ArtifactReadForwardRecovery &&
		r.request.ForwardFence.ExpiresAt.Before(authorizationDeadline) {
		authorizationDeadline = r.request.ForwardFence.ExpiresAt
	}
	authorizationLimited := true
	if parentDeadline, exists := r.ctx.Deadline(); exists &&
		!authorizationDeadline.Before(parentDeadline) {
		authorizationLimited = false
	}
	callCtx, cancel := context.WithDeadline(r.ctx, authorizationDeadline)
	if boundaryErr := callCtx.Err(); boundaryErr != nil {
		cancel()
		if parentErr := r.ctx.Err(); parentErr != nil {
			return artifactBoundaryContext{}, parentErr
		}
		if errors.Is(boundaryErr, context.DeadlineExceeded) && authorizationLimited {
			return artifactBoundaryContext{}, ErrArtifactReadAuthorizationExpired
		}
		return artifactBoundaryContext{}, boundaryErr
	}
	return artifactBoundaryContext{
		ctx:                  callCtx,
		cancel:               cancel,
		authorizationLimited: authorizationLimited,
	}, nil
}

func (r *artifactClassificationRun) normalizeBoundaryError(
	boundary artifactBoundaryContext,
	err error,
) error {
	if err == nil {
		return nil
	}
	if parentErr := r.ctx.Err(); parentErr != nil {
		return parentErr
	}
	if boundaryErr := boundary.ctx.Err(); boundaryErr != nil {
		if errors.Is(boundaryErr, context.DeadlineExceeded) && boundary.authorizationLimited {
			return ErrArtifactReadAuthorizationExpired
		}
		return boundaryErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrArtifactProviderTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ErrArtifactProviderCancelled
	}
	return err
}

func (r *artifactClassificationRun) listInventory(
	exactPath string,
) (GenerationInventory, error) {
	boundary, err := r.newBoundaryContext()
	if err != nil {
		return GenerationInventory{}, err
	}
	inventory, callErr := r.classifier.reader.ListExactPathGenerations(
		boundary.ctx,
		exactPath,
		artifactClassifierInventoryLimit,
	)
	normalized := r.normalizeBoundaryError(boundary, callErr)
	boundary.cancel()
	return inventory, normalized
}

func (r *artifactClassificationRun) inspectGeneration(
	exactPath string,
	generation int64,
) (ArtifactSnapshot, error) {
	boundary, err := r.newBoundaryContext()
	if err != nil {
		return ArtifactSnapshot{}, err
	}
	snapshot, callErr := r.classifier.reader.InspectGeneration(
		boundary.ctx,
		exactPath,
		generation,
	)
	normalized := r.normalizeBoundaryError(boundary, callErr)
	boundary.cancel()
	return snapshot, normalized
}

func (r *artifactClassificationRun) readManifest(target ArtifactTarget) ([]byte, error) {
	boundary, err := r.newBoundaryContext()
	if err != nil {
		return nil, err
	}
	content, callErr := r.classifier.reader.ReadManifestGeneration(
		boundary.ctx,
		target,
		MaxTelemetryManifestBytes,
	)
	normalized := r.normalizeBoundaryError(boundary, callErr)
	boundary.cancel()
	return content, normalized
}

func (r *artifactClassificationRun) readRaw(target ArtifactTarget) ([]byte, error) {
	boundary, err := r.newBoundaryContext()
	if err != nil {
		return nil, err
	}
	content, callErr := r.classifier.reader.ReadRawGenerationCompressed(
		boundary.ctx,
		target,
		MaxTelemetryRawArtifactCompressedBytes,
	)
	normalized := r.normalizeBoundaryError(boundary, callErr)
	boundary.cancel()
	return content, normalized
}

func (r *artifactClassificationRun) handleInventoryError(
	err error,
) (ArtifactClassificationResult, error) {
	if isArtifactClassificationControlError(err) {
		return ArtifactClassificationResult{}, err
	}
	reason := artifactUnavailableReason(err)
	if errors.Is(err, ErrArtifactGenerationNotFound) ||
		errors.Is(err, ErrArtifactPreconditionDrift) ||
		errors.Is(err, ErrArtifactReadLimitExceeded) {
		reason = ArtifactReasonResponseUnverifiable
	}
	return r.terminal(ArtifactClassificationUnavailable, reason)
}

func (r *artifactClassificationRun) terminal(
	classification ArtifactClassification,
	reason ArtifactReasonCode,
) (ArtifactClassificationResult, error) {
	if err := r.checkAuthorization(); err != nil {
		return ArtifactClassificationResult{}, err
	}
	if !validArtifactClassificationOutcome(r.request.Purpose, classification, reason) {
		classification = ArtifactClassificationUnavailable
		reason = ArtifactReasonResponseUnverifiable
	}
	r.result.Classification = classification
	r.result.ReasonCode = reason
	return r.result, nil
}

func classificationForPinnedReadError(
	err error,
	manifest bool,
) (ArtifactClassification, ArtifactReasonCode, error) {
	if isArtifactClassificationControlError(err) {
		return "", "", err
	}
	switch {
	case errors.Is(err, ErrArtifactGenerationNotFound):
		return ArtifactClassificationGenerationDrift, ArtifactReasonGenerationChangedDuringRead, nil
	case errors.Is(err, ErrArtifactPreconditionDrift):
		return ArtifactClassificationGenerationDrift, ArtifactReasonMetagenerationChangedDuringRead, nil
	case errors.Is(err, ErrArtifactReadLimitExceeded):
		if manifest {
			return ArtifactClassificationManifestConflict, ArtifactReasonManifestMalformed, nil
		}
		return ArtifactClassificationRawContentConflict, ArtifactReasonStrictPayloadInvalid, nil
	default:
		return ArtifactClassificationUnavailable, artifactUnavailableReason(err), nil
	}
}

func classificationForContentReason(
	reason ArtifactReasonCode,
) (ArtifactClassification, ArtifactReasonCode) {
	switch reason {
	case ArtifactReasonValidatorUnavailable,
		ArtifactReasonCodecProfileUnavailable,
		ArtifactReasonResponseUnverifiable:
		return ArtifactClassificationUnavailable, reason
	case ArtifactReasonAttrsMalformed,
		ArtifactReasonRequiredMetadataMismatch,
		ArtifactReasonContentHeadersMismatch:
		return ArtifactClassificationMetadataConflict, reason
	case ArtifactReasonManifestMalformed,
		ArtifactReasonManifestNoncanonical,
		ArtifactReasonManifestLineageMismatch:
		return ArtifactClassificationManifestConflict, reason
	case ArtifactReasonDecompressedBodyHashMismatch,
		ArtifactReasonPayloadLineageMismatch,
		ArtifactReasonStrictPayloadInvalid:
		return ArtifactClassificationRawContentConflict, reason
	default:
		return ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable
	}
}

func artifactUnavailableReason(err error) ArtifactReasonCode {
	switch {
	case errors.Is(err, ErrArtifactPermissionDenied):
		return ArtifactReasonPermissionDenied
	case errors.Is(err, ErrArtifactQuotaLimited):
		return ArtifactReasonQuotaLimited
	case errors.Is(err, ErrArtifactProviderTimeout), errors.Is(err, context.DeadlineExceeded):
		return ArtifactReasonProviderTimeout
	case errors.Is(err, ErrArtifactProviderCancelled), errors.Is(err, context.Canceled):
		return ArtifactReasonProviderCancelled
	case errors.Is(err, ErrArtifactResponseUnverifiable):
		return ArtifactReasonResponseUnverifiable
	default:
		return ArtifactReasonProviderUnavailable
	}
}

func isArtifactClassificationControlError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrInvalidArtifactClassificationRequest) ||
		errors.Is(err, ErrInvalidArtifactReadAuthorization) ||
		errors.Is(err, ErrArtifactReadAuthorizationExpired)
}

func validArtifactClassificationOutcome(
	purpose ArtifactReadPurpose,
	classification ArtifactClassification,
	reason ArtifactReasonCode,
) bool {
	if !validArtifactReadPurpose(purpose) {
		return false
	}
	switch classification {
	case ArtifactClassificationNone:
		return purpose == ArtifactReadForwardRecovery && reason == ArtifactReasonNoCandidates
	case ArtifactClassificationValidRawOnly:
		return purpose == ArtifactReadForwardRecovery && reason == ArtifactReasonRawValidManifestAbsent
	case ArtifactClassificationValidComplete:
		return reason == ArtifactReasonManifestAndReferencedRawValid
	case ArtifactClassificationManifestOnly:
		return purpose == ArtifactReadForwardRecovery && reason == ArtifactReasonReferencedRawNotFound
	case ArtifactClassificationRawContentConflict:
		return reason == ArtifactReasonDecompressedBodyHashMismatch ||
			reason == ArtifactReasonPayloadLineageMismatch ||
			reason == ArtifactReasonStrictPayloadInvalid
	case ArtifactClassificationManifestConflict:
		return reason == ArtifactReasonManifestMalformed ||
			reason == ArtifactReasonManifestNoncanonical ||
			reason == ArtifactReasonManifestLineageMismatch
	case ArtifactClassificationMetadataConflict:
		return reason == ArtifactReasonAttrsMalformed ||
			reason == ArtifactReasonRequiredMetadataMismatch ||
			reason == ArtifactReasonContentHeadersMismatch
	case ArtifactClassificationGenerationDrift:
		if reason == ArtifactReasonReferencedGenerationMissingOtherPresent {
			return purpose == ArtifactReadForwardRecovery
		}
		if reason == ArtifactReasonAcceptedGenerationMissingOtherPresent {
			return purpose == ArtifactReadAcceptedIntegrityAudit
		}
		return reason == ArtifactReasonMultipleManifestGenerations ||
			reason == ArtifactReasonMultipleRawGenerations ||
			reason == ArtifactReasonSoftDeletedCandidatePresent ||
			reason == ArtifactReasonGenerationChangedDuringRead ||
			reason == ArtifactReasonMetagenerationChangedDuringRead
	case ArtifactClassificationStoredMissing:
		return purpose == ArtifactReadAcceptedIntegrityAudit &&
			(reason == ArtifactReasonAcceptedManifestMissing ||
				reason == ArtifactReasonAcceptedRawMissing ||
				reason == ArtifactReasonAcceptedBothMissing ||
				reason == ArtifactReasonAcceptedGenerationSoftDeleted)
	case ArtifactClassificationUnavailable:
		return reason == ArtifactReasonPermissionDenied ||
			reason == ArtifactReasonQuotaLimited ||
			reason == ArtifactReasonProviderTimeout ||
			reason == ArtifactReasonProviderCancelled ||
			reason == ArtifactReasonProviderUnavailable ||
			reason == ArtifactReasonValidatorUnavailable ||
			reason == ArtifactReasonCodecProfileUnavailable ||
			reason == ArtifactReasonInventoryCoverageIncomplete ||
			reason == ArtifactReasonResponseUnverifiable
	default:
		return false
	}
}

func validateArtifactInventoryShape(
	inventory GenerationInventory,
	exactPath string,
) (ArtifactClassification, ArtifactReasonCode, bool) {
	if inventory.Coverage != ArtifactInventoryCoverageComplete ||
		!inventory.NonSoftDeleted.Performed || !inventory.SoftDeleted.Performed {
		return ArtifactClassificationUnavailable, ArtifactReasonInventoryCoverageIncomplete, true
	}
	sets := []struct {
		set         ArtifactGenerationSet
		softDeleted bool
	}{
		{set: inventory.NonSoftDeleted, softDeleted: false},
		{set: inventory.SoftDeleted, softDeleted: true},
	}
	seen := make(map[int64]struct{}, artifactClassifierInventoryLimit*2)
	for _, entry := range sets {
		if len(entry.set.Candidates) > artifactClassifierInventoryLimit {
			return ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable, true
		}
		if entry.set.Truncated && len(entry.set.Candidates) < artifactClassifierInventoryLimit {
			return ArtifactClassificationUnavailable, ArtifactReasonInventoryCoverageIncomplete, true
		}
		for _, candidate := range entry.set.Candidates {
			if candidate.Path != exactPath || candidate.Generation <= 0 ||
				candidate.Metageneration <= 0 || candidate.Size <= 0 ||
				candidate.SoftDeleted != entry.softDeleted {
				return ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable, true
			}
			if _, duplicate := seen[candidate.Generation]; duplicate {
				return ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable, true
			}
			seen[candidate.Generation] = struct{}{}
		}
	}
	return "", "", false
}

func artifactGenerationSetAmbiguous(set ArtifactGenerationSet) bool {
	return len(set.Candidates) >= artifactClassifierInventoryLimit || set.Truncated
}

func compareArtifactSnapshots(
	before ArtifactSnapshot,
	after ArtifactSnapshot,
) (ArtifactClassification, ArtifactReasonCode, bool) {
	if before.Path != after.Path || before.Generation != after.Generation {
		return ArtifactClassificationGenerationDrift, ArtifactReasonGenerationChangedDuringRead, true
	}
	if before.Metageneration != after.Metageneration {
		return ArtifactClassificationGenerationDrift, ArtifactReasonMetagenerationChangedDuringRead, true
	}
	if !sameArtifactSnapshot(before, after) {
		return ArtifactClassificationUnavailable, ArtifactReasonResponseUnverifiable, true
	}
	return "", "", false
}

func sameArtifactSnapshot(left, right ArtifactSnapshot) bool {
	if left.Path != right.Path || left.SHA256 != right.SHA256 || left.CRC32C != right.CRC32C ||
		left.Size != right.Size || left.Generation != right.Generation ||
		left.Metageneration != right.Metageneration || left.ContentType != right.ContentType ||
		left.ContentEncoding != right.ContentEncoding || left.CacheControl != right.CacheControl ||
		left.SoftDeleted != right.SoftDeleted || len(left.Metadata) != len(right.Metadata) {
		return false
	}
	for key, value := range left.Metadata {
		rightValue, exists := right.Metadata[key]
		if !exists || rightValue != value {
			return false
		}
	}
	return true
}

func artifactInventorySummary(inventory GenerationInventory) ArtifactInventorySummary {
	return ArtifactInventorySummary{
		Performed:           inventory.NonSoftDeleted.Performed || inventory.SoftDeleted.Performed,
		NonSoftDeletedCount: len(inventory.NonSoftDeleted.Candidates),
		SoftDeletedCount:    len(inventory.SoftDeleted.Candidates),
		Truncated:           inventory.NonSoftDeleted.Truncated || inventory.SoftDeleted.Truncated,
		Coverage:            inventory.Coverage,
	}
}

func artifactPinnedLineageFromSnapshot(snapshot ArtifactSnapshot) *ArtifactPinnedLineage {
	return &ArtifactPinnedLineage{
		SHA256:         snapshot.SHA256,
		CRC32C:         snapshot.CRC32C,
		Size:           snapshot.Size,
		Generation:     snapshot.Generation,
		Metageneration: snapshot.Metageneration,
	}
}

func artifactRetentionPhaseAt(
	request ArtifactClassificationRequest,
	observedAt time.Time,
) ArtifactRetentionPhase {
	if observedAt.Before(request.ArtifactExpiresAt) {
		return ArtifactRetentionBeforeExpiry
	}
	return ArtifactRetentionAtAfterExpiry
}

func cloneArtifactClassificationRequest(
	request ArtifactClassificationRequest,
) ArtifactClassificationRequest {
	if request.AcceptedRawLineage != nil {
		lineage := *request.AcceptedRawLineage
		request.AcceptedRawLineage = &lineage
	}
	if request.AcceptedManifestLineage != nil {
		lineage := *request.AcceptedManifestLineage
		request.AcceptedManifestLineage = &lineage
	}
	if request.ForwardFence != nil {
		fence := *request.ForwardFence
		request.ForwardFence = &fence
	}
	return request
}
