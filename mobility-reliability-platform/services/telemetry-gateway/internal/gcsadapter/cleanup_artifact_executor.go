package gcsadapter

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/storage"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

// CleanupSingleArtifactExecutor owns exactly one artifact mutation surface. It
// never audits absence and cannot touch the counterpart artifact.
type CleanupSingleArtifactExecutor struct {
	reader                cleanupExecutionInventoryReader
	deleter               cleanupGenerationDeleteBackend
	now                   func() time.Time
	cloneRequest          func(ingest.CleanupArtifactExecutionRequest) (ingest.CleanupArtifactExecutionRequest, error)
	validateResult        func(ingest.CleanupArtifactExecutionRequest, ingest.CleanupArtifactExecutionResult) error
	validateAuthorization func(
		firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		ingest.CleanupArtifactExecutionRequest,
		time.Time,
	) error
	authorizationDeadline func(
		firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
		ingest.CleanupArtifactExecutionRequest,
	) (time.Time, error)
}

func NewCleanupSingleArtifactExecutor(
	bucket *storage.BucketHandle,
) (*CleanupSingleArtifactExecutor, error) {
	if bucket == nil {
		return nil, errors.New("Cloud Storage bucket is required")
	}
	return &CleanupSingleArtifactExecutor{
		reader: &HTTPArtifactInventoryReader{
			backend: storageArtifactInventoryReadBackend{bucket: bucket},
		},
		deleter:               storageCleanupGenerationDeleteBackend{bucket: bucket},
		now:                   time.Now,
		cloneRequest:          ingest.CloneCleanupArtifactExecutionRequest,
		validateResult:        ingest.ValidateCleanupArtifactExecutionResult,
		validateAuthorization: firebaseadapter.ValidateCleanupArtifactExecutionAuthorization,
		authorizationDeadline: firebaseadapter.CleanupArtifactExecutionAuthorizationDeadline,
	}, nil
}

func (e *CleanupSingleArtifactExecutor) ExecuteCleanupArtifact(
	ctx context.Context,
	grant firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
) (ingest.CleanupArtifactExecutionResult, error) {
	if e == nil || e.reader == nil || e.deleter == nil || e.now == nil ||
		e.cloneRequest == nil || e.validateResult == nil ||
		e.validateAuthorization == nil || e.authorizationDeadline == nil || ctx == nil {
		return ingest.CleanupArtifactExecutionResult{}, ingest.ErrCleanupExecutionUnavailable
	}
	request, err := e.cloneRequest(request)
	if err != nil {
		return ingest.CleanupArtifactExecutionResult{}, ingest.ErrCleanupExecutionUnauthorized
	}
	if err := e.validateAuthorization(grant, request, e.trustedNow()); err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	deadline, err := e.authorizationDeadline(grant, request)
	if err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	boundary := newCleanupArtifactExecutionBoundary(ctx, deadline)
	defer boundary.cancel()
	if !request.Targeted {
		return e.knownReadOnlyResult(
			boundary, grant, request, ingest.CleanupDeleteNotAttempted, time.Time{},
		)
	}
	if request.Lineage == nil {
		return ingest.CleanupArtifactExecutionResult{}, ingest.ErrCleanupExecutionUnauthorized
	}

	inventory, err := e.readArtifactInventory(boundary, grant, request)
	if err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	state, err := cleanupExecutionInventoryStateForTarget(inventory, request.Lineage)
	if err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	switch state {
	case cleanupGenerationAbsent:
		return e.knownReadOnlyResult(
			boundary, grant, request, ingest.CleanupDeleteNotAttempted, time.Time{},
		)
	case cleanupGenerationLive:
		// Continue to the exact generation inspect and conditional delete.
	default:
		return ingest.CleanupArtifactExecutionResult{}, ingest.ErrCleanupExecutionGenerationDrift
	}
	if err := e.checkBoundary(boundary, grant, request); err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	snapshot, inspectErr := e.reader.InspectGeneration(
		boundary.ctx, request.Lineage.Path, request.Lineage.Generation,
	)
	if _, err := e.completeProviderCall(boundary, grant, request, inspectErr); err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	if inspectErr != nil {
		if errors.Is(inspectErr, ingest.ErrArtifactGenerationNotFound) {
			return e.knownReadOnlyResult(
				boundary, grant, request, ingest.CleanupDeleteNotAttempted, time.Time{},
			)
		}
		return ingest.CleanupArtifactExecutionResult{},
			normalizeCleanupExecutionProviderError(boundary.ctx, inspectErr)
	}
	if !cleanupSnapshotMatchesLineage(snapshot, request.Lineage) {
		return ingest.CleanupArtifactExecutionResult{}, ingest.ErrCleanupExecutionLineageMismatch
	}
	if err := boundary.contextError(); err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	mutationStartedAt := e.trustedNow()
	if err := e.validateAuthorization(grant, request, mutationStartedAt); err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}

	deleteErr := e.deleter.DeleteGeneration(
		boundary.ctx,
		request.Lineage.Path,
		request.Lineage.Generation,
		request.Lineage.Metageneration,
	)
	completedAt, boundaryErr := e.completeProviderCall(boundary, grant, request, deleteErr)
	if boundaryErr != nil {
		return e.unknownResult(request, mutationStartedAt, completedAt, boundaryErr)
	}
	if deleteErr == nil {
		return e.knownResultAt(
			request, ingest.CleanupDeleteObserved, mutationStartedAt, completedAt,
		)
	}
	if errors.Is(deleteErr, ingest.ErrArtifactGenerationNotFound) {
		return e.knownResultAt(
			request, ingest.CleanupDeleteNotFound, mutationStartedAt, completedAt,
		)
	}
	normalized := normalizeCleanupExecutionProviderError(boundary.ctx, deleteErr)
	if errors.Is(normalized, ingest.ErrArtifactProviderTimeout) ||
		errors.Is(normalized, ingest.ErrArtifactProviderCancelled) ||
		errors.Is(normalized, ingest.ErrArtifactProviderUnavailable) ||
		errors.Is(normalized, ingest.ErrArtifactResponseUnverifiable) ||
		errors.Is(normalized, context.Canceled) || errors.Is(normalized, context.DeadlineExceeded) {
		return e.unknownResult(request, mutationStartedAt, completedAt, normalized)
	}
	return ingest.CleanupArtifactExecutionResult{}, normalized
}

