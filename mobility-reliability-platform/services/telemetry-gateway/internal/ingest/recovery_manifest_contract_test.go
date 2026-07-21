package ingest

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestRegisteredValidatorBuildsVersionPinnedRecoveryManifest(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	validator, ok := newTelemetryArtifactContentValidator().(*registeredTelemetryArtifactValidator)
	if !ok {
		t.Fatal("content validator does not expose the private registered profile")
	}

	write, reason := validator.buildRecoveryManifest(
		fixture.request,
		*artifactPinnedLineageFromSnapshot(fixture.rawSnapshot),
	)
	if reason != "" {
		t.Fatalf("buildRecoveryManifest() reason = %q", reason)
	}
	if err := ValidateRecoveryManifestWrite(write); err != nil {
		t.Fatalf("ValidateRecoveryManifestWrite() = %v", err)
	}
	if write.ManifestPath != fixture.request.ExpectedManifestPath ||
		write.Raw.Path != fixture.request.ExpectedRawPath ||
		write.Raw.Generation != fixture.rawSnapshot.Generation ||
		write.Raw.Metageneration != fixture.rawSnapshot.Metageneration {
		t.Fatalf("write lineage = %#v", write)
	}
	if !bytes.Equal(write.CanonicalBody, fixture.manifestBytes) {
		t.Fatal("recovery builder did not use the registered canonical manifest profile")
	}
}

func TestRegisteredValidatorDoesNotFallbackForUnavailableProfile(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	request := fixture.request
	request.ValidatorVersion = "telemetry-validator.prior@1"
	validator, ok := newTelemetryArtifactContentValidator().(*registeredTelemetryArtifactValidator)
	if !ok {
		t.Fatal("content validator does not expose the private registered profile")
	}

	write, reason := validator.buildRecoveryManifest(
		request,
		*artifactPinnedLineageFromSnapshot(fixture.rawSnapshot),
	)
	if reason != ArtifactReasonValidatorUnavailable || write.ManifestPath != "" {
		t.Fatalf("buildRecoveryManifest() = %#v/%q", write, reason)
	}
}

func TestValidateRecoveryManifestWriteRejectsMutations(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	write := recoveryManifestWriteFixture(t, fixture)

	tests := []struct {
		name   string
		mutate func(*RecoveryManifestWrite)
	}{
		{name: "manifest path", mutate: func(value *RecoveryManifestWrite) { value.ManifestPath += ".other" }},
		{name: "raw path", mutate: func(value *RecoveryManifestWrite) { value.Raw.Path += ".other" }},
		{name: "raw replay", mutate: func(value *RecoveryManifestWrite) { value.Raw.Replay = true }},
		{name: "raw generation", mutate: func(value *RecoveryManifestWrite) { value.Raw.Generation = 0 }},
		{name: "input", mutate: func(value *RecoveryManifestWrite) { value.ManifestInput.BatchID = "" }},
		{name: "body", mutate: func(value *RecoveryManifestWrite) { value.CanonicalBody = append(value.CanonicalBody, ' ') }},
		{name: "digest", mutate: func(value *RecoveryManifestWrite) { value.Digest.Size++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneRecoveryManifestWrite(write)
			test.mutate(&changed)
			if err := ValidateRecoveryManifestWrite(changed); !errors.Is(err, ErrInvalidRecoveryManifestWrite) {
				t.Fatalf("ValidateRecoveryManifestWrite() = %v", err)
			}
		})
	}
}

