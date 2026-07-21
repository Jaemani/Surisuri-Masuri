package gcsadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestArtifactInventoryReaderUsesSeparateExactVersionQueries(t *testing.T) {
	exactPath := artifactReaderExactPath()
	metadata := map[string]string{"sha256": strings.Repeat("a", 64), "tenant_id": "tenant"}
	regularAttrs := artifactReaderAttrs(exactPath, 101, metadata)
	softAttrs := artifactReaderSoftAttrs(exactPath, 102, map[string]string{"sha256": strings.Repeat("b", 64)})
	backend := &artifactInventoryReadBackendFake{
		iterators: []*artifactObjectIteratorFake{
			{attrs: []*storage.ObjectAttrs{regularAttrs}},
			{attrs: []*storage.ObjectAttrs{softAttrs}},
		},
	}
	reader := &HTTPArtifactInventoryReader{backend: backend}

	inventory, err := reader.ListExactPathGenerations(context.Background(), exactPath, 2)
	if err != nil {
		t.Fatalf("ListExactPathGenerations() = %v", err)
	}
	if inventory.Coverage != ingest.ArtifactInventoryCoverageComplete ||
		!inventory.NonSoftDeleted.Performed || !inventory.SoftDeleted.Performed ||
		inventory.NonSoftDeleted.Truncated || inventory.SoftDeleted.Truncated {
		t.Fatalf("inventory flags = %#v", inventory)
	}
	if len(inventory.NonSoftDeleted.Candidates) != 1 ||
		inventory.NonSoftDeleted.Candidates[0].Generation != regularAttrs.Generation ||
		inventory.NonSoftDeleted.Candidates[0].SoftDeleted {
		t.Fatalf("regular candidates = %#v", inventory.NonSoftDeleted.Candidates)
	}
	if len(inventory.SoftDeleted.Candidates) != 1 ||
		inventory.SoftDeleted.Candidates[0].Generation != softAttrs.Generation ||
		!inventory.SoftDeleted.Candidates[0].SoftDeleted {
		t.Fatalf("soft candidates = %#v", inventory.SoftDeleted.Candidates)
	}
	if len(backend.queries) != 2 {
		t.Fatalf("query count = %d", len(backend.queries))
	}
	regularQuery, softQuery := backend.queries[0], backend.queries[1]
	if regularQuery.Prefix != exactPath || regularQuery.MatchGlob != exactPath ||
		!regularQuery.Versions || regularQuery.SoftDeleted ||
		regularQuery.Projection != storage.ProjectionNoACL {
		t.Fatalf("regular query = %#v", regularQuery)
	}
	if softQuery.Prefix != exactPath || softQuery.MatchGlob != exactPath ||
		softQuery.Versions || !softQuery.SoftDeleted ||
		softQuery.Projection != storage.ProjectionNoACL {
		t.Fatalf("soft query = %#v", softQuery)
	}
	if backend.iteratorsUsed[0].pageInfo.MaxSize != 3 || backend.iteratorsUsed[1].pageInfo.MaxSize != 3 {
		t.Fatalf("page sizes = %d, %d", backend.iteratorsUsed[0].pageInfo.MaxSize, backend.iteratorsUsed[1].pageInfo.MaxSize)
	}

	metadata["sha256"] = "changed-after-list"
	if inventory.NonSoftDeleted.Candidates[0].SHA256 != strings.Repeat("a", 64) ||
		inventory.NonSoftDeleted.Candidates[0].Metadata["sha256"] != strings.Repeat("a", 64) {
		t.Fatal("snapshot retained mutable provider metadata")
	}
	inventory.NonSoftDeleted.Candidates[0].Metadata["tenant_id"] = "changed-in-result"
	if regularAttrs.Metadata["tenant_id"] != "tenant" {
		t.Fatal("result metadata mutation changed provider attrs")
	}
}

func TestArtifactInventoryReaderRejectsPrefixSiblingFailClosed(t *testing.T) {
	exactPath := artifactReaderExactPath()
	backend := &artifactInventoryReadBackendFake{
		iterators: []*artifactObjectIteratorFake{{
			attrs: []*storage.ObjectAttrs{artifactReaderAttrs(exactPath+".bak", 999, nil)},
		}},
	}
	reader := &HTTPArtifactInventoryReader{backend: backend}

	inventory, err := reader.ListExactPathGenerations(context.Background(), exactPath, 2)
	if !errors.Is(err, ingest.ErrArtifactResponseUnverifiable) {
		t.Fatalf("prefix sibling = %v", err)
	}
	if inventory.Coverage != ingest.ArtifactInventoryCoverageIncomplete ||
		!inventory.NonSoftDeleted.Performed || inventory.SoftDeleted.Performed {
		t.Fatalf("prefix sibling inventory = %#v", inventory)
	}
}

