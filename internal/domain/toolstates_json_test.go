package domain

import (
	"encoding/json"
	"testing"
)

func TestToolStatesUnmarshalAcceptsPersistedSnakeCaseKeys(t *testing.T) {
	var states ToolStates
	err := json.Unmarshal([]byte(`{
		"chat_send": false,
		"exec_write_stdin": false,
		"exec_cleanup_background": false,
		"milestone_add_items": false,
		"milestone_plan_and_decompose": false,
		"milestone_update_item": false
	}`), &states)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []ToolKind{
		ToolKindChatSend,
		ToolKindExecWriteStdin,
		ToolKindExecCleanup,
		ToolKindMilestoneAdd,
		ToolKindMilestonePlan,
		ToolKindMilestoneUpdate,
	} {
		if states[kind] {
			t.Fatalf("expected %s to stay disabled: %#v", kind, states)
		}
	}
}

func TestToolStatesUnmarshalAcceptsRenamedFileToolKeys(t *testing.T) {
	var states ToolStates
	err := json.Unmarshal([]byte(`{
		"read": false,
		"write": false,
		"edit": false,
		"grep": false,
		"glob": false,
		"removed_tool": false
	}`), &states)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []ToolKind{
		ToolKindFileRead,
		ToolKindFileWrite,
		ToolKindFileEdit,
		ToolKindFileGrep,
		ToolKindFileGlob,
	} {
		if states[kind] {
			t.Fatalf("expected %s to stay disabled: %#v", kind, states)
		}
	}
	if len(states) != 5 {
		t.Fatalf("expected unknown tool key to be ignored, got %#v", states)
	}
}
