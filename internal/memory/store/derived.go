package store

import "github.com/lkarlslund/koder/internal/memory"

// DeriveChunkCounts reconstructs chunk counters from canonical graph records. Entries
// belong to exactly one chunk; a link counts once for every chunk it touches directly or
// through an owned entry; evidence counts once per chunk even when several records in
// that chunk cite it.
func DeriveChunkCounts(chunks []memory.Chunk, entries []memory.Entry, links []memory.Link, evidence []memory.Evidence) map[memory.ChunkID]memory.ChunkCounts {
	counts := make(map[memory.ChunkID]memory.ChunkCounts, len(chunks))
	entryOwners := make(map[memory.EntryID]memory.ChunkID, len(entries))
	evidenceByChunk := make(map[memory.ChunkID]map[memory.EvidenceID]struct{}, len(chunks))
	existingEvidence := make(map[memory.EvidenceID]struct{}, len(evidence))
	for _, item := range evidence {
		existingEvidence[item.ID] = struct{}{}
	}
	for _, chunk := range chunks {
		counts[chunk.ID] = memory.ChunkCounts{}
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
		touched := make(map[memory.ChunkID]struct{}, 2)
		for _, endpoint := range []memory.ObjectRef{link.Source, link.Target} {
			switch endpoint.Kind {
			case memory.ObjectKindChunk:
				touched[memory.ChunkID(endpoint.ID)] = struct{}{}
			case memory.ObjectKindEntry:
				if owner, exists := entryOwners[memory.EntryID(endpoint.ID)]; exists {
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

func addExistingEvidence(destination map[memory.ChunkID]map[memory.EvidenceID]struct{}, chunkID memory.ChunkID, existing map[memory.EvidenceID]struct{}, values []memory.EvidenceID) {
	for _, value := range values {
		if _, exists := existing[value]; !exists {
			continue
		}
		ids := destination[chunkID]
		if ids == nil {
			ids = make(map[memory.EvidenceID]struct{})
			destination[chunkID] = ids
		}
		ids[value] = struct{}{}
	}
}
