package store

import (
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
)

func TestDeriveChunkCountsDeduplicatesLinksAndEvidencePerChunk(t *testing.T) {
	t.Parallel()
	chunkA := memory.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a1")
	chunkB := memory.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a2")
	entryA := memory.EntryID("01a01688-fc5d-7f7d-8bb8-de244977f8a3")
	entryB := memory.EntryID("01a01688-fc5d-7f7d-8bb8-de244977f8a4")
	shared := memory.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a5")
	onlyA := memory.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a6")
	linkEvidence := memory.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a7")
	missing := memory.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a8")
	counts := DeriveChunkCounts(
		[]memory.Chunk{{ID: chunkA}, {ID: chunkB}},
		[]memory.Entry{
			{ID: entryA, ChunkID: chunkA, EvidenceIDs: []memory.EvidenceID{shared, onlyA, missing}},
			{ID: entryB, ChunkID: chunkB, EvidenceIDs: []memory.EvidenceID{shared}},
		},
		[]memory.Link{
			{
				Source:      memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryA)},
				Target:      memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunkA)},
				EvidenceIDs: []memory.EvidenceID{shared},
			},
			{
				Source:      memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryA)},
				Target:      memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryB)},
				EvidenceIDs: []memory.EvidenceID{linkEvidence},
			},
		},
		[]memory.Evidence{{ID: shared}, {ID: onlyA}, {ID: linkEvidence}},
	)
	if got := counts[chunkA]; got != (memory.ChunkCounts{Entries: 1, Links: 2, Evidence: 3}) {
		t.Fatalf("chunk A counts = %#v", got)
	}
	if got := counts[chunkB]; got != (memory.ChunkCounts{Entries: 1, Links: 1, Evidence: 2}) {
		t.Fatalf("chunk B counts = %#v", got)
	}
}
