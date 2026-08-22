package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestCreateEvidenceDeduplicatesNormalizedSourceHashWithoutConsumingID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	candidate := testEvidenceCandidate()
	candidate.Source.ID = " source:manual "
	candidate.Source.ContentHash = " SHA256:ABCDEF "
	first, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: candidate})
	if err != nil {
		t.Fatalf("CreateEvidence() error = %v", err)
	}
	if !first.Created || first.Evidence.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a1" ||
		first.Evidence.Source.ID != "source:manual" || first.Evidence.Source.ContentHash != "sha256:abcdef" ||
		first.Evidence.Actor.ID != "user:test" || !first.Evidence.CreatedAt.Equal(serviceTime) {
		t.Fatalf("CreateEvidence() = %#v", first)
	}
	duplicate := candidate
	duplicate.Source.Title = "Different retry metadata"
	second, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: duplicate})
	if err != nil || second.Created || second.Evidence.ID != first.Evidence.ID || second.Evidence.Source.Title != first.Evidence.Source.Title {
		t.Fatalf("CreateEvidence(duplicate) = %#v, %v", second, err)
	}
	different := candidate
	different.Source.ContentHash = "sha256:different"
	third, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: different})
	if err != nil || !third.Created || third.Evidence.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a2" {
		t.Fatalf("CreateEvidence(different hash) = %#v, %v", third, err)
	}
	if err := store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		got, err := tx.Evidence(ctx, first.Evidence.ID)
		if err != nil || got.ID != first.Evidence.ID {
			t.Fatalf("stored Evidence() = %#v, %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateEvidenceRejectsSecretsAndServerOwnedFieldsBeforePersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	classifier := &recordingClassifier{result: knowledge.ClassificationResult{Decision: knowledge.ClassificationDecisionAllow}}
	service := newTestService(t, store, classifier)
	serverOwned := testEvidenceCandidate()
	serverOwned.ID = "01a01688-fc5d-7f7d-8bb8-de244977f8af"
	if _, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: serverOwned}); !errors.Is(err, knowledge.ErrInvalidRecord) {
		t.Fatalf("CreateEvidence(server fields) error = %v, want ErrInvalidRecord", err)
	}
	if classifier.calls != 0 {
		t.Fatalf("server-owned evidence reached classifier; calls=%d", classifier.calls)
	}
	secret := testEvidenceCandidate()
	secret.Source.Excerpt = "password=extremely-secret-value"
	classifier.result = knowledge.ClassificationResult{Decision: knowledge.ClassificationDecisionReject}
	if _, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: secret}); !errors.Is(err, ErrClassificationRejected) {
		t.Fatalf("CreateEvidence(secret) error = %v, want ErrClassificationRejected", err)
	}
	stats, err := store.ScanCanonical(ctx, func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Evidence != 0 {
		t.Fatalf("rejected evidence stats = %#v, %v", stats, err)
	}
}

func TestEntryEvidenceIsAuthorizedPagedAndRevisionBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	firstCandidate := testEvidenceCandidate()
	firstCandidate.Source.ContentHash = "sha256:first"
	first, _ := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: firstCandidate})
	secondCandidate := testEvidenceCandidate()
	secondCandidate.Source.ContentHash = "sha256:second"
	second, _ := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: secondCandidate})
	entryCandidate := testEntryCandidate()
	entryCandidate.EvidenceIDs = []knowledge.EvidenceID{second.Evidence.ID, first.Evidence.ID}
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: entryCandidate})
	if err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}
	firstPage, err := service.EntryEvidence(ctx, EntryEvidenceRequest{EntryID: entry.Entry.ID, Limit: 1})
	if err != nil || len(firstPage.Evidence) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("EntryEvidence(first) = %#v, %v", firstPage, err)
	}
	secondPage, err := service.EntryEvidence(ctx, EntryEvidenceRequest{EntryID: entry.Entry.ID, Limit: 1, Cursor: firstPage.NextCursor})
	if err != nil || len(secondPage.Evidence) != 1 || secondPage.NextCursor != "" || secondPage.Evidence[0].ID == firstPage.Evidence[0].ID {
		t.Fatalf("EntryEvidence(second) = %#v, %v", secondPage, err)
	}

	content := EntryContentFrom(entry.Entry)
	content.Summary = "revision changes cursor generation"
	if _, err := service.UpdateEntry(ctx, UpdateEntryRequest{EntryID: entry.Entry.ID, ExpectedRevision: 1, Content: content}); err != nil {
		t.Fatalf("UpdateEntry() error = %v", err)
	}
	if _, err := service.EntryEvidence(ctx, EntryEvidenceRequest{EntryID: entry.Entry.ID, Limit: 1, Cursor: firstPage.NextCursor}); err == nil {
		t.Fatal("EntryEvidence() accepted a cursor from an older entry revision")
	}
	service.chunkPolicy = denyChunkAction(ChunkPolicyRead)
	if _, err := service.EntryEvidence(ctx, EntryEvidenceRequest{EntryID: entry.Entry.ID}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("EntryEvidence(denied) error = %v", err)
	}
}

func testEvidenceCandidate() knowledge.Evidence {
	return knowledge.Evidence{
		Type: knowledge.EvidenceTypeObservation, Quality: knowledge.EvidenceQualityPrimary,
		Source: knowledge.Source{ID: "source:test", Title: " Manual observation "},
	}
}
