package authorization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const (
	tenantID       = "018f22e2-6c9d-7d10-8d2a-6f31a4e11001"
	deviceID       = "018f22e2-6c9d-7d10-8d2a-6f31a4e11002"
	tripID         = "018f22e2-6c9d-7d10-8d2a-6f31a4e11003"
	sessionID      = "4a1f29c8-d5bf-4dd5-bd8c-b9930a4d0004"
	installationID = "4a1f29c8-d5bf-4dd5-bd8c-b9930a4d0005"
	consentID      = "018f22e2-6c9d-7d10-8d2a-6f31a4e11006"
	personID       = "018f22e2-6c9d-7d10-8d2a-6f31a4e11007"
	assignmentID   = "018f22e2-6c9d-7d10-8d2a-6f31a4e11008"
	firebaseUID    = "firebase-user-01"
	appID          = "1:1234567890:android:abc123"
)

func TestAuthorizerAllowsExactActiveBeneficiaryScope(t *testing.T) {
	now := fixedNow()
	authorizer := newTestAuthorizer(t, validSnapshot(now), now)
	if err := authorizer.Authorize(context.Background(), validPrincipal(), validScope(now)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
}

func TestEvaluateSnapshotAllowsTheSameProviderNeutralPolicy(t *testing.T) {
	now := fixedNow()
	if err := EvaluateSnapshot(validPrincipal(), validScope(now), validSnapshot(now), now); err != nil {
		t.Fatalf("EvaluateSnapshot() error = %v", err)
	}
}

func TestAuthorizerRejectsRelationshipAndValidityFailures(t *testing.T) {
	now := fixedNow()
	tests := []struct {
		name   string
		mutate func(*ingest.Principal, *ingest.BatchScope, *Snapshot)
	}{
		{name: "tenant suspended", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Tenant.Status = "suspended" }},
		{name: "membership UID mismatch", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Membership.FirebaseUID = "another-user"
		}},
		{name: "membership has no beneficiary role", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Membership.Roles = []string{"guardian"}
		}},
		{name: "non-person case worker cannot upload", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Membership.Roles = []string{"case_worker"}
			s.Membership.PersonID = ""
		}},
		{name: "membership starts in future", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Membership.ValidFrom = now.Add(time.Second)
		}},
		{name: "membership started after capture", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, s *Snapshot) {
			s.Membership.ValidFrom = scope.FirstCapturedAt.Add(time.Second)
		}},
		{name: "membership valid to boundary", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Membership.ValidTo = timePointer(now) }},
		{name: "membership does not cover future last sample", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, s *Snapshot) {
			scope.LastCapturedAt = now.Add(2 * time.Minute)
			s.Membership.ValidTo = timePointer(now.Add(time.Minute))
		}},
		{name: "installation UID mismatch", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Installation.FirebaseUID = "another-user"
		}},
		{name: "installation App ID mismatch", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Installation.AppID = "another-app" }},
		{name: "installation registered after capture", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, s *Snapshot) {
			s.Installation.RegisteredAt = scope.FirstCapturedAt.Add(time.Second)
		}},
		{name: "installation revoked", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Installation.Status = "revoked" }},
		{name: "installation has revoked timestamp", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Installation.RevokedAt = timePointer(now.Add(-time.Minute))
		}},
		{name: "trip device mismatch", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Trip.DeviceID = "018f22e2-6c9d-7d10-8d2a-6f31a4e11999"
		}},
		{name: "trip installation mismatch", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Trip.InstallationID = "4a1f29c8-d5bf-4dd5-bd8c-b9930a4d0999"
		}},
		{name: "trip client session mismatch", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Trip.ClientSessionID = "4a1f29c8-d5bf-4dd5-bd8c-b9930a4d0998"
		}},
		{name: "trip person is not member person", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Trip.PersonID = "018f22e2-6c9d-7d10-8d2a-6f31a4e11997"
		}},
		{name: "trip cancelled", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Trip.Status = "cancelled" }},
		{name: "trip ingest expiry boundary", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Trip.IngestExpiresAt = now }},
		{name: "sample predates trip", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, _ *Snapshot) {
			scope.FirstCapturedAt = now.Add(-2 * time.Hour)
		}},
		{name: "sample exceeds clock skew", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, _ *Snapshot) {
			scope.LastCapturedAt = now.Add(5*time.Minute + time.Nanosecond)
		}},
		{name: "sample after ended trip", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, s *Snapshot) {
			s.Trip.Status = "ended"
			s.Trip.EndedAt = timePointer(now.Add(-25 * time.Minute))
			scope.LastCapturedAt = now.Add(-20 * time.Minute)
		}},
		{name: "assignment device mismatch", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Assignment.DeviceID = "018f22e2-6c9d-7d10-8d2a-6f31a4e11996"
		}},
		{name: "assignment inactive", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Assignment.Status = "ended" }},
		{name: "assignment started after capture", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, s *Snapshot) {
			s.Assignment.ValidFrom = scope.FirstCapturedAt.Add(time.Second)
		}},
		{name: "assignment expired", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Assignment.ValidTo = timePointer(now) }},
		{name: "assignment does not cover future last sample", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, s *Snapshot) {
			scope.LastCapturedAt = now.Add(2 * time.Minute)
			s.Assignment.ValidTo = timePointer(now.Add(time.Minute))
		}},
		{name: "consent wrong purpose", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.Consent.PurposeCode = "maintenance_model"
		}},
		{name: "consent withdrawn", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Consent.Status = "withdrawn" }},
		{name: "consent granted after capture", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, s *Snapshot) {
			s.Consent.GrantedAt = timePointer(scope.FirstCapturedAt.Add(time.Second))
		}},
		{name: "consent expiry boundary", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.Consent.ExpiresAt = timePointer(now) }},
		{name: "current consent revision changed", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) {
			s.ConsentState.CurrentRevisionID = "018f22e2-6c9d-7d10-8d2a-6f31a4e11995"
		}},
		{name: "current consent withdrawn", mutate: func(_ *ingest.Principal, _ *ingest.BatchScope, s *Snapshot) { s.ConsentState.Status = "withdrawn" }},
		{name: "current consent effective after capture", mutate: func(_ *ingest.Principal, scope *ingest.BatchScope, s *Snapshot) {
			s.ConsentState.EffectiveAt = scope.FirstCapturedAt.Add(time.Second)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			principal := validPrincipal()
			scope := validScope(now)
			snapshot := validSnapshot(now)
			test.mutate(&principal, &scope, &snapshot)
			authorizer := newTestAuthorizer(t, snapshot, now)
			err := authorizer.Authorize(context.Background(), principal, scope)
			if !errors.Is(err, ingest.ErrBatchUnauthorized) {
				t.Fatalf("Authorize() error = %v, want ErrBatchUnauthorized", err)
			}
		})
	}
}

