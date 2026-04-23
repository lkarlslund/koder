//go:build windows

package tea

import (
	"os"

	"golang.org/x/sys/windows"
)

func enableVirtualTerminalIO(in *os.File, out *os.File) (func(), error) {
	inHandle := windows.Handle(in.Fd())
	outHandle := windows.Handle(out.Fd())

	var inMode uint32
	if err := windows.GetConsoleMode(inHandle, &inMode); err != nil {
		return nil, err
	}
	var outMode uint32
	if err := windows.GetConsoleMode(outHandle, &outMode); err != nil {
		return nil, err
	}

	nextInMode := inMode | windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	nextOutMode := outMode | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if err := windows.SetConsoleMode(inHandle, nextInMode); err != nil {
		return nil, err
	}
	if err := windows.SetConsoleMode(outHandle, nextOutMode); err != nil {
		_ = windows.SetConsoleMode(inHandle, inMode)
		return nil, err
	}

	return func() {
		_ = windows.SetConsoleMode(inHandle, inMode)
		_ = windows.SetConsoleMode(outHandle, outMode)
	}, nil
}
