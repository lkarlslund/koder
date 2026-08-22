package memory

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var _ knowledgeStore.UsageStore = (*Store)(nil)

func (s *Store) EntryUsage(ctx context.Context, ids []knowledge.EntryID) (map[knowledge.EntryID]knowledgeStore.EntryUsage, error) {
	ids, err := knowledgeStore.NormalizeEntryUsageIDs(ids)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, knowledgeStore.ErrClosed
	}
	result := make(map[knowledge.EntryID]knowledgeStore.EntryUsage, len(ids))
	for _, id := range ids {
		if usage, ok := s.data.usage[id]; ok {
			result[id] = usage
		}
	}
	return result, nil
}

func (s *Store) RecordEntryUsage(ctx context.Context, event knowledgeStore.EntryUsageEvent) (knowledgeStore.EntryUsage, bool, error) {
	if err := event.Validate(); err != nil {
		return knowledgeStore.EntryUsage{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return knowledgeStore.EntryUsage{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return knowledgeStore.EntryUsage{}, false, knowledgeStore.ErrClosed
	}
	entry, exists := s.data.entries[event.EntryID]
	if !exists {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("%w: entry %s", knowledgeStore.ErrNotFound, event.EntryID)
	}
	if event.UsedAt.Before(entry.CreatedAt) {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("entry usage time precedes entry creation")
	}
	eventKey := string(event.EntryID) + "\x00" + event.EventID
	if _, duplicate := s.data.usageEvents[eventKey]; duplicate {
		return s.data.usage[event.EntryID], false, nil
	}
	usage := knowledgeStore.ApplyEntryUsageEvent(s.data.usage[event.EntryID], event)
	s.data.usage[event.EntryID] = usage
	s.data.usageEvents[eventKey] = struct{}{}
	return usage, true, nil
}
