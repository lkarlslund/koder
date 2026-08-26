package pebble

import (
	"context"
	"math"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var _ memoryStoreAPI.OperationalDetailsStore = (*Store)(nil)

func (s *Store) OperationalDetails(ctx context.Context) (memoryStoreAPI.OperationalDetails, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.OperationalDetails{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.OperationalDetails{}, memoryStoreAPI.ErrClosed
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
	return memoryStoreAPI.OperationalDetails{
		Storage: memoryStoreAPI.StorageDetails{
			PhysicalBytes: metrics.DiskSpaceUsage(), LiveBytes: liveBytes, ReclaimableBytes: reclaimable,
			MemoryBytes: metrics.MemTable.Size + metrics.MemTable.ZombieSize,
			WALBytes:    metrics.WAL.PhysicalSize + metrics.WAL.ObsoletePhysicalSize, TableFiles: tableFiles,
		},
		Compaction: memoryStoreAPI.CompactionDetails{
			State: state, Count: metrics.Compact.Count, InProgress: metrics.Compact.NumInProgress,
			PendingBytes: metrics.Compact.EstimatedDebt, ReadAmplification: metrics.ReadAmp(),
			WriteAmplification: writeAmplification, MaxLevelScore: maxScore,
		},
	}, ctx.Err()
}

func classifyCompactionState(liveBytes, pendingBytes uint64, inProgress int64, maxScore float64) memoryStoreAPI.CompactionState {
	switch {
	case pendingBytes > max(uint64(64<<20), liveBytes), maxScore >= 2:
		return memoryStoreAPI.CompactionStateBacklog
	case inProgress > 0 || pendingBytes > 0 || maxScore >= 1:
		return memoryStoreAPI.CompactionStateCompacting
	default:
		return memoryStoreAPI.CompactionStateIdle
	}
}
