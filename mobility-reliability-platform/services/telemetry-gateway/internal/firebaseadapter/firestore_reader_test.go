package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const (
	testTenantID      = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a1"
	testInstallation  = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a2"
	testTripID        = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a3"
	testConsentID     = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a4"
	testAssignmentID  = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a5"
	testPersonID      = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a6"
	testDeviceID      = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a7"
	testClientSession = "018f1f4e-2f5e-7d31-8c77-43b50f4c91a8"
)

func TestNewFirestoreSnapshotReaderValidatesDependenciesAndTimeout(t *testing.T) {
	client := &firestore.Client{}
	tests := []struct {
		name    string
		client  *firestore.Client
		timeout time.Duration
		wantErr bool
	}{
		{name: "nil client", timeout: time.Second, wantErr: true},
		{name: "zero timeout", client: client, wantErr: true},
		{name: "negative timeout", client: client, timeout: -time.Second, wantErr: true},
		{name: "unbounded timeout", client: client, timeout: maxSnapshotReadTimeout + time.Nanosecond, wantErr: true},
		{name: "valid", client: client, timeout: 2 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewFirestoreSnapshotReader(test.client, test.timeout)
			if (err != nil) != test.wantErr {
				t.Fatalf("NewFirestoreSnapshotReader() error = %v", err)
			}
			if !test.wantErr && (reader == nil || reader.getAll == nil) {
				t.Fatal("NewFirestoreSnapshotReader() returned an incomplete reader")
			}
		})
	}
}

func TestAuthorizationDocumentPathsAreExactAndPseudonymous(t *testing.T) {
	principal, scope := validAuthorizationInput()
	primary, err := primaryAuthorizationPaths(principal, scope)
	if err != nil {
		t.Fatalf("primaryAuthorizationPaths() error = %v", err)
	}
	wantPrimary := []string{
		"tenants/" + testTenantID,
		"tenants/" + testTenantID + "/memberships/firebase-user",
		"tenants/" + testTenantID + "/appInstallations/" + testInstallation,
		"tenants/" + testTenantID + "/trips/" + testTripID,
		"tenants/" + testTenantID + "/consentRevisions/" + testConsentID,
	}
	if !reflect.DeepEqual(primary, wantPrimary) {
		t.Fatalf("primaryAuthorizationPaths() = %#v, want %#v", primary, wantPrimary)
	}

	related, err := relatedAuthorizationPaths(testTenantID, testAssignmentID, testPersonID)
	if err != nil {
		t.Fatalf("relatedAuthorizationPaths() error = %v", err)
	}
	wantConsentStateID := authorization.ConsentStateDocumentID(
		testPersonID,
		authorization.PreciseLocationPurpose,
	)
	wantRelated := []string{
		"tenants/" + testTenantID + "/deviceAssignments/" + testAssignmentID,
		"tenants/" + testTenantID + "/consentStates/" + wantConsentStateID,
	}
	if !reflect.DeepEqual(related, wantRelated) {
		t.Fatalf("relatedAuthorizationPaths() = %#v, want %#v", related, wantRelated)
	}
	if related[1] == "tenants/"+testTenantID+"/consentStates/"+testPersonID+"-precise_location" {
		t.Fatal("consent state path exposes the person and purpose directly")
	}
}

func TestAuthorizationDocumentPathsRejectMalformedReferences(t *testing.T) {
	principal, scope := validAuthorizationInput()
	principal.FirebaseUID = "nested/user"
	if _, err := primaryAuthorizationPaths(principal, scope); !errors.Is(err, authorization.ErrSnapshotUnavailable) {
		t.Fatalf("primaryAuthorizationPaths() error = %v", err)
	}
	if _, err := relatedAuthorizationPaths(testTenantID, "not-a-uuid", testPersonID); !errors.Is(err, authorization.ErrSnapshotUnavailable) {
		t.Fatalf("relatedAuthorizationPaths() error = %v", err)
	}
}

