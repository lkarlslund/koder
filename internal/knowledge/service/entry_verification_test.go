package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestVerifyEntryRecordsEvidenceActorAndRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	evidence, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: testEvidenceCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	wantActor := knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}
	result, err := service.VerifyEntry(ctx, VerifyEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1,
		Status: knowledge.VerificationStatusVerified, Method: "Reviewed primary observation",
		EvidenceIDs: []knowledge.EvidenceID{evidence.Evidence.ID}, Reason: "confirm command behavior",
	})
	if err != nil {
		t.Fatalf("VerifyEntry() error = %v", err)
	}
	if !result.Updated || result.Entry.Revision.Number != 2 || result.Entry.Revision.Actor != wantActor ||
		result.Entry.Revision.Reason != "confirm command behavior" || result.Entry.Verification.Status != knowledge.VerificationStatusVerified ||
		result.Entry.Verification.Method != "Reviewed primary observation" || result.Entry.Verification.Actor != wantActor ||
		len(result.Entry.Verification.EvidenceIDs) != 1 || result.Entry.Verification.EvidenceIDs[0] != evidence.Evidence.ID ||
		!result.Entry.Verification.VerifiedAt.Equal(result.Entry.UpdatedAt) || !result.Entry.UpdatedAt.After(created.Entry.UpdatedAt) {
		t.Fatalf("VerifyEntry() = %#v", result)
	}

	unverified, err := service.VerifyEntry(ctx, VerifyEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 2,
		Status: knowledge.VerificationStatusUnverified, Method: "ignored",
		EvidenceIDs: []knowledge.EvidenceID{evidence.Evidence.ID},
	})
	if err != nil {
		t.Fatalf("VerifyEntry(unverified) error = %v", err)
	}
	if !unverified.Updated || unverified.Entry.Verification.Status != knowledge.VerificationStatusUnverified ||
		unverified.Entry.Verification.Method != "" || len(unverified.Entry.Verification.EvidenceIDs) != 0 ||
		unverified.Entry.Verification.Actor != (knowledge.Actor{}) || !unverified.Entry.Verification.VerifiedAt.IsZero() ||
		unverified.Entry.Revision.Number != 3 {
		t.Fatalf("VerifyEntry(unverified) = %#v", unverified)
	}
	noOp, err := service.VerifyEntry(ctx, VerifyEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 3, Status: knowledge.VerificationStatusUnverified,
	})
	if err != nil || noOp.Updated || noOp.Entry.Revision != unverified.Entry.Revision {
		t.Fatalf("VerifyEntry(unverified no-op) = %#v, %v", noOp, err)
	}
}

func TestVerifyEntryRejectsMissingEvidenceStaleRevisionAndArchivedEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	parent, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	missingID := knowledge.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8af")
	if _, err := service.VerifyEntry(ctx, VerifyEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1, Status: knowledge.VerificationStatusVerified,
		EvidenceIDs: []knowledge.EvidenceID{missingID},
	}); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("VerifyEntry(missing evidence) error = %v, want ErrNotFound", err)
	}
	evidence, _ := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: testEvidenceCandidate()})
	verified, err := service.VerifyEntry(ctx, VerifyEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1, Status: knowledge.VerificationStatusPartiallyVerified,
		EvidenceIDs: []knowledge.EvidenceID{evidence.Evidence.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyEntry(ctx, VerifyEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1, Status: knowledge.VerificationStatusDisputed,
		EvidenceIDs: []knowledge.EvidenceID{evidence.Evidence.ID},
	}); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("VerifyEntry(stale) error = %v, want ErrConflict", err)
	}
	archived, err := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: created.Entry.ID, ExpectedRevision: verified.Entry.Revision.Number})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyEntry(ctx, VerifyEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: archived.Entry.Revision.Number,
		Status: knowledge.VerificationStatusVerified, EvidenceIDs: []knowledge.EvidenceID{evidence.Evidence.ID},
	}); !errors.Is(err, ErrEntryNotEditable) {
		t.Fatalf("VerifyEntry(archived) error = %v, want ErrEntryNotEditable", err)
	}
}

func TestVerifyEntryRequiresAssessedEvidence(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	_, err := service.VerifyEntry(context.Background(), VerifyEntryRequest{
		EntryID: "01a01688-fc5d-7f7d-8bb8-de244977f8a1", ExpectedRevision: 1,
		Status: knowledge.VerificationStatusVerified,
	})
	if !errors.Is(err, knowledge.ErrInvalidRecord) {
		t.Fatalf("VerifyEntry(no evidence) error = %v, want ErrInvalidRecord", err)
	}
}