func TestAuthorizerTreatsMalformedTrustedDocumentsAsUnavailable(t *testing.T) {
	now := fixedNow()
	tests := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{name: "unknown role", mutate: func(s *Snapshot) { s.Membership.Roles = []string{"beneficiary", "root"} }},
		{name: "duplicate role", mutate: func(s *Snapshot) { s.Membership.Roles = []string{"beneficiary", "beneficiary"} }},
		{name: "zero membership start", mutate: func(s *Snapshot) { s.Membership.ValidFrom = time.Time{} }},
		{name: "inverted membership window", mutate: func(s *Snapshot) { s.Membership.ValidTo = timePointer(s.Membership.ValidFrom) }},
		{name: "unknown capture mode", mutate: func(s *Snapshot) { s.Trip.CaptureMode = "teleport" }},
		{name: "unknown assignment type", mutate: func(s *Snapshot) { s.Assignment.AssignmentType = "owner" }},
		{name: "zero granted at", mutate: func(s *Snapshot) { s.Consent.GrantedAt = timePointer(time.Time{}) }},
		{name: "invalid assignment id", mutate: func(s *Snapshot) { s.Trip.DeviceAssignmentID = "not-an-id" }},
		{name: "ended without ended at", mutate: func(s *Snapshot) { s.Trip.Status = "ended" }},
		{name: "recording with ended at", mutate: func(s *Snapshot) { s.Trip.EndedAt = timePointer(now.Add(-time.Minute)) }},
		{name: "ended in future", mutate: func(s *Snapshot) {
			s.Trip.Status = "ended"
			s.Trip.EndedAt = timePointer(now.Add(time.Second))
		}},
		{name: "end before start", mutate: func(s *Snapshot) { s.Trip.EndedAt = timePointer(s.Trip.StartedAt.Add(-time.Second)) }},
		{name: "zero installation schema", mutate: func(s *Snapshot) { s.Installation.SchemaVersion = 0 }},
		{name: "zero installation revision", mutate: func(s *Snapshot) { s.Installation.Revision = 0 }},
		{name: "zero installation created at", mutate: func(s *Snapshot) { s.Installation.CreatedAt = time.Time{} }},
		{name: "unknown consent status", mutate: func(s *Snapshot) { s.Consent.Status = "maybe" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := validSnapshot(now)
			test.mutate(&snapshot)
			authorizer := newTestAuthorizer(t, snapshot, now)
			err := authorizer.Authorize(context.Background(), validPrincipal(), validScope(now))
			if !errors.Is(err, ErrSnapshotUnavailable) {
				t.Fatalf("Authorize() error = %v, want ErrSnapshotUnavailable", err)
			}
		})
	}
}

