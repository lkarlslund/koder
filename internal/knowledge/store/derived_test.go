package store

import (
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestDeriveChunkCountsDeduplicatesLinksAndEvidencePerChunk(t *testing.T) {
	t.Parallel()
	chunkA := knowledge.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a1")
	chunkB := knowledge.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a2")
	entryA := knowledge.EntryID("01a01688-fc5d-7f7d-8bb8-de244977f8a3")
	entryB := knowledge.EntryID("01a01688-fc5d-7f7d-8bb8-de244977f8a4")
	shared := knowledge.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a5")
	onlyA := knowledge.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a6")
	linkEvidence := knowledge.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a7")
	missing := knowledge.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a8")
	counts := DeriveChunkCounts(
		[]knowledge.Chunk{{ID: chunkA}, {ID: chunkB}},
		[]knowledge.Entry{
			{ID: entryA, ChunkID: chunkA, EvidenceIDs: []knowledge.EvidenceID{shared, onlyA, missing}},
			{ID: entryB, ChunkID: chunkB, EvidenceIDs: []knowledge.EvidenceID{shared}},
		},
		[]knowledge.Link{
			{
				Source:      knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entryA)},
				Target:      knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(chunkA)},
				EvidenceIDs: []knowledge.EvidenceID{shared},
			},
			{
				Source:      knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entryA)},
				Target:      knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entryB)},
				EvidenceIDs: []knowledge.EvidenceID{linkEvidence},
			},
		},
		[]knowledge.Evidence{{ID: shared}, {ID: onlyA}, {ID: linkEvidence}},
	)
	if got := counts[chunkA]; got != (knowledge.ChunkCounts{Entries: 1, Links: 2, Evidence: 3}) {
		t.Fatalf("chunk A counts = %#v", got)
	}
	if got := counts[chunkB]; got != (knowledge.ChunkCounts{Entries: 1, Links: 1, Evidence: 2}) {
		t.Fatalf("chunk B counts = %#v", got)
	}
}
