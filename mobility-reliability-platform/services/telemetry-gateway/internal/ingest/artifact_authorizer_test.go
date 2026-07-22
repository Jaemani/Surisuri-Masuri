package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	artifactAuthTenantID       = "11111111-1111-4111-8111-111111111111"
	artifactAuthDeviceID       = "22222222-2222-4222-8222-222222222222"
	artifactAuthTripID         = "33333333-3333-4333-8333-333333333333"
	artifactAuthInstallationID = "44444444-4444-4444-8444-444444444444"
	artifactAuthClientBatchID  = "55555555-5555-4555-8555-555555555555"
	artifactAuthConsentID      = "66666666-6666-4666-8666-666666666666"
	artifactAuthOwnerID        = "77777777-7777-4777-8777-777777777777"
	artifactAuthPersonID       = "88888888-8888-4888-8888-888888888888"
	artifactAuthAssignmentID   = "99999999-9999-4999-8999-999999999999"
	artifactAuthSessionID      = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	artifactAuthReceiptID      = "01982015-4400-7000-8000-000000000001"
)

func TestSystemRecoveryAuthorizerBuildsAuthoritativeForwardRequestAndGrant(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	if err := validateCurrentForwardRecoverySnapshotShape(snapshot); err != nil {
		t.Fatalf("valid snapshot shape = %v", err)
	}
	if _, err := forwardRecoveryClassificationRequest(snapshot.Receipt); err != nil {
		t.Fatalf("valid receipt conversion = %v", err)
	}
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)

	request, grant, err := authorizer.Authorize(
		context.Background(),
		artifactAuthTenantID,
		snapshot.Receipt.ReservationKey,
		artifactAuthLease(snapshot),
	)
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if store.calls != 1 || store.query != (ForwardRecoveryAuthorizationQuery{
		TenantID:       artifactAuthTenantID,
		ReservationKey: snapshot.Receipt.ReservationKey,
	}) {
		t.Fatalf("store calls/query = %d/%#v", store.calls, store.query)
	}
	if request.Purpose != ArtifactReadForwardRecovery ||
		request.ReceiptID != snapshot.Receipt.ReceiptID ||
		request.ReceiptRevision != snapshot.Receipt.Revision ||
		request.TenantID != snapshot.Receipt.TenantID ||
		request.DeviceID != snapshot.Receipt.DeviceID ||
		request.TripID != snapshot.Receipt.TripID ||
		request.InstallationID != snapshot.Receipt.InstallationID ||
		request.ClientBatchID != snapshot.Receipt.ClientBatchID ||
		request.ConsentRevisionID != snapshot.Receipt.ConsentRevisionID ||
		request.BodyHash != snapshot.Receipt.BodyHash ||
		request.ExpectedSampleCount != snapshot.Receipt.ExpectedSampleCount ||
		request.ForwardFence == nil ||
		request.ForwardFence.OwnerID != snapshot.Receipt.LeaseOwnerID ||
		request.ForwardFence.Token != snapshot.Receipt.FencingToken ||
		!request.ForwardFence.ExpiresAt.Equal(snapshot.Receipt.LeaseExpiresAt) {
		t.Fatalf("authoritative request = %#v", request)
	}
	if request.ExpectedRawPath == "" || request.ExpectedManifestPath == "" {
		t.Fatalf("derived paths are empty: %#v", request)
	}
	if err := ValidateArtifactClassificationRequest(request); err != nil {
		t.Fatalf("ValidateArtifactClassificationRequest() = %v", err)
	}
	if grant.issuer != artifactReadGrantIssuerForwardRecovery ||
		grant.policyVersion != ForwardRecoveryAuthorizationPolicyVersion ||
		!grant.checkedAt.Equal(now) ||
		!grant.expiresAt.Equal(now.Add(ForwardRecoveryArtifactReadGrantTTL)) {
		t.Fatalf("grant = %#v", grant)
	}
	if err := ValidateArtifactReadAuthorization(grant, request, now.Add(time.Second)); err != nil {
		t.Fatalf("ValidateArtifactReadAuthorization() = %v", err)
	}

	accepted := artifactClassificationRequestFixture(t, ArtifactReadAcceptedIntegrityAudit)
	if err := ValidateArtifactReadAuthorization(grant, accepted, now.Add(time.Second)); !errors.Is(err, ErrInvalidArtifactReadAuthorization) {
		t.Fatalf("forward grant accepted an integrity request: %v", err)
	}
}

