package store

import (
	"slices"

	"github.com/lkarlslund/koder/internal/knowledge"
)

// ChunkDeletionBlockers is an authoritative transaction-snapshot view of records that
// would be orphaned by deleting a chunk. ReportedCounts is included because derived
// metadata must be repaired before destructive work if it disagrees with canonical data.
type ChunkDeletionBlockers struct {
	EntryIDs          []knowledge.EntryID    `json:"entry_ids,omitempty"`
	LinkIDs           []knowledge.LinkID     `json:"link_ids,omitempty"`
	EvidenceIDs       []knowledge.EvidenceID `json:"evidence_ids,omitempty"`
	DependencyIDs     []knowledge.ChunkID    `json:"dependency_ids,omitempty"`
	DependentChunkIDs []knowledge.ChunkID    `json:"dependent_chunk_ids,omitempty"`
	ReportedCounts    knowledge.ChunkCounts  `json:"reported_counts,omitzero"`
}

func (b ChunkDeletionBlockers) Empty() bool {
	return len(b.EntryIDs) == 0 && len(b.LinkIDs) == 0 && len(b.EvidenceIDs) == 0 && len(b.DependencyIDs) == 0 &&
		len(b.DependentChunkIDs) == 0 && b.ReportedCounts == (knowledge.ChunkCounts{})
}

// DeriveChunkDeletionBlockers computes canonical delete dependencies from one consistent
// snapshot. Evidence is included only when it exists and no retained entry or link uses it.
func DeriveChunkDeletionBlockers(target knowledge.Chunk, chunks []knowledge.Chunk, entries []knowledge.Entry, links []knowledge.Link, evidence []knowledge.Evidence) ChunkDeletionBlockers {
	blockers := ChunkDeletionBlockers{
		DependencyIDs: slices.Clone(target.DependencyIDs), ReportedCounts: target.Counts,
	}
	ownedEntries := make(map[knowledge.EntryID]struct{})
	for _, entry := range entries {
		if entry.ChunkID == target.ID {
			ownedEntries[entry.ID] = struct{}{}
			blockers.EntryIDs = append(blockers.EntryIDs, entry.ID)
		}
	}
	touchingLinks := make(map[knowledge.LinkID]struct{})
	for _, link := range links {
		if linkTouchesChunk(link, target.ID, ownedEntries) {
			touchingLinks[link.ID] = struct{}{}
			blockers.LinkIDs = append(blockers.LinkIDs, link.ID)
		}
	}
	candidateEvidence := make(map[knowledge.EvidenceID]struct{})
	retainedEvidence := make(map[knowledge.EvidenceID]struct{})
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

func addEvidenceIDs(destination map[knowledge.EvidenceID]struct{}, values []knowledge.EvidenceID) {
	for _, value := range values {
		destination[value] = struct{}{}
	}
}

func linkTouchesChunk(link knowledge.Link, chunkID knowledge.ChunkID, entryIDs map[knowledge.EntryID]struct{}) bool {
	for _, endpoint := range []knowledge.ObjectRef{link.Source, link.Target} {
		if endpoint.Kind == knowledge.ObjectKindChunk && endpoint.ID == string(chunkID) {
			return true
		}
		if endpoint.Kind == knowledge.ObjectKindEntry {
			if _, exists := entryIDs[knowledge.EntryID(endpoint.ID)]; exists {
				return true
			}
		}
	}
	return false
}
