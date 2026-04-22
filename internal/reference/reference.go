package reference

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/tools"
)

type Kind string

const (
	KindFile      Kind = "file"
	KindDirectory Kind = "directory"
)

type Draft struct {
	Kind    Kind
	Path    string
	Display string
	Start   int
	End     int
}

type Metadata struct {
	Kind    Kind   `json:"kind"`
	Path    string `json:"path"`
	Display string `json:"display"`
	Start   int    `json:"start,omitempty"`
	End     int    `json:"end,omitempty"`
}

type Entry struct {
	Kind        Kind
	Path        string
	Display     string
	Description string
}

func EncodeMeta(meta Metadata) (string, error) {
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal reference metadata: %w", err)
	}
	return string(raw), nil
}

func DecodeMeta(raw string) (Metadata, error) {
	if strings.TrimSpace(raw) == "" {
		return Metadata{}, fmt.Errorf("reference metadata is empty")
	}
	var meta Metadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return Metadata{}, fmt.Errorf("decode reference metadata: %w", err)
	}
	meta.Path = NormalizePath(meta.Path)
	if strings.TrimSpace(meta.Path) == "" || meta.Kind == "" {
		return Metadata{}, fmt.Errorf("reference metadata is incomplete")
	}
	if strings.TrimSpace(meta.Display) == "" {
		meta.Display = DisplayToken(meta.Path)
	}
	return meta, nil
}

func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	path = filepath.Clean(filepath.FromSlash(path))
	if path == "." {
		return ""
	}
	return filepath.ToSlash(path)
}

func DisplayToken(path string) string {
	path = NormalizePath(path)
	if path == "" {
		return "@"
	}
	if strings.ContainsAny(path, " \t") {
		return `@"` + path + `"`
	}
	return "@" + path
}

func Entries(workdir string) ([]Entry, error) {
	var entries []Entry
	err := filepath.WalkDir(workdir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == workdir {
			return nil
		}
		name := d.Name()
		if d.IsDir() && name == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(workdir, path)
		if err != nil {
			return nil
		}
		rel = NormalizePath(rel)
		if rel == "" {
			return nil
		}
		kind := KindFile
		desc := "file"
		display := rel
		if d.IsDir() {
			kind = KindDirectory
			desc = "directory"
			display += "/"
		}
		entries = append(entries, Entry{
			Kind:        kind,
			Path:        rel,
			Display:     display,
			Description: desc,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.Kind != b.Kind {
			if a.Kind == KindDirectory {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	return entries, nil
}

func Search(entries []Entry, query string, limit int) []Entry {
	query = strings.ToLower(NormalizePath(query))
	if limit <= 0 {
		limit = 12
	}
	type scored struct {
		entry  Entry
		score  int
		length int
	}
	out := make([]scored, 0, len(entries))
	for _, entry := range entries {
		score, ok := matchScore(query, entry)
		if !ok {
			continue
		}
		out = append(out, scored{entry: entry, score: score, length: len(entry.Path)})
	}
	slices.SortFunc(out, func(a, b scored) int {
		if a.score != b.score {
			return a.score - b.score
		}
		if a.length != b.length {
			return a.length - b.length
		}
		return strings.Compare(a.entry.Path, b.entry.Path)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	result := make([]Entry, 0, len(out))
	for _, item := range out {
		result = append(result, item.entry)
	}
	return result
}

func PathCompletions(workdir, query string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 12
	}
	baseDir, namePrefix, err := completionBase(workdir, query)
	if err != nil {
		return nil, nil
	}
	items, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, nil
	}
	matches := make([]Entry, 0, len(items))
	for _, item := range items {
		name := item.Name()
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(namePrefix)) {
			continue
		}
		full := filepath.Join(baseDir, name)
		rel, err := filepath.Rel(workdir, full)
		if err != nil {
			continue
		}
		rel = NormalizePath(rel)
		if rel == "" || strings.HasPrefix(rel, "../") {
			continue
		}
		entry := Entry{
			Kind:        KindFile,
			Path:        rel,
			Display:     rel,
			Description: "file",
		}
		if item.IsDir() {
			entry.Kind = KindDirectory
			entry.Display += "/"
			entry.Description = "directory"
		}
		matches = append(matches, entry)
	}
	slices.SortFunc(matches, func(a, b Entry) int {
		if a.Kind != b.Kind {
			if a.Kind == KindDirectory {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func ResolveFile(workdir string, meta Metadata) (string, error) {
	abs, rel, err := tools.ReadablePath(workdir, meta.Path)
	if err != nil {
		return "", err
	}
	text, truncated, err := tools.ReadTextFile(abs, tools.DefaultReadLineLimit, tools.DefaultReadByteLimit)
	if err != nil {
		return "", err
	}
	if truncated {
		text = strings.TrimSpace(text)
	}
	return "Referenced file " + rel + ":\n" + text, nil
}

func ResolveDirectory(workdir string, meta Metadata) (string, error) {
	abs, rel, err := tools.ReadablePath(workdir, meta.Path)
	if err != nil {
		return "", err
	}
	items, err := tools.ListDirectory(abs)
	if err != nil {
		return "", err
	}
	body := strings.Join(items, "\n")
	body, _ = tools.TruncateText(body, tools.DefaultToolOutputLimit)
	return "Referenced directory " + rel + ":\n" + body, nil
}

func matchScore(query string, entry Entry) (int, bool) {
	if query == "" {
		if entry.Kind == KindDirectory {
			return 0, true
		}
		return 1, true
	}
	path := strings.ToLower(entry.Path)
	base := strings.ToLower(filepath.Base(entry.Path))
	switch {
	case path == query:
		return 0, true
	case strings.TrimSuffix(path, "/") == strings.TrimSuffix(query, "/"):
		return 0, true
	case base == query:
		return 1, true
	case strings.HasPrefix(base, query):
		return 2, true
	case strings.HasPrefix(path, query):
		return 3, true
	case strings.Contains(base, query):
		return 4, true
	case strings.Contains(path, query):
		return 5, true
	case isSubsequence(query, path):
		return 6, true
	default:
		return 0, false
	}
}

func isSubsequence(query, target string) bool {
	if query == "" {
		return true
	}
	idx := 0
	for _, r := range target {
		if idx < len(query) && r == rune(query[idx]) {
			idx++
		}
	}
	return idx == len(query)
}

func completionBase(workdir, query string) (string, string, error) {
	query = filepath.ToSlash(strings.TrimSpace(query))
	switch {
	case strings.HasPrefix(query, "@/"):
		query = strings.TrimPrefix(query, "@")
	}
	query = strings.TrimPrefix(query, "./")
	query = strings.TrimPrefix(query, "/")
	clean := filepath.Clean(filepath.FromSlash(query))
	if clean == "." {
		clean = ""
	}
	dirPart := filepath.Dir(clean)
	if dirPart == "." {
		dirPart = ""
	}
	namePart := filepath.Base(clean)
	if strings.HasSuffix(query, "/") {
		dirPart = clean
		namePart = ""
	}
	baseDir := filepath.Join(workdir, dirPart)
	rel, err := filepath.Rel(workdir, baseDir)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "", fmt.Errorf("outside workspace")
	}
	return baseDir, namePart, nil
}
