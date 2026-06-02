package exectool

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestCommandNormalizeArgs(t *testing.T) {
	args, err := (commandTool{}).NormalizeArgs(map[string]string{
		"cmd":           "sleep 1",
		"workdir":       "./sub",
		"tty":           "true",
		"yield_time_ms": "250",
	})
	if err != nil {
		t.Fatalf("normalize args: %v", err)
	}
	if args["cmd"] != "sleep 1" || args["workdir"] != "sub" || args["tty"] != "true" || args["yield_time_ms"] != "250" {
		t.Fatalf("unexpected normalized args: %#v", args)
	}
	if _, err := (commandTool{}).NormalizeArgs(map[string]string{"cmd": "pwd", "dir": "sub"}); err == nil {
		t.Fatal("expected dir compatibility error")
	}
}

func TestWriteStdinAllowsEmptyCharsForWait(t *testing.T) {
	args, err := (writeStdinTool{}).NormalizeArgs(map[string]string{"process_id": "exec_1", "chars": "", "yield_time_ms": "1000"})
	if err != nil {
		t.Fatalf("normalize args: %v", err)
	}
	if args["process_id"] != "exec_1" || args["chars"] != "" || args["yield_time_ms"] != "1000" {
		t.Fatalf("unexpected normalized args: %#v", args)
	}
	if preview := (writeStdinTool{}).Preview(tools.Request{Args: map[string]string{"process_id": "exec_1"}}); !strings.Contains(preview, "Wait for output") {
		t.Fatalf("expected wait preview, got %q", preview)
	}
}

func TestExecStartMessageDistinguishesRunningProcess(t *testing.T) {
	running := execStartMessage(execruntime.Snapshot{State: execruntime.StateRunning})
	if running == "" || !containsAll(running, "still running", "empty chars", "exec_status", "exec_write_stdin", "exec_terminate") {
		t.Fatalf("expected running guidance, got %q", running)
	}
	completed := execStartMessage(execruntime.Snapshot{State: execruntime.StateCompleted})
	if completed == "" || !containsAll(completed, "completed", "grace period") {
		t.Fatalf("expected completed grace-period message, got %q", completed)
	}
}

func TestStoredFromSnapshotMarksOutputMode(t *testing.T) {
	drained := storedFromSnapshot(execruntime.Snapshot{State: execruntime.StateRunning, Drained: true}, "waited")
	if drained.OutputMode != "incremental" {
		t.Fatalf("expected incremental output mode, got %q", drained.OutputMode)
	}
	tail := storedFromSnapshot(execruntime.Snapshot{State: execruntime.StateRunning}, "status")
	if tail.OutputMode != "tail" {
		t.Fatalf("expected tail output mode, got %q", tail.OutputMode)
	}
}

func containsAll(text string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}
