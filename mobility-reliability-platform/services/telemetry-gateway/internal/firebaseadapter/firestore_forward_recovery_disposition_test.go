package firebaseadapter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

func TestFirestoreAdmissionStoreCommitsAuthorizationDispositionsAtomically(t *testing.T) {
	tests := []struct {
		name            string
		disposition     ingest.ForwardRecoveryAuthorizationDisposition
		wantState       ingest.ReceiptState
		wantAction      ingest.ForwardRecoveryAction
		wantOutcome     ingest.RecoveryAttemptOutcome
		wantControlCode string
	}{
		{
			name: "denied hold", disposition: ingest.ForwardRecoveryAuthorizationDenied,
			wantState: ingest.ReceiptRecoveryHold, wantAction: ingest.ForwardRecoveryActionMarkHold,
			wantOutcome:     ingest.RecoveryAttemptOutcomeHold,
			wantControlCode: string(ingest.RecoveryHoldCurrentAuthorizationDenied),
		},
		{
			name: "unavailable release", disposition: ingest.ForwardRecoveryAuthorizationUnavailable,
			wantState: ingest.ReceiptReserved, wantAction: ingest.ForwardRecoveryActionReleaseLease,
			wantOutcome:     ingest.RecoveryAttemptOutcomeLeaseReleased,
			wantControlCode: string(ingest.LeaseReleaseAuthorizationUnavailable),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, command := newForwardRecoveryDispositionFixture(t, test.disposition)
			store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
			validatorCalls := 0
			got, err := store.commitForwardRecoveryDisposition(
				context.Background(), ingest.ForwardRecoveryDispositionGrant{}, command,
				fixture.observedAt,
				func(
					_ ingest.ForwardRecoveryDispositionGrant,
					gotCommand ingest.ForwardRecoveryDispositionCommand,
					snapshot ingest.CurrentForwardRecoverySnapshot,
					attempt ingest.CurrentForwardRecoveryAttempt,
					checkedAt time.Time,
				) error {
					validatorCalls++
					if gotCommand != command || snapshot.Receipt.ReceiptID != admissionReceiptID ||
						attempt.AttemptID != command.Attempt.ID ||
						!checkedAt.Equal(fixture.observedAt) {
						t.Fatalf("validator input = %#v %#v %v", gotCommand, attempt, checkedAt)
					}
					return nil
				},
			)
			if err != nil {
				t.Fatalf("commitForwardRecoveryDisposition() = %v", err)
			}
			if validatorCalls != 1 || got.State != test.wantState ||
				got.Revision != command.ReceiptRevision+1 || got.LeaseOwnerID != "" {
				t.Fatalf("result = %#v, validator calls = %d", got, validatorCalls)
			}
			if len(fixture.base.creates) != 0 || len(fixture.base.updates) != 2 ||
				fixture.base.updates[0].path != admissionReceiptPath() ||
				fixture.base.updates[1].path != fixture.attemptPath {
				t.Fatalf("creates/updates = %d/%#v", len(fixture.base.creates), fixture.base.updates)
			}
			attemptUpdate := firestoreUpdateMap(fixture.base.updates[1].updates)
			if attemptUpdate["status"] != string(ingest.RecoveryAttemptCompleted) ||
				attemptUpdate["decision_domain"] != string(ingest.ForwardRecoveryDecisionCurrentAuthorization) ||
				attemptUpdate["authorization_disposition"] != string(test.disposition) ||
				attemptUpdate["action"] != string(test.wantAction) ||
				attemptUpdate["outcome"] != string(test.wantOutcome) ||
				!lowerHexDigest(attemptUpdate["action_hash"].(string)) {
				t.Fatalf("attempt updates = %#v", attemptUpdate)
			}
			code := attemptUpdate["hold_code"]
			if test.disposition == ingest.ForwardRecoveryAuthorizationUnavailable {
				code = attemptUpdate["release_code"]
			}
			if code != test.wantControlCode {
				t.Fatalf("control code = %#v, want %q", code, test.wantControlCode)
			}
			for _, update := range append(fixture.base.updates[0].updates, fixture.base.updates[1].updates...) {
				path := strings.ToLower(update.Path)
				for _, forbidden := range []string{"object_", "manifest_", "raw_", "sample_count"} {
					if strings.Contains(path, forbidden) {
						t.Fatalf("disposition mutated artifact field %q", update.Path)
					}
				}
			}
		})
	}
}

func TestFirestoreAdmissionStoreDispositionRequiresExactAttemptAndCurrentResult(t *testing.T) {
	t.Run("prepopulated decision domain", func(t *testing.T) {
		fixture, command := newForwardRecoveryDispositionFixture(
			t, ingest.ForwardRecoveryAuthorizationDenied,
		)
		attempt := fixture.transaction.attempts[fixture.attemptPath]
		attempt.DecisionDomain = ingest.ForwardRecoveryDecisionCurrentAuthorization
		fixture.transaction.attempts[fixture.attemptPath] = attempt
		store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
		_, err := store.commitForwardRecoveryDisposition(
			context.Background(), ingest.ForwardRecoveryDispositionGrant{}, command,
			fixture.observedAt, allowForwardRecoveryDispositionForAdapterTest,
		)
		if !errors.Is(err, ingest.ErrInvalidForwardRecoveryDispositionAuthorization) ||
			len(fixture.base.updates) != 0 {
			t.Fatalf("commit = %v, updates = %d", err, len(fixture.base.updates))
		}
	})

	t.Run("current disposition changed", func(t *testing.T) {
		fixture, command := newForwardRecoveryDispositionFixture(
			t, ingest.ForwardRecoveryAuthorizationDenied,
		)
		store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
		_, err := store.commitForwardRecoveryDisposition(
			context.Background(), ingest.ForwardRecoveryDispositionGrant{}, command,
			fixture.observedAt,
			func(
				ingest.ForwardRecoveryDispositionGrant,
				ingest.ForwardRecoveryDispositionCommand,
				ingest.CurrentForwardRecoverySnapshot,
				ingest.CurrentForwardRecoveryAttempt,
				time.Time,
			) error {
				return ingest.ErrInvalidForwardRecoveryDispositionAuthorization
			},
		)
		if !errors.Is(err, ingest.ErrInvalidForwardRecoveryDispositionAuthorization) ||
			len(fixture.base.updates) != 0 {
			t.Fatalf("commit = %v, updates = %d", err, len(fixture.base.updates))
		}
	})
}

