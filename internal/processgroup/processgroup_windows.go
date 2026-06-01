//go:build windows

package processgroup

import (
	"os"
	"os/exec"
	"time"
)

func Configure(_ *exec.Cmd) {}

func ConfigureContextCancel(cmd *exec.Cmd, grace time.Duration) {
	if cmd == nil {
		return
	}
	if grace <= 0 {
		grace = time.Second
	}
	cmd.Cancel = func() error {
		return Kill(cmd)
	}
	cmd.WaitDelay = grace
}

func Terminate(cmd *exec.Cmd) error {
	return Kill(cmd)
}

func Kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
