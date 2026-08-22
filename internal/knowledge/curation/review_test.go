package curation

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

type recordSourceFunc func(context.Context, knowledge.CurationRecordID) (knowledge.CurationRecord, error)

func (fn recordSourceFunc) Get(ctx context.Context, id knowledge.CurationRecordID) (knowledge.CurationRecord, error) {
	return fn(ctx, id)
}

type candidateApplierFunc func(context.Context, knowledge.CurationRecord, CandidateDraft) (ApplyReceipt, error)

func (fn candidateApplierFunc) ApplyCandidate(ctx context.Context, record knowledge.CurationRecord, draft CandidateDraft) (ApplyReceipt, error) {
	return fn(ctx, record, draft)
}

type candidateUndoerFunc func(context.Context, StoredCandidate) error

func (fn candidateUndoerFunc) UndoCandidate(ctx context.Context, candidate StoredCandidate) error {
	return fn(ctx, candidate)
}

func TestReviewManagerAcceptRejectAndUndoLifecycle(t *testing.T) {
	t.Parallel()
	now := queueTestTime
	ids := []string{"00000000-0000-7000-8000-000000000101", "00000000-0000-7000-8000-000000000102"}
	store := NewMemoryCandidateStoreWithSources(func() string {
		value := ids[0]
		ids = ids[1:]
		return value
	}, func() time.Time { return now })
	recordID := knowledge.CurationRecordID("00000000-0000-7000-8000-000000000020")
	draft := dedupTestDraft()
	draft.Route = CandidateRoutePendingReview
	if _, err := store.StoreCandidates(context.Background(), recordID, []CandidateDraft{draft}); err != nil {
		t.Fatal(err)
	}
	record := processingRecord()
	record.State = knowledge.CurationStateCandidatesReady
	record.CandidateCount = 1
	record.CompletedAt = record.UpdatedAt
	applied := 0
	undone := 0
	manager, err := NewReviewManager(store, recordSourceFunc(func(_ context.Context, id knowledge.CurationRecordID) (knowledge.CurationRecord, error) {
		if id != recordID {
			t.Fatalf("record ID = %s", id)
		}
		return record, nil
	}), candidateApplierFunc(func(_ context.Context, _ knowledge.CurationRecord, _ CandidateDraft) (ApplyReceipt, error) {
		applied++
		return ApplyReceipt{EntryID: "00000000-0000-7000-8000-000000000111", AfterRevision: 1, Created: true}, nil
	}), candidateUndoerFunc(func(_ context.Context, candidate StoredCandidate) error {
		undone++
		if candidate.Status != CandidateStatusApplied {
			t.Fatalf("undo candidate = %#v", candidate)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := manager.List(context.Background(), []CandidateStatus{CandidateStatusPendingReview}, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("List() = %#v, %v", pending, err)
	}
	accepted, err := manager.Accept(context.Background(), pending[0].ID, pending[0].Version)
	if err != nil || accepted.Status != CandidateStatusApplied || accepted.Version != 2 || applied != 1 {
		t.Fatalf("Accept() = %#v, %v applied=%d", accepted, err, applied)
	}
	undoneCandidate, err := manager.Undo(context.Background(), accepted.ID, accepted.Version)
	if err != nil || undoneCandidate.Status != CandidateStatusUndone || undoneCandidate.Receipt.EntryID == "" || undone != 1 {
		t.Fatalf("Undo() = %#v, %v undone=%d", undoneCandidate, err, undone)
	}

	now = now.Add(time.Minute)
	draft.Entry.Title = "Reject me"
	if _, err := store.StoreCandidates(context.Background(), "00000000-0000-7000-8000-000000000120", []CandidateDraft{draft}); err != nil {
		t.Fatal(err)
	}
	pending, err = manager.List(context.Background(), []CandidateStatus{CandidateStatusPendingReview}, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("List(second) = %#v, %v", pending, err)
	}
	rejected, err := manager.Reject(context.Background(), pending[0].ID, pending[0].Version, "Not durable")
	if err != nil || rejected.Status != CandidateStatusRejected || rejected.DecisionReason != "Not durable" {
		t.Fatalf("Reject() = %#v, %v", rejected, err)
	}
}
