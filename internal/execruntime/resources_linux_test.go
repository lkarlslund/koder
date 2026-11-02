//go:build linux

package execruntime

import (
	"os"
	"testing"
	"time"

	"github.com/prometheus/procfs"
)

func TestProcessTreeUsageIncludesReapedChildrenAndDescendants(t *testing.T) {
	stats := map[int]procfs.ProcStat{
		10: {PID: 10, UTime: 100, STime: 50, CUTime: 200, CSTime: 25, RSS: 2},
		11: {PID: 11, PPID: 10, UTime: 300, RSS: 3},
		12: {PID: 12, PPID: 11, UTime: 100, RSS: 4},
		99: {PID: 99, UTime: 99999, RSS: 999},
	}
	got := collectProcessTree(10, stats, map[int][]int{10: {11}, 11: {12}})
	if got.Processes != 3 || got.CPUSeconds != 7.75 || got.RSSBytes != uint64(9*os.Getpagesize()) {
		t.Fatalf("tree usage=%#v", got)
	}
}

func TestLiveResourcesSampleCurrentProcess(t *testing.T) {
	snap := Snapshot{PID: os.Getpid(), State: StateRunning, Elapsed: time.Second}
	missing := Snapshot{PID: -1, State: StateRunning}
	sampleResources([]*Snapshot{&snap, &missing})
	if snap.Resources == nil || snap.Resources.Processes < 1 || snap.Resources.RSSBytes == 0 {
		t.Fatalf("live usage=%#v", snap.Resources)
	}
	if missing.Resources != nil {
		t.Fatal("invented unavailable metrics")
	}
}
