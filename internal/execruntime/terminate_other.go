//go:build !linux

package execruntime

func terminateDescendants(int) func() { return func() {} }
