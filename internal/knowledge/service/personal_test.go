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
