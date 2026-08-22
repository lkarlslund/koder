package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	defaultLexicalSearchLimit = 25
	maxLexicalSearchLimit     = 100
	lexicalPostingPageLimit   = 1000
	entryCorpusPageLimit      = 200
)

type LexicalSearchRequest struct {
	Query             string
	ChunkIDs          []knowledge.ChunkID
	Scopes            []knowledge.Scope
	EntryStates       []knowledge.EntryState
	ChunkStates       []knowledge.ChunkState
	ValidAt           time.Time
	IncludeInvalid    bool
	IncludeSuperseded bool
	Limit             int
	Weights           LexicalFieldWeights
	GraphExpansion    *GraphExpansionOptions
}

type LexicalSearchResult struct {
	Terms                []string             `json:"terms"`
	Matches              []LexicalSearchMatch `json:"matches"`
	GraphExpansion       *GraphExpansionStats `json:"graph_expansion,omitempty"`
	CorpusDocumentCount  uint64               `json:"corpus_document_count"`
	MatchedDocumentCount uint64               `json:"matched_document_count"`
}

type LexicalSearchMatch struct {
	EntryID          knowledge.EntryID  `json:"entry_id"`
	LexicalScore     float64            `json:"lexical_score"`
	Terms            []LexicalTermScore `json:"terms,omitempty"`
	GraphConnections []GraphConnection  `json:"graph_connections,omitempty"`
	Rank             SearchRank         `json:"rank"`
}

// SearchLexical applies authorization, exact scope, lifecycle, and temporal validity
// filters to the corpus before deriving document frequencies and scoring matches.
func (s *Service) SearchLexical(ctx context.Context, request LexicalSearchRequest) (LexicalSearchResult, error) {
	request, terms, err := s.normalizeLexicalSearchRequest(request)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return LexicalSearchResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return LexicalSearchResult{}, err
	}

	entries, err := s.listLexicalCorpusEntries(ctx, request)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	entries, err = s.authorizeLexicalCorpus(ctx, actor, request, entries)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	allowed := make(map[knowledge.EntryID]knowledge.Entry, len(entries))
	for _, entry := range entries {
		allowed[entry.ID] = entry
	}

	postings, err := s.lookupAllLexicalPostings(ctx, terms)
	if err != nil {
		return LexicalSearchResult{}, err
	}
	postings = deduplicateLexicalPostings(postings)
	filtered := postings[:0]
	matched := make(map[knowledge.EntryID]struct{})
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
	matches, err = s.rankSearchMatches(ctx, matches, allowed, request.Scopes, s.now().UTC().Round(0))
	if err != nil {
		return LexicalSearchResult{}, err
	}
	if len(matches) > request.Limit {
		matches = matches[:request.Limit]
	}
	return LexicalSearchResult{
		Terms: slices.Clone(terms), Matches: matches, GraphExpansion: expansion,
		CorpusDocumentCount: uint64(len(entries)), MatchedDocumentCount: uint64(len(matched)),
	}, nil
}

