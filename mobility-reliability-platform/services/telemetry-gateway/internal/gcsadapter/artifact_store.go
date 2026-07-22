// Package gcsadapter persists immutable telemetry artifacts in Cloud Storage.
package gcsadapter

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const (
	artifactCacheControl = "no-store"
	manifestContentType  = "application/json"
	maxRawBodyBytes      = 2 * 1024 * 1024
	uploadChunkSize      = 3 * 1024 * 1024
)

type objectWriteSpec struct {
	ContentType     string
	ContentEncoding string
	CacheControl    string
	Metadata        map[string]string
}

type objectSnapshot struct {
	Path            string
	CRC32C          uint32
	Size            int64
	Generation      int64
	Metageneration  int64
	ContentType     string
	ContentEncoding string
	CacheControl    string
	Metadata        map[string]string
}

type immutableObjectBackend interface {
	Create(context.Context, string, []byte, ingest.ArtifactDigest, objectWriteSpec) (objectSnapshot, error)
	Inspect(context.Context, string, int64) (objectSnapshot, error)
	ReadGeneration(context.Context, string, int64, bool, int64) ([]byte, error)
}

// ArtifactStore writes a deterministic gzip object and its canonical manifest.
// The caller owns the underlying storage client and closes it at shutdown.
type ArtifactStore struct {
	backend                immutableObjectBackend
	recoveryReader         ingest.ArtifactInventoryReader
	now                    func() time.Time
	validateManifestRepair func(ingest.ManifestRepairAuthorizationGrant, ingest.RecoveryManifestWrite, time.Time) error
	manifestRepairDeadline func(ingest.ManifestRepairAuthorizationGrant, ingest.RecoveryManifestWrite) (time.Time, error)
}

var _ ingest.TelemetryArtifactStore = (*ArtifactStore)(nil)
var _ ingest.TelemetryManifestRecoveryStore = (*ArtifactStore)(nil)

func NewArtifactStore(bucket *storage.BucketHandle) (*ArtifactStore, error) {
	if bucket == nil {
		return nil, errors.New("Cloud Storage bucket is required")
	}
	return &ArtifactStore{
		backend: storageBackend{bucket: bucket},
		recoveryReader: &HTTPArtifactInventoryReader{
			backend: storageArtifactInventoryReadBackend{bucket: bucket},
		},
		now:                    time.Now,
		validateManifestRepair: ingest.ValidateManifestRepairAuthorization,
		manifestRepairDeadline: ingest.ManifestRepairAuthorizationDeadline,
	}, nil
}

// CreateManifest performs the only artifact mutation allowed during forward
// reconciliation. It never receives raw bytes and therefore cannot create,
// rewrite, or delete the pinned raw generation.
func (s *ArtifactStore) CreateManifest(
	ctx context.Context,
	grant ingest.ManifestRepairAuthorizationGrant,
	write ingest.RecoveryManifestWrite,
) (ingest.StoredArtifact, error) {
	if s == nil || s.backend == nil || s.recoveryReader == nil || ctx == nil {
		return ingest.StoredArtifact{}, ingest.ErrArtifactUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ingest.StoredArtifact{}, err
	}
	if err := s.validateManifestRepairAuthorization(grant, write, s.trustedNow()); err != nil {
		return ingest.StoredArtifact{}, err
	}
	authorizationDeadline, err := s.manifestRepairAuthorizationDeadline(grant, write)
	if err != nil {
		return ingest.StoredArtifact{}, err
	}
	boundary := newManifestRepairBoundary(ctx, authorizationDeadline)
	defer boundary.cancel()

	spec := recoveryManifestWriteSpec(write)
	snapshot, createErr := s.backend.Create(
		boundary.ctx,
		write.ManifestPath,
		write.CanonicalBody,
		write.Digest,
		spec,
	)
	if err := s.completeManifestRepairProviderCall(boundary, grant, write, createErr); err != nil {
		return ingest.StoredArtifact{}, err
	}
	if createErr == nil {
		stored, err := validateStoredSnapshot(snapshot, write.ManifestPath, write.Digest, spec, false)
		if err != nil {
			return ingest.StoredArtifact{}, stageArtifactError(
				"validate recovery manifest create",
				ingest.ErrManifestArtifactConflict,
				err,
			)
		}
		if err := s.checkManifestRepairBoundary(boundary, grant, write); err != nil {
			return ingest.StoredArtifact{}, err
		}
		return stored, nil
	}
	if !isPreconditionFailure(createErr) && !isAmbiguousManifestCreateFailure(createErr) {
		return ingest.StoredArtifact{}, mapManifestCreateFailure(createErr)
	}
	return s.verifyRecoveryManifestReplay(boundary, grant, write, spec)
}

