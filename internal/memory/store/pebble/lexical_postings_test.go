package pebble

import (
	"context"
	"testing"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestLookupLexicalPostingsTracksEntryCreateUpdateDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	chunk := txChunk(1)
	entry := txEntry()
	entry.Title = "Linux partition tools"
	entry.Body = "Use sfdisk for scripted partitioning."
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutChunk(ctx, chunk, 0); err != nil {
			return err
		}
		return tx.PutEntry(ctx, entry, 0)
	}); err != nil {
		t.Fatalf("seed lexical postings: %v", err)
	}
	assertLexicalTerms(t, s, []string{"linux", "sfdisk"}, []string{"linux", "sfdisk"})

	updated := entry
	updated.Title = "BSD disk tools"
	updated.Body = "Use disklabel."
	updated.Revision = txRevision(2)
	updated.UpdatedAt = updated.Revision.CreatedAt
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutEntry(ctx, updated, 1) }); err != nil {
		t.Fatalf("update lexical postings: %v", err)
	}
	assertLexicalTerms(t, s, []string{"linux", "sfdisk", "bsd", "disklabel"}, []string{"bsd", "disklabel"})

	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.DeleteEntry(ctx, updated.ID, 2) }); err != nil {
		t.Fatalf("delete lexical postings: %v", err)
	}
	assertLexicalTerms(t, s, []string{"bsd", "disklabel"}, nil)
}

func assertLexicalTerms(t *testing.T, s *Store, terms, want []string) {
	t.Helper()
	page, err := s.LookupLexicalPostings(context.Background(), memoryStoreAPI.LexicalPostingRequest{Terms: terms})
	if err != nil {
		t.Fatalf("LookupLexicalPostings(%v) error = %v", terms, err)
	}
	got := make([]string, 0, len(page.Postings))
	for _, posting := range page.Postings {
		got = append(got, posting.Term)
	}
	wantDocumentCount := uint64(1)
	if want == nil {
		wantDocumentCount = 0
	}
	if page.DocumentCount != wantDocumentCount {
		t.Fatalf("lexical document count = %d, want %d", page.DocumentCount, wantDocumentCount)
	}
	if len(got) != len(want) {
		t.Fatalf("LookupLexicalPostings(%v) terms = %v, want %v", terms, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("LookupLexicalPostings(%v) terms = %v, want %v", terms, got, want)
		}
	}
}
