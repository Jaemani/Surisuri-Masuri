package ingest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

type artifactContentTestFixture struct {
	request          ArtifactClassificationRequest
	manifestSnapshot ArtifactSnapshot
	manifestBytes    []byte
	rawSnapshot      ArtifactSnapshot
	rawCompressed    []byte
}

func TestTelemetryArtifactContentValidatorAcceptsCompleteCanonicalLineage(t *testing.T) {
	fixture := newArtifactContentTestFixture(t)
	result := newTelemetryArtifactContentValidator().Validate(
		fixture.request,
		fixture.manifestSnapshot,
		fixture.manifestBytes,
		fixture.rawSnapshot,
		fixture.rawCompressed,
	)
	assertArtifactContentResult(t, result, artifactContentValidationValid, "")
}

func TestTelemetryArtifactContentValidatorValidatesManifestAndRawIndependently(t *testing.T) {
	fixture := newArtifactContentTestFixture(t)
	validator := newTelemetryArtifactContentValidator()

	manifestResult := validator.ValidateManifest(
		fixture.request,
		fixture.manifestSnapshot,
		fixture.manifestBytes,
	)
	if manifestResult.Status != artifactContentValidationValid || manifestResult.ReasonCode != "" ||
		manifestResult.ReferencedRaw == nil ||
		!validatedRawReferenceMatchesSnapshot(*manifestResult.ReferencedRaw, fixture.rawSnapshot) {
		t.Fatalf("independent manifest result = %#v", manifestResult)
	}

	rawResult := validator.ValidateRaw(
		fixture.request,
		fixture.rawSnapshot,
		fixture.rawCompressed,
	)
	assertArtifactContentResult(t, rawResult, artifactContentValidationValid, "")
}

func TestTelemetryArtifactManifestValidatorPinsAcceptedRawLineage(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*ArtifactLineage)
	}{
		{name: "sha", mutate: func(value *ArtifactLineage) { value.SHA256 = strings.Repeat("a", 64) }},
		{name: "crc", mutate: func(value *ArtifactLineage) { value.CRC32C++ }},
		{name: "size", mutate: func(value *ArtifactLineage) { value.Size++ }},
		{name: "generation", mutate: func(value *ArtifactLineage) { value.Generation++ }},
		{name: "metageneration", mutate: func(value *ArtifactLineage) { value.Metageneration++ }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			fixture := acceptedArtifactContentTestFixture(t)
			test.mutate(fixture.request.AcceptedRawLineage)
			result := newTelemetryArtifactContentValidator().ValidateManifest(
				fixture.request,
				fixture.manifestSnapshot,
				fixture.manifestBytes,
			)
			if result.Status != artifactContentValidationInvalid ||
				result.ReasonCode != ArtifactReasonManifestLineageMismatch ||
				result.ReferencedRaw != nil {
				t.Fatalf("manifest result = %#v", result)
			}
		})
	}
}

func TestTelemetryArtifactManifestValidatorTreatsAcceptedSnapshotMismatchAsMetadata(t *testing.T) {
	fixture := acceptedArtifactContentTestFixture(t)
	fixture.manifestSnapshot.Metageneration++
	result := newTelemetryArtifactContentValidator().ValidateManifest(
		fixture.request,
		fixture.manifestSnapshot,
		fixture.manifestBytes,
	)
	if result.Status != artifactContentValidationInvalid ||
		result.ReasonCode != ArtifactReasonRequiredMetadataMismatch ||
		result.ReferencedRaw != nil {
		t.Fatalf("accepted manifest snapshot result = %#v", result)
	}
}

