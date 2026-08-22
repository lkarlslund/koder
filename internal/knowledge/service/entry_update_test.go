package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestUpdateEntryAdvancesRevisionAndPreservesServerFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	if err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}
	content := EntryContentFrom(created.Entry)
	content.Title = "  Updated   fact "
	content.Tags = []string{" Go Lang ", "go-lang"}
	content.Confidence = 0.75
	updated, err := service.UpdateEntry(ctx, UpdateEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1, Content: content, Reason: "correct wording",
	})
	if err != nil {
		t.Fatalf("UpdateEntry() error = %v", err)
	}
	if !updated.Updated || updated.Entry.Title != "Updated fact" || len(updated.Entry.Tags) != 1 || updated.Entry.Tags[0] != "go-lang" || updated.Entry.Confidence != 0.75 {
		t.Fatalf("UpdateEntry() = %#v", updated)
	}
	if updated.Entry.Revision.Number != 2 || updated.Entry.Revision.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a5" || updated.Entry.Revision.Reason != "correct wording" {
		t.Fatalf("updated revision = %#v", updated.Entry.Revision)
	}
	if updated.Entry.ID != created.Entry.ID || updated.Entry.ChunkID != created.Entry.ChunkID || updated.Entry.State != created.Entry.State ||
		!reflect.DeepEqual(updated.Entry.Verification, created.Entry.Verification) || !updated.Entry.CreatedAt.Equal(created.Entry.CreatedAt) ||
		!updated.Entry.UpdatedAt.After(created.Entry.UpdatedAt) {
		t.Fatalf("server fields changed: before=%#v after=%#v", created.Entry, updated.Entry)
	}
}

func TestUpdateEntryNoOpDoesNotConsumeRevisionAndStaleUpdateConflicts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	noOp, err := service.UpdateEntry(ctx, UpdateEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1, Content: EntryContentFrom(created.Entry),
	})
	if err != nil || noOp.Updated || noOp.Entry.Revision != created.Entry.Revision {
		t.Fatalf("no-op UpdateEntry() = %#v, %v", noOp, err)
	}
	content := EntryContentFrom(created.Entry)
	content.Summary = "Changed"
	changed, err := service.UpdateEntry(ctx, UpdateEntryRequest{EntryID: created.Entry.ID, ExpectedRevision: 1, Content: content})
	if err != nil {
		t.Fatalf("UpdateEntry(change) error = %v", err)
	}
	if changed.Entry.Revision.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a5" {
		t.Fatalf("no-op consumed revision ID: %#v", changed.Entry.Revision)
	}
	content.Summary = "Stale overwrite"
	if _, err := service.UpdateEntry(ctx, UpdateEntryRequest{EntryID: created.Entry.ID, ExpectedRevision: 1, Content: content}); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("stale UpdateEntry() error = %v, want ErrConflict", err)
	}
	got, _ := service.Entry(ctx, created.Entry.ID)
	if got.Summary != "Changed" || got.Revision.Number != 2 {
		t.Fatalf("stale update changed entry: %#v", got)
	}
}

func TestUpdateEntryRejectsArchivedEntryOrParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	archivedEntry := created.Entry
	archivedEntry.State = knowledge.EntryStateArchived
	archivedEntry.UpdatedAt = archivedEntry.UpdatedAt.Add(1)
	archivedEntry.Revision = knowledge.Revision{
		Number: 2, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8af",
		Actor: archivedEntry.Revision.Actor, CreatedAt: archivedEntry.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutEntry(ctx, archivedEntry, 1) }); err != nil {
		t.Fatalf("archive entry fixture: %v", err)
	}
	if _, err := service.UpdateEntry(ctx, UpdateEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 2, Content: EntryContentFrom(archivedEntry),
	}); !errors.Is(err, ErrEntryNotEditable) {
		t.Fatalf("UpdateEntry(archived) error = %v, want ErrEntryNotEditable", err)
	}

	activeEntry := archivedEntry
	activeEntry.State = knowledge.EntryStateActive
	activeEntry.UpdatedAt = activeEntry.UpdatedAt.Add(1)
	activeEntry.Revision = knowledge.Revision{
		Number: 3, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8ae",
		Actor: activeEntry.Revision.Actor, CreatedAt: activeEntry.UpdatedAt,
	}
	archivedParent := parent.Chunk
	archivedParent.State = knowledge.ChunkStateArchived
	archivedParent.UpdatedAt = archivedParent.UpdatedAt.Add(1)
	archivedParent.Revision = knowledge.Revision{
		Number: 2, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8ad",
		Actor: archivedParent.Revision.Actor, CreatedAt: archivedParent.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutEntry(ctx, activeEntry, 2); err != nil {
			return err
		}
		return tx.PutChunk(ctx, archivedParent, 1)
	}); err != nil {
		t.Fatalf("archive parent fixture: %v", err)
	}
	content := EntryContentFrom(activeEntry)
	content.Summary = "edit"
	if _, err := service.UpdateEntry(ctx, UpdateEntryRequest{EntryID: activeEntry.ID, ExpectedRevision: 3, Content: content}); !errors.Is(err, ErrParentChunkArchived) {
		t.Fatalf("UpdateEntry(archived parent) error = %v, want ErrParentChunkArchived", err)
	}
}

func TestUpdateEntryClassificationOrMissingEvidenceAbortsWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	secret := EntryContentFrom(created.Entry)
	secret.Body = "api_key=extremely-secret-value"
	if _, err := service.UpdateEntry(ctx, UpdateEntryRequest{EntryID: created.Entry.ID, ExpectedRevision: 1, Content: secret}); !errors.Is(err, ErrClassificationRejected) {
		t.Fatalf("UpdateEntry(secret) error = %v", err)
	}
	missing := EntryContentFrom(created.Entry)
	missing.EvidenceIDs = []knowledge.EvidenceID{"01a01688-fc5d-7f7d-8bb8-de244977f8af"}
	if _, err := service.UpdateEntry(ctx, UpdateEntryRequest{EntryID: created.Entry.ID, ExpectedRevision: 1, Content: missing}); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("UpdateEntry(missing evidence) error = %v", err)
	}
	got, _ := service.Entry(ctx, created.Entry.ID)
	if got.Revision.Number != 1 || got.Body != "" || len(got.EvidenceIDs) != 0 {
		t.Fatalf("rejected update changed entry: %#v", got)
	}
}