func TestSystemRecoveryAuthorizerRejectsInvalidInputBeforeStore(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	validLease := artifactAuthLease(snapshot)
	tests := []struct {
		name           string
		ctx            context.Context
		tenantID       string
		reservationKey string
		lease          LeaseGrant
		want           error
	}{
		{
			name:           "invalid tenant",
			ctx:            context.Background(),
			tenantID:       "tenant",
			reservationKey: snapshot.Receipt.ReservationKey,
			lease:          validLease,
			want:           ErrInvalidArtifactReadAuthorization,
		},
		{
			name:           "invalid reservation key",
			ctx:            context.Background(),
			tenantID:       artifactAuthTenantID,
			reservationKey: strings.Repeat("z", 64),
			lease:          validLease,
			want:           ErrInvalidArtifactReadAuthorization,
		},
		{
			name:           "request owner",
			ctx:            context.Background(),
			tenantID:       artifactAuthTenantID,
			reservationKey: snapshot.Receipt.ReservationKey,
			lease:          artifactAuthLeaseWithKind(snapshot, LeaseOwnerRequest),
			want:           ErrInvalidArtifactReadAuthorization,
		},
		{
			name:           "invalid fence",
			ctx:            context.Background(),
			tenantID:       artifactAuthTenantID,
			reservationKey: snapshot.Receipt.ReservationKey,
			lease: func() LeaseGrant {
				lease := validLease
				lease.Fence.Token = 0
				return lease
			}(),
			want: ErrInvalidArtifactReadAuthorization,
		},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name           string
		ctx            context.Context
		tenantID       string
		reservationKey string
		lease          LeaseGrant
		want           error
	}{
		name:           "pre-cancelled context",
		ctx:            cancelled,
		tenantID:       artifactAuthTenantID,
		reservationKey: snapshot.Receipt.ReservationKey,
		lease:          validLease,
		want:           context.Canceled,
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &artifactAuthStoreStub{snapshot: snapshot}
			authorizer := artifactAuthAuthorizer(t, store, now)
			_, _, err := authorizer.Authorize(
				test.ctx,
				test.tenantID,
				test.reservationKey,
				test.lease,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Authorize() error = %v, want %v", err, test.want)
			}
			if store.calls != 0 {
				t.Fatalf("store calls = %d, want 0", store.calls)
			}
		})
	}

	if _, err := NewSystemRecoveryAuthorizer(nil, nil); err == nil {
		t.Fatal("NewSystemRecoveryAuthorizer(nil) error = nil")
	}
	var nilAuthorizer *SystemRecoveryAuthorizer
	if _, _, err := nilAuthorizer.Authorize(
		context.Background(),
		artifactAuthTenantID,
		snapshot.Receipt.ReservationKey,
		validLease,
	); !errors.Is(err, ErrForwardRecoveryAuthorizationUnavailable) {
		t.Fatalf("nil Authorize() error = %v", err)
	}
}