func TestTelemetryArtifactContentValidatorRejectsEveryManifestReceiptIdentityMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TelemetryManifest)
	}{
		{name: "tenant", mutate: func(value *TelemetryManifest) { value.TenantID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }},
		{name: "device", mutate: func(value *TelemetryManifest) { value.DeviceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }},
		{name: "trip", mutate: func(value *TelemetryManifest) { value.TripID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }},
		{name: "installation", mutate: func(value *TelemetryManifest) { value.InstallationID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }},
		{name: "batch", mutate: func(value *TelemetryManifest) { value.BatchID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }},
		{name: "client batch", mutate: func(value *TelemetryManifest) { value.ClientBatchID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }},
		{name: "consent revision", mutate: func(value *TelemetryManifest) { value.ConsentRevisionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }},
		{name: "body hash", mutate: func(value *TelemetryManifest) { value.BodyHash = strings.Repeat("a", 64) }},
		{name: "sample count", mutate: func(value *TelemetryManifest) { value.SampleCount++ }},
		{name: "first captured", mutate: func(value *TelemetryManifest) { value.FirstCapturedAt = "2025-12-31T23:59:59Z" }},
		{name: "last captured", mutate: func(value *TelemetryManifest) { value.LastCapturedAt = "2026-01-01T00:00:01Z" }},
		{name: "received", mutate: func(value *TelemetryManifest) { value.ReceivedAt = "2026-01-02T00:00:01Z" }},
		{name: "expires", mutate: func(value *TelemetryManifest) { value.ExpiresAt = "2026-02-01T00:00:01Z" }},
		{name: "validator", mutate: func(value *TelemetryManifest) { value.ValidatorVersion = "telemetry-gateway-validator@2" }},
		{name: "object path", mutate: func(value *TelemetryManifest) { value.ObjectPath += ".other" }},
		{name: "object sha", mutate: func(value *TelemetryManifest) { value.ObjectSHA256 = strings.Repeat("a", 64) }},
		{name: "object crc", mutate: func(value *TelemetryManifest) { value.ObjectCRC32C++ }},
		{name: "object size", mutate: func(value *TelemetryManifest) { value.ObjectSize++ }},
		{name: "object generation", mutate: func(value *TelemetryManifest) { value.ObjectGeneration++ }},
		{name: "object metageneration", mutate: func(value *TelemetryManifest) { value.ObjectMetageneration++ }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newArtifactContentTestFixture(t)
			manifest := decodeManifestFixture(t, fixture.manifestBytes)
			test.mutate(&manifest)
			replaceManifestBytes(t, &fixture, marshalJSON(t, manifest))
			fixture.manifestSnapshot.Metadata["object_generation"] = fmt.Sprint(manifest.ObjectGeneration)
			assertFixtureReason(t, fixture, ArtifactReasonManifestLineageMismatch)
		})
	}
}

func TestTelemetryArtifactContentValidatorSeparatesMalformedAndNoncanonicalManifest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, artifactContentTestFixture) []byte
		reason ArtifactReasonCode
	}{
		{name: "malformed JSON", mutate: func(_ *testing.T, _ artifactContentTestFixture) []byte { return []byte("{") }, reason: ArtifactReasonManifestMalformed},
		{name: "unknown field", mutate: func(_ *testing.T, value artifactContentTestFixture) []byte {
			return appendBeforeFinalBrace(value.manifestBytes, `,"unknown":true`)
		}, reason: ArtifactReasonManifestMalformed},
		{name: "duplicate field", mutate: func(_ *testing.T, value artifactContentTestFixture) []byte {
			return appendBeforeFinalBrace(value.manifestBytes, `,"tenant_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"`)
		}, reason: ArtifactReasonManifestMalformed},
		{name: "trailing document", mutate: func(_ *testing.T, value artifactContentTestFixture) []byte {
			return append(append([]byte(nil), value.manifestBytes...), []byte(` {}`)...)
		}, reason: ArtifactReasonManifestMalformed},
		{name: "unknown version", mutate: mutateManifestFixture(func(value *TelemetryManifest) { value.ManifestVersion++ }), reason: ArtifactReasonManifestMalformed},
		{name: "unknown payload schema", mutate: mutateManifestFixture(func(value *TelemetryManifest) { value.PayloadSchemaVersion = "telemetry-batch.v1" }), reason: ArtifactReasonManifestMalformed},
		{name: "wrong compression", mutate: mutateManifestFixture(func(value *TelemetryManifest) { value.Compression = "identity" }), reason: ArtifactReasonManifestMalformed},
		{name: "wrong content type", mutate: mutateManifestFixture(func(value *TelemetryManifest) { value.ContentType = "text/plain" }), reason: ArtifactReasonManifestMalformed},
		{name: "invalid time", mutate: mutateManifestFixture(func(value *TelemetryManifest) { value.ReceivedAt = "not-a-time" }), reason: ArtifactReasonManifestMalformed},
		{name: "noncanonical whitespace", mutate: func(t *testing.T, value artifactContentTestFixture) []byte {
			var destination bytes.Buffer
			if err := json.Indent(&destination, value.manifestBytes, "", "  "); err != nil {
				t.Fatalf("indent manifest: %v", err)
			}
			return destination.Bytes()
		}, reason: ArtifactReasonManifestNoncanonical},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newArtifactContentTestFixture(t)
			replaceManifestBytes(t, &fixture, test.mutate(t, fixture))
			assertFixtureReason(t, fixture, test.reason)
		})
	}
}

