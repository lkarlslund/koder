package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// ChunkContent is the complete editable portion of a chunk. Lifecycle, identities,
// revisions, timestamps, derived counts, and usage statistics are intentionally absent.
type ChunkContent struct {
	Title           string
	Description     string
	Aliases         []string
	Tags            []string
	Kind            knowledge.ChunkKind
	Scope           knowledge.Scope
	Visibility      knowledge.Visibility
	SharedWith      []knowledge.PrincipalRef
	Language        string
	Locale          string
	Domain          string
	Risk            []knowledge.RiskClass
	Publisher       knowledge.Publisher
	License         string
	SourcePolicy    string
	DependencyIDs   []knowledge.ChunkID
	MinKoderVersion string
	ReviewAfter     time.Time
}

type UpdateChunkRequest struct {
	ChunkID          knowledge.ChunkID
	ExpectedRevision uint64
	Content          ChunkContent
	Reason           string
	ReviewApproved   bool
}

type UpdateChunkResult struct {
	Chunk          knowledge.Chunk
	Classification knowledge.ClassificationResult
	Updated        bool
}

func ChunkContentFrom(value knowledge.Chunk) ChunkContent {
	return ChunkContent{
		Title: value.Title, Description: value.Description,
		Kind: value.Kind, Scope: value.Scope, Visibility: value.Visibility,
		Language: value.Language, Locale: value.Locale, Domain: value.Domain,
		Publisher: value.Publisher, License: value.License, SourcePolicy: value.SourcePolicy,
		DependencyIDs: slices.Clone(value.DependencyIDs), MinKoderVersion: value.MinKoderVersion,
		Aliases: slices.Clone(value.Aliases), Tags: slices.Clone(value.Tags),
		SharedWith: slices.Clone(value.SharedWith), Risk: slices.Clone(value.Risk), ReviewAfter: value.ReviewAfter,
	}
}

func (s *Service) UpdateChunk(ctx context.Context, request UpdateChunkRequest) (UpdateChunkResult, error) {
	if err := ctx.Err(); err != nil {
		return UpdateChunkResult{}, err
	}
	if request.ChunkID == "" || request.ExpectedRevision == 0 {
		return UpdateChunkResult{}, fmt.Errorf("%w: chunk ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	candidate := request.Content.chunk()
	classification, err := s.classifier.Classify(ctx, chunkClassificationInput(candidate))
	if err != nil {
		return UpdateChunkResult{}, fmt.Errorf("classify chunk update: %w", err)
	}
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return UpdateChunkResult{Classification: classification}, err
	}
	candidate, err = knowledge.NormalizeChunk(candidate)
	if err != nil {
		return UpdateChunkResult{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return UpdateChunkResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return UpdateChunkResult{}, err
	}

	result := UpdateChunkResult{Classification: classification}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Chunk(ctx, request.ChunkID)
		if err != nil {
			return err
		}
		if err := s.authorizeChunk(ctx, actor, ChunkPolicyUpdate, current); err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: chunk %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.ChunkID, request.ExpectedRevision, current.Revision.Number)
		}
		next := applyChunkContent(current, candidate)
		if err := validatePersonalMeMutation(current, next); err != nil {
			return err
		}
		if err := validateHighRiskChunkPolicy(next); err != nil {
			return err
		}
		if err := s.authorizeChunk(ctx, actor, ChunkPolicyUpdate, next); err != nil {
			return err
		}
		if chunkContentEqual(next, current) {
			result.Chunk = current
			return nil
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		next.UpdatedAt = now
		next.Revision = knowledge.Revision{
			Number: request.ExpectedRevision + 1,
			ID:     knowledge.RevisionID(s.newID()), Actor: actor, Reason: request.Reason, CreatedAt: now,
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if err := tx.PutChunk(ctx, next, request.ExpectedRevision); err != nil {
			return err
		}
		result.Chunk = next
		result.Updated = true
		return nil
	})
	if err != nil {
		return UpdateChunkResult{}, fmt.Errorf("update knowledge chunk: %w", err)
	}
	if result.Updated {
		s.publishMutation(ctx, chunkMutation(MutationUpdated, result.Chunk))
	}
	return result, nil
}

func chunkContentEqual(left, right knowledge.Chunk) bool {
	return left.Title == right.Title && left.Description == right.Description &&
		slices.Equal(left.Aliases, right.Aliases) && slices.Equal(left.Tags, right.Tags) &&
		left.Kind == right.Kind && left.Scope == right.Scope && left.Visibility == right.Visibility &&
		slices.Equal(left.SharedWith, right.SharedWith) && left.Language == right.Language &&
		left.Locale == right.Locale && left.Domain == right.Domain && slices.Equal(left.Risk, right.Risk) &&
		left.Publisher == right.Publisher && left.License == right.License && left.SourcePolicy == right.SourcePolicy &&
		slices.Equal(left.DependencyIDs, right.DependencyIDs) && left.MinKoderVersion == right.MinKoderVersion &&
		left.ReviewAfter.Equal(right.ReviewAfter)
}

func (c ChunkContent) chunk() knowledge.Chunk {
	return knowledge.Chunk{
		Title: c.Title, Description: c.Description, Aliases: c.Aliases, Tags: c.Tags,
		Kind: c.Kind, Scope: c.Scope, Visibility: c.Visibility, SharedWith: c.SharedWith,
		Language: c.Language, Locale: c.Locale, Domain: c.Domain, Risk: c.Risk,
		Publisher: c.Publisher, License: c.License, SourcePolicy: c.SourcePolicy,
		DependencyIDs: c.DependencyIDs, MinKoderVersion: c.MinKoderVersion, ReviewAfter: c.ReviewAfter,
	}
}

func applyChunkContent(current, normalized knowledge.Chunk) knowledge.Chunk {
	next := current
	next.Title = normalized.Title
	next.Description = normalized.Description
	next.Aliases = normalized.Aliases
	next.Tags = normalized.Tags
	next.Kind = normalized.Kind
	next.Scope = normalized.Scope
	next.Visibility = normalized.Visibility
	next.SharedWith = normalized.SharedWith
	next.Language = normalized.Language
	next.Locale = normalized.Locale
	next.Domain = normalized.Domain
	next.Risk = normalized.Risk
	next.Publisher = normalized.Publisher
	next.License = normalized.License
	next.SourcePolicy = normalized.SourcePolicy
	next.DependencyIDs = normalized.DependencyIDs
	next.MinKoderVersion = normalized.MinKoderVersion
	next.ReviewAfter = normalized.ReviewAfter
	return next
}