func TestSystemRecoveryAuthorizerFailsClosedForReceiptAndRelationshipDrift(t *testing.T) {
	now := artifactAuthNow()
	tests := []struct {
		name   string
		mutate func(*CurrentForwardRecoverySnapshot, *LeaseGrant)
		want   error
	}{
		{
			name: "receipt state",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Receipt.State = ReceiptStored
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "rejected receipt state",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Receipt.State = ReceiptRejected
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "cleanup receipt state",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Receipt.State = ReceiptCleanupPending
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "current request lease is not a recovery capability",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Receipt.LeaseOwnerKind = LeaseOwnerRequest
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "released receipt has no current fence",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Receipt.LeaseOwnerID = ""
				snapshot.Receipt.LeaseOwnerKind = ""
				snapshot.Receipt.LeaseAcquiredAt = time.Time{}
				snapshot.Receipt.LeaseHeartbeatAt = time.Time{}
				snapshot.Receipt.LeaseExpiresAt = time.Time{}
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "receipt revision malformed",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Receipt.Revision = 0
			},
			want: ErrForwardRecoveryAuthorizationUnavailable,
		},
		{
			name: "fence owner",
			mutate: func(_ *CurrentForwardRecoverySnapshot, lease *LeaseGrant) {
				lease.Fence.OwnerID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "fence token",
			mutate: func(_ *CurrentForwardRecoverySnapshot, lease *LeaseGrant) {
				lease.Fence.Token++
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "tenant suspended",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Tenant.Status = "suspended"
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "installation linkage",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Installation.InstallationID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "membership linkage",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Membership.PersonID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "trip device linkage",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Trip.DeviceID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "assignment inactive",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Assignment.Status = "ended"
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "consent withdrawn",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.Consent.Status = "withdrawn"
			},
			want: ErrForwardRecoveryUnauthorized,
		},
		{
			name: "current consent revision changed",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot, _ *LeaseGrant) {
				snapshot.ConsentState.CurrentRevisionID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			},
			want: ErrForwardRecoveryUnauthorized,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := artifactAuthSnapshot(now)
			lease := artifactAuthLease(snapshot)
			test.mutate(&snapshot, &lease)
			store := &artifactAuthStoreStub{snapshot: snapshot}
			authorizer := artifactAuthAuthorizer(t, store, now)
			_, _, err := authorizer.Authorize(
				context.Background(),
				artifactAuthTenantID,
				snapshot.Receipt.ReservationKey,
				lease,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Authorize() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSystemRecoveryAuthorizerTreatsMalformedTrustedSnapshotAsUnavailable(t *testing.T) {
	now := artifactAuthNow()
	tests := []struct {
		name   string
		mutate func(*CurrentForwardRecoverySnapshot)
	}{
		{name: "zero read time", mutate: func(snapshot *CurrentForwardRecoverySnapshot) { snapshot.ReadTime = time.Time{} }},
		{name: "unknown tenant status", mutate: func(snapshot *CurrentForwardRecoverySnapshot) { snapshot.Tenant.Status = "unknown" }},
		{name: "unknown receipt state", mutate: func(snapshot *CurrentForwardRecoverySnapshot) { snapshot.Receipt.State = "unknown" }},
		{name: "partial receipt lease", mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
			snapshot.Receipt.LeaseOwnerID = ""
		}},
		{name: "reserved receipt has hold reason", mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
			snapshot.Receipt.RecoveryHoldCode = RecoveryHoldManifestOnly
		}},
		{name: "reserved receipt has hold review time", mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
			snapshot.Receipt.RecoveryHoldReviewDueAt = now.Add(time.Hour)
		}},
		{name: "duplicate role", mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
			snapshot.Membership.Roles = []string{"beneficiary", "beneficiary"}
		}},
		{name: "unsafe firebase uid", mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
			snapshot.Membership.FirebaseUID = "uid/child"
		}},
		{name: "zero installation revision", mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
			snapshot.Installation.Revision = 0
		}},
		{name: "recording trip with end", mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
			endedAt := now.Add(-time.Minute)
			snapshot.Trip.EndedAt = &endedAt
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := artifactAuthSnapshot(now)
			lease := artifactAuthLease(snapshot)
			test.mutate(&snapshot)
			store := &artifactAuthStoreStub{snapshot: snapshot}
			authorizer := artifactAuthAuthorizer(t, store, now)
			_, _, err := authorizer.Authorize(
				context.Background(),
				artifactAuthTenantID,
				snapshot.Receipt.ReservationKey,
				lease,
			)
			if !errors.Is(err, ErrForwardRecoveryAuthorizationUnavailable) {
				t.Fatalf("Authorize() error = %v", err)
			}
		})
	}
}

