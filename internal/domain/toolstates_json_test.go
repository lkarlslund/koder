package domain

import (
	"encoding/json"
	"testing"
)

func TestToolStatesUnmarshalAcceptsPersistedSnakeCaseKeys(t *testing.T) {
	var states ToolStates
	err := json.Unmarshal([]byte(`{
		"chat_archive": false,
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
		ToolKindChatArchive,
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
