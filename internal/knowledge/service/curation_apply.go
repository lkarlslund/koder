package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type CuratedEntryAction string

const (
	CuratedEntryActionCreate CuratedEntryAction = "create_entry"
	CuratedEntryActionUpdate CuratedEntryAction = "update_entry"
)

type ApplyCuratedEntryRequest struct {
	RecordID         knowledge.CurationRecordID
	Source           knowledge.CompletedTurnRef
	SourceItemIDs    []string
	Action           CuratedEntryAction
	ChunkID          knowledge.ChunkID
	TargetEntryID    knowledge.EntryID
	ExpectedRevision uint64
	Content          EntryContent
	Reason           string
}

type ApplyCuratedEntryResult struct {
	Entry          knowledge.Entry
	Evidence       knowledge.Evidence
	Classification knowledge.ClassificationResult
	Created        bool
	Updated        bool
}

// ApplyCuratedEntry atomically commits chat-turn evidence and one low-risk verified entry
// create/update. Review-classified or risk-labelled drafts never enter this path.
func (s *Service) ApplyCuratedEntry(ctx context.Context, request ApplyCuratedEntryRequest) (ApplyCuratedEntryResult, error) {
	if err := ctx.Err(); err != nil {
		return ApplyCuratedEntryResult{}, err
	}
	if err := validateCuratedEntryRequest(request); err != nil {
		return ApplyCuratedEntryResult{}, err
	}
	candidate := request.Content.entry()
	classification, err := s.classifier.Classify(ctx, entryClassificationInput(candidate))
	if err != nil {
		return ApplyCuratedEntryResult{}, fmt.Errorf("classify curated entry: %w", err)
	}
	if classification.Decision != knowledge.ClassificationDecisionAllow || len(candidate.Risk) != 0 {
		return ApplyCuratedEntryResult{Classification: classification}, fmt.Errorf("%w: automatic curation accepts only low-risk allow-classified candidates", ErrReviewRequired)
	}
	candidate, err = knowledge.NormalizeEntry(candidate)
	if err != nil {
		return ApplyCuratedEntryResult{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return ApplyCuratedEntryResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return ApplyCuratedEntryResult{}, err
	}
	result := ApplyCuratedEntryResult{Classification: classification}
	var evidenceCreated bool
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		now := s.now().UTC().Round(0)
		evidence, created, err := s.curatedTurnEvidence(ctx, tx, actor, request, now)
		if err != nil {
			return err
		}
		result.Evidence, evidenceCreated = evidence, created
		switch request.Action {
		case CuratedEntryActionCreate:
			entry, err := s.createCuratedEntry(ctx, tx, actor, request, candidate, classification, evidence, now)
			if err != nil {
				return err
			}
			result.Entry, result.Created = entry, true
		case CuratedEntryActionUpdate:
			entry, err := s.updateCuratedEntry(ctx, tx, actor, request, candidate, classification, evidence, now)
			if err != nil {
				return err
			}
			result.Entry, result.Updated = entry, true
		default:
			return fmt.Errorf("%w: unsupported curated entry action", knowledge.ErrInvalidRecord)
		}
		return nil
	})
	if err != nil {
		return ApplyCuratedEntryResult{}, fmt.Errorf("apply curated knowledge entry: %w", err)
	}
	events := make([]MutationEvent, 0, 2)
	if evidenceCreated {
		events = append(events, evidenceMutation(MutationCreated, result.Evidence))
	}
	kind := MutationUpdated
	if result.Created {
		kind = MutationCreated
	}
	events = append(events, entryMutation(kind, result.Entry))
	s.publishMutations(ctx, events)
	return result, nil
}

