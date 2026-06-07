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

func TestReasoningReplayTextUsesOnlyShorterCaveman(t *testing.T) {
	original := "inspect repository carefully before changing anything"
	short := ReasoningContent{Text: original, Caveman: "me inspect first"}
	if got := short.ReplayText(); got != "me inspect first" {
		t.Fatalf("expected shorter caveman replay, got %q", got)
	}

	long := ReasoningContent{Text: "short thought", Caveman: "me say many many many extra words and make context worse"}
	if got := long.ReplayText(); got != "short thought" {
		t.Fatalf("expected original replay for longer caveman, got %q", got)
	}

	explicit := ReasoningContent{Text: original, Caveman: "same text but marked worse", Tokens: 4, CavemanTokens: 4}
	if got := explicit.ReplayText(); got != original {
		t.Fatalf("expected original replay when caveman is not strictly smaller, got %q", got)
	}
}

func TestToolPayloadUnmarshalAcceptsCurrentFileToolKeys(t *testing.T) {
	var part Part
	err := json.Unmarshal([]byte(`{
		"kind": "tool_output",
		"payload": {
			"tool": "file_glob",
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
		t.Fatalf("expected file_glob tool kind, got %s", payload.Tool)
	}
	if _, ok := payload.Result.(GlobStoredResult); !ok {
		t.Fatalf("expected glob stored result, got %#v", payload.Result)
	}
}

func TestToolPayloadUnmarshalAcceptsLintToolResult(t *testing.T) {
	var part Part
	err := json.Unmarshal([]byte(`{
		"kind": "tool_output",
		"payload": {
			"tool": "lint",
			"tool_call_id": "call_1",
			"status": "ok",
			"text": "diagnostics",
			"result": {
				"path": "bad.json",
				"mode": "auto",
				"summary": "Diagnostics found.",
				"diagnostics": "bad.json:1:1: invalid",
				"diagnostic_report": {
					"diagnostics": [{
						"source": "lint",
						"path": "bad.json",
						"line": 1,
						"column": 1,
						"severity": "error",
						"tool": "json",
						"message": "invalid"
					}]
				}
			}
		}
	}`), &part)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := part.Payload.(ToolOutputPayload)
	if !ok {
		t.Fatalf("expected tool output payload, got %#v", part.Payload)
	}
	result, ok := payload.Result.(LintStoredResult)
	if !ok {
		t.Fatalf("expected lint stored result, got %#v", payload.Result)
	}
	if result.Path != "bad.json" || result.Diagnostics == "" || len(result.DiagnosticReport.Diagnostics) != 1 {
		t.Fatalf("unexpected lint result: %#v", result)
	}
}

func TestToolCallPayloadUnmarshalAcceptsCurrentFileToolKeys(t *testing.T) {
	var part Part
	err := json.Unmarshal([]byte(`{
		"kind": "tool_call",
		"payload": {
			"tool": "file_read",
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
		t.Fatalf("expected file_read tool kind, got %s", payload.Tool)
	}
}

func TestToolCallUnmarshalAcceptsCurrentFileToolKeys(t *testing.T) {
	var call ToolCall
	err := json.Unmarshal([]byte(`{
		"tool_call_id": "call_1",
		"tool": "file_grep",
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
		t.Fatalf("expected file_grep tool kind, got %s", call.Tool)
	}
	if call.Result == nil {
		t.Fatal("expected result")
	}
	if _, ok := call.Result.Data.(GrepStoredResult); !ok {
		t.Fatalf("expected grep stored result, got %#v", call.Result.Data)
	}
}
