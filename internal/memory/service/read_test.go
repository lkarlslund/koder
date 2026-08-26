package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestGetAuthorizesOwningAndLinkedChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
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
	link, err := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(second.Chunk.ID)},
		Kind:   memory.LinkKindRelatedTo,
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		object memory.ObjectRef
		kind   memoryStoreAPI.RecordKind
	}{
		{memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(first.Chunk.ID)}, memoryStoreAPI.RecordKindChunk},
		{memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entry.Entry.ID)}, memoryStoreAPI.RecordKindEntry},
		{memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(link.Link.ID)}, memoryStoreAPI.RecordKindLink},
	} {
		record, err := service.Get(ctx, test.object)
		if err != nil || record.Kind != test.kind {
			t.Fatalf("Get(%v) = %#v, %v", test.object, record, err)
		}
	}

	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ memory.Actor, action ChunkPolicyAction, chunk memory.Chunk) error {
		if action == ChunkPolicyRead && chunk.ID == second.Chunk.ID {
			return errors.New("denied")
		}
		return nil
	})
	if _, err := service.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(link.Link.ID)}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("Get(denied link) error = %v, want ErrChunkPolicyDenied", err)
	}
}

func TestHistoryRequiresCurrentObjectReadAuthorization(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	object := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(created.Chunk.ID)}
	page, err := service.History(ctx, memoryStoreAPI.RevisionListRequest{Object: object})
	if err != nil || len(page.Revisions) != 1 {
		t.Fatalf("History() = %#v, %v", page, err)
	}
	service.chunkPolicy = denyChunkAction(ChunkPolicyRead)
	if _, err := service.History(ctx, memoryStoreAPI.RevisionListRequest{Object: object}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("History(denied) error = %v", err)
	}
}