func TestAuthorizerMapsReaderOutcomesWithoutLeakingDetails(t *testing.T) {
	now := fixedNow()
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "not found", err: ErrSnapshotNotFound, want: ingest.ErrBatchUnauthorized},
		{name: "provider detail", err: errors.New("private firestore path and token"), want: ErrSnapshotUnavailable},
		{name: "canceled", err: context.Canceled, want: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorizer, err := NewAuthorizer(readerFunc(func(context.Context, ingest.Principal, ingest.BatchScope) (Snapshot, error) {
				return Snapshot{}, test.err
			}), func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			got := authorizer.Authorize(context.Background(), validPrincipal(), validScope(now))
			if !errors.Is(got, test.want) {
				t.Fatalf("Authorize() error = %v, want %v", got, test.want)
			}
			if got != nil && test.name == "provider detail" && got.Error() != ErrSnapshotUnavailable.Error() {
				t.Fatalf("provider detail leaked: %q", got)
			}
		})
	}
}

func TestAuthorizerRejectsInvalidScopeBeforeReader(t *testing.T) {
	now := fixedNow()
	called := false
	authorizer, err := NewAuthorizer(readerFunc(func(context.Context, ingest.Principal, ingest.BatchScope) (Snapshot, error) {
		called = true
		return validSnapshot(now), nil
	}), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ingest.Principal, *ingest.BatchScope)
	}{
		{name: "unsafe UID", mutate: func(p *ingest.Principal, _ *ingest.BatchScope) { p.FirebaseUID = "uid/child" }},
		{name: "invalid tenant", mutate: func(_ *ingest.Principal, s *ingest.BatchScope) { s.TenantID = "tenant" }},
		{name: "zero capture time", mutate: func(_ *ingest.Principal, s *ingest.BatchScope) { s.FirstCapturedAt = time.Time{} }},
		{name: "inverted capture window", mutate: func(_ *ingest.Principal, s *ingest.BatchScope) {
			s.LastCapturedAt = s.FirstCapturedAt.Add(-time.Second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called = false
			principal := validPrincipal()
			scope := validScope(now)
			test.mutate(&principal, &scope)
			if got := authorizer.Authorize(context.Background(), principal, scope); !errors.Is(got, ingest.ErrBatchUnauthorized) {
				t.Fatalf("Authorize() error = %v", got)
			}
			if called {
				t.Fatal("reader called for an invalid request")
			}
		})
	}
}

func TestAuthorizerFailsClosedWhenUninitialized(t *testing.T) {
	if _, err := NewAuthorizer(nil, nil); err == nil {
		t.Fatal("NewAuthorizer(nil) error = nil")
	}
	var nilAuthorizer *Authorizer
	if err := nilAuthorizer.Authorize(context.Background(), validPrincipal(), validScope(fixedNow())); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("nil Authorize() error = %v", err)
	}
	if err := (&Authorizer{}).Authorize(context.Background(), validPrincipal(), validScope(fixedNow())); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("zero Authorize() error = %v", err)
	}
}

