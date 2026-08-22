package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestEnsurePersonalChunkIsIdempotentAndCreatesNoFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.EnsurePersonalChunk(ctx)
	if err != nil {
		t.Fatalf("EnsurePersonalChunk() error = %v", err)
	}
	if !created.Created || created.Chunk.ID != PersonalMeChunkID || created.Chunk.Title != "About me" ||
		created.Chunk.Kind != knowledge.ChunkKindPersonal || created.Chunk.Scope != (knowledge.Scope{Kind: knowledge.ScopeKindPersonal, Selector: "me"}) ||
		created.Chunk.Visibility != knowledge.VisibilityPrivate || created.Chunk.State != knowledge.ChunkStateActive || created.Chunk.Counts != (knowledge.ChunkCounts{}) {
		t.Fatalf("seeded personal chunk = %#v", created)
	}
	again, err := service.EnsurePersonalChunk(ctx)
	if err != nil || again.Created || again.Chunk.Revision != created.Chunk.Revision {
		t.Fatalf("second EnsurePersonalChunk() = %#v, %v", again, err)
	}
	stats, err := store.ScanCanonical(ctx, func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil {
		t.Fatalf("ScanCanonical() error = %v", err)
	}
	if stats.Chunks != 1 || stats.Entries != 0 || stats.Links != 0 || stats.Evidence != 0 {
		t.Fatalf("personal seed stats = %#v", stats)
	}
}

func TestPersonalChunkAllowsContentEditsButProtectsIdentityAndLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	seeded, _ := service.EnsurePersonalChunk(ctx)
	content := ChunkContentFrom(seeded.Chunk)
	content.Title = "My profile"
	updated, err := service.UpdateChunk(ctx, UpdateChunkRequest{
		ChunkID: PersonalMeChunkID, ExpectedRevision: 1, Content: content,
	})
	if err != nil || !updated.Updated || updated.Chunk.Title != "My profile" {
		t.Fatalf("UpdateChunk(personal content) = %#v, %v", updated, err)
	}
	content = ChunkContentFrom(updated.Chunk)
	content.Visibility = knowledge.VisibilityInstallation
	if _, err := service.UpdateChunk(ctx, UpdateChunkRequest{
		ChunkID: PersonalMeChunkID, ExpectedRevision: 2, Content: content,
	}); !errors.Is(err, ErrProtectedChunk) {
		t.Fatalf("UpdateChunk(personal visibility) error = %v, want ErrProtectedChunk", err)
	}
	if _, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{
		ChunkID: PersonalMeChunkID, ExpectedRevision: 2,
	}); !errors.Is(err, ErrProtectedChunk) {
		t.Fatalf("ArchiveChunk(personal) error = %v, want ErrProtectedChunk", err)
	}
	if err := service.DeleteChunk(ctx, DeleteChunkRequest{
		ChunkID: PersonalMeChunkID, ExpectedRevision: 2, Confirmed: true,
	}); !errors.Is(err, ErrProtectedChunk) {
		t.Fatalf("DeleteChunk(personal) error = %v, want ErrProtectedChunk", err)
	}
	reserved := testChunkCandidate()
	reserved.Kind = knowledge.ChunkKindPersonal
	reserved.Scope = knowledge.Scope{Kind: knowledge.ScopeKindPersonal, Selector: "me"}
	if _, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: reserved}); !errors.Is(err, ErrProtectedChunk) {
		t.Fatalf("CreateChunk(personal/me duplicate) error = %v, want ErrProtectedChunk", err)
	}
}

func TestEnsurePersonalChunkRejectsReservedIdentityCorruption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	corrupt := knowledge.Chunk{
		ID: PersonalMeChunkID, Title: "Wrong", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityInstallation,
		State: knowledge.ChunkStateActive, SchemaVersion: 1,
		Revision: knowledge.Revision{
			Number: 1, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a1",
			Actor: knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}, CreatedAt: serviceTime,
		},
		CreatedAt: serviceTime, UpdatedAt: serviceTime,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, corrupt, 0) }); err != nil {
		t.Fatalf("seed corrupt reserved chunk: %v", err)
	}
	service := newTestService(t, store, nil)
	if _, err := service.EnsurePersonalChunk(ctx); !errors.Is(err, ErrProtectedChunk) {
		t.Fatalf("EnsurePersonalChunk(corrupt) error = %v, want ErrProtectedChunk", err)
	}
}

func TestPersonalChunkAndEntriesHaveNoUnauthorizedDirectReadPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	if _, err := service.EnsurePersonalChunk(ctx); err != nil {
		t.Fatal(err)
	}
	entryCandidate := testEntryCandidate()
	entryCandidate.PersonalOrigin = knowledge.PersonalOriginExplicit
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: entryCandidate})
	if err != nil {
		t.Fatal(err)
	}
	link, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(PersonalMeChunkID)},
		Kind:   knowledge.LinkKindRelatedTo,
	}})
	if err != nil {
		t.Fatal(err)
	}
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if chunk.ID == PersonalMeChunkID {
			if chunk.Visibility != knowledge.VisibilityPrivate || chunk.Scope != (knowledge.Scope{Kind: knowledge.ScopeKindPersonal, Selector: "me"}) {
				t.Fatalf("personal read policy received non-private metadata: %#v", chunk)
			}
			return errors.New("personal knowledge denied")
		}
		return nil
	})

	if _, err := service.Chunk(ctx, PersonalMeChunkID); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("Chunk(personal) error = %v, want ErrChunkPolicyDenied", err)
	}
	if _, err := service.Entry(ctx, entry.Entry.ID); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("Entry(personal) error = %v, want ErrChunkPolicyDenied", err)
	}
	if _, err := service.Link(ctx, link.Link.ID); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("Link(personal) error = %v, want ErrChunkPolicyDenied", err)
	}
	if _, err := service.MarkChunkUsed(ctx, PersonalMeChunkID); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("MarkChunkUsed(personal) error = %v, want ErrChunkPolicyDenied", err)
	}
	chunks, err := service.ListChunks(ctx, knowledgeStore.ChunkListRequest{})
	if err != nil || len(chunks.Chunks) != 0 {
		t.Fatalf("ListChunks(personal denied) = %#v, %v", chunks, err)
	}
	entries, err := service.ListEntries(ctx, knowledgeStore.EntryListRequest{})
	if err != nil || len(entries.Entries) != 0 {
		t.Fatalf("ListEntries(personal denied) = %#v, %v", entries, err)
	}
	if _, err := service.History(ctx, knowledgeStore.RevisionListRequest{
		Object: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
	}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("History(personal) error = %v, want ErrChunkPolicyDenied", err)
	}
}