func TestArtifactInventoryReaderStopsAtExactCandidateLimitPlusOne(t *testing.T) {
	exactPath := artifactReaderExactPath()
	regularIterator := &artifactObjectIteratorFake{
		attrs: []*storage.ObjectAttrs{
			artifactReaderAttrs(exactPath, 1, nil),
			artifactReaderAttrs(exactPath, 2, nil),
			artifactReaderAttrs(exactPath, 3, nil),
			artifactReaderAttrs(exactPath, 4, nil),
		},
		terminalErr: errors.New("iterator must not read a fourth exact candidate"),
	}
	backend := &artifactInventoryReadBackendFake{
		iterators: []*artifactObjectIteratorFake{regularIterator, {}},
	}
	reader := &HTTPArtifactInventoryReader{backend: backend}

	inventory, err := reader.ListExactPathGenerations(context.Background(), exactPath, 2)
	if err != nil {
		t.Fatalf("ListExactPathGenerations() = %v", err)
	}
	if !inventory.NonSoftDeleted.Truncated || len(inventory.NonSoftDeleted.Candidates) != 2 {
		t.Fatalf("regular set = %#v", inventory.NonSoftDeleted)
	}
	if regularIterator.nextCalls != 3 {
		t.Fatalf("iterator calls = %d, want limit+1", regularIterator.nextCalls)
	}
	if regularIterator.pageInfo.MaxSize != 3 {
		t.Fatalf("page max size = %d", regularIterator.pageInfo.MaxSize)
	}
}

func TestArtifactInventoryReaderPreservesPartialCoverageOnError(t *testing.T) {
	exactPath := artifactReaderExactPath()
	backend := &artifactInventoryReadBackendFake{
		iterators: []*artifactObjectIteratorFake{
			{attrs: []*storage.ObjectAttrs{artifactReaderAttrs(exactPath, 1, nil)}},
			{terminalErr: errors.New("provider secret: bucket/path/coordinates")},
		},
	}
	reader := &HTTPArtifactInventoryReader{backend: backend}

	inventory, err := reader.ListExactPathGenerations(context.Background(), exactPath, 2)
	if !errors.Is(err, ingest.ErrArtifactProviderUnavailable) ||
		errors.Is(err, ingest.ErrArtifactGenerationNotFound) {
		t.Fatalf("ListExactPathGenerations() error = %v", err)
	}
	if err.Error() != ingest.ErrArtifactProviderUnavailable.Error() {
		t.Fatalf("provider detail escaped: %q", err)
	}
	if inventory.Coverage != ingest.ArtifactInventoryCoverageIncomplete ||
		!inventory.NonSoftDeleted.Performed || len(inventory.NonSoftDeleted.Candidates) != 1 ||
		!inventory.SoftDeleted.Performed {
		t.Fatalf("partial inventory = %#v", inventory)
	}
}

func TestArtifactInventoryReaderNeverMapsList404ToGenerationMissing(t *testing.T) {
	backend := &artifactInventoryReadBackendFake{
		iterators: []*artifactObjectIteratorFake{{terminalErr: &googleapi.Error{Code: 404, Message: "private path"}}},
	}
	reader := &HTTPArtifactInventoryReader{backend: backend}

	inventory, err := reader.ListExactPathGenerations(context.Background(), artifactReaderExactPath(), 2)
	if !errors.Is(err, ingest.ErrArtifactProviderUnavailable) ||
		errors.Is(err, ingest.ErrArtifactGenerationNotFound) {
		t.Fatalf("list 404 = %v", err)
	}
	if inventory.Coverage != ingest.ArtifactInventoryCoverageIncomplete ||
		!inventory.NonSoftDeleted.Performed || inventory.SoftDeleted.Performed {
		t.Fatalf("list 404 inventory = %#v", inventory)
	}
}

