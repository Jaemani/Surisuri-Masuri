package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestArtifactClassifierContractInterfaces(t *testing.T) {
	var _ ArtifactInventoryReader = artifactInventoryReaderContractStub{}
	var _ ArtifactClassifier = artifactClassifierContractStub{}
}

func TestValidateArtifactClassificationRequestAcceptsPurposeShapes(t *testing.T) {
	tests := []struct {
		name    string
		request ArtifactClassificationRequest
	}{
		{name: "forward reserved", request: artifactClassificationRequestFixture(t, ArtifactReadForwardRecovery)},
		{name: "accepted stored", request: artifactClassificationRequestFixture(t, ArtifactReadAcceptedIntegrityAudit)},
		{name: "accepted queued", request: acceptedRequestWithState(t, ReceiptQueued)},
		{name: "accepted projected", request: acceptedRequestWithState(t, ReceiptProjected)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateArtifactClassificationRequest(test.request); err != nil {
				t.Fatalf("ValidateArtifactClassificationRequest() = %v", err)
			}
		})
	}
}

func TestValidateArtifactClassificationRequestRejectsInvalidShape(t *testing.T) {
	tests := []struct {
		name    string
		purpose ArtifactReadPurpose
		mutate  func(*ArtifactClassificationRequest)
	}{
		{
			name:    "unknown purpose",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.Purpose = "unknown" },
		},
		{
			name:    "receipt is not batch",
			purpose: ArtifactReadForwardRecovery,
			mutate: func(request *ArtifactClassificationRequest) {
				request.ReceiptID = "99999999-9999-4999-8999-999999999999"
			},
		},
		{
			name:    "reservation key",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ReservationKey = strings.Repeat("f", 64) },
		},
		{
			name:    "revision",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ReceiptRevision = 0 },
		},
		{
			name:    "validator version",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ValidatorVersion = " validator@1" },
		},
		{
			name:    "body hash",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.BodyHash = strings.Repeat("A", 64) },
		},
		{
			name:    "sample count",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ExpectedSampleCount = 0 },
		},
		{
			name:    "capture bounds",
			purpose: ArtifactReadForwardRecovery,
			mutate: func(request *ArtifactClassificationRequest) {
				request.LastCapturedAt = request.FirstCapturedAt.Add(-time.Second)
			},
		},
		{
			name:    "artifact retention",
			purpose: ArtifactReadForwardRecovery,
			mutate: func(request *ArtifactClassificationRequest) {
				request.ArtifactExpiresAt = request.ArtifactExpiresAt.Add(time.Second)
			},
		},
		{
			name:    "raw path",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ExpectedRawPath += ".bak" },
		},
		{
			name:    "manifest path",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ExpectedManifestPath += ".bak" },
		},
		{
			name:    "forward state",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ReceiptState = ReceiptStored },
		},
		{
			name:    "forward fence missing",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ForwardFence = nil },
		},
		{
			name:    "forward fence invalid",
			purpose: ArtifactReadForwardRecovery,
			mutate:  func(request *ArtifactClassificationRequest) { request.ForwardFence.Token = 0 },
		},
		{
			name:    "forward accepted lineage",
			purpose: ArtifactReadForwardRecovery,
			mutate: func(request *ArtifactClassificationRequest) {
				request.AcceptedRawLineage = artifactLineageFixture(request.ExpectedRawPath, "b")
			},
		},
		{
			name:    "accepted state excluded",
			purpose: ArtifactReadAcceptedIntegrityAudit,
			mutate:  func(request *ArtifactClassificationRequest) { request.ReceiptState = ReceiptDeleting },
		},
		{
			name:    "accepted fence present",
			purpose: ArtifactReadAcceptedIntegrityAudit,
			mutate: func(request *ArtifactClassificationRequest) {
				request.ForwardFence = &LeaseFence{
					OwnerID:   "77777777-7777-4777-8777-777777777777",
					Token:     3,
					ExpiresAt: request.ReceivedAt.Add(5 * time.Minute),
				}
			},
		},
		{
			name:    "accepted raw missing",
			purpose: ArtifactReadAcceptedIntegrityAudit,
			mutate:  func(request *ArtifactClassificationRequest) { request.AcceptedRawLineage = nil },
		},
		{
			name:    "accepted manifest path",
			purpose: ArtifactReadAcceptedIntegrityAudit,
			mutate: func(request *ArtifactClassificationRequest) {
				request.AcceptedManifestLineage.Path += ".other"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := artifactClassificationRequestFixture(t, test.purpose)
			test.mutate(&request)
			if err := ValidateArtifactClassificationRequest(request); !errors.Is(err, ErrInvalidArtifactClassificationRequest) {
				t.Fatalf("ValidateArtifactClassificationRequest() = %v", err)
			}
		})
	}
}

