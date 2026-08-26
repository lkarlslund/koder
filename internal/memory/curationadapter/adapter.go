// Package curationadapter connects provider-neutral curation contracts to the Memory
// service without making either package depend on chat/model implementations.
package curationadapter

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/curation"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const defaultDeduplicationEntryLimit = 10_000

type ServiceEntrySource struct {
	Service    *memoryService.Service
	EntryLimit int
}

func (s ServiceEntrySource) EntriesForDeduplication(ctx context.Context, chunkID memory.ChunkID) ([]memory.Entry, error) {
	if s.Service == nil {
		return nil, curation.ErrUnavailable
	}
	limit := s.EntryLimit
	if limit <= 0 {
		limit = defaultDeduplicationEntryLimit
	}
	entries := make([]memory.Entry, 0, min(limit, 200))
	cursor := ""
	for {
		page, err := s.Service.ListEntries(ctx, memoryStoreAPI.EntryListRequest{
			Filter: memoryStoreAPI.EntryFilter{
				ChunkIDs: []memory.ChunkID{chunkID},
				States:   []memory.EntryState{memory.EntryStateActive, memory.EntryStateSuperseded},
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
			return nil, fmt.Errorf("%w: candidate deduplication exceeds %d entries in one chunk", memory.ErrInvalidRecord, limit)
		}
		cursor = page.NextCursor
	}
}

type LowRiskApplier struct {
	Service *memoryService.Service
}

func (a LowRiskApplier) Apply(ctx context.Context, record memory.CurationRecord, draft curation.CandidateDraft) (memoryService.ApplyCuratedEntryResult, error) {
	if a.Service == nil {
		return memoryService.ApplyCuratedEntryResult{}, curation.ErrUnavailable
	}
	if record.State != memory.CurationStateCandidatesReady {
		return memoryService.ApplyCuratedEntryResult{}, fmt.Errorf("%w: curation record is not ready", memory.ErrInvalidRecord)
	}
	if draft.Route != curation.CandidateRouteAutomatic {
		return memoryService.ApplyCuratedEntryResult{}, fmt.Errorf("%w: candidate is pending review", memoryService.ErrReviewRequired)
	}
	action := memoryService.CuratedEntryAction("")
	switch draft.Action {
	case curation.CandidateActionCreateEntry:
		action = memoryService.CuratedEntryActionCreate
	case curation.CandidateActionUpdateEntry:
		action = memoryService.CuratedEntryActionUpdate
	default:
		return memoryService.ApplyCuratedEntryResult{}, fmt.Errorf("%w: candidate action requires review", memoryService.ErrReviewRequired)
	}
	entry := draft.Entry
	return a.Service.ApplyCuratedEntry(ctx, memoryService.ApplyCuratedEntryRequest{
		RecordID: record.ID, Source: record.Source, SourceItemIDs: draft.SourceItemIDs,
		Action: action, ChunkID: draft.ChunkID, TargetEntryID: draft.TargetEntryID,
		ExpectedRevision: draft.TargetRevision, Reason: draft.Reason,
		Content: memoryService.EntryContent{
			Kind: entry.Kind, Title: entry.Title, Summary: entry.Summary, Body: entry.Body,
			Aliases: entry.Aliases, Tags: entry.Tags, Scope: entry.Scope, Applicability: entry.Applicability,
			Risk: entry.Risk, Confidence: entry.Confidence, ValidFrom: entry.ValidFrom,
			ValidUntil: entry.ValidUntil, ObservedAt: entry.ObservedAt, ReviewAfter: entry.ReviewAfter,
			PersonalOrigin: entry.PersonalOrigin,
		},
	})
}

func (a LowRiskApplier) ApplyCandidate(ctx context.Context, record memory.CurationRecord, draft curation.CandidateDraft) (curation.ApplyReceipt, error) {
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
// Approval relaxes the automatic low-risk gate, but all canonical Memory policy,
// validation, evidence, and optimistic-revision checks remain in force.
type ReviewedApplier struct {
	Service *memoryService.Service
}

func (a ReviewedApplier) ApplyCandidate(ctx context.Context, record memory.CurationRecord, draft curation.CandidateDraft) (curation.ApplyReceipt, error) {
	if a.Service == nil {
		return curation.ApplyReceipt{}, curation.ErrUnavailable
	}
	if record.State != memory.CurationStateCandidatesReady {
		return curation.ApplyReceipt{}, fmt.Errorf("%w: curation record is not ready", memory.ErrInvalidRecord)
	}
	action := memoryService.CuratedEntryAction("")
	switch draft.Action {
	case curation.CandidateActionCreateEntry:
		action = memoryService.CuratedEntryActionCreate
	case curation.CandidateActionUpdateEntry:
		action = memoryService.CuratedEntryActionUpdate
	default:
		return curation.ApplyReceipt{}, fmt.Errorf("%w: reviewed candidate action is not supported", memory.ErrInvalidRecord)
	}
	entry := draft.Entry
	result, err := a.Service.ApplyCuratedEntry(ctx, memoryService.ApplyCuratedEntryRequest{
		RecordID: record.ID, Source: record.Source, SourceItemIDs: draft.SourceItemIDs,
		Action: action, ChunkID: draft.ChunkID, TargetEntryID: draft.TargetEntryID,
		ExpectedRevision: draft.TargetRevision, Reason: draft.Reason, ReviewApproved: true,
		Content: memoryService.EntryContent{
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

func undoCandidate(ctx context.Context, service *memoryService.Service, candidate curation.StoredCandidate) error {
	if service == nil {
		return curation.ErrUnavailable
	}
	receipt := candidate.Receipt
	if receipt.EntryID == "" || receipt.AfterRevision == 0 {
		return fmt.Errorf("%w: candidate has no apply receipt", memory.ErrInvalidRecord)
	}
	reason := "undo curation candidate " + string(candidate.ID)
	if receipt.Created {
		_, err := service.ArchiveEntry(ctx, memoryService.EntryLifecycleRequest{
			EntryID: receipt.EntryID, ExpectedRevision: receipt.AfterRevision, Reason: reason,
		})
		return err
	}
	_, err := service.RestoreEntryRevision(ctx, memoryService.RestoreEntryRevisionRequest{
		EntryID: receipt.EntryID, ExpectedRevision: receipt.AfterRevision,
		SourceRevision: receipt.BeforeRevision, Reason: reason,
	})
	return err
}
