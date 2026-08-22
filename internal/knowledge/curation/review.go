package curation

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
)

type CandidateRepository interface {
	Candidate(context.Context, CandidateID) (StoredCandidate, error)
	ListCandidates(context.Context, []CandidateStatus, int) ([]StoredCandidate, error)
	MarkApplied(context.Context, CandidateID, uint64, ApplyReceipt) (StoredCandidate, error)
	MarkRejected(context.Context, CandidateID, uint64, string) (StoredCandidate, error)
	MarkUndone(context.Context, CandidateID, uint64) (StoredCandidate, error)
}

type CurationRecordSource interface {
	Get(context.Context, knowledge.CurationRecordID) (knowledge.CurationRecord, error)
}

type CandidateApplier interface {
	ApplyCandidate(context.Context, knowledge.CurationRecord, CandidateDraft) (ApplyReceipt, error)
}

type CandidateUndoer interface {
	UndoCandidate(context.Context, StoredCandidate) error
}

type ReviewManager struct {
	candidates CandidateRepository
	records    CurationRecordSource
	applier    CandidateApplier
	undoer     CandidateUndoer
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