func TestFirestoreSnapshotReaderSanitizesProviderErrors(t *testing.T) {
	providerError := errors.New("provider failure containing sensitive implementation detail")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "plain provider", err: providerError, want: authorization.ErrSnapshotUnavailable},
		{name: "grpc unavailable", err: status.Error(codes.Unavailable, "sensitive provider detail"), want: authorization.ErrSnapshotUnavailable},
		{name: "grpc deadline", err: status.Error(codes.DeadlineExceeded, "provider detail"), want: context.DeadlineExceeded},
		{name: "grpc cancelled", err: status.Error(codes.Canceled, "provider detail"), want: context.Canceled},
		{name: "wrapped deadline", err: errors.Join(errors.New("wrapper"), context.DeadlineExceeded), want: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := testFirestoreReader(func(context.Context, []*firestore.DocumentRef) ([]*firestore.DocumentSnapshot, error) {
				return nil, test.err
			})
			principal, scope := validAuthorizationInput()
			_, err := reader.Load(context.Background(), principal, scope)
			if !errors.Is(err, test.want) {
				t.Fatalf("Load() error = %v, want %v", err, test.want)
			}
			if err.Error() == test.err.Error() && test.want == authorization.ErrSnapshotUnavailable {
				t.Fatal("Load() exposed the provider error")
			}
		})
	}
}

