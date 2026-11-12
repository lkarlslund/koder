package webui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileBaselinesRecordFolderText(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"docs/note.md":     "first",
		"docs/image.bin":   "a\x00b",
		"docs/deeper/x.md": "not recorded: folder not watched",
		"top.md":           "not recorded: folder not watched",
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var b fileBaselines
	b.record(filepath.Join(root, "docs"), "docs")
	if err := os.WriteFile(filepath.Join(root, "docs", "note.md"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	b.record(filepath.Join(root, "docs"), "docs") // already recorded: keeps the first text
	cases := []struct {
		path    string
		content string
		known   bool
	}{
		{"docs/note.md", "first", true},
		{"docs/later.md", "", true}, // created after recording: it was empty then
		{"docs/image.bin", "", false},
		{"docs/deeper/x.md", "", false},
		{"top.md", "", false},
	}
	for _, c := range cases {
		content, known := b.lookup(c.path)
		if content != c.content || known != c.known {
			t.Errorf("lookup(%q) = %q, %v; want %q, %v", c.path, content, known, c.content, c.known)
		}
	}
}