func (s *ArtifactStore) verifyRecoveryManifestReplay(
	boundary manifestRepairBoundary,
	grant ingest.ManifestRepairAuthorizationGrant,
	write ingest.RecoveryManifestWrite,
	spec objectWriteSpec,
) (ingest.StoredArtifact, error) {
	candidate, err := s.singleRecoveryManifestCandidate(boundary, grant, write)
	if err != nil {
		return ingest.StoredArtifact{}, err
	}
	if err := s.checkManifestRepairBoundary(boundary, grant, write); err != nil {
		return ingest.StoredArtifact{}, err
	}
	preRead, err := s.recoveryReader.InspectGeneration(
		boundary.ctx,
		candidate.Path,
		candidate.Generation,
	)
	if boundaryErr := s.completeManifestRepairProviderCall(boundary, grant, write, err); boundaryErr != nil {
		return ingest.StoredArtifact{}, boundaryErr
	}
	if err != nil {
		return ingest.StoredArtifact{}, normalizeRecoveryReaderError(boundary.ctx, err)
	}
	if !sameArtifactSnapshot(candidate, preRead) {
		return ingest.StoredArtifact{}, ingest.ErrArtifactResponseUnverifiable
	}
	if err := s.checkManifestRepairBoundary(boundary, grant, write); err != nil {
		return ingest.StoredArtifact{}, err
	}
	content, err := s.recoveryReader.ReadManifestGeneration(boundary.ctx, ingest.ArtifactTarget{
		Path:           preRead.Path,
		Generation:     preRead.Generation,
		Metageneration: preRead.Metageneration,
	}, write.Digest.Size)
	if boundaryErr := s.completeManifestRepairProviderCall(boundary, grant, write, err); boundaryErr != nil {
		return ingest.StoredArtifact{}, boundaryErr
	}
	if err != nil {
		return ingest.StoredArtifact{}, normalizeRecoveryReaderError(boundary.ctx, err)
	}
	if !bytes.Equal(content, write.CanonicalBody) || ingest.ComputeArtifactDigest(content) != write.Digest {
		return ingest.StoredArtifact{}, errors.Join(
			ingest.ErrArtifactConflict,
			ingest.ErrManifestArtifactConflict,
		)
	}
	if err := s.checkManifestRepairBoundary(boundary, grant, write); err != nil {
		return ingest.StoredArtifact{}, err
	}
	postRead, err := s.recoveryReader.InspectGeneration(
		boundary.ctx,
		candidate.Path,
		candidate.Generation,
	)
	if boundaryErr := s.completeManifestRepairProviderCall(boundary, grant, write, err); boundaryErr != nil {
		return ingest.StoredArtifact{}, boundaryErr
	}
	if err != nil {
		return ingest.StoredArtifact{}, normalizeRecoveryReaderError(boundary.ctx, err)
	}
	if !sameArtifactSnapshot(preRead, postRead) {
		return ingest.StoredArtifact{}, ingest.ErrArtifactResponseUnverifiable
	}
	recheckedCandidate, err := s.singleRecoveryManifestCandidate(boundary, grant, write)
	if err != nil {
		return ingest.StoredArtifact{}, err
	}
	if !sameArtifactSnapshot(postRead, recheckedCandidate) {
		return ingest.StoredArtifact{}, ingest.ErrArtifactResponseUnverifiable
	}
	stored, err := validateStoredSnapshot(
		objectSnapshotFromArtifactSnapshot(postRead),
		write.ManifestPath,
		write.Digest,
		spec,
		true,
	)
	if err != nil {
		return ingest.StoredArtifact{}, stageArtifactError(
			"validate recovery manifest replay",
			ingest.ErrManifestArtifactConflict,
			err,
		)
	}
	if err := s.checkManifestRepairBoundary(boundary, grant, write); err != nil {
		return ingest.StoredArtifact{}, err
	}
	return stored, nil
}

