package ingest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestExponentialFullJitterRetryUsesDeterministicRandomAndBounds(t *testing.T) {
	var delays []time.Duration
	retry := &exponentialFullJitterRetry{
		random: bytes.NewReader(make([]byte, 8)),
		base:   100 * time.Millisecond,
		max:    time.Second,
		sleep: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	}
	if err := retry.Wait(context.Background(), 1); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if len(delays) != 1 || delays[0] != 0 {
		t.Fatalf("delays = %#v", delays)
	}
}

func TestExponentialFullJitterRetryFailsClosedOnInvalidSeam(t *testing.T) {
	retry := &exponentialFullJitterRetry{
		random: bytes.NewReader(nil),
		base:   100 * time.Millisecond,
		max:    time.Second,
		sleep:  sleepForwardRecoveryRetry,
	}
	if err := retry.Wait(context.Background(), 1); !errors.Is(err, ErrForwardRecoveryRetryUnavailable) {
		t.Fatalf("Wait() error = %v", err)
	}
}
