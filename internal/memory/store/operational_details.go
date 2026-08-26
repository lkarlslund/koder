package store

import "context"

type CompactionState string

const (
	CompactionStateIdle       CompactionState = "idle"
	CompactionStateCompacting CompactionState = "compacting"
	CompactionStateBacklog    CompactionState = "backlog"
)

type StorageDetails struct {
	PhysicalBytes    uint64 `json:"physical_bytes"`
	LiveBytes        uint64 `json:"live_bytes"`
	ReclaimableBytes uint64 `json:"reclaimable_bytes"`
	MemoryBytes      uint64 `json:"memory_bytes"`
	WALBytes         uint64 `json:"wal_bytes"`
	TableFiles       int64  `json:"table_files"`
}

type CompactionDetails struct {
	State              CompactionState `json:"state"`
	Count              int64           `json:"count"`
	InProgress         int64           `json:"in_progress"`
	PendingBytes       uint64          `json:"pending_bytes"`
	ReadAmplification  int             `json:"read_amplification"`
	WriteAmplification float64         `json:"write_amplification"`
	MaxLevelScore      float64         `json:"max_level_score"`
}

type OperationalDetails struct {
	Storage    StorageDetails    `json:"storage"`
	Compaction CompactionDetails `json:"compaction"`
}

// OperationalDetailsStore exposes sanitized backend metrics. It must not include paths,
// keys, record content, queries, or other canonical Memory values.
type OperationalDetailsStore interface {
	OperationalDetails(context.Context) (OperationalDetails, error)
}
