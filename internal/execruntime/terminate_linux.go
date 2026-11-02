//go:build linux

package execruntime

import (
	"github.com/prometheus/procfs"
	"syscall"
)

// Sandboxes can create a new session/process group beneath their launcher.
// Signal their descendants before the launcher exits and they are reparented.
func terminateDescendants(pid int) func() {
	procs, err := procfs.AllProcs()
	if err != nil {
		return func() {}
	}
	children := map[int][]procfs.ProcStat{}
	for _, proc := range procs {
		if stat, err := proc.Stat(); err == nil {
			children[stat.PPID] = append(children[stat.PPID], stat)
		}
	}
	var descendants []procfs.ProcStat
	var collect func(int)
	collect = func(parent int) {
		for _, child := range children[parent] {
			collect(child.PID)
			descendants = append(descendants, child)
		}
	}
	collect(pid)
	signal := func(sig syscall.Signal) {
		for _, child := range descendants {
			proc, err := procfs.NewProc(child.PID)
			if err != nil {
				continue
			}
			current, err := proc.Stat()
			if err == nil && current.Starttime == child.Starttime {
				_ = syscall.Kill(child.PID, sig)
			}
		}
	}
	signal(syscall.SIGTERM)
	return func() { signal(syscall.SIGKILL) }
}
