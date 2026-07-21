package ingest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type artifactReaderCallKind string

const (
	artifactCallList         artifactReaderCallKind = "list"
	artifactCallInspect      artifactReaderCallKind = "inspect"
	artifactCallReadManifest artifactReaderCallKind = "read_manifest"
	artifactCallReadRaw      artifactReaderCallKind = "read_raw"
)

type scriptedArtifactReaderCall struct {
	kind        artifactReaderCallKind
	path        string
	generation  int64
	target      ArtifactTarget
	limit       int64
	inventory   GenerationInventory
	snapshot    ArtifactSnapshot
	content     []byte
	err         error
	callback    func()
	deadline    *time.Time
	waitForDone bool
}

type scriptedArtifactReader struct {
	t     *testing.T
	mu    sync.Mutex
	calls []scriptedArtifactReaderCall
	seen  int
}

func newScriptedArtifactReader(t *testing.T, calls ...scriptedArtifactReaderCall) *scriptedArtifactReader {
	t.Helper()
	return &scriptedArtifactReader{t: t, calls: append([]scriptedArtifactReaderCall(nil), calls...)}
}

func (r *scriptedArtifactReader) take(
	ctx context.Context,
	kind artifactReaderCallKind,
	path string,
	generation int64,
	target ArtifactTarget,
	limit int64,
) scriptedArtifactReaderCall {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen >= len(r.calls) {
		r.t.Fatalf("unexpected reader call %s path=%q generation=%d target=%#v limit=%d", kind, path, generation, target, limit)
	}
	call := r.calls[r.seen]
	r.seen++
	if call.kind != kind || call.path != path || call.generation != generation ||
		call.target != target || call.limit != limit {
		r.t.Fatalf(
			"reader call %d = %s path=%q generation=%d target=%#v limit=%d; want %s path=%q generation=%d target=%#v limit=%d",
			r.seen, kind, path, generation, target, limit,
			call.kind, call.path, call.generation, call.target, call.limit,
		)
	}
	if call.deadline != nil {
		got, ok := ctx.Deadline()
		if !ok || !got.Equal(*call.deadline) {
			r.t.Fatalf("reader call %d deadline = %v, %v; want %v", r.seen, got, ok, *call.deadline)
		}
	}
	if call.callback != nil {
		call.callback()
	}
	if call.waitForDone {
		<-ctx.Done()
	}
	return call
}

func (r *scriptedArtifactReader) ListExactPathGenerations(
	ctx context.Context,
	path string,
	limit int,
) (GenerationInventory, error) {
	call := r.take(ctx, artifactCallList, path, 0, ArtifactTarget{}, int64(limit))
	return call.inventory, call.err
}

func (r *scriptedArtifactReader) InspectGeneration(
	ctx context.Context,
	path string,
	generation int64,
) (ArtifactSnapshot, error) {
	call := r.take(ctx, artifactCallInspect, path, generation, ArtifactTarget{}, 0)
	return cloneArtifactSnapshot(call.snapshot), call.err
}

func (r *scriptedArtifactReader) ReadManifestGeneration(
	ctx context.Context,
	target ArtifactTarget,
	limit int64,
) ([]byte, error) {
	call := r.take(ctx, artifactCallReadManifest, "", 0, target, limit)
	return append([]byte(nil), call.content...), call.err
}

func (r *scriptedArtifactReader) ReadRawGenerationCompressed(
	ctx context.Context,
	target ArtifactTarget,
	limit int64,
) ([]byte, error) {
	call := r.take(ctx, artifactCallReadRaw, "", 0, target, limit)
	return append([]byte(nil), call.content...), call.err
}

func (r *scriptedArtifactReader) assertDone(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen != len(r.calls) {
		t.Fatalf("reader consumed %d calls, want %d", r.seen, len(r.calls))
	}
}

func (r *scriptedArtifactReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen
}

type countingArtifactValidator struct {
	delegate telemetryArtifactValidator
	mu       sync.Mutex
	calls    int
}

func (v *countingArtifactValidator) ValidateManifest(
	request ArtifactClassificationRequest,
	snapshot ArtifactSnapshot,
	content []byte,
) artifactManifestValidationResult {
	v.record()
	return v.delegate.ValidateManifest(request, snapshot, content)
}

func (v *countingArtifactValidator) ValidateRaw(
	request ArtifactClassificationRequest,
	snapshot ArtifactSnapshot,
	content []byte,
) artifactContentValidationResult {
	v.record()
	return v.delegate.ValidateRaw(request, snapshot, content)
}

func (v *countingArtifactValidator) Validate(
	request ArtifactClassificationRequest,
	manifest ArtifactSnapshot,
	manifestContent []byte,
	raw ArtifactSnapshot,
	rawContent []byte,
) artifactContentValidationResult {
	v.record()
	return v.delegate.Validate(request, manifest, manifestContent, raw, rawContent)
}

func (v *countingArtifactValidator) record() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
}

func (v *countingArtifactValidator) callCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls
}

