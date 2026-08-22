package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestUpdateChunkAdvancesRevisionAndPreservesServerFields(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	content := ChunkContentFrom(created.Chunk)
	content.Title = "  Updated   title "
	content.Tags = []string{" Go Lang ", "go-lang"}
	updated, err := service.UpdateChunk(context.Background(), UpdateChunkRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: content, Reason: "correct title",
	})
	if err != nil {
		t.Fatalf("UpdateChunk() error = %v", err)
	}
	if !updated.Updated || updated.Chunk.Title != "Updated title" || len(updated.Chunk.Tags) != 1 || updated.Chunk.Tags[0] != "go-lang" {
		t.Fatalf("UpdateChunk() = %#v", updated)
	}
	if updated.Chunk.Revision.Number != 2 || updated.Chunk.Revision.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a3" || updated.Chunk.Revision.Reason != "correct title" {
		t.Fatalf("updated revision = %#v", updated.Chunk.Revision)
	}
	if updated.Chunk.ID != created.Chunk.ID || !updated.Chunk.CreatedAt.Equal(created.Chunk.CreatedAt) || updated.Chunk.State != created.Chunk.State || updated.Chunk.SchemaVersion != created.Chunk.SchemaVersion {
		t.Fatalf("server fields changed: before=%#v after=%#v", created.Chunk, updated.Chunk)
	}
	if !updated.Chunk.UpdatedAt.After(created.Chunk.UpdatedAt) || !updated.Chunk.Revision.CreatedAt.Equal(updated.Chunk.UpdatedAt) {
		t.Fatalf("update time did not advance monotonically: before=%v after=%v", created.Chunk.UpdatedAt, updated.Chunk.UpdatedAt)
	}
}

func TestUpdateChunkNoOpDoesNotCreateRevision(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	result, err := service.UpdateChunk(context.Background(), UpdateChunkRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: ChunkContentFrom(created.Chunk),
	})
	if err != nil {
		t.Fatalf("UpdateChunk() error = %v", err)
	}
	if result.Updated || result.Chunk.Revision != created.Chunk.Revision || !result.Chunk.UpdatedAt.Equal(created.Chunk.UpdatedAt) {
		t.Fatalf("no-op update changed revision: %#v", result)
	}

	content := ChunkContentFrom(created.Chunk)
	content.Description = "real change"
	changed, err := service.UpdateChunk(context.Background(), UpdateChunkRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: content})
	if err != nil {
		t.Fatalf("UpdateChunk(real change) error = %v", err)
	}
	if changed.Chunk.Revision.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a3" {
		t.Fatalf("no-op consumed revision identity: %#v", changed.Chunk.Revision)
	}
}

func TestUpdateChunkReturnsOptimisticConflict(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, _ := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	content := ChunkContentFrom(created.Chunk)
	content.Description = "first"
	if _, err := service.UpdateChunk(context.Background(), UpdateChunkRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: content}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	content.Description = "stale overwrite"
	if _, err := service.UpdateChunk(context.Background(), UpdateChunkRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: content}); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("stale UpdateChunk() error = %v, want ErrConflict", err)
	}
	got, _ := service.Chunk(context.Background(), created.Chunk.ID)
	if got.Description != "first" || got.Revision.Number != 2 {
		t.Fatalf("stale update changed chunk: %#v", got)
	}
}

func TestUpdateChunkClassificationAndValidationAbortWrite(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, _ := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})

	secret := ChunkContentFrom(created.Chunk)
	secret.Description = "password=extremely-secret-value"
	if _, err := service.UpdateChunk(context.Background(), UpdateChunkRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: secret}); !errors.Is(err, ErrClassificationRejected) {
		t.Fatalf("secret UpdateChunk() error = %v", err)
	}
	invalid := ChunkContentFrom(created.Chunk)
	invalid.Kind = knowledge.ChunkKindUnspecified
	if _, err := service.UpdateChunk(context.Background(), UpdateChunkRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: invalid}); !errors.Is(err, knowledge.ErrInvalidRecord) {
		t.Fatalf("invalid UpdateChunk() error = %v", err)
	}
	got, _ := service.Chunk(context.Background(), created.Chunk.ID)
	if got.Revision.Number != 1 || got.Description != "" {
		t.Fatalf("rejected update changed chunk: %#v", got)
	}
}
