package firebaseadapter

import (
	"context"
	"errors"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

var _ ingest.ForwardRecoveryAuthorizationStore = (*FirestoreAdmissionStore)(nil)

// forwardRecoveryAuthorizationTransaction is deliberately narrower than the
// admission transaction interface. Existing admission fakes cannot silently
// authorize artifact reads merely because they can mutate receipts.
type forwardRecoveryAuthorizationTransaction interface {
	LoadCurrentForwardRecoveryRelations(
		context.Context,
		firestoreIngestReceipt,
	) (ingest.CurrentForwardRecoverySnapshot, error)
}

// LoadCurrentForwardRecovery reads the authoritative receipt linkage and all
// current authorization relations in one read-only Firestore transaction. It
// returns facts only; the ingest domain authorizer owns policy and grant minting.
func (s *FirestoreAdmissionStore) LoadCurrentForwardRecovery(
	ctx context.Context,
	query ingest.ForwardRecoveryAuthorizationQuery,
) (ingest.CurrentForwardRecoverySnapshot, error) {
	if s == nil || s.runTransaction == nil || ctx == nil ||
		!telemetry.IsUUID(query.TenantID) || !lowerHexDigest(query.ReservationKey) {
		return ingest.CurrentForwardRecoverySnapshot{}, ingest.ErrForwardRecoveryAuthorizationUnavailable
	}

	var result ingest.CurrentForwardRecoverySnapshot
	err := s.runTransaction(ctx, func(runContext context.Context, transaction admissionTransaction) error {
		result = ingest.CurrentForwardRecoverySnapshot{}
		linked, loadErr := loadLinkedReceipt(
			runContext,
			transaction,
			query.TenantID,
			query.ReservationKey,
		)
		if loadErr != nil {
			return loadErr
		}
		if linked.Receipt.Receipt.TenantID != query.TenantID ||
			linked.Receipt.Receipt.ReservationKey != query.ReservationKey {
			return ingest.ErrForwardRecoveryAuthorizationUnavailable
		}

		relationReader, ok := transaction.(forwardRecoveryAuthorizationTransaction)
		if !ok {
			return ingest.ErrForwardRecoveryAuthorizationUnavailable
		}
		snapshot, relationErr := relationReader.LoadCurrentForwardRecoveryRelations(
			runContext,
			linked.Receipt.Receipt,
		)
		if relationErr != nil {
			return relationErr
		}
		if !withinAdmissionClockSkew(linked.Receipt.ReadTime, snapshot.ReadTime) {
			return ingest.ErrForwardRecoveryAuthorizationUnavailable
		}
		if linked.Receipt.ReadTime.After(snapshot.ReadTime) {
			snapshot.ReadTime = linked.Receipt.ReadTime.UTC()
		}
		snapshot.Receipt = linked.Receipt.Receipt.toDomain()
		result = snapshot
		return nil
	})
	if err != nil {
		return ingest.CurrentForwardRecoverySnapshot{}, normalizeForwardRecoveryStoreError(ctx, err)
	}
	if result.Receipt.ReceiptID == "" || result.ReadTime.IsZero() {
		return ingest.CurrentForwardRecoverySnapshot{}, ingest.ErrForwardRecoveryAuthorizationUnavailable
	}
	return result, nil
}

func (transaction firestoreAdmissionTransaction) LoadCurrentForwardRecoveryRelations(
	ctx context.Context,
	receipt firestoreIngestReceipt,
) (ingest.CurrentForwardRecoverySnapshot, error) {
	primaryPaths, err := currentForwardRecoveryPrimaryPaths(receipt)
	if err != nil {
		return ingest.CurrentForwardRecoverySnapshot{}, err
	}
	primaryDocuments, err := transaction.readExact(ctx, primaryPaths)
	if err != nil {
		return ingest.CurrentForwardRecoverySnapshot{}, err
	}
	readTime, err := coherentSnapshotReadTime(primaryDocuments)
	if err != nil {
		return ingest.CurrentForwardRecoverySnapshot{}, err
	}

	var tenant firestoreTenant
	var installation firestoreInstallation
	var trip firestoreTrip
	var consent firestoreConsentRevision
	for index, destination := range []any{&tenant, &installation, &trip, &consent} {
		if dataErr := primaryDocuments[index].DataTo(destination); dataErr != nil {
			return ingest.CurrentForwardRecoverySnapshot{}, authorization.ErrSnapshotUnavailable
		}
	}

	relatedPaths, err := currentForwardRecoveryRelatedPaths(
		receipt.TenantID,
		installation.FirebaseUID,
		trip.DeviceAssignmentID,
		trip.PersonID,
	)
	if err != nil {
		return ingest.CurrentForwardRecoverySnapshot{}, err
	}
	relatedDocuments, err := transaction.readExact(ctx, relatedPaths)
	if err != nil {
		return ingest.CurrentForwardRecoverySnapshot{}, err
	}
	relatedReadTime, err := coherentSnapshotReadTime(relatedDocuments)
	if err != nil || !withinAdmissionClockSkew(readTime, relatedReadTime) {
		return ingest.CurrentForwardRecoverySnapshot{}, authorization.ErrSnapshotUnavailable
	}
	if relatedReadTime.After(readTime) {
		readTime = relatedReadTime
	}

	var membership firestoreMembership
	var assignment firestoreDeviceAssignment
	var consentState firestoreConsentState
	for index, destination := range []any{&membership, &assignment, &consentState} {
		if dataErr := relatedDocuments[index].DataTo(destination); dataErr != nil {
			return ingest.CurrentForwardRecoverySnapshot{}, authorization.ErrSnapshotUnavailable
		}
	}

	return assembleCurrentForwardRecoverySnapshot(
		tenant,
		membership,
		installation,
		trip,
		assignment,
		consent,
		consentState,
		readTime,
	), nil
}

func currentForwardRecoveryPrimaryPaths(receipt firestoreIngestReceipt) ([]string, error) {
	if !telemetry.IsUUID(receipt.TenantID) ||
		!telemetry.IsUUID(receipt.InstallationID) ||
		!telemetry.IsUUID(receipt.TripID) ||
		!telemetry.IsUUID(receipt.ConsentRevisionID) {
		return nil, authorization.ErrSnapshotUnavailable
	}
	tenantPrefix := "tenants/" + receipt.TenantID
	return []string{
		tenantPrefix,
		tenantPrefix + "/appInstallations/" + receipt.InstallationID,
		tenantPrefix + "/trips/" + receipt.TripID,
		tenantPrefix + "/consentRevisions/" + receipt.ConsentRevisionID,
	}, nil
}

func currentForwardRecoveryRelatedPaths(
	tenantID string,
	firebaseUID string,
	assignmentID string,
	personID string,
) ([]string, error) {
	if !telemetry.IsUUID(tenantID) || !safeFirestoreSegment(firebaseUID, 128) ||
		!telemetry.IsUUID(assignmentID) || !telemetry.IsUUID(personID) {
		return nil, authorization.ErrSnapshotUnavailable
	}
	tenantPrefix := "tenants/" + tenantID
	consentStateID := authorization.ConsentStateDocumentID(
		personID,
		authorization.PreciseLocationPurpose,
	)
	return []string{
		tenantPrefix + "/memberships/" + firebaseUID,
		tenantPrefix + "/deviceAssignments/" + assignmentID,
		tenantPrefix + "/consentStates/" + consentStateID,
	}, nil
}

func assembleCurrentForwardRecoverySnapshot(
	tenant firestoreTenant,
	membership firestoreMembership,
	installation firestoreInstallation,
	trip firestoreTrip,
	assignment firestoreDeviceAssignment,
	consent firestoreConsentRevision,
	consentState firestoreConsentState,
	readTime time.Time,
) ingest.CurrentForwardRecoverySnapshot {
	return ingest.CurrentForwardRecoverySnapshot{
		Tenant: ingest.CurrentRecoveryTenant{
			TenantID: tenant.TenantID,
			Status:   tenant.Status,
		},
		Membership: ingest.CurrentRecoveryMembership{
			TenantID:    membership.TenantID,
			FirebaseUID: membership.FirebaseUID,
			PersonID:    membership.PersonID,
			Roles:       append([]string(nil), membership.Roles...),
			Status:      membership.Status,
			ValidFrom:   membership.ValidFrom.UTC(),
			ValidTo:     cloneOptionalTime(membership.ValidTo),
		},
		Installation: ingest.CurrentRecoveryInstallation{
			TenantID:       installation.TenantID,
			InstallationID: installation.InstallationID,
			FirebaseUID:    installation.FirebaseUID,
			AppID:          installation.AppID,
			Status:         installation.Status,
			SchemaVersion:  installation.SchemaVersion,
			Revision:       installation.Revision,
			RegisteredAt:   installation.RegisteredAt.UTC(),
			CreatedAt:      installation.CreatedAt.UTC(),
			UpdatedAt:      installation.UpdatedAt.UTC(),
			RevokedAt:      cloneOptionalTime(installation.RevokedAt),
		},
		Trip: ingest.CurrentRecoveryTrip{
			TenantID:           trip.TenantID,
			TripID:             trip.TripID,
			DeviceID:           trip.DeviceID,
			PersonID:           trip.PersonID,
			DeviceAssignmentID: trip.DeviceAssignmentID,
			InstallationID:     trip.InstallationID,
			ClientSessionID:    trip.ClientSessionID,
			ConsentRevisionID:  trip.ConsentRevisionID,
			StartedAt:          trip.StartedAt.UTC(),
			EndedAt:            cloneOptionalTime(trip.EndedAt),
			IngestExpiresAt:    trip.IngestExpiresAt.UTC(),
			CaptureMode:        trip.CaptureMode,
			Status:             trip.Status,
		},
		Assignment: ingest.CurrentRecoveryDeviceAssignment{
			TenantID:       assignment.TenantID,
			AssignmentID:   assignment.AssignmentID,
			DeviceID:       assignment.DeviceID,
			PersonID:       assignment.PersonID,
			AssignmentType: assignment.AssignmentType,
			Status:         assignment.Status,
			ValidFrom:      assignment.ValidFrom.UTC(),
			ValidTo:        cloneOptionalTime(assignment.ValidTo),
		},
		Consent: ingest.CurrentRecoveryConsentRevision{
			TenantID:          consent.TenantID,
			ConsentRevisionID: consent.ConsentRevisionID,
			PersonID:          consent.PersonID,
			PurposeCode:       consent.PurposeCode,
			Status:            consent.Status,
			GrantedAt:         cloneOptionalTime(consent.GrantedAt),
			WithdrawnAt:       cloneOptionalTime(consent.WithdrawnAt),
			ExpiresAt:         cloneOptionalTime(consent.ExpiresAt),
		},
		ConsentState: ingest.CurrentRecoveryConsentState{
			TenantID:          consentState.TenantID,
			PersonID:          consentState.PersonID,
			PurposeCode:       consentState.PurposeCode,
			CurrentRevisionID: consentState.CurrentRevisionID,
			Status:            consentState.Status,
			EffectiveAt:       consentState.EffectiveAt.UTC(),
			ExpiresAt:         cloneOptionalTime(consentState.ExpiresAt),
		},
		ReadTime: readTime.UTC(),
	}
}

func normalizeForwardRecoveryStoreError(ctx context.Context, err error) error {
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
	if errors.Is(err, authorization.ErrSnapshotNotFound) ||
		errors.Is(err, ingest.ErrForwardRecoveryUnauthorized) {
		return ingest.ErrForwardRecoveryUnauthorized
	}
	return ingest.ErrForwardRecoveryAuthorizationUnavailable
}
