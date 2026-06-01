package chat

import (
	"time"

	"github.com/lkarlslund/koder/internal/id"
)

// NewTimelineID returns a UUIDv7 identifier for a timeline item.
func NewTimelineID(now time.Time) id.ID {
	return id.NewAt(now)
}