func TestSystemRecoveryAuthorizerPreservesContextAndSanitizesStoreErrors(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "cancelled", err: context.Canceled, want: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "bounded denial", err: ErrForwardRecoveryUnauthorized, want: ErrForwardRecoveryUnauthorized},
		{name: "provider detail", err: errors.New("private path and credential detail"), want: ErrForwardRecoveryAuthorizationUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &artifactAuthStoreStub{err: test.err}
			authorizer := artifactAuthAuthorizer(t, store, now)
			_, _, err := authorizer.Authorize(
				context.Background(),
				artifactAuthTenantID,
				snapshot.Receipt.ReservationKey,
				artifactAuthLease(snapshot),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Authorize() error = %v, want %v", err, test.want)
			}
			if test.name == "provider detail" && err.Error() != ErrForwardRecoveryAuthorizationUnavailable.Error() {
				t.Fatalf("provider detail leaked: %q", err)
			}
		})
	}
}

func TestSystemRecoveryAuthorizerUsesConservativeClockAndRejectsSkew(t *testing.T) {
	now := artifactAuthNow()
	tests := []struct {
		name       string
		readOffset time.Duration
		want       error
		checkedAt  time.Time
	}{
		{name: "read behind at boundary", readOffset: -MaxForwardRecoveryAuthorizationClockSkew, checkedAt: now},
		{name: "read ahead at boundary", readOffset: MaxForwardRecoveryAuthorizationClockSkew, checkedAt: now.Add(MaxForwardRecoveryAuthorizationClockSkew)},
		{name: "read behind beyond boundary", readOffset: -MaxForwardRecoveryAuthorizationClockSkew - time.Nanosecond, want: ErrForwardRecoveryAuthorizationUnavailable},
		{name: "read ahead beyond boundary", readOffset: MaxForwardRecoveryAuthorizationClockSkew + time.Nanosecond, want: ErrForwardRecoveryAuthorizationUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := artifactAuthSnapshot(now)
			snapshot.ReadTime = now.Add(test.readOffset)
			store := &artifactAuthStoreStub{snapshot: snapshot}
			authorizer := artifactAuthAuthorizer(t, store, now)
			_, grant, err := authorizer.Authorize(
				context.Background(),
				artifactAuthTenantID,
				snapshot.Receipt.ReservationKey,
				artifactAuthLease(snapshot),
			)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("Authorize() error = %v, want %v", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if !grant.checkedAt.Equal(test.checkedAt) {
				t.Fatalf("checkedAt = %s, want %s", grant.checkedAt, test.checkedAt)
			}
		})
	}
}

func TestSystemRecoveryAuthorizerClampsGrantToEveryAuthorizationExpiry(t *testing.T) {
	now := artifactAuthNow()
	tests := []struct {
		name   string
		mutate func(*CurrentForwardRecoverySnapshot)
		want   time.Time
	}{
		{
			name:   "ttl",
			mutate: func(*CurrentForwardRecoverySnapshot) {},
			want:   now.Add(ForwardRecoveryArtifactReadGrantTTL),
		},
		{
			name: "lease",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				snapshot.Receipt.LeaseExpiresAt = now.Add(10 * time.Second)
				snapshot.Receipt.NextRecoveryAt = snapshot.Receipt.LeaseExpiresAt
			},
			want: now.Add(10 * time.Second),
		},
		{
			name: "reservation deadline",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				snapshot.Receipt.CreatedAt = now.Add(-ReservationProcessingWindow + 10*time.Second)
				snapshot.Receipt.ReservationDeadline = snapshot.Receipt.CreatedAt.Add(ReservationProcessingWindow)
				snapshot.Receipt.ArtifactExpiresAt = snapshot.Receipt.CreatedAt.Add(TelemetryArtifactRetention)
				snapshot.Receipt.ReceiptRetentionFloor = snapshot.Receipt.CreatedAt.Add(ReceiptControlRetention)
			},
			want: now.Add(10 * time.Second),
		},
		{
			name: "trip ingest expiry",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				snapshot.Trip.IngestExpiresAt = now.Add(10 * time.Second)
			},
			want: now.Add(10 * time.Second),
		},
		{
			name: "membership valid to",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				expiresAt := now.Add(10 * time.Second)
				snapshot.Membership.ValidTo = &expiresAt
			},
			want: now.Add(10 * time.Second),
		},
		{
			name: "assignment valid to",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				expiresAt := now.Add(10 * time.Second)
				snapshot.Assignment.ValidTo = &expiresAt
			},
			want: now.Add(10 * time.Second),
		},
		{
			name: "consent expiry",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				expiresAt := now.Add(10 * time.Second)
				snapshot.Consent.ExpiresAt = &expiresAt
			},
			want: now.Add(10 * time.Second),
		},
		{
			name: "consent state expiry",
			mutate: func(snapshot *CurrentForwardRecoverySnapshot) {
				expiresAt := now.Add(10 * time.Second)
				snapshot.ConsentState.ExpiresAt = &expiresAt
			},
			want: now.Add(10 * time.Second),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := artifactAuthSnapshot(now)
			test.mutate(&snapshot)
			store := &artifactAuthStoreStub{snapshot: snapshot}
			authorizer := artifactAuthAuthorizer(t, store, now)
			_, grant, err := authorizer.Authorize(
				context.Background(),
				artifactAuthTenantID,
				snapshot.Receipt.ReservationKey,
				artifactAuthLease(snapshot),
			)
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if !grant.expiresAt.Equal(test.want) {
				t.Fatalf("grant expiry = %s, want %s", grant.expiresAt, test.want)
			}
		})
	}
}

