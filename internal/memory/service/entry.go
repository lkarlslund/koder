package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var ErrParentChunkArchived = errors.New("parent memory chunk is archived")

type CreateEntryRequest struct {
	ChunkID        memory.ChunkID
	Entry          memory.Entry
	ReviewApproved bool
}

type CreateEntryResult struct {
	Entry          memory.Entry
	Classification memory.ClassificationResult
}

func (s *Service) CreateEntry(ctx context.Context, request CreateEntryRequest) (CreateEntryResult, error) {
	if err := ctx.Err(); err != nil {
		return CreateEntryResult{}, err
	}
	if request.ChunkID == "" {
		return CreateEntryResult{}, fmt.Errorf("%w: chunk ID is required", memory.ErrInvalidRecord)
	}
	candidate := request.Entry
	if hasServerOwnedEntryFields(candidate) {
		return CreateEntryResult{}, fmt.Errorf("%w: create entry contains server-owned identity, parent, revision, or timestamp fields", memory.ErrInvalidRecord)
	}
	classification, err := s.classifier.Classify(ctx, entryClassificationInput(candidate))
	if err != nil {
		return CreateEntryResult{}, fmt.Errorf("classify entry candidate: %w", err)
	}
	if err := requireClassificationApproval(classification, request.ReviewApproved); err != nil {
		return CreateEntryResult{Classification: classification}, err
	}
	candidate, err = memory.NormalizeEntry(candidate)
	if err != nil {
		return CreateEntryResult{}, err
	}
	if candidate.State == memory.EntryStateUnspecified {
		candidate.State = memory.EntryStateActive
	}
	if candidate.State == memory.EntryStateArchived || candidate.State == memory.EntryStateSuperseded {
		return CreateEntryResult{}, fmt.Errorf("%w: a new entry cannot start %q", memory.ErrInvalidRecord, candidate.State)
	}
	if candidate.Verification.Status == memory.VerificationStatusUnspecified {
		candidate.Verification = memory.Verification{Status: memory.VerificationStatusUnverified}
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return CreateEntryResult{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return CreateEntryResult{}, err
	}
	now := s.now().UTC().Round(0)
	candidate.ID = memory.EntryID(s.newID())
	candidate.ChunkID = request.ChunkID
	candidate.CreatedAt = now
	candidate.UpdatedAt = now
	candidate.Revision = memory.Revision{
		Number: 1, ID: memory.RevisionID(s.newID()), Actor: actor, CreatedAt: now,
	}
	result := CreateEntryResult{Classification: classification}
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryCreate, request.ChunkID)
		if err != nil {
			return err
		}
		if chunk.State == memory.ChunkStateArchived {
			return fmt.Errorf("%w: restore chunk %s before adding entries", ErrParentChunkArchived, request.ChunkID)
		}
		if candidate.Scope.Kind == memory.ScopeKindUnspecified {
			candidate.Scope = chunk.Scope
		}
		if err := applyPersonalEntryCreatePolicy(&candidate, chunk, classification); err != nil {
			return err
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
		return CreateEntryResult{}, fmt.Errorf("create memory entry: %w", err)
	}
	s.publishMutation(ctx, entryMutation(MutationCreated, result.Entry))
	return result, nil
}

func (s *Service) Entry(ctx context.Context, entryID memory.EntryID) (memory.Entry, error) {
	record, err := s.Get(ctx, memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryID)})
	if err != nil {
		return memory.Entry{}, err
	}
	if record.Entry == nil {
		return memory.Entry{}, fmt.Errorf("%w: entry projection is unavailable", memory.ErrInvalidRecord)
	}
	return *record.Entry, nil
}

func hasServerOwnedEntryFields(value memory.Entry) bool {
	return value.ID != "" || value.ChunkID != "" || value.Revision != (memory.Revision{}) ||
		!value.CreatedAt.IsZero() || !value.UpdatedAt.IsZero() || !value.LastUsedAt.IsZero() ||
		(value.Verification.Status != memory.VerificationStatusUnspecified && value.Verification.Status != memory.VerificationStatusUnverified) ||
		value.Verification.Method != "" || len(value.Verification.EvidenceIDs) != 0 || value.Verification.Actor != (memory.Actor{}) || !value.Verification.VerifiedAt.IsZero()
}

func entryClassificationInput(value memory.Entry) memory.ClassificationInput {
	fields := []memory.ClassificationField{
		{Name: "title", Value: value.Title}, {Name: "summary", Value: value.Summary},
		{Name: "body", Value: value.Body}, {Name: "scope.selector", Value: value.Scope.Selector},
	}
	for index, alias := range value.Aliases {
		fields = append(fields, memory.ClassificationField{Name: fmt.Sprintf("aliases[%d]", index), Value: alias})
	}
	for index, tag := range value.Tags {
		fields = append(fields, memory.ClassificationField{Name: fmt.Sprintf("tags[%d]", index), Value: tag})
	}
	for index, condition := range value.Applicability.Conditions {
		fields = append(fields, memory.ClassificationField{Name: fmt.Sprintf("applicability.conditions[%d]", index), Value: condition})
	}
	for index, operatingSystem := range value.Applicability.OperatingSystems {
		fields = append(fields, memory.ClassificationField{Name: fmt.Sprintf("applicability.operating_systems[%d]", index), Value: operatingSystem})
	}
	for index, architecture := range value.Applicability.Architectures {
		fields = append(fields, memory.ClassificationField{Name: fmt.Sprintf("applicability.architectures[%d]", index), Value: architecture})
	}
	for index, software := range value.Applicability.Software {
		fields = append(fields,
			memory.ClassificationField{Name: fmt.Sprintf("applicability.software[%d].name", index), Value: software.Name},
			memory.ClassificationField{Name: fmt.Sprintf("applicability.software[%d].version_range", index), Value: software.VersionRange},
		)
	}
	for index, locale := range value.Applicability.Locales {
		fields = append(fields, memory.ClassificationField{Name: fmt.Sprintf("applicability.locales[%d]", index), Value: locale})
	}
	return memory.ClassificationInput{Fields: fields, Risk: value.Risk}
}

func requireClassificationApproval(result memory.ClassificationResult, reviewApproved bool) error {
	switch result.Decision {
	case memory.ClassificationDecisionAllow:
		return nil
	case memory.ClassificationDecisionReview:
		if reviewApproved {
			return nil
		}
		return &ClassificationError{Result: result}
	case memory.ClassificationDecisionReject:
		return &ClassificationError{Result: result}
	default:
		return fmt.Errorf("classifier returned invalid decision %q", result.Decision)
	}
}

func validateEvidenceReferences(ctx context.Context, tx memoryStoreAPI.ReadTx, groups ...[]memory.EvidenceID) error {
	seen := make(map[memory.EvidenceID]struct{})
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
