package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/processgroup"
	"github.com/lkarlslund/koder/internal/sandbox"
)

const (
	DefaultReadLineLimit       = 1000
	DefaultReadByteLimit       = 64 * 1024
	DefaultReadOutputCharLimit = 100000
	DefaultToolOutputLimit     = 64 * 1024
	DefaultBashTimeout         = 5 * time.Minute
	MaxBashTimeout             = 10 * time.Minute
)

func FirstArg(args map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(args[key]); value != "" {
			return value
		}
	}
	return ""
}

func NormalizePathInput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(raw))
}

func WorkspacePath(root string, raw string) (abs string, rel string, err error) {
	root, err = workspaceRoot(root)
	if err != nil {
		return "", "", err
	}
	clean, err := cleanPathArg(raw)
	if err != nil {
		return "", "", err
	}
	if clean == "." {
		return root, ".", nil
	}
	if filepath.IsAbs(clean) {
		abs = clean
	} else {
		abs = filepath.Join(root, clean)
	}
	abs = filepath.Clean(abs)
	rel, err = workspaceRel(root, abs, raw, "path")
	if err != nil {
		return "", "", err
	}
	resolved, exists, err := resolveExistingPath(abs)
	if err != nil {
		return "", "", err
	}
	if exists {
		rel, err = workspaceRel(root, resolved, raw, "path")
		if err != nil {
			return "", "", err
		}
		abs = resolved
	}
	return abs, filepath.ToSlash(rel), nil
}

func ReadablePath(root string, raw string) (abs string, label string, err error) {
	root, err = workspaceRoot(root)
	if err != nil {
		return "", "", err
	}
	clean, err := cleanPathArg(raw)
	if err != nil {
		return "", "", err
	}
	if filepath.IsAbs(clean) {
		abs = filepath.Clean(clean)
		resolved, exists, err := resolveExistingPath(abs)
		if err != nil {
			return "", "", err
		}
		if exists {
			abs = resolved
		}
		return abs, filepath.ToSlash(abs), nil
	}
	return WorkspacePath(root, clean)
}

func WritablePath(runtime Runtime, raw string) (abs string, label string, err error) {
	root, err := workspaceRoot(runtime.Workdir)
	if err != nil {
		return "", "", err
	}
	clean, err := cleanPathArg(raw)
	if err != nil {
		return "", "", err
	}
	if filepath.IsAbs(clean) {
		abs = filepath.Clean(clean)
	} else {
		abs = filepath.Clean(filepath.Join(root, clean))
	}
	if err := runtime.CheckPathAccess(accesssettings.AccessWrite, abs); err != nil {
		return "", "", err
	}
	if rel, err := filepath.Rel(root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs, filepath.ToSlash(rel), nil
	}
	return abs, filepath.ToSlash(abs), nil
}

func WorkspaceDir(root string, raw string) (abs string, rel string, err error) {
	if strings.TrimSpace(raw) == "" {
		abs, err = workspaceRoot(root)
		if err != nil {
			return "", "", err
		}
		return abs, ".", nil
	}
	abs, rel, err = WorkspacePath(root, raw)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%q is not a directory", rel)
	}
	return abs, rel, nil
}

func TruncateText(input string, limit int) (string, bool) {
	if limit <= 0 || len(input) <= limit {
		return input, false
	}
	suffix := fmt.Sprintf("\n... truncated to %d bytes ...", limit)
	trimmed := limit - len(suffix)
	if trimmed < 0 {
		trimmed = 0
	}
	return input[:trimmed] + suffix, true
}

func ReadTextFile(abs string, lineLimit int, byteLimit int) (string, bool, error) {
	buf, err := os.ReadFile(abs)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(string(buf), "\n")
	truncated := false
	if lineLimit > 0 && len(lines) > lineLimit {
		lines = lines[:lineLimit]
		truncated = true
	}
	numbered := make([]string, 0, len(lines))
	for idx, line := range lines {
		numbered = append(numbered, fmt.Sprintf("%d: %s", idx+1, line))
	}
	content, byteTruncated := TruncateText(strings.Join(numbered, "\n"), byteLimit)
	truncated = truncated || byteTruncated
	return content, truncated, nil
}

func SummarizePaths(paths []string, limit int) string {
	if len(paths) == 0 {
		return ""
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	if limit <= 0 || len(sorted) <= limit {
		return strings.Join(sorted, ", ")
	}
	return strings.Join(sorted[:limit], ", ") + fmt.Sprintf(", +%d more", len(sorted)-limit)
}

func ShellResult(ctx context.Context, dir string, timeout time.Duration, command string, settings accesssettings.Settings) (string, int, error) {
	if timeout <= 0 {
		timeout = DefaultBashTimeout
	}
	if timeout > MaxBashTimeout {
		timeout = MaxBashTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executable, args, err := sandbox.WrapCommand(sandbox.Command{
		Executable: "bash",
		Args:       []string{"-lc", command},
		Workdir:    dir,
		Settings:   settings,
	})
	if err != nil {
		return "", -1, err
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	processgroup.ConfigureContextCancel(cmd, 500*time.Millisecond)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		text, _ := TruncateText(string(output), DefaultToolOutputLimit)
		return text, -1, fmt.Errorf("command timed out after %s", timeout)
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return "", -1, err
		}
	}
	text, _ := TruncateText(string(output), DefaultToolOutputLimit)
	return text, exitCode, err
}

func ListDirectory(abs string) ([]string, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, item := range entries {
		name := item.Name()
		if item.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func BoolString(value bool) string {
	return strconv.FormatBool(value)
}

func WriteTextFile(abs string, content string, mode fs.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".koder-write-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()
	closeTemp := func() {
		_ = tmp.Close()
	}
	if _, err := io.WriteString(tmp, content); err != nil {
		closeTemp()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		closeTemp()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		return err
	}
	renamed = true
	return nil
}

func workspaceRoot(root string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	return filepath.Clean(abs), nil
}

func cleanPathArg(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path is empty")
	}
	clean := NormalizePathInput(raw)
	if clean == "" {
		return "", errors.New("path is empty")
	}
	return clean, nil
}

func workspaceRel(root string, abs string, raw string, noun string) (string, error) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("resolve relative %s: %w", noun, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace", raw)
	}
	return rel, nil
}

func resolveExistingPath(abs string) (resolved string, exists bool, err error) {
	if _, statErr := os.Stat(abs); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return abs, false, nil
		}
		return "", false, fmt.Errorf("stat path: %w", statErr)
	}
	resolved, err = filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return abs, false, nil
		}
		return "", false, fmt.Errorf("resolve symlink: %w", err)
	}
	return resolved, true, nil
}