func TestTelemetryArtifactContentValidatorRejectsManifestSnapshotDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ArtifactSnapshot)
		reason ArtifactReasonCode
	}{
		{name: "malformed path", mutate: func(value *ArtifactSnapshot) { value.Path += ".other" }, reason: ArtifactReasonAttrsMalformed},
		{name: "malformed generation", mutate: func(value *ArtifactSnapshot) { value.Generation = 0 }, reason: ArtifactReasonAttrsMalformed},
		{name: "digest", mutate: func(value *ArtifactSnapshot) { value.SHA256 = strings.Repeat("a", 64) }, reason: ArtifactReasonRequiredMetadataMismatch},
		{name: "crc", mutate: func(value *ArtifactSnapshot) { value.CRC32C++ }, reason: ArtifactReasonRequiredMetadataMismatch},
		{name: "size", mutate: func(value *ArtifactSnapshot) { value.Size++ }, reason: ArtifactReasonRequiredMetadataMismatch},
		{name: "content type", mutate: func(value *ArtifactSnapshot) { value.ContentType = "text/plain" }, reason: ArtifactReasonContentHeadersMismatch},
		{name: "content encoding", mutate: func(value *ArtifactSnapshot) { value.ContentEncoding = "gzip" }, reason: ArtifactReasonContentHeadersMismatch},
		{name: "cache control", mutate: func(value *ArtifactSnapshot) { value.CacheControl = "public" }, reason: ArtifactReasonContentHeadersMismatch},
		{name: "required metadata", mutate: func(value *ArtifactSnapshot) { delete(value.Metadata, "artifact_kind") }, reason: ArtifactReasonRequiredMetadataMismatch},
		{name: "unknown metadata", mutate: func(value *ArtifactSnapshot) { value.Metadata["untrusted"] = "value" }, reason: ArtifactReasonRequiredMetadataMismatch},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newArtifactContentTestFixture(t)
			test.mutate(&fixture.manifestSnapshot)
			assertFixtureReason(t, fixture, test.reason)
		})
	}
}

func TestTelemetryArtifactContentValidatorAcceptsKnownTestbenchMetadata(t *testing.T) {
	fixture := newArtifactContentTestFixture(t)
	addTestbenchMetadata(fixture.manifestSnapshot.Metadata, fixture.manifestSnapshot.CRC32C)
	addTestbenchMetadata(fixture.rawSnapshot.Metadata, fixture.rawSnapshot.CRC32C)
	result := validateFixture(fixture)
	assertArtifactContentResult(t, result, artifactContentValidationValid, "")
}

func TestTelemetryArtifactContentValidatorRejectsRawCompressedDigestDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ArtifactSnapshot)
	}{
		{name: "sha", mutate: func(value *ArtifactSnapshot) {
			value.SHA256 = strings.Repeat("a", 64)
			value.Metadata["sha256"] = value.SHA256
		}},
		{name: "crc", mutate: func(value *ArtifactSnapshot) { value.CRC32C++ }},
		{name: "size", mutate: func(value *ArtifactSnapshot) { value.Size++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newArtifactContentTestFixture(t)
			test.mutate(&fixture.rawSnapshot)
			refreshCanonicalManifest(t, &fixture)
			assertFixtureReason(t, fixture, ArtifactReasonRequiredMetadataMismatch)
		})
	}
}

func TestTelemetryArtifactContentValidatorRejectsRawHeaderAndMetadataDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ArtifactSnapshot)
		reason ArtifactReasonCode
	}{
		{name: "content type", mutate: func(value *ArtifactSnapshot) { value.ContentType = "text/plain" }, reason: ArtifactReasonContentHeadersMismatch},
		{name: "content encoding", mutate: func(value *ArtifactSnapshot) { value.ContentEncoding = "" }, reason: ArtifactReasonContentHeadersMismatch},
		{name: "cache control", mutate: func(value *ArtifactSnapshot) { value.CacheControl = "public" }, reason: ArtifactReasonContentHeadersMismatch},
		{name: "metadata", mutate: func(value *ArtifactSnapshot) { delete(value.Metadata, "body_sha256") }, reason: ArtifactReasonRequiredMetadataMismatch},
		{name: "unknown metadata", mutate: func(value *ArtifactSnapshot) { value.Metadata["untrusted"] = "value" }, reason: ArtifactReasonRequiredMetadataMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newArtifactContentTestFixture(t)
			test.mutate(&fixture.rawSnapshot)
			assertFixtureReason(t, fixture, test.reason)
		})
	}
}

