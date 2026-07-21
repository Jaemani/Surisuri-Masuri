package identity

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func TestUUIDv7ShapeVersionVariantAndTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 21, 6, 0, 0, 123_000_000, time.UTC)
	generator := NewUUIDv7Generator(
		func() time.Time { return now },
		bytes.NewReader(bytes.Repeat([]byte{0x2a}, 16)),
	)

	id, err := generator.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	decoded := decodeUUID(t, id)
	if decoded[6]>>4 != 7 {
		t.Fatalf("version = %d", decoded[6]>>4)
	}
	if decoded[8]>>6 != 2 {
		t.Fatalf("variant bits = %02b", decoded[8]>>6)
	}
	if got := timestampMillis(decoded); got != now.UnixMilli() {
		t.Fatalf("timestamp = %d, want %d", got, now.UnixMilli())
	}
}

func TestUUIDv7PinsTimestampAcrossClockRollback(t *testing.T) {
	first := time.Date(2026, 7, 21, 6, 0, 1, 0, time.UTC)
	calls := 0
	generator := NewUUIDv7Generator(
		func() time.Time {
			calls++
			if calls == 1 {
				return first
			}
			return first.Add(-time.Hour)
		},
		bytes.NewReader(bytes.Repeat([]byte{0x11}, 32)),
	)

	firstID, err := generator.NewID()
	if err != nil {
		t.Fatalf("first NewID() error = %v", err)
	}
	secondID, err := generator.NewID()
	if err != nil {
		t.Fatalf("second NewID() error = %v", err)
	}
	if timestampMillis(decodeUUID(t, secondID)) != timestampMillis(decodeUUID(t, firstID)) {
		t.Fatal("UUIDv7 timestamp regressed after wall-clock rollback")
	}
}

func TestUUIDv7PropagatesRandomSourceFailure(t *testing.T) {
	generator := NewUUIDv7Generator(
		func() time.Time { return time.UnixMilli(1) },
		errorReader{},
	)
	if _, err := generator.NewID(); err == nil {
		t.Fatal("NewID() error = nil")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic random failure")
}

func decodeUUID(t *testing.T, id string) [16]byte {
	t.Helper()
	compact := []byte(id)
	compact = bytes.ReplaceAll(compact, []byte{'-'}, nil)
	decoded, err := hex.DecodeString(string(compact))
	if err != nil || len(decoded) != 16 {
		t.Fatalf("decode UUID %q: %v", id, err)
	}
	var value [16]byte
	copy(value[:], decoded)
	return value
}

func timestampMillis(value [16]byte) int64 {
	return int64(value[0])<<40 |
		int64(value[1])<<32 |
		int64(value[2])<<24 |
		int64(value[3])<<16 |
		int64(value[4])<<8 |
		int64(value[5])
}
