// Package fsutil holds small filesystem helpers shared across koder.
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WriteFileAtomic replaces path with data. It writes and syncs a temporary
// file in the same directory, then renames it over path, so readers see
// either the old or the new content, never a partial write.
func WriteFileAtomic(path string, data []byte, mode fs.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// Within reports whether path is root or lies inside it. The comparison is
// lexical: both paths are cleaned, but symlinks are not resolved.
func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

// CleanAbs returns path as a cleaned absolute path, or "" for a blank path.
// If the working directory cannot be determined, it returns path cleaned.
func CleanAbs(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
