package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/observability"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const (
	defaultLexicalSearchLimit = 25
	maxLexicalSearchLimit     = 100
	lexicalPostingPageLimit   = 1000
	entryCorpusPageLimit      = 200
)

type LexicalSearchRequest struct {
	Query             string
	ChunkIDs          []memory.ChunkID
	Scopes            []memory.Scope
	ScopeKinds        []memory.ScopeKind
	EntryStates       []memory.EntryState
	ChunkStates       []memory.ChunkState
	ValidAt           time.Time
	IncludeInvalid    bool
	IncludeSuperseded bool
	Limit             int
	Weights           LexicalFieldWeights
	GraphExpansion    *GraphExpansionOptions
	Cursor            string
}

type LexicalSearchResult struct {
	OperationID          string               `json:"operation_id"`
	Terms                []string             `json:"terms"`
	Matches              []LexicalSearchMatch `json:"matches"`
	GraphExpansion       *GraphExpansionStats `json:"graph_expansion,omitempty"`
	Warnings             []SearchWarning      `json:"warnings,omitempty"`
	Contradictions       []Contradiction      `json:"contradictions,omitempty"`
	AsOf                 time.Time            `json:"as_of"`
	NextCursor           string               `json:"next_cursor,omitempty"`
	CorpusDocumentCount  uint64               `json:"corpus_document_count"`
	MatchedDocumentCount uint64               `json:"matched_document_count"`
}

type LexicalSearchMatch struct {
	EntryID          memory.EntryID      `json:"entry_id"`
	Document         SearchDocument      `json:"document"`
	LexicalScore     float64             `json:"lexical_score"`
	Terms            []LexicalTermScore  `json:"terms,omitempty"`
	GraphConnections []GraphConnection   `json:"graph_connections,omitempty"`
	Rank             SearchRank          `json:"rank"`
	Reasons          []SearchMatchReason `json:"reasons"`
}

// SearchDocument is the bounded entry projection returned with a search hit.
// Full Markdown bodies remain behind an explicit get operation.
type SearchDocument struct {
	ChunkID        memory.ChunkID            `json:"chunk_id"`
	Kind           memory.EntryKind          `json:"kind"`
	Title          string                    `json:"title"`
	Summary        string                    `json:"summary,omitempty"`
	Scope          memory.Scope              `json:"scope"`
	State          memory.EntryState         `json:"state"`
	Verification   memory.VerificationStatus `json:"verification"`
	SupersededByID memory.EntryID            `json:"superseded_by_id,omitempty"`
	ValidFrom      time.Time                 `json:"valid_from,omitzero"`
	ValidUntil     time.Time                 `json:"valid_until,omitzero"`
	ReviewAfter    time.Time                 `json:"review_after,omitzero"`
}

