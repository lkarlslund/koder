//go:build !windows

package processgroup

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Configure starts cmd as the leader of a new process group.
func Configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// ConfigureContextCancel makes exec.CommandContext terminate the whole process group.
func ConfigureContextCancel(cmd *exec.Cmd, grace time.Duration) {
	Configure(cmd)
	if cmd == nil {
		return
	}
	if grace <= 0 {
		grace = time.Second
	}
	cmd.Cancel = func() error {
		err := Terminate(cmd)
		go func() {
			time.Sleep(grace)
			_ = Kill(cmd)
		}()
		return err
	}
	cmd.WaitDelay = grace
}

// Terminate sends SIGTERM to cmd's process group.
func Terminate(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGTERM)
}

// Kill sends SIGKILL to cmd's process group.
func Kill(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGKILL)
}

func signalGroup(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
