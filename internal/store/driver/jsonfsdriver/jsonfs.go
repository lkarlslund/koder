package jsonfsdriver

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/lkarlslund/koder/internal/store/driver"
)

const Name = "jsonfs"

type Backend struct {
	root string
	mu   sync.Mutex
}

func Open(stateDir string) (*Backend, error) {
	root := filepath.Join(stateDir, "store-jsonfs-v7")
	if reset, err := needsSchemaReset(root); err != nil {
		return nil, err
	} else if reset {
		if err := os.RemoveAll(root); err != nil {
			return nil, fmt.Errorf("reset jsonfs store: %w", err)
		}
	}
	if err := driver.EnsureDir(filepath.Join(root, "collections")); err != nil {
		return nil, fmt.Errorf("create jsonfs store dir: %w", err)
	}
	b := &Backend{root: root}
	if err := b.init(); err != nil {
		return nil, err
	}
	return b, nil
}

func needsSchemaReset(root string) (bool, error) {
	metaPath := filepath.Join(root, "meta.json")
	if !driver.FileExists(metaPath) {
		return false, nil
	}
	var meta driver.Meta
	if err := driver.ReadJSONFile(metaPath, &meta); err != nil {
		return false, fmt.Errorf("read jsonfs metadata before schema check: %w", err)
	}
	return meta.SchemaVersion != driver.SchemaVersion || meta.Encoding != driver.EncodingJSON || meta.Backend != Name, nil
}

func (b *Backend) init() error {
	metaPath := filepath.Join(b.root, "meta.json")
	if driver.FileExists(metaPath) {
		return nil
	}
	return driver.WriteJSONFile(metaPath, driver.DefaultMeta(Name))
}

func (b *Backend) Close() error { return nil }

func (b *Backend) Get(ctx context.Context, namespace string, id string) ([]byte, error) {
	if err := driver.EnsureContext(ctx); err != nil {
		return nil, err
	}
	path := filepath.Join(b.root, "collections", namespace, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("get %s %s: %w", namespace, id, err)
	}
	return data, nil
}

func (b *Backend) Put(ctx context.Context, namespace string, id string, data []byte, indexes map[string]string) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	dir := filepath.Join(b.root, "collections", namespace)
	if err := driver.EnsureDir(dir); err != nil {
		return err
	}
	if err := b.deleteIndexEntries(namespace, id); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("put %s %s: %w", namespace, id, err)
	}
	for name, value := range indexes {
		indexDir := b.indexDir(namespace, name, value)
		if err := driver.EnsureDir(indexDir); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(indexDir, id), nil, 0o644); err != nil {
			return fmt.Errorf("index %s %s: %w", namespace, id, err)
		}
	}
	return nil
}

func (b *Backend) Delete(ctx context.Context, namespace string, id string) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.deleteIndexEntries(namespace, id); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(b.root, "collections", namespace, id+".json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s %s: %w", namespace, id, err)
	}
	return nil
}

func (b *Backend) List(ctx context.Context, namespace string, lookup *driver.IndexLookup) ([][]byte, error) {
	if err := driver.EnsureContext(ctx); err != nil {
		return nil, err
	}
	paths, err := b.listRecordPaths(namespace, lookup)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, data)
	}
	return out, nil
}

func (b *Backend) listRecordPaths(namespace string, lookup *driver.IndexLookup) ([]string, error) {
	if lookup == nil {
		return driver.SortedJSONPaths(filepath.Join(b.root, "collections", namespace))
	}
	indexPaths, err := sortedIndexEntryPaths(b.indexDir(namespace, lookup.Name, lookup.Value))
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(indexPaths))
	for _, path := range indexPaths {
		id := filepath.Base(path)
		paths = append(paths, filepath.Join(b.root, "collections", namespace, id+".json"))
	}
	return paths, nil
}

func sortedIndexEntryPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func (b *Backend) indexDir(namespace, name, value string) string {
	return filepath.Join(b.root, "indexes", escapePathSegment(namespace), escapePathSegment(name), escapePathSegment(value))
}

func escapePathSegment(value string) string {
	return url.PathEscape(value)
}

func (b *Backend) deleteIndexEntries(namespace, id string) error {
	root := filepath.Join(b.root, "indexes", escapePathSegment(namespace))
	nameEntries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read index namespace %s: %w", namespace, err)
	}
	for _, nameEntry := range nameEntries {
		if !nameEntry.IsDir() {
			continue
		}
		nameDir := filepath.Join(root, nameEntry.Name())
		valueEntries, err := os.ReadDir(nameDir)
		if err != nil {
			return fmt.Errorf("read index %s %s: %w", namespace, nameEntry.Name(), err)
		}
		for _, valueEntry := range valueEntries {
			if !valueEntry.IsDir() {
				continue
			}
			path := filepath.Join(nameDir, valueEntry.Name(), id)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("delete index %s %s: %w", namespace, id, err)
			}
		}
	}
	return nil
}
