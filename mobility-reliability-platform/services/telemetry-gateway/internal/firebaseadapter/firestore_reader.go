package firebaseadapter

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const maxSnapshotReadTimeout = 10 * time.Second

type getAllDocuments func(context.Context, []*firestore.DocumentRef) ([]*firestore.DocumentSnapshot, error)

// FirestoreSnapshotReader reads only the control-plane documents required to
// authorize a telemetry batch. Raw telemetry and coordinates never cross this
// adapter boundary.
type FirestoreSnapshotReader struct {
	client  *firestore.Client
	timeout time.Duration
	getAll  getAllDocuments
}

var _ authorization.SnapshotReader = (*FirestoreSnapshotReader)(nil)

// NewFirestoreSnapshotReader creates a bounded Firestore authorization reader.
// The caller owns the Firestore client and must close it during process shutdown.
func NewFirestoreSnapshotReader(
	client *firestore.Client,
	timeout time.Duration,
) (*FirestoreSnapshotReader, error) {
	if client == nil {
		return nil, errors.New("Firestore client is required")
	}
	if timeout <= 0 || timeout > maxSnapshotReadTimeout {
		return nil, errors.New("Firestore snapshot timeout must be greater than zero and at most ten seconds")
	}
	return &FirestoreSnapshotReader{
		client:  client,
		timeout: timeout,
		getAll:  client.GetAll,
	}, nil
}

func (r *FirestoreSnapshotReader) Load(
	ctx context.Context,
	principal ingest.Principal,
	scope ingest.BatchScope,
) (authorization.Snapshot, error) {
	if r == nil || r.client == nil || r.getAll == nil || r.timeout <= 0 || ctx == nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}

	readContext, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	primaryPaths, err := primaryAuthorizationPaths(principal, scope)
	if err != nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}
	primaryDocuments, err := r.readExact(readContext, primaryPaths)
	if err != nil {
		return authorization.Snapshot{}, err
	}

	var (
		tenant       firestoreTenant
		membership   firestoreMembership
		installation firestoreInstallation
		trip         firestoreTrip
		consent      firestoreConsentRevision
	)
	for index, destination := range []any{
		&tenant,
		&membership,
		&installation,
		&trip,
		&consent,
	} {
		if err := primaryDocuments[index].DataTo(destination); err != nil {
			return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
		}
	}

	relatedPaths, err := relatedAuthorizationPaths(scope.TenantID, trip.DeviceAssignmentID, trip.PersonID)
	if err != nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}
	relatedDocuments, err := r.readExact(readContext, relatedPaths)
	if err != nil {
		return authorization.Snapshot{}, err
	}

	var (
		assignment   firestoreDeviceAssignment
		consentState firestoreConsentState
	)
	if err := relatedDocuments[0].DataTo(&assignment); err != nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}
	if err := relatedDocuments[1].DataTo(&consentState); err != nil {
		return authorization.Snapshot{}, authorization.ErrSnapshotUnavailable
	}

	return assembleAuthorizationSnapshot(
		tenant,
		membership,
		installation,
		trip,
		assignment,
		consent,
		consentState,
	), nil
}

func (r *FirestoreSnapshotReader) readExact(
	ctx context.Context,
	paths []string,
) ([]*firestore.DocumentSnapshot, error) {
	references := make([]*firestore.DocumentRef, len(paths))
	for index, path := range paths {
		reference := r.client.Doc(path)
		if reference == nil {
			return nil, authorization.ErrSnapshotUnavailable
		}
		references[index] = reference
	}

	documents, err := r.getAll(ctx, references)
	if err != nil {
		return nil, mapSnapshotReadError(ctx, err)
	}
	if len(documents) != len(references) {
		return nil, authorization.ErrSnapshotUnavailable
	}
	for _, document := range documents {
		if document == nil {
			return nil, authorization.ErrSnapshotUnavailable
		}
		if !document.Exists() {
			return nil, authorization.ErrSnapshotNotFound
		}
	}
	return documents, nil
}

