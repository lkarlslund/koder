package memory

import (
	"context"
	"testing"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestLookupLexicalPostingsDerivesEntryCandidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	value := chunk(1)
	item := entry()
	item.Title = "Linux partition tools"
	item.Body = "Use sfdisk for scripted partitioning."
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, value, 0); err != nil {
			return err
		}
		return tx.PutEntry(ctx, item, 0)
	}); err != nil {
		t.Fatalf("seed lexical postings: %v", err)
	}

	page, err := s.LookupLexicalPostings(ctx, knowledgeStore.LexicalPostingRequest{Terms: []string{"SFDISK linux"}})
	if err != nil || len(page.Postings) != 2 {
		t.Fatalf("LookupLexicalPostings() = %#v, %v", page, err)
	}
	if page.DocumentCount != 1 || len(page.DocumentFrequencies) != 2 || page.DocumentFrequencies[0].Count != 1 || page.DocumentFrequencies[1].Count != 1 {
		t.Fatalf("lexical corpus statistics = %#v", page)
	}
	if page.Postings[0].Term != "linux" || page.Postings[0].Frequencies.Title != 1 ||
		page.Postings[1].Term != "sfdisk" || page.Postings[1].Frequencies.Body != 1 {
		t.Fatalf("lexical postings = %#v", page.Postings)
	}
}
