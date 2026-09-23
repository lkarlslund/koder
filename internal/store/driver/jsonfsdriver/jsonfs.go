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
	mu   sync.RWMutex
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
	b.mu.RLock()
	defer b.mu.RUnlock()
	path := filepath.Join(b.root, "collections", namespace, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("get %s %s: %w", namespace, id, err)
	}
	return data, nil
}

func (b *Backend) Put(ctx context.Context, namespace string, id string, data []byte, indexes map[string]driver.IndexValue) error {
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
	for name, index := range indexes {
		indexDir := b.indexDir(namespace, name, index.Value)
		if err := driver.EnsureDir(indexDir); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(indexDir, driver.IndexCursor(index.Order, id)), nil, 0o644); err != nil {
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
	b.mu.RLock()
	defer b.mu.RUnlock()
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

func (b *Backend) ListIndexPage(ctx context.Context, namespace string, req driver.IndexPageRequest) (driver.IndexPage, error) {
	if err := driver.EnsureContext(ctx); err != nil {
		return driver.IndexPage{}, err
	}
	if req.Limit <= 0 {
		return driver.IndexPage{}, fmt.Errorf("list %s index page: positive limit is required", namespace)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	entries, err := sortedIndexEntryPaths(b.indexDir(namespace, req.Name, req.Value))
	if err != nil {
		return driver.IndexPage{}, err
	}
	total := len(entries)
	start, end := 0, total
	if req.After != "" {
		start = sort.Search(total, func(i int) bool { return filepath.Base(entries[i]) > req.After })
	}
	if req.Before != "" {
		end = sort.Search(total, func(i int) bool { return filepath.Base(entries[i]) >= req.Before })
	}
	if start > end {
		start = end
	}
	if req.Tail && end-start > req.Limit {
		start = end - req.Limit
	} else if !req.Tail && end-start > req.Limit {
		end = start + req.Limit
	}
	page := driver.IndexPage{
		Items:     make([][]byte, 0, end-start),
		Total:     total,
		HasBefore: start > 0,
		HasAfter:  end < total,
	}
	for _, indexPath := range entries[start:end] {
		if err := driver.EnsureContext(ctx); err != nil {
			return driver.IndexPage{}, err
		}
		itemID := driver.IDFromIndexCursor(filepath.Base(indexPath))
		data, err := os.ReadFile(filepath.Join(b.root, "collections", namespace, itemID+".json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return driver.IndexPage{}, err
		}
		page.Items = append(page.Items, data)
	}
	return page, nil
}

func (b *Backend) AddIndexEntries(ctx context.Context, namespace, name, value string, entries []driver.OrderedIndexEntry) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	dir := b.indexDir(namespace, name, value)
	if err := driver.EnsureDir(dir); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := driver.EnsureContext(ctx); err != nil {
			return err
		}
		if entry.ID == "" {
			continue
		}
		path := filepath.Join(dir, driver.IndexCursor(entry.Order, entry.ID))
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return fmt.Errorf("build index %s %s: %w", namespace, entry.ID, err)
		}
	}
	return nil
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
		id := driver.IDFromIndexCursor(filepath.Base(path))
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
			valueDir := filepath.Join(nameDir, valueEntry.Name())
			indexEntries, err := os.ReadDir(valueDir)
			if err != nil {
				return fmt.Errorf("read index %s %s: %w", namespace, nameEntry.Name(), err)
			}
			for _, indexEntry := range indexEntries {
				if indexEntry.IsDir() || driver.IDFromIndexCursor(indexEntry.Name()) != id {
					continue
				}
				if err := os.Remove(filepath.Join(valueDir, indexEntry.Name())); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("delete index %s %s: %w", namespace, id, err)
				}
			}
		}
	}
	return nil
}