func TestReadOnlyArtifactClassifierForwardHappyPathsAndOrder(t *testing.T) {
	tests := []struct {
		name          string
		wantClass     ArtifactClassification
		wantReason    ArtifactReasonCode
		buildCalls    func(artifactContentTestFixture) []scriptedArtifactReaderCall
		wantManifest  bool
		wantRaw       bool
		manifestCount int
		rawCount      int
	}{
		{
			name:          "none",
			wantClass:     ArtifactClassificationNone,
			wantReason:    ArtifactReasonNoCandidates,
			buildCalls:    forwardNoneCalls,
			manifestCount: 0,
			rawCount:      0,
		},
		{
			name:          "valid raw only",
			wantClass:     ArtifactClassificationValidRawOnly,
			wantReason:    ArtifactReasonRawValidManifestAbsent,
			buildCalls:    forwardRawOnlyCalls,
			wantRaw:       true,
			manifestCount: 0,
			rawCount:      1,
		},
		{
			name:          "valid complete",
			wantClass:     ArtifactClassificationValidComplete,
			wantReason:    ArtifactReasonManifestAndReferencedRawValid,
			buildCalls:    forwardCompleteCalls,
			wantManifest:  true,
			wantRaw:       true,
			manifestCount: 1,
			rawCount:      1,
		},
		{
			name:          "manifest only",
			wantClass:     ArtifactClassificationManifestOnly,
			wantReason:    ArtifactReasonReferencedRawNotFound,
			buildCalls:    forwardManifestOnlyCalls,
			wantManifest:  true,
			manifestCount: 1,
			rawCount:      0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newClassifierContentTestFixture(t)
			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			reader := newScriptedArtifactReader(t, test.buildCalls(fixture)...)
			classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
			result, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			assertArtifactClassification(t, result, test.wantClass, test.wantReason)
			assertPinnedPresence(t, result, test.wantManifest, test.wantRaw)
			if result.ManifestInventory.NonSoftDeletedCount != test.manifestCount ||
				result.RawInventory.NonSoftDeletedCount != test.rawCount {
				t.Fatalf("inventory summaries = %#v, %#v", result.ManifestInventory, result.RawInventory)
			}
			reader.assertDone(t)
		})
	}
}

func TestReadOnlyArtifactClassifierAcceptedStatesMissingAndSoftDeleted(t *testing.T) {
	for _, state := range []ReceiptState{ReceiptStored, ReceiptQueued, ReceiptProjected} {
		t.Run(string(state)+" valid complete", func(t *testing.T) {
			fixture := acceptedArtifactContentTestFixture(t)
			fixture.request.ReceiptState = state
			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			reader := newScriptedArtifactReader(t, acceptedCompleteCalls(fixture)...)
			classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
			result, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			assertArtifactClassification(t, result, ArtifactClassificationValidComplete, ArtifactReasonManifestAndReferencedRawValid)
			assertPinnedPresence(t, result, true, true)
			reader.assertDone(t)
		})
	}

	tests := []struct {
		name       string
		manifest   GenerationInventory
		raw        GenerationInventory
		wantReason ArtifactReasonCode
	}{
		{name: "manifest missing", manifest: completeInventory(), raw: completeInventoryWith(true), wantReason: ArtifactReasonAcceptedManifestMissing},
		{name: "raw missing", manifest: completeInventoryWith(true), raw: completeInventory(), wantReason: ArtifactReasonAcceptedRawMissing},
		{name: "both missing", manifest: completeInventory(), raw: completeInventory(), wantReason: ArtifactReasonAcceptedBothMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := acceptedArtifactContentTestFixture(t)
			if len(test.manifest.NonSoftDeleted.Candidates) == 1 {
				test.manifest.NonSoftDeleted.Candidates[0] = fixture.manifestSnapshot
			}
			if len(test.raw.NonSoftDeleted.Candidates) == 1 {
				test.raw.NonSoftDeleted.Candidates[0] = fixture.rawSnapshot
			}
			calls := []scriptedArtifactReaderCall{listCall(fixture.request.ExpectedManifestPath, test.manifest), listCall(fixture.request.ExpectedRawPath, test.raw)}
			if len(test.manifest.NonSoftDeleted.Candidates) == 1 {
				calls = append(calls, stableManifestCalls(fixture)...)
			}
			if len(test.raw.NonSoftDeleted.Candidates) == 1 {
				calls = append(calls, stableRawCalls(fixture)...)
			}
			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			reader := newScriptedArtifactReader(t, calls...)
			classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
			result, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			assertArtifactClassification(t, result, ArtifactClassificationStoredMissing, test.wantReason)
			for _, forbidden := range []ArtifactClassification{ArtifactClassificationNone, ArtifactClassificationValidRawOnly, ArtifactClassificationManifestOnly} {
				if result.Classification == forbidden {
					t.Fatalf("accepted classification returned forward-only class %q", forbidden)
				}
			}
			reader.assertDone(t)
		})
	}

	t.Run("exact accepted manifest generation soft deleted", func(t *testing.T) {
		fixture := acceptedArtifactContentTestFixture(t)
		soft := cloneArtifactSnapshot(fixture.manifestSnapshot)
		soft.SoftDeleted = true
		inventory := completeInventory()
		inventory.SoftDeleted.Candidates = []ArtifactSnapshot{soft}
		now := fixture.request.ReceivedAt.Add(2 * time.Minute)
		reader := newScriptedArtifactReader(t, listCall(fixture.request.ExpectedManifestPath, inventory))
		classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
		result, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request)
		if err != nil {
			t.Fatalf("Classify() error = %v", err)
		}
		assertArtifactClassification(t, result, ArtifactClassificationStoredMissing, ArtifactReasonAcceptedGenerationSoftDeleted)
		reader.assertDone(t)
	})
}

func TestReadOnlyArtifactClassifierAcceptedMetadataPrecedesManifestContentConflict(t *testing.T) {
	fixture := acceptedArtifactContentTestFixture(t)
	noncanonical := append(append([]byte(nil), fixture.manifestBytes...), '\n')
	replaceManifestBytes(t, &fixture, noncanonical)
	fixture.request.AcceptedManifestLineage = lineageFromSnapshot(fixture.manifestSnapshot)
	delete(fixture.rawSnapshot.Metadata, "artifact_kind")

	now := fixture.request.ReceivedAt.Add(2 * time.Minute)
	reader := newScriptedArtifactReader(t, acceptedCompleteCalls(fixture)...)
	classifier := mustArtifactClassifier(
		t,
		reader,
		newTelemetryArtifactContentValidator(),
		func() time.Time { return now },
	)
	result, err := classifier.Classify(
		context.Background(),
		mintClassifierGrant(t, fixture.request, now),
		fixture.request,
	)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	assertArtifactClassification(
		t,
		result,
		ArtifactClassificationMetadataConflict,
		ArtifactReasonRequiredMetadataMismatch,
	)
	reader.assertDone(t)
}