func TestArtifactInventoryReaderMapsOnlyDirect404ToGenerationMissing(t *testing.T) {
	exactPath := artifactReaderExactPath()
	backend := &artifactInventoryReadBackendFake{inspectErr: storage.ErrObjectNotExist}
	reader := &HTTPArtifactInventoryReader{backend: backend}

	_, err := reader.InspectGeneration(context.Background(), exactPath, 42)
	if !errors.Is(err, ingest.ErrArtifactGenerationNotFound) || err.Error() != ingest.ErrArtifactGenerationNotFound.Error() {
		t.Fatalf("InspectGeneration() = %v", err)
	}

	backend.inspectErr = errors.New("provider secret")
	_, err = reader.InspectGeneration(context.Background(), exactPath, 42)
	if !errors.Is(err, ingest.ErrArtifactProviderUnavailable) || err.Error() != ingest.ErrArtifactProviderUnavailable.Error() {
		t.Fatalf("provider inspect error = %v", err)
	}

	backend.openResults = []artifactOpenResult{{err: storage.ErrObjectNotExist}}
	_, err = reader.ReadManifestGeneration(
		context.Background(),
		ingest.ArtifactTarget{Path: exactPath, Generation: 42, Metageneration: 1},
		1024,
	)
	if !errors.Is(err, ingest.ErrArtifactGenerationNotFound) {
		t.Fatalf("ReadManifestGeneration() 404 = %v", err)
	}

	backend.inspectErr = &googleapi.Error{Code: 404, Message: "private path"}
	_, err = reader.InspectGeneration(context.Background(), exactPath, 42)
	if !errors.Is(err, ingest.ErrArtifactGenerationNotFound) || strings.Contains(err.Error(), "private") {
		t.Fatalf("InspectGeneration() HTTP 404 = %v", err)
	}
}

func TestArtifactInventoryReaderRejectsUnverifiableInventoryAttrs(t *testing.T) {
	exactPath := artifactReaderExactPath()
	tests := map[string][]*storage.ObjectAttrs{
		"zero generation": {artifactReaderAttrs(exactPath, 0, nil)},
		"zero metageneration": {func() *storage.ObjectAttrs {
			attrs := artifactReaderAttrs(exactPath, 1, nil)
			attrs.Metageneration = 0
			return attrs
		}()},
		"zero size": {func() *storage.ObjectAttrs {
			attrs := artifactReaderAttrs(exactPath, 1, nil)
			attrs.Size = 0
			return attrs
		}()},
		"soft marker in regular query": {artifactReaderSoftAttrs(exactPath, 1, nil)},
		"duplicate generation": {
			artifactReaderAttrs(exactPath, 1, nil),
			artifactReaderAttrs(exactPath, 1, nil),
		},
	}
	for name, attrs := range tests {
		t.Run(name, func(t *testing.T) {
			backend := &artifactInventoryReadBackendFake{
				iterators: []*artifactObjectIteratorFake{{attrs: attrs}},
			}
			reader := &HTTPArtifactInventoryReader{backend: backend}
			inventory, err := reader.ListExactPathGenerations(context.Background(), exactPath, 2)
			if !errors.Is(err, ingest.ErrArtifactResponseUnverifiable) ||
				inventory.Coverage != ingest.ArtifactInventoryCoverageIncomplete {
				t.Fatalf("unverifiable attrs = %#v, %v", inventory, err)
			}
		})
	}
}

func TestArtifactInventoryReaderRejectsMissingSoftDeleteMarker(t *testing.T) {
	exactPath := artifactReaderExactPath()
	backend := &artifactInventoryReadBackendFake{
		iterators: []*artifactObjectIteratorFake{
			{},
			{attrs: []*storage.ObjectAttrs{artifactReaderAttrs(exactPath, 1, nil)}},
		},
	}
	reader := &HTTPArtifactInventoryReader{backend: backend}

	inventory, err := reader.ListExactPathGenerations(context.Background(), exactPath, 2)
	if !errors.Is(err, ingest.ErrArtifactResponseUnverifiable) ||
		inventory.Coverage != ingest.ArtifactInventoryCoverageIncomplete ||
		!inventory.NonSoftDeleted.Performed || !inventory.SoftDeleted.Performed {
		t.Fatalf("soft marker = %#v, %v", inventory, err)
	}
}

