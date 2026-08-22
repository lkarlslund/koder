package store

import (
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestDeriveChunkDeletionBlockersOnlyCascadesExclusiveExistingEvidence(t *testing.T) {
	t.Parallel()
	rootID := knowledge.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a1")
	otherID := knowledge.ChunkID("01a01688-fc5d-7f7d-8bb8-de244977f8a2")
	sharedID := knowledge.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a3")
	exclusiveID := knowledge.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a4")
	missingID := knowledge.EvidenceID("01a01688-fc5d-7f7d-8bb8-de244977f8a5")
	entries := []knowledge.Entry{
		{ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a6", ChunkID: rootID, EvidenceIDs: []knowledge.EvidenceID{sharedID, exclusiveID, missingID}},
		{ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a7", ChunkID: otherID, EvidenceIDs: []knowledge.EvidenceID{sharedID}},
	}
	blockers := DeriveChunkDeletionBlockers(
		knowledge.Chunk{ID: rootID},
		[]knowledge.Chunk{{ID: rootID}, {ID: otherID}}, entries, nil,
		[]knowledge.Evidence{{ID: sharedID}, {ID: exclusiveID}},
	)
	if len(blockers.EvidenceIDs) != 1 || blockers.EvidenceIDs[0] != exclusiveID {
		t.Fatalf("exclusive evidence blockers = %v, want [%s]", blockers.EvidenceIDs, exclusiveID)
	}
}