func primaryAuthorizationPaths(
	principal ingest.Principal,
	scope ingest.BatchScope,
) ([]string, error) {
	if !telemetry.IsUUID(scope.TenantID) ||
		!safeFirestoreSegment(principal.FirebaseUID, 128) ||
		!telemetry.IsUUID(scope.InstallationID) ||
		!telemetry.IsUUID(scope.TripID) ||
		!telemetry.IsUUID(scope.ConsentRevisionID) {
		return nil, authorization.ErrSnapshotUnavailable
	}

	tenantPrefix := "tenants/" + scope.TenantID
	return []string{
		tenantPrefix,
		tenantPrefix + "/memberships/" + principal.FirebaseUID,
		tenantPrefix + "/appInstallations/" + scope.InstallationID,
		tenantPrefix + "/trips/" + scope.TripID,
		tenantPrefix + "/consentRevisions/" + scope.ConsentRevisionID,
	}, nil
}

func relatedAuthorizationPaths(tenantID, assignmentID, personID string) ([]string, error) {
	if !telemetry.IsUUID(tenantID) ||
		!telemetry.IsUUID(assignmentID) ||
		!telemetry.IsUUID(personID) {
		return nil, authorization.ErrSnapshotUnavailable
	}

	tenantPrefix := "tenants/" + tenantID
	consentStateID := authorization.ConsentStateDocumentID(
		personID,
		authorization.PreciseLocationPurpose,
	)
	return []string{
		tenantPrefix + "/deviceAssignments/" + assignmentID,
		tenantPrefix + "/consentStates/" + consentStateID,
	}, nil
}

func mapSnapshotReadError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
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
		return authorization.ErrSnapshotUnavailable
	}
}

