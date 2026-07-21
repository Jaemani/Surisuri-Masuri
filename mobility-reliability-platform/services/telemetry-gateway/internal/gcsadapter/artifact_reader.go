package gcsadapter

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const (
	minArtifactGenerationInventoryLimit = 2
	maxArtifactGenerationInventoryLimit = 100
	maxArtifactGenerationReadBytes      = 16 * 1024 * 1024
	maxGCSObjectNameBytes               = 1024
)

// HTTPArtifactInventoryReader owns the HTTP Cloud Storage client used for
// exact compressed-byte reads. It must be closed when the process shuts down.
type HTTPArtifactInventoryReader struct {
	backend artifactInventoryReadBackend
	closeFn func() error

	closeOnce sync.Once
	closeErr  error
}

var _ ingest.ArtifactInventoryReader = (*HTTPArtifactInventoryReader)(nil)

// NewHTTPArtifactInventoryReader deliberately constructs only the HTTP Cloud
// Storage client and fixes its OAuth scope to read-only. It accepts no client
// options that could replace the transport or broaden the scope.
func NewHTTPArtifactInventoryReader(
	ctx context.Context,
	bucketName string,
) (*HTTPArtifactInventoryReader, error) {
	if ctx == nil || !validArtifactBucketName(bucketName) {
		return nil, ingest.ErrArtifactUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, err := storage.NewClient(ctx, option.WithScopes(storage.ScopeReadOnly))
	if err != nil {
		return nil, mapArtifactReaderError(ctx, err, false)
	}
	return &HTTPArtifactInventoryReader{
		backend: storageArtifactInventoryReadBackend{bucket: client.Bucket(bucketName)},
		closeFn: client.Close,
	}, nil
}

func (r *HTTPArtifactInventoryReader) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.closeFn == nil {
			return
		}
		if err := r.closeFn(); err != nil {
			r.closeErr = ingest.ErrArtifactProviderUnavailable
		}
	})
	return r.closeErr
}

func (r *HTTPArtifactInventoryReader) ListExactPathGenerations(
	ctx context.Context,
	exactPath string,
	limit int,
) (ingest.GenerationInventory, error) {
	inventory := ingest.GenerationInventory{Coverage: ingest.ArtifactInventoryCoverageIncomplete}
	if r == nil || r.backend == nil || ctx == nil || !validExactArtifactPath(exactPath) ||
		limit < minArtifactGenerationInventoryLimit || limit > maxArtifactGenerationInventoryLimit {
		return inventory, ingest.ErrArtifactUnavailable
	}
	if err := ctx.Err(); err != nil {
		return inventory, err
	}

	regular, err := r.listGenerationSet(ctx, exactPath, limit, false)
	inventory.NonSoftDeleted = regular
	if err != nil {
		return inventory, mapArtifactReaderError(ctx, err, false)
	}

	softDeleted, err := r.listGenerationSet(ctx, exactPath, limit, true)
	inventory.SoftDeleted = softDeleted
	if err != nil {
		return inventory, mapArtifactReaderError(ctx, err, false)
	}

	inventory.Coverage = ingest.ArtifactInventoryCoverageComplete
	return inventory, nil
}

func (r *HTTPArtifactInventoryReader) InspectGeneration(
	ctx context.Context,
	exactPath string,
	generation int64,
) (ingest.ArtifactSnapshot, error) {
	if r == nil || r.backend == nil || ctx == nil || !validExactArtifactPath(exactPath) || generation <= 0 {
		return ingest.ArtifactSnapshot{}, ingest.ErrArtifactUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ingest.ArtifactSnapshot{}, err
	}
	attrs, err := r.backend.InspectGeneration(ctx, exactPath, generation)
	if err != nil {
		return ingest.ArtifactSnapshot{}, mapArtifactReaderError(ctx, err, true)
	}
	if !validArtifactAttrs(attrs, exactPath, generation, false) {
		return ingest.ArtifactSnapshot{}, ingest.ErrArtifactResponseUnverifiable
	}
	return artifactSnapshotFromAttrs(attrs, false), nil
}

func (r *HTTPArtifactInventoryReader) ReadManifestGeneration(
	ctx context.Context,
	target ingest.ArtifactTarget,
	maxBytes int64,
) ([]byte, error) {
	return r.readGeneration(ctx, target, maxBytes, false)
}