func validateCuratedEntryRequest(request ApplyCuratedEntryRequest) error {
	if err := (knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(request.RecordID)}).Validate(); err != nil {
		return fmt.Errorf("%w: curation record ID is invalid", knowledge.ErrInvalidRecord)
	}
	if err := request.Source.Validate(); err != nil {
		return err
	}
	if err := (knowledge.CurationSignal{
		Kind: knowledge.CurationSignalKindUserCorrection, SourceItemIDs: request.SourceItemIDs, Confidence: 1,
	}).Validate(); err != nil {
		return err
	}
	if len(request.Content.EvidenceIDs) != 0 {
		return fmt.Errorf("%w: curated evidence is assigned by the service", knowledge.ErrInvalidRecord)
	}
	if err := (knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(request.ChunkID)}).Validate(); err != nil {
		return err
	}
	switch request.Action {
	case CuratedEntryActionCreate:
		if request.TargetEntryID != "" || request.ExpectedRevision != 0 {
			return fmt.Errorf("%w: create curation cannot include a target precondition", knowledge.ErrInvalidRecord)
		}
	case CuratedEntryActionUpdate:
		if err := (knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(request.TargetEntryID)}).Validate(); err != nil || request.ExpectedRevision == 0 {
			return fmt.Errorf("%w: update curation requires a target and expected revision", knowledge.ErrInvalidRecord)
		}
	default:
		return fmt.Errorf("%w: curated entry action is invalid", knowledge.ErrInvalidRecord)
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" || len(reason) > 1000 {
		return fmt.Errorf("%w: curation reason must contain 1 to 1000 bytes", knowledge.ErrInvalidRecord)
	}
	return nil
}

func (s *Service) curatedTurnEvidence(ctx context.Context, tx knowledgeStore.WriteTx, actor knowledge.Actor, request ApplyCuratedEntryRequest, now time.Time) (knowledge.Evidence, bool, error) {
	itemIDs := slices.Clone(request.SourceItemIDs)
	slices.Sort(itemIDs)
	digest := sha256.Sum256([]byte(strings.Join(itemIDs, "\n")))
	sourceID := "chat-turn:" + request.Source.SessionID + ":" + request.Source.ChatID + ":" + request.Source.AssistantItemID
	contentHash := "sha256:" + hex.EncodeToString(digest[:])
	if existing, err := tx.EvidenceBySource(ctx, sourceID, contentHash); err == nil {
		return existing, false, nil
	} else if err != nil && !errors.Is(err, knowledgeStore.ErrNotFound) {
		return knowledge.Evidence{}, false, err
	}
	evidence := knowledge.Evidence{
		ID: knowledge.EvidenceID(s.newID()), Type: knowledge.EvidenceTypeChatTurn, Quality: knowledge.EvidenceQualityPrimary,
		Source:     knowledge.Source{ID: sourceID, Title: "Completed Koder chat turn", ContentHash: contentHash},
		ObservedAt: request.Source.SealedAt, Actor: actor, CreatedAt: now,
	}
	if err := evidence.Validate(); err != nil {
		return knowledge.Evidence{}, false, err
	}
	if err := tx.PutEvidence(ctx, evidence); err != nil {
		return knowledge.Evidence{}, false, err
	}
	return evidence, true, nil
}

func (s *Service) createCuratedEntry(ctx context.Context, tx knowledgeStore.WriteTx, actor knowledge.Actor, request ApplyCuratedEntryRequest, candidate knowledge.Entry, classification knowledge.ClassificationResult, evidence knowledge.Evidence, now time.Time) (knowledge.Entry, error) {
	chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryCreate, request.ChunkID)
	if err != nil {
		return knowledge.Entry{}, err
	}
	if chunk.State == knowledge.ChunkStateArchived {
		return knowledge.Entry{}, fmt.Errorf("%w: restore chunk %s before adding entries", ErrParentChunkArchived, request.ChunkID)
	}
	if candidate.Scope.Kind == knowledge.ScopeKindUnspecified {
		candidate.Scope = chunk.Scope
	}
	candidate.ID = knowledge.EntryID(s.newID())
	candidate.ChunkID = request.ChunkID
	candidate.State = knowledge.EntryStateActive
	candidate.EvidenceIDs = appendUniqueEvidence(candidate.EvidenceIDs, evidence.ID)
	candidate.Verification = curatedVerification(actor, evidence.ID, now)
	candidate.CreatedAt, candidate.UpdatedAt = now, now
	candidate.Revision = knowledge.Revision{
		Number: 1, ID: knowledge.RevisionID(s.newID()), Actor: actor,
		Reason: curatedRevisionReason(request), CreatedAt: now,
	}
	if err := applyPersonalEntryCreatePolicy(&candidate, chunk, classification); err != nil {
		return knowledge.Entry{}, err
	}
	if err := validateEvidenceReferences(ctx, tx, candidate.EvidenceIDs, candidate.Verification.EvidenceIDs); err != nil {
		return knowledge.Entry{}, err
	}
	if err := validatePersonalEntryEvidence(ctx, tx, candidate); err != nil {
		return knowledge.Entry{}, err
	}
	if err := candidate.Validate(); err != nil {
		return knowledge.Entry{}, err
	}
	if err := tx.PutEntry(ctx, candidate, 0); err != nil {
		return knowledge.Entry{}, err
	}
	return candidate, nil
}

