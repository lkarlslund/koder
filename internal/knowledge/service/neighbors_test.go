package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestNeighborsReturnsBoundedResolvedOneHopResults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	firstCandidate := testEntryCandidate()
	firstCandidate.Title = "First"
	first, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: firstCandidate})
	secondCandidate := testEntryCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: secondCandidate})
	root := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(chunk.Chunk.ID)}
	outgoing, _ := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: root, Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(first.Entry.ID)},
		Kind: knowledge.LinkKindRelatedTo,
	}})
	incoming, _ := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(second.Entry.ID)}, Target: root,
		Kind: knowledge.LinkKindRequires,
	}})

	request := NeighborRequest{Endpoint: root, Direction: knowledgeStore.LinkDirectionBoth, Limit: 1}
	firstPage, err := service.Neighbors(ctx, request)
	if err != nil || len(firstPage.Neighbors) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("first Neighbors() = %#v, %v", firstPage, err)
	}
	request.Cursor = firstPage.NextCursor
	secondPage, err := service.Neighbors(ctx, request)
	if err != nil || len(secondPage.Neighbors) != 1 || secondPage.Neighbors[0].Link.ID == firstPage.Neighbors[0].Link.ID {
		t.Fatalf("second Neighbors() = %#v, %v", secondPage, err)
	}
	for _, neighbor := range append(firstPage.Neighbors, secondPage.Neighbors...) {
		if neighbor.Object.Kind != knowledgeStore.RecordKindEntry || neighbor.Object.Entry == nil {
			t.Fatalf("unresolved neighbor = %#v", neighbor)
		}
	}

	if _, err := service.Unlink(ctx, LinkLifecycleRequest{LinkID: outgoing.Link.ID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("Unlink(): %v", err)
	}
	active, err := service.Neighbors(ctx, NeighborRequest{Endpoint: root})
	if err != nil || len(active.Neighbors) != 1 || active.Neighbors[0].Link.ID != incoming.Link.ID ||
		active.Neighbors[0].Direction != knowledgeStore.LinkDirectionIncoming {
		t.Fatalf("active neighbors = %#v, %v", active, err)
	}
}

func TestNeighborsEnforcesTraversalPolicyOnRootAndResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	root := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(first.Chunk.ID)}
	_, _ = service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: root, Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(second.Chunk.ID)},
		Kind: knowledge.LinkKindRelatedTo,
	}})
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if action == ChunkPolicyTraverse && chunk.ID == second.Chunk.ID {
			return errors.New("denied")
		}
		return nil
	})
	if _, err := service.Neighbors(ctx, NeighborRequest{Endpoint: root}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("Neighbors(denied result) error = %v, want ErrChunkPolicyDenied", err)
	}
}