func TestFirestoreSnapshotReaderPreservesRequestDeadline(t *testing.T) {
	reader := testFirestoreReader(func(ctx context.Context, _ []*firestore.DocumentRef) ([]*firestore.DocumentSnapshot, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	reader.timeout = 5 * time.Millisecond

	principal, scope := validAuthorizationInput()
	_, err := reader.Load(context.Background(), principal, scope)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Load() error = %v, want context deadline", err)
	}
}

func TestFirestoreSnapshotReaderSeparatesMissingAndMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		docs []*firestore.DocumentSnapshot
		want error
	}{
		{
			name: "missing",
			docs: []*firestore.DocumentSnapshot{{}},
			want: authorization.ErrSnapshotNotFound,
		},
		{
			name: "nil snapshot",
			docs: []*firestore.DocumentSnapshot{nil},
			want: authorization.ErrSnapshotUnavailable,
		},
		{
			name: "wrong response count",
			docs: nil,
			want: authorization.ErrSnapshotUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := testFirestoreReader(func(context.Context, []*firestore.DocumentRef) ([]*firestore.DocumentSnapshot, error) {
				return test.docs, nil
			})
			_, err := reader.readExact(context.Background(), []string{"tenants/" + testTenantID})
			if !errors.Is(err, test.want) {
				t.Fatalf("readExact() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAssembleAuthorizationSnapshotMapsEveryControlPlaneField(t *testing.T) {
	from := time.Date(2026, 7, 21, 1, 2, 3, 4, time.UTC)
	to := from.Add(time.Hour)
	want := authorization.Snapshot{
		Tenant: authorization.Tenant{TenantID: testTenantID, Status: "active"},
		Membership: authorization.Membership{
			TenantID: testTenantID, FirebaseUID: "firebase-user", PersonID: testPersonID,
			Roles: []string{"beneficiary"}, Status: "active", ValidFrom: from, ValidTo: &to,
		},
		Installation: authorization.Installation{
			TenantID: testTenantID, InstallationID: testInstallation, FirebaseUID: "firebase-user",
			AppID: "firebase-app", Status: "active", SchemaVersion: 1, Revision: 2,
			RegisteredAt: from, CreatedAt: from, UpdatedAt: to, RevokedAt: &to,
		},
		Trip: authorization.Trip{
			TenantID: testTenantID, TripID: testTripID, DeviceID: testDeviceID, PersonID: testPersonID,
			DeviceAssignmentID: testAssignmentID, InstallationID: testInstallation,
			ClientSessionID: testClientSession, ConsentRevisionID: testConsentID,
			StartedAt: from, EndedAt: &to, IngestExpiresAt: to, CaptureMode: "foreground", Status: "ended",
		},
		Assignment: authorization.DeviceAssignment{
			TenantID: testTenantID, AssignmentID: testAssignmentID, DeviceID: testDeviceID,
			PersonID: testPersonID, AssignmentType: "primary_user", Status: "active", ValidFrom: from, ValidTo: &to,
		},
		Consent: authorization.ConsentRevision{
			TenantID: testTenantID, ConsentRevisionID: testConsentID, PersonID: testPersonID,
			PurposeCode: "precise_location", Status: "granted", GrantedAt: &from, WithdrawnAt: &to, ExpiresAt: &to,
		},
		ConsentState: authorization.ConsentState{
			TenantID: testTenantID, PersonID: testPersonID, PurposeCode: "precise_location",
			CurrentRevisionID: testConsentID, Status: "granted", EffectiveAt: from, ExpiresAt: &to,
		},
	}

	got := assembleAuthorizationSnapshot(
		firestoreTenant{TenantID: testTenantID, Status: "active"},
		firestoreMembership{
			TenantID: testTenantID, FirebaseUID: "firebase-user", PersonID: testPersonID,
			Roles: []string{"beneficiary"}, Status: "active", ValidFrom: from, ValidTo: &to,
		},
		firestoreInstallation{
			TenantID: testTenantID, InstallationID: testInstallation, FirebaseUID: "firebase-user",
			AppID: "firebase-app", Status: "active", SchemaVersion: 1, Revision: 2,
			RegisteredAt: from, CreatedAt: from, UpdatedAt: to, RevokedAt: &to,
		},
		firestoreTrip{
			TenantID: testTenantID, TripID: testTripID, DeviceID: testDeviceID, PersonID: testPersonID,
			DeviceAssignmentID: testAssignmentID, InstallationID: testInstallation,
			ClientSessionID: testClientSession, ConsentRevisionID: testConsentID,
			StartedAt: from, EndedAt: &to, IngestExpiresAt: to, CaptureMode: "foreground", Status: "ended",
		},
		firestoreDeviceAssignment{
			TenantID: testTenantID, AssignmentID: testAssignmentID, DeviceID: testDeviceID,
			PersonID: testPersonID, AssignmentType: "primary_user", Status: "active", ValidFrom: from, ValidTo: &to,
		},
		firestoreConsentRevision{
			TenantID: testTenantID, ConsentRevisionID: testConsentID, PersonID: testPersonID,
			PurposeCode: "precise_location", Status: "granted", GrantedAt: &from, WithdrawnAt: &to, ExpiresAt: &to,
		},
		firestoreConsentState{
			TenantID: testTenantID, PersonID: testPersonID, PurposeCode: "precise_location",
			CurrentRevisionID: testConsentID, Status: "granted", EffectiveAt: from, ExpiresAt: &to,
		},
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assembleAuthorizationSnapshot() = %#v, want %#v", got, want)
	}
}

func TestFirestoreAuthorizationDTOsUseCanonicalSnakeCaseFields(t *testing.T) {
	tests := []struct {
		value any
		field string
		want  string
	}{
		{value: firestoreInstallation{}, field: "AppID", want: "app_check_app_id"},
		{value: firestoreInstallation{}, field: "SchemaVersion", want: "schema_version"},
		{value: firestoreTrip{}, field: "DeviceAssignmentID", want: "device_assignment_id"},
		{value: firestoreTrip{}, field: "IngestExpiresAt", want: "ingest_expires_at"},
		{value: firestoreConsentState{}, field: "CurrentRevisionID", want: "current_revision_id"},
	}
	for _, test := range tests {
		field, ok := reflect.TypeOf(test.value).FieldByName(test.field)
		if !ok {
			t.Fatalf("field %T.%s not found", test.value, test.field)
		}
		if got := field.Tag.Get("firestore"); got != test.want {
			t.Fatalf("%T.%s firestore tag = %q, want %q", test.value, test.field, got, test.want)
		}
	}
}

func testFirestoreReader(getAll getAllDocuments) *FirestoreSnapshotReader {
	return &FirestoreSnapshotReader{
		client:  &firestore.Client{},
		timeout: time.Second,
		getAll:  getAll,
	}
}

func validAuthorizationInput() (ingest.Principal, ingest.BatchScope) {
	return ingest.Principal{FirebaseUID: "firebase-user", AppID: "firebase-app"}, ingest.BatchScope{
		TenantID:          testTenantID,
		DeviceID:          testDeviceID,
		TripID:            testTripID,
		ClientSessionID:   testClientSession,
		InstallationID:    testInstallation,
		ConsentRevisionID: testConsentID,
	}
}
