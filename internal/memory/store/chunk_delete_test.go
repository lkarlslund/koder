package store

import (
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
)

func TestDeriveChunkDeletionBlockersOnlyCascadesExclusiveExistingEvidence(t *testing.T) {
	t.Parallel()
	rootID := memory.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a1")
	otherID := memory.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a2")
	sharedID := memory.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a3")
	exclusiveID := memory.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a4")
	missingID := memory.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a5")
	entries := []memory.Entry{
		{ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a6", ChunkID: rootID, EvidenceIDs: []memory.EvidenceID{sharedID, exclusiveID, missingID}},
		{ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a7", ChunkID: otherID, EvidenceIDs: []memory.EvidenceID{sharedID}},
	}
	blockers := DeriveChunkDeletionBlockers(
		memory.Chunk{ID: rootID},
		[]memory.Chunk{{ID: rootID}, {ID: otherID}}, entries, nil,
		[]memory.Evidence{{ID: sharedID}, {ID: exclusiveID}},
	)
	if len(blockers.EvidenceIDs) != 1 || blockers.EvidenceIDs[0] != exclusiveID {
		t.Fatalf("exclusive evidence blockers = %v, want [%s]", blockers.EvidenceIDs, exclusiveID)
	}
}
