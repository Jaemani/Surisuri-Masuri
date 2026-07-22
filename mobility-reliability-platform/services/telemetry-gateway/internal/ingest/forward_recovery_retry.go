package ingest

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"time"
)

const (
	forwardRecoveryPageRetryBase = 100 * time.Millisecond
	forwardRecoveryPageRetryMax  = 2 * time.Second
)

var ErrForwardRecoveryRetryUnavailable = errors.New("forward recovery retry policy is unavailable")

type forwardRecoveryPageRetry interface {
	Wait(context.Context, int) error
}

type exponentialFullJitterRetry struct {
	random io.Reader
	base   time.Duration
	max    time.Duration
	sleep  func(context.Context, time.Duration) error
}

func newForwardRecoveryPageRetry() forwardRecoveryPageRetry {
	return &exponentialFullJitterRetry{
		random: rand.Reader,
		base:   forwardRecoveryPageRetryBase,
		max:    forwardRecoveryPageRetryMax,
		sleep:  sleepForwardRecoveryRetry,
	}
}

func (retry *exponentialFullJitterRetry) Wait(ctx context.Context, failure int) error {
	if retry == nil || retry.random == nil || retry.sleep == nil || ctx == nil ||
		failure < 1 || failure > 8 || retry.base <= 0 || retry.max < retry.base {
		return ErrForwardRecoveryRetryUnavailable
	}
	upper := retry.base
	for step := 1; step < failure && upper < retry.max; step++ {
		if upper > retry.max/2 {
			upper = retry.max
			break
		}
		upper *= 2
	}
	if upper > retry.max {
		upper = retry.max
	}
	var randomBytes [8]byte
	if _, err := io.ReadFull(retry.random, randomBytes[:]); err != nil {
		return ErrForwardRecoveryRetryUnavailable
	}
	delay := time.Duration(binary.BigEndian.Uint64(randomBytes[:]) % uint64(upper+1))
	return retry.sleep(ctx, delay)
}

func sleepForwardRecoveryRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