// SearchLexical applies authorization, exact scope, lifecycle, and temporal validity
// filters to the corpus before deriving document frequencies and scoring matches.
func (s *Service) SearchLexical(ctx context.Context, request LexicalSearchRequest) (result LexicalSearchResult, err error) {
	operation := s.operationRecorder.Start(observability.OperationSearch, AuditIDFromContext(ctx))
	defer func() {
		result.OperationID = operation.ID()
		operation.Finish(operationOutcome(err, len(result.Matches) == 0), 1, uint64(len(result.Matches)))
	}()
	explicitValidAt := !request.IncludeInvalid && !request.ValidAt.IsZero()
	request, terms, err := s.normalizeLexicalSearchRequest(request)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return LexicalSearchResult{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return LexicalSearchResult{}, err
	}
	health, err := s.store.Health(ctx)
	if err != nil {
		return LexicalSearchResult{}, fmt.Errorf("read memory search index generation: %w", err)
	}
	binding, err := lexicalSearchCursorBinding(request, terms, actor, health.IndexGeneration, explicitValidAt)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	searchAsOf := request.ValidAt
	if searchAsOf.IsZero() {
		searchAsOf = s.now().UTC().Round(0)
	}
	var cursorPosition *rankedSearchCursorPosition
	if request.Cursor != "" {
		position, err := memoryStoreAPI.DecodeCursor(request.Cursor, binding)
		if err != nil {
			return LexicalSearchResult{}, err
		}
		decoded, err := decodeRankedSearchCursorPosition(position)
		if err != nil {
			return LexicalSearchResult{}, err
		}
		cursorPosition = &decoded
		searchAsOf = decoded.AsOf
		if !request.IncludeInvalid && !explicitValidAt {
			request.ValidAt = decoded.AsOf
		}
	}

	entries, err := s.listLexicalCorpusEntries(ctx, request)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	entries, err = s.authorizeLexicalCorpus(ctx, actor, request, entries)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	allowed := make(map[memory.EntryID]memory.Entry, len(entries))
	for _, entry := range entries {
		allowed[entry.ID] = entry
	}

	postings, err := s.lookupAllLexicalPostings(ctx, terms)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	postings = deduplicateLexicalPostings(postings)
	filtered := postings[:0]
	matched := make(map[memory.EntryID]struct{})
	for _, posting := range postings {
		if _, ok := allowed[posting.EntryID]; !ok {
			continue
		}
		filtered = append(filtered, posting)
		matched[posting.EntryID] = struct{}{}
	}
	frequencies := filteredDocumentFrequencies(filtered, terms)
	scored, err := ScoreLexicalMatches(filtered, uint64(len(entries)), frequencies, request.Weights)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	matches := make([]LexicalSearchMatch, 0, len(scored))
	for _, score := range scored {
		matches = append(matches, LexicalSearchMatch{
			EntryID: score.EntryID, LexicalScore: score.Score, Terms: score.Terms,
		})
	}
	var expansion *GraphExpansionStats
	if request.GraphExpansion != nil {
		matches, expansion, err = s.expandSearchGraph(ctx, matches, allowed, *request.GraphExpansion)
		if err != nil {
			return LexicalSearchResult{}, err
		}
	}
	matches, err = s.rankSearchMatches(ctx, matches, allowed, request.Scopes, searchAsOf)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	if cursorPosition != nil {
		matches = rankedSearchMatchesAfter(matches, *cursorPosition)
	}
	pageMatches := matches
	nextCursor := ""
	if len(pageMatches) > request.Limit {
		pageMatches = pageMatches[:request.Limit]
		position, err := encodeRankedSearchCursorPosition(searchAsOf, pageMatches[len(pageMatches)-1])
		if err != nil {
			return LexicalSearchResult{}, err
		}
		nextCursor, err = memoryStoreAPI.EncodeCursor(binding, position)
		if err != nil {
			return LexicalSearchResult{}, err
		}
	}
	pageMatches = addSearchMatchReasons(pageMatches)
	pageMatches = addSearchDocuments(pageMatches, allowed)
	warnings := searchWarnings(pageMatches, allowed, searchAsOf, expansion)
	contradictions, contradictionTruncated, err := s.searchContradictions(ctx, pageMatches, allowed)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	if contradictionTruncated {
		warnings = append(warnings, SearchWarning{Code: SearchWarningContradictionsTruncated})
	}
	return LexicalSearchResult{
		OperationID: operation.ID(),
		Terms:       slices.Clone(terms), Matches: pageMatches, GraphExpansion: expansion,
		Warnings: warnings, Contradictions: contradictions, AsOf: searchAsOf, NextCursor: nextCursor,
		CorpusDocumentCount: uint64(len(entries)), MatchedDocumentCount: uint64(len(matched)),
	}, nil
}

func addSearchDocuments(matches []LexicalSearchMatch, entries map[memory.EntryID]memory.Entry) []LexicalSearchMatch {
	result := slices.Clone(matches)
	for index, match := range result {
		entry := entries[match.EntryID]
		match.Document = SearchDocument{
			ChunkID: entry.ChunkID, Kind: entry.Kind, Title: entry.Title, Summary: entry.Summary,
			Scope: entry.Scope, State: entry.State, Verification: entry.Verification.Status,
			SupersededByID: entry.SupersededByID, ValidFrom: entry.ValidFrom,
			ValidUntil: entry.ValidUntil, ReviewAfter: entry.ReviewAfter,
		}
		result[index] = match
	}
	return result
}

