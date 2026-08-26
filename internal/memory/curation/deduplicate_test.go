package curation

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
)

type entrySourceFunc func(context.Context, memory.ChunkID) ([]memory.Entry, error)

func (fn entrySourceFunc) EntriesForDeduplication(ctx context.Context, id memory.ChunkID) ([]memory.Entry, error) {
	return fn(ctx, id)
}

func dedupTestDraft() CandidateDraft {
	return CandidateDraft{
		Action:  CandidateActionCreateEntry,
		ChunkID: "00000000-0000-7000-8000-000000000031",
		Entry: EntryDraft{
			Kind: memory.EntryKindFact, Title: "Use sfdisk", Summary: "Use sfdisk when fdisk is unavailable.",
			Scope: memory.Scope{Kind: memory.ScopeKindProject, Selector: "koder"}, Confidence: 0.9,
		},
		Reason: "Successful fallback", SourceItemIDs: []string{"00000000-0000-7000-8000-000000000023"},
	}
}

func entryForDedup(draft CandidateDraft, id memory.EntryID, state memory.EntryState) memory.Entry {
	return memory.Entry{
		ID: id, ChunkID: draft.ChunkID, Kind: draft.Entry.Kind, Title: draft.Entry.Title,
		Summary: draft.Entry.Summary, Body: draft.Entry.Body, Aliases: draft.Entry.Aliases,
		Tags: draft.Entry.Tags, Scope: draft.Entry.Scope, Applicability: draft.Entry.Applicability,
		Risk: draft.Entry.Risk, Confidence: draft.Entry.Confidence, ValidFrom: draft.Entry.ValidFrom,
		ValidUntil: draft.Entry.ValidUntil, ObservedAt: draft.Entry.ObservedAt,
		ReviewAfter: draft.Entry.ReviewAfter, PersonalOrigin: draft.Entry.PersonalOrigin, State: state,
	}
}

func TestDeduplicatingSinkSuppressesActiveSupersededAndBatchDuplicates(t *testing.T) {
	t.Parallel()
	base := dedupTestDraft()
	active := entryForDedup(base, "00000000-0000-7000-8000-000000000041", memory.EntryStateActive)
	supersededDraft := base
	supersededDraft.Entry.Title = "Old fdisk workaround"
	superseded := entryForDedup(supersededDraft, "00000000-0000-7000-8000-000000000042", memory.EntryStateSuperseded)
	store := NewMemoryCandidateStore()
	sink, err := NewDeduplicatingSink(entrySourceFunc(func(_ context.Context, chunkID memory.ChunkID) ([]memory.Entry, error) {
		if chunkID != base.ChunkID {
			t.Fatalf("chunk ID = %s", chunkID)
		}
		return []memory.Entry{active, superseded}, nil
	}), store)
	if err != nil {
		t.Fatal(err)
	}
	newDraft := base
	newDraft.Entry.Title = "Use lsblk first"
	newDraft.Entry.Summary = "Inspect devices before partitioning."
	count, err := sink.StoreCandidates(context.Background(), "00000000-0000-7000-8000-000000000020", []CandidateDraft{
		base, supersededDraft, newDraft, newDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored := store.Candidates(context.Background(), "00000000-0000-7000-8000-000000000020")
	if count != 1 || len(stored) != 1 || stored[0].Entry.Title != "Use lsblk first" {
		t.Fatalf("deduplicated count=%d drafts=%#v", count, stored)
	}
}

func TestDeduplicatingSinkRequiresExistingMutationTarget(t *testing.T) {
	t.Parallel()
	store := NewMemoryCandidateStore()
	sink, err := NewDeduplicatingSink(entrySourceFunc(func(context.Context, memory.ChunkID) ([]memory.Entry, error) {
		return nil, nil
	}), store)
	if err != nil {
		t.Fatal(err)
	}
	draft := dedupTestDraft()
	draft.Action = CandidateActionUpdateEntry
	draft.TargetEntryID = "00000000-0000-7000-8000-000000000049"
	if _, err := sink.StoreCandidates(context.Background(), "00000000-0000-7000-8000-000000000020", []CandidateDraft{draft}); !errors.Is(err, memory.ErrInvalidRecord) {
		t.Fatalf("StoreCandidates(missing target) error = %v", err)
	}
}

func TestDeduplicatingSinkPinsMutationToCurrentRevision(t *testing.T) {
	t.Parallel()
	draft := dedupTestDraft()
	target := entryForDedup(draft, "00000000-0000-7000-8000-000000000049", memory.EntryStateActive)
	target.Revision.Number = 7
	draft.Action = CandidateActionUpdateEntry
	draft.TargetEntryID = target.ID
	draft.Entry.Summary = "A corrected value"
	store := NewMemoryCandidateStore()
	sink, err := NewDeduplicatingSink(entrySourceFunc(func(context.Context, memory.ChunkID) ([]memory.Entry, error) {
		return []memory.Entry{target}, nil
	}), store)
	if err != nil {
		t.Fatal(err)
	}
	id := memory.CurationRecordID("00000000-0000-7000-8000-000000000020")
	if count, err := sink.StoreCandidates(context.Background(), id, []CandidateDraft{draft}); err != nil || count != 1 {
		t.Fatalf("StoreCandidates() = %d, %v", count, err)
	}
	stored := store.Candidates(context.Background(), id)
	if len(stored) != 1 || stored[0].TargetRevision != 7 {
		t.Fatalf("stored target revision = %#v", stored)
	}
}

func TestMemoryCandidateStoreIsAtomicIdempotentAndClones(t *testing.T) {
	t.Parallel()
	store := NewMemoryCandidateStore()
	id := memory.CurationRecordID("00000000-0000-7000-8000-000000000020")
	drafts := []CandidateDraft{dedupTestDraft()}
	if count, err := store.StoreCandidates(context.Background(), id, drafts); err != nil || count != 1 {
		t.Fatalf("StoreCandidates() = %d, %v", count, err)
	}
	drafts[0].Entry.Title = "mutated caller"
	stored := store.Candidates(context.Background(), id)
	if stored[0].Entry.Title != "Use sfdisk" {
		t.Fatalf("stored candidate was aliased: %#v", stored)
	}
	if count, err := store.StoreCandidates(context.Background(), id, stored); err != nil || count != 1 {
		t.Fatalf("StoreCandidates(idempotent) = %d, %v", count, err)
	}
	stored[0].Entry.Title = "different"
	if _, err := store.StoreCandidates(context.Background(), id, stored); !errors.Is(err, ErrCandidateConflict) {
		t.Fatalf("StoreCandidates(conflict) error = %v", err)
	}
}
