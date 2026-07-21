package firebaseadapter

import (
	"context"
	"errors"
	"testing"

	firebaseappcheck "firebase.google.com/go/v4/appcheck"
	firebaseauth "firebase.google.com/go/v4/auth"
)

func TestSDKIDTokenVerifierMapsVerifiedUID(t *testing.T) {
	verifier := sdkIDTokenVerifier{client: firebaseAuthClientFunc(
		func(context.Context, string) (*firebaseauth.Token, error) {
			return &firebaseauth.Token{UID: "firebase-uid"}, nil
		},
	)}
	verified, err := verifier.VerifyIDToken(context.Background(), "synthetic")
	if err != nil || verified.UID != "firebase-uid" {
		t.Fatalf("VerifyIDToken() = %#v, %v", verified, err)
	}
}

func TestSDKIDTokenVerifierDoesNotExposeProviderError(t *testing.T) {
	providerError := errors.New("provider error with token-like-sensitive-value")
	verifier := sdkIDTokenVerifier{client: firebaseAuthClientFunc(
		func(context.Context, string) (*firebaseauth.Token, error) {
			return nil, providerError
		},
	)}
	if _, err := verifier.VerifyIDToken(context.Background(), "synthetic"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("VerifyIDToken() error = %v", err)
	}
}

func TestSDKAppCheckVerifierMapsVerifiedAppID(t *testing.T) {
	verifier := sdkAppCheckTokenVerifier{client: firebaseAppCheckClientFunc(
		func(string) (*firebaseappcheck.DecodedAppCheckToken, error) {
			return &firebaseappcheck.DecodedAppCheckToken{AppID: "app-id"}, nil
		},
	)}
	verified, err := verifier.VerifyToken("synthetic")
	if err != nil || verified.AppID != "app-id" {
		t.Fatalf("VerifyToken() = %#v, %v", verified, err)
	}
}

func TestSDKAppCheckVerifierDoesNotExposeProviderError(t *testing.T) {
	providerError := errors.New("provider error with token-like-sensitive-value")
	verifier := sdkAppCheckTokenVerifier{client: firebaseAppCheckClientFunc(
		func(string) (*firebaseappcheck.DecodedAppCheckToken, error) {
			return nil, providerError
		},
	)}
	if _, err := verifier.VerifyToken("synthetic"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("VerifyToken() error = %v", err)
	}
}

func TestSDKVerifiersFailClosedWithNilClients(t *testing.T) {
	if _, err := (sdkIDTokenVerifier{}).VerifyIDToken(context.Background(), "synthetic"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("ID VerifyIDToken() error = %v", err)
	}
	if _, err := (sdkAppCheckTokenVerifier{}).VerifyToken("synthetic"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("App Check VerifyToken() error = %v", err)
	}
}

func TestNewProductionTokenVerifiersRejectsEmulatorBeforeSDKConstruction(t *testing.T) {
	getenv := func(name string) string {
		if name == "FIREBASE_AUTH_EMULATOR_HOST" {
			return "127.0.0.1:9099"
		}
		return ""
	}
	if _, _, err := newProductionTokenVerifiers(
		context.Background(),
		"synthetic-project",
		getenv,
	); err == nil {
		t.Fatal("NewProductionTokenVerifiers() error = nil")
	}
}

func TestNewSDKTokenVerifiersRequiresProcessContext(t *testing.T) {
	if _, _, err := newSDKTokenVerifiers(nil, "synthetic-project"); err == nil {
		t.Fatal("newSDKTokenVerifiers() error = nil")
	}
}

func TestValidateProductionEnvironmentRejectsEveryEmulatorVariable(t *testing.T) {
	for _, blockedName := range emulatorEnvironmentVariables {
		t.Run(blockedName, func(t *testing.T) {
			getenv := func(name string) string {
				if name == blockedName {
					return "configured"
				}
				return ""
			}
			if err := ValidateProductionEnvironment(getenv); err == nil {
				t.Fatal("ValidateProductionEnvironment() error = nil")
			}
		})
	}
}

func TestValidateProductionEnvironmentAllowsCleanEnvironment(t *testing.T) {
	if err := ValidateProductionEnvironment(func(string) string { return "" }); err != nil {
		t.Fatalf("ValidateProductionEnvironment() error = %v", err)
	}
}

type firebaseAuthClientFunc func(context.Context, string) (*firebaseauth.Token, error)

func (f firebaseAuthClientFunc) VerifyIDToken(ctx context.Context, token string) (*firebaseauth.Token, error) {
	return f(ctx, token)
}

type firebaseAppCheckClientFunc func(string) (*firebaseappcheck.DecodedAppCheckToken, error)

func (f firebaseAppCheckClientFunc) VerifyToken(token string) (*firebaseappcheck.DecodedAppCheckToken, error) {
	return f(token)
}
