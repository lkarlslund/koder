package webui

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Obsidian links a note by name, [[Note]] or ![[image.png]], wherever the
// file sits in the vault. The file browser resolves those names on the server
// so the preview can open them.

var wikilinkPattern = regexp.MustCompile(`!?\[\[([^\]\|#\^\n]*)[^\]\n]*\]\]`)

const (
	wikilinkIndexLimit = 20000
	wikilinkIndexTTL   = 5 * time.Second
)

type wikilinkIndex struct {
	built time.Time
	// byName maps a lower-cased file name, and for notes also the name
	// without ".md", to the vault files carrying it.
	byName map[string][]string
	// paths are the vault files, relative to the vault root.
	paths []string
}

var wikilinkIndexes = struct {
	sync.Mutex
	byRoot map[string]*wikilinkIndex
}{byRoot: map[string]*wikilinkIndex{}}

// resolveWikilinks maps each link target in content to the project-relative
// path of the file it names, for the note at rel under projectRoot. Targets
// that name no file are left out.
func resolveWikilinks(projectRoot, rel, content string) map[string]string {
	matches := wikilinkPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	vault := wikilinkVaultRoot(projectRoot, rel)
	index := loadWikilinkIndex(filepath.Join(projectRoot, filepath.FromSlash(vault)))
	noteRel := rel
	if vault != "" {
		noteRel = strings.TrimPrefix(rel, vault+"/")
	}
	noteDir := path.Dir(noteRel)
	out := map[string]string{}
	for _, match := range matches {
		target := strings.TrimSpace(match[1])
		if target == "" {
			continue
		}
		if _, done := out[target]; done {
			continue
		}
		if found := index.resolve(target, noteDir); found != "" {
			out[target] = path.Join(vault, found)
		}
	}
	return out
}

// wikilinkVaultRoot is the nearest folder holding .obsidian above the note,
// or the project root when there is none. It is relative to projectRoot.
func wikilinkVaultRoot(projectRoot, rel string) string {
	dir, _ := obsidianConfigDir(projectRoot, rel)
	return dir
}

// obsidianConfigDir finds the nearest folder holding .obsidian above the
// note; found is false, with dir "", when there is none.
func obsidianConfigDir(projectRoot, rel string) (dir string, found bool) {
	for dir := path.Dir(rel); ; dir = path.Dir(dir) {
		if dir == "." {
			dir = ""
		}
		if info, err := os.Stat(filepath.Join(projectRoot, filepath.FromSlash(dir), ".obsidian")); err == nil && info.IsDir() {
			return dir, true
		}
		if dir == "" {
			return "", false
		}
	}
}

// obsidianNote says how a Markdown note renders in Obsidian.
type obsidianNote struct {
	// StrictLineBreaks joins single line breaks into one paragraph, as
	// standard Markdown does. Obsidian keeps them by default.
	StrictLineBreaks bool `json:"strict_line_breaks"`
}

// detectObsidianNote returns nil for plain Markdown. A note is an Obsidian
// note when a .obsidian folder sits above it, whose app.json then gives its
// settings, or, for a vault without one, when the note or another note in
// its folder or a parent folder below the project root uses [[wikilinks]].
func detectObsidianNote(projectRoot, rel, content string) *obsidianNote {
	if vault, found := obsidianConfigDir(projectRoot, rel); found {
		note := &obsidianNote{}
		if data, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(vault), ".obsidian", "app.json")); err == nil {
			var settings struct {
				StrictLineBreaks bool `json:"strictLineBreaks"`
			}
			if json.Unmarshal(data, &settings) == nil {
				note.StrictLineBreaks = settings.StrictLineBreaks
			}
		}
		return note
	}
	if strings.Contains(content, "[[") {
		return &obsidianNote{}
	}
	for dir := path.Dir(rel); dir != "." && dir != ""; dir = path.Dir(dir) {
		if folderHasWikilinkNotes(filepath.Join(projectRoot, filepath.FromSlash(dir))) {
			return &obsidianNote{}
		}
	}
	return nil
}