func (s *Service) normalizeLexicalSearchRequest(request LexicalSearchRequest) (LexicalSearchRequest, []string, error) {
	weights := request.Weights
	if weights == (LexicalFieldWeights{}) {
		weights = DefaultLexicalFieldWeights()
	}
	if err := validateLexicalFieldWeights(weights); err != nil {
		return LexicalSearchRequest{}, nil, err
	}
	postingRequest, err := knowledgeStore.NormalizeLexicalPostingRequest(knowledgeStore.LexicalPostingRequest{Terms: []string{request.Query}})
	if err != nil {
		return LexicalSearchRequest{}, nil, err
	}
	if request.Limit < 0 || request.Limit > maxLexicalSearchLimit {
		return LexicalSearchRequest{}, nil, fmt.Errorf("lexical search limit must be between 0 and %d", maxLexicalSearchLimit)
	}
	if request.Limit == 0 {
		request.Limit = defaultLexicalSearchLimit
	}
	request.ChunkIDs = slices.Clone(request.ChunkIDs)
	slices.Sort(request.ChunkIDs)
	request.ChunkIDs = slices.Compact(request.ChunkIDs)
	request.EntryStates = slices.Clone(request.EntryStates)
	if len(request.EntryStates) == 0 {
		request.EntryStates = []knowledge.EntryState{knowledge.EntryStateActive}
		if request.IncludeSuperseded {
			request.EntryStates = append(request.EntryStates, knowledge.EntryStateSuperseded)
		}
	}
	slices.Sort(request.EntryStates)
	request.EntryStates = slices.Compact(request.EntryStates)
	for _, state := range request.EntryStates {
		if state == knowledge.EntryStateUnspecified || !state.IsAEntryState() {
			return LexicalSearchRequest{}, nil, fmt.Errorf("invalid lexical search entry state %q", state)
		}
	}
	if !request.IncludeSuperseded {
		request.EntryStates = slices.DeleteFunc(request.EntryStates, func(state knowledge.EntryState) bool {
			return state == knowledge.EntryStateSuperseded
		})
	}
	request.ChunkStates = slices.Clone(request.ChunkStates)
	if len(request.ChunkStates) == 0 {
		request.ChunkStates = []knowledge.ChunkState{knowledge.ChunkStateActive}
	}
	slices.Sort(request.ChunkStates)
	request.ChunkStates = slices.Compact(request.ChunkStates)
	for _, state := range request.ChunkStates {
		if state == knowledge.ChunkStateUnspecified || !state.IsAChunkState() {
			return LexicalSearchRequest{}, nil, fmt.Errorf("invalid lexical search chunk state %q", state)
		}
	}
	request.Scopes = slices.Clone(request.Scopes)
	for _, scope := range request.Scopes {
		if err := scope.Validate(); err != nil {
			return LexicalSearchRequest{}, nil, err
		}
	}
	slices.SortFunc(request.Scopes, func(left, right knowledge.Scope) int {
		if left.Kind != right.Kind {
			return int(left.Kind) - int(right.Kind)
		}
		return strings.Compare(left.Selector, right.Selector)
	})
	request.Scopes = slices.Compact(request.Scopes)
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

func (s *Service) listLexicalCorpusEntries(ctx context.Context, request LexicalSearchRequest) ([]knowledge.Entry, error) {
	if len(request.EntryStates) == 0 {
		return nil, nil
	}
	scopeKinds := make([]knowledge.ScopeKind, 0, len(request.Scopes))
	for _, scope := range request.Scopes {
		scopeKinds = append(scopeKinds, scope.Kind)
	}
	slices.Sort(scopeKinds)
	scopeKinds = slices.Compact(scopeKinds)
	filter := knowledgeStore.EntryFilter{
		ChunkIDs: request.ChunkIDs, States: request.EntryStates, ScopeKinds: scopeKinds, ValidAt: request.ValidAt,
	}
	entries := make([]knowledge.Entry, 0)
	cursor := ""
	for {
		page, err := s.store.ListEntries(ctx, knowledgeStore.EntryListRequest{
			Filter: filter, Sort: knowledgeStore.EntrySortTitle, Limit: entryCorpusPageLimit, Cursor: cursor,
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

func (s *Service) authorizeLexicalCorpus(ctx context.Context, actor knowledge.Actor, request LexicalSearchRequest, entries []knowledge.Entry) ([]knowledge.Entry, error) {
	chunkIDs := make([]knowledge.ChunkID, 0, len(entries))
	seen := make(map[knowledge.ChunkID]struct{}, len(entries))
	for _, entry := range entries {
		if _, ok := seen[entry.ChunkID]; !ok {
			seen[entry.ChunkID] = struct{}{}
			chunkIDs = append(chunkIDs, entry.ChunkID)
		}
	}
	slices.Sort(chunkIDs)
	chunks := make(map[knowledge.ChunkID]knowledge.Chunk, len(chunkIDs))
	if err := s.store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		for _, chunkID := range chunkIDs {
			chunk, err := tx.Chunk(ctx, chunkID)
			if errors.Is(err, knowledgeStore.ErrNotFound) {
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
	allowedChunks := make(map[knowledge.ChunkID]struct{}, len(chunks))
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

func (s *Service) lookupAllLexicalPostings(ctx context.Context, terms []string) ([]knowledgeStore.LexicalPosting, error) {
	postings := make([]knowledgeStore.LexicalPosting, 0)
	cursor := ""
	for {
		page, err := s.store.LookupLexicalPostings(ctx, knowledgeStore.LexicalPostingRequest{
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

func filteredDocumentFrequencies(postings []knowledgeStore.LexicalPosting, terms []string) []knowledgeStore.LexicalDocumentFrequency {
	counts := make(map[string]uint64, len(terms))
	for _, posting := range postings {
		counts[posting.Term]++
	}
	result := make([]knowledgeStore.LexicalDocumentFrequency, 0, len(terms))
	for _, term := range terms {
		result = append(result, knowledgeStore.LexicalDocumentFrequency{Term: term, Count: counts[term]})
	}
	return result
}