func TestArtifactInventoryReaderPinsReadCompressionAndBounds(t *testing.T) {
	exactPath := artifactReaderExactPath()
	manifestReader := &trackingReadCloser{Reader: bytes.NewReader([]byte("manifest"))}
	rawReader := &trackingReadCloser{Reader: bytes.NewReader([]byte("raw-gzip"))}
	overflowReader := &trackingReadCloser{Reader: bytes.NewReader([]byte("12345"))}
	backend := &artifactInventoryReadBackendFake{
		openResults: []artifactOpenResult{
			{reader: manifestReader},
			{reader: rawReader},
			{reader: overflowReader},
		},
	}
	reader := &HTTPArtifactInventoryReader{backend: backend}
	target := ingest.ArtifactTarget{Path: exactPath, Generation: 77, Metageneration: 2}

	manifest, err := reader.ReadManifestGeneration(context.Background(), target, 8)
	if err != nil || string(manifest) != "manifest" {
		t.Fatalf("manifest read = %q, %v", manifest, err)
	}
	raw, err := reader.ReadRawGenerationCompressed(context.Background(), target, 8)
	if err != nil || string(raw) != "raw-gzip" {
		t.Fatalf("raw read = %q, %v", raw, err)
	}
	oversized, err := reader.ReadRawGenerationCompressed(context.Background(), target, 4)
	if !errors.Is(err, ingest.ErrArtifactReadLimitExceeded) || oversized != nil {
		t.Fatalf("oversized read = %q, %v", oversized, err)
	}
	if len(backend.openCalls) != 3 || backend.openCalls[0].compressed ||
		!backend.openCalls[1].compressed || !backend.openCalls[2].compressed {
		t.Fatalf("open calls = %#v", backend.openCalls)
	}
	for _, call := range backend.openCalls {
		if call.generation != target.Generation || call.metageneration != target.Metageneration {
			t.Fatalf("unpinned open call = %#v", call)
		}
	}
	if !manifestReader.closed || !rawReader.closed || !overflowReader.closed {
		t.Fatal("generation reader was not closed")
	}
}

func TestArtifactInventoryReaderValidatesBoundsBeforeBackend(t *testing.T) {
	unsafePaths := []string{
		"telemetry/*/batch.json.gz",
		"telemetry/?/batch.json.gz",
		"telemetry/[x]/batch.json.gz",
		"telemetry/../batch.json.gz",
		"telemetry/사용자/batch.json.gz",
	}
	for _, path := range unsafePaths {
		t.Run(path, func(t *testing.T) {
			backend := &artifactInventoryReadBackendFake{}
			reader := &HTTPArtifactInventoryReader{backend: backend}
			_, err := reader.ListExactPathGenerations(context.Background(), path, 2)
			if !errors.Is(err, ingest.ErrArtifactUnavailable) || len(backend.queries) != 0 {
				t.Fatalf("unsafe path = %v, calls=%d", err, len(backend.queries))
			}
		})
	}

	for _, limit := range []int{0, 1, maxArtifactGenerationInventoryLimit + 1} {
		backend := &artifactInventoryReadBackendFake{}
		reader := &HTTPArtifactInventoryReader{backend: backend}
		_, err := reader.ListExactPathGenerations(context.Background(), artifactReaderExactPath(), limit)
		if !errors.Is(err, ingest.ErrArtifactUnavailable) || len(backend.queries) != 0 {
			t.Fatalf("limit %d = %v, calls=%d", limit, err, len(backend.queries))
		}
	}
}

func TestArtifactInventoryReaderPreservesCallerContextErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &artifactInventoryReadBackendFake{}
	reader := &HTTPArtifactInventoryReader{backend: backend}
	_, err := reader.ListExactPathGenerations(ctx, artifactReaderExactPath(), 2)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list = %v", err)
	}
	if len(backend.queries) != 0 {
		t.Fatalf("cancelled request reached provider: %d queries", len(backend.queries))
	}
}

func TestArtifactInventoryReaderRejectsUnverifiableInspectAttrs(t *testing.T) {
	exactPath := artifactReaderExactPath()
	tests := map[string]func(*storage.ObjectAttrs){
		"wrong path":       func(attrs *storage.ObjectAttrs) { attrs.Name += ".bak" },
		"wrong generation": func(attrs *storage.ObjectAttrs) { attrs.Generation++ },
		"zero metageneration": func(attrs *storage.ObjectAttrs) {
			attrs.Metageneration = 0
		},
		"soft deleted": func(attrs *storage.ObjectAttrs) {
			attrs.SoftDeleteTime = time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			attrs := artifactReaderAttrs(exactPath, 42, nil)
			mutate(attrs)
			reader := &HTTPArtifactInventoryReader{
				backend: &artifactInventoryReadBackendFake{inspectAttrs: attrs},
			}
			_, err := reader.InspectGeneration(context.Background(), exactPath, 42)
			if !errors.Is(err, ingest.ErrArtifactResponseUnverifiable) {
				t.Fatalf("InspectGeneration() = %v", err)
			}
		})
	}
}

