package gcsadapter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/cleanupattest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/firebaseadapter"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestCleanupAbsenceAuditorReturnsOnlyFreshCompleteEmptyEvidence(t *testing.T) {
	now := time.Now().UTC()
	request := cleanupAbsenceAuditRequestStub(now)
	backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{
		inventoryCall(request.ExpectedPath, emptyCleanupInventory()),
	}}
	auditor, verifier := cleanupAbsenceAuditorTestInstance(t, backend, now)
	evidence, err := auditor.AuditCleanupAbsence(
		context.Background(), firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, request,
	)
	observation, verifyErr := verifier.VerifyCleanupAbsenceEvidence(
		request, strings.Repeat("e", 64), evidence,
	)
	if err != nil || observation.RequestHash != request.RequestHash ||
		observation.Artifact != request.Artifact ||
		observation.Outcome != ingest.CleanupAuditConfirmedAbsent ||
		!observation.ObservedAt.Equal(now) || verifyErr != nil {
		t.Fatalf("AuditCleanupAbsence() = %#v, %v, verify=%v", evidence, err, verifyErr)
	}
	backend.assertDone(t)
}

func TestCleanupAbsenceAuditorRejectsPresenceIncompleteAndProviderErrors(t *testing.T) {
	now := time.Now().UTC()
	request := cleanupAbsenceAuditRequestStub(now)
	live := ingest.ArtifactLineage{
		Path: request.ExpectedPath, SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CRC32C: 123, Size: 1024, Generation: 1700000000000001, Metageneration: 1,
	}
	incomplete := emptyCleanupInventory()
	incomplete.Coverage = ingest.ArtifactInventoryCoverageIncomplete
	for _, test := range []struct {
		name      string
		inventory ingest.GenerationInventory
		provider  error
		want      error
	}{
		{name: "live generation", inventory: liveCleanupInventory(live), want: ingest.ErrCleanupExecutionGenerationDrift},
		{name: "soft-deleted generation", inventory: softDeletedCleanupInventory(live), want: ingest.ErrCleanupExecutionGenerationDrift},
		{name: "incomplete", inventory: incomplete, want: ingest.ErrCleanupExecutionUnavailable},
		{name: "permission", provider: ingest.ErrArtifactPermissionDenied, want: ingest.ErrArtifactPermissionDenied},
		{name: "timeout", provider: ingest.ErrArtifactProviderTimeout, want: ingest.ErrArtifactProviderTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &scriptedCleanupExecutionBackend{t: t, calls: []cleanupExecutionCall{{
				kind: cleanupExecutionCallInventory, path: request.ExpectedPath,
				inventory: test.inventory, err: test.provider,
			}}}
			auditor, _ := cleanupAbsenceAuditorTestInstance(t, backend, now)
			evidence, err := auditor.AuditCleanupAbsence(
				context.Background(), firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, request,
			)
			if !errors.Is(err, test.want) || evidence != (cleanupattest.Evidence{}) {
				t.Fatalf("AuditCleanupAbsence() = %#v, %v", evidence, err)
			}
			backend.assertDone(t)
		})
	}
}

func TestCleanupAbsenceAuditorRejectsAuthorizationBeforeProviderRead(t *testing.T) {
	now := time.Now().UTC()
	request := cleanupAbsenceAuditRequestStub(now)
	backend := &scriptedCleanupExecutionBackend{t: t}
	auditor, _ := cleanupAbsenceAuditorTestInstance(t, backend, now)
	auditor.validateAuthorization = firebaseadapter.ValidateCleanupAbsenceAuditAuthorization
	auditor.authorizationDeadline = firebaseadapter.CleanupAbsenceAuditAuthorizationDeadline
	evidence, err := auditor.AuditCleanupAbsence(
		context.Background(), firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, request,
	)
	if !errors.Is(err, ingest.ErrCleanupExecutionUnauthorized) ||
		evidence != (cleanupattest.Evidence{}) {
		t.Fatalf("zero grant audit = %#v, %v", evidence, err)
	}
	backend.assertDone(t)
}

