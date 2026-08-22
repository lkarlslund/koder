package service

import (
	"math"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestScoreLexicalMatchesRewardsRareTermsAndStrongerFields(t *testing.T) {
	t.Parallel()
	postings := []knowledgeStore.LexicalPosting{
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", Frequencies: knowledgeStore.LexicalFieldFrequencies{Body: 1}},
		{Term: "sfdisk", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd34", Frequencies: knowledgeStore.LexicalFieldFrequencies{Body: 1}},
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd35", Frequencies: knowledgeStore.LexicalFieldFrequencies{Title: 1}},
	}
	frequencies := []knowledgeStore.LexicalDocumentFrequency{{Term: "linux", Count: 8}, {Term: "sfdisk", Count: 1}}
	got, err := ScoreLexicalMatches(postings, 10, frequencies, LexicalFieldWeights{})
	if err != nil {
		t.Fatalf("ScoreLexicalMatches() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("scored matches = %#v", got)
	}
	if got[0].EntryID != knowledge.EntryID("01a01f76-1ff6-7c1d-967a-66ad5703dd34") {
		t.Fatalf("rare-term winner = %s, want sfdisk entry", got[0].EntryID)
	}
	var titleScore, bodyScore float64
	for _, result := range got {
		switch result.EntryID {
		case "01a01f76-1ff6-7c1d-967a-66ad5703dd33":
			bodyScore = result.Score
		case "01a01f76-1ff6-7c1d-967a-66ad5703dd35":
			titleScore = result.Score
		}
	}
	if titleScore <= bodyScore {
		t.Fatalf("title score %f <= body score %f", titleScore, bodyScore)
	}
}

func TestScoreLexicalMatchesAggregatesTermsAndBreaksTiesByID(t *testing.T) {
	t.Parallel()
	postings := []knowledgeStore.LexicalPosting{
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd34", Frequencies: knowledgeStore.LexicalFieldFrequencies{Title: 1}},
		{Term: "tools", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd34", Frequencies: knowledgeStore.LexicalFieldFrequencies{Tags: 1}},
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", Frequencies: knowledgeStore.LexicalFieldFrequencies{Title: 1}},
	}
	frequencies := []knowledgeStore.LexicalDocumentFrequency{{Term: "linux", Count: 2}, {Term: "tools", Count: 1}}
	got, err := ScoreLexicalMatches(postings, 2, frequencies, LexicalFieldWeights{})
	if err != nil {
		t.Fatalf("ScoreLexicalMatches() error = %v", err)
	}
	if len(got) != 2 || got[0].EntryID != "01a01f76-1ff6-7c1d-967a-66ad5703dd34" || len(got[0].Terms) != 2 {
		t.Fatalf("aggregated matches = %#v", got)
	}

	tied, err := ScoreLexicalMatches([]knowledgeStore.LexicalPosting{postings[0], postings[2]}, 2,
		[]knowledgeStore.LexicalDocumentFrequency{{Term: "linux", Count: 2}}, LexicalFieldWeights{})
	if err != nil || len(tied) != 2 || tied[0].EntryID != "01a01f76-1ff6-7c1d-967a-66ad5703dd33" {
		t.Fatalf("single tied setup = %#v, %v", tied, err)
	}
}

func TestScoreLexicalMatchesRejectsInconsistentStatisticsAndWeights(t *testing.T) {
	t.Parallel()
	posting := knowledgeStore.LexicalPosting{
		Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33",
		Frequencies: knowledgeStore.LexicalFieldFrequencies{Title: 1},
	}
	for _, test := range []struct {
		documents   uint64
		frequencies []knowledgeStore.LexicalDocumentFrequency
		weights     LexicalFieldWeights
	}{
		{frequencies: []knowledgeStore.LexicalDocumentFrequency{{Term: "linux", Count: 1}}},
		{documents: 1},
		{documents: 1, frequencies: []knowledgeStore.LexicalDocumentFrequency{{Term: "linux", Count: 2}}},
		{documents: 1, frequencies: []knowledgeStore.LexicalDocumentFrequency{{Term: "linux", Count: 1}}, weights: LexicalFieldWeights{Body: math.NaN()}},
	} {
		if _, err := ScoreLexicalMatches([]knowledgeStore.LexicalPosting{posting}, test.documents, test.frequencies, test.weights); err == nil {
			t.Errorf("ScoreLexicalMatches(%#v) unexpectedly succeeded", test)
		}
	}
	overflow := posting
	overflow.Frequencies.Title = 2
	if _, err := ScoreLexicalMatches([]knowledgeStore.LexicalPosting{overflow}, 1,
		[]knowledgeStore.LexicalDocumentFrequency{{Term: "linux", Count: 1}},
		LexicalFieldWeights{Title: math.MaxFloat64}); err == nil {
		t.Fatal("ScoreLexicalMatches(overflow) unexpectedly succeeded")
	}
}
