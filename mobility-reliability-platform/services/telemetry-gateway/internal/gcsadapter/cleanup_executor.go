package gcsadapter

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"cloud.google.com/go/storage"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const cleanupExecutionInventoryLimit = 2

type cleanupExecutionInventoryReader interface {
	ListExactPathGenerations(context.Context, string, int) (ingest.GenerationInventory, error)
	InspectGeneration(context.Context, string, int64) (ingest.ArtifactSnapshot, error)
}

type cleanupGenerationDeleteBackend interface {
	DeleteGeneration(context.Context, string, int64, int64) error
}

type CleanupArtifactExecutor struct {
	reader                cleanupExecutionInventoryReader
	deleter               cleanupGenerationDeleteBackend
	now                   func() time.Time
	validateAuthorization func(firebaseadapter.CleanupExecutionAuthorizationGrant, ingest.CleanupExecutionPlan, time.Time) error
	authorizationDeadline func(firebaseadapter.CleanupExecutionAuthorizationGrant, ingest.CleanupExecutionPlan) (time.Time, error)
}

func NewCleanupArtifactExecutor(bucket *storage.BucketHandle) (*CleanupArtifactExecutor, error) {
	if bucket == nil {
		return nil, errors.New("Cloud Storage bucket is required")
	}
	return &CleanupArtifactExecutor{
		reader: &HTTPArtifactInventoryReader{
			backend: storageArtifactInventoryReadBackend{bucket: bucket},
		},
		deleter:               storageCleanupGenerationDeleteBackend{bucket: bucket},
		now:                   time.Now,
		validateAuthorization: firebaseadapter.ValidateCleanupExecutionAuthorization,
		authorizationDeadline: firebaseadapter.CleanupExecutionAuthorizationDeadline,
	}, nil
}

func (e *CleanupArtifactExecutor) ExecuteCleanupTarget(
	ctx context.Context,
	grant firebaseadapter.CleanupExecutionAuthorizationGrant,
	plan ingest.CleanupExecutionPlan,
) (ingest.CleanupExecutionObservation, error) {
	if e == nil || e.reader == nil || e.deleter == nil || e.now == nil ||
		e.validateAuthorization == nil || e.authorizationDeadline == nil || ctx == nil {
		return ingest.CleanupExecutionObservation{}, ingest.ErrCleanupExecutionUnavailable
	}
	plan, err := ingest.CloneCleanupExecutionPlan(plan)
	if err != nil {
		return ingest.CleanupExecutionObservation{}, ingest.ErrCleanupExecutionUnauthorized
	}
	if err := e.validateAuthorization(grant, plan, e.trustedNow()); err != nil {
		return ingest.CleanupExecutionObservation{}, err
	}
	deadline, err := e.authorizationDeadline(grant, plan)
	if err != nil {
		return ingest.CleanupExecutionObservation{}, err
	}
	boundary := newCleanupExecutionBoundary(ctx, deadline)
	defer boundary.cancel()

	var rawObservation ingest.CleanupArtifactExecutionObservation
	var manifestObservation ingest.CleanupArtifactExecutionObservation
	if plan.Target.Command.Raw != nil {
		rawObservation, err = e.executeArtifact(boundary, grant, plan, plan.Target.Command.Raw)
		if err != nil {
			return ingest.CleanupExecutionObservation{}, err
		}
	} else {
		rawObservation, err = e.observeFreshEmptyPath(boundary, grant, plan, plan.ExpectedRawPath)
		if err != nil {
			return ingest.CleanupExecutionObservation{}, err
		}
	}
	if plan.Target.Command.Manifest != nil {
		manifestObservation, err = e.executeArtifact(boundary, grant, plan, plan.Target.Command.Manifest)
		if err != nil {
			return ingest.CleanupExecutionObservation{}, err
		}
	} else {
		manifestObservation, err = e.observeFreshEmptyPath(boundary, grant, plan, plan.ExpectedManifestPath)
		if err != nil {
			return ingest.CleanupExecutionObservation{}, err
		}
	}
	completedAt := e.trustedNow()
	if err := e.checkBoundary(boundary, grant, plan); err != nil {
		return ingest.CleanupExecutionObservation{}, err
	}
	planHash, err := ingest.CleanupExecutionPlanHash(plan)
	if err != nil {
		return ingest.CleanupExecutionObservation{}, ingest.ErrCleanupExecutionUnavailable
	}
	observation := ingest.CleanupExecutionObservation{
		PlanHash:    planHash,
		TargetHash:  plan.Target.TargetHash,
		Raw:         rawObservation,
		Manifest:    manifestObservation,
		CompletedAt: completedAt,
	}
	if ingest.ValidateCleanupExecutionObservationShape(plan, observation) != nil {
		return ingest.CleanupExecutionObservation{}, ingest.ErrInvalidCleanupExecutionObservation
	}
	return observation, nil
}

