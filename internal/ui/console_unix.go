//go:build !windows

package ui

import "os"

func enableVirtualTerminalIO(_ *os.File, _ *os.File) (func(), error) {
	return func() {}, nil
}
