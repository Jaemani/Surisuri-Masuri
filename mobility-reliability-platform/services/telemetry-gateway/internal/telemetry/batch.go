// Package telemetry defines the versioned wire contract accepted by the
// telemetry gateway. Validation errors deliberately contain metadata only:
// raw telemetry values, especially coordinates, must never be copied into an
// error string or log field.
package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersionV1 = "telemetry-batch.v1"
	SourcePhoneGPS  = "phone_gps"
	MaxSamples      = 500
)

// Batch is the telemetry-batch.v1 JSON wire type.
type Batch struct {
	SchemaVersion    string   `json:"schemaVersion"`
	BatchID          string   `json:"batchId"`
	IdempotencyKey   string   `json:"idempotencyKey"`
	TenantID         string   `json:"tenantId"`
	ActorID          string   `json:"actorId"`
	MobilityDeviceID string   `json:"mobilityDeviceId"`
	SessionID        string   `json:"sessionId"`
	ConsentVersion   string   `json:"consentVersion"`
	SentAt           string   `json:"sentAt"`
	Samples          []Sample `json:"samples"`
}

// Sample is one smartphone GPS observation in a Batch. Pointers on required
// numeric fields preserve the difference between an omitted field and zero.
type Sample struct {
	SampleID            string         `json:"sampleId"`
	Sequence            *int64         `json:"sequence"`
	CapturedAt          string         `json:"capturedAt"`
	Latitude            *float64       `json:"latitude"`
	Longitude           *float64       `json:"longitude"`
	HorizontalAccuracyM NullableFloat  `json:"horizontalAccuracyM"`
	AltitudeM           *float64       `json:"altitudeM,omitempty"`
	SpeedMPS            *float64       `json:"speedMps,omitempty"`
	HeadingDegrees      *float64       `json:"headingDegrees,omitempty"`
	ActivityHint        OptionalString `json:"activityHint,omitempty"`
	IsMockLocation      *bool          `json:"isMockLocation,omitempty"`
	Source              string         `json:"source"`
}

// OptionalString preserves omission versus an explicit JSON null. This is
// needed for optional schema fields that allow a string but do not allow null.
type OptionalString struct {
	Value string
	Valid bool
	Set   bool
}

func (o *OptionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = ""
		o.Valid = false
		return nil
	}

	if err := json.Unmarshal(data, &o.Value); err != nil {
		return err
	}
	o.Valid = true
	return nil
}

// NullableFloat represents a JSON number that can also be explicitly null.
// Set distinguishes a required null value from an omitted field.
type NullableFloat struct {
	Value float64
	Valid bool
	Set   bool
}

func (n *NullableFloat) UnmarshalJSON(data []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		n.Value = 0
		n.Valid = false
		return nil
	}

	var value float64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	n.Value = value
	n.Valid = true
	return nil
}

func (n NullableFloat) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Value)
}

// ValidationError intentionally carries only a safe field path and stable
// machine-readable code. It must never be extended with the rejected value.
type ValidationError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Code
}

// DecodeBatch decodes exactly one telemetry batch. Unknown object fields and
// trailing JSON values are rejected before validation.
func DecodeBatch(r io.Reader) (Batch, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Batch{}, err
	}
	if !utf8.Valid(raw) {
		return Batch{}, errors.New("invalid_utf8")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return Batch{}, err
	}

	var batch Batch
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&batch); err != nil {
		return Batch{}, err
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Batch{}, errors.New("trailing_json")
		}
		return Batch{}, fmt.Errorf("trailing_json: %w", err)
	}

	return batch, nil
}

