package webui

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/lkarlslund/koder/internal/id"
)

const (
	// A page keeps the text of the files in the folders it watches, as they
	// were when it started watching, so it can show what changed since.
	maxBaselineFileBytes  = 1 << 20
	maxBaselineTotalBytes = 32 << 20
)

// fileBaselines holds the text of files as they were when their folder was
// first watched, or when the page last cleared its change marks.
type fileBaselines struct {
	dirs    map[string]bool   // folders whose files were recorded
	content map[string]string // file -> text when recorded
	skipped map[string]bool   // present then, but binary, too large or over budget
	bytes   int
}

func (b *fileBaselines) record(full, rel string) {
	if b.dirs[rel] {
		return
	}
	if b.dirs == nil {
		b.dirs = map[string]bool{}
		b.content = map[string]string{}
		b.skipped = map[string]bool{}
	}
	b.dirs[rel] = true
	entries, err := os.ReadDir(full)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := path.Join(rel, entry.Name())
		info, err := entry.Info()
		if err != nil || info.Size() > maxBaselineFileBytes || b.bytes+int(info.Size()) > maxBaselineTotalBytes {
			b.skipped[name] = true
			continue
		}
		data, err := os.ReadFile(filepath.Join(full, entry.Name()))
		if err != nil || looksBinary(data) {
			b.skipped[name] = true
			continue
		}
		b.content[name] = string(data)
		b.bytes += len(data)
	}
}

// lookup returns a file's recorded text. Known is false when nothing was
// recorded for it; existed is false for a file in a recorded folder that
// was not there when it was recorded.
func (b *fileBaselines) lookup(rel string) (content string, known, existed bool) {
	if text, ok := b.content[rel]; ok {
		return text, true, true
	}
	if b.skipped[rel] {
		return "", false, true
	}
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	return "", b.dirs[parent], false
}

type fileBaselineParams struct {
	SessionID id.ID  `json:"session_id"`
	Path      string `json:"path"`
}

type fileBaselineResult struct {
	Path    string `json:"path"`
	Known   bool   `json:"known"`
	Existed bool   `json:"existed"`
	Content string `json:"content"`
}

func (w *fileWatch) handleBaseline(raw json.RawMessage) (any, error) {
	var params fileBaselineParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode file_baseline: %w", err)
	}
	rel, err := cleanFileBrowserPath(params.Path)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if params.SessionID != w.sessionID {
		return fileBaselineResult{Path: rel}, nil
	}
	content, known, existed := w.base.lookup(rel)
	return fileBaselineResult{Path: rel, Known: known, Existed: existed, Content: content}, nil
}

// resetBaselines records the watched folders again, as they are now.
func (w *fileWatch) resetBaselines() (any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.base = fileBaselines{}
	for full, rel := range w.dirs {
		w.base.record(full, rel)
	}
	return map[string]bool{"ok": true}, nil
}
