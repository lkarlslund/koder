package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

//go:embed defaults/**
var defaultsFS embed.FS

// UserDefaults returns the embedded defaults managed in ~/.koder.
func UserDefaults() ([]Asset, error) {
	var out []Asset
	err := fs.WalkDir(defaultsFS, "defaults", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := defaultsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded default %s: %w", path, err)
		}
		target := strings.TrimPrefix(filepath.ToSlash(path), "defaults/")
		out = append(out, Asset{
			ID:      target,
			Target:  target,
			Content: content,
			Mode:    0o644,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortAssets(out)
	return out, nil
}
