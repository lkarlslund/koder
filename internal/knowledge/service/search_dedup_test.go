package service

import (
	"testing"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestDeduplicateLexicalPostingsMergesRevisionProjectionsWithoutDoubleCounting(t *testing.T) {
	t.Parallel()
	postings := []knowledgeStore.LexicalPosting{
		{Term: "tools", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd34", Frequencies: knowledgeStore.LexicalFieldFrequencies{Tags: 1}},
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", Frequencies: knowledgeStore.LexicalFieldFrequencies{Title: 1, Body: 3}},
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", Frequencies: knowledgeStore.LexicalFieldFrequencies{Title: 2, Body: 1, Aliases: 1}},
	}
	got := deduplicateLexicalPostings(postings)
	if len(got) != 2 || got[0].Term != "linux" || got[1].Term != "tools" {
		t.Fatalf("deduplicated postings = %#v", got)
	}
	want := knowledgeStore.LexicalFieldFrequencies{Title: 2, Body: 3, Aliases: 1}
	if got[0].Frequencies != want {
		t.Fatalf("merged frequencies = %#v, want %#v", got[0].Frequencies, want)
	}
	frequencies := filteredDocumentFrequencies(got, []string{"linux", "tools"})
	if frequencies[0].Count != 1 || frequencies[1].Count != 1 {
		t.Fatalf("document frequencies after dedup = %#v", frequencies)
	}
}
