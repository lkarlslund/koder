package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	cockroachpebble "github.com/cockroachdb/pebble"

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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		usage, found, err := readEntryUsage(s.db, id)
		if err != nil {
			return nil, err
		}
		if found {
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
	entry, err := readRecord[knowledge.Entry](s.db, entryKey(string(event.EntryID)), "entry", string(event.EntryID))
	if err != nil {
		return knowledgeStore.EntryUsage{}, false, err
	}
	if event.UsedAt.Before(entry.CreatedAt) {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("entry usage time precedes entry creation")
	}
	if _, closer, err := s.db.Get(entryUsageEventKey(event.EntryID, event.EventID)); err == nil {
		_ = closer.Close()
		usage, found, readErr := readEntryUsage(s.db, event.EntryID)
		if readErr == nil && !found {
			readErr = fmt.Errorf("%w: entry usage event has no aggregate", knowledgeStore.ErrIncompatible)
		}
		return usage, false, readErr
	} else if !errors.Is(err, cockroachpebble.ErrNotFound) {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("read entry usage event: %w", err)
	}
	current, _, err := readEntryUsage(s.db, event.EntryID)
	if err != nil {
		return knowledgeStore.EntryUsage{}, false, err
	}
	usage := knowledgeStore.ApplyEntryUsageEvent(current, event)
	encoded, err := json.Marshal(usage)
	if err != nil {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("encode entry usage: %w", err)
	}
	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	if err := batch.Set(entryUsageKey(event.EntryID), encoded, nil); err != nil {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("write entry usage: %w", err)
	}
	if err := batch.Set(entryUsageEventKey(event.EntryID, event.EventID), []byte{1}, nil); err != nil {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("write entry usage event: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return knowledgeStore.EntryUsage{}, false, err
	}
	if err := batch.Commit(cockroachpebble.Sync); err != nil {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("commit entry usage: %w", err)
	}
	return usage, true, nil
}

func readEntryUsage(reader recordReader, id knowledge.EntryID) (knowledgeStore.EntryUsage, bool, error) {
	data, closer, err := reader.Get(entryUsageKey(id))
	if errors.Is(err, cockroachpebble.ErrNotFound) {
		return knowledgeStore.EntryUsage{}, false, nil
	}
	if err != nil {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("read entry usage %s: %w", id, err)
	}
	defer func() { _ = closer.Close() }()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var usage knowledgeStore.EntryUsage
	if err := decoder.Decode(&usage); err != nil {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("%w: decode entry usage: %v", knowledgeStore.ErrIncompatible, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("%w: entry usage contains trailing data", knowledgeStore.ErrIncompatible)
	}
	if usage.EntryID != id || usage.SuccessfulOutcomes > usage.ReuseCount || usage.FailedOutcomes > usage.ReuseCount-usage.SuccessfulOutcomes {
		return knowledgeStore.EntryUsage{}, false, fmt.Errorf("%w: invalid entry usage", knowledgeStore.ErrIncompatible)
	}
	return usage, true, nil
}