func TestReadOnlyArtifactClassifierAcceptedProviderFailurePrecedesManifestContentConflict(t *testing.T) {
	tests := []struct {
		name        string
		providerErr error
		wantReason  ArtifactReasonCode
	}{
		{name: "permission", providerErr: ErrArtifactPermissionDenied, wantReason: ArtifactReasonPermissionDenied},
		{name: "timeout", providerErr: ErrArtifactProviderTimeout, wantReason: ArtifactReasonProviderTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := acceptedArtifactContentTestFixture(t)
			noncanonical := append(append([]byte(nil), fixture.manifestBytes...), '\n')
			replaceManifestBytes(t, &fixture, noncanonical)
			fixture.request.AcceptedManifestLineage = lineageFromSnapshot(fixture.manifestSnapshot)

			calls := []scriptedArtifactReaderCall{
				listCall(fixture.request.ExpectedManifestPath, inventoryWithRegular(fixture.manifestSnapshot)),
				listCall(fixture.request.ExpectedRawPath, inventoryWithRegular(fixture.rawSnapshot)),
			}
			calls = append(calls, stableManifestCalls(fixture)...)
			calls = append(
				calls,
				inspectCall(fixture.rawSnapshot, nil),
				readCall(
					artifactCallReadRaw,
					fixture.rawSnapshot,
					fixture.rawCompressed,
					MaxTelemetryRawArtifactCompressedBytes,
					test.providerErr,
				),
			)

			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			reader := newScriptedArtifactReader(t, calls...)
			classifier := mustArtifactClassifier(
				t,
				reader,
				newTelemetryArtifactContentValidator(),
				func() time.Time { return now },
			)
			result, err := classifier.Classify(
				context.Background(),
				mintClassifierGrant(t, fixture.request, now),
				fixture.request,
			)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			assertArtifactClassification(
				t,
				result,
				ArtifactClassificationUnavailable,
				test.wantReason,
			)
			reader.assertDone(t)
		})
	}
}

func TestReadOnlyArtifactClassifierInventoryMatrix(t *testing.T) {
	tests := []struct {
		name       string
		accepted   bool
		calls      func(artifactContentTestFixture) []scriptedArtifactReaderCall
		wantClass  ArtifactClassification
		wantReason ArtifactReasonCode
	}{
		{
			name: "incomplete coverage",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				value := completeInventory()
				value.Coverage = ArtifactInventoryCoverageIncomplete
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, value)}
			},
			wantClass: ArtifactClassificationUnavailable, wantReason: ArtifactReasonInventoryCoverageIncomplete,
		},
		{
			name: "regular query not performed",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				value := completeInventory()
				value.NonSoftDeleted.Performed = false
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, value)}
			},
			wantClass: ArtifactClassificationUnavailable, wantReason: ArtifactReasonInventoryCoverageIncomplete,
		},
		{
			name: "soft query not performed",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				value := completeInventory()
				value.SoftDeleted.Performed = false
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, value)}
			},
			wantClass: ArtifactClassificationUnavailable, wantReason: ArtifactReasonInventoryCoverageIncomplete,
		},
		{
			name: "wrong exact path",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				candidate := cloneArtifactSnapshot(f.manifestSnapshot)
				candidate.Path += ".sibling"
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, inventoryWithRegular(candidate))}
			},
			wantClass: ArtifactClassificationUnavailable, wantReason: ArtifactReasonResponseUnverifiable,
		},
		{
			name: "duplicate generation across sets",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				soft := cloneArtifactSnapshot(f.manifestSnapshot)
				soft.SoftDeleted = true
				value := inventoryWithRegular(f.manifestSnapshot)
				value.SoftDeleted.Candidates = []ArtifactSnapshot{soft}
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, value)}
			},
			wantClass: ArtifactClassificationUnavailable, wantReason: ArtifactReasonResponseUnverifiable,
		},
		{
			name: "truncated below observation bound",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				value := inventoryWithRegular(f.manifestSnapshot)
				value.NonSoftDeleted.Truncated = true
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, value)}
			},
			wantClass: ArtifactClassificationUnavailable, wantReason: ArtifactReasonInventoryCoverageIncomplete,
		},
		{
			name: "two regular manifest generations",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				other := cloneArtifactSnapshot(f.manifestSnapshot)
				other.Generation++
				value := inventoryWithRegular(f.manifestSnapshot)
				value.NonSoftDeleted.Candidates = append(value.NonSoftDeleted.Candidates, other)
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, value)}
			},
			wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonMultipleManifestGenerations,
		},
		{
			name: "two regular raw generations",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				other := cloneArtifactSnapshot(f.rawSnapshot)
				other.Generation++
				value := inventoryWithRegular(f.rawSnapshot)
				value.NonSoftDeleted.Candidates = append(value.NonSoftDeleted.Candidates, other)
				return []scriptedArtifactReaderCall{
					listCall(f.request.ExpectedManifestPath, completeInventory()),
					listCall(f.request.ExpectedRawPath, value),
				}
			},
			wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonMultipleRawGenerations,
		},
		{
			name: "unexpected soft deleted generation",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				soft := cloneArtifactSnapshot(f.manifestSnapshot)
				soft.Generation++
				soft.SoftDeleted = true
				value := completeInventory()
				value.SoftDeleted.Candidates = []ArtifactSnapshot{soft}
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, value)}
			},
			wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonSoftDeletedCandidatePresent,
		},
		{
			name: "referenced raw generation absent with other present",
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				other := cloneArtifactSnapshot(f.rawSnapshot)
				other.Generation++
				calls := []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, inventoryWithRegular(f.manifestSnapshot))}
				calls = append(calls, stableManifestCalls(f)...)
				return append(calls, listCall(f.request.ExpectedRawPath, inventoryWithRegular(other)))
			},
			wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonReferencedGenerationMissingOtherPresent,
		},
		{
			name:     "accepted generation absent with other present",
			accepted: true,
			calls: func(f artifactContentTestFixture) []scriptedArtifactReaderCall {
				other := cloneArtifactSnapshot(f.manifestSnapshot)
				other.Generation++
				return []scriptedArtifactReaderCall{listCall(f.request.ExpectedManifestPath, inventoryWithRegular(other))}
			},
			wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonAcceptedGenerationMissingOtherPresent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newClassifierContentTestFixture(t)
			if test.accepted {
				fixture = acceptedArtifactContentTestFixture(t)
			}
			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			reader := newScriptedArtifactReader(t, test.calls(fixture)...)
			classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
			result, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			assertArtifactClassification(t, result, test.wantClass, test.wantReason)
			reader.assertDone(t)
		})
	}
}

