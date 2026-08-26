package memory

import (
	"context"
	"testing"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestSearchExactFindsNormalizedChunkAndEntryFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	value := chunk(1)
	value.Title = "Résumé tools"
	value.Aliases = []string{"Partition utilities"}
	value.Tags = []string{"linux-tools"}
	item := entry()
	item.Title = "sfdisk"
	item.Aliases = []string{"Disk script tool"}
	item.Tags = []string{"linux-tools"}
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutChunk(ctx, value, 0); err != nil {
			return err
		}
		return tx.PutEntry(ctx, item, 0)
	}); err != nil {
		t.Fatalf("seed exact search: %v", err)
	}

	for _, test := range []struct {
		query string
		field memoryStoreAPI.ExactMatchField
		count int
	}{
		{query: "RÉSUMÉ TOOLS", field: memoryStoreAPI.ExactMatchTitle, count: 1},
		{query: "partition utilities", field: memoryStoreAPI.ExactMatchAlias, count: 1},
		{query: " Linux Tools ", field: memoryStoreAPI.ExactMatchTag, count: 2},
	} {
		page, err := s.SearchExact(ctx, memoryStoreAPI.ExactSearchRequest{Query: test.query})
		if err != nil || len(page.Hits) != test.count {
			t.Fatalf("SearchExact(%q) = %#v, %v", test.query, page, err)
		}
		if page.Hits[0].Matches[0] != test.field {
			t.Fatalf("SearchExact(%q) matches = %v", test.query, page.Hits[0].Matches)
		}
	}

	page, err := s.SearchExact(ctx, memoryStoreAPI.ExactSearchRequest{
		Query: string(value.ID), Kinds: []memoryStoreAPI.RecordKind{memoryStoreAPI.RecordKindEntry},
	})
	if err != nil || len(page.Hits) != 0 {
		t.Fatalf("kind-filtered SearchExact() = %#v, %v", page, err)
	}
}
