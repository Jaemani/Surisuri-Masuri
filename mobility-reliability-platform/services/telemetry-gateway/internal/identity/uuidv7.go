// Package identity owns server-generated identifiers that must not be chosen
// by mobile clients.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"
)

// UUIDv7Generator creates RFC 9562 UUIDv7 identifiers. If the wall clock
// moves backwards during a process lifetime, the last observed millisecond is
// retained so newly generated identifiers do not regress in timestamp order.
// Random bits still provide uniqueness within the same millisecond.
type UUIDv7Generator struct {
	mu            sync.Mutex
	now           func() time.Time
	random        io.Reader
	lastUnixMilli int64
}

func NewUUIDv7Generator(now func() time.Time, random io.Reader) *UUIDv7Generator {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Reader
	}
	return &UUIDv7Generator{now: now, random: random}
}

func (g *UUIDv7Generator) NewID() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	unixMilli := g.now().UTC().UnixMilli()
	if unixMilli < 0 || unixMilli > 0xFFFFFFFFFFFF {
		return "", errors.New("uuidv7 timestamp is outside 48-bit range")
	}
	if unixMilli < g.lastUnixMilli {
		unixMilli = g.lastUnixMilli
	} else {
		g.lastUnixMilli = unixMilli
	}

	var value [16]byte
	if _, err := io.ReadFull(g.random, value[:]); err != nil {
		return "", err
	}
	value[0] = byte(unixMilli >> 40)
	value[1] = byte(unixMilli >> 32)
	value[2] = byte(unixMilli >> 24)
	value[3] = byte(unixMilli >> 16)
	value[4] = byte(unixMilli >> 8)
	value[5] = byte(unixMilli)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80

	var encoded [36]byte
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded[:]), nil
}
