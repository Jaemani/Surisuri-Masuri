package firebaseadapter

import (
	"context"
	"errors"
	"os"

	firebase "firebase.google.com/go/v4"
	firebaseappcheck "firebase.google.com/go/v4/appcheck"
	firebaseauth "firebase.google.com/go/v4/auth"
)

type firebaseAuthClient interface {
	VerifyIDToken(context.Context, string) (*firebaseauth.Token, error)
}

type firebaseAppCheckClient interface {
	VerifyToken(string) (*firebaseappcheck.DecodedAppCheckToken, error)
}

type sdkIDTokenVerifier struct {
	client firebaseAuthClient
}

func (v sdkIDTokenVerifier) VerifyIDToken(ctx context.Context, token string) (VerifiedIDToken, error) {
	if v.client == nil {
		return VerifiedIDToken{}, ErrProviderUnavailable
	}
	verified, err := v.client.VerifyIDToken(ctx, token)
	if err != nil {
		if firebaseauth.IsCertificateFetchFailed(err) {
			return VerifiedIDToken{}, ErrProviderUnavailable
		}
		return VerifiedIDToken{}, ErrTokenInvalid
	}
	if verified == nil || verified.UID == "" {
		return VerifiedIDToken{}, ErrTokenInvalid
	}
	return VerifiedIDToken{UID: verified.UID}, nil
}

type sdkAppCheckTokenVerifier struct {
	client firebaseAppCheckClient
}

func (v sdkAppCheckTokenVerifier) VerifyToken(token string) (VerifiedAppCheckToken, error) {
	if v.client == nil {
		return VerifiedAppCheckToken{}, ErrProviderUnavailable
	}
	verified, err := v.client.VerifyToken(token)
	if err != nil || verified == nil || verified.AppID == "" {
		return VerifiedAppCheckToken{}, ErrTokenInvalid
	}
	return VerifiedAppCheckToken{AppID: verified.AppID}, nil
}

// NewProductionTokenVerifiers uses Application Default Credentials and fails
// before SDK construction if any emulator endpoint is present. The supplied
// context must live for the process lifetime because the App Check client uses
// it for background JWKS refresh.
func NewProductionTokenVerifiers(
	ctx context.Context,
	projectID string,
) (IDTokenVerifier, AppCheckTokenVerifier, error) {
	return newProductionTokenVerifiers(ctx, projectID, os.Getenv)
}

func newProductionTokenVerifiers(
	ctx context.Context,
	projectID string,
	getenv func(string) string,
) (IDTokenVerifier, AppCheckTokenVerifier, error) {
	if err := ValidateProductionEnvironment(getenv); err != nil {
		return nil, nil, err
	}
	return newSDKTokenVerifiers(ctx, projectID)
}

func newSDKTokenVerifiers(
	ctx context.Context,
	projectID string,
) (IDTokenVerifier, AppCheckTokenVerifier, error) {
	if ctx == nil {
		return nil, nil, errors.New("process context is required")
	}
	if projectID == "" {
		return nil, nil, errors.New("Firebase project ID is required")
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID})
	if err != nil {
		return nil, nil, err
	}
	authClient, err := app.Auth(ctx)
	if err != nil {
		return nil, nil, err
	}
	appCheckClient, err := app.AppCheck(ctx)
	if err != nil {
		return nil, nil, err
	}
	return sdkIDTokenVerifier{client: authClient},
		sdkAppCheckTokenVerifier{client: appCheckClient},
		nil
}

var emulatorEnvironmentVariables = []string{
	"FIREBASE_AUTH_EMULATOR_HOST",
	"FIRESTORE_EMULATOR_HOST",
	"FIREBASE_STORAGE_EMULATOR_HOST",
	"STORAGE_EMULATOR_HOST",
	"FIREBASE_DATABASE_EMULATOR_HOST",
	"PUBSUB_EMULATOR_HOST",
}

// ValidateProductionEnvironment fails closed if an emulator endpoint leaks
// into a production Cloud Run revision.
func ValidateProductionEnvironment(getenv func(string) string) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	for _, name := range emulatorEnvironmentVariables {
		if getenv(name) != "" {
			return errors.New("Firebase emulator environment is forbidden in production")
		}
	}
	return nil
}
