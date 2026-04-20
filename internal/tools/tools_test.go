package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

func TestReadAndPatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(dir)
	readResult, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{"path": "file.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if readResult.Output != "before" {
		t.Fatalf("unexpected read result: %q", readResult.Output)
	}
	patchResult, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindApplyPatch,
		Args: map[string]string{"path": "file.txt", "content": "after"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if patchResult.DiffText == "" {
		t.Fatal("expected diff text")
	}
}
