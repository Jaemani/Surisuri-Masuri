package ingest

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const (
	ForwardRecoveryAuthorizationPolicyVersion  = "forward-recovery.current-state@1"
	ForwardRecoveryManifestRepairPolicyVersion = "manifest-repair.current-state@1"
	ForwardRecoveryArtifactReadGrantTTL        = 30 * time.Second
	MaxForwardRecoveryAuthorizationClockSkew   = 5 * time.Second
)

var (
	// ErrForwardRecoveryUnauthorized is a bounded policy denial. It deliberately
	// does not identify the missing or inactive relationship.
	ErrForwardRecoveryUnauthorized = errors.New("current forward recovery authorization denied")
	// ErrForwardRecoveryAuthorizationUnavailable covers malformed trusted state
	// and dependency failures without exposing provider or document details.
	ErrForwardRecoveryAuthorizationUnavailable = errors.New("current forward recovery authorization unavailable")
)

// ForwardRecoveryAuthorizationQuery contains only the pseudonymous control
// plane lookup keys. A caller cannot provide receipt facts or artifact paths.
type ForwardRecoveryAuthorizationQuery struct {
	TenantID       string
	ReservationKey string
}

// CurrentForwardRecoverySnapshot is the provider-neutral, transaction-coherent
// input to the system recovery policy. Firebase UID and App ID are linkage facts
// only and must never be copied into requests, grants, results, logs or reports.
type CurrentForwardRecoverySnapshot struct {
	Receipt      Receipt
	Tenant       CurrentRecoveryTenant
	Membership   CurrentRecoveryMembership
	Installation CurrentRecoveryInstallation
	Trip         CurrentRecoveryTrip
	Assignment   CurrentRecoveryDeviceAssignment
	Consent      CurrentRecoveryConsentRevision
	ConsentState CurrentRecoveryConsentState
	ReadTime     time.Time
}

type CurrentRecoveryTenant struct {
	TenantID string
	Status   string
}

type CurrentRecoveryMembership struct {
	TenantID    string
	FirebaseUID string
	PersonID    string
	Roles       []string
	Status      string
	ValidFrom   time.Time
	ValidTo     *time.Time
}

