package telemetry

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeAndValidateFixture(t *testing.T) {
	payload := readFixture(t, "telemetry-batch.v2.valid.json")

	batch, err := DecodeBatch(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("DecodeBatch() error = %v", err)
	}
	if validationErr := batch.Validate(); validationErr != nil {
		t.Fatalf("Batch.Validate() error = %v", validationErr)
	}
	if batch.SchemaVersion != SchemaVersionV2 {
		t.Fatalf("SchemaVersion = %q", batch.SchemaVersion)
	}
	if len(batch.Samples) != 1 || batch.Samples[0].Latitude == nil {
		t.Fatal("valid fixture did not preserve its required sample")
	}
}

func TestDecodeBatchRejectsUnknownFields(t *testing.T) {
	payload := strings.Replace(
		string(readFixture(t, "telemetry-batch.v2.valid.json")),
		`"source": "phone_gps"`,
		`"source": "phone_gps", "unexpectedCoordinateMetadata": true`,
		1,
	)

	_, err := DecodeBatch(strings.NewReader(payload))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeBatch() error = %v, want unknown-field rejection", err)
	}
}

func TestDecodeBatchRejectsTrailingJSON(t *testing.T) {
	payload := append(readFixture(t, "telemetry-batch.v2.valid.json"), []byte(` {"another":true}`)...)

	_, err := DecodeBatch(bytes.NewReader(payload))
	if err == nil || !strings.Contains(err.Error(), "trailing_json") {
		t.Fatalf("DecodeBatch() error = %v, want trailing_json", err)
	}
}

func TestDecodeBatchRejectsDuplicateKeys(t *testing.T) {
	payload := strings.Replace(
		string(readFixture(t, "telemetry-batch.v2.valid.json")),
		`"source": "phone_gps"`,
		`"source": "phone_gps", "source": "phone_gps"`,
		1,
	)

	_, err := DecodeBatch(strings.NewReader(payload))
	if err == nil || !strings.Contains(err.Error(), "duplicate_json_key") {
		t.Fatalf("DecodeBatch() error = %v, want duplicate-key rejection", err)
	}
}

func TestDecodeBatchRejectsInvalidUTF8(t *testing.T) {
	payload := readFixture(t, "telemetry-batch.v2.valid.json")
	payload = bytes.Replace(
		payload,
		[]byte("79db4fe2-525f-4fc5-9ea3-ccf2f2a78d75"),
		[]byte{'v', 0xff},
		1,
	)

	_, err := DecodeBatch(bytes.NewReader(payload))
	if err == nil || !strings.Contains(err.Error(), "invalid_utf8") {
		t.Fatalf("DecodeBatch() error = %v, want UTF-8 rejection", err)
	}
}

func TestValidateBounds(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Batch)
		wantField string
		wantCode  string
	}{
		{
			name: "latitude",
			mutate: func(batch *Batch) {
				*batch.Samples[0].Latitude = 90.0001
			},
			wantField: "samples[0].latitude",
			wantCode:  "range",
		},
		{
			name: "longitude",
			mutate: func(batch *Batch) {
				*batch.Samples[0].Longitude = -180.0001
			},
			wantField: "samples[0].longitude",
			wantCode:  "range",
		},
		{
			name: "accuracy",
			mutate: func(batch *Batch) {
				batch.Samples[0].HorizontalAccuracyM = NullableFloat{Set: true, Valid: true, Value: -0.01}
			},
			wantField: "samples[0].horizontalAccuracyM",
			wantCode:  "minimum",
		},
		{
			name: "speed",
			mutate: func(batch *Batch) {
				value := -0.01
				batch.Samples[0].SpeedMPS = &value
			},
			wantField: "samples[0].speedMps",
			wantCode:  "minimum",
		},
		{
			name: "heading exclusive upper bound",
			mutate: func(batch *Batch) {
				value := 360.0
				batch.Samples[0].HeadingDegrees = &value
			},
			wantField: "samples[0].headingDegrees",
			wantCode:  "range",
		},
		{
			name: "sample count",
			mutate: func(batch *Batch) {
				batch.Samples = make([]Sample, MaxSamples+1)
			},
			wantField: "samples",
			wantCode:  "length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := validBatch(t)
			tt.mutate(&batch)
			err := batch.Validate()
			if err == nil {
				t.Fatal("Batch.Validate() error = nil")
			}
			if err.Field != tt.wantField || err.Code != tt.wantCode {
				t.Fatalf("Batch.Validate() error = %#v", err)
			}
		})
	}
}

func TestDecodeBatchRejectsNaNAsInvalidJSON(t *testing.T) {
	payload := strings.Replace(
		string(readFixture(t, "telemetry-batch.v2.valid.json")),
		`"latitude": 37.5665`,
		`"latitude": NaN`,
		1,
	)

	if _, err := DecodeBatch(strings.NewReader(payload)); err == nil {
		t.Fatal("DecodeBatch() error = nil, want invalid JSON rejection")
	}
}

func TestValidationErrorDoesNotExposeCoordinateValue(t *testing.T) {
	batch := validBatch(t)
	sensitiveCoordinate := 91.123456789
	batch.Samples[0].Latitude = &sensitiveCoordinate

	err := batch.Validate()
	if err == nil {
		t.Fatal("Batch.Validate() error = nil")
	}
	if strings.Contains(err.Error(), "91.123456789") {
		t.Fatalf("validation error leaked a coordinate: %q", err.Error())
	}
	if err.Field != "samples[0].latitude" || err.Code != "range" {
		t.Fatalf("Batch.Validate() error = %#v", err)
	}
}

func TestHorizontalAccuracyAllowsExplicitNullButRequiresField(t *testing.T) {
	payload := strings.Replace(
		string(readFixture(t, "telemetry-batch.v2.valid.json")),
		`"horizontalAccuracyM": 8.5`,
		`"horizontalAccuracyM": null`,
		1,
	)
	batch, err := DecodeBatch(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("DecodeBatch() error = %v", err)
	}
	if validationErr := batch.Validate(); validationErr != nil {
		t.Fatalf("explicit null should be valid: %v", validationErr)
	}

	batch.HorizontalAccuracyUnsetForTest()
	validationErr := batch.Validate()
	if validationErr == nil || validationErr.Field != "samples[0].horizontalAccuracyM" || validationErr.Code != "required" {
		t.Fatalf("Batch.Validate() error = %#v", validationErr)
	}
}

func TestActivityHintRejectsExplicitNull(t *testing.T) {
	payload := strings.Replace(
		string(readFixture(t, "telemetry-batch.v2.valid.json")),
		`"activityHint": "wheeled"`,
		`"activityHint": null`,
		1,
	)
	batch, err := DecodeBatch(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("DecodeBatch() error = %v", err)
	}
	validationErr := batch.Validate()
	if validationErr == nil || validationErr.Field != "samples[0].activityHint" || validationErr.Code != "type" {
		t.Fatalf("Batch.Validate() error = %#v", validationErr)
	}
}

func (b *Batch) HorizontalAccuracyUnsetForTest() {
	b.Samples[0].HorizontalAccuracyM = NullableFloat{}
}

func validBatch(t *testing.T) Batch {
	t.Helper()
	batch, err := DecodeBatch(bytes.NewReader(readFixture(t, "telemetry-batch.v2.valid.json")))
	if err != nil {
		t.Fatalf("DecodeBatch() error = %v", err)
	}
	return batch
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "packages", "contracts", "fixtures", name)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return payload
}
