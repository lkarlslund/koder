package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func curatedTestSource() knowledge.CompletedTurnRef {
	return knowledge.CompletedTurnRef{
		SessionID: "00000000-0000-7000-8000-000000000061", ChatID: "00000000-0000-7000-8000-000000000062",
		UserItemID: "00000000-0000-7000-8000-000000000063", AssistantItemID: "00000000-0000-7000-8000-000000000064",
		SealedAt: serviceTime.Add(-time.Minute),
	}
}

func curatedCreateRequest(chunkID knowledge.ChunkID) ApplyCuratedEntryRequest {
	return ApplyCuratedEntryRequest{
		RecordID: "00000000-0000-7000-8000-000000000060", Source: curatedTestSource(),
		SourceItemIDs: []string{"00000000-0000-7000-8000-000000000063", "00000000-0000-7000-8000-000000000064"},
		Action:        CuratedEntryActionCreate, ChunkID: chunkID, Reason: "Successful fallback after user correction",
		Content: EntryContent{
			Kind: knowledge.EntryKindFact, Title: "Use sfdisk", Summary: "Use sfdisk when fdisk is unavailable.",
			Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Confidence: 0.95,
		},
	}
}

func TestApplyCuratedEntryAtomicallyCreatesVerifiedKnowledgeAndAuditEvidence(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ApplyCuratedEntry(context.Background(), curatedCreateRequest(chunk.Chunk.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Updated || result.Entry.Verification.Status != knowledge.VerificationStatusVerified ||
		result.Entry.Verification.Method != "curation:completed_turn" || len(result.Entry.EvidenceIDs) != 1 ||
		!strings.Contains(result.Entry.Revision.Reason, "00000000-0000-7000-8000-000000000060") {
		t.Fatalf("ApplyCuratedEntry() = %#v", result)
	}
	page, err := service.EntryEvidence(context.Background(), EntryEvidenceRequest{EntryID: result.Entry.ID})
	if err != nil || len(page.Evidence) != 1 || page.Evidence[0].Type != knowledge.EvidenceTypeChatTurn ||
		page.Evidence[0].Source.Excerpt != "" || !page.Evidence[0].ObservedAt.Equal(curatedTestSource().SealedAt) {
		t.Fatalf("curated evidence = %#v, %v", page, err)
	}
}

func TestApplyCuratedEntryUsesOptimisticRevisionForUpdate(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateEntry(context.Background(), CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: testEntryCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	request := curatedCreateRequest(chunk.Chunk.ID)
	request.Action = CuratedEntryActionUpdate
	request.TargetEntryID = created.Entry.ID
	request.ExpectedRevision = created.Entry.Revision.Number
	request.Content = EntryContentFrom(created.Entry)
	request.Content.Summary = "Corrected after a successful completed turn."
	request.Content.EvidenceIDs = nil
	result, err := service.ApplyCuratedEntry(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.Entry.Revision.Number != 2 || result.Entry.Summary != request.Content.Summary || result.Entry.Verification.Status != knowledge.VerificationStatusVerified {
		t.Fatalf("ApplyCuratedEntry(update) = %#v", result)
	}
	request.ExpectedRevision = 1
	if _, err := service.ApplyCuratedEntry(context.Background(), request); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("ApplyCuratedEntry(stale) error = %v", err)
	}
}

func TestApplyCuratedEntryKeepsRiskAndReviewCandidatesOutOfAutomaticPath(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	request := curatedCreateRequest(chunk.Chunk.ID)
	request.Content.Risk = []knowledge.RiskClass{knowledge.RiskClassMedical}
	if _, err := service.ApplyCuratedEntry(context.Background(), request); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("ApplyCuratedEntry(risk) error = %v", err)
	}
	request = curatedCreateRequest(chunk.Chunk.ID)
	request.Content.Summary = "Contact personal@example.dk"
	if _, err := service.ApplyCuratedEntry(context.Background(), request); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("ApplyCuratedEntry(review) error = %v", err)
	}
	stats, err := store.ScanCanonical(context.Background(), func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Entries != 0 || stats.Evidence != 0 {
		t.Fatalf("rejected curation wrote records: %#v, %v", stats, err)
	}
}

func TestApplyCuratedEntryAllowsHumanReviewedRiskCandidate(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	request := curatedCreateRequest(chunk.Chunk.ID)
	request.Content.Risk = []knowledge.RiskClass{knowledge.RiskClassMedical}
	request.ReviewApproved = true
	result, err := service.ApplyCuratedEntry(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || !slices.Contains(result.Entry.Risk, knowledge.RiskClassMedical) {
		t.Fatalf("ApplyCuratedEntry(review approved) = %#v", result)
	}
}

func TestApplyCuratedEntryRollsBackEvidenceWhenEntryValidationFails(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	ids := []string{
		"00000000-0000-7000-8000-000000000071", "00000000-0000-7000-8000-000000000072",
		"00000000-0000-7000-8000-000000000073", "not-a-uuid", "00000000-0000-7000-8000-000000000075",
	}
	service, err := New(Config{
		Store: store,
		Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:curator"}),
		Now:   func() time.Time { return serviceTime },
		NewID: func() string { value := ids[0]; ids = ids[1:]; return value },
	})
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyCuratedEntry(context.Background(), curatedCreateRequest(chunk.Chunk.ID)); !errors.Is(err, knowledge.ErrInvalidRecord) {
		t.Fatalf("ApplyCuratedEntry(invalid generated ID) error = %v", err)
	}
	stats, err := store.ScanCanonical(context.Background(), func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Chunks != 1 || stats.Entries != 0 || stats.Evidence != 0 {
		t.Fatalf("failed curation was not rolled back: %#v, %v", stats, err)
	}
}