type CurrentRecoveryInstallation struct {
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

type CurrentRecoveryTrip struct {
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

type CurrentRecoveryDeviceAssignment struct {
	TenantID       string
	AssignmentID   string
	DeviceID       string
	PersonID       string
	AssignmentType string
	Status         string
	ValidFrom      time.Time
	ValidTo        *time.Time
}

type CurrentRecoveryConsentRevision struct {
	TenantID          string
	ConsentRevisionID string
	PersonID          string
	PurposeCode       string
	Status            string
	GrantedAt         *time.Time
	WithdrawnAt       *time.Time
	ExpiresAt         *time.Time
}

type CurrentRecoveryConsentState struct {
	TenantID          string
	PersonID          string
	PurposeCode       string
	CurrentRevisionID string
	Status            string
	EffectiveAt       time.Time
	ExpiresAt         *time.Time
}

type ForwardRecoveryAuthorizationStore interface {
	LoadCurrentForwardRecovery(
		context.Context,
		ForwardRecoveryAuthorizationQuery,
	) (CurrentForwardRecoverySnapshot, error)
}

// SystemRecoveryAuthorizer reevaluates current control-plane state and is the
// only production boundary that mints forward-recovery artifact read grants.
type SystemRecoveryAuthorizer struct {
	store     ForwardRecoveryAuthorizationStore
	now       func() time.Time
	validator *registeredTelemetryArtifactValidator
}

// ForwardRecoveryManifestEvidence is classifier-produced pass-1 evidence. The
// result carries an unexported request binding, so package-external callers
// cannot fabricate evidence for an arbitrary receipt revision or fence.
type ForwardRecoveryManifestEvidence struct {
	Request ArtifactClassificationRequest
	Result  ArtifactClassificationResult
}

func NewSystemRecoveryAuthorizer(
	store ForwardRecoveryAuthorizationStore,
	now func() time.Time,
) (*SystemRecoveryAuthorizer, error) {
	if store == nil {
		return nil, errors.New("forward recovery authorization store is required")
	}
	if now == nil {
		now = time.Now
	}
	validator, ok := newTelemetryArtifactContentValidator().(*registeredTelemetryArtifactValidator)
	if !ok {
		return nil, errors.New("forward recovery validator registry is required")
	}
	return &SystemRecoveryAuthorizer{store: store, now: now, validator: validator}, nil
}

// AuthorizeManifestRepair performs a fresh current-state authorization and
// mints a write-only capability for one exact canonical manifest. The read
// grant created by the shared policy path is intentionally not returned.
func (a *SystemRecoveryAuthorizer) AuthorizeManifestRepair(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
	evidence ForwardRecoveryManifestEvidence,
	write RecoveryManifestWrite,
) (ArtifactClassificationRequest, ManifestRepairAuthorizationGrant, error) {
	if a == nil || a.validator == nil {
		return ArtifactClassificationRequest{}, ManifestRepairAuthorizationGrant{}, ErrForwardRecoveryAuthorizationUnavailable
	}
	evidence.Request = cloneArtifactClassificationRequest(evidence.Request)
	evidence.Result = cloneManifestRepairClassificationResult(evidence.Result)
	write = cloneManifestRepairWrite(write)
	initialPlan, err := PlanForwardRecoveryAction(ForwardRecoveryPlanInput{
		Phase: RecoveryPhaseInitial, Request: evidence.Request, Result: evidence.Result,
	})
	if err != nil || initialPlan.Action != ForwardRecoveryActionCreateManifest || initialPlan.Raw == nil ||
		!sameArtifactLineage(*initialPlan.Raw, artifactLineageFromStored(write.Raw)) ||
		!recoveryManifestWriteMatchesRequest(write, evidence.Request) {
		return ArtifactClassificationRequest{}, ManifestRepairAuthorizationGrant{}, ErrInvalidManifestRepairAuthorization
	}
	request, readGrant, err := a.Authorize(ctx, tenantID, reservationKey, lease)
	if err != nil {
		return ArtifactClassificationRequest{}, ManifestRepairAuthorizationGrant{}, err
	}
	if canonicalArtifactClassificationRequestBinding(request) !=
		canonicalArtifactClassificationRequestBinding(evidence.Request) {
		return ArtifactClassificationRequest{}, ManifestRepairAuthorizationGrant{}, ErrInvalidManifestRepairAuthorization
	}
	grant, err := a.validator.mintManifestRepairAuthorizationGrant(
		ForwardRecoveryManifestRepairPolicyVersion,
		request,
		write,
		readGrant.checkedAt,
		readGrant.expiresAt,
	)
	if err != nil {
		return ArtifactClassificationRequest{}, ManifestRepairAuthorizationGrant{}, ErrInvalidManifestRepairAuthorization
	}
	return request, grant, nil
}

func cloneManifestRepairClassificationResult(
	result ArtifactClassificationResult,
) ArtifactClassificationResult {
	cloned := result
	if result.PinnedRaw != nil {
		raw := *result.PinnedRaw
		cloned.PinnedRaw = &raw
	}
	if result.PinnedManifest != nil {
		manifest := *result.PinnedManifest
		cloned.PinnedManifest = &manifest
	}
	return cloned
}

func cloneManifestRepairWrite(write RecoveryManifestWrite) RecoveryManifestWrite {
	write.CanonicalBody = append([]byte(nil), write.CanonicalBody...)
	return write
}

func (a *SystemRecoveryAuthorizer) Authorize(
	ctx context.Context,
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
) (ArtifactClassificationRequest, ArtifactReadAuthorizationGrant, error) {
	if a == nil || a.store == nil || a.now == nil || ctx == nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, ErrForwardRecoveryAuthorizationUnavailable
	}
	if err := validateForwardRecoveryAuthorizationInput(tenantID, reservationKey, lease); err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}
	if err := ctx.Err(); err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}

	snapshot, err := a.store.LoadCurrentForwardRecovery(ctx, ForwardRecoveryAuthorizationQuery{
		TenantID:       tenantID,
		ReservationKey: reservationKey,
	})
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, normalizeForwardRecoveryAuthorizationError(ctx, err)
	}
	snapshot = cloneCurrentForwardRecoverySnapshot(snapshot)
	if err := validateCurrentForwardRecoverySnapshotShape(snapshot); err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}

	checkedAt, err := forwardRecoveryAuthorizationTime(a.now().UTC(), snapshot.ReadTime.UTC())
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}
	if err := evaluateCurrentForwardRecovery(snapshot, tenantID, reservationKey, lease, checkedAt); err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}

	request, err := forwardRecoveryClassificationRequest(snapshot.Receipt)
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, err
	}
	expiresAt := currentForwardRecoveryGrantExpiry(snapshot, checkedAt)
	if !checkedAt.Before(expiresAt) {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, ErrForwardRecoveryUnauthorized
	}
	grant, err := mintArtifactReadAuthorizationGrant(
		artifactReadGrantIssuerForwardRecovery,
		ForwardRecoveryAuthorizationPolicyVersion,
		request,
		checkedAt,
		expiresAt,
	)
	if err != nil {
		return ArtifactClassificationRequest{}, ArtifactReadAuthorizationGrant{}, ErrForwardRecoveryAuthorizationUnavailable
	}
	return request, grant, nil
}