func TestArtifactInventoryReaderNormalizesProviderErrorsWithoutDetails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "http 401", err: &googleapi.Error{Code: 401, Message: "credential detail"}, want: ingest.ErrArtifactPermissionDenied},
		{name: "http 403", err: &googleapi.Error{Code: 403, Message: "bucket/path detail"}, want: ingest.ErrArtifactPermissionDenied},
		{name: "http 429", err: &googleapi.Error{Code: 429, Message: "quota project detail"}, want: ingest.ErrArtifactQuotaLimited},
		{name: "http 500", err: &googleapi.Error{Code: 500, Message: "backend detail"}, want: ingest.ErrArtifactProviderUnavailable},
		{name: "live context cancelled", err: context.Canceled, want: ingest.ErrArtifactProviderCancelled},
		{name: "live context deadline", err: context.DeadlineExceeded, want: ingest.ErrArtifactProviderTimeout},
		{name: "grpc unauthenticated", err: status.Error(codes.Unauthenticated, "token detail"), want: ingest.ErrArtifactPermissionDenied},
		{name: "grpc quota", err: status.Error(codes.ResourceExhausted, "tenant detail"), want: ingest.ErrArtifactQuotaLimited},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &artifactInventoryReadBackendFake{inspectErr: test.err}
			reader := &HTTPArtifactInventoryReader{backend: backend}
			_, err := reader.InspectGeneration(context.Background(), artifactReaderExactPath(), 42)
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("normalized error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestArtifactInventoryReaderMapsReadPreconditionToDrift(t *testing.T) {
	backend := &artifactInventoryReadBackendFake{
		openResults: []artifactOpenResult{{
			err: &googleapi.Error{Code: 412, Message: "generation/path detail"},
		}},
	}
	reader := &HTTPArtifactInventoryReader{backend: backend}
	target := ingest.ArtifactTarget{
		Path:           artifactReaderExactPath(),
		Generation:     42,
		Metageneration: 7,
	}

	_, err := reader.ReadManifestGeneration(context.Background(), target, 1024)
	if !errors.Is(err, ingest.ErrArtifactPreconditionDrift) ||
		err.Error() != ingest.ErrArtifactPreconditionDrift.Error() {
		t.Fatalf("precondition error = %v", err)
	}
	if len(backend.openCalls) != 1 || backend.openCalls[0].generation != 42 ||
		backend.openCalls[0].metageneration != 7 {
		t.Fatalf("open calls = %#v", backend.openCalls)
	}
}

func TestArtifactInventoryReaderCloseIsIdempotentAndRedacted(t *testing.T) {
	closeCalls := 0
	reader := &HTTPArtifactInventoryReader{
		backend: &artifactInventoryReadBackendFake{},
		closeFn: func() error {
			closeCalls++
			return errors.New("credential and endpoint detail")
		},
	}
	for i := 0; i < 2; i++ {
		err := reader.Close()
		if !errors.Is(err, ingest.ErrArtifactProviderUnavailable) || err.Error() != ingest.ErrArtifactProviderUnavailable.Error() {
			t.Fatalf("Close() = %v", err)
		}
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d", closeCalls)
	}
}

