package firebaseadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/authorization"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func emulatorForwardRecoveryDispositionCheckedAt(
	t *testing.T,
	grant ingest.ForwardRecoveryDispositionGrant,
	command ingest.ForwardRecoveryDispositionCommand,
) time.Time {
	t.Helper()
	deadline, err := ingest.ForwardRecoveryDispositionAuthorizationDeadline(grant, command)
	if err != nil {
		t.Fatalf("ForwardRecoveryDispositionAuthorizationDeadline() = %v", err)
	}
	checkedAt := deadline.Add(-ingest.ForwardRecoveryArtifactReadGrantTTL)
	if err := ingest.ValidateForwardRecoveryDispositionAuthorization(grant, command, checkedAt); err != nil {
		t.Fatalf("disposition fixture grant is not TTL-bound: %v", err)
	}
	return checkedAt
}

func emulatorForwardRecoveryOutcomeCheckedAt(
	t *testing.T,
	grant ingest.ForwardRecoveryOutcomeReadGrant,
	query ingest.ForwardRecoveryOutcomeQuery,
) time.Time {
	t.Helper()
	deadline, err := ingest.ForwardRecoveryOutcomeAuthorizationDeadline(grant, query)
	if err != nil {
		t.Fatalf("ForwardRecoveryOutcomeAuthorizationDeadline() = %v", err)
	}
	checkedAt := deadline.Add(-ingest.ForwardRecoveryOutcomeGrantTTL)
	if err := ingest.ValidateForwardRecoveryOutcomeAuthorization(grant, query, checkedAt); err != nil {
		t.Fatalf("outcome fixture grant is not TTL-bound: %v", err)
	}
	return checkedAt
}

