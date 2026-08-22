package pebble

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestSearchExactUsesNormalizedChunkAndEntryIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	chunk := txChunk(1)
	chunk.Title = "Résumé tools"
	chunk.Aliases = []string{"Partition utilities"}
	chunk.Tags = []string{"linux-tools"}
	entry := txEntry()
	entry.Title = "sfdisk"
	entry.Aliases = []string{"Disk script tool"}
	entry.Tags = []string{"linux-tools"}
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, chunk, 0); err != nil {
			return err
		}
		return tx.PutEntry(ctx, entry, 0)
	}); err != nil {
		t.Fatalf("seed exact search: %v", err)
	}

	for _, test := range []struct {
		query string
		field knowledgeStore.ExactMatchField
		count int
	}{
		{query: "RÉSUMÉ TOOLS", field: knowledgeStore.ExactMatchTitle, count: 1},
		{query: "PARTITION UTILITIES", field: knowledgeStore.ExactMatchAlias, count: 1},
		{query: "Linux Tools", field: knowledgeStore.ExactMatchTag, count: 2},
		{query: string(entry.ID), field: knowledgeStore.ExactMatchID, count: 1},
	} {
		page, err := s.SearchExact(ctx, knowledgeStore.ExactSearchRequest{Query: test.query})
		if err != nil || len(page.Hits) != test.count {
			t.Fatalf("SearchExact(%q) = %#v, %v", test.query, page, err)
		}
		if page.Hits[0].Matches[0] != test.field {
			t.Fatalf("SearchExact(%q) matches = %v", test.query, page.Hits[0].Matches)
		}
	}
}

func TestSearchExactReturnsNoTitleHitWhenDerivedIndexIsAbsent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	chunk := txChunk(1)
	chunk.Title = "Indexed title"
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(ctx, chunk, 0) }); err != nil {
		t.Fatalf("seed exact search: %v", err)
	}
	key := indexKey(s.meta.IndexGeneration, chunkTitleIndex,
		encodeIndexTuple(knowledge.NormalizeComparisonKey(chunk.Title), string(chunk.ID)))
	if err := s.db.Delete(key, nil); err != nil {
		t.Fatalf("remove title index: %v", err)
	}
	page, err := s.SearchExact(ctx, knowledgeStore.ExactSearchRequest{Query: chunk.Title})
	if err != nil || len(page.Hits) != 0 {
		t.Fatalf("SearchExact() without index = %#v, %v", page, err)
	}
}
