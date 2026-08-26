package pebble

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestEntryUsagePersistsIdempotentlyAndIsDeletedWithEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutChunk(ctx, txChunk(1), 0); err != nil {
			return err
		}
		return tx.PutEntry(ctx, txEntry(), 0)
	}); err != nil {
		t.Fatalf("seed usage store: %v", err)
	}
	event := memoryStoreAPI.EntryUsageEvent{
		EntryID: txEntryID, EventID: "event-1", UsedAt: txTime.Add(time.Minute), Outcome: memoryStoreAPI.EntryOutcomeSuccess,
	}
	first, recorded, err := s.RecordEntryUsage(ctx, event)
	if err != nil || !recorded || first.ReuseCount != 1 || first.SuccessfulOutcomes != 1 {
		t.Fatalf("first usage = %#v, %v, %v", first, recorded, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	s, err = Open(stateDir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	duplicate, recorded, err := s.RecordEntryUsage(ctx, event)
	if err != nil || recorded || duplicate != first {
		t.Fatalf("durable duplicate = %#v, %v, %v", duplicate, recorded, err)
	}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteEntry(ctx, txEntryID, 1) }); err != nil {
		t.Fatalf("DeleteEntry() error = %v", err)
	}
	usage, err := s.EntryUsage(ctx, []memory.EntryID{txEntryID})
	if err != nil || len(usage) != 0 {
		t.Fatalf("usage after delete = %#v, %v", usage, err)
	}
	if _, _, err := s.RecordEntryUsage(ctx, event); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("usage for deleted entry error = %v, want ErrNotFound", err)
	}
}