func (s *ArtifactStore) singleRecoveryManifestCandidate(
	boundary manifestRepairBoundary,
	grant ingest.ManifestRepairAuthorizationGrant,
	write ingest.RecoveryManifestWrite,
) (ingest.ArtifactSnapshot, error) {
	if err := s.checkManifestRepairBoundary(boundary, grant, write); err != nil {
		return ingest.ArtifactSnapshot{}, err
	}
	inventory, err := s.recoveryReader.ListExactPathGenerations(
		boundary.ctx,
		write.ManifestPath,
		2,
	)
	if boundaryErr := s.completeManifestRepairProviderCall(boundary, grant, write, err); boundaryErr != nil {
		return ingest.ArtifactSnapshot{}, boundaryErr
	}
	if err != nil {
		return ingest.ArtifactSnapshot{}, normalizeRecoveryReaderError(boundary.ctx, err)
	}
	if inventory.Coverage != ingest.ArtifactInventoryCoverageComplete ||
		!inventory.NonSoftDeleted.Performed || !inventory.SoftDeleted.Performed ||
		inventory.NonSoftDeleted.Truncated || inventory.SoftDeleted.Truncated {
		return ingest.ArtifactSnapshot{}, ingest.ErrArtifactResponseUnverifiable
	}
	if len(inventory.NonSoftDeleted.Candidates) == 0 && len(inventory.SoftDeleted.Candidates) == 0 {
		return ingest.ArtifactSnapshot{}, ingest.ErrArtifactUnavailable
	}
	if len(inventory.NonSoftDeleted.Candidates) != 1 || len(inventory.SoftDeleted.Candidates) != 0 {
		return ingest.ArtifactSnapshot{}, ingest.ErrArtifactResponseUnverifiable
	}
	candidate := inventory.NonSoftDeleted.Candidates[0]
	if candidate.Path != write.ManifestPath || candidate.SoftDeleted || candidate.Generation <= 0 ||
		candidate.Metageneration <= 0 {
		return ingest.ArtifactSnapshot{}, ingest.ErrArtifactResponseUnverifiable
	}
	return candidate, nil
}

func recoveryManifestWriteSpec(write ingest.RecoveryManifestWrite) objectWriteSpec {
	return objectWriteSpec{
		ContentType:  manifestContentType,
		CacheControl: artifactCacheControl,
		Metadata: map[string]string{
			"artifact_kind":     "telemetry_manifest",
			"artifact_version":  strconv.Itoa(ingest.TelemetryManifestVersion),
			"batch_id":          write.ManifestInput.BatchID,
			"expires_at":        canonicalTime(write.ManifestInput.ArtifactExpiresAt),
			"object_generation": strconv.FormatInt(write.Raw.Generation, 10),
			"sha256":            write.Digest.SHA256,
			"tenant_id":         write.ManifestInput.TenantID,
		},
	}
}

type manifestRepairBoundary struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	parent               context.Context
	authorizationLimited bool
}

func newManifestRepairBoundary(
	parent context.Context,
	authorizationDeadline time.Time,
) manifestRepairBoundary {
	authorizationLimited := true
	if parentDeadline, exists := parent.Deadline(); exists &&
		!authorizationDeadline.Before(parentDeadline) {
		authorizationLimited = false
	}
	ctx, cancel := context.WithDeadline(parent, authorizationDeadline)
	return manifestRepairBoundary{
		ctx: ctx, cancel: cancel, parent: parent,
		authorizationLimited: authorizationLimited,
	}
}

func (b manifestRepairBoundary) contextError() error {
	if b.parent != nil {
		if parentErr := b.parent.Err(); parentErr != nil {
			return parentErr
		}
	}
	if b.ctx != nil && b.ctx.Err() != nil {
		if b.authorizationLimited && errors.Is(b.ctx.Err(), context.DeadlineExceeded) {
			return ingest.ErrManifestRepairAuthorizationExpired
		}
		return b.ctx.Err()
	}
	return nil
}

func (b manifestRepairBoundary) normalizeProviderError(err error) error {
	if contextErr := b.contextError(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return ingest.ErrArtifactProviderCancelled
	}
	return nil
}

func (s *ArtifactStore) trustedNow() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *ArtifactStore) validateManifestRepairAuthorization(
	grant ingest.ManifestRepairAuthorizationGrant,
	write ingest.RecoveryManifestWrite,
	observedAt time.Time,
) error {
	if s != nil && s.validateManifestRepair != nil {
		return s.validateManifestRepair(grant, write, observedAt)
	}
	return ingest.ValidateManifestRepairAuthorization(grant, write, observedAt)
}

