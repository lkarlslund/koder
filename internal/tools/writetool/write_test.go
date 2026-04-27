package writetool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkarlslund/koder/internal/tools"
)

func TestNormalizeArgsValidatesPath(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty path error")
	}
}

func TestExecuteCreatesAndOverwritesFile(t *testing.T) {
	dir := t.TempDir()
	req := tools.Request{Args: map[string]string{"path": "notes.txt", "content": "hello\n"}}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["action"] != "created" {
		t.Fatalf("expected created action, got %#v", result.Meta)
	}
	body, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello\n" {
		t.Fatalf("unexpected file contents: %q", string(body))
	}

	req.Args["content"] = "updated\n"
	result, err = tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["action"] != "overwrote" {
		t.Fatalf("expected overwrite action, got %#v", result.Meta)
	}
}