func validateForwardRecoveryAuthorizationInput(
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
) error {
	if !telemetry.IsUUID(tenantID) || !isLowerHexDigest(reservationKey) ||
		ValidateLeaseGrant(lease) != nil || lease.OwnerKind != LeaseOwnerSweeper {
		return ErrInvalidArtifactReadAuthorization
	}
	return nil
}

func normalizeForwardRecoveryAuthorizationError(ctx context.Context, err error) error {
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
	if errors.Is(err, ErrForwardRecoveryUnauthorized) {
		return ErrForwardRecoveryUnauthorized
	}
	return ErrForwardRecoveryAuthorizationUnavailable
}

func forwardRecoveryAuthorizationTime(applicationTime, readTime time.Time) (time.Time, error) {
	if applicationTime.IsZero() || readTime.IsZero() {
		return time.Time{}, ErrForwardRecoveryAuthorizationUnavailable
	}
	difference := applicationTime.Sub(readTime)
	if difference < 0 {
		difference = -difference
	}
	if difference > MaxForwardRecoveryAuthorizationClockSkew {
		return time.Time{}, ErrForwardRecoveryAuthorizationUnavailable
	}
	if readTime.After(applicationTime) {
		return readTime.UTC(), nil
	}
	return applicationTime.UTC(), nil
}

