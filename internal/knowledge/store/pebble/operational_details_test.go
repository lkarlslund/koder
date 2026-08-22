package pebble

import (
	"context"
	"errors"
	"math"
	"testing"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestOperationalDetailsReportsSanitizedStorageAndCompactionMetrics(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(tx knowledgeStore.WriteTx) error {
		return tx.PutChunk(context.Background(), txChunk(1), 0)
	}); err != nil {
		t.Fatal(err)
	}
	details, err := store.OperationalDetails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if details.Storage.PhysicalBytes == 0 || details.Storage.WALBytes == 0 {
		t.Fatalf("storage details = %#v", details.Storage)
	}
	if details.Compaction.State != "idle" && details.Compaction.State != "compacting" && details.Compaction.State != "backlog" {
		t.Fatalf("compaction state = %q", details.Compaction.State)
	}
	if math.IsNaN(details.Compaction.WriteAmplification) || math.IsInf(details.Compaction.WriteAmplification, 0) ||
		math.IsNaN(details.Compaction.MaxLevelScore) || math.IsInf(details.Compaction.MaxLevelScore, 0) {
		t.Fatalf("compaction details contain non-JSON float: %#v", details.Compaction)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.OperationalDetails(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("OperationalDetails(canceled) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OperationalDetails(context.Background()); !errors.Is(err, knowledgeStore.ErrClosed) {
		t.Fatalf("OperationalDetails(closed) error = %v, want ErrClosed", err)
	}
}

func TestCompactionStateSeparatesNormalWorkFromBacklog(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		live, pending uint64
		inProgress    int64
		score         float64
		want          knowledgeStore.CompactionState
	}{
		{name: "idle", want: knowledgeStore.CompactionStateIdle},
		{name: "active", inProgress: 1, want: knowledgeStore.CompactionStateCompacting},
		{name: "routine debt", live: 128 << 20, pending: 1, want: knowledgeStore.CompactionStateCompacting},
		{name: "score", score: 1, want: knowledgeStore.CompactionStateCompacting},
		{name: "debt backlog", live: 64 << 20, pending: 65 << 20, want: knowledgeStore.CompactionStateBacklog},
		{name: "score backlog", score: 2, want: knowledgeStore.CompactionStateBacklog},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyCompactionState(test.live, test.pending, test.inProgress, test.score); got != test.want {
				t.Fatalf("classifyCompactionState() = %q, want %q", got, test.want)
			}
		})
	}
}
