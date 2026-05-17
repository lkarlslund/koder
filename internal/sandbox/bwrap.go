package sandbox

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lkarlslund/koder/internal/permissionprofile"
)

// Command describes an executable that should run inside a sandbox profile.
type Command struct {
	Executable string
	Args       []string
	Workdir    string
	Profile    permissionprofile.Profile
}

// WrapCommand returns an executable and arguments that run cmd through bwrap.
func WrapCommand(cmd Command) (string, []string, error) {
	if runtime.GOOS != "linux" {
		return "", nil, fmt.Errorf("bwrap sandboxing is only supported on linux, not %s", runtime.GOOS)
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return "", nil, errors.New("bwrap is required for sandboxed shell commands")
	}
	args, err := Args(cmd)
	if err != nil {
		return "", nil, err
	}
	return bwrap, args, nil
}

// Args builds the bubblewrap arguments for cmd.
func Args(cmd Command) ([]string, error) {
	executable := strings.TrimSpace(cmd.Executable)
	if executable == "" {
		return nil, errors.New("sandbox executable is required")
	}
	workdir, err := filepath.Abs(strings.TrimSpace(cmd.Workdir))
	if err != nil || workdir == "" {
		return nil, errors.New("sandbox workdir is required")
	}
	profile := permissionprofile.Normalize(cmd.Profile)
	if err := permissionprofile.ValidateSandbox(profile); err != nil {
		return nil, err
	}
	args := []string{"--die-with-parent", "--new-session"}
	if !profile.Network {
		args = append(args, "--unshare-net")
	}
	args = appendBind(args, permissionprofile.MountMode(profile.Root), "/", "/")
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp")
	args = appendBind(args, permissionprofile.MountMode(profile.Workspace), workdir, workdir)
	for _, mount := range profile.Mounts {
		path, err := filepath.Abs(strings.TrimSpace(mount.Path))
		if err != nil || path == "" {
			return nil, fmt.Errorf("invalid mount path %q", mount.Path)
		}
		args = appendBind(args, mount.Mode, filepath.Clean(path), filepath.Clean(path))
	}
	args = append(args, "--chdir", workdir, "--", executable)
	args = append(args, cmd.Args...)
	return args, nil
}

func appendBind(args []string, mode permissionprofile.MountMode, src, dst string) []string {
	if mode == permissionprofile.ModeReadWrite {
		return append(args, "--bind", src, dst)
	}
	return append(args, "--ro-bind", src, dst)
}
