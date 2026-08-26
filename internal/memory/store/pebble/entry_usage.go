package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	cockroachpebble "github.com/cockroachdb/pebble"

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
	entry, err := readRecord[memory.Entry](s.db, entryKey(string(event.EntryID)), "entry", string(event.EntryID))
	if err != nil {
		return memoryStoreAPI.EntryUsage{}, false, err
	}
	if event.UsedAt.Before(entry.CreatedAt) {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("entry usage time precedes entry creation")
	}
	if _, closer, err := s.db.Get(entryUsageEventKey(event.EntryID, event.EventID)); err == nil {
		_ = closer.Close()
		usage, found, readErr := readEntryUsage(s.db, event.EntryID)
		if readErr == nil && !found {
			readErr = fmt.Errorf("%w: entry usage event has no aggregate", memoryStoreAPI.ErrIncompatible)
		}
		return usage, false, readErr
	} else if !errors.Is(err, cockroachpebble.ErrNotFound) {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("read entry usage event: %w", err)
	}
	current, _, err := readEntryUsage(s.db, event.EntryID)
	if err != nil {
		return memoryStoreAPI.EntryUsage{}, false, err
	}
	usage := memoryStoreAPI.ApplyEntryUsageEvent(current, event)
	encoded, err := json.Marshal(usage)
	if err != nil {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("encode entry usage: %w", err)
	}
	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	if err := batch.Set(entryUsageKey(event.EntryID), encoded, nil); err != nil {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("write entry usage: %w", err)
	}
	if err := batch.Set(entryUsageEventKey(event.EntryID, event.EventID), []byte{1}, nil); err != nil {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("write entry usage event: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.EntryUsage{}, false, err
	}
	if err := batch.Commit(cockroachpebble.Sync); err != nil {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("commit entry usage: %w", err)
	}
	return usage, true, nil
}

func readEntryUsage(reader recordReader, id memory.EntryID) (memoryStoreAPI.EntryUsage, bool, error) {
	data, closer, err := reader.Get(entryUsageKey(id))
	if errors.Is(err, cockroachpebble.ErrNotFound) {
		return memoryStoreAPI.EntryUsage{}, false, nil
	}
	if err != nil {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("read entry usage %s: %w", id, err)
	}
	defer func() { _ = closer.Close() }()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var usage memoryStoreAPI.EntryUsage
	if err := decoder.Decode(&usage); err != nil {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("%w: decode entry usage: %v", memoryStoreAPI.ErrIncompatible, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("%w: entry usage contains trailing data", memoryStoreAPI.ErrIncompatible)
	}
	if usage.EntryID != id || usage.SuccessfulOutcomes > usage.ReuseCount || usage.FailedOutcomes > usage.ReuseCount-usage.SuccessfulOutcomes {
		return memoryStoreAPI.EntryUsage{}, false, fmt.Errorf("%w: invalid entry usage", memoryStoreAPI.ErrIncompatible)
	}
	return usage, true, nil
}
