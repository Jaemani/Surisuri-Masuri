// Package firebaseadapter connects verified Firebase identity signals to the
// gateway's provider-neutral ingest principal.
package firebaseadapter

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/httpapi"
	"github.com/Jaemani/Surisuri-Masuri/mobility-reliability-platform/services/telemetry-gateway/internal/ingest"
)

const AppCheckHeader = "X-Firebase-AppCheck"

// Firebase tokens are normally a few KiB. A separate limit prevents direct
// verifier use from accepting the HTTP server's entire header budget as one
// token and bounds parser/JWT work before SDK verification.
const MaxTokenBytes = 16 * 1024

var (
	ErrTokenInvalid        = errors.New("firebase token is invalid")
	ErrProviderUnavailable = errors.New("firebase token provider is unavailable")
	ErrAppNotAllowed       = errors.New("verified Firebase app is not allowed")
)

type VerifiedIDToken struct {
	UID string
}

type VerifiedAppCheckToken struct {
	AppID string
}

type IDTokenVerifier interface {
	VerifyIDToken(context.Context, string) (VerifiedIDToken, error)
}

type AppCheckTokenVerifier interface {
	VerifyToken(string) (VerifiedAppCheckToken, error)
}

type TokenPrincipalVerifier struct {
	idTokens    IDTokenVerifier
	appTokens   AppCheckTokenVerifier
	allowedApps map[string]struct{}
}

func NewTokenPrincipalVerifier(
	idTokens IDTokenVerifier,
	appTokens AppCheckTokenVerifier,
	allowedAppIDs []string,
) (*TokenPrincipalVerifier, error) {
	if idTokens == nil || appTokens == nil {
		return nil, errors.New("Firebase token verifiers are required")
	}
	allowedApps := make(map[string]struct{}, len(allowedAppIDs))
	for _, appID := range allowedAppIDs {
		appID = strings.TrimSpace(appID)
		if appID == "" {
			return nil, errors.New("Firebase app allowlist contains an empty ID")
		}
		allowedApps[appID] = struct{}{}
	}
	if len(allowedApps) == 0 {
		return nil, errors.New("at least one Firebase app ID is required")
	}
	return &TokenPrincipalVerifier{
		idTokens:    idTokens,
		appTokens:   appTokens,
		allowedApps: allowedApps,
	}, nil
}

func (v *TokenPrincipalVerifier) Verify(request *http.Request) (ingest.Principal, error) {
	if v == nil || v.idTokens == nil || v.appTokens == nil {
		return ingest.Principal{}, httpapi.ErrVerifierUnavailable
	}
	if request == nil {
		return ingest.Principal{}, httpapi.ErrUnauthenticated
	}
	idToken, ok := bearerToken(request.Header.Values("Authorization"))
	if !ok {
		return ingest.Principal{}, httpapi.ErrUnauthenticated
	}
	appCheckToken, ok := opaqueToken(request.Header.Values(AppCheckHeader))
	if !ok {
		return ingest.Principal{}, httpapi.ErrUnauthenticated
	}

	verifiedID, err := v.idTokens.VerifyIDToken(request.Context(), idToken)
	if err != nil {
		return ingest.Principal{}, mapVerificationError(err)
	}
	verifiedApp, err := v.appTokens.VerifyToken(appCheckToken)
	if err != nil {
		return ingest.Principal{}, mapVerificationError(err)
	}
	if verifiedID.UID == "" || verifiedApp.AppID == "" {
		return ingest.Principal{}, httpapi.ErrUnauthenticated
	}
	if _, allowed := v.allowedApps[verifiedApp.AppID]; !allowed {
		return ingest.Principal{}, ErrAppNotAllowed
	}

	return ingest.Principal{
		FirebaseUID: verifiedID.UID,
		AppID:       verifiedApp.AppID,
	}, nil
}

func mapVerificationError(err error) error {
	if errors.Is(err, ErrProviderUnavailable) {
		return httpapi.ErrVerifierUnavailable
	}
	return httpapi.ErrUnauthenticated
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	if len(values[0]) > MaxTokenBytes+len("Bearer ") || strings.ContainsAny(values[0], "\t\r\n,") {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	if !validTokenBytes(parts[1]) {
		return "", false
	}
	return parts[1], true
}

func opaqueToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	raw := values[0]
	token := strings.TrimSpace(raw)
	if token != raw || !validTokenBytes(token) {
		return "", false
	}
	return token, true
}

func validTokenBytes(token string) bool {
	if token == "" || len(token) > MaxTokenBytes {
		return false
	}
	for index := 0; index < len(token); index++ {
		if token[index] <= 0x20 || token[index] >= 0x7f || token[index] == ',' {
			return false
		}
	}
	return true
}