func TestReadOnlyArtifactClassifierPinnedReadDriftAndLimits(t *testing.T) {
	tests := []struct {
		name       string
		manifest   bool
		stage      artifactReaderCallKind
		err        error
		mutate     func(*ArtifactSnapshot)
		wantClass  ArtifactClassification
		wantReason ArtifactReasonCode
	}{
		{name: "inspect manifest not found", manifest: true, stage: artifactCallInspect, err: ErrArtifactGenerationNotFound, wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonGenerationChangedDuringRead},
		{name: "read manifest not found", manifest: true, stage: artifactCallReadManifest, err: ErrArtifactGenerationNotFound, wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonGenerationChangedDuringRead},
		{name: "post inspect manifest not found", manifest: true, stage: "post_inspect", err: ErrArtifactGenerationNotFound, wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonGenerationChangedDuringRead},
		{name: "inspect precondition", manifest: true, stage: artifactCallInspect, err: ErrArtifactPreconditionDrift, wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonMetagenerationChangedDuringRead},
		{name: "read raw precondition", manifest: false, stage: artifactCallReadRaw, err: ErrArtifactPreconditionDrift, wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonMetagenerationChangedDuringRead},
		{name: "post inspect raw precondition", manifest: false, stage: "post_inspect", err: ErrArtifactPreconditionDrift, wantClass: ArtifactClassificationGenerationDrift, wantReason: ArtifactReasonMetagenerationChangedDuringRead},
		{name: "same lineage manifest attrs mutated", manifest: true, stage: "post_inspect", mutate: func(value *ArtifactSnapshot) { value.CacheControl = "public" }, wantClass: ArtifactClassificationUnavailable, wantReason: ArtifactReasonResponseUnverifiable},
		{name: "same lineage raw attrs mutated", manifest: false, stage: "post_inspect", mutate: func(value *ArtifactSnapshot) { value.ContentType = "text/plain" }, wantClass: ArtifactClassificationUnavailable, wantReason: ArtifactReasonResponseUnverifiable},
		{name: "manifest read limit", manifest: true, stage: artifactCallReadManifest, err: ErrArtifactReadLimitExceeded, wantClass: ArtifactClassificationManifestConflict, wantReason: ArtifactReasonManifestMalformed},
		{name: "raw read limit", manifest: false, stage: artifactCallReadRaw, err: ErrArtifactReadLimitExceeded, wantClass: ArtifactClassificationRawContentConflict, wantReason: ArtifactReasonStrictPayloadInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newClassifierContentTestFixture(t)
			var snapshot ArtifactSnapshot
			var content []byte
			var calls []scriptedArtifactReaderCall
			if test.manifest {
				snapshot, content = fixture.manifestSnapshot, fixture.manifestBytes
				calls = append(calls, listCall(fixture.request.ExpectedManifestPath, inventoryWithRegular(snapshot)))
			} else {
				snapshot, content = fixture.rawSnapshot, fixture.rawCompressed
				calls = append(calls,
					listCall(fixture.request.ExpectedManifestPath, completeInventory()),
					listCall(fixture.request.ExpectedRawPath, inventoryWithRegular(snapshot)),
				)
			}
			readKind := artifactCallReadRaw
			readLimit := MaxTelemetryRawArtifactCompressedBytes
			if test.manifest {
				readKind = artifactCallReadManifest
				readLimit = MaxTelemetryManifestBytes
			}
			inspectErr := error(nil)
			if test.stage == artifactCallInspect {
				inspectErr = test.err
			}
			calls = append(calls, inspectCall(snapshot, inspectErr))
			if inspectErr == nil {
				readErr := error(nil)
				if test.stage == readKind {
					readErr = test.err
				}
				calls = append(calls, readCall(readKind, snapshot, content, readLimit, readErr))
				if readErr == nil {
					post := cloneArtifactSnapshot(snapshot)
					if test.mutate != nil {
						test.mutate(&post)
					}
					postErr := error(nil)
					if test.stage == "post_inspect" {
						postErr = test.err
					}
					calls = append(calls, inspectResponseCall(snapshot.Path, snapshot.Generation, post, postErr))
				}
			}
			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			reader := newScriptedArtifactReader(t, calls...)
			classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
			result, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			assertArtifactClassification(t, result, test.wantClass, test.wantReason)
			reader.assertDone(t)
		})
	}
}

