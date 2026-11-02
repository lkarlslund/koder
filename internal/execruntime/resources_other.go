//go:build !linux

package execruntime

// Live process-tree accounting is currently available on Linux only.
// Final CPU time still comes from os.ProcessState on supported platforms.
func sampleResources([]*Snapshot) {}