func (s *ArtifactStore) manifestRepairAuthorizationDeadline(
	grant ingest.ManifestRepairAuthorizationGrant,
	write ingest.RecoveryManifestWrite,
) (time.Time, error) {
	if s != nil && s.manifestRepairDeadline != nil {
		return s.manifestRepairDeadline(grant, write)
	}
	return ingest.ManifestRepairAuthorizationDeadline(grant, write)
}

func (s *ArtifactStore) checkManifestRepairBoundary(
	boundary manifestRepairBoundary,
	grant ingest.ManifestRepairAuthorizationGrant,
	write ingest.RecoveryManifestWrite,
) error {
	if err := boundary.contextError(); err != nil {
		return err
	}
	return s.validateManifestRepairAuthorization(grant, write, s.trustedNow())
}

func (s *ArtifactStore) completeManifestRepairProviderCall(
	boundary manifestRepairBoundary,
	grant ingest.ManifestRepairAuthorizationGrant,
	write ingest.RecoveryManifestWrite,
	providerErr error,
) error {
	if err := s.checkManifestRepairBoundary(boundary, grant, write); err != nil {
		return err
	}
	return boundary.normalizeProviderError(providerErr)
}

func isAmbiguousManifestCreateFailure(err error) bool {
	var apiError *googleapi.Error
	if errors.As(err, &apiError) {
		switch apiError.Code {
		case 408, 500, 502, 503, 504:
			return true
		}
	}
	if grpcStatus, ok := status.FromError(err); ok {
		switch grpcStatus.Code() {
		case codes.DeadlineExceeded, codes.Unavailable, codes.Unknown, codes.Internal:
			return true
		}
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func mapManifestCreateFailure(err error) error {
	var apiError *googleapi.Error
	if errors.As(err, &apiError) {
		switch apiError.Code {
		case 401, 403:
			return ingest.ErrArtifactPermissionDenied
		case 429:
			return ingest.ErrArtifactQuotaLimited
		default:
			return ingest.ErrArtifactUnavailable
		}
	}
	switch status.Code(err) {
	case codes.PermissionDenied, codes.Unauthenticated:
		return ingest.ErrArtifactPermissionDenied
	case codes.ResourceExhausted:
		return ingest.ErrArtifactQuotaLimited
	default:
		return ingest.ErrArtifactUnavailable
	}
}

func normalizeRecoveryReaderError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	for _, bounded := range []error{
		ingest.ErrArtifactPermissionDenied,
		ingest.ErrArtifactQuotaLimited,
		ingest.ErrArtifactProviderTimeout,
		ingest.ErrArtifactProviderCancelled,
		ingest.ErrArtifactProviderUnavailable,
		ingest.ErrArtifactResponseUnverifiable,
		ingest.ErrArtifactReadLimitExceeded,
		ingest.ErrArtifactGenerationNotFound,
		ingest.ErrArtifactPreconditionDrift,
	} {
		if errors.Is(err, bounded) {
			return bounded
		}
	}
	return ingest.ErrArtifactUnavailable
}

func objectSnapshotFromArtifactSnapshot(snapshot ingest.ArtifactSnapshot) objectSnapshot {
	return objectSnapshot{
		Path:            snapshot.Path,
		CRC32C:          snapshot.CRC32C,
		Size:            snapshot.Size,
		Generation:      snapshot.Generation,
		Metageneration:  snapshot.Metageneration,
		ContentType:     snapshot.ContentType,
		ContentEncoding: snapshot.ContentEncoding,
		CacheControl:    snapshot.CacheControl,
		Metadata:        cloneMetadata(snapshot.Metadata),
	}
}

func sameArtifactSnapshot(left, right ingest.ArtifactSnapshot) bool {
	if left.Path != right.Path || left.SHA256 != right.SHA256 || left.CRC32C != right.CRC32C ||
		left.Size != right.Size || left.Generation != right.Generation ||
		left.Metageneration != right.Metageneration || left.ContentType != right.ContentType ||
		left.ContentEncoding != right.ContentEncoding || left.CacheControl != right.CacheControl ||
		left.SoftDeleted != right.SoftDeleted || len(left.Metadata) != len(right.Metadata) {
		return false
	}
	for key, value := range left.Metadata {
		if right.Metadata[key] != value {
			return false
		}
	}
	return true
}

func (s *ArtifactStore) StoreBatch(
	ctx context.Context,
	write ingest.BatchArtifactWrite,
) (ingest.StoredBatchArtifacts, error) {
	if s == nil || s.backend == nil || ctx == nil {
		return ingest.StoredBatchArtifacts{}, ingest.ErrArtifactUnavailable
	}
	if err := validateBatchWrite(write); err != nil {
		return ingest.StoredBatchArtifacts{}, err
	}

	objectDigest := ingest.ComputeArtifactDigest(write.CompressedBody)
	objectMetadata := map[string]string{
		"artifact_kind":    "telemetry_raw",
		"artifact_version": strconv.Itoa(ingest.TelemetryManifestVersion),
		"batch_id":         write.Manifest.BatchID,
		"body_sha256":      write.Manifest.BodyHash,
		"expires_at":       canonicalTime(write.Manifest.ArtifactExpiresAt),
		"sha256":           objectDigest.SHA256,
		"tenant_id":        write.Manifest.TenantID,
	}
	object, err := s.writeImmutable(
		ctx,
		write.ObjectPath,
		write.CompressedBody,
		objectDigest,
		objectWriteSpec{
			ContentType:     ingest.TelemetryContentType,
			ContentEncoding: ingest.TelemetryCompression,
			CacheControl:    artifactCacheControl,
			Metadata:        objectMetadata,
		},
		true,
	)
	if err != nil {
		return ingest.StoredBatchArtifacts{}, stageArtifactError("store telemetry object", ingest.ErrRawArtifactConflict, err)
	}

	manifestBytes, manifestDigest, err := ingest.CanonicalTelemetryManifest(write.Manifest, object)
	if err != nil {
		return ingest.StoredBatchArtifacts{}, err
	}
	manifestMetadata := map[string]string{
		"artifact_kind":     "telemetry_manifest",
		"artifact_version":  strconv.Itoa(ingest.TelemetryManifestVersion),
		"batch_id":          write.Manifest.BatchID,
		"expires_at":        canonicalTime(write.Manifest.ArtifactExpiresAt),
		"object_generation": strconv.FormatInt(object.Generation, 10),
		"sha256":            manifestDigest.SHA256,
		"tenant_id":         write.Manifest.TenantID,
	}
	manifest, err := s.writeImmutable(
		ctx,
		write.ManifestPath,
		manifestBytes,
		manifestDigest,
		objectWriteSpec{
			ContentType:  manifestContentType,
			CacheControl: artifactCacheControl,
			Metadata:     manifestMetadata,
		},
		false,
	)
	if err != nil {
		return ingest.StoredBatchArtifacts{}, stageArtifactError("store telemetry manifest", ingest.ErrManifestArtifactConflict, err)
	}

	return ingest.StoredBatchArtifacts{Object: object, Manifest: manifest}, nil
}

func (s *ArtifactStore) writeImmutable(
	ctx context.Context,
	path string,
	content []byte,
	digest ingest.ArtifactDigest,
	spec objectWriteSpec,
	readCompressed bool,
) (ingest.StoredArtifact, error) {
	snapshot, err := s.backend.Create(ctx, path, content, digest, spec)
	if err == nil {
		return validateStoredSnapshot(snapshot, path, digest, spec, false)
	}
	if contextErr := contextError(ctx, err); contextErr != nil {
		return ingest.StoredArtifact{}, contextErr
	}
	if !isPreconditionFailure(err) {
		return ingest.StoredArtifact{}, ingest.ErrArtifactUnavailable
	}
	return s.verifyReplay(ctx, path, content, digest, spec, readCompressed)
}

func (s *ArtifactStore) verifyReplay(
	ctx context.Context,
	path string,
	expected []byte,
	digest ingest.ArtifactDigest,
	spec objectWriteSpec,
	readCompressed bool,
) (ingest.StoredArtifact, error) {
	latest, err := s.backend.Inspect(ctx, path, 0)
	if err != nil {
		return ingest.StoredArtifact{}, mapBackendError(ctx, err)
	}
	if latest.Generation <= 0 {
		return ingest.StoredArtifact{}, ingest.ErrArtifactUnavailable
	}
	snapshot, err := s.backend.Inspect(ctx, path, latest.Generation)
	if err != nil {
		return ingest.StoredArtifact{}, mapBackendError(ctx, err)
	}
	if err := validateSnapshotIdentity(snapshot, path, latest.Generation); err != nil {
		return ingest.StoredArtifact{}, err
	}
	actual, err := s.backend.ReadGeneration(
		ctx,
		path,
		snapshot.Generation,
		readCompressed,
		digest.Size+1,
	)
	if err != nil {
		return ingest.StoredArtifact{}, mapBackendError(ctx, err)
	}
	if !bytes.Equal(actual, expected) || ingest.ComputeArtifactDigest(actual) != digest {
		return ingest.StoredArtifact{}, errors.Join(
			ingest.ErrArtifactConflict,
			ingest.ErrArtifactContentConflict,
		)
	}
	rechecked, err := s.backend.Inspect(ctx, path, snapshot.Generation)
	if err != nil {
		return ingest.StoredArtifact{}, mapBackendError(ctx, err)
	}
	if !sameObjectSnapshot(snapshot, rechecked) {
		return ingest.StoredArtifact{}, ingest.ErrArtifactUnavailable
	}
	return validateStoredSnapshot(rechecked, path, digest, spec, true)
}

func validateBatchWrite(write ingest.BatchArtifactWrite) error {
	if len(write.CompressedBody) == 0 || write.ObjectPath != ingest.ExpectedTelemetryObjectPath(write.Manifest) ||
		write.ManifestPath != ingest.ExpectedTelemetryManifestPath(write.Manifest) {
		return ingest.ErrInvalidArtifactManifest
	}
	reader, err := gzip.NewReader(bytes.NewReader(write.CompressedBody))
	if err != nil {
		return ingest.ErrInvalidArtifactManifest
	}
	raw, readErr := io.ReadAll(io.LimitReader(reader, maxRawBodyBytes+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || int64(len(raw)) > maxRawBodyBytes {
		return ingest.ErrInvalidArtifactManifest
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != write.Manifest.BodyHash {
		return ingest.ErrInvalidArtifactManifest
	}
	objectDigest := ingest.ComputeArtifactDigest(write.CompressedBody)
	if _, _, err := ingest.CanonicalTelemetryManifest(write.Manifest, ingest.StoredArtifact{
		Path:           write.ObjectPath,
		SHA256:         objectDigest.SHA256,
		CRC32C:         objectDigest.CRC32C,
		Size:           objectDigest.Size,
		Generation:     1,
		Metageneration: 1,
	}); err != nil {
		return err
	}
	return nil
}

func stageArtifactError(operation string, stageError, err error) error {
	if errors.Is(err, ingest.ErrArtifactConflict) &&
		(stageError == ingest.ErrManifestArtifactConflict || errors.Is(err, ingest.ErrArtifactContentConflict)) {
		return fmt.Errorf("%s: %w", operation, errors.Join(stageError, err))
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func validateStoredSnapshot(
	snapshot objectSnapshot,
	path string,
	digest ingest.ArtifactDigest,
	spec objectWriteSpec,
	replay bool,
) (ingest.StoredArtifact, error) {
	if err := validateSnapshotIdentity(snapshot, path, snapshot.Generation); err != nil {
		return ingest.StoredArtifact{}, err
	}
	if snapshot.Size != digest.Size || snapshot.CRC32C != digest.CRC32C ||
		snapshot.ContentType != spec.ContentType ||
		snapshot.ContentEncoding != spec.ContentEncoding ||
		snapshot.CacheControl != spec.CacheControl {
		return ingest.StoredArtifact{}, ingest.ErrArtifactConflict
	}
	if !metadataMatches(snapshot.Metadata, spec.Metadata, digest.CRC32C) {
		return ingest.StoredArtifact{}, ingest.ErrArtifactConflict
	}
	return ingest.StoredArtifact{
		Path:           snapshot.Path,
		SHA256:         digest.SHA256,
		CRC32C:         digest.CRC32C,
		Size:           digest.Size,
		Generation:     snapshot.Generation,
		Metageneration: snapshot.Metageneration,
		Replay:         replay,
	}, nil
}

func validateSnapshotIdentity(snapshot objectSnapshot, path string, generation int64) error {
	if snapshot.Path != path || generation <= 0 || snapshot.Generation != generation ||
		snapshot.Metageneration <= 0 {
		return ingest.ErrArtifactUnavailable
	}
	return nil
}

func metadataMatches(actual, expected map[string]string, crc32c uint32) bool {
	for key, expectedValue := range expected {
		if actual[key] != expectedValue {
			return false
		}
	}
	crcValue := encodedCRC32C(crc32c)
	allowedExtras := map[string]string{
		"x_emulator_crc32c":  crcValue,
		"x_emulator_upload":  "multipart",
		"x_testbench_crc32c": crcValue,
		"x_testbench_upload": "multipart",
	}
	for key, actualValue := range actual {
		if _, required := expected[key]; required {
			continue
		}
		expectedExtra, allowed := allowedExtras[key]
		if !allowed || expectedExtra != actualValue {
			return false
		}
	}
	return true
}

func encodedCRC32C(checksum uint32) string {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], checksum)
	return base64.StdEncoding.EncodeToString(encoded[:])
}

func sameObjectSnapshot(left, right objectSnapshot) bool {
	if left.Path != right.Path || left.CRC32C != right.CRC32C || left.Size != right.Size ||
		left.Generation != right.Generation || left.Metageneration != right.Metageneration ||
		left.ContentType != right.ContentType || left.ContentEncoding != right.ContentEncoding ||
		left.CacheControl != right.CacheControl || len(left.Metadata) != len(right.Metadata) {
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

func contextError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	switch status.Code(err) {
	case codes.Canceled:
		return context.Canceled
	case codes.DeadlineExceeded:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func mapBackendError(ctx context.Context, err error) error {
	if contextErr := contextError(ctx, err); contextErr != nil {
		return contextErr
	}
	return ingest.ErrArtifactUnavailable
}

func isPreconditionFailure(err error) bool {
	var apiError *googleapi.Error
	return errors.As(err, &apiError) && apiError.Code == 412 ||
		status.Code(err) == codes.FailedPrecondition
}

func canonicalTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

type storageBackend struct {
	bucket *storage.BucketHandle
}

func (b storageBackend) Create(
	ctx context.Context,
	path string,
	content []byte,
	digest ingest.ArtifactDigest,
	spec objectWriteSpec,
) (objectSnapshot, error) {
	writer := b.bucket.Object(path).If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	writer.ContentType = spec.ContentType
	writer.ContentEncoding = spec.ContentEncoding
	writer.CacheControl = spec.CacheControl
	writer.Metadata = cloneMetadata(spec.Metadata)
	writer.CRC32C = digest.CRC32C
	writer.SendCRC32C = true
	writer.ChunkSize = uploadChunkSize

	_, writeErr := writer.Write(content)
	if writeErr != nil {
		closeErr := writer.CloseWithError(writeErr)
		if closeErr != nil && !errors.Is(closeErr, writeErr) {
			return objectSnapshot{}, errors.Join(writeErr, closeErr)
		}
		return objectSnapshot{}, writeErr
	}
	if closeErr := writer.Close(); closeErr != nil {
		return objectSnapshot{}, closeErr
	}
	return snapshotFromAttrs(writer.Attrs()), nil
}

func (b storageBackend) Inspect(
	ctx context.Context,
	path string,
	generation int64,
) (objectSnapshot, error) {
	handle := b.bucket.Object(path)
	if generation > 0 {
		handle = handle.Generation(generation)
	}
	attrs, err := handle.Attrs(ctx)
	if err != nil {
		return objectSnapshot{}, err
	}
	return snapshotFromAttrs(attrs), nil
}

func (b storageBackend) ReadGeneration(
	ctx context.Context,
	path string,
	generation int64,
	readCompressed bool,
	limit int64,
) ([]byte, error) {
	if generation <= 0 || limit <= 0 {
		return nil, ingest.ErrArtifactUnavailable
	}
	handle := b.bucket.Object(path).Generation(generation).ReadCompressed(readCompressed)
	reader, err := handle.NewReader(ctx)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, limit))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return content, nil
}

func snapshotFromAttrs(attrs *storage.ObjectAttrs) objectSnapshot {
	if attrs == nil {
		return objectSnapshot{}
	}
	return objectSnapshot{
		Path:            attrs.Name,
		CRC32C:          attrs.CRC32C,
		Size:            attrs.Size,
		Generation:      attrs.Generation,
		Metageneration:  attrs.Metageneration,
		ContentType:     attrs.ContentType,
		ContentEncoding: attrs.ContentEncoding,
		CacheControl:    attrs.CacheControl,
		Metadata:        cloneMetadata(attrs.Metadata),
	}
}

func cloneMetadata(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