func (e *CleanupArtifactExecutor) executeArtifact(
	boundary cleanupExecutionBoundary,
	grant firebaseadapter.CleanupExecutionAuthorizationGrant,
	plan ingest.CleanupExecutionPlan,
	lineage *ingest.ArtifactLineage,
) (ingest.CleanupArtifactExecutionObservation, error) {
	if lineage == nil {
		return ingest.CleanupArtifactExecutionObservation{}, ingest.ErrCleanupExecutionUnavailable
	}
	preflight, err := e.readInventory(boundary, grant, plan, lineage.Path)
	if err != nil {
		return ingest.CleanupArtifactExecutionObservation{}, err
	}
	state, err := cleanupExecutionInventoryStateForTarget(preflight, lineage)
	if err != nil {
		return ingest.CleanupArtifactExecutionObservation{}, err
	}
	if state == cleanupGenerationAbsent {
		return confirmedAbsentObservation(ingest.CleanupDeleteNotAttempted), nil
	}
	if state != cleanupGenerationLive {
		return ingest.CleanupArtifactExecutionObservation{}, ingest.ErrCleanupExecutionGenerationDrift
	}
	if err := e.checkBoundary(boundary, grant, plan); err != nil {
		return ingest.CleanupArtifactExecutionObservation{}, err
	}
	snapshot, inspectErr := e.reader.InspectGeneration(boundary.ctx, lineage.Path, lineage.Generation)
	if err := e.completeProviderCall(boundary, grant, plan, inspectErr); err != nil {
		return ingest.CleanupArtifactExecutionObservation{}, err
	}
	if inspectErr != nil {
		if errors.Is(inspectErr, ingest.ErrArtifactGenerationNotFound) {
			return e.observeFreshEmptyPath(boundary, grant, plan, lineage.Path)
		}
		return ingest.CleanupArtifactExecutionObservation{}, normalizeCleanupExecutionProviderError(boundary.ctx, inspectErr)
	}
	if !cleanupSnapshotMatchesLineage(snapshot, lineage) {
		return ingest.CleanupArtifactExecutionObservation{}, ingest.ErrCleanupExecutionLineageMismatch
	}
	if err := e.checkBoundary(boundary, grant, plan); err != nil {
		return ingest.CleanupArtifactExecutionObservation{}, err
	}
	deleteErr := e.deleter.DeleteGeneration(
		boundary.ctx,
		lineage.Path,
		lineage.Generation,
		lineage.Metageneration,
	)
	if boundaryErr := e.completeProviderCall(boundary, grant, plan, deleteErr); boundaryErr != nil {
		return ingest.CleanupArtifactExecutionObservation{}, boundaryErr
	}
	deleteOutcome := ingest.CleanupDeleteObserved
	var ambiguousDeleteErr error
	if deleteErr != nil {
		switch {
		case errors.Is(deleteErr, ingest.ErrArtifactGenerationNotFound):
			deleteOutcome = ingest.CleanupDeleteNotFound
		case errors.Is(deleteErr, ingest.ErrArtifactProviderTimeout),
			errors.Is(deleteErr, ingest.ErrArtifactProviderCancelled),
			errors.Is(deleteErr, ingest.ErrArtifactProviderUnavailable):
			deleteOutcome = ingest.CleanupDeleteUnknown
			ambiguousDeleteErr = normalizeCleanupExecutionProviderError(boundary.ctx, deleteErr)
		default:
			return ingest.CleanupArtifactExecutionObservation{}, normalizeCleanupExecutionProviderError(boundary.ctx, deleteErr)
		}
	}
	observation, err := e.observeFreshEmptyPath(boundary, grant, plan, lineage.Path)
	if err != nil {
		return ingest.CleanupArtifactExecutionObservation{}, err
	}
	observation.DeleteOutcome = deleteOutcome
	if ambiguousDeleteErr != nil {
		return observation, ambiguousDeleteErr
	}
	return observation, nil
}

func (e *CleanupArtifactExecutor) observeFreshEmptyPath(
	boundary cleanupExecutionBoundary,
	grant firebaseadapter.CleanupExecutionAuthorizationGrant,
	plan ingest.CleanupExecutionPlan,
	path string,
) (ingest.CleanupArtifactExecutionObservation, error) {
	inventory, err := e.readInventory(boundary, grant, plan, path)
	if err != nil {
		return ingest.CleanupArtifactExecutionObservation{}, err
	}
	if len(inventory.NonSoftDeleted.Candidates) != 0 ||
		len(inventory.SoftDeleted.Candidates) != 0 {
		return ingest.CleanupArtifactExecutionObservation{}, ingest.ErrCleanupExecutionGenerationDrift
	}
	return confirmedAbsentObservation(ingest.CleanupDeleteNotAttempted), nil
}

