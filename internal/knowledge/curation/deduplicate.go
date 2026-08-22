package curation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/lkarlslund/koder/internal/knowledge"
)

var ErrCandidateConflict = errors.New("knowledge curation candidates conflict")

// EntrySource returns authorized active and superseded entries for one chunk.
type EntrySource interface {
	EntriesForDeduplication(context.Context, knowledge.ChunkID) ([]knowledge.Entry, error)
}

// DeduplicatingSink removes exact semantic no-ops against canonical active/superseded
// entries and earlier drafts in the same atomic batch.
type DeduplicatingSink struct {
	source EntrySource
	next   CandidateSink
}

func NewDeduplicatingSink(source EntrySource, next CandidateSink) (*DeduplicatingSink, error) {
	if source == nil || next == nil {
		return nil, fmt.Errorf("%w: deduplication requires entry source and candidate sink", ErrUnavailable)
	}
	return &DeduplicatingSink{source: source, next: next}, nil
}

func (s *DeduplicatingSink) StoreCandidates(ctx context.Context, recordID knowledge.CurationRecordID, drafts []CandidateDraft) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	byChunk := make(map[knowledge.ChunkID][]knowledge.Entry)
	canonicalFingerprints := make(map[string]struct{})
	targets := make(map[knowledge.ChunkID]map[knowledge.EntryID]struct{})
	for _, draft := range drafts {
		if _, loaded := byChunk[draft.ChunkID]; loaded {
			continue
		}
		entries, err := s.source.EntriesForDeduplication(ctx, draft.ChunkID)
		if err != nil {
			return 0, fmt.Errorf("load entries for candidate deduplication: %w", err)
		}
		byChunk[draft.ChunkID] = slices.Clone(entries)
		targets[draft.ChunkID] = make(map[knowledge.EntryID]struct{}, len(entries))
		for _, entry := range entries {
			if entry.ChunkID != draft.ChunkID || (entry.State != knowledge.EntryStateActive && entry.State != knowledge.EntryStateSuperseded) {
				continue
			}
			targets[draft.ChunkID][entry.ID] = struct{}{}
			fingerprint, err := entryDraftFingerprint(entryDraftFromEntry(entry))
			if err != nil {
				return 0, err
			}
			canonicalFingerprints[string(draft.ChunkID)+"\x00"+fingerprint] = struct{}{}
		}
	}

	unique := make([]CandidateDraft, 0, len(drafts))
	seenDrafts := make(map[string]struct{}, len(drafts))
	for _, draft := range drafts {
		if draft.Action != CandidateActionCreateEntry {
			if _, exists := targets[draft.ChunkID][draft.TargetEntryID]; !exists {
				return 0, fmt.Errorf("%w: candidate target is not active or superseded in its chunk", knowledge.ErrInvalidRecord)
			}
		}
		fingerprint, err := entryDraftFingerprint(draft.Entry)
		if err != nil {
			return 0, err
		}
		key := string(draft.ChunkID) + "\x00" + fingerprint
		if _, exists := canonicalFingerprints[key]; exists {
			continue
		}
		if _, exists := seenDrafts[key]; exists {
			continue
		}
		seenDrafts[key] = struct{}{}
		unique = append(unique, cloneCandidateDraft(draft))
	}
	return s.next.StoreCandidates(ctx, recordID, unique)
}

func entryDraftFromEntry(entry knowledge.Entry) EntryDraft {
	return EntryDraft{
		Kind: entry.Kind, Title: entry.Title, Summary: entry.Summary, Body: entry.Body,
		Aliases: slices.Clone(entry.Aliases), Tags: slices.Clone(entry.Tags), Scope: entry.Scope,
		Applicability: cloneDraftApplicability(entry.Applicability), Risk: slices.Clone(entry.Risk), Confidence: entry.Confidence,
		ValidFrom: entry.ValidFrom, ValidUntil: entry.ValidUntil, ObservedAt: entry.ObservedAt,
		ReviewAfter: entry.ReviewAfter, PersonalOrigin: entry.PersonalOrigin,
	}
}

func entryDraftFingerprint(entry EntryDraft) (string, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("fingerprint curation candidate: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func cloneCandidateDraft(draft CandidateDraft) CandidateDraft {
	draft.Entry.Aliases = slices.Clone(draft.Entry.Aliases)
	draft.Entry.Tags = slices.Clone(draft.Entry.Tags)
	draft.Entry.Risk = slices.Clone(draft.Entry.Risk)
	draft.Entry.Applicability = cloneDraftApplicability(draft.Entry.Applicability)
	draft.SourceItemIDs = slices.Clone(draft.SourceItemIDs)
	draft.Classification.Findings = slices.Clone(draft.Classification.Findings)
	return draft
}

func cloneDraftApplicability(value knowledge.Applicability) knowledge.Applicability {
	value.OperatingSystems = slices.Clone(value.OperatingSystems)
	value.Architectures = slices.Clone(value.Architectures)
	value.Software = slices.Clone(value.Software)
	value.Locales = slices.Clone(value.Locales)
	value.Conditions = slices.Clone(value.Conditions)
	return value
}

// MemoryCandidateStore is an idempotent atomic sink suitable for tests and ephemeral
// curation. Persistent deployments can implement CandidateSink without changing extractors.
type MemoryCandidateStore struct {
	mu       sync.Mutex
	byRecord map[knowledge.CurationRecordID][]CandidateDraft
}

func NewMemoryCandidateStore() *MemoryCandidateStore {
	return &MemoryCandidateStore{byRecord: make(map[knowledge.CurationRecordID][]CandidateDraft)}
}

func (s *MemoryCandidateStore) StoreCandidates(ctx context.Context, recordID knowledge.CurationRecordID, drafts []CandidateDraft) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := cloneCandidateDrafts(drafts)
	if existing, exists := s.byRecord[recordID]; exists {
		if !reflect.DeepEqual(existing, cloned) {
			return 0, ErrCandidateConflict
		}
		return uint32(len(existing)), nil
	}
	s.byRecord[recordID] = cloned
	return uint32(len(cloned)), nil
}

func (s *MemoryCandidateStore) Candidates(_ context.Context, recordID knowledge.CurationRecordID) []CandidateDraft {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCandidateDrafts(s.byRecord[recordID])
}

func cloneCandidateDrafts(drafts []CandidateDraft) []CandidateDraft {
	cloned := make([]CandidateDraft, len(drafts))
	for index, draft := range drafts {
		cloned[index] = cloneCandidateDraft(draft)
	}
	return cloned
}