const (
	wikilinkScanFiles = 200
	wikilinkScanBytes = 64 << 10
)

var wikilinkFolders = struct {
	sync.Mutex
	byDir map[string]wikilinkFolder
}{byDir: map[string]wikilinkFolder{}}

type wikilinkFolder struct {
	checked time.Time
	links   bool
}

// folderHasWikilinkNotes reports whether a Markdown file directly in dir
// holds a [[wikilink]], reading at most the first 64 KB of 200 files.
func folderHasWikilinkNotes(dir string) bool {
	wikilinkFolders.Lock()
	defer wikilinkFolders.Unlock()
	if known, ok := wikilinkFolders.byDir[dir]; ok && time.Since(known.checked) < wikilinkIndexTTL {
		return known.links
	}
	links := false
	entries, _ := os.ReadDir(dir)
	scanned := 0
	for _, entry := range entries {
		if links || scanned >= wikilinkScanFiles {
			break
		}
		if entry.IsDir() || !isMarkdownFile(entry.Name()) {
			continue
		}
		scanned++
		file, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		head := make([]byte, wikilinkScanBytes)
		n, _ := io.ReadFull(file, head)
		_ = file.Close()
		links = bytes.Contains(head[:n], []byte("[["))
	}
	wikilinkFolders.byDir[dir] = wikilinkFolder{checked: time.Now(), links: links}
	return links
}

func loadWikilinkIndex(root string) *wikilinkIndex {
	wikilinkIndexes.Lock()
	defer wikilinkIndexes.Unlock()
	if index := wikilinkIndexes.byRoot[root]; index != nil && time.Since(index.built) < wikilinkIndexTTL {
		return index
	}
	index := &wikilinkIndex{built: time.Now(), byName: map[string][]string{}}
	_ = filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if full != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(index.paths) >= wikilinkIndexLimit {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		index.paths = append(index.paths, rel)
		lower := strings.ToLower(name)
		index.byName[lower] = append(index.byName[lower], rel)
		if note, ok := strings.CutSuffix(lower, ".md"); ok {
			index.byName[note] = append(index.byName[note], rel)
		}
		return nil
	})
	wikilinkIndexes.byRoot[root] = index
	return index
}

// resolve finds the file a link target names. A target with a folder must
// match the end of the file's path; a bare name matches anywhere, and the
// file nearest the linking note wins, as in Obsidian.
func (x *wikilinkIndex) resolve(target, noteDir string) string {
	target = strings.TrimPrefix(strings.ReplaceAll(target, "\\", "/"), "/")
	lower := strings.ToLower(target)
	var candidates []string
	if strings.Contains(lower, "/") {
		for _, p := range x.paths {
			pl := strings.ToLower(p)
			for _, want := range []string{lower, lower + ".md"} {
				if pl == want || strings.HasSuffix(pl, "/"+want) {
					candidates = append(candidates, p)
				}
			}
		}
	} else {
		candidates = x.byName[lower]
	}
	if len(candidates) == 0 {
		return ""
	}
	return slices.MinFunc(candidates, func(a, b string) int {
		if d := cmp.Compare(wikilinkDistance(noteDir, a), wikilinkDistance(noteDir, b)); d != 0 {
			return d
		}
		if d := cmp.Compare(len(a), len(b)); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
}

// wikilinkDistance counts the folder steps from noteDir to the file.
func wikilinkDistance(noteDir, file string) int {
	from := splitWikilinkDir(noteDir)
	to := splitWikilinkDir(path.Dir(file))
	shared := 0
	for shared < len(from) && shared < len(to) && from[shared] == to[shared] {
		shared++
	}
	return len(from) - shared + len(to) - shared
}

func splitWikilinkDir(dir string) []string {
	if dir == "." || dir == "" {
		return nil
	}
	return strings.Split(dir, "/")
}
