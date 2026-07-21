package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

func TestAPIIsNotReadyWithoutAdapters(t *testing.T) {
	api := NewAPI(nil, nil).Routes()

	for _, path := range []string{"/readyz", "/v1/telemetry/batches"} {
		method := http.MethodGet
		if strings.Contains(path, "telemetry") {
			method = http.MethodPost
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
}

func TestAPIRejectsUnauthenticatedBeforeIngest(t *testing.T) {
	called := false
	api := NewAPI(
		verifierFunc(func(*http.Request) (ingest.Principal, error) {
			return ingest.Principal{}, ErrUnauthenticated
		}),
		ingestorFunc(func(context.Context, ingest.Principal, telemetry.Batch, []byte) (ingest.Result, error) {
			called = true
			return ingest.Result{}, nil
		}),
	).Routes()

	response := performJSON(api, validPayload(t))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if called {
		t.Fatal("ingestor called for unauthenticated request")
	}
}

func TestVerifierCannotConsumeTelemetryBody(t *testing.T) {
	payload := validPayload(t)
	api := NewAPI(
		verifierFunc(func(request *http.Request) (ingest.Principal, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read verifier body: %v", err)
			}
			if len(body) != 0 {
				t.Fatalf("verifier received %d telemetry bytes", len(body))
			}
			return successPrincipal(), nil
		}),
		ingestorFunc(func(
			_ context.Context,
			_ ingest.Principal,
			batch telemetry.Batch,
			raw []byte,
		) (ingest.Result, error) {
			if !bytes.Equal(raw, payload) {
				t.Fatal("ingestor did not receive the original body")
			}
			return successfulResult(batch, false), nil
		}),
	).Routes()

	response := performJSON(api, payload)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAPIRejectsOversizedBody(t *testing.T) {
	api := NewAPI(successVerifier(), successIngestor(false)).Routes()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/telemetry/batches",
		bytes.NewReader(bytes.Repeat([]byte("x"), int(MaxRequestBytes)+1)),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAPIRejectsInvalidJSONWithoutEchoingBody(t *testing.T) {
	sensitiveCoordinate := "91.123456789"
	payload := bytes.Replace(
		validPayload(t),
		[]byte(`"latitude": 37.5665`),
		[]byte(`"latitude": `+sensitiveCoordinate+`, "unknown": true`),
		1,
	)
	api := NewAPI(successVerifier(), successIngestor(false)).Routes()
	response := performJSON(api, payload)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), sensitiveCoordinate) {
		t.Fatal("error response leaked coordinate")
	}
}

func TestAPIMapsValidationErrorWithoutCoordinateValue(t *testing.T) {
	sensitiveCoordinate := "91.123456789"
	payload := bytes.Replace(
		validPayload(t),
		[]byte(`"latitude": 37.5665`),
		[]byte(`"latitude": `+sensitiveCoordinate),
		1,
	)
	api := NewAPI(successVerifier(), successIngestor(false)).Routes()
	response := performJSON(api, payload)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"field":"samples[0].latitude"`) {
		t.Fatalf("validation field missing: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), sensitiveCoordinate) {
		t.Fatal("validation response leaked coordinate")
	}
}

func TestAPIReturnsAcceptedAndReplayStatuses(t *testing.T) {
	tests := []struct {
		name       string
		replay     bool
		wantStatus int
	}{
		{name: "accepted", replay: false, wantStatus: http.StatusAccepted},
		{name: "replay", replay: true, wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := NewAPI(successVerifier(), successIngestor(test.replay)).Routes()
			response := performJSON(api, validPayload(t))
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"receiptId"`) {
				t.Fatalf("receipt response missing: %s", response.Body.String())
			}
		})
	}
}

func TestAPIMapsIdempotencyConflict(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "idempotency", err: ingest.ErrIdempotencyConflict, wantCode: "idempotency_conflict"},
		{name: "batch id", err: ingest.ErrBatchIDConflict, wantCode: "batch_conflict"},
		{name: "object", err: ingest.ErrObjectConflict, wantCode: "object_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := NewAPI(
				successVerifier(),
				ingestorFunc(func(context.Context, ingest.Principal, telemetry.Batch, []byte) (ingest.Result, error) {
					return ingest.Result{}, test.err
				}),
			).Routes()
			response := performJSON(api, validPayload(t))
			if response.Code != http.StatusConflict {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), test.wantCode) {
				t.Fatalf("body = %s", response.Body.String())
			}
		})
	}
}

func TestAPIMapsBatchAuthorizationFailure(t *testing.T) {
	api := NewAPI(
		successVerifier(),
		ingestorFunc(func(context.Context, ingest.Principal, telemetry.Batch, []byte) (ingest.Result, error) {
			return ingest.Result{}, ingest.ErrBatchUnauthorized
		}),
	).Routes()
	response := performJSON(api, validPayload(t))
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "batch_unauthorized") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

type verifierFunc func(*http.Request) (ingest.Principal, error)

func (f verifierFunc) Verify(request *http.Request) (ingest.Principal, error) {
	return f(request)
}

type ingestorFunc func(context.Context, ingest.Principal, telemetry.Batch, []byte) (ingest.Result, error)

func (f ingestorFunc) Ingest(
	ctx context.Context,
	principal ingest.Principal,
	batch telemetry.Batch,
	raw []byte,
) (ingest.Result, error) {
	return f(ctx, principal, batch, raw)
}

func successVerifier() PrincipalVerifier {
	return verifierFunc(func(*http.Request) (ingest.Principal, error) {
		return successPrincipal(), nil
	})
}

func successPrincipal() ingest.Principal {
	return ingest.Principal{
		TenantID: "00000000-0000-4000-8000-000000000010",
		ActorID:  "00000000-0000-4000-8000-000000000011",
	}
}

func successIngestor(replay bool) Ingestor {
	return ingestorFunc(func(
		_ context.Context,
		_ ingest.Principal,
		batch telemetry.Batch,
		_ []byte,
	) (ingest.Result, error) {
		return successfulResult(batch, replay), nil
	})
}

func successfulResult(batch telemetry.Batch, replay bool) ingest.Result {
	return ingest.Result{
		Receipt: ingest.Receipt{
			ReceiptID:   batch.BatchID,
			State:       ingest.ReceiptStored,
			SampleCount: len(batch.Samples),
		},
		Replay: replay,
	}
}

func performJSON(handler http.Handler, payload []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/telemetry/batches", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func validPayload(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "packages", "contracts", "fixtures", "telemetry-batch.valid.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return payload
}
