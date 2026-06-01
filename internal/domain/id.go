package domain

import (
	"time"

	"github.com/lkarlslund/koder/internal/id"
)

// ID is the canonical datastore identifier.
type ID = id.ID

// NewID returns a UUIDv7 datastore identifier.
func NewID() ID {
	return id.New()
}

// NewIDAt returns a UUIDv7 datastore identifier using now for the timestamp bits.
func NewIDAt(now time.Time) ID {
	return id.NewAt(now)
}

// NewTimelineID returns a UUIDv7 identifier for a timeline item.
func NewTimelineID(now time.Time) ID {
	return id.NewAt(now)
}