func TestFirestoreAdmissionStoreEmulatorCommitsAuthorizationDispositionsAtomically(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(*testing.T, *firestore.Client)
		wantDisposition ingest.ForwardRecoveryAuthorizationDisposition
		wantState       ingest.ReceiptState
		wantOutcome     ingest.RecoveryAttemptOutcome
	}{
		{
			name: "withdrawn current consent holds",
			mutate: func(t *testing.T, client *firestore.Client) {
				t.Helper()
				consentStateID := authorization.ConsentStateDocumentID(
					emulatorPersonID, authorization.PreciseLocationPurpose,
				)
				if _, err := client.Doc(
					"tenants/"+emulatorTenantID+"/consentStates/"+consentStateID,
				).Update(context.Background(), []firestore.Update{{Path: "status", Value: "withdrawn"}}); err != nil {
					t.Fatalf("withdraw consent: %v", err)
				}
			},
			wantDisposition: ingest.ForwardRecoveryAuthorizationDenied,
			wantState:       ingest.ReceiptRecoveryHold,
			wantOutcome:     ingest.RecoveryAttemptOutcomeHold,
		},
		{
			name: "readable malformed installation releases",
			mutate: func(t *testing.T, client *firestore.Client) {
				t.Helper()
				if _, err := client.Doc(
					"tenants/"+emulatorTenantID+"/appInstallations/"+emulatorInstallationID,
				).Update(context.Background(), []firestore.Update{{Path: "revision", Value: int64(0)}}); err != nil {
					t.Fatalf("malform installation revision: %v", err)
				}
			},
			wantDisposition: ingest.ForwardRecoveryAuthorizationUnavailable,
			wantState:       ingest.ReceiptReserved,
			wantOutcome:     ingest.RecoveryAttemptOutcomeLeaseReleased,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEmulatorForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
			test.mutate(t, fixture.client)
			authorizer, err := ingest.NewSystemRecoveryAuthorizer(fixture.store, time.Now)
			if err != nil {
				t.Fatalf("NewSystemRecoveryAuthorizer() = %v", err)
			}
			command, grant, err := authorizer.AuthorizeForwardRecoveryDisposition(
				context.Background(), fixture.command.TenantID, fixture.command.ReservationKey,
				fixture.lease, fixture.command.Attempt,
			)
			if err != nil || command.Disposition != test.wantDisposition {
				t.Fatalf("AuthorizeForwardRecoveryDisposition() = %#v, %v", command, err)
			}
			got, err := fixture.store.CommitForwardRecoveryDisposition(
				context.Background(), grant, command,
				emulatorForwardRecoveryDispositionCheckedAt(t, grant, command),
			)
			if err != nil {
				t.Fatalf("CommitForwardRecoveryDisposition() = %v", err)
			}
			persistedReceipt := readAdmissionEmulatorReceipt(
				t, fixture.client, command.TenantID, got.ReceiptID,
			)
			persistedAttempt := readAdmissionEmulatorAttempt(
				t, fixture.client, command.TenantID, got.ReceiptID, command.Attempt.ID,
			)
			if persistedReceipt.State != test.wantState ||
				persistedReceipt.Revision != command.ReceiptRevision+1 ||
				persistedAttempt.Status != ingest.RecoveryAttemptCompleted ||
				persistedAttempt.DecisionDomain != ingest.ForwardRecoveryDecisionCurrentAuthorization ||
				persistedAttempt.AuthorizationDisposition != test.wantDisposition ||
				persistedAttempt.Outcome != test.wantOutcome ||
				!persistedAttempt.CompletedAt.Equal(persistedReceipt.UpdatedAt) ||
				receiptHasLeaseFields(persistedReceipt) {
				t.Fatalf("receipt/attempt = %#v / %#v", persistedReceipt, persistedAttempt)
			}
			if persistedReceipt.ObjectPath != "" || persistedReceipt.ManifestPath != "" ||
				persistedReceipt.ObjectSHA256 != "" || persistedReceipt.ManifestSHA256 != "" ||
				persistedReceipt.SampleCount != 0 || persistedAttempt.RawSHA256 != "" ||
				persistedAttempt.ManifestSHA256 != "" {
				t.Fatalf("disposition mutated artifact lineage = %#v / %#v", persistedReceipt, persistedAttempt)
			}

			query, err := ingest.ForwardRecoveryOutcomeQueryForDisposition(command)
			if err != nil {
				t.Fatalf("ForwardRecoveryOutcomeQueryForDisposition() = %v", err)
			}
			outcomeAuthorizer, err := ingest.NewSystemRecoveryOutcomeAuthorizer(fixture.store, time.Now)
			if err != nil {
				t.Fatalf("NewSystemRecoveryOutcomeAuthorizer() = %v", err)
			}
			outcomeGrant, err := outcomeAuthorizer.Authorize(context.Background(), query)
			if err != nil {
				t.Fatalf("outcome Authorize() = %v", err)
			}
			outcome, err := fixture.store.GetForwardRecoveryActionOutcome(
				context.Background(), outcomeGrant, query,
				emulatorForwardRecoveryOutcomeCheckedAt(t, outcomeGrant, query),
			)
			if err != nil || outcome.CommitStatus != ingest.RecoveryActionCommitted ||
				outcome.Outcome != test.wantOutcome || outcome.ActionHash != query.ExpectedActionHash {
				t.Fatalf("GetForwardRecoveryActionOutcome() = %#v, %v", outcome, err)
			}
			unchanged := readAdmissionEmulatorReceipt(
				t, fixture.client, command.TenantID, got.ReceiptID,
			)
			unchangedAttempt := readAdmissionEmulatorAttempt(
				t, fixture.client, command.TenantID, got.ReceiptID, command.Attempt.ID,
			)
			if unchanged.Revision != persistedReceipt.Revision ||
				!unchanged.UpdatedAt.Equal(persistedReceipt.UpdatedAt) ||
				unchangedAttempt.Status != persistedAttempt.Status ||
				unchangedAttempt.ActionHash != persistedAttempt.ActionHash ||
				unchangedAttempt.DecisionDomain != persistedAttempt.DecisionDomain ||
				unchangedAttempt.AuthorizationDisposition != persistedAttempt.AuthorizationDisposition ||
				!unchangedAttempt.CompletedAt.Equal(persistedAttempt.CompletedAt) {
				t.Fatalf("outcome read replayed mutation = %#v / %#v", unchanged, unchangedAttempt)
			}
		})
	}
}

