package store

import (
	"math"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

func TestApplyEntryUsageEventTracksMonotonicTimeAndSaturatingCounters(t *testing.T) {
	t.Parallel()
	entryID := memory.EntryID("01a01f76-1ff6-7c1d-967a-66ad5703dd33")
	newer := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	current := EntryUsage{
		EntryID: entryID, LastUsedAt: newer, ReuseCount: math.MaxUint64,
		SuccessfulOutcomes: math.MaxUint64, FailedOutcomes: 1,
	}
	got := ApplyEntryUsageEvent(current, EntryUsageEvent{
		EntryID: entryID, EventID: "event", UsedAt: newer.Add(-time.Hour), Outcome: EntryOutcomeSuccess,
	})
	if !got.LastUsedAt.Equal(newer) || got.ReuseCount != math.MaxUint64 || got.SuccessfulOutcomes != math.MaxUint64 || got.FailedOutcomes != 1 {
		t.Fatalf("saturated usage = %#v", got)
	}
}

func TestEntryUsageEventValidation(t *testing.T) {
	t.Parallel()
	valid := EntryUsageEvent{
		EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", EventID: "event-1",
		UsedAt: time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC), Outcome: EntryOutcomeNone,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	for _, mutate := range []func(*EntryUsageEvent){
		func(value *EntryUsageEvent) { value.EntryID = "" },
		func(value *EntryUsageEvent) { value.EventID = " bad " },
		func(value *EntryUsageEvent) { value.UsedAt = time.Time{} },
		func(value *EntryUsageEvent) { value.Outcome = "unknown" },
	} {
		value := valid
		mutate(&value)
		if err := value.Validate(); err == nil {
			t.Errorf("EntryUsageEvent(%#v) unexpectedly valid", value)
		}
	}
}