func TestCanonicalArtifactClassificationRequestBindingIsStableAndComplete(t *testing.T) {
	request := artifactClassificationRequestFixture(t, ArtifactReadForwardRecovery)
	want := canonicalArtifactClassificationRequestBinding(request)

	korea := time.FixedZone("KST", 9*60*60)
	equivalent := request
	equivalent.FirstCapturedAt = request.FirstCapturedAt.In(korea)
	equivalent.LastCapturedAt = request.LastCapturedAt.In(korea)
	equivalent.ReceivedAt = request.ReceivedAt.In(korea)
	equivalent.ArtifactExpiresAt = request.ArtifactExpiresAt.In(korea)
	fence := *request.ForwardFence
	fence.ExpiresAt = request.ForwardFence.ExpiresAt.In(korea)
	equivalent.ForwardFence = &fence
	if got := canonicalArtifactClassificationRequestBinding(equivalent); got != want {
		t.Fatal("equivalent instants produced different request binding")
	}

	mutations := []struct {
		name   string
		mutate func(*ArtifactClassificationRequest)
	}{
		{name: "revision", mutate: func(value *ArtifactClassificationRequest) { value.ReceiptRevision++ }},
		{name: "consent", mutate: func(value *ArtifactClassificationRequest) {
			value.ConsentRevisionID = "88888888-8888-4888-8888-888888888888"
		}},
		{name: "fence", mutate: func(value *ArtifactClassificationRequest) {
			cloned := *value.ForwardFence
			cloned.Token++
			value.ForwardFence = &cloned
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := request
			mutation.mutate(&changed)
			if got := canonicalArtifactClassificationRequestBinding(changed); got == want {
				t.Fatal("security-relevant mutation did not change request binding")
			}
		})
	}
}

func TestArtifactReadAuthorizationGrantValidatesOpaqueBinding(t *testing.T) {
	request := artifactClassificationRequestFixture(t, ArtifactReadForwardRecovery)
	checkedAt := request.ReceivedAt.Add(time.Minute)
	expiresAt := checkedAt.Add(2 * time.Minute)
	grant, err := mintArtifactReadAuthorizationGrant(
		artifactReadGrantIssuerForwardRecovery,
		"recovery-policy@1",
		request,
		checkedAt,
		expiresAt,
	)
	if err != nil {
		t.Fatalf("mintArtifactReadAuthorizationGrant() = %v", err)
	}
	if err := ValidateArtifactReadAuthorization(grant, request, checkedAt.Add(time.Second)); err != nil {
		t.Fatalf("ValidateArtifactReadAuthorization() = %v", err)
	}

	tests := []struct {
		name       string
		grant      ArtifactReadAuthorizationGrant
		request    ArtifactClassificationRequest
		observedAt time.Time
		want       error
	}{
		{
			name:       "zero grant",
			request:    request,
			observedAt: checkedAt.Add(time.Second),
			want:       ErrInvalidArtifactReadAuthorization,
		},
		{
			name:       "request revision changed",
			grant:      grant,
			request:    mutateRequestRevision(request),
			observedAt: checkedAt.Add(time.Second),
			want:       ErrInvalidArtifactReadAuthorization,
		},
		{
			name:       "request fence changed",
			grant:      grant,
			request:    mutateRequestFence(request),
			observedAt: checkedAt.Add(time.Second),
			want:       ErrInvalidArtifactReadAuthorization,
		},
		{
			name:       "issuer changed",
			grant:      mutateGrantIssuer(grant),
			request:    request,
			observedAt: checkedAt.Add(time.Second),
			want:       ErrInvalidArtifactReadAuthorization,
		},
		{
			name:       "binding hash changed",
			grant:      mutateGrantBinding(grant),
			request:    request,
			observedAt: checkedAt.Add(time.Second),
			want:       ErrInvalidArtifactReadAuthorization,
		},
		{
			name:       "capability seal changed",
			grant:      mutateGrantSeal(grant),
			request:    request,
			observedAt: checkedAt.Add(time.Second),
			want:       ErrInvalidArtifactReadAuthorization,
		},
		{
			name:       "before authorization check",
			grant:      grant,
			request:    request,
			observedAt: checkedAt.Add(-time.Nanosecond),
			want:       ErrInvalidArtifactReadAuthorization,
		},
		{
			name:       "grant exact expiry",
			grant:      grant,
			request:    request,
			observedAt: expiresAt,
			want:       ErrArtifactReadAuthorizationExpired,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateArtifactReadAuthorization(test.grant, test.request, test.observedAt); !errors.Is(err, test.want) {
				t.Fatalf("ValidateArtifactReadAuthorization() = %v, want %v", err, test.want)
			}
		})
	}
}