func TestFirestoreAdmissionStoreDispositionRejectsStartedAttemptDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*forwardRecoveryActionFixture)
	}{
		{name: "missing", mutate: func(f *forwardRecoveryActionFixture) {
			delete(f.transaction.attempts, f.attemptPath)
		}},
		{name: "wrong tenant", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.TenantID = emulatorThirdReceiptID
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "wrong receipt", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.ReceiptID = emulatorThirdReceiptID
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "wrong owner", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.OwnerKind = ingest.LeaseOwnerRequest
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "wrong started at", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.StartedAt = attempt.StartedAt.Add(time.Second)
			f.transaction.attempts[f.attemptPath] = attempt
		}},
		{name: "prepopulated disposition", mutate: func(f *forwardRecoveryActionFixture) {
			attempt := f.transaction.attempts[f.attemptPath]
			attempt.AuthorizationDisposition = ingest.ForwardRecoveryAuthorizationDenied
			f.transaction.attempts[f.attemptPath] = attempt
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, command := newForwardRecoveryDispositionFixture(
				t, ingest.ForwardRecoveryAuthorizationDenied,
			)
			test.mutate(fixture)
			store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
			_, err := store.commitForwardRecoveryDisposition(
				context.Background(), ingest.ForwardRecoveryDispositionGrant{}, command,
				fixture.observedAt, allowForwardRecoveryDispositionForAdapterTest,
			)
			if !errors.Is(err, ingest.ErrInvalidForwardRecoveryDispositionAuthorization) ||
				len(fixture.base.updates) != 0 {
				t.Fatalf("commit = %v, updates = %d", err, len(fixture.base.updates))
			}
		})
	}
}

func TestFirestoreAdmissionStoreRejectsInvalidDispositionGrantBeforeTransaction(t *testing.T) {
	fixture, command := newForwardRecoveryDispositionFixture(
		t, ingest.ForwardRecoveryAuthorizationDenied,
	)
	transactionCalls := 0
	store := admissionTestStore(fixture.observedAt, func(
		_ context.Context,
		_ func(context.Context, admissionTransaction) error,
	) error {
		transactionCalls++
		return nil
	})
	_, err := store.CommitForwardRecoveryDisposition(
		context.Background(), ingest.ForwardRecoveryDispositionGrant{}, command, fixture.observedAt,
	)
	if !errors.Is(err, ingest.ErrInvalidForwardRecoveryDispositionAuthorization) || transactionCalls != 0 {
		t.Fatalf("CommitForwardRecoveryDisposition() = %v, calls = %d", err, transactionCalls)
	}
}

func TestForwardRecoveryDispositionReadsAllEvidenceBeforeWrites(t *testing.T) {
	fixture, command := newForwardRecoveryDispositionFixture(
		t, ingest.ForwardRecoveryAuthorizationDenied,
	)
	store := admissionTestStore(fixture.observedAt, admissionRunner(fixture.transaction))
	_, err := store.commitForwardRecoveryDisposition(
		context.Background(), ingest.ForwardRecoveryDispositionGrant{}, command,
		fixture.observedAt, allowForwardRecoveryDispositionForAdapterTest,
	)
	if err != nil {
		t.Fatalf("commitForwardRecoveryDisposition() = %v", err)
	}
	want := []string{
		"index:" + admissionIdempotencyPath(),
		"index:" + admissionClientBatchPath(),
		"receipt:" + admissionReceiptPath(),
		"relations",
		"attempt:" + fixture.attemptPath,
		"update:" + admissionReceiptPath(),
		"update:" + fixture.attemptPath,
	}
	if !reflect.DeepEqual(fixture.base.calls, want) {
		t.Fatalf("calls = %v, want %v", fixture.base.calls, want)
	}
}

func newForwardRecoveryDispositionFixture(
	t *testing.T,
	disposition ingest.ForwardRecoveryAuthorizationDisposition,
) (*forwardRecoveryActionFixture, ingest.ForwardRecoveryDispositionCommand) {
	t.Helper()
	fixture := newForwardRecoveryActionFixture(t, ingest.ForwardRecoveryActionMarkHold)
	command := ingest.ForwardRecoveryDispositionCommand{
		TenantID: fixture.command.TenantID, ReservationKey: fixture.command.ReservationKey,
		Attempt: fixture.command.Attempt, ReceiptRevision: fixture.command.ReceiptRevision,
		Fence: fixture.command.Fence, Disposition: disposition,
	}
	if disposition == ingest.ForwardRecoveryAuthorizationDenied {
		command.HoldReviewDueAt = fixture.observedAt.Add(time.Hour)
	}
	return fixture, command
}

func allowForwardRecoveryDispositionForAdapterTest(
	ingest.ForwardRecoveryDispositionGrant,
	ingest.ForwardRecoveryDispositionCommand,
	ingest.CurrentForwardRecoverySnapshot,
	ingest.CurrentForwardRecoveryAttempt,
	time.Time,
) error {
	return nil
}
