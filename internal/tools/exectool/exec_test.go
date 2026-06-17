package exectool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestCommandSpecGuidesMinimalExecutableCommand(t *testing.T) {
	spec := tools.Info(domain.ToolKindExecCommand)
	text := strings.Join([]string{spec.Description, spec.Usage, spec.Parameters}, "\n")
	for _, want := range []string{"executable-only", "do not include reasoning", "explanatory comments", "comment"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected exec_command spec to contain %q, got:\n%s", want, text)
		}
	}
}

func TestCommandNormalizeArgs(t *testing.T) {
	args, err := (commandTool{}).NormalizeArgs(map[string]string{
		"cmd":           "sleep 1",
		"comment":       "  run a short sleep\nfor testing  ",
		"workdir":       "./sub",
		"tty":           "true",
		"yield_time_ms": "250",
	})
	if err != nil {
		t.Fatalf("normalize args: %v", err)
	}
	if args["cmd"] != "sleep 1" || args["comment"] != "run a short sleep for testing" || args["workdir"] != "sub" || args["tty"] != "true" || args["yield_time_ms"] != "250" {
		t.Fatalf("unexpected normalized args: %#v", args)
	}
}

func TestCommandExecuteDefaultsToSessionProjectRoot(t *testing.T) {
	root := t.TempDir()
	control := &recordingExecControl{}
	result, err := (commandTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		Workdir:   root,
		SessionID: "session-1",
		ChatID:    "chat-1",
		Exec:      control,
	}, Request: tools.Request{
		Args: map[string]string{"cmd": "pwd", "comment": "show current directory"},
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if control.start.Workdir != root {
		t.Fatalf("expected session project root workdir %q, got %q", root, control.start.Workdir)
	}
	if result.Meta["workdir"] != "." {
		t.Fatalf("expected relative workdir metadata '.', got %#v", result.Meta)
	}
	stored, ok := result.Stored.(tools.ExecStoredResult)
	if !ok {
		t.Fatalf("expected exec stored result, got %T", result.Stored)
	}
	if stored.Workdir != "." {
		t.Fatalf("expected stored relative workdir '.', got %q", stored.Workdir)
	}
	if stored.Comment != "show current directory" || result.Meta["comment"] != "show current directory" {
		t.Fatalf("expected stored comment metadata, stored=%#v meta=%#v", stored, result.Meta)
	}
}

func TestCommandExecuteResolvesWorkdirRelativeToSessionProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	control := &recordingExecControl{}
	_, err := (commandTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		Workdir:   root,
		SessionID: "session-1",
		ChatID:    "chat-1",
		Exec:      control,
	}, Request: tools.Request{
		Args: map[string]string{"cmd": "pwd", "workdir": "sub"},
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := filepath.Join(root, "sub"); control.start.Workdir != want {
		t.Fatalf("expected session-relative workdir %q, got %q", want, control.start.Workdir)
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

func TestYieldTimeRejectsZero(t *testing.T) {
	for name, tool := range map[string]interface {
		NormalizeArgs(map[string]string) (map[string]string, error)
	}{
		"command":     commandTool{},
		"write_stdin": writeStdinTool{},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tool.NormalizeArgs(map[string]string{
				"cmd":           "sleep 1",
				"process_id":    "exec_1",
				"yield_time_ms": "0",
			})
			if err == nil || !strings.Contains(err.Error(), "positive integer") {
				t.Fatalf("expected positive integer error, got %v", err)
			}
		})
	}
}

func TestWriteStdinExecuteDefaultsToTenSecondYield(t *testing.T) {
	control := &recordingExecControl{}
	_, err := (writeStdinTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		SessionID: "session-1",
		ChatID:    "chat-1",
		Exec:      control,
	}, Request: tools.Request{
		Args: map[string]string{"process_id": "exec_1", "chars": ""},
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if control.writeStdin.YieldTime != 10*time.Second {
		t.Fatalf("expected 10s default yield, got %s", control.writeStdin.YieldTime)
	}
}

func TestWriteStdinExecuteDoesNotAllowZeroYield(t *testing.T) {
	control := &recordingExecControl{}
	_, err := (writeStdinTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		SessionID: "session-1",
		ChatID:    "chat-1",
		Exec:      control,
	}, Request: tools.Request{
		Args: map[string]string{"process_id": "exec_1", "chars": "", "yield_time_ms": "0"},
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if control.writeStdin.YieldTime != 10*time.Second {
		t.Fatalf("expected zero yield to fall back to 10s, got %s", control.writeStdin.YieldTime)
	}
}

type recordingExecControl struct {
	start      execruntime.StartRequest
	writeStdin execruntime.WriteStdinRequest
}

func (c *recordingExecControl) Start(_ context.Context, req execruntime.StartRequest) (execruntime.Snapshot, error) {
	c.start = req
	if req.Workdir == "" {
		return execruntime.Snapshot{}, errors.New("workdir is empty")
	}
	return execruntime.Snapshot{
		ProcessID: "exec_1",
		SessionID: req.SessionID,
		ChatID:    req.ChatID,
		Command:   req.Command,
		Workdir:   req.Workdir,
		State:     execruntime.StateCompleted,
	}, nil
}

func (c *recordingExecControl) Status(context.Context, execruntime.StatusRequest) (execruntime.Snapshot, error) {
	return execruntime.Snapshot{}, errors.New("not implemented")
}

func (c *recordingExecControl) List(context.Context, execruntime.ListRequest) ([]execruntime.Snapshot, error) {
	return nil, errors.New("not implemented")
}

func (c *recordingExecControl) WriteStdin(_ context.Context, req execruntime.WriteStdinRequest) (execruntime.Snapshot, error) {
	c.writeStdin = req
	return execruntime.Snapshot{
		ProcessID: req.ProcessID,
		SessionID: req.SessionID,
		ChatID:    req.ChatID,
		State:     execruntime.StateRunning,
		Drained:   true,
	}, nil
}

func (c *recordingExecControl) Resize(context.Context, execruntime.ResizeRequest) (execruntime.Snapshot, error) {
	return execruntime.Snapshot{}, errors.New("not implemented")
}

func (c *recordingExecControl) Terminate(context.Context, execruntime.TerminateRequest) (execruntime.Snapshot, error) {
	return execruntime.Snapshot{}, errors.New("not implemented")
}

func (c *recordingExecControl) Cleanup(context.Context, execruntime.CleanupRequest) ([]execruntime.Snapshot, error) {
	return nil, errors.New("not implemented")
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