type artifactAuthStoreStub struct {
	snapshot CurrentForwardRecoverySnapshot
	err      error
	calls    int
	query    ForwardRecoveryAuthorizationQuery
}

var _ ForwardRecoveryAuthorizationStore = (*artifactAuthStoreStub)(nil)

func (s *artifactAuthStoreStub) LoadCurrentForwardRecovery(
	_ context.Context,
	query ForwardRecoveryAuthorizationQuery,
) (CurrentForwardRecoverySnapshot, error) {
	s.calls++
	s.query = query
	return s.snapshot, s.err
}

func artifactAuthAuthorizer(
	t *testing.T,
	store ForwardRecoveryAuthorizationStore,
	now time.Time,
) *SystemRecoveryAuthorizer {
	t.Helper()
	authorizer, err := NewSystemRecoveryAuthorizer(store, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewSystemRecoveryAuthorizer() error = %v", err)
	}
	return authorizer
}

func TestSystemRecoveryAuthorizerMintsFreshManifestRepairGrant(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)

	request, _, err := authorizer.Authorize(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
	)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	write, reason := authorizer.validator.buildRecoveryManifest(request, ArtifactPinnedLineage{
		SHA256: strings.Repeat("b", 64), CRC32C: 0, Size: 128,
		Generation: 91, Metageneration: 2,
	})
	if reason != "" {
		t.Fatalf("buildRecoveryManifest() reason = %q", reason)
	}

	freshRequest, grant, err := authorizer.AuthorizeManifestRepair(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		artifactAuthManifestEvidence(request, write.Raw, now),
		write,
	)
	if err != nil {
		t.Fatalf("AuthorizeManifestRepair() = %v", err)
	}
	if canonicalArtifactClassificationRequestBinding(freshRequest) !=
		canonicalArtifactClassificationRequestBinding(request) {
		t.Fatal("fresh manifest authorization changed authoritative request")
	}
	if err := ValidateManifestRepairAuthorization(grant, write, now.Add(time.Second)); err != nil {
		t.Fatalf("ValidateManifestRepairAuthorization() = %v", err)
	}
	if store.calls != 2 {
		t.Fatalf("authorization store calls = %d, want 2", store.calls)
	}
}

