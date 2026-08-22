package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var ErrParentChunkArchived = errors.New("parent knowledge chunk is archived")

type CreateEntryRequest struct {
	ChunkID        knowledge.ChunkID
	Entry          knowledge.Entry
	ReviewApproved bool
}

type CreateEntryResult struct {
	Entry          knowledge.Entry
	Classification knowledge.ClassificationResult
}

func (s *Service) CreateEntry(ctx context.Context, request CreateEntryRequest) (CreateEntryResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateEntryResult{}, err
	}
	if request.ChunkID == "" {
		return CreateEntryResult{}, fmt.Errorf("%w: chunk ID is required", knowledge.ErrInvalidRecord)
	}
	candidate := request.Entry
	if hasServerOwnedEntryFields(candidate) {
		return CreateEntryResult{}, fmt.Errorf("%w: create entry contains server-owned identity, parent, revision, or timestamp fields", knowledge.ErrInvalidRecord)
	}
	classification, err := s.classifier.Classify(ctx, entryClassificationInput(candidate))
	if err != nil {
		return CreateEntryResult{}, fmt.Errorf("classify entry candidate: %w", err)
	}
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return CreateEntryResult{Classification: classification}, err
	}
	candidate, err = knowledge.NormalizeEntry(candidate)
	if err != nil {
		return CreateEntryResult{}, err
	}
	if candidate.State == knowledge.EntryStateUnspecified {
		candidate.State = knowledge.EntryStateActive
	}
	if candidate.State == knowledge.EntryStateArchived || candidate.State == knowledge.EntryStateSuperseded {
		return CreateEntryResult{}, fmt.Errorf("%w: a new entry cannot start %q", knowledge.ErrInvalidRecord, candidate.State)
	}
	if candidate.Verification.Status == knowledge.VerificationStatusUnspecified {
		candidate.Verification = knowledge.Verification{Status: knowledge.VerificationStatusUnverified}
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return CreateEntryResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return CreateEntryResult{}, err
	}
	now := s.now().UTC().Round(0)
	candidate.ID = knowledge.EntryID(s.newID())
	candidate.ChunkID = request.ChunkID
	candidate.CreatedAt = now
	candidate.UpdatedAt = now
	candidate.Revision = knowledge.Revision{
		Number: 1, ID: knowledge.RevisionID(s.newID()), Actor: actor, CreatedAt: now,
	}
	result := CreateEntryResult{Classification: classification}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryCreate, request.ChunkID)
		if err != nil {
			return err
		}
		if chunk.State == knowledge.ChunkStateArchived {
			return fmt.Errorf("%w: restore chunk %s before adding entries", ErrParentChunkArchived, request.ChunkID)
		}
		if candidate.Scope.Kind == knowledge.ScopeKindUnspecified {
			candidate.Scope = chunk.Scope
		}
		if candidate.Scope.Kind == knowledge.ScopeKindPersonal && candidate.PersonalOrigin == knowledge.PersonalOriginInferred &&
			classification.Decision == knowledge.ClassificationDecisionReview {
			candidate.State = knowledge.EntryStateDraft
		}
		if err := validateEvidenceReferences(ctx, tx, candidate.EvidenceIDs, candidate.Verification.EvidenceIDs); err != nil {
			return err
		}
		if err := validatePersonalEntryEvidence(ctx, tx, candidate); err != nil {
			return err
		}
		if err := candidate.Validate(); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, candidate, 0); err != nil {
			return err
		}
		result.Entry = candidate
		return nil
	})
	if err != nil {
		return CreateEntryResult{}, fmt.Errorf("create knowledge entry: %w", err)
	}
	return result, nil
}

func (s *Service) Entry(ctx context.Context, entryID knowledge.EntryID) (knowledge.Entry, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Entry{}, err
	}
	var result knowledge.Entry
	if err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		var err error
		result, err = tx.Entry(ctx, entryID)
		return err
	}); err != nil {
		return knowledge.Entry{}, fmt.Errorf("get knowledge entry: %w", err)
	}
	return result, nil
}

func hasServerOwnedEntryFields(value knowledge.Entry) bool {
	return value.ID != "" || value.ChunkID != "" || value.Revision != (knowledge.Revision{}) ||
		!value.CreatedAt.IsZero() || !value.UpdatedAt.IsZero() || !value.LastUsedAt.IsZero() ||
		(value.Verification.Status != knowledge.VerificationStatusUnspecified && value.Verification.Status != knowledge.VerificationStatusUnverified) ||
		value.Verification.Method != "" || len(value.Verification.EvidenceIDs) != 0 || value.Verification.Actor != (knowledge.Actor{}) || !value.Verification.VerifiedAt.IsZero()
}

func entryClassificationInput(value knowledge.Entry) knowledge.ClassificationInput {
	fields := []knowledge.ClassificationField{
		{Name: "title", Value: value.Title}, {Name: "summary", Value: value.Summary},
		{Name: "body", Value: value.Body}, {Name: "scope.selector", Value: value.Scope.Selector},
	}
	for index, alias := range value.Aliases {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("aliases[%d]", index), Value: alias})
	}
	for index, tag := range value.Tags {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("tags[%d]", index), Value: tag})
	}
	for index, condition := range value.Applicability.Conditions {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("applicability.conditions[%d]", index), Value: condition})
	}
	for index, operatingSystem := range value.Applicability.OperatingSystems {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("applicability.operating_systems[%d]", index), Value: operatingSystem})
	}
	for index, architecture := range value.Applicability.Architectures {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("applicability.architectures[%d]", index), Value: architecture})
	}
	for index, software := range value.Applicability.Software {
		fields = append(fields,
			knowledge.ClassificationField{Name: fmt.Sprintf("applicability.software[%d].name", index), Value: software.Name},
			knowledge.ClassificationField{Name: fmt.Sprintf("applicability.software[%d].version_range", index), Value: software.VersionRange},
		)
	}
	for index, locale := range value.Applicability.Locales {
		fields = append(fields, knowledge.ClassificationField{Name: fmt.Sprintf("applicability.locales[%d]", index), Value: locale})
	}
	return knowledge.ClassificationInput{Fields: fields, Risk: value.Risk}
}

func requireClassificationApproval(result knowledge.ClassificationResult, reviewApproved bool) error {
	switch result.Decision {
	case knowledge.ClassificationDecisionAllow:
		return nil
	case knowledge.ClassificationDecisionReview:
		if reviewApproved {
			return nil
		}
		return &ClassificationError{Result: result}
	case knowledge.ClassificationDecisionReject:
		return &ClassificationError{Result: result}
	default:
		return fmt.Errorf("classifier returned invalid decision %q", result.Decision)
	}
}

func validateEvidenceReferences(ctx context.Context, tx knowledgeStore.ReadTx, groups ...[]knowledge.EvidenceID) error {
	seen := make(map[knowledge.EvidenceID]struct{})
	for _, values := range groups {
		for _, evidenceID := range values {
			if _, exists := seen[evidenceID]; exists {
				continue
			}
			seen[evidenceID] = struct{}{}
			if _, err := tx.Evidence(ctx, evidenceID); err != nil {
				return fmt.Errorf("resolve evidence %s: %w", evidenceID, err)
			}
		}
	}
	return nil
}
