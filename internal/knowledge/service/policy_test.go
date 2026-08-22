package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestCrossChunkLinkChecksEveryOwningChunkPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	entry, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: first.Chunk.ID, Entry: testEntryCandidate()})

	var checked []knowledge.ChunkID
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if action != ChunkPolicyLinkCreate {
			t.Fatalf("policy action = %q", action)
		}
		checked = append(checked, chunk.ID)
		if chunk.ID == second.Chunk.ID {
			return fmt.Errorf("fixture denies target")
		}
		return nil
	})
	_, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(second.Chunk.ID)},
		Kind:   knowledge.LinkKindRelatedTo,
	}})
	if !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("CreateLink() error = %v, want ErrChunkPolicyDenied", err)
	}
	if len(checked) != 2 || checked[0] != first.Chunk.ID || checked[1] != second.Chunk.ID {
		t.Fatalf("checked chunks = %v", checked)
	}
}

func TestLinkCreateAndRestoreRequireActiveEndpointChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: second.Chunk.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("ArchiveChunk(): %v", err)
	}
	_, err = service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(first.Chunk.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(archived.Chunk.ID)},
		Kind:   knowledge.LinkKindRelatedTo,
	}})
	if !errors.Is(err, ErrLinkEndpointUnavailable) {
		t.Fatalf("CreateLink(archived endpoint) error = %v, want ErrLinkEndpointUnavailable", err)
	}
}

func TestUnlinkChecksBothPoliciesButAllowsArchivedEndpointCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created := createServiceLinkFixture(t, ctx, service)
	if _, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{
		ChunkID: knowledge.ChunkID(created.Target.ID), ExpectedRevision: 1,
	}); err != nil {
		t.Fatalf("ArchiveChunk(endpoint): %v", err)
	}
	var calls int
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, _ knowledge.Chunk) error {
		calls++
		if action != ChunkPolicyLinkUnlink {
			t.Fatalf("policy action = %q", action)
		}
		return nil
	})
	if _, err := service.Unlink(ctx, LinkLifecycleRequest{LinkID: created.ID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("Unlink(): %v", err)
	}
	if calls != 1 {
		t.Fatalf("same-chunk policy calls = %d, want 1", calls)
	}
}
