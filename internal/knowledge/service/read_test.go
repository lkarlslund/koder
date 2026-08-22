package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestGetAuthorizesOwningAndLinkedChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: first.Chunk.ID, Entry: testEntryCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	link, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(second.Chunk.ID)},
		Kind:   knowledge.LinkKindRelatedTo,
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		object knowledge.ObjectRef
		kind   knowledgeStore.RecordKind
	}{
		{knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(first.Chunk.ID)}, knowledgeStore.RecordKindChunk},
		{knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)}, knowledgeStore.RecordKindEntry},
		{knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(link.Link.ID)}, knowledgeStore.RecordKindLink},
	} {
		record, err := service.Get(ctx, test.object)
		if err != nil || record.Kind != test.kind {
			t.Fatalf("Get(%v) = %#v, %v", test.object, record, err)
		}
	}

	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if action == ChunkPolicyRead && chunk.ID == second.Chunk.ID {
			return errors.New("denied")
		}
		return nil
	})
	if _, err := service.Get(ctx, knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(link.Link.ID)}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("Get(denied link) error = %v, want ErrChunkPolicyDenied", err)
	}
}

func TestHistoryRequiresCurrentObjectReadAuthorization(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	object := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(created.Chunk.ID)}
	page, err := service.History(ctx, knowledgeStore.RevisionListRequest{Object: object})
	if err != nil || len(page.Revisions) != 1 {
		t.Fatalf("History() = %#v, %v", page, err)
	}
	service.chunkPolicy = denyChunkAction(ChunkPolicyRead)
	if _, err := service.History(ctx, knowledgeStore.RevisionListRequest{Object: object}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("History(denied) error = %v", err)
	}
}
