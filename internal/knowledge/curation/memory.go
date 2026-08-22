package curation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

// MemoryStore is a deterministic queue store for tests and ephemeral deployments.
type MemoryStore struct {
	mu     sync.Mutex
	byID   map[knowledge.CurationRecordID]knowledge.CurationRecord
	byTurn map[string]knowledge.CurationRecordID
	order  []knowledge.CurationRecordID
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:   make(map[knowledge.CurationRecordID]knowledge.CurationRecord),
		byTurn: make(map[string]knowledge.CurationRecordID),
	}
}

func (s *MemoryStore) Submit(ctx context.Context, record knowledge.CurationRecord) (knowledge.CurationRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.CurationRecord{}, false, err
	}
	if err := record.Validate(); err != nil {
		return knowledge.CurationRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := completedTurnKey(record.Source)
	if id, exists := s.byTurn[key]; exists {
		return cloneRecord(s.byID[id]), false, nil
	}
	if _, exists := s.byID[record.ID]; exists {
		return knowledge.CurationRecord{}, false, fmt.Errorf("%w: duplicate record ID", knowledge.ErrInvalidRecord)
	}
	s.byID[record.ID] = cloneRecord(record)
	s.byTurn[key] = record.ID
	s.order = append(s.order, record.ID)
	return cloneRecord(record), true, nil
}

func (s *MemoryStore) Claim(ctx context.Context, now time.Time) (knowledge.CurationRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.CurationRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		record := s.byID[id]
		if record.State != knowledge.CurationStateQueued {
			continue
		}
		record.State = knowledge.CurationStateProcessing
		record.Attempts++
		record.UpdatedAt = monotonicCurationTime(record.UpdatedAt, now)
		if err := record.Validate(); err != nil {
			return knowledge.CurationRecord{}, false, err
		}
		s.byID[id] = cloneRecord(record)
		return cloneRecord(record), true, nil
	}
	return knowledge.CurationRecord{}, false, nil
}

func (s *MemoryStore) Complete(ctx context.Context, id knowledge.CurationRecordID, result ExtractionResult, errorCode string, now time.Time) (knowledge.CurationRecord, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.CurationRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.byID[id]
	if !exists {
		return knowledge.CurationRecord{}, ErrNotFound
	}
	if record.State != knowledge.CurationStateProcessing {
		return knowledge.CurationRecord{}, fmt.Errorf("%w: record %s is %s", knowledge.ErrInvalidRecord, id, record.State)
	}
	record.UpdatedAt = monotonicCurationTime(record.UpdatedAt, now)
	record.CompletedAt = record.UpdatedAt
	if errorCode != "" {
		record.State = knowledge.CurationStateFailed
		record.LastErrorCode = errorCode
	} else if result.CandidateCount == 0 {
		record.State = knowledge.CurationStateNoCandidates
	} else {
		record.State = knowledge.CurationStateCandidatesReady
		record.CandidateCount = result.CandidateCount
	}
	if err := record.Validate(); err != nil {
		return knowledge.CurationRecord{}, err
	}
	s.byID[id] = cloneRecord(record)
	return cloneRecord(record), nil
}

func (s *MemoryStore) Get(ctx context.Context, id knowledge.CurationRecordID) (knowledge.CurationRecord, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.CurationRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.byID[id]
	if !exists {
		return knowledge.CurationRecord{}, ErrNotFound
	}
	return cloneRecord(record), nil
}

func completedTurnKey(source knowledge.CompletedTurnRef) string {
	return source.SessionID + "\x00" + source.ChatID + "\x00" + source.UserItemID + "\x00" + source.AssistantItemID
}

func monotonicCurationTime(previous, candidate time.Time) time.Time {
	candidate = candidate.UTC().Round(0)
	if !candidate.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return candidate
}

func cloneRecord(record knowledge.CurationRecord) knowledge.CurationRecord {
	record.Signals = cloneSignals(record.Signals)
	return record
}