func (e *CleanupArtifactExecutor) readInventory(
	boundary cleanupExecutionBoundary,
	grant firebaseadapter.CleanupExecutionAuthorizationGrant,
	plan ingest.CleanupExecutionPlan,
	path string,
) (ingest.GenerationInventory, error) {
	if err := e.checkBoundary(boundary, grant, plan); err != nil {
		return ingest.GenerationInventory{}, err
	}
	inventory, providerErr := e.reader.ListExactPathGenerations(
		boundary.ctx,
		path,
		cleanupExecutionInventoryLimit,
	)
	if err := e.completeProviderCall(boundary, grant, plan, providerErr); err != nil {
		return ingest.GenerationInventory{}, err
	}
	if providerErr != nil {
		return ingest.GenerationInventory{}, normalizeCleanupExecutionProviderError(boundary.ctx, providerErr)
	}
	if validateCompleteCleanupInventory(inventory, path) != nil {
		return ingest.GenerationInventory{}, ingest.ErrCleanupExecutionUnavailable
	}
	return inventory, nil
}

func (e *CleanupArtifactExecutor) checkBoundary(
	boundary cleanupExecutionBoundary,
	grant firebaseadapter.CleanupExecutionAuthorizationGrant,
	plan ingest.CleanupExecutionPlan,
) error {
	if err := boundary.contextError(); err != nil {
		return err
	}
	return e.validateAuthorization(grant, plan, e.trustedNow())
}

func (e *CleanupArtifactExecutor) completeProviderCall(
	boundary cleanupExecutionBoundary,
	grant firebaseadapter.CleanupExecutionAuthorizationGrant,
	plan ingest.CleanupExecutionPlan,
	providerErr error,
) error {
	if err := e.checkBoundary(boundary, grant, plan); err != nil {
		return err
	}
	if boundaryErr := boundary.contextError(); boundaryErr != nil {
		return boundaryErr
	}
	if providerErr != nil && boundary.parent != nil && boundary.parent.Err() != nil {
		return boundary.parent.Err()
	}
	return nil
}

func (e *CleanupArtifactExecutor) trustedNow() time.Time {
	if e != nil && e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}

type cleanupGenerationState string

const (
	cleanupGenerationLive        cleanupGenerationState = "live"
	cleanupGenerationAbsent      cleanupGenerationState = "absent"
	cleanupGenerationSoftDeleted cleanupGenerationState = "soft_deleted"
)

func cleanupExecutionInventoryStateForTarget(
	inventory ingest.GenerationInventory,
	lineage *ingest.ArtifactLineage,
) (cleanupGenerationState, error) {
	if lineage == nil || validateCompleteCleanupInventory(inventory, lineage.Path) != nil {
		return "", ingest.ErrCleanupExecutionUnavailable
	}
	regular := inventory.NonSoftDeleted.Candidates
	softDeleted := inventory.SoftDeleted.Candidates
	if len(regular) > 1 || len(softDeleted) > 1 {
		return "", ingest.ErrCleanupExecutionGenerationDrift
	}
	for _, candidate := range append(append([]ingest.ArtifactSnapshot{}, regular...), softDeleted...) {
		if !cleanupSnapshotIdentityMatchesLineage(candidate, lineage) {
			return "", ingest.ErrCleanupExecutionGenerationDrift
		}
	}
	if len(regular) == 1 && len(softDeleted) == 1 {
		return "", ingest.ErrCleanupExecutionGenerationDrift
	}
	if len(regular) == 1 {
		if regular[0].SoftDeleted {
			return "", ingest.ErrCleanupExecutionGenerationDrift
		}
		return cleanupGenerationLive, nil
	}
	if len(softDeleted) == 1 {
		if !softDeleted[0].SoftDeleted {
			return "", ingest.ErrCleanupExecutionGenerationDrift
		}
		return cleanupGenerationSoftDeleted, nil
	}
	return cleanupGenerationAbsent, nil
}

func validateCompleteCleanupInventory(inventory ingest.GenerationInventory, path string) error {
	if inventory.Coverage != ingest.ArtifactInventoryCoverageComplete ||
		!inventory.NonSoftDeleted.Performed || !inventory.SoftDeleted.Performed ||
		inventory.NonSoftDeleted.Truncated || inventory.SoftDeleted.Truncated ||
		len(inventory.NonSoftDeleted.Candidates) > cleanupExecutionInventoryLimit ||
		len(inventory.SoftDeleted.Candidates) > cleanupExecutionInventoryLimit {
		return ingest.ErrCleanupExecutionUnavailable
	}
	seen := make(map[int64]struct{}, len(inventory.NonSoftDeleted.Candidates)+len(inventory.SoftDeleted.Candidates))
	for _, set := range []struct {
		candidates  []ingest.ArtifactSnapshot
		softDeleted bool
	}{
		{candidates: inventory.NonSoftDeleted.Candidates, softDeleted: false},
		{candidates: inventory.SoftDeleted.Candidates, softDeleted: true},
	} {
		for _, candidate := range set.candidates {
			if !validCleanupInventorySnapshot(candidate, path, set.softDeleted) {
				return ingest.ErrCleanupExecutionUnavailable
			}
			if _, duplicate := seen[candidate.Generation]; duplicate {
				return ingest.ErrCleanupExecutionUnavailable
			}
			seen[candidate.Generation] = struct{}{}
		}
	}
	return nil
}