func (e *CleanupSingleArtifactExecutor) readArtifactInventory(
	boundary cleanupArtifactExecutionBoundary,
	grant firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
) (ingest.GenerationInventory, error) {
	if err := e.checkBoundary(boundary, grant, request); err != nil {
		return ingest.GenerationInventory{}, err
	}
	inventory, providerErr := e.reader.ListExactPathGenerations(
		boundary.ctx, request.ExpectedPath, cleanupExecutionInventoryLimit,
	)
	if _, err := e.completeProviderCall(boundary, grant, request, providerErr); err != nil {
		return ingest.GenerationInventory{}, err
	}
	if providerErr != nil {
		return ingest.GenerationInventory{},
			normalizeCleanupExecutionProviderError(boundary.ctx, providerErr)
	}
	if validateCompleteCleanupInventory(inventory, request.ExpectedPath) != nil {
		return ingest.GenerationInventory{}, ingest.ErrCleanupExecutionUnavailable
	}
	return inventory, nil
}

func (e *CleanupSingleArtifactExecutor) knownReadOnlyResult(
	boundary cleanupArtifactExecutionBoundary,
	grant firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
	outcome ingest.CleanupDeleteRPCOutcome,
	mutationStartedAt time.Time,
) (ingest.CleanupArtifactExecutionResult, error) {
	observedAt := e.trustedNow()
	if err := e.checkBoundaryAt(boundary, grant, request, observedAt); err != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	return e.knownResultAt(request, outcome, mutationStartedAt, observedAt)
}

func (e *CleanupSingleArtifactExecutor) knownResultAt(
	request ingest.CleanupArtifactExecutionRequest,
	outcome ingest.CleanupDeleteRPCOutcome,
	mutationStartedAt time.Time,
	observedAt time.Time,
) (ingest.CleanupArtifactExecutionResult, error) {
	result := ingest.CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision: request.DispatchRevision, DeleteOutcome: outcome,
		MutationStartedAt: mutationStartedAt, ObservedAt: observedAt,
	}
	if e.validateResult(request, result) != nil {
		return ingest.CleanupArtifactExecutionResult{}, ingest.ErrInvalidCleanupExecutionObservation
	}
	return result, nil
}

