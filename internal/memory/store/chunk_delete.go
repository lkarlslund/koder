package store

import (
	"slices"

	"github.com/lkarlslund/koder/internal/memory"
)

// ChunkDeletionBlockers is an authoritative transaction-snapshot view of records that
// would be orphaned by deleting a chunk. ReportedCounts is included because derived
// metadata must be repaired before destructive work if it disagrees with canonical data.
type ChunkDeletionBlockers struct {
	EntryIDs          []memory.EntryID    `json:"entry_ids,omitempty"`
	LinkIDs           []memory.LinkID     `json:"link_ids,omitempty"`
	EvidenceIDs       []memory.EvidenceID `json:"evidence_ids,omitempty"`
	DependencyIDs     []memory.ChunkID    `json:"dependency_ids,omitempty"`
	DependentChunkIDs []memory.ChunkID    `json:"dependent_chunk_ids,omitempty"`
	ReportedCounts    memory.ChunkCounts  `json:"reported_counts,omitzero"`
}

func (b ChunkDeletionBlockers) Empty() bool {
	return len(b.EntryIDs) == 0 && len(b.LinkIDs) == 0 && len(b.EvidenceIDs) == 0 && len(b.DependencyIDs) == 0 &&
		len(b.DependentChunkIDs) == 0 && b.ReportedCounts == (memory.ChunkCounts{})
}

// DeriveChunkDeletionBlockers computes canonical delete dependencies from one consistent
// snapshot. Evidence is included only when it exists and no retained entry or link uses it.
func DeriveChunkDeletionBlockers(target memory.Chunk, chunks []memory.Chunk, entries []memory.Entry, links []memory.Link, evidence []memory.Evidence) ChunkDeletionBlockers {
	blockers := ChunkDeletionBlockers{
		DependencyIDs: slices.Clone(target.DependencyIDs), ReportedCounts: target.Counts,
	}
	ownedEntries := make(map[memory.EntryID]struct{})
	for _, entry := range entries {
		if entry.ChunkID == target.ID {
			ownedEntries[entry.ID] = struct{}{}
			blockers.EntryIDs = append(blockers.EntryIDs, entry.ID)
		}
	}
	touchingLinks := make(map[memory.LinkID]struct{})
	for _, link := range links {
		if linkTouchesChunk(link, target.ID, ownedEntries) {
			touchingLinks[link.ID] = struct{}{}
			blockers.LinkIDs = append(blockers.LinkIDs, link.ID)
		}
	}
	candidateEvidence := make(map[memory.EvidenceID]struct{})
	retainedEvidence := make(map[memory.EvidenceID]struct{})
	for _, entry := range entries {
		destination := retainedEvidence
		if _, owned := ownedEntries[entry.ID]; owned {
			destination = candidateEvidence
		}
		addEvidenceIDs(destination, entry.EvidenceIDs)
		addEvidenceIDs(destination, entry.Verification.EvidenceIDs)
	}
	for _, link := range links {
		destination := retainedEvidence
		if _, touching := touchingLinks[link.ID]; touching {
			destination = candidateEvidence
		}
		addEvidenceIDs(destination, link.EvidenceIDs)
	}
	for _, item := range evidence {
		_, candidate := candidateEvidence[item.ID]
		_, retained := retainedEvidence[item.ID]
		if candidate && !retained {
			blockers.EvidenceIDs = append(blockers.EvidenceIDs, item.ID)
		}
	}
	for _, candidate := range chunks {
		if candidate.ID != target.ID && slices.Contains(candidate.DependencyIDs, target.ID) {
			blockers.DependentChunkIDs = append(blockers.DependentChunkIDs, candidate.ID)
		}
	}
	slices.Sort(blockers.EntryIDs)
	slices.Sort(blockers.LinkIDs)
	slices.Sort(blockers.EvidenceIDs)
	slices.Sort(blockers.DependencyIDs)
	slices.Sort(blockers.DependentChunkIDs)
	return blockers
}

func addEvidenceIDs(destination map[memory.EvidenceID]struct{}, values []memory.EvidenceID) {
	for _, value := range values {
		destination[value] = struct{}{}
	}
}

func linkTouchesChunk(link memory.Link, chunkID memory.ChunkID, entryIDs map[memory.EntryID]struct{}) bool {
	for _, endpoint := range []memory.ObjectRef{link.Source, link.Target} {
		if endpoint.Kind == memory.ObjectKindChunk && endpoint.ID == string(chunkID) {
			return true
		}
		if endpoint.Kind == memory.ObjectKindEntry {
			if _, exists := entryIDs[memory.EntryID(endpoint.ID)]; exists {
				return true
			}
		}
	}
	return false
}
