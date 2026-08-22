package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestActiveHighRiskChunkRequiresApplicabilitySourceAndReviewMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	valid := testChunkCandidate()
	valid.Title = "Danish over-the-counter medicine"
	valid.Locale = "da-DK"
	valid.Domain = "medicine"
	valid.Risk = []knowledge.RiskClass{knowledge.RiskClassMedical}
	valid.SourcePolicy = "Require current authoritative Danish medicine sources."
	valid.ReviewAfter = serviceTime.AddDate(0, 1, 0)

	for _, field := range []string{"locale", "domain", "source_policy", "review_after"} {
		candidate := valid
		switch field {
		case "locale":
			candidate.Locale = ""
		case "domain":
			candidate.Domain = ""
		case "source_policy":
			candidate.SourcePolicy = ""
		case "review_after":
			candidate.ReviewAfter = time.Time{}
		}
		_, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: candidate, ReviewApproved: true})
		if !errors.Is(err, knowledge.ErrInvalidRecord) || !strings.Contains(err.Error(), field) {
			t.Fatalf("CreateChunk(missing %s) error = %v", field, err)
		}
	}
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: valid, ReviewApproved: true})
	if err != nil {
		t.Fatalf("CreateChunk(valid high risk): %v", err)
	}
	content := ChunkContentFrom(created.Chunk)
	content.SourcePolicy = ""
	if _, err := service.UpdateChunk(ctx, UpdateChunkRequest{
		ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: content, ReviewApproved: true,
	}); !errors.Is(err, knowledge.ErrInvalidRecord) {
		t.Fatalf("UpdateChunk(remove policy) error = %v", err)
	}
	unchanged, err := service.Chunk(ctx, created.Chunk.ID)
	if err != nil || unchanged.Revision.Number != 1 || unchanged.SourcePolicy != valid.SourcePolicy {
		t.Fatalf("rejected policy removal changed chunk: %#v, %v", unchanged, err)
	}
}

func TestHighRiskDraftMayBeIncomplete(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	draft := testChunkCandidate()
	draft.Risk = []knowledge.RiskClass{knowledge.RiskClassMedical}
	draft.State = knowledge.ChunkStateDraft
	if _, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: draft, ReviewApproved: true}); err != nil {
		t.Fatalf("CreateChunk(incomplete research draft): %v", err)
	}
}