func TestFirestoreAdmissionStoreEmulatorDispositionChangeBeforeCommitWritesZero(t *testing.T) {
	consentStateID := authorization.ConsentStateDocumentID(
		emulatorPersonID, authorization.PreciseLocationPurpose,
	)
	consentStatePath := "tenants/" + emulatorTenantID + "/consentStates/" + consentStateID
	installationPath := "tenants/" + emulatorTenantID + "/appInstallations/" + emulatorInstallationID
	tests := []struct {
		name   string
		setup  func(*testing.T, *firestore.Client)
		change func(*testing.T, *firestore.Client)
	}{
		{
			name: "denied to allowed",
			setup: func(t *testing.T, client *firestore.Client) {
				updateEmulatorField(t, client, consentStatePath, "status", "withdrawn")
			},
			change: func(t *testing.T, client *firestore.Client) {
				updateEmulatorField(t, client, consentStatePath, "status", "granted")
			},
		},
		{
			name: "unavailable to allowed",
			setup: func(t *testing.T, client *firestore.Client) {
				updateEmulatorField(t, client, installationPath, "revision", int64(0))
			},
			change: func(t *testing.T, client *firestore.Client) {
				updateEmulatorField(t, client, installationPath, "revision", int64(1))
			},
		},
		{
			name: "unavailable to denied",
			setup: func(t *testing.T, client *firestore.Client) {
				updateEmulatorField(t, client, installationPath, "revision", int64(0))
			},
			change: func(t *testing.T, client *firestore.Client) {
				updateEmulatorField(t, client, installationPath, "revision", int64(1))
				updateEmulatorField(t, client, consentStatePath, "status", "withdrawn")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEmulatorForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
			test.setup(t, fixture.client)
			authorizer, err := ingest.NewSystemRecoveryAuthorizer(fixture.store, time.Now)
			if err != nil {
				t.Fatalf("NewSystemRecoveryAuthorizer() = %v", err)
			}
			command, grant, err := authorizer.AuthorizeForwardRecoveryDisposition(
				context.Background(), fixture.command.TenantID, fixture.command.ReservationKey,
				fixture.lease, fixture.command.Attempt,
			)
			if err != nil {
				t.Fatalf("AuthorizeForwardRecoveryDisposition() = %v", err)
			}
			before := readAdmissionEmulatorReceipt(
				t, fixture.client, command.TenantID, fixture.receiptID,
			)
			test.change(t, fixture.client)
			_, err = fixture.store.CommitForwardRecoveryDisposition(
				context.Background(), grant, command,
				emulatorForwardRecoveryDispositionCheckedAt(t, grant, command),
			)
			if !errors.Is(err, ingest.ErrInvalidForwardRecoveryDispositionAuthorization) {
				t.Fatalf("CommitForwardRecoveryDisposition() = %v", err)
			}
			after := readAdmissionEmulatorReceipt(t, fixture.client, command.TenantID, fixture.receiptID)
			attempt := readAdmissionEmulatorAttempt(
				t, fixture.client, command.TenantID, fixture.receiptID, command.Attempt.ID,
			)
			if after.Revision != before.Revision || after.State != before.State ||
				after.LeaseOwnerID != before.LeaseOwnerID || attempt.Status != ingest.RecoveryAttemptStarted ||
				attempt.DecisionDomain != "" || !attempt.CompletedAt.IsZero() {
				t.Fatalf("changed disposition mutated receipt/attempt = %#v / %#v", after, attempt)
			}
		})
	}
}

func TestFirestoreAdmissionStoreEmulatorMissingDispositionAttemptRollsBackReceipt(t *testing.T) {
	fixture := newEmulatorForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkStored)
	consentStateID := authorization.ConsentStateDocumentID(
		emulatorPersonID, authorization.PreciseLocationPurpose,
	)
	updateEmulatorField(
		t, fixture.client,
		"tenants/"+emulatorTenantID+"/consentStates/"+consentStateID,
		"status", "withdrawn",
	)
	authorizer, err := ingest.NewSystemRecoveryAuthorizer(fixture.store, time.Now)
	if err != nil {
		t.Fatalf("NewSystemRecoveryAuthorizer() = %v", err)
	}
	command, grant, err := authorizer.AuthorizeForwardRecoveryDisposition(
		context.Background(), fixture.command.TenantID, fixture.command.ReservationKey,
		fixture.lease, fixture.command.Attempt,
	)
	if err != nil {
		t.Fatalf("AuthorizeForwardRecoveryDisposition() = %v", err)
	}
	before := readAdmissionEmulatorReceipt(t, fixture.client, command.TenantID, fixture.receiptID)
	if _, err := fixture.client.Doc(recoveryAttemptDocumentPath(
		command.TenantID, fixture.receiptID, command.Attempt.ID,
	)).Delete(context.Background()); err != nil {
		t.Fatalf("delete recovery attempt: %v", err)
	}
	_, err = fixture.store.CommitForwardRecoveryDisposition(
		context.Background(), grant, command,
		emulatorForwardRecoveryDispositionCheckedAt(t, grant, command),
	)
	if !errors.Is(err, ingest.ErrInvalidForwardRecoveryDispositionAuthorization) {
		t.Fatalf("CommitForwardRecoveryDisposition() = %v", err)
	}
	after := readAdmissionEmulatorReceipt(t, fixture.client, command.TenantID, fixture.receiptID)
	if after.Revision != before.Revision || after.State != before.State ||
		after.LeaseOwnerID != before.LeaseOwnerID || !after.LeaseExpiresAt.Equal(before.LeaseExpiresAt) {
		t.Fatalf("missing attempt mutated receipt = %#v, before %#v", after, before)
	}
}

func updateEmulatorField(
	t *testing.T,
	client *firestore.Client,
	path string,
	field string,
	value any,
) {
	t.Helper()
	if _, err := client.Doc(path).Update(
		context.Background(), []firestore.Update{{Path: field, Value: value}},
	); err != nil {
		t.Fatalf("update %s.%s: %v", path, field, err)
	}
}
