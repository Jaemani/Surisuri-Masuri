package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	cleanupTargetTenantID       = "11111111-1111-4111-8111-111111111111"
	cleanupTargetDeviceID       = "22222222-2222-4222-8222-222222222222"
	cleanupTargetTripID         = "33333333-3333-4333-8333-333333333333"
	cleanupTargetInstallationID = "44444444-4444-4444-8444-444444444444"
	cleanupTargetClientBatchID  = "55555555-5555-4555-8555-555555555555"
	cleanupTargetConsentID      = "66666666-6666-4666-8666-666666666666"
	cleanupTargetAttemptID      = "77777777-7777-4777-8777-777777777777"
	cleanupTargetReceiptID      = "01982015-4400-7000-8000-000000000001"
)

func TestCleanupArtifactReadCapabilitySeparatesForwardAndCleanupFences(t *testing.T) {
	now, snapshot, lease, _ := cleanupTargetFixture(t)
	store := &cleanupAuthorizationStoreStub{snapshot: snapshot}
	authorizer := mustCleanupAuthorizer(t, store, now)

	request, cleanupGrant, err := authorizer.AuthorizeArtifactRead(
		context.Background(),
		cleanupTargetTenantID,
		snapshot.Receipt.ReservationKey,
		lease,
	)
	if err != nil {
		t.Fatalf("AuthorizeArtifactRead() = %v", err)
	}
	if request.Purpose != ArtifactReadCleanupDryRun || request.ForwardFence != nil ||
		request.CleanupFence == nil || cleanupGrant.issuer != artifactReadGrantIssuerCleanupDryRun {
		t.Fatalf("cleanup request/grant shape = %#v / %#v", request, cleanupGrant)
	}
	if err := ValidateArtifactReadAuthorization(cleanupGrant, request, now.Add(time.Second)); err != nil {
		t.Fatalf("ValidateArtifactReadAuthorization(cleanup) = %v", err)
	}

	cleanupWithForwardFence := cloneArtifactClassificationRequest(request)
	cleanupWithForwardFence.ForwardFence = &LeaseFence{
		OwnerID:   lease.Lease.Fence.OwnerID,
		Token:     lease.Lease.Fence.Token,
		ExpiresAt: lease.Lease.Fence.ExpiresAt,
	}
	if !errors.Is(
		ValidateArtifactClassificationRequest(cleanupWithForwardFence),
		ErrInvalidArtifactClassificationRequest,
	) {
		t.Fatal("cleanup purpose accepted a forward fence")
	}

	forwardRequest := cloneArtifactClassificationRequest(request)
	forwardRequest.Purpose = ArtifactReadForwardRecovery
	forwardRequest.ReceiptState = ReceiptReserved
	forwardRequest.ForwardFence = forwardRequest.CleanupFence
	forwardRequest.CleanupFence = nil
	forwardGrant, err := mintArtifactReadAuthorizationGrant(
		artifactReadGrantIssuerForwardRecovery,
		"forward-test@1",
		forwardRequest,
		now,
		now.Add(CleanupArtifactReadGrantTTL),
	)
	if err != nil {
		t.Fatalf("mint forward grant = %v", err)
	}
	if !errors.Is(
		ValidateArtifactReadAuthorization(cleanupGrant, forwardRequest, now.Add(time.Second)),
		ErrInvalidArtifactReadAuthorization,
	) {
		t.Fatal("cleanup issuer authorized a forward request")
	}
	if !errors.Is(
		ValidateArtifactReadAuthorization(forwardGrant, request, now.Add(time.Second)),
		ErrInvalidArtifactReadAuthorization,
	) {
		t.Fatal("forward issuer authorized a cleanup request")
	}

	tampered := cloneArtifactClassificationRequest(request)
	tampered.CleanupFence.Token++
	if !errors.Is(
		ValidateArtifactReadAuthorization(cleanupGrant, tampered, now.Add(time.Second)),
		ErrInvalidArtifactReadAuthorization,
	) {
		t.Fatal("cleanup grant accepted a tampered cleanup fence")
	}
}

