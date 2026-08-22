package service

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestTraversalSurfacesContradictionsAndSupersessionChains(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	oldCandidate := testEntryCandidate()
	oldCandidate.Title = "Old claim"
	old, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: oldCandidate})
	replacementCandidate := testEntryCandidate()
	replacementCandidate.Title = "Corrected claim"
	replacement, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: replacementCandidate})
	if _, err := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: old.Entry.ID, ExpectedRevision: 1, ReplacementEntryID: replacement.Entry.ID,
	}); err != nil {
		t.Fatalf("SupersedeEntry(): %v", err)
	}
	if _, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(old.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(replacement.Entry.ID)},
		Kind:   knowledge.LinkKindContradicts,
	}}); err != nil {
		t.Fatalf("CreateLink(contradiction): %v", err)
	}
	result, err := service.Traverse(ctx, TraversalRequest{
		Root: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(old.Entry.ID)}, MaxDepth: 1,
	})
	if err != nil {
		t.Fatalf("Traverse(): %v", err)
	}
	if len(result.Contradictions) != 1 || len(result.SupersessionChains) != 1 {
		t.Fatalf("semantic traversal = %#v", result)
	}
	chain := result.SupersessionChains[0]
	if !chain.Complete || chain.Cycle || len(chain.Entries) != 2 || chain.Entries[1].ID != replacement.Entry.ID {
		t.Fatalf("supersession chain = %#v", chain)
	}
}

func TestSupersessionChainReportsTruncationAndCycles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	first, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: testEntryCandidate()})
	secondCandidate := testEntryCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: secondCandidate})
	updatedFirst, _ := service.SupersedeEntry(ctx, SupersedeEntryRequest{
		EntryID: first.Entry.ID, ExpectedRevision: 1, ReplacementEntryID: second.Entry.ID,
	})
	limited, err := service.SupersessionChain(ctx, SupersessionChainRequest{EntryID: first.Entry.ID, MaxEntries: 1})
	if err != nil || !limited.Truncated || limited.Complete || len(limited.Entries) != 1 {
		t.Fatalf("limited chain = %#v, %v", limited, err)
	}
	cycleEntry := second.Entry
	cycleEntry.State = knowledge.EntryStateSuperseded
	cycleEntry.SupersededByID = first.Entry.ID
	cycleEntry.UpdatedAt = updatedFirst.Entry.UpdatedAt.Add(time.Nanosecond)
	cycleEntry.Revision = knowledge.Revision{
		Number: 2, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8af",
		Actor: cycleEntry.Revision.Actor, Reason: "cycle fixture", CreatedAt: cycleEntry.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEntry(ctx, cycleEntry, 1) }); err != nil {
		t.Fatalf("seed cycle: %v", err)
	}
	cycle, err := service.SupersessionChain(ctx, SupersessionChainRequest{EntryID: first.Entry.ID})
	if err != nil || !cycle.Cycle || cycle.Complete || len(cycle.Entries) != 2 {
		t.Fatalf("cycle chain = %#v, %v", cycle, err)
	}
}
