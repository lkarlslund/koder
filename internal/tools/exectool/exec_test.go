package exectool

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/execruntime"
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

func TestWriteStdinRequiresAction(t *testing.T) {
	if _, err := (writeStdinTool{}).NormalizeArgs(map[string]string{"process_id": "exec_1"}); err == nil {
		t.Fatal("expected missing chars/close_stdin error")
	}
}

func TestExecStartMessageDistinguishesRunningProcess(t *testing.T) {
	running := execStartMessage(execruntime.Snapshot{State: execruntime.StateRunning})
	if running == "" || !containsAll(running, "still running", "exec_status", "exec_write_stdin", "exec_terminate") {
		t.Fatalf("expected running guidance, got %q", running)
	}
	completed := execStartMessage(execruntime.Snapshot{State: execruntime.StateCompleted})
	if completed == "" || !containsAll(completed, "completed", "grace period") {
		t.Fatalf("expected completed grace-period message, got %q", completed)
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
