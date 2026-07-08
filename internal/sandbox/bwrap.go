package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
)

const envDisableNetUnshare = "KODER_BWRAP_DISABLE_NET_UNSHARE"

// Command describes an executable that should run inside a sandbox profile.
type Command struct {
	Executable string
	Args       []string
	Workdir    string
	Settings   accesssettings.Settings
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
	settings := accesssettings.Normalize(cmd.Settings)
	if err := accesssettings.Validate(settings); err != nil {
		return nil, err
	}
	args := []string{"--die-with-parent", "--new-session"}
	if !settings.Network && strings.TrimSpace(os.Getenv(envDisableNetUnshare)) == "" {
		args = append(args, "--unshare-net")
	}
	args = appendBind(args, settings.Root, "/", "/")
	if settings.Root != accesssettings.ModeNone {
		args = append(args, "--tmpfs", "/etc/ssh")
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc")
	args = appendTmp(args, settings)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		args = appendAccess(args, settings.Home, filepath.Clean(home))
	}
	args = appendAccess(args, settings.Project, workdir)
	for _, mount := range settings.Mounts {
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

func appendBind(args []string, mode accesssettings.Mode, src, dst string) []string {
	if mode == accesssettings.ModeNone {
		return args
	}
	if mode == accesssettings.ModeReadWrite {
		return append(args, "--bind", src, dst)
	}
	return append(args, "--ro-bind", src, dst)
}

func appendAccess(args []string, mode accesssettings.Mode, path string) []string {
	if mode == accesssettings.ModeNone {
		return append(args, "--tmpfs", path)
	}
	return appendBind(args, mode, path, path)
}

func appendTmp(args []string, settings accesssettings.Settings) []string {
	switch settings.Tmp {
	case accesssettings.TmpHost:
		return append(args, "--bind", "/tmp", "/tmp")
	case accesssettings.TmpSession:
		tmpDir := strings.TrimSpace(settings.TmpDir)
		if tmpDir != "" {
			return append(args, "--bind", filepath.Clean(tmpDir), "/tmp")
		}
	}
	return append(args, "--tmpfs", "/tmp")
}