func TestSystemRecoveryAuthorizerDeniesManifestRepairAfterConsentWithdrawal(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)
	request, _, err := authorizer.Authorize(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
	)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	write, reason := authorizer.validator.buildRecoveryManifest(request, ArtifactPinnedLineage{
		SHA256: strings.Repeat("b", 64), Size: 128, Generation: 91, Metageneration: 2,
	})
	if reason != "" {
		t.Fatalf("buildRecoveryManifest() reason = %q", reason)
	}

	withdrawnAt := now.Add(-time.Second)
	store.snapshot.Consent.Status = "withdrawn"
	store.snapshot.Consent.WithdrawnAt = &withdrawnAt
	store.snapshot.ConsentState.Status = "withdrawn"
	store.snapshot.ConsentState.EffectiveAt = withdrawnAt
	deniedRequest, grant, err := authorizer.AuthorizeManifestRepair(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
		artifactAuthManifestEvidence(request, write.Raw, now),
		write,
	)
	if !errors.Is(err, ErrForwardRecoveryUnauthorized) {
		t.Fatalf("AuthorizeManifestRepair() = %v", err)
	}
	if grant != (ManifestRepairAuthorizationGrant{}) {
		t.Fatal("withdrawn consent received manifest repair capability")
	}
	if deniedRequest != (ArtifactClassificationRequest{}) {
		t.Fatal("withdrawn consent received an authoritative classification request")
	}
	if store.calls != 2 {
		t.Fatalf("authorization store calls = %d, want initial plus withdrawal reads", store.calls)
	}
}

func TestSystemRecoveryAuthorizerRejectsManifestEvidenceBeforeCurrentStateRead(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)
	request, _, err := authorizer.Authorize(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
	)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	write, reason := authorizer.validator.buildRecoveryManifest(request, ArtifactPinnedLineage{
		SHA256: strings.Repeat("b", 64), Size: 128, Generation: 91, Metageneration: 2,
	})
	if reason != "" {
		t.Fatalf("buildRecoveryManifest() reason = %q", reason)
	}
	store.calls = 0

	tests := []struct {
		name     string
		evidence ForwardRecoveryManifestEvidence
		write    RecoveryManifestWrite
	}{
		{
			name: "pass one raw pin differs from write",
			evidence: func() ForwardRecoveryManifestEvidence {
				mismatched := artifactAuthManifestEvidence(request, write.Raw, now)
				mismatched.Result.PinnedRaw.Generation++
				return mismatched
			}(),
			write: write,
		},
		{
			name: "classifier request binding is absent",
			evidence: func() ForwardRecoveryManifestEvidence {
				fabricated := artifactAuthManifestEvidence(request, write.Raw, now)
				fabricated.Result.requestBindingHash = [sha256.Size]byte{}
				return fabricated
			}(),
			write: write,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, grant, err := authorizer.AuthorizeManifestRepair(
				context.Background(),
				snapshot.Receipt.TenantID,
				snapshot.Receipt.ReservationKey,
				lease,
				test.evidence,
				test.write,
			)
			if !errors.Is(err, ErrInvalidManifestRepairAuthorization) {
				t.Fatalf("AuthorizeManifestRepair() = %v", err)
			}
			if grant != (ManifestRepairAuthorizationGrant{}) {
				t.Fatal("invalid pass-one evidence received manifest repair capability")
			}
			if store.calls != 0 {
				t.Fatalf("invalid evidence reached current-state store: calls=%d", store.calls)
			}
		})
	}
}

func TestSystemRecoveryAuthorizerRejectsManifestEvidenceAfterLeaseRenewal(t *testing.T) {
	now := artifactAuthNow()
	snapshot := artifactAuthSnapshot(now)
	store := &artifactAuthStoreStub{snapshot: snapshot}
	authorizer := artifactAuthAuthorizer(t, store, now)
	lease := artifactAuthLease(snapshot)
	request, _, err := authorizer.Authorize(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		lease,
	)
	if err != nil {
		t.Fatalf("Authorize() = %v", err)
	}
	write, reason := authorizer.validator.buildRecoveryManifest(request, ArtifactPinnedLineage{
		SHA256: strings.Repeat("b", 64), Size: 128, Generation: 91, Metageneration: 2,
	})
	if reason != "" {
		t.Fatalf("buildRecoveryManifest() reason = %q", reason)
	}
	evidence := artifactAuthManifestEvidence(request, write.Raw, now)

	store.snapshot.Receipt.Revision++
	store.snapshot.Receipt.LeaseHeartbeatAt = now
	store.snapshot.Receipt.LeaseExpiresAt = now.Add(3 * time.Minute)
	store.snapshot.Receipt.NextRecoveryAt = store.snapshot.Receipt.LeaseExpiresAt
	store.snapshot.Receipt.UpdatedAt = now
	renewedLease := artifactAuthLease(store.snapshot)
	store.calls = 0
	_, grant, err := authorizer.AuthorizeManifestRepair(
		context.Background(),
		snapshot.Receipt.TenantID,
		snapshot.Receipt.ReservationKey,
		renewedLease,
		evidence,
		write,
	)
	if !errors.Is(err, ErrInvalidManifestRepairAuthorization) {
		t.Fatalf("AuthorizeManifestRepair() = %v", err)
	}
	if grant != (ManifestRepairAuthorizationGrant{}) {
		t.Fatal("stale pass-one evidence received renewed-lease capability")
	}
	if store.calls != 1 {
		t.Fatalf("fresh current-state reads = %d, want 1", store.calls)
	}
}