func TestReadOnlyArtifactClassifierBoundsProviderFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantReason ArtifactReasonCode
	}{
		{name: "permission", err: ErrArtifactPermissionDenied, wantReason: ArtifactReasonPermissionDenied},
		{name: "quota", err: ErrArtifactQuotaLimited, wantReason: ArtifactReasonQuotaLimited},
		{name: "typed timeout", err: ErrArtifactProviderTimeout, wantReason: ArtifactReasonProviderTimeout},
		{name: "context timeout from provider", err: context.DeadlineExceeded, wantReason: ArtifactReasonProviderTimeout},
		{name: "typed cancel", err: ErrArtifactProviderCancelled, wantReason: ArtifactReasonProviderCancelled},
		{name: "context cancel from provider", err: context.Canceled, wantReason: ArtifactReasonProviderCancelled},
		{name: "unavailable", err: ErrArtifactProviderUnavailable, wantReason: ArtifactReasonProviderUnavailable},
		{name: "list generation not found is unverifiable", err: ErrArtifactGenerationNotFound, wantReason: ArtifactReasonResponseUnverifiable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newClassifierContentTestFixture(t)
			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			reader := newScriptedArtifactReader(t, scriptedArtifactReaderCall{
				kind: artifactCallList, path: fixture.request.ExpectedManifestPath,
				limit: artifactClassifierInventoryLimit, err: test.err,
			})
			classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
			result, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			assertArtifactClassification(t, result, ArtifactClassificationUnavailable, test.wantReason)
			reader.assertDone(t)
		})
	}

	for _, test := range []struct {
		name string
		ctx  func() context.Context
		want error
	}{
		{name: "caller canceled", ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, want: context.Canceled},
		{name: "caller deadline", ctx: func() context.Context {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			t.Cleanup(cancel)
			return ctx
		}, want: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newClassifierContentTestFixture(t)
			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			reader := newScriptedArtifactReader(t)
			classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
			_, err := classifier.Classify(test.ctx(), mintClassifierGrant(t, fixture.request, now), fixture.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Classify() error = %v, want %v", err, test.want)
			}
			reader.assertDone(t)
		})
	}
}

func TestReadOnlyArtifactClassifierRejectsAuthorizationMutationBeforeIO(t *testing.T) {
	tests := []struct {
		name        string
		mutateGrant func(ArtifactReadAuthorizationGrant) ArtifactReadAuthorizationGrant
		mutateReq   func(ArtifactClassificationRequest) ArtifactClassificationRequest
		atExpiry    string
		want        error
	}{
		{name: "request binding revision", mutateReq: mutateRequestRevision, want: ErrInvalidArtifactReadAuthorization},
		{name: "request binding fence", mutateReq: mutateRequestFence, want: ErrInvalidArtifactReadAuthorization},
		{name: "issuer", mutateGrant: mutateGrantIssuer, want: ErrInvalidArtifactReadAuthorization},
		{name: "binding", mutateGrant: mutateGrantBinding, want: ErrInvalidArtifactReadAuthorization},
		{name: "seal", mutateGrant: mutateGrantSeal, want: ErrInvalidArtifactReadAuthorization},
		{name: "grant exact expiry", atExpiry: "grant", want: ErrArtifactReadAuthorizationExpired},
		{name: "fence exact expiry", atExpiry: "fence", want: ErrArtifactReadAuthorizationExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newClassifierContentTestFixture(t)
			now := fixture.request.ReceivedAt.Add(2 * time.Minute)
			grant := mintClassifierGrant(t, fixture.request, now)
			request := fixture.request
			if test.mutateGrant != nil {
				grant = test.mutateGrant(grant)
			}
			if test.mutateReq != nil {
				request = test.mutateReq(request)
			}
			switch test.atExpiry {
			case "grant":
				now = grant.expiresAt
			case "fence":
				now = request.ForwardFence.ExpiresAt
			}
			reader := newScriptedArtifactReader(t)
			validator := &countingArtifactValidator{delegate: newTelemetryArtifactContentValidator()}
			classifier := mustArtifactClassifier(t, reader, validator, func() time.Time { return now })
			_, err := classifier.Classify(context.Background(), grant, request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Classify() error = %v, want %v", err, test.want)
			}
			if reader.callCount() != 0 || validator.callCount() != 0 {
				t.Fatalf("unauthorized calls: reader=%d validator=%d", reader.callCount(), validator.callCount())
			}
		})
	}
}

func TestReadOnlyArtifactClassifierStopsAtAuthorizationExpiryBetweenCalls(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	now := fixture.request.ReceivedAt.Add(2 * time.Minute)
	grant := mintClassifierGrant(t, fixture.request, now)
	reader := newScriptedArtifactReader(t, scriptedArtifactReaderCall{
		kind: artifactCallList, path: fixture.request.ExpectedManifestPath,
		limit:     artifactClassifierInventoryLimit,
		inventory: inventoryWithRegular(fixture.manifestSnapshot),
		callback:  func() { now = grant.expiresAt },
	})
	validator := &countingArtifactValidator{delegate: newTelemetryArtifactContentValidator()}
	classifier := mustArtifactClassifier(t, reader, validator, func() time.Time { return now })
	_, err := classifier.Classify(context.Background(), grant, fixture.request)
	if !errors.Is(err, ErrArtifactReadAuthorizationExpired) {
		t.Fatalf("Classify() error = %v, want expiry", err)
	}
	if reader.callCount() != 1 || validator.callCount() != 0 {
		t.Fatalf("post-expiry calls: reader=%d validator=%d", reader.callCount(), validator.callCount())
	}
}