func TestArtifactSnapshotIncludesRequiredProviderFields(t *testing.T) {
	exactPath := artifactReaderExactPath()
	attrs := artifactReaderAttrs(exactPath, 88, map[string]string{
		"sha256": strings.Repeat("d", 64),
		"custom": "value",
	})
	backend := &artifactInventoryReadBackendFake{inspectAttrs: attrs}
	reader := &HTTPArtifactInventoryReader{backend: backend}

	snapshot, err := reader.InspectGeneration(context.Background(), exactPath, attrs.Generation)
	if err != nil {
		t.Fatalf("InspectGeneration() = %v", err)
	}
	if snapshot.Path != attrs.Name || snapshot.SHA256 != attrs.Metadata["sha256"] ||
		snapshot.CRC32C != attrs.CRC32C || snapshot.Size != attrs.Size ||
		snapshot.Generation != attrs.Generation || snapshot.Metageneration != attrs.Metageneration ||
		snapshot.ContentType != attrs.ContentType || snapshot.ContentEncoding != attrs.ContentEncoding ||
		snapshot.CacheControl != attrs.CacheControl || snapshot.SoftDeleted {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	attrs.Metadata["custom"] = "provider changed"
	if snapshot.Metadata["custom"] != "value" {
		t.Fatal("inspect snapshot metadata was not cloned")
	}
}

func artifactReaderExactPath() string {
	return "telemetry/v2/tenants/11111111-1111-4111-8111-111111111111/devices/22222222-2222-4222-8222-222222222222/trips/33333333-3333-4333-8333-333333333333/year=2026/month=07/day=21/01982015-4400-7000-8000-000000000001.json.gz"
}

func artifactReaderAttrs(path string, generation int64, metadata map[string]string) *storage.ObjectAttrs {
	return &storage.ObjectAttrs{
		Name:            path,
		CRC32C:          uint32(generation),
		Size:            generation + 100,
		Generation:      generation,
		Metageneration:  2,
		ContentType:     "application/json",
		ContentEncoding: "gzip",
		CacheControl:    "no-store",
		Metadata:        metadata,
	}
}

func artifactReaderSoftAttrs(path string, generation int64, metadata map[string]string) *storage.ObjectAttrs {
	attrs := artifactReaderAttrs(path, generation, metadata)
	attrs.SoftDeleteTime = time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	return attrs
}

type artifactObjectIteratorFake struct {
	attrs       []*storage.ObjectAttrs
	terminalErr error
	nextCalls   int
	pageInfo    iterator.PageInfo
}

func (i *artifactObjectIteratorFake) Next() (*storage.ObjectAttrs, error) {
	i.nextCalls++
	if len(i.attrs) > 0 {
		attrs := i.attrs[0]
		i.attrs = i.attrs[1:]
		return attrs, nil
	}
	if i.terminalErr != nil {
		return nil, i.terminalErr
	}
	return nil, iterator.Done
}

func (i *artifactObjectIteratorFake) PageInfo() *iterator.PageInfo {
	return &i.pageInfo
}

type artifactOpenResult struct {
	reader io.ReadCloser
	err    error
}

type artifactOpenCall struct {
	path           string
	generation     int64
	metageneration int64
	compressed     bool
}

type artifactInventoryReadBackendFake struct {
	queries       []storage.Query
	iterators     []*artifactObjectIteratorFake
	iteratorsUsed []*artifactObjectIteratorFake
	inspectAttrs  *storage.ObjectAttrs
	inspectErr    error
	openResults   []artifactOpenResult
	openCalls     []artifactOpenCall
}

func (b *artifactInventoryReadBackendFake) Objects(
	_ context.Context,
	query *storage.Query,
) artifactObjectIterator {
	if query != nil {
		b.queries = append(b.queries, *query)
	}
	if len(b.iterators) == 0 {
		iterator := &artifactObjectIteratorFake{}
		b.iteratorsUsed = append(b.iteratorsUsed, iterator)
		return iterator
	}
	iterator := b.iterators[0]
	b.iterators = b.iterators[1:]
	b.iteratorsUsed = append(b.iteratorsUsed, iterator)
	return iterator
}

func (b *artifactInventoryReadBackendFake) InspectGeneration(
	_ context.Context,
	_ string,
	_ int64,
) (*storage.ObjectAttrs, error) {
	return b.inspectAttrs, b.inspectErr
}

func (b *artifactInventoryReadBackendFake) OpenGeneration(
	_ context.Context,
	path string,
	generation int64,
	metageneration int64,
	readCompressed bool,
) (io.ReadCloser, error) {
	b.openCalls = append(b.openCalls, artifactOpenCall{
		path:           path,
		generation:     generation,
		metageneration: metageneration,
		compressed:     readCompressed,
	})
	if len(b.openResults) == 0 {
		return nil, errors.New("no open result")
	}
	result := b.openResults[0]
	b.openResults = b.openResults[1:]
	return result.reader, result.err
}

type trackingReadCloser struct {
	*bytes.Reader
	closed   bool
	closeErr error
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return r.closeErr
}