func validateCurrentForwardRecoverySnapshotShape(snapshot CurrentForwardRecoverySnapshot) error {
	identifiers := []string{
		snapshot.Tenant.TenantID,
		snapshot.Membership.TenantID,
		snapshot.Membership.PersonID,
		snapshot.Installation.TenantID,
		snapshot.Installation.InstallationID,
		snapshot.Trip.TenantID,
		snapshot.Trip.TripID,
		snapshot.Trip.DeviceID,
		snapshot.Trip.PersonID,
		snapshot.Trip.DeviceAssignmentID,
		snapshot.Trip.InstallationID,
		snapshot.Trip.ClientSessionID,
		snapshot.Trip.ConsentRevisionID,
		snapshot.Assignment.TenantID,
		snapshot.Assignment.AssignmentID,
		snapshot.Assignment.DeviceID,
		snapshot.Assignment.PersonID,
		snapshot.Consent.TenantID,
		snapshot.Consent.ConsentRevisionID,
		snapshot.Consent.PersonID,
		snapshot.ConsentState.TenantID,
		snapshot.ConsentState.PersonID,
		snapshot.ConsentState.CurrentRevisionID,
	}
	for _, identifier := range identifiers {
		if !telemetry.IsUUID(identifier) {
			return ErrForwardRecoveryAuthorizationUnavailable
		}
	}
	if snapshot.ReadTime.IsZero() ||
		!safeRecoveryDocumentID(snapshot.Membership.FirebaseUID, 128) ||
		!safeRecoveryDocumentID(snapshot.Installation.FirebaseUID, 128) ||
		snapshot.Installation.AppID == "" || len(snapshot.Installation.AppID) > 512 ||
		!knownRecoveryRoles(snapshot.Membership.Roles) {
		return ErrForwardRecoveryAuthorizationUnavailable
	}
	if !knownRecoveryValue(snapshot.Tenant.Status, "active", "suspended", "closed") ||
		!knownRecoveryValue(snapshot.Membership.Status, "active", "suspended", "revoked") ||
		!knownRecoveryValue(snapshot.Installation.Status, "active", "revoked") ||
		!knownRecoveryValue(snapshot.Trip.Status, "recording", "ended", "cancelled") ||
		!knownRecoveryValue(snapshot.Trip.CaptureMode, "foreground", "background", "reconciled_offline") ||
		!knownRecoveryValue(snapshot.Assignment.Status, "active", "ended", "revoked") ||
		!knownRecoveryValue(snapshot.Assignment.AssignmentType, "primary_user", "temporary_user") ||
		!knownRecoveryValue(snapshot.Consent.Status, "granted", "denied", "withdrawn", "expired") ||
		!knownRecoveryValue(snapshot.ConsentState.Status, "granted", "denied", "withdrawn", "expired") {
		return ErrForwardRecoveryAuthorizationUnavailable
	}
	if snapshot.Membership.ValidFrom.IsZero() ||
		snapshot.Installation.RegisteredAt.IsZero() ||
		snapshot.Installation.CreatedAt.IsZero() ||
		snapshot.Installation.UpdatedAt.IsZero() ||
		snapshot.Trip.StartedAt.IsZero() || snapshot.Trip.IngestExpiresAt.IsZero() ||
		snapshot.Assignment.ValidFrom.IsZero() || snapshot.ConsentState.EffectiveAt.IsZero() {
		return ErrForwardRecoveryAuthorizationUnavailable
	}
	if snapshot.Installation.SchemaVersion <= 0 || snapshot.Installation.Revision <= 0 ||
		snapshot.Installation.UpdatedAt.Before(snapshot.Installation.CreatedAt) ||
		snapshot.Installation.RegisteredAt.Before(snapshot.Installation.CreatedAt) ||
		!snapshot.Trip.StartedAt.Before(snapshot.Trip.IngestExpiresAt) ||
		(snapshot.Consent.Status == "granted" &&
			(snapshot.Consent.GrantedAt == nil || snapshot.Consent.GrantedAt.IsZero())) ||
		invalidOptionalRecoveryTime(snapshot.Membership.ValidTo) ||
		invalidOptionalRecoveryTime(snapshot.Installation.RevokedAt) ||
		invalidOptionalRecoveryTime(snapshot.Trip.EndedAt) ||
		invalidOptionalRecoveryTime(snapshot.Assignment.ValidTo) ||
		invalidOptionalRecoveryTime(snapshot.Consent.GrantedAt) ||
		invalidOptionalRecoveryTime(snapshot.Consent.WithdrawnAt) ||
		invalidOptionalRecoveryTime(snapshot.Consent.ExpiresAt) ||
		invalidOptionalRecoveryTime(snapshot.ConsentState.ExpiresAt) ||
		invalidRecoveryWindow(snapshot.Membership.ValidFrom, snapshot.Membership.ValidTo) ||
		invalidRecoveryWindow(snapshot.Assignment.ValidFrom, snapshot.Assignment.ValidTo) ||
		(snapshot.Trip.EndedAt != nil && snapshot.Trip.EndedAt.Before(snapshot.Trip.StartedAt)) ||
		(snapshot.Trip.Status == "recording" && snapshot.Trip.EndedAt != nil) ||
		(snapshot.Trip.Status == "ended" && snapshot.Trip.EndedAt == nil) {
		return ErrForwardRecoveryAuthorizationUnavailable
	}
	return nil
}