func TestCleanupArtifactClassifierUsesCleanupPurposeForReadOnlyDiscovery(t *testing.T) {
	now, snapshot, lease, _ := cleanupTargetFixture(t)
	authorizer := mustCleanupAuthorizer(t, &cleanupAuthorizationStoreStub{snapshot: snapshot}, now)
	request, grant, err := authorizer.AuthorizeArtifactRead(
		context.Background(), cleanupTargetTenantID, snapshot.Receipt.ReservationKey, lease,
	)
	if err != nil {
		t.Fatalf("AuthorizeArtifactRead() = %v", err)
	}
	reader := newScriptedArtifactReader(t,
		scriptedArtifactReaderCall{
			kind: artifactCallList, path: request.ExpectedManifestPath,
			limit: artifactClassifierInventoryLimit, inventory: completeInventory(),
		},
		scriptedArtifactReaderCall{
			kind: artifactCallList, path: request.ExpectedRawPath,
			limit: artifactClassifierInventoryLimit, inventory: completeInventory(),
		},
	)
	classifier := mustArtifactClassifier(
		t, reader, newTelemetryArtifactContentValidator(), func() time.Time { return now.Add(time.Second) },
	)
	result, err := classifier.Classify(context.Background(), grant, request)
	if err != nil {
		t.Fatalf("Classify() = %v", err)
	}
	assertArtifactClassification(t, result, ArtifactClassificationNone, ArtifactReasonNoCandidates)
	if result.RetentionPhase != ArtifactRetentionBeforeExpiry {
		t.Fatalf("retention phase = %q, want %q", result.RetentionPhase, ArtifactRetentionBeforeExpiry)
	}
	reader.assertDone(t)
}

func TestCleanupTargetDispositionCreatesBoundedImmutablePlans(t *testing.T) {
	tests := []struct {
		name           string
		classification ArtifactClassification
		wantDecision   CleanupTargetDecision
		wantStatus     CleanupTargetStatus
		wantRaw        bool
		wantManifest   bool
	}{
		{
			name: "none is verified empty", classification: ArtifactClassificationNone,
			wantDecision: CleanupTargetVerifiedEmpty, wantStatus: CleanupTargetStatusPlanned,
		},
		{
			name: "raw only is delete candidate", classification: ArtifactClassificationValidRawOnly,
			wantDecision: CleanupTargetDeleteCandidate, wantStatus: CleanupTargetStatusPlanned,
			wantRaw: true,
		},
		{
			name: "complete is delete candidate", classification: ArtifactClassificationValidComplete,
			wantDecision: CleanupTargetDeleteCandidate, wantStatus: CleanupTargetStatusPlanned,
			wantRaw: true, wantManifest: true,
		},
		{
			name: "generation drift is hold", classification: ArtifactClassificationGenerationDrift,
			wantDecision: CleanupTargetHold, wantStatus: CleanupTargetStatusHold,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now, snapshot, lease, attempt := cleanupTargetFixture(t)
			store := &cleanupAuthorizationStoreStub{snapshot: snapshot}
			authorizer := mustCleanupAuthorizer(t, store, now)
			request, _, err := authorizer.AuthorizeArtifactRead(
				context.Background(), cleanupTargetTenantID, snapshot.Receipt.ReservationKey, lease,
			)
			if err != nil {
				t.Fatalf("AuthorizeArtifactRead() = %v", err)
			}
			result := cleanupClassificationResultFixture(request, test.classification, now.Add(-time.Second))

			command, grant, err := authorizer.AuthorizeTargetCreation(
				context.Background(),
				cleanupTargetTenantID,
				snapshot.Receipt.ReservationKey,
				lease,
				attempt,
				request,
				result,
			)
			if err != nil {
				t.Fatalf("AuthorizeTargetCreation() = %v", err)
			}
			if command.Decision != test.wantDecision || command.Status != test.wantStatus ||
				(command.Raw != nil) != test.wantRaw || (command.Manifest != nil) != test.wantManifest {
				t.Fatalf("command disposition = %#v", command)
			}
			if command.CleanupID != attempt.ID || command.AttemptID != attempt.ID ||
				command.FencingToken != lease.Lease.Fence.Token ||
				command.ReceiptRevision != lease.ReceiptRevision {
				t.Fatalf("command control binding = %#v", command)
			}
			if err := ValidateCleanupTargetAuthorization(grant, command, now.Add(time.Second)); err != nil {
				t.Fatalf("ValidateCleanupTargetAuthorization() = %v", err)
			}
			if _, err := CleanupTargetHash(command); err != nil {
				t.Fatalf("CleanupTargetHash() = %v", err)
			}
		})
	}
}

