package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestCreateLinkResolvesTypedEndpointsAndUpdatesChunkCounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	firstChunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("create first chunk: %v", err)
	}
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Other chunk"
	secondChunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	if err != nil {
		t.Fatalf("create second chunk: %v", err)
	}
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: firstChunk.Chunk.ID, Entry: testEntryCandidate()})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	candidate := knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(secondChunk.Chunk.ID)},
		Kind:   knowledge.LinkKindAppliesTo, Label: "  Applies   to  ", Notes: "  Shared context  ",
	}
	created, err := service.CreateLink(ctx, CreateLinkRequest{Link: candidate})
	if err != nil {
		t.Fatalf("CreateLink(): %v", err)
	}
	if created.Link.ID == "" || created.Link.Revision.Number != 1 || created.Link.State != knowledge.LinkStateActive ||
		created.Link.Label != "Applies to" || created.Link.Notes != "Shared context" {
		t.Fatalf("created link = %#v", created.Link)
	}
	got, err := service.Link(ctx, created.Link.ID)
	if err != nil || got.ID != created.Link.ID || got.Revision != created.Link.Revision || got.Label != created.Link.Label {
		t.Fatalf("Link() = %#v, %v", got, err)
	}
	first, _ := service.Chunk(ctx, firstChunk.Chunk.ID)
	second, _ := service.Chunk(ctx, secondChunk.Chunk.ID)
	if first.Counts.Links != 1 || second.Counts.Links != 1 {
		t.Fatalf("link counts = first:%#v second:%#v", first.Counts, second.Counts)
	}
}

func TestCreateLinkRejectsMissingMismatchedAndInvalidEndpointsAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	entry, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: testEntryCandidate()})
	valid := knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(chunk.Chunk.ID)},
		Kind:   knowledge.LinkKindPartOf,
	}
	tests := map[string]knowledge.Link{
		"missing target": func() knowledge.Link {
			value := valid
			value.Target.ID = "01a01688-fc5d-7f7d-8bb8-de244977f8af"
			return value
		}(),
		"mismatched kind": func() knowledge.Link {
			value := valid
			value.Source.Kind = knowledge.ObjectKindChunk
			return value
		}(),
		"link endpoint": func() knowledge.Link {
			value := valid
			value.Target = knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: "01a01688-fc5d-7f7d-8bb8-de244977ffe"}
			return value
		}(),
		"same endpoint": func() knowledge.Link {
			value := valid
			value.Target = value.Source
			return value
		}(),
		"missing evidence": func() knowledge.Link {
			value := valid
			value.EvidenceIDs = []knowledge.EvidenceID{"01a01688-fc5d-7f7d-8bb8-de244977f8ae"}
			return value
		}(),
	}
	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := service.CreateLink(ctx, CreateLinkRequest{Link: candidate}); err == nil {
				t.Fatal("CreateLink() unexpectedly succeeded")
			}
		})
	}
	stats, err := store.ScanCanonical(ctx, func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Links != 0 {
		t.Fatalf("rejected link stats = %#v, %v", stats, err)
	}
}

func TestCreateLinkClassifiesTextBeforePersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	candidate := knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: "01a01688-fc5d-7f7d-8bb8-de244977f80"},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: "01a01688-fc5d-7f7d-8bb8-de244977f81"},
		Kind:   knowledge.LinkKindRelatedTo, Notes: "password=extremely-secret-value",
	}
	if _, err := service.CreateLink(ctx, CreateLinkRequest{Link: candidate, ReviewApproved: true}); !errors.Is(err, ErrClassificationRejected) {
		t.Fatalf("CreateLink(secret) error = %v, want ErrClassificationRejected", err)
	}
	stats, err := store.ScanCanonical(ctx, func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Total != 0 {
		t.Fatalf("rejected secret link stats = %#v, %v", stats, err)
	}
}

func TestCreateLinkRejectsSymmetricDuplicatesInEitherOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	left := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(first.Chunk.ID)}
	right := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(second.Chunk.ID)}
	created, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: right, Target: left, Kind: knowledge.LinkKindRelatedTo,
	}})
	if err != nil || created.Link.Source != left || created.Link.Target != right {
		t.Fatalf("canonical CreateLink() = %#v, %v", created, err)
	}
	for _, endpoints := range [][2]knowledge.ObjectRef{{left, right}, {right, left}} {
		_, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
			Source: endpoints[0], Target: endpoints[1], Kind: knowledge.LinkKindRelatedTo,
		}})
		var duplicate *DuplicateLinkError
		if !errors.As(err, &duplicate) || duplicate.Existing.ID != created.Link.ID {
			t.Fatalf("duplicate error = %v, detail=%#v", err, duplicate)
		}
	}
	if _, err := service.Unlink(ctx, LinkLifecycleRequest{LinkID: created.Link.ID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("Unlink(): %v", err)
	}
	if _, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: left, Target: right, Kind: knowledge.LinkKindRelatedTo,
	}}); !errors.Is(err, ErrDuplicateLink) {
		t.Fatalf("duplicate of archived relationship error = %v", err)
	}
}

func TestCreateLinkKeepsReverseDirectedRelationshipsDistinct(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	left := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(first.Chunk.ID)}
	right := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(second.Chunk.ID)}
	for _, endpoints := range [][2]knowledge.ObjectRef{{left, right}, {right, left}} {
		if _, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
			Source: endpoints[0], Target: endpoints[1], Kind: knowledge.LinkKindRequires,
		}}); err != nil {
			t.Fatalf("CreateLink(%v): %v", endpoints, err)
		}
	}
	stats, err := store.ScanCanonical(ctx, func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Links != 2 {
		t.Fatalf("directed link stats = %#v, %v", stats, err)
	}
}
