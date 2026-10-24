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
	for _, want := range []string{"executable-only", "do not include reasoning", "explanatory comments", "comment", "output-settle", "Defaults to 10000"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected exec_command spec to contain %q, got:\n%s", want, text)
		}
	}
}

func TestExecSessionDefinitionAndWaitDispatch(t *testing.T) {
	if _, enabled := tools.DefinitionFor(tools.ExecCommand, tools.Runtime{}); !enabled {
		t.Fatal("exec_command starter is not model-visible")
	}
	def, enabled := tools.DefinitionFor(tools.ExecSession, tools.Runtime{})
	if !enabled {
		t.Fatal("exec_session definition is disabled")
	}
	params := string(def.Function.Parameters)
	for _, action := range []string{"list", "status", "wait", "send_input", "resize", "terminate", "cleanup"} {
		if !strings.Contains(params, `"`+action+`"`) {
			t.Fatalf("exec_session schema is missing %q: %s", action, params)
		}
	}
	for _, legacy := range []tools.ID{tools.ExecStatus, tools.ExecList, tools.ExecWriteStdin, tools.ExecResize, tools.ExecTerminate, tools.ExecCleanup} {
		if _, enabled := tools.DefinitionFor(legacy, tools.Runtime{}); enabled {
			t.Fatalf("legacy %s remained model-visible", legacy)
		}
	}
	control := &recordingExecControl{}
	_, err := tools.Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		SessionID: "session-1", ChatID: "chat-1", Exec: control,
	}, Request: tools.Request{Tool: tools.ExecSession, Args: map[string]string{
		"action": "wait", "process_id": "exec_1", "yield_time_ms": "25",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if control.writeStdin.ProcessID != "exec_1" || control.writeStdin.YieldTime != 25*time.Millisecond || control.writeStdin.Chars != "" {
		t.Fatalf("wait request = %#v", control.writeStdin)
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
	if control.start.YieldTime != 10*time.Second {
		t.Fatalf("expected 10s default startup wait, got %s", control.start.YieldTime)
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
	if control.start.SessionRoot != root {
		t.Fatalf("expected session root %q, got %q", root, control.start.SessionRoot)
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

func TestWriteStdinMissingProcessIDExplainsNoRunningProcesses(t *testing.T) {
	control := &recordingExecControl{}
	_, err := (writeStdinTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		SessionID: "session-1",
		ChatID:    "chat-1",
		Exec:      control,
	}, Request: tools.Request{
		Args: map[string]string{"process_id": "", "chars": ""},
	}})
	if err == nil {
		t.Fatal("expected missing process id error")
	}
	for _, want := range []string{"process_id is empty", "no exec sessions are running in this chat"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestWriteStdinMissingProcessIDListsRunningProcesses(t *testing.T) {
	control := &recordingExecControl{list: []execruntime.Snapshot{{
		ProcessID: "exec_7",
		Command:   "sleep 60",
		State:     execruntime.StateRunning,
	}, {
		ProcessID: "exec_8",
		Command:   "echo done",
		State:     execruntime.StateCompleted,
	}}}
	_, err := (writeStdinTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		SessionID: "session-1",
		ChatID:    "chat-1",
		Exec:      control,
	}, Request: tools.Request{
		Args: map[string]string{"process_id": "", "chars": ""},
	}})
	if err == nil {
		t.Fatal("expected missing process id error")
	}
	for _, want := range []string{"process_id is empty", "running exec sessions in this chat: exec_7 (sleep 60)"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "exec_8") {
		t.Fatalf("did not expect completed process in running guidance, got %v", err)
	}
}

type recordingExecControl struct {
	startState execruntime.State
	start      execruntime.StartRequest
	writeStdin execruntime.WriteStdinRequest
	list       []execruntime.Snapshot
}

func (c *recordingExecControl) Start(_ context.Context, req execruntime.StartRequest) (execruntime.Snapshot, error) {
	c.start = req
	if req.Workdir == "" {
		return execruntime.Snapshot{}, errors.New("workdir is empty")
	}
	state := c.startState
	if state == "" {
		state = execruntime.StateCompleted
	}
	return execruntime.Snapshot{
		ProcessID: "exec_1",
		SessionID: req.SessionID,
		ChatID:    req.ChatID,
		Command:   req.Command,
		Workdir:   req.Workdir,
		State:     state,
	}, nil
}

func (c *recordingExecControl) Status(context.Context, execruntime.StatusRequest) (execruntime.Snapshot, error) {
	return execruntime.Snapshot{}, errors.New("not implemented")
}

func (c *recordingExecControl) List(context.Context, execruntime.ListRequest) ([]execruntime.Snapshot, error) {
	return c.list, nil
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

func TestCommandResultStatesFactsWithoutInstructions(t *testing.T) {
	for _, tc := range []struct {
		state execruntime.State
		want  string
	}{
		{execruntime.StateRunning, "process_id: exec_1\nstate: running (startup wait ended; the command continues in the background)\nreporting: automatic: the exit arrives in this chat as a notification\noutput_mode: tail\noutput:\n(none yet)"},
		{execruntime.StateCompleted, "process_id: exec_1\nstate: completed\noutput_mode: tail\noutput:\n(none)"},
	} {
		control := &recordingExecControl{startState: tc.state}
		result, err := (commandTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
			Workdir:   t.TempDir(),
			SessionID: "session-1",
			ChatID:    "chat-1",
			Exec:      control,
		}, Request: tools.Request{Args: map[string]string{"cmd": "sleep 60"}}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if result.Output != tc.want {
			t.Fatalf("%s output = %q, want %q", tc.state, result.Output, tc.want)
		}
	}
}

func TestCommandNotifyLinesReachesTheRuntime(t *testing.T) {
	control := &recordingExecControl{startState: execruntime.StateRunning}
	args, err := (commandTool{}).NormalizeArgs(map[string]string{"cmd": "tail -f app.log", "notify": "lines", "comment": "errors in app.log"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	result, err := (commandTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		Workdir:   t.TempDir(),
		SessionID: "session-1",
		ChatID:    "chat-1",
		Exec:      control,
	}, Request: tools.Request{Args: args}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if control.start.Notify != execruntime.NotifyLines || control.start.Label != "errors in app.log" {
		t.Fatalf("start request notify=%q label=%q", control.start.Notify, control.start.Label)
	}
	if !strings.Contains(result.Output, "reporting: "+tools.ExecReportingLines) {
		t.Fatalf("output = %q", result.Output)
	}
	if _, err := (commandTool{}).NormalizeArgs(map[string]string{"cmd": "true", "notify": "always"}); err == nil {
		t.Fatal("expected an invalid notify value to be rejected")
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

func TestListShowsOnlyRunningSessionsWithShortCommands(t *testing.T) {
	long := "python3 - <<'PY'\n" + strings.Repeat("print('x')\n", 50) + "PY"
	control := &recordingExecControl{list: []execruntime.Snapshot{
		{ProcessID: "exec_1", Command: "echo done", State: execruntime.StateCompleted},
		{ProcessID: "exec_2", Command: long, State: execruntime.StateRunning},
		{ProcessID: "exec_3", Command: strings.Repeat("a", 300), State: execruntime.StateRunning},
	}}
	result, err := (listTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		SessionID: "session-1", ChatID: "chat-1", Exec: control,
	}, Request: tools.Request{Tool: tools.ExecList, Args: map[string]string{}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "Listed running exec sessions\nexec_2 [running] python3 - <<'PY' …\nexec_3 [running] " + strings.Repeat("a", 120) + " …"
	if result.Output != want {
		t.Fatalf("output:\n%s\nwant:\n%s", result.Output, want)
	}
}
