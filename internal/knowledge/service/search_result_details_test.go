package service

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestSearchLexicalReturnsReasonsWarningsAndAuthorizedContradictions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newLexicalSearchTestService(t, store)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	created, _ := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: knowledge.Entry{Title: "Needle claim", Kind: knowledge.EntryKindFact},
	})
	other, _ := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: knowledge.Entry{Title: "Other claim", Kind: knowledge.EntryKindFact},
	})
	entry := created.Entry
	evidence := knowledge.Evidence{
		ID: "01a01688-fc6b-7a53-a907-4f903461820e", Type: knowledge.EvidenceTypeObservation,
		Quality: knowledge.EvidenceQualityPrimary, Source: knowledge.Source{ID: "warning:test"},
		Actor: knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "test"}, CreatedAt: serviceTime,
	}
	entry.ValidUntil = serviceTime.Add(-1)
	entry.ReviewAfter = serviceTime.Add(-1)
	entry.Verification = knowledge.Verification{
		Status:      knowledge.VerificationStatusDisputed,
		EvidenceIDs: []knowledge.EvidenceID{evidence.ID},
		Actor:       knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "test"}, VerifiedAt: serviceTime.Add(-2),
	}
	entry.Revision = knowledge.Revision{
		Number: 2, ID: "01a01688-fc5d-7f7d-8bb8-de244977f900",
		Actor: knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "test"}, CreatedAt: serviceTime.Add(1),
	}
	entry.UpdatedAt = entry.Revision.CreatedAt
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutEvidence(ctx, evidence); err != nil {
			return err
		}
		return tx.PutEntry(ctx, entry, 1)
	}); err != nil {
		t.Fatalf("update warning fixture: %v", err)
	}
	link, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(other.Entry.ID)},
		Kind:   knowledge.LinkKindContradicts,
	}})
	if err != nil {
		t.Fatalf("CreateLink() error = %v", err)
	}
	deniedChunk, denied := createLexicalSearchEntry(t, ctx, service, "Denied", knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, "Denied claim")
	deniedLink, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(denied.ID)},
		Kind:   knowledge.LinkKindContradicts,
	}})
	if err != nil {
		t.Fatalf("CreateLink(denied) error = %v", err)
	}
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, _ ChunkPolicyAction, chunk knowledge.Chunk) error {
		if chunk.ID == deniedChunk.ID {
			return fmt.Errorf("denied fixture")
		}
		return nil
	})

	result, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "needle", IncludeInvalid: true})
	if err != nil {
		t.Fatalf("SearchLexical() error = %v", err)
	}
	if len(result.Matches) != 1 || len(result.Matches[0].Reasons) != 1 {
		t.Fatalf("search reasons = %#v", result.Matches)
	}
	reason := result.Matches[0].Reasons[0]
	if reason.Kind != SearchMatchReasonLexical || reason.Term != "needle" || !slices.Equal(reason.Fields, []string{"title"}) {
		t.Fatalf("lexical reason = %#v", reason)
	}
	warningCodes := make([]SearchWarningCode, 0, len(result.Warnings))
	for _, warning := range result.Warnings {
		warningCodes = append(warningCodes, warning.Code)
	}
	for _, want := range []SearchWarningCode{SearchWarningDisputed, SearchWarningExpired, SearchWarningReviewDue} {
		if !slices.Contains(warningCodes, want) {
			t.Fatalf("warning codes = %v, missing %s", warningCodes, want)
		}
	}
	if len(result.Contradictions) != 1 || result.Contradictions[0].LinkID != link.Link.ID {
		t.Fatalf("contradictions = %#v; denied link %s must not leak", result.Contradictions, deniedLink.Link.ID)
	}
}