func evaluateCurrentForwardRecovery(
	snapshot CurrentForwardRecoverySnapshot,
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
	checkedAt time.Time,
) error {
	receipt := snapshot.Receipt
	if err := validateForwardRecoveryReceiptEligibility(receipt, tenantID, reservationKey, lease); err != nil {
		return err
	}
	request, err := forwardRecoveryClassificationRequest(receipt)
	if err != nil {
		return err
	}
	if !checkedAt.Before(receipt.LeaseExpiresAt) ||
		!checkedAt.Before(receipt.ReservationDeadline) {
		return ErrForwardRecoveryUnauthorized
	}
	if request.ForwardFence == nil || request.ForwardFence.OwnerID != lease.Fence.OwnerID ||
		request.ForwardFence.Token != lease.Fence.Token ||
		!request.ForwardFence.ExpiresAt.Equal(lease.Fence.ExpiresAt) {
		return ErrForwardRecoveryAuthorizationUnavailable
	}

	if snapshot.Tenant.TenantID != tenantID || snapshot.Tenant.Status != "active" {
		return ErrForwardRecoveryUnauthorized
	}
	if snapshot.Installation.TenantID != tenantID ||
		snapshot.Installation.InstallationID != receipt.InstallationID ||
		snapshot.Installation.Status != "active" || snapshot.Installation.RevokedAt != nil ||
		snapshot.Installation.RegisteredAt.After(receipt.FirstCapturedAt) {
		return ErrForwardRecoveryUnauthorized
	}
	if snapshot.Membership.TenantID != tenantID ||
		snapshot.Membership.FirebaseUID != snapshot.Installation.FirebaseUID ||
		snapshot.Membership.PersonID != snapshot.Trip.PersonID ||
		snapshot.Membership.Status != "active" ||
		!containsRecoveryValue(snapshot.Membership.Roles, "beneficiary") ||
		!activeRecoveryAt(snapshot.Membership.ValidFrom, snapshot.Membership.ValidTo, checkedAt) ||
		!activeRecoveryAt(snapshot.Membership.ValidFrom, snapshot.Membership.ValidTo, receipt.FirstCapturedAt) ||
		!activeRecoveryAt(snapshot.Membership.ValidFrom, snapshot.Membership.ValidTo, receipt.LastCapturedAt) {
		return ErrForwardRecoveryUnauthorized
	}
	if snapshot.Trip.TenantID != tenantID || snapshot.Trip.TripID != receipt.TripID ||
		snapshot.Trip.DeviceID != receipt.DeviceID ||
		snapshot.Trip.InstallationID != receipt.InstallationID ||
		snapshot.Trip.ConsentRevisionID != receipt.ConsentRevisionID ||
		(snapshot.Trip.Status != "recording" && snapshot.Trip.Status != "ended") ||
		!checkedAt.Before(snapshot.Trip.IngestExpiresAt) ||
		receipt.FirstCapturedAt.Before(snapshot.Trip.StartedAt) ||
		receipt.LastCapturedAt.After(checkedAt.Add(5*time.Minute)) ||
		(snapshot.Trip.Status == "ended" && receipt.LastCapturedAt.After(checkedAt)) ||
		(snapshot.Trip.EndedAt != nil && receipt.LastCapturedAt.After(*snapshot.Trip.EndedAt)) {
		return ErrForwardRecoveryUnauthorized
	}
	if snapshot.Trip.Status == "ended" && snapshot.Trip.EndedAt.After(checkedAt) {
		return ErrForwardRecoveryAuthorizationUnavailable
	}
	if snapshot.Assignment.TenantID != tenantID ||
		snapshot.Assignment.AssignmentID != snapshot.Trip.DeviceAssignmentID ||
		snapshot.Assignment.DeviceID != receipt.DeviceID ||
		snapshot.Assignment.PersonID != snapshot.Trip.PersonID ||
		snapshot.Assignment.Status != "active" ||
		!activeRecoveryAt(snapshot.Assignment.ValidFrom, snapshot.Assignment.ValidTo, checkedAt) ||
		!activeRecoveryAt(snapshot.Assignment.ValidFrom, snapshot.Assignment.ValidTo, receipt.FirstCapturedAt) ||
		!activeRecoveryAt(snapshot.Assignment.ValidFrom, snapshot.Assignment.ValidTo, receipt.LastCapturedAt) {
		return ErrForwardRecoveryUnauthorized
	}
	if snapshot.Consent.TenantID != tenantID ||
		snapshot.Consent.ConsentRevisionID != receipt.ConsentRevisionID ||
		snapshot.Consent.PersonID != snapshot.Trip.PersonID ||
		snapshot.Consent.PurposeCode != "precise_location" ||
		snapshot.Consent.Status != "granted" || snapshot.Consent.GrantedAt == nil ||
		snapshot.Consent.GrantedAt.After(receipt.FirstCapturedAt) ||
		snapshot.Consent.WithdrawnAt != nil ||
		expiredRecoveryAt(snapshot.Consent.ExpiresAt, checkedAt) ||
		expiredRecoveryAt(snapshot.Consent.ExpiresAt, receipt.LastCapturedAt) {
		return ErrForwardRecoveryUnauthorized
	}
	if snapshot.ConsentState.TenantID != tenantID ||
		snapshot.ConsentState.PersonID != snapshot.Trip.PersonID ||
		snapshot.ConsentState.PurposeCode != "precise_location" ||
		snapshot.ConsentState.CurrentRevisionID != receipt.ConsentRevisionID ||
		snapshot.ConsentState.Status != "granted" ||
		snapshot.ConsentState.EffectiveAt.After(receipt.FirstCapturedAt) ||
		expiredRecoveryAt(snapshot.ConsentState.ExpiresAt, checkedAt) ||
		expiredRecoveryAt(snapshot.ConsentState.ExpiresAt, receipt.LastCapturedAt) {
		return ErrForwardRecoveryUnauthorized
	}
	return nil
}