func (s *Service) updateCuratedEntry(ctx context.Context, tx knowledgeStore.WriteTx, actor knowledge.Actor, request ApplyCuratedEntryRequest, candidate knowledge.Entry, classification knowledge.ClassificationResult, evidence knowledge.Evidence, now time.Time) (knowledge.Entry, error) {
	current, err := tx.Entry(ctx, request.TargetEntryID)
	if err != nil {
		return knowledge.Entry{}, err
	}
	chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryUpdate, current.ChunkID)
	if err != nil {
		return knowledge.Entry{}, err
	}
	if current.ChunkID != request.ChunkID || current.Revision.Number != request.ExpectedRevision {
		return knowledge.Entry{}, fmt.Errorf("%w: curated entry target changed", knowledgeStore.ErrConflict)
	}
	if current.State != knowledge.EntryStateActive && current.State != knowledge.EntryStateDraft {
		return knowledge.Entry{}, fmt.Errorf("%w: entry %s is %q", ErrEntryNotEditable, current.ID, current.State)
	}
	if chunk.State == knowledge.ChunkStateArchived {
		return knowledge.Entry{}, fmt.Errorf("%w: restore chunk %s before editing entries", ErrParentChunkArchived, chunk.ID)
	}
	next := applyEntryContent(current, candidate)
	if err := applyPersonalEntryUpdatePolicy(&next, current, chunk, classification); err != nil {
		return knowledge.Entry{}, err
	}
	next.EvidenceIDs = appendUniqueEvidence(next.EvidenceIDs, evidence.ID)
	next.Verification = curatedVerification(actor, evidence.ID, now)
	if !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
		next.Verification.VerifiedAt = now
	}
	next.UpdatedAt = now
	next.Revision = knowledge.Revision{
		Number: current.Revision.Number + 1, ID: knowledge.RevisionID(s.newID()), Actor: actor,
		Reason: curatedRevisionReason(request), CreatedAt: now,
	}
	if err := validateEvidenceReferences(ctx, tx, next.EvidenceIDs, next.Verification.EvidenceIDs); err != nil {
		return knowledge.Entry{}, err
	}
	if err := validatePersonalEntryEvidence(ctx, tx, next); err != nil {
		return knowledge.Entry{}, err
	}
	if err := next.Validate(); err != nil {
		return knowledge.Entry{}, err
	}
	if err := tx.PutEntry(ctx, next, current.Revision.Number); err != nil {
		return knowledge.Entry{}, err
	}
	return next, nil
}

func curatedVerification(actor knowledge.Actor, evidenceID knowledge.EvidenceID, now time.Time) knowledge.Verification {
	return knowledge.Verification{
		Status: knowledge.VerificationStatusVerified, Method: "curation:completed_turn",
		EvidenceIDs: []knowledge.EvidenceID{evidenceID}, Actor: actor, VerifiedAt: now,
	}
}

func appendUniqueEvidence(values []knowledge.EvidenceID, id knowledge.EvidenceID) []knowledge.EvidenceID {
	values = slices.Clone(values)
	if !slices.Contains(values, id) {
		values = append(values, id)
	}
	return values
}

func curatedRevisionReason(request ApplyCuratedEntryRequest) string {
	return "curation " + string(request.RecordID) + ": " + strings.TrimSpace(request.Reason)
}
