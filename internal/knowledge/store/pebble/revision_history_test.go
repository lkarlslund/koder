package pebble

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestListRevisionsReadsDurableHistoryAndExcludesTouches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	first, second := txChunk(1), txChunk(2)
	first.Revision.Reason = "created"
	second.Revision.Reason = "renamed"
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, first, 0) }); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, second, 1) }); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		return tx.TouchChunk(ctx, txChunkID, second.UpdatedAt.Add(time.Hour))
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}
	request := knowledgeStore.RevisionListRequest{
		Object: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(txChunkID)},
	}
	page, err := s.ListRevisions(ctx, request)
	if err != nil {
		t.Fatalf("ListRevisions(): %v", err)
	}
	if len(page.Revisions) != 2 || page.Revisions[0].Chunk.Title != second.Title || page.Revisions[0].Chunk.Revision.Reason != "renamed" {
		t.Fatalf("history = %#v", page.Revisions)
	}
	if !page.Revisions[0].Chunk.LastUsedAt.IsZero() {
		t.Fatalf("historical LastUsedAt = %v, want zero", page.Revisions[0].Chunk.LastUsedAt)
	}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.DeleteChunk(ctx, txChunkID, 2) }); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.ListRevisions(ctx, request); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("history after permanent delete error = %v, want ErrNotFound", err)
	}
}