func artifactAuthManifestEvidence(
	request ArtifactClassificationRequest,
	raw StoredArtifact,
	observedAt time.Time,
) ForwardRecoveryManifestEvidence {
	pinnedRaw := ArtifactPinnedLineage{
		SHA256:         raw.SHA256,
		CRC32C:         raw.CRC32C,
		Size:           raw.Size,
		Generation:     raw.Generation,
		Metageneration: raw.Metageneration,
	}
	return ForwardRecoveryManifestEvidence{
		Request: request,
		Result: ArtifactClassificationResult{
			Classification: ArtifactClassificationValidRawOnly,
			ReasonCode:     ArtifactReasonRawValidManifestAbsent,
			RetentionPhase: ArtifactRetentionBeforeExpiry,
			ManifestInventory: ArtifactInventorySummary{
				Performed: true,
				Coverage:  ArtifactInventoryCoverageComplete,
			},
			RawInventory: ArtifactInventorySummary{
				Performed: true, NonSoftDeletedCount: 1,
				Coverage: ArtifactInventoryCoverageComplete,
			},
			PinnedRaw:          &pinnedRaw,
			ValidatorVersion:   request.ValidatorVersion,
			ObservedAt:         observedAt,
			requestBindingHash: canonicalArtifactClassificationRequestBinding(request),
		},
	}
}

func artifactAuthNow() time.Time {
	return time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
}

