package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewTimelineIDReturnsUUIDv7(t *testing.T) {
	id := NewTimelineID(time.UnixMilli(0x019aa0000000).UTC())
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("expected uuid format, got %q", id)
	}
	if len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Fatalf("expected uuid group lengths, got %q", id)
	}
	if parts[2][0] != '7' {
		t.Fatalf("expected uuidv7 version nibble, got %q", id)
	}
	switch parts[3][0] {
	case '8', '9', 'a', 'b':
	default:
		t.Fatalf("expected RFC 4122 variant nibble, got %q", id)
	}
}

func TestTimelineItemMarshalRoundTripsLintMessage(t *testing.T) {
	item := TimelineItem{
		ID:      "019aa000-0000-7000-8000-000000000001",
		ChatID:  "019aa000-0000-7000-8000-000000000002",
		Seq:     1,
		Content: LintMessage{Text: "bad.json\n- [syntax error] Line 1: invalid", Files: []string{"bad.json"}},
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"kind":"lint"`) {
		t.Fatalf("expected lint discriminator, got %s", data)
	}
	var decoded TimelineItem
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	lint, ok := decoded.Content.(LintMessage)
	if !ok {
		t.Fatalf("expected lint content, got %#v", decoded.Content)
	}
	if lint.Text != "bad.json\n- [syntax error] Line 1: invalid" || len(lint.Files) != 1 || lint.Files[0] != "bad.json" {
		t.Fatalf("unexpected lint payload: %#v", lint)
	}
}

func TestToolPayloadUnmarshalAcceptsRenamedFileToolKeys(t *testing.T) {
	var part Part
	err := json.Unmarshal([]byte(`{
		"kind": "tool_output",
		"payload": {
			"tool": "glob",
			"tool_call_id": "call_1",
			"status": "ok",
			"text": "matched",
			"result": {"pattern": "*.go", "matches": ["main.go"]}
		}
	}`), &part)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := part.Payload.(ToolOutputPayload)
	if !ok {
		t.Fatalf("expected tool output payload, got %#v", part.Payload)
	}
	if payload.Tool != ToolKindFileGlob {
		t.Fatalf("expected renamed glob tool kind, got %s", payload.Tool)
	}
	if _, ok := payload.Result.(GlobStoredResult); !ok {
		t.Fatalf("expected glob stored result, got %#v", payload.Result)
	}
}

func TestToolCallPayloadUnmarshalAcceptsRenamedFileToolKeys(t *testing.T) {
	var part Part
	err := json.Unmarshal([]byte(`{
		"kind": "tool_call",
		"payload": {
			"tool": "read",
			"tool_call_id": "call_1",
			"args": {"path": "README.md"}
		}
	}`), &part)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := part.Payload.(ToolCallPayload)
	if !ok {
		t.Fatalf("expected tool call payload, got %#v", part.Payload)
	}
	if payload.Tool != ToolKindFileRead {
		t.Fatalf("expected renamed read tool kind, got %s", payload.Tool)
	}
}

func TestToolPayloadUnmarshalIgnoresRemovedToolKeys(t *testing.T) {
	var part Part
	err := json.Unmarshal([]byte(`{
		"kind": "tool_output",
		"payload": {
			"tool": "apply_patch",
			"tool_call_id": "call_1",
			"status": "ok",
			"text": "patched",
			"result": {"summary": "patched"}
		}
	}`), &part)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := part.Payload.(ToolOutputPayload)
	if !ok {
		t.Fatalf("expected tool output payload, got %#v", part.Payload)
	}
	if payload.Tool != 0 {
		t.Fatalf("expected removed tool kind to decode as zero, got %s", payload.Tool)
	}
	if payload.Result != nil {
		t.Fatalf("expected removed tool result to be ignored, got %#v", payload.Result)
	}
}

func TestToolCallUnmarshalAcceptsRenamedFileToolKeys(t *testing.T) {
	var call ToolCall
	err := json.Unmarshal([]byte(`{
		"tool_call_id": "call_1",
		"tool": "grep",
		"status": "done",
		"result": {
			"status": "ok",
			"text": "matched",
			"data": {"pattern": "needle", "output": "main.go:1:needle"}
		}
	}`), &call)
	if err != nil {
		t.Fatal(err)
	}
	if call.Tool != ToolKindFileGrep {
		t.Fatalf("expected renamed grep tool kind, got %s", call.Tool)
	}
	if call.Result == nil {
		t.Fatal("expected result")
	}
	if _, ok := call.Result.Data.(GrepStoredResult); !ok {
		t.Fatalf("expected grep stored result, got %#v", call.Result.Data)
	}
}