// Validate returns the first contract violation in deterministic wire order.
// A nil result means the batch satisfies telemetry-batch.v1.
func (b Batch) Validate() *ValidationError {
	if b.SchemaVersion != SchemaVersionV1 {
		return invalid("schemaVersion", "const")
	}
	if !isUUID(b.BatchID) {
		return invalid("batchId", "uuid")
	}
	if length := utf8.RuneCountInString(b.IdempotencyKey); length < 16 || length > 128 {
		return invalid("idempotencyKey", "length")
	}
	if !isUUID(b.TenantID) {
		return invalid("tenantId", "uuid")
	}
	if !isUUID(b.ActorID) {
		return invalid("actorId", "uuid")
	}
	if !isUUID(b.MobilityDeviceID) {
		return invalid("mobilityDeviceId", "uuid")
	}
	if !isUUID(b.SessionID) {
		return invalid("sessionId", "uuid")
	}
	if length := utf8.RuneCountInString(b.ConsentVersion); length < 1 || length > 64 {
		return invalid("consentVersion", "length")
	}
	if !isDateTime(b.SentAt) {
		return invalid("sentAt", "date_time")
	}
	if len(b.Samples) < 1 || len(b.Samples) > MaxSamples {
		return invalid("samples", "length")
	}

	for i := range b.Samples {
		if err := b.Samples[i].validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (s Sample) validate(index int) *ValidationError {
	field := func(name string) string {
		return fmt.Sprintf("samples[%d].%s", index, name)
	}

	if !isUUID(s.SampleID) {
		return invalid(field("sampleId"), "uuid")
	}
	if s.Sequence == nil {
		return invalid(field("sequence"), "required")
	}
	if *s.Sequence < 0 {
		return invalid(field("sequence"), "minimum")
	}
	if !isDateTime(s.CapturedAt) {
		return invalid(field("capturedAt"), "date_time")
	}
	if s.Latitude == nil {
		return invalid(field("latitude"), "required")
	}
	if !isFinite(*s.Latitude) || *s.Latitude < -90 || *s.Latitude > 90 {
		return invalid(field("latitude"), "range")
	}
	if s.Longitude == nil {
		return invalid(field("longitude"), "required")
	}
	if !isFinite(*s.Longitude) || *s.Longitude < -180 || *s.Longitude > 180 {
		return invalid(field("longitude"), "range")
	}
	if !s.HorizontalAccuracyM.Set {
		return invalid(field("horizontalAccuracyM"), "required")
	}
	if s.HorizontalAccuracyM.Valid && (!isFinite(s.HorizontalAccuracyM.Value) || s.HorizontalAccuracyM.Value < 0) {
		return invalid(field("horizontalAccuracyM"), "minimum")
	}
	if s.AltitudeM != nil && !isFinite(*s.AltitudeM) {
		return invalid(field("altitudeM"), "finite")
	}
	if s.SpeedMPS != nil && (!isFinite(*s.SpeedMPS) || *s.SpeedMPS < 0) {
		return invalid(field("speedMps"), "minimum")
	}
	if s.HeadingDegrees != nil && (!isFinite(*s.HeadingDegrees) || *s.HeadingDegrees < 0 || *s.HeadingDegrees >= 360) {
		return invalid(field("headingDegrees"), "range")
	}
	if s.ActivityHint.Set {
		if !s.ActivityHint.Valid {
			return invalid(field("activityHint"), "type")
		}
		if !validActivityHint(s.ActivityHint.Value) {
			return invalid(field("activityHint"), "enum")
		}
	}
	if s.Source != SourcePhoneGPS {
		return invalid(field("source"), "const")
	}
	return nil
}

// rejectDuplicateJSONKeys walks the JSON token stream before typed decoding.
// encoding/json otherwise accepts duplicate object keys and keeps the last
// value, which can make downstream parsers disagree about signed telemetry.
func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing_json")
		}
		return fmt.Errorf("trailing_json: %w", err)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
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
				return errors.New("invalid_object_key")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate_json_key")
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("invalid_object")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("invalid_array")
		}
	default:
		return errors.New("unexpected_json_delimiter")
	}
	return nil
}

func invalid(field, code string) *ValidationError {
	return &ValidationError{Field: field, Code: code}
}

func isDateTime(value string) bool {
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validActivityHint(value string) bool {
	switch value {
	case "unknown", "stationary", "walking", "wheeled", "motor_vehicle":
		return true
	default:
		return false
	}
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i := range len(value) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		char := value[i]
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