func artifactAuthSnapshot(now time.Time) CurrentForwardRecoverySnapshot {
	createdAt := now.Add(-5 * time.Minute)
	firstCapturedAt := now.Add(-30 * time.Minute)
	lastCapturedAt := now.Add(-20 * time.Minute)
	grantedAt := now.Add(-48 * time.Hour)
	leaseAcquiredAt := now.Add(-time.Minute)
	leaseHeartbeatAt := now.Add(-30 * time.Second)
	leaseExpiresAt := now.Add(2 * time.Minute)
	reservationKey := DeriveReservationKey(
		"telemetry-batch.v2",
		artifactAuthTenantID,
		artifactAuthInstallationID,
		artifactAuthClientBatchID,
	)
	clientBatchKey := DeriveClientBatchKey(artifactAuthTenantID, artifactAuthClientBatchID)

	return CurrentForwardRecoverySnapshot{
		Receipt: Receipt{
			ReservationKey:        reservationKey,
			ClientBatchKey:        clientBatchKey,
			ReceiptID:             artifactAuthReceiptID,
			TenantID:              artifactAuthTenantID,
			BatchID:               artifactAuthReceiptID,
			DeviceID:              artifactAuthDeviceID,
			TripID:                artifactAuthTripID,
			InstallationID:        artifactAuthInstallationID,
			ConsentRevisionID:     artifactAuthConsentID,
			ClientBatchID:         artifactAuthClientBatchID,
			PayloadSchemaVersion:  "telemetry-batch.v2",
			BodyHash:              strings.Repeat("a", 64),
			ExpectedSampleCount:   2,
			FirstCapturedAt:       firstCapturedAt,
			LastCapturedAt:        lastCapturedAt,
			ValidatorVersion:      TelemetryValidatorVersion,
			State:                 ReceiptReserved,
			FencingToken:          3,
			LeaseOwnerID:          artifactAuthOwnerID,
			LeaseOwnerKind:        LeaseOwnerSweeper,
			LeaseAcquiredAt:       leaseAcquiredAt,
			LeaseHeartbeatAt:      leaseHeartbeatAt,
			LeaseExpiresAt:        leaseExpiresAt,
			RecoveryAttemptCount:  1,
			NextRecoveryAt:        leaseExpiresAt,
			Revision:              4,
			CreatedAt:             createdAt,
			UpdatedAt:             leaseHeartbeatAt,
			ReservationDeadline:   createdAt.Add(ReservationProcessingWindow),
			ArtifactExpiresAt:     createdAt.Add(TelemetryArtifactRetention),
			ReceiptRetentionFloor: createdAt.Add(ReceiptControlRetention),
		},
		Tenant: CurrentRecoveryTenant{TenantID: artifactAuthTenantID, Status: "active"},
		Membership: CurrentRecoveryMembership{
			TenantID: artifactAuthTenantID, FirebaseUID: "firebase-user", PersonID: artifactAuthPersonID,
			Roles: []string{"beneficiary"}, Status: "active", ValidFrom: now.Add(-7 * 24 * time.Hour),
		},
		Installation: CurrentRecoveryInstallation{
			TenantID: artifactAuthTenantID, InstallationID: artifactAuthInstallationID,
			FirebaseUID: "firebase-user", AppID: "firebase-app", Status: "active",
			SchemaVersion: 1, Revision: 2, RegisteredAt: now.Add(-6 * 24 * time.Hour),
			CreatedAt: now.Add(-7 * 24 * time.Hour), UpdatedAt: now.Add(-time.Hour),
		},
		Trip: CurrentRecoveryTrip{
			TenantID: artifactAuthTenantID, TripID: artifactAuthTripID, DeviceID: artifactAuthDeviceID,
			PersonID: artifactAuthPersonID, DeviceAssignmentID: artifactAuthAssignmentID,
			InstallationID: artifactAuthInstallationID, ClientSessionID: artifactAuthSessionID,
			ConsentRevisionID: artifactAuthConsentID, StartedAt: now.Add(-time.Hour),
			IngestExpiresAt: now.Add(24 * time.Hour), CaptureMode: "foreground", Status: "recording",
		},
		Assignment: CurrentRecoveryDeviceAssignment{
			TenantID: artifactAuthTenantID, AssignmentID: artifactAuthAssignmentID,
			DeviceID: artifactAuthDeviceID, PersonID: artifactAuthPersonID,
			AssignmentType: "primary_user", Status: "active", ValidFrom: now.Add(-7 * 24 * time.Hour),
		},
		Consent: CurrentRecoveryConsentRevision{
			TenantID: artifactAuthTenantID, ConsentRevisionID: artifactAuthConsentID,
			PersonID: artifactAuthPersonID, PurposeCode: "precise_location", Status: "granted",
			GrantedAt: &grantedAt,
		},
		ConsentState: CurrentRecoveryConsentState{
			TenantID: artifactAuthTenantID, PersonID: artifactAuthPersonID,
			PurposeCode: "precise_location", CurrentRevisionID: artifactAuthConsentID,
			Status: "granted", EffectiveAt: grantedAt,
		},
		ReadTime: now.Add(-time.Second),
	}
}

func artifactAuthLease(snapshot CurrentForwardRecoverySnapshot) LeaseGrant {
	return artifactAuthLeaseWithKind(snapshot, LeaseOwnerSweeper)
}

func artifactAuthLeaseWithKind(
	snapshot CurrentForwardRecoverySnapshot,
	kind LeaseOwnerKind,
) LeaseGrant {
	return LeaseGrant{
		Fence: LeaseFence{
			OwnerID:   snapshot.Receipt.LeaseOwnerID,
			Token:     snapshot.Receipt.FencingToken,
			ExpiresAt: snapshot.Receipt.LeaseExpiresAt,
		},
		OwnerKind:   kind,
		AcquiredAt:  snapshot.Receipt.LeaseAcquiredAt,
		HeartbeatAt: snapshot.Receipt.LeaseHeartbeatAt,
	}
}
