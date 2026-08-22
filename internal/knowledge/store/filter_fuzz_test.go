package store

import (
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func FuzzBackendNeutralFiltersStayBounded(f *testing.F) {
	f.Add(uint8(knowledge.ChunkKindReference), uint8(knowledge.EntryKindFact), uint8(knowledge.LinkKindPartOf), int16(1), "", "")
	f.Add(uint8(255), uint8(255), uint8(255), int16(-1), "../tag", "bad-cursor")
	f.Fuzz(func(t *testing.T, chunkKind, entryKind, linkKind uint8, limit int16, tag, cursor string) {
		chunk := exactTestChunk("019f132e-4f3a-739a-9ab2-5198dcd19e67", "Chunk")
		chunk.Tags = []string{"safe"}
		entry := exactTestEntry("01a01f76-1ff6-7c1d-967a-66ad5703dd33", chunk.ID, "Entry")
		link := exactTestLink("01a020a6-84d5-7b03-a995-bb2cfb4528b0", chunk.ID, entry.ID)

		chunkPage, err := PaginateChunks([]knowledge.Chunk{chunk}, ChunkListRequest{
			Filter: ChunkFilter{Kinds: []knowledge.ChunkKind{knowledge.ChunkKind(chunkKind)}, Tags: []string{tag}},
			Limit:  int(limit), Cursor: cursor,
		}, 1)
		if err == nil && len(chunkPage.Chunks) > 1 {
			t.Fatalf("PaginateChunks() returned %d records from one candidate", len(chunkPage.Chunks))
		}
		entryPage, err := PaginateEntries([]knowledge.Entry{entry}, EntryListRequest{
			Filter: EntryFilter{Kinds: []knowledge.EntryKind{knowledge.EntryKind(entryKind)}, Tags: []string{tag}},
			Limit:  int(limit), Cursor: cursor,
		}, 1)
		if err == nil && len(entryPage.Entries) > 1 {
			t.Fatalf("PaginateEntries() returned %d records from one candidate", len(entryPage.Entries))
		}
		linkPage, err := PaginateAdjacentLinks([]knowledge.Link{link}, AdjacentLinkListRequest{
			Filter: AdjacentLinkFilter{
				Endpoint: link.Source, Kinds: []knowledge.LinkKind{knowledge.LinkKind(linkKind)}, Direction: LinkDirectionBoth,
			},
			Limit: int(limit), Cursor: cursor,
		}, 1)
		if err == nil && len(linkPage.Links) > 1 {
			t.Fatalf("PaginateAdjacentLinks() returned %d records from one candidate", len(linkPage.Links))
		}
	})
}