func TestCleanupTargetUnavailableDoesNotMintTargetCapability(t *testing.T) {
	now, snapshot, lease, attempt := cleanupTargetFixture(t)
	store := &cleanupAuthorizationStoreStub{snapshot: snapshot}
	authorizer := mustCleanupAuthorizer(t, store, now)
	request, _, err := authorizer.AuthorizeArtifactRead(
		context.Background(), cleanupTargetTenantID, snapshot.Receipt.ReservationKey, lease,
	)
	if err != nil {
		t.Fatalf("AuthorizeArtifactRead() = %v", err)
	}
	store.calls = 0
	result := cleanupClassificationResultFixture(request, ArtifactClassificationUnavailable, now.Add(-time.Second))

	command, grant, err := authorizer.AuthorizeTargetCreation(
		context.Background(),
		cleanupTargetTenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		attempt,
		request,
		result,
	)
	if !errors.Is(err, ErrCleanupTargetUnavailable) {
		t.Fatalf("AuthorizeTargetCreation() = %v, want %v", err, ErrCleanupTargetUnavailable)
	}
	if command != (CleanupTargetCommand{}) || grant != (CleanupTargetAuthorizationGrant{}) {
		t.Fatal("unavailable classification minted target material")
	}
	if store.calls != 0 {
		t.Fatalf("current-state reads = %d, want 0 before unavailable disposition", store.calls)
	}
}

func TestCleanupTargetRejectsTamperedClassificationEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ArtifactClassificationResult)
	}{
		{
			name: "request binding",
			mutate: func(result *ArtifactClassificationResult) {
				result.requestBindingHash[0] ^= 0xff
			},
		},
		{
			name: "pinned digest",
			mutate: func(result *ArtifactClassificationResult) {
				result.PinnedRaw.SHA256 = "invalid"
			},
		},
		{
			name: "shape valid pinned generation and digest substitution",
			mutate: func(result *ArtifactClassificationResult) {
				result.PinnedRaw.Generation++
				result.PinnedRaw.SHA256 = strings.Repeat("d", 64)
			},
		},
		{
			name: "shape valid classification substitution",
			mutate: func(result *ArtifactClassificationResult) {
				result.Classification = ArtifactClassificationValidRawOnly
				result.ReasonCode = ArtifactReasonRawValidManifestAbsent
				result.ManifestInventory.NonSoftDeletedCount = 0
				result.PinnedManifest = nil
			},
		},
		{
			name: "shape valid observation time substitution",
			mutate: func(result *ArtifactClassificationResult) {
				result.ObservedAt = result.ObservedAt.Add(time.Second)
			},
		},
		{
			name: "inventory cardinality",
			mutate: func(result *ArtifactClassificationResult) {
				result.RawInventory.NonSoftDeletedCount = 0
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now, snapshot, lease, attempt := cleanupTargetFixture(t)
			store := &cleanupAuthorizationStoreStub{snapshot: snapshot}
			authorizer := mustCleanupAuthorizer(t, store, now)
			request, _, err := authorizer.AuthorizeArtifactRead(
				context.Background(), cleanupTargetTenantID, snapshot.Receipt.ReservationKey, lease,
			)
			if err != nil {
				t.Fatalf("AuthorizeArtifactRead() = %v", err)
			}
			result := cleanupClassificationResultFixture(
				request, ArtifactClassificationValidComplete, now.Add(-time.Second),
			)
			test.mutate(&result)
			store.calls = 0

			command, grant, err := authorizer.AuthorizeTargetCreation(
				context.Background(),
				cleanupTargetTenantID,
				snapshot.Receipt.ReservationKey,
				lease,
				attempt,
				request,
				result,
			)
			if !errors.Is(err, ErrInvalidCleanupTarget) {
				t.Fatalf("AuthorizeTargetCreation() = %v, want %v", err, ErrInvalidCleanupTarget)
			}
			if command != (CleanupTargetCommand{}) || grant != (CleanupTargetAuthorizationGrant{}) {
				t.Fatal("tampered evidence minted target material")
			}
			if store.calls != 0 {
				t.Fatalf("current-state reads = %d, want 0 before invalid evidence", store.calls)
			}
		})
	}
}