func validateForwardRecoveryReceiptEligibility(
	receipt Receipt,
	tenantID string,
	reservationKey string,
	lease LeaseGrant,
) error {
	switch receipt.State {
	case ReceiptReserved:
	case ReceiptStored, ReceiptRejected, ReceiptQueued, ReceiptProjected,
		ReceiptDeleting, ReceiptDeleted, ReceiptCleanupPending, ReceiptExpired, ReceiptRecoveryHold:
		return ErrForwardRecoveryUnauthorized
	default:
		return ErrForwardRecoveryAuthorizationUnavailable
	}
	if !telemetry.IsUUID(receipt.TenantID) || !isLowerHexDigest(receipt.ReservationKey) {
		return ErrForwardRecoveryAuthorizationUnavailable
	}
	if receipt.TenantID != tenantID || receipt.ReservationKey != reservationKey {
		return ErrForwardRecoveryUnauthorized
	}
	if !forwardRecoveryReceiptHasLease(receipt) {
		return ErrForwardRecoveryUnauthorized
	}
	receiptLease := LeaseGrant{
		Fence: LeaseFence{
			OwnerID:   receipt.LeaseOwnerID,
			Token:     receipt.FencingToken,
			ExpiresAt: receipt.LeaseExpiresAt,
		},
		OwnerKind:   receipt.LeaseOwnerKind,
		AcquiredAt:  receipt.LeaseAcquiredAt,
		HeartbeatAt: receipt.LeaseHeartbeatAt,
	}
	if ValidateLeaseGrant(receiptLease) != nil {
		return ErrForwardRecoveryAuthorizationUnavailable
	}
	if receipt.LeaseOwnerKind != LeaseOwnerSweeper {
		return ErrForwardRecoveryUnauthorized
	}
	if receipt.LeaseOwnerID != lease.Fence.OwnerID ||
		receipt.FencingToken != lease.Fence.Token ||
		!receipt.LeaseExpiresAt.Equal(lease.Fence.ExpiresAt) ||
		!receipt.LeaseAcquiredAt.Equal(lease.AcquiredAt) ||
		!receipt.LeaseHeartbeatAt.Equal(lease.HeartbeatAt) {
		return ErrForwardRecoveryUnauthorized
	}
	return nil
}

func forwardRecoveryReceiptHasLease(receipt Receipt) bool {
	return receipt.LeaseOwnerID != "" || receipt.LeaseOwnerKind != "" ||
		!receipt.LeaseAcquiredAt.IsZero() || !receipt.LeaseHeartbeatAt.IsZero() ||
		!receipt.LeaseExpiresAt.IsZero()
}