func (s *Service) normalizeLexicalSearchRequest(request LexicalSearchRequest) (LexicalSearchRequest, []string, error) {
	weights := request.Weights
	if weights == (LexicalFieldWeights{}) {
		weights = DefaultLexicalFieldWeights()
	}
	if err := validateLexicalFieldWeights(weights); err != nil {
		return LexicalSearchRequest{}, nil, err
	}
	request.Weights = weights
	postingRequest, err := memoryStoreAPI.NormalizeLexicalPostingRequest(memoryStoreAPI.LexicalPostingRequest{Terms: []string{request.Query}})
	if err != nil {
		return LexicalSearchRequest{}, nil, err
	}
	if request.Limit < 0 || request.Limit > maxLexicalSearchLimit {
		return LexicalSearchRequest{}, nil, fmt.Errorf("%w: lexical search limit must be between 0 and %d", memory.ErrInvalidRecord, maxLexicalSearchLimit)
	}
	if request.Limit == 0 {
		request.Limit = defaultLexicalSearchLimit
	}
	request.ChunkIDs = slices.Clone(request.ChunkIDs)
	slices.Sort(request.ChunkIDs)
	request.ChunkIDs = slices.Compact(request.ChunkIDs)
	request.EntryStates = slices.Clone(request.EntryStates)
	if len(request.EntryStates) == 0 {
		request.EntryStates = []memory.EntryState{memory.EntryStateActive}
		if request.IncludeSuperseded {
			request.EntryStates = append(request.EntryStates, memory.EntryStateSuperseded)
		}
	}
	slices.Sort(request.EntryStates)
	request.EntryStates = slices.Compact(request.EntryStates)
	for _, state := range request.EntryStates {
		if state == memory.EntryStateUnspecified || !state.IsAEntryState() {
			return LexicalSearchRequest{}, nil, fmt.Errorf("%w: invalid lexical search entry state %q", memory.ErrInvalidRecord, state)
		}
	}
	if !request.IncludeSuperseded {
		request.EntryStates = slices.DeleteFunc(request.EntryStates, func(state memory.EntryState) bool {
			return state == memory.EntryStateSuperseded
		})
	}
	request.ChunkStates = slices.Clone(request.ChunkStates)
	if len(request.ChunkStates) == 0 {
		request.ChunkStates = []memory.ChunkState{memory.ChunkStateActive}
	}
	slices.Sort(request.ChunkStates)
	request.ChunkStates = slices.Compact(request.ChunkStates)
	for _, state := range request.ChunkStates {
		if state == memory.ChunkStateUnspecified || !state.IsAChunkState() {
			return LexicalSearchRequest{}, nil, fmt.Errorf("%w: invalid lexical search chunk state %q", memory.ErrInvalidRecord, state)
		}
	}
	request.Scopes = slices.Clone(request.Scopes)
	for _, scope := range request.Scopes {
		if err := scope.Validate(); err != nil {
			return LexicalSearchRequest{}, nil, err
		}
	}
	slices.SortFunc(request.Scopes, func(left, right memory.Scope) int {
		if left.Kind != right.Kind {
			return int(left.Kind) - int(right.Kind)
		}
		return strings.Compare(left.Selector, right.Selector)
	})
	request.Scopes = slices.Compact(request.Scopes)
	request.ScopeKinds = slices.Clone(request.ScopeKinds)
	slices.Sort(request.ScopeKinds)
	request.ScopeKinds = slices.Compact(request.ScopeKinds)
	for _, scopeKind := range request.ScopeKinds {
		if scopeKind == memory.ScopeKindUnspecified || !scopeKind.IsAScopeKind() {
			return LexicalSearchRequest{}, nil, fmt.Errorf("%w: invalid lexical search scope kind %q", memory.ErrInvalidRecord, scopeKind)
		}
	}
	if request.IncludeInvalid {
		request.ValidAt = time.Time{}
	} else if request.ValidAt.IsZero() {
		request.ValidAt = s.now().UTC().Round(0)
	} else {
		request.ValidAt = request.ValidAt.UTC().Round(0)
	}
	if request.GraphExpansion != nil {
		normalized, err := normalizeGraphExpansionOptions(*request.GraphExpansion)
		if err != nil {
			return LexicalSearchRequest{}, nil, err
		}
		request.GraphExpansion = &normalized
	}
	return request, postingRequest.Terms, nil
}