func TestCleanupAbsenceAuditorHonorsCancellationAndAuthorizationDeadline(t *testing.T) {
	now := time.Now().UTC()
	request := cleanupAbsenceAuditRequestStub(now)

	t.Run("caller cancellation before read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		backend := &scriptedCleanupExecutionBackend{t: t}
		auditor, _ := cleanupAbsenceAuditorTestInstance(t, backend, now)
		evidence, err := auditor.AuditCleanupAbsence(
			ctx, firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, request,
		)
		if !errors.Is(err, context.Canceled) || evidence != (cleanupattest.Evidence{}) {
			t.Fatalf("cancelled audit = %#v, %v", evidence, err)
		}
		backend.assertDone(t)
	})

	t.Run("expired authorization before read", func(t *testing.T) {
		backend := &scriptedCleanupExecutionBackend{t: t}
		auditor, _ := cleanupAbsenceAuditorTestInstance(t, backend, now)
		auditor.authorizationDeadline = func(
			firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
			ingest.CleanupAbsenceAuditRequest,
		) (time.Time, error) {
			return time.Now().UTC().Add(-time.Second), nil
		}
		evidence, err := auditor.AuditCleanupAbsence(
			context.Background(), firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, request,
		)
		if !errors.Is(err, firebaseadapter.ErrCleanupAbsenceAuditAuthorizationExpired) ||
			evidence != (cleanupattest.Evidence{}) {
			t.Fatalf("expired audit = %#v, %v", evidence, err)
		}
		backend.assertDone(t)
	})

	t.Run("deadline while provider read is pending", func(t *testing.T) {
		reader := newBlockingCleanupAbsenceInventoryReader()
		auditor, _ := cleanupAbsenceAuditorTestInstance(
			t, &scriptedCleanupExecutionBackend{t: t}, now,
		)
		auditor.reader = reader
		auditor.authorizationDeadline = func(
			firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
			ingest.CleanupAbsenceAuditRequest,
		) (time.Time, error) {
			return time.Now().UTC().Add(20 * time.Millisecond), nil
		}
		evidence, err := auditor.AuditCleanupAbsence(
			context.Background(), firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, request,
		)
		if !errors.Is(err, firebaseadapter.ErrCleanupAbsenceAuditAuthorizationExpired) ||
			evidence != (cleanupattest.Evidence{}) {
			t.Fatalf("provider deadline audit = %#v, %v", evidence, err)
		}
	})

	t.Run("caller cancellation while provider read is pending", func(t *testing.T) {
		reader := newBlockingCleanupAbsenceInventoryReader()
		auditor, _ := cleanupAbsenceAuditorTestInstance(
			t, &scriptedCleanupExecutionBackend{t: t}, now,
		)
		auditor.reader = reader
		ctx, cancel := context.WithCancel(context.Background())
		type auditResult struct {
			evidence cleanupattest.Evidence
			err      error
		}
		result := make(chan auditResult, 1)
		go func() {
			evidence, err := auditor.AuditCleanupAbsence(
				ctx, firebaseadapter.CleanupAbsenceAuditAuthorizationGrant{}, request,
			)
			result <- auditResult{evidence: evidence, err: err}
		}()
		<-reader.started
		cancel()
		completed := <-result
		if !errors.Is(completed.err, context.Canceled) ||
			completed.evidence != (cleanupattest.Evidence{}) {
			t.Fatalf("provider cancellation audit = %#v, %v", completed.evidence, completed.err)
		}
	})
}

type blockingCleanupAbsenceInventoryReader struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingCleanupAbsenceInventoryReader() *blockingCleanupAbsenceInventoryReader {
	return &blockingCleanupAbsenceInventoryReader{started: make(chan struct{})}
}

func (r *blockingCleanupAbsenceInventoryReader) ListExactPathGenerations(
	ctx context.Context,
	_ string,
	_ int,
) (ingest.GenerationInventory, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return ingest.GenerationInventory{}, ctx.Err()
}