func TestCurrentCleanupTargetRejectsRevisionFenceAndAttemptDrift(t *testing.T) {
	now, snapshot, lease, attempt := cleanupTargetFixture(t)
	store := &cleanupAuthorizationStoreStub{snapshot: snapshot}
	authorizer := mustCleanupAuthorizer(t, store, now)
	request, _, err := authorizer.AuthorizeArtifactRead(
		context.Background(), cleanupTargetTenantID, snapshot.Receipt.ReservationKey, lease,
	)
	if err != nil {
		t.Fatalf("AuthorizeArtifactRead() = %v", err)
	}
	result := cleanupClassificationResultFixture(
		request, ArtifactClassificationValidComplete, now.Add(-time.Second),
	)
	command, grant, err := authorizer.AuthorizeTargetCreation(
		context.Background(),
		cleanupTargetTenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		attempt,
		request,
		result,
	)
	if err != nil {
		t.Fatalf("AuthorizeTargetCreation() = %v", err)
	}
	observedAt := now.Add(time.Second)
	if err := ValidateCurrentCleanupTarget(grant, command, snapshot, observedAt); err != nil {
		t.Fatalf("ValidateCurrentCleanupTarget(valid) = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*CurrentCleanupSnapshot)
	}{
		{
			name: "receipt revision",
			mutate: func(current *CurrentCleanupSnapshot) {
				current.Receipt.Revision++
			},
		},
		{
			name: "receipt fence",
			mutate: func(current *CurrentCleanupSnapshot) {
				current.Receipt.FencingToken++
			},
		},
		{
			name: "attempt fence",
			mutate: func(current *CurrentCleanupSnapshot) {
				current.Attempt.FencingToken++
			},
		},
		{
			name: "attempt identity",
			mutate: func(current *CurrentCleanupSnapshot) {
				current.Attempt.AttemptID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := snapshot
			test.mutate(&current)
			if !errors.Is(
				ValidateCurrentCleanupTarget(grant, command, current, observedAt),
				ErrCleanupArtifactUnauthorized,
			) {
				t.Fatal("current-state drift was accepted")
			}
		})
	}
}

type cleanupAuthorizationStoreStub struct {
	snapshot CurrentCleanupSnapshot
	calls    int
}

func (s *cleanupAuthorizationStoreStub) LoadCurrentCleanup(
	_ context.Context,
	_ CleanupArtifactAuthorizationQuery,
) (CurrentCleanupSnapshot, error) {
	s.calls++
	return s.snapshot, nil
}

func mustCleanupAuthorizer(
	t *testing.T,
	store CleanupArtifactAuthorizationStore,
	now time.Time,
) *SystemCleanupAuthorizer {
	t.Helper()
	authorizer, err := NewSystemCleanupAuthorizer(store, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewSystemCleanupAuthorizer() = %v", err)
	}
	return authorizer
}

func cleanupTargetFixture(
	t *testing.T,
) (time.Time, CurrentCleanupSnapshot, CleanupLeaseGrant, CleanupAttemptProposal) {
	t.Helper()
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	createdAt := now.Add(-30 * time.Minute)
	transitionedAt := now.Add(-13 * time.Minute)
	quiescenceUntil := transitionedAt.Add(DefaultCleanupLateWriteGrace)
	leaseAcquiredAt := quiescenceUntil
	leaseHeartbeatAt := now.Add(-time.Minute)
	leaseExpiresAt := now.Add(2 * time.Minute)
	reservationKey := DeriveReservationKey(
		"telemetry-batch.v2",
		cleanupTargetTenantID,
		cleanupTargetInstallationID,
		cleanupTargetClientBatchID,
	)
	receipt := Receipt{
		ReservationKey:         reservationKey,
		ClientBatchKey:         DeriveClientBatchKey(cleanupTargetTenantID, cleanupTargetClientBatchID),
		ReceiptID:              cleanupTargetReceiptID,
		TenantID:               cleanupTargetTenantID,
		BatchID:                cleanupTargetReceiptID,
		DeviceID:               cleanupTargetDeviceID,
		TripID:                 cleanupTargetTripID,
		InstallationID:         cleanupTargetInstallationID,
		ConsentRevisionID:      cleanupTargetConsentID,
		ClientBatchID:          cleanupTargetClientBatchID,
		PayloadSchemaVersion:   "telemetry-batch.v2",
		BodyHash:               strings.Repeat("a", 64),
		ExpectedSampleCount:    2,
		FirstCapturedAt:        createdAt.Add(-20 * time.Minute),
		LastCapturedAt:         createdAt.Add(-time.Minute),
		ValidatorVersion:       TelemetryValidatorVersion,
		State:                  ReceiptCleanupPending,
		FencingToken:           4,
		LeaseOwnerID:           cleanupTargetAttemptID,
		LeaseOwnerKind:         LeaseOwnerCleanup,
		LeaseAcquiredAt:        leaseAcquiredAt,
		LeaseHeartbeatAt:       leaseHeartbeatAt,
		LeaseExpiresAt:         leaseExpiresAt,
		RecoveryAttemptCount:   2,
		CleanupTransitionedAt:  transitionedAt,
		CleanupQuiescenceUntil: quiescenceUntil,
		CleanupMode:            CleanupModeReservationExpiry,
		CleanupOriginStatus:    ReceiptReserved,
		CleanupPolicyVersion:   CleanupTransitionPolicyV1,
		Revision:               5,
		CreatedAt:              createdAt,
		UpdatedAt:              leaseHeartbeatAt,
		ReservationDeadline:    createdAt.Add(ReservationProcessingWindow),
		ArtifactExpiresAt:      createdAt.Add(TelemetryArtifactRetention),
		ReceiptRetentionFloor:  createdAt.Add(ReceiptControlRetention),
	}
	lease := CleanupLeaseGrant{
		Lease: LeaseGrant{
			Fence: LeaseFence{
				OwnerID: cleanupTargetAttemptID, Token: receipt.FencingToken, ExpiresAt: leaseExpiresAt,
			},
			OwnerKind: LeaseOwnerCleanup, AcquiredAt: leaseAcquiredAt, HeartbeatAt: leaseHeartbeatAt,
		},
		ReceiptRevision: receipt.Revision, Mode: receipt.CleanupMode,
		OriginStatus: receipt.CleanupOriginStatus, PolicyVersion: receipt.CleanupPolicyVersion,
		TransitionedAt: receipt.CleanupTransitionedAt, QuiescenceUntil: receipt.CleanupQuiescenceUntil,
	}
	attempt := CleanupAttemptProposal{ID: cleanupTargetAttemptID, WorkerVersion: CleanupWorkerVersion}
	snapshot := CurrentCleanupSnapshot{
		Receipt: receipt,
		Attempt: CurrentCleanupAttempt{
			AttemptID: cleanupTargetAttemptID, TenantID: cleanupTargetTenantID,
			ReceiptID: cleanupTargetReceiptID, OwnerKind: LeaseOwnerCleanup,
			FencingToken: receipt.FencingToken, WorkerVersion: CleanupWorkerVersion,
			Status: RecoveryAttemptStarted, StartedAt: leaseAcquiredAt,
		},
		ReadTime: now.Add(-time.Second),
	}
	if ValidateCleanupLeaseGrant(lease) != nil {
		t.Fatal("cleanupTargetFixture constructed an invalid cleanup lease")
	}
	return now, snapshot, lease, attempt
}

func cleanupClassificationResultFixture(
	request ArtifactClassificationRequest,
	classification ArtifactClassification,
	observedAt time.Time,
) ArtifactClassificationResult {
	result := ArtifactClassificationResult{
		Classification:     classification,
		RetentionPhase:     artifactRetentionPhaseAt(request, observedAt),
		ValidatorVersion:   request.ValidatorVersion,
		ObservedAt:         observedAt,
		requestBindingHash: canonicalArtifactClassificationRequestBinding(request),
	}
	complete := func(count int) ArtifactInventorySummary {
		return ArtifactInventorySummary{
			Performed: true, NonSoftDeletedCount: count,
			Coverage: ArtifactInventoryCoverageComplete,
		}
	}
	raw := &ArtifactPinnedLineage{
		SHA256: strings.Repeat("b", 64), CRC32C: 0x12345678, Size: 4096,
		Generation: 1700000000000001, Metageneration: 1,
	}
	manifest := &ArtifactPinnedLineage{
		SHA256: strings.Repeat("c", 64), CRC32C: 0x87654321, Size: 1024,
		Generation: 1700000000000002, Metageneration: 1,
	}
	switch classification {
	case ArtifactClassificationNone:
		result.ReasonCode = ArtifactReasonNoCandidates
		result.RawInventory = complete(0)
		result.ManifestInventory = complete(0)
	case ArtifactClassificationValidRawOnly:
		result.ReasonCode = ArtifactReasonRawValidManifestAbsent
		result.RawInventory = complete(1)
		result.ManifestInventory = complete(0)
		result.PinnedRaw = raw
	case ArtifactClassificationValidComplete:
		result.ReasonCode = ArtifactReasonManifestAndReferencedRawValid
		result.RawInventory = complete(1)
		result.ManifestInventory = complete(1)
		result.PinnedRaw = raw
		result.PinnedManifest = manifest
	case ArtifactClassificationGenerationDrift:
		result.ReasonCode = ArtifactReasonMultipleRawGenerations
		result.RawInventory = complete(2)
		result.ManifestInventory = complete(0)
	case ArtifactClassificationUnavailable:
		result.ReasonCode = ArtifactReasonProviderTimeout
		result.RawInventory = ArtifactInventorySummary{Coverage: ArtifactInventoryCoverageUnknown}
		result.ManifestInventory = ArtifactInventorySummary{Coverage: ArtifactInventoryCoverageUnknown}
	}
	return sealArtifactClassificationResult(request, result)
}