func (s *Service) listLexicalCorpusEntries(ctx context.Context, request LexicalSearchRequest) ([]memory.Entry, error) {
	if len(request.EntryStates) == 0 {
		return nil, nil
	}
	scopeKinds := slices.Clone(request.ScopeKinds)
	exactScopeKinds := make([]memory.ScopeKind, 0, len(request.Scopes))
	for _, scope := range request.Scopes {
		exactScopeKinds = append(exactScopeKinds, scope.Kind)
	}
	slices.Sort(exactScopeKinds)
	exactScopeKinds = slices.Compact(exactScopeKinds)
	if len(scopeKinds) == 0 {
		scopeKinds = exactScopeKinds
	} else if len(exactScopeKinds) != 0 {
		scopeKinds = slices.DeleteFunc(scopeKinds, func(kind memory.ScopeKind) bool {
			return !slices.Contains(exactScopeKinds, kind)
		})
		if len(scopeKinds) == 0 {
			return nil, nil
		}
	}
	filter := memoryStoreAPI.EntryFilter{
		ChunkIDs: request.ChunkIDs, States: request.EntryStates, ScopeKinds: scopeKinds, ValidAt: request.ValidAt,
	}
	entries := make([]memory.Entry, 0)
	if scanner, ok := s.store.(memoryStoreAPI.EntryScanner); ok {
		if err := scanner.ScanEntries(ctx, filter, func(entry memory.Entry) error {
			if len(request.Scopes) == 0 || slices.Contains(request.Scopes, entry.Scope) {
				entries = append(entries, entry)
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("scan lexical search corpus: %w", err)
		}
		return entries, nil
	}
	cursor := ""
	for {
		page, err := s.store.ListEntries(ctx, memoryStoreAPI.EntryListRequest{
			Filter: filter, Sort: memoryStoreAPI.EntrySortTitle, Limit: entryCorpusPageLimit, Cursor: cursor,
		})
		if err != nil {
			return nil, fmt.Errorf("list lexical search corpus: %w", err)
		}
		for _, entry := range page.Entries {
			if len(request.Scopes) == 0 || slices.Contains(request.Scopes, entry.Scope) {
				entries = append(entries, entry)
			}
		}
		if page.NextCursor == "" {
			return entries, nil
		}
		cursor = page.NextCursor
	}
}

func (s *Service) authorizeLexicalCorpus(ctx context.Context, actor memory.Actor, request LexicalSearchRequest, entries []memory.Entry) ([]memory.Entry, error) {
	chunkIDs := make([]memory.ChunkID, 0, len(entries))
	seen := make(map[memory.ChunkID]struct{}, len(entries))
	for _, entry := range entries {
		if _, ok := seen[entry.ChunkID]; !ok {
			seen[entry.ChunkID] = struct{}{}
			chunkIDs = append(chunkIDs, entry.ChunkID)
		}
	}
	slices.Sort(chunkIDs)
	chunks := make(map[memory.ChunkID]memory.Chunk, len(chunkIDs))
	if err := s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		for _, chunkID := range chunkIDs {
			chunk, err := tx.Chunk(ctx, chunkID)
			if errors.Is(err, memoryStoreAPI.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			chunks[chunkID] = chunk
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("load lexical search chunks: %w", err)
	}
	allowedChunks := make(map[memory.ChunkID]struct{}, len(chunks))
	for _, chunkID := range chunkIDs {
		chunk, ok := chunks[chunkID]
		if !ok || !slices.Contains(request.ChunkStates, chunk.State) {
			continue
		}
		if err := s.chunkPolicy.AuthorizeChunk(ctx, actor, ChunkPolicySearch, chunk); err != nil {
			if contextError := ctx.Err(); contextError != nil {
				return nil, contextError
			}
			continue
		}
		allowedChunks[chunkID] = struct{}{}
	}
	allowedEntries := entries[:0]
	for _, entry := range entries {
		if _, ok := allowedChunks[entry.ChunkID]; ok {
			allowedEntries = append(allowedEntries, entry)
		}
	}
	return allowedEntries, nil
}

func (s *Service) lookupAllLexicalPostings(ctx context.Context, terms []string) ([]memoryStoreAPI.LexicalPosting, error) {
	postings := make([]memoryStoreAPI.LexicalPosting, 0)
	cursor := ""
	for {
		page, err := s.store.LookupLexicalPostings(ctx, memoryStoreAPI.LexicalPostingRequest{
			Terms: terms, Limit: lexicalPostingPageLimit, Cursor: cursor,
		})
		if err != nil {
			return nil, fmt.Errorf("lookup lexical search postings: %w", err)
		}
		postings = append(postings, page.Postings...)
		if page.NextCursor == "" {
			return postings, nil
		}
		cursor = page.NextCursor
	}
}

func filteredDocumentFrequencies(postings []memoryStoreAPI.LexicalPosting, terms []string) []memoryStoreAPI.LexicalDocumentFrequency {
	counts := make(map[string]uint64, len(terms))
	for _, posting := range postings {
		counts[posting.Term]++
	}
	result := make([]memoryStoreAPI.LexicalDocumentFrequency, 0, len(terms))
	for _, term := range terms {
		result = append(result, memoryStoreAPI.LexicalDocumentFrequency{Term: term, Count: counts[term]})
	}
	return result
}
