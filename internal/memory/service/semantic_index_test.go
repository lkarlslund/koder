package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
)

const (
	semanticEntryA    memory.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd33"
	semanticEntryB    memory.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd34"
	semanticRevisionA memory.RevisionID = "01a01688-fc5d-7f7d-8bb8-de244977f8a1"
	semanticRevisionB memory.RevisionID = "01a01688-fc5d-7f7d-8bb8-de244977f8a2"
)

func TestNormalizeSemanticSearchRequestBuildsDeterministicAuthorizedCorpus(t *testing.T) {
	t.Parallel()
	request, err := NormalizeSemanticSearchRequest(SemanticSearchRequest{
		Query: "  disk partition utility  ", Limit: 5,
		Corpus: []SemanticCorpusEntry{
			{EntryID: semanticEntryB, RevisionID: semanticRevisionB},
			{EntryID: semanticEntryA, RevisionID: semanticRevisionA},
			{EntryID: semanticEntryA, RevisionID: semanticRevisionA},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeSemanticSearchRequest() error = %v", err)
	}
	if request.Query != "disk partition utility" {
		t.Fatalf("query = %q", request.Query)
	}
	want := []SemanticCorpusEntry{
		{EntryID: semanticEntryA, RevisionID: semanticRevisionA},
		{EntryID: semanticEntryB, RevisionID: semanticRevisionB},
	}
	if !slices.Equal(request.Corpus, want) {
		t.Fatalf("corpus = %#v, want %#v", request.Corpus, want)
	}
}

func TestNormalizeSemanticSearchRequestRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	t.Parallel()
	for name, request := range map[string]SemanticSearchRequest{
		"empty query": {Limit: 1},
		"zero limit":  {Query: "query"},
		"conflicting revision": {
			Query: "query", Limit: 1,
			Corpus: []SemanticCorpusEntry{
				{EntryID: semanticEntryA, RevisionID: semanticRevisionA},
				{EntryID: semanticEntryA, RevisionID: semanticRevisionB},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeSemanticSearchRequest(request); err == nil {
				t.Fatal("NormalizeSemanticSearchRequest() unexpectedly succeeded")
			}
		})
	}
}

func TestNormalizeSemanticSearchResultRejectsLeaksAndStaleRevisions(t *testing.T) {
	t.Parallel()
	request := SemanticSearchRequest{
		Query: "partition tool", Limit: 2,
		Corpus: []SemanticCorpusEntry{{EntryID: semanticEntryA, RevisionID: semanticRevisionA}},
	}
	identity := SemanticIndexIdentity{
		ProviderID: "test", ModelID: "embed-test", Dimensions: 3,
		Metric: SemanticMetricCosine, DocumentSchema: "memory-entry-v1",
	}
	for name, match := range map[string]SemanticSearchMatch{
		"unauthorized entry": {EntryID: semanticEntryB, RevisionID: semanticRevisionB, Score: 0.9},
		"stale revision":     {EntryID: semanticEntryA, RevisionID: semanticRevisionB, Score: 0.9},
		"invalid score":      {EntryID: semanticEntryA, RevisionID: semanticRevisionA, Score: math.NaN()},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeSemanticSearchResult(request, SemanticSearchResult{
				Identity: identity, Generation: 1, Matches: []SemanticSearchMatch{match},
			})
			if err == nil {
				t.Fatal("NormalizeSemanticSearchResult() unexpectedly succeeded")
			}
		})
	}
}

