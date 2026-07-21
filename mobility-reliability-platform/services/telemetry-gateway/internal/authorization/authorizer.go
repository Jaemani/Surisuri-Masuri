package authorization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var (
	// ErrSnapshotNotFound is returned by a reader when at least one required
	// authorization document does not exist. Absence is an ordinary denial and
	// must not reveal which relationship was missing.
	ErrSnapshotNotFound = errors.New("authorization snapshot not found")
	// ErrSnapshotUnavailable covers dependency failures and malformed trusted
	// documents. It maps to a generic 503 rather than an end-user 403.
	ErrSnapshotUnavailable = errors.New("authorization snapshot unavailable")
)

const preciseLocationPurpose = "precise_location"

const PreciseLocationPurpose = preciseLocationPurpose

// ConsentStateDocumentID creates the server-owned projection key without
// putting a person identifier or purpose string directly in a document path.
func ConsentStateDocumentID(personID, purposeCode string) string {
	digest := sha256.Sum256([]byte(personID + "\x1f" + purposeCode))
	return hex.EncodeToString(digest[:])
}

type SnapshotReader interface {
	Load(context.Context, ingest.Principal, ingest.BatchScope) (Snapshot, error)
}

type Snapshot struct {
	Tenant       Tenant
	Membership   Membership
	Installation Installation
	Trip         Trip
	Assignment   DeviceAssignment
	Consent      ConsentRevision
	ConsentState ConsentState
}

type Tenant struct {
	TenantID string
	Status   string
}

type Membership struct {
	TenantID    string
	FirebaseUID string
	PersonID    string
	Roles       []string
	Status      string
	ValidFrom   time.Time
	ValidTo     *time.Time
}

type Installation struct {
	TenantID       string
	InstallationID string
	FirebaseUID    string
	AppID          string
	Status         string
	SchemaVersion  int64
	Revision       int64
	RegisteredAt   time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	RevokedAt      *time.Time
}

type Trip struct {
	TenantID           string
	TripID             string
	DeviceID           string
	PersonID           string
	DeviceAssignmentID string
	InstallationID     string
	ClientSessionID    string
	ConsentRevisionID  string
	StartedAt          time.Time
	EndedAt            *time.Time
	IngestExpiresAt    time.Time
	CaptureMode        string
	Status             string
}

type DeviceAssignment struct {
	TenantID       string
	AssignmentID   string
	DeviceID       string
	PersonID       string
	AssignmentType string
	Status         string
	ValidFrom      time.Time
	ValidTo        *time.Time
}

type ConsentRevision struct {
	TenantID          string
	ConsentRevisionID string
	PersonID          string
	PurposeCode       string
	Status            string
	GrantedAt         *time.Time
	WithdrawnAt       *time.Time
	ExpiresAt         *time.Time
}

type ConsentState struct {
	TenantID          string
	PersonID          string
	PurposeCode       string
	CurrentRevisionID string
	Status            string
	EffectiveAt       time.Time
	ExpiresAt         *time.Time
}

type Authorizer struct {
	reader SnapshotReader
	now    func() time.Time
}

