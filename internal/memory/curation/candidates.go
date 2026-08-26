package curation

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

type CandidateID string
type CandidateStatus string

const (
	CandidateStatusPendingAutomatic CandidateStatus = "pending_automatic"
	CandidateStatusPendingReview    CandidateStatus = "pending_review"
	CandidateStatusApplied          CandidateStatus = "applied"
	CandidateStatusRejected         CandidateStatus = "rejected"
	CandidateStatusUndone           CandidateStatus = "undone"
)

type ApplyReceipt struct {
	EntryID        memory.EntryID `json:"entry_id"`
	BeforeRevision uint64         `json:"before_revision"`
	AfterRevision  uint64         `json:"after_revision"`
	Created        bool           `json:"created"`
}

type StoredCandidate struct {
	ID             CandidateID             `json:"id"`
	RecordID       memory.CurationRecordID `json:"record_id"`
	Draft          CandidateDraft          `json:"draft"`
	Status         CandidateStatus         `json:"status"`
	Receipt        ApplyReceipt            `json:"receipt,omitzero"`
	DecisionReason string                  `json:"decision_reason,omitempty"`
	Version        uint64                  `json:"version"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

func (s *MemoryCandidateStore) Candidate(ctx context.Context, candidateID CandidateID) (StoredCandidate, error) {
	if err := ctx.Err(); err != nil {
		return StoredCandidate{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, exists := s.byID[candidateID]
	if !exists {
		return StoredCandidate{}, ErrNotFound
	}
	return cloneStoredCandidate(candidate), nil
}

func (s *MemoryCandidateStore) ListCandidates(ctx context.Context, statuses []CandidateStatus, limit int) ([]StoredCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		return nil, fmt.Errorf("%w: candidate list limit exceeds 200", memory.ErrInvalidRecord)
	}
	allowed := make(map[CandidateStatus]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]StoredCandidate, 0, min(limit, len(s.byID)))
	for _, candidate := range s.byID {
		if len(allowed) != 0 {
			if _, ok := allowed[candidate.Status]; !ok {
				continue
			}
		}
		result = append(result, cloneStoredCandidate(candidate))
	}
	slices.SortFunc(result, func(left, right StoredCandidate) int {
		if order := left.CreatedAt.Compare(right.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(string(left.ID), string(right.ID))
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *MemoryCandidateStore) MarkApplied(ctx context.Context, candidateID CandidateID, expectedVersion uint64, receipt ApplyReceipt) (StoredCandidate, error) {
	if !isCanonicalUUIDv7(string(receipt.EntryID)) || receipt.AfterRevision == 0 ||
		(receipt.Created && receipt.BeforeRevision != 0) || (!receipt.Created && (receipt.BeforeRevision == 0 || receipt.AfterRevision != receipt.BeforeRevision+1)) {
		return StoredCandidate{}, fmt.Errorf("%w: candidate apply receipt is invalid", memory.ErrInvalidRecord)
	}
	return s.transition(ctx, candidateID, expectedVersion, CandidateStatusApplied, receipt, "")
}

func (s *MemoryCandidateStore) MarkRejected(ctx context.Context, candidateID CandidateID, expectedVersion uint64, reason string) (StoredCandidate, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1000 {
		return StoredCandidate{}, fmt.Errorf("%w: rejection reason must contain 1 to 1000 bytes", memory.ErrInvalidRecord)
	}
	return s.transition(ctx, candidateID, expectedVersion, CandidateStatusRejected, ApplyReceipt{}, reason)
}

func (s *MemoryCandidateStore) MarkUndone(ctx context.Context, candidateID CandidateID, expectedVersion uint64) (StoredCandidate, error) {
	return s.transition(ctx, candidateID, expectedVersion, CandidateStatusUndone, ApplyReceipt{}, "")
}

func (s *MemoryCandidateStore) transition(ctx context.Context, candidateID CandidateID, expectedVersion uint64, status CandidateStatus, receipt ApplyReceipt, reason string) (StoredCandidate, error) {
	if err := ctx.Err(); err != nil {
		return StoredCandidate{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, exists := s.byID[candidateID]
	if !exists {
		return StoredCandidate{}, ErrNotFound
	}
	if candidate.Version != expectedVersion {
		return StoredCandidate{}, ErrCandidateConflict
	}
	switch status {
	case CandidateStatusApplied, CandidateStatusRejected:
		if candidate.Status != CandidateStatusPendingAutomatic && candidate.Status != CandidateStatusPendingReview {
			return StoredCandidate{}, ErrCandidateConflict
		}
	case CandidateStatusUndone:
		if candidate.Status != CandidateStatusApplied {
			return StoredCandidate{}, ErrCandidateConflict
		}
	default:
		return StoredCandidate{}, fmt.Errorf("%w: candidate transition status is invalid", memory.ErrInvalidRecord)
	}
	candidate.Status = status
	if status != CandidateStatusUndone {
		candidate.Receipt = receipt
	}
	candidate.DecisionReason = reason
	candidate.Version++
	now := s.now().UTC().Round(0)
	if !now.After(candidate.UpdatedAt) {
		now = candidate.UpdatedAt.Add(time.Nanosecond)
	}
	candidate.UpdatedAt = now
	s.byID[candidateID] = candidate
	return cloneStoredCandidate(candidate), nil
}

func cloneStoredCandidate(candidate StoredCandidate) StoredCandidate {
	candidate.Draft = cloneCandidateDraft(candidate.Draft)
	return candidate
}