func forwardRecoveryClassificationRequest(receipt Receipt) (ArtifactClassificationRequest, error) {
	manifestInput := BatchManifestInput{
		PayloadSchemaVersion: receipt.PayloadSchemaVersion,
		TenantID:             receipt.TenantID,
		DeviceID:             receipt.DeviceID,
		TripID:               receipt.TripID,
		InstallationID:       receipt.InstallationID,
		BatchID:              receipt.BatchID,
		ClientBatchID:        receipt.ClientBatchID,
		ConsentRevisionID:    receipt.ConsentRevisionID,
		BodyHash:             receipt.BodyHash,
		SampleCount:          receipt.ExpectedSampleCount,
		FirstCapturedAt:      receipt.FirstCapturedAt,
		LastCapturedAt:       receipt.LastCapturedAt,
		ReceivedAt:           receipt.CreatedAt,
		ArtifactExpiresAt:    receipt.ArtifactExpiresAt,
		ValidatorVersion:     receipt.ValidatorVersion,
	}
	request := ArtifactClassificationRequest{
		Purpose:              ArtifactReadForwardRecovery,
		ReceiptID:            receipt.ReceiptID,
		ReservationKey:       receipt.ReservationKey,
		ReceiptState:         receipt.State,
		ReceiptRevision:      receipt.Revision,
		TenantID:             receipt.TenantID,
		DeviceID:             receipt.DeviceID,
		TripID:               receipt.TripID,
		InstallationID:       receipt.InstallationID,
		BatchID:              receipt.BatchID,
		ClientBatchID:        receipt.ClientBatchID,
		ConsentRevisionID:    receipt.ConsentRevisionID,
		PayloadSchemaVersion: receipt.PayloadSchemaVersion,
		ValidatorVersion:     receipt.ValidatorVersion,
		BodyHash:             receipt.BodyHash,
		ExpectedSampleCount:  receipt.ExpectedSampleCount,
		FirstCapturedAt:      receipt.FirstCapturedAt,
		LastCapturedAt:       receipt.LastCapturedAt,
		ReceivedAt:           receipt.CreatedAt,
		ArtifactExpiresAt:    receipt.ArtifactExpiresAt,
		ExpectedRawPath:      ExpectedTelemetryObjectPath(manifestInput),
		ExpectedManifestPath: ExpectedTelemetryManifestPath(manifestInput),
		ForwardFence: &LeaseFence{
			OwnerID:   receipt.LeaseOwnerID,
			Token:     receipt.FencingToken,
			ExpiresAt: receipt.LeaseExpiresAt,
		},
	}
	if !isLowerHexDigest(receipt.ClientBatchKey) ||
		receipt.ClientBatchKey != DeriveClientBatchKey(receipt.TenantID, receipt.ClientBatchID) ||
		receipt.State != ReceiptReserved || receipt.ReceiptID != receipt.BatchID ||
		receipt.Revision <= 0 || receipt.CreatedAt.IsZero() || receipt.UpdatedAt.IsZero() ||
		receipt.UpdatedAt.Before(receipt.CreatedAt) || receipt.ReservationDeadline.IsZero() ||
		!receipt.ReservationDeadline.Equal(receipt.CreatedAt.Add(ReservationProcessingWindow)) ||
		!receipt.ArtifactExpiresAt.Equal(receipt.CreatedAt.Add(TelemetryArtifactRetention)) ||
		!receipt.ReceiptRetentionFloor.Equal(receipt.CreatedAt.Add(ReceiptControlRetention)) ||
		!receipt.CreatedAt.Before(receipt.ReservationDeadline) ||
		!receipt.ReservationDeadline.Before(receipt.ArtifactExpiresAt) ||
		receipt.PurgeEligibleAt != nil ||
		receipt.ObjectPath != "" || receipt.ManifestPath != "" ||
		receipt.ObjectSHA256 != "" || receipt.ManifestSHA256 != "" ||
		receipt.ObjectCRC32C != 0 || receipt.ManifestCRC32C != 0 ||
		receipt.ObjectSize != 0 || receipt.ManifestSize != 0 ||
		receipt.ObjectGeneration != 0 || receipt.ObjectMetageneration != 0 ||
		receipt.ManifestGeneration != 0 || receipt.ManifestMetageneration != 0 ||
		receipt.SampleCount != 0 || receipt.RejectionCode != "" ||
		receipt.RecoveryHoldCode != "" || !receipt.RecoveryHoldReviewDueAt.IsZero() ||
		receipt.LeaseOwnerKind != LeaseOwnerSweeper ||
		receipt.LeaseAcquiredAt.IsZero() || receipt.LeaseHeartbeatAt.IsZero() ||
		receipt.LeaseHeartbeatAt.Before(receipt.LeaseAcquiredAt) ||
		!receipt.UpdatedAt.Equal(receipt.LeaseHeartbeatAt) ||
		!receipt.NextRecoveryAt.Equal(receipt.LeaseExpiresAt) ||
		receipt.RecoveryAttemptCount <= 0 || receipt.LastRecoveryCode != "" ||
		!receipt.CleanupTransitionedAt.IsZero() || !receipt.CleanupQuiescenceUntil.IsZero() ||
		receipt.CleanupMode != "" || receipt.CleanupOriginStatus != "" || receipt.CleanupPolicyVersion != "" ||
		ValidateArtifactClassificationRequest(request) != nil {
		return ArtifactClassificationRequest{}, ErrForwardRecoveryAuthorizationUnavailable
	}
	return request, nil
}

