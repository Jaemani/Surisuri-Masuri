package gcsadapter

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/googleapi"
)

func TestPreconditionClassificationUnwrapsAPIErrorHTTP412(t *testing.T) {
	original := &googleapi.Error{
		Code:    http.StatusPreconditionFailed,
		Message: "conditionNotMet",
	}
	wrapped, ok := apierror.FromError(original)
	if !ok {
		t.Fatal("apierror.FromError() did not recognize googleapi.Error")
	}

	var recovered *googleapi.Error
	if !errors.As(wrapped, &recovered) || recovered != original {
		t.Fatal("apierror.APIError did not preserve its googleapi.Error cause")
	}

	tests := []struct {
		name string
		err  error
	}{
		{name: "APIError", err: wrapped},
		{name: "outer operation wrapper", err: fmt.Errorf("create immutable object: %w", wrapped)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !isPreconditionFailure(test.err) {
				t.Fatalf("isPreconditionFailure(%T) = false, want true", test.err)
			}
		})
	}
}
