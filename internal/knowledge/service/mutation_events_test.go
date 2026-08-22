package service

import (
	"context"
	"errors"
	"testing"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestMutationEventsAreRevisionOrderedAndSkipFailedOrNoopWrites(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	events, unsubscribe := service.SubscribeMutations(8)
	ctx, err := WithAuditID(context.Background(), "request:test-1")
	if err != nil {
		t.Fatalf("WithAuditID() error = %v", err)
	}

	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	first := receiveMutation(t, events)
	assertMutation(t, first, MutationCreated, knowledgeStore.RecordKindChunk, string(created.Chunk.ID), 1, 1)
	if first.AuditID != "request:test-1" {
		t.Fatalf("mutation audit ID = %q", first.AuditID)
	}
	if checkpoint := service.MutationCheckpoint(); checkpoint.StreamID != first.StreamID || checkpoint.Sequence != first.Sequence {
		t.Fatalf("mutation checkpoint = %#v, want event checkpoint %#v", checkpoint, first)
	}

	noop, err := service.UpdateChunk(ctx, UpdateChunkRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: ChunkContentFrom(created.Chunk),
	})
	if err != nil || noop.Updated {
		t.Fatalf("UpdateChunk(noop) = %#v, %v", noop, err)
	}
	assertNoMutation(t, events)

	content := ChunkContentFrom(created.Chunk)
	content.Title = "Revised chunk"
	updated, err := service.UpdateChunk(ctx, UpdateChunkRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: content,
	})
	if err != nil {
		t.Fatalf("UpdateChunk() error = %v", err)
	}
	second := receiveMutation(t, events)
	assertMutation(t, second, MutationUpdated, knowledgeStore.RecordKindChunk, string(created.Chunk.ID), 2, 2)
	if second.StreamID != first.StreamID {
		t.Fatalf("mutation stream changed from %q to %q", first.StreamID, second.StreamID)
	}

	content.Title = "Stale write"
	if _, err := service.UpdateChunk(ctx, UpdateChunkRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: content,
	}); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("UpdateChunk(stale) error = %v, want conflict", err)
	}
	assertNoMutation(t, events)

	archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: updated.Chunk.Revision.Number,
	})
	if err != nil {
		t.Fatalf("ArchiveChunk() error = %v", err)
	}
	third := receiveMutation(t, events)
	assertMutation(t, third, MutationArchived, knowledgeStore.RecordKindChunk, string(created.Chunk.ID), 3, 3)

	if err := service.DeleteChunk(ctx, DeleteChunkRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: archived.Chunk.Revision.Number, Confirmed: true,
	}); err != nil {
		t.Fatalf("DeleteChunk() error = %v", err)
	}
	fourth := receiveMutation(t, events)
	assertMutation(t, fourth, MutationDeleted, knowledgeStore.RecordKindChunk, string(created.Chunk.ID), 3, 4)
	if checkpoint := service.MutationCheckpoint(); checkpoint.Sequence != fourth.Sequence {
		t.Fatalf("final mutation checkpoint = %#v", checkpoint)
	}

	unsubscribe()
	if _, open := <-events; open {
		t.Fatal("mutation subscription remained open after unsubscribe")
	}
}

func receiveMutation(t *testing.T, events <-chan MutationEvent) MutationEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	default:
		t.Fatal("expected mutation event")
		return MutationEvent{}
	}
}

func assertNoMutation(t *testing.T, events <-chan MutationEvent) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected mutation event: %#v", event)
	default:
	}
}

func assertMutation(t *testing.T, event MutationEvent, kind MutationKind, recordKind knowledgeStore.RecordKind, objectID string, revision, sequence uint64) {
	t.Helper()
	if event.StreamID == "" || event.Sequence != sequence || event.Kind != kind || event.Object.Kind != recordKind || event.Object.ID != objectID {
		t.Fatalf("mutation event = %#v", event)
	}
	if event.Revision == nil || event.Revision.Number != revision {
		t.Fatalf("mutation revision = %#v, want %d", event.Revision, revision)
	}
}