func TestReadOnlyArtifactClassifierClampsBoundaryDeadlinesAndReadBounds(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*artifactContentTestFixture, time.Time) (context.Context, context.CancelFunc, time.Time)
	}{
		{
			name: "grant deadline",
			configure: func(f *artifactContentTestFixture, base time.Time) (context.Context, context.CancelFunc, time.Time) {
				f.request.ForwardFence.ExpiresAt = base.Add(3 * time.Hour)
				ctx, cancel := context.WithDeadline(context.Background(), base.Add(4*time.Hour))
				return ctx, cancel, base.Add(2 * time.Hour)
			},
		},
		{
			name: "fence deadline",
			configure: func(f *artifactContentTestFixture, base time.Time) (context.Context, context.CancelFunc, time.Time) {
				f.request.ForwardFence.ExpiresAt = base.Add(time.Hour)
				ctx, cancel := context.WithDeadline(context.Background(), base.Add(4*time.Hour))
				return ctx, cancel, base.Add(time.Hour)
			},
		},
		{
			name: "caller deadline",
			configure: func(f *artifactContentTestFixture, base time.Time) (context.Context, context.CancelFunc, time.Time) {
				f.request.ForwardFence.ExpiresAt = base.Add(3 * time.Hour)
				ctx, cancel := context.WithDeadline(context.Background(), base.Add(time.Hour))
				return ctx, cancel, base.Add(time.Hour)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newClassifierContentTestFixture(t)
			base := time.Now().UTC().Add(24 * time.Hour)
			ctx, cancel, wantDeadline := test.configure(&fixture, base)
			defer cancel()
			trustedNow := fixture.request.ReceivedAt.Add(2 * time.Minute)
			grantExpiry := base.Add(2 * time.Hour)
			grant := mintClassifierGrantExpiring(t, fixture.request, trustedNow, grantExpiry)
			badInventory := completeInventory()
			badInventory.Coverage = ArtifactInventoryCoverageIncomplete
			reader := newScriptedArtifactReader(t, scriptedArtifactReaderCall{
				kind: artifactCallList, path: fixture.request.ExpectedManifestPath,
				limit: artifactClassifierInventoryLimit, inventory: badInventory, deadline: &wantDeadline,
			})
			classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return trustedNow })
			result, err := classifier.Classify(ctx, grant, fixture.request)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			assertArtifactClassification(t, result, ArtifactClassificationUnavailable, ArtifactReasonInventoryCoverageIncomplete)
			reader.assertDone(t)
		})
	}

	fixture := newClassifierContentTestFixture(t)
	now := fixture.request.ReceivedAt.Add(2 * time.Minute)
	calls := forwardCompleteCalls(fixture)
	reader := newScriptedArtifactReader(t, calls...)
	classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
	if _, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request); err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	reader.assertDone(t)
}

func TestValidArtifactClassificationOutcomeMatrix(t *testing.T) {
	tests := []struct {
		name           string
		purpose        ArtifactReadPurpose
		classification ArtifactClassification
		reason         ArtifactReasonCode
		want           bool
	}{
		{name: "forward none", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationNone, reason: ArtifactReasonNoCandidates, want: true},
		{name: "accepted rejects none", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationNone, reason: ArtifactReasonNoCandidates},
		{name: "forward raw only", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationValidRawOnly, reason: ArtifactReasonRawValidManifestAbsent, want: true},
		{name: "accepted rejects raw only", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationValidRawOnly, reason: ArtifactReasonRawValidManifestAbsent},
		{name: "valid complete forward", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, want: true},
		{name: "valid complete accepted", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid, want: true},
		{name: "forward manifest only", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationManifestOnly, reason: ArtifactReasonReferencedRawNotFound, want: true},
		{name: "accepted rejects manifest only", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationManifestOnly, reason: ArtifactReasonReferencedRawNotFound},
		{name: "raw conflict", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationRawContentConflict, reason: ArtifactReasonStrictPayloadInvalid, want: true},
		{name: "raw conflict wrong reason", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationRawContentConflict, reason: ArtifactReasonManifestMalformed},
		{name: "manifest conflict", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationManifestConflict, reason: ArtifactReasonManifestNoncanonical, want: true},
		{name: "manifest conflict wrong reason", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationManifestConflict, reason: ArtifactReasonStrictPayloadInvalid},
		{name: "metadata conflict", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationMetadataConflict, reason: ArtifactReasonRequiredMetadataMismatch, want: true},
		{name: "metadata conflict wrong reason", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationMetadataConflict, reason: ArtifactReasonManifestMalformed},
		{name: "shared generation drift", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationGenerationDrift, reason: ArtifactReasonGenerationChangedDuringRead, want: true},
		{name: "forward referenced generation drift", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationGenerationDrift, reason: ArtifactReasonReferencedGenerationMissingOtherPresent, want: true},
		{name: "accepted rejects referenced generation drift", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationGenerationDrift, reason: ArtifactReasonReferencedGenerationMissingOtherPresent},
		{name: "accepted pinned generation drift", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationGenerationDrift, reason: ArtifactReasonAcceptedGenerationMissingOtherPresent, want: true},
		{name: "forward rejects accepted generation drift", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationGenerationDrift, reason: ArtifactReasonAcceptedGenerationMissingOtherPresent},
		{name: "accepted stored missing", purpose: ArtifactReadAcceptedIntegrityAudit, classification: ArtifactClassificationStoredMissing, reason: ArtifactReasonAcceptedRawMissing, want: true},
		{name: "forward rejects stored missing", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationStoredMissing, reason: ArtifactReasonAcceptedRawMissing},
		{name: "unavailable", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationUnavailable, reason: ArtifactReasonProviderUnavailable, want: true},
		{name: "unavailable wrong reason", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationUnavailable, reason: ArtifactReasonNoCandidates},
		{name: "unknown classification", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassification("unknown"), reason: ArtifactReasonNoCandidates},
		{name: "unknown reason", purpose: ArtifactReadForwardRecovery, classification: ArtifactClassificationNone, reason: ArtifactReasonCode("unknown")},
		{name: "unknown purpose", purpose: ArtifactReadPurpose("unknown"), classification: ArtifactClassificationValidComplete, reason: ArtifactReasonManifestAndReferencedRawValid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validArtifactClassificationOutcome(test.purpose, test.classification, test.reason); got != test.want {
				t.Fatalf("validArtifactClassificationOutcome(%q, %q, %q) = %v, want %v", test.purpose, test.classification, test.reason, got, test.want)
			}
		})
	}
}

func TestReadOnlyArtifactClassifierClonesForwardFenceAtEntry(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	now := fixture.request.ReceivedAt.Add(2 * time.Minute)
	grant := mintClassifierGrant(t, fixture.request, now)
	calls := forwardNoneCalls(fixture)
	calls[0].callback = func() {
		fixture.request.ForwardFence.Token++
		fixture.request.ForwardFence.ExpiresAt = now
	}
	reader := newScriptedArtifactReader(t, calls...)
	classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
	result, err := classifier.Classify(context.Background(), grant, fixture.request)
	if err != nil {
		t.Fatalf("Classify() error after caller fence mutation = %v", err)
	}
	assertArtifactClassification(t, result, ArtifactClassificationNone, ArtifactReasonNoCandidates)
	reader.assertDone(t)
}

