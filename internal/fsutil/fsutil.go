// Package fsutil holds small filesystem helpers shared across koder.
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
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
