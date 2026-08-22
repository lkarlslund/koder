package pebble

import (
	"context"
	"math"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var _ knowledgeStore.OperationalDetailsStore = (*Store)(nil)

func (s *Store) OperationalDetails(ctx context.Context) (knowledgeStore.OperationalDetails, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.OperationalDetails{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.OperationalDetails{}, knowledgeStore.ErrClosed
	}
	metrics := s.db.Metrics()
	var liveBytes uint64
	var tableFiles int64
	var maxScore float64
	for _, level := range metrics.Levels {
		if level.Size > 0 {
			liveBytes += uint64(level.Size)
		}
		tableFiles += level.NumFiles
		if !math.IsNaN(level.Score) && !math.IsInf(level.Score, 0) && level.Score > maxScore {
			maxScore = level.Score
		}
	}
	liveBytes += metrics.WAL.Size
	reclaimable := metrics.WAL.ObsoletePhysicalSize + metrics.Table.ObsoleteSize + metrics.Table.ZombieSize
	total := metrics.Total()
	writeAmplification := total.WriteAmp()
	if math.IsNaN(writeAmplification) || math.IsInf(writeAmplification, 0) {
		writeAmplification = 0
	}
	state := classifyCompactionState(liveBytes, metrics.Compact.EstimatedDebt, metrics.Compact.NumInProgress, maxScore)
	return knowledgeStore.OperationalDetails{
		Storage: knowledgeStore.StorageDetails{
			PhysicalBytes: metrics.DiskSpaceUsage(), LiveBytes: liveBytes, ReclaimableBytes: reclaimable,
			MemoryBytes: metrics.MemTable.Size + metrics.MemTable.ZombieSize,
			WALBytes:    metrics.WAL.PhysicalSize + metrics.WAL.ObsoletePhysicalSize, TableFiles: tableFiles,
		},
		Compaction: knowledgeStore.CompactionDetails{
			State: state, Count: metrics.Compact.Count, InProgress: metrics.Compact.NumInProgress,
			PendingBytes: metrics.Compact.EstimatedDebt, ReadAmplification: metrics.ReadAmp(),
			WriteAmplification: writeAmplification, MaxLevelScore: maxScore,
		},
	}, ctx.Err()
}

func classifyCompactionState(liveBytes, pendingBytes uint64, inProgress int64, maxScore float64) knowledgeStore.CompactionState {
	switch {
	case pendingBytes > max(uint64(64<<20), liveBytes), maxScore >= 2:
		return knowledgeStore.CompactionStateBacklog
	case inProgress > 0 || pendingBytes > 0 || maxScore >= 1:
		return knowledgeStore.CompactionStateCompacting
	default:
		return knowledgeStore.CompactionStateIdle
	}
}
