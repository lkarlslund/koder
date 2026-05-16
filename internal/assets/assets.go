package assets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const manifestName = "managed-assets.json"

// Status is the outcome for one managed asset sync.
type Status string

const (
	StatusInstalled Status = "installed"
	StatusUpdated   Status = "updated"
	StatusUnchanged Status = "unchanged"
	StatusModified  Status = "modified"
	StatusUnmanaged Status = "unmanaged"
)

// Asset is one embedded default file managed under a target root.
type Asset struct {
	ID      string
	Target  string
	Content []byte
	Mode    fs.FileMode
}

// Result describes the sync outcome for one asset.
type Result struct {
	ID     string
	Target string
	Status Status
}

type manifest struct {
	Version int                     `json:"version"`
	Files   map[string]manifestFile `json:"files"`
}

type manifestFile struct {
	AssetID   string    `json:"asset_id"`
	Target    string    `json:"target"`
	SHA256    string    `json:"sha256"`
	Mode      string    `json:"mode"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Sync installs or updates managed assets beneath root.
func Sync(ctx context.Context, root string, items []Asset) ([]Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("asset root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve asset root: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create asset root: %w", err)
	}
	m, err := readManifest(root)
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(items))
	changed := false
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, itemChanged, err := syncAsset(root, &m, item)
		if err != nil {
			return nil, err
		}
		changed = changed || itemChanged
		results = append(results, result)
	}
	if changed {
		if err := writeManifest(root, m); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func syncAsset(root string, m *manifest, item Asset) (Result, bool, error) {
	if strings.TrimSpace(item.ID) == "" {
		return Result{}, false, fmt.Errorf("asset id is required")
	}
	target, rel, err := resolveTarget(root, item.Target)
	if err != nil {
		return Result{}, false, err
	}
	mode := item.Mode
	if mode == 0 {
		mode = 0o644
	}
	result := Result{ID: item.ID, Target: rel}
	embeddedHash := contentHash(item.Content)
	entry, hasEntry := m.Files[rel]
	current, readErr := os.ReadFile(target)
	switch {
	case errors.Is(readErr, os.ErrNotExist):
		if err := writeAssetFile(target, item.Content, mode); err != nil {
			return Result{}, false, err
		}
		m.Files[rel] = manifestEntry(item, rel, embeddedHash, mode)
		result.Status = StatusInstalled
		return result, true, nil
	case readErr != nil:
		return Result{}, false, fmt.Errorf("read managed asset %s: %w", target, readErr)
	case !hasEntry:
		result.Status = StatusUnmanaged
		return result, false, nil
	}
	currentHash := contentHash(current)
	if currentHash != entry.SHA256 {
		result.Status = StatusModified
		return result, false, nil
	}
	if currentHash == embeddedHash {
		result.Status = StatusUnchanged
		return result, false, nil
	}
	if err := writeAssetFile(target, item.Content, mode); err != nil {
		return Result{}, false, err
	}
	m.Files[rel] = manifestEntry(item, rel, embeddedHash, mode)
	result.Status = StatusUpdated
	return result, true, nil
}

func readManifest(root string) (manifest, error) {
	m := manifest{Version: 1, Files: map[string]manifestFile{}}
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return manifest{}, fmt.Errorf("read asset manifest: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, fmt.Errorf("decode asset manifest: %w", err)
	}
	if m.Version == 0 {
		m.Version = 1
	}
	if m.Files == nil {
		m.Files = map[string]manifestFile{}
	}
	return m, nil
}

func writeManifest(root string, m manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode asset manifest: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(filepath.Join(root, manifestName), data, 0o644)
}

func resolveTarget(root string, target string) (string, string, error) {
	target = filepath.Clean(strings.TrimSpace(target))
	if target == "." || target == "" || filepath.IsAbs(target) || strings.HasPrefix(target, ".."+string(filepath.Separator)) || target == ".." {
		return "", "", fmt.Errorf("invalid managed asset target %q", target)
	}
	abs := filepath.Join(root, target)
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", "", fmt.Errorf("managed asset target escapes root: %q", target)
	}
	return abs, filepath.ToSlash(rel), nil
}

func writeAssetFile(path string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create managed asset directory: %w", err)
	}
	if err := atomicWrite(path, content, mode); err != nil {
		return fmt.Errorf("write managed asset %s: %w", path, err)
	}
	return nil
}

func atomicWrite(path string, content []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".koder-managed-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func manifestEntry(item Asset, target string, hash string, mode fs.FileMode) manifestFile {
	return manifestFile{
		AssetID:   item.ID,
		Target:    target,
		SHA256:    hash,
		Mode:      fmt.Sprintf("%04o", mode.Perm()),
		UpdatedAt: time.Now().UTC(),
	}
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func sortAssets(items []Asset) {
	sort.SliceStable(items, func(i, j int) bool {
		return strings.Compare(items[i].Target, items[j].Target) < 0
	})
}
