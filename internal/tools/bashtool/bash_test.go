package bashtool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/tools"
)

func TestNormalizeArgsValidatesCommandAndTimeout(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty command error")
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"command": "echo hi", "timeout_ms": "abc"}); err == nil {
		t.Fatal("expected timeout validation error")
	}
	got, err := (tool{}).NormalizeArgs(map[string]string{"cmd": "echo hi", "workdir": "./sub"})
	if err != nil {
		t.Fatal(err)
	}
	if got["command"] != "echo hi" || got["workdir"] != "sub" {
		t.Fatalf("unexpected normalized args: %#v", got)
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"command": "echo hi", "cwd": "./sub"}); err == nil {
		t.Fatal("expected cwd compatibility error")
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

func TestExecuteTimeoutKillsBackgroundChildHoldingOutputPipe(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	_, err := tool{}.Execute(context.Background(), tools.Runtime{Workdir: dir}, tools.Request{
		Args: map[string]string{
			"command":    "sleep 5 &",
			"timeout_ms": "100",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %s; background child likely kept output pipe open", elapsed)
	}
}