func TestArtifactReadAuthorizationEnforcesPurposeAndFenceExpiry(t *testing.T) {
	accepted := artifactClassificationRequestFixture(t, ArtifactReadAcceptedIntegrityAudit)
	checkedAt := accepted.ReceivedAt.Add(time.Minute)
	acceptedGrant, err := mintArtifactReadAuthorizationGrant(
		artifactReadGrantIssuerAcceptedIntegrityAudit,
		"integrity-policy@1",
		accepted,
		checkedAt,
		checkedAt.Add(5*time.Minute),
	)
	if err != nil {
		t.Fatalf("mint accepted grant: %v", err)
	}
	if err := ValidateArtifactReadAuthorization(acceptedGrant, accepted, checkedAt.Add(time.Second)); err != nil {
		t.Fatalf("validate accepted grant: %v", err)
	}
	if _, err := mintArtifactReadAuthorizationGrant(
		artifactReadGrantIssuerForwardRecovery,
		"integrity-policy@1",
		accepted,
		checkedAt,
		checkedAt.Add(5*time.Minute),
	); !errors.Is(err, ErrInvalidArtifactReadAuthorization) {
		t.Fatalf("cross-purpose mint = %v", err)
	}

	forward := artifactClassificationRequestFixture(t, ArtifactReadForwardRecovery)
	checkedAt = forward.ReceivedAt.Add(time.Minute)
	grant, err := mintArtifactReadAuthorizationGrant(
		artifactReadGrantIssuerForwardRecovery,
		"recovery-policy@1",
		forward,
		checkedAt,
		forward.ForwardFence.ExpiresAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("mint forward grant: %v", err)
	}
	if err := ValidateArtifactReadAuthorization(grant, forward, forward.ForwardFence.ExpiresAt); !errors.Is(err, ErrArtifactReadAuthorizationExpired) {
		t.Fatalf("fence exact expiry = %v", err)
	}
}

