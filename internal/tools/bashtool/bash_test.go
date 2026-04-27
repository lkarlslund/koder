package bashtool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/tools"
)

func TestNormalizeArgsValidatesCommandAndTimeout(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty command error")
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"command": "echo hi", "timeout_ms": "abc"}); err == nil {
		t.Fatal("expected timeout validation error")
	}
	got, err := (tool{}).NormalizeArgs(map[string]string{"cmd": "echo hi", "cwd": "./sub"})
	if err != nil {
		t.Fatal(err)
	}
	if got["command"] != "echo hi" || got["workdir"] != "sub" {
		t.Fatalf("unexpected normalized args: %#v", got)
	}
}

func TestExecuteRunsCommandAndCapturesMetadata(t *testing.T) {
	dir := t.TempDir()
	result, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{"command": "printf ok", "timeout_ms": "500"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Output) != "ok" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if result.Meta["timeout_ms"] != "500" || result.Meta["workdir"] != "." {
		t.Fatalf("unexpected metadata: %#v", result.Meta)
	}
}
