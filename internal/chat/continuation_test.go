package chat

import (
	"strconv"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestToolLoopTrackerRequiresFullArgsMatch(t *testing.T) {
	var tracker ToolLoopTracker
	calls := []tools.Request{
		{
			Tool: domain.ToolKindExecCommand,
			Args: map[string]string{
				"cmd":        "echo one",
				"comment":    "Run test command",
				"timeout_ms": "60000",
			},
		},
		{
			Tool: domain.ToolKindExecCommand,
			Args: map[string]string{
				"cmd":        "echo two",
				"comment":    "Run test command",
				"timeout_ms": "60000",
			},
		},
		{
			Tool: domain.ToolKindExecCommand,
			Args: map[string]string{
				"cmd":        "echo three",
				"comment":    "Run test command",
				"timeout_ms": "60000",
			},
		},
	}

	for idx, call := range calls {
		action, pause := tracker.TrackCalls([]tools.Request{call})
		if action != ToolLoopAllow {
			t.Fatalf("call %d action = %v, pause = %#v", idx+1, action, pause)
		}
	}
}

func TestToolLoopTrackerDeniesIdenticalFullArgs(t *testing.T) {
	var tracker ToolLoopTracker
	call := tools.Request{
		Tool: domain.ToolKindExecCommand,
		Args: map[string]string{
			"cmd":        "echo one",
			"comment":    "Run test command",
			"timeout_ms": "60000",
		},
	}

	for idx := 1; idx < RepeatedToolLoopThreshold; idx++ {
		action, pause := tracker.TrackCalls([]tools.Request{call})
		if action != ToolLoopAllow {
			t.Fatalf("call %d action = %v, pause = %#v", idx, action, pause)
		}
	}
	action, pause := tracker.TrackCalls([]tools.Request{call})
	if action != ToolLoopDeny {
		t.Fatalf("threshold action = %v, pause = %#v", action, pause)
	}
}

func TestToolLoopTrackerStopsSameToolVariationsAfterDenial(t *testing.T) {
	var tracker ToolLoopTracker
	repeated := tools.Request{
		Tool: domain.ToolKindExecCommand,
		Args: map[string]string{"cmd": "base64 photo.jpg"},
	}
	for idx := 1; idx <= RepeatedToolLoopThreshold; idx++ {
		action, pause := tracker.TrackCalls([]tools.Request{repeated})
		want := ToolLoopAllow
		if idx == RepeatedToolLoopThreshold {
			want = ToolLoopDeny
		}
		if action != want {
			t.Fatalf("identical call %d action = %v, want %v; pause = %#v", idx, action, want, pause)
		}
	}

	for idx := 1; idx <= RepeatedToolRecoveryThreshold; idx++ {
		call := tools.Request{
			Tool: domain.ToolKindExecCommand,
			Args: map[string]string{"cmd": "echo attempt " + strconv.Itoa(idx)},
		}
		action, pause := tracker.TrackCalls([]tools.Request{call})
		want := ToolLoopAllow
		if idx == RepeatedToolRecoveryThreshold {
			want = ToolLoopStop
		}
		if action != want {
			t.Fatalf("recovery call %d action = %v, want %v; pause = %#v", idx, action, want, pause)
		}
		if action == ToolLoopStop && !strings.Contains(pause.Body, "changing input") {
			t.Fatalf("recovery pause body = %q", pause.Body)
		}
	}
}

func TestToolLoopTrackerIgnoresProcessControlBesideRecoveryCall(t *testing.T) {
	var tracker ToolLoopTracker
	repeated := tools.Request{Tool: domain.ToolKindExecCommand, Args: map[string]string{"cmd": "base64 photo.jpg"}}
	for idx := 0; idx < RepeatedToolLoopThreshold; idx++ {
		tracker.TrackCalls([]tools.Request{repeated})
	}

	for idx := 1; idx <= RepeatedToolRecoveryThreshold; idx++ {
		action, pause := tracker.TrackCalls([]tools.Request{
			{
				Tool: domain.ToolKindExecSession,
				Args: map[string]string{"action": "terminate", "process_id": "exec_1"},
			},
			{
				Tool: domain.ToolKindExecCommand,
				Args: map[string]string{"cmd": "echo attempt " + strconv.Itoa(idx)},
			},
		})
		want := ToolLoopAllow
		if idx == RepeatedToolRecoveryThreshold {
			want = ToolLoopStop
		}
		if action != want {
			t.Fatalf("recovery batch %d action = %v, want %v; pause = %#v", idx, action, want, pause)
		}
	}
}

func TestToolLoopTrackerDifferentToolEndsRecovery(t *testing.T) {
	var tracker ToolLoopTracker
	repeated := tools.Request{Tool: domain.ToolKindExecCommand, Args: map[string]string{"cmd": "base64 photo.jpg"}}
	for idx := 0; idx < RepeatedToolLoopThreshold; idx++ {
		tracker.TrackCalls([]tools.Request{repeated})
	}

	action, pause := tracker.TrackCalls([]tools.Request{{
		Tool: domain.ToolKindMCP,
		Args: map[string]string{"server": "exchange", "tool": "exchange_contacts", "arguments_raw": `{"action":"set_photo"}`},
	}})
	if action != ToolLoopAllow || tracker.recoveryTool != "" {
		t.Fatalf("different tool action = %v, pause = %#v, recovery = %q", action, pause, tracker.recoveryTool)
	}
}

func TestToolLoopTrackerCountsEmptyExecWriteStdinWithoutProcessID(t *testing.T) {
	var tracker ToolLoopTracker
	call := tools.Request{
		Tool: domain.ToolKindExecWriteStdin,
		Args: map[string]string{
			"process_id": "",
		},
	}

	for idx := 1; idx < RepeatedToolLoopThreshold; idx++ {
		action, pause := tracker.TrackCalls([]tools.Request{call})
		if action != ToolLoopAllow {
			t.Fatalf("call %d action = %v, pause = %#v", idx, action, pause)
		}
	}
	action, pause := tracker.TrackCalls([]tools.Request{call})
	if action != ToolLoopDeny {
		t.Fatalf("threshold action = %v, pause = %#v", action, pause)
	}
}

func TestToolLoopTrackerIgnoresEmptyExecWriteStdinWithProcessID(t *testing.T) {
	var tracker ToolLoopTracker
	call := tools.Request{
		Tool: domain.ToolKindExecWriteStdin,
		Args: map[string]string{
			"process_id": "exec_1",
		},
	}

	for idx := 0; idx < RepeatedToolLoopThreshold+1; idx++ {
		action, pause := tracker.TrackCalls([]tools.Request{call})
		if action != ToolLoopAllow {
			t.Fatalf("call %d action = %v, pause = %#v", idx+1, action, pause)
		}
	}
}

func TestToolLoopTrackerCountsStoredErroredToolCalls(t *testing.T) {
	var tracker ToolLoopTracker
	call := domain.ToolCall{
		Tool: domain.ToolKindExecWriteStdin,
		Args: map[string]string{
			"process_id": "",
		},
		Status: domain.ToolStatusErrored,
		Error:  &domain.ToolError{Message: "Invalid tool call: process_id is empty"},
	}

	for idx := 1; idx < RepeatedToolLoopThreshold; idx++ {
		action, pause := tracker.TrackToolCalls([]domain.ToolCall{call})
		if action != ToolLoopAllow {
			t.Fatalf("call %d action = %v, pause = %#v", idx, action, pause)
		}
	}
	action, pause := tracker.TrackToolCalls([]domain.ToolCall{call})
	if action != ToolLoopDeny {
		t.Fatalf("threshold action = %v, pause = %#v", action, pause)
	}
}

func TestToolLoopTrackerSnapshotsArgs(t *testing.T) {
	var tracker ToolLoopTracker
	args := map[string]string{"cmd": "echo one"}
	action, pause := tracker.TrackCalls([]tools.Request{{Tool: domain.ToolKindExecCommand, Args: args}})
	if action != ToolLoopAllow {
		t.Fatalf("first action = %v, pause = %#v", action, pause)
	}

	args["cmd"] = "echo two"
	action, pause = tracker.TrackCalls([]tools.Request{{Tool: domain.ToolKindExecCommand, Args: args}})
	if action != ToolLoopAllow {
		t.Fatalf("mutated action = %v, pause = %#v", action, pause)
	}
	if tracker.repeatCount != 1 {
		t.Fatalf("repeat count = %d", tracker.repeatCount)
	}
}
