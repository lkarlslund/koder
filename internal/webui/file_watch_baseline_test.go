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
		path           string
		content        string
		known, existed bool
	}{
		{"docs/note.md", "first", true, true},
		{"docs/later.md", "", true, false}, // created after recording
		{"docs/image.bin", "", false, true},
		{"docs/deeper/x.md", "", false, false},
		{"top.md", "", false, false},
	}
	for _, c := range cases {
		content, known, existed := b.lookup(c.path)
		if content != c.content || known != c.known || existed != c.existed {
			t.Errorf("lookup(%q) = %q, %v, %v; want %q, %v, %v", c.path, content, known, existed, c.content, c.known, c.existed)
		}
	}
}
