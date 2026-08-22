package service

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestRestoreEntryRevisionCreatesAuditedRestoration(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	entry := testEntryCandidate()
	entry.Title = "Original"
	created, err := service.CreateEntry(context.Background(), CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	content := EntryContentFrom(created.Entry)
	content.Title = "Curated change"
	updated, err := service.UpdateEntry(context.Background(), UpdateEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1, Content: content, Reason: "curated",
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := service.RestoreEntryRevision(context.Background(), RestoreEntryRevisionRequest{
		EntryID: created.Entry.ID, ExpectedRevision: updated.Entry.Revision.Number, SourceRevision: 1,
		Reason: "undo curation candidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Entry.Title != "Original" || restored.Entry.Revision.Number != 3 || restored.Entry.Revision.Reason != "undo curation candidate" {
		t.Fatalf("RestoreEntryRevision() = %#v", restored)
	}
	history, err := service.History(context.Background(), knowledgeStore.RevisionListRequest{
		Object: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(created.Entry.ID)}, Limit: 10,
	})
	if err != nil || len(history.Revisions) != 3 {
		t.Fatalf("History() = %#v, %v", history, err)
	}
}
