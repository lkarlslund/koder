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
	"github.com/lkarlslund/koder/internal/fsutil"
)

const envDisableNetUnshare = "KODER_BWRAP_DISABLE_NET_UNSHARE"

// Command describes an executable that should run inside a sandbox profile.
type Command struct {
	Executable string
	Args       []string
	Workdir    string
	Settings   accesssettings.Settings
	// Binds expose host paths at other paths inside the sandbox, read-write.
	// They are applied after every grant, so they win over a grant that
	// covers their target.
	Binds []Bind
}

// Bind maps a host path to a different path inside the sandbox.
type Bind struct {
	Source string
	Target string
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
	args := []string{"--die-with-parent", "--new-session", "--unshare-pid"}
	if !settings.Network && strings.TrimSpace(os.Getenv(envDisableNetUnshare)) == "" {
		args = append(args, "--unshare-net")
	}
	args = appendBind(args, settings.Root, "/", "/")
	if settings.Root != accesssettings.ModeNone {
		args = append(args, "--tmpfs", "/etc/ssh")
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc")
	args = appendTmp(args, settings)
	// Later binds cover earlier ones, so grants go shallowest first and a
	// deeper grant (a project inside a read-only shared folder) wins.
	for _, grant := range accesssettings.Grants(settings, workdir) {
		if grant.Mode == accesssettings.ModeDevice {
			args = append(args, "--dev-bind", grant.Path, grant.Path)
		} else {
			args = appendAccess(args, grant.Mode, grant.Path)
		}
	}
	for _, bind := range cmd.Binds {
		if !filepath.IsAbs(bind.Source) || !filepath.IsAbs(bind.Target) {
			return nil, fmt.Errorf("sandbox bind %q -> %q must use absolute paths", bind.Source, bind.Target)
		}
		args = append(args, "--bind", filepath.Clean(bind.Source), filepath.Clean(bind.Target))
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

// ResolveExecutable finds executable for a sandboxed agent process. When the
// sandbox hides the home directory and the executable lives there (for
// example under a Node version manager), it also returns the directory that
// must be mounted read-only so the executable and its runtime stay reachable.
func ResolveExecutable(executable string, settings accesssettings.Settings) (string, string, error) {
	path, err := exec.LookPath(strings.TrimSpace(executable))
	if err != nil {
		return "", "", fmt.Errorf("find executable %q: %w", executable, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve executable %q: %w", executable, err)
	}
	path = filepath.Clean(path)
	home, homeErr := os.UserHomeDir()
	if homeErr != nil || settings.Home != accesssettings.ModeNone || !fsutil.Within(home, path) {
		return path, "", nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve executable symlink %q: %w", path, err)
	}
	mount := commonAncestor(filepath.Dir(path), filepath.Dir(resolved))
	if mount == "" || !fsutil.Within(home, mount) {
		mount = filepath.Dir(path)
	}
	return path, mount, nil
}

func commonAncestor(first, second string) string {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	for !fsutil.Within(first, second) {
		parent := filepath.Dir(first)
		if parent == first {
			return ""
		}
		first = parent
	}
	return first
}