func NewAuthorizer(reader SnapshotReader, now func() time.Time) (*Authorizer, error) {
	if reader == nil {
		return nil, errors.New("authorization snapshot reader is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Authorizer{reader: reader, now: now}, nil
}

func (a *Authorizer) Authorize(
	ctx context.Context,
	principal ingest.Principal,
	scope ingest.BatchScope,
) error {
	if a == nil || a.reader == nil || a.now == nil {
		return ErrSnapshotUnavailable
	}
	if err := validateRequest(principal, scope); err != nil {
		return err
	}
	snapshot, err := a.reader.Load(ctx, principal, scope)
	if err != nil {
		switch {
		case errors.Is(err, ErrSnapshotNotFound):
			return ingest.ErrBatchUnauthorized
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return err
		default:
			return ErrSnapshotUnavailable
		}
	}
	return evaluate(principal, scope, snapshot, a.now().UTC())
}

func validateRequest(principal ingest.Principal, scope ingest.BatchScope) error {
	if !safeDocumentID(principal.FirebaseUID, 128) || principal.AppID == "" || len(principal.AppID) > 512 {
		return ingest.ErrBatchUnauthorized
	}
	for _, value := range []string{
		scope.TenantID,
		scope.DeviceID,
		scope.TripID,
		scope.ClientSessionID,
		scope.InstallationID,
		scope.ConsentRevisionID,
	} {
		if !telemetry.IsUUID(value) {
			return ingest.ErrBatchUnauthorized
		}
	}
	if scope.FirstCapturedAt.IsZero() || scope.LastCapturedAt.IsZero() || scope.LastCapturedAt.Before(scope.FirstCapturedAt) {
		return ingest.ErrBatchUnauthorized
	}
	return nil
}

func evaluate(
	principal ingest.Principal,
	scope ingest.BatchScope,
	s Snapshot,
	now time.Time,
) error {
	if now.IsZero() {
		return ErrSnapshotUnavailable
	}
	if err := validateDocumentShapes(s, now); err != nil {
		return err
	}
	if s.Tenant.Status != "active" || s.Tenant.TenantID != scope.TenantID {
		return ingest.ErrBatchUnauthorized
	}
	if s.Membership.TenantID != scope.TenantID ||
		s.Membership.FirebaseUID != principal.FirebaseUID ||
		s.Membership.Status != "active" ||
		!activeAt(s.Membership.ValidFrom, s.Membership.ValidTo, now) ||
		!activeAt(s.Membership.ValidFrom, s.Membership.ValidTo, scope.FirstCapturedAt) ||
		!activeAt(s.Membership.ValidFrom, s.Membership.ValidTo, scope.LastCapturedAt) ||
		!contains(s.Membership.Roles, "beneficiary") {
		return ingest.ErrBatchUnauthorized
	}
	if s.Installation.TenantID != scope.TenantID ||
		s.Installation.InstallationID != scope.InstallationID ||
		s.Installation.FirebaseUID != principal.FirebaseUID ||
		s.Installation.AppID != principal.AppID ||
		s.Installation.Status != "active" ||
		s.Installation.RegisteredAt.After(scope.FirstCapturedAt) ||
		s.Installation.RevokedAt != nil {
		return ingest.ErrBatchUnauthorized
	}
	if s.Trip.TenantID != scope.TenantID ||
		s.Trip.TripID != scope.TripID ||
		s.Trip.DeviceID != scope.DeviceID ||
		s.Trip.InstallationID != scope.InstallationID ||
		s.Trip.ClientSessionID != scope.ClientSessionID ||
		s.Trip.ConsentRevisionID != scope.ConsentRevisionID ||
		s.Trip.PersonID != s.Membership.PersonID ||
		(s.Trip.Status != "recording" && s.Trip.Status != "ended") ||
		!now.Before(s.Trip.IngestExpiresAt) {
		return ingest.ErrBatchUnauthorized
	}
	if scope.FirstCapturedAt.Before(s.Trip.StartedAt) ||
		scope.LastCapturedAt.After(now.Add(5*time.Minute)) ||
		(s.Trip.Status == "ended" && scope.LastCapturedAt.After(now)) ||
		(s.Trip.EndedAt != nil && scope.LastCapturedAt.After(*s.Trip.EndedAt)) {
		return ingest.ErrBatchUnauthorized
	}
	if s.Trip.Status == "ended" && s.Trip.EndedAt == nil {
		return ErrSnapshotUnavailable
	}
	if s.Assignment.TenantID != scope.TenantID ||
		s.Assignment.AssignmentID != s.Trip.DeviceAssignmentID ||
		s.Assignment.DeviceID != scope.DeviceID ||
		s.Assignment.PersonID != s.Trip.PersonID ||
		s.Assignment.Status != "active" ||
		!activeAt(s.Assignment.ValidFrom, s.Assignment.ValidTo, now) ||
		!activeAt(s.Assignment.ValidFrom, s.Assignment.ValidTo, scope.FirstCapturedAt) ||
		!activeAt(s.Assignment.ValidFrom, s.Assignment.ValidTo, scope.LastCapturedAt) {
		return ingest.ErrBatchUnauthorized
	}
	if s.Consent.TenantID != scope.TenantID ||
		s.Consent.ConsentRevisionID != scope.ConsentRevisionID ||
		s.Consent.PersonID != s.Trip.PersonID ||
		s.Consent.PurposeCode != preciseLocationPurpose ||
		s.Consent.Status != "granted" ||
		s.Consent.GrantedAt == nil ||
		s.Consent.GrantedAt.After(scope.FirstCapturedAt) ||
		s.Consent.WithdrawnAt != nil ||
		expired(s.Consent.ExpiresAt, now) ||
		expired(s.Consent.ExpiresAt, scope.LastCapturedAt) {
		return ingest.ErrBatchUnauthorized
	}
	if s.ConsentState.TenantID != scope.TenantID ||
		s.ConsentState.PersonID != s.Trip.PersonID ||
		s.ConsentState.PurposeCode != preciseLocationPurpose ||
		s.ConsentState.CurrentRevisionID != scope.ConsentRevisionID ||
		s.ConsentState.Status != "granted" ||
		s.ConsentState.EffectiveAt.After(scope.FirstCapturedAt) ||
		expired(s.ConsentState.ExpiresAt, now) ||
		expired(s.ConsentState.ExpiresAt, scope.LastCapturedAt) {
		return ingest.ErrBatchUnauthorized
	}
	return nil
}

func validateDocumentShapes(s Snapshot, now time.Time) error {
	for _, value := range []string{
		s.Tenant.TenantID,
		s.Membership.TenantID,
		s.Installation.TenantID,
		s.Installation.InstallationID,
		s.Trip.TenantID,
		s.Trip.TripID,
		s.Trip.DeviceID,
		s.Trip.PersonID,
		s.Trip.DeviceAssignmentID,
		s.Trip.InstallationID,
		s.Trip.ClientSessionID,
		s.Trip.ConsentRevisionID,
		s.Assignment.TenantID,
		s.Assignment.AssignmentID,
		s.Assignment.DeviceID,
		s.Assignment.PersonID,
		s.Consent.TenantID,
		s.Consent.ConsentRevisionID,
		s.Consent.PersonID,
		s.ConsentState.TenantID,
		s.ConsentState.PersonID,
		s.ConsentState.CurrentRevisionID,
	} {
		if !telemetry.IsUUID(value) {
			return ErrSnapshotUnavailable
		}
	}
	if !safeDocumentID(s.Membership.FirebaseUID, 128) ||
		!safeDocumentID(s.Installation.FirebaseUID, 128) ||
		s.Installation.AppID == "" || len(s.Installation.AppID) > 512 {
		return ErrSnapshotUnavailable
	}
	if contains(s.Membership.Roles, "beneficiary") && !telemetry.IsUUID(s.Membership.PersonID) {
		return ErrSnapshotUnavailable
	}
	if !known(s.Tenant.Status, "active", "suspended", "closed") ||
		!knownRoles(s.Membership.Roles) ||
		!known(s.Membership.Status, "active", "suspended", "revoked") ||
		!known(s.Installation.Status, "active", "revoked") ||
		!known(s.Trip.Status, "recording", "ended", "cancelled") ||
		!known(s.Trip.CaptureMode, "foreground", "background", "reconciled_offline") ||
		!known(s.Assignment.Status, "active", "ended", "revoked") ||
		!known(s.Assignment.AssignmentType, "primary_user", "temporary_user") ||
		!known(s.Consent.Status, "granted", "denied", "withdrawn", "expired") ||
		!known(s.ConsentState.Status, "granted", "denied", "withdrawn", "expired") {
		return ErrSnapshotUnavailable
	}
	if s.Membership.ValidFrom.IsZero() ||
		s.Installation.RegisteredAt.IsZero() ||
		s.Installation.CreatedAt.IsZero() ||
		s.Installation.UpdatedAt.IsZero() ||
		s.Trip.StartedAt.IsZero() ||
		s.Trip.IngestExpiresAt.IsZero() ||
		s.Assignment.ValidFrom.IsZero() ||
		s.ConsentState.EffectiveAt.IsZero() {
		return ErrSnapshotUnavailable
	}
	if s.Installation.SchemaVersion <= 0 ||
		s.Installation.Revision <= 0 ||
		s.Installation.UpdatedAt.Before(s.Installation.CreatedAt) ||
		s.Installation.RegisteredAt.Before(s.Installation.CreatedAt) ||
		(s.Consent.Status == "granted" && (s.Consent.GrantedAt == nil || s.Consent.GrantedAt.IsZero())) {
		return ErrSnapshotUnavailable
	}
	if invalidWindow(s.Membership.ValidFrom, s.Membership.ValidTo) ||
		invalidWindow(s.Assignment.ValidFrom, s.Assignment.ValidTo) ||
		(s.Trip.EndedAt != nil && s.Trip.EndedAt.Before(s.Trip.StartedAt)) ||
		(s.Trip.Status == "recording" && s.Trip.EndedAt != nil) ||
		(s.Trip.Status == "ended" && (s.Trip.EndedAt == nil || s.Trip.EndedAt.After(now))) {
		return ErrSnapshotUnavailable
	}
	return nil
}

func safeDocumentID(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength || strings.Contains(value, "/") {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func knownRoles(roles []string) bool {
	if len(roles) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if !known(role, "beneficiary", "guardian", "case_worker", "repairer", "tenant_admin", "auditor") {
			return false
		}
		if _, duplicate := seen[role]; duplicate {
			return false
		}
		seen[role] = struct{}{}
	}
	return true
}

func known(value string, allowed ...string) bool {
	return contains(allowed, value)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func activeAt(from time.Time, to *time.Time, at time.Time) bool {
	return !from.After(at) && (to == nil || at.Before(*to))
}

func expired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt != nil && !now.Before(*expiresAt)
}

func invalidWindow(from time.Time, to *time.Time) bool {
	return to != nil && !from.Before(*to)
}