func (r *HTTPArtifactInventoryReader) ReadRawGenerationCompressed(
	ctx context.Context,
	target ingest.ArtifactTarget,
	maxBytes int64,
) ([]byte, error) {
	return r.readGeneration(ctx, target, maxBytes, true)
}

func (r *HTTPArtifactInventoryReader) listGenerationSet(
	ctx context.Context,
	exactPath string,
	limit int,
	softDeleted bool,
) (ingest.ArtifactGenerationSet, error) {
	set := ingest.ArtifactGenerationSet{Performed: true}
	query := &storage.Query{
		Prefix:      exactPath,
		Versions:    !softDeleted,
		Projection:  storage.ProjectionNoACL,
		MatchGlob:   exactPath,
		SoftDeleted: softDeleted,
	}
	objects := r.backend.Objects(ctx, query)
	if objects == nil {
		return set, ingest.ErrArtifactResponseUnverifiable
	}
	pageInfo := objects.PageInfo()
	if pageInfo == nil {
		return set, ingest.ErrArtifactResponseUnverifiable
	}
	pageInfo.MaxSize = limit + 1
	seenGenerations := make(map[int64]struct{}, limit+1)

	for {
		if err := ctx.Err(); err != nil {
			return set, err
		}
		attrs, err := objects.Next()
		if errors.Is(err, iterator.Done) {
			return set, nil
		}
		if err != nil {
			return set, err
		}
		if !validArtifactAttrs(attrs, exactPath, 0, softDeleted) {
			return set, ingest.ErrArtifactResponseUnverifiable
		}
		if _, duplicate := seenGenerations[attrs.Generation]; duplicate {
			return set, ingest.ErrArtifactResponseUnverifiable
		}
		seenGenerations[attrs.Generation] = struct{}{}
		set.Candidates = append(set.Candidates, artifactSnapshotFromAttrs(attrs, softDeleted))
		if len(set.Candidates) > limit {
			set.Candidates = set.Candidates[:limit]
			set.Truncated = true
			return set, nil
		}
	}
}

func (r *HTTPArtifactInventoryReader) readGeneration(
	ctx context.Context,
	target ingest.ArtifactTarget,
	maxBytes int64,
	readCompressed bool,
) ([]byte, error) {
	if r == nil || r.backend == nil || ctx == nil || !validExactArtifactPath(target.Path) ||
		target.Generation <= 0 || target.Metageneration <= 0 ||
		maxBytes <= 0 || maxBytes > maxArtifactGenerationReadBytes {
		return nil, ingest.ErrArtifactUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader, err := r.backend.OpenGeneration(
		ctx,
		target.Path,
		target.Generation,
		target.Metageneration,
		readCompressed,
	)
	if err != nil {
		return nil, mapArtifactReaderError(ctx, err, true)
	}
	if reader == nil {
		return nil, ingest.ErrArtifactUnavailable
	}

	content, readErr := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, mapArtifactReaderError(ctx, readErr, false)
	}
	if closeErr != nil {
		return nil, mapArtifactReaderError(ctx, closeErr, false)
	}
	if int64(len(content)) > maxBytes {
		return nil, ingest.ErrArtifactReadLimitExceeded
	}
	return content, nil
}

func artifactSnapshotFromAttrs(attrs *storage.ObjectAttrs, softDeleted bool) ingest.ArtifactSnapshot {
	if attrs == nil {
		return ingest.ArtifactSnapshot{}
	}
	return ingest.ArtifactSnapshot{
		Path:            attrs.Name,
		SHA256:          attrs.Metadata["sha256"],
		CRC32C:          attrs.CRC32C,
		Size:            attrs.Size,
		Generation:      attrs.Generation,
		Metageneration:  attrs.Metageneration,
		ContentType:     attrs.ContentType,
		ContentEncoding: attrs.ContentEncoding,
		CacheControl:    attrs.CacheControl,
		Metadata:        cloneMetadata(attrs.Metadata),
		SoftDeleted:     softDeleted,
	}
}