func TestConsentStateDocumentIDIsStableAndOpaque(t *testing.T) {
	got := ConsentStateDocumentID(personID, PreciseLocationPurpose)
	if len(got) != 64 {
		t.Fatalf("ConsentStateDocumentID() length = %d", len(got))
	}
	if got == ConsentStateDocumentID(personID, "maintenance_model") {
		t.Fatal("purpose was not included in derived ID")
	}
	if got == ConsentStateDocumentID(deviceID, PreciseLocationPurpose) {
		t.Fatal("person ID was not included in derived ID")
	}
}

func newTestAuthorizer(t *testing.T, snapshot Snapshot, now time.Time) *Authorizer {
	t.Helper()
	authorizer, err := NewAuthorizer(readerFunc(func(context.Context, ingest.Principal, ingest.BatchScope) (Snapshot, error) {
		return snapshot, nil
	}), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return authorizer
}

func validPrincipal() ingest.Principal {
	return ingest.Principal{FirebaseUID: firebaseUID, AppID: appID}
}

func validScope(now time.Time) ingest.BatchScope {
	return ingest.BatchScope{
		TenantID:          tenantID,
		DeviceID:          deviceID,
		TripID:            tripID,
		ClientSessionID:   sessionID,
		InstallationID:    installationID,
		ConsentRevisionID: consentID,
		FirstCapturedAt:   now.Add(-30 * time.Minute),
		LastCapturedAt:    now.Add(-20 * time.Minute),
	}
}

func validSnapshot(now time.Time) Snapshot {
	grantedAt := now.Add(-48 * time.Hour)
	return Snapshot{
		Tenant: Tenant{TenantID: tenantID, Status: "active"},
		Membership: Membership{
			TenantID: tenantID, FirebaseUID: firebaseUID, PersonID: personID,
			Roles: []string{"beneficiary"}, Status: "active", ValidFrom: now.Add(-7 * 24 * time.Hour),
		},
		Installation: Installation{
			TenantID: tenantID, InstallationID: installationID, FirebaseUID: firebaseUID,
			AppID: appID, Status: "active", SchemaVersion: 1, Revision: 1,
			CreatedAt: now.Add(-8 * 24 * time.Hour), RegisteredAt: now.Add(-7 * 24 * time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
		Trip: Trip{
			TenantID: tenantID, TripID: tripID, DeviceID: deviceID, PersonID: personID,
			DeviceAssignmentID: assignmentID, InstallationID: installationID,
			ClientSessionID: sessionID, ConsentRevisionID: consentID,
			StartedAt: now.Add(-time.Hour), IngestExpiresAt: now.Add(24 * time.Hour),
			CaptureMode: "foreground", Status: "recording",
		},
		Assignment: DeviceAssignment{
			TenantID: tenantID, AssignmentID: assignmentID, DeviceID: deviceID, PersonID: personID,
			AssignmentType: "primary_user", Status: "active", ValidFrom: now.Add(-7 * 24 * time.Hour),
		},
		Consent: ConsentRevision{
			TenantID: tenantID, ConsentRevisionID: consentID, PersonID: personID,
			PurposeCode: PreciseLocationPurpose, Status: "granted", GrantedAt: &grantedAt,
		},
		ConsentState: ConsentState{
			TenantID: tenantID, PersonID: personID, PurposeCode: PreciseLocationPurpose,
			CurrentRevisionID: consentID, Status: "granted", EffectiveAt: grantedAt,
		},
	}
}

func fixedNow() time.Time {
	return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
}

func timePointer(value time.Time) *time.Time {
	return &value
}

type readerFunc func(context.Context, ingest.Principal, ingest.BatchScope) (Snapshot, error)

func (f readerFunc) Load(ctx context.Context, principal ingest.Principal, scope ingest.BatchScope) (Snapshot, error) {
	return f(ctx, principal, scope)
}
