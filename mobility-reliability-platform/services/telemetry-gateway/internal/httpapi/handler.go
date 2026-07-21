package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

const MaxRequestBytes int64 = 2 * 1024 * 1024

var (
	ErrUnauthenticated     = errors.New("request is not authenticated")
	ErrVerifierUnavailable = errors.New("principal verifier is unavailable")
)

type PrincipalVerifier interface {
	Verify(*http.Request) (ingest.Principal, error)
}

type Ingestor interface {
	Ingest(context.Context, ingest.Principal, telemetry.Batch, []byte) (ingest.Result, error)
}

type API struct {
	verifier PrincipalVerifier
	ingestor Ingestor
}

func NewAPI(verifier PrincipalVerifier, ingestor Ingestor) *API {
	return &API{verifier: verifier, ingestor: ingestor}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("POST /v1/telemetry/batches", a.ingestBatch)
	return securityHeaders(mux)
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (a *API) ready(w http.ResponseWriter, _ *http.Request) {
	if a.verifier == nil || a.ingestor == nil {
		writeError(w, http.StatusServiceUnavailable, "adapters_unconfigured", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (a *API) ingestBatch(w http.ResponseWriter, r *http.Request) {
	if a.verifier == nil || a.ingestor == nil {
		writeError(w, http.StatusServiceUnavailable, "adapters_unconfigured", "")
		return
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "content_type", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
	if r.ContentLength > MaxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "")
		return
	}

	// Verifiers are header-only. Supplying an empty body prevents a future
	// adapter from consuming or buffering telemetry outside the size boundary.
	verificationRequest := r.Clone(r.Context())
	verificationRequest.Body = http.NoBody
	verificationRequest.ContentLength = 0
	principal, err := a.verifier.Verify(verificationRequest)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnauthenticated):
			writeError(w, http.StatusUnauthorized, "unauthenticated", "")
		case errors.Is(err, ErrVerifierUnavailable):
			writeError(w, http.StatusServiceUnavailable, "verifier_unavailable", "")
		default:
			writeError(w, http.StatusForbidden, "forbidden", "")
		}
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "")
			return
		}
		writeError(w, http.StatusBadRequest, "body_read_failed", "")
		return
	}

	batch, err := telemetry.DecodeBatch(bytes.NewReader(rawBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "")
		return
	}
	if validationErr := batch.Validate(); validationErr != nil {
		writeError(w, http.StatusUnprocessableEntity, validationErr.Code, validationErr.Field)
		return
	}

	result, err := a.ingestor.Ingest(r.Context(), principal, batch, rawBody)
	if err != nil {
		switch {
		case errors.Is(err, ingest.ErrIdentityMismatch):
			writeError(w, http.StatusForbidden, "identity_mismatch", "")
		case errors.Is(err, ingest.ErrBatchUnauthorized):
			writeError(w, http.StatusForbidden, "batch_unauthorized", "")
		case errors.Is(err, ingest.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency_conflict", "")
		case errors.Is(err, ingest.ErrBatchIDConflict):
			writeError(w, http.StatusConflict, "batch_conflict", "")
		case errors.Is(err, ingest.ErrObjectConflict):
			writeError(w, http.StatusConflict, "object_conflict", "")
		default:
			writeError(w, http.StatusServiceUnavailable, "ingest_unavailable", "")
		}
		return
	}

	status := http.StatusAccepted
	if result.Replay {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"receiptId":   result.Receipt.ReceiptID,
		"state":       result.Receipt.State,
		"sampleCount": result.Receipt.SampleCount,
		"replay":      result.Replay,
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, code, field string) {
	payload := map[string]any{"error": map[string]any{"code": code}}
	if field != "" {
		payload["error"].(map[string]any)["field"] = field
	}
	writeJSON(w, status, payload)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
