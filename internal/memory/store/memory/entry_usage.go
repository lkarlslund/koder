package memory

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var _ memoryStoreAPI.UsageStore = (*Store)(nil)

func (s *Store) EntryUsage(ctx context.Context, ids []memory.EntryID) (map[memory.EntryID]memoryStoreAPI.EntryUsage, error) {
	ids, err := memoryStoreAPI.NormalizeEntryUsageIDs(ids)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, memoryStoreAPI.ErrClosed
	}
	result := make(map[memory.EntryID]memoryStoreAPI.EntryUsage, len(ids))
	for _, id := range ids {
		if usage, ok := s.data.usage[id]; ok {
			result[id] = usage
		}
	}
	return result, nil
}

func (s *Store) RecordEntryUsage(ctx context.Context, event memoryStoreAPI.EntryUsageEvent) (memoryStoreAPI.EntryUsage, bool, error) {
	if err := event.Validate(); err != nil {
		return memoryStoreAPI.EntryUsage{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.EntryUsage{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return memoryStoreAPI.EntryUsage{}, false, memoryStoreAPI.ErrClosed
	}
	entry, exists := s.data.entries[event.EntryID]
	if !exists {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("%w: entry %s", memoryStoreAPI.ErrNotFound, event.EntryID)
	}
	if event.UsedAt.Before(entry.CreatedAt) {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("entry usage time precedes entry creation")
	}
	eventKey := string(event.EntryID) + "\x00" + event.EventID
	if _, duplicate := s.data.usageEvents[eventKey]; duplicate {
		return s.data.usage[event.EntryID], false, nil
	}
	usage := memoryStoreAPI.ApplyEntryUsageEvent(s.data.usage[event.EntryID], event)
	s.data.usage[event.EntryID] = usage
	s.data.usageEvents[eventKey] = struct{}{}
	return usage, true, nil
}
