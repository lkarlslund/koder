package curation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

// MemoryStore is a deterministic queue store for tests and ephemeral deployments.
type MemoryStore struct {
	mu     sync.Mutex
	byID   map[memory.CurationRecordID]memory.CurationRecord
	byTurn map[string]memory.CurationRecordID
	order  []memory.CurationRecordID
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:   make(map[memory.CurationRecordID]memory.CurationRecord),
		byTurn: make(map[string]memory.CurationRecordID),
	}
}

func (s *MemoryStore) Submit(ctx context.Context, record memory.CurationRecord) (memory.CurationRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return memory.CurationRecord{}, false, err
	}
	if err := record.Validate(); err != nil {
		return memory.CurationRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := completedTurnKey(record.Source)
	if id, exists := s.byTurn[key]; exists {
		return cloneRecord(s.byID[id]), false, nil
	}
	if _, exists := s.byID[record.ID]; exists {
		return memory.CurationRecord{}, false, fmt.Errorf("%w: duplicate record ID", memory.ErrInvalidRecord)
	}
	s.byID[record.ID] = cloneRecord(record)
	s.byTurn[key] = record.ID
	s.order = append(s.order, record.ID)
	return cloneRecord(record), true, nil
}

func (s *MemoryStore) Claim(ctx context.Context, now time.Time) (memory.CurationRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return memory.CurationRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		record := s.byID[id]
		if record.State != memory.CurationStateQueued {
			continue
		}
		record.State = memory.CurationStateProcessing
		record.Attempts++
		record.UpdatedAt = monotonicCurationTime(record.UpdatedAt, now)
		if err := record.Validate(); err != nil {
			return memory.CurationRecord{}, false, err
		}
		s.byID[id] = cloneRecord(record)
		return cloneRecord(record), true, nil
	}
	return memory.CurationRecord{}, false, nil
}

func (s *MemoryStore) Complete(ctx context.Context, id memory.CurationRecordID, result ExtractionResult, errorCode string, now time.Time) (memory.CurationRecord, error) {
	if err := ctx.Err(); err != nil {
		return memory.CurationRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.byID[id]
	if !exists {
		return memory.CurationRecord{}, ErrNotFound
	}
	if record.State != memory.CurationStateProcessing {
		return memory.CurationRecord{}, fmt.Errorf("%w: record %s is %s", memory.ErrInvalidRecord, id, record.State)
	}
	record.UpdatedAt = monotonicCurationTime(record.UpdatedAt, now)
	record.CompletedAt = record.UpdatedAt
	if errorCode != "" {
		record.State = memory.CurationStateFailed
		record.LastErrorCode = errorCode
	} else if result.CandidateCount == 0 {
		record.State = memory.CurationStateNoCandidates
	} else {
		record.State = memory.CurationStateCandidatesReady
		record.CandidateCount = result.CandidateCount
	}
	if err := record.Validate(); err != nil {
		return memory.CurationRecord{}, err
	}
	s.byID[id] = cloneRecord(record)
	return cloneRecord(record), nil
}

func (s *MemoryStore) Get(ctx context.Context, id memory.CurationRecordID) (memory.CurationRecord, error) {
	if err := ctx.Err(); err != nil {
		return memory.CurationRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.byID[id]
	if !exists {
		return memory.CurationRecord{}, ErrNotFound
	}
	return cloneRecord(record), nil
}

func completedTurnKey(source memory.CompletedTurnRef) string {
	return source.SessionID + "\x00" + source.ChatID + "\x00" + source.UserItemID + "\x00" + source.AssistantItemID
}

func monotonicCurationTime(previous, candidate time.Time) time.Time {
	candidate = candidate.UTC().Round(0)
	if !candidate.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return candidate
}

func cloneRecord(record memory.CurationRecord) memory.CurationRecord {
	record.Signals = cloneSignals(record.Signals)
	return record
}