func (e *CleanupSingleArtifactExecutor) unknownResult(
	request ingest.CleanupArtifactExecutionRequest,
	mutationStartedAt time.Time,
	observedAt time.Time,
	err error,
) (ingest.CleanupArtifactExecutionResult, error) {
	errorClass, ok := cleanupArtifactExecutionErrorClass(err)
	if !ok {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	result := ingest.CleanupArtifactExecutionResult{
		RequestHash: request.RequestHash, Artifact: request.Artifact,
		DispatchRevision: request.DispatchRevision, DeleteOutcome: ingest.CleanupDeleteUnknown,
		ErrorClass: errorClass, MutationStartedAt: mutationStartedAt,
		ObservedAt: observedAt,
	}
	if e.validateResult(request, result) != nil {
		return ingest.CleanupArtifactExecutionResult{}, err
	}
	return result, err
}

func cleanupArtifactExecutionErrorClass(
	err error,
) (ingest.CleanupExecutionErrorClass, bool) {
	switch {
	case errors.Is(err, ingest.ErrArtifactProviderTimeout),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, firebaseadapter.ErrCleanupArtifactExecutionAuthorizationExpired):
		return ingest.CleanupExecutionErrorProviderTimeout, true
	case errors.Is(err, ingest.ErrArtifactProviderCancelled), errors.Is(err, context.Canceled):
		return ingest.CleanupExecutionErrorProviderCancelled, true
	case errors.Is(err, ingest.ErrArtifactProviderUnavailable):
		return ingest.CleanupExecutionErrorProviderUnavailable, true
	case errors.Is(err, ingest.ErrArtifactResponseUnverifiable):
		return ingest.CleanupExecutionErrorResponseUnverifiable, true
	default:
		return "", false
	}
}

func (e *CleanupSingleArtifactExecutor) checkBoundary(
	boundary cleanupArtifactExecutionBoundary,
	grant firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
) error {
	return e.checkBoundaryAt(boundary, grant, request, e.trustedNow())
}

func (e *CleanupSingleArtifactExecutor) checkBoundaryAt(
	boundary cleanupArtifactExecutionBoundary,
	grant firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
	observedAt time.Time,
) error {
	if err := boundary.contextError(); err != nil {
		return err
	}
	return e.validateAuthorization(grant, request, observedAt)
}

func (e *CleanupSingleArtifactExecutor) completeProviderCall(
	boundary cleanupArtifactExecutionBoundary,
	grant firebaseadapter.CleanupArtifactExecutionAuthorizationGrant,
	request ingest.CleanupArtifactExecutionRequest,
	providerErr error,
) (time.Time, error) {
	completedAt := e.trustedNow()
	if err := e.checkBoundaryAt(boundary, grant, request, completedAt); err != nil {
		return completedAt, err
	}
	if boundaryErr := boundary.contextError(); boundaryErr != nil {
		return completedAt, boundaryErr
	}
	if providerErr != nil && boundary.parent != nil && boundary.parent.Err() != nil {
		return completedAt, boundary.parent.Err()
	}
	return completedAt, nil
}

func (e *CleanupSingleArtifactExecutor) trustedNow() time.Time {
	if e != nil && e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}

type cleanupArtifactExecutionBoundary struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	parent               context.Context
	authorizationLimited bool
}

func newCleanupArtifactExecutionBoundary(
	parent context.Context,
	authorizationDeadline time.Time,
) cleanupArtifactExecutionBoundary {
	authorizationLimited := true
	if parentDeadline, exists := parent.Deadline(); exists &&
		!authorizationDeadline.Before(parentDeadline) {
		authorizationLimited = false
	}
	ctx, cancel := context.WithDeadline(parent, authorizationDeadline)
	return cleanupArtifactExecutionBoundary{
		ctx: ctx, cancel: cancel, parent: parent,
		authorizationLimited: authorizationLimited,
	}
}

func (b cleanupArtifactExecutionBoundary) contextError() error {
	if b.parent != nil {
		if parentErr := b.parent.Err(); parentErr != nil {
			return parentErr
		}
	}
	if b.ctx != nil && b.ctx.Err() != nil {
		if b.authorizationLimited && errors.Is(b.ctx.Err(), context.DeadlineExceeded) {
			return firebaseadapter.ErrCleanupArtifactExecutionAuthorizationExpired
		}
		return b.ctx.Err()
	}
	return nil
}