func TestTelemetryArtifactContentValidatorPreservesMetadataBeforeManifestPrecedence(t *testing.T) {
	fixture := newArtifactContentTestFixture(t)
	manifest := decodeManifestFixture(t, fixture.manifestBytes)
	manifest.ObjectGeneration++
	replaceManifestBytes(t, &fixture, marshalJSON(t, manifest))
	fixture.manifestSnapshot.Metadata["object_generation"] = fmt.Sprint(manifest.ObjectGeneration)
	fixture.rawSnapshot.ContentType = "text/plain"

	assertFixtureReason(t, fixture, ArtifactReasonContentHeadersMismatch)
}

func TestTelemetryArtifactContentValidatorRejectsInvalidGZIPEnvelope(t *testing.T) {
	valid := newArtifactContentTestFixture(t)
	tests := []struct {
		name       string
		compressed func(*testing.T) []byte
		bodyHash   string
	}{
		{name: "corrupt", compressed: func(_ *testing.T) []byte { return []byte("not-gzip") }, bodyHash: valid.request.BodyHash},
		{name: "trailing garbage", compressed: func(_ *testing.T) []byte {
			return append(append([]byte(nil), valid.rawCompressed...), []byte("garbage")...)
		}, bodyHash: valid.request.BodyHash},
		{name: "multiple streams", compressed: func(_ *testing.T) []byte {
			return append(append([]byte(nil), valid.rawCompressed...), valid.rawCompressed...)
		}, bodyHash: valid.request.BodyHash},
		{name: "decompressed overflow", compressed: func(t *testing.T) []byte {
			compressed, err := deterministicGZIP(bytes.Repeat([]byte("z"), maxTelemetryArtifactBodyBytes+1))
			if err != nil {
				t.Fatalf("compress overflow fixture: %v", err)
			}
			return compressed
		}, bodyHash: hashBytes(bytes.Repeat([]byte("z"), maxTelemetryArtifactBodyBytes+1))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newArtifactContentTestFixture(t)
			fixture.request.BodyHash = test.bodyHash
			fixture.rawCompressed = test.compressed(t)
			refreshRawSnapshotAndManifest(t, &fixture)
			assertFixtureReason(t, fixture, ArtifactReasonStrictPayloadInvalid)
		})
	}
}

