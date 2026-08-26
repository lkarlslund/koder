package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestCreateAndGetEntryCanonicalizesAndUpdatesParentCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	classifier := &recordingClassifier{result: memory.ClassificationResult{Decision: memory.ClassificationDecisionAllow}}
	service := newTestService(t, store, classifier)
	parent, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	candidate := testEntryCandidate()
	candidate.Title = "  Go\u0308   modules "
	candidate.Aliases = []string{" GÖ modules ", "mod", "MOD"}
	candidate.Tags = []string{" Go Tools ", "go-tools"}
	result, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: candidate})
	if err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}
	entry := result.Entry
	if entry.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a3" || entry.ChunkID != parent.Chunk.ID ||
		entry.Revision.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a4" || entry.Revision.Number != 1 {
		t.Fatalf("assigned entry identity = %#v", entry)
	}
	if entry.Title != "Gö modules" || len(entry.Aliases) != 1 || entry.Aliases[0] != "mod" ||
		len(entry.Tags) != 1 || entry.Tags[0] != "go-tools" || entry.Scope != parent.Chunk.Scope ||
		entry.State != memory.EntryStateActive || entry.Verification.Status != memory.VerificationStatusUnverified {
		t.Fatalf("canonical entry = %#v", entry)
	}
	if classifier.calls != 2 || classificationField(classifier.input, "title") != "  Go\u0308   modules " {
		t.Fatalf("entry classifier input = %#v, calls=%d", classifier.input, classifier.calls)
	}
	got, err := service.Entry(ctx, entry.ID)
	if err != nil || got.Revision != entry.Revision || got.Title != entry.Title {
		t.Fatalf("Entry() = %#v, %v", got, err)
	}
	updatedParent, err := service.Chunk(ctx, parent.Chunk.ID)
	if err != nil || updatedParent.Counts.Entries != 1 {
		t.Fatalf("parent count after entry = %#v, %v", updatedParent.Counts, err)
	}
}

func TestCreateEntryRejectsSecretBeforePersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	candidate := testEntryCandidate()
	candidate.Body = "password=extremely-secret-value"
	if _, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: candidate}); !errors.Is(err, ErrClassificationRejected) {
		t.Fatalf("CreateEntry(secret) error = %v, want ErrClassificationRejected", err)
	}
	stats, err := store.ScanCanonical(ctx, func(memoryStoreAPI.CanonicalRecord) error { return nil })
	if err != nil || stats.Entries != 0 {
		t.Fatalf("rejected entry stats = %#v, %v", stats, err)
	}
}

func TestCreateEntryRejectsArchivedParentAndMissingEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	archived := parent.Chunk
	archived.State = memory.ChunkStateArchived
	archived.UpdatedAt = archived.UpdatedAt.Add(1)
	archived.Revision = memory.Revision{
		Number: 2, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8af",
		Actor: archived.Revision.Actor, Reason: "fixture", CreatedAt: archived.UpdatedAt,
	}
	if err := store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, archived, 1) }); err != nil {
		t.Fatalf("archive fixture: %v", err)
	}
	if _, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()}); !errors.Is(err, ErrParentChunkArchived) {
		t.Fatalf("CreateEntry(archived) error = %v, want ErrParentChunkArchived", err)
	}

	activeStore := memoryBackend.New()
	t.Cleanup(func() { _ = activeStore.Close() })
	activeService := newTestService(t, activeStore, nil)
	activeParent, _ := activeService.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	candidate := testEntryCandidate()
	candidate.EvidenceIDs = []memory.EvidenceID{"01a01688-fc5d-7f7d-8bb8-de244977f8ae"}
	if _, err := activeService.CreateEntry(ctx, CreateEntryRequest{ChunkID: activeParent.Chunk.ID, Entry: candidate}); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("CreateEntry(missing evidence) error = %v, want ErrNotFound", err)
	}
}

func TestCreateEntryRejectsServerOwnedVerification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	classifier := &recordingClassifier{result: memory.ClassificationResult{Decision: memory.ClassificationDecisionAllow}}
	service := newTestService(t, store, classifier)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	candidate := testEntryCandidate()
	candidate.Verification = memory.Verification{Status: memory.VerificationStatusVerified}
	if _, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: candidate}); !errors.Is(err, memory.ErrInvalidRecord) {
		t.Fatalf("CreateEntry(verification) error = %v, want ErrInvalidRecord", err)
	}
	if classifier.calls != 1 {
		t.Fatalf("server-owned entry reached classifier; calls=%d", classifier.calls)
	}
}

func testEntryCandidate() memory.Entry {
	return memory.Entry{Title: "Test entry", Kind: memory.EntryKindFact}
}
