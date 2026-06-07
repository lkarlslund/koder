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
		"exec_cleanup": false,
		"milestone_add": false,
		"milestone_plan": false,
		"milestone_update": false
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

func TestToolStatesUnmarshalAcceptsCurrentFileToolKeys(t *testing.T) {
	var states ToolStates
	err := json.Unmarshal([]byte(`{
		"file_read": false,
		"file_write": false,
		"file_edit": false,
		"file_grep": false,
		"file_glob": false,
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