func safeFirestoreSegment(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength || strings.Contains(value, "/") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

type firestoreTenant struct {
	TenantID string `firestore:"tenant_id"`
	Status   string `firestore:"status"`
}

type firestoreMembership struct {
	TenantID    string     `firestore:"tenant_id"`
	FirebaseUID string     `firestore:"firebase_uid"`
	PersonID    string     `firestore:"person_id"`
	Roles       []string   `firestore:"roles"`
	Status      string     `firestore:"status"`
	ValidFrom   time.Time  `firestore:"valid_from"`
	ValidTo     *time.Time `firestore:"valid_to"`
}

type firestoreInstallation struct {
	TenantID       string     `firestore:"tenant_id"`
	InstallationID string     `firestore:"installation_id"`
	FirebaseUID    string     `firestore:"firebase_uid"`
	AppID          string     `firestore:"app_check_app_id"`
	Status         string     `firestore:"status"`
	SchemaVersion  int64      `firestore:"schema_version"`
	Revision       int64      `firestore:"revision"`
	RegisteredAt   time.Time  `firestore:"registered_at"`
	CreatedAt      time.Time  `firestore:"created_at"`
	UpdatedAt      time.Time  `firestore:"updated_at"`
	RevokedAt      *time.Time `firestore:"revoked_at"`
}

type firestoreTrip struct {
	TenantID           string     `firestore:"tenant_id"`
	TripID             string     `firestore:"trip_id"`
	DeviceID           string     `firestore:"device_id"`
	PersonID           string     `firestore:"person_id"`
	DeviceAssignmentID string     `firestore:"device_assignment_id"`
	InstallationID     string     `firestore:"installation_id"`
	ClientSessionID    string     `firestore:"client_session_id"`
	ConsentRevisionID  string     `firestore:"consent_revision_id"`
	StartedAt          time.Time  `firestore:"started_at"`
	EndedAt            *time.Time `firestore:"ended_at"`
	IngestExpiresAt    time.Time  `firestore:"ingest_expires_at"`
	CaptureMode        string     `firestore:"capture_mode"`
	Status             string     `firestore:"status"`
}

type firestoreDeviceAssignment struct {
	TenantID       string     `firestore:"tenant_id"`
	AssignmentID   string     `firestore:"assignment_id"`
	DeviceID       string     `firestore:"device_id"`
	PersonID       string     `firestore:"person_id"`
	AssignmentType string     `firestore:"assignment_type"`
	Status         string     `firestore:"status"`
	ValidFrom      time.Time  `firestore:"valid_from"`
	ValidTo        *time.Time `firestore:"valid_to"`
}

type firestoreConsentRevision struct {
	TenantID          string     `firestore:"tenant_id"`
	ConsentRevisionID string     `firestore:"consent_revision_id"`
	PersonID          string     `firestore:"person_id"`
	PurposeCode       string     `firestore:"purpose_code"`
	Status            string     `firestore:"status"`
	GrantedAt         *time.Time `firestore:"granted_at"`
	WithdrawnAt       *time.Time `firestore:"withdrawn_at"`
	ExpiresAt         *time.Time `firestore:"expires_at"`
}

type firestoreConsentState struct {
	TenantID          string     `firestore:"tenant_id"`
	PersonID          string     `firestore:"person_id"`
	PurposeCode       string     `firestore:"purpose_code"`
	CurrentRevisionID string     `firestore:"current_revision_id"`
	Status            string     `firestore:"status"`
	EffectiveAt       time.Time  `firestore:"effective_at"`
	ExpiresAt         *time.Time `firestore:"expires_at"`
}

func assembleAuthorizationSnapshot(
	tenant firestoreTenant,
	membership firestoreMembership,
	installation firestoreInstallation,
	trip firestoreTrip,
	assignment firestoreDeviceAssignment,
	consent firestoreConsentRevision,
	consentState firestoreConsentState,
) authorization.Snapshot {
	return authorization.Snapshot{
		Tenant: authorization.Tenant{
			TenantID: tenant.TenantID,
			Status:   tenant.Status,
		},
		Membership: authorization.Membership{
			TenantID:    membership.TenantID,
			FirebaseUID: membership.FirebaseUID,
			PersonID:    membership.PersonID,
			Roles:       membership.Roles,
			Status:      membership.Status,
			ValidFrom:   membership.ValidFrom,
			ValidTo:     membership.ValidTo,
		},
		Installation: authorization.Installation{
			TenantID:       installation.TenantID,
			InstallationID: installation.InstallationID,
			FirebaseUID:    installation.FirebaseUID,
			AppID:          installation.AppID,
			Status:         installation.Status,
			SchemaVersion:  installation.SchemaVersion,
			Revision:       installation.Revision,
			RegisteredAt:   installation.RegisteredAt,
			CreatedAt:      installation.CreatedAt,
			UpdatedAt:      installation.UpdatedAt,
			RevokedAt:      installation.RevokedAt,
		},
		Trip: authorization.Trip{
			TenantID:           trip.TenantID,
			TripID:             trip.TripID,
			DeviceID:           trip.DeviceID,
			PersonID:           trip.PersonID,
			DeviceAssignmentID: trip.DeviceAssignmentID,
			InstallationID:     trip.InstallationID,
			ClientSessionID:    trip.ClientSessionID,
			ConsentRevisionID:  trip.ConsentRevisionID,
			StartedAt:          trip.StartedAt,
			EndedAt:            trip.EndedAt,
			IngestExpiresAt:    trip.IngestExpiresAt,
			CaptureMode:        trip.CaptureMode,
			Status:             trip.Status,
		},
		Assignment: authorization.DeviceAssignment{
			TenantID:       assignment.TenantID,
			AssignmentID:   assignment.AssignmentID,
			DeviceID:       assignment.DeviceID,
			PersonID:       assignment.PersonID,
			AssignmentType: assignment.AssignmentType,
			Status:         assignment.Status,
			ValidFrom:      assignment.ValidFrom,
			ValidTo:        assignment.ValidTo,
		},
		Consent: authorization.ConsentRevision{
			TenantID:          consent.TenantID,
			ConsentRevisionID: consent.ConsentRevisionID,
			PersonID:          consent.PersonID,
			PurposeCode:       consent.PurposeCode,
			Status:            consent.Status,
			GrantedAt:         consent.GrantedAt,
			WithdrawnAt:       consent.WithdrawnAt,
			ExpiresAt:         consent.ExpiresAt,
		},
		ConsentState: authorization.ConsentState{
			TenantID:          consentState.TenantID,
			PersonID:          consentState.PersonID,
			PurposeCode:       consentState.PurposeCode,
			CurrentRevisionID: consentState.CurrentRevisionID,
			Status:            consentState.Status,
			EffectiveAt:       consentState.EffectiveAt,
			ExpiresAt:         consentState.ExpiresAt,
		},
	}
}
