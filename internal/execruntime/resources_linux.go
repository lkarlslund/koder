//go:build linux

package execruntime

import "github.com/prometheus/procfs"

// Read /proc once per list/status request, never on each output event.
// Reaped-child counters retain CPU consumed by children that have exited.
// RSS sums live processes and can count shared pages more than once.
func sampleResources(snaps []*Snapshot) {
	needed := false
	for _, snap := range snaps {
		if snap.State == StateRunning && snap.PID > 0 {
			needed = true
			break
		}
	}
	if !needed {
		return
	}
	procs, err := procfs.AllProcs()
	if err != nil {
		return
	}
	stats := make(map[int]procfs.ProcStat, len(procs))
	children := make(map[int][]int)
	for _, proc := range procs {
		stat, err := proc.Stat()
		if err != nil {
			continue
		}
		stats[stat.PID] = stat
		children[stat.PPID] = append(children[stat.PPID], stat.PID)
	}
	for _, snap := range snaps {
		if snap.State != StateRunning || snap.PID <= 0 {
			continue
		}
		if _, ok := stats[snap.PID]; !ok {
			continue
		}
		usage := collectProcessTree(snap.PID, stats, children)
		if snap.Elapsed > 0 {
			usage.CPUPercent = usage.CPUSeconds / snap.Elapsed.Seconds() * 100
		}
		snap.Resources = &usage
	}
}

func collectProcessTree(pid int, stats map[int]procfs.ProcStat, children map[int][]int) ResourceUsage {
	var usage ResourceUsage
	pending := []int{pid}
	seen := make(map[int]bool)
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[current] {
			continue
		}
		seen[current] = true
		stat, ok := stats[current]
		if !ok {
			continue
		}
		usage.Processes++
		// procfs's Linux CPUTime method converts USER_HZ ticks to seconds.
		unit := procfs.ProcStat{UTime: 1}
		ticks := float64(stat.UTime) + float64(stat.STime) + float64(max(0, stat.CUTime)) + float64(max(0, stat.CSTime))
		usage.CPUSeconds += ticks * unit.CPUTime()
		usage.RSSBytes += uint64(max(0, stat.ResidentMemory()))
		pending = append(pending, children[current]...)
	}
	return usage
}
