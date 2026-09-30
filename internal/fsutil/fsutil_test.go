package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicReplacesContentAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("content = %q, err = %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected no leftover temp files, got %v (err %v)", entries, err)
	}
}

func TestWriteFileAtomicFailsWithoutDirectory(t *testing.T) {
	if err := WriteFileAtomic(filepath.Join(t.TempDir(), "missing", "file"), []byte("x"), 0o644); err == nil {
		t.Fatal("expected error for missing parent directory")
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/a", "/a", true},
		{"/a", "/a/b/c", true},
		{"/a", "/a/..b", true},
		{"/a", "/ab", false},
		{"/a", "/a/../c", false},
		{"/a/b", "/a", false},
		{"/a", "relative", false},
	}
	for _, tc := range cases {
		if got := Within(tc.root, tc.path); got != tc.want {
			t.Errorf("Within(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}