func TestManifestRepairAuthorizationBindsExactWriteAndFence(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	write := recoveryManifestWriteFixture(t, fixture)
	checkedAt := fixture.request.ReceivedAt.Add(2 * time.Minute)
	expiresAt := checkedAt.Add(20 * time.Second)
	validator, ok := newTelemetryArtifactContentValidator().(*registeredTelemetryArtifactValidator)
	if !ok {
		t.Fatal("content validator does not expose the private registered profile")
	}
	grant, err := validator.mintManifestRepairAuthorizationGrant(
		"manifest-repair.current-state@1",
		fixture.request,
		write,
		checkedAt,
		expiresAt,
	)
	if err != nil {
		t.Fatalf("mintManifestRepairAuthorizationGrant() = %v", err)
	}
	if err := ValidateManifestRepairAuthorization(grant, write, checkedAt.Add(time.Second)); err != nil {
		t.Fatalf("ValidateManifestRepairAuthorization() = %v", err)
	}

	t.Run("zero grant", func(t *testing.T) {
		if err := ValidateManifestRepairAuthorization(
			ManifestRepairAuthorizationGrant{},
			write,
			checkedAt,
		); !errors.Is(err, ErrInvalidManifestRepairAuthorization) {
			t.Fatalf("ValidateManifestRepairAuthorization() = %v", err)
		}
	})
	t.Run("write mutation", func(t *testing.T) {
		changed := cloneRecoveryManifestWrite(write)
		changed.Raw.Generation++
		if err := ValidateManifestRepairAuthorization(
			grant,
			changed,
			checkedAt.Add(time.Second),
		); !errors.Is(err, ErrInvalidManifestRepairAuthorization) {
			t.Fatalf("ValidateManifestRepairAuthorization() = %v", err)
		}
	})
	t.Run("self consistent noncanonical body cannot be minted", func(t *testing.T) {
		changed := cloneRecoveryManifestWrite(write)
		changed.CanonicalBody = append(changed.CanonicalBody, ' ')
		changed.Digest = ComputeArtifactDigest(changed.CanonicalBody)
		if err := ValidateRecoveryManifestWrite(changed); err != nil {
			t.Fatalf("shape validation unexpectedly rejected self-consistent body: %v", err)
		}
		_, err = validator.mintManifestRepairAuthorizationGrant(
			"manifest-repair.current-state@1",
			fixture.request,
			changed,
			checkedAt,
			expiresAt,
		)
		if !errors.Is(err, ErrInvalidManifestRepairAuthorization) {
			t.Fatalf("mintManifestRepairAuthorizationGrant() = %v", err)
		}
	})
	t.Run("binding mutation", func(t *testing.T) {
		changed := grant
		changed.writeBindingHash[0] ^= 0xff
		if err := ValidateManifestRepairAuthorization(
			changed,
			write,
			checkedAt.Add(time.Second),
		); !errors.Is(err, ErrInvalidManifestRepairAuthorization) {
			t.Fatalf("ValidateManifestRepairAuthorization() = %v", err)
		}
	})
	t.Run("seal mutation", func(t *testing.T) {
		changed := grant
		changed.capabilitySeal[0] ^= 0xff
		if err := ValidateManifestRepairAuthorization(
			changed,
			write,
			checkedAt.Add(time.Second),
		); !errors.Is(err, ErrInvalidManifestRepairAuthorization) {
			t.Fatalf("ValidateManifestRepairAuthorization() = %v", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		if err := ValidateManifestRepairAuthorization(
			grant,
			write,
			expiresAt,
		); !errors.Is(err, ErrManifestRepairAuthorizationExpired) {
			t.Fatalf("ValidateManifestRepairAuthorization() = %v", err)
		}
	})
}

func TestManifestRepairGrantRejectsMismatchedAuthoritativeRequest(t *testing.T) {
	fixture := newClassifierContentTestFixture(t)
	write := recoveryManifestWriteFixture(t, fixture)
	write.ManifestInput.ConsentRevisionID = "88888888-8888-4888-8888-888888888888"
	body, digest, err := CanonicalTelemetryManifest(write.ManifestInput, write.Raw)
	if err != nil {
		t.Fatalf("CanonicalTelemetryManifest() = %v", err)
	}
	write.CanonicalBody = body
	write.Digest = digest
	checkedAt := fixture.request.ReceivedAt.Add(2 * time.Minute)
	validator, ok := newTelemetryArtifactContentValidator().(*registeredTelemetryArtifactValidator)
	if !ok {
		t.Fatal("content validator does not expose the private registered profile")
	}
	_, err = validator.mintManifestRepairAuthorizationGrant(
		"manifest-repair.current-state@1",
		fixture.request,
		write,
		checkedAt,
		checkedAt.Add(20*time.Second),
	)
	if !errors.Is(err, ErrInvalidManifestRepairAuthorization) {
		t.Fatalf("mintManifestRepairAuthorizationGrant() = %v", err)
	}
}

func recoveryManifestWriteFixture(
	t *testing.T,
	fixture artifactContentTestFixture,
) RecoveryManifestWrite {
	t.Helper()
	validator, ok := newTelemetryArtifactContentValidator().(*registeredTelemetryArtifactValidator)
	if !ok {
		t.Fatal("content validator does not expose the private registered profile")
	}
	write, reason := validator.buildRecoveryManifest(
		fixture.request,
		*artifactPinnedLineageFromSnapshot(fixture.rawSnapshot),
	)
	if reason != "" {
		t.Fatalf("buildRecoveryManifest() reason = %q", reason)
	}
	return write
}

func cloneRecoveryManifestWrite(write RecoveryManifestWrite) RecoveryManifestWrite {
	cloned := write
	cloned.CanonicalBody = append([]byte(nil), write.CanonicalBody...)
	return cloned
}