func TestReadOnlyArtifactClassifierClonesAcceptedLineagesAtEntry(t *testing.T) {
	fixture := acceptedArtifactContentTestFixture(t)
	now := fixture.request.ReceivedAt.Add(2 * time.Minute)
	grant := mintClassifierGrant(t, fixture.request, now)
	calls := acceptedCompleteCalls(fixture)
	calls[0].callback = func() {
		fixture.request.AcceptedManifestLineage.Generation++
		fixture.request.AcceptedManifestLineage.SHA256 = strings.Repeat("0", 64)
		fixture.request.AcceptedRawLineage.Generation++
		fixture.request.AcceptedRawLineage.SHA256 = strings.Repeat("0", 64)
	}
	reader := newScriptedArtifactReader(t, calls...)
	classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
	result, err := classifier.Classify(context.Background(), grant, fixture.request)
	if err != nil {
		t.Fatalf("Classify() error after caller lineage mutation = %v", err)
	}
	assertArtifactClassification(t, result, ArtifactClassificationValidComplete, ArtifactReasonManifestAndReferencedRawValid)
	reader.assertDone(t)
}

func TestReadOnlyArtifactClassifierNormalizesAuthorizationBoundaryDeadline(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	trustedNow := fixture.request.ReceivedAt.Add(2 * time.Minute)
	deadline := time.Now().UTC().Add(150 * time.Millisecond)
	grant := mintClassifierGrantExpiring(t, fixture.request, trustedNow.Add(-time.Minute), deadline)
	reader := newScriptedArtifactReader(t, scriptedArtifactReaderCall{
		kind: artifactCallList, path: fixture.request.ExpectedManifestPath,
		limit: artifactClassifierInventoryLimit, err: context.DeadlineExceeded,
		deadline: &deadline, waitForDone: true,
	})
	classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return trustedNow })
	_, err := classifier.Classify(context.Background(), grant, fixture.request)
	if !errors.Is(err, ErrArtifactReadAuthorizationExpired) {
		t.Fatalf("Classify() error = %v, want authorization expiry", err)
	}
	reader.assertDone(t)
}

func TestArtifactClassificationResultCarriesOnlyBoundedEvidence(t *testing.T) {
	resultType := reflect.TypeOf(ArtifactClassificationResult{})
	for _, forbidden := range []string{"Path", "Metadata", "Body", "Content", "Batch", "Token", "UID", "TenantID", "DeviceID", "TripID"} {
		if _, exists := resultType.FieldByName(forbidden); exists {
			t.Fatalf("result exposes forbidden field %q", forbidden)
		}
	}

	fixture := newClassifierContentTestFixture(t)
	now := fixture.request.ReceivedAt.Add(2 * time.Minute)
	reader := newScriptedArtifactReader(t, forwardCompleteCalls(fixture)...)
	classifier := mustArtifactClassifier(t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now })
	result, err := classifier.Classify(context.Background(), mintClassifierGrant(t, fixture.request, now), fixture.request)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	encoded := fmt.Sprintf("%#v", result)
	for _, secret := range []string{fixture.request.ExpectedRawPath, fixture.request.ExpectedManifestPath, fixture.request.BodyHash, fixture.request.TenantID} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("result representation retained source identity %q", secret)
		}
	}
	if result.ManifestInventory != artifactInventorySummary(inventoryWithRegular(fixture.manifestSnapshot)) ||
		result.RawInventory != artifactInventorySummary(inventoryWithRegular(fixture.rawSnapshot)) {
		t.Fatalf("result summaries are not actual inventories: %#v", result)
	}
	assertPinnedPresence(t, result, true, true)
	reader.assertDone(t)
}

func mustArtifactClassifier(
	t *testing.T,
	reader ArtifactInventoryReader,
	validator telemetryArtifactValidator,
	now func() time.Time,
) *readOnlyArtifactClassifier {
	t.Helper()
	classifier, err := newReadOnlyArtifactClassifier(reader, validator, now)
	if err != nil {
		t.Fatalf("newReadOnlyArtifactClassifier() error = %v", err)
	}
	return classifier
}

func newClassifierContentTestFixture(t *testing.T) artifactContentTestFixture {
	t.Helper()
	fixture := newArtifactContentTestFixture(t)
	fence := *fixture.request.ForwardFence
	fence.ExpiresAt = time.Now().UTC().Add(4 * time.Hour)
	fixture.request.ForwardFence = &fence
	return fixture
}

func mintClassifierGrant(
	t *testing.T,
	request ArtifactClassificationRequest,
	now time.Time,
) ArtifactReadAuthorizationGrant {
	t.Helper()
	expiresAt := time.Now().UTC().Add(3 * time.Hour)
	if request.Purpose == ArtifactReadForwardRecovery && request.ForwardFence.ExpiresAt.Before(expiresAt) {
		expiresAt = request.ForwardFence.ExpiresAt
	}
	return mintClassifierGrantExpiring(t, request, now.Add(-time.Minute), expiresAt)
}

func mintClassifierGrantExpiring(
	t *testing.T,
	request ArtifactClassificationRequest,
	checkedAt time.Time,
	expiresAt time.Time,
) ArtifactReadAuthorizationGrant {
	t.Helper()
	issuer := artifactReadGrantIssuerForwardRecovery
	if request.Purpose == ArtifactReadAcceptedIntegrityAudit {
		issuer = artifactReadGrantIssuerAcceptedIntegrityAudit
	}
	grant, err := mintArtifactReadAuthorizationGrant(
		issuer,
		"classifier-test-policy@1",
		request,
		checkedAt,
		expiresAt,
	)
	if err != nil {
		t.Fatalf("mintArtifactReadAuthorizationGrant() error = %v", err)
	}
	return grant
}

func completeInventory() GenerationInventory {
	return GenerationInventory{
		NonSoftDeleted: ArtifactGenerationSet{Performed: true},
		SoftDeleted:    ArtifactGenerationSet{Performed: true},
		Coverage:       ArtifactInventoryCoverageComplete,
	}
}

