package chat

import (
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
