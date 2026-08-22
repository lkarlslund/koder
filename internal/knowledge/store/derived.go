package store

import "github.com/lkarlslund/koder/internal/knowledge"

// DeriveChunkCounts reconstructs chunk counters from canonical graph records. Entries
// belong to exactly one chunk; a link counts once for every chunk it touches directly or
// through an owned entry; evidence counts once per chunk even when several records in
// that chunk cite it.
func DeriveChunkCounts(chunks []knowledge.Chunk, entries []knowledge.Entry, links []knowledge.Link, evidence []knowledge.Evidence) map[knowledge.ChunkID]knowledge.ChunkCounts {
	counts := make(map[knowledge.ChunkID]knowledge.ChunkCounts, len(chunks))
	entryOwners := make(map[knowledge.EntryID]knowledge.ChunkID, len(entries))
	evidenceByChunk := make(map[knowledge.ChunkID]map[knowledge.EvidenceID]struct{}, len(chunks))
	existingEvidence := make(map[knowledge.EvidenceID]struct{}, len(evidence))
	for _, item := range evidence {
		existingEvidence[item.ID] = struct{}{}
	}
	for _, chunk := range chunks {
		counts[chunk.ID] = knowledge.ChunkCounts{}
	}
	for _, entry := range entries {
		entryOwners[entry.ID] = entry.ChunkID
		count, exists := counts[entry.ChunkID]
		if !exists {
			continue
		}
		count.Entries++
		counts[entry.ChunkID] = count
		addExistingEvidence(evidenceByChunk, entry.ChunkID, existingEvidence, entry.EvidenceIDs)
		addExistingEvidence(evidenceByChunk, entry.ChunkID, existingEvidence, entry.Verification.EvidenceIDs)
	}
	for _, link := range links {
		touched := make(map[knowledge.ChunkID]struct{}, 2)
		for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
			switch endpoint.Kind {
			case knowledge.ObjectKindChunk:
				touched[knowledge.ChunkID(endpoint.ID)] = struct{}{}
			case knowledge.ObjectKindEntry:
				if owner, exists := entryOwners[knowledge.EntryID(endpoint.ID)]; exists {
					touched[owner] = struct{}{}
				}
			}
		}
		for chunkID := range touched {
			count, exists := counts[chunkID]
			if !exists {
				continue
			}
			count.Links++
			counts[chunkID] = count
			addExistingEvidence(evidenceByChunk, chunkID, existingEvidence, link.EvidenceIDs)
		}
	}
	for chunkID, ids := range evidenceByChunk {
		count := counts[chunkID]
		count.Evidence = uint64(len(ids))
		counts[chunkID] = count
	}
	return counts
}

func addExistingEvidence(destination map[knowledge.ChunkID]map[knowledge.EvidenceID]struct{}, chunkID knowledge.ChunkID, existing map[knowledge.EvidenceID]struct{}, values []knowledge.EvidenceID) {
	for _, value := range values {
		if _, exists := existing[value]; !exists {
			continue
		}
		ids := destination[chunkID]
		if ids == nil {
			ids = make(map[knowledge.EvidenceID]struct{})
			destination[chunkID] = ids
		}
		ids[value] = struct{}{}
	}
}
