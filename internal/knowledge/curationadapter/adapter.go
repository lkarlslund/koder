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
