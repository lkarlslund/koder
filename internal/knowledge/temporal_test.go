package knowledge

import (
	"errors"
	"testing"
	"time"
)

func TestEntryTemporalStatusAtBoundaries(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	entry := Entry{ValidFrom: start, ValidUntil: start.Add(2 * time.Hour), ReviewAfter: start.Add(time.Hour)}
	tests := []struct {
		name                               string
		at                                 time.Time
		valid, future, expired, due, stale bool
	}{
		{name: "before validity", at: start.Add(-time.Nanosecond), future: true},
		{name: "valid from inclusive", at: start, valid: true},
		{name: "review boundary inclusive", at: start.Add(time.Hour), valid: true, due: true, stale: true},
		{name: "valid until exclusive", at: start.Add(2 * time.Hour), expired: true, due: true, stale: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, err := EntryTemporalStatusAt(entry, test.at)
			if err != nil {
				t.Fatalf("EntryTemporalStatusAt(): %v", err)
			}
			if status.Valid != test.valid || status.NotYetValid != test.future || status.Expired != test.expired ||
				status.ReviewDue != test.due || status.Stale != test.stale {
				t.Fatalf("status = %#v", status)
			}
		})
	}
}

func TestTemporalStatusRequiresExplicitAsOfAndNormalizesZone(t *testing.T) {
	t.Parallel()
	if _, err := EntryTemporalStatusAt(Entry{}, time.Time{}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("zero as-of error = %v, want ErrInvalidRecord", err)
	}
	local := time.Date(2026, 8, 22, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	status, err := ChunkTemporalStatusAt(Chunk{ReviewAfter: local.UTC()}, local)
	if err != nil || status.AsOf.Location() != time.UTC || !status.Valid || !status.ReviewDue || !status.Stale {
		t.Fatalf("ChunkTemporalStatusAt() = %#v, %v", status, err)
	}
}