func currentForwardRecoveryGrantExpiry(
	snapshot CurrentForwardRecoverySnapshot,
	checkedAt time.Time,
) time.Time {
	expiresAt := checkedAt.Add(ForwardRecoveryArtifactReadGrantTTL)
	expiresAt = earlierRecoveryTime(expiresAt, snapshot.Receipt.LeaseExpiresAt)
	expiresAt = earlierRecoveryTime(expiresAt, snapshot.Receipt.ReservationDeadline)
	expiresAt = earlierRecoveryTime(expiresAt, snapshot.Trip.IngestExpiresAt)
	for _, optional := range []*time.Time{
		snapshot.Membership.ValidTo,
		snapshot.Assignment.ValidTo,
		snapshot.Consent.ExpiresAt,
		snapshot.ConsentState.ExpiresAt,
	} {
		if optional != nil {
			expiresAt = earlierRecoveryTime(expiresAt, *optional)
		}
	}
	return expiresAt.UTC()
}

func earlierRecoveryTime(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}

func cloneCurrentForwardRecoverySnapshot(snapshot CurrentForwardRecoverySnapshot) CurrentForwardRecoverySnapshot {
	snapshot.Membership.Roles = append([]string(nil), snapshot.Membership.Roles...)
	snapshot.Membership.ValidTo = cloneRecoveryTime(snapshot.Membership.ValidTo)
	snapshot.Installation.RevokedAt = cloneRecoveryTime(snapshot.Installation.RevokedAt)
	snapshot.Trip.EndedAt = cloneRecoveryTime(snapshot.Trip.EndedAt)
	snapshot.Assignment.ValidTo = cloneRecoveryTime(snapshot.Assignment.ValidTo)
	snapshot.Consent.GrantedAt = cloneRecoveryTime(snapshot.Consent.GrantedAt)
	snapshot.Consent.WithdrawnAt = cloneRecoveryTime(snapshot.Consent.WithdrawnAt)
	snapshot.Consent.ExpiresAt = cloneRecoveryTime(snapshot.Consent.ExpiresAt)
	snapshot.ConsentState.ExpiresAt = cloneRecoveryTime(snapshot.ConsentState.ExpiresAt)
	return snapshot
}

func cloneRecoveryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func safeRecoveryDocumentID(value string, maxLength int) bool {
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

func knownRecoveryRoles(roles []string) bool {
	if len(roles) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if !knownRecoveryValue(role, "beneficiary", "guardian", "case_worker", "repairer", "tenant_admin", "auditor") {
			return false
		}
		if _, duplicate := seen[role]; duplicate {
			return false
		}
		seen[role] = struct{}{}
	}
	return true
}

func knownRecoveryValue(value string, allowed ...string) bool {
	return containsRecoveryValue(allowed, value)
}

func containsRecoveryValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func activeRecoveryAt(from time.Time, to *time.Time, at time.Time) bool {
	return !from.After(at) && (to == nil || at.Before(*to))
}

func expiredRecoveryAt(expiresAt *time.Time, at time.Time) bool {
	return expiresAt != nil && !at.Before(*expiresAt)
}

func invalidRecoveryWindow(from time.Time, to *time.Time) bool {
	return to != nil && !from.Before(*to)
}

func invalidOptionalRecoveryTime(value *time.Time) bool {
	return value != nil && value.IsZero()
}
