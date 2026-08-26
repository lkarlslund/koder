package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

type CreateEvidenceRequest struct {
	Evidence       memory.Evidence
	ReviewApproved bool
}

type CreateEvidenceResult struct {
	Evidence       memory.Evidence
	Classification memory.ClassificationResult
	Created        bool
}

type EntryEvidenceRequest struct {
	EntryID memory.EntryID
	Limit   int
	Cursor  string
}

type EvidencePage struct {
	Evidence   []memory.Evidence
	NextCursor string
}

func (s *Service) CreateEvidence(ctx context.Context, request CreateEvidenceRequest) (CreateEvidenceResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateEvidenceResult{}, err
	}
	candidate := request.Evidence
	if candidate.ID != "" || candidate.Actor != (memory.Actor{}) || !candidate.CreatedAt.IsZero() {
		return CreateEvidenceResult{}, fmt.Errorf("%w: create evidence contains server-owned identity, actor, or creation time", memory.ErrInvalidRecord)
	}
	classification, err := s.classifier.Classify(ctx, evidenceClassificationInput(candidate))
	if err != nil {
		return CreateEvidenceResult{}, fmt.Errorf("classify evidence candidate: %w", err)
	}
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return CreateEvidenceResult{Classification: classification}, err
	}
	candidate.Source.ID, candidate.Source.ContentHash = memory.NormalizeEvidenceIdentity(candidate.Source.ID, candidate.Source.ContentHash)
	candidate.Source.URI = strings.TrimSpace(candidate.Source.URI)
	candidate.Source.Title = memory.NormalizeTitle(candidate.Source.Title)
	candidate.Source.Excerpt = strings.TrimSpace(candidate.Source.Excerpt)
	result := CreateEvidenceResult{Classification: classification}
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		existing, err := tx.EvidenceBySource(ctx, candidate.Source.ID, candidate.Source.ContentHash)
		switch {
		case err == nil:
			result.Evidence = existing
			return nil
		case !errors.Is(err, memoryStoreAPI.ErrNotFound):
			return err
		}
		actor, err := s.actor(ctx)
		if err != nil {
			return fmt.Errorf("resolve memory actor: %w", err)
		}
		if err := actor.Validate(); err != nil {
			return err
		}
		candidate.ID = memory.EvidenceID(s.newID())
		candidate.Actor = actor
		candidate.CreatedAt = s.now().UTC().Round(0)
		if err := candidate.Validate(); err != nil {
			return err
		}
		if err := tx.PutEvidence(ctx, candidate); err != nil {
			return err
		}
		result.Evidence = candidate
		result.Created = true
		return nil
	})
	if err != nil {
		return CreateEvidenceResult{}, fmt.Errorf("create memory evidence: %w", err)
	}
	if result.Created {
		s.publishMutation(ctx, evidenceMutation(MutationCreated, result.Evidence))
	}
	return result, nil
}

// EntryEvidence returns evidence through an authorized owning entry. Evidence is
// intentionally contextual because one immutable record may be cited by several chunks
// with different visibility policies.
func (s *Service) EntryEvidence(ctx context.Context, request EntryEvidenceRequest) (EvidencePage, error) {
	if err := ctx.Err(); err != nil {
		return EvidencePage{}, err
	}
	if request.EntryID == "" {
		return EvidencePage{}, fmt.Errorf("%w: entry ID is required", memory.ErrInvalidRecord)
	}
	if request.Limit <= 0 {
		request.Limit = 50
	}
	if request.Limit > 200 {
		return EvidencePage{}, fmt.Errorf("%w: evidence page limit must not exceed 200", memory.ErrInvalidRecord)
	}
	record, err := s.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(request.EntryID)})
	if err != nil {
		return EvidencePage{}, fmt.Errorf("authorize evidence owner: %w", err)
	}
	if record.Entry == nil {
		return EvidencePage{}, fmt.Errorf("%w: entry was not found", memoryStoreAPI.ErrNotFound)
	}
	ids := append(slices.Clone(record.Entry.EvidenceIDs), record.Entry.Verification.EvidenceIDs...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	binding := entryEvidenceCursorBinding(*record.Entry)
	start := 0
	if request.Cursor != "" {
		position, err := memoryStoreAPI.DecodeCursor(request.Cursor, binding)
		if err != nil {
			return EvidencePage{}, err
		}
		start = len(ids)
		for index, evidenceID := range ids {
			if string(evidenceID) > position.ObjectID {
				start = index
				break
			}
		}
	}
	end := min(start+request.Limit, len(ids))
	pageIDs := ids[start:end]
	result := EvidencePage{Evidence: make([]memory.Evidence, 0, len(pageIDs))}
	err = s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		for _, evidenceID := range pageIDs {
			evidence, err := tx.Evidence(ctx, evidenceID)
			if err != nil {
				return err
			}
			result.Evidence = append(result.Evidence, evidence)
		}
		return nil
	})
	if err != nil {
		return EvidencePage{}, fmt.Errorf("get entry evidence: %w", err)
	}
	if end < len(ids) && len(pageIDs) > 0 {
		last := string(pageIDs[len(pageIDs)-1])
		result.NextCursor, err = memoryStoreAPI.EncodeCursor(binding, memoryStoreAPI.CursorPosition{SortValue: last, ObjectID: last})
		if err != nil {
			return EvidencePage{}, err
		}
	}
	return result, nil
}

func entryEvidenceCursorBinding(entry memory.Entry) memoryStoreAPI.CursorBinding {
	digest := sha256.Sum256([]byte(entry.ID))
	return memoryStoreAPI.CursorBinding{
		Index: "entry-evidence", IndexGeneration: entry.Revision.Number,
		QueryFingerprint: hex.EncodeToString(digest[:]), SortField: "evidence_id",
	}
}

func evidenceClassificationInput(value memory.Evidence) memory.ClassificationInput {
	return memory.ClassificationInput{Fields: []memory.ClassificationField{
		{Name: "source.id", Value: value.Source.ID}, {Name: "source.uri", Value: value.Source.URI},
		{Name: "source.title", Value: value.Source.Title}, {Name: "source.excerpt", Value: value.Source.Excerpt},
	}}
}