func mapArtifactReaderError(ctx context.Context, err error, allowGenerationNotFound bool) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ingest.ErrArtifactResponseUnverifiable) {
		return ingest.ErrArtifactResponseUnverifiable
	}
	if allowGenerationNotFound && (errors.Is(err, storage.ErrObjectNotExist) ||
		status.Code(err) == codes.NotFound) {
		return ingest.ErrArtifactGenerationNotFound
	}
	if isArtifactReaderPreconditionFailure(err) {
		return ingest.ErrArtifactPreconditionDrift
	}
	var apiError *googleapi.Error
	if errors.As(err, &apiError) {
		switch apiError.Code {
		case 404:
			if allowGenerationNotFound {
				return ingest.ErrArtifactGenerationNotFound
			}
			return ingest.ErrArtifactProviderUnavailable
		case 401, 403:
			return ingest.ErrArtifactPermissionDenied
		case 408, 504:
			return ingest.ErrArtifactProviderTimeout
		case 429:
			return ingest.ErrArtifactQuotaLimited
		case 499:
			return ingest.ErrArtifactProviderCancelled
		default:
			return ingest.ErrArtifactProviderUnavailable
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return ingest.ErrArtifactProviderTimeout
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return ingest.ErrArtifactProviderCancelled
	}
	switch status.Code(err) {
	case codes.PermissionDenied, codes.Unauthenticated:
		return ingest.ErrArtifactPermissionDenied
	case codes.ResourceExhausted:
		return ingest.ErrArtifactQuotaLimited
	case codes.Unavailable:
		return ingest.ErrArtifactProviderUnavailable
	default:
		return ingest.ErrArtifactProviderUnavailable
	}
}

func isArtifactReaderPreconditionFailure(err error) bool {
	var apiError *googleapi.Error
	return errors.As(err, &apiError) && apiError.Code == 412 ||
		status.Code(err) == codes.FailedPrecondition
}

func validArtifactAttrs(
	attrs *storage.ObjectAttrs,
	exactPath string,
	expectedGeneration int64,
	softDeleted bool,
) bool {
	if attrs == nil || attrs.Name != exactPath || attrs.Generation <= 0 ||
		attrs.Metageneration <= 0 || attrs.Size <= 0 ||
		(!attrs.SoftDeleteTime.IsZero()) != softDeleted {
		return false
	}
	return expectedGeneration == 0 || attrs.Generation == expectedGeneration
}

func validExactArtifactPath(path string) bool {
	if path == "" || len(path) > maxGCSObjectNameBytes || path[0] == '/' || path[len(path)-1] == '/' {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	for i := 0; i < len(path); i++ {
		character := path[i]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '/', '-', '_', '.', '=':
			continue
		default:
			return false
		}
	}
	return true
}

func validArtifactBucketName(name string) bool {
	if name == "" || len(name) > 222 || strings.TrimSpace(name) != name {
		return false
	}
	for i := 0; i < len(name); i++ {
		character := name[i]
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '-', '_', '.':
			continue
		default:
			return false
		}
	}
	return true
}

type artifactObjectIterator interface {
	Next() (*storage.ObjectAttrs, error)
	PageInfo() *iterator.PageInfo
}

type artifactInventoryReadBackend interface {
	Objects(context.Context, *storage.Query) artifactObjectIterator
	InspectGeneration(context.Context, string, int64) (*storage.ObjectAttrs, error)
	OpenGeneration(context.Context, string, int64, int64, bool) (io.ReadCloser, error)
}

type storageArtifactInventoryReadBackend struct {
	bucket *storage.BucketHandle
}

func (b storageArtifactInventoryReadBackend) Objects(
	ctx context.Context,
	query *storage.Query,
) artifactObjectIterator {
	return b.bucket.Objects(ctx, query)
}

func (b storageArtifactInventoryReadBackend) InspectGeneration(
	ctx context.Context,
	path string,
	generation int64,
) (*storage.ObjectAttrs, error) {
	return b.bucket.Object(path).Generation(generation).Attrs(ctx)
}

func (b storageArtifactInventoryReadBackend) OpenGeneration(
	ctx context.Context,
	path string,
	generation int64,
	metageneration int64,
	readCompressed bool,
) (io.ReadCloser, error) {
	return b.bucket.Object(path).
		Generation(generation).
		If(storage.Conditions{
			GenerationMatch:     generation,
			MetagenerationMatch: metageneration,
		}).
		ReadCompressed(readCompressed).
		NewReader(ctx)
}
