package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestEntryUsageIsIdempotentAndDeletedWithEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutChunk(ctx, chunk(1), 0); err != nil {
			return err
		}
		return tx.PutEntry(ctx, entry(), 0)
	}); err != nil {
		t.Fatalf("seed usage store: %v", err)
	}
	firstEvent := memoryStoreAPI.EntryUsageEvent{
		EntryID: entryID, EventID: "event-1", UsedAt: fixedTime.Add(time.Minute), Outcome: memoryStoreAPI.EntryOutcomeSuccess,
	}
	first, recorded, err := s.RecordEntryUsage(ctx, firstEvent)
	if err != nil || !recorded || first.ReuseCount != 1 || first.SuccessfulOutcomes != 1 {
		t.Fatalf("first usage = %#v, %v, %v", first, recorded, err)
	}
	duplicate := firstEvent
	duplicate.Outcome = memoryStoreAPI.EntryOutcomeFailure
	got, recorded, err := s.RecordEntryUsage(ctx, duplicate)
	if err != nil || recorded || got != first {
		t.Fatalf("duplicate usage = %#v, %v, %v", got, recorded, err)
	}
	second, recorded, err := s.RecordEntryUsage(ctx, memoryStoreAPI.EntryUsageEvent{
		EntryID: entryID, EventID: "event-2", UsedAt: fixedTime.Add(2 * time.Minute), Outcome: memoryStoreAPI.EntryOutcomeFailure,
	})
	if err != nil || !recorded || second.ReuseCount != 2 || second.SuccessfulOutcomes != 1 || second.FailedOutcomes != 1 {
		t.Fatalf("second usage = %#v, %v, %v", second, recorded, err)
	}
	usage, err := s.EntryUsage(ctx, []memory.EntryID{entryID})
	if err != nil || usage[entryID] != second {
		t.Fatalf("EntryUsage() = %#v, %v", usage, err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteEntry(ctx, entryID, 1) }); err != nil {
		t.Fatalf("DeleteEntry() error = %v", err)
	}
	usage, err = s.EntryUsage(ctx, []memory.EntryID{entryID})
	if err != nil || len(usage) != 0 {
		t.Fatalf("usage after delete = %#v, %v", usage, err)
	}
	if _, _, err := s.RecordEntryUsage(ctx, firstEvent); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("usage for deleted entry error = %v, want ErrNotFound", err)
	}
}
