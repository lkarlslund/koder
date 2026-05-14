package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNewTimelineIDReturnsUUIDv7(t *testing.T) {
	id := NewTimelineID(time.UnixMilli(0x019aa0000000).UTC())
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("expected uuid format, got %q", id)
	}
	if len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Fatalf("expected uuid group lengths, got %q", id)
	}
	if parts[2][0] != '7' {
		t.Fatalf("expected uuidv7 version nibble, got %q", id)
	}
	switch parts[3][0] {
	case '8', '9', 'a', 'b':
	default:
		t.Fatalf("expected RFC 4122 variant nibble, got %q", id)
	}
}