func validCleanupInventorySnapshot(
	snapshot ingest.ArtifactSnapshot,
	path string,
	softDeleted bool,
) bool {
	decoded, err := hex.DecodeString(snapshot.SHA256)
	return err == nil && len(decoded) == 32 && snapshot.SHA256 == strings.ToLower(snapshot.SHA256) &&
		snapshot.Path == path && snapshot.Size > 0 && snapshot.Generation > 0 &&
		snapshot.Metageneration > 0 && snapshot.SoftDeleted == softDeleted
}

func cleanupSnapshotIdentityMatchesLineage(
	snapshot ingest.ArtifactSnapshot,
	lineage *ingest.ArtifactLineage,
) bool {
	return lineage != nil && snapshot.Path == lineage.Path &&
		snapshot.SHA256 == lineage.SHA256 && snapshot.CRC32C == lineage.CRC32C &&
		snapshot.Size == lineage.Size && snapshot.Generation == lineage.Generation &&
		snapshot.Metageneration == lineage.Metageneration
}

func cleanupSnapshotMatchesLineage(
	snapshot ingest.ArtifactSnapshot,
	lineage *ingest.ArtifactLineage,
) bool {
	return !snapshot.SoftDeleted && cleanupSnapshotIdentityMatchesLineage(snapshot, lineage)
}

func confirmedAbsentObservation(
	deleteOutcome ingest.CleanupDeleteRPCOutcome,
) ingest.CleanupArtifactExecutionObservation {
	return ingest.CleanupArtifactExecutionObservation{
		DeleteOutcome: deleteOutcome,
		AuditOutcome:  ingest.CleanupAuditConfirmedAbsent,
	}
}

func normalizeCleanupExecutionProviderError(ctx context.Context, err error) error {
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	for _, bounded := range []error{
		ingest.ErrArtifactPermissionDenied,
		ingest.ErrArtifactQuotaLimited,
		ingest.ErrArtifactProviderTimeout,
		ingest.ErrArtifactProviderCancelled,
		ingest.ErrArtifactProviderUnavailable,
		ingest.ErrArtifactResponseUnverifiable,
		ingest.ErrArtifactGenerationNotFound,
		ingest.ErrArtifactPreconditionDrift,
	} {
		if errors.Is(err, bounded) {
			return bounded
		}
	}
	return ingest.ErrCleanupExecutionUnavailable
}

type cleanupExecutionBoundary struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	parent               context.Context
	authorizationLimited bool
}

func newCleanupExecutionBoundary(
	parent context.Context,
	authorizationDeadline time.Time,
) cleanupExecutionBoundary {
	authorizationLimited := true
	if parentDeadline, exists := parent.Deadline(); exists &&
		!authorizationDeadline.Before(parentDeadline) {
		authorizationLimited = false
	}
	ctx, cancel := context.WithDeadline(parent, authorizationDeadline)
	return cleanupExecutionBoundary{
		ctx:                  ctx,
		cancel:               cancel,
		parent:               parent,
		authorizationLimited: authorizationLimited,
	}
}

func (b cleanupExecutionBoundary) contextError() error {
	if b.parent != nil {
		if parentErr := b.parent.Err(); parentErr != nil {
			return parentErr
		}
	}
	if b.ctx != nil && b.ctx.Err() != nil {
		if b.authorizationLimited && errors.Is(b.ctx.Err(), context.DeadlineExceeded) {
			return firebaseadapter.ErrCleanupExecutionAuthorizationExpired
		}
		return b.ctx.Err()
	}
	return nil
}

type storageCleanupGenerationDeleteBackend struct {
	bucket *storage.BucketHandle
}

func (b storageCleanupGenerationDeleteBackend) DeleteGeneration(
	ctx context.Context,
	path string,
	generation int64,
	metageneration int64,
) error {
	if b.bucket == nil || ctx == nil || !validExactArtifactPath(path) || generation <= 0 || metageneration <= 0 {
		return ingest.ErrCleanupExecutionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := b.bucket.Object(path).
		Generation(generation).
		If(storage.Conditions{
			GenerationMatch:     generation,
			MetagenerationMatch: metageneration,
		}).
		Delete(ctx)
	if err != nil {
		return mapArtifactReaderError(ctx, err, true)
	}
	return nil
}
