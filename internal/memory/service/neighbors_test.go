package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestNeighborsReturnsBoundedResolvedOneHopResults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	firstCandidate := testEntryCandidate()
	firstCandidate.Title = "First"
	first, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: firstCandidate})
	secondCandidate := testEntryCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: secondCandidate})
	root := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunk.Chunk.ID)}
	outgoing, _ := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: root, Target: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(first.Entry.ID)},
		Kind: memory.LinkKindRelatedTo,
	}})
	incoming, _ := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(second.Entry.ID)}, Target: root,
		Kind: memory.LinkKindRequires,
	}})

	request := NeighborRequest{Endpoint: root, Direction: memoryStoreAPI.LinkDirectionBoth, Limit: 1}
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
		if neighbor.Object.Kind != memoryStoreAPI.RecordKindEntry || neighbor.Object.Entry == nil {
			t.Fatalf("unresolved neighbor = %#v", neighbor)
		}
	}

	if _, err := service.Unlink(ctx, LinkLifecycleRequest{LinkID: outgoing.Link.ID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("Unlink(): %v", err)
	}
	active, err := service.Neighbors(ctx, NeighborRequest{Endpoint: root})
	if err != nil || len(active.Neighbors) != 1 || active.Neighbors[0].Link.ID != incoming.Link.ID ||
		active.Neighbors[0].Direction != memoryStoreAPI.LinkDirectionIncoming {
		t.Fatalf("active neighbors = %#v, %v", active, err)
	}
}

func TestNeighborsEnforcesTraversalPolicyOnRootAndResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	root := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(first.Chunk.ID)}
	firstVisibleCandidate := testChunkCandidate()
	firstVisibleCandidate.Title = "First visible"
	firstVisible, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: firstVisibleCandidate})
	firstVisibleLink, _ := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: root, Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(firstVisible.Chunk.ID)},
		Kind: memory.LinkKindRelatedTo,
	}})
	_, _ = service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: root, Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(second.Chunk.ID)},
		Kind: memory.LinkKindRelatedTo,
	}})
	_, _ = service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: root, Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(second.Chunk.ID)},
		Kind: memory.LinkKindRequires,
	}})
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ memory.Actor, action ChunkPolicyAction, chunk memory.Chunk) error {
		if action == ChunkPolicyTraverse && chunk.ID == second.Chunk.ID {
			return errors.New("denied")
		}
		return nil
	})
	page, err := service.Neighbors(ctx, NeighborRequest{Endpoint: root, Limit: 1})
	if err != nil || len(page.Neighbors) != 1 || page.Neighbors[0].Link.ID != firstVisibleLink.Link.ID || page.NextCursor != "" {
		t.Fatalf("Neighbors(policy-filtered first) = %#v, %v", page, err)
	}
	service.chunkPolicy = denyChunkAction(ChunkPolicyTraverse)
	if _, err := service.Neighbors(ctx, NeighborRequest{Endpoint: root}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("Neighbors(denied root) error = %v, want ErrChunkPolicyDenied", err)
	}
}

func TestNeighborsSkipsDeniedLinksBeforeVisiblePageResults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	rootChunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	hiddenCandidate := testChunkCandidate()
	hiddenCandidate.Title = "Hidden"
	hidden, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: hiddenCandidate})
	visibleCandidate := testChunkCandidate()
	visibleCandidate.Title = "Visible"
	visible, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: visibleCandidate})
	root := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(rootChunk.Chunk.ID)}
	_, _ = service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: root, Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(hidden.Chunk.ID)}, Kind: memory.LinkKindRelatedTo,
	}})
	visibleLink, _ := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: root, Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(visible.Chunk.ID)}, Kind: memory.LinkKindRelatedTo,
	}})
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ memory.Actor, action ChunkPolicyAction, chunk memory.Chunk) error {
		if action == ChunkPolicyTraverse && chunk.ID == hidden.Chunk.ID {
			return errors.New("denied")
		}
		return nil
	})
	page, err := service.Neighbors(ctx, NeighborRequest{Endpoint: root, Limit: 1})
	if err != nil || len(page.Neighbors) != 1 || page.Neighbors[0].Link.ID != visibleLink.Link.ID || page.NextCursor != "" {
		t.Fatalf("Neighbors(policy-filtered) = %#v, %v", page, err)
	}
}

func TestNeighborsAppliesScopeFilterBeforePagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	rootCandidate := testChunkCandidate()
	rootCandidate.Kind = memory.ChunkKindProject
	rootCandidate.Scope = memory.Scope{Kind: memory.ScopeKindProject, Selector: "project:test"}
	root, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: rootCandidate})
	hiddenCandidate := testChunkCandidate()
	hiddenCandidate.Title = "Hidden global"
	hidden, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: hiddenCandidate})
	visibleCandidate := rootCandidate
	visibleCandidate.Title = "Visible project"
	visible, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: visibleCandidate})
	rootRef := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(root.Chunk.ID)}
	_, _ = service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: rootRef, Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(hidden.Chunk.ID)},
		Kind: memory.LinkKindRelatedTo,
	}})
	visibleLink, _ := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: rootRef, Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(visible.Chunk.ID)},
		Kind: memory.LinkKindRelatedTo,
	}})

	page, err := service.Neighbors(ctx, NeighborRequest{
		Endpoint: rootRef, ScopeKinds: []memory.ScopeKind{memory.ScopeKindProject}, Limit: 1,
	})
	if err != nil || len(page.Neighbors) != 1 || page.Neighbors[0].Link.ID != visibleLink.Link.ID || page.NextCursor != "" {
		t.Fatalf("Neighbors(scope-filtered) = %#v, %v", page, err)
	}
	if _, err := service.Neighbors(ctx, NeighborRequest{
		Endpoint: rootRef, ScopeKinds: []memory.ScopeKind{memory.ScopeKindUnspecified},
	}); !errors.Is(err, memory.ErrInvalidRecord) {
		t.Fatalf("Neighbors(invalid scope) error = %v, want ErrInvalidRecord", err)
	}
}