func TestNormalizeSemanticSearchResultOrdersEqualScoresByEntryID(t *testing.T) {
	t.Parallel()
	request := SemanticSearchRequest{
		Query: "partition tool", Limit: 2,
		Corpus: []SemanticCorpusEntry{
			{EntryID: semanticEntryA, RevisionID: semanticRevisionA},
			{EntryID: semanticEntryB, RevisionID: semanticRevisionB},
		},
	}
	result, err := NormalizeSemanticSearchResult(request, SemanticSearchResult{
		Identity: SemanticIndexIdentity{
			ProviderID: "test", ModelID: "embed-test", Dimensions: 3,
			Metric: SemanticMetricCosine, DocumentSchema: "memory-entry-v1",
		},
		Generation: 4,
		Matches: []SemanticSearchMatch{
			{EntryID: semanticEntryB, RevisionID: semanticRevisionB, Score: 0.75},
			{EntryID: semanticEntryA, RevisionID: semanticRevisionA, Score: 0.75},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeSemanticSearchResult() error = %v", err)
	}
	if got := []memory.EntryID{result.Matches[0].EntryID, result.Matches[1].EntryID}; !slices.Equal(got, []memory.EntryID{semanticEntryA, semanticEntryB}) {
		t.Fatalf("ordered entries = %v", got)
	}
}

func TestSemanticDocumentRequiresMatchingContentHash(t *testing.T) {
	t.Parallel()
	content := "Title: sfdisk\nUse sfdisk when fdisk is unavailable."
	document := SemanticDocument{
		EntryID: semanticEntryA, RevisionID: semanticRevisionA, FragmentID: "body-0001",
		Content: content, ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content))),
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	document.Content += " changed"
	if err := document.Validate(); err == nil {
		t.Fatal("Validate() accepted stale content hash")
	}
}

func TestNormalizeBlendedSearchScoresRejectsInjectedEntriesAndOrdersTies(t *testing.T) {
	t.Parallel()
	request := SearchScoreBlendRequest{
		Lexical:  []SearchScoreSignal{{EntryID: semanticEntryB, Score: 2}},
		Semantic: []SearchScoreSignal{{EntryID: semanticEntryA, Score: 0.8}},
		Limit:    2,
	}
	result, err := NormalizeBlendedSearchScores(request, []BlendedSearchScore{
		{EntryID: semanticEntryB, Score: 0.8, LexicalContribution: 0.8},
		{EntryID: semanticEntryA, Score: 0.8, SemanticContribution: 0.8},
	})
	if err != nil {
		t.Fatalf("NormalizeBlendedSearchScores() error = %v", err)
	}
	if got := []memory.EntryID{result[0].EntryID, result[1].EntryID}; !slices.Equal(got, []memory.EntryID{semanticEntryA, semanticEntryB}) {
		t.Fatalf("ordered entries = %v", got)
	}
	if _, err := NormalizeBlendedSearchScores(request, []BlendedSearchScore{
		{EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd35", Score: 1, SemanticContribution: 1},
	}); err == nil {
		t.Fatal("NormalizeBlendedSearchScores() accepted injected entry")
	}
}

func TestSemanticProviderAndScoreBlenderContractsAreReplaceable(t *testing.T) {
	t.Parallel()
	var _ SemanticIndexProvider = semanticProviderStub{}
	want := []BlendedSearchScore{{EntryID: semanticEntryA, Score: 0.8, LexicalContribution: 0.5, SemanticContribution: 0.3}}
	blender := SearchScoreBlenderFunc(func(ctx context.Context, request SearchScoreBlendRequest) ([]BlendedSearchScore, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if request.Limit != 1 {
			t.Fatalf("blend limit = %d", request.Limit)
		}
		return slices.Clone(want), nil
	})
	got, err := blender.BlendSearchScores(context.Background(), SearchScoreBlendRequest{Limit: 1})
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("BlendSearchScores() = %#v, %v", got, err)
	}
}

type semanticProviderStub struct{}

func (semanticProviderStub) Status(context.Context) (SemanticIndexStatus, error) {
	return SemanticIndexStatus{}, ErrSemanticIndexUnavailable
}

func (semanticProviderStub) Search(context.Context, SemanticSearchRequest) (SemanticSearchResult, error) {
	return SemanticSearchResult{}, ErrSemanticIndexUnavailable
}

func (semanticProviderStub) Rebuild(context.Context, SemanticDocumentSource) (SemanticIndexStatus, error) {
	return SemanticIndexStatus{}, ErrSemanticIndexUnavailable
}