func TestTelemetryArtifactContentValidatorRejectsStrictPayloadAndBodyHashMismatch(t *testing.T) {
	t.Run("strict payload invalid", func(t *testing.T) {
		fixture := newArtifactContentTestFixture(t)
		setRawBody(t, &fixture, []byte(`{"schemaVersion":"telemetry-batch.v2","unknown":true}`), true)
		assertFixtureReason(t, fixture, ArtifactReasonStrictPayloadInvalid)
	})

	t.Run("body hash mismatch", func(t *testing.T) {
		fixture := newArtifactContentTestFixture(t)
		alternate := bytes.ReplaceAll(
			[]byte(telemetryCodecGoldenRawV1),
			[]byte("22222222-2222-4222-8222-222222222222"),
			[]byte("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		)
		setRawBody(t, &fixture, alternate, false)
		assertFixtureReason(t, fixture, ArtifactReasonDecompressedBodyHashMismatch)
	})
}

func TestTelemetryArtifactContentValidatorRejectsEveryPayloadLineageMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, []byte) []byte
	}{
		{name: "tenant", mutate: replaceRawString("22222222-2222-4222-8222-222222222222", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
		{name: "device", mutate: replaceRawString("33333333-3333-4333-8333-333333333333", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
		{name: "trip", mutate: replaceRawString("44444444-4444-4444-8444-444444444444", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
		{name: "installation", mutate: replaceRawString("66666666-6666-4666-8666-666666666666", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
		{name: "consent", mutate: replaceRawString("77777777-7777-4777-8777-777777777777", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
		{name: "client batch", mutate: replaceRawString("11111111-1111-4111-8111-111111111111", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")},
		{name: "sample count", mutate: func(t *testing.T, raw []byte) []byte {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			samples := value["samples"].([]any)
			second := cloneJSONMap(samples[0].(map[string]any))
			second["clientSampleId"] = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			second["sequence"] = float64(1)
			value["samples"] = append(samples, second)
			return marshalJSON(t, value)
		}},
		{name: "captured bounds", mutate: func(t *testing.T, raw []byte) []byte {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			value["samples"].([]any)[0].(map[string]any)["capturedAt"] = "2026-01-01T00:00:01Z"
			return marshalJSON(t, value)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newArtifactContentTestFixture(t)
			setRawBody(t, &fixture, test.mutate(t, []byte(telemetryCodecGoldenRawV1)), true)
			assertFixtureReason(t, fixture, ArtifactReasonPayloadLineageMismatch)
		})
	}

	t.Run("schema is rejected by strict validator before lineage", func(t *testing.T) {
		fixture := newArtifactContentTestFixture(t)
		mutated := bytes.Replace(
			[]byte(telemetryCodecGoldenRawV1),
			[]byte(telemetry.SchemaVersionV2),
			[]byte("telemetry-batch.v3"),
			1,
		)
		setRawBody(t, &fixture, mutated, true)
		assertFixtureReason(t, fixture, ArtifactReasonStrictPayloadInvalid)
	})
}

func TestTelemetryArtifactContentValidatorFailsClosedForRegistryAndCodecDrift(t *testing.T) {
	t.Run("unknown validator", func(t *testing.T) {
		fixture := newArtifactContentTestFixture(t)
		fixture.request.ValidatorVersion = "telemetry-gateway-validator@unknown"
		assertFixtureReason(t, fixture, ArtifactReasonValidatorUnavailable)
	})

	t.Run("codec startup self-test mismatch", func(t *testing.T) {
		fixture := newArtifactContentTestFixture(t)
		profile := currentTelemetryArtifactValidatorProfile()
		profile.codec.goldenCompressedSHA256 = strings.Repeat("0", 64)
		validator := newTelemetryArtifactContentValidatorWithProfiles(profile)
		result := validator.Validate(
			fixture.request,
			fixture.manifestSnapshot,
			fixture.manifestBytes,
			fixture.rawSnapshot,
			fixture.rawCompressed,
		)
		assertArtifactContentResult(t, result, artifactContentValidationInvalid, ArtifactReasonCodecProfileUnavailable)
	})

	t.Run("oversized validator profile bound", func(t *testing.T) {
		fixture := newArtifactContentTestFixture(t)
		profile := currentTelemetryArtifactValidatorProfile()
		profile.maxDecompressedBytes = maxTelemetryArtifactBodyBytes + 1
		validator := newTelemetryArtifactContentValidatorWithProfiles(profile)
		result := validator.ValidateRaw(
			fixture.request,
			fixture.rawSnapshot,
			fixture.rawCompressed,
		)
		assertArtifactContentResult(t, result, artifactContentValidationInvalid, ArtifactReasonValidatorUnavailable)
	})

	t.Run("valid non-profile gzip is not a content conflict", func(t *testing.T) {
		fixture := newArtifactContentTestFixture(t)
		alternate := append([]byte(nil), fixture.rawCompressed...)
		if len(alternate) < 10 {
			t.Fatal("gzip fixture is unexpectedly short")
		}
		alternate[9] = 3
		fixture.rawCompressed = alternate
		refreshRawSnapshotAndManifest(t, &fixture)
		assertFixtureReason(t, fixture, ArtifactReasonCodecProfileUnavailable)
	})
}

func TestTelemetryArtifactValidatorRegistryRejectsDuplicateVersionsRegardlessOfOrder(t *testing.T) {
	fixture := newArtifactContentTestFixture(t)
	validProfile := currentTelemetryArtifactValidatorProfile()
	malformedProfile := currentTelemetryArtifactValidatorProfile()
	malformedProfile.decode = nil

	tests := []struct {
		name     string
		profiles []telemetryArtifactValidatorProfile
	}{
		{name: "malformed then valid", profiles: []telemetryArtifactValidatorProfile{malformedProfile, validProfile}},
		{name: "valid then malformed", profiles: []telemetryArtifactValidatorProfile{validProfile, malformedProfile}},
		{name: "valid duplicate", profiles: []telemetryArtifactValidatorProfile{validProfile, validProfile}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validator := newTelemetryArtifactContentValidatorWithProfiles(test.profiles...)
			result := validator.ValidateRaw(
				fixture.request,
				fixture.rawSnapshot,
				fixture.rawCompressed,
			)
			assertArtifactContentResult(
				t,
				result,
				artifactContentValidationInvalid,
				ArtifactReasonValidatorUnavailable,
			)
		})
	}
}

func TestTelemetryArtifactContentResultCannotCarrySourceMaterial(t *testing.T) {
	resultType := reflect.TypeOf(artifactContentValidationResult{})
	if resultType.NumField() != 2 || resultType.Field(0).Type.Kind() == reflect.Slice ||
		resultType.Field(1).Type.Kind() == reflect.Slice {
		t.Fatalf("artifact content result shape can retain source material: %v", resultType)
	}
}

func TestTelemetryCodecGoldenCompressedDigest(t *testing.T) {
	compressed, err := deterministicGZIP([]byte(telemetryCodecGoldenRawV1))
	if err != nil {
		t.Fatalf("deterministicGZIP() error = %v", err)
	}
	if got := ComputeArtifactDigest(compressed).SHA256; got != telemetryCodecGoldenCompressedSHA256V1 {
		t.Fatalf("codec golden digest = %s, want %s", got, telemetryCodecGoldenCompressedSHA256V1)
	}
}

func newArtifactContentTestFixture(t *testing.T) artifactContentTestFixture {
	t.Helper()
	rawBody := []byte(telemetryCodecGoldenRawV1)
	batch, err := telemetry.DecodeBatch(bytes.NewReader(rawBody))
	if err != nil || batch.Validate() != nil {
		t.Fatalf("invalid synthetic raw fixture")
	}
	firstCapturedAt, lastCapturedAt := capturedAtBounds(batch)
	receivedAt := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	bodyHash := hashBytes(rawBody)
	request := ArtifactClassificationRequest{
		Purpose:              ArtifactReadForwardRecovery,
		ReceiptID:            "99999999-9999-4999-8999-999999999999",
		ReceiptState:         ReceiptReserved,
		ReceiptRevision:      1,
		TenantID:             batch.TenantID,
		DeviceID:             batch.DeviceID,
		TripID:               batch.TripID,
		InstallationID:       batch.InstallationID,
		BatchID:              "99999999-9999-4999-8999-999999999999",
		ClientBatchID:        batch.ClientBatchID,
		ConsentRevisionID:    batch.ConsentRevisionID,
		PayloadSchemaVersion: batch.SchemaVersion,
		ValidatorVersion:     TelemetryValidatorVersion,
		BodyHash:             bodyHash,
		ExpectedSampleCount:  len(batch.Samples),
		FirstCapturedAt:      firstCapturedAt,
		LastCapturedAt:       lastCapturedAt,
		ReceivedAt:           receivedAt,
		ArtifactExpiresAt:    receivedAt.Add(TelemetryArtifactRetention),
		ForwardFence: &LeaseFence{
			OwnerID:   "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			Token:     1,
			ExpiresAt: receivedAt.Add(time.Hour),
		},
	}
	request.ReservationKey = DeriveReservationKey(
		request.PayloadSchemaVersion,
		request.TenantID,
		request.InstallationID,
		request.ClientBatchID,
	)
	manifestInput := manifestInputFromClassificationRequest(request)
	request.ExpectedRawPath = ExpectedTelemetryObjectPath(manifestInput)
	request.ExpectedManifestPath = ExpectedTelemetryManifestPath(manifestInput)
	if err := ValidateArtifactClassificationRequest(request); err != nil {
		t.Fatalf("fixture request is invalid: %v", err)
	}

	compressed, err := deterministicGZIP(rawBody)
	if err != nil {
		t.Fatalf("compress fixture: %v", err)
	}
	rawDigest := ComputeArtifactDigest(compressed)
	rawSnapshot := ArtifactSnapshot{
		Path:            request.ExpectedRawPath,
		SHA256:          rawDigest.SHA256,
		CRC32C:          rawDigest.CRC32C,
		Size:            rawDigest.Size,
		Generation:      101,
		Metageneration:  1,
		ContentType:     TelemetryContentType,
		ContentEncoding: TelemetryCompression,
		CacheControl:    artifactCacheControlNoStore,
		Metadata: map[string]string{
			"artifact_kind":    "telemetry_raw",
			"artifact_version": fmt.Sprint(TelemetryManifestVersion),
			"batch_id":         request.BatchID,
			"body_sha256":      request.BodyHash,
			"expires_at":       canonicalTime(request.ArtifactExpiresAt),
			"sha256":           rawDigest.SHA256,
			"tenant_id":        request.TenantID,
		},
	}
	fixture := artifactContentTestFixture{
		request:       request,
		rawSnapshot:   rawSnapshot,
		rawCompressed: compressed,
	}
	refreshCanonicalManifest(t, &fixture)
	return fixture
}

func acceptedArtifactContentTestFixture(t *testing.T) artifactContentTestFixture {
	t.Helper()
	fixture := newArtifactContentTestFixture(t)
	fixture.request.Purpose = ArtifactReadAcceptedIntegrityAudit
	fixture.request.ReceiptState = ReceiptStored
	fixture.request.ForwardFence = nil
	fixture.request.AcceptedRawLineage = lineageFromSnapshot(fixture.rawSnapshot)
	fixture.request.AcceptedManifestLineage = lineageFromSnapshot(fixture.manifestSnapshot)
	if err := ValidateArtifactClassificationRequest(fixture.request); err != nil {
		t.Fatalf("accepted fixture request is invalid: %v", err)
	}
	return fixture
}

func lineageFromSnapshot(snapshot ArtifactSnapshot) *ArtifactLineage {
	return &ArtifactLineage{
		Path:           snapshot.Path,
		SHA256:         snapshot.SHA256,
		CRC32C:         snapshot.CRC32C,
		Size:           snapshot.Size,
		Generation:     snapshot.Generation,
		Metageneration: snapshot.Metageneration,
	}
}

func currentTelemetryArtifactValidatorProfile() telemetryArtifactValidatorProfile {
	return telemetryArtifactValidatorProfile{
		version:              TelemetryValidatorVersion,
		decode:               telemetry.DecodeBatch,
		validate:             func(batch telemetry.Batch) *telemetry.ValidationError { return batch.Validate() },
		buildManifest:        CanonicalTelemetryManifest,
		maxDecompressedBytes: maxTelemetryArtifactBodyBytes,
		codec: telemetryArtifactCodecProfile{
			name:                   telemetryGZIPCodecProfileV1,
			compress:               deterministicGZIP,
			goldenRaw:              []byte(telemetryCodecGoldenRawV1),
			goldenCompressedSHA256: telemetryCodecGoldenCompressedSHA256V1,
		},
	}
}

func refreshCanonicalManifest(t *testing.T, fixture *artifactContentTestFixture) {
	t.Helper()
	manifestBytes, manifestDigest, err := CanonicalTelemetryManifest(
		manifestInputFromClassificationRequest(fixture.request),
		storedArtifactFromSnapshot(fixture.rawSnapshot),
	)
	if err != nil {
		t.Fatalf("build canonical manifest: %v", err)
	}
	fixture.manifestBytes = manifestBytes
	fixture.manifestSnapshot = ArtifactSnapshot{
		Path:           fixture.request.ExpectedManifestPath,
		SHA256:         manifestDigest.SHA256,
		CRC32C:         manifestDigest.CRC32C,
		Size:           manifestDigest.Size,
		Generation:     202,
		Metageneration: 1,
		ContentType:    TelemetryContentType,
		CacheControl:   artifactCacheControlNoStore,
		Metadata: map[string]string{
			"artifact_kind":     "telemetry_manifest",
			"artifact_version":  fmt.Sprint(TelemetryManifestVersion),
			"batch_id":          fixture.request.BatchID,
			"expires_at":        canonicalTime(fixture.request.ArtifactExpiresAt),
			"object_generation": fmt.Sprint(fixture.rawSnapshot.Generation),
			"sha256":            manifestDigest.SHA256,
			"tenant_id":         fixture.request.TenantID,
		},
	}
}

func refreshRawSnapshotAndManifest(t *testing.T, fixture *artifactContentTestFixture) {
	t.Helper()
	digest := ComputeArtifactDigest(fixture.rawCompressed)
	fixture.rawSnapshot.SHA256 = digest.SHA256
	fixture.rawSnapshot.CRC32C = digest.CRC32C
	fixture.rawSnapshot.Size = digest.Size
	fixture.rawSnapshot.Metadata["body_sha256"] = fixture.request.BodyHash
	fixture.rawSnapshot.Metadata["sha256"] = digest.SHA256
	refreshCanonicalManifest(t, fixture)
}

func setRawBody(t *testing.T, fixture *artifactContentTestFixture, raw []byte, bindBodyHash bool) {
	t.Helper()
	compressed, err := deterministicGZIP(raw)
	if err != nil {
		t.Fatalf("compress raw fixture: %v", err)
	}
	if bindBodyHash {
		fixture.request.BodyHash = hashBytes(raw)
	}
	fixture.rawCompressed = compressed
	refreshRawSnapshotAndManifest(t, fixture)
}

func replaceManifestBytes(t *testing.T, fixture *artifactContentTestFixture, raw []byte) {
	t.Helper()
	fixture.manifestBytes = append([]byte(nil), raw...)
	digest := ComputeArtifactDigest(raw)
	fixture.manifestSnapshot.SHA256 = digest.SHA256
	fixture.manifestSnapshot.CRC32C = digest.CRC32C
	fixture.manifestSnapshot.Size = digest.Size
	fixture.manifestSnapshot.Metadata["sha256"] = digest.SHA256
}

func decodeManifestFixture(t *testing.T, raw []byte) TelemetryManifest {
	t.Helper()
	value, err := DecodeTelemetryManifestV1(raw)
	if err != nil {
		t.Fatalf("decode fixture manifest: %v", err)
	}
	return value
}

func mutateManifestFixture(
	mutate func(*TelemetryManifest),
) func(*testing.T, artifactContentTestFixture) []byte {
	return func(t *testing.T, fixture artifactContentTestFixture) []byte {
		value := decodeManifestFixture(t, fixture.manifestBytes)
		mutate(&value)
		return marshalJSON(t, value)
	}
}

func replaceRawString(oldValue, newValue string) func(*testing.T, []byte) []byte {
	return func(t *testing.T, raw []byte) []byte {
		t.Helper()
		updated := bytes.Replace(raw, []byte(oldValue), []byte(newValue), 1)
		if bytes.Equal(updated, raw) {
			t.Fatal("raw fixture replacement did not match")
		}
		return updated
	}
}

func validateFixture(fixture artifactContentTestFixture) artifactContentValidationResult {
	return newTelemetryArtifactContentValidator().Validate(
		fixture.request,
		fixture.manifestSnapshot,
		fixture.manifestBytes,
		fixture.rawSnapshot,
		fixture.rawCompressed,
	)
}

func assertFixtureReason(t *testing.T, fixture artifactContentTestFixture, reason ArtifactReasonCode) {
	t.Helper()
	assertArtifactContentResult(t, validateFixture(fixture), artifactContentValidationInvalid, reason)
}

func assertArtifactContentResult(
	t *testing.T,
	result artifactContentValidationResult,
	status artifactContentValidationStatus,
	reason ArtifactReasonCode,
) {
	t.Helper()
	if result.Status != status || result.ReasonCode != reason {
		t.Fatalf("validation result = %#v, want status=%q reason=%q", result, status, reason)
	}
}

func appendBeforeFinalBrace(raw []byte, addition string) []byte {
	result := append([]byte(nil), raw[:len(raw)-1]...)
	result = append(result, addition...)
	return append(result, '}')
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return raw
}

func hashBytes(raw []byte) string {
	return ComputeArtifactDigest(raw).SHA256
}

func cloneJSONMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func addTestbenchMetadata(metadata map[string]string, checksum uint32) {
	var encoded [4]byte
	encoded[0] = byte(checksum >> 24)
	encoded[1] = byte(checksum >> 16)
	encoded[2] = byte(checksum >> 8)
	encoded[3] = byte(checksum)
	checksumValue := base64.StdEncoding.EncodeToString(encoded[:])
	metadata["x_emulator_crc32c"] = checksumValue
	metadata["x_emulator_upload"] = "multipart"
	metadata["x_testbench_crc32c"] = checksumValue
	metadata["x_testbench_upload"] = "multipart"
}
