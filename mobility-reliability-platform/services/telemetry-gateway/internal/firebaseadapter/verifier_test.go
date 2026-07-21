package firebaseadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/httpapi"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/telemetry"
)

func TestTokenPrincipalVerifierReturnsSeparatedPrincipal(t *testing.T) {
	verifier := mustTokenVerifier(
		t,
		idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
			return VerifiedIDToken{UID: "firebase-uid-123"}, nil
		}),
		appCheckVerifierFunc(func(string) (VerifiedAppCheckToken, error) {
			return VerifiedAppCheckToken{AppID: "1:123:android:allowed"}, nil
		}),
		[]string{"1:123:android:allowed"},
	)
	request := authenticatedRequest()

	principal, err := verifier.Verify(request)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.FirebaseUID != "firebase-uid-123" || principal.AppID != "1:123:android:allowed" {
		t.Fatalf("principal = %#v", principal)
	}
}

func TestTokenPrincipalVerifierRejectsMalformedOrDuplicateHeaders(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "missing authorization", mutate: func(r *http.Request) { r.Header.Del("Authorization") }},
		{name: "wrong scheme", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Basic token") }},
		{name: "missing bearer token", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer") }},
		{name: "NUL bearer token", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer token\x00") }},
		{name: "DEL bearer token", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer token\x7f") }},
		{name: "duplicate authorization", mutate: func(r *http.Request) { r.Header.Add("Authorization", "Bearer another") }},
		{name: "combined authorization", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer token,another") }},
		{name: "missing app check", mutate: func(r *http.Request) { r.Header.Del(AppCheckHeader) }},
		{name: "spaced app check", mutate: func(r *http.Request) { r.Header.Set(AppCheckHeader, "token another") }},
		{name: "vertical-tab app check", mutate: func(r *http.Request) { r.Header.Set(AppCheckHeader, "token\v") }},
		{name: "non-ASCII app check", mutate: func(r *http.Request) { r.Header.Set(AppCheckHeader, "token한") }},
		{name: "leading-space app check", mutate: func(r *http.Request) { r.Header.Set(AppCheckHeader, " token") }},
		{name: "trailing-space app check", mutate: func(r *http.Request) { r.Header.Set(AppCheckHeader, "token ") }},
		{name: "duplicate app check", mutate: func(r *http.Request) { r.Header.Add(AppCheckHeader, "another") }},
	}

	verifier := mustTokenVerifier(t, successIDVerifier(), successAppVerifier(), []string{"allowed-app"})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedRequest()
			test.mutate(request)
			if _, err := verifier.Verify(request); !errors.Is(err, httpapi.ErrUnauthenticated) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestTokenPrincipalVerifierMapsProviderFailureToUnavailable(t *testing.T) {
	tests := []struct {
		name string
		id   IDTokenVerifier
		app  AppCheckTokenVerifier
	}{
		{
			name: "ID token provider",
			id: idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
				return VerifiedIDToken{}, ErrProviderUnavailable
			}),
			app: successAppVerifier(),
		},
		{
			name: "App Check provider",
			id:   successIDVerifier(),
			app: appCheckVerifierFunc(func(string) (VerifiedAppCheckToken, error) {
				return VerifiedAppCheckToken{}, ErrProviderUnavailable
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := mustTokenVerifier(t, test.id, test.app, []string{"allowed-app"})
			if _, err := verifier.Verify(authenticatedRequest()); !errors.Is(err, httpapi.ErrVerifierUnavailable) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestTokenPrincipalVerifierRejectsInvalidAndIncompleteTokens(t *testing.T) {
	tests := []struct {
		name string
		id   IDTokenVerifier
		app  AppCheckTokenVerifier
	}{
		{
			name: "invalid ID token",
			id: idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
				return VerifiedIDToken{}, ErrTokenInvalid
			}),
			app: successAppVerifier(),
		},
		{
			name: "empty UID",
			id: idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
				return VerifiedIDToken{}, nil
			}),
			app: successAppVerifier(),
		},
		{
			name: "invalid App Check token",
			id:   successIDVerifier(),
			app: appCheckVerifierFunc(func(string) (VerifiedAppCheckToken, error) {
				return VerifiedAppCheckToken{}, ErrTokenInvalid
			}),
		},
		{
			name: "empty App ID",
			id:   successIDVerifier(),
			app: appCheckVerifierFunc(func(string) (VerifiedAppCheckToken, error) {
				return VerifiedAppCheckToken{}, nil
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := mustTokenVerifier(t, test.id, test.app, []string{"allowed-app"})
			if _, err := verifier.Verify(authenticatedRequest()); !errors.Is(err, httpapi.ErrUnauthenticated) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestTokenPrincipalVerifierRejectsVerifiedButUnlistedApp(t *testing.T) {
	verifier := mustTokenVerifier(t, successIDVerifier(), successAppVerifier(), []string{"different-app"})
	if _, err := verifier.Verify(authenticatedRequest()); !errors.Is(err, ErrAppNotAllowed) {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestTokenPrincipalVerifierRequiresNonemptyAllowlist(t *testing.T) {
	for _, allowed := range [][]string{nil, {}, {" "}} {
		if _, err := NewTokenPrincipalVerifier(successIDVerifier(), successAppVerifier(), allowed); err == nil {
			t.Fatalf("NewTokenPrincipalVerifier(%q) error = nil", allowed)
		}
	}
}

func TestTokenPrincipalVerifierZeroValueAndNilRequestFailClosed(t *testing.T) {
	var nilVerifier *TokenPrincipalVerifier
	if _, err := nilVerifier.Verify(authenticatedRequest()); !errors.Is(err, httpapi.ErrVerifierUnavailable) {
		t.Fatalf("nil verifier error = %v", err)
	}
	if _, err := (&TokenPrincipalVerifier{}).Verify(authenticatedRequest()); !errors.Is(err, httpapi.ErrVerifierUnavailable) {
		t.Fatalf("zero verifier error = %v", err)
	}
	verifier := mustTokenVerifier(t, successIDVerifier(), successAppVerifier(), []string{"allowed-app"})
	if _, err := verifier.Verify(nil); !errors.Is(err, httpapi.ErrUnauthenticated) {
		t.Fatalf("nil request error = %v", err)
	}
}

func TestTokenPrincipalVerifierTrimsAllowlistButMatchesCaseExactly(t *testing.T) {
	verifier := mustTokenVerifier(t, successIDVerifier(), successAppVerifier(), []string{" allowed-app "})
	if _, err := verifier.Verify(authenticatedRequest()); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	caseMismatch := mustTokenVerifier(t, successIDVerifier(), successAppVerifier(), []string{"Allowed-App"})
	if _, err := caseMismatch.Verify(authenticatedRequest()); !errors.Is(err, ErrAppNotAllowed) {
		t.Fatalf("case-mismatched Verify() error = %v", err)
	}
}

func TestTokenPrincipalVerifierRejectsOversizedTokensBeforeSDKCalls(t *testing.T) {
	var idCalls, appCalls int
	verifier := mustTokenVerifier(
		t,
		idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
			idCalls++
			return VerifiedIDToken{UID: "uid"}, nil
		}),
		appCheckVerifierFunc(func(string) (VerifiedAppCheckToken, error) {
			appCalls++
			return VerifiedAppCheckToken{AppID: "allowed-app"}, nil
		}),
		[]string{"allowed-app"},
	)

	requests := []*http.Request{authenticatedRequest(), authenticatedRequest()}
	requests[0].Header.Set("Authorization", "Bearer "+strings.Repeat("a", MaxTokenBytes+1))
	requests[1].Header.Set(AppCheckHeader, strings.Repeat("a", MaxTokenBytes+1))
	for _, request := range requests {
		if _, err := verifier.Verify(request); !errors.Is(err, httpapi.ErrUnauthenticated) {
			t.Fatalf("Verify() error = %v", err)
		}
	}
	if idCalls != 0 || appCalls != 0 {
		t.Fatalf("SDK calls = id:%d app:%d", idCalls, appCalls)
	}
}

func TestTokenPrincipalVerifierDoesNotCallAppCheckAfterIDFailure(t *testing.T) {
	appCalls := 0
	verifier := mustTokenVerifier(
		t,
		idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
			return VerifiedIDToken{}, ErrTokenInvalid
		}),
		appCheckVerifierFunc(func(string) (VerifiedAppCheckToken, error) {
			appCalls++
			return VerifiedAppCheckToken{}, nil
		}),
		[]string{"allowed-app"},
	)
	if _, err := verifier.Verify(authenticatedRequest()); !errors.Is(err, httpapi.ErrUnauthenticated) {
		t.Fatalf("Verify() error = %v", err)
	}
	if appCalls != 0 {
		t.Fatalf("App Check calls = %d", appCalls)
	}
}

func TestTokenPrincipalVerifierPassesRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	verifier := mustTokenVerifier(
		t,
		idTokenVerifierFunc(func(got context.Context, _ string) (VerifiedIDToken, error) {
			if !errors.Is(got.Err(), context.Canceled) {
				t.Fatalf("context error = %v", got.Err())
			}
			return VerifiedIDToken{UID: "uid"}, nil
		}),
		successAppVerifier(),
		[]string{"allowed-app"},
	)
	request := authenticatedRequest().WithContext(ctx)
	if _, err := verifier.Verify(request); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestTokenPrincipalVerifierHTTPFailureContractIsSanitized(t *testing.T) {
	const sensitive = "token-like-sensitive-provider-detail"
	tests := []struct {
		name       string
		id         IDTokenVerifier
		app        AppCheckTokenVerifier
		allowed    []string
		wantStatus int
		wantCode   string
	}{
		{
			name: "invalid ID token",
			id: idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
				return VerifiedIDToken{}, errors.New(sensitive)
			}),
			app:        successAppVerifier(),
			allowed:    []string{"allowed-app"},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthenticated",
		},
		{
			name:       "unlisted app",
			id:         successIDVerifier(),
			app:        successAppVerifier(),
			allowed:    []string{"different-app"},
			wantStatus: http.StatusForbidden,
			wantCode:   "forbidden",
		},
		{
			name: "provider unavailable",
			id: idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
				return VerifiedIDToken{}, ErrProviderUnavailable
			}),
			app:        successAppVerifier(),
			allowed:    []string{"allowed-app"},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "verifier_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := mustTokenVerifier(t, test.id, test.app, test.allowed)
			api := httpapi.NewAPI(verifier, neverIngestor{}).Routes()
			request := authenticatedRequest()
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			body := response.Body.String()
			if response.Code != test.wantStatus || !strings.Contains(body, test.wantCode) {
				t.Fatalf("status = %d, body = %s", response.Code, body)
			}
			if strings.Contains(body, sensitive) || strings.Contains(body, "allowed-app") {
				t.Fatalf("response leaked verification detail: %s", body)
			}
		})
	}
}

type idTokenVerifierFunc func(context.Context, string) (VerifiedIDToken, error)

func (f idTokenVerifierFunc) VerifyIDToken(ctx context.Context, token string) (VerifiedIDToken, error) {
	return f(ctx, token)
}

type appCheckVerifierFunc func(string) (VerifiedAppCheckToken, error)

func (f appCheckVerifierFunc) VerifyToken(token string) (VerifiedAppCheckToken, error) {
	return f(token)
}

func successIDVerifier() IDTokenVerifier {
	return idTokenVerifierFunc(func(context.Context, string) (VerifiedIDToken, error) {
		return VerifiedIDToken{UID: "firebase-uid"}, nil
	})
}

func successAppVerifier() AppCheckTokenVerifier {
	return appCheckVerifierFunc(func(string) (VerifiedAppCheckToken, error) {
		return VerifiedAppCheckToken{AppID: "allowed-app"}, nil
	})
}

func authenticatedRequest() *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/telemetry/batches", nil)
	request.Header.Set("Authorization", "Bearer synthetic-id-token")
	request.Header.Set(AppCheckHeader, "synthetic-app-check-token")
	return request
}

func mustTokenVerifier(
	t *testing.T,
	idTokens IDTokenVerifier,
	appTokens AppCheckTokenVerifier,
	allowedApps []string,
) *TokenPrincipalVerifier {
	t.Helper()
	verifier, err := NewTokenPrincipalVerifier(idTokens, appTokens, allowedApps)
	if err != nil {
		t.Fatalf("NewTokenPrincipalVerifier() error = %v", err)
	}
	return verifier
}

type neverIngestor struct{}

func (neverIngestor) Ingest(
	context.Context,
	ingest.Principal,
	telemetry.Batch,
	[]byte,
) (ingest.Result, error) {
	panic("ingestor called after verifier failure")
}
