package curation

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
)

type CandidateRepository interface {
	Candidate(context.Context, CandidateID) (StoredCandidate, error)
	ListCandidates(context.Context, []CandidateStatus, int) ([]StoredCandidate, error)
	MarkApplied(context.Context, CandidateID, uint64, ApplyReceipt) (StoredCandidate, error)
	MarkRejected(context.Context, CandidateID, uint64, string) (StoredCandidate, error)
	MarkUndone(context.Context, CandidateID, uint64) (StoredCandidate, error)
}

type CurationRecordSource interface {
	Get(context.Context, memory.CurationRecordID) (memory.CurationRecord, error)
}

type CandidateApplier interface {
	ApplyCandidate(context.Context, memory.CurationRecord, CandidateDraft) (ApplyReceipt, error)
}

type CandidateUndoer interface {
	UndoCandidate(context.Context, StoredCandidate) error
}

type ReviewManager struct {
	candidates CandidateRepository
	records    CurationRecordSource
	applier    CandidateApplier
	automatic  CandidateApplier
	undoer     CandidateUndoer
}

func NewReviewManagerWithAutomatic(candidates CandidateRepository, records CurationRecordSource, reviewed CandidateApplier, automatic CandidateApplier, undoer CandidateUndoer) (*ReviewManager, error) {
	manager, err := NewReviewManager(candidates, records, reviewed, undoer)
	if err != nil {
		return nil, err
	}
	if automatic == nil {
		return nil, fmt.Errorf("%w: automatic candidate applier is required", ErrUnavailable)
	}
	manager.automatic = automatic
	return manager, nil
}

func NewReviewManager(candidates CandidateRepository, records CurationRecordSource, applier CandidateApplier, undoer CandidateUndoer) (*ReviewManager, error) {
	if candidates == nil || records == nil || applier == nil || undoer == nil {
		return nil, fmt.Errorf("%w: review manager requires candidates, records, applier, and undoer", ErrUnavailable)
	}
	return &ReviewManager{candidates: candidates, records: records, applier: applier, undoer: undoer}, nil
}

func (m *ReviewManager) List(ctx context.Context, statuses []CandidateStatus, limit int) ([]StoredCandidate, error) {
	return m.candidates.ListCandidates(ctx, statuses, limit)
}

func (m *ReviewManager) Accept(ctx context.Context, candidateID CandidateID, expectedVersion uint64) (StoredCandidate, error) {
	candidate, err := m.candidates.Candidate(ctx, candidateID)
	if err != nil {
		return StoredCandidate{}, err
	}
	if candidate.Version != expectedVersion {
		return StoredCandidate{}, ErrCandidateConflict
	}
	record, err := m.records.Get(ctx, candidate.RecordID)
	if err != nil {
		return StoredCandidate{}, fmt.Errorf("load curation record: %w", err)
	}
	receipt, err := m.applier.ApplyCandidate(ctx, record, candidate.Draft)
	if err != nil {
		return StoredCandidate{}, err
	}
	updated, err := m.candidates.MarkApplied(ctx, candidateID, expectedVersion, receipt)
	if err != nil {
		return StoredCandidate{}, fmt.Errorf("mark candidate applied after canonical commit: %w", err)
	}
	return updated, nil
}

func (m *ReviewManager) Reject(ctx context.Context, candidateID CandidateID, expectedVersion uint64, reason string) (StoredCandidate, error) {
	return m.candidates.MarkRejected(ctx, candidateID, expectedVersion, reason)
}

func (m *ReviewManager) Undo(ctx context.Context, candidateID CandidateID, expectedVersion uint64) (StoredCandidate, error) {
	candidate, err := m.candidates.Candidate(ctx, candidateID)
	if err != nil {
		return StoredCandidate{}, err
	}
	if candidate.Version != expectedVersion || candidate.Status != CandidateStatusApplied {
		return StoredCandidate{}, ErrCandidateConflict
	}
	if err := m.undoer.UndoCandidate(ctx, candidate); err != nil {
		return StoredCandidate{}, err
	}
	updated, err := m.candidates.MarkUndone(ctx, candidateID, expectedVersion)
	if err != nil {
		return StoredCandidate{}, fmt.Errorf("mark candidate undone after canonical rollback: %w", err)
	}
	return updated, nil
}

// ApplyAutomatic commits every currently pending automatic candidate in queue order.
// It is called only by the background coordinator, never by the human review API.
func (m *ReviewManager) ApplyAutomatic(ctx context.Context) error {
	if m == nil || m.automatic == nil {
		return ErrUnavailable
	}
	candidates, err := m.candidates.ListCandidates(ctx, []CandidateStatus{CandidateStatusPendingAutomatic}, 200)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		record, err := m.records.Get(ctx, candidate.RecordID)
		if err != nil {
			return fmt.Errorf("load automatic curation record: %w", err)
		}
		receipt, err := m.automatic.ApplyCandidate(ctx, record, candidate.Draft)
		if err != nil {
			return fmt.Errorf("apply automatic curation candidate: %w", err)
		}
		if _, err := m.candidates.MarkApplied(ctx, candidate.ID, candidate.Version, receipt); err != nil {
			return fmt.Errorf("mark automatic curation candidate applied: %w", err)
		}
	}
	return nil
}