func TestArtifactServerLabelsRejectUnsafeValues(t *testing.T) {
	unsafe := []struct {
		name  string
		value string
	}{
		{name: "newline", value: "policy\nversion"},
		{name: "nul", value: "policy\x00version"},
		{name: "slash", value: "policy/version"},
		{name: "non ascii", value: "정책-version"},
		{name: "unsafe first character", value: ".policy"},
	}
	for _, test := range unsafe {
		t.Run(test.name+" validator", func(t *testing.T) {
			request := artifactClassificationRequestFixture(t, ArtifactReadForwardRecovery)
			request.ValidatorVersion = test.value
			if err := ValidateArtifactClassificationRequest(request); !errors.Is(err, ErrInvalidArtifactClassificationRequest) {
				t.Fatalf("validator label %q = %v", test.value, err)
			}
		})
		t.Run(test.name+" policy", func(t *testing.T) {
			request := artifactClassificationRequestFixture(t, ArtifactReadForwardRecovery)
			checkedAt := request.ReceivedAt.Add(time.Minute)
			_, err := mintArtifactReadAuthorizationGrant(
				artifactReadGrantIssuerForwardRecovery,
				test.value,
				request,
				checkedAt,
				checkedAt.Add(time.Minute),
			)
			if !errors.Is(err, ErrInvalidArtifactReadAuthorization) {
				t.Fatalf("policy label %q = %v", test.value, err)
			}
		})
	}
}

func TestInvalidArtifactReadAuthorizationCanCloseBeforeReaderCall(t *testing.T) {
	request := artifactClassificationRequestFixture(t, ArtifactReadForwardRecovery)
	reader := &countingArtifactInventoryReader{}
	if err := validateThenInspectForContractTest(
		reader,
		ArtifactReadAuthorizationGrant{},
		request,
		request.ReceivedAt.Add(time.Minute),
	); !errors.Is(err, ErrInvalidArtifactReadAuthorization) {
		t.Fatalf("validateThenInspectForContractTest() = %v", err)
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d, want 0", reader.calls)
	}
}

func artifactClassificationRequestFixture(
	t *testing.T,
	purpose ArtifactReadPurpose,
) ArtifactClassificationRequest {
	t.Helper()
	receivedAt := time.Date(2026, time.July, 21, 9, 0, 0, 0, time.UTC)
	request := ArtifactClassificationRequest{
		Purpose:              purpose,
		ReceiptID:            "01982015-4400-7000-8000-000000000001",
		ReceiptState:         ReceiptReserved,
		ReceiptRevision:      7,
		TenantID:             "11111111-1111-4111-8111-111111111111",
		DeviceID:             "22222222-2222-4222-8222-222222222222",
		TripID:               "33333333-3333-4333-8333-333333333333",
		InstallationID:       "44444444-4444-4444-8444-444444444444",
		BatchID:              "01982015-4400-7000-8000-000000000001",
		ClientBatchID:        "55555555-5555-4555-8555-555555555555",
		ConsentRevisionID:    "66666666-6666-4666-8666-666666666666",
		PayloadSchemaVersion: "telemetry-batch.v2",
		ValidatorVersion:     "telemetry-gateway-validator@1",
		BodyHash:             strings.Repeat("a", 64),
		ExpectedSampleCount:  2,
		FirstCapturedAt:      receivedAt.Add(-20 * time.Minute),
		LastCapturedAt:       receivedAt.Add(-time.Minute),
		ReceivedAt:           receivedAt,
		ArtifactExpiresAt:    receivedAt.Add(TelemetryArtifactRetention),
		ForwardFence: &LeaseFence{
			OwnerID:   "77777777-7777-4777-8777-777777777777",
			Token:     11,
			ExpiresAt: receivedAt.Add(5 * time.Minute),
		},
	}
	request.ReservationKey = DeriveReservationKey(
		request.PayloadSchemaVersion,
		request.TenantID,
		request.InstallationID,
		request.ClientBatchID,
	)
	manifestInput := BatchManifestInput{
		PayloadSchemaVersion: request.PayloadSchemaVersion,
		TenantID:             request.TenantID,
		DeviceID:             request.DeviceID,
		TripID:               request.TripID,
		InstallationID:       request.InstallationID,
		BatchID:              request.BatchID,
		ClientBatchID:        request.ClientBatchID,
		ConsentRevisionID:    request.ConsentRevisionID,
		BodyHash:             request.BodyHash,
		SampleCount:          request.ExpectedSampleCount,
		FirstCapturedAt:      request.FirstCapturedAt,
		LastCapturedAt:       request.LastCapturedAt,
		ReceivedAt:           request.ReceivedAt,
		ArtifactExpiresAt:    request.ArtifactExpiresAt,
		ValidatorVersion:     request.ValidatorVersion,
	}
	request.ExpectedRawPath = ExpectedTelemetryObjectPath(manifestInput)
	request.ExpectedManifestPath = ExpectedTelemetryManifestPath(manifestInput)

	if purpose == ArtifactReadAcceptedIntegrityAudit {
		request.ReceiptState = ReceiptStored
		request.ForwardFence = nil
		request.AcceptedRawLineage = artifactLineageFixture(request.ExpectedRawPath, "b")
		request.AcceptedManifestLineage = artifactLineageFixture(request.ExpectedManifestPath, "c")
	}
	return request
}

