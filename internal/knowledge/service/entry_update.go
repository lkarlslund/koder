package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var ErrEntryNotEditable = errors.New("knowledge entry is not editable in its lifecycle state")

type EntryContent struct {
	Kind           knowledge.EntryKind
	Title          string
	Summary        string
	Body           string
	Aliases        []string
	Tags           []string
	Scope          knowledge.Scope
	Applicability  knowledge.Applicability
	Risk           []knowledge.RiskClass
	Confidence     float32
	ValidFrom      time.Time
	ValidUntil     time.Time
	ObservedAt     time.Time
	ReviewAfter    time.Time
	EvidenceIDs    []knowledge.EvidenceID
	PersonalOrigin knowledge.PersonalOrigin
}

type UpdateEntryRequest struct {
	EntryID          knowledge.EntryID
	ExpectedRevision uint64
	Content          EntryContent
	Reason           string
	ReviewApproved   bool
}

type UpdateEntryResult struct {
	Entry          knowledge.Entry
	Classification knowledge.ClassificationResult
	Updated        bool
}

func EntryContentFrom(value knowledge.Entry) EntryContent {
	return EntryContent{
		Kind: value.Kind, Title: value.Title, Summary: value.Summary, Body: value.Body,
		Aliases: slices.Clone(value.Aliases), Tags: slices.Clone(value.Tags), Scope: value.Scope,
		Applicability: cloneApplicability(value.Applicability), Risk: slices.Clone(value.Risk), Confidence: value.Confidence,
		ValidFrom: value.ValidFrom, ValidUntil: value.ValidUntil, ObservedAt: value.ObservedAt, ReviewAfter: value.ReviewAfter,
		EvidenceIDs: slices.Clone(value.EvidenceIDs), PersonalOrigin: value.PersonalOrigin,
	}
}

func (s *Service) UpdateEntry(ctx context.Context, request UpdateEntryRequest) (UpdateEntryResult, error) {
	if err := ctx.Err(); err != nil {
		return UpdateEntryResult{}, err
	}
	if request.EntryID == "" || request.ExpectedRevision == 0 {
		return UpdateEntryResult{}, fmt.Errorf("%w: entry ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	candidate := request.Content.entry()
	classification, err := s.classifier.Classify(ctx, entryClassificationInput(candidate))
	if err != nil {
		return UpdateEntryResult{}, fmt.Errorf("classify entry update: %w", err)
	}
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return UpdateEntryResult{Classification: classification}, err
	}
	candidate, err = knowledge.NormalizeEntry(candidate)
	if err != nil {
		return UpdateEntryResult{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return UpdateEntryResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return UpdateEntryResult{}, err
	}
	result := UpdateEntryResult{Classification: classification}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryUpdate, current.ChunkID)
		if err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: entry %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.EntryID, request.ExpectedRevision, current.Revision.Number)
		}
		if current.State != knowledge.EntryStateActive && current.State != knowledge.EntryStateDraft {
			return fmt.Errorf("%w: entry %s is %q", ErrEntryNotEditable, current.ID, current.State)
		}
		if chunk.State == knowledge.ChunkStateArchived {
			return fmt.Errorf("%w: restore chunk %s before editing entries", ErrParentChunkArchived, chunk.ID)
		}
		next := applyEntryContent(current, candidate)
		if err := applyPersonalEntryUpdatePolicy(&next, current, classification); err != nil {
			return err
		}
		if entryContentEqual(next, current) {
			result.Entry = current
			return nil
		}
		if err := validateEvidenceReferences(ctx, tx, next.EvidenceIDs, next.Verification.EvidenceIDs); err != nil {
			return err
		}
		if err := validatePersonalEntryEvidence(ctx, tx, next); err != nil {
			return err
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		next.UpdatedAt = now
		next.Revision = knowledge.Revision{
			Number: current.Revision.Number + 1, ID: knowledge.RevisionID(s.newID()),
			Actor: actor, Reason: request.Reason, CreatedAt: now,
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, next, current.Revision.Number); err != nil {
			return err
		}
		result.Entry = next
		result.Updated = true
		return nil
	})
	if err != nil {
		return UpdateEntryResult{}, fmt.Errorf("update knowledge entry: %w", err)
	}
	return result, nil
}

func (c EntryContent) entry() knowledge.Entry {
	return knowledge.Entry{
		Kind: c.Kind, Title: c.Title, Summary: c.Summary, Body: c.Body,
		Aliases: c.Aliases, Tags: c.Tags, Scope: c.Scope, Applicability: c.Applicability,
		Risk: c.Risk, Confidence: c.Confidence, ValidFrom: c.ValidFrom, ValidUntil: c.ValidUntil,
		ObservedAt: c.ObservedAt, ReviewAfter: c.ReviewAfter, EvidenceIDs: c.EvidenceIDs, PersonalOrigin: c.PersonalOrigin,
	}
}

func applyEntryContent(current, normalized knowledge.Entry) knowledge.Entry {
	next := current
	next.Kind = normalized.Kind
	next.Title = normalized.Title
	next.Summary = normalized.Summary
	next.Body = normalized.Body
	next.Aliases = normalized.Aliases
	next.Tags = normalized.Tags
	next.Scope = normalized.Scope
	next.Applicability = normalized.Applicability
	next.Risk = normalized.Risk
	next.Confidence = normalized.Confidence
	next.ValidFrom = normalized.ValidFrom
	next.ValidUntil = normalized.ValidUntil
	next.ObservedAt = normalized.ObservedAt
	next.ReviewAfter = normalized.ReviewAfter
	next.EvidenceIDs = normalized.EvidenceIDs
	next.PersonalOrigin = normalized.PersonalOrigin
	return next
}

func entryContentEqual(left, right knowledge.Entry) bool {
	return left.Kind == right.Kind && left.Title == right.Title && left.Summary == right.Summary && left.Body == right.Body &&
		slices.Equal(left.Aliases, right.Aliases) && slices.Equal(left.Tags, right.Tags) && left.Scope == right.Scope &&
		applicabilityEqual(left.Applicability, right.Applicability) && slices.Equal(left.Risk, right.Risk) &&
		left.Confidence == right.Confidence && left.ValidFrom.Equal(right.ValidFrom) && left.ValidUntil.Equal(right.ValidUntil) &&
		left.ObservedAt.Equal(right.ObservedAt) && left.ReviewAfter.Equal(right.ReviewAfter) &&
		slices.Equal(left.EvidenceIDs, right.EvidenceIDs) && left.PersonalOrigin == right.PersonalOrigin && left.State == right.State
}

func cloneApplicability(value knowledge.Applicability) knowledge.Applicability {
	value.OperatingSystems = slices.Clone(value.OperatingSystems)
	value.Architectures = slices.Clone(value.Architectures)
	value.Software = slices.Clone(value.Software)
	value.Locales = slices.Clone(value.Locales)
	value.Conditions = slices.Clone(value.Conditions)
	return value
}

func applicabilityEqual(left, right knowledge.Applicability) bool {
	return slices.Equal(left.OperatingSystems, right.OperatingSystems) && slices.Equal(left.Architectures, right.Architectures) &&
		slices.Equal(left.Software, right.Software) && slices.Equal(left.Locales, right.Locales) && slices.Equal(left.Conditions, right.Conditions)
}
