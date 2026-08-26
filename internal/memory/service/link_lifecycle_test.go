package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestUnlinkAndRestoreAreRevisionedAndReversible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created := createServiceLinkFixture(t, ctx, service)

	unlinked, err := service.Unlink(ctx, LinkLifecycleRequest{LinkID: created.ID, ExpectedRevision: 1})
	if err != nil || !unlinked.Updated || unlinked.Link.State != memory.LinkStateArchived ||
		unlinked.Link.Revision.Number != 2 || unlinked.Link.Revision.Reason != "unlink relationship" {
		t.Fatalf("Unlink() = %#v, %v", unlinked, err)
	}
	restored, err := service.RestoreLink(ctx, LinkLifecycleRequest{
		LinkID: created.ID, ExpectedRevision: 2, Reason: "relationship applies again",
	})
	if err != nil || !restored.Updated || restored.Link.State != memory.LinkStateActive ||
		restored.Link.Revision.Number != 3 || restored.Link.Revision.Reason != "relationship applies again" {
		t.Fatalf("RestoreLink() = %#v, %v", restored, err)
	}
	page, err := service.History(ctx, memoryStoreAPI.RevisionListRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(created.ID)},
	})
	if err != nil || len(page.Revisions) != 3 || page.Revisions[0].Link.State != memory.LinkStateActive ||
		page.Revisions[1].Link.State != memory.LinkStateArchived {
		t.Fatalf("link history = %#v, %v", page, err)
	}
}

func TestLinkLifecycleIsIdempotentAndRejectsStaleRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created := createServiceLinkFixture(t, ctx, service)
	unlinked, err := service.Unlink(ctx, LinkLifecycleRequest{LinkID: created.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("Unlink(): %v", err)
	}
	noOp, err := service.Unlink(ctx, LinkLifecycleRequest{LinkID: created.ID, ExpectedRevision: 2})
	if err != nil || noOp.Updated || noOp.Link.Revision.Number != 2 {
		t.Fatalf("idempotent Unlink() = %#v, %v", noOp, err)
	}
	if _, err := service.RestoreLink(ctx, LinkLifecycleRequest{LinkID: created.ID, ExpectedRevision: 1}); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("stale RestoreLink() error = %v, want ErrConflict", err)
	}
	got, _ := service.Link(ctx, created.ID)
	if got.Revision != unlinked.Link.Revision || got.State != memory.LinkStateArchived {
		t.Fatalf("stale lifecycle changed link: %#v", got)
	}
}

func createServiceLinkFixture(t *testing.T, ctx context.Context, service *Service) memory.Link {
	t.Helper()
	chunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("create chunk: %v", err)
	}
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: testEntryCandidate()})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	created, err := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunk.Chunk.ID)},
		Kind:   memory.LinkKindPartOf,
	}})
	if err != nil {
		t.Fatalf("CreateLink(): %v", err)
	}
	return created.Link
}