func cleanupAbsenceAuditorTestInstance(
	t *testing.T,
	backend *scriptedCleanupExecutionBackend,
	now time.Time,
) (*CleanupAbsenceAuditor, cleanupattest.Verifier) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() = %v", err)
	}
	verifier, err := cleanupattest.NewVerifier(publicKey)
	if err != nil {
		t.Fatalf("cleanupattest.NewVerifier() = %v", err)
	}
	auditor := &CleanupAbsenceAuditor{
		reader: backend,
		now:    func() time.Time { return now },
		validateAuthorization: func(
			firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
			ingest.CleanupAbsenceAuditRequest,
			time.Time,
		) error {
			return nil
		},
		authorizationDeadline: func(
			firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
			ingest.CleanupAbsenceAuditRequest,
		) (time.Time, error) {
			return now.Add(time.Minute), nil
		},
		authorizationBinding: func(
			firebaseadapter.CleanupAbsenceAuditAuthorizationGrant,
			ingest.CleanupAbsenceAuditRequest,
		) (string, error) {
			return strings.Repeat("e", 64), nil
		},
	}
	copy(auditor.evidencePrivateKey[:], privateKey)
	return auditor, verifier
}

func cleanupAbsenceAuditRequestStub(now time.Time) ingest.CleanupAbsenceAuditRequest {
	request := ingest.CleanupAbsenceAuditRequest{
		Query: ingest.CleanupExecutionQuery{
			TenantID:       "11111111-1111-4111-8111-111111111111",
			ReservationKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AttemptID:      "77777777-7777-4777-8777-777777777777",
		},
		ExpectedTargetHash:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ExpectedPlanHash:        "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ExpectedReceiptRevision: 5,
		ExpectedFence: ingest.LeaseFence{
			OwnerID: "77777777-7777-4777-8777-777777777777", Token: 4, ExpiresAt: now.Add(2 * time.Minute),
		},
		ExpectedLedgerRevision: 3,
		NextPhase:              ingest.CleanupExecutionPhaseRawAbsenceConfirmed,
		Artifact:               ingest.CleanupAbsenceAuditRaw,
		ExpectedPath:           "telemetry/tenant/raw.json.gz",
	}
	request.RequestHash = cleanupAbsenceAuditRequestTestHash(request)
	return request
}

func cleanupAbsenceAuditRequestTestHash(request ingest.CleanupAbsenceAuditRequest) string {
	encoder := &cleanupAbsenceAuditTestEncoder{digest: sha256.New()}
	encoder.addString("cleanup-absence-audit-request@1")
	encoder.addString(request.Query.TenantID)
	encoder.addString(request.Query.ReservationKey)
	encoder.addString(request.Query.AttemptID)
	encoder.addString(request.ExpectedTargetHash)
	encoder.addString(request.ExpectedPlanHash)
	encoder.addInt64(request.ExpectedReceiptRevision)
	encoder.addString(request.ExpectedFence.OwnerID)
	encoder.addInt64(request.ExpectedFence.Token)
	encoder.addTime(request.ExpectedFence.ExpiresAt)
	encoder.addInt64(request.ExpectedLedgerRevision)
	encoder.addString(string(request.NextPhase))
	encoder.addString(string(request.Artifact))
	encoder.addString(request.ExpectedPath)
	return hex.EncodeToString(encoder.digest.Sum(nil))
}

type cleanupAbsenceAuditTestEncoder struct {
	digest hash.Hash
}

func (e *cleanupAbsenceAuditTestEncoder) addString(value string) {
	e.addBytes([]byte(value))
}

func (e *cleanupAbsenceAuditTestEncoder) addBytes(value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = e.digest.Write(length[:])
	_, _ = e.digest.Write(value)
}

func (e *cleanupAbsenceAuditTestEncoder) addInt64(value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	e.addBytes(encoded[:])
}

func (e *cleanupAbsenceAuditTestEncoder) addTime(value time.Time) {
	e.addString(value.UTC().Format(time.RFC3339Nano))
}