func acceptedRequestWithState(t *testing.T, state ReceiptState) ArtifactClassificationRequest {
	t.Helper()
	request := artifactClassificationRequestFixture(t, ArtifactReadAcceptedIntegrityAudit)
	request.ReceiptState = state
	return request
}

func artifactLineageFixture(path, digestCharacter string) *ArtifactLineage {
	return &ArtifactLineage{
		Path:           path,
		SHA256:         strings.Repeat(digestCharacter, 64),
		CRC32C:         0x12345678,
		Size:           4096,
		Generation:     1700000000000001,
		Metageneration: 1,
	}
}

func mutateRequestRevision(request ArtifactClassificationRequest) ArtifactClassificationRequest {
	request.ReceiptRevision++
	return request
}

func mutateRequestFence(request ArtifactClassificationRequest) ArtifactClassificationRequest {
	fence := *request.ForwardFence
	fence.Token++
	request.ForwardFence = &fence
	return request
}

func mutateGrantIssuer(grant ArtifactReadAuthorizationGrant) ArtifactReadAuthorizationGrant {
	grant.issuer = artifactReadGrantIssuerAcceptedIntegrityAudit
	return grant
}

func mutateGrantBinding(grant ArtifactReadAuthorizationGrant) ArtifactReadAuthorizationGrant {
	grant.requestBindingHash[0] ^= 0xff
	return grant
}

func mutateGrantSeal(grant ArtifactReadAuthorizationGrant) ArtifactReadAuthorizationGrant {
	grant.capabilitySeal[0] ^= 0xff
	return grant
}

type artifactInventoryReaderContractStub struct{}

func (artifactInventoryReaderContractStub) ListExactPathGenerations(
	context.Context,
	string,
	int,
) (GenerationInventory, error) {
	return GenerationInventory{}, nil
}

func (artifactInventoryReaderContractStub) InspectGeneration(
	context.Context,
	string,
	int64,
) (ArtifactSnapshot, error) {
	return ArtifactSnapshot{}, nil
}

func (artifactInventoryReaderContractStub) ReadManifestGeneration(
	context.Context,
	ArtifactTarget,
	int64,
) ([]byte, error) {
	return nil, nil
}

func (artifactInventoryReaderContractStub) ReadRawGenerationCompressed(
	context.Context,
	ArtifactTarget,
	int64,
) ([]byte, error) {
	return nil, nil
}

type artifactClassifierContractStub struct{}

func (artifactClassifierContractStub) Classify(
	context.Context,
	ArtifactReadAuthorizationGrant,
	ArtifactClassificationRequest,
) (ArtifactClassificationResult, error) {
	return ArtifactClassificationResult{}, nil
}

type countingArtifactInventoryReader struct {
	calls int
}

func (r *countingArtifactInventoryReader) ListExactPathGenerations(
	context.Context,
	string,
	int,
) (GenerationInventory, error) {
	r.calls++
	return GenerationInventory{}, nil
}

func validateThenInspectForContractTest(
	reader *countingArtifactInventoryReader,
	grant ArtifactReadAuthorizationGrant,
	request ArtifactClassificationRequest,
	observedAt time.Time,
) error {
	if err := ValidateArtifactReadAuthorization(grant, request, observedAt); err != nil {
		return err
	}
	_, err := reader.ListExactPathGenerations(context.Background(), request.ExpectedManifestPath, 2)
	return err
}
