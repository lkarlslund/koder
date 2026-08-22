package store

import "github.com/lkarlslund/koder/internal/knowledge"

// ChunkDeletionBlockers is an authoritative transaction-snapshot view of records that
// would be orphaned by deleting a chunk. ReportedCounts is included because derived
// metadata must be repaired before destructive work if it disagrees with canonical data.
type ChunkDeletionBlockers struct {
	EntryIDs          []knowledge.EntryID   `json:"entry_ids,omitempty"`
	LinkIDs           []knowledge.LinkID    `json:"link_ids,omitempty"`
	DependencyIDs     []knowledge.ChunkID   `json:"dependency_ids,omitempty"`
	DependentChunkIDs []knowledge.ChunkID   `json:"dependent_chunk_ids,omitempty"`
	ReportedCounts    knowledge.ChunkCounts `json:"reported_counts,omitzero"`
}

func (b ChunkDeletionBlockers) Empty() bool {
	return len(b.EntryIDs) == 0 && len(b.LinkIDs) == 0 && len(b.DependencyIDs) == 0 &&
		len(b.DependentChunkIDs) == 0 && b.ReportedCounts == (knowledge.ChunkCounts{})
}
