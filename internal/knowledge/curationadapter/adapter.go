// Package curationadapter connects provider-neutral curation contracts to the Knowledge
// service without making either package depend on chat/model implementations.
package curationadapter

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/curation"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const defaultDeduplicationEntryLimit = 10_000

type ServiceEntrySource struct {
	Service    *knowledgeService.Service
	EntryLimit int
}

func (s ServiceEntrySource) EntriesForDeduplication(ctx context.Context, chunkID knowledge.ChunkID) ([]knowledge.Entry, error) {
	if s.Service == nil {
		return nil, curation.ErrUnavailable
	}
	limit := s.EntryLimit
	if limit <= 0 {
		limit = defaultDeduplicationEntryLimit
	}
	entries := make([]knowledge.Entry, 0, min(limit, 200))
	cursor := ""
	for {
		page, err := s.Service.ListEntries(ctx, knowledgeStore.EntryListRequest{
			Filter: knowledgeStore.EntryFilter{
				ChunkIDs: []knowledge.ChunkID{chunkID},
				States:   []knowledge.EntryState{knowledge.EntryStateActive, knowledge.EntryStateSuperseded},
			},
			Limit: min(200, limit-len(entries)), Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		entries = append(entries, page.Entries...)
		if page.NextCursor == "" {
			return entries, nil
		}
		if len(entries) >= limit {
			return nil, fmt.Errorf("%w: candidate deduplication exceeds %d entries in one chunk", knowledge.ErrInvalidRecord, limit)
		}
		cursor = page.NextCursor
	}
}

type LowRiskApplier struct {
	Service *knowledgeService.Service
}

func (a LowRiskApplier) Apply(ctx context.Context, record knowledge.CurationRecord, draft curation.CandidateDraft) (knowledgeService.ApplyCuratedEntryResult, error) {
	if a.Service == nil {
		return knowledgeService.ApplyCuratedEntryResult{}, curation.ErrUnavailable
	}
	if record.State != knowledge.CurationStateCandidatesReady {
		return knowledgeService.ApplyCuratedEntryResult{}, fmt.Errorf("%w: curation record is not ready", knowledge.ErrInvalidRecord)
	}
	if draft.Route != curation.CandidateRouteAutomatic {
		return knowledgeService.ApplyCuratedEntryResult{}, fmt.Errorf("%w: candidate is pending review", knowledgeService.ErrReviewRequired)
	}
	action := knowledgeService.CuratedEntryAction("")
	switch draft.Action {
	case curation.CandidateActionCreateEntry:
		action = knowledgeService.CuratedEntryActionCreate
	case curation.CandidateActionUpdateEntry:
		action = knowledgeService.CuratedEntryActionUpdate
	default:
		return knowledgeService.ApplyCuratedEntryResult{}, fmt.Errorf("%w: candidate action requires review", knowledgeService.ErrReviewRequired)
	}
	entry := draft.Entry
	return a.Service.ApplyCuratedEntry(ctx, knowledgeService.ApplyCuratedEntryRequest{
		RecordID: record.ID, Source: record.Source, SourceItemIDs: draft.SourceItemIDs,
		Action: action, ChunkID: draft.ChunkID, TargetEntryID: draft.TargetEntryID,
		ExpectedRevision: draft.TargetRevision, Reason: draft.Reason,
		Content: knowledgeService.EntryContent{
			Kind: entry.Kind, Title: entry.Title, Summary: entry.Summary, Body: entry.Body,
			Aliases: entry.Aliases, Tags: entry.Tags, Scope: entry.Scope, Applicability: entry.Applicability,
			Risk: entry.Risk, Confidence: entry.Confidence, ValidFrom: entry.ValidFrom,
			ValidUntil: entry.ValidUntil, ObservedAt: entry.ObservedAt, ReviewAfter: entry.ReviewAfter,
			PersonalOrigin: entry.PersonalOrigin,
		},
	})
}

func (a LowRiskApplier) ApplyCandidate(ctx context.Context, record knowledge.CurationRecord, draft curation.CandidateDraft) (curation.ApplyReceipt, error) {
	result, err := a.Apply(ctx, record, draft)
	if err != nil {
		return curation.ApplyReceipt{}, err
	}
	return curation.ApplyReceipt{
		EntryID: result.Entry.ID, BeforeRevision: draft.TargetRevision,
		AfterRevision: result.Entry.Revision.Number, Created: result.Created,
	}, nil
}

func (a LowRiskApplier) UndoCandidate(ctx context.Context, candidate curation.StoredCandidate) error {
	return undoCandidate(ctx, a.Service, candidate)
}

// ReviewedApplier applies a candidate only after the curator explicitly accepts it.
// Approval relaxes the automatic low-risk gate, but all canonical Knowledge policy,
// validation, evidence, and optimistic-revision checks remain in force.
type ReviewedApplier struct {
	Service *knowledgeService.Service
}

func (a ReviewedApplier) ApplyCandidate(ctx context.Context, record knowledge.CurationRecord, draft curation.CandidateDraft) (curation.ApplyReceipt, error) {
	if a.Service == nil {
		return curation.ApplyReceipt{}, curation.ErrUnavailable
	}
	if record.State != knowledge.CurationStateCandidatesReady {
		return curation.ApplyReceipt{}, fmt.Errorf("%w: curation record is not ready", knowledge.ErrInvalidRecord)
	}
	action := knowledgeService.CuratedEntryAction("")
	switch draft.Action {
	case curation.CandidateActionCreateEntry:
		action = knowledgeService.CuratedEntryActionCreate
	case curation.CandidateActionUpdateEntry:
		action = knowledgeService.CuratedEntryActionUpdate
	default:
		return curation.ApplyReceipt{}, fmt.Errorf("%w: reviewed candidate action is not supported", knowledge.ErrInvalidRecord)
	}
	entry := draft.Entry
	result, err := a.Service.ApplyCuratedEntry(ctx, knowledgeService.ApplyCuratedEntryRequest{
		RecordID: record.ID, Source: record.Source, SourceItemIDs: draft.SourceItemIDs,
		Action: action, ChunkID: draft.ChunkID, TargetEntryID: draft.TargetEntryID,
		ExpectedRevision: draft.TargetRevision, Reason: draft.Reason, ReviewApproved: true,
		Content: knowledgeService.EntryContent{
			Kind: entry.Kind, Title: entry.Title, Summary: entry.Summary, Body: entry.Body,
			Aliases: entry.Aliases, Tags: entry.Tags, Scope: entry.Scope, Applicability: entry.Applicability,
			Risk: entry.Risk, Confidence: entry.Confidence, ValidFrom: entry.ValidFrom,
			ValidUntil: entry.ValidUntil, ObservedAt: entry.ObservedAt, ReviewAfter: entry.ReviewAfter,
			PersonalOrigin: entry.PersonalOrigin,
		},
	})
	if err != nil {
		return curation.ApplyReceipt{}, err
	}
	return curation.ApplyReceipt{
		EntryID: result.Entry.ID, BeforeRevision: draft.TargetRevision,
		AfterRevision: result.Entry.Revision.Number, Created: result.Created,
	}, nil
}

func (a ReviewedApplier) UndoCandidate(ctx context.Context, candidate curation.StoredCandidate) error {
	return undoCandidate(ctx, a.Service, candidate)
}

func undoCandidate(ctx context.Context, service *knowledgeService.Service, candidate curation.StoredCandidate) error {
	if service == nil {
		return curation.ErrUnavailable
	}
	receipt := candidate.Receipt
	if receipt.EntryID == "" || receipt.AfterRevision == 0 {
		return fmt.Errorf("%w: candidate has no apply receipt", knowledge.ErrInvalidRecord)
	}
	reason := "undo curation candidate " + string(candidate.ID)
	if receipt.Created {
		_, err := service.ArchiveEntry(ctx, knowledgeService.EntryLifecycleRequest{
			EntryID: receipt.EntryID, ExpectedRevision: receipt.AfterRevision, Reason: reason,
		})
		return err
	}
	_, err := service.RestoreEntryRevision(ctx, knowledgeService.RestoreEntryRevisionRequest{
		EntryID: receipt.EntryID, ExpectedRevision: receipt.AfterRevision,
		SourceRevision: receipt.BeforeRevision, Reason: reason,
	})
	return err
}
