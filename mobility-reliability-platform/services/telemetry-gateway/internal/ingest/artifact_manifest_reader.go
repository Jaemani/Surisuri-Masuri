package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

// MaxTelemetryManifestBytes bounds the server-produced control artifact before
// tokenization or typed decoding. A canonical v1 manifest is much smaller;
// the headroom permits longer validated paths without accepting an unbounded
// provider object.
const MaxTelemetryManifestBytes = 64 * 1024

// DecodeTelemetryManifestV1 decodes one strict manifest document. It performs
// syntax and version-shape validation only: receipt cross-lineage, artifact
// attributes, hashes, paths, and generations belong to the read-only
// classifier that consumes the result.
//
// Errors contain only stable field labels. The rejected manifest bytes are
// never returned or embedded in an error.
func DecodeTelemetryManifestV1(raw []byte) (TelemetryManifest, error) {
	if len(raw) == 0 || len(raw) > MaxTelemetryManifestBytes {
		return TelemetryManifest{}, invalidManifest("manifest_size")
	}
	if !utf8.Valid(raw) {
		return TelemetryManifest{}, invalidManifest("manifest_utf8")
	}
	if err := rejectDuplicateManifestJSONKeys(raw); err != nil {
		return TelemetryManifest{}, invalidManifest("manifest_json")
	}

	var manifest TelemetryManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return TelemetryManifest{}, invalidManifest("manifest_json")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return TelemetryManifest{}, invalidManifest("manifest_json")
	}

	if manifest.ManifestVersion != TelemetryManifestVersion {
		return TelemetryManifest{}, invalidManifest("manifest_version")
	}
	if manifest.PayloadSchemaVersion != telemetry.SchemaVersionV2 {
		return TelemetryManifest{}, invalidManifest("payload_schema_version")
	}
	if manifest.Compression != TelemetryCompression {
		return TelemetryManifest{}, invalidManifest("compression")
	}
	if manifest.ContentType != TelemetryContentType {
		return TelemetryManifest{}, invalidManifest("content_type")
	}
	for _, timestamp := range []struct {
		field string
		value string
	}{
		{field: "first_captured_at", value: manifest.FirstCapturedAt},
		{field: "last_captured_at", value: manifest.LastCapturedAt},
		{field: "received_at", value: manifest.ReceivedAt},
		{field: "expires_at", value: manifest.ExpiresAt},
	} {
		if _, err := time.Parse(time.RFC3339Nano, timestamp.value); err != nil {
			return TelemetryManifest{}, invalidManifest(timestamp.field)
		}
	}

	return manifest, nil
}

// rejectDuplicateManifestJSONKeys walks the entire token stream because
// encoding/json otherwise keeps the last duplicate object member. The caller
// normalizes every failure so parser details and input fragments do not escape.
func rejectDuplicateManifestJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkManifestJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing manifest JSON")
		}
		return err
	}
	return nil
}

func walkManifestJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}

	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid manifest object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate manifest JSON key")
			}
			seen[key] = struct{}{}
			if err := walkManifestJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("invalid manifest object")
		}
	case '[':
		for decoder.More() {
			if err := walkManifestJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("invalid manifest array")
		}
	default:
		return errors.New("unexpected manifest JSON delimiter")
	}
	return nil
}
