package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DefaultReadLineLimit   = 400
	DefaultReadByteLimit   = 64 * 1024
	DefaultToolOutputLimit = 64 * 1024
	DefaultBashTimeout     = 2 * time.Minute
	MaxBashTimeout         = 10 * time.Minute
)

func FirstArg(args map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(args[key]); value != "" {
			return value
		}
	}
	return ""
}

func NormalizeStringMap(args map[string]string) map[string]string {
	if len(args) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(args))
	for key, value := range args {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func NormalizePathInput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(raw))
}

func WorkspacePath(root string, raw string) (abs string, rel string, err error) {
	root, err = filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return "", "", errors.New("path is empty")
	}
	clean := NormalizePathInput(raw)
	if clean == "" {
		return "", "", errors.New("path is empty")
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
	rel, err = filepath.Rel(root, abs)
	if err != nil {
		return "", "", fmt.Errorf("resolve relative path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q is outside the workspace", raw)
	}
	if info, statErr := os.Stat(abs); statErr == nil {
		resolved, resolveErr := filepath.EvalSymlinks(abs)
		if resolveErr == nil {
			rel, err = filepath.Rel(root, resolved)
			if err != nil {
				return "", "", fmt.Errorf("resolve relative symlink path: %w", err)
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", "", fmt.Errorf("path %q resolves outside the workspace", raw)
			}
			abs = resolved
		} else if !errors.Is(resolveErr, fs.ErrNotExist) {
			return "", "", fmt.Errorf("resolve symlink: %w", resolveErr)
		}
		_ = info
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", fmt.Errorf("stat path: %w", statErr)
	}
	return abs, filepath.ToSlash(rel), nil
}

func ReadablePath(root string, raw string) (abs string, label string, err error) {
	root, err = filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return "", "", errors.New("path is empty")
	}
	clean := NormalizePathInput(raw)
	if clean == "" {
		return "", "", errors.New("path is empty")
	}
	if filepath.IsAbs(clean) {
		abs = filepath.Clean(clean)
		if info, statErr := os.Stat(abs); statErr == nil {
			resolved, resolveErr := filepath.EvalSymlinks(abs)
			if resolveErr == nil {
				abs = resolved
			} else if !errors.Is(resolveErr, fs.ErrNotExist) {
				return "", "", fmt.Errorf("resolve symlink: %w", resolveErr)
			}
			_ = info
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return "", "", fmt.Errorf("stat path: %w", statErr)
		}
		return abs, filepath.ToSlash(abs), nil
	}
	return WorkspacePath(root, clean)
}

func WorkspaceDir(root string, raw string) (abs string, rel string, err error) {
	if strings.TrimSpace(raw) == "" {
		abs, err = filepath.Abs(strings.TrimSpace(root))
		if err != nil {
			return "", "", fmt.Errorf("resolve workspace dir: %w", err)
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
		numbered = append(numbered, fmt.Sprintf("%6d\t%s", idx+1, line))
	}
	content, byteTruncated := TruncateText(strings.Join(numbered, "\n"), byteLimit)
	truncated = truncated || byteTruncated
	return content, truncated, nil
}

func SummarizePaths(paths []string, limit int) string {
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	if limit <= 0 || len(paths) <= limit {
		return strings.Join(paths, ", ")
	}
	return strings.Join(paths[:limit], ", ") + fmt.Sprintf(", +%d more", len(paths)-limit)
}

func ShellResult(ctx context.Context, dir string, timeout time.Duration, command string) (string, int, error) {
	if timeout <= 0 {
		timeout = DefaultBashTimeout
	}
	if timeout > MaxBashTimeout {
		timeout = MaxBashTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
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

func limitCopy(dst io.Writer, src io.Reader, n int64) (int64, error) {
	if n <= 0 {
		return io.Copy(dst, src)
	}
	return io.Copy(dst, io.LimitReader(src, n))
}

func BoolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func JSONMeta(values map[string]string) string {
	data, _ := json.Marshal(values)
	return string(data)
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
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	if _, err := io.WriteString(tmp, content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		cleanup()
		return err
	}
	return nil
}