func completeInventoryWith(candidate bool) GenerationInventory {
	value := completeInventory()
	if candidate {
		value.NonSoftDeleted.Candidates = []ArtifactSnapshot{{}}
	}
	return value
}

func inventoryWithRegular(snapshot ArtifactSnapshot) GenerationInventory {
	value := completeInventory()
	value.NonSoftDeleted.Candidates = []ArtifactSnapshot{cloneArtifactSnapshot(snapshot)}
	return value
}

func cloneArtifactSnapshot(snapshot ArtifactSnapshot) ArtifactSnapshot {
	clone := snapshot
	clone.Metadata = make(map[string]string, len(snapshot.Metadata))
	for key, value := range snapshot.Metadata {
		clone.Metadata[key] = value
	}
	return clone
}

func listCall(path string, inventory GenerationInventory) scriptedArtifactReaderCall {
	return scriptedArtifactReaderCall{
		kind: artifactCallList, path: path,
		limit: artifactClassifierInventoryLimit, inventory: inventory,
	}
}

func inspectCall(snapshot ArtifactSnapshot, err error) scriptedArtifactReaderCall {
	return inspectResponseCall(snapshot.Path, snapshot.Generation, snapshot, err)
}

func inspectResponseCall(
	path string,
	generation int64,
	response ArtifactSnapshot,
	err error,
) scriptedArtifactReaderCall {
	return scriptedArtifactReaderCall{
		kind: artifactCallInspect, path: path, generation: generation,
		snapshot: response, err: err,
	}
}

func readCall(
	kind artifactReaderCallKind,
	snapshot ArtifactSnapshot,
	content []byte,
	limit int64,
	err error,
) scriptedArtifactReaderCall {
	return scriptedArtifactReaderCall{
		kind: kind,
		target: ArtifactTarget{
			Path: snapshot.Path, Generation: snapshot.Generation, Metageneration: snapshot.Metageneration,
		},
		limit: limit, content: content, err: err,
	}
}

func stableManifestCalls(fixture artifactContentTestFixture) []scriptedArtifactReaderCall {
	return []scriptedArtifactReaderCall{
		inspectCall(fixture.manifestSnapshot, nil),
		readCall(artifactCallReadManifest, fixture.manifestSnapshot, fixture.manifestBytes, MaxTelemetryManifestBytes, nil),
		inspectCall(fixture.manifestSnapshot, nil),
	}
}

func stableRawCalls(fixture artifactContentTestFixture) []scriptedArtifactReaderCall {
	return []scriptedArtifactReaderCall{
		inspectCall(fixture.rawSnapshot, nil),
		readCall(artifactCallReadRaw, fixture.rawSnapshot, fixture.rawCompressed, MaxTelemetryRawArtifactCompressedBytes, nil),
		inspectCall(fixture.rawSnapshot, nil),
	}
}

func forwardNoneCalls(fixture artifactContentTestFixture) []scriptedArtifactReaderCall {
	return []scriptedArtifactReaderCall{
		listCall(fixture.request.ExpectedManifestPath, completeInventory()),
		listCall(fixture.request.ExpectedRawPath, completeInventory()),
	}
}

func forwardRawOnlyCalls(fixture artifactContentTestFixture) []scriptedArtifactReaderCall {
	calls := []scriptedArtifactReaderCall{
		listCall(fixture.request.ExpectedManifestPath, completeInventory()),
		listCall(fixture.request.ExpectedRawPath, inventoryWithRegular(fixture.rawSnapshot)),
	}
	return append(calls, stableRawCalls(fixture)...)
}

func forwardCompleteCalls(fixture artifactContentTestFixture) []scriptedArtifactReaderCall {
	calls := []scriptedArtifactReaderCall{listCall(fixture.request.ExpectedManifestPath, inventoryWithRegular(fixture.manifestSnapshot))}
	calls = append(calls, stableManifestCalls(fixture)...)
	calls = append(calls, listCall(fixture.request.ExpectedRawPath, inventoryWithRegular(fixture.rawSnapshot)))
	return append(calls, stableRawCalls(fixture)...)
}

func forwardManifestOnlyCalls(fixture artifactContentTestFixture) []scriptedArtifactReaderCall {
	calls := []scriptedArtifactReaderCall{listCall(fixture.request.ExpectedManifestPath, inventoryWithRegular(fixture.manifestSnapshot))}
	calls = append(calls, stableManifestCalls(fixture)...)
	return append(calls, listCall(fixture.request.ExpectedRawPath, completeInventory()))
}

func acceptedCompleteCalls(fixture artifactContentTestFixture) []scriptedArtifactReaderCall {
	calls := []scriptedArtifactReaderCall{
		listCall(fixture.request.ExpectedManifestPath, inventoryWithRegular(fixture.manifestSnapshot)),
		listCall(fixture.request.ExpectedRawPath, inventoryWithRegular(fixture.rawSnapshot)),
	}
	calls = append(calls, stableManifestCalls(fixture)...)
	return append(calls, stableRawCalls(fixture)...)
}

func assertArtifactClassification(
	t *testing.T,
	result ArtifactClassificationResult,
	wantClass ArtifactClassification,
	wantReason ArtifactReasonCode,
) {
	t.Helper()
	if result.Classification != wantClass || result.ReasonCode != wantReason {
		t.Fatalf("classification = %q/%q, want %q/%q", result.Classification, result.ReasonCode, wantClass, wantReason)
	}
}

func assertPinnedPresence(
	t *testing.T,
	result ArtifactClassificationResult,
	wantManifest bool,
	wantRaw bool,
) {
	t.Helper()
	if (result.PinnedManifest != nil) != wantManifest || (result.PinnedRaw != nil) != wantRaw {
		t.Fatalf("pins = manifest:%#v raw:%#v, want presence %v/%v", result.PinnedManifest, result.PinnedRaw, wantManifest, wantRaw)
	}
}
