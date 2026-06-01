package id

import (
	"crypto/rand"
	"fmt"
	"time"
)

// ID is the canonical datastore identifier.
type ID = string

// New returns a UUIDv7 datastore identifier.
func New() ID {
	return NewAt(time.Now().UTC())
}

// NewAt returns a UUIDv7 datastore identifier using now for the timestamp bits.
func NewAt(now time.Time) ID {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ms := uint64(now.UTC().UnixMilli())
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		nano := uint64(now.UTC().UnixNano())
		for i := 6; i < len(b); i++ {
			b[i] = byte(nano >> ((i - 6) * 8))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<40|uint64(b[11])<<32|uint64(b[12])<<24|uint64(b[13])<<16|uint64(b[14])<<8|uint64(b[15]),
	)
}
